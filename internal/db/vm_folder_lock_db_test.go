package db

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

var folderLockClusterID = uuid.MustParse("f01d0000-0000-4000-8000-00000000000c")

// Two moves that would close a cycle — A under B, B under A — each under the
// cluster's folder lock, as handlers.inFolderTree runs them: the second waits
// while the first holds the lock, then reads the tree the first left and
// moves nothing. Without the lock both pass their check and both commit.
func TestFolderMovesUnderTheClusterLock(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	ctx, pool := env.Ctx, env.Pool
	migrateUp(t, env.Migrate)
	q := gen.New(pool)
	purge := func() {
		pctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = pool.Exec(pctx, `DELETE FROM clusters WHERE id = $1`, folderLockClusterID)
	}
	purge()
	defer purge()
	if _, err := pool.Exec(ctx, `INSERT INTO clusters (id, name, api_url, token_id, token_secret_encrypted)
		VALUES ($1, 'cluster01', 'https://invalid.local', 'tok', 'enc')`, folderLockClusterID); err != nil {
		t.Fatal(err)
	}
	a, errA := q.CreateVMFolder(ctx, gen.CreateVMFolderParams{ClusterID: folderLockClusterID, Name: "folder01"})
	b, errB := q.CreateVMFolder(ctx, gen.CreateVMFolderParams{ClusterID: folderLockClusterID, Name: "folder02"})
	if err := errors.Join(errA, errB); err != nil {
		t.Fatal(err)
	}
	begin := func() pgx.Tx {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	move := func(tx pgx.Tx, id, parent uuid.UUID) error {
		qx := q.WithTx(tx)
		if err := qx.LockClusterFolders(ctx, folderLockClusterID); err != nil {
			return err
		}
		_, err := qx.MoveVMFolder(ctx, gen.MoveVMFolderParams{ID: id, ParentID: pgtype.UUID{Bytes: parent, Valid: true}})
		return err
	}

	first := begin()
	defer func() { _ = first.Rollback(context.Background()) }()
	if err := move(first, a.ID, b.ID); err != nil {
		t.Fatalf("the first move: %v", err)
	}
	second := make(chan error, 1)
	go func() {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			second <- err
			return
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if err = move(tx, b.ID, a.ID); err == nil {
			err = tx.Commit(ctx)
		}
		second <- err
	}()
	for waiting := false; !waiting; {
		select {
		case err := <-second:
			t.Fatalf("the second move ended (%v) while the first held the cluster's folder lock", err)
		default:
		}
		// Waiting on any lock: without the folder lock the second still waits,
		// on the first's row lock through its parent's foreign key — but only
		// after its cycle check passed against the old tree.
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_locks WHERE NOT granted)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
	}
	// A third, with a short lock_timeout, gives up while the first holds it.
	third, errThird := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if errThird != nil {
		t.Fatal(errThird)
	}
	q3 := q.WithTx(third)
	errThird = q3.SetLocalLockTimeout(ctx, "100ms")
	if errThird == nil {
		errThird = q3.LockClusterFolders(ctx, folderLockClusterID)
	}
	_ = third.Rollback(context.Background())
	if errThird == nil || !strings.Contains(errThird.Error(), "SQLSTATE 55P03") {
		t.Errorf("a wait past its lock_timeout: %v, want lock_not_available", errThird)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-second; !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("the second move: %v, want no row — B under A is a cycle once A is under B", err)
	}
	gotA, errA := q.GetVMFolder(ctx, a.ID)
	gotB, errB := q.GetVMFolder(ctx, b.ID)
	if err := errors.Join(errA, errB); err != nil {
		t.Fatal(err)
	}
	if gotA.ParentID != (pgtype.UUID{Bytes: b.ID, Valid: true}) || gotB.ParentID.Valid {
		t.Errorf("A's parent %v, B's %v; want A under B and B at the top", gotA.ParentID, gotB.ParentID)
	}
}
