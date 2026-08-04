package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

func stampedAt(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// TestDRSShouldEvaluate pins the scheduling contract for DRS passes: the
// per-cluster interval throttles automatic evaluation, a queued operator
// request (migration 000079) bypasses it, and a cluster this process has never
// evaluated always runs so a new leader picks up the whole fleet.
func TestDRSShouldEvaluate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		cfg      db.DrsConfig
		lastEval time.Time
		seen     bool
		want     bool
	}{
		{
			name: "never evaluated by this process runs",
			cfg:  db.DrsConfig{EvalIntervalSeconds: 300},
			seen: false,
			want: true,
		},
		{
			name:     "inside the interval is throttled",
			cfg:      db.DrsConfig{EvalIntervalSeconds: 300},
			lastEval: now.Add(-100 * time.Second),
			seen:     true,
			want:     false,
		},
		{
			name:     "interval elapsed runs",
			cfg:      db.DrsConfig{EvalIntervalSeconds: 300},
			lastEval: now.Add(-301 * time.Second),
			seen:     true,
			want:     true,
		},
		{
			name:     "exactly at the interval runs",
			cfg:      db.DrsConfig{EvalIntervalSeconds: 300},
			lastEval: now.Add(-300 * time.Second),
			seen:     true,
			want:     true,
		},
		{
			name:     "queued request bypasses the interval",
			cfg:      db.DrsConfig{EvalIntervalSeconds: 300, EvalRequestedAt: stampedAt(now.Add(-1 * time.Second))},
			lastEval: now.Add(-1 * time.Second),
			seen:     true,
			want:     true,
		},
		{
			name:     "non-positive interval falls back to the default",
			cfg:      db.DrsConfig{EvalIntervalSeconds: 0},
			lastEval: now.Add(-299 * time.Second),
			seen:     true,
			want:     false,
		},
		{
			name:     "non-positive interval runs past the default",
			cfg:      db.DrsConfig{EvalIntervalSeconds: 0},
			lastEval: now.Add(-301 * time.Second),
			seen:     true,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := drsShouldEvaluate(tt.cfg, tt.lastEval, tt.seen, now); got != tt.want {
				t.Errorf("drsShouldEvaluate = %t, want %t", got, tt.want)
			}
		})
	}
}

// fakeDRSClearer records the params clearDRSEvalRequestOn passes through.
type fakeDRSClearer struct {
	calls []db.ClearDRSEvalRequestParams
	err   error
}

func (f *fakeDRSClearer) ClearDRSEvalRequest(_ context.Context, arg db.ClearDRSEvalRequestParams) error {
	f.calls = append(f.calls, arg)
	return f.err
}

// TestClearDRSEvalRequestOn_UsesReadTimestamp locks the invariant that makes
// the queue safe: the clear is issued against the timestamp read at the start
// of the pass, never now(). Combined with the query's `eval_requested_at <= $2`
// guard, a request stamped while the pass is running survives to the next tick
// instead of being swallowed by this one.
func TestClearDRSEvalRequestOn_UsesReadTimestamp(t *testing.T) {
	t.Parallel()

	clusterID := uuid.New()
	requestedAt := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	cfg := db.DrsConfig{ClusterID: clusterID, EvalRequestedAt: stampedAt(requestedAt)}

	clearer := &fakeDRSClearer{}
	if err := clearDRSEvalRequestOn(context.Background(), clearer, cfg); err != nil {
		t.Fatalf("clearDRSEvalRequestOn: %v", err)
	}

	if len(clearer.calls) != 1 {
		t.Fatalf("clear calls = %d, want 1", len(clearer.calls))
	}
	got := clearer.calls[0]
	if got.ClusterID != clusterID {
		t.Errorf("ClusterID = %v, want %v", got.ClusterID, clusterID)
	}
	if !got.EvalRequestedAt.Time.Equal(requestedAt) {
		t.Errorf("EvalRequestedAt = %v, want the timestamp read from the config (%v) — "+
			"clearing against now() would swallow a request stamped mid-pass",
			got.EvalRequestedAt.Time, requestedAt)
	}
}

// TestClearDRSEvalRequestOn_NoRequestIsNoop keeps the clear off the hot path
// for the overwhelmingly common case: a scheduled pass with nothing queued.
func TestClearDRSEvalRequestOn_NoRequestIsNoop(t *testing.T) {
	t.Parallel()

	clearer := &fakeDRSClearer{}
	cfg := db.DrsConfig{ClusterID: uuid.New()} // EvalRequestedAt zero → invalid

	if err := clearDRSEvalRequestOn(context.Background(), clearer, cfg); err != nil {
		t.Fatalf("clearDRSEvalRequestOn: %v", err)
	}
	if len(clearer.calls) != 0 {
		t.Errorf("clear calls = %d, want 0 when no request is queued", len(clearer.calls))
	}
}

// TestClearDRSEvalRequestOn_PropagatesError ensures a failed clear surfaces to
// the caller (which logs it) rather than being swallowed here.
func TestClearDRSEvalRequestOn_PropagatesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("boom")
	clearer := &fakeDRSClearer{err: wantErr}
	cfg := db.DrsConfig{ClusterID: uuid.New(), EvalRequestedAt: stampedAt(time.Now())}

	if err := clearDRSEvalRequestOn(context.Background(), clearer, cfg); !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}
}
