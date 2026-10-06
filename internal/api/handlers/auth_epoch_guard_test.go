package handlers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// authSource is a non-test Go file of the repository, by slash path relative to it: its
// text, and its syntax without comments.
type authSource struct {
	rel  string
	text []byte
	file *ast.File
}

var (
	authSourcesOnce sync.Once
	authSourcesFset *token.FileSet
	authSourcesAll  []authSource
	authSourcesErr  error
)

// authParsedSources is every non-test Go source of the repository (goSourceFiles: generated
// code and nested checkouts left out), parsed once per test binary for the guards that
// read them.
func authParsedSources(t *testing.T) (*token.FileSet, []authSource) {
	t.Helper()
	authSourcesOnce.Do(func() {
		authSourcesFset = token.NewFileSet()
		for _, path := range goSourceFiles(t) {
			text, err := os.ReadFile(path)
			if err != nil {
				authSourcesErr = err
				return
			}
			file, err := parser.ParseFile(authSourcesFset, path, text, parser.SkipObjectResolution)
			if err != nil {
				authSourcesErr = fmt.Errorf("parse %s: %w", path, err)
				return
			}
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				authSourcesErr = err
				return
			}
			authSourcesAll = append(authSourcesAll, authSource{rel: filepath.ToSlash(rel), text: text, file: file})
		}
	})
	if authSourcesErr != nil {
		t.Fatal(authSourcesErr)
	}
	if len(authSourcesAll) < 100 {
		t.Fatalf("scanned %d Go files, want the whole tree: the walk is not reaching the code the guards read", len(authSourcesAll))
	}
	return authSourcesFset, authSourcesAll
}

// epochCallSite is one call, or one non-call reference, found in the source.
type epochCallSite struct {
	where  string // "path:EnclosingFunc"
	callee string
	forms  []string // the epoch argument of each call, in source order; nil for a reference
}

// TestGuard_TheSessionsEpochIsAlwaysTheOneTheCheckRead reads the source tree for what the
// behavioural tests prove path by path: wherever a session is created, the epoch handed to the
// insert is the one the credential check read, never one from a row the function re-read.
// Every CALL of issueTokens, IssueTokens, issueOrTOTP, CreateTOTPPendingToken and
// SessionManager.CreateSession must pass its epoch in the form allowed for that call site, and
// every non-call reference to them (an alias hides a call) must be one of the three that exist.
// `user.AuthEpoch` in VerifyLogin behaves like `*pending.AuthEpoch`, so only the exact form
// tells them apart. The SSO hand-off is pinned by the variable, its value by
// TestOIDCCallback_TheExchangeCodeCarriesTheEpochOfTheProvisionedUser.
func TestGuard_TheSessionsEpochIsAlwaysTheOneTheCheckRead(t *testing.T) {
	_, sources := authParsedSources(t)

	callees := map[string]bool{
		"issueTokens": true, "IssueTokens": true, "issueOrTOTP": true,
		"CreateTOTPPendingToken": true, "CreateSession": true,
	}
	// The two OIDC hand-offs no test can drive from outside, because Callback needs an
	// identity provider: Callback calls provisionAndStoreExchange (which a test drives),
	// which calls storeExchange with the user it provisioned. Keyed
	// "path:EnclosingFunc:callee", valued by the call's arguments.
	handOffNames := map[string]bool{"provisionAndStoreExchange": true, "storeExchange": true}
	handOffs := map[string][]string{}
	var calls, refs []epochCallSite
	readers := map[string][]string{} // "path:Func" -> the X of every X.AuthEpoch in it
	userReads := map[string]int{}    // "path:Func" -> user.AuthEpoch reads outside the permitted comparison

	for _, src := range sources {
		rel := src.rel
		visit := func(fn string, node ast.Node) {
			where := rel + ":" + fn
			isCallFun := map[ast.Node]bool{}
			// The one permitted read of user.AuthEpoch: an operand of `!=` whose other operand
			// is *pending.AuthEpoch.
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
		for _, decl := range src.file.Decls {
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

	// The functions that only pass an epoch on read none at all (the next subtest's exact map);
	// VerifyLogin, which re-reads the user, may read user.AuthEpoch only in that comparison.
	t.Run("VerifyLogin reads user.AuthEpoch only to compare it with the pending token's", func(t *testing.T) {
		const where = "internal/api/handlers/totp.go:VerifyLogin"
		if _, ok := readers[where]; !ok {
			t.Fatal("VerifyLogin reads no AuthEpoch at all: the guard no longer finds what it checks")
		}
		if n := userReads[where]; n != 0 {
			t.Errorf("%s reads user.AuthEpoch %d time(s) outside the one comparison with *pending.AuthEpoch: "+
				"it must use the epoch it carries (the credential check's), never the epoch of a user row it holds", where, n)
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
		var found bool
		for _, src := range sources {
			if src.rel != "internal/api/handlers/auth.go" {
				continue
			}
			for _, decl := range src.file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "tryLDAPLogin" || fn.Body == nil {
					continue
				}
				found = true
				ret, ok := fn.Body.List[len(fn.Body.List)-1].(*ast.ReturnStmt)
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

	// Callback itself needs an identity provider, so nothing drives it; what a test does drive
	// is provisionAndStoreExchange, and that covers the callback only if Callback gets its code
	// from it and nothing else stores one. A Callback that stored its own, from a user rebuilt
	// from its id (epoch 0), would pass every behavioural test and refuse the SSO sign-in of
	// any account whose epoch has ever moved.
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

// TestRevokeAllPaths_BumpTheEpochFirstAndInTheirOwnTransaction proves, against the handlers
// themselves, that the two revoke-alls in auth.go go through the bumping code, in the right
// place: a password change inside its transaction, after the update and before the listing
// and the revoke; and a sign-out of all devices on the pool, as the first of its three
// statements. A handler that ended the sessions through the raw revoke would pass every
// older test of it and leave a sign-in that was in flight free to mint a live session. The
// deactivation, the third, is pinned in users_deactivate_test.go.
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
		authRequireStatus(t, resp, http.StatusOK)

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
		authRequireStatus(t, resp, http.StatusOK)

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
		authRequireStatus(t, resp, http.StatusServiceUnavailable)

		if got := a.store.snapshotUser().AuthEpoch; got != before {
			t.Errorf("the epoch is %d after a change that did not happen, want %d: the bump must roll back with the change", got, before)
		}
	})
}
