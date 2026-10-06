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
		// mountRegistry refuses to mount a route naming a node without a
		// lookup. Nothing here serves a request, so one that says yes to
		// every node is as good as any; the tests that are about membership
		// remount the registry with their own.
		nodeLookup: everyNodeIsAMember(),
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

// TestEndpointMetaMatchesRegisteredRoutes asserts every curated endpointMeta key ("METHOD path")
// matches a route actually registered by setupRoutes. GetDocs looks entries up with the registered path
// verbatim, so a key with a stale path or param name (":id" vs ":cluster_id") is dead weight: the endpoint
// renders with blank description/permission and nothing else notices. The reverse direction (every route
// having a curated entry) is intentionally NOT enforced: routes without one get auto-derived metadata by
// design (see the endpointMeta doc comment).
func TestEndpointMetaMatchesRegisteredRoutes(t *testing.T) {
	s := sharedRouteStub(t)

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

// endpointMetaSurvivingKeys is the exact, closed set of keys endpointMeta carries: the 13
// legacy routes (of the 17 in router.go) that have a curated entry (see endpointMeta in
// internal/api/handlers/api_docs.go). TestEndpointMetaMatchesRegisteredRoutes only
// checks that every key points at a real route, so it passes over 13 keys or none; an
// entry deleted without this list changing silently blanks that route's description and
// permission in the live payload. It grew from 9 to 13 when four routes whose Group
// groupFromPath derived WRONG earned one; none can be declared instead.
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

// TestGuard_EndpointMetaKeysAreExactlyTheSurvivingSet fails, naming the key, when
// endpointMeta loses one of the surviving entries or gains one that was not added to
// endpointMetaSurvivingKeys: a new legacy route earning an entry updates the list in the
// same change.
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
// A declared route states its Group; a LEGACY route states one only through an
// endpointMeta entry; otherwise handlers.groupFromPath derives it from the second path
// segment, which fails silently: a nested route lands in its parent's section (the
// storage-content delete and the vm-folders reparent both derived "Clusters") and a
// collection of its own invents a section (the firewall-template writes). Every other
// docs guard checks a value that IS stated; the guard below reports the ABSENCE.
// registry_group_guard_test.go holds what a stated section may say.

// derivedGroupAllowList is the reviewed set of routes whose docs section is derived
// rather than stated, each with the section it derives to. The value is a claim about a
// specific string, true here by coincidence (branding lives under /settings): it catches
// groupFromPath's own algorithm moving under the route, and the keys catch a route that
// moves. To add an entry only when the derived value genuinely is the section the route
// belongs in; otherwise declare the route or give it an endpointMeta entry.
var derivedGroupAllowList = map[string]string{
	"GET /api/v1/settings/branding":              "Settings",
	"GET /api/v1/settings/branding/favicon-file": "Settings",
	"GET /api/v1/settings/branding/logo-file":    "Settings",
}

// TestGuard_DerivedGroupAllowListNamesRealSections holds the value column to the Group
// vocabulary, which the derived-vs-recorded comparison cannot: a value that faithfully
// records the derivation AND names a section nothing else uses. The fix is always a
// declared Group or an endpointMeta entry, never a canonicalGroups line, because
// groupVocabularyFindings also requires every canonical entry to be named by a
// declaration or overlay and never reads a derivation.
func TestGuard_DerivedGroupAllowListNamesRealSections(t *testing.T) {
	// Already route-sorted by the detector; no second sort here, which would
	// imply the order is unspecified when it is pinned.
	findings := derivedSectionVocabularyFindings(derivedGroupAllowList, canonicalGroups)

	if len(derivedGroupAllowList) == 0 {
		t.Fatal("derivedGroupAllowList is empty, so this guard checked nothing; delete it or " +
			"re-point it at whatever replaced the list")
	}
	for _, f := range findings {
		t.Error(f)
	}
}

// derivedSectionVocabularyFindings reports each allow-listed route whose
// recorded section is not in the vocabulary.
func derivedSectionVocabularyFindings(allow, canonical map[string]string) (findings []string) {
	for _, route := range slices.Sorted(maps.Keys(allow)) {
		section := allow[route]
		if _, ok := canonical[section]; ok {
			continue
		}
		findings = append(findings, fmt.Sprintf(
			"%s is allow-listed as deriving section %q, which is not in canonicalGroups. Give the "+
				"route a declared Group, or an endpointMeta entry naming its section — do NOT add "+
				"%q to canonicalGroups, which only moves the failure to groupVocabularyFindings, "+
				"since nothing declares or overlays it.",
			route, section, section))
	}
	return findings
}

// liveDocsPayload renders the REAL /api/v1/api-docs payload — the production handler over
// the declarations and route table setupRoutes built — decoded as a caller receives it.
// Group reaches a reader through three merging steps, and a guard that re-implemented
// the merge would check its own copy of the rule. The handler is mounted on a throwaway
// app because the real route sits behind authRequired; SetApp is New()'s half of the
// wiring, which a stub never goes through.
func liveDocsPayload(t *testing.T, s *Server) []handlers.APIEndpoint {
	t.Helper()
	docs := *s.apiDocsHandler // a copy, so the shared stub is not written to
	docs.SetApp(s.app)

	probe := fiber.New()
	probe.Get("/docs", docs.GetDocs)

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

// statedDocGroups is every route whose docs section somebody DECIDED, keyed as the
// payload keys it: the payload alone cannot tell a stated section from a derived one, the
// two being frequently the same string. The overlay counts only where nothing is
// declared, since GetDocs takes a declaration whole.
func statedDocGroups(s *Server) map[string]string {
	out := make(map[string]string, s.registry.Len())
	for _, e := range docEndpoints(s.registry) {
		// Normalised as the docs lookup does, so ".../foo/" keys as it renders.
		out[e.Method+" "+handlers.NormalizeDocPath(e.Path)] = e.Group
	}
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

// derivedGroupFindings checks both directions and reports how many payload entries it
// looked at. Forward: an entry whose section nothing states and the reviewed list does
// not cover, or covers with a value it no longer derives to. Reverse: a reviewed entry
// whose route is gone or whose section is now stated; that half decays quietly, since a
// leftover exemption reads as review and the next route on that key inherits it.
// stated and allowed are parameters so a synthetic input can prove the comparison fires.
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
	s := sharedRouteStub(t)

	payload := liveDocsPayload(t, s)
	stated := statedDocGroups(s)

	findings, examined := derivedGroupFindings(payload, stated, derivedGroupAllowList)

	// Anti-vacuity, on the input: an empty payload reports zero findings, as a healthy one
	// does. Every declaration must appear in it (TestGuard_DeclaredDocsReachTheDocsHandler),
	// so the registry's size is the floor.
	if examined < s.registry.Len() {
		t.Fatalf("the docs payload carries %d endpoints but the registry declares %d; this guard would be "+
			"checking something other than what /api/v1/api-docs serves", examined, s.registry.Len())
	}
	if len(stated) == 0 {
		t.Fatal("no route states a docs section at all; every route would read as derived and the guard " +
			"would be reporting the wrong thing")
	}
	// An over-broad `stated` (statedDocGroups reading the route table) marks every route
	// decided and the guard compares nothing, with the payload full and `stated`
	// non-empty. Checked by where a stated section may come from, a declaration or an
	// overlay entry, not by requiring a derived route: the allow-list is meant to empty.
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

// TestGuard_NoRouteFallsThroughToADerivedGroup_CatchesAStatedGroupGoingMissing plants
// one fault of each kind derivedGroupFindings reports. The real payload is clean, so it
// cannot tell a working detector from one that stopped comparing: the first row removes a
// real stated section (the vm-folders reparent, whose derived "Clusters" files it with
// strangers) from the real payload and the stated map together, as deleting its
// endpointMeta entry would; the others are synthetic. The last row is the vocabulary
// detector, which no edit to the shipped maps can make fire.
func TestGuard_NoRouteFallsThroughToADerivedGroup_CatchesAStatedGroupGoingMissing(t *testing.T) {
	s := sharedRouteStub(t)
	payload := liveDocsPayload(t, s)
	stated := statedDocGroups(s)

	baseline, examined := derivedGroupFindings(payload, stated, derivedGroupAllowList)
	if examined == 0 || len(baseline) != 0 {
		t.Fatalf("the real payload must be clean and non-empty: examined %d, findings %v", examined, baseline)
	}
	const key = "PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id"
	if _, ok := stated[key]; !ok {
		t.Fatalf("%s does not state a docs section, so removing it proves nothing", key)
	}
	mutated := slices.Clone(payload)
	for i := range mutated {
		if mutated[i].Method+" "+mutated[i].Path == key {
			mutated[i].Group = "Clusters"
		}
	}
	removed := maps.Clone(stated)
	delete(removed, key)

	branding := handlers.APIEndpoint{Method: "GET", Path: "/api/v1/settings/branding", Group: "Settings"}
	for _, tt := range []struct {
		name           string
		payload        []handlers.APIEndpoint
		stated         map[string]string
		allowed        map[string]string
		wantExamined   int
		wantFindings   int
		wantInFindings []string
	}{
		{"a stated section removed from the real payload", mutated, removed, derivedGroupAllowList, len(payload), 1,
			[]string{key, `"Clusters"`, "DERIVED from its path"}},
		{"a derived value that moved under its exemption",
			[]handlers.APIEndpoint{{Method: "GET", Path: "/api/v1/settings/branding", Group: "Branding"}},
			map[string]string{}, map[string]string{"GET /api/v1/settings/branding": "Settings"}, 1, 1,
			[]string{`"Branding"`, `"Settings"`, "now derives docs section"}},
		{"exemptions gone stale: now stated, and a vanished route",
			[]handlers.APIEndpoint{branding}, map[string]string{"GET /api/v1/settings/branding": "Settings"},
			map[string]string{"GET /api/v1/settings/branding": "Settings", "GET /api/v1/vanished-thing": "Vanished Thing"},
			1, 2, []string{"is now STATED", "no route in the docs payload has that key"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			findings, got := derivedGroupFindings(tt.payload, tt.stated, tt.allowed)
			if got != tt.wantExamined || len(findings) != tt.wantFindings {
				t.Fatalf("examined %d with %d findings, want %d and %d: %v", got, len(findings),
					tt.wantExamined, tt.wantFindings, findings)
			}
			joined := strings.Join(findings, "\n")
			for _, want := range tt.wantInFindings {
				if !strings.Contains(joined, want) {
					t.Errorf("no finding carries %q: %s", want, joined)
				}
			}
		})
	}

	canonical := map[string]string{"Settings": "real section"}
	if got := derivedSectionVocabularyFindings(map[string]string{"GET /x": "Settings"}, canonical); len(got) != 0 {
		t.Errorf("a canonical section produced findings %v, want none", got)
	}
	got := derivedSectionVocabularyFindings(map[string]string{"GET /x": "Branding"}, canonical)
	if len(got) != 1 || !strings.Contains(got[0], "Branding") || !strings.Contains(got[0], "GET /x") {
		t.Errorf("a non-canonical section produced %v, want one finding naming the route and the section", got)
	}
}
