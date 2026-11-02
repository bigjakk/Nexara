package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	upsServerID = uuid.MustParse("ab000000-0000-4000-8000-0000000000a0")
	upsDevA     = uuid.MustParse("ab000000-0000-4000-8000-0000000000a1")
	upsDevB     = uuid.MustParse("ab000000-0000-4000-8000-0000000000b1")
)

// The UPS queries' contracts with their callers, run against Postgres: no
// other test executes this SQL (the poller and handler tests use fakes).
//
// Skipped unless NEXARA_TEST_DB_URL names a throwaway database (CI sets it).
func TestUpsQueries(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM ups_metrics WHERE ups_id = ANY($1)`, []uuid.UUID{upsDevA, upsDevB})
		_, _ = pool.Exec(pctx, `DELETE FROM nut_servers WHERE id = $1`, upsServerID)
	}
	purge()
	defer purge()
	q := gen.New(pool)
	exec := func(t *testing.T, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(t, `INSERT INTO nut_servers (id, name, host) VALUES ($1, 'nut-test', '192.0.2.40')`, upsServerID)
	exec(t, `INSERT INTO ups_devices (id, server_id, ups_name) VALUES ($1, $3, 'a'), ($2, $3, 'b')`, upsDevA, upsDevB, upsServerID)

	t.Run("events page through a tie on started_at", func(t *testing.T) {
		// Every device of an unreachable server opens its episode at the same
		// instant, so ties are routine; a page must carry both halves of the key.
		at := time.Date(2026, 11, 2, 3, 12, 0, 0, time.UTC)
		for _, kind := range []string{"unreachable", "stale", "on_battery"} {
			if err := q.OpenUpsEvent(ctx, gen.OpenUpsEventParams{UpsID: upsDevA, Kind: kind, StartedAt: at, Details: json.RawMessage(`{}`)}); err != nil {
				t.Fatal(err)
			}
		}
		first, err := q.ListUpsEvents(ctx, gen.ListUpsEventsParams{UpsID: pgtype.UUID{Bytes: upsDevA, Valid: true}, RowLimit: 2})
		if err != nil || len(first) != 2 {
			t.Fatalf("first page: %d rows, %v", len(first), err)
		}
		last := first[len(first)-1]
		rest, err := q.ListUpsEvents(ctx, gen.ListUpsEventsParams{
			UpsID: pgtype.UUID{Bytes: upsDevA, Valid: true}, RowLimit: 2,
			BeforeStartedAt: pgtype.Timestamptz{Time: last.StartedAt, Valid: true},
			BeforeID:        pgtype.UUID{Bytes: last.ID, Valid: true},
		})
		if err != nil || len(rest) != 1 || rest[0].ID == first[0].ID || rest[0].ID == first[1].ID {
			t.Fatalf("second page: %v, %v", rest, err)
		}
	})

	t.Run("absent devices are marked, a nil list marks all, an upsert restores", func(t *testing.T) {
		present := func() map[string]bool {
			rows, err := q.ListUpsDevicesByServer(ctx, upsServerID)
			if err != nil {
				t.Fatal(err)
			}
			m := map[string]bool{}
			for _, r := range rows {
				m[r.UpsName] = true
			}
			return m
		}
		if err := q.MarkUpsDevicesAbsent(ctx, gen.MarkUpsDevicesAbsentParams{ServerID: upsServerID, PresentNames: []string{"a"}}); err != nil {
			t.Fatal(err)
		}
		if p := present(); !p["a"] || p["b"] {
			t.Fatalf("after LIST UPS named only a: present %v", p)
		}
		if err := q.MarkUpsDevicesAbsent(ctx, gen.MarkUpsDevicesAbsentParams{ServerID: upsServerID}); err != nil {
			t.Fatal(err)
		}
		if p := present(); len(p) != 0 {
			t.Fatalf("after an empty LIST UPS: present %v", p)
		}
		if _, err := q.UpsertUpsDevice(ctx, gen.UpsertUpsDeviceParams{ServerID: upsServerID, UpsName: "b"}); err != nil {
			t.Fatal(err)
		}
		if p := present(); !p["b"] {
			t.Fatalf("after b came back: present %v", p)
		}
	})

	t.Run("series keeps each bucket's extremes and reads only the named source", func(t *testing.T) {
		base := time.Now().Truncate(5 * time.Minute).Add(-10 * time.Minute)
		for i, r := range []struct {
			charge, vin float64
			ob          bool
		}{{100, 228, false}, {90, 251, true}, {80, 230, false}} {
			exec(t, `INSERT INTO ups_metrics (time, ups_id, battery_charge, input_voltage, on_battery) VALUES ($1, $2, $3, $4, $5)`,
				base.Add(time.Duration(i)*time.Minute), upsDevA, r.charge, r.vin, r.ob)
		}
		rows, err := q.GetUpsMetricSeries(ctx, gen.GetUpsMetricSeriesParams{Source: "raw", UpsID: upsDevA, Since: base.Add(-time.Minute), BucketSeconds: 300})
		if err != nil || len(rows) != 1 {
			t.Fatalf("raw: %d buckets, %v", len(rows), err)
		}
		r := rows[0]
		if r.ChargeMin.Float64 != 80 || r.InputVoltageMin.Float64 != 228 || r.InputVoltageMax.Float64 != 251 || !r.OnBattery.Bool {
			t.Errorf("bucket = charge %v, input %v–%v, on battery %v", r.ChargeMin, r.InputVoltageMin, r.InputVoltageMax, r.OnBattery)
		}
		if rows, err := q.GetUpsMetricSeries(ctx, gen.GetUpsMetricSeriesParams{Source: "nope", UpsID: upsDevA, Since: base, BucketSeconds: 300}); err != nil || len(rows) != 0 {
			t.Errorf("unknown source: %d rows, %v", len(rows), err)
		}
	})

	t.Run("deleting a server takes its readings when they go first", func(t *testing.T) {
		if err := q.DeleteUpsMetricsForServer(ctx, upsServerID); err != nil {
			t.Fatal(err)
		}
		if err := q.DeleteNutServer(ctx, upsServerID); err != nil {
			t.Fatal(err)
		}
		var left int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM ups_metrics WHERE ups_id = ANY($1)`, []uuid.UUID{upsDevA, upsDevB}).Scan(&left); err != nil || left != 0 {
			t.Fatalf("%d readings left, %v", left, err)
		}
	})
}
