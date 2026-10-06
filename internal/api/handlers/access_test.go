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

// The access routes are declared endpoints (internal/api/registry_access.go): their
// permissions and parameter rules are tested against the REAL declarations in
// internal/api/registry_access_test.go, not here. What stays here is the handlers'
// own: the percent-decode of a path identifier, the self-credential guard, which
// edits count as capable of severing access, the exact audit details of a user
// create, a user edit and a token update, the static audit-detail guard, and what
// the two user reads make of a user's two-factor keys.

func accessPtr[T any](v T) *T { return &v }

func TestSplitFullTokenID(t *testing.T) {
	for _, tc := range []struct {
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
	} {
		u, k, ok := splitFullTokenID(tc.in)
		if ok != tc.ok || u != tc.user || k != tc.tok {
			t.Errorf("splitFullTokenID(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.in, u, k, ok, tc.user, tc.tok, tc.ok)
		}
	}
}

// TestSelfCredentialSubject covers the guard that stops Nexara destroying its own
// cluster credential. The empty-tokenid cases matter most: deleting a user takes its
// tokens with it, so that collides on the user alone.
func TestSelfCredentialSubject(t *testing.T) {
	const own = "nexara@pve!api"
	const bang = "svc!x@pve!api" // a user name that contains "!": the cut is at the last one

	for _, tc := range []struct {
		name            string
		ownTokenID      string
		userid, tokenid string
		wantConflict    bool
		wantSubject     string
	}{
		{"exact token match", own, "nexara@pve", "api", true, "token nexara@pve!api"},
		{"case-insensitive", own, "NEXARA@PVE", "API", true, "token nexara@pve!api"},
		{"user delete takes our token", own, "nexara@pve", "", true, "user nexara@pve"},
		{"user delete, in another case", own, "NEXARA@PVE", "", true, "user nexara@pve"},
		{"different token, same user", own, "nexara@pve", "other", false, ""},
		{"different user", own, "alice@pve", "api", false, ""},
		{"different user, whole-user delete", own, "alice@pve", "", false, ""},
		{"cluster token id has no bang", "nexara@pve", "nexara@pve", "api", false, ""},
		{"cluster token id empty", "", "nexara@pve", "api", false, ""},

		{"bang: exact token match", bang, "svc!x@pve", "api", true, "token svc!x@pve!api"},
		{"bang: case-insensitive", bang, "SVC!X@PVE", "API", true, "token svc!x@pve!api"},
		{"bang: user delete takes our token", bang, "svc!x@pve", "", true, "user svc!x@pve"},
		{"bang: different token", bang, "svc!x@pve", "other", false, ""},
		{"bang: different account", bang, "svc!y@pve", "api", false, ""},
		{"bang: the leading part of it is another account", bang, "svc@pve", "api", false, ""},
	} {
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

// TestSelfCredentialRefusal is the table of guardSelfCredential's decision, with no
// database or Proxmox: what the request names, what the lookup found, and whether the
// caller sent force. Which request names Nexara's credential is
// TestSelfCredentialSubject's; the rows here are the decision over it.
//
// A cluster row that could not be read used to let an unforced delete of Nexara's own
// token through; it is a refusal now, and never the 409 that has the SPA open an
// override saying the action WILL cut Nexara off, which nothing here knows. The 404/500
// split is CreateProxmoxClient's own for a cluster row it cannot read.
func TestSelfCredentialRefusal(t *testing.T) {
	const own = "nexara@pve!api"

	// A lookup failure whose text names an address, to prove the answer does not carry it.
	const leak = "192.0.2.10:5432"
	dbDown := fmt.Errorf("dial tcp %s: connection refused", leak)
	cantCheck := []string{"could not check", "nothing was changed"}
	conflict := func(subject string) []string { return []string{subject, "force=true"} }

	tests := []struct {
		name            string
		lookupErr       error
		ownTokenID      string
		userid, tokenid string
		wantStatus      int      // the refusal's status; 0 means the request may go on
		wantIn          []string // fragments the refusal's message must carry
	}{
		// Not Nexara's own credential.
		{"another token of the same user", nil, own, "nexara@pve", "other", 0, nil},
		{"another user, the whole account", nil, own, "alice@pve", "", 0, nil},
		{"the cluster's token id has no bang", nil, "nexara@pve", "nexara@pve", "api", 0, nil},
		{"the cluster's token id is empty", nil, "", "nexara@pve", "api", 0, nil},

		// Nexara's own credential, unforced: the conflict, and the way past it. The
		// message covers a narrowing edit and one undone in Proxmox as well as a cut-off,
		// since the guard covers all of them.
		{"the token", nil, own, "nexara@pve", "api", fiber.StatusConflict,
			append(conflict("token nexara@pve!api"), "cut Nexara off", "take away permissions it relies on", "undone in Proxmox")},
		{"the user that owns it", nil, own, "nexara@pve", "", fiber.StatusConflict, conflict("user nexara@pve")},

		// Could not tell: a refusal, and one that is not a conflict.
		{"the cluster row cannot be read", dbDown, "", "nexara@pve", "api", fiber.StatusInternalServerError, cantCheck},
		{"the cluster row cannot be read, and the request names another user's token", dbDown, "", "alice@pve", "other", fiber.StatusInternalServerError, cantCheck},
		{"the read failed with a wrapped error", fmt.Errorf("get cluster: %w", dbDown), "", "nexara@pve", "api", fiber.StatusInternalServerError, cantCheck},
		{"the read ran out of time", context.DeadlineExceeded, "", "nexara@pve", "api", fiber.StatusInternalServerError, cantCheck},
		{"the request was cancelled mid-read", context.Canceled, "", "nexara@pve", "api", fiber.StatusInternalServerError, cantCheck},
		{"the handler has no database", errAccessNoQueries, "", "nexara@pve", "api", fiber.StatusInternalServerError, cantCheck},
		// A token id left beside the error is stale, not an answer: the failure decides,
		// whether or not the leftover would have matched.
		{"an error beside an id that matches is not a conflict", dbDown, own, "nexara@pve", "api", fiber.StatusInternalServerError, []string{"could not check"}},
		{"an error beside an id that does not match is not a pass", dbDown, own, "alice@pve", "other", fiber.StatusInternalServerError, []string{"could not check"}},

		{"the cluster does not exist", pgx.ErrNoRows, "", "nexara@pve", "api", fiber.StatusNotFound, []string{"Cluster not found"}},
		{"the cluster does not exist, wrapped", fmt.Errorf("get cluster: %w", pgx.ErrNoRows), "", "nexara@pve", "api", fiber.StatusNotFound, []string{"Cluster not found"}},
		{"the cluster does not exist, whoever the request names", pgx.ErrNoRows, "", "alice@pve", "", fiber.StatusNotFound, []string{"Cluster not found"}},
	}

	// statusOf is the refusal's status, 0 for none, and fails the test on an error that
	// is not the fiber.Error every refusal is.
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
			status, message := statusOf(t, selfCredentialRefusal(false, tc.lookupErr, tc.ownTokenID, tc.userid, tc.tokenid))
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

	// The property the rows illustrate, held over every combination rather than the ones
	// somebody thought of: force always lets the request go on (it does not depend on the
	// lookup), and an unforced request whose row could not be read never does — and never
	// answers with the conflict, which is only for a credential known to be Nexara's.
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
// cannot read the cluster row. Warn is for a context that has ended, cancelled or out
// of time: the caller went away, and the database is not at fault. It is decided by
// ctx.Err() and not by the failure's chain, because a context error in the chain can
// be the driver's own timeout under a live context, and that is the database's, an
// Error. Fiber's c.Context() is Background unless SetContext is called, so no request
// reaches the Warn today; the contexts that have ended are made by hand here. A
// cluster that is not there is not logged, whatever the context.
func TestLogGuardLookupFailure(t *testing.T) {
	clusterID := uuid.MustParse("cccccccc-0000-0000-0000-000000000006")
	dbDown := errors.New("dial tcp 192.0.2.10:5432: connection refused")

	live := context.Background()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, release := context.WithTimeout(context.Background(), 0)
	defer release()

	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		// wantLevel is the level of the one record the guard writes; "" is none.
		wantLevel string
	}{
		{"the database fails", live, dbDown, "ERROR"},
		{"the driver's own timeout, under a live context", live, fmt.Errorf("get cluster: %w", context.DeadlineExceeded), "ERROR"},
		{"a query the driver cancelled, under a live context", live, fmt.Errorf("get cluster: %w", context.Canceled), "ERROR"},

		{"the request was cancelled", cancelled, fmt.Errorf("get cluster: %w", context.Canceled), "WARN"},
		{"the request ran out of time", expired, fmt.Errorf("get cluster: %w", context.DeadlineExceeded), "WARN"},
		{"the context has ended, whatever the failure was", cancelled, dbDown, "WARN"},

		{"the cluster does not exist", live, fmt.Errorf("get cluster: %w", pgx.ErrNoRows), ""},
		{"the cluster does not exist, under a context that has ended", cancelled, fmt.Errorf("get cluster: %w", pgx.ErrNoRows), ""},
		{"nothing failed", live, nil, ""},
	} {
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
// json.Marshal in place of a map literal. The guard below holds a builder's body, and
// the arguments it is called with, to its rules about credential-shaped keys and
// reads, but it cannot tell what the builder records under any other name: an entry
// is also the decision to trust the tests that compare what the builder returns
// exactly (so a key it gains fails one whether or not it looks like a credential),
// the one driven through the real route in internal/api (TestAccessUpdateUserAuditRow,
// TestAccessCreateUserAuditRow, TestAccessUpdateTokenAuditRow), and a comment on the
// builder naming them.
var accessAuditBuilders = map[string]bool{
	"accessUserUpdateDetails":  true,
	"accessUserCreateDetails":  true,
	"accessTokenUpdateDetails": true,
}

// isAccessAuditBuilderCall reports whether arg is a call to a function listed in
// accessAuditBuilders. Only a bare call counts, and only one whose name resolves to a
// top-level function (the parser's own lexical resolution, ast.Ident.Obj): a method, a
// package-qualified function of the same name, a local closure or a parameter that
// shadows it is some other function.
func isAccessAuditBuilderCall(arg ast.Expr) bool {
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return false
	}
	name, ok := call.Fun.(*ast.Ident)
	return ok && accessAuditBuilders[name.Name] && name.Obj != nil && name.Obj.Kind == ast.Fun
}

// TestGuard_AccessAuditDetailsCarryNoSecrets is a static guard over access.go, whose
// audit rows are readable by every built-in Viewer (view:audit) and where a token
// secret, which Proxmox returns exactly once, lives. Every one-argument json.Marshal
// must be handed a map literal or a call to a builder in accessAuditBuilders; those, and
// a builder's own body, may carry no credential-shaped key and read no credential (a
// selector named .Value, .Password, .TokenSecret, .Secret or .Keys, or a call such as
// p.String("password")). Any other argument hides its keys, so it is refused.
// Not seen: json.MarshalIndent, an Encoder, a hand-built RawMessage, a key held in a
// constant, a credential kept under another name. It is aimed at the realistic mistake,
// someone adding `"secret": created.Value`, not at deliberate laundering.
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
			// Unreachable: ParseFile rejects a malformed string literal before this runs.
			return "", false
		}
		return strings.ToLower(s), true
	}

	// scanKeys refuses a credential-shaped name used as a string-literal key anywhere
	// under node: a key of a map literal, nested maps included, or the index of an
	// assignment such as details["keys"] = ….
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

	// scanReads refuses a read of a credential anywhere under node, whatever key the
	// value sits under: details{"x": created.Value} is the leak.
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

	// An entry names a function this file declares: one that outlived its function would
	// exempt whatever is later given that name.
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

// TestGuard_AccessAuditDetailsNeverReadTheRawParams is the other half of the guard
// above: apischema.Params.Raw() returns every validated parameter, including
// `password` on the user-create route, and handing it to json.Marshal would write that
// password into a row every Viewer can read. No call site does this today.
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

// TestAccessParamDecodesPercentEncoding is a regression test for a feature-breaking
// bug. Fiber v3 does not percent-decode path params, and a PVE user id is
// "name@realm", so any correct client sends encodeURIComponent("nexara@pve") =
// "nexara%40pve". Using that raw value handed the validator a string with "%" and no
// "@", which it rejected — every user, token, group, role and realm lookup 400'd. The
// decode must stay paired with validation afterwards: "%2e%2e" decodes to ".." and the
// proxmox client's validators reject it (TestAccessMethodsRejectInjectionWithoutIssuingRequest).
func TestAccessParamDecodesPercentEncoding(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"nexara%40pve", "nexara@pve"},
		{"nexara@pve", "nexara@pve"},
		{"root%40pam", "root@pam"},
		{"first.last-1%40pve", "first.last-1@pve"},
		{"%2e%2e", ".."}, // a traversal; safe to decode first because the client rejects it downstream
	} {
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

	// The malformed-escape branch: the declared patterns admit only a well-formed "%XX",
	// so no request reaches it today; naming the parameter is what makes the 400 actionable.
	if _, err := accessParam("nexara%zz", "userid"); err == nil {
		t.Error("accessParam accepted a malformed percent-escape")
	} else if !strings.Contains(err.Error(), "userid") {
		t.Errorf("the rejection does not name the parameter: %v", err)
	}
}

// TestAccessUpdateAffectsAccess covers which user edits are treated as capable of
// severing Nexara's own cluster access. Guarding too little lets a PUT disable
// nexara@pve with no confirmation (every later token call 401s); guarding too much
// makes routine comment edits demand a force flag and trains operators to pass it
// without reading.
func TestAccessUpdateAffectsAccess(t *testing.T) {
	edit := func(f proxmox.AccessUserFields) proxmox.UpdateAccessUserParams {
		return proxmox.UpdateAccessUserParams{AccessUserFields: f}
	}
	for _, tc := range []struct {
		name string
		req  proxmox.UpdateAccessUserParams
		want bool
	}{
		{"empty update", proxmox.UpdateAccessUserParams{}, false},
		{"comment only", edit(proxmox.AccessUserFields{Comment: accessPtr("hi")}), false},
		{"email only", edit(proxmox.AccessUserFields{Email: accessPtr("a@b.c")}), false},
		{"explicitly enabling", edit(proxmox.AccessUserFields{Enable: accessPtr(true)}), false},
		{"expire cleared to never", edit(proxmox.AccessUserFields{Expire: accessPtr[int64](0)}), false},

		{"disabling", edit(proxmox.AccessUserFields{Enable: accessPtr(false)}), true},
		{"setting an expiry", edit(proxmox.AccessUserFields{Expire: accessPtr[int64](1767225600)}), true},
		{"changing groups", edit(proxmox.AccessUserFields{Groups: accessPtr("admins")}), true},
		{"clearing groups", edit(proxmox.AccessUserFields{Groups: accessPtr("")}), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessUpdateAffectsAccess(tc.req); got != tc.want {
				t.Errorf("accessUpdateAffectsAccess = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAccessTokenUpdateAffectsAccess covers which token updates are treated as
// capable of stopping the token working or narrowing what it may do. The direction of
// privsep is the row worth a second look: a token without privilege separation has
// exactly its owning user's permissions, one with it only the token's own ACL entries
// intersected with the user's (PVE::RPCEnvironment permissions), so turning separation
// ON can only take permissions away and OFF can only add them. True is guarded, and
// false — which can never make Nexara's token weaker — is not.
func TestAccessTokenUpdateAffectsAccess(t *testing.T) {
	hi, never, soon := accessPtr("hi"), accessPtr[int64](0), accessPtr[int64](1767225600)
	for _, tc := range []struct {
		name string
		req  proxmox.UpdateAccessTokenParams
		want bool
	}{
		{"empty update", proxmox.UpdateAccessTokenParams{}, false},
		{"comment only", proxmox.UpdateAccessTokenParams{Comment: hi}, false},
		{"clearing the comment", proxmox.UpdateAccessTokenParams{Comment: accessPtr("")}, false},
		{"expire cleared to never", proxmox.UpdateAccessTokenParams{Expire: never}, false},
		{"turning privilege separation off", proxmox.UpdateAccessTokenParams{PrivSep: accessPtr(false)}, false},
		{"everything harmless at once", proxmox.UpdateAccessTokenParams{Comment: hi, Expire: never, PrivSep: accessPtr(false)}, false},

		{"regenerating", proxmox.UpdateAccessTokenParams{Regenerate: true}, true},
		{"regenerating with harmless fields beside it", proxmox.UpdateAccessTokenParams{Regenerate: true, Comment: hi, Expire: never, PrivSep: accessPtr(false)}, true},
		{"setting an expiry", proxmox.UpdateAccessTokenParams{Expire: soon}, true},
		{"an expiry already in the past is judged by being set, not by the clock", proxmox.UpdateAccessTokenParams{Expire: accessPtr[int64](1)}, true},
		{"turning privilege separation on", proxmox.UpdateAccessTokenParams{PrivSep: accessPtr(true)}, true},
		{"turning it on beside an expiry of 0", proxmox.UpdateAccessTokenParams{PrivSep: accessPtr(true), Expire: never}, true},
		{"turning it on beside a comment", proxmox.UpdateAccessTokenParams{PrivSep: accessPtr(true), Comment: hi}, true},
		{"every field", proxmox.UpdateAccessTokenParams{Comment: hi, Expire: soon, PrivSep: accessPtr(true), Regenerate: true}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessTokenUpdateAffectsAccess(tc.req); got != tc.want {
				t.Errorf("accessTokenUpdateAffectsAccess = %v, want %v", got, tc.want)
			}
		})
	}
}

// accessDetailsJSON is the bytes an audit-details builder's result marshals to, which
// is what the audit row stores and a Viewer reads.
func accessDetailsJSON(t *testing.T, details any) string {
	t.Helper()
	out, err := json.Marshal(details)
	if err != nil {
		t.Fatalf("marshal the audit details: %v", err)
	}
	return string(out)
}

// Values of the fields the user audit rows must never carry, each recognisable in the
// output whatever key it might be recorded under. The group list is among them: the
// row says a membership was set, not what it became.
const (
	accessSentinelComment = "sentinel-comment"
	accessSentinelEmail   = "sentinel-email@example.com"
	accessSentinelFirst   = "sentinel-first"
	accessSentinelLast    = "sentinel-last"
	accessSentinelKeys    = "sentinel-keys"
	accessSentinelGroups  = "sentinel-group-list"
)

var accessSentinelUnrecorded = []string{accessSentinelComment, accessSentinelEmail, accessSentinelFirst, accessSentinelLast, accessSentinelKeys, accessSentinelGroups}

// accessSentinelFields sets every field of a user's request that the audit rows keep
// out, to its sentinel.
func accessSentinelFields() proxmox.AccessUserFields {
	return proxmox.AccessUserFields{
		Comment:   accessPtr(accessSentinelComment),
		Email:     accessPtr(accessSentinelEmail),
		FirstName: accessPtr(accessSentinelFirst),
		LastName:  accessPtr(accessSentinelLast),
		Keys:      accessPtr(accessSentinelKeys),
		Groups:    accessPtr(accessSentinelGroups),
		Enable:    accessPtr(false),
		Expire:    accessPtr[int64](1767225600),
	}
}

// TestAccessUserUpdateDetails pins a user update's audit details to the exact JSON
// that reaches the row, one case for each thing the builder decides. The comparison is
// exact, so a key the builder gains fails every case that sets the field it would
// come from. TestGuard_AccessAuditDetailsCarryNoSecrets checks the builder's body only
// for credential-shaped keys and reads, so the key set it returns is pinned here,
// across the request type in TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields,
// and through the real route in TestAccessUpdateUserAuditRow.
func TestAccessUserUpdateDetails(t *testing.T) {
	const self = "nexara@pve"
	edit := func(f proxmox.AccessUserFields) proxmox.UpdateAccessUserParams {
		return proxmox.UpdateAccessUserParams{AccessUserFields: f}
	}
	for _, tc := range []struct {
		name   string
		userid string
		force  bool
		req    proxmox.UpdateAccessUserParams
		want   string
	}{
		{"nothing set", self, false, edit(proxmox.AccessUserFields{}), `{"forced":false,"userid":"nexara@pve"}`},
		{"force on an edit that sets nothing overrides nothing", self, true, edit(proxmox.AccessUserFields{}), `{"forced":false,"userid":"nexara@pve"}`},
		{"force on a comment change overrides nothing", self, true, edit(proxmox.AccessUserFields{Comment: accessPtr(accessSentinelComment)}),
			`{"forced":false,"userid":"nexara@pve"}`},

		{"disabling", self, false, edit(proxmox.AccessUserFields{Enable: accessPtr(false)}), `{"enable":false,"forced":false,"userid":"nexara@pve"}`},
		{"disabling, forced", self, true, edit(proxmox.AccessUserFields{Enable: accessPtr(false)}), `{"enable":false,"forced":true,"userid":"nexara@pve"}`},
		{"enabling is recorded although the guard ignores it", self, true, edit(proxmox.AccessUserFields{Enable: accessPtr(true)}),
			`{"enable":true,"forced":false,"userid":"nexara@pve"}`},

		{"giving an expiry, forced", self, true, edit(proxmox.AccessUserFields{Expire: accessPtr[int64](1767225600)}),
			`{"expire":1767225600,"forced":true,"userid":"nexara@pve"}`},
		{"clearing the expiry is recorded as 0", self, false, edit(proxmox.AccessUserFields{Expire: accessPtr[int64](0)}),
			`{"expire":0,"forced":false,"userid":"nexara@pve"}`},

		{"replacing groups, forced: the list is not recorded", self, true, edit(proxmox.AccessUserFields{Groups: accessPtr(accessSentinelGroups)}),
			`{"forced":true,"groups_changed":true,"userid":"nexara@pve"}`},
		{"removing every group", self, false, edit(proxmox.AccessUserFields{Groups: accessPtr("")}),
			`{"forced":false,"groups_changed":true,"userid":"nexara@pve"}`},
		{"appending groups records what replacing them does", self, true,
			proxmox.UpdateAccessUserParams{AccessUserFields: proxmox.AccessUserFields{Groups: accessPtr(accessSentinelGroups)}, Append: true},
			`{"forced":true,"groups_changed":true,"userid":"nexara@pve"}`},
		{"append without groups sets no groups", self, true, proxmox.UpdateAccessUserParams{Append: true}, `{"forced":false,"userid":"nexara@pve"}`},

		{"the account named is the one edited", "alice@pve", false, edit(proxmox.AccessUserFields{Enable: accessPtr(false)}),
			`{"enable":false,"forced":false,"userid":"alice@pve"}`},
		{"every field at once carries only the three", self, true,
			proxmox.UpdateAccessUserParams{AccessUserFields: accessSentinelFields(), Append: true},
			`{"enable":false,"expire":1767225600,"forced":true,"groups_changed":true,"userid":"nexara@pve"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := accessDetailsJSON(t, accessUserUpdateDetails(tc.userid, tc.force, tc.req))
			if got != tc.want {
				t.Errorf("details = %s, want %s", got, tc.want)
			}
			for _, secret := range accessSentinelUnrecorded {
				if strings.Contains(got, secret) {
					t.Errorf("details %s carry %q — audit rows are readable by every Viewer", got, secret)
				}
			}
		})
	}
}

// fillAccessUserRequestField sets slot, a pointer or a plain value, to something that
// stands out: a string carrying the field's name, true, or 1700000000. The reflection
// tests over the request types use it to set one field at a time; a field of a type it
// does not know fails the test outright rather than being skipped.
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

// TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields is the allow-list checked
// against the request type rather than a list of the fields that exist today. It walks
// every field of proxmox.UpdateAccessUserParams, the embedded AccessUserFields
// flattened and Append beside it, and sets each one that is not recorded, on its own,
// to a value that stands out: the details must stay what an empty edit's are. A field
// added later and recorded by mistake fails here; one the row is meant to carry belongs
// in recorded below, in the builder and in its comment. The filled request must differ
// from an empty one, a field of a type the fill does not know fails outright, and the
// walk must still see the fields it exists for.
func TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields(t *testing.T) {
	const userid = "nexara@pve"
	recorded := map[string]bool{"Enable": true, "Expire": true, "Groups": true}
	build := func(t *testing.T, req proxmox.UpdateAccessUserParams) string {
		return accessDetailsJSON(t, accessUserUpdateDetails(userid, false, req))
	}
	empty := build(t, proxmox.UpdateAccessUserParams{})

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
			if got := build(t, req); got != empty {
				t.Errorf("setting %s changed the audit details to %s, from %s — "+
					"audit rows are readable by every Viewer, so only enable, expire and groups may be recorded", field.Name, got, empty)
			}
		})
	}
	// The five fields that are never recorded, and Append, are the reason this test
	// exists; fewer means the walk stopped seeing them and every subtest is vacuous.
	if visited < 6 {
		t.Fatalf("visited %d fields of proxmox.UpdateAccessUserParams, want at least the six that are never "+
			"recorded (comment, email, firstname, lastname, keys, append)", visited)
	}
}

// accessRequireDetailsClassified walks every field of T, sets each on its own to a
// value that stands out, and holds build's audit details to the field's class:
// recorded (its own key set, as the JSON value given, and nothing else changed) or
// withheld (the details stay what an empty request's are). A field in neither fails —
// a field added later has to be decided by someone, in the test, the builder and its
// comment, before anything passes — and so does a classified name that is no longer a
// field, so that a rename cannot leave the subtests vacuous.
func accessRequireDetailsClassified[T any](t *testing.T, build func(*testing.T, T) string,
	recorded map[string]struct{ key, value string }, withheld map[string]bool, allowed string) {
	t.Helper()
	object := func(t *testing.T, details string) map[string]json.RawMessage {
		t.Helper()
		var out map[string]json.RawMessage
		if err := json.Unmarshal([]byte(details), &out); err != nil {
			t.Fatalf("the details %s are not a JSON object: %v", details, err)
		}
		return out
	}
	var zero T
	empty := build(t, zero)

	seen := map[string]bool{}
	for _, field := range reflect.VisibleFields(reflect.TypeFor[T]()) {
		if !field.IsExported() {
			continue
		}
		seen[field.Name] = true
		t.Run(field.Name, func(t *testing.T) {
			var req T
			fillAccessUserRequestField(t, reflect.ValueOf(&req).Elem().FieldByIndex(field.Index), field.Name)
			if reflect.DeepEqual(req, zero) {
				t.Fatalf("filling %s left the request empty; this test would pass without checking it", field.Name)
			}
			got := build(t, req)
			rec, isRecorded := recorded[field.Name]
			switch {
			case isRecorded:
				want := object(t, empty)
				want[rec.key] = json.RawMessage(rec.value)
				if !reflect.DeepEqual(object(t, got), want) {
					t.Errorf("setting %s made the audit details %s, want %s set in %s", field.Name, got, `"`+rec.key+`":`+rec.value, empty)
				}
			case withheld[field.Name]:
				if got != empty {
					t.Errorf("setting %s changed the audit details to %s, from %s — audit rows are readable by every Viewer, so only %s may be recorded",
						field.Name, got, empty, allowed)
				}
			default:
				t.Errorf("%s is neither recorded nor withheld: decide whether a Viewer may read it, then list it here, in its builder and in its comment", field.Name)
			}
		})
	}
	for name := range recorded {
		if !seen[name] {
			t.Errorf("%s is listed as recorded, but %s has no such field", name, reflect.TypeFor[T]())
		}
	}
	for name := range withheld {
		if !seen[name] {
			t.Errorf("%s is listed as withheld, but %s has no such field", name, reflect.TypeFor[T]())
		}
	}
}

// TestAccessUserCreateDetails pins a user create's audit details to the exact JSON
// that reaches the row, one case for each thing the builder decides (see
// TestAccessUserUpdateDetails). The route-level check is TestAccessCreateUserAuditRow.
func TestAccessUserCreateDetails(t *testing.T) {
	const (
		self  = "nexara@pve"
		alice = "alice@pve"
	)
	for _, tc := range []struct {
		name        string
		userid      string
		hasPassword bool
		fields      proxmox.AccessUserFields
		want        string
	}{
		{"nothing set", self, false, proxmox.AccessUserFields{}, `{"has_password":false,"userid":"nexara@pve"}`},
		{"a password is recorded as the fact of it", alice, true, proxmox.AccessUserFields{}, `{"has_password":true,"userid":"alice@pve"}`},
		{"comment, e-mail, names and keys are not recorded", self, false, proxmox.AccessUserFields{
			Comment: accessPtr(accessSentinelComment), Email: accessPtr(accessSentinelEmail), FirstName: accessPtr(accessSentinelFirst),
			LastName: accessPtr(accessSentinelLast), Keys: accessPtr(accessSentinelKeys)}, `{"has_password":false,"userid":"nexara@pve"}`},

		{"disabling is recorded", alice, false, proxmox.AccessUserFields{Enable: accessPtr(false)}, `{"enable":false,"has_password":false,"userid":"alice@pve"}`},
		{"enabling is recorded", alice, false, proxmox.AccessUserFields{Enable: accessPtr(true)}, `{"enable":true,"has_password":false,"userid":"alice@pve"}`},
		{"an expiry is recorded as sent", alice, false, proxmox.AccessUserFields{Expire: accessPtr[int64](1767225600)},
			`{"expire":1767225600,"has_password":false,"userid":"alice@pve"}`},
		{"an expiry of 0, never, is recorded as 0", alice, false, proxmox.AccessUserFields{Expire: accessPtr[int64](0)},
			`{"expire":0,"has_password":false,"userid":"alice@pve"}`},

		{"groups: the list is not recorded, the fact that it was set is", alice, false, proxmox.AccessUserFields{Groups: accessPtr(accessSentinelGroups)},
			`{"groups_set":true,"has_password":false,"userid":"alice@pve"}`},
		{"groups sent empty are still set", alice, false, proxmox.AccessUserFields{Groups: accessPtr("")},
			`{"groups_set":true,"has_password":false,"userid":"alice@pve"}`},

		{"every field at once carries only the five keys", self, true, accessSentinelFields(),
			`{"enable":false,"expire":1767225600,"groups_set":true,"has_password":true,"userid":"nexara@pve"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := accessDetailsJSON(t, accessUserCreateDetails(tc.userid, tc.hasPassword, tc.fields))
			if got != tc.want {
				t.Errorf("details = %s, want %s", got, tc.want)
			}
			for _, secret := range accessSentinelUnrecorded {
				if strings.Contains(got, secret) {
					t.Errorf("details %s carry %q — audit rows are readable by every Viewer", got, secret)
				}
			}
		})
	}
}

// TestAccessUserCreateDetailsRecordsOnlyTheAllowedFields is the allow-list checked
// against proxmox.AccessUserFields rather than a list of the fields that exist today
// (accessRequireDetailsClassified): enable and expire as they were given, groups as
// the fact that they were.
func TestAccessUserCreateDetailsRecordsOnlyTheAllowedFields(t *testing.T) {
	accessRequireDetailsClassified(t,
		func(t *testing.T, fields proxmox.AccessUserFields) string {
			return accessDetailsJSON(t, accessUserCreateDetails("alice@pve", false, fields))
		},
		map[string]struct{ key, value string }{
			"Enable": {"enable", "true"},
			"Expire": {"expire", "1700000000"},
			"Groups": {"groups_set", "true"},
		},
		map[string]bool{"Comment": true, "Email": true, "FirstName": true, "LastName": true, "Keys": true},
		"enable, expire and groups_set")
}

// TestAccessTokenUpdateDetails pins a token update's audit details to the exact JSON
// that reaches the row, one case for each thing the builder decides (see
// TestAccessUserUpdateDetails). The route-level check is TestAccessUpdateTokenAuditRow.
func TestAccessTokenUpdateDetails(t *testing.T) {
	const (
		self  = "nexara@pve"
		token = "api"
	)
	const sentinelComment = "sentinel-comment"
	hi := accessPtr(sentinelComment)
	for _, tc := range []struct {
		name            string
		userid, tokenid string
		force           bool
		req             proxmox.UpdateAccessTokenParams
		want            string
	}{
		{"nothing set", self, token, false, proxmox.UpdateAccessTokenParams{}, `{"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"force on an update that sets nothing overrides nothing", self, token, true, proxmox.UpdateAccessTokenParams{},
			`{"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"force on a comment change overrides nothing, and the comment is not recorded", self, token, true, proxmox.UpdateAccessTokenParams{Comment: hi},
			`{"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},

		{"regenerating", self, token, false, proxmox.UpdateAccessTokenParams{Regenerate: true},
			`{"forced":false,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`},
		{"regenerating, forced", self, token, true, proxmox.UpdateAccessTokenParams{Regenerate: true},
			`{"forced":true,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`},

		{"giving an expiry", self, token, false, proxmox.UpdateAccessTokenParams{Expire: accessPtr[int64](1767225600)},
			`{"expire":1767225600,"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"giving an expiry, forced", self, token, true, proxmox.UpdateAccessTokenParams{Expire: accessPtr[int64](1767225600)},
			`{"expire":1767225600,"forced":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"clearing the expiry is recorded as 0, and force on it overrides nothing", self, token, true, proxmox.UpdateAccessTokenParams{Expire: accessPtr[int64](0)},
			`{"expire":0,"forced":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},

		{"turning privilege separation on", self, token, false, proxmox.UpdateAccessTokenParams{PrivSep: accessPtr(true)},
			`{"forced":false,"privsep":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"turning privilege separation on, forced", self, token, true, proxmox.UpdateAccessTokenParams{PrivSep: accessPtr(true)},
			`{"forced":true,"privsep":true,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},
		{"turning it off is recorded, and force on it overrides nothing", self, token, true, proxmox.UpdateAccessTokenParams{PrivSep: accessPtr(false)},
			`{"forced":false,"privsep":false,"regenerate":false,"tokenid":"api","userid":"nexara@pve"}`},

		{"the token named is the one updated", "alice@pve", "ci", false, proxmox.UpdateAccessTokenParams{Regenerate: true},
			`{"forced":false,"regenerate":true,"tokenid":"ci","userid":"alice@pve"}`},
		{"every field at once carries only the six keys", self, token, true,
			proxmox.UpdateAccessTokenParams{Comment: hi, Expire: accessPtr[int64](1767225600), PrivSep: accessPtr(true), Regenerate: true},
			`{"expire":1767225600,"forced":true,"privsep":true,"regenerate":true,"tokenid":"api","userid":"nexara@pve"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := accessDetailsJSON(t, accessTokenUpdateDetails(tc.userid, tc.tokenid, tc.force, tc.req))
			if got != tc.want {
				t.Errorf("details = %s, want %s", got, tc.want)
			}
			if strings.Contains(got, sentinelComment) {
				t.Errorf("details %s carry the comment — audit rows are readable by every Viewer", got)
			}
		})
	}
}

// TestAccessTokenUpdateDetailsRecordsOnlyTheAllowedFields is the allow-list checked
// against proxmox.UpdateAccessTokenParams (accessRequireDetailsClassified): expire and
// privsep as they were given, regenerate as the fact that it was. The request is
// unforced, so forced stays false whichever field is filled.
func TestAccessTokenUpdateDetailsRecordsOnlyTheAllowedFields(t *testing.T) {
	accessRequireDetailsClassified(t,
		func(t *testing.T, req proxmox.UpdateAccessTokenParams) string {
			return accessDetailsJSON(t, accessTokenUpdateDetails("nexara@pve", "api", false, req))
		},
		map[string]struct{ key, value string }{
			"Expire":     {"expire", "1700000000"},
			"PrivSep":    {"privsep", "true"},
			"Regenerate": {"regenerate", "true"},
		},
		map[string]bool{"Comment": true},
		"expire, privsep and regenerate")
}

// accessKeysProbe is the fixture value of a user's keys: findable in a body whatever
// key it is under, and obviously not a real key.
const accessKeysProbe = "PROBE-ACCESS-KEYS-NOT-A-REAL-VALUE"

// accessWire marshals what a user shaper returns and reads back the one user in it:
// the object itself, or the only element of an array. It returns the JSON as well. The
// list shaper's result is passed as the slice it is, whose elements are addressable, so
// a pointer-receiver MarshalJSON added to the proxmox type later is dispatched here as
// it would be in production; the detail shaper's is passed as the value c.JSON is given.
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

// TestAccessUserReadsWithholdKeys pins what the two user reads make of a user's keys
// (c978c43; see accessUserResponse for what keys holds and why both reads, which every
// Viewer can make, must not send it). Each response is read back as the JSON a Viewer
// receives: the value is not in it, the key is not there even blank, and has_keys says
// whether Proxmox sent anything but whitespace. The response is also examined as a
// value, because it must hold no copy of keys: that keeps the body's silence from
// resting on the json tag alone.
func TestAccessUserReadsWithholdKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		keys    string
		hasKeys bool
	}{
		{"a value", accessKeysProbe, true},
		{"a value with whitespace around it", " \t" + accessKeysProbe + "\n", true},
		{"none", "", false},
		{"spaces only", "   ", false},
		{"a tab and a newline only", "\t\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			list := accessUsersForRead([]proxmox.AccessUser{{UserID: "alice@pve", Keys: tc.keys}})
			if len(list) != 1 {
				t.Fatalf("the list shaper turned one user into %d entries", len(list))
			}
			detail := accessUserDetailForRead(proxmox.AccessUserDetail{UserID: "alice@pve", Keys: tc.keys})

			for name, resp := range map[string]struct {
				body any
				// held is the keys value the response still carries as a value.
				held string
			}{
				"list":   {list, list[0].AccessUser.Keys},
				"detail": {detail, detail.AccessUserDetail.Keys},
			} {
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

// accessFilled returns a T, one of the Proxmox access structs the reads shape (a user,
// a user's detail, or a realm), with every field set to something that stands out: each
// string is a probe, a user's keys among them.
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
		// A fill that reached nothing would leave both sides near-empty and the comparison true of anything.
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

// TestAccessUserReadsCarryEveryOtherField compares each response with the Proxmox
// struct's own JSON: the same fields with the same values, minus keys, plus has_keys.
// The SPA reads the rest of a user from these responses. Both structs are filled by
// reflection, so a field Proxmox adds to either later has to reach the response to
// pass — which stops the embedding in accessUserResponse from being swapped for a
// hand-copied field list that leaves one out.
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
