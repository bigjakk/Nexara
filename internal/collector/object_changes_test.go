package collector

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// diffCephOSDs and diffCephPools decide what the History shows of a cluster's
// Ceph: what differs between two readings, in the fields that are configuration
// (an OSD's up, in, host and weight; a pool's size, min_size and pg_num) and
// never in the ones that are load.
func TestDiffCephObjects(t *testing.T) {
	t.Parallel()
	osd := func(id int, host string, up, in bool, weight float64) cephOSDMetricSnapshot {
		return cephOSDMetricSnapshot{OSDID: id, OSDName: "osd." + string(rune('0'+id)), Host: host, StatusUp: up, StatusIn: in, CrushWeight: weight}
	}
	pool := func(id int, name string, size, minSize, pgNum int, used int64) cephPoolMetricSnapshot {
		return cephPoolMetricSnapshot{PoolID: id, PoolName: name, Size: size, MinSize: minSize, PGNum: pgNum, BytesUsed: used}
	}
	autoscale := func(p cephPoolMetricSnapshot, mode string, pgNumMin int) cephPoolMetricSnapshot {
		p.PGAutoscale, p.PGNumMin = mode, pgNumMin
		return p
	}
	type want struct{ id, change, node, detail, fields string }
	check := func(t *testing.T, got []objectChange, wants []want) {
		t.Helper()
		have := make([]want, 0, len(got))
		for _, ch := range got {
			raw, _ := json.Marshal(ch.detail)
			have = append(have, want{ch.resourceID, ch.change, ch.node, string(raw), strings.Join(ch.fields, ",")})
		}
		if len(have) == 0 && len(wants) == 0 {
			return
		}
		if !reflect.DeepEqual(have, wants) {
			t.Errorf("changes = %+v\nwant      %+v", have, wants)
		}
	}

	osds := map[string]struct {
		prev, cur map[int]cephOSDMetricSnapshot
		want      []want
	}{
		"nothing differs": {
			prev: map[int]cephOSDMetricSnapshot{0: osd(0, "pve-01", true, true, 1.8)},
			cur:  map[int]cephOSDMetricSnapshot{0: osd(0, "pve-01", true, true, 1.8)},
		},
		"an OSD goes down and out": {
			prev: map[int]cephOSDMetricSnapshot{0: osd(0, "pve-01", true, true, 1.8)},
			cur:  map[int]cephOSDMetricSnapshot{0: osd(0, "pve-01", false, false, 1.8)},
			want: []want{{"0", "changed", "pve-01", `{"in":{"from":true,"to":false},"up":{"from":true,"to":false}}`, "in,up"}},
		},
		"reweighted and moved": {
			prev: map[int]cephOSDMetricSnapshot{0: osd(0, "pve-01", true, true, 1.8)},
			cur:  map[int]cephOSDMetricSnapshot{0: osd(0, "pve-02", true, true, 0.9)},
			want: []want{{"0", "changed", "pve-02", `{"crush_weight":{"from":1.8,"to":0.9},"host":{"from":"pve-01","to":"pve-02"}}`, "crush_weight,host"}},
		},
		"one appears, one is gone": {
			prev: map[int]cephOSDMetricSnapshot{0: osd(0, "pve-01", true, true, 1.8)},
			cur:  map[int]cephOSDMetricSnapshot{1: osd(1, "pve-02", true, true, 1.8)},
			want: []want{
				{"0", "removed", "pve-01", `{"crush_weight":1.8,"host":"pve-01","in":true,"name":"osd.0","up":true}`, ""},
				{"1", "added", "pve-02", `{"crush_weight":1.8,"host":"pve-02","in":true,"name":"osd.1","up":true}`, ""},
			},
		},
	}
	for name, tc := range osds {
		t.Run("osd/"+name, func(t *testing.T) {
			t.Parallel()
			check(t, diffCephOSDs(tc.prev, tc.cur), tc.want)
		})
	}

	pools := map[string]struct {
		prev, cur map[string]cephPoolMetricSnapshot
		want      []want
	}{
		"load is not a change": {
			prev: map[string]cephPoolMetricSnapshot{"pool01": pool(1, "pool01", 3, 2, 32, 100)},
			cur:  map[string]cephPoolMetricSnapshot{"pool01": pool(1, "pool01", 3, 2, 32, 900)},
		},
		"resized": {
			prev: map[string]cephPoolMetricSnapshot{"pool01": pool(1, "pool01", 3, 2, 32, 0)},
			cur:  map[string]cephPoolMetricSnapshot{"pool01": pool(1, "pool01", 2, 1, 64, 0)},
			want: []want{{"pool01", "changed", "", `{"min_size":{"from":2,"to":1},"pg_num":{"from":32,"to":64},"size":{"from":3,"to":2}}`, "min_size,pg_num,size"}},
		},
		"deleted and made again under its name is two things": {
			prev: map[string]cephPoolMetricSnapshot{"pool01": pool(1, "pool01", 3, 2, 32, 0)},
			cur:  map[string]cephPoolMetricSnapshot{"pool01": pool(7, "pool01", 3, 2, 32, 0)},
			want: []want{
				{"pool01", "added", "", `{"min_size":2,"pg_num":32,"pool_id":7,"size":3}`, ""},
				{"pool01", "removed", "", `{"min_size":2,"pg_num":32,"pool_id":1,"size":3}`, ""},
			},
		},
		"pg_num moves under the autoscaler": {
			prev: map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 32, 0), "on", 32)},
			cur:  map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 64, 0), "on", 32)},
		},
		"pg_num edited with the autoscaler off": {
			prev: map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 32, 0), "off", 0)},
			cur:  map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 64, 0), "off", 0)},
			want: []want{{"pool01", "changed", "", `{"pg_num":{"from":32,"to":64}}`, "pg_num"}},
		},
		"pg_num edited with the autoscaler only warning": {
			prev: map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 32, 0), "warn", 0)},
			cur:  map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 64, 0), "warn", 0)},
			want: []want{{"pool01", "changed", "", `{"pg_num":{"from":32,"to":64}}`, "pg_num"}},
		},
		"the autoscaler turned off while it moved pg_num": {
			prev: map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 32, 0), "on", 0)},
			cur:  map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 64, 0), "off", 0)},
			want: []want{{"pool01", "changed", "", `{"pg_autoscale_mode":{"from":"on","to":"off"}}`, "pg_autoscale_mode"}},
		},
		"the autoscaler turned on with a minimum": {
			prev: map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 32, 0), "off", 0)},
			cur:  map[string]cephPoolMetricSnapshot{"pool01": autoscale(pool(1, "pool01", 3, 2, 64, 0), "on", 32)},
			want: []want{{"pool01", "changed", "", `{"pg_autoscale_mode":{"from":"off","to":"on"},"pg_num_min":{"from":0,"to":32}}`, "pg_autoscale_mode,pg_num_min"}},
		},
		"one is gone": {
			prev: map[string]cephPoolMetricSnapshot{"pool01": pool(1, "pool01", 3, 2, 32, 0), "pool02": pool(2, "pool02", 3, 2, 32, 0)},
			cur:  map[string]cephPoolMetricSnapshot{"pool01": pool(1, "pool01", 3, 2, 32, 0)},
			want: []want{{"pool02", "removed", "", `{"min_size":2,"pg_num":32,"pool_id":2,"size":3}`, ""}},
		},
		"one appears": {
			prev: map[string]cephPoolMetricSnapshot{},
			cur:  map[string]cephPoolMetricSnapshot{"pool02": pool(2, "pool02", 3, 2, 32, 0)},
			want: []want{{"pool02", "added", "", `{"min_size":2,"pg_num":32,"pool_id":2,"size":3}`, ""}},
		},
	}
	for name, tc := range pools {
		t.Run("pool/"+name, func(t *testing.T) {
			t.Parallel()
			check(t, diffCephPools(tc.prev, tc.cur), tc.want)
		})
	}
}

// cephFakeClient answers the three reads syncCeph makes.
type cephFakeClient struct {
	ProxmoxClient
	osds    *proxmox.CephOSDResponse
	osdErr  error
	pools   []proxmox.CephPool
	poolErr error
}

func (c *cephFakeClient) GetCephStatus(context.Context, string) (*proxmox.CephStatus, error) {
	return &proxmox.CephStatus{}, nil
}

func (c *cephFakeClient) GetCephOSDs(context.Context, string) (*proxmox.CephOSDResponse, error) {
	return c.osds, c.osdErr
}

func (c *cephFakeClient) GetCephPools(context.Context, string) ([]proxmox.CephPool, error) {
	return c.pools, c.poolErr
}

func cephTree(downIDs ...int) *proxmox.CephOSDResponse {
	in := proxmox.FlexBool(true)
	var osds []proxmox.CephOSDTreeNode
	for id := 0; id < 2; id++ {
		status := "up"
		for _, d := range downIDs {
			if d == id {
				status = "down"
			}
		}
		osds = append(osds, proxmox.CephOSDTreeNode{ID: proxmox.FlexInt(id), Name: "osd." + string(rune('0'+id)), Type: "osd", Status: status, In: &in, CrushWeight: 1.8})
	}
	return &proxmox.CephOSDResponse{Root: proxmox.CephOSDTreeNode{
		Type: "root", Children: []proxmox.CephOSDTreeNode{{Type: "host", Name: "pve-01", Children: osds}},
	}}
}

func cephPools(size int) []proxmox.CephPool {
	return []proxmox.CephPool{{PoolName: "pool01", Pool: 1, Size: proxmox.FlexInt(size), MinSize: 2, PGNum: 32}}
}

// TestSyncCephRecordsWhatChanged follows one cluster's Ceph pass by pass: the
// first reading is the baseline and records nothing; an unchanged one records
// nothing; what changed is recorded against the reading before; and a read that
// failed, or came back empty after a populated one, is "could not look" — it
// records no removals and leaves the baseline where it was.
func TestSyncCephRecordsWhatChanged(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	mq := newMockQueries()
	s := &Syncer{queries: mq, logger: testLogger()}
	client := &cephFakeClient{osds: cephTree(), pools: cephPools(3)}
	pass := func() []string {
		before := len(mq.objectChanges)
		s.syncCeph(context.Background(), client, clusterID, "pve-01")
		got := make([]string, 0, len(mq.objectChanges)-before)
		for _, ch := range mq.objectChanges[before:] {
			got = append(got, ch.ResourceType+"/"+ch.ResourceID+" "+ch.Change+" "+ch.Node)
		}
		return got
	}
	expect := func(step string, got []string, want ...string) {
		t.Helper()
		if !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
			t.Errorf("%s: recorded %v, want %v", step, got, want)
		}
	}

	expect("first reading is the baseline", pass())
	expect("an unchanged reading", pass())

	client.osds, client.pools = cephTree(1), cephPools(2)
	expect("an OSD down and a pool resized", pass(), "ceph_osd/1 changed pve-01", "ceph_pool/pool01 changed ")
	if got := mq.objectChanges[len(mq.objectChanges)-2]; got.ClusterID != clusterID || got.LastSeenAt.IsZero() ||
		got.ObservedBy != "ceph" || !reflect.DeepEqual(got.Fields, []string{"up"}) {
		t.Errorf("the change is not tied to its cluster, the reading before and the field that differs: %+v", got)
	}

	client.osdErr, client.osds = errors.New("timeout"), nil
	expect("an OSD list that could not be read", pass())
	client.osdErr, client.osds = nil, &proxmox.CephOSDResponse{}
	expect("an OSD list that came back empty", pass())

	client.osds = cephTree()
	expect("the OSD back up, against the last good reading", pass(), "ceph_osd/1 changed pve-01")
}

// syncReplication marks a cluster's replication as read once the listing was
// written whole — after which the triggers record a job appearing — and not for
// a pass that failed to write a job, which would then land as new.
func TestSyncReplicationMarksTheBaselineOnceWritten(t *testing.T) {
	t.Parallel()
	clusterID := uuid.New()
	client := &replFakeClient{jobs: []proxmox.ReplicationJob{
		{ID: "100-0", Guest: 100, Target: "pve-02", Schedule: "*/15", Rate: "10", Disable: 1, Comment: "nightly"},
	}}

	t.Run("a whole listing", func(t *testing.T) {
		t.Parallel()
		mq := &mockQueries{}
		s := &Syncer{queries: mq, logger: testLogger()}
		s.syncReplication(context.Background(), client, clusterID)
		s.syncReplication(context.Background(), client, clusterID)
		if len(mq.baselineMarks) != 1 || mq.baselineMarks[0].ClusterID != clusterID || mq.baselineMarks[0].ResourceType != "replication" {
			t.Errorf("baseline marks = %+v, want one for this cluster's replication", mq.baselineMarks)
		}
		if got := mq.replicationUpserts[0]; got.Schedule != "*/15" || got.Rate != "10" || got.Disable != 1 || got.Comment != "nightly" {
			t.Errorf("the job's configuration did not reach the upsert: %+v", got)
		}
	})

	t.Run("a job that failed to write", func(t *testing.T) {
		t.Parallel()
		mq := &mockQueries{replicationUpsertErr: errors.New("db down")}
		s := &Syncer{queries: mq, logger: testLogger()}
		s.syncReplication(context.Background(), client, clusterID)
		if len(mq.baselineMarks) != 0 {
			t.Errorf("baseline marked after a failed write: %+v", mq.baselineMarks)
		}
	})
}

// An object adds at most maxObjectChangesPerHour rows an hour, however often it
// changes: the count is per object and starts over an hour after its first row.
func TestAllowObjectChangeCapsEachObjectPerHour(t *testing.T) {
	t.Parallel()
	s := &Syncer{logger: testLogger()}
	cluster := uuid.New()
	start := time.Now()
	allowed := func(id string, at time.Time) bool {
		ok, _ := s.allowObjectChange(cluster, "ceph_osd", id, at)
		return ok
	}
	for i := 0; i < maxObjectChangesPerHour; i++ {
		if !allowed("1", start.Add(time.Duration(i)*time.Minute)) {
			t.Fatalf("row %d of an hour was refused, want the first %d allowed", i+1, maxObjectChangesPerHour)
		}
	}
	if allowed("1", start.Add(50*time.Minute)) {
		t.Errorf("row %d of the hour was allowed", maxObjectChangesPerHour+1)
	}
	if _, firstOver := s.allowObjectChange(cluster, "ceph_osd", "1", start.Add(51*time.Minute)); firstOver {
		t.Error("the warning is wanted once an hour per object, not for every refused row")
	}
	if !allowed("2", start.Add(50*time.Minute)) {
		t.Error("another object was refused for the first one's flapping")
	}
	if !allowed("1", start.Add(61*time.Minute)) {
		t.Error("the first object is still refused an hour after its first row")
	}
}

// recordObjectChanges stops filing an object's rows at the hourly cap.
func TestRecordObjectChangesStopsAtTheCap(t *testing.T) {
	t.Parallel()
	mq := newMockQueries()
	s := &Syncer{queries: mq, logger: testLogger()}
	changes := make([]objectChange, 0, maxObjectChangesPerHour+5)
	for i := 0; i < maxObjectChangesPerHour+5; i++ {
		changes = append(changes, objectChange{resourceID: "7", change: "changed", fields: []string{"up"}, detail: map[string]any{}})
	}
	s.recordObjectChanges(context.Background(), uuid.New(), "ceph_osd", changes, time.Now())
	if len(mq.objectChanges) != maxObjectChangesPerHour {
		t.Errorf("%d rows filed for one object in an hour, want the cap of %d", len(mq.objectChanges), maxObjectChangesPerHour)
	}
}
