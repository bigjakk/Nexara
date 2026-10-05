package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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

// All 4 routes are declared in internal/api/registry_users.go, which states
// their parameters and, for three of them, their permission. The fourth —
// Update — is Deferred and keeps BOTH of its checks here, because the second
// one fires only when the body carries `role`; see userUpdateReason.
//
// What else stays here is what a declaration cannot see: the refusal to touch
// the system account, the refusal to change your own role or active status, the
// refusal to delete your own account, and the session revocation that has to
// follow a deactivation — which is part of the deactivation's own transaction.

// txOptionsBeginner is what a deactivation needs from the connection pool: a
// transaction whose isolation level it names itself. *pgxpool.Pool satisfies it, and
// so does txBeginner, which the auth handlers use and whose BeginTx is what Register
// begins its own with. It is the narrower of the two so that this handler asks for
// nothing it does not use.
type txOptionsBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// UserHandler handles user management endpoints.
type UserHandler struct {
	pool     txOptionsBeginner
	queries  *db.Queries
	rbac     *auth.RBACEngine
	eventPub *events.Publisher
	sessions *auth.SessionManager

	// dbTimeout bounds the deciding database work of a deactivation — the
	// transaction, begin to commit — and is authDBTimeout when zero.
	// followUpTimeout bounds each follow-up of a decision that has been made (the
	// RBAC cache, the audit row, the Redis cleanup) and is authFollowUpTimeout when
	// zero. Fields rather than constants so a test can make starvation fail in
	// milliseconds instead of waiting out the production bounds, as AuthHandler's do.
	dbTimeout       time.Duration
	followUpTimeout time.Duration
}

// NewUserHandler creates a new user handler. pool is what a deactivation begins its
// transaction on; passing nil is supported only for unit tests that never reach one.
func NewUserHandler(pool *pgxpool.Pool, queries *db.Queries, rbac *auth.RBACEngine, eventPub *events.Publisher, sessions *auth.SessionManager) *UserHandler {
	h := &UserHandler{
		queries:  queries,
		rbac:     rbac,
		eventPub: eventPub,
		sessions: sessions,
	}
	// Assigned only when there is a pool, for the reason NewAuthHandler gives: a nil
	// *pgxpool.Pool stored in the interface would make `h.pool == nil` false and the
	// first Begin would dereference it.
	if pool != nil {
		h.pool = pool
	}
	return h
}

type userListResponse struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	Roles       []string  `json:"roles"`
	IsActive    bool      `json:"is_active"`
	AuthSource  string    `json:"auth_source"`
	TotpEnabled bool      `json:"totp_enabled"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

// List handles GET /api/v1/users.
func (h *UserHandler) List(c fiber.Ctx, _ *apischema.Params) error {
	users, err := h.queries.ListUsersWithRoles(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list users")
	}

	resp := make([]userListResponse, len(users))
	for i, u := range users {
		var roles []string
		if rbacStr, ok := u.RbacRoles.(string); ok && rbacStr != "" {
			roles = strings.Split(rbacStr, ", ")
		}
		if roles == nil {
			roles = []string{}
		}
		resp[i] = userListResponse{
			ID:          u.ID,
			Email:       u.Email,
			DisplayName: u.DisplayName,
			Role:        u.Role,
			Roles:       roles,
			IsActive:    u.IsActive,
			AuthSource:  u.AuthSource,
			TotpEnabled: u.TotpEnabled,
			CreatedAt:   u.CreatedAt.Format(time.RFC3339Nano),
			UpdatedAt:   u.UpdatedAt.Format(time.RFC3339Nano),
		}
	}

	return RespondItems(c, resp)
}

// Get handles GET /api/v1/users/:id.
func (h *UserHandler) Get(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	user, err := h.queries.GetUserByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "User not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}

	return c.JSON(userListResponse{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		IsActive:    user.IsActive,
		AuthSource:  user.AuthSource,
		TotpEnabled: user.TotpSecret.Valid,
		CreatedAt:   user.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   user.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// Update handles PUT /api/v1/users/:id.
func (h *UserHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	// Deferred, so BOTH checks live here. This one is unconditional and runs
	// before anything is read or written; the manage:role one below fires only
	// when the body carries `role`. See userUpdateReason in
	// internal/api/registry_users.go for why the pair is not a static Check.
	if err := requirePerm(c, "manage", "user"); err != nil {
		return err
	}

	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if id == auth.SystemUserID {
		return fiber.NewError(fiber.StatusForbidden, "Cannot modify the system account")
	}

	existing, err := h.queries.GetUserByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "User not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}

	// Tristate reads throughout: every refusal below keys on the field having
	// been SUPPLIED, not on its value, so a request that never mentioned
	// is_active must not be read as "set it to false".
	//
	// profile carries ONLY what was supplied, and UpdateUserProfile writes only that.
	// The account read above is for the 404 and the refusals; writing its values
	// back for the fields the request did not mention was a lost update — a
	// deactivation put back the role a concurrent edit had just demoted, and a name
	// edit that had read is_active = true re-activated an account deactivated in the
	// meantime.
	profile := db.UpdateUserProfileParams{ID: id}

	callerID, _ := c.Locals("user_id").(uuid.UUID)

	if v, supplied := p.OptString("display_name"); supplied {
		profile.DisplayName = pgtype.Text{String: v, Valid: true}
	}
	deactivating := false
	if v, supplied := p.OptBool("is_active"); supplied {
		if callerID == id {
			return fiber.NewError(fiber.StatusForbidden, "Cannot change your own active status")
		}
		profile.IsActive = pgtype.Bool{Bool: v, Valid: true}
		deactivating = !v
	}
	if v, supplied := p.OptString("role"); supplied {
		if callerID == id {
			return fiber.NewError(fiber.StatusForbidden, "Cannot change your own role")
		}
		// The second, CONDITIONAL half of this route's authorization — the
		// reason it is declared Deferred rather than Check. The vocabulary
		// check that used to follow is now the schema's enum.
		if err := requirePerm(c, "manage", "role"); err != nil {
			return fiber.NewError(fiber.StatusForbidden, "Only role managers can change user roles")
		}
		profile.Role = pgtype.Text{String: v, Valid: true}
	}

	var user db.User
	var revoked []uuid.UUID
	if deactivating {
		// The change and the end of every session of the account are ONE
		// transaction; see deactivate for what that buys and what its answers mean.
		var unconfirmed bool
		user, revoked, unconfirmed, err = h.deactivate(c, profile)
		if err != nil {
			if unconfirmed {
				// The COMMIT's answer was lost: the deactivation, and the end of the
				// account's sessions, may have taken effect. The audit row is written
				// all the same — an audit filtered on user_updated must not miss a
				// deactivation that landed — and says the outcome is unconfirmed,
				// which is true whichever way it went. The RBAC cache is purged too:
				// a purge is right in both outcomes. The Redis rows of the sessions
				// are left to their TTL, which is safe in both.
				h.withFollowUp(c, func() {
					h.rbac.InvalidateUser(c.Context(), id)
				})
				unconfirmedDetails, _ := json.Marshal(map[string]string{"email": existing.Email, "outcome": "unconfirmed"})
				h.withFollowUp(c, func() {
					AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "user", id.String(), "user_updated", unconfirmedDetails)
				})
			}
			return err
		}
	} else {
		user, err = h.queries.UpdateUserProfile(c.Context(), profile)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to update user")
		}
	}

	// What is left runs once the change has been made — for a deactivation, once its
	// transaction has COMMITTED, never inside it — and each step under a deadline of
	// its own (see followUp), so that a request which spent its budget getting here
	// still records the change and cleans up after it. The RBAC cache and the audit
	// row come first; the Redis rows of the revoked sessions are a cache nothing
	// reads, may take their whole deadline, and so go last.
	h.withFollowUp(c, func() {
		h.rbac.InvalidateUser(c.Context(), id)
	})

	details, _ := json.Marshal(map[string]string{"email": user.Email})
	h.withFollowUp(c, func() {
		AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "user", id.String(), "user_updated", details)
	})

	if h.sessions != nil && len(revoked) > 0 {
		fctx, cancel := h.followUp(c.Context())
		h.sessions.ForgetSessions(fctx, revoked)
		cancel()
	}

	return c.JSON(userListResponse{
		ID:          user.ID,
		Email:       user.Email,
		DisplayName: user.DisplayName,
		Role:        user.Role,
		IsActive:    user.IsActive,
		AuthSource:  user.AuthSource,
		TotpEnabled: user.TotpSecret.Valid,
		CreatedAt:   user.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   user.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// The answers of a deactivation whose transaction does not complete. The change to
// the account and the end of its sessions are ONE transaction, so there are only
// three things to tell the administrator: it was not done, it was not done and the
// database was the reason, or it may have been.
//
// deactivationNotDoneUnavailableMessage and deactivationNotDoneMessage are for a
// failure before the COMMIT, or a COMMIT the server itself answered or that never
// went out: the transaction rolled back, the account is as it was — still active,
// every session untouched — and repeating the request is safe.
// deactivationUnconfirmedMessage is for a COMMIT whose answer was lost.
const (
	deactivationNotDoneUnavailableMessage = "The database could not be reached or did not answer in time, and the account was NOT changed: " +
		"it is still active and its sessions are untouched. Please try again shortly."
	deactivationNotDoneMessage     = "Failed to update the user; the account was NOT changed."
	deactivationUnconfirmedMessage = "The change could not be confirmed: the account may now be deactivated, with every session of it ended, " +
		"or may still be active. Check the account's state; repeating the request is safe."
)

// deactivate applies a profile change that sets the account inactive and ends every
// session of the account — the change, the bump of the account's auth epoch, the
// listing and the revoke — in ONE transaction, and returns the updated account and
// the ids of the sessions that were live, for their Redis rows.
//
// Why one transaction. They used to be two steps on the pool: the account was
// disabled and then its sessions revoked, and a revoke that failed was only logged.
// The answer was a 200 for an account that was disabled with its sessions unrevoked:
// a holder that tries to refresh while the account is inactive is refused, and that
// session revoked then, but one that does not stays live in the database, and the
// first reactivation — an administrator undoing a mistake — brings every such
// session back, a stolen one among them. Now the revoke fails the request: the
// transaction rolls back, the account stays active, and the answer says so. And the
// epoch bump is what stops a sign-in that was in flight from minting a session after
// the revoke: see RevokeAllUserSessionsIn and queries/sessions.sql
// CreateSessionAtEpoch.
//
// What it must not do while the transaction is open is ask the pool for another
// connection, which would deadlock a pool of N connections under N+1 such requests
// (every connection held by a request waiting for another): everything between
// Begin and Commit goes through the transaction's own queries, and nothing touches
// Redis. The RBAC cache and the Redis rows of the revoked sessions are the caller's
// to deal with once this has returned, which is after the COMMIT. The whole of it —
// begin, statements, commit — is bounded by authDBTimeout, so a pool with no free
// connection or a statement stuck on a lock is a 503 and not a request that waits
// for ever.
//
// A failure before the COMMIT, one the server answered, and a COMMIT that never went
// out because its context had already ended roll the transaction back
// (commitOutcomeUnknown), and the answer says the account was NOT changed. A COMMIT
// that went out and whose answer was lost — a reset, an EOF, the bound running out
// while it was awaited — may have landed, and the answer says exactly that, with
// unconfirmed true so that Update can still record it (a user_updated audit row that
// says the outcome is unconfirmed). It does not read the account back to find out,
// as ChangePassword does for a password: a deactivation is idempotent — setting an
// inactive account inactive and ending its sessions again changes nothing — so "may
// have taken effect, repeat it" is a safe and complete answer, where a password
// change that was repeated would be refused for using the old password. A read that
// settled it would be a locking read like GetPasswordHashForSettle, another query,
// and another way to be wrong, for a reset at the one instant that matters.
func (h *UserHandler) deactivate(c fiber.Ctx, profile db.UpdateUserProfileParams) (user db.User, revoked []uuid.UUID, unconfirmed bool, err error) {
	if h.pool == nil {
		return db.User{}, nil, false, fiber.NewError(fiber.StatusInternalServerError, "Failed to update user")
	}

	d := h.dbTimeout
	if d <= 0 {
		d = authDBTimeout
	}
	ctx, cancel := context.WithTimeout(c.Context(), d)
	defer cancel()

	// READ COMMITTED is pinned and not left to the server's default, because the
	// guarantee depends on it: the epoch bump waits for a sign-in that is in the
	// middle of its insert, and the listing and the revoke that follow must each take
	// a snapshot AFTER that sign-in committed to see its session. Under REPEATABLE
	// READ the snapshot is taken by the transaction's first statement, the listing
	// finds nothing and the session stays live (TestRevokeAll_UnderRepeatableRead…
	// shows it against Postgres). Postgres defaults to READ COMMITTED, but a role,
	// a database or a connection setting can change that default.
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return db.User{}, nil, false, deactivationNotDone("start the transaction", profile.ID, err)
	}
	// release gives the connection back: a rollback on a context of its own, which is
	// idempotent and so the backstop for every return below.
	release := releaseTx(tx, "deactivate user")
	defer release()
	qx := h.queries.WithTx(tx)

	user, err = qx.UpdateUserProfile(ctx, profile)
	if err != nil {
		return db.User{}, nil, false, deactivationNotDone("update the account", profile.ID, err)
	}

	revoked, err = auth.RevokeAllUserSessionsIn(ctx, qx, profile.ID)
	if err != nil {
		return db.User{}, nil, false, deactivationNotDone("end the account's sessions", profile.ID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		if !commitOutcomeUnknown(err) {
			// The server answered the COMMIT, or it never went out: the transaction
			// rolled back (see commitOutcomeUnknown).
			return db.User{}, nil, false, deactivationNotDone("commit the transaction", profile.ID, err)
		}
		return db.User{}, nil, true, deactivationCommitUnconfirmed(profile.ID, err)
	}
	return user, revoked, false, nil
}

// deactivationNotDone answers a failure of a deactivation before anything was
// committed — a step of the transaction, up to and including a COMMIT the server
// refused or that never went out: whatever was begun rolls back, nothing changed,
// and the answer says so. The database being away is a 503 and anything else a 500
// (see isTransientDBError); both are logged with the step and the account's id,
// never anything else.
func deactivationNotDone(step string, userID uuid.UUID, err error) error {
	if isTransientDBError(err) {
		slog.Warn("deactivate user: could not "+step+"; the account was not changed",
			"user_id", userID, "error", err)
		return fiber.NewError(fiber.StatusServiceUnavailable, deactivationNotDoneUnavailableMessage)
	}
	slog.Error("deactivate user: failed to "+step+"; the account was not changed",
		"user_id", userID, "error", err)
	return fiber.NewError(fiber.StatusInternalServerError, deactivationNotDoneMessage)
}

// deactivationCommitUnconfirmed answers a COMMIT that went out and whose answer was
// lost: the deactivation and the end of the account's sessions may both have taken
// effect, or neither. The status follows the usual rule (isTransientDBError) and the
// message says it is unconfirmed. Update still records the change, as unconfirmed,
// and purges the RBAC cache — both right whichever way the commit went — but writes
// nothing that presumes it landed.
func deactivationCommitUnconfirmed(userID uuid.UUID, err error) error {
	if isTransientDBError(err) {
		slog.Warn("deactivate user: the commit did not complete; its outcome is UNCONFIRMED — the account may be deactivated and its sessions ended",
			"user_id", userID, "error", err)
		return fiber.NewError(fiber.StatusServiceUnavailable, deactivationUnconfirmedMessage)
	}
	slog.Error("deactivate user: the commit failed; its outcome is UNCONFIRMED — the account may be deactivated and its sessions ended",
		"user_id", userID, "error", err)
	return fiber.NewError(fiber.StatusInternalServerError, deactivationUnconfirmedMessage)
}

// followUpDuration is the deadline of one follow-up: authFollowUpTimeout unless a
// test shortened it.
func (h *UserHandler) followUpDuration() time.Duration {
	return followUpBound(h.followUpTimeout)
}

// followUp returns the context for one follow-up of a decision already made (see
// followUpContext).
func (h *UserHandler) followUp(parent context.Context) (context.Context, context.CancelFunc) {
	return followUpContext(parent, h.followUpDuration())
}

// withFollowUp runs fn — a call that reads c.Context(), such as AuditLog with the
// event it publishes — under a follow-up deadline (see runFollowUp).
func (h *UserHandler) withFollowUp(c fiber.Ctx, fn func()) {
	runFollowUp(c, h.followUpDuration(), fn)
}

// Delete handles DELETE /api/v1/users/:id.
func (h *UserHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if id == auth.SystemUserID {
		return fiber.NewError(fiber.StatusForbidden, "Cannot delete the system account")
	}

	// Prevent self-deletion
	callerID, _ := c.Locals("user_id").(uuid.UUID)
	if callerID == id {
		return fiber.NewError(fiber.StatusForbidden, "Cannot delete your own account")
	}

	user, err := h.queries.GetUserByID(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "User not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get user")
	}

	if err := h.queries.DeleteUser(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete user")
	}

	details, _ := json.Marshal(map[string]string{"email": user.Email})
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "user", id.String(), "user_deleted", details)

	return c.SendStatus(fiber.StatusNoContent)
}
