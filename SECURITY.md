# Security Policy

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| 1.x     | Yes                |
| < 1.0   | No                 |

## Reporting a Vulnerability

We take security seriously. If you discover a vulnerability in Nexara, please report it responsibly.

**Do NOT open a public GitHub issue for security vulnerabilities.**

### How to Report

1. Email **security@nexara.dev** with a description of the vulnerability
2. Include steps to reproduce, if possible
3. Include the version of Nexara affected

### What to Expect

- **Acknowledgement** within 48 hours of your report
- **Status update** within 7 days with an assessment and expected timeline
- **Fix release** as soon as a patch is ready, typically within 30 days for critical issues
- **Credit** in the release notes (unless you prefer to remain anonymous)

## Security Features

Nexara includes the following security measures:

### Authentication & Authorization
- **JWT authentication** with short-lived access tokens and refresh token rotation
- **RBAC** — granular role-based access control with built-in and custom roles
- **LDAP/AD integration** — bind password encrypted at rest with AES-256-GCM
- **OIDC/SSO** — PKCE + state + nonce validation, single-use authorization codes
- **TOTP two-factor authentication** — secrets encrypted at rest, rate-limited verification

### Data Protection
- **Encryption at rest** — all sensitive data (API tokens, LDAP bind passwords, OIDC client secrets, TOTP secrets, SSH credentials, notification channel configs) encrypted with AES-256-GCM
- **No plaintext secrets** — secrets are never logged or returned in API responses
- **Environment-based configuration** — no secrets in config files or source code

### Network Security
- **SSRF protection** — private IP blocking, DNS resolution checks, no-redirect HTTP clients on all outbound requests (webhooks, OIDC discovery, LDAP)
- **CRLF injection prevention** — header sanitization on SMTP and HTTP dispatchers
- **Input validation** — string length limits, format validation, SQL injection prevention via parameterized queries (sqlc)
- **Rate limiting** — configurable per-endpoint rate limits
- **TLS** — Caddy automatic HTTPS with Let's Encrypt

### Infrastructure
- **Distroless containers** — minimal attack surface in production images
- **Health checks** — all services monitored for availability
- **Audit logging** — all user and system actions logged with full context

## Out of Scope

The following are not considered vulnerabilities:

- Denial of service via resource exhaustion (use rate limiting and infrastructure-level controls)
- Issues requiring physical access to the server
- Social engineering attacks
- Vulnerabilities in dependencies that do not affect Nexara's usage of them
- Issues in development/test configurations not intended for production use
- Missing security headers that do not lead to a concrete exploit
