package handlers

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// The change-password lockout is read and written by ChangePassword and by nothing
// else. In particular no path that ends a session — sign out, sign out everywhere,
// the revoke of one session, any revoke-all — may clear it: whoever holds a stolen
// token can end the sessions of the account it was stolen from (and, with the
// interactive-only routes, only with a session), and a lock that such a revoke
// lifted would be a lock the guesser lifts for himself. The two tests below hold
// that, one by behaviour and one by shape.

// TestChangePassword_EndingSessionsDoesNotLiftTheLockout: lock the account, end its
// sessions the way the harness can — sign out everywhere — and the account is still
// locked, with the lock's own remaining time.
func TestChangePassword_EndingSessionsDoesNotLiftTheLockout(t *testing.T) {
	for _, kind := range []string{"redis", "memory"} {
		t.Run(kind, func(t *testing.T) {
			a, _ := lockoutApp(t, kind, raceOptions{})
			a.redis.Set(a.sessionKey(), "{}")
			for i := 1; i <= passwordLockoutThreshold; i++ {
				a.change(t, wrongChangeBody)
			}
			if status, _, _ := a.change(t, changeBody); status != http.StatusTooManyRequests {
				t.Fatalf("the account is not locked after %d wrong passwords (%d): this test proved nothing", passwordLockoutThreshold, status)
			}

			resp, _ := a.postAs(t, "/auth/logout-all", "{}", 10*time.Second)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("sign out everywhere = %d, want 200: it must have ended the sessions for this test to mean anything", resp.StatusCode)
			}
			if !a.store.snapshot().IsRevoked {
				t.Fatal("sign out everywhere did not revoke the sessions, so this test proved nothing")
			}

			status, body, header := a.change(t, changeBody)
			if status != http.StatusTooManyRequests {
				t.Errorf("the right password after the sessions were ended = %d (%v), want 429: ending sessions must not lift the lock", status, body)
			}
			if got := header.Get("Retry-After"); got == "" || got == "0" {
				t.Errorf("Retry-After = %q, want the lock's remaining seconds", got)
			}
			if kind == "redis" {
				if !a.redis.Exists(passwordLockKey(a.store.user.ID)) {
					t.Error("the lock's key is gone from Redis after the sessions were ended")
				}
			}
		})
	}
}

// TestGuard_OnlyChangePasswordTouchesTheLockout holds the shape of the code to the
// rule above. The keys the lockout keeps appear in one file, and the operations that
// count an attempt, give it back or clear the count are called from the one function
// each of them belongs to — which ChangePassword alone reaches. A new caller, or a
// second one in a handler that ends a session, fails here by name.
//
// The scan of the source finds the calls it is about, and fails when it finds none of
// one: a guard whose subject was renamed would otherwise go on passing.
func TestGuard_OnlyChangePasswordTouchesTheLockout(t *testing.T) {
	t.Run("the keys are written in one file", func(t *testing.T) {
		const owner = "internal/api/handlers/password_lockout.go"
		for _, dir := range []string{"internal", "cmd", "pkg"} {
			root := filepath.Join(repoRoot, dir)
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
					return nil
				}
				raw, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(repoRoot, path)
				if err != nil {
					return err
				}
				rel = filepath.ToSlash(rel)
				if strings.Contains(string(raw), "pwchange:") && rel != owner {
					t.Errorf("%s mentions the lockout's keys (pwchange:): only %s may, so that nothing else can read, clear or extend the lock", rel, owner)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("walk %s: %v", dir, err)
			}
		}
		raw, err := os.ReadFile(filepath.Join(repoRoot, owner))
		if err != nil {
			t.Fatalf("read %s: %v", owner, err)
		}
		if !strings.Contains(string(raw), `"pwchange:user:`) {
			t.Errorf("%s no longer holds the lockout's keys: this guard has gone stale", owner)
		}
	})

	t.Run("who calls what", func(t *testing.T) {
		// callee name -> the functions that may call it. A selector call (x.name) for the
		// three store operations, which have common names; a plain or selector call for
		// the rest, whose names are the lockout's own.
		allowed := map[string][]string{
			"admitPasswordAttempt":   {"ChangePassword"},
			"handBackAttempt":        {"ChangePassword"},
			"forgetPasswordFailures": {"ChangePassword"},
			"lockoutStore":           {"admitPasswordAttempt", "handBackAttempt", "forgetPasswordFailures"},
			"reserve":                {"admitPasswordAttempt"},
			"release":                {"handBackAttempt"},
			"clear":                  {"forgetPasswordFailures"},
		}
		selectorOnly := map[string]bool{"reserve": true, "release": true, "clear": true}
		// the store operations are implemented in password_lockout.go, where a method
		// named like one may call another of the store's: only its callers are checked.
		seen := map[string][]string{}

		files, err := filepath.Glob(filepath.Join(repoRoot, "internal", "api", "handlers", "*.go"))
		if err != nil {
			t.Fatalf("glob: %v", err)
		}
		fset := token.NewFileSet()
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					var name string
					switch f := call.Fun.(type) {
					case *ast.Ident:
						name = f.Name
					case *ast.SelectorExpr:
						name = f.Sel.Name
					default:
						return true
					}
					if _, watched := allowed[name]; !watched {
						return true
					}
					if _, isSelector := call.Fun.(*ast.SelectorExpr); selectorOnly[name] && !isSelector {
						return true // the builtin clear, or a local function of the same name
					}
					seen[name] = append(seen[name], fn.Name.Name)
					if !slices.Contains(allowed[name], fn.Name.Name) {
						t.Errorf("%s calls %s: only %v may — the lockout's state is touched by ChangePassword and by nothing that ends a session",
							fn.Name.Name, name, allowed[name])
					}
					return true
				})
			}
		}
		for name := range allowed {
			if len(seen[name]) == 0 {
				t.Errorf("the scan found no call of %s: this guard has gone stale", name)
			}
		}
		// Each of ChangePassword's three entry points is called exactly once: a second
		// call would be a second place that decides what becomes of the count.
		for _, name := range []string{"admitPasswordAttempt", "handBackAttempt", "forgetPasswordFailures"} {
			if got := len(seen[name]); got != 1 {
				t.Errorf("%s is called %d times (from %v), want once", name, got, seen[name])
			}
		}
	})
}
