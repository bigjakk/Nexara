package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// upsertSettingSQL mirrors the UpsertSetting statement in queries/settings.sql
// verbatim. The whole point of migration 000075 is that this exact ON CONFLICT
// target behaves differently before and after it, so the test drives the real
// statement rather than a paraphrase.
const upsertSettingSQL = `
INSERT INTO settings (key, value, scope, scope_id)
VALUES ($1, $2, $3, $4)
ON CONFLICT (key, scope, scope_id)
DO UPDATE SET value = EXCLUDED.value, updated_at = now()`

// TestMigration075_DedupsAndPreventsSharedScopeDuplicates is the
// data-preservation + regression lock for migration 000075, which swaps the
// settings uniqueness constraint from a plain `UNIQUE (key, scope, scope_id)`
// to `UNIQUE NULLS NOT DISTINCT (...)` after deduping the rows the old form
// allowed to accumulate.
//
// Under a plain UNIQUE, Postgres treats NULLs as distinct, so no two rows with
// scope_id IS NULL ever conflict — and every shared-scope settings row is keyed
// that way. The ON CONFLICT inference above could therefore never match one,
// and each write APPENDED a row instead of updating. Three properties are
// asserted end-to-end:
//
//  1. The defect reproduces at 74 — three shared-scope upserts of one key leave
//     three rows. This is the "before" half of the lock; without it the "after"
//     assertions could pass against a schema that never had the bug.
//  2. Dedup + preservation — 075 collapses each duplicated (key, scope) group to
//     its NEWEST row (the value the admin last saved, not the stale one an
//     unordered LIMIT 1 tended to return), and leaves per-user rows alone.
//  3. THE REGRESSION LOCK — after 075 the same upsert UPDATES IN PLACE, so a
//     shared-scope key can never grow a second row again.
//
// The dedup partitions on (key, scope) rather than filtering to scope='global',
// because before the scope allow-list landed a 'cluster'-scoped write also fell
// through to scope_id = NULL; that path is covered too.
//
// The down migration is round-tripped: the plain UNIQUE comes back and the
// surviving row is untouched.
//
// Skipped unless NEXARA_TEST_DB_URL is set (a throwaway database — never the
// live nexara DB, since this round-trips the schema up and down).
func TestMigration075_DedupsAndPreventsSharedScopeDuplicates(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()

	ctx, pool, m := env.Ctx, env.Pool, env.Migrate

	const (
		globalKey  = "migration-075-test.branding"
		clusterKey = "migration-075-test.legacy-cluster"
		userKey    = "migration-075-test.preferences"
		tieKey     = "migration-075-test.same-txn-tie"
	)
	// Two ids straddling the same timestamp, for the tiebreak case below.
	tieLowID := uuid.MustParse("75000000-0000-4000-8000-00000000000a")
	tieHighID := uuid.MustParse("75000000-0000-4000-8000-00000000000b")
	// Two users holding the SAME key — the shape every real per-user setting has.
	userScopeID := pgtype.UUID{
		Bytes: uuid.MustParse("75000000-0000-4000-8000-000000000001"),
		Valid: true,
	}
	userScopeID2 := pgtype.UUID{
		Bytes: uuid.MustParse("75000000-0000-4000-8000-000000000002"),
		Valid: true,
	}

	// settings.scope_id carries no foreign key, so the seeded rows stand alone
	// and cleanup is a plain delete by key — version-agnostic across 74/75.
	//
	// Deliberately a defer rather than t.Cleanup: t.Cleanup runs AFTER the test
	// function's defers, by which point `defer env.Cleanup()` has already closed
	// the pool and cancelled env.Ctx, so the delete would silently no-op and leak
	// rows into the next run's counts. Registering the defer after env.Cleanup's
	// makes it run first (LIFO), while the pool is still open. It carries its own
	// context so it stays correct if the ordering ever shifts. The up-front call
	// clears rows left by a run that aborted before its defers.
	testKeys := []string{globalKey, clusterKey, userKey, tieKey}
	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = pool.Exec(pctx, `DELETE FROM settings WHERE key = ANY($1)`, testKeys)
	}
	purge()
	defer purge()

	countRows := func(key string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM settings WHERE key = $1`, key).Scan(&n); err != nil {
			t.Fatalf("count rows for %s: %v", key, err)
		}
		return n
	}
	uniqueConstraintDef := func() string {
		t.Helper()
		var def string
		// Pinned to the constraint name so a future migration adding a second
		// unique constraint to settings can't make these assertions flaky.
		if err := pool.QueryRow(ctx,
			`SELECT pg_get_constraintdef(oid) FROM pg_constraint
			 WHERE conrelid = 'settings'::regclass AND conname = 'settings_key_scope_scope_id_key'`).Scan(&def); err != nil {
			t.Fatalf("read settings unique constraint: %v", err)
		}
		return def
	}
	sharedUpsert := func(key, value, scope string) {
		t.Helper()
		if _, err := pool.Exec(ctx, upsertSettingSQL, key, value, scope, nil); err != nil {
			t.Fatalf("upsert %s=%s: %v", key, value, err)
		}
	}

	// Step 1: migrate to 74 — settings still carries the NULL-distinct UNIQUE.
	if err := m.Migrate(74); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate to 74: %v", err)
	}
	if got := uniqueConstraintDef(); got != "UNIQUE (key, scope, scope_id)" {
		t.Fatalf("constraint at 74: got %q, want the plain NULL-distinct UNIQUE", got)
	}

	// Step 2: reproduce the defect. Each Exec is its own transaction, so now()
	// strictly increases and "newest" is unambiguous.
	sharedUpsert(globalKey, `{"logo":"oldest"}`, "global")
	sharedUpsert(globalKey, `{"logo":"middle"}`, "global")
	sharedUpsert(globalKey, `{"logo":"newest"}`, "global")
	if n := countRows(globalKey); n != 3 {
		t.Fatalf("pre-075 defect did not reproduce: three shared-scope upserts left %d row(s), want 3 "+
			"(did 000001's UNIQUE get rewritten? migrations are append-only)", n)
	}

	// Legacy 'cluster'-scoped writes landed on scope_id = NULL too.
	sharedUpsert(clusterKey, `{"v":"stale"}`, "cluster")
	sharedUpsert(clusterKey, `{"v":"current"}`, "cluster")
	if n := countRows(clusterKey); n != 2 {
		t.Fatalf("cluster-scope duplicates at 74: got %d row(s), want 2", n)
	}

	// Per-user rows were always covered by the constraint and must survive 075
	// untouched, which is what makes the dedup safe to run unconditionally.
	//
	// TWO users under one key, differing only by scope_id, is the case that
	// actually locks the dedup's `WHERE scope_id IS NULL` guard. A lone per-user
	// row proves nothing: it sits alone in its (key, scope) partition and gets
	// rn = 1 whether or not the guard is there. With two, dropping the guard
	// makes PARTITION BY (key, scope) span both users and delete the older —
	// verified on PG16, where three users' rows collapse to one. Real installs
	// share keys across users exactly like this (user.preferences,
	// dashboard.layout, dashboard.presets).
	perUser := []struct {
		scopeID pgtype.UUID
		seed    string
		stored  string
	}{
		{userScopeID, `{"theme":"dark"}`, `{"theme": "dark"}`},
		{userScopeID2, `{"theme":"light"}`, `{"theme": "light"}`},
	}
	for _, u := range perUser {
		if _, err := pool.Exec(ctx, upsertSettingSQL, userKey, u.seed, "user", u.scopeID); err != nil {
			t.Fatalf("seed per-user setting %v: %v", u.scopeID, err)
		}
	}
	if n := countRows(userKey); n != len(perUser) {
		t.Fatalf("per-user upserts at 74: got %d row(s), want %d (distinct non-NULL scope_ids must not collide)",
			n, len(perUser))
	}

	// Step 2b: with duplicates present, the generated GetSetting must resolve to
	// the NEWEST of them. This locks the `ORDER BY updated_at DESC` added to
	// queries/settings.sql — without it the planner is free to return the oldest
	// (stale) heap tuple, which is how a corrected syslog host got resurrected.
	got, err := gen.New(pool).GetSetting(ctx, gen.GetSettingParams{
		Key:   globalKey,
		Scope: "global",
	})
	if err != nil {
		t.Fatalf("GetSetting over duplicates: %v", err)
	}
	if string(got.Value) != `{"logo": "newest"}` {
		t.Fatalf("GetSetting over duplicates returned %s, want the newest write "+
			"(is the ORDER BY missing from queries/settings.sql?)", got.Value)
	}

	// Step 2c: the tiebreak. now() is the *transaction* timestamp, so duplicates
	// written in one transaction share updated_at exactly and ordering by it
	// alone collapses back to heap order — verified against PG16, where the
	// unordered and updated_at-only queries both returned the oldest row. id is
	// the PK, so appending it makes the ordering total; the dedup in 000075 and
	// GetSetting must agree on which row that leaves.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tie seed txn: %v", err)
	}
	for _, row := range []struct {
		id    uuid.UUID
		value string
	}{{tieLowID, `{"v":"low-id"}`}, {tieHighID, `{"v":"high-id"}`}} {
		if _, err := tx.Exec(ctx,
			`INSERT INTO settings (id, key, value, scope, scope_id) VALUES ($1, $2, $3, 'global', NULL)`,
			row.id, tieKey, row.value); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("seed tie row %v: %v", row.id, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tie seed txn: %v", err)
	}
	var sameTimestamp bool
	if err := pool.QueryRow(ctx,
		`SELECT count(DISTINCT updated_at) = 1 FROM settings WHERE key = $1`, tieKey).Scan(&sameTimestamp); err != nil {
		t.Fatalf("probe tie timestamps: %v", err)
	}
	if !sameTimestamp {
		t.Fatalf("tie seed rows did not share updated_at — the tiebreak case is not being exercised")
	}
	tieGot, err := gen.New(pool).GetSetting(ctx, gen.GetSettingParams{Key: tieKey, Scope: "global"})
	if err != nil {
		t.Fatalf("GetSetting over tied duplicates: %v", err)
	}
	if tieGot.ID != tieHighID {
		t.Fatalf("GetSetting over tied duplicates returned id %v, want %v "+
			"(ordering is not total — add the id tiebreak back to queries/settings.sql)", tieGot.ID, tieHighID)
	}

	// Step 3: migrate up to 75 — dedup runs, constraint tightens.
	if err := m.Migrate(75); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate up to 75: %v", err)
	}
	if def := uniqueConstraintDef(); def != "UNIQUE NULLS NOT DISTINCT (key, scope, scope_id)" {
		t.Fatalf("constraint after 075: got %q, want UNIQUE NULLS NOT DISTINCT", def)
	}

	assertSingle := func(key, wantValue string) {
		t.Helper()
		if n := countRows(key); n != 1 {
			t.Fatalf("%s after 075: got %d row(s), want 1 (dedup did not collapse the group)", key, n)
		}
		var value string
		if err := pool.QueryRow(ctx, `SELECT value::text FROM settings WHERE key = $1`, key).Scan(&value); err != nil {
			t.Fatalf("read %s after 075: %v", key, err)
		}
		if value != wantValue {
			t.Fatalf("%s after 075: kept %s, want %s (dedup kept the stale row, not the newest)",
				key, value, wantValue)
		}
	}
	assertSingle(globalKey, `{"logo": "newest"}`)
	assertSingle(clusterKey, `{"v": "current"}`)

	// The dedup must resolve the timestamp tie the same way GetSetting does.
	assertSingle(tieKey, `{"v": "high-id"}`)
	var keptTieID uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM settings WHERE key = $1`, tieKey).Scan(&keptTieID); err != nil {
		t.Fatalf("read tie survivor after 075: %v", err)
	}
	if keptTieID != tieHighID {
		t.Fatalf("dedup kept tie row %v, want %v — 000075 and GetSetting disagree on the tiebreak",
			keptTieID, tieHighID)
	}

	// Every per-user row must be untouched — none was part of a duplicate group,
	// and the dedup's scope_id guard must keep different users independent.
	if n := countRows(userKey); n != len(perUser) {
		t.Fatalf("per-user rows after 075: got %d, want %d — the dedup's `WHERE scope_id IS NULL` guard "+
			"is missing, so PARTITION BY (key, scope) collapsed different users' rows", n, len(perUser))
	}
	for _, u := range perUser {
		var userValue string
		if err := pool.QueryRow(ctx,
			`SELECT value::text FROM settings WHERE key = $1 AND scope_id = $2`,
			userKey, u.scopeID).Scan(&userValue); err != nil {
			t.Fatalf("read per-user setting %v after 075 (dedup ate a scoped row?): %v", u.scopeID, err)
		}
		if userValue != u.stored {
			t.Fatalf("per-user setting %v after 075: got %s, want %s", u.scopeID, userValue, u.stored)
		}
	}

	// Step 3b — THE REGRESSION LOCK. The same upsert that appended at 74 must
	// now update in place: one row, carrying the newest value.
	sharedUpsert(globalKey, `{"logo":"post-fix"}`, "global")
	if n := countRows(globalKey); n != 1 {
		t.Fatalf("shared-scope upsert after 075 appended again (%d rows) — "+
			"ON CONFLICT still fails to infer the NULL scope_id row", n)
	}
	assertSingle(globalKey, `{"logo": "post-fix"}`)

	// Step 4: migrate back down to 74. The plain UNIQUE returns and the
	// surviving rows are preserved — a rollback loses no live settings.
	if err := m.Migrate(74); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrate down to 74: %v", err)
	}
	if def := uniqueConstraintDef(); def != "UNIQUE (key, scope, scope_id)" {
		t.Fatalf("constraint after down to 74: got %q, want the plain UNIQUE restored", def)
	}
	assertSingle(globalKey, `{"logo": "post-fix"}`)
	assertSingle(clusterKey, `{"v": "current"}`)
	if n := countRows(userKey); n != len(perUser) {
		t.Fatalf("per-user settings after down to 74: got %d row(s), want %d", n, len(perUser))
	}
}
