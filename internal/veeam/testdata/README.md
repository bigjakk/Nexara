# Veeam REST API fixtures

Captured 2026-08-25 from a live **Veeam Backup & Replication 13.1.0.411** server
(Windows, NFR EnterprisePlus) with a real Proxmox VE cluster attached — 11
`ProxmoxBackupJob` jobs, 27 Proxmox backup objects, 162 Proxmox restore points.

API revision: **`1.3-rev2`** — the same value as `veeam.DefaultRevision`. If that
constant moves, these need recapturing: the whole point of pinning the revision
is that the schema is allowed to change between them.

These exist so `internal/veeam` can be built and tested against real response
shapes without a live Veeam server. Serve them from an `httptest.Server`; see
`client_test.go` for the harness.

## Redaction

Sanitised before committing. Real hostnames, usernames, org name, bucket names
and repository names were substituted; **access and refresh tokens were replaced
with shape-preserving placeholders** (`eyJREDACTED.…`) and are not valid
credentials. UUIDs were deliberately kept so cross-file references stay intact —
`jobs_states.json` `sessionId` still resolves into `sessions_list.json`, and
`backupobjects_list.json` `backupId` still resolves into `backups_list.json`.

Substitutions applied: `*.ad.<internal>.net` → `*.example.com`, user → `jdoe`,
org → `EXAMPLE`/`ExampleOrg`, repositories → `repo-nas-01` / `example-bucket`,
object-store endpoint → `s3.object-store.example.com`, worker appliances →
`vbr01-worker01..03`.

The workers' mixed case (`vbr01-worker01` beside `Vbr01-worker02`) is preserved
deliberately: the guest-resolution tests turn on it, and a substitution that
regularised the case would leave them passing against a case-SENSITIVE match.
When recapturing, a worker name appears in exactly two places — `name` on each
`"type": "PVE"` row of `backupinfrastructure_proxies_states.json`, and each
`Preparing worker …` session name in `sessions_list.json`. Leave the
GeneralPurposeProxy and ViProxy rows in that file alone: their product-default
names are what prove the worker filter keys on `type`, not on the name.

`swagger_index.js` is the server's Swagger UI bootstrap verbatim — it carries no
hostname or identifier, so nothing needed redacting.

## Files

| File | Endpoint |
|---|---|
| `swagger_index.js` | `GET /swagger/index.js` — **unauthenticated**; the revision list |
| `token_password_grant.json` | `POST /api/oauth2/token` (`grant_type=password`) |
| `error_bad_credentials.json` | same, wrong password — **401** |
| `error_bad_api_version.json` | same, unsupported `x-api-version` — **400** |
| `serverinfo.json` | `GET /api/v1/serverInfo` |
| `license.json` | `GET /api/v1/license` — edition gate + per-workload Proxmox list |
| `jobs_list.json` | `GET /api/v1/jobs` — ⚠️ contains **no** Proxmox jobs |
| `jobs_states.json` | `GET /api/v1/jobs/states` — the real Proxmox job source |
| `backups_list.json` | `GET /api/v1/backups` |
| `backupobjects_list.json` | `GET /api/v1/backupObjects` |
| `restorepoints_list.json` | `GET /api/v1/restorePoints` |
| `sessions_list.json` | `GET /api/v1/sessions` |
| `backupinfrastructure_repositories.json` | `GET /api/v1/backupInfrastructure/repositories` |
| `backupinfrastructure_repositories_states.json` | `…/repositories/states` — capacity in float **GB** |
| `backupinfrastructure_managedservers.json` | `…/managedServers` — ⚠️ Proxmox server is **not** listed; the VBR server itself is (`isBackupServer`) |
| `backupinfrastructure_proxies.json` | `…/proxies` — ⚠️ contains **no** Proxmox workers |
| `backupinfrastructure_proxies_states.json` | `…/proxies/states` — the real Proxmox worker source |

Phase 1 exercises the first six. The rest are captured ahead of Phase 2 so the
inventory sync can be written against real shapes on day one — deliberately
committed unused rather than left in a scratch directory to rot.

## Gotchas these fixtures encode

- Veeam's published HTML reference **strips group prefixes**: the real path is
  `/api/v1/backupInfrastructure/repositories`, not `/api/v1/repositories`. Build
  paths from the server's own `swagger.json`, not the docs.
- `jobs_list.json` vs `jobs_states.json` — Proxmox jobs appear only in the
  latter. `GET /jobs/{id}` rejects them outright with
  `400 "Specify job of supported platform type."`
- `backupinfrastructure_proxies.json` vs `…_proxies_states.json` — **the same
  trap again**, and documented nowhere. The plain listing returned 2 rows on
  the live server and none of them were Proxmox; `/states` returned 5,
  including all three `"type": "PVE"` worker appliances. Assume any
  Proxmox-bearing collection has this shape until proven otherwise.
- A `PVE` proxy carries **no smbios uuid and no vmid** — only `name` and the
  Proxmox node in `hostName`. Name is therefore the only key for matching a
  worker to a guest, which is why the match is exact and case-insensitive
  rather than a prefix test: the capture holds `vbr01-worker01` beside
  `Vbr01-worker02`, and a worker's name commonly shares a prefix with the VBR
  server's own guest — here `vbr01`, which a prefix test would collide with.
- Workers report `isOnline: false` in the capture, and that is the **healthy**
  steady state — Veeam powers an appliance on for a job and off afterwards.
- The backup server's identity for guest matching is `managedServers.name`,
  the FQDN. `serverInfo.name` is the short form ("vbr01" against a guest
  named `vbr01.example.com`) and matches nothing.
- `backupobjects_list.json` has duplicate `name` values AND **duplicate `id`
  values**: 27 Proxmox rows carry only 18 distinct ids. `id` is the **guest's**
  identity within Veeam and `backupId` is what differs, so the collector folds
  the listing to one row per guest — summing `restorePointsCount`, which is
  per-backup, and taking `size` as-is because it is identical across a guest's
  rows.
- Restore points come from `/backupObjects/{id}/restorePoints`, **not** the
  bulk `restorepoints_list.json` path: the bulk listing carries no object id,
  so its rows could only be linked back by name — and a rebuilt guest reuses
  its name.
- `sessions_list.json` is dominated by `ConfigurationResynchronize` — filter
  server-side with `typeFilter`, or the poll budget is spent on noise.
- Proxmox sessions are `sessionType: "PlatformBackupJob"` +
  `platformName: "Proxmox"`; there is no Proxmox value in `ESessionType`.
- `swagger_index.js` lists revisions as `V1.3-REV2` (uppercase) in `name` but
  `/swagger/v1.3-rev2/swagger.json` in `url`. The `x-api-version` header takes
  the bare lowercase form with no `v` prefix.
