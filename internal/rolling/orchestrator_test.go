package rolling

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// TaskSucceeded coverage now lives in internal/proxmox/task_test.go;
// this package's local taskSucceeded was removed in Phase 5.3.

func TestGuestSnapshotTypes(t *testing.T) {
	// Verify struct fields are correctly defined.
	g := GuestSnapshot{
		VMID:   100,
		Name:   "test-vm",
		Type:   "qemu",
		Status: "running",
	}
	if g.VMID != 100 {
		t.Errorf("VMID = %d, want 100", g.VMID)
	}
	if g.Type != "qemu" {
		t.Errorf("Type = %s, want qemu", g.Type)
	}
}

// TestCleanupCtxFor covers the helper that protects failure-outcome DB
// writes from being silently dropped when the orchestrator's ctx has been
// cancelled by SIGTERM. With a live ctx the same ctx is returned unchanged
// (so we don't pay for an extra timeout context on the happy path); with
// a cancelled ctx, a fresh Background-derived context is returned that is
// not Done(), so the DB write actually runs.
func TestCleanupCtxFor(t *testing.T) {
	t.Run("live ctx is returned unchanged", func(t *testing.T) {
		ctx := context.Background()
		got, cancel := cleanupCtxFor(ctx)
		defer cancel()
		if got != ctx {
			t.Fatal("expected the same ctx back when parent is alive")
		}
		select {
		case <-got.Done():
			t.Fatal("returned ctx should not be Done()")
		default:
		}
	})

	t.Run("cancelled ctx becomes a fresh background-rooted ctx", func(t *testing.T) {
		parent, parentCancel := context.WithCancel(context.Background())
		parentCancel()
		got, cancel := cleanupCtxFor(parent)
		defer cancel()
		if got == parent {
			t.Fatal("expected a fresh ctx when parent is cancelled")
		}
		select {
		case <-got.Done():
			t.Fatal("fresh cleanup ctx should not be Done() immediately")
		default:
		}
		// Has a deadline (5s timeout).
		if _, ok := got.Deadline(); !ok {
			t.Fatal("fresh cleanup ctx should have a deadline")
		}
	})
}

// TestNewOrchestrator_NilShutdownCtxFallback ensures nil ctx falls back to
// context.Background() so partial construction (tests, etc.) doesn't
// nil-panic when waitForTask reads o.shutdownCtx.
func TestNewOrchestrator_NilShutdownCtxFallback(t *testing.T) {
	o := NewOrchestrator(nil, nil, "", slog.Default(), nil, nil) //nolint:staticcheck // nil ctx is the path under test
	if o.shutdownCtx == nil {
		t.Fatal("shutdownCtx is nil; constructor must fall back to context.Background()")
	}
	select {
	case <-o.shutdownCtx.Done():
		t.Fatal("background-derived ctx should not be Done()")
	default:
	}
}

// TestOrchestrator_WaitForTask_CancelsOnShutdown is the load-bearing test
// for the Swarm/K8s rolling-restart resilience fix: if the per-server
// shutdown context is cancelled while waitForTask is polling a Proxmox
// task, the function must exit promptly with "interrupted" (NOT "timeout")
// so callers know to leave the rolling-update node in its current step
// and let the next scheduler-leader resume — rather than marking it failed.
//
// The old behavior returned "timeout" here, which prod-tripped a node
// failure during a Docker Swarm reschedule: an in-flight migration was
// labelled "migration of qemu 111 failed (status: timeout)" 105 seconds
// after job start (well short of the 30-min real timeout) even though
// the Proxmox migrate task actually succeeded.
func TestOrchestrator_WaitForTask_CancelsOnShutdown(t *testing.T) {
	// Stub Proxmox: always return a "running" task — never completes on
	// its own. This way the only way out is via shutdownCtx.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/nodes/test-node/tasks/UPID:test:0/status" {
			payload, _ := json.Marshal(map[string]interface{}{
				"data": proxmox.TaskStatus{Status: "running", UPID: "UPID:test:0", Node: "test-node"},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		http.NotFound(w, r)
	}))
	defer stub.Close()

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:     stub.URL,
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	o := NewOrchestrator(shutdownCtx, nil, "", slog.Default(), nil, nil)

	done := make(chan string, 1)
	go func() {
		// First arg ignored by impl; pass Background to make that explicit.
		done <- o.waitForTask(context.Background(), client, "test-node", "UPID:test:0")
	}()

	// Give the goroutine a moment to enter the for-select before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancelShutdown()

	select {
	case status := <-done:
		if status != "interrupted" {
			t.Errorf("waitForTask = %q, want %q after shutdown cancel", status, "interrupted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitForTask did not return within 3s of shutdown cancel — pollCtx is not derived from shutdownCtx")
	}
}

// TestOrchestrator_WaitForGuestUnlocked_CancelsOnShutdown ensures the
// post-migrate lock-wait helper also returns "interrupted" (not "timeout")
// when the container is being shut down. Callers branch on this to skip
// the failNode path so the next scheduler leader can resume.
func TestOrchestrator_WaitForGuestUnlocked_CancelsOnShutdown(t *testing.T) {
	// Stub Proxmox: always reports the guest as locked, never clears.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/cluster/resources" {
			payload, _ := json.Marshal(map[string]interface{}{
				"data": []proxmox.ClusterResource{
					{Type: "qemu", VMID: 999, Node: "test-node", Lock: "migrate"},
				},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
			return
		}
		http.NotFound(w, r)
	}))
	defer stub.Close()

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:     stub.URL,
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())
	o := NewOrchestrator(shutdownCtx, nil, "", slog.Default(), nil, nil)

	done := make(chan string, 1)
	go func() {
		done <- o.waitForGuestUnlocked(context.Background(), client, "qemu", 999, "")
	}()

	// Give the goroutine a moment to enter the for-select before cancelling.
	// waitForGuestUnlocked has a 3s ticker, so cancel happens before the
	// first tick — the pollCtx.Done branch is what's under test.
	time.Sleep(50 * time.Millisecond)
	cancelShutdown()

	select {
	case status := <-done:
		if status != "interrupted" {
			t.Errorf("waitForGuestUnlocked = %q, want %q after shutdown cancel", status, "interrupted")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitForGuestUnlocked did not return within 3s of shutdown cancel — pollCtx is not derived from shutdownCtx")
	}
}

// stubOrchestrator builds an Orchestrator pointed at a stub Proxmox server.
func stubOrchestrator(t *testing.T, handler http.HandlerFunc) (*Orchestrator, *proxmox.Client, func()) {
	t.Helper()
	stub := httptest.NewServer(handler)
	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:     stub.URL,
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		stub.Close()
		t.Fatalf("NewClient: %v", err)
	}
	o := NewOrchestrator(context.Background(), nil, "", slog.Default(), nil, nil)
	return o, client, stub.Close
}

// TestOrchestrator_MigrateWithRetry_RetriesTransientLock verifies a drain
// migration that hits "VM is locked (migrate)" — a guest still settling from
// an in-flight HA migration — waits for the lock to clear and retries rather
// than failing the whole job. This is the exact prod failure (VM 106 on HV02).
func TestOrchestrator_MigrateWithRetry_RetriesTransientLock(t *testing.T) {
	var migrateCalls atomic.Int32
	o, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/migrate"):
			if migrateCalls.Add(1) == 1 {
				// First attempt: guest still locked by an in-flight migration.
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"data":null,"message":"VM is locked (migrate)\n"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":"UPID:HV02:0000ABCD:00010000:00000000:qmigrate:106:root@pam:"}`))
		case strings.HasSuffix(r.URL.Path, "/cluster/resources"):
			payload, _ := json.Marshal(map[string]interface{}{
				"data": []proxmox.ClusterResource{{Type: "qemu", VMID: 106, Node: "HV01", Lock: ""}},
			})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	})
	defer closeStub()

	guest := GuestSnapshot{VMID: 106, Name: "test", Type: "qemu", Status: "running"}
	upid, err := o.migrateWithRetry(context.Background(), client, "HV02", guest, proxmox.MigrateParams{Target: "HV01", Online: true})
	if err != nil {
		t.Fatalf("migrateWithRetry should have succeeded on retry: %v", err)
	}
	if upid == "" {
		t.Error("migrateWithRetry returned empty UPID on success")
	}
	if got := migrateCalls.Load(); got != 2 {
		t.Errorf("migrate attempts = %d, want 2 (one lock failure + one success)", got)
	}
}

// TestOrchestrator_MigrateWithRetry_NonLockErrorNoRetry ensures non-lock
// failures surface immediately without burning retry attempts.
func TestOrchestrator_MigrateWithRetry_NonLockErrorNoRetry(t *testing.T) {
	var migrateCalls atomic.Int32
	o, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/migrate") {
			migrateCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"data":null,"message":"storage 'ceph' not available\n"}`))
			return
		}
		http.NotFound(w, r)
	})
	defer closeStub()

	guest := GuestSnapshot{VMID: 106, Name: "test", Type: "qemu", Status: "running"}
	if _, err := o.migrateWithRetry(context.Background(), client, "HV02", guest, proxmox.MigrateParams{Target: "HV01", Online: true}); err == nil {
		t.Fatal("migrateWithRetry returned nil error for a non-lock failure")
	}
	if got := migrateCalls.Load(); got != 1 {
		t.Errorf("migrate attempts = %d, want 1 (non-lock errors must not retry)", got)
	}
}

// TestOrchestrator_WaitForGuestUnlocked_ConfirmsTarget covers the HA
// hamigrate→async qmigrate gap fix: with an expected target node given, the
// wait must NOT return on the first empty-lock poll while the guest is still
// at the source (the HA CRM hasn't set lock=migrate yet). It must keep polling
// until the guest is observed unlocked at the target — otherwise the next
// node's drain races the still-pending migration and hits "VM is locked".
func TestOrchestrator_WaitForGuestUnlocked_ConfirmsTarget(t *testing.T) {
	var polls atomic.Int32
	o, client, closeStub := stubOrchestrator(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/cluster/resources") {
			http.NotFound(w, r)
			return
		}
		var res proxmox.ClusterResource
		switch polls.Add(1) {
		case 1:
			// Premature gap: unlocked but still on the source node. The old
			// "first empty lock wins" check would wrongly return here.
			res = proxmox.ClusterResource{Type: "qemu", VMID: 106, Node: "HV02", Lock: ""}
		case 2:
			// HA CRM has now started the qmigrate — lock is set.
			res = proxmox.ClusterResource{Type: "qemu", VMID: 106, Node: "HV02", Lock: "migrate"}
		default:
			// Migration finished: unlocked at the target node.
			res = proxmox.ClusterResource{Type: "qemu", VMID: 106, Node: "HV01", Lock: ""}
		}
		payload, _ := json.Marshal(map[string]interface{}{"data": []proxmox.ClusterResource{res}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	})
	defer closeStub()

	done := make(chan string, 1)
	go func() {
		done <- o.waitForGuestUnlocked(context.Background(), client, "qemu", 106, "HV01")
	}()

	select {
	case status := <-done:
		if status != "completed" {
			t.Errorf("waitForGuestUnlocked = %q, want %q", status, "completed")
		}
		if got := polls.Load(); got < 3 {
			t.Errorf("polls = %d, want >= 3 (must not return on the premature first empty-lock poll)", got)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("waitForGuestUnlocked did not settle within 20s")
	}
}
