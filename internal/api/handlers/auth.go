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

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

// AuthHandler handles authentication endpoints.
type AuthHandler struct {
	pool           *pgxpool.Pool
	queries        *db.Queries
	jwtService     *auth.JWTService
	sessionManager *auth.SessionManager
	rbac           *auth.RBACEngine
	eventPub       *events.Publisher
	ldapHandler    *LDAPHandler
	oidcHandler    *OIDCHandler
	totpHandler    *TOTPHandler
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
	return &AuthHandler{
		pool:           pool,
		queries:        queries,
		jwtService:     jwtSvc,
		sessionManager: sessMgr,
		rbac:           rbac,
		eventPub:       eventPub,
	}
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
	// gating on view:node handed read-only accounts a root shell on the
	// hypervisor. console_token_authz_test.go pins this invariant.
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

	session, err := h.sessionManager.ValidateRefreshToken(c.Context(), refreshToken)
	if err != nil {
		// Stale cookie → clear it so the browser stops sending it.
		clearRefreshCookie(c)
		return fiber.NewError(fiber.StatusUnauthorized, "Invalid or expired refresh token")
	}

	// User load, role-rotation guard, permission load, and refresh-token
	// rotation share one transaction so an admin demoting the user mid-
	// refresh cannot race past the role check (Finding A11). Redis cache
	// update happens after commit; ValidateRefreshToken hits Postgres so a
	// stale Redis row is non-load-bearing.
	tx, err := h.pool.Begin(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to start transaction")
	}
	defer func() {
		rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer rbCancel()
		if rbErr := tx.Rollback(rbCtx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.Warn("refresh: transaction rollback failed", "error", rbErr)
		}
	}()
	qx := h.queries.WithTx(tx)

	user, err := qx.GetUserByID(c.Context(), session.UserID)
	if err != nil {
		_ = h.sessionManager.RevokeSession(c.Context(), session.ID)
		clearRefreshCookie(c)
		return fiber.NewError(fiber.StatusUnauthorized, "User not found")
	}

	if !user.IsActive {
		_ = h.sessionManager.RevokeSession(c.Context(), session.ID)
		clearRefreshCookie(c)
		return fiber.NewError(fiber.StatusUnauthorized, "Account is disabled")
	}

	// Role rotation guard: if the legacy users.role at session creation is
	// non-empty and has since changed, force re-login. Empty user_role
	// means a pre-migration session — accept it once so the upgrade path
	// does not log out every existing user; it gets populated below.
	if session.UserRole != "" && session.UserRole != user.Role {
		_ = h.sessionManager.RevokeSession(c.Context(), session.ID)
		clearRefreshCookie(c)
		details, _ := json.Marshal(map[string]string{
			"session_role": session.UserRole,
			"current_role": user.Role,
		})
		AuditLogAs(c, h.queries, h.eventPub, user.ID, pgtype.UUID{}, "auth", user.ID.String(), "refresh_denied_role_changed", details)
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

	newHash := auth.HashToken(newRefreshToken)
	if err := qx.UpdateSessionTokenHash(c.Context(), db.UpdateSessionTokenHashParams{
		ID:        session.ID,
		TokenHash: newHash,
		UserRole:  user.Role,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to rotate refresh token")
	}

	if err := tx.Commit(c.Context()); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to commit refresh")
	}

	// Detached context: post-commit Redis cache write should converge even
	// if the HTTP client disconnects between commit and response. Redis is
	// a cache (ValidateRefreshToken reads Postgres), so a transient
	// failure here is non-fatal — log and continue.
	redisCtx, redisCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer redisCancel()
	if err := h.sessionManager.WriteSessionRedis(redisCtx, session.ID, user.ID, newHash, user.Role, h.jwtService.RefreshTokenTTL()); err != nil {
		slog.Warn("refresh: redis cache update failed", "session_id", session.ID, "error", err)
	}

	perms := h.loadPerms(c, user.ID)

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

// Logout revokes the session identified by the refresh token (cookie for
// browser clients, body for mobile clients) and clears the browser cookie.
// The cookie is cleared unconditionally so a stale cookie does not linger
// after logout even if the token is already invalid.
func (h *AuthHandler) Logout(c fiber.Ctx) error {
	var req logoutRequest
	// Body is optional for browser clients (cookie carries the token).
	_ = c.Bind().Body(&req)

	if req.RefreshToken == "" {
		req.RefreshToken = readRefreshTokenFromCookie(c)
	}

	// Always clear the cookie regardless of whether we found a token.
	clearRefreshCookie(c)

	if req.RefreshToken == "" {
		// Nothing to revoke — treat as idempotent success.
		return c.JSON(fiber.Map{"message": "Logged out successfully"})
	}

	session, err := h.sessionManager.ValidateRefreshToken(c.Context(), req.RefreshToken)
	if err != nil {
		// Token already invalid/revoked — treat as success
		return c.JSON(fiber.Map{"message": "Logged out successfully"})
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

	if err := h.sessionManager.RevokeSession(c.Context(), session.ID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke session")
	}

	AuditLogAs(c, h.queries, h.eventPub, session.UserID, pgtype.UUID{}, "auth", session.UserID.String(), "logout", nil)

	return c.JSON(fiber.Map{"message": "Logged out successfully"})
}

// LogoutAll revokes all sessions for the current user and clears the browser
// refresh cookie.
func (h *AuthHandler) LogoutAll(c fiber.Ctx, _ *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	if err := h.sessionManager.RevokeAllUserSessions(c.Context(), userID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to revoke sessions")
	}

	clearRefreshCookie(c)

	AuditLogAs(c, h.queries, h.eventPub, userID, pgtype.UUID{}, "auth", userID.String(), "logout_all", nil)

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

// loadPerms loads the flat permissions list for the given user via RBAC engine.
func (h *AuthHandler) loadPerms(c fiber.Ctx, userID uuid.UUID) []string {
	if h.rbac == nil {
		return []string{}
	}
	perms, err := h.rbac.GetFlatPermissions(c.Context(), userID)
	if err != nil || perms == nil {
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
func (h *AuthHandler) ChangePassword(c fiber.Ctx, p *apischema.Params) error {
	userID, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
	}

	user, err := h.queries.GetUserByID(c.Context(), userID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to fetch user")
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

	if err := h.queries.UpdatePassword(c.Context(), db.UpdatePasswordParams{
		ID:           userID,
		PasswordHash: hashedPassword,
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update password")
	}

	// Revoke all sessions to force re-authentication on all devices.
	if err := h.sessionManager.RevokeAllUserSessions(c.Context(), userID); err != nil {
		// Password already changed — log the session revocation failure but don't fail the request.
		slog.Warn("failed to revoke sessions after password change", "user_id", userID, "error", err)
	}

	AuditLogAs(c, h.queries, h.eventPub, userID, pgtype.UUID{}, "auth", userID.String(), "password_changed", nil)

	return c.JSON(fiber.Map{"message": "Password changed successfully"})
}

// isDuplicateKeyError checks if a pgx error is a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
