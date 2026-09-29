package api

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The unit tests of accessUserCreateDetails (internal/api/handlers/access_test.go)
// show what the builder makes of a set of fields. They pass with the builder
// deleted from the handler, and with a CreateUser that hands it the wrong account
// or the wrong fields. This file keeps the REAL POST .../access/users
// declaration, its permission gate and the real AccessHandler.CreateUser, on the
// stand-in Proxmox and database that registry_access_update_user_test.go builds,
// so a value can be followed from the request to the form Proxmox receives and
// the audit row every Viewer can read.

// newAccessCreateApp mounts the real POST .../access/users declaration, with its
// real permission, carrying the real AccessHandler.CreateUser wired to the two
// stand-ins in place of the one newRouteStubServer bound to an empty
// AccessHandler.
func newAccessCreateApp(t *testing.T) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
	t.Helper()
	pve, store, h := newAccessStandIns(t)

	e := declaredEndpoint(t, fiber.MethodPost, accessScope+"/users")
	e.Handler = h.CreateUser

	// stubAuth sets the user AuditLog needs — without one it writes nothing —
	// and grants the declared manage:access.
	return newRegistryApp(t, stubAuth(map[string]bool{"manage:" + handlers.AccessResource: true}), e), pve, store
}

// accessCreateRequest is the POST a client sends to create a user.
func accessCreateRequest(body string) *http.Request {
	req := jsonRequest(http.MethodPost, accessRoute(accessScope+"/users"), body)
	req.Header.Set("X-Test-User", "yes")
	return req
}

// accessCreatePassword is the password the requests below set: findable in a row
// whatever key it is under, and obviously not a real one.
const accessCreatePassword = "PROBE-CREATE-PASSWORD-NOT-A-REAL-VALUE"

// accessCreateNeverRecorded are the values of the fields an audit row must never
// carry, each recognisable in the row whatever key it might be recorded under.
// The group list is here too: the row says a membership was set, not what it is.
var accessCreateNeverRecorded = []string{
	accessCreatePassword, "sentinel-comment", "sentinel-email@example.com",
	"sentinel-first", "sentinel-last", "sentinel-keys", "sentinel-group-list",
}

// TestAccessCreateUserAuditRow follows a user create from the request, through
// the declaration and the handler, to the form Proxmox receives and the audit row.
//
// The row is what every Viewer reads (view:audit), so this is where the promise
// that it carries the account, whether it has a password, and the enable, expire
// and groups the request set, and no password, e-mail, comment, name, two-factor
// keys or group list, is kept against the code that writes it. Each case asserts
// that what it sent reached Proxmox, so that a field's absence from the row is an
// absence and not a request that never carried it.
//
// The first case sets every field but enable and expire, the second adds those
// two, and the third is what the SPA sends: the account, a password and a
// comment. The account alone has no password. The next two are edges of the
// allow-list: a password and groups sent empty, which is no password and still
// groups, and enable true with an expiry of 0, which a builder that recorded only
// a disable or a non-zero expiry would drop.
//
// The last case has Proxmox refuse the create: the caller gets an error and
// nothing is audited, because nothing was created. The other cases' stand-in
// always succeeds, so without it a row written ahead of the Proxmox call would
// pass.
func TestAccessCreateUserAuditRow(t *testing.T) {
	tests := []struct {
		name string
		body string

		wantStatus int
		// wantForm is the form the one Proxmox request carried.
		wantForm url.Values
		// wantAudit is the exact details of the one audit row, as stored.
		wantAudit string
		// wantID is the account created: the row's resource id.
		wantID string
		// pveStatus, when set, is the HTTP status the stand-in Proxmox refuses the
		// create with, and pveBody what it says. The create still reaches Proxmox,
		// and wantForm is what it carried. The caller must get an error, but which
		// one is the house mappers' to decide (a 409 would be as right as today's
		// 502 for a duplicate), so wantStatus is unused; and no audit row may be
		// written, so wantAudit is too.
		pveStatus int
		pveBody   string
	}{
		{
			name:       "every field but enable and expire",
			wantStatus: fiber.StatusCreated,
			body: `{
				"userid": "alice@pve", "password": "` + accessCreatePassword + `",
				"comment": "sentinel-comment", "email": "sentinel-email@example.com",
				"firstname": "sentinel-first", "lastname": "sentinel-last", "keys": "sentinel-keys",
				"groups": "sentinel-group-list"
			}`,
			wantForm: url.Values{
				"userid":    {"alice@pve"},
				"password":  {accessCreatePassword},
				"comment":   {"sentinel-comment"},
				"email":     {"sentinel-email@example.com"},
				"firstname": {"sentinel-first"},
				"lastname":  {"sentinel-last"},
				"keys":      {"sentinel-keys"},
				"groups":    {"sentinel-group-list"},
			},
			wantAudit: `{"groups_set":true,"has_password":true,"userid":"alice@pve"}`,
			wantID:    "alice@pve",
		},
		{
			name:       "every field, disabled and with an expiry",
			wantStatus: fiber.StatusCreated,
			body: `{
				"userid": "alice@pve", "password": "` + accessCreatePassword + `",
				"comment": "sentinel-comment", "email": "sentinel-email@example.com",
				"firstname": "sentinel-first", "lastname": "sentinel-last", "keys": "sentinel-keys",
				"groups": "sentinel-group-list", "enable": false, "expire": 1767225600
			}`,
			wantForm: url.Values{
				"userid":    {"alice@pve"},
				"password":  {accessCreatePassword},
				"comment":   {"sentinel-comment"},
				"email":     {"sentinel-email@example.com"},
				"firstname": {"sentinel-first"},
				"lastname":  {"sentinel-last"},
				"keys":      {"sentinel-keys"},
				"groups":    {"sentinel-group-list"},
				"enable":    {"0"},
				"expire":    {"1767225600"},
			},
			wantAudit: `{"enable":false,"expire":1767225600,"groups_set":true,"has_password":true,"userid":"alice@pve"}`,
			wantID:    "alice@pve",
		},
		{
			name:       "as the SPA sends it: the account, a password and a comment",
			wantStatus: fiber.StatusCreated,
			body:       `{"userid": "nexara@pve", "password": "` + accessCreatePassword + `", "comment": "sentinel-comment"}`,
			wantForm: url.Values{
				"userid":   {"nexara@pve"},
				"password": {accessCreatePassword},
				"comment":  {"sentinel-comment"},
			},
			wantAudit: `{"has_password":true,"userid":"nexara@pve"}`,
			wantID:    "nexara@pve",
		},
		{
			name:       "the account alone has no password",
			wantStatus: fiber.StatusCreated,
			body:       `{"userid": "alice@pve"}`,
			wantForm:   url.Values{"userid": {"alice@pve"}},
			wantAudit:  `{"has_password":false,"userid":"alice@pve"}`,
			wantID:     "alice@pve",
		},
		{
			name:       "an empty password is no password, and empty groups are still groups set",
			wantStatus: fiber.StatusCreated,
			body:       `{"userid": "alice@pve", "password": "", "groups": ""}`,
			// proxmox.CreateAccessUser sends a password only when there is one, and
			// groups whenever the request carried them.
			wantForm:  url.Values{"userid": {"alice@pve"}, "groups": {""}},
			wantAudit: `{"groups_set":true,"has_password":false,"userid":"alice@pve"}`,
			wantID:    "alice@pve",
		},
		{
			name:       "enable true and an expiry of 0 are recorded as sent",
			wantStatus: fiber.StatusCreated,
			body:       `{"userid": "alice@pve", "enable": true, "expire": 0}`,
			wantForm:   url.Values{"userid": {"alice@pve"}, "enable": {"1"}, "expire": {"0"}},
			wantAudit:  `{"enable":true,"expire":0,"has_password":false,"userid":"alice@pve"}`,
			wantID:     "alice@pve",
		},
		{
			// Proxmox says no, as it does for an account that exists already, so
			// nothing was created and nothing may be recorded as if it had been.
			name:      "a create Proxmox refuses answers with an error and writes no audit row",
			body:      `{"userid": "alice@pve", "password": "` + accessCreatePassword + `", "comment": "sentinel-comment"}`,
			pveStatus: http.StatusInternalServerError,
			pveBody:   `{"data":null,"message":"user 'alice@pve' already exists\n"}`,
			wantForm: url.Values{
				"userid":   {"alice@pve"},
				"password": {accessCreatePassword},
				"comment":  {"sentinel-comment"},
			},
			wantID: "alice@pve",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app, pve, store := newAccessCreateApp(t)
			if tt.pveStatus != 0 {
				pve.refuse("/api2/json/access/users", tt.pveStatus, tt.pveBody)
			}
			status, env := send(t, app, accessCreateRequest(tt.body))
			if tt.pveStatus != 0 {
				if status < fiber.StatusBadRequest {
					t.Fatalf("Proxmox refused the create and the caller got %d (%q), want an error", status, env.Message)
				}
			} else if status != tt.wantStatus {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.wantStatus)
			}

			// What Proxmox received is established first: it is what makes the
			// row's silence about a field mean something.
			sent := pve.requests()
			if len(sent) != 1 {
				t.Fatalf("the handler made %d Proxmox calls, want exactly 1: %+v", len(sent), sent)
			}
			if sent[0].method != http.MethodPost || sent[0].path != "/api2/json/access/users" {
				t.Fatalf("the handler sent %s %s, want POST /api2/json/access/users", sent[0].method, sent[0].path)
			}
			if !reflect.DeepEqual(sent[0].form, tt.wantForm) {
				t.Errorf("Proxmox received the form %v, want %v", sent[0].form, tt.wantForm)
			}

			if tt.pveStatus != 0 {
				if rows := store.auditRows(); len(rows) != 0 {
					t.Errorf("Proxmox refused the create and the handler wrote %d audit row(s), want none", len(rows))
				}
				return
			}
			row := oneAccessAuditRow(t, store.auditRows())
			if row.resourceType != "pve_user" || row.resourceID != tt.wantID || row.action != "created" {
				t.Errorf("the audit row is (%q, %q, %q), want (pve_user, %q, created)",
					row.resourceType, row.resourceID, row.action, tt.wantID)
			}
			if string(row.details) != tt.wantAudit {
				t.Errorf("the audit details are %s, want %s", row.details, tt.wantAudit)
			}
			// The exact comparison above already excludes these; naming them says
			// why, and keeps the promise from resting on a wantAudit that was
			// written wrong.
			for _, secret := range accessCreateNeverRecorded {
				if strings.Contains(string(row.details), secret) {
					t.Errorf("the audit details %s carry %q — audit rows are readable by every Viewer", row.details, secret)
				}
			}
		})
	}
}
