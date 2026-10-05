package handlers

import (
	"cmp"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/auth"
)

// TestGuard_SessionsRotateOnlyThroughRotateRefreshToken keeps the "0 rows means
// refused" half of the refresh race fix from being bypassed.
//
// queries/sessions.sql's RotateSessionToken is conditional, and says so with a
// row count: 0 means the session was revoked, expired or already rotated since the
// refresh validated it, and no token may be issued. A caller that ignores the
// count gets the old bug back exactly — a revoked session rotated, a live access
// token minted for it — and nothing in the type system objects, because the
// count is just an int64. auth.RotateRefreshToken is the one place that turns it
// into an error, so it must be the only caller. The unconditional query it
// replaced, UpdateSessionTokenHash, must not come back either.
//
// A static check, because no behavioural test can see a caller that does not
// exist yet: the handler tests prove the handler that IS there refuses, not that
// a second one would.
//
// Both spellings are watched — a call (`q.RotateSessionToken(...)`) and a bare
// reference (`f := q.RotateSessionToken`) — because the second sails past a check
// that only looks at call expressions and still discards the count.
func TestGuard_SessionsRotateOnlyThroughRotateRefreshToken(t *testing.T) {
	const allowed = "internal/auth/session.go"

	fset := token.NewFileSet()
	scanned := 0
	allowedHits := 0

	for _, path := range goSourceFiles(t) {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++
		slash := filepath.ToSlash(path)
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "RotateSessionToken":
				if strings.HasSuffix(slash, allowed) {
					allowedHits++
					return true
				}
				t.Errorf("%s: calls RotateSessionToken directly; rotate through auth.RotateRefreshToken, "+
					"which turns 0 rows into ErrInvalidToken — ignoring the count re-opens the refresh/sign-out race",
					fset.Position(sel.Pos()))
			case "UpdateSessionTokenHash":
				t.Errorf("%s: UpdateSessionTokenHash was an unconditional rotation, removed because a refresh that lost a race "+
					"with a sign-out still minted a token through it; use auth.RotateRefreshToken",
					fset.Position(sel.Pos()))
			}
			return true
		})
	}

	// The guard is only worth its green if it can see the one call it permits. If
	// the walk found no sources, or the rename of either name left it matching
	// nothing, every assertion above would pass for the wrong reason.
	if scanned == 0 {
		t.Fatal("the guard scanned no Go sources")
	}
	if allowedHits != 1 {
		t.Fatalf("found %d references to RotateSessionToken in %s, want exactly 1 (RotateRefreshToken's own call): "+
			"the guard cannot see what it exists to restrict", allowedHits, allowed)
	}
}

// TestGuard_PreviousTokenLookupOnlyDecides keeps the lookup of a session by the
// token it had one rotation ago from ever being used to authenticate.
//
// GetSessionByPreviousTokenHash is the one query in which a rotated-away token
// matches a live session, and that match is only safe for decisions that issue
// nothing: ending the session (FindSessionForLogout) and choosing how a refusal is
// shaped (RefusalSparesCookie). Reached from anywhere else — Refresh, the session
// list, a new endpoint — it would turn every token a session ever had into a
// second credential for the length of its window. So it may be named in exactly
// one file, internal/auth/session.go, and in it only inside those two methods and
// once in each. ValidateRefreshToken in particular must not: it is what Refresh
// and is_current stand on.
//
// A static check, because no behavioural test can see a caller that does not
// exist yet. Both spellings are watched, a call and a bare reference, as in
// TestGuard_SessionsRotateOnlyThroughRotateRefreshToken, and the guard fails if
// it cannot see the two uses it permits.
func TestGuard_PreviousTokenLookupOnlyDecides(t *testing.T) {
	const allowedFile = "internal/auth/session.go"
	allowed := map[string]int{"FindSessionForLogout": 0, "RefusalSparesCookie": 0}

	fset := token.NewFileSet()
	scanned := 0
	for _, path := range goSourceFiles(t) {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++
		inAllowedFile := strings.HasSuffix(filepath.ToSlash(path), allowedFile)
		for _, decl := range file.Decls {
			enclosing := ""
			if fn, ok := decl.(*ast.FuncDecl); ok {
				enclosing = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "GetSessionByPreviousTokenHash" {
					return true
				}
				if _, ok := allowed[enclosing]; inAllowedFile && ok {
					allowed[enclosing]++
					return true
				}
				t.Errorf("%s: %s reaches GetSessionByPreviousTokenHash; only FindSessionForLogout and "+
					"RefusalSparesCookie, in %s, may — a rotated-away token must decide a sign-out or the "+
					"shape of a refusal and never authenticate anything", fset.Position(sel.Pos()),
					cmp.Or(enclosing, "(package level)"), allowedFile)
				return true
			})
		}
	}

	if scanned == 0 {
		t.Fatal("the guard scanned no Go sources")
	}
	for name, n := range allowed {
		if n != 1 {
			t.Fatalf("found %d references to GetSessionByPreviousTokenHash in %s of %s, want exactly 1: "+
				"the guard cannot see what it exists to restrict", n, name, allowedFile)
		}
	}
}

// TestGuard_FindSessionForLogoutIsOnlyReachedFromLogoutAndLogoutAll keeps the sign-out lookup
// from becoming a way to authenticate.
//
// FindSessionForLogout is the one method that hands back a WHOLE session for a
// token that is not its current one: a match on the token the session had one
// rotation ago, for the length of the sign-out window. That is safe for uses that
// issue nothing, and the two sign-out handlers are exactly those: Logout ends the
// session the token names, and LogoutAll only decides whether the cookie in its
// request is the caller's to delete (it issues nothing and identifies no one; the
// caller is the access token's user, found by authRequired). Everything that
// stands on "this token is the session's live credential" must go through
// ValidateRefreshToken instead, which never matches a previous token: Refresh, and
// the session list's is_current (currentSessionID), which resolves the caller's
// own session through the refresh cookie.
//
// Pointing currentSessionID at FindSessionForLogout is the case worth naming,
// because it passes every behavioural test that existed — the method returns a
// complete, correct-looking session — and quietly makes a rotated-away cookie
// work as an identity for the label, and for whatever is built on it later. A
// static check, because no behavioural test can see a caller that does not exist
// yet. So the method may be named in exactly two places outside tests: one call
// in Logout and one in LogoutAll, both in internal/api/handlers/auth.go. Both
// spellings are watched, a call and a bare reference, and the guard fails if it
// cannot see the one use each permits.
func TestGuard_FindSessionForLogoutIsOnlyReachedFromLogoutAndLogoutAll(t *testing.T) {
	const allowedFile = "internal/api/handlers/auth.go"
	// Each permitted function, and the reason it may. A new entry is a decision,
	// and its reason has to say why the lookup cannot be used to authenticate there.
	allowed := map[string]string{
		"Logout":    "ends the session its token names",
		"LogoutAll": "issues nothing and identifies no one; it only decides whether the cookie in the request is the caller's to clear",
	}
	allowedNames := slices.Sorted(maps.Keys(allowed))

	fset := token.NewFileSet()
	scanned := 0
	hits := map[string]int{}
	for _, path := range goSourceFiles(t) {
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		scanned++
		inAllowedFile := strings.HasSuffix(filepath.ToSlash(path), allowedFile)
		for _, decl := range file.Decls {
			enclosing := ""
			if fn, ok := decl.(*ast.FuncDecl); ok {
				enclosing = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "FindSessionForLogout" {
					return true
				}
				if _, permitted := allowed[enclosing]; inAllowedFile && permitted {
					hits[enclosing]++
					return true
				}
				t.Errorf("%s: %s reaches FindSessionForLogout; only %s in %s may — it matches a token the session "+
					"had one rotation ago, which may end a session and must never identify one: use "+
					"ValidateRefreshToken for anything that treats the cookie as the session's live credential",
					fset.Position(sel.Pos()), cmp.Or(enclosing, "(package level)"), strings.Join(allowedNames, " and "), allowedFile)
				return true
			})
		}
	}

	if scanned == 0 {
		t.Fatal("the guard scanned no Go sources")
	}
	for _, name := range allowedNames {
		if hits[name] != 1 {
			t.Fatalf("found %d references to FindSessionForLogout in %s of %s, want exactly 1: "+
				"the guard cannot see what it exists to restrict", hits[name], name, allowedFile)
		}
	}
}

// TestGuard_RefusalSparesCookieReturnsOnlyABool pins the property its doc comment
// leans on: the question "does this refusal spare the cookie" is answered with a
// bool and nothing else, so its match cannot be turned into a session, an
// identity or a token. A change that makes it return more has to change this
// test, and so has to be a decision rather than a convenience.
func TestGuard_RefusalSparesCookieReturnsOnlyABool(t *testing.T) {
	m, ok := reflect.TypeOf((*auth.SessionManager)(nil)).MethodByName("RefusalSparesCookie")
	if !ok {
		t.Fatal("auth.SessionManager has no RefusalSparesCookie")
	}
	if m.Type.NumOut() != 1 || m.Type.Out(0).Kind() != reflect.Bool {
		t.Errorf("RefusalSparesCookie returns %v; it must return exactly one bool, because it decides the shape "+
			"of a refusal and must never be able to hand back what a token authenticates", m.Type)
	}
}
