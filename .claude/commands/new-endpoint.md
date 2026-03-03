Create a new API endpoint for: $ARGUMENTS

Follow this process:
1. Read the root CLAUDE.md and internal/api/CLAUDE.md
2. Add the endpoint to api/openapi.yaml (use api-spec-writer subagent)
3. Run oapi-codegen to regenerate types
4. Create the handler in internal/api/handlers/
5. Register the route in internal/api/router.go
6. Add any needed sqlc queries in queries/
7. Run `make generate` to regenerate DB code
8. Write tests for the new handler
9. Run `make test` and `make lint`
