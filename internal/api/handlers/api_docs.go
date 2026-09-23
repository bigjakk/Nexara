package handlers

import (
	"sort"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// APIDocsHandler serves the API endpoint catalog. Endpoints are
// auto-discovered from Fiber's registered route table at request time
// so the docs cannot drift away from what the server actually serves.
//
// Each route's prose comes from one of two places, in this order:
//
//   - the DECLARATION, for a route the endpoint registry owns. The
//     registry states a route's description, group, permission and full
//     parameter schema next to the route itself, so the declaration is
//     the single source of truth and nothing here can disagree with it.
//     Package `api` pushes those declarations in via
//     [APIDocsHandler.SetDeclaredEndpoints] — see the note on that
//     method for why they arrive rather than being read.
//   - `endpointMeta`, the hand-curated overlay keyed on
//     `METHOD<space>path`, for the routes still registered imperatively
//     in router.go. Routes in neither get an auto-derived group and an
//     empty description, which is the signal that a curator should fill
//     them in — or, better, that the route should be migrated.
//
// Phase 5.8: previously a 134-entry hand-typed list that had drifted
// (~50% of registered routes were missing). Auto-generation from the
// route table was chosen over deletion because the per-page UX
// (search, grouping, method colours) is genuinely useful to operators
// scripting against the API.
type APIDocsHandler struct {
	app *fiber.App

	// declared holds the registry's declarations, keyed "METHOD path"
	// exactly as the route is registered. Written once at startup,
	// read-only from then on, like app.
	declared map[string]APIEndpoint
}

// NewAPIDocsHandler creates a new API docs handler. The app reference
// is the per-server Fiber instance whose routes the handler enumerates;
// it is wired in router.go::registerRoutes once the v1 group has been
// fully constructed.
func NewAPIDocsHandler() *APIDocsHandler { return &APIDocsHandler{} }

// SetApp attaches the Fiber app whose routes this handler enumerates.
// Called by the server once all routes are registered.
func (h *APIDocsHandler) SetApp(app *fiber.App) { h.app = app }

// SetDeclaredEndpoints hands the handler the endpoint registry's
// declarations. It is the same shape as SetApp and exists for the same
// structural reason: the registry lives in package `api`, which already
// imports this package, so `handlers` cannot import it back. The
// dependency is inverted instead — this package DEFINES the payload and
// package `api` FILLS it, at startup, from router.go's registry block.
//
// Passing the already-rendered payload rather than the Endpoint values
// also keeps the docs free of the registry's internals: nothing here has
// to know what a Permissions declaration is or how a parameter's source
// is resolved.
//
// eps is keyed internally on "METHOD path"; a duplicate key would be a
// registry that registered the same route twice, which Register already
// refuses.
func (h *APIDocsHandler) SetDeclaredEndpoints(eps []APIEndpoint) {
	m := make(map[string]APIEndpoint, len(eps))
	for _, e := range eps {
		// Keyed through the SAME normalisation GetDocs applies to the
		// route-table path before it looks a declaration up. Keying on
		// the raw path instead is a silent miss for any declaration
		// mounted at ".../foo/": the lookup would ask for ".../foo",
		// find nothing, fall through to endpointMeta, find nothing
		// there either, and render the endpoint blank.
		m[e.Method+" "+NormalizeDocPath(e.Path)] = e
	}
	h.declared = m
}

// DeclaredEndpointKeys returns the sorted "METHOD path" keys the
// registry declared. It mirrors EndpointMetaKeys and exists for the same
// guard test in internal/api: a declaration whose path does not match a
// registered route renders nothing, silently, exactly as a stale
// endpointMeta key does.
func (h *APIDocsHandler) DeclaredEndpointKeys() []string {
	keys := make([]string, 0, len(h.declared))
	for k := range h.declared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// APIEndpoint describes a single API endpoint.
type APIEndpoint struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description"`
	Permission  string `json:"permission"`
	Group       string `json:"group"`

	// Parameters is the route's full request contract — path, query and
	// body alike. It is absent in two different cases the payload does not
	// tell apart: a declared route that declares no parameters (and so
	// refuses any query key, and on a POST, PUT or PATCH any key in a JSON
	// body — a multipart upload's form fields are its handler's to read), and
	// one of the legacy routes, which has no machine-readable schema. So an
	// empty list is not the mark of a legacy route — docs/api-reference.md
	// says the same.
	Parameters []APIParameter `json:"parameters,omitempty"`
}

// APIParameter is one parameter of a declared endpoint, rendered as what
// a caller needs in order to form a request.
//
// Optional and Default are two separate facts and must stay that way.
// "Optional, no default" means the endpoint does something else when the
// caller says nothing — disks/attach picks the lowest free slot when
// `index` is omitted — while "optional, default 0" means it behaves as
// if the caller had asked for slot 0, i.e. the boot disk. Collapsing the
// two is precisely the ambiguity that destroyed a live VM's boot disk,
// and it is why Default is omitted from the JSON when absent rather than
// rendered as a zero value. There is no third state to encode: apischema
// refuses a declaration that is required AND carries a default.
//
// The same absent-versus-zero rule governs every bound below, and for
// the same reason: `minimum: 0` is a real floor, `minimum` absent is no
// floor, and a caller cannot tell them apart if the zero value stands in
// for both.
type APIParameter struct {
	Name string `json:"name"`
	Type string `json:"type"`

	// Source is where the value goes on the wire: "path", "query" or
	// "body". Nothing documented it before, which is why every
	// query-string parameter in this API was invisible to a reader.
	Source string `json:"source"`

	Optional bool `json:"optional"`

	// Default is omitted entirely when the parameter has none. Test
	// TestAPIParameterJSON_OptionalWithAndWithoutDefault pins that,
	// including for a `false` / `0` default, which must still render.
	Default any `json:"default,omitempty"`

	Enum        []string `json:"enum,omitempty"`
	Format      string   `json:"format,omitempty"`
	Typetext    string   `json:"typetext,omitempty"`
	Description string   `json:"description,omitempty"`

	// Pattern is the regex a string value must match. Undocumented, it is
	// the same failure the disk-attach incident was: `snap_name` may not
	// start with a digit, and a caller sending "1abc" got a 400 nothing in
	// the docs let them predict.
	Pattern string `json:"pattern,omitempty"`

	// The bounds are POINTERS for the reason Default is omitted when
	// absent: a minimum of 0 is a real bound and must not be
	// indistinguishable from "no bound". omitempty on a pointer drops it
	// only when nil, so *0 renders as `"minimum": 0` and an absent bound
	// renders as nothing at all.
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
	MinLength *int     `json:"min_length,omitempty"`
	MaxLength *int     `json:"max_length,omitempty"`

	// Alias is a second name the endpoint also accepts for this
	// parameter. Omitting it from the docs is worse than merely
	// incomplete: it documents the endpoint as REJECTING input it
	// accepts.
	Alias string `json:"alias,omitempty"`

	// Items is the element schema when Type is "array". Without it the
	// table says "array" and stops, which does not tell a caller what to
	// put in one.
	Items *APIItems `json:"items,omitempty"`

	// Rule is what the NAME in Format — or the regex in Pattern — actually
	// permits. Absent when neither names a catalogued rule.
	Rule *APIRule `json:"rule,omitempty"`

	// Requires names the parameters a caller must supply ALONGSIDE this
	// one. A companion carrying only its default does not satisfy it.
	Requires []string `json:"requires,omitempty"`
}

// APIItems is an array parameter's element schema.
//
// It is a type of its own rather than a nested APIParameter, because an
// element is not a parameter and the schema engine says so: apischema's
// compileItems accepts only scalar element types and REFUSES an element
// that declares a name's worth of context — optionality, a default, a
// source, an alias or a requires. A nested APIParameter would carry all
// six as fields that can only ever be empty, and would invite a reader to
// fill one in.
type APIItems struct {
	Type        string   `json:"type"`
	Enum        []string `json:"enum,omitempty"`
	Format      string   `json:"format,omitempty"`
	Pattern     string   `json:"pattern,omitempty"`
	Typetext    string   `json:"typetext,omitempty"`
	Description string   `json:"description,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	MinLength   *int     `json:"min_length,omitempty"`
	MaxLength   *int     `json:"max_length,omitempty"`

	// Rule is the element's own catalogued rule, on the same terms as
	// APIParameter.Rule. An element carries a format or a pattern as
	// readily as a parameter does — `node_names` on the DRS rule bodies is
	// an array of node-name — and an element whose rule went unstated
	// would be the original complaint one nesting level down.
	Rule *APIRule `json:"rule,omitempty"`
}

// APIRule is the catalogued rule a parameter's Format or Pattern names,
// rendered as what a caller needs in order to satisfy it.
//
// It exists because a NAME is not a RULE. `format: "pve-configid"` states
// that a rule applies without stating what it is, which leaves a caller
// with the same lookup task the catalogue was written to end: open the
// repo, find the format registry, read the regex. An external consumer
// cannot do even that.
//
// # This block describes the RULE, not the parameter
//
// Both fields below state what the named rule permits IN GENERAL. The
// parameter's own facets — its max_length, min_length, enum, minimum and
// maximum — are published beside it and apply AS WELL, exactly as
// `pattern` and `maxLength` coexist in JSON Schema: every assertion holds,
// and a value must satisfy all of them. A route is free to be stricter
// than the general rule, and several are.
//
// The live example is `snap_name` on the two snapshot-create routes. It
// declares `format: "pve-configid"`, whose rule permits 2 to 128
// characters, AND `max_length: 40`, because the client's own
// proxmox.ValidateSnapshotName caps it there. Both are published; the effective
// contract is their intersection, which is 2 to 40. Reading `permits` or
// `regex` on its own and concluding that a 64-character name will be
// accepted is the mistake this section exists to prevent — which is why
// the rule is not suppressed when a narrowing facet is present. Dropping
// the regex would leave a caller with less, not less-wrong: they would
// lose the character class and keep the length cap, and the character
// class is the half they cannot guess.
//
// One narrowing is NOT expressible in either place and is stated in the
// snap_name PARAMETER's Description instead (internal/api/registry_vms.go and
// internal/api/registry_containers.go): Proxmox reserves some names outright,
// and the set differs by guest kind — "current" for both, "pending" (in any
// case) for a VM, "vzdump" for a container; internal/proxmox/client_guests.go
// holds the rule and cites upstream for each. A schema facet cannot say
// "anything but these words", so the prose carries it.
//
// What is published here is deliberately a SUBSET of the catalogue entry
// (internal/api/apischema/catalogue.go). The entry also carries the
// upstream Proxmox file the rule was transcribed from, that upstream rule
// verbatim, a divergence note wherever ours differs, the accept/reject
// witnesses its tests run, and an Origin. Every one of the first four is
// written for a maintainer re-verifying the transcription — they name Go
// identifiers, repo paths and decisions that were considered and rejected
// — and none of them changes what a caller may send. They stay in the
// catalogue, which is where a maintainer reads them.
//
// ORIGIN WAS PUBLISHED AND THEN WITHDRAWN, and the reason is worth keeping
// because the field looks obviously useful. It conflates two questions
// that are not the same one: WHO WROTE THIS RULE, which is what the
// catalogue records, and WHO IS THE AUTHORITY ON THIS VALUE, which is what
// a caller wanted it for. Those diverge in both directions, for 81 of the
// 790 parameters that carry a rule:
//
//   - The five "-or-empty" rules inherit Origin from the rule they widen,
//     so 27 parameters reported "proxmox" for a rule Proxmox has no
//     validator for, and whose empty string — the whole reason the variant
//     exists — pve_verify_node_name rejects outright.
//   - pve-object-id (49 parameters) and pve-object-id-colon (5) report
//     "nexara" because Nexara defines them, yet those values ARE forwarded
//     to Proxmox and Proxmox is far stricter: POST /sdn/zones takes `zone`
//     under a rule admitting uppercase, dots, underscores and a leading
//     digit at 64 characters, where PVE's own pve-sdn-zone-id is
//     [a-z][a-z0-9]* capped at 8.
//
// A caller applying the documented semantics — "nexara means this API is
// the authority, so an upstream 400 would be a bug" — reaches a wrong
// conclusion at both. Nor is it fixable per entry: origin is a property of
// the RULE, and "who may still refuse this value" is a property of the
// ROUTE that carries it. A per-rule field cannot answer a per-route
// question. It also had no reader — the SPA never rendered it — and cost
// ~15 KB of payload to say nothing correct.
type APIRule struct {
	// Name is the catalogued rule's name. For a Format parameter it
	// repeats Format, which is not redundant in the one place it counts:
	// for a PATTERN parameter the payload carries only the regex, and the
	// name is the term an operator can search the catalogue for and quote
	// in a support thread.
	Name string `json:"name"`

	// Permits states in one line what the RULE allows — not what this
	// parameter allows. Where the parameter publishes a narrower facet of
	// its own, both hold and the narrower one binds. See the section on
	// the type above.
	//
	// This is the field the whole payload exists to carry.
	Permits string `json:"permits"`

	// Regex is the RULE's regular expression. It is one of the constraints
	// on this parameter and not the whole contract: a value must match it
	// AND satisfy every other facet the parameter publishes. Compiling it
	// alone yields a validator that is correct about shape and silent
	// about length — it would accept a 64-character snap_name the route
	// answers 400 for.
	//
	// It is present ONLY when the regex is the whole SHAPE check. A format
	// like `disk-size` or `ip` validates by parsing rather than by
	// matching, and has no regex to give; Permits states its rule in full
	// instead. Publishing a part-prose, part-regex string under this key
	// would hand a consumer something that looks compilable and is not, so
	// absence is the signal: a caller may compile this when it is here,
	// and must read Permits when it is not.
	//
	// For a Pattern parameter this repeats Pattern verbatim. The
	// duplication is on purpose — a consumer reads one rule out of one
	// place whichever kind it is — and it costs nothing, because the two
	// are the same string and gzip charges for it once.
	Regex string `json:"regex,omitempty"`
}

// endpointMeta is the curated overlay for routes the endpoint registry does
// NOT own: the 17 routes still registered imperatively in router.go (13 of
// which carry an entry here). Of the other 4, the three branding reads get
// auto-derived metadata — their derived section happens to be the right one
// — and /healthz gets none at all: GetDocs filters on the /api/v1/ prefix,
// so it never sees that route. See legacy_route_ratchet_test.go for the
// full, closed list and why each one resists the registry.
//
// FOUR of the 13 were added because their GROUP was wrong, and are worth
// naming because they are the shape this overlay is easiest to forget about.
// groupFromPath derives a section from the second path segment, which is
// right often enough to hide how often it is not: it filed the
// storage-content delete and the vm-folders reparent under "Clusters", while
// their 12 and 4 declared siblings sit in "Storage" and "Virtual Machines",
// and it invented a two-route "Firewall Templates" section beside the
// "Firewall" section that already held their four declared siblings. An
// operator hunting for an endpoint opens the wrong card and concludes it does
// not exist; nothing 500s and nothing logs. Declaring them instead — which
// would be the better fix, since it also shrinks the legacy set — is not
// available: each is held by the same limitation that makes it legacy at all
// (a greedy wildcard segment, a three-state parent_id, a JSON array of
// objects), and legacy_route_ratchet_test.go records which per route. So the
// overlay states the section the declaration would have.
// TestGuard_NoRouteFallsThroughToADerivedGroup (api_docs_drift_test.go) is
// what keeps the next one from going unnoticed for as long as these did.
//
// They carry a Description and a Permission as well, and the Permission is
// not decoration: an entry whose Permission is non-empty is compared against
// what the handler's call graph actually gates on, by
// TestGuard_DocumentedPermissionMatchesEnforcement (rbac_route_guard_test.go),
// which skips a blank one. Adding these four therefore put four routes under
// that check for the first time — so the strings have to be kept in step with
// the require*Perm calls in the handler bodies, not just with each other.
//
// GetDocs renders a registry-declared route from its declaration and never
// consults this map for it (see GetDocs' doc comment), so an entry here
// for a migrated route is inert as far as the docs payload goes. Until
// Phase 7, this map also carried a "shadow copy" entry for 223 of the
// (then) 535 declared routes — 42%, not all of them; most migrated routes
// never had an overlay entry to begin with — including the 6 API Keys
// ones, so that TestGuard_DocumentedPermissionMatchesEnforcement and
// registry_api_keys_test.go's TestAPIKeyDocsPromiseWhatTheRoutesEnforce
// had a second copy to compare each declaration's PERMISSION against
// (EndpointMetaPermissions exposes only that field, never Description —
// see the retirement note on TestGuard_DeclarationDropsNoDocumentedPermission
// in api_docs_payload_test.go for why that distinction matters). The two
// copies' Permission text usually agreed, by construction, but the overlay
// Description was not always the full picture either: convert-to-template's
// overlay Permission was "manage:vm" alone, and its "additionally requires
// manage:container" condition lived only in Description — a field neither
// this overlay's own consumer above nor the retired guard ever compared.
// All 223 of those entries were removed once the comparisons no longer needed one:
// TestAPIKeyRoutesDeclareTheSamePermissionTheyEnforced (registry_api_keys_test.go)
// already makes the same comparison against the DECLARATION, which is what
// an operator actually reads for a migrated route, and
// TestAPIKeyDocsPromiseWhatTheRoutesEnforce was retired rather than left
// checking a copy nothing renders. Nothing here can drift against a
// declaration when there is only one copy left to write into.
//
// Key format: "METHOD path" exactly as the route is registered (e.g.
// "GET /api/v1/clusters/:id"). The path is matched against
// `fiber.Route.Path` which uses ":param" syntax, so param NAMES must
// match the router registration verbatim (":cluster_id", not ":id") —
// a lookup miss silently renders the route with blank metadata.
// `TestEndpointMetaMatchesRegisteredRoutes` (internal/api) fails the
// build when a key here stops matching a registered route.
var endpointMeta = map[string]APIEndpoint{
	// ── Legacy routes (router.go; no registry declaration exists) ──────
	"GET /api/v1/api-docs":           {Description: "Get this API reference", Group: "API Documentation"},
	"GET /api/v1/auth/oidc/callback": {Description: "OIDC callback redirect target", Group: "Authentication"},
	"POST /api/v1/auth/logout":       {Description: "End the current session", Group: "Authentication"},
	"POST /api/v1/auth/register":     {Description: "Register a new user account", Group: "Authentication"},
	"GET /api/v1/settings":           {Description: "Get application settings (scope: global or user); global keys owned by a dedicated endpoint are omitted", Group: "Settings"},
	"GET /api/v1/settings/:key":      {Description: "Get a single setting (scope: global or user); global keys owned by a dedicated endpoint are rejected", Group: "Settings"},
	"PUT /api/v1/settings/:key":      {Description: "Create or update a setting; global-scope writes require manage:settings, user-scope writes affect only the caller, global keys owned by a dedicated endpoint are rejected", Permission: "manage:settings", Group: "Settings"},
	"POST /api/v1/alert-rules":       {Description: "Create an alert rule", Permission: "manage:alert", Group: "Alerts"},
	"PUT /api/v1/alert-rules/:id":    {Description: "Update an alert rule", Permission: "manage:alert", Group: "Alerts"},

	// ── Legacy routes whose DERIVED section was the wrong one ──────────
	//
	// Each names the Group its 4-to-12 declared siblings carry, so it
	// renders in the card an operator would look in. See the note above on
	// why these four cannot simply be declared.
	"DELETE /api/v1/clusters/:cluster_id/storage/:storage_id/content/*": {
		Description: "Delete one volume from a storage pool. The volume id is the greedy wildcard tail of the path — " +
			"percent-encoded, and the reason this route cannot be declared, since a parameter schema cannot describe a wildcard",
		Permission: "delete:storage",
		Group:      "Storage",
	},
	"PATCH /api/v1/clusters/:cluster_id/vm-folders/:folder_id": {
		Description: "Rename a folder, re-parent it, or both. parent_id is three-state: omitting it leaves the folder " +
			"where it is, an explicit null moves it to the top level, and a uuid moves it under that folder",
		Permission: "manage:vm_folder",
		Group:      "Virtual Machines",
	},
	"POST /api/v1/firewall-templates": {
		Description: "Create a firewall rule template. rules is a JSON array of rule objects, saved in Nexara's own " +
			"database rather than on a cluster; applying it to one is a separate, cluster-scoped route",
		Permission: "manage:network",
		Group:      "Firewall",
	},
	"PUT /api/v1/firewall-templates/:id": {
		Description: "Replace a firewall rule template's name, description and rules. Rules already applied to a " +
			"cluster are not touched — applying a template copies its rules rather than linking them",
		Permission: "manage:network",
		Group:      "Firewall",
	},
}

// EndpointMetaKeys returns the sorted "METHOD path" keys of the curated
// overlay. It exists for the route-drift guard test in internal/api,
// which asserts every key still matches a registered route.
func EndpointMetaKeys() []string {
	keys := make([]string, 0, len(endpointMeta))
	for k := range endpointMeta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// EndpointMetaGroups returns the curated "METHOD path" → Group mapping.
//
// It exists for TestGuard_NoRouteFallsThroughToADerivedGroup in
// internal/api, which has to tell a section the overlay STATED from one
// groupFromPath derived — and the rendered payload alone cannot, because
// the two are frequently the same string. "GET /api/v1/settings" renders
// "Settings" either way; the difference is whether anybody decided it.
func EndpointMetaGroups() map[string]string {
	out := make(map[string]string, len(endpointMeta))
	for k, v := range endpointMeta {
		out[k] = v.Group
	}
	return out
}

// EndpointMetaPermissions returns the curated "METHOD path" → permission
// mapping. It exists for the RBAC guard test in internal/api, which asserts
// the documented permission matches the action the handler actually enforces —
// operators build roles from this field, so drift here is a security-relevant
// lie rather than a cosmetic one.
func EndpointMetaPermissions() map[string]string {
	out := make(map[string]string, len(endpointMeta))
	for k, v := range endpointMeta {
		out[k] = v.Permission
	}
	return out
}

// NormalizeDocPath is the single spelling of a route's path used for
// every docs lookup: the route table's own path, the curated
// endpointMeta keys, and the registry's declarations.
//
// Fiber's Group(...) + .Post("/") produces a trailing-slash path like
// "/api/v1/api-keys/". Trimming it keeps the rendered docs readable and
// spares curators the convention — but the reason it is a FUNCTION, and
// exported, is that the two sides of a map lookup have to agree. Keying
// declarations one way and looking them up another is a miss that
// renders a blank endpoint and reports nothing, so the guard test in
// internal/api calls this rather than re-deriving the rule.
func NormalizeDocPath(path string) string {
	if len(path) > len("/api/v1/") && strings.HasSuffix(path, "/") {
		return strings.TrimSuffix(path, "/")
	}
	return path
}

// groupFromPath derives a Group label from a path when the curated
// `endpointMeta` map has no entry for it. The second segment after
// `/api/v1/` is the natural carve-up (e.g. `/api/v1/ldap/...` →
// "Ldap"). Falls through to "Other" for paths that don't fit.
//
// It is a LAST RESORT, not a default. The second segment is the resource a
// route hangs off, which is only sometimes the section it belongs in:
// everything under `/api/v1/clusters/…` derives "Clusters" however deeply
// nested, and a collection of its own derives a section of its own even when
// its siblings already have one. Four routes rendered in the wrong card that
// way before anything looked. So a route reaching here is a finding rather
// than a state — TestGuard_NoRouteFallsThroughToADerivedGroup in
// internal/api/api_docs_drift_test.go reports every one that is not on a
// short reviewed list, and pins the derived value for the ones that are.
func groupFromPath(path string) string {
	const prefix = "/api/v1/"
	if !strings.HasPrefix(path, prefix) {
		return "Other"
	}
	rest := strings.TrimPrefix(path, prefix)
	seg, _, _ := strings.Cut(rest, "/")
	if seg == "" {
		return "Other"
	}
	// Title-case dashes / underscores. "alert-rules" -> "Alert Rules".
	parts := strings.FieldsFunc(seg, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// GetDocs returns the auto-generated API endpoint catalog.
//
// The route table is the canonical list — an endpoint nobody serves is
// not documented, and an endpoint nobody documented still appears. What
// each entry SAYS comes from the declaration when the registry owns the
// route, and from endpointMeta otherwise; see the type comment.
//
// The declaration is taken whole rather than field-by-field. Register
// already refuses a declaration with no Description, no Group or no
// Permissions, so there is no blank field for the overlay to fill —
// and a per-field merge would quietly resurrect a stale endpointMeta
// value the moment a declaration legitimately said something shorter.
func (h *APIDocsHandler) GetDocs(c fiber.Ctx) error {
	if h.app == nil {
		// SetApp wasn't called yet — surface the failure rather than
		// silently returning an empty list, which would confuse the
		// frontend.
		return fiber.NewError(fiber.StatusInternalServerError, "API docs handler is not wired to a Fiber app")
	}

	routes := h.app.GetRoutes(true) // filterUseOption=true → skip USE() middleware
	seen := make(map[string]bool, len(routes))
	out := make([]APIEndpoint, 0, len(routes))

	for _, r := range routes {
		if !strings.HasPrefix(r.Path, "/api/v1/") {
			continue
		}
		// Skip Fiber's auto-generated HEAD / OPTIONS / TRACE entries —
		// they show up alongside every GET/POST and would double the
		// list size.
		if r.Method == fiber.MethodHead || r.Method == fiber.MethodOptions || r.Method == fiber.MethodTrace {
			continue
		}
		path := NormalizeDocPath(r.Path)
		key := r.Method + " " + path
		if seen[key] {
			continue
		}
		seen[key] = true

		ep, declared := h.declared[key]
		if !declared {
			ep = endpointMeta[key]
		}
		ep.Method = r.Method
		ep.Path = path
		if ep.Group == "" {
			ep.Group = groupFromPath(path)
		}
		out = append(out, ep)
	}

	// Sort by group, then method (with GET first to match REST conventions),
	// then path. Stable order makes the rendered docs predictable.
	methodRank := map[string]int{
		"GET": 0, "POST": 1, "PUT": 2, "PATCH": 3, "DELETE": 4,
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return methodRank[out[i].Method] < methodRank[out[j].Method]
	})

	return RespondItems(c, out)
}
