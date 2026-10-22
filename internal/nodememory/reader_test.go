package nodememory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/rolling"
	sshpkg "github.com/bigjakk/nexara/internal/ssh"
)

var testNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// recordingStore records what the reader stores; the SQL that keeps the last
// good read through a failure is tested against Postgres in internal/db.
type recordingStore struct {
	nodes    []db.ListNodeMemoryReadStateRow
	reads    []db.UpsertNodeMemoryReadParams
	failures []db.RecordNodeMemoryFailureParams
	audits   []db.InsertAuditLogParams
	writeErr error // fails every read and failure write
}

func (s *recordingStore) ListNodeMemoryReadState(context.Context) ([]db.ListNodeMemoryReadStateRow, error) {
	return s.nodes, nil
}
func (s *recordingStore) UpsertNodeMemoryRead(_ context.Context, p db.UpsertNodeMemoryReadParams) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.reads = append(s.reads, p)
	return nil
}
func (s *recordingStore) RecordNodeMemoryFailure(_ context.Context, p db.RecordNodeMemoryFailureParams) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	s.failures = append(s.failures, p)
	return nil
}
func (s *recordingStore) InsertAuditLog(_ context.Context, p db.InsertAuditLogParams) error {
	s.audits = append(s.audits, p)
	return nil
}

// The pass reads a node only when it is online and due, retries a read that
// never dialed on every pass, makes a read that reached the node and failed
// wait a whole interval, stores each outcome with a reason fit to show, and
// audits as the system actor every read that dialed.
func TestRunDue(t *testing.T) {
	t.Parallel()
	const interval = 24 * time.Hour
	attempted := func(ago time.Duration, status string) (pgtype.Timestamptz, pgtype.Text) {
		return pgtype.Timestamptz{Time: testNow.Add(-ago), Valid: true}, pgtype.Text{String: status, Valid: true}
	}
	good := &sshpkg.ExecResult{Stdout: qemuGuest}
	tests := []struct {
		name       string
		nodeStatus string
		attempted  time.Duration // 0: never read
		lastStatus string
		res        *sshpkg.ExecResult
		err        error
		writeErr   error
		panics     bool
		wantRun    bool
		wantStored string // StatusOK, StatusUnavailable, StatusError, or "" for nothing
		wantReason string
		wantAudit  string // the audited status, when it is not the stored one
	}{
		{name: "never read", nodeStatus: "online", res: good, wantRun: true, wantStored: StatusOK},
		{name: "read 23 h ago", nodeStatus: "online", attempted: 23 * time.Hour, lastStatus: StatusOK, res: good},
		{name: "read a day ago", nodeStatus: "online", attempted: interval, lastStatus: StatusOK, res: good, wantRun: true, wantStored: StatusOK},
		{name: "offline", nodeStatus: "offline", res: good},
		{
			name: "no credentials is re-checked every pass", nodeStatus: "online", attempted: 5 * time.Minute, lastStatus: StatusUnavailable,
			err: fmt.Errorf("%w: no rows", rolling.ErrSSHNotConfigured), wantRun: true, wantStored: StatusUnavailable, wantReason: "SSH Credentials",
		},
		{name: "a failed login waits the interval", nodeStatus: "online", attempted: time.Hour, lastStatus: StatusError, res: good},
		{
			name: "a login that is not root", nodeStatus: "online", res: &sshpkg.ExecResult{ExitCode: 1, Stderr: "/dev/mem: Permission denied"},
			wantRun: true, wantStored: StatusError, wantReason: "needs root",
		},
		{
			name: "dmidecode missing", nodeStatus: "online", res: &sshpkg.ExecResult{ExitCode: 127, Stderr: "bash: /usr/sbin/dmidecode: No such file or directory"},
			wantRun: true, wantStored: StatusError, wantReason: "not installed",
		},
		{
			name: "a read the store refuses is still audited", nodeStatus: "online", res: good, writeErr: errors.New("invalid input syntax for type json"),
			wantRun: true, wantAudit: "store_failed",
		},
		{
			name: "a read that panics is recovered, recorded and audited", nodeStatus: "online", panics: true,
			wantRun: true, wantStored: StatusError, wantReason: "could not be read", wantAudit: "store_failed",
		},
		{
			name: "an unclassified error says nothing of its detail", nodeStatus: "online", err: errors.New("SSH handshake with 192.0.2.10:22: unable to authenticate"),
			wantRun: true, wantStored: StatusError, wantReason: "Could not read the memory layout",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			node := db.ListNodeMemoryReadStateRow{NodeID: uuid.New(), ClusterID: uuid.New(), Name: "pve-01", Status: tt.nodeStatus}
			if tt.attempted > 0 {
				node.AttemptedAt, node.ReadStatus = attempted(tt.attempted, tt.lastStatus)
			}
			store := &recordingStore{nodes: []db.ListNodeMemoryReadStateRow{node}, writeErr: tt.writeErr}
			runs := 0
			run := func(_ context.Context, clusterID uuid.UUID, nodeName, command string) (*sshpkg.ExecResult, error) {
				runs++
				if tt.panics {
					panic("a parser bug on hostile output")
				}
				if clusterID != node.ClusterID || nodeName != "pve-01" || command != Command {
					t.Errorf("ran %q on %s/%s", command, clusterID, nodeName)
				}
				return tt.res, tt.err
			}
			r := NewReader(run, store, interval, nil)
			r.now = func() time.Time { return testNow }
			r.RunDue(context.Background())

			if (runs > 0) != tt.wantRun {
				t.Fatalf("runs = %d, want a run: %v", runs, tt.wantRun)
			}
			var stored, reason string
			switch {
			case len(store.reads) == 1 && len(store.failures) == 0:
				stored = StatusOK
				if p := store.reads[0]; p.NodeID != node.NodeID || !p.ReadAt.Equal(testNow) || p.InstalledBytes != 16<<30 || p.SlotsTotal != 1 {
					t.Errorf("stored read = %+v", p)
				}
			case len(store.failures) == 1 && len(store.reads) == 0:
				stored, reason = store.failures[0].Status, store.failures[0].Reason
				if strings.Contains(reason, "192.0.2.10") {
					t.Errorf("reason %q carries the error's address", reason)
				}
			case len(store.reads)+len(store.failures) > 0:
				t.Fatalf("stored %d reads and %d failures for one node", len(store.reads), len(store.failures))
			}
			if stored != tt.wantStored || !strings.Contains(reason, tt.wantReason) {
				t.Errorf("stored %q with reason %q; want %q containing %q", stored, reason, tt.wantStored, tt.wantReason)
			}
			wantAudits := 0
			if tt.wantRun && tt.wantStored != StatusUnavailable {
				wantAudits = 1
			}
			if len(store.audits) != wantAudits {
				t.Fatalf("audited %d reads, want %d", len(store.audits), wantAudits)
			}
			if wantAudits == 1 {
				wantStatus := tt.wantAudit
				if wantStatus == "" {
					wantStatus = tt.wantStored
				}
				if a := store.audits[0]; a.UserID.Bytes != auth.SystemUserID || a.Action != "read_memory_modules" || a.ResourceID != "pve-01" ||
					a.ClusterID.Bytes != node.ClusterID || !strings.Contains(string(a.Details), `"status":"`+wantStatus+`"`) {
					t.Errorf("audit row = %+v, details %s; want status %s", a, a.Details, wantStatus)
				}
			}
		})
	}
}

// Read now refuses a second read within a minute of one that reached the node,
// each being a root login, but not after one that dialed nothing.
func TestReadNowCooldown(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		firstErr error
		after    time.Duration
		wantSoon bool
	}{
		{"a read that dialed holds off another for a minute", nil, 59 * time.Second, true},
		{"after the minute it reads again", nil, time.Minute, false},
		{"a read that dialed nothing holds off nothing", fmt.Errorf("%w: no rows", rolling.ErrSSHNotConfigured), time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			now, runs := testNow, 0
			run := func(context.Context, uuid.UUID, string, string) (*sshpkg.ExecResult, error) {
				runs++
				if runs == 1 && tt.firstErr != nil {
					return nil, tt.firstErr
				}
				return &sshpkg.ExecResult{Stdout: qemuGuest}, nil
			}
			r := NewReader(run, &recordingStore{}, 24*time.Hour, nil)
			r.now = func() time.Time { return now }
			cluster, node := uuid.New(), uuid.New()
			if err := r.ReadNode(context.Background(), cluster, node, "pve-01"); err != nil {
				t.Fatalf("first read: %v", err)
			}
			now = now.Add(tt.after)
			err := r.ReadNode(context.Background(), cluster, node, "pve-01")
			wantRuns := 2
			if tt.wantSoon {
				wantRuns = 1
			}
			if errors.Is(err, ErrReadTooSoon) != tt.wantSoon || runs != wantRuns {
				t.Errorf("second read: err %v after %d runs; want too soon: %v after %d", err, runs, tt.wantSoon, wantRuns)
			}
		})
	}
}
