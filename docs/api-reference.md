# API Reference

Nexara exposes a REST API at `/api/v1`. All endpoints (except auth and health) require a valid JWT bearer token.

> **The live source of truth is the in-app catalog at `/settings/api-docs`.**
> That page enumerates every route from the running Fiber router at request
> time, so it cannot drift from what the server actually serves. This file
> covers the auth handshake, error envelope, and WebSocket protocol — the
> parts the auto-generated catalog can't infer — plus a hand-curated
> overview of the major endpoint groups for offline reference.

## Base URL

```
http://localhost/api/v1
```

In production behind a reverse proxy with TLS:
```
https://nexara.example.com/api/v1
```

## Authentication

### JWT Flow

1. **Register** (first user only — anonymous; subsequent registrations require an admin caller):
   ```
   POST /api/v1/auth/register
   Body: { "email": "admin@example.com", "password": "...", "display_name": "Admin" }
   ```

2. **Login**:
   ```
   POST /api/v1/auth/login
   Body: { "email": "admin@example.com", "password": "..." }
   Response: { "access_token": "...", "user": {...}, "expires_at": 1767225600, "permissions": ["view:cluster", ...] }
   ```
   The refresh token is **never** returned in the body — it is set as an
   HttpOnly, SameSite=Strict cookie, and the `refresh_token` response field is
   always an empty string (retained only so the response shape stays stable).
   The same applies to `/auth/register`.

3. **Use the token** on all subsequent requests:
   ```
   Authorization: Bearer <access_token>
   ```

4. **Refresh** when the access token expires:
   ```
   POST /api/v1/auth/refresh
   Body: {}                            # the refresh cookie is read
   Body: { "refresh_token": "..." }    # or pass the token explicitly
   Response: { "access_token": "...", "user": {...}, "expires_at": 1767225600, "permissions": [...] }
   ```
   A missing or stale refresh token returns `401` and clears the cookie.

5. **Logout**:
   ```
   POST /api/v1/auth/logout
   ```

### TOTP Challenge

If the user has 2FA enabled, the login response returns a pending token instead of access/refresh tokens:

```
POST /api/v1/auth/login
Response: { "totp_required": true, "totp_pending_token": "..." }
```

Complete the challenge with a 6-digit TOTP code, or with a recovery code:
```
POST /api/v1/auth/totp/verify-login
Body: { "totp_pending_token": "...", "code": "123456" }
Body: { "totp_pending_token": "...", "recovery_code": "..." }
Response: { "access_token": "...", "user": {...}, "expires_at": 1767225600, "permissions": [...] }
```

### OIDC Flow

1. Check if SSO is available:
   ```
   GET /api/v1/auth/sso-status
   Response: { "oidc_enabled": true, "oidc_provider_name": "Okta" }
   ```

2. Start the OIDC flow:
   ```
   GET /api/v1/auth/oidc/authorize
   Response: { "redirect_url": "https://idp.example.com/authorize?..." }
   ```

3. After the IdP redirects back, exchange the code:
   ```
   POST /api/v1/auth/oidc/token-exchange
   Body: { "code": "..." }
   Response: { "access_token": "...", "user": {...}, "expires_at": 1767225600, "permissions": [...] }
   ```
   As with password login, the refresh token is set as an HttpOnly cookie
   rather than returned in the body, and a user with 2FA enabled gets
   `{ "totp_required": true, "totp_pending_token": "..." }` here instead of
   tokens — complete it against `/auth/totp/verify-login` as above.

## Error Format

All errors return a consistent envelope:

```json
{
  "error": "bad_request",
  "message": "Human-readable description"
}
```

`error` is a stable slug derived from the status code (`bad_request`,
`unauthorized`, `forbidden`, `not_found`, `method_not_allowed`, `conflict`,
`unprocessable_entity`, `too_many_requests`, `internal_server_error`);
`message` is the human-readable detail. An optional `details` object is
reserved in the envelope but no endpoint currently populates it.

Common HTTP status codes:

| Code | Meaning |
|------|---------|
| 400 | Bad request — invalid input |
| 401 | Unauthorized — missing or invalid token |
| 403 | Forbidden — insufficient permissions |
| 404 | Not found |
| 409 | Conflict — resource already exists |
| 429 | Rate limited |
| 500 | Internal server error |

## Rate Limits

All limiters key on the client IP (`c.IP()` — see `TRUSTED_PROXIES` before
deploying behind a reverse proxy) and return `429`. Note these responses come
from the limiter middleware, not the API error handler: the body is the plain
text `Too Many Requests` (`Content-Type: text/plain`), not the JSON error
envelope documented above.

| Scope | Budget | Applies to |
|-------|--------|------------|
| Auth | 15/min | `/auth/login`, `/auth/register`, `/auth/totp/verify-login`, `DELETE /auth/totp`, `/auth/totp/recovery-codes/regenerate`, `/auth/oidc/authorize`, `/auth/oidc/callback` |
| Refresh | 30/min | `/auth/refresh` |
| WS token | 60/min | `/auth/ws-token` |
| Snapshot resync | 30/min | `/clusters/:id/guest-snapshots/resync` |
| General | `RATE_LIMIT_MAX` per `RATE_LIMIT_EXPIRATION` (default 600/min) | Everything whose path does not start with `/api/v1/auth/` or `/ws` — `/healthz` included |

The general limiter's exemption is by path prefix, not by coverage: the auth
paths listed above carry their own budgets, but the remaining `/api/v1/auth/*`
endpoints (`/auth/me`, `/auth/logout`, `/auth/change-password`,
`/auth/totp/setup`, `/auth/oidc/token-exchange`, …) and the `/ws`,
`/ws/console`, `/ws/vnc` upgrades have no request-rate limit at all.

## Pagination & Filtering

List endpoints support query parameters:

| Parameter | Description | Example |
|-----------|-------------|---------|
| `limit` | Max items to return. The default and the ceiling are per-endpoint — most list endpoints default to 50 and cap at 100 or 200, a few default to 500 and cap higher. Out-of-range values are clamped or fall back to the default rather than erroring | `?limit=100` |
| `offset` | Skip N items (negative values are clamped to 0) | `?offset=50` |

Result ordering is fixed per endpoint — there is no generic `sort`/`order`
parameter.

Some endpoints support additional filters documented in their sections below.

Global list endpoints are **scoped to the caller's accessible clusters**
before paging: `/alerts`, `/alert-rules`, `/audit-log`, `/audit-log/recent`,
`/migrations`, `/tasks`, `/reports/schedules` and `/reports/runs` return only
rows for clusters the caller can view with the endpoint's permission
(`view:alert`, `view:audit`, `view:migration`, `view:task`, `view:report`),
and any `total` in the response counts the scoped set. Rows with no cluster
(global alert rules, non-cluster audit entries) are visible only to holders of
the corresponding *global* permission. A caller with no grant at all gets an
empty list rather than an error.

---

## Health

```
GET /healthz
```
Returns `200 OK` when the API server is ready, or `503` when the database ping fails. Not behind authentication, but the general rate limiter does apply — only `/api/v1/auth/*` and `/ws*` are exempt, so a probe interval must stay inside `RATE_LIMIT_MAX` (default 600 per minute per IP).

```
GET /api/v1/version
```
Returns the application version, commit hash, and build time.

```
GET /api/v1/changelog
```
Returns recent release notes from GitHub Releases (feeds the in-app "What's new" dialog). Public, no auth. The source repo is configurable via `CHANGELOG_REPO`.

---

## Endpoint Catalog

### Auth

| Method | Path | Description |
|--------|------|-------------|
| POST | `/auth/register` | Register a new user (first user becomes admin) |
| POST | `/auth/login` | Login with email and password |
| POST | `/auth/refresh` | Refresh access token |
| POST | `/auth/logout` | Logout (invalidate tokens) |
| POST | `/auth/logout-all` | Logout all sessions |
| GET | `/auth/me` | Get current user profile |
| PUT | `/auth/profile` | Update user profile |
| POST | `/auth/change-password` | Change password |
| POST | `/auth/ws-token` | Mint a 60 s scope-locked JWT for the `/ws` hub upgrade |
| POST | `/auth/console-token` | Mint a 60 s scope-locked JWT bound to a single console (`/ws/console`, `/ws/vnc`) |
| GET | `/auth/setup-status` | Check if initial registration is needed |
| GET | `/auth/sso-status` | Check if OIDC/SSO is configured |
| GET | `/auth/oidc/authorize` | Start OIDC authorization flow |
| GET | `/auth/oidc/callback` | OIDC callback (internal) |
| POST | `/auth/oidc/token-exchange` | Exchange OIDC code for JWT |
| POST | `/auth/totp/verify-login` | Complete TOTP challenge |
| POST | `/auth/totp/setup` | Begin TOTP enrollment |
| POST | `/auth/totp/setup/verify` | Confirm TOTP enrollment |
| DELETE | `/auth/totp` | Disable TOTP |
| GET | `/auth/totp/status` | Get TOTP enrollment status |
| POST | `/auth/totp/recovery-codes/regenerate` | Regenerate recovery codes |

### Clusters

| Method | Path | Description |
|--------|------|-------------|
| POST | `/clusters` | Add a new cluster |
| GET | `/clusters` | List all clusters |
| GET | `/clusters/:id` | Get cluster details |
| PUT | `/clusters/:id` | Update cluster |
| DELETE | `/clusters/:id` | Remove cluster |
| POST | `/clusters/fetch-fingerprint` | Fetch TLS fingerprint from a Proxmox URL |

### Cluster Options & Config

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/options` | Get cluster options |
| PUT | `/clusters/:id/options` | Update cluster options |
| GET | `/clusters/:id/description` | Get cluster description |
| PUT | `/clusters/:id/description` | Update cluster description |
| GET | `/clusters/:id/tags` | Get cluster tags |
| PUT | `/clusters/:id/tags` | Update cluster tags |
| GET | `/clusters/:id/config` | Get cluster config (Corosync) |
| GET | `/clusters/:id/config/join` | Get cluster join info |
| GET | `/clusters/:id/config/nodes` | List Corosync nodes |

### Nodes

> `:node` is the Proxmox node *name*; `:node_id` is Nexara's node UUID.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/nodes` | List nodes in a cluster |
| GET | `/clusters/:id/nodes/:node/bridges` | List network bridges |
| GET | `/clusters/:id/nodes/:node/hardware/usb` | List USB devices |
| GET | `/clusters/:id/nodes/:node/hardware/pci` | List PCI devices |
| GET | `/clusters/:id/nodes/:node/machine-types` | List available machine types |
| GET | `/clusters/:id/nodes/:node/cpu-models` | List available CPU models |
| GET | `/clusters/:id/nodes/:node/isos` | List ISO images |
| GET | `/clusters/:id/nodes/:node/packages` | Preview available package updates |
| GET | `/clusters/:id/nodes/:node_id/disks` | List node disks (model, size, health, wearout) |
| GET | `/clusters/:id/nodes/:node_id/network-interfaces` | List node network interfaces |
| GET | `/clusters/:id/nodes/:node_id/pci-devices` | List node PCI devices |
| GET | `/clusters/:id/nodes/:node/dns` | Get node DNS config |
| PUT | `/clusters/:id/nodes/:node/dns` | Set node DNS config |
| GET | `/clusters/:id/nodes/:node/time` | Get node time and timezone |
| PUT | `/clusters/:id/nodes/:node/time` | Set node timezone |
| POST | `/clusters/:id/nodes/:node/shutdown` | Shut down a node |
| POST | `/clusters/:id/nodes/:node/reboot` | Reboot a node |
| POST | `/clusters/:id/nodes/:node/maintenance` | Enter/exit HA node maintenance (needs cluster SSH credentials) |
| POST | `/clusters/:id/nodes/:node/evacuate` | Migrate all guests off a node |
| GET | `/clusters/:id/nodes/:node/services` | List node services |
| POST | `/clusters/:id/nodes/:node/services/:service/:action` | Start/stop/restart a node service |
| GET | `/clusters/:id/nodes/:node/syslog` | Get node syslog (filterable) |

### Node Disks

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/nodes/:node/disks/list` | Live disk inventory from the node |
| GET | `/clusters/:id/nodes/:node/disks/smart` | Get S.M.A.R.T. data for a disk |
| GET | `/clusters/:id/nodes/:node/disks/zfs` | List ZFS pools |
| POST | `/clusters/:id/nodes/:node/disks/zfs` | Create ZFS pool |
| DELETE | `/clusters/:id/nodes/:node/disks/zfs/:pool` | Delete ZFS pool |
| GET | `/clusters/:id/nodes/:node/disks/lvm` | List LVM volume groups |
| POST | `/clusters/:id/nodes/:node/disks/lvm` | Create LVM volume group |
| DELETE | `/clusters/:id/nodes/:node/disks/lvm/:vg` | Delete LVM volume group |
| GET | `/clusters/:id/nodes/:node/disks/lvmthin` | List LVM-thin pools |
| POST | `/clusters/:id/nodes/:node/disks/lvmthin` | Create LVM-thin pool |
| DELETE | `/clusters/:id/nodes/:node/disks/lvmthin/:pool` | Delete LVM-thin pool |
| GET | `/clusters/:id/nodes/:node/disks/directory` | List directory storages |
| POST | `/clusters/:id/nodes/:node/disks/directory` | Create directory storage |
| POST | `/clusters/:id/nodes/:node/disks/initgpt` | Initialize a disk with GPT |
| PUT | `/clusters/:id/nodes/:node/disks/wipe` | Wipe a disk |

### APT Repositories

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/nodes/:node/apt/repositories` | List APT repositories |
| PUT | `/clusters/:id/nodes/:node/apt/repositories` | Enable/disable a repository |
| POST | `/clusters/:id/nodes/:node/apt/repositories` | Add a standard Proxmox repository |

### Virtual Machines

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/vms` | List VMs in a cluster |
| POST | `/clusters/:id/vms` | Create a new VM |
| GET | `/clusters/:id/vms/:vm_id` | Get VM details |
| POST | `/clusters/:id/vms/:vm_id/status` | Perform action (start/stop/shutdown/reboot/suspend/resume) |
| POST | `/clusters/:id/vms/:vm_id/clone` | Clone a VM |
| POST | `/clusters/:id/vms/:vm_id/convert-to-template` | Convert VM to template |
| POST | `/clusters/:id/vms/:vm_id/clone-to-template` | Clone VM as template |
| POST | `/clusters/:id/vms/:vm_id/migrate` | Migrate VM to another node |
| DELETE | `/clusters/:id/vms/:vm_id` | Destroy a VM |
| GET | `/clusters/:id/vms/:vm_id/snapshot-capability` | Check whether the guest can be snapshotted — returns `{ "supported": bool, "blocking_volumes": [...] }` |
| GET | `/clusters/:id/vms/:vm_id/snapshots` | List VM snapshots |
| POST | `/clusters/:id/vms/:vm_id/snapshots` | Create a snapshot |
| DELETE | `/clusters/:id/vms/:vm_id/snapshots/:name` | Delete a snapshot |
| POST | `/clusters/:id/vms/:vm_id/snapshots/:name/rollback` | Rollback to snapshot |
| GET | `/clusters/:id/vms/:vm_id/config` | Get VM configuration |
| PUT | `/clusters/:id/vms/:vm_id/config` | Update VM configuration |
| GET | `/clusters/:id/vms/:vm_id/agent` | Get QEMU guest agent info |
| POST | `/clusters/:id/vms/:vm_id/disks/resize` | Resize a disk |
| POST | `/clusters/:id/vms/:vm_id/disks/move` | Move a disk to another storage |
| POST | `/clusters/:id/vms/:vm_id/disks/attach` | Attach a disk |
| POST | `/clusters/:id/vms/:vm_id/disks/detach` | Detach a disk |
| POST | `/clusters/:id/vms/:vm_id/media` | Change CD/DVD media |
| PUT | `/clusters/:id/vms/:vm_id/pool` | Set VM resource pool |

### VM Folders

Organize guests into folders in the Nexara inventory tree (Nexara-side only — Proxmox is untouched).

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/vm-folders` | List VM folders |
| POST | `/clusters/:id/vm-folders` | Create folder |
| PATCH | `/clusters/:id/vm-folders/:folder_id` | Update folder (PATCH, not PUT) |
| DELETE | `/clusters/:id/vm-folders/:folder_id` | Delete folder |
| PUT | `/clusters/:id/vms/:vm_id/folder` | Assign a VM to a folder |

### Containers (LXC)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/containers` | List containers in a cluster |
| POST | `/clusters/:id/containers` | Create a new container |
| GET | `/clusters/:id/containers/:ct_id` | Get container details |
| POST | `/clusters/:id/containers/:ct_id/status` | Perform action (start/stop/shutdown/reboot) |
| POST | `/clusters/:id/containers/:ct_id/clone` | Clone a container |
| POST | `/clusters/:id/containers/:ct_id/convert-to-template` | Convert to template |
| POST | `/clusters/:id/containers/:ct_id/clone-to-template` | Clone as template |
| POST | `/clusters/:id/containers/:ct_id/migrate` | Migrate container |
| DELETE | `/clusters/:id/containers/:ct_id` | Destroy a container |
| GET | `/clusters/:id/containers/:ct_id/snapshot-capability` | Check whether the container can be snapshotted — returns `{ "supported": bool, "blocking_volumes": [...] }` |
| GET | `/clusters/:id/containers/:ct_id/snapshots` | List snapshots |
| POST | `/clusters/:id/containers/:ct_id/snapshots` | Create a snapshot |
| DELETE | `/clusters/:id/containers/:ct_id/snapshots/:name` | Delete a snapshot |
| POST | `/clusters/:id/containers/:ct_id/snapshots/:name/rollback` | Rollback to snapshot |
| GET | `/clusters/:id/containers/:ct_id/config` | Get container configuration |
| PUT | `/clusters/:id/containers/:ct_id/config` | Update container config |
| POST | `/clusters/:id/containers/:ct_id/disks/resize` | Resize a container disk |
| POST | `/clusters/:id/containers/:ct_id/volumes/move` | Move a volume |

### Guest Snapshots (central inventory)

Cluster-wide snapshot inventory collected by the snapshot sync loop, so the
snapshots page doesn't have to fan out to every guest. Access is split per
row: QEMU rows require `view:vm` on the row's cluster, LXC rows
`view:container`.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/guest-snapshots` | List every guest snapshot the caller may see, across all clusters (optional `?cluster_id=` filter) |
| POST | `/clusters/:id/guest-snapshots/resync` | Re-read one guest's snapshots from Proxmox — body `{ "vmid": 101 }`. Rate-limited to 30/min/IP on top of the general limiter |

### Storage

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/storage` | List storage in a cluster |
| POST | `/clusters/:id/storage` | Create storage |
| GET | `/clusters/:id/storage/:sid/config` | Get storage configuration |
| PUT | `/clusters/:id/storage/:sid` | Update storage |
| DELETE | `/clusters/:id/storage/:sid` | Delete storage |
| GET | `/clusters/:id/storage/:sid/content` | List storage content |
| POST | `/clusters/:id/storage/:sid/upload` | Upload a file |
| DELETE | `/clusters/:id/storage/:sid/content/*` | Delete content |
| POST | `/clusters/:id/storage/:sid/oci-pull` | Pull an OCI image as a container template |
| POST | `/clusters/:id/storage/:sid/download-url` | Download a file from a URL to storage |
| POST | `/clusters/:id/storage/:sid/appliances` | Download a turnkey appliance |
| GET | `/clusters/:id/appliances` | List available appliance templates |
| GET | `/clusters/:id/scan/iscsi` | Discover iSCSI targets on a portal — `?portal=<host[:port]>`; returns `[{ "target", "portal" }]`. **Requires `manage:storage`** (node-side network probe) |

### VM Import

Import VMs from ESXi/vCenter sources, OVA/OVF appliances, or disk images. Reads require `view:vm_import`, mutations `manage:vm_import`, except where noted.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/clusters/:id/import-metadata` | Parse guest metadata (CPU, memory, disks, OS) from an importable source |
| GET | `/clusters/:id/query-url-metadata` | Probe a download URL for filename/size — **requires `manage:storage`** (node-side network primitive) |
| GET | `/clusters/:id/vm-import-sources` | List registered import sources |
| GET | `/clusters/:id/vm-import-sources/content` | List importable content across sources |
| POST | `/clusters/:id/vm-import-sources/esxi` | Register an ESXi/vCenter source |
| POST | `/clusters/:id/vm-import-sources/enable-content` | Enable `import` content on an existing storage — **requires `manage:storage`** |
| DELETE | `/clusters/:id/vm-import-sources/:storage` | Unregister an import source (only accepts import-source storages) |
| GET | `/clusters/:id/vm-imports` | List import jobs |
| POST | `/clusters/:id/vm-imports` | Start an import |
| GET | `/clusters/:id/vm-imports/:id` | Get import job status |
| POST | `/clusters/:id/vm-imports/:id/cancel` | Cancel an import (optionally deleting the partially created VM) |

### Resource Pools

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/pools` | List resource pools |
| POST | `/clusters/:id/pools` | Create pool |
| GET | `/clusters/:id/pools/:pool_id` | Get pool details |
| PUT | `/clusters/:id/pools/:pool_id` | Update pool |
| DELETE | `/clusters/:id/pools/:pool_id` | Delete pool |

### Metrics

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/metrics` | Get cluster historical metrics |
| GET | `/clusters/:id/vms/:vm_id/metrics` | Get VM historical metrics |
| GET | `/clusters/:id/nodes/:node_id/metrics` | Get node historical metrics |

### Ceph

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/ceph/status` | Get Ceph cluster status |
| GET | `/clusters/:id/ceph/osds` | List OSDs |
| GET | `/clusters/:id/ceph/pools` | List Ceph pools |
| GET | `/clusters/:id/ceph/monitors` | List monitors |
| GET | `/clusters/:id/ceph/fs` | List CephFS |
| GET | `/clusters/:id/ceph/rules` | List CRUSH rules |
| POST | `/clusters/:id/ceph/pools` | Create Ceph pool |
| DELETE | `/clusters/:id/ceph/pools/:name` | Delete Ceph pool |
| GET | `/clusters/:id/ceph/osds/:osd_id/preflight?action=` | Assess the redundancy impact of an OSD action |
| POST | `/clusters/:id/ceph/osds/:osd_id/in` | Mark OSD in |
| POST | `/clusters/:id/ceph/osds/:osd_id/out` | Mark OSD out |
| POST | `/clusters/:id/ceph/osds/:osd_id/start` | Start OSD daemon |
| POST | `/clusters/:id/ceph/osds/:osd_id/stop` | Stop OSD daemon |
| POST | `/clusters/:id/ceph/osds/:osd_id/restart` | Restart OSD daemon |
| GET | `/clusters/:id/ceph/metrics` | Get Ceph historical metrics |
| GET | `/clusters/:id/ceph/osds/metrics` | Get OSD metrics |
| GET | `/clusters/:id/ceph/pools/metrics` | Get pool metrics |

### Networking

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/networks` | List all network interfaces |
| GET | `/clusters/:id/networks/:node` | List node network interfaces |
| POST | `/clusters/:id/networks/:node` | Create network interface |
| PUT | `/clusters/:id/networks/:node/:iface` | Update network interface |
| DELETE | `/clusters/:id/networks/:node/:iface` | Delete network interface |
| POST | `/clusters/:id/networks/:node/apply` | Apply network config |
| POST | `/clusters/:id/networks/:node/revert` | Revert network config |

### Firewall

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/firewall/rules` | List cluster firewall rules |
| POST | `/clusters/:id/firewall/rules` | Create firewall rule |
| PUT | `/clusters/:id/firewall/rules/:pos` | Update firewall rule |
| DELETE | `/clusters/:id/firewall/rules/:pos` | Delete firewall rule |
| GET | `/clusters/:id/firewall/options` | Get firewall options |
| PUT | `/clusters/:id/firewall/options` | Set firewall options |
| GET | `/clusters/:id/vms/:vm_id/firewall/rules` | List VM firewall rules |
| POST | `/clusters/:id/vms/:vm_id/firewall/rules` | Create VM firewall rule |
| PUT | `/clusters/:id/vms/:vm_id/firewall/rules/:pos` | Update VM firewall rule |
| DELETE | `/clusters/:id/vms/:vm_id/firewall/rules/:pos` | Delete VM firewall rule |
| GET | `/clusters/:id/nodes/:node/firewall/rules` | List node firewall rules |
| POST | `/clusters/:id/nodes/:node/firewall/rules` | Create node firewall rule |
| PUT | `/clusters/:id/nodes/:node/firewall/rules/:pos` | Update node firewall rule |
| DELETE | `/clusters/:id/nodes/:node/firewall/rules/:pos` | Delete node firewall rule |
| GET | `/clusters/:id/nodes/:node/firewall/log` | Get node firewall log |
| GET | `/clusters/:id/firewall/aliases` | List firewall aliases |
| POST | `/clusters/:id/firewall/aliases` | Create alias |
| PUT | `/clusters/:id/firewall/aliases/:name` | Update alias |
| DELETE | `/clusters/:id/firewall/aliases/:name` | Delete alias |
| GET | `/clusters/:id/firewall/ipset` | List IP sets |
| POST | `/clusters/:id/firewall/ipset` | Create IP set |
| DELETE | `/clusters/:id/firewall/ipset/:name` | Delete IP set |
| GET | `/clusters/:id/firewall/ipset/:name/entries` | List IP set entries |
| POST | `/clusters/:id/firewall/ipset/:name/entries` | Add IP set entry |
| DELETE | `/clusters/:id/firewall/ipset/:name/entries/:cidr` | Delete IP set entry |
| GET | `/clusters/:id/firewall/groups` | List security groups |
| POST | `/clusters/:id/firewall/groups` | Create security group |
| DELETE | `/clusters/:id/firewall/groups/:group` | Delete security group |
| GET | `/clusters/:id/firewall/groups/:group/rules` | List group rules |
| POST | `/clusters/:id/firewall/groups/:group/rules` | Create group rule |
| PUT | `/clusters/:id/firewall/groups/:group/rules/:pos` | Update group rule |
| DELETE | `/clusters/:id/firewall/groups/:group/rules/:pos` | Delete group rule |
| GET | `/clusters/:id/firewall/log` | Get firewall log |

### Firewall Templates

| Method | Path | Description |
|--------|------|-------------|
| GET | `/firewall-templates` | List templates |
| POST | `/firewall-templates` | Create template |
| GET | `/firewall-templates/:id` | Get template |
| PUT | `/firewall-templates/:id` | Update template |
| DELETE | `/firewall-templates/:id` | Delete template |
| POST | `/clusters/:id/firewall-templates/:id/apply` | Apply template to cluster |

### SDN (Software-Defined Networking)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/sdn/zones` | List SDN zones |
| POST | `/clusters/:id/sdn/zones` | Create zone |
| PUT | `/clusters/:id/sdn/zones/:zone` | Update zone |
| DELETE | `/clusters/:id/sdn/zones/:zone` | Delete zone |
| GET | `/clusters/:id/sdn/vnets` | List VNets |
| POST | `/clusters/:id/sdn/vnets` | Create VNet |
| PUT | `/clusters/:id/sdn/vnets/:vnet` | Update VNet |
| DELETE | `/clusters/:id/sdn/vnets/:vnet` | Delete VNet |
| GET | `/clusters/:id/sdn/vnets/:vnet/subnets` | List subnets |
| POST | `/clusters/:id/sdn/vnets/:vnet/subnets` | Create subnet |
| PUT | `/clusters/:id/sdn/vnets/:vnet/subnets/:subnet` | Update subnet |
| DELETE | `/clusters/:id/sdn/vnets/:vnet/subnets/:subnet` | Delete subnet |
| PUT | `/clusters/:id/sdn/apply` | Apply SDN config |
| GET | `/clusters/:id/sdn/controllers` | List SDN controllers |
| POST | `/clusters/:id/sdn/controllers` | Create controller |
| PUT | `/clusters/:id/sdn/controllers/:controller` | Update controller |
| DELETE | `/clusters/:id/sdn/controllers/:controller` | Delete controller |
| GET | `/clusters/:id/sdn/ipams` | List IPAMs |
| POST | `/clusters/:id/sdn/ipams` | Create IPAM |
| PUT | `/clusters/:id/sdn/ipams/:ipam` | Update IPAM |
| DELETE | `/clusters/:id/sdn/ipams/:ipam` | Delete IPAM |
| GET | `/clusters/:id/sdn/dns` | List DNS configs |
| POST | `/clusters/:id/sdn/dns` | Create DNS config |
| PUT | `/clusters/:id/sdn/dns/:dns` | Update DNS config |
| DELETE | `/clusters/:id/sdn/dns/:dns` | Delete DNS config |

### HA (High Availability)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/ha/resources` | List HA resources |
| POST | `/clusters/:id/ha/resources` | Create HA resource |
| GET | `/clusters/:id/ha/resources/:sid` | Get HA resource |
| PUT | `/clusters/:id/ha/resources/:sid` | Update HA resource |
| DELETE | `/clusters/:id/ha/resources/:sid` | Delete HA resource |
| GET | `/clusters/:id/ha/groups` | List HA groups |
| POST | `/clusters/:id/ha/groups` | Create HA group |
| PUT | `/clusters/:id/ha/groups/:group` | Update HA group |
| DELETE | `/clusters/:id/ha/groups/:group` | Delete HA group |
| GET | `/clusters/:id/ha/status` | Get HA status |
| GET | `/clusters/:id/ha/rules` | List HA rules |
| POST | `/clusters/:id/ha/rules` | Create HA rule |
| PUT | `/clusters/:id/ha/rules/:rule` | Update HA rule |
| DELETE | `/clusters/:id/ha/rules/:rule` | Delete HA rule |
| GET | `/clusters/:id/ha/manager-status` | Get HA manager status |
| POST | `/clusters/:id/ha/arm` | Re-arm HA cluster-wide (PVE 9.2+) |
| POST | `/clusters/:id/ha/disarm` | Disarm HA cluster-wide, freezing or ignoring resources (PVE 9.2+) |

### Replication

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/replication` | List replication jobs |
| POST | `/clusters/:id/replication` | Create replication job |
| GET | `/clusters/:id/replication/:job_id` | Get replication job |
| PUT | `/clusters/:id/replication/:job_id` | Update replication job |
| DELETE | `/clusters/:id/replication/:job_id` | Delete replication job |
| POST | `/clusters/:id/replication/:job_id/trigger` | Trigger sync |
| GET | `/clusters/:id/replication/:job_id/status` | Get replication status |
| GET | `/clusters/:id/replication/:job_id/log` | Get replication log |

### ACME Certificates

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/acme/accounts` | List ACME accounts |
| POST | `/clusters/:id/acme/accounts` | Create ACME account |
| GET | `/clusters/:id/acme/accounts/:name` | Get account |
| PUT | `/clusters/:id/acme/accounts/:name` | Update account |
| DELETE | `/clusters/:id/acme/accounts/:name` | Delete account |
| GET | `/clusters/:id/acme/plugins` | List ACME plugins |
| POST | `/clusters/:id/acme/plugins` | Create plugin |
| PUT | `/clusters/:id/acme/plugins/:id` | Update plugin |
| DELETE | `/clusters/:id/acme/plugins/:id` | Delete plugin |
| GET | `/clusters/:id/acme/challenge-schema` | List challenge schemas |
| GET | `/clusters/:id/acme/directories` | List directories |
| GET | `/clusters/:id/acme/tos` | Get terms of service |
| GET | `/clusters/:id/nodes/:node/acme-config` | Get node ACME config |
| PUT | `/clusters/:id/nodes/:node/acme-config` | Set node ACME config |
| GET | `/clusters/:id/nodes/:node/certificates` | List certificates |
| POST | `/clusters/:id/nodes/:node/certificates/order` | Order certificate |
| PUT | `/clusters/:id/nodes/:node/certificates/renew` | Renew certificate |
| DELETE | `/clusters/:id/nodes/:node/certificates/revoke` | Revoke certificate |

### Metric Servers

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/metric-servers` | List external metric servers |
| POST | `/clusters/:id/metric-servers` | Create metric server |
| GET | `/clusters/:id/metric-servers/:sid` | Get metric server |
| PUT | `/clusters/:id/metric-servers/:sid` | Update metric server |
| DELETE | `/clusters/:id/metric-servers/:sid` | Delete metric server |

### PBS (Proxmox Backup Server)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/pbs-servers` | Add a PBS server |
| GET | `/pbs-servers` | List PBS servers |
| GET | `/pbs-servers/:id` | Get PBS server |
| PUT | `/pbs-servers/:id` | Update PBS server |
| DELETE | `/pbs-servers/:id` | Remove PBS server |
| GET | `/clusters/:id/pbs-servers` | List the PBS servers attached to a cluster (requires `view:pbs` on that cluster) |
| GET | `/pbs-servers/:id/datastores` | List datastores |
| GET | `/pbs-servers/:id/datastores/status` | Get datastore status |
| POST | `/pbs-servers/:id/datastores/:store/gc` | Trigger garbage collection |
| DELETE | `/pbs-servers/:id/datastores/:store/snapshots` | Delete snapshot |
| PUT | `/pbs-servers/:id/datastores/:store/snapshots/protect` | Protect/unprotect snapshot |
| PUT | `/pbs-servers/:id/datastores/:store/snapshots/notes` | Update snapshot notes |
| POST | `/pbs-servers/:id/datastores/:store/prune` | Prune datastore |
| GET | `/pbs-servers/:id/datastores/:store/rrd` | Get datastore RRD data |
| GET | `/pbs-servers/:id/datastores/:store/config` | Get datastore config |
| GET | `/pbs-servers/:id/snapshots` | List all snapshots |
| GET | `/pbs-servers/:id/sync-jobs` | List sync jobs |
| POST | `/pbs-servers/:id/sync-jobs/:job_id/run` | Run sync job |
| GET | `/pbs-servers/:id/verify-jobs` | List verify jobs |
| POST | `/pbs-servers/:id/verify-jobs/:job_id/run` | Run verify job |
| GET | `/pbs-servers/:id/tasks` | List PBS tasks |
| GET | `/pbs-servers/:id/tasks/:upid` | Get PBS task status |
| GET | `/pbs-servers/:id/tasks/:upid/log` | Get PBS task log |
| GET | `/pbs-servers/:id/metrics` | Get datastore metrics |

### Backup & Restore

| Method | Path | Description |
|--------|------|-------------|
| GET | `/pbs-snapshots` | List snapshots by backup ID |
| GET | `/backup-coverage` | Get backup coverage report |
| POST | `/clusters/:id/restore` | Restore a backup |
| POST | `/clusters/:id/backup` | Trigger an ad-hoc backup |
| GET | `/clusters/:id/backup-jobs` | List backup jobs |
| POST | `/clusters/:id/backup-jobs` | Create backup job |
| PUT | `/clusters/:id/backup-jobs/:job_id` | Update backup job |
| DELETE | `/clusters/:id/backup-jobs/:job_id` | Delete backup job |
| POST | `/clusters/:id/backup-jobs/:job_id/run` | Run backup job |

A backup job carries exactly one guest selection, set on create and update:

| Field | Meaning |
|-------|---------|
| `all: 1` | Back up every guest on the cluster |
| `exclude: "101,102"` | Every guest except these VMIDs — implies `all: 1`, which the server sends for you |
| `pool: "<name>"` | Every guest in a resource pool |
| `vmid: "101,102"` | Exactly these VMIDs |

They are mutually exclusive and evaluated in the order `exclude`, `all`, `pool`,
`vmid` — an exclusion list wins (and travels with `all: 1`), then `all`, then a
pool, then an explicit VMID list. On update, the selection keys the request does *not* name
are unset on the job, so switching a job from an explicit VMID list to a pool
clears the list. A request naming no selection at all leaves the job's current
selection untouched.

The `schedule` field is a Proxmox **systemd calendar event** (`02:00`,
`mon,fri 22:30`, `*/6:00`, `*-*-01 04:00`), not a cron expression.

### Migrations (Cross-Cluster)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/migrations` | Create a cross-cluster migration |
| GET | `/migrations` | List migrations the caller can view on either endpoint cluster |
| GET | `/migrations/:id` | Get migration details |
| POST | `/migrations/:id/check` | Run pre-migration check |
| POST | `/migrations/:id/execute` | Execute migration |
| POST | `/migrations/:id/cancel` | Cancel migration |
| GET | `/clusters/:id/migrations` | List migrations for a cluster |

### CVE Scanning

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/cve-scans` | List CVE scans |
| POST | `/clusters/:id/cve-scans` | Trigger a CVE scan |
| GET | `/clusters/:id/cve-scans/:scan_id` | Get scan details |
| GET | `/clusters/:id/cve-scans/:scan_id/vulnerabilities` | List vulnerabilities |
| DELETE | `/clusters/:id/cve-scans/:scan_id` | Delete a scan |
| GET | `/clusters/:id/security-posture` | Get security posture score |
| GET | `/clusters/:id/cve-scan-schedule` | Get scan schedule |
| PUT | `/clusters/:id/cve-scan-schedule` | Update scan schedule |
| GET | `/clusters/:id/cve-notifications` | Get CVE notification config |
| PUT | `/clusters/:id/cve-notifications` | Update CVE notification config |

### Alerts

| Method | Path | Description |
|--------|------|-------------|
| GET | `/alerts` | List alerts across the caller's accessible clusters |
| GET | `/alerts/summary` | Get alert summary counts |
| GET | `/alerts/:id` | Get alert details |
| POST | `/alerts/:id/acknowledge` | Acknowledge an alert |
| POST | `/alerts/:id/resolve` | Resolve an alert |
| GET | `/clusters/:id/alerts` | List alerts for a cluster |
| GET | `/clusters/:id/alerts/count` | Count active alerts for a cluster |

### Alert Rules

| Method | Path | Description |
|--------|------|-------------|
| GET | `/alert-rules` | List alert rules |
| POST | `/alert-rules` | Create alert rule |
| GET | `/alert-rules/:id` | Get alert rule |
| PUT | `/alert-rules/:id` | Update alert rule |
| DELETE | `/alert-rules/:id` | Delete alert rule |

### Notification Channels

| Method | Path | Description |
|--------|------|-------------|
| GET | `/notification-channels` | List channels |
| POST | `/notification-channels` | Create channel |
| GET | `/notification-channels/:id` | Get channel |
| PUT | `/notification-channels/:id` | Update channel |
| DELETE | `/notification-channels/:id` | Delete channel |
| POST | `/notification-channels/:id/test` | Send test notification |

### Notification Dead-Letter Queue

Failed notification deliveries land here for inspection, retry, or dismissal.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/notification-dlq` | List failed notifications |
| GET | `/notification-dlq/summary` | Get failure counts |
| POST | `/notification-dlq/:id/retry` | Retry a failed notification |
| POST | `/notification-dlq/:id/dismiss` | Dismiss a failed notification |
| DELETE | `/notification-dlq/:id` | Delete a DLQ entry |

### Maintenance Windows

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/maintenance-windows` | List maintenance windows |
| POST | `/clusters/:id/maintenance-windows` | Create window |
| PUT | `/clusters/:id/maintenance-windows/:id` | Update window |
| DELETE | `/clusters/:id/maintenance-windows/:id` | Delete window |

### DRS (Distributed Resource Scheduler)

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/drs/config` | Get DRS config |
| PUT | `/clusters/:id/drs/config` | Update DRS config |
| GET | `/clusters/:id/drs/rules` | List DRS rules |
| POST | `/clusters/:id/drs/rules` | Create DRS rule |
| DELETE | `/clusters/:id/drs/rules/:rule_id` | Delete DRS rule |
| POST | `/clusters/:id/drs/evaluate` | Trigger DRS evaluation |
| GET | `/clusters/:id/drs/history` | List DRS evaluation history |
| GET | `/clusters/:id/drs/ha-rules` | List HA-aware DRS rules |
| POST | `/clusters/:id/drs/ha-rules` | Create HA-aware DRS rule |
| DELETE | `/clusters/:id/drs/ha-rules/:name` | Delete HA-aware DRS rule |

### Rolling Updates

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/rolling-updates` | List rolling update jobs |
| POST | `/clusters/:id/rolling-updates` | Create job |
| GET | `/clusters/:id/rolling-updates/:id` | Get job details |
| POST | `/clusters/:id/rolling-updates/:id/start` | Start job |
| POST | `/clusters/:id/rolling-updates/:id/cancel` | Cancel job |
| POST | `/clusters/:id/rolling-updates/:id/pause` | Pause job |
| POST | `/clusters/:id/rolling-updates/:id/resume` | Resume job |
| GET | `/clusters/:id/rolling-updates/:id/nodes` | List node statuses |
| POST | `/clusters/:id/rolling-updates/:id/nodes/:nid/confirm-upgrade` | Confirm upgrade |
| POST | `/clusters/:id/rolling-updates/:id/nodes/:nid/skip` | Skip node |
| POST | `/clusters/:id/rolling-updates/preflight-ha` | Pre-flight HA check |

### SSH Credentials

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/ssh-credentials` | Get SSH credentials |
| PUT | `/clusters/:id/ssh-credentials` | Create/update SSH credentials |
| DELETE | `/clusters/:id/ssh-credentials` | Delete SSH credentials |
| POST | `/clusters/:id/ssh-credentials/test` | Test SSH connection |
| GET | `/clusters/:id/ssh-known-hosts` | List pinned SSH host keys |
| POST | `/clusters/:id/ssh-known-hosts` | Pin an SSH host key |
| DELETE | `/clusters/:id/ssh-known-hosts/:id` | Remove a pinned host key |

### Schedules

| Method | Path | Description |
|--------|------|-------------|
| POST | `/clusters/:id/schedules` | Create scheduled task |
| GET | `/clusters/:id/schedules` | List schedules |
| PUT | `/clusters/:id/schedules/:id` | Update schedule |
| DELETE | `/clusters/:id/schedules/:id` | Delete schedule |

### Reports

| Method | Path | Description |
|--------|------|-------------|
| GET | `/reports/schedules` | List report schedules |
| POST | `/reports/schedules` | Create report schedule |
| GET | `/reports/schedules/:id` | Get schedule |
| PUT | `/reports/schedules/:id` | Update schedule |
| DELETE | `/reports/schedules/:id` | Delete schedule |
| POST | `/reports/generate` | Generate a report |
| GET | `/reports/runs` | List report runs |
| GET | `/reports/runs/:id` | Get report run |
| GET | `/reports/runs/:id/html` | Download report as HTML |
| GET | `/reports/runs/:id/csv` | Download report as CSV |

`report_type` accepts: `resource_utilization`, `capacity_forecast`,
`backup_compliance`, `patch_status`, `uptime_summary`, `vm_resource_usage`,
`snapshot_inventory`. Any other value is rejected with `400`.

### Tasks

| Method | Path | Description |
|--------|------|-------------|
| GET | `/tasks` | List tracked tasks |
| POST | `/tasks` | Create a task entry |
| PUT | `/tasks/:upid` | Update task status |
| DELETE | `/tasks` | Clear completed tasks |
| GET | `/clusters/:id/tasks/:upid` | Get Proxmox task status |
| GET | `/clusters/:id/tasks/:upid/log` | Get Proxmox task log |

### Audit Log

| Method | Path | Description |
|--------|------|-------------|
| GET | `/audit-log` | List audit entries across the caller's accessible clusters |
| GET | `/audit-log/recent` | List recent entries |
| GET | `/audit-log/actions` | List distinct action types |
| GET | `/audit-log/users` | List distinct users |
| GET | `/audit-log/export` | Export audit log |
| GET | `/audit-log/syslog-config` | Get syslog forwarding config |
| PUT | `/audit-log/syslog-config` | Update syslog config |
| POST | `/audit-log/syslog-test` | Test syslog forwarding |
| GET | `/clusters/:id/audit-log` | List audit entries for a cluster |

### RBAC

| Method | Path | Description |
|--------|------|-------------|
| GET | `/rbac/roles` | List roles |
| POST | `/rbac/roles` | Create role |
| GET | `/rbac/roles/:id` | Get role |
| PUT | `/rbac/roles/:id` | Update role |
| DELETE | `/rbac/roles/:id` | Delete role |
| GET | `/rbac/permissions` | List all permissions |
| GET | `/rbac/users/:user_id/roles` | List user's roles |
| POST | `/rbac/users/:user_id/roles` | Assign role to user |
| DELETE | `/rbac/users/:user_id/roles/:id` | Revoke role from user |
| GET | `/rbac/me/permissions` | List current user's permissions |

### Users

| Method | Path | Description |
|--------|------|-------------|
| GET | `/users` | List all users |
| GET | `/users/:id` | Get user |
| PUT | `/users/:id` | Update user |
| DELETE | `/users/:id` | Delete user |
| DELETE | `/users/:id/totp` | Admin reset user's TOTP |

### LDAP

| Method | Path | Description |
|--------|------|-------------|
| GET | `/ldap/configs` | List LDAP configs |
| POST | `/ldap/configs` | Create LDAP config |
| GET | `/ldap/configs/:id` | Get LDAP config |
| PUT | `/ldap/configs/:id` | Update LDAP config |
| DELETE | `/ldap/configs/:id` | Delete LDAP config |
| POST | `/ldap/configs/:id/test` | Test LDAP connection |
| POST | `/ldap/configs/:id/sync` | Sync LDAP users |

### OIDC

| Method | Path | Description |
|--------|------|-------------|
| GET | `/oidc/configs` | List OIDC configs |
| POST | `/oidc/configs` | Create OIDC config |
| GET | `/oidc/configs/:id` | Get OIDC config |
| PUT | `/oidc/configs/:id` | Update OIDC config |
| DELETE | `/oidc/configs/:id` | Delete OIDC config |
| POST | `/oidc/configs/:id/test` | Test OIDC connection |

### Settings

| Method | Path | Description |
|--------|------|-------------|
| GET | `/settings` | List all settings |
| GET | `/settings/branding` | Get branding settings |
| GET | `/settings/branding/logo-file` | Serve logo image |
| GET | `/settings/branding/favicon-file` | Serve favicon |
| POST | `/settings/branding/logo` | Upload logo |
| POST | `/settings/branding/favicon` | Upload favicon |
| GET | `/settings/:key` | Get a setting by key |
| PUT | `/settings/:key` | Create/update a setting |
| DELETE | `/settings/:key` | Delete a setting |

### API Keys

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api-keys` | Create an API key |
| GET | `/api-keys` | List your API keys |
| DELETE | `/api-keys/:id` | Revoke an API key |
| DELETE | `/api-keys` | Revoke all your API keys |
| GET | `/admin/api-keys` | Admin: list all API keys |
| DELETE | `/admin/api-keys/:id` | Admin: revoke any API key |

### Search

| Method | Path | Description |
|--------|------|-------------|
| GET | `/search` | Global search across clusters, VMs, nodes, storage |

### API Documentation

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api-docs` | Get API documentation |

## WebSocket

The WebSocket server runs on the same port as the API and provides real-time
data streaming and console access.

| Path | Description |
|------|-------------|
| `/ws` | Metric and event subscription hub |
| `/ws/console` | Serial / node-shell console proxy (xterm.js) |
| `/ws/vnc` | VNC console proxy (noVNC) |

### Authentication

The long-lived access token is **never** accepted on a WebSocket upgrade.
Every connection requires a short-lived (60 s) scope-locked JWT minted
right before the upgrade:

| Endpoint | Purpose |
|----------|---------|
| `POST /api/v1/auth/ws-token` | Hub token for `/ws` subscription channels |
| `POST /api/v1/auth/console-token` | Console token bound to a single `(cluster, node, vmid, type)` tuple for `/ws/console` or `/ws/vnc` |

Minting a console token requires the dedicated **`console:*`** permission on
the target cluster, not `view:*`: `console:node` for `node_shell`,
`console:vm` for `vm_serial`/`vm_vnc`, and `console:container` for
`ct_attach`/`ct_vnc`. The built-in Viewer role holds every `view:*`
permission and deliberately holds none of these. Each mint is written to the
audit log unless the request sets `"silent": true`, which is honoured only for
the two VNC types (background thumbnail previews) and still enforces RBAC.

The token rides in the WebSocket subprotocol so it never appears in URLs,
proxy logs, or `Referer` headers:

```
Sec-WebSocket-Protocol: nexara.token, nexara.token.<jwt>
```

In the browser this looks like:

```js
const { token } = await fetch("/api/v1/auth/ws-token", { method: "POST" }).then(r => r.json());
const ws = new WebSocket(
  `wss://nexara.example.com/ws`,
  ["nexara.token", `nexara.token.${token}`],
);
```

### Subscribing to channels

Once `/ws` is open, send JSON messages to subscribe to real-time data. The
message carries a `type` and an array of `channels`:

```json
{"type": "subscribe", "channels": ["cluster:<cluster_id>:metrics", "cluster:<cluster_id>:events"]}
{"type": "unsubscribe", "channels": ["cluster:<cluster_id>:metrics"]}
{"type": "ping"}
```

Valid channels:

| Channel | Contents | Required permission |
|---------|----------|---------------------|
| `cluster:<cluster_id>:metrics` | Live metric samples for the cluster | `view:cluster` on that cluster |
| `cluster:<cluster_id>:alerts` | Alert state changes for the cluster | `view:cluster` on that cluster |
| `cluster:<cluster_id>:events` | Operational events for the cluster | `view:cluster` on that cluster |
| `cluster:<cluster_id>:audit` | Audit entries for the cluster | `view:audit` on that cluster |
| `system:events` | Non-cluster operational events (task updates, report completion, PBS changes) | Any authenticated session |
| `system:audit` | Non-cluster audit entries | Global `view:audit` |

The server replies `{"type": "welcome"}` on connect, `{"type": "subscribed",
"channel": "..."}` per accepted channel, `{"type": "data", "channel": "...",
"payload": {...}}` for streamed data, `{"type": "pong"}` for a ping, and
`{"type": "error", "message": "..."}` when a channel is malformed
(`invalid channel format`) or denied (`forbidden`). A rejected channel is
skipped — the connection and any other subscriptions stay open.

### Console connections

VNC and serial console WebSocket connections proxy directly to Proxmox.
Mint a console token first (which embeds the target tuple), then upgrade
with the scope params on the URL:

```
POST /api/v1/auth/console-token
Body: { "cluster_id": "<uuid>", "node": "<name>", "vmid": <int>, "type": "vm_vnc" }
Response: { "token": "...", "expires_in": 60 }
```

```
wss://nexara.example.com/ws/vnc?cluster_id=<uuid>&node=<name>&vmid=<int>
wss://nexara.example.com/ws/console?cluster_id=<uuid>&node=<name>&vmid=<int>&type=<console_type>
Sec-WebSocket-Protocol: nexara.token, nexara.token.<console_jwt>
```

`type` values: `node_shell`, `vm_serial`, `vm_vnc`, `ct_attach`, `ct_vnc`.
A console token is single-purpose — it's rejected on any upgrade whose
scope tuple doesn't match the one it was minted for.
