package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// These tests exist because a path parameter crosses three encoders on its way
// from the browser to Proxmox, and for two routes it used to cross one too many.
//
// The chain is: the SPA percent-encodes the value (encodeURIComponent); Fiber
// routes and hands it back UNCHANGED, because UnescapePath is at its default of
// false; the handler reads it; the proxmox client percent-escapes it into the
// outbound path. Without a decode in the middle, the client takes the encoded
// TEXT for the name and escapes it again. A pool the operator named "a#b"
// arrives as "a%23b" and goes out as "a%2523b", so Proxmox resolves a pool
// named "a%23b" — which does not exist. An IP set entry "192.0.2.0/24" arrives
// as "192.0.2.0%2F24" and goes out as "192.0.2.0%252F24", which is why no CIDR
// entry could be deleted through that route at all.
//
// The assertion is deliberately made on the RAW request target the far side
// receives (r.RequestURI, not r.URL.Path), for the reason the whole defect is
// invisible otherwise: r.URL.Path is already decoded, so "%2523" and "%23" look
// identical there. Everything below compares bytes on the wire.

// pathParamEncKey is the AES-256 key the fake cluster row's token is encrypted
// with. Fixed, and a test fixture only.
const pathParamEncKey = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// pathParamNode is the node resolveClusterNode will pick.
const pathParamNode = "pve-01"

// proxmoxCapture records the raw request target of every call a handler makes
// to the stand-in Proxmox server.
type proxmoxCapture struct {
	mu      sync.Mutex
	targets []string
}

func (p *proxmoxCapture) record(target string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = append(p.targets, target)
}

func (p *proxmoxCapture) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = nil
}

// only returns the single target the request produced, failing if the handler
// made no Proxmox call or more than one.
func (p *proxmoxCapture) only(t *testing.T) string {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.targets) != 1 {
		t.Fatalf("the handler made %d Proxmox calls, want exactly 1: %q", len(p.targets), p.targets)
	}
	return p.targets[0]
}

// seen returns the targets recorded since the last reset.
func (p *proxmoxCapture) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.targets...)
}

// newProxmoxCaptureServer stands up a server that answers every request with a
// Proxmox-shaped envelope carrying a UPID, and records what it was asked for.
//
// http.HandlerFunc directly rather than http.ServeMux: ServeMux cleans and
// redirects paths, and would answer a request whose segment contains an encoded
// slash with a 301 to the cleaned form — normalising away the very thing under
// test.
func newProxmoxCaptureServer(t *testing.T) (string, *proxmoxCapture) {
	t.Helper()
	cap := &proxmoxCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.record(r.RequestURI)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": "UPID:" + pathParamNode + ":00001234:0000ABCD:66000000:cephdestroypool::root@pam:",
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL, cap
}

// pathParamCacheQueries is the proxmox.CacheQueries a ClientCache needs: one
// cluster, pointed at the capture server.
type pathParamCacheQueries struct{ cluster db.Cluster }

func (q pathParamCacheQueries) GetCluster(_ context.Context, id uuid.UUID) (db.Cluster, error) {
	if id != q.cluster.ID {
		return db.Cluster{}, pgx.ErrNoRows
	}
	return q.cluster, nil
}

func (pathParamCacheQueries) GetPBSServer(context.Context, uuid.UUID) (db.PbsServer, error) {
	return db.PbsServer{}, pgx.ErrNoRows
}

// No node endpoints, so SelectClusterEndpoint keeps the configured api_url —
// the capture server — rather than failing over to a member address.
func (pathParamCacheQueries) ListNodeEndpoints(context.Context, uuid.UUID) ([]db.ListNodeEndpointsRow, error) {
	return nil, nil
}

// errPathParamUnexpectedQuery is what the stand-in DBTX answers for anything
// other than the node listing. Nothing else should run: with no user_id local,
// AuditLog and TrackTask both return before touching the database.
var errPathParamUnexpectedQuery = errors.New("unexpected query")

// pathParamDBTX answers the node listing with one online node and fails
// everything else — including any OTHER statement sent to Query, so a handler
// that starts reading a different result set fails loudly here rather than
// being handed node rows that happen to satisfy it.
type pathParamDBTX struct{ node db.Node }

func (pathParamDBTX) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errPathParamUnexpectedQuery
}

func (d pathParamDBTX) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if !strings.Contains(sql, "FROM nodes") {
		return nil, errPathParamUnexpectedQuery
	}
	return &nodeRows{rows: []db.Node{d.node}}, nil
}

func (pathParamDBTX) QueryRow(context.Context, string, ...any) pgx.Row {
	return failRow{err: errPathParamUnexpectedQuery}
}

// nodeRows replays a fixed []db.Node through pgx.Rows, assigning positionally
// in the generated struct's field order — which is the order the generated
// queries select. A column added in the middle makes this report a type
// mismatch rather than silently shifting a value into the wrong field.
type nodeRows struct {
	rows []db.Node
	i    int
	err  error
}

func (r *nodeRows) Next() bool {
	if r.i >= len(r.rows) {
		return false
	}
	r.i++
	return true
}

func (r *nodeRows) Scan(dest ...any) error {
	if r.i == 0 || r.i > len(r.rows) {
		r.err = fmt.Errorf("scan called without a preceding Next")
		return r.err
	}
	row := reflect.ValueOf(r.rows[r.i-1])
	if len(dest) != row.NumField() {
		r.err = fmt.Errorf("scan got %d destinations, want %d", len(dest), row.NumField())
		return r.err
	}
	for i, d := range dest {
		ptr := reflect.ValueOf(d)
		if ptr.Kind() != reflect.Pointer {
			r.err = fmt.Errorf("scan destination %d is %T, not a pointer", i, d)
			return r.err
		}
		if ptr.Elem().Type() != row.Field(i).Type() {
			r.err = fmt.Errorf("scan destination %d is *%s, want *%s — the select column order changed",
				i, ptr.Elem().Type(), row.Field(i).Type())
			return r.err
		}
		ptr.Elem().Set(row.Field(i))
	}
	return nil
}

func (r *nodeRows) Err() error                                 { return r.err }
func (*nodeRows) Close()                                       {}
func (*nodeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*nodeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*nodeRows) Values() ([]any, error)                       { return nil, errPathParamUnexpectedQuery }
func (*nodeRows) RawValues() [][]byte                          { return nil }
func (*nodeRows) Conn() *pgx.Conn                              { return nil }

// pathParamHarness is one wired-up request pipeline: a real Fiber app carrying
// the real handler, a real proxmox.Client built the way production builds it,
// and a capture server on the far end.
type pathParamHarness struct {
	app       *fiber.App
	clusterID uuid.UUID
	capture   *proxmoxCapture
}

// newPathParamHarness builds that pipeline. register mounts the route under
// test on the app; it is handed the queries the handler should hold.
func newPathParamHarness(t *testing.T, register func(app *fiber.App, queries *db.Queries)) *pathParamHarness {
	t.Helper()

	baseURL, capture := newProxmoxCaptureServer(t)
	clusterID := uuid.New()

	encrypted, err := crypto.Encrypt("token-secret-value", pathParamEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cache := proxmox.NewClientCache(pathParamCacheQueries{cluster: db.Cluster{
		ID:                   clusterID,
		Name:                 "cluster01",
		ApiUrl:               baseURL,
		TokenID:              "user@pam!test",
		TokenSecretEncrypted: encrypted,
		IsActive:             true,
	}}, pathParamEncKey, nil, nil)

	// fiber.New() with no Config is exactly production's UnescapePath: the
	// server never sets it (see buildFiberConfig in internal/api/server.go), so
	// the default of false is what runs.
	//
	// Do not be tempted to fix the double encoding by flipping it to true. It
	// decodes the whole path before routing, so an IP set entry's encoded slash
	// becomes a real separator and the entry route 404s on every CIDR; and it
	// decodes with fasthttp's ARGUMENT decoder, which turns "+" into a space,
	// silently renaming the Ceph pool "rbd+meta" to "rbd meta" — a name Ceph
	// will not have.
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		SetProxmoxCacheLocal(c, cache)
		return c.Next()
	})
	register(app, db.New(pathParamDBTX{node: db.Node{
		ID:        uuid.New(),
		ClusterID: clusterID,
		Name:      pathParamNode,
		Status:    "online",
	}}))

	return &pathParamHarness{app: app, clusterID: clusterID, capture: capture}
}

// send drives one request and returns the raw target the Proxmox side saw.
//
// target is checked against the request's own escaped path before it goes
// anywhere, because net/url rewrites some request targets on the way in and a
// test that assumes otherwise proves nothing: httptest.NewRequest parses the
// string, and only URL.EscapedPath says what will actually be written into the
// request line.
func (h *pathParamHarness) send(t *testing.T, method, target string) string {
	t.Helper()

	req := httptest.NewRequest(method, target, nil)
	if got := req.URL.EscapedPath(); got != target {
		t.Fatalf("the request would go out as %q, not the %q this case means to send — "+
			"net/url rewrote the target, so the case is testing something else", got, target)
	}

	h.capture.reset()
	resp, err := h.app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		t.Fatalf("%s %s: status = %d, want a success — the request never reached Proxmox",
			method, target, resp.StatusCode)
	}
	return h.capture.only(t)
}

// cephPoolNameMirror mirrors cephPoolNameParam from internal/api/registry_ceph.go.
// The pattern comes from the shared catalogue and cluster_id from the shared
// standard option, rather than either being retyped here: what these cases are
// validated against is then the route's real vocabulary, not a copy of it that
// drifts the day the rule is widened again.
func cephPoolNameMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"pool_name": {
			Type:      apischema.String,
			Pattern:   apischema.Rule("ceph-pool-name"),
			MaxLength: apischema.Ptr(128),
		},
	})
}

// ipSetEntryMirror mirrors the :name and :cidr parameters of the IP set entry
// delete route in internal/api/registry_firewall.go.
func ipSetEntryMirror(t *testing.T) apischema.Properties {
	t.Helper()
	return compiledMirror(t, apischema.Properties{
		"cluster_id": apischema.StdOption("cluster-id"),
		"name":       {Type: apischema.String, Pattern: apischema.Rule("pve-object-id"), MaxLength: apischema.Ptr(64)},
		"cidr": {
			Type:      apischema.String,
			Pattern:   `^[0-9A-Za-z:%][0-9A-Za-z.:%_-]*$`,
			MaxLength: apischema.Ptr(64),
		},
	})
}

// TestCephPoolDeleteReachesProxmoxAsTheNameTheCallerMeant is the end-to-end
// proof for DELETE /clusters/:cluster_id/ceph/pools/:pool_name.
//
// Each case names a pool, encodes it the way a correct client must, drives the
// real router into the real CephHandler.DeletePool, and asserts on the bytes
// the Proxmox side receives. The four characters that carry the defect —
// "%", "#", "?" and a space — are the ones url.PathEscape encodes, so they are
// the ones that were escaped twice; the rest are here to hold the cases that
// already worked.
//
// A space is worth a word of its own: the catalogued ceph-pool-name rule
// excludes whitespace, and it still reaches the handler, because validation
// runs against the value AS IT ARRIVES — "a%20b" carries no whitespace. That
// ordering is a separate wart and is not what this asserts; the assertion is
// only that whatever does get through arrives at Proxmox unmangled.
func TestCephPoolDeleteReachesProxmoxAsTheNameTheCallerMeant(t *testing.T) {
	h := newPathParamHarness(t, func(app *fiber.App, queries *db.Queries) {
		handler := NewCephHandler(queries, pathParamEncKey, nil)
		app.Delete("/api/v1/clusters/:cluster_id/ceph/pools/:pool_name",
			withRequestParams(t, cephPoolNameMirror(t), []string{"cluster_id", "pool_name"}, handler.DeletePool))
	})

	for _, poolName := range []string{
		"a#b", "a%b", "a?b", "a b", "100%", "a%23b",
		"rbd", "rbd+meta", ".mgr", "pool!1",
	} {
		t.Run(poolName, func(t *testing.T) {
			got := h.send(t, http.MethodDelete,
				cephPoolTarget(h.clusterID.String(), url.PathEscape(poolName)))

			want := "/api2/json/nodes/" + pathParamNode + "/ceph/pool/" + url.PathEscape(poolName)
			if got != want {
				t.Errorf("Proxmox was asked for %q, want %q — the pool destroyed is not the pool named",
					got, want)
			}
		})
	}
}

// TestIPSetEntryDeleteReachesProxmoxAsTheEntryTheCallerMeant is the same proof
// for the sibling route, DELETE
// /clusters/:cluster_id/firewall/ipset/:name/entries/:cidr.
//
// The CIDR cases are the whole point: a slash is not legal unencoded in a path
// segment, so every one of them arrives percent-encoded and every one of them
// used to be re-escaped into an entry id containing a literal "%2F". A bare
// address was the only entry this route could ever remove.
func TestIPSetEntryDeleteReachesProxmoxAsTheEntryTheCallerMeant(t *testing.T) {
	h := newPathParamHarness(t, func(app *fiber.App, queries *db.Queries) {
		handler := NewNetworkHandler(queries, pathParamEncKey, nil)
		app.Delete("/api/v1/clusters/:cluster_id/firewall/ipset/:name/entries/:cidr",
			withRequestParams(t, ipSetEntryMirror(t), []string{"cluster_id", "name", "cidr"}, handler.DeleteFirewallIPSetEntry))
	})

	for _, entry := range []string{
		"192.0.2.0/24", "192.0.2.10", "2001:db8::/32", "2001:db8::1", "198.51.100.0/24",
	} {
		t.Run(entry, func(t *testing.T) {
			got := h.send(t, http.MethodDelete,
				ipSetEntryTarget(h.clusterID.String(), url.PathEscape(entry)))

			want := "/api2/json/cluster/firewall/ipset/storeset/" + url.PathEscape(entry)
			if got != want {
				t.Errorf("Proxmox was asked to remove %q, want %q — the entry removed is not the entry named",
					got, want)
			}
		})
	}
}

// TestPathParamDecodeRefusesWhatItNowMakesReachable is the other half of the
// decode, and the reason it belongs at the handler rather than after the
// client's escape.
//
// Decoding moves the traversal guards onto the string the client will actually
// put in the path. Before it, "%2E%2E" was a pool literally named "%2E%2E" and
// the guard never saw a dot; after it, the same request really does mean "..",
// a dot segment on the wire, and validatePathSegment is what refuses it. A
// malformed escape is refused too: the contract is that a path parameter is
// percent-encoded, so "%zz" is a bad request rather than a pool name to be
// guessed at.
//
// Nothing may reach Proxmox in any of these cases.
func TestPathParamDecodeRefusesWhatItNowMakesReachable(t *testing.T) {
	ceph := newPathParamHarness(t, func(app *fiber.App, queries *db.Queries) {
		handler := NewCephHandler(queries, pathParamEncKey, nil)
		app.Delete("/api/v1/clusters/:cluster_id/ceph/pools/:pool_name",
			withRequestParams(t, cephPoolNameMirror(t), []string{"cluster_id", "pool_name"}, handler.DeletePool))
	})
	ipset := newPathParamHarness(t, func(app *fiber.App, queries *db.Queries) {
		handler := NewNetworkHandler(queries, pathParamEncKey, nil)
		app.Delete("/api/v1/clusters/:cluster_id/firewall/ipset/:name/entries/:cidr",
			withRequestParams(t, ipSetEntryMirror(t), []string{"cluster_id", "name", "cidr"}, handler.DeleteFirewallIPSetEntry))
	})

	tests := []struct {
		name string
		h    *pathParamHarness
		// segment is the last path segment, written exactly as it goes on the
		// wire.
		segment string
		suffix  func(clusterID, segment string) string
	}{
		{"ceph pool, encoded traversal", ceph, "%2E%2E", cephPoolTarget},
		{"ceph pool, encoded slash", ceph, "a%2Fb", cephPoolTarget},
		{"ceph pool, malformed escape", ceph, "a%zzb", cephPoolTarget},
		// A control byte no catalogued pattern can see: the rule matches the
		// value as it ARRIVES, where "%0A" is three ordinary characters. Once
		// decoded it is a newline, and the same string is written into the
		// TrackTask description and the audit row that every Viewer can read.
		{"ceph pool, encoded newline", ceph, "a%0Ab", cephPoolTarget},
		{"ceph pool, encoded NUL", ceph, "a%00b", cephPoolTarget},
		{"ip set entry, encoded traversal", ipset, "%2E%2E", ipSetEntryTarget},
		{"ip set entry, traversal through the slash it now allows", ipset, "%2E%2E%2F%2E%2E", ipSetEntryTarget},
		{"ip set entry, malformed escape", ipset, "a%zzb", ipSetEntryTarget},
		{"ip set entry, encoded newline", ipset, "a%0Ab", ipSetEntryTarget},
		// "Descent" names the shape, not a route: pveproxy joins everything
		// after the set name back into the one cidr value, so it would not go
		// deeper (see proxmox.validatePathSegmentAllowingSlash). The client's
		// one-slash rule is what refuses it.
		{"ip set entry, descent through the slash it now allows", ipset, "a%2Fb%2Fc", ipSetEntryTarget},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.h.capture.reset()

			resp, err := tt.h.app.Test(rawRequest(http.MethodDelete, tt.suffix(tt.h.clusterID.String(), tt.segment)))
			if err != nil {
				t.Fatalf("app.Test: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			// 400 specifically, not "any error": a 404 would mean the route
			// stopped matching, which would refuse every case here for a
			// reason that has nothing to do with the guard under test.
			if resp.StatusCode != fiber.StatusBadRequest {
				t.Errorf("status = %d, want 400 — the value must be refused by a guard, not by the router",
					resp.StatusCode)
			}
			if sent := tt.h.capture.seen(); len(sent) != 0 {
				t.Errorf("the request reached Proxmox as %q; it must be refused before it is sent", sent)
			}
		})
	}

	// The control, without which a mistyped path above would refuse everything
	// for the wrong reason and every case would still pass.
	t.Run("control: an ordinary name still gets through", func(t *testing.T) {
		if got := ceph.send(t, http.MethodDelete, cephPoolTarget(ceph.clusterID.String(), "rbd")); got == "" {
			t.Error("the control request made no Proxmox call")
		}
		if got := ipset.send(t, http.MethodDelete, ipSetEntryTarget(ipset.clusterID.String(), "192.0.2.10")); got == "" {
			t.Error("the control request made no Proxmox call")
		}
	})
}

func cephPoolTarget(clusterID, segment string) string {
	return "/api/v1/clusters/" + clusterID + "/ceph/pools/" + segment
}

func ipSetEntryTarget(clusterID, segment string) string {
	return "/api/v1/clusters/" + clusterID + "/firewall/ipset/storeset/entries/" + segment
}

// rawRequest builds a request whose target goes out on the wire byte for byte.
//
// httptest.NewRequest cannot express the malformed-escape cases: it runs
// url.Parse on the target and PANICS on "a%zzb" — "invalid URL escape". Setting
// URL.Opaque is what gets past that, because URL.RequestURI returns Opaque
// verbatim when it is set, and httputil.DumpRequest (which is how fiber's
// App.Test produces the request line) asks for exactly that. A real client can
// send such a target, and fasthttp accepts it, so the case has to be reachable
// here too.
func rawRequest(method, target string) *http.Request {
	return &http.Request{
		Method:     method,
		URL:        &url.URL{Opaque: target},
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Host:       "nexara.example.com",
	}
}
