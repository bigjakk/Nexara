package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// A realm's two-factor setting is a property string that holds the Yubico API id,
// key and url, and Proxmox's single-realm read returns it whole. RealmTFAType
// reduces it to the type, and both realm reads do that before they return, so the
// raw string never leaves the client. These tests pin the reducer, that choke
// point, and the set of fields AccessDomain decodes.

// The values a stored tfa holds and nothing that leaves the client may carry:
// findable in a body whatever they sit under, and none of them real.
const (
	realmProbeID  = "PROBE-YUBICO-ID-NOT-A-REAL-VALUE"
	realmProbeKey = "PROBE-YUBICO-KEY-NOT-A-REAL-VALUE"
	realmProbeURL = "https://example.com/wsapi/2.0/verify"

	// A key of domains.cfg that AccessDomain does not decode, an OIDC realm's client-key.
	realmProbeClientKey = "PROBE-REALM-CLIENT-KEY-NOT-A-REAL-VALUE"

	// A Yubico setting as Proxmox stores it.
	realmYubico = "type=yubico,id=" + realmProbeID + ",key=" + realmProbeKey + ",url=" + realmProbeURL
)

// TestRealmTFAType: the bare types are what Proxmox's realm list sends. The stored
// forms and the decoys are what its read returns, each run through pve-common's own
// parse_property_string over the real $tfa_format. The rest cannot come from Proxmox
// and are here for what must never leave: a value that is not a type.
func TestRealmTFAType(t *testing.T) {
	for _, tc := range []struct{ name, tfa, want string }{
		{"yubico, as the realm list sends it", "yubico", "yubico"},
		{"oath, as the realm list sends it", "oath", "oath"},

		{"yubico with its id, key and url", realmYubico, "yubico"},
		{"the same parts with type last", "url=" + realmProbeURL + ",key=" + realmProbeKey + ",id=" + realmProbeID + ",type=yubico", "yubico"},
		{"the same parts with type between them", "id=" + realmProbeID + ",type=yubico,key=" + realmProbeKey, "yubico"},
		{"yubico alone", "type=yubico", "yubico"},
		{"oath alone", "type=oath", "oath"},
		{"oath with digits and step", "type=oath,digits=8,step=30", "oath"},

		// Values that hold type= without starting a part with it.
		{"an id that reads like a type part, before the type", "id=type=oath,type=yubico", "yubico"},
		{"an id that reads like a type part, after the type", "type=yubico,id=type=oath", "yubico"},
		{"a key that reads like a type part", "key=type=yubico,type=oath", "oath"},
		{"a url that holds type=", "url=https://example.com/wsapi?a=1&type=oath,type=yubico", "yubico"},
		{"probes in the id, key and url", "id=" + realmProbeID + ",key=" + realmProbeKey + ",type=oath,url=" + realmProbeURL, "oath"},

		{"nothing", "", ""},
		{"only commas and blanks", " , ,\t", ""},
		{"no type", "id=" + realmProbeID + ",key=" + realmProbeKey + ",url=" + realmProbeURL, ""},
		{"a bare type with options", "yubico,id=" + realmProbeID + ",key=" + realmProbeKey, ""},
		{"a type Proxmox does not have", "type=totp,key=" + realmProbeKey, ""},
		{"a type in the wrong case", "type=YUBICO,key=" + realmProbeKey, ""},
		{"a type with a trailing space", "type=yubico ", ""},
		{"a type with a suffix", "type=yubicoX,key=" + realmProbeKey, ""},
		{"an empty type", "type=", ""},
		{"a known type as a value, not a type part", "key=yubico", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RealmTFAType(tc.tfa)
			if got != tc.want {
				t.Errorf("RealmTFAType(%q) = %q, want %q", tc.tfa, got, tc.want)
			}
			requireRealmTFAOnlyAType(t, tc.tfa, got)
			if again := RealmTFAType(got); again != got {
				t.Errorf("RealmTFAType is not idempotent: %q gave %q, and that gave %q", tc.tfa, got, again)
			}
		})
	}
}

// requireRealmTFAOnlyAType fails unless got is "", "yubico" or "oath", and a type
// only when tfa is that type or has a type= part that says so. The values are
// spelled out here, not read from realmTFATypes, so the set the reducer draws from
// cannot be widened without failing this.
func requireRealmTFAOnlyAType(t *testing.T, tfa, got string) {
	t.Helper()
	switch got {
	case "":
		return
	case "yubico", "oath":
	default:
		t.Errorf("RealmTFAType(%q) = %q, which is not a two-factor type", tfa, got)
		return
	}
	if tfa != got && !strings.Contains(","+tfa, ",type="+got) {
		t.Errorf("RealmTFAType(%q) = %q, which nothing in the string says", tfa, got)
	}
	for _, leaked := range []string{"PROBE", "key=", "id=", "url=", "example.com"} {
		if strings.Contains(got, leaked) {
			t.Errorf("RealmTFAType(%q) = %q, which carries %q", tfa, got, leaked)
		}
	}
}

// TestRealmTFATypeAnswersOnlyATypeOrNothing runs every string of up to three
// parts from a set that holds each kind of part there is, the Yubico probes among
// them, and requires the answer to be a type the string says, or "". The table
// above shows what particular strings give; this is the property for all. Three
// parts put the type part in every position beside every decoy: the reducer's only
// branches are a bare type and the first part that starts with type=.
func TestRealmTFATypeAnswersOnlyATypeOrNothing(t *testing.T) {
	parts := []string{
		"type=yubico", "type=oath", "type=totp", "type=",
		"id=" + realmProbeID, "key=" + realmProbeKey, "url=" + realmProbeURL, "key=",
		"digits=8", "step=30", "yubico", "oath", "bogus=1", " ", "=", "id=type=oath", "key=type=yubico",
	}
	const maxParts = 3
	walked, want := 0, 0
	for n, total := 1, 1; n <= maxParts; n++ {
		total *= len(parts)
		want += total
	}
	var walk func(prefix []string)
	walk = func(prefix []string) {
		if len(prefix) > 0 {
			tfa := strings.Join(prefix, ",")
			requireRealmTFAOnlyAType(t, tfa, RealmTFAType(tfa))
			walked++
		}
		if len(prefix) == maxParts {
			return
		}
		for _, part := range parts {
			walk(append(prefix[:len(prefix):len(prefix)], part))
		}
	}
	walk(nil)
	if walked != want { // a walk that stopped early would prove less than the test says
		t.Fatalf("walked %d strings, want %d", walked, want)
	}
}

// requireNoRealmSecrets fails if v, marshalled, carries a Yubico value, the
// property string they came in, or the client-key the stand-in Proxmox holds.
func requireNoRealmSecrets(t *testing.T, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, leaked := range []string{
		realmProbeID, realmProbeKey, realmProbeURL, realmProbeClientKey, "wsapi", "key=", "id=", "url=", "type=yubico",
	} {
		if strings.Contains(string(raw), leaked) {
			t.Errorf("the client returned %q: %s", leaked, raw)
		}
	}
}

// TestGetAccessDomainReducesTFA drives the single-realm read against a stand-in
// Proxmox that answers as its read does, the realm's section as stored. What the
// client returns holds the type and no more, and the fields it does decode come
// through. This is the choke point: it does not depend on a handler shaping the
// result afterwards.
func TestGetAccessDomainReducesTFA(t *testing.T) {
	for _, tc := range []struct{ name, tfa, want string }{
		{"yubico with its id, key and url", realmYubico, "yubico"},
		{"oath", "type=oath,digits=8,step=30", "oath"},
		{"an id that reads like a type part", "id=type=oath,key=" + realmProbeKey + ",type=yubico", "yubico"},
		{"no two-factor setting", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			section := map[string]any{
				"type": "ldap", "comment": "sentinel-comment", "default": 1, "digest": "0123456789abcdef",
				"server1": "192.0.2.10", "client-key": realmProbeClientKey,
			}
			if tc.tfa != "" {
				section["tfa"] = tc.tfa
			}
			srv := newTestServer(t, map[string]http.HandlerFunc{
				"/api2/json/access/domains/realm01": func(w http.ResponseWriter, _ *http.Request) { jsonResponse(w, section) },
			})
			t.Cleanup(srv.Close)

			got, err := newTestClient(t, srv.URL).GetAccessDomain(context.Background(), "realm01")
			if err != nil {
				t.Fatalf("GetAccessDomain: %v", err)
			}
			if got.TFA != tc.want {
				t.Errorf("TFA = %q, want %q", got.TFA, tc.want)
			}
			if got.Realm != "realm01" || got.Type != "ldap" || got.Comment != "sentinel-comment" || !got.Default {
				t.Errorf("the other fields are %+v, want realm01, ldap, sentinel-comment and default", *got)
			}
			requireNoRealmSecrets(t, got)
		})
	}
}

// TestGetAccessDomainsReducesTFA drives the realm list twice over: as Proxmox
// answers it, each tfa the type alone, which must come through as it is; and with a
// stored string in one entry, which is not what Proxmox sends and must be reduced
// all the same.
func TestGetAccessDomainsReducesTFA(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/access/domains": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]any{
				{"realm": "realm02", "type": "ad", "tfa": "oath"},
				{"realm": "pve", "type": "pve", "comment": "sentinel-builtin", "default": 1},
				{"realm": "realm01", "type": "ldap", "tfa": realmYubico, "client-key": realmProbeClientKey},
				{"realm": "pam", "type": "pam", "tfa": "yubico"},
			})
		},
	})
	t.Cleanup(srv.Close)

	got, err := newTestClient(t, srv.URL).GetAccessDomains(context.Background())
	if err != nil {
		t.Fatalf("GetAccessDomains: %v", err)
	}
	want := []struct{ realm, tfa string }{{"pam", "yubico"}, {"pve", ""}, {"realm01", "yubico"}, {"realm02", "oath"}}
	if len(got) != len(want) {
		t.Fatalf("got %d realms, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Realm != w.realm || got[i].TFA != w.tfa {
			t.Errorf("realm %d is %q with tfa %q, want %q with %q", i, got[i].Realm, got[i].TFA, w.realm, w.tfa)
		}
	}
	if got[1].Comment != "sentinel-builtin" || !got[1].Default {
		t.Errorf("the other fields of pve are %+v, want its comment and default", got[1])
	}
	requireNoRealmSecrets(t, got)
}

// TestAccessDomainFieldSet pins the fields AccessDomain decodes. Nothing else keeps
// a field added to it from reaching every Viewer: Proxmox's single-realm read
// returns the realm's section as stored, so a field that can hold a secret is sent
// the moment it is decoded, whatever its name.
func TestAccessDomainFieldSet(t *testing.T) {
	want := []string{"comment", "default", "realm", "tfa", "type"}

	typ := reflect.TypeOf(AccessDomain{})
	var got []string
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		switch name {
		case "-":
			continue
		case "":
			name = field.Name
		}
		got = append(got, name)
	}
	sort.Strings(got)

	if !reflect.DeepEqual(got, want) {
		t.Errorf("AccessDomain decodes %v, and this pins %v.\n"+
			"Proxmox's GET /access/domains/{realm} returns a realm's section as stored, and domains.cfg holds "+
			"secrets: an OIDC realm's client-key, and the Yubico API key inside a tfa. A field added here reaches "+
			"every Viewer with view:access unless it is classified first. Decide whether it can hold a secret; if "+
			"it can, leave it out or reduce it in GetAccessDomain and GetAccessDomains and in the handlers' "+
			"accessDomainForRead and accessDomainsForRead. Then update this list.", got, want)
	}
}
