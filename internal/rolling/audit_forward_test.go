package rolling

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/audit/audittest"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// TestRollingUpdateEntriesReachSyslog: both of the engine's writers, the job's
// row and the changed object's row, go through audit.Write.
func TestRollingUpdateEntriesReachSyslog(t *testing.T) {
	t.Parallel()
	pub, next := audittest.Collector(t)
	o := NewOrchestrator(context.Background(), db.New(&memberDB{t: t, audits: true}), "",
		slog.New(slog.NewTextHandler(io.Discard, nil)), pub, nil)
	ctx, cluster, job := context.Background(), uuid.New(), uuid.New()

	o.auditLog(ctx, cluster, job, "rolling_update_completed", nil)
	o.auditObject(ctx, cluster, job, "ha_rule", "rule01", "deleted", nil)

	for _, want := range []string{`action="rolling_update_completed"`, `resource_id="rule01"`} {
		if line := next(5 * time.Second); !strings.Contains(line, want) {
			t.Errorf("collector received %q, want %s", line, want)
		}
	}
}
