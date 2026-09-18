package handlers

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// PBSHandler handles PBS server CRUD endpoints.
type PBSHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewPBSHandler creates a new PBS server handler.
func NewPBSHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *PBSHandler {
	return &PBSHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
	}
}

type pbsResponse struct {
	ID             uuid.UUID  `json:"id"`
	Name           string     `json:"name"`
	APIURL         string     `json:"api_url"`
	TokenID        string     `json:"token_id"`
	TLSFingerprint string     `json:"tls_fingerprint"`
	ClusterID      *uuid.UUID `json:"cluster_id"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func toPBSResponse(p db.PbsServer) pbsResponse {
	resp := pbsResponse{
		ID:             p.ID,
		Name:           p.Name,
		APIURL:         p.ApiUrl,
		TokenID:        p.TokenID,
		TLSFingerprint: p.TlsFingerprint,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
	}
	if p.ClusterID.Valid {
		id := p.ClusterID.Bytes
		uid := uuid.UUID(id)
		resp.ClusterID = &uid
	}
	return resp
}

// Create handles POST /api/v1/pbs-servers.
//
// Declared Deferred: the cluster to authorize is named in the BODY, which
// no middleware can read, and an absent one means the server is standalone
// and needs the instance-wide grant instead.
func (h *PBSHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	// Permission gate runs against the cluster in the body when present (so
	// an operator with manage:pbs only on cluster X can attach a PBS to
	// cluster X), or globally otherwise. The EMPTY string means standalone
	// here, which is why the parameter carries a pattern rather than the
	// uuid format — see pbsAttachedClusterParam.
	var clusterID pgtype.UUID
	if raw := p.String("attached_cluster_id"); raw != "" {
		parsed, err := parseParamUUID(raw)
		if err != nil {
			return err
		}
		if err := requireClusterPerm(c, "manage", "pbs", parsed); err != nil {
			return err
		}
		clusterID = pgtype.UUID{Bytes: parsed, Valid: true}
	} else {
		// Standalone PBS server — gate behind global manage:pbs.
		if err := requirePerm(c, "manage", "pbs"); err != nil {
			return err
		}
	}

	// The four "x is required" checks and the 255-character name cap are the
	// schema's now. What stays here is the URL policy: validateURLFormat
	// owns the https/host/no-credentials rule, and enforceURLAddressPolicy
	// is a DNS-resolving SSRF check no parameter schema could make.
	apiURL := p.String("api_url")
	if err := validateURLFormat(apiURL); err != nil {
		return err
	}
	if err := enforceURLAddressPolicy(c.Context(), apiURL, p.Bool("allow_private_address")); err != nil {
		return renderAddressPolicyError(c, err)
	}

	encrypted, err := crypto.Encrypt(p.String("token_secret"), h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt token secret")
	}

	pbs, err := h.queries.CreatePBSServer(c.Context(), db.CreatePBSServerParams{
		Name:                 p.String("name"),
		ApiUrl:               apiURL,
		TokenID:              p.String("token_id"),
		TokenSecretEncrypted: encrypted,
		ClusterID:            clusterID,
		TlsFingerprint:       p.String("tls_fingerprint"),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create PBS server")
	}

	AuditLog(c, h.queries, h.eventPub,
		pbs.ClusterID, "pbs_server", pbs.ID.String(), "pbs_created",
		pbsAuditName(pbs.Name))

	return c.Status(fiber.StatusCreated).JSON(toPBSResponse(pbs))
}

// List handles GET /api/v1/pbs-servers.
//
// Declared Advisory: nothing gates this listing, and the permission is
// applied as a per-row FILTER instead — cluster-scoped for a server
// attached to one, instance-wide for a standalone one.
func (h *PBSHandler) List(c fiber.Ctx, _ *apischema.Params) error {
	access, err := accessibleClusters(c, "view", "pbs")
	if err != nil {
		return err
	}

	servers, err := h.queries.ListPBSServers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list PBS servers")
	}

	resp := make([]pbsResponse, 0, len(servers))
	for _, s := range servers {
		// Standalone PBS (no cluster_id) requires global view:pbs; cluster-bound
		// PBS requires view:pbs on that cluster.
		if !s.ClusterID.Valid {
			if !access.HasGlobal {
				continue
			}
		} else if !access.PermitsCluster(uuid.UUID(s.ClusterID.Bytes)) {
			continue
		}
		resp = append(resp, toPBSResponse(s))
	}

	return RespondItems(c, resp)
}

// ListByCluster handles GET /api/v1/clusters/:cluster_id/pbs-servers.
//
// The one route in this domain whose subject is the cluster in its own
// path, and therefore the one whose permission hoists into middleware.
func (h *PBSHandler) ListByCluster(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	servers, err := h.queries.ListPBSServersByCluster(c.Context(), pgtype.UUID{Bytes: clusterID, Valid: true})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list PBS servers")
	}

	resp := make([]pbsResponse, len(servers))
	for i, s := range servers {
		resp[i] = toPBSResponse(s)
	}

	return RespondItems(c, resp)
}

// Get handles GET /api/v1/pbs-servers/:id.
//
// Declared Deferred: the scope depends on a DB lookup — the row says
// whether this server belongs to a cluster, and therefore whether the
// grant is cluster-scoped or instance-wide.
func (h *PBSHandler) Get(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	pbs, err := h.queries.GetPBSServer(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	if pbs.ClusterID.Valid {
		if err := requireClusterPerm(c, "view", "pbs", uuid.UUID(pbs.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "view", "pbs"); err != nil {
		return err
	}

	return c.JSON(toPBSResponse(pbs))
}

// Update handles PUT /api/v1/pbs-servers/:id.
//
// Declared Deferred for the reason Get gives, plus one of its own: moving
// the server to another cluster needs manage:pbs on the TARGET as well,
// and the target is named in the body.
func (h *PBSHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	// An explicit "" is not a way to say "keep the current secret" — omitting
	// the field is. Encrypting it would swap a working credential for the
	// ciphertext of an empty string (crypto.Encrypt always emits nonce+tag, so
	// the column stays non-empty and nothing downstream notices), which is a
	// one-request way to break this server's collection. It also reads to the
	// redirect check below as "no secret supplied", so the caller would be
	// told to re-enter a secret they did in fact send. Kept here rather than
	// expressed as a MinLength so the message can say what to do instead.
	suppliedSecret, secretSupplied := p.OptString("token_secret")
	if secretSupplied && suppliedSecret == "" {
		return fiber.NewError(fiber.StatusBadRequest,
			"token_secret must not be empty — omit the field to keep the stored secret")
	}

	existing, err := h.queries.GetPBSServer(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	if existing.ClusterID.Valid {
		if err := requireClusterPerm(c, "manage", "pbs", uuid.UUID(existing.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "manage", "pbs"); err != nil {
		return err
	}

	params := db.UpdatePBSServerParams{
		ID:                   id,
		Name:                 existing.Name,
		ApiUrl:               existing.ApiUrl,
		TokenID:              existing.TokenID,
		TokenSecretEncrypted: existing.TokenSecretEncrypted,
		ClusterID:            existing.ClusterID,
		TlsFingerprint:       existing.TlsFingerprint,
	}

	// If the user is moving the PBS server to a different cluster, also require
	// manage:pbs on the target cluster.
	//
	// The parse and the check are one branch rather than two, which closes a
	// fail-open gap: the check used to sit behind `if perr == nil`, so a
	// cluster_id that did not parse SKIPPED it and was refused only later,
	// by a second parse further down. The schema's uuid format means an
	// unparsable value never reaches here at all, and the move is now
	// authorized and applied from the same read.
	if raw, supplied := p.OptString("attached_cluster_id"); supplied {
		parsed, perr := parseParamUUID(raw)
		if perr != nil {
			return perr
		}
		if err := requireClusterPerm(c, "manage", "pbs", parsed); err != nil {
			return err
		}
		params.ClusterID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	if name, supplied := p.OptString("name"); supplied {
		params.Name = name
	}
	if apiURL, supplied := p.OptString("api_url"); supplied {
		if err := validateURLFormat(apiURL); err != nil {
			return err
		}
		if err := enforceURLAddressPolicy(c.Context(), apiURL, p.Bool("allow_private_address")); err != nil {
			return renderAddressPolicyError(c, err)
		}
		params.ApiUrl = apiURL
	}
	if tokenID, supplied := p.OptString("token_id"); supplied {
		params.TokenID = tokenID
	}
	if secretSupplied {
		encrypted, err := crypto.Encrypt(suppliedSecret, h.encryptionKey)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt token secret")
		}
		params.TokenSecretEncrypted = encrypted
	}
	if fingerprint, supplied := p.OptString("tls_fingerprint"); supplied {
		params.TlsFingerprint = fingerprint
	}

	// Refuse to re-point a stored token at an address the operator never
	// entrusted it to. See credential_redirect.go. Nothing connects in this
	// handler, so the delivery is deferred: the collector rebuilds a client
	// from this row on its next sync (internal/proxmox/cache.go), which is
	// what makes the row worth refusing rather than merely warning about.
	if credentialRedirected(params.ApiUrl, existing.ApiUrl, existing.TokenSecretEncrypted, suppliedSecret) {
		// Audited, because this is the one request that is unambiguously an
		// attempt to point a stored credential somewhere new. Refusing it
		// silently would make an enumeration of this path invisible.
		redirectDetails, _ := json.Marshal(map[string]any{
			// Truncated: nothing bounds the length of a URL a caller can
			// submit, and audit_log.details is readable by every Viewer.
			"attempted_api_url": auditSafe(params.ApiUrl),
			"previous_api_url":  auditSafe(existing.ApiUrl),
		})
		AuditLog(c, h.queries, h.eventPub, existing.ClusterID, "pbs_server", id.String(),
			"pbs_credential_redirect_refused", redirectDetails)
		return errCredentialRedirect("server API URL", "API token secret")
	}

	pbs, err := h.queries.UpdatePBSServer(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update PBS server")
	}

	if cache := proxmoxCacheFromCtx(c); cache != nil {
		cache.PublishInvalidation(c.Context(), proxmox.CacheKindPBS, pbs.ID)
	}

	AuditLog(c, h.queries, h.eventPub,
		pbs.ClusterID, "pbs_server", pbs.ID.String(), "pbs_updated",
		pbsAuditName(pbs.Name))

	return c.JSON(toPBSResponse(pbs))
}

// Delete handles DELETE /api/v1/pbs-servers/:id.
//
// Declared Deferred for the reason Get gives: the scope comes off the row.
func (h *PBSHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	existing, err := h.queries.GetPBSServer(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "PBS server not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get PBS server")
	}

	if existing.ClusterID.Valid {
		if err := requireClusterPerm(c, "delete", "pbs", uuid.UUID(existing.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "delete", "pbs"); err != nil {
		return err
	}

	AuditLog(c, h.queries, h.eventPub,
		existing.ClusterID, "pbs_server", id.String(), "pbs_deleted",
		pbsAuditName(existing.Name))

	if err := h.queries.DeletePBSServer(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete PBS server")
	}

	if cache := proxmoxCacheFromCtx(c); cache != nil {
		cache.PublishInvalidation(c.Context(), proxmox.CacheKindPBS, id)
	}

	return c.SendStatus(fiber.StatusNoContent)
}

// pbsAuditName builds the details blob for the plain name-only PBS audit rows.
// Marshalled rather than concatenated: audit_log.details is JSONB NOT NULL, so
// a server name containing a quote would produce a malformed blob, fail the
// insert, and leave the action with no audit row at all — AuditLog only logs
// that failure. A crafted name could otherwise inject its own keys into a row
// every Viewer can read.
func pbsAuditName(name string) json.RawMessage {
	details, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return details
}
