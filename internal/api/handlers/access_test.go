package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// The 25 access routes are declared endpoints now
// (internal/api/registry_access.go), so what used to be tested here splits in
// two, the same way the Veeam and PBS routes did.
//
// The PERMISSION each route enforces is a middleware Check attached from its
// declaration, and the parameter rules — a required userid, a malformed body, a
// cluster id that is not a UUID — are the schema's. Both are tested against the
// REAL declarations in internal/api/registry_access_test.go; a bare handler
// mount here could not exercise a gate that no longer sits inside the handler,
// and a test that mounted one anyway would pass while proving nothing.
//
// What stays here is what is still this file's own: the percent-decode of a
// path identifier, the self-credential guard's decision, which user edits and
// which token updates count as capable of severing access, the exact audit
// details of a user create, of a user edit and of a token update, the static
// audit-detail guard, and what the two user reads make of a user's two-factor
// keys.

func TestSplitFullTokenID(t *testing.T) {
	tests := []struct {
		in        string
		user, tok string
		ok        bool
	}{
		{"nexara@pve!api", "nexara@pve", "api", true},
		{"root@pam!nexara", "root@pam", "nexara", true},
		{"root@pam", "", "", false},
		{"!api", "", "", false},
		{"root@pam!", "", "", false},
		{"", "", "", false},
		// A user name may contain "!", so the cut is at the last one, as PVE's is.
		{"a!b@pve!api", "a!b@pve", "api", true},
		{"svc!x@pve!api", "svc!x@pve", "api", true},
		{"a!b!c@pve!api", "a!b!c@pve", "api", true},
		{"a!b@pve!", "", "", false},
	}
	for _, tc := range tests {
		u, k, ok := splitFullTokenID(tc.in)
		if ok != tc.ok || u != tc.user || k != tc.tok {
			t.Errorf("splitFullTokenID(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.in, u, k, ok, tc.user, tc.tok, tc.ok)
		}
	}
}

// TestSelfCredentialSubject covers the guard that stops Nexara destroying its
// own cluster credential. The empty-tokenid cases matter most: deleting a user
// takes its tokens with it, so that collides on the user alone.
func TestSelfCredentialSubject(t *testing.T) {
	const own = "nexara@pve!api"

	tests := []struct {
		name            string
		ownTokenID      string
		userid, tokenid string
		wantConflict    bool
		wantSubject     string
	}{
		{"exact token match", own, "nexara@pve", "api", true, "token nexara@pve!api"},
		{"case-insensitive", own, "NEXARA@PVE", "API", true, "token nexara@pve!api"},
		{"user delete takes our token", own, "nexara@pve", "", true, "user nexara@pve"},
		{"different token, same user", own, "nexara@pve", "other", false, ""},
		{"different user", own, "alice@pve", "api", false, ""},
		{"different user, whole-user delete", own, "alice@pve", "", false, ""},
		{"cluster token id has no bang", "nexara@pve", "nexara@pve", "api", false, ""},
		{"cluster token id empty", "", "nexara@pve", "api", false, ""},

		// An own token whose user name contains "!" is still recognised: the cut
		// between user and token is at the last one.
		{"user name with a bang, exact token match", "svc!x@pve!api", "svc!x@pve", "api", true, "token svc!x@pve!api"},
		{"user name with a bang, case-insensitive", "svc!x@pve!api", "SVC!X@PVE", "API", true, "token svc!x@pve!api"},
		{"user name with a bang, user delete takes our token", "svc!x@pve!api", "svc!x@pve", "", true, "user svc!x@pve"},
		{"user name with a bang, different token", "svc!x@pve!api", "svc!x@pve", "other", false, ""},
		{"user name with a bang, different account", "svc!x@pve!api", "svc!y@pve", "api", false, ""},
		{"user name with a bang, the leading part of it is another account", "svc!x@pve!api", "svc@pve", "api", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject, conflict := selfCredentialSubject(tc.ownTokenID, tc.userid, tc.tokenid)
			if conflict != tc.wantConflict {
				t.Fatalf("conflict = %v, want %v", conflict, tc.wantConflict)
			}
			if conflict && subject != tc.wantSubject {
				t.Errorf("subject = %q, want %q", subject, tc.wantSubject)
			}
		})
	}
}

// TestSelfCredentialRefusal is the table of guardSelfCredential's decision, over
// everything it depends on and with no database or Proxmox: what the request
// names, what the lookup found, and whether the caller sent force.
//
// The rows that matter most are the ones the guard used to get wrong. A cluster
// row that could not be read let an unforced delete of Nexara's own token
// through; it is a refusal now, and never the 409 that has the SPA open an
// override saying the action WILL cut Nexara off, which nothing here knows. The
// 404/500 split is CreateProxmoxClient's own for a cluster row it cannot read.
func TestSelfCredentialRefusal(t *testing.T) {
	const own = "nexara@pve!api"

	// A lookup failure whose text names an address, to prove the answer does not
	// carry it.
	const leak = "192.0.2.10:5432"
	dbDown := fmt.Errorf("dial tcp %s: connection refused", leak)

	tests := []struct {
		name            string
		force           bool
		lookupErr       error
		ownTokenID      string
		userid, tokenid string
		// wantStatus is the refusal's status; 0 means the request may go on.
		wantStatus int
		// wantIn are the fragments the refusal's message must carry.
		wantIn []string
	}{
		// Not Nexara's own credential.
		{"another token of the same user", false, nil, own, "nexara@pve", "other", 0, nil},
		{"another user's token", false, nil, own, "alice@pve", "api", 0, nil},
		{"another user, the whole account", false, nil, own, "alice@pve", "", 0, nil},
		{"the cluster's token id has no bang", false, nil, "nexara@pve", "nexara@pve", "api", 0, nil},
		{"the cluster's token id is empty", false, nil, "", "nexara@pve", "api", 0, nil},

		// Nexara's own credential, unforced: the conflict, and the way past it.
		{"the token", false, nil, own, "nexara@pve", "api", fiber.StatusConflict,
			[]string{"token nexara@pve!api", "force=true"}},
		{"the token, in another case", false, nil, own, "NEXARA@PVE", "API", fiber.StatusConflict,
			[]string{"token nexara@pve!api", "force=true"}},
		{"the user that owns it", false, nil, own, "nexara@pve", "", fiber.StatusConflict,
			[]string{"user nexara@pve", "force=true"}},
		{"the user that owns it, in another case", false, nil, own, "Nexara@PVE", "", fiber.StatusConflict,
			[]string{"user nexara@pve", "force=true"}},
		// The message covers a narrowing edit and one undone in Proxmox as well as a
		// cut-off, since the guard covers all of them.
		{"the conflict says the change can also take permissions away", false, nil, own, "nexara@pve", "api", fiber.StatusConflict,
			[]string{"cut Nexara off", "take away permissions it relies on", "undone in Proxmox"}},
		{"a token whose user name contains a bang", false, nil, "svc!x@pve!api", "svc!x@pve", "api", fiber.StatusConflict,
			[]string{"token svc!x@pve!api", "force=true"}},

		// Nexara's own credential, forced: the operator's decision.
		{"the token, forced", true, nil, own, "nexara@pve", "api", 0, nil},
		{"the user that owns it, forced", true, nil, own, "nexara@pve", "", 0, nil},

		// Could not tell: a refusal, and one that is not a conflict.
		{"the cluster row cannot be read", false, dbDown, "", "nexara@pve", "api", fiber.StatusInternalServerError,
			[]string{"could not check", "nothing was changed"}},
		{"the cluster row cannot be read, and the request names another user's token", false, dbDown, "", "alice@pve", "other",
			fiber.StatusInternalServerError, []string{"could not check", "nothing was changed"}},
		{"the cluster row cannot be read, whole-user delete", false, dbDown, "", "alice@pve", "", fiber.StatusInternalServerError,
			[]string{"could not check", "nothing was changed"}},
		{"the read failed with a wrapped error", false, fmt.Errorf("get cluster: %w", dbDown), "", "nexara@pve", "api",
			fiber.StatusInternalServerError, []string{"could not check", "nothing was changed"}},
		{"the read ran out of time", false, context.DeadlineExceeded, "", "nexara@pve", "api", fiber.StatusInternalServerError,
			[]string{"could not check", "nothing was changed"}},
		{"the request was cancelled mid-read", false, context.Canceled, "", "nexara@pve", "api", fiber.StatusInternalServerError,
			[]string{"could not check", "nothing was changed"}},
		{"the handler has no database", false, errAccessNoQueries, "", "nexara@pve", "api", fiber.StatusInternalServerError,
			[]string{"could not check", "nothing was changed"}},
		// A token id left beside the error is stale, not an answer: the failure
		// decides, whether or not the leftover would have matched.
		{"an error beside an id that matches is not a conflict", false, dbDown, own, "nexara@pve", "api",
			fiber.StatusInternalServerError, []string{"could not check"}},
		{"an error beside an id that does not match is not a pass", false, dbDown, own, "alice@pve", "other",
			fiber.StatusInternalServerError, []string{"could not check"}},

		{"the cluster does not exist", false, pgx.ErrNoRows, "", "nexara@pve", "api", fiber.StatusNotFound,
			[]string{"Cluster not found"}},
		{"the cluster does not exist, wrapped", false, fmt.Errorf("get cluster: %w", pgx.ErrNoRows), "", "nexara@pve", "api",
			fiber.StatusNotFound, []string{"Cluster not found"}},
		{"the cluster does not exist, whoever the request names", false, pgx.ErrNoRows, "", "alice@pve", "",
			fiber.StatusNotFound, []string{"Cluster not found"}},

		// force does not depend on the lookup: it is not made for it, and a failed
		// one changes nothing.
		{"the cluster row cannot be read, forced", true, dbDown, "", "nexara@pve", "api", 0, nil},
		{"the cluster does not exist, forced", true, pgx.ErrNoRows, "", "nexara@pve", "api", 0, nil},
		{"an error beside an id that matches, forced", true, dbDown, own, "nexara@pve", "api", 0, nil},
	}

	// statusOf is the refusal's status, 0 for none, and fails the test on an error
	// that is not the fiber.Error every refusal is.
	statusOf := func(t *testing.T, err error) (int, string) {
		t.Helper()
		if err == nil {
			return 0, ""
		}
		var fe *fiber.Error
		if !errors.As(err, &fe) {
			t.Fatalf("the refusal is a %T (%v), not a *fiber.Error", err, err)
		}
		return fe.Code, fe.Message
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, message := statusOf(t, selfCredentialRefusal(tc.force, tc.lookupErr, tc.ownTokenID, tc.userid, tc.tokenid))
			if status != tc.wantStatus {
				t.Fatalf("status = %d (%q), want %d", status, message, tc.wantStatus)
			}
			for _, fragment := range tc.wantIn {
				if !strings.Contains(message, fragment) {
					t.Errorf("the message %q does not carry %q", message, fragment)
				}
			}
			if strings.Contains(message, leak) || strings.Contains(message, "connection refused") {
				t.Errorf("the message %q carries the lookup failure's own text", message)
			}
		})
	}

	// The property the rows above illustrate, held over every combination rather
	// than the ones somebody thought of: force always lets the request go on, and
	// an unforced request whose row could not be read never does — and never
	// answers with the conflict, which is only for a credential known to be
	// Nexara's.
	visited := 0
	for _, force := range []bool{false, true} {
		for _, lookupErr := range []error{nil, pgx.ErrNoRows, dbDown} {
			for _, ownID := range []string{own, "nexara@pve", ""} {
				for _, target := range [][2]string{{"nexara@pve", "api"}, {"nexara@pve", ""}, {"alice@pve", "api"}} {
					visited++
					status, message := statusOf(t, selfCredentialRefusal(force, lookupErr, ownID, target[0], target[1]))
					switch {
					case force:
						if status != 0 {
							t.Errorf("force=true was refused with %d (%q) for lookup error %v", status, message, lookupErr)
						}
					case lookupErr == nil:
						if status != 0 && status != fiber.StatusConflict {
							t.Errorf("a readable row was refused with %d (%q), want none or the conflict", status, message)
						}
					case errors.Is(lookupErr, pgx.ErrNoRows):
						if status != fiber.StatusNotFound {
							t.Errorf("a missing cluster answered %d (%q), want %d", status, message, fiber.StatusNotFound)
						}
					default:
						if status != fiber.StatusInternalServerError {
							t.Errorf("an unreadable cluster answered %d (%q), want %d", status, message, fiber.StatusInternalServerError)
						}
					}
				}
			}
		}
	}
	if want := 2 * 3 * 3 * 3; visited != want {
		t.Fatalf("visited %d combinations, want %d — the loop above stopped covering them", visited, want)
	}
}

// TestLogGuardLookupFailure pins the level of the record the guard writes when it
// cannot read the cluster row, over the guard's own context and the failure it
// met, with the writer redirected the way captureSlog does it.
//
// Warn is for a context that has ended, cancelled or out of time: the caller went
// away, and the database is not at fault. It is decided by ctx.Err() and not by
// the failure's chain, because a context error in the chain can be the driver's
// own timeout (pgconn's connect_timeout wraps DeadlineExceeded) under a context
// that is live, and that is the database's, an Error. Fiber's c.Context() is
// Background unless SetContext is called, so no request reaches the Warn today;
// the contexts that have ended are made by hand here. A cluster that is not there
// is not logged, whatever the context.
func TestLogGuardLookupFailure(t *testing.T) {
	clusterID := uuid.MustParse("cccccccc-0000-0000-0000-000000000006")
	dbDown := errors.New("dial tcp 192.0.2.10:5432: connection refused")

	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, release := context.WithTimeout(context.Background(), 0)
	defer release()

	tests := []struct {
		name string
		ctx  context.Context
		err  error
		// wantLevel is the level of the one record the guard writes; "" is none.
		wantLevel string
	}{
		{"the database fails", live, dbDown, "ERROR"},
		{"the driver's own timeout, under a live context", live,
			fmt.Errorf("get cluster: %w", context.DeadlineExceeded), "ERROR"},
		{"a query the driver cancelled, under a live context", live,
			fmt.Errorf("get cluster: %w", context.Canceled), "ERROR"},

		{"the request was cancelled", cancelled, fmt.Errorf("get cluster: %w", context.Canceled), "WARN"},
		{"the request ran out of time", expired, fmt.Errorf("get cluster: %w", context.DeadlineExceeded), "WARN"},
		{"the context has ended, whatever the failure was", cancelled, dbDown, "WARN"},

		{"the cluster does not exist", live, fmt.Errorf("get cluster: %w", pgx.ErrNoRows), ""},
		{"the cluster does not exist, under a context that has ended", cancelled,
			fmt.Errorf("get cluster: %w", pgx.ErrNoRows), ""},
		{"nothing failed", live, nil, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logged := captureSlog(t)
			logGuardLookupFailure(tc.ctx, clusterID, tc.err)

			var records []string
			for _, line := range strings.Split(logged(), "\n") {
				if line != "" {
					records = append(records, line)
				}
			}
			if tc.wantLevel == "" {
				if len(records) != 0 {
					t.Errorf("logged %q, want nothing", records)
				}
				return
			}
			if len(records) != 1 {
				t.Fatalf("logged %d records, want 1: %q", len(records), records)
			}
			for _, want := range []string{"level=" + tc.wantLevel, "cluster_id=" + clusterID.String(), tc.err.Error()} {
				if !strings.Contains(records[0], want) {
					t.Errorf("the record %q does not carry %q", records[0], want)
				}
			}
		})
	}
}

// secretishKey matches audit-detail keys that would leak a credential. keys is a
// user's legacy two-factor field, which can hold TOTP shared secrets (see
// accessUserResponse).
var secretishKey = []string{"password", "secret", "token_secret", "value", "ticket", "otp", "csrf", "privatekey", "bind_password", "keys"}

// accessAuditBuilders are the functions whose result access.go may hand to
// json.Marshal in place of a map literal.
//
// The guard below holds a builder's body, and the arguments it is called with, to
// its rules about credential-shaped keys and reads, but it cannot tell what the
// builder records under any other name. An entry is therefore also the decision
// to trust other tests, and is acceptable only with all of these: tests that
// compare what the builder returns exactly, so that a key added to it fails one
// whether or not it looks like a credential; one of them driven through the real
// route, so that a handler that stops calling the builder fails too; and a comment
// on the builder naming them.
//
// Today that is three:
//   - accessUserUpdateDetails, pinned by TestAccessUserUpdateDetails,
//     TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields and, through the
//     real route, TestAccessUpdateUserAuditRow in internal/api;
//   - accessUserCreateDetails, pinned by TestAccessUserCreateDetails,
//     TestAccessUserCreateDetailsRecordsOnlyTheAllowedFields and, through the
//     real route, TestAccessCreateUserAuditRow in internal/api;
//   - accessTokenUpdateDetails, pinned by TestAccessTokenUpdateDetails,
//     TestAccessTokenUpdateDetailsRecordsOnlyTheAllowedFields and, through the
//     real route, TestAccessUpdateTokenAuditRow in internal/api.
var accessAuditBuilders = map[string]bool{
	"accessUserUpdateDetails":  true,
	"accessUserCreateDetails":  true,
	"accessTokenUpdateDetails": true,
}

// isAccessAuditBuilderCall reports whether arg is a call to a function listed in
// accessAuditBuilders. Only a bare call counts, and only one whose name resolves
// to a top-level function: a method or a package-qualified function of the same
// name is some other function, and so is a local closure or parameter that
// shadows it.
//
// The resolution is the parser's own lexical one, ast.Ident.Obj. Its documented
// blind spot, the field names in a struct literal, cannot touch a callee.
func isAccessAuditBuilderCall(arg ast.Expr) bool {
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return false
	}
	name, ok := call.Fun.(*ast.Ident)
	return ok && accessAuditBuilders[name.Name] && name.Obj != nil && name.Obj.Kind == ast.Fun
}

// TestGuard_AccessAuditDetailsCarryNoSecrets is a static guard over access.go.
// Every one-argument json.Marshal must be handed a map literal or a call to a
// builder in accessAuditBuilders. A map literal, and the arguments of a builder
// call, may carry no credential-shaped key, and nothing in them may read a
// credential. A builder's own body is held to the same two rules.
//
// This matters more here than almost anywhere else in the codebase. Proxmox
// returns a token secret exactly once, this file is where that value lives, and
// audit rows are readable by anyone holding view:audit — which every built-in
// Viewer does. A secret reaching an audit detail would be readable by
// accounts that cannot even see the token list.
//
// Static rather than behavioural because most detail maps are built inline, one
// per call site; a runtime test would have to reach Proxmox at each one.
//
// The marshal argument is enforced, not assumed. A map assigned to a local
// first, a struct or slice literal, or a call that is not in accessAuditBuilders
// hides its keys from the checks on a map literal, so it is refused: a guard
// that skipped what it could not read would pass exactly the shape a secret is
// laundered through. A call by a builder's name that resolves to a local, not to
// the top-level function, is such a call.
//
// What it cannot see, stated plainly. It inspects one-argument calls to a function
// named Marshal (json.Marshal, here) and nothing else, so json.MarshalIndent, an
// Encoder, or a json.RawMessage built by hand bypass it. A key is a string
// literal, in a map literal or as the index of an assignment, matched exactly
// against secretishKey (has_password is legitimate, so user_password would pass,
// and so would a constant key). A read is a selector named .Value, .Password,
// .TokenSecret, .Secret or .Keys, or a call whose first argument is a
// secretishKey literal, as p.String("password") is. A credential held in a local,
// reached through a call, or kept under another name passes, and so does what a
// builder records under a name outside the list: its exact-match tests are what
// see that. It is aimed at the realistic mistake — someone adding
// `"secret": created.Value` while wiring up a new endpoint — not at deliberate
// laundering. Keeping the rule absolute (never read .Value/.Password here at all)
// is what makes it worth having; the one legitimate derived value, has_password,
// is computed before the Marshal call and commented as such.
func TestGuard_AccessAuditDetailsCarryNoSecrets(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "access.go", nil, 0)
	if err != nil {
		t.Fatalf("parse access.go: %v", err)
	}
	line := func(n ast.Node) int { return fset.Position(n.Pos()).Line }

	funcs := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			funcs[fn.Name.Name] = fn
		}
	}

	// stringLit is the lower-cased value of e when it is a string literal.
	stringLit := func(e ast.Expr) (string, bool) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(lit.Value) // a raw string is a string literal too
		if err != nil {
			// Unreachable: ParseFile rejects a malformed string literal before this
			// runs, and a name still wrapped in its quotes could never match
			// secretishKey anyway.
			return "", false
		}
		return strings.ToLower(s), true
	}

	// scanKeys refuses a credential-shaped name used as a string-literal key
	// anywhere under node: a key of a map literal, the keys of a map nested in a
	// value included, or the index of an assignment such as details["keys"] = ….
	scanKeys := func(node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			var key ast.Expr
			switch n := n.(type) {
			case *ast.KeyValueExpr:
				key = n.Key
			case *ast.IndexExpr:
				key = n.Index
			default:
				return true
			}
			if name, ok := stringLit(key); ok && slices.Contains(secretishKey, name) {
				t.Errorf("%s:%d: audit details carry key %q — token secrets and passwords must never reach an audit row (view:audit is held by every Viewer)",
					"access.go", line(key), name)
			}
			return true
		})
	}

	// scanReads refuses a read of a credential anywhere under node, whatever key
	// the value sits under: details{"x": created.Value} is the leak. A read is a
	// selector of a credential-bearing field, or a call that names a credential in
	// its first argument, as p.String("password") does.
	scanReads := func(node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				switch strings.ToLower(n.Sel.Name) {
				case "value", "password", "tokensecret", "secret", "keys":
					t.Errorf("%s:%d: audit details read .%s — that is a credential, keep it out of the audit row",
						"access.go", line(n), n.Sel.Name)
				}
			case *ast.CallExpr:
				if len(n.Args) == 0 {
					break
				}
				if name, ok := stringLit(n.Args[0]); ok && slices.Contains(secretishKey, name) {
					t.Errorf("%s:%d: audit details read %s(%q) — that names a credential, keep it out of the audit row",
						"access.go", line(n), types.ExprString(n.Fun), name)
				}
			}
			return true
		})
	}

	// An entry names a function this file declares: one that outlived its
	// function would exempt whatever is later given that name. Its body is held to
	// the rules a map literal is, since the guard cannot see into a call from the
	// outside.
	for name := range accessAuditBuilders {
		fn := funcs[name]
		if fn == nil || fn.Body == nil {
			t.Fatalf("accessAuditBuilders lists %s, but access.go declares no such function — "+
				"an entry that outlives its function exempts whatever is later given that name", name)
		}
		scanKeys(fn.Body)
		scanReads(fn.Body)
	}

	var checked int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || callName(call) != "Marshal" || len(call.Args) != 1 {
			return true
		}
		switch arg := call.Args[0].(type) {
		case *ast.CompositeLit:
			if _, isMap := arg.Type.(*ast.MapType); isMap {
				checked++
				scanKeys(arg)
				scanReads(arg)
				return true
			}
		case *ast.CallExpr:
			if isAccessAuditBuilderCall(arg) {
				for _, a := range arg.Args {
					scanKeys(a)
					scanReads(a)
				}
				return true
			}
		}
		t.Errorf("%s:%d: json.Marshal(%s) — an audit detail must be a `map[…]…{…}` literal "+
			"(a named map type such as fiber.Map is refused too), whose keys and values this guard reads, "+
			"or a call to a builder listed in accessAuditBuilders, whose keys exact-match tests pin; "+
			"a struct or slice literal, a local, or any other call hides them from both, and audit rows are readable by every Viewer",
			"access.go", line(call), types.ExprString(call.Args[0]))
		return true
	})

	if checked == 0 {
		t.Fatal("found no json.Marshal(map literal) in access.go — the guard would pass vacuously")
	}
}

// TestGuard_AccessAuditDetailsNeverReadTheRawParams is the other half of the
// guard above, and it exists because the migration to declared parameters
// created a NEW way to leak the same secret.
//
// apischema.Params.Raw() returns every validated parameter, including
// `password` on the user-create route, and handing it to json.Marshal would
// write that password into a row every Viewer can read — which is exactly what
// Raw's own doc comment warns against. No call site does this today; the guard
// is here so none appears.
func TestGuard_AccessAuditDetailsNeverReadTheRawParams(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "access.go", nil, 0)
	if err != nil {
		t.Fatalf("parse access.go: %v", err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || callName(call) != "Raw" {
			return true
		}
		t.Errorf("access.go:%d: calls Params.Raw() — it carries every declared parameter, "+
			"including the create route's password, and this file's audit rows are readable by every Viewer",
			fset.Position(call.Pos()).Line)
		return true
	})
}

// TestAccessParamDecodesPercentEncoding is a regression test for a
// feature-breaking bug.
//
// Fiber v3 does not percent-decode path params, and the registry hands the
// handler what Fiber matched. A PVE user id is "name@realm", so any correct
// client sends encodeURIComponent("nexara@pve") = "nexara%40pve". Using that
// raw value handed the validator a string containing "%" and no "@", which it
// rejected — so every user, token, group, role and realm lookup 400'd for
// clients doing exactly the right thing.
//
// The decode must stay paired with validation happening afterwards: "%2e%2e"
// decodes to ".." and is rejected by the proxmox client's validators, and the
// outbound path is re-escaped. This test pins the decode; the traversal half is
// pinned by TestAccessMethodsRejectInjectionWithoutIssuingRequest.
func TestAccessParamDecodesPercentEncoding(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"nexara%40pve", "nexara@pve"},
		{"nexara@pve", "nexara@pve"},
		{"root%40pam", "root@pam"},
		{"first.last-1%40pve", "first.last-1@pve"},
		// Decodes to a traversal; the proxmox-client validators reject it
		// downstream, which is why decoding first is safe.
		{"%2e%2e", ".."},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := accessParam(tc.raw, "userid")
			if err != nil {
				t.Fatalf("accessParam(%q) returned %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("accessParam(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}

	// The malformed-escape branch. The declared patterns admit only a
	// well-formed "%XX", so no request can reach this today — it stays as
	// defence in depth, and naming the parameter is what makes the 400
	// actionable when something else starts calling this.
	if _, err := accessParam("nexara%zz", "userid"); err == nil {
		t.Error("accessParam accepted a malformed percent-escape")
	} else if !strings.Contains(err.Error(), "userid") {
		t.Errorf("the rejection does not name the parameter: %v", err)
	}
}

// TestAccessUpdateAffectsAccess covers which user edits are treated as capable
// of severing Nexara's own cluster access.
//
// The distinction matters in both directions: guarding too little lets a PUT
// disable nexara@pve with no confirmation (PVE validates the owning user when
// verifying a token, so every later call 401s), while guarding too much makes
// routine comment edits demand a force flag and trains operators to pass it
// without reading.
func TestAccessUpdateAffectsAccess(t *testing.T) {
	str := func(s string) *string { return &s }
	b := func(v bool) *bool { return &v }
	i64 := func(v int64) *int64 { return &v }

	tests := []struct {
		name string
		req  proxmox.UpdateAccessUserParams
		want bool
	}{
		{"empty update", proxmox.UpdateAccessUserParams{}, false},
		{"comment only", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Comment: str("hi")},
		}, false},
		{"email only", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Email: str("a@b.c")},
		}, false},
		{"explicitly enabling", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Enable: b(true)},
		}, false},
		{"expire cleared to never", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Expire: i64(0)},
		}, false},

		{"disabling", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Enable: b(false)},
		}, true},
		{"setting an expiry", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Expire: i64(1767225600)},
		}, true},
		{"changing groups", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Groups: str("admins")},
		}, true},
		{"clearing groups", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Groups: str("")},
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessUpdateAffectsAccess(tc.req); got != tc.want {
				t.Errorf("accessUpdateAffectsAccess = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAccessTokenUpdateAffectsAccess covers which token updates are treated as
// capable of stopping the token working or narrowing what it may do, and so of
// severing Nexara's own cluster access when the token is the one it uses.
//
// The direction of privsep is the row worth a second look. PVE gives a token
// without privilege separation exactly its owning user's permissions, and one
// with it only the token's own ACL entries, intersected with the user's
// (PVE::RPCEnvironment permissions): turning separation ON can only take
// permissions away, turning it OFF can only add them. So true is guarded, and
// false — which can never make Nexara's token weaker — is not; guarding both
// would have every harmless edit of the token demand a force flag.
func TestAccessTokenUpdateAffectsAccess(t *testing.T) {
	str := func(s string) *string { return &s }
	b := func(v bool) *bool { return &v }
	i64 := func(v int64) *int64 { return &v }

	tests := []struct {
		name string
		req  proxmox.UpdateAccessTokenParams
		want bool
	}{
		{"empty update", proxmox.UpdateAccessTokenParams{}, false},
		{"comment only", proxmox.UpdateAccessTokenParams{Comment: str("hi")}, false},
		{"clearing the comment", proxmox.UpdateAccessTokenParams{Comment: str("")}, false},
		{"expire cleared to never", proxmox.UpdateAccessTokenParams{Expire: i64(0)}, false},
		{"turning privilege separation off", proxmox.UpdateAccessTokenParams{PrivSep: b(false)}, false},
		{"everything harmless at once", proxmox.UpdateAccessTokenParams{
			Comment: str("hi"), Expire: i64(0), PrivSep: b(false)}, false},

		{"regenerating", proxmox.UpdateAccessTokenParams{Regenerate: true}, true},
		{"regenerating with harmless fields beside it", proxmox.UpdateAccessTokenParams{
			Regenerate: true, Comment: str("hi"), Expire: i64(0), PrivSep: b(false)}, true},
		{"setting an expiry", proxmox.UpdateAccessTokenParams{Expire: i64(1767225600)}, true},
		{"an expiry already in the past is judged by being set, not by the clock",
			proxmox.UpdateAccessTokenParams{Expire: i64(1)}, true},
		{"turning privilege separation on", proxmox.UpdateAccessTokenParams{PrivSep: b(true)}, true},
		{"turning it on beside an expiry of 0", proxmox.UpdateAccessTokenParams{
			PrivSep: b(true), Expire: i64(0)}, true},
		{"turning it on beside a comment", proxmox.UpdateAccessTokenParams{
			PrivSep: b(true), Comment: str("hi")}, true},
		{"every field", proxmox.UpdateAccessTokenParams{
			Comment: str("hi"), Expire: i64(1767225600), PrivSep: b(true), Regenerate: true}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessTokenUpdateAffectsAccess(tc.req); got != tc.want {
				t.Errorf("accessTokenUpdateAffectsAccess = %v, want %v", got, tc.want)
			}
		})
	}
}

// accessUpdateDetailsJSON is the bytes a user update's audit details marshal to,
// which is what the audit row stores and a Viewer reads.
func accessUpdateDetailsJSON(t *testing.T, userid string, force bool, req proxmox.UpdateAccessUserParams) string {
	t.Helper()
	out, err := json.Marshal(accessUserUpdateDetails(userid, force, req))
	if err != nil {
		t.Fatalf("marshal the audit details: %v", err)
	}
	return string(out)
}

// TestAccessUserUpdateDetails pins a user update's audit details to the exact
// JSON that reaches the row, one case for each thing the builder decides.
//
// The comparison is on the marshalled bytes because they are what the row stores
// and a Viewer reads. It is exact, so a key the builder gains fails every case
// that sets the field it would come from.
//
// One of the guards for these keys. TestGuard_AccessAuditDetailsCarryNoSecrets
// checks the builder's body and arguments (accessAuditBuilders) only for
// credential-shaped keys and reads, so the key set it returns is pinned here,
// across the request type in
// TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields, and through the real
// route in TestAccessUpdateUserAuditRow.
func TestAccessUserUpdateDetails(t *testing.T) {
	const self = "nexara@pve"

	// Values of the fields the row must never carry, each one recognisable in
	// the output whatever key it might be recorded under. The group list is here
	// too: the row says a membership was set, not what it became.
	const (
		sentinelComment = "sentinel-comment"
		sentinelEmail   = "sentinel-email@example.com"
		sentinelFirst   = "sentinel-first"
		sentinelLast    = "sentinel-last"
		sentinelKeys    = "sentinel-keys"
		sentinelGroups  = "sentinel-group-list"
	)
	unrecorded := []string{sentinelComment, sentinelEmail, sentinelFirst, sentinelLast, sentinelKeys, sentinelGroups}

	str := func(s string) *string { return &s }
	b := func(v bool) *bool { return &v }
	i64 := func(v int64) *int64 { return &v }
	edit := func(f proxmox.AccessUserFields) proxmox.UpdateAccessUserParams {
		return proxmox.UpdateAccessUserParams{AccessUserFields: f}
	}

	tests := []struct {
		name   string
		userid string
		force  bool
		req    proxmox.UpdateAccessUserParams
		want   string
	}{
		{"nothing set", self, false, edit(proxmox.AccessUserFields{}),
			`{"forced":false,"userid":"nexara@pve"}`},
		{"force on an edit that sets nothing overrides nothing", self, true, edit(proxmox.AccessUserFields{}),
			`{"forced":false,"userid":"nexara@pve"}`},
		{"force on a comment change overrides nothing", self, true, edit(proxmox.AccessUserFields{Comment: str(sentinelComment)}),
			`{"forced":false,"userid":"nexara@pve"}`},

		{"disabling", self, false, edit(proxmox.AccessUserFields{Enable: b(false)}),
			`{"enable":false,"forced":false,"userid":"nexara@pve"}`},
		{"disabling, forced", self, true, edit(proxmox.AccessUserFields{Enable: b(false)}),
			`{"enable":false,"forced":true,"userid":"nexara@pve"}`},
		{"enabling is recorded although the guard ignores it", self, true, edit(proxmox.AccessUserFields{Enable: b(true)}),
			`{"enable":true,"forced":false,"userid":"nexara@pve"}`},

		{"giving an expiry, forced", self, true, edit(proxmox.AccessUserFields{Expire: i64(1767225600)}),
			`{"expire":1767225600,"forced":true,"userid":"nexara@pve"}`},
		{"clearing the expiry is recorded as 0", self, false, edit(proxmox.AccessUserFields{Expire: i64(0)}),
			`{"expire":0,"forced":false,"userid":"nexara@pve"}`},

		{"replacing groups, forced: the list is not recorded", self, true, edit(proxmox.AccessUserFields{Groups: str(sentinelGroups)}),
			`{"forced":true,"groups_changed":true,"userid":"nexara@pve"}`},
		{"removing every group", self, false, edit(proxmox.AccessUserFields{Groups: str("")}),
			`{"forced":false,"groups_changed":true,"userid":"nexara@pve"}`},
		{"appending groups records what replacing them does", self, true, proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Groups: str(sentinelGroups)}, Append: true},
			`{"forced":true,"groups_changed":true,"userid":"nexara@pve"}`},
		{"append without groups sets no groups", self, true, proxmox.UpdateAccessUserParams{Append: true},
			`{"forced":false,"userid":"nexara@pve"}`},

		{"the account named is the one edited", "alice@pve", false, edit(proxmox.AccessUserFields{Enable: b(false)}),
			`{"enable":false,"forced":false,"userid":"alice@pve"}`},

		{"every field at once carries only the three", self, true, proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{
				Comment:   str(sentinelComment),
				Email:     str(sentinelEmail),
				FirstName: str(sentinelFirst),
				LastName:  str(sentinelLast),
				Keys:      str(sentinelKeys),
				Groups:    str(sentinelGroups),
				Enable:    b(false),
				Expire:    i64(1767225600),
			},
			Append: true,
		}, `{"enable":false,"expire":1767225600,"forced":true,"groups_changed":true,"userid":"nexara@pve"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := accessUpdateDetailsJSON(t, tc.userid, tc.force, tc.req)
			if got != tc.want {
				t.Errorf("details = %s, want %s", got, tc.want)
			}
			for _, secret := range unrecorded {
				if strings.Contains(got, secret) {
					t.Errorf("details %s carry %q — audit rows are readable by every Viewer", got, secret)
				}
			}
		})
	}
}

// fillAccessUserRequestField sets slot, a pointer or a plain value, to something
// that stands out: a string carrying the field's name, true, or 1700000000. The
// reflection tests over the user request types use it to set one field at a
// time. A field of a type it does not know fails the test outright rather than
// being skipped.
func fillAccessUserRequestField(t *testing.T, slot reflect.Value, name string) {
	t.Helper()
	value := slot
	if slot.Kind() == reflect.Pointer {
		slot.Set(reflect.New(slot.Type().Elem()))
		value = slot.Elem()
	}
	switch value.Kind() {
	case reflect.String:
		value.SetString("sentinel-" + name)
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Int64:
		value.SetInt(1700000000)
	default:
		t.Fatalf("%s is a %s; teach this test to set it", name, slot.Type())
	}
}

// TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields is the allow-list
// checked against the request type rather than against a list of the fields that
// exist today.
//
// It walks every field of proxmox.UpdateAccessUserParams, the embedded
// AccessUserFields flattened and Append beside it, and sets each one that is not
// recorded, on its own, to a value that stands out. The details must stay what an
// empty edit's are. A field added later, to either struct, and recorded by
// mistake fails here without anyone having had to think of a case for it. A field
// the row is meant to carry belongs in recorded below, in the builder and in its
// comment.
//
// Three checks are on the test itself, each one a slip in how a field is set: the
// filled request must differ from an empty one (a fill that sets nothing, sets a
// zero, or sets a copy would otherwise pass every subtest), a field of a type the
// fill does not know fails outright, and the walk must still see the fields it
// exists for.
func TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields(t *testing.T) {
	const userid = "nexara@pve"
	recorded := map[string]bool{"Enable": true, "Expire": true, "Groups": true}

	empty := accessUpdateDetailsJSON(t, userid, false, proxmox.UpdateAccessUserParams{})

	visited := 0
	for _, field := range reflect.VisibleFields(reflect.TypeOf(proxmox.UpdateAccessUserParams{})) {
		// The embedded AccessUserFields is walked through its own fields.
		if !field.IsExported() || field.Anonymous || recorded[field.Name] {
			continue
		}
		visited++
		t.Run(field.Name, func(t *testing.T) {
			var req proxmox.UpdateAccessUserParams
			fillAccessUserRequestField(t, reflect.ValueOf(&req).Elem().FieldByIndex(field.Index), field.Name)
			if reflect.DeepEqual(req, proxmox.UpdateAccessUserParams{}) {
				t.Fatalf("filling %s left the request empty; this test would pass without checking it", field.Name)
			}

			if got := accessUpdateDetailsJSON(t, userid, false, req); got != empty {
				t.Errorf("setting %s changed the audit details to %s, from %s — "+
					"audit rows are readable by every Viewer, so only enable, expire and groups may be recorded",
					field.Name, got, empty)
			}
		})
	}

	// The five fields that are never recorded, and Append, are the reason this
	// test exists. Fewer than that means the walk stopped seeing them and every
	// subtest above is vacuous.
	if visited < 6 {
		t.Fatalf("visited %d fields of proxmox.UpdateAccessUserParams, want at least the six that are never "+
			"recorded (comment, email, firstname, lastname, keys, append)", visited)
	}
}

// accessCreateDetailsJSON is the bytes a user create's audit details marshal to,
// which is what the audit row stores and a Viewer reads.
func accessCreateDetailsJSON(t *testing.T, userid string, hasPassword bool, fields proxmox.AccessUserFields) string {
	t.Helper()
	out, err := json.Marshal(accessUserCreateDetails(userid, hasPassword, fields))
	if err != nil {
		t.Fatalf("marshal the audit details: %v", err)
	}
	return string(out)
}

// TestAccessUserCreateDetails pins a user create's audit details to the exact
// JSON that reaches the row, one case for each thing the builder decides.
//
// The comparison is on the marshalled bytes because they are what the row stores
// and a Viewer reads. It is exact, so a key the builder gains fails every case
// that sets the field it would come from.
//
// One of the guards for these keys. TestGuard_AccessAuditDetailsCarryNoSecrets
// checks the builder's body and arguments (accessAuditBuilders) only for
// credential-shaped keys and reads, so the key set it returns is pinned here,
// across the fields type in
// TestAccessUserCreateDetailsRecordsOnlyTheAllowedFields, and through the real
// route in TestAccessCreateUserAuditRow.
func TestAccessUserCreateDetails(t *testing.T) {
	const (
		self  = "nexara@pve"
		alice = "alice@pve"
	)

	// Values of the fields the row must never carry, each one recognisable in the
	// output whatever key it might be recorded under. The group list is here too:
	// the row says a membership was set, not what it is.
	const (
		sentinelComment = "sentinel-comment"
		sentinelEmail   = "sentinel-email@example.com"
		sentinelFirst   = "sentinel-first"
		sentinelLast    = "sentinel-last"
		sentinelKeys    = "sentinel-keys"
		sentinelGroups  = "sentinel-group-list"
	)
	unrecorded := []string{sentinelComment, sentinelEmail, sentinelFirst, sentinelLast, sentinelKeys, sentinelGroups}

	str := func(s string) *string { return &s }
	b := func(v bool) *bool { return &v }
	i64 := func(v int64) *int64 { return &v }

	tests := []struct {
		name        string
		userid      string
		hasPassword bool
		fields      proxmox.AccessUserFields
		want        string
	}{
		{"nothing set", self, false, proxmox.AccessUserFields{},
			`{"has_password":false,"userid":"nexara@pve"}`},
		{"a password is recorded as the fact of it", alice, true, proxmox.AccessUserFields{},
			`{"has_password":true,"userid":"alice@pve"}`},

		{"comment, e-mail, names and keys are not recorded", self, false, proxmox.AccessUserFields{
			Comment:   str(sentinelComment),
			Email:     str(sentinelEmail),
			FirstName: str(sentinelFirst),
			LastName:  str(sentinelLast),
			Keys:      str(sentinelKeys),
		}, `{"has_password":false,"userid":"nexara@pve"}`},

		{"disabling is recorded", alice, false, proxmox.AccessUserFields{Enable: b(false)},
			`{"enable":false,"has_password":false,"userid":"alice@pve"}`},
		{"enabling is recorded", alice, false, proxmox.AccessUserFields{Enable: b(true)},
			`{"enable":true,"has_password":false,"userid":"alice@pve"}`},

		{"an expiry is recorded as sent", alice, false, proxmox.AccessUserFields{Expire: i64(1767225600)},
			`{"expire":1767225600,"has_password":false,"userid":"alice@pve"}`},
		{"an expiry of 0, never, is recorded as 0", alice, false, proxmox.AccessUserFields{Expire: i64(0)},
			`{"expire":0,"has_password":false,"userid":"alice@pve"}`},

		{"groups: the list is not recorded, the fact that it was set is", alice, false,
			proxmox.AccessUserFields{Groups: str(sentinelGroups)},
			`{"groups_set":true,"has_password":false,"userid":"alice@pve"}`},
		{"groups sent empty are still set", alice, false, proxmox.AccessUserFields{Groups: str("")},
			`{"groups_set":true,"has_password":false,"userid":"alice@pve"}`},

		{"every field at once carries only the five keys", self, true, proxmox.AccessUserFields{
			Comment:   str(sentinelComment),
			Email:     str(sentinelEmail),
			FirstName: str(sentinelFirst),
			LastName:  str(sentinelLast),
			Keys:      str(sentinelKeys),
			Groups:    str(sentinelGroups),
			Enable:    b(false),
			Expire:    i64(1767225600),
		}, `{"enable":false,"expire":1767225600,"groups_set":true,"has_password":true,"userid":"nexara@pve"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := accessCreateDetailsJSON(t, tc.userid, tc.hasPassword, tc.fields)
			if got != tc.want {
				t.Errorf("details = %s, want %s", got, tc.want)
			}
			for _, secret := range unrecorded {
				if strings.Contains(got, secret) {
					t.Errorf("details %s carry %q — audit rows are readable by every Viewer", got, secret)
				}
			}
		})
	}
}

// TestAccessUserCreateDetailsRecordsOnlyTheAllowedFields is the allow-list
// checked against the fields type rather than against a list of the fields that
// exist today.
//
// It walks every field of proxmox.AccessUserFields and sets each one, on its own,
// to a value that stands out. Every field has to be classified below, as recorded
// or as withheld, and one that is in neither fails: a field added later has to be
// decided by someone, in this test, in the builder and in its comment, before
// anything passes. A withheld field must leave the details what an empty
// request's are; a recorded one must add its own key and nothing else: enable
// and expire as they were given, groups as the fact that they were.
//
// Three checks are on the test itself. The filled fields must differ from an
// empty set (a fill that sets nothing would otherwise pass every subtest); a
// field of a type the fill does not know fails outright; and every field
// classified below must still be on the type, so that a walk which stopped seeing
// the fields, or a rename that left an entry behind, cannot leave the subtests
// vacuous.
func TestAccessUserCreateDetailsRecordsOnlyTheAllowedFields(t *testing.T) {
	const userid = "alice@pve"

	// recorded maps each field the row carries to the key it is carried under and
	// the JSON that key holds when the field is set by fillAccessUserRequestField:
	// enable and expire as sent, groups as the fact that they were set.
	recorded := map[string]struct{ key, value string }{
		"Enable": {"enable", "true"},
		"Expire": {"expire", "1700000000"},
		"Groups": {"groups_set", "true"},
	}
	withheld := map[string]bool{"Comment": true, "Email": true, "FirstName": true, "LastName": true, "Keys": true}

	keysOf := func(t *testing.T, details string) map[string]json.RawMessage {
		t.Helper()
		var out map[string]json.RawMessage
		if err := json.Unmarshal([]byte(details), &out); err != nil {
			t.Fatalf("the details %s are not a JSON object: %v", details, err)
		}
		return out
	}

	empty := accessCreateDetailsJSON(t, userid, false, proxmox.AccessUserFields{})

	seen := map[string]bool{}
	for _, field := range reflect.VisibleFields(reflect.TypeOf(proxmox.AccessUserFields{})) {
		if !field.IsExported() {
			continue
		}
		seen[field.Name] = true
		t.Run(field.Name, func(t *testing.T) {
			var set proxmox.AccessUserFields
			fillAccessUserRequestField(t, reflect.ValueOf(&set).Elem().FieldByIndex(field.Index), field.Name)
			if reflect.DeepEqual(set, proxmox.AccessUserFields{}) {
				t.Fatalf("filling %s left the fields empty; this test would pass without checking it", field.Name)
			}
			got := accessCreateDetailsJSON(t, userid, false, set)

			rec, isRecorded := recorded[field.Name]
			switch {
			case isRecorded:
				want := keysOf(t, empty)
				want[rec.key] = json.RawMessage(rec.value)
				if !reflect.DeepEqual(keysOf(t, got), want) {
					t.Errorf("setting %s made the audit details %s, want %s added to %s",
						field.Name, got, `"`+rec.key+`":`+rec.value, empty)
				}
			case withheld[field.Name]:
				if got != empty {
					t.Errorf("setting %s changed the audit details to %s, from %s — "+
						"audit rows are readable by every Viewer, so only enable, expire and groups_set may be recorded",
						field.Name, got, empty)
				}
			default:
				t.Errorf("%s is neither recorded nor withheld: decide whether a Viewer may read it, "+
					"then list it here, in accessUserCreateDetails and in its comment", field.Name)
			}
		})
	}

	for name := range recorded {
		if !seen[name] {
			t.Errorf("%s is listed as recorded, but proxmox.AccessUserFields has no such field", name)
		}
	}
	for name := range withheld {
		if !seen[name] {
			t.Errorf("%s is listed as withheld, but proxmox.AccessUserFields has no such field", name)
		}
	}
}

// accessTokenUpdateDetailsJSON is the bytes a token update's audit details
// marshal to, which is what the audit row stores and a Viewer reads.
func accessTokenUpdateDetailsJSON(t *testing.T, userid, tokenid string, force bool, req proxmox.UpdateAccessTokenParams) string {
	t.Helper()
	out, err := json.Marshal(accessTokenUpdateDetails(userid, tokenid, force, req))
	if err != nil {
		t.Fatalf("marshal the audit details: %v", err)
	}
	return string(out)
}

// TestAccessTokenUpdateDetails pins a token update's audit details to the exact
// JSON that reaches the row, one case for each thing the builder decides.
//
// The comparison is on the marshalled bytes because they are what the row stores
// and a Viewer reads. It is exact, so a key the builder gains fails every case
// that sets the field it would come from.
//
// One of the guards for these keys. TestGuard_AccessAuditDetailsCarryNoSecrets
// checks the builder's body and arguments (accessAuditBuilders) only for
// credential-shaped keys and reads, so the key set it returns is pinned here,
// across the request type in
// TestAccessTokenUpdateDetailsRecordsOnlyTheAllowedFields, and through the real
// route in TestAccessUpdateTokenAuditRow.
func TestAccessTokenUpdateDetails(t *testing.T) {
	const (
		self  = "nexara@pve"
		token = "api"
	)

	// The value of the field the row must never carry, recognisable in the output
	// whatever key it might be recorded under.
	const sentinelComment = "sentinel-comment"

	str := func(s string) *string { return &s }
	b := func(v bool) *bool { return &v }
	i64 := func(v int64) *int64 { return &v }

	tests := []struct {
		name            string
		userid, tokenid string
		force           bool
		req             proxmox.UpdateAccessTokenParams
		want            string
	}{
		{"nothing set", self, token, false, proxmox.UpdateAccessTokenParams{},
			`{"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"force on an update that sets nothing overrides nothing", self, token, true, proxmox.UpdateAccessTokenParams{},
			`{"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"force on a comment change overrides nothing, and the comment is not recorded", self, token, true,
			proxmox.UpdateAccessTokenParams{Comment: str(sentinelComment)},
			`{"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},

		{"regenerating", self, token, false, proxmox.UpdateAccessTokenParams{Regenerate: true},
			`{"forced":false,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`},
		{"regenerating, forced", self, token, true, proxmox.UpdateAccessTokenParams{Regenerate: true},
			`{"forced":true,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`},

		{"giving an expiry", self, token, false, proxmox.UpdateAccessTokenParams{Expire: i64(1767225600)},
			`{"expire":1767225600,"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"giving an expiry, forced", self, token, true, proxmox.UpdateAccessTokenParams{Expire: i64(1767225600)},
			`{"expire":1767225600,"forced":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"clearing the expiry is recorded as 0, and force on it overrides nothing", self, token, true,
			proxmox.UpdateAccessTokenParams{Expire: i64(0)},
			`{"expire":0,"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},

		{"turning privilege separation on", self, token, false, proxmox.UpdateAccessTokenParams{PrivSep: b(true)},
			`{"forced":false,"privsep":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"turning privilege separation on, forced", self, token, true, proxmox.UpdateAccessTokenParams{PrivSep: b(true)},
			`{"forced":true,"privsep":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"turning it off is recorded, and force on it overrides nothing", self, token, true,
			proxmox.UpdateAccessTokenParams{PrivSep: b(false)},
			`{"forced":false,"privsep":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},

		{"the token named is the one updated", "alice@pve", "ci", false, proxmox.UpdateAccessTokenParams{Regenerate: true},
			`{"forced":false,"regenerate":true,"tokenid":"ci","userid":"alice@pve"}`},

		{"every field at once carries only the six keys", self, token, true, proxmox.UpdateAccessTokenParams{
			Comment: str(sentinelComment), Expire: i64(1767225600), PrivSep: b(true), Regenerate: true},
			`{"expire":1767225600,"forced":true,"privsep":true,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := accessTokenUpdateDetailsJSON(t, tc.userid, tc.tokenid, tc.force, tc.req)
			if got != tc.want {
				t.Errorf("details = %s, want %s", got, tc.want)
			}
			if strings.Contains(got, sentinelComment) {
				t.Errorf("details %s carry the comment — audit rows are readable by every Viewer", got)
			}
		})
	}
}

// TestAccessTokenUpdateDetailsRecordsOnlyTheAllowedFields is the allow-list
// checked against the request type rather than against a list of the fields that
// exist today.
//
// It walks every field of proxmox.UpdateAccessTokenParams and sets each one, on
// its own, to a value that stands out. Every field has to be classified below, as
// recorded or as withheld, and one that is in neither fails: a field added later
// has to be decided by someone, in this test, in the builder and in its comment,
// before anything passes. A withheld field must leave the details what an empty
// request's are; a recorded one must change its own key and nothing else: expire
// and privsep as they were given, regenerate as the fact that it was.
//
// Three checks are on the test itself. The filled request must differ from an
// empty one (a fill that sets nothing would otherwise pass every subtest); a
// field of a type the fill does not know fails outright; and every field
// classified below must still be on the type, so that a walk which stopped seeing
// the fields, or a rename that left an entry behind, cannot leave the subtests
// vacuous.
func TestAccessTokenUpdateDetailsRecordsOnlyTheAllowedFields(t *testing.T) {
	const (
		userid  = "nexara@pve"
		tokenid = "api"
	)

	// recorded maps each field the row carries to the key it is carried under and
	// the JSON that key holds when the field is set by fillAccessUserRequestField.
	// The request is unforced, so forced stays false whichever field is filled.
	recorded := map[string]struct{ key, value string }{
		"Expire":     {"expire", "1700000000"},
		"PrivSep":    {"privsep", "true"},
		"Regenerate": {"regenerate", "true"},
	}
	withheld := map[string]bool{"Comment": true}

	object := func(t *testing.T, details string) map[string]json.RawMessage {
		t.Helper()
		var out map[string]json.RawMessage
		if err := json.Unmarshal([]byte(details), &out); err != nil {
			t.Fatalf("the details %s are not a JSON object: %v", details, err)
		}
		return out
	}

	empty := accessTokenUpdateDetailsJSON(t, userid, tokenid, false, proxmox.UpdateAccessTokenParams{})

	seen := map[string]bool{}
	for _, field := range reflect.VisibleFields(reflect.TypeOf(proxmox.UpdateAccessTokenParams{})) {
		if !field.IsExported() {
			continue
		}
		seen[field.Name] = true
		t.Run(field.Name, func(t *testing.T) {
			var req proxmox.UpdateAccessTokenParams
			fillAccessUserRequestField(t, reflect.ValueOf(&req).Elem().FieldByIndex(field.Index), field.Name)
			if reflect.DeepEqual(req, proxmox.UpdateAccessTokenParams{}) {
				t.Fatalf("filling %s left the request empty; this test would pass without checking it", field.Name)
			}
			got := accessTokenUpdateDetailsJSON(t, userid, tokenid, false, req)

			rec, isRecorded := recorded[field.Name]
			switch {
			case isRecorded:
				want := object(t, empty)
				want[rec.key] = json.RawMessage(rec.value)
				if !reflect.DeepEqual(object(t, got), want) {
					t.Errorf("setting %s made the audit details %s, want %s set in %s",
						field.Name, got, `"`+rec.key+`":`+rec.value, empty)
				}
			case withheld[field.Name]:
				if got != empty {
					t.Errorf("setting %s changed the audit details to %s, from %s — "+
						"audit rows are readable by every Viewer, so only expire, privsep and regenerate may be recorded",
						field.Name, got, empty)
				}
			default:
				t.Errorf("%s is neither recorded nor withheld: decide whether a Viewer may read it, "+
					"then list it here, in accessTokenUpdateDetails and in its comment", field.Name)
			}
		})
	}

	for name := range recorded {
		if !seen[name] {
			t.Errorf("%s is listed as recorded, but proxmox.UpdateAccessTokenParams has no such field", name)
		}
	}
	for name := range withheld {
		if !seen[name] {
			t.Errorf("%s is listed as withheld, but proxmox.UpdateAccessTokenParams has no such field", name)
		}
	}
}

// accessKeysProbe is the fixture value of a user's keys: findable in a body
// whatever key it is under, and obviously not a real key.
const accessKeysProbe = "PROBE-ACCESS-KEYS-NOT-A-REAL-VALUE"

// accessWire marshals what a user shaper returns and reads back the one user in
// it: the object itself, or the only element of an array. It returns the JSON as
// well, for a message or a substring check.
//
// The list shaper's result is passed as the slice it is, which is what
// RespondItems hands the encoder and whose elements are addressable, so a
// pointer-receiver MarshalJSON added to the proxmox type later is dispatched
// here as it would be in production; a copy of one element would hide it. The
// detail shaper's is passed as the value c.JSON is given.
func accessWire(t *testing.T, v any) ([]byte, map[string]json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	if len(raw) > 0 && raw[0] == '[' {
		var users []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &users); err != nil || len(users) != 1 {
			t.Fatalf("%s is not an array of one user (%v)", raw, err)
		}
		return raw, users[0]
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("%s is not a JSON object: %v", raw, err)
	}
	return raw, fields
}

// TestAccessUserReadsWithholdKeys pins what the two user reads make of a user's
// keys, one case for each thing has_keys has to tell apart. See
// accessUserResponse for what keys holds and why both reads, which every Viewer
// can make, must not send it.
//
// Each response is read back as the JSON a Viewer receives: the value is not in
// it, the key is not there even blank, and has_keys says whether Proxmox sent
// anything but whitespace. The response is also examined as a value, because it
// must hold no copy of keys: that is what keeps the body's silence from resting
// on the json tag alone. TestReadStructsStripCredentials makes the same demand
// of a probe in every field, and TestAccessUserReadsCarryEveryOtherField checks
// that nothing else went with it.
func TestAccessUserReadsWithholdKeys(t *testing.T) {
	tests := []struct {
		name    string
		keys    string
		hasKeys bool
	}{
		{"a value", accessKeysProbe, true},
		{"a value with whitespace around it", " \t" + accessKeysProbe + "\n", true},
		{"none", "", false},
		{"spaces only", "   ", false},
		{"a tab and a newline only", "\t\n", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list := accessUsersForRead([]proxmox.AccessUser{{UserID: "alice@pve", Keys: tc.keys}})
			if len(list) != 1 {
				t.Fatalf("the list shaper turned one user into %d entries", len(list))
			}
			detail := accessUserDetailForRead(proxmox.AccessUserDetail{UserID: "alice@pve", Keys: tc.keys})

			responses := map[string]struct {
				body any
				// held is the keys value the response still carries as a value.
				held string
			}{
				"list":   {list, list[0].AccessUser.Keys},
				"detail": {detail, detail.AccessUserDetail.Keys},
			}
			for name, resp := range responses {
				raw, fields := accessWire(t, resp.body)

				if strings.Contains(string(raw), accessKeysProbe) {
					t.Errorf("%s: the keys value reached the body: %s", name, raw)
				}
				if _, present := fields["keys"]; present {
					t.Errorf("%s: the body carries a keys key; it should be absent, not blank: %s", name, raw)
				}
				if resp.held != "" {
					t.Errorf("%s: the response still holds the keys value %q", name, resp.held)
				}
				if got, want := string(fields["has_keys"]), strconv.FormatBool(tc.hasKeys); got != want {
					t.Errorf("%s: has_keys = %q, want %s for keys %q: %s", name, got, want, tc.keys, raw)
				}
			}
		})
	}
}

// accessFilled returns a T, one of the Proxmox access structs the reads shape (a
// user, a user's detail, or a realm), with every field set to something that
// stands out: each string is a probe, a user's keys among them.
func accessFilled[T any](t *testing.T) T {
	t.Helper()
	var user T
	v := reflect.ValueOf(&user).Elem()
	for i := range v.NumField() {
		field, slot := v.Type().Field(i), v.Field(i)
		switch {
		case slot.Kind() == reflect.String:
			slot.SetString(probeValue(v.Type().Name(), jsonName(field)))
		case slot.Kind() == reflect.Bool:
			slot.SetBool(true)
		case slot.CanInt():
			slot.SetInt(7)
		case slot.Kind() == reflect.Slice && slot.Type().Elem().Kind() == reflect.String:
			slot.Set(reflect.ValueOf([]string{"group-a", "group-b"}))
		default:
			t.Fatalf("%s.%s is a %s; teach this test to fill it", v.Type().Name(), field.Name, slot.Type())
		}
	}
	return user
}

// requireOnlyKeysChanged runs a filled T through shape and requires the JSON it
// marshals to be the struct's own JSON without keys, and with has_keys.
func requireOnlyKeysChanged[T any](t *testing.T, shape func(T) any) {
	t.Helper()
	for _, hasKeys := range []bool{true, false} {
		user := accessFilled[T](t)
		if !hasKeys {
			reflect.ValueOf(&user).Elem().FieldByName("Keys").SetString("")
		}

		_, want := accessWire(t, user)
		// A fill that reached nothing would leave both sides near-empty and the
		// comparison below true of anything.
		if _, ok := want["keys"]; !ok || len(want) < 6 {
			t.Fatalf("the filled %T marshals to %d fields, keys among them or not: the fill did not reach them", user, len(want))
		}
		delete(want, "keys")
		want["has_keys"] = json.RawMessage(strconv.FormatBool(hasKeys))

		_, got := accessWire(t, shape(user))
		for name, w := range want {
			switch g, ok := got[name]; {
			case !ok:
				t.Errorf("%T (has_keys %v): %q is missing from the response", user, hasKeys, name)
			case string(g) != string(w):
				t.Errorf("%T (has_keys %v): %q is %s in the response, want %s", user, hasKeys, name, g, w)
			}
		}
		for name := range got {
			if _, ok := want[name]; !ok {
				t.Errorf("%T (has_keys %v): the response carries %q, which is not a field of the user", user, hasKeys, name)
			}
		}
	}
}

// TestAccessUserReadsCarryEveryOtherField compares each response with the
// Proxmox struct's own JSON: the same fields with the same values, minus keys,
// plus has_keys. The SPA reads the rest of a user from these responses.
//
// Both structs are filled by reflection, so a field Proxmox adds to either later
// has to reach the response to pass. That is what stops the embedding in
// accessUserResponse from being swapped for a hand-copied field list that leaves
// one out. A field of a kind the fill does not know fails outright rather than
// being skipped. The list is marshalled as the slice the shaper returns, and the
// detail as a value: see accessWire.
func TestAccessUserReadsCarryEveryOtherField(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		requireOnlyKeysChanged(t, func(u proxmox.AccessUser) any {
			return accessUsersForRead([]proxmox.AccessUser{u})
		})
	})
	t.Run("detail", func(t *testing.T) {
		requireOnlyKeysChanged(t, func(u proxmox.AccessUserDetail) any {
			return accessUserDetailForRead(u)
		})
	})
}
