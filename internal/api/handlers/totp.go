package handlers

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

const (
	totpSetupTTL         = 10 * time.Minute
	totpPendingTTL       = 5 * time.Minute
	totpMaxAttempts      = 5 // max tries per pending login token before it self-destructs
	totpSetupMaxAttempts = 5 // max tries per setup secret before it self-destructs
	totpRecoveryCount    = 10

	// Per-user TOTP brute-force lockout — orthogonal to the per-IP auth limiter
	// at the middleware layer. An attacker botnet distributing attempts across
	// IPs would slip past 15-attempts/min/IP; this catches that pattern.
	totpLockoutThreshold = 5
	totpLockoutWindow    = 10 * time.Minute
	totpLockoutDuration  = 5 * time.Minute

	// The sign-in step's recovery codes count apart from its authenticator codes
	// (totpRecoveryLockoutPolicy), with the same figures, so that wrong authenticator
	// codes no longer block a recovery code, and the reverse: the owner who has lost the
	// device never waits longer than before. It does not stop someone who has the
	// password and keeps trying both kinds from keeping both locked; the remedy for that
	// is still an administrator's reset.
	totpRecoveryLockoutThreshold = totpLockoutThreshold
	totpRecoveryLockoutWindow    = totpLockoutWindow
	totpRecoveryLockoutDuration  = totpLockoutDuration
)

// The answers of a setup that cannot go on until the caller proves it is the owner.
const (
	totpPasswordRequiredMessage          = "Enter your current password to set up two-factor authentication."
	totpDirectoryPasswordRequiredMessage = "Enter your directory password to set up two-factor authentication."
	totpNoProofMessage                   = "Two-factor authentication cannot be set up for this account: it has no password or identity provider to confirm it is you."
	totpSSOStepUpRequiredMessage         = "Confirm with your identity provider first: your account signs in through single sign-on, " +
		"so Nexara asks you to sign in there again before it sets up two-factor authentication."
)

var totpCodePattern = regexp.MustCompile(`^\d{6}$`)

// All 8 routes are declared in internal/api/registry_totp.go: six SelfService
// (the caller's own enrollment), one Public (the second factor of an
// in-progress login) and one global manage:user Check (the admin reset, the
// only route here that acts on somebody else).
//
// What stays here is what a declaration cannot see: the per-user lockout, the
// per-token attempt budget, the peek-without-consuming reads, and the
// "code or recovery_code, at least one" rule, which is cross-field.

// TOTPHandler handles TOTP 2FA endpoints.
type TOTPHandler struct {
	queries     *db.Queries
	totpService *auth.TOTPService
	rdb         *redis.Client
	eventPub    *events.Publisher
	issueTokens func(c fiber.Ctx, user db.User, epoch int64, auditAction string) error

	// proveCurrentPassword is AuthHandler.ProveCurrentPassword: BeginSetup's check of
	// a local account's current password, under the change-password lockout's
	// budget. Wired by SetCurrentPasswordCheck; nil refuses every local account's
	// setup rather than skipping the check.
	proveCurrentPassword func(c fiber.Ctx, userID uuid.UUID, password string) error
	// proveDirectoryPassword is AuthHandler.ProveDirectoryPassword: the same for an LDAP
	// account, by a bind as its entry under the sign-in's budgets. Wired by
	// SetDirectoryPasswordCheck; nil refuses every directory account's setup.
	proveDirectoryPassword func(c fiber.Ctx, userID uuid.UUID, password string) error
	// startSSOStepUp is OIDCHandler.StartStepUp: the round trip through the identity
	// provider that a single sign-on account proves itself with. Wired by SetSSOStepUp;
	// nil refuses every step-up.
	startSSOStepUp func(c fiber.Ctx, userID, sessionID uuid.UUID) (string, error)
	// finishSSOSignIn is OIDCHandler.FinishSignInAfterSecondFactor: VerifyLogin's finish
	// of an SSO sign-in the callback left for the second factor. Wired by
	// SetSSOSignInFinish; nil refuses every such sign-in.
	finishSSOSignIn func(c fiber.Ctx, userID uuid.UUID, identity *pendingSSOIdentity) (db.User, error)

	// followUpTimeout bounds each follow-up of a decision that has been made — the
	// audit row of a second step that was refused — and is authFollowUpTimeout when
	// zero. A field rather than a constant so that a test can make a stalled insert
	// fail in milliseconds instead of waiting out the production bound, as
	// AuthHandler's and UserHandler's do.
	followUpTimeout time.Duration
}

// totpPendingLogin is the JSON stored under totp:pending:<token> for the five
// minutes between a login's first step and its second.
//
// AuthEpoch is the user's auth epoch as the FIRST step's credential check read it —
// the password comparison, the directory bind, or the SSO callback — and it is what
// the session the second step creates is made conditional on (VerifyLogin). It is a
// pointer so that a token minted by a release that recorded none is told apart from
// one that recorded 0, the epoch of every user who has never had a revoke-all: the
// first cannot be tied to any check and is refused, the second is an ordinary
// value.
//
// SSO is the provider identity of an SSO sign-in the callback left to finish after the
// second factor (pendingSSOIdentity); nil for every other sign-in, and for a token minted
// by a release that carried none.
type totpPendingLogin struct {
	UserID      string              `json:"user_id"`
	AuditAction string              `json:"audit_action"`
	AuthEpoch   *int64              `json:"auth_epoch"`
	SSO         *pendingSSOIdentity `json:"sso,omitempty"`
}

// NewTOTPHandler creates a new TOTP handler.
func NewTOTPHandler(queries *db.Queries, encryptionKey string, rdb *redis.Client, eventPub *events.Publisher) *TOTPHandler {
	return &TOTPHandler{
		queries:     queries,
		totpService: auth.NewTOTPService(encryptionKey),
		rdb:         rdb,
		eventPub:    eventPub,
	}
}

// followUpDuration is the deadline of one follow-up: authFollowUpTimeout unless a
// test shortened it.
func (h *TOTPHandler) followUpDuration() time.Duration {
	return followUpBound(h.followUpTimeout)
}

// withFollowUp runs fn — a call that reads c.Context(), such as AuditLogAs with the
// event it publishes — under a follow-up deadline (see runFollowUp).
func (h *TOTPHandler) withFollowUp(c fiber.Ctx, fn func()) {
	runFollowUp(c, h.followUpDuration(), fn)
}

// SetIssueTokensFn sets the token-issuing function (called from server setup).
func (h *TOTPHandler) SetIssueTokensFn(fn func(c fiber.Ctx, user db.User, epoch int64, auditAction string) error) {
	h.issueTokens = fn
}

// SetCurrentPasswordCheck sets the check of a local account's current password
// that BeginSetup asks for (AuthHandler.ProveCurrentPassword; called from server
// setup).
func (h *TOTPHandler) SetCurrentPasswordCheck(fn func(c fiber.Ctx, userID uuid.UUID, password string) error) {
	h.proveCurrentPassword = fn
}

// SetDirectoryPasswordCheck sets the check of a directory account's password that
// BeginSetup asks for (AuthHandler.ProveDirectoryPassword; called from server setup).
func (h *TOTPHandler) SetDirectoryPasswordCheck(fn func(c fiber.Ctx, userID uuid.UUID, password string) error) {
	h.proveDirectoryPassword = fn
}

// SetSSOStepUp sets how a single sign-on account's step-up begins
// (OIDCHandler.StartStepUp; called from server setup).
func (h *TOTPHandler) SetSSOStepUp(fn func(c fiber.Ctx, userID, sessionID uuid.UUID) (string, error)) {
	h.startSSOStepUp = fn
}

// SetSSOSignInFinish sets how VerifyLogin finishes an SSO sign-in after its second
// factor (OIDCHandler.FinishSignInAfterSecondFactor; called from server setup).
func (h *TOTPHandler) SetSSOSignInFinish(fn func(c fiber.Ctx, userID uuid.UUID, identity *pendingSSOIdentity) (db.User, error)) {
	h.finishSSOSignIn = fn
}

// BeginSSOStepUp handles POST /api/v1/auth/totp/setup/sso-step-up: it starts the round
// trip through the identity provider that a single sign-on account proves it is the
// owner with before BeginSetup (see OIDCHandler.StartStepUp), and answers the URL to
// send the browser to. Only for the caller's own live session, on an SSO account with
// no second factor yet.
func (h *TOTPHandler) BeginSSOStepUp(c fiber.Ctx, _ *apischema.Params) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	sessionID, _ := c.Locals(LocalsSessionID).(uuid.UUID)
	if userID == uuid.Nil || sessionID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, sessionEndedMessage)
	}
	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check TOTP status")
	}
	if row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusConflict, "TOTP is already enabled")
	}
	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}
	if user.AuthSource != "oidc" {
		return fiber.NewError(fiber.StatusForbidden, "Only an account that signs in through single sign-on confirms with its identity provider")
	}
	if h.startSSOStepUp == nil {
		slog.Error("two-factor setup: no SSO step-up is wired, so no single sign-on account can begin a setup", "user_id", userID)
		return fiber.NewError(fiber.StatusInternalServerError, totpSetupFailedMessage)
	}
	redirectURL, err := h.startSSOStepUp(c, userID, sessionID)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"redirect_url": redirectURL})
}

// BeginSetup handles POST /api/v1/auth/totp/setup — starts TOTP enrollment.
//
// The secret it hands out is what turns a second factor on (ConfirmSetup needs a
// code from it), so it is handed out only to the account's owner: a session that
// is live (the registry's InteractiveOnly gate) and that proves it is the owner
// (confirmItIsTheOwner). A stolen access token alone would otherwise enrol its own
// authenticator, take the recovery codes, and lock the owner out of sign-in.
func (h *TOTPHandler) BeginSetup(c fiber.Ctx, p *apischema.Params) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check TOTP status")
	}
	if row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusConflict, "TOTP is already enabled")
	}

	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}
	if err := h.confirmItIsTheOwner(c, user, p.String("current_password")); err != nil {
		return err
	}

	encrypted, otpauthURL, plainSecret, err := h.totpService.GenerateSecret(user.Email)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate TOTP secret")
	}

	key := totpSetupKey(userID)
	if err := h.rdb.Set(c.Context(), key, encrypted, totpSetupTTL).Err(); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to store setup data")
	}
	// Wipe any leftover counter from a prior setup attempt so the user gets a
	// fresh budget on this enrollment.
	_ = h.rdb.Del(c.Context(), totpSetupAttemptsKey(userID)).Err()

	return c.JSON(fiber.Map{
		"secret":      plainSecret,
		"otpauth_url": otpauthURL,
	})
}

// confirmItIsTheOwner is what BeginSetup asks of a caller beyond a live session.
//
// An account that signs in with a Nexara password proves it, under the budget
// change-password counts in (proveCurrentPassword): 403 for a wrong one, 429 while
// the account is locked out of both routes, 503 when the attempt cannot be counted.
// An empty password is no attempt at all and counts nothing.
//
// An account that signs in through LDAP proves its directory password instead, by a
// bind as its own entry counted in the sign-in's budgets (proveDirectoryPassword), with
// the same answers: 400 when none is sent, 403 wrong, 429 locked, 503 when the attempt
// cannot be counted or the directory cannot be reached. Nexara's budget sits in front of
// the bind, so a guesser locks Nexara's sign-in before the directory's own lockout.
//
// An account that signs in through single sign-on has no password anywhere, so it
// proves a fresh sign-in at its identity provider instead: a step-up round trip
// (POST /auth/totp/setup/sso-step-up, OIDCHandler.StartStepUp) records a proof for
// this session, and the setup uses it up (useSSOStepUp); 403 without one. A password
// it sends is not looked at. A token stolen from any session, however recent, gets
// nothing from it without signing in at the provider as the owner.
//
// An account of any other source has no way to prove it is the owner, and is refused
// 403. (There used to be a third way, for both LDAP and SSO accounts: a session that
// began within the last ten minutes, which a token stolen within them also passed.)
func (h *TOTPHandler) confirmItIsTheOwner(c fiber.Ctx, user db.User, password string) error {
	if user.AuthSource == "local" {
		if password == "" {
			return fiber.NewError(fiber.StatusBadRequest, totpPasswordRequiredMessage)
		}
		if h.proveCurrentPassword == nil {
			slog.Error("two-factor setup: no current-password check is wired, so no local account can begin a setup", "user_id", user.ID)
			return fiber.NewError(fiber.StatusInternalServerError, totpSetupFailedMessage)
		}
		return h.proveCurrentPassword(c, user.ID, password)
	}
	if user.AuthSource == "ldap" {
		if password == "" {
			return fiber.NewError(fiber.StatusBadRequest, totpDirectoryPasswordRequiredMessage)
		}
		if h.proveDirectoryPassword == nil {
			slog.Error("two-factor setup: no directory-password check is wired, so no directory account can begin a setup", "user_id", user.ID)
			return fiber.NewError(fiber.StatusInternalServerError, totpSetupFailedMessage)
		}
		return h.proveDirectoryPassword(c, user.ID, password)
	}
	if user.AuthSource == "oidc" {
		return h.useSSOStepUp(c, user)
	}

	slog.Warn("two-factor setup: refused an account of a source that has no way to prove it is the owner",
		"user_id", user.ID, "auth_source", user.AuthSource)
	return fiber.NewError(fiber.StatusForbidden, totpNoProofMessage)
}

// useSSOStepUp uses up the proof a step-up through the identity provider recorded for
// this session (OIDCHandler.finishStepUp): valid, by the database's clock, only for
// this session, this account and this purpose, only from the browser whose cookie it is
// bound to, and only once. None is 403, and a request without the cookie leaves the
// owner's proof where it is.
func (h *TOTPHandler) useSSOStepUp(c fiber.Ctx, user db.User) error {
	sessionID, _ := c.Locals(LocalsSessionID).(uuid.UUID)
	if sessionID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, sessionEndedMessage)
	}
	bindings := totpStepUpCookieRecords(c)
	if len(bindings) == 0 {
		return fiber.NewError(fiber.StatusForbidden, totpSSOStepUpRequiredMessage)
	}
	ctx, cancel := context.WithTimeout(c.Context(), authDBTimeout)
	n, err := h.queries.ConsumeTOTPStepUpProof(ctx, db.ConsumeTOTPStepUpProofParams{
		SessionID: sessionID, UserID: user.ID, Purpose: totpStepUpPurpose, Bindings: bindings,
	})
	cancel()
	if err != nil {
		return totpSetupNotChecked("use the identity provider's confirmation", user.ID, err)
	}
	if n == 0 {
		return fiber.NewError(fiber.StatusForbidden, totpSSOStepUpRequiredMessage)
	}
	// Cleared where it was set: under the redirect URI's prefix. A configuration that
	// cannot be read clears the unprefixed path; the proof is spent either way.
	redirectURI := ""
	if cfg, err := h.queries.GetEnabledOIDCConfig(c.Context()); err == nil {
		redirectURI = cfg.RedirectUri
	}
	clearTOTPStepUpCookie(c, redirectURI)
	return nil
}

// ConfirmSetup handles POST /api/v1/auth/totp/setup/verify — confirms TOTP enrollment.
func (h *TOTPHandler) ConfirmSetup(c fiber.Ctx, p *apischema.Params) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	// The declaration's pattern refuses both an absent code and a malformed
	// one, which is the pair of checks this used to make by hand.
	code := p.String("code")

	setupKey := totpSetupKey(userID)
	attemptsKey := totpSetupAttemptsKey(userID)

	// Peek the secret without consuming — a single typo must not destroy
	// enrollment. The explicit per-secret counter caps brute-force at
	// totpSetupMaxAttempts; the (totpSetupMaxAttempts+1)th attempt nukes the
	// secret so an attacker who somehow reached this authenticated path can't
	// run an unbounded oracle against it.
	encrypted, err := h.rdb.Get(c.Context(), setupKey).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "No pending TOTP setup — start setup first")
	}

	attempts, err := h.rdb.Incr(c.Context(), attemptsKey).Result()
	if err != nil {
		// Don't fail-open on the budget side either: if Redis can't track
		// the counter, refuse rather than allow unlimited attempts.
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to record attempt")
	}
	if attempts == 1 {
		_ = h.rdb.Expire(c.Context(), attemptsKey, totpSetupTTL).Err()
	}
	if attempts > int64(totpSetupMaxAttempts) {
		_ = h.rdb.Del(c.Context(), setupKey, attemptsKey).Err()
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many failed attempts — please restart setup")
	}

	valid, err := h.totpService.ValidateCode(encrypted, code)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to validate code")
	}
	if !valid {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
	}

	// Validation succeeded — consume the secret + clear the counter.
	_ = h.rdb.Del(c.Context(), setupKey, attemptsKey).Err()

	if err := h.queries.SetTOTPSecret(c.Context(), db.SetTOTPSecretParams{
		ID:         userID,
		TotpSecret: pgtype.Text{String: encrypted, Valid: true},
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to enable TOTP")
	}

	plainCodes, hashedCodes, err := h.totpService.GenerateRecoveryCodes(totpRecoveryCount)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate recovery codes")
	}

	// Clear any stale recovery codes and store new ones.
	_ = h.queries.DeleteAllRecoveryCodes(c.Context(), userID)
	for _, hash := range hashedCodes {
		if err := h.queries.InsertRecoveryCode(c.Context(), db.InsertRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hash,
		}); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to store recovery codes")
		}
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", userID.String(), "totp_enabled", nil)

	return c.JSON(fiber.Map{
		"enabled":        true,
		"recovery_codes": plainCodes,
	})
}

// Disable handles DELETE /api/v1/auth/totp — disables TOTP for the current user.
func (h *TOTPHandler) Disable(c fiber.Ctx, p *apischema.Params) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	// Cross-field, so it stays here: apischema's Requires names a companion a
	// parameter ALWAYS needs, not one of a set. The code's SHAPE stays here for
	// a different reason — an explicitly empty code is legal, meaning "I am
	// using the recovery code", and a schema Pattern would refuse it. See
	// totpOptionalCodeParam.
	code := p.String("code")
	recoveryCode := p.String("recovery_code")
	if code == "" && recoveryCode == "" {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP code or recovery code is required")
	}
	if code != "" && !totpCodePattern.MatchString(code) {
		return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check TOTP status")
	}
	if !row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP is not enabled")
	}

	attempt, err := h.admitTOTPAttempt(c, totpManageLockoutPolicy, passwordLockoutSubject(c, userID), userID, totpLockedMessage)
	if err != nil {
		return err
	}
	validated, err := h.validateCodeOrRecovery(c.Context(), userID, row.TotpSecret.String, code, recoveryCode)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to validate code")
	}
	if !validated {
		if attempt.admission == lockoutAdmittedLast {
			return lockedFor(c, attempt.retryAfter, totpLockedMessage)
		}
		if code != "" {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
		}
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid recovery code")
	}

	if err := h.queries.ClearTOTPSecret(c.Context(), userID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to disable TOTP")
	}
	_ = h.queries.DeleteAllRecoveryCodes(c.Context(), userID)
	h.forgetTOTPFailures(c, totpManageLockoutPolicy, passwordLockoutSubject(c, userID), userID)

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", userID.String(), "totp_disabled", nil)

	return c.JSON(fiber.Map{"message": "TOTP disabled"})
}

// Status handles GET /api/v1/auth/totp/status — returns TOTP status for current user.
func (h *TOTPHandler) Status(c fiber.Ctx, _ *apischema.Params) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check TOTP status")
	}

	count, err := h.queries.CountRecoveryCodes(c.Context(), userID)
	if err != nil {
		count = 0
	}

	return c.JSON(fiber.Map{
		"enabled":                  row.TotpSecret.Valid,
		"recovery_codes_remaining": count,
	})
}

// RegenerateRecoveryCodes handles POST /api/v1/auth/totp/recovery-codes/regenerate.
func (h *TOTPHandler) RegenerateRecoveryCodes(c fiber.Ctx, p *apischema.Params) error {
	userID, _ := c.Locals("user_id").(uuid.UUID)
	if userID == uuid.Nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	// The declaration's pattern refuses both an absent code and a malformed
	// one. A RECOVERY code is deliberately not accepted on this route: it would
	// let one leaked recovery code mint ten fresh ones.
	code := p.String("code")

	row, err := h.queries.GetUserTOTPSecret(c.Context(), userID)
	if err != nil || !row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP is not enabled")
	}

	attempt, err := h.admitTOTPAttempt(c, totpManageLockoutPolicy, passwordLockoutSubject(c, userID), userID, totpLockedMessage)
	if err != nil {
		return err
	}
	valid, err := h.totpService.ValidateCode(row.TotpSecret.String, code)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to validate code")
	}
	if !valid {
		if attempt.admission == lockoutAdmittedLast {
			return lockedFor(c, attempt.retryAfter, totpLockedMessage)
		}
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
	}
	h.forgetTOTPFailures(c, totpManageLockoutPolicy, passwordLockoutSubject(c, userID), userID)

	_ = h.queries.DeleteAllRecoveryCodes(c.Context(), userID)

	plainCodes, hashedCodes, err := h.totpService.GenerateRecoveryCodes(totpRecoveryCount)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate recovery codes")
	}

	for _, hash := range hashedCodes {
		if err := h.queries.InsertRecoveryCode(c.Context(), db.InsertRecoveryCodeParams{
			UserID:   userID,
			CodeHash: hash,
		}); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to store recovery codes")
		}
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", userID.String(), "totp_recovery_codes_regenerated", nil)

	return c.JSON(fiber.Map{
		"recovery_codes": plainCodes,
	})
}

// VerifyLogin handles POST /api/v1/auth/totp/verify-login — completes two-step login.
func (h *TOTPHandler) VerifyLogin(c fiber.Ctx, p *apischema.Params) error {
	pendingToken := p.String("totp_pending_token")

	// Cross-field, so it stays here; and the code's SHAPE stays here because an
	// explicitly empty code is legal, meaning "I am using the recovery code".
	// See totpOptionalCodeParam.
	code := p.String("code")
	recoveryCode := p.String("recovery_code")
	if code == "" && recoveryCode == "" {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP code or recovery code is required")
	}
	if code != "" && !totpCodePattern.MatchString(code) {
		return fiber.NewError(fiber.StatusBadRequest, "Code must be exactly 6 digits")
	}

	pendingKey := fmt.Sprintf("totp:pending:%s", pendingToken)
	attemptKey := fmt.Sprintf("totp:attempts:%s", pendingToken)

	// Per-pending-token attempt counter — caps brute-force on a single token
	// regardless of which user it belongs to.
	attempts, err := h.rdb.Incr(c.Context(), attemptKey).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to record attempt")
	}
	if attempts == 1 {
		_ = h.rdb.Expire(c.Context(), attemptKey, totpPendingTTL).Err()
	}
	if attempts > int64(totpMaxAttempts) {
		_ = h.rdb.Del(c.Context(), pendingKey, attemptKey).Err()
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many attempts — please log in again")
	}

	// Peek the pending token without consuming. A typo no longer destroys the
	// pending session — the per-token counter above bounds the budget; the
	// per-user lockout below catches attackers cycling through fresh tokens.
	data, err := h.rdb.Get(c.Context(), pendingKey).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired pending token")
	}

	var pending totpPendingLogin
	if err := json.Unmarshal([]byte(data), &pending); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Invalid pending data")
	}

	userID, err := uuid.Parse(pending.UserID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Invalid user in pending data")
	}

	// A pending token minted without an epoch (by a release before it existed, in
	// the minutes around an upgrade) cannot be tied to the password step's check, so
	// the session it would create could not be conditional on anything. Refused as an
	// expired token, and destroyed — it can never succeed — before any code, and in
	// particular any single-use recovery code, is spent on it.
	if pending.AuthEpoch == nil {
		slog.Warn("totp verify-login: the pending token carries no auth epoch; refusing it", "user_id", userID)
		_ = h.rdb.Del(c.Context(), pendingKey, attemptKey).Err()
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired pending token")
	}

	// user is read HERE, after the password step, to check that the account is still
	// active and has a second factor. Its AuthEpoch is whatever the account holds now:
	// it is compared with the pending token's below, and it is NEVER what the session
	// is created against (see the issueTokens call at the end).
	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "User not found")
	}
	if !user.IsActive {
		return fiber.NewError(fiber.StatusForbidden, "Account is disabled")
	}
	if !user.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusInternalServerError, "TOTP not configured for user")
	}

	// The account's epoch against the one the password step read. They differ when a
	// revoke-all — a password change, a sign-out of all devices, an account
	// deactivated and reactivated — landed since: the credential this pending token
	// stands for has been replaced. The token is refused HERE, before any code is
	// looked at, and as an expired token, with the same text, so that it tells its
	// holder nothing new. That is what keeps three things from happening to a login
	// that cannot be allowed: a single-use recovery code is not spent on it, no
	// failure is counted against the user, and the stale token stops being an oracle
	// for the second factor, which a holder could otherwise keep testing codes
	// against for the rest of its five minutes. The token and its attempt counter are
	// destroyed — it can never succeed.
	//
	// This comparison is the early, cheap "no". The conditional session insert in
	// issueTokens is still the authority: it decides against the epoch at the instant
	// of the insert, which a revoke-all landing after this read can still change.
	if user.AuthEpoch != *pending.AuthEpoch {
		slog.Info("totp verify-login: the account's sessions were ended since the password step; refusing the pending token",
			"user_id", userID)
		_ = h.rdb.Del(c.Context(), pendingKey, attemptKey).Err()
		// A sign-in refused after a CORRECT credential is worth a row of its own (see
		// signInRefusedAuditAction); the answer does not say so.
		details, _ := json.Marshal(map[string]string{"email": user.Email, "ip": c.IP()})
		h.withFollowUp(c, func() {
			AuditLogAs(c, h.queries, h.eventPub, user.ID, pgtype.UUID{}, "auth", user.ID.String(), signInRefusedAuditAction, details)
		})
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired pending token")
	}

	// Counted here, after every refusal above that spends no code, and before the
	// code is checked: a locked account's code is not looked at. An authenticator code
	// and a recovery code count in budgets of their own, so a lock on one leaves the
	// other usable (totpRecoveryLockoutPolicy).
	policy, lockedMessage := totpLockoutPolicy, totpLockedMessage
	if code == "" {
		policy, lockedMessage = totpRecoveryLockoutPolicy, totpRecoveryLockedMessage
	}
	attempt, err := h.admitTOTPAttempt(c, policy, userID, userID, lockedMessage)
	if err != nil {
		return err
	}
	validated, verr := h.validateCodeOrRecovery(c.Context(), userID, user.TotpSecret.String, code, recoveryCode)
	if verr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to validate code")
	}
	if !validated {
		if attempt.admission == lockoutAdmittedLast {
			// Lockout just triggered — invalidate this pending session too so
			// the attacker can't burn the rest of the per-token budget while
			// the lock is in effect.
			_ = h.rdb.Del(c.Context(), pendingKey, attemptKey).Err()
			return lockedFor(c, attempt.retryAfter, lockedMessage)
		}
		if code != "" {
			return fiber.NewError(fiber.StatusUnauthorized, "Invalid TOTP code")
		}
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid recovery code")
	}

	// Validation succeeded — consume pending state. If two concurrent requests
	// both validate, both will issue tokens (each producing a session for the
	// same user with a real second factor); not a security hole, just an
	// edge-case ergonomic. Race-detection via Del-result is deliberately not
	// done because a recovery code might already be burned at this point.
	_ = h.rdb.Del(c.Context(), pendingKey, attemptKey).Err()
	h.forgetTOTPFailures(c, policy, userID, userID)

	if h.issueTokens == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Token issuer not configured")
	}

	// An SSO sign-in the callback left for the second factor is finished now, with the
	// identity the callback carried on and the account read again: linked, its roles
	// synced, or refused with nothing issued. A token without an identity (any other
	// sign-in, or one minted by a release that carried none, whose callback had already
	// done this) goes on as before. A recovery code spent on a sign-in refused here stays
	// spent: putting it back would make a single-use code reusable.
	if pending.SSO != nil {
		if h.finishSSOSignIn == nil {
			slog.Error("totp verify-login: no SSO finish is wired, so no SSO sign-in can pass its second factor", "user_id", userID)
			return fiber.NewError(fiber.StatusInternalServerError, oidcCouldNotCompleteMessage)
		}
		finished, err := h.finishSSOSignIn(c, userID, pending.SSO)
		if err != nil {
			return err
		}
		user = finished
	}

	// The session is created against the epoch the pending token carries — the one
	// the PASSWORD step read. At this point it equals user.AuthEpoch, because the
	// comparison above refused any token that differs, so reading it off the re-read
	// row would behave the same today; it is written as the carried value because
	// that is the one that is true by construction, and it stays tied to the password
	// step if that comparison is ever loosened. A revoke-all that lands after the
	// re-read leaves the insert conditional on the old epoch, and it is refused (401,
	// nothing issued): the user signs in again. The epoch-source guard
	// (TestGuard_TheSessionsEpochIsAlwaysTheOneTheCheckRead) holds this argument to
	// exactly `*pending.AuthEpoch`, and user.AuthEpoch to that comparison alone.
	return h.issueTokens(c, user, *pending.AuthEpoch, pending.AuditAction)
}

// AdminReset handles DELETE /api/v1/users/:id/totp — admin resets user's TOTP.
func (h *TOTPHandler) AdminReset(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	// Prevent admins from resetting their own TOTP — use the self-service disable flow.
	callerID, _ := c.Locals("user_id").(uuid.UUID)
	if id == callerID {
		return fiber.NewError(fiber.StatusForbidden, "Cannot reset your own TOTP via admin endpoint — use the self-service disable flow")
	}

	row, err := h.queries.GetUserTOTPSecret(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "User not found")
	}
	if !row.TotpSecret.Valid {
		return fiber.NewError(fiber.StatusBadRequest, "TOTP is not enabled for this user")
	}

	if err := h.queries.ClearTOTPSecret(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to reset TOTP")
	}
	_ = h.queries.DeleteAllRecoveryCodes(c.Context(), id)

	details, _ := json.Marshal(map[string]string{"target_user_id": id.String()})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "auth", id.String(), "totp_admin_reset", details)

	return c.JSON(fiber.Map{"message": "TOTP reset for user"})
}

// CreateTOTPPendingToken generates a random token and stores it in Redis with user
// data, among it epoch: the user's auth epoch as the credential check that earned
// this challenge read it. VerifyLogin creates the session against that value.
// sso, when not nil, is the identity of an SSO sign-in VerifyLogin is to finish.
func (h *TOTPHandler) CreateTOTPPendingToken(ctx context.Context, userID uuid.UUID, epoch int64, auditAction string, sso *pendingSSOIdentity) (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("generate pending token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)

	data, _ := json.Marshal(totpPendingLogin{
		UserID:      userID.String(),
		AuditAction: auditAction,
		AuthEpoch:   &epoch,
		SSO:         sso,
	})

	key := fmt.Sprintf("totp:pending:%s", token)
	if err := h.rdb.Set(ctx, key, string(data), totpPendingTTL).Err(); err != nil {
		return "", fmt.Errorf("store pending token: %w", err)
	}

	return token, nil
}

// totpSetupKey returns the Redis key that holds the pending (encrypted) TOTP
// secret for a given user.
func totpSetupKey(userID uuid.UUID) string {
	return fmt.Sprintf("totp:setup:%s", userID.String())
}

// totpSetupAttemptsKey returns the Redis key that holds the per-secret failed
// attempt counter during enrollment confirmation.
func totpSetupAttemptsKey(userID uuid.UUID) string {
	return fmt.Sprintf("totp:setup:attempts:%s", userID.String())
}

// totpUserFailKey returns the Redis key that holds the rolling-window failed
// 2FA attempt counter for a user (across all pending tokens / endpoints).
func totpUserFailKey(userID uuid.UUID) string {
	return fmt.Sprintf("totp:user:fail:%s", userID.String())
}

// totpUserLockKey returns the Redis key that, when present, indicates the
// user is in a 2FA cooldown period.
func totpUserLockKey(userID uuid.UUID) string {
	return fmt.Sprintf("totp:user:lock:%s", userID.String())
}

// totpLockoutPolicy is the per-user second-factor lockout's rule, in the stores and
// scripts the current-password lockout counts in (password_lockout.go): an attempt is
// counted, atomically with the lock it may arm, BEFORE its code is checked, so that
// requests in flight together cannot all be checked under one count, and a counter
// cannot be left without an expiry. Its keys are the ones it always had.
var totpLockoutPolicy = lockoutPolicy{
	threshold: totpLockoutThreshold,
	window:    totpLockoutWindow,
	duration:  totpLockoutDuration,
	failKey:   totpUserFailKey,
	lockKey:   totpUserLockKey,
}

// totpRecoveryLockoutPolicy is the sign-in step's budget for RECOVERY codes, per user
// like totpLockoutPolicy and under keys of its own (totp:user:recovery:*), with the
// figures above. Disable and Regenerate keep one budget for both kinds: theirs is the
// session's, so only the session that sent the wrong codes is locked, never the owner.
var totpRecoveryLockoutPolicy = lockoutPolicy{
	threshold: totpRecoveryLockoutThreshold,
	window:    totpRecoveryLockoutWindow,
	duration:  totpRecoveryLockoutDuration,
	failKey:   func(subject uuid.UUID) string { return "totp:user:recovery:fail:" + subject.String() },
	lockKey:   func(subject uuid.UUID) string { return "totp:user:recovery:lock:" + subject.String() },
}

// totpManageLockoutPolicy is the same rule for the two routes that check a code from
// a signed-in session — Disable and RegenerateRecoveryCodes — counted per SESSION
// (passwordLockoutSubject) and under keys of its own. Counted in the user's keys,
// the holder of one stolen session could send wrong codes there and lock the owner's
// second step of sign-in, recovery codes included; this way he spends his own
// session's budget only. The sign-in step, which has no session yet, keeps the
// per-user keys.
var totpManageLockoutPolicy = lockoutPolicy{
	threshold: totpLockoutThreshold,
	window:    totpLockoutWindow,
	duration:  totpLockoutDuration,
	failKey:   func(subject uuid.UUID) string { return "totp:session:fail:" + subject.String() },
	lockKey:   func(subject uuid.UUID) string { return "totp:session:lock:" + subject.String() },
}

const (
	totpLockedMessage = "Too many failed 2FA attempts — try again in a few minutes"
	// totpRecoveryLockedMessage is the sign-in step's lock on recovery codes, which
	// leaves the authenticator's codes usable.
	totpRecoveryLockedMessage = "Too many wrong recovery codes — try a recovery code again in a few minutes. A code from your authenticator app still works."
	// totpLockoutUnavailableMessage is the 503 of a code that could not be counted,
	// and so was not checked.
	totpLockoutUnavailableMessage = "The two-factor code cannot be checked right now because its attempt limit could not be counted; " +
		"nothing was changed. Try again shortly."
)

func (h *TOTPHandler) totpLockout(policy lockoutPolicy) passwordLockoutStore {
	return redisPasswordLockout{rdb: h.rdb, policy: policy}
}

// admitTOTPAttempt counts one attempt at the account's second factor before its code
// is checked: the sign-in's second step, the disable and the regeneration of recovery
// codes. A locked account is refused 429 with a Retry-After and its code is not
// checked. An attempt that cannot be counted is refused 503 and its code is not
// checked either: it fails closed, as the current-password lockout does, because a
// code checked without being counted is a guess with no limit. The count is bounded by
// the follow-up deadline, not by what the Redis client would wait (boundedByContext).
//
// policy and subject say whose budget: the user's for the sign-in step
// (totpLockoutPolicy, or totpRecoveryLockoutPolicy for a recovery code), the session's
// for Disable and Regenerate (totpManageLockoutPolicy); locked is the 429's message.
func (h *TOTPHandler) admitTOTPAttempt(c fiber.Ctx, policy lockoutPolicy, subject, userID uuid.UUID, locked string) (lockoutReservation, error) {
	ctx, cancel := context.WithTimeout(c.Context(), h.followUpDuration())
	defer cancel()
	res, err := h.totpLockout(policy).reserve(ctx, subject)
	if err != nil {
		slog.Warn("two-factor: the failed-attempt counter could not be reached; the code was not checked and nothing was changed",
			"user_id", userID, "error", err)
		return lockoutReservation{}, fiber.NewError(fiber.StatusServiceUnavailable, totpLockoutUnavailableMessage)
	}
	switch res.admission {
	case lockoutAdmitted, lockoutAdmittedLast:
		return res, nil
	default: // lockoutRefused, and anything this code does not know
		return lockoutReservation{}, lockedFor(c, res.retryAfter, locked)
	}
}

// forgetTOTPFailures clears the account's count and any lock once a code verified,
// so a near miss followed by a success does not carry over. Best effort, on a
// follow-up deadline: a count left behind runs out with its window.
func (h *TOTPHandler) forgetTOTPFailures(c fiber.Ctx, policy lockoutPolicy, subject, userID uuid.UUID) {
	ctx, cancel := context.WithTimeout(c.Context(), h.followUpDuration())
	defer cancel()
	if err := h.totpLockout(policy).clear(ctx, subject); err != nil {
		slog.Warn("two-factor: could not clear the failed-attempt count after a code verified; it stays until it runs out",
			"user_id", userID, "error", err)
	}
}

// validateCodeOrRecovery checks an input TOTP code or recovery code against
// the user's stored secret/recovery hashes. Returns (validated, internalErr).
// `internalErr` is non-nil only for genuine internal errors (e.g. decryption
// failure); a wrong code returns (false, nil) so the caller can register a
// failure rather than a 500. Format checks on `code` are the caller's
// responsibility — call the regex first, then this helper.
func (h *TOTPHandler) validateCodeOrRecovery(ctx context.Context, userID uuid.UUID, encryptedSecret, code, recoveryCode string) (bool, error) {
	if code != "" {
		valid, err := h.totpService.ValidateCode(encryptedSecret, code)
		if err != nil {
			return false, err
		}
		return valid, nil
	}
	if err := h.consumeRecoveryCode(ctx, userID, recoveryCode); err != nil {
		// Same handling as the original inline path: any error from the
		// recovery flow (no match, DB blip) is treated as "no match." A
		// rigorous fix to distinguish DB errors from misses would require
		// sentinel errors in consumeRecoveryCode; left as-is to keep this
		// commit's blast radius bounded.
		return false, nil
	}
	return true, nil
}

// consumeRecoveryCode finds and deletes a matching recovery code.
func (h *TOTPHandler) consumeRecoveryCode(ctx context.Context, userID uuid.UUID, inputCode string) error {
	codes, err := h.queries.ListRecoveryCodes(ctx, userID)
	if err != nil {
		return fmt.Errorf("list recovery codes: %w", err)
	}

	for _, rc := range codes {
		if h.totpService.ValidateRecoveryCode(rc.CodeHash, inputCode) {
			if err := h.queries.DeleteRecoveryCode(ctx, rc.ID); err != nil {
				return fmt.Errorf("delete recovery code: %w", err)
			}
			return nil
		}
	}

	return fmt.Errorf("no matching recovery code")
}
