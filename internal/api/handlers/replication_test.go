package handlers

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// An edit names the fields it sent that Proxmox stored differently, read before the
// edit and after it: the dialog sends schedule and comment whatever was touched, a
// rate is kept as a number however it was typed, and a field that changed meanwhile
// without being sent is not the edit's.
func TestChangedReplicationFields(t *testing.T) {
	t.Parallel()
	job := func(schedule string, rate proxmox.FlexString, comment string, disable int) *proxmox.ReplicationJob {
		return &proxmox.ReplicationJob{Schedule: schedule, Rate: rate, Comment: comment, Disable: disable}
	}
	disable := func(n int) *int { return &n }
	sent := proxmox.UpdateReplicationJobParams{Schedule: "*/5", Rate: "20.0", Comment: "weekly", Disable: disable(0)}
	for _, tc := range []struct {
		name          string
		before, after *proxmox.ReplicationJob
		req           proxmox.UpdateReplicationJobParams
		want          []string
	}{
		{"the job could not be read before", nil, job("*/5", "20", "weekly", 0), sent, []string{}},
		{"nor after", job("*/15", "10", "nightly", 1), nil, sent, []string{}},
		{"everything sent was already so", job("*/5", "20", "weekly", 0), job("*/5", "20", "weekly", 0), sent, []string{}},
		{"everything sent, none of it stored", job("*/15", "10", "nightly", 1), job("*/15", "10", "nightly", 1), sent, []string{}},
		{"everything changed", job("*/15", "10", "nightly", 1), job("*/5", "20", "weekly", 0), sent, []string{"schedule", "rate", "comment", "disable"}},
		{"a rate kept as another spelling of the same number", job("*/5", "10", "", 0), job("*/5", "10.0", "", 0), proxmox.UpdateReplicationJobParams{Rate: "10"}, []string{}},
		{"a rate that was none and is one", job("*/5", "", "", 0), job("*/5", "10", "", 0), proxmox.UpdateReplicationJobParams{Rate: "10"}, []string{"rate"}},
		{"a field that changed meanwhile and was not sent", job("*/15", "10", "nightly", 0), job("*/5", "10", "weekly", 0), proxmox.UpdateReplicationJobParams{Rate: "10"}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := changedReplicationFields(tc.before, tc.after, tc.req); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("fields = %v, want %v", got, tc.want)
			}
		})
	}
}

// A read of the job that Proxmox does not answer in time is given up, and said so in
// the log, so the edit's audit row is not held behind it.
func TestReadReplicationJobGivesUpAfterItsTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	client, err := proxmox.NewClient(proxmox.ClientConfig{BaseURL: srv.URL, TokenID: "user@pam!test", TokenSecret: "secret"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, nil))
	got := make(chan *proxmox.ReplicationJob, 1)
	go func() {
		got <- readReplicationJob(context.Background(), 50*time.Millisecond, logger, client, uuid.New(), "100-0", "before")
	}()
	select {
	case job := <-got:
		if job != nil {
			t.Errorf("a read that timed out answered %+v, want nil", job)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the read was not given up on after its timeout")
	}
	if text := logged.String(); !strings.Contains(text, "level=WARN") || !strings.Contains(text, "could not read the job before the edit") {
		t.Errorf("the log says %q, want a warning that the job could not be read before the edit", text)
	}
}
