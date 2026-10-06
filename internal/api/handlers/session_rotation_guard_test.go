package handlers

import (
	"cmp"
	"go/ast"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/auth"
)

// TestGuard_SessionsRotateOnlyThroughRotateRefreshToken keeps the "0 rows means refused" half of the
// refresh race fix from being bypassed. RotateSessionToken is conditional and says so with a row
// count: 0 means the session was revoked, expired or already rotated since the refresh validated it.
// A caller that ignores the count gets the old bug back (a live access token for a revoked session)
// and the type system cannot object, so auth.RotateRefreshToken, the one place that turns the count
// into an error, must be the only caller; the unconditional UpdateSessionTokenHash must not return.
// Static, because no behavioural test sees a caller that does not exist yet. A call and a bare
// reference (`f := q.RotateSessionToken`) are both watched: the second discards the count too.
func TestGuard_SessionsRotateOnlyThroughRotateRefreshToken(t *testing.T) {
	const allowed = "internal/auth/session.go"

	fset := guardFset
	scanned := 0
	allowedHits := 0

	for _, path := range goSourceFiles(t) {
		file, err := guardParsed(path)
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

// TestGuard_PreviousTokenLookupOnlyDecides keeps GetSessionByPreviousTokenHash, the one query where a
// rotated-away token matches a live session, from ever authenticating. That match is safe only for
// decisions that issue nothing: ending the session (FindSessionForLogout) and shaping a refusal
// (RefusalSparesCookie). Anywhere else (Refresh, the session list) it would make every token a session
// ever had a second credential for its window, so it may be named only in internal/auth/session.go,
// inside those two methods, once each; ValidateRefreshToken in particular must not. Static, a call and
// a bare reference watched, and it fails if it cannot see the two uses it permits.
func TestGuard_PreviousTokenLookupOnlyDecides(t *testing.T) {
	const allowedFile = "internal/auth/session.go"
	allowed := map[string]int{"FindSessionForLogout": 0, "RefusalSparesCookie": 0}

	fset := guardFset
	scanned := 0
	for _, path := range goSourceFiles(t) {
		file, err := guardParsed(path)
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

// TestGuard_FindSessionForLogoutIsOnlyReachedFromLogoutAndLogoutAll keeps the sign-out lookup from
// becoming a way to authenticate. FindSessionForLogout returns a WHOLE session for a token that is not
// its current one (a match on the previous token, for the sign-out window): safe for Logout, which ends
// the session the token names, and LogoutAll, which only decides whether the request's cookie is the
// caller's to delete. Anything standing on "this token is the live credential" must use
// ValidateRefreshToken, which never matches a previous token. Pointing currentSessionID at
// FindSessionForLogout passes every behavioural test (it returns a complete session) and quietly makes
// a rotated-away cookie an identity for is_current, so the method may be named only in one call each in
// Logout and LogoutAll (internal/api/handlers/auth.go); the guard fails if it cannot see both.
func TestGuard_FindSessionForLogoutIsOnlyReachedFromLogoutAndLogoutAll(t *testing.T) {
	const allowedFile = "internal/api/handlers/auth.go"
	// Each permitted function, and the reason it may. A new entry is a decision,
	// and its reason has to say why the lookup cannot be used to authenticate there.
	allowed := map[string]string{
		"Logout":    "ends the session its token names",
		"LogoutAll": "issues nothing and identifies no one; it only decides whether the cookie in the request is the caller's to clear",
	}
	allowedNames := slices.Sorted(maps.Keys(allowed))

	fset := guardFset
	scanned := 0
	hits := map[string]int{}
	for _, path := range goSourceFiles(t) {
		file, err := guardParsed(path)
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
