package db

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The app's pool gets both backstops by default, a DATABASE_URL value wins over
// the default, and a lock wait past lock_timeout comes back as an ordinary
// 55P03 error that the pool logs once by statement.
func TestConnectWithRetry_SessionBackstops(t *testing.T) {
	t.Parallel()
	dbURL := testDBURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	show := func(t *testing.T, url string) (lock, idle string) {
		t.Helper()
		pool, err := ConnectWithRetry(ctx, url, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		defer pool.Close()
		if err := pool.QueryRow(ctx, "SELECT current_setting('lock_timeout'), current_setting('idle_in_transaction_session_timeout')").
			Scan(&lock, &idle); err != nil {
			t.Fatalf("read settings: %v", err)
		}
		return lock, idle
	}
	if lock, idle := show(t, dbURL); lock != "2min" || idle != "1min" {
		t.Errorf("defaults: lock_timeout %q, idle_in_transaction_session_timeout %q; want 2min, 1min", lock, idle)
	}
	if lock, idle := show(t, dbURL+"&lock_timeout=50ms"); lock != "50ms" || idle != "1min" {
		t.Errorf("URL override: lock_timeout %q, idle %q; want 50ms, 1min", lock, idle)
	}

	var logs bytes.Buffer
	pool, err := ConnectWithRetry(ctx, dbURL+"&lock_timeout=50ms", slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	holder, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer holder.Release()
	const key = 0x4E584C17
	if _, err := holder.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		t.Fatalf("take the lock: %v", err)
	}
	defer func() { _, _ = holder.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", key) }()

	_, err = pool.Exec(ctx, "SELECT pg_advisory_lock($1)", key)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		t.Fatalf("second lock: %v, want lock_not_available", err)
	}
	if out := logs.String(); strings.Count(out, "lock_timeout") != 1 || !strings.Contains(out, "statement=\"SELECT pg_advisory_lock($1)\"") {
		t.Errorf("want one log line naming the statement, got:\n%s", out)
	}
}
