package api

import (
	"reflect"
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
// is supposed to carry: the 9 legacy routes (of the 17 in router.go) that
// have a curated entry — see the doc comment on endpointMeta itself
// (internal/api/handlers/api_docs.go) for which 9 and why.
//
// This exists because TestEndpointMetaMatchesRegisteredRoutes only checks
// the direction "every key that exists points at a real route" — it has
// no floor on HOW MANY keys should exist, so it passes just as cleanly
// over 9 keys, 3 keys, or zero. That gap is not hypothetical: it is what
// let a real regression through in an earlier draft of the cleanup that
// produced this list — deleting any of these 9 (which GetDocs DOES render
// for these legacy routes, unlike the ~223 removed entries for
// registry-declared ones) silently blanks that route's description and
// permission in the live /api/v1/api-docs payload, and nothing in this
// package's test suite noticed. TestGuard_DocumentedPermissionMatchesEnforcement's
// own anti-vacuity floor (rbac_route_guard_test.go) checks that SOMETHING
// was compared, not that these specific 9 keys survived, so it does not
// substitute for this.
var endpointMetaSurvivingKeys = []string{
	"GET /api/v1/api-docs",
	"GET /api/v1/auth/oidc/callback",
	"GET /api/v1/settings",
	"GET /api/v1/settings/:key",
	"POST /api/v1/alert-rules",
	"POST /api/v1/auth/logout",
	"POST /api/v1/auth/register",
	"PUT /api/v1/alert-rules/:id",
	"PUT /api/v1/settings/:key",
}

// TestGuard_EndpointMetaKeysAreExactlyTheSurvivingSet is the floor
// TestEndpointMetaMatchesRegisteredRoutes does not provide: it fails,
// naming the key, if endpointMeta ever loses one of the 9 surviving
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
