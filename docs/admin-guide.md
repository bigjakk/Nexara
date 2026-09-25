# Administration Guide

This guide covers day-to-day administration of Nexara: managing clusters, users, security, backups, alerts, and more.

## Table of Contents

- [Cluster Management](#cluster-management)
- [Infrastructure Health](#infrastructure-health)
- [Topology Map](#topology-map)
- [User Management](#user-management)
- [RBAC Setup](#rbac-setup)
- [Proxmox Access Control](#proxmox-access-control)
- [Authentication Providers](#authentication-providers)
- [DRS Configuration](#drs-configuration)
- [High Availability](#high-availability)
- [Storage Management](#storage-management)
- [VM Imports](#vm-imports)
- [Backup Management](#backup-management)
- [Veeam Backup & Replication](#veeam-backup--replication)
- [Snapshots](#snapshots)
- [Windows Guest Tools](#windows-guest-tools)
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
3. Fill in the **Cluster Name** and the **API URL** (e.g., `https://pve.example.com:8006`), then choose how Nexara gets its credential:

   **Create a token for me** (the default tab). Supply a privileged Proxmox login once — typically `root@pam` and its password, plus a TOTP code if the account has two-factor enabled. Nexara logs in, creates a dedicated `nexara@pve` user, grants it an ACL, mints an API token and verifies it works before saving anything. It retries the verification to ride out ACL replication across the cluster.

   The password is used for that single request: it is never persisted, never logged and never written to the audit log. Only the minted token's ciphertext is stored, and the dialog's summary reports object names only — never the secret. The `pve` realm is deliberate: a PVE-realm account has no shell, no home directory and no `/etc/passwd` entry, so it cannot be used to log into a node. You can override the **Token Name** (`nexara`) if you have a naming convention; the created user id (`nexara@pve`) is overridable only through the API's `bootstrap.user_id` field, not the dialog.

   Over plain HTTP the dialog warns before accepting a password — it belongs to a privileged human account that is very likely reused elsewhere. It warns rather than hides the option, because a lab reached over a trusted LAN is a legitimate setup.

   **I have a token.** Supply the **API Token ID** (e.g. `root@pam!nexara`) and **API Token Secret** you created in the Proxmox UI.

4. Click **Connect**. Nexara probes the endpoint's certificate:
   - a CA-signed certificate is confirmed as **Trusted Certificate** and you continue
   - a self-signed certificate shows its **SHA-256 fingerprint**; verify it against the Proxmox host, then tick *I have verified this fingerprint and trust this certificate*
5. Click **Add Cluster**

If the API URL resolves to a private address, Nexara warns before connecting — confirm to proceed. That is a guard against pointing the server at something on its own network by accident, not a block.

The collector starts syncing inventory (nodes, VMs, containers, storage) and metrics immediately.

### Editing a Cluster

Click the cluster name to open its detail page, then click **Edit** to update the name, API URL, or token.

**Changing the API URL requires re-entering the token secret.** A stored credential is only ever sent to the address it was saved for — leaving the secret blank while moving the address would deliver it to whatever the new URL points at. The refusal is audited.

Changing the address also **clears the cluster's stored SSH credential and every pinned host key**, because node addresses are learned from the cluster API and those secrets were entrusted to machines the cluster is leaving. Nexara warns first, naming what will be lost; the credential cannot be recovered afterwards.

### Removing a Cluster

From the cluster list, click the delete button. This removes the cluster from Nexara but does **not** affect the actual Proxmox cluster.

### API Token Requirements

A token Nexara minted itself already holds everything it needs — onboarding grants its user the `Administrator` role on `/` — so this section only applies to tokens you create by hand. The cluster row records which route its token came from, so you can tell later which clusters Nexara onboarded itself.

For full functionality, the token needs `Administrator` on `/`: grant the role to the token's user and create the token with privilege separation off (`--privsep 0`, or **Privilege Separation** unchecked), as in [Creating a Proxmox API Token](installation.md#creating-a-proxmox-api-token). A privilege-separated token holds only what both it and its user are granted, so it would need the grant twice. For read-only monitoring, `PVEAuditor` is sufficient.

`PVEAdmin` covers most of what Nexara does, but it lacks seven privileges that `Administrator` has — `Sys.Modify`, `Sys.PowerMgmt`, `Sys.Incoming`, `Sys.AccessNetwork`, `Realm.Allocate`, `Permissions.Modify` and `Mapping.Modify` — and whatever needs one of them either fails or, where Nexara treats the step as optional, is skipped: node power actions need `Sys.PowerMgmt`, a rolling update cannot pause Proxmox VE 9.2's native CRS auto-rebalance without `Sys.Modify` on `/` and drains with it still on, and some backup jobs cannot be run on demand (see [Backup Jobs](#backup-jobs)).

**Grant the role on `/` itself**, not only on `/nodes`, `/vms` or `/storage`. Proxmox answers some cluster-wide reads only to a holder of `Sys.Audit` on `/` — HA status, the HA resource, group and rule listings, and the backup job definitions among them — and Nexara will not act without HA state:

- **DRS** skips the evaluation rather than plan migrations it cannot check against HA maintenance, node pins and anti-affinity rules, and tries again at its next interval.
- **Node evacuation** is refused whenever Nexara has to choose the target nodes. Naming an explicit target still works, because Nexara does not score that choice.
- **A rolling update that drains guests** fails before it migrates any of them, because it cannot tell which HA rules would fight the drain or where HA lets each guest go. An in-place update drains nothing and is unaffected.

A grant that stops at `/nodes` therefore blocks every DRS evaluation, every evacuation that leaves the targets to Nexara, and every rolling update that drains guests.

Once a cluster is connected, the cluster's **Access Control** tab manages Proxmox's own users, tokens, groups, roles and ACLs from Nexara — see [Proxmox Access Control](#proxmox-access-control).

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

The **first** user self-registers via the registration page and is automatically promoted to Admin — that is the install bootstrap. After that, self-registration is closed: only an administrator can create accounts.

To add a user, go to **Admin > Users** and click **Create User**, then supply an email, a password, and an optional display name. New accounts receive the **Viewer** role globally; assign anything more from the user's role dialog.

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
| `view:cluster` | View clusters |
| `manage:cluster` | Create, update clusters |
| `delete:cluster` | Delete clusters |
| `manage:vm` | Create, update VM configuration |
| `execute:vm` | Start, stop, migrate, snapshot VMs |
| `manage:migration` | Create, execute, and cancel Nexara migration jobs (the planner) |
| `console:vm` | Open VM serial and VNC consoles |
| `console:container` | Open container attach and VNC consoles |
| `console:node` | Open node shell consoles (root shell on the Proxmox host) |
| `view:audit` | View audit log |
| `manage:alert` | Create and manage alert rules |
| `manage:user` | Create, update, delete users |
| `manage:role` | Create, update, delete roles and assignments |

Note that acting on a guest and configuring it are separate: starting, stopping,
migrating and snapshotting a VM is `execute:vm`, while editing its hardware or
creating it is `manage:vm`. `manage:migration` governs Nexara's own migration
jobs, not the per-guest migrate button.

The complete catalog is whatever **Admin > Roles** lists — it is seeded from the database, so the role editor is always the authoritative list.

> **Console access is not implied by `view:*`.** Opening a shell or console
> requires the dedicated `console:*` permissions, which the built-in Admin
> and Operator roles hold. The built-in Viewer role deliberately does not —
> a read-only account cannot open a root shell on a node or a guest console.
> To build a "viewer plus consoles" role, grant `console:vm` /
> `console:container` (and `console:node` only if node shells are intended)
> alongside the view permissions.

### Assigning Roles

1. Go to **Admin > Users**
2. Click a user to edit
3. Assign one or more roles
4. Permissions are the union of all assigned roles

Every assignment carries a **scope**: `global` (all clusters) or `cluster` (one cluster). A global grant covers everything; a cluster-scoped grant covers only that cluster, and list views follow it — alert rules, migration jobs, report schedules and runs, task history and audit entries are all filtered to the clusters the caller can reach, so a scoped user sees a smaller, complete list rather than a full one with holes in it.

The role dialog assigns global scope only. To scope a grant to one cluster, use the API:

```bash
curl -X POST https://nexara.example.com/api/v1/rbac/users/$USER_ID/roles \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"role_id":"'$ROLE_ID'","scope_type":"cluster","scope_id":"'$CLUSTER_ID'"}'
```

The assignment's scope shows as a badge next to the role name in the user's role dialog.

---

## Proxmox Access Control

A cluster's **Access Control** tab manages its **own** Proxmox users, API tokens,
groups, roles and ACL entries, so you do not have to leave for the Proxmox UI.

This is not Nexara's RBAC. The two are separate systems:

| | Nexara RBAC | Proxmox access control |
|--|--|--|
| Governs | who can do what **in Nexara** | who can do what **on the cluster** |
| Subjects | Nexara users (local, LDAP, OIDC) | PVE users and API tokens |
| Where | Nexara's database | `/etc/pve/user.cfg` on the cluster |
| See | [RBAC Setup](#rbac-setup) | this section |

### Permissions

Reads need `view:access` (Admin, Operator and Viewer by default). Writes need
`manage:access`, granted to **Admin only**.

That asymmetry is deliberate: `manage:access` can mint a PVE token holding the
**Administrator** role, which is full control of the cluster *and* a way out of
Nexara's own RBAC — the holder could take the token and drive Proxmox directly,
with no Nexara permission check in the path. Granting it to Operator by default
would make Operator silently equivalent to Admin.

What Nexara can actually do is bounded by what the cluster's stored credential is
permitted to do. **Access Control → Permissions** reports exactly that; check it first
when an action fails with a Proxmox-side error rather than a Nexara 403.

### What You Can Manage

- **Users** — `name@realm`. Deleting a user takes **every API token it owns** with it; that is Proxmox behaviour, not Nexara's
- **API tokens** — create, edit, regenerate and revoke
- **Groups** — so an ACL can name a set of users rather than one
- **Roles** — built-in roles are read-only; custom ones can be created and edited
- **ACL entries** — grant a role to a user, token or group on a path (`/vms/101`, `/storage/local`, `/` for the whole cluster), with optional propagation
- **Realms** — **read-only.** Creating or editing one needs the `Realm.Allocate` privilege, which no bundled Proxmox role except `Administrator` carries

### Token Secrets Are Shown Once

Minting a token returns its secret exactly once. Proxmox has no read-back
endpoint, so there is nothing for Nexara to re-fetch. The secret is **never**
written to the audit log and **never** persisted — note that `view:audit` is held
by every built-in Viewer, which makes an audit row exactly the wrong place for a
credential.

If you lose a secret, regenerate it rather than looking for it anywhere.

### Nexara Will Not Quietly Cut Its Own Connection

Deleting or regenerating the token Nexara authenticates with — or deleting the
user that owns it, which takes its tokens along — returns **409** with an
explanation rather than proceeding.

It warns rather than blocking outright: an operator rotating credentials by hand
has a legitimate reason to do exactly this, and a hard block would just send them
to the Proxmox UI to do it less safely. Append `?force=true` (the UI puts this
behind a type-the-name confirmation) to proceed; the cluster will then show as
unreachable until you update its credentials.

The guard is narrow on purpose — it fires only for the specific credential this
cluster authenticates with, and only for changes that could actually break it.
Cosmetic edits (comment, email, SSH keys) are not guarded, because making every
edit to `nexara@pve` demand a force flag would train operators to pass it
reflexively.

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
2. Nexara looks the address up locally first:
   - **no local user** — and LDAP is enabled: the credentials go to LDAP, and a successful bind provisions the account (JIT)
   - **local user with `auth_source=ldap`** — the credentials go to LDAP
   - **local user with `auth_source=local`** — the password is checked against the stored hash and LDAP is never contacted, so a local account shadows a directory account with the same address

   The password is only ever sent to the directory on the first two paths — a local account's password never leaves Nexara.
3. If no local account existed, one is JIT-provisioned with `auth_source=ldap`
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

> This dialog moves disks as they are: target storage and whether the
> source is deleted, nothing else. To convert a disk's format on the way
> across, or to throttle the copy, start the move from the guest instead —
> the Hardware tab's per-disk **Move** dialog and the guest **Migrate**
> dialog both add **Format** (*QEMU image format (qcow2)*, *Raw disk image
> (raw)*, *VMware image format (vmdk)*, or *Storage default*) and
> **Bandwidth Limit (KiB/s)**, blank meaning unlimited. Format is offered
> only where Proxmox accepts a choice — a QEMU guest landing on file-based
> storage (directory, NFS, CIFS, GlusterFS). Block-backed targets read *Set
> by target storage*, because they store images as raw only, and containers
> never get a format choice.

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
   - **Server Name** — display name
   - **API URL** — PBS API endpoint (e.g., `https://pbs.example.com:8007`)
   - **API Token ID** — e.g. `root@pam!nexara`
   - **API Token Secret**
   - **Associated Cluster** (optional) — links the PBS server to a PVE cluster for backup coverage reporting
4. Click **Connect**, accept the certificate the same way as for a cluster, and confirm

### Managing Datastores

After adding a PBS server, Nexara syncs its datastores. For each datastore you can:

- View **usage statistics** and storage capacity
- Browse **snapshots** with filtering by VM/CT
- **Protect/unprotect** snapshots
- **Delete** snapshots
- Trigger **garbage collection**
- **Prune** old snapshots based on retention rules
- View **datastore metrics** over time

### Sync, Verify and Coverage

Beyond datastore management, the Backup dashboard carries four more tabs:

- **Replication** — the PBS server's sync jobs (pulling datastores from a remote PBS). List them, and run one on demand
- **Verification** — the PBS server's verify jobs, which re-check stored chunks for corruption. Same list-and-run treatment
- **Tasks** — the PBS server's own task log, so a stuck garbage collection or verify is visible without leaving Nexara
- **Coverage** — the one that matters at review time. Four cards count **Total VMs**, **Protected (<24h)**, **Stale (>24h)** and **No Backup**, over a searchable per-guest table showing how long ago each guest was last backed up. A guest that has never been backed up reads "Never"

  Coverage spans **both providers**: freshness is taken from the newest restore point either PBS or Veeam holds, so a guest protected only by Veeam is no longer reported as unprotected. Each row also carries an **eligibility** verdict — Veeam's own worker appliances and the VBR server are not backup targets, and counting them as unprotected is how a coverage view teaches operators to ignore it. LXC containers are reported as backup targets but not Veeam-capable, since Veeam cannot back them up and PBS can.

  A viewer holding `view:backup` but not `view:veeam` sees exactly the report that existed before Veeam support: no restore-point data and no eligibility refinement, rather than a partial answer

### Backup Jobs

1. Navigate to **Backup** from the sidebar and select a PBS server
2. Open the **Schedules** tab (pick the cluster if you have more than one) and click **Add Schedule**
3. Configure:
   - **Schedule** — pick **Hourly**, **Daily**, **Weekly** or **Monthly** and the time; the builder writes the Proxmox calendar event for you and shows it in plain English. Switch to **Custom** to type a calendar event directly (e.g. `mon..fri 02:00`). Note that these are systemd calendar events, *not* cron — `0 2 * * *` is not valid here
   - **Storage** — the PVE storage ID the backup writes to (`local`, or a PBS-backed storage such as `pbs-store`)
   - **Node** — restrict the job to one node, or leave it on all nodes
   - **Guests** — how the job picks what to back up:
     - **All guests** — everything on the cluster (or on the selected node)
     - **Selected guests** — only the guests you pick
     - **All except selected** — everything *except* the guests you pick
     - **Resource pool** — every guest in one PVE pool
   - **Mode** — snapshot, suspend, or stop
   - **Compression** — zstd (recommended), lzo, gzip, or none
   - **Comment** and **Enabled**
4. Save. Use the **Run Now** (▶) button on the job's row to fire it outside its schedule; the **Next Run** column shows when it would fire on its own. Run Now asks first, as Proxmox's own **Run now** does, then starts the job with its own settings on the node it names — or, when it names none, on every online node — and each backup task it starts appears in the task list. A node that is offline or refuses is reported beside the tasks that did start, and a node whose answer does not confirm a start is reported separately: a backup may be running there, so check the task list before running the job again. A job pinned to a node that is offline is refused outright. Each run writes an audit row per backup task it starts and, whenever it asked Proxmox anything, one more recording each node's outcome.

> A job carries exactly **one** selection. Switching an existing job from, say, a VMID list to a pool clears the old selection rather than layering the two — that is deliberate, and it matches what vzdump accepts.

Run Now uses the cluster's API token, so the token needs `Sys.Audit` on `/` to read the job, and then what Proxmox's backup call checks: `VM.Backup` on the guests and `Datastore.AllocateSpace` on the target storage. On an **All guests** or **All except selected** job, Proxmox silently leaves out any guest the token lacks `VM.Backup` on, so Run Now can back up fewer guests than the job's own schedule, which runs as `root@pam`; on any other job, the backup task fails instead. A job carrying settings Nexara's form does not offer — typically one created in Proxmox — can need more: fleecing needs `Datastore.AllocateSpace` on the fleecing storage; retention (`prune-backups`, or `maxfiles` on Proxmox VE 8) needs `Datastore.Allocate` on the target storage; a bandwidth limit (`bwlimit`), I/O priority (`ionice`) or `performance` settings need `Sys.Modify` on `/`; and `stop` (end any backup already running on the node first) needs `Sys.Modify` on that node. `PVEAdmin` has no `Sys.Modify`. Proxmox reserves `tmpdir`, `dumpdir` and `script` for `root@pam` itself, and no API token counts as `root@pam`, not even one of its own, so Proxmox refuses Run Now for a job that sets any of them; its schedule still runs it.

The jobs table shows the schedule in plain English with the raw calendar event underneath, and the expanded row spells out the guest selection. Creating or editing a job writes an audit row naming the schedule, storage, node, selection and mode.

### Restoring from Backup

1. Find the snapshot in the **Backup** dashboard
2. Click **Restore**
3. Select the target cluster and node
4. Choose the target storage
5. Optionally change the VMID
6. Click **Restore**

---

## Veeam Backup & Replication

Veeam is a **second backup provider alongside PBS**. Nexara reads a Veeam
Backup & Replication server's inventory, correlates its backup objects to
Proxmox guests, folds the result into backup coverage, and can start and stop
jobs.

**Requirements:** VBR **13.1 or later**, Enterprise Plus, reachable on its REST
API port (9419 by default).

### Connecting a Server

1. Navigate to **Backup** from the sidebar and open the **Veeam** tab
2. Click **Add Veeam Server**
3. Enter:
   - **Server Name** — display name
   - **REST API URL** — `https://host:9419`. This is the **REST API root, not the console URL** — getting it wrong is the most common first-time failure
   - **Proxmox Username** — a VBR admin account, often `DOMAIN\user`. The backslash is significant
   - **Password** — encrypted at rest with AES-256-GCM, like every other stored credential
4. Click **Continue** to fetch the certificate, accept the fingerprint the same way as for a cluster, then click **Add Server**

Nexara performs a real logon before saving, so **a failed connection is a failed create**. The product version, API revision and licence edition it negotiates are recorded on the server row and shown in the UI.

The **Test** action on an already-registered server re-runs that logon and reports the same figures, but deliberately **persists nothing** — diagnosing a broken server should not rewrite its row. It needs `manage:veeam`, not just `view`, because it makes an outbound authenticated connection with stored admin credentials.

Registration schedules the first inventory sync rather than blocking on one. Until it finishes the server shows as **syncing** rather than reporting an empty inventory as though it were a finished one.

Changing a server's base URL later requires re-entering the password, for the same reason it does on a cluster.

### Mapping Platforms to Clusters

**Do this first — nothing appears on the cluster pages until you do.**

One Veeam server can protect several Proxmox clusters. Everything Veeam returns is tagged with a `platformId` identifying one Proxmox connection, and that is the *only* cluster discriminator its REST API exposes. Nexara asks you to confirm the mapping once.

Open the server's **Clusters** tab — it carries an amber count of platforms still unmapped. Each discovered platform is listed with a label taken from the licence workload hostname, usually the Proxmox cluster name. Treat that as a hint, not proof, and pick the actual cluster.

Until a platform is mapped, its rows are not attributable to any cluster, so they are visible only to a holder of **global** `view:veeam`, and coverage reports that cluster's guests using PBS data alone. That is deliberate: guessing the mapping from a hostname string would show one tenant's backup inventory to an operator scoped to a different cluster.

Removing a cluster from Nexara unmaps its platform rather than deleting the collected Veeam history — re-add the cluster and re-confirm.

### Coverage, Orphans and Manual Pins

Backup objects are correlated to guests on the **SMBIOS UUID**, which Veeam happens to use as its `objectId`. That makes the match deterministic rather than a name guess — and a name match would silently report a rebuilt guest as protected by a backup of the machine it replaced.

- **Orphans** (the server's **Orphaned** tab) are backup objects whose platform *is* mapped but which match no guest on it: a deleted VM, a template whose name was reused under a new UUID, a host rebuilt in place. They consume repository space for machines that no longer exist in the form that was backed up, and the Veeam console does not call them out.
- **Manual pins** are the escape hatch for what automatic resolution cannot know — a guest whose SMBIOS UUID changed, or an orphan you recognise. Pin the object to a guest and no sync overwrites it.

  A pin records the guest's SMBIOS UUID as it stood when you made it. Proxmox reuses a VMID once its guest is destroyed, so without that check a pin to VMID 105 would silently transfer to whatever new machine inherits 105. A collector churn or a rename leaves the pin alone; a genuine identity change invalidates it and the object returns to automatic resolution.

### Running Jobs

Job control needs **`execute:veeam`**, which is separate from `manage:veeam`. The built-in **Admin and Operator** roles both hold it, and on upgrade it was mirrored onto every custom role that already held `manage:backup`. Add it to any other custom role that should be able to run backups.

The server's tabs are **Jobs**, **Runs**, **Repositories**, **Clusters** and **Orphaned**.

From **Jobs** you can **start** and **stop** a run, and **enable** or **disable** a job's schedule. **Runs** lists the sessions; open one to see its per-guest **tasks**, which is what names *which* guest in a job failed rather than showing the job as a single red row. Session logs are read live from Veeam on demand rather than stored, so an empty log is the normal result for a run that was killed.

Job control has its own rate limit (30/min per IP), deliberately separate from and looser than the 10/min budget shared by add/edit/test — an afternoon of ordinary job control should not lock you out of registering a server.

A job Nexara itself stopped is excluded from the `veeam_job_failed` alert metric; stopping a run on purpose should not page anyone.

### Veeam's Own Guests, and DRS

A Veeam deployment puts machines on the cluster it protects — worker appliances it powers on for a job and off again afterwards, and often the VBR server itself. Nexara identifies them and shows them alongside the mapping on the **Clusters** tab, so coverage does not count them as unprotected guests. The coverage report reports them as *excluded (Veeam infrastructure)* rather than unprotected.

**DRS keeps off them by default** — the cluster's DRS settings carry a **Never migrate Veeam's own guests** checkbox (the `exclude_veeam_workers` field in the API), on by default including for existing installs. DRS treating a worker as ordinary load migrates it mid-backup and the job running on it fails. They are still *scored* — their load is real — but never selected for migration.

Note the limit: this pins the guests Veeam **owns**. It does not stop DRS moving a *protected* guest away from the node its worker happens to be on, which silently downgrades that guest's transport from hot-add to network mode — no error, just a slower backup. Covering that would mean pinning half the estate to wherever Veeam last placed a worker, and Veeam re-places them per job run.

### Veeam Alerts

Four metrics are backed by the Veeam inventory rather than the metric tables, and are configured like any other alert rule:

| Metric | Unit | Scopes | Meaning |
|--------|------|--------|---------|
| `veeam_rpo_hours` | hours | cluster, VM | Hours since the newest restore point. At cluster scope, reports the **worst** guest |
| `veeam_malware_status` | 0–3 | cluster, VM | Newest restore point's verdict: Clean 0, Informative 1, Suspicious 2, Infected 3 |
| `veeam_repo_used_percent` | % | **global only** | The fullest repository |
| `veeam_job_failed` | count | **cluster only** | Jobs whose latest run failed, excluding runs Nexara stopped |

`veeam_repo_used_percent` is global-scope only because one repository holds the backups of every cluster a Veeam server protects — there is no cluster to attribute it to. A global rule raises alerts with no cluster, which existing RBAC already handles: such an alert is readable only by a holder of global `view:alert`. `veeam_job_failed` is cluster-only because a job protects many guests, so there is no single VM to blame.

### Proxmox Task Alerts

Nexara collects every Proxmox task into its task history, including the terminal status Proxmox reported. Two metrics turn that into a notification:

| Metric | Unit | Scopes | Meaning |
|--------|------|--------|---------|
| `pve_backup_failed` | count | **cluster only** | `vzdump` backups that failed inside the Duration window |
| `pve_task_failed` | count | **cluster only** | Tasks of **any** type that failed inside the Duration window |

Three things to know before creating one:

- **Duration is the window being counted**, not "how long the condition must persist" as it is for the percentage metrics. The form defaults it to 24 hours for these two. Leaving it at 300 seconds creates a rule that counts only the last five minutes — a nightly backup that failed at 02:00 has aged out long before anyone reads the alert, so the rule evaluates false every tick and can never fire. These two metrics accept up to **30 days** (2592000), where every other metric is capped at 24 hours: a weekly backup needs a window longer than a week to be seen at all.
- **A task that completed with warnings is not a failure.** Proxmox reports `WARNINGS: N` for a run that finished with non-fatal warnings, and `vzdump` emits it routinely; a backup that warned still produced a backup. Nexara uses one success rule everywhere, so this agrees with what the Tasks page shows for the same task.
- **A cancelled task is indistinguishable from a broken one.** Proxmox records an operator who stops a migration exactly as it records one that failed, so `pve_task_failed` will count both — which is why the alert text says "failed or cancelled". Prefer `pve_backup_failed` unless you want that noise: nobody cancels backups by hand.

Both are cluster-scoped because a task's node is stored by name while an alert rule targets a node by ID. The alert names the failed tasks, so the node is one click away in the Tasks page.

---

## Snapshots

Navigate to **Snapshots** from the sidebar for every guest snapshot across every cluster in one table — the page exists to find the ones somebody took "just for a minute" eight months ago.

### The Inventory

Four cards summarise the fleet: **Total Snapshots**, **Guests with Snapshots**, **Older than 7 days** (amber) and **Older than 30 days** (red). Below them, one row per snapshot with **Age**, **Name**, **Guest**, **Cluster**, **Node** and **Created**. A **RAM** badge marks snapshots that captured guest memory. Expand a row for its description, parent snapshot, VMID, last-seen time and current guest status.

Filter by cluster, guest type (VMs / containers), age bucket (older than 7 days, older than 30 days, unknown age), or free-text search across snapshot name, guest name, VMID, description, node and cluster.

### Acting on a Snapshot

- **Open guest** — jump to the guest's detail page
- **Refresh from Proxmox** — re-read that one guest's snapshots immediately instead of waiting for the next collector pass
- **Delete snapshot** — inline confirm, then the delete runs as a Proxmox task and the row clears when it succeeds. Deleting needs `delete:vm` for VMs or `delete:container` for containers; without it the button is disabled

### How the Inventory Stays Current

Proxmox has no bulk snapshot endpoint, so the collector walks one listing per guest on its own cadence — every **5 minutes** by default, tuned with `SNAPSHOT_SYNC_INTERVAL`. That pass is deliberately conservative about deleting rows: a guest on an offline node, a guest whose listing errored, or a listing that came back empty (which Proxmox never legitimately returns) leaves that guest's rows untouched, so a transient blip can never look like "all your snapshots vanished". Rows are only removed when the guest itself has left the inventory, or when a successful listing says the snapshot is gone.

> Snapshot age is also an alertable metric — see the `snapshot_age_days` metric under [Alert Configuration](#alert-configuration) — and **Snapshot Inventory** is one of the [report](#reports) types.

---

## Windows Guest Tools

Nexara tracks the **virtio-win drivers** and **QEMU guest agent** inside Windows
guests, and can stage updates to them.

### Why Updates Apply on Reboot

Installing the virtio-win tools restarts the QEMU-GA service — the very channel an install would be driven through. Running the installer directly kills its own session mid-install. On top of that, upstream's driver upgrade fails with error 1603 when the drivers are in use, and a `viostor` boot disk always is.

So Nexara does not run the installer through the guest agent. It writes a script into the guest and registers it as a scheduled task running as SYSTEM, which executes detached — at the next boot, or immediately if you ask — and leaves a result file Nexara reads back. That is both the workaround and the natural implementation of "update on reboot".

### Configuring a Cluster

**Cluster → Guest Tools**. The cluster `mode` is:

| Mode | Behaviour |
|------|-----------|
| `disabled` | **Default.** Nexara does nothing |
| `report` | Detect and report versions. Never writes into a guest |
| `staged` | Detect, and stage updates on guests that need them |

Also on the cluster config:

- **Target version** — pin the fleet to a specific virtio-win version instead of the newest catalogued
- **Snapshot before** — take a snapshot before staging an update (off by default; not every storage type supports snapshots)
- **Max concurrent** — how many guests may be staged at once (default 5)

Per guest you can set an **exclusion** (never touch this one, whatever the cluster mode says), a **target version** of its own, and a **note** — so the reason an exclusion exists outlives whoever set it.

### The Fleet View

The guest list shows each Windows guest's installed guest-tools version alongside the target, and its **staging state** — which moves `idle → staging → staged → running → succeeded` or `failed`. The QEMU guest agent's own version and whether its service is running are on the guest's **Guest Tools** card, not the fleet table.

**Detect** re-reads one guest now. **Update** stages an update; **Cancel** removes a staged task that has not fired yet. Detection is a read (`view:guest_tools`); changing policy needs `manage:guest_tools`; staging or cancelling needs `execute:guest_tools`.

> Two version spellings are unavoidably in play: upstream's directory version (`0.1.302-1`) and the installer's `DisplayVersion` as Windows reports it (`0.1.302`), which matches the ISO filename. Nexara stores what the guest said and normalises to the suffix-less form when comparing.

### virtio-win ISOs

An update needs an ISO to install from, and Nexara can keep one in Proxmox storage for you. The **Cluster → Guest Tools** tab stacks everything on one page: the virtio-win storage config at the top, then the cluster's guest-tools policy, the fleet table, and the ISO download history at the bottom.

These actions are gated on the **storage** permissions (`view:storage` / `manage:storage`), not the guest-tools ones, because downloading writes into a Proxmox storage.

> Upstream publishes **no ISO checksum** — its `CHECKSUM` file covers only the RPMs — so a downloaded ISO is not hash-verified, which is why the source should be HTTPS. Downloads are dispatched asynchronously and reconciled from their Proxmox task ID, so a download finishes correctly even if Nexara restarts.

#### When it checks

The card shows **Last checked** and **Next check**, and lets you say when the next one lands:

| Schedule | Behaviour |
|----------|-----------|
| `Every 6 hours` | **Default.** Counted from the last check, so restarting Nexara no longer resets the cycle |
| `Daily at HH:MM` | One check a day, at that time |
| `Weekly on <day> at HH:MM` | One check a week |

What is really being scheduled is not the upstream lookup — that is one small request — but the **~840 MB fetch** a lookup can dispatch, which is why the timed options exist: point them at a maintenance window.

For the timed options, set the **time zone** as well. Left on `Server time` the hour is read in the container's own zone, which is almost always UTC.

**Check now** is available while automatic downloads are on — it is the manual
trigger for that cycle, and with the cycle off there is nothing to trigger. It
runs the check immediately without waiting for the next slot: it refreshes the release list from the source and downloads only if the target ISO is actually missing. That is the difference from **Download now**, which fetches the version you have selected. Both run against the *saved* config, so save first.

Enabling the feature, changing the target storage, or changing the pinned version each schedule a check within the minute — the schedule only governs the unattended cycle.

#### Downloading from a mirror (air-gapped installs)

**Download source**, at the bottom of the tab, is where releases are discovered and ISOs fetched from. It applies to **every cluster on the install**, not just the one you are looking at, and changing it needs `manage:settings`.

Leave it empty to follow upstream. Point it at an internal mirror when neither Nexara nor the Proxmox nodes can reach `fedorapeople.org` — both have to reach it, since Nexara reads the release list and the *node* fetches the ISO.

The mirror must be a copy of the upstream tree, so that `<base>/archive-virtio/virtio-win-<version>/` holds each ISO:

```bash
wget -m -np https://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/
```

Serve the resulting directory over HTTP and set the base URL to its root. The `stable-virtio/` redirect is not needed — without one, Nexara uses the newest version in the archive index instead.

> Two confirmations you may be asked for, both once, both deliberate: a base that resolves to a **private address** (which an internal mirror always does), and a **plain-HTTP** base. HTTP means the driver media a Windows guest installs arrives with no integrity or authenticity protection, and upstream publishes no checksum to fall back on — so serve the mirror over HTTPS. Cloud metadata and other never-routable addresses are refused outright and cannot be confirmed through.

After changing the source, run **Check now** to rebuild the release list from it; the versions listed until then were discovered against the previous source.

---

## Alert Configuration

Navigate to **Alerts** from the sidebar.

### Alert Rules

1. Click the **Alert Rules** tab
2. Click **Create Rule**
3. Configure:
   - **Name** — descriptive rule name
   - **Scope** — **Cluster** (any matching resource in the cluster), **Node** (one specific node), or **VM** (one specific guest). VM-scoped rules are pinned to the guest's VMID within the cluster, so they keep working across migrations and inventory re-syncs.
   - **Metric** — one of the seven supported metrics:

     | Metric | Unit | Scopes |
     |--------|------|--------|
     | CPU Usage | % | cluster, node, VM |
     | Memory Usage | % | cluster, node, VM |
     | Disk Read | bytes/s | cluster, node, VM |
     | Disk Write | bytes/s | cluster, node, VM |
     | Network In | bytes/s | cluster, node, VM |
     | Network Out | bytes/s | cluster, node, VM |
     | Snapshot Age | **days** | cluster, VM |

     **Snapshot Age (days)** reads the guest snapshot inventory rather than the metrics history, so it behaves differently from the rest: the value is the age of the oldest dated snapshot in scope, node scope is not offered (picking the metric switches a node-scoped rule to cluster scope), and snapshots whose creation time Proxmox does not report are excluded rather than counted as ancient. With no dated snapshots in scope the condition is false, so the alert auto-resolves once the offenders are cleaned up.
   - **Condition** — threshold and comparison (e.g., CPU > 90%)
   - **Duration** — how long the condition must persist before firing; `0` fires on the first breaching sample
   - **Severity** — info, warning, critical
   - **Cooldown** — minimum time between re-fires, measured from when the previous alert resolved
4. Add an **Escalation Chain** — this is how the rule notifies; a rule with an empty chain never sends anything
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

The escalation chain is an ordered list of steps, each naming **one** notification channel and a **delay in minutes**. It is the rule's entire notification path — there is no separate "channels" field.

1. **Step 1** — dispatched the moment the alert fires. Its delay is ignored
2. **Step 2 and later** — dispatched once the alert has been firing for that step's delay, counted **from when the alert fired** (not from the previous step). Give step 2 `15` and step 3 `60` and they land 15 and 60 minutes after the fire, respectively

Escalation only advances while the alert is still firing **and** unacknowledged — acknowledging an alert stops the chain where it is. Resolution notifications go to whichever step the alert had reached.

### Maintenance Windows

Maintenance windows suppress alert evaluation for a cluster (or a single node in it) during a planned outage. They are currently **API-only** — there is no page for them yet:

```bash
curl -X POST https://nexara.example.com/api/v1/clusters/$CLUSTER_ID/maintenance-windows \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"description":"PVE 9.2 upgrade","starts_at":"2026-09-01T22:00:00Z","ends_at":"2026-09-02T04:00:00Z"}'
```

- **description** — free text shown in the window list
- **starts_at** / **ends_at** — RFC 3339 timestamps; the end must be after the start
- **node_id** (optional) — scope the window to one node instead of the whole cluster

While a cluster-wide window is active, the alert engine skips every rule in that cluster. A node-scoped window is narrower but not rule-scoped: it skips node rules bound to that node, and it skips the per-node evaluation of cluster-wide rules for that node while the rest of the cluster keeps alarming. VM-scoped rules are suppressed only by a cluster-wide window.

Windows do not repeat — create one per outage. Managing them needs `manage:maintenance_window`; listing them needs `view:maintenance_window`.

### Managing Alerts

From the **Alert History** tab:
- **Acknowledge** — mark an alert as seen (stops escalation)
- **Resolve** — mark an alert as resolved
- Filter by severity, state, cluster, and time range

### Failed Notifications (Dead-letter Queue)

A notification that cannot be delivered is not lost. Every dispatch goes through three layers — a per-channel rate limiter, three retry attempts with backoff, and finally the dead-letter queue — so a dead Slack endpoint or a flapping rule leaves a durable record instead of silence.

The **Dead-letter queue** tab appears on the Alerts page for anyone holding `view:notification_dlq`, with a badge counting open entries. Each row carries the channel, the alert it belonged to, the failure reason, and a state:

| State | Meaning |
|-------|---------|
| **Failed** | All retries exhausted |
| **Rate-limited** | Dropped by the per-channel token bucket rather than flooding the endpoint |
| **Retrying** | A replay is in flight |
| **Resolved** | A replay succeeded |
| **Dismissed** | Acknowledged and set aside without replaying |

Per row: **Retry** re-dispatches it, **Dismiss** files it away, and **Delete** removes it permanently. Managing entries needs `manage:notification_dlq`.

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

Each cluster is scanned automatically on its own interval — **every 24 hours** unless you change it. The scheduler checks every 6 hours which clusters are due, so intervals shorter than that are rounded up in practice.

1. Open the **Scan Schedule** card on the Security page
2. Toggle **Automatic scanning** on or off, and pick an interval — hourly, 6 / 12 / 24 / 48 hours, or weekly
3. Changes save immediately

### Security Posture

The posture card shows:
- **Score** — overall security rating (0-100)
- **Critical/High/Medium/Low** — vulnerability counts by severity
- **Trend** — score change over time

### How Severity Is Decided

Nexara does not take the Debian tracker's rating at face value. Each CVE gets a 0–10 **risk score** combining three signals:

- **CVSS base** — the Debian Security Tracker publishes no CVSS score, so Nexara substitutes the midpoint of the urgency band it *did* publish (critical → 9.5, high → 7.5, medium → 5.0, low → 2.5)
- **EPSS** — FIRST's empirical probability of exploitation in the next 30 days
- **KEV** — CISA's binary "actively exploited in the wild" flag, refreshed hourly

A CVE on the KEV list is floored into the **critical** bucket regardless of its CVSS, because "actively exploited right now" outranks any paper score. A top-decile EPSS (≥ 0.9) is floored into **high**. Everything else grades by severity × probability, so a CVSS-9 with no public exploit lands low rather than screaming.

The counts on the posture card are these derived buckets — which is why a CVE's severity here can differ from the Debian tracker's. The tracker rates the flaw; Nexara rates your exposure.

The **actively exploited** callout on the posture card opens a per-CVE list: each CVE ID links to its NIST NVD page, and the flame badge beside it links to CISA's KEV catalog entry with the required action and due date.

### CVE Notifications

The **Notifications** card wires scan results into the same notification channels alerts use. Tick **Enable notifications**, then choose which SSVC-style bands to notify on:

- **Act** — actively exploited (KEV) or high-likelihood (EPSS ≥ 0.5 with CVSS ≥ 7)
- **Attend** — moderate likelihood (EPSS ≥ 0.1) or critical CVSS

Tick one or more **Channels** — the same set serves whichever bands you enabled — set **Re-notify after** to bound repeats, and click **Save**. A newly discovered CVE always notifies immediately; the cooldown only gates re-notification while the same set of CVEs is still present.

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
2. Work through the wizard's three steps — **Select Nodes**, **HA Constraint Check**, **Configure Update**:
   - **Nodes** — select which nodes to update (or all)
   - **Conflict Policy** — offered on the **HA Constraint Check** step when the pre-flight finds conflicts: **Warn & Proceed** (continue despite the findings) or **Strict (fail on violation)**. With **Strict** selected you cannot move on to the configure step while the pre-flight report contains errors
   - **Parallelism (max nodes updated at once)** — default 1
   - **Reboot after update** — off means auto-detect (reboot only when kernel or critical updates require it); on means always reboot
   - **Auto-restore guests** — migrate drained guests back after the node returns healthy
   - **Automated upgrade (SSH)** — run `apt dist-upgrade` over SSH. Disabled until the cluster has SSH credentials; with it off, the job pauses at each node in the **Awaiting Upgrade** step until you click **Confirm Upgrade**
   - **Package excludes** — comma-separated globs held back from the upgrade (e.g. `pve-kernel-*, grub-*`)
   - **Notify on completion/failure** — an existing notification channel to message when the job ends
   - **Start immediately** — begin right after creation instead of leaving the job idle
3. Click **Create**

### Update Pipeline

Each node goes through these steps:

1. **Draining** — live-migrates VMs off the node
2. **Awaiting Upgrade** — manual mode only; the job holds here until you click **Confirm Upgrade** for that node
3. **Upgrading** — applies package updates (manual or automated)
4. **Rebooting** — reboots the node if kernel updates were applied
5. **Health Check** — waits for the node to come back online and healthy
6. **Restoring** — migrates VMs back to the node

> **Native CRS:** if the cluster runs Proxmox VE 9.2's native CRS dynamic balancer with auto-rebalance enabled, Nexara pauses `ha-auto-rebalance` for the duration of the job — so the balancer can't move guests back onto a node being drained — and restores it when the job finishes or fails.

### Updating a Single Node In Place

A node's own **Updates** tab lists its pending packages and offers **Update this node**. This is the same job machinery — same SSH path, same progress view — with the drain turned off: the node is upgraded where it stands and its guests keep running.

Use it when draining is not what you want:

- **A single-node cluster.** The drain migrates guests to another online node, so with no other node it fails with *no available target nodes for migration*. In-place is the only form of update job such a cluster can run.
- **A package update with no reboot.** `apt` on a PVE node does not require the guests to be elsewhere, so draining a whole node to install userland updates is work for nothing.

What it does differently from a cluster-wide rolling update:

| | Rolling update | Update this node |
| --- | --- | --- |
| Guests | Migrated off, then back | Stay running on the node |
| Other nodes | Needed as migration targets | Not involved |
| HA rules | Temporarily disabled so guests can move | Untouched |
| Pre-flight HA/capacity checks | Run | Skipped — nothing migrates |
| Reboot | Taken once the node is empty | Only if you asked for one *and* no guests are running |

DRS and native CRS are paused for the cluster in both cases, and restored when the job ends.

**Reboots are never taken under running guests.** If the upgrade needs one — any kernel update leaves `/var/run/reboot-required` behind — the node finishes as **Reboot required** instead of **Completed**, the completion notification names it, and you schedule the reboot yourself. The same applies when Nexara cannot confirm what is running on the node: it defers rather than guessing.

The cluster-wide wizard does not offer this mode; it is reached from the node page.

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

Scheduled tasks run on cron expressions and are attached to a single guest.

### Creating a Schedule

1. Open a VM or container's detail page
2. Go to the **Schedules** tab
3. Click **Add Schedule**
4. Configure:
   - **Action** — **Snapshot** or **Reboot**
   - **Cron Expression** — minute hour day month weekday (e.g. `0 2 * * *` = daily at 2 AM)
   - **Snapshot Name (optional)** (snapshot actions only) — leave it empty and each run is
     named `auto-<timestamp>`, which is what you almost always want. A name you type is used
     **verbatim on every run**, with no date substituted, so the second run collides with the
     first run's snapshot and fails.
5. Click **Create**

The table lists each schedule with its last status, next run, and last run, and a delete button.

The scheduler evaluates schedules every 60 seconds. Every execution of a scheduled snapshot or reboot is recorded in the task history and the audit log, so scheduled activity is traceable exactly like manual actions.

---

## Tasks & Audit Log

Everything that happens — user-initiated, scheduled, or automated (DRS, rolling updates) — is tracked in two places:

- **Task history** — every Proxmox task (UPID) Nexara dispatches or observes, with live status (pending → running → completed / failed). Rows expand to show the full details and error output. Tasks started outside Nexara (directly in the Proxmox UI or CLI) are synced in as well and carry a **PVE** badge so you can tell where an action originated.
- **Audit log** — who did what, when, and to which resource, with the full request details in expandable rows.

Failed tasks surface the error message from Proxmox in the UI — failures are never silently swallowed.

Both live on the **Events** page in the sidebar: the **Audit Log** tab, and a **Tasks** tab for anyone holding `view:task`. Reaching the page at all requires `view:audit`, which every built-in role has — including Viewer, so assume audit rows are readable by any signed-in user.

Filter the audit log by cluster, resource type, user, action, source, severity and time range. What you see is scoped to the clusters your roles reach.

### Exporting the Audit Log

The cluster, resource type, user, action and time-range filters carry into the export, offered as **JSON**, **CSV**, or **syslog** (RFC 5424) from the export menu — handy for a one-off handover to an auditor. The source and severity filters are applied in the browser only and do not narrow the exported file.

### Forwarding to a SIEM

For continuous delivery, expand **Syslog Forwarding** on the Audit Log tab:

- **Host** and **Port** — your collector. The port follows the protocol (514 for UDP and TCP, 6514 for TLS) until you enter one of your own; leave it blank to use the protocol's default
- **Protocol** — UDP, TCP, or TLS (with an optional *Skip TLS certificate verification* for self-signed collectors)
- **Facility** — the syslog facility to stamp on records

**Test Connection** sends a single probe record so you can confirm the collector is receiving before turning it on. Records are RFC 5424 with STRUCTURED-DATA, every field quoted and the record length bounded, and forwarding happens off the request path so a slow or dead collector never stalls an API call. Authentication events are forwarded alongside resource changes, and changes to the forwarding configuration — plus the test probes themselves — are audited. Editing this configuration requires `manage:audit`.

---

## Reports

Navigate to **Reports** from the sidebar. The page opens on a **catalogue** of report types — each card says what the report answers, what is inside, and when it last ran — with the run history and schedules beneath it.

Every report shares one layout: a masthead with the period and the sources consulted, a strip of headline figures, a ranked list of **findings** in plain language (the thing to act on, most severe first), the evidence sections with charts and tables, and a footer stating exactly how each number was computed. Charts are inline SVG, so the same document previews in the app, prints or saves as PDF from the viewer's **Print / PDF** button, downloads as HTML, and travels as an email attachment.

### Report types

| Report | What it answers | Options |
|---|---|---|
| **Cluster digest** | One page across availability, load, backups, alerts, tasks, snapshots and patching, with every other report's findings | stale hours, snapshot age |
| **Backup compliance** | Every guest against **Proxmox Backup Server and Veeam**: coverage by state, recovery-point age, backup runs and failures in the period, datastore and repository capacity with days-to-full, malware verdicts and orphaned Veeam objects | stale hours; the runs section can be switched off |
| **Resource utilisation** | CPU, memory, disk and network per node and per day, with sustained-pressure findings | — |
| **VM resource usage** | Top consumers per resource, idle guests, every guest's period averages | top N |
| **Capacity forecast** | Where the period's trend takes CPU and memory per node; how full each storage pool is | — |
| **Snapshot inventory** | Every guest snapshot by age, and the ones most likely forgotten | snapshot age |
| **Patch status** | The latest vulnerability scan per node, known-exploited CVEs first | — |
| **Uptime summary** | Which nodes are up, for how long, and which rebooted in the period | — |

**Backup compliance and Veeam.** The report shares its verdict with the Backup page's coverage view, so the two never disagree: a guest is *current* when its newest restore point from **either** provider is younger than the stale threshold (default 24 h), *stale* otherwise, and has *no backup* when neither provider holds a point. PBS snapshots are matched through the datastores the cluster actually mounts, so a same-numbered guest on another cluster never counts. Veeam objects are matched on the guest's SMBIOS UUID; a name-only match is flagged. Veeam's own worker appliances and backup server are listed but excluded from every percentage.

Veeam data appears in a report when the person who requested it — the on-demand requester, or for a schedule the person who last **saved** it — holds `view:veeam` on that cluster; otherwise the report consults Proxmox Backup Server alone and says so. Saving a schedule makes its runs read under your grants from then on, and a schedule whose saver has been deactivated stops running until someone saves it again. A stored run is then readable by anyone holding `view:report` on the cluster: a report is a summary product, shared under that one grant by design, the same posture audit details take for Viewers. If the Veeam tables cannot be read, the run **fails** rather than quietly calling every Veeam-only guest unprotected.

### Generating a report

1. Click **Generate** on a catalogue card (or **Generate report** at the top of the page)
2. Pick the **Cluster** and the **Period** in hours (1–8760). Point-in-time reports — snapshots, patch status — use the period as context only
3. Set any **report options** the type offers; blank means the documented default
4. Click **Generate**. The finished run opens on its own page with **Print / PDF**, **HTML**, **CSV**, **Email** and **Run again**

Runs also appear on the **Report history** tab, where each one can be opened, downloaded, emailed or — with `manage:report` — deleted. Runs produced by a *schedule* are pruned automatically after 90 days; a report you generate on demand is kept until you delete it.

### Scheduled reports

1. Click **Schedule** on a catalogue card (or **New schedule**)
2. Configure:
   - **Name** — e.g. `Weekly backup review`
   - **Report** and **Cluster** — one cluster per schedule
   - **Cron schedule** — minute hour day month weekday (e.g. `0 6 * * 1` = Mondays at 06:00)
   - **Period (hours)** — how much history each run covers
   - **Attachments** — **HTML report**, or **HTML report + CSV**
   - **Report options** — the same options as an on-demand run
   - **Email delivery** (optional) — pick an email notification channel and every run is mailed to it
3. Click **Create schedule**

### Emailed reports

An emailed run carries a **digest** as the body — the headline figures, a coverage bar and the top findings, built from plain tables so every mail client renders it — with the full report attached as `<type>-<cluster>-<date>.html` (and `.csv` when the schedule asks for it). The digest is built from the same data as the attachment, so the two cannot disagree. Any finished run can also be sent on demand from its page or the history tab: pick the channel, optionally override the recipients, and choose whether to attach the CSV.

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

Whatever proxy you use, do not let it strip or redirect a trailing slash under `/api/`. Nexara refuses a write whose path ends in `/`, because that is what a browser sends when a `.` or `..` name is resolved away, and a proxy that removes the slash or redirects to the slash-less path hands the parent object that write instead. Behind Traefik that refusal covers browsers only: Traefik's default path sanitising resolves a `.` or `..` segment itself, `%2E` spellings included, and drops the slash a final one leaves, so a script's `DELETE` aimed at a pool named `..` reaches Nexara as the cluster's — which Nexara refuses too, because deleting a cluster requires `confirm` set to its name. Nexara does not rely on that sanitising, so an entry point that serves only Nexara can turn it off. No API client should send `.` or `..` as a path segment, encoded or not. The README's [Reverse Proxy](../README.md#reverse-proxy) section has the details.
