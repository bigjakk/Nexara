package handlers

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// A realm's two-factor setting is a property string that holds the Yubico API id, key
// and url, and Proxmox's single-realm read returns it whole to anyone with
// view:access (7fd2b27). The client reduces it to its type before it returns
// (proxmox.RealmTFAType, whose matrix is pinned in internal/proxmox), and the two
// shapers reduce it again, so a response rests on two layers. These tests pin the
// shapers, one layer, on their own: each is given the stored string directly, as if
// the client had not reduced it. TestReadStructsStripCredentials cannot cover this —
// the field is called tfa, which is not credential-shaped, and reducing is not
// stripping — and TestGuard_CredentialReadsAreShapedBeforeResponding only sees that a
// shaper is called.

// The Yubico values a stored tfa string holds and no response may carry: findable in a
// body whatever they sit under, and none of them real.
const (
	accessRealmProbeID  = "PROBE-YUBICO-ID-NOT-A-REAL-VALUE"
	accessRealmProbeKey = "PROBE-YUBICO-KEY-NOT-A-REAL-VALUE"
	accessRealmProbeURL = "https://example.com/wsapi/2.0/verify"

	// A Yubico setting as Proxmox stores it.
	accessRealmYubico = "type=yubico,id=" + accessRealmProbeID + ",key=" + accessRealmProbeKey + ",url=" + accessRealmProbeURL
)

// requireOnlyATFAType fails unless got is "", "yubico" or "oath", and carries nothing
// of the setting it came from. The values are spelled out here, not read from the
// reducer, so the set it draws from cannot be widened without failing this.
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

// accessRealmFilled returns a realm with every field a probe, its tfa a stored Yubico setting.
func accessRealmFilled(t *testing.T) proxmox.AccessDomain {
	t.Helper()
	realm := accessFilled[proxmox.AccessDomain](t)
	realm.TFA = accessRealmYubico
	return realm
}

// TestAccessRealmShapersSendTheTypeOnly drives both shapers with the Yubico setting as
// Proxmox's single read returns it, and reads the response back as the JSON a Viewer
// receives: the type is there, the Yubico values are not, and neither is the string
// they came in.
func TestAccessRealmShapersSendTheTypeOnly(t *testing.T) {
	realm := accessRealmFilled(t)
	for name, body := range map[string]any{
		"single": accessDomainForRead(realm),
		"list":   accessDomainsForRead([]proxmox.AccessDomain{realm}),
	} {
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
// Every field of the realm is filled by reflection, so one added to the struct later
// has to come through to pass, which a shaper that copied the fields it knew by name
// would not.
func TestAccessRealmShapersCarryEveryOtherField(t *testing.T) {
	realm := accessRealmFilled(t)
	for name, got := range map[string]proxmox.AccessDomain{
		"single": accessDomainForRead(realm),
		"list":   accessDomainsForRead([]proxmox.AccessDomain{realm})[0],
	} {
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

// TestAccessRealmShapersReduceEveryForm gives both shapers each thing a realm's tfa
// can be: Proxmox's index sends a bare type, which comes through as it is; the stored
// string is reduced; a value that only holds a type, or is not one, becomes empty. A
// shaper that let through what merely contains a type would put the Yubico key in the
// list. The list shaper is also held to keeping its entries and their order.
func TestAccessRealmShapersReduceEveryForm(t *testing.T) {
	for _, tc := range []struct{ name, tfa, want string }{
		{"a stored string", accessRealmYubico, "yubico"},
		{"a stored string for oath", "type=oath,digits=8,step=30", "oath"},
		{"a stored string with an id that reads like a type part", "id=type=oath,type=yubico,key=" + accessRealmProbeKey, "yubico"},
		{"a bare yubico, as the index sends it", "yubico", "yubico"},
		{"a bare oath, as the index sends it", "oath", "oath"},
		{"no tfa", "", ""},
		{"a bare type with options", "yubico,key=" + accessRealmProbeKey, ""},
		{"a value that holds a type without being one", "key=yubico,id=" + accessRealmProbeID, ""},
		{"a type Proxmox does not have", "type=totp,key=" + accessRealmProbeKey, ""},
		{"a type in the wrong case", "Yubico", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			single := accessDomainForRead(proxmox.AccessDomain{Realm: "realm01", Type: "ldap", TFA: tc.tfa}).TFA
			if single != tc.want {
				t.Errorf("single: tfa %q became %q, want %q", tc.tfa, single, tc.want)
			}
			requireOnlyATFAType(t, tc.tfa, single)

			list := accessDomainsForRead([]proxmox.AccessDomain{
				{Realm: "before", Type: "pve"},
				{Realm: "realm01", Type: "ldap", Comment: "sentinel-comment", TFA: tc.tfa, Default: true},
				{Realm: "after", Type: "pam"},
			})
			if len(list) != 3 || list[0].Realm != "before" || list[1].Realm != "realm01" || list[2].Realm != "after" {
				t.Fatalf("the shaper changed the list's entries or their order: %+v", list)
			}
			if list[1].TFA != tc.want {
				t.Errorf("list: tfa %q became %q, want %q", tc.tfa, list[1].TFA, tc.want)
			}
			requireOnlyATFAType(t, tc.tfa, list[1].TFA)
			if list[0].TFA != "" || list[2].TFA != "" {
				t.Errorf("entries with no tfa now have %q and %q", list[0].TFA, list[2].TFA)
			}
		})
	}
	if list := accessDomainsForRead(nil); len(list) != 0 {
		t.Errorf("no realms became %+v", list)
	}
}

// TestGuard_RealmReadsRespondWithTheirShapers requires each realm read to hand its
// response call the shaper's result. TestGuard_CredentialReadsAreShapedBeforeResponding
// only sees that the shaper is called, and the client reduces tfa as well, so a handler
// that called the shaper and sent the client's value instead would pass that guard and
// every route test: its layer would be there in name only. It is as blunt as the
// mistake: the response call's argument must BE a call of the shaper.
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

	// access.go alone: the realm handlers live there, and a move out of it fails the
	// count below loudly instead of leaving the guard watching nothing.
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "access.go", nil, 0)
	if err != nil {
		t.Fatalf("parse access.go: %v", err)
	}
	for _, read := range reads {
		found := 0
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
		if found != 1 {
			t.Fatalf("found %d AccessHandler methods named %s in access.go, want 1: it was renamed or moved, and this guard "+
				"has stopped watching it", found, read.handler)
		}
	}
}
