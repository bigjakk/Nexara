package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The bug this guards: GetHistorical used to hand the raw ceph_cluster_metrics
// hypertable straight to the client. At the 10s collection interval that
// docker-compose ships, a 7-day window is tens of thousands of rows — and
// CephMetricsChart draws that into four charts on a 60s refetch. The PBS equivalent produced a 6.4MB response that
// froze the whole SPA for ~10s. Bucketing is what keeps the response bounded,
// so the invariant worth pinning is the point count, not the constants.
//
// This ranges over the timeframe table rather than restating it, so a
// timeframe added later is held to the same bound without a new test row.
func TestCephMetricsWindowBounded(t *testing.T) {
	// A few hundred points is already finer than the pixels available to draw
	// them; well past that and we are shipping data no one can see.
	const maxPointsPerSeries = 256

	for timeframe := range cephMetricsTimeframes {
		t.Run(timeframe, func(t *testing.T) {
			window, bucketSeconds := cephMetricsWindow(timeframe)

			if bucketSeconds <= 0 {
				// time_bucket() rejects a zero or negative width outright, so
				// this would be a 500 on every request rather than a silent
				// fallback to raw rows.
				t.Fatalf("bucketSeconds = %d, must be positive", bucketSeconds)
			}
			if window <= 0 {
				t.Fatalf("window = %v, must be positive", window)
			}

			points := int64(window/time.Second) / int64(bucketSeconds)
			if points > maxPointsPerSeries {
				t.Errorf("timeframe %q yields %d points "+
					"(window %v / bucket %ds), want <= %d",
					timeframe, points, window, bucketSeconds,
					maxPointsPerSeries)
			}
		})
	}
}

// An unrecognised ?timeframe= must still resolve to a bounded window — the
// query is driven by whatever the client sends. The default doubles as the
// documented value for a missing parameter, so it has to be a real key.
func TestCephMetricsWindowFallback(t *testing.T) {
	fallback, ok := cephMetricsTimeframes[CephMetricsDefaultTimeframe]
	if !ok {
		t.Fatalf("default timeframe %q is not in the table",
			CephMetricsDefaultTimeframe)
	}

	for _, timeframe := range []string{"", "not-a-timeframe", "1H", "30d"} {
		t.Run("falls back: "+timeframe, func(t *testing.T) {
			window, bucketSeconds := cephMetricsWindow(timeframe)
			if window != fallback.window || bucketSeconds != fallback.bucketSeconds {
				t.Errorf("cephMetricsWindow(%q) = (%v, %d), want (%v, %d)",
					timeframe, window, bucketSeconds,
					fallback.window, fallback.bucketSeconds)
			}
		})
	}
}

// TestCephMetricsTimeframesCoverTheTable holds the exported enum and the
// window table together.
//
// CephMetricsTimeframes is what the endpoint declaration in
// internal/api/registry_ceph.go validates ?timeframe= against, and
// cephMetricsTimeframes is what cephMetricsWindow looks the value up in.
// Two lists, one vocabulary: a timeframe added to the table but not the
// slice is unreachable through the API, and one added to the slice but not
// the table passes validation and then silently serves the default window.
// Both directions fail here.
func TestCephMetricsTimeframesCoverTheTable(t *testing.T) {
	for _, tf := range CephMetricsTimeframes {
		if _, ok := cephMetricsTimeframes[tf]; !ok {
			t.Errorf("CephMetricsTimeframes offers %q, but cephMetricsTimeframes has no window for it — "+
				"the schema would accept it and cephMetricsWindow would silently serve the default", tf)
		}
	}
	for tf := range cephMetricsTimeframes {
		if !slices.Contains(CephMetricsTimeframes, tf) {
			t.Errorf("cephMetricsTimeframes defines a window for %q, but CephMetricsTimeframes does not "+
				"offer it — the schema's enum makes it unreachable", tf)
		}
	}
	if !slices.Contains(CephMetricsTimeframes, CephMetricsDefaultTimeframe) {
		t.Errorf("the default timeframe %q is not in CephMetricsTimeframes, so the declaration's "+
			"Default would fail its own enum", CephMetricsDefaultTimeframe)
	}
}

// TestCephOSDActionsCoverTheDisruptiveSet keeps the exported action
// vocabulary honest against the two places that reason about an action.
//
// CephOSDActions is what the pre-flight endpoint validates ?action=
// against; cephDisruptiveOSDActions decides whether that action gets a
// redundancy warning, and osdServingAfter decides what the cluster looks
// like afterwards. An action the enum accepts but osdServingAfter has no
// case for falls to the default branch and is graded as if nothing
// happened — an advisory check that answers "ok" to an action it does not
// understand.
func TestCephOSDActionsCoverTheDisruptiveSet(t *testing.T) {
	for action := range cephDisruptiveOSDActions {
		if !slices.Contains(CephOSDActions, action) {
			t.Errorf("cephDisruptiveOSDActions names %q, which CephOSDActions does not offer — "+
				"the warning it carries is unreachable", action)
		}
	}

	// osdServingAfter has no exported shape to inspect, so this drives it:
	// every declared action must produce a verdict that depends on the
	// action rather than on the OSD's current state alone. A serving OSD
	// is the input, so anything disruptive must answer false and anything
	// else must answer true.
	serving := cephOSDResponse{ID: 1, Up: 1, In: 1, Host: "pve-01"}
	for _, action := range CephOSDActions {
		got := osdServingAfter(serving, action)
		if want := !cephDisruptiveOSDActions[action]; got != want {
			t.Errorf("osdServingAfter(serving, %q) = %v, want %v — the action is %sdisruptive",
				action, got, want, map[bool]string{true: "", false: "not "}[cephDisruptiveOSDActions[action]])
		}
	}
}

// cephNodesDBTX is pathParamDBTX with a node list of its own.
type cephNodesDBTX struct {
	pathParamDBTX
	nodes []db.Node
}

func (d cephNodesDBTX) Query(_ context.Context, sql string, _ ...any) (pgx.Rows, error) {
	if !strings.Contains(sql, "FROM nodes") {
		return nil, errPathParamUnexpectedQuery
	}
	return &nodeRows{rows: d.nodes}, nil
}

// cephCacheQueries is pathParamCacheQueries with the member endpoints the
// cache fails over between.
type cephCacheQueries struct {
	pathParamCacheQueries
	endpoints []db.ListNodeEndpointsRow
}

func (q cephCacheQueries) ListNodeEndpoints(context.Context, uuid.UUID) ([]db.ListNodeEndpointsRow, error) {
	return q.endpoints, nil
}

// cephMember is a stand-in Proxmox node: it records each path asked of it and
// answers those in reply, 595 (pveproxy failing to forward) for any other.
func cephMember(t *testing.T, srv *httptest.Server, reply map[string]string) *proxmoxCapture {
	t.Helper()
	capture := &proxmoxCapture{}
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.record(r.URL.Path, nil)
		body, ok := reply[r.URL.Path]
		if !ok {
			w.WriteHeader(595)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":` + body + `}`))
	})
	t.Cleanup(srv.Close)
	return capture
}

// cephReadApp mounts the Ceph routes these tests drive on a fresh app with a
// fresh client cache for a cluster at apiURL.
func cephReadApp(t *testing.T, apiURL string, nodes []db.Node, endpoints []db.ListNodeEndpointsRow) (*fiber.App, uuid.UUID) {
	t.Helper()
	encrypted, err := crypto.Encrypt("token-secret-value", pathParamEncKey)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	cluster := db.Cluster{ID: uuid.New(), Name: "cluster01", ApiUrl: apiURL,
		TokenID: "user@pam!test", TokenSecretEncrypted: encrypted, IsActive: true}
	cache := proxmox.NewClientCache(cephCacheQueries{pathParamCacheQueries{cluster: cluster}, endpoints},
		pathParamEncKey, nil, nil)
	h := NewCephHandler(db.New(cephNodesDBTX{nodes: nodes}), pathParamEncKey, nil)

	clusterOnly := apischema.Properties{"cluster_id": apischema.StdOption("cluster-id")}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error { SetProxmoxCacheLocal(c, cache); return c.Next() })
	base := "/api/v1/clusters/:cluster_id/ceph"
	app.Get(base+"/status", withRequestParams(t, clusterOnly, []string{"cluster_id"}, h.GetStatus))
	app.Get(base+"/osds", withRequestParams(t, clusterOnly, []string{"cluster_id"}, h.ListOSDs))
	app.Delete(base+"/pools/:pool_name", withRequestParams(t, cephPoolNameMirror(t),
		[]string{"cluster_id", "pool_name"}, h.DeletePool))
	return app, cluster.ID
}

func cephCall(t *testing.T, app *fiber.App, method, target string) (int, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(method, target, nil), fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

const cephStatusFixture = `{"health":{"status":"HEALTH_WARN"},"monmap":{"num_mons":3},
	"osdmap":{"num_osds":3,"num_up_osds":2,"num_in_osds":3},
	"pgmap":{"num_pgs":97,"pgs_by_state":[{"state_name":"active+undersized+degraded","count":16},
	{"state_name":"active+clean","count":81}]}}`

// D5: the node behind the cluster's api_url reboots while the database still
// calls it online. A read that finds the endpoint gone takes the client from
// the cache once more, which now points at the member the cache chose, and a
// write is not re-sent anywhere. Each row gets a fresh cache.
func TestCephReadsSurviveTheEndpointNodeGoingDown(t *testing.T) {
	t.Parallel()
	// The cache fails over only for an api_url on pveproxy's port, so the
	// member listens on 127.0.0.2:8006 and the endpoint is 127.0.0.3:8006,
	// where nothing listens.
	ln, err := net.Listen("tcp", "127.0.0.2:8006")
	if err != nil {
		// Free on CI by construction; a developer machine may hold the port.
		if os.Getenv("CI") != "" {
			t.Fatalf("cannot listen on 127.0.0.2:8006: %v", err)
		}
		t.Skipf("cannot listen on 127.0.0.2:8006: %v", err)
	}
	srv := httptest.NewUnstartedServer(nil)
	_ = srv.Listener.Close()
	srv.Listener = ln
	member := cephMember(t, srv, map[string]string{
		"/api2/json/cluster/ceph/status":   cephStatusFixture,
		"/api2/json/nodes/pve-02/ceph/osd": `{"root":{"type":"root","children":[]}}`,
	})
	srv.StartTLS()
	sum := sha256.Sum256(srv.Certificate().Raw)
	nodes := []db.Node{{Name: "pve-01", Status: "online"}, {Name: "pve-02", Status: "online"}}
	endpoints := []db.ListNodeEndpointsRow{
		{Name: "pve-01", Address: "127.0.0.3", SslFingerprint: strings.Repeat("AA", 32), Status: "online"},
		{Name: "pve-02", Address: "127.0.0.2", SslFingerprint: hex.EncodeToString(sum[:]), Status: "online"},
	}

	for _, tt := range []struct {
		method, route string
		wantPaths     []string
		wantStatus    int
		wantBody      string // D9: the PG states reach the answer as Proxmox gave them
	}{
		{http.MethodGet, "/status", []string{"/api2/json/cluster/ceph/status"}, http.StatusOK,
			`"num_pgs":97,"pgs_by_state":[{"state_name":"active+undersized+degraded","count":16},` +
				`{"state_name":"active+clean","count":81}]`},
		// Through the member, pve-01 is a node it cannot forward to (595).
		{http.MethodGet, "/osds", []string{"/api2/json/nodes/pve-01/ceph/osd", "/api2/json/nodes/pve-02/ceph/osd"},
			http.StatusOK, ""},
		{http.MethodDelete, "/pools/store01", nil, http.StatusBadGateway, ""},
	} {
		app, clusterID := cephReadApp(t, "https://127.0.0.3:8006", nodes, endpoints)
		target := "/api/v1/clusters/" + clusterID.String() + "/ceph" + tt.route
		member.reset()
		status, body := cephCall(t, app, tt.method, target)
		if status != tt.wantStatus {
			t.Errorf("%s %s = %d %s, want %d", tt.method, tt.route, status, body, tt.wantStatus)
		}
		if !strings.Contains(body, tt.wantBody) {
			t.Errorf("%s %s answered %s, want it to hold %s", tt.method, tt.route, body, tt.wantBody)
		}
		if got := member.seen(); !slices.Equal(got, tt.wantPaths) {
			t.Errorf("%s %s: the member was asked %q, want %q", tt.method, tt.route, got, tt.wantPaths)
		}
	}
}

// The endpoint is up but the node in the path is not: pveproxy answers 595, and
// the read is asked again about the next node through the same endpoint.
func TestCephReadMovesOnFromANodeTheEndpointCannotReach(t *testing.T) {
	t.Parallel()
	srv := httptest.NewUnstartedServer(nil)
	endpoint := cephMember(t, srv, map[string]string{
		"/api2/json/nodes/pve-02/ceph/osd": `{"root":{"type":"root","children":[]}}`,
	})
	srv.Start()
	app, clusterID := cephReadApp(t, srv.URL, []db.Node{
		{Name: "pve-01", Status: "online"}, {Name: "pve-02", Status: "online"},
	}, nil)

	status, body := cephCall(t, app, http.MethodGet, "/api/v1/clusters/"+clusterID.String()+"/ceph/osds")
	if status != http.StatusOK {
		t.Errorf("status = %d %s, want 200", status, body)
	}
	want := []string{"/api2/json/nodes/pve-01/ceph/osd", "/api2/json/nodes/pve-02/ceph/osd"}
	if got := endpoint.seen(); !slices.Equal(got, want) {
		t.Errorf("Proxmox was asked %q, want %q", got, want)
	}
}
