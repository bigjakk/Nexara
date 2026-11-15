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
// offline, as what was typed. Case and surrounding spaces are ignored, so that
// spelling an address differently is not a fresh budget. A handler built without a
// JWT service (only in tests: production builds none without one) digests unkeyed.
func (h *AuthHandler) loginSubject(identifier string) uuid.UUID {
	norm := strings.ToLower(strings.TrimSpace(identifier))
	var d [32]byte
	if h.jwtService != nil {
		d = h.jwtService.Digest("login lockout", norm)
	} else {
		d = sha256.Sum256([]byte("login lockout: " + norm))
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
// on. account is the account the identifier names, nil when it names none; it
// decides only what is recorded, never the answer.
func (h *AuthHandler) refuseSignIn(c fiber.Ctx, a loginAttempt, account *db.User) error {
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
	if h.lockoutRedis != nil {
		ctx, cancel := context.WithTimeout(c.Context(), loginLockoutWait)
		_, err := boundedByContext(ctx, func(ctx context.Context) (struct{}, error) {
			return struct{}{}, h.lockoutRedis.Set(ctx, loginLockedKey(a.subject), account.ID.String(), loginLockRecordTTL).Err()
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
	if h.lockoutRedis == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), loginLockoutWait)
	owner, err := boundedByContext(ctx, func(ctx context.Context) (string, error) {
		return h.lockoutRedis.GetDel(ctx, loginLockedKey(subject)).Result()
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

// loginLockedMessage is the locked sign-in's answer, the same whether or not the
// identifier names an account.
func loginLockedMessage(wait time.Duration) string {
	minutes, unit := minutesLeft(wait)
	return fmt.Sprintf("Too many failed sign-in attempts. Signing in to this account is locked for %d more %s; try again then.", minutes, unit)
}
