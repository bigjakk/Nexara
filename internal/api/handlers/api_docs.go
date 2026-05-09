package handlers

import (
	"sort"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// APIDocsHandler serves the API endpoint catalog. Endpoints are
// auto-discovered from Fiber's registered route table at request time
// so the docs cannot drift away from what the server actually serves.
//
// Permission / description / group are looked up from `endpointMeta`
// (a hand-curated overlay keyed on `METHOD<space>path`); routes that
// have no overlay entry get an auto-derived group and empty
// description, which is the signal that a curator should fill them in.
//
// Phase 5.8: previously a 134-entry hand-typed list that had drifted
// (~50% of registered routes were missing). Auto-generation from the
// route table was chosen over deletion because the per-page UX
// (search, grouping, method colours) is genuinely useful to operators
// scripting against the API.
type APIDocsHandler struct {
	app *fiber.App
}

// NewAPIDocsHandler creates a new API docs handler. The app reference
// is the per-server Fiber instance whose routes the handler enumerates;
// it is wired in router.go::registerRoutes once the v1 group has been
// fully constructed.
func NewAPIDocsHandler() *APIDocsHandler { return &APIDocsHandler{} }

// SetApp attaches the Fiber app whose routes this handler enumerates.
// Called by the server once all routes are registered.
func (h *APIDocsHandler) SetApp(app *fiber.App) { h.app = app }

// APIEndpoint describes a single API endpoint.
type APIEndpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Permission  string `json:"permission"`
	Group       string `json:"group"`
}

// endpointMeta is the curated overlay: rich descriptions + permission +
// group for routes that have them. The handler pulls the canonical
// route list from `app.GetRoutes()` and decorates entries from this
// map. Routes not present here get auto-derived metadata — `Group`
// from the second `/api/v1/...` segment, blank description, blank
// permission. New endpoints inherit those defaults until a curator
// adds an entry here.
//
// Key format: "METHOD path" exactly as the route is registered (e.g.
// "GET /api/v1/clusters/:id"). The path is matched against
// `fiber.Route.Path` which uses ":param" syntax.
var endpointMeta = map[string]APIEndpoint{
	// ── Authentication ────────────────────────────────────────────────
	"POST /api/v1/auth/register":           {Description: "Register a new user account", Group: "Authentication"},
	"POST /api/v1/auth/login":              {Description: "Authenticate with email and password", Group: "Authentication"},
	"POST /api/v1/auth/refresh":            {Description: "Refresh an expired access token", Group: "Authentication"},
	"POST /api/v1/auth/logout":             {Description: "End the current session", Group: "Authentication"},
	"POST /api/v1/auth/logout-all":         {Description: "End all sessions for the current user", Group: "Authentication"},
	"POST /api/v1/auth/console-token":      {Description: "Mint a short-lived scope-locked JWT for a specific console WebSocket (mobile)", Permission: "view:vm|view:container|view:node", Group: "Authentication"},
	"GET /api/v1/auth/me":                  {Description: "Get current user profile", Group: "Authentication"},
	"PUT /api/v1/auth/profile":             {Description: "Update display name", Group: "Authentication"},
	"POST /api/v1/auth/change-password":    {Description: "Change account password", Group: "Authentication"},
	"GET /api/v1/auth/sso-status":          {Description: "Check if SSO is available", Group: "Authentication"},
	"GET /api/v1/auth/setup-status":        {Description: "Check if initial setup is needed", Group: "Authentication"},
	"GET /api/v1/auth/oidc/authorize":      {Description: "Begin OIDC SSO authorization flow (PKCE+state+nonce)", Group: "Authentication"},
	"GET /api/v1/auth/oidc/callback":       {Description: "OIDC callback redirect target", Group: "Authentication"},
	"POST /api/v1/auth/oidc/token-exchange": {Description: "Exchange the OIDC one-time code for an access+refresh token pair", Group: "Authentication"},

	// ── Mobile Devices ────────────────────────────────────────────────
	"POST /api/v1/me/devices":              {Description: "Register a mobile device for push notifications", Group: "Mobile Devices"},
	"GET /api/v1/me/devices":               {Description: "List the current user's registered mobile devices", Group: "Mobile Devices"},
	"DELETE /api/v1/me/devices/:id":        {Description: "Remove one of the current user's registered mobile devices", Group: "Mobile Devices"},
	"GET /api/v1/admin/users/:id/devices":  {Description: "List a user's registered mobile devices", Permission: "manage:user", Group: "Mobile Devices"},
	"DELETE /api/v1/admin/devices/:id":     {Description: "Remove a mobile device (admin)", Permission: "manage:user", Group: "Mobile Devices"},

	// ── Two-Factor Authentication ─────────────────────────────────────
	"POST /api/v1/auth/totp/setup":                       {Description: "Generate TOTP setup QR code", Group: "Two-Factor Authentication"},
	"POST /api/v1/auth/totp/setup/verify":                {Description: "Confirm TOTP enrollment with a code", Group: "Two-Factor Authentication"},
	"DELETE /api/v1/auth/totp":                           {Description: "Disable two-factor authentication", Group: "Two-Factor Authentication"},
	"GET /api/v1/auth/totp/status":                       {Description: "Get 2FA enrollment status", Group: "Two-Factor Authentication"},
	"POST /api/v1/auth/totp/verify-login":                {Description: "Complete login with TOTP code", Group: "Two-Factor Authentication"},
	"POST /api/v1/auth/totp/recovery-codes/regenerate":   {Description: "Generate new recovery codes", Group: "Two-Factor Authentication"},
	"POST /api/v1/admin/users/:id/totp/reset":            {Description: "Admin: clear another user's 2FA enrollment", Permission: "manage:user", Group: "Two-Factor Authentication"},

	// ── API Keys ──────────────────────────────────────────────────────
	"POST /api/v1/api-keys":         {Description: "Create a personal API key", Permission: "manage:api_key", Group: "API Keys"},
	"GET /api/v1/api-keys":          {Description: "List your API keys", Permission: "manage:api_key", Group: "API Keys"},
	"DELETE /api/v1/api-keys/:id":   {Description: "Revoke an API key", Permission: "manage:api_key", Group: "API Keys"},
	"DELETE /api/v1/api-keys":       {Description: "Revoke all your API keys", Permission: "manage:api_key", Group: "API Keys"},

	// ── Clusters ──────────────────────────────────────────────────────
	"GET /api/v1/clusters":          {Description: "List all clusters", Permission: "view:cluster", Group: "Clusters"},
	"POST /api/v1/clusters":         {Description: "Add a new Proxmox cluster", Permission: "manage:cluster", Group: "Clusters"},
	"GET /api/v1/clusters/:id":      {Description: "Get cluster details", Permission: "view:cluster", Group: "Clusters"},
	"PUT /api/v1/clusters/:id":      {Description: "Update cluster settings", Permission: "manage:cluster", Group: "Clusters"},
	"DELETE /api/v1/clusters/:id":   {Description: "Remove a cluster", Permission: "delete:cluster", Group: "Clusters"},
	"POST /api/v1/clusters/fetch-fingerprint":      {Description: "Fetch a remote cluster's TLS fingerprint", Permission: "manage:cluster", Group: "Clusters"},
	"POST /api/v1/clusters/test-connectivity":      {Description: "Probe a cluster's API URL + token without saving", Permission: "manage:cluster", Group: "Clusters"},

	// ── Nodes ─────────────────────────────────────────────────────────
	"GET /api/v1/clusters/:id/nodes":           {Description: "List cluster nodes", Permission: "view:node", Group: "Nodes"},
	"GET /api/v1/clusters/:id/nodes/:node_id":  {Description: "Get node details", Permission: "view:node", Group: "Nodes"},

	// ── Virtual Machines ──────────────────────────────────────────────
	"GET /api/v1/clusters/:id/vms":                                       {Description: "List all VMs in a cluster", Permission: "view:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:id/vms":                                      {Description: "Create a new VM", Permission: "manage:vm", Group: "Virtual Machines"},
	"GET /api/v1/clusters/:id/vms/:vm_id":                                {Description: "Get VM details", Permission: "view:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:id/vms/:vm_id/status":                        {Description: "Change VM power state", Permission: "execute:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:id/vms/:vm_id/clone":                         {Description: "Clone a VM", Permission: "manage:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:id/vms/:vm_id/migrate":                       {Description: "Migrate VM to another node", Permission: "manage:vm", Group: "Virtual Machines"},
	"DELETE /api/v1/clusters/:id/vms/:vm_id":                             {Description: "Destroy a VM", Permission: "delete:vm", Group: "Virtual Machines"},
	"GET /api/v1/clusters/:id/vms/:vm_id/snapshots":                      {Description: "List VM snapshots", Permission: "view:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:id/vms/:vm_id/snapshots":                     {Description: "Create a snapshot", Permission: "manage:vm", Group: "Virtual Machines"},
	"DELETE /api/v1/clusters/:id/vms/:vm_id/snapshots/:snap":             {Description: "Delete a snapshot", Permission: "manage:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:id/vms/:vm_id/snapshots/:snap/rollback":      {Description: "Rollback to snapshot", Permission: "manage:vm", Group: "Virtual Machines"},
	"GET /api/v1/clusters/:id/vms/:vm_id/config":                         {Description: "Get VM configuration", Permission: "view:vm", Group: "Virtual Machines"},
	"PUT /api/v1/clusters/:id/vms/:vm_id/config":                         {Description: "Update VM configuration", Permission: "manage:vm", Group: "Virtual Machines"},
	"PUT /api/v1/clusters/:id/vms/:vm_id/resize":                         {Description: "Resize VM disk", Permission: "manage:vm", Group: "Virtual Machines"},
	"POST /api/v1/clusters/:id/vms/:vm_id/move-disk":                     {Description: "Move VM disk to another storage", Permission: "manage:vm", Group: "Virtual Machines"},

	// ── Containers ────────────────────────────────────────────────────
	"GET /api/v1/clusters/:id/containers":                          {Description: "List all containers", Permission: "view:container", Group: "Containers"},
	"POST /api/v1/clusters/:id/containers":                         {Description: "Create a container", Permission: "manage:container", Group: "Containers"},
	"GET /api/v1/clusters/:id/containers/:ct_id":                   {Description: "Get container details", Permission: "view:container", Group: "Containers"},
	"POST /api/v1/clusters/:id/containers/:ct_id/status":           {Description: "Change container power state", Permission: "execute:container", Group: "Containers"},
	"POST /api/v1/clusters/:id/containers/:ct_id/clone":            {Description: "Clone a container", Permission: "manage:container", Group: "Containers"},
	"POST /api/v1/clusters/:id/containers/:ct_id/migrate":          {Description: "Migrate container", Permission: "manage:container", Group: "Containers"},
	"DELETE /api/v1/clusters/:id/containers/:ct_id":                {Description: "Destroy a container", Permission: "delete:container", Group: "Containers"},
	"GET /api/v1/clusters/:id/containers/:ct_id/snapshots":         {Description: "List container snapshots", Permission: "view:container", Group: "Containers"},
	"POST /api/v1/clusters/:id/containers/:ct_id/snapshots":        {Description: "Create a snapshot", Permission: "manage:container", Group: "Containers"},
	"GET /api/v1/clusters/:id/containers/:ct_id/config":            {Description: "Get container config", Permission: "view:container", Group: "Containers"},
	"PUT /api/v1/clusters/:id/containers/:ct_id/config":            {Description: "Update container config", Permission: "manage:container", Group: "Containers"},
	"PUT /api/v1/clusters/:id/containers/:ct_id/resize":            {Description: "Resize container", Permission: "manage:container", Group: "Containers"},

	// ── Storage ───────────────────────────────────────────────────────
	"GET /api/v1/clusters/:id/storage":                                  {Description: "List storage pools", Permission: "view:storage", Group: "Storage"},
	"POST /api/v1/clusters/:id/storage":                                 {Description: "Create a storage pool", Permission: "manage:storage", Group: "Storage"},
	"GET /api/v1/clusters/:id/storage/:storage_id":                      {Description: "Get storage details", Permission: "view:storage", Group: "Storage"},
	"PUT /api/v1/clusters/:id/storage/:storage_id":                      {Description: "Update storage config", Permission: "manage:storage", Group: "Storage"},
	"DELETE /api/v1/clusters/:id/storage/:storage_id":                   {Description: "Delete a storage pool", Permission: "delete:storage", Group: "Storage"},
	"GET /api/v1/clusters/:id/storage/:storage_id/content":              {Description: "List storage content", Permission: "view:storage", Group: "Storage"},
	"POST /api/v1/clusters/:id/storage/:storage_id/upload":              {Description: "Upload an ISO or template", Permission: "manage:storage", Group: "Storage"},

	// ── Backup (PBS) ──────────────────────────────────────────────────
	"GET /api/v1/pbs-servers":          {Description: "List PBS servers", Permission: "view:pbs", Group: "Backup"},
	"POST /api/v1/pbs-servers":         {Description: "Add a PBS server", Permission: "manage:pbs", Group: "Backup"},
	"GET /api/v1/pbs-servers/:id":      {Description: "Get PBS server details", Permission: "view:pbs", Group: "Backup"},
	"PUT /api/v1/pbs-servers/:id":      {Description: "Update PBS server", Permission: "manage:pbs", Group: "Backup"},
	"DELETE /api/v1/pbs-servers/:id":   {Description: "Remove a PBS server", Permission: "delete:pbs", Group: "Backup"},

	// ── DRS ───────────────────────────────────────────────────────────
	"GET /api/v1/clusters/:id/drs/config":             {Description: "Get DRS configuration", Permission: "view:drs", Group: "DRS"},
	"PUT /api/v1/clusters/:id/drs/config":             {Description: "Update DRS configuration", Permission: "manage:drs", Group: "DRS"},
	"GET /api/v1/clusters/:id/drs/rules":              {Description: "List DRS rules", Permission: "view:drs", Group: "DRS"},
	"POST /api/v1/clusters/:id/drs/rules":             {Description: "Create a DRS rule", Permission: "manage:drs", Group: "DRS"},
	"DELETE /api/v1/clusters/:id/drs/rules/:rule_id":  {Description: "Delete a DRS rule", Permission: "manage:drs", Group: "DRS"},
	"POST /api/v1/clusters/:id/drs/evaluate":          {Description: "Trigger DRS evaluation", Permission: "manage:drs", Group: "DRS"},
	"GET /api/v1/clusters/:id/drs/history":            {Description: "Get DRS history", Permission: "view:drs", Group: "DRS"},
	"GET /api/v1/clusters/:id/drs/ha-rules":           {Description: "List Proxmox HA rules for DRS", Permission: "view:drs", Group: "DRS"},

	// ── Alerts ────────────────────────────────────────────────────────
	"GET /api/v1/alerts":                       {Description: "List alert history", Permission: "view:alert", Group: "Alerts"},
	"GET /api/v1/alerts/summary":               {Description: "Get alert summary counts", Permission: "view:alert", Group: "Alerts"},
	"GET /api/v1/alerts/:id":                   {Description: "Get alert details", Permission: "view:alert", Group: "Alerts"},
	"POST /api/v1/alerts/:id/acknowledge":      {Description: "Acknowledge an alert", Permission: "acknowledge:alert", Group: "Alerts"},
	"POST /api/v1/alerts/:id/resolve":          {Description: "Resolve an alert", Permission: "manage:alert", Group: "Alerts"},
	"GET /api/v1/alert-rules":                  {Description: "List alert rules", Permission: "view:alert", Group: "Alerts"},
	"POST /api/v1/alert-rules":                 {Description: "Create an alert rule", Permission: "manage:alert", Group: "Alerts"},
	"GET /api/v1/alert-rules/:id":              {Description: "Get alert rule details", Permission: "view:alert", Group: "Alerts"},
	"PUT /api/v1/alert-rules/:id":              {Description: "Update an alert rule", Permission: "manage:alert", Group: "Alerts"},
	"DELETE /api/v1/alert-rules/:id":           {Description: "Delete an alert rule", Permission: "manage:alert", Group: "Alerts"},

	// ── Notification Channels ─────────────────────────────────────────
	"GET /api/v1/notification-channels":              {Description: "List notification channels", Permission: "view:notification_channel", Group: "Notification Channels"},
	"POST /api/v1/notification-channels":             {Description: "Create a channel", Permission: "manage:notification_channel", Group: "Notification Channels"},
	"GET /api/v1/notification-channels/:id":          {Description: "Get channel details", Permission: "view:notification_channel", Group: "Notification Channels"},
	"PUT /api/v1/notification-channels/:id":          {Description: "Update a channel", Permission: "manage:notification_channel", Group: "Notification Channels"},
	"DELETE /api/v1/notification-channels/:id":       {Description: "Delete a channel", Permission: "manage:notification_channel", Group: "Notification Channels"},
	"POST /api/v1/notification-channels/:id/test":    {Description: "Send a test notification", Permission: "manage:notification_channel", Group: "Notification Channels"},

	// ── CVE Scanning / Security ───────────────────────────────────────
	"GET /api/v1/clusters/:id/cve-scans":                                {Description: "List CVE scans", Permission: "view:cve_scan", Group: "Security"},
	"POST /api/v1/clusters/:id/cve-scans":                               {Description: "Trigger a CVE scan", Permission: "manage:cve_scan", Group: "Security"},
	"GET /api/v1/clusters/:id/cve-scans/:scan_id":                       {Description: "Get scan details", Permission: "view:cve_scan", Group: "Security"},
	"GET /api/v1/clusters/:id/cve-scans/:scan_id/vulnerabilities":       {Description: "List vulnerabilities", Permission: "view:cve_scan", Group: "Security"},
	"DELETE /api/v1/clusters/:id/cve-scans/:scan_id":                    {Description: "Delete a scan", Permission: "manage:cve_scan", Group: "Security"},
	"GET /api/v1/clusters/:id/security-posture":                         {Description: "Get security posture", Permission: "view:cve_scan", Group: "Security"},

	// ── Rolling Updates ───────────────────────────────────────────────
	"GET /api/v1/clusters/:id/rolling-updates":                              {Description: "List rolling update jobs", Permission: "view:rolling_update", Group: "Rolling Updates"},
	"POST /api/v1/clusters/:id/rolling-updates":                             {Description: "Create a rolling update job", Permission: "manage:rolling_update", Group: "Rolling Updates"},
	"POST /api/v1/clusters/:id/rolling-updates/:job_id/start":               {Description: "Start a job", Permission: "manage:rolling_update", Group: "Rolling Updates"},
	"POST /api/v1/clusters/:id/rolling-updates/:job_id/cancel":              {Description: "Cancel a job", Permission: "manage:rolling_update", Group: "Rolling Updates"},

	// ── Reports ───────────────────────────────────────────────────────
	"GET /api/v1/reports/schedules":          {Description: "List report schedules", Permission: "view:report", Group: "Reports"},
	"POST /api/v1/reports/schedules":         {Description: "Create a schedule", Permission: "manage:report", Group: "Reports"},
	"POST /api/v1/reports/generate":          {Description: "Generate an on-demand report", Permission: "generate:report", Group: "Reports"},

	// ── Tasks ─────────────────────────────────────────────────────────
	"GET /api/v1/clusters/:id/tasks/:upid":     {Description: "Get Proxmox task status", Permission: "view:task", Group: "Tasks"},
	"GET /api/v1/clusters/:id/tasks/:upid/log": {Description: "Get task log output", Permission: "view:task", Group: "Tasks"},

	// ── Audit Log ─────────────────────────────────────────────────────
	"GET /api/v1/audit-log":         {Description: "List audit log entries", Permission: "view:audit", Group: "Audit Log"},
	"GET /api/v1/audit-log/recent":  {Description: "Get recent activity", Permission: "view:audit", Group: "Audit Log"},
	"GET /api/v1/audit-log/export":  {Description: "Export audit log as CSV", Permission: "view:audit", Group: "Audit Log"},

	// ── User Management ───────────────────────────────────────────────
	"GET /api/v1/users":                      {Description: "List all users", Permission: "view:user", Group: "User Management"},
	"GET /api/v1/users/:id":                  {Description: "Get user details", Permission: "view:user", Group: "User Management"},
	"PUT /api/v1/users/:id":                  {Description: "Update a user", Permission: "manage:user", Group: "User Management"},
	"DELETE /api/v1/users/:id":               {Description: "Delete a user", Permission: "manage:user", Group: "User Management"},
	"GET /api/v1/admin/api-keys":             {Description: "List all API keys (admin)", Permission: "manage:user", Group: "User Management"},
	"DELETE /api/v1/admin/api-keys/:id":      {Description: "Revoke any user's API key", Permission: "manage:user", Group: "User Management"},

	// ── Roles & Permissions ───────────────────────────────────────────
	"GET /api/v1/rbac/roles":          {Description: "List roles", Permission: "view:role", Group: "Roles & Permissions"},
	"POST /api/v1/rbac/roles":         {Description: "Create a custom role", Permission: "manage:role", Group: "Roles & Permissions"},
	"GET /api/v1/rbac/roles/:id":      {Description: "Get role details", Permission: "view:role", Group: "Roles & Permissions"},
	"PUT /api/v1/rbac/roles/:id":      {Description: "Update a role", Permission: "manage:role", Group: "Roles & Permissions"},
	"DELETE /api/v1/rbac/roles/:id":   {Description: "Delete a custom role", Permission: "manage:role", Group: "Roles & Permissions"},
	"GET /api/v1/rbac/permissions":    {Description: "List all permissions", Permission: "view:role", Group: "Roles & Permissions"},

	// ── Settings ──────────────────────────────────────────────────────
	"GET /api/v1/settings":         {Description: "Get application settings", Group: "Settings"},
	"PUT /api/v1/settings":         {Description: "Update settings", Permission: "manage:user", Group: "Settings"},
	"GET /api/v1/version":          {Description: "Get API version", Group: "Settings"},
	"GET /api/v1/changelog":        {Description: "Get release notes parsed from GitHub Releases", Group: "Settings"},
	"GET /api/v1/search":           {Description: "Search across all resources", Group: "Settings"},

	// ── Metrics ───────────────────────────────────────────────────────
	"GET /api/v1/clusters/:id/metrics":                       {Description: "Get cluster metrics", Permission: "view:cluster", Group: "Metrics"},
	"GET /api/v1/clusters/:id/nodes/:node_id/metrics":        {Description: "Get node metrics", Permission: "view:node", Group: "Metrics"},

	// ── API Documentation ─────────────────────────────────────────────
	"GET /api/v1/api-docs":         {Description: "Get this API reference", Group: "API Documentation"},
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
func (h *APIDocsHandler) GetDocs(c *fiber.Ctx) error {
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
		// Fiber's Group(...) + .Post("/") produces a trailing-slash
		// path like "/api/v1/api-keys/". Normalise to the no-trailing
		// form so curators don't have to memorise the convention and
		// the rendered docs read cleanly.
		path := r.Path
		if len(path) > len("/api/v1/") && strings.HasSuffix(path, "/") {
			path = strings.TrimSuffix(path, "/")
		}
		key := r.Method + " " + path
		if seen[key] {
			continue
		}
		seen[key] = true

		ep := endpointMeta[key]
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

	return c.JSON(out)
}
