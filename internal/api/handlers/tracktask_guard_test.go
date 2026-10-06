package handlers

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// This file is the Phase 3 enforcement layer for Proxmox task tracking. It uses
// static analysis (go/ast) — no database, no running server — so it executes as
// part of the normal `go test ./internal/api/handlers/...` run and fails CI the
// moment a handler diverges from the canonical task-tracking / audit path.
//
// See TASK_TRACKING_RFC.md §9 and CLAUDE.md ("Dispatching a Proxmox task").

// upidMethods are the proxmox *Client methods that dispatch a Proxmox task and
// return its UPID (signature `(string, error)` where the string is the UPID).
//
// Any handler that CAPTURES the UPID from one of these MUST record it via
// handlers.TrackTask — enforced by TestGuard_AllUPIDDispatchersTrackTask.
//
// Keep this in sync with internal/proxmox: TestGuard_UPIDMethodListInSync fails
// if a new `(string, error)` *Client method appears that is classified in
// neither this set nor nonUPIDStringMethods.
var upidMethods = map[string]bool{
	// Guests (internal/proxmox/client_guests.go)
	"StartVM": true, "StopVM": true, "ShutdownVM": true, "RebootVM": true,
	"ResetVM": true, "SuspendVM": true, "ResumeVM": true, "CloneVM": true,
	"DestroyVM": true, "ConvertVMToTemplate": true,
	"StartCT": true, "StopCT": true, "ShutdownCT": true, "RebootCT": true,
	"SuspendCT": true, "ResumeCT": true, "CloneCT": true, "DestroyCT": true,
	"ConvertCTToTemplate": true,
	"MigrateCT":           true, "MigrateVM": true, "MoveDisk": true, "MoveCTVolume": true,
	"RestoreVM": true, "RestoreCT": true,
	"CreateVMSnapshot": true, "DeleteVMSnapshot": true, "RollbackVMSnapshot": true,
	"CreateCTSnapshot": true, "DeleteCTSnapshot": true, "RollbackCTSnapshot": true,
	"CreateVM": true, "CreateCT": true,
	"RemoteMigrateVM": true, "RemoteMigrateCT": true,

	// Nodes (internal/proxmox/client_nodes.go)
	"CreateNodeZFSPool": true, "DeleteNodeZFSPool": true,
	"CreateNodeLVM": true, "DeleteNodeLVM": true,
	"CreateNodeLVMThin": true, "DeleteNodeLVMThin": true,
	"CreateNodeDirectory": true, "InitializeGPT": true, "WipeDisk": true,
	"MigrateAllGuests": true, "ServiceAction": true, "RefreshNodeAptIndex": true,
	"OrderNodeCertificate": true, "RenewNodeCertificate": true, "RevokeNodeCertificate": true,

	// Ceph (internal/proxmox/client_storage.go). Note the OSD in/out mon
	// commands are NOT here: they return no UPID, so their handlers AuditLog.
	"CephServiceAction": true,
	"CreateCephPool":    true, "DeleteCephPool": true,

	// Replication / storage / backup / acme
	"TriggerReplication":   true,
	"UploadToStorage":      true,
	"DeleteStorageContent": true,
	"PullOCIImage":         true,
	"DownloadURLToStorage": true,
	"DownloadAppliance":    true,
	"TriggerBackup":        true,
	"CreateACMEAccount":    true,

	// RunBackupJob is the one entry that does not return (string, error): it
	// starts a vzdump task on each of several nodes and returns a
	// *BackupJobRun holding every UPID. It stays listed because this set is
	// what the per-function rule keys on, so the handler that calls it must
	// still TrackTask — once per task, which the rule cannot count and
	// TestRunBackupJobTracksEveryTaskOnItsOwnNode (internal/api) asserts.
	// TestGuard_UPIDMethodListInSync cannot see a method of this shape, so a
	// second multi-UPID method has to be added here by hand.
	"RunBackupJob": true,
}

// nonUPIDStringMethods are proxmox *Client methods that return `(string, error)`
// but whose string is NOT a task UPID, so handlers need not TrackTask them. They
// exist only to keep TestGuard_UPIDMethodListInSync exhaustive.
var nonUPIDStringMethods = map[string]bool{
	"GetNodeAptChangelog": true, // returns package changelog text
	"GetACMETOS":          true, // returns the ACME Terms-of-Service URL
	"GetNodeReport":       true, // returns the pvereport bundle text, generated synchronously

	// returns the digest of a node's config file, read for the save check
	"GetNodeConfigDigest": true,
}

// parseGoFiles parses every non-test .go file in dir into *ast.File. It uses
// Glob + ParseFile (not the deprecated parser.ParseDir/ast.Package) so it stays
// clean under staticcheck SA1019. Each file is parsed once per test binary, with
// positions in the one shared FileSet; callers only read the trees.
func parseGoFiles(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	var files []*ast.File
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		f, err := guardParsed(m)
		if err != nil {
			t.Fatalf("parse %s: %v", m, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatalf("no source files parsed in %s", dir)
	}
	return guardFset, files
}

// guardParsed is path parsed once per test binary, positions in guardFset. About
// thirty guards read this package's source, test files included; each used to parse
// all of it again.
func guardParsed(path string) (*ast.File, error) {
	v, _ := guardParsedFiles.LoadOrStore(path, &guardParsedFile{})
	e := v.(*guardParsedFile)
	e.once.Do(func() { e.file, e.err = parser.ParseFile(guardFset, path, nil, parser.SkipObjectResolution) })
	return e.file, e.err
}

// guardEach runs fn over paths, a few at a time, and returns the first error in path
// order: the whole-tree scans parse hundreds of files and are most of their own time.
func guardEach(paths []string, fn func(i int, path string) error) error {
	errs := make([]error, len(paths))
	var wg sync.WaitGroup
	workers := make(chan struct{}, min(runtime.GOMAXPROCS(0), 8))
	for i, path := range paths {
		wg.Add(1)
		workers <- struct{}{}
		go func() {
			defer func() { <-workers; wg.Done() }()
			errs[i] = fn(i, path)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

type guardParsedFile struct {
	once sync.Once
	file *ast.File
	err  error
}

var (
	guardFset        = token.NewFileSet()
	guardParsedFiles sync.Map // path → *guardParsedFile
)

// callName returns the bare identifier of a call's function, handling both
// `Foo(...)` (ast.Ident) and `pkg.Foo(...)` / `x.Foo(...)` (ast.SelectorExpr).
func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

// TestGuard_AllUPIDDispatchersTrackTask enforces the core rule: any function in package handlers that
// dispatches a Proxmox task (calls a known UPID-returning client method) must also call TrackTask in
// the same function. "Dispatches" means ANY call to a upidMethod, captured, passed, returned or
// discarded with `_, _ =`: discarding the UPID is not a way out, since a destructive op (DestroyVM,
// WipeDisk, ...) could then skip both the audit log and task_history and pass CI. The only escape is the
// documented exempt list below. By design the check is per-function, not per-UPID: a function that
// dispatches several tasks is satisfied by one TrackTask.
func TestGuard_AllUPIDDispatchersTrackTask(t *testing.T) {
	fset, files := parseGoFiles(t, ".")

	// Functions that dispatch a UPID but legitimately must not TrackTask. Keep
	// this tiny and documented — it is the ONLY sanctioned way to skip tracking.
	exempt := map[string]string{
		// convertCloneToTemplate (VMHandler + ContainerHandler) runs in the
		// background after a clone whose own task the Clone handler already
		// tracks; it converts the fresh copy to a template and intentionally
		// discards that secondary UPID.
		"convertCloneToTemplate": "background template conversion of an already-tracked clone",
	}

	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var dispatchesUPID, callsTrackTask bool
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch name := callName(call); {
				case upidMethods[name]:
					dispatchesUPID = true
				case name == "TrackTask":
					callsTrackTask = true
				}
				return true
			})
			if !dispatchesUPID || callsTrackTask {
				continue
			}
			if _, ok := exempt[fn.Name.Name]; ok {
				continue
			}
			pos := fset.Position(fn.Pos())
			t.Errorf("%s: %q dispatches a Proxmox task (UPID-returning client call) but never "+
				"calls TrackTask.\n\tRecord it via handlers.TrackTask. If it is an intentional "+
				"fire-and-forget secondary op, add the function to the documented exempt list in "+
				"this test.", pos, fn.Name.Name)
		}
	}
}

// sanctionedAuditEntryPoints are the only functions permitted to carry an
// audit-log name. AuditLog reads the actor from the request; AuditLogAs takes it
// explicitly, for authentication events audited where c.Locals("user_id") is not
// set. Both live in common.go and share one body.
var sanctionedAuditEntryPoints = map[string]bool{
	"AuditLog":   true,
	"AuditLogAs": true,
}

// TestGuard_NoHandlerAuditLogWrappers enforces the "empty allowlist": exactly one audit path, the shared
// helpers in common.go; per-handler wrappers diverged in signature and forked it. The match is on the
// name containing "auditlog" anywhere, case-insensitively: the old "auditLog" prefix rule let
// `authAuditLog` through for the life of the syslog feature, writing its row with a direct
// InsertAuditLog that never reached the WS event or the syslog forwarder, so `login`, `logout` and
// `password_changed`, the events a SIEM is deployed to collect, were never forwarded. parseGoFiles
// skips _test.go, so this test's own name does not match itself.
func TestGuard_NoHandlerAuditLogWrappers(t *testing.T) {
	fset, files := parseGoFiles(t, ".")
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if sanctionedAuditEntryPoints[fn.Name.Name] {
				continue
			}
			if !strings.Contains(strings.ToLower(fn.Name.Name), "auditlog") {
				continue
			}
			pos := fset.Position(fn.Pos())
			t.Errorf("%s: %q is a per-handler audit wrapper — call the shared handlers.AuditLog, "+
				"handlers.AuditLogAs (when the actor cannot be read from the request), or "+
				"handlers.TrackTask (for UPID-bearing tasks) directly instead.\n"+
				"\tA wrapper that inserts its own row skips the audit_entry event and the syslog "+
				"forward, which is how auth events went unforwarded.", pos, fn.Name.Name)
		}
	}
}

// TestGuard_UPIDMethodListInSync is the drift guard: it parses internal/proxmox
// and asserts every exported *Client method returning `(string, error)` is
// classified in upidMethods or nonUPIDStringMethods. A newly-added dispatch
// method that nobody classified fails here — forcing the next author to decide
// whether handlers must TrackTask it, instead of silently skipping tracking
// (the exact gap that let acme.go and node-evacuate slip through Phase 2).
func TestGuard_UPIDMethodListInSync(t *testing.T) {
	_, files := parseGoFiles(t, filepath.FromSlash("../../proxmox"))
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || !fn.Name.IsExported() {
				continue
			}
			if !isClientReceiver(fn.Recv) || !returnsStringError(fn.Type) {
				continue
			}
			name := fn.Name.Name
			if upidMethods[name] || nonUPIDStringMethods[name] {
				continue
			}
			t.Errorf("proxmox.*Client.%s returns (string, error) but is classified in neither "+
				"upidMethods nor nonUPIDStringMethods (tracktask_guard_test.go).\n"+
				"\tIf it returns a task UPID, add it to upidMethods and TrackTask it in the handler; "+
				"otherwise add it to nonUPIDStringMethods.", name)
		}
	}
}

// isClientReceiver reports whether a method's receiver is Client or *Client.
func isClientReceiver(recv *ast.FieldList) bool {
	if recv == nil || len(recv.List) == 0 {
		return false
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "Client"
}

// returnsStringError reports whether a function's results are exactly
// (string, error), covering both unnamed `(string, error)` and named
// `(upid string, err error)` result lists.
func returnsStringError(ft *ast.FuncType) bool {
	if ft.Results == nil || len(ft.Results.List) != 2 {
		return false
	}
	isIdent := func(e ast.Expr, name string) bool {
		id, ok := e.(*ast.Ident)
		return ok && id.Name == name
	}
	return isIdent(ft.Results.List[0].Type, "string") && isIdent(ft.Results.List[1].Type, "error")
}
