package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — globalCheck, withParams
// and optString — lives in registry_vms.go, where the first migrated domain
// defined it. emptyOrUUID comes from registry_pbs.go.

// ldapConfigScope is the prefix every route in this domain hangs off.
const ldapConfigScope = pathPrefix + "ldap/configs"

// ldapConfigIDParam is an LDAP configuration's row id as a PATH parameter.
var ldapConfigIDParam = apischema.Property{
	Type:        apischema.String,
	Format:      "uuid",
	Source:      apischema.SourcePath,
	Typetext:    "<uuid>",
	Description: "LDAP configuration identifier.",
}

// registerLDAPEndpoints declares all 7 of LDAPHandler's routes.
//
// Every one is a plain global Check on manage:user, which is the domain's own
// shape rather than a simplification: a directory configuration decides who may
// sign in to the WHOLE install, so there is no cluster in any of these paths and
// nothing for a handler to resolve that middleware could not. The resource is
// "user" rather than a dedicated one because that is what each handler already
// checked; renaming it would be a permission change, not a migration.
//
// What stays in the handlers is everything the declaration cannot see, and in
// this domain that is unusually load-bearing:
//
//   - requireLDAPTransportAck, the confirm-and-proceed gate on a config that
//     would carry passwords in cleartext or over an unverified certificate. It
//     compares the STORED config against the requested one, so it needs the row.
//   - credentialRedirected, which refuses to re-point the stored bind password
//     at a directory the operator never entrusted it to.
//   - validateLDAPServerURL and validateLDAPFilters, which own the scheme rule,
//     the loopback/link-local refusal and the {{username}}/{{userDN}}
//     placeholder requirement.
func registerLDAPEndpoints(reg *Registry, h *handlers.LDAPHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   ldapConfigScope,
		Description: "List the LDAP directory configurations. The bind password is never returned — each " +
			"row carries only a bind_password_set flag.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{},
		Handler:     h.List,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   ldapConfigScope,
		Description: "Create an LDAP directory configuration. A config whose connection is unencrypted, " +
			"or encrypted but unverified, is refused with a confirmation prompt until " +
			"acknowledge_insecure_tls is sent: LDAP binds AS THE USER, so every interactive login puts " +
			"that person's password on this connection.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  ldapConfigParams(nil),
		Handler:     h.Create,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        ldapConfigScope + "/:id",
		Description: "Get one LDAP directory configuration. The bind password is never returned.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{"id": ldapConfigIDParam},
		Handler:     h.Get,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   ldapConfigScope + "/:id",
		Description: "Replace an LDAP directory configuration. This is a FULL-BODY write: an omitted " +
			"boolean is false, which is why turning start_tls off by omission is exactly what the " +
			"insecure-transport confirmation catches. Omitting bind_password keeps the stored one, and " +
			"moving server_url while a password is stored is refused outright.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  ldapConfigParams(apischema.Properties{"id": ldapConfigIDParam}),
		Handler:     h.Update,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        ldapConfigScope + "/:id",
		Description: "Delete an LDAP directory configuration. Accounts already provisioned from it are left in place.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{"id": ldapConfigIDParam},
		Handler:     h.Delete,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   ldapConfigScope + "/:id/test",
		Description: "Test a stored LDAP configuration: connect, bind, and — when test_username is given " +
			"— search for that user and report their groups. Answers 200 with success=false rather than " +
			"an error status, so the admin page can show the reason.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters: apischema.Properties{
			"id": ldapConfigIDParam,
			"test_username": optString(256, "<string>",
				"Optional directory username to look up, so the search base and user filter are exercised too."),
		},
		Handler: h.TestConnection,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   ldapConfigScope + "/:id/sync",
		Description: "Re-read every LDAP-sourced account from the directory: disable the ones that are " +
			"gone, re-enable the ones that are back, refresh display names, and re-apply the group-to-role " +
			"mapping.",
		Group:       "Authentication",
		Permissions: globalCheck("manage", "user"),
		Parameters:  apischema.Properties{"id": ldapConfigIDParam},
		Handler:     h.Sync,
	})
}

// ldapConfigParams is the body both writes share — one struct in the handler,
// one schema here.
//
// The three booleans carry Default: false DELIBERATELY, and that is the one
// decision in this schema worth stating out loud. They were non-pointer bools
// on a full-body PUT, so omitting start_tls has always turned StartTLS OFF —
// and requireLDAPTransportAck exists precisely to catch that accident. Making
// them tristate here would have been an improvement in isolation and a
// REGRESSION in context: the confirmation would stop firing on the exact
// request it was written for.
//
// bind_password is write-only. It is never echoed by toLDAPConfigResponse
// (which reports only whether one is set) and never recorded in an audit row —
// the handlers build their audit details field by field rather than from
// Params.Raw(), which matters because view:audit is a default Viewer grant.
func ldapConfigParams(extra apischema.Properties) apischema.Properties {
	return withParams(apischema.Properties{
		"name": optString(200, "<string>", "Label for this directory, shown in the admin list."),
		"enabled": {
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<boolean>",
			Description: "Whether this directory participates in login. Only ONE config is used at a " +
				"time; the login path reads the enabled one.",
		},
		"server_url": {
			Type:      apischema.String,
			MinLength: apischema.Ptr(1),
			MaxLength: apischema.Ptr(512),
			Typetext:  "<ldap://host|ldaps://host>",
			Description: "Directory URL. The scheme rule and the loopback/link-local refusal belong to " +
				"validateLDAPServerURL, which owns them for every caller.",
		},
		"start_tls": {
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<boolean>",
			Description: "Upgrade an ldap:// connection to TLS. OMITTING this on an update turns it off, " +
				"which is what the insecure-transport confirmation is there to catch.",
		},
		"skip_tls_verify": {
			Type:        apischema.Boolean,
			Optional:    true,
			Default:     false,
			Typetext:    "<boolean>",
			Description: "Do not verify the directory's certificate. Requires acknowledge_insecure_tls.",
		},
		"bind_dn": optString(1024, "<dn>",
			"Service account DN to bind as. Empty binds anonymously, which is legal and still carries "+
				"every user's own password on login."),
		"bind_password": {
			Type:      apischema.String,
			Optional:  true,
			MaxLength: apischema.Ptr(1024),
			Typetext:  "<string>",
			Description: "Service account password. Write-only: it is never returned and never written to " +
				"an audit row. Omitted on an update, the stored one is kept.",
		},
		"search_base_dn": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(1024),
			Typetext:    "<dn>",
			Description: "Base DN user searches start from.",
		},
		"user_filter": optString(1024, "<filter>",
			"LDAP filter for a user lookup; must contain {{username}}. Empty applies the built-in default."),
		"username_attribute": optString(128, "<attr>",
			"Attribute carrying the login name (uid, or sAMAccountName on AD). Empty applies the built-in default."),
		"email_attribute": optString(128, "<attr>",
			"Attribute carrying the e-mail address. Empty applies the built-in default."),
		"display_name_attribute": optString(128, "<attr>",
			"Attribute carrying the display name. Empty applies the built-in default."),
		"group_search_base_dn": optString(1024, "<dn>", "Base DN group searches start from."),
		"group_filter": optString(1024, "<filter>",
			"LDAP filter for a group lookup; must contain {{userDN}}. Empty applies the built-in default."),
		"group_attribute": optString(128, "<attr>",
			"Attribute carrying the group name. Empty applies the built-in default."),
		"group_role_mapping": groupRoleMappingParam(
			"Directory group DN -> Nexara role id. A user's roles are rebuilt from this on every login and sync."),
		"default_role_id": {
			Type:     apischema.String,
			Optional: true,
			// A pattern rather than the uuid FORMAT: the empty string has
			// always meant "no default role", the same as an absent key, and
			// every registered format refuses "". See emptyOrUUID.
			Pattern:     emptyOrUUID,
			Typetext:    "<uuid>",
			Description: "Role assigned when no directory group matches. Empty or omitted assigns none.",
		},
		"sync_interval_minutes": {
			Type:     apischema.Integer,
			Optional: true,
			Default:  0,
			Minimum:  apischema.Ptr(0.0),
			// math.MaxInt32, because the column is int32 and this value is
			// narrowed to it. The struct field used to be int32, so the JSON
			// decoder refused an overflowing number for us; reading it as an
			// int64 and converting would WRAP instead. The bound keeps the old
			// refusal rather than imposing a new policy.
			Maximum:  apischema.Ptr(2147483647.0),
			Typetext: "<integer>",
			Description: "How often the scheduled sync runs, in minutes. 0 or omitted applies the built-in " +
				"default of 60.",
		},
		"acknowledge_insecure_tls": {
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<boolean>",
			Description: "Confirms storing a config that carries passwords over a connection that is " +
				"unencrypted, or encrypted but unverified. Only sent after the operator accepts the " +
				"warning the first attempt returns.",
		},
	}, extra)
}

// groupRoleMappingParam is the directory-group -> Nexara-role map that both
// this domain and the OIDC one carry.
//
// It is an Object, which apischema carries through UNVALIDATED: there is no
// nested-properties field, so the schema asserts only "this is a JSON object".
// The values therefore have to be checked by the handler — see
// handlers.StringMapFromObject — because the column is read back as a
// map[string]string and a numeric value would be stored and then silently
// dropped by the json.Unmarshal that reads it. That was a real narrowing to
// preserve: the map[string]string field this replaces made the JSON decoder
// refuse such a body with a 400.
func groupRoleMappingParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.Object,
		Optional:    true,
		Typetext:    "<object>",
		Description: description,
	}
}
