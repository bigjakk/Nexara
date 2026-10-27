package collector

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
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

// An OSD that went out is recorded with whether the cluster had noout when it did, at
// that reading or the one before: Ceph marks a down OSD out by itself only while noout
// is off, so the classifier explains an out as Ceph's own only when it is recorded off.
func TestSyncCephRecordsTheNooutFlagOnAnOSDThatWentOut(t *testing.T) {
	t.Parallel()
	mq := newMockQueries()
	s := &Syncer{queries: mq, logger: testLogger()}
	client := &cephFakeClient{pools: cephPools(3)}
	clusterID := uuid.New()
	pass := func(osd1In bool, flags string) {
		tree := cephTree()
		in := proxmox.FlexBool(osd1In)
		tree.Root.Children[0].Children[1].In = &in
		tree.Flags = flags
		client.osds = tree
		s.syncCeph(context.Background(), client, clusterID, "pve-01")
	}
	noout := func(row int) any {
		var detail map[string]map[string]any
		if err := json.Unmarshal(mq.objectChanges[row].Detail, &detail); err != nil {
			t.Fatalf("detail: %v", err)
		}
		return detail["in"]["noout"]
	}
	pass(true, "")                   // the baseline
	pass(false, "noout,sortbitwise") // out with noout set
	pass(true, "sortbitwise")        // back in (a change to in, which carries no noout)
	pass(false, "sortbitwise")       // out, neither reading had noout
	pass(true, "noout")
	pass(false, "sortbitwise") // out, the reading before had noout
	if len(mq.objectChanges) != 5 {
		t.Fatalf("%d rows recorded, want the five ins and outs", len(mq.objectChanges))
	}
	for row, want := range map[int]any{0: true, 2: false, 4: true} {
		if got := noout(row); got != want {
			t.Errorf("out #%d: noout = %v, want %v", row/2+1, got, want)
		}
	}
	if got := noout(1); got != nil {
		t.Errorf("an OSD that came back in carries noout = %v: only an out does", got)
	}
}

// syncReplication marks a cluster's replication as read at the end of every pass
// that wrote the listing whole — after the first the triggers record a job
// appearing, and the latest dates it — and not for a pass that failed to write a
// job, which would then land as new.
func TestSyncReplicationMarksTheBaselineAfterEveryWholePass(t *testing.T) {
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
		if len(mq.baselineMarks) != 2 || mq.baselineMarks[1].ClusterID != clusterID || mq.baselineMarks[1].ResourceType != "replication" {
			t.Errorf("baseline marks = %+v, want one per pass for this cluster's replication", mq.baselineMarks)
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

// A field adds at most maxObjectChangesPerHour rows an hour, however often it
// changes, counted apart from the object's other fields. What the cap holds back is
// not lost: the field keeps the reading it was last filed against, so when the
// hour ends the net change since is filed once — and one that came back to where it
// was files nothing.
func TestObjectChangeCapIsPerFieldAndHoldsBackNothingForGood(t *testing.T) {
	t.Parallel()
	start := time.Now().Truncate(time.Second)
	minute := func(n int) time.Time { return start.Add(time.Duration(n) * time.Minute) }
	osd := func(id int, up, in bool) cephOSDMetricSnapshot {
		return cephOSDMetricSnapshot{OSDID: id, OSDName: "osd." + strconv.Itoa(id), Host: "pve-01", StatusUp: up, StatusIn: in, CrushWeight: 1.8}
	}
	pool := func(size, minSize int) cephPoolMetricSnapshot {
		return cephPoolMetricSnapshot{PoolID: 1, PoolName: "pool01", Size: size, MinSize: minSize, PGNum: 32}
	}
	// filed is the rows filed for one object, as "change fields".
	filed := func(mq *mockQueries, id string) []string {
		var out []string
		for _, ch := range mq.objectChanges {
			if ch.ResourceID == id {
				out = append(out, ch.Change+" "+strings.Join(ch.Fields, ","))
			}
		}
		return out
	}
	repeat := func(row string, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = row
		}
		return out
	}
	last := func(mq *mockQueries) db.InsertClusterObjectChangeParams {
		return mq.objectChanges[len(mq.objectChanges)-1]
	}
	netChange := func(t *testing.T, row db.InsertClusterObjectChangeParams, field string) (from, to any) {
		t.Helper()
		var detail map[string]map[string]any
		if err := json.Unmarshal(row.Detail, &detail); err != nil {
			t.Fatalf("detail %s: %v", row.Detail, err)
		}
		return detail[field]["from"], detail[field]["to"]
	}

	t.Run("a storm in one field does not hide another, and ends as one net row", func(t *testing.T) {
		t.Parallel()
		mq, cluster := newMockQueries(), uuid.New()
		s := &Syncer{queries: mq, logger: testLogger()}
		pass := func(at int, osds ...cephOSDMetricSnapshot) {
			s.observeCephOSDsAt(context.Background(), cluster, osds, false, minute(at))
		}
		pass(0, osd(1, true, true), osd(2, true, true))
		for i := 1; i <= 25; i++ { // osd.1's up flaps each minute; osd.2 goes down once
			pass(i, osd(1, i%2 == 0, true), osd(2, i < 3, true))
		}
		pass(26, osd(1, false, false), osd(2, false, true)) // osd.1 goes out too
		if got, want := filed(mq, "1"), append(repeat("changed up", maxObjectChangesPerHour), "changed in"); !reflect.DeepEqual(got, want) {
			t.Errorf("osd.1 filed %v, want %d up rows then the out", got, maxObjectChangesPerHour)
		}
		if got := filed(mq, "2"); !reflect.DeepEqual(got, []string{"changed up"}) {
			t.Errorf("osd.2 filed %v, want its one change: another object's storm is not its cap", got)
		}
		before := len(mq.objectChanges)
		pass(60, osd(1, false, false), osd(2, false, true))
		if len(mq.objectChanges) != before {
			t.Fatalf("a row was filed before the hour ended: %+v", last(mq))
		}
		pass(61, osd(1, false, false), osd(2, false, true))
		if len(mq.objectChanges) != before+1 {
			t.Fatalf("%d rows filed when the hour ended, want the one net change", len(mq.objectChanges)-before)
		}
		row := last(mq)
		from, to := netChange(t, row, "up")
		if row.ResourceID != "1" || !reflect.DeepEqual(row.Fields, []string{"up"}) || from != true || to != false || !row.LastSeenAt.Equal(minute(20)) {
			t.Errorf("net row = %s %v up %v -> %v, seen as it was at %v; want osd.1 up true -> false, last seen as it was at minute 20",
				row.ResourceID, row.Fields, from, to, row.LastSeenAt.Sub(start))
		}
		pass(62, osd(1, false, false), osd(2, false, true))
		if len(mq.objectChanges) != before+1 {
			t.Error("the net change was filed again")
		}
	})

	t.Run("a flap that comes back files nothing when the hour ends", func(t *testing.T) {
		t.Parallel()
		mq, cluster := newMockQueries(), uuid.New()
		s := &Syncer{queries: mq, logger: testLogger()}
		s.observeCephOSDsAt(context.Background(), cluster, []cephOSDMetricSnapshot{osd(1, true, true)}, false, minute(0))
		for i := 1; i <= 24; i++ {
			s.observeCephOSDsAt(context.Background(), cluster, []cephOSDMetricSnapshot{osd(1, i%2 == 0, true)}, false, minute(i))
		}
		for _, at := range []int{61, 62} {
			s.observeCephOSDsAt(context.Background(), cluster, []cephOSDMetricSnapshot{osd(1, true, true)}, false, minute(at))
		}
		if n := len(mq.objectChanges); n != maxObjectChangesPerHour {
			t.Errorf("%d rows filed, want only the %d of the storm", n, maxObjectChangesPerHour)
		}
		if n := len(s.cephSeen[cluster].rows); n != 0 {
			t.Errorf("%d windows are still kept an hour on", n)
		}
	})

	t.Run("an object that comes and goes beyond the cap ends as it is", func(t *testing.T) {
		t.Parallel()
		mq, cluster := newMockQueries(), uuid.New()
		s := &Syncer{queries: mq, logger: testLogger()}
		pass := func(at int, withThree bool) {
			osds := []cephOSDMetricSnapshot{osd(1, true, true)}
			if withThree {
				osds = append(osds, osd(3, true, true))
			}
			s.observeCephOSDsAt(context.Background(), cluster, osds, false, minute(at))
		}
		pass(0, true)
		for i := 1; i <= 21; i++ { // gone at 1, back at 2 ... gone at 21: the 21st is held
			pass(i, i%2 == 0)
		}
		if n := len(filed(mq, "3")); n != maxObjectChangesPerHour {
			t.Fatalf("osd.3 filed %d rows, want %d", n, maxObjectChangesPerHour)
		}
		pass(61, false)
		if got := filed(mq, "3"); len(got) != maxObjectChangesPerHour+1 || got[len(got)-1] != "removed " {
			t.Errorf("osd.3 filed %v, want a removal once the hour ended: it is gone", got)
		}
		if row := last(mq); !row.LastSeenAt.Equal(minute(20)) {
			t.Errorf("the removal says it was last seen at %v, want minute 20", row.LastSeenAt.Sub(start))
		}
	})

	t.Run("a pool's size storm leaves its minimum alone", func(t *testing.T) {
		t.Parallel()
		mq, cluster := newMockQueries(), uuid.New()
		s := &Syncer{queries: mq, logger: testLogger()}
		pass := func(at, size, minSize int) {
			s.observeCephPoolsAt(context.Background(), cluster, []cephPoolMetricSnapshot{pool(size, minSize)}, minute(at))
		}
		pass(0, 3, 2)
		for i := 1; i <= 23; i++ {
			pass(i, 3-i%2, 2) // 2 at the odd minutes, 3 at the even
		}
		pass(24, 2, 1)
		if got, want := filed(mq, "pool01"), append(repeat("changed size", maxObjectChangesPerHour), "changed min_size"); !reflect.DeepEqual(got, want) {
			t.Errorf("the pool filed %v, want %d size rows then the minimum", got, maxObjectChangesPerHour)
		}
		pass(61, 2, 1)
		if from, to := netChange(t, last(mq), "size"); len(mq.objectChanges) != maxObjectChangesPerHour+2 || from != float64(3) || to != float64(2) {
			t.Errorf("%d rows, last size %v -> %v; want one net row, size 3 -> 2", len(mq.objectChanges), from, to)
		}
	})
}
