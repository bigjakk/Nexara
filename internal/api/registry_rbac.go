package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — globalCheck,
// withParams and optString — lives in registry_vms.go, where the first
// migrated domain defined it.

// rbacScope is the prefix every route in this domain hangs off.
const rbacScope = pathPrefix + "rbac"

// Nexara's OWN role model — the roles a Nexara account holds, and the
// permissions inside them — as opposed to the Proxmox access model in
// registry_access.go. That difference is why every check here is GLOBAL: a
// role is an instance-wide object, granted to a user at global or cluster
// scope, and there is no cluster in any of these paths for a cluster-scoped
// gate to resolve.
func roleView() Permissions   { return globalCheck("view", "role") }
func roleManage() Permissions { return globalCheck("manage", "role") }

// roleIDParam is a Nexara role's row id as a PATH parameter.
var roleIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "Nexara role identifier.",
}

// rbacUserIDParam is the SUBJECT of a role assignment — the account whose
// grants are being read or changed — and it is deliberately spelled
// :user_id rather than :id.
//
// That is not cosmetic. "id" is one of the two names clusterIDFromParam
// reads (gateParamNames in registry.go), so on a path whose first
// placeholder were :id a cluster-scoped gate would silently authorize
// against a USER's uuid. Every check in this file is global, so nothing
// resolves a cluster today — but the name is what keeps that true if one
// of these routes ever gains a scope.
var rbacUserIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "Nexara user identifier.",
}

// rbacPermissionIDsParam is the set of permissions a role carries.
//
// It is an array of scalars — permission row ids — which is what makes this
// domain declarable at all: apischema's Property.Items is restricted to
// scalar element types, and the two routes elsewhere in the codebase that
// stayed legacy did so because their arrays hold OBJECTS.
//
// It is Optional on BOTH routes, but omitting it means something different on
// each. On create, a role with no permissions is a legitimate thing to make
// before filling it in. On update it is a TRISTATE — the request struct's field
// was a *[]uuid.UUID, so absent means "leave the role's permissions alone" and
// an EMPTY array means "take them all away", which the handler tells apart with
// p.Has — a read no default can fool (apischema.Property.Default). It carries
// no Default on either route all the same: CreateRole reads the list with
// p.Strings (parseRolePermissionIDs), so a default would be GRANTED to every
// role created without one, and on the update it would document an edit as
// replacing the permissions it leaves alone.
func rbacPermissionIDsParam(description string) apischema.Property {
	return apischema.Property{
		Type:     apischema.Array,
		Optional: true,
		Items: &apischema.Property{
			Type:     apischema.String,
			Format:   "uuid",
			Typetext: "<uuid>",
		},
		Typetext:    "<uuid>[,<uuid>...]",
		Description: description,
	}
}

// registerRBACEndpoints declares all 10 of RBACHandler's routes.
//
// Nine are a plain global Check — four view:role and five manage:role — and
// the tenth is MyPermissions, which is SelfService: it answers with the
// CALLER's own grants, read from c.Locals("user_id"), and takes no
// parameter naming a subject at all. A permission gate there would be
// circular as well as meaningless, since the SPA calls it to discover which
// permissions it holds.
//
// What stays in the handlers is what a declaration cannot see: the refusal
// to edit or delete a built-in role, the refusal to touch the system
// account, and the RBAC cache invalidation that has to follow a grant
// change.
func registerRBACEndpoints(reg *Registry, h *handlers.RBACHandler) {
	// ── Roles ─────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        rbacScope + "/roles",
		Description: "List Nexara's roles. The permissions inside each one are returned by the per-role read, not here.",
		Group:       "Roles & Permissions",
		Permissions: roleView(),
		Parameters:  apischema.Properties{},
		Handler:     h.ListRoles,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   rbacScope + "/roles",
		Description: "Create a custom Nexara role. Omitting permission_ids creates a role that grants " +
			"nothing, which is the normal shape while it is still being assembled.",
		Group:       "Roles & Permissions",
		Permissions: roleManage(),
		Parameters:  createRoleParams(),
		Handler:     h.CreateRole,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        rbacScope + "/roles/:id",
		Description: "Get one role together with the permissions it carries.",
		Group:       "Roles & Permissions",
		Permissions: roleView(),
		Parameters:  apischema.Properties{"id": roleIDParam},
		Handler:     h.GetRole,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   rbacScope + "/roles/:id",
		Description: "Change a custom role. Every field is optional and an omitted one is left alone; " +
			"sending permission_ids as an EMPTY array strips every permission from the role. Built-in " +
			"roles are refused.",
		Group:       "Roles & Permissions",
		Permissions: roleManage(),
		Parameters:  updateRoleParams(),
		Handler:     h.UpdateRole,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   rbacScope + "/roles/:id",
		Description: "Delete a custom role. Built-in roles are refused, and every user holding the role " +
			"has their cached permissions invalidated first.",
		Group:       "Roles & Permissions",
		Permissions: roleManage(),
		Parameters:  apischema.Properties{"id": roleIDParam},
		Handler:     h.DeleteRole,
	})

	// ── Permission catalogue ──────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   rbacScope + "/permissions",
		Description: "List every permission the instance knows, so a client can build a role editor " +
			"from the catalogue rather than from a hardcoded list that dates.",
		Group:       "Roles & Permissions",
		Permissions: roleView(),
		Parameters:  apischema.Properties{},
		Handler:     h.ListPermissions,
	})

	// ── Role assignments ──────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        rbacScope + "/users/:user_id/roles",
		Description: "List the roles assigned to one user, with the scope each assignment applies at.",
		Group:       "Roles & Permissions",
		Permissions: roleView(),
		Parameters:  apischema.Properties{"user_id": rbacUserIDParam},
		Handler:     h.ListUserRoles,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   rbacScope + "/users/:user_id/roles",
		Description: "Assign a role to a user, instance-wide or on one cluster. A cluster-scoped " +
			"assignment needs scope_id; the system account cannot be assigned roles.",
		Group:       "Roles & Permissions",
		Permissions: roleManage(),
		Parameters:  assignUserRoleParams(),
		Handler:     h.AssignUserRole,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   rbacScope + "/users/:user_id/roles/:id",
		Description: "Revoke one role assignment. The assignment id is the row's own id, not the role's, " +
			"so revoking a cluster-scoped grant leaves an instance-wide one in place.",
		Group:       "Roles & Permissions",
		Permissions: roleManage(),
		Parameters: apischema.Properties{
			"user_id": rbacUserIDParam,
			"id": {
				Type:        apischema.String,
				Format:      "uuid",
				Source:      apischema.SourcePath,
				Typetext:    "<uuid>",
				Description: "Role assignment identifier, as the per-user listing returns it.",
			},
		},
		Handler: h.RevokeUserRole,
	})

	// ── The caller's own grants ───────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   rbacScope + "/me/permissions",
		Description: "Report the CALLER's own flattened permissions and role assignments. The SPA reads " +
			"it to decide what to render, so it answers every authenticated caller.",
		Group:       "Roles & Permissions",
		Permissions: Permissions{SelfService: rbacMyPermissionsReason},
		Parameters:  apischema.Properties{},
		Handler:     h.MyPermissions,
	})
}

// rbacMyPermissionsReason is the SelfService justification for
// /rbac/me/permissions, carried across VERBATIM from selfServiceRoutes in
// rbac_route_guard_test.go — the reviewed list this declaration folds into.
// TestRBACMyPermissionsReasonMatchesTheReviewedList pins that the two stay
// identical, the way the auth and TOTP domains already do, so one exemption
// cannot end up with two justifications.
//
// What the sentence has to establish is the thing SelfService is riskiest
// about: that the subject comes from the session rather than from anything the
// caller sent. It does, twice over. The path names no subject at all — there is
// no :user_id to substitute — and MyPermissions takes the id from
// c.Locals("user_id"), refusing with 401 when that is absent. The per-USER
// listing one path up is the route that names a subject, and that one is a
// view:role Check.
const rbacMyPermissionsReason = "returns the caller's own grants"

// createRoleParams is the body of POST /api/v1/rbac/roles.
//
// `name` is the one required field, matching the handler's single explicit
// check. Uniqueness stays in the database, which owns it for every caller
// and comes back as the 409 the handler already maps.
func createRoleParams() apischema.Properties {
	return apischema.Properties{
		"name": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(100),
			Typetext:    "<string>",
			Description: "Name for the new role. Must be unique across the instance.",
		},
		"description":    optString(500, "<string>", "Free-text note on what the role is for."),
		"permission_ids": rbacPermissionIDsParam("Permissions to put in the new role. Omitted, the role grants nothing."),
	}
}

// updateRoleParams is the body of PUT /api/v1/rbac/roles/:id.
//
// All three are TRISTATE, and that is why none of them carries a Default:
// the request struct's fields are pointers, so an omitted key leaves the
// stored value alone. The handler reads them with p.OptString and p.Has,
// which never report a default as supplied (apischema.Property.Default), so
// a Default would not blank a renamed role's description; it would document
// that it does.
func updateRoleParams() apischema.Properties {
	return apischema.Properties{
		"id":          roleIDParam,
		"name":        optString(100, "<string>", "New name. Omitted, the stored one is kept."),
		"description": optString(500, "<string>", "New note. Omitted, the stored one is kept; sent empty, it clears the stored one."),
		"permission_ids": rbacPermissionIDsParam(
			"Replacement permission set. Omitted, the role's permissions are left alone; sent as an " +
				"empty array, every permission is removed."),
	}
}

// assignUserRoleParams is the body of POST /api/v1/rbac/users/:user_id/roles.
//
// The "scope_id is required when scope_type is cluster" rule stays in the
// handler: apischema's Requires names a companion a parameter ALWAYS needs,
// not one it needs only for a particular value of another parameter.
//
// scope_id gains a uuid format it did not have, which is a deliberate
// narrowing in one direction only. A malformed value was already a 400 on a
// cluster-scoped assignment; what changes is that sending one alongside
// scope_type "global" is now refused rather than silently dropped.
func assignUserRoleParams() apischema.Properties {
	return apischema.Properties{
		"user_id": rbacUserIDParam,
		"role_id": {
			Type:        apischema.String,
			Format:      "uuid",
			Typetext:    "<uuid>",
			Description: "Role to assign, as GET /api/v1/rbac/roles returns it.",
		},
		"scope_type": {
			Type:        apischema.String,
			Optional:    true,
			Default:     "global",
			Enum:        []string{"global", "cluster"},
			Typetext:    "<global|cluster>",
			Description: "Whether the grant applies instance-wide or to a single cluster.",
		},
		"scope_id": {
			Type:        apischema.String,
			Optional:    true,
			Format:      "uuid",
			Typetext:    "<uuid>",
			Description: "Cluster the grant applies to. Required when scope_type is \"cluster\", and meaningless otherwise.",
		},
	}
}
