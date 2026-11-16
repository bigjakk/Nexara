package handlers

import (
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestUserGetAndUpdate_AnswerTheGlobalRoles: GET and PUT /users/:id answered roles:
// null. They answer what the list answers: the user's global role names, each once, in
// the database's order; a cluster-scoped grant is not among them.
func TestUserGetAndUpdate_AnswerTheGlobalRoles(t *testing.T) {
	t.Parallel()
	admin := epochUser(t, epochAdminEmail, 0)
	member := epochUser(t, epochLoginEmail, 0)
	store := newEpochStore(admin, member)
	store.userRoles = map[uuid.UUID][]db.ListUserRolesRow{member.ID: {
		// The database's order (ORDER BY r.name), which is not Go's byte order.
		{ScopeType: "cluster", RoleName: "Admin"},
		{ScopeType: "global", RoleName: "admin-x"},
		{ScopeType: "global", RoleName: "Viewer"},
		{ScopeType: "cluster", RoleName: "Viewer"},
	}}
	a := newEpochApp(t, store, epochOptions{})
	as := map[string]string{"X-Test-Acting-User": admin.ID.String()}
	for _, tc := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPut, `{"display_name":"Carol"}`},
	} {
		resp := a.send(t, tc.method, "/users/"+member.ID.String(), tc.body, as, 20*time.Second)
		body := decodeObject(t, resp)
		if want := []any{"admin-x", "Viewer"}; resp.StatusCode != http.StatusOK || !reflect.DeepEqual(body["roles"], want) {
			t.Errorf("%s: %d roles = %v, want %v", tc.method, resp.StatusCode, body["roles"], want)
		}
	}
}
