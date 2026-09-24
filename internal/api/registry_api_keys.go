package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — globalCheck — lives in
// registry_vms.go, where the first migrated domain defined it.

const (
	// apiKeyScope is where a caller manages their OWN keys.
	apiKeyScope = pathPrefix + "api-keys"
	// adminAPIKeyScope is where an operator sees everybody's, which is why
	// it is gated on manage:user rather than on manage:api_key: holding the
	// grant that lets you mint your own keys must not let you enumerate or
	// revoke someone else's.
	adminAPIKeyScope = pathPrefix + "admin/api-keys"
)

// The four self-service routes were listed in selfServiceRoutes before this
// migration, and that entry was WRONG in a way worth recording: every one of
// them opened with requirePerm(c, "manage", "api_key"), so they were never
// exempt from an RBAC check at all. The exemption made
// TestGuard_EveryRouteEnforcesPermission skip them before it ever walked their
// call graph, and it made
// TestGuard_DocumentedPermissionMatchesEnforcement skip them too — so the
// endpointMeta entry promising manage:api_key was never compared against
// anything. Declaring the Check and dropping the four exemptions puts both
// guards back on these routes.
//
// They ARE also scoped to the caller — the listing and the bulk revoke are
// keyed by c.Locals("user_id") and the single revoke refuses another user's
// key — but that is a property of the handler, not the shape of the gate.
// Permissions allows exactly one field, and the Check is the thing that
// actually runs.
func apiKeyManage() Permissions { return globalCheck("manage", "api_key") }

// apiKeyIDParam is an API key's row id as a PATH parameter.
var apiKeyIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "API key identifier.",
}

// registerAPIKeyEndpoints declares all 6 of APIKeyHandler's routes: four for
// the caller's own keys and two for the instance-wide admin view.
//
// Every one is a plain global Check — an API key belongs to an ACCOUNT, and no
// path here names a cluster — and none is Deferred or Advisory.
//
// What stays in the handlers is what the declaration cannot see: the refusal to
// mint a key while authenticated BY a key (which would let a stolen key
// self-replicate), the 25-keys-per-user cap, and the ownership check on the
// single revoke.
func registerAPIKeyEndpoints(reg *Registry, h *handlers.APIKeyHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   apiKeyScope,
		Description: "Mint an API key for the caller. The secret is returned ONCE and is never " +
			"retrievable again — only its 12-character prefix is stored in clear. A caller " +
			"authenticated BY an API key is refused, so a stolen key cannot mint more of itself.",
		Group:       "API Keys",
		Permissions: apiKeyManage(),
		Parameters:  createAPIKeyParams(),
		Handler:     h.Create,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        apiKeyScope,
		Description: "List the caller's own API keys. Secrets are never returned; each row carries its prefix, its expiry and when it was last used.",
		Group:       "API Keys",
		Permissions: apiKeyManage(),
		Parameters:  apischema.Properties{},
		Handler:     h.List,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        apiKeyScope + "/:id",
		Description: "Revoke one of the caller's own API keys. Another user's key answers 403.",
		Group:       "API Keys",
		Permissions: apiKeyManage(),
		Parameters:  apischema.Properties{"id": apiKeyIDParam},
		Handler:     h.Revoke,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        apiKeyScope,
		Description: "Revoke every API key the caller owns at once.",
		Group:       "API Keys",
		Permissions: apiKeyManage(),
		Parameters:  apischema.Properties{},
		Handler:     h.RevokeAll,
	})

	// ── Admin view ────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   adminAPIKeyScope,
		Description: "List every API key on the instance with its owner. Gated on manage:user rather " +
			"than manage:api_key: minting your own keys must not let you enumerate everyone else's.",
		Group:       "User Management",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{},
		Handler:     h.AdminList,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        adminAPIKeyScope + "/:id",
		Description: "Revoke any user's API key. The audit row records the owner and the key's name, never its secret.",
		Group:       "User Management",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{"id": apiKeyIDParam},
		Handler:     h.AdminRevoke,
	})
}

// createAPIKeyParams is the body of POST /api/v1/api-keys.
//
// `expires_in` was a *int64 whose nil meant "never expires", so it carries no
// Default and the handler reads p.OptInt, which reports an omitted key as not
// supplied whatever the declaration says (apischema.Property.Default). A
// default would therefore not shorten any key's life; it would document a
// lifetime no omitted key gets (and a Default of 0 would not even register,
// being below the Minimum).
//
// The MINIMUM is the handler's own "at least 3600 seconds" rule moved one layer
// out. The MAXIMUM is new, and it is a bound rather than a policy: the value is
// multiplied by time.Second into a time.Duration, which is int64 NANOSECONDS
// and overflows just past 9.2e9 seconds — so a caller asking for 1e18 seconds
// got a negative duration and a key that was already expired when it was
// handed to them. 100 years is far beyond any real use and an order of
// magnitude below the wrap.
func createAPIKeyParams() apischema.Properties {
	return apischema.Properties{
		"name": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(100),
			Typetext:    "<string>",
			Description: "Label for the key, so it can be recognised in the listing and revoked later.",
		},
		"expires_in": {
			Type:     apischema.Integer,
			Optional: true,
			// NO Default: omitting the key is how a caller asks for a key
			// that never expires.
			Minimum:  apischema.Ptr(3600.0),
			Maximum:  apischema.Ptr(3155760000.0),
			Typetext: "<integer>",
			Description: "Lifetime in seconds, at least one hour. Omitted, the key never expires. " +
				"Capped at 100 years, which is a bound on the arithmetic rather than a policy.",
		},
	}
}
