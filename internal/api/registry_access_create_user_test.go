package api

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The REAL POST .../access/users declaration, permission gate and
// AccessHandler.CreateUser over the stand-ins of registry_access_kit_test.go. The
// handler's unit tests pass with the audit builder deleted from it, or with a
// CreateUser that hands it the wrong account or fields.

func newAccessCreateApp(t *testing.T) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	return newAccessApp(t, manageAccess, func(h *handlers.AccessHandler) []realRoute {
		return []realRoute{{fiber.MethodPost, accessScope + "/users", h.CreateUser}}
	})
}

// accessCreatePassword is the password the requests below set: findable in a row
// whatever key it is under, and not a real one.
const accessCreatePassword = "PROBE-CREATE-PASSWORD-NOT-A-REAL-VALUE"

// accessCreateNeverRecorded are the values of the fields an audit row must never
// carry. The group list is here too: the row says a membership was set, not what it is.
var accessCreateNeverRecorded = []string{
	accessCreatePassword, "sentinel-comment", "sentinel-email@example.com",
	"sentinel-first", "sentinel-last", "sentinel-keys", "sentinel-group-list",
}

// TestAccessCreateUserAuditRow follows a user create to the form Proxmox receives
// and the audit row. The row carries the account, whether it has a password, and
// the enable, expire and groups the request set, and no password, e-mail, comment,
// name, two-factor keys or group list: each case asserts that what it sent reached
// Proxmox, so that a field's absence from the row is an absence. The edges of the
// allow-list: a password and groups sent empty (no password, still groups), and
// enable true with an expiry of 0, which a builder that recorded only a disable or
// a non-zero expiry would drop. The last has Proxmox refuse the create.
func TestAccessCreateUserAuditRow(t *testing.T) {
	const usersPath = "/api2/json/access/users"
	created := func(form url.Values, id, details string) auditWant {
		return auditWant{
			status: fiber.StatusCreated, method: http.MethodPost, path: usersPath, form: form, never: accessCreateNeverRecorded,
			audit: auditRow("pve_user", id, "created", details),
		}
	}
	everything := `"userid": "alice@pve", "password": "` + accessCreatePassword + `",
		"comment": "sentinel-comment", "email": "sentinel-email@example.com",
		"firstname": "sentinel-first", "lastname": "sentinel-last", "keys": "sentinel-keys",
		"groups": "sentinel-group-list"`
	everythingForm := url.Values{
		"userid": {"alice@pve"}, "password": {accessCreatePassword}, "comment": {"sentinel-comment"},
		"email": {"sentinel-email@example.com"}, "firstname": {"sentinel-first"}, "lastname": {"sentinel-last"},
		"keys": {"sentinel-keys"}, "groups": {"sentinel-group-list"},
	}
	withEnable := url.Values{"enable": {"0"}, "expire": {"1767225600"}}
	for k, v := range everythingForm {
		withEnable[k] = v
	}

	tests := []struct {
		name      string
		body      string
		pveStatus int // Proxmox refuses the create with this status and pveBody
		pveBody   string
		want      auditWant
	}{
		{name: "every field but enable and expire", body: `{` + everything + `}`,
			want: created(everythingForm, "alice@pve", `{"groups_set":true,"has_password":true,"userid":"alice@pve"}`)},
		{name: "every field, disabled and with an expiry", body: `{` + everything + `, "enable": false, "expire": 1767225600}`,
			want: created(withEnable, "alice@pve", `{"enable":false,"expire":1767225600,"groups_set":true,"has_password":true,"userid":"alice@pve"}`)},
		{name: "as the SPA sends it: the account, a password and a comment",
			body: `{"userid": "nexara@pve", "password": "` + accessCreatePassword + `", "comment": "sentinel-comment"}`,
			want: created(url.Values{"userid": {"nexara@pve"}, "password": {accessCreatePassword}, "comment": {"sentinel-comment"}},
				"nexara@pve", `{"has_password":true,"userid":"nexara@pve"}`)},
		{name: "the account alone has no password", body: `{"userid": "alice@pve"}`,
			want: created(url.Values{"userid": {"alice@pve"}}, "alice@pve", `{"has_password":false,"userid":"alice@pve"}`)},
		{
			// proxmox.CreateAccessUser sends a password only when there is one, and
			// groups whenever the request carried them.
			name: "an empty password is no password, and empty groups are still groups set",
			body: `{"userid": "alice@pve", "password": "", "groups": ""}`,
			want: created(url.Values{"userid": {"alice@pve"}, "groups": {""}}, "alice@pve", `{"groups_set":true,"has_password":false,"userid":"alice@pve"}`),
		},
		{name: "enable true and an expiry of 0 are recorded as sent", body: `{"userid": "alice@pve", "enable": true, "expire": 0}`,
			want: created(url.Values{"userid": {"alice@pve"}, "enable": {"1"}, "expire": {"0"}}, "alice@pve",
				`{"enable":true,"expire":0,"has_password":false,"userid":"alice@pve"}`)},
		{
			name:      "a create Proxmox refuses answers with an error and writes no audit row",
			body:      `{"userid": "alice@pve", "password": "` + accessCreatePassword + `", "comment": "sentinel-comment"}`,
			pveStatus: http.StatusInternalServerError, pveBody: `{"data":null,"message":"user 'alice@pve' already exists\n"}`,
			want: auditWant{method: http.MethodPost, path: usersPath,
				form: url.Values{"userid": {"alice@pve"}, "password": {accessCreatePassword}, "comment": {"sentinel-comment"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newAccessCreateApp(t)
			if tt.pveStatus != 0 {
				pve.refuse(usersPath, tt.pveStatus, tt.pveBody)
			}
			req := jsonRequest(http.MethodPost, accessRoute(accessScope+"/users"), tt.body)
			req.Header.Set("X-Test-User", "yes")
			requireAudited(t, app, pve, store, req, tt.want)
		})
	}
}
