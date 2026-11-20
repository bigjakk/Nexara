package scheduler

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/drs"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// rendezvous holds the first want callers of arrive until open, and lets any
// later caller straight through. Work on two clusters held in it at once is
// what shows they ran side by side: handled one after the other, the second
// never arrives while the first is held, and the first gives up waiting.
type rendezvous struct {
	want    int
	arrived chan struct{}
	opened  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	callers int
	gaveUp  int // held callers whose context ended before open
}

func newRendezvous(want int) *rendezvous {
	return &rendezvous{want: want, arrived: make(chan struct{}, want), opened: make(chan struct{})}
}

func (r *rendezvous) arrive(ctx context.Context) {
	r.mu.Lock()
	r.callers++
	held := r.callers <= r.want
	r.mu.Unlock()
	if !held {
		return
	}
	r.arrived <- struct{}{}
	select {
	case <-r.opened:
	case <-ctx.Done():
		r.mu.Lock()
		r.gaveUp++
		r.mu.Unlock()
	}
}

func (r *rendezvous) open() { r.once.Do(func() { close(r.opened) }) }

func (r *rendezvous) counts() (callers, gaveUp int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callers, r.gaveUp
}

// heldPVEClient is a client for a stub Proxmox whose every answer waits in rv:
// a UPID for a call that starts a task, a successful end for a status read.
func heldPVEClient(t *testing.T, rv *rendezvous) func(context.Context, uuid.UUID) (*proxmox.Client, error) {
	srv, _ := newPVEStubFunc(t, func(r *http.Request) (int, string) {
		rv.arrive(r.Context())
		if r.Method == http.MethodPost {
			return http.StatusOK, upidBody(snapshotUPID)
		}
		return http.StatusOK, `{"data":{"status":"stopped","exitstatus":"OK"}}`
	})
	client := newStubPVEClient(t, srv.URL)
	return func(context.Context, uuid.UUID) (*proxmox.Client, error) { return client, nil }
}

// fakeDRS evaluates each cluster to one recommendation and holds its Execute
// in rv, as a migration holds its cluster's pass.
type fakeDRS struct {
	rv *rendezvous
	// undeadlined counts evaluations whose context had no deadline.
	undeadlined atomic.Int32
}

func (f *fakeDRS) Evaluate(ctx context.Context, _ uuid.UUID) (*drs.EvalResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		f.undeadlined.Add(1)
	}
	return &drs.EvalResult{Recommendations: []drs.Recommendation{
		{VMID: 100, VMType: "qemu", SourceNode: "pve-01", TargetNode: "pve-02"},
	}}, nil
}

func (f *fakeDRS) Execute(ctx context.Context, _ *proxmox.Client, _ uuid.UUID, _ string, _ []drs.Recommendation) error {
	f.rv.arrive(ctx)
	return nil
}

// importUPID is the task behind a VM import; placeholder node and token.
const importUPID = "UPID:pve-01:0000A1B4:0001C3D6:66F4E3C2:qmcreate:101:nexara@pve!api:"

// These engines handled their clusters in turn, in one pass the next tick
// waited for, so one cluster migrating or not answering held back every other
// cluster's evaluation, scheduled runs or import status. Each cluster's work
// must run beside the other's, and a pass started meanwhile must leave both
// clusters alone.
func TestPerClusterEngines_AClusterStillAtWorkHoldsBackNoOther(t *testing.T) {
	t.Parallel()
	clusters := []uuid.UUID{uuid.New(), uuid.New()}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	for _, tc := range []struct {
		name string
		run  func(*Scheduler, context.Context)
		// setup builds a scheduler whose work on each cluster waits in rv, and
		// returns what else to check once both passes are done.
		setup func(t *testing.T, rv *rendezvous) (*Scheduler, func(t *testing.T))
	}{
		{"drs", (*Scheduler).RunDRS, func(t *testing.T, rv *rendezvous) (*Scheduler, func(*testing.T)) {
			var configs []any
			for _, id := range clusters {
				// A queued request makes the cluster due on every pass.
				configs = append(configs, db.DrsConfig{ClusterID: id, Mode: "automatic", Enabled: true, EvalRequestedAt: stampedAt(time.Now())})
			}
			fake := &fakeDRS{rv: rv}
			s := &Scheduler{
				queries:     db.New(&queryOrderRecorder{rows: map[string][]any{"ListEnabledDRSConfigs": configs}}),
				logger:      logger,
				drsEngine:   fake,
				drsExecutor: fake,
				drsLastEval: map[uuid.UUID]time.Time{},
				// Execute is faked, so nothing dials the client.
				newClient: func(context.Context, uuid.UUID) (*proxmox.Client, error) { return nil, nil },
			}
			return s, func(t *testing.T) {
				if n := fake.undeadlined.Load(); n > 0 {
					t.Errorf("%d evaluations read under the leader's context, which has no deadline", n)
				}
			}
		}},
		{"scheduled tasks", (*Scheduler).Run, func(t *testing.T, rv *rendezvous) (*Scheduler, func(*testing.T)) {
			var claimed []db.ScheduledTask
			for _, id := range clusters {
				task := claimedTask("snapshot", "0 2 * * *")
				task.ClusterID = id
				claimed = append(claimed, task)
			}
			recorder := &taskRunRecorder{claims: [][]db.ScheduledTask{claimed}}
			s := newRecordingScheduler(recorder)
			s.newClient = heldPVEClient(t, rv)
			return s, func(t *testing.T) {
				// The second claim came while both clusters were dispatching:
				// one that took their unstarted rows again would run them twice.
				if len(recorder.claimedOut) != 2 {
					t.Fatalf("claimed %d times, want twice", len(recorder.claimedOut))
				}
				if got := slices.SortedFunc(slices.Values(recorder.claimedOut[1]), uuidCompare); !slices.Equal(got, slices.SortedFunc(slices.Values(clusters), uuidCompare)) {
					t.Errorf("the second claim left out %v, want both clusters %v", got, clusters)
				}
				for _, w := range recorder.writes {
					if status, _ := w.text(t, "last_status"); status != runStatusDispatched {
						t.Errorf("a run was recorded %q, want %q", status, runStatusDispatched)
					}
				}
				if len(recorder.writes) != len(clusters) {
					t.Errorf("%d runs recorded, want one per cluster (recorder error: %v)", len(recorder.writes), recorder.err)
				}
			}
		}},
		{"vm import reconcile", (*Scheduler).RunVMImportReconcile, func(t *testing.T, rv *rendezvous) (*Scheduler, func(*testing.T)) {
			var jobs []any
			for _, id := range clusters {
				jobs = append(jobs, db.VmImportJob{ID: uuid.New(), ClusterID: id, TargetNode: "pve-01", Upid: importUPID, Status: "running"})
			}
			recorder := &queryOrderRecorder{rows: map[string][]any{"ListActiveVMImportJobs": jobs}}
			s := &Scheduler{queries: db.New(recorder), logger: logger, newClient: heldPVEClient(t, rv)}
			return s, func(t *testing.T) {
				if got := recorder.count("CompleteVMImportJob"); got != len(clusters) {
					t.Errorf("%d imports completed, want one per cluster", got)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rv := newRendezvous(len(clusters))
			defer rv.open()
			s, check := tc.setup(t, rv)

			first := make(chan struct{})
			go func() {
				defer close(first)
				tc.run(s, ctx)
			}()
			// Only a pass that fails gets near this bound: work on two clusters
			// side by side meets in rv at once.
			timeout := time.After(time.Minute)
			for range clusters {
				select {
				case <-rv.arrived:
				case <-timeout:
					t.Fatal("one cluster's work never started while the other's was under way")
				}
			}
			tc.run(s, ctx) // a pass while both clusters are still at work
			rv.open()
			<-first

			callers, gaveUp := rv.counts()
			if gaveUp > 0 {
				t.Errorf("%d clusters' work gave up waiting for the other's to start: the clusters were handled one at a time", gaveUp)
			}
			if callers != len(clusters) {
				t.Errorf("work started %d times, want once per cluster: a pass did not leave a cluster still at work alone", callers)
			}
			// A pass that kept its cluster marked would leave it alone for good.
			for _, p := range []*clusterPasses{&s.drsPasses, &s.taskPasses, &s.importPasses} {
				if busy := p.busy(); len(busy) > 0 {
					t.Errorf("clusters %v still marked busy after their passes ended", busy)
				}
			}
			check(t)
		})
	}
}

func uuidCompare(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) }
