package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// metricsSeriesDB stands in for the database behind the metric routes: it
// holds the node and guest rows the handlers check cluster membership against,
// answers each series query with its rows in series (none when unset), and
// records which ones ran and with what arguments.
type metricsSeriesDB struct {
	clusterID uuid.UUID
	series    map[string][]any
	mu        sync.Mutex
	ran       []string
	args      map[string][]any
}

func (d *metricsSeriesDB) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(sql, "-- name: GetNode :one"):
		return structRow{db.Node{ClusterID: d.clusterID}}
	case strings.Contains(sql, "-- name: GetVM :one"):
		return structRow{db.Vm{ClusterID: d.clusterID}}
	}
	return failedRow{fmt.Errorf("%w: %.60q", errUnexpectedStatement, sql)}
}

func (d *metricsSeriesDB) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	name := stmtName(sql)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ran = append(d.ran, name)
	if d.args == nil {
		d.args = map[string][]any{}
	}
	d.args[name] = args
	return &metricRows{rows: d.series[name]}, nil
}

func (*metricsSeriesDB) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, fmt.Errorf("%w: %.60q", errUnexpectedStatement, sql)
}

func (d *metricsSeriesDB) queries() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.ran)
}

// metricRows replays generated row structs through pgx.Rows, each as
// structRow does.
type metricRows struct {
	rows []any
	i    int
}

func (r *metricRows) Next() bool                                 { r.i++; return r.i <= len(r.rows) }
func (r *metricRows) Scan(dest ...any) error                     { return structRow{r.rows[r.i-1]}.Scan(dest...) }
func (*metricRows) Close()                                       {}
func (*metricRows) Err() error                                   { return nil }
func (*metricRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (*metricRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (*metricRows) Values() ([]any, error)                       { return nil, nil }
func (*metricRows) RawValues() [][]byte                          { return nil }
func (*metricRows) Conn() *pgx.Conn                              { return nil }

// TestMetricRangesReadTheirSource drives the real handlers through their real
// declarations: the short ranges read the raw samples, which is what lets a
// chart show a stall of a minute or two, 24h the 5-minute rollup and 7d the
// hourly one.
func TestMetricRangesReadTheirSource(t *testing.T) {
	t.Parallel()
	nodeID, vmID := uuid.NewString(), uuid.NewString()
	h := func(d *metricsSeriesDB) *handlers.MetricsHandler { return handlers.NewMetricsHandler(db.New(d)) }
	routes := []struct {
		path    string
		handler func(*handlers.MetricsHandler) Handler
		source  map[string]string
	}{
		{clusterScope + "/metrics",
			func(m *handlers.MetricsHandler) Handler { return m.GetClusterHistorical },
			map[string]string{"1h": "GetClusterNodeMetricsRaw", "6h": "GetClusterNodeMetricsRaw",
				"24h": "GetClusterNodeMetrics5m", "7d": "GetClusterNodeMetrics1h"}},
		{clusterScope + "/nodes/:node_id/metrics",
			func(m *handlers.MetricsHandler) Handler { return m.GetNodeHistorical },
			map[string]string{"1h": "GetNodeRecentMetrics", "6h": "GetNodeRecentMetrics",
				"24h": "GetNodeMetrics5m", "7d": "GetNodeMetrics1h"}},
		{clusterScope + "/vms/:vm_id/metrics",
			func(m *handlers.MetricsHandler) Handler { return m.GetVMHistorical },
			map[string]string{"1h": "GetVMRecentMetrics", "6h": "GetVMRecentMetrics",
				"24h": "GetVMMetrics5m", "7d": "GetVMMetrics1h"}},
		{clusterScope + "/nodes/:node_id/pressure",
			func(m *handlers.MetricsHandler) Handler { return m.GetNodePressure },
			map[string]string{"1h": "GetNodePressure", "7d": "GetNodePressure"}},
		{clusterScope + "/vms/:vm_id/pressure",
			func(m *handlers.MetricsHandler) Handler { return m.GetVMPressure },
			map[string]string{"1h": "GetVMPressure", "7d": "GetVMPressure"}},
	}
	for _, r := range routes {
		for rng, want := range r.source {
			d := &metricsSeriesDB{clusterID: uuid.MustParse(testClusterID)}
			e := declaredEndpoint(t, fiber.MethodGet, r.path)
			e.Handler = r.handler(h(d))
			e.Permissions = Permissions{SelfService: "source fixture; authorization is exercised separately"}
			app := newRegistryApp(t, noAuth(), e)

			target := strings.NewReplacer(":cluster_id", testClusterID, ":node_id", nodeID, ":vm_id", vmID).Replace(r.path)
			status, env := send(t, app, httptest.NewRequest(http.MethodGet, target+"?range="+rng, nil))
			if status != fiber.StatusOK {
				t.Errorf("%s ?range=%s: status %d (%q), want 200", r.path, rng, status, env.Message)
				continue
			}
			if got := d.queries(); !slices.Equal(got, []string{want}) {
				t.Errorf("%s ?range=%s read %v, want [%s]", r.path, rng, got, want)
			}
		}
	}
}

// TestGuestSeriesIsRatedOldestFirstAsAGuest drives a guest's 1h series through
// the real handler with rows as the alert engine's query returns them, newest
// first: they must be rated oldest first, with a running guest's zero counter
// taken as unreported, from a fetch reaching counterReach before the window.
func TestGuestSeriesIsRatedOldestFirstAsAGuest(t *testing.T) {
	t.Parallel()
	vmID := uuid.NewString()
	now := time.Now().Truncate(10 * time.Second)
	row := func(ago int, read int64) any {
		return db.GetVMRecentMetricsRow{Bucket: now.Add(-time.Duration(ago) * time.Second),
			CpuUsage: 0.5, MemUsed: 1, MemTotal: 4, DiskRead: read}
	}
	d := &metricsSeriesDB{
		clusterID: uuid.MustParse(testClusterID),
		// 1000, then a running guest's 0 (unreported), a fall to 500, and 1500.
		series: map[string][]any{"GetVMRecentMetrics": {row(10, 1500), row(20, 500), row(30, 0), row(40, 1000)}},
	}
	path := clusterScope + "/vms/:vm_id/metrics"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	e.Handler = handlers.NewMetricsHandler(db.New(d)).GetVMHistorical
	e.Permissions = Permissions{SelfService: "series fixture; authorization is exercised separately"}
	app := newRegistryApp(t, noAuth(), e)

	target := strings.NewReplacer(":cluster_id", testClusterID, ":vm_id", vmID).Replace(path)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, target+"?range=1h", nil))
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Items []struct {
			DiskReadBps *float64 `json:"diskReadBps"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var got []any
	for _, p := range body.Items {
		if p.DiskReadBps == nil {
			got = append(got, nil)
		} else {
			got = append(got, *p.DiskReadBps)
		}
	}
	// The fall to 500 has no rate; 1500 is measured from 1000, 30 s before.
	if want := []any{nil, nil, nil, 17.0}; !slices.Equal(got, want) {
		t.Errorf("disk read rates = %v, want %v", got, want)
	}
	d.mu.Lock()
	since, _ := d.args["GetVMRecentMetrics"][1].(time.Time)
	d.mu.Unlock()
	if reach := time.Until(since); reach > -(time.Hour + 10*time.Minute) {
		t.Errorf("the raw fetch starts %v before now, want at least 1h10m: counterReach before the window", -reach)
	}
}

// TestPressureSeriesMapsEachFigure drives both pressure routes with one row
// whose figures are all distinct, so a figure answered in another's field
// shows.
func TestPressureSeriesMapsEachFigure(t *testing.T) {
	t.Parallel()
	f := func(v float32) pgtype.Float4 { return pgtype.Float4{Float32: v, Valid: true} }
	at := time.Now().Add(-time.Minute)
	for _, tt := range []struct {
		path, query string
		handler     func(*handlers.MetricsHandler) Handler
		row         any
		want        string
	}{
		{clusterScope + "/nodes/:node_id/pressure", "GetNodePressure",
			func(m *handlers.MetricsHandler) Handler { return m.GetNodePressure },
			db.GetNodePressureRow{Bucket: at, CpuSome: f(1), IoSome: f(3), IoFull: f(4), MemSome: f(5), MemFull: f(6)},
			`"cpuSome":1,"cpuFull":null,"ioSome":3,"ioFull":4,"memSome":5,"memFull":6`},
		{clusterScope + "/vms/:vm_id/pressure", "GetVMPressure",
			func(m *handlers.MetricsHandler) Handler { return m.GetVMPressure },
			db.GetVMPressureRow{Bucket: at, CpuSome: f(1), CpuFull: f(2), IoSome: f(3), IoFull: f(4), MemSome: f(5), MemFull: f(6)},
			`"cpuSome":1,"cpuFull":2,"ioSome":3,"ioFull":4,"memSome":5,"memFull":6`},
	} {
		d := &metricsSeriesDB{clusterID: uuid.MustParse(testClusterID), series: map[string][]any{tt.query: {tt.row}}}
		e := declaredEndpoint(t, fiber.MethodGet, tt.path)
		e.Handler = tt.handler(handlers.NewMetricsHandler(db.New(d)))
		e.Permissions = Permissions{SelfService: "series fixture; authorization is exercised separately"}
		app := newRegistryApp(t, noAuth(), e)

		target := strings.NewReplacer(":cluster_id", testClusterID, ":node_id", uuid.NewString(), ":vm_id", uuid.NewString()).Replace(tt.path)
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, target+"?range=1h", nil))
		if err != nil {
			t.Fatalf("app.Test: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if !strings.Contains(string(body), tt.want) {
			t.Errorf("%s answered %s, want the figures %s", tt.path, body, tt.want)
		}
	}
}
