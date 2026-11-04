package db

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/handlers"
	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// Fixed ids so a run that aborts before its purge leaves rows the next run's
// purge finds.
var (
	upsHistServer = uuid.MustParse("a7200000-0000-4000-8000-000000000001")
	upsHistDevice = uuid.MustParse("a7200000-0000-4000-8000-000000000002")
	upsHistOther  = uuid.MustParse("a7200000-0000-4000-8000-000000000003")
)

// A UPS's History holds its power events beside its own audit rows: each event
// once, at its start, categorized power, its end in details. Another UPS's
// events and those before the window are not its. Skipped unless
// NEXARA_TEST_DB_URL names a throwaway database.
func TestUpsDeviceHistory(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM audit_log WHERE resource_type = 'ups_device' AND resource_id = ANY($1)`,
			[]string{upsHistDevice.String(), upsHistOther.String()})
		_, _ = pool.Exec(pctx, `DELETE FROM nut_servers WHERE id = $1`, upsHistServer) // its devices and events cascade
	}
	purge()
	defer purge()
	now := time.Now().UTC().Truncate(time.Second)
	ago := func(m int) time.Time { return now.Add(-time.Duration(m) * time.Minute) }
	for _, step := range []struct {
		what, sql string
		args      []any
	}{
		{"server", `INSERT INTO nut_servers (id, name, host) VALUES ($1, 'ups-history-fixture', '192.0.2.40')`, []any{upsHistServer}},
		{"devices", `INSERT INTO ups_devices (id, server_id, ups_name) VALUES ($1, $3, 'ups01'), ($2, $3, 'ups02')`,
			[]any{upsHistDevice, upsHistOther, upsHistServer}},
		{"events", `INSERT INTO ups_events (ups_id, kind, started_at, ended_at, details) VALUES
			($1, 'on_battery', $3, $4, '{"lowest_charge": 95}'),
			($1, 'self_test', $5, $5, '{"result": "Done and passed"}'),
			($1, 'on_battery', $6, $6, '{}'),
			($2, 'on_battery', $5, NULL, '{}')`,
			[]any{upsHistDevice, upsHistOther, ago(30), ago(25), ago(10), ago(10 * 24 * 60)}},
	} {
		if _, err := pool.Exec(ctx, step.sql, step.args...); err != nil {
			t.Fatalf("seed %s: %v", step.what, err)
		}
	}
	if err := gen.New(pool).InsertAuditLogWithSource(ctx, gen.InsertAuditLogWithSourceParams{
		UserID:       pgtype.UUID{Bytes: uuid.MustParse("00000000-0000-0000-0000-000000000001"), Valid: true},
		ResourceType: "ups_device", ResourceID: upsHistDevice.String(), Action: "ups_command_sent",
		Details: json.RawMessage(`{"kind": "command", "name": "beeper.mute"}`), Source: "nexara", CreatedAt: ago(5),
	}); err != nil {
		t.Fatal(err)
	}

	p, err := handlers.UpsDeviceHistory().ListParams(uuid.Nil, handlers.HistorySelection{Objects: []handlers.HistoryObject{
		{ResourceType: "ups_device", Value: upsHistDevice.String()}}})
	if err != nil {
		t.Fatal(err)
	}
	p.Since, p.RowLimit = pgtype.Timestamptz{Time: now.Add(-7 * 24 * time.Hour), Valid: true}, 50
	rows, err := gen.New(handlers.HistoryDBTX(pool)).ListHistory(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r.Kind+" "+r.Action+" "+r.Category)
	}
	want := []string{"audit ups_command_sent commands", "power self_test power", "power on_battery power"}
	if !slices.Equal(got, want) {
		t.Fatalf("entries %v, want %v", got, want)
	}
	var outage struct {
		EndedAt      time.Time `json:"ended_at"`
		EndObserved  bool      `json:"end_observed"`
		LowestCharge float64   `json:"lowest_charge"`
	}
	if err := json.Unmarshal(rows[2].Details, &outage); err != nil || !outage.EndedAt.Equal(ago(25)) || !outage.EndObserved ||
		outage.LowestCharge != 95 || !rows[2].OccurredAt.Equal(ago(30)) || rows[2].ClusterID.Valid {
		t.Errorf("the outage %s at %v (cluster %v): %v; want it at its start, ended 25 minutes ago, its reading kept, no cluster",
			rows[2].Details, rows[2].OccurredAt, rows[2].ClusterID, err)
	}

	// Capped at two rows a source and unbounded, the events' read stops at its
	// second, and the page is whole only after it: the arm's cap, and its place
	// in the horizon.
	capped := p
	capped.Since, capped.RowCap = pgtype.Timestamptz{}, 2
	if rows, err = gen.New(handlers.HistoryDBTX(pool)).ListHistory(ctx, capped); err != nil {
		t.Fatal(err)
	}
	got = got[:0]
	for _, r := range rows {
		got = append(got, r.Kind+" "+r.Action)
	}
	if want := []string{"audit ups_command_sent", "power self_test"}; !slices.Equal(got, want) ||
		!rows[0].SearchedFrom.Valid || !rows[0].SearchedFrom.Time.Equal(ago(30).Add(time.Microsecond)) {
		t.Errorf("capped at 2: %v, whole from %v; want %v, whole from just after the outage began", got, rows[0].SearchedFrom, want)
	}
}
