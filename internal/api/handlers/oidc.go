package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
)

// Seven of the 8 routes are declared in internal/api/registry_oidc.go, which
// states their permission — a global manage:user Check on the six admin ones,
// Public on /authorize — and their parameters.
//
// Callback is the EIGHTH and stays legacy in router.go: its query string is
// composed by the identity provider, and the registry answers an undeclared key
// with a 400. See registerOIDCEndpoints' doc comment.
//
// What stays here is what a declaration cannot see: the cleartext-callback
// confirmation, the credential-redirect refusal, and the issuer/redirect URL
// validators — one of which resolves DNS, which no parameter schema could do.

// oidcExchangeEpochKey is the key, in the JSON a callback stores under
// oidc:exchange:<code>, that holds the user's auth epoch as the callback read it
// (decimal). storeExchange writes it and OIDCTokenExchange reads it, and the session
// the exchange creates is conditional on it: see OIDCTokenExchange for why the epoch
// travels with the code instead of being read again at the exchange.
const oidcExchangeEpochKey = "auth_epoch"

// oidcExchangeBindingKey is the key, in the same JSON, of the digest of the browser
// binding's secret (oidcExchangeBinding), which the exchange checks before it spends
// the code.
const oidcExchangeBindingKey = "binding"

// OIDCHandler handles OIDC/SSO configuration and auth flow endpoints.
type OIDCHandler struct {
	queries       *db.Queries
	encryptionKey string
	rbac          *auth.RBACEngine
	eventPub      *events.Publisher
	rdb           *redis.Client
}

// NewOIDCHandler creates a new OIDC handler.
func NewOIDCHandler(queries *db.Queries, encryptionKey string, rbac *auth.RBACEngine, eventPub *events.Publisher, rdb *redis.Client) *OIDCHandler {
	return &OIDCHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		rbac:          rbac,
		eventPub:      eventPub,
		rdb:           rdb,
	}
}

// --- Request/Response types ---

// oidcConfigRequest is the validated body of both OIDC writes, read once out of
// the registry's parameters so the two handlers cannot drift on how they read
// it. The shape is unchanged from the struct Bind().Body used to fill; what
// moved out is the parsing.
//
// ClientSecret is write-only and must stay out of every audit row: view:audit
// is a default Viewer grant, and both handlers build their details field by
// field for exactly that reason.
type oidcConfigRequest struct {
	Name             string
	Enabled          bool
	IssuerURL        string
	ClientID         string
	ClientSecret     string
	RedirectURI      string
	Scopes           []string
	EmailClaim       string
	DisplayNameClaim string
	GroupsClaim      string
	GroupRoleMapping map[string]string
	DefaultRoleID    string
	AutoProvision    bool
	AllowedDomains   []string
	// AcknowledgeInsecureRedirect is required to store a plain-http callback
	// on anything but loopback.
	AcknowledgeInsecureRedirect bool
	// AllowPrivateAddress confirms an issuer on a private network address
	// (checkOIDCIssuer).
	AllowPrivateAddress bool
	// AcknowledgeIssuerChange confirms moving the issuer of a configuration that
	// accounts are linked under (requireOIDCIssuerChangeAck).
	AcknowledgeIssuerChange bool
}

// oidcConfigFromParams reads the declared body.
//
// Every key is a string literal at this call site on purpose:
// registry_paramkey_guard_test.go checks each one against the route's schema,
// and a name that only appeared inside a helper would be a name it cannot see.
func oidcConfigFromParams(p *apischema.Params) (oidcConfigRequest, error) {
	mapping, err := StringMapFromObject(p.Object("group_role_mapping"), "group_role_mapping")
	if err != nil {
		return oidcConfigRequest{}, err
	}
	return oidcConfigRequest{
		Name:             p.String("name"),
		Enabled:          p.Bool("enabled"),
		IssuerURL:        p.String("issuer_url"),
		ClientID:         p.String("client_id"),
		ClientSecret:     p.String("client_secret"),
		RedirectURI:      p.String("redirect_uri"),
		Scopes:           p.Strings("scopes"),
		EmailClaim:       p.String("email_claim"),
		DisplayNameClaim: p.String("display_name_claim"),
		GroupsClaim:      p.String("groups_claim"),
		GroupRoleMapping: mapping,
		DefaultRoleID:    p.String("default_role_id"),
		AutoProvision:    p.Bool("auto_provision"),
		AllowedDomains:   p.Strings("allowed_domains"),

		AcknowledgeInsecureRedirect: p.Bool("acknowledge_insecure_redirect"),
		AllowPrivateAddress:         p.Bool("allow_private_address"),
		AcknowledgeIssuerChange:     p.Bool("acknowledge_issuer_change"),
	}, nil
}

type oidcConfigResponse struct {
	ID               uuid.UUID         `json:"id"`
	Name             string            `json:"name"`
	Enabled          bool              `json:"enabled"`
	IssuerURL        string            `json:"issuer_url"`
	ClientID         string            `json:"client_id"`
	ClientSecretSet  bool              `json:"client_secret_set"`
	RedirectURI      string            `json:"redirect_uri"`
	Scopes           []string          `json:"scopes"`
	EmailClaim       string            `json:"email_claim"`
	DisplayNameClaim string            `json:"display_name_claim"`
	GroupsClaim      string            `json:"groups_claim"`
	GroupRoleMapping map[string]string `json:"group_role_mapping"`
	DefaultRoleID    *string           `json:"default_role_id"`
	AutoProvision    bool              `json:"auto_provision"`
	AllowedDomains   []string          `json:"allowed_domains"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`

	// AllowPrivateAddress: the operator confirmed this configuration's issuer on
	// a private network address.
	AllowPrivateAddress bool `json:"allow_private_address"`

	// LinkedAccounts is how many accounts are bound to an identity of this
	// configuration's issuer: the ones a change of the issuer leaves refused.
	LinkedAccounts int64 `json:"linked_accounts"`
}

// configResponse is toOIDCConfigResponse with the count of accounts linked under
// the configuration's issuer.
func (h *OIDCHandler) configResponse(ctx context.Context, cfg db.OidcConfig) (oidcConfigResponse, error) {
	resp := toOIDCConfigResponse(cfg)
	n, err := h.queries.CountUsersLinkedToIssuer(ctx, cfg.IssuerUrl)
	if err != nil {
		return oidcConfigResponse{}, fmt.Errorf("count linked accounts: %w", err)
	}
	resp.LinkedAccounts = n
	return resp, nil
}

func toOIDCConfigResponse(cfg db.OidcConfig) oidcConfigResponse {
	resp := oidcConfigResponse{
		ID:               cfg.ID,
		Name:             cfg.Name,
		Enabled:          cfg.Enabled,
		IssuerURL:        cfg.IssuerUrl,
		ClientID:         cfg.ClientID,
		ClientSecretSet:  cfg.ClientSecretEncrypted != "",
		RedirectURI:      cfg.RedirectUri,
		Scopes:           cfg.Scopes,
		EmailClaim:       cfg.EmailClaim,
		DisplayNameClaim: cfg.DisplayNameClaim,
		GroupsClaim:      cfg.GroupsClaim,
		AutoProvision:    cfg.AutoProvision,
		AllowedDomains:   cfg.AllowedDomains,
		CreatedAt:        cfg.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:        cfg.UpdatedAt.Format(time.RFC3339Nano),

		AllowPrivateAddress: cfg.AllowPrivateAddress,
	}

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

	// Ensure non-nil slices for JSON
	if resp.Scopes == nil {
		resp.Scopes = []string{}
	}
	if resp.AllowedDomains == nil {
		resp.AllowedDomains = []string{}
	}

	return resp
}

// --- Admin CRUD endpoints ---

// List handles GET /api/v1/oidc/configs.
func (h *OIDCHandler) List(c fiber.Ctx, _ *apischema.Params) error {
	configs, err := h.queries.ListOIDCConfigs(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list OIDC configs")
	}

	resp := make([]oidcConfigResponse, len(configs))
	for i, cfg := range configs {
		if resp[i], err = h.configResponse(c.Context(), cfg); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to list OIDC configs")
		}
	}

	return RespondItems(c, resp)
}

// Get handles GET /api/v1/oidc/configs/:id.
func (h *OIDCHandler) Get(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	cfg, err := h.queries.GetOIDCConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "OIDC config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OIDC config")
	}

	resp, err := h.configResponse(c.Context(), cfg)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OIDC config")
	}
	return c.JSON(resp)
}

// Create handles POST /api/v1/oidc/configs.
func (h *OIDCHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	req, err := oidcConfigFromParams(p)
	if err != nil {
		return err
	}

	allowPrivate, err := checkOIDCIssuer(c.Context(), req.IssuerURL, req.AllowPrivateAddress, oidcIssuerResubmit)
	if err != nil {
		return renderAddressPolicyError(c, err)
	}
	if err := validateOIDCRedirectURI(req.RedirectURI); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if err := requireOIDCRedirectSchemeAck(req.RedirectURI, req.AcknowledgeInsecureRedirect); err != nil {
		return renderConfirmRequired(c, err)
	}

	encSecret := ""
	if req.ClientSecret != "" {
		var encErr error
		encSecret, encErr = crypto.Encrypt(req.ClientSecret, h.encryptionKey)
		if encErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt client secret")
		}
	}

	mappingJSON, _ := json.Marshal(req.GroupRoleMapping)

	defaultRoleID, err := optionalRoleID(req.DefaultRoleID)
	if err != nil {
		return err
	}

	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}

	allowedDomains := req.AllowedDomains
	if allowedDomains == nil {
		allowedDomains = []string{}
	}

	cfg, err := h.queries.CreateOIDCConfig(c.Context(), db.CreateOIDCConfigParams{
		Name:                  withDefault(req.Name, "Default"),
		Enabled:               req.Enabled,
		IssuerUrl:             req.IssuerURL,
		ClientID:              req.ClientID,
		ClientSecretEncrypted: encSecret,
		RedirectUri:           req.RedirectURI,
		Scopes:                scopes,
		EmailClaim:            withDefault(req.EmailClaim, "email"),
		DisplayNameClaim:      withDefault(req.DisplayNameClaim, "name"),
		GroupsClaim:           withDefault(req.GroupsClaim, "groups"),
		GroupRoleMapping:      mappingJSON,
		DefaultRoleID:         defaultRoleID,
		AutoProvision:         req.AutoProvision,
		AllowedDomains:        allowedDomains,
		AllowPrivateAddress:   allowPrivate,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create OIDC config")
	}

	// The redirect URI is recorded on create for the same reason Update records
	// a change to it: nothing downstream can tell a legitimate callback from
	// one pointed at a host the caller controls, so the row is the only trace.
	fields := map[string]any{
		"name":         cfg.Name,
		"issuer_url":   auditSafe(cfg.IssuerUrl),
		"redirect_uri": auditSafe(cfg.RedirectUri),
	}
	if allowPrivate {
		fields["allow_private_address"] = true
	}
	details, _ := json.Marshal(fields)
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "oidc", cfg.ID.String(), "oidc_config_created", details)

	resp, err := h.configResponse(c.Context(), cfg)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "The OIDC config was created; reading it back failed")
	}
	return c.Status(fiber.StatusCreated).JSON(resp)
}

// Update handles PUT /api/v1/oidc/configs/:id.
func (h *OIDCHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	existing, err := h.queries.GetOIDCConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "OIDC config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OIDC config")
	}

	req, err := oidcConfigFromParams(p)
	if err != nil {
		return err
	}

	// The issuer is checked again on every write. A confirmation is kept while the
	// issuer stays the same, so that editing anything else does not ask again; a new
	// issuer is decided anew.
	confirmed := req.AllowPrivateAddress || existing.AllowPrivateAddress && req.IssuerURL == existing.IssuerUrl
	allowPrivate, err := checkOIDCIssuer(c.Context(), req.IssuerURL, confirmed, oidcIssuerResubmit)
	if err != nil {
		return renderAddressPolicyError(c, err)
	}

	// Validated only when it changes: this rule is newer than the column, so an
	// install carrying a value that predates it can still edit everything else
	// on the config. Any NEW value has to satisfy it.
	if req.RedirectURI != existing.RedirectUri {
		if err := validateOIDCRedirectURI(req.RedirectURI); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if err := requireOIDCRedirectSchemeAck(req.RedirectURI, req.AcknowledgeInsecureRedirect); err != nil {
			return renderConfirmRequired(c, err)
		}
	}

	// Refuse to re-point the stored client secret at an issuer the operator
	// never entrusted it to. See credential_redirect.go. Delivery is deferred
	// here: the issuer determines the token endpoint, so the secret is posted
	// to whatever that host names on the next callback. A public client holds
	// no stored secret and is not blocked from moving.
	if credentialRedirected(req.IssuerURL, existing.IssuerUrl, existing.ClientSecretEncrypted, req.ClientSecret) {
		// Audited, because this is the one request that is unambiguously an
		// attempt to point a stored credential somewhere new. Refusing it
		// silently would make an enumeration of this path invisible.
		redirectDetails, _ := json.Marshal(map[string]any{
			// Truncated: nothing bounds the length of a URL a caller can
			// submit, and audit_log.details is readable by every Viewer.
			"attempted_issuer_url": auditSafe(req.IssuerURL),
			"previous_issuer_url":  auditSafe(existing.IssuerUrl),
		})
		AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "oidc", id.String(),
			"oidc_credential_redirect_refused", redirectDetails)
		return errCredentialRedirect("issuer URL", "client secret")
	}

	if req.IssuerURL != existing.IssuerUrl && !req.AcknowledgeIssuerChange {
		linked, err := h.queries.CountUsersLinkedToIssuer(c.Context(), existing.IssuerUrl)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to update OIDC config")
		}
		if err := requireOIDCIssuerChangeAck(linked); err != nil {
			return renderConfirmRequired(c, err)
		}
	}

	// If secret is provided, encrypt it; otherwise keep existing
	encSecret := existing.ClientSecretEncrypted
	if req.ClientSecret != "" {
		encSecret, err = crypto.Encrypt(req.ClientSecret, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt client secret")
		}
	}

	mappingJSON, _ := json.Marshal(req.GroupRoleMapping)

	defaultRoleID, err := optionalRoleID(req.DefaultRoleID)
	if err != nil {
		return err
	}

	scopes := req.Scopes
	if len(scopes) == 0 {
		scopes = []string{"openid", "email", "profile"}
	}

	allowedDomains := req.AllowedDomains
	if allowedDomains == nil {
		allowedDomains = []string{}
	}

	cfg, err := h.queries.UpdateOIDCConfig(c.Context(), db.UpdateOIDCConfigParams{
		ID:                    id,
		Name:                  withDefault(req.Name, "Default"),
		Enabled:               req.Enabled,
		IssuerUrl:             req.IssuerURL,
		ClientID:              req.ClientID,
		ClientSecretEncrypted: encSecret,
		RedirectUri:           req.RedirectURI,
		Scopes:                scopes,
		EmailClaim:            withDefault(req.EmailClaim, "email"),
		DisplayNameClaim:      withDefault(req.DisplayNameClaim, "name"),
		GroupsClaim:           withDefault(req.GroupsClaim, "groups"),
		GroupRoleMapping:      mappingJSON,
		DefaultRoleID:         defaultRoleID,
		AutoProvision:         req.AutoProvision,
		AllowedDomains:        allowedDomains,
		AllowPrivateAddress:   allowPrivate,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update OIDC config")
	}

	// Name a moved redirect_uri explicitly. Format validation cannot tell a
	// legitimate re-home from a caller pointing every login's authorization
	// code at a host they control (see validateOIDCRedirectURI), so the audit
	// row is what makes the change legible instead of silent.
	//
	// These ARE a widening: GET on this config needs manage:user, while
	// view:audit is held by every Viewer. Accepted because the values are an
	// issuer hostname and this install's own public origin, neither of which is
	// a secret — not because the audiences match. They do not.
	fields := map[string]any{"name": cfg.Name}
	if cfg.RedirectUri != existing.RedirectUri {
		fields["previous_redirect_uri"] = auditSafe(existing.RedirectUri)
		fields["new_redirect_uri"] = auditSafe(cfg.RedirectUri)
	}
	if cfg.IssuerUrl != existing.IssuerUrl {
		fields["previous_issuer_url"] = auditSafe(existing.IssuerUrl)
		fields["new_issuer_url"] = auditSafe(cfg.IssuerUrl)
	}
	// Recorded whenever the confirmation is or was held: a write that confirms it
	// again, grants it or drops it says so.
	if allowPrivate || existing.AllowPrivateAddress {
		fields["allow_private_address"] = allowPrivate
	}
	details, _ := json.Marshal(fields)
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "oidc", cfg.ID.String(), "oidc_config_updated", details)

	resp, err := h.configResponse(c.Context(), cfg)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "The OIDC config was updated; reading it back failed")
	}
	return c.JSON(resp)
}

// Delete handles DELETE /api/v1/oidc/configs/:id.
func (h *OIDCHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if err := h.queries.DeleteOIDCConfig(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete OIDC config")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "oidc", id.String(), "oidc_config_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}

// TestConnection handles POST /api/v1/oidc/configs/:id/test.
func (h *OIDCHandler) TestConnection(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	cfg, err := h.queries.GetOIDCConfig(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "OIDC config not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get OIDC config")
	}

	// Re-check the issuer before making the request, with the stored confirmation:
	// an unconfirmed private address is the 422 a save answers, which the test cannot
	// confirm itself; any other refusal is reported as the test's result.
	if _, err := checkOIDCIssuer(c.Context(), cfg.IssuerUrl, cfg.AllowPrivateAddress, oidcIssuerSaveToConfirm); err != nil {
		var pErr *addressPolicyError
		if errors.As(err, &pErr) && !pErr.HardReject {
			return renderAddressPolicyError(c, err)
		}
		return c.JSON(testConnectionResponse{
			Success: false,
			Message: "Issuer URL validation failed: " + errorMessage(err),
		})
	}

	// Try to fetch the OIDC discovery document using the address-checked client,
	// which checks the address again at the dial.
	discoveryURL := strings.TrimRight(cfg.IssuerUrl, "/") + "/.well-known/openid-configuration"
	client := notifications.SafeHTTPClientAllowingPrivate(10*time.Second, cfg.AllowPrivateAddress)
	resp, err := client.Get(discoveryURL) //nolint:gosec // URL comes from admin-configured issuer
	if err != nil {
		slog.Error("OIDC test connection failed", "config_id", id, "error", err)
		return c.JSON(testConnectionResponse{
			Success: false,
			Message: "Failed to reach OIDC discovery endpoint. Check the issuer URL and network connectivity.",
		})
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return c.JSON(testConnectionResponse{
			Success: false,
			Message: fmt.Sprintf("OIDC discovery endpoint returned HTTP %d", resp.StatusCode),
		})
	}

	return c.JSON(testConnectionResponse{
		Success: true,
		Message: "OIDC discovery endpoint is reachable and returned a valid response",
	})
}

// --- Auth flow endpoints (public, no auth required) ---

// Authorize handles GET /api/v1/auth/oidc/authorize.
// Returns the IdP redirect URL with state, nonce, PKCE.
func (h *OIDCHandler) Authorize(c fiber.Ctx, _ *apischema.Params) error {
	cfg, err := h.queries.GetEnabledOIDCConfig(c.Context())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "No enabled OIDC configuration")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load OIDC config")
	}

	oidcCfg, err := h.buildOIDCConfig(cfg)
	if err != nil {
		slog.Error("Failed to build OIDC config", "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to initialize OIDC configuration")
	}

	provider, err := auth.NewOIDCProvider(c.Context(), oidcCfg, h.rdb)
	if err != nil {
		slog.Error("OIDC provider init failed", "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to initialize OIDC provider")
	}

	secure := oidcBindingSecure(cfg.RedirectUri)
	secret, record, err := newBinding(secure)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate auth URL")
	}
	authURL, state, err := provider.GenerateAuthURL(c.Context(), record)
	if err != nil {
		// A refused authorization endpoint lands here as well as a Redis failure.
		slog.Error("OIDC authorize: could not generate the auth URL", "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to generate auth URL")
	}
	// The callback refuses a state this browser was not given (oidcStateBinding).
	oidcStateBinding.at(oidcCallbackCookiePath(cfg.RedirectUri)).set(c, state, secret, secure)

	return c.JSON(fiber.Map{"redirect_url": authURL})
}

// oidcNotThisBrowserMessage answers a callback, or an exchange, that this browser did
// not start: the login-CSRF refusal of browserBinding. Taking longer than the state
// lives, or starting at another address than the one the redirect URI names, are the
// honest ways to get it, so it says those too.
const oidcNotThisBrowserMessage = "The single sign-on could not be finished: it was not started in this browser, it took " +
	"too long, or it was started at a different address than this one. Start it again from the sign-in page."

// oidcCouldNotCompleteMessage answers a callback Nexara could not finish for a reason of
// its own, such as Redis not answering.
const oidcCouldNotCompleteMessage = "The single sign-on could not be completed. Try again."

// Callback handles GET /api/v1/auth/oidc/callback.
// Called by the IdP after authentication; exchanges code, provisions user,
// stores short-lived exchange code in Redis, and redirects to frontend.
//
// The browser that comes back must be the one the sign-in was started in: a state it
// holds no binding for (oidcStateBinding) — no cookie whose secret hashes to the digest
// stored with the state — is refused before anything else is looked at, and so is a
// state that is not stored at all. The binding is spent once the configuration says where its cookie lives (the
// redirect URI's path); a binding that does not match is left alone, as it may be the
// browser's own sign-in, still in flight.
//
// A state that a step-up stored (StartStepUp) is finished by finishStepUp and never
// as a sign-in, whatever the provider answers: its refusals, a binding that does not
// match included, are audited and sent back to the Security page.
func (h *OIDCHandler) Callback(c fiber.Ctx) error {
	code := c.Query("code")
	state := c.Query("state")
	binding, stepUp := "", ""
	if state != "" {
		var err error
		binding, stepUp, err = auth.OIDCStatePeek(c.Context(), h.rdb, state)
		if err != nil {
			slog.Error("oidc callback: could not read the sign-in's state", "error", err)
			return c.Redirect().Status(fiber.StatusFound).To("/login?error=" + url.QueryEscape(oidcCouldNotCompleteMessage))
		}
	}
	if code == "" || state == "" {
		if stepUp != "" {
			return stepUpRedirect(c, h.refuseStepUp(c, stepUp, "provider_error"))
		}
		errMsg := c.Query("error_description", c.Query("error", "unknown error"))
		return c.Redirect().Status(fiber.StatusFound).To("/login?error=" + url.QueryEscape("SSO failed: "+errMsg))
	}
	if !oidcStateBinding.matches(c, state, binding) {
		slog.Warn("oidc callback: refused a sign-in that was not started in this browser", "ip", c.IP())
		if stepUp != "" {
			// A step-up's state is spent: its callback URL, pasted somewhere, must not
			// be replayable by whoever holds the cookie. (A sign-in's is left: it may be
			// this browser's own sign-in, still in flight.)
			if err := auth.DiscardOIDCState(c.Context(), h.rdb, state); err != nil {
				slog.Warn("oidc callback: could not discard a refused step-up's state", "error", err)
			}
			return stepUpRedirect(c, h.refuseStepUp(c, stepUp, "another_browser"))
		}
		return c.Redirect().Status(fiber.StatusFound).To("/login?error=" + url.QueryEscape(oidcNotThisBrowserMessage))
	}
	// fail is where a refusal from here on goes: the login page with message, or the
	// Security page for a step-up.
	fail := func(message string) error {
		if stepUp != "" {
			return stepUpRedirect(c, h.refuseStepUp(c, stepUp, "not_completed"))
		}
		return c.Redirect().Status(fiber.StatusFound).To("/login?error=" + url.QueryEscape(message))
	}

	cfg, err := h.queries.GetEnabledOIDCConfig(c.Context())
	if err != nil {
		return fail("OIDC not configured")
	}
	oidcStateBinding.at(oidcCallbackCookiePath(cfg.RedirectUri)).spend(c, state, binding)

	oidcCfg, err := h.buildOIDCConfig(cfg)
	if err != nil {
		return fail("OIDC config error")
	}

	provider, err := auth.NewOIDCProvider(c.Context(), oidcCfg, h.rdb)
	if err != nil {
		slog.Error("OIDC provider init failed in callback", "error", err)
		return fail("OIDC provider error")
	}

	userInfo, err := provider.ExchangeAndVerify(c.Context(), code, state)
	if err != nil {
		slog.Error("OIDC exchange failed", "error", err)
		if stepUp != "" {
			return fail("")
		}
		return c.Redirect().Status(fiber.StatusFound).To("/login?error=" + url.QueryEscape(h.exchangeFailure(c, err)))
	}
	if stepUp != "" || userInfo.StepUp != "" {
		return stepUpRedirect(c, h.finishStepUp(c, stepUp, userInfo, oidcBindingSecure(cfg.RedirectUri)))
	}

	user, exchangeCode, failure := h.provisionAndStoreExchange(c, cfg, userInfo)
	if failure != "" {
		return c.Redirect().Status(fiber.StatusFound).To("/login?error=" + url.QueryEscape(failure))
	}

	// Through the shared path, so the row is published and forwarded to syslog like
	// every other: c.Locals has no user on a callback, hence AuditLogAs.
	details, _ := json.Marshal(map[string]string{"email": userInfo.Email, "ip": c.IP()})
	AuditLogAs(c, h.queries, h.eventPub, user.ID, pgtype.UUID{}, "auth", user.ID.String(), "oidc_callback", details)

	return c.Redirect().Status(fiber.StatusFound).To("/oidc-callback?oidc_token=" + exchangeCode)
}

// --- The step-up: a fresh sign-in at the provider by somebody signed in already ---
//
// A single sign-on account has no password Nexara could ask for before it hands out a
// second factor's secret (TOTPHandler.BeginSetup), so it proves instead that the person
// at this browser has just signed in at the identity provider again, as this account.
// StartStepUp sends the browser to the provider with prompt=login and max_age=0, the
// state bound to this browser exactly as a sign-in's is (oidcStateBinding) and carrying
// the session and the account that asked (stepUpRecord). The callback of such a state
// is never a sign-in: it creates no session, provisions and links nothing, and only
// records, for that session, a short-lived proof that the setup then uses up
// (finishStepUp). A thief holding a stolen session token can start one, but cannot
// finish it without signing in at the provider as the owner, in the browser that
// started it.
const (
	// totpStepUpPurpose is the one thing a step-up's proof is good for.
	totpStepUpPurpose = "totp_setup"
	// ssoStepUpWindow is how recent the ID token's auth_time must be, by the database's
	// clock; ssoStepUpSkew how far ahead of it the provider's clock may be.
	ssoStepUpWindow = 5 * time.Minute
	ssoStepUpSkew   = time.Minute
	// ssoStepUpProofTTL is how long the proof waits for the setup.
	ssoStepUpProofTTL = 5 * time.Minute
)

// The audit actions of a step-up's callback. Their details are the client address
// and, for a refusal, the class of it: never a claim of the ID token.
const (
	ssoStepUpConfirmedAction = "totp_step_up_confirmed"
	ssoStepUpRefusedAction   = "totp_step_up_refused"
)

// A refused step-up comes back to the Security page as a reason class, never as text:
// the page maps the classes it knows to its own words (a crafted link cannot put a
// sentence into Nexara's dialog). The classes, also the audit rows' reason:
// provider_error, another_browser, not_completed, malformed_state, account_not_checked,
// another_account, no_auth_time, not_recorded, stale_sign_in, session_ended.
// ssoStepUpPage is where a step-up's callback sends the browser back to.
const ssoStepUpPage = "/settings/security"

// stepUpRecord is what a step-up's state carries: the session and the account that
// asked for it.
func stepUpRecord(sessionID, userID uuid.UUID) string {
	return sessionID.String() + ":" + userID.String()
}

func parseStepUpRecord(record string) (sessionID, userID uuid.UUID, ok bool) {
	s, u, found := strings.Cut(record, ":")
	sid, err1 := uuid.Parse(s)
	uid, err2 := uuid.Parse(u)
	if !found || err1 != nil || err2 != nil || sid == uuid.Nil || uid == uuid.Nil {
		return uuid.Nil, uuid.Nil, false
	}
	return sid, uid, true
}

// StartStepUp begins a step-up for the session sessionID of userID and returns the
// provider's URL to send the browser to. It sets the state's browser binding as
// Authorize does. Its error is the answer the route ends in.
func (h *OIDCHandler) StartStepUp(c fiber.Ctx, userID, sessionID uuid.UUID) (string, error) {
	cfg, err := h.queries.GetEnabledOIDCConfig(c.Context())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fiber.NewError(fiber.StatusNotFound, "No enabled OIDC configuration")
		}
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to load OIDC config")
	}
	oidcCfg, err := h.buildOIDCConfig(cfg)
	if err != nil {
		slog.Error("OIDC step-up: could not build the configuration", "error", err)
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to initialize OIDC configuration")
	}
	provider, err := auth.NewOIDCProvider(c.Context(), oidcCfg, h.rdb)
	if err != nil {
		slog.Error("OIDC step-up: provider init failed", "error", err)
		return "", fiber.NewError(fiber.StatusServiceUnavailable, "The identity provider could not be reached. Try again shortly.")
	}
	secure := oidcBindingSecure(cfg.RedirectUri)
	secret, record, err := newBinding(secure)
	if err != nil {
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to generate auth URL")
	}
	authURL, state, err := provider.GenerateStepUpURL(c.Context(), record, stepUpRecord(sessionID, userID))
	if err != nil {
		slog.Error("OIDC step-up: could not generate the auth URL", "error", err)
		return "", fiber.NewError(fiber.StatusInternalServerError, "Failed to generate auth URL")
	}
	oidcStateBinding.at(oidcCallbackCookiePath(cfg.RedirectUri)).set(c, state, secret, secure)
	return authURL, nil
}

// ssoStepUpIsSameAccount decides whether the identity the provider vouched for in a
// step-up is the account that asked for it: the account a sign-in with this ID token
// would resolve to must be that one. Today a sign-in resolves an SSO account by its
// email claim among the oidc accounts (provisionUser's GetUserByEmailAndSource); this is
// the one place the step-up asks, so it follows the sign-in's rule when that changes.
func (h *OIDCHandler) ssoStepUpIsSameAccount(ctx context.Context, userID uuid.UUID, info *auth.OIDCUserInfo) (bool, error) {
	u, err := h.queries.GetUserByEmailAndSource(ctx, db.GetUserByEmailAndSourceParams{Email: info.Email, AuthSource: "oidc"})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up the step-up's account: %w", err)
	}
	return u.ID == userID, nil
}

// finishStepUp is the step-up callback once the provider has vouched for info: it
// records the proof for the session the state names, or refuses. It answers "" when the
// proof was recorded, or the refusal's reason class. Every outcome is audited.
// It is split out of Callback, like provisionAndStoreExchange, so a test can drive it.
//
// The proof is bound to this browser as well as to the session: a random secret goes
// into the browser's step-up cookie (setTOTPStepUpCookie, Secure as secure says) and only
// its record into the proof, so a stolen token of the same session cannot use it.
func (h *OIDCHandler) finishStepUp(c fiber.Ctx, stepUp string, info *auth.OIDCUserInfo, secure bool) string {
	sessionID, userID, ok := parseStepUpRecord(stepUp)
	if !ok || info.StepUp != stepUp {
		return h.refuseStepUp(c, stepUp, "malformed_state")
	}
	same, err := h.ssoStepUpIsSameAccount(c.Context(), userID, info)
	switch {
	case err != nil:
		slog.Error("OIDC step-up: could not check the account", "user_id", userID, "error", err)
		return h.refuseStepUp(c, stepUp, "account_not_checked")
	case !same:
		return h.refuseStepUp(c, stepUp, "another_account")
	case info.AuthTime.IsZero():
		return h.refuseStepUp(c, stepUp, "no_auth_time")
	}
	authTime := float64(info.AuthTime.Unix())
	secret, record, err := newBinding(secure)
	if err != nil {
		slog.Error("OIDC step-up: could not make the browser binding", "user_id", userID, "error", err)
		return h.refuseStepUp(c, stepUp, "not_recorded")
	}
	n, err := h.queries.RecordTOTPStepUpProof(c.Context(), db.RecordTOTPStepUpProofParams{
		Purpose: totpStepUpPurpose, Binding: record, TtlSeconds: ssoStepUpProofTTL.Seconds(), SessionID: sessionID, UserID: userID,
		AuthTime: authTime, WindowSeconds: ssoStepUpWindow.Seconds(), SkewSeconds: ssoStepUpSkew.Seconds(),
	})
	if err != nil {
		slog.Error("OIDC step-up: could not record the proof", "user_id", userID, "error", err)
		return h.refuseStepUp(c, stepUp, "not_recorded")
	}
	if n == 0 {
		fresh, ferr := h.queries.TOTPStepUpAuthTimeIsFresh(c.Context(), db.TOTPStepUpAuthTimeIsFreshParams{
			AuthTime: authTime, WindowSeconds: ssoStepUpWindow.Seconds(), SkewSeconds: ssoStepUpSkew.Seconds(),
		})
		if ferr == nil && !fresh {
			return h.refuseStepUp(c, stepUp, "stale_sign_in")
		}
		return h.refuseStepUp(c, stepUp, "session_ended")
	}
	setTOTPStepUpCookie(c, secret, secure, ssoStepUpProofTTL)
	details, _ := json.Marshal(map[string]string{"ip": c.IP(), "method": "oidc"})
	AuditLogAs(c, h.queries, h.eventPub, userID, pgtype.UUID{}, "auth", userID.String(), ssoStepUpConfirmedAction, details)
	return ""
}

// refuseStepUp audits a refused step-up under the account its state names (none when
// it names none), logs the class, and returns it.
func (h *OIDCHandler) refuseStepUp(c fiber.Ctx, stepUp, reason string) string {
	_, userID, _ := parseStepUpRecord(stepUp)
	slog.Warn("OIDC step-up: refused", "user_id", userID, "ip", c.IP(), "reason", reason)
	details, _ := json.Marshal(map[string]string{"ip": c.IP(), "method": "oidc", "reason": reason})
	target := ""
	if userID != uuid.Nil {
		target = userID.String()
	}
	AuditLogAs(c, h.queries, h.eventPub, userID, pgtype.UUID{}, "auth", target, ssoStepUpRefusedAction, details)
	return reason
}

// stepUpRedirect sends the browser back to the Security page with the step-up's
// outcome: confirmed when reason is "", else the refusal's reason class.
func stepUpRedirect(c fiber.Ctx, reason string) error {
	if reason == "" {
		return c.Redirect().Status(fiber.StatusFound).To(ssoStepUpPage + "?sso_step_up=confirmed")
	}
	return c.Redirect().Status(fiber.StatusFound).To(ssoStepUpPage + "?sso_step_up_error=" + url.QueryEscape(reason))
}

// oidcEmailNotVerifiedMessage answers a sign-in whose identity provider says it has
// not verified the account's email address.
const oidcEmailNotVerifiedMessage = "Your identity provider has not verified your email address. " +
	"Verify it with your provider, then sign in again."

// exchangeFailure is the message the login page shows for a callback whose exchange
// with the identity provider failed. An unverified email address is audited as a
// failed sign-in, as a password sign-in's is (recordFailedSignIn): under the SSO
// account the address names, with the account's own address; or, when there is no
// such account, as login_failed_unknown_account with no address at all — audit
// details are readable by every Viewer — bounded per client address like the
// password path's (unknownSignInDetails).
func (h *OIDCHandler) exchangeFailure(c fiber.Ctx, err error) string {
	var unverified *auth.OIDCEmailNotVerifiedError
	switch {
	case errors.As(err, &unverified):
		account, lookupErr := h.queries.GetUserByEmailAndSource(c.Context(), db.GetUserByEmailAndSourceParams{
			Email: unverified.Email, AuthSource: "oidc"})
		if lookupErr == nil {
			details, _ := json.Marshal(map[string]string{"email": account.Email, "ip": c.IP(),
				"method": "oidc", "reason": "email_not_verified"})
			AuditLogAs(c, h.queries, h.eventPub, account.ID, pgtype.UUID{}, "auth", account.ID.String(), "login_failed", details)
		} else if h.rdb != nil {
			if details, audit := unknownSignInDetails(c, h.rdb, "oidc", "email_not_verified"); audit {
				AuditLogAs(c, h.queries, h.eventPub, uuid.Nil, pgtype.UUID{}, "auth", "", "login_failed_unknown_account", details)
			}
		}
		return oidcEmailNotVerifiedMessage
	case errors.Is(err, auth.ErrOIDCDomainNotAllowed):
		return "Your email domain is not allowed"
	default:
		return "Authentication failed"
	}
}

// provisionAndStoreExchange is Callback once the identity provider has vouched for
// userInfo: it provisions or updates the account, syncs its roles from the groups,
// and stores the one-time exchange code the browser will trade for tokens — with the
// epoch of the account the PROVISIONING returned (storeExchange). It returns the
// account and the code, or, when it could not, the message the login page shows in
// place of both.
//
// It is split out so that the hand-off the whole SSO guarantee rests on — the user
// provisionUser read or created is the user whose epoch the code records — is
// something a test can drive: Callback itself needs an identity provider.
func (h *OIDCHandler) provisionAndStoreExchange(c fiber.Ctx, cfg db.OidcConfig, userInfo *auth.OIDCUserInfo) (user db.User, exchangeCode, failure string) {
	// JIT provision, or find the account; then, unless a check after the provider can
	// still refuse this sign-in, link it and bring its roles and name up to date.
	user, finish, err := h.provisionUser(c, cfg, userInfo)
	if err == nil && finish {
		user, err = h.finishOIDCSignIn(c, cfg, user, userInfo)
	}
	if errors.Is(err, errOIDCIdentityMismatch) {
		return db.User{}, "", oidcIdentityMismatchMessage
	}
	if err != nil {
		slog.Error("OIDC user provisioning failed", "email", userInfo.Email, "error", err)
		return db.User{}, "", "User provisioning failed"
	}

	// Store short-lived exchange code in Redis (5 second TTL), with the digest of the
	// browser binding's secret: the exchange refuses a code this browser was not given
	// (oidcExchangeBinding).
	secure := oidcBindingSecure(cfg.RedirectUri)
	secret, record, err := newBinding(secure)
	if err == nil {
		exchangeCode, err = h.storeExchange(c.Context(), user, record)
	}
	if err != nil {
		slog.Error("OIDC callback: could not store the exchange code", "user_id", user.ID, "error", err)
		return db.User{}, "", "Internal error"
	}
	oidcExchangeBinding.set(c, exchangeCode, secret, secure)
	return user, exchangeCode, ""
}

// oidcExchangeTTL is how long the code Callback redirects the browser with can be
// traded for tokens.
const oidcExchangeTTL = 5 * time.Second

// storeExchange mints the single-use code the browser trades for tokens
// (OIDCTokenExchange) and records, under it, who it is for and the auth epoch of
// that user as it stands NOW — the one on the row provisionUser has just read or
// created, after the identity provider vouched for the user, which is the
// credential check of an SSO sign-in. The exchange creates its session against this
// value and not against whatever the user's row holds when the browser comes back
// with the code, so that a password change, a sign-out of all devices or a
// deactivation that lands in between refuses the session instead of being
// outlived by it. binding is the record of the secret the browser's binding
// cookie holds (oidcExchangeBinding), kept under oidcExchangeBindingKey.
func (h *OIDCHandler) storeExchange(ctx context.Context, user db.User, binding string) (string, error) {
	code, err := auth.GenerateRandomString(32)
	if err != nil {
		return "", fmt.Errorf("generating the exchange code: %w", err)
	}
	data, _ := json.Marshal(map[string]string{
		"user_id":              user.ID.String(),
		oidcExchangeEpochKey:   strconv.FormatInt(user.AuthEpoch, 10),
		oidcExchangeBindingKey: binding,
	})
	if err := h.rdb.Set(ctx, "oidc:exchange:"+code, string(data), oidcExchangeTTL).Err(); err != nil {
		return "", fmt.Errorf("storing the exchange code: %w", err)
	}
	return code, nil
}

// --- User provisioning ---

// errOIDCIdentityMismatch refuses a sign-in whose address names an SSO account that is
// bound to another provider identity: another subject, or another issuer.
var errOIDCIdentityMismatch = errors.New("oidc: the account with this address is bound to another identity")

// oidcIdentityMismatchMessage answers that refusal on the login page. It names no
// subject, no issuer and no address: the person learns what to do, an attacker nothing.
const oidcIdentityMismatchMessage = "This sign-in does not match the single sign-on identity your account is " +
	"linked to. Contact an administrator."

// oidcAccount is accountForIdentity's answer to "which account is this identity".
type oidcAccount struct {
	// User is the account: the zero value when Found is false.
	User db.User
	// Found: an SSO account answers to this identity.
	Found bool
	// Bound: it answers by the issuer and subject stored on it. Found but not Bound: by
	// its address, an SSO account with no link stored yet (its first sign-in since the
	// upgrade that added links, or since an administrator reset its link).
	Bound bool
}

// accountForIdentity answers which Nexara account a provider identity is, and writes
// nothing:
//   - the SSO account bound to info's issuer and subject, whatever its address says
//     now (Found, Bound);
//   - else the SSO account info's address names (GetUserByEmailAndSource: exact
//     spelling first, else the one differing only by case), when it has no link stored
//     (Found, not Bound): a sign-in links it before it signs in (linkOIDCAccount);
//   - else none (not Found, nil error).
//
// An address that names an SSO account bound to another identity is
// errOIDCIdentityMismatch, with that account in User for the audit row. An account of
// another source is never an answer, and an identity without an issuer or a subject
// is an error. The caller must have had the identity verified by the provider.
func (h *OIDCHandler) accountForIdentity(ctx context.Context, info *auth.OIDCUserInfo) (oidcAccount, error) {
	if info.Issuer == "" || info.Subject == "" {
		return oidcAccount{}, fmt.Errorf("oidc: the identity has no issuer or subject")
	}
	user, err := h.queries.GetUserByOIDCIdentity(ctx, db.GetUserByOIDCIdentityParams{
		OidcIssuer: info.Issuer, OidcSubject: info.Subject})
	if err == nil {
		return oidcAccount{User: user, Found: true, Bound: true}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return oidcAccount{}, fmt.Errorf("lookup user by identity: %w", err)
	}
	user, err = h.queries.GetUserByEmailAndSource(ctx, db.GetUserByEmailAndSourceParams{
		Email: info.Email, AuthSource: "oidc"})
	if errors.Is(err, pgx.ErrNoRows) {
		return oidcAccount{}, nil
	}
	if err != nil {
		return oidcAccount{}, fmt.Errorf("lookup user: %w", err)
	}
	if user.OidcSubject != "" || user.OidcIssuer != "" {
		return oidcAccount{User: user}, errOIDCIdentityMismatch
	}
	return oidcAccount{User: user, Found: true}, nil
}

// provisionUser is the account a verified sign-in signs in to: the one
// accountForIdentity names, or a new one bound to the identity when auto-provisioning
// is on. finish says the caller is to finish the sign-in for it now
// (finishOIDCSignIn): always, except for an account found by address that a check
// after the provider can still refuse — one with a second factor, or a deactivated one
// — which is left exactly as it is, unlinked, with its roles and name untouched. A
// sign-in whose address names an account bound to another identity is refused
// (errOIDCIdentityMismatch) and audited as a failed sign-in under that account.
func (h *OIDCHandler) provisionUser(c fiber.Ctx, cfg db.OidcConfig, userInfo *auth.OIDCUserInfo) (user db.User, finish bool, err error) {
	acct, err := h.accountForIdentity(c.Context(), userInfo)
	if errors.Is(err, errOIDCIdentityMismatch) {
		h.auditIdentityMismatch(c, acct.User)
		return db.User{}, false, err
	}
	if err != nil {
		return db.User{}, false, err
	}
	if acct.Found {
		return acct.User, acct.Bound || acct.User.IsActive && !acct.User.TotpSecret.Valid, nil
	}

	// User not found — JIT provision if enabled
	if !cfg.AutoProvision {
		return db.User{}, false, fmt.Errorf("auto-provisioning disabled and user not found")
	}

	displayName := userInfo.DisplayName
	if displayName == "" {
		displayName = userInfo.Email
	}

	// A new account must not differ from an existing one only by case: refused like the
	// duplicate key below, by the same second lookup, as provisionLDAPUser does.
	taken, err := h.queries.EmailTakenIgnoringCase(c.Context(), userInfo.Email)
	if err != nil {
		return db.User{}, false, fmt.Errorf("check the address: %w", err)
	}
	if taken {
		err = errEmailTakenInAnotherCase
	} else {
		user, err = h.queries.CreateOIDCUser(c.Context(), db.CreateOIDCUserParams{
			Email:       userInfo.Email,
			DisplayName: displayName,
			OidcIssuer:  userInfo.Issuer,
			OidcSubject: userInfo.Subject,
		})
	}
	if err != nil {
		// Handle race condition: another sign-in created the account in the meantime.
		if isDuplicateKeyError(err) || errors.Is(err, errEmailTakenInAnotherCase) {
			user, err = h.boundAccountAfterRace(c, userInfo)
			return user, err == nil, err
		}
		return db.User{}, false, fmt.Errorf("create user: %w", err)
	}
	return user, true, nil
}

// finishOIDCSignIn finishes an SSO sign-in for user, the account the identity info
// signs in to, once nothing can refuse that sign-in any more: the callback calls it
// for an account with no later check, and the second-factor step can call it once the
// code has passed. It links the account to the identity if it has no link
// (LinkOIDCIdentity, conditional in SQL), refusing with errOIDCIdentityMismatch an
// account bound — then, or by a sign-in that won a race — to any other identity; then
// replaces its roles from info.Groups by cfg's mapping and takes info.DisplayName. It
// needs from the provider only info.Issuer, Subject, Groups and DisplayName; the
// address is never rewritten. It returns the account as last written.
func (h *OIDCHandler) finishOIDCSignIn(c fiber.Ctx, cfg db.OidcConfig, user db.User, info *auth.OIDCUserInfo) (db.User, error) {
	if info.Issuer == "" || info.Subject == "" {
		return db.User{}, fmt.Errorf("oidc: the identity has no issuer or subject")
	}
	if user.OidcSubject == "" && user.OidcIssuer == "" {
		linked, err := h.linkOIDCAccount(c, user, info)
		if err != nil {
			return db.User{}, err
		}
		if linked.ID != user.ID {
			// The identity was bound to another account while this one waited.
			h.auditIdentityMismatch(c, user)
			return db.User{}, errOIDCIdentityMismatch
		}
		user = linked
	} else if user.OidcIssuer != info.Issuer || user.OidcSubject != info.Subject {
		h.auditIdentityMismatch(c, user)
		return db.User{}, errOIDCIdentityMismatch
	}

	mapping := make(map[string]string)
	if len(cfg.GroupRoleMapping) > 0 {
		_ = json.Unmarshal(cfg.GroupRoleMapping, &mapping)
	}
	h.syncUserRoles(c, user.ID, info.Groups, mapping, cfg.DefaultRoleID)

	// The address is not rewritten from the token: a bound account keeps its own.
	if info.DisplayName != "" && info.DisplayName != user.DisplayName {
		if updated, err := h.queries.UpdateOIDCUserProfile(c.Context(), db.UpdateOIDCUserProfileParams{
			ID:          user.ID,
			DisplayName: info.DisplayName,
		}); err == nil {
			user = updated
		}
	}
	return user, nil
}

// linkOIDCAccount binds an unlinked SSO account to the identity signing in to it. The
// write is conditional in SQL (LinkOIDCIdentity: only while no link is stored), so when
// another sign-in got there first what is stored then decides (boundAccountAfterRace).
func (h *OIDCHandler) linkOIDCAccount(c fiber.Ctx, user db.User, info *auth.OIDCUserInfo) (db.User, error) {
	linked, err := h.queries.LinkOIDCIdentity(c.Context(), db.LinkOIDCIdentityParams{
		ID: user.ID, OidcIssuer: info.Issuer, OidcSubject: info.Subject})
	if err == nil {
		return linked, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) && !isDuplicateKeyError(err) {
		return db.User{}, fmt.Errorf("link the account: %w", err)
	}
	return h.boundAccountAfterRace(c, info)
}

// boundAccountAfterRace settles a link or a creation that lost a race with another
// sign-in: the identity signs in only to an account now bound to it. An address now
// naming an account bound to another identity is refused as accountForIdentity's
// mismatch; anything else is an error.
func (h *OIDCHandler) boundAccountAfterRace(c fiber.Ctx, info *auth.OIDCUserInfo) (db.User, error) {
	acct, err := h.accountForIdentity(c.Context(), info)
	if errors.Is(err, errOIDCIdentityMismatch) {
		h.auditIdentityMismatch(c, acct.User)
		return db.User{}, err
	}
	if err != nil {
		return db.User{}, fmt.Errorf("lookup user after race: %w", err)
	}
	if !acct.Bound {
		return db.User{}, fmt.Errorf("lookup user after race: no account is bound to the identity")
	}
	return acct.User, nil
}

// auditIdentityMismatch records a sign-in refused for a mismatched identity as a failed
// sign-in under the account concerned, with that account's own address and a fixed
// reason: nothing the provider asserted (audit details are readable by every Viewer).
func (h *OIDCHandler) auditIdentityMismatch(c fiber.Ctx, account db.User) {
	slog.Warn("oidc callback: refused a sign-in whose identity does not match the account's link", "user_id", account.ID)
	details, _ := json.Marshal(map[string]string{"email": account.Email, "ip": c.IP(),
		"method": "oidc", "reason": "identity_mismatch"})
	AuditLogAs(c, h.queries, h.eventPub, account.ID, pgtype.UUID{}, "auth", account.ID.String(), "login_failed", details)
}

// syncUserRoles maps OIDC group names to RBAC role UUIDs and assigns them.
func (h *OIDCHandler) syncUserRoles(c fiber.Ctx, userID uuid.UUID, groups []string, mapping map[string]string, defaultRoleID pgtype.UUID) {
	_ = h.queries.RevokeAllUserRoles(c.Context(), userID)

	assigned := false
	for _, group := range groups {
		roleIDStr, ok := mapping[group]
		if !ok {
			continue
		}
		roleID, err := uuid.Parse(roleIDStr)
		if err != nil {
			continue
		}
		if _, err := h.queries.AssignUserRole(c.Context(), db.AssignUserRoleParams{
			UserID:    userID,
			RoleID:    roleID,
			ScopeType: "global",
		}); !mappedRoleAssigned(err, "oidc", userID, roleID) {
			continue
		}
		assigned = true
	}

	if !assigned && defaultRoleID.Valid {
		defID, _ := uuid.FromBytes(defaultRoleID.Bytes[:])
		_, _ = h.queries.AssignUserRole(c.Context(), db.AssignUserRoleParams{
			UserID:    userID,
			RoleID:    defID,
			ScopeType: "global",
		})
	}

	if h.rbac != nil {
		h.rbac.InvalidateUser(c.Context(), userID)
	}
}

// --- Helpers ---

func (h *OIDCHandler) buildOIDCConfig(cfg db.OidcConfig) (auth.OIDCConfig, error) {
	clientSecret := ""
	if cfg.ClientSecretEncrypted != "" {
		var err error
		clientSecret, err = crypto.Decrypt(cfg.ClientSecretEncrypted, h.encryptionKey)
		if err != nil {
			return auth.OIDCConfig{}, fmt.Errorf("decrypt client secret: %w", err)
		}
	}

	return auth.OIDCConfig{
		IssuerURL:        cfg.IssuerUrl,
		ClientID:         cfg.ClientID,
		ClientSecret:     clientSecret,
		RedirectURI:      cfg.RedirectUri,
		Scopes:           cfg.Scopes,
		EmailClaim:       cfg.EmailClaim,
		DisplayNameClaim: cfg.DisplayNameClaim,
		GroupsClaim:      cfg.GroupsClaim,
		AllowedDomains:   cfg.AllowedDomains,

		AllowPrivateAddress: cfg.AllowPrivateAddress,
	}, nil
}

// confirmOIDCIssuerChange is the code the admin page keys on to warn that moving the
// issuer leaves the accounts linked under the old one refused.
const confirmOIDCIssuerChange = "oidc_issuer_change_confirm_required"

// requireOIDCIssuerChangeAck asks for a confirmation before a configuration's issuer
// moves while linked accounts are bound to the current one: each of them answers to its
// old issuer's identity only, so it is refused at its next sign-in until an
// administrator resets its link. The safe default is kept; the confirmation makes it a
// decision rather than a surprise.
func requireOIDCIssuerChangeAck(linked int64) error {
	if linked == 0 {
		return nil
	}
	return &confirmRequiredError{
		Code: confirmOIDCIssuerChange,
		Message: fmt.Sprintf("%d account(s) are linked to the current issuer. After the change each of them is "+
			"refused at single sign-on until its link is reset.", linked),
		AcknowledgeField: "acknowledge_issuer_change",
		Fields:           map[string]any{"linked_accounts": linked},
	}
}

// confirmInsecureOIDCRedirect is the code the admin page keys on to turn the
// cleartext-callback refusal into a prompt.
const confirmInsecureOIDCRedirect = "insecure_oidc_redirect_confirm_required"

// requireOIDCRedirectSchemeAck refuses a cleartext callback unless the operator
// says so. The authorization code rides back on this URL, so over anything but
// loopback it is readable by whoever is on the wire.
//
// Confirm-and-proceed rather than a refusal: an install genuinely served over
// plain http on a lab network has no https origin to name, and the admin page
// derives this value from window.location.origin — a hard block would leave it
// unable to save the URI its own UI generated. Fails closed, which the earlier
// c.Scheme() version did not.
//
// Takes no fiber.Ctx: gates decide, renderers respond. See confirm_gate.go.
func requireOIDCRedirectSchemeAck(rawURL string, acknowledged bool) error {
	if acknowledged {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		// Self-sufficient rather than relying on validateOIDCRedirectURI having
		// run first: an unparseable URI cannot be shown to be safe, and the
		// ordering between the two is not asserted anywhere.
		return &confirmRequiredError{
			Code:             confirmInsecureOIDCRedirect,
			Message:          "This callback could not be parsed, so it cannot be shown to be encrypted.",
			AcknowledgeField: "acknowledge_insecure_redirect",
			Fields:           map[string]any{"redirect_scheme": "unknown"},
		}
	}
	if u.Scheme != "http" {
		return nil
	}
	// Over the loopback interface the code never leaves the machine.
	if host := u.Hostname(); host == "localhost" || net.ParseIP(host).IsLoopback() {
		return nil
	}
	return &confirmRequiredError{
		Code: confirmInsecureOIDCRedirect,
		Message: "This callback is plain http, so every login's authorization code crosses " +
			"the network in the clear. Use https unless this install has no TLS at all.",
		AcknowledgeField: "acknowledge_insecure_redirect",
		Fields:           map[string]any{"redirect_scheme": "http"},
	}
}

// oidcCallbackPath is the route Nexara actually serves for the OIDC callback
// (see router.go). A redirect_uri that does not end here cannot complete a
// login against this install, so it is either a misconfiguration or an attempt
// to send the authorization code somewhere else.
const oidcCallbackPath = "/api/v1/auth/oidc/callback"

// validateOIDCRedirectURI checks the address the IdP sends the browser back
// to, carrying the authorization code. It was previously written to the
// database with no validation at all.
//
// What this DOES catch: cleartext delivery of the code, credentials or a
// fragment smuggled into the URI (RFC 6749 §3.1.2 forbids the fragment
// outright), and any path that is not the callback this install serves.
//
// What it CANNOT catch, and no format check could: a well-formed URI on a host
// the caller controls — https://attacker.example/api/v1/auth/oidc/callback
// satisfies every rule here. Closing that needs an authoritative public origin
// for the install, which Nexara does not have server-side (the value is
// correct only relative to whatever the reverse proxy publishes; the admin UI
// derives it from window.location.origin in the browser). The residual risk is
// bounded — redeeming a stolen code still needs the client secret, which
// credential_redirect.go now protects — and the change is audited, so
// re-pointing it is at least legible after the fact rather than silent.
//
// http on loopback is always fine; http anywhere else is a separate,
// overridable refusal — see requireOIDCRedirectSchemeAck. Deriving that from
// the request was tried and removed: c.Scheme() only reports https when
// TRUSTED_PROXIES is set AND the proxy sends X-Forwarded-Proto, so on the
// documented default it reads "http" for every request and silently turned the
// rule off on exactly the deployments it was written for.
func validateOIDCRedirectURI(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid redirect URI: %w", err)
	}
	if u.Scheme == "" || u.Host == "" || u.Hostname() == "" {
		// Host alone is not enough: "https://:8080/..." parses with a non-empty
		// Host and an empty Hostname.
		return fmt.Errorf("redirect_uri must be an absolute URL including scheme and host")
	}
	if u.User != nil {
		return fmt.Errorf("redirect_uri must not contain credentials")
	}
	if u.Fragment != "" || strings.Contains(rawURL, "#") {
		return fmt.Errorf("redirect_uri must not contain a fragment")
	}

	// Only http and https are ever a callback. The cleartext question is
	// handled separately, because it has a legitimate answer on a lab install
	// and this function's other rules do not.
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("redirect_uri must use https")
	}

	// Tolerate a reverse proxy serving Nexara under a path prefix, which makes
	// the published callback ".../nexara/api/v1/auth/oidc/callback".
	//
	// EscapedPath, not Path: Path is decoded, so ".../oidc%2Fcallback" — one
	// segment naming a different resource — would satisfy a check written
	// against the decoded form.
	if !strings.HasSuffix(strings.TrimSuffix(u.EscapedPath(), "/"), oidcCallbackPath) {
		return fmt.Errorf("redirect_uri must end in %s — that is the callback this install serves", oidcCallbackPath)
	}
	return nil
}

// What a refusal of an unconfirmed private issuer tells the caller to do: a save
// resends with the confirmation; a test of the stored configuration cannot give one.
const (
	oidcIssuerResubmit      = "Re-submit with allow_private_address=true to confirm it for this configuration."
	oidcIssuerSaveToConfirm = "Save the configuration with its private address confirmed, then test it."
)

// checkOIDCIssuer checks the issuer URL of an OIDC configuration: https, a host, and
// the address the host resolves to, by the notification channels' rule
// (notifications.CheckTargetHost). It returns the confirmation to store — true only
// for a private issuer the caller confirmed — or the refusal for
// renderAddressPolicyError: a private address not confirmed (422
// private_address_confirm_required, its address in details, with todo as the way
// out), an address no configuration may use — loopback, link-local (the cloud
// metadata addresses included), unspecified, or one with a zone — or a name that does
// not resolve (400). Every contact with the provider checks it again at the dial
// (auth.NewOIDCProvider).
func checkOIDCIssuer(ctx context.Context, rawURL string, confirmed bool, todo string) (bool, error) {
	if !strings.HasPrefix(rawURL, "https://") {
		return false, fiber.NewError(fiber.StatusBadRequest, "issuer_url must use https:// scheme")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false, fiber.NewError(fiber.StatusBadRequest, "invalid issuer URL")
	}
	host := u.Hostname()
	if host == "" {
		return false, fiber.NewError(fiber.StatusBadRequest, "issuer_url must include a hostname")
	}
	private, err := notifications.CheckTargetHost(ctx, host, confirmed)
	var refused *notifications.TargetAddressError
	switch {
	case errors.As(err, &refused) && refused.Unreadable:
		return false, &addressPolicyError{HardReject: true, IP: refused.IP, Reason: "The issuer URL has an " +
			"address with a zone, or one that cannot be read, which an OIDC issuer may never use."}
	case errors.As(err, &refused) && !refused.Overridable:
		return false, &addressPolicyError{HardReject: true, IP: refused.IP, Reason: fmt.Sprintf(
			"The issuer URL resolves to %s, a loopback, link-local or unspecified address, "+
				"which an OIDC issuer may never use.", refused.IP)}
	case errors.As(err, &refused):
		return false, &addressPolicyError{IP: refused.IP, Reason: fmt.Sprintf(
			"The issuer URL resolves to %s, an address on a private network. %s", refused.IP, todo)}
	case err != nil:
		return false, fiber.NewError(fiber.StatusBadRequest, "cannot resolve issuer hostname")
	}
	return private && confirmed, nil
}

// errorMessage is err's message for the caller: a fiber error's own, else err's.
func errorMessage(err error) string {
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fe.Message
	}
	return err.Error()
}
