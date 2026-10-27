package collector

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/auditkey"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// Changes to the cluster page's objects that reach no audit row — a replication
// job edited in the Proxmox GUI, an OSD that went down, a pool resized there —
// are recorded in cluster_object_changes (migrations/000124), keyed as audit_log
// is, and classified like a guest's status changes (status_change.go): a change
// something on record explains is not shown.
//
// Replication jobs are recorded by triggers on replication_jobs
// (migrations/000125), whatever wrote the row. The Ceph objects are diffed here,
// pass against pass.

// objectChange is one difference between two readings of a cluster's objects.
type objectChange struct {
	resourceID string
	change     string   // added, removed or changed
	fields     []string // the names of what differs, for changed; sorted
	detail     map[string]any
	node       string // the OSD's host, for classification; empty when no node owns the object
}

// classifyObjectChanges decides, for the cluster's settled changes on the nodes
// this pass listed the tasks of (and those no node owns), whether something
// already on record explains each (ClassifyClusterObjectChanges). A failure is
// only logged: the changes stay unclassified, and hidden, until a later pass.
func (s *Syncer) classifyObjectChanges(ctx context.Context, cluster db.Cluster, listedNodes []string) {
	if _, err := s.queries.ClassifyClusterObjectChanges(ctx, db.ClassifyClusterObjectChangesParams{
		ClusterID:     cluster.ID,
		SettledBefore: time.Now().Add(-statusChangeSettle),
		ListedNodes:   listedNodes,
		GiveUpBefore:  time.Now().Add(-statusChangeGiveUp),
	}); err != nil {
		s.logger.Warn("failed to classify cluster object changes",
			"cluster_id", cluster.ID,
			"error", err,
		)
	}
}

// markObjectBaseline records that a cluster's objects of one kind have been read
// once, after which the triggers record an object appearing. Marked once per
// process; the insert ignores a mark already there.
func (s *Syncer) markObjectBaseline(ctx context.Context, clusterID uuid.UUID, resourceType string) {
	key := clusterID.String() + "/" + resourceType
	if _, done := s.objectBaselines.Load(key); done {
		return
	}
	if err := s.queries.MarkClusterObjectBaseline(ctx, db.MarkClusterObjectBaselineParams{
		ClusterID:    clusterID,
		ResourceType: resourceType,
	}); err != nil {
		s.logger.Warn("failed to mark a cluster's objects as read",
			"cluster_id", clusterID, "resource_type", resourceType, "error", err)
		return
	}
	s.objectBaselines.Store(key, true)
}

// cephSeen is what the last Ceph pass read of one cluster, kept in memory: the
// next pass diffs against it.
type cephSeen struct {
	osds  readings[int, cephOSDMetricSnapshot]
	pools readings[string, cephPoolMetricSnapshot]
}

// readings is the last successful reading of one kind of object. A nil map is
// "not read yet": the first reading after a start, or of a cluster just added or
// whose Ceph was just set up, is the baseline and records nothing.
type readings[K comparable, V any] struct {
	last map[K]V
	at   time.Time
}

// advance makes cur the latest reading and returns the one before it and when it
// was taken. ok is false when there is nothing to diff against, or when cur
// must not be taken for a reading: an empty list after a populated one is a read
// that came back empty, not a cluster that lost every object, and it changes
// nothing — the earlier reading stays the baseline.
func (r *readings[K, V]) advance(cur map[K]V) (prev map[K]V, prevAt time.Time, ok bool) {
	prev, prevAt = r.last, r.at
	if prev != nil && len(cur) == 0 && len(prev) > 0 {
		return nil, time.Time{}, false
	}
	r.last, r.at = cur, time.Now()
	return prev, prevAt, prev != nil
}

// cephState returns the cluster's Ceph readings; the caller holds s.cephMu.
func (s *Syncer) cephState(clusterID uuid.UUID) *cephSeen {
	if s.cephSeen == nil {
		s.cephSeen = map[uuid.UUID]*cephSeen{}
	}
	st := s.cephSeen[clusterID]
	if st == nil {
		st = &cephSeen{}
		s.cephSeen[clusterID] = st
	}
	return st
}

// observeCephOSDs diffs a successful reading of the cluster's OSDs against the
// last one and records what differs. The caller must not call it for a read
// that failed: a failed read is "could not look", never "every OSD is gone".
func (s *Syncer) observeCephOSDs(ctx context.Context, clusterID uuid.UUID, osds []cephOSDMetricSnapshot) {
	cur := make(map[int]cephOSDMetricSnapshot, len(osds))
	for _, o := range osds {
		cur[o.OSDID] = o
	}
	s.cephMu.Lock()
	prev, prevAt, ok := s.cephState(clusterID).osds.advance(cur)
	s.cephMu.Unlock()
	if ok {
		s.recordObjectChanges(ctx, clusterID, "ceph_osd", diffCephOSDs(prev, cur), prevAt)
	}
}

// observeCephPools is observeCephOSDs for the cluster's pools.
func (s *Syncer) observeCephPools(ctx context.Context, clusterID uuid.UUID, pools []cephPoolMetricSnapshot) {
	cur := make(map[string]cephPoolMetricSnapshot, len(pools))
	for _, p := range pools {
		if auditkey.ValidID(p.PoolName) {
			cur[p.PoolName] = p
		}
	}
	s.cephMu.Lock()
	prev, prevAt, ok := s.cephState(clusterID).pools.advance(cur)
	s.cephMu.Unlock()
	if ok {
		s.recordObjectChanges(ctx, clusterID, "ceph_pool", diffCephPools(prev, cur), prevAt)
	}
}

// recordObjectChanges files each change; lastSeen is when the previous reading
// was taken. An insert that fails is logged and the change is lost — the DB being
// down is a bigger problem than one missed row.
func (s *Syncer) recordObjectChanges(ctx context.Context, clusterID uuid.UUID, resourceType string, changes []objectChange, lastSeen time.Time) {
	for _, ch := range changes {
		detail, err := json.Marshal(ch.detail)
		if err != nil {
			continue
		}
		fields := ch.fields
		if fields == nil {
			fields = []string{}
		}
		if err := s.queries.InsertClusterObjectChange(ctx, db.InsertClusterObjectChangeParams{
			ClusterID:    clusterID,
			ResourceType: resourceType,
			ResourceID:   ch.resourceID,
			Change:       ch.change,
			Fields:       fields,
			Detail:       detail,
			Node:         ch.node,
			ObservedBy:   "ceph",
			LastSeenAt:   lastSeen,
		}); err != nil {
			s.logger.Warn("failed to record a cluster object change",
				"cluster_id", clusterID, "resource_type", resourceType, "resource_id", ch.resourceID, "error", err)
		}
	}
}

// changedObject is a "changed" difference: the fields' names beside their values.
func changedObject(id string, changed map[string]any, node string) objectChange {
	names := make([]string, 0, len(changed))
	for name := range changed {
		names = append(names, name)
	}
	sort.Strings(names)
	return objectChange{resourceID: id, change: "changed", fields: names, detail: changed, node: node}
}

func osdSnapshot(o cephOSDMetricSnapshot) map[string]any {
	return map[string]any{
		"name": o.OSDName, "host": o.Host, "up": o.StatusUp, "in": o.StatusIn, "crush_weight": o.CrushWeight,
	}
}

// diffCephOSDs is what differs between two readings of a cluster's OSDs: one that
// appeared, one that is gone, and the fields of one that changed — up, in, host
// and crush weight; the name follows the id. Used and total capacity are run
// state and not compared.
func diffCephOSDs(prev, cur map[int]cephOSDMetricSnapshot) []objectChange {
	var out []objectChange
	for id, o := range cur {
		old, existed := prev[id]
		if !existed {
			out = append(out, objectChange{resourceID: strconv.Itoa(id), change: "added", detail: osdSnapshot(o), node: o.Host})
			continue
		}
		changed := map[string]any{}
		if old.StatusUp != o.StatusUp {
			changed["up"] = map[string]any{"from": old.StatusUp, "to": o.StatusUp}
		}
		if old.StatusIn != o.StatusIn {
			changed["in"] = map[string]any{"from": old.StatusIn, "to": o.StatusIn}
		}
		if old.Host != o.Host {
			changed["host"] = map[string]any{"from": old.Host, "to": o.Host}
		}
		if old.CrushWeight != o.CrushWeight {
			changed["crush_weight"] = map[string]any{"from": old.CrushWeight, "to": o.CrushWeight}
		}
		if len(changed) > 0 {
			out = append(out, changedObject(strconv.Itoa(id), changed, o.Host))
		}
	}
	for id, old := range prev {
		if _, still := cur[id]; !still {
			out = append(out, objectChange{resourceID: strconv.Itoa(id), change: "removed", detail: osdSnapshot(old), node: old.Host})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].resourceID != out[j].resourceID {
			return out[i].resourceID < out[j].resourceID
		}
		return out[i].change < out[j].change
	})
	return out
}

func poolSnapshot(p cephPoolMetricSnapshot) map[string]any {
	return map[string]any{"pool_id": p.PoolID, "size": p.Size, "min_size": p.MinSize, "pg_num": p.PGNum}
}

// diffCephPools is diffCephOSDs for pools, keyed by name — the key the pool is
// filed under. A pool deleted and made again under its old name is a different
// pool (its id differs), so it is a removal and an addition, not a change.
func diffCephPools(prev, cur map[string]cephPoolMetricSnapshot) []objectChange {
	var out []objectChange
	for name, p := range cur {
		old, existed := prev[name]
		switch {
		case !existed:
			out = append(out, objectChange{resourceID: name, change: "added", detail: poolSnapshot(p)})
		case old.PoolID != p.PoolID:
			out = append(out,
				objectChange{resourceID: name, change: "removed", detail: poolSnapshot(old)},
				objectChange{resourceID: name, change: "added", detail: poolSnapshot(p)})
		default:
			changed := map[string]any{}
			if old.Size != p.Size {
				changed["size"] = map[string]any{"from": old.Size, "to": p.Size}
			}
			if old.MinSize != p.MinSize {
				changed["min_size"] = map[string]any{"from": old.MinSize, "to": p.MinSize}
			}
			if old.PGNum != p.PGNum {
				changed["pg_num"] = map[string]any{"from": old.PGNum, "to": p.PGNum}
			}
			if len(changed) > 0 {
				out = append(out, changedObject(name, changed, ""))
			}
		}
	}
	for name, old := range prev {
		if _, still := cur[name]; !still {
			out = append(out, objectChange{resourceID: name, change: "removed", detail: poolSnapshot(old)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].resourceID != out[j].resourceID {
			return out[i].resourceID < out[j].resourceID
		}
		return out[i].change < out[j].change
	})
	return out
}
