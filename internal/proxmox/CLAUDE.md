# internal/proxmox — Proxmox API Client

## Client Pattern
- All methods live on the `Client` struct
- Use typed request/response structs for every endpoint
- Auth via API tokens (never username/password in production)
- TLS fingerprint verification for self-signed certs

## Adding a New API Method
1. Define request/response structs with JSON tags
2. Add method on `*Client` with descriptive name (e.g., `GetNodeStatus`)
3. Use typed errors — wrap with context: `fmt.Errorf("get node %s: %w", id, err)`
4. Handle HTTP status codes explicitly (404 → ErrNotFound, 403 → ErrForbidden)

## Error Types
- `ErrNotFound` — resource doesn't exist
- `ErrForbidden` — insufficient permissions
- `ErrConnectionFailed` — node unreachable

## Reference
- PVE API docs: https://pve.proxmox.com/pve-docs/api-viewer/
- PBS API follows the same patterns
