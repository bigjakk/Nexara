package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/veeam"
)

// VeeamHandler serves the Veeam Backup & Replication server registry.
//
// Every route here is gated on GLOBAL view/manage/delete:veeam rather than
// per-cluster, because one Veeam server can protect N Proxmox clusters —
// unlike a PBS server, which maps to at most one. Knowing a Veeam server
// exists therefore reveals infrastructure spanning clusters the caller may not
// otherwise see, so the registry itself is a global-scope resource. The
// per-cluster scoping the plan calls for applies to the data those servers
// produce (jobs, sessions, restore points), which lands in Phase 2 keyed on
// veeam_platforms.platform_id.
type VeeamHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewVeeamHandler creates a Veeam server handler.
func NewVeeamHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *VeeamHandler {
	return &VeeamHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
	}
}

// probeTimeout bounds a connection test. Generous relative to the three calls
// it makes, because a first contact often pays a DNS and TLS-handshake cost
// against a server that is not warm.
const veeamProbeTimeout = 45 * time.Second

type createVeeamRequest struct {
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Username string `json:"username"`
	Password string `json:"password"`
	// TLSFingerprint pins the leaf certificate. Empty means verify against
	// the system CA pool.
	TLSFingerprint string `json:"tls_fingerprint"`
	// VerifyTLS defaults to true when omitted.
	VerifyTLS *bool `json:"verify_tls"`
	// AcknowledgeInsecureTLS is required to store a credential against a host
	// whose certificate will not be verified at all.
	AcknowledgeInsecureTLS bool `json:"acknowledge_insecure_tls,omitempty"`
	AllowPrivateAddress    bool `json:"allow_private_address,omitempty"`
}

type updateVeeamRequest struct {
	Name           *string `json:"name"`
	BaseURL        *string `json:"base_url"`
	Username       *string `json:"username"`
	Password       *string `json:"password"`
	TLSFingerprint *string `json:"tls_fingerprint"`
	VerifyTLS      *bool   `json:"verify_tls"`
	Enabled        *bool   `json:"enabled"`
	// AcknowledgeInsecureTLS is required to remove a certificate pin or turn
	// verification off on a server that already has one.
	AcknowledgeInsecureTLS bool `json:"acknowledge_insecure_tls,omitempty"`
	AllowPrivateAddress    bool `json:"allow_private_address,omitempty"`
}

// veeamServerResponse is the only shape a Veeam server is ever serialized in.
// It has no password field at all — not an empty one, not a redacted one —
// so no future edit can accidentally populate it.
type veeamServerResponse struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	BaseURL        string     `json:"base_url"`
	Username       string     `json:"username"`
	APIRevision    string     `json:"api_revision"`
	ProductVersion string     `json:"product_version"`
	LicenseEdition string     `json:"license_edition"`
	TLSFingerprint string     `json:"tls_fingerprint"`
	VerifyTLS      bool       `json:"verify_tls"`
	Enabled        bool       `json:"enabled"`
	LastSyncAt     *time.Time `json:"last_sync_at"`
	LastSyncError  string     `json:"last_sync_error"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// toVeeamResponse maps a stored row to its wire shape. includeUsername is
// false for read-only callers — see callerSeesUsername.
func toVeeamResponse(s db.VeeamServer, includeUsername bool) veeamServerResponse {
	username := s.Username
	if !includeUsername {
		username = ""
	}
	resp := veeamServerResponse{
		ID:             s.ID,
		Name:           s.Name,
		BaseURL:        s.BaseUrl,
		Username:       username,
		APIRevision:    s.ApiRevision,
		ProductVersion: s.ProductVersion,
		LicenseEdition: s.LicenseEdition,
		TLSFingerprint: s.TlsFingerprint,
		VerifyTLS:      s.VerifyTls,
		Enabled:        s.Enabled,
		LastSyncError:  s.LastSyncError,
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
	if s.LastSyncAt.Valid {
		t := s.LastSyncAt.Time
		resp.LastSyncAt = &t
	}
	return resp
}

// Create handles POST /api/v1/veeam-servers.
//
// The server is probed before it is stored, and a failed probe is a failed
// create. That is stricter than the PBS equivalent, deliberately: the probe is
// what produces api_revision, and a row with no pinned revision would fall
// back to whatever schema the server chooses to default to — the exact drift
// the header exists to prevent. Adding a Veeam server already requires the
// host to be reachable (the fingerprint fetch precedes it), so this costs no
// workflow that was otherwise available.
func (h *VeeamHandler) Create(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "veeam"); err != nil {
		return err
	}

	var req createVeeamRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if req.Name == "" || req.BaseURL == "" || req.Username == "" || req.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name, base_url, username, and password are required")
	}
	if len(req.Name) > 255 {
		return fiber.NewError(fiber.StatusBadRequest, "name must be 255 characters or fewer")
	}
	if err := validateURLFormat(req.BaseURL); err != nil {
		return err
	}
	if err := enforceURLAddressPolicy(c.Context(), req.BaseURL, req.AllowPrivateAddress); err != nil {
		return renderAddressPolicyError(c, err)
	}

	verifyTLS := true
	if req.VerifyTLS != nil {
		verifyTLS = *req.VerifyTLS
	}
	if err := requireInsecureTLSAck(!verifyTLS && req.TLSFingerprint == "", req.AcknowledgeInsecureTLS); err != nil {
		return err
	}

	probe, err := h.probe(c.Context(), veeam.Config{
		BaseURL:        req.BaseURL,
		Username:       req.Username,
		Password:       req.Password,
		TLSFingerprint: req.TLSFingerprint,
		VerifyTLS:      verifyTLS,
	})
	if err != nil {
		return renderVeeamProbeError(c, err)
	}

	encrypted, err := crypto.Encrypt(req.Password, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt password")
	}

	server, err := h.queries.CreateVeeamServer(c.Context(), db.CreateVeeamServerParams{
		Name:              req.Name,
		BaseUrl:           req.BaseURL,
		Username:          req.Username,
		PasswordEncrypted: encrypted,
		ApiRevision:       auditTruncate(probe.APIRevision, maxVeeamUpstreamField),
		ProductVersion:    auditTruncate(probe.BuildVersion, maxVeeamUpstreamField),
		LicenseEdition:    auditTruncate(probe.LicenseEdition, maxVeeamUpstreamField),
		TlsFingerprint:    req.TLSFingerprint,
		VerifyTls:         verifyTLS,
		Enabled:           true,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create Veeam server")
	}

	h.audit(c, server, "veeam_server_created", nil)

	return c.Status(fiber.StatusCreated).JSON(toVeeamResponse(server, true))
}

// List handles GET /api/v1/veeam-servers.
func (h *VeeamHandler) List(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "veeam"); err != nil {
		return err
	}

	full, err := h.callerSeesUsername(c)
	if err != nil {
		return err
	}

	servers, err := h.queries.ListVeeamServers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list Veeam servers")
	}

	resp := make([]veeamServerResponse, len(servers))
	for i, s := range servers {
		resp[i] = toVeeamResponse(s, full)
	}
	return RespondItems(c, resp)
}

// Get handles GET /api/v1/veeam-servers/:id.
func (h *VeeamHandler) Get(c fiber.Ctx) error {
	if err := requirePerm(c, "view", "veeam"); err != nil {
		return err
	}

	full, err := h.callerSeesUsername(c)
	if err != nil {
		return err
	}

	server, err := h.fetch(c)
	if err != nil {
		return err
	}
	return c.JSON(toVeeamResponse(server, full))
}

// callerSeesUsername reports whether the caller may see the configured Veeam
// account name.
//
// The username is half of a domain administrator credential, which is why the
// audit path already keeps it out of a details blob every Viewer can read (see
// the audit doc comment). Returning it from the read endpoints would defeat
// that control through the front door: view:veeam is seeded to the built-in
// Viewer, and 000016 gives Viewer every view action.
//
// manage:veeam holders keep it, because they can already rotate the credential
// and therefore gain nothing from being shown it.
func (h *VeeamHandler) callerSeesUsername(c fiber.Ctx) (bool, error) {
	return hasGlobalPerm(c, "manage", "veeam")
}

// Update handles PUT /api/v1/veeam-servers/:id.
//
// Any change to how we connect — URL, credentials, TLS handling — re-probes
// and refreshes the stored revision, version and edition. Renaming a server or
// toggling `enabled` does not, so an operator can disable an unreachable
// server without first having to make it reachable.
func (h *VeeamHandler) Update(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "veeam"); err != nil {
		return err
	}

	var req updateVeeamRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	existing, err := h.fetch(c)
	if err != nil {
		return err
	}

	params := db.UpdateVeeamServerParams{
		ID:                existing.ID,
		Name:              existing.Name,
		BaseUrl:           existing.BaseUrl,
		Username:          existing.Username,
		PasswordEncrypted: existing.PasswordEncrypted,
		ApiRevision:       existing.ApiRevision,
		ProductVersion:    existing.ProductVersion,
		LicenseEdition:    existing.LicenseEdition,
		TlsFingerprint:    existing.TlsFingerprint,
		VerifyTls:         existing.VerifyTls,
		Enabled:           existing.Enabled,
	}

	if req.Name != nil {
		if *req.Name == "" {
			return fiber.NewError(fiber.StatusBadRequest, "name must not be empty")
		}
		if len(*req.Name) > 255 {
			return fiber.NewError(fiber.StatusBadRequest, "name must be 255 characters or fewer")
		}
		params.Name = *req.Name
	}
	if req.Enabled != nil {
		params.Enabled = *req.Enabled
	}

	connectionChanged := false
	if req.BaseURL != nil && *req.BaseURL != existing.BaseUrl {
		if err := validateURLFormat(*req.BaseURL); err != nil {
			return err
		}
		if err := enforceURLAddressPolicy(c.Context(), *req.BaseURL, req.AllowPrivateAddress); err != nil {
			return renderAddressPolicyError(c, err)
		}
		params.BaseUrl = *req.BaseURL
		connectionChanged = true
	}
	if req.Username != nil && *req.Username != existing.Username {
		if *req.Username == "" {
			return fiber.NewError(fiber.StatusBadRequest, "username must not be empty")
		}
		params.Username = *req.Username
		connectionChanged = true
	}
	if req.TLSFingerprint != nil && *req.TLSFingerprint != existing.TlsFingerprint {
		params.TlsFingerprint = *req.TLSFingerprint
		connectionChanged = true
	}
	if req.VerifyTLS != nil && *req.VerifyTLS != existing.VerifyTls {
		params.VerifyTls = *req.VerifyTLS
		connectionChanged = true
	}

	// Dropping to a connection with NO verification at all has to be
	// deliberate: the stored password rides on every request to this host, so
	// it would then be in reach of anyone on the path. Neither field is ever
	// sent by the UI, so without this the only callers exercising them are the
	// ones nobody is watching.
	//
	// The predicate is the END STATE, not the individual fields. A pin always
	// outranks verify_tls in the transport, so turning verification off on a
	// pinned server changes nothing and needs no ceremony — and clearing a pin
	// while verification stays on is a legitimate move to a CA-signed address,
	// which is exactly what the edit dialog does when a server is re-homed.
	// Gating on either field alone would block that.
	pinRemoved := params.TlsFingerprint == "" && existing.TlsFingerprint != ""
	wasUnverified := existing.TlsFingerprint == "" && !existing.VerifyTls
	nowUnverified := params.TlsFingerprint == "" && !params.VerifyTls
	if err := requireInsecureTLSAck(nowUnverified && !wasUnverified, req.AcknowledgeInsecureTLS); err != nil {
		return err
	}

	// The plaintext password lives only in this local, only for as long as
	// the probe needs it. It is never assigned to params.
	password := ""
	if req.Password != nil && *req.Password != "" {
		password = *req.Password
		connectionChanged = true
	}

	// ANY change to the address, not merely a change of origin. The client
	// appends /api/oauth2/token to base_url verbatim, so a path or a query
	// string re-points the credential at a different listener behind the same
	// host — "https://vbr.example.com/anything-i-control" and
	// "https://vbr.example.com:9419?x=" both keep scheme and host while
	// sending the password somewhere else entirely. Origin equality would
	// have made the message below a lie.
	addressChanged := params.BaseUrl != existing.BaseUrl

	if connectionChanged {
		if password == "" {
			// The stored credential was entrusted to ONE server. Re-using it
			// against a different origin would post a decrypted domain
			// administrator password to whatever is listening there — which
			// is a credential-exfiltration primitive, not an address change:
			// the caller never has to know the password to steal it. Requiring
			// it to be re-typed means the caller can only send a credential
			// they already hold.
			if addressChanged {
				// Audited, because this is the one request that is
				// unambiguously an attempt to point a stored domain-admin
				// credential somewhere new. Refusing it silently would make
				// an enumeration of this path invisible.
				h.audit(c, existing, "veeam_server_credential_redirect_refused", map[string]any{
					"attempted_base_url": auditSafe(params.BaseUrl),
				})
				return fiber.NewError(fiber.StatusBadRequest,
					"Changing the server address requires re-entering the password. "+
						"The stored credential is only ever sent to the address it was saved for.")
			}

			decrypted, derr := crypto.Decrypt(existing.PasswordEncrypted, h.encryptionKey)
			if derr != nil {
				return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt stored password")
			}
			password = decrypted
		}

		probe, perr := h.probe(c.Context(), veeam.Config{
			BaseURL:        params.BaseUrl,
			Username:       params.Username,
			Password:       password,
			TLSFingerprint: params.TlsFingerprint,
			VerifyTLS:      params.VerifyTls,
		})
		if perr != nil {
			// Audit the FAILED attempt, not just the successful save. A
			// connection that sends a credential somewhere new is worth a row
			// whether or not it worked — otherwise the only trace of a
			// redirected credential is its absence.
			h.audit(c, existing, "veeam_server_connect_failed", map[string]any{
				// Truncated: nothing bounds the length of a URL a caller can
				// submit, and this row is readable by every Viewer.
				"attempted_base_url": auditSafe(params.BaseUrl),
				"base_url_changed":   params.BaseUrl != existing.BaseUrl,
				"username_changed":   params.Username != existing.Username,
				"password_supplied":  req.Password != nil && *req.Password != "",
			})
			return renderVeeamProbeError(c, perr)
		}
		params.ApiRevision = auditTruncate(probe.APIRevision, maxVeeamUpstreamField)
		params.ProductVersion = auditTruncate(probe.BuildVersion, maxVeeamUpstreamField)
		params.LicenseEdition = auditTruncate(probe.LicenseEdition, maxVeeamUpstreamField)

		if req.Password != nil && *req.Password != "" {
			encrypted, eerr := crypto.Encrypt(*req.Password, h.encryptionKey)
			if eerr != nil {
				return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt password")
			}
			params.PasswordEncrypted = encrypted
		}
	}

	server, err := h.queries.UpdateVeeamServer(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update Veeam server")
	}

	// What a later security review will want: whether the credential moved,
	// whether it was rotated, and whether the transport protecting it was
	// weakened. None of it reveals a secret — the username is deliberately
	// absent (see the audit doc comment).
	h.audit(c, server, "veeam_server_updated", map[string]any{
		"password_rotated": req.Password != nil && *req.Password != "",
		"verify_tls":       server.VerifyTls,
		"tls_pin_removed":  pinRemoved,
		"base_url_changed": server.BaseUrl != existing.BaseUrl,
		"username_changed": server.Username != existing.Username,
	})

	return c.JSON(toVeeamResponse(server, true))
}

// Delete handles DELETE /api/v1/veeam-servers/:id.
func (h *VeeamHandler) Delete(c fiber.Ctx) error {
	if err := requirePerm(c, "delete", "veeam"); err != nil {
		return err
	}

	existing, err := h.fetch(c)
	if err != nil {
		return err
	}

	if err := h.queries.DeleteVeeamServer(c.Context(), existing.ID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete Veeam server")
	}

	// After the delete, not before: audit_log has no foreign key to
	// veeam_servers, so the row survives on its own, and auditing first would
	// leave a "deleted" record for a delete that failed.
	h.audit(c, existing, "veeam_server_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}

// Test handles POST /api/v1/veeam-servers/:id/test.
//
// Gated on manage rather than view: it makes an outbound authenticated
// connection using stored admin credentials, which is not a read of Nexara's
// own state. Persists nothing — an operator diagnosing a broken server should
// not have the act of diagnosing it rewrite the row.
func (h *VeeamHandler) Test(c fiber.Ctx) error {
	if err := requirePerm(c, "manage", "veeam"); err != nil {
		return err
	}

	server, err := h.fetch(c)
	if err != nil {
		return err
	}

	password, err := crypto.Decrypt(server.PasswordEncrypted, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt stored password")
	}

	probe, err := h.probe(c.Context(), veeam.Config{
		BaseURL:        server.BaseUrl,
		Username:       server.Username,
		Password:       password,
		APIRevision:    server.ApiRevision,
		TLSFingerprint: server.TlsFingerprint,
		VerifyTLS:      server.VerifyTls,
	})

	// Audited either way: this is the endpoint that spends a real logon
	// against the stored account, so a burst of failures here is what an
	// account lockout looks like from Nexara's side.
	h.audit(c, server, "veeam_server_tested", map[string]any{"succeeded": err == nil})

	if err != nil {
		return renderVeeamProbeError(c, err)
	}

	return c.JSON(probe)
}

// fetch loads the server named by :id, mapping a missing row to 404.
func (h *VeeamHandler) fetch(c fiber.Ctx) (db.VeeamServer, error) {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return db.VeeamServer{}, fiber.NewError(fiber.StatusBadRequest, "Invalid Veeam server ID")
	}
	server, err := h.queries.GetVeeamServer(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.VeeamServer{}, fiber.NewError(fiber.StatusNotFound, "Veeam server not found")
		}
		return db.VeeamServer{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get Veeam server")
	}
	return server, nil
}

// probe builds a throwaway client, runs the connection test, and releases the
// token it minted.
//
// The client is not cached: Phase 1 has no sync loop, and a per-probe client
// means the plaintext password has no lifetime beyond this call.
func (h *VeeamHandler) probe(ctx context.Context, cfg veeam.Config) (*veeam.ProbeResult, error) {
	cfg.Timeout = veeamProbeTimeout

	client, err := veeam.New(cfg)
	if err != nil {
		return nil, err
	}

	probeCtx, cancel := context.WithTimeout(ctx, veeamProbeTimeout)
	defer cancel()

	result, err := client.Probe(probeCtx)
	// Release the session even when the probe failed part-way — a successful
	// auth followed by a failed version check still left a token on the
	// server. Best-effort: it expires on its own in 15 minutes.
	_ = client.Logout(probeCtx)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// audit records a Veeam server mutation.
//
// clusterID is deliberately the zero (NULL) pgtype.UUID: a Veeam server is not
// a per-cluster resource, and attributing it to one cluster would both be
// wrong and narrow the row out of a scoped reader's view.
//
// Details never carry the password, and never carry the username either.
// view:audit is granted to every Viewer by default, and the username is half
// of a domain administrator credential — a fact worth keeping out of a row
// every read-only account can list.
func (h *VeeamHandler) audit(c fiber.Ctx, server db.VeeamServer, action string, extra map[string]any) {
	AuditLog(c, h.queries, h.eventPub,
		pgtype.UUID{}, "veeam_server", server.ID.String(), action,
		veeamAuditDetails(server, extra))
}

// veeamAuditDetails builds the details blob for a Veeam audit row. Split out
// from audit so TestVeeamAuditDetailsCarryNoSecrets can assert its contents
// without a database.
func veeamAuditDetails(server db.VeeamServer, extra map[string]any) json.RawMessage {
	details := map[string]any{
		"name": server.Name,
		// Both come off the wire from a server the caller chose, and nothing
		// upstream bounds either — buildVersion only has to start with two
		// numeric components to pass the version gate. audit_log.details is
		// written once per request, never trimmed, and readable by every
		// Viewer, so upstream text is capped here as it is everywhere else.
		"product_version": auditSafe(server.ProductVersion),
		"license_edition": auditSafe(server.LicenseEdition),
	}
	for k, v := range extra {
		details[k] = v
	}

	// Marshal rather than concatenate: a server name containing a quote would
	// otherwise produce a malformed details blob.
	blob, err := json.Marshal(details)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return blob
}

// maxVeeamUpstreamField bounds a value read off a Veeam server before it is
// stored. buildVersion is "13.1.0.411" and edition is "EnterprisePlus"; the
// cap only ever bites on a value a hostile or broken server invented, and it
// stops that value reaching a TEXT column that every view:veeam holder reads
// back.
const maxVeeamUpstreamField = 128

// requireInsecureTLSAck refuses a request that would store or keep a
// credential against an unverified connection unless the caller says so
// explicitly.
//
// A confirm-and-proceed gate rather than a refusal, matching how this project
// handles private addresses: a lab with an unreachable internal CA is a real
// case. What it removes is doing it by accident, or by a request the UI never
// makes and nobody reviews.
func requireInsecureTLSAck(insecure, acknowledged bool) error {
	if !insecure || acknowledged {
		return nil
	}
	return fiber.NewError(fiber.StatusUnprocessableEntity,
		"This would send the stored administrator password over a connection whose "+
			"certificate is never verified. Pin the certificate instead, or re-submit "+
			"with acknowledge_insecure_tls=true to confirm.")
}

// renderVeeamProbeError turns a client error into the most actionable HTTP
// response available. Each branch exists because the operator's next action
// differs: fix the credential, upgrade the server, fix the URL, or trust the
// certificate.
func renderVeeamProbeError(c fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, veeam.ErrAuthFailed):
		// 422, never 401. A 401 from Nexara's own API means "your session is
		// invalid", and the SPA's api-client acts on that: it burns a token
		// refresh and REPLAYS the request, which runs the probe a second time
		// and spends a second failed logon against what is usually a domain
		// admin account — halving the operator's lockout budget per click.
		// If that refresh happens to fail, the client clears the session and
		// logs the operator out because their *Veeam* password was wrong.
		// clusters_bootstrap.go takes the same 422 line for PVE.
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error":   "veeam_auth_failed",
			"message": err.Error(),
		})
	case errors.Is(err, veeam.ErrVersionUnsupported):
		return fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, veeam.ErrRevisionUnknown):
		return fiber.NewError(fiber.StatusUnprocessableEntity,
			"Could not negotiate a supported Veeam API version with this server. "+err.Error())
	case errors.Is(err, veeam.ErrUnreachable):
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	case errors.Is(err, veeam.ErrInvalidInput):
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	default:
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
}
