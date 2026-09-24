# API Reference

Nexara exposes a REST API at `/api/v1`. All endpoints (except auth and health) require a valid JWT bearer token.

> **The live source of truth is the in-app catalog at `/settings/api-docs`.**
> That page enumerates every route from the running Fiber router at request
> time, so it cannot drift from what the server actually serves, and for each
> route the endpoint registry declares it renders the whole request contract —
> every parameter's type, where it goes on the wire, its bounds, and whether it
> is required. This file covers the auth handshake, error envelope, and
> WebSocket protocol — the parts the auto-generated catalog can't infer — plus
> a hand-curated overview of the major endpoint groups for offline reference.

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
`precondition_failed`, `request_entity_too_large`, `unsupported_media_type`,
`unprocessable_entity`, `upgrade_required`, `too_many_requests`,
`request_header_fields_too_large`, `internal_server_error`, `not_implemented`,
`bad_gateway`, `service_unavailable`), unless an endpoint sends a more
specific one of its own — cluster onboarding's `tfa_required` and
`token_exists`, the `*_confirm_required` confirmation gates, and the two
below, among others; `message` is the human-readable detail. Some of those
endpoint-specific errors also carry an optional `details` object of
structured context: `token_exists` does, and so do most of the
`*_confirm_required` gates.

- `veeam_auth_failed` (422) — Veeam refused the credential Nexara logged on
  with. Sent when adding, editing or testing a Veeam server, and by the job
  actions (start, stop, enable, disable) and the session stop, log and task
  routes. It is a 422 rather than a 401 on purpose: a 401 from Nexara means
  the caller's own session has expired, and the web UI would refresh its
  token and replay the request — spending a second failed logon against the
  Veeam account.
- `preflight_conflict` (409) — sent by `POST /clusters/:id/rolling-updates`
  for a job that drains its guests with `ha_policy` set to `strict`, when the
  HA or capacity pre-flight check finds a problem. The failing checks come
  back in a top-level `conflicts` array rather than in `details`.

Common HTTP status codes:

| Code | Meaning |
|------|---------|
| 400 | Bad request — invalid input |
| 401 | Unauthorized — missing or invalid token |
| 403 | Forbidden — insufficient permissions |
| 404 | Not found |
| 409 | Conflict — resource already exists |
| 413 | Request body too large — a `Content-Length` over 10 MiB, on every route but the storage upload |
| 415 | Unsupported media type — the request named a `Content-Encoding` other than `identity`; see Compressed Request Bodies |
| 429 | Rate limited |
| 500 | Internal server error |
| 502 | Bad gateway — Proxmox VE, PBS or Veeam could not be reached, or answered with an error of its own |
| 503 | Service unavailable — a background engine the endpoint needs is not running, or no node is online to do the work |

## Compressed Request Bodies

Send request bodies uncompressed. A request whose `Content-Encoding` names any
content coding other than `identity` — `gzip`, `br`, `zstd`, `deflate` or any
other — is refused on every endpoint with `415` and the `unsupported_media_type`
envelope before any handler reads the body, so the body is never decompressed.
The response carries `Accept-Encoding: identity`, which is how RFC 9110
§12.5.3 has a server say which codings it would have accepted: none but "no
encoding". The server then closes the connection, because a refused body is
never read and so nothing can safely follow it there; a client still sending
a large body at that point may see the connection reset rather than the 415.
Resend the request, on a new connection, without the header.

The refusal goes by the header alone, so it applies whatever the method and
whether or not a body follows. A compressed body can be thousands of times
smaller than what it decodes to, which would make every size limit on a
request body a limit on the compressed bytes only.

Responses are not affected: a client that sends `Accept-Encoding` still gets a
compressed response wherever the server compresses one.

## Trailing Slashes

Only a `GET`, `HEAD` or `OPTIONS` request may end its path in `/` — the three
methods Nexara answers as reads. Any other method (`POST`, `PUT`, `PATCH`,
`DELETE`, and also `TRACE` and `QUERY`, which no route serves) is refused on
every endpoint with `400` and the `bad_request` envelope before it reaches a
route: send `DELETE /api/v1/clusters/:id`, not `DELETE /api/v1/clusters/:id/`.
Reads are unaffected, because the router ignores a trailing slash:
`GET /api/v1/clusters/` still lists clusters.

The reason is the way browsers build a URL. The URL parser every browser
shares resolves a `.` or `..` path segment before the request is sent, so a
request aimed at `.../pools/..` goes out as `/api/v1/clusters/:id/` — the
object one level up, with a trailing slash. Refusing that slash means a name
made of dots can never turn a write on one object into a write on its
collection or its parent when the name is the path's LAST segment. A dot
segment in the middle of a path leaves no trailing slash
(`.../pools/../members` goes out as `/api/v1/clusters/:id/members`), so there
the client must refuse to send it: the web UI does, for every path it builds.

An API client must not send a `.` or `..` path segment at all, and
percent-encoding one does not help. A proxy may resolve it before Nexara sees
the request: Traefik does by default (its `sanitizePath` entry-point option),
after decoding `%2E` to `.`, and drops the slash a final dot segment would
leave, so `DELETE /api/v1/clusters/:id/pools/..` sent through it arrives as
`DELETE /api/v1/clusters/:id` — the cluster — with no slash to refuse. That
request is refused all the same, because a cluster delete must carry
`confirm` set to the cluster's name (below), and no request rewritten from
another route carries it. Sent to Nexara unresolved, the same path reaches
the pool route, whose `pool_id` rule refuses `.` and `..`.

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
| Cluster create | 10/min | `POST /clusters` — in bootstrap mode each call spends a Proxmox login attempt |
| General | `RATE_LIMIT_MAX` per `RATE_LIMIT_EXPIRATION` (default 600/min) | Everything whose path does not start with `/api/v1/auth/` or `/ws` — `/healthz` included |

The general limiter's exemption is by path prefix, not by coverage: the auth
paths listed above carry their own budgets, but the remaining `/api/v1/auth/*`
endpoints (`/auth/me`, `/auth/logout`, `/auth/change-password`,
`/auth/totp/setup`, `/auth/oidc/token-exchange`, …) and the `/ws`,
`/ws/console`, `/ws/vnc` upgrades have no request-rate limit at all.

## Request Parameters

Each endpoint declares its parameters — path, query and body alike — in one
place, and the declaration is enforced before the handler runs. Four
consequences are worth knowing before scripting against the API:

- **An undeclared key is rejected, not ignored.** A request carrying a key the
  endpoint does not declare answers `400` naming it: `siz: unknown parameter
  (not declared by this endpoint)`.
- **A declared parameter must arrive where it is declared.** Moving a body
  parameter onto the query string answers `400` — `vmid: must be sent in the
  request body, not as a query parameter` — which also keeps a body-only secret
  out of URLs that proxies and access logs record.
- **Values are coerced across wire spellings.** `500` and `"500"` are the same
  integer, and `true`, `1`, `yes` and `on` are all the same boolean, so a query
  string and a JSON body can say the same thing. `1.5` is still not an integer,
  and a repeated query key is how a query string spells an array. A parameter
  that declares a format is additionally **normalized** before the handler sees
  it: a disk size reaches Proxmox as a bare GiB count (`"1T"` becomes `1024`),
  and a UUID is lowercased.
- **A value outside a declared bound is a `400`, not a clamp** — see
  Pagination & Filtering below.

A rejection uses the error envelope above, with the parameter's name at the
front of `message`:

```json
{ "error": "bad_request", "message": "limit: must be at most 100 (got 500)" }
```

There is no per-field rejection map: only the first problem is reported, and
unknown keys are reported before missing ones, so a misspelled `size` comes
back as the unknown key it is rather than as a missing required `size`.

The check is on the request's own keys. The contents of an object-valued
parameter — a report's `parameters`, for instance — are the handler's to
validate and are documented with their endpoint. A handful of routes predate
the declaration layer and still parse their own input; the catalog does not
mark them, and an empty parameter list is not the tell — a declared endpoint
that declares no parameters renders identically, and refuses any query key,
and on a POST, PUT or PATCH any key in a JSON body. (The branding logo and
favicon uploads are declared that way too, and read their multipart form
fields themselves.)

## Response Envelope

Every endpoint that returns a **collection** returns the same envelope:

```json
{ "items": [ ... ], "total": 128 }
```

- `items` is always a JSON array — never `null`, even when empty.
- `total` is the number of rows matching the request's filters **before**
  `limit`/`offset`, so `total > items.length` means the response is one page of
  a larger set. On endpoints that return everything they have (most Proxmox
  passthroughs), the two are equal.

Single-resource endpoints (`GET /clusters/:id`, `GET /vms/:id`, …) return the
object directly, unwrapped.

> **Changed in v1.10.0 — breaking for API clients.** Collections previously
> returned three different shapes: a bare JSON array on most routes,
> `{items,total}` on `/audit-log` and `/tasks`, and `{entries,total}` on node
> syslog. The same logical resource disagreed with itself across routes —
> `/clusters/:id/audit-log` was a bare array while `/audit-log` was not — and a
> bare array had nowhere to carry `total`, so a caller could not tell a full
> page from a truncated one.
>
> All collections now use the envelope above. **A client doing `jq '.[]'` or
> `for row in response:` over a bare array will not error — it will silently
> read nothing, or iterate the envelope's two values.** Update such callers to
> read `.items`:
>
> ```bash
> # before
> curl -s "$B/vms" | jq -r '.[].name'
> # after
> curl -s "$B/vms" | jq -r '.items[].name'
> ```
>
> The bundled web UI ships in the same binary and was updated in the same
> change, so **no operator action is required on upgrade** — the container
> starts, migrations apply, the UI works. That is why this lands in a minor
> release despite changing the response contract: nothing about the deployment
> changes. What changes is what an external caller has to parse.

---

## Pagination & Filtering

List endpoints support query parameters:

| Parameter | Description | Example |
|-----------|-------------|---------|
| `limit` | Max items to return. The default and the ceiling are per-endpoint — most list endpoints default to 50 and cap at 100 or 200, a few default to 500 and cap higher, and a few declare no ceiling at all. Where a bound is declared, a value outside it is a `400` naming the bound, **not** a clamp | `?limit=100` |
| `offset` | Skip N items. A negative value — or one past the endpoint's ceiling, where it declares one — is a `400` rather than being clamped | `?offset=50` |

Result ordering is fixed per endpoint — there is no generic `sort`/`order`
parameter.

Some endpoints support additional filters documented in their sections below.

Global list endpoints are **scoped to the caller's accessible clusters**
before paging: `/alerts`, `/alert-rules`, `/audit-log`, `/audit-log/recent`,
`/migrations`, `/tasks`, `/reports/schedules` and `/reports/runs` return only
rows for clusters the caller can view with the endpoint's permission
(`view:alert`, `view:audit`, `view:migration`, `view:task`, `view:report`),
and the response's `total` counts the scoped set. Rows with no cluster
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
| POST | `/clusters` | Add a cluster, with a pasted API token or a Nexara-minted one |
| GET | `/clusters` | List all clusters |
| GET | `/clusters/:id` | Get cluster details |
| PUT | `/clusters/:id` | Update cluster |
| DELETE | `/clusters/:id` | Remove cluster. Requires `?confirm=<the cluster's current name>`, exactly: without it `400`; with any other value `400`, or the `409` (a rolling update is running) or `403` (revoking without global `manage:cluster`) that applies first; nothing is deleted or revoked (`?revoke_pve_credentials=1` also revokes a Nexara-minted credential) |
| POST | `/clusters/fetch-fingerprint` | Fetch TLS fingerprint from a Proxmox URL |

#### Onboarding a cluster without a pre-made token

`POST /clusters` takes either `token_id` + `token_secret`, or a `bootstrap`
block — never both. With `bootstrap`, Nexara signs in once with the supplied
password and creates the credential itself:

```json
{
  "name": "Production",
  "api_url": "https://pve.example.com:8006",
  "tls_fingerprint": "AB:CD:…",
  "bootstrap": {
    "username": "root@pam",
    "password": "…",
    "otp": "123456",
    "user_id": "nexara@pve",
    "token_name": "nexara"
  }
}
```

`otp` is needed only when the account has TOTP two-factor authentication
enabled — it rides the same `/access/ticket` request, so hardware factors
(WebAuthn/U2F), which need a challenge exchange, cannot be used here.
`user_id` and `token_name` default to `nexara@pve` and `nexara`; `user_id` may
not name a `@pam` account, since that realm maps to real shell users on the
node. `username` (the account you log in AS) is unrestricted — `root@pam` is
the normal answer.

The password is spent on a single `POST /access/ticket` and never stored. What
Nexara keeps is the minted token's ciphertext; **the secret is never returned to
the client** — folding the mint into the create call means it crosses the wire
zero times. The response carries a `bootstrap` summary of what was created:

```json
{
  "cluster": { "…": "…", "credential_source": "bootstrap" },
  "connectivity": { "reachable": true, "message": "…" },
  "bootstrap": {
    "token_id": "nexara@pve!nexara",
    "steps": [
      { "step": "user",   "status": "created",  "detail": "nexara@pve" },
      { "step": "acl",    "status": "created",  "detail": "Administrator on /" },
      { "step": "token",  "status": "created",  "detail": "nexara@pve!nexara" },
      { "step": "verify", "status": "verified", "detail": "authenticated with the new token" }
    ]
  }
}
```

The flow is forward-idempotent: a rerun after a partial failure adopts whatever
already exists (`"status": "existed"`). The user is created **without a
password**, which is what makes every half-finished state inert.

Objects created by a run that then fails are rolled back — and only those, never
an adopted pre-existing user or grant. Whatever the rollback cannot remove is
recorded in a `cluster_bootstrap_failed` audit row naming it, so a failed
onboarding is never silent even though no cluster row exists to attach it to.

Failures specific to the supplied Proxmox credential answer **422**, not 401 —
a 401 would be indistinguishable from an expired Nexara session, and clients
that refresh-and-replay on 401 would double every login attempt against the
hypervisor:

| `error` | Status | Meaning |
|---------|--------|---------|
| `tfa_required` | 422 | The account needs a one-time code; resubmit with `otp` |
| `bootstrap_auth_failed` | 422 | Wrong username or password |
| `bootstrap_forbidden` | 422 | The account cannot create users or tokens |
| `token_exists` | 409 | That token name is taken. Nexara never auto-suffixes — each auto-named retry would leave another live `privsep=0` Administrator credential nobody holds |

Deleting the cluster with `?revoke_pve_credentials=1` removes the PVE objects
Nexara created, and only those: `credential_source` must be `bootstrap`, and
each object is revoked only if Nexara recorded creating it. A pasted-in token is
never touched.

Four further constraints:

- **It needs global `manage:cluster`**, not just the `delete:cluster` that the
  deletion itself requires. Minting the credential took global `manage:cluster`;
  removing it from a live hypervisor is the same class of act, and
  `delete:cluster` can be granted scoped to one cluster.
- **Nothing on the account is touched while another cluster in Nexara still uses
  it.** If a second cluster row authenticates as the same PVE user on the same
  host, only this cluster's own token is removed. Revoking the shared
  `Administrator` grant would de-privilege that cluster's `privsep=0` token —
  leaving it listed and healthy-looking in Nexara while unable to reach Proxmox
  at all.
- **The user is never cascade-deleted while it owns another API token.**
  `DELETE /access/users` takes every token and ACL entry with it, so if the
  account holds anything Nexara did not mint for this cluster — the same host
  onboarded twice, or a token added by hand — only this cluster's own grant and
  token are removed. If the token list cannot be read, it fails closed the same
  way.
- **Revocation runs after the cluster row is deleted**, so a failed deletion
  cannot leave a cluster present in Nexara whose credential is already gone.

Revocation is best-effort — an unreachable cluster still deletes, and what was
left behind is recorded in the audit row.

`PUT /clusters/:id` clears the provenance columns when `api_url` or `token_id`
changes: those fields describe objects on the target the cluster used to have,
and acting on them against a new host would delete something Nexara never
created. Rotating only `token_secret` is a regenerate, not a re-point, and
leaves provenance intact.

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

> `:node` is the Proxmox node *name* (e.g. `pve-01`); `:node_id` is Nexara's own
> node UUID, as returned in the `id` field of `GET /clusters/:id/nodes`. They
> are not interchangeable — a route taking `:node_id` rejects a node name with
> `400 node_id: expected a UUID such as …`.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/nodes` | List nodes in a cluster |
| GET | `/clusters/:id/nodes/:node/bridges` | List network bridges |
| GET | `/clusters/:id/nodes/:node/hardware/usb` | List USB devices |
| GET | `/clusters/:id/nodes/:node/hardware/pci` | List PCI devices |
| GET | `/clusters/:id/nodes/:node/machine-types` | List available machine types |
| GET | `/clusters/:id/nodes/:node/cpu-models` | List available CPU models |
| GET | `/clusters/:id/nodes/:node/cpu-flags` | List available VM CPU flags, with the nodes supporting each |
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
| GET | `/clusters/:id/nodes/:node/syslog` | Read node syslog over a time window |
| GET | `/clusters/:id/nodes/:node/journal` | Read the node's systemd journal by line count or cursor |

#### Reading node logs

Two endpoints, because Proxmox exposes two mechanisms and neither covers the
other's common case.

**`GET /clusters/:id/nodes/:node/syslog`** — a time window.

| Parameter | Description |
|-----------|-------------|
| `since`, `until` | Window bounds. Accepts `YYYY-MM-DD`, `YYYY-MM-DD HH:MM[:SS]`, a relative offset (`-1h`, `30m ago`, `2d`), or a unix timestamp. Invalid or out-of-range values return `400`, not `500`. |
| `start`, `limit` | Offset into the matched lines and page size (default 500, max 5000). `start` defaults to fetching the *newest* `limit` lines. |
| `service` | Restrict to one systemd unit. |

> **Timezone.** Proxmox hands `since`/`until` to `journalctl` on the node, which
> reads a wall-clock string in the **node's local timezone**. The two forms are
> therefore handled differently, and the distinction is the whole ballgame:
>
> - A **wall-clock string** you supply is passed through untouched — you wrote
>   it, and the node reads it, in its own local terms.
> - A **relative offset or unix timestamp** names an absolute instant, so the
>   API resolves the node's UTC offset (from Proxmox's `/nodes/{node}/time`) and
>   renders the wall clock that node would show for it. `?since=1h` means one
>   hour ago in real terms on every node, whatever its timezone.
>
> Rendering those as UTC instead points at the node's *future* on any node
> behind UTC, and journalctl answers with its literal `-- No entries --`. If the offset lookup fails the API falls back to
> UTC and logs a warning, so an empty result on a non-UTC node is worth checking
> the server log for.
>
> One residual: the offset is read as of *now*, so a window spanning a
> daylight-saving transition is off by the DST delta for part of its span.
>
> When `since` is omitted it defaults to **the date 24 hours ago** — so between
> 24 and 48 hours of journal, depending on the hour. Date-only because that is
> the form Proxmox has always accepted, and a full day back because it is in the
> past for any real UTC offset.
>
> Before v1.10.0 the default was *today's* date in UTC, which is a future
> timestamp for any node west of UTC — those nodes answered with journalctl's
> literal `-- No entries --`, which reads like a node with no logs. The web UI's
> "Today" preset had the same bug against the browser's timezone; its presets
> are now relative offsets, which carry no date to disagree about.
>
> `since`/`until` are capped at 90 days back. Unbounded, `?since=1` asks
> journalctl to walk the entire journal — and with the default paging the API
> first asks Proxmox to *count* every matching line, so the cost lands on the
> hypervisor.

**`GET /clusters/:id/nodes/:node/journal`** — a line count or a cursor.

| Parameter | Description |
|-----------|-------------|
| `lastentries` | The newest N lines, 1 to 5000. The common case, and the one `syslog` cannot express. A value above 5000 is a 400, not clamped; defaults to 500 when no other bound is given. |
| `since`, `until` | Same accepted forms as above, but sent to Proxmox as **unix timestamps**, so no wall clock — and no node timezone — is involved at any point. A wall-clock string given here is read as UTC. |
| `startcursor`, `endcursor` | Opaque journal cursors, for resuming a read. |

`items` is a flat array of raw journal lines (strings), unlike `syslog`'s
`{n, t}` objects.

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
| POST | `/clusters/:id/vms/:vm_id/disks/attach` | Allocate and attach a disk. Omit `index` to take the lowest free slot on the bus; an occupied slot, or one the VM boots from, is refused. `size` takes `500`, `"500"`, `"500G"`, `"512M"` or `"1T"` and may not exceed the target pool's total capacity |
| POST | `/clusters/:id/vms/:vm_id/disks/detach` | Detach a disk by its config key. A drive key (`ide0`–`ide3`, `sata0`–`sata5`, `scsi0`–`scsi30`, `virtio0`–`virtio15`, `efidisk0`, `tpmstate0`) parks a volume the VM owns as `unusedN`, except a cloud-init drive, which is deleted; `unused0`–`unused255` and `vmstate` delete the volume from storage if this VM owns it. Any other config key is refused |
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
| GET | `/clusters/:id/scan/iscsi` | Discover iSCSI targets on a portal — `?portal=<host[:port]>`; each item is `{ "target", "portal" }`. **Requires `manage:storage`** (node-side network probe) |

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

### Windows Guest Tools

Tracks the virtio-win drivers and QEMU guest agent installed inside Windows
guests, and stages updates to them. Reads need `view:guest_tools`, policy
changes `manage:guest_tools`, and staging or cancelling an update
`execute:guest_tools`.

Nothing here runs the installer through `guest-exec`: installing the tools
restarts QEMU-GA, which is the very channel the install would be driven through,
so a direct run kills its own session mid-install. Instead a script is written
into the guest and registered as a scheduled task running as SYSTEM, which
executes detached — at the next boot, or immediately on request — leaving a
result file Nexara reads back. That is both the workaround and the natural
implementation of "update on reboot".

Cluster `mode` is `disabled` (default), `report` (detect only, never write into a
guest) or `staged` (detect and stage updates). Per-guest state moves
`idle → staging → staged → running → succeeded` or `failed`.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/guest-tools/config` | Get the cluster's Windows guest tools update policy |
| PUT | `/clusters/:id/guest-tools/config` | Update the cluster's Windows guest tools update policy |
| GET | `/clusters/:id/guest-tools/guests` | List Windows guests with installed guest tools versions and update state |
| POST | `/clusters/:id/guest-tools/guests/:vmid/detect` | Probe a guest for its installed guest tools version |
| PUT | `/clusters/:id/guest-tools/guests/:vmid/policy` | Pin a version or exclude a guest from guest tools updates |
| POST | `/clusters/:id/guest-tools/guests/:vmid/update` | Stage a guest tools update for next boot, or run it now |
| DELETE | `/clusters/:id/guest-tools/guests/:vmid/update` | Cancel a staged guest tools update |

### virtio-win ISOs

Acquires the ISO an update installs from. Gated on the **storage** grants
(`view:storage` / `manage:storage`) rather than the guest-tools ones, because
downloading writes into a Proxmox storage.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/virtio-win/releases` | List known upstream virtio-win releases |
| GET | `/virtio-win/mirror` | Get the instance-wide download source |
| PUT | `/virtio-win/mirror` | Point downloads at a mirror, for air-gapped installs (`manage:settings`) |
| GET | `/clusters/:id/virtio-win/config` | Get the cluster's virtio-win auto-download policy |
| PUT | `/clusters/:id/virtio-win/config` | Update the cluster's virtio-win auto-download policy |
| GET | `/clusters/:id/virtio-win/downloads` | List virtio-win download history for the cluster |
| POST | `/clusters/:id/virtio-win/check` | Run the cluster's check now, off-schedule |
| POST | `/clusters/:id/virtio-win/download` | Download a virtio-win ISO to the configured storage now |

The release catalogue is global — every cluster sees the same upstream list.
Upstream publishes **no ISO checksum** (its `CHECKSUM` file covers only the
RPMs), so a downloaded ISO is not hash-verified — which is why the source base
URL should be HTTPS. Downloads are dispatched asynchronously and reconciled from
their UPID, so a pass survives a Nexara restart.

**Check schedule.** The config carries `check_schedule` (a five-field cron
expression; empty means every six hours, counted from the last check) and
`check_timezone` (an IANA zone; empty means the server's own). `next_check_at`
is when the next one is due — `null` reads as *due now*, which is what a
just-enabled cluster and a config upgraded in place both carry.

Rejected on write rather than stored: an unparseable expression, an unknown
zone, and an expression that parses but never comes round (`0 3 31 4 *` —
April 31, and the Feb 30 / Sep 31 variants). The last is not pedantry: the cron
library range-checks each field on its own, accepts the date, and then answers
"next occurrence" with the zero time — which as a `next_check_at` is
permanently in the past, i.e. due on every tick forever.

`POST .../check` refreshes the catalogue from the source and reconciles the
cluster's storage, downloading only if the target ISO is missing; it records the
outcome exactly as the scheduler does, so the next check moves to its next slot.
It answers `{"config": …}`, plus `"download"` when one was dispatched.

**Download source.** `PUT /virtio-win/mirror` takes `{"base_url": …}` and
replaces `fedorapeople.org` for both discovery and the URL handed to the node.
It is instance-wide — the catalogue it fills is global — which is why writing it
needs `manage:settings` rather than `manage:storage`; reading it needs only
`view:storage`. The mirror is expected to mirror the upstream layout, so that
`<base>/archive-virtio/virtio-win-<version>/` holds each ISO.

Two shapes need an explicit confirmation, each returned as a `422` the caller
re-submits with a flag: `insecure_source_confirm_required` for a plain-HTTP base
(`allow_insecure: true`), and `private_address_confirm_required` for one
resolving to a private or loopback address (`allow_private_address: true`) — the
normal case for an internal mirror. Cloud-metadata and other never-routable
addresses are a `400` and cannot be confirmed through. The key is reserved
against the generic settings endpoints, so it cannot be written unvalidated.

### Resource Pools

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/pools` | List resource pools |
| POST | `/clusters/:id/pools` | Create pool |
| GET | `/clusters/:id/pools/:pool_id` | Get pool details |
| PUT | `/clusters/:id/pools/:pool_id` | Update pool |
| DELETE | `/clusters/:id/pools/:pool_id` | Delete pool |

Creating a pool whose id is exactly `.` or `..` is refused with `400`. That is
Nexara's rule, not Proxmox's — its pool-id format admits both — and it exists
because no browser can send either as a path segment, so the pool could never
be read, edited or deleted here afterwards.

### Proxmox Access Control

Manages a **cluster's own** Proxmox users, API tokens, groups, roles and ACLs —
distinct from Nexara's local users and roles under `/rbac` and `/admin`.

Reads need `view:access`; writes need `manage:access`, which is granted only to
the built-in Admin role by default (it can mint a token with cluster-wide
Administrator rights, which bypasses Nexara's own RBAC).

Realms are read-only: creating or editing one requires the `Realm.Allocate`
privilege, which no bundled Proxmox role except `Administrator` carries.

| Method | Path | Description |
|--------|------|-------------|
| GET | `/clusters/:id/access/users` | List Proxmox users |
| POST | `/clusters/:id/access/users` | Create a Proxmox user |
| GET | `/clusters/:id/access/users/:userid` | Get a Proxmox user |
| PUT | `/clusters/:id/access/users/:userid` | Update a Proxmox user |
| DELETE | `/clusters/:id/access/users/:userid` | Delete a user and every token it owns |
| GET | `/clusters/:id/access/users/:userid/tokens` | List a user's API tokens |
| GET | `/clusters/:id/access/users/:userid/tokens/:tokenid` | Get token metadata (never the secret) |
| POST | `/clusters/:id/access/users/:userid/tokens/:tokenid` | Mint an API token — returns the secret **once** |
| PUT | `/clusters/:id/access/users/:userid/tokens/:tokenid` | Update a token, or regenerate its secret |
| DELETE | `/clusters/:id/access/users/:userid/tokens/:tokenid` | Revoke an API token |
| GET | `/clusters/:id/access/groups` | List groups |
| POST | `/clusters/:id/access/groups` | Create a group |
| GET | `/clusters/:id/access/groups/:groupid` | Get a group and its members |
| PUT | `/clusters/:id/access/groups/:groupid` | Update a group |
| DELETE | `/clusters/:id/access/groups/:groupid` | Delete a group |
| GET | `/clusters/:id/access/roles` | List roles and their privileges |
| POST | `/clusters/:id/access/roles` | Create a custom role |
| GET | `/clusters/:id/access/roles/:roleid` | Get one role's privilege map |
| PUT | `/clusters/:id/access/roles/:roleid` | Replace or extend a role's privileges |
| DELETE | `/clusters/:id/access/roles/:roleid` | Delete a custom role |
| GET | `/clusters/:id/access/acl` | List access control entries |
| PUT | `/clusters/:id/access/acl` | Grant, or revoke with `"delete": true` |
| GET | `/clusters/:id/access/domains` | List authentication realms (read-only) |
| GET | `/clusters/:id/access/domains/:realm` | Get one realm (read-only) |
| GET | `/clusters/:id/access/permissions` | Report what Nexara's own cluster token may do |

**Token secrets are returned exactly once.** Proxmox has no read-back endpoint,
so the `value` field in the create/regenerate response is the only copy that
will ever exist outside the cluster. It is deliberately excluded from audit
records.

**Self-protection.** Deleting or regenerating the token Nexara authenticates
with returns `409` with an explanation instead of proceeding. Append
`?force=true` to override — the cluster will then show as unreachable until you
update its credentials.

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
| POST | `/clusters/:id/ceph/pools` | Create Ceph pool. Proxmox runs this in a background worker, so it answers `202` with the task `upid` — the pool does not exist yet |
| DELETE | `/clusters/:id/ceph/pools/:name` | Delete Ceph pool and its data. Also a background worker: `202` with the task `upid`. Proxmox refuses outright, before dispatching, if a storage still references the pool and holds disks |
| GET | `/clusters/:id/ceph/osds/:osd_id/preflight?action=` | Redundancy verdict for a proposed OSD action (`out` by default), cross-referencing OSD counts against pool `size`/`min_size`. A pool-config read failure does not block it — the gap is flagged instead |
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
| GET | `/pbs-servers/:id/prune-jobs` | List prune jobs, optionally narrowed with `?store=`. Retention is reported from prune jobs, not `datastore.cfg` alone |

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

### Veeam Backup & Replication

A **second backup provider alongside PBS**, not a replacement. Requires VBR
**13.1+** with an Enterprise Plus licence, reachable on its REST API port (9419
by default — the API root, not the console URL).

Reads need `view:veeam`, writes `manage:veeam`, removing a server
`delete:veeam`, and job control `execute:veeam` — a separate grant, held by the
built-in Admin and Operator roles and mirrored onto any custom role that already
held `manage:backup`.

Authentication differs enough from PBS that the two are modelled separately: VBR
uses an OAuth2 password grant against a single admin account (often
`DOMAIN\user`, where the backslash is significant), negotiates an
`x-api-version` per server, and identifies work with plain UUIDs rather than
UPIDs. One Veeam server can also protect *several* Proxmox clusters, where a PBS
server maps to at most one.

Because every call spends a real domain logon, two dedicated rate limits apply:
**10/min per IP** shared by create, update and test, and a separate **30/min per
IP** for job control and session logs.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/veeam-servers` | Register a Veeam Backup & Replication server (VBR 13.1+). Connects and records the negotiated API revision, product version and licence edition; a failed connection is a failed create |
| GET | `/veeam-servers` | List registered Veeam Backup & Replication servers |
| GET | `/veeam-servers/:id` | Get Veeam server details |
| PUT | `/veeam-servers/:id` | Update a Veeam server. Changing the URL, credentials or TLS handling re-tests the connection; renaming or disabling does not |
| DELETE | `/veeam-servers/:id` | Remove a Veeam server and its stored credential |
| POST | `/veeam-servers/:id/test` | Test the stored connection and report version, licence edition and covered Proxmox clusters. Persists nothing |
| GET | `/veeam-servers/:id/platforms` | List the Veeam platforms (Proxmox connections) this server protects, and the Nexara cluster each is mapped to |
| PUT | `/veeam-servers/:id/platforms/:platform_id` | Map a Veeam platform to a Nexara cluster. Every cluster-scoped Veeam permission resolves through this mapping, so the write needs global manage:veeam |
| GET | `/veeam-servers/:id/repositories` | List Veeam backup repositories with capacity. Global scope — one repository holds every cluster's backups |
| GET | `/veeam-servers/:id/repositories/:repository_id/metrics` | Repository capacity over time. `?range=` accepts `24h`, `7d`, `30d` or `90d` (default `7d`) |
| GET | `/veeam-servers/:id/jobs` | List Veeam Proxmox backup jobs with their last result and progress |
| POST | `/veeam-servers/:id/jobs/:job_id/start` | Start a Veeam backup job. Async — the returned session is a starting state, not a result. A job with no objects to process returns started=false and creates no run |
| POST | `/veeam-servers/:id/jobs/:job_id/stop` | Stop a Veeam job's running session. Recorded as Nexara-initiated so veeam_job_failed does not fire for it — Veeam itself records a cancelled run as "Failed" |
| POST | `/veeam-servers/:id/jobs/:job_id/enable` | Put a Veeam job back on its schedule |
| POST | `/veeam-servers/:id/jobs/:job_id/disable` | Take a Veeam job off its schedule. Protection stops accruing while existing restore points remain, and Veeam raises no alarm about it |
| GET | `/veeam-servers/:id/sessions` | List recent Veeam Proxmox backup runs |
| GET | `/veeam-servers/:id/sessions/:session_id/tasks` | Per-guest breakdown of one run: which guests it processed and which failed, linked to their Nexara guest where the name resolves unambiguously. Empty while a run is still in flight — Veeam reports task rows only as tasks finish |
| GET | `/veeam-servers/:id/sessions/:session_id/logs` | Read a session's log, live from Veeam. Empty is the NORMAL result for a stopped run — Veeam keeps no records for a killed session |
| POST | `/veeam-servers/:id/sessions/:session_id/stop` | Stop one running Veeam session. Recorded as Nexara-initiated, as for the job stop |
| GET | `/veeam-servers/:id/backup-objects` | List backed-up guests. One row per (guest x backup), so a guest covered by several jobs appears more than once |
| GET | `/veeam-servers/:id/backup-objects/:object_id/restore-points` | List a backup object's restore points, with malware status and file-level-restore availability |
| PUT | `/veeam-servers/:id/backup-objects/:object_id/guest` | Pin a backup object to a guest by VMID, overriding automatic correlation |
| GET | `/veeam-servers/:id/orphaned-objects` | List backup objects whose platform is mapped but which match no guest on it — restore points held for machines that no longer exist in the form that was backed up |
| GET | `/veeam-servers/:id/infrastructure` | List Veeam's own guests on the cluster — worker appliances and the VBR server. Coverage excludes these |
| GET | `/clusters/:id/vms/:vm_id/veeam` | Per-guest Veeam protection: correlated backup object, restore points, malware verdict, and the job protecting it |

**Map platforms before expecting data.** Everything Veeam returns carries a
`platformId` identifying one Proxmox connection — the only cluster discriminator
the REST API exposes. `cluster_id` stays `NULL` until an operator confirms the
mapping, and rows hanging off an unmapped platform cannot be attributed to a
cluster, so they require *global* `view:veeam` rather than a cluster-scoped
grant. Until a platform is mapped, its jobs and backup objects do not appear on
the cluster pages.

**Correlation is on the SMBIOS UUID, not the name.** Veeam's backup-object
`objectId` *is* the guest's `smbios1` UUID, which makes the match deterministic
and is what makes the orphan listing possible at all — a name match would
silently report a rebuilt guest as protected by a backup of the machine it
replaced. Manual pins (`PUT …/backup-objects/:object_id/guest`) are exempt from
re-correlation and survive collector churn and renames, but are invalidated if
the pinned VMID comes to belong to a different machine.

**Changing a server's base URL requires re-entering the password.** A stored
credential is only ever sent to the address it was saved for; the refusal is
audited as `veeam_server_credential_redirect_refused`.

Four inventory-backed alert metrics come with it: `veeam_rpo_hours` (cluster and
VM scope; cluster reports the worst guest), `veeam_malware_status` (Clean 0,
Informative 1, Suspicious 2, Infected 3), `veeam_repo_used_percent` (**global**
scope only — one repository holds every cluster's backups) and
`veeam_job_failed` (cluster scope only, excluding runs Nexara itself stopped).

Two more read the Proxmox task history Nexara already collects: `pve_backup_failed`
and `pve_task_failed` (both cluster scope only) count tasks that failed inside the
rule's `duration_seconds` window — for these that field is the window being
counted, not a persistence requirement, so they fire on the first evaluation
that sees a failure rather than waiting the window out. They also accept a
`duration_seconds` up to 2592000 (30 days) where every other metric is capped at
86400. A task Proxmox finished with `WARNINGS: N` counts as a success, matching
the Tasks page; one whose status Proxmox can no longer report (`vanished`) is
not counted at all, since that is lost track of rather than failed.

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
| DELETE | `/reports/runs/:id` | Delete a run and its stored renderings (`manage:report`) |
| POST | `/reports/runs/:id/email` | Email a finished run through an email channel (`generate:report`). Body: `{"channel_id": "…", "recipients": ["…"], "with_csv": false}`; empty `recipients` uses the channel's own list |

Report types: `cluster_digest`, `backup_compliance`, `resource_utilization`, `vm_resource_usage`, `capacity_forecast`, `snapshot_inventory`, `patch_status`, `uptime_summary`.

`POST /reports/generate` and the schedule endpoints accept a `parameters` object, also returned on every run and schedule. Every key has a server-side default, so `{}` is the report as documented: `stale_after_hours` (1–8760, default 24 — backup compliance and the digest), `top_n` (1–100, default 10 — VM resource usage), `snapshot_warn_days` (1–3650, default 7 — snapshot inventory and the digest), and `sections` (`{"runs": false}` switches an optional section off). Values outside these ranges are rejected with 400.

Every schedule carries `run_as`: the user whose grants its runs read under, stamped with whoever last saved it (`created_by` never changes). A run refuses to start while that user is deactivated.

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
| GET | `/clusters/:id/audit-log` | List audit entries for a cluster (supports `limit`/`offset`) |

Both audit routes share one filter set: `resource_type`, `user_id`, `action`,
`source`, `start_time`/`end_time` (RFC 3339), and `vmids` — a comma-separated
list of guest VMIDs, matching the same filter on `/tasks`. `/audit-log` also
takes `cluster_id`. `/clusters/:id/audit-log` does not: the path already names
the cluster, so a `?cluster_id=` there is refused with a 400
(`cluster_id: must be sent in the request path, not as a query parameter`)
rather than being overridden.

#### Guest identity on an audit entry

Every entry carries `resource_vmid` and `resource_name` describing the guest it
concerns (`0` and `""` when the entry names no guest — a login, a settings
change, a node action).

`resource_vmid` reads a VMID denormalized onto the row **at insert time**,
derived from `details.vmid`, the task's UPID, or the guest the entry pointed at
when it was written. `resource_name` prefers the name recorded in `details` at
the time of the action, falling back to the guest's current name.

This matters because the alternative — resolving the guest through
`resource_id` at read time — silently stops working: Nexara's collector deletes
and re-inserts a guest's inventory row on resync, so the UUID an entry recorded
resolves until it doesn't, and a destroyed guest never resolves at all. Before
v1.10.0 both fields were derived that way and were empty on the large majority
of entries, including every `destroy`. Entries written before the upgrade are
backfilled where the information survives in `details`; the few that recorded
neither a VMID nor a UPID stay empty.

Both fields are also emitted as first-class `vmid` and `resource_name` params in
the RFC 5424 structured data of the syslog forwarder and the `format=syslog`
export, so a SIEM decoder can key a rule on the guest without re-parsing the
`details` JSON. They are appended after `details`, so params an existing decoder
already matches keep their position.

#### Export

`GET /audit-log/export?format=json|csv|syslog` returns a downloadable file,
capped at 10 000 rows. The JSON form uses the standard list envelope, so
`total > items.length` tells you the export hit that cap — narrow the time range
and re-run. The CSV and syslog forms are line-based and carry no such marker.

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
