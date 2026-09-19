package api

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// declaredParameterNames returns the sorted parameter names of every
// declared endpoint, keyed "METHOD path".
func declaredParameterNames(reg *Registry) map[string][]string {
	if reg == nil {
		return nil
	}
	out := make(map[string][]string, reg.Len())
	for _, e := range reg.Endpoints() {
		out[e.Method+" "+e.Path] = slices.Sorted(maps.Keys(e.Parameters))
	}
	return out
}

// docParamsOf renders one endpoint's parameters, keyed by name, for the
// tests below.
func docParamsOf(t *testing.T, e Endpoint) map[string]handlers.APIParameter {
	t.Helper()
	reg := NewRegistry()
	reg.Register(e)
	eps := docEndpoints(reg)
	if len(eps) != 1 {
		t.Fatalf("docEndpoints returned %d endpoints, want 1", len(eps))
	}
	out := make(map[string]handlers.APIParameter, len(eps[0].Parameters))
	for _, p := range eps[0].Parameters {
		out[p.Name] = p
	}
	return out
}

// TestDocEndpoints_RendersEveryPermissionShape pins that the docs say
// something for all six Permissions shapes. endpointMeta could only spell
// the two that install a gate; the other four rendered as a blank, which
// reads as "undocumented" rather than as "no gate, on purpose".
func TestDocEndpoints_RendersEveryPermissionShape(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		perms Permissions
		want  string
	}{
		{
			name: "Check", path: "/api/v1/clusters/:cluster_id/probe",
			perms: clusterCheck("manage", "vm"), want: "manage:vm",
		},
		{
			name: "Alternatives", path: "/api/v1/clusters/:cluster_id/probe",
			perms: Permissions{Alternatives: []Check{
				{Action: "view", Resource: "vm", Scope: ScopeCluster},
				{Action: "view", Resource: "container", Scope: ScopeCluster},
			}},
			want: "view:vm | view:container",
		},
		{
			name: "Deferred", path: "/api/v1/probe",
			perms: Permissions{Deferred: "the resource depends on the request body"},
			want:  "deferred",
		},
		{
			name: "Advisory", path: "/api/v1/probe",
			perms: Permissions{Advisory: &AdvisoryCheck{
				Check:  Check{Action: "view", Resource: "cluster", Scope: ScopeCluster},
				Reason: "accessibleClusters filters the listing",
			}},
			want: "view:cluster (filtered)",
		},
		{
			name: "Public", path: "/api/v1/probe",
			perms: Permissions{Public: "the login form needs it before a session exists"},
			want:  "public",
		},
		{
			name: "SelfService", path: "/api/v1/probe",
			perms: Permissions{SelfService: "acts on the caller's own session only"},
			want:  "self-service",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params := apischema.Properties{}
			if strings.Contains(tc.path, ":cluster_id") {
				params["cluster_id"] = apischema.StdOption("cluster-id")
			}
			reg := NewRegistry()
			reg.Register(Endpoint{
				Method:      fiber.MethodGet,
				Path:        tc.path,
				Description: "Synthetic probe endpoint.",
				Group:       "Test",
				Permissions: tc.perms,
				Parameters:  params,
				Handler:     noopHandler,
			})

			eps := docEndpoints(reg)
			if len(eps) != 1 {
				t.Fatalf("docEndpoints returned %d endpoints, want 1", len(eps))
			}
			if eps[0].Permission != tc.want {
				t.Errorf("permission = %q, want %q", eps[0].Permission, tc.want)
			}
			if eps[0].Description != "Synthetic probe endpoint." || eps[0].Group != "Test" {
				t.Errorf("description/group = %q/%q, want the declaration's", eps[0].Description, eps[0].Group)
			}
		})
	}
}

// TestDocParameters_Sources covers every Source a parameter can resolve
// to, including the two that are INFERRED rather than declared: a name
// matching a :param is a path parameter, and everything else follows the
// method — query on a read, body on a write. Nothing documented any of
// this before, which is why every query-string parameter in the API was
// invisible to a reader.
func TestDocParameters_Sources(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		params apischema.Properties
		want   map[string]string
	}{
		{
			name:   "GET infers query for everything the path does not name",
			method: fiber.MethodGet,
			path:   "/api/v1/clusters/:cluster_id/probe",
			params: apischema.Properties{
				"cluster_id": apischema.StdOption("cluster-id"),
				"range":      {Type: apischema.String, Optional: true},
			},
			want: map[string]string{"cluster_id": "path", "range": "query"},
		},
		{
			name:   "POST infers body for everything the path does not name",
			method: fiber.MethodPost,
			path:   "/api/v1/clusters/:cluster_id/probe",
			params: apischema.Properties{
				"cluster_id": apischema.StdOption("cluster-id"),
				"storage":    {Type: apischema.String},
			},
			want: map[string]string{"cluster_id": "path", "storage": "body"},
		},
		{
			name:   "an explicit Source overrides the inference",
			method: fiber.MethodPost,
			path:   "/api/v1/probe",
			params: apischema.Properties{
				"force": {Type: apischema.Boolean, Optional: true, Source: apischema.SourceQuery},
				"name":  {Type: apischema.String},
			},
			want: map[string]string{"force": "query", "name": "body"},
		},
		{
			name:   "DELETE reads from the query, like every other read method",
			method: fiber.MethodDelete,
			path:   "/api/v1/probe",
			params: apischema.Properties{
				"purge": {Type: apischema.Boolean, Optional: true},
			},
			want: map[string]string{"purge": "query"},
		},
	}

	seen := map[string]bool{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := docParamsOf(t, Endpoint{
				Method: tc.method, Path: tc.path,
				Description: "Synthetic probe endpoint.", Group: "Test",
				Permissions: Permissions{Deferred: "synthetic"},
				Parameters:  tc.params,
				Handler:     noopHandler,
			})
			for name, want := range tc.want {
				p, ok := got[name]
				if !ok {
					t.Fatalf("parameter %q missing from the payload", name)
				}
				if p.Source != want {
					t.Errorf("%s source = %q, want %q", name, p.Source, want)
				}
				seen[p.Source] = true
			}
		})
	}

	for _, src := range []string{"path", "query", "body"} {
		if !seen[src] {
			t.Errorf("no case rendered source %q — a source the tests never see is "+
				"a source that can silently stop being rendered", src)
		}
	}
}

// TestDocParameters_OptionalAndDefault is the payload half of the
// distinction the boot disk was destroyed for. Three states, three
// renderings, and the middle one must not look like the third.
func TestDocParameters_OptionalAndDefault(t *testing.T) {
	got := docParamsOf(t, Endpoint{
		Method: fiber.MethodPost, Path: "/api/v1/probe",
		Description: "Synthetic probe endpoint.", Group: "Test",
		Permissions: Permissions{Deferred: "synthetic"},
		Parameters: apischema.Properties{
			"bus":     {Type: apischema.String, Enum: []string{"scsi", "sata"}},
			"index":   {Type: apischema.Integer, Optional: true},
			"retries": {Type: apischema.Integer, Optional: true, Default: 0},
			"full":    {Type: apischema.Boolean, Optional: true, Default: false},
		},
		Handler: noopHandler,
	})

	tests := []struct {
		param       string
		optional    bool
		hasDefault  bool
		wantDefault any
	}{
		{param: "bus", optional: false, hasDefault: false},
		{param: "index", optional: true, hasDefault: false},
		// A ZERO default is still a default. If these two collapsed into
		// "index", an omitted index would mean slot 0 — the boot disk.
		{param: "retries", optional: true, hasDefault: true, wantDefault: 0},
		{param: "full", optional: true, hasDefault: true, wantDefault: false},
	}

	for _, tc := range tests {
		t.Run(tc.param, func(t *testing.T) {
			p, ok := got[tc.param]
			if !ok {
				t.Fatalf("parameter %q missing from the payload", tc.param)
			}
			if p.Optional != tc.optional {
				t.Errorf("optional = %v, want %v", p.Optional, tc.optional)
			}
			if !tc.hasDefault {
				if p.Default != nil {
					t.Errorf("default = %#v, want none — an optional parameter with no default "+
						"means the endpoint does something else when the caller stays silent", p.Default)
				}
				return
			}
			if p.Default != tc.wantDefault {
				t.Errorf("default = %#v, want %#v", p.Default, tc.wantDefault)
			}
		})
	}
}

// TestDocParameters_Constraints pins that every rule a caller must
// satisfy reaches the payload.
//
// Without these, the docs answer "does this parameter exist" and not
// "what is a valid request" — which is the same failure the disk-attach
// incident was, one field along: snap_name's pattern forbids a leading
// digit, and a caller sending "1abc" gets a 400 nothing in the docs let
// them predict.
//
// The zero-valued cases are the point of the table. A minimum of 0 is a
// real floor and an absent minimum is no floor; a pointer is what keeps
// them apart, and a test that only ever used non-zero bounds would pass
// just as happily against a *float64 flattened to a float64.
func TestDocParameters_Constraints(t *testing.T) {
	got := docParamsOf(t, Endpoint{
		Method: fiber.MethodPost, Path: "/api/v1/probe",
		Description: "Synthetic probe endpoint.", Group: "Test",
		Permissions: Permissions{Deferred: "synthetic"},
		Parameters: apischema.Properties{
			"snap_name": {
				Type:      apischema.String,
				Pattern:   `^[A-Za-z][A-Za-z0-9_-]*$`,
				MinLength: apischema.Ptr(2),
				MaxLength: apischema.Ptr(40),
			},
			// minimum 0 / maximum 0 are the absent-vs-zero pair.
			"index": {
				Type: apischema.Integer, Optional: true,
				Minimum: apischema.Ptr(0.0), Maximum: apischema.Ptr(30.0),
			},
			"offset": {Type: apischema.Integer, Optional: true, Maximum: apischema.Ptr(0.0)},
			// max_length 0 accepts only the empty string. Nothing declares
			// one today; it is here so the rendering cannot start treating
			// a zero cap as no cap.
			"blank":     {Type: apischema.String, Optional: true, MaxLength: apischema.Ptr(0)},
			"unbounded": {Type: apischema.Integer, Optional: true},
			"newname":   {Type: apischema.String, Optional: true, Alias: "oldname"},
			"tags": {
				Type:     apischema.Array,
				Optional: true,
				Items: &apischema.Property{
					Type:      apischema.String,
					Enum:      []string{"red", "green"},
					MaxLength: apischema.Ptr(16),
				},
			},
		},
		Handler: noopHandler,
	})

	tests := []struct {
		name  string
		param string
		check func(t *testing.T, p handlers.APIParameter)
	}{
		{
			name: "pattern and length bounds survive", param: "snap_name",
			check: func(t *testing.T, p handlers.APIParameter) {
				if p.Pattern != `^[A-Za-z][A-Za-z0-9_-]*$` {
					t.Errorf("pattern = %q, want the declared regex", p.Pattern)
				}
				wantPtr(t, "min_length", p.MinLength, 2)
				wantPtr(t, "max_length", p.MaxLength, 40)
			},
		},
		{
			name: "a ZERO minimum is a bound, not an absence", param: "index",
			check: func(t *testing.T, p handlers.APIParameter) {
				wantPtr(t, "minimum", p.Minimum, 0.0)
				wantPtr(t, "maximum", p.Maximum, 30.0)
			},
		},
		{
			name: "a ZERO maximum is a bound too", param: "offset",
			check: func(t *testing.T, p handlers.APIParameter) {
				if p.Minimum != nil {
					t.Errorf("minimum = %v, want nil (none declared)", *p.Minimum)
				}
				wantPtr(t, "maximum", p.Maximum, 0.0)
			},
		},
		{
			name: "a ZERO max_length is a bound too", param: "blank",
			check: func(t *testing.T, p handlers.APIParameter) {
				wantPtr(t, "max_length", p.MaxLength, 0)
			},
		},
		{
			name: "an unbounded parameter carries no bounds at all", param: "unbounded",
			check: func(t *testing.T, p handlers.APIParameter) {
				if p.Minimum != nil || p.Maximum != nil || p.MinLength != nil || p.MaxLength != nil {
					t.Errorf("bounds = %v/%v/%v/%v, want all nil",
						p.Minimum, p.Maximum, p.MinLength, p.MaxLength)
				}
				if p.Pattern != "" || p.Alias != "" || p.Items != nil {
					t.Errorf("pattern/alias/items = %q/%q/%v, want all empty", p.Pattern, p.Alias, p.Items)
				}
			},
		},
		{
			name: "an alias is a second accepted name and must be shown", param: "newname",
			check: func(t *testing.T, p handlers.APIParameter) {
				if p.Alias != "oldname" {
					t.Errorf("alias = %q, want %q — omitting it documents the endpoint as "+
						"rejecting input it accepts", p.Alias, "oldname")
				}
			},
		},
		{
			name: "an array says what goes in it", param: "tags",
			check: func(t *testing.T, p handlers.APIParameter) {
				if p.Items == nil {
					t.Fatal(`items = nil; "array" on its own does not tell a caller what to send`)
				}
				if p.Items.Type != string(apischema.String) {
					t.Errorf("items.type = %q, want %q", p.Items.Type, apischema.String)
				}
				if !slices.Equal(p.Items.Enum, []string{"red", "green"}) {
					t.Errorf("items.enum = %v, want [red green]", p.Items.Enum)
				}
				wantPtr(t, "items.max_length", p.Items.MaxLength, 16)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := got[tc.param]
			if !ok {
				t.Fatalf("parameter %q missing from the payload", tc.param)
			}
			tc.check(t, p)
		})
	}
}

// wantPtr asserts a pointer-valued bound is present and holds want. The
// presence half is the one that matters: nil is how "no bound" is said.
func wantPtr[T comparable](t *testing.T, field string, got *T, want T) {
	t.Helper()
	if got == nil {
		t.Errorf("%s is absent, want %v", field, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %v, want %v", field, *got, want)
	}
}

// TestDocParameters_BoundsAreNotAliased proves the payload does not share
// a bound pointer with the schema it was rendered from. Register does not
// clone an endpoint's Parameters and neither does Compile, so the pointer
// the schema holds IS the one docParameters reads; apischema deep-copies
// a standard option per route for exactly this reason, and a payload that
// aliased the bound would hand every reader a pointer into the live
// schema.
//
// All FOUR bounds are checked, not just the first: they are four separate
// clonePtr calls, and a single-field test passes against three of them
// written as a plain assignment.
func TestDocParameters_BoundsAreNotAliased(t *testing.T) {
	props := apischema.Properties{
		"index": {
			Type: apischema.Integer, Optional: true,
			Minimum: apischema.Ptr(0.0), Maximum: apischema.Ptr(30.0),
		},
		"label": {
			Type: apischema.String, Optional: true,
			MinLength: apischema.Ptr(1), MaxLength: apischema.Ptr(64),
		},
	}
	got := docParamsOf(t, Endpoint{
		Method: fiber.MethodPost, Path: "/api/v1/probe",
		Description: "Synthetic probe endpoint.", Group: "Test",
		Permissions: Permissions{Deferred: "synthetic"},
		Parameters:  props,
		Handler:     noopHandler,
	})

	index, label := got["index"], got["label"]
	floats := []struct {
		field    string
		rendered *float64
		schema   *float64
	}{
		{"minimum", index.Minimum, props["index"].Minimum},
		{"maximum", index.Maximum, props["index"].Maximum},
	}
	for _, c := range floats {
		if c.rendered == nil {
			t.Errorf("%s is absent", c.field)
			continue
		}
		if c.rendered == c.schema {
			t.Errorf("the rendered %s aliases the schema's own pointer", c.field)
		}
	}

	ints := []struct {
		field    string
		rendered *int
		schema   *int
	}{
		{"min_length", label.MinLength, props["label"].MinLength},
		{"max_length", label.MaxLength, props["label"].MaxLength},
	}
	for _, c := range ints {
		if c.rendered == nil {
			t.Errorf("%s is absent", c.field)
			continue
		}
		if c.rendered == c.schema {
			t.Errorf("the rendered %s aliases the schema's own pointer", c.field)
		}
	}
}

// TestDocItems_CarriesEveryElementFacet walks the whole element mapping.
// Without it, deleting any of Pattern, Format, Typetext, Description,
// Minimum, Maximum or MinLength from docItems leaves every other test in
// the repo green — the array cases elsewhere only ever assert type, enum
// and max_length.
func TestDocItems_CarriesEveryElementFacet(t *testing.T) {
	item := &apischema.Property{
		Type:        apischema.String,
		Enum:        []string{"red", "green"},
		Format:      "storage-id",
		Pattern:     `^[a-z]+$`,
		Typetext:    "<colour>",
		Description: "A colour tag.",
		Minimum:     apischema.Ptr(0.0),
		Maximum:     apischema.Ptr(9.0),
		MinLength:   apischema.Ptr(1),
		MaxLength:   apischema.Ptr(16),
	}
	got := docItems(item)
	if got == nil {
		t.Fatal("docItems returned nil for a non-nil element")
	}

	if got.Type != string(apischema.String) {
		t.Errorf("type = %q, want %q", got.Type, apischema.String)
	}
	if !slices.Equal(got.Enum, item.Enum) {
		t.Errorf("enum = %v, want %v", got.Enum, item.Enum)
	}
	if got.Format != item.Format {
		t.Errorf("format = %q, want %q", got.Format, item.Format)
	}
	if got.Pattern != item.Pattern {
		t.Errorf("pattern = %q, want %q", got.Pattern, item.Pattern)
	}
	if got.Typetext != item.Typetext {
		t.Errorf("typetext = %q, want %q", got.Typetext, item.Typetext)
	}
	if got.Description != item.Description {
		t.Errorf("description = %q, want %q", got.Description, item.Description)
	}
	wantPtr(t, "items.minimum", got.Minimum, 0.0)
	wantPtr(t, "items.maximum", got.Maximum, 9.0)
	wantPtr(t, "items.min_length", got.MinLength, 1)
	wantPtr(t, "items.max_length", got.MaxLength, 16)

	// The enum slice is cloned, not aliased, for the same reason the
	// bounds are.
	if len(got.Enum) > 0 && &got.Enum[0] == &item.Enum[0] {
		t.Error("the rendered enum aliases the element schema's own slice")
	}
}

// TestDocItems_NilIsNil keeps the non-array path honest: every parameter
// that is not an array must render no items at all, rather than an empty
// element schema that reads as "an array of nothing".
func TestDocItems_NilIsNil(t *testing.T) {
	if got := docItems(nil); got != nil {
		t.Errorf("docItems(nil) = %+v, want nil", got)
	}
}

// TestDocParameters_Order pins the rendering order: path, then query,
// then body; required before optional; then by name. The docs are read
// side by side with a previous version often enough that a map's
// iteration order would be pure noise.
func TestDocParameters_Order(t *testing.T) {
	got := docEndpoints(func() *Registry {
		reg := NewRegistry()
		reg.Register(Endpoint{
			Method: fiber.MethodPost, Path: "/api/v1/clusters/:cluster_id/probe",
			Description: "Synthetic probe endpoint.", Group: "Test",
			Permissions: clusterCheck("manage", "vm"),
			Parameters: apischema.Properties{
				"cluster_id": apischema.StdOption("cluster-id"),
				"zeta":       {Type: apischema.String},
				"alpha":      {Type: apischema.String},
				"omega":      {Type: apischema.String, Optional: true},
				"beta":       {Type: apischema.String, Optional: true},
				"dry_run":    {Type: apischema.Boolean, Optional: true, Source: apischema.SourceQuery},
			},
			Handler: noopHandler,
		})
		return reg
	}())

	names := make([]string, 0, len(got[0].Parameters))
	for _, p := range got[0].Parameters {
		names = append(names, p.Name)
	}
	want := []string{"cluster_id", "dry_run", "alpha", "zeta", "beta", "omega"}
	if !slices.Equal(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}

func TestDocEndpoints_NilRegistry(t *testing.T) {
	if got := docEndpoints(nil); got != nil {
		t.Errorf("docEndpoints(nil) = %v, want nil", got)
	}
}

// TestGuard_DeclaredDocsReachTheDocsHandler proves the seam end to end
// against the REAL route table: setupRoutes must hand the docs handler
// every declaration, and every declared key must match a route that is
// actually registered.
//
// The second half is the same drift TestEndpointMetaMatchesRegisteredRoutes
// catches for the curated overlay, and it fails the same silent way: a
// key that matches no route renders nothing at all, and nothing else
// notices. It is worth asserting separately because the two keys are
// produced by different code — the overlay's are hand-typed, the
// declarations' come from Endpoint.Path — and only one of them is
// currently proven to survive Fiber's registration.
func TestGuard_DeclaredDocsReachTheDocsHandler(t *testing.T) {
	s := newRouteStubServer(t)

	declared := s.apiDocsHandler.DeclaredEndpointKeys()
	if len(declared) != s.registry.Len() {
		t.Fatalf("the docs handler holds %d declarations but the registry has %d — "+
			"setupRoutes is not pushing them (see SetDeclaredEndpoints in router.go)",
			len(declared), s.registry.Len())
	}
	if len(declared) == 0 {
		t.Fatal("no declarations at all; this guard would pass vacuously")
	}

	// Normalised through the same function GetDocs and
	// SetDeclaredEndpoints use. Comparing against the RAW route path
	// would make this guard disagree with the lookup it exists to
	// protect: a declaration at ".../foo/" matches a raw ".../foo/" and
	// passes here, while GetDocs trims both sides and resolves it fine —
	// so the guard would be answering a different question from the one
	// its failure message claims.
	registered := make(map[string]bool)
	for _, r := range s.app.GetRoutes(true) {
		registered[r.Method+" "+handlers.NormalizeDocPath(r.Path)] = true
	}
	for _, key := range declared {
		if !registered[key] {
			t.Errorf("declared endpoint %q matches no registered route — the docs would render "+
				"nothing for it, silently", key)
		}
	}
}

// TestDocEndpoints_TrailingSlashPathStillResolves covers the one path
// shape where the two sides of the docs lookup could disagree.
//
// GetDocs normalises the route-table path before looking a declaration
// up, so a declaration keyed on the RAW path would miss for any route
// mounted at ".../foo/" — fall through to endpointMeta, miss there too,
// and render the endpoint with a blank description and no parameters.
// Nothing declares such a path today, and Register does not forbid one:
// the registry_shadow_guard tests register exactly this shape on purpose,
// because Fiber's StrictRouting is unset and treats the two spellings as
// one route. So the agreement has to hold rather than be legislated away.
func TestDocEndpoints_TrailingSlashPathStillResolves(t *testing.T) {
	reg := NewRegistry()
	reg.Register(registryProbeEndpointNoParams("/api/v1/probe/",
		Permissions{Deferred: "synthetic"}, noopParamsHandler))

	h := handlers.NewAPIDocsHandler()
	h.SetDeclaredEndpoints(docEndpoints(reg))

	keys := h.DeclaredEndpointKeys()
	if len(keys) != 1 {
		t.Fatalf("keys = %v, want exactly 1", keys)
	}
	// Keyed the way GetDocs will ask for it — without the slash.
	if want := "GET /api/v1/probe"; keys[0] != want {
		t.Errorf("key = %q, want %q; the declaration would never be found", keys[0], want)
	}
}

// TestGuard_AttachDiskIndexStaysDefaultless pins the one parameter this
// whole phase exists for, in the payload a caller actually reads. A
// Default on `index` would make an omitted index mean slot 0, which on a
// VM with a disk is its boot disk — and the docs would then say so,
// which is worse than saying nothing.
func TestGuard_AttachDiskIndexStaysDefaultless(t *testing.T) {
	s := newRouteStubServer(t)

	const key = "POST /api/v1/clusters/:cluster_id/vms/:vm_id/disks/attach"
	var params []handlers.APIParameter
	for _, ep := range docEndpoints(s.registry) {
		if ep.Method+" "+ep.Path == key {
			params = ep.Parameters
			break
		}
	}
	if params == nil {
		t.Fatalf("%q is not declared; this guard would pass vacuously", key)
	}

	var index *handlers.APIParameter
	for i, p := range params {
		if p.Name == "index" {
			index = &params[i]
			break
		}
	}
	if index == nil {
		t.Fatalf("disks/attach declares no index parameter (got %v)", params)
	}
	if !index.Optional {
		t.Error("index is required; it must stay optional so a caller can omit it")
	}
	if index.Default != nil {
		t.Errorf("index declares default %#v; it must have NONE, so that omitting it "+
			"means \"lowest free slot\" and not \"slot 0\"", index.Default)
	}
	if index.Description == "" {
		t.Error("index has no description; the docs would not say what omitting it does")
	}
}

// TestGuard_DeclarationDropsNoDocumentedPermission (RETIRED) was the guard
// for the regression this phase very nearly shipped: GetDocs renders a
// declared route from its declaration and stops reading endpointMeta for
// it, so anything the overlay said and the declaration did not say would
// go silently missing from the PAYLOAD's permission field.
//
// Be precise about what it actually checked, because the obvious reading
// overstates it: `overlay := handlers.EndpointMetaPermissions()` returns
// only the `Permission` FIELD of each endpointMeta entry, never the
// Description. For convert-to-template that field was "manage:vm" —
// "manage:container" lived only in endpointMeta's Description sentence,
// which this guard never read and therefore never compared against
// anything. It could only ever prove "manage:vm still appears somewhere
// in what the declaration renders" for that route, never "manage:container
// still appears somewhere". Confirmed by re-running this exact body against
// registry_vms.go with "and manage:container as well when the guest is a
// container." deleted from both convert-to-template's and
// clone-to-template's Description: it still passes, 210 comparisons, zero
// findings. So while this comment block's earlier revision claimed the
// guard covered the manage:container half, it did not — nothing in this
// test suite did, and per the assessment on the endpointMeta cleanup this
// file's other guards inherited, nothing does today either.
//
// What it DID do was catch a route whose overlay Permission field (the one
// thing it could see) stopped appearing anywhere in the declaration — a
// one-time migration-safety check ("did we lose the ONE fact this narrow
// view had") against the OLD, frozen endpointMeta text, which is a
// comparison that needs an old copy to diff against. Its own anti-vacuity
// check — "no declared route has a curated permission to compare... remove
// it once endpointMeta no longer overlaps the registry" — said explicitly
// what should happen once that old copy was gone: this test's `checked`
// count is a self-fulfilling zero once every registry-overlapping
// endpointMeta entry is removed (see internal/api/handlers/api_docs.go's
// endpointMeta doc comment for the full removal), which is the state this
// repo is now in.
//
// Unlike TestAPIKeyDocsPromiseWhatTheRoutesEnforce (registry_api_keys_test.go,
// also retired the same way), there is no replacement test for the narrow
// thing this one verified, because there is nothing left to verify it
// against: a migration-time "did the Permission field survive" diff has no
// object once the field it diffed no longer exists anywhere. It is NOT a
// replacement for prose-level review of a Deferred or Advisory route's
// Description — it never did that job even before retirement.

// TestDeclaredParameterNames_CoversEveryPathParam re-states, over the
// real registry and through the docs accessor, what checkPathParams
// enforces at registration: a :param with no schema entry reaches the
// handler as a value no accessor can read.
func TestDeclaredParameterNames_CoversEveryPathParam(t *testing.T) {
	s := newRouteStubServer(t)

	names := declaredParameterNames(s.registry)
	for _, e := range s.registry.Endpoints() {
		declared := names[e.Method+" "+e.Path]
		for _, p := range pathParamNames(e.Path) {
			if !slices.Contains(declared, p) {
				t.Errorf("%s %s: path parameter %q is not in the documented parameter set %v",
					e.Method, e.Path, p, declared)
			}
		}
	}
}
