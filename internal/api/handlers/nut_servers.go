package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/nut"
)

// NutReloadTrigger asks the NUT poller to re-read its server list now.
type NutReloadTrigger interface{ TriggerInventory() }

// NutHandler serves the NUT server registry and the UPS devices, readings
// and power events the poller collects from it (registry_ups.go).
type NutHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
	reload        NutReloadTrigger
	pollInterval  time.Duration
	// nodeSyncInterval is how often a full sync refreshes the node rows
	// (METRICS_COLLECT_INTERVAL), so a node status older than a few passes is
	// not taken as current.
	nodeSyncInterval time.Duration
	dial             func(context.Context, nut.Config) (*nut.Client, error)
	// shutdownCtx ends the watch of a tracked command run when Nexara stops
	// (SetShutdownContext); runSlots caps the runs being watched; a run waits
	// trackingWait for the driver's answer, asking every trackingPoll (P8).
	shutdownCtx  context.Context
	runSlots     chan struct{}
	runMu        sync.Mutex
	serverRuns   map[uuid.UUID]int
	trackingWait time.Duration
	trackingPoll time.Duration
}

// NewNutHandler creates the handler.
func NewNutHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *NutHandler {
	return &NutHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
		pollInterval:  10 * time.Second,
		// config.MetricsCollectInterval's default.
		nodeSyncInterval: 30 * time.Second,
		dial:             nut.Dial,
		shutdownCtx:      context.Background(),
		runSlots:         make(chan struct{}, upsRunsInFlight),
		serverRuns:       map[uuid.UUID]int{},
		trackingWait:     time.Minute,
		trackingPoll:     time.Second,
	}
}

// SetShutdownContext ends the watch of tracked command runs when ctx ends.
func (h *NutHandler) SetShutdownContext(ctx context.Context) { h.shutdownCtx = ctx }

// SetReloadTrigger wires the poller's reload. Without one, a change is picked
// up on the poller's next reconcile, within 30 s.
func (h *NutHandler) SetReloadTrigger(t NutReloadTrigger) { h.reload = t }

// SetPollInterval tells the device endpoints how often readings arrive, so a
// reading older than a few intervals is reported as stale (see upsStaleAfter).
func (h *NutHandler) SetPollInterval(d time.Duration) {
	if d > 0 {
		h.pollInterval = d
	}
}

// SetNodeSyncInterval is METRICS_COLLECT_INTERVAL, the full sync's cadence.
func (h *NutHandler) SetNodeSyncInterval(d time.Duration) {
	if d > 0 {
		h.nodeSyncInterval = d
	}
}

// nutProbeTimeout bounds a connection test: a dial, VER, NETVER and LIST UPS.
const nutProbeTimeout = 15 * time.Second

// nutServerResponse is the only shape a NUT server is serialized in. It has no
// password field at all.
type nutServerResponse struct {
	ID              uuid.UUID  `json:"id"`
	Name            string     `json:"name"`
	Host            string     `json:"host"`
	Port            int32      `json:"port"`
	Username        string     `json:"username"`
	HasPassword     bool       `json:"has_password"`
	TLSMode         string     `json:"tls_mode"`
	VerifyTLS       bool       `json:"verify_tls"`
	TLSFingerprint  string     `json:"tls_fingerprint"`
	Enabled         bool       `json:"enabled"`
	NutVersion      string     `json:"nut_version"`
	ProtocolVersion string     `json:"protocol_version"`
	LastPollAt      *time.Time `json:"last_poll_at"`
	LastPollError   string     `json:"last_poll_error"`
	// CommandsVerifiedAt is when upsd last took a command from the stored
	// account: the only proof it may run them, since upsd checks an account
	// only when a command runs.
	CommandsVerifiedAt *time.Time `json:"commands_verified_at"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func toNutServerResponse(s db.NutServer, includeUsername bool) nutServerResponse {
	resp := nutServerResponse{
		ID: s.ID, Name: s.Name, Host: s.Host, Port: s.Port,
		HasPassword: s.PasswordEncrypted != "",
		TLSMode:     s.TlsMode, VerifyTLS: s.VerifyTls, TLSFingerprint: s.TlsFingerprint,
		Enabled: s.Enabled, NutVersion: s.NutVersion, ProtocolVersion: s.ProtocolVersion,
		LastPollError: s.LastPollError, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
	if includeUsername {
		resp.Username = s.Username
	}
	if s.LastPollAt.Valid {
		t := s.LastPollAt.Time
		resp.LastPollAt = &t
	}
	if s.CommandsVerifiedAt.Valid {
		t := s.CommandsVerifiedAt.Time
		resp.CommandsVerifiedAt = &t
	}
	return resp
}

// nutProbeResponse is what a connection test found.
type nutProbeResponse struct {
	NutVersion      string           `json:"nut_version"`
	ProtocolVersion string           `json:"protocol_version"`
	TookMS          int64            `json:"took_ms"`
	TLS             bool             `json:"tls"`
	TLSFingerprint  string           `json:"tls_fingerprint"`
	Devices         []nutProbeDevice `json:"devices"`
}

type nutProbeDevice struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// maxProbeDevices caps the device list a probe returns; a server reporting
// more is still stored, and the poller reads every one.
const maxProbeDevices = 64

func (h *NutHandler) probe(ctx context.Context, cfg nut.Config) (*nutProbeResponse, error) {
	pctx, cancel := context.WithTimeout(ctx, nutProbeTimeout)
	defer cancel()
	start := time.Now()
	c, err := h.dial(pctx, cfg)
	if err != nil {
		return nil, err
	}
	defer func() { _ = c.Close() }()
	release, protocol, err := c.Versions(pctx)
	if err != nil {
		return nil, err
	}
	list, err := c.ListUPS(pctx)
	if err != nil {
		return nil, err
	}
	resp := &nutProbeResponse{
		NutVersion: release, ProtocolVersion: protocol, TookMS: time.Since(start).Milliseconds(),
		TLS: cfg.TLS == nut.TLSStartTLS, TLSFingerprint: c.PeerFingerprint(),
		Devices: make([]nutProbeDevice, 0, min(len(list), maxProbeDevices)),
	}
	for _, u := range list[:min(len(list), maxProbeDevices)] {
		resp.Devices = append(resp.Devices, nutProbeDevice{Name: u.Name, Description: auditTruncate(u.Description, 256)})
	}
	return resp, nil
}

// renderNutProbeError picks the response for a failed probe by what the
// operator does next: fix the credential, fix TLS, or fix the address.
func renderNutProbeError(c fiber.Ctx, err error) error {
	switch {
	case nut.IsAuth(err):
		// 422, never 401: the SPA reads a 401 as its own session ending,
		// refreshes and replays the request (see renderVeeamProbeError).
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error":   "nut_auth_failed",
			"message": "The NUT server refused this host or these credentials (" + err.Error() + ").",
		})
	case errors.Is(err, nut.ErrTLSUnavailable):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error":   "nut_tls_unavailable",
			"message": "The NUT server does not offer STARTTLS. Turn TLS off for it, or give upsd a certificate (CERTFILE in upsd.conf).",
		})
	default:
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"error":   "nut_unreachable",
			"message": "Could not read from the NUT server: " + auditSafe(err.Error()),
		})
	}
}

// nutTarget is a validated connection target from a request body.
type nutTarget struct {
	host        string
	port        int
	username    string
	password    string
	tlsMode     string
	verifyTLS   bool
	fingerprint string
}

func (t nutTarget) config() nut.Config {
	return nut.Config{
		Host: t.host, Port: t.port, Username: t.username, Password: t.password,
		TLS: nut.TLSMode(t.tlsMode), VerifyTLS: t.verifyTLS, Fingerprint: t.fingerprint,
	}
}

// normalizeNutHost accepts a hostname or an IP literal (IPv6 with or without
// brackets) and refuses anything carrying a scheme, port, path or userinfo,
// which belong elsewhere or nowhere.
func normalizeNutHost(raw string) (string, error) {
	host := strings.TrimSpace(raw)
	if ip := net.ParseIP(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")); ip != nil {
		return ip.String(), nil
	}
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /:@?#[]\\") {
		return "", fiber.NewError(fiber.StatusBadRequest,
			"host must be a hostname or an IP address, without a scheme or port (the port is its own field)")
	}
	return host, nil
}

// nutTransportRisk ranks how a password would travel to upsd: 0 protected or
// no password at all, 1 over TLS that would accept any certificate, 2 in
// plain text.
func nutTransportRisk(t nutTarget) int {
	switch {
	case t.password == "":
		return 0
	case t.tlsMode == string(nut.TLSOff):
		return 2
	case t.fingerprint == "" && !t.verifyTLS:
		return 1
	}
	return 0
}

// requireNutTransportAck refuses storing a password that would travel
// unprotected, unless the caller confirms. A warning rather than a block: a
// NUT server on a LAN without TLS is the common case.
func requireNutTransportAck(t nutTarget, ackCleartext, ackInsecureTLS bool) error {
	switch nutTransportRisk(t) {
	case 2:
		if !ackCleartext {
			return &confirmRequiredError{
				Code:             "nut_cleartext_credentials_confirm_required",
				Message:          "This server is set to connect without TLS, so the password would cross the network in plain text.",
				AcknowledgeField: "acknowledge_cleartext_credentials",
			}
		}
	case 1:
		if !ackInsecureTLS {
			return &confirmRequiredError{
				Code:             "nut_insecure_tls_confirm_required",
				Message:          "TLS without a pinned or verified certificate would send the password to whoever answers on that address.",
				AcknowledgeField: "acknowledge_insecure_tls",
			}
		}
	}
	return nil
}

// nutUpdateTransportAck is the edit's version: judged on the end state, and
// only a move to a weaker transport asks. A server confirmed for plain text is
// not asked again on a rename; one confirmed only for unverified TLS is asked
// before its password goes out in plain text.
func nutUpdateTransportAck(was, next nutTarget, ackCleartext, ackInsecureTLS bool) error {
	if nutTransportRisk(next) <= nutTransportRisk(was) {
		return nil
	}
	return requireNutTransportAck(next, ackCleartext, ackInsecureTLS)
}

// targetFromParams reads POST /nut-servers and POST /nut-servers/probe.
func targetFromParams(p *apischema.Params) (nutTarget, error) {
	host, err := normalizeNutHost(p.String("host"))
	if err != nil {
		return nutTarget{}, err
	}
	t := nutTarget{
		host: host, port: int(p.Int("port")),
		username: p.String("username"), password: p.String("password"),
		tlsMode: p.String("tls_mode"), verifyTLS: p.Bool("verify_tls"),
		fingerprint: nut.NormalizeFingerprint(p.String("tls_fingerprint")),
	}
	if t.password != "" && t.username == "" {
		return nutTarget{}, fiber.NewError(fiber.StatusBadRequest, "a password needs a username: upsd checks them as a pair")
	}
	return t, nil
}

// Probe handles POST /api/v1/nut-servers/probe: test a connection the add
// dialog has not saved yet. It grants nothing Create does not: a manage:ups
// holder can already make Nexara connect by creating the server.
func (h *NutHandler) Probe(c fiber.Ctx, p *apischema.Params) error {
	t, err := targetFromParams(p)
	if err != nil {
		return err
	}
	if err := enforceHostAddressPolicy(c.Context(), t.host, p.Bool("allow_private_address")); err != nil {
		return renderAddressPolicyError(c, err)
	}
	if err := requireNutTransportAck(t, p.Bool("acknowledge_cleartext_credentials"), p.Bool("acknowledge_insecure_tls")); err != nil {
		return renderConfirmRequired(c, err)
	}
	probe, err := h.probe(c.Context(), t.config())
	if err != nil {
		return renderNutProbeError(c, err)
	}
	return c.JSON(probe)
}

// Create handles POST /api/v1/nut-servers. The server is probed before it is
// stored, so a typo in the address is a failed create rather than a server
// that never reads.
func (h *NutHandler) Create(c fiber.Ctx, p *apischema.Params) error {
	t, err := targetFromParams(p)
	if err != nil {
		return err
	}
	if err := enforceHostAddressPolicy(c.Context(), t.host, p.Bool("allow_private_address")); err != nil {
		return renderAddressPolicyError(c, err)
	}
	if err := requireNutTransportAck(t, p.Bool("acknowledge_cleartext_credentials"), p.Bool("acknowledge_insecure_tls")); err != nil {
		return renderConfirmRequired(c, err)
	}
	probe, err := h.probe(c.Context(), t.config())
	if err != nil {
		return renderNutProbeError(c, err)
	}
	encrypted := ""
	if t.password != "" {
		if encrypted, err = crypto.Encrypt(t.password, h.encryptionKey); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt password")
		}
	}
	server, err := h.queries.CreateNutServer(c.Context(), db.CreateNutServerParams{
		Name: p.String("name"), Host: t.host, Port: int32(t.port), //nolint:gosec // G115: declared 1..65535
		Username: t.username, PasswordEncrypted: encrypted,
		TlsMode: t.tlsMode, VerifyTls: t.verifyTLS, TlsFingerprint: t.fingerprint, Enabled: true,
		NutVersion: auditTruncate(probe.NutVersion, 32), ProtocolVersion: auditTruncate(probe.ProtocolVersion, 32),
	})
	if err != nil {
		return nutWriteError(err, "create")
	}
	h.audit(c, server, "nut_server_created", map[string]any{"devices": len(probe.Devices)})
	h.changed(c)
	return c.Status(fiber.StatusCreated).JSON(toNutServerResponse(server, true))
}

func nutWriteError(err error, verb string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fiber.NewError(fiber.StatusConflict, "A NUT server with that name already exists")
	}
	return fiber.NewError(fiber.StatusInternalServerError, "Failed to "+verb+" NUT server")
}

// List handles GET /api/v1/nut-servers.
func (h *NutHandler) List(c fiber.Ctx, _ *apischema.Params) error {
	full, err := hasGlobalPerm(c, "manage", "ups")
	if err != nil {
		return err
	}
	servers, err := h.queries.ListNutServers(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list NUT servers")
	}
	resp := make([]nutServerResponse, len(servers))
	for i, s := range servers {
		resp[i] = toNutServerResponse(s, full)
	}
	return RespondItems(c, resp)
}

// Get handles GET /api/v1/nut-servers/:id.
func (h *NutHandler) Get(c fiber.Ctx, p *apischema.Params) error {
	full, err := hasGlobalPerm(c, "manage", "ups")
	if err != nil {
		return err
	}
	server, err := h.fetchServer(c, p)
	if err != nil {
		return err
	}
	return c.JSON(toNutServerResponse(server, full))
}

// Update handles PUT /api/v1/nut-servers/:id. Every field is optional. A
// change to how Nexara connects re-probes; renaming or toggling enabled does
// not, so an unreachable server can still be disabled.
func (h *NutHandler) Update(c fiber.Ctx, p *apischema.Params) error {
	existing, err := h.fetchServer(c, p)
	if err != nil {
		return err
	}
	params := db.UpdateNutServerParams{
		ID: existing.ID, Name: existing.Name, Host: existing.Host, Port: existing.Port,
		Username: existing.Username, PasswordEncrypted: existing.PasswordEncrypted,
		TlsMode: existing.TlsMode, VerifyTls: existing.VerifyTls, TlsFingerprint: existing.TlsFingerprint,
		Enabled: existing.Enabled, NutVersion: existing.NutVersion, ProtocolVersion: existing.ProtocolVersion,
	}
	if name, ok := p.OptString("name"); ok {
		params.Name = name
	}
	if enabled, ok := p.OptBool("enabled"); ok {
		params.Enabled = enabled
	}

	connectionChanged := false
	if raw, ok := p.OptString("host"); ok {
		host, herr := normalizeNutHost(raw)
		if herr != nil {
			return herr
		}
		if host != existing.Host {
			if err := enforceHostAddressPolicy(c.Context(), host, p.Bool("allow_private_address")); err != nil {
				return renderAddressPolicyError(c, err)
			}
			params.Host, connectionChanged = host, true
		}
	}
	if port, ok := p.OptInt("port"); ok && port != int64(existing.Port) {
		params.Port, connectionChanged = int32(port), true //nolint:gosec // G115: declared 1..65535
	}
	if mode, ok := p.OptString("tls_mode"); ok && mode != existing.TlsMode {
		params.TlsMode, connectionChanged = mode, true
	}
	if verify, ok := p.OptBool("verify_tls"); ok && verify != existing.VerifyTls {
		params.VerifyTls, connectionChanged = verify, true
	}
	if fp, ok := p.OptString("tls_fingerprint"); ok && nut.NormalizeFingerprint(fp) != existing.TlsFingerprint {
		params.TlsFingerprint, connectionChanged = nut.NormalizeFingerprint(fp), true
	}
	if username, ok := p.OptString("username"); ok && username != existing.Username {
		params.Username, connectionChanged = username, true
	}
	clearCredentials := p.Bool("clear_credentials")
	if clearCredentials {
		params.Username, params.PasswordEncrypted = "", ""
		connectionChanged = connectionChanged || existing.PasswordEncrypted != "" || existing.Username != ""
	}
	supplied := p.String("password")
	if supplied != "" {
		connectionChanged = true
	}

	// Moving the address while keeping a stored password would send that
	// password to the new address without the caller ever knowing it. Compare
	// the full host:port: see credentialRedirected.
	newAddr := net.JoinHostPort(params.Host, strconv.Itoa(int(params.Port)))
	oldAddr := net.JoinHostPort(existing.Host, strconv.Itoa(int(existing.Port)))
	if credentialRedirected(newAddr, oldAddr, params.PasswordEncrypted, supplied) {
		h.audit(c, existing, "nut_server_credential_redirect_refused", map[string]any{"attempted_address": auditSafe(newAddr)})
		return errCredentialRedirect("server address", "password")
	}

	password := supplied
	if password == "" && params.PasswordEncrypted != "" {
		decrypted, derr := crypto.Decrypt(params.PasswordEncrypted, h.encryptionKey)
		if derr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt stored password")
		}
		password = decrypted
	}
	target := nutTarget{
		host: params.Host, port: int(params.Port), username: params.Username, password: password,
		tlsMode: params.TlsMode, verifyTLS: params.VerifyTls, fingerprint: params.TlsFingerprint,
	}
	if target.password != "" && target.username == "" {
		return fiber.NewError(fiber.StatusBadRequest, "a password needs a username: upsd checks them as a pair")
	}
	was := nutTarget{password: boolStr(existing.PasswordEncrypted != ""), tlsMode: existing.TlsMode,
		verifyTLS: existing.VerifyTls, fingerprint: existing.TlsFingerprint}
	// The redirect gate's sibling: an acknowledgement alone must not send the
	// stored password over a weaker connection (TLS off, or no longer
	// verified), where whoever can watch the wire reads an account that may
	// run UPS commands; nor to a certificate the caller just pinned, which
	// scores as protected but is whichever one they chose. Re-entering it
	// shows the caller holds it already.
	pinChanged := params.TlsFingerprint != "" && params.TlsFingerprint != existing.TlsFingerprint
	if supplied == "" && params.PasswordEncrypted != "" &&
		(nutTransportRisk(target) > nutTransportRisk(was) || pinChanged && nutTransportRisk(was) == 0) {
		h.audit(c, existing, "nut_server_credential_downgrade_refused", map[string]any{"tls_mode": params.TlsMode})
		return fiber.NewError(fiber.StatusBadRequest,
			"Sending the password over a less protected connection requires re-entering the password. "+
				"The stored one is only ever sent as protected as when it was saved.")
	}
	if err := nutUpdateTransportAck(was, target, p.Bool("acknowledge_cleartext_credentials"), p.Bool("acknowledge_insecure_tls")); err != nil {
		return renderConfirmRequired(c, err)
	}

	if connectionChanged {
		probe, perr := h.probe(c.Context(), target.config())
		if perr != nil {
			h.audit(c, existing, "nut_server_connect_failed", map[string]any{
				"attempted_address": auditSafe(newAddr), "address_changed": newAddr != oldAddr,
				"password_supplied": supplied != "",
			})
			return renderNutProbeError(c, perr)
		}
		params.NutVersion = auditTruncate(probe.NutVersion, 32)
		params.ProtocolVersion = auditTruncate(probe.ProtocolVersion, 32)
		if supplied != "" {
			encrypted, eerr := crypto.Encrypt(supplied, h.encryptionKey)
			if eerr != nil {
				return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt password")
			}
			params.PasswordEncrypted = encrypted
		}
	}

	server, err := h.queries.UpdateNutServer(c.Context(), params)
	if err != nil {
		return nutWriteError(err, "update")
	}
	h.audit(c, server, "nut_server_updated", map[string]any{
		"address_changed":     newAddr != oldAddr,
		"password_rotated":    supplied != "",
		"credentials_cleared": clearCredentials,
		"tls_mode":            server.TlsMode,
		"enabled":             server.Enabled,
	})
	h.changed(c)
	return c.JSON(toNutServerResponse(server, true))
}

// boolStr turns "is there a password" into a non-empty marker for the
// transport check, which looks only at whether one is set.
func boolStr(b bool) string {
	if b {
		return "set"
	}
	return ""
}

// Delete handles DELETE /api/v1/nut-servers/:id. The readings go first: the
// query finds them through the devices, which the server's delete cascades.
func (h *NutHandler) Delete(c fiber.Ctx, p *apischema.Params) error {
	existing, err := h.fetchServer(c, p)
	if err != nil {
		return err
	}
	if err := h.queries.DeleteUpsMetricsForServer(c.Context(), existing.ID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete UPS readings")
	}
	if err := h.queries.DeleteNutServer(c.Context(), existing.ID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete NUT server")
	}
	h.audit(c, existing, "nut_server_deleted", nil)
	h.changed(c)
	return c.SendStatus(fiber.StatusNoContent)
}

// Test handles POST /api/v1/nut-servers/:id/test. It persists nothing.
func (h *NutHandler) Test(c fiber.Ctx, p *apischema.Params) error {
	server, err := h.fetchServer(c, p)
	if err != nil {
		return err
	}
	password := ""
	if server.PasswordEncrypted != "" {
		if password, err = crypto.Decrypt(server.PasswordEncrypted, h.encryptionKey); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt stored password")
		}
	}
	probe, err := h.probe(c.Context(), nut.Config{
		Host: server.Host, Port: int(server.Port), Username: server.Username, Password: password,
		TLS: nut.TLSMode(server.TlsMode), VerifyTLS: server.VerifyTls, Fingerprint: server.TlsFingerprint,
	})
	h.audit(c, server, "nut_server_tested", map[string]any{"succeeded": err == nil})
	if err != nil {
		return renderNutProbeError(c, err)
	}
	return c.JSON(probe)
}

func (h *NutHandler) fetchServer(c fiber.Ctx, p *apischema.Params) (db.NutServer, error) {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return db.NutServer{}, err
	}
	server, err := h.queries.GetNutServer(c.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.NutServer{}, fiber.NewError(fiber.StatusNotFound, "NUT server not found")
		}
		return db.NutServer{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get NUT server")
	}
	return server, nil
}

// changed tells the poller and open tabs that the server list moved.
func (h *NutHandler) changed(c fiber.Ctx) {
	if h.reload != nil {
		h.reload.TriggerInventory()
	}
	if h.eventPub != nil {
		h.eventPub.SystemEvent(c.Context(), events.KindUPSChange, "server_changed")
	}
}

// audit records a NUT server change. The cluster is NULL: a NUT server is not
// a per-cluster resource. Details never carry the password, nor the username,
// because view:audit is every Viewer's and audit details are readable there.
func (h *NutHandler) audit(c fiber.Ctx, server db.NutServer, action string, extra map[string]any) {
	details := map[string]any{"name": server.Name, "host": server.Host, "port": server.Port,
		"resource_name": auditTruncate(server.Name, maxAuditResourceNameLen)}
	for k, v := range extra {
		details[k] = v
	}
	blob, err := json.Marshal(details)
	if err != nil {
		blob = json.RawMessage(`{}`)
	}
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "nut_server", server.ID.String(), action, blob)
}
