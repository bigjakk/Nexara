package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — optString — lives in
// registry_vms.go, where the first migrated domain defined it.

// authScope is the prefix every route in this domain hangs off.
const authScope = pathPrefix + "auth"

// consoleTokenReason is the Deferred justification for POST
// /api/v1/auth/console-token, the one route in this domain whose permission
// cannot be written down in advance.
//
// It picks its RESOURCE by switching on the request body's `type` —
// node_shell asks about a node, vm_serial and vm_vnc about a vm, ct_attach and
// ct_vnc about a container — and it resolves the CLUSTER from the body too,
// because there is no cluster in the path. Middleware runs before the body is
// read, so neither half is knowable to it.
//
// The action is the dedicated "console" family rather than "view", and that is
// not a detail: the built-in Viewer role holds every view:* permission, and
// gating this on view:node once handed read-only accounts a root shell on the
// hypervisor (fixed in 17a7cc0, pinned by console_token_authz_test.go).
const consoleTokenReason = "the resource is chosen by the request BODY — node_shell gates on console:node, " +
	"vm_serial and vm_vnc on console:vm, ct_attach and ct_vnc on console:container — and the cluster " +
	"comes from the body as well, since the path names none; middleware runs before either is readable"

// The seven SelfService reasons and the five Public ones below are carried
// across VERBATIM from selfServiceRoutes and publicRoutes in
// rbac_route_guard_test.go, which are the reviewed lists these declarations
// fold into — registrySelfServiceRouteKeys and registryPublicRouteKeys are what
// make them fold, and TestAuthExemptionReasonsMatchTheReviewedLists pins that
// they stay identical rather than drifting into two justifications for one
// exemption.

// registerAuthEndpoints declares 13 of AuthHandler's 15 routes.
//
// The split is the domain's own shape:
//
//	5  Public       login, refresh, the two login-page status probes and the
//	                OIDC code exchange: every one runs before a session exists.
//	7  SelfService  the caller's own profile, password, sessions and hub token.
//	                Each takes its subject from c.Locals("user_id"); none reads
//	                a subject from the path, the query or the body.
//	1  Deferred     console-token — see consoleTokenReason.
//
// The TWO that stay in router.go are register and logout, and they are blocked
// by a gap in the Permissions vocabulary rather than by a parameter type. Both
// are mounted with authOptional: the session is parsed IF one is presented and
// the request proceeds either way. That is neither Public (which installs no
// authentication at all, so c.Locals("role") would be empty and Register would
// refuse every admin-created account after the first) nor authenticated (which
// would 401 the logout that a valid refresh cookie must still be able to
// perform once the access token has expired). See
// TestAuthOptionalRoutesAreStillLegacy, which pins that this is a decision, and
// the report accompanying this change, which asks for the vocabulary.
//
// What stays in the handlers is everything the declaration cannot see: the
// first-user advisory lock, the constant-time login failure paths, the
// role-rotation guard on refresh, the session ownership check, and the
// "auth_source must be local" refusals on profile and password edits.
func registerAuthEndpoints(reg *Registry, h *handlers.AuthHandler) {
	// ── Anonymous ─────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   authScope + "/login",
		Description: "Authenticate with an e-mail and password and receive an access token plus a " +
			"refresh cookie. When the account has a second factor the response is a TOTP challenge " +
			"instead. Every rejection is deliberately indistinguishable: wrong password, unknown " +
			"address, disabled account and SSO-only account all answer the same way, after the same work.",
		Group:       "Authentication",
		Permissions: Permissions{Public: "issues the session"},
		Parameters: apischema.Properties{
			"email": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(320),
				Typetext:  "<email>",
				// Deliberately NOT validated as an e-mail address. This is a
				// LOOKUP key, matched against whatever is stored — including
				// accounts provisioned from a directory — and a format rule
				// here would lock out an account whose address Nexara itself
				// accepted at creation.
				Description: "The account's e-mail address, as stored.",
			},
			"password": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(1024),
				Typetext:  "<string>",
				Description: "The account's password. Write-only: never returned, and never recorded in " +
					"an audit row.",
			},
		},
		Handler: h.Login,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   authScope + "/refresh",
		Description: "Exchange a refresh token for a new access token and rotate the refresh token. The " +
			"HttpOnly cookie is the delivery path browsers use; the body field is retained for API " +
			"clients. A role change since the session was created forces a fresh login.",
		Group:       "Authentication",
		Permissions: Permissions{Public: "exchanges the refresh cookie for a new access token"},
		Parameters: apischema.Properties{
			"refresh_token": optString(1024, "<token>",
				"Refresh token, for a client that does not carry cookies. Omitted, the HttpOnly cookie is used."),
		},
		Handler: h.Refresh,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   authScope + "/setup-status",
		Description: "Report whether this install has any user yet, so the login page can offer first-run " +
			"setup. Returns a single boolean.",
		Group:       "Authentication",
		Permissions: Permissions{Public: "login page asks whether an admin exists yet"},
		Parameters:  apischema.Properties{},
		Handler:     h.SetupStatus,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   authScope + "/sso-status",
		Description: "Report whether SSO is configured, and under what name, so the login page can render " +
			"the provider button. Returns only a boolean and the display name — never the issuer or the client id.",
		Group:       "Authentication",
		Permissions: Permissions{Public: "login page asks whether SSO is configured; returns only a bool and the provider display name"},
		Parameters:  apischema.Properties{},
		Handler:     h.SSOStatus,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   authScope + "/oidc/token-exchange",
		Description: "Trade the one-time code the OIDC callback redirected with for an access token and " +
			"a refresh cookie. The code is consumed atomically, so a replay finds nothing.",
		Group:       "Authentication",
		Permissions: Permissions{Public: "exchanges the OIDC one-time code for tokens"},
		Parameters: apischema.Properties{
			"code": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(256),
				Typetext:  "<code>",
				Description: "The single-use exchange code from the /oidc-callback redirect. Not the " +
					"provider's authorization code.",
			},
		},
		Handler: h.OIDCTokenExchange,
	})

	// ── The caller's own account ──────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   authScope + "/logout-all",
		Description: "Revoke every session on the caller's own account and clear their refresh cookie — " +
			"the \"sign out everywhere\" action.",
		Group:       "Authentication",
		Permissions: Permissions{SelfService: "revokes the caller's own sessions"},
		Parameters:  apischema.Properties{},
		Handler:     h.LogoutAll,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   authScope + "/sessions",
		Description: "List the caller's own active sessions with the device, address and last use of " +
			"each, and a flag on the one this request came from. The refresh-token hash is never returned.",
		Group:       "Authentication",
		Permissions: Permissions{SelfService: "lists the caller's own sessions; the query is keyed by the authenticated user id"},
		Parameters:  apischema.Properties{},
		Handler:     h.ListSessions,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   authScope + "/sessions/:id",
		Description: "Revoke one of the caller's own sessions. Somebody else's id answers 404 rather than " +
			"403, so the endpoint cannot be used to probe which session ids exist; revoking the current " +
			"session also clears the refresh cookie.",
		Group:       "Authentication",
		Permissions: Permissions{SelfService: "revokes one of the caller's own sessions; ownership is checked in the handler"},
		Parameters: apischema.Properties{
			"id": {
				Type:        apischema.String,
				Format:      "uuid",
				Source:      apischema.SourcePath,
				Typetext:    "<uuid>",
				Description: "Session identifier, as GET /api/v1/auth/sessions returns it.",
			},
		},
		Handler: h.RevokeSessionByID,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   authScope + "/ws-token",
		Description: "Mint a 60-second token whose only valid use is upgrading the /ws hub. It exists so " +
			"the long-lived access token never travels in a WebSocket URL; per-cluster authorization is " +
			"applied at subscribe time, not here.",
		Group:       "Authentication",
		Permissions: Permissions{SelfService: "mints a hub token for the caller; per-cluster authorization is enforced at subscribe time in internal/ws"},
		Parameters:  apischema.Properties{},
		Handler:     h.WSToken,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        authScope + "/me",
		Description: "Return the caller's own profile: identity, role, auth source and whether a second factor is enrolled.",
		Group:       "Authentication",
		Permissions: Permissions{SelfService: "returns the caller's own profile"},
		Parameters:  apischema.Properties{},
		Handler:     h.GetMe,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   authScope + "/profile",
		Description: "Change the caller's own display name. Refused for an account provisioned from LDAP " +
			"or OIDC, whose profile is owned by the identity provider.",
		Group:       "Authentication",
		Permissions: Permissions{SelfService: "updates the caller's own profile"},
		Parameters: apischema.Properties{
			"display_name": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(200),
				Typetext:  "<string>",
				Description: "New display name. Leading and trailing whitespace is trimmed, and a name " +
					"that is only whitespace is refused.",
			},
		},
		Handler: h.UpdateProfile,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   authScope + "/change-password",
		Description: "Change the caller's own password, proving the current one first, and revoke every " +
			"session on the account so each device signs in again. Refused for an account provisioned " +
			"from LDAP or OIDC.",
		Group:       "Authentication",
		Permissions: Permissions{SelfService: "changes the caller's own password"},
		Parameters: apischema.Properties{
			// Both are write-only, and neither reaches an audit row: the
			// handler records the ACTION with a nil details payload, which
			// matters because view:audit is a default Viewer grant.
			"old_password": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(1024),
				Typetext:    "<string>",
				Description: "The current password. Never returned, and never recorded in an audit row.",
			},
			"new_password": {
				Type:      apischema.String,
				MinLength: apischema.Ptr(1),
				MaxLength: apischema.Ptr(1024),
				Typetext:  "<string>",
				Description: "The replacement password. Its strength rules belong to " +
					"auth.ValidatePasswordStrength, which owns them for every caller and answers with a " +
					"message naming the rule that failed.",
			},
		},
		Handler: h.ChangePassword,
	})

	// ── Console ───────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   authScope + "/console-token",
		Description: "Mint a 60-second token locked to one console: a single cluster, node, guest and " +
			"console type. Requires console:node, console:vm or console:container on THAT cluster " +
			"depending on the type requested — deliberately not view:*, which every built-in Viewer holds.",
		Group:       "Authentication",
		Permissions: Permissions{Deferred: consoleTokenReason},
		Parameters:  consoleTokenParams(),
		Handler:     h.ConsoleToken,
	})
}

// consoleTokenParams is the body of POST /api/v1/auth/console-token.
//
// The cluster is named console_cluster_id with "cluster_id" as an ALIAS, and
// that is not cosmetic. checkPathParams refuses a parameter NAMED cluster_id
// that resolves to anything but the path, because clusterIDFromParam reads that
// name to decide which cluster a permission gate authorizes — so a body
// parameter of that name is a name the gate also reads. No gate runs on this
// route at all (it is Deferred, and the handler resolves the cluster itself),
// but the refusal is deliberately about the NAME rather than about whether
// today's shape happens to make it safe. The alias is what keeps every existing
// caller working: the console code sends {"cluster_id": …}, and Alias exists for
// exactly this — a second spelling of one parameter, read from the same source.
// registry_pbs.go's attached_cluster_id carries the same pairing.
func consoleTokenParams() apischema.Properties {
	return apischema.Properties{
		"console_cluster_id": {
			Type:     apischema.String,
			Format:   "uuid",
			Alias:    "cluster_id",
			Typetext: "<uuid>",
			Description: "Cluster the console is opened on. Also accepted as \"cluster_id\", which is " +
				"what the console code sends. The permission is checked against THIS cluster.",
		},
		// The canonical node-name option rather than a bare bounded string.
		// The handler only checked non-empty and <= 128 characters; a PVE node
		// name is a DNS label, and this is the definition the codebase's three
		// hand-rolled copies are meant to converge on.
		"node": apischema.StdOption("node-name"),
		"type": {
			Type:     apischema.String,
			Enum:     []string{"node_shell", "vm_serial", "vm_vnc", "ct_attach", "ct_vnc"},
			Typetext: "<node_shell|vm_serial|vm_vnc|ct_attach|ct_vnc>",
			Description: "Which console to open. It selects the permission as well as the transport: " +
				"node_shell requires console:node, vm_serial and vm_vnc console:vm, ct_attach and " +
				"ct_vnc console:container.",
		},
		"vmid": {
			Type:     apischema.Integer,
			Optional: true,
			// Minimum 0 rather than 1, because 0 is how a node_shell request
			// spells "no guest" — and the handler REFUSES a non-zero vmid for
			// node_shell and a non-positive one for a guest console. Those two
			// rules are cross-field with `type` and stay there.
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(999999999.0),
			Typetext:    "<integer>",
			Description: "Proxmox VMID of the guest. Required for a guest console, and refused for node_shell.",
		},
		"silent": {
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<boolean>",
			Description: "Skip the audit row for this mint, for a background VNC preview that would " +
				"otherwise flood the activity feed. Honoured for vm_vnc and ct_vnc only — a terminal " +
				"mint is always user-initiated and always audited — and it grants no authority: the " +
				"permission check is unchanged and the mint is still logged at INFO.",
		},
	}
}
