package handlers

import "github.com/gofiber/fiber/v2"

// APIDocsHandler serves the API endpoint catalog.
type APIDocsHandler struct{}

// NewAPIDocsHandler creates a new API docs handler.
func NewAPIDocsHandler() *APIDocsHandler { return &APIDocsHandler{} }

// APIEndpoint describes a single API endpoint.
type APIEndpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Permission  string `json:"permission"`
	Group       string `json:"group"`
}

// apiEndpoints is the complete catalog of Nexara API endpoints.
var apiEndpoints = []APIEndpoint{
	// ── Authentication ──────────────────────────────────────────────────
	{Method: "POST", Path: "/api/v1/auth/register", Description: "Register a new user account", Permission: "", Group: "Authentication"},
	{Method: "POST", Path: "/api/v1/auth/login", Description: "Authenticate with email and password", Permission: "", Group: "Authentication"},
	{Method: "POST", Path: "/api/v1/auth/refresh", Description: "Refresh an expired access token", Permission: "", Group: "Authentication"},
	{Method: "POST", Path: "/api/v1/auth/logout", Description: "End the current session", Permission: "", Group: "Authentication"},
	{Method: "POST", Path: "/api/v1/auth/logout-all", Description: "End all sessions for the current user", Permission: "", Group: "Authentication"},
	{Method: "GET", Path: "/api/v1/auth/me", Description: "Get current user profile", Permission: "", Group: "Authentication"},
	{Method: "PUT", Path: "/api/v1/auth/profile", Description: "Update display name", Permission: "", Group: "Authentication"},
	{Method: "POST", Path: "/api/v1/auth/change-password", Description: "Change account password", Permission: "", Group: "Authentication"},
	{Method: "GET", Path: "/api/v1/auth/sso-status", Description: "Check if SSO is available", Permission: "", Group: "Authentication"},
	{Method: "GET", Path: "/api/v1/auth/setup-status", Description: "Check if initial setup is needed", Permission: "", Group: "Authentication"},

	// ── Two-Factor Authentication ───────────────────────────────────────
	{Method: "POST", Path: "/api/v1/auth/totp/setup", Description: "Generate TOTP setup QR code", Permission: "", Group: "Two-Factor Authentication"},
	{Method: "POST", Path: "/api/v1/auth/totp/setup/verify", Description: "Confirm TOTP enrollment with a code", Permission: "", Group: "Two-Factor Authentication"},
	{Method: "DELETE", Path: "/api/v1/auth/totp", Description: "Disable two-factor authentication", Permission: "", Group: "Two-Factor Authentication"},
	{Method: "GET", Path: "/api/v1/auth/totp/status", Description: "Get 2FA enrollment status", Permission: "", Group: "Two-Factor Authentication"},
	{Method: "POST", Path: "/api/v1/auth/totp/verify-login", Description: "Complete login with TOTP code", Permission: "", Group: "Two-Factor Authentication"},
	{Method: "POST", Path: "/api/v1/auth/totp/recovery-codes/regenerate", Description: "Generate new recovery codes", Permission: "", Group: "Two-Factor Authentication"},

	// ── API Keys ────────────────────────────────────────────────────────
	{Method: "POST", Path: "/api/v1/api-keys", Description: "Create a personal API key", Permission: "manage:api_key", Group: "API Keys"},
	{Method: "GET", Path: "/api/v1/api-keys", Description: "List your API keys", Permission: "manage:api_key", Group: "API Keys"},
	{Method: "DELETE", Path: "/api/v1/api-keys/:id", Description: "Revoke an API key", Permission: "manage:api_key", Group: "API Keys"},
	{Method: "DELETE", Path: "/api/v1/api-keys", Description: "Revoke all your API keys", Permission: "manage:api_key", Group: "API Keys"},

	// ── Clusters ────────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters", Description: "List all clusters", Permission: "view:cluster", Group: "Clusters"},
	{Method: "POST", Path: "/api/v1/clusters", Description: "Add a new Proxmox cluster", Permission: "manage:cluster", Group: "Clusters"},
	{Method: "GET", Path: "/api/v1/clusters/:id", Description: "Get cluster details", Permission: "view:cluster", Group: "Clusters"},
	{Method: "PUT", Path: "/api/v1/clusters/:id", Description: "Update cluster settings", Permission: "manage:cluster", Group: "Clusters"},
	{Method: "DELETE", Path: "/api/v1/clusters/:id", Description: "Remove a cluster", Permission: "delete:cluster", Group: "Clusters"},

	// ── Nodes ───────────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters/:id/nodes", Description: "List cluster nodes", Permission: "view:node", Group: "Nodes"},
	{Method: "GET", Path: "/api/v1/clusters/:id/nodes/:node_id", Description: "Get node details", Permission: "view:node", Group: "Nodes"},

	// ── Virtual Machines ────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters/:id/vms", Description: "List all VMs in a cluster", Permission: "view:vm", Group: "Virtual Machines"},
	{Method: "POST", Path: "/api/v1/clusters/:id/vms", Description: "Create a new VM", Permission: "manage:vm", Group: "Virtual Machines"},
	{Method: "GET", Path: "/api/v1/clusters/:id/vms/:vm_id", Description: "Get VM details", Permission: "view:vm", Group: "Virtual Machines"},
	{Method: "POST", Path: "/api/v1/clusters/:id/vms/:vm_id/status", Description: "Change VM power state", Permission: "execute:vm", Group: "Virtual Machines"},
	{Method: "POST", Path: "/api/v1/clusters/:id/vms/:vm_id/clone", Description: "Clone a VM", Permission: "manage:vm", Group: "Virtual Machines"},
	{Method: "POST", Path: "/api/v1/clusters/:id/vms/:vm_id/migrate", Description: "Migrate VM to another node", Permission: "manage:vm", Group: "Virtual Machines"},
	{Method: "DELETE", Path: "/api/v1/clusters/:id/vms/:vm_id", Description: "Destroy a VM", Permission: "delete:vm", Group: "Virtual Machines"},
	{Method: "GET", Path: "/api/v1/clusters/:id/vms/:vm_id/snapshots", Description: "List VM snapshots", Permission: "view:vm", Group: "Virtual Machines"},
	{Method: "POST", Path: "/api/v1/clusters/:id/vms/:vm_id/snapshots", Description: "Create a snapshot", Permission: "manage:vm", Group: "Virtual Machines"},
	{Method: "DELETE", Path: "/api/v1/clusters/:id/vms/:vm_id/snapshots/:snap", Description: "Delete a snapshot", Permission: "manage:vm", Group: "Virtual Machines"},
	{Method: "POST", Path: "/api/v1/clusters/:id/vms/:vm_id/snapshots/:snap/rollback", Description: "Rollback to snapshot", Permission: "manage:vm", Group: "Virtual Machines"},
	{Method: "GET", Path: "/api/v1/clusters/:id/vms/:vm_id/config", Description: "Get VM configuration", Permission: "view:vm", Group: "Virtual Machines"},
	{Method: "PUT", Path: "/api/v1/clusters/:id/vms/:vm_id/config", Description: "Update VM configuration", Permission: "manage:vm", Group: "Virtual Machines"},
	{Method: "PUT", Path: "/api/v1/clusters/:id/vms/:vm_id/resize", Description: "Resize VM disk", Permission: "manage:vm", Group: "Virtual Machines"},
	{Method: "POST", Path: "/api/v1/clusters/:id/vms/:vm_id/move-disk", Description: "Move VM disk to another storage", Permission: "manage:vm", Group: "Virtual Machines"},

	// ── Containers ──────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters/:id/containers", Description: "List all containers", Permission: "view:container", Group: "Containers"},
	{Method: "POST", Path: "/api/v1/clusters/:id/containers", Description: "Create a container", Permission: "manage:container", Group: "Containers"},
	{Method: "GET", Path: "/api/v1/clusters/:id/containers/:ct_id", Description: "Get container details", Permission: "view:container", Group: "Containers"},
	{Method: "POST", Path: "/api/v1/clusters/:id/containers/:ct_id/status", Description: "Change container power state", Permission: "execute:container", Group: "Containers"},
	{Method: "POST", Path: "/api/v1/clusters/:id/containers/:ct_id/clone", Description: "Clone a container", Permission: "manage:container", Group: "Containers"},
	{Method: "POST", Path: "/api/v1/clusters/:id/containers/:ct_id/migrate", Description: "Migrate container", Permission: "manage:container", Group: "Containers"},
	{Method: "DELETE", Path: "/api/v1/clusters/:id/containers/:ct_id", Description: "Destroy a container", Permission: "delete:container", Group: "Containers"},
	{Method: "GET", Path: "/api/v1/clusters/:id/containers/:ct_id/snapshots", Description: "List container snapshots", Permission: "view:container", Group: "Containers"},
	{Method: "POST", Path: "/api/v1/clusters/:id/containers/:ct_id/snapshots", Description: "Create a snapshot", Permission: "manage:container", Group: "Containers"},
	{Method: "GET", Path: "/api/v1/clusters/:id/containers/:ct_id/config", Description: "Get container config", Permission: "view:container", Group: "Containers"},
	{Method: "PUT", Path: "/api/v1/clusters/:id/containers/:ct_id/config", Description: "Update container config", Permission: "manage:container", Group: "Containers"},
	{Method: "PUT", Path: "/api/v1/clusters/:id/containers/:ct_id/resize", Description: "Resize container", Permission: "manage:container", Group: "Containers"},

	// ── Storage ─────────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters/:id/storage", Description: "List storage pools", Permission: "view:storage", Group: "Storage"},
	{Method: "POST", Path: "/api/v1/clusters/:id/storage", Description: "Create a storage pool", Permission: "manage:storage", Group: "Storage"},
	{Method: "GET", Path: "/api/v1/clusters/:id/storage/:storage_id", Description: "Get storage details", Permission: "view:storage", Group: "Storage"},
	{Method: "PUT", Path: "/api/v1/clusters/:id/storage/:storage_id", Description: "Update storage config", Permission: "manage:storage", Group: "Storage"},
	{Method: "DELETE", Path: "/api/v1/clusters/:id/storage/:storage_id", Description: "Delete a storage pool", Permission: "delete:storage", Group: "Storage"},
	{Method: "GET", Path: "/api/v1/clusters/:id/storage/:storage_id/content", Description: "List storage content", Permission: "view:storage", Group: "Storage"},
	{Method: "POST", Path: "/api/v1/clusters/:id/storage/:storage_id/upload", Description: "Upload an ISO or template", Permission: "manage:storage", Group: "Storage"},

	// ── Backup (PBS) ────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/pbs-servers", Description: "List PBS servers", Permission: "view:pbs", Group: "Backup"},
	{Method: "POST", Path: "/api/v1/pbs-servers", Description: "Add a PBS server", Permission: "manage:pbs", Group: "Backup"},
	{Method: "GET", Path: "/api/v1/pbs-servers/:id", Description: "Get PBS server details", Permission: "view:pbs", Group: "Backup"},
	{Method: "PUT", Path: "/api/v1/pbs-servers/:id", Description: "Update PBS server", Permission: "manage:pbs", Group: "Backup"},
	{Method: "DELETE", Path: "/api/v1/pbs-servers/:id", Description: "Remove a PBS server", Permission: "delete:pbs", Group: "Backup"},

	// ── DRS ─────────────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters/:id/drs/config", Description: "Get DRS configuration", Permission: "view:drs", Group: "DRS"},
	{Method: "PUT", Path: "/api/v1/clusters/:id/drs/config", Description: "Update DRS configuration", Permission: "manage:drs", Group: "DRS"},
	{Method: "GET", Path: "/api/v1/clusters/:id/drs/rules", Description: "List DRS rules", Permission: "view:drs", Group: "DRS"},
	{Method: "POST", Path: "/api/v1/clusters/:id/drs/rules", Description: "Create a DRS rule", Permission: "manage:drs", Group: "DRS"},
	{Method: "DELETE", Path: "/api/v1/clusters/:id/drs/rules/:rule_id", Description: "Delete a DRS rule", Permission: "manage:drs", Group: "DRS"},
	{Method: "POST", Path: "/api/v1/clusters/:id/drs/evaluate", Description: "Trigger DRS evaluation", Permission: "manage:drs", Group: "DRS"},
	{Method: "GET", Path: "/api/v1/clusters/:id/drs/history", Description: "Get DRS history", Permission: "view:drs", Group: "DRS"},
	{Method: "GET", Path: "/api/v1/clusters/:id/drs/ha-rules", Description: "List Proxmox HA rules for DRS", Permission: "view:drs", Group: "DRS"},

	// ── Alerts ──────────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/alerts", Description: "List alert history", Permission: "view:alert", Group: "Alerts"},
	{Method: "GET", Path: "/api/v1/alerts/summary", Description: "Get alert summary counts", Permission: "view:alert", Group: "Alerts"},
	{Method: "GET", Path: "/api/v1/alerts/:id", Description: "Get alert details", Permission: "view:alert", Group: "Alerts"},
	{Method: "POST", Path: "/api/v1/alerts/:id/acknowledge", Description: "Acknowledge an alert", Permission: "acknowledge:alert", Group: "Alerts"},
	{Method: "POST", Path: "/api/v1/alerts/:id/resolve", Description: "Resolve an alert", Permission: "manage:alert", Group: "Alerts"},
	{Method: "GET", Path: "/api/v1/alert-rules", Description: "List alert rules", Permission: "view:alert", Group: "Alerts"},
	{Method: "POST", Path: "/api/v1/alert-rules", Description: "Create an alert rule", Permission: "manage:alert", Group: "Alerts"},
	{Method: "GET", Path: "/api/v1/alert-rules/:id", Description: "Get alert rule details", Permission: "view:alert", Group: "Alerts"},
	{Method: "PUT", Path: "/api/v1/alert-rules/:id", Description: "Update an alert rule", Permission: "manage:alert", Group: "Alerts"},
	{Method: "DELETE", Path: "/api/v1/alert-rules/:id", Description: "Delete an alert rule", Permission: "manage:alert", Group: "Alerts"},

	// ── Notification Channels ───────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/notification-channels", Description: "List notification channels", Permission: "view:notification_channel", Group: "Notification Channels"},
	{Method: "POST", Path: "/api/v1/notification-channels", Description: "Create a channel", Permission: "manage:notification_channel", Group: "Notification Channels"},
	{Method: "GET", Path: "/api/v1/notification-channels/:id", Description: "Get channel details", Permission: "view:notification_channel", Group: "Notification Channels"},
	{Method: "PUT", Path: "/api/v1/notification-channels/:id", Description: "Update a channel", Permission: "manage:notification_channel", Group: "Notification Channels"},
	{Method: "DELETE", Path: "/api/v1/notification-channels/:id", Description: "Delete a channel", Permission: "manage:notification_channel", Group: "Notification Channels"},
	{Method: "POST", Path: "/api/v1/notification-channels/:id/test", Description: "Send a test notification", Permission: "manage:notification_channel", Group: "Notification Channels"},

	// ── CVE Scanning / Security ─────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters/:id/cve-scans", Description: "List CVE scans", Permission: "view:cve_scan", Group: "Security"},
	{Method: "POST", Path: "/api/v1/clusters/:id/cve-scans", Description: "Trigger a CVE scan", Permission: "manage:cve_scan", Group: "Security"},
	{Method: "GET", Path: "/api/v1/clusters/:id/cve-scans/:scan_id", Description: "Get scan details", Permission: "view:cve_scan", Group: "Security"},
	{Method: "GET", Path: "/api/v1/clusters/:id/cve-scans/:scan_id/vulnerabilities", Description: "List vulnerabilities", Permission: "view:cve_scan", Group: "Security"},
	{Method: "DELETE", Path: "/api/v1/clusters/:id/cve-scans/:scan_id", Description: "Delete a scan", Permission: "manage:cve_scan", Group: "Security"},
	{Method: "GET", Path: "/api/v1/clusters/:id/security-posture", Description: "Get security posture", Permission: "view:cve_scan", Group: "Security"},

	// ── Rolling Updates ─────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters/:id/rolling-updates", Description: "List rolling update jobs", Permission: "view:rolling_update", Group: "Rolling Updates"},
	{Method: "POST", Path: "/api/v1/clusters/:id/rolling-updates", Description: "Create a rolling update job", Permission: "manage:rolling_update", Group: "Rolling Updates"},
	{Method: "POST", Path: "/api/v1/clusters/:id/rolling-updates/:job_id/start", Description: "Start a job", Permission: "manage:rolling_update", Group: "Rolling Updates"},
	{Method: "POST", Path: "/api/v1/clusters/:id/rolling-updates/:job_id/cancel", Description: "Cancel a job", Permission: "manage:rolling_update", Group: "Rolling Updates"},

	// ── Reports ─────────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/reports/schedules", Description: "List report schedules", Permission: "view:report", Group: "Reports"},
	{Method: "POST", Path: "/api/v1/reports/schedules", Description: "Create a schedule", Permission: "manage:report", Group: "Reports"},
	{Method: "POST", Path: "/api/v1/reports/generate", Description: "Generate an on-demand report", Permission: "generate:report", Group: "Reports"},

	// ── Tasks ───────────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters/:id/tasks/:upid", Description: "Get Proxmox task status", Permission: "view:task", Group: "Tasks"},
	{Method: "GET", Path: "/api/v1/clusters/:id/tasks/:upid/log", Description: "Get task log output", Permission: "view:task", Group: "Tasks"},

	// ── Audit Log ───────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/audit-log", Description: "List audit log entries", Permission: "view:audit", Group: "Audit Log"},
	{Method: "GET", Path: "/api/v1/audit-log/recent", Description: "Get recent activity", Permission: "view:audit", Group: "Audit Log"},
	{Method: "GET", Path: "/api/v1/audit-log/export", Description: "Export audit log as CSV", Permission: "view:audit", Group: "Audit Log"},

	// ── User Management ─────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/users", Description: "List all users", Permission: "view:user", Group: "User Management"},
	{Method: "GET", Path: "/api/v1/users/:id", Description: "Get user details", Permission: "view:user", Group: "User Management"},
	{Method: "PUT", Path: "/api/v1/users/:id", Description: "Update a user", Permission: "manage:user", Group: "User Management"},
	{Method: "DELETE", Path: "/api/v1/users/:id", Description: "Delete a user", Permission: "manage:user", Group: "User Management"},
	{Method: "GET", Path: "/api/v1/admin/api-keys", Description: "List all API keys (admin)", Permission: "manage:user", Group: "User Management"},
	{Method: "DELETE", Path: "/api/v1/admin/api-keys/:id", Description: "Revoke any user's API key", Permission: "manage:user", Group: "User Management"},

	// ── Roles & Permissions ─────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/rbac/roles", Description: "List roles", Permission: "view:role", Group: "Roles & Permissions"},
	{Method: "POST", Path: "/api/v1/rbac/roles", Description: "Create a custom role", Permission: "manage:role", Group: "Roles & Permissions"},
	{Method: "GET", Path: "/api/v1/rbac/roles/:id", Description: "Get role details", Permission: "view:role", Group: "Roles & Permissions"},
	{Method: "PUT", Path: "/api/v1/rbac/roles/:id", Description: "Update a role", Permission: "manage:role", Group: "Roles & Permissions"},
	{Method: "DELETE", Path: "/api/v1/rbac/roles/:id", Description: "Delete a custom role", Permission: "manage:role", Group: "Roles & Permissions"},
	{Method: "GET", Path: "/api/v1/rbac/permissions", Description: "List all permissions", Permission: "view:role", Group: "Roles & Permissions"},

	// ── Settings ────────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/settings", Description: "Get application settings", Permission: "", Group: "Settings"},
	{Method: "PUT", Path: "/api/v1/settings", Description: "Update settings", Permission: "manage:user", Group: "Settings"},
	{Method: "GET", Path: "/api/v1/version", Description: "Get API version", Permission: "", Group: "Settings"},
	{Method: "GET", Path: "/api/v1/search", Description: "Search across all resources", Permission: "", Group: "Settings"},

	// ── Metrics ─────────────────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/clusters/:id/metrics", Description: "Get cluster metrics", Permission: "view:cluster", Group: "Metrics"},
	{Method: "GET", Path: "/api/v1/clusters/:id/nodes/:node_id/metrics", Description: "Get node metrics", Permission: "view:node", Group: "Metrics"},

	// ── API Documentation ───────────────────────────────────────────────
	{Method: "GET", Path: "/api/v1/api-docs", Description: "Get this API reference", Permission: "", Group: "API Documentation"},
}

// GetDocs returns the complete API endpoint catalog.
func (h *APIDocsHandler) GetDocs(c *fiber.Ctx) error {
	return c.JSON(apiEndpoints)
}
