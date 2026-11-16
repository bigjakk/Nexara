package collector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/audit/audittest"
	"github.com/bigjakk/nexara/internal/proxmox"
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
