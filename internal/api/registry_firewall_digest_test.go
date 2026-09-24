package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The rule list digest on the eight positional rule writes — update and
// delete of the node, cluster, guest and security-group rule sets — driven
// through the REAL declaration and the REAL handler, with stand-ins only for
// Proxmox and the database.
//
// The stand-in Proxmox behaves the way pve-firewall's update_rule and
// delete_rule do (src/PVE/API2/Firewall/Rules.pm): it holds the current list
// digest and runs PVE::Tools::assert_if_modified (pve-common
// src/PVE/Tools.pm) against the digest a write carries — comparing only when
// one was sent, and dying with its sentence as a plain 500 when they differ.

// fwCurrentDigest is the digest of the rule list as the stand-in Proxmox holds
// it, and fwStaleDigest one read before that list changed. 40 hex characters,
// the length of the sha1 copy_list_with_digest produces.
const (
	fwCurrentDigest = "0123456789abcdef0123456789abcdef01234567"
	fwStaleDigest   = "fedcba9876543210fedcba9876543210fedcba98"
)

// fwRulePVE stands in for the cluster's Proxmox API.
type fwRulePVE struct {
	mu     sync.Mutex
	writes []fwRuleWrite
}

// fwRuleWrite is one write the stand-in received: where it went, and the
// digest it carried in each place a digest could travel.
type fwRuleWrite struct {
	method, path string
	query        url.Values
	form         url.Values
}

func (p *fwRulePVE) serve(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("stand-in Proxmox: ParseForm: %v", err)
		}
		// r.PostForm is filled for a PUT's form body only; the query of a
		// DELETE lands in r.URL.Query(), so the two stay apart.
		write := fwRuleWrite{method: r.Method, path: r.URL.Path, query: r.URL.Query(), form: r.PostForm}
		p.mu.Lock()
		p.writes = append(p.writes, write)
		p.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		sent := write.form.Get("digest")
		if r.Method == http.MethodDelete {
			sent = write.query.Get("digest")
		}
		// assert_if_modified: `if ($digest1 && $digest2 && ($digest1 ne $digest2))`.
		if sent != "" && sent != fwCurrentDigest {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"data":null,"message":"detected modified configuration - file changed by other user? Try again.\n"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":null}`))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (p *fwRulePVE) only(t *testing.T) fwRuleWrite {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.writes) != 1 {
		t.Fatalf("Proxmox received %d requests, want exactly 1", len(p.writes))
	}
	return p.writes[0]
}

// fwRuleDBTX stands in for the database. It accepts AuditLog's insert and
// counts it, and answers every single-row read with a row whose text columns
// all read "pve-01" — which is what the guest routes' resolveGuest needs: the
// guest lookup succeeds and the node it names is called pve-01. The one
// exception is the guest row's type column, which reads guestType, so a test
// can make the guest a VM ("qemu") or a container ("lxc").
type fwRuleDBTX struct {
	mu        sync.Mutex
	audits    int
	guestType string
}

func (d *fwRuleDBTX) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if !strings.Contains(sql, "INSERT INTO audit_log") {
		return pgconn.CommandTag{}, errHACreateUnexpectedQuery
	}
	d.mu.Lock()
	d.audits++
	d.mu.Unlock()
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}

func (*fwRuleDBTX) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errHACreateUnexpectedQuery
}

func (d *fwRuleDBTX) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if strings.Contains(sql, "-- name: GetVMByClusterAndVmid ") {
		return fwGuestRow{guestType: d.guestType}
	}
	return fwNodeNameRow{}
}

func (d *fwRuleDBTX) auditCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.audits
}

type fwNodeNameRow struct{}

func (fwNodeNameRow) Scan(dest ...any) error {
	for _, d := range dest {
		if s, ok := d.(*string); ok {
			*s = "pve-01"
		}
	}
	return nil
}

// fwGuestRow is the vms row GetVMByClusterAndVmid reads: every text column
// "pve-01" like fwNodeNameRow, except type (the sixth column the generated
// query selects — id, cluster_id, node_id, vmid, name, type, …).
type fwGuestRow struct{ guestType string }

func (r fwGuestRow) Scan(dest ...any) error {
	if err := (fwNodeNameRow{}).Scan(dest...); err != nil {
		return err
	}
	const typeColumn = 5
	if len(dest) != 21 {
		return errHACreateUnexpectedQuery
	}
	typ, ok := dest[typeColumn].(*string)
	if !ok {
		return errHACreateUnexpectedQuery
	}
	*typ = r.guestType
	return nil
}

// fwRuleRoute is one positional rule write.
type fwRuleRoute struct {
	name    string
	method  string
	path    string // the declared path
	target  string // the request Nexara receives, without its query
	pvePath string // where Proxmox receives it
	pick    func(*handlers.NetworkHandler, *handlers.NodeHandler) Handler
}

func fwRuleRoutes() []fwRuleRoute {
	cluster := "/api/v1/clusters/" + testClusterID
	return []fwRuleRoute{
		{"cluster update", fiber.MethodPut, firewallScope + "/rules/:pos",
			cluster + "/firewall/rules/3", "/api2/json/cluster/firewall/rules/3",
			func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.UpdateClusterFirewallRule }},
		{"cluster delete", fiber.MethodDelete, firewallScope + "/rules/:pos",
			cluster + "/firewall/rules/3", "/api2/json/cluster/firewall/rules/3",
			func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.DeleteClusterFirewallRule }},
		{"node update", fiber.MethodPut, nodeScope + "/:node_name/firewall/rules/:pos",
			cluster + "/nodes/pve-01/firewall/rules/3", "/api2/json/nodes/pve-01/firewall/rules/3",
			func(_ *handlers.NetworkHandler, n *handlers.NodeHandler) Handler { return n.UpdateNodeFirewallRule }},
		{"node delete", fiber.MethodDelete, nodeScope + "/:node_name/firewall/rules/:pos",
			cluster + "/nodes/pve-01/firewall/rules/3", "/api2/json/nodes/pve-01/firewall/rules/3",
			func(_ *handlers.NetworkHandler, n *handlers.NodeHandler) Handler { return n.DeleteNodeFirewallRule }},
		{"guest update", fiber.MethodPut, guestFirewallScope + "/rules/:pos",
			cluster + "/vms/101/firewall/rules/3", "/api2/json/nodes/pve-01/qemu/101/firewall/rules/3",
			func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.UpdateVMFirewallRule }},
		{"guest delete", fiber.MethodDelete, guestFirewallScope + "/rules/:pos",
			cluster + "/vms/101/firewall/rules/3", "/api2/json/nodes/pve-01/qemu/101/firewall/rules/3",
			func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.DeleteVMFirewallRule }},
		{"security group update", fiber.MethodPut, firewallScope + "/groups/:group/rules/:pos",
			cluster + "/firewall/groups/sg01/rules/3", "/api2/json/cluster/firewall/groups/sg01/3",
			func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.UpdateSecurityGroupRule }},
		{"security group delete", fiber.MethodDelete, firewallScope + "/groups/:group/rules/:pos",
			cluster + "/firewall/groups/sg01/rules/3", "/api2/json/cluster/firewall/groups/sg01/3",
			func(n *handlers.NetworkHandler, _ *handlers.NodeHandler) Handler { return n.DeleteSecurityGroupRule }},
	}
}

// newFWRuleApp mounts one route's real declaration, with its real permission
// and its real handler, wired to the two stand-ins.
func newFWRuleApp(t *testing.T, r fwRuleRoute, pve *fwRulePVE) (*fiber.App, *fwRuleDBTX) {
	t.Helper()
	return newFWRuleAppForGuest(t, r, pve, "qemu")
}

// newFWRuleAppForGuest is newFWRuleApp with the guest the database holds
// being of guestType.
func newFWRuleAppForGuest(t *testing.T, r fwRuleRoute, pve *fwRulePVE, guestType string) (*fiber.App, *fwRuleDBTX) {
	t.Helper()
	baseURL := pve.serve(t)
	encrypted, err := crypto.Encrypt("token-secret-value", haCreateEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cache := proxmox.NewClientCache(haCreateCacheQueries{cluster: db.Cluster{
		ID:                   uuid.MustParse(testClusterID),
		Name:                 "cluster01",
		ApiUrl:               baseURL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
		IsActive:             true,
	}}, haCreateEncKey, nil, nil)

	dbtx := &fwRuleDBTX{guestType: guestType}
	queries := db.New(dbtx)
	e := declaredEndpoint(t, r.method, r.path)
	e.Handler = r.pick(handlers.NewNetworkHandler(queries, haCreateEncKey, nil),
		handlers.NewNodeHandler(queries, haCreateEncKey, nil))

	authed := stubAuth(map[string]bool{"view:network": true, "manage:network": true, "delete:network": true})
	auth := func(c fiber.Ctx) error {
		handlers.SetProxmoxCacheLocal(c, cache)
		return authed(c)
	}
	return newRegistryApp(t, auth, e), dbtx
}

// fwRuleRequest builds the write with digest carried where the route reads
// it: a PUT's body, a DELETE's query. present=false leaves the key out.
func fwRuleRequest(r fwRuleRoute, digest string, present bool) *http.Request {
	var req *http.Request
	if r.method == fiber.MethodDelete {
		target := r.target
		if present {
			target += "?" + url.Values{"digest": {digest}}.Encode()
		}
		req = httptest.NewRequest(r.method, target, nil)
	} else {
		body := `{"action":"ACCEPT","enable":1}`
		if present {
			body = `{"action":"ACCEPT","enable":1,"digest":"` + digest + `"}`
		}
		req = jsonRequest(r.method, r.target, body)
	}
	req.Header.Set("X-Test-User", "yes")
	return req
}

// sentDigest is the digest the write carried to Proxmox, and whether it
// carried the key at all — looked for in BOTH places, so a digest sent in the
// wrong one is caught rather than read as absent.
func (w fwRuleWrite) sentDigest(t *testing.T) (string, bool) {
	t.Helper()
	right, wrong := w.form, w.query
	if w.method == http.MethodDelete {
		right, wrong = w.query, w.form
	}
	if _, ok := wrong["digest"]; ok {
		t.Errorf("%s carried the digest in the wrong place: query %v, form %v", w.method, w.query, w.form)
	}
	v, ok := right["digest"]
	if !ok {
		return "", false
	}
	return strings.Join(v, ","), true
}

// TestFirewallRuleWritesForwardTheListDigest: a digest sent to any of the
// eight positional rule writes reaches Proxmox as that digest, in the place
// Proxmox reads it; an absent or empty one reaches it as no key at all, which
// is the unconditional write these routes have always made.
func TestFirewallRuleWritesForwardTheListDigest(t *testing.T) {
	for _, r := range fwRuleRoutes() {
		for _, tt := range []struct {
			name    string
			digest  string
			present bool
			wantKey bool
		}{
			{"a digest is forwarded", fwCurrentDigest, true, true},
			{"no digest sends no key", "", false, false},
			{"an empty digest sends no key", "", true, false},
		} {
			t.Run(r.name+"/"+tt.name, func(t *testing.T) {
				pve := &fwRulePVE{}
				app, dbtx := newFWRuleApp(t, r, pve)

				if status, env := send(t, app, fwRuleRequest(r, tt.digest, tt.present)); status != fiber.StatusOK {
					t.Fatalf("status = %d (%s: %q), want 200", status, env.Error, env.Message)
				}
				w := pve.only(t)
				if w.method != r.method || w.path != r.pvePath {
					t.Fatalf("Proxmox received %s %s, want %s %s", w.method, w.path, r.method, r.pvePath)
				}
				got, ok := w.sentDigest(t)
				if ok != tt.wantKey {
					t.Fatalf("Proxmox received a digest key = %t (%q), want %t", ok, got, tt.wantKey)
				}
				if tt.wantKey && got != tt.digest {
					t.Errorf("Proxmox received digest %q, want %q", got, tt.digest)
				}
				if n := dbtx.auditCount(); n != 1 {
					t.Errorf("the write recorded %d audit rows, want 1", n)
				}
			})
		}
	}
}

// TestFirewallRuleWritesAnswerAStaleDigestWithConflict: when the list changed
// since the caller read it, Proxmox refuses the write and the caller is told
// so — 409 with the "conflict" slug, not the 502 gateway failure a plain PVE
// die would otherwise map to, and not the 404 a missing position gets. The
// refused write is not audited: nothing changed.
//
// The precondition twin runs the same stand-in with no digest and gets a 200,
// so the refusal is the digest's doing rather than the stand-in's.
func TestFirewallRuleWritesAnswerAStaleDigestWithConflict(t *testing.T) {
	for _, r := range fwRuleRoutes() {
		t.Run(r.name, func(t *testing.T) {
			pve := &fwRulePVE{}
			app, _ := newFWRuleApp(t, r, pve)
			if status, _ := send(t, app, fwRuleRequest(r, "", false)); status != fiber.StatusOK {
				t.Fatalf("precondition: the same write with no digest answered %d, want 200", status)
			}

			pve = &fwRulePVE{}
			app, dbtx := newFWRuleApp(t, r, pve)
			status, env := send(t, app, fwRuleRequest(r, fwStaleDigest, true))
			if status != fiber.StatusConflict {
				t.Fatalf("status = %d (%s: %q), want 409", status, env.Error, env.Message)
			}
			if env.Error != "conflict" {
				t.Errorf("error slug = %q, want conflict", env.Error)
			}
			if !strings.Contains(env.Message, "rule list changed since it was loaded") {
				t.Errorf("message = %q, want it to say the rule list changed", env.Message)
			}
			if got, _ := pve.only(t).sentDigest(t); got != fwStaleDigest {
				t.Errorf("Proxmox received digest %q, want the stale %q", got, fwStaleDigest)
			}
			if n := dbtx.auditCount(); n != 0 {
				t.Errorf("the refused write recorded %d audit rows, want 0", n)
			}
		})
	}
}

// TestFirewallRuleDigestIsDeclaredAsPVEDeclaresIt pins the declaration on all
// eight writes to pve-common's 'pve-config-digest' (src/PVE/JSONSchema.pm):
// optional, a string, maxLength 64, and nothing stricter — no pattern and no
// format, which would be Nexara's own and would refuse a digest PVE accepts.
// The creates must NOT declare it: PVE's create_rule never compares one.
func TestFirewallRuleDigestIsDeclaredAsPVEDeclaresIt(t *testing.T) {
	for _, r := range fwRuleRoutes() {
		t.Run(r.name, func(t *testing.T) {
			prop, ok := declaredEndpoint(t, r.method, r.path).Parameters["digest"]
			if !ok {
				t.Fatal("digest is not declared")
			}
			if prop.Type != apischema.String || !prop.Optional {
				t.Errorf("digest is %s optional=%t, want an optional string", prop.Type, prop.Optional)
			}
			if prop.MaxLength == nil || *prop.MaxLength != 64 {
				t.Errorf("digest MaxLength = %v, want 64 (pve-config-digest)", prop.MaxLength)
			}
			if prop.MinLength != nil || prop.Pattern != "" || prop.Format != "" || len(prop.Enum) != 0 {
				t.Errorf("digest carries a rule PVE does not: min %v, pattern %q, format %q, enum %v",
					prop.MinLength, prop.Pattern, prop.Format, prop.Enum)
			}
		})
	}
	for _, path := range []string{
		firewallScope + "/rules",
		nodeScope + "/:node_name/firewall/rules",
		guestFirewallScope + "/rules",
		firewallScope + "/groups/:group/rules",
	} {
		if _, ok := declaredEndpoint(t, fiber.MethodPost, path).Parameters["digest"]; ok {
			t.Errorf("POST %s declares digest; a create names no position to go stale", path)
		}
	}
}
