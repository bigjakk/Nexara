package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
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

	// Replication / storage / backup / acme
	"TriggerReplication":   true,
	"UploadToStorage":      true,
	"DeleteStorageContent": true,
	"PullOCIImage":         true,
	"DownloadURLToStorage": true,
	"DownloadAppliance":    true,
	"TriggerBackup":        true,
	"RunBackupJob":         true,
	"CreateACMEAccount":    true,
}

// nonUPIDStringMethods are proxmox *Client methods that return `(string, error)`
// but whose string is NOT a task UPID, so handlers need not TrackTask them. They
// exist only to keep TestGuard_UPIDMethodListInSync exhaustive.
var nonUPIDStringMethods = map[string]bool{
	"GetNodeAptChangelog": true, // returns package changelog text
	"GetACMETOS":          true, // returns the ACME Terms-of-Service URL
}

// parseGoFiles parses every non-test .go file in dir into *ast.File. It uses
// Glob + ParseFile (not the deprecated parser.ParseDir/ast.Package) so it stays
// clean under staticcheck SA1019.
func parseGoFiles(t *testing.T, dir string) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	var files []*ast.File
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, m, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", m, err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatalf("no source files parsed in %s", dir)
	}
	return fset, files
}

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

// TestGuard_AllUPIDDispatchersTrackTask enforces the core rule: any function in
// package handlers that dispatches a Proxmox task (calls a known UPID-returning
// client method) must also call TrackTask somewhere in the same function (the
// RFC's per-function rule).
//
// "Dispatches" deliberately means ANY call to a upidMethod — captured into a
// variable, passed as an argument, returned, OR discarded with `_, _ =`. Merely
// discarding the UPID is NOT a way out: that would let a destructive op
// (DestroyVM, WipeDisk, …) skip both the audit log and task_history while still
// passing CI. The only sanctioned escape is the explicit, documented exempt list
// below, so a new untracked dispatch fails CI until someone justifies it here.
//
// Limitation (by design): the check is per-function, not per-UPID — a function
// that dispatches several tasks satisfies the rule with a single TrackTask.
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

// TestGuard_NoHandlerAuditLogWrappers enforces the "empty allowlist": there must
// be exactly one audit entry point, the shared handlers.AuditLog. Per-handler
// auditLog/auditLogGlobal/auditLogDetails wrappers (which historically diverged
// in signature and risked forking the audit path) are banned. Call AuditLog —
// or TrackTask for UPID-bearing tasks — directly.
func TestGuard_NoHandlerAuditLogWrappers(t *testing.T) {
	fset, files := parseGoFiles(t, ".")
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			// Ban the lowercase auditLog* family; the exported shared AuditLog
			// (capital A) is the sanctioned entry point and is unaffected.
			if strings.HasPrefix(fn.Name.Name, "auditLog") {
				pos := fset.Position(fn.Pos())
				t.Errorf("%s: %q is a per-handler audit wrapper — call the shared handlers.AuditLog "+
					"(or handlers.TrackTask for UPID-bearing tasks) directly instead.", pos, fn.Name.Name)
			}
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
