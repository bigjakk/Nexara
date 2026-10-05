package db

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
	gen "github.com/bigjakk/nexara/internal/db/generated"
)

// The SQL half of the refresh/sign-out race (migration 000105, queries/sessions.sql):
// what RotateSessionToken and GetSessionByPreviousTokenHash do against Postgres,
// including the interleavings of a refresh and a revoke, driven with real row
// locks on separate connections rather than described.
//
// These skip without NEXARA_TEST_DB_URL, like every test here that needs a
// database, so the Go half — how a row count becomes a 401, which statements each
// entry point may send — is pinned without one in internal/auth/session_test.go
// and internal/api/handlers/auth_refresh_race_test.go. They live in this package
// because CI exports the URL for the whole of `go test ./...` and the migration
// chain test here drops the shared database to zero: a database test in another
// package would be pulled out from under mid-run.

// Fixed ids so a run that aborts before its purge leaves rows the next run's
// up-front purge can find. sessions cascades from users.
var (
	sessRaceUserA = uuid.MustParse("a3000000-0000-4000-8000-000000000001")
	sessRaceUserB = uuid.MustParse("a3000000-0000-4000-8000-000000000002")
)

// sessionRace is the harness every test below shares: the real generated
// queries and the real SessionManager (over miniredis, which it needs only to
// write and delete its Redis rows) against the migrated throwaway database.
type sessionRace struct {
	ctx  context.Context
	pool *pgxpool.Pool
	q    *gen.Queries
	sm   *auth.SessionManager
}

// newSessionRace migrates to head, clears and seeds the two users every test
// keys its sessions on, and returns the harness and a purge for the caller to
// defer. The purge must be deferred AFTER env.Cleanup — see setupMigration — and
// carries its own context so it survives that ordering shifting.
func newSessionRace(t *testing.T, env *migrationTestEnv) (*sessionRace, func()) {
	t.Helper()

	migrateUp(t, env.Migrate)

	purge := func() {
		pctx, pcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer pcancel()
		_, _ = env.Pool.Exec(pctx, `DELETE FROM users WHERE id = ANY($1)`,
			[]uuid.UUID{sessRaceUserA, sessRaceUserB})
	}
	purge()

	for id, email := range map[uuid.UUID]string{
		sessRaceUserA: "session-race-a@example.com",
		sessRaceUserB: "session-race-b@example.com",
	} {
		if _, err := env.Pool.Exec(env.Ctx,
			`INSERT INTO users (id, email, password_hash, display_name)
			 VALUES ($1, $2, 'x', 'Session Race')`, id, email); err != nil {
			purge()
			t.Fatalf("seed user %s: %v", email, err)
		}
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	q := gen.New(env.Pool)
	return &sessionRace{
		ctx:  env.Ctx,
		pool: env.Pool,
		q:    q,
		sm:   auth.NewSessionManager(q, rdb),
	}, purge
}

// The role a fixture session is created with and the role the fixture rotations
// carry. They DIFFER on purpose: with one value for both, a rotation that wrote
// the wrong role — or none — would still leave the row reading as the test
// expects, and nothing here could tell.
const (
	sessionFixtureRole  = "admin"
	rotationFixtureRole = "viewer"
)

// newSession inserts a live session whose refresh token is token, through the
// generated CreateSession the application uses.
func (sr *sessionRace) newSession(t *testing.T, user uuid.UUID, token string) gen.Session {
	t.Helper()
	return sr.newSessionWithRole(t, user, token, sessionFixtureRole)
}

// newSessionWithRole is newSession with the recorded role of the caller's
// choosing, "" included: that is what a session issued before migration 000055
// holds.
//
// The session is created through the one statement that creates sessions,
// CreateSessionAtEpoch, against the user's CURRENT epoch — what a credential check
// that had just read the user would pass. The harness has to read it: a test that
// ends the user's sessions through the manager bumps the epoch (every revoke-all
// does, 000106), and a later fixture created against the epoch the user had at the
// start would be refused, which is the very behaviour session_epoch_db_test.go
// pins and not what these fixtures are for.
func (sr *sessionRace) newSessionWithRole(t *testing.T, user uuid.UUID, token, role string) gen.Session {
	t.Helper()
	s, err := sr.q.CreateSessionAtEpoch(sr.ctx, gen.CreateSessionAtEpochParams{
		UserID:    user,
		Epoch:     sr.epochOf(t, user),
		TokenHash: auth.HashToken(token),
		UserAgent: "Mozilla/5.0",
		IpAddress: "192.0.2.10",
		ExpiresAt: time.Now().Add(24 * time.Hour),
		UserRole:  role,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return s
}

// revokeAll ends every session of user the way a sign-out of all devices does:
// RevokeAllUserSessionsIn on the pool, then the Redis rows of the sessions it ended.
func (sr *sessionRace) revokeAll(user uuid.UUID) error {
	ids, err := auth.RevokeAllUserSessionsIn(sr.ctx, sr.q, user)
	if err != nil {
		return err
	}
	sr.sm.ForgetSessions(sr.ctx, ids)
	return nil
}

// epochOf is the user's auth_epoch right now.
func (sr *sessionRace) epochOf(t *testing.T, user uuid.UUID) int64 {
	t.Helper()
	u, err := sr.q.GetUserByID(sr.ctx, user)
	if err != nil {
		t.Fatalf("read the user's epoch: %v", err)
	}
	return u.AuthEpoch
}

func (sr *sessionRace) reload(t *testing.T, id uuid.UUID) gen.Session {
	t.Helper()
	s, err := sr.q.GetSessionByID(sr.ctx, id)
	if err != nil {
		t.Fatalf("reload session %v: %v", id, err)
	}
	return s
}

// rotate asks for the rotation a refresh would make: from the hash of oldToken to
// the hash of newToken.
func (sr *sessionRace) rotate(q *gen.Queries, id uuid.UUID, oldToken, newToken string) (int64, error) {
	return q.RotateSessionToken(sr.ctx, gen.RotateSessionTokenParams{
		ID:           id,
		OldTokenHash: auth.HashToken(oldToken),
		NewTokenHash: auth.HashToken(newToken),
		UserRole:     rotationFixtureRole,
	})
}

func (sr *sessionRace) mustRotate(t *testing.T, id uuid.UUID, oldToken, newToken string) {
	t.Helper()
	rows, err := sr.rotate(sr.q, id, oldToken, newToken)
	if err != nil || rows != 1 {
		t.Fatalf("setup rotation %q -> %q: rows=%d err=%v, want 1 row", oldToken, newToken, rows, err)
	}
}

// backdate moves rotated_at into the past, which is how a test reaches a
// rotation that happened a while ago without sleeping for it.
func (sr *sessionRace) backdate(t *testing.T, id uuid.UUID, ago time.Duration) {
	t.Helper()
	sr.exec(t, "backdate rotated_at",
		`UPDATE sessions SET rotated_at = now() - make_interval(secs => $2::float) WHERE id = $1`,
		id, ago.Seconds())
}

func (sr *sessionRace) exec(t *testing.T, what, sql string, args ...any) {
	t.Helper()
	if _, err := sr.pool.Exec(sr.ctx, sql, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// clearSessions deletes every session of the two test users.
func (sr *sessionRace) clearSessions(t *testing.T) {
	t.Helper()
	sr.exec(t, "clear sessions", `DELETE FROM sessions WHERE user_id = ANY($1)`,
		[]uuid.UUID{sessRaceUserA, sessRaceUserB})
}

func sameTimestamptz(a, b pgtype.Timestamptz) bool {
	return a.Valid == b.Valid && a.Time.Equal(b.Time)
}

// sessionUnchanged fails the test when the row differs from before in any column
// a rotation writes, or in the revoked flag.
func sessionUnchanged(t *testing.T, before, after gen.Session) {
	t.Helper()
	if after.TokenHash != before.TokenHash {
		t.Errorf("token_hash changed from %q to %q; a refused rotation must write nothing", before.TokenHash, after.TokenHash)
	}
	if after.PreviousTokenHash != before.PreviousTokenHash {
		t.Errorf("previous_token_hash changed from %+v to %+v", before.PreviousTokenHash, after.PreviousTokenHash)
	}
	if !sameTimestamptz(after.RotatedAt, before.RotatedAt) {
		t.Errorf("rotated_at changed from %+v to %+v", before.RotatedAt, after.RotatedAt)
	}
	if after.UserRole != before.UserRole {
		t.Errorf("user_role changed from %q to %q", before.UserRole, after.UserRole)
	}
	if !after.LastUsedAt.Equal(before.LastUsedAt) {
		t.Errorf("last_used_at changed from %v to %v", before.LastUsedAt, after.LastUsedAt)
	}
	if after.IsRevoked != before.IsRevoked {
		t.Errorf("is_revoked changed from %t to %t", before.IsRevoked, after.IsRevoked)
	}
}

// TestRotateSessionToken_OnlyTheLiveCurrentHashRotates pins the conditions of the
// rotation, one at a time. Each refusal row asserts two things: 0 rows, and — the
// one that matters — that the row is exactly what it was. The old unconditional
// UPDATE overwrote the hash of a revoked session, and a rotation that refuses by
// reporting 0 rows but still writes would pass the first assertion alone.
//
// The first row is the positive control for the rest: the same harness does
// rotate, and does record the previous hash.
func TestRotateSessionToken_OnlyTheLiveCurrentHashRotates(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()

	const (
		oldToken = "old-refresh-token"
		newToken = "new-refresh-token"
	)

	tests := []struct {
		name string
		// prepare changes the world after the session exists and before the
		// refresh presents its hash.
		prepare func(t *testing.T, s gen.Session)
		// present is what the refresh presents: the session id and the token it
		// validated. nil means the session and oldToken.
		present  func(t *testing.T, s gen.Session) (uuid.UUID, string)
		wantRows int64
	}{
		{
			name:     "the current hash of a live session",
			wantRows: 1,
		},
		{
			name: "a revoked session",
			prepare: func(t *testing.T, s gen.Session) {
				if err := sr.q.RevokeSession(sr.ctx, s.ID); err != nil {
					t.Fatalf("revoke: %v", err)
				}
			},
		},
		{
			name: "an expired session",
			prepare: func(t *testing.T, s gen.Session) {
				sr.exec(t, "expire", `UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`, s.ID)
			},
		},
		{
			name: "a hash the session never held",
			present: func(_ *testing.T, s gen.Session) (uuid.UUID, string) {
				return s.ID, "a-token-this-session-never-had"
			},
		},
		{
			name: "another session's hash",
			present: func(t *testing.T, s gen.Session) (uuid.UUID, string) {
				sr.newSession(t, sessRaceUserB, "the-other-sessions-token")
				return s.ID, "the-other-sessions-token"
			},
		},
		{
			name: "no such session",
			present: func(*testing.T, gen.Session) (uuid.UUID, string) {
				return uuid.New(), oldToken
			},
		},
		{
			// The loser of two refreshes presenting one cookie: the first rotated,
			// so the hash the second presents is no longer the current one.
			name: "the hash a concurrent refresh has just rotated away",
			prepare: func(t *testing.T, s gen.Session) {
				sr.mustRotate(t, s.ID, oldToken, "the-winning-refreshs-token")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr.clearSessions(t)
			defer sr.clearSessions(t)

			s := sr.newSession(t, sessRaceUserA, oldToken)
			if tt.prepare != nil {
				tt.prepare(t, s)
			}
			id, presented := s.ID, oldToken
			if tt.present != nil {
				id, presented = tt.present(t, s)
			}
			before := sr.reload(t, s.ID)

			rows, err := sr.rotate(sr.q, id, presented, newToken)
			if err != nil {
				t.Fatalf("RotateSessionToken: %v", err)
			}
			if rows != tt.wantRows {
				t.Fatalf("rows = %d, want %d", rows, tt.wantRows)
			}

			after := sr.reload(t, s.ID)
			if tt.wantRows == 0 {
				sessionUnchanged(t, before, after)
				return
			}

			if after.TokenHash != auth.HashToken(newToken) {
				t.Errorf("token_hash = %q, want the new hash", after.TokenHash)
			}
			if !after.PreviousTokenHash.Valid || after.PreviousTokenHash.String != auth.HashToken(oldToken) {
				t.Errorf("previous_token_hash = %+v, want the hash that was rotated away", after.PreviousTokenHash)
			}
			if age := time.Since(after.RotatedAt.Time); !after.RotatedAt.Valid || age < -time.Minute || age > time.Minute {
				t.Errorf("rotated_at = %+v, want a moment ago", after.RotatedAt)
			}
			if after.IsRevoked {
				t.Error("a rotation revoked the session")
			}
			if !after.LastUsedAt.After(before.LastUsedAt) {
				t.Errorf("last_used_at did not advance: %v -> %v", before.LastUsedAt, after.LastUsedAt)
			}
			if after.UserRole != rotationFixtureRole {
				t.Errorf("user_role = %q, want %q, the role the rotation carried (the session was created as %q)",
					after.UserRole, rotationFixtureRole, sessionFixtureRole)
			}
		})
	}
}

// TestRotateSessionToken_OneOfTwoRacingRefreshesWins runs two rotations from one
// presented hash at the same moment, many times over, and requires exactly one
// to succeed every time and the row to end on the winner's hash.
//
// Unconditional — before this change — both succeeded, each handing its client a
// different new cookie, of which the browser kept whichever landed last. That
// outcome does not depend on timing, so this fails every iteration against the
// old statement; the repetition is there to give a real race the chance to take
// both orderings on a correct one.
func TestRotateSessionToken_OneOfTwoRacingRefreshesWins(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	const rounds = 25
	for i := range rounds {
		s := sr.newSession(t, sessRaceUserA, "shared-cookie")

		type outcome struct {
			rows int64
			err  error
		}
		results := make([]outcome, 2)
		newTokens := []string{"winner-candidate-one", "winner-candidate-two"}

		start := make(chan struct{})
		var wg sync.WaitGroup
		for j := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				rows, err := sr.rotate(sr.q, s.ID, "shared-cookie", newTokens[j])
				results[j] = outcome{rows, err}
			}()
		}
		close(start)
		wg.Wait()

		winners := 0
		var winner string
		for j, r := range results {
			if r.err != nil {
				t.Fatalf("round %d: rotation %d: %v", i, j, r.err)
			}
			if r.rows == 1 {
				winners++
				winner = newTokens[j]
			} else if r.rows != 0 {
				t.Fatalf("round %d: rotation %d changed %d rows", i, j, r.rows)
			}
		}
		if winners != 1 {
			t.Fatalf("round %d: %d of 2 refreshes presenting one cookie rotated it, want exactly 1", i, winners)
		}
		after := sr.reload(t, s.ID)
		if after.TokenHash != auth.HashToken(winner) {
			t.Fatalf("round %d: the row holds %q, want the winner's hash %q", i, after.TokenHash, auth.HashToken(winner))
		}
		if after.PreviousTokenHash.String != auth.HashToken("shared-cookie") {
			t.Fatalf("round %d: previous_token_hash = %+v, want the shared cookie's hash", i, after.PreviousTokenHash)
		}
	}
}

// waitForBlockedUpdate waits until Postgres reports a statement on this database
// stuck waiting for a row lock, which is the only evidence that the interleaving
// a test set up is the one it is about to assert on. Without it a test that
// merely started a goroutine and committed could pass whether or not the
// statement ever waited.
func waitForBlockedUpdate(t *testing.T, sr *sessionRace) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var n int
		if err := sr.pool.QueryRow(sr.ctx,
			`SELECT count(*) FROM pg_stat_activity
			  WHERE datname = current_database()
			    AND wait_event_type = 'Lock'
			    AND query LIKE '%UPDATE sessions%'`).Scan(&n); err != nil {
			t.Fatalf("read pg_stat_activity: %v", err)
		}
		if n > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the second statement never blocked on the row lock, so the interleaving under test did not happen")
}

// lockedPair runs hold inside a transaction that stays open (so it holds the
// session row's lock), starts wait on another connection, waits until Postgres
// says wait is blocked, checks it really has not finished, then commits hold and
// returns wait's result. It is how these tests put two statements on one row in a
// chosen order rather than hoping a race produces it.
func lockedPair[T any](t *testing.T, sr *sessionRace, hold func(q *gen.Queries) error, wait func(q *gen.Queries) (T, error)) T {
	t.Helper()

	tx, err := sr.pool.Begin(sr.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	committed := false
	defer func() {
		// Never leave the row locked if the test dies between the two halves.
		if !committed {
			rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = tx.Rollback(rbCtx)
		}
	}()

	if err := hold(sr.q.WithTx(tx)); err != nil {
		t.Fatalf("the statement holding the lock: %v", err)
	}

	type result struct {
		val T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := wait(sr.q)
		done <- result{v, err}
	}()

	waitForBlockedUpdate(t, sr)
	select {
	case r := <-done:
		t.Fatalf("the second statement finished (%v, %v) while the first transaction still held the row lock", r.val, r.err)
	default:
	}

	if err := tx.Commit(sr.ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	committed = true

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("the second statement: %v", r.err)
		}
		return r.val
	case <-time.After(15 * time.Second):
		t.Fatal("the second statement never finished after the lock was released")
	}
	panic("unreachable")
}

// revoker is one of the ways a session ends, as the application issues it.
type revoker struct {
	name string
	// via names who calls it, for the failure message and the reader.
	via string
	// viaManager revokes through the SessionManager method that handler calls.
	viaManager func(sr *sessionRace, s gen.Session) error
	// viaQueries is the same statement against any Queries — the pool, or a
	// transaction the test is holding open.
	viaQueries func(ctx context.Context, q *gen.Queries, s gen.Session) error
}

var sessionRevokers = []revoker{
	{
		name: "RevokeSession",
		via:  "Logout, DELETE /auth/sessions/:id, and a refresh that finds its user gone, disabled or re-roled",
		viaManager: func(sr *sessionRace, s gen.Session) error {
			return sr.sm.RevokeSession(sr.ctx, s.ID)
		},
		viaQueries: func(ctx context.Context, q *gen.Queries, s gen.Session) error {
			return q.RevokeSession(ctx, s.ID)
		},
	},
	{
		name: "RevokeAllUserSessions",
		via:  "LogoutAll, a password change, and an admin deactivating the user",
		viaManager: func(sr *sessionRace, s gen.Session) error {
			return sr.revokeAll(s.UserID)
		},
		viaQueries: func(ctx context.Context, q *gen.Queries, s gen.Session) error {
			return q.RevokeAllUserSessions(ctx, s.UserID)
		},
	},
}

// TestRotateSessionToken_RevokeInterleavings puts a refresh and each way of
// ending a session on one row in each order, against Postgres, and requires the
// session to end revoked and the refresh to issue nothing it should not.
//
//	a. The refresh validated, the revoke committed, then the refresh's rotation
//	   ran. The rotation must see the revoke: 0 rows, and nothing written. This is
//	   the race the change exists for, and LogoutAll is one of its two revokers.
//	b. The rotation holds the row lock in a transaction that has not committed;
//	   the revoke arrives and waits; the rotation commits. The refresh won, and the
//	   revoke then applies on top of the rotated row: the session ends revoked.
//	c. The revoke holds the row lock; the rotation arrives and waits; the revoke
//	   commits. The rotation re-evaluates its WHERE against the revoked row (this
//	   is READ COMMITTED's re-check, and the reason the statement can be a single
//	   conditional UPDATE): 0 rows, and nothing written.
//
// In b and c the blocked statement is shown to be blocked, by Postgres, before
// the lock is released — see lockedPair.
func TestRotateSessionToken_RevokeInterleavings(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	const (
		oldToken = "old-refresh-token"
		newToken = "new-refresh-token"
	)

	for _, rv := range sessionRevokers {
		t.Run(rv.name+"/a_revoke_commits_before_the_rotation_runs", func(t *testing.T) {
			sr.clearSessions(t)
			s := sr.newSession(t, sessRaceUserA, oldToken)

			// The refresh's validation, as Refresh makes it.
			validated, err := sr.sm.ValidateRefreshToken(sr.ctx, oldToken)
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			// ...then the revoke, through the manager method its handler calls.
			if err := rv.viaManager(sr, s); err != nil {
				t.Fatalf("%s: %v", rv.name, err)
			}
			before := sr.reload(t, s.ID)

			// ...then the rotation, through the production helper.
			err = auth.RotateRefreshToken(sr.ctx, sr.q, validated, auth.HashToken(newToken), "admin")
			if !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("rotation after %s: error = %v, want ErrInvalidToken (used by %s)", rv.name, err, rv.via)
			}
			after := sr.reload(t, s.ID)
			if !after.IsRevoked {
				t.Error("the session is not revoked")
			}
			sessionUnchanged(t, before, after)
		})

		t.Run(rv.name+"/a_control_without_a_revoke_the_same_sequence_rotates", func(t *testing.T) {
			sr.clearSessions(t)
			s := sr.newSession(t, sessRaceUserA, oldToken)

			validated, err := sr.sm.ValidateRefreshToken(sr.ctx, oldToken)
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			if err := auth.RotateRefreshToken(sr.ctx, sr.q, validated, auth.HashToken(newToken), "admin"); err != nil {
				t.Fatalf("rotation with nothing interfering: %v", err)
			}
			if got := sr.reload(t, s.ID); got.TokenHash != auth.HashToken(newToken) || got.IsRevoked {
				t.Errorf("after an uncontested rotation the row holds %q revoked=%t", got.TokenHash, got.IsRevoked)
			}
		})

		t.Run(rv.name+"/b_revoke_waits_for_a_rotation_in_progress", func(t *testing.T) {
			sr.clearSessions(t)
			s := sr.newSession(t, sessRaceUserA, oldToken)

			lockedPair(t, sr,
				func(q *gen.Queries) error {
					rows, err := sr.rotate(q, s.ID, oldToken, newToken)
					if err != nil || rows != 1 {
						t.Fatalf("the rotation holding the lock: rows=%d err=%v, want 1", rows, err)
					}
					return nil
				},
				func(q *gen.Queries) (struct{}, error) {
					return struct{}{}, rv.viaQueries(sr.ctx, q, s)
				})

			after := sr.reload(t, s.ID)
			if !after.IsRevoked {
				t.Fatalf("the session is live: a revoke that waited for a rotation was lost (%s)", rv.via)
			}
			if after.TokenHash != auth.HashToken(newToken) {
				t.Errorf("token_hash = %q, want the rotation's hash: the refresh won this ordering", after.TokenHash)
			}
		})

		t.Run(rv.name+"/c_rotation_waits_for_a_revoke_in_progress", func(t *testing.T) {
			sr.clearSessions(t)
			s := sr.newSession(t, sessRaceUserA, oldToken)
			before := sr.reload(t, s.ID)

			rows := lockedPair(t, sr,
				func(q *gen.Queries) error { return rv.viaQueries(sr.ctx, q, s) },
				func(q *gen.Queries) (int64, error) { return sr.rotate(q, s.ID, oldToken, newToken) })

			if rows != 0 {
				t.Fatalf("a rotation that waited for a revoke changed %d rows, want 0 (%s)", rows, rv.via)
			}
			after := sr.reload(t, s.ID)
			if !after.IsRevoked {
				t.Error("the session is not revoked")
			}
			// is_revoked is the revoke's own write; everything else must be as it was.
			before.IsRevoked = true
			sessionUnchanged(t, before, after)
		})
	}

	t.Run("d_second_rotation_waits_for_the_first_and_is_refused", func(t *testing.T) {
		sr.clearSessions(t)
		s := sr.newSession(t, sessRaceUserA, oldToken)

		rows := lockedPair(t, sr,
			func(q *gen.Queries) error {
				rows, err := sr.rotate(q, s.ID, oldToken, "winner-token")
				if err != nil || rows != 1 {
					t.Fatalf("the rotation holding the lock: rows=%d err=%v, want 1", rows, err)
				}
				return nil
			},
			func(q *gen.Queries) (int64, error) { return sr.rotate(q, s.ID, oldToken, "loser-token") })

		if rows != 0 {
			t.Fatalf("the second refresh on one cookie changed %d rows, want 0", rows)
		}
		if got := sr.reload(t, s.ID); got.TokenHash != auth.HashToken("winner-token") {
			t.Errorf("the row holds %q, want the first refresh's hash", got.TokenHash)
		}
	})
}

// TestGetSessionByPreviousTokenHash_Window pins the revocation-only lookup: it
// matches the hash a session had before its last rotation, inside the window,
// while the session is live — and in no other case.
//
// Every "no match" row is checked beside the first row, which shows the same
// session being found, so a lookup that matches nothing at all cannot pass.
func TestGetSessionByPreviousTokenHash_Window(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	const (
		first  = "first-refresh-token"
		second = "second-refresh-token"
		third  = "third-refresh-token"
	)
	window := auth.PreviousTokenRevocationWindow

	tests := []struct {
		name string
		// prepare runs on a session that has been rotated once (first -> second).
		prepare func(t *testing.T, s gen.Session)
		look    string // the token whose hash is looked up
		seconds float64
		want    bool
	}{
		{name: "just rotated", look: first, seconds: window.Seconds(), want: true},
		{
			name:    "rotated well inside the window",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, window/4) },
			look:    first, seconds: window.Seconds(), want: true,
		},
		{
			name:    "rotated just inside the window",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, window-10*time.Second) },
			look:    first, seconds: window.Seconds(), want: true,
		},
		{
			name:    "rotated just outside the window",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, window+10*time.Second) },
			look:    first, seconds: window.Seconds(),
		},
		{
			name:    "rotated long ago",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, 24*time.Hour) },
			look:    first, seconds: window.Seconds(),
		},
		{
			name: "a revoked session",
			prepare: func(t *testing.T, s gen.Session) {
				if err := sr.q.RevokeSession(sr.ctx, s.ID); err != nil {
					t.Fatalf("revoke: %v", err)
				}
			},
			look: first, seconds: window.Seconds(),
		},
		{
			// A previous hash with no timestamp can only be a row written some
			// other way; it must not be matchable for ever.
			name: "a previous hash with no rotated_at",
			prepare: func(t *testing.T, s gen.Session) {
				sr.exec(t, "null rotated_at", `UPDATE sessions SET rotated_at = NULL WHERE id = $1`, s.ID)
			},
			look: first, seconds: window.Seconds(),
		},
		{
			// The query itself returns a live session or nothing: it does not leave
			// expiry to its callers.
			name: "an expired session",
			prepare: func(t *testing.T, s gen.Session) {
				sr.exec(t, "expire", `UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`, s.ID)
			},
			look: first, seconds: window.Seconds(),
		},
		{name: "the current hash is not a previous hash", look: second, seconds: window.Seconds()},
		{
			name: "two rotations back",
			prepare: func(t *testing.T, s gen.Session) {
				sr.mustRotate(t, s.ID, second, third)
			},
			look: first, seconds: window.Seconds(),
		},
		{
			name: "one rotation back after two rotations",
			prepare: func(t *testing.T, s gen.Session) {
				sr.mustRotate(t, s.ID, second, third)
			},
			look: second, seconds: window.Seconds(), want: true,
		},
		{name: "a window of zero matches nothing", look: first, seconds: 0},
		{name: "a negative window matches nothing", look: first, seconds: -60},
		{name: "a token the session never had", look: "someone-elses-token", seconds: window.Seconds()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr.clearSessions(t)
			defer sr.clearSessions(t)

			s := sr.newSession(t, sessRaceUserA, first)
			sr.mustRotate(t, s.ID, first, second)
			if tt.prepare != nil {
				tt.prepare(t, s)
			}

			got, err := sr.q.GetSessionByPreviousTokenHash(sr.ctx, gen.GetSessionByPreviousTokenHashParams{
				TokenHash:     auth.HashToken(tt.look),
				WindowSeconds: tt.seconds,
			})
			switch {
			case tt.want && err != nil:
				t.Fatalf("lookup of %q: %v, want the session", tt.look, err)
			case tt.want && got.ID != s.ID:
				t.Fatalf("lookup found session %v, want %v", got.ID, s.ID)
			case !tt.want && err == nil:
				t.Fatalf("lookup of %q found session %v, want no match", tt.look, got.ID)
			case !tt.want && !errors.Is(err, pgx.ErrNoRows):
				t.Fatalf("lookup of %q: %v, want pgx.ErrNoRows", tt.look, err)
			}
		})
	}
}

// TestTokenIssuingLookupsNeverMatchThePreviousToken is the half of the rule that
// keeps the grace window from becoming a second credential: the lookup behind
// Refresh and the session list's is_current never honours a rotated-away token,
// however recently it was rotated.
//
// The control is the same session found BY the previous token through the
// revocation lookup, so the refusal is the query's doing and not an absence.
func TestTokenIssuingLookupsNeverMatchThePreviousToken(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	s := sr.newSession(t, sessRaceUserA, "first-refresh-token")
	sr.mustRotate(t, s.ID, "first-refresh-token", "second-refresh-token")

	if _, err := sr.q.GetSessionByPreviousTokenHash(sr.ctx, gen.GetSessionByPreviousTokenHashParams{
		TokenHash:     auth.HashToken("first-refresh-token"),
		WindowSeconds: auth.PreviousTokenRevocationWindow.Seconds(),
	}); err != nil {
		t.Fatalf("control: the revocation lookup does not find the session by its previous token: %v", err)
	}

	if _, err := sr.q.GetSessionByTokenHash(sr.ctx, auth.HashToken("first-refresh-token")); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("GetSessionByTokenHash(previous) = %v, want pgx.ErrNoRows", err)
	}
	if _, err := sr.sm.ValidateRefreshToken(sr.ctx, "first-refresh-token"); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("ValidateRefreshToken(previous) = %v, want ErrInvalidToken: it would let a rotated-away token refresh", err)
	}
	got, err := sr.sm.ValidateRefreshToken(sr.ctx, "second-refresh-token")
	if err != nil || got.ID != s.ID {
		t.Errorf("ValidateRefreshToken(current) = %v, %v, want the session", got.ID, err)
	}
}

// TestFindSessionForLogout_AgainstPostgres runs the sign-out lookup — the
// production wrapper, not just the query — over real sessions.
func TestFindSessionForLogout_AgainstPostgres(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	const (
		first  = "first-refresh-token"
		second = "second-refresh-token"
	)
	window := auth.PreviousTokenRevocationWindow

	tests := []struct {
		name    string
		prepare func(t *testing.T, s gen.Session)
		token   string
		want    bool
		wantVia bool // viaPrevious, when found
	}{
		{name: "the current token", token: second, want: true},
		{name: "the previous token, just rotated", token: first, want: true, wantVia: true},
		{
			name:    "the previous token, just inside the window",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, window-10*time.Second) },
			token:   first, want: true, wantVia: true,
		},
		{
			name:    "the previous token, just outside the window",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, window+10*time.Second) },
			token:   first,
		},
		{
			name: "the previous token of a session that has expired",
			prepare: func(t *testing.T, s gen.Session) {
				sr.exec(t, "expire", `UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`, s.ID)
			},
			token: first,
		},
		{
			name: "the current token of a session that has expired",
			prepare: func(t *testing.T, s gen.Session) {
				sr.exec(t, "expire", `UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`, s.ID)
			},
			token: second,
		},
		{
			name: "the previous token of a session already revoked",
			prepare: func(t *testing.T, s gen.Session) {
				if err := sr.q.RevokeSession(sr.ctx, s.ID); err != nil {
					t.Fatalf("revoke: %v", err)
				}
			},
			token: first,
		},
		{name: "a token nobody holds", token: "a-token-nobody-issued"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr.clearSessions(t)
			defer sr.clearSessions(t)

			s := sr.newSession(t, sessRaceUserA, first)
			sr.mustRotate(t, s.ID, first, second)
			if tt.prepare != nil {
				tt.prepare(t, s)
			}

			got, via, err := sr.sm.FindSessionForLogout(sr.ctx, tt.token)
			if tt.want {
				if err != nil || got.ID != s.ID {
					t.Fatalf("FindSessionForLogout = %v, %v, want session %v", got.ID, err, s.ID)
				}
				if via != tt.wantVia {
					t.Errorf("viaPrevious = %t, want %t", via, tt.wantVia)
				}
				return
			}
			if !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("FindSessionForLogout = %v, %v, want ErrInvalidToken", got.ID, err)
			}
		})
	}
}

// TestSignOutRacingARefresh_EndToEnd walks the two scenarios the change exists
// for through the production wrappers, against Postgres, from the session being
// created to the state each cookie ends in.
//
//   - A refresh rotates, and the sign-out that was sent a moment earlier — so it
//     still carries the OLD cookie — arrives after. It must revoke the session,
//     and the new cookie the refresh just handed out must be dead.
//   - A refresh validates, LogoutAll lands, then the refresh rotates. The rotation
//     must refuse: nothing issued, and no hash written to a session that is gone.
//
// The session list (ListUserSessions) is the user-visible half: the first
// scenario's failure mode was a session that kept showing as active.
func TestSignOutRacingARefresh_EndToEnd(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	listed := func(t *testing.T) int {
		t.Helper()
		rows, err := sr.q.ListUserSessions(sr.ctx, sessRaceUserA)
		if err != nil {
			t.Fatalf("list sessions: %v", err)
		}
		return len(rows)
	}

	t.Run("a sign-out one rotation behind still ends the session", func(t *testing.T) {
		sr.clearSessions(t)
		session, err := sr.sm.CreateSession(sr.ctx, sessRaceUserA, sr.epochOf(t, sessRaceUserA), "cookie-before-the-refresh", "admin",
			"Mozilla/5.0", "192.0.2.10", time.Hour, auth.DeviceInfo{Type: "web"})
		if err != nil {
			t.Fatalf("create session: %v", err)
		}

		// The refresh: validate, rotate. Its response (carrying the new cookie) is
		// still in flight.
		validated, err := sr.sm.ValidateRefreshToken(sr.ctx, "cookie-before-the-refresh")
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if err := auth.RotateRefreshToken(sr.ctx, sr.q, validated, auth.HashToken("cookie-from-the-refresh"), "admin"); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		if n := listed(t); n != 1 {
			t.Fatalf("control: %d sessions listed after the refresh, want 1", n)
		}

		// The sign-out, sent with the old cookie.
		found, via, err := sr.sm.FindSessionForLogout(sr.ctx, "cookie-before-the-refresh")
		if err != nil {
			t.Fatalf("the sign-out with the old cookie found no session: %v", err)
		}
		if !via {
			t.Error("the sign-out matched by the old cookie but viaPrevious is false; its audit row would not say so")
		}
		if found.ID != session.ID {
			t.Fatalf("sign-out found %v, want %v", found.ID, session.ID)
		}
		if err := sr.sm.RevokeSession(sr.ctx, found.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}

		// The new cookie lands in the browser after the sign-out cleared it. It
		// must be worth nothing.
		if _, err := sr.sm.ValidateRefreshToken(sr.ctx, "cookie-from-the-refresh"); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("the refresh's new cookie still validates after the sign-out: %v", err)
		}
		if n := listed(t); n != 0 {
			t.Errorf("%d sessions still listed as active after the sign-out", n)
		}
	})

	t.Run("a refresh that validated before LogoutAll mints nothing", func(t *testing.T) {
		sr.clearSessions(t)
		s := sr.newSession(t, sessRaceUserA, "cookie-before-the-refresh")

		validated, err := sr.sm.ValidateRefreshToken(sr.ctx, "cookie-before-the-refresh")
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if err := sr.revokeAll(sessRaceUserA); err != nil {
			t.Fatalf("LogoutAll: %v", err)
		}

		err = auth.RotateRefreshToken(sr.ctx, sr.q, validated, auth.HashToken("cookie-from-the-refresh"), "admin")
		if !errors.Is(err, auth.ErrInvalidToken) {
			t.Fatalf("rotation after LogoutAll: %v, want ErrInvalidToken", err)
		}
		after := sr.reload(t, s.ID)
		if after.TokenHash != auth.HashToken("cookie-before-the-refresh") || after.PreviousTokenHash.Valid {
			t.Errorf("the refusal still wrote: token_hash=%q previous=%+v", after.TokenHash, after.PreviousTokenHash)
		}
		if _, err := sr.sm.ValidateRefreshToken(sr.ctx, "cookie-from-the-refresh"); !errors.Is(err, auth.ErrInvalidToken) {
			t.Errorf("a cookie from the refused refresh validates: %v", err)
		}
		if n := listed(t); n != 0 {
			t.Errorf("%d sessions listed after LogoutAll", n)
		}
	})
}

// TestRefusalSparesCookie_AgainstPostgres runs the question Refresh asks of every
// refusal — was this the loser of a race? — over real sessions, through the
// production method and not just the query.
//
// It is true only for the token a LIVE session replaced within
// auth.ConcurrentRefreshTolerance, and each way of being otherwise is a row: just
// outside the tolerance, a minute ago, a revoked session (the sign-out race), an
// expired one, a token two rotations old, the session's own current token, a
// token nobody holds, a rotation with no timestamp. The first rows are the
// controls for the rest: the same harness does answer true.
//
// The tolerance is the SHORT window, and the rows prove it is not the sign-out
// one: a token replaced a minute ago is inside the window Logout uses and must not
// spare a cookie.
func TestRefusalSparesCookie_AgainstPostgres(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	const (
		first  = "first-refresh-token"
		second = "second-refresh-token"
		third  = "third-refresh-token"
	)
	tolerance := auth.ConcurrentRefreshTolerance

	tests := []struct {
		name    string
		prepare func(t *testing.T, s gen.Session)
		token   string
		want    bool
	}{
		{name: "the token a live session just replaced", token: first, want: true},
		{
			name:    "replaced well inside the tolerance",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, tolerance/5) },
			token:   first, want: true,
		},
		{
			name:    "replaced just inside the tolerance",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, tolerance-3*time.Second) },
			token:   first, want: true,
		},
		{
			name:    "replaced just outside the tolerance",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, tolerance+3*time.Second) },
			token:   first,
		},
		{
			name:    "replaced a minute ago: inside the sign-out window, outside the tolerance",
			prepare: func(t *testing.T, s gen.Session) { sr.backdate(t, s.ID, time.Minute) },
			token:   first,
		},
		{
			name: "the replaced token of a session that was revoked, as by a sign-out",
			prepare: func(t *testing.T, s gen.Session) {
				if err := sr.q.RevokeSession(sr.ctx, s.ID); err != nil {
					t.Fatalf("revoke: %v", err)
				}
			},
			token: first,
		},
		{
			name: "the replaced token of a session that has expired",
			prepare: func(t *testing.T, s gen.Session) {
				sr.exec(t, "expire", `UPDATE sessions SET expires_at = now() - interval '1 hour' WHERE id = $1`, s.ID)
			},
			token: first,
		},
		{
			name:    "a token two rotations old",
			prepare: func(t *testing.T, s gen.Session) { sr.mustRotate(t, s.ID, second, third) },
			token:   first,
		},
		{
			name:    "one rotation old after two rotations",
			prepare: func(t *testing.T, s gen.Session) { sr.mustRotate(t, s.ID, second, third) },
			token:   second, want: true,
		},
		{name: "the session's own current token", token: second},
		{name: "a token nobody holds", token: "a-token-nobody-issued"},
		{
			name: "a replaced token whose rotation has no timestamp",
			prepare: func(t *testing.T, s gen.Session) {
				sr.exec(t, "null rotated_at", `UPDATE sessions SET rotated_at = NULL WHERE id = $1`, s.ID)
			},
			token: first,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr.clearSessions(t)
			defer sr.clearSessions(t)

			s := sr.newSession(t, sessRaceUserA, first)
			sr.mustRotate(t, s.ID, first, second)
			if tt.prepare != nil {
				tt.prepare(t, s)
			}

			if got := sr.sm.RefusalSparesCookie(sr.ctx, tt.token); got != tt.want {
				t.Errorf("RefusalSparesCookie(%q) = %t, want %t", tt.token, got, tt.want)
			}
		})
	}
}

// TestRefusedRefreshAfterAConcurrentWinner_AgainstPostgres is the 0-row branch of
// Refresh against real locks: the loser has VALIDATED, the winner rotates and has
// not committed, the loser's rotation waits on the row, the winner commits, and
// the loser's rotation comes back refused. What Refresh does next is ask whether
// that refusal was a race — and here it was, so the cookie is spared (409). The
// same sequence with the session revoked before the loser asks is the sign-out
// race, and is not spared (401, cookie cleared).
//
// The loser's rotation is the production helper, RotateRefreshToken, and the
// session it rotates is the one ValidateRefreshToken returned, so the
// hash it is conditional on is the one a real refresh would carry.
func TestRefusedRefreshAfterAConcurrentWinner_AgainstPostgres(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	const (
		cookie = "the-cookie-both-refreshes-carry"
		winner = "the-winners-new-cookie"
		loser  = "the-losers-would-be-cookie"
	)

	type refusal struct{ err error }
	race := func(t *testing.T) (gen.Session, refusal) {
		t.Helper()
		sr.clearSessions(t)
		s := sr.newSession(t, sessRaceUserA, cookie)
		validated, err := sr.sm.ValidateRefreshToken(sr.ctx, cookie)
		if err != nil {
			t.Fatalf("the loser's validation: %v", err)
		}
		got := lockedPair(t, sr,
			func(q *gen.Queries) error {
				rows, err := sr.rotate(q, s.ID, cookie, winner)
				if err != nil || rows != 1 {
					t.Fatalf("the winner's rotation: rows=%d err=%v, want 1", rows, err)
				}
				return nil
			},
			func(q *gen.Queries) (refusal, error) {
				return refusal{auth.RotateRefreshToken(sr.ctx, q, validated, auth.HashToken(loser), "admin")}, nil
			})
		return s, got
	}

	t.Run("the winner commits: the loser is refused and the refusal spares the cookie", func(t *testing.T) {
		s, got := race(t)
		if !errors.Is(got.err, auth.ErrInvalidToken) {
			t.Fatalf("the loser's rotation = %v, want ErrInvalidToken", got.err)
		}
		if !sr.sm.RefusalSparesCookie(sr.ctx, cookie) {
			t.Error("a refresh that lost to a concurrent winner is not recognised as such; it would clear the winner's cookie")
		}
		if row := sr.reload(t, s.ID); row.TokenHash != auth.HashToken(winner) || row.IsRevoked {
			t.Errorf("the session holds %q revoked=%t, want the winner's hash and live", row.TokenHash, row.IsRevoked)
		}
	})

	t.Run("the session is revoked before the loser asks: the sign-out race, not spared", func(t *testing.T) {
		s, got := race(t)
		if !errors.Is(got.err, auth.ErrInvalidToken) {
			t.Fatalf("the loser's rotation = %v, want ErrInvalidToken", got.err)
		}
		if err := sr.sm.RevokeSession(sr.ctx, s.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if sr.sm.RefusalSparesCookie(sr.ctx, cookie) {
			t.Error("the predecessor of a REVOKED session spares the cookie; a sign-out would leave the browser holding a live-looking cookie")
		}
	})
}

// TestLookupsThatCouldNotBeMade_AgainstPostgres puts the three-outcome rule
// against the error pgx really returns. The unit tests inject an error; this one
// asks Postgres with a context that is already cancelled, so the lookup fails the
// way a request whose client has gone away does — and none of the lookups may
// answer it as "no such session". A failure that read as ErrInvalidToken would,
// in Refresh, clear a good cookie, and in Logout report a sign-out nobody did.
//
// The control is the same calls with a live context, which answer.
func TestLookupsThatCouldNotBeMade_AgainstPostgres(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	s := sr.newSession(t, sessRaceUserA, "first-refresh-token")
	sr.mustRotate(t, s.ID, "first-refresh-token", "second-refresh-token")

	if _, err := sr.sm.ValidateRefreshToken(sr.ctx, "second-refresh-token"); err != nil {
		t.Fatalf("control: ValidateRefreshToken with a live context = %v", err)
	}
	if _, via, err := sr.sm.FindSessionForLogout(sr.ctx, "first-refresh-token"); err != nil || !via {
		t.Fatalf("control: FindSessionForLogout with a live context = %v, %v", via, err)
	}
	if !sr.sm.RefusalSparesCookie(sr.ctx, "first-refresh-token") {
		t.Fatal("control: RefusalSparesCookie with a live context = false")
	}

	cancelled, cancel := context.WithCancel(sr.ctx)
	cancel()

	if _, err := sr.sm.ValidateRefreshToken(cancelled, "second-refresh-token"); err == nil || errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("ValidateRefreshToken with a cancelled context = %v, want an error that is NOT ErrInvalidToken", err)
	}
	if _, _, err := sr.sm.FindSessionForLogout(cancelled, "first-refresh-token"); err == nil || errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("FindSessionForLogout with a cancelled context = %v, want an error that is NOT ErrInvalidToken", err)
	}
	if sr.sm.RefusalSparesCookie(cancelled, "first-refresh-token") {
		t.Error("RefusalSparesCookie with a cancelled context = true; a lookup that failed must refuse as stale (false)")
	}
}

// TestRotateSessionToken_RecordsTheRoleOfTheRefresh pins the write nothing else
// pins: a rotation stores the role it is GIVEN, whatever the session held.
//
// It matters most for the empty role. A session issued before migration 000055
// has an empty user_role — Refresh's role-rotation guard accepts that once, "so
// the upgrade path does not log out every existing user; it gets populated" by
// the first rotation — and a rotation that did not write the role would leave it
// empty for good, so the guard would stay open for that session for ever. Every
// other fixture creates a session whose role equals the one it is rotated with,
// which cannot tell a rotation that writes the role from one that does not.
// A refused rotation writes nothing, the role included.
func TestRotateSessionToken_RecordsTheRoleOfTheRefresh(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	rotateAs := func(id uuid.UUID, role string) int64 {
		t.Helper()
		rows, err := sr.q.RotateSessionToken(sr.ctx, gen.RotateSessionTokenParams{
			ID:           id,
			OldTokenHash: auth.HashToken("old-refresh-token"),
			NewTokenHash: auth.HashToken("new-refresh-token"),
			UserRole:     role,
		})
		if err != nil {
			t.Fatalf("RotateSessionToken: %v", err)
		}
		return rows
	}

	for _, tt := range []struct{ name, created, rotated string }{
		{"a session issued before the role was recorded", "", "viewer"},
		{"a session whose role the rotation changes", "admin", "viewer"},
		{"the same, the other way round", "viewer", "admin"},
		{"a role that stays the same", "admin", "admin"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sr.clearSessions(t)
			s := sr.newSessionWithRole(t, sessRaceUserA, "old-refresh-token", tt.created)

			if rows := rotateAs(s.ID, tt.rotated); rows != 1 {
				t.Fatalf("rotation changed %d rows, want 1", rows)
			}
			if got := sr.reload(t, s.ID).UserRole; got != tt.rotated {
				t.Errorf("user_role = %q after a rotation carrying %q (created as %q)", got, tt.rotated, tt.created)
			}
		})
	}

	t.Run("a refused rotation writes no role", func(t *testing.T) {
		sr.clearSessions(t)
		s := sr.newSessionWithRole(t, sessRaceUserA, "old-refresh-token", "")
		if err := sr.q.RevokeSession(sr.ctx, s.ID); err != nil {
			t.Fatalf("revoke: %v", err)
		}

		if rows := rotateAs(s.ID, "viewer"); rows != 0 {
			t.Fatalf("a revoked session's rotation changed %d rows, want 0", rows)
		}
		if got := sr.reload(t, s.ID).UserRole; got != "" {
			t.Errorf("user_role = %q after a refused rotation, want it untouched (empty)", got)
		}
	})
}

// TestRotateSessionToken_StampsTheMomentOfTheUpdate pins which clock rotated_at
// carries. now() is the START of the transaction, and Refresh runs the rotation a
// few statements after it begins; a transaction that had waited — for a pool
// connection, for a lock — would date the rotation by that wait, and the windows
// that rotated_at bounds are only seconds long. rotated_at is therefore stamped
// with clock_timestamp(), the database clock as the UPDATE runs.
//
// The transaction here begins, lets a second and a half pass, and then rotates:
// the stamp must be about that much AFTER the transaction's own start. A stamp
// of now() would be equal to it.
func TestRotateSessionToken_StampsTheMomentOfTheUpdate(t *testing.T) {
	env := setupMigration(t)
	defer env.Cleanup()
	sr, purge := newSessionRace(t, env)
	defer purge()
	sr.clearSessions(t)
	defer sr.clearSessions(t)

	s := sr.newSession(t, sessRaceUserA, "old-refresh-token")

	tx, err := sr.pool.Begin(sr.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	committed := false
	defer func() {
		if !committed {
			rbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = tx.Rollback(rbCtx)
		}
	}()

	var txStart time.Time
	if err := tx.QueryRow(sr.ctx, `SELECT now()`).Scan(&txStart); err != nil {
		t.Fatalf("read the transaction's start: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)

	rows, err := sr.rotate(sr.q.WithTx(tx), s.ID, "old-refresh-token", "new-refresh-token")
	if err != nil || rows != 1 {
		t.Fatalf("rotation: rows=%d err=%v, want 1", rows, err)
	}
	if err := tx.Commit(sr.ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	committed = true

	after := sr.reload(t, s.ID)
	if !after.RotatedAt.Valid {
		t.Fatal("rotated_at is NULL after a rotation")
	}
	if lag := after.RotatedAt.Time.Sub(txStart); lag < time.Second {
		t.Errorf("rotated_at is %v after the transaction began, want about 1.5s: it carries the transaction's "+
			"start instead of the moment of the UPDATE, so a transaction that waited would backdate the rotation", lag)
	}
}
