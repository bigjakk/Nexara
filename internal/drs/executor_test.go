package drs

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestNewExecutor_NilShutdownCtxFallback ensures nil ctx falls back to
// context.Background() so partial construction (tests, etc.) doesn't
// nil-panic when waitForTask reads e.shutdownCtx.
func TestNewExecutor_NilShutdownCtxFallback(t *testing.T) {
	e := NewExecutor(nil, nil, slog.Default(), nil) //nolint:staticcheck // nil ctx is the path under test
	if e.shutdownCtx == nil {
		t.Fatal("shutdownCtx is nil; constructor must fall back to context.Background()")
	}
	select {
	case <-e.shutdownCtx.Done():
		t.Fatal("background-derived ctx should not be Done()")
	default:
	}
}

// TestExecutor_WaitForTask_CancelsOnShutdown is the load-bearing test for
// Finding #14 on the DRS path: a SIGTERM-induced shutdown context cancel
// must abort the 30-minute poll loop promptly. The 10-second final-check
// against context.Background() still runs (sees "running" → returns
// "timeout") so a tracked task isn't orphaned without an outcome.
func TestExecutor_WaitForTask_CancelsOnShutdown(t *testing.T) {
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
	e := NewExecutor(shutdownCtx, nil, slog.Default(), nil)

	type result struct {
		status string
		detail string
	}
	done := make(chan result, 1)
	go func() {
		s, d := e.waitForTask(context.Background(), client, "test-node", "UPID:test:0")
		done <- result{s, d}
	}()

	time.Sleep(50 * time.Millisecond)
	cancelShutdown()

	select {
	case r := <-done:
		if r.status != "timeout" {
			t.Errorf("waitForTask status = %q, want %q after shutdown cancel", r.status, "timeout")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waitForTask did not return within 3s of shutdown cancel — pollCtx is not derived from shutdownCtx")
	}
}
