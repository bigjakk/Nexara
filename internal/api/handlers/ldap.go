package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/audit"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// All 7 routes are declared in internal/api/registry_ldap.go, which states
// their permission — a global manage:user Check on every one — and their
// parameters; nothing below re-checks either.
//
// What stays here is what the declaration cannot see: the insecure-transport
// confirmation, which compares the STORED config against the requested one; the
// credential-redirect refusal; and the URL and filter validators, which own
// their rules for every caller rather than for one route.

// LDAPHandler handles LDAP configuration endpoints.
type LDAPHandler struct {
	queries       *db.Queries
	encryptionKey string
	rbac          *auth.RBACEngine
	eventPub      *events.Publisher
	// rdb bounds how often an unchanged audit row is written again (claimAddressAudit);
	// nil writes it every time. Set by SetRedis.
	rdb *redis.Client

	// directory, when set, replaces auth.NewLDAPClient as Sync's directory; only
	// tests set it, to answer a search without an LDAP server.
	directory func(auth.LDAPConfig) ldapDirectory
	// signIn, when set, replaces the directory a sign-in opens (openSignIn); tests only.
	signIn func(auth.LDAPConfig) (ldapSignInSession, error)

	// now, when set, is RunScheduledSync's clock (tests); lastScheduled is when it last
	// started a sync of each config, guarded by scheduleMu.
	now           func() time.Time
	scheduleMu    sync.Mutex
	lastScheduled map[uuid.UUID]time.Time
}

// ldapDirectory is the part of *auth.LDAPClient that Sync asks.
type ldapDirectory interface {
	SearchUser(username string) (*auth.LDAPUser, error)
	SearchUserByEntryID(id string) (*auth.LDAPUser, error)
}

// ldapSignInSession is the part of *auth.LDAPSession a sign-in asks: the search for
// the entry, then the bind as it.
type ldapSignInSession interface {
	Search(username string) (*auth.LDAPUser, error)
	Authenticate(user *auth.LDAPUser, password string) error
	Close()
}

// openSignIn opens the directory for a sign-in: signIn when a test set it, else a
// connection bound as the service account.
func (h *LDAPHandler) openSignIn(cfg auth.LDAPConfig) (ldapSignInSession, error) {
	if h.signIn != nil {
		return h.signIn(cfg)
	}
	s, err := auth.NewLDAPClient(cfg).Open()
	if err != nil {
		return nil, err
	}
	return s, nil
}

// NewLDAPHandler creates a new LDAP handler.
func NewLDAPHandler(queries *db.Queries, encryptionKey string, rbac *auth.RBACEngine, eventPub *events.Publisher) *LDAPHandler {
	return &LDAPHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		rbac:          rbac,
		eventPub:      eventPub,
	}
}

// SetRedis gives the handler the Redis client its audit-row claims use (server setup).
func (h *LDAPHandler) SetRedis(rdb *redis.Client) { h.rdb = rdb }

// ldapConfigRequest is the validated body of both LDAP writes, read once out of
// the registry's parameters so the two handlers cannot drift on how they read
// it. The shape is unchanged from the struct Bind().Body used to fill; what
// moved out is the parsing.
//
// BindPassword is write-only and must stay out of every audit row: view:audit
// is a default Viewer grant, and both handlers build their details field by
// field for exactly that reason.
type ldapConfigRequest struct {
	Name                 string
	Enabled              bool
	ServerURL            string
	StartTLS             bool
	SkipTLSVerify        bool
	BindDN               string
	BindPassword         string
	SearchBaseDN         string
	UserFilter           string
	UsernameAttribute    string
	EmailAttribute       string
	DisplayNameAttribute string
	GroupSearchBaseDN    string
	GroupFilter          string
	GroupAttribute       string
	GroupRoleMapping     map[string]string
	DefaultRoleID        string
	SyncIntervalMinutes  int32
	// AcknowledgeInsecureTLS is required to store a config that carries
	// passwords over a connection that is unencrypted, or encrypted but
	// unverified.
	AcknowledgeInsecureTLS bool
}

// ldapConfigFromParams reads the declared body.
//
// Every key is a string literal at this call site on purpose:
// registry_paramkey_guard_test.go checks each one against the route's schema,
// and a name that only appeared inside a helper would be a name it cannot see.
func ldapConfigFromParams(p *apischema.Params) (ldapConfigRequest, error) {
	mapping, err := StringMapFromObject(p.Object("group_role_mapping"), "group_role_mapping")
	if err != nil {
		return ldapConfigRequest{}, err
	}
	return ldapConfigRequest{
		Name:                 p.String("name"),
		Enabled:              p.Bool("enabled"),
		ServerURL:            p.String("server_url"),
		StartTLS:             p.Bool("start_tls"),
		SkipTLSVerify:        p.Bool("skip_tls_verify"),
		BindDN:               p.String("bind_dn"),
		BindPassword:         p.String("bind_password"),
		SearchBaseDN:         p.String("search_base_dn"),
		UserFilter:           p.String("user_filter"),
		UsernameAttribute:    p.String("username_attribute"),
		EmailAttribute:       p.String("email_attribute"),
		DisplayNameAttribute: p.String("display_name_attribute"),
		GroupSearchBaseDN:    p.String("group_search_base_dn"),
		GroupFilter:          p.String("group_filter"),
		GroupAttribute:       p.String("group_attribute"),
		GroupRoleMapping:     mapping,
		DefaultRoleID:        p.String("default_role_id"),
		// The schema bounds this at math.MaxInt32, so the clamp can never
		// fire; see sync_interval_minutes in internal/api/registry_ldap.go.
		// It is here because the DECLARATION is what makes that true, and
		// nothing in this file can see the declaration — the same reason
		// gosec cannot.
		SyncIntervalMinutes:    safeconv.Int32(int(p.Int("sync_interval_minutes"))),
		AcknowledgeInsecureTLS: p.Bool("acknowledge_insecure_tls"),
	}, nil
}

type ldapConfigResponse struct {
	ID                   uuid.UUID         `json:"id"`
	Name                 string            `json:"name"`
	Enabled              bool              `json:"enabled"`
	ServerURL            string            `json:"server_url"`
	StartTLS             bool              `json:"start_tls"`
	SkipTLSVerify        bool              `json:"skip_tls_verify"`
	BindDN               string            `json:"bind_dn"`
	BindPasswordSet      bool              `json:"bind_password_set"`
	SearchBaseDN         string            `json:"search_base_dn"`
	UserFilter           string            `json:"user_filter"`
	UsernameAttribute    string            `json:"username_attribute"`
	EmailAttribute       string            `json:"email_attribute"`
	DisplayNameAttribute string            `json:"display_name_attribute"`
	GroupSearchBaseDN    string            `json:"group_search_base_dn"`
	GroupFilter          string            `json:"group_filter"`
	GroupAttribute       string            `json:"group_attribute"`
	GroupRoleMapping     map[string]string `json:"group_role_mapping"`
	DefaultRoleID        *string           `json:"default_role_id"`
	SyncIntervalMinutes  int32             `json:"sync_interval_minutes"`
	LastSyncAt           *string           `json:"last_sync_at"`
	CreatedAt            string            `json:"created_at"`
	UpdatedAt            string            `json:"updated_at"`
}

func toLDAPConfigResponse(cfg db.LdapConfig) ldapConfigResponse {
	resp := ldapConfigResponse{
		ID:                   cfg.ID,
		Name:                 cfg.Name,
		Enabled:              cfg.Enabled,
		ServerURL:            cfg.ServerUrl,
		StartTLS:             cfg.StartTls,
		SkipTLSVerify:        cfg.SkipTlsVerify,
		BindDN:               cfg.BindDn,
		BindPasswordSet:      cfg.BindPasswordEncrypted != "",
		SearchBaseDN:         cfg.SearchBaseDn,
		UserFilter:           cfg.UserFilter,
		UsernameAttribute:    cfg.UsernameAttribute,
		EmailAttribute:       cfg.EmailAttribute,
		DisplayNameAttribute: cfg.DisplayNameAttribute,
		GroupSearchBaseDN:    cfg.GroupSearchBaseDn,
		GroupFilter:          cfg.GroupFilter,
		GroupAttribute:       cfg.GroupAttribute,
		SyncIntervalMinutes:  cfg.SyncIntervalMinutes,
		CreatedAt:            cfg.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:            cfg.UpdatedAt.Format(time.RFC3339Nano),
	}

	// Parse group_role_mapping from JSONB
	mapping := make(map[string]string)
	if len(cfg.GroupRoleMapping) > 0 {
		_ = json.Unmarshal(cfg.GroupRoleMapping, &mapping)
	}
	resp.GroupRoleMapping = mapping

	if cfg.DefaultRoleID.Valid {
		id, _ := uuid.FromBytes(cfg.DefaultRoleID.Bytes[:])
		s := id.String()
		resp.DefaultRoleID = &s
	}

	if cfg.LastSyncAt.Valid {
		s := cfg.LastSyncAt.Time.Format(time.RFC3339Nano)
		resp.LastSyncAt = &s
	}

	return resp
}

// List handles GET /api/v1/ldap/configs.
func (h *LDAPHandler) List(c fiber.Ctx, _ *apischema.Params) error {
	configs, err := h.queries.ListLDAPConfigs(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list LDAP configs")
	}

	resp := make([]ldapConfigResponse, len(configs))
	for i, cfg := range configs {
		resp[i] = toLDAPConfigResponse(cfg)
	}

	return RespondItems(c, resp)
}

// Get handles GET /api/v1/ldap/configs/:id.
func (h *LDAPHandler) Get(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	cfg, err := h.queries.GetLDAPConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "LDAP config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get LDAP config")
	}

	return c.JSON(toLDAPConfigResponse(cfg))
}

// Create handles POST /api/v1/ldap/configs.
func (h *LDAPHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	req, err := ldapConfigFromParams(p)
	if err != nil {
		return err
	}

	if err := validateLDAPServerURL(req.ServerURL); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if err := validateLDAPFilters(req.UserFilter, req.GroupFilter); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// No previous state on create, so the gate reads the END state: pass a
	// protected, verifying "before" and let the requested config be compared
	// against it.
	if err := requireLDAPTransportAck(
		true, ldapTransportProtected(req.ServerURL, req.StartTLS),
		false, req.SkipTLSVerify,
		req.AcknowledgeInsecureTLS); err != nil {
		return renderConfirmRequired(c, err)
	}

	encPassword := ""
	if req.BindPassword != "" {
		var encErr error
		encPassword, encErr = crypto.Encrypt(req.BindPassword, h.encryptionKey)
		if encErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt bind password")
		}
	}

	mappingJSON, _ := json.Marshal(req.GroupRoleMapping)

	defaultRoleID, err := optionalRoleID(req.DefaultRoleID)
	if err != nil {
		return err
	}

	syncInterval := req.SyncIntervalMinutes // 0 turns the scheduled sync off

	cfg, err := h.queries.CreateLDAPConfig(c.Context(), db.CreateLDAPConfigParams{
		Name:                  req.Name,
		Enabled:               req.Enabled,
		ServerUrl:             req.ServerURL,
		StartTls:              req.StartTLS,
		SkipTlsVerify:         req.SkipTLSVerify,
		BindDn:                req.BindDN,
		BindPasswordEncrypted: encPassword,
		SearchBaseDn:          req.SearchBaseDN,
		UserFilter:            withDefault(req.UserFilter, "(|(uid={{username}})(mail={{username}}))"),
		UsernameAttribute:     withDefault(req.UsernameAttribute, "uid"),
		EmailAttribute:        withDefault(req.EmailAttribute, "mail"),
		DisplayNameAttribute:  withDefault(req.DisplayNameAttribute, "cn"),
		GroupSearchBaseDn:     req.GroupSearchBaseDN,
		GroupFilter:           withDefault(req.GroupFilter, "(member={{userDN}})"),
		GroupAttribute:        withDefault(req.GroupAttribute, "cn"),
		GroupRoleMapping:      mappingJSON,
		DefaultRoleID:         defaultRoleID,
		SyncIntervalMinutes:   syncInterval,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create LDAP config")
	}

	// Same reasoning as Update: a directory created straight into cleartext is
	// as worth a trail as one moved there, and creating a new enabled config is
	// the easier of the two for a caller who wants passwords sent somewhere.
	createFields := map[string]any{"name": cfg.Name}
	if lostEnc, lostVer := ldapTransportWeakened(
		true, ldapTransportProtected(cfg.ServerUrl, cfg.StartTls),
		false, cfg.SkipTlsVerify); lostEnc || lostVer {
		createFields["insecure_transport_acknowledged"] = true
		createFields["new_transport"] = ldapTransportLabel(cfg.ServerUrl, cfg.StartTls, cfg.SkipTlsVerify)
	}
	details, _ := json.Marshal(createFields)
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", cfg.ID.String(), "ldap_config_created", details)

	return c.Status(fiber.StatusCreated).JSON(toLDAPConfigResponse(cfg))
}

// Update handles PUT /api/v1/ldap/configs/:id.
func (h *LDAPHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	existing, err := h.queries.GetLDAPConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "LDAP config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get LDAP config")
	}

	req, err := ldapConfigFromParams(p)
	if err != nil {
		return err
	}

	if err := validateLDAPServerURL(req.ServerURL); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	if err := validateLDAPFilters(req.UserFilter, req.GroupFilter); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	// A downgrade of the transport is gated the same way an address move is,
	// and for the same reason: the caller need never have seen the bind
	// password, and this is the other way to get it out of Nexara.
	wasProtected := ldapTransportProtected(existing.ServerUrl, existing.StartTls)
	nowProtected := ldapTransportProtected(req.ServerURL, req.StartTLS)
	lostEncryption, lostVerification := ldapTransportWeakened(
		wasProtected, nowProtected, existing.SkipTlsVerify, req.SkipTLSVerify)
	if err := requireLDAPTransportAck(
		wasProtected, nowProtected,
		existing.SkipTlsVerify, req.SkipTLSVerify,
		req.AcknowledgeInsecureTLS); err != nil {
		return renderConfirmRequired(c, err)
	}

	// Refuse to re-point the stored bind password at a directory the operator
	// never entrusted it to. See credential_redirect.go. Delivery is deferred
	// here: the password is bound against server_url on the next TestConnection,
	// scheduled sync, or user login. A config that binds anonymously has no
	// stored password and is not blocked from moving.
	if credentialRedirected(req.ServerURL, existing.ServerUrl, existing.BindPasswordEncrypted, req.BindPassword) {
		// Audited, because this is the one request that is unambiguously an
		// attempt to point a stored credential somewhere new. Refusing it
		// silently would make an enumeration of this path invisible.
		redirectDetails, _ := json.Marshal(map[string]any{
			// Truncated: nothing bounds the length of a URL a caller can
			// submit, and audit_log.details is readable by every Viewer.
			"attempted_server_url": auditSafe(req.ServerURL),
			"previous_server_url":  auditSafe(existing.ServerUrl),
		})
		AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", id.String(),
			"ldap_credential_redirect_refused", redirectDetails)
		return errCredentialRedirect("server URL", "bind password")
	}

	// If password is provided, encrypt it; otherwise keep existing
	encPassword := existing.BindPasswordEncrypted
	if req.BindPassword != "" {
		encPassword, err = crypto.Encrypt(req.BindPassword, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt bind password")
		}
	}

	mappingJSON, _ := json.Marshal(req.GroupRoleMapping)

	defaultRoleID, err := optionalRoleID(req.DefaultRoleID)
	if err != nil {
		return err
	}

	syncInterval := req.SyncIntervalMinutes // 0 turns the scheduled sync off

	cfg, err := h.queries.UpdateLDAPConfig(c.Context(), db.UpdateLDAPConfigParams{
		ID:                    id,
		Name:                  req.Name,
		Enabled:               req.Enabled,
		ServerUrl:             req.ServerURL,
		StartTls:              req.StartTLS,
		SkipTlsVerify:         req.SkipTLSVerify,
		BindDn:                req.BindDN,
		BindPasswordEncrypted: encPassword,
		SearchBaseDn:          req.SearchBaseDN,
		UserFilter:            withDefault(req.UserFilter, "(|(uid={{username}})(mail={{username}}))"),
		UsernameAttribute:     withDefault(req.UsernameAttribute, "uid"),
		EmailAttribute:        withDefault(req.EmailAttribute, "mail"),
		DisplayNameAttribute:  withDefault(req.DisplayNameAttribute, "cn"),
		GroupSearchBaseDn:     req.GroupSearchBaseDN,
		GroupFilter:           withDefault(req.GroupFilter, "(member={{userDN}})"),
		GroupAttribute:        withDefault(req.GroupAttribute, "cn"),
		GroupRoleMapping:      mappingJSON,
		DefaultRoleID:         defaultRoleID,
		SyncIntervalMinutes:   syncInterval,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update LDAP config")
	}

	// An acknowledged downgrade is the one action in this file the project
	// gates behind a confirmation, so it has to leave a trail — otherwise the
	// only record that a directory was moved to cleartext is the absence of
	// one. The transport is recorded as a label rather than the raw server_url:
	// GET on this config needs manage:user while view:audit is held by every
	// Viewer, and the label carries the whole point without the address.
	fields := map[string]any{"name": cfg.Name}
	if lostEncryption || lostVerification {
		fields["insecure_transport_acknowledged"] = true
		fields["previous_transport"] = ldapTransportLabel(existing.ServerUrl, existing.StartTls, existing.SkipTlsVerify)
		fields["new_transport"] = ldapTransportLabel(cfg.ServerUrl, cfg.StartTls, cfg.SkipTlsVerify)
	}
	details, _ := json.Marshal(fields)
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", cfg.ID.String(), "ldap_config_updated", details)

	return c.JSON(toLDAPConfigResponse(cfg))
}

// Delete handles DELETE /api/v1/ldap/configs/:id.
func (h *LDAPHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if err := h.queries.DeleteLDAPConfig(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete LDAP config")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", id.String(), "ldap_config_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}

type testConnectionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// TestConnection handles POST /api/v1/ldap/configs/:id/test.
func (h *LDAPHandler) TestConnection(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	cfg, err := h.queries.GetLDAPConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "LDAP config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get LDAP config")
	}

	ldapCfg, err := h.buildLDAPConfig(cfg)
	if err != nil {
		return c.JSON(testConnectionResponse{Success: false, Message: err.Error()})
	}

	client := auth.NewLDAPClient(ldapCfg)

	// Test basic connectivity and bind
	if err := client.TestConnection(); err != nil {
		slog.Error("LDAP test connection failed", "config_id", id, "error", err)
		return c.JSON(testConnectionResponse{
			Success: false,
			Message: classifyLDAPError(err),
		})
	}

	msg := "Connection and bind successful"

	// If a test username was provided, try to search for it.
	if testUsername := p.String("test_username"); testUsername != "" {
		user, err := client.SearchUser(testUsername)
		if err != nil {
			slog.Error("LDAP test user search failed", "config_id", id, "username", testUsername, "error", err)
			return c.JSON(testConnectionResponse{
				Success: false,
				Message: classifyLDAPError(err),
			})
		}
		msg = fmt.Sprintf("User found: %s (%s), %d groups.", user.DisplayName, user.Email, len(user.Groups))
		if kind, _, ok := strings.Cut(user.EntryID, ":"); ok {
			msg += " Accounts are bound to their directory entry by its " + kind + "."
		} else {
			msg += " The entry has neither entryUUID nor objectGUID, so accounts are matched by sign-in name and address only, as before."
		}
	}

	return c.JSON(testConnectionResponse{
		Success: true,
		Message: msg,
	})
}

// Sync handles POST /api/v1/ldap/configs/:id/sync.
func (h *LDAPHandler) Sync(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if _, err := h.queries.GetLDAPConfig(c.Context(), id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "LDAP config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get LDAP config")
	}

	// The count a confirm named, to pass the mass-miss rule on purpose (0: not given).
	// Bounded by the schema at math.MaxInt32 (registry_ldap.go).
	ack, _ := p.OptInt("acknowledge_missing")
	caller, _ := c.Locals("user_id").(uuid.UUID)
	res, err := h.syncConfig(c.Context(), id, int(ack), caller)
	if errors.Is(err, errLDAPSyncRunning) {
		// When the lease ends: a sync that stopped without releasing it holds it until then.
		answer := fiber.Map{
			"error":   "ldap_sync_running",
			"message": "A sync of this directory is already running. Try again when it has finished.",
		}
		if cur, gerr := h.queries.GetLDAPConfig(c.Context(), id); gerr == nil && cur.SyncLeaseExpiresAt.Valid {
			until := cur.SyncLeaseExpiresAt.Time.UTC()
			answer["message"] = "A sync of this directory is already running. Try again when it has finished; if it stopped, " +
				"a new sync can start from " + until.Format("15:04:05") + " UTC."
			answer["details"] = fiber.Map{"lease_expires_at": until.Format(time.RFC3339)}
		}
		return c.Status(fiber.StatusConflict).JSON(answer)
	}
	if res.ran {
		AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ldap", id.String(), "ldap_sync", res.auditDetails(err))
		for _, f := range res.followed {
			if h.claimAddressAudit(c.Context(), f) {
				AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "user", f.userID.String(), ldapAddressFollowedAction, f.details())
			}
		}
	}
	switch {
	case err == nil:
	case errors.Is(err, errLDAPSyncLeaseLost):
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error":   "ldap_sync_running",
			"message": "Another sync of this directory took over while this one ran, so this one stopped. Its counts are in the audit log.",
		})
	case errors.Is(err, errLDAPSyncTooManyMissing):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "ldap_sync_too_many_missing",
			"message": fmt.Sprintf("The directory found no entry for %d of the %d accounts it answered for, so the sync changed nothing. "+
				"Check the user filter, the search base DN and the bind account's read access. If these people have left, "+
				"repeat the sync with acknowledge_missing set to exactly %d to deactivate them.", res.missing, res.answered, res.missing),
			"details": fiber.Map{"users_missing": res.missing, "users_skipped": res.skipped, "acknowledge_field": "acknowledge_missing"},
		})
	case errors.Is(err, auth.ErrLDAPConnection), errors.Is(err, auth.ErrLDAPBindCredentials):
		return fiber.NewError(fiber.StatusBadGateway, "The directory could not be searched, so the sync stopped and nobody was deactivated by it. "+
			classifyLDAPError(err))
	case errors.Is(err, auth.ErrLDAPFilterInvalid):
		return fiber.NewError(fiber.StatusUnprocessableEntity, "The sync did not run, so nobody was deactivated by it: "+
			strings.TrimPrefix(err.Error(), "ldap: ")+". Correct the filter in the directory settings and save them.")
	default:
		slog.Error("LDAP sync failed", "config_id", id, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "The sync failed; the server log has the reason.")
	}

	return c.JSON(fiber.Map{
		"message":          "Sync complete",
		"users_synced":     res.synced,
		"users_disabled":   res.disabled,
		"users_re_enabled": res.reEnabled,
		"users_skipped":    res.skipped,
		// Missing accounts left active because they are the caller's own or the last
		// active holders of global manage:user; counted in users_skipped too.
		"users_kept_active": res.keptActive,
		// Accounts no sign-in has named yet, with a filter that does not match the
		// address: left as they are until they sign in once.
		"users_not_signed_in_since_upgrade": res.notSignedIn,
	})
}

// ldapSyncLease is how long a sync's lease (ClaimLDAPSyncLease) lasts past its last
// renewal: the sync renews it before every account, so a process that died or stopped
// holds the config for this long at most. It outlasts one account's worst case with a
// margin: against a degraded directory a search can be a dial, StartTLS, a bind, the
// search and the group search, each up to the one-minute request timeout
// (internal/auth ldapRequestTimeout). ldapSyncMaxDuration bounds the whole sync.
const (
	ldapSyncLease       = 6 * time.Minute
	ldapSyncMaxDuration = time.Hour
)

// errLDAPSyncLeaseLost is a sync stopped because its lease was no longer its own.
var errLDAPSyncLeaseLost = errors.New("ldap sync: the sync lease was lost")

// errLDAPSyncRunning is a sync refused because another sync of the same config holds
// its lease: the scheduled one, or a Sync Now on this or another replica.
var errLDAPSyncRunning = errors.New("ldap sync: another sync of this config is running")

// ldapSyncResult is what a sync did. ran is false for a sync that never started.
type ldapSyncResult struct {
	ran                                           bool
	synced, disabled, reEnabled, skipped, missing int
	answered                                      int // searches that answered: found or missing
	notSignedIn                                   int // no stored sign-in name, and a filter that does not match the address
	// acknowledged is the count a Sync Now confirmed to pass the mass-miss rule, when
	// the rule would have stopped this sync and the count was this run's; 0 otherwise.
	acknowledged int
	// keptActive are missing accounts not deactivated: the caller's own, or the last
	// active holder of global manage:user (also counted in skipped).
	keptActive int
	// followed are the accounts whose address moved with their entry's (or could not),
	// one audit row each.
	followed []addressFollowed
}

// ldapAddressFollowedAction is the audit action of an account whose entry's address
// changed: moved to the new address, or kept on its own when another account has it.
const ldapAddressFollowedAction = "ldap_entry_address_changed"

// addressFollowed is one account's address change, for its audit row: the directory's
// addresses (never anything typed), and whether the account took the new one.
type addressFollowed struct {
	userID         uuid.UUID
	from, to       string
	keptOwnBecause string // "" when the account moved to the new address
}

// details is the row's: for an account that kept its own address, not the new one,
// which another account holds (audit details are readable by every Viewer).
func (f addressFollowed) details() json.RawMessage {
	d := map[string]string{"previous_email": f.from, "email": f.to, "outcome": "updated"}
	if f.keptOwnBecause != "" {
		d = map[string]string{"email": f.from, "outcome": "kept", "reason": f.keptOwnBecause}
	}
	raw, _ := json.Marshal(d)
	return raw
}

// addressKeptAuditEvery is how often an account that keeps its own address is audited
// again: every sign-in and every sync would otherwise write the same row.
const addressKeptAuditEvery = 24 * time.Hour

// claimAddressKeptAudit says whether f's row is to be written: always for an account
// that moved, and for one that kept its address once a day (a Redis claim, as
// audit.ConsoleOpenRefusal), or every time without Redis or when it cannot answer.
func claimAddressKeptAudit(ctx context.Context, rdb *redis.Client, f addressFollowed) bool {
	if f.keptOwnBecause == "" || rdb == nil {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	first, err := boundedByContext(ctx, func(ctx context.Context) (bool, error) {
		return rdb.SetNX(ctx, "ldap:address-kept-audit:"+f.userID.String(), "1", addressKeptAuditEvery).Result()
	})
	return err != nil || first
}

// claimAddressAudit is claimAddressKeptAudit with this handler's Redis.
func (h *LDAPHandler) claimAddressAudit(ctx context.Context, f addressFollowed) bool {
	return claimAddressKeptAudit(ctx, h.rdb, f)
}

// sameAddress says whether an entry's address is the account's: the same spelling, or,
// between plain-ASCII addresses only, the same ignoring case (the rule of GetUserByEmail).
func sameAddress(entry, account string) bool {
	if entry == account {
		return true
	}
	ascii := func(s string) bool {
		for i := 0; i < len(s); i++ {
			if s[i] >= 0x80 {
				return false
			}
		}
		return true
	}
	return ascii(entry) && ascii(account) && strings.EqualFold(entry, account)
}

// filterMatch is what userFilterMatchesAddress can tell.
type filterMatch int

const (
	filterCannotTell filterMatch = iota
	filterMatches
	filterDoesNotMatch
)

// userFilterMatchesAddress says whether the user filter matches the address attribute
// against what was typed, so that searching by an account's address finds it: an
// equality term (<emailAttribute>={{username}}), ignoring case and spaces. A negation,
// or a term that puts {{username}} in a substring or another form, is "cannot tell".
func userFilterMatchesAddress(filter, emailAttribute string) filterMatch {
	f := strings.ToLower(strings.Join(strings.Fields(filter), ""))
	attr := strings.ToLower(strings.TrimSpace(emailAttribute))
	if attr == "" || strings.Contains(f, "!") {
		return filterCannotTell
	}
	term := "(" + attr + "={{username}})"
	if strings.Contains(f, term) {
		return filterMatches
	}
	if strings.Contains(f, "("+attr+"=") && strings.Contains(f, "{{username}}") {
		return filterCannotTell // the attribute is matched, but not as an equality on the typed name
	}
	return filterDoesNotMatch
}

// errLDAPSyncTooManyMissing is a sync that changed nothing because most of the accounts
// it searched for were missing (tooManyMissing).
var errLDAPSyncTooManyMissing = errors.New("ldap sync: most accounts searched for are missing from the directory")

// tooManyMissing is the mass-miss rule: every account whose search answered is
// missing, or more than half of them and at least three. A sync that meets it
// deactivates nobody, so it never deactivates the last accounts it can see.
func tooManyMissing(missing, searched int) bool {
	return missing > 0 && missing == searched || missing >= 3 && 2*missing > searched
}

// auditDetails are the ldap_sync row's details: the counts and, for a sync that
// stopped, the class of the failure (never the directory's own error text).
func (r ldapSyncResult) auditDetails(err error) json.RawMessage {
	d := map[string]any{"synced": r.synced, "disabled": r.disabled, "re_enabled": r.reEnabled, "skipped": r.skipped, "missing": r.missing,
		"not_signed_in_since_upgrade": r.notSignedIn}
	if r.keptActive > 0 {
		d["kept_active"] = r.keptActive
	}
	if r.acknowledged > 0 {
		d["mass_miss_acknowledged"] = true
		d["acknowledged_missing"] = r.acknowledged
	}
	if err != nil {
		d["outcome"] = "failed"
		d["reason"] = ldapFailureClass(err)
	}
	details, _ := json.Marshal(d)
	return details
}

// syncConfig runs one sync of a config, under its lease, and says what it did.
//
// Every LDAP account is searched for by the name it signs in with (its address when
// none is stored and the filter matches the address). An account bound to an entry
// under this config is that entry: found by its identifier when the name finds nobody
// or someone else, as long as the entry still passes the user filter. Only a search
// that succeeded and found nobody deactivates an account, and ends its sessions; a
// search that failed leaves the account as it is and is counted as skipped, and a
// directory that cannot be reached or bound to stops the sync. An account a sync
// deactivated is re-enabled by a sync that finds it again, after its old sessions are
// ended (an account deactivated by hand stays inactive: deactivated_by_directory). It
// stops between accounts when ctx ends, which is how it stops with the process.
//
// ackMissing is the count a Sync Now's confirm named: when the mass-miss rule would
// stop the sync and exactly that many accounts are missing, the sync goes on. The
// scheduled sync passes 0 and never does. No sync deactivates caller (uuid.Nil for the
// scheduled one) or the last active holder of global manage:user.
func (h *LDAPHandler) syncConfig(ctx context.Context, id uuid.UUID, ackMissing int, caller uuid.UUID) (res ldapSyncResult, err error) {
	token := uuid.New()
	cfg, err := h.queries.ClaimLDAPSyncLease(ctx, db.ClaimLDAPSyncLeaseParams{
		ID: id, Token: token, LeaseSeconds: ldapSyncLease.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return res, errLDAPSyncRunning
	}
	if err != nil {
		return res, fmt.Errorf("claim the sync lease: %w", err)
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := h.queries.ReleaseLDAPSyncLease(rctx, db.ReleaseLDAPSyncLeaseParams{ID: id, Token: token}); err != nil {
			slog.Warn("LDAP sync: could not release the sync lease; it runs out on its own", "config_id", id, "error", err)
		}
	}()
	res.ran = true
	ctx, cancel := context.WithTimeout(ctx, ldapSyncMaxDuration)
	defer cancel()
	renew := func() error {
		n, err := h.queries.RenewLDAPSyncLease(ctx, db.RenewLDAPSyncLeaseParams{ID: id, Token: token, LeaseSeconds: ldapSyncLease.Seconds()})
		if err != nil {
			return fmt.Errorf("renew the sync lease: %w", err)
		}
		if n == 0 {
			return errLDAPSyncLeaseLost
		}
		return nil
	}

	ldapCfg, err := h.buildLDAPConfig(cfg)
	if err != nil {
		return res, fmt.Errorf("build the LDAP config: %w", err)
	}
	// A filter that does not compile would fail every search, and each account
	// would only be skipped: the sync stops instead, and says why.
	if err := auth.ValidateFilters(cfg.UserFilter, cfg.GroupFilter); err != nil {
		return res, err
	}
	var client ldapDirectory = auth.NewLDAPClient(ldapCfg)
	if h.directory != nil {
		client = h.directory(ldapCfg)
	}

	ldapUsers, err := h.queries.ListLDAPUsers(ctx, cfg.ID)
	if err != nil {
		return res, fmt.Errorf("list the LDAP users: %w", err)
	}

	mapping := make(map[string]string)
	if len(cfg.GroupRoleMapping) > 0 {
		_ = json.Unmarshal(cfg.GroupRoleMapping, &mapping)
	}

	// Every search first, then the decision: a directory that answers "nobody" for
	// most accounts is a broken filter, base DN or bind account far more often than a
	// mass departure, and acting on it would deactivate everyone.
	type searched struct {
		dbUser db.ListLDAPUsersRow
		found  *auth.LDAPUser // nil: the search succeeded and found nobody
		name   string         // the name the bound entry was found by, when not the stored one
	}
	results := make([]searched, 0, len(ldapUsers))
	missing := 0
	byAddress := userFilterMatchesAddress(cfg.UserFilter, cfg.EmailAttribute) == filterMatches
	for _, dbUser := range ldapUsers {
		if err := ctx.Err(); err != nil {
			return res, fmt.Errorf("the sync was stopped: %w", err)
		}
		if err := renew(); err != nil {
			return res, err
		}
		bound := boundUnder(dbUser.LdapConfigID, cfg.ID)
		// The name the person signs in with, which is what the user filter is written
		// for. An account no sign-in has named yet is searched by its address only when
		// the filter matches the address; otherwise its search proves nothing either way,
		// so it is left alone until its next sign-in stores its name, unless it is bound
		// to an entry, which is found by its identifier.
		name := dbUser.LdapLoginName
		if name == "" && byAddress {
			name = dbUser.Email
		}
		if name == "" && !bound {
			res.notSignedIn++
			continue
		}
		found, foundBy, outcome, err := findSyncEntry(client, dbUser, name, bound)
		switch outcome {
		case syncEntryStop:
			return res, fmt.Errorf("search the directory: %w", err)
		case syncEntrySkipped:
			// The search did not answer whether the user is there: nothing changes.
			res.skipped++
			if err != nil {
				slog.Warn("LDAP sync: a user's search failed; the account is left as it is",
					"user_id", dbUser.ID, "reason", ldapFailureClass(err))
			}
		case syncEntryMissing:
			missing++
			results = append(results, searched{dbUser: dbUser})
		default:
			results = append(results, searched{dbUser: dbUser, found: found, name: foundBy})
		}
	}
	res.missing, res.answered = missing, len(results)
	if tooManyMissing(missing, len(results)) {
		if ackMissing <= 0 || missing != ackMissing {
			return res, errLDAPSyncTooManyMissing
		}
		res.acknowledged = ackMissing
	}

	// The accounts a deactivation must leave: the caller's own, and the last active
	// holder of global manage:user, without whom nobody could manage the install's
	// users (read once, before the first deactivation, and kept up to date).
	var managers map[uuid.UUID]bool
	keepActive := func(userID uuid.UUID) (bool, error) {
		if userID == caller {
			return true, nil
		}
		if managers == nil {
			ids, err := h.queries.ListActiveGlobalPermissionHolders(ctx, db.ListActiveGlobalPermissionHoldersParams{Action: "manage", Resource: "user"})
			if err != nil {
				return false, err
			}
			managers = make(map[uuid.UUID]bool, len(ids))
			for _, id := range ids {
				managers[id] = true
			}
		}
		return managers[userID] && len(managers) <= 1, nil
	}

	for _, r := range results {
		if err := renew(); err != nil {
			return res, err
		}
		dbUser, ldapUser := r.dbUser, r.found
		if ldapUser == nil {
			// Gone from the directory. And the sessions are ended, as an admin's
			// deactivation does: flipping is_active only suspends them, and a later
			// re-enable would revive every one with no sign-in. After the flip, as
			// separate statements: a sign-in racing it is refused by
			// CreateSessionAtEpoch's is_active, and every session older than the
			// flip is listed and revoked here.
			if !dbUser.IsActive {
				continue
			}
			if keep, err := keepActive(dbUser.ID); err != nil || keep {
				if err != nil {
					slog.Warn("LDAP sync: could not tell whether a missing account is the last user manager; it is left active",
						"user_id", dbUser.ID, "error", err)
				}
				res.skipped++
				res.keptActive++
				continue
			}
			n, err := h.queries.DeactivateLDAPUserForDirectory(ctx, dbUser.ID)
			if err != nil || n == 0 {
				continue
			}
			if _, err := auth.RevokeAllUserSessionsIn(ctx, h.queries, dbUser.ID); err != nil {
				slog.Error("LDAP sync: disabled a user but could not end their sessions; the sync that re-enables them retries it",
					"user_id", dbUser.ID, "error", err)
			}
			res.disabled++
			delete(managers, dbUser.ID)
			slog.Info("LDAP sync: disabled user removed from directory", "email", dbUser.Email)
			continue
		}

		// Bound now, by today's rule, when the directory exposes an identifier: the
		// entry is the one the account's name finds and its address is the account's.
		// An entry another account holds is that account's: nothing of this one changes.
		if !boundUnder(dbUser.LdapConfigID, cfg.ID) && ldapUser.EntryID != "" {
			if _, err := h.queries.LinkLDAPEntry(ctx, db.LinkLDAPEntryParams{ConfigID: cfg.ID, EntryID: ldapUser.EntryID, ID: dbUser.ID}); err != nil {
				res.skipped++
				slog.Warn("LDAP sync: could not bind an account to its directory entry; the account is left as it is",
					"user_id", dbUser.ID, "reason", ldapLinkFailureClass(err))
				continue
			}
		}
		if r.name != "" {
			if err := h.queries.SetLDAPLoginName(ctx, db.SetLDAPLoginNameParams{ID: dbUser.ID, LoginName: r.name}); err != nil {
				slog.Warn("LDAP sync: could not keep the name the account's entry answers to now", "user_id", dbUser.ID, "error", err)
			}
		}
		if f, ok := h.followEntryAddress(ctx, cfg.ID, dbUser.ID, dbUser.Email, ldapUser.Email); ok {
			res.followed = append(res.followed, f)
		}

		// A group search that failed says nothing about the user's roles: nothing of
		// theirs changes this time.
		if ldapUser.GroupLookupFailed {
			res.skipped++
			slog.Warn("LDAP sync: a user's group search failed; the account is left as it is", "user_id", dbUser.ID)
			continue
		}

		// In the directory: re-enabled when a sync had disabled them.
		if !dbUser.IsActive && dbUser.DeactivatedByDirectory {
			// End any session left from before the deactivation first: the revoke
			// above may have failed, and nothing else retries it. A no-op when they
			// are already ended; if it fails, the user stays disabled for the next sync.
			if _, err := auth.RevokeAllUserSessionsIn(ctx, h.queries, dbUser.ID); err != nil {
				slog.Error("LDAP sync: could not end a disabled user's old sessions; leaving the user disabled",
					"user_id", dbUser.ID, "error", err)
				continue
			}
			if n, err := h.queries.ReactivateLDAPUserFromDirectory(ctx, dbUser.ID); err == nil && n == 1 {
				res.reEnabled++
				slog.Info("LDAP sync: re-enabled user found in directory", "email", dbUser.Email)
			}
		}

		// Update display name if changed
		if ldapUser.DisplayName != "" && ldapUser.DisplayName != dbUser.DisplayName {
			_, _ = h.queries.UpdateLDAPUserProfile(ctx, db.UpdateLDAPUserProfileParams{
				ID:          dbUser.ID,
				DisplayName: ldapUser.DisplayName,
			})
		}

		// Sync group-to-role mapping
		h.syncUserRoles(ctx, dbUser.ID, ldapUser.Groups, mapping, cfg.DefaultRoleID)
		res.synced++
	}

	_ = h.queries.UpdateLDAPConfigLastSync(ctx, id)
	return res, nil
}

// boundUnder says whether an account's directory binding (users.ldap_config_id) is
// under this config. An account of a config since deleted reads NULL and binds again;
// one bound under another config that exists keeps that binding: a sync of this config
// does not list it, and a sign-in through this one signs it in by its address, as
// before bindings, and writes no binding.
func boundUnder(configID pgtype.UUID, cfgID uuid.UUID) bool {
	return configID.Valid && uuid.UUID(configID.Bytes) == cfgID
}

// errLDAPEntryMismatch is a directory entry whose address names an account bound to
// another entry: a new person given a departed person's name and address.
var errLDAPEntryMismatch = errors.New("ldap: the address names an account bound to another directory entry")

// usersLDAPEntryKey is the unique index on users (ldap_config_id, ldap_entry_id)
// (migration 000152): a binding refused by it is an entry another account holds.
const usersLDAPEntryKey = "users_ldap_entry_key"

// ldapAccount is ldapAccountForEntry's answer to "which account is this entry".
type ldapAccount struct {
	// User is the account: the zero value when Found is false (and, for
	// errLDAPEntryMismatch, the account the address names, for the audit row).
	User db.User
	// Found: a directory account answers to this entry.
	Found bool
	// Bound: it answers by the identifier stored on it. Found but not Bound: by its
	// address, an account not bound under this config (bound under none, or under
	// another config, which the caller does not overwrite).
	Bound bool
}

// ldapAccountForEntry answers which directory account an entry the directory found
// under config cfgID is, and writes nothing:
//   - the account bound to the entry's identifier, whatever its address says now
//     (Found, Bound);
//   - else the directory account the entry's address names (the typed name when the
//     entry has none; GetUserByEmailAndSource), when it is not bound under this config
//     (Found, not Bound): the caller binds it;
//   - else none (not Found, nil error).
//
// An address that names an account bound under this config to another entry, or bound
// while this entry carries no identifier, is errLDAPEntryMismatch. The sign-in's rule
// (provisionLDAPUser) and its failed bind's audit (directoryAccount) both ask it.
func ldapAccountForEntry(ctx context.Context, q *db.Queries, cfgID uuid.UUID, entry *auth.LDAPUser, typed string) (ldapAccount, error) {
	if entry.EntryID != "" {
		u, err := q.GetUserByLDAPEntry(ctx, db.GetUserByLDAPEntryParams{ConfigID: cfgID, EntryID: entry.EntryID})
		if err == nil {
			return ldapAccount{User: u, Found: true, Bound: true}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return ldapAccount{}, fmt.Errorf("lookup user by directory entry: %w", err)
		}
	}
	address := entry.Email
	if address == "" {
		address = typed
	}
	u, err := q.GetUserByEmailAndSource(ctx, db.GetUserByEmailAndSourceParams{Email: address, AuthSource: "ldap"})
	if errors.Is(err, pgx.ErrNoRows) {
		return ldapAccount{}, nil
	}
	if err != nil {
		return ldapAccount{}, fmt.Errorf("lookup user: %w", err)
	}
	if boundUnder(u.LdapConfigID, cfgID) {
		return ldapAccount{User: u}, errLDAPEntryMismatch
	}
	// Bound under another configuration that still exists: that configuration's
	// binding, which this one neither checks nor overwrites (LinkLDAPEntry).
	return ldapAccount{User: u, Found: true}, nil
}

// ldapLinkFailureClass names why a binding was not written, for the log.
func ldapLinkFailureClass(err error) string {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return "already_bound"
	case isDuplicateKeyError(err, usersLDAPEntryKey):
		return "entry_bound_to_another_account"
	default:
		return "database_error"
	}
}

// followEntryAddress moves a bound account to its entry's address when the entry's
// address differs from the account's (sameAddress), and says so for the audit row: a
// person whose mail changed keeps their one account, which is neither deactivated by a
// sync nor duplicated by a sign-in. When another account has the new address the
// account keeps its own, and that is recorded too. ok is false when nothing changed.
func (h *LDAPHandler) followEntryAddress(ctx context.Context, cfgID, userID uuid.UUID, from, to string) (f addressFollowed, ok bool) {
	if to == "" || sameAddress(to, from) {
		return addressFollowed{}, false
	}
	f = addressFollowed{userID: userID, from: from, to: to}
	_, err := h.queries.FollowLDAPEntryAddress(ctx, db.FollowLDAPEntryAddressParams{Email: to, ID: userID, ConfigID: cfgID})
	switch {
	case err == nil:
		slog.Info("LDAP: an account moved to its directory entry's new address", "user_id", userID)
	case errors.Is(err, pgx.ErrNoRows), isDuplicateKeyError(err):
		f.keptOwnBecause = "address_in_use"
	default:
		slog.Warn("LDAP: could not move an account to its directory entry's new address", "user_id", userID, "error", err)
		return addressFollowed{}, false
	}
	return f, true
}

// syncEntryOutcome is what a sync's search said about one account.
type syncEntryOutcome int

const (
	syncEntryFound   syncEntryOutcome = iota
	syncEntryMissing                  // the directory answered: the account's entry is not there
	syncEntrySkipped                  // no answer either way: nothing of the account changes
	syncEntryStop                     // the directory cannot be reached or bound to: the sync stops
)

// findSyncEntry is the sync's search for one account. An account not bound under the
// config is found by today's rule: the entry its name finds, when that entry's address
// is the account's (sameAddress; a reused name finds someone else, who is never this
// account). A bound account is the entry whose identifier it holds: the one its name
// finds when that carries the identifier, else the entry the identifier finds, when it
// still answers to the user filter by its current name or address (foundBy, for the
// next sync). A name that finds an entry with no identifier says nothing about a bound
// account: skipped.
func findSyncEntry(client ldapDirectory, u db.ListLDAPUsersRow, name string, bound bool) (found *auth.LDAPUser, foundBy string, outcome syncEntryOutcome, err error) {
	classify := func(err error) syncEntryOutcome {
		switch {
		case errors.Is(err, auth.ErrLDAPUserNotFound):
			return syncEntryMissing
		case errors.Is(err, auth.ErrLDAPConnection), errors.Is(err, auth.ErrLDAPBindCredentials):
			return syncEntryStop
		default:
			return syncEntrySkipped
		}
	}
	var entry *auth.LDAPUser
	if name != "" {
		e, err := client.SearchUser(name)
		if err != nil && classify(err) != syncEntryMissing {
			return nil, "", classify(err), err
		}
		entry = e
	}
	if !bound {
		switch {
		case entry == nil:
			return nil, "", syncEntryMissing, nil
		case entry.Email == "":
			// An entry with no address cannot be told to be this account's.
			return nil, "", syncEntrySkipped, nil
		case !sameAddress(entry.Email, u.Email):
			return nil, "", syncEntryMissing, nil
		}
		return entry, "", syncEntryFound, nil
	}
	if entry != nil {
		switch entry.EntryID {
		case u.LdapEntryID:
			return entry, "", syncEntryFound, nil
		case "":
			return nil, "", syncEntrySkipped, nil
		}
	}
	// The name finds nobody, or someone else: the account's own entry, by its identifier.
	own, err := client.SearchUserByEntryID(u.LdapEntryID)
	if err != nil {
		return nil, "", classify(err), err
	}
	for _, n := range []string{own.Username, own.Email} {
		if n == "" || n == name {
			continue
		}
		e, err := client.SearchUser(n)
		switch {
		case err == nil && e.EntryID == u.LdapEntryID:
			return e, n, syncEntryFound, nil
		case err != nil && classify(err) != syncEntryMissing:
			return nil, "", classify(err), err
		}
	}
	// The entry is there but the user filter no longer admits it.
	return nil, "", syncEntryMissing, nil
}

// RunScheduledSync runs the sync of the config sign-in uses (the oldest enabled one)
// when its Sync Interval has passed since its last sync, or since the last scheduled
// attempt, which a sync that stopped does not move last_sync_at past; 0 is off. The
// scheduler ticks it every minute on the leader replica. It is audited as the system
// account, the way internal/scheduler writes its own rows.
func (h *LDAPHandler) RunScheduledSync(ctx context.Context) {
	// A panic here, or in go-ldap, must not take the process down; the lease is released
	// by syncConfig's own defer. Only the panic's type is logged: its value can carry a
	// search filter, and with it what someone typed.
	defer func() {
		if r := recover(); r != nil {
			slog.Error("LDAP sync: the scheduled sync panicked", "panic_type", fmt.Sprintf("%T", r))
		}
	}()
	cfg, err := h.queries.GetEnabledLDAPConfig(ctx)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("LDAP sync: could not read the enabled config", "error", err)
		}
		return
	}
	if cfg.SyncIntervalMinutes <= 0 {
		return
	}
	now := time.Now()
	if h.now != nil {
		now = h.now()
	}
	interval := time.Duration(cfg.SyncIntervalMinutes) * time.Minute
	h.scheduleMu.Lock()
	last := h.lastScheduled[cfg.ID]
	if cfg.LastSyncAt.Valid && cfg.LastSyncAt.Time.After(last) {
		last = cfg.LastSyncAt.Time
	}
	h.scheduleMu.Unlock()
	if !last.IsZero() && now.Sub(last) < interval {
		return
	}

	res, err := h.syncConfig(ctx, cfg.ID, 0, uuid.Nil) // the schedule never passes the mass-miss rule
	if !res.ran {
		if !errors.Is(err, errLDAPSyncRunning) {
			slog.Warn("LDAP sync: the scheduled sync could not start", "config_id", cfg.ID, "error", err)
		}
		return // tried again at the next tick
	}
	h.scheduleMu.Lock()
	if h.lastScheduled == nil {
		h.lastScheduled = map[uuid.UUID]time.Time{}
	}
	h.lastScheduled[cfg.ID] = now
	h.scheduleMu.Unlock()
	if err != nil {
		slog.Warn("LDAP sync: the scheduled sync stopped", "config_id", cfg.ID, "reason", ldapFailureClass(err))
	}
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := audit.Write(actx, h.queries, h.eventPub, db.InsertAuditLogParams{
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "ldap",
		ResourceID:   cfg.ID.String(),
		Action:       "ldap_sync",
		Details:      res.auditDetails(err),
	}); err != nil {
		slog.Warn("LDAP sync: failed to insert the audit row of a scheduled sync", "config_id", cfg.ID, "error", err)
	}
	for _, f := range res.followed {
		if !h.claimAddressAudit(actx, f) {
			continue
		}
		if err := audit.Write(actx, h.queries, h.eventPub, db.InsertAuditLogParams{
			UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
			ResourceType: "user",
			ResourceID:   f.userID.String(),
			Action:       ldapAddressFollowedAction,
			Details:      f.details(),
		}); err != nil {
			slog.Warn("LDAP sync: failed to insert the audit row of an address change", "user_id", f.userID, "error", err)
		}
	}
}

func (h *LDAPHandler) buildLDAPConfig(cfg db.LdapConfig) (auth.LDAPConfig, error) {
	bindPassword := ""
	if cfg.BindPasswordEncrypted != "" {
		var err error
		bindPassword, err = crypto.Decrypt(cfg.BindPasswordEncrypted, h.encryptionKey)
		if err != nil {
			return auth.LDAPConfig{}, fmt.Errorf("decrypt bind password: %w", err)
		}
	}

	return auth.LDAPConfig{
		ServerURL:            cfg.ServerUrl,
		StartTLS:             cfg.StartTls,
		SkipTLSVerify:        cfg.SkipTlsVerify,
		BindDN:               cfg.BindDn,
		BindPassword:         bindPassword,
		SearchBaseDN:         cfg.SearchBaseDn,
		UserFilter:           cfg.UserFilter,
		UsernameAttribute:    cfg.UsernameAttribute,
		EmailAttribute:       cfg.EmailAttribute,
		DisplayNameAttribute: cfg.DisplayNameAttribute,
		GroupSearchBaseDN:    cfg.GroupSearchBaseDn,
		GroupFilter:          cfg.GroupFilter,
		GroupAttribute:       cfg.GroupAttribute,
	}, nil
}

// syncUserRoles maps LDAP group DNs to RBAC role UUIDs and assigns them.
func (h *LDAPHandler) syncUserRoles(ctx context.Context, userID uuid.UUID, groups []string, mapping map[string]string, defaultRoleID pgtype.UUID) {
	// Clear existing RBAC roles
	_ = h.queries.RevokeAllUserRoles(ctx, userID)

	assigned := false
	for _, groupDN := range groups {
		roleIDStr, ok := mapping[groupDN]
		if !ok {
			continue
		}
		roleID, err := uuid.Parse(roleIDStr)
		if err != nil {
			continue
		}
		if _, err := h.queries.AssignUserRole(ctx, db.AssignUserRoleParams{
			UserID:    userID,
			RoleID:    roleID,
			ScopeType: "global",
		}); !mappedRoleAssigned(err, "ldap", userID, roleID) {
			continue
		}
		assigned = true
	}

	// If no group matched, assign default role
	if !assigned && defaultRoleID.Valid {
		defID, _ := uuid.FromBytes(defaultRoleID.Bytes[:])
		_, _ = h.queries.AssignUserRole(ctx, db.AssignUserRoleParams{
			UserID:    userID,
			RoleID:    defID,
			ScopeType: "global",
		})
	}

	// Invalidate RBAC cache
	if h.rbac != nil {
		h.rbac.InvalidateUser(ctx, userID)
	}
}

// mappedRoleAssigned says whether a group mapping's role was assigned, and logs one
// that was not. A mapping can name a role deleted since (nothing ties the mapping's
// JSON to roles), and that assigns nothing: the user then gets the default role, as a
// user no mapping matches does.
func mappedRoleAssigned(err error, source string, userID, roleID uuid.UUID) bool {
	if err == nil || isDuplicateKeyError(err) { // a second group mapping to a role already assigned
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation {
		slog.Warn("group mapping names a role that no longer exists; it assigns nothing",
			"source", source, "user_id", userID, "role_id", roleID)
	} else {
		slog.Warn("could not assign a group mapping's role", "source", source, "user_id", userID, "role_id", roleID, "error", err)
	}
	return false
}

// BuildLDAPConfigFromDB creates an auth.LDAPConfig from a DB model. Exported for use by auth handler.
func (h *LDAPHandler) BuildLDAPConfigFromDB(cfg db.LdapConfig) (auth.LDAPConfig, error) {
	return h.buildLDAPConfig(cfg)
}

// SyncUserRoles is exported for use by the auth handler during login.
func (h *LDAPHandler) SyncUserRoles(c fiber.Ctx, userID uuid.UUID, groups []string, mapping map[string]string, defaultRoleID pgtype.UUID) {
	h.syncUserRoles(c.Context(), userID, groups, mapping, defaultRoleID)
}

// optionalRoleID turns the default_role_id body value into the nullable column
// it is written to.
//
// The empty string and an absent key mean the same thing — no default role —
// which is why the parameter is declared with the emptyOrUUID PATTERN rather
// than the uuid format: every registered format refuses "". The parse cannot
// fail on a value that satisfied the pattern, so a failure here is a
// declaration bug rather than a caller's mistake, and parseParamUUID reports it
// as one.
//
// Shared by the LDAP and OIDC writes, which carry the same field.
func optionalRoleID(value string) (pgtype.UUID, error) {
	if value == "" {
		return pgtype.UUID{}, nil
	}
	parsed, err := parseParamUUID(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func withDefault(val, def string) string {
	if val == "" {
		return def
	}
	return val
}

func classifyLDAPError(err error) string {
	switch {
	case errors.Is(err, auth.ErrLDAPConnection):
		return "Could not connect to LDAP server. Verify the server URL and that the server is reachable."
	case errors.Is(err, auth.ErrLDAPBindCredentials):
		return "Bind failed: invalid bind DN or password. Verify your service account credentials."
	case errors.Is(err, auth.ErrLDAPUserNotFound):
		return "User not found. Check your search base DN, user filter, and username attribute. For Active Directory, use sAMAccountName instead of uid."
	case errors.Is(err, auth.ErrLDAPSearchFailed):
		return "Search failed. Verify the search base DN exists and the bind account has read permissions."
	case errors.Is(err, auth.ErrLDAPUnavailable):
		return "The directory did not answer in time. Verify that it is reachable and not overloaded."
	case errors.Is(err, auth.ErrLDAPUserBindFailed):
		return "User found but password is incorrect."
	case errors.Is(err, auth.ErrLDAPFilterInvalid):
		return "The user filter or the group filter is not a valid LDAP filter. Correct it and save the settings again."
	default:
		return "LDAP operation failed. Check server logs for details."
	}
}

func validateLDAPFilters(userFilter, groupFilter string) error {
	if userFilter != "" && !strings.Contains(userFilter, "{{username}}") {
		return fmt.Errorf("user_filter must contain the {{username}} placeholder")
	}
	if groupFilter != "" && !strings.Contains(groupFilter, "{{userDN}}") {
		return fmt.Errorf("group_filter must contain the {{userDN}} placeholder")
	}
	return auth.ValidateFilters(userFilter, groupFilter)
}

// ldapTransportProtected reports whether a config encrypts its connection at
// all. ldaps:// is implicit TLS; on ldap:// only StartTLS upgrades it.
func ldapTransportProtected(serverURL string, startTLS bool) bool {
	// Lowercased because wasProtected is computed from a STORED server_url.
	// validateLDAPServerURL is case-sensitive and runs on every write path, so
	// "LDAPS://" cannot be stored today — but a row that predates it would
	// otherwise read as cleartext, making a real downgrade look like no change
	// and skipping the prompt.
	return startTLS || strings.HasPrefix(strings.ToLower(serverURL), "ldaps://")
}

// ldapTransportWeakened reports the two ways a config can move to a weaker
// connection: losing encryption entirely, and keeping it but dropping the
// certificate check. They are reported separately because the operator needs
// different words for each — cleartext is readable by anyone on the wire,
// unverified only by someone who can intercept.
//
// Split out from the gate so the rule is testable without a request context.
func ldapTransportWeakened(wasProtected, nowProtected, wasSkippingVerify, nowSkippingVerify bool) (lostEncryption, lostVerification bool) {
	return wasProtected && !nowProtected, !wasSkippingVerify && nowSkippingVerify
}

// ldapTransportLabel names a config's connection security in one token, for
// audit rows that must not carry the server address itself.
func ldapTransportLabel(serverURL string, startTLS, skipVerify bool) string {
	if !ldapTransportProtected(serverURL, startTLS) {
		return "cleartext"
	}
	base := "starttls"
	if strings.HasPrefix(strings.ToLower(serverURL), "ldaps://") {
		base = "ldaps"
	}
	if skipVerify {
		return base + " (unverified)"
	}
	return base
}

// requireLDAPTransportAck refuses a config that weakens LDAP transport unless
// the caller says so explicitly.
//
// Why this is NOT conditional on a stored bind password: LDAP authentication
// binds AS THE USER, so every interactive login puts that person's password on
// this connection. A config doing an anonymous service bind still carries end
// user passwords, and a directory on a segment someone can sniff needs no MITM
// at all for cleartext to be readable.
//
// Confirm-and-proceed rather than a refusal, matching requireInsecureTLSAck and
// the private-address gate: a lab directory with no CA is a real case. What it
// removes is doing it by accident — and note start_tls and skip_tls_verify are
// non-pointer bools on a full-body PUT, so a client that merely OMITS them
// turns StartTLS off. That is the accident this catches.
//
// The predicate is the CHANGE, not the end state, so an install already running
// cleartext is not made to re-acknowledge on every unrelated edit. Pass
// wasProtected=true / wasSkippingVerify=false on create, where there is no
// previous state and the end state is what matters.
func requireLDAPTransportAck(wasProtected, nowProtected, wasSkippingVerify, nowSkippingVerify, acknowledged bool) error {
	lostEncryption, lostVerification := ldapTransportWeakened(
		wasProtected, nowProtected, wasSkippingVerify, nowSkippingVerify)
	if (!lostEncryption && !lostVerification) || acknowledged {
		return nil
	}

	// Losing encryption outranks losing verification: a cleartext connection
	// has no certificate to talk about, and the operator needs the stronger of
	// the two warnings.
	if lostEncryption {
		return &confirmRequiredError{
			Code: confirmInsecureLDAPTransport,
			Message: "This would carry the bind password and every user's login password to " +
				"the directory in cleartext — ldap:// without StartTLS is not encrypted at all.",
			AcknowledgeField: "acknowledge_insecure_tls",
			Fields:           map[string]any{"transport_kind": "cleartext"},
		}
	}
	return &confirmRequiredError{
		Code: confirmInsecureLDAPTransport,
		Message: "This directory connection's certificate would never be verified, so the " +
			"passwords on it are exposed to anyone who can intercept the connection.",
		AcknowledgeField: "acknowledge_insecure_tls",
		Fields:           map[string]any{"transport_kind": "unverified"},
	}
}

// confirmInsecureLDAPTransport is the code the admin page keys on to turn the
// refusal into a prompt.
const confirmInsecureLDAPTransport = "insecure_ldap_transport_confirm_required"

func validateLDAPServerURL(rawURL string) error {
	if !strings.HasPrefix(rawURL, "ldap://") && !strings.HasPrefix(rawURL, "ldaps://") {
		return fmt.Errorf("server_url must use ldap:// or ldaps:// scheme")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid server URL: %w", err)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("server_url must include a hostname")
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return fmt.Errorf("server_url must not point to loopback or link-local addresses")
	}
	return nil
}
