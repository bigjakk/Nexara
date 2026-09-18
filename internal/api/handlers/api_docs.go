package handlers

import (
	"sort"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// APIDocsHandler serves the API endpoint catalog. Endpoints are
// auto-discovered from Fiber's registered route table at request time
// so the docs cannot drift away from what the server actually serves.
//
// Each route's prose comes from one of two places, in this order:
//
//   - the DECLARATION, for a route the endpoint registry owns. The
//     registry states a route's description, group, permission and full
//     parameter schema next to the route itself, so the declaration is
//     the single source of truth and nothing here can disagree with it.
//     Package `api` pushes those declarations in via
//     [APIDocsHandler.SetDeclaredEndpoints] — see the note on that
//     method for why they arrive rather than being read.
//   - `endpointMeta`, the hand-curated overlay keyed on
//     `METHOD<space>path`, for the routes still registered imperatively
//     in router.go. Routes in neither get an auto-derived group and an
//     empty description, which is the signal that a curator should fill
//     them in — or, better, that the route should be migrated.
//
// Phase 5.8: previously a 134-entry hand-typed list that had drifted
// (~50% of registered routes were missing). Auto-generation from the
// route table was chosen over deletion because the per-page UX
// (search, grouping, method colours) is genuinely useful to operators
// scripting against the API.
type APIDocsHandler struct {
	app *fiber.App

	// declared holds the registry's declarations, keyed "METHOD path"
	// exactly as the route is registered. Written once at startup,
	// read-only from then on, like app.
	declared map[string]APIEndpoint
}

// NewAPIDocsHandler creates a new API docs handler. The app reference
// is the per-server Fiber instance whose routes the handler enumerates;
// it is wired in router.go::registerRoutes once the v1 group has been
// fully constructed.
func NewAPIDocsHandler() *APIDocsHandler { return &APIDocsHandler{} }

// SetApp attaches the Fiber app whose routes this handler enumerates.
// Called by the server once all routes are registered.
func (h *APIDocsHandler) SetApp(app *fiber.App) { h.app = app }

// SetDeclaredEndpoints hands the handler the endpoint registry's
// declarations. It is the same shape as SetApp and exists for the same
// structural reason: the registry lives in package `api`, which already
// imports this package, so `handlers` cannot import it back. The
// dependency is inverted instead — this package DEFINES the payload and
// package `api` FILLS it, at startup, from router.go's registry block.
//
// Passing the already-rendered payload rather than the Endpoint values
// also keeps the docs free of the registry's internals: nothing here has
// to know what a Permissions declaration is or how a parameter's source
// is resolved.
//
// eps is keyed internally on "METHOD path"; a duplicate key would be a
// registry that registered the same route twice, which Register already
// refuses.
func (h *APIDocsHandler) SetDeclaredEndpoints(eps []APIEndpoint) {
	m := make(map[string]APIEndpoint, len(eps))
	for _, e := range eps {
		// Keyed through the SAME normalisation GetDocs applies to the
		// route-table path before it looks a declaration up. Keying on
		// the raw path instead is a silent miss for any declaration
		// mounted at ".../foo/": the lookup would ask for ".../foo",
		// find nothing, fall through to endpointMeta, find nothing
		// there either, and render the endpoint blank.
		m[e.Method+" "+NormalizeDocPath(e.Path)] = e
	}
	h.declared = m
}

// DeclaredEndpointKeys returns the sorted "METHOD path" keys the
// registry declared. It mirrors EndpointMetaKeys and exists for the same
// guard test in internal/api: a declaration whose path does not match a
// registered route renders nothing, silently, exactly as a stale
// endpointMeta key does.
func (h *APIDocsHandler) DeclaredEndpointKeys() []string {
	keys := make([]string, 0, len(h.declared))
	for k := range h.declared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// APIEndpoint describes a single API endpoint.
type APIEndpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Permission  string `json:"permission"`
	Group       string `json:"group"`

	// Parameters is the route's full request contract — path, query and
	// body alike. It is empty for a legacy route, and that emptiness is
	// honest rather than a placeholder: the route genuinely has no
	// machine-readable schema, and the gap is what tells a reader which
	// endpoints have been migrated.
	Parameters []APIParameter `json:"parameters,omitempty"`
}

// APIParameter is one parameter of a declared endpoint, rendered as what
// a caller needs in order to form a request.
//
// Optional and Default are two separate facts and must stay that way.
// "Optional, no default" means the endpoint does something else when the
// caller says nothing — disks/attach picks the lowest free slot when
// `index` is omitted — while "optional, default 0" means it behaves as
// if the caller had asked for slot 0, i.e. the boot disk. Collapsing the
// two is precisely the ambiguity that destroyed a live VM's boot disk,
// and it is why Default is omitted from the JSON when absent rather than
// rendered as a zero value. There is no third state to encode: apischema
// refuses a declaration that is required AND carries a default.
//
// The same absent-versus-zero rule governs every bound below, and for
// the same reason: `minimum: 0` is a real floor, `minimum` absent is no
// floor, and a caller cannot tell them apart if the zero value stands in
// for both.
type APIParameter struct {
	Name string `json:"name"`
	Type string `json:"type"`

	// Source is where the value goes on the wire: "path", "query" or
	// "body". Nothing documented it before, which is why every
	// query-string parameter in this API was invisible to a reader.
	Source string `json:"source"`

	Optional bool `json:"optional"`

	// Default is omitted entirely when the parameter has none. Test
	// TestAPIParameterJSON_OptionalWithAndWithoutDefault pins that,
	// including for a `false` / `0` default, which must still render.
	Default any `json:"default,omitempty"`

	Enum        []string `json:"enum,omitempty"`
	Format      string   `json:"format,omitempty"`
	Typetext    string   `json:"typetext,omitempty"`
	Description string   `json:"description,omitempty"`

	// Pattern is the regex a string value must match. Undocumented, it is
	// the same failure the disk-attach incident was: `snap_name` may not
	// start with a digit, and a caller sending "1abc" got a 400 nothing in
	// the docs let them predict.
	Pattern string `json:"pattern,omitempty"`

	// The bounds are POINTERS for the reason Default is omitted when
	// absent: a minimum of 0 is a real bound and must not be
	// indistinguishable from "no bound". omitempty on a pointer drops it
	// only when nil, so *0 renders as `"minimum": 0` and an absent bound
	// renders as nothing at all.
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
	MinLength *int     `json:"min_length,omitempty"`
	MaxLength *int     `json:"max_length,omitempty"`

	// Alias is a second name the endpoint also accepts for this
	// parameter. Omitting it from the docs is worse than merely
	// incomplete: it documents the endpoint as REJECTING input it
	// accepts.
	Alias string `json:"alias,omitempty"`

	// Items is the element schema when Type is "array". Without it the
	// table says "array" and stops, which does not tell a caller what to
	// put in one.
	Items *APIItems `json:"items,omitempty"`

	// Requires names the parameters a caller must supply ALONGSIDE this
	// one. A companion carrying only its default does not satisfy it.
	Requires []string `json:"requires,omitempty"`
}

// APIItems is an array parameter's element schema.
//
// It is a type of its own rather than a nested APIParameter, because an
// element is not a parameter and the schema engine says so: apischema's
// compileItems accepts only scalar element types and REFUSES an element
// that declares a name's worth of context — optionality, a default, a
// source, an alias or a requires. A nested APIParameter would carry all
// six as fields that can only ever be empty, and would invite a reader to
// fill one in.
type APIItems struct {
	Type        string   `json:"type"`
	Enum        []string `json:"enum,omitempty"`
	Format      string   `json:"format,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	Typetext    string   `json:"typetext,omitempty"`
	Description string   `json:"description,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	MinLength   *int     `json:"min_length,omitempty"`
	MaxLength   *int     `json:"max_length,omitempty"`
}

// endpointMeta is the curated overlay for the routes router.go still
// registers imperatively: rich descriptions + permission + group for
// routes that have them. The handler pulls the canonical route list from
// `app.GetRoutes()` and decorates entries from this map. Routes not
// present here get auto-derived metadata — `Group` from the second
// `/api/v1/...` segment, blank description, blank permission. New
// endpoints inherit those defaults until a curator adds an entry here.
//
// A route the endpoint registry declares does NOT read this map: its
// declaration wins outright (see GetDocs). Entries here for a migrated
// route are the leftover second copy, kept in step by
// TestGuard_DocumentedPermissionMatchesEnforcement until they are
// removed.
//
// Key format: "METHOD path" exactly as the route is registered (e.g.
// "GET /api/v1/clusters/:id"). The path is matched against
// `fiber.Route.Path` which uses ":param" syntax, so param NAMES must
// match the router registration verbatim (":cluster_id", not ":id") —
// a lookup miss silently renders the route with blank metadata.
// `TestEndpointMetaMatchesRegisteredRoutes` (internal/api) fails the
// build when a key here stops matching a registered route.
var endpointMeta = map[string]APIEndpoint{
	// ── Authentication ────────────────────────────────────────────────
	"POST /api/v1/auth/register":            {Description: "Register a new user account", Group: "Authentication"},
	"POST /api/v1/auth/login":               {Description: "Authenticate with email and password", Group: "Authentication"},
	"POST /api/v1/auth/refresh":             {Description: "Refresh an expired access token", Group: "Authentication"},
	"POST /api/v1/auth/logout":              {Description: "End the current session", Group: "Authentication"},
	"POST /api/v1/auth/logout-all":          {Description: "End all sessions for the current user", Group: "Authentication"},
	"GET /api/v1/auth/sessions":             {Description: "List the caller's own active sessions", Group: "Authentication"},
	"DELETE /api/v1/auth/sessions/:id":      {Description: "Revoke one of the caller's own sessions", Group: "Authentication"},
	"POST /api/v1/auth/console-token":       {Description: "Mint a short-lived scope-locked JWT for a specific console WebSocket (mobile)", Permission: "console:node|console:vm|console:container", Group: "Authentication"},
	"POST /api/v1/auth/ws-token":            {Description: "Mint a short-lived JWT for the /ws hub upgrade (subscription channels)", Group: "Authentication"},
	"GET /api/v1/auth/me":                   {Description: "Get current user profile", Group: "Authentication"},
	"PUT /api/v1/auth/profile":              {Description: "Update display name", Group: "Authentication"},
	"POST /api/v1/auth/change-password":     {Description: "Change account password", Group: "Authentication"},
	"GET /api/v1/auth/sso-status":           {Description: "Check if SSO is available", Group: "Authentication"},
	"GET /api/v1/auth/setup-status":         {Description: "Check if initial setup is needed", Group: "Authentication"},
	"GET /api/v1/auth/oidc/authorize":       {Description: "Begin OIDC SSO authorization flow (PKCE+state+nonce)", Group: "Authentication"},
	"GET /api/v1/auth/oidc/callback":        {Description: "OIDC callback redirect target", Group: "Authentication"},
	"POST /api/v1/auth/oidc/token-exchange": {Description: "Exchange the OIDC one-time code for an access+refresh token pair", Group: "Authentication"},

	// ── Mobile Devices ────────────────────────────────────────────────

	// ── Two-Factor Authentication ─────────────────────────────────────
	"POST /api/v1/auth/totp/setup":                     {Description: "Generate TOTP setup QR code", Group: "Two-Factor Authentication"},
	"POST /api/v1/auth/totp/setup/verify":              {Description: "Confirm TOTP enrollment with a code", Group: "Two-Factor Authentication"},
	"DELETE /api/v1/auth/totp":                         {Description: "Disable two-factor authentication", Group: "Two-Factor Authentication"},
	"GET /api/v1/auth/totp/status":                     {Description: "Get 2FA enrollment status", Group: "Two-Factor Authentication"},
	"POST /api/v1/auth/totp/verify-login":              {Description: "Complete login with TOTP code", Group: "Two-Factor Authentication"},
	"POST /api/v1/auth/totp/recovery-codes/regenerate": {Description: "Generate new recovery codes", Group: "Two-Factor Authentication"},
	"DELETE /api/v1/users/:id/totp":                    {Description: "Admin: clear another user's 2FA enrollment", Permission: "manage:user", Group: "Two-Factor Authentication"},

	// ── API Keys ──────────────────────────────────────────────────────
	"POST /api/v1/api-keys":       {Description: "Create a personal API key", Permission: "manage:api_key", Group: "API Keys"},
	"GET /api/v1/api-keys":        {Description: "List your API keys", Permission: "manage:api_key", Group: "API Keys"},
	"DELETE /api/v1/api-keys/:id": {Description: "Revoke an API key", Permission: "manage:api_key", Group: "API Keys"},
	"DELETE /api/v1/api-keys":     {Description: "Revoke all your API keys", Permission: "manage:api_key", Group: "API Keys"},

	// ── Favorites ─────────────────────────────────────────────────────
	"GET /api/v1/favorites":    {Description: "List your starred clusters, nodes and guests", Permission: "view:cluster|view:node|view:vm", Group: "Favorites"},
	"POST /api/v1/favorites":   {Description: "Star a cluster, node or guest", Permission: "view:cluster|view:node|view:vm", Group: "Favorites"},
	"DELETE /api/v1/favorites": {Description: "Unstar one of your own favorites", Group: "Favorites"},

	// ── Clusters ──────────────────────────────────────────────────────
	"GET /api/v1/clusters":                    {Description: "List all clusters", Permission: "view:cluster", Group: "Clusters"},
	"POST /api/v1/clusters":                   {Description: "Add a Proxmox cluster, with a pasted API token or a Nexara-minted one", Permission: "manage:cluster", Group: "Clusters"},
	"GET /api/v1/clusters/:id":                {Description: "Get cluster details", Permission: "view:cluster", Group: "Clusters"},
	"PUT /api/v1/clusters/:id":                {Description: "Update cluster settings", Permission: "manage:cluster", Group: "Clusters"},
	"DELETE /api/v1/clusters/:id":             {Description: "Remove a cluster, optionally revoking the credential Nexara created for it", Permission: "delete:cluster", Group: "Clusters"},
	"POST /api/v1/clusters/fetch-fingerprint": {Description: "Fetch a remote host's TLS fingerprint. Shared by the cluster, PBS and Veeam add-flows", Permission: "manage:cluster|manage:pbs|manage:veeam", Group: "Clusters"},

	// ── Nodes ─────────────────────────────────────────────────────────
	"GET /api/v1/clusters/:cluster_id/nodes":                    {Description: "List cluster nodes", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/syslog":  {Description: "Read a node's syslog over a time window (since/until)", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/journal": {Description: "Read a node's systemd journal by line count (lastentries) or cursor", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/report":  {Description: "Download a node's pvereport support bundle as plain text", Permission: "manage:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/sensors": {Description: "Read a node's hardware temperatures from its hwmon sensors over SSH", Permission: "view:node", Group: "Nodes"},

	// ── Virtual Machines ──────────────────────────────────────────────
	"GET /api/v1/clusters/:cluster_id/vms":                                       {Description: "List all VMs in a cluster", Permission: "view:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms":                                      {Description: "Create a new VM", Permission: "manage:vm", Group: "Virtual Machines"},
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id":                                {Description: "Get VM details", Permission: "view:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/status":                        {Description: "Change VM power state", Permission: "execute:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/clone":                         {Description: "Clone a VM", Permission: "manage:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/migrate":                       {Description: "Migrate VM to another node", Permission: "execute:vm", Group: "Virtual Machines"},
	"DELETE /api/v1/clusters/:cluster_id/vms/:vm_id":                             {Description: "Destroy a VM", Permission: "delete:vm", Group: "Virtual Machines"},
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/snapshot-capability":            {Description: "Check whether the VM's configuration supports snapshots", Permission: "view:vm", Group: "Virtual Machines"},
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots":                      {Description: "List VM snapshots", Permission: "view:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots":                     {Description: "Create a snapshot", Permission: "execute:vm", Group: "Virtual Machines"},
	"DELETE /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots/:snap_name":        {Description: "Delete a snapshot", Permission: "delete:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots/:snap_name/rollback": {Description: "Rollback to snapshot", Permission: "execute:vm", Group: "Virtual Machines"},
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/config":                         {Description: "Get VM configuration", Permission: "view:vm", Group: "Virtual Machines"},
	"PUT /api/v1/clusters/:cluster_id/vms/:vm_id/config":                         {Description: "Update VM configuration", Permission: "manage:vm", Group: "Virtual Machines"},
	// Both of these serve either guest kind through a /vms/ path — Nexara
	// keeps VMs and containers in one inventory table — so converting a
	// CONTAINER through them needs container rights as well. The
	// Permission field carries only the statically-required half; the
	// conditional half is in the description, because an operator building
	// a role needs to read it and the field has no vocabulary for "and, if
	// it is a container, also".
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/convert-to-template": {Description: "Convert a stopped VM or container into a template. Irreversible in Proxmox. Converting a container additionally requires manage:container", Permission: "manage:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/clone-to-template":   {Description: "Clone a guest and convert the clone into a template once it settles. Cloning a container additionally requires manage:container", Permission: "manage:vm", Group: "Virtual Machines"},
	"GET /api/v1/clusters/:cluster_id/vms/:vm_id/agent":                {Description: "Read the guest agent's reported OS and network interfaces. Answers running=false when no agent responds", Permission: "view:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/resize":        {Description: `Grow a VM disk. Takes Proxmox's resize format: "64G" to grow to, "+8G" to grow by. Proxmox cannot shrink a disk`, Permission: "manage:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/move":          {Description: "Move a VM disk onto another storage, optionally converting its format", Permission: "manage:vm", Group: "Virtual Machines"},
	// The description this endpoint did NOT have is what an external
	// consumer went without before destroying a VM's boot disk through it:
	// they omitted index, got slot 0, and overwrote scsi0. Every rule the
	// endpoint now enforces is stated here, and size's accepted spellings
	// most of all — "500G" used to be a parse error on the Proxmox side and
	// "512000" used to allocate 500 TiB.
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/attach": {Description: `Allocate a new disk and attach it. Omit index to take the lowest FREE slot on the bus; an explicitly named slot that is occupied, or that the VM boots from, is refused. size accepts 500, "500", "500G", "512M" or "1T" and may not exceed the target pool's total capacity`, Permission: "manage:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/detach": {Description: "Detach a disk. Proxmox keeps the volume as an unused disk rather than deleting it", Permission: "manage:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/media":        {Description: `Mount an ISO on the VM's CD-ROM device, or eject it with volid "none"`, Permission: "execute:vm", Group: "Virtual Machines"},
	"PUT /api/v1/clusters/:cluster_id/vms/:vm_id/pool":          {Description: "Move a guest into a Proxmox resource pool, or out of its current one with an empty pool", Permission: "manage:pool", Group: "Virtual Machines"},
	// Filed under Virtual Machines rather than a section of its own, and
	// the endpoint's own Group in internal/api/registry_vms.go says the
	// same. The declaration is now what GetDocs renders, so this entry no
	// longer decides the section — it is the shadow copy, and it is kept
	// saying the same thing so that removing it is a deletion rather than
	// a behaviour change.
	"GET /api/v1/clusters/:cluster_id/pools": {Description: "List the cluster's Proxmox resource pools", Permission: "view:cluster", Group: "Virtual Machines"},

	// ── Node hardware, for the VM dialogs ─────────────────────────────
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/bridges":       {Description: "List a node's network bridges, for picking a VM's NIC", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/hardware/usb":  {Description: "List a node's USB devices, for passthrough", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/hardware/pci":  {Description: "List a node's PCI devices, for passthrough", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/machine-types": {Description: "List the QEMU machine types a node offers", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/cpu-models":    {Description: "List the CPU models a node offers. Empty on Proxmox versions without the endpoint", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/cpu-flags":     {Description: "List the CPU flags a node offers, with which nodes support each", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_name/isos":          {Description: "List every ISO on a node's ISO-capable storages", Permission: "view:node", Group: "Nodes"},

	// ── Containers ────────────────────────────────────────────────────
	//
	// Every entry below is a SHADOW COPY: all 18 container routes are
	// declared in internal/api/registry_containers.go, and GetDocs renders
	// a declared route from its declaration, so what a reader sees is the
	// declaration's description, permission, group and full parameter
	// schema — not these lines. They are kept saying the same thing about
	// the permission so that removing them is a deletion rather than a
	// behaviour change, and because
	// TestGuard_DocumentedPermissionMatchesEnforcement compares this copy
	// against the declared one: a second copy that nothing compares is a
	// second copy that silently rots.
	//
	// Three container routes never had an entry here at all — the two
	// template routes and volumes/move — and now get their docs from the
	// declaration, which is the point of the migration rather than an
	// omission to fix by adding more shadow copies.
	"GET /api/v1/clusters/:cluster_id/containers":                                       {Description: "List all containers", Permission: "view:container", Group: "Containers"},
	"POST /api/v1/clusters/:cluster_id/containers":                                      {Description: "Create a container", Permission: "manage:container", Group: "Containers"},
	"GET /api/v1/clusters/:cluster_id/containers/:ct_id":                                {Description: "Get container details", Permission: "view:container", Group: "Containers"},
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/status":                        {Description: "Change container power state", Permission: "execute:container", Group: "Containers"},
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/clone":                         {Description: "Clone a container", Permission: "manage:container", Group: "Containers"},
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/migrate":                       {Description: "Migrate container", Permission: "execute:container", Group: "Containers"},
	"DELETE /api/v1/clusters/:cluster_id/containers/:ct_id":                             {Description: "Destroy a container", Permission: "delete:container", Group: "Containers"},
	"GET /api/v1/clusters/:cluster_id/containers/:ct_id/snapshot-capability":            {Description: "Check whether the container's configuration supports snapshots", Permission: "view:container", Group: "Containers"},
	"GET /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots":                      {Description: "List container snapshots", Permission: "view:container", Group: "Containers"},
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots":                     {Description: "Create a snapshot", Permission: "execute:container", Group: "Containers"},
	"DELETE /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots/:snap_name":        {Description: "Delete a snapshot", Permission: "delete:container", Group: "Containers"},
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/snapshots/:snap_name/rollback": {Description: "Rollback to snapshot", Permission: "execute:container", Group: "Containers"},
	"GET /api/v1/clusters/:cluster_id/containers/:ct_id/config":                         {Description: "Get container config", Permission: "view:container", Group: "Containers"},
	"PUT /api/v1/clusters/:cluster_id/containers/:ct_id/config":                         {Description: "Update container config", Permission: "manage:container", Group: "Containers"},
	"POST /api/v1/clusters/:cluster_id/containers/:ct_id/disks/resize":                  {Description: "Resize container disk", Permission: "manage:container", Group: "Containers"},

	// ── Guest Snapshots ───────────────────────────────────────────────
	"GET /api/v1/guest-snapshots":                              {Description: "List collected guest snapshots across all clusters (QEMU rows need view:vm, LXC rows view:container)", Permission: "view:vm|view:container", Group: "Guest Snapshots"},
	"POST /api/v1/clusters/:cluster_id/guest-snapshots/resync": {Description: "Refresh one guest's snapshot inventory from Proxmox", Permission: "view:vm|view:container", Group: "Guest Snapshots"},

	// ── Storage ───────────────────────────────────────────────────────
	"GET /api/v1/clusters/:cluster_id/storage":                     {Description: "List storage pools", Permission: "view:storage", Group: "Storage"},
	"POST /api/v1/clusters/:cluster_id/storage":                    {Description: "Create a storage pool", Permission: "manage:storage", Group: "Storage"},
	"GET /api/v1/clusters/:cluster_id/storage/:storage_id/config":  {Description: "Get storage pool configuration", Permission: "view:storage", Group: "Storage"},
	"PUT /api/v1/clusters/:cluster_id/storage/:storage_id":         {Description: "Update storage config", Permission: "manage:storage", Group: "Storage"},
	"DELETE /api/v1/clusters/:cluster_id/storage/:storage_id":      {Description: "Delete a storage pool", Permission: "delete:storage", Group: "Storage"},
	"GET /api/v1/clusters/:cluster_id/storage/:storage_id/content": {Description: "List storage content", Permission: "view:storage", Group: "Storage"},
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/upload": {Description: "Upload an ISO or template", Permission: "manage:storage", Group: "Storage"},
	// manage, not view: discovery makes a node dial a caller-supplied address.
	"GET /api/v1/clusters/:cluster_id/scan/iscsi": {Description: "Discover iSCSI targets on a portal", Permission: "manage:storage", Group: "Storage"},

	// ── Guest Tools ───────────────────────────────────────────────────
	// A dedicated resource rather than execute:vm: running an installer inside
	// a guest OS is worth granting, auditing and withholding on its own.
	"GET /api/v1/clusters/:cluster_id/guest-tools/config":                 {Description: "Get the cluster's Windows guest tools update policy", Permission: "view:guest_tools", Group: "Guest Tools"},
	"PUT /api/v1/clusters/:cluster_id/guest-tools/config":                 {Description: "Update the cluster's Windows guest tools update policy", Permission: "manage:guest_tools", Group: "Guest Tools"},
	"GET /api/v1/clusters/:cluster_id/guest-tools/guests":                 {Description: "List Windows guests with installed guest tools versions and update state", Permission: "view:guest_tools", Group: "Guest Tools"},
	"PUT /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/policy":    {Description: "Pin a version or exclude a guest from guest tools updates", Permission: "manage:guest_tools", Group: "Guest Tools"},
	"POST /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/detect":   {Description: "Probe a guest for its installed guest tools version", Permission: "view:guest_tools", Group: "Guest Tools"},
	"POST /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/update":   {Description: "Stage a guest tools update for next boot, or run it now", Permission: "execute:guest_tools", Group: "Guest Tools"},
	"DELETE /api/v1/clusters/:cluster_id/guest-tools/guests/:vmid/update": {Description: "Cancel a staged guest tools update", Permission: "execute:guest_tools", Group: "Guest Tools"},

	// ── virtio-win ────────────────────────────────────────────────────
	// Gated on storage permissions rather than a resource of their own: the
	// effect is a node fetching a URL into a storage, which is what
	// manage:storage already authorises on POST .../storage/:id/download-url.
	// The source override is the exception: one write repoints every cluster's
	// downloads, so it wants manage:settings, not a per-cluster permission.
	"GET /api/v1/virtio-win/releases":                       {Description: "List known upstream virtio-win releases", Permission: "view:storage", Group: "virtio-win"},
	"GET /api/v1/virtio-win/mirror":                         {Description: "Get the instance-wide virtio-win download source", Permission: "view:storage", Group: "virtio-win"},
	"PUT /api/v1/virtio-win/mirror":                         {Description: "Point virtio-win downloads at a mirror, for air-gapped installs", Permission: "manage:settings", Group: "virtio-win"},
	"GET /api/v1/clusters/:cluster_id/virtio-win/config":    {Description: "Get the cluster's virtio-win auto-download policy", Permission: "view:storage", Group: "virtio-win"},
	"PUT /api/v1/clusters/:cluster_id/virtio-win/config":    {Description: "Update the cluster's virtio-win auto-download policy", Permission: "manage:storage", Group: "virtio-win"},
	"POST /api/v1/clusters/:cluster_id/virtio-win/check":    {Description: "Run the cluster's virtio-win check now, off-schedule", Permission: "manage:storage", Group: "virtio-win"},
	"POST /api/v1/clusters/:cluster_id/virtio-win/download": {Description: "Download a virtio-win ISO to the configured storage now", Permission: "manage:storage", Group: "virtio-win"},
	"GET /api/v1/clusters/:cluster_id/virtio-win/downloads": {Description: "List virtio-win download history for the cluster", Permission: "view:storage", Group: "virtio-win"},

	// ── Backup (PBS) ──────────────────────────────────────────────────
	"GET /api/v1/pbs-servers":                    {Description: "List PBS servers", Permission: "view:pbs", Group: "Backup"},
	"POST /api/v1/pbs-servers":                   {Description: "Add a PBS server", Permission: "manage:pbs", Group: "Backup"},
	"GET /api/v1/pbs-servers/:id":                {Description: "Get PBS server details", Permission: "view:pbs", Group: "Backup"},
	"PUT /api/v1/pbs-servers/:id":                {Description: "Update PBS server", Permission: "manage:pbs", Group: "Backup"},
	"DELETE /api/v1/pbs-servers/:id":             {Description: "Remove a PBS server", Permission: "delete:pbs", Group: "Backup"},
	"GET /api/v1/pbs-servers/:pbs_id/prune-jobs": {Description: "List prune jobs, optionally narrowed to one datastore with ?store=", Permission: "view:backup", Group: "Backup"},

	// ── Veeam Backup & Replication ────────────────────────────────────
	// Global scope, not per-cluster: one Veeam server can protect several
	// Proxmox clusters, so its existence spans them.
	"GET /api/v1/veeam-servers":                                              {Description: "List registered Veeam Backup & Replication servers", Permission: "view:veeam", Group: "Backup"},
	"POST /api/v1/veeam-servers":                                             {Description: "Register a Veeam Backup & Replication server (VBR 13.1+). Connects and records the negotiated API revision, product version and licence edition; a failed connection is a failed create", Permission: "manage:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id":                                          {Description: "Get Veeam server details", Permission: "view:veeam", Group: "Backup"},
	"PUT /api/v1/veeam-servers/:id":                                          {Description: "Update a Veeam server. Changing the URL, credentials or TLS handling re-tests the connection; renaming or disabling does not", Permission: "manage:veeam", Group: "Backup"},
	"DELETE /api/v1/veeam-servers/:id":                                       {Description: "Remove a Veeam server and its stored credential", Permission: "delete:veeam", Group: "Backup"},
	"POST /api/v1/veeam-servers/:id/test":                                    {Description: "Test the stored connection and report version, licence edition and covered Proxmox clusters. Persists nothing", Permission: "manage:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/repositories":                             {Description: "List Veeam backup repositories with capacity. Global scope — one repository holds every cluster's backups", Permission: "view:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/repositories/:repository_id/metrics":      {Description: "Repository capacity over time. ?range=24h|7d|30d|90d, default 7d", Permission: "view:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/jobs":                                     {Description: "List Veeam Proxmox backup jobs with their last result and progress", Permission: "view:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/sessions":                                 {Description: "List recent Veeam Proxmox backup runs", Permission: "view:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/backup-objects":                           {Description: "List backed-up guests. One row per (guest x backup), so a guest covered by several jobs appears more than once", Permission: "view:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/backup-objects/:object_id/restore-points": {Description: "List a backup object's restore points, with malware status and file-level-restore availability", Permission: "view:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/platforms":                                {Description: "List the Veeam platforms (Proxmox connections) this server protects, and the Nexara cluster each is mapped to", Permission: "view:veeam", Group: "Backup"},
	"PUT /api/v1/veeam-servers/:id/platforms/:platform_id":                   {Description: "Map a Veeam platform to a Nexara cluster. Every cluster-scoped Veeam permission resolves through this mapping, so the write needs global manage:veeam", Permission: "manage:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/infrastructure":                           {Description: "List Veeam's own guests on the cluster — worker appliances and the VBR server. Coverage excludes these", Permission: "view:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/orphaned-objects":                         {Description: "List backup objects whose platform is mapped but which match no guest on it — restore points held for machines that no longer exist in the form that was backed up", Permission: "view:veeam", Group: "Backup"},
	"PUT /api/v1/veeam-servers/:id/backup-objects/:object_id/guest":          {Description: "Pin a backup object to a guest by VMID, overriding automatic correlation", Permission: "manage:veeam", Group: "Backup"},
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/start":                      {Description: "Start a Veeam backup job. Async — the returned session is a starting state, not a result. A job with no objects to process returns started=false and creates no run", Permission: "execute:veeam", Group: "Backup"},
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/stop":                       {Description: "Stop a Veeam job's running session. Recorded as Nexara-initiated so veeam_job_failed does not fire for it — Veeam itself records a cancelled run as \"Failed\"", Permission: "execute:veeam", Group: "Backup"},
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/enable":                     {Description: "Put a Veeam job back on its schedule", Permission: "execute:veeam", Group: "Backup"},
	"POST /api/v1/veeam-servers/:id/jobs/:job_id/disable":                    {Description: "Take a Veeam job off its schedule. Protection stops accruing while existing restore points remain, and Veeam raises no alarm about it", Permission: "execute:veeam", Group: "Backup"},
	"POST /api/v1/veeam-servers/:id/sessions/:session_id/stop":               {Description: "Stop one running Veeam session. Recorded as Nexara-initiated, as for the job stop", Permission: "execute:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/sessions/:session_id/logs":                {Description: "Read a session's log, live from Veeam. Empty is the NORMAL result for a stopped run — Veeam keeps no records for a killed session", Permission: "view:veeam", Group: "Backup"},
	"GET /api/v1/veeam-servers/:id/sessions/:session_id/tasks":               {Description: "Per-guest breakdown of one run: which guests it processed and which failed, linked to their Nexara guest where the name resolves unambiguously. Empty while a run is still in flight — Veeam reports task rows only as tasks finish", Permission: "view:veeam", Group: "Backup"},

	// ── DRS ───────────────────────────────────────────────────────────
	"GET /api/v1/clusters/:cluster_id/drs/config":            {Description: "Get DRS configuration", Permission: "view:drs", Group: "DRS"},
	"PUT /api/v1/clusters/:cluster_id/drs/config":            {Description: "Update DRS configuration", Permission: "manage:drs", Group: "DRS"},
	"GET /api/v1/clusters/:cluster_id/drs/rules":             {Description: "List DRS rules", Permission: "view:drs", Group: "DRS"},
	"POST /api/v1/clusters/:cluster_id/drs/rules":            {Description: "Create a DRS rule", Permission: "manage:drs", Group: "DRS"},
	"DELETE /api/v1/clusters/:cluster_id/drs/rules/:rule_id": {Description: "Delete a DRS rule", Permission: "manage:drs", Group: "DRS"},
	"POST /api/v1/clusters/:cluster_id/drs/evaluate":         {Description: "Trigger DRS evaluation", Permission: "manage:drs", Group: "DRS"},
	"GET /api/v1/clusters/:cluster_id/drs/history":           {Description: "Get DRS history", Permission: "view:drs", Group: "DRS"},
	"GET /api/v1/clusters/:cluster_id/drs/ha-rules":          {Description: "List Proxmox HA rules for DRS", Permission: "view:drs", Group: "DRS"},

	// ── Alerts ────────────────────────────────────────────────────────
	"GET /api/v1/alerts":                  {Description: "List alert history", Permission: "view:alert", Group: "Alerts"},
	"GET /api/v1/alerts/summary":          {Description: "Get alert summary counts", Permission: "view:alert", Group: "Alerts"},
	"GET /api/v1/alerts/:id":              {Description: "Get alert details", Permission: "view:alert", Group: "Alerts"},
	"POST /api/v1/alerts/:id/acknowledge": {Description: "Acknowledge an alert", Permission: "acknowledge:alert", Group: "Alerts"},
	"POST /api/v1/alerts/:id/resolve":     {Description: "Resolve an alert", Permission: "acknowledge:alert", Group: "Alerts"},
	"GET /api/v1/alert-rules":             {Description: "List alert rules", Permission: "view:alert", Group: "Alerts"},
	"POST /api/v1/alert-rules":            {Description: "Create an alert rule", Permission: "manage:alert", Group: "Alerts"},
	"GET /api/v1/alert-rules/:id":         {Description: "Get alert rule details", Permission: "view:alert", Group: "Alerts"},
	"PUT /api/v1/alert-rules/:id":         {Description: "Update an alert rule", Permission: "manage:alert", Group: "Alerts"},
	"DELETE /api/v1/alert-rules/:id":      {Description: "Delete an alert rule", Permission: "manage:alert", Group: "Alerts"},

	// ── Notification Channels ─────────────────────────────────────────
	"GET /api/v1/notification-channels":           {Description: "List notification channels", Permission: "view:notification_channel", Group: "Notification Channels"},
	"POST /api/v1/notification-channels":          {Description: "Create a channel", Permission: "manage:notification_channel", Group: "Notification Channels"},
	"GET /api/v1/notification-channels/:id":       {Description: "Get channel details", Permission: "view:notification_channel", Group: "Notification Channels"},
	"PUT /api/v1/notification-channels/:id":       {Description: "Update a channel", Permission: "manage:notification_channel", Group: "Notification Channels"},
	"DELETE /api/v1/notification-channels/:id":    {Description: "Delete a channel", Permission: "manage:notification_channel", Group: "Notification Channels"},
	"POST /api/v1/notification-channels/:id/test": {Description: "Send a test notification", Permission: "manage:notification_channel", Group: "Notification Channels"},

	// ── CVE Scanning / Security ───────────────────────────────────────
	"GET /api/v1/clusters/:cluster_id/cve-scans":                          {Description: "List CVE scans", Permission: "view:cve_scan", Group: "Security"},
	"POST /api/v1/clusters/:cluster_id/cve-scans":                         {Description: "Trigger a CVE scan", Permission: "manage:cve_scan", Group: "Security"},
	"GET /api/v1/clusters/:cluster_id/cve-scans/:scan_id":                 {Description: "Get scan details", Permission: "view:cve_scan", Group: "Security"},
	"GET /api/v1/clusters/:cluster_id/cve-scans/:scan_id/vulnerabilities": {Description: "List vulnerabilities", Permission: "view:cve_scan", Group: "Security"},
	"DELETE /api/v1/clusters/:cluster_id/cve-scans/:scan_id":              {Description: "Delete a scan", Permission: "manage:cve_scan", Group: "Security"},
	"GET /api/v1/clusters/:cluster_id/security-posture":                   {Description: "Get security posture", Permission: "view:cve_scan", Group: "Security"},

	// ── Rolling Updates ───────────────────────────────────────────────
	"GET /api/v1/clusters/:cluster_id/rolling-updates":             {Description: "List rolling update jobs", Permission: "view:rolling_update", Group: "Rolling Updates"},
	"POST /api/v1/clusters/:cluster_id/rolling-updates":            {Description: "Create a rolling update job", Permission: "manage:rolling_update", Group: "Rolling Updates"},
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/start":  {Description: "Start a job", Permission: "manage:rolling_update", Group: "Rolling Updates"},
	"POST /api/v1/clusters/:cluster_id/rolling-updates/:id/cancel": {Description: "Cancel a job", Permission: "manage:rolling_update", Group: "Rolling Updates"},

	// ── Reports ───────────────────────────────────────────────────────
	"GET /api/v1/reports/schedules":  {Description: "List report schedules", Permission: "view:report", Group: "Reports"},
	"POST /api/v1/reports/schedules": {Description: "Create a schedule", Permission: "manage:report", Group: "Reports"},
	"POST /api/v1/reports/generate":  {Description: "Generate an on-demand report", Permission: "generate:report", Group: "Reports"},

	// ── Tasks ─────────────────────────────────────────────────────────
	"GET /api/v1/clusters/:cluster_id/tasks/:upid":     {Description: "Get Proxmox task status", Permission: "view:task", Group: "Tasks"},
	"GET /api/v1/clusters/:cluster_id/tasks/:upid/log": {Description: "Get task log output", Permission: "view:task", Group: "Tasks"},

	// ── Audit Log ─────────────────────────────────────────────────────
	"GET /api/v1/audit-log":        {Description: "List audit log entries", Permission: "view:audit", Group: "Audit Log"},
	"GET /api/v1/audit-log/recent": {Description: "Get recent activity", Permission: "view:audit", Group: "Audit Log"},
	"GET /api/v1/audit-log/export": {Description: "Export audit log as CSV", Permission: "view:audit", Group: "Audit Log"},

	// ── User Management ───────────────────────────────────────────────
	"GET /api/v1/users":                 {Description: "List all users", Permission: "view:user", Group: "User Management"},
	"GET /api/v1/users/:id":             {Description: "Get user details", Permission: "view:user", Group: "User Management"},
	"PUT /api/v1/users/:id":             {Description: "Update a user", Permission: "manage:user", Group: "User Management"},
	"DELETE /api/v1/users/:id":          {Description: "Delete a user", Permission: "manage:user", Group: "User Management"},
	"GET /api/v1/admin/api-keys":        {Description: "List all API keys (admin)", Permission: "manage:user", Group: "User Management"},
	"DELETE /api/v1/admin/api-keys/:id": {Description: "Revoke any user's API key", Permission: "manage:user", Group: "User Management"},

	// ── Proxmox Access Control ────────────────────────────────────────
	// Manages a CLUSTER's own PVE users/tokens/groups/roles/ACLs, which is
	// distinct from Nexara's local users and roles above.
	"GET /api/v1/clusters/:cluster_id/access/users":                            {Description: "List the cluster's Proxmox users", Permission: "view:access", Group: "Proxmox Access Control"},
	"POST /api/v1/clusters/:cluster_id/access/users":                           {Description: "Create a Proxmox user", Permission: "manage:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/users/:userid":                    {Description: "Get a Proxmox user", Permission: "view:access", Group: "Proxmox Access Control"},
	"PUT /api/v1/clusters/:cluster_id/access/users/:userid":                    {Description: "Update a Proxmox user", Permission: "manage:access", Group: "Proxmox Access Control"},
	"DELETE /api/v1/clusters/:cluster_id/access/users/:userid":                 {Description: "Delete a Proxmox user and every token it owns", Permission: "manage:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/users/:userid/tokens":             {Description: "List a Proxmox user's API tokens", Permission: "view:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":    {Description: "Get one API token's metadata (never its secret)", Permission: "view:access", Group: "Proxmox Access Control"},
	"POST /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":   {Description: "Mint a Proxmox API token — the secret is returned once and cannot be retrieved again", Permission: "manage:access", Group: "Proxmox Access Control"},
	"PUT /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid":    {Description: "Update an API token, or regenerate its secret", Permission: "manage:access", Group: "Proxmox Access Control"},
	"DELETE /api/v1/clusters/:cluster_id/access/users/:userid/tokens/:tokenid": {Description: "Revoke a Proxmox API token", Permission: "manage:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/groups":                           {Description: "List Proxmox groups", Permission: "view:access", Group: "Proxmox Access Control"},
	"POST /api/v1/clusters/:cluster_id/access/groups":                          {Description: "Create a Proxmox group", Permission: "manage:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/groups/:groupid":                  {Description: "Get a Proxmox group and its members", Permission: "view:access", Group: "Proxmox Access Control"},
	"PUT /api/v1/clusters/:cluster_id/access/groups/:groupid":                  {Description: "Update a Proxmox group", Permission: "manage:access", Group: "Proxmox Access Control"},
	"DELETE /api/v1/clusters/:cluster_id/access/groups/:groupid":               {Description: "Delete a Proxmox group", Permission: "manage:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/roles":                            {Description: "List Proxmox roles and their privileges", Permission: "view:access", Group: "Proxmox Access Control"},
	"POST /api/v1/clusters/:cluster_id/access/roles":                           {Description: "Create a custom Proxmox role", Permission: "manage:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/roles/:roleid":                    {Description: "Get one Proxmox role's privilege map", Permission: "view:access", Group: "Proxmox Access Control"},
	"PUT /api/v1/clusters/:cluster_id/access/roles/:roleid":                    {Description: "Replace or extend a Proxmox role's privileges", Permission: "manage:access", Group: "Proxmox Access Control"},
	"DELETE /api/v1/clusters/:cluster_id/access/roles/:roleid":                 {Description: "Delete a custom Proxmox role", Permission: "manage:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/acl":                              {Description: "List the cluster's access control entries", Permission: "view:access", Group: "Proxmox Access Control"},
	"PUT /api/v1/clusters/:cluster_id/access/acl":                              {Description: "Grant or revoke roles on an ACL path", Permission: "manage:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/domains":                          {Description: "List authentication realms (read-only)", Permission: "view:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/domains/:realm":                   {Description: "Get one authentication realm (read-only)", Permission: "view:access", Group: "Proxmox Access Control"},
	"GET /api/v1/clusters/:cluster_id/access/permissions":                      {Description: "Report what Nexara's own cluster credential is permitted to do", Permission: "view:access", Group: "Proxmox Access Control"},

	// ── Roles & Permissions ───────────────────────────────────────────
	"GET /api/v1/rbac/roles":        {Description: "List roles", Permission: "view:role", Group: "Roles & Permissions"},
	"POST /api/v1/rbac/roles":       {Description: "Create a custom role", Permission: "manage:role", Group: "Roles & Permissions"},
	"GET /api/v1/rbac/roles/:id":    {Description: "Get role details", Permission: "view:role", Group: "Roles & Permissions"},
	"PUT /api/v1/rbac/roles/:id":    {Description: "Update a role", Permission: "manage:role", Group: "Roles & Permissions"},
	"DELETE /api/v1/rbac/roles/:id": {Description: "Delete a custom role", Permission: "manage:role", Group: "Roles & Permissions"},
	"GET /api/v1/rbac/permissions":  {Description: "List all permissions", Permission: "view:role", Group: "Roles & Permissions"},

	// ── Settings ──────────────────────────────────────────────────────
	"GET /api/v1/settings":         {Description: "Get application settings (scope: global or user); global keys owned by a dedicated endpoint are omitted", Group: "Settings"},
	"GET /api/v1/settings/:key":    {Description: "Get a single setting (scope: global or user); global keys owned by a dedicated endpoint are rejected", Group: "Settings"},
	"PUT /api/v1/settings/:key":    {Description: "Create or update a setting; global-scope writes require manage:settings, user-scope writes affect only the caller, global keys owned by a dedicated endpoint are rejected", Permission: "manage:settings", Group: "Settings"},
	"DELETE /api/v1/settings/:key": {Description: "Delete a setting; global-scope deletes require manage:settings, user-scope deletes affect only the caller, global keys owned by a dedicated endpoint are rejected", Permission: "manage:settings", Group: "Settings"},
	"GET /api/v1/version":          {Description: "Get API version", Group: "Settings"},
	"GET /api/v1/changelog":        {Description: "Get release notes parsed from GitHub Releases", Group: "Settings"},
	"GET /api/v1/search":           {Description: "Search across all resources", Group: "Settings"},

	// ── Metrics ───────────────────────────────────────────────────────
	"GET /api/v1/clusters/:cluster_id/metrics":                {Description: "Get cluster metrics", Permission: "view:cluster", Group: "Metrics"},
	"GET /api/v1/clusters/:cluster_id/nodes/:node_id/metrics": {Description: "Get node metrics", Permission: "view:node", Group: "Metrics"},

	// ── API Documentation ─────────────────────────────────────────────
	"GET /api/v1/api-docs": {Description: "Get this API reference", Group: "API Documentation"},
}

// EndpointMetaKeys returns the sorted "METHOD path" keys of the curated
// overlay. It exists for the route-drift guard test in internal/api,
// which asserts every key still matches a registered route.
func EndpointMetaKeys() []string {
	keys := make([]string, 0, len(endpointMeta))
	for k := range endpointMeta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// EndpointMetaPermissions returns the curated "METHOD path" → permission
// mapping. It exists for the RBAC guard test in internal/api, which asserts
// the documented permission matches the action the handler actually enforces —
// operators build roles from this field, so drift here is a security-relevant
// lie rather than a cosmetic one.
func EndpointMetaPermissions() map[string]string {
	out := make(map[string]string, len(endpointMeta))
	for k, v := range endpointMeta {
		out[k] = v.Permission
	}
	return out
}

// NormalizeDocPath is the single spelling of a route's path used for
// every docs lookup: the route table's own path, the curated
// endpointMeta keys, and the registry's declarations.
//
// Fiber's Group(...) + .Post("/") produces a trailing-slash path like
// "/api/v1/api-keys/". Trimming it keeps the rendered docs readable and
// spares curators the convention — but the reason it is a FUNCTION, and
// exported, is that the two sides of a map lookup have to agree. Keying
// declarations one way and looking them up another is a miss that
// renders a blank endpoint and reports nothing, so the guard test in
// internal/api calls this rather than re-deriving the rule.
func NormalizeDocPath(path string) string {
	if len(path) > len("/api/v1/") && strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/")
	}
	return path
}

// groupFromPath derives a Group label from a path when the curated
// `endpointMeta` map has no entry for it. The second segment after
// `/api/v1/` is the natural carve-up (e.g. `/api/v1/ldap/...` →
// "Ldap"). Falls through to "Other" for paths that don't fit.
func groupFromPath(path string) string {
	const prefix = "/api/v1/"
	if !strings.HasPrefix(path, prefix) {
		return "Other"
	}
	rest := strings.TrimPrefix(path, prefix)
	seg, _, _ := strings.Cut(rest, "/")
	if seg == "" {
		return "Other"
	}
	// Title-case dashes / underscores. "alert-rules" -> "Alert Rules".
	parts := strings.FieldsFunc(seg, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// GetDocs returns the auto-generated API endpoint catalog.
//
// The route table is the canonical list — an endpoint nobody serves is
// not documented, and an endpoint nobody documented still appears. What
// each entry SAYS comes from the declaration when the registry owns the
// route, and from endpointMeta otherwise; see the type comment.
//
// The declaration is taken whole rather than field-by-field. Register
// already refuses a declaration with no Description, no Group or no
// Permissions, so there is no blank field for the overlay to fill —
// and a per-field merge would quietly resurrect a stale endpointMeta
// value the moment a declaration legitimately said something shorter.
func (h *APIDocsHandler) GetDocs(c fiber.Ctx) error {
	if h.app == nil {
		// SetApp wasn't called yet — surface the failure rather than
		// silently returning an empty list, which would confuse the
		// frontend.
		return fiber.NewError(fiber.StatusInternalServerError, "API docs handler is not wired to a Fiber app")
	}

	routes := h.app.GetRoutes(true) // filterUseOption=true → skip USE() middleware
	seen := make(map[string]bool, len(routes))
	out := make([]APIEndpoint, 0, len(routes))

	for _, r := range routes {
		if !strings.HasPrefix(r.Path, "/api/v1/") {
			continue
		}
		// Skip Fiber's auto-generated HEAD / OPTIONS / TRACE entries —
		// they show up alongside every GET/POST and would double the
		// list size.
		if r.Method == fiber.MethodHead || r.Method == fiber.MethodOptions || r.Method == fiber.MethodTrace {
			continue
		}
		path := NormalizeDocPath(r.Path)
		key := r.Method + " " + path
		if seen[key] {
			continue
		}
		seen[key] = true

		ep, declared := h.declared[key]
		if !declared {
			ep = endpointMeta[key]
		}
		ep.Method = r.Method
		ep.Path = path
		if ep.Group == "" {
			ep.Group = groupFromPath(path)
		}
		out = append(out, ep)
	}

	// Sort by group, then method (with GET first to match REST conventions),
	// then path. Stable order makes the rendered docs predictable.
	methodRank := map[string]int{
		"GET": 0, "POST": 1, "PUT": 2, "PATCH": 3, "DELETE": 4,
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return methodRank[out[i].Method] < methodRank[out[j].Method]
	})

	return RespondItems(c, out)
}
