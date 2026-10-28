package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/handlers"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// observeStore is mockQueries' copy of the observer's tables: the stored
// readings and marks, and the changes filed, applied as RecordObservedObjects
// applies them (the SQL itself is tested in internal/db).
type observeStore struct {
	mu       sync.Mutex
	state    map[string]db.ListClusterObjectStateRow // "cluster/kind/id"
	marks    map[string]db.ListClusterObjectBaselinesRow
	changes  []observedChangeRow
	shown    int                                        // ShowUnclassifiedObservedChanges calls
	fail     map[string]int                             // RecordObservedObjects fails this many more writes of a kind
	lose     map[string]int                             // ... or writes them and loses the reply, as a dropped COMMIT
	presence map[string]db.ListClusterObjectPresenceRow // "cluster/kind/id"
	writes   map[string]int                             // RecordObservedObjects calls by kind
}

func (m *mockQueries) ListClusterObjectState(_ context.Context, cluster uuid.UUID) ([]db.ListClusterObjectStateRow, error) {
	m.observed.mu.Lock()
	defer m.observed.mu.Unlock()
	var out []db.ListClusterObjectStateRow
	for key, row := range m.observed.state {
		if strings.HasPrefix(key, cluster.String()+"/") {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *mockQueries) ListClusterObjectPresence(_ context.Context, cluster uuid.UUID) ([]db.ListClusterObjectPresenceRow, error) {
	m.observed.mu.Lock()
	defer m.observed.mu.Unlock()
	var out []db.ListClusterObjectPresenceRow
	for key, row := range m.observed.presence {
		if strings.HasPrefix(key, cluster.String()+"/") {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *mockQueries) ListClusterObjectBaselines(_ context.Context, cluster uuid.UUID) ([]db.ListClusterObjectBaselinesRow, error) {
	m.observed.mu.Lock()
	defer m.observed.mu.Unlock()
	var out []db.ListClusterObjectBaselinesRow
	for key, row := range m.observed.marks {
		if strings.HasPrefix(key, cluster.String()+"/") {
			out = append(out, row)
		}
	}
	return out, nil
}

func (m *mockQueries) RecordObservedObjects(_ context.Context, arg db.RecordObservedObjectsParams) (err error) {
	var changes []observedChangeRow
	var states []struct {
		ObjectID   string          `json:"object_id"`
		Fields     json.RawMessage `json:"fields"`
		Node       string          `json:"node"`
		Signature  string          `json:"signature"`
		LastSeenAt time.Time       `json:"last_seen_at"`
	}
	if err := json.Unmarshal(arg.Changes, &changes); err != nil {
		return err
	}
	if err := json.Unmarshal(arg.States, &states); err != nil {
		return err
	}
	written := map[string]bool{}
	for _, st := range states {
		if written[st.ObjectID] {
			// As Postgres refuses one statement's upsert of a row twice.
			return errors.New("ON CONFLICT DO UPDATE command cannot affect row a second time")
		}
		written[st.ObjectID] = true
	}
	s := &m.observed
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail[arg.Kind] > 0 {
		s.fail[arg.Kind]--
		return errors.New("the database refused the write")
	}
	if s.lose[arg.Kind] > 0 {
		s.lose[arg.Kind]--
		defer func() { err = errors.New("the reply was lost") }()
	}
	if s.state == nil {
		s.state, s.marks = map[string]db.ListClusterObjectStateRow{}, map[string]db.ListClusterObjectBaselinesRow{}
	}
	if s.presence == nil {
		s.presence, s.writes = map[string]db.ListClusterObjectPresenceRow{}, map[string]int{}
	}
	s.writes[arg.Kind]++
	var present []observedPresenceRow
	if len(arg.Presence) > 0 {
		if err := json.Unmarshal(arg.Presence, &present); err != nil {
			return err
		}
	}
	for _, id := range arg.PresenceRemoved {
		delete(s.presence, arg.ClusterID.String()+"/"+arg.Kind+"/"+id)
	}
	for _, r := range present {
		s.presence[arg.ClusterID.String()+"/"+arg.Kind+"/"+r.ObjectID] = db.ListClusterObjectPresenceRow{
			Kind: arg.Kind, ObjectID: r.ObjectID, Node: r.Node, Tag: r.Tag, Signature: r.Signature}
	}
	if arg.Notices != "" {
		s.marks[arg.ClusterID.String()+"/"+observedNoticesMark] = db.ListClusterObjectBaselinesRow{
			ResourceType: observedNoticesMark, Signature: arg.Notices, LastReadAt: arg.SeenAt}
	}
	prefix := arg.ClusterID.String() + "/" + arg.Kind + "/"
	s.changes = append(s.changes, changes...)
	for _, st := range states {
		s.state[prefix+st.ObjectID] = db.ListClusterObjectStateRow{Kind: arg.Kind, ObjectID: st.ObjectID,
			Fields: st.Fields, Node: st.Node, Signature: st.Signature, LastSeenAt: st.LastSeenAt}
	}
	for _, id := range arg.Removed {
		delete(s.state, prefix+id)
	}
	for _, id := range arg.Touched {
		row := s.state[prefix+id]
		row.LastSeenAt = arg.SeenAt
		s.state[prefix+id] = row
	}
	if arg.Baseline != "" {
		s.marks[arg.ClusterID.String()+"/"+arg.Kind] = db.ListClusterObjectBaselinesRow{ResourceType: arg.Kind,
			Signature: arg.Baseline, LastReadAt: arg.SeenAt}
	}
	return nil
}

func (m *mockQueries) MarkObservedBulk(_ context.Context, arg db.MarkObservedBulkParams) error {
	m.observed.mu.Lock()
	defer m.observed.mu.Unlock()
	for i, c := range m.observed.changes {
		if id, err := uuid.Parse(c.ID); err == nil && slices.Contains(arg.Ids, id) {
			m.observed.changes[i].Detail["bulk"], m.observed.changes[i].Detail["bulk_size"] = arg.Bulk, float64(arg.BulkSize)
		}
	}
	return nil
}

func (m *mockQueries) DeleteClusterObjectState(_ context.Context, arg db.DeleteClusterObjectStateParams) error {
	m.observed.mu.Lock()
	defer m.observed.mu.Unlock()
	for _, id := range arg.ObjectIds {
		delete(m.observed.state, arg.ClusterID.String()+"/"+arg.Kind+"/"+id)
	}
	return nil
}

// takeChanges returns the changes filed since the last call, as
// "change resource_type/resource_id [fields]".
func (s *observeStore) takeChanges() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.changes))
	for _, c := range s.changes {
		out = append(out, fmt.Sprintf("%s %s/%s %v", c.Change, c.ResourceType, c.ResourceID, c.Fields))
	}
	s.changes = nil
	slices.Sort(out)
	return out
}

// observeFakeClient answers each target from a table and counts the reads. A
// target with no answer is a 501, as Proxmox answers a path it does not have; a
// read without a deadline of its own is refused, as every read must have one.
type observeFakeClient struct {
	mu      sync.Mutex
	answers map[string]observeAnswer
	reads   map[string]int
	block   chan struct{} // when set, every read waits for it to close
	failAll error         // when set, every read but the probe fails with it
}

type observeAnswer struct {
	data string
	err  error
}

func (f *observeFakeClient) ObserveRead(ctx context.Context, t proxmox.ObserveTarget) (json.RawMessage, error) {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > observeCallTimeout {
		return nil, errors.New("a read with no timeout of its own")
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads[t.String()]++
	if f.failAll != nil && t.String() != pathPerms {
		return nil, f.failAll
	}
	a, ok := f.answers[t.String()]
	if !ok {
		return nil, &proxmox.APIError{StatusCode: http.StatusNotImplemented, Message: "Method not implemented"}
	}
	return json.RawMessage(a.data), a.err
}

func (f *observeFakeClient) set(path, data string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[path] = observeAnswer{data: data}
}

func (f *observeFakeClient) fail(path string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers[path] = observeAnswer{err: err}
}

func (f *observeFakeClient) readsOf(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads[path]
}

const (
	pathPerms   = "/access/permissions?path=%2F"
	pathStorage = "/storage"
	pathNet     = "/nodes/pve-01/network"
	pathGuest   = "/nodes/pve-01/qemu/100/config"
)

// observeBaseAnswers is a small cluster as Proxmox answers it, every value a
// placeholder.
func observeBaseAnswers() map[string]observeAnswer {
	a := func(data string) observeAnswer { return observeAnswer{data: data} }
	return map[string]observeAnswer{
		pathPerms:                      a(`{"/":{"Sys.Audit":1,"VM.Audit":1,"Datastore.Audit":1,"Pool.Audit":1,"Mapping.Audit":1}}`),
		"/pools":                       a(`[]`),
		pathStorage:                    a(`[{"storage":"store01","type":"dir","path":"/srv/store01","content":"images,iso","nodes":"pve-01,pve-02","digest":"d1"}]`),
		"/cluster/backup":              a(`[]`),
		"/cluster/mapping/usb":         a(`[]`),
		"/cluster/mapping/pci":         a(`[]`),
		"/cluster/metrics/server":      a(`[]`),
		"/cluster/options":             a(`{"keyboard":"en-us"}`),
		pathNet:                        a(`[{"iface":"vmbr0","type":"bridge","method":"static","cidr":"192.0.2.10/24","active":1}]`),
		"/nodes/pve-01/config":         a(`{"digest":"n1"}`),
		"/nodes/pve-01/dns":            a(`{"search":"example.com","dns1":"192.0.2.53"}`),
		"/nodes/pve-01/time":           a(`{"timezone":"UTC","time":1,"localtime":1}`),
		"/nodes/pve-01/firewall/rules": a(`[]`),
		pathGuest:                      a(`{"name":"linux01","memory":"2048","cores":2,"digest":"g1"}`),
	}
}

// observeEnv is one cluster with one online node and one VM.
type observeEnv struct {
	t       *testing.T
	mq      *mockQueries
	client  *observeFakeClient
	obs     *Observer
	cluster db.Cluster
	node    db.Node
}

func newObserveEnv(t *testing.T) *observeEnv {
	t.Helper()
	cluster := db.Cluster{ID: uuid.New(), Name: "cluster01"}
	node := db.Node{ID: uuid.New(), ClusterID: cluster.ID, Name: "pve-01", Status: "online",
		PveVersion: "pve-manager/9.2.20/0000aaaa", KernelVersion: "6.14.8-2-pve"}
	mq := &mockQueries{
		clusters:       []db.Cluster{cluster},
		nodesByCluster: []db.Node{node},
		vmsByCluster:   []db.Vm{{ID: uuid.New(), ClusterID: cluster.ID, NodeID: node.ID, Vmid: 100, Type: "qemu"}},
	}
	client := &observeFakeClient{answers: observeBaseAnswers(), reads: map[string]int{}}
	obs, err := NewObserver(mq, testEncryptionKey, func(context.Context, db.Cluster) (ObserverClient, error) {
		return client, nil
	}, time.Minute, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	obs.gap, obs.spread = 0, false
	return &observeEnv{t: t, mq: mq, client: client, obs: obs, cluster: cluster, node: node}
}

// The interval is held between a minute and an hour: the look-back is three of
// them, and must stay bounded.
func TestObserverInterval(t *testing.T) {
	t.Parallel()
	for configured, want := range map[time.Duration]time.Duration{
		30 * time.Second: time.Minute, 5 * time.Minute: 5 * time.Minute, 3 * time.Hour: time.Hour,
	} {
		obs, err := NewObserver(&mockQueries{}, testEncryptionKey, nil, configured, testLogger())
		if err != nil {
			t.Fatal(err)
		}
		if got := obs.Interval(); got != want {
			t.Errorf("%v: interval %v, want %v", configured, got, want)
		}
	}
}

// The limiter spaces the starts of a pass's reads and caps how many are in
// flight: together they bound what a pass costs a cluster.
func TestObserveLimiter(t *testing.T) {
	t.Parallel()
	const gap = 20 * time.Millisecond
	spaced := newObserveLimiter(observeMaxInFlight)
	start := time.Now()
	for range 4 {
		release, err := spaced.acquire(context.Background(), gap)
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	if elapsed := time.Since(start); elapsed < 3*gap {
		t.Errorf("four starts in %v, want each %v after the last", elapsed, gap)
	}
	held := newObserveLimiter(2)
	first, _ := held.acquire(context.Background(), 0)
	_, _ = held.acquire(context.Background(), 0)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := held.acquire(ctx, 0); err == nil {
		t.Error("a third read started with two in flight")
	}
	first()
	release, err := held.acquire(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

// restart is Nexara restarting: a new observer, over the same database.
func (e *observeEnv) restart() {
	e.t.Helper()
	obs, err := NewObserver(e.mq, testEncryptionKey, func(context.Context, db.Cluster) (ObserverClient, error) {
		return e.client, nil
	}, time.Minute, testLogger())
	if err != nil {
		e.t.Fatal(err)
	}
	obs.gap, obs.spread, obs.maxObjects, obs.maxKind, obs.maxAccess = 0, false, e.obs.maxObjects, e.obs.maxKind, e.obs.maxAccess
	e.obs = obs
}

// passRows runs one pass and returns the rows it filed, as written.
func (e *observeEnv) passRows() []observedChangeRow {
	e.obs.ObserveAll(context.Background())
	e.obs.Wait()
	e.mq.observed.mu.Lock()
	defer e.mq.observed.mu.Unlock()
	rows := e.mq.observed.changes
	e.mq.observed.changes = nil
	return rows
}

// pass runs one pass and returns what it filed.
func (e *observeEnv) pass() []string {
	e.obs.ObserveAll(context.Background())
	e.obs.Wait()
	return e.mq.observed.takeChanges()
}

func (e *observeEnv) expect(step string, want ...string) {
	e.t.Helper()
	if got := e.pass(); !slices.Equal(got, want) {
		e.t.Fatalf("%s: filed %q, want %q", step, got, want)
	}
}

// Every kind compares only what an edit changes: its first read is a baseline,
// a second that differs only in what Proxmox rewrites by itself — the digest,
// run state, an order Proxmox re-encodes per request, a number sent as a
// string — records nothing, and a real edit records that field's name, filed
// under the object's audit identity, without its value.
func TestObserverKinds(t *testing.T) {
	t.Parallel()
	type answers map[string]string
	cases := []struct {
		kind              string
		base, noise, edit answers
		want              string // {cluster} is the cluster's id
		value             string // the edited value, which must appear nowhere a reader selects
		detail            string // the row's detail, when it has one
		members           string // the members that joined and left, when it lists other objects
	}{
		{kind: "storage",
			base:  answers{pathStorage: `[{"storage":"store01","type":"dir","content":"images,iso","nodes":"pve-01,pve-02","shared":0,"digest":"d1"}]`},
			noise: answers{pathStorage: `[{"storage":"store01","type":"dir","content":"iso,images","nodes":"pve-02,pve-01","shared":"0","digest":"d2"}]`},
			edit:  answers{pathStorage: `[{"storage":"store01","type":"dir","content":"iso,images","nodes":"pve-03","shared":"0","digest":"d3"}]`},
			want:  "changed storage/store01 [nodes]", value: "pve-03"},
		{kind: "backup",
			base:  answers{"/cluster/backup": `[{"id":"backup-01","schedule":"21:00","vmid":"100,101","next-run":1000,"enabled":1}]`},
			noise: answers{"/cluster/backup": `[{"id":"backup-01","schedule":"21:00","vmid":"101,100","next-run":2000,"enabled":true}]`},
			edit:  answers{"/cluster/backup": `[{"id":"backup-01","schedule":"22:15","vmid":"101,100","next-run":3000,"enabled":1}]`},
			want:  "changed backup/backup-01 [schedule]", value: "22:15"},
		{kind: "usb_mapping",
			base:  answers{"/cluster/mapping/usb": `[{"id":"usb01","map":["node=pve-01,path=1-2","node=pve-02,path=1-3"],"digest":"m1"}]`},
			noise: answers{"/cluster/mapping/usb": `[{"id":"usb01","map":["node=pve-02,path=1-3","node=pve-01,path=1-2"],"digest":"m2","checks":[]}]`},
			edit:  answers{"/cluster/mapping/usb": `[{"id":"usb01","map":["node=pve-02,path=1-3","node=pve-01,path=1-2"],"description":"dongle"}]`},
			want:  "changed usb_mapping/usb01 [description]", value: "dongle"},
		{kind: "pci_mapping",
			base: answers{"/cluster/mapping/pci": `[{"id":"gpu01","map":["node=pve-01,path=0000:01:00.0"],"digest":"p1"}]`},
			edit: answers{"/cluster/mapping/pci": `[{"id":"gpu01","map":["node=pve-01,path=0000:02:00.0"],"digest":"p2"}]`},
			want: "changed pci_mapping/gpu01 [map]", value: "0000:02:00.0"},
		{kind: "node_dns",
			edit: answers{"/nodes/pve-01/dns": `{"search":"example.com","dns1":"192.0.2.53","dns2":"192.0.2.54"}`},
			want: "changed node/pve-01 [dns2]", value: "192.0.2.54"},
		{kind: "pool",
			base: answers{"/pools": `[{"poolid":"web"}]`,
				"/pools?poolid=web": `[{"poolid":"web","members":[{"type":"qemu","vmid":100,"node":"pve-01","status":"running","cpu":0.1}]}]`},
			noise: answers{"/pools?poolid=web": `[{"poolid":"web","members":[{"type":"qemu","vmid":"100","node":"pve-02","status":"stopped","cpu":0}]}]`},
			edit: answers{"/pools?poolid=web": `[{"poolid":"web","members":[{"type":"qemu","vmid":100},{"type":"lxc","vmid":101},` +
				`{"type":"storage","storage":"store01"}]}]`},
			want: "changed pool/web [storage vms]", value: "store01", members: "[storage:store01 vms:101] []"},
		{kind: "metric_server",
			base: answers{"/cluster/metrics/server": `[{"id":"influx01","type":"influxdb","port":8089}]`,
				"/cluster/metrics/server/influx01": `{"type":"influxdb","server":"192.0.2.30","port":8089,"bucket":"proxmox"}`},
			noise: answers{"/cluster/metrics/server/influx01": `{"type":"influxdb","server":"192.0.2.30","port":"8089","bucket":"proxmox"}`},
			edit:  answers{"/cluster/metrics/server/influx01": `{"type":"influxdb","server":"192.0.2.30","port":8089,"bucket":"metrics02"}`},
			want:  "changed metric_server/influx01 [bucket]", value: "metrics02"},
		{kind: "cluster_options",
			base:  answers{"/cluster/options": `{"keyboard":"en-us","allowed-tags":["a"]}`},
			noise: answers{"/cluster/options": `{"keyboard":"en-us","allowed-tags":["a","b"]}`},
			edit:  answers{"/cluster/options": `{"keyboard":"en-us","allowed-tags":[],"http_proxy":"http://user:secret@192.0.2.40:3128"}`},
			want:  "changed cluster_options/{cluster} [http_proxy]", value: "secret"},
		{kind: "network",
			base:  answers{pathNet: `[{"iface":"vmbr0","type":"bridge","cidr":"192.0.2.10/24","active":1,"priority":3,"families":["inet"]}]`},
			noise: answers{pathNet: `[{"iface":"vmbr0","type":"bridge","cidr":"192.0.2.10/24","active":0,"priority":4,"families":["inet","inet6"],"exists":1,"altnames":["x"]}]`},
			edit:  answers{pathNet: `[{"iface":"vmbr0","type":"bridge","cidr":"192.0.2.11/24","active":1}]`},
			want:  "changed network/pve-01/vmbr0 [cidr]", value: "192.0.2.11"},
		{kind: "node_options",
			base:  answers{"/nodes/pve-01/config": `{"description":"rack 2","digest":"n1"}`},
			noise: answers{"/nodes/pve-01/config": `{"description":"rack 2","digest":"n2"}`},
			edit:  answers{"/nodes/pve-01/config": `{"description":"rack 3","digest":"n3"}`},
			want:  "changed node/pve-01 [description]", value: "rack 3"},
		{kind: "acme_config",
			base: answers{"/nodes/pve-01/config": `{"acme":"account=default","digest":"n1"}`},
			edit: answers{"/nodes/pve-01/config": `{"acme":"account=default","acmedomain0":"domain=pve-01.example.com","digest":"n2"}`},
			want: "changed acme_config/pve-01 [acmedomain0]", value: "pve-01.example.com"},
		{kind: "node_time",
			base:  answers{"/nodes/pve-01/time": `{"timezone":"UTC","time":1,"localtime":1}`},
			noise: answers{"/nodes/pve-01/time": `{"timezone":"UTC","time":2,"localtime":3}`},
			edit:  answers{"/nodes/pve-01/time": `{"timezone":"Etc/UTC","time":4,"localtime":4}`},
			want:  "changed node/pve-01 [timezone]", value: "Etc/UTC"},
		{kind: "node_firewall",
			base:  answers{"/nodes/pve-01/firewall/rules": `[{"pos":0,"type":"in","action":"ACCEPT","dport":"22","digest":"f1"}]`},
			noise: answers{"/nodes/pve-01/firewall/rules": `[{"pos":0,"type":"in","action":"ACCEPT","dport":"22","digest":"f2","ipversion":4}]`},
			edit: answers{"/nodes/pve-01/firewall/rules": `[{"pos":0,"type":"in","action":"DROP","dport":"8006"},` +
				`{"pos":1,"type":"in","action":"ACCEPT","dport":"22"}]`},
			want: "changed firewall_rule/node/pve-01 [rules]", value: "8006",
			detail: `{"counts":{"rules":{"added":1,"removed":0}}}`},
		{kind: "ha_resource",
			base:  answers{"/cluster/ha/resources": `[{"sid":"vm:100","type":"vm","state":"started","digest":"h1"}]`},
			noise: answers{"/cluster/ha/resources": `[{"sid":"vm:100","type":"vm","state":"started","digest":"h2","errors":{"group":"x"}}]`},
			edit:  answers{"/cluster/ha/resources": `[{"sid":"vm:100","type":"vm","state":"started","max_restart":3}]`},
			want:  "changed ha_resource/vm:100 [max_restart]", value: `"3"`},
		{kind: "ha_rule",
			base:  answers{"/cluster/ha/rules": `[{"rule":"apart","type":"resource-affinity","resources":"vm:100,vm:101","order":1,"digest":"r1"}]`},
			noise: answers{"/cluster/ha/rules": `[{"rule":"apart","type":"resource-affinity","resources":"vm:100,vm:101","order":2,"errors":{"x":"y"}}]`},
			edit:  answers{"/cluster/ha/rules": `[{"rule":"apart","type":"resource-affinity","resources":"vm:100,vm:101","comment":"spread"}]`},
			want:  "changed ha_rule/apart [comment]", value: "spread"},
		{kind: "ha_group",
			base:  answers{"/cluster/ha/groups": `[{"group":"prefer01","type":"group","nodes":"pve-01:2,pve-02:1","digest":"g1"}]`},
			noise: answers{"/cluster/ha/groups": `[{"group":"prefer01","type":"group","nodes":"pve-02:1,pve-01:2","digest":"g2"}]`},
			edit:  answers{"/cluster/ha/groups": `[{"group":"prefer01","type":"group","nodes":"pve-02:1,pve-01:2","restricted":1}]`},
			want:  "changed ha_group/prefer01 [restricted]", value: `"1"`},
		{kind: "sdn_zone",
			base:  answers{"/cluster/sdn/zones": `[{"zone":"zone01","type":"simple","nodes":"pve-01,pve-02","digest":"z1"}]`},
			noise: answers{"/cluster/sdn/zones": `[{"zone":"zone01","type":"simple","nodes":"pve-02,pve-01","digest":"z2"}]`},
			edit:  answers{"/cluster/sdn/zones": `[{"zone":"zone01","type":"simple","nodes":"pve-02,pve-01","ipam":"pve"}]`},
			want:  "changed sdn_zone/zone01 [ipam]", value: `"pve"`},
		{kind: "sdn_subnet",
			base: answers{"/cluster/sdn/vnets": `[{"vnet":"vnet01","zone":"zone01"}]`,
				"/cluster/sdn/vnets/vnet01/subnets": `[{"subnet":"zone01-192.0.2.0-24","gateway":"192.0.2.1","digest":"s1"}]`},
			edit: answers{"/cluster/sdn/vnets/vnet01/subnets": `[{"subnet":"zone01-192.0.2.0-24","gateway":"192.0.2.254"}]`},
			want: "changed sdn_subnet/vnet01/zone01-192.0.2.0-24 [gateway]", value: "192.0.2.254"},
		{kind: "sdn_ipam",
			base: answers{"/cluster/sdn/ipams": `[{"ipam":"netbox01","type":"netbox","url":"https://192.0.2.60","token":"s3cr3t"}]`},
			edit: answers{"/cluster/sdn/ipams": `[{"ipam":"netbox01","type":"netbox","url":"https://192.0.2.60","token":"n3wsecret"}]`},
			want: "changed sdn_ipam/netbox01 [token]", value: "n3wsecret"},
		{kind: "firewall_options",
			base:  answers{"/cluster/firewall/options": `{"enable":1,"policy_in":"DROP","digest":"o1"}`},
			noise: answers{"/cluster/firewall/options": `{"enable":true,"policy_in":"DROP","digest":"o2"}`},
			edit:  answers{"/cluster/firewall/options": `{"enable":1,"policy_in":"ACCEPT"}`},
			want:  "changed firewall_options/cluster [policy_in]", value: "ACCEPT"},
		{kind: "firewall_rule",
			base:  answers{"/cluster/firewall/rules": `[{"pos":0,"type":"in","action":"ACCEPT","dport":"22","digest":"f1"}]`},
			noise: answers{"/cluster/firewall/rules": `[{"pos":0,"type":"in","action":"ACCEPT","dport":"22","digest":"f2","ipversion":4}]`},
			edit:  answers{"/cluster/firewall/rules": `[{"pos":0,"type":"in","action":"ACCEPT","dport":"2222"}]`},
			want:  "changed firewall_rule/cluster [rules]", value: "2222",
			detail: `{"counts":{"rules":{"added":1,"removed":1}}}`},
		{kind: "firewall_alias",
			base:  answers{"/cluster/firewall/aliases": `[{"name":"WebNet","cidr":"192.0.2.0/24","digest":"a1","ipversion":4}]`},
			noise: answers{"/cluster/firewall/aliases": `[{"name":"WebNet","cidr":"192.0.2.0/24","digest":"a2","ipversion":6}]`},
			edit:  answers{"/cluster/firewall/aliases": `[{"name":"WebNet","cidr":"192.0.2.0/24","comment":"front"}]`},
			want:  "changed firewall_alias/webnet [comment]", value: "front"},
		{kind: "firewall_ipset_entry",
			base: answers{"/cluster/firewall/ipset": `[{"name":"blocked","digest":"s1"}]`,
				"/cluster/firewall/ipset/blocked": `[{"cidr":"192.0.2.0/24","digest":"e1"}]`},
			noise: answers{"/cluster/firewall/ipset/blocked": `[{"cidr":"192.0.2.0/24","digest":"e2","errors":{"cidr":"x"}}]`},
			edit:  answers{"/cluster/firewall/ipset/blocked": `[{"cidr":"192.0.2.0/24","nomatch":1}]`},
			want:  "changed firewall_ipset_entry/blocked/192.0.2.0/24 [nomatch]", value: `"1"`},
		{kind: "firewall_security_group_rule",
			base: answers{"/cluster/firewall/groups": `[{"group":"web","digest":"g1"}]`,
				"/cluster/firewall/groups/web": `[{"pos":0,"type":"in","action":"ACCEPT","dport":"443"}]`},
			edit: answers{"/cluster/firewall/groups/web": `[{"pos":0,"type":"in","action":"ACCEPT","dport":"8443"}]`},
			want: "changed firewall_security_group_rule/web [rules]", value: "8443"},
		{kind: "pve_token",
			base: answers{"/access/users?full=1": `[{"userid":"user01@pve","enable":1,"groups":"ops,admins",` +
				`"tokens":[{"tokenid":"token01","expire":0,"privsep":1}]}]`},
			noise: answers{"/access/users?full=1": `[{"userid":"user01@pve","enable":1,"groups":"admins,ops","totp-locked":true,` +
				`"tfa-locked-until":1700000000,"realm-type":"pve","tokens":[{"tokenid":"token01","expire":0,"privsep":1}]}]`},
			edit: answers{"/access/users?full=1": `[{"userid":"user01@pve","enable":1,"groups":"admins,ops",` +
				`"tokens":[{"tokenid":"token01","expire":0,"privsep":1,"comment":"ci"}]}]`},
			want: "changed pve_token/user01@pve!token01 [comment]", value: `"ci"`},
		{kind: "pve_role",
			base: answers{"/access/roles": `[{"roleid":"Auditors","privs":"Sys.Audit,VM.Audit","special":0},` +
				`{"roleid":"PVEAuditor","privs":"Sys.Audit","special":1}]`},
			noise: answers{"/access/roles": `[{"roleid":"Auditors","privs":"VM.Audit,Sys.Audit","special":0},` +
				`{"roleid":"PVEAuditor","privs":"Sys.Audit,VM.Audit","special":1}]`},
			edit: answers{"/access/roles": `[{"roleid":"Auditors","privs":"VM.Audit,Sys.Audit,Datastore.Audit","special":0},` +
				`{"roleid":"PVEAuditor","privs":"Sys.Audit,VM.Audit","special":1}]`},
			want: "changed pve_role/Auditors [privs]", value: "Datastore", members: "[privs:Datastore.Audit] []"},
		{kind: "pve_acl",
			base: answers{"/access/acl": `[{"path":"/vms/100","type":"user","ugid":"user01@pve","roleid":"PVEVMUser","propagate":1}]`},
			edit: answers{"/access/acl": `[{"path":"/vms/100","type":"user","ugid":"user01@pve","roleid":"PVEVMUser","propagate":1},` +
				`{"path":"/vms/100","type":"group","ugid":"ops","roleid":"PVEAuditor","propagate":0}]`},
			// A grant is its role, propagate and grantee, ids in clear, in
			// the member columns alone.
			want: "changed pve_acl//vms/100 [group_grants groups]", value: "PVEAuditor",
			members: "[group_grants:PVEAuditor:0:ops groups:ops] []"},
		{kind: "acme_plugin",
			base: answers{"/cluster/acme/plugins": `[{"plugin":"dns01","type":"dns","api":"cf","data":"QUJDREVG","digest":"p1"}]`},
			edit: answers{"/cluster/acme/plugins": `[{"plugin":"dns01","type":"dns","api":"cf","data":"WllYV1ZV","digest":"p2"}]`},
			want: "changed acme_plugin/dns01 [data]", value: "WllYV1ZV"},
		{kind: "vm",
			base: answers{pathGuest: `{"name":"linux01","memory":"2048","cores":2,"digest":"g1"}`},
			noise: answers{pathGuest: `{"name":"linux01","memory":2048,"cores":"2","digest":"g2","lock":"backup","parent":"snap1",` +
				`"vmstate":"store01:vm-100-state","runningmachine":"pc-q35-9.2","runningcpu":"host","running-nets-host-mtu":"net0=1500"}`},
			edit: answers{pathGuest: `{"name":"linux01","memory":4096,"cores":2,"cipassword":"**********"}`},
			want: "changed vm/100 [cipassword memory]", value: "4096"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			e := newObserveEnv(t)
			apply := func(a answers) {
				for path, data := range a {
					e.client.set(path, data)
				}
			}
			apply(tc.base)
			e.expect("baseline")
			apply(tc.noise)
			e.expect("what Proxmox rewrites by itself")
			apply(tc.edit)
			want := strings.Replace(tc.want, "{cluster}", e.cluster.ID.String(), 1)
			e.mq.observed.mu.Lock()
			e.mq.observed.changes = nil
			e.mq.observed.mu.Unlock()
			e.obs.ObserveAll(context.Background())
			e.obs.Wait()
			e.mq.observed.mu.Lock()
			rows := slices.Clone(e.mq.observed.changes)
			e.mq.observed.mu.Unlock()
			if got := e.mq.observed.takeChanges(); !slices.Equal(got, []string{want}) {
				t.Fatalf("edit: filed %q, want %q", got, want)
			}
			got := fmt.Sprint(rows[0].MembersJoined, rows[0].MembersLeft)
			if tc.members != "" && got != tc.members {
				t.Fatalf("members %s, want %s", got, tc.members)
			}
			if tc.members == "" && strings.Contains(got, tc.value) {
				t.Fatalf("the members carry the value %q: %s", tc.value, got)
			}
			for i := range rows {
				rows[i].MembersJoined, rows[i].MembersLeft = nil, nil // ids, in columns no reader selects
			}
			raw, _ := json.Marshal(rows)
			if strings.Contains(string(raw), tc.value) {
				t.Fatalf("the change row carries the value %q: %s", tc.value, raw)
			}
			if detail, _ := json.Marshal(rows[0].Detail); tc.detail != "" && string(detail) != tc.detail {
				t.Fatalf("detail %s, want %s", detail, tc.detail)
			}
			if !hasExplainRule(rows[0].ResourceType) {
				t.Fatalf("no rule can explain a %s change", rows[0].ResourceType)
			}
		})
	}
}

// The outcomes of a read, and what each leaves stored.
func TestObserverOutcomes(t *testing.T) {
	t.Parallel()
	refused := fmt.Errorf("%w: permission check failed", proxmox.ErrForbidden)
	t.Run("an object added and removed after the baseline", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.expect("baseline")
		store01 := `{"storage":"store01","type":"dir","path":"/srv/store01","content":"images,iso","nodes":"pve-01,pve-02"}`
		e.client.set(pathStorage, `[`+store01+`,{"storage":"store02","type":"nfs"}]`)
		e.expect("added", "added storage/store02 []")
		e.client.set(pathStorage, `[{"storage":"store02","type":"nfs"}]`)
		e.expect("removed", "removed storage/store01 []")
	})
	t.Run("a failed read changes nothing; the next compares from the last reading", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.expect("baseline")
		if e.mq.observed.shown == 0 {
			t.Fatal("a pass did not show what nothing classified")
		}
		before := time.Now()
		e.client.fail(pathStorage, errors.New("proxmox API error 500"))
		e.expect("failed read")
		// An answer that is no list fails the read too, however long it lasts.
		e.client.set(pathStorage, `null`)
		e.expect("no list")
		e.expect("still no list")
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","path":"/srv/store01","content":"images,iso","nodes":"pve-01"}]`)
		e.obs.ObserveAll(context.Background())
		e.obs.Wait()
		e.mq.observed.mu.Lock()
		got := slices.Clone(e.mq.observed.changes)
		e.mq.observed.mu.Unlock()
		if len(got) != 1 || !got[0].LastSeenAt.Before(before) {
			t.Fatalf("after the failure: %+v, want one change whose window opens before it", got)
		}
	})
	t.Run("a reading as stored touches nothing, and a change after it opens there", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.expect("baseline")
		stored := func() time.Time {
			e.mq.observed.mu.Lock()
			defer e.mq.observed.mu.Unlock()
			return e.mq.observed.state[e.cluster.ID.String()+"/storage/store01"].LastSeenAt
		}
		first, second := stored(), time.Now()
		e.expect("as it was")
		if !stored().Equal(first) {
			t.Fatal("a reading as stored touched the object: the kind's mark is its time")
		}
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","path":"/srv/store01","content":"images","nodes":"pve-01,pve-02"}]`)
		if rows := e.passRows(); len(rows) != 1 || rows[0].LastSeenAt.Before(second) {
			t.Fatalf("filed %+v, want one change whose window opens at the reading before it", rows)
		}
	})
	t.Run("an empty answer is held a pass", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.expect("baseline")
		e.client.set(pathStorage, `[]`)
		e.expect("held")
		e.expect("believed", "removed storage/store01 []")
	})
	t.Run("a kind Proxmox lacks, or refuses, is not read again", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		delete(e.client.answers, "/cluster/mapping/usb")
		e.client.fail("/cluster/mapping/pci", refused)
		e.expect("first")
		e.expect("second")
		if usb, pci := e.client.readsOf("/cluster/mapping/usb"), e.client.readsOf("/cluster/mapping/pci"); usb != 1 || pci != 1 {
			t.Fatalf("read the skipped kinds %d and %d times, want once each", usb, pci)
		}
	})
	t.Run("nothing is read when the token's probe fails", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.client.fail(pathPerms, proxmox.ErrConnectionFailed)
		e.expect("unreachable")
		if n := e.client.readsOf(pathStorage); n != 0 {
			t.Fatalf("read storage %d times", n)
		}
	})
	t.Run("cluster options are not read without Sys.Audit on /", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.client.set(pathPerms, `{"/":{"VM.Audit":1}}`)
		e.expect("no Sys.Audit")
		if n := e.client.readsOf("/cluster/options"); n != 0 {
			t.Fatalf("read the options %d times", n)
		}
	})
	t.Run("an upgrade is compared across, what moved filed as one, during it; a privilege change is filed, and re-baselines only the kind it gates", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		plugins := func(data string) {
			e.client.set("/cluster/acme/plugins", `[{"plugin":"dns01","type":"dns","api":"cf","data":"`+data+`"}]`)
		}
		perms := func(extra string) {
			e.client.set(pathPerms, `{"/":{"Sys.Audit":1,"VM.Audit":1,"Datastore.Audit":1,"Pool.Audit":1,"Mapping.Audit":1`+extra+`}}`)
		}
		plugins("QUJD")
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","nodes":"pve-01"},{"storage":"store02","type":"dir"}]`)
		e.expect("baseline")
		e.mq.nodesByCluster[0].PveVersion = "pve-manager/9.2.21/0000bbbb"
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","nodes":"pve-02"},{"storage":"store02","type":"dir","shared":1}]`)
		e.client.set(pathGuest, `{"name":"linux01","memory":"4096"}`)
		rows := e.passRows()
		var bulk []any
		for _, r := range rows {
			if r.Detail["during_upgrade"] != true {
				t.Errorf("upgraded: filed %+v, want each marked during_upgrade", r)
			}
			if r.ResourceType == "storage" {
				bulk = append(bulk, r.Detail["bulk"])
			}
		}
		if len(rows) != 3 || len(bulk) != 2 || bulk[0] == nil || bulk[0] != bulk[1] {
			t.Fatalf("upgraded: filed %+v, want both storages' edits as one and the guest's", rows)
		}
		e.expect("and the pass after, as any other")
		token := "changed cluster/" + e.cluster.ID.String() + " [token_privileges]"
		perms(`,"Sys.PowerMgmt":1`)
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","nodes":"pve-03"},{"storage":"store02","type":"dir","shared":1}]`)
		e.expect("a privilege granted", token, "changed storage/store01 [nodes]")
		perms(`,"Sys.PowerMgmt":1,"Sys.Modify":1`)
		plugins("WllY")
		e.expect("the privilege the plugins need granted", token)
		plugins("Q0RF")
		e.expect("then an edit", "changed acme_plugin/dns01 [data]")
	})
	t.Run("a privilege change in an upgrade's pass is filed, and says so", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.expect("baseline")
		e.mq.nodesByCluster[0].PveVersion = "pve-manager/9.2.21/0000bbbb"
		e.client.set(pathPerms, `{"/":{"Sys.Audit":1,"VM.Audit":1,"Datastore.Audit":1,"Pool.Audit":1,"Mapping.Audit":1,"Sys.PowerMgmt":1}}`)
		e.obs.ObserveAll(context.Background())
		e.obs.Wait()
		e.mq.observed.mu.Lock()
		defer e.mq.observed.mu.Unlock()
		if got := e.mq.observed.changes; len(got) != 1 || got[0].Fields[0] != "token_privileges" ||
			got[0].Detail["during_upgrade"] != true || got[0].Detail["notice"] != "token_privileges" ||
			!got[0].LastSeenAt.Equal(got[0].ObservedAt) {
			t.Fatalf("filed %+v, want a notice that the token's privileges changed during an upgrade", got)
		}
	})
	t.Run("a node down, or back and not read yet, re-baselines nothing", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.mq.nodesByCluster = append(e.mq.nodesByCluster, db.Node{ID: uuid.New(), ClusterID: e.cluster.ID,
			Name: "pve-02", Status: "online", PveVersion: "pve-manager/9.2.21/0000bbbb", KernelVersion: "6.14.8-2-pve"})
		e.expect("baseline")
		down := &e.mq.nodesByCluster[1]
		down.Status, down.PveVersion, down.KernelVersion = "offline", "", ""
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","path":"/srv/store01","content":"images,iso","nodes":"pve-01"}]`)
		e.expect("a node down", "changed storage/store01 [nodes]")
		back := &e.mq.nodesByCluster[0]
		back.PveVersion, back.KernelVersion = "", ""
		e.client.set("/nodes/pve-01/dns", `{"search":"example.com","dns1":"192.0.2.55"}`)
		e.expect("a node back, its versions not read yet")
		back.PveVersion, back.KernelVersion = "pve-manager/9.2.20/0000aaaa", "6.14.8-2-pve"
		e.expect("and read", "changed node/pve-01 [dns1]")
	})
	t.Run("a node never read holds nothing back, and is filed once a while on", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.mq.nodesByCluster = append(e.mq.nodesByCluster, db.Node{ID: uuid.New(), ClusterID: e.cluster.ID,
			Name: "pve-02", Status: "offline"})
		e.expect("baseline")
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","path":"/srv/store01","content":"images,iso","nodes":"pve-01"}]`)
		e.expect("an edit beside it", "changed storage/store01 [nodes]")
		for pass := 3; pass < observeUnreadNodePasses; pass++ {
			e.expect("pass " + strconv.Itoa(pass))
		}
		// Its notice refused while a kind writes a gate of its own in the same
		// pass: the notice is still the next pass's.
		e.mq.observed.mu.Lock()
		e.mq.observed.fail = map[string]int{observedNoticesMark: 1}
		e.mq.observed.mu.Unlock()
		e.obs.maxObjects = 1
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","path":"/srv/store01","content":"images,iso","nodes":"pve-01"},`+
			`{"storage":"store02","type":"dir"}]`)
		e.passRows()
		e.expect("filed", "changed cluster/"+e.cluster.ID.String()+" [nodes]")
		e.expect("once")
		e.restart()
		for pass := 1; pass <= observeUnreadNodePasses; pass++ {
			e.expect("after a restart, pass " + strconv.Itoa(pass))
		}
	})
	t.Run("IP set entries as the set lists them, one each", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.client.set("/cluster/firewall/ipset", `[{"name":"blocked"}]`)
		e.client.set("/cluster/firewall/ipset/blocked", `[{"cidr":"192.0.2.5/24"},{"cidr":"192.0.2.0/24"},{"cidr":"192.0.2.0/24"}]`)
		e.expect("baseline")
		e.client.set("/cluster/firewall/ipset/blocked", `[{"cidr":"192.0.2.5/24","comment":"x"},{"cidr":"192.0.2.0/24"}]`)
		e.expect("an edit", "changed firewall_ipset_entry/blocked/192.0.2.5/24 [comment]")
	})
	t.Run("a Proxmox VE without the pool query skips the pools", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.client.set("/pools", `[{"poolid":"web"}]`)
		e.client.fail("/pools?poolid=web", &proxmox.APIError{StatusCode: http.StatusBadRequest, Message: "poolid: property is not defined in schema"})
		e.expect("refused")
		e.expect("skipped")
		if n := e.client.readsOf("/pools"); n != 1 {
			t.Fatalf("pools listed %d times, want once", n)
		}
		// An upgrade can bring what the release lacked: they are tried again.
		e.mq.nodesByCluster[0].PveVersion = "pve-manager/9.2.21/0000bbbb"
		e.expect("upgraded")
		if n := e.client.readsOf("/pools"); n != 2 {
			t.Fatalf("pools listed %d times by the pass after an upgrade, want twice", n)
		}
	})
	t.Run("a node that left the cluster is forgotten", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 1 // pve-02's interfaces held by presence, with a gate
		e.mq.nodesByCluster = append(e.mq.nodesByCluster, db.Node{ID: uuid.New(), ClusterID: e.cluster.ID,
			Name: "pve-02", Status: "online", PveVersion: "pve-manager/9.2.20/0000aaaa", KernelVersion: "6.14.8-2-pve"})
		e.client.set("/nodes/pve-02/network", `[{"iface":"vmbr0","type":"bridge","cidr":"192.0.2.12/24"},{"iface":"vmbr1","type":"bridge"}]`)
		held := func() []string {
			e.mq.observed.mu.Lock()
			defer e.mq.observed.mu.Unlock()
			out := slices.Collect(maps.Keys(e.mq.observed.state))
			out = slices.AppendSeq(out, maps.Keys(e.mq.observed.presence))
			out = append(out, e.mq.observed.marks[e.cluster.ID.String()+"/"+observedNoticesMark].Signature)
			return slices.DeleteFunc(out, func(k string) bool { return !strings.Contains(k, "pve-02") })
		}
		e.passRows()
		if len(held()) < 3 {
			t.Fatalf("held of pve-02 %q, want its interfaces and its gate", held())
		}
		e.mq.nodesByCluster = e.mq.nodesByCluster[:1]
		e.expect("the node gone")
		if got := held(); len(got) != 0 {
			t.Fatalf("still held: %q", got)
		}
	})
	t.Run("a cluster that stops answering ends the pass", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.client.failAll = fmt.Errorf("read response body: %w", context.DeadlineExceeded)
		e.pass()
		e.client.mu.Lock()
		reads := 0
		for _, n := range e.client.reads {
			reads += n
		}
		e.client.mu.Unlock()
		if reads > 1+observeAbortAfter+observeMaxInFlight {
			t.Fatalf("%d reads of a cluster that answers none", reads)
		}
	})
	t.Run("one hung node is skipped for the pass, and the others' objects and guests are read", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		hung := fmt.Errorf("read response body: %w", context.DeadlineExceeded)
		for _, name := range []string{"pve-02", "pve-03"} {
			n := db.Node{ID: uuid.New(), ClusterID: e.cluster.ID, Name: name, Status: "online",
				PveVersion: e.node.PveVersion, KernelVersion: e.node.KernelVersion}
			e.mq.nodesByCluster = append(e.mq.nodesByCluster, n)
			for vmid := range 3 {
				id := 200 + 10*len(e.mq.nodesByCluster) + vmid
				e.mq.vmsByCluster = append(e.mq.vmsByCluster, db.Vm{ID: uuid.New(), ClusterID: e.cluster.ID, NodeID: n.ID,
					Vmid: int32(id), Type: "qemu"})
				e.client.set(fmt.Sprintf("/nodes/%s/qemu/%d/config", name, id), `{"memory":"1"}`)
			}
			for part, data := range map[string]string{"network": `[]`, "config": `{}`, "dns": `{}`, "time": `{}`,
				"firewall/rules": `[]`} {
				e.client.set("/nodes/"+name+"/"+part, data)
			}
		}
		for path := range e.client.answers {
			if strings.HasPrefix(path, "/nodes/pve-02/") {
				e.client.fail(path, hung)
			}
		}
		e.pass()
		pve02 := 0
		e.client.mu.Lock()
		for path, n := range e.client.reads {
			if strings.HasPrefix(path, "/nodes/pve-02/") {
				pve02 += n
			}
		}
		e.client.mu.Unlock()
		if pve02 > observeNodeSkipAfter+observeMaxInFlight {
			t.Errorf("the hung node was read %d times in a pass", pve02)
		}
		for _, path := range []string{pathStorage, "/nodes/pve-03/network", "/nodes/pve-03/qemu/231/config", pathGuest} {
			if e.client.readsOf(path) != 1 {
				t.Errorf("%s read %d times beside the hung node, want once", path, e.client.readsOf(path))
			}
		}
	})
	t.Run("more changes of a kind than are shown one by one", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		storages := func(n int, nodes string) string {
			items := make([]string, n)
			for i := range items {
				items[i] = fmt.Sprintf(`{"storage":"store%02d","type":"dir","nodes":"%s"}`, i, nodes)
			}
			return "[" + strings.Join(items, ",") + "]"
		}
		e.client.set(pathStorage, storages(observeMaxChanges+1, "pve-01"))
		e.expect("baseline")
		e.client.set(pathStorage, storages(observeMaxChanges+1, "pve-02"))
		e.obs.ObserveAll(context.Background())
		e.obs.Wait()
		e.mq.observed.mu.Lock()
		bulk := map[any]int{}
		var size any
		for _, c := range e.mq.observed.changes {
			bulk[c.Detail["bulk"]]++
			size = c.Detail["bulk_size"]
		}
		e.mq.observed.changes = nil
		e.mq.observed.mu.Unlock()
		if id, _ := slices.Collect(maps.Keys(bulk))[0].(string); len(bulk) != 1 || id == "" ||
			bulk[id] != observeMaxChanges+1 || size != float64(observeMaxChanges+1) {
			t.Fatalf("each one edited: %v of size %v, want every change filed and marked as one bulk", bulk, size)
		}
		e.client.set(pathStorage, strings.Replace(storages(observeMaxChanges+1, "pve-01"), `"pve-01"`, `"pve-02"`, 1))
		e.obs.ObserveAll(context.Background())
		e.obs.Wait()
		e.mq.observed.mu.Lock()
		for _, c := range e.mq.observed.changes {
			if c.Detail["bulk"] != nil {
				t.Errorf("%d changes, one marked bulk: %+v", observeMaxChanges, c)
			}
		}
		e.mq.observed.changes = nil
		e.mq.observed.mu.Unlock()
	})
	storages := func(ids ...string) string {
		items := make([]string, 0, len(ids))
		for _, id := range ids {
			items = append(items, `{"storage":"`+id+`","type":"dir","nodes":"pve-02"}`)
		}
		return "[" + strings.Join(items, ",") + "]"
	}
	// pastCap is the one row a pass gave past the cap, failing on more than one.
	pastCap := func(t *testing.T, rows []observedChangeRow) observedChangeRow {
		t.Helper()
		var found []observedChangeRow
		for _, r := range rows {
			if r.Detail["notice"] == "changed_past_cap" {
				found = append(found, r)
			}
		}
		if len(found) != 1 {
			t.Fatalf("filed %+v, want one row of what moved past the cap", rows)
		}
		return found[0]
	}
	counted := func(t *testing.T, r observedChangeRow, added, removed, changed []string) {
		t.Helper()
		for name, want := range map[string][]string{"added": added, "removed": removed, "changed": changed} {
			ids, _ := r.Detail[name+"_ids"].([]any)
			var got []string
			for _, id := range ids {
				got = append(got, id.(string))
			}
			if r.Detail[name] != float64(len(want)) || !slices.Equal(got, want) {
				t.Errorf("%s: %v %v, want %d %v", name, r.Detail[name], got, len(want), want)
			}
		}
		if r.Detail["while_over_cap"] != true || len(r.MembersJoined)+len(r.MembersLeft) != 0 {
			t.Errorf("past the cap: %+v, want while_over_cap and no member named", r)
		}
	}
	t.Run("past the cap a kind is counted by presence, one row a pass, and a pass that finds it as it was writes nothing", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 3
		e.client.set(pathStorage, `[{"storage":"s0","type":"dir","nodes":"pve-01"},{"storage":"s9","type":"dir"}]`)
		e.expect("baseline")
		// Crossing: what was held is compared field by field one last time,
		// gone included, and what is new is counted.
		e.client.set(pathStorage, storages("s0", "s1", "s2", "s3"))
		rows := e.passRows()
		counted(t, pastCap(t, rows), []string{"s1", "s2", "s3"}, nil, nil)
		own := func(id, change string) bool {
			return slices.ContainsFunc(rows, func(r observedChangeRow) bool { return r.ResourceID == id && r.Change == change })
		}
		if len(rows) != 4 || !own("s0", "changed") || !own("s9", "removed") {
			t.Fatalf("crossing: filed %+v, want s0's and s9's own rows, the count and the notice", rows)
		}
		writes := func() int {
			e.mq.observed.mu.Lock()
			defer e.mq.observed.mu.Unlock()
			return e.mq.observed.writes["storage"]
		}
		before := writes()
		e.expect("as it was")
		e.restart()
		e.expect("and after a restart")
		if n := writes() - before; n != 0 {
			t.Fatalf("two passes that found it as it was wrote %d times, want none", n)
		}
		// One came, one went, one changed: one row, exact.
		e.client.set(pathStorage, `[{"storage":"s1","type":"dir","nodes":"pve-03"},{"storage":"s2","type":"dir","nodes":"pve-02"},`+
			`{"storage":"s3","type":"dir","nodes":"pve-02"},{"storage":"s4","type":"dir","nodes":"pve-02"}]`)
		counted(t, pastCap(t, e.passRows()), []string{"s4"}, []string{"s0"}, []string{"s1"})
		// Back under: each object is compared with the rows kept since the
		// crossing and named (s0 went, s1 and s2 came), and only what went that
		// no rows were kept of is counted.
		e.client.set(pathStorage, storages("s1", "s2"))
		rows = e.passRows()
		counted(t, pastCap(t, rows), nil, []string{"s3", "s4"}, nil)
		for _, want := range [][2]string{{"s0", "removed"}, {"s1", "added"}, {"s2", "added"}} {
			if !slices.ContainsFunc(rows, func(r observedChangeRow) bool {
				return r.ResourceID == want[0] && r.Change == want[1] && r.Detail["while_over_cap"] == true
			}) {
				t.Errorf("back under: filed %+v, want %s %s while over", rows, want[1], want[0])
			}
		}
		if !slices.ContainsFunc(rows, func(r observedChangeRow) bool { return r.Detail["notice"] == "back_under_cap" }) {
			t.Fatalf("back under: %+v, want the notice", rows)
		}
		e.client.set(pathStorage, storages("s1", "s2", "s3"))
		e.expect("then an addition, its own", "added storage/s3 []")
	})
	t.Run("past the cap a flood is counted exactly, and names a few", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 2
		e.client.set(pathStorage, storages("s1"))
		e.expect("baseline")
		flood := make([]string, 0, observePastCapNamed+101)
		flood = append(flood, "s1")
		for i := range observePastCapNamed + 100 {
			flood = append(flood, fmt.Sprintf("f%04d", i))
		}
		e.client.set(pathStorage, storages(flood...))
		r := pastCap(t, e.passRows())
		if ids, _ := r.Detail["added_ids"].([]any); r.Detail["added"] != float64(observePastCapNamed+100) || len(ids) != observePastCapNamed {
			t.Fatalf("a flood of %d: counted %v, named %d, want every one counted and %d named",
				observePastCapNamed+100, r.Detail["added"], len(ids), observePastCapNamed)
		}
	})
	t.Run("a kind first read over the cap takes that reading as its baseline, and counts from the next", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 1
		e.client.set(pathStorage, storages("s1", "s2"))
		e.expect("over from its first reading", "changed storage/"+e.cluster.ID.String()+" [storage]")
		e.client.set(pathStorage, storages("s1", "s3"))
		counted(t, pastCap(t, e.passRows()), []string{"s3"}, []string{"s2"}, nil)
	})
	t.Run("past the cap a parent's objects are counted apart, and a parent gone files one row", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 2
		entries := func(cidrs ...string) string {
			items := make([]string, 0, len(cidrs))
			for _, c := range cidrs {
				items = append(items, `{"cidr":"`+c+`"}`)
			}
			return "[" + strings.Join(items, ",") + "]"
		}
		e.client.set("/cluster/firewall/ipset", `[{"name":"allow"},{"name":"block"}]`)
		e.client.set("/cluster/firewall/ipset/allow", entries("192.0.2.1"))
		e.client.set("/cluster/firewall/ipset/block", entries("192.0.2.10", "192.0.2.11"))
		e.expect("baseline")
		e.client.set("/cluster/firewall/ipset/allow", entries("192.0.2.1", "192.0.2.2"))
		e.client.set("/cluster/firewall/ipset/block", entries("192.0.2.10", "192.0.2.11", "192.0.2.12"))
		rows := e.passRows()
		r := pastCap(t, rows)
		counted(t, r, []string{"block/192.0.2.12"}, nil, nil)
		if r.Detail["parent"] != "block" || !slices.ContainsFunc(rows, func(r observedChangeRow) bool {
			return r.ResourceID == "allow/192.0.2.2" && r.Change == "added" && r.Detail["while_over_cap"] == nil
		}) {
			t.Fatalf("filed %+v, want block's count and allow's entry as any addition", rows)
		}
		// A set gone takes its entries with it: one row, saying how many, and
		// nothing of them held.
		e.client.set("/cluster/firewall/ipset", `[{"name":"allow"}]`)
		rows = e.passRows()
		if len(rows) != 1 || rows[0].ResourceID != "block" || rows[0].Change != "removed" || rows[0].Detail["children"] != float64(3) {
			t.Fatalf("a set gone: filed %+v, want its removal alone, of its 3 entries", rows)
		}
		e.mq.observed.mu.Lock()
		defer e.mq.observed.mu.Unlock()
		for key := range e.mq.observed.presence {
			if strings.Contains(key, "/firewall_ipset_entry/block/") {
				t.Errorf("still held: %s", key)
			}
		}
	})
	t.Run("past the kind's ceiling every parent's objects are counted, under the kind's one notice", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects, e.obs.maxKind = 3, 4
		e.client.set("/cluster/firewall/ipset", `[{"name":"allow"},{"name":"block"}]`)
		e.client.set("/cluster/firewall/ipset/allow", `[{"cidr":"192.0.2.1"}]`)
		e.client.set("/cluster/firewall/ipset/block", `[{"cidr":"192.0.2.10"},{"cidr":"192.0.2.11"}]`)
		e.expect("baseline")
		e.client.set("/cluster/firewall/ipset/allow", `[{"cidr":"192.0.2.1"},{"cidr":"192.0.2.2"}]`)
		e.client.set("/cluster/firewall/ipset/block", `[{"cidr":"192.0.2.10"},{"cidr":"192.0.2.11"},{"cidr":"192.0.2.12"}]`)
		var notices, counts []string
		for _, r := range e.passRows() {
			switch r.Detail["notice"] {
			case "too_many_objects":
				notices = append(notices, fmt.Sprintf("%v %v %v", r.Detail["parent"], r.Detail["objects"], r.Detail["cap"]))
			case "changed_past_cap":
				counts = append(counts, fmt.Sprintf("%v %v", r.Detail["parent"], r.Detail["added_ids"]))
			}
		}
		slices.Sort(counts)
		if !slices.Equal(notices, []string{"<nil> 5 4"}) ||
			!slices.Equal(counts, []string{"allow [allow/192.0.2.2]", "block [block/192.0.2.12]"}) {
			t.Fatalf("past the ceiling: notices %q, counts %q, want the kind's one notice and each set's count", notices, counts)
		}
		e.client.set("/cluster/firewall/ipset/allow", `[{"cidr":"192.0.2.1"}]`)
		var under []any
		for _, r := range e.passRows() {
			if r.Detail["notice"] == "back_under_cap" {
				under = append(under, r.Detail["parent"])
			}
		}
		if len(under) != 1 || under[0] != nil {
			t.Fatalf("back under the ceiling: notices of %v, want the kind's alone", under)
		}
		e.client.set("/cluster/firewall/ipset/block", `[{"cidr":"192.0.2.10"},{"cidr":"192.0.2.11"}]`)
		e.expect("back under, field by field", "removed firewall_ipset_entry/block/192.0.2.12 []")
	})
	t.Run("a token new as the users come back under the cap is new, not one that appeared while over", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxAccess = 1
		users := func(tokens string, more ...string) string {
			return `[{"userid":"user01@pve","tokens":[` + tokens + `]}` + strings.Join(more, "") + `]`
		}
		e.client.set("/access/users?full=1", users(``))
		e.expect("baseline")
		e.client.set("/access/users?full=1", users(``, `,{"userid":"user02@pve"}`))
		counted(t, pastCap(t, e.passRows()), []string{"user02@pve"}, nil, nil) // the access kinds' own cap
		before := time.Now()
		e.passRows()
		// The users' listing names the tokens: they were read all the while.
		e.client.set("/access/users?full=1", users(`{"tokenid":"ci"}`))
		for _, r := range e.passRows() {
			if r.ResourceType == "pve_token" && (r.Detail["while_over_cap"] != nil || r.LastSeenAt.Before(before)) {
				t.Fatalf("filed %+v, want a token added since the last reading", r)
			}
		}
	})
	t.Run("past its kind's cap a set kept field by field has its entries read, one added read shallow is named back under with its own", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 2
		e.client.set("/cluster/firewall/ipset", `[{"name":"allow"},{"name":"block"}]`)
		e.client.set("/cluster/firewall/ipset/allow", `[{"cidr":"192.0.2.1"}]`)
		e.client.set("/cluster/firewall/ipset/block", `[{"cidr":"192.0.2.10"}]`)
		e.expect("baseline")
		before := time.Now()
		e.client.set("/cluster/firewall/ipset", `[{"name":"allow"},{"name":"block"},{"name":"extra"}]`)
		e.client.set("/cluster/firewall/ipset/extra", `[{"cidr":"192.0.2.20"}]`)
		e.passRows()
		if n := e.client.readsOf("/cluster/firewall/ipset/extra"); n != 0 {
			t.Fatalf("read the entries of a set read shallow %d times, want none", n)
		}
		e.client.set("/cluster/firewall/ipset/allow", `[{"cidr":"192.0.2.1"},{"cidr":"192.0.2.2"}]`)
		e.expect("over, a kept set's entries compared", "added firewall_ipset_entry/allow/192.0.2.2 []")
		e.client.set("/cluster/firewall/ipset", `[{"name":"allow"},{"name":"extra"}]`)
		named := map[string]observedChangeRow{}
		for _, r := range e.passRows() {
			if r.Detail["notice"] == nil {
				named[r.Change+" "+r.ResourceType+"/"+r.ResourceID] = r
			}
		}
		for _, want := range []string{"removed firewall_ipset/block", "added firewall_ipset/extra", "added firewall_ipset_entry/extra/192.0.2.20"} {
			if r, ok := named[want]; !ok || r.Detail["while_over_cap"] != true || !r.LastSeenAt.Before(before) {
				t.Errorf("back under: filed %v, want %s while over, dated from before", slices.Collect(maps.Keys(named)), want)
			}
		}
		if len(named) != 3 || named["removed firewall_ipset/block"].Detail["children"] != float64(1) {
			t.Errorf("back under: filed %+v, want those three, block's removal counting its entry", named)
		}
	})
	t.Run("a cluster no longer active leaves no presence held", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 0
		e.passRows()
		held := func() int {
			e.obs.mu.Lock()
			defer e.obs.mu.Unlock()
			return len(e.obs.presence)
		}
		before := held()
		e.mq.clusters = nil
		e.passRows()
		if before != 1 || held() != 0 {
			t.Fatalf("held for %d clusters, then %d, want 1 then none", before, held())
		}
	})
	t.Run("a write whose reply is lost is read back, not counted again", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 2
		e.client.set(pathStorage, storages("s1"))
		e.expect("baseline")
		e.client.set(pathStorage, storages("s1", "s2", "s3"))
		e.mq.observed.mu.Lock()
		e.mq.observed.lose = map[string]int{"storage": 1}
		e.mq.observed.mu.Unlock()
		e.passRows()
		e.expect("the next pass, held as written")
	})
	t.Run("a gate a lost write wrote is read back before the next kind writes the gates", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 1
		e.client.set("/pools", `[{"poolid":"web"}]`)
		e.client.set("/pools?poolid=web", `[{"poolid":"web","members":[]}]`)
		e.client.set(pathStorage, storages("s1"))
		e.expect("baseline")
		e.client.set("/pools", `[{"poolid":"web"},{"poolid":"p0"}]`)
		e.client.set(pathStorage, storages("s1", "s2"))
		e.mq.observed.mu.Lock()
		e.mq.observed.lose = map[string]int{"pool": 1}
		e.mq.observed.mu.Unlock()
		e.passRows()
		e.mq.observed.mu.Lock()
		gates := e.mq.observed.marks[e.cluster.ID.String()+"/"+observedNoticesMark].Signature
		e.mq.observed.mu.Unlock()
		for _, key := range []string{"too_many_objects/pool/", "too_many_objects/storage/"} {
			if !strings.Contains(gates, `"`+key+`"`) {
				t.Errorf("gates %s, want %s among them", gates, key)
			}
		}
		e.expect("and none filed again")
	})
	t.Run("past the cap a field change is counted, and an upgrade meanwhile changes nothing", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 2
		e.client.set(pathStorage, storages("s1"))
		e.expect("baseline")
		e.client.set(pathStorage, storages("s1", "s2", "s3"))
		e.passRows()
		e.mq.nodesByCluster[0].PveVersion = "pve-manager/9.2.21/0000bbbb"
		e.expect("an upgrade: compared across, nothing moved")
		e.client.set(pathStorage, `[{"storage":"s1","type":"dir","nodes":"pve-02"},{"storage":"s2","type":"dir","nodes":"pve-02"},`+
			`{"storage":"s3","type":"dir","nodes":"pve-01"}]`)
		counted(t, pastCap(t, e.passRows()), nil, nil, []string{"s3"})
	})
	t.Run("every notice is filed under its kind's own type, the cluster's own under the cluster", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 0 // every kind listing an object is past its cap, and the guests are read in turns
		rows := e.passRows()
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir"},{"storage":"store02","type":"dir"}]`)
		e.client.set(pathNet, `[{"iface":"vmbr0","type":"bridge"},{"iface":"vmbr1","type":"bridge"}]`)
		rows = append(rows, e.passRows()...)
		e.obs.maxObjects = 10
		e.restart()
		rows = append(rows, e.passRows()...)
		clusters := map[string]bool{"token_privileges": true, "node_never_read": true, "guests_read_in_turns": true}
		filed := map[string]bool{}
		for _, r := range rows {
			notice, _ := r.Detail["notice"].(string)
			if notice == "" {
				continue
			}
			want := removedObject(r.Fields[0], "", "").resourceType
			if clusters[notice] {
				want = observedClusterType
			}
			if r.ResourceType != want || r.Detail["node"] != nil && r.Node != r.Detail["node"] {
				t.Errorf("%s of %v filed under %s (node %q), want %s", notice, r.Fields, r.ResourceType, r.Node, want)
			}
			filed[notice+" "+r.ResourceType+"/"+r.ResourceID] = true
		}
		id := e.cluster.ID.String()
		for _, want := range []string{"too_many_objects storage/" + id, "changed_past_cap network/pve-01",
			"back_under_cap node/pve-01", "guests_read_in_turns cluster/" + id} {
			if !filed[want] {
				t.Errorf("no %s among %v", want, slices.Sorted(maps.Keys(filed)))
			}
		}
	})
	t.Run("a back-under pass whose write is refused is the next pass's, once", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 2
		e.client.set(pathStorage, storages("s1"))
		e.expect("baseline")
		e.client.set(pathStorage, storages("s1", "s2", "s3"))
		e.passRows()
		e.client.set(pathStorage, storages("s1"))
		e.mq.observed.mu.Lock()
		e.mq.observed.fail = map[string]int{"storage": 1}
		e.mq.observed.mu.Unlock()
		e.expect("back under, the write refused")
		under := 0
		for _, r := range e.passRows() {
			if r.Detail["notice"] == "back_under_cap" {
				under++
			}
		}
		if under != 1 {
			t.Fatalf("the next pass filed %d back_under_cap, want 1", under)
		}
		e.expect("and not again")
	})
	t.Run("past the cap a pool kept field by field is tagged with its members, and back under what moved in it is named", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 2
		e.client.set("/pools", `[{"poolid":"web"},{"poolid":"old"}]`)
		e.client.set("/pools?poolid=web", `[{"poolid":"web","members":[]}]`)
		e.client.set("/pools?poolid=old", `[{"poolid":"old","members":[]}]`)
		e.expect("baseline")
		before := time.Now()
		e.client.set("/pools", `[{"poolid":"web"},{"poolid":"old"},{"poolid":"p0"},{"poolid":"p1"}]`)
		counted(t, pastCap(t, e.passRows()), []string{"p0", "p1"}, nil, nil)
		// The listing says nothing of a pool's members; its own read does.
		e.client.set("/pools?poolid=web", `[{"poolid":"web","members":[{"type":"qemu","vmid":100}]}]`)
		counted(t, pastCap(t, e.passRows()), nil, nil, []string{"web"})
		// Back under, old gone with the flood and web's own read failing: old's
		// removal is named, not counted, and web waits, still dated from before
		// the flood, for the pass that reads it.
		e.client.set("/pools", `[{"poolid":"web"}]`)
		e.client.fail("/pools?poolid=web", &proxmox.APIError{StatusCode: http.StatusInternalServerError, Message: "got timeout"})
		rows := e.passRows()
		counted(t, pastCap(t, rows), nil, []string{"p0", "p1"}, nil)
		if !slices.ContainsFunc(rows, func(r observedChangeRow) bool {
			return r.ResourceID == "old" && r.Change == "removed" && r.Detail["while_over_cap"] == true
		}) || slices.ContainsFunc(rows, func(r observedChangeRow) bool { return r.ResourceID == "web" }) {
			t.Fatalf("back under, web unread: filed %+v, want old's removal named and nothing of web", rows)
		}
		e.client.set("/pools?poolid=web", `[{"poolid":"web","members":[{"type":"qemu","vmid":100}]}]`)
		rows = e.passRows()
		if len(rows) != 1 || rows[0].ResourceID != "web" || rows[0].Change != "changed" || !slices.Equal(rows[0].MembersJoined, []string{"vms:100"}) ||
			rows[0].Detail["while_over_cap"] != true || !rows[0].LastSeenAt.Before(before) {
			t.Fatalf("back under: filed %+v, want web's new member named, dated from before the flood", rows)
		}
		e.expect("and then as any other")
	})
	t.Run("back under in the pass its kind's signature moves, what came while over is still filed", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 1
		perms := func(modify string) {
			e.client.set(pathPerms, `{"/":{"Sys.Audit":1,"VM.Audit":1,"Datastore.Audit":1,"Pool.Audit":1,"Mapping.Audit":1,"Sys.Modify":`+modify+`}}`)
		}
		plugins := func(ids ...string) {
			items := make([]string, 0, len(ids))
			for _, id := range ids {
				items = append(items, `{"plugin":"`+id+`","type":"dns","api":"cf"}`)
			}
			e.client.set("/cluster/acme/plugins", "["+strings.Join(items, ",")+"]")
		}
		perms("1")
		plugins("dns01")
		e.expect("baseline")
		plugins("dns01", "dns02")
		e.passRows()
		// Sys.Modify kept, no longer propagated: the plugins' signature moves.
		perms("0")
		plugins("dns02")
		rows := e.passRows()
		if !slices.ContainsFunc(rows, func(r observedChangeRow) bool {
			return r.ResourceID == "dns02" && r.Change == "added" && r.Detail["while_over_cap"] == true
		}) {
			t.Fatalf("back under: filed %+v, want dns02 added while over", rows)
		}
	})
	t.Run("past the cap a reader reads on only what is held, and guests are read in turns", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.obs.maxObjects = 3
		e.client.set("/pools", `[{"poolid":"web"}]`)
		e.client.set("/pools?poolid=web", `[{"poolid":"web","members":[]}]`)
		e.expect("baseline")
		pools := make([]string, 0, e.obs.maxObjects+1)
		for i := range e.obs.maxObjects + 1 {
			pools = append(pools, fmt.Sprintf(`{"poolid":"p%05d"}`, i))
		}
		e.client.set("/pools", "["+strings.Join(append(pools, `{"poolid":"web"}`), ",")+"]")
		e.client.set("/pools?poolid=web", `[{"poolid":"web","comment":"x","members":[]}]`)
		e.expect("past the cap", "changed pool/"+e.cluster.ID.String()+" [pool]", "changed pool/"+e.cluster.ID.String()+" [pool]",
			"changed pool/web [comment]")
		e.expect("still past it")
		if n := e.client.readsOf("/pools?poolid=p00000"); n != 0 {
			t.Fatalf("read a pool past the cap %d times, want it held by its listing alone", n)
		}

		e.mq.vmsByCluster = nil
		for vmid := range e.obs.maxObjects + 1 {
			e.mq.vmsByCluster = append(e.mq.vmsByCluster,
				db.Vm{ID: uuid.New(), ClusterID: e.cluster.ID, NodeID: e.node.ID, Vmid: int32(1000 + vmid), Type: "qemu"})
			e.client.set(fmt.Sprintf("/nodes/pve-01/qemu/%d/config", 1000+vmid), `{"memory":"1"}`)
		}
		turns := "changed cluster/" + e.cluster.ID.String() + " [guests]"
		if got := e.pass(); !slices.Contains(got, turns) {
			t.Fatalf("first pass in turns filed %q, want a notice that guests are read in turns", got)
		}
		if got := e.pass(); slices.Contains(got, turns) {
			t.Fatalf("second pass filed %q, want the notice once", got)
		}
		e.client.mu.Lock()
		defer e.client.mu.Unlock()
		total := 0
		for _, g := range e.mq.vmsByCluster {
			n := e.client.reads[fmt.Sprintf("/nodes/pve-01/qemu/%d/config", g.Vmid)]
			if n == 0 {
				t.Fatalf("guest %d never read in two passes, want each read in turns", g.Vmid)
			}
			total += n
		}
		if total != 2*e.obs.maxObjects {
			t.Fatalf("%d guest reads in two passes, want %d a pass", total, e.obs.maxObjects)
		}
	})
	t.Run("a mass edit of guests is filed guest by guest, as one bulk of the pass", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.mq.vmsByCluster = nil
		for vmid := 200; vmid < 200+3*observeMaxChanges; vmid++ {
			e.mq.vmsByCluster = append(e.mq.vmsByCluster,
				db.Vm{ID: uuid.New(), ClusterID: e.cluster.ID, NodeID: e.node.ID, Vmid: int32(vmid), Type: "qemu"})
			e.client.set(fmt.Sprintf("/nodes/pve-01/qemu/%d/config", vmid), `{"memory":"2048"}`)
		}
		e.expect("baseline")
		// Every other guest: no batch of observeGuestBatch holds more than
		// observeMaxChanges of them, the pass does.
		for i, g := range e.mq.vmsByCluster {
			if i%2 == 0 {
				e.client.set(fmt.Sprintf("/nodes/pve-01/qemu/%d/config", g.Vmid), `{"memory":"4096"}`)
			}
		}
		rows := e.passRows()
		bulk := map[any]int{}
		for _, c := range rows {
			bulk[c.Detail["bulk"]]++
			if c.Detail["bulk_size"] != float64(len(rows)) {
				t.Fatalf("a row of the bulk says %v, want bulk_size %d", c.Detail["bulk_size"], len(rows))
			}
		}
		if id, _ := slices.Collect(maps.Keys(bulk))[0].(string); len(bulk) != 1 || id == "" || len(rows) <= observeMaxChanges {
			t.Fatalf("%d guests edited: %v, want each filed and marked as one bulk", len(rows), bulk)
		}
	})
	t.Run("a guest is only ever changed", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.expect("baseline")
		e.mq.vmsByCluster = []db.Vm{{ID: uuid.New(), ClusterID: e.cluster.ID, NodeID: e.node.ID, Vmid: 101, Type: "lxc"}}
		e.client.set("/nodes/pve-01/lxc/101/config", `{"hostname":"linux02","memory":512}`)
		e.expect("one guest gone, one new")
		e.mq.observed.mu.Lock()
		_, kept := e.mq.observed.state[e.cluster.ID.String()+"/vm/100"]
		e.mq.observed.mu.Unlock()
		if kept {
			t.Fatal("kept the reading of a guest no longer in the inventory")
		}
		e.client.set("/nodes/pve-01/lxc/101/config", `{"hostname":"linux02","memory":1024,"lock":"backup"}`)
		e.expect("edited", "changed container/101 [memory]")
	})
	t.Run("a guest read while a clone or a create still writes it is no baseline", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.client.set(pathGuest, `{"lock":"clone"}`)
		e.expect("half a clone")
		e.client.set(pathGuest, `{"lock":"clone","scsi0":"store01:vm-100-disk-0"}`)
		e.expect("still half")
		e.client.set(pathGuest, `{"name":"linux01","memory":"2048","cores":2}`)
		e.expect("the clone made")
		e.client.set(pathGuest, `{"name":"linux01","memory":"4096","cores":2}`)
		e.expect("then an edit", "changed vm/100 [memory]")
		e.client.set(pathGuest, `{"name":"linux01","memory":"8192","cores":2,"lock":"create"}`)
		e.expect("an edit a known guest's lock brackets", "changed vm/100 [memory]")
	})
	t.Run("a NIC comes and goes silently, a bridge does not", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.expect("baseline")
		bridge := `{"iface":"vmbr0","type":"bridge","method":"static","cidr":"192.0.2.10/24","active":1}`
		e.client.set(pathNet, `[`+bridge+`,{"iface":"eth9","type":"eth","method":"manual"},{"iface":"vmbr1","type":"bridge"}]`)
		e.expect("a NIC and a bridge", "added network/pve-01/vmbr1 []")
		e.client.set(pathNet, `[`+bridge+`,{"iface":"vmbr1","type":"bridge"}]`)
		e.expect("the NIC gone")
	})
	t.Run("Proxmox VE 9's HA group migration records nothing", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.client.set("/cluster/ha/groups", `[{"group":"prefer01","type":"group","nodes":"pve-01"}]`)
		e.client.set("/cluster/ha/resources", `[{"sid":"vm:100","type":"vm","group":"prefer01"}]`)
		e.client.set("/cluster/ha/rules", `[{"rule":"ha-group-prefer01","type":"node-affinity","resources":"vm:100","nodes":"pve-01"}]`)
		e.expect("baseline")
		e.client.fail("/cluster/ha/groups", &proxmox.APIError{StatusCode: http.StatusInternalServerError,
			Message: "cannot index groups: ha groups have been migrated to rules"})
		e.client.set("/cluster/ha/resources", `[{"sid":"vm:100","type":"vm","failback":0}]`)
		e.expect("migrated")
		e.expect("the next pass")
		e.client.set("/cluster/ha/resources", `[{"sid":"vm:100","type":"vm","failback":1}]`)
		e.expect("then an edit", "changed ha_resource/vm:100 [failback]")
	})
	t.Run("an IP set's entries go with it; a set not read keeps them", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.client.set("/cluster/firewall/ipset", `[{"name":"blocked"},{"name":"allowed"}]`)
		e.client.set("/cluster/firewall/ipset/blocked", `[{"cidr":"192.0.2.0/24"}]`)
		e.client.set("/cluster/firewall/ipset/allowed", `[{"cidr":"192.0.2.128/25"}]`)
		e.expect("baseline")
		e.client.fail("/cluster/firewall/ipset/allowed", errors.New("proxmox API error 500"))
		e.client.set("/cluster/firewall/ipset", `[{"name":"allowed"}]`)
		e.expect("one set gone, one unread", "removed firewall_ipset/blocked []")
		e.client.set("/cluster/firewall/ipset/allowed", `[]`)
		e.expect("the last entry gone: an empty kind, held") // see "an empty answer is held a pass"
		e.expect("believed", "removed firewall_ipset_entry/allowed/192.0.2.128/25 []")
	})
	t.Run("an object changed every pass is recorded hourly, then flushed", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.expect("baseline")
		for i := range observeNoisyStreak {
			e.client.set(pathGuest, `{"name":"linux01","memory":"`+strconv.Itoa(4096+i)+`","cores":2}`)
			e.expect("noisy "+strconv.Itoa(i), "changed vm/100 [memory]")
		}
		e.client.set(pathGuest, `{"name":"linux01","memory":"1","cores":"8"}`)
		e.expect("gathered")
		e.expect("quiet again", "changed vm/100 [cores memory]")
	})
}

// A tick that finds a cluster's pass still running starts nothing for it.
func TestObserverPassesDoNotOverlap(t *testing.T) {
	t.Parallel()
	e := newObserveEnv(t)
	e.client.block = make(chan struct{})
	e.obs.ObserveAll(context.Background())
	e.obs.ObserveAll(context.Background())
	close(e.client.block)
	e.obs.Wait()
	if n := e.client.readsOf(pathPerms); n != 1 {
		t.Fatalf("probed %d times, want one pass", n)
	}
}

// What Proxmox answers, read through the real client, sorts into the three
// outcomes the passes rely on.
func TestObserveOutcomeOfProxmoxAnswers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		body   string
		want   readOutcome
	}{
		{"a listing", http.StatusOK, `{"data":[]}`, readOK},
		{"refused", http.StatusForbidden, `{"data":null}`, readForbidden},
		{"a path this version lacks", http.StatusNotImplemented, `{"message":"Method 'GET /cluster/ha/rules' not implemented"}`, readUnsupported},
		{"groups migrated to rules", http.StatusInternalServerError, `{"message":"cannot index groups: ha groups have been migrated to rules"}`, readUnsupported},
		{"an error", http.StatusInternalServerError, `{"message":"cfs-lock timeout"}`, readFailed},
		{"a proxy's 404", http.StatusNotFound, `not found`, readFailed},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		client, err := proxmox.NewClient(proxmox.ClientConfig{BaseURL: srv.URL, TokenID: "nexara@pve!t", TokenSecret: "s"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.ObserveRead(context.Background(), proxmox.ObserveStorage())
		srv.Close()
		if got := observeOutcomeOf(err); got != tc.want {
			t.Errorf("%s: outcome %d, want %d (%v)", tc.name, got, tc.want, err)
		}
	}
}

// One canonical form per value, whatever encoding Proxmox chose, and a key of an
// unexpected shape folded away rather than kept as a field name.
func TestObserveCanonicalForms(t *testing.T) {
	t.Parallel()
	spec := newFieldSpec([]string{"lock"}, []string{"nodes"})
	same := []struct{ a, b string }{
		{`{"memory":"2048"}`, `{"memory":2048}`},
		{`{"onboot":1}`, `{"onboot":true}`},
		{`{"nodes":"pve-01,pve-02"}`, `{"nodes":"pve-02, pve-01,pve-01"}`},
		{`{"map":[{"b":1,"a":"x"}]}`, `{"map":[{"a":"x","b":"1"}]}`},
		{`{"memory":"2048","lock":"backup","digest":"x"}`, `{"memory":"2048"}`},
		{`{"memory":"2048","comment":null}`, `{"memory":"2048"}`},
	}
	canon := func(s string) map[string]string {
		var item map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &item); err != nil {
			t.Fatal(err)
		}
		return spec.canonicalFields(item)
	}
	for _, tc := range same {
		if a, b := canon(tc.a), canon(tc.b); fmt.Sprint(a) != fmt.Sprint(b) {
			t.Errorf("%s and %s differ: %v, %v", tc.a, tc.b, a, b)
		}
	}
	got := canon(`{"memory":"1","Odd Key":"x","lxc.idmap":"y"}`)
	if _, ok := got["Odd Key"]; ok || got[observeOtherField] == "" || len(got) != 2 {
		t.Errorf("unexpected keys kept as names: %v", got)
	}

	// A member list is kept as its ids, sorted and once each, and says who
	// joined and who left; one holding anything but ids is a plain set.
	members := newFieldSpec(nil, nil).withMembers("vmid")
	split := func(s string) (map[string]string, map[string][]string) {
		var item map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &item); err != nil {
			t.Fatal(err)
		}
		return members.split(item)
	}
	if f, m := split(`{"vmid":"101,100,100"}`); len(f) != 0 || fmt.Sprint(m) != "map[vmid:[100 101]]" {
		t.Errorf("members: %v %v", f, m)
	}
	if f, m := split(`{"vmid":"100,<b>x</b>"}`); f["vmid"] == "" || m != nil {
		t.Errorf("a list that is no ids: %v %v", f, m)
	}
	if d := diffTags(objectTags{scalar: map[string]string{"a": "1", "b": "1"}},
		objectTags{scalar: map[string]string{"a": "2", "b": "1", "c": "1"}}); fmt.Sprint(d.fields, d.appeared) != "[a c] [c]" {
		t.Errorf("a field that appeared: %+v", d)
	}
	d := diffTags(objectTags{members: map[string][]string{"vmid": {"100", "101"}}},
		objectTags{members: map[string][]string{"vmid": {"101", "102"}}})
	if fmt.Sprint(d.fields, d.joined, d.left, d.counts) != "[vmid] [vmid:102] [vmid:100] map[vmid:{1 1 0}]" {
		t.Errorf("member diff: %+v", d)
	}
	// A rule set counts what came, went, and of the rest what moved.
	for _, tc := range []struct{ prev, cur, want string }{
		{"a b c", "b a c", "{0 0 1}"}, {"a b c", "c a b", "{0 0 1}"}, {"a b c", "a x c", "{1 1 0}"}, {"a a b", "a b a", "{0 0 1}"},
		{"a b a b", "b a b a", "{0 0 1}"}, // one moved, though matching repeats in order would say two
	} {
		if got := fmt.Sprint(multisetDiff(strings.Fields(tc.prev), strings.Fields(tc.cur))); got != tc.want {
			t.Errorf("%s -> %s: %s, want %s", tc.prev, tc.cur, got, tc.want)
		}
	}
}

// The subsequence that says what moved is the textbook table's, without its
// cost: exact within the pair limit, and past it exact for distinct items.
func TestLCSLen(t *testing.T) {
	t.Parallel()
	table := func(a, b []string) int { // the O(n·m) dynamic programme
		row := make([]int, len(b)+1)
		for i := range a {
			diag := 0
			for j := range b {
				up := row[j+1]
				if a[i] == b[j] {
					row[j+1] = diag + 1
				} else {
					row[j+1] = max(row[j+1], row[j])
				}
				diag = up
			}
		}
		return row[len(b)]
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range 300 {
		n, kinds := 1+rng.IntN(12), 1+rng.IntN(6)
		a := make([]string, n)
		for i := range a {
			a[i] = strconv.Itoa(rng.IntN(kinds))
		}
		b := slices.Clone(a)
		rng.Shuffle(len(b), func(i, j int) { b[i], b[j] = b[j], b[i] })
		if got, want := lcsLen(a, b), table(a, b); got != want {
			t.Fatalf("%v, %v: %d, want %d", a, b, got, want)
		} else if past := lcsLenWithin(a, b, 0); past > want {
			t.Fatalf("past the limit, %v, %v: %d, more than the %d there are", a, b, past, want)
		}
		distinct := slices.Clone(a)
		for i := range distinct {
			distinct[i] += "." + strconv.Itoa(i)
		}
		shuffled := slices.Clone(distinct)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if got, want := lcsLenWithin(distinct, shuffled, 0), table(distinct, shuffled); got != want {
			t.Fatalf("past the limit, %v, %v: %d, want %d", distinct, shuffled, got, want)
		}
	}
}

// A classification that runs out of time decides half as many the next time,
// so a pass does not retry the same oldest rows at the size that timed out,
// and grows back once one answers.
func TestObservedClassifyBatchShrinksAfterATimeout(t *testing.T) {
	t.Parallel()
	mq := &mockQueries{classifyErrs: []error{fmt.Errorf("classify: %w", context.DeadlineExceeded),
		fmt.Errorf("classify: %w", context.DeadlineExceeded)}}
	s := &Syncer{queries: mq, logger: testLogger()}
	cluster := db.Cluster{ID: uuid.New()}
	for range 4 {
		s.classifyObservedChanges(context.Background(), cluster, true, json.RawMessage(`{}`), json.RawMessage(`{}`))
	}
	got := make([]int32, 0, len(mq.classifyObserved))
	for _, c := range mq.classifyObserved {
		got = append(got, c.MaxRows)
	}
	if want := []int32{observeClassifyBatch, observeClassifyBatch / 2, observeClassifyBatch / 4, observeClassifyBatch / 2}; !slices.Equal(got, want) {
		t.Fatalf("batches %v, want %v", got, want)
	}
}

// Every type the observer files under is one a History reads, under that
// type's view permission: a row of a type it lacks reaches no one.
func TestObservedTypesHaveAReader(t *testing.T) {
	t.Parallel()
	types := make([]string, 0, 1+len(observeKindSchemas))
	types = append(types, observedClusterType)
	for kind := range observeKindSchemas {
		types = append(types, removedObject(kind, "x", "").resourceType)
	}
	for _, resourceType := range types {
		if _, ok := handlers.ObservedTypePermission(resourceType); !ok {
			t.Errorf("%s: no History reads the observer's rows of it", resourceType)
		}
	}
}

// Every kind's signature carries how its fields are read, so a release that
// reads one another way re-baselines it once instead of filing every object
// of the kind as changed.
func TestObserveKindSchemas(t *testing.T) {
	t.Parallel()
	kinds := make([]string, 0, 15+len(observeClusterKinds))
	kinds = append(kinds, "ha_resource", "ha_rule", "ha_group", "sdn_subnet", "firewall_ipset_entry",
		"firewall_security_group_rule", "pve_token", "network", "node_options", "acme_config", "node_dns",
		"node_time", "node_firewall", "vm", "container")
	for _, k := range observeClusterKinds {
		kinds = append(kinds, k.kind)
	}
	for _, k := range kinds {
		if observeKindSchemas[k] == "" {
			t.Errorf("%s has no schema", k)
		}
	}
	if newFieldSpec([]string{"a"}, nil).digest() == newFieldSpec([]string{"a", "b"}, nil).digest() ||
		newFieldSpec(nil, []string{"a"}).digest() == newFieldSpec(nil, nil).withMembers("a").digest() {
		t.Error("a spec's digest does not follow the spec")
	}
	p := &observePass{}
	if p.kindSig("storage", "s") == p.kindSig("backup", "s") {
		t.Error("a kind's signature does not carry its schema")
	}
}

// A tag depends on the key, the object and the field as well as the value, so
// it cannot be tested against a guess without the key, nor compared across
// objects; and it is never the value.
func TestObserveTags(t *testing.T) {
	t.Parallel()
	tagger, err := newObserveTagger(testEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	other, err := newObserveTagger(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	cluster := uuid.New()
	tag := func(tg *observeTagger, kind, id, field, value string) string {
		got, err := tg.tag(cluster, kind, id, field, value)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	base := tag(tagger, "vm", "100", "cipassword", "hunter2")
	for name, got := range map[string]string{
		"another key":    tag(other, "vm", "100", "cipassword", "hunter2"),
		"another guest":  tag(tagger, "vm", "101", "cipassword", "hunter2"),
		"another field":  tag(tagger, "vm", "100", "description", "hunter2"),
		"another value":  tag(tagger, "vm", "100", "cipassword", "hunter3"),
		"split the same": tag(tagger, "vm", "10", "0cipassword", "hunter2"),
	} {
		if got == base {
			t.Errorf("%s gives the same tag", name)
		}
	}
	if base != tag(tagger, "vm", "100", "cipassword", "hunter2") || strings.Contains(base, "hunter2") {
		t.Errorf("tag %q is not stable or carries the value", base)
	}
	if tagger.keyID == other.keyID {
		t.Error("two keys share a key id")
	}
}

// Every rule names a match key the query knows and patterns that compile; a
// kind's changes are explainable (TestObserverKinds checks each kind it files).
func TestObservedChangeRules(t *testing.T) {
	t.Parallel()
	keys := regexp.MustCompile(`^(id|node|vmid|sid|cluster|details:[a-z_]+)$`)
	checked := 0
	for _, r := range slices.Concat(observedChangeRules, observedGlobalRules) {
		if !keys.MatchString(r.MatchKey) || (r.AuditType == "" && !slices.Contains([]string{"vmid", "sid", "cluster"}, r.MatchKey)) {
			t.Errorf("rule %+v: match key or audit type", r)
		}
		if !slices.Contains([]string{"added", "changed", "removed"}, r.Change) {
			t.Errorf("rule %+v names no direction", r)
		}
		named := func(s string) bool {
			for _, part := range strings.Split(s, "+") {
				source, format, ok := strings.Cut(part, ":")
				if !ok || !regexp.MustCompile(`^(upid|audit|vmid|details\.[a-z_]+|details-list\.[a-z_]+|grants\.(users|groups|tokens))$`).MatchString(source) {
					return false
				}
				for _, alt := range strings.Split(format, "|") {
					grants := regexp.MustCompile(`^[a-z]+_grants:%1\$s:(%2\$s|\*):%3\$s$`)
					if strings.HasPrefix(source, "grants.") && !grants.MatchString(alt) ||
						!strings.HasPrefix(source, "grants.") && (strings.Count(alt, "%") != 1 || !strings.Contains(alt, "%s")) ||
						strings.Count(alt, "*") > 1 {
						return false
					}
				}
			}
			return true
		}
		memberOK := (r.Member == "" && r.MemberOf == "") || r.Member == "joined" || r.Member == "left" ||
			(r.Member == "sole" || r.Member == "left_unlisted") && r.MemberOf != ""
		if r.DetailValue != "" && r.DetailKey == "" {
			memberOK = false
		}
		if (r.Object != "" && (!named(r.Object) || strings.ContainsAny(r.Object, "|+"))) ||
			(r.MemberOf != "" && !named(r.MemberOf)) || !memberOK ||
			!slices.Contains([]string{"", "add1", "del1", "edit1", "adds", "dels"}, r.Counts) ||
			!regexp.MustCompile(`^!?[a-z_]*$`).MatchString(r.DetailKey) {
			t.Errorf("rule %+v: object, member, counts or detail_key", r)
		}
		if !slices.Contains([]string{"", "config", "settings", "keys", "fields", "realm"}, r.FieldsFrom) ||
			len(r.Derived) > 0 && r.FieldsFrom != "fields" || r.Appeared && r.FieldRe == "" {
			t.Errorf("rule %+v: fields_from, derived without the fields it is derived from, or appeared naming no field", r)
		}
		for _, re := range []string{r.ActionRe, r.ExceptRe, r.WorkerRe, r.FieldRe} {
			if _, err := regexp.Compile(re); err != nil || (re != "" && !strings.HasPrefix(re, "^")) {
				t.Errorf("rule %+v: pattern %q", r, re)
			}
		}
		// An object's own update explains the fields its row says it set, or
		// its kind is one whose row records none (anyFieldUpdates).
		if r.Change == "changed" && r.MatchKey == "id" && r.Member == "" && r.FieldRe == "" && r.FieldsFrom == "" &&
			r.DetailKey == "" && r.Counts == "" && !slices.Contains(anyFieldUpdates, r.Kind) {
			t.Errorf("rule %+v explains any field of its object, and %s is not in anyFieldUpdates", r, r.Kind)
		}
		// Proxmox fills in a zone's mac only where there was none.
		if r.Kind == "sdn_zone" && r.FieldRe != "" && regexp.MustCompile(r.FieldRe).MatchString("mac") && !r.Appeared {
			t.Errorf("rule %+v: a zone's mac is explained only where it appeared", r)
		}
		// An ACL rule names its grantees, unless it is a path taken with its
		// object: a grant explains no one it does not name. A deletion of a
		// role names that role in each grant it takes out (<kind>_grants:
		// <role>:<propagate>:<grantee>); of a grantee, that grantee.
		if r.Kind == "pve_acl" && r.Member == "" && (r.Change != "removed" || r.Object == "") {
			t.Errorf("rule %+v explains an ACL change without naming its grantees", r)
		}
		if r.Kind == "pve_user" && r.AuditType == "pve_user" && r.Change == "changed" &&
			regexp.MustCompile(r.FieldRe).MatchString("keys") && r.DetailKey != "keys_changed" {
			t.Errorf("rule %+v: Nexara's update explains a user's keys only by a row that says it set them", r)
		}
		if r.Kind == "pve_role" && r.Change == "changed" && r.MemberOf == "" {
			t.Errorf("rule %+v: a role's update explains only the privileges its row lists", r)
		}
		if r.Kind == "pool" && r.AuditType == "pool" && r.Change == "changed" && (r.MemberOf == "" &&
			(r.FieldRe == "" || regexp.MustCompile(r.FieldRe).MatchString("vms") || regexp.MustCompile(r.FieldRe).MatchString("storage")) ||
			r.Member != "" && r.DetailKey != map[string]string{"joined": "!delete", "left": "delete"}[r.Member]) {
			t.Errorf("rule %+v: a pool's update explains a member only by the members its row lists, joining without delete", r)
		}
		// A realm sync adds, removes, and purges the ACL of, only what its log
		// says, and a user joins its group only where its log added or replaced it.
		sync := strings.Contains(r.WorkerRe, "auth-realm-sync")
		if sync && (r.Kind == "pve_acl" && r.LogNames != "purged" ||
			r.Change == "removed" && r.Kind != "pve_acl" && !strings.HasPrefix(r.LogNames, "removed:") ||
			r.Change == "added" && !strings.HasPrefix(r.LogNames, "added:") ||
			r.Kind == "pve_user" && r.Member == "joined" && r.LogNames != "added:users" && r.LogNames != "overwritten:users") ||
			r.LogNames != "" && r.WorkerRe != `^auth-realm-sync$` {
			t.Errorf("rule %+v: a realm sync's additions, removals and joins are what its log names, and only its log is read", r)
		}
		// Of a user it has, a sync sets enable, expire and keys only where its
		// realm maps them, or expire and keys where it replaced the user whole,
		// and never the enable of one it replaced.
		for _, f := range realmSyncFields {
			matches := regexp.MustCompile(r.FieldRe).MatchString(f)
			if sync && r.Kind == "pve_user" && r.Change == "changed" && matches &&
				(r.FieldsFrom != "realm" && (r.LogNames != "overwritten:users" || f == "enable") ||
					r.FieldsFrom == "realm" && f == "enable" && r.LogNames != "!overwritten:users") {
				t.Errorf("rule %+v: a realm sync explains a user's %s only as its realm maps it", r, f)
			}
		}
		if source, formats, _ := strings.Cut(r.MemberOf, ":"); r.Kind == "pve_acl" && source == "audit" {
			pinned := map[string]*regexp.Regexp{
				"pve_role":  regexp.MustCompile(`^[a-z]+_grants:%s:\*$`),
				"pve_user":  regexp.MustCompile(`^(users:%s|user_grants:\*:%s)$`),
				"pve_group": regexp.MustCompile(`^(groups:%s|group_grants:\*:%s)$`),
				"pve_token": regexp.MustCompile(`^(tokens:%s|token_grants:\*:%s)$`),
			}[r.AuditType]
			for _, alt := range strings.Split(formats, "|") {
				if pinned == nil || !pinned.MatchString(alt) {
					t.Errorf("rule %+v: a deletion's %q does not name what it deleted where a grant names it", r, alt)
				}
			}
		}
		// A row that reaches the object from elsewhere and covers a list of ids
		// says which way the ids go, or a role's delete hides a grant made
		// meanwhile: unpinned, it explains an id arriving as well as leaving.
		lists, ok := memberFields[r.Kind]
		if ok && r.Change == "changed" && r.MatchKey != "id" && r.Member == "" &&
			slices.ContainsFunc(lists, func(f string) bool { return regexp.MustCompile(r.FieldRe).MatchString(f) }) {
			checked++
			t.Errorf("rule %+v reaches %s's ids from elsewhere and names no direction", r, r.Kind)
		} else if ok && r.MatchKey != "id" {
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no rule reaches a kind's ids from elsewhere: the direction check reads nothing")
	}
}

// anyFieldUpdates are the kinds whose own update explains any field of the
// object changed in its window, its handler unable to know what it set. There
// are none: each records the names it sent (handlers' auditFieldNames). The
// admin guide says so, beside qmconfig's task, which explains any field.
var anyFieldUpdates []string

// memberFields are each kind's lists of other objects' ids (fieldSpec.withMembers
// and the readers that build members).
var memberFields = map[string][]string{
	"pool": {"vms", "storage"}, "backup": {"vmid", "exclude"}, "ha_rule": {"resources"}, "pve_user": {"groups"},
	"pve_group": {"users"}, "sdn_subnet": {"cidr"}, "pve_role": {"privs"},
	"pve_acl": {"users", "groups", "tokens", "user_grants", "group_grants", "token_grants"},
}

func hasExplainRule(resourceType string) bool {
	return slices.ContainsFunc(observedChangeRules, func(r explainRule) bool { return r.Kind == resourceType })
}

// Every literal action Nexara files for an object the observer reads is
// classified: by a rule of that kind, which says which way it changes the
// object, or as one that changes nothing the observer compares; for a guest,
// with no task, as an edit of its configuration or not. An action nobody
// classified would show Nexara's own edit as one made in Proxmox.
func TestObservedActionVocabulary(t *testing.T) {
	t.Parallel()
	observed := map[string]bool{}
	for _, r := range observedChangeRules {
		observed[r.Kind] = true
	}
	explains := func(kind, action string) bool {
		if slices.Contains(observedActionsExplainingNothing[kind], action) {
			return true
		}
		return slices.ContainsFunc(observedChangeRules, func(r explainRule) bool {
			return (r.Kind == kind || r.AuditType == kind) && r.ActionRe != "" &&
				regexp.MustCompile(r.ActionRe).MatchString(action)
		})
	}
	guestClassified := slices.Concat(guestConfigActions, guestOtherActions)
	checked, rolling := 0, 0
	for _, dir := range []string{"../api/handlers", "../guesttools", "../scheduler", "../migration", "../drs", "../rolling",
		"../collector"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range files {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				for _, site := range auditSites(n) {
					switch {
					case site.kind == "vm" || site.kind == "container":
						if site.task {
							break // explained by its worker type
						}
						checked++
						if !slices.Contains(guestClassified, site.action) {
							t.Errorf("%s: guest action %q is in neither guestConfigActions nor guestOtherActions", path, site.action)
						}
					case observed[site.kind]:
						checked++
						if strings.HasPrefix(path, "../rolling") {
							rolling++
						}
						if !explains(site.kind, site.action) {
							t.Errorf("%s: %s action %q is in no rule and not in observedActionsExplainingNothing",
								path, site.kind, site.action)
						}
					}
				}
				return true
			})
		}
	}
	// The task ingest files from a table, which the scan cannot see.
	ingested := 0
	for worker, m := range proxmoxTaskMap {
		if m.ResourceType != "vm" && m.ResourceType != "container" && observed[m.ResourceType] {
			ingested++
			if !explains(m.ResourceType, m.Action) {
				t.Errorf("proxmoxTaskMap: %s files %s action %q, which is in no rule and not in observedActionsExplainingNothing",
					worker, m.ResourceType, m.Action)
			}
		}
	}
	if checked < 60 || rolling == 0 || ingested == 0 {
		t.Fatalf("checked %d call sites, %d of the rolling update's, %d ingest mappings; the scan is not reading them",
			checked, rolling, ingested)
	}
}

type auditSite struct {
	kind, action string
	task         bool // a TrackTask, which carries a UPID
}

// auditSites is the literal resource type and action of an AuditLog or
// AuditLogAs call, or of an InsertAuditLogParams or TrackTaskParams literal.
func auditSites(n ast.Node) []auditSite {
	lit := func(e ast.Expr) string {
		if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
			s, _ := strconv.Unquote(b.Value)
			return s
		}
		return ""
	}
	switch x := n.(type) {
	case *ast.CallExpr:
		name := ""
		switch fn := x.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		}
		typeAt := map[string]int{"AuditLog": 4, "AuditLogAs": 5, "auditObject": 3}[name]
		if typeAt > 0 && len(x.Args) > typeAt+2 {
			if kind, action := lit(x.Args[typeAt]), lit(x.Args[typeAt+2]); kind != "" && action != "" {
				return []auditSite{{kind: kind, action: action}}
			}
		}
	case *ast.CompositeLit:
		typeName := ""
		switch tn := x.Type.(type) {
		case *ast.Ident:
			typeName = tn.Name
		case *ast.SelectorExpr:
			typeName = tn.Sel.Name
		}
		if typeName != "InsertAuditLogParams" && typeName != "TrackTaskParams" {
			return nil
		}
		var kind, action string
		for _, el := range x.Elts {
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				switch fmt.Sprint(kv.Key) {
				case "ResourceType":
					kind = lit(kv.Value)
				case "Action":
					action = lit(kv.Value)
				}
			}
		}
		if kind != "" && action != "" {
			return []auditSite{{kind: kind, action: action, task: typeName == "TrackTaskParams"}}
		}
	}
	return nil
}

func TestEveryOnlineNodeListed(t *testing.T) {
	t.Parallel()
	nodes := []proxmox.NodeListEntry{{Node: "pve-01", Status: "online"}, {Node: "pve-02", Status: "offline"}}
	if !everyOnlineNodeListed(nodes, []string{"pve-01"}) || everyOnlineNodeListed(nodes, []string{"pve-02"}) {
		t.Fatal("an offline node must not hold the classification back, an unlisted online one must")
	}
}
