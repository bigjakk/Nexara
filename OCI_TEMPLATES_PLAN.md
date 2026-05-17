# Template Management & OCI Image Support — Design Doc

**Status:** Draft → In progress
**Started:** 2026-05-17
**Target release:** v1.0 (rolled into the in-flight Phase-1..5 plan; no interim version bump)

## Goals

1. Give Nexara users a first-class **template management** UI — currently we have manual upload + delete but no way to pull an official Proxmox appliance template or download from an arbitrary URL.
2. Add **Proxmox VE 9.1 OCI image** support so users can deploy LXC application containers (e.g. `docker.io/eclipse-mosquitto:latest`) without ever leaving Nexara.
3. Make the OCI tech-preview limitations visible (no in-place updates, file-based-storage-only, skopeo-required) so users aren't surprised.

## Non-goals (for this release)

- **Private-registry authentication.** Proxmox upstream doesn't expose registry auth params on the `oci-registry-pull` endpoint yet; defer until they do.
- **OCI layer-aware update workflow.** Proxmox squashes layers on import; recreate-on-update is the official story. We surface that, but don't paper over it.
- **Template provenance tracking** in our own DB (e.g. "this `mosquitto_latest.tar` came from `docker.io/eclipse-mosquitto:latest`"). Nice-to-have for v2; for now Proxmox is the source of truth.
- **Template sync between storages or clusters.** A single-storage upload/pull is enough for v1.

## Background: how Proxmox 9.1 OCI works

A new endpoint, one dependency, no new content type:

- **`POST /nodes/{node}/storage/{storage}/oci-registry-pull`**
  - Body: `reference=docker.io/eclipse-mosquitto:latest` (Docker-style image reference)
  - Optional: `file_name=custom_name` (server appends `.tar`)
  - Permissions: `Datastore.AllocateTemplate` on `/storage/{storage}` + `Sys.AccessNetwork` on `/nodes/{node}`
  - Backend runs `skopeo copy docker://<ref> oci-archive://<storage>/template/cache/<sanitized>.tar` in a Proxmox worker; returns a **UPID** for async tracking.
  - Result is stored as a regular `vztmpl` file. Container creation (`POST /nodes/{node}/lxc`) uses the same `ostemplate=storage:vztmpl/file.tar` it always has — PVE detects the OCI archive on create and converts.
  - Storage prerequisites: file-based plugin (`dir`, `nfs`, `cifs`, `cephfs`, `glusterfs`, `btrfs`) with `vztmpl` in its content list.
  - Node prerequisite: `skopeo` package installed (pulled in by `pve-container` recommends in 9.1).
  - Limitation: layers squashed at create time; updates = recreate.

Related endpoints we'll also use (these are not new but Nexara doesn't wrap them yet):

- **`GET /nodes/{node}/aplinfo`** — official appliance template catalog (Debian/Ubuntu/Alpine/Turnkey). Each entry has `template`, `os`, `version`, `description`, `infopage`, `source`, `package`, `headline`, `manageurl`.
- **`POST /nodes/{node}/aplinfo`** — download a specific appliance from the catalog. Params: `storage`, `template`. Returns UPID.
- **`POST /nodes/{node}/storage/{storage}/download-url`** — generic URL → storage downloader. Params: `url`, `content` (`iso`/`vztmpl`/`import`), `filename`, optional `checksum`/`checksum-algorithm`, `decompression-algorithm`, `verify-certificates`. Returns UPID.

## Scope

### Backend (Go)

**`internal/proxmox/client_storage.go`** — new methods:

```go
PullOCIImage(ctx, node, storage string, params OCIPullParams) (upid string, err error)
DownloadURLToStorage(ctx, node, storage string, params URLDownloadParams) (upid string, err error)
GetAppliances(ctx, node string) ([]ApplianceTemplate, error)
DownloadAppliance(ctx, node, storage, template string) (upid string, err error)
```

**`internal/proxmox/client_cluster.go`** (or a new `client_version.go`):

```go
GetVersion(ctx) (*Version, error)   // hits /version, returns release "9.1.2", repoid, version "9.1"
```

**`internal/proxmox/types.go`** — new types:

- `OCIPullParams { Reference, FileName string }`
- `URLDownloadParams { URL, Content, Filename, Checksum, ChecksumAlgorithm, DecompressionAlgorithm string; VerifyCerts *bool }`
- `ApplianceTemplate { Template, OS, Version, Description, InfoPage, Source, Package, Headline, ManageURL, Section string; SHA256 string }`
- `Version { Release, RepoID, Version string }`

Validation (server-side, before hitting Proxmox):

- OCI reference: mirror PVE's regex from the patch (`(<host>/)?<path>(:<tag>)?(@<digest>)?`). Length cap 512.
- URL download: require `https://` (or explicit allow-list of `http://` for self-hosted internal mirrors — flag via config?); cap URL length 2048; validate filename (no slashes, no `..`, length ≤ 255).
- Filename for OCI: optional, must match `[a-zA-Z0-9._-]+`, ≤ 64 chars. Server appends `.tar`.
- Reject if storage's `content` doesn't include `vztmpl` (or `iso`/`import` for URL download).

**`internal/api/handlers/storage.go`** — new handlers:

```go
PullOCI(c)         POST   /api/v1/clusters/:cid/storage/:sid/oci-pull        manage:storage
DownloadURL(c)     POST   /api/v1/clusters/:cid/storage/:sid/download-url    manage:storage
ListAppliances(c)  GET    /api/v1/clusters/:cid/nodes/:nid/appliances        view:storage
DownloadAppliance(c) POST /api/v1/clusters/:cid/storage/:sid/appliances      manage:storage
```

All four:

- Resolve cluster → node → Proxmox client via the existing `resolveStorage` / `createProxmoxClient` helpers (30-min timeout — OCI pulls of big images can be slow).
- Validate inputs explicitly; return `400` with `{error, message}` envelope on failure.
- Audit-log every mutation via existing `h.auditLog`. Resource type = `"storage"`, action = `"pull_oci"` / `"download_url"` / `"download_appliance"`. Details JSON includes the reference/URL/template name.
- Return `{upid, status: "dispatched"}` — same shape as `UploadFile`.
- Surface skopeo-not-installed and oci-not-supported errors with friendlier messages than the raw Proxmox bubble-up (regex-match on the wrapped error string).

**Cluster API** (`internal/api/handlers/clusters.go`) — extend the cluster response to include `pve_version` (a string like `"9.1.2"`). Collector job populates it on each cycle. Add a migration:

```sql
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS pve_version TEXT NOT NULL DEFAULT '';
```

(Safe per CLAUDE.md upgrade-safety rules: `IF NOT EXISTS` + `NOT NULL DEFAULT`.)

Collector update: in the existing per-cluster cycle, fetch `/version` once and `UPDATE clusters SET pve_version = $1 WHERE id = $2`. Log + continue on failure (don't break the whole cycle).

**RBAC.** No new permissions — all four routes reuse existing `manage:storage` / `view:storage`. The Operator role already carries `manage:storage`; Viewer gets only `view:storage`. (Verified during scoping; revisit if testing shows otherwise.)

### Frontend (TypeScript + React)

**New API hooks** (`features/storage/api/storage-queries.ts`):

- `usePullOCI(clusterId, storageId)` — mutation
- `useDownloadURL(clusterId, storageId)` — mutation
- `useAppliances(clusterId, nodeId)` — query (1-hour staleTime, big response)
- `useDownloadAppliance(clusterId, storageId)` — mutation

**New components** (`features/storage/components/`):

- `OCIPullDialog.tsx` — reference input with inline regex validation, optional filename, storage select (pre-filtered to file-based + `vztmpl`-enabled), submit → toast + task-watcher row.
- `URLDownloadDialog.tsx` — URL, content type, filename, optional checksum/algorithm/decompress, verify-certs toggle.
- `ApplianceBrowserDialog.tsx` — table from `useAppliances`, columnar OS/Version/Section, search + filter, "Download to <storage select>" action per row.
- `TemplateToolbar.tsx` (new, mounts on `StorageDetailPage`) — `Upload` (existing) + `Download URL` + `Pull OCI` (only if `cluster.pve_version >= 9.1`) + `Browse Appliances`. Hidden if storage `content` lacks `vztmpl`/`iso`/`import` as appropriate.

**Storage detail page** (`features/storage/pages/StorageDetailPage.tsx`) — mount the toolbar above the existing `StorageContentTable`; preserve current layout for ISO-only storages.

**LXC create wizard** (`features/vms/components/CreateCTDialog.tsx`) — small touch in the template step:

- For each `vztmpl` row, detect OCI archives heuristically (filename starts with non-Debian/Ubuntu pattern + `.tar` extension + no `.tar.gz`/`.tar.xz`) — or, better, use a backend hint if we add a `Source` column (deferred).
- Badge OCI rows with a small "OCI (preview)" pill and show a one-line inline note: "Built from an OCI image. In-place updates aren't supported — recreate to apply image changes."

**Cluster store** — add `pveVersion` to the cluster type/store; frontend gates the OCI button via a `semver`-style compare helper (`isPVEAtLeast(version, "9.1")`).

### Telemetry / observability

- Audit log captures every pull/download with the user, cluster, storage, and reference/URL.
- The existing event bus (`KindStorage`?) emits an event on dispatch so the task-watcher panel auto-refreshes — if there's no event kind for storage today, reuse `KindAudit`.

## Version & capability gating

Two gates:

1. **PVE ≥ 9.1** — required for OCI. Detected via cluster `pve_version`. If missing, frontend hides the Pull OCI button.
2. **`skopeo` present** — required at pull time. We can't probe this cheaply; instead, if the first OCI pull on a node fails with `command not found: skopeo` (string-match in error), surface: *"Install skopeo on the node: `apt install skopeo`"*.

Storage content filter — UI auto-filters the storage select in OCI/URL dialogs to ones that have the right content type. Backend re-validates so an out-of-date frontend can't bypass it.

## Migration / DB

One additive migration:

```
migrations/000028_cluster_pve_version.up.sql
migrations/000028_cluster_pve_version.down.sql
```

Up:
```sql
ALTER TABLE clusters ADD COLUMN IF NOT EXISTS pve_version TEXT NOT NULL DEFAULT '';
```

Down:
```sql
ALTER TABLE clusters DROP COLUMN IF EXISTS pve_version;
```

Safe per CLAUDE.md (additive, `IF NOT EXISTS`, `NOT NULL DEFAULT`).

## Phased rollout

### Phase 1 — Backend (this PR)

- [x] Plan written
- [ ] **1a** Proxmox client methods + types + unit tests
- [ ] **1b** API handlers + routes + audit-log + error mapping
- [ ] **1c** PVE version migration + collector wiring + cluster response field
- [ ] `make lint` clean, `make test` green

### Phase 2 — Frontend

- [ ] **2a** API hooks + types
- [ ] **2b** Three new dialogs
- [ ] **2c** Toolbar on Storage detail page
- [ ] **2d** OCI badge in CreateCT wizard
- [ ] `cd frontend && npx eslint` clean, `cd frontend && npm test` green
- [ ] `cd frontend && npx tsc --noEmit` clean

### Phase 3 — Verification

- [ ] Rebuild full Docker stack per UAC protocol (`docker-compose.yml` + `docker-compose.dev.yml`, port 80).
- [ ] Drive Chrome extension against `nexara-dev.crjlab.net` (per memory: must visually verify on real browser).
  - Upload a `.tar.gz` template (regression check on existing flow).
  - Download Proxmox official `debian-12-standard` template via URL.
  - Pull `docker.io/library/hello-world:latest` via OCI dialog.
  - Browse appliance catalog, install `alpine-3.20-default`.
  - Create LXC from each: classic template, OCI template (check the preview badge).
  - Delete each from the storage detail page.
  - Hammer non-Ceph cluster: confirm UAC scopes show but stay read-only (per `feedback_uac_ceph_only.md`).
  - Confirm audit log entries exist for every mutation; expandable rows show details JSON.
- [ ] Verify failure paths: bad reference regex → 400 with friendly message; skopeo-missing → friendly message; permission-denied → 403 envelope.

## Open questions to settle as we implement

- Do we want to expose `decompression-algorithm` and `checksum` to the URL-download UI as advanced fields, or hide them behind a "Show advanced" toggle? (Lean: hide behind toggle.)
- Should `ListAppliances` be cluster-scoped or node-scoped on the API path? Proxmox returns the same data per-node (it's the upstream catalog), but the URL path requires a node. Lean: pick the cluster's healthiest node automatically inside the handler, take `cluster_id` only.
- For very large OCI images (multi-GB), the 30-minute storage handler timeout may not be enough. Easy fix if it bites: bump to 2h for OCI-pull endpoints specifically.

## Risks

- **OCI feature is tech-preview** — Proxmox could break/rename the endpoint in 9.2. We isolate the surface to one client method so a future patch is small.
- **Skopeo failures** are noisy — bad references can leave half-pulled tar files. The recent upstream patch series (Nov 2025) added "pull to temp file" — we benefit automatically as long as users are on a recent 9.1.x.
- **UI complexity** — the storage detail page is already busy. We add one toolbar row and three modals; if it feels cramped, fall back to a dropdown ("Add Template ▾").

## References

- Proxmox VE 9.1 release notes: https://www.proxmox.com/en/about/company-details/press-releases/proxmox-virtual-environment-9-1
- Linux Container wiki — OCI section: https://pve.proxmox.com/wiki/Linux_Container
- Source patch for the endpoint: https://lore.proxmox.com/pve-devel/20251008171028.196998-14-f.schauer@proxmox.com/
- Follow-up fixes: https://lore.proxmox.com/pve-devel/20251117171528.262443-1-f.schauer@proxmox.com/
- Walkthrough (Raymii): https://raymii.org/s/tutorials/Finally_run_Docker_containers_natively_in_Proxmox_9.1.html
- Terraform provider model (bpg/terraform-provider-proxmox): https://deepwiki.com/bpg/terraform-provider-proxmox/3.3.4-download-file-and-oci-image-resources
