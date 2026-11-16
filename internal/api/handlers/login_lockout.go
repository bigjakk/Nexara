package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The per-account lockout of POST /auth/login. The per-address limiter
// (authLimitedPaths, 15 a minute) bounds what one address can try; this bounds what
// every address together can try against one account: loginLockoutThreshold failed
// sign-ins within loginLockoutWindow lock signing in with a password for
// loginLockoutDuration, and while it lasts every attempt — the right password
// included — is answered 429 with a Retry-After, before the account is looked up.
//
// It counts in the stores and scripts of the current-password lockout
// (password_lockout.go): an attempt is counted, atomically with the lock it may arm,
// BEFORE its password is checked, so requests in flight together cannot all be
// checked under one count. The figures sit beside that lockout's: the window is as
// long as the lock, which is what keeps tripping the lock the fastest way to guess
// (see passwordLockoutWindow), and the threshold is twice as high, because a sign-in
// is something a person does every day, from devices that remember old passwords.
//
// What is counted is the identifier typed, not the account it names: an identifier
// that matches no account is counted, locked and answered exactly as one that does,
// so the lockout is no oracle for which accounts exist. Local and directory accounts,
// single-sign-on accounts that cannot sign in with a password and deactivated ones
// all count alike. The identifier is never kept as typed: it is often a password
// typed into the wrong box, so the store holds a keyed digest of it (loginSubject).
// A sign-in that succeeds clears the count.
//
// It fails OPEN, unlike the current-password lockout: a Redis that cannot be reached
// must not lock everybody out of everything, and the per-address limiter stays in
// force. Such an attempt is logged at Warn, and its sign-in goes on uncounted.
//
// Its price is the one every per-account lockout has: anyone who knows an address
// can keep that account from signing in with a password, ten guesses every quarter
// of an hour. The lock and its release are audited for an account that exists.
const (
	loginLockoutThreshold = 10
	loginLockoutWindow    = 15 * time.Minute
	loginLockoutDuration  = 15 * time.Minute

	// loginLockoutWait bounds each Redis call the lockout makes for one sign-in,
	// whatever the client would wait (boundedByContext): a Redis that never answers
	// costs a sign-in this, not the client's read timeout tried again.
	loginLockoutWait = time.Second

	// loginLockRecordTTL is how long the record that an account was locked is kept:
	// the first attempt after the lock ended, within it, audits the release.
	loginLockRecordTTL = 24 * time.Hour
)

var loginLockoutPolicy = lockoutPolicy{
	threshold: loginLockoutThreshold,
	window:    loginLockoutWindow,
	duration:  loginLockoutDuration,
	failKey:   loginFailKey,
	lockKey:   loginLockKey,
}

func loginFailKey(subject uuid.UUID) string { return "login:fail:" + subject.String() }
func loginLockKey(subject uuid.UUID) string { return "login:lock:" + subject.String() }

// loginLockedKey holds the id of an account whose sign-in was locked, until the
// release is audited or loginLockRecordTTL runs out.
func loginLockedKey(subject uuid.UUID) string { return "login:locked:" + subject.String() }

// loginAttempt is one sign-in's place in the lockout.
type loginAttempt struct {
	subject uuid.UUID
	// counted is false when the counter could not be reached: the lockout is not
	// enforced for this attempt, and nothing about it is recorded there.
	counted bool
	res     lockoutReservation
}

// loginSubject is what the lockout counts an identifier under: a keyed digest of it
// (JWTService.Digest), so that nothing in Redis can be read back, or guessed
// offline, as what was typed. Surrounding spaces, case and compatibility forms are
// ignored (NFKC, then Unicode case folding, which also folds what a directory's
// matching folds and Go's ToLower does not, such as fullwidth letters), so that
// spelling an address differently is not a fresh budget. A handler built without a
// JWT service (only in tests: production builds none without one) digests unkeyed.
func (h *AuthHandler) loginSubject(identifier string) uuid.UUID {
	folded := cases.Fold().String(norm.NFKC.String(strings.TrimSpace(identifier)))
	var d [32]byte
	if h.jwtService != nil {
		d = h.jwtService.Digest("login lockout", folded)
	} else {
		d = sha256.Sum256([]byte("login lockout: " + folded))
	}
	return uuid.UUID(d[:16])
}

// loginLockoutStore is where sign-in attempts are counted: Redis when wired
// (SetPasswordLockoutStore), else this handler's own memory, which says so once.
// The memory is the handler's and not the process's, unlike the current-password
// lockout's, because sign-ins name accounts by address, and handlers that share an
// address must not share a count.
func (h *AuthHandler) loginLockoutStore() passwordLockoutStore {
	if h.loginLockout != nil {
		return h.loginLockout
	}
	h.loginMemoryOnce.Do(func() {
		h.loginMemory = newMemoryPasswordLockout(loginLockoutPolicy, time.Now)
		slog.Warn("sign-in: this handler has no Redis client, so failed sign-ins are counted in the memory of this process only")
	})
	return h.loginMemory
}

// admitLoginAttempt counts this sign-in against the identifier it names, before the
// account is looked up. A locked identifier is answered 429 here, and nothing else
// about the request is looked at. A counter that cannot be reached admits the
// attempt uncounted (see the note at the top of this file).
func (h *AuthHandler) admitLoginAttempt(c fiber.Ctx, identifier string) (loginAttempt, error) {
	a := loginAttempt{subject: h.loginSubject(identifier)}
	ctx, cancel := context.WithTimeout(c.Context(), loginLockoutWait)
	defer cancel()
	res, err := h.loginLockoutStore().reserve(ctx, a.subject)
	if err != nil {
		slog.Warn("sign-in: the failed-attempt counter could not be reached; the per-account lockout is not enforced for this attempt",
			"ip", c.IP(), "error", err)
		return a, nil
	}
	switch res.admission {
	case lockoutAdmitted, lockoutAdmittedLast:
		a.counted, a.res = true, res
		if res.attempts == 1 {
			h.recordLoginLockRelease(c, a.subject)
		}
		return a, nil
	default: // lockoutRefused, and anything this code does not know
		return a, lockedFor(c, res.retryAfter, loginLockedMessage(res.retryAfter))
	}
}

// refuseSignIn answers a sign-in whose credential was not accepted: 401, or — for
// the attempt that used up the budget — the 429 of the lock that is in place from now
// on. account is the account the identifier names, nil when it names none, and
// reason the class of the failure; they decide only what is recorded (see
// recordFailedSignIn), never the answer.
func (h *AuthHandler) refuseSignIn(c fiber.Ctx, a loginAttempt, account *db.User, reason string) error {
	h.recordFailedSignIn(c, a, account, reason)
	if a.counted && a.res.admission == lockoutAdmittedLast {
		h.recordLoginLock(c, a, account)
		return lockedFor(c, a.res.retryAfter, loginLockedMessage(a.res.retryAfter))
	}
	return fiber.NewError(fiber.StatusUnauthorized, "Invalid email or password")
}

// recordLoginLock leaves the lock's trace: a warning in the log, and for an account
// that exists an audit row, and the record that lets the first attempt after the
// lock audit its release. Never the identifier or the password: audit details are
// readable by every Viewer.
func (h *AuthHandler) recordLoginLock(c fiber.Ctx, a loginAttempt, account *db.User) {
	lockSeconds := int(a.res.retryAfter / time.Second)
	if account == nil {
		slog.Warn("sign-in: an identifier that names no account is locked after repeated failed sign-ins",
			"ip", c.IP(), "failed_attempts", a.res.attempts, "lockout_seconds", lockSeconds)
		return
	}
	slog.Warn("sign-in: the account is locked out of signing in with a password after repeated failed sign-ins",
		"user_id", account.ID, "ip", c.IP(), "failed_attempts", a.res.attempts, "lockout_seconds", lockSeconds)
	if h.rdb != nil {
		ctx, cancel := context.WithTimeout(c.Context(), loginLockoutWait)
		_, err := boundedByContext(ctx, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, h.rdb.Set(ctx, loginLockedKey(a.subject), account.ID.String(), loginLockRecordTTL).Err()
		})
		cancel()
		if err != nil {
			slog.Warn("sign-in: could not keep the record of a lock; its release will not be audited", "user_id", account.ID, "error", err)
		}
	}
	details, _ := json.Marshal(map[string]any{
		"ip":              c.IP(),
		"failed_attempts": a.res.attempts,
		"lockout_seconds": lockSeconds,
	})
	h.withFollowUp(c, func() {
		AuditLogAs(c, h.queries, h.eventPub, account.ID, pgtype.UUID{}, "auth", account.ID.String(), "login_locked", details)
	})
}

// recordLoginLockRelease audits the end of a lock, at the first attempt counted after
// it, when there is a record of one (recordLoginLock): the lock itself ends in Redis,
// where nothing can watch it go.
func (h *AuthHandler) recordLoginLockRelease(c fiber.Ctx, subject uuid.UUID) {
	if h.rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), loginLockoutWait)
	owner, err := boundedByContext(ctx, func(ctx context.Context) (string, error) {
		return h.rdb.GetDel(ctx, loginLockedKey(subject)).Result()
	})
	cancel()
	if err != nil {
		return // redis.Nil: no lock of an account ended here; anything else, nothing to record
	}
	id, err := uuid.Parse(owner)
	if err != nil {
		return
	}
	details, _ := json.Marshal(map[string]any{"ip": c.IP()})
	h.withFollowUp(c, func() {
		AuditLogAs(c, h.queries, h.eventPub, id, pgtype.UUID{}, "auth", id.String(), "login_lock_released", details)
	})
}

// forgetLoginFailures clears the count of an identifier whose sign-in succeeded,
// best effort: a count left behind runs out with its window.
func (h *AuthHandler) forgetLoginFailures(c fiber.Ctx, a loginAttempt) {
	if !a.counted {
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), loginLockoutWait)
	defer cancel()
	if err := h.loginLockoutStore().clear(ctx, a.subject); err != nil {
		slog.Warn("sign-in: could not clear the failed-attempt count after a sign-in succeeded; it stays until it runs out", "error", err)
	}
}

// Failed sign-ins are audited, as login_failed, under the account they named, with
// the source address, the method and the class of the failure (wrong_password,
// account_disabled, sso_only_account, directory_rejected): never what was typed.
// The lockout bounds how many one account can have: a locked attempt is refused
// before it gets here, so an account writes at most loginLockoutThreshold of them a
// lock cycle (with Redis down, only the per-address limiter bounds them).
//
// An identifier that names no account is audited without it, as
// login_failed_unknown_account: audit details are readable by every Viewer, and what
// was typed into the address box is sometimes a password. Those have no account to
// bound them, so they are bounded per source address: one row the first time an
// address fails this way in a minute (unknownSignInWindow), carrying the number of
// such failures in that address's previous minute of them, within the last hour.
// Without Redis to count in, or when the attempt could not be counted there, none
// is written.
const (
	unknownSignInWindow = time.Minute
	unknownSignInCarry  = time.Hour
	signInMethod        = "password"
)

// The bound is per sign-in method as well as per address, so that each method's row
// is labelled with its own failures: one SSO failure must not take a minute of
// password failures' row.
func unknownSignInKey(method, ip string) string { return "login:unknown:" + method + ":" + ip }
func unknownSignInCarryKey(method, ip string) string {
	return "login:unknown:last:" + method + ":" + ip
}

// countUnknownSignIn counts one failure of an identifier that names no account
// against its source address. KEYS: this window's counter, the carry of the last
// window's count. ARGV: the window and the carry's life in milliseconds. It answers
// {this window's count, the last window's count, or 0}.
var countUnknownSignIn = redis.NewScript(`
local n = redis.call('INCR', KEYS[1])
local last = 0
if n == 1 or redis.call('PTTL', KEYS[1]) < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
if n == 1 then
  last = tonumber(redis.call('GET', KEYS[2]) or '0')
end
redis.call('SET', KEYS[2], n, 'PX', ARGV[2])
return {n, last}
`)

// recordFailedSignIn audits a failed sign-in (see the note above).
func (h *AuthHandler) recordFailedSignIn(c fiber.Ctx, a loginAttempt, account *db.User, reason string) {
	ip := c.IP()
	if account != nil {
		details, _ := json.Marshal(map[string]string{"email": account.Email, "ip": ip, "method": signInMethod, "reason": reason})
		h.withFollowUp(c, func() {
			AuditLogAs(c, h.queries, h.eventPub, account.ID, pgtype.UUID{}, "auth", account.ID.String(), "login_failed", details)
		})
		return
	}
	// Not when the attempt itself could not be counted: a Redis that does not answer
	// would hold this failure for a second bound that a known account's does not
	// wait, and the time would tell the two apart.
	if h.rdb == nil || !a.counted {
		return
	}
	details, audit := unknownSignInDetails(c, h.rdb, signInMethod, "unknown_account")
	if !audit {
		return
	}
	h.withFollowUp(c, func() {
		AuditLogAs(c, h.queries, h.eventPub, uuid.Nil, pgtype.UUID{}, "auth", "", "login_failed_unknown_account", details)
	})
}

// unknownSignInDetails counts a failed sign-in that names no account against its
// source address (the bound the note above describes, one for every sign-in method)
// and, when it is the one this minute audits, returns the details of its
// login_failed_unknown_account row: no identifier, the number of such failures in
// the address's previous window. audit is false otherwise, or when it could not be
// counted.
func unknownSignInDetails(c fiber.Ctx, rdb *redis.Client, method, reason string) (details json.RawMessage, audit bool) {
	ip := c.IP()
	ctx, cancel := context.WithTimeout(c.Context(), loginLockoutWait)
	vals, err := boundedByContext(ctx, func(ctx context.Context) ([]int64, error) {
		return countUnknownSignIn.Run(ctx, rdb, []string{unknownSignInKey(method, ip), unknownSignInCarryKey(method, ip)},
			unknownSignInWindow.Milliseconds(), unknownSignInCarry.Milliseconds()).Int64Slice()
	})
	cancel()
	if err != nil || len(vals) != 2 {
		slog.Warn("sign-in: could not count a failed sign-in that named no account; it is not audited", "ip", ip, "error", err)
		return nil, false
	}
	if vals[0] != 1 {
		return nil, false
	}
	details, _ = json.Marshal(map[string]any{
		"ip": ip, "method": method, "reason": reason,
		"window_seconds": int(unknownSignInWindow / time.Second), "failures_in_last_window": vals[1],
	})
	return details, true
}

// loginLockedMessage is the locked sign-in's answer, the same whether or not the
// identifier names an account.
func loginLockedMessage(wait time.Duration) string {
	minutes, unit := minutesLeft(wait)
	return fmt.Sprintf("Too many failed sign-in attempts. Signing in to this account is locked for %d more %s; try again then.", minutes, unit)
}
