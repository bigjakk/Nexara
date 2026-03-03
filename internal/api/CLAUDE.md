# internal/api — Handler Conventions

## Adding a New Handler
1. Create a file in `handlers/` named after the resource (e.g., `nodes.go`)
2. Register routes in `router.go` using the middleware chain
3. Always validate input before processing
4. Always check RBAC permissions via middleware or explicit checks
5. Return the consistent error envelope on failure

## Error Response Envelope
```json
{"error": "error_code", "message": "Human-readable message", "details": {}}
```

## Middleware Chain
Apply in order: logging → auth → RBAC → rate-limit → handler

## Rules
- Handlers receive validated, typed request structs
- Never access the database directly — use the `db` package
- Group related endpoints in the same handler file
- Use Fiber's context methods for param/query/body parsing
