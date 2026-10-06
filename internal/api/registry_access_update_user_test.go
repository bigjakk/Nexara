package api

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The REAL PUT .../access/users/:userid declaration, permission gate and
// AccessHandler.UpdateUser over the stand-ins of registry_access_kit_test.go. The
// handler's unit tests pass with the audit builder deleted and cannot see where
// `force` comes from; here a value is followed from the request to the form Proxmox
// receives and the audit row every Viewer can read.

// manageAccess is a caller holding the permission the access write routes declare.
var manageAccess = grantsOf("manage:" + handlers.AccessResource)

// pveUserPath is where the stand-in Proxmox sees a user.
func pveUserPath(userID string) string { return "/api2/json/access/users/" + userID }

func newAccessUpdateApp(t *testing.T) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	return newAccessApp(t, manageAccess, func(h *handlers.AccessHandler) []realRoute {
		return []realRoute{{fiber.MethodPut, accessScope + "/users/:userid", h.UpdateUser}}
	})
}

// accessUpdateRequest is the PUT a client sends to edit user, given as the path
// segment a client sends: percent-encoded.
func accessUpdateRequest(user, query, body string) *http.Request {
	target := strings.Replace(accessRoute(accessScope+"/users/:userid"), testAccessUserID, user, 1) + query
	req := jsonRequest(http.MethodPut, target, body)
	req.Header.Set("X-Test-User", "yes")
	return req
}

// The fields an audit row must never carry, each recognisable in the row whatever
// key it might be recorded under. The group list is here too: the row says a
// membership was set, not what it became.
const accessUpdateEverything = `{
	"enable": false, "expire": 1767225600, "groups": "sentinel-group-list", "append": true,
	"comment": "sentinel-comment", "email": "sentinel-email@example.com",
	"firstname": "sentinel-first", "lastname": "sentinel-last", "keys": "sentinel-keys"
}`

var accessUpdateEverythingForm = url.Values{
	"enable":    {"0"},
	"expire":    {"1767225600"},
	"groups":    {"sentinel-group-list"},
	"append":    {"1"},
	"comment":   {"sentinel-comment"},
	"email":     {"sentinel-email@example.com"},
	"firstname": {"sentinel-first"},
	"lastname":  {"sentinel-last"},
	"keys":      {"sentinel-keys"},
}

// TestAccessUpdateUserAuditRow follows a user edit to the form Proxmox receives and
// the audit row. The row carries the enable, expire and groups an edit set and
// whether it was sent with force, and no e-mail, comment, name or two-factor keys:
// the first case sends every field so that their absence from the row is an
// absence. The guard is live (the second case is the same edit refused for want of
// force), and the last has Proxmox refuse the edit, so a row written ahead of the
// Proxmox call would show.
func TestAccessUpdateUserAuditRow(t *testing.T) {
	const alice = "alice@pve"
	tests := []struct {
		name              string
		user, query, body string // the user as the client sends it in the path
		pveStatus         int    // Proxmox refuses the edit with this status and pveBody
		pveBody           string
		want              auditWant
	}{
		{
			name: "a forced edit of the account Nexara uses, with every field",
			user: testAccessUserID, query: "?force=true", body: accessUpdateEverything,
			want: auditWant{
				status: fiber.StatusOK, method: http.MethodPut, path: pveUserPath(accessUpdateSelf), form: accessUpdateEverythingForm,
				audit: auditRow("pve_user", accessUpdateSelf, "updated", `{"enable":false,"expire":1767225600,"forced":true,"groups_changed":true,"userid":"nexara@pve"}`),
			},
		},
		{
			name: "the same edit without force is refused and leaves no trace",
			user: testAccessUserID, body: accessUpdateEverything,
			want: auditWant{status: fiber.StatusConflict, message: []string{"force=true"}},
		},
		{
			name: "force on an edit the guard does not cover overrides nothing",
			user: testAccessUserID, query: "?force=true", body: `{"comment":"sentinel-comment"}`,
			want: auditWant{
				status: fiber.StatusOK, method: http.MethodPut, path: pveUserPath(accessUpdateSelf), form: url.Values{"comment": {"sentinel-comment"}},
				audit: auditRow("pve_user", accessUpdateSelf, "updated", `{"forced":false,"userid":"nexara@pve"}`),
			},
		},
		{
			name: "clearing the expiry needs no force and is recorded as 0",
			user: testAccessUserID, body: `{"expire":0}`,
			want: auditWant{
				status: fiber.StatusOK, method: http.MethodPut, path: pveUserPath(accessUpdateSelf), form: url.Values{"expire": {"0"}},
				audit: auditRow("pve_user", accessUpdateSelf, "updated", `{"expire":0,"forced":false,"userid":"nexara@pve"}`),
			},
		},
		{
			name: "another account is edited without force and the row names it",
			user: "alice%40pve", body: `{"enable":false}`,
			want: auditWant{
				status: fiber.StatusOK, method: http.MethodPut, path: pveUserPath(alice), form: url.Values{"enable": {"0"}},
				audit: auditRow("pve_user", alice, "updated", `{"enable":false,"forced":false,"userid":"alice@pve"}`),
			},
		},
		{
			// force is recorded as sent, not as a verdict. Here force is true and append
			// is not, so a handler that hands the builder the wrong flag cannot pass.
			name: "force on a guarded edit of another account is recorded whether or not the guard would have refused it",
			user: "alice%40pve", query: "?force=true", body: `{"enable":false}`,
			want: auditWant{
				status: fiber.StatusOK, method: http.MethodPut, path: pveUserPath(alice), form: url.Values{"enable": {"0"}},
				audit: auditRow("pve_user", alice, "updated", `{"enable":false,"forced":true,"userid":"alice@pve"}`),
			},
		},
		{
			name: "an edit Proxmox refuses answers with an error and writes no audit row",
			user: "alice%40pve", body: `{"comment":"sentinel-comment"}`,
			pveStatus: http.StatusInternalServerError, pveBody: `{"data":null,"message":"no such user ('alice@pve')\n"}`,
			want: auditWant{method: http.MethodPut, path: pveUserPath(alice), form: url.Values{"comment": {"sentinel-comment"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newAccessUpdateApp(t)
			if tt.pveStatus != 0 {
				pve.refuse(tt.want.path, tt.pveStatus, tt.pveBody)
			}
			requireAudited(t, app, pve, store, accessUpdateRequest(tt.user, tt.query, tt.body), tt.want)
		})
	}
}
