package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// totpRouteCount is how many endpoints registerTOTPEndpoints declares. It is
// ALL 7 of TOTPHandler's routes — the six under /auth/totp and the admin reset
// that hangs off /users/:id. See vmRouteCount in registry_vms_test.go for why
// the registry total is a sum of per-domain constants.
const totpRouteCount = 7

// totpAdminResetKey is the one route in this domain that acts on somebody else.
const totpAdminResetKey = "DELETE /api/v1/users/:id/totp"

// totpLegacyPermissions is what each handler checked BEFORE this migration,
// transcribed from `git show HEAD:internal/api/handlers/totp.go` at commit
// 357be6f.
//
// Exactly ONE of the seven made a requirePerm call — AdminReset, with a static
// manage:user — so the tally is 1 in, 1 out. The other six are listed with an
// empty permission because they checked nothing, and they are here rather than
// omitted for the reason the tally exists: a route with nothing to move is
// exactly the route that would otherwise slip out of the count unnoticed.
var totpLegacyPermissions = map[string]string{
	"POST /api/v1/auth/totp/verify-login":              "",
	"POST /api/v1/auth/totp/setup":                     "",
	"POST /api/v1/auth/totp/setup/verify":              "",
	"DELETE /api/v1/auth/totp":                         "",
	"GET /api/v1/auth/totp/status":                     "",
	"POST /api/v1/auth/totp/recovery-codes/regenerate": "",
	totpAdminResetKey:                                  "manage:user",
}

// totpSelfServiceRoutes are the five routes that act on the CALLER's own
// enrollment, with the property SelfService has to establish: the subject comes
// from the session, so there is no parameter a caller could substitute.
var totpSelfServiceRoutes = []string{
	"POST /api/v1/auth/totp/setup",
	"POST /api/v1/auth/totp/setup/verify",
	"DELETE /api/v1/auth/totp",
	"GET /api/v1/auth/totp/status",
	"POST /api/v1/auth/totp/recovery-codes/regenerate",
}

// totpRoutesOutsideTheClusterCheckShape is every route in this domain: a second
// factor belongs to an ACCOUNT, and no path here names a cluster.
var totpRoutesOutsideTheClusterCheckShape = map[string]string{
	"POST /api/v1/auth/totp/verify-login":              "Public: the caller holds a pending login token, not a session",
	"POST /api/v1/auth/totp/setup":                     "SelfService: the subject is the caller's own session",
	"POST /api/v1/auth/totp/setup/verify":              "SelfService: the subject is the caller's own session",
	"DELETE /api/v1/auth/totp":                         "SelfService: the subject is the caller's own session",
	"GET /api/v1/auth/totp/status":                     "SelfService: the subject is the caller's own session",
	"POST /api/v1/auth/totp/recovery-codes/regenerate": "SelfService: the subject is the caller's own session",
	totpAdminResetKey:                                  "a Nexara account is instance-wide; the path names no cluster",
}

// declaredTOTPEndpoints returns every declaration in this domain, keyed
// "METHOD path". It matches the /auth/totp prefix plus the one admin route,
// which lives under /users.
func declaredTOTPEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if strings.HasPrefix(e.Path, totpScope) || key == totpAdminResetKey {
			out[key] = e
		}
	}
	return out
}

// TestTOTPRoutesDeclareTheSamePermissionTheyEnforced is the tally: 1
// hand-placed call in, 1 declared global Check out, and six routes that checked
// nothing declared as the shape that says so.
func TestTOTPRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredTOTPEndpoints(t)
	if len(declared) != totpRouteCount {
		t.Fatalf("the registry declares %d TOTP routes, want %d", len(declared), totpRouteCount)
	}
	if len(totpLegacyPermissions) != totpRouteCount {
		t.Fatalf("totpLegacyPermissions has %d entries, want %d — the table must cover every migrated route",
			len(totpLegacyPermissions), totpRouteCount)
	}

	hoisted := 0
	for key, want := range totpLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s is not declared in the registry", key)
			continue
		}
		if want == "" {
			// A route that checked nothing must declare one of the two shapes
			// that say so, not silently acquire a gate or lose its session.
			if e.Permissions.Public == "" && e.Permissions.SelfService == "" {
				t.Errorf("%s declares %q; it checked no permission before the migration, so it is either "+
					"Public or SelfService", key, e.Permissions.Describe())
			}
			continue
		}
		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check — it made one static, unconditional requirePerm call",
				key, e.Permissions.Describe())
			continue
		}
		hoisted++
		if e.Permissions.Describe() != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration",
				key, e.Permissions.Describe(), want)
		}
		if e.Permissions.Check.Scope != ScopeGlobal {
			t.Errorf("%s is %s-scoped, want %s", key, e.Permissions.Check.Scope, ScopeGlobal)
		}
	}
	if hoisted != 1 {
		t.Errorf("the tally moves %d permission call(s) into middleware, want 1", hoisted)
	}

	for key := range declared {
		if _, listed := totpLegacyPermissions[key]; !listed {
			t.Errorf("%s is declared but is not in totpLegacyPermissions — a new TOTP route must be added "+
				"to the tally, or the tally stops being a review surface", key)
		}
	}
}

// TestTOTPSelfServiceRoutesTakeNoSubject is the assertion SelfService actually
// needs, rather than a restatement of the label.
//
// SelfService is the riskiest shape in the vocabulary: the route IS
// authenticated, so an IDOR — a subject read from a path, query or body value
// instead of from the session — hides exactly there and nothing else in the
// package would notice. None of these five declares ANY parameter that could
// name a user, which is the structural version of "the subject comes from
// c.Locals(\"user_id\")": there is nothing for a caller to substitute.
func TestTOTPSelfServiceRoutesTakeNoSubject(t *testing.T) {
	// Any parameter whose name could plausibly carry an identity. A route here
	// that grew one would be a route where the subject stopped being the
	// session.
	subjectish := []string{"id", "user_id", "userid", "user", "email", "subject", "account_id"}

	for _, key := range totpSelfServiceRoutes {
		method, path, _ := strings.Cut(key, " ")
		t.Run(key, func(t *testing.T) {
			e := declaredEndpoint(t, method, path)
			if e.Permissions.SelfService == "" {
				t.Fatalf("declares %q, want SelfService", e.Permissions.Describe())
			}
			if !e.Permissions.authenticated() {
				t.Error("is not authenticated; SelfService means the session identifies the subject, so there must be one")
			}
			if len(pathParamNames(e.Path)) != 0 {
				t.Errorf("the path carries %v; a self-service route must not name a subject", pathParamNames(e.Path))
			}
			for _, name := range subjectish {
				if _, declared := e.Parameters[name]; declared {
					t.Errorf("declares a %q parameter — the subject must come from the session, not from the caller", name)
				}
			}

			// End to end: no grant is needed, but a session is.
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			app := newRegistryApp(t, stubAuth(map[string]bool{}), gated)
			status, _ := send(t, app, httptest.NewRequest(method, path, nil))
			if status != fiber.StatusUnauthorized {
				t.Errorf("an anonymous caller got %d, want 401", status)
			}
			if cap.called {
				t.Error("the handler ran for a request carrying no session")
			}
		})
	}
}

// TestTOTPExemptionReasonsMatchTheReviewedLists keeps one exemption to one
// justification.
//
// A declared SelfService must ALSO be a key in selfServiceRoutes
// (TestGuard_RegistrySelfServiceRoutesAreReviewed enforces that much), and a
// declared Public must be in publicRoutes. Neither guard compares the REASONS,
// so without this the inline string and the reviewed one could say different
// things — and the next reader would have two accounts of why a route needs no
// gate, with nothing saying which is current.
func TestTOTPExemptionReasonsMatchTheReviewedLists(t *testing.T) {
	for _, key := range totpSelfServiceRoutes {
		method, path, _ := strings.Cut(key, " ")
		e := declaredEndpoint(t, method, path)
		reviewed, listed := selfServiceRoutes[key]
		if !listed {
			t.Errorf("%s declares SelfService but is not in selfServiceRoutes", key)
			continue
		}
		if e.Permissions.SelfService != reviewed {
			t.Errorf("%s: the declared reason is %q but selfServiceRoutes says %q — one exemption, one justification",
				key, e.Permissions.SelfService, reviewed)
		}
	}

	const publicKey = "POST /api/v1/auth/totp/verify-login"
	e := declaredEndpoint(t, fiber.MethodPost, totpScope+"/verify-login")
	if e.Permissions.Public == "" {
		t.Fatalf("%s declares %q, want Public", publicKey, e.Permissions.Describe())
	}
	reviewed, listed := publicRoutes[publicKey]
	if !listed {
		t.Fatalf("%s declares Public but is not in publicRoutes", publicKey)
	}
	if e.Permissions.Public != reviewed {
		t.Errorf("%s: the declared reason is %q but publicRoutes says %q", publicKey, e.Permissions.Public, reviewed)
	}
}

// TestTOTPAdminResetIsGatedByTheDeclaration proves the one hoisted permission
// is the permission the route enforces, end to end.
//
// It is the route that clears somebody ELSE's second factor, so the grant is
// what stands between an operator doing a password-reset favour and any
// authenticated account stripping 2FA from an admin.
func TestTOTPAdminResetIsGatedByTheDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, userScope+"/:id/totp")
	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := userScope + "/" + testUserRowID + "/totp"

	app := newRegistryApp(t, stubAuth(map[string]bool{"view:user": true}), gated)
	status, _ := send(t, app, authedRequest(http.MethodDelete, target))
	if status != fiber.StatusForbidden {
		t.Fatalf("a caller holding only view:user got %d, want 403", status)
	}
	if cap.called {
		t.Error("the handler ran for a caller without manage:user")
	}

	cap.called = false
	app = newRegistryApp(t, stubAuth(map[string]bool{"manage:user": true}), gated)
	status, _ = send(t, app, authedRequest(http.MethodDelete, target))
	if status != fiber.StatusNoContent {
		t.Fatalf("a caller holding manage:user got %d", status)
	}

	cap.called = false
	app = newRegistryApp(t, stubAuth(map[string]bool{"manage:user": true}), gated)
	status, _ = send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
	if status != fiber.StatusUnauthorized {
		t.Fatalf("an anonymous caller got %d, want 401", status)
	}
}

// probeTOTPEndpoint is a declared TOTP endpoint with its handler swapped for a
// capture and its gate removed, so a parameter test needs neither Redis nor a
// session.
func probeTOTPEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestTOTPDisableReadsItsCredentialsFromTheBody is the source assertion this
// domain could not do without.
//
// ResolveSource reads an unsourced parameter on DELETE out of the QUERY STRING,
// and the SPA sends the disable code in a JSON body. A declaration that left
// Source unset would fail in both directions at once: the handler would see no
// code and refuse every request, and a caller who then "fixed" it by moving the
// code into the URL would be putting a second factor into proxy access logs and
// Referer headers.
func TestTOTPDisableReadsItsCredentialsFromTheBody(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodDelete, totpScope)
	for _, name := range []string{"code", "recovery_code"} {
		prop, ok := e.Parameters[name]
		if !ok {
			t.Errorf("declares no %q parameter", name)
			continue
		}
		if prop.Source != apischema.SourceBody {
			t.Errorf("%q declares source %q, want %q — DELETE resolves an unsourced parameter from the "+
				"query string, and this is a credential", name, prop.Source, apischema.SourceBody)
		}
	}

	// End to end on the shape the SPA sends.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeTOTPEndpoint(t, fiber.MethodDelete, totpScope, cap))
	status, env := send(t, app, jsonRequest(http.MethodDelete, totpScope, `{"code":"123456"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.String("code"); got != "123456" {
		t.Errorf("code = %q, want %q — the body value did not reach the handler", got, "123456")
	}

	// And the same value in the URL is refused rather than silently accepted,
	// which is what keeps a second factor out of an access log.
	cap.called = false
	status, env = send(t, app, httptest.NewRequest(http.MethodDelete, totpScope+"?code=123456", nil))
	if status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400 for a code sent as a query parameter", status, env.Message)
	}
	if cap.called {
		t.Error("the handler ran for a code sent in the URL")
	}
}

// TestTOTPOptionalCodeStillAcceptsAnEmptyString records a narrowing that was
// deliberately NOT made.
//
// Disable and VerifyLogin test `if code != "" && !pattern`, so an explicitly
// empty code has always been legal on them — it is how a client spells "I am
// using the recovery code instead". apischema treats "" as a SUPPLIED value, so
// a Pattern on these two would answer 400 for a request that works today. The
// regex therefore stays in the handler; this pins that the declaration does not
// quietly reintroduce it.
func TestTOTPOptionalCodeStillAcceptsAnEmptyString(t *testing.T) {
	for _, route := range []struct{ method, path string }{
		{fiber.MethodDelete, totpScope},
		{fiber.MethodPost, totpScope + "/verify-login"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			prop := declaredEndpoint(t, route.method, route.path).Parameters["code"]
			if !prop.Optional {
				t.Error("code is required, but these two routes accept a recovery code instead")
			}
			if prop.Pattern != "" {
				t.Errorf("code declares pattern %q; that refuses \"\", which is how a client spells "+
					"\"I am using the recovery code\"", prop.Pattern)
			}
		})
	}

	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeTOTPEndpoint(t, fiber.MethodDelete, totpScope, cap))
	status, env := send(t, app, jsonRequest(http.MethodDelete, totpScope,
		`{"code":"","recovery_code":"AAAA-BBBB"}`))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — an empty code alongside a recovery code has always been legal",
			status, env.Message)
	}
}

// TestTOTPRequiredCodeIsSixDigits pins the pattern that DID move out, on the
// two routes where the code is the only credential accepted.
//
// Both handlers made the same pair of checks — `== ""`, then the six-digit
// regex — and the pattern refuses both cases at once. Regenerate deliberately
// does NOT accept a recovery code: it would let one leaked recovery code mint
// ten fresh ones.
func TestTOTPRequiredCodeIsSixDigits(t *testing.T) {
	routes := []struct{ method, path string }{
		{fiber.MethodPost, totpScope + "/setup/verify"},
		{fiber.MethodPost, totpScope + "/recovery-codes/regenerate"},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			e := declaredEndpoint(t, route.method, route.path)
			prop := e.Parameters["code"]
			if prop.Optional {
				t.Error("code is optional, but this route has always refused a request without one")
			}
			if prop.Pattern == "" {
				t.Fatal("code declares no pattern; the handler's six-digit check has to live somewhere")
			}
			if _, declared := e.Parameters["recovery_code"]; declared {
				t.Error("declares a recovery_code parameter; accepting one here would let a single leaked " +
					"recovery code mint ten fresh ones")
			}

			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeTOTPEndpoint(t, route.method, route.path, cap))
			for _, body := range []string{`{}`, `{"code":""}`, `{"code":"12345"}`, `{"code":"1234567"}`, `{"code":"abcdef"}`} {
				cap.called = false
				status, env := send(t, app, jsonRequest(route.method, route.path, body))
				if status != fiber.StatusBadRequest {
					t.Errorf("%s: status = %d (%q), want 400", body, status, env.Message)
				}
				if cap.called {
					t.Errorf("%s: the handler ran for a code that is not six digits", body)
				}
			}

			cap.called = false
			status, env := send(t, app, jsonRequest(route.method, route.path, `{"code":"000123"}`))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — a leading-zero code is a real code", status, env.Message)
			}
		})
	}
}

// TestTOTPVerifyLoginRequiresThePendingToken pins the one required parameter on
// the public route, including the empty-string case its `== ""` check refused.
func TestTOTPVerifyLoginRequiresThePendingToken(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, totpScope+"/verify-login")
	prop := e.Parameters["totp_pending_token"]
	if prop.Optional {
		t.Error("totp_pending_token is optional, but the route has always refused a request without it")
	}
	if prop.MinLength == nil || *prop.MinLength < 1 {
		t.Error("totp_pending_token declares no MinLength; the handler's `== \"\"` check refused a blank one too")
	}

	cap := &capture{}
	path := totpScope + "/verify-login"
	app := newRegistryApp(t, noAuth(), probeTOTPEndpoint(t, fiber.MethodPost, path, cap))
	for _, body := range []string{`{"code":"123456"}`, `{"totp_pending_token":"","code":"123456"}`} {
		cap.called = false
		status, env := send(t, app, jsonRequest(http.MethodPost, path, body))
		if status != fiber.StatusBadRequest {
			t.Errorf("%s: status = %d (%q), want 400", body, status, env.Message)
		}
		if cap.called {
			t.Errorf("%s: the handler ran without a pending token", body)
		}
	}
}

// TestTOTPCodePatternMatchesTheHandler is the pin the declaration's comment on
// totpCodePatternRE promises.
//
// The six-digit rule now lives in TWO packages: totpCodePatternRE
// (`^[0-9]{6}$`, registry_totp.go) is the schema's, and totpCodePattern
// (`^\d{6}$`, handlers/totp.go) is the one the Disable and VerifyLogin handlers
// still apply conditionally — those two cannot use the schema's copy, because
// an explicitly empty code is legal there and a Pattern would refuse it. Two
// copies of one rule with nothing comparing them is how they diverge.
//
// The handler's copy is read out of its SOURCE rather than exported, because a
// regexp.Regexp and a schema Pattern are different types and exporting one to
// satisfy a test would put a symbol in the handlers package that nothing else
// wants. Package api already walks handlers/*.go this way — see buildCallGraph.
//
// The candidate table deliberately includes non-ASCII digits: Go's `\d` is
// ASCII-only unless the Unicode flag is set, so the two agree today, and a
// future `(?U)` or a swap to `\p{Nd}` on either side would make them disagree
// on exactly those rows.
func TestTOTPCodePatternMatchesTheHandler(t *testing.T) {
	handlerPattern := handlerTOTPCodePattern(t)
	if handlerPattern == totpCodePatternRE {
		// Identical source is the strongest possible agreement, but the two
		// are deliberately spelled differently, so fall through to the
		// behavioural comparison rather than returning early.
		t.Logf("both copies spell the pattern %q", handlerPattern)
	}

	schema := regexp.MustCompile(totpCodePatternRE)
	handler := regexp.MustCompile(handlerPattern)

	candidates := []string{
		"123456", "000000", "000123", "999999",
		"", " ", "12345", "1234567", "abcdef", "12345a", "12 456",
		"12345\n", "\n123456", "+12345", "-12345", "1.2345",
		// Non-ASCII digits: Arabic-Indic and full-width. Both patterns must
		// refuse them, and they do only because `\d` is ASCII-only here.
		"١٢٣٤٥٦", "１２３４５６",
	}
	for _, candidate := range candidates {
		if got, want := schema.MatchString(candidate), handler.MatchString(candidate); got != want {
			t.Errorf("%q: the schema pattern %q says %v but the handler's %q says %v — one rule, two "+
				"copies, and they have diverged", candidate, totpCodePatternRE, got, handlerPattern, want)
		}
	}
}

// handlerTOTPCodePattern extracts the regexp source the handlers package
// compiles into totpCodePattern, by parsing its declaration.
func handlerTOTPCodePattern(t *testing.T) string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join("handlers", "totp.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse handlers/totp.go: %v", err)
	}

	var found string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || spec.Names[0].Name != "totpCodePattern" || len(spec.Values) != 1 {
			return true
		}
		call, ok := spec.Values[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		found = value
		return false
	})

	if found == "" {
		t.Fatal("could not find totpCodePattern's regexp literal in handlers/totp.go — this guard " +
			"would pass vacuously; re-point it at wherever the rule moved")
	}
	return found
}

// TestEveryTOTPEndpointIsDocumented holds the declaration-is-the-documentation
// rule for this domain.
func TestEveryTOTPEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredTOTPEndpoints(t) {
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no Description", key)
		}
		if e.Group != "Two-Factor Authentication" {
			t.Errorf("%s declares group %q, want %q", key, e.Group, "Two-Factor Authentication")
		}
	}
}
