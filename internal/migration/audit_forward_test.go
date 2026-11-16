package migration

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

// TestMigrationEntryReachesSyslog: a migration's own entries go through
// audit.Write, so they reach a configured collector.
func TestMigrationEntryReachesSyslog(t *testing.T) {
	t.Parallel()
	pub, next := audittest.Collector(t)
	o := NewOrchestrator(db.New(&jobDB{t: t}), "", slog.New(slog.NewTextHandler(io.Discard, nil)), pub)
	mc := &migrationContext{job: db.MigrationJob{ID: uuid.New(), SourceClusterID: uuid.New()}, userID: uuid.New()}

	o.auditLog(context.Background(), mc, "migrate_failed", `{"vmid":100}`)

	if line := next(5 * time.Second); !strings.Contains(line, `action="migrate_failed"`) || !strings.Contains(line, `vmid="100"`) {
		t.Errorf("collector received %q, want the migrate_failed entry", line)
	}
}
