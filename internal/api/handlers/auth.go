package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
)

// firstUserAdvisoryLockKey is the constant passed to pg_advisory_xact_lock so
// concurrent /auth/register calls serialise during the count-then-create
// window. Anything fits — the value just has to be stable across processes
// (it is the lock identity in pg_locks). The lock auto-releases on COMMIT or
// ROLLBACK, so there is no caller responsibility to drop it.
const firstUserAdvisoryLockKey int64 = 0x4E455841524131 // ASCII "NEXARA1"

// MaxRefreshTokenLength is the longest refresh token, in characters, that the
// API will look at. A real one is a few dozen characters of base64.
//
// /auth/refresh's schema declares its body parameter with this bound
// (registry_auth.go), but a cookie never passes through a schema, so Refresh and
// Logout both hold the token they are given — from the body or from the cookie —
// to it themselves (refreshTokenTooLong), before anything is hashed, looked up or
// logged. Refresh answers an over-long one as an invalid token, 401 with the
// cookie cleared; Logout answers it as no token, 200 with the cookie cleared.
const MaxRefreshTokenLength = 1024

// refreshTokenTooLong reports whether token is longer than any refresh token can
// be. It counts characters, as the schema's maxLength does, so a multi-byte token
// is held to the same bound as an ASCII one.
func refreshTokenTooLong(token string) bool {
	return utf8.RuneCountInString(token) > MaxRefreshTokenLength
}

// RefreshSupersededCode is the `error` code of the 409 a refresh answers with when
// another refresh of the same session replaced its token moments ago.
//
// The SPA retries a refresh once on exactly this status AND this code, so it is a
// contract between two languages, and it is spelled here once. It is also why the
// 409 is written by Refresh itself (refuseStaleRefresh) and not returned as a
// fiber.Error: errorHandler derives the envelope's `error` from the status, and
// for 409 that is "conflict" — a client matching this code would never see it,
// and the race fix would silently not work. Three tests hold it in place: the raw
// body of the answer (TestRefresh_TheSupersededAnswerCarriesItsOwnEnvelope), the
// literal itself (TestRefreshSupersededCode_IsTheLiteralTheClientMatches), and the
// client's source (TestGuard_TheSPAMatchesTheSupersededContract).
const RefreshSupersededCode = "refresh_superseded"

// authDBTimeout bounds the database work of one Refresh, Logout, LogoutAll or
// ChangePassword.
//
// None has a deadline of its own — in Fiber the request context is
// context.Background() unless a handler sets one, so it never ends — and a pool
// with no free connection makes a caller wait for one for as long as its context
// lasts. Refresh in particular holds a connection (its transaction) while it
// waits, so an unbounded wait is not only a hang: enough of them at once hold
// every connection the process has, and then nothing in it can query the database
// at all. The bound turns that into an answer, a 503 with nothing decided, and it
// is generous for what the work is — a handful of indexed statements.
//
// It is ONE bound for the work that DECIDES the answer. The steps that enforce or
// record a decision already made — revoking the session of a refused account, the
// race check that shapes a refusal, the audit row and the event it publishes, the
// Redis cleanup — each run under a deadline of their own (authFollowUpTimeout), so
// that a request which spent its budget deciding can still do them: they would
// otherwise run on whatever was left, and a benign race loser would be answered
// with the wipe the 409 exists to prevent. The API reference states the figure.
const authDBTimeout = 15 * time.Second

// authFollowUpTimeout bounds each follow-up of a decision on its own, from a
// fresh start: short, because a follow-up is one statement or one command, and
// still a bound, because a follow-up that waited for ever would hold the response
// the decision has already been made for.
const authFollowUpTimeout = 5 * time.Second

// releaseTxTimeout bounds the rollback releaseTx sends, on a context of its own:
// the request's has usually just run out, and a rollback sent on it would never
// leave. It is part of the worst case a client has to allow for when a refresh is
// refused — the transaction is rolled back, the refused account's session revoked
// and a changed role audited, each under a bound of its own — and the API
// reference states that sum.
const releaseTxTimeout = 5 * time.Second

// logoutUnconfirmedMessage is what a sign-out says when it could not be confirmed.
// Its own Set-Cookie has already deleted the browser's cookie by then, so "try
// again" would be false advice: a browser's second attempt carries no token and
// is answered 200 for nothing. The message says what is true instead.
const logoutUnconfirmedMessage = "The sign-out could not be confirmed and the session may still be active. " +
	"A client that sent its refresh token in the request body can retry with it; " +
	"otherwise sign in again and use \"Sign Out All Devices\"."

// logoutAllUnconfirmedMessage is what LogoutAll says when its revoke could not be
// completed (the database unreachable, too busy, or out of the bound). Unlike Logout it clears nothing on failure — the caller is still signed
// in, with the cookie they sent — so "try again" is true here, and the sessions
// are said to be possibly still active because the revoke may have landed after
// the caller stopped waiting, or not at all.
const logoutAllUnconfirmedMessage = "Your sessions could not be revoked right now and may still be active; " +
	"you are still signed in here, so please try again shortly."

// The answers of ChangePassword when its transaction does not complete. The
// password and the end of the user's sessions are ONE transaction, so there are
// only three things to tell the user: it was not done, it was not done because
// something else got there first, or it may have been.
//
// passwordNotChangedUnavailableMessage and passwordNotChangedMessage are for a
// failure before the COMMIT, a COMMIT the server itself answered or that never
// went out, or one that reading the user back showed had not landed: the
// transaction rolled back and nothing changed — the old password and every session
// stand, and retrying is safe. passwordChangedMeanwhileMessage (a 409) is for an
// update that matched no row: the password it was conditional on is no longer the
// one in effect, or the account is gone, so nothing was changed either.
// passwordChangeUnconfirmedMessage is for a COMMIT whose outcome neither it nor the
// read that follows it could settle: the new password may be in effect, and with it
// the end of EVERY session of the account, the caller's included. If the new
// password is refused, the old one is still in effect. passwordAccountRemovedMessage
// (a 409) is for that read finding the account gone: whichever way the COMMIT went,
// there is no password left to change or to sign in with, and "try the new
// password" would send the user nowhere.
const (
	passwordNotChangedUnavailableMessage = "The database could not be reached or did not answer in time, and your password was NOT changed; please try again shortly."
	passwordNotChangedMessage            = "Failed to change the password; your password was NOT changed."
	passwordChangedMeanwhileMessage      = "Your password was changed by another request, or your account was removed, while this one was being processed; nothing was changed."
	passwordAccountRemovedMessage        = "Your account was removed while this request was being processed, so there is no password to change and none to sign in with."
	passwordChangeUnconfirmedMessage     = "Your password change could not be confirmed: the new password may be in effect, with every session of yours, this one included, signed out. " +
		"Try signing in with the new password; if it is refused, the old password is still in effect. Use \"Sign Out All Devices\" to be sure."
)

// logoutMatchedPreviousToken is the audit detail a sign-out leaves when it ended
// its session by the token that session had one rotation ago (see
// SessionManager.FindSessionForLogout). Audit details are readable by every
// Viewer, so this marker says only THAT it happened — never which token, hash or
// when.
const logoutMatchedPreviousToken = `{"matched":"previous_token"}`

// 13 of the 15 routes are declared in internal/api/registry_auth.go — five
// Public, seven SelfService and the Deferred console-token mint — which states
// their parameters and how each is authorized.
//
// Register and Logout stay in router.go, and it is a vocabulary gap rather than
// a parameter type: both are mounted with authOptional, which parses a session
// if one is presented and lets the request through either way. Register READS
// c.Locals("role") to decide whether the caller may create an account, and
// Logout reads c.Locals("user_id") for its ownership cross-check — neither of
// which a Public declaration would populate, since Public installs no
// authentication middleware at all. See registerAuthEndpoints.
//
// What stays here is everything a declaration cannot see: the first-user
// advisory lock, the constant-time login failure paths, the role-rotation guard
// on refresh, the session ownership check, and the "auth_source must be local"
// refusals on the profile and password edits.

// txBeginner is the one thing the auth handler needs from the connection pool:
// a transaction. It is an interface so a test can hand Refresh a transaction
// whose statements it controls and answers — the refresh races this file's
// comments describe can only be driven by deciding what each statement reports,
// and a real pool cannot be told to. *pgxpool.Pool satisfies it, and
// NewAuthHandler is the only place that converts one.
type txBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	pool           txBeginner
	queries        *db.Queries
	jwtService     *auth.JWTService
	sessionManager *auth.SessionManager
	rbac           *auth.RBACEngine
	eventPub       *events.Publisher
	ldapHandler    *LDAPHandler
	oidcHandler    *OIDCHandler
	totpHandler    *TOTPHandler

	// dbTimeout bounds Refresh's, Logout's, LogoutAll's and ChangePassword's
	// deciding database work; authDBTimeout when zero. followUpTimeout bounds each
	// follow-up of a decision; authFollowUpTimeout when zero. Fields rather than
	// constants so a test can make starvation fail in milliseconds instead of
	// waiting out the production bounds.
	dbTimeout       time.Duration
	followUpTimeout time.Duration
}

type totpRequiredResponse struct {
	TOTPRequired     bool   `json:"totp_required"`
	TOTPPendingToken string `json:"totp_pending_token"`
}

// NewAuthHandler creates a new auth handler. pool is required for the
// register-time advisory-lock transaction; passing nil is supported only
// for unit tests that exercise validation paths above the DB layer.
// eventPub is a constructor parameter rather than one of the Set* optional
// dependencies below on purpose: it is what carries auth events to the
// audit_entry WS feed and the syslog collector, and a setter that a future
// wiring change forgets to call would silently drop them again — which is
// exactly how they went missing before.
func NewAuthHandler(pool *pgxpool.Pool, queries *db.Queries, jwtSvc *auth.JWTService, sessMgr *auth.SessionManager, rbac *auth.RBACEngine, eventPub *events.Publisher) *AuthHandler {
	h := &AuthHandler{
		queries:        queries,
		jwtService:     jwtSvc,
		sessionManager: sessMgr,
		rbac:           rbac,
		eventPub:       eventPub,
	}
	// Assigned only when there is a pool. A nil *pgxpool.Pool stored in the
	// interface field would make `h.pool == nil` — Register's guard for the unit
	// tests that pass none — false, and the first Begin would then dereference
	// the nil pointer instead of reporting "registration unavailable".
	if pool != nil {
		h.pool = pool
	}
	return h
}

// SetLDAPHandler sets the LDAP handler reference for LDAP-aware login.
func (h *AuthHandler) SetLDAPHandler(lh *LDAPHandler) {
	h.ldapHandler = lh
}

// SetOIDCHandler sets the OIDC handler reference for SSO-aware login and token exchange.
func (h *AuthHandler) SetOIDCHandler(oh *OIDCHandler) {
	h.oidcHandler = oh
}

// SetTOTPHandler sets the TOTP handler reference for TOTP-aware login.
func (h *AuthHandler) SetTOTPHandler(th *TOTPHandler) {
	h.totpHandler = th
}

// deviceInfoFromRequest derives the DeviceInfo for a session being created.
// Any API client may set X-Nexara-Device-Type (mobile|desktop), X-Nexara-Device-Name
// and X-Nexara-Device-ID to tag its sessions; browsers send none of them, so a
// session with no headers is tagged "web". Purely descriptive metadata — it does
// not change authentication behaviour.
func deviceInfoFromRequest(c fiber.Ctx) auth.DeviceInfo {
	deviceType := strings.ToLower(strings.TrimSpace(c.Get("X-Nexara-Device-Type")))
	switch deviceType {
	case "mobile", "desktop":
		// valid, keep as-is
	default:
		deviceType = "web"
	}

	deviceName := strings.TrimSpace(c.Get("X-Nexara-Device-Name"))
	if len(deviceName) > 128 {
		deviceName = deviceName[:128]
	}

	deviceID := strings.TrimSpace(c.Get("X-Nexara-Device-ID"))
	if len(deviceID) > 128 {
		deviceID = deviceID[:128]
	}

	return auth.DeviceInfo{
		Name: deviceName,
		Type: deviceType,
		ID:   deviceID,
	}
}

type registerRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

// logoutRequest is the optional POST /auth/logout body, for clients that send
// the refresh token explicitly instead of relying on the cookie.
type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// String, GoString, LogValue and MarshalJSON keep the live refresh token out
// of fmt, slog and encoding/json. Logout runs next to the session-revocation
// code, which logs on every failure path, and `%v` of the decoded body is the
// natural thing to add when a logout is being rejected for reasons nobody can
// see.
//
// The json tag stays: this struct is DECODED from the request body, so
// json:"-" would silently stop body-carrying clients logging out. Only the
// marshal direction is overridden — UnmarshalJSON is a separate interface, so
// decoding is untouched.
//
// MarshalJSON is here even though nothing in the tree marshals a
// logoutRequest, and the reason is worth stating because an earlier version
// of this type left it out on exactly that argument. Production builds its
// logger with slog.NewJSONHandler (cmd/nexara/main.go:64 and :272), and for a
// value that is not itself a LogValuer the JSON handler MARSHALS it where the
// text handler formats it with %+v. A logoutRequest reached as an exported
// field of some context struct — `slog.Error("logout failed", "ctx",
// struct{ Req logoutRequest; IP string }{req, ip})` — therefore goes through
// encoding/json, not through String, and without this method it writes the
// live refresh token to stdout in cleartext. slog.Any on the type ITSELF is
// safe either way, because LogValue resolves first; it is the wrapper shape
// that needs this. So a slog call IS a serialisation here, and "nothing
// marshals it" was never the right test.
//
// It emits the REDACTED marker rather than an empty object so a log line says
// which it was. The round-trip that implies — marshal then unmarshal yields
// the literal "REDACTED" as a token — is not reachable: nothing marshals this
// type outside a log line, and anything that starts to must carry the token
// itself.
//
// The type has exactly one field and that field IS the secret, so the usual
// non-vacuity pairing — assert a non-credential value SURVIVES the rendering
// — has nothing to reach for. That does not make the type untestable, only
// differently testable: requiring the marker REDACTED to be PRESENT is the
// survivor half. Unlike exact equality against a whole literal it survives
// any rewording THAT KEEPS THE MARKER, and it fails against a String() that
// returns "" as well as one that prints the field. It is not free of
// change-detection: a reword to something like "refresh token withheld",
// which redacts perfectly well, also fails — the marker is part of the
// contract, not incidental phrasing. The slog rows pin slightly more than the
// marker, deliberately: they match on the grouped key too
// (req.refresh_token=REDACTED, and the quoted form under the JSON handler),
// because the key is what makes LogValue killable on its own rather than
// masked by String.
// TestGuard_LogoutRequestNeverPrintsTheRefreshToken does exactly that, and
// TestCredentialRedactorsAreInTheValueMethodSet pins the value receivers.
//
// Value receivers, like every other redactor in this file: fmt, slog and
// encoding/json all skip a pointer-receiver method on a value they cannot
// address.
func (l logoutRequest) String() string {
	return "logoutRequest{refresh_token:REDACTED}"
}

// GoString covers %#v, which dispatches GoStringer rather than Stringer and
// would otherwise print the struct literal with the live token in it.
func (l logoutRequest) GoString() string {
	return `handlers.logoutRequest{RefreshToken:"REDACTED"}`
}

func (l logoutRequest) LogValue() slog.Value {
	return slog.GroupValue(slog.String("refresh_token", "REDACTED"))
}

func (l logoutRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		RefreshToken string `json:"refresh_token"`
	}{"REDACTED"})
}

type authResponse struct {
	User        authUserResponse `json:"user"`
	AccessToken string           `json:"access_token"`
	// RefreshToken is always empty, and MarshalJSON below is what makes that
	// true — not the three construction sites, which merely agree with it.
	// The refresh token is delivered solely as an HttpOnly cookie (see
	// RefreshCookieName); the native app that used to read it from the body
	// was removed in v1.9.x.
	//
	// The field and its live json tag stay because the shape is a published
	// contract: frontend/src/types/api.ts declares refresh_token
	// NON-OPTIONAL and docs/api-reference.md documents it as present and
	// empty, so json:"-" — the remedy used on auth.TokenPair, which nothing
	// consumes — would break existing clients here.
	RefreshToken string   `json:"refresh_token"`
	ExpiresAt    int64    `json:"expires_at"`
	Permissions  []string `json:"permissions"`
}

// MarshalJSON turns three call-site invariants into one type invariant.
//
// Every response body this type produces carries "refresh_token":"" whatever
// the construction site put in the field, so a fourth construction site — a
// new endpoint, an OIDC path, a debug handler — cannot reopen the v1.9.x leak
// by forgetting the line. The three existing `RefreshToken: ""` literals are
// kept as defence in depth; they are no longer what enforces this.
//
// AccessToken is deliberately NOT blanked. Unlike String, GoString and
// LogValue below, this method is the wire format, and the access token is the
// thing the client came for.
//
// State that asymmetry as the trade-off it is, not as an achieved invariant.
// One method cannot serve both the wire and the log: because MarshalJSON
// keeps AccessToken, an authResponse reached as an exported field of a
// wrapper under slog's JSON handler — which marshals rather than formatting —
// logs a live access token. slog.Any on the response ITSELF is safe, since
// LogValue resolves first, and so is every fmt verb and the text handler;
// TestGuard_AuthResponseNeverPrintsEitherToken covers exactly those. Nothing
// logs an authResponse today, in any shape. Do not start: log the user id, not
// the response.
//
// Copy-and-blank rather than embed-and-shadow: embedding an anonymous struct
// would reorder the fields and change the emitted bytes, and the byte shape is
// the contract. `alias` must be a DEFINED type, not a Go type alias — written
// `type alias = authResponse` it would be the same type, keep this method in
// its method set, and recurse until the stack dies.
//
// NodeCertificate.MarshalJSON in internal/proxmox/types.go shares only that
// `type Alias T` trick; it is the embed-and-shadow variant, which is the one
// deliberately NOT used here. Read it for the defined-type idiom, not for the
// structure.
//
// VALUE receiver: all three sites hand `authResponse{...}` to c.JSON as an
// unaddressable value, and encoding/json skips a pointer-receiver MarshalJSON
// on one of those — a pointer receiver would compile, lint clean and blank
// nothing.
func (a authResponse) MarshalJSON() ([]byte, error) {
	type alias authResponse
	blanked := alias(a)
	blanked.RefreshToken = ""
	return json.Marshal(blanked)
}

// String, GoString and LogValue redact BOTH tokens. AccessToken is a live
// bearer JWT for its whole TTL, so a `%v` of an authResponse in an error, or a
// slog.Any on the way out of a login handler, hands out a working session.
// fmt reaches String for %v/%s/%q/%x/%X/%+v/fmt.Sprint and error wrapping, and
// GoStringer — not Stringer — for %#v, which is why GoString is separate.
//
// The user identity and expiry stay visible; they are what makes a redacted
// line worth logging, and they are already Viewer-readable.
func (a authResponse) String() string {
	return "authResponse{user:" + a.User.Email + " access_token:REDACTED refresh_token:REDACTED expires_at:" +
		strconv.FormatInt(a.ExpiresAt, 10) + "}"
}

func (a authResponse) GoString() string {
	return `handlers.authResponse{User:handlers.authUserResponse{Email:"` + a.User.Email +
		`"}, AccessToken:"REDACTED", RefreshToken:"REDACTED", ExpiresAt:` +
		strconv.FormatInt(a.ExpiresAt, 10) + `}`
}

func (a authResponse) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("user_id", a.User.ID.String()),
		slog.String("email", a.User.Email),
		slog.Int64("expires_at", a.ExpiresAt),
	)
}

type authUserResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
}

// Register handles user registration.
//
// First user is auto-promoted to admin and requires no auth. Subsequent
// users require admin auth (checked via authOptional middleware + handler
// check).
//
// Order of operations (Findings #17, #18):
//   - Cheap validation (body shape, email format, password complexity) runs
//     first, before any DB or bcrypt work — bcrypt at cost 12 burns ~80–100ms
//     per call, so an unauthenticated attacker spamming /auth/register would
//     otherwise force every request through an expensive hash even though
//     the admin gate would reject all of them.
//   - The count-then-create pair runs inside a transaction guarded by
//     pg_advisory_xact_lock so two simultaneous registrations on a fresh
//     install can't both observe count == 0 and both promote themselves to
//     admin. The lock is released automatically when the transaction commits
//     or rolls back.
//   - HashPassword runs only after the admin gate passes. Failed gates burn
//     no bcrypt CPU.
func (h *AuthHandler) Register(c fiber.Ctx) error {
	var req registerRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Email == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Email and password are required")
	}

	if _, err := mail.ParseAddress(req.Email); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid email address")
	}

	// Validate password complexity before any DB or bcrypt work — this is a
	// pure-string check that costs microseconds, while bcrypt costs ~80–100ms.
	if err := auth.ValidatePasswordStrength(req.Password); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if h.pool == nil {
		return fiber.NewError(fiber.StatusInternalServerError, "registration unavailable")
	}

	tx, err := h.pool.Begin(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to start transaction")
	}
	// Rollback runs after request context is already cancelled on a happy
	// commit (no-op since the tx is closed) or after an early return on
	// failure (the request context may also be cancelled). Bound the
	// rollback context so a hung Postgres can't pin the connection — and
	// log non-ErrTxClosed failures so a leaked transaction is observable.
	defer func() {
		rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer rbCancel()
		if rbErr := tx.Rollback(rbCtx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.Warn("register: transaction rollback failed", "error", rbErr)
		}
	}()

	// Serialise concurrent first-user registrations. The transaction-scoped
	// advisory lock forces any other Register call to block here until we
	// commit or roll back, so the count we read on the next line is the
	// authoritative answer for this candidate registration. Lock auto-
	// releases on COMMIT/ROLLBACK.
	if _, err := tx.Exec(c.Context(), "SELECT pg_advisory_xact_lock($1)", firstUserAdvisoryLockKey); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to acquire registration lock")
	}

	q := h.queries.WithTx(tx)

	count, err := q.CountUsers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check user count")
	}

	role := "user"
	if count == 0 {
		role = "admin"
	} else {
		callerRole, _ := c.Locals("role").(string)
		if callerRole != "admin" {
			// Reject before bcrypt — this is the entire point of #17.
			return fiber.NewError(fiber.StatusForbidden, "Only admins can register new users")
		}
	}

	// Admin gate passed (or this is the first user). Hash the password now.
	// Note: bcrypt runs while we still hold the advisory lock and a pool
	// connection. That's deliberate — non-admin attempts were already
	// rejected above with no bcrypt work, so an unauthenticated attacker
	// cannot pin pool slots through this path. The remaining path
	// (authenticated admin or first-user) is bounded by the auth limiter
	// (15 req/min/IP) and is intentionally serialised so concurrent
	// first-user registrations cannot both observe count == 0.
	hashedPassword, err := auth.HashPassword(req.Password)
	if err != nil {
		// ValidatePasswordStrength already ran above, so password-class errors
		// here would be a logic bug. Any other error path is a server fault.
		if errors.Is(err, auth.ErrPasswordTooShort) || errors.Is(err, auth.ErrPasswordTooLong) || errors.Is(err, auth.ErrPasswordWeak) {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to process password")
	}

	displayName := req.DisplayName
	if displayName == "" {
		displayName = req.Email
	}

	user, err := q.CreateUser(c.Context(), db.CreateUserParams{
		Email:        req.Email,
		PasswordHash: hashedPassword,
		DisplayName:  displayName,
		IsActive:     true,
		Role:         role,
	})
	if err != nil {
		if isDuplicateKeyError(err) {
			return fiber.NewError(fiber.StatusConflict, "Email already registered")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create user")
	}

	if err := tx.Commit(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to commit registration")
	}

	// Assign RBAC role: admin -> Admin, user -> Viewer
	rbacRoleID := "a0000000-0000-0000-0000-000000000003" // Viewer
	if role == "admin" {
		rbacRoleID = "a0000000-0000-0000-0000-000000000001" // Admin
	}
	if roleUUID, parseErr := uuid.Parse(rbacRoleID); parseErr == nil {
		_, _ = h.queries.AssignUserRole(c.Context(), db.AssignUserRoleParams{
			UserID:    user.ID,
			RoleID:    roleUUID,
			ScopeType: "global",
		})
	}

	accessToken, expiresAt, err := h.jwtService.GenerateAccessToken(user.ID, user.Email, user.Role)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate access token")
	}

	refreshToken, err := h.jwtService.GenerateRefreshToken()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate refresh token")
	}

	_, err = h.sessionManager.CreateSession(
		c.Context(), user.ID, refreshToken, user.Role,
		c.Get("User-Agent"), c.IP(),
		h.jwtService.RefreshTokenTTL(),
		deviceInfoFromRequest(c),
	)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create session")
	}

	details, _ := json.Marshal(map[string]string{"email": user.Email, "role": user.Role})
	AuditLogAs(c, h.queries, h.eventPub, user.ID, pgtype.UUID{}, "auth", user.ID.String(), "register", details)

	perms := h.loadPerms(c, user.ID)

	setRefreshCookie(c, refreshToken, h.jwtService.RefreshTokenTTL())

	return c.Status(fiber.StatusCreated).JSON(authResponse{
		User: authUserResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
		AccessToken:  accessToken,
		RefreshToken: "",
		ExpiresAt:    expiresAt.Unix(),
		Permissions:  perms,
	})
}

// Login handles user authentication.
//
// Lookup-first ordering: a local user's password is NEVER shipped to the
// LDAP server (closes the data leak from Finding #12 where every login
// attempt's plaintext password was sent to LDAP regardless of auth_source).
//
// Constant-time failure paths: every rejection runs a dummy bcrypt so a
// stopwatch on /auth/login cannot distinguish "real local user, bad
// password" from "no such user", "OIDC user trying password login", or
// "disabled account" (closes Finding #11 username-enumeration timing
// oracle).
//
// Residual oracle (LDAP-enabled deployments only): when LDAP is enabled
// AND the email is unknown locally, the request takes one LDAP roundtrip
// PLUS the dummy bcrypt; when LDAP is disabled it takes only the dummy
// bcrypt. The wall-clock differential leaks "this email is/isn't in your
// directory" — a strictly weaker oracle than #11 (it's about directory
// membership, not local-account existence) and unavoidable without
// disabling JIT-provisioning of new LDAP users on first login. Operators
// who accept this trade can leave it; tighter postures should disable JIT
// and pre-provision LDAP users.
func (h *AuthHandler) Login(c fiber.Ctx, p *apischema.Params) error {
	email := p.String("email")
	password := p.String("password")

	invalidCredentials := fiber.NewError(fiber.StatusUnauthorized, "Invalid email or password")

	user, err := h.queries.GetUserByEmail(c.Context(), email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up user")
		}
		// No local user with this email. If LDAP is enabled, try it —
		// the LDAP path handles JIT-provisioning of new directory users.
		// Otherwise fall through to a dummy bcrypt so timing matches the
		// "real user, bad password" path.
		if ldapUser, ok := h.tryLDAPLogin(c, email, password); ok {
			return h.issueOrTOTP(c, ldapUser, "ldap_login")
		}
		auth.RunDummyBcrypt(password)
		return invalidCredentials
	}

	switch user.AuthSource {
	case "ldap":
		if ldapUser, ok := h.tryLDAPLogin(c, email, password); ok {
			return h.issueOrTOTP(c, ldapUser, "ldap_login")
		}
		// Pad failure to keep timing roughly aligned with the local-bcrypt
		// path. LDAP roundtrip dominates wall-clock anyway, so this is a
		// best-effort defence-in-depth, not a strict equaliser.
		auth.RunDummyBcrypt(password)
		return invalidCredentials
	case "oidc":
		// OIDC-sourced users cannot log in via password. Burn equivalent
		// CPU so the response time is indistinguishable from a real
		// local user with a wrong password.
		auth.RunDummyBcrypt(password)
		return invalidCredentials
	}

	// auth_source == "local" from here.
	if !user.IsActive {
		auth.RunDummyBcrypt(password)
		return invalidCredentials
	}

	if err := auth.CheckPassword(user.PasswordHash, password); err != nil {
		return invalidCredentials
	}

	return h.issueOrTOTP(c, user, "login")
}

// tryLDAPLogin attempts LDAP authentication. Returns the DB user and true on success.
//
// Failure modes are logged at WARN with the email but never the password,
// so an admin investigating "why is LDAP login broken" gets a signal without
// the silent-failure mode the original implementation suffered from.
//
// Residual race (acknowledged): if a local user with the same email is
// being created concurrently with a JIT-provisioning LDAP login, the LDAP
// path still ships the password to the LDAP server before the duplicate-key
// path discovers the conflict. Vanishingly narrow window in practice and
// no different from the LDAP-bound attempt the user was about to make
// anyway, but worth documenting.
func (h *AuthHandler) tryLDAPLogin(c fiber.Ctx, email, password string) (db.User, bool) {
	if h.ldapHandler == nil {
		return db.User{}, false
	}

	ldapCfg, err := h.queries.GetEnabledLDAPConfig(c.Context())
	if err != nil {
		// pgx.ErrNoRows here is the common case — LDAP just isn't enabled.
		// Don't log that. Other DB errors are operationally interesting.
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("ldap login: failed to load config", "error", err)
		}
		return db.User{}, false
	}

	authCfg, err := h.ldapHandler.BuildLDAPConfigFromDB(ldapCfg)
	if err != nil {
		slog.Warn("ldap login: invalid stored config", "email", email, "error", err)
		return db.User{}, false
	}

	client := auth.NewLDAPClient(authCfg)
	ldapUser, err := client.Authenticate(email, password)
	if err != nil {
		// LDAP rejection / connection failure / search failure — typed
		// errors carry the class. Log so admins can distinguish "wrong
		// password" from "directory unreachable" without us leaking which
		// to the client.
		slog.Warn("ldap login: authentication failed", "email", email, "error", err)
		return db.User{}, false
	}

	// Determine the email to use
	userEmail := ldapUser.Email
	if userEmail == "" {
		userEmail = email
	}

	// Look up or JIT-create the user
	user, err := h.queries.GetUserByEmailAndSource(c.Context(), db.GetUserByEmailAndSourceParams{
		Email:      userEmail,
		AuthSource: "ldap",
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("ldap login: failed to look up user", "email", userEmail, "error", err)
			return db.User{}, false
		}

		// JIT provision
		displayName := ldapUser.DisplayName
		if displayName == "" {
			displayName = userEmail
		}
		user, err = h.queries.CreateLDAPUser(c.Context(), db.CreateLDAPUserParams{
			Email:       userEmail,
			DisplayName: displayName,
		})
		if err != nil {
			// Handle race condition: another concurrent request may have created this user
			if isDuplicateKeyError(err) {
				user, err = h.queries.GetUserByEmailAndSource(c.Context(), db.GetUserByEmailAndSourceParams{
					Email:      userEmail,
					AuthSource: "ldap",
				})
				if err != nil {
					slog.Warn("ldap login: post-race lookup failed", "email", userEmail, "error", err)
					return db.User{}, false
				}
			} else {
				slog.Warn("ldap login: JIT provisioning failed", "email", userEmail, "error", err)
				return db.User{}, false
			}
		}
	} else if ldapUser.DisplayName != "" && ldapUser.DisplayName != user.DisplayName {
		// Update display name if changed
		if updated, err := h.queries.UpdateLDAPUserProfile(c.Context(), db.UpdateLDAPUserProfileParams{
			ID:          user.ID,
			DisplayName: ldapUser.DisplayName,
		}); err == nil {
			user = updated
		}
	}

	if !user.IsActive {
		slog.Warn("ldap login: account disabled", "email", userEmail, "user_id", user.ID)
		return db.User{}, false
	}

	// Sync RBAC roles from LDAP group mapping
	mapping := make(map[string]string)
	if len(ldapCfg.GroupRoleMapping) > 0 {
		_ = json.Unmarshal(ldapCfg.GroupRoleMapping, &mapping)
	}
	h.ldapHandler.SyncUserRoles(c, user.ID, ldapUser.Groups, mapping, ldapCfg.DefaultRoleID)

	return user, true
}

// issueTokens creates JWT + session for the given user and returns the auth response.
func (h *AuthHandler) issueTokens(c fiber.Ctx, user db.User, auditAction string) error {
	accessToken, expiresAt, err := h.jwtService.GenerateAccessToken(user.ID, user.Email, user.Role)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate access token")
	}

	refreshToken, err := h.jwtService.GenerateRefreshToken()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate refresh token")
	}

	_, err = h.sessionManager.CreateSession(
		c.Context(), user.ID, refreshToken, user.Role,
		c.Get("User-Agent"), c.IP(),
		h.jwtService.RefreshTokenTTL(),
		deviceInfoFromRequest(c),
	)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create session")
	}

	details, _ := json.Marshal(map[string]string{"email": user.Email, "ip": c.IP()})
	AuditLogAs(c, h.queries, h.eventPub, user.ID, pgtype.UUID{}, "auth", user.ID.String(), auditAction, details)

	perms := h.loadPerms(c, user.ID)

	setRefreshCookie(c, refreshToken, h.jwtService.RefreshTokenTTL())

	return c.JSON(authResponse{
		User: authUserResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
		AccessToken:  accessToken,
		RefreshToken: "",
		ExpiresAt:    expiresAt.Unix(),
		Permissions:  perms,
	})
}

// IssueTokens is the exported version of issueTokens for cross-handler use.
func (h *AuthHandler) IssueTokens(c fiber.Ctx, user db.User, auditAction string) error {
	return h.issueTokens(c, user, auditAction)
}

type consoleTokenResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

// ConsoleToken mints a short-lived (60 second), scope-locked JWT that can ONLY
// be used to open a single console WebSocket matching cluster_id/node/vmid/type.
// Browsers cannot attach Authorization headers to a WebSocket upgrade, so the
// token is passed via query string instead.
//
// The underlying access token + RBAC check happens first — minting requires
// the dedicated console:<resource> permission on the target cluster (view:*
// is deliberately not enough; see migration 000078).
func (h *AuthHandler) ConsoleToken(c fiber.Ctx, p *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	clusterID := p.String("console_cluster_id")
	clusterUUID, err := parseParamUUID(clusterID)
	if err != nil {
		return err
	}
	node := p.String("node")
	consoleType := p.String("type")
	vmid := int(p.Int("vmid"))

	// The type's VOCABULARY is the declaration's enum; what stays here is the
	// mapping from type to RBAC resource, and the two cross-field rules about
	// vmid that no per-parameter schema can express.
	var resource string
	switch consoleType {
	case "node_shell":
		resource = "node"
		if vmid != 0 {
			return fiber.NewError(fiber.StatusBadRequest, "vmid must be omitted for node_shell")
		}
	case "vm_serial", "vm_vnc":
		resource = "vm"
		if vmid <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "vmid is required for vm console")
		}
	case "ct_attach", "ct_vnc":
		resource = "container"
		if vmid <= 0 {
			return fiber.NewError(fiber.StatusBadRequest, "vmid is required for container console")
		}
	default:
		return fiber.NewError(fiber.StatusBadRequest, "invalid console type")
	}

	// Console tokens scope to a specific cluster — gate on per-cluster perm so
	// a user with console:vm only on cluster X cannot mint a console token
	// for cluster Y.
	//
	// The action is the dedicated "console" family (migration 000078), NOT
	// "view": the built-in Viewer role holds every view:* permission, and
	// gating on view:* handed read-only accounts the hypervisor's shell (at
	// its login prompt: Proxmox skips the login only for root@pam, never for
	// an API token) and every guest console, which for a container with
	// cmode=shell is a root shell inside it, with no login.
	// console_token_authz_test.go pins this invariant.
	if err := requireClusterPerm(c, "console", resource, clusterUUID); err != nil {
		return err
	}

	// Fetch user details so we can embed them in the scoped JWT.
	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "User not found")
	}
	if !user.IsActive {
		return fiber.NewError(fiber.StatusUnauthorized, "Account is disabled")
	}
	// The token carries the node to /ws/console and /ws/vnc, which open a
	// termproxy or vncproxy at /nodes/{node}/… and dial its vncwebsocket, so
	// it must be one of the cluster's before it is signed. Checked here — after
	// the permission check, and after the account checks, so a disabled
	// account holding a token that has not expired yet gets its 401 whatever
	// node it names — rather than by the registry
	// (Endpoint.NodesCheckedByHandler), because the cluster is in the body.
	// The token lives 60 seconds, so the WS side does not check again.
	if err := requireNodeInCluster(c, lookupOf(h.queries), clusterUUID, node); err != nil {
		return err
	}

	// Console tokens are bound to a single immediate WS upgrade — the SPA
	// mints and connects within ~50ms. 60 seconds is
	// generous but tight enough that a leaked URL or proxy access log
	// entry is unusable by the time it surfaces. Reduced from 5 minutes
	// per remediation 2.7 (≤60s scope for all WS-bound JWTs).
	const consoleTokenTTL = 60 * time.Second
	token, _, err := h.jwtService.GenerateConsoleToken(
		user.ID, user.Email, user.Role,
		auth.ConsoleScope{
			ClusterID: clusterID,
			Node:      node,
			VMID:      vmid,
			Type:      consoleType,
		},
		consoleTokenTTL,
	)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to mint console token")
	}

	// Honour silent only for VNC types — the "preview thumbnail" use case.
	// Terminal/serial mints are always user-initiated and always audit.
	silent := p.Bool("silent") && (consoleType == "vm_vnc" || consoleType == "ct_vnc")
	if silent {
		slog.Info("console_token_mint (silent)",
			"user_id", userID,
			"cluster_id", clusterID,
			"node", node,
			"vmid", vmid,
			"type", consoleType,
		)
	} else {
		// Field by field rather than p.Raw(): view:audit is a default Viewer
		// grant, and Raw() would publish whatever the caller sent — including
		// any parameter this route later gains.
		details, _ := json.Marshal(map[string]any{
			"cluster_id": clusterID,
			"node":       node,
			"vmid":       vmid,
			"type":       consoleType,
		})
		// The mint is scoped to one cluster — the same clusterUUID the
		// permission check above gates on — so the audit row carries it.
		// Auditing NULL here marked the mint a global entry, which the scoped
		// audit reads show only to holders of global view:audit: the operator
		// who holds console:vm on this cluster, and who just opened the
		// console, could not see their own mint.
		AuditLogAs(c, h.queries, h.eventPub, userID, ClusterUUID(clusterUUID), "auth", userID.String(), "console_token_mint", details)
	}

	return c.JSON(consoleTokenResponse{
		Token:     token,
		ExpiresIn: int(consoleTokenTTL.Seconds()),
	})
}

type wsTokenResponse struct {
	Token     string `json:"token"`
	ExpiresIn int    `json:"expires_in"`
}

// WSToken mints a short-lived (60s) JWT whose only valid use is upgrading the
// generic /ws hub. It carries the same UserID/Role as the access token but
// is restricted by Claims.WSScope == "hub" — the WS auth middleware refuses
// it on console paths, and refuses regular access tokens on the hub.
//
// The point is to keep the long-lived access token out of the WebSocket
// upgrade entirely. The hub token can be carried in `?token=` or in the
// `Sec-WebSocket-Protocol: nexara.token, nexara.token.<jwt>` subprotocol
// header (preferred — keeps the JWT out of proxy access logs and Referer).
func (h *AuthHandler) WSToken(c fiber.Ctx, _ *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "User not found")
	}
	if !user.IsActive {
		// Audit the deny — a disabled user trying to open a WS is a
		// signal worth surfacing in the log. (Successful mints aren't
		// audited; every reconnect mints, so it'd be log noise.)
		AuditLogAs(c, h.queries, h.eventPub, userID, pgtype.UUID{}, "auth", userID.String(), "ws_token_denied_inactive", nil)
		return fiber.NewError(fiber.StatusUnauthorized, "Account is disabled")
	}

	const wsHubTokenTTL = 60 * time.Second
	token, _, err := h.jwtService.GenerateWSHubToken(user.ID, user.Email, user.Role, wsHubTokenTTL)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to mint ws token")
	}

	return c.JSON(wsTokenResponse{
		Token:     token,
		ExpiresIn: int(wsHubTokenTTL.Seconds()),
	})
}

// Refresh exchanges a valid refresh token for a new token pair. The refresh
// token is read from the JSON body when present (mobile path) and otherwise
// from the HttpOnly cookie set on prior auth responses (web path).
//
// A refresh token is single-use: the rotation below only succeeds against the
// session's current token, and only while the session is live, so of two
// refreshes presenting the same token one wins, and a refresh that validated just
// before its session was revoked issues nothing. A refusal is one of:
//
//   - 401, cookie cleared: the token is not the live session's — unknown, rotated
//     away, revoked, expired, longer than any refresh token can be (which is
//     refused before it is looked up) — or the session was revoked under the
//     refresh;
//   - 409 refresh_superseded, cookie left alone: the token is the one another
//     refresh of the same live session replaced seconds ago, so this is the loser
//     of a race, and the cookie jar may already hold the winner's newer cookie;
//   - 503, cookie and session left alone: a database call could not be completed
//     — a lookup, the transaction, the rotation, the commit — because the database
//     was unreachable, too busy or out of the bound (isTransientDBError), which
//     says nothing about the token and must not be answered as if it did; a
//     database failure of any other kind is a defect and a 500, with the cookie
//     and the session left alone in the same way.
//
// Its database work is bounded by authDBTimeout, so that a pool with no free
// connection is a 503 and not a request that waits for ever.
func (h *AuthHandler) Refresh(c fiber.Ctx, p *apischema.Params) error {
	// Body is optional — web clients post `{}` and rely on the cookie.
	refreshToken := p.String("refresh_token")

	if refreshToken == "" {
		refreshToken = readRefreshTokenFromCookie(c)
	}

	if refreshToken == "" {
		// Auth state, not a malformed request — return 401 so the SPA's
		// refresh-failure path treats it consistently with stale-cookie
		// rejections, and so an attacker scanning for /auth/refresh cannot
		// distinguish "no cookie" from "stale cookie" via the status code.
		clearRefreshCookie(c)
		return fiber.NewError(fiber.StatusUnauthorized, "Refresh token is required")
	}

	// A token longer than any refresh token can be is not one of ours, however it
	// arrived: answered as the invalid token it is — the same 401 and the same
	// cleared cookie as any other — before it is hashed, looked up or logged. The
	// body parameter is already bounded by the route's schema; the cookie is not.
	if refreshTokenTooLong(refreshToken) {
		clearRefreshCookie(c)
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired refresh token")
	}

	// The work below that decides the answer shares one bound, and that includes
	// anything that reads c.Context() (see dbContext). The steps that enforce or
	// record a decision — the revoke of a refused account's session, the race check
	// behind a refusal, the audit row and its event — each get a deadline of their
	// own, so that a request which spent its budget deciding can still do them; see
	// authDBTimeout and followUp.
	ctx, cancel := h.dbContext(c)
	defer cancel()

	session, err := h.sessionManager.ValidateRefreshToken(ctx, refreshToken)
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidToken) {
			// The lookup failed; nothing is known about the token — or its session,
			// which is why this one line carries no session id. See
			// refreshLookupFailed for why that is not the stale-cookie answer.
			return refreshDBFailed("session lookup", "Failed to look up the session", uuid.Nil, err)
		}
		return h.refuseStaleRefresh(ctx, c, refreshToken)
	}

	// The permissions go out in the response, and they are read HERE, before the
	// transaction, for two reasons.
	//
	// They come from the pool, and a request that holds a transaction must not ask
	// the pool for a second connection (see release below). And once the rotation
	// has committed, what is left must be work that cannot stall: a refresh that
	// cannot answer after it committed has rotated the cookie and told nobody, and
	// the old one is then only the previous token (a 409 for a few seconds, a 401
	// after). So the last database read happens while nothing has been changed yet.
	//
	// A read that fails is a refresh that could not be completed: nothing rotated,
	// nothing issued. It used to be answered with an empty list and a success,
	// which signs the user in to a page that shows them nothing.
	perms, err := h.loadPermsStrict(ctx, session.UserID)
	if err != nil {
		return refreshDBFailed("permission lookup", "Failed to load permissions", session.ID, err)
	}

	// The user load, the role-rotation guard and the refresh-token rotation run
	// in one transaction (Finding A11), so they commit or roll back together: a
	// refresh that is refused at the rotation leaves nothing behind.
	//
	// What that does NOT give is a frozen view. At READ COMMITTED each statement
	// reads the latest committed state, so the role guard is check-then-act, and
	// an admin demotion that commits between the user read and this transaction's
	// commit is not excluded. Such a refresh behaves like one that ran an instant
	// before the demotion: its access token carries the old role until it
	// expires, and the NEXT refresh sees the changed role and forces a re-login.
	// The same instant-before reading applies to the permissions above.
	//
	// After the commit nothing here touches the database: the Redis row, which
	// nothing reads (see WriteSessionRedis), is written from its own goroutine.
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		// Could not even get a connection to begin on — the pool was exhausted for
		// the whole bound, or the database is down. Nothing was decided.
		return refreshDBFailed("transaction start", "Failed to start transaction", session.ID, err)
	}
	// release gives the transaction's connection back to the pool. It is idempotent
	// — a rollback on a transaction that is already closed answers pgx.ErrTxClosed,
	// which is not a failure here — so the deferred call is only the backstop, and an
	// explicit call before a pool query costs nothing.
	//
	// It has to be called BEFORE every query that goes to the pool rather than to
	// the transaction: that query needs a connection of its own, and this request
	// is still holding one. With enough such requests at once (one more than the
	// pool has connections, all presenting one cookie) every connection is held by
	// a request waiting for another, and nothing in the process can query the
	// database again. Each branch below that talks to the pool releases first.
	release := releaseTx(tx, "refresh")
	defer release()
	qx := h.queries.WithTx(tx)

	user, err := qx.GetUserByID(ctx, session.UserID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			// Not "the user is gone": the read failed. Revoking the session and
			// clearing the cookie over that would sign out a user whose account is
			// fine, for good, because a database hiccup coincided with a refresh.
			return refreshDBFailed("user lookup", "Failed to load the user", session.ID, err)
		}
		release()
		h.revokeRefusedSession(ctx, session.ID, "user not found")
		clearRefreshCookie(c)
		return fiber.NewError(fiber.StatusUnauthorized, "User not found")
	}

	if !user.IsActive {
		release()
		h.revokeRefusedSession(ctx, session.ID, "account disabled")
		clearRefreshCookie(c)
		return fiber.NewError(fiber.StatusUnauthorized, "Account is disabled")
	}

	// Role rotation guard: if the legacy users.role at session creation is
	// non-empty and has since changed, force re-login. Empty user_role
	// means a pre-migration session — accept it once so the upgrade path
	// does not log out every existing user; it gets populated below.
	if session.UserRole != "" && session.UserRole != user.Role {
		release()
		h.revokeRefusedSession(ctx, session.ID, "role changed")
		clearRefreshCookie(c)
		details, _ := json.Marshal(map[string]string{
			"session_role": session.UserRole,
			"current_role": user.Role,
		})
		h.withFollowUp(c, func() {
			AuditLogAs(c, h.queries, h.eventPub, user.ID, pgtype.UUID{}, "auth", user.ID.String(), "refresh_denied_role_changed", details)
		})
		return fiber.NewError(fiber.StatusUnauthorized, "Role changed; please log in again")
	}

	accessToken, expiresAt, err := h.jwtService.GenerateAccessToken(user.ID, user.Email, user.Role)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate access token")
	}

	newRefreshToken, err := h.jwtService.GenerateRefreshToken()
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate refresh token")
	}

	// The rotation is the one step that has to be atomic with the question "is
	// this still the session's live token?", because ValidateRefreshToken asked
	// it in a separate statement some milliseconds ago. When the answer has since
	// become no — a sign-out or a revoke from the session list landed, sign-out
	// everywhere, a password change, an admin, or the session expired, or another
	// refresh presenting this same cookie rotated it first — nothing changes and
	// this refresh LOST. It issues nothing: the access token generated above is
	// dropped unsent, no new cookie is set, no Redis row is written, and the
	// transaction rolls back with nothing in it. How it is refused is
	// refuseStaleRefresh's decision: a loser to a concurrent refresh of the same
	// session answers 409 and leaves the cookie alone, everything else answers as
	// a stale cookie always has — 401, cookie cleared.
	//
	// Before this check the loser of a race with a sign-out rotated the revoked
	// session anyway and went on to mint a 15-minute access token the sign-out
	// had been meant to deny (authRequired trusts the JWT alone), and the loser of
	// two refreshes on one cookie still succeeded with a second, different cookie.
	//
	// An error that is not "lost the race" is a failure, not a refusal: it leaves
	// the cookie alone and stays a 500 — or a 503 when the bound ran out.
	newHash := auth.HashToken(newRefreshToken)
	if err := auth.RotateRefreshToken(ctx, qx, session, newHash, user.Role); err != nil {
		if errors.Is(err, auth.ErrInvalidToken) {
			// Info, with the session id and nothing replayable: a lost rotation is
			// rare, and this line is what tells an operator one from a revoke — the
			// refresh was refused either way, but only here did it get as far as
			// the rotation. If it lost to a concurrent refresh, the 409 is logged
			// as such by RefusalSparesCookie.
			slog.Info("refresh: lost the rotation; the session changed after the refresh validated it",
				"session_id", session.ID)
			// The refusal asks the pool whether this was a race; the transaction's
			// connection has to be back in it first (see release).
			release()
			return h.refuseStaleRefresh(ctx, c, refreshToken)
		}
		return refreshDBFailed("rotate refresh token", "Failed to rotate refresh token", session.ID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		// A COMMIT that returns an error may still have landed, and then the session
		// has been rotated and this client holds the old cookie. The answer is the
		// same as for any other failed step — nothing more can be done from here —
		// but it is logged, and documented, as unconfirmed rather than as "changes
		// nothing": see refreshCommitFailed.
		return refreshCommitFailed(session.ID, err)
	}

	// The Redis row is written from a goroutine of its own, not here: nothing reads
	// it, and the new cookie must not wait on it. With Redis slow or unreachable
	// this used to hold the winner's response for the write's whole timeout, and a
	// loser whose short retry then went out before that cookie arrived carried the
	// OLD one into a second 409.
	go h.mirrorSessionToRedis(session.ID, user.ID, newHash, user.Role) //nolint:gosec // G118: intentionally detached — the cache write must outlive the request (Fiber recycles the request context)

	setRefreshCookie(c, newRefreshToken, h.jwtService.RefreshTokenTTL())

	return c.JSON(authResponse{
		User: authUserResponse{
			ID:          user.ID,
			Email:       user.Email,
			DisplayName: user.DisplayName,
			Role:        user.Role,
		},
		AccessToken:  accessToken,
		RefreshToken: "",
		ExpiresAt:    expiresAt.Unix(),
		Permissions:  perms,
	})
}

// refuseStaleRefresh answers a refresh whose token turned out not to be the
// session's live one, either at validation or when the rotation changed no row.
//
// A token that another refresh of the same live session replaced within
// auth.ConcurrentRefreshTolerance is the loser of a race, and the answer is 409
// refresh_superseded with the cookie LEFT ALONE: the loser's own Set-Cookie
// deletion would race the winner's new cookie in the browser's shared jar and
// could erase it, and a deletion cannot be made conditional on what the jar
// holds. The caller retries once with the newest cookie. Anything else — an
// unknown token, one older than a rotation, a revoked or expired session — is
// 401 with the cookie cleared, as a stale cookie always has been.
//
// Whether to spare the cookie is the only thing RefusalSparesCookie decides, and
// nothing here issues anything either way.
//
// It asks the POOL, so a caller that holds a transaction must have released it
// first (see Refresh's release): this needs a connection of its own.
func (h *AuthHandler) refuseStaleRefresh(ctx context.Context, c fiber.Ctx, refreshToken string) error {
	// The question is a follow-up of the decision that the token is stale, and its
	// answer shapes the refusal: a loser to a concurrent refresh is owed the 409
	// that leaves its cookie alone, and a question asked of an exhausted budget
	// fails and reads as "not a race", which answers 401 and clears the cookie the
	// winner may already have replaced. So it gets a deadline of its own.
	fctx, fcancel := h.followUp(ctx)
	defer fcancel()
	if h.sessionManager.RefusalSparesCookie(fctx, refreshToken) {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			// The code is the contract: the SPA retries on exactly this status and
			// this code. It is written here, not derived from the status by
			// errorHandler (which would say "conflict"); see RefreshSupersededCode.
			"error":   RefreshSupersededCode,
			"message": "This session was refreshed by another request a moment ago; retry the refresh once with the newest refresh cookie.",
		})
	}
	clearRefreshCookie(c)
	return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired refresh token")
}

// dbContext is the bounded context a handler runs its database work in.
//
// It is also installed as the request's own context (c.SetContext), and that is
// not decoration: the helpers every handler shares — AuditLogAs with the event it
// publishes — read c.Context() themselves, and in Fiber that is
// context.Background() unless a handler has set one. A stall in either would
// otherwise be unbounded: a sign-out that was confirmed would wait on its audit
// row for as long as the database took, and the response of a refresh that had
// already rotated its session would wait with it, the new cookie with the
// response. So everything the handler does after this call is bounded, whether it
// takes ctx explicitly or reads c.Context().
//
// This is the bound of the work that DECIDES. What enforces or records a decision
// already made — the audit row, the revoke of a refused account's session, the
// race check behind a refusal, the Redis cleanup — runs under a deadline of its
// own instead (followUp, withFollowUp), because on this one it would get whatever
// the deciding work left, which after a slow decision may be nothing.
//
// The returned cancel puts back the context that was there before, so that
// nothing running after the handler returns sees a cancelled one.
func (h *AuthHandler) dbContext(c fiber.Ctx) (context.Context, context.CancelFunc) {
	d := h.dbTimeout
	if d <= 0 {
		d = authDBTimeout
	}
	parent := c.Context()
	ctx, cancel := context.WithTimeout(parent, d)
	c.SetContext(ctx)
	return ctx, func() {
		cancel()
		c.SetContext(parent)
	}
}

// followUp returns the context for one follow-up of a decision already made: a
// deadline of its own, from a fresh start, on a context detached from the deciding
// work's — which may have spent every second of its budget, and must not take the
// enforcement or the record down with it. The returned cancel must be called.
func (h *AuthHandler) followUp(parent context.Context) (context.Context, context.CancelFunc) {
	d := h.followUpTimeout
	if d <= 0 {
		d = authFollowUpTimeout
	}
	return context.WithTimeout(context.WithoutCancel(parent), d)
}

// withFollowUp runs fn — a call that reads c.Context(), such as AuditLogAs with
// the event it publishes — under a follow-up deadline, and puts the request's
// context back afterwards.
func (h *AuthHandler) withFollowUp(c fiber.Ctx, fn func()) {
	prev := c.Context()
	ctx, cancel := h.followUp(prev)
	c.SetContext(ctx)
	defer func() {
		cancel()
		c.SetContext(prev)
	}()
	fn()
}

// forgetSessions deletes the Redis rows of sessions a handler has just ended, as a
// follow-up of the revoke that ended them. The revoke is in PostgreSQL and is what
// decides; the rows are a cache nothing reads (see SessionManager.WriteSessionRedis),
// and deleting them runs under a deadline of its own so that a Redis that does not
// answer cannot hold the response of a sign-out the database has already confirmed,
// and a request that spent its budget on the revoke still cleans up after it. It is
// best effort and says nothing, and it is the LAST thing a handler does: the audit
// row is what must not be lost, and this is what may take its whole deadline.
func (h *AuthHandler) forgetSessions(ctx context.Context, ids ...uuid.UUID) {
	fctx, cancel := h.followUp(ctx)
	defer cancel()
	h.sessionManager.ForgetSessions(fctx, ids)
}

// releaseTx returns a function that gives a transaction's connection back to the
// pool: it rolls the transaction back on a context of its own — fresh,
// releaseTxTimeout, and not the request's, which has usually just run out — and treats two
// outcomes as the successes they are. A transaction that is already closed
// (committed, or released by an earlier call) has nothing to roll back; and a
// connection pgx has already closed has none either: when a statement's context
// ends mid-flight pgx closes the connection itself, so the rollback that follows
// every bounded stall finds it closed (pgconn.ErrConnClosed), the pool discards
// it, and nothing is left to do — a warning for it on each stall would bury the
// rollbacks that really fail.
//
// It is idempotent, so a handler calls it explicitly before any query that needs a
// connection of its own and defers it as the backstop. A rollback that really
// fails is logged under who's name.
func releaseTx(tx pgx.Tx, who string) func() {
	return func() {
		rbCtx, rbCancel := context.WithTimeout(context.Background(), releaseTxTimeout)
		defer rbCancel()
		rbErr := tx.Rollback(rbCtx)
		if rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) && !errors.Is(rbErr, pgconn.ErrConnClosed) {
			slog.Warn(who+": transaction rollback failed", "error", rbErr)
		}
	}
}

// refreshDBFailed answers a database call of a refresh that did not complete —
// a lookup, the start of the transaction, or the rotation (the commit has
// refreshCommitFailed, which differs in what it says it knows). The same
// rule decides all of them (see isTransientDBError): the database being
// unreachable, too busy or out of its bound is a 503, "nothing was decided,
// retry"; anything else is a defect and a 500, which says so and is not worth
// retrying. Neither touches the cookie or the session, and neither reports
// success; both log, with the session id where it is known — it is what lets an
// operator find the session that was being refreshed — and never a token or a
// hash.
//
// what names the step in the log, message is the 500's text.
func refreshDBFailed(what, message string, sessionID uuid.UUID, err error) error {
	if isTransientDBError(err) {
		return refreshLookupFailed(what, sessionID, err)
	}
	if sessionID == uuid.Nil {
		slog.Error("refresh: "+what+" failed; answering 500 and leaving the cookie alone", "error", err)
	} else {
		slog.Error("refresh: "+what+" failed; answering 500 and leaving the cookie alone", "session_id", sessionID, "error", err)
	}
	return fiber.NewError(fiber.StatusInternalServerError, message)
}

// refreshCommitFailed answers a COMMIT of the refresh that returned an error. The
// status follows the same rule as every other step (see refreshDBFailed), and the
// cookie is left alone, but the log does NOT say that nothing changed, because a
// commit can land and still return a transient error — a reset after the server
// committed, a deadline that ran out while its reply was awaited. Then the session
// has been rotated and the client keeps the old cookie: a 409 for a few seconds
// (the refresh's own race window), a 401 after, and a sign-in.
//
// No behaviour differs from refreshDBFailed; what is pinned is that the line an
// operator reads — and the API reference — call the outcome unconfirmed. Finding
// out would take a second read of the session after the failure, which is not done.
func refreshCommitFailed(sessionID uuid.UUID, err error) error {
	if isTransientDBError(err) {
		slog.Warn("refresh: commit did not complete; its outcome is UNCONFIRMED — the session may have been rotated although this client will not receive the new cookie; answering 503 and leaving the cookie alone",
			"session_id", sessionID, "error", err)
		return fiber.NewError(fiber.StatusServiceUnavailable, "Could not verify the refresh token right now; please retry shortly")
	}
	slog.Error("refresh: commit failed; its outcome is UNCONFIRMED — the session may have been rotated although this client will not receive the new cookie; answering 500 and leaving the cookie alone",
		"session_id", sessionID, "error", err)
	return fiber.NewError(fiber.StatusInternalServerError, "Failed to commit refresh")
}

// revokeRefusedSession ends the session of a refresh that was refused because of
// its account — the user is gone, disabled, or has a different role than the
// session was issued under. The refresh is answered 401 whatever happens here;
// what this decides is whether the session also stops being live.
//
// A revoke that fails leaves it live — and the bound makes failure possible, a
// stalled database now being a timeout rather than a wait. The next refresh on
// that session meets the same refusal and tries again, which is the only thing
// that ends such a session before it expires, so the failure is logged: with the
// session id and why it was being revoked, never a token or a hash.
func (h *AuthHandler) revokeRefusedSession(ctx context.Context, sessionID uuid.UUID, reason string) {
	// The revoke enforces a refusal already decided, so it runs under a deadline of
	// its own and not on whatever the deciding work left of the request's budget.
	fctx, fcancel := h.followUp(ctx)
	defer fcancel()
	if err := h.sessionManager.RevokeSession(fctx, sessionID); err != nil {
		slog.Warn("refresh: could not revoke the session of a refused refresh; it stays live until a later refresh is refused again or it expires",
			"session_id", sessionID, "reason", reason, "error", err)
	}
}

// mirrorSessionToRedis writes the session's Redis row. It runs in its own
// goroutine with its own deadline and no part of the request: the client
// disconnecting must not stop it, nothing waits on it, and a failure is a
// warning and nothing else (see WriteSessionRedis). It recovers, because a panic
// in a goroutine nothing is waiting on would take the whole process down for the
// sake of a cache row.
func (h *AuthHandler) mirrorSessionToRedis(sessionID, userID uuid.UUID, tokenHash, role string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("refresh: redis cache update panicked", "session_id", sessionID, "panic", r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.sessionManager.WriteSessionRedis(ctx, sessionID, userID, tokenHash, role, h.jwtService.RefreshTokenTTL()); err != nil {
		slog.Warn("refresh: redis cache update failed", "session_id", sessionID, "error", err)
	}
}

// refreshLookupFailed answers a refresh whose session or user lookup could not be
// made: 503, with the cookie left alone and nothing revoked.
//
// That is a different thing from "no such session", and answering it as one is
// how a database blip signs a user out for good — the 401 clears the cookie, and
// the SPA ends the session on a 401. A 503 is the one answer that says "nothing
// was decided, try again", and the failure is logged because it is otherwise
// invisible.
//
// sessionID is the session being refreshed, when it is known — it is what an
// operator needs to find the session — and uuid.Nil when the failure is the very
// lookup that would have found it. It is never a token or a hash.
func refreshLookupFailed(what string, sessionID uuid.UUID, err error) error {
	if sessionID == uuid.Nil {
		slog.Warn("refresh: "+what+" failed; answering 503 and leaving the cookie and the session alone", "error", err)
	} else {
		slog.Warn("refresh: "+what+" failed; answering 503 and leaving the cookie and the session alone",
			"session_id", sessionID, "error", err)
	}
	return fiber.NewError(fiber.StatusServiceUnavailable, "Could not verify the refresh token right now; please retry shortly")
}

// Logout revokes the session identified by the refresh token (cookie for
// browser clients, body for mobile clients) and clears the browser cookie.
// The cookie is cleared unconditionally so a stale cookie does not linger
// after logout even if the token is already invalid.
//
// The token may also be the one the session had before its last rotation, for
// auth.PreviousTokenRevocationWindow after that rotation: a sign-out sent while a
// refresh was in flight carries the cookie the refresh was about to replace, and
// without this it would find no session, revoke nothing and still answer
// success, leaving the refresh's new cookie live. The ownership check below
// applies to that match exactly as to a current-token match, and the audit row
// and the log say when it was the previous token that matched.
//
// Three answers, and the third is not the first: 200 when the session was ended,
// or when there was nothing to end (no token, an absurdly long one, one no live
// session holds); 403 for a session that is not the caller's; 503 when a lookup
// could not be made, in which case nothing was revoked and the caller is told so
// rather than shown a success that never happened. The cookie is cleared in every
// case — which is why the 503 does not say "try again": a browser's second attempt
// would carry no token. Its database work is bounded by authDBTimeout; the audit
// row and the Redis cleanup that follow run under deadlines of their own.
func (h *AuthHandler) Logout(c fiber.Ctx) error {
	var req logoutRequest
	// Body is optional for browser clients (cookie carries the token).
	_ = c.Bind().Body(&req)

	if req.RefreshToken == "" {
		req.RefreshToken = readRefreshTokenFromCookie(c)
	}

	// Always clear the cookie regardless of whether we found a token.
	clearRefreshCookie(c)

	// No token, or one longer than any refresh token can be (and than /auth/refresh
	// accepts): there is nothing of ours to revoke, so treat it as the idempotent
	// success it is — before anything is hashed, looked up or logged. The bound
	// counts characters, as the schema's does.
	if req.RefreshToken == "" || refreshTokenTooLong(req.RefreshToken) {
		return c.JSON(fiber.Map{"message": "Logged out successfully"})
	}

	ctx, cancel := h.dbContext(c)
	defer cancel()

	session, viaPrevious, err := h.sessionManager.FindSessionForLogout(ctx, req.RefreshToken)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidToken) {
			// No live session holds this token — already revoked, expired or
			// never ours. Idempotent success.
			return c.JSON(fiber.Map{"message": "Logged out successfully"})
		}
		// A lookup could not be made, so nothing is known about the session — and
		// nothing was revoked. That is not "already logged out", and answering 200
		// would tell a signed-in user otherwise. The cookie is already cleared, so
		// the message must not tell a browser to simply try again. The database
		// being unavailable is a 503; a lookup that failed for any other reason is
		// a defect and a 500, with the same message, because what the caller needs
		// to know is the same: the session may still be active.
		if isTransientDBError(err) {
			slog.Warn("logout: session lookup failed, nothing revoked", "error", err)
			return fiber.NewError(fiber.StatusServiceUnavailable, logoutUnconfirmedMessage)
		}
		slog.Error("logout: session lookup failed, nothing revoked", "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, logoutUnconfirmedMessage)
	}

	// Defence-in-depth: when an access token IS present, verify the session
	// owner matches. With authOptional Logout supports an expired access
	// token + valid cookie; we only enforce the cross-check when a caller
	// happens to also send an Authorization header.
	if userID, ok := c.Locals("user_id").(uuid.UUID); ok {
		if session.UserID != userID {
			return fiber.NewError(fiber.StatusForbidden, "Session does not belong to authenticated user")
		}
	}

	// The session row is what ends the session, and it is revoked here, on the
	// deciding bound. Its Redis row is deleted at the end, as a follow-up of its own
	// (SessionManager.RevokeSession would do both on this one).
	if err := h.queries.RevokeSession(ctx, session.ID); err != nil {
		if isTransientDBError(err) {
			slog.Warn("logout: revoking the session did not complete", "session_id", session.ID, "error", err)
			return fiber.NewError(fiber.StatusServiceUnavailable, logoutUnconfirmedMessage)
		}
		slog.Error("logout: revoking the session failed", "session_id", session.ID, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke session")
	}

	var details json.RawMessage
	if viaPrevious {
		details = json.RawMessage(logoutMatchedPreviousToken)
		slog.Info("logout: revoked a session by the token it had before its last rotation",
			"session_id", session.ID, "user_id", session.UserID)
	}
	h.withFollowUp(c, func() {
		AuditLogAs(c, h.queries, h.eventPub, session.UserID, pgtype.UUID{}, "auth", session.UserID.String(), "logout", details)
	})
	h.forgetSessions(ctx, session.ID)

	return c.JSON(fiber.Map{"message": "Logged out successfully"})
}

// LogoutAll revokes all sessions for the current user and clears the browser
// refresh cookie.
//
// Its database work is bounded by authDBTimeout, like Logout's — it is the remedy
// Logout's own 503 points a user to, so it must not be the one that hangs. A
// revoke the database could not complete (see isTransientDBError) is a 503 saying
// the sessions may still be active, with the cookie left alone (the caller is
// still signed in and can try again); any other failure is the 500 it always was.
// The audit row and the Redis cleanup that follow run under deadlines of their own.
func (h *AuthHandler) LogoutAll(c fiber.Ctx, _ *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	ctx, cancel := h.dbContext(c)
	defer cancel()

	revoked, err := auth.RevokeAllUserSessionsIn(ctx, h.queries, userID)
	if err != nil {
		if isTransientDBError(err) {
			slog.Warn("logout-all: revoking the sessions did not complete", "user_id", userID, "error", err)
			return fiber.NewError(fiber.StatusServiceUnavailable, logoutAllUnconfirmedMessage)
		}
		slog.Error("logout-all: revoking the sessions failed", "user_id", userID, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke sessions")
	}

	clearRefreshCookie(c)

	h.withFollowUp(c, func() {
		AuditLogAs(c, h.queries, h.eventPub, userID, pgtype.UUID{}, "auth", userID.String(), "logout_all", nil)
	})
	h.forgetSessions(ctx, revoked...)

	return c.JSON(fiber.Map{"message": "All sessions revoked"})
}

// SetupStatus returns whether initial admin setup has been completed.
func (h *AuthHandler) SetupStatus(c fiber.Ctx, _ *apischema.Params) error {
	count, err := h.queries.CountUsers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to check user count")
	}
	return c.JSON(fiber.Map{
		"needs_setup": count == 0,
	})
}

// loadPermsStrict loads the flat permissions list for the given user via the RBAC
// engine, and says so when it cannot. The list is never nil on success — the
// response field is always a JSON array — and an engine that is not wired (the
// no-database unit tests) is a user with none.
func (h *AuthHandler) loadPermsStrict(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if h.rbac == nil {
		return []string{}, nil
	}
	perms, err := h.rbac.GetFlatPermissions(ctx, userID)
	if err != nil {
		return nil, err
	}
	if perms == nil {
		return []string{}, nil
	}
	return perms, nil
}

// loadPerms is loadPermsStrict for a response that can do without: a failed read
// is an empty list. Login and the SSO exchange use it. Refresh does not — it
// answers 503 rather than sign a user in to a page that shows them nothing.
func (h *AuthHandler) loadPerms(c fiber.Ctx, userID uuid.UUID) []string {
	perms, err := h.loadPermsStrict(c.Context(), userID)
	if err != nil {
		return []string{}
	}
	return perms
}

// SSOStatus returns whether OIDC/SSO is enabled and the provider name.
func (h *AuthHandler) SSOStatus(c fiber.Ctx, _ *apischema.Params) error {
	resp := fiber.Map{
		"oidc_enabled":       false,
		"oidc_provider_name": "",
	}

	if h.oidcHandler != nil {
		cfg, err := h.queries.GetEnabledOIDCConfig(c.Context())
		if err == nil {
			resp["oidc_enabled"] = true
			resp["oidc_provider_name"] = cfg.Name
		}
	}

	return c.JSON(resp)
}

// OIDCTokenExchange consumes the short-lived exchange code and issues standard JWT tokens.
func (h *AuthHandler) OIDCTokenExchange(c fiber.Ctx, p *apischema.Params) error {
	// The route is declared on AuthHandler alone, so a Server wired with an
	// auth handler but no OIDC one reaches here rather than 404ing at the
	// router. This is the same answer either way.
	if h.oidcHandler == nil {
		return fiber.NewError(fiber.StatusNotFound, "OIDC not configured")
	}

	// Atomic GetDel — single-use exchange code
	data, err := h.oidcHandler.rdb.GetDel(c.Context(), "oidc:exchange:"+p.String("code")).Result()
	if err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired exchange code")
	}

	var exchangeData map[string]string
	if err := json.Unmarshal([]byte(data), &exchangeData); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Invalid exchange data")
	}

	userID, err := uuid.Parse(exchangeData["user_id"])
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Invalid user in exchange data")
	}

	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "User not found")
	}

	if !user.IsActive {
		return fiber.NewError(fiber.StatusForbidden, "Account is disabled")
	}

	return h.issueOrTOTP(c, user, "oidc_login")
}

// issueOrTOTP checks if the user has TOTP enabled. If so, returns a pending token
// instead of issuing JWT tokens directly. Otherwise, issues tokens normally.
func (h *AuthHandler) issueOrTOTP(c fiber.Ctx, user db.User, auditAction string) error {
	if user.TotpSecret.Valid && h.totpHandler != nil {
		token, err := h.totpHandler.CreateTOTPPendingToken(c.Context(), user.ID, auditAction)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to create TOTP challenge")
		}
		return c.JSON(totpRequiredResponse{
			TOTPRequired:     true,
			TOTPPendingToken: token,
		})
	}
	return h.issueTokens(c, user, auditAction)
}

// profileResponse is the response for GET /api/v1/auth/me.
type profileResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	AuthSource  string    `json:"auth_source"`
	TOTPEnabled bool      `json:"totp_enabled"`
	CreatedAt   string    `json:"created_at"`
}

// GetMe returns the current user's profile.
func (h *AuthHandler) GetMe(c fiber.Ctx, _ *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to fetch user")
	}

	return c.JSON(profileResponse{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		AuthSource:  user.AuthSource,
		TOTPEnabled: user.TotpSecret.Valid,
		CreatedAt:   user.CreatedAt.Format(time.RFC3339Nano),
	})
}

// UpdateProfile allows the current user to update their own display name.
// Only local users can edit their profile — LDAP/OIDC profiles are managed externally.
func (h *AuthHandler) UpdateProfile(c fiber.Ctx, p *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to fetch user")
	}

	if user.AuthSource != "local" {
		return fiber.NewError(fiber.StatusForbidden, "Profile is managed by your identity provider")
	}

	// The declaration bounds the length; the TRIM and the "only whitespace"
	// refusal stay here, because the schema's MinLength counts characters and
	// cannot see that all of them are spaces.
	displayName := strings.TrimSpace(p.String("display_name"))
	if displayName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Display name is required")
	}

	updated, err := h.queries.UpdateUserDisplayName(c.Context(), db.UpdateUserDisplayNameParams{
		ID:          userID,
		DisplayName: displayName,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update profile")
	}

	details, _ := json.Marshal(map[string]string{"display_name": displayName})
	AuditLogAs(c, h.queries, h.eventPub, userID, pgtype.UUID{}, "auth", userID.String(), "profile_updated", details)

	return c.JSON(profileResponse{
		ID:          updated.ID,
		Email:       updated.Email,
		DisplayName: updated.DisplayName,
		Role:        updated.Role,
		AuthSource:  updated.AuthSource,
		TOTPEnabled: updated.TotpSecret.Valid,
		CreatedAt:   updated.CreatedAt.Format(time.RFC3339Nano),
	})
}

// ChangePassword allows the current user to change their own password.
// Only available for local auth users.
//
// The new password and the end of every session of the user are ONE transaction,
// run through the transaction's own queries and nothing else — a request that
// holds a connection must not ask the pool for a second one. So either both
// happen or neither does. They used to be two statements on the pool: a change
// that landed while its revoke failed answered 200 "Password changed
// successfully" with every other session — possibly an attacker's, the reason the
// password was being changed — still live on the old credentials, and a change
// whose answer was lost skipped the revoke altogether and left the user's retry
// refused for using the old password.
//
// Any failure before the COMMIT, any the server itself answered, and a COMMIT that
// never went out because its context had already ended rolled the transaction back,
// and the answer says the password was NOT changed: retrying is safe. The update is conditional on the password that
// was verified (UpdatePassword is a compare-and-swap), so a request that raced
// another change of the same account, or whose account was removed meanwhile,
// changes nothing and is answered 409.
//
// A COMMIT that went out and returns without the server's answer — a reset, an
// EOF, the bound running out — may have landed, and the handler settles it rather
// than guess: it reads the stored hash back once — a locking read, which waits for
// a transaction the server is still finishing — on a follow-up deadline, and
// compares it with the one this request generated. bcrypt salts every hash, so
// equal can only mean that this very transaction committed — the password and the
// end of every session together — and the request goes on as a success: the
// record, the cleanup, the 200. Different means it did not, and the answer says
// NOT changed. An account that is gone is answered 409 and says so. Only when the
// read fails too is the outcome left unconfirmed, and the answer says exactly that
// and how to find out. The Redis rows of the revoked sessions are deleted after
// the commit and never decide anything.
//
// Its database work is bounded by authDBTimeout in two phases, the read of the
// user and then the transaction, because the bcrypt work between them is CPU time,
// not database time: a slow hash must not be charged to the database's bound and
// turn into a deadline on a write that the database would have answered. The
// Redis cleanup and the audit row run under follow-up deadlines of their own.
func (h *AuthHandler) ChangePassword(c fiber.Ctx, p *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	readCtx, readCancel := h.dbContext(c)
	user, err := h.queries.GetUserByID(readCtx, userID)
	readCancel()
	if err != nil {
		return passwordNotChanged("read the user", userID, err)
	}

	if user.AuthSource != "local" {
		return fiber.NewError(fiber.StatusForbidden, "Password is managed by your identity provider")
	}

	if err := auth.CheckPassword(user.PasswordHash, p.String("old_password")); err != nil {
		return fiber.NewError(fiber.StatusUnauthorized, "Current password is incorrect")
	}

	hashedPassword, err := auth.HashPassword(p.String("new_password"))
	if err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) || errors.Is(err, auth.ErrPasswordTooLong) || errors.Is(err, auth.ErrPasswordWeak) {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to process password")
	}

	ctx, cancel := h.dbContext(c)
	defer cancel()

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return passwordNotChanged("start the transaction", userID, err)
	}
	release := releaseTx(tx, "change password")
	defer release()
	qx := h.queries.WithTx(tx)

	rows, err := qx.UpdatePassword(ctx, db.UpdatePasswordParams{
		ID:           userID,
		PasswordHash: hashedPassword,
		// The hash the old password was just checked against. The change is made to
		// that password only: without the condition a request that verified the same
		// old password as another would overwrite that request's change — and revoke
		// the sessions it had just created — and an account deleted since the check
		// would be answered "changed" for a change that went nowhere.
		ExpectedHash: user.PasswordHash,
	})
	if err != nil {
		return passwordNotChanged("update the password", userID, err)
	}
	if rows == 0 {
		return passwordChangedMeanwhile(userID)
	}

	// Revoke all sessions to force re-authentication on all devices, in the same
	// transaction: the change is not made unless this is.
	revoked, err := auth.RevokeAllUserSessionsIn(ctx, qx, userID)
	if err != nil {
		return passwordNotChanged("revoke the user's sessions", userID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		if !commitOutcomeUnknown(err) {
			// The server answered the COMMIT, or it never went out: the transaction
			// rolled back (see commitOutcomeUnknown).
			return passwordNotChanged("commit the transaction", userID, err)
		}
		switch h.settleCommit(ctx, userID, hashedPassword) {
		case changeLanded:
			slog.Warn("change password: the commit did not complete, but reading the user back shows the new password in place: the change was made",
				"user_id", userID, "error", err)
		case changeNotLanded:
			return passwordNotChanged("commit the transaction (reading the user back found the old password in place)", userID, err)
		case changeAccountGone:
			return fiber.NewError(fiber.StatusConflict, passwordAccountRemovedMessage)
		default:
			return passwordCommitUnconfirmed(userID, err)
		}
	}

	// The change is made. What is left runs under deadlines of its own, so that a
	// request which spent its budget getting here still records it and cleans up.
	// The record comes first: it is what must not be lost, and the cleanup is a cache
	// that nothing reads and may take its whole deadline. A failure of either is not
	// the user's to hear about.
	h.withFollowUp(c, func() {
		AuditLogAs(c, h.queries, h.eventPub, userID, pgtype.UUID{}, "auth", userID.String(), "password_changed", nil)
	})
	h.forgetSessions(ctx, revoked...)

	return c.JSON(fiber.Map{"message": "Password changed successfully"})
}

// passwordNotChanged answers a failure of ChangePassword before anything was
// committed — the read of the user, or a step of the transaction up to and
// including a COMMIT the server refused: whatever was begun rolls back, nothing
// changed, and the answer says so. The database being away is a 503 and anything
// else a 500 (see isTransientDBError); both are logged, with the step and the user
// id and never a password or a hash.
func passwordNotChanged(step string, userID uuid.UUID, err error) error {
	if isTransientDBError(err) {
		slog.Warn("change password: could not "+step+"; the password was not changed",
			"user_id", userID, "error", err)
		return fiber.NewError(fiber.StatusServiceUnavailable, passwordNotChangedUnavailableMessage)
	}
	slog.Error("change password: failed to "+step+"; the password was not changed",
		"user_id", userID, "error", err)
	return fiber.NewError(fiber.StatusInternalServerError, passwordNotChangedMessage)
}

// passwordChangedMeanwhile answers an update that matched no row: the password the
// request had verified is no longer the one in effect, or the account is gone. The
// transaction rolls back, nothing was changed — and no session was revoked — so
// the answer is a 409 that says so, and is not a 200 for a change that went
// nowhere or an overwrite of someone else's.
func passwordChangedMeanwhile(userID uuid.UUID) error {
	slog.Warn("change password: the password changed, or the account was removed, between the check of the old password and the update; nothing was changed",
		"user_id", userID)
	return fiber.NewError(fiber.StatusConflict, passwordChangedMeanwhileMessage)
}

// changeOutcome is what settleCommit found: the answers a question that can fail
// has — yes, no, and could not look — and the one the account itself can give,
// that it is no longer there.
type changeOutcome int

const (
	changeUnknown     changeOutcome = iota // the read failed: nothing is known
	changeLanded                           // the stored hash is the one this request generated
	changeNotLanded                        // the stored hash is something else
	changeAccountGone                      // there is no such account any more
)

// settleCommit finds out, after a COMMIT whose outcome is unknown, whether it
// landed: it reads the stored hash back, on a follow-up deadline of its own (the
// request's bound has usually just run out), and compares it with the one this
// request generated. bcrypt salts every hash, so the two are equal only if this
// request's transaction committed, and since the password and the end of every
// session are that one transaction, the sessions are revoked too.
//
// The read is a LOCKING read (GetPasswordHashForSettle, FOR SHARE), and that is
// what makes a hash that is not the new one a proof that the transaction did not
// commit. The COMMIT was sent, and the server finishes a commit it has received
// even when its client has given up: until the commit record is flushed — and any
// wait for a synchronous replica is over — every other session's snapshot still
// sees the old row, so a plain read in that window finds the old hash and would
// call a change that is about to land one that did not. The transaction holds the
// row's lock until it ends, so the locking read waits for it, and under READ
// COMMITTED then returns the newest committed version: the new hash if it
// committed, the old one if it rolled back (the server aborts the transaction of a
// connection that went away before the COMMIT ran, once it notices the peer is
// gone). A transaction that stays open longer than the follow-up deadline is a read
// that fails, which is changeUnknown, and the honest answer. The case is a
// half-open connection whose COMMIT never reached the server: the server has no
// reason yet to think its client has gone, so the transaction stays open holding
// the row's lock, the locking read waits on it and gives up at the deadline, and
// the answer is "could not be confirmed" although nothing will land (a COMMIT the
// server did receive and is slow to finish ends the same way, and does land).
// Telling the two apart would take the server noticing that the connection is dead,
// which it does only when a keepalive probe or a write to the peer fails.
//
// The one exception left is another request, or an administrator, changing the
// password between this COMMIT and this read: the account then holds that later
// password, which reads as "not changed", and holds it either way.
//
// The connection of the transaction is already back in the pool — Commit returns
// it whether it succeeded or not — so asking the pool for this read is not a
// second connection held at once. A read that fails leaves the question open
// (changeUnknown), and is logged; one that finds no account says so
// (changeAccountGone).
func (h *AuthHandler) settleCommit(ctx context.Context, userID uuid.UUID, newHash string) changeOutcome {
	fctx, cancel := h.followUp(ctx)
	defer cancel()
	stored, err := h.queries.GetPasswordHashForSettle(fctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("change password: the account was removed before a commit whose outcome is unknown could be settled",
				"user_id", userID)
			return changeAccountGone
		}
		slog.Warn("change password: could not read the user back to settle a commit whose outcome is unknown",
			"user_id", userID, "error", err)
		return changeUnknown
	}
	if stored == newHash {
		return changeLanded
	}
	return changeNotLanded
}

// passwordCommitUnconfirmed answers a COMMIT that went out, whose answer was lost,
// and whose outcome the read-back could not settle either: the change may have
// landed, the status follows the usual rule, and the message says the change is
// unconfirmed and how to find out.
func passwordCommitUnconfirmed(userID uuid.UUID, err error) error {
	if isTransientDBError(err) {
		slog.Warn("change password: the commit did not complete; its outcome is UNCONFIRMED — the password and the end of the user's sessions may both have taken effect",
			"user_id", userID, "error", err)
		return fiber.NewError(fiber.StatusServiceUnavailable, passwordChangeUnconfirmedMessage)
	}
	slog.Error("change password: the commit failed; its outcome is UNCONFIRMED — the password and the end of the user's sessions may both have taken effect",
		"user_id", userID, "error", err)
	return fiber.NewError(fiber.StatusInternalServerError, passwordChangeUnconfirmedMessage)
}

// isDuplicateKeyError checks if a pgx error is a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
