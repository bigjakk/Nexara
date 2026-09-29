package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// Both realm reads are gated on view:access, which every built-in Viewer holds.
// Proxmox's single-realm read returns a realm's section as stored, and that holds
// secrets: a Yubico setting's API id, key and url inside tfa, and an OIDC realm's
// client-key. The reads send the type alone, as Proxmox's own realm list does, and
// nothing else of the sort.
//
// Two layers see to it, the client (proxmox.RealmTFAType, pinned in
// internal/proxmox) and the handlers' shapers (pinned by access_realm_test.go in
// internal/api/handlers), and each hides the other's absence from a test that goes
// through both. So this file proves the end result, not either layer. It keeps the
// REAL declarations, permission gate, handlers and client, with stand-ins only for
// Proxmox and the database, so a route that skips both fails here.

// The secrets the stand-in Proxmox holds in a realm: the Yubico values in its tfa,
// and a client-key. Findable in a body whatever they sit under, and none real.
const (
	realmReadID  = "PROBE-REALM-YUBICO-ID-NOT-A-REAL-VALUE"
	realmReadKey = "PROBE-REALM-YUBICO-KEY-NOT-A-REAL-VALUE"
	realmReadURL = "https://example.com/wsapi/2.0/verify"

	// realmReadClientKey stands for a key of domains.cfg that AccessDomain does not
	// decode: an OIDC realm's client-key. No real section holds one beside a tfa,
	// which is no matter here; the stand-in only has to send it.
	realmReadClientKey = "PROBE-REALM-CLIENT-KEY-NOT-A-REAL-VALUE"

	// realmReadYubico is a Yubico setting as Proxmox stores it and its single
	// read returns it.
	realmReadYubico = "type=yubico,id=" + realmReadID + ",key=" + realmReadKey + ",url=" + realmReadURL
)

const realmReadRealm = "realm01"

// realmReadSingle is what the stand-in Proxmox answers to GET
// /access/domains/realm01, as its read does: the realm's stored section, tfa
// (when there is one) in the property-string form, and keys of the realm that
// AccessDomain does not decode, a client-key among them. It sends no realm id, as
// Proxmox does not, and default as 1.
func realmReadSingle(tfa string) string {
	stored := `"type":"ldap","comment":"sentinel-comment","default":1,"digest":"0123456789abcdef",` +
		`"server1":"192.0.2.10","base_dn":"dc=example,dc=com","user_attr":"uid","client-key":"` + realmReadClientKey + `"`
	if tfa != "" {
		stored += `,"tfa":"` + tfa + `"`
	}
	return `{"data":{` + stored + `}}`
}

// realmReadIndex is what the stand-in Proxmox answers to GET /access/domains, as
// its index does: tfa is the type alone, or absent.
const realmReadIndex = `{"data":[
	{"realm":"realm02","type":"ad","tfa":"oath"},
	{"realm":"pve","type":"pve","comment":"sentinel-builtin","default":1},
	{"realm":"realm01","type":"ldap","comment":"sentinel-comment","tfa":"yubico"},
	{"realm":"pam","type":"pam"}
]}`

// realmReadUnreducedIndex is a realm list whose tfa is the stored string, which is
// not what Proxmox sends and is what a Proxmox that stopped reducing it would.
// The entries are a Yubico setting, an oath one, and one with a key Proxmox does
// not know.
const realmReadUnreducedIndex = `{"data":[
	{"realm":"realm01","type":"ldap","tfa":"` + realmReadYubico + `"},
	{"realm":"realm02","type":"ad","tfa":"type=oath,digits=8,step=30"},
	{"realm":"realm03","type":"ldap","tfa":"type=yubico,key=` + realmReadKey + `,bogus=1"}
]}`

// newAccessRealmReadApp mounts the real GET .../access/domains and
// .../access/domains/:realm declarations, with their real permission, on the real
// AccessHandler and the two stand-ins the user reads use, with Proxmox answering as
// replies say. The caller holds view:access and nothing else, which is all a
// Viewer needs for these routes.
func newAccessRealmReadApp(t *testing.T, replies map[string]string) *fiber.App {
	t.Helper()
	pve, _, h := newAccessStandIns(t)
	for path, body := range replies {
		pve.reply(path, body)
	}

	list := declaredEndpoint(t, fiber.MethodGet, accessScope+"/domains")
	list.Handler = h.ListDomains
	detail := declaredEndpoint(t, fiber.MethodGet, accessScope+"/domains/:realm")
	detail.Handler = h.GetDomain

	return newRegistryApp(t, stubAuth(map[string]bool{"view:" + handlers.AccessResource: true}), list, detail)
}

// requireNoRealmSecrets fails if body carries a Yubico value, the setting's
// property-string form, or the client-key, under any key.
func requireNoRealmSecrets(t *testing.T, body string) {
	t.Helper()
	for _, leaked := range []string{
		realmReadID, realmReadKey, realmReadURL, realmReadClientKey, "wsapi", "key=", "id=", "url=", "type=yubico", "client-key",
	} {
		if strings.Contains(body, leaked) {
			t.Errorf("the body carries %q: %s", leaked, body)
		}
	}
}

// TestAccessRealmDetailSendsTheTypeOnly drives GET .../access/domains/:realm for a
// realm whose stored tfa is each thing it can be, and whose section holds a
// client-key besides. A Viewer's body never holds a Yubico value or the client-key,
// its tfa is the type, or "" when there is no type to send, and the rest of the
// realm comes through as it always did.
func TestAccessRealmDetailSendsTheTypeOnly(t *testing.T) {
	tests := []struct {
		name string
		tfa  string // as Proxmox stores it; "" for a realm with none
		want string
	}{
		{"yubico with its id, key and url", realmReadYubico, "yubico"},
		{"the same parts in another order", "url=" + realmReadURL + ",key=" + realmReadKey + ",id=" + realmReadID + ",type=yubico", "yubico"},
		{"oath", "type=oath,digits=8,step=30", "oath"},
		{"an id that reads like a type part", "id=type=oath,key=" + realmReadKey + ",type=yubico", "yubico"},
		{"no two-factor setting", "", ""},
		{"a setting with a key Proxmox does not know", "type=yubico,key=" + realmReadKey + ",bogus=1", "yubico"},
		{"a setting with no type", "id=" + realmReadID + ",key=" + realmReadKey, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newAccessRealmReadApp(t, map[string]string{
				"/api2/json/access/domains/" + realmReadRealm: realmReadSingle(tt.tfa),
			})

			status, body := getAccessRead(t, app, accessRoute(accessScope+"/domains")+"/"+realmReadRealm)
			if status != fiber.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, body)
			}
			requireNoRealmSecrets(t, body)

			var got map[string]any
			if err := json.Unmarshal([]byte(body), &got); err != nil {
				t.Fatalf("the body is not a JSON object (%s): %v", body, err)
			}
			requireFields(t, got, map[string]any{
				"realm": realmReadRealm, "type": "ldap", "comment": "sentinel-comment", "default": true, "tfa": tt.want,
			})
			if len(got) != 5 {
				t.Errorf("the body has %d fields, want realm, type, comment, tfa and default: %s", len(got), body)
			}
		})
	}
}

// realmReadListed decodes a realm list and indexes its entries by realm.
func realmReadListed(t *testing.T, body string) (map[string]map[string]any, int) {
	t.Helper()
	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("the body is not the {items,total} envelope (%s): %v", body, err)
	}
	if list.Total != len(list.Items) {
		t.Fatalf("total = %d with %d items: %s", list.Total, len(list.Items), body)
	}
	byRealm := map[string]map[string]any{}
	for _, item := range list.Items {
		id, _ := item["realm"].(string)
		byRealm[id] = item
	}
	return byRealm, list.Total
}

// TestAccessRealmListSendsTheTypeOnly drives GET .../access/domains twice: with
// the reply Proxmox gives, its tfa the type alone, which must come through as it
// is, since the SPA shows it; and with one whose tfa is the stored string, which
// is not what Proxmox sends, and must be reduced all the same.
func TestAccessRealmListSendsTheTypeOnly(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		want  map[string]string // realm -> tfa
	}{
		{
			name:  "as Proxmox sends it",
			reply: realmReadIndex,
			want:  map[string]string{"pam": "", "pve": "", "realm01": "yubico", "realm02": "oath"},
		},
		{
			name:  "with the stored string in place of the type",
			reply: realmReadUnreducedIndex,
			want:  map[string]string{"realm01": "yubico", "realm02": "oath", "realm03": "yubico"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newAccessRealmReadApp(t, map[string]string{"/api2/json/access/domains": tt.reply})

			status, body := getAccessRead(t, app, accessRoute(accessScope+"/domains"))
			if status != fiber.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, body)
			}
			requireNoRealmSecrets(t, body)

			byRealm, total := realmReadListed(t, body)
			if total != len(tt.want) {
				t.Fatalf("total = %d, want %d: %s", total, len(tt.want), body)
			}
			for realm, tfa := range tt.want {
				item, ok := byRealm[realm]
				if !ok {
					t.Fatalf("the list has no %s: %s", realm, body)
				}
				requireFields(t, item, map[string]any{"tfa": tfa})
			}
		})
	}
}

// TestAccessRealmListCarriesEveryOtherField keeps what the SPA reads of a realm
// list as it was: the realm, its type and comment, and which is the default, for
// the reply Proxmox gives.
func TestAccessRealmListCarriesEveryOtherField(t *testing.T) {
	app := newAccessRealmReadApp(t, map[string]string{"/api2/json/access/domains": realmReadIndex})

	status, body := getAccessRead(t, app, accessRoute(accessScope+"/domains"))
	if status != fiber.StatusOK {
		t.Fatalf("status = %d (%s), want 200", status, body)
	}
	byRealm, _ := realmReadListed(t, body)

	want := map[string]map[string]any{
		"pam":     {"realm": "pam", "type": "pam", "comment": "", "default": false},
		"pve":     {"realm": "pve", "type": "pve", "comment": "sentinel-builtin", "default": true},
		"realm01": {"realm": "realm01", "type": "ldap", "comment": "sentinel-comment", "default": false},
		"realm02": {"realm": "realm02", "type": "ad", "comment": "", "default": false},
	}
	for realm, w := range want {
		t.Run(realm, func(t *testing.T) {
			got, ok := byRealm[realm]
			if !ok {
				t.Fatalf("the list has no %s: %s", realm, body)
			}
			requireFields(t, got, w)
		})
	}
}

// TestAccessRealmListOfNoRealmsIsAnEmptyList keeps the listing's envelope as it
// was: a cluster whose Proxmox answers with no realms, or with null, still gets
// `items: []`.
func TestAccessRealmListOfNoRealmsIsAnEmptyList(t *testing.T) {
	for name, reply := range map[string]string{"an empty list": `{"data":[]}`, "null": `{"data":null}`} {
		t.Run(name, func(t *testing.T) {
			app := newAccessRealmReadApp(t, map[string]string{"/api2/json/access/domains": reply})

			status, body := getAccessRead(t, app, accessRoute(accessScope+"/domains"))
			if status != fiber.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, body)
			}
			if body != `{"items":[],"total":0}` {
				t.Errorf("body = %s, want {\"items\":[],\"total\":0}", body)
			}
		})
	}
}
