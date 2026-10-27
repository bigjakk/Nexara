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
// are recorded in cluster_object_changes (migrations/000119), keyed as audit_log
// is, and classified like a guest's status changes (status_change.go): a change
// something on record explains is not shown.
//
// Replication jobs are recorded by triggers on replication_jobs
// (migrations/000120), whatever wrote the row. The Ceph objects are diffed here,
// pass against pass.

// objectChange is one difference between two readings of a cluster's objects.
type objectChange struct {
	resourceID string
	change     string   // added, removed or changed
	fields     []string // the names of what differs, for changed; sorted
	detail     map[string]any
	node       string    // the OSD's host, for classification; empty when no node owns the object
	lastSeen   time.Time // when the object was last seen as it was, if that is not the previous reading
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

// markObjectBaseline records that a cluster's objects of one kind have been read,
// at the end of a pass that wrote them all: from the first mark the triggers record
// an object appearing, and the latest says when it was last absent.
func (s *Syncer) markObjectBaseline(ctx context.Context, clusterID uuid.UUID, resourceType string) {
	if err := s.queries.MarkClusterObjectBaseline(ctx, db.MarkClusterObjectBaselineParams{
		ClusterID:    clusterID,
		ResourceType: resourceType,
	}); err != nil {
		s.logger.Warn("failed to mark a cluster's objects as read",
			"cluster_id", clusterID, "resource_type", resourceType, "error", err)
	}
}

// cephSeen is what the last Ceph pass read of one cluster, kept in memory: the
// next pass diffs against it.
type cephSeen struct {
	osds  readings[int, cephOSDMetricSnapshot]
	pools readings[string, cephPoolMetricSnapshot]
	rows  map[string]*changeWindow // the rows each object's fields have added this hour
}

// changeWindow is what one field of one object has added to the History since start.
type changeWindow struct {
	start  time.Time
	n      int       // rows filed in the window
	frozen time.Time // when the object was last seen as the baseline holds it, once the cap has held a change back
}

// maxObjectChangesPerHour bounds the rows one field of one object adds in an hour.
// An OSD that flaps, or a pool whose pg_num steps down, would otherwise add one
// every pass; twenty is several real events (a reboot is a down and an up per OSD)
// and cuts a storm off at a row every three minutes. It is counted per field, so a
// storm in one cannot hide a change to another.
const maxObjectChangesPerHour = 20

// window returns the field's window as of now ("" is the object's own existence).
// One that is an hour old has ended: a new one takes its place, and the old one's
// frozen time is returned so the row that ends the hold can say when the object was
// last seen as it was.
func (st *cephSeen) window(resourceType, resourceID, field string, now time.Time) (w *changeWindow, wasFrozen time.Time) {
	key := resourceType + "/" + resourceID + "/" + field
	w = st.rows[key]
	if w != nil && now.Sub(w.start) >= time.Hour {
		wasFrozen = w.frozen
		w = nil
	}
	if w == nil {
		w = &changeWindow{start: now}
		st.rows[key] = w
	}
	return w, wasFrozen
}

// capObjectChanges applies the hourly cap to a pass's changes, field by field, and
// returns what to file and, for each object, the fields (and "" for its existence)
// whose change was held back. A held change is not lost: the caller keeps the
// reading it was seen against as that field's baseline (freezeOSDs, freezePools),
// so every pass that follows sees the field as still changed, and the first one in
// a window that has room files the net change since — once, from where the field
// was last filed to where it is. The caller holds s.cephMu.
func (s *Syncer) capObjectChanges(st *cephSeen, clusterID uuid.UUID, resourceType string, changes []objectChange, lastSeen, now time.Time) (kept []objectChange, frozen map[string]map[string]bool) {
	if st.rows == nil {
		st.rows = map[string]*changeWindow{}
	}
	hold := func(resourceID, field string, w *changeWindow) {
		if frozen == nil {
			frozen = map[string]map[string]bool{}
		}
		if frozen[resourceID] == nil {
			frozen[resourceID] = map[string]bool{}
		}
		frozen[resourceID][field] = true
		if w.frozen.IsZero() {
			w.frozen = lastSeen
			s.logger.Warn("ceph: a field of an object changed more than the hourly cap allows; its later changes this hour are held back and filed as one net change",
				"cluster_id", clusterID, "resource_type", resourceType, "resource_id", resourceID, "field", field, "cap", maxObjectChangesPerHour)
		}
	}
	for _, ch := range changes {
		seen := lastSeen
		take := func(w *changeWindow, wasFrozen time.Time) bool {
			if w.n >= maxObjectChangesPerHour {
				return false
			}
			w.n++
			if !wasFrozen.IsZero() && wasFrozen.Before(seen) {
				seen = wasFrozen
			}
			return true
		}
		if ch.change != "changed" {
			w, wasFrozen := st.window(resourceType, ch.resourceID, "", now)
			if !take(w, wasFrozen) {
				hold(ch.resourceID, "", w)
				continue
			}
			ch.lastSeen = seen
			kept = append(kept, ch)
			continue
		}
		var fields []string
		detail := map[string]any{}
		for _, f := range ch.fields {
			w, wasFrozen := st.window(resourceType, ch.resourceID, f, now)
			if !take(w, wasFrozen) {
				hold(ch.resourceID, f, w)
				continue
			}
			fields = append(fields, f)
			detail[f] = ch.detail[f]
		}
		if len(fields) == 0 {
			continue
		}
		ch.fields, ch.detail, ch.lastSeen = fields, detail, seen
		kept = append(kept, ch)
	}
	for key, w := range st.rows { // the windows that ended and were not renewed above
		if now.Sub(w.start) >= time.Hour {
			delete(st.rows, key)
		}
	}
	return kept, frozen
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
func (r *readings[K, V]) advance(cur map[K]V, now time.Time) (prev map[K]V, prevAt time.Time, ok bool) {
	prev, prevAt = r.last, r.at
	if prev != nil && len(cur) == 0 && len(prev) > 0 {
		return nil, time.Time{}, false
	}
	r.last, r.at = cur, now
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
	s.observeCephOSDsAt(ctx, clusterID, osds, time.Now())
}

func (s *Syncer) observeCephOSDsAt(ctx context.Context, clusterID uuid.UUID, osds []cephOSDMetricSnapshot, now time.Time) {
	cur := make(map[int]cephOSDMetricSnapshot, len(osds))
	for _, o := range osds {
		cur[o.OSDID] = o
	}
	s.cephMu.Lock()
	st := s.cephState(clusterID)
	prev, prevAt, ok := st.osds.advance(cur, now)
	var kept []objectChange
	if ok {
		var frozen map[string]map[string]bool
		kept, frozen = s.capObjectChanges(st, clusterID, "ceph_osd", diffCephOSDs(prev, cur), prevAt, now)
		freezeOSDs(cur, prev, frozen)
	}
	s.cephMu.Unlock()
	s.recordObjectChanges(ctx, clusterID, "ceph_osd", kept, prevAt)
}

// observeCephPools is observeCephOSDs for the cluster's pools.
func (s *Syncer) observeCephPools(ctx context.Context, clusterID uuid.UUID, pools []cephPoolMetricSnapshot) {
	s.observeCephPoolsAt(ctx, clusterID, pools, time.Now())
}

func (s *Syncer) observeCephPoolsAt(ctx context.Context, clusterID uuid.UUID, pools []cephPoolMetricSnapshot, now time.Time) {
	cur := make(map[string]cephPoolMetricSnapshot, len(pools))
	for _, p := range pools {
		if auditkey.ValidID(p.PoolName) {
			cur[p.PoolName] = p
		}
	}
	s.cephMu.Lock()
	st := s.cephState(clusterID)
	prev, prevAt, ok := st.pools.advance(cur, now)
	var kept []objectChange
	if ok {
		var frozen map[string]map[string]bool
		kept, frozen = s.capObjectChanges(st, clusterID, "ceph_pool", diffCephPools(prev, cur), prevAt, now)
		freezePools(cur, prev, frozen)
	}
	s.cephMu.Unlock()
	s.recordObjectChanges(ctx, clusterID, "ceph_pool", kept, prevAt)
}

// freezeOSDs puts back, in the baseline the next pass diffs against, what the cap
// held back: the field as the previous reading had it, or the object as it was (or
// not at all) when it is its existence that was held.
func freezeOSDs(base, prev map[int]cephOSDMetricSnapshot, frozen map[string]map[string]bool) {
	for idText, fields := range frozen {
		id, err := strconv.Atoi(idText)
		if err != nil {
			continue
		}
		old, had := prev[id]
		if fields[""] {
			if had {
				base[id] = old
			} else {
				delete(base, id)
			}
			continue
		}
		cur, still := base[id]
		if !had || !still {
			continue
		}
		for f := range fields {
			switch f {
			case "up":
				cur.StatusUp = old.StatusUp
			case "in":
				cur.StatusIn = old.StatusIn
			case "host":
				cur.Host = old.Host
			case "crush_weight":
				cur.CrushWeight = old.CrushWeight
			}
		}
		base[id] = cur
	}
}

// freezePools is freezeOSDs for pools.
func freezePools(base, prev map[string]cephPoolMetricSnapshot, frozen map[string]map[string]bool) {
	for name, fields := range frozen {
		old, had := prev[name]
		if fields[""] {
			if had {
				base[name] = old
			} else {
				delete(base, name)
			}
			continue
		}
		cur, still := base[name]
		if !had || !still {
			continue
		}
		for f := range fields {
			switch f {
			case "size":
				cur.Size = old.Size
			case "min_size":
				cur.MinSize = old.MinSize
			case "pg_autoscale_mode":
				cur.PGAutoscale = old.PGAutoscale
			case "pg_num_min":
				cur.PGNumMin = old.PGNumMin
			case "pg_num":
				cur.PGNum = old.PGNum
			}
		}
		base[name] = cur
	}
}

// recordObjectChanges files each change; lastSeen is when the previous reading
// was taken, unless the change says otherwise. An insert that fails is logged and
// the change is lost — the DB being down is a bigger problem than one missed row.
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
		seen := lastSeen
		if !ch.lastSeen.IsZero() {
			seen = ch.lastSeen
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
			LastSeenAt:   seen,
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

// autoscaled reports whether a pool's PG autoscaler mode lets it change the pool's
// pg_num: "on" does, "warn" only advises, "off" and an unreported mode leave it to
// the admin.
func autoscaled(mode string) bool { return mode == "on" }

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
			if old.PGAutoscale != p.PGAutoscale {
				changed["pg_autoscale_mode"] = map[string]any{"from": old.PGAutoscale, "to": p.PGAutoscale}
			}
			if old.PGNumMin != p.PGNumMin {
				changed["pg_num_min"] = map[string]any{"from": old.PGNumMin, "to": p.PGNumMin}
			}
			// pg_num is the autoscaler's to change while it is on, and moves without
			// anyone editing it (Proxmox reports no target for it, only the live count
			// and the autoscaler's own estimate), so it is compared only while the pool
			// is not autoscaled. What an admin sets under the autoscaler is its mode and
			// minimum, above.
			if !autoscaled(old.PGAutoscale) && !autoscaled(p.PGAutoscale) && old.PGNum != p.PGNum {
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
