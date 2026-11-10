# Administration Guide

This guide covers day-to-day administration of Nexara: managing clusters, users, security, backups, alerts, and more.

## Table of Contents

- [Dashboard](#dashboard)
- [Cluster Management](#cluster-management)
- [Infrastructure Health](#infrastructure-health)
- [Topology Map](#topology-map)
- [Guests](#guests)
- [Consoles](#consoles)
- [Migration](#migration)
- [Folders and Favorites](#folders-and-favorites)
- [Node Management](#node-management)
- [Node Options](#node-options)
- [Memory Modules](#memory-modules)
- [Metric History and Pressure](#metric-history-and-pressure)
- [Block Jobs](#block-jobs)
- [Pools, Replication and Cluster Options](#pools-replication-and-cluster-options)
- [Your Account](#your-account)
- [User Management](#user-management)
- [RBAC Setup](#rbac-setup)
- [Proxmox Access Control](#proxmox-access-control)
- [Authentication Providers](#authentication-providers)
- [DRS Configuration](#drs-configuration)
- [High Availability](#high-availability)
- [Storage Management](#storage-management)
- [Ceph](#ceph)
- [Networking](#networking)
- [Certificates (ACME)](#certificates-acme)
- [Device Passthrough](#device-passthrough)
- [VM Imports](#vm-imports)
- [Backup Management](#backup-management)
- [Veeam Backup & Replication](#veeam-backup--replication)
- [Snapshots](#snapshots)
- [Power (UPS monitoring)](#power-ups-monitoring)
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

## Dashboard

The dashboard is the landing page. It needs no permission of its own: each widget shows what your grants on each cluster let you read.

- **Controls.** The preset menu, **Customize** (on screens at least 768 px wide), the time range (**Live**, **1h**, **6h**, **24h**, **7d**, which applies to the charts and is not saved), the refresh rate (5, 10, 30 or 60 seconds, remembered per browser) and **Add Cluster**.
- **Default layout.** **Stats Overview** and **Cluster Cards**; for each cluster the **CPU Usage**, **Memory Usage**, **Disk I/O** and **Network In** charts and **Live Metric Cards**; and **Top Consumers**, the top 10 per cluster.
- **Customize.** **Add Widget**, drag a widget by its grip, resize it, remove it with its ✕, or **Reset to Default**. **Lock** saves; leaving the page before that loses the changes. **UPS Status** appears only if you add it, and needs a global `view:ups`.
- **Presets.** Pick a preset, or **Save Current as Preset**; a name already in use is overwritten. Picking a preset and **Reset to Default** replace the layout at once, without asking.

Layouts and presets are stored per user on the server, so they follow you between browsers. A saved layout does not gain widgets for a cluster added later: add them, or reset.

---

## Cluster Management

### Adding a Cluster

1. Click **Add Cluster** in the Dashboard's header, or the **+** beside **Datacenter** in the inventory tree
2. Fill in the **Cluster Name** and the **API URL** (e.g., `https://pve.example.com:8006`) and click **Connect**. Nexara probes the endpoint's certificate:
   - a CA-signed certificate is confirmed as **Trusted Certificate** and you continue
   - a self-signed certificate shows its **SHA-256 fingerprint**; verify it against the Proxmox host, then tick *I have verified this fingerprint and trust this certificate*
3. Choose how Nexara gets its credential:

   **Create a token for me** (the default tab). Supply a privileged Proxmox login once — typically `root@pam` and its password; a **One-Time Code** field appears only if Proxmox asks for one. Nexara logs in, creates a dedicated `nexara@pve` user, grants it an ACL, mints an API token and verifies it works before saving anything. It retries the verification to ride out ACL replication across the cluster.

   The password is used for that single request: it is never persisted, never logged and never written to the audit log. Only the minted token's ciphertext is stored, and the dialog's summary reports object names only — never the secret. The `pve` realm is deliberate: a PVE-realm account has no shell, no home directory and no `/etc/passwd` entry, so it cannot be used to log into a node. You can override the **Token Name** (`nexara`) if you have a naming convention; the created user id (`nexara@pve`) is overridable only through the API's `bootstrap.user_id` field, not the dialog.

   Over plain HTTP the dialog warns before accepting a password — it belongs to a privileged human account that is very likely reused elsewhere. It warns rather than hides the option, because a lab reached over a trusted LAN is a legitimate setup.

   **I have a token.** Supply the **API Token ID** (e.g. `root@pam!nexara`) and **API Token Secret** you created in the Proxmox UI.

4. Click **Create Token & Add**, which ends on a summary of what it created, or **Add Cluster**

If the API URL resolves to a private address, Nexara warns before connecting — confirm to proceed. That is a guard against pointing the server at something on its own network by accident, not a block.

The collector starts syncing inventory (nodes, VMs, containers, storage) and metrics immediately.

### Editing a Cluster

Open the cluster's **⋮** menu in the inventory tree, or right-click it, and choose **Edit** to update the name, API URL, or token.

**Changing the API URL requires re-entering the token secret.** A stored credential is only ever sent to the address it was saved for — leaving the secret blank while moving the address would deliver it to whatever the new URL points at. The refusal is audited.

Changing the address also **clears the cluster's stored SSH credential and every pinned host key**, because node addresses are learned from the cluster API and those secrets were entrusted to machines the cluster is leaving. Nexara warns first, naming what will be lost; the credential cannot be recovered afterwards.

### Removing a Cluster

From the same **⋮** menu choose **Delete** and type the cluster's name. It is refused while a rolling update runs on the cluster. This removes the cluster from Nexara but does **not** affect the actual Proxmox cluster, unless you tick **Also delete the Proxmox user and API token Nexara created for this cluster**. Everything else scoped to the cluster goes with it and does not come back if you add it again: DRS rules and settings, alert rules scoped to it with their alerts, maintenance windows, scheduled tasks, report schedules and runs, rolling-update jobs, CVE scans and schedules, migration jobs, folders, guest tools and virtio-win settings, and favorites. The audit entries recorded against it are kept, labelled with the cluster's name followed by `(removed)`. Earlier releases deleted them with the cluster. Like entries that belong to no cluster, they are shown only to someone who can view the audit log across all clusters. Role assignments scoped to the removed cluster are removed with it, so a user who held one loses it and it leaves their role list (a global role is not affected). The removal itself is recorded among them, with how many role assignments went with the cluster. A cluster you add again is a new cluster, and its entries do not join the old ones.

Removing a cluster leaves its audit entries untouched, so it takes no longer for having many.

### The Cluster Page

Click a cluster in the inventory tree. Its tabs are **Overview**, **VMs**, **Options**, **HA**, **Pools**, **Resource Mappings**, **Access Control**, **Replication**, **Ceph**, **Networks**, **Firewall**, **Certificates**, **Metrics**, **DRS**, **Guest Tools** and **History**, each covered in its own section below.

### When a Node's Certificate Changes

Nexara pins a self-signed certificate's fingerprint, and a Proxmox upgrade can regenerate the certificate. Live calls to that address then fail the TLS handshake while the collector fails over to another node, so inventory and metrics keep flowing and the only symptom is tabs that render empty or answer 502. The health pill says **TLS certificate changed**, and the cluster's page shows a banner.

**Verify certificate** (`manage:cluster`) checks the certificate two ways: a live handshake, and what the cluster itself reports for that node, read over a connection pinned to a fingerprint already trusted. It re-pins only when they agree, and the change is audited as `tls_fingerprint_verified`. When they disagree it refuses: investigate first, since a mismatch is what an interception looks like, then use **Edit manually** → **Re-fetch Certificate**.

The check applies only when the API URL names a node on port 8006. A VIP, a load balancer or a proxy in front of the node is not compared, since the cluster reports nothing about the certificate that endpoint serves.

### API Token Requirements

A token Nexara minted itself already holds everything it needs — onboarding grants its user the `Administrator` role on `/` — so this section only applies to tokens you create by hand. The cluster row records which route its token came from, so you can tell later which clusters Nexara onboarded itself.

For full functionality, the token needs `Administrator` on `/`: grant the role to the token's user and create the token with privilege separation off (`--privsep 0`, or **Privilege Separation** unchecked), as in [Creating a Proxmox API Token](installation.md#creating-a-proxmox-api-token). A privilege-separated token holds only what both it and its user are granted, so it would need the grant twice. For read-only monitoring, `PVEAuditor` is sufficient.

`PVEAdmin` covers most of what Nexara does, but it lacks seven privileges that `Administrator` has — `Sys.Modify`, `Sys.PowerMgmt`, `Sys.Incoming`, `Sys.AccessNetwork`, `Realm.Allocate`, `Permissions.Modify` and `Mapping.Modify` — and whatever needs one of them either fails or, where Nexara treats the step as optional, is skipped: node power actions need `Sys.PowerMgmt`, saving a node's [options](#node-options) needs `Sys.Modify` on `/`, creating a USB or PCI mapping for [device passthrough](#device-passthrough) needs `Mapping.Modify`, a rolling update cannot pause Proxmox VE 9.2's native CRS auto-rebalance without `Sys.Modify` on `/` and drains with it still on, and some backup jobs cannot be run on demand (see [Backup Jobs](#backup-jobs)).

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
| Storage near-full | warning from 85%, error at ≥ 95% |
| Node root filesystem near-full | warning from 85%, error at ≥ 95% |
| Failed tasks | warning |
| HA resource errors | error |
| Quorum lost | error |
| Guest I/O errors | error |
| Replication failures | error |
| Ceph health checks | mirrors Ceph's own severity |
| [TLS certificate changed](#when-a-nodes-certificate-changes) | error |
| UPS on battery, low battery, overloaded, unreachable, or with a battery to replace — for holders of a global `view:ups` ([Power](#power-ups-monitoring)) | warning; error for low battery and overload |
| A node a UPS powers that has no `upsmon` ([Not protected](#shutdown-protection)) | warning; error while that UPS is on battery |

### Dismissing and Muting

- **Dismiss** hides one specific current issue. It stays hidden until the underlying condition resolves; if the same problem comes back later, it reappears.
- **Mute** suppresses an entire issue type everywhere until you restore it from the same menu.

Both are stored in your browser (localStorage), not server-wide — other admins still see the issues. Signing out clears dismissals; mutes stay in that browser for whoever uses it next.

---

## Topology Map

Navigate to **Topology** from the sidebar for an interactive map of your infrastructure — clusters, nodes, guests, and storage rendered as a live graph.

- **Filters** — pick a **Cluster**, toggle **VMs/CTs** (off by default) and **Storage**; switch **Layout** between Top-Down and Left-Right
- **Health coloring** — elements are colored by status: healthy, degraded, or offline
- **Click-through** — click any element to jump to its detail page
- **Power** — with a global `view:ups`, the **Power** toggle shows each UPS just above the hosts it powers (left of the clusters in the Left-Right layout), joined to them by a solid line while it is on mains and a dashed one on battery, and marks **No upsmon** on a host that would lose power without shutting down (see [Shutdown Protection](#shutdown-protection))
- Pan and zoom with the mouse or trackpad

---

## Guests

### Creating a Guest

Click **Create** in the top bar and choose **Virtual Machine** or **Container**, or right-click a cluster in the tree. They need `manage:vm` and `manage:container`. **Import VM**, in the same menu, is under [VM Imports](#vm-imports).

- The VM wizard's steps are General, OS, System, Disks, CPU, Memory, Network, Cloud-Init and Confirm, which also has **Start after creation** and **Start on boot**.
- The container wizard's are General, Template, Root Disk, CPU & Memory, Network, DNS & Options and Confirm. **Unprivileged container** is on by default.

> The wizard's Cloud-Init step saves the settings but adds no drive, and without one they never reach the guest. Add it afterwards from **Hardware** → **Add Device** → **Cloud-Init Drive**.

### The Guest Page

A VM's tabs are **Overview**, **Hardware**, **Snapshots**, **Cloud-Init**, **Guest Tools** (Windows guests), **Backups**, **Schedules** and **History**. A container's have **Resources** in place of Hardware, and no Cloud-Init or Guest Tools.

| Action | Needs | Notes |
|--------|-------|-------|
| **Start**, **Shutdown**, **Reboot**, **Stop**, **Reset** (VMs), **Suspend**, **Resume** | `execute:vm` / `execute:container` | Shutdown, Stop and Reset ask first |
| **Take Snapshot** | `execute:vm` / `execute:container` | |
| **Clone**, **Clone to Template**, **Deploy from Template** (on a template) | `manage:vm` / `manage:container` | Asks for the new VMID (the first free one from 100), name, target node and storage. **Full clone (independent copy)** is on by default |
| **Convert to Template** | `manage:vm` / `manage:container` | Stopped guests only. Irreversible, so you type the name |
| **Migrate** | `manage:migration` | See [Migration](#migration) |
| **Destroy** | `delete:vm` / `delete:container` | Disabled while the guest runs. Irreversible, so you type the name |

These are also on a guest's right-click menu, in the tree and in tables, with **Console** and **Move to folder…**. The Inventory page lists templates under **Templates**.

**Hardware** (`manage:vm`). Changes are staged until **Save Changes**, and on a running VM stay pending until it restarts. **Add Device** offers Hard Disk, CD/DVD Drive, Network Device, USB Device and PCI Device ([Device Passthrough](#device-passthrough)), Serial Port, VirtIO RNG, Cloud-Init Drive, VirtioFS Share, EFI Disk and TPM State. A disk row has move, **Resize To** (growing only) and **Remove**. A container's **Resources** tab is the equivalent for it.

**Cloud-Init** needs a Cloud-Init drive. It sets User, Password, IP Config, DNS Server, Search Domain and SSH Public Keys. An empty field is not sent, so a value cannot be cleared from this tab.

### Bulk Actions

Tick rows in a guest table (Inventory, a cluster's or node's VMs, a folder's) and the toolbar offers **Start**, **Stop**, **Shutdown**, **Reboot**, **Migrate** and **Delete**. Stop, Shutdown and Delete ask first, without a typed name, and the progress count includes failures.

---

## Consoles

A console needs `console:vm`, `console:container` or `console:node`, which the built-in Viewer does not hold.

- **Open one** with **VNC Console** on a VM's page or **Attach** on a container's, **Console** on a running guest's right-click menu, **Shell** on a node's page or a cluster's **Overview**, or **New Console** on the **Console** page in the sidebar. A guest that is stopped parks the console on **Start VM** and **Connect anyway**, and connects when the guest powers on. A running VM's page also shows a small view-only preview.
- **Node shell.** It opens at the host's login prompt, not a root shell: Proxmox logs a shell straight in only for `root@pam` itself, and an API token is never that.
- **The floating window** holds a tab per console, with **New Console**, minimize, maximize and **Open in new tab**. **Close** hides it and disconnects; the tabs remain.
- **In its own browser tab.** Set **Open consoles in** to **New browser tab** under [Appearance](#appearance), or Ctrl/⌘-click any console button. The tab has **Reconnect** and **Fullscreen**, and a link to a console that Nexara did not open itself asks **Open this console?** first.
- **The VNC toolbar** has **Power**; **Media** on a VM (**Mount ISO...** and **Eject CD-ROM**, `execute:vm`, which creates `ide2` if the VM has no CD-ROM); **Keys** (Ctrl+Alt+Del, Ctrl+Alt+F1–F12, Alt+Tab, Alt+F4, Print Screen, SysRq); paste, typed on a US layout, which reports any character it cannot type; a PNG screenshot; and **Scale** and **Resize**.
- **A console ends with its sign-in.** Within about 5 seconds of the session or API key that opened it ending, the server closes the console and it reads **Session ended**.

A VM's button opens VNC, and a container's **Attach** is its VNC terminal. The `vm_serial` and `ct_attach` console types still exist, but both relay Proxmox's VNC stream rather than a text terminal, and no button opens them. **Shell** is the only text terminal.

---

## Migration

**Migrate** on a guest, or in the bulk toolbar, moves it within its cluster or to another one.

1. Set **Migration Type**. Within a cluster, choose a **Migration Mode** (Live, Storage or Both), a **Target Node** (automatic picks the least loaded) and a **Target Storage**, per disk where there are several. Across clusters, choose a **Target Cluster**, **Target Node**, **Storage Mapping** and **Network Mapping** (an unmapped network keeps its name), and optionally a **Target VMID**
2. Set **Live Migration** (for a container, a restart migration), **Bandwidth Limit (KiB/s)**, **Format** where the target allows a choice, and **Delete Source After Migration**, which is on for a storage move and off across clusters, where it destroys the source
3. Click **Create & Run Pre-Flight Checks**, then **Execute Migration**. A failed check blocks it; a warning does not

Within a cluster the checks are that the target node is online and the VMID is free. Across clusters they add both clusters being Proxmox VE 7 or later, CPU compatibility (a warning) and the storage and network mappings. Nothing checks capacity.

This needs `manage:migration` on the source cluster and, across clusters, on the target too. There is no page of migration jobs: `GET /api/v1/migrations` lists them and `POST /api/v1/migrations/:id/cancel` cancels one. The API's simpler `POST …/vms/:vm_id/migrate` and `…/containers/:ct_id/migrate` need `execute:vm` and `execute:container`.

### Node Evacuation

**Evacuate** in a node's header (`manage:node`; needs another online node) moves its running guests away, **Distribute across nodes (DRS-aware)** or to a **Single target**. Stopped guests stay. The results list per-guest errors, and "migrated" there means the migration started, so follow the tasks. See [API Token Requirements](#api-token-requirements) for what Proxmox must let the token read.

---

## Folders and Favorites

**Folders** live in the inventory tree's **VMs & Templates** view, and belong to Nexara, not Proxmox. Right-click a cluster for **New folder**, and a folder for **New subfolder**, **Rename** or **Delete** (its guests fall back to **Discovered**, and its subfolders are deleted). Move a guest by dragging it or with **Move to folder…**. A name is 1 to 128 characters, unique at its level, and this needs `view:vm_folder` and `manage:vm_folder`.

A folder's page has **Summary**, **VMs**, **History** (see [A Folder's History](#a-folders-history)) and **Alerts** (`view:alert`).

**Favorites.** **Add to Favorites** on a cluster's, node's or guest's menu pins it to a **Favorites** section above the tree, per user. The section shows only what your grants still let you see, and a guest there keeps its full menu.

---

## Node Management

A node's tabs are **Summary**, **VMs**, **Network**, **Disks**, **Services**, **Firewall**, **Syslog**, **PCI Devices**, **Updates** and **History**. While it is online, its header has:

- **Shell** — see [Consoles](#consoles)
- **Support Bundle** (`manage:node`, audited) — downloads its `pvereport`, which discloses storage paths, network layout, packages and every guest's configuration
- **Evacuate** — see [Node Evacuation](#node-evacuation)
- **Enter Maintenance** / **Exit Maintenance** — see [Node Maintenance](#node-maintenance)
- **Reboot** and **Shutdown** (`manage:node`) — they ask first and do not migrate anything

**Summary** shows the processor, memory and system, with a pencil for the time zone and another for **Network & DNS** (**Search Domain**, **DNS Server 1** to **3**; a save replaces them all, `manage:node`). Below are [Options and Notes](#node-options), [Memory modules](#memory-modules) and **Temperatures**, the node's hardware sensors, read over the cluster's [SSH credentials](#prerequisites) with a pinned host key (`view:node`).

**Network** — see [Network Interfaces](#network-interfaces). **Firewall** — see [Node Firewall](#node-firewall).

**Disks** (`manage:node`). Click a device for its S.M.A.R.T. data. **Init GPT** and **Wipe** act on a disk, and ZFS, LVM, LVM-Thin and Directory storage can be created from the node's disks. Destroying one offers **Cleanup Disks** and **Cleanup Storage Configuration**; a Directory cannot be destroyed here.

**Services.** Start, restart and stop node daemons (`manage:node`). **Restart** and **Stop** ask first; **Start** does not. Proxmox will not stop `pveproxy`, `pvedaemon` or `pve-cluster`, so those offer **Restart** only. **Syslog.** The node's log for the last hour, day, three days or week, filtered by service, 500 lines a page. **PCI Devices** is read-only.

**Updates.** **Pending updates** with [**Update this node**](#updating-a-single-node-in-place) (`view:rolling_update` and `manage:rolling_update`), and **APT Repositories**, which switches repositories on and off and adds the standard Proxmox ones (`view:apt_repository` and `manage:apt_repository`).

---

## Node Options

A node's own settings are on its page, in the **Options** and **Notes** cards. They are the settings Proxmox shows in a node's **Options** and **Notes** panels, changed with the pencil (**Edit**) button on each card and saved to the node without leaving Nexara.

| Setting | What it does | Shown when unset |
|---------|--------------|------------------|
| **Start on boot delay** | Seconds the node waits, once it has booted, before it starts the guests marked to start on boot (0–300). Raise it when guests start before their storage is online. Proxmox waits 0 seconds when it is unset. | *Default (no delay)* |
| **RAM ballooning target** | The RAM usage, in percent of the node's memory, that automatic ballooning aims for (0–100). Proxmox aims for 80% when it is unset. | *Default (80%)* |
| **Wake-on-LAN** | The MAC address this node is woken by and, optionally, the interface and the broadcast address this node uses when it sends a wake packet to another node. | *Not configured* |
| **Location** | Latitude, longitude and an optional name of up to 128 characters. The datacenter's location applies when it is unset. | *From datacenter* |
| **Notes** | Free text, shown as plain text. | *No notes.* |

**Unset is not the same as the default.** A node's config file holds only what has been set, and Nexara shows what is stored: a delay saved as `0` is a stored value, not an absent one. To remove a setting, clear its field and save; Nexara tells Proxmox to delete it, which returns the node to its default. A field you left as it was is not sent at all.

**Wake-on-LAN is read from two nodes.** The MAC is the one Proxmox wakes a node with, and it reads it from the node being woken. It is the required part of the setting, and a bare MAC is enough. The interface and the broadcast address are what a node uses when it *sends* a wake packet, and Proxmox reads them from the sending node, so set them on the node you wake others from. That also means entering that node's own MAC, because the setting is not valid without one.

**Version floors.** A field is shown only when the node's Proxmox VE is new enough to have it, and is not sent to one that is not: **RAM ballooning target** needs pve-manager 8.3.6, the Wake-on-LAN interface and broadcast address need 8.1.9, and **Location** needs 9.1.13 (Proxmox VE 9.2). A setting already stored on an older node is still shown and can be removed; changing it needs a Proxmox VE that knows the setting, because Proxmox refuses a setting it has no schema for, while removing one works for any.

**The node must be online.** Proxmox forwards the read and the write to the node itself, so an offline node shows no options and cannot be edited.

**Concurrent edits.** Saving is a compare-and-swap against the whole node config file, and that file is shared with the node's ACME certificate settings and with the Notes panel in Proxmox's own UI. If anything changed it after you opened the dialog, the save is refused with *The node's configuration changed since it was read*; Nexara re-reads the node's stored settings, so check what changed and save again. The exception is a node that had no config file when you opened the dialog, or whose file was empty (every setting and the notes cleared, which Proxmox reads as no file): it has nothing to be checked against, so that next save is unconditional and cannot conflict.

**The `digest` is an opaque token.** What the API returns as `digest` for a node's options, notes and ACME settings, and takes back on a save, is a save token that Nexara makes, not Proxmox's own digest of the file; send it back exactly as the read returned it. Proxmox's digest is never returned, because it is an unsalted hash of the whole file, notes included, and would let anyone shown it test guesses at the file's content. The token is tied to the cluster and the node it was read from and is checked against the file as it is when you save, so one read's token is refused for another node, and a client that sends Proxmox's own digest is always refused with the same *changed since it was read* message. See [the save token](api-reference.md#the-node-config-save-token) in the API reference.

**ACME settings are checked on every save.** Every save of options or notes makes Proxmox re-check the node's ACME settings, so a broken ACME configuration, such as a domain whose DNS plugin has since been deleted, makes the save fail with Proxmox's own message. Fix the ACME settings first.

**Size and characters.** Proxmox refuses a request over 64 KiB (512 KiB from Proxmox VE 8.4), counted after encoding, where a line break or a non-ASCII character takes several bytes. Very long notes can therefore fail well inside the 65,536-character limit: Nexara answers *The request is too large for Proxmox* and the node is unchanged, so shorten the notes. Nexara refuses a request over 512 KiB itself, before sending it. On a Proxmox VE before 8.4, a request between 64 and 512 KiB is sent, and it is Proxmox that refuses it: it answers after reading only the head of the request and may reset the connection instead, so you can see a connection error (a 502) in place of that message. The node is unchanged in either case. The Wake-on-LAN and Location fields refuse line breaks and other control characters; the notes keep them.

**Permissions.** Reading the settings needs `view:node`. Reading the notes, and changing either, needs `manage:node`: notes are shown only to users who hold `manage:node` on the cluster, and everyone else sees a note saying so. The same goes for the save token a save is checked against: Nexara returns it only to users who can save, whether they read it through the node's options (`manage:node`) or through its ACME settings (`manage:certificate`), although it reveals nothing about the file's content (a holder of a token can still tell that the file changed). The cluster's Proxmox API token needs `Sys.Audit` on `/` for either read and `Sys.Modify` on `/` to write, and `Sys.Modify` is held by `Administrator` and not by `PVEAdmin`; see [API Token Requirements](#api-token-requirements).

**Audit.** Each save writes one `set_options` entry against the node. It records which settings were set and which were cleared, by name, and the values of the two integers. It never records the notes, the location or the Wake-on-LAN setting: every built-in Viewer holds `view:audit`, and a position or a MAC address identifies a machine.

> **Keep credentials out of notes.** Nexara shows them only to users with `manage:node`, but anyone whose Proxmox account holds `Sys.Audit` on `/` can still read them in Proxmox's own Notes panel, and they are stored as plain text in the node's config file.

---

## Memory Modules

A node's page shows its memory DIMM layout in the **Memory modules** card on the Summary tab: every slot, the module in it (size, type, the speed the board runs it at against its rating, manufacturer and part number), and the totals: installed memory, slots used, the firmware's maximum and ECC. The same layout feeds the **Memory headroom** section of the capacity forecast report.

Proxmox has no API for this, so Nexara reads it with `/usr/sbin/dmidecode -t 16 -t 17` over the cluster's **SSH credentials** (Security → Rolling Updates → SSH Credentials). That needs root.

- **Daily.** The collector reads each online node of every active cluster once its last read is older than `NODE_MEMORY_READ_INTERVAL` (default `24h`; `0` turns it off). That is one root SSH login per node per day, recorded in the audit log as the system user. A cluster without SSH credentials, or a node whose host key is not pinned, is never dialed.
- **Read now.** The card's **Read now** button (`manage:node`) reads the node at once, and is audited. It works once a minute per node: each read is a root login, and repeated rejected logins get Nexara's address banned by fail2ban.
- **A failed read keeps the last good one.** The card shows why the newest read failed above the last good read, for example *Reading the memory layout needs root* when the SSH user is not root, or a warning when the node's host key no longer matches the pinned one.
- **What is not stored.** Serial numbers and asset tags are skipped and never kept. The firmware's maximum is the board's own figure, which boards often misstate.
- **Virtual nodes.** A node that is itself a VM lists only the memory it booted with; memory hot-plugged later is in no DIMM table. When the firmware lists less than the node has, the card says so, and the capacity forecast calls that node's table incomplete rather than reading its headroom from it.

---

## Metric History and Pressure

The CPU, memory, disk and network charts on the dashboard, a node's page and a guest's page read Nexara's stored samples, one per `METRICS_COLLECT_INTERVAL`, kept for 30 days.

- **Resolution.** **1h** shows the samples themselves, in 10-second buckets; **6h** one-minute buckets; **24h** five-minute and **7d** hourly averages.
- **Live** shows the last hour of stored samples, refreshed every minute, followed by the newest ticks.
- **A break in a line means nothing was measured:** Nexara took no sample (for example while a collection pass stalled), a counter fell (a guest restarted or moved), or Proxmox left it unreported. A drop to zero is a real zero.
- **A cluster's charts** show the total disk and network throughput of its nodes, the average of their CPU, and memory as a share of the cluster's total.

**Pressure.** Below those charts, the **Pressure** card shows Linux pressure stall information (PSI): the share of time tasks waited for CPU, memory or IO. The area is time at least one task waited ("some"); the dashed line is time every non-idle task waited at once ("full"). A storage stall shows here even when throughput only dips, because the tasks wait. Each point is the highest value in its interval.

- **Proxmox VE 9 or later.** A node or guest on Proxmox VE 8 shows no pressure data. A node's RRD keeps no CPU "full", so a node's card has no dashed CPU line.
- **Guests** report pressure with every metrics cycle.
- **Nodes** keep it in their RRD, at one-minute steps for a day. A background pass copies new steps every `NODE_PRESSURE_SYNC_INTERVAL` (default `5m`; `0` turns it off), so a node's pressure survives a Nexara outage of up to a day.
- Pressure is kept as long as the other raw metrics, 30 days.

---

## Block Jobs

A running VM's [History](#a-guests-history) tab opens with the QEMU block jobs working on its disks, as live rows above the history: a mirror while a disk moves, a live migration copies local disks, or a running VM is cloned; a stream during a live restore from Proxmox Backup Server, or while a snapshot is removed on Proxmox VE 9's volume-chain storage; a backup. Each shows how far it has got, its speed limit, and the rate and time left, worked out between refreshes. The rows refresh every 3 seconds while a job runs, every 15 seconds otherwise, and every minute after a failed read, and only while the History tab is open. With no job running there are no rows.

- The read needs `view:vm`, and the History tab it sits in needs `view:audit` and `view:task` — every built-in role holds all three. Nexara reads the jobs through Proxmox's QEMU monitor with one fixed, read-only command (`info block-jobs`). For that the cluster's API token needs `Sys.Audit` on the VM on Proxmox VE 9, or `VM.Monitor` on Proxmox VE 8.
- A QEMU too stuck to answer (a hung storage backend, say) shows that as a row's message: Proxmox waits up to 25 seconds for QEMU, and Nexara up to 30 for Proxmox. Several people watching the same VM share one read.
- A line QEMU prints in a shape Nexara does not recognise is shown as printed rather than dropped.

---

## Pools, Replication and Cluster Options

**Pools** (`view:pool`, `manage:pool`). **Create Pool**, then expand a pool's row for **Add VM** and **Add Storage**. Proxmox refuses to delete a pool that still has members, and on success the delete removes the ACL entries on it too.

**Replication** (`view:replication`, `manage:replication`). **Create Job** takes the guest, **Target Node**, **Schedule** (a Proxmox calendar event, `*/15` by default), **Rate Limit** and **Comment**. A row's action triggers a sync now. Deleting a job also removes the target's copy once Proxmox gets to it. A job's log is available only through the API.

**Options** (`manage:cluster`). **Notes**; **General**, whose **Datacenter Options** are the migration network, fencing, the HA **Shutdown Policy**, [CRS](#native-crs-coexistence-proxmox-ve-92), **Bandwidth Limits** and the **Next VMID Range**, saved with **Save Changes**; **Tags**; and **Cluster Info**.

**Metrics** (`manage:cluster`). **Add Server** points Proxmox at an external metrics server by ID, Type, Server and Port. There is no edit, and an InfluxDB v2 server, which needs a token and a bucket, can be added only through the API.

---

## Your Account

The user menu (your name, top right) holds **Profile**, **Security**, **API Keys**, **What's New**, **About**, **Sign Out** and **Sign Out All Devices**.

**Profile** shows your email, role, auth source, 2FA status and join date. A local account can change its **Display Name** and use **Change Password**: 8 to 72 characters with an upper-case letter, a lower-case letter, a digit and a special character. Changing it ends every session. Five wrong current passwords within 60 minutes, here and at Enable 2FA together, lock both for 30 minutes.

**Security** has [two-factor authentication](#two-factor-authentication-totp) and **Active Sessions**: each sign-in's device and address, when it was last active and **This device**. **Revoke** ends one, and asks first for your own. An access token that sign-in already holds keeps working until it expires, within 15 minutes by default.

**Sign Out All Devices** ends every session without asking. An API key is not affected, and an access token already issued keeps working until it expires, within 15 minutes by default.

### API Keys

**API Keys** → **Create API Key** takes a name and an expiration (**Never**, **30 days**, **90 days** or **1 year**; through the API, from an hour to 100 years) and shows the key once. Send it as `Authorization: Bearer nxra_…`.

- It acts with its owner's permissions as they are at each request, and stops working if the owner is deactivated.
- It survives a password change and **Sign Out All Devices**, so revoke it yourself if it may have leaked: **Revoke** → **Revoke Key**.
- It cannot change its owner's credentials or end their sessions: change-password, the two-factor routes, sign-out-all, ending a session and creating another key answer 403.
- Creating one needs a global `manage:api_key`, which every built-in role holds. An account holds at most 25 unrevoked keys, expired ones counted. `manage:user` lists and revokes other users' keys through the API (`/api/v1/admin/api-keys`).

**View API Documentation** on that page opens the live catalog of every route, with its permission and parameters, for any signed-in user.

---

## User Management

Navigate to **Admin > Users** to manage user accounts.

### Creating Users

The **first** user self-registers via the registration page and is automatically promoted to Admin — that is the install bootstrap. After that, self-registration is closed: only an administrator can create accounts.

To add a user, go to **Admin > Users** and click **Create User**, then supply an email, a password, and an optional display name. New accounts receive the **Viewer** role globally; assign anything more from the user's role dialog.

Creating a user takes an account whose legacy `role` field is `admin`, which is a different thing from the Admin RBAC role. The first user has both. Someone you grant the Admin role to gets 403 *Only admins can register new users* until you also send `PUT /api/v1/users/:id` with `{"role":"admin"}` (that needs `manage:user` and `manage:role`) and they refresh their sign-in, since the check reads it from their token. The field shows as **Role** on the user's Profile.

### Managing Users

From the Users page, administrators can:

- **View** all registered users with their roles, auth source, and 2FA status. The Roles column lists global roles only, so a user who holds only cluster-scoped roles reads "No roles"
- **Active** — a switch that deactivates the account, without asking: it ends the user's sessions and refuses their API keys at once, though an access token already issued keeps working until it expires, within 15 minutes by default. You cannot switch off your own, but nothing stops the last administrator being deactivated by another
- **Manage roles** — the shield icon
- **Reset 2FA** — remove a user's TOTP enrollment if they lose access to their authenticator
- **Delete user** — answers *Failed to delete user* for anyone who has created an alert rule, channel, maintenance window, report, rolling update or import, or acknowledged or resolved an alert, since those records name them. Deactivate such an account instead

There is no way to reset someone's password from the UI or API. See [Recovering from a Lost Admin Account](#recovering-from-a-lost-admin-account).

### Auth Sources

Each user has an auth source:
- **local** — email + password stored in Nexara
- **ldap** — authenticated against LDAP/Active Directory
- **oidc** — authenticated via OIDC/SSO provider

LDAP and OIDC users are provisioned automatically (JIT) on first login.

### Recovering from a Lost Admin Account

If every administrator has lost access (forgotten password, lost 2FA device with no recovery codes), set a new password directly in the database: see [Reset admin password](installation.md#reset-admin-password). Do not delete rows from `users` to bring back the first-run setup, because other records reference them.

---

## RBAC Setup

Nexara uses role-based access control with granular permissions. Navigate to **Admin > Roles**.

### Built-in Roles

| Role | Description |
|------|-------------|
| **Admin** | Every permission |
| **Operator** | Everything except users and roles (which it cannot even read), SSH credentials, settings and Proxmox access writes. On alerts, notification channels, maintenance windows, reports, CVE scans, certificates and guest tools it can read but not manage, though it can acknowledge alerts, generate reports and delete clusters |
| **Viewer** | Every `view:*` permission, the user list and role list included, plus `manage:api_key` |

Built-in roles cannot be edited or deleted.

### Custom Roles

1. Click **Create Role**
2. Enter a role name and description
3. Tick permissions in the matrix
4. Click **Create Role** (**Save Changes** when editing)

A change takes effect at each holder's next request.

### Permission Format

Permissions follow the pattern `action:resource`. Examples:

| Permission | Description |
|------------|-------------|
| `view:cluster` | View clusters |
| `manage:cluster` | Create, update clusters; create, change and delete the resource mappings used for [device passthrough](#device-passthrough) — a PCI mapping can name any device of a node, its own disk and network controllers included |
| `delete:cluster` | Delete clusters |
| `manage:vm` | Create, update VM configuration |
| `execute:vm` | Start, stop, snapshot VMs |
| `manage:migration` | Create, execute, and cancel migration jobs, which is what the **Migrate** button does |
| `console:vm` | Open VM consoles |
| `console:container` | Open container consoles |
| `console:node` | Open node shell consoles (a login prompt on the Proxmox host) |
| `view:audit` | View audit log |
| `manage:alert` | Create and manage alert rules |
| `manage:user` | Create, update, delete users |
| `manage:role` | Create, update, delete roles and assignments |

Note that acting on a guest and configuring it are separate: starting, stopping
and snapshotting a VM is `execute:vm`, while editing its hardware or creating it is
`manage:vm`. The **Migrate** button creates a migration job, so it needs
`manage:migration`; `execute:vm` covers only the API's direct migrate route.

The complete catalog is whatever **Admin > Roles** lists — it is seeded from the database, so the role editor is always the authoritative list.

> **Console access is not implied by `view:*`.** Opening a shell or console
> requires the dedicated `console:*` permissions, which the built-in Admin
> and Operator roles hold. The built-in Viewer role deliberately does not —
> a read-only account cannot open a node shell or a guest console.
> To build a "viewer plus consoles" role, grant `console:vm` /
> `console:container` (and `console:node` only if node shells are intended)
> alongside the view permissions. A node shell opens at the host's login
> prompt: Proxmox logs a shell straight in as root only for `root@pam` itself,
> and Nexara connects with an API token, whose identity is
> `user@realm!token` — never `root@pam`, even for a token `root@pam` owns.

### Assigning Roles

1. Go to **Admin > Users**
2. Click the shield icon (**Manage roles**) on a user's row
3. Choose from **Select a role...** and click **Assign**, once for each role
4. Permissions are the union of all assigned roles

Every assignment carries a **scope**: `global` (all clusters) or `cluster` (one cluster). A global grant covers everything; a cluster-scoped grant covers only that cluster, and list views follow it — alert rules, migration jobs, report schedules and runs, task history and audit entries are all filtered to the clusters the caller can reach, so a scoped user sees a smaller, complete list rather than a full one with holes in it.

The role dialog assigns global scope only. To scope a grant to one cluster, use the API:

```bash
curl -X POST https://nexara.example.com/api/v1/rbac/users/$USER_ID/roles \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"role_id":"'$ROLE_ID'","scope_type":"cluster","scope_id":"'$CLUSTER_ID'"}'
```

The assignment's scope shows as a badge next to the role name in the user's role dialog; it says `cluster`, not which one. A route that belongs to no one cluster never accepts a cluster-scoped grant, and an account that signs in through LDAP or OIDC loses cluster-scoped and hand-assigned roles at its next sign-in ([see below](#how-ldap-login-works)).

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
permitted to do. `GET /api/v1/clusters/{cluster_id}/access/permissions` (`view:access`) reports
exactly that, and the tab uses it to mark what it cannot change as read-only; check it first when
an action fails with a Proxmox-side error rather than a Nexara 403.

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
explanation rather than proceeding. So does editing that user so as to
**disable** it, give it an **expiry**, or **change its groups**: Proxmox checks
the owning user whenever it verifies a token, and a token's permissions can come
through that user's groups. So does editing that token (an API call; the UI
only regenerates tokens) to give it an **expiry** (any value but 0, which means
never) or to turn its **privilege separation on**: an expired token is refused,
and a separated token holds only the permissions granted to the token itself, and
never more than its user holds, so turning separation on can only take
permissions away.

It warns rather than blocking outright: an operator rotating credentials by hand
has a legitimate reason to do exactly this, and a hard block would just send them
to the Proxmox UI to do it less safely. Append `?force=true` to proceed. A
delete, a regenerate or a disable cuts Nexara off at once, and an expiry does
when it arrives; a group change, or privilege separation turned on, can take away
permissions the token relies on. The cluster then shows as unreachable, or fails
some actions with permission errors (403), until you update its credentials in
Nexara or, for an edit, undo it in Proxmox. The audit log's `forced` is true for
a delete, a regenerate or a guarded edit sent with `force=true`, on any account,
so it alone does not mean the account was Nexara's own; a forced edit the guard
does not cover (a comment-only edit, a token expiry of 0, privilege separation
turned off) records `forced: false`. A user edit's entry also shows which of
enable, expiry and groups it set, and a token update's shows the expiry and
privilege separation it set.

When Nexara cannot read the cluster's own record to check whether the credential
is its own, it refuses the guarded change without sending anything to Proxmox, so
nothing changes: **500** (try again), or **404** if the cluster no longer exists.
A request with `?force=true` skips the check. Neither is a **409**, so the UI
reports an ordinary failure and does not offer the override.

The UI puts each override behind a type-the-name confirmation whose title says
what it confirms — "Deleting this user will cut off Nexara's access" (button
**Delete User**), "Revoking this token will cut off Nexara's access" (**Revoke
Token**), "Regenerating this token will cut off Nexara's access" (**Regenerate
Token**) and, for an edit, "Saving this edit will cut off Nexara's access"
(**Save Anyway**). Type the user id — the full `user@realm!token` id for a
token — to enable the button. An edit sends only the fields you changed, and
**Save Anyway** re-sends exactly the edit that was refused, with
`?force=true`; Cancel or Escape on that dialog goes back to the form with the
edit intact. While a delete, revoke, regenerate or forced save is being sent,
its dialog stays open and cannot be dismissed until the request settles.

The guard is narrow on purpose — it fires only for the specific credential this
cluster authenticates with, and only for changes that could actually break it.
Edits that cannot affect an API token (comment, e-mail, names, the two-factor keys
field) are not guarded, because making every edit to `nexara@pve` demand a force
flag would train operators to pass it reflexively. The same goes for a token's
comment, an expiry of 0 and privilege separation turned off. It does not watch
every route to the same end, though: ACL changes (revoking the token's or its
user's grant, granting NoAccess or a narrower role on a deeper path), deleting a
group its user belongs to, and editing or deleting a custom role it holds can
each take away permissions Nexara relies on, and none of them is guarded.

---

## Authentication Providers

### LDAP / Active Directory

Navigate to **Admin > LDAP** to configure LDAP/AD authentication. Only one enabled configuration is used.

#### Setup

1. Click **Add Config**
2. Choose a **Directory Type** preset, name it, and tick **Enabled** (off by default)
3. Configure connection:
   - **Server URL** — `ldaps://ad.example.com:636`, or `ldap://ad.example.com:389` with **StartTLS**. A plain `ldap://` URL sends passwords in the clear, so saving it asks for confirmation (**Save without encryption**)
   - **Skip TLS Verify** — for a self-signed directory certificate
   - **Bind DN** — service account DN (e.g., `cn=nexara,ou=services,dc=example,dc=com`)
   - **Bind Password** — service account password (encrypted at rest)
   - **Search Base DN** — search base (e.g., `dc=example,dc=com`)
4. Configure user search:
   - **User Filter** — filter template, where `{{username}}` is what the user typed. The sign-in page asks for an email address and **Sync Now** searches by the stored email, so the filter must match on mail, as the Active Directory preset's `(|(sAMAccountName={{username}})(userPrincipalName={{username}})(mail={{username}}))` does. A filter on `sAMAccountName` alone finds nobody
   - **Email Attribute** and **Display Name Attribute** (e.g., `mail`, `displayName`)
5. Configure group mapping (optional):
   - **Group Search Base DN** — where to search for groups; left empty, no groups are read
   - **Group Filter** — filter for group membership (e.g., `(member={{userDN}})`)
   - **Group-to-Role Mapping** — map a group's **full DN**, as the directory returns it, to a Nexara role
   - **Default Role (no group match)** — what a user gets when no group matches. Left at no default, a new user has no permissions at all
6. Click **Create**

The **Test Connection** panel appears only once the config is saved, and tests the saved values: **Test** and **Sync Now**.

> **StartTLS needs Skip TLS Verify today.** With verification on, Nexara starts the TLS handshake with no server name and it fails. Use `ldaps://` to keep the certificate checked.
>
> **Sync is manual.** The **Sync Interval (minutes)** field is stored, but nothing runs it: only **Sync Now** syncs. A sync disables a user the search does not find, and that includes any search error, such as a directory outage; a later sync re-enables anyone still in the directory, including someone you deactivated by hand.

#### How LDAP Login Works

1. User enters their email and password on the login page
2. Nexara looks the address up locally first:
   - **no local user** — and LDAP is enabled: the credentials go to LDAP, and a successful bind provisions the account (JIT)
   - **local user with `auth_source=ldap`** — the credentials go to LDAP
   - **local user with `auth_source=local`** — the password is checked against the stored hash and LDAP is never contacted, so a local account shadows a directory account with the same address

   The password is only ever sent to the directory on the first two paths — a local account's password never leaves Nexara.
3. If no local account existed, one is JIT-provisioned with `auth_source=ldap`
4. LDAP group memberships are mapped to Nexara roles
5. Every login and sync **replaces** the user's roles: all of them are removed, hand-assigned and cluster-scoped ones included, and only the mapped global roles or the default role are added back

### OIDC / SSO

Navigate to **Admin > OIDC / SSO** to configure single sign-on.

#### Setup

1. Click **Add Config**
2. Choose a **Provider Preset** (Keycloak, Authentik, Azure AD, Google, Okta or Custom), name it, and tick **Enabled** (off by default). The name is what the sign-in button says
3. Configure provider:
   - **Issuer URL** — OIDC issuer (e.g., `https://accounts.google.com`). It must be `https` and resolve only to public addresses, with no override, so a provider on your LAN is refused
   - **Client ID** — from your identity provider
   - **Client Secret** — from your identity provider (encrypted at rest)
   - **Redirect URI** — `https://your-nexara-domain/api/v1/auth/oidc/callback`. Users must open Nexara at this
     same address to sign in with SSO: the sign-in is bound to the browser that starts it, and one finished at
     another address is refused — on the sign-in page at this address, where starting again works
4. Configure claims mapping, all read from the ID token:
   - **Email Claim**, **Display Name Claim** and **Groups Claim**
   - **Scopes** — comma-separated
5. Configure who gets in:
   - **Auto-Provision Users** — create an account on first sign-in (on by default in the form, off through the API)
   - **Allowed Email Domains** — comma-separated; empty allows all
   - **Group-to-Role Mapping** — exact group names to Nexara roles
   - **Default Role (no group match)**
6. Click **Create**

**Test Discovery Endpoint** appears once the config is saved, and tests the saved values.

#### How OIDC Login Works

1. User clicks **Sign in with** the provider's name on the login page
2. Browser redirects to the identity provider with PKCE challenge
3. After authentication, the provider redirects back with an authorization code
4. Nexara exchanges the code for tokens, verifies the ID token (signature, nonce, audience)
5. With **Auto-Provision Users** on, a local user is created (JIT provisioning) with `auth_source=oidc`. A local or LDAP account with the same email is not taken over: the sign-in fails with *User provisioning failed*
6. Group claims are mapped to Nexara roles, and as with LDAP this replaces the user's roles at every sign-in

The sign-in is bound to the browser that started it, with a cookie set at step 1 and another once steps 4–6 are done. A
link from someone else's sign-in — the provider's redirect back, or the `/oidc-callback` page it leads to —
opened in another browser signs nobody in, so an attacker cannot sign a victim's browser in as themselves.

### Two-Factor Authentication (TOTP)

#### User Self-Service

1. Open **Security** from the user menu (your name, top right)
2. Click **Enable 2FA**
3. Enter your current password. An account that signs in through LDAP or single sign-on has no Nexara password to
   enter: it needs a sign-in from the last 10 minutes instead, so sign out and back in first if yours is older.
   Five wrong passwords within an hour, here and at **Change Password** together, lock both for 30 minutes
4. Scan the QR code with an authenticator app (Google Authenticator, Authy, etc.)
5. Enter the 6-digit code to verify
6. Save the recovery codes in a secure location: 10 single-use `XXXX-XXXX` codes, shown once. **Regenerate Recovery Codes** needs a code from your authenticator

#### Admin Management

- Admins can see 2FA status for all users on the **Admin > Users** page
- Admins can **reset** a user's 2FA if they lose access (the user will need to re-enroll)
- Admins cannot reset their own 2FA (security measure)

#### Login with 2FA

When 2FA is enabled, login becomes a two-step process:
1. Enter email and password (for LDAP too, since the sign-in page asks for an email address), or complete SSO
2. Enter the 6-digit TOTP code from your authenticator app
3. If you lose your device, use a recovery code instead. It signs you in, but **Disable** and **Regenerate** in the UI take only an authenticator code, so you then need an admin's **Reset 2FA** (or `DELETE /api/v1/auth/totp` with `recovery_code`)

Five wrong codes in 10 minutes block further tries for 5 minutes.

---

## DRS Configuration

The Distributed Resource Scheduler automatically balances VM workloads across cluster nodes.

### Enable DRS

1. Navigate to a cluster's detail page
2. Go to the **DRS** tab
3. Choose a **Mode**:
   - **Advisory — recommend only** — DRS generates migration recommendations for you to review; carry out the ones you agree with using **Migrate**
   - **Automatic — migrate VMs** — DRS executes the migrations itself
   - **Disabled — do nothing** — turns evaluation off
4. Configure:
   - **Resource Weights** — how heavily CPU vs. memory pressure counts toward a node's load score
   - **Imbalance Threshold** — the cluster imbalance percentage that triggers migration planning (5–100%, in 5% steps)
   - **Evaluation Interval** — how often DRS evaluates balance, in seconds, at least 60
   - **Include containers in balancing** — balance CTs alongside VMs
5. Click **Save Configuration**

### DRS Rules

On the **Rules** sub-tab, **Add Rule** controls VM placement:

- **Affinity** — keep guests together on the same node; needs at least two
- **Anti-affinity** — keep guests on different nodes (e.g., HA pairs); needs at least two
- **Pin** — lock a guest to one or more nodes

Its **Target** is **Nexara Only**, which only DRS honours, or **Proxmox HA**, which writes a rule to Proxmox under the **Rule Name** you give it.

### Manual Evaluation

Click **Run Evaluation** to trigger an immediate DRS evaluation. In Advisory mode, this lists migration recommendations for you to review; they cannot be applied from the list. With **Mode** on Disabled, or a configuration that was never saved, it reports **Cluster Balanced** at 0.0%: nothing was evaluated.

### DRS Migrations and History

The **Migrations** tab within DRS shows the moves DRS proposed or carried out. An evaluation that found the cluster balanced leaves no row. Its **History** tab is the audit trail: who changed the DRS configuration, triggered an evaluation, created or deleted a rule, and what was done to Proxmox's HA rules from this page.

### Guests DRS Leaves in Place

DRS does not pick a guest Proxmox cannot migrate right now: one with a config lock (a backup or a snapshot, say) or with a task running on it, such as a disk move. An open console does not count. If a node's tasks cannot be read, its guests stay put until the next evaluation. All of these still count toward their node's load and toward rules.

After a failed migration DRS leaves that guest alone for 15 minutes, doubling with each failure in a row up to 6 hours, and balances with other guests meanwhile. A completed DRS migration resets the count, and failures older than 7 days stop counting. The wait comes from the DRS history, so restarting Nexara does not clear it, and **Run Evaluation** does not skip it. To move the guest sooner, migrate it by hand; that does not reset the count.

The audit entry for a failed migration carries the reason from the task's log in its details, such as `migration aborted (duration 00:00:10): can't lock file '/var/lock/qemu-server/lock-101.conf' - got timeout`. If the log names no reason or cannot be read, it carries Proxmox's exit text instead.

For an HA-managed guest, Proxmox's migrate call only queues the request. DRS follows it to the migration the HA manager starts and records how that ended, and it waits for each migration before starting the next. If the HA manager starts none within two minutes, the attempt counts as failed.

If Nexara shuts down while following a migration, the attempt is recorded as `interrupted` in the DRS history, with a `drs_migrate_interrupted` audit entry. Proxmox carries on with the migration regardless, so this is not a failure and starts no wait, and the migration's task shows how it ended. If that task then fails, the failure does not count toward the wait, so DRS may try the guest again sooner. If the scheduler role moves to another replica without a shutdown, the old leader keeps following the migration and records its result as usual. A process killed outright records nothing, and its attempt stays `pending` until a scheduler start at least an hour later marks it `cancelled`.

### Native CRS Coexistence (Proxmox VE 9.2+)

Proxmox VE 9.2 added a native dynamic load balancer to its Cluster Resource Scheduler (CRS). When a cluster has it enabled (`crs: ha=dynamic, ha-auto-rebalance=1` in the datacenter options), Nexara detects it and **disables its own automatic migrations** so the two balancers don't fight — Advisory mode still works as a read-only second opinion, and a banner on the DRS tab explains the state. You can manage the native CRS options from the cluster's **Options** tab, under **General** → **Cluster Resource Scheduling (CRS)**: choose the scheduler **Dynamic — live load balancing**, turn on **Automatic Rebalancing**, and set the threshold, hold rounds, margin and method.

---

## High Availability

Nexara manages Proxmox HA from each cluster's **HA** tab.

On its **Resources** sub-tab, a resource's **State** sets the HA request state. `started` and `ignored` apply at once; `stopped` and `disabled`, which shut the guest down, ask first. HA still moves a `stopped` resource to another node when its node fails, and not a `disabled` one.

### HA Rules

On Proxmox VE 9 and later, create rules from the **HA** tab's **Groups / Rules** sub-tab: **HA Rules** → **Add Rule**. Proxmox VE 8 has **Add Group** there instead. Rule names must start with a letter, be at least 2 characters, and contain only letters, numbers, hyphens, and underscores — the form validates this up front instead of letting Proxmox reject the rule later.

While picking resources for a rule, the search also lists guests that are not yet HA-managed. When you select one, the form offers to add it to HA management with the settings below, ticked by default, and Nexara enrolls it as part of creating the rule — no need to leave the form and add the resource separately.

### Node Maintenance

To service a node without fighting HA (without the cluster's [SSH credentials](#prerequisites) a disabled **Maintenance** button says so, and you also need `manage:node`. The page can tell only with `manage:ssh_credentials`, so a built-in Operator sees it disabled either way, though the API route still works):

1. Open the node's detail page
2. Click **Enter Maintenance** — HA-managed guests are migrated away, and the node shows a maintenance indicator in the tree and node views
3. When you're done, click **Exit Maintenance**

### Cluster-Wide HA Arm/Disarm (PVE 9.2+)

On Proxmox VE 9.2+, the **HA Maintenance** card on the HA tab's **Status** sub-tab (`manage:ha`) can take HA offline cluster-wide for major maintenance:

- **Disarm HA…** — choose what happens to HA resources while disarmed:
  - **Freeze — lock services in place**
  - **Ignore — suspend HA tracking (manage manually)**
- **Re-arm HA** — restore normal HA operation when maintenance is finished

---

## Storage Management

Switch the inventory tree to its **Storage** view and click a cluster, or search
for "Storage" with Ctrl+K. The pool list groups shared storage at the top and
per-node storage below.

### Adding Storage

**Add Storage** on the pool list, or right-click a cluster in the Storage view
(`manage:storage`). Give it an **ID** (letters, digits, `-` and `_`), a **Type**,
that type's fields, its **Content Types**, optionally the **Nodes** it is on, and
**Enable** it. The types are Directory, BTRFS, NFS, CIFS/SMB, GlusterFS, LVM, LVM-Thin,
ZFS, iSCSI, iSCSI Direct, RBD (Ceph), CephFS and Proxmox Backup Server.

- **iSCSI and iSCSI Direct.** Shortly after you type the **Portal (IP/Host)** a node
  scans it and preselects the first target. **Rescan portal** repeats that, and you can
  type an IQN yourself; a failed scan warns and does not block the form.
- **RBD and CephFS.** Filling **Monitor Hosts** makes the storage external, so
  **Keyring** or **Secret Key** becomes required.
- **Proxmox Backup Server.** A generated encryption key is shown once and Nexara keeps
  no copy.

**Edit** cannot change the ID, the Type or an iSCSI portal or target. It sends only
fields you changed to a non-empty value, so blanking a text field does nothing, and
Proxmox refuses changes to some backend fields such as an NFS export.

**Delete** (`delete:storage`) asks you to type the name. It removes the definition for
every node; the data stays where it is.

### Content

On a pool's **Files** tab (`manage:storage` to add anything, `delete:storage` to delete a volume):

- **Upload** — an ISO (`.iso`, `.img`) or a container template (`.tar.gz`, `.tar.xz`,
  `.tar.zst`). Nexara streams it to Proxmox and sets no size limit of its own, so the
  limit that matters is a [reverse proxy's](../README.md#reverse-proxy)
- **Download from URL** — `http(s)`, with a filename, an optional checksum, decompression
  and **Verify TLS certificates**. The node does the fetching
- **Pull OCI** — a container image as a template: Proxmox VE 9.1 or later, with `skopeo` on
  the node. Proxmox calls this a technology preview
- **Browse Appliances** — Proxmox's own appliance list; reading it needs `view:storage`

### Pool Detail

Click a pool to open its detail page. Its tabs are **Summary**, **Files**
(the default), **VMs/CTs** and **History**:

- **Files** — the pool's volumes, with a sub-tab per Proxmox content type (**iso**,
  **vztmpl**, **snippets**, **backup**, and **All**) when it holds more than one.
- **VMs/CTs** — guest-grouped view of every VM disk (`images`) and
  container volume (`rootdir`) currently consuming this pool.

### Migrating Guests Off a Pool

The **VMs/CTs** tab shows one row per guest with name, VMID, kind, disk
count, and total bytes consumed. Two ways to move:

- **Per-guest migrate** — click the migrate icon at the end of a row
  (or right-click and pick *Migrate <N> disks*). The dialog walks every
  disk that guest owns on this pool and dispatches them sequentially to
  the chosen target storage. A tick beside a volume means Proxmox accepted
  the move, not that it finished, so follow the moves in the task list. Most
  users want this — they don't need to know whether the volume key is
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

Multi-select with the checkbox column works on the **Files** sub-tabs. Once
anything is selected, a toolbar appears with **Delete selected**.
Useful for ISO / template cleanup. Each delete is a separate Proxmox
task; failures are reported individually so a single broken volume
doesn't block the rest of the batch.

### Evacuating a Pool Entirely

The **Evacuate** button at the top of a pool detail page kicks off a
bulk move of every VM disk on the pool to a target storage —
equivalent to selecting all guests in the VMs/CTs tab and migrating,
but with a dedicated dialog tuned for "I'm decommissioning this
pool" workflows. It covers `scsi`, `ide`, `sata` and `virtio` disks: EFI, TPM
and unused volumes fail with *Could not resolve disk key*. Container volumes
are not yet covered by Evacuate; use the per-guest migrate or bulk-migrate flow
for those.

---

## Ceph

A cluster's **Ceph** tab needs `view:ceph` to read and `manage:ceph` to change anything. A cluster without Ceph reads **Ceph Not Available**. Above the tabs are health, capacity, IOPS and throughput cards. Below them:

- **Overview** — charts from an hour to 7 days, and the CRUSH topology
- **OSDs** — each OSD's menu has **Mark Out** / **Mark In**, which take effect immediately, and **Stop** / **Start** / **Restart Daemon**, which run as a task on the OSD's host
- **Pools** — **Create Pool** takes a **Pool Name**, **Replication Size** (3) and **PG Number** (128), with a **PG Calculator**. `min_size`, the CRUSH rule and autoscaling can be set only through the API. It creates no Proxmox storage: [add one](#adding-storage) of type RBD. **Delete** asks once, with no typed name, and Proxmox refuses while a storage that references the pool holds disks
- **Monitors**, and **CephFS** when there is one — read-only
- **History**

**Before a daemon stops or an OSD goes out,** Nexara shows the hosts serving data, the OSDs up and in, and each pool's redundancy, and warns *Redundancy will be reduced* or *This can stall guest I/O*, in which case the button reads "… anyway". This assumes host-level failure domains. It advises and never blocks, and reads **Safety check unavailable** when it cannot be worked out.

Creating or destroying an OSD and creating a CephFS are not offered.

---

## Networking

### Network Interfaces

A cluster's **Networks** → **Interfaces**, after picking a node in **Filter by node:**, or a node's **Network** tab. **Create Interface** offers Linux Bridge, Bond and VLAN, and OVS Bridge, Bond and IntPort, each with Proxmox's own options for that type. Edits stay pending, with no marker, until you apply them.

**Apply Config** makes every pending edit live and reloads the node's network, and **Revert** discards every pending edit. Both act on edits made outside Nexara too, and both ask first. A bad configuration can cut the node off from Nexara, and then needs fixing from its console. `view:network`, `manage:network` and `delete:network` gate reading, changing and deleting.

### Cluster Firewall

A cluster's **Firewall** tab has **Cluster Rules**, **Templates**, **Aliases**, **IP Sets**, **Security Groups**, **Log**, **Options** and **History**.

- **Add Rule** inserts at the top, live within about 10 seconds. Rules cannot be edited or reordered here; deleting one needs `delete:network`
- **Options** — **Enable/Disable Firewall** and **Inbound/Outbound Policy** take effect immediately, without asking
- **IP Sets** must be empty to delete. **Security Groups** can be created and deleted, but a group's rules have no page

### Node Firewall

A node's **Firewall** tab has **+ Accept Rule** and **+ Drop Rule**, the log, and delete (`manage:network`). Each button asks first, then adds an enabled, match-everything inbound rule at the top of the node's list, with no form.

> **Both rules match everything.** On a node whose firewall is on, **+ Drop Rule** can cut off access to the node — its web UI and API, SSH, and Nexara's own connection to it. **+ Accept Rule** lets every inbound connection through, whatever the rules below it say.

A guest's own firewall is API only (`/clusters/:id/vms/:vmid/firewall/…`, where the id is the VMID).

### Firewall Templates

**Firewall** → **Templates** holds reusable rule sets that belong to Nexara rather than one cluster, so listing, creating and deleting them needs an instance-wide `view:network`, `manage:network` or `delete:network`. A rule has a direction, action, protocol, destination port and comment. The page cannot edit a template; `PUT /api/v1/firewall-templates/:id` can.

**Apply to Cluster** (`manage:network` on the cluster) only adds rules, and never replaces what is there. The template's rules go to the top of the cluster's list, above your existing rules, in the template's order. Rules are matched from the top, so a broad `DROP` or `REJECT` in a template comes before every rule the cluster already has and can lock you out of the cluster. The dialog says how many rules it adds and where, and warns when the template has a `DROP` or `REJECT` rule. A rule Proxmox rejects is skipped, and a message then says how many of the template's rules were added.

### SDN

A cluster's **Networks** → **SDN** has **Zones** (simple, VLAN, QinQ, VXLAN, EVPN), **VNets** (expand one for **Add Subnet**, with **Enable SNAT** and a **DHCP Range**), **Controllers** (EVPN, BGP, IS-IS), **IPAM** (Proxmox, NetBox, phpIPAM) and **DNS** (PowerDNS). Changes to zones, VNets and subnets stay pending until **Apply Changes**, which asks first.

---

## Certificates (ACME)

A cluster's **Certificates** tab reads with `view:certificate` and changes with `manage:certificate`, which among the built-in roles only Admin holds.

- **Accounts** are read-only here: create one in the Proxmox web UI. The API has routes for them
- **Plugins** — **Add Plugin** takes Standalone (HTTP) or DNS with its **Validation Delay (seconds)** (30 by default). A plugin can be deleted but not edited
- **Node Certificates** — pick a node. **Add Domain** (up to 6) and **Edit** set its ACME domains; removing a domain is done in Proxmox. **Order Certificate** needs a domain and restarts `pveproxy`. **Renew** always forces a renewal, and Proxmox then revokes the old certificate. Both ask first. If Nexara pinned the certificate of the node it connects through, a new one there breaks the pin: see [When a Node's Certificate Changes](#when-a-nodes-certificate-changes). A certificate with under 30 days left reads **Expiring**. Revoking is API only

---

## Device Passthrough

Proxmox lets only the `root@pam` user pass a host device straight through to a VM — a USB device by vendor/device ID or by port (`usbN: host=…`), or a PCI device by its address (`hostpciN: 0000:01:00.0`) — and it checks for that user by name. An API token is `user@realm!tokenname`, never `root@pam`, even when root owns it. Nexara connects to every cluster with an API token, so it passes a device through the one way Proxmox allows a token: a cluster **resource mapping** (Datacenter → Resource Mappings in Proxmox), which the VM names as `usbN: mapping=<name>` or `hostpciN: mapping=<name>`.

### USB Devices

In a VM's **Hardware** tab, **Add Device → USB Device** offers:

- **Mapped device** — a mapping that already exists. Each one shows what Proxmox reports for the VM's node: a warning when the mapping has no entry for that node, an error when its entry names hardware the node does not have. Such a mapping can still be picked, but Proxmox will not start the VM on that node with it.
- **Host device** — this device, on whichever port it is plugged into.
- **Host USB port** — this device, on this port. Use it to tell two identical devices apart.
- **SPICE port** — a device redirected from the SPICE client.

For a host device or port, Nexara reuses the mapping that already passes exactly that device on the VM's node. Otherwise it creates one, named after the device (you can change the name first), with a single entry for the VM's node, and then adds the device to the VM's pending changes. The mapping is created as soon as you confirm, and stays if you then discard the pending change; the next time you add that device, Nexara reuses it. Creating a mapping needs the Nexara permission `manage:cluster`, and the cluster's API token needs `Mapping.Modify`, which Proxmox's `Administrator` role has and `PVEAdmin` does not. Attaching an existing mapping needs `Mapping.Use`, which both roles have. Proxmox keeps mapping creation away from `PVEAdmin`, but Nexara's built-in Operator role holds `manage:cluster` and can create mappings.

Two things work differently from passing a device straight through in Proxmox:

- **A mapped device must be present when the VM starts.** Proxmox checks the mapping at start and refuses to start the VM if the device is missing or, for a port mapping, if a different device is on the port. A direct passthrough starts without the device.
- **Nexara maps the device on one node.** It creates the entry for the VM's current node. For the VM to start on another node, give the mapping an entry for that node (see [Managing mappings](#managing-mappings)).

A USB device passed through directly in Proxmox (`host=…`) is listed in the Hardware tab under its device name, and the row says that only `root@pam` can remove it: Proxmox refuses the change to anyone else. Remove it in Proxmox, then add the device again here if you want Nexara to manage it.

### PCI Devices

**Add Device → PCI Device** offers:

- **Mapped device** — a PCI mapping that already exists, with what Proxmox reports for the VM's node: a warning when the mapping has no entry for that node, an error when an entry names a device the node does not have or that no longer matches it. A PCI mapping may have several entries for one node; the VM then gets the first of those devices that is not in use when it starts.
- **Host device** — a PCI device of the VM's node, listed by IOMMU group. Tick **All functions** to pass the whole device through, every function as one — a graphics card with its audio function, for example.

For a host device, Nexara reuses a mapping whose only entry for the VM's node passes exactly that device, or creates one as it does for USB, with the same permissions. Nexara builds the new mapping from what the node itself reports for the device — its vendor and device IDs, its subsystem IDs, its IOMMU group, and whether it can provide mediated devices — because Proxmox refuses to start a VM whose device does not match its mapping in every one of them. If the node's device list is not available, type the device's address instead, for example `0000:01:00.0`: Nexara still reads the device from the node when it creates the mapping.

The dialog also offers **PCIe**, only when the VM uses the q35 machine type, where it is ticked by default (Proxmox refuses to start any other VM with it); **ROM-BAR**, on by default; and **Primary GPU**. It never sets a ROM file, which Proxmox lets only `root@pam` set.

**A passed-through device leaves the node.** When the VM starts, Proxmox takes the device away from the node's own driver — and every other device in its IOMMU group except PCI-to-PCI bridges — whether or not the node is using it. A PCI mapping can name any device the node reports, including the controller its own disks are on or the network card its cluster network runs over, and anyone with `manage:vm` can then attach it to a VM. Passing such a device through can cut the node off its disks or its cluster, and gives the guest direct access to whatever is attached to it. So the dialog lists what it can tell — a storage, network or USB controller, and the other devices in the IOMMU group (its device list leaves out memory controllers, processors, and host and ISA bridges, which Proxmox takes too if they share the group) — and says so when it cannot tell at all, for an address you typed or a device its list does not hold; it adds the device only once you confirm the node does not need it. It weighs the VM's current node: a mapping's devices on other nodes are taken from those nodes when the VM starts there. Grant `manage:cluster`, which creates mappings, and `manage:vm` with that in mind.

A PCI device passed through directly in Proxmox, or one with a ROM file, says in its row that only `root@pam` can remove it, as a direct USB passthrough does.

As for USB, a mapping Nexara creates has an entry for the VM's current node only. The cluster's **Resource Mappings** tab gives it one for the other nodes the VM may run on, and manages PCI mappings the way it manages USB ones (see [Managing mappings](#managing-mappings)).

### Managing mappings

A cluster's **Resource Mappings** tab lists its USB mappings and its PCI mappings, each on a card of its own. The USB card lists each mapping with each node entry: the node, the device's vendor:product ID, and the port, or *Any port* for a mapping that follows the device. Every entry shows what Proxmox reports when it checks the mapping **on that node**: *OK*, the problem (the device is missing, or a different device is on the port), or *Not checked* with the reason — the node is offline or not a node of this cluster, it did not answer in time, the mappings changed while they were being checked, or Nexara could not read the cluster's node list (it then checks no node rather than guess which names are real). A node that was not checked is never shown as OK. Proxmox lists only the mappings the cluster's API token may see.

With `manage:cluster` (and `Mapping.Modify` on the cluster's token, as for creating one), the USB card offers:

- **New mapping** — a name, a description, and a first entry: a node and a device on it, by ID or by port.
- **Add node** — an entry for a node that has none, so a VM using the mapping can start there. When the node has the same device as the mapping's other entries, it is picked for you.
- **Replace device** — point an entry at different hardware on the same node, after a device was swapped or moved to another port.
- **Remove** — drop one node's entry. VMs using the mapping will not start on that node until it has an entry again. Removing a mapping's last entry deletes the mapping.
- **Edit description**, and **Delete**.

A USB mapping has at most one entry per node. Proxmox itself would store two, but it then refuses to start any VM using the mapping on that node, so the tab flags such a mapping, and until the extra entry is removed Nexara saves no other change to it. Nexara also writes device IDs in lowercase, which is how Proxmox compares them when a VM starts: a mapping made elsewhere with an uppercase ID is repaired the next time Nexara saves it — any change will do, or **Replace device** with the same device.

**PCI mappings.** The PCI card lists each mapping with its entries grouped by node — the device's address, its vendor:device ID (and its subsystem ID), and its IOMMU group — and shows when the mapping is set to use mediated devices or to allow live migration. A node may have several entries: they are alternatives, and a VM starting there takes the first of those devices that is not in use. Proxmox checks a node's entries together, and refuses to start a VM using the mapping on that node when any one of them fails, so the card shows the check once for each node. With `manage:cluster` it offers:

- **Add device** — a device of any node, including one that already has entries: the node gets one more alternative. The devices its entries already name, as a whole or as one of their functions, are not offered.
- **Replace device** — point an entry at different hardware on the same node, in the same place among the node's entries. Picking the device it already names brings the entry's IDs and IOMMU group up to date with what the node now reports.
- **Remove** — drop one entry. The node keeps its other entries; when it was the node's last, VMs using the mapping will not start there until it has one again, and removing the mapping's last entry deletes the mapping.
- **Edit description**, and **Delete**.

Nexara builds every entry it adds from what the node itself reports for the device, as **Add PCI Device** does, and writes back the mapping's other entries saying what they said: nothing typed ever becomes an entry. It does write every entry the same way — device IDs in lowercase and the IOMMU group as a plain number — so, as for USB, an entry Proxmox refuses for an uppercase ID or a group written as `07` is repaired by any save of the mapping, or by **Replace device** with the same device. The dialog asks before adding a device the node may need, as **Add PCI Device** does. The mapping's *mediated devices* flag must match every entry's device: while the mapping has other entries, the dialog refuses a device that does not match it — change the flag in Proxmox first — and a device replacing a mapping's only entry sets the flag to match, which the dialog says before you save. An entry that Nexara cannot read the way Proxmox does — one written by hand, say — is shown with the reason and can only be removed, since Nexara cannot save the mapping while it is there; removing it removes every such entry of the mapping at once. A mapping with no entries at all can only be deleted. The flags and the order of a node's entries are changed in Proxmox, and new mappings are created by **Add PCI Device**.

**Delete checks which VMs use the mapping first.** Proxmox deletes a mapping without looking, and a VM that still uses it will not start until a mapping of that name exists again. So the delete confirmation lists the VMs whose configuration names the mapping (a `usbN` or `hostpciN` of `mapping=<name>`), read live from each VM, and any VM it could not read — on a node that is offline, or that did not answer in time — which may use it too. The check needs `view:vm`, and it has limits worth knowing: it reads each VM's current configuration (pending changes included) but not its snapshots, so rolling a VM back to a snapshot taken while it used the mapping brings back its reference to the mapping; and it sees only the VMs the cluster's API token can see. The delete is on offer once the check has answered, and still is when it could not be done, with a warning. Each check reads every VM in the cluster, so it is limited to 10 a minute per user, one running at once per user and two per cluster, USB and PCI checks counted together; a check over a limit fails and says so. A user's newer check replaces their older one, so two windows — or a script — signed in as the same Nexara user can cancel each other's checks.

**A save can conflict.** Every change — deletes included — is checked against the mappings as the tab showed them. If anything changed in the meantime — in Proxmox or in another Nexara window — nothing is saved or deleted: the tab reloads the mappings and you check and try again. For a delete, Nexara makes that check itself just before asking Proxmox, whose delete has no such check: a delete confirmed as removing a mapping's only entry is refused if an entry was added since the tab loaded, though not one added in the instant between that check and the delete. Proxmox keeps each kind's mappings in one file — every USB mapping in one, every PCI mapping in another — and checks the whole file, so a change to a *different* mapping of the same kind also makes a save conflict.

**Who can attach a mapping.** Nexara reaches Proxmox with one API token, which can use every mapping in the cluster, so Nexara's own permission is the only gate: anyone who can manage a VM (`manage:vm`) can attach any existing mapping to any VM in that cluster: a USB device, a PCI device, or a VirtioFS directory. Proxmox checks `Mapping.Use` on each mapping for a user signed in to it; Nexara has no per-mapping permission, just as `manage:storage` covers every storage. Bear it in mind before giving a custom role `manage:vm`.

---

## VM Imports

Navigate to **Imports** from the sidebar to see imports and register sources for bringing existing VMs into Proxmox from ESXi/vCenter hosts or OVA/OVF appliances. Imports run as Proxmox tasks, so they show up in the task history and audit log like any other operation.

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

1. Click **Create** → **Import VM** in the top bar, right-click a cluster in the tree, or use the Ctrl+K command palette. The wizard's steps are Source, Inspect, Target, Customize and Review
2. Pick where the guest comes from:
   - **From import storage** — a VM on a registered ESXi source, or an appliance on import-enabled storage
   - **Download OVA from URL** — paste a link and click **Check URL** to probe it; optionally override the filename, pick the storage, click **Download to storage** and wait, then choose it under **Source guest / image**
   - **Upload OVA** — pick a `.ova` file from your machine and click **Upload to storage**
3. Review the guest in the customize step — settings are grouped into **OS & System**, **CPU & Memory**, **Network**, and **Identity & options**
4. Start the import and follow progress in the history table

Proxmox-side defaults that trip up manual imports are handled automatically: UEFI (OVMF) guests get an EFI vars disk created, imported disks attach on SATA so the firmware can boot them, and the CPU type defaults to `x86-64-v2-AES` (required for Windows 11 guests).

### Import History

The history table shows **Name**, **Target**, **Source**, **Status** (pending / running / completed / failed / cancelled), and **Created**. Cancelling a running import asks whether to **Also delete the partially created VM**.

### Permissions

Importing is gated by a dedicated VM-import permission (`manage:vm_import`). Three operations additionally require storage-management rights (`manage:storage`) because they exercise or change storage configuration: probing a download URL, downloading it to storage, and enabling import content on an existing storage. **Upload OVA** takes either permission.

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
   - **Associated Cluster** (optional) — decides who may see the server: bound to a cluster, that cluster's grants apply; standalone, instance-wide ones. Coverage does not read it
4. Click **Connect**, accept the certificate the same way as for a cluster, and confirm

### Managing Datastores

After adding a PBS server, Nexara syncs its datastores. For each datastore you can:

- View **usage statistics** and storage capacity
- Browse **snapshots**
- **Protect/unprotect** snapshots
- **Delete** snapshots
- Trigger **garbage collection**
- **Prune** old snapshots based on retention rules
- View **datastore metrics** over time

### Sync, Verify and Coverage

The Backup page's top tabs are **Proxmox Backup Server**, **Veeam** and **Coverage**. Under a PBS server are **Overview**, **Snapshots**, **Schedules**, **Replication**, **Verification**, **Tasks** and **History**:

- **Replication** — the PBS server's sync jobs (pulling datastores from a remote PBS). List them, and run one on demand
- **Verification** — the PBS server's verify jobs, which re-check stored chunks for corruption. Same list-and-run treatment
- **Tasks** — the PBS server's own task log, so a stuck garbage collection or verify is visible without leaving Nexara
- **Coverage**, at the top level — the one that matters at review time. Four cards count **Backup targets**, **Protected (<24h)**, **Stale (>24h)** and **No Backup**, over a searchable per-guest table showing how long ago each guest was last backed up. A guest that has never been backed up reads "Never"

  Coverage spans **both providers**: freshness is taken from the newest restore point either PBS or Veeam holds, so a guest protected only by Veeam is no longer reported as unprotected. PBS counts only through a `pbs` storage in the cluster **whose ID is the same as the datastore's name**; a storage with another ID leaves its guests reading **No Backup**. Each row also carries an **eligibility** verdict — Veeam's own worker appliances and the VBR server are not backup targets, and counting them as unprotected is how a coverage view teaches operators to ignore it. LXC containers are reported as backup targets but not Veeam-capable, since Veeam cannot back them up and PBS can.

  A viewer holding `view:backup` but not `view:veeam` sees exactly the report that existed before Veeam support: no restore-point data and no eligibility refinement, rather than a partial answer

### Backup Jobs

1. Navigate to **Backup** from the sidebar and select a PBS server. The jobs are Proxmox's own, per cluster, but this tab is only reached through a registered PBS server that answers
2. Open the **Schedules** tab (pick the cluster if you have more than one) and click **Add Schedule**
3. Configure:
   - **Schedule** — pick **Hourly**, **Daily**, **Weekly** or **Monthly** and the time; the builder writes the Proxmox calendar event for you and shows it in plain English. Switch to **Custom** to type a calendar event directly (e.g. `mon..fri 02:00`). Note that these are systemd calendar events, *not* cron — `0 2 * * *` is not valid here
   - **Storage** — the PVE storage ID the backup writes to (`local`, or a PBS-backed storage such as `datastore01`)
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

The restore reads the snapshot through the Proxmox storage on the target cluster that is set up for the snapshot's PBS server and datastore (Datacenter > Storage > Proxmox Backup Server). The storage must be enabled, available on the node the guest is restored onto, have no namespace, and name the snapshot's datastore. Nexara ties it to the PBS server by the certificate fingerprint when both carry one (two different fingerprints rule a storage out), and otherwise by address and port, 8007 where none is set; the same host on another port is another server. A PBS that Nexara reaches by an IP address and Proxmox by a name, with no fingerprint to say so, is used when its storage is the only candidate left and no other PBS server Nexara knows is tied to it; the restore's audit entry then records `matched_by: only_candidate`. If two storages are tied to the server the restore is refused, and so is one for which no storage can be used; either way the answer names the PBS storages it found, rather than reading from a PBS nobody picked. **Overwrite existing VM** stops the guest first, once that storage is found, and the stop is a task of its own in the guest's History. A restore needs permission to manage backups on the target cluster and to view backups on the PBS server (on the cluster it is attached to, or instance-wide when it is standalone); a server you may not view is reported as not found.

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
   - **Username** — a VBR admin account, often `DOMAIN\user`. The backslash is significant
   - **Password** — encrypted at rest with AES-256-GCM, like every other stored credential
4. Click **Continue** to fetch the certificate, accept the fingerprint the same way as for a cluster, then click **Add Server**

Nexara performs a real logon before saving, so **a failed connection is a failed create**. The product version, API revision and licence edition it negotiates are recorded on the server row and shown in the UI.

The **Test** action on an already-registered server re-runs that logon and reports the same figures, but deliberately **persists nothing** — diagnosing a broken server should not rewrite its row. It needs `manage:veeam`, not just `view`, because it makes an outbound authenticated connection with stored admin credentials.

Registration schedules the first inventory sync rather than blocking on one. Until it finishes the server shows as **syncing** rather than reporting an empty inventory as though it were a finished one.

Changing a server's base URL later requires re-entering the password, for the same reason it does on a cluster.

### Mapping Platforms to Clusters

**Do this first — nothing appears on the cluster pages until you do.**

One Veeam server can protect several Proxmox clusters. Everything Veeam returns is tagged with a `platformId` identifying one Proxmox connection, and that is the *only* cluster discriminator its REST API exposes. Nexara asks you to confirm the mapping once.

On an install with a single active cluster Nexara maps it for you. Otherwise open the server's **Clusters** tab — it carries an amber count of platforms still unmapped — where changing a mapping needs a global `manage:veeam`. Each discovered platform is listed with a label taken from the licence workload hostname, usually the Proxmox cluster name. Treat that as a hint, not proof, and pick the actual cluster.

Until a platform is mapped, its rows are not attributable to any cluster, so they are visible only to a holder of **global** `view:veeam`, and coverage reports that cluster's guests using PBS data alone. That is deliberate: guessing the mapping from a hostname string would show one tenant's backup inventory to an operator scoped to a different cluster.

Removing a cluster from Nexara unmaps its platform rather than deleting the collected Veeam history — re-add the cluster and re-confirm.

### Coverage, Orphans and Manual Pins

Backup objects are correlated to guests on the **SMBIOS UUID**, which Veeam happens to use as its `objectId`. That makes the match deterministic rather than a name guess — and a name match would silently report a rebuilt guest as protected by a backup of the machine it replaced. Only a guest recorded with no SMBIOS UUID falls back to its name, and coverage marks that **name match**.

- **Orphans** (the server's **Orphaned** tab) are backup objects whose platform *is* mapped but which match no guest on it: a deleted VM, a template whose name was reused under a new UUID, a host rebuilt in place. They consume repository space for machines that no longer exist in the form that was backed up, and the Veeam console does not call them out.
- **Manual pins** are the escape hatch for what automatic resolution cannot know — a guest whose SMBIOS UUID changed, or an orphan you recognise. Pin the object to a guest (**Map to this guest** on an **Orphaned** row) and no sync overwrites it.

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

## Power (UPS monitoring)

Navigate to **Power** from the sidebar to watch the UPSes your Network UPS Tools (NUT) servers report: whether each one is on mains or on battery, how much charge and runtime it has left, its load, and a history of power events. Nexara reads upsd over its network protocol. It sends a UPS command or changes a setting only when someone runs one from a UPS's page, through an account you give it (see [UPS Commands and Settings](#ups-commands-and-settings)). It never logs in as a monitor (NUT's `LOGIN`) and never asks for a forced shutdown (`FSD`), so reading cannot change what your existing `upsmon` setup does. A setting changed from Nexara can: the battery-low point is when `upsmon` shuts machines down.

### Before You Add a Server

- **upsd must be reachable from the Nexara container**, on TCP 3493 unless you changed the port. upsd listens on localhost only until told otherwise: add a `LISTEN` line for an address Nexara can reach to `upsd.conf` and restart upsd.
- **Some NAS appliances run upsd for their attached UPS and answer only an allow-list of client addresses.** Add the address Nexara connects from to that list.
- **No account is needed to read.** upsd serves status to any client that can connect, so leave the username and password empty unless you want to run UPS commands from Nexara. For that, give it a dedicated `upsd.users` entry with **no `upsmon` line**, so that account can never act as a monitor (see [UPS Commands and Settings](#ups-commands-and-settings)).

### Adding a NUT Server

**Add NUT server**, at the top of the Power page (needs `manage:ups`). Give it a name, the host (a hostname or IP address, with no scheme or port), the port, and the **Connection security**:

- **Plain TCP** — the usual case for upsd on a LAN.
- **STARTTLS (required)** — the connection is upgraded before anything else is sent. A server that does not offer STARTTLS is an error, never a silent fall-back to plain text. Verify its certificate against the system CA pool, or pin its SHA-256 fingerprint.

**Test** connects before you save and lists the UPSes the server reports; saving tests again, so a typo in the address is a failed save rather than a server that never reads. Three things ask for confirmation instead of refusing:

- a private address (a NUT server on your LAN is the usual case);
- a password that would cross the network in plain text;
- a password sent over TLS without a verified or pinned certificate, which would reach whoever answers on that address.

Changing the host or port of a saved server requires re-entering its password: a stored password is only ever sent to the address it was saved for. So does making the connection less protected (turning TLS off, or no longer verifying the certificate) or pinning a different certificate: a stored password is only ever sent as protected as when it was saved, since the account can run UPS commands.

### What the Power Page Shows

- **Devices** — a card per UPS with its status, charge, runtime, load, power and input voltage. A power figure marked **≈** is estimated from load × the UPS's rated power, for a UPS that does not measure it. While any UPS is on battery, a banner names it and the shortest runtime left.
- **NUT servers** — each server's upsd and protocol versions, when it was last read, and the last error, with a **Test** button that checks a saved server's connection.
- **Power events** — outages, low battery, overloads, bypass, voltage correction, a battery that needs replacing, lost contact, stale driver data, and self-test results. Expand a row for the lowest charge and runtime and the peak load seen during it.

Click a UPS for its detail page: charts of charge, runtime, load and input voltage over 1 hour to 7 days (the input's lowest and highest reading in each bucket show sags and surges, and shaded spans mark time on battery), every variable upsd reports with its description, the UPS's own events, its maintenance windows, and its **History** (see A UPS's History). The search palette (Ctrl+K) finds a UPS by its name, description, model or NUT server, and the Power page by "power", "ups" or "nut", for a holder of a global `view:ups`.

**A UPS Nexara cannot read shows that, never its last state.** **Unreachable** means upsd did not answer (**Rolling update** instead, when its NUT server runs on a node a rolling update is upgrading or rebooting and you may see that node; see UPS Alerts), **Driver down** that upsd answered but its driver for that UPS is not connected, and **Data stale** that the driver stopped updating. A reading older than three poll intervals (and at least two minutes), while upsd last answered, shows **No recent reading**: the poller stopped reading it. A server whose polling is turned off (**Poll this server**, in its Edit dialog) shows **Polling off**, keeps its history, and is left out of the alerts.

An event's end that Nexara did not see is marked on its duration. **≤** means contact was lost and the condition had cleared by the next reading, so it lasted at most that long. **≥** means Nexara stopped watching while it was still going: polling was turned off, or upsd stopped listing the UPS.

### What Each UPS Powers

Nexara works out which nodes a UPS powers from who is logged in to it. Every `upsmon` that watches a UPS logs in to that UPS's upsd, and upsd lists those logins by address (NUT's `LIST CLIENT`, which needs no account). Once a minute, while the UPS is on mains, Nexara reads that list and matches each address to a node's cluster address or one of its interfaces' IPv4 addresses. A match adds the node to the UPS's **Powers** tab as found from its login. Found nodes are never removed automatically: a node that stops logging in still draws power from the UPS, and that is what the check below is for.

Add by hand what Nexara cannot find, with **Add** on the UPS's **Powers** tab: a node, or a label for something that is not a node, such as a switch or a NAS. Adding a node needs `manage:ups` and `view:node` on its cluster. **Remove** takes a node off for good: its next login does not add it back, and only adding it by hand again restores it.

A NUT server that runs on a node, with the UPS on its USB port, has its own `upsmon` log in over loopback, and that login counts for the node it runs on. Nexara knows that node when the server's address is one of the node's. Otherwise set it under **Where … runs** on the Powers tab, which needs `manage:ups` and a global `view:node`, since a server's node may be in any cluster.

### Shutdown Protection

Each node a UPS powers gets a verdict on whether it would shut down before the battery runs out:

| Verdict | Meaning |
|---------|---------|
| **Protected** | A `upsmon` on the node is logged in to this UPS |
| **Not protected** | None is, so in a long outage the node loses power without shutting down |
| **Checked elsewhere** | Marked **Protected another way**: the node shuts down some way Nexara cannot see |
| **Node offline** | Nexara sees the node offline (rebooting, or still off after an outage), so there is nothing to judge |
| **Not checked yet** | Nexara has not listed this UPS's logins on mains yet, cannot tell this node's logins from another node's, or has no recent status for the node |

- A node that was logged in reads **Not protected** only after it is missing from two listings in a row, so a `upsmon` restart does not raise it.
- **Logins are listed only on mains.** Nodes log out on purpose as they shut down, so during an outage the logins hold where they were until the UPS is back on mains; a node that has shut down still reads **Node offline**.
- **Not protected** shows on the health pill, naming the node and its cluster, on the Power page's **Shutdown protection** card and its outage banner, on the UPS's Powers tab, on the node's **Power** card, and on the [topology map](#topology-map)'s Power overlay. The outage banner leaves out a node that another UPS, still on mains, also powers.

**Fixing a node that is not protected.** The node's page has a **Power** card, for `view:ups` holders, with the UPSes that power it, their charge, runtime and load, and the verdict. For a node that is not protected it gives the two lines to add on the node:

```
# /etc/nut/nut.conf
MODE=netclient

# /etc/nut/upsmon.conf
MONITOR ups01@192.0.2.40 1 <user> <password> secondary
```

Install NUT's client on the node (`apt install nut-client`), take an account from the NUT server's `upsd.users` that has an `upsmon secondary` line, and restart `nut-monitor`. NUT before 2.8 spells `secondary` as `slave`. Use the address the node reaches upsd at, which is not always the one Nexara uses: when Nexara reaches upsd over loopback, the card leaves the address for you to fill in. Within a minute of the login the node reads **Protected**.

During an outage, a node that is not protected and that no UPS still on mains powers gets **Shut down … now** on its Power card, for `manage:node` holders. It is the node's own Shutdown, behind the same confirmation.

**upsd behind NAT, a proxy or an ingress.** upsd then sees every login from one address, the proxy's, so Nexara cannot tell which node a login came from: nothing is found, and nodes added by hand read **Not protected** even with `upsmon` running. Let upsd see the nodes' own addresses (for a Docker Swarm service, publish its port with `mode: host` rather than through the ingress), or switch on **Protected another way** for each node, on the Powers tab or the node's Power card. The same switch covers a node shut down by other software, such as a UPS vendor's own agent. It needs `manage:ups`.

**upsmon over IPv6.** Interfaces are matched by their IPv4 addresses, so a login over IPv6 is found only when it comes from the node's cluster address. Point the node's `MONITOR` line at upsd's IPv4 address, or switch on **Protected another way** for it.

### UPS Commands and Settings

A UPS's page has a **Commands** tab with the instant commands upsd offers for it (NUT's `INSTCMD`), and its **Variables** tab marks the variables upsd lets clients set (`SET VAR`), with a **Set** button on those you may change. Nexara lists both every 10 minutes, and again when the driver changes.

**The account.** upsd runs a command or sets a variable only for an account in its `upsd.users`, and checks the account only when a command runs. Add one for Nexara, and enter it on the NUT server in Nexara:

```
[nexara]
  password = <pick one>
  actions = SET
  instcmds = ALL
```

`actions = SET` is for variables and `instcmds` for commands. To keep Nexara from ever cutting a UPS's power, list only the commands you want (one `instcmds = <command>` line each) instead of `ALL`, and leave out `actions = SET`, since upsd cannot limit SET to some variables and most of them can change the power (below); upsd enforces both itself. Leave out `upsmon`. The NUT servers table's Access column reads **Commands** once upsd has taken a command from the account; until then the first command is its test.

**What a command can do.** Commands are grouped by what they can do to what the UPS powers, and each group asks for a different confirmation:

| Group | Commands | To run one |
|-------|----------|------------|
| Everyday | the beeper, the front-panel test, and every `*.stop` (stopping a battery test, cancelling a shutdown in progress) | A click |
| Tests | battery tests (`test.battery.*`) and calibration: they drain the battery, a deep test down to low | A confirmation |
| Powers on | `load.on*`, `outlet.*.load.on*` | A confirmation |
| Driver | `driver.reload*` | A confirmation |
| Cuts power | `load.off*`, `outlet.*.load.off*`, `shutdown.*` but `shutdown.stop`, `bypass.start`, `driver.killpower`, and **any command Nexara does not recognise** | The UPS's name typed |

A command that cuts power also takes `manage:node` on every cluster with a node the UPS powers or has powered (one removed from its Powers tab still counts), and on the cluster of the node its NUT server runs on; when Nexara knows none of these, `manage:node` everywhere. Its dialog lists those nodes with the guests running on each, and warns when the NUT server runs on one of them: cutting the power stops it too, and Nexara may not hear how the command went.

**Settings.** A variable is set only to a value upsd will take: one of the values it lists for an ENUM, a number within a range it lists, at most its `STRING:n` characters. Most variables can change the power the UPS supplies or when `upsmon` shuts machines down (`battery.charge.low` set to 0 means no low-battery warning before the battery is flat), so setting any of them is gated as cutting power is: `execute:ups` too, `manage:node` on the clusters the UPS powers, and a confirmation; `driver.flag.allow_killpower` takes the UPS's name typed. Only those that describe the UPS (`device.contact`, `device.description`, `device.location`, `ups.id`, `battery.date`, `battery.mfr.date`), the beeper's status and the driver's debug level need `manage:ups` alone. upsd's own placeholder lists (one value, such as snmp-ups's `battery.date`) are not offered for editing.

**Outcomes.** upsd answering a command only means it passed it to the driver. On NUT 2.8 Nexara asks for the driver's own answer for up to a minute, and the run reads **succeeded**, **failed** (with what the driver or upsd said), or **unconfirmed** when no answer came; NUT 2.7 cannot say, so its runs read unconfirmed. A variable that took is read back. The Commands tab lists the runs with who asked for each. The audit log records each command and variable by name, never a variable's value, as `ups_command_sent` (upsd took it) or `ups_command_failed` (upsd refused it, or could not be reached); the driver's later answer is in the run list only.

**Limits.** Running commands and setting variables is limited to 30 a minute per user. Nexara waits on at most 16 drivers' answers at once, 4 on one NUT server; past that a command or setting is refused (429) until one finishes, except a cancel (`*.stop`), which always goes out, without waiting for the driver's answer, and reads unconfirmed.

### Polling and History

Every enabled server is read every **10 seconds**, tuned with `NUT_POLL_INTERVAL` (minimum `2s`). `0` stops polling every server, and within two minutes every UPS reads as unreachable and counts toward `ups_unreachable`; to stop watching one server, turn its polling off instead. A server that stops answering is retried after 10 seconds, doubling up to 5 minutes. Readings are kept at full resolution for 30 days; 5-minute and hourly rollups, which the longer chart ranges read, are kept without a limit.

### Permissions

| Permission | Allows | Granted to |
|------------|--------|------------|
| `view:ups` | The Power page, UPS issues in the health pill, the UPS dashboard widget, a node's Power card, the topology's Power overlay, UPS search results; with `view:alert`, the UPS alerts; with `manage:alert`, the UPS alert rules | Viewer, Operator, Admin |
| `manage:ups` | Adding, editing and testing NUT servers; adding and removing what a UPS powers, and marking a node protected another way (with `view:node` on the node's cluster); setting the node a NUT server runs on (with a global `view:node`); setting a UPS's variables (with `execute:ups` and `manage:node` too for one that can change its power) | Operator, Admin |
| `execute:ups` | Running a UPS's instant commands (with `manage:node` too for one that cuts its power) | Operator, Admin |
| `delete:ups` | Removing a NUT server and the UPS history collected from it | Operator, Admin |

All four must be held install-wide: the UPS routes refuse a grant held on one cluster, so the Power page and its sidebar link appear only with a global `view:ups`. Custom roles were given the same `view`, `manage` and `delete` actions on `ups` as they hold on `pbs` when UPS monitoring arrived, and no `execute:ups`. Built-in roles cannot be edited, so every Operator can run every UPS command; to keep someone from that, give them a custom role.

### UPS Alerts

Six metrics read the UPS readings and are configured like any other alert rule. A NUT server belongs to no cluster, so a rule has one of two scopes: **Global (all UPS)** judges every UPS and its alert names the worst one, as `ups@server`; **One UPS** judges the UPS it names. Either needs a global `manage:alert` and a global `view:ups`, since its alerts name a UPS and its reading; the form offers the UPS metrics only with that view, and turning such a rule off needs only `manage:alert`. Their alerts are shown only to holders of a global `view:ups`: without it they are left out of the Alerts list, the dead-letter queue and their counts, and opening, acknowledging or resolving one answers as if it did not exist. Removing a NUT server removes the rules that name one of its UPSes, with their alerts.

| Metric | Unit | Meaning |
|--------|------|---------|
| `ups_on_battery` | count | UPSes on battery |
| `ups_battery_charge` | % | The lowest battery charge |
| `ups_runtime` | **minutes** | The shortest runtime left |
| `ups_load` | % | The highest load |
| `ups_unreachable` | count | UPSes Nexara cannot read: unreachable, driver down, stale data, or a reading too old |
| `ups_replace_battery` | count | UPSes reporting a battery that needs replacing |

- **A UPS Nexara cannot read holds a rule only if it could tip it.** Rules are judged on the UPSes Nexara can read. If one it cannot read was last seen meeting the condition (the UPS that was on battery went silent), the rule neither fires nor resolves until it reads again; otherwise it is judged without that UPS, and the alert's text says how many were not read. So a broken `ups.conf` entry cannot silence the others. `ups_unreachable` is the rule that tells you a UPS cannot be read, so pair the others with it.
- **A server with polling turned off is left out** of every rule, `ups_unreachable` included.
- **A UPS under a maintenance window** (see Maintenance Windows) drops out of the global rules, and the rules naming it hold: they neither fire nor resolve while the window is open. A global rule that only the UPS under a window would trip holds too, so opening a window during an outage does not report it over.
- **A rolling update that reboots a NUT server's node** — upgrading, rebooting or health-checking it, in a running job — reads that server's UPSes as down for the update: `ups_unreachable` leaves them out, and holds where they alone would trip it (a rule naming one holds), so an alert neither fires nor resolves for them. For a viewer who may see the server's host node (`view:node` on its cluster), the health pill says "NUT server rebooting for a rolling update" instead of "UPS unreachable" and the UPS reads **Rolling update**; anyone else sees it unreachable, since that would say a rolling update is running there. That lasts until Nexara has read the UPS again, at most ten minutes after the node is back, since the poller retries a server it lost on a backoff of up to five minutes. It needs the server's host node to be known (see What Each UPS Powers); a paused job's node is not counted, so a real failure during a long pause alerts.
- **A forced shutdown on mains is not an outage.** `ups_on_battery` counts UPSes on battery, so the FSD that `upsmon -c fsd` sets during a shutdown test does not trip it.
- The form starts charge and runtime rules at **<**, since those watch a level running down, and the on-battery, charge and runtime rules with **no cooldown**: the cooldown runs from when the last alert resolved, and an hour of it would swallow the second outage of a storm.
- Rules are evaluated every 60 seconds, so an on-battery alert arrives within about a minute of the switch, plus the rule's Duration.


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
| `disabled` | **Default.** The automatic pass does nothing |
| `report` | The automatic pass detects and reports versions, and never writes into a guest |
| `staged` | The automatic pass detects, and stages updates on guests that need them |

The mode governs the automatic pass only. The actions on a guest's own page work in any mode.

Also on the cluster config:

- **Target version** — by default it follows the virtio-win ISO target, then the stable release; or pin the fleet to a specific version
- **Snapshot before an immediate update** — take a snapshot before **Update now**, never before staging (off by default; not every storage type supports snapshots)
- **Maximum guests updating at once** — how many guests the automatic pass may stage at once (default 5)

Per guest, on its **Guest Tools** tab, you can set **Exclude from automatic updates** (never touch this one, whatever the cluster mode says, which also blocks staging by hand) and a **Pinned version**. The API also takes a **note**, so the reason an exclusion exists outlives whoever set it.

### The Fleet View

The guest list shows each Windows guest's installed guest-tools version alongside the target, and a badge: **Staging**, **Staged for next boot**, **Installing**, **Failed**, **Updated - reboot to finish**, **Update available**, **Up to date**, **Unknown** or **Excluded**. The QEMU guest agent's own version and whether its service is running are on the guest's **Guest Tools** card, not the fleet table.

The fleet table's actions are icon buttons. On the guest's **Guest Tools** tab they are **Re-check**, which re-reads the guest now; **Stage update at next boot**; **Update now**; and **Cancel staged update**, which removes a staged task that has not fired yet. Detection is a read (`view:guest_tools`); changing policy needs `manage:guest_tools`; staging or cancelling needs `execute:guest_tools`.

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

What is really being scheduled is not the upstream lookup — that is one small request — but the **~840 MiB fetch** a lookup can dispatch, which is why the timed options exist: point them at a maintenance window.

For the timed options, set the **time zone** as well. Left on `Server time` the hour is read in the container's own zone, which is almost always UTC.

**Check now** is available while **Download automatically** is on — it is the manual
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

Navigate to **Alerts** from the sidebar. Its tabs are **Alert History**, **Alert Rules**, **Channels** and, with `view:notification_dlq`, **Dead-letter queue**.

### Alert Rules

1. Click the **Alert Rules** tab
2. Click **Create Rule**
3. Configure:
   - **Name** — descriptive rule name
   - **Scope** — what the rule judges, which depends on the metric. For CPU, memory, disk and network: **Cluster** (each node of the cluster in turn, with one alert per node; guests are not judged), **Node** (one specific node), or **VM** (one specific guest). VM-scoped rules are pinned to the guest's VMID within the cluster, so they keep working across migrations and inventory re-syncs. The Veeam and UPS metrics add **Global (all Veeam servers)**, **Global (all UPS)** and **One UPS**.
   - **Metric** — one of 19. These seven are the general ones; the others are under [Veeam Alerts](#veeam-alerts), [Proxmox Task Alerts](#proxmox-task-alerts) and [UPS Alerts](#ups-alerts):

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

     **Memory Usage** on a VM needs the balloon driver's statistics. A VM without them is measured only from the host, which sits near 100% for most VMs, so such a reading is not judged and the rule does not fire.

     **Disk Read/Write** and **Network In/Out** are rates in bytes/s, measured between samples. Under load Proxmox at times leaves a running VM's disk counters unreported (it reads them as 0). Nexara then keeps the last reported figures, for up to six hours, and marks the sample — for a node, any sample in which one of its guests was unreported or a guest list could not be read. A disk rule skips marked samples, holding (neither firing nor resolving) while the newest sample is one, and measures the rate across them once a reported sample follows. A counter Proxmox never reports while the guest runs (network on a VM without a NIC, say) is never judged, so a `<` rule on it never fires. When a counter falls — a guest restarted, stopped or moved off the node, or a node's guest list could not be read — the rule holds until the counter is back at its earlier level or that sample is older than the rule's duration plus ten minutes. A dip Nexara could not mark — one already under way when it started, or one past six hours — can still read as a burst, so give node- and cluster-scope disk rules a duration rather than `0`.
   - **Condition** — threshold and comparison (e.g., CPU > 90%)
   - **Duration** — how long the condition must persist before firing; `0` fires on the first breaching sample. The CPU, memory, disk and network metrics serve it twice, because a tick only matches once every sample in the window meets the condition and the pending alert then waits again: 300 fires after about ten minutes
   - **Severity** — info, warning, critical
   - **Cooldown** — minimum time between re-fires, measured from when the previous alert resolved
4. Add an **Escalation Chain** — this is how the rule notifies; a rule with an empty chain never sends anything
5. Add a custom message template (optional)
6. Click **Create Rule**

A saved rule can be turned off or on from its row, or deleted, which also deletes every alert it has raised. There is no form for editing one: delete it and create it again, or send the change to `PUT /api/v1/alert-rules/:id`.

### Notification Channels

1. Click the **Channels** tab
2. Click **Add Channel**
3. Select channel type and configure:

| Type | Configuration |
|------|--------------|
| **Email (SMTP)** | Host, port, username, password, from/to addresses, **Use STARTTLS** (turning it off is accepted only for a loopback address), optional subject template |
| **Slack** | Webhook URL, channel, username |
| **Discord** | Webhook URL, username |
| **Microsoft Teams** | Webhook URL |
| **Telegram** | Bot token, chat ID |
| **Generic Webhook** | URL, method (POST/PUT/PATCH), custom headers, optional body template |
| **PagerDuty** | Integration/Routing Key. Its severity mapping can be set only through the API |

4. Click **Create**
5. Click **Test** on the channel's row to send a test notification. A failed test says only to check the server logs, but the reason is not logged there. To see it, let a real notification fail and read its dead-letter entry

A Slack, Discord, Teams or Generic Webhook URL that resolves to a private or loopback address is refused when Nexara sends to it, with no override, so a webhook receiver on your LAN cannot be used. An SMTP host that resolves to a private address is refused too: with **Use STARTTLS** on, any private or loopback address, and with it off, anything but loopback. So a mail relay on your LAN cannot send alerts or report email unless it is reached through a loopback address.

> **Known issue: don't use a channel's Edit button.** The dialog shows only the name and **Enabled**, but saving it also replaces the channel's stored settings with empty ones, so the channel stops sending. To change a channel, send only the fields you are changing to `PUT /api/v1/notification-channels/:id`; leaving out `config` keeps the stored settings, and sending it replaces them all, since they are never shown again. Deleting a channel and creating it again gives it a new id: every alert rule step that named the old one silently sends nothing, with no dead-letter entry, and the channel drops out of CVE notifications and report schedules.

### Escalation Chains

The escalation chain is an ordered list of steps, each naming **one** notification channel and a **delay in minutes**. It is the rule's entire notification path — there is no separate "channels" field.

1. **Step 1** — dispatched the moment the alert fires. Its delay is ignored
2. **Step 2 and later** — dispatched once the alert has been firing for that step's delay, counted **from when the alert fired** (not from the previous step). Give step 2 `15` and step 3 `60` and they land 15 and 60 minutes after the fire, respectively

Escalation only advances while the alert is still firing **and** unacknowledged, and while its rule is still enabled on the same metric — acknowledging the alert, turning the rule off or changing its metric stops the chain where it is. Resolution notifications go to whichever step the alert had reached. An alert whose condition clears before the rule's duration is up never fired, so it resolves without a notification.

### Maintenance Windows

Maintenance windows suppress alert evaluation for a cluster (or a single node in it), or for one UPS, during a planned outage. A **UPS's windows** are on its detail page, in the Overview's **Maintenance windows** card: open one for a battery swap, say, and the UPS drops out of the global UPS rules while the rules naming it hold (see UPS Alerts). They need `view:ups` with `view:maintenance_window` to see them and with `manage:maintenance_window` to change them, all held install-wide (a cluster's `maintenance_window` grant does not show the card), and each change is in the UPS's History. A cluster's windows are **API-only** — there is no page for them yet:

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
- **Acknowledge** — mark an alert as seen (stops escalation). While its condition holds the rule raises no new alert, and once the condition clears the alert resolves on its own and sends its resolution notification. A rule keeps one alert at a time (a cluster rule judged node by node, one per node), so acknowledging the alert of a rule that judges every UPS, or a cluster's tasks or Veeam jobs, quiets that rule for all of them: give a UPS a rule of its own to hear about it separately
- **Resolve** — mark an alert as resolved
- Filter by state and severity. The tab lists the newest 50 alerts; `GET /api/v1/alerts` also takes a cluster, a limit and an offset

### Failed Notifications (Dead-letter Queue)

An alert rule's notification that cannot be delivered is not lost. Each dispatch goes through three layers — a per-channel rate limiter, three retry attempts with backoff, and finally the dead-letter queue — so a dead Slack endpoint or a flapping rule leaves a durable record instead of silence.

This covers alert rules only. A step whose channel has been disabled or deleted is logged and leaves no entry, and CVE, rolling update and report notifications are sent once: a failure there is in the server log and nowhere else.

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

1. Nexara reads each node's **pending updates** from Proxmox. A package with no update pending is not checked, so a score of 100 means no known CVE among the pending upgrades, not that nothing installed has one
2. Each pending package is checked against the Debian Security Tracker, and its changelog, which Proxmox serves, is searched for CVE IDs the tracker does not list. The tracker files CVEs under source packages, so a package from Debian is looked up under its source, which the tracker's own package list gives, or failing that the package's changelog (`libssl3` under `openssl`). A Proxmox-built package is looked up under its own name, and Debian's kernel source is skipped, since a Proxmox node runs Proxmox's kernel. A Debian update is a security update when its changelog shows an upload to Debian's security archive (`bookworm-security`, `trixie-security`) newer than the installed version: Proxmox's update list cannot say, since it names that archive `Debian` like the main one. A security update of a package the tracker does not list appears as `UNKNOWN-<package>`, and every security update appears as `SEC-<package>` when the tracker cannot be reached
3. Results are aggregated into a security posture score per cluster

### Manual Scan

1. Select a cluster from the filter
2. Click **Scan Now**
3. Results appear when the scan completes (typically 30-60 seconds)

### Automated Scanning

Each cluster is scanned automatically on its own interval — **every 24 hours** unless you change it. The scheduler checks every minute which clusters are due, so a scan starts within a minute of its interval, counted from the start of the cluster's last scan, a manual one included.

1. Open the **Scan Schedule** card on the Security page
2. Toggle **Automatic scanning** on or off, and pick an interval — hourly, or every 6, 12, 24 or 48 hours
3. Changes save immediately

### Security Posture

The posture card shows:
- **Score** — overall security rating (0-100), with the nodes scanned and the vulnerabilities found
- **Critical/High/Medium/Low/Unknown** — vulnerability counts by severity

Beside it, **Action Required (SSVC)** counts the latest scan's CVEs by the bands described under [CVE Notifications](#cve-notifications).

### How Severity Is Decided

Nexara does not take the Debian tracker's rating at face value. Each CVE gets a 0–10 **risk score** combining three signals:

- **CVSS base** — the Debian Security Tracker publishes no CVSS score, so Nexara substitutes the midpoint of the urgency band it *did* publish (critical → 9.5, high → 7.5, medium → 5.0, low → 2.5)
- **EPSS** — FIRST's empirical probability of exploitation in the next 30 days
- **KEV** — CISA's binary "actively exploited in the wild" flag, refreshed hourly

A CVE on the KEV list is floored into the **critical** bucket regardless of its CVSS, because "actively exploited right now" outranks any paper score. A top-decile EPSS (≥ 0.9) is floored into **high**. Everything else grades by severity × probability, so a CVSS-9 with no public exploit lands low rather than screaming.

The counts on the posture card are these derived buckets — which is why a CVE's severity here can differ from the Debian tracker's. The tracker rates the flaw; Nexara rates your exposure.

The **actively exploited** callout on the posture card opens a per-CVE list: each CVE ID links to its NIST NVD page, and the flame badge beside it links to CISA's KEV catalog entry with the required action and due date.

### CVE Notifications

The **CVE Notifications** card wires scan results into the same notification channels alerts use. Tick **Enable notifications**, then choose which SSVC-style bands to notify on:

- **Act** — actively exploited (KEV) or high-likelihood (EPSS ≥ 0.5 with CVSS ≥ 7)
- **Attend** — moderate likelihood (EPSS ≥ 0.1) or critical CVSS

Tick one or more **Channels** — the same set serves whichever bands you enabled — set **Re-notify after** to bound repeats, and click **Save**. Any change that leaves at least one CVE notifies immediately; there is no all-clear message. The cooldown only gates re-notification while the same set is still present.

### Reviewing Vulnerabilities

In **Scan History**, expanding a scan shows its **Node Results**, and the **›** in the scan's own row opens that scan's vulnerabilities across its nodes, highest **Score** first: **CVE ID**, **Action**, **Risk**, **Score**, **EPSS**, **Package**, **Installed**, **Fixed In** and **Description**, filtered by **Actively Exploited** and by the severity the **Risk** column shows.

**Fixed In** is the version that fixes the CVE in the node's Debian release, as the tracker gives it, or the pending version when the changelog entries the upgrade brings name the CVE. It reads `-` for a CVE Debian has not fixed in that release, which the pending upgrade does not fix either, and when the node's release could not be read.

A node whose changelogs Proxmox could not serve still reads completed, and its row in **Node Results** names the packages: CVEs named only in those changelogs are missing from the result. Nexara's log gives the first reason Proxmox returned.

### How Long Scans Are Kept

Nexara keeps each cluster's 20 newest scans, the number **Scan History** lists, and once an hour deletes older ones with their node results and vulnerabilities. Two kinds of older scan stay, however old: the latest completed scan, which the posture card reads, so a run of failed scans never sets the card back to a score of 100 with no findings; and a scan still running. On an hourly schedule the 20 cover less than a day, on the default daily one 20 days. For a longer record, schedule the **Patch status** report, which keeps each scheduled run for 90 days.

---

## Rolling Updates

Navigate to **Security > Rolling Updates** tab.

### Prerequisites

1. **SSH Credentials** — required for automated upgrades, and on this same tab for the selected cluster (`manage:ssh_credentials`)
   - Click **Configure SSH** and enter the username, port and **Auth Type**: **Password** (the default) or **SSH Key**
   - Saving opens **Pin SSH host keys**, which pins each node's host key. A job fails a node whose host key is not pinned, and by then the node has already been drained
   - **Test Connection** tests one node you choose, with **Trust & Pin** for its key

### Creating an Update Job

1. Click **New Rolling Update**
2. Work through the wizard's three steps — **Select Nodes**, **HA Constraint Check**, **Configure Update**:
   - **Nodes** — select which nodes to update (or all)
   - **Parallelism (max nodes updated at once)** — default 1, set on the **Select Nodes** step because the pre-flight that follows checks capacity for that many nodes drained at once
   - **Conflict Policy** — offered on the **HA Constraint Check** step when the pre-flight finds conflicts: **Warn & Proceed** (continue despite the findings) or **Strict (fail on violation)**. With **Strict** selected you cannot move on to the configure step while the pre-flight report contains errors
   - **Reboot after update** — on means always reboot. Off, an automated upgrade reboots only when the node asks for it, and a manual one never reboots
   - **Auto-restore guests** — migrate drained guests back after the node returns healthy
   - **Automated upgrade (SSH)** — run `apt dist-upgrade` over SSH. Disabled until the cluster has SSH credentials, and for anyone without `manage:ssh_credentials`, which includes a built-in Operator, since the wizard cannot tell; with it off, the job pauses at each node in the **Awaiting Upgrade** step until you click **Confirm Upgrade**
   - **Package excludes** — packages the automated upgrade holds back, as exact names separated by commas; wildcards are refused. Each one installed on the node is put on hold with `apt-mark hold` for the upgrade and released after it, whether the upgrade worked or not, and a package already on hold before the job stays on hold. A held package keeps back any update that needs a newer version of it, and a node with nothing else pending is skipped rather than drained. To keep a node on its kernel, name both its kernel series package, such as `proxmox-kernel-6.17`, which brings each new build of that series, and `proxmox-default-kernel`, which moves the node to a newer series (`dpkg -l 'proxmox-kernel*'` on the node lists them). A manual upgrade ignores the list. An upgrade cut off between the hold and the release (Nexara restarted, the SSH session died) leaves its holds on, listed in `/var/lib/nexara-rolling-update-holds` on the node, and the node's next automated upgrade releases them before it starts
   - **Notify on completion/failure** — an existing notification channel to message when the job ends
   - **Start immediately** — begin right after creation instead of leaving the job idle
3. Click **Create & Start**, or **Create Job** with **Start immediately** off

A cluster has one active job at a time, and any failed node fails the job. A failed job is created again rather than resumed. A job waiting to be started counts as the active one, so a job not started within 24 hours of being created is cancelled automatically, with *Never started* as its reason.

### Update Pipeline

Each node goes through these steps:

1. **Draining** — migrates guests off the node: VMs live, containers by restart. A guest with a PCI or USB device cannot migrate, so it is shut down where it is and started again afterwards
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
- **Pause** — the node in flight finishes its current step and nothing advances until you resume. The pause of DRS and CRS, and any HA rules disabled for the job, stay in force meanwhile. A restore already under way stops after the guest it is moving and carries on from there once you resume. Resuming restarts the timeouts of the nodes in flight (1 hour in **Draining**, 60 minutes in **Upgrading**, 10 minutes in **Rebooting**, 30 minutes in **Restoring**), so the time spent paused does not count against them
- **Resume** — continue a paused job
- **Cancel** — abort the job. Nodes already updated stay updated and guests already migrated are not moved back; a restore under way stops after the guest it is moving
- **Skip** — skip a node that has not started yet; a node already under way cannot be skipped
- **Confirm Upgrade** — in manual mode, confirm that you've completed the upgrade on a node; with **Reboot after update** on, this reboots it. Only a running job's node can be confirmed: resume a paused job first, and a node a failed or cancelled job left in **Awaiting Upgrade** stays there

### HA-Aware Scheduling

Before starting, the pre-flight check analyzes:
- Proxmox HA groups and rules
- DRS affinity/anti-affinity rules
- Available capacity on remaining nodes

---

## Scheduled Tasks

Scheduled tasks run on cron expressions and are attached to a single guest. They need `view:schedule` and `manage:schedule`. A schedule can be deleted but not edited from this tab.

### Creating a Schedule

1. Open a VM or container's detail page
2. Go to the **Schedules** tab
3. Click **Add Schedule**
4. Configure:
   - **Action** — **Snapshot** or **Reboot**
   - **Cron Expression** — minute hour day month weekday (e.g. `0 2 * * *` = daily at 2 AM)
   - **Snapshot Name Prefix (optional)** (snapshot actions only) — each run takes a new
     snapshot named `<prefix>-YYYYMMDD-HHMMSS` from the date and time of the run, for example
     `nightly-20260926-020000`; leave it empty and the prefix is `auto`. The date and time are
     in UTC. The cron expression itself still runs on the Nexara server's clock, in the server's
     time zone: UTC unless the container is given one (a `TZ` variable added to the `nexara`
     service's `environment:` in `docker-compose.yml`, or a mounted `/etc/localtime`), so on a
     server given another zone a `0 2 * * *` schedule still runs at 2 AM there, and its names
     show that moment in UTC. A prefix starts with a letter, uses only letters, digits, `-` and
     `_`, and is at most 24 characters, which leaves room for the date and time within
     Proxmox's 40-character limit on a snapshot name. Earlier snapshots are kept; nothing
     deletes them.

     Before names became prefixes, a name you typed was used as-is, so such a schedule took
     one snapshot and every later run failed in Proxmox. The failure showed in the task
     history (**Events → Tasks**) as a failed "Scheduled snapshot" task reading
     `snapshot name '…' already used`, and in a `pve_task_failed` alert if you had one. The
     schedule's own status did not show it: it recorded whether a run was sent to Proxmox,
     not how the Proxmox task ended. It now follows the task (see **Status** below). Those
     schedules now run again with no action from you: the old name becomes the prefix, cut
     to its first 24 characters if it is longer, and every run from then on adds a snapshot.

     Before names were in UTC, their date and time came from the server's clock, in the
     server's time zone. On an install that sets a time zone, the time in new names — and
     sometimes the date — therefore moves by the zone's offset from UTC, while every schedule
     keeps firing at the same local times. East of UTC, if a guest's `auto-…` snapshots (from
     schedules with no prefix) are taken again exactly that offset later — every hour, say,
     where the offset is whole hours — runs in the first hours after the upgrade can rarely
     fail `snapshot name '…' already used`: that needs a run's new UTC name to match, to the
     second, a name an earlier run took on local time. Later runs succeed with nothing for you
     to do.
5. Click **Create**

The table lists each schedule with its status, next run, and last run, and a delete button.

**Status** follows each run to the end of the Proxmox task it starts:

- **Pending** — the schedule has not run yet.
- **Running** — a run is being started, or the Proxmox task it started has not finished yet.
- **Success** — the last run's Proxmox task finished successfully. A task that finished with
  warnings counts as a success, as it does in the task history.
- **Failed** — the last run's Proxmox task failed, and the text beneath shows Proxmox's exit
  status, for example `snapshot name '…' already used` or a storage that cannot take
  snapshots; or the run never reached Proxmox, and the text says why.

Proxmox refuses much of what it refuses inside the task, after accepting the request, which is
why a run only reads Success once its task has ended. The collector notices the end on its next
sync of the cluster (every `METRICS_COLLECT_INTERVAL` — 10 seconds in the Docker deployment),
and the scheduler updates the schedule within 15 seconds after that. Some runs go differently:

- **The schedule fires again first.** If a run comes due before the previous run's task has
  finished and been recorded, the new run starts anyway and the status follows it; the older
  run's outcome is then only in the task history (**Events → Tasks**).
- **Proxmox stops reporting on the task** — the node rebooted, or its task log was rotated.
  Nexara gives up on the task after 24 hours. If the schedule has not run again by then — it
  fires less often than daily — it shows **Failed** with "Proxmox never reported how this task
  ended; Nexara stopped following it after 24 hours"; the task may well have succeeded, so check
  the guest. Otherwise its next run has already replaced the status, and the lost task shows
  only in the task history.
- **The cluster cannot be reached.** Nothing is followed until it can be; the status then
  settles as above.
- **The task never reaches the task history** — the scheduler could not record it, and the
  collector did not pick it up either. The schedule shows Running until its next run replaces
  it.

An open **Schedules** tab re-reads the list whenever a task on the cluster starts or ends, and
whenever the scheduler settles a run, so it shows a run once Proxmox has started its task and
how it ended as soon as that is recorded. A run that fails before it reaches Proxmox starts no
task; the tab shows it the next time it re-reads the list. As a fallback, while one of this
guest's enabled schedules shows Running, the tab also re-reads the list every 10 seconds, for up
to 30 minutes after it first showed that run.

A schedule the scheduler had to disable because its cron expression can never fire again shows
a **Disabled** badge, with the reason after the run's own error.

The scheduler evaluates schedules every 60 seconds. Every execution of a scheduled snapshot or reboot is recorded in the task history and the audit log, so scheduled activity is traceable exactly like manual actions.

---

## Tasks & Audit Log

Everything that happens — user-initiated, scheduled, or automated (DRS, rolling updates) — is tracked in two places:

- **Task history** — every Proxmox task (UPID) Nexara dispatches or observes, with live status (running, then completed or failed). Rows expand to show the full details and error output. Tasks started outside Nexara (directly in the Proxmox UI or CLI) are synced in as well and carry a **PVE** badge so you can tell where an action originated.
- **Audit log** — who did what, when, and to which resource, with the full request details in expandable rows. A secret a request sent is never among them. The writes that record the values they sent Proxmox (a guest's configuration) record a password, token or key — under any of the names Proxmox gives one: a cloud-init password, a container's root password or environment, a realm's client key — as `(set)`, or `(cleared)` when sent empty; every other write records what it changed, not a secret; and a URL anywhere in them keeps its scheme, host and path alone, its user name and password, query and fragment each `(set)`. The same holds for the row's syslog copy, the task's description, the task's log and exit status as Nexara shows them, and the alerts and reports that name a task; a failed notification's error names its channel by scheme and host alone.

Failed tasks surface the error message from Proxmox in the UI — failures are never silently swallowed.

Both live on the **Events** page in the sidebar: the **Audit Log** tab, and a **Tasks** tab for anyone holding `view:task`. Reaching the page at all requires `view:audit`, which every built-in role has — including Viewer, so assume audit rows are readable by any signed-in user.

Filter the audit log by cluster, resource type, user, action, source, severity and time range. What you see is scoped to the clusters your roles reach.

### HA and migration markers

Proxmox runs some actions as more than one task and lists each of them, and so does Nexara. A marker on the entry says which part of the action it is, in the Activity drawer, the audit log and every History:

- **HA request** — a start, stop or migration of an HA-managed guest is asked of the HA manager, and the request is a task of its own under the account that asked. It ends OK as soon as it is queued, whatever becomes of it.
- **HA manager · for _who_** — the task that does the work, which the HA manager runs as `root@pam` a few seconds later. Its outcome is the action's. It names who asked when that entry is in the same list, or the Proxmox account for a request made in Proxmox.
- **Migration target** — a live migration's start of the guest on its target node, also as `root@pam`.

A `root@pam` start, stop or migration with no marker is an action of its own: a guest started at boot, the HA manager recovering one, or a command run on the node.

- An entry gets its marker on the collector's next sync of the cluster (every 30 seconds by default), so it can be listed without one until then. Entries recorded before this release are marked in the background after the upgrade, newest first and a month at a time; a removed cluster's are not.
- If the HA manager retries a failed start, the first attempt is the one marked. A migration's start on the target is marked when it began within 5 seconds of the migration, or before the end Proxmox recorded for it.
- **Events → Tasks** has no markers.

### The Activity Drawer

A bar at the bottom of every page: **Activity**, how many tasks are running, and **All**, **N errors** and **Others** to filter by. Click the bar to open or close it, and drag its top edge to resize it; both are remembered per browser.

- **What it lists** — the 50 newest audit entries you may see (`view:audit`), plus up to 50 older entries for tasks still running or finished in the last hour, so a long disk move stays there however many entries come after it. A running task on an offline node is not kept this way.
- **Order** — tasks Proxmox confirms are running come first in the default newest-first order; a sort you choose replaces that, and applies within what the drawer holds.
- **N errors** counts those entries whose Level reads `error`: failed tasks, and errors with no task behind them, such as a TLS fingerprint mismatch or a failed sync. A cluster that is down, or that Nexara reaches through another node because its configured endpoint is unusable, adds an entry every 5 minutes, so the count climbs while that lasts and falls only as newer entries push those out of the newest 50.
- **Rows** — click to expand with the task's log (`view:task`); double-click a task for its live view.

### A UPS's History

Every UPS page has a **History** tab: its power events (each at its start, with how long it lasted and what the battery got down to), and what was done to it through Nexara: commands run, settings changed, what it powers added, removed or marked protected another way (not a node found from its `upsmon` login), and its maintenance windows. Filter by type and time; it opens on the last 7 days. An episode that began before the window is not in it, even while it is still going: the Power events tab beside it lists the newest ones whatever their start. It needs `view:audit` and `view:ups`, both install-wide.

### A Guest's History

Every VM and container page has a **History** tab: what happened to that guest, newest first — power actions, migrations (DRS and HA included), snapshots, backups, clones, config and HA-resource changes, and console opens — whether done in Nexara or in Proxmox, each task with how it ended. A row expands to its details, a **Live view** button and the task's log from Proxmox. Filter by type, by source (done in Nexara, done in Proxmox, or no task) and by time.

- A guest is its cluster and VMID: the other cluster's VM with the same number never mixes in, and the history starts at the guest's own create, clone or restore — or just after the destroy of the guest that held the VMID before, since Proxmox reuses the lowest free one.
- **Changes no task explains** — a shutdown or a crash inside the guest, a node that stopped reporting — are recorded as Nexara's collector sees them and shown about five minutes later, once it has ingested any Proxmox task that would explain them. A change made while Nexara was down is seen when it comes back, and still matched against the tasks that ran in between. A change on a node where a backup job is running waits until the job has finished and Nexara has read its log: only then does it know which guests the job backed up, and when. A job whose log could be read only in part counts for every guest on its node while it ran; one whose log could not be read at all explains nothing, so a stop-mode backup's stop then shows.
- A task's outcome outlives `TASK_HISTORY_RETENTION`: when the retention sweep prunes a guest task, it moves to an archive the History tab reads. A failed migration keeps its verdict there but not Proxmox's text, which for a cross-cluster migration can carry the target cluster's API token.
- **Backup jobs.** A backup job that covers several of a node's guests runs as one Proxmox task that names none of them, so the collector reads the job's log once it has finished and gives each guest its own **Back up (job)** row — the guest's own outcome and error, when its backup started and ended, and, expanded, its part of the job's log. A job shows up a pass or two after it finishes, not while it runs — the collector reads a few jobs a pass, so just after an upgrade the jobs of the last `TASK_HISTORY_RETENTION` take a while — and a job that ran while Nexara was down is read once Nexara is back. A job whose log Proxmox no longer has gets no rows; one whose log cannot be read in full keeps the guests it could read.
- The tab needs `view:audit` and `view:task`. Through the API (`GET /api/v1/clusters/{cluster_id}/guests/{vmid}/history`) a `view:audit` holder gets each task's outcome; the exit text from its task record, tasks with no audit entry and backup-job rows need `view:task` as well, and status changes `view:vm` (VMs) or `view:container` (containers).

### A Folder's History

A folder's page has a **History** tab, where its **Tasks** tab was: what happened to the guests in the folder and in the folders below it — each guest's History, as on the guest's own page, from that guest's own create, clone or restore — and to the folders themselves: folders made, renamed, moved and deleted, and guests filed into and out of them. A **Guest** column names each row's guest. It opens on the last 7 days; pick **All time** for the rest. Whatever the time chosen, each source is read newest first up to 20,000 entries. Where one reaches that, the History lists only what it is complete for and says from when (*Complete back to …*); **Show older**, on its last page, lists the entries before that, a stretch at a time. While you read older entries the page holds still: turning the page or changing a filter brings it up to date. **Discovered** has one too, for the guests in no folder. A folder of more than 500 guests follows the 500 most recently active — those with the latest entries — of the ones you may open, and says of how many; each guest's own page has its full History.

- A guest's whole History is the folder's while the guest is in it, including what happened before it was filed: when a guest joined a folder is not on record for guests filed before this release.
- A guest that leaves the folder, and a subfolder that is moved out or deleted, show as leaving **for moves made from this release on**. Earlier ones were recorded without the folder they left, so the folder cannot find them.
- The tab needs `view:audit` and `view:task`. Through the API (`GET /api/v1/clusters/{cluster_id}/vm-folders/{folder_id}/history`, `unassigned` for Discovered) it needs `view:audit` and `view:vm_folder`, and a guest is listed only to a caller who may open it — `view:vm` for a VM, `view:container` for a container — as on the guest's own History.

### A Cluster's History

A cluster's page has a **History** tab: everything that happened in the cluster, newest first — every action Nexara recorded in it, done in Nexara or in Proxmox, tasks with no audit entry, and its guests' status changes no task explains and parts of backup jobs — filterable by type (guests, nodes, storage, backup, Ceph, HA, network, access, certificates, replication, DRS, sync, options). A guest's row reads as it does on the guest's own History, and a **Guest** column links it. It opens on the last 7 days.

- **Sync failures** are one entry per burst: an unreachable cluster files one every few minutes, so the History shows "Sync failed, 412 times" between the first and the last, with the last one's error. A quarter of an hour without one ends the run.
- An unfiltered read holds the newest 2,000 entries of each source, the sync failures among them counted before they are folded into runs; a read narrowed by type or source searches the newest 20,000 of each source in the time chosen, a source it names counting only its own entries. Where one reaches that, the History lists only what it is complete for and says from when (*Complete back to …*); **Show older**, on its last page, lists the entries before that, a stretch at a time. While you read older entries the page holds still: turning the page or changing a filter brings it up to date. A run of sync failures that spans the point where a stretch ends is shown as one run on each side.
- The tab needs `view:audit` and `view:task`. Through the API (`GET /api/v1/clusters/{cluster_id}/history`) it needs `view:audit` and `view:cluster`; a guest's entries need `view:vm` or `view:container` by its type, as on the guest's own History.

### A Cluster Page's History

Every tab of a cluster's page has a **History**: what was done to the tab's objects, newest first — created, changed, deleted, applied, and the tasks Nexara saw Proxmox run on them — with who did it, when, and for a task how it ended. The tabs that have sub-tabs (HA, Access Control, Ceph, Networks, Firewall, Certificates, DRS, Options) end with a **History** sub-tab; Pools, Replication and Metrics have a History button beside their title, and Resource Mappings one above its cards. Filter by type (the tab's kinds of object, or what happened to one), by source and by time.

- **Rows.** An object with a stable name has a History button in its row, for what happened to that one object: an HA resource, group or rule; a pool; a resource mapping; a Proxmox user, group or role; a replication job; a Ceph OSD, pool or monitor; a node interface and the SDN's zones, VNets, subnets, controllers, IPAMs and DNS plugins; a firewall alias, IP set, security group or template; an ACME account or plugin and a node's certificate; a metric server; a DRS rule. A firewall rule has no name — its position changes with every insert — so the Firewall tab's History holds the cluster's rule set and options as a whole.
- **Window.** Every History opens on the last 7 days; pick **All time** for the rest. Each source is read newest first up to 20,000 entries whatever the time chosen, and **Show older** reads on below where one reaches that, as on a folder's History.
- **What a row's History holds beyond its object.** An HA resource: the starts, stops, migrations and relocations the HA manager ordered for its guest, to those who may open the guest. (Rows an older Nexara filed under a bare VMID, before it resolved the resource's `vm:` or `ct:`, are not listed there; the HA tab's History lists them.) A pool: the guests moved into or out of it from their own pages. A Proxmox user: its API tokens, and the permissions granted to it or revoked from it directly; a group's and a role's: the permissions that name them. A group's History is its creation, changes and removal and those permissions; its members are not listed, a user joining or leaving it is in that user's History (the user's creation or edit, which says that the user's groups were set, not which). A node interface: the node's network configuration applied or reverted. A VNet: its subnets. An IP set: its entries. A security group: its rules.
- **Aliases, IP sets and security groups** are listed under their lower-case name, the one Proxmox stores them under.
- **Firewall templates** belong to Nexara, not to a cluster: a template's creation, change and removal are in the template's own History, which needs `view:audit` and `view:network` on every cluster (an instance-wide grant). Applying a template to a cluster is in that cluster's Firewall History too.
- **DRS.** The sub-tab that lists the moves DRS made is now **Migrations**; **History** is what was done to the DRS configuration, its rules and Proxmox's HA rules.
- **Permissions.** A tab's History needs the view permission of the tab's own list (`view:ha` for HA, `view:cluster` for Pools, Resource Mappings, Metrics and Options, `view:access`, `view:replication`, `view:ceph`, `view:network` for Networks and Firewall, `view:certificate`, `view:drs`); a row's, that of the object's own page, which is the tab's but for a single pool's (`view:pool`). The tab or button is shown to someone who holds it and `view:audit` and `view:task`. Through the API (`GET /api/v1/clusters/{cluster_id}/history/{tab}` and the object routes beneath it) `view:audit` and that permission give the rows; a task's exit text needs `view:task` as well.
- **What is recorded.** Nexara's own changes, and the tasks it ingests from Proxmox that name the object (Ceph daemons and pools, certificates, the SDN and network applies). An edit made directly in Proxmox that nothing on record explains shows too, as **Changed in Proxmox** with the fields that changed (see Changes Made Directly in Proxmox).

### A Node's History

A node's page has a **History** tab: what happened to the node, newest first — reboots and shutdowns, maintenance and evacuations, repository changes, option, DNS and time-zone changes, its network, firewall-rule, storage, Ceph OSD and certificate changes, console opens, the tasks that ran on it, done in Nexara or in Proxmox, its status changes, and the guests that arrived on it or left it. It opens on the last 7 days. Whatever the time chosen, each source is read newest first up to 20,000 entries. Where one reaches that, the History lists only what it is complete for and says from when (*Complete back to …*); **Show older**, on its last page, lists the entries before that, a stretch at a time. While you read older entries the page holds still: turning the page or changing a filter brings it up to date.

- **Show routine checks** brings back the reads that change nothing on the node, left out until then: package-list refreshes — Proxmox runs one daily — memory-module reads — Nexara makes one daily — and report downloads.
- **Include guest activity** adds what the node's guests did on it — their tasks, migrations and backups — which a busy node runs by the thousand; each guest has its own History. Without it, a guest is listed only where it arrived on the node or left it, and a **Guest** column links it.
- **Status changes** — online, offline, and unknown while Proxmox does not report the node — and **guest arrivals and departures** are recorded from this release on, as Nexara sees them. A round trip through unknown that is back within two minutes is not listed. A guest seen on another node is an arrival or a departure when a migration of it to that node is on record that succeeded, ending as the guest moved; with none, the entry says only that its VMID is now on the other node — an HA recovery, a guest moved outside Proxmox's tasks, or a new guest on the VMID. A guest destroyed and created again under its VMID, or one of another type in its place, has not moved. Past twenty in an hour, a node's further status changes, or a guest's further moves, are one entry: how many, from the first to the last of them, and where they ended.
- A node removed and added again under its name continues its History.
- The tab needs `view:audit` and `view:task`. Through the API (`GET /api/v1/clusters/{cluster_id}/nodes/{node_name}/history`, with `include_guests=true` for the guests' activity) it needs `view:audit` and `view:node`; a guest's entries need `view:vm` or `view:container` by its type, as on the guest's own History, and tasks with no audit entry `view:task`.

### Histories of Storage and Backup

A storage, a backup server and a backup job have the same timeline as a guest: newest first, with the type, source and time filters, and rows that expand to their details and, for a task, its Proxmox log.

- **A storage** — the **History** tab of its page: its definition added, changed or removed, uploads, downloads and deletes of its content, disks moved onto it or off it, the backups and restores that name it, the LVM, LVM-thin or directory volume that added it, and a resource pool it was added to or taken out of. A disk that a migration places on it by the migration itself (a remote migration's target storage, a storage map) is not there, because the migration's entries name the nodes and not the storages; the per-disk moves of a storage migration are. A storage is its name in its cluster, as Proxmox calls it on every node, so the History outlives Nexara's inventory row for the storage and never mixes in another cluster's storage of the same name. A storage that is not shared is a different storage on every node: its page shows what ran on its host node, and the changes to its configuration, which belong to every node's.
- **A Proxmox Backup Server** — the **History** tab beside Tasks: the server added, changed or removed, datastore garbage collections and prunes, snapshot changes, sync and verify jobs run from Nexara, and the restores from the server (a restore that Nexara tied to the server only because no other storage could be reads *source not confirmed*). Above the first page are the server's own latest tasks, read live; a task Nexara started is one row there, named by who started it. **A datastore** has a **History** button on its card, with the rows that name it and the restores from it.
- **A Veeam server, one of its jobs and one of its runs** — the **History** button in the row's actions: the server added, changed, removed or tested, a platform mapped to a cluster and a backup object mapped to a guest, every job started, stopped, enabled or disabled and every run stopped; the job's, what was done to it and its runs; the run's, a stop asked for. A job's latest runs are listed live above its history, from the runs Nexara has collected; they are not in the history itself, which is what was done to the job.
- **A Proxmox backup job** — the **History** button in the Schedules table: created, changed or deleted, and run from Nexara, with each run's outcome. The runs Proxmox starts on the job's schedule are not here: the task they start names no job, so they are in the History of each guest they back up. A job is the id Proxmox gave it, not its name.

Changes made directly in Proxmox that Nexara detects are listed too, as *Changed in Proxmox*.

The tab and the buttons need `view:audit` and `view:task`, and the object's own permission: `view:storage` or `view:backup` on the cluster for a storage or a backup job. A backup server's rows are in any cluster, or none, so its History needs instance-wide grants, not grants on one cluster: `view:audit` and `view:veeam` for a Veeam server, and `view:audit`, `view:pbs` and `view:backup` for a Proxmox Backup Server, most of whose rows are the backups' (snapshots, restores).

### Changes Made Directly in Proxmox

An edit made in the Proxmox GUI, its API or a node's shell often runs no task — a VM's memory, a container's options, a pool's members, a storage definition, a node's DNS — and so leaves no audit entry. Every `OBJECT_OBSERVE_INTERVAL` (default `5m`, between `1m` and `1h`; `0` stops these reads, though a Proxmox Backup Server's job edits are still recorded, as below) Nexara reads each cluster's objects and compares them with what it read last, and a History shows what changed as **Changed in Proxmox**, **Created in Proxmox** or **Removed in Proxmox**, between the two reads.

- **What is read:** every guest's configuration; the HA resources, groups and rules; the resource pools and their members; the SDN zones, VNets, subnets, controllers, IPAMs and DNS servers; the cluster firewall's options, rules, aliases, IP sets and their entries, and security groups and their rules; the Proxmox users, API tokens, groups, roles and ACLs; the ACME accounts and plugins; the storage definitions, the backup jobs, the USB and PCI mappings, the metric servers and the datacenter options; and each online node's network interfaces, options, notes, ACME domains, DNS, time zone and firewall rules. A Proxmox Backup Server's sync and verify jobs — their datastore, remote, schedule and comment — are compared from what the PBS pass already reads, so they cost no call of their own; Nexara never edits them, so every change to one shows. A PBS's rows belong to no cluster and stay when a cluster is removed, and they are recorded whatever `OBJECT_OBSERVE_INTERVAL` is: setting it to `0` stops the reads of the clusters, not the PBS's.
- **Where it shows, and to whom:** a guest's configuration on the guest's History and its folder's; a node's options, DNS, time zone, notes, interfaces, firewall rules and ACME domains on the node's History; an object of a cluster page's tab — an HA group, a pool, a firewall alias, a user — on its tab's History and its own; every change filed under a cluster on the cluster's History. Each is listed only to someone who holds the view permission of its own kind of object in that cluster — a pool's change to `view:pool`, a node's firewall rules to `view:network`, a guest's configuration to `view:vm` or `view:container` — whichever History it appears in. A change an entry of the History explains is not listed on its own: what it changed beyond the fields the entry names shows on the entry, object by object — **Also changed in Proxmox: boot; Pool pool01 (vms)**.
- **Field names only, never values.** A row names the fields that changed — `memory`, `net0`, `nodes` — and for a firewall rule set how many rules came and went. An ACL path's fields are the kinds of grantee and their grants; which role was granted is kept only as a keyed hash. What Nexara keeps of the last reading is a keyed hash of each value (an HMAC under a key derived from `ENCRYPTION_KEY`), so a guest's cloud-init password, an IPAM token or a node's notes cannot be read or guessed from the database.
- **Only what nothing explains.** A change shows about five minutes after it is seen, once Nexara has ingested the Proxmox tasks that ran meanwhile, and only when no audit entry in its window explains it — and only an entry of the change's own kind and direction does: an object created by a create, changed by an update, removed by a delete, made through Nexara, or by the Proxmox task that can make that change. A disk move explains the disk it moved, not the memory someone changed in the GUI while it ran; a guest's destroy explains that guest leaving its pools, backup jobs and HA rules, not another guest leaving them, and a pool's edit the guests and storages it names joining it, or with delete leaving it, not another; an edit made through Nexara explains the settings it made — its audit entry names the fields it changed: an edit of a storage, a backup job, an interface, an SDN object, a metric server or an ACME plugin reads the object just before it writes it and compares as Nexara does here, and an edit of a guest's configuration, the datacenter options, the cluster firewall's options or an HA resource, group or rule reads it just before and again just after and names what moved, so a dialog that sends its whole form names only what differs (a guest's entry, which also keeps the values it sent, a secret among them as `(set)`, names them in `changed`, and a disk re-sent as it was, in whatever form Proxmox writes it back, names nothing); when a read fails the entry lists every field it sent, says `fields_unverified`, and explains nothing seen in Proxmox — together with the ones Proxmox rewrites with them (an interface's method and its address, netmask and CIDR, which follow from one another; a backup job's other guest selections; all of a firewall alias, which an edit writes whole), and a firewall rule set's own edits only as many rules as they added, removed or moved. Only a Proxmox `qmconfig` task — the GUI's own asynchronous configuration edit, which can write any key — explains any field of its guest changed in its window. A realm sync explains what its task log says it did: the users and groups of its realm it added and removed, and the ACL entries it purged; of a user it replaced whole (remove-vanished `properties`, logged `overwriting user`) every field but enable, which Proxmox keeps from the old record; of a user it already had, the names, e-mail and comment, and enable, expiry and SSH keys only where the realm's sync attributes map them (none while Nexara cannot read the realm, which needs Sys.Audit); and a user joining one of its groups, seen on the user or on the group, only when its log added, replaced or updated that user (`adding`, `overwriting`, `updating user`). Its log counts only as a sync writes it, each line in its own section of a sync that finished and wrote them — one with a line out of place, or with a warning whose quoted directory value runs past its line, explains nothing — and only a sync Nexara has an audit entry of is read, its log kept once it is final. An entry from more than an hour before the change was seen (three intervals, if `OBJECT_OBSERVE_INTERVAL` is set longer than 20 minutes) explains nothing, so after an outage one action from early in the gap does not hide everything found at its end. A guest created, cloned, restored or destroyed is never a row of its own here: those are tasks, already in its History; a guest read while a clone or a create is still writing it is read again once it is done, and only that reading is its baseline.
- **What it ignores:** what Proxmox rewrites by itself — the file's digest, a guest's lock and snapshot pointer, the state a hibernated VM carries, an interface's run state, a backup job's next run, the order of a list Proxmox re-encodes on every request. A guest is read with its pending changes applied, so an edit that waits for a restart shows when it is made. The first read of a cluster, of a node or of a guest, and the first read after a new `ENCRYPTION_KEY` or a Nexara release that reads a kind's fields another way, is a baseline and shows nothing. A Proxmox VE upgrade (or a node's new kernel) is compared across: what moved shows, marked as during an upgrade and grouped as one — but for the Proxmox users, groups, tokens, roles and ACLs and the firewall, each change of which shows on its own — and is explained or not like any change. A change to the cluster token's privileges on `/` is shown in the cluster's History as a change of its own, since a privilege granted or taken away can make objects in a filtered list appear or vanish; it re-baselines only the ACME plugins (`Sys.Modify`) and containers (`VM.Config.Options`, which a container's `env` needs), and everything else is compared as ever.
- **When Proxmox cannot be read:** a read that fails changes nothing, and the object is compared again from its last reading once it can be read, the window running from then; a kind of object this Proxmox VE does not have, or that the token may not read, is skipped for a day and logged once. A list that comes back empty where objects were is believed only on the second pass. A node that leaves two reads in a row unanswered — the cluster's endpoint passes them on, and a hung node times them out — is not read again that pass, while the other nodes' objects and guests are; a pass ends early only when the endpoint itself stops answering. A node Nexara has never read holds nothing back: the cluster's objects are compared as ever, after twelve passes its History says that node has never been read, and the day it is, a Proxmox version none of the other nodes runs marks what the cluster's objects then show as during an upgrade, as an upgrade would.
- **Cost:** about 27 calls a cluster a pass, one more per pool, SDN VNet, IP set, security group and metric server, five per node, and one per guest — spread over the interval, at most two at once and ten a second per cluster. At 50 guests that is about 0.35 calls a second, at 500 about 2. At the default interval the ten-a-second cap binds at about 2,400 guests a cluster, and a pass takes longer than the interval at about 3,000. Past 10,000 objects of a kind — or of one parent's objects, counted apart: an IP set's entries, a VNet's subnets, a user's tokens; of the Proxmox users, tokens, groups, roles and ACLs, past 20,000; and of any kind past 100,000 across all its parents — Nexara keeps one keyed hash of each object whole, of all it reads of it (a pool's members included), writes it only when the object appears, changes or goes, and keeps what it compared field by field before it went over as it was. A pass that finds something moved records one entry for the kind or parent: how many came, went and changed, exactly, naming up to 500 of each, picked at random beyond that; one that finds all as it was writes nothing. A kind already past the cap at its first reading takes that reading as its baseline and counts from the next. Back under the cap, every object is compared with what it was before the kind went over, and each change, addition and removal since shows on its own, dated from then and explained by the object's own audit entries since then — an addition hidden in a flood included, once the flood is gone. The kind's History notes going over, once a day while it lasts, and coming back; a parent removed — an IP set, a VNet, a security group, a user — is one entry saying how many of its own went with it. Of more than 10,000 guests a pass reads the 10,000 read longest ago, so each is still compared, in turns; more than 50 changes of one kind in one reading are each recorded and explained or not on its own, marked as one bulk change so a History can show them together; and a change nothing could classify within a day is shown rather than kept hidden.
- **Privileges:** the token reads nothing it could not before. `PVEAuditor` on `/` is enough for everything except the ACME plugins (`Sys.Modify` on `/`, which only `Administrator` holds) and a container's `env` (`VM.Config.Options`); an ACME account is read by name only, since its details are `root@pam`'s alone. The HA configuration, the cluster firewall, the backup jobs, the metric servers and the nodes' options need `Sys.Audit` on `/`; without it the datacenter options are skipped, since Proxmox then answers with only two of them. Lists Proxmox filters by privilege — pools, storage, SDN, mappings, users, groups, ACLs — show what the token may see.
- **Not covered:** a guest's own firewall rules and options (several more calls per guest), authentication realms, and anything a node's root shell changes in a file Proxmox does not list. A disk reassigned from one guest to another shows on both, since the task names neither by its VMID; a cloud-init password changed from one value to another is invisible, since Proxmox only ever shows it masked; a token scoped below `/` sees objects come and go as its grants there change. When Proxmox VE 9 migrates the HA groups to rules, nothing is recorded: Proxmox did it.

### Exporting the Audit Log

The **CSV**, **JSON** and **Syslog** (RFC 5424) buttons export what the cluster, resource type, user, action and time-range filters select — handy for a one-off handover to an auditor. The source and severity filters do not narrow the file, and severity narrows only the page on screen, since it is worked out in the browser. An export stops at the newest 10,000 entries; a JSON export's `total` says how many matched.

### Forwarding to a SIEM

For continuous delivery, expand **Syslog Forwarding** on the Audit Log tab:

- **Host** and **Port** — your collector. The port follows the protocol (514 for UDP and TCP, 6514 for TLS) until you enter one of your own; leave it blank to use the protocol's default
- **Protocol** — UDP, TCP, or TLS (with an optional *Skip TLS certificate verification* for self-signed collectors)
- **Facility** — the syslog facility to stamp on records

Tick **Enable syslog forwarding** and click **Save**. **Test Connection** sends a single probe record so you can confirm the collector is receiving before turning it on; over UDP nothing answers, so success means only that it was sent. Records are RFC 5424 with STRUCTURED-DATA, every field quoted and the record length bounded, and forwarding happens off the request path so a slow or dead collector never stalls an API call. Nothing is stored for later, though: a record a dead collector cannot take, or that overflows the 1,024-record queue, is dropped and only the log says so, so the audit log stays the record of truth. Only entries written for a request to the UI or API are forwarded, which includes authentication events and resource changes. Tasks observed on Proxmox, sync failures, scheduled tasks, rolling updates, a migration's own progress and failure entries, DRS moves, automatic guest-tools actions and daily memory reads are not, so for a complete record pull `GET /api/v1/audit-log/export` instead. Changes to the forwarding configuration — plus the test probes themselves — are audited. Editing this configuration requires `manage:audit`.

---

## Reports

Navigate to **Reports** from the sidebar. The page opens on a **catalogue** of report types — each card says what the report answers, what is inside, and when it last ran — with the run history and schedules beneath it.

Every report shares one layout: a masthead with the period and the sources consulted, a strip of headline figures, a ranked list of **findings** in plain language (the thing to act on, most severe first), the evidence sections with charts and tables, and a footer stating exactly how each number was computed. Charts are inline SVG, so the same document previews in the app, prints or saves as PDF from the viewer's **Print / PDF** button, downloads as HTML, and travels as an email attachment.

### Report types

| Report | What it answers | Options |
|---|---|---|
| **Cluster digest** | One page across availability, load, backups, alerts, tasks, snapshots and patching, with the findings of the uptime, resource utilisation, backup compliance, snapshot inventory and patch status reports | stale hours, snapshot age |
| **Backup compliance** | Every guest against **Proxmox Backup Server and Veeam**: coverage by state, recovery-point age, backup runs and failures in the period, datastore and repository capacity with days-to-full, malware verdicts and orphaned Veeam objects | stale hours; the runs section can be switched off |
| **Resource utilisation** | CPU, memory, disk and network per node and per day, with sustained-pressure findings | — |
| **VM resource usage** | Top consumers per resource, idle guests, every guest's period averages | top N |
| **Rightsizing** | Per guest, p95/p99/peak CPU over the time it ran with busy vCPUs, its own memory use against its allocation, and how much of the time it ran; flags clearly over- and under-sized guests. Covers at most the last 30 days | — |
| **Capacity forecast** | Where the period's trend takes CPU and memory per node; how much room each node's memory has to grow, from its DIMM layout; how full each storage pool is | — |
| **Snapshot inventory** | Every guest snapshot by age, and the ones most likely forgotten | snapshot age |
| **Patch status** | The latest vulnerability scan per node, known-exploited CVEs first | — |
| **Uptime summary** | Which nodes are up, for how long, and which rebooted in the period | — |

**Rightsizing.** Its figures come from the raw guest readings, which Nexara keeps for 30 days, so a longer period is clamped to the last 30 days and the masthead says so. CPU is measured only over the readings taken while the guest ran, so time spent stopped does not read as idle. Memory is the guest's own figure: a VM's balloon statistics (a Linux guest counts its page cache as used) or a container's cgroup usage. A VM without balloon statistics is measured only from the host, which says nothing about use inside it, so the report says it cannot tell rather than inventing a figure. Flags: CPU over-allocated (a VM with 2 or more vCPUs under 25 % at p95), CPU-bound (90 % or more at p95), memory over-allocated (its own p95 under 50 %), memory tight (a container's own p95 at 90 % or more), and running under 10 % of the time. A guest that ran under an hour in the window is never flagged.

**Backup compliance and Veeam.** The report shares its verdict with the Backup page's coverage view, so the two never disagree: a guest is *current* when its newest restore point from **either** provider is younger than the stale threshold (default 24 h), *stale* otherwise, and has *no backup* when neither provider holds a point. PBS snapshots are matched through the datastores the cluster actually mounts, so a same-numbered guest on another cluster never counts. Veeam objects are matched on the guest's SMBIOS UUID; a name-only match is flagged. Veeam's own worker appliances and backup server are listed but excluded from every percentage.

Veeam data appears in a report when the person who requested it — the on-demand requester, or for a schedule the person who last **saved** it — holds `view:veeam` on that cluster; otherwise the report consults Proxmox Backup Server alone and says so. Saving a schedule makes its runs read under your grants from then on, and a schedule whose saver has been deactivated stops running until someone saves it again. A stored run is then readable by anyone holding `view:report` on the cluster: a report is a summary product, shared under that one grant by design, the same posture audit details take for Viewers. If the Veeam tables cannot be read, the run **fails** rather than quietly calling every Veeam-only guest unprotected.

### Generating a report

1. Click **Generate** on a catalogue card (or **Generate report** at the top of the page)
2. Pick the **Cluster** and the **Period** in hours (1–8760). Point-in-time reports — snapshots, patch status — use the period as context only
3. Set any **report options** the type offers; blank means the documented default
4. Click **Generate**. The finished run opens on its own page with **Print / PDF**, **HTML**, **CSV**, **Email** and **Run again**. Generating, emailing and running again need `generate:report`, and at most three reports generate at once across the install; a fourth caller is told to try again

Runs also appear on the **Report history** tab, where each one can be opened, downloaded, emailed or — with `manage:report` — deleted. Runs produced by a *schedule* are pruned automatically after 90 days; a report you generate on demand is kept until you delete it.

### Scheduled reports

1. Click the schedule button on a catalogue card (or **New Schedule**)
2. Configure:
   - **Name** — e.g. `Weekly backup review`
   - **Report Type** and **Cluster** — one cluster per schedule
   - **Cron Schedule** — minute hour day month weekday (e.g. `0 6 * * 1` = Mondays at 06:00, on the server's clock: UTC unless the container is given a time zone. **Next Run** shows it in your browser's)
   - **Time Range (hours)** — how much history each run covers
   - **Attachments** — **HTML report**, or **HTML report + CSV**
   - **Report options** — the same options as an on-demand run
   - **Email delivery** (optional) — pick an email notification channel and every run is mailed to it
   - **Enabled**
3. Click **Create Schedule**

### Emailed reports

An emailed run carries a **digest** as the body — the headline figures, a coverage bar and the top findings, built from plain tables so every mail client renders it — with the full report attached as `<type>-<cluster>-<date>.html` (and `.csv` when the schedule asks for it). The digest is built from the same data as the attachment, so the two cannot disagree. Any finished run can also be sent on demand from its page or the history tab: pick the channel, optionally override the recipients, and choose whether to attach the CSV.

---

## Mobile & PWA

The entire UI is responsive — every feature works on phones and tablets:

- Navigation collapses into a drawer menu
- Detail pages and tables reflow into mobile-friendly layouts
- Consoles (VNC, node shell) take over the full screen on mobile
- Touch targets are sized for fingers, including tree and header actions

To install Nexara as an app, open it in your phone's browser and choose **Add to Home Screen** (or **Install app**). It then launches in a standalone window without browser chrome.

> Nexara is a live dashboard and needs a connection to your Nexara server — the installed app does not work offline.

---

## UI Tips

- **Command palette** — press **Ctrl+K** (**⌘K** on macOS) anywhere, or click **Search or jump to…**. Type at least two characters to search guests by name or VMID, nodes, storage and clusters, and, with a global `view:ups`, UPSes by name, description, model or NUT server. It also jumps to pages and to a cluster's tabs, and runs actions: create a VM or container, import a VM, open the console page, switch the theme. Right-click a guest in the results for its own actions.
- **What's new** — after an upgrade, a "What's new" dialog summarizes the releases you've picked up since your last visit (fed by GitHub Releases, per browser). **What's New** in the user menu shows the current release again. If Nexara cannot reach GitHub there is no popup, and the menu item says so with a link to the releases.
- **About** — the user menu's **About** shows the version you are running, with links to the source, the releases and the issue tracker.
- **Confirmation dialogs** — most destructive operations ask first and name the exact resource affected; the exceptions are noted where they apply.
- **Click to copy** — where a dialog makes you type a resource's name (destroying a guest, converting it to a template, deleting a cluster, a storage, a PBS or a Veeam server), clicking the name copies it.

### Tables

- **Sort** — click a heading for ascending, again for descending; blanks sort last. A sort is not saved.
- **Rearrange** — on the Activity drawer, **Events → Tasks**, every History, the Veeam, Guest Tools and UPS tables, drag a heading to move the column and drag its right edge to resize it. That is saved per browser; **Reset columns** appears once you have changed something.
- **Choose columns** — the Inventory table has a **Columns** menu instead, saved separately for desktop and mobile.
- **Expand** — click a row to expand it. Alerts, the Audit Log, Tasks, Activity and History keep one row open at a time.

---

## Branding & Theming

Navigate to **Admin > Branding**.

### Custom Branding

- **Application Title** — change the title shown in the browser tab and sidebar
- **Logo** — upload a custom logo (displayed in the sidebar)
- **Favicon** — upload a custom favicon

### Appearance

**Settings** in the sidebar opens **Appearance**, for your own account:

- **Language** — only English ships today; see the [translation guide](../frontend/src/locales/CONTRIBUTING.md)
- **Theme** — light, dark, or system (follows OS preference), remembered per browser
- **Accent Color** — the default or one of 9 presets, which theme the entire UI
- **Storage Units** — binary (GiB) or decimal (GB)
- **Date Format** and **Default Refresh Interval** — saved, but nothing reads them yet. The dashboard has its own refresh control
- **Open consoles in** — **Floating window** (the default) or **New browser tab**; see [Consoles](#consoles)

---

## Security & Deployment Settings

Two environment variables matter for any production deployment — see the [Installation Guide](installation.md#configuration-reference) for the full reference:

- **`WS_ALLOWED_ORIGINS`** — exact-match allow-list of `Origin` values accepted on WebSocket upgrades (`/ws`, `/ws/console`, `/ws/vnc`). Set it to your public origin (e.g. `https://nexara.example.com`) so a malicious page on another domain can't open a WebSocket through a logged-in user's browser. Empty or `*` accepts any origin — fine for labs, and warned about at startup.
- **`TRUSTED_PROXIES`** — when Nexara sits behind nginx/Traefik/Caddy, set this to the proxy's IP or CIDR so the per-IP rate limiters key on the real client address instead of the proxy's. Without it, all proxied traffic shares one rate-limit bucket. Pair with `PROXY_HEADER` if your proxy uses a non-standard header.

Also consider `SECURE_COOKIES=always` behind a TLS-terminating proxy, and `HSTS_MAX_AGE` for HTTPS-only deployments with a trusted certificate.

Whatever proxy you use, do not let it strip or redirect a trailing slash under `/api/`. Nexara refuses a write whose path ends in `/`, because that is what a browser sends when a `.` or `..` name is resolved away, and a proxy that removes the slash or redirects to the slash-less path hands the parent object that write instead. Behind Traefik that refusal covers browsers only: Traefik's default path sanitising resolves a `.` or `..` segment itself, `%2E` spellings included, and drops the slash a final one leaves, so a script's `DELETE` aimed at a pool named `..` reaches Nexara as the cluster's — which Nexara refuses too, because deleting a cluster requires `confirm` set to its name. Nexara does not rely on that sanitising, so an entry point that serves only Nexara can turn it off. No API client should send `.` or `..` as a path segment, encoded or not. The README's [Reverse Proxy](../README.md#reverse-proxy) section has the details.
