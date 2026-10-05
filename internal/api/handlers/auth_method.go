package handlers

import "github.com/gofiber/fiber/v3"

// LocalsAuthMethod is the c.Locals key under which authentication records HOW the
// caller authenticated, and AuthMethodSession and AuthMethodAPIKey are the two
// values it records: authRequired writes the first for a request carrying a valid
// access token — an interactive session — and authenticateAPIKey writes the second
// for one carrying a valid API key (both in middleware.go). Nothing else records a
// method, so a request with no method is one nothing authenticated.
//
// They are one set of constants, used by the writers and by the reader
// (AuthenticatedWithSession), because they used to be two string literals in two
// packages — "auth_method" in middleware.go and in api_keys.go, "api_key" in both —
// and a rename of one of them would have left the other reading a key nobody set,
// every API key allowed through what the refusal was written to keep it out of,
// with every test green.
const (
	LocalsAuthMethod  = "auth_method"
	AuthMethodSession = "session"
	AuthMethodAPIKey  = "api_key"
)

// AuthenticatedWithSession reports whether the request was authenticated by an
// interactive session. It is what the registry's InteractiveOnly gate asks
// (registry.go), and it is an ALLOWLIST on purpose: the gate admits a session and
// nothing else, so a principal type added tomorrow — a service token, a delegated
// credential — is refused on these routes until somebody decides it belongs there,
// instead of admitted until somebody remembers to refuse it. A handler has no
// business asking: "this route is not for a key" is declared on the route, in one
// place, and enforced ahead of the handler.
func AuthenticatedWithSession(c fiber.Ctx) bool {
	method, _ := c.Locals(LocalsAuthMethod).(string)
	return method == AuthMethodSession
}
