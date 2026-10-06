package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// Both user reads are gated on view:access, which every built-in Viewer holds, and
// neither may send a user's keys (see accessUserResponse in internal/api/handlers). The
// shapers are pinned on their own by TestAccessUserReadsWithholdKeys and
// TestReadStructsStripCredentials in that package; this file keeps the REAL
// declarations, permission gate and handlers over the stand-ins, so that a handler which
// stops calling its shaper, or a decode that stops reaching it, fails here as well.

// accessReadKeys is the value the stand-in Proxmox gives alice's keys: findable in a
// body whatever key it sits under, and not a real key.
const accessReadKeys = "PROBE-ACCESS-KEYS-NOT-A-REAL-VALUE"

// The stand-in cluster's three users, each answering differently for keys: alice has a
// value, nexara sends the field empty, and root does not send it at all. The list
// carries groups as a string and the detail as an array, as Proxmox does, and enable as
// 0 or 1.
const (
	accessReadListJSON = `{"data":[
		{"userid":"alice@pve","enable":1,"expire":0,"firstname":"Alice","lastname":"Example",
		 "email":"alice@example.com","comment":"sentinel-comment","groups":"operators,auditors",
		 "keys":"` + accessReadKeys + `","realm-type":"pve","totp-locked":0,"tfa-locked-until":0},
		{"userid":"nexara@pve","enable":0,"expire":1767225600,"firstname":"","lastname":"",
		 "email":"","comment":"","groups":"","keys":"","realm-type":"pve","totp-locked":1,"tfa-locked-until":0},
		{"userid":"root@pam","enable":1,"expire":0,"email":"root@example.com","realm-type":"pam",
		 "totp-locked":0,"tfa-locked-until":1767225600}
	]}`

	accessReadAliceJSON = `{"data":{"enable":1,"expire":0,"firstname":"Alice","lastname":"Example",
		"email":"alice@example.com","comment":"sentinel-comment","groups":["operators","auditors"],
		"keys":"` + accessReadKeys + `"}}`
	accessReadNexaraJSON = `{"data":{"enable":0,"expire":1767225600,"groups":[],"keys":""}}`
	accessReadRootJSON   = `{"data":{"enable":1,"expire":0,"email":"root@example.com"}}`
)

// accessReadCluster is what the stand-in Proxmox says for each read the two routes make.
var accessReadCluster = map[string]string{
	"/api2/json/access/users":            accessReadListJSON,
	"/api2/json/access/users/alice@pve":  accessReadAliceJSON,
	"/api2/json/access/users/nexara@pve": accessReadNexaraJSON,
	"/api2/json/access/users/root@pam":   accessReadRootJSON,
}

// newAccessReadApp mounts the real read routes wire picks on the real AccessHandler over
// a stand-in Proxmox answering as replies say, for a caller holding view:access and
// nothing else, which is all a Viewer needs.
func newAccessReadApp(t *testing.T, replies map[string]string, wire func(h *handlers.AccessHandler) []realRoute) *fiber.App {
	t.Helper()
	app, pve, _ := newAccessApp(t, grantsOf("view:"+handlers.AccessResource), wire)
	for path, body := range replies {
		pve.reply(path, body)
	}
	return app
}

func newAccessUserReadApp(t *testing.T, replies map[string]string) *fiber.App {
	t.Helper()
	return newAccessReadApp(t, replies, func(h *handlers.AccessHandler) []realRoute {
		return []realRoute{
			{fiber.MethodGet, accessScope + "/users", h.ListUsers},
			{fiber.MethodGet, accessScope + "/users/:userid", h.GetUser},
		}
	})
}

// getAccessRead sends an authenticated GET and returns the status and the raw body,
// which is what a Viewer receives.
func getAccessRead(t *testing.T, app *fiber.App, target string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("X-Test-User", "yes")
	status, body := sendBody(t, app, req)
	return status, string(body)
}

// requireKeysWithheld fails unless body is free of the keys value and of a keys key,
// blank or not, whatever object it is under. has_keys is another name and is checked
// with the other fields.
func requireKeysWithheld(t *testing.T, body string) {
	t.Helper()
	if strings.Contains(body, accessReadKeys) {
		t.Errorf("the keys value reached the body: %s", body)
	}
	if strings.Contains(body, `"keys"`) {
		t.Errorf("the body carries a keys key; it should be absent, not blank: %s", body)
	}
}

// requireFields fails unless every field want names holds that value in got. Fields it
// does not name are not examined, so one Proxmox gains later does not fail it;
// TestAccessUserReadsCarryEveryOtherField in internal/api/handlers covers those.
func requireFields(t *testing.T, got, want map[string]any) {
	t.Helper()
	for name, w := range want {
		if g, ok := got[name]; !ok || !reflect.DeepEqual(g, w) {
			t.Errorf("%q is %v (present: %v), want %v; the object is %v", name, g, ok, w, got)
		}
	}
}

// requireEmptyList drives a list route whose Proxmox answers with each reply and holds
// the listing's envelope as it was: a cluster with nothing, or null, still gets `items: []`.
func requireEmptyList(t *testing.T, newApp func(*testing.T, map[string]string) *fiber.App, proxmoxPath, listing string) {
	t.Helper()
	for name, reply := range map[string]string{"an empty list": `{"data":[]}`, "null": `{"data":null}`} {
		t.Run(name, func(t *testing.T) {
			status, body := getAccessRead(t, newApp(t, map[string]string{proxmoxPath: reply}), listing)
			if status != fiber.StatusOK || body != `{"items":[],"total":0}` {
				t.Errorf("status %d, body = %s, want 200 {\"items\":[],\"total\":0}", status, body)
			}
		})
	}
}

// TestAccessUserListWithholdsKeys drives GET .../access/users. The value is not in the
// body and no user has a keys key, blank or not; has_keys is true for the user Proxmox
// sent a value for and false for one it sent "" or nothing for; and the fields the SPA
// reads come through as they always did.
func TestAccessUserListWithholdsKeys(t *testing.T) {
	app := newAccessUserReadApp(t, accessReadCluster)

	status, body := getAccessRead(t, app, accessRoute(accessScope+"/users"))
	if status != fiber.StatusOK {
		t.Fatalf("status = %d (%s), want 200", status, body)
	}
	requireKeysWithheld(t, body)

	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("the body is not the {items,total} envelope (%s): %v", body, err)
	}
	if list.Total != 3 || len(list.Items) != 3 {
		t.Fatalf("total = %d with %d items, want 3 and 3: %s", list.Total, len(list.Items), body)
	}
	byID := map[string]map[string]any{}
	for _, item := range list.Items {
		id, _ := item["userid"].(string)
		byID[id] = item
	}

	want := map[string]map[string]any{
		"alice@pve": {
			"userid": "alice@pve", "enable": true, "expire": float64(0), "firstname": "Alice",
			"lastname": "Example", "email": "alice@example.com", "comment": "sentinel-comment",
			"groups": "operators,auditors", "realm-type": "pve", "totp-locked": false,
			"tfa-locked-until": float64(0), "has_keys": true,
		},
		"nexara@pve": {
			"userid": "nexara@pve", "enable": false, "expire": float64(1767225600), "firstname": "",
			"lastname": "", "email": "", "comment": "", "groups": "", "realm-type": "pve",
			"totp-locked": true, "tfa-locked-until": float64(0), "has_keys": false,
		},
		"root@pam": {
			"userid": "root@pam", "enable": true, "expire": float64(0), "email": "root@example.com",
			"realm-type": "pam", "totp-locked": false, "tfa-locked-until": float64(1767225600),
			"has_keys": false,
		},
	}
	for id, w := range want {
		t.Run(id, func(t *testing.T) {
			got, ok := byID[id]
			if !ok {
				t.Fatalf("the list has no %s: %s", id, body)
			}
			requireFields(t, got, w)
		})
	}
}

// TestAccessUserDetailWithholdsKeys drives GET .../access/users/:userid for the same
// three users. The detail sends groups as an array, and the client restores the userid
// Proxmox leaves out of it.
func TestAccessUserDetailWithholdsKeys(t *testing.T) {
	app := newAccessUserReadApp(t, accessReadCluster)

	for _, tt := range []struct {
		name string
		user string // as the client sends it in the path: percent-encoded
		want map[string]any
	}{
		{"a user with keys", "alice%40pve", map[string]any{
			"userid": "alice@pve", "enable": true, "expire": float64(0), "firstname": "Alice",
			"lastname": "Example", "email": "alice@example.com", "comment": "sentinel-comment",
			"groups": []any{"operators", "auditors"}, "has_keys": true,
		}},
		{"a user whose keys are sent empty", "nexara%40pve", map[string]any{
			"userid": "nexara@pve", "enable": false, "expire": float64(1767225600), "groups": []any{}, "has_keys": false,
		}},
		{"a user whose keys are not sent", "root%40pam", map[string]any{
			"userid": "root@pam", "enable": true, "expire": float64(0), "email": "root@example.com", "has_keys": false,
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			target := strings.Replace(accessRoute(accessScope+"/users/:userid"), testAccessUserID, tt.user, 1)
			status, body := getAccessRead(t, app, target)
			if status != fiber.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, body)
			}
			requireKeysWithheld(t, body)

			var got map[string]any
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("the body is not a JSON object (%s): %v", body, err)
			}
			requireFields(t, got, tt.want)
		})
	}
}

func TestAccessUserListOfNoUsersIsAnEmptyList(t *testing.T) {
	requireEmptyList(t, newAccessUserReadApp, "/api2/json/access/users", accessRoute(accessScope+"/users"))
}
