package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// The observer records edits made directly in Proxmox — in its GUI, its API or
// a node's shell — that reach no audit row because they ran no task: a guest's
// options, a pool's members, a storage definition, a node's DNS. Every
// OBJECT_OBSERVE_INTERVAL it reads each cluster's objects of the kinds in
// observer_kinds.go, compares each with what it read last (cluster_object_state,
// migrations/000128: HMAC tags, never values) and files what differs in
// cluster_object_changes (000124), observed_by 'observer': the object as its
// audit rows name it, the names of the fields that differ and the window
// between the two reads. Classification (observer_explain.go) then hides every
// change that something on record explains.
//
// A reading follows three rules:
//
//   - Three outcomes. A read that worked is compared. One that failed — a
//     timeout, a 5xx, a node that is not online — changes nothing, and the
//     object is compared again from its last reading once it is read. A kind
//     this Proxmox does not have, or that the token may not read, is skipped
//     for a day. A read that comes back empty where objects were is held for
//     one pass: an empty answer is read as "every object deleted" only twice.
//   - First sight is a baseline. A kind never read whole, a node's objects
//     never read, a guest never read, or anything read last under another
//     signature — the key, the Proxmox versions, the token's privileges —
//     records nothing.
//   - Bounded. One pass per cluster at a time; at most two reads in flight and
//     ten starts a second; every read with its own timeout; guests spread over
//     the interval, the least recently read first, so a pass cut short resumes
//     where it stopped.

const (
	// ObjectObserveMinInterval is the floor under OBJECT_OBSERVE_INTERVAL.
	ObjectObserveMinInterval = time.Minute

	observeMaxInFlight = 2
	observeMinGap      = 100 * time.Millisecond // at most ten reads started a second
	observeCallTimeout = 10 * time.Second
	observeGuestShare  = 0.8 // the share of the interval the guest reads are spread over
	observeSkipFor     = 24 * time.Hour
	observeGuestBatch  = 100
	observeAbortAfter  = 5 // consecutive connection failures that end a pass

	// An object read changed in this many passes running is noisy — something
	// automated writes it — and from then on is recorded at most once an hour
	// with the hour's fields together, until a pass reads it unchanged.
	observeNoisyStreak = 12
	observeNoisyEvery  = time.Hour
)

// ObserverClient is the one Proxmox read the observer makes; *proxmox.Client
// has it.
type ObserverClient interface {
	ObserveRead(ctx context.Context, target proxmox.ObserveTarget) (json.RawMessage, error)
}

// ObserverClients returns a cluster's client: the shared client cache, in
// production, which also routes around an unreachable api_url.
type ObserverClients func(ctx context.Context, cluster db.Cluster) (ObserverClient, error)

// CachedObserverClients reads through the shared client cache: the clients
// every collector loop reuses, which route around an api_url node that is down.
func CachedObserverClients(cache *proxmox.ClientCache) ObserverClients {
	return func(ctx context.Context, cluster db.Cluster) (ObserverClient, error) {
		if cache == nil {
			return nil, errors.New("no Proxmox client cache")
		}
		return cache.Get(ctx, cluster.ID)
	}
}

// ObserverQueries is the observer's slice of the database.
type ObserverQueries interface {
	ListActiveClusters(ctx context.Context) ([]db.Cluster, error)
	ListNodesByCluster(ctx context.Context, clusterID uuid.UUID) ([]db.Node, error)
	ListVMsByCluster(ctx context.Context, clusterID uuid.UUID) ([]db.Vm, error)
	ListClusterObjectState(ctx context.Context, clusterID uuid.UUID) ([]db.ListClusterObjectStateRow, error)
	ListClusterObjectBaselines(ctx context.Context, clusterID uuid.UUID) ([]db.ListClusterObjectBaselinesRow, error)
	RecordObservedObjects(ctx context.Context, arg db.RecordObservedObjectsParams) error
	DeleteClusterObjectState(ctx context.Context, arg db.DeleteClusterObjectStateParams) error
}

// Observer is the loop's state between passes: what is skipped, what is held,
// which objects are noisy. What it read is in the database.
type Observer struct {
	queries  ObserverQueries
	clients  ObserverClients
	tags     *observeTagger
	interval time.Duration
	logger   *slog.Logger
	gap      time.Duration // the least time between two reads' starts; tests zero it
	spread   bool          // whether guest reads are spaced over the interval; tests turn it off

	inflight sync.Map       // cluster ID -> struct{}{} while its pass runs
	passes   sync.WaitGroup // the passes running

	mu      sync.Mutex
	skips   map[string]time.Time    // skip key -> until when it is not read
	held    map[string]bool         // hold key -> an empty reading has been held once
	noisy   map[string]*noisyObject // object key -> how it has been changing
	lastSig map[uuid.UUID]string    // the cluster signature a cluster's last pass saw
}

// NewObserver builds the observer. It refuses an encryption key crypto.Tag
// refuses: without one there is nothing to make the tags with.
func NewObserver(queries ObserverQueries, encryptionKey string, clients ObserverClients, interval time.Duration, logger *slog.Logger) (*Observer, error) {
	tags, err := newObserveTagger(encryptionKey)
	if err != nil {
		return nil, err
	}
	return &Observer{
		queries:  queries,
		clients:  clients,
		tags:     tags,
		interval: max(interval, ObjectObserveMinInterval),
		logger:   logger,
		gap:      observeMinGap,
		spread:   true,
		skips:    map[string]time.Time{},
		held:     map[string]bool{},
		noisy:    map[string]*noisyObject{},
		lastSig:  map[uuid.UUID]string{},
	}, nil
}

// Interval is the cadence the loop should tick at, the floor applied.
func (o *Observer) Interval() time.Duration { return o.interval }

// ObserveAll starts a pass for every active cluster whose previous pass has
// finished, and returns: one cluster's long guest walk never holds up another's
// pass, and a tick that finds a pass still running for a cluster skips that
// cluster rather than queueing a second pass.
func (o *Observer) ObserveAll(ctx context.Context) {
	clusters, err := o.queries.ListActiveClusters(ctx)
	if err != nil {
		o.logger.Warn("object observer: failed to list active clusters", "error", err)
		return
	}
	o.forgetQuietObjects(time.Now())
	for _, cluster := range clusters {
		if _, busy := o.inflight.LoadOrStore(cluster.ID, struct{}{}); busy {
			continue
		}
		o.passes.Add(1)
		go func(cluster db.Cluster) {
			defer o.passes.Done()
			defer o.inflight.Delete(cluster.ID)
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("object observer pass panicked", "cluster_id", cluster.ID, "panic", r)
				}
			}()
			o.observeCluster(ctx, cluster)
		}(cluster)
	}
}

// Wait returns once every pass started so far has finished.
func (o *Observer) Wait() { o.passes.Wait() }

// observePass is one cluster's pass.
type observePass struct {
	o        *Observer
	cluster  db.Cluster
	client   ObserverClient
	lim      *observeLimiter
	sysAudit bool   // the token holds Sys.Audit on "/"
	privFP   string // the token's privileges on "/", condensed

	state     map[string]map[string]storedObject // kind -> object id -> stored reading
	baselines map[string]storedBaseline          // kind -> its mark

	calls     atomic.Int64
	changes   atomic.Int64
	connFails atomic.Int64
	aborted   atomic.Bool
}

type storedObject struct {
	tags      objectTags
	node      string
	signature string
	lastSeen  time.Time
}

type storedBaseline struct {
	signature string
	lastRead  time.Time
}

func (o *Observer) observeCluster(ctx context.Context, cluster db.Cluster) {
	started := time.Now()
	nodes, err := o.queries.ListNodesByCluster(ctx, cluster.ID)
	if err != nil {
		o.logger.Debug("object observer: list nodes", "cluster_id", cluster.ID, "error", err)
		return
	}
	var online []db.Node
	for _, n := range nodes {
		if n.Status == "online" {
			online = append(online, n)
		}
	}
	if len(online) == 0 {
		// Unreachable as far as the metrics pass knows: it reports that, and
		// every read here would only time out.
		return
	}
	client, err := o.clients(ctx, cluster)
	if err != nil {
		o.logger.Debug("object observer: build client", "cluster_id", cluster.ID, "error", err)
		return
	}
	p := &observePass{o: o, cluster: cluster, client: client, lim: newObserveLimiter(observeMaxInFlight)}
	if !p.probe(ctx) {
		return
	}
	if err := p.load(ctx); err != nil {
		o.logger.Debug("object observer: load state", "cluster_id", cluster.ID, "error", err)
		return
	}

	clusterSig := observeSignature("cluster", o.tags.keyID, p.privFP, clusterVersions(nodes))
	o.noticeSignature(cluster.ID, clusterSig)
	for _, k := range observeClusterKinds {
		p.observeClusterKind(ctx, k, clusterSig)
	}
	for _, n := range online {
		nodeSig := observeSignature("node", o.tags.keyID, p.privFP, n.PveVersion, n.KernelVersion)
		for _, k := range observeNodeKinds {
			p.observeNodeKind(ctx, k, n, nodeSig)
		}
	}
	p.observeGuests(ctx, online, observeSignature("guest", o.tags.keyID, p.privFP))

	o.logger.Debug("object observer pass",
		"cluster_id", cluster.ID,
		"calls", p.calls.Load(),
		"changes", p.changes.Load(),
		"aborted", p.aborted.Load(),
		"elapsed", time.Since(started).Round(time.Millisecond))
}

// probe reads the token's own privileges on "/". It is the pass's check that
// the cluster answers and the token is accepted — a pass that cannot read it
// reads nothing — and what the signature and the Sys.Audit gate come from.
func (p *observePass) probe(ctx context.Context) bool {
	raw, out := p.read(ctx, proxmox.ObservePermissionsRoot(), p.o.gap)
	if out != readOK {
		return false
	}
	var perms map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &perms); err != nil {
		return false
	}
	root := perms["/"]
	privs := make([]string, 0, len(root))
	for priv, propagate := range root {
		v, _ := canonicalValue(propagate)
		privs = append(privs, priv+"="+v)
	}
	slices.Sort(privs)
	_, p.sysAudit = root["Sys.Audit"]
	p.privFP = observeSignature(privs...)
	return true
}

// load reads what the cluster's objects were last read as.
func (p *observePass) load(ctx context.Context) error {
	rows, err := p.o.queries.ListClusterObjectState(ctx, p.cluster.ID)
	if err != nil {
		return err
	}
	p.state = map[string]map[string]storedObject{}
	for _, r := range rows {
		tags, err := parseObjectTags(r.Fields)
		if err != nil {
			continue // unreadable: the object is read again as a first sight
		}
		if p.state[r.Kind] == nil {
			p.state[r.Kind] = map[string]storedObject{}
		}
		p.state[r.Kind][r.ObjectID] = storedObject{tags: tags, node: r.Node, signature: r.Signature, lastSeen: r.LastSeenAt}
	}
	marks, err := p.o.queries.ListClusterObjectBaselines(ctx, p.cluster.ID)
	if err != nil {
		return err
	}
	p.baselines = map[string]storedBaseline{}
	for _, m := range marks {
		p.baselines[m.ResourceType] = storedBaseline{signature: m.Signature, lastRead: m.LastReadAt}
	}
	return nil
}

func (p *observePass) observeClusterKind(ctx context.Context, k observeKind, sig string) {
	key := p.skipKey(k.kind, "")
	if p.o.skipped(key) {
		return
	}
	if k.sysAudit && !p.sysAudit {
		p.o.skip(key, "the cluster's token lacks Sys.Audit on /, and Proxmox answers this read in part without it")
		return
	}
	readAt := time.Now()
	readings, out := k.read(ctx, p)
	if !p.settle(key, out) {
		return
	}
	for _, rd := range readings {
		p.apply(ctx, rd, "", sig, readAt, time.Now())
	}
}

func (p *observePass) observeNodeKind(ctx context.Context, k observeNodeKind, node db.Node, sig string) {
	key := p.skipKey(k.kind, node.Name)
	if p.o.skipped(key) {
		return
	}
	readAt := time.Now()
	readings, out := k.read(ctx, p, node)
	if !p.settle(key, out) {
		return
	}
	for _, rd := range readings {
		p.apply(ctx, rd, node.Name, sig, readAt, time.Now())
	}
}

// settle acts on a read's outcome: true when there is a reading to compare.
func (p *observePass) settle(key string, out readOutcome) bool {
	switch out {
	case readOK:
		return true
	case readUnsupported:
		p.o.skip(key, "this Proxmox VE has no such objects")
	case readForbidden:
		p.o.skip(key, "the cluster's token may not read them")
	}
	return false
}

func (p *observePass) skipKey(kind, node string) string {
	return p.cluster.ID.String() + "/" + kind + "/" + node
}

// apply compares one reading with what is stored and writes the outcome: a
// node's reading (node set) covers that node's stored objects of the kind, a
// cluster's reading all of them.
func (p *observePass) apply(ctx context.Context, rd kindReading, node, sig string, readAt, doneAt time.Time) {
	prev := map[string]storedObject{}
	for id, s := range p.state[rd.kind] {
		if node == "" || s.node == node {
			prev[id] = s
		}
	}

	if !p.o.passEmpty(p.skipKey(rd.kind, node), len(rd.objects) == 0 && len(rd.unread) == 0 && len(prev) > 0) {
		return
	}

	// Whether an object new to this reading was created, or is part of a
	// baseline: a cluster's kind was read whole under this signature before; a
	// node's objects were read under it before.
	var (
		baselined  bool
		addedSince time.Time
	)
	if node == "" {
		mark := p.baselines[rd.kind]
		baselined, addedSince = mark.signature == sig, mark.lastRead
	} else {
		for _, s := range prev {
			if s.signature == sig {
				baselined = true
				if s.lastSeen.After(addedSince) {
					addedSince = s.lastSeen
				}
			}
		}
	}

	var batch recordBatch
	seen := make(map[string]bool, len(rd.objects))
	for _, obj := range rd.objects {
		tags, err := p.tagObject(rd.kind, obj)
		if err != nil {
			if rd.unread == nil {
				rd.unread = map[string]bool{}
			}
			rd.unread[obj.id] = true
			continue
		}
		seen[obj.id] = true
		old, existed := prev[obj.id]
		switch {
		case !existed:
			batch.save(obj, tags, sig, readAt)
			if baselined && !obj.hardware {
				batch.record(p.o.pace(p.objectKey(rd.kind, obj.id), changeRow(obj, "added", nil, nil, addedSince, doneAt), doneAt))
			}
		case old.signature != sig:
			batch.save(obj, tags, sig, readAt)
		default:
			fields, counts := diffTags(old.tags, tags)
			if len(fields) == 0 {
				batch.touched = append(batch.touched, obj.id)
				batch.record(p.o.quiet(p.objectKey(rd.kind, obj.id)))
				continue
			}
			batch.save(obj, tags, sig, readAt)
			batch.record(p.o.pace(p.objectKey(rd.kind, obj.id), changeRow(obj, "changed", fields, counts, old.lastSeen, doneAt), doneAt))
		}
	}
	for id, old := range prev {
		if seen[id] || rd.unread[id] {
			continue
		}
		batch.removed = append(batch.removed, id)
		flushed := p.o.quiet(p.objectKey(rd.kind, id))
		batch.record(flushed)
		if baselined && old.signature == sig && !p.wasHardware(rd.kind, id, old) {
			obj := removedObject(rd.kind, id, old.node)
			batch.record([]observedChangeRow{changeRow(obj, "removed", nil, nil, old.lastSeen, doneAt)})
		}
	}

	mark := ""
	if node == "" && len(rd.unread) == 0 {
		mark = sig
	}
	if p.write(ctx, rd.kind, batch, readAt, mark) && mark != "" {
		p.baselines[rd.kind] = storedBaseline{signature: mark, lastRead: readAt}
	}
}

// tagObject tags each of an object's fields.
func (p *observePass) tagObject(kind string, obj observedObject) (objectTags, error) {
	out := objectTags{scalar: make(map[string]string, len(obj.fields)), lists: map[string][]string{}}
	for f, v := range obj.fields {
		t, err := p.o.tags.tag(p.cluster.ID, kind, obj.id, f, v)
		if err != nil {
			return objectTags{}, err
		}
		out.scalar[f] = t
	}
	for f, items := range obj.lists {
		list := make([]string, 0, len(items))
		for _, v := range items {
			// An item's tag does not depend on its place, so a rule moved or
			// inserted above leaves the others' tags as they were.
			t, err := p.o.tags.tag(p.cluster.ID, kind, obj.id, f, v)
			if err != nil {
				return objectTags{}, err
			}
			list = append(list, t)
		}
		out.lists[f] = list
	}
	return out, nil
}

// wasHardware says whether a stored network interface was a physical NIC, which
// vanishes with nobody editing: its stored type is the tag "eth" would have.
func (p *observePass) wasHardware(kind, id string, old storedObject) bool {
	if kind != "network" {
		return false
	}
	eth, err := p.o.tags.tag(p.cluster.ID, kind, id, "type", "eth")
	return err == nil && old.tags.scalar["type"] == eth
}

func (p *observePass) objectKey(kind, id string) string {
	return p.cluster.ID.String() + "/" + kind + "/" + id
}

// removedObject is the identity a removed object's change is filed under: a
// node's own objects are the node's, everything else its kind's.
func removedObject(kind, id, node string) observedObject {
	resourceType := kind
	switch kind {
	case "node_options", "node_dns", "node_time", "node_firewall":
		resourceType = "node"
	}
	return observedObject{id: id, resourceType: resourceType, resourceID: id, node: node}
}

// observeGuests reads every guest on an online node, the least recently read
// first, through two workers, spaced so the walk fills most of the interval.
// A guest is changed or not: its first sight, or a reading under another
// signature, is its baseline, and one that leaves the inventory is forgotten —
// creating, cloning, restoring and destroying a guest are tasks, already in its
// History.
func (p *observePass) observeGuests(ctx context.Context, online []db.Node, sig string) {
	guests, err := p.o.queries.ListVMsByCluster(ctx, p.cluster.ID)
	if err != nil {
		p.o.logger.Debug("object observer: list guests", "cluster_id", p.cluster.ID, "error", err)
		return
	}
	nodeByID := make(map[uuid.UUID]string, len(online))
	for _, n := range online {
		nodeByID[n.ID] = n.Name
	}
	lastRead := func(g db.Vm) time.Time {
		return p.state[guestKind(g.Type)][guestID(g)].lastSeen
	}
	sort.SliceStable(guests, func(i, j int) bool { return lastRead(guests[i]).Before(lastRead(guests[j])) })

	gap := p.o.gap
	if p.o.spread && len(guests) > 0 {
		gap = max(gap, time.Duration(observeGuestShare*float64(p.o.interval)/float64(len(guests))))
	}

	type result struct {
		guest          db.Vm
		node           string
		config         map[string]json.RawMessage
		out            readOutcome
		readAt, doneAt time.Time
	}
	jobs := make(chan db.Vm)
	results := make(chan result)
	var workers sync.WaitGroup
	for range observeMaxInFlight {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for g := range jobs {
				node := nodeByID[g.NodeID]
				r := result{guest: g, node: node, out: readFailed, readAt: time.Now()}
				if target, err := proxmox.ObserveGuestConfig(node, g.Type, int(g.Vmid)); err == nil {
					r.config, r.out = p.readObjectGap(ctx, target, gap)
				}
				r.doneAt = time.Now()
				results <- r
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, g := range guests {
			if _, online := nodeByID[g.NodeID]; !online || p.o.skipped(p.skipKey("guest", guestID(g))) {
				continue
			}
			if p.aborted.Load() || ctx.Err() != nil {
				return
			}
			select {
			case jobs <- g:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	batches := map[string]*recordBatch{"vm": {}, "container": {}}
	pending := map[string]int{}
	refused := 0
	for r := range results {
		kind := guestKind(r.guest.Type)
		switch r.out {
		case readOK:
		case readForbidden:
			// A token scoped to some guests is refused the rest: skipped for a
			// day each, logged once for all of them below.
			p.o.skipSilently(p.skipKey("guest", guestID(r.guest)))
			refused++
			continue
		default:
			continue
		}
		p.applyGuest(batches[kind], kind, guestObject(r.guest, r.node, r.config), sig, r.readAt, r.doneAt)
		pending[kind]++
		if pending[kind] >= observeGuestBatch {
			p.write(ctx, kind, *batches[kind], time.Now(), "")
			batches[kind], pending[kind] = &recordBatch{}, 0
		}
	}
	for kind, b := range batches {
		if pending[kind] > 0 {
			p.write(ctx, kind, *b, time.Now(), "")
		}
	}
	if refused > 0 {
		p.o.logger.Info("object observer: the cluster's token may not read some guests' configurations; not reading them for a day",
			"cluster_id", p.cluster.ID, "guests", refused)
	}
	if ctx.Err() == nil {
		p.forgetGuests(ctx, guests)
	}
}

// applyGuest compares one guest's configuration with its last reading. A
// batch's touched rows carry their own read time, so they are saved rather than
// touched: guests in one batch were read up to a minute apart.
func (p *observePass) applyGuest(batch *recordBatch, kind string, obj observedObject, sig string, readAt, doneAt time.Time) {
	tags, err := p.tagObject(kind, obj)
	if err != nil {
		return
	}
	old, existed := p.state[kind][obj.id]
	if existed && old.signature == sig {
		fields, counts := diffTags(old.tags, tags)
		if len(fields) == 0 {
			batch.save(obj, tags, sig, readAt)
			batch.record(p.o.quiet(p.objectKey(kind, obj.id)))
			return
		}
		batch.record(p.o.pace(p.objectKey(kind, obj.id), changeRow(obj, "changed", fields, counts, old.lastSeen, doneAt), doneAt))
	}
	batch.save(obj, tags, sig, readAt)
}

// forgetGuests drops the stored readings of guests no longer in the inventory
// under that type. The inventory keeps a guest for a few minutes after it
// vanishes, so a migration's cutover is not mistaken for this.
func (p *observePass) forgetGuests(ctx context.Context, guests []db.Vm) {
	present := map[string]bool{}
	for _, g := range guests {
		present[guestKind(g.Type)+"/"+guestID(g)] = true
	}
	for _, kind := range []string{"vm", "container"} {
		var gone []string
		for id := range p.state[kind] {
			if !present[kind+"/"+id] {
				gone = append(gone, id)
			}
		}
		if len(gone) == 0 {
			continue
		}
		if err := p.o.queries.DeleteClusterObjectState(ctx, db.DeleteClusterObjectStateParams{
			ClusterID: p.cluster.ID, Kind: kind, ObjectIds: gone,
		}); err != nil {
			p.o.logger.Debug("object observer: forget guests", "cluster_id", p.cluster.ID, "error", err)
		}
	}
}

func guestID(g db.Vm) string { return fmt.Sprint(g.Vmid) }

// --- reads ---

type readOutcome int

const (
	readOK readOutcome = iota
	readFailed
	readUnsupported
	readForbidden
)

// observeOutcomeOf sorts a read's error into the three outcomes. A 404 is a
// failure, not "unsupported": no Proxmox answers an unknown path with one (it
// answers 501), so a 404 comes from something in between
// (IsHARulesUnsupportedError gives the evidence).
func observeOutcomeOf(err error) readOutcome {
	switch {
	case err == nil:
		return readOK
	case errors.Is(err, proxmox.ErrForbidden):
		return readForbidden
	case proxmox.IsHARulesUnsupportedError(err), proxmox.IsGroupsMigratedError(err):
		return readUnsupported
	default:
		return readFailed
	}
}

func (p *observePass) read(ctx context.Context, target proxmox.ObserveTarget, gap time.Duration) (json.RawMessage, readOutcome) {
	if p.aborted.Load() {
		return nil, readFailed
	}
	release, err := p.lim.acquire(ctx, gap)
	if err != nil {
		return nil, readFailed
	}
	defer release()
	callCtx, cancel := context.WithTimeout(ctx, observeCallTimeout)
	defer cancel()
	p.calls.Add(1)
	raw, err := p.client.ObserveRead(callCtx, target)
	if errors.Is(err, proxmox.ErrConnectionFailed) {
		if p.connFails.Add(1) >= observeAbortAfter && p.aborted.CompareAndSwap(false, true) {
			p.o.logger.Debug("object observer: cluster stopped answering, pass ended",
				"cluster_id", p.cluster.ID)
		}
	} else {
		p.connFails.Store(0)
	}
	out := observeOutcomeOf(err)
	if out != readOK {
		p.o.logger.Debug("object observer: read", "cluster_id", p.cluster.ID, "target", target.String(), "error", err)
		return nil, out
	}
	return raw, readOK
}

// readList reads a listing. An answer that is not a list — null, nothing —
// is a failed read, never an empty one.
func (p *observePass) readList(ctx context.Context, target proxmox.ObserveTarget) ([]map[string]json.RawMessage, readOutcome) {
	raw, out := p.read(ctx, target, p.o.gap)
	if out != readOK {
		return nil, out
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil || items == nil {
		return nil, readFailed
	}
	return items, readOK
}

func (p *observePass) readObject(ctx context.Context, target proxmox.ObserveTarget) (map[string]json.RawMessage, readOutcome) {
	return p.readObjectGap(ctx, target, p.o.gap)
}

func (p *observePass) readObjectGap(ctx context.Context, target proxmox.ObserveTarget, gap time.Duration) (map[string]json.RawMessage, readOutcome) {
	raw, out := p.read(ctx, target, gap)
	if out != readOK {
		return nil, out
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, readFailed
	}
	return obj, readOK
}

// observeLimiter bounds a pass's reads: at most n in flight, and each start at
// least the caller's gap after the one before.
type observeLimiter struct {
	slots chan struct{}
	mu    sync.Mutex
	next  time.Time
}

func newObserveLimiter(n int) *observeLimiter {
	return &observeLimiter{slots: make(chan struct{}, n)}
}

func (l *observeLimiter) acquire(ctx context.Context, gap time.Duration) (func(), error) {
	l.mu.Lock()
	now := time.Now()
	start := l.next
	if start.Before(now) {
		start = now
	}
	l.next = start.Add(gap)
	l.mu.Unlock()
	if wait := time.Until(start); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		}
	}
	select {
	case l.slots <- struct{}{}:
		return func() { <-l.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// --- writes ---

// observedChangeRow is one row for cluster_object_changes, as
// RecordObservedObjects decodes it. Never a value: field names, and for an
// ordered list how many items came and went.
type observedChangeRow struct {
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Vmid         *int32         `json:"vmid"`
	Change       string         `json:"change"`
	Fields       []string       `json:"fields"`
	Detail       map[string]any `json:"detail"`
	Node         string         `json:"node"`
	LastSeenAt   time.Time      `json:"last_seen_at"`
	ObservedAt   time.Time      `json:"observed_at"`
}

type observedStateRow struct {
	ObjectID   string     `json:"object_id"`
	Fields     objectTags `json:"fields"`
	Node       string     `json:"node"`
	Signature  string     `json:"signature"`
	LastSeenAt time.Time  `json:"last_seen_at"`
}

type recordBatch struct {
	changes []observedChangeRow
	states  []observedStateRow
	removed []string
	touched []string
}

func (b *recordBatch) save(obj observedObject, tags objectTags, sig string, readAt time.Time) {
	b.states = append(b.states, observedStateRow{ObjectID: obj.id, Fields: tags, Node: obj.node, Signature: sig, LastSeenAt: readAt})
}

func (b *recordBatch) record(rows []observedChangeRow) { b.changes = append(b.changes, rows...) }

func changeRow(obj observedObject, change string, fields []string, counts map[string]listCount, lastSeen, observed time.Time) observedChangeRow {
	row := observedChangeRow{
		ResourceType: obj.resourceType, ResourceID: obj.resourceID, Change: change, Fields: fields,
		Detail: map[string]any{}, Node: obj.node, LastSeenAt: lastSeen, ObservedAt: observed,
	}
	if row.Fields == nil {
		row.Fields = []string{}
	}
	if len(counts) > 0 {
		row.Detail["counts"] = counts
	}
	if obj.vmid != 0 {
		vmid := obj.vmid
		row.Vmid = &vmid
	}
	return row
}

// write records one batch in one statement; mark is the signature to mark the
// kind read whole under, or "". It reports whether the write went through: a
// failed write is logged, and the next pass compares against the old reading
// and finds the same changes again.
func (p *observePass) write(ctx context.Context, kind string, b recordBatch, seenAt time.Time, mark string) bool {
	if len(b.changes) == 0 && len(b.states) == 0 && len(b.removed) == 0 && len(b.touched) == 0 && mark == "" {
		return true
	}
	changes, err := json.Marshal(b.changes)
	if err != nil {
		return false
	}
	states, err := json.Marshal(b.states)
	if err != nil {
		return false
	}
	if p.cluster.ID == uuid.Nil {
		// Never a NULL-cluster row from here: only a global kind's trigger may
		// write one (migrations/000129), and a zero UUID names no cluster.
		return false
	}
	if err := p.o.queries.RecordObservedObjects(ctx, db.RecordObservedObjectsParams{
		ClusterID: p.cluster.ID,
		Kind:      kind,
		Changes:   changes,
		States:    states,
		Removed:   nonNil(b.removed),
		Touched:   nonNil(b.touched),
		SeenAt:    seenAt,
		Baseline:  mark,
	}); err != nil {
		p.o.logger.Warn("object observer: record a reading",
			"cluster_id", p.cluster.ID, "kind", kind, "error", err)
		return false
	}
	p.changes.Add(int64(len(b.changes)))
	return true
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// --- state between passes ---

// skipped reports whether key is being skipped.
func (o *Observer) skipped(key string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	until, ok := o.skips[key]
	if ok && time.Now().After(until) {
		delete(o.skips, key)
		return false
	}
	return ok
}

// skip stops reading key for a day, logging it once.
func (o *Observer) skip(key, why string) {
	o.mu.Lock()
	_, already := o.skips[key]
	o.skips[key] = time.Now().Add(observeSkipFor)
	o.mu.Unlock()
	if !already {
		o.logger.Info("object observer: not reading these objects for a day", "what", key, "reason", why)
	}
}

// skipSilently is skip without the log line, for a caller that logs a count.
func (o *Observer) skipSilently(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.skips[key] = time.Now().Add(observeSkipFor)
}

// noticeSignature clears a cluster's skips when its signature moves: an upgrade
// can bring a kind, a new privilege can open one.
func (o *Observer) noticeSignature(cluster uuid.UUID, sig string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if old, ok := o.lastSig[cluster]; ok && old != sig {
		prefix := cluster.String() + "/"
		for key := range o.skips {
			if strings.HasPrefix(key, prefix) {
				delete(o.skips, key)
			}
		}
	}
	o.lastSig[cluster] = sig
}

// passEmpty decides whether a reading may be compared. An empty reading where
// objects were stored is held the first time — the answer of a node whose
// config filesystem was not mounted is as empty as a cluster whose last pool
// was deleted — and believed the second.
func (o *Observer) passEmpty(key string, empty bool) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !empty {
		delete(o.held, key)
		return true
	}
	if o.held[key] {
		delete(o.held, key)
		return true
	}
	o.held[key] = true
	return false
}

// noisyObject is how one object has been changing.
type noisyObject struct {
	streak   int                // passes running in which it was read changed
	held     *observedChangeRow // the change being gathered while it is noisy
	lastEmit time.Time
}

// pace decides what to record of a change: the change itself, until the object
// has changed observeNoisyStreak passes running; then at most one row an hour,
// its fields those of the hour's changes together.
func (o *Observer) pace(key string, ch observedChangeRow, now time.Time) []observedChangeRow {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := o.noisy[key]
	if n == nil {
		n = &noisyObject{}
		o.noisy[key] = n
	}
	n.streak++
	if n.streak <= observeNoisyStreak {
		n.lastEmit = now
		return []observedChangeRow{ch}
	}
	if n.held == nil {
		held := ch
		n.held = &held
	} else {
		n.held.Fields = unionSorted(n.held.Fields, ch.Fields)
		n.held.ObservedAt = ch.ObservedAt
	}
	if now.Sub(n.lastEmit) < observeNoisyEvery {
		return nil
	}
	out := *n.held
	n.held, n.lastEmit = nil, now
	return []observedChangeRow{out}
}

// quiet is an object read unchanged, or gone: its streak ends, and a change
// gathered while it was noisy is recorded now.
func (o *Observer) quiet(key string) []observedChangeRow {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := o.noisy[key]
	if n == nil {
		return nil
	}
	delete(o.noisy, key)
	if n.held == nil {
		return nil
	}
	return []observedChangeRow{*n.held}
}

// forgetQuietObjects drops the streaks of objects not changed for a day: an
// object no longer read (its guest gone, its node removed) would keep one.
func (o *Observer) forgetQuietObjects(now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for key, n := range o.noisy {
		if n.held == nil && now.Sub(n.lastEmit) > observeSkipFor {
			delete(o.noisy, key)
		}
	}
}

func unionSorted(a, b []string) []string {
	out := slices.Concat(a, b)
	slices.Sort(out)
	return slices.Compact(out)
}

// clusterVersions is the Proxmox versions a cluster's nodes run, as one string.
func clusterVersions(nodes []db.Node) string {
	vs := make([]string, 0, len(nodes))
	for _, n := range nodes {
		vs = append(vs, n.PveVersion)
	}
	slices.Sort(vs)
	return strings.Join(slices.Compact(vs), " ")
}
