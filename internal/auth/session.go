package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// PreviousTokenRevocationWindow is how long after a refresh rotates a session's
// token the token it replaced can still sign that session out (Logout only —
// see FindSessionForLogout; it never authenticates a refresh).
//
// What it has to cover is one sign-out request that left the browser carrying
// the old cookie before the refresh response's Set-Cookie was processed: the
// span from the rotation committing to that request arriving, which is one
// response and one request in flight. That is milliseconds on a LAN and a few
// seconds on a poor link, and nothing in the application bounds a response's
// flight time (the server sets no write timeout and the SPA's auth requests
// carry no fetch timeout), so a retransmitting connection can stretch it to
// tens of seconds. Two minutes is comfortably past that and still well inside
// the 15-minute access token.
//
// What a longer window would cost: for as long as it lasts, a rotated-away
// token — a log line or proxy capture that held the previous cookie — can end
// its session. It can do nothing else with it, and a holder of the CURRENT token
// could do far more, so the window is sized for the race rather than for any
// threat; there is no reason to make it generous.
const PreviousTokenRevocationWindow = 2 * time.Minute

// ConcurrentRefreshTolerance is how long after a refresh rotates a session's
// token the token it replaced is still recognised as "superseded by a concurrent
// refresh" — the one question RefusalSparesCookie answers. Nothing is issued on
// that recognition; it only changes the SHAPE of a refusal (409 with the cookie
// left alone, instead of 401 with it cleared).
//
// It has to cover one in-flight race and no more. Two requests carrying one
// cookie — two tabs of a browser, whose cookie jar they share — leave before
// either response is processed, so the loser reaches the server either a few
// milliseconds before the winner commits (and is refused at the rotation) or up
// to one response's flight after it (and is refused at validation). That span is
// a round trip: milliseconds on a LAN, a few seconds on a poor link. Ten seconds
// covers it with room.
//
// It is far shorter than PreviousTokenRevocationWindow on purpose. For as long as
// it lasts, a refusal of the replaced token reads differently from a refusal of
// any other stale token — which a holder of that token can observe — so the
// window is kept to the race rather than to anything the race might one day need.
const ConcurrentRefreshTolerance = 10 * time.Second

// ErrSessionRefused is returned by CreateSession, having created nothing, when
// the credential check the session was to be issued on no longer holds: every
// session of the user was ended since the check read the user (a sign-out of all
// devices, a password change, an admin deactivating the account — each a revoke-all
// that bumps users.auth_epoch), or the account has since been deactivated or
// removed. It is an answer about the sign-in and not a failure of the database:
// callers refuse the sign-in — a 401 telling the user to sign in again, no token,
// no cookie, no login audit row — and must not treat it as the 500/503 of a
// statement that did not run.
var ErrSessionRefused = errors.New("sign-in refused: the credential check no longer holds")

// DeviceInfo describes the client device a session is being created for.
// All fields are optional; a zero-value DeviceInfo creates an un-tagged session
// (matching the legacy behavior).
type DeviceInfo struct {
	Name string // human-friendly name (e.g. "Pixel 8 Pro", "Chrome on macOS")
	Type string // one of: "web", "mobile", "desktop" (empty = untagged)
	ID   string // stable per-device identifier (mobile only)
}

func nullText(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

// SessionManager handles session lifecycle in Redis + PostgreSQL.
type SessionManager struct {
	queries *db.Queries
	redis   *redis.Client

	// mirrorWait is how long CreateSession waits for the Redis mirror of a session it
	// has created; sessionMirrorWait when zero. A field so that a test can watch the
	// wait end in milliseconds.
	mirrorWait time.Duration
}

// sessionMirrorWait is how long CreateSession waits for the Redis write of a session it
// has just created before it carries on without it, and sessionMirrorTimeout is how
// long that write is then allowed to run on its own. See mirrorSession.
const (
	sessionMirrorWait    = time.Second
	sessionMirrorTimeout = 5 * time.Second
)

// redisSession is the JSON stored under nexara:session:<id>. Nothing reads it
// back (see WriteSessionRedis), so it is a mirror of the session, not a lookup
// structure.
type redisSession struct {
	SessionID string `json:"session_id"`
	UserID    string `json:"user_id"`
	TokenHash string `json:"token_hash"`
	Role      string `json:"role"`
}

// NewSessionManager creates a new session manager.
func NewSessionManager(queries *db.Queries, rdb *redis.Client) *SessionManager {
	return &SessionManager{
		queries: queries,
		redis:   rdb,
	}
}

// HashToken returns the SHA-256 hex digest of a refresh token.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func redisKey(sessionID string) string {
	return "nexara:session:" + sessionID
}

// CreateSession stores a new session in both PostgreSQL and Redis, and only while
// the credential check it is issued on still holds.
//
// epoch is the user's auth_epoch AS THE CREDENTIAL CHECK READ IT — the generation
// carried by the row that was read next to the password comparison, the LDAP bind
// or the identity provider's answer, or recorded in the TOTP pending token or the
// SSO exchange code when the check was an earlier step. It is never the epoch of a
// user row read afterwards: a caller that re-read the user after a revoke-all and
// passed that row's epoch would hand the insert the new generation and make the
// condition vacuous. There is no default for it, and no overload without it, so
// that every caller has to say where its value came from.
//
// The insert (CreateSessionAtEpoch) takes a lock on the user's row and goes ahead
// only while the row is active and still holds epoch; otherwise nothing is created
// and the error is ErrSessionRefused. That is what ties a sign-in to the revoke-all
// that has since invalidated its credential — see the query for why it holds for
// both orders in which the two can meet, and RevokeAllUserSessionsIn for the other
// half.
//
// The Redis row is written after the insert has committed and is best effort: it
// mirrors the session for nobody to read, so a failed write is logged and the
// session is returned all the same, and one that has not finished after a second is
// not waited for (mirrorSession).
//
// role is the user's legacy role at the time the session was issued. It is
// persisted to sessions.user_role so the Refresh handler can detect a role
// rotation (e.g. admin demoting the user) and force re-login.
func (sm *SessionManager) CreateSession(ctx context.Context, userID uuid.UUID, epoch int64, refreshToken, role, userAgent, ipAddress string, ttl time.Duration, device DeviceInfo) (db.Session, error) {
	tokenHash := HashToken(refreshToken)
	expiresAt := time.Now().Add(ttl)

	session, err := sm.queries.CreateSessionAtEpoch(ctx, db.CreateSessionAtEpochParams{
		UserID:     userID,
		Epoch:      epoch,
		TokenHash:  tokenHash,
		UserAgent:  userAgent,
		IpAddress:  ipAddress,
		ExpiresAt:  expiresAt,
		DeviceName: nullText(device.Name),
		DeviceType: nullText(device.Type),
		DeviceID:   nullText(device.ID),
		UserRole:   role,
	})
	if err != nil {
		// No row is the insert's refusal (see ErrSessionRefused), and it is the only
		// thing pgx.ErrNoRows can mean for a statement that RETURNs what it inserted:
		// any other error is the database failing, and says nothing about the
		// credential.
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Session{}, fmt.Errorf("creating session for user %s: %w", userID, ErrSessionRefused)
		}
		return db.Session{}, fmt.Errorf("creating session in db: %w", err)
	}

	// The session exists from here on. Its Redis row is a mirror that nothing reads
	// (see WriteSessionRedis), so a failure to write it is logged and nothing more:
	// failing the sign-in over it would answer "nothing was issued" for a session
	// that is live in PostgreSQL, with no token anybody holds for it. Nor may a Redis
	// that does not answer hold the sign-in: see mirrorSession.
	sm.mirrorSession(ctx, session.ID, userID, tokenHash, role, ttl)

	return session, nil
}

// mirrorSession writes the Redis row of a session CreateSession has just created, and
// waits for it for at most sessionMirrorWait.
//
// The write runs on a goroutine of its own, on a context that is not the caller's —
// the caller's is cancelled as soon as CreateSession returns, and the write must not
// die with it — under a deadline of its own, sessionMirrorTimeout. The caller waits
// for whichever comes first, the write's end or the wait's. That is what bounds it:
// the bound cannot be left to the Redis client. The one production builds
// (pkg/redisutil, from redis.ParseURL with its defaults) does not let a context's
// deadline reach the socket (ContextTimeoutEnabled is off), so a Redis that accepts
// the connection and never answers would hold every sign-in for the client's read
// timeout, five seconds, and a failed read is tried again. Not waiting is the only
// bound that holds whatever the client does.
//
// A write that is given up on is not cancelled. It ends when the client's own timeout
// ends it, and if it lands afterwards the row is one nothing reads for a session that
// exists. Everything about it is a warning and nothing else — what failed, for which
// session and which user, never a token or its hash — and a panic in it is recovered
// into one, because a goroutine nothing is waiting on must not take the process down
// for the sake of a cache row.
func (sm *SessionManager) mirrorSession(ctx context.Context, sessionID, userID uuid.UUID, tokenHash, role string, ttl time.Duration) {
	done := make(chan struct{})
	go func() { //nolint:gosec // G118: intentionally detached — the write must outlive the request, and nothing may wait on it for long
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				slog.Error("create session: the Redis mirror write panicked; the session exists and nothing reads the row",
					"session_id", sessionID, "user_id", userID, "panic", r)
			}
		}()
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sessionMirrorTimeout)
		defer cancel()
		if err := sm.WriteSessionRedis(wctx, sessionID, userID, tokenHash, role, ttl); err != nil {
			slog.Warn("create session: the Redis mirror of the session was not written; the session exists and nothing reads the row",
				"session_id", sessionID, "user_id", userID, "error", err)
		}
	}()

	wait := sm.mirrorWait
	if wait <= 0 {
		wait = sessionMirrorWait
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		slog.Warn("create session: the Redis mirror write is taking too long and is not being waited for; the session exists and nothing reads the row",
			"session_id", sessionID, "user_id", userID, "waited", wait)
	}
}

// ValidateRefreshToken looks up a session by refresh token hash in PostgreSQL.
// Returns the session if valid and not expired/revoked.
//
// There are three answers, and they must not be confused. A session: the token
// is live. ErrInvalidToken: no live session holds this token — no row, revoked,
// or expired. Any other error: the lookup itself failed, so nothing is known
// about the token. That last one is returned wrapped and is NOT "invalid": a
// caller that read it as invalid would clear a perfectly good cookie and sign
// the user out over a database blip (Refresh), or report a sign-out as done when
// it never looked (Logout).
//
// It matches the session's CURRENT hash and nothing else. A token that a refresh
// has rotated away is held in previous_token_hash for FindSessionForLogout's
// sake, and must not authenticate anything here: this is the lookup behind
// Refresh (which issues tokens) and behind the session list's is_current, and
// either one honouring a rotated-away token would turn it into a second valid
// credential. The rule lives in the query this calls (GetSessionByTokenHash),
// not in this wrapper.
func (sm *SessionManager) ValidateRefreshToken(ctx context.Context, refreshToken string) (db.Session, error) {
	tokenHash := HashToken(refreshToken)

	session, err := sm.queries.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Session{}, ErrInvalidToken
		}
		return db.Session{}, fmt.Errorf("looking up session by token hash: %w", err)
	}

	if session.IsRevoked {
		return db.Session{}, ErrInvalidToken
	}

	if time.Now().After(session.ExpiresAt) {
		return db.Session{}, ErrInvalidToken
	}

	return session, nil
}

// FindSessionForLogout resolves the session a sign-out presented a refresh token
// for, so that sign-out can revoke it. It accepts the session's current token,
// exactly as ValidateRefreshToken does, and ALSO the token the session had
// before its last rotation for PreviousTokenRevocationWindow after that
// rotation; viaPrevious says which of the two matched, so that the caller can
// leave a mark of the second.
//
// The second case is the race this exists for. A sign-out and a refresh sent
// close together can reach the server in either order. When the refresh lands
// first it rotates the token, and the sign-out — still carrying the old cookie,
// because the refresh response had not yet reached the browser — would find no
// session, revoke nothing and answer success, while the refresh's Set-Cookie
// goes on to hand the browser a working cookie for the session that was meant
// to end. Matching the previous token for a moment closes that.
//
// ONE GENERATION only: the session remembers the token it had immediately before
// its last rotation and nothing older. A sign-out that carries a token two
// rotations back — it would have to have been in flight across two refreshes
// inside the window — finds nothing.
//
// This is for REVOCATION ONLY. Ending a session on the strength of a rotated-away
// token is harmless — the worst a holder of one can do is sign that session out —
// whereas honouring it anywhere that issues a token would make every rotated
// token a second valid credential. Refresh, the session list and anything else
// that decides who is "current" keep using ValidateRefreshToken, which never
// matches it.
//
// Returns ErrInvalidToken when no live session matches either token (unknown,
// revoked, expired, or rotated away longer ago than the window), which callers
// treat as "nothing to revoke". Any other error means a lookup could not be
// made, from EITHER of the two, and is returned at once: a failed first lookup is
// not "no session", so it does not fall through to the second, and a caller must
// not take it for "nothing to revoke" either.
func (sm *SessionManager) FindSessionForLogout(ctx context.Context, refreshToken string) (session db.Session, viaPrevious bool, err error) {
	session, err = sm.ValidateRefreshToken(ctx, refreshToken)
	if err == nil {
		return session, false, nil
	}
	if !errors.Is(err, ErrInvalidToken) {
		return db.Session{}, false, err
	}

	session, err = sm.queries.GetSessionByPreviousTokenHash(ctx, db.GetSessionByPreviousTokenHashParams{
		TokenHash:     HashToken(refreshToken),
		WindowSeconds: PreviousTokenRevocationWindow.Seconds(),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.Session{}, false, ErrInvalidToken
		}
		return db.Session{}, false, fmt.Errorf("looking up session by previous token hash: %w", err)
	}

	// The query already requires a live session (not revoked, not expired); this
	// re-check is the second line, as in ValidateRefreshToken, and costs nothing.
	if time.Now().After(session.ExpiresAt) {
		return db.Session{}, false, ErrInvalidToken
	}

	return session, true, nil
}

// RefusalSparesCookie decides one thing: whether a refresh that is about to be
// refused should leave the browser's refresh cookie alone. It is true when the
// presented token is the one a LIVE session held immediately before its last
// rotation and that rotation happened within ConcurrentRefreshTolerance — which
// is what a refresh that lost a race to another refresh of the same session looks
// like, whichever side of the winner's commit it fell on.
//
// A true answer makes the refusal Refresh's 409 refresh_superseded rather than
// its 401, and the cookie must then not be cleared: the cookie jar is shared by
// every tab of the browser, so it may already hold the winner's newer cookie, and
// a Set-Cookie deletion cannot be made conditional on the cookie's value. Every
// other refusal — an unknown token, one older than a single rotation, one
// outside the tolerance, the predecessor of a session that is revoked or expired
// — answers 401 and clears the cookie, as a stale token always has.
//
// It NEVER authenticates, and nothing may use it to. It returns a bool and not a
// session on purpose, so that the match cannot be turned into a token or into an
// identity: a rotated-away token still cannot be exchanged for a new one
// (ValidateRefreshToken never matches it), and this only picks which of two
// refusals the caller sends. The query behind it is the revocation lookup,
// GetSessionByPreviousTokenHash, used with the short tolerance as its window.
//
// A lookup that fails answers false. That is deliberately the opposite of
// ValidateRefreshToken's rule, because here "could not look" has a safe reading:
// the caller already knows the token is not current, so it is refused either way,
// and the conservative refusal is the 401 that clears the cookie. The failure is
// logged.
//
// A true answer is logged too, at Info, with the session id and nothing that
// could be replayed. A lost race is rare and benign, which is why it is Info and
// not Debug: it is exactly the line that lets an operator tell "this refresh lost
// to another refresh" from "this refresh was refused because the session was
// revoked", which look alike from the outside as a failed refresh. Ordinary stale
// tokens are NOT logged; they are common.
func (sm *SessionManager) RefusalSparesCookie(ctx context.Context, refreshToken string) bool {
	session, err := sm.queries.GetSessionByPreviousTokenHash(ctx, db.GetSessionByPreviousTokenHashParams{
		TokenHash:     HashToken(refreshToken),
		WindowSeconds: ConcurrentRefreshTolerance.Seconds(),
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("refresh: could not check whether the refused token was superseded; refusing it as stale", "error", err)
		}
		return false
	}
	// The query already requires a live session; this re-check is the second line.
	if time.Now().After(session.ExpiresAt) {
		return false
	}
	slog.Info("refresh: the refused token was replaced by a concurrent refresh moments ago; answering 409 and sparing the cookie",
		"session_id", session.ID)
	return true
}

// RotateRefreshToken replaces the refresh token of the session a refresh has just
// validated, through q — the Queries of the transaction the refresh is running
// in. It returns ErrInvalidToken, having changed nothing, when the session is no
// longer what that validation saw: revoked since (a sign-out, sign-out
// everywhere, a revoke from the session list, a password change, an admin
// deactivating the user), expired since, or already rotated by another refresh
// that presented the same token. The caller must then issue nothing — no access
// token, no cookie — and answer 401.
//
// Validation and rotation are separate statements, and a revoke can land between
// them. Before this check a refresh that validated just ahead of a sign-out
// rotated the revoked session anyway and handed back a 15-minute access token
// the sign-out had been meant to deny, and two refreshes racing on one cookie
// both succeeded with different new cookies. RotateSessionToken's WHERE clause
// is where the decision is made, atomically with the write; this function exists
// so that the "0 rows means refused" half cannot be forgotten by a caller, which
// is why it is the only caller of that query.
//
// session is what ValidateRefreshToken returned. Its TokenHash is the hash the
// refresh presented, and is what the rotation is conditional on.
func RotateRefreshToken(ctx context.Context, q *db.Queries, session db.Session, newTokenHash, role string) error {
	rows, err := q.RotateSessionToken(ctx, db.RotateSessionTokenParams{
		ID:           session.ID,
		OldTokenHash: session.TokenHash,
		NewTokenHash: newTokenHash,
		UserRole:     role,
	})
	if err != nil {
		return fmt.Errorf("rotating session token: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("session %s changed since its refresh token was validated: %w", session.ID, ErrInvalidToken)
	}
	return nil
}

// WriteSessionRedis writes (or overwrites) the Redis row for a session. The
// Refresh handler runs the DB rotation inside a transaction (so the user load,
// the role-rotation check and the rotation commit or roll back together) and
// then calls this helper post-commit.
//
// Nothing reads these rows. Authentication is the JWT plus the Postgres sessions
// table (ValidateRefreshToken, FindSessionForLogout, RefusalSparesCookie,
// RotateRefreshToken); no code looks a nexara:session:* key up. The rows are
// written at login and at rotation and deleted at revocation, and that is all
// that happens to them. So a failed write is not load-bearing, and neither is a
// row that outlives its session: a refresh that commits just before a sign-out
// can write its row just AFTER the sign-out has deleted it, and nothing is the
// worse for it. Should a reader ever be added it must treat a row as a hint and
// confirm against Postgres, for exactly that reason.
//
// Because nothing waits on the row, a caller on the request path must not wait on
// it for long either: Refresh writes it from its own goroutine, after the response is
// on its way, so that a slow Redis cannot delay the new cookie reaching the browser,
// and CreateSession waits for it for at most a second before carrying on without it
// (mirrorSession).
func (sm *SessionManager) WriteSessionRedis(ctx context.Context, sessionID, userID uuid.UUID, tokenHash, role string, ttl time.Duration) error {
	rs := redisSession{
		SessionID: sessionID.String(),
		UserID:    userID.String(),
		TokenHash: tokenHash,
		Role:      role,
	}
	data, err := json.Marshal(rs)
	if err != nil {
		return fmt.Errorf("marshaling redis session: %w", err)
	}
	if err := sm.redis.Set(ctx, redisKey(sessionID.String()), data, ttl).Err(); err != nil {
		return fmt.Errorf("updating session in redis: %w", err)
	}
	return nil
}

// RevokeSession marks a session as revoked in both PostgreSQL and Redis.
func (sm *SessionManager) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	if err := sm.queries.RevokeSession(ctx, sessionID); err != nil {
		return fmt.Errorf("revoking session in db: %w", err)
	}
	sm.redis.Del(ctx, redisKey(sessionID.String()))
	return nil
}

// RevokeAllUserSessionsIn ends every session of a user through q, and returns the
// ids of the sessions that were live — the ones with Redis rows to delete. EVERY
// revoke-all goes through it — a password change, a sign-out of all devices, an
// admin deactivating the account — and that is what makes the epoch below mean
// "this user's sessions were just ended".
//
// It is THREE statements, in this order, and the order is the point:
//
//  1. BumpUserAuthEpoch. A session is created only against the epoch its credential
//     check read (CreateSession), so moving the epoch is what refuses every sign-in
//     whose check is older than this revoke-all. It runs FIRST, as a statement of
//     its own, because it waits for the row lock of any sign-in that is in the middle
//     of its insert: when it returns, every session created against the old epoch
//     has committed, and the next two statements — each with a snapshot of its own
//     under READ COMMITTED — see it. Written as one statement together with the
//     revoke (a data-modifying CTE) it would share the revoke's single snapshot, and
//     a session committed while it waited for that lock would be invisible to it and
//     survive. Revoking first and bumping afterwards leaves a session inserted
//     between the two live: the epoch it was conditional on has not moved yet.
//  2. ListUserSessions, the live sessions, for their Redis rows.
//  3. RevokeAllUserSessions.
//
// q is the Queries of a transaction the caller owns when the revoke has to commit
// or roll back together with something else: a password change must end the user's
// sessions or not happen at all, and a change that lands while the revoke fails
// leaves every other device signed in on the old credentials; a deactivation must
// not leave an account disabled with its sessions live. The bump takes the user
// row's lock and the transaction holds it until it ends, so a sign-in racing the
// revoke-all queues behind the whole of it and is decided against the committed
// epoch — and if the transaction rolls back the epoch is unchanged and the sign-in
// goes ahead, as nothing was revoked. Nothing here touches the pool or Redis, so a
// caller that holds a transaction never asks the pool for a second connection by
// calling it; the Redis rows are deleted with ForgetSessions once — and only if —
// the transaction has committed.
//
// When q is the pool's (the sign-out of all devices) the three are separate
// autocommit statements and the order is still the safe one. A sign-in that inserts
// after the bump carries the new epoch — it read the user after the bump committed,
// so it is a new sign-in and not a replaced credential — or is refused; the
// listing and the revoke can therefore meet, besides the old sessions, only such a
// new sign-in that finished in the same instants, and ending it is a
// sign-out-everywhere racing a sign-in, which is what was asked for. A crash or a
// failed statement between the three leaves a bumped epoch and sessions that are
// still live: the caller reports the failure — the sessions may still be active —
// and repeating the call ends them; the bump on its own only refuses sign-ins that
// were in flight, who sign in again. The reverse order would leave a session
// inserted between the revoke and the bump live, with nothing to say so.
func RevokeAllUserSessionsIn(ctx context.Context, q *db.Queries, userID uuid.UUID) ([]uuid.UUID, error) {
	// The generation first; see above. How many rows it changed is not a decision:
	// no row is a user that is gone, and there is nothing to refuse for one.
	if _, err := q.BumpUserAuthEpoch(ctx, userID); err != nil {
		return nil, fmt.Errorf("bumping the user's auth epoch: %w", err)
	}

	// The live sessions, for their Redis rows.
	sessions, err := q.ListUserSessions(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listing user sessions: %w", err)
	}

	if err := q.RevokeAllUserSessions(ctx, userID); err != nil {
		return nil, fmt.Errorf("revoking all sessions in db: %w", err)
	}

	ids := make([]uuid.UUID, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	return ids, nil
}

// ForgetSessions deletes the Redis rows of sessions that have been revoked, in one
// command. It is best effort and says nothing: nothing reads these rows (see
// WriteSessionRedis), so a row that survives is not a live session, and the call
// must not decide anything.
func (sm *SessionManager) ForgetSessions(ctx context.Context, ids []uuid.UUID) {
	if len(ids) == 0 {
		return
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, redisKey(id.String()))
	}
	sm.redis.Del(ctx, keys...)
}
