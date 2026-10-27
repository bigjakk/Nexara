package collector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
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

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// observeStore is mockQueries' copy of the observer's tables: the stored
// readings and marks, and the changes filed, applied as RecordObservedObjects
// applies them (the SQL itself is tested in internal/db).
type observeStore struct {
	mu      sync.Mutex
	state   map[string]db.ListClusterObjectStateRow // "cluster/kind/id"
	marks   map[string]db.ListClusterObjectBaselinesRow
	changes []observedChangeRow
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

func (m *mockQueries) RecordObservedObjects(_ context.Context, arg db.RecordObservedObjectsParams) error {
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
	s := &m.observed
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		s.state, s.marks = map[string]db.ListClusterObjectStateRow{}, map[string]db.ListClusterObjectBaselinesRow{}
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
// target with no answer is a 501, as Proxmox answers a path it does not have.
type observeFakeClient struct {
	mu      sync.Mutex
	answers map[string]observeAnswer
	reads   map[string]int
	block   chan struct{} // when set, every read waits for it to close
}

type observeAnswer struct {
	data string
	err  error
}

func (f *observeFakeClient) ObserveRead(ctx context.Context, t proxmox.ObserveTarget) (json.RawMessage, error) {
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
		value             string // the edited value, which must appear nowhere in the row
		detail            string // the row's detail, when it has one
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
			want: "changed pool/web [storage vms]", value: "store01"},
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
			want: "changed node/pve-01 [firewall_rules]", value: "8006",
			detail: `{"counts":{"firewall_rules":{"added":1,"removed":0}}}`},
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
		before := time.Now()
		e.client.fail(pathStorage, errors.New("proxmox API error 500"))
		e.expect("failed read")
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
	t.Run("an upgrade or a privilege change re-baselines", func(t *testing.T) {
		t.Parallel()
		e := newObserveEnv(t)
		e.expect("baseline")
		e.mq.nodesByCluster[0].PveVersion = "pve-manager/9.2.21/0000bbbb"
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","nodes":"pve-01"}]`)
		e.client.set(pathGuest, `{"name":"linux01","memory":"4096"}`)
		// A guest's configuration is its raw file, so the version is not in its
		// signature: its edit in the same pass is still recorded.
		e.expect("upgraded", "changed vm/100 [cores memory]")
		e.client.set(pathPerms, `{"/":{"Sys.Audit":1,"VM.Audit":1}}`)
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","nodes":"pve-02"}]`)
		e.client.set(pathGuest, `{"name":"linux01","memory":"8192"}`)
		e.expect("privileges changed")
		e.client.set(pathStorage, `[{"storage":"store01","type":"dir","nodes":"pve-03"}]`)
		e.expect("then an edit", "changed storage/store01 [nodes]")
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
	for _, r := range observedChangeRules {
		if !keys.MatchString(r.MatchKey) || (r.AuditType == "" && !slices.Contains([]string{"vmid", "sid", "cluster"}, r.MatchKey)) {
			t.Errorf("rule %+v: match key or audit type", r)
		}
		for _, re := range []string{r.ActionRe, r.ExceptRe, r.WorkerRe, r.FieldRe} {
			if _, err := regexp.Compile(re); err != nil || (re != "" && !strings.HasPrefix(re, "^")) {
				t.Errorf("rule %+v: pattern %q", r, re)
			}
		}
	}
}

func hasExplainRule(resourceType string) bool {
	return slices.ContainsFunc(observedChangeRules, func(r explainRule) bool { return r.Kind == resourceType })
}

// Every action Nexara files for a guest with no task, at a call site that names
// the action, is classified as an edit of its configuration or not: an action
// nobody classified would show Nexara's own edit as one made in Proxmox, or
// hide one made there.
func TestObservedGuestActionVocabulary(t *testing.T) {
	t.Parallel()
	classified := slices.Concat(guestConfigActions, guestOtherActions)
	found := map[string]string{}
	for _, dir := range []string{"../api/handlers", "../guesttools", "../scheduler", "../migration", "../drs"} {
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
				for action, kind := range guestAuditActions(n) {
					found[action] = kind + " in " + path
				}
				return true
			})
		}
	}
	if len(found) < 8 {
		t.Fatalf("found %d guest actions, the scan is not reading the call sites: %v", len(found), found)
	}
	for action, where := range found {
		if !slices.Contains(classified, action) {
			t.Errorf("guest action %q (%s) is in neither guestConfigActions nor guestOtherActions", action, where)
		}
	}
}

// guestAuditActions is the literal action of an AuditLog or AuditLogAs call, or
// an InsertAuditLogParams literal, whose resource type is a literal "vm" or
// "container".
func guestAuditActions(n ast.Node) map[string]string {
	lit := func(e ast.Expr) string {
		if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
			s, _ := strconv.Unquote(b.Value)
			return s
		}
		return ""
	}
	out := map[string]string{}
	switch x := n.(type) {
	case *ast.CallExpr:
		name := ""
		switch fn := x.Fun.(type) {
		case *ast.Ident:
			name = fn.Name
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		}
		typeAt := map[string]int{"AuditLog": 4, "AuditLogAs": 5}[name]
		if typeAt > 0 && len(x.Args) > typeAt+2 {
			if kind := lit(x.Args[typeAt]); kind == "vm" || kind == "container" {
				if action := lit(x.Args[typeAt+2]); action != "" {
					out[action] = kind
				}
			}
		}
	case *ast.CompositeLit:
		sel, ok := x.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "InsertAuditLogParams" {
			return out
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
		if (kind == "vm" || kind == "container") && action != "" {
			out[action] = kind
		}
	}
	return out
}

func TestEveryOnlineNodeListed(t *testing.T) {
	t.Parallel()
	nodes := []proxmox.NodeListEntry{{Node: "pve-01", Status: "online"}, {Node: "pve-02", Status: "offline"}}
	if !everyOnlineNodeListed(nodes, []string{"pve-01"}) || everyOnlineNodeListed(nodes, []string{"pve-02"}) {
		t.Fatal("an offline node must not hold the classification back, an unlisted online one must")
	}
}
