package nodememory

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/audit/audittest"
)

// TestDailyReadReachesSyslog: the daily read's entry goes through audit.Write
// with the publisher NewReader was handed, so it reaches a configured collector.
func TestDailyReadReachesSyslog(t *testing.T) {
	t.Parallel()
	pub, next := audittest.Collector(t)
	r := NewReader(nil, &recordingStore{}, time.Hour, nil, pub)

	r.audit(context.Background(), uuid.New(), "pve-01", StatusOK)

	if line := next(5 * time.Second); !strings.Contains(line, `action="read_memory_modules"`) {
		t.Errorf("collector received %q, want the read_memory_modules entry", line)
	}
}
