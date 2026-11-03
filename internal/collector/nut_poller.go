package collector

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/nut"
	"github.com/bigjakk/nexara/internal/upsfeed"
)

// Reachability of a UPS on the last poll, as stored in ups_devices.reachability.
const (
	upsReachOK          = "ok"
	upsReachStale       = "stale"
	upsReachDriverDown  = "driver_down"
	upsReachUnreachable = "unreachable"
	upsReachUnknown     = "unknown"
)

const (
	// nutListInterval is how often LIST UPS re-reads a server's device list.
	// It changes only when someone edits ups.conf.
	nutListInterval = time.Minute
	// nutReconcileInterval is how often the server list is re-read without a
	// reload request.
	nutReconcileInterval = 30 * time.Second
	// nutDescribePerPoll bounds GET DESC round trips per poll, so a new
	// device's sixty-odd variables are described over a few polls instead of
	// stalling one.
	nutDescribePerPoll = 8
	nutBackoffBase     = 10 * time.Second
	nutBackoffMax      = 5 * time.Minute
	// maxStoredVars and maxStoredValue cap the variable snapshot kept per device.
	maxStoredVars  = 512
	maxStoredValue = 256
)

// NutQueries is what the NUT poller needs from the database.
type NutQueries interface {
	ListEnabledNutServers(ctx context.Context) ([]db.NutServer, error)
	ListUpsDevicesByServer(ctx context.Context, serverID uuid.UUID) ([]db.UpsDevice, error)
	UpsertUpsDevice(ctx context.Context, arg db.UpsertUpsDeviceParams) (db.UpsDevice, error)
	MarkUpsDevicesAbsent(ctx context.Context, arg db.MarkUpsDevicesAbsentParams) error
	RecordUpsReading(ctx context.Context, arg db.RecordUpsReadingParams) error
	SetUpsReachability(ctx context.Context, arg db.SetUpsReachabilityParams) error
	MergeUpsVarDescriptions(ctx context.Context, arg db.MergeUpsVarDescriptionsParams) error
	InsertUpsMetric(ctx context.Context, arg db.InsertUpsMetricParams) error
	ListOpenUpsEventsByServer(ctx context.Context, serverID uuid.UUID) ([]db.UpsEvent, error)
	OpenUpsEvent(ctx context.Context, arg db.OpenUpsEventParams) error
	UpdateOpenUpsEvent(ctx context.Context, arg db.UpdateOpenUpsEventParams) error
	CloseUpsEvent(ctx context.Context, arg db.CloseUpsEventParams) error
	CloseUpsEventsOfDisabledServers(ctx context.Context) (int64, error)
	InsertUpsPointEvent(ctx context.Context, arg db.InsertUpsPointEventParams) error
	SetNutServerPolled(ctx context.Context, arg db.SetNutServerPolledParams) error
	SetNutServerPollError(ctx context.Context, arg db.SetNutServerPollErrorParams) error
	GetNutServer(ctx context.Context, id uuid.UUID) (db.NutServer, error)
	SetNutServerHostNode(ctx context.Context, arg db.SetNutServerHostNodeParams) error
	ListNodeLoginAddresses(ctx context.Context) ([]db.ListNodeLoginAddressesRow, error)
	UpsertUpsClients(ctx context.Context, arg db.UpsertUpsClientsParams) error
	MarkUpsClientsMissed(ctx context.Context, arg db.MarkUpsClientsMissedParams) error
	PruneUpsClients(ctx context.Context, arg db.PruneUpsClientsParams) error
	InsertFoundUpsFeed(ctx context.Context, arg db.InsertFoundUpsFeedParams) (int64, error)
	SetUpsClientsListed(ctx context.Context, arg db.SetUpsClientsListedParams) error
}

// NutPollerConfig configures the NUT poller.
type NutPollerConfig struct {
	// Interval is how often each server is read (NUT_POLL_INTERVAL).
	Interval time.Duration
}

// NutPoller reads every enabled NUT server on its own goroutine, so one slow
// server never delays another, and records each UPS's reading, state and
// power events. It only ever reads from upsd (see internal/nut).
type NutPoller struct {
	q        NutQueries
	encKey   string
	cfg      NutPollerConfig
	logger   *slog.Logger
	eventPub eventPublisher
	dial     func(context.Context, nut.Config) (*nut.Client, error)
	now      func() time.Time
}

// NewNutPoller creates a NUT poller.
func NewNutPoller(q NutQueries, encryptionKey string, cfg NutPollerConfig, logger *slog.Logger) *NutPoller {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}
	return &NutPoller{q: q, encKey: encryptionKey, cfg: cfg, logger: logger, dial: nut.Dial, now: time.Now}
}

// SetEventPublisher attaches the publisher that tells open tabs a UPS changed.
func (p *NutPoller) SetEventPublisher(pub eventPublisher) { p.eventPub = pub }

// Run polls until ctx ends. A value on reload re-reads the server list at
// once, which creating, changing or removing a server asks for.
func (p *NutPoller) Run(ctx context.Context, reload <-chan struct{}) {
	workers := map[uuid.UUID]*nutWorker{}
	defer func() {
		for _, w := range workers {
			w.stop()
		}
	}()
	p.reconcile(ctx, workers)
	t := time.NewTicker(nutReconcileInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.reconcile(ctx, workers)
		case <-reload:
			p.reconcile(ctx, workers)
		}
	}
}

// reconcile starts a worker per enabled server, stops the ones whose server is
// gone or disabled, and restarts any whose connection settings changed or
// whose goroutine has died.
func (p *NutPoller) reconcile(ctx context.Context, workers map[uuid.UUID]*nutWorker) {
	servers, err := p.q.ListEnabledNutServers(ctx)
	if err != nil {
		if ctx.Err() == nil {
			p.logger.Warn("nut: listing servers failed", "error", err)
		}
		return
	}
	want := make(map[uuid.UUID]db.NutServer, len(servers))
	for _, s := range servers {
		want[s.ID] = s
	}
	for id, w := range workers {
		s, ok := want[id]
		if !ok || nutConnectionKey(s) != w.key || w.exited() {
			w.stop()
			delete(workers, id)
		}
	}
	// Run after the stops, so no worker of a disabled server can open an
	// episode behind it; a no-op once there are none.
	if n, err := p.q.CloseUpsEventsOfDisabledServers(ctx); err != nil {
		if ctx.Err() == nil {
			p.logger.Warn("nut: closing a disabled server's episodes failed", "error", err)
		}
	} else if n > 0 && p.eventPub != nil {
		p.eventPub.SystemEvent(ctx, events.KindUPSChange, "state_changed")
	}
	for id, s := range want {
		if _, ok := workers[id]; !ok {
			workers[id] = p.startWorker(ctx, s)
		}
	}
}

// nutConnectionKey is what, when it changes, means a worker must reconnect.
// The poll-status columns are left out: they change on every poll.
func nutConnectionKey(s db.NutServer) string {
	return strings.Join([]string{
		s.Host, strconv.Itoa(int(s.Port)), s.Username, s.PasswordEncrypted,
		s.TlsMode, strconv.FormatBool(s.VerifyTls), s.TlsFingerprint,
	}, "\x00")
}

type nutWorker struct {
	key    string
	cancel context.CancelFunc
	done   chan struct{}
}

func (w *nutWorker) stop() {
	w.cancel()
	<-w.done
}

func (w *nutWorker) exited() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

func (p *NutPoller) startWorker(parent context.Context, srv db.NutServer) *nutWorker {
	ctx, cancel := context.WithCancel(parent)
	w := &nutWorker{key: nutConnectionKey(srv), cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer func() {
			if r := recover(); r != nil {
				p.logger.Error("nut: poller panicked; restarting on the next reconcile", "server", srv.Name, "panic", r)
			}
		}()
		st := p.loadServerState(ctx, srv)
		defer st.closeConn()
		p.poll(ctx, st)
		t := time.NewTicker(p.cfg.Interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.poll(ctx, st)
			}
		}
	}()
	return w
}

// nutServerState is one worker's memory of its server between polls.
type nutServerState struct {
	srv               db.NutServer
	conn              *nut.Client
	release, protocol string
	failures          int
	retryAt           time.Time
	listedAt          time.Time
	clientsAt         time.Time
	devices           map[string]*upsDeviceState
}

func (st *nutServerState) closeConn() {
	if st.conn != nil {
		_ = st.conn.Close()
		st.conn = nil
	}
}

// upsDeviceState is the poller's memory of one UPS.
type upsDeviceState struct {
	id        uuid.UUID
	name      string
	prev      upsSnapshot
	lastFresh time.Time
	open      map[string]upsEpisodeDetails
	varNames  []string
	described map[string]bool
	// clients is the last LIST CLIENT, connections per address; fed holds the
	// nodes a found feed was already stored or refused for.
	clients map[netip.Addr]int16
	fed     map[upsfeed.Node]bool
}

// loadServerState seeds a worker from what the database already holds, so a
// restart neither re-opens an episode that is still open nor forgets one.
func (p *NutPoller) loadServerState(ctx context.Context, srv db.NutServer) *nutServerState {
	st := &nutServerState{srv: srv, devices: map[string]*upsDeviceState{}}
	rows, err := p.q.ListUpsDevicesByServer(ctx, srv.ID)
	if err != nil {
		p.logger.Warn("nut: loading devices failed", "server", srv.Name, "error", err)
		return st
	}
	byID := map[uuid.UUID]*upsDeviceState{}
	for _, r := range rows {
		d := newUpsDeviceState(r)
		st.devices[r.UpsName] = d
		byID[r.ID] = d
	}
	evs, err := p.q.ListOpenUpsEventsByServer(ctx, srv.ID)
	if err != nil {
		p.logger.Warn("nut: loading open events failed", "server", srv.Name, "error", err)
	}
	for _, e := range evs {
		if d := byID[e.UpsID]; d != nil {
			var det upsEpisodeDetails
			_ = json.Unmarshal(e.Details, &det)
			d.open[e.Kind] = det
		}
	}
	// A loss of contact whose episode was closed meanwhile (polling was turned
	// off) is forgotten, so one that persists opens a new episode instead of
	// reading as already recorded.
	for _, d := range st.devices {
		if kind := reachEpisodeKind(d.prev.Reach); kind != "" {
			if _, ok := d.open[kind]; !ok {
				d.prev.Reach = upsReachUnknown
			}
		}
	}
	return st
}

// reachEpisodeKind is the episode a reachability other than ok keeps open.
func reachEpisodeKind(reach string) string {
	switch reach {
	case upsReachUnreachable:
		return "unreachable"
	case upsReachStale, upsReachDriverDown:
		return "stale"
	}
	return ""
}

func newUpsDeviceState(r db.UpsDevice) *upsDeviceState {
	d := &upsDeviceState{
		id:        r.ID,
		name:      r.UpsName,
		open:      map[string]upsEpisodeDetails{},
		described: map[string]bool{},
		fed:       map[upsfeed.Node]bool{},
	}
	var vars map[string]string
	_ = json.Unmarshal(r.Vars, &vars)
	status := nut.ParseStatus(r.Status)
	d.prev = upsSnapshot{Reach: r.Reachability, State: status.State, Flags: flagSet(status)}
	d.prev.TestResult, d.prev.TestRunning, _ = selfTest(upsSnapshot{}, strings.TrimSpace(vars["ups.test.result"]))
	if r.LastReadingAt.Valid {
		d.lastFresh = r.LastReadingAt.Time
	}
	var descs map[string]string
	_ = json.Unmarshal(r.VarDescriptions, &descs)
	for k := range descs {
		d.described[k] = true
	}
	return d
}

// poll reads one server once. A connection an earlier poll left open can
// have been dropped by upsd in between, so an I/O failure on it gets one
// immediate redial before the server counts as unreachable.
func (p *NutPoller) poll(ctx context.Context, st *nutServerState) {
	now := p.now()
	if st.conn == nil && now.Before(st.retryAt) {
		return
	}
	reused := st.conn != nil
	changed, err := p.pollOnce(ctx, st, now)
	if err != nil && reused && ctx.Err() == nil {
		st.closeConn()
		var again bool
		again, err = p.pollOnce(ctx, st, now)
		changed = changed || again
	}
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		changed = p.serverUnreachable(ctx, st, err, now) || changed
	} else {
		st.failures = 0
		if qerr := p.q.SetNutServerPolled(ctx, db.SetNutServerPolledParams{
			ID: st.srv.ID, NutVersion: clipText(st.release, 32), ProtocolVersion: clipText(st.protocol, 32),
		}); qerr != nil {
			p.logger.Warn("nut: recording poll failed", "server", st.srv.Name, "error", qerr)
		}
	}
	if changed && p.eventPub != nil {
		p.eventPub.SystemEvent(ctx, events.KindUPSChange, "state_changed")
	}
}

// pollOnce returns an error only when the server itself could not be read.
// A single UPS that upsd answers with an ERR for is recorded on that device.
func (p *NutPoller) pollOnce(ctx context.Context, st *nutServerState, now time.Time) (changed bool, err error) {
	if st.conn == nil {
		cfg, err := p.config(st.srv)
		if err != nil {
			return false, err
		}
		conn, err := p.dial(ctx, cfg)
		if err != nil {
			return false, err
		}
		st.conn = conn
		if st.release, st.protocol, err = conn.Versions(ctx); err != nil {
			return false, err
		}
		st.listedAt = time.Time{}
	}
	if st.listedAt.IsZero() || now.Sub(st.listedAt) >= nutListInterval {
		list, err := st.conn.ListUPS(ctx)
		if err != nil {
			return false, err
		}
		if p.syncDevices(ctx, st, list, now) {
			changed = true
		}
		st.listedAt = now
	}
	for _, name := range sortedDeviceNames(st.devices) {
		d := st.devices[name]
		vars, err := st.conn.ListVars(ctx, name)
		var pe *nut.ProtocolError
		switch {
		case err == nil:
			changed = p.recordReading(ctx, d, vars, now) || changed
		case nut.IsUnknownUPS(err):
			st.listedAt = time.Time{} // gone from ups.conf: re-list next poll
		case nut.IsStale(err):
			reach := upsReachStale
			if errors.As(err, &pe) && pe.Code == "DRIVER-NOT-CONNECTED" {
				reach = upsReachDriverDown
			}
			changed = p.recordUnreadable(ctx, d, reach, err.Error(), now) || changed
		case errors.As(err, &pe):
			changed = p.recordUnreadable(ctx, d, upsReachUnreachable, err.Error(), now) || changed
		default:
			return changed, err
		}
	}
	changed = p.listClients(ctx, st, now) || changed
	p.describe(ctx, st)
	return changed, nil
}

func (p *NutPoller) config(srv db.NutServer) (nut.Config, error) {
	password := ""
	if srv.PasswordEncrypted != "" {
		pw, err := crypto.Decrypt(srv.PasswordEncrypted, p.encKey)
		if err != nil {
			return nut.Config{}, errors.New("the stored password could not be decrypted")
		}
		password = pw
	}
	return nut.Config{
		Host:        srv.Host,
		Port:        int(srv.Port),
		Username:    srv.Username,
		Password:    password,
		TLS:         nut.TLSMode(srv.TlsMode),
		VerifyTLS:   srv.VerifyTls,
		Fingerprint: srv.TlsFingerprint,
	}, nil
}

// serverUnreachable closes the connection, backs off, records the error and
// marks every device unreachable. Their power state is left alone: Nexara
// does not know it, and must not report it as on mains.
func (p *NutPoller) serverUnreachable(ctx context.Context, st *nutServerState, cause error, now time.Time) bool {
	st.closeConn()
	st.failures++
	// From when the attempt failed, not when the poll began: a timeout can
	// take as long as the first backoff step.
	backoff := min(nutBackoffBase<<min(st.failures-1, 8), nutBackoffMax)
	st.retryAt = p.now().Add(backoff)
	reason := clipText(cause.Error(), 500)
	p.logger.Warn("nut: server unreachable", "server", st.srv.Name, "failures", st.failures,
		"retry_in", backoff, "error", reason)
	if err := p.q.SetNutServerPollError(ctx, db.SetNutServerPollErrorParams{ID: st.srv.ID, LastPollError: reason}); err != nil {
		p.logger.Warn("nut: recording poll error failed", "server", st.srv.Name, "error", err)
	}
	changed := false
	for _, name := range sortedDeviceNames(st.devices) {
		changed = p.recordUnreadable(ctx, st.devices[name], upsReachUnreachable, reason, now) || changed
	}
	return changed
}

// syncDevices applies a LIST UPS reply: new devices are added, and devices it
// no longer names are marked absent and their open episodes closed.
func (p *NutPoller) syncDevices(ctx context.Context, st *nutServerState, list []nut.UPS, now time.Time) bool {
	changed := false
	names := make([]string, 0, len(list))
	seen := make(map[string]bool, len(list))
	for _, u := range list {
		names = append(names, u.Name)
		seen[u.Name] = true
		row, err := p.q.UpsertUpsDevice(ctx, db.UpsertUpsDeviceParams{
			ServerID: st.srv.ID, UpsName: u.Name, Description: clipText(u.Description, maxStoredValue),
		})
		if err != nil {
			p.logger.Warn("nut: storing device failed", "server", st.srv.Name, "ups", u.Name, "error", err)
			continue
		}
		if _, ok := st.devices[u.Name]; !ok {
			st.devices[u.Name] = newUpsDeviceState(row)
			changed = true
		}
	}
	if err := p.q.MarkUpsDevicesAbsent(ctx, db.MarkUpsDevicesAbsentParams{ServerID: st.srv.ID, PresentNames: names}); err != nil {
		p.logger.Warn("nut: marking absent devices failed", "server", st.srv.Name, "error", err)
	}
	for name, d := range st.devices {
		if seen[name] {
			continue
		}
		// Nobody saw the condition clear: the UPS stopped being reported.
		for kind, det := range d.open {
			det.EndedBy = "removed"
			p.apply(ctx, d, []upsEventChange{{Kind: kind, Op: upsEventClose, Details: det, Unobserved: true}}, now)
		}
		delete(st.devices, name)
		changed = true
	}
	return changed
}

func (p *NutPoller) recordReading(ctx context.Context, d *upsDeviceState, vars map[string]string, now time.Time) bool {
	status := nut.ParseStatus(vars["ups.status"])
	r := decodeUpsReading(vars)
	stored, _ := json.Marshal(capVars(vars))
	if err := p.q.RecordUpsReading(ctx, db.RecordUpsReadingParams{
		ID:           d.id,
		Status:       clipText(vars["ups.status"], maxStoredValue),
		State:        string(status.State),
		Vars:         stored,
		Manufacturer: clipText(firstVar(vars, "device.mfr", "ups.mfr"), 128),
		Model:        clipText(firstVar(vars, "device.model", "ups.model"), 128),
		Serial:       clipText(firstVar(vars, "device.serial", "ups.serial"), 128),
		Firmware:     clipText(firstVar(vars, "ups.firmware", "device.firmware"), 128),
		Driver:       clipText(strings.TrimSpace(vars["driver.name"]+" "+vars["driver.version"]), 128),
		ReadAt:       pgtype.Timestamptz{Time: now, Valid: true},
	}); err != nil {
		p.logger.Warn("nut: storing reading failed", "ups", d.name, "error", err)
	}
	if err := p.q.InsertUpsMetric(ctx, db.InsertUpsMetricParams{
		Time: now, UpsID: d.id,
		BatteryCharge: r.charge, BatteryRuntime: r.runtime, BatteryVoltage: r.batteryVoltage,
		UpsLoad: r.load, Realpower: r.realpower, InputVoltage: r.inputVoltage,
		OutputVoltage: r.outputVoltage, InputFrequency: r.inputFrequency, Temperature: r.temperature,
		OnBattery: status.OnBattery(),
	}); err != nil {
		p.logger.Warn("nut: storing metric failed", "ups", d.name, "error", err)
	}
	next := upsSnapshot{
		Reach: upsReachOK, State: status.State, Flags: flagSet(status),
		Charge: floatOf(r.charge), Runtime: floatOf(r.runtime), Load: floatOf(r.load),
		Status: status.Flags,
	}
	next.TestResult, next.TestRunning, next.TestDone = selfTest(d.prev, clipText(strings.TrimSpace(vars["ups.test.result"]), maxStoredValue))
	gap := !d.lastFresh.IsZero() && now.Sub(d.lastFresh) > 3*p.cfg.Interval
	p.apply(ctx, d, upsTransitions(d.open, next, gap), now)
	changed := d.prev.Reach != next.Reach || d.prev.State != next.State || !sameFlags(d.prev.Flags, next.Flags)
	d.prev, d.lastFresh = next, now
	d.varNames = sortedVarNames(vars)
	return changed
}

// recordUnreadable notes that a UPS could not be read. The power state stays
// as it was, so only reachability episodes can open or close.
func (p *NutPoller) recordUnreadable(ctx context.Context, d *upsDeviceState, reach, reason string, now time.Time) bool {
	if d.prev.Reach == reach {
		return false
	}
	if err := p.q.SetUpsReachability(ctx, db.SetUpsReachabilityParams{ID: d.id, Reachability: reach}); err != nil {
		p.logger.Warn("nut: storing reachability failed", "ups", d.name, "error", err)
	}
	next := d.prev
	next.Reach, next.Reason = reach, clipText(reason, 500)
	p.apply(ctx, d, upsTransitions(d.open, next, false), now)
	d.prev = next
	return true
}

func (p *NutPoller) apply(ctx context.Context, d *upsDeviceState, changes []upsEventChange, now time.Time) {
	for _, c := range changes {
		details, _ := json.Marshal(c.Details)
		var err error
		switch c.Op {
		case upsEventOpen:
			err = p.q.OpenUpsEvent(ctx, db.OpenUpsEventParams{UpsID: d.id, Kind: c.Kind, StartedAt: now, Details: details})
			d.open[c.Kind] = c.Details
		case upsEventUpdate:
			err = p.q.UpdateOpenUpsEvent(ctx, db.UpdateOpenUpsEventParams{UpsID: d.id, Kind: c.Kind, Details: details})
			d.open[c.Kind] = c.Details
		case upsEventClose:
			err = p.q.CloseUpsEvent(ctx, db.CloseUpsEventParams{
				UpsID: d.id, Kind: c.Kind, Details: details,
				EndedAt: pgtype.Timestamptz{Time: now, Valid: true}, EndObserved: !c.Unobserved,
			})
			delete(d.open, c.Kind)
		case upsEventPoint:
			err = p.q.InsertUpsPointEvent(ctx, db.InsertUpsPointEventParams{UpsID: d.id, Kind: c.Kind, At: now, Details: details})
		}
		if err != nil {
			p.logger.Warn("nut: storing power event failed", "ups", d.name, "kind", c.Kind, "error", err)
		}
	}
}

// describe fetches upsd's description of variables not yet described, a few
// per poll.
func (p *NutPoller) describe(ctx context.Context, st *nutServerState) {
	budget := nutDescribePerPoll
	for _, name := range sortedDeviceNames(st.devices) {
		d := st.devices[name]
		found := map[string]string{}
		for _, v := range d.varNames {
			if budget == 0 {
				break
			}
			if d.described[v] {
				continue
			}
			desc, err := st.conn.VarDescription(ctx, name, v)
			var pe *nut.ProtocolError
			if err != nil && !errors.As(err, &pe) {
				return // the connection is in trouble; the next poll finds out
			}
			budget--
			d.described[v] = true
			found[v] = clipText(desc, maxStoredValue)
		}
		if len(found) > 0 {
			b, _ := json.Marshal(found)
			if err := p.q.MergeUpsVarDescriptions(ctx, db.MergeUpsVarDescriptionsParams{ID: d.id, Descriptions: b}); err != nil {
				p.logger.Warn("nut: storing descriptions failed", "ups", name, "error", err)
			}
		}
		if budget == 0 {
			return
		}
	}
}

// upsSnapshot is what the event rules compare between polls.
type upsSnapshot struct {
	Reach      string
	Reason     string
	State      nut.State
	Flags      map[string]bool
	Status     []string
	TestResult string
	// TestRunning: a reading showed the test in progress since the last
	// finished result, so the next finished one is a new test even if its
	// text is the same.
	TestRunning bool
	TestDone    bool
	Charge      *float64
	Runtime     *float64
	Load        *float64
}

// upsEpisodeDetails is stored in ups_events.details.
type upsEpisodeDetails struct {
	LowestCharge  *float64 `json:"lowest_charge,omitempty"`
	LowestRuntime *float64 `json:"lowest_runtime,omitempty"`
	PeakLoad      *float64 `json:"peak_load,omitempty"`
	Status        string   `json:"status,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	Result        string   `json:"result,omitempty"`
	// EndedBy says why an episode closed unobserved without contact being
	// lost: "removed" here, "polling_off" from CloseUpsEventsOfDisabledServers.
	EndedBy string `json:"ended_by,omitempty"`
}

type upsEventOp int

const (
	upsEventOpen upsEventOp = iota
	upsEventUpdate
	upsEventClose
	upsEventPoint
)

type upsEventChange struct {
	Kind       string
	Op         upsEventOp
	Unobserved bool
	Details    upsEpisodeDetails
}

// upsPowerEpisodes are judged only on a fresh reading.
var upsPowerEpisodes = []struct {
	kind string
	on   func(upsSnapshot) bool
}{
	// OB, not the state: FSD on mains (upsmon -c fsd) is low_battery but no outage.
	{"on_battery", func(s upsSnapshot) bool { return s.Flags["OB"] }},
	{"low_battery", func(s upsSnapshot) bool { return s.State == nut.StateLowBattery }},
	{"overload", func(s upsSnapshot) bool { return s.Flags["OVER"] }},
	{"bypass", func(s upsSnapshot) bool { return s.Flags["BYPASS"] }},
	{"voltage_correction", func(s upsSnapshot) bool { return s.Flags["TRIM"] || s.Flags["BOOST"] }},
	{"replace_battery", func(s upsSnapshot) bool { return s.Flags["RB"] }},
}

// upsTransitions decides which power events open, update, close or are
// recorded on one poll; open holds the episodes open before it.
//
// Three outcomes, not two: a power condition is judged only on a fresh
// reading. A poll that could not read the UPS leaves those episodes as they
// were, so a lost connection never reads as "the outage ended". gap means the
// previous fresh reading is more than three intervals old, so an episode that
// has cleared closes with its end unobserved.
func upsTransitions(open map[string]upsEpisodeDetails, next upsSnapshot, gap bool) []upsEventChange {
	var out []upsEventChange
	want := func(kind string, on bool, det upsEpisodeDetails, unobserved bool) {
		cur, isOpen := open[kind]
		switch {
		case on && !isOpen:
			out = append(out, upsEventChange{Kind: kind, Op: upsEventOpen, Details: det})
		case on && isOpen && !sameEpisode(det, cur):
			out = append(out, upsEventChange{Kind: kind, Op: upsEventUpdate, Details: det})
		case !on && isOpen:
			out = append(out, upsEventChange{Kind: kind, Op: upsEventClose, Details: cur, Unobserved: unobserved})
		}
	}
	if next.Reach == upsReachOK {
		for _, e := range upsPowerEpisodes {
			det := open[e.kind]
			if e.kind == "on_battery" || e.kind == "low_battery" {
				det.LowestCharge = lowest(det.LowestCharge, next.Charge)
				det.LowestRuntime = lowest(det.LowestRuntime, next.Runtime)
				det.PeakLoad = highest(det.PeakLoad, next.Load)
			}
			if _, isOpen := open[e.kind]; !isOpen {
				det.Status = strings.Join(next.Status, " ")
			}
			want(e.kind, e.on(next), det, gap)
		}
		if next.TestDone {
			out = append(out, upsEventChange{Kind: "self_test", Op: upsEventPoint, Details: upsEpisodeDetails{Result: next.TestResult}})
		}
	}
	reachDet := func(kind string) upsEpisodeDetails {
		if d, ok := open[kind]; ok {
			return d
		}
		return upsEpisodeDetails{Reason: next.Reason}
	}
	want("unreachable", next.Reach == upsReachUnreachable, reachDet("unreachable"), false)
	stale := next.Reach == upsReachStale || next.Reach == upsReachDriverDown
	want("stale", stale, reachDet("stale"), false)
	return out
}

func sameEpisode(a, b upsEpisodeDetails) bool {
	return sameFloat(a.LowestCharge, b.LowestCharge) && sameFloat(a.LowestRuntime, b.LowestRuntime) &&
		sameFloat(a.PeakLoad, b.PeakLoad) && a.Status == b.Status && a.Reason == b.Reason && a.Result == b.Result
}

func sameFloat(a, b *float64) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// selfTest folds one ups.test.result reading into the baseline. A value that
// says the test is running is not a result: it only marks that the next
// finished value is a new test. A finished value is an event when it differs
// from the baseline, or whenever a run was seen since. The first finished
// value seen is only a baseline. A test that starts and finishes between two polls with the
// same text as before goes unrecorded.
func selfTest(prev upsSnapshot, raw string) (result string, running, done bool) {
	if raw == "" {
		return prev.TestResult, prev.TestRunning, false
	}
	if strings.Contains(strings.ToLower(raw), "progress") {
		return prev.TestResult, true, false
	}
	done = prev.TestRunning || (prev.TestResult != "" && raw != prev.TestResult)
	return raw, false, done
}

// upsReading is one LIST VAR reply's numbers.
type upsReading struct {
	charge, runtime, batteryVoltage, load, realpower         pgtype.Float8
	inputVoltage, outputVoltage, inputFrequency, temperature pgtype.Float8
}

func decodeUpsReading(vars map[string]string) upsReading {
	return upsReading{
		charge:         varFloat(vars, "battery.charge"),
		runtime:        varFloat(vars, "battery.runtime"),
		batteryVoltage: varFloat(vars, "battery.voltage"),
		load:           varFloat(vars, "ups.load"),
		realpower:      varFloat(vars, "ups.realpower"),
		inputVoltage:   varFloat(vars, "input.voltage"),
		outputVoltage:  varFloat(vars, "output.voltage"),
		inputFrequency: varFloat(vars, "input.frequency"),
		temperature:    varFloat(vars, "ups.temperature", "battery.temperature"),
	}
}

func varFloat(vars map[string]string, keys ...string) pgtype.Float8 {
	f, ok := nut.Number(vars, keys...)
	return pgtype.Float8{Float64: f, Valid: ok}
}

func floatOf(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}

func lowest(a, b *float64) *float64 {
	if a == nil || (b != nil && *b < *a) {
		return b
	}
	return a
}

func highest(a, b *float64) *float64 {
	if a == nil || (b != nil && *b > *a) {
		return b
	}
	return a
}

func firstVar(vars map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(vars[k]); v != "" {
			return v
		}
	}
	return ""
}

func flagSet(s nut.Status) map[string]bool {
	m := make(map[string]bool, len(s.Flags))
	for _, f := range s.Flags {
		m[f] = true
	}
	return m
}

func sameFlags(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// capVars keeps at most maxStoredVars variables, in name order, each value
// clipped: the snapshot is stored per device and shown in the UI.
func capVars(vars map[string]string) map[string]string {
	names := sortedVarNames(vars)
	if len(names) > maxStoredVars {
		names = names[:maxStoredVars]
	}
	out := make(map[string]string, len(names))
	for _, n := range names {
		out[n] = clipText(vars[n], maxStoredValue)
	}
	return out
}

func sortedVarNames(vars map[string]string) []string {
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func sortedDeviceNames(m map[string]*upsDeviceState) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// clipText makes upsd text safe to store: invalid UTF-8 dropped (Postgres
// refuses a whole statement over one bad byte, which would freeze the row at
// its last state), then at most n bytes without splitting a sequence.
func clipText(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}
