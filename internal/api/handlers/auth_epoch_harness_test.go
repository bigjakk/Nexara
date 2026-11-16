package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The handler half of "no session from a credential check that a revoke-all has since
// invalidated": the real Login, Register, OIDCTokenExchange, VerifyLogin and UserHandler.Update
// through a real Fiber app, the real SessionManager over miniredis, and epochStore standing in
// for the users and sessions tables. The session insert matches only a user that is active and
// still holds the epoch it is given, so an epoch passed from the wrong place shows up as a
// refusal here (the SQL, and the FOR SHARE a store without row locks cannot model, are pinned
// in internal/db/session_epoch_db_test.go). Tests move the world between two statements of one
// request with the after hooks: a writer landing in the gap between check and insert.

// epochStore is the users table and the sessions table as the handlers see them.
type epochStore struct {
	authFakeDB

	users    map[uuid.UUID]*db.User
	sessions []db.Session // the rows CreateSessionAtEpoch inserted

	// recoveryCodes are a user's unspent recovery codes, and spentRecoveryCodes how many
	// DeleteRecoveryCode removed.
	recoveryCodes      map[uuid.UUID][]db.ListRecoveryCodesRow
	spentRecoveryCodes int

	// after runs, store locked, once a statement of this name has produced its answer: a
	// writer landing right behind a read.
	after map[string]func(*epochStore)

	// oidcConfig is what GetEnabledOIDCConfig answers; nil is no enabled configuration.
	oidcConfig *db.OidcConfig
	// ldapConfig is what GetLDAPConfig answers, for any id, and GetEnabledLDAPConfig
	// when it is enabled; nil is none. syncLeased says a sync holds its lease.
	ldapConfig *db.LdapConfig
	syncLeased bool
	leaseLost  bool // RenewLDAPSyncLease finds the lease no longer the sync's
	// grants are the global "action:resource" permissions GetUserScopedPermissions
	// answers for a user, for a real RBAC engine over this store.
	grants map[uuid.UUID][]string
	// roles, when not nil, is the roles table: AssignUserRole of a role not in it fails
	// as its foreign key does, and roleUsers are the directory configs naming a role as
	// their default (ListDirectoryConfigsUsingRole).
	roles     map[uuid.UUID]db.Role
	roleUsers map[uuid.UUID][]db.ListDirectoryConfigsUsingRoleRow
}

func newEpochStore(users ...db.User) *epochStore {
	s := &epochStore{
		users:         map[uuid.UUID]*db.User{},
		recoveryCodes: map[uuid.UUID][]db.ListRecoveryCodesRow{},
		after:         map[string]func(*epochStore){},
	}
	s.model = s
	s.failOn, s.stallOn, s.stalledFor = map[string]error{}, map[string]bool{}, map[string][]time.Duration{}
	for _, u := range users {
		s.users[u.ID] = &u
	}
	return s
}

func (s *epochStore) user(id uuid.UUID) db.User {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[id]; ok {
		return *u
	}
	return db.User{}
}

// mutate changes a user's row the way another request committing would.
func (s *epochStore) mutate(id uuid.UUID, fn func(*db.User)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u, ok := s.users[id]; ok {
		fn(u)
	}
}

func (s *epochStore) bump(id uuid.UUID) { s.mutate(id, func(u *db.User) { u.AuthEpoch++ }) }

func (s *epochStore) sessionsOf(id uuid.UUID) []db.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []db.Session
	for _, sess := range s.sessions {
		if sess.UserID == id {
			out = append(out, sess)
		}
	}
	return out
}

func (s *epochStore) liveSessionsOf(id uuid.UUID) []db.Session {
	var out []db.Session
	for _, sess := range s.sessionsOf(id) {
		if !sess.IsRevoked {
			out = append(out, sess)
		}
	}
	return out
}

func (s *epochStore) recoveryCodesOf(id uuid.UUID) []db.ListRecoveryCodesRow {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]db.ListRecoveryCodesRow(nil), s.recoveryCodes[id]...)
}

func (s *epochStore) spentRecoveryCodeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.spentRecoveryCodes
}

// requireStallCutShort fails unless a stalled statement of this name was reached and cut
// at the follow-up bound a test sets, a tenth of a second, and not at the production
// bound of five seconds (a follow-up the test could not shorten, or never bounded).
func requireStallCutShort(t *testing.T, store *epochStore, name string) {
	t.Helper()
	waits := store.stallWaits(name)
	if len(waits) == 0 {
		t.Errorf("no %s statement stalled: the premise of the test is gone", name)
		return
	}
	for _, w := range waits {
		if w > 2*time.Second {
			t.Errorf("a stalled %s waited %v with a 100 ms follow-up bound: the follow-up held the answer", name, w)
		}
	}
}

// byEmail is the account GetUserByEmail matches, as its SQL: the exact spelling, else
// the one account differing only by case. Called with the store locked.
func (s *epochStore) byEmail(email string) *db.User {
	var folded []*db.User
	for _, u := range s.users {
		if u.Email == email {
			return u
		}
		if strings.EqualFold(u.Email, email) {
			folded = append(folded, u)
		}
	}
	if len(folded) == 1 {
		return folded[0]
	}
	return nil
}

// byIdentity is the account GetUserByOIDCIdentity matches: an SSO account bound to this
// issuer and subject. Called with the store locked.
func (s *epochStore) byIdentity(issuer, subject string) *db.User {
	for _, u := range s.users {
		if subject != "" && u.AuthSource == "oidc" && u.OidcIssuer == issuer && u.OidcSubject == subject {
			return u
		}
	}
	return nil
}

func (s *epochStore) afterHook(name string) {
	if fn := s.after[name]; fn != nil {
		fn(s)
	}
}

func (s *epochStore) doExec(name string, args []any, remember func(func())) (pgconn.CommandTag, error) {
	defer s.afterHook(name)
	switch name {
	case "SELECT": // pg_advisory_xact_lock
		return pgconn.NewCommandTag("SELECT 1"), nil
	case "BumpUserAuthEpoch":
		u, ok := s.users[args[0].(uuid.UUID)]
		if !ok {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		was := u.AuthEpoch
		remember(func() { u.AuthEpoch = was })
		u.AuthEpoch++
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "RevokeAllUserSessions":
		id := args[0].(uuid.UUID)
		for i := range s.sessions {
			if s.sessions[i].UserID == id && !s.sessions[i].IsRevoked {
				remember(func() { s.sessions[i].IsRevoked = false })
				s.sessions[i].IsRevoked = true
			}
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "RevokeAllUserRoles":
		return pgconn.NewCommandTag("DELETE 0"), nil
	case "DeactivateLDAPUserForDirectory", "ReactivateLDAPUserFromDirectory":
		// id; the SQL's WHERE: an LDAP account, active to deactivate, and inactive by a
		// sync to reactivate.
		u, ok := s.users[args[0].(uuid.UUID)]
		deactivate := name == "DeactivateLDAPUserForDirectory"
		if !ok || u.AuthSource != "ldap" || u.IsActive != deactivate || (!deactivate && !u.DeactivatedByDirectory) {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		before := *u
		remember(func() { *u = before })
		u.IsActive, u.DeactivatedByDirectory = !deactivate, deactivate
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "SetLDAPLoginName":
		// login_name, id: an LDAP account, as the SQL's WHERE.
		if u, ok := s.users[args[1].(uuid.UUID)]; ok && u.AuthSource == "ldap" {
			u.LdapLoginName = args[0].(string)
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "RenewLDAPSyncLease":
		// The holder's renewal; 0 rows once leaseLost says another took the lease.
		if s.leaseLost {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "ReleaseLDAPSyncLease":
		s.syncLeased = false
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "UpdateLDAPConfigLastSync":
		if s.ldapConfig != nil {
			s.ldapConfig.LastSyncAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "ClearOIDCIdentity":
		// id; the SQL's WHERE: a linked SSO account.
		u, ok := s.users[args[0].(uuid.UUID)]
		if !ok || u.AuthSource != "oidc" || u.OidcSubject == "" {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		before := *u
		remember(func() { *u = before })
		u.OidcIssuer, u.OidcSubject = "", ""
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "DeleteRecoveryCode":
		id := args[0].(uuid.UUID)
		for uid, codes := range s.recoveryCodes {
			for i, rc := range codes {
				if rc.ID == id {
					s.recoveryCodes[uid] = append(append([]db.ListRecoveryCodesRow(nil), codes[:i]...), codes[i+1:]...)
					s.spentRecoveryCodes++
					return pgconn.NewCommandTag("DELETE 1"), nil
				}
			}
		}
		return pgconn.NewCommandTag("DELETE 0"), nil
	case "RevokeSession":
		id := args[0].(uuid.UUID)
		for i := range s.sessions {
			if s.sessions[i].ID == id && !s.sessions[i].IsRevoked {
				remember(func() { s.sessions[i].IsRevoked = false })
				s.sessions[i].IsRevoked = true
			}
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case "InsertAuditLog":
		return s.insertAudit(args)
	case "DeleteRole":
		delete(s.roles, args[0].(uuid.UUID))
		return pgconn.NewCommandTag("DELETE 1"), nil
	default:
		return pgconn.CommandTag{}, fmt.Errorf("unexpected Exec: %s", name)
	}
}

func (s *epochStore) doQueryRow(name string, args []any, remember func(func())) pgx.Row {
	defer s.afterHook(name)
	switch name {
	case "GetUserByEmail":
		if u := s.byEmail(args[0].(string)); u != nil {
			return authFakeRow{value: *u}
		}
		return authFakeRow{err: pgx.ErrNoRows}
	case "EmailTakenIgnoringCase":
		for _, u := range s.users {
			if strings.EqualFold(u.Email, args[0].(string)) {
				return authFakeScalar{value: true}
			}
		}
		return authFakeScalar{value: false}
	case "GetUserByID":
		if u, ok := s.users[args[0].(uuid.UUID)]; ok {
			return authFakeRow{value: *u}
		}
		return authFakeRow{err: pgx.ErrNoRows}
	case "GetUserTOTPSecret":
		if u, ok := s.users[args[0].(uuid.UUID)]; ok {
			return authFakeRow{value: db.GetUserTOTPSecretRow{ID: u.ID, TotpSecret: u.TotpSecret}}
		}
		return authFakeRow{err: pgx.ErrNoRows}
	case "CountUsersLinkedToIssuer":
		var n int64
		for _, u := range s.users {
			if u.OidcSubject != "" && u.OidcIssuer == args[0].(string) {
				n++
			}
		}
		return authFakeScalar{value: n}
	case "CountUsers":
		return authFakeScalar{value: int64(len(s.users))}
	case "CreateUser":
		// email, password_hash, display_name, is_active, totp_secret, role
		u := &db.User{
			ID: uuid.New(), Email: args[0].(string), PasswordHash: args[1].(string), DisplayName: args[2].(string),
			IsActive: args[3].(bool), TotpSecret: args[4].(pgtype.Text), Role: args[5].(string), AuthSource: "local",
		}
		s.users[u.ID] = u
		remember(func() { delete(s.users, u.ID) })
		return authFakeRow{value: *u}
	case "AssignUserRole":
		if _, ok := s.roles[args[1].(uuid.UUID)]; s.roles != nil && !ok {
			return authFakeRow{err: &pgconn.PgError{Code: "23503", ConstraintName: "user_roles_role_id_fkey"}}
		}
		return authFakeRow{value: db.UserRole{}}
	case "GetRole":
		if r, ok := s.roles[args[0].(uuid.UUID)]; ok {
			return authFakeRow{value: r}
		}
		return authFakeRow{err: pgx.ErrNoRows}
	case "GetUserByEmailAndSource":
		if u := s.byEmail(args[0].(string)); u != nil && u.AuthSource == args[1].(string) {
			return authFakeRow{value: *u}
		}
		return authFakeRow{err: pgx.ErrNoRows}
	case "CreateLDAPUser", "CreateOIDCUser":
		// email, display_name: active, role 'user', the source of the query.
		source := "ldap"
		if name == "CreateOIDCUser" {
			source = "oidc"
		}
		u := &db.User{
			ID: uuid.New(), Email: args[0].(string), DisplayName: args[1].(string),
			IsActive: true, Role: "user", AuthSource: source,
		}
		if name == "CreateOIDCUser" {
			// oidc_issuer, oidc_subject; users_oidc_identity_key refuses a second holder.
			u.OidcIssuer, u.OidcSubject = args[2].(string), args[3].(string)
			if s.byIdentity(u.OidcIssuer, u.OidcSubject) != nil {
				return authFakeRow{err: &pgconn.PgError{Code: "23505", ConstraintName: "users_oidc_identity_key",
					Message: `duplicate key value violates unique constraint "users_oidc_identity_key"`}}
			}
		}
		s.users[u.ID] = u
		remember(func() { delete(s.users, u.ID) })
		return authFakeRow{value: *u}
	case "GetUserByOIDCIdentity":
		// oidc_issuer, oidc_subject.
		if u := s.byIdentity(args[0].(string), args[1].(string)); u != nil {
			return authFakeRow{value: *u}
		}
		return authFakeRow{err: pgx.ErrNoRows}
	case "LinkOIDCIdentity":
		// oidc_issuer, oidc_subject, id; the SQL's WHERE: an SSO account with no link
		// stored, and users_oidc_identity_key.
		iss, sub := args[0].(string), args[1].(string)
		u, ok := s.users[args[2].(uuid.UUID)]
		if !ok || u.AuthSource != "oidc" || u.OidcSubject != "" || u.OidcIssuer != "" || iss == "" || sub == "" {
			return authFakeRow{err: pgx.ErrNoRows}
		}
		if s.byIdentity(iss, sub) != nil {
			return authFakeRow{err: &pgconn.PgError{Code: "23505", ConstraintName: "users_oidc_identity_key",
				Message: `duplicate key value violates unique constraint "users_oidc_identity_key"`}}
		}
		before := *u
		remember(func() { *u = before })
		u.OidcIssuer, u.OidcSubject = iss, sub
		return authFakeRow{value: *u}
	case "UpdateLDAPUserProfile", "UpdateOIDCUserProfile":
		// id, display_name: only for an account of the query's own source, as the SQL's WHERE.
		source := "ldap"
		if name == "UpdateOIDCUserProfile" {
			source = "oidc"
		}
		u, ok := s.users[args[0].(uuid.UUID)]
		if !ok || u.AuthSource != source {
			return authFakeRow{err: pgx.ErrNoRows}
		}
		before := *u
		remember(func() { *u = before })
		u.DisplayName = args[1].(string)
		return authFakeRow{value: *u}
	case "UpdateUserProfile":
		// display_name, is_active, role, id: a PARTIAL update, as the SQL's COALESCE, so a
		// field the caller left NULL keeps the value the row holds now.
		u, ok := s.users[args[3].(uuid.UUID)]
		if !ok {
			return authFakeRow{err: pgx.ErrNoRows}
		}
		before := *u
		remember(func() { *u = before })
		if v := args[0].(pgtype.Text); v.Valid {
			u.DisplayName = v.String
		}
		if v := args[1].(pgtype.Bool); v.Valid {
			u.IsActive, u.DeactivatedByDirectory = v.Bool, false
		}
		if v := args[2].(pgtype.Text); v.Valid {
			u.Role = v.String
		}
		return authFakeRow{value: *u}
	case "CreateSessionAtEpoch":
		// token_hash, user_agent, ip_address, expires_at, device_name, device_type,
		// device_id, user_role, user_id, epoch; and the SQL's WHERE: the user is active
		// and still holds the epoch the check read.
		u, ok := s.users[args[8].(uuid.UUID)]
		if !ok || !u.IsActive || u.AuthEpoch != args[9].(int64) {
			return authFakeRow{err: pgx.ErrNoRows}
		}
		sess := db.Session{
			ID: uuid.New(), UserID: u.ID, TokenHash: args[0].(string), UserAgent: args[1].(string),
			IpAddress: args[2].(string), ExpiresAt: args[3].(time.Time), DeviceName: args[4].(pgtype.Text),
			DeviceType: args[5].(pgtype.Text), DeviceID: args[6].(pgtype.Text), UserRole: args[7].(string),
			CreatedAt: time.Now(), LastUsedAt: time.Now(),
		}
		s.sessions = append(s.sessions, sess)
		return authFakeRow{value: sess}
	case "GetLDAPConfig":
		if s.ldapConfig == nil {
			return authFakeRow{err: pgx.ErrNoRows}
		}
		return authFakeRow{value: *s.ldapConfig}
	case "GetEnabledLDAPConfig":
		if s.ldapConfig == nil || !s.ldapConfig.Enabled {
			return authFakeRow{err: pgx.ErrNoRows}
		}
		return authFakeRow{value: *s.ldapConfig}
	case "ClaimLDAPSyncLease":
		// No row while another sync holds the lease, as the SQL's WHERE.
		if s.ldapConfig == nil || s.syncLeased {
			return authFakeRow{err: pgx.ErrNoRows}
		}
		s.syncLeased = true
		return authFakeRow{value: *s.ldapConfig}
	case "GetEnabledOIDCConfig", "GetOIDCConfig":
		if s.oidcConfig == nil {
			return authFakeRow{err: pgx.ErrNoRows}
		}
		return authFakeRow{value: *s.oidcConfig}
	case "CreateOIDCConfig", "UpdateOIDCConfig":
		// The one configuration the store holds, as written: Update's arguments are
		// Create's after the id.
		cfg := db.OidcConfig{ID: uuid.New()}
		if name == "UpdateOIDCConfig" {
			cfg.ID, args = args[0].(uuid.UUID), args[1:]
		}
		cfg.Name, cfg.Enabled, cfg.IssuerUrl, cfg.ClientID = args[0].(string), args[1].(bool), args[2].(string), args[3].(string)
		cfg.ClientSecretEncrypted, cfg.RedirectUri = args[4].(string), args[5].(string)
		cfg.AllowPrivateAddress = args[14].(bool)
		was := s.oidcConfig
		remember(func() { s.oidcConfig = was })
		s.oidcConfig = &cfg
		return authFakeRow{value: cfg}
	case "GetSessionByTokenHash":
		// token_hash: the SQL's predicate, the session's current hash and not revoked.
		for _, sess := range s.sessions {
			if sess.TokenHash == args[0].(string) && !sess.IsRevoked {
				return authFakeRow{value: sess}
			}
		}
		return authFakeRow{err: pgx.ErrNoRows}
	case "GetNodeByClusterAndName":
		// cluster_id, name: every node is a member; no test here is about membership.
		return authFakeRow{value: db.Node{ID: uuid.New(), ClusterID: args[0].(uuid.UUID), Name: args[1].(string)}}
	case "IsConsoleCredentialLive":
		// user_id, session_id, api_key_id; and the SQL's predicate over the sessions
		// held here (the store holds no API keys).
		u, ok := s.users[args[0].(uuid.UUID)]
		sid := args[1].(pgtype.UUID)
		live := false
		for _, sess := range s.sessions {
			if ok && u.IsActive && sid.Valid && sess.ID == uuid.UUID(sid.Bytes) && sess.UserID == u.ID &&
				!sess.IsRevoked && sess.ExpiresAt.After(time.Now()) {
				live = true
			}
		}
		return authFakeScalar{value: live}
	default:
		return authFakeRow{err: fmt.Errorf("unexpected QueryRow: %s", name)}
	}
}

func (s *epochStore) doQuery(name string, args []any) (pgx.Rows, error) {
	defer s.afterHook(name)
	switch name {
	case "ListRecoveryCodes":
		codes := s.recoveryCodes[args[0].(uuid.UUID)]
		items := make([]any, 0, len(codes))
		for _, rc := range codes {
			items = append(items, rc)
		}
		return &authFakeRows{items: items}, nil
	case "ListLDAPUsers":
		var items []any
		for _, u := range s.users {
			if u.AuthSource == "ldap" {
				items = append(items, db.ListLDAPUsersRow{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName,
					Role: u.Role, IsActive: u.IsActive, AuthSource: u.AuthSource, DeactivatedByDirectory: u.DeactivatedByDirectory, LdapLoginName: u.LdapLoginName})
			}
		}
		return &authFakeRows{items: items}, nil
	case "ListDirectoryConfigsUsingRole":
		var items []any
		for _, r := range s.roleUsers[args[0].(uuid.UUID)] {
			items = append(items, r)
		}
		return &authFakeRows{items: items}, nil
	case "ClearAllOIDCIdentities":
		// Every linked SSO account, as the SQL's WHERE; its ids, as RETURNING id. A Query
		// has no undo here: the tests that roll back do so before it.
		var items []any
		for _, u := range s.users {
			if u.AuthSource == "oidc" && u.OidcSubject != "" {
				u.OidcIssuer, u.OidcSubject = "", ""
				items = append(items, struct{ ID uuid.UUID }{u.ID})
			}
		}
		return &authFakeRows{items: items}, nil
	case "ListUsersWithRoles":
		var items []any
		for _, u := range s.users {
			items = append(items, db.ListUsersWithRolesRow{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName,
				Role: u.Role, IsActive: u.IsActive, AuthSource: u.AuthSource, SsoLinked: u.OidcSubject != ""})
		}
		return &authFakeRows{items: items}, nil
	case "ListUserIDsByRole":
		return &authFakeRows{}, nil
	case "GetUserScopedPermissions":
		var items []any
		for _, g := range s.grants[args[0].(uuid.UUID)] {
			action, resource, _ := strings.Cut(g, ":")
			items = append(items, db.GetUserScopedPermissionsRow{Action: action, Resource: resource, ScopeType: "global"})
		}
		return &authFakeRows{items: items}, nil
	case "ListUserSessions":
		id := args[0].(uuid.UUID)
		var items []any
		for _, sess := range s.sessions {
			if sess.UserID == id && !sess.IsRevoked && sess.ExpiresAt.After(time.Now()) {
				items = append(items, sess)
			}
		}
		return &authFakeRows{items: items}, nil
	default:
		return nil, fmt.Errorf("unexpected Query: %s", name)
	}
}

// epochOptions are the knobs a few tests turn.
type epochOptions struct {
	dbTimeout       time.Duration // the handlers' bound on deciding database work; 0 is the production bound
	followUpTimeout time.Duration // and on each follow-up; 0 is the production bound
	totp            bool          // wire the TOTP handler into the auth handler
	noSessions      bool          // a UserHandler with no SessionManager
}

type epochApp struct {
	app   *fiber.App
	store *epochStore
	pool  *authFakePool
	redis *miniredis.Miniredis
	rdb   *redis.Client
	jwt   *auth.JWTService

	auth  *AuthHandler
	totp  *TOTPHandler
	oidc  *OIDCHandler
	users *UserHandler

	// probe is what GET /probe runs, for the tests that drive a function no route reaches.
	probe fiber.Handler
}

// The accounts the harness holds, and the provider identity its SSO sign-ins present.
const (
	epochLoginEmail = "alice@example.com"
	epochAdminEmail = "bob@example.com"
	epochIssuer     = "https://sso.example.com"
	epochSubject    = "subject-of-alice"
)

// epochUser is a local, active account whose password is racePassword.
func epochUser(t *testing.T, email string, epoch int64) db.User {
	t.Helper()
	return db.User{
		ID:           uuid.New(),
		Email:        email,
		PasswordHash: racePasswordHash(t),
		DisplayName:  "Example User",
		IsActive:     true,
		Role:         "user",
		AuthSource:   "local",
		AuthEpoch:    epoch,
	}
}

// authErrorHandler turns a fiber error into the envelope the API answers with.
func authErrorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	message := "Internal Server Error"
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
		message = e.Message
	}
	return c.Status(code).JSON(fiber.Map{"error": code, "message": message})
}

func newEpochApp(t *testing.T, store *epochStore, opts epochOptions) *epochApp {
	t.Helper()

	mr := miniredis.RunT(t)
	// No retries and a short dial, so a test that closes Redis fails fast instead of
	// spending the client's defaults, a second and a half, per command.
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: 300 * time.Millisecond})
	t.Cleanup(func() { _ = rdb.Close() })

	queries := db.New(authFakeConn{store: &store.authFakeDB})
	pool := &authFakePool{store: &store.authFakeDB}
	jwtSvc := auth.NewJWTService("test-secret", 15*time.Minute, 7*24*time.Hour)
	sessions := auth.NewSessionManager(queries, rdb)

	ah := &AuthHandler{
		pool:            pool,
		queries:         queries,
		jwtService:      jwtSvc,
		sessionManager:  sessions,
		dbTimeout:       opts.dbTimeout,
		followUpTimeout: opts.followUpTimeout,
	}
	a := &epochApp{store: store, pool: pool, redis: mr, rdb: rdb, jwt: jwtSvc, auth: ah,
		probe: func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusNoContent) }}

	// The SSO handler is the part of the OIDC flow after the identity provider: the
	// exchange and the storage of its code. Callback itself needs a provider.
	a.oidc = &OIDCHandler{queries: queries, rdb: rdb}
	ah.SetOIDCHandler(a.oidc)

	if opts.totp {
		a.totp = &TOTPHandler{queries: queries, totpService: auth.NewTOTPService(totpTestKey), rdb: rdb,
			followUpTimeout: opts.followUpTimeout}
		ah.SetTOTPHandler(a.totp)
		a.totp.SetIssueTokensFn(ah.IssueTokens)
		a.totp.SetCurrentPasswordCheck(ah.ProveCurrentPassword)
		a.totp.SetDirectoryPasswordCheck(ah.ProveDirectoryPassword)
	}

	uh := &UserHandler{
		pool:            pool,
		queries:         queries,
		rbac:            auth.NewRBACEngine(queries, rdb),
		dbTimeout:       opts.dbTimeout,
		followUpTimeout: opts.followUpTimeout,
	}
	if !opts.noSessions {
		uh.sessions = sessions
	}
	a.users = uh

	app := fiber.New(fiber.Config{ErrorHandler: authErrorHandler})
	// The caller of an admin route, as the auth middleware would have set it up: the header
	// names the acting account, and an admin role is what the stub engine grants every
	// permission to. X-Test-Acting-Role gives that caller another role.
	app.Use(func(c fiber.Ctx) error {
		if v := c.Get("X-Test-Acting-User"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "bad X-Test-Acting-User")
			}
			role := c.Get("X-Test-Acting-Role")
			if role == "" {
				role = "admin"
			}
			c.Locals("user_id", id)
			c.Locals("role", role)
		}
		// How the caller authenticated, as authRequired and authenticateAPIKey record it:
		// X-Test-Session names the session its access token belongs to ("none" for a token
		// that names no session), X-Test-API-Key the key.
		if v := c.Get("X-Test-Session"); v != "" {
			c.Locals(LocalsAuthMethod, AuthMethodSession)
			if id, err := uuid.Parse(v); err == nil {
				c.Locals(LocalsSessionID, id)
			}
		}
		if v := c.Get("X-Test-API-Key"); v != "" {
			id, err := uuid.Parse(v)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "bad X-Test-API-Key")
			}
			c.Locals(LocalsAuthMethod, AuthMethodAPIKey)
			c.Locals(LocalsAPIKeyID, id)
		}
		return c.Next()
	})
	installStubEngineMiddleware(app)

	app.Post("/auth/register", ah.Register)
	app.Post("/auth/login", withRequestParams(t, authLoginMirror(), nil, ah.Login))
	app.Post("/auth/oidc/token-exchange", withRequestParams(t, epochOIDCExchangeMirror(), nil, ah.OIDCTokenExchange))
	app.Get("/auth/oidc/callback", a.oidc.Callback)
	if a.totp != nil {
		app.Post("/auth/totp/verify-login", withRequestParams(t, totpVerifyLoginMirror(), nil, a.totp.VerifyLogin))
		app.Post("/auth/totp/setup", withRequestParams(t, apischema.Properties{
			"current_password": {Type: apischema.String, Optional: true},
		}, nil, a.totp.BeginSetup))
		app.Delete("/auth/totp", withRequestParams(t, totpDisableMirror(), nil, a.totp.Disable))
		app.Post("/auth/totp/recovery-codes/regenerate", withRequestParams(t, totpConfirmSetupMirror(), nil, a.totp.RegenerateRecoveryCodes))
	}
	app.Put("/users/:id", withRequestParams(t, epochUserUpdateMirror(), []string{"id"}, uh.Update))
	app.Get("/users", withRequestParams(t, apischema.Properties{}, nil, uh.List))
	app.Delete("/oidc/links", withRequestParams(t, apischema.Properties{}, nil, uh.ResetAllSSOLinks))
	app.Delete("/users/:id/sso-link", withRequestParams(t, apischema.Properties{
		"id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath}}, []string{"id"}, uh.ResetSSOLink))
	app.Post("/auth/console-token", withRequestParams(t, consoleTokenMirror(), nil, ah.ConsoleToken))
	app.Post("/auth/ws-token", withRequestParams(t, apischema.Properties{}, nil, ah.WSToken))
	app.Get("/probe", func(c fiber.Ctx) error { return a.probe(c) })

	a.app = app
	return a
}

// The mirrors are local copies of the parts of these routes' declarations
// (internal/api/registry_auth.go, registry_users.go) that the handlers read, for the
// reason migrationListMirror gives.
func epochOIDCExchangeMirror() apischema.Properties {
	return apischema.Properties{
		"code": {Type: apischema.String, MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(256)},
	}
}

func epochUserUpdateMirror() apischema.Properties {
	return apischema.Properties{
		"id":           {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
		"display_name": {Type: apischema.String, Optional: true},
		"is_active":    {Type: apischema.Boolean, Optional: true},
		"role":         {Type: apischema.String, Optional: true},
	}
}

// requireTokenNamesSession fails unless the response's access token names session: a
// console it opens is held to that session (ConsoleToken).
func requireTokenNamesSession(t *testing.T, a *epochApp, body map[string]any, session uuid.UUID) {
	t.Helper()
	tok, _ := body["access_token"].(string)
	claims, err := a.jwt.ValidateAccessToken(tok)
	if err != nil {
		t.Fatalf("the access token does not validate: %v", err)
	}
	if claims.SessionID != session {
		t.Errorf("the access token names session %v, want the one created, %v", claims.SessionID, session)
	}
}

// send sends a request and fails the test on a hang rather than waiting for one.
func (a *epochApp) send(t *testing.T, method, path, body string, headers map[string]string, timeout time.Duration) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.app.Test(req, fiber.TestConfig{Timeout: timeout, FailOnTimeout: true})
	if err != nil {
		t.Fatalf("%s %s did not answer (a hang, if that is the timeout): %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (a *epochApp) post(t *testing.T, path, body string) *http.Response {
	t.Helper()
	return a.send(t, http.MethodPost, path, body, nil, 20*time.Second)
}

// epochBindingSecret is the browser binding's secret of the exchange codes the tests
// seed (epochSeedExchange); the code's data holds its digest.
const epochBindingSecret = "binding-secret-of-the-browser"

// exchange trades an SSO exchange code as the browser the callback handed it to
// does: the code in the body, and its binding's secret in a cookie (oidcExchangeBinding).
func (a *epochApp) exchange(t *testing.T, code string) *http.Response {
	t.Helper()
	return a.send(t, http.MethodPost, "/auth/oidc/token-exchange", `{"code":"`+code+`"}`,
		map[string]string{"Cookie": oidcExchangeBinding.cookieName(false, code) + "=" + epochBindingSecret}, 20*time.Second)
}

// epochLoginBody is the body of a password login.
func epochLoginBody(email string) string {
	return `{"email":"` + email + `","password":"` + racePassword + `"}`
}

// epochRequireNothingIssued fails unless the response carries no session at all: no access
// token, no refresh cookie of any kind. It is what every refusal and failure shares.
func epochRequireNothingIssued(t *testing.T, resp *http.Response, body map[string]any) {
	t.Helper()
	if tok, ok := body["access_token"]; ok && tok != "" {
		t.Errorf("an access token was issued for a session that was not created: %v", body)
	}
	if cookies := refreshCookies(resp); len(cookies) != 0 {
		t.Errorf("a refresh cookie was set for a session that was not created: %+v", cookies)
	}
	if resp.Header.Get("Authorization") != "" {
		t.Errorf("an Authorization header was set: %q", resp.Header.Get("Authorization"))
	}
}

// epochRequireNoLoginAudit fails if any authentication event was audited: a refused
// sign-in is not a login.
func epochRequireNoLoginAudit(t *testing.T, store *epochStore) {
	t.Helper()
	for _, action := range store.auditActions() {
		switch action {
		case "login", "ldap_login", "oidc_login", "register":
			t.Errorf("a %q audit row was written for a session that was not created", action)
		}
	}
}
