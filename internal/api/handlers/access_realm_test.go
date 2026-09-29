package handlers

import (
	"encoding/json"
	"go/ast"
	"reflect"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// A realm's two-factor setting is a property string that holds the Yubico API id,
// key and url, and Proxmox's single-realm read returns it whole to anyone with
// view:access. The client reduces it to its type before it returns
// (proxmox.RealmTFAType, pinned in internal/proxmox), and the two shapers reduce
// it again, so a response rests on two layers. These tests pin the shapers, one
// layer, on their own: each is given the stored string directly, as if the client
// had not reduced it.
//
// TestReadStructsStripCredentials cannot cover this. The field is called tfa,
// which is not credential-shaped, and reducing a value is not the stripping that
// test looks for (a stripped key is absent, a kept probe survives). The route
// tests in internal/api drive the real declarations, and
// TestGuard_CredentialReadsAreShapedBeforeResponding requires both handlers to
// call their shapers.

// The Yubico values a stored tfa string holds and no response may carry: findable
// in a body whatever they sit under, and none of them real.
const (
	accessRealmProbeID  = "PROBE-YUBICO-ID-NOT-A-REAL-VALUE"
	accessRealmProbeKey = "PROBE-YUBICO-KEY-NOT-A-REAL-VALUE"
	accessRealmProbeURL = "https://example.com/wsapi/2.0/verify"
)

// accessRealmYubico is a Yubico setting as Proxmox stores it.
const accessRealmYubico = "type=yubico,id=" + accessRealmProbeID +
	",key=" + accessRealmProbeKey + ",url=" + accessRealmProbeURL

// requireOnlyATFAType fails unless got is "", "yubico" or "oath", and carries
// nothing of the setting it came from. The three values are spelled out here, not
// read from the reducer, so that the set it draws from cannot be widened without
// failing this.
func requireOnlyATFAType(t *testing.T, tfa, got string) {
	t.Helper()
	switch got {
	case "", "yubico", "oath":
	default:
		t.Errorf("tfa %q became %q, which is not a two-factor type", tfa, got)
	}
	for _, leaked := range []string{"PROBE", "key=", "id=", "url=", "example.com"} {
		if strings.Contains(got, leaked) {
			t.Errorf("tfa %q became %q, which carries %q", tfa, got, leaked)
		}
	}
}

// accessRealmFilled returns a realm with every field a probe, its tfa a stored
// Yubico setting.
func accessRealmFilled(t *testing.T) proxmox.AccessDomain {
	t.Helper()
	realm := accessFilled[proxmox.AccessDomain](t)
	realm.TFA = accessRealmYubico
	return realm
}

// TestAccessRealmShapersSendTheTypeOnly drives both shapers with the Yubico setting
// as Proxmox's single read returns it, and reads the response back as the JSON a
// Viewer receives: the type is there, the Yubico values are not, and neither is
// the string they came in.
func TestAccessRealmShapersSendTheTypeOnly(t *testing.T) {
	realm := accessRealmFilled(t)

	bodies := map[string]any{
		"single": accessDomainForRead(realm),
		"list":   accessDomainsForRead([]proxmox.AccessDomain{realm}),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, leaked := range []string{accessRealmProbeID, accessRealmProbeKey, accessRealmProbeURL, "key=", "id=", "url=", "type=yubico"} {
				if strings.Contains(string(raw), leaked) {
					t.Errorf("the body carries %q: %s", leaked, raw)
				}
			}
			if !strings.Contains(string(raw), `"tfa":"yubico"`) {
				t.Errorf("the body does not carry tfa yubico: %s", raw)
			}
		})
	}
}

// TestAccessRealmShapersCarryEveryOtherField requires each shaper to change the tfa
// field and nothing else: the SPA reads the rest of a realm from these responses.
// Every field of the realm is filled by reflection, so one added to the struct
// later has to come through to pass, which a shaper that copied the fields it
// knew by name would not.
func TestAccessRealmShapersCarryEveryOtherField(t *testing.T) {
	realm := accessRealmFilled(t)

	shaped := map[string]proxmox.AccessDomain{
		"single": accessDomainForRead(realm),
		"list":   accessDomainsForRead([]proxmox.AccessDomain{realm})[0],
	}
	for name, got := range shaped {
		t.Run(name, func(t *testing.T) {
			if got.TFA != "yubico" {
				t.Errorf("tfa = %q, want yubico", got.TFA)
			}
			got.TFA = realm.TFA
			if !reflect.DeepEqual(got, realm) {
				t.Errorf("a field other than tfa changed:\n got %+v\nwant %+v", got, realm)
			}
		})
	}
}

// TestAccessRealmListShaper pins what the list shaper does with each thing a
// realm's tfa can be in a realm list. Proxmox's index sends a bare type, which
// comes through as it is; the stored string is reduced, and a value that only
// holds a type, or is not one, becomes empty. A shaper that let through what
// merely contains a type would put the Yubico key in the list.
func TestAccessRealmListShaper(t *testing.T) {
	tests := []struct {
		name string
		tfa  string
		want string
	}{
		{"a bare yubico, as the index sends it", "yubico", "yubico"},
		{"a bare oath, as the index sends it", "oath", "oath"},
		{"no tfa", "", ""},
		{"a stored string", accessRealmYubico, "yubico"},
		{"a stored string for oath", "type=oath,digits=8,step=30", "oath"},
		{"a stored string with an id that reads like a type part", "id=type=oath,type=yubico,key=" + accessRealmProbeKey, "yubico"},
		{"a stored string with a key Proxmox does not know", "type=yubico,key=" + accessRealmProbeKey + ",bogus=1", "yubico"},
		{"a bare type with options", "yubico,key=" + accessRealmProbeKey, ""},
		{"a value that holds a type without being one", "key=yubico,id=" + accessRealmProbeID, ""},
		{"a type Proxmox does not have", "totp", ""},
		{"a type in the wrong case", "Yubico", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list := accessDomainsForRead([]proxmox.AccessDomain{
				{Realm: "before", Type: "pve"},
				{Realm: "realm01", Type: "ldap", Comment: "sentinel-comment", TFA: tc.tfa, Default: true},
				{Realm: "after", Type: "pam"},
			})
			if len(list) != 3 || list[0].Realm != "before" || list[1].Realm != "realm01" || list[2].Realm != "after" {
				t.Fatalf("the shaper changed the list's entries or their order: %+v", list)
			}
			if list[1].TFA != tc.want {
				t.Errorf("tfa %q became %q, want %q", tc.tfa, list[1].TFA, tc.want)
			}
			requireOnlyATFAType(t, tc.tfa, list[1].TFA)
			for _, other := range []int{0, 2} {
				if list[other].TFA != "" {
					t.Errorf("entry %d has no tfa, and now has %q", other, list[other].TFA)
				}
			}
		})
	}

	t.Run("no realms", func(t *testing.T) {
		if list := accessDomainsForRead(nil); len(list) != 0 {
			t.Errorf("nil became %+v", list)
		}
	})
}

// TestAccessRealmSingleShaperReducesEveryForm gives the single-read shaper the
// forms the list shaper gets above, so that neither is tested only through the
// other: the stored string, the bare type, and the values that must become empty.
func TestAccessRealmSingleShaperReducesEveryForm(t *testing.T) {
	tests := []struct {
		name string
		tfa  string
		want string
	}{
		{"a stored string", accessRealmYubico, "yubico"},
		{"a bare yubico", "yubico", "yubico"},
		{"a bare oath", "oath", "oath"},
		{"no tfa", "", ""},
		{"a bare type with options", "yubico,key=" + accessRealmProbeKey, ""},
		{"a value that holds a type without being one", "key=yubico,id=" + accessRealmProbeID, ""},
		{"a type Proxmox does not have", "type=totp,key=" + accessRealmProbeKey, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := accessDomainForRead(proxmox.AccessDomain{Realm: "realm01", Type: "ldap", TFA: tc.tfa}).TFA
			if got != tc.want {
				t.Errorf("tfa %q became %q, want %q", tc.tfa, got, tc.want)
			}
			requireOnlyATFAType(t, tc.tfa, got)
		})
	}
}

// TestGuard_RealmReadsRespondWithTheirShapers requires each realm read to hand its
// response call the shaper's result. TestGuard_CredentialReadsAreShapedBeforeResponding
// only sees that the shaper is called, and the client reduces tfa as well, so a
// handler that called the shaper and sent the client's value instead would pass that
// guard and every route test: its layer would be there in name only.
//
// It is as blunt as the mistake: the response call's argument must BE a call of the
// shaper. A handler that shaped into a variable first has to be added here by hand.
func TestGuard_RealmReadsRespondWithTheirShapers(t *testing.T) {
	reads := []struct{ handler, respond, shaper string }{
		{"GetDomain", "JSON", "accessDomainForRead"},
		{"ListDomains", "RespondItems", "accessDomainsForRead"},
	}

	calleeName := func(call *ast.CallExpr) string {
		switch f := call.Fun.(type) {
		case *ast.SelectorExpr:
			return f.Sel.Name
		case *ast.Ident:
			return f.Name
		}
		return ""
	}
	isAccessHandlerMethod := func(fn *ast.FuncDecl) bool {
		if fn.Recv == nil || len(fn.Recv.List) != 1 {
			return false
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			return false
		}
		recv, ok := star.X.(*ast.Ident)
		return ok && recv.Name == "AccessHandler"
	}

	fset, files := parsePackageFiles(t)
	for _, read := range reads {
		found := 0
		for _, file := range files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || fn.Name.Name != read.handler || !isAccessHandlerMethod(fn) {
					continue
				}
				found++
				shaped := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok || calleeName(call) != read.respond {
						return true
					}
					for _, arg := range call.Args {
						if inner, ok := arg.(*ast.CallExpr); ok && calleeName(inner) == read.shaper {
							shaped = true
						}
					}
					return true
				})
				if !shaped {
					t.Errorf("%s: %s does not pass %s a call of %s. The client reduces tfa too, so the response "+
						"would not show it, and the handler's own layer would be gone.",
						fset.Position(fn.Pos()), read.handler, read.respond, read.shaper)
				}
			}
		}
		if found != 1 {
			t.Fatalf("found %d AccessHandler methods named %s, want 1: it was renamed or moved, and this guard "+
				"has stopped watching it", found, read.handler)
		}
	}
}
