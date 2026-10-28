package db

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	vsServer = uuid.MustParse("5e550000-0000-4000-8000-000000000001")
	vsOther  = uuid.MustParse("5e550000-0000-4000-8000-000000000002")
	vsJob1   = uuid.MustParse("5e550000-0000-4000-8000-0000000000a1")
	vsJob2   = uuid.MustParse("5e550000-0000-4000-8000-0000000000a2")
)

// TestListVeeamSessionsByServerForOneJob holds the job filter to what a job's
// History needs: that job's runs, newest first, in front of the row limit and
// on this server alone — a Veeam server restored onto a new one keeps its job
// ids.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database).
func TestListVeeamSessionsByServerForOneJob(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM veeam_servers WHERE id = ANY($1)`, []uuid.UUID{vsServer, vsOther})
	}
	purge()
	defer purge()
	migrateUp(t, env.Migrate)

	for _, id := range []uuid.UUID{vsServer, vsOther} {
		if _, err := pool.Exec(ctx, `INSERT INTO veeam_servers (id, name, base_url, username, password_encrypted)
			VALUES ($1, $2, 'https://vbr.invalid:9419', 'u', 'x')`, id, "vbr-"+id.String()[:8]); err != nil {
			t.Fatalf("seed server: %v", err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	run := func(server uuid.UUID, name string, job *uuid.UUID, ago time.Duration) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO veeam_sessions (veeam_server_id, veeam_id, job_veeam_id, name, creation_time)
			VALUES ($1, $2, $3, $4, $5)`, server, uuid.New(), job, name, now.Add(-ago)); err != nil {
			t.Fatalf("seed run %s: %v", name, err)
		}
	}
	run(vsServer, "job1-old", &vsJob1, 3*time.Hour)
	run(vsServer, "job2", &vsJob2, 2*time.Hour)
	run(vsServer, "job1-new", &vsJob1, time.Hour)
	run(vsServer, "no-job", nil, 30*time.Minute)
	run(vsOther, "other-servers-job1", &vsJob1, 10*time.Minute)

	q := gen.New(pool)
	for _, tt := range []struct {
		name  string
		job   pgtype.UUID
		limit int32
		want  []string
	}{
		{"every run", pgtype.UUID{}, 10, []string{"no-job", "job1-new", "job2", "job1-old"}},
		{"one job's runs, this server's alone", pgtype.UUID{Bytes: vsJob1, Valid: true}, 10, []string{"job1-new", "job1-old"}},
		// The filter comes before the limit: the newest run of the server is not job 2's.
		{"a job whose run is not the newest", pgtype.UUID{Bytes: vsJob2, Valid: true}, 1, []string{"job2"}},
	} {
		rows, err := q.ListVeeamSessionsByServer(ctx, gen.ListVeeamSessionsByServerParams{
			VeeamServerID: vsServer, JobVeeamID: tt.job, RowLimit: tt.limit,
		})
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		got := make([]string, 0, len(rows))
		for _, r := range rows {
			got = append(got, r.Name)
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}
