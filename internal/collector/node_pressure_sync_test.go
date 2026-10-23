package collector

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestSyncAllNodePressure drives the pass against a fake PVE: a node with
// nothing stored reads the RRD's day and one with a recent step its hour;
// only steps newer than the newest stored, and carrying a pressure value, are
// inserted; a node whose RRD read fails leaves the others to finish; and an
// offline node is not read at all.
func TestSyncAllNodePressure(t *testing.T) {
	t.Parallel()
	newest := time.Now().Add(-5 * time.Minute).Truncate(time.Minute)
	// A step carrying v in every series, each offset so a swapped field shows.
	step := func(at time.Time, v string) json.RawMessage {
		if v == "" {
			return json.RawMessage(`{"time":` + strconv.FormatInt(at.Unix(), 10) + `}`)
		}
		return json.RawMessage(`{"time":` + strconv.FormatInt(at.Unix(), 10) + `,"pressurecpusome":"` + v + `1",` +
			`"pressureiosome":` + v + `2,"pressureiofull":` + v + `3,"pressurememorysome":` + v + `4,"pressurememoryfull":` + v + `5}`)
	}

	var mu sync.Mutex
	timeframes := map[string]string{}
	rrd := func(node string, steps ...json.RawMessage) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			timeframes[node] = r.URL.Query().Get("timeframe")
			mu.Unlock()
			jsonResponse(w, steps)
		}
	}
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/rrddata": rrd("pve1",
			step(newest.Add(-2*time.Minute), "1"), step(newest.Add(-time.Minute), "2")),
		"/api2/json/nodes/pve2/rrddata": rrd("pve2",
			step(newest, "9"), step(newest.Add(time.Minute), "3"), step(newest.Add(2*time.Minute), "")),
		// pve3's RRD read fails (no route); pve4 has one but is offline.
		"/api2/json/nodes/pve4/rrddata": rrd("pve4", step(newest, "4")),
	})
	defer srv.Close()

	cluster := makeCluster(t, srv.URL)
	// The failing node first, so its failure has nodes left to stop.
	nodes := []db.Node{
		{ID: uuid.New(), ClusterID: cluster.ID, Name: "pve3", Status: "online"},
		{ID: uuid.New(), ClusterID: cluster.ID, Name: "pve1", Status: "online"},
		{ID: uuid.New(), ClusterID: cluster.ID, Name: "pve2", Status: "online"},
		{ID: uuid.New(), ClusterID: cluster.ID, Name: "pve4", Status: "offline"},
	}
	mq := newMockQueries()
	mq.clusters = []db.Cluster{cluster}
	mq.nodesByCluster = nodes
	mq.nodePressureNewest = map[uuid.UUID]time.Time{nodes[2].ID: newest}

	newTestSyncer(mq).SyncAllNodePressure(context.Background())

	if timeframes["pve1"] != "day" || timeframes["pve2"] != "hour" {
		t.Errorf("timeframes read = %v, want day for a node with nothing stored, hour for one with a recent step", timeframes)
	}
	if _, read := timeframes["pve4"]; read {
		t.Error("the offline node's RRD was read")
	}
	f4 := func(v float32) pgtype.Float4 { return pgtype.Float4{Float32: v, Valid: true} }
	want := func(node uuid.UUID, at time.Time, v float32) db.InsertNodePressureParams {
		return db.InsertNodePressureParams{Time: at, NodeID: node, CpuSome: f4(v*10 + 1),
			IoSome: f4(v*10 + 2), IoFull: f4(v*10 + 3), MemSome: f4(v*10 + 4), MemFull: f4(v*10 + 5)}
	}
	// pve1's two steps, and pve2's one newer than stored that has a value.
	wantRows := []db.InsertNodePressureParams{
		want(nodes[1].ID, newest.Add(-2*time.Minute), 1), want(nodes[1].ID, newest.Add(-time.Minute), 2),
		want(nodes[2].ID, newest.Add(time.Minute), 3),
	}
	if !reflect.DeepEqual(mq.nodePressureInserts, wantRows) {
		t.Errorf("inserted %+v\nwant     %+v", mq.nodePressureInserts, wantRows)
	}
}
