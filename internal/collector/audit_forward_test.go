package collector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/audit/audittest"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// TestSyncFailureReachesSyslog: the syncer's entries go through audit.Write,
// so a sync failure reaches a configured collector as a handler's entry does.
func TestSyncFailureReachesSyslog(t *testing.T) {
	t.Parallel()
	pub, next := audittest.Collector(t)
	s := &Syncer{queries: &mockQueries{}, logger: testLogger()}
	s.SetEventPublisher(pub)

	s.reportSyncError(context.Background(), makeCluster(t, "https://192.0.2.10:8006/"), proxmox.ErrConnectionFailed)

	if line := next(5 * time.Second); !strings.Contains(line, `resource_type="cluster" `) || !strings.Contains(line, `action="sync_failed"`) {
		t.Errorf("collector received %q, want the sync_failed entry", line)
	}
}

// forwardingEventPub records events like mockEventPub and forwards like the
// process publisher.
type forwardingEventPub struct {
	*mockEventPub
	fwd *proxsyslog.Forwarder
}

func (p forwardingEventPub) SyslogForwarder() *proxsyslog.Forwarder { return p.fwd }

// TestObservedTaskReachesSyslog: a task observed on Proxmox is forwarded
// stamped with the task's start, and its audit_entry event is published once,
// by publishTaskEvents, not again by the writer.
func TestObservedTaskReachesSyslog(t *testing.T) {
	t.Parallel()
	collector, next := audittest.Collector(t)
	rec := &mockEventPub{}
	s := &Syncer{queries: newMockQueries(), logger: testLogger()}
	s.SetEventPublisher(forwardingEventPub{rec, collector.SyslogForwarder()})
	start := time.Now().Add(-time.Hour).Truncate(time.Second)
	task := proxmox.NodeTask{UPID: "UPID:pve-01:qmsnapshot:100:run", Type: "qmsnapshot", ID: "100", StartTime: start.Unix()}

	if _, err := s.ingestTask(context.Background(), db.Cluster{ID: uuid.New()}, "pve-01", task, nil, nil); err != nil {
		t.Fatalf("ingestTask: %v", err)
	}

	line := next(5 * time.Second)
	if !strings.Contains(line, " "+start.UTC().Format(time.RFC3339Nano)+" nexara audit ") || !strings.Contains(line, `resource_id="100"`) {
		t.Errorf("collector received %q, want the task's entry stamped %s", line, start.UTC().Format(time.RFC3339Nano))
	}
	if n := rec.countKind(events.KindAuditEntry); n != 1 {
		t.Errorf("published %d audit_entry events, want 1", n)
	}
}
