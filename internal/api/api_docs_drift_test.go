package api

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// newRouteStubServer builds a Server whose handler fields are all
// non-nil zero values, checks none was missed, and registers the routes —
// the three steps every caller needs before it can walk app.GetRoutes. No
// handler is ever invoked — route registration only stores bound method
// values — so zero-value handlers are safe.
//
// If a new handler field is added to Server, add it here too; the stub
// check fails with an explicit message when a handler field is left nil.
func newRouteStubServer(t *testing.T) *Server {
	t.Helper()
	s := &Server{
		app:                    fiber.New(),
		authHandler:            &handlers.AuthHandler{},
		clusterHandler:         &handlers.ClusterHandler{},
		pbsHandler:             &handlers.PBSHandler{},
		veeamHandler:           &handlers.VeeamHandler{},
		nodeHandler:            &handlers.NodeHandler{},
		vmHandler:              &handlers.VMHandler{},
		containerHandler:       &handlers.ContainerHandler{},
		storageHandler:         &handlers.StorageHandler{},
		virtioWinHandler:       &handlers.VirtioWinHandler{},
		guestToolsHandler:      &handlers.GuestToolsHandler{},
		vmImportHandler:        &handlers.VMImportHandler{},
		vmFoldersHandler:       &handlers.VMFoldersHandler{},
		favoritesHandler:       &handlers.FavoritesHandler{},
		metricsHandler:         &handlers.MetricsHandler{},
		cephHandler:            &handlers.CephHandler{},
		backupHandler:          &handlers.BackupHandler{},
		guestSnapshotHandler:   &handlers.GuestSnapshotHandler{},
		taskHandler:            &handlers.TaskHandler{},
		scheduleHandler:        &handlers.ScheduleHandler{},
		auditHandler:           &handlers.AuditHandler{},
		drsHandler:             &handlers.DRSHandler{},
		migrationHandler:       &handlers.MigrationHandler{},
		networkHandler:         &handlers.NetworkHandler{},
		rbacHandler:            &handlers.RBACHandler{},
		userHandler:            &handlers.UserHandler{},
		ldapHandler:            &handlers.LDAPHandler{},
		oidcHandler:            &handlers.OIDCHandler{},
		totpHandler:            &handlers.TOTPHandler{},
		cveHandler:             &handlers.CVEHandler{},
		alertHandler:           &handlers.AlertHandler{},
		notificationDLQHandler: &handlers.NotificationDLQHandler{},
		reportHandler:          &handlers.ReportHandler{},
		rollingUpdateHandler:   &handlers.RollingUpdateHandler{},
		settingsHandler:        &handlers.SettingsHandler{},
		clusterOptionsHandler:  &handlers.ClusterOptionsHandler{},
		haHandler:              &handlers.HAHandler{},
		poolHandler:            &handlers.PoolHandler{},
		accessHandler:          &handlers.AccessHandler{},
		replicationHandler:     &handlers.ReplicationHandler{},
		acmeHandler:            &handlers.ACMEHandler{},
		aptRepositoryHandler:   &handlers.AptRepositoryHandler{},
		metricServerHandler:    &handlers.MetricServerHandler{},
		searchHandler:          &handlers.SearchHandler{},
		apiKeyHandler:          &handlers.APIKeyHandler{},
		apiDocsHandler:         &handlers.APIDocsHandler{},
		changelogHandler:       &handlers.ChangelogHandler{},
	}
	requireAllHandlersStubbed(t, s)
	s.setupRoutes()
	return s
}

// requireAllHandlersStubbed fails the test if any *handlers.X field of
// the stub Server is nil. setupRoutes gates each route block on its
// handler being non-nil, so a nil field would silently drop that
// block's routes from the table and let stale endpointMeta keys pass
// (or fresh ones fail) for the wrong reason.
func requireAllHandlersStubbed(t *testing.T, s *Server) {
	t.Helper()
	sv := reflect.ValueOf(s).Elem()
	st := sv.Type()
	for i := range st.NumField() {
		f := st.Field(i)
		// Recognise handler fields by type (pointer to a struct in the
		// handlers package) OR by the *Handler naming convention, so a
		// future interface-typed handler field can't slip past the guard.
		byType := f.Type.Kind() == reflect.Pointer && f.Type.Elem().Kind() == reflect.Struct &&
			strings.HasSuffix(f.Type.Elem().PkgPath(), "internal/api/handlers")
		byName := strings.HasSuffix(f.Name, "Handler")
		if !byType && !byName {
			continue
		}
		switch f.Type.Kind() {
		case reflect.Pointer, reflect.Interface:
			if sv.Field(i).IsNil() {
				t.Fatalf("Server.%s is nil in newRouteStubServer — add it to the stub so its routes register", f.Name)
			}
		default:
			// Value-typed handler fields can't be nil; nothing to check.
		}
	}
}

// TestEndpointMetaMatchesRegisteredRoutes asserts every curated
// endpointMeta key ("METHOD path") matches a route actually registered
// by setupRoutes. GetDocs looks entries up with the registered path
// verbatim, so a key with a stale path or param name (":id" vs
// ":cluster_id") is dead weight: the endpoint renders with blank
// description/permission and nothing else notices. This test makes
// that drift a build failure instead.
//
// The reverse direction — every route having a curated entry — is
// intentionally NOT enforced: routes without an entry get auto-derived
// metadata by design (see the endpointMeta doc comment).
func TestEndpointMetaMatchesRegisteredRoutes(t *testing.T) {
	s := newRouteStubServer(t)

	registered := make(map[string]bool)
	for _, r := range s.app.GetRoutes(true) {
		// handlers.NormalizeDocPath is GetDocs' own trailing-slash
		// normalisation: Group(...) + .Post("/") registers
		// "/api/v1/api-keys/"; curated keys use the trimmed form. Calling
		// the real function rather than re-deriving the rule here is what
		// keeps this guard unable to drift from what GetDocs actually does.
		path := handlers.NormalizeDocPath(r.Path)
		registered[r.Method+" "+path] = true
	}

	for _, key := range handlers.EndpointMetaKeys() {
		if !registered[key] {
			t.Errorf("endpointMeta key %q matches no registered route — update the key in internal/api/handlers/api_docs.go to the exact method+path registered in router.go (param names included)", key)
		}
	}
}

// endpointMetaSurvivingKeys is the exact, closed set of keys endpointMeta
// is supposed to carry: the 13 legacy routes (of the 17 in router.go) that
// have a curated entry — see the doc comment on endpointMeta itself
// (internal/api/handlers/api_docs.go) for which 13 and why.
//
// This exists because TestEndpointMetaMatchesRegisteredRoutes only checks
// the direction "every key that exists points at a real route" — it has
// no floor on HOW MANY keys should exist, so it passes just as cleanly
// over 13 keys, 3 keys, or zero. That gap is not hypothetical: it is what
// let a real regression through in an earlier draft of the cleanup that
// produced this list — deleting any of these (which GetDocs DOES render
// for these legacy routes, unlike the ~223 removed entries for
// registry-declared ones) silently blanks that route's description and
// permission in the live /api/v1/api-docs payload, and nothing in this
// package's test suite noticed. TestGuard_DocumentedPermissionMatchesEnforcement's
// own anti-vacuity floor (rbac_route_guard_test.go) checks that SOMETHING
// was compared, not that these specific keys survived, so it does not
// substitute for this.
//
// It grew from 9 to 13 when the four routes whose section groupFromPath
// derived WRONG earned an entry — the storage-content delete and the
// vm-folders reparent (both rendered under "Clusters") and the two
// firewall-template writes (which had a two-route "Firewall Templates"
// section to themselves, beside the "Firewall" one holding their siblings).
// None of the four can be declared instead; the endpointMeta doc comment
// names the limitation holding each, and
// TestGuard_NoRouteFallsThroughToADerivedGroup below is what would have
// found them.
var endpointMetaSurvivingKeys = []string{
	"DELETE /api/v1/clusters/:cluster_id/storage/:storage_id/content/*",
	"GET /api/v1/api-docs",
	"GET /api/v1/auth/oidc/callback",
	"GET /api/v1/settings",
	"GET /api/v1/settings/:key",
	"PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id",
	"POST /api/v1/alert-rules",
	"POST /api/v1/auth/logout",
	"POST /api/v1/auth/register",
	"POST /api/v1/firewall-templates",
	"PUT /api/v1/alert-rules/:id",
	"PUT /api/v1/firewall-templates/:id",
	"PUT /api/v1/settings/:key",
}

// TestGuard_EndpointMetaKeysAreExactlyTheSurvivingSet is the floor
// TestEndpointMetaMatchesRegisteredRoutes does not provide: it fails,
// naming the key, if endpointMeta ever loses one of the 13 surviving
// entries OR gains a new one that was not deliberately added to
// endpointMetaSurvivingKeys above. A genuinely new legacy route earning a
// curated entry is expected to update this list in the same change; an
// entry disappearing without this list changing is the regression this
// guard exists to catch.
func TestGuard_EndpointMetaKeysAreExactlyTheSurvivingSet(t *testing.T) {
	got := handlers.EndpointMetaKeys()

	want := make(map[string]bool, len(endpointMetaSurvivingKeys))
	for _, k := range endpointMetaSurvivingKeys {
		want[k] = true
	}
	gotSet := make(map[string]bool, len(got))
	for _, k := range got {
		gotSet[k] = true
	}

	for _, k := range got {
		if !want[k] {
			t.Errorf("endpointMeta carries %q, which is not in endpointMetaSurvivingKeys — "+
				"if this is a deliberate new legacy-route entry, add it to that list; "+
				"if not, it is an unreviewed addition to what GetDocs renders for a legacy route", k)
		}
	}
	for _, k := range endpointMetaSurvivingKeys {
		if !gotSet[k] {
			t.Errorf("endpointMetaSurvivingKeys expects %q but endpointMeta no longer carries it — "+
				"this route's description and permission just went blank in the live "+
				"/api/v1/api-docs payload; restore the entry or, if the route stopped being "+
				"legacy (it now has a registry declaration), remove it from this list too", k)
		}
	}
}

// ── Derived docs sections ─────────────────────────────────────────────
//
// Group is the section structure of /api/v1/api-docs and of the in-app
// catalog at /settings/api-docs: the handler sorts by it and the SPA renders
// one collapsible card per distinct value. A declared route states its Group;
// a LEGACY route states one only if somebody wrote an endpointMeta entry for
// it; otherwise handlers.groupFromPath derives one from the second path
// segment and the route renders under whatever that produces.
// registry_group_guard_test.go pins the vocabulary a STATED section may use,
// from either of those two sources. The two guards meet on that word: this
// one says which routes are allowed to state nothing, and that one says what
// the ones that do state something are allowed to say.
//
// Derivation is right often enough to hide how often it is not, and it fails
// silently in both of its shapes. A nested route lands in its PARENT's
// section — the storage-content delete and the vm-folders reparent both
// derived "Clusters", away from the 12 and 4 declared siblings they belong
// with — and a collection of its own invents a SECTION of its own, which is
// how the two firewall-template writes came to sit in a two-route "Firewall
// Templates" card beside the "Firewall" section that already held their four
// declared siblings. Nothing 500s,
// nothing logs, and the docs look healthy from the inside; an operator
// hunting for an endpoint opens the wrong card and concludes it does not
// exist.
//
// Nothing caught any of that, and the reason is worth naming: every existing
// docs guard checks a value that IS stated (does the key match a route, does
// the permission match enforcement, is the Group canonical). None of them
// can see a route that states nothing, because there is no value to check.
// The guard below is the other direction — it reports the ABSENCE.

// derivedGroupAllowList is the reviewed set of routes whose docs section is
// derived rather than stated, mapped to the section each one derives to.
//
// The value is not decoration. Being on this list says "the derived section
// is the right one", which is a claim about a specific string — and the
// three entries here are true only by coincidence: /api/v1/settings/branding
// derives "Settings" because branding happens to live under the settings
// collection, not because anybody filed it there.
//
// The value column catches the way that coincidence can break WITHOUT the
// route changing: groupFromPath's own algorithm moving under it — a
// title-casing rule, a different segment, a new special case — so that the
// same method and path start deriving something else. The key columns catch
// the other way, a route that moves: relocate the branding reads to
// /api/v1/branding and all three keys go missing at once, which the forward
// and reverse loops both report. Either way the SPA does not quietly grow a
// card. Keep both, because neither sees the other's case.
//
// TO ADD AN ENTRY: only when the derived value genuinely is the section the
// route belongs in. Otherwise the fix is a declaration, or — if a real
// limitation blocks one, as it does for the four routes whose derived
// section was wrong and which would otherwise have doubled this list — an
// endpointMeta entry naming the section outright.
var derivedGroupAllowList = map[string]string{
	"GET /api/v1/settings/branding":              "Settings",
	"GET /api/v1/settings/branding/favicon-file": "Settings",
	"GET /api/v1/settings/branding/logo-file":    "Settings",
}

// liveDocsPayload renders the REAL /api/v1/api-docs payload: the production
// handler, holding the declarations setupRoutes pushed into it, reading the
// route table setupRoutes built, marshalled and decoded the way a caller
// receives it.
//
// Reading the payload rather than the inputs is the point. Group reaches a
// reader through three merging steps — declaration, then overlay, then
// derivation — and a guard that re-implemented the merge would be checking
// its own copy of the rule rather than the rule.
//
// It is mounted on a throwaway app rather than called through s.app because
// the real route sits behind authRequired and a stub Server has no signing
// key to mint a session with. Same handler, same route table; only the gate
// in front of it is absent. SetApp is New()'s half of the wiring (see
// server.go) and has to happen here, since a stub Server never goes through
// New.
func liveDocsPayload(t *testing.T, s *Server) []handlers.APIEndpoint {
	t.Helper()
	s.apiDocsHandler.SetApp(s.app)

	probe := fiber.New()
	probe.Get("/docs", s.apiDocsHandler.GetDocs)

	resp, err := probe.Test(httptest.NewRequest(fiber.MethodGet, "/docs", nil))
	if err != nil {
		t.Fatalf("requesting the docs payload: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != fiber.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("docs payload status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var out handlers.ListResponse[handlers.APIEndpoint]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding the docs payload: %v", err)
	}
	return out.Items
}

// statedDocGroups is every route whose docs section somebody DECIDED, keyed
// the way the payload keys it, mapped to what they decided.
//
// It exists because the payload alone cannot tell a stated section from a
// derived one: the two are frequently the same string. "GET /api/v1/settings"
// renders "Settings" whether the overlay says so or groupFromPath works it
// out, and the difference — whether anybody looked — is exactly what the
// guard is about.
func statedDocGroups(s *Server) map[string]string {
	out := make(map[string]string, s.registry.Len())
	for _, e := range docEndpoints(s.registry) {
		// Normalised through the function the docs lookup uses, so a
		// declaration mounted at ".../foo/" keys the way the payload will
		// render it. Register requires a non-blank Group, so there is no
		// empty value to filter here.
		out[e.Method+" "+handlers.NormalizeDocPath(e.Path)] = e.Group
	}
	// The overlay second, and only where nothing is declared: GetDocs takes a
	// declaration WHOLE and never consults the overlay for a declared route,
	// so letting an overlay entry shadow one here would make this map
	// disagree with the payload it is compared against.
	for key, group := range handlers.EndpointMetaGroups() {
		if group == "" {
			continue
		}
		if _, declared := out[key]; declared {
			continue
		}
		out[key] = group
	}
	return out
}

// derivedGroupFindings checks both directions and reports what it finds,
// plus how many payload entries it looked at.
//
// Forward: a payload entry whose section nothing states, and which the
// reviewed list does not cover — or covers with a different value than it
// now derives to.
//
// Reverse: a reviewed entry that is no longer earning its place, because the
// route is gone or because its section became stated. That half has no
// natural pressure on it and decays quietly: a leftover exemption reads as
// review and is none, and the next route to land on that key inherits a
// blessing nobody gave it.
//
// stated and allowed are parameters rather than package references so a
// synthetic input can prove the comparison fires; examined is returned for
// the anti-vacuity assertion the callers make.
func derivedGroupFindings(payload []handlers.APIEndpoint, stated, allowed map[string]string) (findings []string, examined int) {
	inPayload := make(map[string]bool, len(payload))

	for _, e := range payload {
		examined++
		key := e.Method + " " + e.Path
		inPayload[key] = true

		if _, decided := stated[key]; decided {
			continue
		}
		want, reviewed := allowed[key]
		if !reviewed {
			findings = append(findings, fmt.Sprintf(
				"%s renders in docs section %q, which handlers.groupFromPath DERIVED from its path — "+
					"neither a registry declaration nor an endpointMeta entry states one. Derivation reads the "+
					"second path segment, so a nested route lands in its parent's section and a collection of its "+
					"own invents a section of its own; four routes rendered in the wrong card that way before "+
					"anything looked. Declare the route (internal/api/registry_*.go), which also shrinks the legacy "+
					"set; if a real limitation blocks that, give it an endpointMeta entry naming the section its "+
					"siblings carry (internal/api/handlers/api_docs.go). Only if the derived section really is the "+
					"right one, add it to derivedGroupAllowList with the value it derives to.",
				key, e.Group))
			continue
		}
		if want != e.Group {
			findings = append(findings, fmt.Sprintf(
				"%s now derives docs section %q, but derivedGroupAllowList records %q. The derived value moved — "+
					"the route was renamed, or groupFromPath changed — so the coincidence that made deriving "+
					"acceptable no longer holds in the form it was reviewed in. Re-check which section the route "+
					"belongs in, then either update the recorded value or state the section outright.",
				key, e.Group, want))
		}
	}

	for _, key := range slices.Sorted(maps.Keys(allowed)) {
		if group, decided := stated[key]; decided {
			findings = append(findings, fmt.Sprintf(
				"derivedGroupAllowList lists %s, but its section is now STATED (%q) rather than derived — "+
					"delete the line. An exemption for something that no longer needs one reads as review and is "+
					"none, and the next route to take that key would inherit it.", key, group))
			continue
		}
		if !inPayload[key] {
			findings = append(findings, fmt.Sprintf(
				"derivedGroupAllowList lists %s, but no route in the docs payload has that key — it was renamed "+
					"or removed. Delete the line, or correct it to the key the route carries now.", key))
		}
	}

	return findings, examined
}

// TestGuard_NoRouteFallsThroughToADerivedGroup is the production guard, over
// the real payload a caller receives.
func TestGuard_NoRouteFallsThroughToADerivedGroup(t *testing.T) {
	s := newRouteStubServer(t)

	payload := liveDocsPayload(t, s)
	stated := statedDocGroups(s)

	findings, examined := derivedGroupFindings(payload, stated, derivedGroupAllowList)

	// Anti-vacuity, on the input rather than on the findings. A payload that
	// came back empty — a handler that lost its app, a route table built
	// before the registry mounted — produces zero findings and reports PASS,
	// which is what a healthy payload reports too. The registry's own size is
	// the floor because every declaration must appear in the payload
	// (TestGuard_DeclaredDocsReachTheDocsHandler proves that separately), so
	// anything smaller means the payload is not the one callers get.
	if examined < s.registry.Len() {
		t.Fatalf("the docs payload carries %d endpoints but the registry declares %d; this guard would be "+
			"checking something other than what /api/v1/api-docs serves", examined, s.registry.Len())
	}
	if len(stated) == 0 {
		t.Fatal("no route states a docs section at all; every route would read as derived and the guard " +
			"would be reporting the wrong thing")
	}
	// The other direction of the same worry, and the one that would not
	// announce itself. An over-broad `stated` — statedDocGroups reading the
	// ROUTE TABLE rather than the declarations, say — marks every route
	// decided, the forward loop `continue`s past all of them, and the guard
	// reports a clean payload while comparing nothing. Neither assertion above
	// notices: the payload is full and `stated` is non-empty.
	//
	// It is checked structurally rather than by asserting that some route is
	// still derived, because derivedGroupAllowList emptying is the STATED GOAL
	// of this work — entries are meant to leave it — and a floor that fails
	// when the last one does is a floor somebody deletes. What cannot
	// legitimately change is where a stated section may come from: a
	// declaration or an overlay entry, and nothing else.
	mayState := make(map[string]bool, len(stated))
	for _, e := range docEndpoints(s.registry) {
		mayState[e.Method+" "+handlers.NormalizeDocPath(e.Path)] = true
	}
	for key, group := range handlers.EndpointMetaGroups() {
		if group != "" {
			mayState[key] = true
		}
	}
	for key := range stated {
		if !mayState[key] {
			t.Fatalf("statedDocGroups reports that %q states a docs section, but neither the registry nor "+
				"the overlay carries it — the map has grown a third source. Every route it wrongly covers "+
				"is a route this guard silently stops checking, while still reporting a clean payload", key)
		}
	}

	for _, f := range findings {
		t.Error(f)
	}
}

// TestGuard_NoRouteFallsThroughToADerivedGroup_CatchesAStatedGroupGoingMissing
// runs the production detector over the REAL payload with one real stated
// section removed — the exact regression the guard exists for, since an
// endpointMeta entry deleted (or a declaration migrated away) is how a route
// starts falling through.
//
// It exists because the payload is clean, and a clean guard is where vacuity
// hides: a detector that had stopped comparing anything reports zero findings
// over 551 endpoints and looks identical to the test above. The reparent is
// the case chosen because its derived section is WRONG in the quiet way —
// "Clusters" is a real section full of real routes, so the route does not
// vanish, it just files itself with strangers.
func TestGuard_NoRouteFallsThroughToADerivedGroup_CatchesAStatedGroupGoingMissing(t *testing.T) {
	s := newRouteStubServer(t)
	payload := liveDocsPayload(t, s)
	stated := statedDocGroups(s)

	baseline, examined := derivedGroupFindings(payload, stated, derivedGroupAllowList)
	if examined == 0 {
		t.Fatal("examined 0 endpoints over the real payload")
	}
	if len(baseline) != 0 {
		t.Fatalf("the real payload already has %d finding(s) (%v); this test measures the delta from a "+
			"clean baseline, so fix those first", len(baseline), baseline)
	}

	const key = "PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id"
	if _, ok := stated[key]; !ok {
		t.Fatalf("%s does not state a docs section, so removing it proves nothing", key)
	}

	// The regression moves BOTH sides at once, so the mutation has to as
	// well. The endpointMeta entry is the only thing standing between this
	// route and groupFromPath: deleting it removes the stated section AND
	// changes what GetDocs renders. Moving only the first would hand the
	// detector a payload still reading "Virtual Machines" and prove nothing
	// about what an operator would see. "Clusters" is what the second segment
	// of /api/v1/clusters/… derives to, and it is what deleting the real
	// entry and re-rendering the real payload produces.
	const derived = "Clusters"
	delete(stated, key)
	mutated := slices.Clone(payload)
	var mutatedOne bool
	for i := range mutated {
		if mutated[i].Method+" "+mutated[i].Path == key {
			mutated[i].Group = derived
			mutatedOne = true
		}
	}
	if !mutatedOne {
		t.Fatalf("%s is not in the docs payload; the mutation changed nothing", key)
	}

	findings, examined := derivedGroupFindings(mutated, stated, derivedGroupAllowList)
	if examined != len(payload) {
		t.Fatalf("examined %d endpoints, want %d", examined, len(payload))
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %v", len(findings), findings)
	}
	// Actionable on its own: the route, and the section it would silently
	// render in. Without the second the reader has to go and derive it.
	if !strings.Contains(findings[0], key) {
		t.Errorf("the finding does not name the route: %s", findings[0])
	}
	if !strings.Contains(findings[0], `"Clusters"`) {
		t.Errorf("the finding does not name the section the route would fall through to: %s", findings[0])
	}
}

// TestDerivedGroupFindings_PinsTheDerivedValue proves the reviewed list is a
// claim about a VALUE rather than a blanket pass for the key.
//
// This is the half that makes listing /settings/branding honest: the entry
// says the derived section is "Settings", and if it stops being "Settings"
// the exemption stops applying. A list that only carried keys would keep
// excusing the route after the thing it excused had changed.
func TestDerivedGroupFindings_PinsTheDerivedValue(t *testing.T) {
	payload := []handlers.APIEndpoint{
		{Method: "GET", Path: "/api/v1/settings/branding", Group: "Branding"},
	}
	allowed := map[string]string{"GET /api/v1/settings/branding": "Settings"}

	findings, examined := derivedGroupFindings(payload, map[string]string{"GET /api/v1/other": "Settings"}, allowed)
	if examined != 1 {
		t.Fatalf("examined %d, want 1", examined)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings, want exactly 1: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0], `"Branding"`) || !strings.Contains(findings[0], `"Settings"`) {
		t.Errorf("the finding does not name both the derived and the recorded section: %s", findings[0])
	}
}

// TestDerivedGroupFindings_ReportsAStaleExemption covers the reverse
// direction, which nothing else would notice: a listed route that no longer
// derives its section, either because the route is gone or because somebody
// stated one.
func TestDerivedGroupFindings_ReportsAStaleExemption(t *testing.T) {
	allowed := map[string]string{
		"GET /api/v1/settings/branding": "Settings",
		"GET /api/v1/vanished-thing":    "Vanished Thing",
	}
	payload := []handlers.APIEndpoint{
		{Method: "GET", Path: "/api/v1/settings/branding", Group: "Settings"},
	}
	stated := map[string]string{"GET /api/v1/settings/branding": "Settings"}

	findings, examined := derivedGroupFindings(payload, stated, allowed)
	if examined != 1 {
		t.Fatalf("examined %d, want 1", examined)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want exactly 2: %v", len(findings), findings)
	}
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "is now STATED") {
		t.Errorf("no finding reports the exemption that became redundant: %s", joined)
	}
	if !strings.Contains(joined, "no route in the docs payload has that key") {
		t.Errorf("no finding reports the exemption whose route is gone: %s", joined)
	}
}

// TestGuard_DerivedGroupAntiVacuityTriggerIsReachable pins the input that
// makes the production test's t.Fatal fire, so the assertion is known to be
// reachable rather than reading like a safeguard while being dead.
func TestGuard_DerivedGroupAntiVacuityTriggerIsReachable(t *testing.T) {
	findings, examined := derivedGroupFindings(nil, statedDocGroups(newRouteStubServer(t)), derivedGroupAllowList)
	if examined != 0 {
		t.Fatalf("examined = %d over an empty payload, want 0; the production test's floor can never fire", examined)
	}
	// Every reviewed entry looks stale over an empty payload, which is what
	// makes the empty case indistinguishable from a real failure without the
	// examined count — and therefore why the production test checks the count
	// rather than trusting len(findings).
	if len(findings) != len(derivedGroupAllowList) {
		t.Fatalf("got %d findings over an empty payload, want %d", len(findings), len(derivedGroupAllowList))
	}
}
