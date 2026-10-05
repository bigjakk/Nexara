package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// epochGuardGoFiles returns the non-test Go sources under root that are this
// checkout's code, as slash paths relative to root. It skips generated code, vendored
// trees, node_modules and — the part that matters — anything that is a copy of the
// tree rather than part of it: a directory named .claude, and any directory that is
// the root of a checkout of its own (it carries a .git entry; a worktree's is a
// file). A gitignored agent worktree under .claude/worktrees/<name>/ holds a full
// copy of these sources at some other commit, and a walk that counted it twice made
// a guard that passes in CI fail on a developer's machine — the same class
// credential_render_guard_test.go and scope_params_guard_test.go skip.
func epochGuardGoFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "node_modules", "vendor", "generated", "frontend":
				return filepath.SkipDir
			}
			if filepath.Clean(path) != filepath.Clean(root) {
				if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(files)
	return files
}

// TestEpochGuardWalkSkipsNestedCopies pins the walk's exclusions on a tree built for
// the purpose: a copy under .claude/worktrees (by name), a nested checkout elsewhere
// (by its .git file), generated and vendored code, and test files are all left out;
// the real sources are kept.
func TestEpochGuardWalkSkipsNestedCopies(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("internal/api/handlers/auth.go", "package handlers\n")
	write("internal/api/handlers/auth_test.go", "package handlers\n")
	write("internal/db/generated/models.go", "package db\n")
	write("vendor/x/x.go", "package x\n")
	write(".claude/worktrees/old/internal/api/handlers/auth.go", "package handlers\n")
	write("elsewhere/checkout/.git", "gitdir: /somewhere\n")
	write("elsewhere/checkout/internal/api/handlers/auth.go", "package handlers\n")

	got := epochGuardGoFiles(t, root)
	if want := []string{"internal/api/handlers/auth.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("walk = %v, want %v: a copy of the tree was counted, or the real source was skipped", got, want)
	}
}

// epochCallSite is one call, or one non-call reference, found in the source.
type epochCallSite struct {
	where  string // "path:EnclosingFunc"
	callee string
	forms  []string // the epoch argument of each call, in source order; nil for a reference
}

// TestGuard_TheSessionsEpochIsAlwaysTheOneTheCheckRead reads the whole source tree
// for the property the behavioural tests prove by driving each path: wherever a
// session is created, the epoch handed to the insert is the one the credential check
// read — and no function that re-reads the user supplies one from the row it re-read.
//
// It holds three things, each in a table that a new call site fails until someone has
// read it:
//
//   - Every CALL of issueTokens, IssueTokens, issueOrTOTP, CreateTOTPPendingToken and
//     SessionManager.CreateSession anywhere in the tree (not only in the handlers that
//     exist today: a new handler calling IssueTokens is exactly what this is for) has
//     its epoch argument in an allowed form for that call site: Login passes the epoch
//     of the very row it hands to issueOrTOTP (a password check's, a directory
//     login's); OIDCTokenExchange passes the epoch the exchange code carried;
//     issueOrTOTP and issueTokens pass their own `epoch` parameter on; Register passes
//     the account it just created; and VerifyLogin passes exactly `*pending.AuthEpoch`,
//     the password step's.
//   - Every NON-CALL reference to those five names (a method value handed to
//     SetIssueTokensFn, a nil check on the function field) is one of the three that
//     exist. `create := h.sessionManager.CreateSession; create(…)` and
//     `mk := h.totpHandler.CreateTOTPPendingToken` are references too: the call
//     through the alias is one the table never sees, so the reference is what is held.
//   - No function that only passes an epoch on reads one off a user. VerifyLogin is
//     the exception that needs a sentence: it re-reads the user, and it may read that
//     row's AuthEpoch ONLY in the comparison with `*pending.AuthEpoch` that refuses a
//     pending token the account has outgrown — never as the value the session is
//     created against. issueTokens, issueOrTOTP, OIDCTokenExchange and
//     CreateTOTPPendingToken read none at all.
//
// With the early comparison in place the carried epoch and the re-read one are equal
// whenever VerifyLogin reaches the insert, so passing `user.AuthEpoch` there is
// BEHAVIOURALLY EQUIVALENT to passing `*pending.AuthEpoch`: no test that drives a
// request can tell them apart. This guard is what kills that mutant, which is the
// reason it holds the argument's exact form rather than its behaviour.
//
// What it does NOT see, so that nobody reads it as more than it is:
//
//   - The SSO hand-off is pinned by the VARIABLE, not by the read point.
//     provisionAndStoreExchange must call storeExchange with `user`, and Callback must
//     get its code from there; but a GetUserByID re-read assigned to `user` between
//     the provisioning and the store would pass. The window that opens is the role
//     sync's — the statements between the provisioning and the store — and what covers
//     the value itself is TestOIDCCallback_TheExchangeCodeCarriesTheEpochOfTheProvisionedUser,
//     which drives provisionAndStoreExchange for every provisioning outcome.
//   - It reads Go SYNTAX, by name. A call made through reflect, a function reached by
//     an interface that this table does not name, or code in a directory the walk
//     skips by name (generated, vendor, frontend, .claude) is not seen. The first two
//     are for review; the third is code that is not part of this checkout's handlers.
func TestGuard_TheSessionsEpochIsAlwaysTheOneTheCheckRead(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	files := epochGuardGoFiles(t, root)
	if len(files) < 100 {
		t.Fatalf("scanned %d Go files, want the whole tree: the walk is not reaching the code it guards", len(files))
	}

	callees := map[string]bool{
		"issueTokens": true, "IssueTokens": true, "issueOrTOTP": true,
		"CreateTOTPPendingToken": true, "CreateSession": true,
	}
	// The two OIDC hand-offs that no test can drive from the outside, because Callback
	// needs an identity provider: Callback calls provisionAndStoreExchange (which a test
	// drives), and provisionAndStoreExchange calls storeExchange with the user it
	// provisioned. Keyed "path:EnclosingFunc:callee", valued by the call's arguments.
	handOffNames := map[string]bool{"provisionAndStoreExchange": true, "storeExchange": true}
	handOffs := map[string][]string{}
	var calls, refs []epochCallSite
	readers := map[string][]string{} // "path:Func" -> the X of every X.AuthEpoch in it
	userReads := map[string]int{}    // "path:Func" -> user.AuthEpoch reads outside the permitted comparison

	for _, rel := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", rel, err)
		}
		visit := func(fn string, node ast.Node) {
			where := rel + ":" + fn
			isCallFun := map[ast.Node]bool{}
			// The one permitted read of user.AuthEpoch: an operand of `!=` whose other
			// operand is *pending.AuthEpoch.
			permitted := map[ast.Node]bool{}
			ast.Inspect(node, func(n ast.Node) bool {
				if be, ok := n.(*ast.BinaryExpr); ok && be.Op == token.NEQ {
					for _, pair := range [][2]ast.Expr{{be.X, be.Y}, {be.Y, be.X}} {
						if types.ExprString(pair[0]) == "user.AuthEpoch" && types.ExprString(pair[1]) == "*pending.AuthEpoch" {
							permitted[pair[0]] = true
						}
					}
				}
				return true
			})
			ast.Inspect(node, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.CallExpr:
					var name string
					switch f := x.Fun.(type) {
					case *ast.SelectorExpr:
						name = f.Sel.Name
						isCallFun[f] = true
					case *ast.Ident:
						name = f.Name
					}
					if handOffNames[name] {
						args := make([]string, 0, len(x.Args))
						for _, a := range x.Args {
							args = append(args, types.ExprString(a))
						}
						handOffs[where+":"+name] = append(handOffs[where+":"+name], strings.Join(args, ", "))
					}
					if callees[name] {
						form := "<missing>"
						if len(x.Args) > 2 {
							form = types.ExprString(x.Args[2])
						}
						calls = append(calls, epochCallSite{where: where, callee: name, forms: []string{form}})
						// Login's calls also pair the epoch with the user argument.
						if name == "issueOrTOTP" && fn == "Login" && len(x.Args) > 2 &&
							types.ExprString(x.Args[2]) != types.ExprString(x.Args[1])+".AuthEpoch" {
							t.Errorf("%s: issueOrTOTP(%s, %s, …) — the epoch must be the AuthEpoch of the user it is passed with",
								where, types.ExprString(x.Args[1]), types.ExprString(x.Args[2]))
						}
					}
				case *ast.SelectorExpr:
					switch x.Sel.Name {
					case "issueTokens", "IssueTokens", "issueOrTOTP", "CreateSession", "CreateTOTPPendingToken":
						if !isCallFun[x] {
							refs = append(refs, epochCallSite{where: where, callee: x.Sel.Name})
						}
					case "AuthEpoch":
						who := "?"
						if id, ok := x.X.(*ast.Ident); ok {
							who = id.Name
						}
						readers[where] = append(readers[where], who)
						if who == "user" && !permitted[x] {
							userReads[where]++
						}
					}
				}
				return true
			})
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					visit(d.Name.Name, d.Body)
				}
			case *ast.GenDecl:
				visit("", d) // a package-level initializer
			}
		}
	}

	t.Run("every call passes its epoch in the form that call site is allowed", func(t *testing.T) {
		want := map[string][]string{
			"internal/api/handlers/auth.go:Login:issueOrTOTP":                  {"ldapUser.AuthEpoch", "ldapUser.AuthEpoch", "user.AuthEpoch"},
			"internal/api/handlers/auth.go:OIDCTokenExchange:issueOrTOTP":      {"epoch"},
			"internal/api/handlers/auth.go:issueOrTOTP:CreateTOTPPendingToken": {"epoch"},
			"internal/api/handlers/auth.go:issueOrTOTP:issueTokens":            {"epoch"},
			"internal/api/handlers/auth.go:IssueTokens:issueTokens":            {"epoch"},
			"internal/api/handlers/auth.go:issueTokens:CreateSession":          {"epoch"},
			"internal/api/handlers/auth.go:Register:CreateSession":             {"user.AuthEpoch"},
			"internal/api/handlers/totp.go:VerifyLogin:issueTokens":            {"*pending.AuthEpoch"},
		}
		got := map[string][]string{}
		for _, c := range calls {
			key := c.where + ":" + c.callee
			got[key] = append(got[key], c.forms...)
		}
		for key, forms := range want {
			if !reflect.DeepEqual(got[key], forms) {
				t.Errorf("%s passes the epoch as %v, want %v", key, got[key], forms)
			}
		}
		for key, forms := range got {
			if _, ok := want[key]; !ok {
				t.Errorf("%s calls with epoch %v and is not in the table: a new place that creates or defers a session "+
					"must say where its epoch came from, and be added here once someone has read it", key, forms)
			}
		}
	})

	t.Run("the references that are not calls are the ones that exist", func(t *testing.T) {
		want := map[string]int{
			"internal/api/server.go:wireAuthCompositions:IssueTokens":    1, // the method value handed to SetIssueTokensFn
			"internal/api/handlers/totp.go:SetIssueTokensFn:issueTokens": 1, // the field it fills
			"internal/api/handlers/totp.go:VerifyLogin:issueTokens":      1, // its nil check
		}
		got := map[string]int{}
		for _, r := range refs {
			got[r.where+":"+r.callee]++
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("non-call references = %v, want %v: a function value that reaches IssueTokens, CreateSession or "+
				"CreateTOTPPendingToken can be called with any epoch, and the call through it is not one this guard sees", got, want)
		}
	})

	t.Run("the functions that only pass an epoch on never read one off a user", func(t *testing.T) {
		for _, fn := range []string{"issueTokens", "IssueTokens", "issueOrTOTP", "OIDCTokenExchange", "CreateTOTPPendingToken", "VerifyLogin"} {
			where := "internal/api/handlers/auth.go:" + fn
			if fn == "VerifyLogin" || fn == "CreateTOTPPendingToken" {
				where = "internal/api/handlers/totp.go:" + fn
			}
			if _, ok := readers[where]; !ok && fn == "VerifyLogin" {
				t.Fatalf("VerifyLogin reads no AuthEpoch at all: the guard no longer finds what it checks")
			}
			if n := userReads[where]; n != 0 {
				t.Errorf("%s reads user.AuthEpoch %d time(s) outside the one comparison with *pending.AuthEpoch: "+
					"it must use the epoch it is handed or carries (the credential check's), never the epoch of a user row it holds", where, n)
			}
		}
		// Apart from VerifyLogin's comparison, none of them reads an AuthEpoch of any
		// kind off anything but the pending token.
		for _, fn := range []string{"issueTokens", "IssueTokens", "issueOrTOTP", "OIDCTokenExchange"} {
			where := "internal/api/handlers/auth.go:" + fn
			if r := readers[where]; len(r) != 0 {
				t.Errorf("%s reads .AuthEpoch of %v", where, r)
			}
		}
		if r := readers["internal/api/handlers/totp.go:CreateTOTPPendingToken"]; len(r) != 0 {
			t.Errorf("CreateTOTPPendingToken reads .AuthEpoch of %v", r)
		}
		// VerifyLogin: pending.AuthEpoch (the carried one) and the user's, in the comparison.
		for _, who := range readers["internal/api/handlers/totp.go:VerifyLogin"] {
			if who != "pending" && who != "user" {
				t.Errorf("VerifyLogin reads .AuthEpoch of %q", who)
			}
		}
	})

	t.Run("only the functions that hand an epoch on touch an AuthEpoch at all", func(t *testing.T) {
		want := map[string][]string{
			"internal/api/handlers/auth.go:Login":         {"ldapUser", "ldapUser", "user"},
			"internal/api/handlers/auth.go:Register":      {"user"},
			"internal/api/handlers/oidc.go:storeExchange": {"user"},
			"internal/api/handlers/totp.go:VerifyLogin":   {"pending", "pending", "pending", "user"},
		}
		got := map[string][]string{}
		for where, who := range readers {
			got[where] = append([]string(nil), who...)
			sort.Strings(got[where])
		}
		for where := range want {
			sort.Strings(want[where])
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("AuthEpoch is read or written in %v, want exactly %v: a function that is not in the table has no business "+
				"with an epoch — tryLDAPLogin returning a user it had rebuilt, a callback recording a constant — and the "+
				"ones that are, hand it on as the credential check read it", got, want)
		}
	})

	t.Run("tryLDAPLogin returns what provisionLDAPUser returned, as it is", func(t *testing.T) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, "internal", "api", "handlers", "auth.go"), nil, 0)
		if err != nil {
			t.Fatalf("parse auth.go: %v", err)
		}
		var found bool
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "tryLDAPLogin" || fn.Body == nil {
				continue
			}
			found = true
			last := fn.Body.List[len(fn.Body.List)-1]
			ret, ok := last.(*ast.ReturnStmt)
			if !ok || len(ret.Results) != 1 {
				t.Fatalf("tryLDAPLogin does not end in `return h.provisionLDAPUser(…)`")
			}
			call, ok := ret.Results[0].(*ast.CallExpr)
			if !ok {
				t.Fatalf("tryLDAPLogin's last return is not a call")
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "provisionLDAPUser" {
				t.Errorf("tryLDAPLogin ends by returning %s, want provisionLDAPUser's own answer, unchanged", types.ExprString(call.Fun))
			}
		}
		if !found {
			t.Fatal("tryLDAPLogin not found: the guard no longer finds what it checks")
		}
	})

	t.Run("the SSO exchange code records the epoch of the user it is for", func(t *testing.T) {
		if len(readers["internal/api/handlers/oidc.go:storeExchange"]) == 0 {
			t.Error("storeExchange never reads user.AuthEpoch: the code would carry nothing for the exchange to create its session against")
		}
	})

	// Callback itself needs an identity provider, so nothing drives it. What a test
	// does drive is provisionAndStoreExchange, and what makes that cover the callback
	// is that Callback gets its code from it and that nothing else stores a code. A
	// Callback that stored its own — `h.storeExchange(c.Context(), db.User{ID: user.ID})`,
	// a user rebuilt from its id and so at epoch 0 — would pass every behavioural test
	// and refuse the SSO sign-in of any account whose epoch has ever moved.
	t.Run("the SSO exchange code is stored only from the provisioning, for the user it returned", func(t *testing.T) {
		want := map[string][]string{
			"internal/api/handlers/oidc.go:Callback:provisionAndStoreExchange":      {"c, cfg, userInfo"},
			"internal/api/handlers/oidc.go:provisionAndStoreExchange:storeExchange": {"c.Context(), user"},
		}
		if !reflect.DeepEqual(handOffs, want) {
			t.Errorf("OIDC hand-offs = %v, want %v: the exchange code must be stored by provisionAndStoreExchange, "+
				"for the user provisionUser returned, and Callback must get it from there", handOffs, want)
		}
	})
}

// TestRevokeAllPaths_BumpTheEpochFirstAndInTheirOwnTransaction proves, against the
// handlers themselves, that the two revoke-alls that live in auth.go go through the
// bumping code, in the right place: a password change inside its transaction, after
// the update and before the listing and the revoke; and a sign-out of all devices on
// the pool, as the first of its three statements. A handler that ended the sessions
// through the raw revoke would pass every older test of it — the sessions are
// revoked — and leave a sign-in that was in flight free to mint a live one.
//
// The deactivation, the third, is pinned in users_deactivate_test.go.
func TestRevokeAllPaths_BumpTheEpochFirstAndInTheirOwnTransaction(t *testing.T) {
	order := func(a *authRaceApp, names ...string) (positions []int, inTx []bool) {
		t.Helper()
		stmts := a.store.statements()
		for _, want := range names {
			at := -1
			for i, st := range stmts {
				if st.name == want {
					at = i
					inTx = append(inTx, st.inTx)
					break
				}
			}
			if at < 0 {
				t.Fatalf("no %s statement was sent; statements: %v", want, stmts)
			}
			positions = append(positions, at)
		}
		return positions, inTx
	}
	increasing := func(p []int) bool {
		for i := 1; i < len(p); i++ {
			if p[i] <= p[i-1] {
				return false
			}
		}
		return true
	}

	t.Run("a password change", func(t *testing.T) {
		a := newAuthRaceApp(t, withPasswordHash(t, nil))
		before := a.store.snapshotUser().AuthEpoch

		resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}

		pos, inTx := order(a, "UpdatePassword", "BumpUserAuthEpoch", "ListUserSessions", "RevokeAllUserSessions")
		if !increasing(pos) {
			t.Errorf("statements ran at positions %v, want the update, the bump, the listing and the revoke in that order", pos)
		}
		if !reflect.DeepEqual(inTx, []bool{true, true, true, true}) {
			t.Errorf("in-transaction flags = %v: the bump must be in the same transaction as the change and the revoke, so that it commits or rolls back with them", inTx)
		}
		if got := a.store.snapshotUser().AuthEpoch; got != before+1 {
			t.Errorf("the epoch is %d after the change, want %d", got, before+1)
		}
	})

	t.Run("a sign-out of all devices", func(t *testing.T) {
		a := newAuthRaceApp(t, nil)
		before := a.store.snapshotUser().AuthEpoch

		resp, _ := a.postAs(t, "/auth/logout-all", "{}", 20*time.Second)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", resp.StatusCode)
		}

		pos, inTx := order(a, "BumpUserAuthEpoch", "ListUserSessions", "RevokeAllUserSessions")
		if !increasing(pos) {
			t.Errorf("statements ran at positions %v, want the bump, the listing and the revoke in that order", pos)
		}
		if !reflect.DeepEqual(inTx, []bool{false, false, false}) {
			t.Errorf("in-transaction flags = %v: a sign-out of all devices runs on the pool, as three statements", inTx)
		}
		if got := a.store.snapshotUser().AuthEpoch; got != before+1 {
			t.Errorf("the epoch is %d after the sign-out, want %d", got, before+1)
		}
	})

	t.Run("a password change that rolls back leaves the epoch where it was", func(t *testing.T) {
		a := newAuthRaceApp(t, withPasswordHash(t, func(s *raceStore) { s.revokeAllErr = errRaceTransient }))
		before := a.store.snapshotUser().AuthEpoch

		resp, _ := a.postAs(t, "/auth/change-password", changeBody, 120*time.Second)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", resp.StatusCode)
		}
		if got := a.store.snapshotUser().AuthEpoch; got != before {
			t.Errorf("the epoch is %d after a change that did not happen, want %d: the bump must roll back with the change", got, before)
		}
	})
}
