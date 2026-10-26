package handlers

import (
	"context"
	"log/slog"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// LocalsAuthMethod is the c.Locals key under which authentication records HOW the
// caller authenticated, and AuthMethodSession and AuthMethodAPIKey are the two
// values it records: authRequired writes the first for a request carrying a valid
// access token — an interactive session — and authenticateAPIKey writes the second
// for one carrying a valid API key (both in middleware.go). Nothing else records a
// method, so a request with no method is one nothing authenticated.
//
// They are one set of constants, used by the writers and by the reader
// (AuthenticatedWithSession), because they used to be two string literals in two
// packages — "auth_method" in middleware.go and in api_keys.go, "api_key" in both —
// and a rename of one of them would have left the other reading a key nobody set,
// every API key allowed through what the refusal was written to keep it out of,
// with every test green.
const (
	LocalsAuthMethod  = "auth_method"
	AuthMethodSession = "session"
	AuthMethodAPIKey  = "api_key"
)

// LocalsSessionID and LocalsAPIKeyID are the keys under which authentication
// records WHICH credential the caller used, as a uuid.UUID: the session its
// access token belongs to (authRequired, authOptional), or the API key
// (authenticateAPIKey). LiveCredential reads them, and ConsoleToken copies the
// answer into the console token, which holds the console open only while that
// credential is live.
const (
	LocalsSessionID = "session_id"
	LocalsAPIKeyID  = "api_key_id"
)

// AuthenticatedWithSession reports whether the request was authenticated by an
// interactive session. It is what the registry's InteractiveOnly gate asks
// (registry.go), and it is an ALLOWLIST on purpose: the gate admits a session and
// nothing else, so a principal type added tomorrow — a service token, a delegated
// credential — is refused on these routes until somebody decides it belongs there,
// instead of admitted until somebody remembers to refuse it. A handler has no
// business asking: "this route is not for a key" is declared on the route, in one
// place, and enforced ahead of the handler.
func AuthenticatedWithSession(c fiber.Ctx) bool {
	method, _ := c.Locals(LocalsAuthMethod).(string)
	return method == AuthMethodSession
}

// CredentialLiveness answers whether a sign-in is still live, by
// IsConsoleCredentialLive (queries/sessions.sql): its user is active, and the
// session or API key it names is the user's, not revoked and not expired.
// *db.Queries is one; it is an interface so that the registry's InteractiveOnly
// gate can be handed it the way it is handed a node lookup, and a test can say
// what it answers.
type CredentialLiveness interface {
	IsConsoleCredentialLive(ctx context.Context, arg db.IsConsoleCredentialLiveParams) (bool, error)
}

// sessionEndedMessage answers a request whose access token belongs to a session
// that has ended, or names none, on a route that is held to a live one.
const sessionEndedMessage = "Session has ended"

// errSessionEnded and errNoSession are LiveCredential's two refusals: the session the
// access token names has ended, or the token names none (it was issued before tokens
// named their session). Both are 401 "Session has ended"; they are two values so that
// a caller can tell them apart with errors.Is, as wsTokenRefusal does, without reading
// the status code — which another refusal could share.
var (
	errSessionEnded = fiber.NewError(fiber.StatusUnauthorized, sessionEndedMessage)
	errNoSession    = fiber.NewError(fiber.StatusUnauthorized, sessionEndedMessage)
)

// LiveCredential is the credential the request was authenticated with, on a route
// that is held to a live one: the API key authenticateAPIKey checked in this very
// request, or the session the access token belongs to, which is looked up here.
// Its callers are the routes that mint something that outlives the request — the
// console token, which names the answer and is held to it while its console is
// open; the hub token; and, through the registry's InteractiveOnly gate, the
// routes that change the account's own credentials or factors: an API key's
// creation, a second factor's enrolment, disabling, recovery codes. The owner's
// remedies (Endpoint.AdmitsEndedSession) are the exception.
//
// An access token outlives its session by up to its own lifetime (ACCESS_TOKEN_TTL,
// fifteen minutes by default): a sign-out, a revoke from the session list and every
// revoke-all (a password change, Sign out all devices, a deactivation) end the
// session at once, and authRequired, which trusts the token's signature, admits it
// for the rest of it. A token that was stolen and then cut off that way must not, in
// that time, mint a key or a second factor that outlives it.
//
// A session that has ended, and an access token that names none (issued before
// tokens named their session), get 401 "Session has ended": the SPA answers a 401
// with a refresh, which ends the session there too — or, for the older token,
// mints one that names it. A session that could not be checked is a 503, never a
// yes, and any other failure of the check a 500.
func LiveCredential(c fiber.Ctx, live CredentialLiveness) (auth.ConsoleCredential, error) {
	if method, _ := c.Locals(LocalsAuthMethod).(string); method == AuthMethodAPIKey {
		keyID, _ := c.Locals(LocalsAPIKeyID).(uuid.UUID)
		if keyID == uuid.Nil {
			return auth.ConsoleCredential{}, fiber.NewError(fiber.StatusUnauthorized, "Authentication required")
		}
		return auth.ConsoleCredential{APIKeyID: keyID}, nil
	}
	userID, _ := c.Locals("user_id").(uuid.UUID)
	sessionID, _ := c.Locals(LocalsSessionID).(uuid.UUID)
	if userID == uuid.Nil || sessionID == uuid.Nil {
		return auth.ConsoleCredential{}, errNoSession
	}
	ctx, cancel := context.WithTimeout(c.Context(), authDBTimeout)
	ok, err := live.IsConsoleCredentialLive(ctx, db.IsConsoleCredentialLiveParams{
		UserID:    userID,
		SessionID: pgtype.UUID{Bytes: sessionID, Valid: true},
	})
	cancel()
	route := c.Route().Path
	switch {
	case isTransientDBError(err):
		slog.Warn("the caller's session could not be checked; answering 503", "route", route, "user_id", userID, "error", err)
		return auth.ConsoleCredential{}, fiber.NewError(fiber.StatusServiceUnavailable, "Service temporarily unavailable")
	case err != nil:
		slog.Error("checking the caller's session failed; answering 500", "route", route, "user_id", userID, "error", err)
		return auth.ConsoleCredential{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to check the session")
	case !ok:
		slog.Info("refused, as the caller's session has ended", "route", route, "user_id", userID, "session_id", sessionID)
		return auth.ConsoleCredential{}, errSessionEnded
	}
	return auth.ConsoleCredential{SessionID: sessionID}, nil
}
