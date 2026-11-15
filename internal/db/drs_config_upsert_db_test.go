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

// A save that omits boot_holdoff_minutes (a client predating it) must keep the
// stored value, and a new row gets the 10-minute default.
//
// Not parallel: it shares the migrated test database with the package.
func TestUpsertDRSConfigKeepsAnOmittedBootHoldoff(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	ctx, pool, q := env.Ctx, env.Pool, gen.New(env.Pool)

	cluster := uuid.MustParse("d5000000-0000-4000-8000-000000000011")
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, cluster)
	}
	purge()
	defer purge()
	if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		VALUES ($1, 'cluster01', 'https://192.0.2.1:8006', 'tok', 'enc')`, cluster); err != nil {
		t.Fatal(err)
	}

	for _, step := range []struct {
		sent pgtype.Int4
		want int32
	}{
		{pgtype.Int4{}, 10},
		{pgtype.Int4{Int32: 0, Valid: true}, 0},
		{pgtype.Int4{}, 0},
	} {
		cfg, err := q.UpsertDRSConfig(ctx, gen.UpsertDRSConfigParams{
			ClusterID: cluster, Mode: "advisory", Enabled: true, Weights: json.RawMessage(`{"cpu":0.3,"memory":0.7}`),
			ImbalanceThreshold: 0.25, EvalIntervalSeconds: 300, BootHoldoffMinutes: step.sent,
		})
		if err != nil {
			t.Fatalf("UpsertDRSConfig: %v", err)
		}
		if cfg.BootHoldoffMinutes != step.want {
			t.Errorf("sent %+v: stored %d, want %d", step.sent, cfg.BootHoldoffMinutes, step.want)
		}
	}
}
