package db

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// The SQL half of "no session from a credential check that a revoke-all has since
// invalidated" (migration 000106, queries/sessions.sql CreateSessionAtEpoch,
// auth.RevokeAllUserSessionsIn): what the conditional insert and the bump do
// against Postgres, including every interleaving of a sign-in's insert with a
// revoke-all, driven with real row locks on separate connections rather than
// described. The Go half — how a refused insert becomes a 401, which statements
// each handler sends — is pinned without a database in internal/auth and
// internal/api/handlers.
//
// They skip without NEXARA_TEST_DB_URL, like every test here that needs a
// database, and live in this package for the reason given in
// session_rotation_db_test.go.

// Fixed ids so a run that aborts before its purge leaves rows the next run's
// up-front purge can find. sessions cascades from users.
var (
	epochUserA = uuid.MustParse("a3060000-0000-4000-8000-000000000001")
	epochUserB = uuid.MustParse("a3060000-0000-4000-8000-000000000002")
)

// epochHarness is what every test below shares: the real generated queries and the
// real SessionManager (over miniredis, which it needs only to write and delete its
// Redis rows) against the migrated throwaway database, with two active users at
// epoch 0.
type epochHarness struct {
	env *migrationTestEnv
	q   *gen.Queries
	sm  *auth.SessionManager
	mr  *miniredis.Miniredis
}

// newEpochHarness migrates to head, clears and seeds the two users, and returns the
// harness and a purge for the caller to defer. The purge must be deferred AFTER
// env.Cleanup — see setupMigration — and carries its own context so it survives that
// ordering shifting.
func newEpochHarness(t *testing.T, env *migrationTestEnv) (*epochHarness, func()) {
	t.Helper()

	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = env.Pool.Exec(pctx, `DELETE FROM users WHERE id = ANY($1)`, []uuid.UUID{epochUserA, epochUserB})
	}
	purge()

	for id, email := range map[uuid.UUID]string{
		epochUserA: "session-epoch-a@example.com",
		epochUserB: "session-epoch-b@example.com",
	} {
		if _, err := env.Pool.Exec(env.Ctx,
			`INSERT INTO users (id, email, password_hash, display_name)
			 VALUES ($1, $2, 'x', 'Session Epoch')`, id, email); err != nil {
			purge()
			t.Fatalf("seed user %s: %v", email, err)
		}
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	q := gen.New(env.Pool)
	return &epochHarness{env: env, q: q, sm: auth.NewSessionManager(q, rdb), mr: mr}, purge
}

func (h *epochHarness) epoch(t *testing.T, user uuid.UUID) int64 {
	t.Helper()
	u, err := h.q.GetUserByID(h.env.Ctx, user)
	if err != nil {
		t.Fatalf("read the user: %v", err)
	}
	return u.AuthEpoch
}

// params is the insert a sign-in would make for user, whose credential check read
// epoch, with the refresh token token.
func epochParams(user uuid.UUID, epoch int64, token string) gen.CreateSessionAtEpochParams {
	return gen.CreateSessionAtEpochParams{
		UserID:    user,
		Epoch:     epoch,
		TokenHash: auth.HashToken(token),
		UserAgent: "Mozilla/5.0",
		IpAddress: "192.0.2.10",
		ExpiresAt: time.Now().Add(time.Hour),
		UserRole:  "admin",
	}
}

// epochDeactivate is the profile change a deactivation makes: is_active alone — the
// update is partial, and a deactivation writes nothing else.
func epochDeactivate(user uuid.UUID) gen.UpdateUserProfileParams {
	return gen.UpdateUserProfileParams{ID: user, IsActive: pgtype.Bool{Bool: false, Valid: true}}
}

func (h *epochHarness) create(user uuid.UUID, epoch int64, token string) (gen.Session, error) {
	return h.q.CreateSessionAtEpoch(h.env.Ctx, epochParams(user, epoch, token))
}

func (h *epochHarness) mustCreate(t *testing.T, user uuid.UUID, epoch int64, token string) gen.Session {
	t.Helper()
	s, err := h.create(user, epoch, token)
	if err != nil {
		t.Fatalf("create a session for %v at epoch %d: %v", user, epoch, err)
	}
	return s
}

func (h *epochHarness) exec(t *testing.T, what, sql string, args ...any) {
	t.Helper()
	if _, err := h.env.Pool.Exec(h.env.Ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// reset puts both users back to epoch 0 and active, with no sessions and no Redis
// rows, so a subtest starts from the state the harness was seeded in.
func (h *epochHarness) reset(t *testing.T) {
	t.Helper()
	h.exec(t, "clear sessions", `DELETE FROM sessions WHERE user_id = ANY($1)`, []uuid.UUID{epochUserA, epochUserB})
	h.exec(t, "reset users", `UPDATE users SET auth_epoch = 0, is_active = true WHERE id = ANY($1)`,
		[]uuid.UUID{epochUserA, epochUserB})
	h.mr.FlushAll()
}

// sessionsWithToken is how many session rows hold the hash of token. A refused
// insert must leave none.
func (h *epochHarness) sessionsWithToken(t *testing.T, token string) int {
	t.Helper()
	var n int
	if err := h.env.Pool.QueryRow(h.env.Ctx, `SELECT count(*) FROM sessions WHERE token_hash = $1`,
		auth.HashToken(token)).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func (h *epochHarness) revoked(t *testing.T, id uuid.UUID) bool {
	t.Helper()
	s, err := h.q.GetSessionByID(h.env.Ctx, id)
	if err != nil {
		t.Fatalf("reload session %v: %v", id, err)
	}
	return s.IsRevoked
}

// epochPair puts a session insert and a revoke-all on one user row in a chosen
// order, the way lockedPair does for a refresh: hold runs inside a transaction that
// stays open, so it holds the user row's lock; wait starts on another connection;
// Postgres is asked until it reports wait blocked on that lock (like names a
// fragment of its text); the holder is then committed or rolled back, and wait's
// answer returned. A wait that FINISHES while the holder is still open is a failure
// of the test's premise — the two did not contend — and is reported at once, not
// after the polling bound: it is what a statement that forgot its lock looks like.
func epochPair[T any](t *testing.T, h *epochHarness, like string, commit bool,
	hold func(q *gen.Queries) error, wait func(q *gen.Queries) (T, error)) (T, error) {
	t.Helper()

	tx, err := h.env.Pool.Begin(h.env.Ctx)
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

	if err := hold(h.q.WithTx(tx)); err != nil {
		t.Fatalf("the statement holding the lock: %v", err)
	}

	type result struct {
		val T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := wait(h.q)
		done <- result{v, err}
	}()

	deadline := time.Now().Add(15 * time.Second)
	blocked := false
	for !blocked && time.Now().Before(deadline) {
		select {
		case r := <-done:
			t.Fatalf("the second statement finished (%v, %v) while the first transaction still held the row lock: the two did not contend", r.val, r.err)
		default:
		}
		var n int
		if err := h.env.Pool.QueryRow(h.env.Ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock'
			    AND query LIKE $1`, like).Scan(&n); err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if n > 0 {
			blocked = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !blocked {
		t.Fatalf("the second statement (%s) never blocked on the row lock, so the interleaving under test did not happen", like)
	}

	if commit {
		err = tx.Commit(h.env.Ctx)
	} else {
		err = tx.Rollback(h.env.Ctx)
	}
	if err != nil {
		t.Fatalf("ending the first transaction: %v", err)
	}
	finished = true

	select {
	case r := <-done:
		return r.val, r.err
	case <-time.After(15 * time.Second):
		t.Fatal("the second statement never finished after the lock was released")
	}
	panic("unreachable")
}

// TestCreateSessionAtEpoch_OnlyAgainstTheEpochTheCheckSaw pins the conditions of
// the insert, one at a time. Each refusal row asserts two things: pgx.ErrNoRows —
// which is what SessionManager.CreateSession turns into ErrSessionRefused — and,
// the one that matters, that no session row was written.
//
// The first row is the positive control for the rest: the same harness does
// create, and the row it creates is the one that was asked for.
func TestCreateSessionAtEpoch_OnlyAgainstTheEpochTheCheckSaw(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	h, purge := newEpochHarness(t, env)
	defer purge()

	tests := []struct {
		name string
		// prepare changes the world after the user was read and before the insert.
		prepare func(t *testing.T)
		user    uuid.UUID
		epoch   int64
		want    bool // created
	}{
		{name: "the epoch the check read, an active user", user: epochUserA, epoch: 0, want: true},
		{
			name: "an epoch the user has since moved past",
			prepare: func(t *testing.T) {
				h.exec(t, "bump", `UPDATE users SET auth_epoch = auth_epoch + 1 WHERE id = $1`, epochUserA)
			},
			user: epochUserA, epoch: 0,
		},
		{name: "an epoch the user has never held", user: epochUserA, epoch: 1},
		{
			name: "an epoch the user has not reached yet, however far ahead",
			user: epochUserA, epoch: 1 << 40,
		},
		{
			name: "an account deactivated since the check, its epoch unchanged",
			prepare: func(t *testing.T) {
				h.exec(t, "deactivate", `UPDATE users SET is_active = false WHERE id = $1`, epochUserA)
			},
			user: epochUserA, epoch: 0,
		},
		{
			// The condition is on the user the session is FOR: another account's
			// matching epoch is no authority.
			name: "another account's epoch",
			prepare: func(t *testing.T) {
				h.exec(t, "bump B", `UPDATE users SET auth_epoch = 7 WHERE id = $1`, epochUserB)
			},
			user: epochUserA, epoch: 7,
		},
		{name: "an account that does not exist", user: uuid.New(), epoch: 0},
		{
			name: "the epoch of a user the session is for, after other users' epochs moved",
			prepare: func(t *testing.T) {
				h.exec(t, "bump B", `UPDATE users SET auth_epoch = 9 WHERE id = $1`, epochUserB)
			},
			user: epochUserA, epoch: 0, want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h.reset(t)
			if tt.prepare != nil {
				tt.prepare(t)
			}

			s, err := h.create(tt.user, tt.epoch, "epoch-token")
			if !tt.want {
				if !errors.Is(err, pgx.ErrNoRows) {
					t.Fatalf("CreateSessionAtEpoch(%v, epoch %d) = (%v, %v), want pgx.ErrNoRows", tt.user, tt.epoch, s.ID, err)
				}
				if n := h.sessionsWithToken(t, "epoch-token"); n != 0 {
					t.Errorf("%d session rows hold the refused token: a refusal must write nothing", n)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateSessionAtEpoch: %v", err)
			}
			if s.UserID != tt.user || s.TokenHash != auth.HashToken("epoch-token") || s.UserRole != "admin" ||
				s.UserAgent != "Mozilla/5.0" || s.IpAddress != "192.0.2.10" || s.IsRevoked {
				t.Errorf("the created session is %+v, not the one that was asked for", s)
			}
			if time.Until(s.ExpiresAt) < 50*time.Minute || time.Until(s.ExpiresAt) > 70*time.Minute {
				t.Errorf("expires_at = %v, want about an hour from now", s.ExpiresAt)
			}
			if s.DeviceName.Valid || s.DeviceType.Valid || s.DeviceID.Valid {
				t.Errorf("device fields = %+v %+v %+v, want NULL for a client that named none", s.DeviceName, s.DeviceType, s.DeviceID)
			}
			if n := h.sessionsWithToken(t, "epoch-token"); n != 1 {
				t.Errorf("%d session rows hold the token, want 1", n)
			}
		})
	}

	t.Run("the device fields are written when given", func(t *testing.T) {
		h.reset(t)
		p := epochParams(epochUserA, 0, "device-token")
		p.DeviceName = pgtype.Text{String: "Pixel 8", Valid: true}
		p.DeviceType = pgtype.Text{String: "mobile", Valid: true}
		p.DeviceID = pgtype.Text{String: "device-0001", Valid: true}
		s, err := h.q.CreateSessionAtEpoch(env.Ctx, p)
		if err != nil {
			t.Fatalf("CreateSessionAtEpoch: %v", err)
		}
		if s.DeviceName.String != "Pixel 8" || s.DeviceType.String != "mobile" || s.DeviceID.String != "device-0001" {
			t.Errorf("device fields = %+v %+v %+v", s.DeviceName, s.DeviceType, s.DeviceID)
		}
	})
}

// TestBumpUserAuthEpoch pins the bump: one more each time, only for the named
// user, a user that is not there is no rows and no error, and — the claim the
// migration's header makes — the users updated_at trigger moves with it.
func TestBumpUserAuthEpoch(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	h, purge := newEpochHarness(t, env)
	defer purge()
	h.reset(t)

	updatedAt := func(user uuid.UUID) time.Time {
		t.Helper()
		var at time.Time
		if err := env.Pool.QueryRow(env.Ctx, `SELECT updated_at FROM users WHERE id = $1`, user).Scan(&at); err != nil {
			t.Fatalf("read updated_at: %v", err)
		}
		return at
	}
	before := updatedAt(epochUserA)
	// updated_at is now(), the start of the statement's transaction, so a bump that
	// follows within a microsecond of the seed is not distinguishable from it.
	time.Sleep(20 * time.Millisecond)

	for want := int64(1); want <= 3; want++ {
		rows, err := h.q.BumpUserAuthEpoch(env.Ctx, epochUserA)
		if err != nil || rows != 1 {
			t.Fatalf("bump %d: rows=%d err=%v, want 1 row", want, rows, err)
		}
		if got := h.epoch(t, epochUserA); got != want {
			t.Fatalf("after %d bumps the epoch is %d", want, got)
		}
	}
	if got := h.epoch(t, epochUserB); got != 0 {
		t.Errorf("another user's epoch moved to %d", got)
	}
	if !updatedAt(epochUserA).After(before) {
		t.Error("updated_at did not move with the bump: the trigger the migration header describes is not firing")
	}

	rows, err := h.q.BumpUserAuthEpoch(env.Ctx, uuid.New())
	if err != nil || rows != 0 {
		t.Errorf("bump of a user that is not there: rows=%d err=%v, want 0 rows and no error", rows, err)
	}
}

// TestRevokeAllUserSessionsIn_EveryPathMovesTheEpochAndEndsTheSessions runs each
// way the application ends a user's sessions — through the production entry points,
// against Postgres — and requires the same three things of each: every live session
// is revoked, the epoch has moved by exactly one, and a sign-in whose check read the
// old epoch is refused through the manager (ErrSessionRefused) with nothing written
// and no Redis row, while one that reads the new epoch is not (a deactivation
// excepted: the account is no longer active).
//
// The three paths are the sign-out of all devices on the pool, a password change
// (the revoke inside the transaction of the update), and a deactivation (the revoke
// inside the transaction of the profile change). Each is the shape of its handler;
// that the handler uses it is pinned in internal/api/handlers.
func TestRevokeAllUserSessionsIn_EveryPathMovesTheEpochAndEndsTheSessions(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	h, purge := newEpochHarness(t, env)
	defer purge()

	inTx := func(t *testing.T, fn func(q *gen.Queries) error) {
		t.Helper()
		tx, err := env.Pool.Begin(env.Ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		done := false
		defer func() {
			if !done {
				_ = tx.Rollback(context.Background())
			}
		}()
		if err := fn(h.q.WithTx(tx)); err != nil {
			t.Fatal(err)
		}
		if err := tx.Commit(env.Ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
		done = true
	}

	paths := []struct {
		name        string
		run         func(t *testing.T)
		stillActive bool
	}{
		{
			name: "sign out of all devices, on the pool",
			run: func(t *testing.T) {
				ids, err := auth.RevokeAllUserSessionsIn(env.Ctx, h.q, epochUserA)
				if err != nil {
					t.Fatal(err)
				}
				h.sm.ForgetSessions(env.Ctx, ids)
			},
			stillActive: true,
		},
		{
			name: "sign out of all devices through the Queries a handler holds",
			run: func(t *testing.T) {
				if _, err := auth.RevokeAllUserSessionsIn(env.Ctx, h.q, epochUserA); err != nil {
					t.Fatal(err)
				}
			},
			stillActive: true,
		},
		{
			name: "a password change: the update and the revoke in one transaction",
			run: func(t *testing.T) {
				inTx(t, func(q *gen.Queries) error {
					rows, err := q.UpdatePassword(env.Ctx, gen.UpdatePasswordParams{
						ID: epochUserA, PasswordHash: "hash-of-the-new-password", ExpectedHash: "x"})
					if err != nil || rows != 1 {
						t.Fatalf("UpdatePassword: rows=%d err=%v", rows, err)
					}
					_, err = auth.RevokeAllUserSessionsIn(env.Ctx, q, epochUserA)
					return err
				})
			},
			stillActive: true,
		},
		{
			name: "a deactivation: the profile change and the revoke in one transaction",
			run: func(t *testing.T) {
				inTx(t, func(q *gen.Queries) error {
					if _, err := q.UpdateUserProfile(env.Ctx, epochDeactivate(epochUserA)); err != nil {
						return err
					}
					_, err := auth.RevokeAllUserSessionsIn(env.Ctx, q, epochUserA)
					return err
				})
			},
			stillActive: false,
		},
	}

	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			h.reset(t)
			h.exec(t, "reset the password", `UPDATE users SET password_hash = 'x' WHERE id = $1`, epochUserA)
			oldEpoch := h.epoch(t, epochUserA)
			live := []gen.Session{
				h.mustCreate(t, epochUserA, oldEpoch, "first-device"),
				h.mustCreate(t, epochUserA, oldEpoch, "second-device"),
			}
			other := h.mustCreate(t, epochUserB, 0, "another-users-device")

			p.run(t)

			for _, s := range live {
				if !h.revoked(t, s.ID) {
					t.Errorf("session %v is still live after the revoke-all", s.ID)
				}
			}
			if h.revoked(t, other.ID) {
				t.Error("another user's session was revoked")
			}
			if got := h.epoch(t, epochUserA); got != oldEpoch+1 {
				t.Errorf("the epoch is %d, want %d: a revoke-all must move it by exactly one", got, oldEpoch+1)
			}
			if got := h.epoch(t, epochUserB); got != 0 {
				t.Errorf("another user's epoch moved to %d", got)
			}

			// A sign-in whose check read the OLD epoch, through the manager.
			_, err := h.sm.CreateSession(env.Ctx, epochUserA, oldEpoch, "stale-sign-in", "admin", "Mozilla/5.0", "192.0.2.10", time.Hour, auth.DeviceInfo{})
			if !errors.Is(err, auth.ErrSessionRefused) {
				t.Fatalf("a sign-in that read epoch %d was answered %v, want ErrSessionRefused", oldEpoch, err)
			}
			if n := h.sessionsWithToken(t, "stale-sign-in"); n != 0 {
				t.Errorf("%d session rows hold the refused sign-in's token", n)
			}
			if keys := h.mr.Keys(); len(keys) != 0 {
				t.Errorf("a refused sign-in left Redis rows behind: %v", keys)
			}

			// One that reads the new epoch is a new sign-in: it succeeds unless the
			// account is no longer active.
			s, err := h.sm.CreateSession(env.Ctx, epochUserA, oldEpoch+1, "fresh-sign-in", "admin", "Mozilla/5.0", "192.0.2.10", time.Hour, auth.DeviceInfo{})
			if p.stillActive {
				if err != nil {
					t.Fatalf("a sign-in that read the new epoch: %v", err)
				}
				if h.revoked(t, s.ID) {
					t.Error("a sign-in after the revoke-all was created revoked")
				}
				if !h.mr.Exists("nexara:session:" + s.ID.String()) {
					t.Error("the created session has no Redis row")
				}
			} else if !errors.Is(err, auth.ErrSessionRefused) {
				t.Errorf("a sign-in for a deactivated account was answered %v, want ErrSessionRefused", err)
			}
		})
	}
}

// TestSessionInsertAgainstARevokeAll puts the insert and the revoke-all on the user
// row in each order, against Postgres, and requires every session to end in one of
// two states: refused (nothing written), or created and revoked. A session that is
// created and LIVE after a revoke-all that began later is the failure this whole
// change exists to prevent.
//
//	a. The revoke-all committed before the insert ran. The insert reads the new
//	   epoch: 0 rows.
//	b. The insert holds the row lock, in a transaction that has not committed; the
//	   revoke-all arrives and waits — its FIRST statement, the bump, is what waits;
//	   the insert commits. The session was created, the revoke-all then lists it and
//	   revokes it: it ends revoked, and is among the ids whose Redis rows are
//	   deleted. (Rolled back instead, nothing was created and the revoke-all simply
//	   goes through.)
//	c. The revoke-all holds the row lock (its bump has run, its transaction is
//	   open); the insert arrives and waits; the revoke-all commits. The insert
//	   re-evaluates its conditions against the row the revoke-all wrote — READ
//	   COMMITTED's re-check — and finds a different epoch: 0 rows. Rolled back
//	   instead, the epoch is unchanged and the insert goes ahead: nothing was
//	   revoked, so nothing is refused.
//	d. The same as c with the user DEACTIVATED, in a transaction with the revoke-all
//	   and — separately — with no revoke-all at all, which leaves the epoch alone and
//	   so shows that is_active refuses by itself.
//
// In b, c and d the second statement is shown to be blocked, by Postgres, before
// the lock is released — see epochPair.
func TestSessionInsertAgainstARevokeAll(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	h, purge := newEpochHarness(t, env)
	defer purge()

	const (
		blockedBump   = "%UPDATE users SET auth_epoch%"
		blockedInsert = "%INSERT INTO sessions%"
	)

	t.Run("a_revoke_all_committed_before_the_insert_ran", func(t *testing.T) {
		h.reset(t)
		old := h.mustCreate(t, epochUserA, 0, "existing-session")

		if _, err := auth.RevokeAllUserSessionsIn(env.Ctx, h.q, epochUserA); err != nil {
			t.Fatalf("revoke-all: %v", err)
		}
		if _, err := h.create(epochUserA, 0, "late-sign-in"); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("an insert on the epoch read before the revoke-all: %v, want pgx.ErrNoRows", err)
		}
		if n := h.sessionsWithToken(t, "late-sign-in"); n != 0 {
			t.Errorf("%d rows hold the refused sign-in's token", n)
		}
		if !h.revoked(t, old.ID) {
			t.Error("the existing session survived the revoke-all")
		}
		// And the control: the same insert against the epoch the revoke-all produced.
		if _, err := h.create(epochUserA, 1, "new-sign-in"); err != nil {
			t.Errorf("an insert on the new epoch: %v", err)
		}
	})

	t.Run("b_revoke_all_waits_for_an_insert_in_progress", func(t *testing.T) {
		for _, commit := range []bool{true, false} {
			name := "the insert commits: the session is created and then revoked"
			if !commit {
				name = "control, the insert rolls back: nothing was created"
			}
			t.Run(name, func(t *testing.T) {
				h.reset(t)
				var created gen.Session
				ids, err := epochPair(t, h, blockedBump, commit,
					func(q *gen.Queries) error {
						s, err := q.CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "racing-sign-in"))
						created = s
						return err
					},
					func(q *gen.Queries) ([]uuid.UUID, error) {
						return auth.RevokeAllUserSessionsIn(env.Ctx, q, epochUserA)
					})
				if err != nil {
					t.Fatalf("the revoke-all: %v", err)
				}
				if got := h.epoch(t, epochUserA); got != 1 {
					t.Errorf("the epoch is %d after the revoke-all, want 1", got)
				}
				if !commit {
					if n := h.sessionsWithToken(t, "racing-sign-in"); n != 0 {
						t.Errorf("%d rows hold a rolled-back insert's token", n)
					}
					if len(ids) != 0 {
						t.Errorf("the revoke-all listed %v with nothing created", ids)
					}
					return
				}
				if created.ID == uuid.Nil {
					t.Fatal("the insert holding the lock returned no session")
				}
				if !h.revoked(t, created.ID) {
					t.Fatal("the session created while the revoke-all waited is LIVE: a sign-in outlived the revoke-all that began after it")
				}
				if len(ids) != 1 || ids[0] != created.ID {
					t.Errorf("the revoke-all listed %v, want exactly the session the insert committed (%v): the listing ran before the insert was visible", ids, created.ID)
				}
			})
		}
	})

	t.Run("c_insert_waits_for_a_revoke_all_in_progress", func(t *testing.T) {
		t.Run("the revoke-all commits: the insert is refused", func(t *testing.T) {
			h.reset(t)
			old := h.mustCreate(t, epochUserA, 0, "existing-session")

			got, err := epochPair(t, h, blockedInsert, true,
				func(q *gen.Queries) error {
					_, err := auth.RevokeAllUserSessionsIn(env.Ctx, q, epochUserA)
					return err
				},
				func(q *gen.Queries) (gen.Session, error) {
					return q.CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "waiting-sign-in"))
				})
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("an insert that waited for a revoke-all: (%v, %v), want pgx.ErrNoRows", got.ID, err)
			}
			if n := h.sessionsWithToken(t, "waiting-sign-in"); n != 0 {
				t.Errorf("%d rows hold the refused sign-in's token", n)
			}
			if !h.revoked(t, old.ID) {
				t.Error("the existing session survived")
			}
		})
		t.Run("control, the revoke-all rolls back: the insert goes ahead", func(t *testing.T) {
			h.reset(t)
			old := h.mustCreate(t, epochUserA, 0, "existing-session")

			got, err := epochPair(t, h, blockedInsert, false,
				func(q *gen.Queries) error {
					_, err := auth.RevokeAllUserSessionsIn(env.Ctx, q, epochUserA)
					return err
				},
				func(q *gen.Queries) (gen.Session, error) {
					return q.CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "waiting-sign-in"))
				})
			if err != nil {
				t.Fatalf("an insert that waited for a revoke-all that rolled back: %v, want it to go ahead", err)
			}
			if h.revoked(t, got.ID) || h.revoked(t, old.ID) {
				t.Error("a rolled-back revoke-all left a session revoked")
			}
			if e := h.epoch(t, epochUserA); e != 0 {
				t.Errorf("the epoch is %d after a rollback, want 0", e)
			}
		})
	})

	t.Run("d_insert_waits_for_a_deactivation_in_progress", func(t *testing.T) {
		deactivate := func(q *gen.Queries, revokeAll bool) error {
			if _, err := q.UpdateUserProfile(env.Ctx, epochDeactivate(epochUserA)); err != nil {
				return err
			}
			if !revokeAll {
				return nil
			}
			_, err := auth.RevokeAllUserSessionsIn(env.Ctx, q, epochUserA)
			return err
		}
		for _, tt := range []struct {
			name      string
			revokeAll bool
			wantEpoch int64
		}{
			{"with the revoke-all, as the API does it", true, 1},
			{"without one — the epoch stays, so is_active alone refuses", false, 0},
		} {
			t.Run(tt.name+": the insert is refused", func(t *testing.T) {
				h.reset(t)

				got, err := epochPair(t, h, blockedInsert, true,
					func(q *gen.Queries) error { return deactivate(q, tt.revokeAll) },
					func(q *gen.Queries) (gen.Session, error) {
						return q.CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "deactivated-sign-in"))
					})
				if !errors.Is(err, pgx.ErrNoRows) {
					t.Fatalf("an insert that waited for a deactivation: (%v, %v), want pgx.ErrNoRows", got.ID, err)
				}
				if n := h.sessionsWithToken(t, "deactivated-sign-in"); n != 0 {
					t.Errorf("%d rows hold the refused sign-in's token", n)
				}
				if e := h.epoch(t, epochUserA); e != tt.wantEpoch {
					t.Errorf("the epoch is %d, want %d", e, tt.wantEpoch)
				}
			})
		}
		t.Run("control, the deactivation rolls back: the insert goes ahead", func(t *testing.T) {
			h.reset(t)
			_, err := epochPair(t, h, blockedInsert, false,
				func(q *gen.Queries) error { return deactivate(q, true) },
				func(q *gen.Queries) (gen.Session, error) {
					return q.CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "waiting-sign-in"))
				})
			if err != nil {
				t.Fatalf("an insert that waited for a deactivation that rolled back: %v", err)
			}
		})
	})

	t.Run("concurrent sign-ins of one user do not wait for each other", func(t *testing.T) {
		// FOR SHARE conflicts with the bump's lock and with nothing a sign-in holds:
		// the lock is there to order the insert against a revoke-all, and must not
		// serialise logins. One transaction holds its insert open; a second sign-in of
		// the same user must complete while it does.
		h.reset(t)
		tx, err := env.Pool.Begin(env.Ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		if _, err := h.q.WithTx(tx).CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "first-sign-in")); err != nil {
			t.Fatalf("the first sign-in: %v", err)
		}
		ctx, cancel := context.WithTimeout(env.Ctx, 5*time.Second)
		defer cancel()
		if _, err := h.q.CreateSessionAtEpoch(ctx, epochParams(epochUserA, 0, "second-sign-in")); err != nil {
			t.Fatalf("a second sign-in of the same user while the first holds its lock: %v", err)
		}
	})
}

// TestRevokeAll_TheWrongShapesLeaveASessionLive is the negative control for the
// interleavings above, and the reason RevokeAllUserSessionsIn is three statements in
// the order it is. It runs the two tempting alternatives against the very
// interleaving b — an insert in progress when the revoke-all arrives — and shows
// that each leaves the session LIVE, which is the state every test above forbids:
//
//   - one statement, a data-modifying CTE that bumps the epoch and revokes the
//     sessions: it takes ONE snapshot when it starts, so a session the insert
//     commits while the statement waits for the row lock is invisible to its UPDATE
//     of sessions;
//   - revoke first, bump afterwards: the revoke runs and finishes while the insert
//     is open, and the bump that follows waits — the session was committed after
//     the revoke had looked.
//
// If Postgres ever changed so that either of these held, these would fail and the
// three-statement shape could be revisited; until then they are what shows that the
// tests above can fail, and for the reason the code comment gives.
func TestRevokeAll_TheWrongShapesLeaveASessionLive(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	h, purge := newEpochHarness(t, env)
	defer purge()

	shapes := []struct {
		name string
		run  func() error
	}{
		{
			name: "one statement: a CTE that bumps and revokes",
			run: func() error {
				// The bump is the part that waits for the row lock; the revoke is the
				// main statement of the same snapshot.
				_, err := env.Pool.Exec(env.Ctx, `
					WITH bumped AS (
						UPDATE users SET auth_epoch = auth_epoch + 1 WHERE id = $1 RETURNING id
					)
					UPDATE sessions SET is_revoked = true WHERE user_id = $1`, epochUserA)
				return err
			},
		},
		{
			name: "revoke first, bump afterwards",
			run: func() error {
				if err := h.q.RevokeAllUserSessions(env.Ctx, epochUserA); err != nil {
					return err
				}
				_, err := h.q.BumpUserAuthEpoch(env.Ctx, epochUserA)
				return err
			},
		},
	}

	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			h.reset(t)
			var created gen.Session
			_, err := epochPair(t, h, "%UPDATE users SET auth_epoch%", true,
				func(q *gen.Queries) error {
					s, err := q.CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "racing-sign-in"))
					created = s
					return err
				},
				func(*gen.Queries) (struct{}, error) { return struct{}{}, sh.run() })
			if err != nil {
				t.Fatalf("the alternative revoke-all: %v", err)
			}
			if created.ID == uuid.Nil {
				t.Fatal("the insert holding the lock returned no session")
			}
			if h.revoked(t, created.ID) {
				t.Errorf("the session ended revoked: this shape of revoke-all is safe after all, and the reasoning in RevokeAllUserSessionsIn and CreateSessionAtEpoch is wrong")
			}
		})
	}
}

// TestRevokeAllUserSessions_OverlappingRevokeAllsDoNotDeadlock drives the statement
// that ends a user's sessions against ITSELF, the way two sign-outs of all devices
// overlap on the pool (each is three separate statements there, not one transaction):
// several workers revoke the same user's sessions again and again, half of them
// already revoked.
//
// The statement used to rewrite every session row of the user, revoked ones included,
// on every call. Each rewrite moves the rows to new places in the table, so two
// overlapping statements met each other's rows in different orders, waited on each
// other's row locks and deadlocked (SQLSTATE 40P01, found by the server after its
// deadlock_timeout), the loser's sign-out failing for nothing. Writing only the rows
// that are still live gives the second statement nothing left to rewrite once the first
// has committed, and no cycle can form.
//
// The count is of deadlocks and of any other error: with the predicate there are none,
// and the run is fast, because after the first pass every statement matches no row.
// Without it the same workload deadlocks repeatedly (a quarter to a third of the
// statements in the runs that calibrated these numbers) and each deadlock costs the
// server's deadlock_timeout, so the failure is both loud and slow.
func TestRevokeAllUserSessions_OverlappingRevokeAllsDoNotDeadlock(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	h, purge := newEpochHarness(t, env)
	defer purge()
	h.reset(t)

	const (
		sessions   = 400
		workers    = 3
		iterations = 25
	)
	for i := 0; i < sessions; i++ {
		s := h.mustCreate(t, epochUserA, 0, fmt.Sprintf("overlap-%d", i))
		if i%2 == 0 {
			h.exec(t, "revoke half of them first", `UPDATE sessions SET is_revoked = true WHERE id = $1`, s.ID)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var (
		wg        sync.WaitGroup
		deadlocks atomic.Int64
		others    atomic.Int64
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for it := 0; it < iterations; it++ {
				if err := h.q.RevokeAllUserSessions(ctx, epochUserA); err != nil {
					var pgErr *pgconn.PgError
					if errors.As(err, &pgErr) && pgErr.Code == "40P01" {
						deadlocks.Add(1)
					} else {
						others.Add(1)
						t.Errorf("RevokeAllUserSessions: %v", err)
					}
				}
			}
		}()
	}
	wg.Wait()

	if n := deadlocks.Load(); n != 0 {
		t.Errorf("%d of %d overlapping revoke-alls deadlocked (40P01): the statement rewrites rows that are already revoked", n, workers*iterations)
	}
	var live int
	if err := env.Pool.QueryRow(env.Ctx, `SELECT count(*) FROM sessions WHERE user_id = $1 AND is_revoked = false`, epochUserA).Scan(&live); err != nil {
		t.Fatalf("count live sessions: %v", err)
	}
	if live != 0 {
		t.Errorf("%d sessions are still live after the revoke-alls", live)
	}
}

// revokeShape is a revoke-all TRANSACTION as a handler runs it, first statement
// included: a password change writes the new hash and then ends the sessions; a
// deactivation writes the inactive flag and then ends them. Neither starts with the
// epoch bump — their first statement takes the users row's lock on its own — so the
// interleavings of TestSessionInsertAgainstARevokeAll, which start at the bump, are
// not the whole story, and these are the production shapes.
type revokeShape struct {
	name string
	// first is the shape's own first statement, on the transaction's Queries. It
	// returns its error instead of failing the test: it runs on goroutines of its own.
	first func(env *migrationTestEnv, q *gen.Queries) error
	// like matches the text of that first statement in pg_stat_activity.
	like string
}

var revokeShapes = []revokeShape{
	{
		name: "a password change",
		first: func(env *migrationTestEnv, q *gen.Queries) error {
			rows, err := q.UpdatePassword(env.Ctx, gen.UpdatePasswordParams{
				ID: epochUserA, PasswordHash: "hash-of-the-new-password", ExpectedHash: "x"})
			if err != nil {
				return err
			}
			if rows != 1 {
				return fmt.Errorf("UpdatePassword changed %d rows, want 1", rows)
			}
			return nil
		},
		like: "%SET password_hash = %", // the statement spans lines: UPDATE users / SET …
	},
	{
		name: "a deactivation",
		first: func(env *migrationTestEnv, q *gen.Queries) error {
			_, err := q.UpdateUserProfile(env.Ctx, epochDeactivate(epochUserA))
			return err
		},
		like: "%SET display_name = COALESCE%",
	},
}

// run executes the shape in a transaction of the given isolation level and returns
// the ids the revoke-all listed.
func (sh revokeShape) run(env *migrationTestEnv, q *gen.Queries, iso pgx.TxIsoLevel) ([]uuid.UUID, error) {
	tx, err := env.Pool.BeginTx(env.Ctx, pgx.TxOptions{IsoLevel: iso})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	qx := q.WithTx(tx)
	if err := sh.first(env, qx); err != nil {
		return nil, err
	}
	ids, err := auth.RevokeAllUserSessionsIn(env.Ctx, qx, epochUserA)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(env.Ctx); err != nil {
		return nil, err
	}
	return ids, nil
}

// TestRevokeAllTransactions_AgainstASignInInProgress puts each production revoke-all
// transaction on the user row with a sign-in's insert in progress, in both orders, and
// requires the same two outcomes as the interleavings at the bump: created and
// revoked, or refused.
//
//	insert first: the insert holds the row lock (FOR SHARE, uncommitted) and the
//	  transaction's FIRST statement — the password write or the inactive flag — blocks
//	  on it; the insert commits; the transaction runs to its end, and its listing, taken
//	  after that commit, finds the session and the revoke ends it.
//	transaction first: the transaction has run its first statement and the revoke-all
//	  (the row lock is held until it commits); the insert blocks, and after the commit
//	  re-evaluates against the new epoch and is refused.
//
// The transaction is begun READ COMMITTED, as the deactivation handler begins its own
// (it names the level; the password change will be pinned the same way when it is
// reworked). TestRevokeAll_UnderRepeatableReadALateInsertSurvives is why that matters.
func TestRevokeAllTransactions_AgainstASignInInProgress(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	h, purge := newEpochHarness(t, env)
	defer purge()

	for _, sh := range revokeShapes {
		t.Run(sh.name+"/the_insert_holds_the_lock_first", func(t *testing.T) {
			h.reset(t)
			h.exec(t, "the password the shape changes", `UPDATE users SET password_hash = 'x' WHERE id = $1`, epochUserA)
			var created gen.Session
			ids, err := epochPair(t, h, sh.like, true,
				func(q *gen.Queries) error {
					s, err := q.CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "racing-sign-in"))
					created = s
					return err
				},
				func(q *gen.Queries) ([]uuid.UUID, error) { return sh.run(env, q, pgx.ReadCommitted) })
			if err != nil {
				t.Fatalf("the transaction: %v", err)
			}
			if created.ID == uuid.Nil {
				t.Fatal("the insert holding the lock returned no session")
			}
			if !h.revoked(t, created.ID) {
				t.Fatal("the session created while the transaction waited is LIVE: a sign-in outlived the revoke-all that began after it")
			}
			if len(ids) != 1 || ids[0] != created.ID {
				t.Errorf("the revoke-all listed %v, want exactly the session the insert committed (%v)", ids, created.ID)
			}
			if got := h.epoch(t, epochUserA); got != 1 {
				t.Errorf("the epoch is %d, want 1", got)
			}
		})

		t.Run(sh.name+"/the_transaction_holds_the_lock_first", func(t *testing.T) {
			h.reset(t)
			h.exec(t, "the password the shape changes", `UPDATE users SET password_hash = 'x' WHERE id = $1`, epochUserA)
			got, err := epochPair(t, h, "%INSERT INTO sessions%", true,
				func(q *gen.Queries) error {
					if err := sh.first(env, q); err != nil {
						return err
					}
					_, err := auth.RevokeAllUserSessionsIn(env.Ctx, q, epochUserA)
					return err
				},
				func(q *gen.Queries) (gen.Session, error) {
					return q.CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "waiting-sign-in"))
				})
			if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("an insert that waited for the transaction: (%v, %v), want pgx.ErrNoRows", got.ID, err)
			}
			if n := h.sessionsWithToken(t, "waiting-sign-in"); n != 0 {
				t.Errorf("%d rows hold the refused sign-in's token", n)
			}
		})
	}
}

// TestRevokeAll_UnderRepeatableReadALateInsertSurvives is the negative control for the
// isolation level, and the reason the deactivation transaction names READ COMMITTED
// instead of trusting the server's default. The same interleaving as above — a
// sign-in's insert in progress when the transaction starts, committing while it waits —
// run at REPEATABLE READ leaves the session LIVE: the transaction's snapshot is taken
// by its first statement, which is before the insert committed, so the listing finds
// nothing and the revoke does not see the row. The bump has done its part (the epoch
// moved), but the session that committed through the gap is not ended.
//
// If this ever stops failing — the statement errors, or Postgres changes what a
// snapshot sees — the dependence on READ COMMITTED is gone and the reasoning in
// RevokeAllUserSessionsIn and CreateSessionAtEpoch can be revisited.
func TestRevokeAll_UnderRepeatableReadALateInsertSurvives(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	h, purge := newEpochHarness(t, env)
	defer purge()

	for _, sh := range revokeShapes {
		t.Run(sh.name, func(t *testing.T) {
			h.reset(t)
			h.exec(t, "the password the shape changes", `UPDATE users SET password_hash = 'x' WHERE id = $1`, epochUserA)
			var created gen.Session
			ids, err := epochPair(t, h, sh.like, true,
				func(q *gen.Queries) error {
					s, err := q.CreateSessionAtEpoch(env.Ctx, epochParams(epochUserA, 0, "racing-sign-in"))
					created = s
					return err
				},
				func(q *gen.Queries) ([]uuid.UUID, error) { return sh.run(env, q, pgx.RepeatableRead) })
			if err != nil {
				t.Fatalf("the REPEATABLE READ transaction failed outright (%v): the failure this control documents is a silent one", err)
			}
			if created.ID == uuid.Nil {
				t.Fatal("the insert holding the lock returned no session")
			}
			if h.revoked(t, created.ID) || len(ids) != 0 {
				t.Errorf("revoked=%t listed=%v: the session was ended after all, so READ COMMITTED is no longer what the guarantee rests on", h.revoked(t, created.ID), ids)
			}
		})
	}
}
