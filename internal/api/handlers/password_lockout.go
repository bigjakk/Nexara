package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The per-user lockout of POST /auth/change-password.
//
// The route checks the caller's CURRENT password, which makes it a second oracle
// for the secret login guards, and one that anybody holding a bearer token for
// the account can reach: a stolen access token never meets login's defences. A
// right guess does not only answer "yes" — it sets the attacker's own password
// and ends every session of the owner. The per-IP cap on the route
// (authLimitedPaths) bounds what one address can try; this bounds what ALL
// addresses together can try against one account.
//
// The rule: passwordLockoutThreshold wrong current passwords within
// passwordLockoutWindow lock the account out of this ONE route for
// passwordLockoutDuration. While it is locked every request — the right password
// included — answers 429 with a Retry-After and changes nothing. Nothing else is
// locked: login, the sessions and every other route read none of this state, and
// its keys (pwchange:user:*) are its own, apart from the TOTP lockout's. Nothing
// that ends a session — logout-all, the revoke of one session, any revoke-all —
// clears it either: only ChangePassword touches the state, a change that commits
// (clear) or a request handing back its own attempt (release), and
// TestGuard_OnlyChangePasswordTouchesTheLockout holds the keys to this file and the
// calls to that one handler.
//
// The window is AT LEAST as long as the lock, and that is not a detail. Were it
// shorter, an attacker could stay under the threshold — four guesses a window,
// window after window — and out-guess one who trips the lock: with a 15-minute
// window and a 30-minute lock that is 384 guesses a day with no lock, no audit row
// and no log line, against 240 for tripping it. With a window of an hour and a
// lock of half of one, staying under is 96 a day and tripping it is 240, so the
// audited lock cycle — five guesses per half hour, from every address together —
// is the ceiling, and every wrong guess leaves a warning in the log besides
// (logWrongPassword). The lock is also longer than the TOTP lockout's five
// minutes, because nobody is waiting on this action: a user who mistypes five
// times has a rare, unhurried thing to try again later. The two durations are
// different numbers on purpose, so that a mix-up of them is a change the tests see.
// TestPasswordLockoutTheLockCycleIsTheCeilingOnGuesses holds the ceiling by
// simulation, whatever the constants become.
//
// An attempt is counted BEFORE its password is checked, not after the check
// fails. Counting afterwards leaves a gap as wide as a bcrypt: requests that
// arrive together all find the count below the threshold, are all checked, and
// all fail, so fifteen guesses in flight cost the attacker one lockout. Counting
// first makes a guess checkable only once it has been admitted, and the counter
// hands each number out once, so at most passwordLockoutThreshold guesses are
// checked per lock cycle however the requests interleave.
//
// The price is that a RIGHT password has spent an attempt too, and what becomes of
// it depends on what the request then did. A change that COMMITS makes the secret
// new, so every count against the old one is moot and all of it is cleared
// (clear). A request that verified the password and went no further — the new one
// was too weak, or the database failed — changed nothing: the guesses already
// counted are still guesses against the SAME secret, so it gives back only the one
// attempt it took (release), and if that was the attempt that armed the lock it
// undoes exactly that. Clearing everything there would hand whoever holds counted
// guesses a fresh budget against a password that has not changed, each time the
// owner mistypes a new one.
const (
	passwordLockoutThreshold = 5
	passwordLockoutWindow    = 60 * time.Minute
	passwordLockoutDuration  = 30 * time.Minute
)

// passwordLockoutUnavailableMessage is the 503 for an attempt that could not be
// counted. It says the password was NOT changed, like every other refusal of this
// route that is not the user's doing, and that it is worth retrying.
const passwordLockoutUnavailableMessage = "Changing the password is unavailable right now because its attempt limit could not be checked, " +
	"and your password was NOT changed; please try again shortly."

// lockoutAdmission is what counting an attempt decided. The zero value is the
// refusal, so a reservation that was never filled in cannot be mistaken for
// permission to check a password.
type lockoutAdmission int

const (
	// lockoutRefused: the account is locked. The password is not checked.
	lockoutRefused lockoutAdmission = iota
	// lockoutAdmitted: the password may be checked, and more attempts are left.
	lockoutAdmitted
	// lockoutAdmittedLast: the password may be checked, and it is the last attempt
	// of the budget. The lock is already in place; it holds if this one is wrong
	// and is lifted (clear, or release) if it is right.
	lockoutAdmittedLast
)

// lockoutReservation is the answer to counting one attempt.
type lockoutReservation struct {
	admission lockoutAdmission
	// attempts is the number of attempts counted, this one included, when it was
	// admitted.
	attempts int
	// retryAfter is how long the account stays locked: what is left of the lock
	// when this attempt was refused, the whole lock when it armed it.
	retryAfter time.Duration
	// token names the lock this attempt armed, when it armed one (lockoutAdmittedLast):
	// it is what the lock's key holds, so that release can take back THIS lock and
	// not one a later attempt armed.
	token string
}

// passwordLockoutStore is where the attempt counts live. Redis holds them in
// production, so that every replica counts the same attempts and a restart does
// not hand an attacker a fresh budget; process memory holds them for a handler
// that has no Redis (see lockoutStore).
type passwordLockoutStore interface {
	// reserve counts one attempt at the account's current password, atomically, and
	// says whether it may be checked. An error means the attempt could NOT be
	// counted, and the caller must not check the password.
	reserve(ctx context.Context, userID uuid.UUID) (lockoutReservation, error)
	// release gives back the one attempt res reserved, because its password was
	// verified and the change did not commit: the count falls by one, never below
	// zero, and if res armed the lock — and the lock is still the one it armed —
	// the lock is lifted and the count is what it was before res: one short of the
	// threshold. It never touches the attempts of anyone else, which are guesses
	// against a secret that has not changed. A refused reservation took nothing and
	// has nothing to give back.
	release(ctx context.Context, userID uuid.UUID, res lockoutReservation) error
	// clear forgets the account's attempts and any lock: a change COMMITTED, so the
	// secret the counted guesses were aimed at is gone.
	clear(ctx context.Context, userID uuid.UUID) error
}

// SetPasswordLockoutStore makes Redis the home of the change-password attempt
// counts. A nil client changes nothing: a handler that was never given one keeps
// counting in the memory of the process (lockoutStore).
//
// It is a setter, unlike eventPub (see NewAuthHandler), because forgetting it
// does not silently drop what it is for: an unwired handler still has a lockout,
// local to the process, and the test of registerAuth's wiring is the one that
// notices the counts are not in Redis.
func (h *AuthHandler) SetPasswordLockoutStore(rdb *redis.Client) {
	if rdb == nil {
		return
	}
	h.passwordLockout = redisPasswordLockout{rdb: rdb}
}

// processPasswordLockout is the counts of every handler without a Redis client:
// one process, one set of counts.
var processPasswordLockout = newMemoryPasswordLockout(time.Now)

// lockoutStore is the store this handler counts in: Redis when it has been wired
// (SetPasswordLockoutStore), the process's own memory when it has not.
//
// An unwired handler is a test that built an AuthHandler by hand — in production
// sessions need Redis, so the handler cannot exist without one — and it must not
// quietly have no lockout. It counts locally instead, with the same rule. What a
// handler that HAS a Redis does when Redis cannot be reached is a different
// question, and the answer is the opposite: see admitPasswordAttempt.
//
// The fallback is not silent, though. Counts in the memory of one process are not
// shared with the other replicas and start from nothing on every restart, which a
// deployment that reached this by a refactor — a constructor that stopped passing
// the Redis client on — would not otherwise find out until somebody guessed at it.
// The first count a handler makes in memory says so, once, at Warn.
func (h *AuthHandler) lockoutStore() passwordLockoutStore {
	if h.passwordLockout != nil {
		return h.passwordLockout
	}
	h.memoryLockoutWarning.Do(func() {
		slog.Warn("change password: this handler has no Redis client, so the failed-attempt counts of the route are kept in the memory of this process only: " +
			"they are not shared with other replicas and are lost on restart")
	})
	return processPasswordLockout
}

// passwordFailKey is the Redis key holding an account's attempts in the current
// window, and passwordLockKey the one whose presence is the lock. They are
// pwchange:user:* — apart from totp:user:*, which login's second factor reads, so
// that locking one cannot lock the other.
func passwordFailKey(userID uuid.UUID) string { return "pwchange:user:fail:" + userID.String() }
func passwordLockKey(userID uuid.UUID) string { return "pwchange:user:lock:" + userID.String() }

// reservePasswordAttempt counts one attempt, and arms the lock when it is the
// last of the budget, in ONE script, so that nothing can come between the count
// and the lock it may arm.
//
// KEYS: the attempt counter, the lock. ARGV: the window and the lock duration in
// milliseconds, the threshold, and a token naming the lock this attempt would arm.
// It answers {admission, attempts, wait in milliseconds}: 0 admitted, 1 admitted
// and last (the lock is armed now, for the whole duration, holding the token), 2
// refused because the account is locked (what is left of it).
//
// The counter expires with its window, set in the same script as the increment —
// the TOTP lockout does an INCR and then, separately, an EXPIRE, and a failed
// EXPIRE leaves a counter that never goes away. Here a counter that has somehow
// lost its expiry gets one back on the next attempt. Reaching the threshold
// deletes the counter, so the budget starts afresh the moment the lock ends,
// whichever part of the window was left.
//
// A refused attempt is not counted and does not lengthen the lock: an attacker
// who hammers a locked account does not keep it locked. A lock that has no expiry
// (PTTL -1: nothing this script writes, but a PERSIST or a hand-made SET could) is
// healed the way the counter's is — given the whole duration, and answered as
// locked — so that it can become neither permanent nor ignored.
var reservePasswordAttempt = redis.NewScript(`
local wait = redis.call('PTTL', KEYS[2])
if wait > 0 then
  return {2, 0, wait}
end
if wait == -1 then
  redis.call('PEXPIRE', KEYS[2], ARGV[2])
  return {2, 0, tonumber(ARGV[2])}
end
local attempts = redis.call('INCR', KEYS[1])
if attempts == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
if attempts >= tonumber(ARGV[3]) then
  redis.call('SET', KEYS[2], ARGV[4], 'PX', ARGV[2])
  redis.call('DEL', KEYS[1])
  return {1, attempts, tonumber(ARGV[2])}
end
return {0, attempts, 0}
`)

// releasePasswordAttempt gives one attempt back, atomically.
//
// KEYS: the attempt counter, the lock. ARGV: the window in milliseconds, the
// threshold, and the token of the lock the attempt armed — empty for an attempt
// that armed none.
//
// With a token: if the lock is still the one that token names, lift it and put the
// counter back at one short of the threshold, which is where it stood before the
// attempt that armed it, with a whole window (the expiry the counter had when it was
// deleted is gone; a longer one keeps the counted guesses counted for longer, which
// is the safe way to be wrong). A lock that is gone, or is another attempt's, is
// left alone.
//
// Without one: take one off the counter, and delete it when that leaves nothing. A
// counter that is already gone — its window ended, or the lock replaced it — is made
// by DECR at minus one and deleted again by the same script, so a release can neither
// leave a negative count behind, which would be a budget of six, nor bring back a
// window that has ended. (It can, at a window boundary, take one attempt from the next
// window: the request that holds the right password was in flight across it, and the
// window is an hour while a request is under a minute.)
var releasePasswordAttempt = redis.NewScript(`
if ARGV[3] ~= '' then
  if redis.call('GET', KEYS[2]) == ARGV[3] then
    redis.call('DEL', KEYS[2])
    redis.call('SET', KEYS[1], tonumber(ARGV[2]) - 1, 'PX', ARGV[1])
  end
  return 1
end
if redis.call('DECR', KEYS[1]) <= 0 then
  redis.call('DEL', KEYS[1])
end
return 1
`)

// boundedByContext runs call and stops WAITING for it when ctx ends, answering the
// context's error, whatever the client does.
//
// It exists because the bound cannot be left to the Redis client. The one production
// builds (pkg/redisutil) comes from redis.ParseURL with its defaults: a context's
// deadline does not reach the socket (ContextTimeoutEnabled is off), so a read waits
// for ReadTimeout — five seconds — and a failed one is tried again (MaxRetries is
// three). A Redis that accepts the connection and never answers would hold a request
// for as long as twenty seconds, past every bound this route documents: the deciding
// 15 and the follow-up 5. So the call runs on a goroutine of its own and the caller
// waits for whichever comes first, its answer or the end of its context.
//
// The call that is given up on is not cancelled. It ends when the client's own read
// timeout ends it — the context that ended has already stopped the retries — and its
// answer goes into a channel nobody reads, which is buffered so that it never blocks.
// But it can still take effect in Redis afterwards, and what that costs depends on
// the call:
//
//   - A release or a clear that lands late is one that was owed anyway.
//   - A count that lands after its request was answered 503 is an attempt nobody was
//     told about: one too many on the account and, when it is the attempt that
//     reaches the threshold, a LOCK ARMED WITH NO AUDIT ROW AND NO LOG LINE OF ITS
//     OWN. The next request is then answered 429 "too many incorrect attempts" for an
//     account whose owner made none. It takes a Redis that stalls past the deciding
//     bound while the count stands one short of the threshold, and it errs on the
//     side of locking; the 503 says so in the log (passwordLockoutUnavailable). Nothing
//     tracks a count that is still in flight — a marker for it would live in the
//     Redis that is not answering — so this is a documented residual, and
//     TestChangePassword_AnAbandonedCountThatLandsLaterCanArmTheLockUnrecorded pins it.
//
// A panic in the call is recovered into an error: a goroutine nothing is waiting on
// must not take the process down.
func boundedByContext[T any](ctx context.Context, call func(context.Context) (T, error)) (T, error) {
	type outcome struct {
		value T
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{err: fmt.Errorf("the Redis call panicked: %v", r)}
			}
		}()
		value, err := call(ctx)
		done <- outcome{value: value, err: err}
	}()
	select {
	case o := <-done:
		return o.value, o.err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("the Redis call did not answer in time: %w", ctx.Err())
	}
}

// redisPasswordLockout is the lockout kept in Redis. Every call is bounded by its
// context's end (boundedByContext), not by what the client would wait.
type redisPasswordLockout struct {
	rdb *redis.Client
}

// reserve counts one attempt with the script above, and inherits what go-redis does
// to a command whose reply is lost: with the defaults pkg/redisutil leaves (three
// retries) a script that ran on the server and whose reply never arrived is sent
// again, up to four sends in all. Three things follow, all on the side of locking
// and none of them an extra guess for anybody.
//
//   - One guess can be counted up to FOUR times, which costs the account attempts.
//   - When the attempt that armed the lock is the one retried, a later send finds
//     the lock already in place and answers "locked": the request is refused 429 as
//     the lock says and its password is not checked, but no audit row is written for
//     the arming. A lost reply costs the record of the lock, never the lock.
//   - A count that lands after the request that made it was answered 503 can arm the
//     lock the same way, unrecorded (see boundedByContext).
//
// (Releasing is idempotent for the attempt that armed a lock, which it names, and
// can give back one attempt too many for an ordinary one — a request that holds the
// right password.)
func (r redisPasswordLockout) reserve(ctx context.Context, userID uuid.UUID) (lockoutReservation, error) {
	token := uuid.NewString()
	vals, err := boundedByContext(ctx, func(ctx context.Context) ([]int64, error) {
		return reservePasswordAttempt.Run(ctx, r.rdb,
			[]string{passwordFailKey(userID), passwordLockKey(userID)},
			passwordLockoutWindow.Milliseconds(), passwordLockoutDuration.Milliseconds(), passwordLockoutThreshold, token,
		).Int64Slice()
	})
	if err != nil {
		return lockoutReservation{}, fmt.Errorf("count a password attempt: %w", err)
	}
	if len(vals) != 3 {
		return lockoutReservation{}, fmt.Errorf("count a password attempt: the script answered %d values, want 3", len(vals))
	}
	res := lockoutReservation{attempts: int(vals[1]), retryAfter: time.Duration(vals[2]) * time.Millisecond}
	switch vals[0] {
	case 0:
		res.admission = lockoutAdmitted
	case 1:
		res.admission = lockoutAdmittedLast
		res.token = token
	case 2:
		res.admission = lockoutRefused
	default:
		return lockoutReservation{}, fmt.Errorf("count a password attempt: the script answered admission %d", vals[0])
	}
	return res, nil
}

func (r redisPasswordLockout) release(ctx context.Context, userID uuid.UUID, res lockoutReservation) error {
	if res.admission == lockoutRefused {
		return nil
	}
	token := ""
	if res.admission == lockoutAdmittedLast {
		token = res.token
	}
	_, err := boundedByContext(ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, releasePasswordAttempt.Run(ctx, r.rdb,
			[]string{passwordFailKey(userID), passwordLockKey(userID)},
			passwordLockoutWindow.Milliseconds(), passwordLockoutThreshold, token,
		).Err()
	})
	if err != nil {
		return fmt.Errorf("give back a password attempt: %w", err)
	}
	return nil
}

func (r redisPasswordLockout) clear(ctx context.Context, userID uuid.UUID) error {
	_, err := boundedByContext(ctx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, r.rdb.Del(ctx, passwordFailKey(userID), passwordLockKey(userID)).Err()
	})
	if err != nil {
		return fmt.Errorf("clear the password attempts: %w", err)
	}
	return nil
}

// memoryPasswordLockout is the same lockout kept in the memory of one process.
// It makes the decisions the script makes, in the same order, and a test pins
// that the two agree (TestPasswordLockoutStoresAgree).
type memoryPasswordLockout struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[uuid.UUID]*memoryLockoutEntry
}

type memoryLockoutEntry struct {
	attempts    int
	windowEnds  time.Time // when the attempts are forgotten
	lockedUntil time.Time
	lockToken   string // names the attempt that armed the lock
}

func newMemoryPasswordLockout(now func() time.Time) *memoryPasswordLockout {
	return &memoryPasswordLockout{now: now, entries: map[uuid.UUID]*memoryLockoutEntry{}}
}

func (m *memoryPasswordLockout) reserve(_ context.Context, userID uuid.UUID) (lockoutReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	m.sweep(now)

	e := m.entries[userID]
	if e != nil && e.lockedUntil.After(now) {
		return lockoutReservation{admission: lockoutRefused, retryAfter: e.lockedUntil.Sub(now)}, nil
	}
	if e == nil || !e.windowEnds.After(now) {
		e = &memoryLockoutEntry{windowEnds: now.Add(passwordLockoutWindow)}
		m.entries[userID] = e
	}
	e.attempts++
	if e.attempts >= passwordLockoutThreshold {
		attempts := e.attempts
		token := uuid.NewString()
		e.lockedUntil, e.lockToken = now.Add(passwordLockoutDuration), token
		e.attempts, e.windowEnds = 0, time.Time{}
		return lockoutReservation{admission: lockoutAdmittedLast, attempts: attempts, retryAfter: passwordLockoutDuration, token: token}, nil
	}
	return lockoutReservation{admission: lockoutAdmitted, attempts: e.attempts}, nil
}

func (m *memoryPasswordLockout) release(_ context.Context, userID uuid.UUID, res lockoutReservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	e := m.entries[userID]
	if e == nil {
		return nil
	}
	switch res.admission {
	case lockoutAdmittedLast:
		if e.lockedUntil.After(now) && e.lockToken == res.token {
			e.lockedUntil, e.lockToken = time.Time{}, ""
			e.attempts, e.windowEnds = passwordLockoutThreshold-1, now.Add(passwordLockoutWindow)
		}
	case lockoutAdmitted:
		if e.attempts > 0 && e.windowEnds.After(now) {
			e.attempts--
			if e.attempts == 0 && !e.lockedUntil.After(now) {
				delete(m.entries, userID)
			}
		}
	}
	return nil
}

func (m *memoryPasswordLockout) clear(_ context.Context, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, userID)
	return nil
}

// sweep drops the accounts whose attempts and lock have both run out, so the map
// holds only accounts with something to remember. The caller holds the lock.
func (m *memoryPasswordLockout) sweep(now time.Time) {
	for id, e := range m.entries {
		if !e.windowEnds.After(now) && !e.lockedUntil.After(now) {
			delete(m.entries, id)
		}
	}
}

// admitPasswordAttempt is the first half of ChangePassword: the read of the
// account, and the lockout's count of this attempt, under ONE deciding bound. It
// answers with the account and the admitted attempt, or with the refusal the
// request ends in.
//
// The lock is read BEFORE the password is checked, and counting the attempt is
// what reads it. Consulting it after the check would let the right password
// through a lock that exists to stop guessing, and would make a locked request
// cost a bcrypt, so that the lock bounds the guesses and not the work.
//
// An attempt that cannot be counted is refused, 503, and the password is not
// checked — for the lock and for the count alike, because they are one call. The
// TOTP lockout fails the other way, open, with the per-IP cap as its backstop, and
// that is right for it: a login that fails when Redis does locks everybody out of
// everything. Here a failure costs one retry of a rare action the user is not
// waiting on, and still being signed in, while the alternative is a password
// oracle that anybody holding a stolen token can reach whenever Redis is down —
// or can make it down, by filling the instance with the state the unauthenticated
// OIDC routes write. A password that is checked must be a password that was
// counted. A Redis that does not answer at all is the same failure, and it is
// answered when the deciding bound ends and not when the client gives up
// (boundedByContext).
func (h *AuthHandler) admitPasswordAttempt(c fiber.Ctx, userID uuid.UUID) (user db.User, attempt lockoutReservation, err error) {
	ctx, cancel := h.dbContext(c)
	defer cancel()

	user, err = h.queries.GetUserByID(ctx, userID)
	if err != nil {
		return db.User{}, lockoutReservation{}, passwordNotChanged("read the user", userID, err)
	}
	if user.AuthSource != "local" {
		return db.User{}, lockoutReservation{}, fiber.NewError(fiber.StatusForbidden, "Password is managed by your identity provider")
	}

	attempt, err = h.lockoutStore().reserve(ctx, userID)
	if err != nil {
		return db.User{}, lockoutReservation{}, passwordLockoutUnavailable(userID, err)
	}
	switch attempt.admission {
	case lockoutAdmitted, lockoutAdmittedLast:
		return user, attempt, nil
	default: // lockoutRefused, and anything this code does not know
		return db.User{}, lockoutReservation{}, passwordLocked(c, attempt.retryAfter)
	}
}

// logWrongPassword leaves a line for EVERY wrong current password that was checked,
// at Warn: who, from where, and which attempt of the budget it was. The lock leaves
// an audit row and a line of its own, but the attempts below it would otherwise
// leave nothing, and the guesses made under the threshold are the ones nobody would
// otherwise see. Never the password — not the one that was tried, which is often the
// right one for another account, and not the one it would have been changed to.
func logWrongPassword(c fiber.Ctx, userID uuid.UUID, attempt lockoutReservation) {
	slog.Warn("change password: wrong current password",
		"user_id", userID, "ip", c.IP(), "attempt", attempt.attempts, "attempts_before_lock", passwordLockoutThreshold)
}

// passwordLockoutRecord is what the lockout leaves behind when it locks: the
// details of its audit row, and a warning in the log. ChangePassword writes the row
// itself — a lockout is the second thing that request can record, next to
// password_changed, and both are one user's credential events, install-global
// (audit_cluster_guard_test.go) — and it is written only by the wrong current
// password that was the last of the budget. A refused attempt on a locked account
// records nothing, so one lockout is one row, and an attempt that could not be
// counted is never a lockout.
//
// The row says that it happened, from where, how many attempts and how long it
// lasts, and never the password that was tried: audit details are readable by
// every Viewer, and a wrong password is often a right one for another account.
func passwordLockoutRecord(c fiber.Ctx, userID uuid.UUID, attempt lockoutReservation) json.RawMessage {
	lockSeconds := int(attempt.retryAfter / time.Second)
	slog.Warn("change password: the account is locked out of changing its password after repeated wrong current passwords",
		"user_id", userID, "ip", c.IP(), "failed_attempts", attempt.attempts, "lockout_seconds", lockSeconds)

	details, _ := json.Marshal(map[string]any{
		"ip":              c.IP(),
		"failed_attempts": attempt.attempts,
		"lockout_seconds": lockSeconds,
	})
	return details
}

// forgetPasswordFailures clears every count and any lock of the account, because
// the change COMMITTED: the secret the counted guesses were aimed at is gone, and
// whoever was guessing it has nothing left to guess. It is NOT what a request that
// merely verified the password does — that gives back its own attempt
// (handBackAttempt), because its guesses are still aimed at a secret that has not
// changed.
//
// It is best effort and runs in the follow-up that records the change, under that
// follow-up's deadline: the change is made, and a Redis that cannot be reached now
// leaves the counts where they were — conservative: the next wrong guess is counted
// on top of them — so it must not fail the request that earned the clear.
func (h *AuthHandler) forgetPasswordFailures(ctx context.Context, userID uuid.UUID) {
	if err := h.lockoutStore().clear(ctx, userID); err != nil {
		slog.Warn("change password: could not clear the failed-attempt count after the password was changed; it stays until it runs out "+
			"(and so does the lock, when this very attempt was the one that armed it)",
			"user_id", userID, "error", err)
	}
}

// handBackAttempt gives back the one attempt a request took when its password
// verified and its change did not commit — the new password was refused, the
// database failed, another request got there first: the password was right, so it
// was not a guess, and the request has nothing to show for the attempt it spent.
// Only that attempt: see the store's release for why the others stay.
//
// It is best effort, and it runs after the answer is decided, under a deadline of its
// own (authFollowUpTimeout) like every step that enforces or records a decision, so
// it adds at most that to the longest path through the handler, which is a change
// that commits.
func (h *AuthHandler) handBackAttempt(c fiber.Ctx, userID uuid.UUID, attempt lockoutReservation) {
	ctx, cancel := h.followUp(c.Context())
	defer cancel()
	if err := h.lockoutStore().release(ctx, userID, attempt); err != nil {
		slog.Warn("change password: could not give back the attempt a verified password took; it stays counted until its window ends",
			"user_id", userID, "error", err)
	}
}

// checkCurrentPassword is auth.CheckPassword, behind a seam that exists for one
// test: it counts the passwords that were CHECKED, which is the only way to tell a
// lockout that counts before it checks from one that checks and then counts — both
// answer a burst of wrong guesses the same way, and only the second has looked at
// all of them.
func (h *AuthHandler) checkCurrentPassword(hash, password string) error {
	if h.passwordChecker != nil {
		return h.passwordChecker(hash, password)
	}
	return auth.CheckPassword(hash, password)
}

// passwordLocked answers a request the lock refuses: 429, the wait in a
// Retry-After header, rounded UP to whole seconds so a client that waits that
// long is not refused again, and in the message, which is what the SPA's form
// shows.
func passwordLocked(c fiber.Ctx, wait time.Duration) error {
	seconds := int((wait + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	c.Set(fiber.HeaderRetryAfter, strconv.Itoa(seconds))
	return fiber.NewError(fiber.StatusTooManyRequests, passwordLockedMessage(wait))
}

// passwordLockedMessage says how long the lock has left, in whole minutes rounded
// up, and that nothing else is affected: a user who has just been told to wait is
// also wondering whether they are still signed in.
func passwordLockedMessage(wait time.Duration) string {
	minutes := int((wait + time.Minute - 1) / time.Minute)
	if minutes < 1 {
		minutes = 1
	}
	unit := "minutes"
	if minutes == 1 {
		unit = "minute"
	}
	return fmt.Sprintf("Too many incorrect attempts at the current password. Changing the password is locked for %d more %s; "+
		"you stay signed in and nothing else about the account is affected.", minutes, unit)
}

// passwordLockoutUnavailable answers an attempt that could not be counted: 503,
// nothing checked, nothing changed. The error is logged — it names a Redis
// failure and never a password, which the lockout never sees.
//
// The line also says what the 503 cannot: that the count may have been made
// anyway. A call that was only given up on (the deciding bound ended, or the reply
// was lost) can still land in Redis, and if it is the attempt that reaches the
// threshold it arms the lock with no audit row, and the next request is answered
// "locked" for an account whose owner was told nothing about an attempt (see
// boundedByContext). It is the only trace such a lock leaves.
func passwordLockoutUnavailable(userID uuid.UUID, err error) error {
	slog.Warn("change password: the failed-attempt counter could not be reached; the current password was not checked and the password was not changed. "+
		"The attempt may still have been counted: a call that was only given up on can land later, and if it is the one that reaches the threshold "+
		"the lock is armed with no audit row",
		"user_id", userID, "error", err,
		"abandoned", errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled))
	return fiber.NewError(fiber.StatusServiceUnavailable, passwordLockoutUnavailableMessage)
}
