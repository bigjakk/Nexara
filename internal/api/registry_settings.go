package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — globalCheck — lives in
// registry_vms.go, where the first migrated domain defined it.

// settingsScope is the collection all nine settings routes hang off.
const settingsScope = pathPrefix + "settings"

// THREE of SettingsHandler's nine routes are declared here. The other six stay
// in router.go, and this comment is the reason — the whole reason, per route,
// because this domain is the one where "what does it enforce today" and "what
// can the declaration say" turned out to be different questions.
//
// # What each route actually enforces today
//
// Every settings route funnels its scope handling through settingScopeID
// (handlers/settings.go), and that function makes its permission check
// CONDITIONALLY:
//
//	if write && sc.adminOnly { requirePerm(c, "manage", "settings") }
//
// So:
//
//	GET  /settings            write=false → NO permission check runs, ever.
//	GET  /settings/:key       write=false → NO permission check runs, ever.
//	PUT  /settings/:key       write=true  → manage:settings, but ONLY when the
//	                                        scope is shared (global). A
//	                                        user-scoped write is keyed on the
//	                                        caller's own user_id and is ungated.
//	DELETE /settings/:key     write=true  → the same conditional.
//	POST /settings/branding/* an unconditional requirePerm(manage, settings) in
//	                          the handler body, before anything else.
//	GET  /settings/branding{,/logo-file,/favicon-file}
//	                          NO permission check at all; they are listed in
//	                          instanceSharedRoutes.
//
// That conditional is the LIVE INSTANCE named in rbac_route_guard_test.go's own
// LIMITATION note: the call-graph guard proves a check is REACHABLE, and
// settingScopeID is reachable from the reads — so the settings reads have
// always passed a guard they never satisfy.
//
// # What is declared, and why the rest is not
//
//   - The two branding UPLOADS declare a plain global Check. Their check is
//     unconditional and statically known, which is exactly what Check means.
//   - The DELETE declares Deferred, because the check genuinely depends on a
//     value only the handler sees: the ?scope=. That is the third bullet of the
//     decision — "genuinely conditional in a way no shape expresses".
//   - The PUT is the same shape and would declare the same thing, but it cannot
//     be declared at all: its `value` is arbitrary JSON. The branding page
//     stores a STRING, the appearance page stores an OBJECT, and json.Valid is
//     the only rule the handler applies — while apischema's Type vocabulary has
//     no "any JSON value" member. Declaring it Object would reject the branding
//     title; declaring it String would reject every preferences blob.
//   - The two generic READS perform no check on any path, so there is nothing
//     for Deferred to describe: its own doc comment requires the reason to say
//     what the handler checks, and Describe() renders it as "deferred", which
//     an operator reads as "a check is computed at runtime". Claiming that
//     would be a declaration saying MORE than the code enforces, which is worse
//     than the imperative call it replaced. What they actually are is
//     instanceSharedRoutes-shaped for ?scope=global and self-service for
//     ?scope=user, selected per request — and Permissions has no shape for
//     either half, let alone their union.
//   - The three branding READS are instanceSharedRoutes-shaped outright, and
//     Permissions has no shape for that: the comment on that map explains why
//     it is deliberately NOT selfServiceRoutes, and folding them in would make
//     that list's stated invariant false.
//
// TestSettingsReadsAreStillLegacy pins all six from both sides, and the report
// accompanying this change asks for the two missing vocabulary items and
// reports the READ enforcement gap on its own.

// settingKeyParam is the :key path parameter.
//
// settings.key is TEXT, so the 128-character cap is the application's alone —
// there is no database constraint behind it. The handler's settingKeyFromPath
// applies the same bound and stays, because the two reads and the PUT still go
// through it.
var settingKeyParam = apischema.Property{
	Type:      apischema.String,
	MinLength: apischema.Ptr(1),
	MaxLength: apischema.Ptr(128),
	Source:    apischema.SourcePath,
	Typetext:  "<key>",
	Description: "Setting key, e.g. dashboard.layout. Keys owned by a dedicated endpoint are refused " +
		"here under a shared scope, whatever permission the caller holds.",
}

// settingScopeParam is the ?scope= that selects which row a key names — and,
// on a write, which permission applies.
//
// The Enum is the handler's own settingScopes map restated one layer earlier.
// It is safe to restate: these are Nexara's own namespaces, not Proxmox's, and
// TestSettingScopeVocabulary pins the two against each other. The 'cluster'
// scope the settings table's CHECK also permits is deliberately absent from
// both — nothing plumbs a cluster id through these endpoints, so such a row
// would land on scope_id NULL, i.e. a second shared namespace that fell through
// the write gate entirely.
var settingScopeParam = apischema.Property{
	Type:     apischema.String,
	Optional: true,
	Default:  "user",
	Enum:     []string{"global", "user"},
	Typetext: "<global|user>",
	Description: "Which row the key names: \"user\" is the caller's own, \"global\" is the one every user " +
		"shares. A write to a shared scope requires manage:settings; a write to the caller's own does not.",
}

// settingDeleteReason is the Deferred justification for DELETE
// /api/v1/settings/:key.
//
// It names the condition, which is what Deferred requires: the permission
// depends on the ?scope= the caller sends, and middleware runs before that is
// read. It also names what the ungated branch is safe on — the row is keyed on
// the caller's own user_id — because "sometimes no permission is checked" is
// exactly the kind of statement that needs its own justification rather than a
// shrug.
const settingDeleteReason = "the permission depends on the ?scope= the caller sends: settingScopeID " +
	"requires manage:settings for a SHARED scope (global) and nothing for the caller's own (user), " +
	"whose row is keyed on their user_id and readable by nobody else; middleware runs before the query " +
	"string is bound to a parameter, so it cannot know which branch applies"

// registerSettingsEndpoints declares 3 of SettingsHandler's 9 routes. See the
// long comment above for what the other six are and why each one stays.
func registerSettingsEndpoints(reg *Registry, h *handlers.SettingsHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   settingsScope + "/branding/logo",
		Description: "Upload the instance logo, as a multipart form field named \"logo\". At most 2 MB, " +
			"and the bytes are decoded to confirm they really are the image the extension claims — the " +
			"stored file is then served with a default-src 'none' CSP, so an SVG that slipped past cannot " +
			"execute.",
		Group:       "Settings",
		Permissions: globalCheck("manage", "settings"),
		// Empty on purpose: the payload is multipart, not JSON, and declaring
		// nothing is what keeps extraction from touching the body at all. Fiber
		// runs with StreamRequestBody, so a c.Body() here would drain the upload
		// before c.FormFile could read it.
		Parameters: apischema.Properties{},
		Handler:    h.UploadLogo,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPost,
		Path:   settingsScope + "/branding/favicon",
		Description: "Upload the instance favicon, as a multipart form field named \"favicon\". At most " +
			"512 KB, .ico, .png or .svg, and validated and served under the same rules as the logo.",
		Group:       "Settings",
		Permissions: globalCheck("manage", "settings"),
		Parameters:  apischema.Properties{},
		Handler:     h.UploadFavicon,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodDelete,
		Path:   settingsScope + "/:key",
		Description: "Delete a setting. A shared-scope delete requires manage:settings; a user-scope one " +
			"affects only the caller's own row and requires none. Keys owned by a dedicated endpoint are " +
			"refused under a shared scope. Answers 204 whether or not a row existed — the DELETE is " +
			"unconditional — and records the attempt either way.",
		Group:       "Settings",
		Permissions: Permissions{Deferred: settingDeleteReason},
		Parameters: apischema.Properties{
			"key":   settingKeyParam,
			"scope": settingScopeParam,
		},
		Handler: h.DeleteSetting,
	})
}
