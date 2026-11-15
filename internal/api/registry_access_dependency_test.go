package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The wiring of the ACL, group and role guards (handlers/access_dependency.go, whose
// rule is tabled in access_dependency_test.go): each route reads what the rule needs
// from the stand-in Proxmox, answers a change that touches Nexara's token (the
// stand-in cluster's nexara@pve!api) with the 409 and sends nothing, lets the same
// change through with force and audits it as forced, and lets a change that cannot
// touch the token through unasked.

const (
	depUserPVE  = "GET /api2/json/access/users/nexara@pve"
	depGroupPVE = "GET /api2/json/access/groups/" + testAccessGroupID
	depRolePVE  = "GET /api2/json/access/roles/" + testAccessRoleID
	depACLPVE   = "GET /api2/json/access/acl"
)

// depACL is an ACL listing granting the route's role to ugid.
func depACL(typ, ugid, role string) string {
	return `{"data":[{"path":"/","type":"` + typ + `","ugid":"` + ugid + `","roleid":"` + role + `","propagate":1}]}`
}

func TestAccessDependencyGuardRoutes(t *testing.T) {
	t.Parallel()
	nobodysGroups := `{"data":{"groups":[]}}`
	for _, tc := range []struct {
		name         string
		method, path string
		body         string
		handler      func(*handlers.AccessHandler) Handler
		touches      map[string]string // stand-in answers under which the change touches the token
		untouched    map[string]string // and under which it cannot
		wantIn       string
		wantPVE      string // the change itself, "METHOD path"
		wantForced   string // the forced row's details
	}{
		{
			name: "an ACL grant to a group the user is in", method: http.MethodPut, path: accessScope + "/acl",
			body:      `{"path":"/","roles":"NoAccess","groups":"` + testAccessGroupID + `"}`,
			handler:   func(h *handlers.AccessHandler) Handler { return h.UpdateACL },
			touches:   map[string]string{depUserPVE: `{"data":{"groups":["` + testAccessGroupID + `"]}}`},
			untouched: map[string]string{depUserPVE: `{"data":{"groups":["admins"]}}`},
			wantIn:    "group " + testAccessGroupID, wantPVE: "PUT /api2/json/access/acl",
			wantForced: `{"forced":true,"groups":"` + testAccessGroupID + `","path":"/","roles":"NoAccess","tokens":"","users":""}`,
		},
		{
			name: "a group delete", method: http.MethodDelete, path: accessScope + "/groups/:groupid",
			handler:   func(h *handlers.AccessHandler) Handler { return h.DeleteGroup },
			touches:   map[string]string{depGroupPVE: `{"data":{"members":["alice@pve","nexara@pve"]}}`},
			untouched: map[string]string{depGroupPVE: `{"data":{"members":["alice@pve"]}}`},
			wantIn:    "nexara@pve is in group", wantPVE: "DELETE /api2/json/access/groups/" + testAccessGroupID,
			wantForced: `{"forced":true,"groupid":"` + testAccessGroupID + `"}`,
		},
		{
			name: "a role edit that drops a privilege", method: http.MethodPut, path: accessScope + "/roles/:roleid",
			body:    `{"privs":"VM.Audit"}`,
			handler: func(h *handlers.AccessHandler) Handler { return h.UpdateRole },
			touches: map[string]string{
				depRolePVE: `{"data":{"VM.Audit":1,"Sys.Audit":1}}`, depACLPVE: depACL("user", "nexara@pve", testAccessRoleID), depUserPVE: nobodysGroups,
			},
			// The same role granted to the same token, but the edit keeps every privilege.
			untouched: map[string]string{
				depRolePVE: `{"data":{"VM.Audit":1}}`, depACLPVE: depACL("user", "nexara@pve", testAccessRoleID), depUserPVE: nobodysGroups,
			},
			wantIn: "role " + testAccessRoleID + " is granted to the token's user", wantPVE: "PUT /api2/json/access/roles/" + testAccessRoleID,
			wantForced: `{"append":false,"forced":true,"privs":"VM.Audit","roleid":"` + testAccessRoleID + `"}`,
		},
		{
			name: "a role delete", method: http.MethodDelete, path: accessScope + "/roles/:roleid",
			handler:   func(h *handlers.AccessHandler) Handler { return h.DeleteRole },
			touches:   map[string]string{depACLPVE: depACL("token", "nexara@pve!api", testAccessRoleID), depUserPVE: nobodysGroups},
			untouched: map[string]string{depACLPVE: depACL("token", "nexara@pve!api", "Other"), depUserPVE: nobodysGroups},
			wantIn:    "granted to that token", wantPVE: "DELETE /api2/json/access/roles/" + testAccessRoleID,
			wantForced: `{"forced":true,"roleid":"` + testAccessRoleID + `"}`,
		},
	} {
		route := accessGuardedRoute{method: tc.method, path: tc.path, body: tc.body, handler: tc.handler}
		mount := func(t *testing.T, replies map[string]string) (*fiber.App, *accessUpdatePVE, *accessUpdateDB) {
			app, pve, store := newAccessGuardedApp(t, route)
			for k, v := range replies {
				pve.reply(k, v)
			}
			return app, pve, store
		}
		// changes are the requests that reached Proxmox other than the guard's reads.
		changes := func(pve *accessUpdatePVE) []string {
			var out []string
			for _, l := range pve.lines() {
				if !strings.HasPrefix(l, "GET ") {
					out = append(out, l)
				}
			}
			return out
		}

		t.Run(tc.name+", touching the token", func(t *testing.T) {
			t.Parallel()
			app, pve, store := mount(t, tc.touches)
			status, env := send(t, app, route.request(""))
			if status != fiber.StatusConflict || !strings.Contains(env.Message, tc.wantIn) || !strings.Contains(env.Message, "force=true") {
				t.Fatalf("status = %d (%q), want a 409 carrying %q", status, env.Message, tc.wantIn)
			}
			// The SPA offers its override for this slug alone.
			if env.Error != handlers.CredentialGuardCode {
				t.Errorf("the refusal's slug is %q, want %q", env.Error, handlers.CredentialGuardCode)
			}
			if sent := changes(pve); len(sent) != 0 || len(store.auditRows()) != 0 {
				t.Errorf("the refused change reached Proxmox (%v) or was audited (%d rows)", sent, len(store.auditRows()))
			}
		})
		t.Run(tc.name+", forced", func(t *testing.T) {
			t.Parallel()
			app, pve, store := mount(t, tc.touches)
			method, path, _ := strings.Cut(tc.wantPVE, " ")
			status, env := send(t, app, route.request("?force=true"))
			if status >= fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want the forced change to go through", status, env.Message)
			}
			if got := pve.lines(); len(got) != 1 || got[0] != method+" "+path {
				t.Errorf("Proxmox received %v, want the change alone: force reads nothing", got)
			}
			if row := oneAccessAuditRow(t, store.auditRows()); row.details != tc.wantForced {
				t.Errorf("details = %s, want %s", row.details, tc.wantForced)
			}
		})
		t.Run(tc.name+", not touching the token", func(t *testing.T) {
			t.Parallel()
			app, pve, store := mount(t, tc.untouched)
			status, env := send(t, app, route.request(""))
			if status >= fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want the change to go through unasked", status, env.Message)
			}
			if sent := changes(pve); len(sent) != 1 || sent[0] != tc.wantPVE {
				t.Errorf("Proxmox received the changes %v, want %s", sent, tc.wantPVE)
			}
			if row := oneAccessAuditRow(t, store.auditRows()); !strings.Contains(row.details, `"forced":false`) {
				t.Errorf("details = %s, want forced false", row.details)
			}
		})
	}
}

// TestAccessDependencyGuardAsksWhenItCannotLook: a read the guard needs that Proxmox
// refuses is not waved through, and not a dead end either: the 409 the operator can
// confirm. Here the token may not read the ACL a role delete depends on.
func TestAccessDependencyGuardAsksWhenItCannotLook(t *testing.T) {
	t.Parallel()
	route := accessGuardedRoute{method: http.MethodDelete, path: accessScope + "/roles/:roleid",
		handler: func(h *handlers.AccessHandler) Handler { return h.DeleteRole }}
	app, pve, store := newAccessGuardedApp(t, route)
	pve.refuse(depACLPVE, http.StatusForbidden, `{"data":null,"message":"Permission check failed\n"}`)

	status, env := send(t, app, route.request(""))
	if status != fiber.StatusConflict || !strings.Contains(env.Message, "could not check") {
		t.Fatalf("status = %d (%q), want a 409 saying the check could not be made", status, env.Message)
	}
	if got := pve.lines(); len(got) != 1 || len(store.auditRows()) != 0 {
		t.Errorf("Proxmox received %v and %d rows were written, want the ACL read alone", got, len(store.auditRows()))
	}
}
