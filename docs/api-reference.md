# API Reference

Nexara exposes a REST API at `/api/v1`. All endpoints (except auth and health) require a valid JWT bearer token.

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

1. **Register** (first user only):
   ```
   POST /api/v1/auth/register
   Body: { "username": "admin", "email": "admin@example.com", "password": "..." }
   ```

2. **Login**:
   ```
   POST /api/v1/auth/login
   Body: { "username": "admin", "password": "..." }
   Response: { "access_token": "...", "refresh_token": "...", "user": {...} }
   ```

3. **Use the token** on all subsequent requests:
   ```
   Authorization: Bearer <access_token>
   ```

4. **Refresh** when the access token expires:
   ```
   POST /api/v1/auth/refresh
   Body: { "refresh_token": "..." }
   Response: { "access_token": "...", "refresh_token": "..." }
   ```

5. **Logout**:
   ```
   POST /api/v1/auth/logout
   ```

### TOTP Challenge

If the user has 2FA enabled, the login response returns a `totp_pending` token instead of access/refresh tokens:

```
POST /api/v1/auth/login
Response: { "totp_required": true, "totp_token": "..." }
```

Complete the challenge:
```
POST /api/v1/auth/totp/verify-login
Body: { "totp_token": "...", "code": "123456" }
Response: { "access_token": "...", "refresh_token": "...", "user": {...} }
```

### OIDC Flow

1. Check if SSO is available:
   ```
   GET /api/v1/auth/sso-status
   Response: { "oidc_enabled": true, "providers": [...] }
   ```

2. Start the OIDC flow:
   ```
   GET /api/v1/auth/oidc/authorize
   Response: { "redirect_url": "https://idp.example.com/authorize?..." }
   ```

3. After the IdP redirects back, exchange the code:
   ```
   POST /api/v1/auth/oidc/token-exchange
   Body: { "exchange_code": "..." }
   Response: { "access_token": "...", "refresh_token": "...", "user": {...} }
   ```

## Error Format

All errors return a consistent envelope:

```json
{
  "error": "error_code",
  "message": "Human-readable description",
  "details": {}
}
```

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

## Pagination & Filtering

List endpoints support query parameters:

| Parameter | Description | Example |
|-----------|-------------|---------|
| `limit` | Max items to return (default: 50) | `?limit=100` |
| `offset` | Skip N items | `?offset=50` |
| `sort` | Sort field | `?sort=created_at` |
| `order` | Sort direction: `asc` or `desc` | `?order=desc` |

Some endpoints support additional filters documented in their sections below.

---

## Health

```
GET /healthz
```
Returns `200 OK` when the API server is ready. Not behind authentication or rate limiting.

```
GET /api/v1/version
```
Returns the application version, commit hash, and build time.

---

## Endpoint Catalog

### Auth

| Method | Path | Description |
|--------|------|-------------|
| POST | `/auth/register` | Register a new user (first user becomes admin) |
| POST | `/auth/login` | Login with username/password |
| POST | `/auth/refresh` | Refresh access token |
| POST | `/auth/logout` | Logout (invalidate tokens) |
| POST | `/auth/logout-all` | Logout all sessions |
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
| GET | `/clusters/:id/containers/:ct_id/snapshots` | List snapshots |
| POST | `/clusters/:id/containers/:ct_id/snapshots` | Create a snapshot |
| DELETE | `/clusters/:id/containers/:ct_id/snapshots/:name` | Delete a snapshot |
| POST | `/clusters/:id/containers/:ct_id/snapshots/:name/rollback` | Rollback to snapshot |
| PUT | `/clusters/:id/containers/:ct_id/config` | Update container config |
| POST | `/clusters/:id/containers/:ct_id/volumes/move` | Move a volume |

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
| DELETE | `/clusters/:id/ha/rules/:rule` | Delete HA rule |

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

### Migrations (Cross-Cluster)

| Method | Path | Description |
|--------|------|-------------|
| POST | `/migrations` | Create a cross-cluster migration |
| GET | `/migrations` | List all migrations |
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

### Alerts

| Method | Path | Description |
|--------|------|-------------|
| GET | `/alerts` | List all alerts |
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
| GET | `/audit-log` | List all audit entries |
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

### Search

| Method | Path | Description |
|--------|------|-------------|
| GET | `/search` | Global search across clusters, VMs, nodes, storage |

## WebSocket

The WebSocket server runs on the same port as the API and provides:

- **Real-time metrics** — CPU, memory, disk, network metrics streamed per cluster
- **Event notifications** — inventory changes, VM state changes, alerts, task completions
- **Console sessions** — VNC and serial terminal connections proxied to Proxmox

Connect with:
```
ws://localhost:8080/ws?token=<access_token>
```

Subscribe to channels by sending JSON messages:
```json
{"action": "subscribe", "channel": "metrics:cluster:<cluster_id>"}
{"action": "subscribe", "channel": "events:cluster:<cluster_id>"}
```
