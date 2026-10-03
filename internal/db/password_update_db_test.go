package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// The SQL half of the change-password compare-and-swap (queries/users.sql,
// UpdatePassword): what it does against Postgres, including the interleaving of
// two requests that verified the same old password, driven with a real row lock on
// separate connections rather than described. The Go half — how a row count
// becomes a 409, and what the handler does around it — is pinned without a
// database in internal/api/handlers/auth_change_password_test.go, against a
// stand-in that implements the same predicate; these tests are what keep that
// stand-in honest.
//
// They skip without NEXARA_TEST_DB_URL like every test here that needs a database,
// and live in this package for the reason given in session_rotation_db_test.go.

// A fixed id so a run that aborts before its purge leaves a row the next run's
// up-front purge can find.
var passwordCASUser = uuid.MustParse("a4000000-0000-4000-8000-000000000001")

const (
	passwordCASOld = "hash-of-the-old-password"
	passwordCASNew = "hash-of-the-new-password"
)

// newPasswordCAS migrates to head, clears and seeds the one user the tests below
// key on, and returns the generated queries and a purge for the caller to defer.
// The purge must be deferred AFTER env.Cleanup — see setupMigration — and carries
// its own context so it survives that ordering shifting.
func newPasswordCAS(t *testing.T, env *migrationTestEnv) (*gen.Queries, func()) {
	t.Helper()

	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = env.Pool.Exec(pctx, `DELETE FROM users WHERE id = $1`, passwordCASUser)
	}
	purge()

	if _, err := env.Pool.Exec(env.Ctx,
		`INSERT INTO users (id, email, password_hash, display_name)
		 VALUES ($1, 'password-cas@example.com', $2, 'Password CAS')`, passwordCASUser, passwordCASOld); err != nil {
		purge()
		t.Fatalf("seed user: %v", err)
	}
	return gen.New(env.Pool), purge
}

func storedPasswordHash(t *testing.T, env *migrationTestEnv) string {
	t.Helper()
	var hash string
	if err := env.Pool.QueryRow(env.Ctx, `SELECT password_hash FROM users WHERE id = $1`, passwordCASUser).Scan(&hash); err != nil {
		t.Fatalf("read the stored hash: %v", err)
	}
	return hash
}

func resetPasswordHash(t *testing.T, env *migrationTestEnv) {
	t.Helper()
	if _, err := env.Pool.Exec(env.Ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, passwordCASUser, passwordCASOld); err != nil {
		t.Fatalf("reset the stored hash: %v", err)
	}
}

// TestUpdatePassword_IsACompareAndSwapAgainstPostgres runs the update with the
// hash it is conditional on, with another, with an empty one and for a user that
// does not exist, and reads back both the row count the handler turns into its
// answer and what the account holds. A plain overwrite answers 1 row and changes
// the hash in every row of the table below; only the first may.
func TestUpdatePassword_IsACompareAndSwapAgainstPostgres(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	q, purge := newPasswordCAS(t, env)
	defer purge()

	tests := []struct {
		name     string
		user     uuid.UUID
		expected string
		wantRows int64
		wantHash string // what the account holds afterwards
	}{
		{"the expected hash is the stored one", passwordCASUser, passwordCASOld, 1, passwordCASNew},
		{"the expected hash is not the stored one", passwordCASUser, "hash-of-some-other-password", 0, passwordCASOld},
		{"the expected hash is empty", passwordCASUser, "", 0, passwordCASOld},
		{"the user does not exist", uuid.New(), passwordCASOld, 0, passwordCASOld},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetPasswordHash(t, env)

			rows, err := q.UpdatePassword(env.Ctx, gen.UpdatePasswordParams{
				ID: tt.user, PasswordHash: passwordCASNew, ExpectedHash: tt.expected,
			})
			if err != nil {
				t.Fatalf("UpdatePassword: %v", err)
			}
			if rows != tt.wantRows {
				t.Errorf("rows = %d, want %d", rows, tt.wantRows)
			}
			if got := storedPasswordHash(t, env); got != tt.wantHash {
				t.Errorf("the account holds %q, want %q", got, tt.wantHash)
			}
		})
	}

	t.Run("two changes that verified the same old password: the second matches nothing", func(t *testing.T) {
		resetPasswordHash(t, env)

		first, err := q.UpdatePassword(env.Ctx, gen.UpdatePasswordParams{ID: passwordCASUser, PasswordHash: "hash-set-by-the-first", ExpectedHash: passwordCASOld})
		if err != nil || first != 1 {
			t.Fatalf("the first change: rows=%d err=%v, want 1 row", first, err)
		}
		second, err := q.UpdatePassword(env.Ctx, gen.UpdatePasswordParams{ID: passwordCASUser, PasswordHash: "hash-set-by-the-second", ExpectedHash: passwordCASOld})
		if err != nil {
			t.Fatalf("the second change: %v", err)
		}
		if second != 0 {
			t.Errorf("the second change matched %d rows: it overwrote the first", second)
		}
		if got := storedPasswordHash(t, env); got != "hash-set-by-the-first" {
			t.Errorf("the account holds %q, want the first change's hash", got)
		}
	})
}

// waitForBlockedOnUsers waits until Postgres reports a statement on this database,
// whose text matches like, stuck waiting for a lock on users — the only evidence
// that the interleaving a test set up is the one it is about to assert on.
func waitForBlockedOnUsers(t *testing.T, env *migrationTestEnv, like string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := env.Pool.QueryRow(env.Ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock'
			    AND query LIKE $1`, like).Scan(&n); err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the second statement never blocked on the row lock, so the interleaving under test did not happen")
}

// TestUpdatePassword_ARacingChangeWaitsForTheRowLockAndThenMatchesNothing is the
// interleaving the compare-and-swap exists for. Two requests verified the same old
// password. The first updates it inside a transaction that stays open, so it holds
// the row's lock; the second's update blocks on that lock — Postgres says so — and
// when the first commits, the second re-evaluates its WHERE against the row the
// first wrote (READ COMMITTED) and matches nothing: the first change stands. The
// control is the same pair with the first transaction rolled back, where the
// second goes through: it is the wait on a change that landed, not the harness,
// that produces the 0.
func TestUpdatePassword_ARacingChangeWaitsForTheRowLockAndThenMatchesNothing(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	q, purge := newPasswordCAS(t, env)
	defer purge()

	for _, tt := range []struct {
		name         string
		commitFirst  bool
		wantSecond   int64
		wantStoredBy string
	}{
		{"the first change commits: the second matches nothing", true, 0, "hash-set-by-the-first"},
		{"control, the first change rolls back: the second goes through", false, 1, "hash-set-by-the-second"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetPasswordHash(t, env)

			tx, err := env.Pool.Begin(env.Ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			finished := false
			defer func() {
				// Never leave the row locked if the test dies between the two halves.
				if !finished {
					rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_ = tx.Rollback(rbCtx)
				}
			}()

			rows, err := q.WithTx(tx).UpdatePassword(env.Ctx, gen.UpdatePasswordParams{
				ID: passwordCASUser, PasswordHash: "hash-set-by-the-first", ExpectedHash: passwordCASOld,
			})
			if err != nil || rows != 1 {
				t.Fatalf("the first change: rows=%d err=%v, want 1 row", rows, err)
			}

			type result struct {
				rows int64
				err  error
			}
			done := make(chan result, 1)
			go func() {
				r, err := q.UpdatePassword(env.Ctx, gen.UpdatePasswordParams{
					ID: passwordCASUser, PasswordHash: "hash-set-by-the-second", ExpectedHash: passwordCASOld,
				})
				done <- result{r, err}
			}()

			waitForBlockedOnUsers(t, env, "%UPDATE users%")
			select {
			case r := <-done:
				t.Fatalf("the second change finished (%d rows, %v) while the first transaction still held the row lock", r.rows, r.err)
			default:
			}

			if tt.commitFirst {
				err = tx.Commit(env.Ctx)
			} else {
				err = tx.Rollback(env.Ctx)
			}
			if err != nil {
				t.Fatalf("ending the first transaction: %v", err)
			}
			finished = true

			select {
			case r := <-done:
				if r.err != nil {
					t.Fatalf("the second change: %v", r.err)
				}
				if r.rows != tt.wantSecond {
					t.Errorf("the second change matched %d rows, want %d", r.rows, tt.wantSecond)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("the second change never finished after the lock was released")
			}
			if got := storedPasswordHash(t, env); got != tt.wantStoredBy {
				t.Errorf("the account holds %q, want %q", got, tt.wantStoredBy)
			}
		})
	}
}

// TestGetPasswordHashForSettle_WaitsForATransactionThatIsStillRunning is the
// interleaving the settle read exists for. A change-password COMMIT went out and its
// answer was lost; the server finishes a commit it has received even when its
// client has gone, and until the commit is done every other session still sees the
// old row. The change-password transaction is A: it has run the compare-and-swap
// update and has not ended — which, to everyone else, is exactly what a COMMIT the
// server is still executing looks like.
//
// A plain read on another connection answers at once with the OLD hash. That is the
// false negative: asked "did it land?", it says no about a change that is about to.
// The locking read does not answer: Postgres reports it waiting on A's row lock. When
// A commits it returns the NEW hash — under READ COMMITTED a locking read that
// waited for a writer returns the newest committed version — and the control, A
// rolling back, returns the OLD hash, which is then a true negative.
func TestGetPasswordHashForSettle_WaitsForATransactionThatIsStillRunning(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	q, purge := newPasswordCAS(t, env)
	defer purge()

	for _, tt := range []struct {
		name   string
		commit bool
		want   string
	}{
		{"the transaction commits: the locking read returns the new hash", true, passwordCASNew},
		{"control, the transaction rolls back: the locking read returns the old hash", false, passwordCASOld},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resetPasswordHash(t, env)

			tx, err := env.Pool.Begin(env.Ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			finished := false
			defer func() {
				// Never leave the row locked if the test dies between the two halves.
				if !finished {
					rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					_ = tx.Rollback(rbCtx)
				}
			}()
			rows, err := q.WithTx(tx).UpdatePassword(env.Ctx, gen.UpdatePasswordParams{
				ID: passwordCASUser, PasswordHash: passwordCASNew, ExpectedHash: passwordCASOld,
			})
			if err != nil || rows != 1 {
				t.Fatalf("the update inside the transaction: rows=%d err=%v, want 1 row", rows, err)
			}

			// The false negative: a plain read answers at once, with the old hash.
			plainCtx, plainCancel := context.WithTimeout(env.Ctx, 5*time.Second)
			defer plainCancel()
			var plain string
			if err := env.Pool.QueryRow(plainCtx, `SELECT password_hash FROM users WHERE id = $1`, passwordCASUser).Scan(&plain); err != nil {
				t.Fatalf("the plain read: %v (it must not wait for the transaction)", err)
			}
			if plain != passwordCASOld {
				t.Fatalf("the plain read returned %q, want the old hash %q: this is the answer the settle read must not rely on", plain, passwordCASOld)
			}

			// The locking read waits.
			type result struct {
				hash string
				err  error
			}
			done := make(chan result, 1)
			go func() {
				h, err := q.GetPasswordHashForSettle(env.Ctx, passwordCASUser)
				done <- result{h, err}
			}()
			waitForBlockedOnUsers(t, env, "%FOR SHARE%")
			select {
			case r := <-done:
				t.Fatalf("the locking read finished (%q, %v) while the transaction was still running", r.hash, r.err)
			default:
			}

			if tt.commit {
				err = tx.Commit(env.Ctx)
			} else {
				err = tx.Rollback(env.Ctx)
			}
			if err != nil {
				t.Fatalf("ending the transaction: %v", err)
			}
			finished = true

			select {
			case r := <-done:
				if r.err != nil {
					t.Fatalf("the locking read: %v", r.err)
				}
				if r.hash != tt.want {
					t.Errorf("the locking read returned %q, want %q", r.hash, tt.want)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("the locking read never finished after the transaction ended")
			}
		})
	}

	t.Run("an account that does not exist is no rows", func(t *testing.T) {
		_, err := q.GetPasswordHashForSettle(env.Ctx, uuid.New())
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Errorf("err = %v, want pgx.ErrNoRows: the handler tells a removed account from a failed read by it", err)
		}
	})

	t.Run("with nothing running it answers at once with what is stored", func(t *testing.T) {
		resetPasswordHash(t, env)
		ctx, cancel := context.WithTimeout(env.Ctx, 5*time.Second)
		defer cancel()
		got, err := q.GetPasswordHashForSettle(ctx, passwordCASUser)
		if err != nil || got != passwordCASOld {
			t.Errorf("got (%q, %v), want (%q, nil)", got, err, passwordCASOld)
		}
	})
}

// TestAnUnsentCommitIsSafeToRetryAndTheServerRollsBack pins two facts about the
// driver that commitOutcomeUnknown (internal/api/handlers/db_errors.go) is built
// on, and that nothing else here would notice changing.
//
// A COMMIT whose context had ended before it was sent is refused by pgconn without
// sending anything, and says so — pgconn.SafeToRetry — and the server then rolls
// the transaction back (pgx closes the connection of a transaction it could not
// commit): the write made inside it is gone and its row lock is released. So "the
// COMMIT never went out" is a known outcome, not an unknown one. A statement cut
// off while it ran is the opposite: it was sent, and pgconn does not say it is safe
// to retry — which is why a deadline error alone is not a known outcome.
func TestAnUnsentCommitIsSafeToRetryAndTheServerRollsBack(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	q, purge := newPasswordCAS(t, env)
	defer purge()

	tx, err := env.Pool.Begin(env.Ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	finished := false
	defer func() {
		if !finished {
			rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = tx.Rollback(rbCtx)
		}
	}()
	rows, err := q.WithTx(tx).UpdatePassword(env.Ctx, gen.UpdatePasswordParams{
		ID: passwordCASUser, PasswordHash: passwordCASNew, ExpectedHash: passwordCASOld,
	})
	if err != nil || rows != 1 {
		t.Fatalf("the write inside the transaction: rows=%d err=%v, want 1 row", rows, err)
	}

	ended, cancel := context.WithCancel(env.Ctx)
	cancel()
	err = tx.Commit(ended)
	finished = true
	if err == nil {
		t.Fatal("a COMMIT on a context that had ended succeeded")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the COMMIT's error = %v, want it to wrap the context's cancellation", err)
	}
	if !pgconn.SafeToRetry(err) {
		t.Errorf("pgconn.SafeToRetry(%v) = false: an unsent COMMIT would be reported as one whose outcome is unknown", err)
	}

	// The server rolled the transaction back: the old hash is still stored, and the
	// row's lock is gone, so a conditional update from another connection matches it
	// at once instead of waiting for a transaction that is still open.
	updateCtx, updateCancel := context.WithTimeout(env.Ctx, 15*time.Second)
	defer updateCancel()
	again, err := q.UpdatePassword(updateCtx, gen.UpdatePasswordParams{
		ID: passwordCASUser, PasswordHash: "hash-set-afterwards", ExpectedHash: passwordCASOld,
	})
	if err != nil {
		t.Fatalf("an update after the unsent COMMIT: %v (the first transaction still holds the row?)", err)
	}
	if again != 1 {
		t.Errorf("an update conditional on the old hash matched %d rows: the unsent COMMIT's write was not rolled back", again)
	}

	// The counterpart: a statement cut off while it ran was sent, and is not safe
	// to retry.
	slowCtx, slowCancel := context.WithTimeout(env.Ctx, 150*time.Millisecond)
	defer slowCancel()
	_, err = env.Pool.Exec(slowCtx, `SELECT pg_sleep(10)`)
	if err == nil {
		t.Fatal("a ten second statement finished inside 150 ms")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("the cut-off statement's error = %v, want it to wrap the deadline", err)
	}
	if pgconn.SafeToRetry(err) {
		t.Errorf("pgconn.SafeToRetry(%v) = true for a statement that was cut off while it ran: a COMMIT cut off the same way would be reported as rolled back", err)
	}
}
