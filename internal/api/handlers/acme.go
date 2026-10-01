package handlers

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// ACMEHandler handles ACME certificate management endpoints.
//
// All 18 routes are declared in internal/api/registry_acme.go, which states
// their permission (view:certificate for the eight reads, manage:certificate for
// the ten writes) and their parameters. Nothing below re-checks either.
type ACMEHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewACMEHandler creates a new ACMEHandler.
func NewACMEHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *ACMEHandler {
	return &ACMEHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *ACMEHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// staleDigestPhrases is PVE::Tools::assert_if_modified's die string, which is
// the only thing a digest mismatch produces: a plain 500 with no rejection map,
// so mapProxmoxError can only call it a gateway failure. It is not one — the
// node config changed under the caller, which is what 409 says, and it is the
// answer the compare-and-swap exists to give. Two operators editing one node's
// ACME settings produce it routinely.
//
// Note the digest covers the WHOLE node config file, not just the ACME keys, so
// an operator editing the node's Notes in the PVE UI can trigger this too, and
// so can a save of the node's options (NodeHandler.SetNodeOptions, which maps
// with the same mapper) — an ACME edit and an options edit each make the other's
// pending save conflict. The message says "changed" rather than naming ACME for
// that reason.
//
// The die string is pve-common's, not the node config's, so the firewall rule
// mapper (mapFirewallRuleError, firewall.go) matches the same phrase for a rule
// list digest that no longer matches.
var staleDigestPhrases = []string{"detected modified configuration"}

// proxmoxBodyTooLargePhrase is what pveproxy answers a request whose body is
// over its post limit: 501, with the reason written into the body (pve-http-server
// src/PVE/APIServer/AnyEvent.pm — authenticate_and_handle_request calls
// `$self->error($reqstate, 501, "for data too large")`, and error() puts `$msg`
// in the body when no content is given), so checkStatus hands back an APIError
// with StatusCode 501 and exactly this Message.
//
// The limit is `$limit_max_post` in the same file: 64 KiB before
// libpve-http-server-perl 5.2.1 (commit 2650923, "fix #6230: increase allowed
// post size", 2025-04-03, about Proxmox VE 8.4) and 512 KiB since. It is checked
// against Content-Length, so it is the FORM-ENCODED body that is counted: a
// line break takes three bytes (%0A) and a non-ASCII character at least six
// (%C3%A9 for é), which is how notes well inside the declared 65536 characters
// can be over it.
const proxmoxBodyTooLargePhrase = "for data too large"

// isProxmoxBodyTooLarge reports whether err is pveproxy refusing the size of the
// request body. Both halves are needed: 501 is also how pveproxy says "no such
// uri" and "method not available", and a 501 that carries another sentence is
// one of those, not this.
func isProxmoxBodyTooLarge(err error) bool {
	var apiErr *proxmox.APIError
	return errors.As(err, &apiErr) &&
		apiErr.StatusCode == fiber.StatusNotImplemented &&
		strings.Contains(strings.ToLower(apiErr.Message), proxmoxBodyTooLargePhrase)
}

// mapNodeConfigError adds the two answers a write of the node's config file can
// get that the shared mapping would call a gateway failure: the digest mismatch
// (409), and a body pveproxy refuses as too large (413). The first is the
// compare-and-swap doing its job; the second is the caller's own request, which
// no retry will shrink. It serves both writers of the file, the ACME settings and
// the node's options.
func mapNodeConfigError(err error) error {
	if isProxmoxBodyTooLarge(err) {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge, proxmoxRequestTooLargeMessage)
	}
	return mapProxmoxDieError(fiber.StatusConflict, nodeConfigChangedMessage, staleDigestPhrases, err)
}

// --- ACME Accounts ---

// ListAccounts handles GET /clusters/:cluster_id/acme/accounts.
func (h *ACMEHandler) ListAccounts(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	accounts, err := pxClient.GetACMEAccounts(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, accounts)
}

// defaultACMEAccountName is the name Proxmox gives an ACME account that is
// registered without one, and therefore the name such an account really exists
// under once POST .../acme/accounts returns.
//
// Upstream says so twice (pve-manager, read 2026-09-20):
//
//   - PVE/CertHelpers.pm, standard option 'pve-acme-account-name':
//     `optional => 1, default => 'default', format => 'pve-configid'`.
//   - PVE/API2/ACMEAccount.pm, register_method 'register_account' — the POST
//     /cluster/acme/account this handler dispatches:
//     `my $account_name = extract_param($param, 'name') // 'default';`
//
// That `//` is defined-or, not empty-or: it fires only when the form key is
// ABSENT. What turns an EMPTY name into an absent one is proxmox.CreateACMEAccount
// setting the key only `if params.Name != ""`. Drop that guard and an empty name
// arrives DEFINED, fails the pve-configid format (`qr/[a-z][a-z0-9_-]+/i` in
// pve-common src/PVE/JSONSchema.pm — note the /i, so uppercase is legal too;
// what an empty name fails is the two-character minimum) before
// register_account's body runs, and the route
// starts 400ing a request that has always worked. It is not this constant that
// goes wrong — the handler returns at the CreateACMEAccount error and records
// nothing — but the guard is load-bearing all the same, so
// TestCreateACMEAccountOmitsAnEmptyName pins it.
const defaultACMEAccountName = "default"

// acmeAccountNameForAudit is the account name to RECORD for a create.
//
// An omitted or empty name is valid input on this route — registry_acme.go
// declares it optional and permits "" — so the four sinks below used to record
// the "" the caller sent, naming no account at all while the account existed
// under defaultACMEAccountName. view:audit is granted to every Viewer by
// default, so that row is the one most readers see.
//
// The substitution is paired with the name_defaulted detail at the call site
// rather than left implicit: "Proxmox named it" and "the caller asked for
// default" are different facts, and only the flag separates them.
func acmeAccountNameForAudit(requested string) string {
	if requested == "" {
		return defaultACMEAccountName
	}
	return requested
}

// CreateAccount handles POST /clusters/:cluster_id/acme/accounts.
func (h *ACMEHandler) CreateAccount(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := proxmox.CreateACMEAccountParams{
		Name:      p.String("name"),
		Contact:   p.String("contact"),
		Directory: p.String("directory"),
		TOSUrl:    p.String("tos_url"),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.CreateACMEAccount(c.Context(), req)
	if err != nil {
		return mapProxmoxError(err)
	}
	// The name the account exists under, which for an omitted or empty
	// req.Name is not req.Name. Computed once so the four sinks below cannot
	// drift apart.
	name := acmeAccountNameForAudit(req.Name)
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         extractNodeFromUPID(upid),
		ResourceType: "acme_account",
		ResourceID:   name,
		Action:       "created",
		UPID:         upid,
		Description:  "Create ACME account " + name,
		Extra: map[string]any{
			"name": name,
			// Whether the name above is Proxmox's default rather than one the
			// caller chose. Without it the row cannot tell the two apart.
			"name_defaulted": req.Name == "",
			"contact":        req.Contact,
		},
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_account", name, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"upid": upid})
}

// GetAccount handles GET /clusters/:cluster_id/acme/accounts/:name.
func (h *ACMEHandler) GetAccount(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name := p.String("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	account, err := pxClient.GetACMEAccount(c.Context(), name)
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(account)
}

// UpdateAccount handles PUT /clusters/:cluster_id/acme/accounts/:name.
func (h *ACMEHandler) UpdateAccount(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name := p.String("name")
	req := proxmox.UpdateACMEAccountParams{Contact: p.String("contact")}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateACMEAccount(c.Context(), name, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "acme_account", name, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_account", name, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteAccount handles DELETE /clusters/:cluster_id/acme/accounts/:name.
func (h *ACMEHandler) DeleteAccount(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	name := p.String("name")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteACMEAccount(c.Context(), name); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"name": name})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "acme_account", name, "deleted", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_account", name, "deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- ACME Plugins ---

// ListPlugins handles GET /clusters/:cluster_id/acme/plugins.
func (h *ACMEHandler) ListPlugins(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	plugins, err := pxClient.GetACMEPlugins(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	// The dns-01 plugin "data" field holds the DNS provider's API credentials.
	// Never return it on a view-gated read; expose only whether it's configured.
	for i := range plugins {
		plugins[i].Data = ""
	}
	return RespondItems(c, plugins)
}

// CreatePlugin handles POST /clusters/:cluster_id/acme/plugins.
func (h *ACMEHandler) CreatePlugin(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := proxmox.CreateACMEPluginParams{
		// plugin_id, not "id": the declaration renames it and keeps "id" as an
		// alias, because a body parameter called "id" is a name the permission
		// middleware also reads. See createACMEPluginParams.
		ID:   p.String("plugin_id"),
		Type: p.String("type"),
		API:  p.String("api"),
		Data: p.String("data"),
		// Tri-state: omitted leaves Proxmox's own validation delay in force.
		ValidationDelay: optIntPtr(p.OptInt("validation-delay")),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateACMEPlugin(c.Context(), req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": req.ID, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "acme_plugin", req.ID, "created", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_plugin", req.ID, "created")
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// UpdatePlugin handles PUT /clusters/:cluster_id/acme/plugins/:plugin_id.
func (h *ACMEHandler) UpdatePlugin(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pluginID := p.String("plugin_id")
	req := proxmox.UpdateACMEPluginParams{
		API:             p.String("api"),
		Data:            p.String("data"),
		ValidationDelay: optIntPtr(p.OptInt("validation-delay")),
		Digest:          p.String("digest"),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateACMEPlugin(c.Context(), pluginID, req); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": pluginID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "acme_plugin", pluginID, "updated", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_plugin", pluginID, "updated")
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeletePlugin handles DELETE /clusters/:cluster_id/acme/plugins/:plugin_id.
func (h *ACMEHandler) DeletePlugin(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pluginID := p.String("plugin_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteACMEPlugin(c.Context(), pluginID); err != nil {
		return mapProxmoxError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": pluginID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "acme_plugin", pluginID, "deleted", details)
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "acme_plugin", pluginID, "deleted")
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- ACME Challenge Schema, Directories & TOS ---

// ListChallengeSchema handles GET /clusters/:cluster_id/acme/challenge-schema.
func (h *ACMEHandler) ListChallengeSchema(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	// Decoded rather than relayed verbatim: this is a collection, and every
	// collection goes out in the ListResponse envelope. c.Send(raw) shipped the
	// bare PVE array straight through — the one response in this package that
	// TestGuard_ListEndpointsUseEnvelope could not see, because it inspects
	// c.JSON arguments and this never called c.JSON.
	//
	// The typed client method already existed alongside the raw one; the raw
	// variant is gone, so there is one way to read this endpoint.
	schema, err := pxClient.GetACMEChallengeSchema(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, schema)
}

// ListDirectories handles GET /clusters/:cluster_id/acme/directories.
func (h *ACMEHandler) ListDirectories(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	dirs, err := pxClient.GetACMEDirectories(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, dirs)
}

// GetTOS handles GET /clusters/:cluster_id/acme/tos.
func (h *ACMEHandler) GetTOS(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	tos, err := pxClient.GetACMETOS(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	return c.JSON(fiber.Map{"url": tos})
}

// --- Node ACME Config ---

// GetNodeACMEConfig handles GET /clusters/:cluster_id/nodes/:node/acme-config.
//
// The `digest` it returns is the save token (node_config_token.go), and only to a
// caller who holds manage:certificate, the permission the compare-and-swap PUT is
// gated on. Proxmox's own digest is never returned. It is the unsalted SHA1 of the
// WHOLE node config file, which holds the node's notes, and Proxmox writes that
// file deterministically (the notes as '#' lines first, then every other key
// sorted), so a caller who can read the rest of it can hash a guess at the notes
// and compare, offline, with no rate limit and no audit row. A built-in Viewer
// holds view:certificate and so reads the ACME settings here, and view:node and so
// reads the other settings through .../options; and a caller holding
// manage:certificate without manage:node would otherwise have the digest, the rest
// of the file and a PUT that tells a hit from a miss by its 409. The token is an
// HMAC of the digest under a key only Nexara has, bound to the cluster and the
// node, and nothing can be tested against it. The settings read
// (NodeHandler.GetNodeOptions) returns the same token to its writers, and the two
// have to agree on all of it, or the oracle stays open through the one that does
// not. The notes are read under manage:node because old ones sometimes hold
// credentials.
//
// It is withheld from a caller who cannot write as well, which would not strictly
// be needed: a token that changed whenever the file did would tell a Viewer for
// free whether a node has notes and when they change, and a Viewer has no use for
// one. The permission is the path's cluster's, read with hasClusterPerm
// (TestNodeConfigTokenPermissionIsScopedToThePathsCluster).
//
// The permission is read before anything is sent to Proxmox, and a lookup that
// fails fails the request: leaving the token out instead would turn a manager's
// next save into an unconditional write, the compare-and-swap failing open.
func (h *ACMEHandler) GetNodeACMEConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	node := p.String("node")
	canWrite, err := hasClusterPerm(c, "manage", "certificate", clusterID)
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	cfg, err := pxClient.GetNodeACMEConfig(c.Context(), node)
	if err != nil {
		return mapProxmoxError(err)
	}
	// cfg.Digest is Proxmox's raw digest, and is replaced, not filtered: what the
	// response carries is the token, or nothing. omitempty makes "nothing" an
	// absent key, so the page cannot mistake a missing token for one to send back.
	token := ""
	if canWrite {
		if token, err = readNodeConfigToken(h.encryptionKey, clusterID, node, cfg.Digest); err != nil {
			return err
		}
	}
	cfg.Digest = token
	return c.JSON(cfg)
}

// SetNodeACMEConfig handles PUT /clusters/:cluster_id/nodes/:node/acme-config.
func (h *ACMEHandler) SetNodeACMEConfig(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	node := p.String("node")
	// Digest is what a read returned in its `digest` field, the save token; it is
	// turned into the digest Proxmox compares below.
	req := proxmox.NodeACMEConfig{
		ACME:        p.String("acme"),
		ACMEDomain0: p.String("acmedomain0"),
		ACMEDomain1: p.String("acmedomain1"),
		ACMEDomain2: p.String("acmedomain2"),
		ACMEDomain3: p.String("acmedomain3"),
		ACMEDomain4: p.String("acmedomain4"),
		ACMEDomain5: p.String("acmedomain5"),
		Delete:      p.Strings("delete"),
		Digest:      p.String("digest"),
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	// The delete allow-list, the "set and cleared in one request" refusal, the
	// "nothing to change" refusal and the size refusal live in
	// proxmox.SetNodeACMEConfig, not here. They are choke-point checks that protect
	// every caller of the client — see deletableNodeACMEKeys — and the schema
	// deliberately does not restate any of them: it bounds the list, it does not
	// decide what may be in it.
	//
	// They run again here, ahead of the save check, and not only inside
	// SetNodeACMEConfig where they have to stay: the check re-reads the node's
	// digest from Proxmox, and a request that is going to be refused anyway should
	// cost that read nothing. The digest is swapped for a 40-character placeholder,
	// since the one the request carries is about to be replaced by Proxmox's own,
	// which is that long, and the size refusal has to count the request as Proxmox
	// will be sent it (nodeConfigValidationDigest). They sit after
	// createProxmoxClient like every refusal, which is what the route sweep needs.
	checked := req
	checked.Digest = nodeConfigValidationDigest(req.Digest)
	if err := proxmox.ValidateNodeACMEConfig(checked); err != nil {
		return mapNodeConfigError(err)
	}
	// The save check: a token is only good for the file as it was when it was
	// read, and what goes to Proxmox is the file's digest as it is now. A caller
	// that echoes back what a read returned is unaffected; one that sends Proxmox's
	// own raw digest, which no read of Nexara's returns, is always told 409.
	req.Digest, err = nodeConfigSaveDigest(c.Context(), pxClient, h.encryptionKey, clusterID, node, req.Digest)
	if err != nil {
		return err
	}
	// The audit row is built from a copy without the digest, taken here, before the
	// write: req.Digest is Proxmox's raw digest from this point on, and nothing
	// after the write may read req (nodeACMEConfigForAudit;
	// TestGuard_NodeConfigAuditIsBuiltFromACopyWithoutTheDigest).
	audited := nodeACMEConfigForAudit(req)
	if err := pxClient.SetNodeACMEConfig(c.Context(), node, req); err != nil {
		return mapNodeConfigError(err)
	}
	// Names only, never values — networks.go splits its row the same way,
	// though its helper is unexported and local while this one has to live in
	// internal/proxmox beside the table it reads, so the key list stays single.
	// Without these the row says "updated" and names nothing, and for a clear
	// the settings it removed are exactly the ones no later read can recover.
	// Values are ACME domains and view:audit is granted to every Viewer.
	//
	// Both keys are non-nil slices so "nothing set" and "nothing cleared" read
	// as [] rather than one of them being null: a reader filtering on the row
	// should not have to handle two spellings of empty.
	cleared := audited.Delete
	if cleared == nil {
		cleared = []string{}
	}
	details, _ := json.Marshal(map[string]any{
		"node":     node,
		"settings": proxmox.NodeACMESetKeys(audited),
		"cleared":  cleared,
	})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "acme_config", node, "updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// --- Node Certificates ---

// ListNodeCertificates handles GET /clusters/:cluster_id/nodes/:node/certificates.
func (h *ACMEHandler) ListNodeCertificates(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	node := p.String("node")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	certs, err := pxClient.GetNodeCertificates(c.Context(), node)
	if err != nil {
		return mapProxmoxError(err)
	}
	return RespondItems(c, certs)
}

// OrderNodeCertificate handles POST /clusters/:cluster_id/nodes/:node/certificates/order.
func (h *ACMEHandler) OrderNodeCertificate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	node := p.String("node")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.OrderNodeCertificate(c.Context(), node, p.Bool("force"))
	if err != nil {
		return mapProxmoxError(err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         node,
		ResourceType: "certificate",
		ResourceID:   node,
		Action:       "ordered",
		UPID:         upid,
		Description:  "Order certificate for " + node,
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "certificate", node, "ordered")
	return c.JSON(fiber.Map{"upid": upid})
}

// RenewNodeCertificate handles PUT /clusters/:cluster_id/nodes/:node/certificates/renew.
func (h *ACMEHandler) RenewNodeCertificate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	node := p.String("node")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.RenewNodeCertificate(c.Context(), node, p.Bool("force"))
	if err != nil {
		return mapProxmoxError(err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         node,
		ResourceType: "certificate",
		ResourceID:   node,
		Action:       "renewed",
		UPID:         upid,
		Description:  "Renew certificate for " + node,
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "certificate", node, "renewed")
	return c.JSON(fiber.Map{"upid": upid})
}

// RevokeNodeCertificate handles DELETE /clusters/:cluster_id/nodes/:node/certificates/revoke.
func (h *ACMEHandler) RevokeNodeCertificate(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	node := p.String("node")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.RevokeNodeCertificate(c.Context(), node)
	if err != nil {
		return mapProxmoxError(err)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         node,
		ResourceType: "certificate",
		ResourceID:   node,
		Action:       "revoked",
		UPID:         upid,
		Description:  "Revoke certificate for " + node,
	})
	h.eventPub.ClusterEvent(c.Context(), clusterID.String(), events.KindACMEChange, "certificate", node, "revoked")
	return c.JSON(fiber.Map{"upid": upid})
}
