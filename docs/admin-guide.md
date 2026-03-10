# Administration Guide

This guide covers day-to-day administration of Nexara: managing clusters, users, security, backups, alerts, and more.

## Table of Contents

- [Cluster Management](#cluster-management)
- [User Management](#user-management)
- [RBAC Setup](#rbac-setup)
- [Authentication Providers](#authentication-providers)
- [DRS Configuration](#drs-configuration)
- [Backup Management](#backup-management)
- [Alert Configuration](#alert-configuration)
- [CVE Scanning](#cve-scanning)
- [Rolling Updates](#rolling-updates)
- [Scheduled Tasks](#scheduled-tasks)
- [Reports](#reports)
- [Branding & Theming](#branding--theming)

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
- **local** — username/password stored in Nexara
- **ldap** — authenticated against LDAP/Active Directory
- **oidc** — authenticated via OIDC/SSO provider

LDAP and OIDC users are provisioned automatically (JIT) on first login.

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
1. Enter username/password (or complete SSO)
2. Enter the 6-digit TOTP code from your authenticator app
3. If you lose your device, use a recovery code instead

---

## DRS Configuration

The Distributed Resource Scheduler automatically balances VM workloads across cluster nodes.

### Enable DRS

1. Navigate to a cluster's detail page
2. Go to the **DRS** tab
3. Toggle DRS to **Enabled**
4. Configure:
   - **Mode** — `manual` (recommend migrations) or `automatic` (execute migrations)
   - **Evaluation Interval** — how often DRS evaluates balance (e.g., `300s`)
   - **CPU Threshold** — imbalance percentage to trigger migrations
   - **Memory Threshold** — imbalance percentage to trigger migrations

### DRS Rules

Create rules to control VM placement:

- **Affinity** — keep VMs together on the same node
- **Anti-affinity** — keep VMs on different nodes (e.g., HA pairs)
- **Pin** — lock a VM to a specific node

### Manual Evaluation

Click **Evaluate Now** to trigger an immediate DRS evaluation. In manual mode, this generates migration recommendations that you can review and approve.

### DRS History

The DRS History tab shows all past evaluations, including which migrations were recommended and executed.

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
   - **Metric** — what to monitor (CPU, memory, disk, etc.)
   - **Condition** — threshold and comparison (e.g., CPU > 90%)
   - **Duration** — how long the condition must persist before firing
   - **Severity** — info, warning, critical
   - **Cooldown** — minimum time between re-fires
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
