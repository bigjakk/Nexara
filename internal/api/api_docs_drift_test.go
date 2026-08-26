package api

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// newRouteStubServer builds a Server whose handler fields are all
// non-nil zero values so setupRoutes registers every route the real
// binary can serve. No handler is ever invoked — route registration
// only stores bound method values — so zero-value handlers are safe.
//
// If a new handler field is added to Server, add it here too;
// TestEndpointMetaMatchesRegisteredRoutes fails with an explicit
// message when a handler field is left nil.
func newRouteStubServer() *Server {
	return &Server{
		app:                    fiber.New(),
		authHandler:            &handlers.AuthHandler{},
		clusterHandler:         &handlers.ClusterHandler{},
		pbsHandler:             &handlers.PBSHandler{},
		veeamHandler:           &handlers.VeeamHandler{},
		nodeHandler:            &handlers.NodeHandler{},
		vmHandler:              &handlers.VMHandler{},
		containerHandler:       &handlers.ContainerHandler{},
		storageHandler:         &handlers.StorageHandler{},
		vmImportHandler:        &handlers.VMImportHandler{},
		vmFoldersHandler:       &handlers.VMFoldersHandler{},
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
	s := newRouteStubServer()
	requireAllHandlersStubbed(t, s)
	s.setupRoutes()

	registered := make(map[string]bool)
	for _, r := range s.app.GetRoutes(true) {
		// Mirror GetDocs' trailing-slash normalisation: Group(...) +
		// .Post("/") registers "/api/v1/api-keys/"; curated keys use
		// the trimmed form.
		path := r.Path
		if len(path) > len("/api/v1/") && strings.HasSuffix(path, "/") {
			path = strings.TrimSuffix(path, "/")
		}
		registered[r.Method+" "+path] = true
	}

	for _, key := range handlers.EndpointMetaKeys() {
		if !registered[key] {
			t.Errorf("endpointMeta key %q matches no registered route — update the key in internal/api/handlers/api_docs.go to the exact method+path registered in router.go (param names included)", key)
		}
	}
}
