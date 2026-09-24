package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — globalCheck and
// optString — lives in registry_vms.go, where the first migrated domain
// defined it.

// userScope is the prefix the four account-management routes hang off.
//
// These are NEXARA accounts — who may sign in to this install — as opposed
// to the Proxmox users in registry_access.go, which belong to one cluster.
// That is why every check here is global: an account is an instance-wide
// object and no path in this domain names a cluster.
const userScope = pathPrefix + "users"

// userIDParam is a Nexara account's row id as a PATH parameter.
//
// It is spelled :id because that is what the three routes already register,
// and "id" is one of the two names clusterIDFromParam falls back to
// (gateParamNames in registry.go). That is safe only because it resolves to
// the PATH — checkPathParams refuses the same name declared as a body or
// query parameter — and because nothing in this domain is cluster-scoped,
// so the fallback is never reached.
var userIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "Nexara user identifier.",
}

// userUpdateReason is the Deferred justification for PUT /api/v1/users/:id,
// and it is the one route in this tranche where the answer was not simply
// "hoist the call".
//
// Update takes manage:user unconditionally — that part IS a static gate and
// would hoist cleanly — but it ALSO takes manage:role, and only when the
// body carries `role`. A Check would state the first and say nothing about
// the second, so Describe() would render "manage:user" and an operator
// building a role from the docs would grant exactly that, then get a 403 on
// the one field they cared about with nothing telling them which grant they
// were missing. Deferred keeps the whole decision in one place and makes the
// reason carry both halves; the route's Description spells them out for the
// docs, the way every Deferred route in registry_veeam.go does.
//
// It is also the shape the guards check hardest: registryEnforcementGaps
// walks a Deferred route's handler and fails unless it reaches a permission
// leaf, which a Check installs no obligation to do.
const userUpdateReason = "the gate is CONDITIONAL on the body: the handler requires manage:user for any " +
	"edit and an ADDITIONAL manage:role when `role` is present, which middleware cannot decide because " +
	"it runs before the body is read — and a static Check would document only the first of the two"

// registerUserEndpoints declares all 4 of UserHandler's routes.
//
// Three are a plain global Check (two view:user, one manage:user) and the
// fourth is the conditional Deferred above.
//
// What stays in the handlers is what the declaration cannot see: the refusal
// to touch the system account, the refusal to change your own role or active
// status, the refusal to delete your own account, and the session revocation
// that has to follow a deactivation.
func registerUserEndpoints(reg *Registry, h *handlers.UserHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   userScope,
		Description: "List Nexara's user accounts with their RBAC role names, auth source and 2FA " +
			"enrollment. The per-user read does not carry the role names; this listing joins them.",
		Group:       "User Management",
		Permissions: globalCheck("view", "user"),
		Parameters:  apischema.Properties{},
		Handler:     h.List,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        userScope + "/:id",
		Description: "Get one Nexara user account.",
		Group:       "User Management",
		Permissions: globalCheck("view", "user"),
		Parameters:  apischema.Properties{"id": userIDParam},
		Handler:     h.Get,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   userScope + "/:id",
		Description: "Change a Nexara user account. Requires manage:user; changing `role` requires " +
			"manage:role IN ADDITION. Every field is optional and an omitted one is left alone. A caller " +
			"cannot change their own role or their own active status, the system account cannot be " +
			"edited, and deactivating an account revokes its sessions immediately rather than waiting " +
			"for the access token to expire.",
		Group:       "User Management",
		Permissions: Permissions{Deferred: userUpdateReason},
		Parameters:  updateUserParams(),
		Handler:     h.Update,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   userScope + "/:id",
		Description: "Delete a Nexara user account. A caller cannot delete their own account, and the " +
			"system account cannot be deleted.",
		Group:       "User Management",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{"id": userIDParam},
		Handler:     h.Delete,
	})
}

// updateUserParams is the body of PUT /api/v1/users/:id.
//
// All three are TRISTATE — the request struct's fields were pointers — so
// none carries a Default: an omitted key leaves the stored value alone. The
// handler reads each through an Opt accessor and acts, its "cannot change
// your own active status" refusal included, only on a field that was
// SUPPLIED, which a default never is (apischema.Property.Default); a Default
// would therefore change nothing but the docs, which would then describe
// every unrelated edit as setting the account's active state.
//
// `role` is the legacy two-value column, not an RBAC role. Its vocabulary
// moves here from the handler's own `!= "admin" && != "user"` check, which
// means an unknown value is now refused by the schema before the
// manage:role check runs — a 400 where it used to be a 403 for a caller who
// held neither grant. That is the registry's ordinary ordering (validate,
// then authorize) and the vocabulary it leaks is already in the API docs.
func updateUserParams() apischema.Properties {
	return apischema.Properties{
		"id": userIDParam,
		"display_name": optString(200, "<string>",
			"New display name. Omitted, the stored one is kept."),
		"is_active": {
			Type:     apischema.Boolean,
			Optional: true,
			// NO Default. See the function comment: a default here would
			// document every edit that never mentioned it as flipping it.
			Typetext: "<boolean>",
			Description: "Whether the account may sign in. Omitted, it is left as it is. Setting it false " +
				"revokes the account's sessions immediately. A caller cannot change their own.",
		},
		"role": {
			Type:     apischema.String,
			Optional: true,
			Enum:     []string{"admin", "user"},
			Typetext: "<admin|user>",
			Description: "The account's built-in tier. Changing it requires manage:role in addition to " +
				"manage:user, and a caller cannot change their own. Omitted, it is left as it is.",
		},
	}
}
