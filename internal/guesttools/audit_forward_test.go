package guesttools

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/bigjakk/nexara/internal/audit/audittest"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// execOnlyDB accepts writes and refuses reads: the audit insert is an Exec.
type execOnlyDB struct{}

func (execOnlyDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (execOnlyDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected query")
}
func (execOnlyDB) QueryRow(context.Context, string, ...any) pgx.Row { return nil }

// TestAutoCancelReachesSyslog: the automatic withdrawal's entry goes through
// audit.Write with the publisher NewEngine was handed.
func TestAutoCancelReachesSyslog(t *testing.T) {
	t.Parallel()
	pub, next := audittest.Collector(t)
	e := NewEngine(db.New(execOnlyDB{}), "", slog.New(slog.NewTextHandler(io.Discard, nil)), pub)

	e.auditSupersededCancel(context.Background(), db.GuestToolsState{Vmid: 100, StagedVersion: "0.1.285-1"}, "0.1.302-1", true)

	if line := next(5 * time.Second); !strings.Contains(line, `action="guest_tools_auto_cancel"`) {
		t.Errorf("collector received %q, want the guest_tools_auto_cancel entry", line)
	}
}
