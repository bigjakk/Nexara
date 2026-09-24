package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// clusterRouteCount is how many endpoints registerClusterEndpoints declares.
const clusterRouteCount = 7

// clusterRoutesOutsideTheClusterCheckShape records why three of the seven are
// not cluster-scoped Checks. It is folded into
// routesOutsideTheClusterCheckShape in registry_vms_test.go.
var clusterRoutesOutsideTheClusterCheckShape = map[string]string{
	"POST /api/v1/clusters": "global: there is no cluster yet — this is the route that creates one",
	"GET /api/v1/clusters": "Advisory: the listing spans every cluster, so there is none for a gate to " +
		"resolve; accessibleClusters builds the scope and PermitsCluster drops the rest",
	// Not a widening: the three-way grant is what the handler already enforced
	// at HEAD, via requireAnyGlobalManage(c, "cluster", "pbs", "veeam"). The
	// declaration transcribes it. Recorded here because "Alternatives over three
	// permissions" reads like a decision someone made during the migration, and
	// the next reader should not have to re-derive that it was not.
	"POST /api/v1/clusters/fetch-fingerprint": "Alternatives over the three GLOBAL manage permissions the " +
		"handler already required: every add-flow that stores a credential against a remote server starts " +
		"here, so manage:cluster alone refused a backup-only role at step 1 of a flow it was granted",
}

// clusterLegacyPermissions is what each handler checked BEFORE Phase 6j,
// transcribed from `git show HEAD:internal/api/handlers/clusters.go` at commit
// eaeafa7 — seven handlers, seven checks, in four shapes:
//
//	Create             requirePerm(manage, cluster) — global.
//	FetchFingerprint   requireAnyGlobalManage(cluster, pbs, veeam).
//	List               accessibleClusters(view, cluster), no gate.
//	Get/Update/
//	VerifyCertificate  requireClusterPerm(view|manage, cluster, <:id>).
//	Delete             requireClusterPerm(delete, cluster, <:id>).
var clusterLegacyPermissions = map[string]string{
	"POST /api/v1/clusters":                        "manage:cluster",
	"POST /api/v1/clusters/fetch-fingerprint":      "manage:cluster | manage:pbs | manage:veeam",
	"GET /api/v1/clusters":                         "view:cluster (filtered)",
	"GET /api/v1/clusters/:id":                     "view:cluster",
	"PUT /api/v1/clusters/:id":                     "manage:cluster",
	"POST /api/v1/clusters/:id/verify-certificate": "manage:cluster",
	"DELETE /api/v1/clusters/:id":                  "delete:cluster",
}

func declaredClusterEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if _, want := clusterLegacyPermissions[e.Method+" "+e.Path]; want {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestClusterRoutesDeclareTheSamePermissionTheyEnforced is the tally that makes
// deleting seven hand-placed checks a refactor rather than a change.
func TestClusterRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredClusterEndpoints(t)
	if len(declared) != clusterRouteCount {
		t.Fatalf("the registry declares %d cluster routes, want %d", len(declared), clusterRouteCount)
	}
	if len(clusterLegacyPermissions) != clusterRouteCount {
		t.Fatalf("clusterLegacyPermissions has %d entries, want %d", len(clusterLegacyPermissions), clusterRouteCount)
	}

	for key, want := range clusterLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s is in the tally but is not declared in the registry", key)
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
	}

	// The four per-cluster routes resolve :id, which namesACluster accepts
	// only at this one anchored legacy prefix. Pinned here because it is the
	// only domain that relies on it.
	for _, key := range []string{
		"GET /api/v1/clusters/:id",
		"PUT /api/v1/clusters/:id",
		"POST /api/v1/clusters/:id/verify-certificate",
		"DELETE /api/v1/clusters/:id",
	} {
		e := declared[key]
		if e.Permissions.Check == nil || e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s declares %q; it has to be a cluster-scoped Check resolved from :id", key, e.Permissions.Describe())
		}
	}
}

// TestClusterRoutesAreGatedByTheirDeclaration proves, end to end, that the
// permission each route declares is the permission it enforces — the assertion
// TestCluster_NonAdminDenied used to make from inside the handlers package,
// now made against the real declaration rather than a mirror of it.
//
// The DELETE is the one driven hardest: it is the most consequential of the
// seven, and its Proxmox-side revocation is the one action in this domain that
// reaches a live hypervisor.
func TestClusterRoutesAreGatedByTheirDeclaration(t *testing.T) {
	for _, tt := range []struct {
		method  string
		path    string
		granted map[string]bool
		denied  map[string]bool
	}{
		{fiber.MethodPost, clustersScope,
			map[string]bool{"manage:cluster": true}, map[string]bool{"view:cluster": true}},
		{fiber.MethodGet, clusterByID,
			map[string]bool{"view:cluster": true}, map[string]bool{"view:vm": true}},
		{fiber.MethodPut, clusterByID,
			map[string]bool{"manage:cluster": true}, map[string]bool{"view:cluster": true}},
		{fiber.MethodDelete, clusterByID,
			map[string]bool{"delete:cluster": true}, map[string]bool{"manage:cluster": true}},
		// Any ONE of the three global manage grants opens the fingerprint fetch.
		{fiber.MethodPost, clustersScope + "/fetch-fingerprint",
			map[string]bool{"manage:veeam": true}, map[string]bool{"view:cluster": true}},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, tt.method, tt.path)
			target := strings.ReplaceAll(tt.path, ":id", testClusterID)

			for _, tc := range []struct {
				name   string
				grants map[string]bool
				want   int
			}{
				{"granted", tt.granted, fiber.StatusNoContent},
				{"denied", tt.denied, fiber.StatusForbidden},
			} {
				cap := &capture{}
				gated := e
				gated.Handler = cap.handler()
				// The rate limiter is dropped: a table-driven test would spend
				// its budget. Its presence is pinned by
				// TestClusterCreateLimiterIsRouteScopedNotAppLevel and its
				// position in the chain by TestRegistryChainOrder.
				gated.RateLimiter = nil
				app := newRegistryApp(t, stubAuth(tc.grants), gated)

				req := jsonRequest(tt.method, target+clusterProbeQuery(tt.method), clusterProbeBody(tt.method, tt.path))
				req.Header.Set("X-Test-User", "yes")
				status, env := send(t, app, req)
				if status != tc.want {
					t.Fatalf("%s: status = %d (%q), want %d", tc.name, status, env.Message, tc.want)
				}
				if cap.called != (tc.want == fiber.StatusNoContent) {
					t.Errorf("%s: handler called = %v, want %v", tc.name, cap.called, tc.want == fiber.StatusNoContent)
				}
			}

			// And an anonymous caller never reaches the gate at all.
			cap := &capture{}
			gated := e
			gated.Handler = cap.handler()
			gated.RateLimiter = nil
			app := newRegistryApp(t, stubAuth(tt.granted), gated)
			if status, _ := send(t, app, jsonRequest(tt.method, target+clusterProbeQuery(tt.method), clusterProbeBody(tt.method, tt.path))); status != fiber.StatusUnauthorized {
				t.Errorf("anonymous: status = %d, want 401", status)
			}
			if cap.called {
				t.Error("the handler ran for a request carrying no session")
			}
		})
	}
}

// clusterProbeQuery is the query the DELETE's schema requires, so its granted
// case reaches the handler rather than a 400: confirm, which the capture handler
// in its place never compares with anything.
func clusterProbeQuery(method string) string {
	if method == fiber.MethodDelete {
		return "?confirm=cluster01"
	}
	return ""
}

// clusterProbeBody is the minimum body each probed route's schema accepts, so
// the test reaches the permission decision rather than a 400.
func clusterProbeBody(method, path string) string {
	switch {
	case method == fiber.MethodPost && path == clustersScope:
		return `{"name":"cluster01","api_url":"https://pve-01.example.com:8006"}`
	case method == fiber.MethodPost && strings.HasSuffix(path, "/fetch-fingerprint"):
		return `{"api_url":"https://pve-01.example.com:8006"}`
	case method == fiber.MethodPut:
		return `{}`
	default:
		return ""
	}
}

// TestFingerprintLimitersAreSeparateInstances pins the one thing about this
// domain's rate limiting that "is a limiter attached" cannot see.
//
// fingerprintFetchLimiter() returns a NEW limiter on every call, and the legacy
// block called it TWICE — so fetch-fingerprint and verify-certificate each had
// their own 30/min store despite both keying on <ip>:fetch-fingerprint. Handing
// one instance to both would halve the budget for a caller who uses both, and
// nothing about the route table would look different: the two closures are
// indistinguishable by type, by name and by code pointer. Only spending one
// budget and then checking the other tells them apart.
func TestFingerprintLimitersAreSeparateInstances(t *testing.T) {
	const budget = 30 // fingerprintFetchLimiter's Max; see middleware.go.

	// BOTH endpoints come from ONE registry build. declaredEndpoint builds a
	// fresh stub server per call, which calls buildRegistry again and hands back
	// freshly constructed limiters — so taking them one at a time would compare
	// two instances that are separate no matter what buildRegistry does, and the
	// test would pass vacuously.
	byKey := registryEndpointsByKey(newRouteStubServer(t).registry.Endpoints())
	fetch, ok := byKey["POST "+clustersScope+"/fetch-fingerprint"]
	if !ok {
		t.Fatal("POST /api/v1/clusters/fetch-fingerprint is not declared")
	}
	verify, ok := byKey["POST "+clusterByID+"/verify-certificate"]
	if !ok {
		t.Fatal("POST /api/v1/clusters/:id/verify-certificate is not declared")
	}
	for name, e := range map[string]Endpoint{"fetch-fingerprint": fetch, "verify-certificate": verify} {
		if e.RateLimiter == nil {
			t.Fatalf("%s declares no RateLimiter; it dials a host the caller names", name)
		}
	}

	fetchCap, verifyCap := &capture{}, &capture{}
	fetch.Handler, verify.Handler = fetchCap.handler(), verifyCap.handler()
	fetch.Permissions = Permissions{SelfService: "rate-limit fixture; authorization is exercised separately"}
	verify.Permissions = Permissions{SelfService: "rate-limit fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), fetch, verify)

	fetchTarget := clustersScope + "/fetch-fingerprint"
	body := `{"api_url":"https://pve-01.example.com:8006"}`
	var last int
	for i := 0; i < budget+1; i++ {
		last, _ = send(t, app, jsonRequest(http.MethodPost, fetchTarget, body))
	}
	if last != fiber.StatusTooManyRequests {
		t.Fatalf("request %d to fetch-fingerprint = %d, want 429 — the budget is %d and this test cannot "+
			"tell the two limiters apart without spending one", budget+1, last, budget)
	}

	verifyTarget := strings.ReplaceAll(clusterByID+"/verify-certificate", ":id", testClusterID)
	status, env := send(t, app, jsonRequest(http.MethodPost, verifyTarget, ""))
	if status == fiber.StatusTooManyRequests {
		t.Error("verify-certificate is 429 after fetch-fingerprint spent its budget — the two share one " +
			"limiter instance, which halves what the legacy block gave each of them")
	}
	if status != fiber.StatusNoContent {
		t.Fatalf("verify-certificate = %d (%q), want 204", status, env.Message)
	}
}

// TestClusterUpdateKeepsEveryFieldOptional is the merge-semantics assertion
// this domain needed.
//
// The edit handler merges POINTERS onto the stored row, so "the caller did not
// mention this field" has to stay distinct from "the caller sent the zero
// value". A Default on any of them would collapse the two and, for is_active,
// pause every cluster whose editor only changed its name.
func TestClusterUpdateKeepsEveryFieldOptional(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPut, clusterByID)
	for _, name := range []string{
		"name", "api_url", "token_id", "token_secret", "tls_fingerprint",
		"sync_interval_seconds", "is_active",
	} {
		prop := e.Parameters[name]
		if !prop.Optional {
			t.Errorf("%s is required on the edit; the dialog saves whatever subset the operator touched", name)
		}
		if prop.Default != nil {
			t.Errorf("%s declares default %#v; the handler merges pointers, and a default makes every "+
				"save look like an explicit choice", name, prop.Default)
		}
	}

	// token_secret carries no MinLength, on purpose: an explicit "" has to
	// reach the handler, which answers with a message telling the caller to
	// omit the field instead — encrypting an empty string would leave a
	// non-empty ciphertext in the column and silently break the cluster.
	if e.Parameters["token_secret"].MinLength != nil {
		t.Error("token_secret declares a MinLength; an explicit empty value must reach the handler, " +
			"which explains what to do instead")
	}

	cap := &capture{}
	probe := e
	probe.Handler = cap.handler()
	probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), probe)

	target := strings.ReplaceAll(clusterByID, ":id", testClusterID)
	if status, env := send(t, app, jsonRequest(http.MethodPut, target, `{"name":"renamed"}`)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if !cap.params.Has("name") {
		t.Error("name reads as unsupplied on a body that sent it")
	}
	for _, name := range []string{"api_url", "is_active", "sync_interval_seconds", "token_secret"} {
		if cap.params.Has(name) {
			t.Errorf("%s reads as supplied on a body that omitted it — the merge would overwrite the stored value", name)
		}
	}
}

// TestClusterCreateRejectsWhatTheHandlerUsedTo pins the "x is required" and
// bound checks the handler no longer writes itself.
func TestClusterCreateRejectsWhatTheHandlerUsedTo(t *testing.T) {
	for _, tt := range []struct{ name, body, field string }{
		{"no name", `{"api_url":"https://pve-01.example.com:8006"}`, "name:"},
		{"empty name", `{"name":"","api_url":"https://pve-01.example.com:8006"}`, "name:"},
		{"an over-long name", `{"name":"` + strings.Repeat("a", 256) + `","api_url":"https://pve-01.example.com:8006"}`, "name:"},
		{"no api_url", `{"name":"cluster01"}`, "api_url:"},
		{"a sync interval below the floor", `{"name":"cluster01","api_url":"https://x.example.com","sync_interval_seconds":9}`, "sync_interval_seconds:"},
		{"a sync interval above the ceiling", `{"name":"cluster01","api_url":"https://x.example.com","sync_interval_seconds":86401}`, "sync_interval_seconds:"},
		{"a misspelled key", `{"name":"cluster01","api_url":"https://x.example.com","tls_finger":"AA"}`, "tls_finger:"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			e := declaredEndpoint(t, fiber.MethodPost, clustersScope)
			e.Handler = cap.handler()
			e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			e.RateLimiter = nil
			app := newRegistryApp(t, noAuth(), e)

			status, env := send(t, app, jsonRequest(http.MethodPost, clustersScope, tt.body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.field) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.field)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}

	// An omitted sync interval still means 30 — the substitution the handler
	// made is now the schema's Default, and the docs say so.
	cap := &capture{}
	e := declaredEndpoint(t, fiber.MethodPost, clustersScope)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	e.RateLimiter = nil
	app := newRegistryApp(t, noAuth(), e)
	body := `{"name":"cluster01","api_url":"https://pve-01.example.com:8006","token_id":"t","token_secret":"s"}`
	if status, env := send(t, app, jsonRequest(http.MethodPost, clustersScope, body)); status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if got := cap.params.Int("sync_interval_seconds"); got != 30 {
		t.Errorf("sync_interval_seconds = %d, want 30", got)
	}
	if cap.params.Has("sync_interval_seconds") {
		t.Error("sync_interval_seconds reads as supplied; a default is not something the caller sent")
	}
}

// TestClusterDeleteRevokeStaysAString is the one place this migration
// deliberately did NOT tidy a parameter into its natural type.
//
// wantsCredentialRevocation reads exactly "1", "true" and "yes" as consent.
// apischema's toBool ALSO accepts "on" and "off", so declaring this a boolean
// would make the schema accept a spelling the handler then reads as "no" — on
// the endpoint that deletes users and tokens from a live hypervisor. A schema
// and a handler that disagree about consent is not a tidy-up worth making.
func TestClusterDeleteRevokeStaysAString(t *testing.T) {
	prop := declaredEndpoint(t, fiber.MethodDelete, clusterByID).Parameters["revoke_pve_credentials"]
	if prop.Type != "string" {
		t.Fatalf("revoke_pve_credentials is declared %q; a boolean would accept \"on\", which "+
			"wantsCredentialRevocation reads as no", prop.Type)
	}
	if !prop.Optional || prop.Default != nil {
		t.Error("revoke_pve_credentials must be optional with no default — revocation is opt-in only, " +
			"and never the default")
	}

	// confirm is required on this route (TestClusterDeleteRequiresTheClusterName);
	// it rides along so the request reaches the handler.
	target := strings.ReplaceAll(clusterByID, ":id", testClusterID) + "?confirm=cluster01"
	for _, tt := range []struct {
		query string
		want  string
	}{
		{"", ""},
		{"&revoke_pve_credentials=1", "1"},
		{"&revoke_pve_credentials=on", "on"},
	} {
		cap := &capture{}
		e := declaredEndpoint(t, fiber.MethodDelete, clusterByID)
		e.Handler = cap.handler()
		e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		status, env := send(t, app, httptest.NewRequest(http.MethodDelete, target+tt.query, nil))
		if status != fiber.StatusNoContent {
			t.Fatalf("%q: status = %d (%q), want 204", tt.query, status, env.Message)
		}
		if got := cap.params.String("revoke_pve_credentials"); got != tt.want {
			t.Errorf("%q: reached the handler as %q, want %q", tt.query, got, tt.want)
		}
	}
}

// TestEveryClusterEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEveryClusterEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredClusterEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Clusters" {
			t.Errorf("%s is in group %q, want Clusters", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
