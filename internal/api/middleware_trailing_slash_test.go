package api

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/config"
)

// These tests hold refuseTrailingSlashWrites (middleware.go) to what it is
// for: a write whose path ends in "/" — which is what a browser puts on the
// wire when it resolves a FINAL "." or ".." segment — never reaches a
// route. The dispatch half is shown against the cluster delete's own
// declaration, where the bug removed clusters; the coverage half against
// the server as cmd/nexara assembles it (newAssembledServer, in
// middleware_content_coding_test.go).

// newClusterDeleteProbe mounts the REAL declaration of DELETE
// /api/v1/clusters/:id, its handler swapped for a capture, on an app built
// with the production Fiber config — and, when stack is set, behind the
// production middleware stack, installed by setupMiddleware exactly as New
// installs it. The two variants differ in nothing else, which is what lets
// the bare one be the other's precondition twin.
func newClusterDeleteProbe(t *testing.T, stack bool) (*fiber.App, *capture) {
	t.Helper()
	cap := &capture{}
	e := declaredEndpoint(t, fiber.MethodDelete, clusterByID)
	e.Handler = cap.handler()
	// The real declaration gates on delete:cluster through the RBAC engine,
	// which this app has none of. Where the request is DISPATCHED is the
	// question here; TestRegistryChainOrder exercises the gate.
	e.Permissions = Permissions{SelfService: "dispatch fixture; authorization is exercised separately"}

	cfg := &config.Config{RateLimitMax: 1_000_000, RateLimitExpiration: time.Minute}
	s := &Server{config: cfg, app: fiber.New(buildFiberConfig(cfg))}
	if stack {
		s.setupMiddleware()
	}
	reg := NewRegistry()
	reg.Register(e)
	mountRegistry(s.app, reg, noAuth())
	return s.app, cap
}

// TestTrailingSlashDeleteNeverReachesTheClusterDelete is the bug the gate
// exists for, each case with its precondition twin.
//
// The twin sends the request to the cluster delete's declaration with
// nothing but the production router config in front of it, and requires
// the handler to RUN, with the cluster's id: Fiber ignoring the trailing
// slash is what let a pool named ".." delete a cluster, and a fixture that
// did not reach the handler could not show the gate preventing anything.
// The query string rides along untouched — the revoke spelling is the
// variant that also deletes the Proxmox-side user and token — which is also
// the measurement that c.Path() leaves the query out of what the gate
// reads: the gate refuses that spelling too.
func TestTrailingSlashDeleteNeverReachesTheClusterDelete(t *testing.T) {
	base := pathPrefix + "clusters/" + testClusterID
	// The cluster delete also requires confirm=<the cluster's name>
	// (TestClusterDeleteRequiresTheClusterName), and a request a browser
	// resolved from a child path would not carry it. It rides along here so
	// that the twin reaches the handler, and the gate is shown to refuse the
	// slash on its own.
	for _, tt := range []struct {
		target string
		revoke bool
	}{
		{target: base + "/?confirm=cluster01"},
		{target: base + "//?confirm=cluster01"},
		{target: base + "/?confirm=cluster01&revoke_pve_credentials=1", revoke: true},
	} {
		t.Run(tt.target, func(t *testing.T) {
			bare, reached := newClusterDeleteProbe(t, false)
			status, env := send(t, bare, httptest.NewRequest(http.MethodDelete, tt.target, nil))
			if status != fiber.StatusNoContent || !reached.called || reached.params.String("id") != testClusterID {
				t.Fatalf("precondition: with only the router in front, DELETE %s answered %d %+v (handler ran: %v) — it "+
					"has to reach the cluster delete with the cluster's id, or a refusal prevents nothing",
					tt.target, status, env, reached.called)
			}
			if revoke, _ := reached.params.OptString("revoke_pve_credentials"); tt.revoke && revoke != "1" {
				t.Fatalf("precondition: revoke_pve_credentials reached the handler as %q, want \"1\"", revoke)
			}

			app, cap := newClusterDeleteProbe(t, true)
			status, env = send(t, app, httptest.NewRequest(http.MethodDelete, tt.target, nil))
			if status != fiber.StatusBadRequest || env.Error != "bad_request" || env.Message != trailingSlashRefusal {
				t.Errorf("DELETE %s behind the middleware stack answered %d %+v, want the gate's 400", tt.target, status, env)
			}
			if cap.called {
				t.Errorf("DELETE %s reached the cluster delete handler behind the middleware stack", tt.target)
			}
		})
	}

	// The same stack still dispatches the canonical spelling: the gate
	// refuses a slash, not a DELETE.
	app, cap := newClusterDeleteProbe(t, true)
	status, env := send(t, app, httptest.NewRequest(http.MethodDelete, base+"?confirm=cluster01", nil))
	if status != fiber.StatusNoContent || !cap.called || cap.params.String("id") != testClusterID {
		t.Errorf("DELETE %s behind the middleware stack answered %d %+v (handler ran: %v), want the handler to run",
			base, status, env, cap.called)
	}
}

// refusedByTheSlashGate reports what, if anything, is wrong with res as the
// gate's refusal: the status, the envelope, and the request id and security
// headers the refusal carries only because the gate sits after requestid
// and the security-header middleware.
func refusedByTheSlashGate(res servedResponse) []string {
	var wrong []string
	if res.status != fiber.StatusBadRequest || res.envelope.Error != "bad_request" ||
		res.envelope.Message != trailingSlashRefusal {
		wrong = append(wrong, fmt.Sprintf("answered %d %+v, want the gate's 400 (body %.200s)",
			res.status, res.envelope, res.body))
	}
	if res.requestID == "" || res.nosniff != "nosniff" {
		wrong = append(wrong, fmt.Sprintf("X-Request-Id = %q, X-Content-Type-Options = %q: the refusal did not pass "+
			"through requestid and the security headers", res.requestID, res.nosniff))
	}
	return wrong
}

// TestTrailingSlashGateRefusesEveryMethodButGetHeadAndOptions drives the
// assembled server with each method Fiber accepts, with and without a
// trailing slash.
//
// A refused method with the slash must get the gate's refusal, and without
// it must NOT — the positive control that the path itself is fine. GET,
// HEAD and OPTIONS must get byte-for-byte the answer the same request gets
// without the slash, which is what "unaffected" means; comparing against a
// status written into the table would pass for a gate that refused both
// spellings.
func TestTrailingSlashGateRefusesEveryMethodButGetHeadAndOptions(t *testing.T) {
	s := newAssembledServer(t)
	cluster := pathPrefix + "clusters/" + testClusterID

	for _, tt := range []struct {
		method, path string
		refused      bool
	}{
		// The four writes Nexara routes, each on a path that has a route
		// for that method: a public registry route, two registry routes
		// behind authentication, and the one PATCH, a legacy route.
		{fiber.MethodPost, pathPrefix + "auth/login", true},
		{fiber.MethodPut, cluster, true},
		{fiber.MethodDelete, cluster, true},
		{fiber.MethodPatch, cluster + "/vm-folders/" + testClusterID, true},
		// Routed nowhere here, and refused because Nexara answers only GET,
		// HEAD and OPTIONS as reads — not because something serves them.
		// TRACE and QUERY are safe methods to HTTP (RFC 9110 §9.2.1, RFC
		// 10008) and are refused all the same: the exemption is Nexara's
		// contract, not HTTP's list.
		{fiber.MethodConnect, cluster, true},
		{fiber.MethodTrace, pathPrefix + "version", true},
		{fiber.MethodQuery, pathPrefix + "version", true},

		{fiber.MethodGet, pathPrefix + "version", false},
		{fiber.MethodHead, pathPrefix + "version", false},
		{fiber.MethodOptions, cluster, false},
	} {
		t.Run(tt.method, func(t *testing.T) {
			plainCtx, _ := wireRequest{method: tt.method, target: tt.path}.build(t)
			plain := serveThroughApp(s, plainCtx)
			slashedCtx, _ := wireRequest{method: tt.method, target: tt.path + "/"}.build(t)
			slashed := serveThroughApp(s, slashedCtx)

			if plain.envelope.Message == trailingSlashRefusal {
				t.Fatalf("precondition: %s %s without a slash was refused by the gate", tt.method, tt.path)
			}
			if tt.refused {
				for _, w := range refusedByTheSlashGate(slashed) {
					t.Errorf("%s %s/: %s", tt.method, tt.path, w)
				}
				return
			}
			if slashed.status != plain.status || !bytes.Equal(slashed.body, plain.body) {
				t.Errorf("%s %s/ answered %d %.200q; without the slash it answers %d %.200q — a read "+
					"must be unaffected", tt.method, tt.path, slashed.status, slashed.body, plain.status, plain.body)
			}
		})
	}

	// "/" alone is the root: the router strips a slash only from a longer
	// path, and the gate uses the same length test.
	root, _ := wireRequest{method: fiber.MethodPost, target: "/"}.build(t)
	if res := serveThroughApp(s, root); res.envelope.Message == trailingSlashRefusal {
		t.Errorf("POST / was refused as a trailing slash (%d); the root has none for the router to strip", res.status)
	}
}

// TestTrailingSlashGatePrecedesEveryRoute is the structural half of the
// coverage claim: every route the assembled server mounts under a method
// the gate refuses — the registry's, the legacy ones in router.go and the
// two of those registered on a group root — every prefix an app-level Use
// mounts a handler on, and the SPA handler answer that method with a
// trailing slash with the gate's refusal. A route or path-scoped handler
// registered ahead of refuseTrailingSlashWrites would answer first and fail
// here, whichever it is.
//
// It cannot tell a route from a path that routes nowhere, since the gate
// answers both — that is TestTrailingSlashDeleteNeverReachesTheClusterDelete's
// job. What it adds is that the route table holds the kinds of route it
// claims to cover, so the sweep cannot be vacuous.
func TestTrailingSlashGatePrecedesEveryRoute(t *testing.T) {
	s := newAssembledServer(t)

	type probe struct{ method, path string }
	routes := s.app.GetRoutes(true)
	covered := map[string]bool{}
	var probes []probe
	for _, r := range routes {
		covered[r.Method+" "+r.Path] = true
		if isReadMethod(r.Method) {
			continue
		}
		probes = append(probes, probe{r.Method, fiberRouteParamRe.ReplaceAllString(r.Path, "x") + "/"})
	}
	for _, want := range []string{
		"DELETE /api/v1/clusters/:id",
		"POST /api/v1/auth/register",
		// The two legacy group roots: the table spells them with the slash
		// Fiber matches them without, which middleware.go says of them.
		"POST /api/v1/alert-rules/",
		"POST /api/v1/firewall-templates/",
		"PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id",
		"POST /api/v1/clusters/:cluster_id/storage/:storage_id/upload",
	} {
		if !covered[want] {
			t.Errorf("precondition: %s is not in the route table, so the sweep does not cover it", want)
		}
	}
	if len(probes) < 250 {
		t.Fatalf("precondition: only %d routes under a refused method; the assembled server mounts far more", len(probes))
	}

	// GetRoutes(true) leaves out what Use mounted, which GetRoutes(false)
	// lists under every method; a Use handler matches any method, so each
	// prefix is probed once, with a POST.
	usePrefixes := map[string]bool{}
	for _, r := range s.app.GetRoutes(false) {
		if !covered[r.Method+" "+r.Path] {
			usePrefixes[r.Path] = true
		}
	}
	if !usePrefixes["/api/v1/settings"] {
		t.Fatal("precondition: the legacy /api/v1/settings group's Use prefix is missing, so Use prefixes are not being found")
	}
	for prefix := range usePrefixes {
		// "/" itself is the root, not a trailing slash, to the router and
		// the gate alike; the SPA handler mounted there is probed below.
		if p := strings.TrimRight(fiberRouteParamRe.ReplaceAllString(prefix, "x"), "/") + "/"; p != "/" {
			probes = append(probes, probe{fiber.MethodPost, p})
		}
	}
	// A deep link the SPA handler serves to a GET.
	probes = append(probes, probe{fiber.MethodPost, "/clusters/x/vms/"})

	var failures []string
	for _, p := range probes {
		fctx, _ := wireRequest{method: p.method, target: p.path}.build(t)
		if wrong := refusedByTheSlashGate(serveThroughApp(s, fctx)); len(wrong) > 0 {
			failures = append(failures, fmt.Sprintf("%s %s: %s", p.method, p.path, strings.Join(wrong, "; ")))
		}
	}
	slices.Sort(failures)
	for _, f := range failures {
		t.Error(f)
	}
}

// TestTrailingSlashRefusalReachesACrossOriginCaller pins the gate after
// CORS, which middleware.go gives as a reason for where it sits: CORS sets
// its headers on a request that is not a preflight and then calls Next, so
// the refusal carries Access-Control-Allow-Origin and a cross-origin
// caller's browser shows it the 400. Ahead of CORS the gate would still
// refuse, and only this header would show the difference.
//
// The preflight is the other half: an OPTIONS with Access-Control-Request-
// Method is answered by CORS itself, with a 204 — the gate never sees it,
// and would let an OPTIONS through anyway.
func TestTrailingSlashRefusalReachesACrossOriginCaller(t *testing.T) {
	s := newAssembledServer(t)
	target := pathPrefix + "clusters/" + testClusterID + "/"

	refused, _ := wireRequest{method: fiber.MethodDelete, target: target,
		lines: []string{"Origin: https://nexara.example.com"}}.build(t)
	res := serveThroughApp(s, refused)
	for _, w := range refusedByTheSlashGate(res) {
		t.Error(w)
	}
	if acao := string(refused.Response.Header.Peek(fiber.HeaderAccessControlAllowOrigin)); acao != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q on the refusal, want \"*\" (CORS_ALLOW_ORIGINS is unset): "+
			"the gate answered before CORS set its headers", acao)
	}

	preflight, _ := wireRequest{method: fiber.MethodOptions, target: target, lines: []string{
		"Origin: https://nexara.example.com", "Access-Control-Request-Method: DELETE",
	}}.build(t)
	if res := serveThroughApp(s, preflight); res.status != fiber.StatusNoContent || res.envelope.Message != "" {
		t.Errorf("the preflight answered %d %+v, want CORS's own 204", res.status, res.envelope)
	}
}

// TestTrailingSlashRefusalIsAccessLogged pins the gate inside the access
// logger, the other reason middleware.go gives for its position: a refused
// request is logged, with its request id and the path as sent, like every
// other rejection. Moved ahead of the logger, the gate would still refuse,
// and only the log would show the difference.
//
// The logger writes to the standard output its package captured when it
// initialised, so this runs the test binary again as a child and reads the
// child's output — the production logger, unmodified — the way
// TestContentCodingRefusalIsAccessLogged does, for the same reasons. The
// plain request is the positive control: a missing line for the refusal
// means something only if the plain request's line is there.
func TestTrailingSlashRefusalIsAccessLogged(t *testing.T) {
	const plainID, refusedID = "slash-log-plain-0001", "slash-log-refused-0001"
	target := pathPrefix + "clusters/" + testClusterID + "/"
	if os.Getenv(accessLogChildEnv) == "1" {
		s := newAssembledServer(t)
		plain, _ := wireRequest{method: fiber.MethodGet, target: pathPrefix + "version",
			lines: []string{"X-Request-ID: " + plainID}}.build(t)
		serveThroughApp(s, plain)
		refused, _ := wireRequest{method: fiber.MethodDelete, target: target,
			lines: []string{"X-Request-ID: " + refusedID}}.build(t)
		serveThroughApp(s, refused)
		return
	}

	child := exec.Command(os.Args[0], "-test.run=^TestTrailingSlashRefusalIsAccessLogged$", "-test.count=1")
	child.Env = append(os.Environ(), accessLogChildEnv+"=1")
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	if err := child.Run(); err != nil {
		t.Fatalf("the child run failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.Bytes(), stderr.Bytes())
	}
	lineFor := func(id string) string {
		for _, line := range strings.Split(stdout.String(), "\n") {
			if strings.Contains(line, "| "+id+" |") {
				return line
			}
		}
		return ""
	}

	if line := lineFor(plainID); !strings.Contains(line, "| 200 |") {
		t.Fatalf("precondition: no access-log line with status 200 for the plain request (found %q), so the "+
			"child's output is not showing the access log at all\n%s", line, stdout.Bytes())
	}
	line := lineFor(refusedID)
	if line == "" {
		t.Fatalf("the refused request left no line in the access log: the gate answers from outside the logger\n%s",
			stdout.Bytes())
	}
	if !strings.Contains(line, "| 400 |") || !strings.HasSuffix(strings.TrimSpace(line), "| DELETE "+target) {
		t.Errorf("access-log line %q, want the 400 for DELETE %s", line, target)
	}
}

// TestTrailingSlashGateReadsThePathTheRouterStrips measures what c.Path()
// hands the gate, which middleware.go states and the gate relies on: the
// raw request path, the query left out, nothing decoded and nothing
// resolved. An encoded slash is therefore NOT a trailing slash to the gate —
// nor to the router, which strips only the bytes this returns.
func TestTrailingSlashGateReadsThePathTheRouterStrips(t *testing.T) {
	s := newAssembledServer(t)
	cluster := pathPrefix + "clusters/" + testClusterID

	for _, tt := range []struct{ target, path string }{
		{cluster + "/?revoke_pve_credentials=1", cluster + "/"},
		{cluster + "%2F", cluster + "%2F"},
		{cluster + "/pools/%2e%2e", cluster + "/pools/%2e%2e"},
		{cluster + "/pools/../", cluster + "/pools/../"},
	} {
		fctx, _ := wireRequest{method: fiber.MethodDelete, target: tt.target}.build(t)
		c := s.app.AcquireCtx(fctx)
		got := c.Path()
		s.app.ReleaseCtx(c)
		if got != tt.path {
			t.Errorf("c.Path() for %q = %q, want %q", tt.target, got, tt.path)
		}
	}
}
