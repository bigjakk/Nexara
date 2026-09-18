package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — globalCheck, withParams
// and optString — lives in registry_vms.go, where the first migrated domain
// defined it. emptyOrUUID comes from registry_pbs.go and groupRoleMappingParam
// from registry_ldap.go, which carries the same field.

const (
	// oidcConfigScope is the admin CRUD prefix.
	oidcConfigScope = pathPrefix + "oidc/configs"
	// oidcAuthorizePath starts the login redirect, before any identity
	// exists. It sits under /auth/ rather than under /oidc/ because that is
	// where the login page already calls it.
	oidcAuthorizePath = pathPrefix + "auth/oidc/authorize"
)

// oidcConfigIDParam is an OIDC configuration's row id as a PATH parameter.
var oidcConfigIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "OIDC configuration identifier.",
}

// registerOIDCEndpoints declares 7 of OIDCHandler's 8 routes: the six admin
// CRUD routes, each a global manage:user Check, and the anonymous /authorize
// that starts the redirect flow.
//
// The EIGHTH — GET /api/v1/auth/oidc/callback — is deliberately left in
// router.go, and it is the first route in this migration blocked by something
// other than a parameter type. See registerOIDCEndpoints' sibling note in
// router.go and TestOIDCCallbackIsStillLegacy: its query string is composed by
// the IDENTITY PROVIDER, not by Nexara, and the registry rejects an undeclared
// key with a 400 (PVE's additionalProperties => 0). RFC 9207 adds `iss`,
// session management adds `session_state`, an error response carries `error`
// and `error_description`, and a provider may add its own — so the parameter
// set is open by construction. Declaring it would turn "this provider sends one
// extra parameter" into "SSO login returns 400", at the worst possible moment
// and with nothing to point at.
//
// What stays in the handlers is what the declaration cannot see: the
// cleartext-callback confirmation, the credential-redirect refusal, and the
// issuer/redirect URL validators, which own an SSRF-resolving check and a rule
// about which path this install actually serves.
func registerOIDCEndpoints(reg *Registry, h *handlers.OIDCHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   oidcConfigScope,
		Description: "List the OIDC provider configurations. The client secret is never returned — each " +
			"row carries only a client_secret_set flag.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{},
		Handler:     h.List,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   oidcConfigScope,
		Description: "Create an OIDC provider configuration. A plain-http callback on anything but " +
			"loopback is refused with a confirmation prompt until acknowledge_insecure_redirect is sent: " +
			"every login's authorization code rides back on that URL.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  oidcConfigParams(nil),
		Handler:     h.Create,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        oidcConfigScope + "/:id",
		Description: "Get one OIDC provider configuration. The client secret is never returned.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{"id": oidcConfigIDParam},
		Handler:     h.Get,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   oidcConfigScope + "/:id",
		Description: "Replace an OIDC provider configuration. Omitting client_secret keeps the stored " +
			"one, and moving issuer_url while a secret is stored is refused outright. redirect_uri is " +
			"re-validated only when it CHANGES, so a value predating the rule does not block unrelated edits.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  oidcConfigParams(apischema.Properties{"id": oidcConfigIDParam}),
		Handler:     h.Update,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        oidcConfigScope + "/:id",
		Description: "Delete an OIDC provider configuration. Accounts already provisioned from it are left in place.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{"id": oidcConfigIDParam},
		Handler:     h.Delete,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   oidcConfigScope + "/:id/test",
		Description: "Fetch the provider's discovery document through the SSRF-safe client and report " +
			"whether it answered. Answers 200 with success=false rather than an error status, so the " +
			"admin page can show the reason.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{"id": oidcConfigIDParam},
		Handler:     h.TestConnection,
	})

	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   oidcAuthorizePath,
		Description: "Begin the OIDC login redirect: build the provider's authorization URL with state, " +
			"nonce and PKCE, and return it for the browser to follow. Answers 404 when no configuration " +
			"is enabled.",
		Group: "Authentication",
		// Carried across verbatim from publicRoutes in
		// rbac_route_guard_test.go, which is the reviewed list this folds
		// into — see registryPublicRouteKeys.
		Permissions: Permissions{Public: "starts the OIDC redirect flow, before any identity exists"},
		Parameters:  apischema.Properties{},
		Handler:     h.Authorize,
	})
}

// oidcConfigParams is the body both writes share — one struct in the handler,
// one schema here.
//
// The booleans carry Default: false for the same reason the LDAP ones do: they
// were non-pointer bools on a full-body write, so an omitted key has always
// meant false, and requireOIDCRedirectSchemeAck is written against that.
//
// client_secret is write-only. It is never echoed by toOIDCConfigResponse
// (which reports only whether one is set) and never recorded in an audit row —
// the handlers build their audit details field by field rather than from
// Params.Raw(), which matters because view:audit is a default Viewer grant.
func oidcConfigParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"name": optString(200, "<string>",
			"Label shown on the login page's SSO button. Empty applies the built-in default."),
		"enabled": {
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<boolean>",
			Description: "Whether this provider participates in login. Only ONE config is used at a " +
				"time; the login path reads the enabled one.",
		},
		"issuer_url": {
			Type:      apischema.String,
			MinLength: apischema.Ptr(1),
			MaxLength: apischema.Ptr(512),
			Typetext:  "<https url>",
			Description: "Provider issuer URL. The https rule and the private-address refusal belong to " +
				"validateOIDCIssuerURL, which RESOLVES the hostname — an SSRF check no parameter schema could make.",
		},
		"client_id": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(512),
			Typetext:    "<string>",
			Description: "OAuth client identifier registered with the provider.",
		},
		"client_secret": {
			Type:      apischema.String,
			Optional:  true,
			MaxLength: apischema.Ptr(1024),
			Typetext:  "<string>",
			Description: "OAuth client secret. Write-only: it is never returned and never written to an " +
				"audit row. Omitted on an update, the stored one is kept. A public client has none.",
		},
		"redirect_uri": optString(512, "<url>",
			"Where the provider sends the browser back, carrying the authorization code. It must end in "+
				"this install's own callback path; validateOIDCRedirectURI owns that rule and the "+
				"no-credentials, no-fragment ones beside it."),
		"scopes": {
			Type:     apischema.Array,
			Optional: true,
			Items: &apischema.Property{
				Type:      apischema.String,
				MaxLength: apischema.Ptr(128),
				Typetext:  "<scope>",
			},
			Typetext:    "<scope>[,<scope>...]",
			Description: "Scopes requested at the provider. Empty applies the built-in openid/email/profile set.",
		},
		"email_claim": optString(128, "<claim>",
			"ID-token claim carrying the e-mail address. Empty applies the built-in default."),
		"display_name_claim": optString(128, "<claim>",
			"ID-token claim carrying the display name. Empty applies the built-in default."),
		"groups_claim": optString(128, "<claim>",
			"ID-token claim carrying group membership. Empty applies the built-in default."),
		"group_role_mapping": groupRoleMappingParam(
			"Provider group name -> Nexara role id. A user's roles are rebuilt from this on every login."),
		"default_role_id": {
			Type:     apischema.String,
			Optional: true,
			// A pattern rather than the uuid FORMAT: the empty string has
			// always meant "no default role", the same as an absent key, and
			// every registered format refuses "". See emptyOrUUID.
			Pattern:     emptyOrUUID,
			Typetext:    "<uuid>",
			Description: "Role assigned when no provider group matches. Empty or omitted assigns none.",
		},
		"auto_provision": {
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<boolean>",
			Description: "Create a Nexara account the first time an unknown user authenticates. Off, an " +
				"account must already exist.",
		},
		"allowed_domains": {
			Type:     apischema.Array,
			Optional: true,
			Items: &apischema.Property{
				Type:      apischema.String,
				MaxLength: apischema.Ptr(253),
				Typetext:  "<domain>",
			},
			Typetext:    "<domain>[,<domain>...]",
			Description: "E-mail domains permitted to sign in. Empty permits every domain the provider authenticates.",
		},
		"acknowledge_insecure_redirect": {
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<boolean>",
			Description: "Confirms a plain-http callback on anything but loopback. Only sent after the " +
				"operator accepts the refusal the first attempt returns.",
		},
	}, extra)
}
