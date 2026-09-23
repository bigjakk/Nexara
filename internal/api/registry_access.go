package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, withParams and optString — lives in
// registry_vms.go, where the first migrated domain defined it.

// accessScope is the prefix every route in this domain hangs off. It is one
// segment deeper than clusterScope because router.go mounted these through
// clusters.Group("/:cluster_id/access"), and the group's path has to be spelled
// out here: mountRegistry attaches every endpoint at its full path so that the
// permission middleware shows up in the route's own Handlers slice (see
// mountRegistry in registry.go for why a Group would hide it).
const accessScope = clusterScope + "/access"

// Every route in this domain is a cluster-scoped Check on ONE resource, whose
// name is handlers.AccessResource rather than a literal here. The const carries
// the reason the resource exists at all — manage:access can mint an
// Administrator token, so migrations/000085_access_permissions.up.sql seeds it
// to Admin alone — and a second spelling of "access" in this file would be a
// copy that silently diverges the day that resource is renamed.
func accessView() Permissions   { return clusterCheck("view", handlers.AccessResource) }
func accessManage() Permissions { return clusterCheck("manage", handlers.AccessResource) }

// ── Path parameters ────────────────────────────────────────────────────
//
// Four of the five identifiers below become a SEGMENT of a Proxmox request
// path (internal/proxmox/client_access.go builds them with url.PathEscape), and
// PathEscape escapes "/" but leaves "." and ".." alone. pveproxy takes such a
// segment literally (proxmox.validatePathSegment records the evidence), but a
// normalising proxy in front of it resolves an un-anchored value upward and
// lands the request on the PARENT collection or above it: there DELETE
// /access/groups/. is the group COLLECTION, not the group the caller named,
// and /access/groups/.. is one level further up still. Keeping those two
// segments out is what every pattern below is for, which RE2 cannot express as
// a negative lookahead; registry_networks.go's pveObjectNamePattern and
// registry_ceph.go's cephPoolNameParam carry the same anchor for the same
// reason.
//
// Each pattern is deliberately the tightest thing that costs NOTHING a real
// caller sends, rather than the tightest thing that reads well. A narrowing here
// does not fail loudly — it 400s one operator with one unusual identifier, months
// later — so the classes below are derived from what the client-side validators
// accept and what encodeURIComponent emits, not from what looks like a sensible
// identifier.
//
// What is DIFFERENT here, and is the reason these are not simply
// pveObjectNameParam, is that the value arrives PERCENT-ENCODED. Fiber v3 does
// not decode path parameters, and a PVE user id is "name@realm", so a correct
// client sends "nexara%40pve" — see accessParam's own doc comment in
// handlers/access.go. The pattern therefore describes the value as it arrives on
// the wire, not as the handler will read it.
//
// The client-side validators stay exactly where they are. validateUserID,
// validateTokenID, validateGroupID, validateRoleID and validateRealm run on the
// DECODED value inside internal/proxmox, which is the only place that can see
// it: "%2e%2e" satisfies any pattern over the encoded form and becomes ".." only
// after url.PathUnescape. Those validators refuse "." and ".." by name, and they
// own the identifier vocabulary (the name@realm shape, the realm's leading
// letter). Nothing here replaces them; this is the same refusal made one layer
// earlier, with a message that names the parameter.
//
// Three of the four are spelled in the const block below because they are this
// domain's alone. The fourth, accessNamePattern, is a `var` under it instead:
// its rule is shared with pool ids and lives in the catalogue, for the reason
// its own comment gives.
const (
	// accessUserIDPattern is the ONE identifier that legitimately carries a
	// percent-escape: encodeURIComponent turns the "@" of "name@realm" into
	// "%40", and a literal "@" is also legal in a path segment (RFC 3986), so
	// both spellings have to pass. The alternation also admits a well-formed
	// escape anywhere, for a client that encodes more than it had to.
	//
	// The literal class is encodeURIComponent's OWN unreserved set — letters,
	// digits and `- _ . ! ~ * ' ( )` — plus "@", and it has to be, because
	// those are exactly the characters a correct client does NOT escape.
	// validateUserID (internal/proxmox/client_access.go) accepts every one of
	// them in the name half: it refuses only "/", "\", "%", control characters
	// and a name of exactly "." or "..". Narrowing the class to [A-Za-z0-9._@-]
	// would 400 a PVE-valid id like "o'brien@ad" that works today — an
	// apostrophe in an AD-realm username is ordinary.
	//
	// The ONE narrowing that stays is the first character, which excludes ".".
	// That is the traversal anchor, and it costs a leading-dot name such as
	// ".svc@pve" — a value validateUserID would accept. It is on the record
	// rather than hidden: a dot-led PVE account is vanishingly rare, and a raw
	// "." or ".." segment reaching url.PathEscape is the thing this refusal
	// exists for.
	accessUserIDPattern = `^(?:[A-Za-z0-9_@!~*'()-]|%[0-9A-Fa-f]{2})(?:[A-Za-z0-9._@!~*'()-]|%[0-9A-Fa-f]{2})*$`

	// accessTokenIDPattern is proxmox.accessTokenIDPattern verbatim — PVE's own
	// token-subid format. No percent-escape is admitted, and that is deliberate
	// rather than an omission: every character this class allows is one
	// encodeURIComponent leaves alone, so a correct client never sends an
	// escape here and admitting one would only widen what reaches the decoder.
	accessTokenIDPattern = `^[A-Za-z][A-Za-z0-9._-]+$`

	// accessRealmPattern is proxmox.accessRealmPattern verbatim.
	accessRealmPattern = `^[A-Za-z][A-Za-z0-9._-]*$`
)

// accessNamePattern is a PVE group or role id. proxmox.accessNamePattern is
// `^[A-Za-z0-9._-]+$`, which matches ".." — so what this carries is that class
// MINUS exactly {".", ".."}, which is what proxmox.validateAccessName refuses
// by name. Nothing else is taken away, and that exactness is the point: a group
// or role created outside Nexara must not become unreadable and undeletable
// through it.
//
// THE RULE ITSELF IS NOT SPELLED HERE, and that is the whole reason this line
// is a binding rather than a regex. Pool ids are the same charset in the same
// kind of path slot and need the identical carve-out, so the rule lives in the
// catalogue as path-safe-dotted-name and registry_pools.go's poolIDParam reads
// the same entry. A second copy here would be one rule in two files — invisible
// to TestNoInlinePatternIsWrittenTwice and TestNoInlinePatternRestatesACataloguedRule
// alike, because this identifier is not a literal — and neither copy would then
// be individually killable, since a mutation to either leaves the other
// refusing the same values.
//
// The catalogue entry carries the reasoning that used to sit here: why the
// regex has three branches (RE2 has no negative lookahead, so the pair is
// carved out by length), and why the obvious `^\.?[A-Za-z0-9_-][A-Za-z0-9._-]*$`
// is wrong in a way that does not look wrong — the optional leading dot eats
// the first character of "..archive" and makes a legal group unreachable.
var accessNamePattern = apischema.Rule("path-safe-dotted-name")

// accessUserIDParam is the PVE user id as a PATH parameter.
//
// The 192-character cap is three times validateUserID's own 64, because a
// 64-character user id in which every character was percent-escaped is 192
// characters on the wire. It is here to keep the path segment finite; the real
// 64 stays in validateUserID, which measures the decoded value.
var accessUserIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     accessUserIDPattern,
	MaxLength:   apischema.Ptr(192),
	Typetext:    "<user@realm>",
	Description: "PVE user id in name@realm form, percent-encoded (\"nexara%40pve\").",
}

// accessTokenIDParam is the API token NAME — the part after the "!" in
// "user@realm!tokenname", never the full token id.
var accessTokenIDParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     accessTokenIDPattern,
	MaxLength:   apischema.Ptr(64),
	Typetext:    "<token name>",
	Description: "API token name — the part after the \"!\" in user@realm!tokenname.",
}

// accessUserParams is the pair every per-user route carries.
func accessUserParams(extra apischema.Properties) apischema.Properties {
	return clusterParams(withParams(apischema.Properties{"userid": accessUserIDParam}, extra))
}

// accessTokenParams is accessUserParams plus the token name.
func accessTokenParams(extra apischema.Properties) apischema.Properties {
	return accessUserParams(withParams(apischema.Properties{"tokenid": accessTokenIDParam}, extra))
}

// accessNameParam builds a group or role id path parameter.
func accessNameParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Pattern:     accessNamePattern,
		MaxLength:   apischema.Ptr(64),
		Typetext:    "<id>",
		Description: description,
	}
}

// accessForceParam is the ?force= confirmation the four routes that can sever
// Nexara's own credential accept.
//
// Source is stated EXPLICITLY, and that is the load-bearing part: two of the
// four are PUTs, and ResolveSource reads an unsourced parameter on a mutating
// verb out of the BODY — where no caller has ever put this one. The frontend
// appends "?force=true" to the URL on all four.
//
// It is a Boolean rather than a string with an enum, which widens the accepted
// vocabulary slightly: forceRequested read only "1" and "true", where apischema
// also accepts "yes" and "on" (and refuses anything that is neither true nor
// false instead of silently reading it as "no"). Both changes point the same
// way. This flag decides whether Nexara is allowed to destroy the credential it
// reaches the cluster with, and "?force=yes" quietly meaning "no" is exactly the
// ambiguity the confirmation exists to remove.
func accessForceParam(what string) apischema.Property {
	return apischema.Property{
		Type:     apischema.Boolean,
		Optional: true,
		Default:  false,
		Source:   apischema.SourceQuery,
		Typetext: "<boolean>",
		Description: "Proceed even though " + what + " is the credential Nexara uses to reach this " +
			"cluster. Omitted, the request is refused with 409 and an explanation.",
	}
}

// accessExpireParam is a PVE account or token expiry, as a Unix timestamp.
//
// Zero means "never", which is Proxmox's own encoding and is why the minimum is
// 0 rather than 1. The ceiling is the last second of year 9999, a bound on a
// value that reaches strconv.FormatInt rather than a policy about how far ahead
// an operator may schedule one.
func accessExpireParam(description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.Integer,
		Optional:    true,
		Minimum:     apischema.Ptr(0.0),
		Maximum:     apischema.Ptr(253402300799.0),
		Typetext:    "<integer>",
		Description: description + " 0 never expires.",
	}
}

// registerAccessEndpoints declares all 25 of AccessHandler's routes.
//
// Every one of them is a plain cluster-scoped Check — 12 view:access and 13
// manage:access — and none is Deferred, Advisory or global. That uniformity is
// the domain's own shape rather than a simplification: each route acts on ONE
// cluster's Proxmox access model, reached through that cluster's own API token,
// and the cluster is the first parameter of every path. There is nothing for a
// handler to resolve that middleware could not.
//
// The three guards each handler keeps are unrelated to the permission and stay
// in the handler body, which is where they can see what the schema cannot:
//
//   - guardSelfCredential, the 409 that refuses to destroy the token Nexara
//     authenticates with unless ?force= says so. It reads the cluster row.
//   - accessUpdateAffectsAccess, which decides whether a user UPDATE is one of
//     the edits that can sever access (disable, expire, regroup) rather than a
//     cosmetic one.
//   - the identifier validators in internal/proxmox/client_access.go, which run
//     on the DECODED path segment. See the note on the patterns above.
func registerAccessEndpoints(reg *Registry, h *handlers.AccessHandler) {
	// ── Users ─────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   accessScope + "/users",
		Description: "List the cluster's Proxmox users. Sorted by user id, because PVE builds several " +
			"of these responses from an unsorted Perl hash and the order changes between requests.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListUsers,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   accessScope + "/users",
		Description: "Create a Proxmox user. Omitting the password creates an account that cannot log " +
			"in interactively, which is what a user that exists only to own an API token wants.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters:  clusterParams(createAccessUserParams()),
		Handler:     h.CreateUser,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        accessScope + "/users/:userid",
		Description: "Get one Proxmox user. Its groups arrive as an array here and as a comma-separated string in the listing — that difference is Proxmox's.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  accessUserParams(nil),
		Handler:     h.GetUser,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   accessScope + "/users/:userid",
		Description: "Change a Proxmox user. Every field is optional and an omitted one is left alone; " +
			"sending one EMPTY clears it. Disabling the account, giving it an expiry or rewriting its " +
			"groups can stop its API tokens working, so those three require ?force=true when the account " +
			"is the one Nexara authenticates with. A comment or e-mail change does not.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters: accessUserParams(withParams(updateAccessUserParams(), apischema.Properties{
			"force": accessForceParam("the account"),
		})),
		Handler: h.UpdateUser,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   accessScope + "/users/:userid",
		Description: "Delete a Proxmox user and, with it, every API token it owns. Deleting the account " +
			"Nexara authenticates with requires ?force=true.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters:  accessUserParams(apischema.Properties{"force": accessForceParam("the account")}),
		Handler:     h.DeleteUser,
	})

	// ── API tokens ────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        accessScope + "/users/:userid/tokens",
		Description: "List the API tokens a Proxmox user owns. Secrets are never returned — Proxmox has no read-back endpoint for one.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  accessUserParams(nil),
		Handler:     h.ListTokens,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        accessScope + "/users/:userid/tokens/:tokenid",
		Description: "Get one API token's metadata: its comment, expiry and privilege-separation flag. Never its secret.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  accessTokenParams(nil),
		Handler:     h.GetToken,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   accessScope + "/users/:userid/tokens/:tokenid",
		Description: "Mint a Proxmox API token. The response carries the secret, and this is the only " +
			"time it exists outside the cluster's own configuration — Proxmox will not show it again, and " +
			"it is deliberately kept out of the audit row. Surface it to the operator immediately.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters:  accessTokenParams(createAccessTokenParams()),
		Handler:     h.CreateToken,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   accessScope + "/users/:userid/tokens/:tokenid",
		Description: "Change an API token's metadata, or regenerate its secret. A regeneration invalidates " +
			"the previous secret immediately and returns the new one once, so regenerating the token Nexara " +
			"authenticates with requires ?force=true.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters: accessTokenParams(withParams(updateAccessTokenParams(), apischema.Properties{
			"force": accessForceParam("the token"),
		})),
		Handler: h.UpdateToken,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        accessScope + "/users/:userid/tokens/:tokenid",
		Description: "Revoke an API token. Revoking the one Nexara authenticates with requires ?force=true.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters:  accessTokenParams(apischema.Properties{"force": accessForceParam("the token")}),
		Handler:     h.DeleteToken,
	})

	// ── Groups ────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        accessScope + "/groups",
		Description: "List the cluster's Proxmox groups with their member lists.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListGroups,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        accessScope + "/groups",
		Description: "Create a Proxmox group.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters: clusterParams(apischema.Properties{
			"groupid": {
				Type:        apischema.String,
				Pattern:     accessNamePattern,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<id>",
				Description: "Identifier for the new group.",
			},
			"comment": accessCommentParam("Free-text note on the group."),
		}),
		Handler: h.CreateGroup,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        accessScope + "/groups/:groupid",
		Description: "Get one Proxmox group and the users in it.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  clusterParams(apischema.Properties{"groupid": accessNameParam("PVE group id.")}),
		Handler:     h.GetGroup,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   accessScope + "/groups/:groupid",
		Description: "Change a group's comment. Omitting it leaves the stored comment alone; sending it " +
			"empty clears it. Membership is not edited here — a user's groups are a field on the USER.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters: clusterParams(apischema.Properties{
			"groupid": accessNameParam("PVE group id."),
			"comment": accessCommentParam("New note. Sent empty, it clears the stored one."),
		}),
		Handler: h.UpdateGroup,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        accessScope + "/groups/:groupid",
		Description: "Delete a Proxmox group. ACL entries granted to it stop applying.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters:  clusterParams(apischema.Properties{"groupid": accessNameParam("PVE group id.")}),
		Handler:     h.DeleteGroup,
	})

	// ── Roles ─────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   accessScope + "/roles",
		Description: "List the cluster's roles and their privileges. The built-in Administrator role's " +
			"privilege list is Proxmox's complete set of valid privileges, so a client can build a " +
			"privilege picker from this response rather than hardcoding one that dates.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListRoles,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        accessScope + "/roles",
		Description: "Create a custom Proxmox role.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters: clusterParams(apischema.Properties{
			"roleid": {
				Type:        apischema.String,
				Pattern:     accessNamePattern,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(64),
				Typetext:    "<id>",
				Description: "Identifier for the new role.",
			},
			"privs": accessPrivsParam(true),
		}),
		Handler: h.CreateRole,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        accessScope + "/roles/:roleid",
		Description: "Get one role's privilege map.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  clusterParams(apischema.Properties{"roleid": accessNameParam("PVE role id.")}),
		Handler:     h.GetRole,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   accessScope + "/roles/:roleid",
		Description: "Replace a role's privilege list, or extend it with append=true. privs is REQUIRED " +
			"rather than optional: an absent value would have to mean either \"leave it alone\" or " +
			"\"remove everything\", and a client that forgot the field must not be able to disarm a role " +
			"by accident. Send it empty to clear the role deliberately.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters: clusterParams(apischema.Properties{
			"roleid": accessNameParam("PVE role id."),
			"privs":  accessPrivsParam(false),
			"append": {
				Type:        apischema.Boolean,
				Optional:    true,
				Default:     false,
				Typetext:    "<boolean>",
				Description: "Add the listed privileges to the role instead of replacing its list.",
			},
		}),
		Handler: h.UpdateRole,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   accessScope + "/roles/:roleid",
		Description: "Delete a custom Proxmox role. Proxmox refuses to delete a built-in one; the roles " +
			"listing marks those with special=true.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters:  clusterParams(apischema.Properties{"roleid": accessNameParam("PVE role id.")}),
		Handler:     h.DeleteRole,
	})

	// ── ACL ───────────────────────────────────────────────────────────
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        accessScope + "/acl",
		Description: "List the cluster's access control entries: which role each user, group or token holds on which path.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListACL,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   accessScope + "/acl",
		Description: "Grant roles on an ACL path, or revoke them with delete=true — Proxmox uses one " +
			"endpoint for both directions and the audit trail follows suit. At least one of users, groups " +
			"or tokens must name a subject.",
		Group:       "Proxmox Access Control",
		Permissions: accessManage(),
		Parameters:  clusterParams(updateAccessACLParams()),
		Handler:     h.UpdateACL,
	})

	// ── Realms (read-only) and the capability probe ───────────────────
	//
	// There is no realm write route, and its absence is deliberate: creating,
	// editing or deleting a realm needs PVE's Realm.Allocate privilege, which
	// sits in Proxmox's root tier and is carried by no built-in role except
	// Administrator.
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        accessScope + "/domains",
		Description: "List the cluster's authentication realms. Read-only.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  clusterParams(nil),
		Handler:     h.ListDomains,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   accessScope + "/domains/:realm",
		Description: "Get one realm's configuration. Read-only, and the response carries no credential: " +
			"the struct omits the LDAP/AD bind password by construction rather than fetching and blanking it.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters: clusterParams(apischema.Properties{
			"realm": {
				Type:        apischema.String,
				Pattern:     accessRealmPattern,
				MaxLength:   apischema.Ptr(32),
				Typetext:    "<realm>",
				Description: "Authentication realm id, as GET .../access/domains reports it.",
			},
		}),
		Handler: h.GetDomain,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   accessScope + "/permissions",
		Description: "Report what NEXARA's own cluster credential is permitted to do, as path -> privilege " +
			"-> propagate. Proxmox declares the underlying endpoint `user => 'all'`, so it answers even for " +
			"a token that can do nothing else — which makes it a dependable capability probe: the UI can " +
			"disable what will not work instead of surfacing a 403 after a form submit.",
		Group:       "Proxmox Access Control",
		Permissions: accessView(),
		Parameters:  clusterParams(nil),
		Handler:     h.GetPermissions,
	})
}

// accessCommentParam is the free-text note on a user, group, role or token.
//
// Every one of them is a TRISTATE on the wire: the Proxmox parameter structs
// carry *string, so omitting the key leaves the stored comment alone and sending
// it EMPTY clears it. That is why it has no Default — a default would make every
// request look like it supplied one, and p.OptString could no longer tell the
// two apart.
func accessCommentParam(description string) apischema.Property {
	return optString(1024, "<string>", description)
}

// accessPrivsParam is a role's comma-separated privilege list.
//
// required is the one difference between create and update, and it is pinned to
// what each already did rather than to what reads as a sensible minimum. On
// CREATE the handler dereferences a nil into "", so omitting it creates a role
// with no privileges — a legal, useful thing to do before filling it in. On
// UPDATE proxmox.UpdateAccessRole REFUSES a nil outright, because an empty privs
// value strips every privilege from the role and a client that forgot the field
// must not be able to disarm one by accident. That refusal is the choke point
// and stays in the client; this states the same rule a layer earlier, where it
// can name the parameter.
//
// The vocabulary itself is deliberately not an Enum. Proxmox owns the privilege
// list, it grows with each release, and GET .../access/roles already publishes
// the authoritative set as the Administrator role's own privs.
func accessPrivsParam(optional bool) apischema.Property {
	return apischema.Property{
		Type:      apischema.String,
		Optional:  optional,
		MaxLength: apischema.Ptr(4096),
		Typetext:  "<priv[,priv...]>",
		Description: "Comma-separated privilege list, e.g. \"VM.Audit,VM.PowerMgmt\". The complete set of " +
			"valid privileges is the built-in Administrator role's own list.",
	}
}

// createAccessUserParams is the body of POST .../access/users.
//
// userid is the only required field, matching the handler's one explicit check.
// Its SHAPE — the name@realm form, the realm's character class — belongs to
// proxmox.validateUserID, which owns it for every caller rather than for this
// one; what the declaration adds is the length bound and the closed parameter
// set. Unlike the path parameter of the same name this value is a form FIELD
// rather than a path segment, so it carries no traversal anchor and no
// percent-encoding.
func createAccessUserParams() apischema.Properties {
	return withParams(accessUserFieldParams(), apischema.Properties{
		"userid": {
			Type:        apischema.String,
			MinLength:   apischema.Ptr(1),
			MaxLength:   apischema.Ptr(64),
			Typetext:    "<user@realm>",
			Description: "User id for the new account, in name@realm form.",
		},
		"password": {
			Type:     apischema.String,
			Optional: true,
			// Write-only, and never echoed anywhere: CreateUser derives a
			// has_password BOOL for the audit row precisely so the marshalled
			// map never reads this field, because view:audit is granted to
			// every built-in Viewer.
			MaxLength: apischema.Ptr(1024),
			Typetext:  "<string>",
			Description: "Initial password. Omitted, the account has none and cannot log in interactively " +
				"at all — which is what a user that exists only to own an API token wants. Never returned " +
				"and never recorded in the audit trail.",
		},
	})
}

// updateAccessUserParams is the body of PUT .../access/users/:userid.
//
// It is createAccessUserParams without the identity fields and with `append`,
// which switches `groups` from "replace the list" to "add to it".
func updateAccessUserParams() apischema.Properties {
	return withParams(accessUserFieldParams(), apischema.Properties{
		"append": {
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<boolean>",
			Description: "Add the listed groups to the account's membership instead of replacing it. " +
				"Ignored when groups is omitted.",
		},
	})
}

// accessUserFieldParams are the mutable account attributes create and update
// share — proxmox.AccessUserFields, one for one.
//
// Every one of them is a TRISTATE, and keeping that is the whole reason they are
// declared without defaults: the struct's fields are pointers, so an omitted key
// leaves the stored value alone and an EMPTY one clears it. A Default here would
// make p.OptString report every request as having supplied the field, and a
// rename would start clearing the e-mail address of every account it touched.
//
// `email` deliberately carries no "email" format for the same reason: the format
// refuses the empty string, which is the only way to clear a stored address.
func accessUserFieldParams() apischema.Properties {
	return apischema.Properties{
		"comment": accessCommentParam("Free-text note. Sent empty, it clears the stored one."),
		"email": optString(255, "<string>",
			"Contact address. Sent empty, it clears the stored one — which is why this is not "+
				"validated as an e-mail address here; Proxmox rejects a malformed one."),
		"firstname": optString(255, "<string>", "Given name. Sent empty, it clears the stored one."),
		"lastname":  optString(255, "<string>", "Family name. Sent empty, it clears the stored one."),
		"groups": optString(4096, "<group[,group...]>",
			"Comma-separated group membership. Replaces the stored list unless append is set; sent "+
				"empty, it removes the account from every group."),
		"keys": optString(8192, "<string>",
			"SSH public keys stored on the account. Sent empty, it clears them."),
		"enable": {
			Type:     apischema.Boolean,
			Optional: true,
			// NO Default. A disabled account whose comment is edited must stay
			// disabled, and a Default of true here would re-enable it.
			Typetext:    "<boolean>",
			Description: "Whether the account may authenticate. Omitted, it is left as it is.",
		},
		"expire": accessExpireParam("When the account stops authenticating, as a Unix timestamp."),
	}
}

// createAccessTokenParams is the body of POST .../users/:userid/tokens/:tokenid.
//
// comment is a plain string here and a tristate on the update, which mirrors
// proxmox.CreateAccessTokenParams and proxmox.UpdateAccessTokenParams exactly:
// there is nothing to leave alone on a create, so "" and absent mean the same
// thing.
func createAccessTokenParams() apischema.Properties {
	return apischema.Properties{
		"comment": optString(1024, "<string>", "Free-text note on what the token is for."),
		"expire":  accessExpireParam("When the token stops working, as a Unix timestamp."),
		"privsep": {
			Type:     apischema.Boolean,
			Optional: true,
			// NO Default, deliberately. Omitting the key means "do not send it",
			// which lets PROXMOX apply its own default — privilege separation
			// ON. Defaulting it here would start writing an explicit privsep=1
			// on every mint and would silently own a decision Proxmox makes.
			Typetext: "<boolean>",
			Description: "Whether the token is privilege-separated — it then holds only the privileges " +
				"granted to the token itself, never the owning user's. Omitted, Proxmox's own default " +
				"applies, which is separated.",
		},
	}
}

// updateAccessTokenParams is the body of PUT .../users/:userid/tokens/:tokenid.
func updateAccessTokenParams() apischema.Properties {
	return apischema.Properties{
		"comment": accessCommentParam("New note. Sent empty, it clears the stored one."),
		"expire":  accessExpireParam("New expiry, as a Unix timestamp."),
		"privsep": {
			Type:        apischema.Boolean,
			Optional:    true,
			Typetext:    "<boolean>",
			Description: "Whether the token is privilege-separated. Omitted, it is left as it is.",
		},
		"regenerate": {
			Type:     apischema.Boolean,
			Optional: true,
			Default:  false,
			Typetext: "<boolean>",
			Description: "Rotate the secret. The previous one stops working immediately and the new one " +
				"is returned once — Proxmox has no read-back endpoint for it.",
		},
	}
}

// updateAccessACLParams is the body of PUT .../access/acl.
//
// `path` and `roles` are required because the client refuses an empty value for
// either, and "at least one of users, groups or tokens" is a cross-field rule
// apischema cannot state — Requires names a companion a parameter always needs,
// not one of a set. proxmox.UpdateAccessACL owns both rules and keeps them: it
// is the choke point every caller goes through, and validateACLPath additionally
// owns the ACL path's own shape (absolute, slash-separated, no relative segment).
func updateAccessACLParams() apischema.Properties {
	return apischema.Properties{
		"path": {
			Type:      apischema.String,
			MinLength: apischema.Ptr(1),
			MaxLength: apischema.Ptr(256),
			Typetext:  "<path>",
			Description: "ACL path the roles apply to — absolute and slash-separated, such as \"/\", " +
				"\"/vms/100\" or \"/storage/store01\".",
		},
		"roles": {
			Type:        apischema.String,
			MaxLength:   apischema.Ptr(4096),
			Typetext:    "<role[,role...]>",
			Description: "Comma-separated roles to grant or revoke. At least one is required.",
		},
		"users":  optString(4096, "<user[,user...]>", "Comma-separated user ids to act on."),
		"groups": optString(4096, "<group[,group...]>", "Comma-separated group ids to act on."),
		"tokens": optString(4096, "<token[,token...]>", "Comma-separated full token ids to act on."),
		"propagate": {
			Type:     apischema.Boolean,
			Optional: true,
			// NO Default: proxmox.UpdateAccessACLParams.Propagate is a *bool and
			// an omitted key means "do not send it", leaving Proxmox's own
			// default in place.
			Typetext:    "<boolean>",
			Description: "Whether the grant applies to paths below this one. Omitted, Proxmox's default applies.",
		},
		"delete": {
			Type:        apischema.Boolean,
			Optional:    true,
			Default:     false,
			Typetext:    "<boolean>",
			Description: "Revoke instead of grant. Proxmox uses one endpoint for both directions.",
		},
	}
}
