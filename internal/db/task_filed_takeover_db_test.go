package db

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var (
	filedCluster = uuid.MustParse("15330000-0000-4000-8000-000000000001")
	filedOther   = uuid.MustParse("15330000-0000-4000-8000-000000000002")
	filedFiler   = uuid.MustParse("15330000-0000-4000-8000-0000000000f1")
	filedRunner  = uuid.MustParse("15330000-0000-4000-8000-0000000000f2")
)

// TestFiledTaskGivesWayToWhoeverRanIt: a row filed through POST /api/v1/tasks
// never replaces a row on record, and is taken over — actor, source, the
// earlier start — by the dispatcher's or the collector's insert in its own
// cluster; the collector's dedup does not count it as recorded.
// Not parallel: it writes to the shared schema. Skipped without NEXARA_TEST_DB_URL.
func TestFiledTaskGivesWayToWhoeverRanIt(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)

	clusters := []uuid.UUID{filedCluster, filedOther}
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM task_history WHERE cluster_id = ANY($1)`, clusters)
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = ANY($1)`, clusters)
		_, _ = pool.Exec(pctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{filedFiler, filedRunner})
	}
	purge()
	defer purge() // A defer, not t.Cleanup: the pool must still be open (see setupMigration).
	for i, id := range clusters {
		if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
			VALUES ($1, $2, 'https://192.0.2.10:8006', 'tok', 'enc')`, id, fmt.Sprintf("cluster0%d", i+1)); err != nil {
			t.Fatalf("seed cluster: %v", err)
		}
	}
	for i, id := range []uuid.UUID{filedFiler, filedRunner} {
		if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, password_hash, display_name)
			VALUES ($1, $2, 'x', 'Filed Task Test')`, id, fmt.Sprintf("filed-task-%d@example.invalid", i)); err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}

	proxmoxStart := time.Unix(1700000000, 0)
	file := func(cluster uuid.UUID, upid string) error {
		_, err := q.InsertFiledTaskHistory(ctx, gen.InsertFiledTaskHistoryParams{ClusterID: cluster, UserID: filedFiler,
			Upid: upid, Node: "pve-01", TaskType: "qmstart", StartedAt: proxmoxStart})
		return err
	}
	dispatch := func(upid string) error {
		_, err := q.InsertTaskHistory(ctx, gen.InsertTaskHistoryParams{ClusterID: filedCluster, UserID: filedRunner,
			Upid: upid, Description: "Start linux01", Status: "running", Node: "pve-01", TaskType: "qmstart"})
		return err
	}
	ingest := func(upid string) error {
		return q.InsertExternalTaskHistory(ctx, gen.InsertExternalTaskHistoryParams{ClusterID: filedCluster,
			UserID: systemUserID, Upid: upid, Description: "Start linux01", Status: "running", Node: "pve-01",
			TaskType: "qmstart", StartedAt: proxmoxStart.Add(-time.Minute)})
	}
	// The filer's word on how it ended, then Proxmox's.
	forged := func(u string) error {
		if err := file(filedCluster, u); err != nil {
			return err
		}
		_, err := q.UpdateTaskHistory(ctx, gen.UpdateTaskHistoryParams{Upid: u, UserID: filedFiler, Status: "completed", ExitStatus: "OK",
			FinishedAt: pgtype.Timestamptz{Time: proxmoxStart.Add(time.Hour), Valid: true}})
		return err
	}
	ingestFailed := func(upid string) error {
		return q.InsertExternalTaskHistory(ctx, gen.InsertExternalTaskHistoryParams{ClusterID: filedCluster,
			UserID: systemUserID, Upid: upid, Description: "Start linux01", Status: "failed", ExitStatus: "boom",
			Node: "pve-01", TaskType: "qmstart", StartedAt: proxmoxStart,
			FinishedAt: pgtype.Timestamptz{Time: proxmoxStart.Add(time.Minute), Valid: true}})
	}
	tests := []struct {
		name          string
		first, second func(string) error
		secondErr     error
		user          uuid.UUID
		source        string
		start         time.Time
		verdict       string // status/exit/finish, when the case is about it
	}{
		{"filed, then dispatched", func(u string) error { return file(filedCluster, u) }, dispatch, nil, filedRunner, "nexara", proxmoxStart, ""},
		{"filed, then ingested", func(u string) error { return file(filedCluster, u) }, ingest, nil, systemUserID, "proxmox", proxmoxStart.Add(-time.Minute), ""},
		{"dispatched, then filed", dispatch, func(u string) error { return file(filedCluster, u) }, pgx.ErrNoRows, filedRunner, "nexara", time.Time{}, ""},
		{"filed in another cluster, then dispatched here", func(u string) error { return file(filedOther, u) }, dispatch, pgx.ErrNoRows, filedFiler, "filed", proxmoxStart, ""},
		{"filed and forged completed, then ingested failed", forged, ingestFailed, nil, systemUserID, "proxmox", proxmoxStart,
			"failed/boom/" + proxmoxStart.Add(time.Minute).UTC().Format(time.RFC3339)},
		{"filed and forged completed, then dispatched", forged, dispatch, nil, filedRunner, "nexara", proxmoxStart, "running//"},
	}
	for i, tt := range tests {
		upid := fmt.Sprintf("UPID:pve-01:%08X:00000001:6553F100:qmstart:100:root@pam:", i+1)
		if err := tt.first(upid); err != nil {
			t.Fatalf("%s: first insert: %v", tt.name, err)
		}
		if err := tt.second(upid); !errors.Is(err, tt.secondErr) {
			t.Errorf("%s: second insert = %v, want %v", tt.name, err, tt.secondErr)
		}
		row, err := q.GetTaskByUpid(ctx, upid)
		if err != nil {
			t.Fatalf("%s: read back: %v", tt.name, err)
		}
		if row.UserID != tt.user || row.Source != tt.source || !tt.start.IsZero() && !row.StartedAt.Equal(tt.start) {
			t.Errorf("%s: row is %s/%s from %s, want %s/%s from %s", tt.name, row.UserID, row.Source, row.StartedAt,
				tt.user, tt.source, tt.start)
		}
		finished := ""
		if row.FinishedAt.Valid {
			finished = row.FinishedAt.Time.UTC().Format(time.RFC3339)
		}
		if got := row.Status + "/" + row.ExitStatus + "/" + finished; tt.verdict != "" && got != tt.verdict {
			t.Errorf("%s: verdict %s, want %s", tt.name, got, tt.verdict)
		}
	}

	// A filed row alone is not "recorded" for the collector's dedup.
	filedOnly := "UPID:pve-01:000000FF:00000001:6553F100:qmstart:100:root@pam:"
	if err := file(filedCluster, filedOnly); err != nil {
		t.Fatal(err)
	}
	seen, err := q.ListExistingTaskHistoryUPIDs(ctx, []string{filedOnly})
	if err != nil || len(seen) != 0 {
		t.Errorf("ListExistingTaskHistoryUPIDs = %v, %v; want the filed row left out", seen, err)
	}
}
