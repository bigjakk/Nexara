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
	// body alike. It is empty for a legacy route, and that emptiness is
	// honest rather than a placeholder: the route genuinely has no
	// machine-readable schema, and the gap is what tells a reader which
	// endpoints have been migrated.
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
}

// endpointMeta is the curated overlay for routes the endpoint registry does
// NOT own: the 17 routes still registered imperatively in router.go (9 of
// which carry an entry here; the other 8 — the storage content wildcard,
// the three branding reads, /healthz, the vm-folders reparent, and both
// firewall-templates routes — never had one and get auto-derived metadata
// instead). See legacy_route_ratchet_test.go for the full, closed list and
// why each one resists the registry.
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
