package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// settingsRouteCount is how many endpoints registerSettingsEndpoints declares.
// It is THREE of SettingsHandler's nine — see TestSettingsReadsAreStillLegacy
// for the other six and why each one stays.
const settingsRouteCount = 3

// settingsRoutesOutsideTheClusterCheckShape records why none of the three is a
// cluster-scoped Check. It is folded into routesOutsideTheClusterCheckShape in
// registry_vms_test.go.
var settingsRoutesOutsideTheClusterCheckShape = map[string]string{
	"POST /api/v1/settings/branding/logo":    "global: instance branding belongs to the install, not to a cluster",
	"POST /api/v1/settings/branding/favicon": "global: instance branding belongs to the install, not to a cluster",
	"DELETE /api/v1/settings/:key": "Deferred: the permission depends on the ?scope= the caller sends — " +
		"manage:settings for a shared row, none for the caller's own",
}

// settingsLegacyPermissions is what each MIGRATED handler checked BEFORE Phase
// 6j, transcribed from `git show HEAD:internal/api/handlers/settings.go` at
// commit eaeafa7.
//
// The file held THREE requirePerm sites across nine handlers: one in each
// upload, unconditional, and one inside settingScopeID, reached by all six
// generic routes but executed only when `write && adminOnly`. That is the whole
// finding of this domain, and TestSettingsReadsAreStillLegacy is the other half
// of it.
var settingsLegacyPermissions = map[string]string{
	"POST /api/v1/settings/branding/logo":    "manage:settings",
	"POST /api/v1/settings/branding/favicon": "manage:settings",
	"DELETE /api/v1/settings/:key":           "deferred",
}

func declaredSettingsEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		if strings.HasPrefix(e.Path, settingsScope) {
			out[e.Method+" "+e.Path] = e
		}
	}
	return out
}

// TestSettingsRoutesDeclareWhatTheyEnforced is the tally for the three routes
// that could be declared.
func TestSettingsRoutesDeclareWhatTheyEnforced(t *testing.T) {
	declared := declaredSettingsEndpoints(t)
	if len(declared) != settingsRouteCount {
		t.Fatalf("the registry declares %d settings routes, want %d", len(declared), settingsRouteCount)
	}

	for key, want := range settingsLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s is in the tally but is not declared in the registry", key)
			continue
		}
		if got := e.Permissions.Describe(); got != want {
			t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want)
		}
	}

	// The two uploads are GLOBAL Checks: instance branding is not a cluster's.
	for _, key := range []string{
		"POST /api/v1/settings/branding/logo",
		"POST /api/v1/settings/branding/favicon",
	} {
		e := declared[key]
		if e.Permissions.Check == nil || e.Permissions.Check.Scope != ScopeGlobal {
			t.Errorf("%s declares %q; it has to be a GLOBAL Check", key, e.Permissions.Describe())
		}
	}

	// The delete's Deferred reason has to name the CONDITION, not merely say
	// that the handler checks something — that is the field's own contract, and
	// on this route the condition is the whole point.
	reason := declared["DELETE /api/v1/settings/:key"].Permissions.Deferred
	for _, phrase := range []string{"scope", "manage:settings", "settingScopeID", "user_id"} {
		if !strings.Contains(reason, phrase) {
			t.Errorf("the delete's Deferred reason does not mention %q: %q", phrase, reason)
		}
	}
}

// TestSettingsReadsAreStillLegacy records the decision this tranche's hardest
// domain produced, so every one of the six stays a decision rather than
// becoming a gap.
//
// SIX of SettingsHandler's nine routes are NOT declared, for THREE distinct
// reasons:
//
//   - GET /settings and GET /settings/:key perform no permission check on any
//     path. settingScopeID gates only when `write && adminOnly`, and both pass
//     write=false — which is the live instance rbac_route_guard_test.go's
//     LIMITATION note names: the check is REACHABLE from them and never runs.
//     They are instanceSharedRoutes-shaped for ?scope=global and self-service
//     for ?scope=user, selected per request, and Permissions has no shape for
//     either half. Declaring them Deferred would render as "deferred" to an
//     operator reading the docs, which claims a runtime check that does not
//     exist — a declaration saying MORE than the code enforces.
//   - PUT /settings/:key has the same conditional check as the delete and would
//     declare the same Deferred, but its `value` is arbitrary JSON: the
//     branding page stores a string, the appearance page an object, and
//     json.Valid is the only rule. apischema's Type vocabulary has no "any JSON
//     value" member, so no declaration can accept both.
//   - GET /settings/branding and the two branding file routes are
//     instanceSharedRoutes-shaped outright.
//
// Pinned from both sides: each must still be registered, and none may be in the
// registry — a well-meaning later declaration of any of them is a silent
// behaviour change, not a compile error.
func TestSettingsReadsAreStillLegacy(t *testing.T) {
	keys := []string{
		"GET /api/v1/settings",
		"GET /api/v1/settings/:key",
		"PUT /api/v1/settings/:key",
		"GET /api/v1/settings/branding",
		"GET /api/v1/settings/branding/logo-file",
		"GET /api/v1/settings/branding/favicon-file",
	}

	s := newRouteStubServer(t)
	inRegistry := registryRouteKeySet(s.registry.Endpoints())
	registered := map[string]bool{}
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == "USE" || len(r.Handlers) == 0 {
			continue
		}
		registered[r.Method+" "+normalizeRoutePath(r.Path)] = true
	}

	for _, key := range keys {
		if inRegistry[key] {
			t.Errorf("%s is declared in the registry — see registerSettingsEndpoints for why it cannot be", key)
		}
		if !registered[key] {
			t.Errorf("%s is not registered at all — it is meant to stay in router.go, not to disappear", key)
		}
		if !legacyRouteBaseline[key] {
			t.Errorf("%s is not in legacyRouteBaseline; the ratchet would flag it as a new legacy route", key)
		}
	}

	// The three branding reads carry their instanceSharedRoutes exemption, and
	// it stays theirs: folding them into selfServiceRoutes would make that
	// list's stated invariant ("acts on the caller's own identity") false, which
	// is the failure that map's own comment exists to prevent.
	for _, key := range []string{
		"GET /api/v1/settings/branding",
		"GET /api/v1/settings/branding/logo-file",
		"GET /api/v1/settings/branding/favicon-file",
	} {
		if _, shared := instanceSharedRoutes[key]; !shared {
			t.Errorf("%s is not in instanceSharedRoutes; it serves instance data with no subject to authorize", key)
		}
		if _, self := selfServiceRoutes[key]; self {
			t.Errorf("%s is in selfServiceRoutes, which claims it acts on the caller's own identity — it does not", key)
		}
	}

	// The two generic READS are in NEITHER list, and that is the gap this
	// migration reports rather than closes: they perform no check, and adding
	// one is a behaviour change that could break a working deployment. Asserted
	// so the gap cannot be quietly papered over with an exemption entry instead
	// of being fixed.
	for _, key := range []string{"GET /api/v1/settings", "GET /api/v1/settings/:key"} {
		if _, shared := instanceSharedRoutes[key]; shared {
			t.Errorf("%s was added to instanceSharedRoutes — it is not instance-shared for ?scope=user, "+
				"where it returns the caller's own rows; if the decision has changed, change the code, "+
				"not the exemption list", key)
		}
	}
}

// TestSettingScopeVocabulary pins the declared Enum against the handler's own
// settingScopes map.
//
// The direction that bites is a schema accepting a scope the map has no entry
// for: the zero settingScope is perUser false AND adminOnly false, i.e. a
// SHARED row that no permission gates — which is exactly the hole the 'cluster'
// scope used to be, and why it is in neither list.
func TestSettingScopeVocabulary(t *testing.T) {
	got := slices.Clone(declaredEndpoint(t, fiber.MethodDelete, settingsScope+"/:key").Parameters["scope"].Enum)
	slices.Sort(got)
	if want := handlers.SettingScopeKeys(); !slices.Equal(got, want) {
		t.Errorf("the declared ?scope= enum is %v but handlers.settingScopes carries %v — a scope in the "+
			"schema and not the map falls through to the zero settingScope, a shared row nothing gates",
			got, want)
	}
	if got := declaredEndpoint(t, fiber.MethodDelete, settingsScope+"/:key").Parameters["scope"].Default; got != "user" {
		t.Errorf("?scope= default = %#v, want \"user\" — the value the handler substituted", got)
	}
	if slices.Contains(got, "cluster") {
		t.Error("the declared enum accepts \"cluster\"; that scope has no cluster id to key on and would " +
			"land on a second shared namespace with no write gate")
	}
}

// TestSettingsDeleteRejectsWhatTheHandlerUsedTo drives the declaration end to
// end over the refusals settingScopeID and settingKeyFromPath used to make.
func TestSettingsDeleteRejectsWhatTheHandlerUsedTo(t *testing.T) {
	const path = settingsScope + "/:key"
	e := declaredEndpoint(t, fiber.MethodDelete, path)

	for _, tt := range []struct {
		key   string
		query string
		want  int
	}{
		{"dashboard.layout", "", fiber.StatusNoContent},
		{"dashboard.layout", "?scope=user", fiber.StatusNoContent},
		{"dashboard.layout", "?scope=global", fiber.StatusNoContent},
		{"dashboard.layout", "?scope=cluster", fiber.StatusBadRequest},
		{"dashboard.layout", "?scope=node", fiber.StatusBadRequest},
		{"dashboard.layout", "?scopes=user", fiber.StatusBadRequest},
		{strings.Repeat("k", 129), "", fiber.StatusBadRequest},
	} {
		cap := &capture{}
		probe := e
		probe.Handler = cap.handler()
		probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), probe)

		target := strings.ReplaceAll(path, ":key", tt.key) + tt.query
		status, env := send(t, app, httptest.NewRequest(http.MethodDelete, target, nil))
		if status != tt.want {
			t.Errorf("key %q%s: status = %d (%q), want %d", tt.key, tt.query, status, env.Message, tt.want)
			continue
		}
		if tt.want != fiber.StatusNoContent {
			continue
		}
		wantScope := "user"
		if strings.Contains(tt.query, "global") {
			wantScope = "global"
		}
		if got := cap.params.String("scope"); got != wantScope {
			t.Errorf("key %q%s: scope reached the handler as %q, want %q", tt.key, tt.query, got, wantScope)
		}
	}
}

// TestBrandingUploadsDeclareNoParameters is the one assertion this domain's
// uploads genuinely needed, and it is not about permissions.
//
// Both are MULTIPART. api.Endpoint.bodyValues decodes a body only for a JSON
// content type and otherwise returns without touching it — which is
// load-bearing rather than tidy: Fiber runs with StreamRequestBody, so reading
// the body in extraction would drain the upload before c.FormFile could see it,
// and the endpoint would answer "Logo file is required" for a request that
// carried one. This drives a real multipart POST through the real mount and
// reads the file back out on the other side.
func TestBrandingUploadsDeclareNoParameters(t *testing.T) {
	for _, tt := range []struct {
		path  string
		field string
	}{
		{settingsScope + "/branding/logo", "logo"},
		{settingsScope + "/branding/favicon", "favicon"},
	} {
		t.Run(tt.path, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodPost, tt.path)
			if len(e.Parameters) != 0 {
				t.Fatalf("declares %d parameters; the payload is multipart, and declaring a body "+
					"parameter would make extraction drain the upload stream", len(e.Parameters))
			}

			var got string
			probe := e
			probe.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
			probe.Handler = func(c fiber.Ctx, _ *apischema.Params) error {
				file, err := c.FormFile(tt.field)
				if err != nil {
					return fiber.NewError(fiber.StatusBadRequest, "the upload stream was already consumed: "+err.Error())
				}
				got = file.Filename
				return c.SendStatus(fiber.StatusNoContent)
			}
			app := newRegistryApp(t, noAuth(), probe)

			var body bytes.Buffer
			w := multipart.NewWriter(&body)
			part, err := w.CreateFormFile(tt.field, tt.field+".png")
			if err != nil {
				t.Fatalf("build multipart body: %v", err)
			}
			if _, err := part.Write([]byte("\x89PNG\r\n\x1a\n")); err != nil {
				t.Fatalf("write multipart part: %v", err)
			}
			if err := w.Close(); err != nil {
				t.Fatalf("close multipart writer: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, tt.path, &body)
			req.Header.Set(fiber.HeaderContentType, w.FormDataContentType())
			status, env := send(t, app, req)
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			if got != tt.field+".png" {
				t.Errorf("the handler read %q out of the multipart body, want %q", got, tt.field+".png")
			}
		})
	}
}

// TestEverySettingsEndpointIsDocumented holds the declarations to the standard
// that makes this whole effort worth doing.
func TestEverySettingsEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredSettingsEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Settings" {
			t.Errorf("%s is in group %q, want Settings", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
