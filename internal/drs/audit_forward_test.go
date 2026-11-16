package drs

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/audit/audittest"
)

// TestDRSEntryReachesSyslog: a DRS move's entry goes through audit.Write, so it
// reaches a configured collector.
func TestDRSEntryReachesSyslog(t *testing.T) {
	t.Parallel()
	pub, next := audittest.Collector(t)
	e := &Executor{queries: &recordingExecQueries{}, logger: slog.New(slog.DiscardHandler), eventPub: pub}

	e.auditLog(context.Background(), uuid.New(), "", "drs_migrate",
		Recommendation{VMID: 101, VMType: "qemu", SourceNode: "pve-01", TargetNode: "pve-02"}, nil)

	if line := next(5 * time.Second); !strings.Contains(line, `action="drs_migrate"`) || !strings.Contains(line, `vmid="101"`) {
		t.Errorf("collector received %q, want the drs_migrate entry", line)
	}
}
