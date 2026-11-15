package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestDeleteRole_ADirectoryDefaultRoleIsA409NamingTheConfig: a role an LDAP or OIDC
// config hands out by default is refused 409 with the configs named, not the 500 of the
// foreign key; and so is one a config took up between the check and the delete.
func TestDeleteRole_ADirectoryDefaultRoleIsA409NamingTheConfig(t *testing.T) {
	t.Parallel()
	for _, raced := range []bool{false, true} {
		role := db.Role{ID: uuid.New(), Name: "Ops"}
		store := newEpochStore()
		store.roles = map[uuid.UUID]db.Role{role.ID: role}
		used := []db.ListDirectoryConfigsUsingRoleRow{{Kind: "ldap", Name: "Directory"}}
		if raced {
			store.failOn["DeleteRole"] = &pgconn.PgError{Code: "23503"}
			store.after["ListDirectoryConfigsUsingRole"] = func(s *epochStore) { s.roleUsers = map[uuid.UUID][]db.ListDirectoryConfigsUsingRoleRow{role.ID: used} }
		} else {
			store.roleUsers = map[uuid.UUID][]db.ListDirectoryConfigsUsingRoleRow{role.ID: used}
		}
		a := newEpochApp(t, store, epochOptions{})
		h := &RBACHandler{queries: a.users.queries}
		params, err := apischema.Properties{"id": {Type: apischema.String}}.Validate(map[string]any{"id": role.ID.String()})
		if err != nil {
			t.Fatalf("params: %v", err)
		}
		a.probe = func(c fiber.Ctx) error { return h.DeleteRole(c, params) }
		resp, err := a.app.Test(httptest.NewRequest(http.MethodGet, "/probe", nil), fiber.TestConfig{Timeout: 10 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		body := decodeObject(t, resp)
		details, _ := body["details"].(map[string]any)
		if resp.StatusCode != http.StatusConflict || body["error"] != "role_in_use" || !strings.Contains(fmt.Sprint(details["used_by"]), "Directory") {
			t.Errorf("raced %t: %d %v, want 409 role_in_use naming the config", raced, resp.StatusCode, body)
		}
		if _, ok := store.roles[role.ID]; !ok {
			t.Errorf("raced %t: the role was deleted", raced)
		}
	}
}

// TestSyncUserRoles_AMappingToADeletedRoleAssignsTheDefault: a group mapping that names
// a role deleted since assigns nothing, so the user gets the default role, as a user no
// mapping matches does.
func TestSyncUserRoles_AMappingToADeletedRoleAssignsTheDefault(t *testing.T) {
	t.Parallel()
	def := db.Role{ID: uuid.New(), Name: "Viewer"}
	store := newEpochStore()
	store.roles = map[uuid.UUID]db.Role{def.ID: def}
	a := newEpochApp(t, store, epochOptions{})
	h := &LDAPHandler{queries: a.users.queries}
	gone, user := uuid.New(), uuid.New()
	a.probe = func(c fiber.Ctx) error {
		h.syncUserRoles(c, user, []string{"cn=ops"}, map[string]string{"cn=ops": gone.String()}, pgtype.UUID{Bytes: def.ID, Valid: true})
		return c.SendStatus(fiber.StatusNoContent)
	}
	if _, err := a.app.Test(httptest.NewRequest(http.MethodGet, "/probe", nil)); err != nil {
		t.Fatal(err)
	}
	assigns := store.named("AssignUserRole")
	if len(assigns) != 2 || assigns[1].args[1] != def.ID {
		t.Errorf("role assignments %v, want the deleted role's, refused, then the default role", assigns)
	}
}
