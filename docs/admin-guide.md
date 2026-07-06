# Administration Guide

This guide covers day-to-day administration of Nexara: managing clusters, users, security, backups, alerts, and more.

## Table of Contents

- [Cluster Management](#cluster-management)
- [Infrastructure Health](#infrastructure-health)
- [Topology Map](#topology-map)
- [User Management](#user-management)
- [RBAC Setup](#rbac-setup)
- [Authentication Providers](#authentication-providers)
- [DRS Configuration](#drs-configuration)
- [High Availability](#high-availability)
- [Storage Management](#storage-management)
- [VM Imports](#vm-imports)
- [Backup Management](#backup-management)
- [Alert Configuration](#alert-configuration)
- [CVE Scanning](#cve-scanning)
- [Rolling Updates](#rolling-updates)
- [Scheduled Tasks](#scheduled-tasks)
- [Tasks & Audit Log](#tasks--audit-log)
- [Reports](#reports)
- [Mobile & PWA](#mobile--pwa)
- [UI Tips](#ui-tips)
- [Branding & Theming](#branding--theming)
- [Security & Deployment Settings](#security--deployment-settings)

---

## Cluster Management

### Adding a Cluster

1. Navigate to **Clusters** from the sidebar
2. Click **Add Cluster**
3. Fill in:
   - **Name** — a display name for the cluster
   - **API URL** — the Proxmox VE API endpoint (e.g., `https://pve.example.com:8006`)
   - **API Token** — format: `user@realm!tokenid=secret-value`
4. For self-signed certificates, click **Fetch Fingerprint** to retrieve and trust the TLS fingerprint
5. Click **Save**

The collector starts syncing inventory (nodes, VMs, containers, storage) and metrics immediately.

### Editing a Cluster

Click the cluster name to open its detail page, then click **Edit** to update the name, API URL, or token.

### Removing a Cluster

From the cluster list, click the delete button. This removes the cluster from Nexara but does **not** affect the actual Proxmox cluster.

### API Token Requirements

The Proxmox API token needs sufficient privileges to read cluster state and perform actions. For full functionality, use a token with `PVEAdmin` role or equivalent. For read-only monitoring, `PVEAuditor` is sufficient.

---

## Infrastructure Health

Nexara aggregates health across everything it manages, server-side, and surfaces it as a health pill in the header. Click the pill to see every current issue with its reason.

### What It Checks

| Issue | Severity |
|-------|----------|
| Node offline / fenced | error |
| Failed disk (S.M.A.R.T.) | error |
| Storage inactive | warning |
| Storage near-full | warning, error at ≥ 95% |
| Node root filesystem near-full | warning, error at ≥ 95% |
| Failed tasks | warning |
| HA resource errors | error |
| Quorum lost | error |
| Guest I/O errors | error |
| Replication failures | error |
| Ceph health checks | mirrors Ceph's own severity |

### Dismissing and Muting

- **Dismiss** hides one specific current issue. It stays hidden until the underlying condition resolves; if the same problem comes back later, it reappears.
- **Mute** suppresses an entire issue type everywhere until you restore it from the same menu.

Both are stored in your browser (localStorage), not server-wide — other admins still see the issues.

---

## Topology Map

Navigate to **Topology** from the sidebar for an interactive map of your infrastructure — clusters, nodes, guests, and storage rendered as a live graph.

- **Filters** — toggle **VMs/CTs** and **Storage**; switch **Layout** between Top-Down and Left-Right
- **Health coloring** — elements are colored by status: healthy, degraded, or offline
- **Click-through** — click any element to jump to its detail page
- Pan and zoom with the mouse or trackpad

---

## User Management

Navigate to **Admin > Users** to manage user accounts.

### Creating Users

Users self-register via the registration page. The first user automatically receives the Admin role. Subsequent users get the Viewer role by default.

### Managing Users

From the Users page, administrators can:

- **View** all registered users with their roles, auth source, and 2FA status
- **Edit** user details and role assignments
- **Delete** user accounts
- **Reset 2FA** — remove a user's TOTP enrollment if they lose access to their authenticator

### Auth Sources

Each user has an auth source:
- **local** — email + password stored in Nexara
- **ldap** — authenticated against LDAP/Active Directory
- **oidc** — authenticated via OIDC/SSO provider

LDAP and OIDC users are provisioned automatically (JIT) on first login.

### Recovering from a Lost Admin Account

If every administrator has lost access (forgotten password, lost 2FA
device with no recovery codes), drop the login-capable rows directly
in the database to re-trigger the first-run setup flow:

```bash
docker exec -it nexara-db psql -U nexara nexara

DELETE FROM users WHERE id != '00000000-0000-0000-0000-000000000001';
```

The `WHERE` clause keeps the seeded system actor (UUID
`00000000-…-001`) that audit-log entries and DRS / scheduler tasks
attribute to — removing that row would break those references.

After the delete, `/api/v1/auth/setup-status` returns `needs_setup:
true`, the registration page reappears, and the next sign-up becomes
the new admin. LDAP and OIDC configurations remain intact, so users
authenticated via those providers will be re-provisioned on their
next login.

---

## RBAC Setup

Nexara uses role-based access control with granular permissions. Navigate to **Admin > Roles**.

### Built-in Roles

| Role | Description |
|------|-------------|
| **Admin** | Full access to all features and settings |
| **Operator** | Can manage VMs, backups, and operational tasks; cannot manage users or RBAC |
| **Viewer** | Read-only access to dashboards, inventory, and metrics |

### Custom Roles

1. Click **Create Role**
2. Enter a role name and description
3. Select permissions from the available list
4. Click **Save**

### Permission Format

Permissions follow the pattern `action:resource`. Examples:

| Permission | Description |
|------------|-------------|
| `view:cluster` | View cluster information |
| `manage:cluster` | Create, edit, delete clusters |
| `manage:vm` | Start, stop, migrate VMs |
| `view:audit_log` | View audit log entries |
| `manage:alert` | Create and manage alert rules |
| `manage:user` | Manage user accounts |
| `manage:rbac` | Manage roles and permissions |

### Assigning Roles

1. Go to **Admin > Users**
2. Click a user to edit
3. Assign one or more roles
4. Permissions are the union of all assigned roles

---

## Authentication Providers

### LDAP / Active Directory

Navigate to **Admin > LDAP** to configure LDAP/AD authentication.

#### Setup

1. Click **Add LDAP Configuration**
2. Configure connection:
   - **Server URL** — `ldap://ad.example.com:389` or `ldaps://ad.example.com:636`
   - **Bind DN** — service account DN (e.g., `cn=nexara,ou=services,dc=example,dc=com`)
   - **Bind Password** — service account password (encrypted at rest)
   - **Base DN** — search base (e.g., `dc=example,dc=com`)
3. Configure user search:
   - **User Filter** — LDAP filter template (e.g., `(sAMAccountName={{username}})`)
   - **Username Attribute** — attribute for username (e.g., `sAMAccountName`)
   - **Email Attribute** — attribute for email (e.g., `mail`)
4. Configure group mapping (optional):
   - **Group Base DN** — where to search for groups
   - **Group Filter** — filter for group membership
   - **Group-to-Role Mapping** — map LDAP groups to Nexara roles
5. Click **Test Connection** to verify
6. Click **Save**

#### How LDAP Login Works

1. User enters username/password on the login page
2. Nexara tries LDAP authentication first (if configured)
3. On success, a local user is created (JIT provisioning) with `auth_source=ldap`
4. LDAP group memberships are mapped to Nexara roles
5. Subsequent logins re-sync group memberships

### OIDC / SSO

Navigate to **Admin > OIDC** to configure single sign-on.

#### Setup

1. Click **Add OIDC Configuration**
2. Configure provider:
   - **Provider Name** — display name (e.g., "Google", "Okta")
   - **Issuer URL** — OIDC issuer (e.g., `https://accounts.google.com`)
   - **Client ID** — from your identity provider
   - **Client Secret** — from your identity provider (encrypted at rest)
   - **Redirect URI** — `https://your-nexara-domain/oidc-callback`
3. Configure claims mapping:
   - **Username Claim** — which claim to use as username (default: `preferred_username`)
   - **Email Claim** — which claim to use as email (default: `email`)
   - **Groups Claim** — claim containing group memberships (optional)
4. Configure group-to-role mapping (optional)
5. Click **Test Connection** to verify discovery
6. Click **Save**

#### How OIDC Login Works

1. User clicks the **SSO** button on the login page
2. Browser redirects to the identity provider with PKCE challenge
3. After authentication, the provider redirects back with an authorization code
4. Nexara exchanges the code for tokens, verifies the ID token (signature, nonce, audience)
5. A local user is created (JIT provisioning) with `auth_source=oidc`
6. Group claims are mapped to Nexara roles

### Two-Factor Authentication (TOTP)

#### User Self-Service

1. Navigate to **Settings > Security**
2. Click **Enable 2FA**
3. Scan the QR code with an authenticator app (Google Authenticator, Authy, etc.)
4. Enter the 6-digit code to verify
5. Save the recovery codes in a secure location

#### Admin Management

- Admins can see 2FA status for all users on the **Admin > Users** page
- Admins can **reset** a user's 2FA if they lose access (the user will need to re-enroll)
- Admins cannot reset their own 2FA (security measure)

#### Login with 2FA

When 2FA is enabled, login becomes a two-step process:
1. Enter email/password (or LDAP username + password, or complete SSO)
2. Enter the 6-digit TOTP code from your authenticator app
3. If you lose your device, use a recovery code instead

---

## DRS Configuration

The Distributed Resource Scheduler automatically balances VM workloads across cluster nodes.

### Enable DRS

1. Navigate to a cluster's detail page
2. Go to the **DRS** tab
3. Choose a **Mode**:
   - **Advisory — recommend only** — DRS generates migration recommendations for you to review and apply
   - **Automatic — migrate VMs** — DRS executes the migrations itself
   - **Disabled — do nothing** — turns evaluation off
4. Configure:
   - **Resource Weights** — how heavily CPU vs. memory pressure counts toward a node's load score
   - **Imbalance Threshold** — the cluster imbalance percentage that triggers migration planning (5–100%, in 5% steps)
   - **Evaluation Interval** — how often DRS evaluates balance, in seconds
   - **Include containers in balancing** — balance CTs alongside VMs

### DRS Rules

Create rules to control VM placement:

- **Affinity** — keep VMs together on the same node
- **Anti-affinity** — keep VMs on different nodes (e.g., HA pairs)
- **Pin** — lock a VM to a specific node

### Manual Evaluation

Click **Evaluate Now** to trigger an immediate DRS evaluation. In Advisory mode, this generates migration recommendations that you can review and approve.

### DRS History

The **History** tab within DRS shows all past evaluations, including which migrations were recommended and executed.

### Native CRS Coexistence (Proxmox VE 9.2+)

Proxmox VE 9.2 added a native dynamic load balancer to its Cluster Resource Scheduler (CRS). When a cluster has it enabled (`crs: ha=dynamic, ha-auto-rebalance=1` in the datacenter options), Nexara detects it and **disables its own automatic migrations** so the two balancers don't fight — Advisory mode still works as a read-only second opinion, and a banner on the DRS tab explains the state. You can manage the native CRS dynamic options (threshold, hold duration, margin, method) from **Cluster → Datacenter Options → CRS**.

---

## High Availability

Nexara manages Proxmox HA from each cluster's **HA** tab.

### HA Rules

Create HA rules from the cluster's **HA** tab. Rule names must start with a letter, be at least 2 characters, and contain only letters, numbers, hyphens, and underscores — the form validates this up front instead of letting Proxmox reject the rule later.

While picking resources for a rule, the search also lists guests that are not yet HA-managed. Tick **Add to HA management** and Nexara enrolls them as part of creating the rule — no need to leave the form and add the resource separately.

### Node Maintenance

To service a node without fighting HA (the maintenance actions appear once the cluster's SSH credentials are configured):

1. Open the node's detail page
2. Click **Enter Maintenance** — HA-managed guests are migrated away, and the node shows a maintenance indicator in the tree and node views
3. When you're done, click **Exit Maintenance**

### Cluster-Wide HA Arm/Disarm (PVE 9.2+)

On Proxmox VE 9.2+, the **HA Maintenance** card on the HA tab can take HA offline cluster-wide for major maintenance:

- **Disarm HA…** — choose what happens to HA resources while disarmed:
  - **Freeze — lock services in place**
  - **Ignore — suspend HA tracking (manage manually)**
- **Re-arm HA** — restore normal HA operation when maintenance is finished

---

## Storage Management

Navigate to **Storage** from the sidebar. The pool list groups shared
storage at the top and per-node storage below.

### Pool Detail

Click a pool to open its detail page. Tabs split content by Proxmox
content type:

- **VMs/CTs** — guest-grouped view of every VM disk (`images`) and
  container volume (`rootdir`) currently consuming this pool. Selected
  by default when the pool can hold guest volumes.
- **iso** / **vztmpl** / **snippets** / **backup** — raw item lists
  per content type.
- **All** — every volume on the pool regardless of content type.

### Migrating Guests Off a Pool

The **VMs/CTs** tab shows one row per guest with name, VMID, kind, disk
count, and total bytes consumed. Two ways to move:

- **Per-guest migrate** — click the migrate icon at the end of a row
  (or right-click and pick *Migrate <N> disks*). The dialog walks every
  disk that guest owns on this pool and dispatches them sequentially to
  the chosen target storage, showing per-volume progress. Most users
  want this — they don't need to know whether the volume key is
  `scsi0` or `mp0`; Nexara resolves it from the guest config.
- **Bulk migrate** — tick the checkbox on multiple guests, then use
  *Migrate selected* in the toolbar that appears above the table.
  Same sequential runner, longer job list.

The migrate target dropdown is filtered to other active pools in the
same cluster that accept `images` or `rootdir` content. The "Delete
original after move completes" checkbox is on by default — uncheck it
if you want to keep both copies for a rollback window.

### Bulk Delete

Multi-select with the checkbox column works on every tab. Once
anything is selected, a toolbar appears with **Delete selected**.
Useful for ISO / template cleanup. Each delete is a separate Proxmox
task; failures are reported individually so a single broken volume
doesn't block the rest of the batch.

### Evacuating a Pool Entirely

The **Evacuate** button at the top of a pool detail page kicks off a
bulk move of every VM disk on the pool to a target storage —
equivalent to selecting all guests in the VMs/CTs tab and migrating,
but with a dedicated dialog tuned for "I'm decommissioning this
pool" workflows. Container volumes are not yet covered by Evacuate;
use the per-guest migrate or bulk-migrate flow for those.

---

## VM Imports

Navigate to **Imports** from the sidebar to bring existing VMs into Proxmox from ESXi/vCenter hosts, OVA/OVF appliances, or disk images. Imports run as Proxmox tasks, so they show up in the task history and audit log like any other operation.

### Registering an ESXi / vCenter Source

1. On the **VM Imports** page, click **Add ESXi source**
2. Fill in:
   - **Storage ID** — the name Proxmox will use for this source (must start with a letter; letters, digits, `-_.` only)
   - **Server** — the ESXi or vCenter address
   - **Username** / **Password**
   - Optionally tick **Skip TLS certificate verification** for self-signed ESXi hosts
3. Click **Register source**

The source is registered as a special storage on the cluster, and the VMs it exposes become selectable when importing.

### Importing a VM

1. Click **Import VM** (also available from the Ctrl+K command palette)
2. Pick where the guest comes from:
   - **From import storage** — a VM on a registered ESXi source, or an appliance on import-enabled storage
   - **Download OVA from URL** — paste a link and click **Check URL** to probe it; optionally override the filename
   - **Upload OVA** — pick a `.ova` file from your machine and click **Upload to storage**
3. Review the guest in the customize step — settings are grouped into **OS & System**, **CPU & Memory**, **Network**, and **Identity & options**
4. Start the import and follow progress in the history table

Proxmox-side defaults that trip up manual imports are handled automatically: UEFI (OVMF) guests get an EFI vars disk created, imported disks attach on SATA so the firmware can boot them, and the CPU type defaults to `x86-64-v2-AES` (required for Windows 11 guests).

### Import History

The history table shows **Name**, **Target**, **Source**, **Status** (pending / running / completed / failed / cancelled), and **Created**. Cancelling a running import asks whether to **Also delete the partially created VM**.

### Permissions

Importing is gated by a dedicated VM-import permission (`manage:vm_import`). Two operations additionally require storage-management rights (`manage:storage`) because they exercise or change storage configuration: probing a download URL, and enabling import content on an existing storage.

---

## Backup Management

### PBS Server Setup

1. Navigate to **Backup** from the sidebar
2. Click **Add PBS Server**
3. Enter:
   - **Name** — display name
   - **API URL** — PBS API endpoint (e.g., `https://pbs.example.com:8007`)
   - **API Token** — PBS API token
   - **TLS Fingerprint** — for self-signed certificates
4. Click **Save**

### Managing Datastores

After adding a PBS server, Nexara syncs its datastores. For each datastore you can:

- View **usage statistics** and storage capacity
- Browse **snapshots** with filtering by VM/CT
- **Protect/unprotect** snapshots
- **Delete** snapshots
- Trigger **garbage collection**
- **Prune** old snapshots based on retention rules
- View **datastore metrics** over time

### Backup Jobs

1. Navigate to a cluster's backup section
2. Click **Create Backup Job**
3. Configure:
   - **Schedule** — cron expression (e.g., `0 2 * * *` for daily at 2 AM)
   - **Selection** — all VMs, specific VMs, or by pool
   - **Storage** — target PBS datastore
   - **Mode** — snapshot, suspend, or stop
   - **Compression** — zstd (recommended), lzo, or gzip
4. Run immediately or wait for the schedule

### Restoring from Backup

1. Find the snapshot in the **Backup** dashboard
2. Click **Restore**
3. Select the target cluster and node
4. Choose the target storage
5. Optionally change the VMID
6. Click **Restore**

---

## Alert Configuration

Navigate to **Alerts** from the sidebar.

### Alert Rules

1. Click the **Alert Rules** tab
2. Click **Create Rule**
3. Configure:
   - **Name** — descriptive rule name
   - **Scope** — **Cluster** (any matching resource in the cluster), **Node** (one specific node), or **VM** (one specific guest). VM-scoped rules are pinned to the guest's VMID within the cluster, so they keep working across migrations and inventory re-syncs.
   - **Metric** — what to monitor (CPU, memory, disk, etc.)
   - **Condition** — threshold and comparison (e.g., CPU > 90%)
   - **Duration** — how long the condition must persist before firing; `0` fires on the first breaching sample
   - **Severity** — info, warning, critical
   - **Cooldown** — minimum time between re-fires, measured from when the previous alert resolved
4. Add notification channels and escalation chain (optional)
5. Add a custom message template (optional)
6. Click **Save**

### Notification Channels

1. Click the **Channels** tab
2. Click **Create Channel**
3. Select channel type and configure:

| Type | Configuration |
|------|--------------|
| **Email (SMTP)** | Host, port, username, password, from/to addresses |
| **Slack** | Webhook URL, channel |
| **Discord** | Webhook URL |
| **Microsoft Teams** | Webhook URL |
| **Telegram** | Bot token, chat ID |
| **Webhook** | URL, method (POST/PUT/PATCH), custom headers |
| **PagerDuty** | Integration key, severity mapping |

4. Click **Test** to send a test notification
5. Click **Save**

### Escalation Chains

When creating an alert rule, you can define escalation steps:

1. **Step 1** — notify channels A and B immediately
2. **Step 2** — if not acknowledged within 15 minutes, notify channel C
3. **Step 3** — if still unresolved after 1 hour, notify channel D

### Maintenance Windows

1. Navigate to a cluster's detail page
2. Go to **Maintenance Windows**
3. Create a window with:
   - **Name** — description of the maintenance
   - **Start/End** — time range
   - **Recurring** — optional repeat schedule
4. During a maintenance window, alerts for that cluster are suppressed

### Managing Alerts

From the **Alert History** tab:
- **Acknowledge** — mark an alert as seen (stops escalation)
- **Resolve** — mark an alert as resolved
- Filter by severity, state, cluster, and time range

---

## CVE Scanning

Navigate to **Security** from the sidebar.

### How It Works

1. Nexara queries each Proxmox node's `apt update` data to find installed packages
2. Package versions are checked against the Debian Security Tracker for known CVEs
3. Results are aggregated into a security posture score per cluster

### Manual Scan

1. Select a cluster from the filter
2. Click **Scan Now**
3. Results appear when the scan completes (typically 30-60 seconds)

### Automated Scanning

The scheduler runs CVE scans automatically every 6 hours. You can configure the schedule per cluster:

1. Click **Scan Schedule**
2. Set the interval or disable automatic scanning
3. Click **Save**

### Security Posture

The posture card shows:
- **Score** — overall security rating (0-100)
- **Critical/High/Medium/Low** — vulnerability counts by severity
- **Trend** — score change over time

### Reviewing Vulnerabilities

Expand a scan to see all detected vulnerabilities with:
- CVE ID and description
- Affected package and installed version
- Fixed version (if available)
- Severity rating

---

## Rolling Updates

Navigate to **Security > Rolling Updates** tab.

### Prerequisites

1. **SSH Credentials** — required for automated upgrades
   - Go to the cluster's SSH credentials section
   - Enter the SSH username, private key, and port
   - Click **Test Connection** to verify access to all nodes

### Creating an Update Job

1. Click **Create Rolling Update**
2. Configure:
   - **Cluster** — target cluster
   - **Nodes** — select which nodes to update (or all)
   - **Parallelism** — how many nodes to update simultaneously (default: 1)
   - **Upgrade Mode**:
     - **Manual** — pauses at each node for you to run `apt dist-upgrade` via Proxmox console
     - **Automated** — runs `apt dist-upgrade -y` via SSH
   - **HA Policy** — `strict` (abort on HA constraint violations) or `warn` (continue with warnings)
3. Click **Create**

### Update Pipeline

Each node goes through these steps:

1. **Draining** — live-migrates VMs off the node
2. **Upgrading** — applies package updates (manual or automated)
3. **Rebooting** — reboots the node if kernel updates were applied
4. **Health Check** — waits for the node to come back online and healthy
5. **Restoring** — migrates VMs back to the node

> **Native CRS:** if the cluster runs Proxmox VE 9.2's native CRS dynamic balancer with auto-rebalance enabled, Nexara pauses `ha-auto-rebalance` for the duration of the job — so the balancer can't move guests back onto a node being drained — and restores it when the job finishes or fails.

### Managing Jobs

- **Start** — begin the rolling update
- **Pause** — pause after the current node completes
- **Resume** — continue a paused job
- **Cancel** — abort the job (nodes already updated stay updated)
- **Skip Node** — skip a node that's having issues
- **Confirm Upgrade** — in manual mode, confirm that you've completed the upgrade on a node

### HA-Aware Scheduling

Before starting, the pre-flight check analyzes:
- Proxmox HA groups and rules
- DRS affinity/anti-affinity rules
- Available capacity on remaining nodes

---

## Scheduled Tasks

Scheduled tasks run on cron expressions. They are managed per cluster.

### Creating a Schedule

1. Navigate to a cluster's detail page
2. Go to the **Schedules** tab
3. Click **Create Schedule**
4. Configure:
   - **Type** — snapshot, backup, or reboot
   - **Target** — specific VMs/CTs or all
   - **Cron Expression** — when to run (e.g., `0 3 * * 0` for Sundays at 3 AM)
   - **Retention** — how many snapshots to keep (for snapshot tasks)
5. Click **Save**

The scheduler evaluates schedules every 60 seconds (configurable via `SCHEDULER_TICK`).

Every execution of a scheduled snapshot or reboot is recorded in the task history and the audit log, so scheduled activity is traceable exactly like manual actions.

---

## Tasks & Audit Log

Everything that happens — user-initiated, scheduled, or automated (DRS, rolling updates) — is tracked in two places:

- **Task history** — every Proxmox task (UPID) Nexara dispatches or observes, with live status (pending → running → completed / failed). Rows expand to show the full details and error output. Tasks started outside Nexara (directly in the Proxmox UI or CLI) are synced in as well and carry a **PVE** badge so you can tell where an action originated.
- **Audit log** — who did what, when, and to which resource, with the full request details in expandable rows.

Failed tasks surface the error message from Proxmox in the UI — failures are never silently swallowed.

---

## Reports

Navigate to **Reports** from the sidebar.

### Generating Reports

1. Click **Generate Report**
2. Select report type:
   - **Cluster Summary** — overview of resources, utilization, and health
   - **VM Inventory** — complete list of VMs/CTs with configuration
   - **Capacity Planning** — resource trends and projections
   - **Security** — CVE scan results and posture scores
3. Select clusters and time range
4. Click **Generate**
5. Download as **HTML** or **CSV**

### Scheduled Reports

1. Click **Create Schedule**
2. Configure report type, scope, and cron expression
3. Reports are generated automatically and available in the **Report Runs** list

---

## Mobile & PWA

The entire UI is responsive — every feature works on phones and tablets:

- Navigation collapses into a drawer menu
- Detail pages and tables reflow into mobile-friendly layouts
- Consoles (VNC, serial, node shell) take over the full screen on mobile
- Touch targets are sized for fingers, including tree and header actions

To install Nexara as an app, open it in your phone's browser and choose **Add to Home Screen** (or **Install app**). It then launches in a standalone window without browser chrome.

> Nexara is a live dashboard and needs a connection to your Nexara server — the installed app does not work offline.

---

## UI Tips

- **Command palette** — press **Ctrl+K** (**⌘K** on macOS) anywhere. Search across clusters, nodes, VMs/CTs, and storage; jump to pages; or run actions like creating a VM or container, importing a VM, opening a console, and switching the theme.
- **What's new** — after an upgrade, a "What's new" dialog summarizes the releases you've picked up since your last visit (fed by GitHub Releases).
- **Confirmation dialogs** — destructive operations (shutdown, reboot, destroy, delete) always ask first and name the exact resource affected.
- **Click to copy** — resource names shown in confirmation dialogs copy to the clipboard on click, handy for pasting into a terminal.

---

## Branding & Theming

Navigate to **Admin > Branding**.

### Custom Branding

- **Application Title** — change the title shown in the browser tab and sidebar
- **Logo** — upload a custom logo (displayed in the sidebar)
- **Favicon** — upload a custom favicon

### Accent Colors

Navigate to **Settings > Appearance** to choose from 9 accent color presets that theme the entire UI.

### Display Preferences

- **Theme** — light, dark, or system (follows OS preference)
- **Byte Unit** — binary (GiB) or decimal (GB)
- **Date Format** — various date/time display formats
- **Refresh Interval** — how often dashboards auto-refresh

---

## Security & Deployment Settings

Two environment variables matter for any production deployment — see the [Installation Guide](installation.md#configuration-reference) for the full reference:

- **`WS_ALLOWED_ORIGINS`** — exact-match allow-list of `Origin` values accepted on WebSocket upgrades (`/ws`, `/ws/console`, `/ws/vnc`). Set it to your public origin (e.g. `https://nexara.example.com`) so a malicious page on another domain can't open a WebSocket through a logged-in user's browser. Empty or `*` accepts any origin — fine for labs, and warned about at startup.
- **`TRUSTED_PROXIES`** — when Nexara sits behind nginx/Traefik/Caddy, set this to the proxy's IP or CIDR so the per-IP rate limiters key on the real client address instead of the proxy's. Without it, all proxied traffic shares one rate-limit bucket. Pair with `PROXY_HEADER` if your proxy uses a non-standard header.

Also consider `SECURE_COOKIES=always` behind a TLS-terminating proxy, and `HSTS_MAX_AGE` for HTTPS-only deployments with a trusted certificate.
