package collector

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// outageReminder is how often the server log repeats that a cluster or a PBS
// server is still failing. The Activity feed hears of an outage only when it
// starts, when it changes and when it ends.
const outageReminder = 30 * time.Minute

// outageClearAfter is how long a told outage must stay clear before it is
// over: longer than the client cache's hold on an endpoint it could not reach
// (proxmox unreachableWindow, a minute), during which a pass syncs through
// another member without knowing it, and long enough that a flapping link
// files one pair rather than one per flap.
const outageClearAfter = 2 * time.Minute

// substitution is a pass's sync through a member other than the configured
// endpoint: the action to tell, why, and which member.
type substitution struct{ action, reason, member string }

// outage is one cluster's or PBS server's current problem. A planned shutdown
// once filed a "Sync failed" every five minutes for as long as it lasted.
type outage struct {
	since    time.Time     // its first failed or degraded pass
	clear    time.Time     // its first clean pass since the last bad one; zero while bad
	failures int           // failed passes in a row
	reported string        // the audit action the feed was told of it; "" none yet
	failing  bool          // reported is a failed sync, not a sync through another member
	sub      *substitution // this pass's, told only if the pass succeeds
	reminded time.Time     // the server log's last reminder
}

// outages holds each unit's outage. A unit's passes never overlap (the slots
// in SyncAll and SyncAllPBS), so what a pass notes is that pass's.
type outages struct {
	mu  sync.Mutex
	m   map[uuid.UUID]*outage
	now func() time.Time // nil is time.Now; tests set it
}

func (t *outages) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// at returns id's outage, opening one. The caller holds mu.
func (t *outages) at(id uuid.UUID) *outage {
	if t.m == nil {
		t.m = map[uuid.UUID]*outage{}
	}
	o := t.m[id]
	if o == nil {
		o = &outage{since: t.clock()}
		t.m[id] = o
	}
	return o
}

// reset forgets every outage: a new leader term starts with none told, since
// what the last one told may have ended under another leader.
func (t *outages) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m = nil
}

// fail records a failed pass and returns how many there have been in a row.
// A substitution noted earlier in the pass goes with it.
func (t *outages) fail(id uuid.UUID) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	o := t.at(id)
	o.failures++
	o.sub, o.clear = nil, time.Time{}
	return o.failures
}

// substitute notes that this pass is syncing through another member.
func (t *outages) substitute(id uuid.UUID, s substitution) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.at(id).sub = &s
}

// took returns the substitution of a pass that succeeded, nil when it went
// through its own endpoint, and how long the outage has lasted. A pass through
// another member is a bad one.
func (t *outages) took(id uuid.UUID) (*substitution, time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	o := t.m[id]
	if o == nil || o.sub == nil {
		return nil, 0
	}
	s := o.sub
	o.sub, o.failures, o.clear = nil, 0, time.Time{}
	return s, t.clock().Sub(o.since)
}

// due reports whether the feed has yet to be told of this kind of outage.
func (t *outages) due(id uuid.UUID, kind string, failing bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	o := t.at(id)
	return o.reported != kind || o.failing != failing
}

// told records that the feed now has been.
func (t *outages) told(id uuid.UUID, kind string, failing bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	o := t.at(id)
	o.reported, o.failing = kind, failing
}

// remind reports whether the server log is due another reminder of id's
// outage, the first one outageReminder after it began, and how long it runs.
func (t *outages) remind(id uuid.UUID) (lasting time.Duration, due bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	o := t.m[id]
	if o == nil {
		return 0, false
	}
	now := t.clock()
	if now.Sub(o.since) < outageReminder || now.Sub(o.reminded) < outageReminder {
		return now.Sub(o.since), false
	}
	o.reminded = now
	return now.Sub(o.since), true
}

// cleared records a pass through id's own endpoint. An outage nobody was told
// of ends with it; a told one, once it has stayed clear for outageClearAfter,
// is returned with true so its end can be told, and ends when close says so.
func (t *outages) cleared(id uuid.UUID) (outage, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	o := t.m[id]
	if o == nil {
		return outage{}, false
	}
	o.failures = 0
	if o.reported == "" {
		delete(t.m, id)
		return outage{}, false
	}
	now := t.clock()
	if o.clear.IsZero() {
		o.clear = now
	}
	if now.Sub(o.clear) < outageClearAfter {
		return outage{}, false
	}
	return *o, true
}

// close ends id's outage once its end has been told.
func (t *outages) close(id uuid.UUID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.m, id)
}
