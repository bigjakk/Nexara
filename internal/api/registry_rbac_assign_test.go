package api

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// A cluster-scoped assignment has to name a cluster that exists: it is deleted with
// that cluster, and one on a cluster that is not there could never be. A cluster
// removed between the lookup and the insert is refused by the key (000123), and
// answered the same; a violation of any other key is not the cluster's.
func TestAssignUserRole_ClusterScopeNamesAnExistingCluster(t *testing.T) {
	t.Parallel()
	// The refusal as a client reads it, pinned here rather than shared with the handler.
	const missing = "scope_id must name an existing cluster"
	violation := func(constraint string) error { return &pgconn.PgError{Code: "23503", ConstraintName: constraint} }
	cluster, assigned := db.Cluster{}, db.UserRole{}
	tests := []struct {
		name        string
		scopeType   string
		lookups     map[string]any
		wantStatus  int
		wantMessage string
		wantAsked   []string
	}{
		{"a cluster that exists", "cluster",
			map[string]any{"GetCluster": cluster, "AssignUserRole": assigned}, http.StatusCreated, "", []string{"GetCluster", "AssignUserRole"}},
		{"a cluster that does not exist", "cluster",
			map[string]any{"AssignUserRole": assigned}, http.StatusBadRequest, missing, []string{"GetCluster"}},
		{"a cluster removed before the insert", "cluster",
			map[string]any{"GetCluster": cluster, "AssignUserRole": violation("user_roles_scope_id_fkey")},
			http.StatusBadRequest, missing, []string{"GetCluster", "AssignUserRole"}},
		{"a role that does not exist is not the cluster's mistake", "cluster",
			map[string]any{"GetCluster": cluster, "AssignUserRole": violation("user_roles_role_id_fkey")},
			http.StatusInternalServerError, "", []string{"GetCluster", "AssignUserRole"}},
		{"a lookup that fails", "cluster",
			map[string]any{"GetCluster": errors.New("the database is down")}, http.StatusInternalServerError, "", []string{"GetCluster"}},
		{"an instance-wide assignment asks about no cluster", "global",
			map[string]any{"AssignUserRole": assigned}, http.StatusCreated, "", []string{"AssignUserRole"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dbtx := &backupRunDBTX{lookups: tt.lookups}
			e := declaredEndpoint(t, fiber.MethodPost, rbacScope+"/users/:user_id/roles")
			e.Handler = handlers.NewRBACHandler(db.New(dbtx), auth.NewRBACEngine(nil, nil), nil).AssignUserRole
			app := newRegistryApp(t, stubAuth(grantsOf("manage:role")), e)

			body := `{"role_id":"` + testRoleID + `","scope_type":"` + tt.scopeType + `","scope_id":"` + testClusterID + `"}`
			if tt.scopeType == "global" {
				body = `{"role_id":"` + testRoleID + `","scope_type":"global"}`
			}
			req := jsonRequest(http.MethodPost, rbacScope+"/users/"+testRBACUserID+"/roles", body)
			req.Header.Set("X-Test-User", "yes")
			status, env := send(t, app, req)

			if status != tt.wantStatus || !strings.Contains(env.Message, tt.wantMessage) {
				t.Errorf("answered %d %q, want %d containing %q", status, env.Message, tt.wantStatus, tt.wantMessage)
			}
			if !slices.Equal(dbtx.asked, tt.wantAsked) {
				t.Errorf("the handler ran %v, want %v", dbtx.asked, tt.wantAsked)
			}
		})
	}
}
