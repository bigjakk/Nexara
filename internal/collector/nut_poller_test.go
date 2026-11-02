package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/nut"
	"github.com/bigjakk/nexara/internal/nut/nuttest"
)

func TestUpsTransitions(t *testing.T) {
	t.Parallel()
	pf := func(v float64) *float64 { return &v }
	read := func(state nut.State, flags ...string) upsSnapshot {
		s := upsSnapshot{Reach: upsReachOK, State: state, Flags: map[string]bool{}, Status: flags, Charge: pf(80)}
		for _, f := range flags {
			s.Flags[f] = true
		}
		return s
	}
	// An unread snapshot carries no power flags, so a power episode that the
	// three-outcome guard failed to protect would visibly close.
	unread := func(reach string, _ upsSnapshot) upsSnapshot {
		return upsSnapshot{Reach: reach, State: nut.StateUnknown, Flags: map[string]bool{}}
	}
	ol, ob, lb := read(nut.StateOnline, "OL"), read(nut.StateOnBattery, "OB"), read(nut.StateLowBattery, "OB", "LB")
	open := func(kinds ...string) map[string]upsEpisodeDetails {
		m := map[string]upsEpisodeDetails{}
		for _, k := range kinds {
			m[k] = upsEpisodeDetails{LowestCharge: pf(80)}
		}
		return m
	}

	for _, tc := range []struct {
		name string
		open map[string]upsEpisodeDetails
		// prev documents the transition; the rules read only open and next.
		prev, next upsSnapshot
		gap        bool
		want       []string
	}{
		{"steady on mains", nil, ol, ol, false, nil},
		{"mains lost", nil, ol, ob, false, []string{"open on_battery"}},
		{"battery falls", open("on_battery"), ob, func() upsSnapshot { s := ob; s.Charge = pf(60); return s }(), false, []string{"update on_battery"}},
		{"goes low", open("on_battery"), ob, lb, false, []string{"open low_battery"}},
		{"mains back", open("on_battery"), ob, ol, false, []string{"close on_battery"}},
		{"mains back after a gap", open("on_battery"), ob, ol, true, []string{"close on_battery unobserved"}},
		// Could not look is neither yes nor no: the outage stays open.
		{"contact lost mid-outage", open("on_battery"), ob, unread(upsReachUnreachable, ob), false, []string{"open unreachable"}},
		{"contact back", open("on_battery", "unreachable"), unread(upsReachUnreachable, ob), ob, false, []string{"close unreachable"}},
		{"driver down", nil, ol, unread(upsReachDriverDown, ol), false, []string{"open stale"}},
		{"replace battery raised", nil, ol, read(nut.StateOnline, "OL", "RB"), false, []string{"open replace_battery"}},
		{"trim then boost is one correction", open("voltage_correction"), read(nut.StateOnline, "OL", "TRIM"), read(nut.StateOnline, "OL", "BOOST"), false, nil},
		{"self-test finished", nil, ol, func() upsSnapshot { s := ol; s.TestDone = true; return s }(), false, []string{"point self_test"}},
	} {
		var got []string
		for _, c := range upsTransitions(tc.open, tc.next, tc.gap) {
			s := [...]string{"open", "update", "close", "point"}[c.Op] + " " + c.Kind
			if c.Unobserved {
				s += " unobserved"
			}
			got = append(got, s)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestSelfTest(t *testing.T) {
	t.Parallel()
	base := func(result string, running bool) upsSnapshot {
		return upsSnapshot{TestResult: result, TestRunning: running}
	}
	for _, tc := range []struct {
		name          string
		prev          upsSnapshot
		raw           string
		result        string
		running, done bool
	}{
		{"first value is a baseline", base("", false), "Done and passed", "Done and passed", false, false},
		{"running is not a result", base("Done and passed", false), "In progress", "Done and passed", true, false},
		{"the same result after a run is a new test", base("Done and passed", true), "Done and passed", "Done and passed", false, true},
		{"a changed result", base("Done and passed", false), "Done and warning", "Done and warning", false, true},
		{"unchanged", base("Done and passed", false), "Done and passed", "Done and passed", false, false},
		{"a run seen before any baseline", base("", true), "Done and passed", "Done and passed", false, true},
	} {
		result, running, done := selfTest(tc.prev, tc.raw)
		if result != tc.result || running != tc.running || done != tc.done {
			t.Errorf("%s: got %q %v %v", tc.name, result, running, done)
		}
	}
}

// One poller against the fake upsd, tick by tick: what it writes, and that it
// announces a change only when there is one.
func TestNutPoller_Polls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := nuttest.NewServer(t)
	fake.SetDevice("ups01", nuttest.Device{Description: "Rack UPS", Vars: map[string]string{
		"ups.status": "OL", "battery.charge": "100", "battery.runtime": "1440", "ups.load": "38",
		"ups.test.result": "Done and passed",
	}})
	host, port := fake.Addr()
	q := newFakeNutQueries(db.NutServer{ID: uuid.New(), Name: "nut01", Host: host, Port: int32(port), TlsMode: "off", Enabled: true})
	pub := &mockEventPub{}
	p := NewNutPoller(q, "", NutPollerConfig{Interval: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.SetEventPublisher(pub)
	clock := time.Date(2026, 11, 2, 3, 12, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	st := p.loadServerState(ctx, q.server)
	defer st.closeConn()
	tick := func(d time.Duration) { clock = clock.Add(d); p.poll(ctx, st) }

	tick(0)
	dev := q.device("ups01")
	if dev.State != "online" || dev.Reachability != upsReachOK || q.metrics != 1 || len(q.events) != 0 {
		t.Fatalf("first poll: state %q reach %q metrics %d events %v", dev.State, dev.Reachability, q.metrics, q.events)
	}
	if !json.Valid(dev.VarDescriptions) || pub.countKind(events.KindUPSChange) != 1 {
		t.Fatalf("first poll: descriptions %s, %d change events", dev.VarDescriptions, pub.countKind(events.KindUPSChange))
	}

	tick(time.Second)
	if q.metrics != 2 || pub.countKind(events.KindUPSChange) != 1 {
		t.Errorf("steady poll: metrics %d, %d change events; want 2 and still 1", q.metrics, pub.countKind(events.KindUPSChange))
	}

	fake.SetVar("ups01", "ups.status", "OB DISCHRG")
	fake.SetVar("ups01", "battery.charge", "80")
	tick(time.Second)
	if got := q.openKinds(); !slices.Equal(got, []string{"on_battery"}) || pub.countKind(events.KindUPSChange) != 2 {
		t.Fatalf("mains lost: open %q, %d change events", got, pub.countKind(events.KindUPSChange))
	}

	fake.SetReachable(false)
	tick(time.Second)
	if dev := q.device("ups01"); dev.Reachability != upsReachUnreachable || dev.State != "on_battery" || q.pollError == "" {
		t.Fatalf("server gone: reach %q state %q error %q", dev.Reachability, dev.State, q.pollError)
	}
	if got := q.openKinds(); !slices.Equal(got, []string{"on_battery", "unreachable"}) {
		t.Fatalf("server gone: open %q; the outage must stay open", got)
	}

	// Nexara restarts mid-outage: the new worker resumes from the open rows.
	st.closeConn()
	st = p.loadServerState(ctx, q.server)
	defer st.closeConn()
	if d := st.devices["ups01"]; d == nil || len(d.open) != 2 {
		t.Fatalf("after a restart: %+v; want the two open episodes resumed", d)
	}

	// Back a minute later, on mains: contact is seen returning, the end of the
	// outage is not.
	fake.SetReachable(true)
	fake.SetVar("ups01", "ups.status", "OL")
	tick(time.Minute)
	if got := q.openKinds(); len(got) != 0 {
		t.Fatalf("back on mains: still open %q", got)
	}
	for _, e := range q.events {
		if observed := e.kind == "unreachable"; e.endObserved != observed {
			t.Errorf("%s closed with end_observed=%v", e.kind, e.endObserved)
		}
	}

	// Disabled mid-outage, upsd gone too: nothing watches it any more, so the
	// episodes are closed as not observed instead of reading as ongoing.
	fake.SetVar("ups01", "ups.status", "OB")
	tick(time.Second)
	fake.SetReachable(false)
	tick(time.Minute)
	setEnabled := func(on bool) { q.mu.Lock(); q.server.Enabled = on; q.mu.Unlock() }
	setEnabled(false)
	p.reconcile(ctx, map[uuid.UUID]*nutWorker{})
	if got := q.openKinds(); len(got) != 0 {
		t.Fatalf("after disabling: still open %q", got)
	}

	// Re-enabled with upsd still gone: the loss of contact is recorded again.
	setEnabled(true)
	st.closeConn()
	st = p.loadServerState(ctx, q.server)
	defer st.closeConn()
	tick(time.Second)
	if got := q.openKinds(); !slices.Equal(got, []string{"unreachable"}) {
		t.Errorf("re-enabled while unreachable: open %q, want the loss recorded again", got)
	}
}

// A failed attempt can take a whole timeout. The backoff runs from when it
// failed, or a dead host would be redialled on every tick.
func TestNutPoller_BacksOffFromTheFailure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	q := newFakeNutQueries(db.NutServer{ID: uuid.New(), Name: "nut01", Host: "127.0.0.1", Port: 1, TlsMode: "off", Enabled: true})
	p := NewNutPoller(q, "", NutPollerConfig{Interval: time.Second}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clock := time.Date(2026, 11, 2, 3, 12, 0, 0, time.UTC)
	p.now = func() time.Time { return clock }
	dials := 0
	p.dial = func(context.Context, nut.Config) (*nut.Client, error) {
		dials++
		clock = clock.Add(nutBackoffBase) // the attempt times out
		return nil, errors.New("i/o timeout")
	}
	st := p.loadServerState(ctx, q.server)
	p.poll(ctx, st)
	clock = clock.Add(nutBackoffBase / 2)
	p.poll(ctx, st)
	if dials != 1 {
		t.Errorf("dialled %d times; the second poll falls inside the backoff", dials)
	}
}

// fakeNutQueries is an in-memory NutQueries for one server.
type fakeNutQueries struct {
	mu        sync.Mutex
	server    db.NutServer
	devices   map[string]db.UpsDevice
	metrics   int
	events    []*fakeUpsEvent
	pollError string
}

type fakeUpsEvent struct {
	upsID       uuid.UUID
	kind        string
	open        bool
	endObserved bool
	details     json.RawMessage
}

func newFakeNutQueries(s db.NutServer) *fakeNutQueries {
	return &fakeNutQueries{server: s, devices: map[string]db.UpsDevice{}}
}

func (f *fakeNutQueries) device(name string) db.UpsDevice {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.devices[name]
}

func (f *fakeNutQueries) openKinds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.events {
		if e.open {
			out = append(out, e.kind)
		}
	}
	slices.Sort(out)
	return out
}

func (f *fakeNutQueries) byID(id uuid.UUID) (string, db.UpsDevice) {
	for n, d := range f.devices {
		if d.ID == id {
			return n, d
		}
	}
	panic(fmt.Sprintf("no device %s", id))
}

func (f *fakeNutQueries) ListEnabledNutServers(context.Context) ([]db.NutServer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.server.Enabled {
		return nil, nil
	}
	return []db.NutServer{f.server}, nil
}

func (f *fakeNutQueries) CloseUpsEventsOfDisabledServers(context.Context) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, e := range f.events {
		if e.open && !f.server.Enabled {
			e.open, e.endObserved = false, false
			n++
		}
	}
	return n, nil
}

func (f *fakeNutQueries) ListUpsDevicesByServer(context.Context, uuid.UUID) ([]db.UpsDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]db.UpsDevice, 0, len(f.devices))
	for _, d := range f.devices {
		out = append(out, d)
	}
	return out, nil
}

func (f *fakeNutQueries) UpsertUpsDevice(_ context.Context, a db.UpsertUpsDeviceParams) (db.UpsDevice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devices[a.UpsName]
	if !ok {
		d = db.UpsDevice{ID: uuid.New(), ServerID: a.ServerID, UpsName: a.UpsName, State: "unknown",
			Reachability: upsReachUnknown, Vars: []byte("{}"), VarDescriptions: []byte("{}")}
	}
	d.Description, d.Present = a.Description, true
	f.devices[a.UpsName] = d
	return d, nil
}

func (f *fakeNutQueries) MarkUpsDevicesAbsent(context.Context, db.MarkUpsDevicesAbsentParams) error {
	return nil
}

func (f *fakeNutQueries) RecordUpsReading(_ context.Context, a db.RecordUpsReadingParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, d := f.byID(a.ID)
	d.Status, d.State, d.Reachability, d.Vars, d.LastReadingAt = a.Status, a.State, upsReachOK, a.Vars, a.ReadAt
	f.devices[n] = d
	return nil
}

func (f *fakeNutQueries) SetUpsReachability(_ context.Context, a db.SetUpsReachabilityParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, d := f.byID(a.ID)
	d.Reachability = a.Reachability
	f.devices[n] = d
	return nil
}

func (f *fakeNutQueries) MergeUpsVarDescriptions(_ context.Context, a db.MergeUpsVarDescriptionsParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, d := f.byID(a.ID)
	d.VarDescriptions = a.Descriptions
	f.devices[n] = d
	return nil
}

func (f *fakeNutQueries) InsertUpsMetric(context.Context, db.InsertUpsMetricParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.metrics++
	return nil
}

func (f *fakeNutQueries) ListOpenUpsEventsByServer(context.Context, uuid.UUID) ([]db.UpsEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []db.UpsEvent
	for _, e := range f.events {
		if e.open {
			out = append(out, db.UpsEvent{UpsID: e.upsID, Kind: e.kind, Details: e.details})
		}
	}
	return out, nil
}

func (f *fakeNutQueries) OpenUpsEvent(_ context.Context, a db.OpenUpsEventParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, &fakeUpsEvent{upsID: a.UpsID, kind: a.Kind, open: true, details: a.Details})
	return nil
}

func (f *fakeNutQueries) UpdateOpenUpsEvent(_ context.Context, a db.UpdateOpenUpsEventParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.open && e.upsID == a.UpsID && e.kind == a.Kind {
			e.details = a.Details
		}
	}
	return nil
}

func (f *fakeNutQueries) CloseUpsEvent(_ context.Context, a db.CloseUpsEventParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.events {
		if e.open && e.upsID == a.UpsID && e.kind == a.Kind {
			e.open, e.endObserved, e.details = false, a.EndObserved, a.Details
		}
	}
	return nil
}

func (f *fakeNutQueries) InsertUpsPointEvent(_ context.Context, a db.InsertUpsPointEventParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, &fakeUpsEvent{upsID: a.UpsID, kind: a.Kind, endObserved: true, details: a.Details})
	return nil
}

func (f *fakeNutQueries) SetNutServerPolled(context.Context, db.SetNutServerPolledParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollError = ""
	return nil
}

func (f *fakeNutQueries) SetNutServerPollError(_ context.Context, a db.SetNutServerPollErrorParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollError = a.LastPollError
	return nil
}
