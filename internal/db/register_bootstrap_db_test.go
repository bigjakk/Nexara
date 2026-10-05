package db

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/auth"
	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// The first registration decides who is the administrator, and — since an
// administrator's registration stopped signing the new account in — who gets a
// session out of it. Two concurrent registrations on a fresh install must not both
// be told they are the first. What serialises them is the advisory lock Register
// takes before it counts the users (internal/api/handlers/auth.go); what makes the
// count that follows the lock see the registration that held it is the isolation
// level, and these tests run the transaction as the handler runs it against Postgres
// to show both halves.
//
// Register itself cannot run here: package handlers' tests have no database, for the
// reason internal/api/handlers/auth_refresh_race_test.go gives. So these REPLAY its
// transaction in test code (registerFirst), and they are the proof of the Postgres
// semantics — what the lock and the isolation level do — and not of the handler. The
// handler's own tests pin what production Register does: that the level is asked for
// by name (TestRegister_TheTransactionIsPinnedToReadCommitted) and that its first
// statement is the advisory lock, before the count
// (TestRegister_TheTransactionTakesTheAdvisoryLockBeforeItCounts). Neither half
// substitutes for the other: a Register that dropped the lock would pass every test
// here, and one that kept it at the wrong level would pass every test there.

// registerLockKey is handlers.firstUserAdvisoryLockKey, whose value that package's
// TestRegister_FirstUserAdvisoryLockKeyIsStable pins.
const registerLockKey int64 = 0x4E455841524131

// registerOutcome is what one registration decided: the count it read once it held
// the lock, and the account it created — nil when it saw users and so was not the
// first, which Register answers with a 403 for the anonymous caller it models here.
type registerOutcome struct {
	count   int64
	created *gen.User
}

// registerFirst is Register's transaction up to its COMMIT, on tx: the advisory lock,
// the count, and — only when the count is zero — the administrator it creates.
func registerFirst(ctx context.Context, tx pgx.Tx, email string) (registerOutcome, error) {
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", registerLockKey); err != nil {
		return registerOutcome{}, fmt.Errorf("the advisory lock: %w", err)
	}
	q := gen.New(tx)
	count, err := q.CountUsers(ctx)
	if err != nil {
		return registerOutcome{}, fmt.Errorf("CountUsers: %w", err)
	}
	if count != 0 {
		return registerOutcome{count: count}, nil
	}
	u, err := q.CreateUser(ctx, gen.CreateUserParams{
		Email: email, PasswordHash: "x", DisplayName: email, IsActive: true, Role: "admin",
	})
	if err != nil {
		return registerOutcome{}, fmt.Errorf("CreateUser: %w", err)
	}
	return registerOutcome{count: count, created: &u}, nil
}

// registerAt runs the whole transaction at iso — begin, registerFirst, commit — and
// rolls back when anything fails.
func registerAt(ctx context.Context, pool interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}, iso pgx.TxIsoLevel, email string) (registerOutcome, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: iso})
	if err != nil {
		return registerOutcome{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	out, err := registerFirst(ctx, tx, email)
	if err != nil {
		return registerOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return registerOutcome{}, err
	}
	return out, nil
}

// clearUsers deletes every account but the system actor: a fresh install, which is
// the state a first registration starts from. This package's tests own their
// database (the migration chain test migrates it down to zero), so nothing here can
// be somebody else's data. (A table whose foreign key to users is NO ACTION would make
// the DELETE fail, loudly, instead of cascading: the failure is the notice to clear
// that table's rows too.)
func clearUsers(t *testing.T, env *migrationTestEnv) {
	t.Helper()
	cctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := env.Pool.Exec(cctx, `DELETE FROM users WHERE id <> $1`, auth.SystemUserID); err != nil {
		t.Fatalf("clear the users: %v", err)
	}
}

func adminCount(t *testing.T, env *migrationTestEnv) int {
	t.Helper()
	var n int
	if err := env.Pool.QueryRow(env.Ctx, `SELECT count(*) FROM users WHERE id <> $1 AND role = 'admin'`, auth.SystemUserID).Scan(&n); err != nil {
		t.Fatalf("count the administrators: %v", err)
	}
	return n
}

// TestRegister_TwoConcurrentFirstRegistrations puts two first registrations on the
// advisory lock in a fixed order — the first holds it with its administrator created
// and uncommitted, the second blocks on it — and lets the first commit. The second
// then counts, and what it reads is the point:
//
//   - at READ COMMITTED, the level Register asks for, its CountUsers takes a fresh
//     snapshot after the lock was granted and sees the first administrator: it is not
//     the first, creates nothing, and is the 403. One administrator exists, and the
//     session the first registration is signed in with is the only one.
//   - at REPEATABLE READ the same two transactions end with TWO administrators: the
//     snapshot is taken by the lock request itself, before it blocks, so the count
//     that follows reads the table as it was before the first committed. This is the
//     failure the pin exists for, and it is shown here so that it is known, not
//     assumed: if it ever stops failing, Postgres changed what a snapshot sees and the
//     reasoning in Register can be revisited.
func TestRegister_TwoConcurrentFirstRegistrations(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	clearUsers(t, env)
	defer clearUsers(t, env)
	q := gen.New(env.Pool)

	// run is the two registrations in the fixed order, the second at iso. It returns
	// what each decided, and the first's session when it got one.
	run := func(t *testing.T, iso pgx.TxIsoLevel) (first, second registerOutcome) {
		t.Helper()
		clearUsers(t, env)

		tx, err := env.Pool.BeginTx(env.Ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
		if err != nil {
			t.Fatalf("begin the first registration: %v", err)
		}
		finished := false
		defer func() {
			if !finished {
				_ = tx.Rollback(context.Background())
			}
		}()
		first, err = registerFirst(env.Ctx, tx, "first-registrant@example.com")
		if err != nil {
			t.Fatalf("the first registration: %v", err)
		}
		if first.created == nil || first.count != 0 {
			t.Fatalf("the first registration read %d users and created %v, want 0 and an administrator", first.count, first.created)
		}

		type result struct {
			out registerOutcome
			err error
		}
		done := make(chan result, 1)
		go func() {
			out, err := registerAt(env.Ctx, env.Pool, iso, "second-registrant@example.com")
			done <- result{out, err}
		}()

		// The second is on the lock when Postgres says a statement of its kind is
		// waiting for one. If it finishes first the two did not contend.
		deadline := time.Now().Add(15 * time.Second)
		blocked := false
		for !blocked && time.Now().Before(deadline) {
			if len(done) > 0 {
				r := <-done
				t.Fatalf("the second registration finished (%+v, %v) while the first still held the advisory lock: the two did not contend", r.out, r.err)
			}
			var n int
			if err := env.Pool.QueryRow(env.Ctx,
				`SELECT count(*) FROM pg_stat_activity
				  WHERE datname = current_database()
				    AND wait_event_type = 'Lock'
				    AND query LIKE '%pg_advisory_xact_lock%'`).Scan(&n); err != nil {
				t.Fatalf("read pg_stat_activity: %v", err)
			}
			blocked = n > 0
			if !blocked {
				time.Sleep(20 * time.Millisecond)
			}
		}
		if !blocked {
			t.Fatal("the second registration never blocked on the advisory lock, so the interleaving under test did not happen")
		}

		if err := tx.Commit(env.Ctx); err != nil {
			t.Fatalf("commit the first registration: %v", err)
		}
		finished = true

		select {
		case r := <-done:
			if r.err != nil {
				t.Fatalf("the second registration: %v", r.err)
			}
			second = r.out
		case <-time.After(15 * time.Second):
			t.Fatal("the second registration never finished after the lock was released")
		}
		return first, second
	}

	t.Run("at READ COMMITTED the second is not the first", func(t *testing.T) {
		first, second := run(t, pgx.ReadCommitted)

		if second.count != 1 || second.created != nil {
			t.Errorf("the second registration read %d users and created %v, want 1 and nothing: it must see the first administrator", second.count, second.created)
		}
		if n := adminCount(t, env); n != 1 {
			t.Errorf("%d administrators exist, want exactly 1", n)
		}

		// The first registration is signed in; the second is not.
		session, err := q.CreateSessionAtEpoch(env.Ctx, epochParams(first.created.ID, first.created.AuthEpoch, "first-registrant-token"))
		if err != nil {
			t.Fatalf("the first registration's session: %v", err)
		}
		var sessions int
		if err := env.Pool.QueryRow(env.Ctx,
			`SELECT count(*) FROM sessions s JOIN users u ON u.id = s.user_id WHERE u.id <> $1`, auth.SystemUserID).Scan(&sessions); err != nil {
			t.Fatalf("count the sessions: %v", err)
		}
		if sessions != 1 || session.UserID != first.created.ID {
			t.Errorf("%d sessions exist (the new one for %v), want exactly 1, the first registration's", sessions, session.UserID)
		}
	})

	t.Run("at REPEATABLE READ both are the first", func(t *testing.T) {
		_, second := run(t, pgx.RepeatableRead)

		if second.count != 0 || second.created == nil {
			t.Fatalf("the second registration read %d users and created %v: at REPEATABLE READ it was expected to read the table "+
				"as it was before the first committed, and so to be told it is the first too", second.count, second.created)
		}
		if n := adminCount(t, env); n != 2 {
			t.Errorf("%d administrators exist, want 2: the failure the READ COMMITTED pin prevents", n)
		}
	})
}

// TestRegister_ManyConcurrentFirstRegistrationsMakeOneAdministrator is the same
// guarantee without a chosen order: several registrations on a fresh install start
// together at READ COMMITTED, and however Postgres interleaves them exactly one is
// the first. Which one it is is not defined; that there is one is.
//
// Every registrant has a connection of its own, opened BEFORE the start barrier. A
// pool opens its connections lazily, and the first registrant to get one would finish
// its whole transaction before the others had even connected: the run would pass with
// the lock removed (it did, twelve times in twelve). With the connections ready the
// registrants meet at the lock, and what the test measures is the lock — removed from
// the replay, or run at REPEATABLE READ, it fails every time.
func TestRegister_ManyConcurrentFirstRegistrationsMakeOneAdministrator(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	migrateUp(t, env.Migrate)
	clearUsers(t, env)
	defer clearUsers(t, env)

	const registrants = 6
	url := testDBURL(t)
	conns := make([]*pgx.Conn, registrants)
	for i := range conns {
		c, err := pgx.Connect(env.Ctx, url)
		if err != nil {
			t.Fatalf("connect registrant %d: %v", i, err)
		}
		defer func() { _ = c.Close(context.Background()) }()
		conns[i] = c
	}

	start := make(chan struct{})
	outcomes := make([]registerOutcome, registrants)
	errs := make([]error, registrants)
	var wg sync.WaitGroup
	for i := range registrants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			outcomes[i], errs[i] = registerAt(env.Ctx, conns[i], pgx.ReadCommitted,
				fmt.Sprintf("registrant-%d-%s@example.com", i, uuid.NewString()[:8]))
		}()
	}
	close(start)
	wg.Wait()

	firsts := 0
	for i, out := range outcomes {
		if errs[i] != nil {
			t.Fatalf("registrant %d: %v", i, errs[i])
		}
		if out.created != nil {
			firsts++
		}
	}
	if firsts != 1 {
		t.Errorf("%d of %d concurrent registrations were told they were the first, want exactly 1", firsts, registrants)
	}
	if n := adminCount(t, env); n != 1 {
		t.Errorf("%d administrators exist, want exactly 1", n)
	}
}
