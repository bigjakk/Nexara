package api

import (
	"maps"
	"reflect"
	"regexp"
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

// TestDocParameters_Constraints pins that every rule a caller must satisfy reaches the payload: without
// them the docs answer "does this parameter exist", not "what is a valid request" (snap_name's pattern
// forbids a leading digit, and "1abc" is a 400 nothing in the docs predicts). The zero-valued cases
// are the point: a minimum of 0 is a real floor and an absent minimum is no floor, a pointer keeps
// them apart, and non-zero bounds alone would pass against a *float64 flattened to a float64.
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
			"kev":       {Type: apischema.Boolean, Optional: true, Default: false, EmptyIsAbsent: true},
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
				if p.Pattern != "" || p.Alias != "" || p.Items != nil || p.EmptyIsAbsent {
					t.Errorf("pattern/alias/items/empty_is_absent = %q/%q/%v/%v, want all empty",
						p.Pattern, p.Alias, p.Items, p.EmptyIsAbsent)
				}
				// Added with the rule block: a parameter that names no rule
				// must not acquire one. Without this line the "unbounded"
				// case asserted the absence of every facet EXCEPT the
				// newest, which is how a guard quietly stops covering the
				// field most likely to be wrong.
				if p.Rule != nil {
					t.Errorf("rule = %+v, want nil — this parameter names neither a format nor a "+
						"pattern, so there is no rule to attribute to it", p.Rule)
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
			name: "an empty value read as omitted must be shown", param: "kev",
			check: func(t *testing.T, p handlers.APIParameter) {
				if !p.EmptyIsAbsent {
					t.Error("empty_is_absent = false — a boolean admits no empty value, so omitting the facet " +
						"documents the endpoint as rejecting ?kev= when it accepts it")
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

// TestDocParameters_BoundsAreNotAliased proves the payload does not share a bound pointer with the
// schema it was rendered from: Register and Compile do not clone Parameters, so the pointer the schema
// holds IS the one docParameters reads, and a payload that aliased it would hand every reader a pointer
// into the live schema. All FOUR bounds are checked: they are four separate clonePtr calls, and a
// single-field test passes against three of them written as a plain assignment.
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

// TestGuard_DeclaredDocsReachTheDocsHandler proves the seam against the REAL route table: setupRoutes
// must hand the docs handler every declaration, and every declared key must match a registered route.
// The second half is the drift TestEndpointMetaMatchesRegisteredRoutes catches for the overlay, failing
// the same silent way (a key matching no route renders nothing); it is separate because the keys come
// from different code (the overlay's are hand-typed, the declarations' come from Endpoint.Path) and
// only one is proven to survive Fiber's registration.
func TestGuard_DeclaredDocsReachTheDocsHandler(t *testing.T) {
	s := sharedRouteStub(t)

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

// TestDocEndpoints_TrailingSlashPathStillResolves covers the one path shape where the two sides of the
// docs lookup could disagree: GetDocs normalises the route-table path before the lookup, so a
// declaration keyed on the RAW path would miss for a route mounted at ".../foo/" and render a blank
// endpoint. Nothing declares one today and Register does not forbid it (StrictRouting is unset, so
// the two spellings are one route), so the agreement has to hold rather than be legislated away.
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
// Default on `index` would not change what an omitted index does — the
// handler's OptInt read still sees it as omitted and picks a free slot
// (apischema.Property.Default) — but the docs would then say an omitted
// index means slot 0, which on a VM with a disk is its boot disk, and
// that is worse than saying nothing.
func TestGuard_AttachDiskIndexStaysDefaultless(t *testing.T) {
	s := sharedRouteStub(t)

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

// The old TestGuard_DeclarationDropsNoDocumentedPermission was retired with the endpointMeta overlay
// entries it diffed against, and nothing replaces it. It read only the overlay's Permission FIELD,
// never the Description, so it never covered convert-to-template's manage:container half (deleting
// that sentence from the declaration left it green); a migration-time diff needs an old copy to diff
// against, and prose-level review of a Deferred or Advisory Description was never its job.

// TestDeclaredParameterNames_CoversEveryPathParam re-states, over the
// real registry and through the docs accessor, what checkPathParams
// enforces at registration: a :param with no schema entry reaches the
// handler as a value no accessor can read.
func TestDeclaredParameterNames_CoversEveryPathParam(t *testing.T) {
	s := sharedRouteStub(t)

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

// TestDocParameters_RuleText is the payload half of the rule catalogue's payoff: a parameter that
// names a rule must also say what it permits. A bare `format: "pve-configid"` states that a rule
// applies without stating what it is; an operator on the in-app catalog can read
// apischema/catalogue.go, an external consumer of /api/v1/api-docs cannot (the undocumented `pattern`
// failure, one indirection along).
func TestDocParameters_RuleText(t *testing.T) {
	got := docParamsOf(t, Endpoint{
		Method: fiber.MethodPost, Path: "/api/v1/probe",
		Description: "Synthetic probe endpoint.", Group: "Test",
		Permissions: Permissions{Deferred: "synthetic"},
		Parameters: apischema.Properties{
			// A format whose rule IS a regex: the payload publishes both.
			"snap_name": {Type: apischema.String, Format: "pve-configid"},
			// A format whose rule is NOT a regex — disk-size parses and
			// converts rather than matching — so there is no regex to give
			// and Permits has to carry the whole rule.
			"size": {Type: apischema.String, Optional: true, Format: "disk-size"},
			// A catalogued PATTERN, reached by value rather than by name.
			"alias": {Type: apischema.String, Optional: true, Pattern: apischema.Rule("pve-object-id")},
			// A DERIVED sentinel rule. Its whole reason to exist is the
			// empty string, so its Permits line is the one place a caller
			// can learn what sending "" does.
			"pool": {Type: apischema.String, Optional: true, Pattern: apischema.Rule("pve-poolid-or-empty")},
			// A pattern nobody catalogued: still published as a regex,
			// with no prose, which is the honest gap.
			"adhoc": {Type: apischema.String, Optional: true, Pattern: `^zzz-[0-9]+$`},
			// No rule at all.
			"plain": {Type: apischema.String, Optional: true},
			// An element schema carries a rule on the same terms.
			"node_names": {
				Type: apischema.Array, Optional: true,
				Items: &apischema.Property{Type: apischema.String, Format: "node-name"},
			},
		},
		// A node list on a path naming no cluster is only accepted as the
		// handler's own to check (see Endpoint.NodesCheckedByHandler).
		NodesCheckedByHandler: map[string]string{"node_names": "synthetic"},
		Handler:               noopHandler,
	})

	t.Run("a format publishes what it permits, and its regex", func(t *testing.T) {
		r := got["snap_name"].Rule
		if r == nil {
			t.Fatal(`snap_name renders format:"pve-configid" with no rule — the docs name a rule ` +
				`and decline to say what it is, which is the gap the catalogue exists to close`)
		}
		if r.Name != "pve-configid" {
			t.Errorf("rule.name = %q, want %q", r.Name, "pve-configid")
		}
		want, _ := apischema.LookupRule("pve-configid")
		if r.Permits != want.Permits {
			t.Errorf("rule.permits = %q, want the catalogue's own line %q", r.Permits, want.Permits)
		}
		if r.Permits == "" {
			t.Error("rule.permits is empty; a rule block with no prose publishes nothing new")
		}
		if r.Regex != `^[A-Za-z][A-Za-z0-9_-]{1,127}$` {
			t.Errorf("rule.regex = %q, want the catalogued regex — a format's regex is reachable "+
				"NOWHERE ELSE in this payload, so dropping it leaves a consumer unable to "+
				"pre-validate at all", r.Regex)
		}
	})

	t.Run("a rule that is not one regex publishes none", func(t *testing.T) {
		r := got["size"].Rule
		if r == nil {
			t.Fatal("size renders no rule for its disk-size format")
		}
		if r.Permits == "" {
			t.Error("rule.permits is empty, and it is the ONLY statement of this rule there is")
		}
		if r.Regex != "" {
			t.Errorf("rule.regex = %q, want empty: disk-size validates by parsing and converting, "+
				"so anything here looks compilable and is not", r.Regex)
		}
	})

	t.Run("a catalogued pattern says what it permits too", func(t *testing.T) {
		p := got["alias"]
		r := p.Rule
		if r == nil {
			t.Fatal(`alias carries the catalogued rule pve-object-id and renders no rule block. ` +
				`A regex says what SHAPE a value has and never why — that this one refuses a ` +
				`leading dot to keep ".." out of a Proxmox path is unreadable from the regex`)
		}
		if r.Name != "pve-object-id" {
			t.Errorf("rule.name = %q, want %q — the name is the only handle a reader of a "+
				"PATTERN parameter has, since the payload otherwise carries just the regex",
				r.Name, "pve-object-id")
		}
		if r.Regex != p.Pattern {
			t.Errorf("rule.regex = %q but pattern = %q; for a pattern rule the two ARE the same "+
				"string and must not be able to disagree", r.Regex, p.Pattern)
		}
	})

	t.Run("a sentinel rule's line reaches the payload and names the empty string", func(t *testing.T) {
		// A derived sentinel rule is the one kind whose Permits line has a
		// job its base's does not: the base rejects "" and this admits it,
		// so if the line never names the empty string the payload has not
		// said what distinguishes the two.
		//
		// It deliberately stops there. Publishing what "" MEANS was tried
		// and reverted — the meaning is per-ROUTE, not per-rule (see
		// derive's doc comment in apischema/catalogue.go), and three of the
		// five lines were wrong at most of their sites, one of them
		// contradicting its own Description in the same payload cell.
		r := got["pool"].Rule
		if r == nil {
			t.Fatal("pool carries pve-poolid-or-empty and renders no rule block")
		}
		want, ok := apischema.LookupRule("pve-poolid-or-empty")
		if !ok {
			t.Fatal("pve-poolid-or-empty left the catalogue")
		}
		// Pinned against the catalogue rather than against frozen prose:
		// the line is free to be reworded, it is not free to stop arriving.
		if r.Permits != want.Permits {
			t.Errorf("rule.permits = %q, want the catalogue's own line %q", r.Permits, want.Permits)
		}
		if !strings.Contains(r.Permits, "empty string") {
			t.Errorf("rule.permits = %q and never mentions the empty string, which is the only "+
				"reason this rule exists apart from its base", r.Permits)
		}
		// What is NOT asserted, and must not be: that the line says what sending "" DOES. Two of the five rules
		// do, because theirs generalises over every site; three deliberately do not, and a shape check would
		// fail the three or a rewording of the two and be deleted rather than satisfied (the reason prose-
		// scraping was rejected in registry_rule_catalogue_test.go). A "must be longer than its base" draft was
		// wrong on its first run: the derived line is legitimately SHORTER, naming the base rule instead of
		// restating it. The subtest name says what it proves, that the line ARRIVES and NAMES the empty
		// string, not a meaning check that would have passed against a line with none.
	})

	t.Run("an uncatalogued pattern renders no rule", func(t *testing.T) {
		p := got["adhoc"]
		if p.Pattern != `^zzz-[0-9]+$` {
			t.Errorf("pattern = %q, want the declared regex", p.Pattern)
		}
		if p.Rule != nil {
			t.Errorf("rule = %+v, want nil: attributing an arbitrary regex to a catalogue entry "+
				"would publish provenance this rule does not have", p.Rule)
		}
	})

	t.Run("a parameter with no named rule renders none", func(t *testing.T) {
		if r := got["plain"].Rule; r != nil {
			t.Errorf("rule = %+v, want nil", r)
		}
	})

	t.Run("an array element carries its rule", func(t *testing.T) {
		items := got["node_names"].Items
		if items == nil {
			t.Fatal("node_names lost its element schema")
		}
		if items.Rule == nil {
			t.Fatal(`the element renders format:"node-name" with no rule — the original ` +
				`complaint, one nesting level down`)
		}
		if items.Rule.Name != "node-name" || items.Rule.Permits == "" {
			t.Errorf("items.rule = %+v, want the node-name entry with its prose", items.Rule)
		}
	})
}

// TestBuildRuleByPattern indexes the catalogue the way docRule reads it,
// and pins the collision case.
//
// The panic is the point. Two entries sharing one regex leaves the lookup
// with two candidates and no way to choose, so it would attribute the
// parameter to whichever Go's map iteration reached first — a different
// answer per process, and a wrong one either way. Failing at init makes
// that a build-time stop rather than a docs page that quietly lies.
func TestBuildRuleByPattern(t *testing.T) {
	t.Run("indexes patterns and excludes formats", func(t *testing.T) {
		idx := buildRuleByPattern(apischema.Catalogue())
		if got := idx[apischema.Rule("pve-object-id")]; got != "pve-object-id" {
			t.Errorf("lookup of the pve-object-id regex = %q, want %q", got, "pve-object-id")
		}
		// uuid is a FORMAT. Its regex must not resolve to a rule through
		// the pattern index: a value that merely matches it has not been
		// through the format's normalization, so documenting the two as
		// the same rule would promise a rewrite the route never performs.
		uuid, ok := apischema.LookupRule("uuid")
		if !ok {
			t.Fatal("the uuid format left the catalogue")
		}
		if got, found := idx[uuid.Rule]; found {
			t.Errorf("the uuid format's regex resolves to %q through the pattern index; formats "+
				"must be reachable by NAME only", got)
		}
	})

	t.Run("panics when two rules share one regex", func(t *testing.T) {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("buildRuleByPattern accepted two rules with the same regex; the docs would " +
					"attribute a parameter to whichever one the map happened to hash first")
			}
			msg, _ := r.(string)
			for _, want := range []string{"first-name", "second-name"} {
				if !strings.Contains(msg, want) {
					t.Errorf("panic message %q does not name %q", msg, want)
				}
			}
		}()
		buildRuleByPattern([]apischema.RuleDoc{
			{Name: "first-name", Kind: apischema.KindPattern, Rule: `^same$`, RuleIsRegex: true},
			{Name: "second-name", Kind: apischema.KindPattern, Rule: `^same$`, RuleIsRegex: true},
		})
	})
}

// TestGuard_EveryNamedRuleInThePayloadSaysWhatItPermits walks the REAL registry and fails if a
// parameter names a rule the payload does not explain: the synthetic tests above prove the wiring for
// one declaration, this proves it reaches every declaration that has a rule to state, the claim an
// operator relies on. The floors keep it from passing by looking at nothing (rendering stopped, or a
// stub that registered no routes, would leave an empty loop and a green test).
func TestGuard_EveryNamedRuleInThePayloadSaysWhatItPermits(t *testing.T) {
	s := sharedRouteStub(t)
	eps := docEndpoints(s.registry)
	if len(eps) < 400 {
		t.Fatalf("docEndpoints rendered %d endpoints, want the declared set; this test would pass "+
			"by looking at almost nothing", len(eps))
	}

	// check is shared by parameters and by array elements: an element
	// carries the same two rule-bearing fields and the same obligation.
	var formats, patterns int
	check := func(where, format, pattern string, rule *handlers.APIRule) {
		named := format
		if named == "" {
			named = ruleByPattern[pattern]
			if named == "" {
				return // an uncatalogued pattern owes no prose
			}
			patterns++
		} else {
			formats++
		}
		if rule == nil {
			t.Errorf("%s names the rule %q and publishes no rule block: the docs state that a "+
				"rule applies and decline to say what it is", where, named)
			return
		}
		if rule.Name != named {
			t.Errorf("%s: rule.name = %q, want %q", where, rule.Name, named)
		}
		// `named` is derived from the SAME index production reads, so the check above proves self-consistency,
		// not attribution: corrupt ruleByPattern and both sides move together. The published regex is the
		// independent witness: a pattern is indexed BY its regex, so a correctly attributed rule publishes
		// the regex the parameter declares. Gated on format == "" because docRule resolves a Format FIRST
		// (then rule.Regex is correctly the format's); bothFields reports that case. This holds for every
		// catalogued pattern today (all RuleIsRegex) though nothing enforces it in general: orEmptyRules
		// panics on a non-regex base, but a hand-written RuleIsRegex-false pattern entry would publish no
		// regex and this check would not fire. The count is not written down: it rotted once (13 to 14).
		if format == "" && pattern != "" && rule.Regex != pattern {
			t.Errorf("%s: rule %q publishes the regex %s, but the parameter declares the pattern %s — "+
				"the pattern was attributed to the wrong catalogue entry",
				where, rule.Name, rule.Regex, pattern)
		}
		if rule.Permits == "" {
			t.Errorf("%s: rule %q publishes an empty permits line", where, named)
		}
	}

	// docRule asks Format first and a pattern second, which is only
	// unambiguous because no declaration carries both. apischema does NOT
	// forbid the pair — compileProperty validates the two independently —
	// so this pins the premise rather than assuming it. A parameter
	// carrying both would publish `format: X` and a rule block for X
	// beside a `pattern` that is some OTHER regex, and the page would
	// render the rule's name over the pattern's text.
	bothFields := func(where, format, pattern string) {
		if format != "" && pattern != "" {
			t.Errorf("%s declares both format %q and pattern %s. The docs can name one rule per "+
				"value: decide which one governs, or teach docRule to publish both.",
				where, format, pattern)
		}
	}

	for _, e := range eps {
		for _, p := range e.Parameters {
			at := e.Method + " " + e.Path + " [" + p.Name + "]"
			bothFields(at, p.Format, p.Pattern)
			check(at, p.Format, p.Pattern, p.Rule)
			if p.Items != nil {
				bothFields(at+"[]", p.Items.Format, p.Items.Pattern)
				check(at+"[]", p.Items.Format, p.Items.Pattern, p.Items.Rule)
			}
		}
	}

	// Both kinds must be represented, and in quantity. Checking only
	// formats would leave the pattern half — the half that needs the
	// reverse index, and so the half likelier to break — unexercised.
	if formats < 100 {
		t.Errorf("only %d format-bearing parameters were checked; the registry declares far more, "+
			"so the walk is missing them", formats)
	}
	if patterns < 50 {
		t.Errorf("only %d catalogued-pattern-bearing parameters were checked; the reverse index "+
			"is resolving almost nothing", patterns)
	}
}

// TestGuard_PublishedRuleFieldsAreTheReviewedSet holds the rule block to the three fields reviewed for
// publication. /api/v1/api-docs is served to any authenticated caller and this repo is public, so
// every field added to APIRule is a publication decision. The catalogue entry carries five more
// (the upstream file, the upstream rule verbatim, a divergence note, the Accepts/Rejects witnesses),
// left out on purpose: the first three are maintainer notes naming Go identifiers and repo paths, and
// the witnesses are free-form strings, the one place a real host, guest or storage name could be
// typed. A field-set check rather than a string scan, since a scan would have to name the tokens it
// forbids, writing the estate's real identifiers into a public repo to prove they are absent.
func TestGuard_PublishedRuleFieldsAreTheReviewedSet(t *testing.T) {
	want := []string{"Name", "Permits", "Regex"}

	rt := reflect.TypeOf(handlers.APIRule{})
	got := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		got = append(got, rt.Field(i).Name)
	}
	if !slices.Equal(got, want) {
		t.Errorf("handlers.APIRule publishes %v, want exactly %v.\n"+
			"Every field here is served to any authenticated caller. If this is a deliberate "+
			"addition, review it against the identifier rule in CLAUDE.md and update this list; "+
			"if it came from RuleDoc because the field was there, it should not be published — "+
			"the upstream citation and the divergence note are written for a maintainer, the "+
			"Accepts/Rejects witnesses are free-form strings, and Origin was published once and "+
			"withdrawn (see the note on handlers.APIRule).", got, want)
	}
}

// TestGuard_NoPublishedRuleCarriesAnAddressLiteral is the content half,
// written so that it names nothing.
//
// The three published fields are generic BY CONSTRUCTION — a regex, a
// sentence about a Proxmox validator — but "by construction" is an
// argument, not a check. A dotted quad is the one shape that has no
// business in any of them and is unambiguous to look for, so it stands in
// for "somebody pasted a real value into a prose line". A hit is not
// necessarily a leak; it is a line a human should read.
func TestGuard_NoPublishedRuleCarriesAnAddressLiteral(t *testing.T) {
	dottedQuad := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

	seen := 0
	for _, d := range apischema.Catalogue() {
		r := docRule(ruleFieldsFor(d))
		if r == nil {
			t.Errorf("catalogue entry %q renders no rule block, so nothing about it is checked", d.Name)
			continue
		}
		seen++
		for field, text := range map[string]string{
			"name": r.Name, "permits": r.Permits, "regex": r.Regex,
		} {
			if hit := dottedQuad.FindString(text); hit != "" {
				t.Errorf("catalogue rule %q publishes %q in its %s, which this payload serves to "+
					"every authenticated caller — check it is not a real address", d.Name, hit, field)
			}
		}
	}
	if seen != len(apischema.Catalogue()) {
		t.Fatalf("checked %d of %d catalogue entries; the rest rendered nothing and this test "+
			"would pass by looking at them", seen, len(apischema.Catalogue()))
	}
}

// nameCollisionRules are the catalogue entries whose Divergence says Proxmox registers a DIFFERENT
// rule under the same name: the one divergence shape `origin` cannot compress. For every other entry
// "origin: proxmox" carries the actionable half (Proxmox is the authority and may refuse what we
// accept), but a reader who knows PVE sees `format: "disk-size"` and applies its semantics (a bare
// number is BYTES, here GiB) unless the Permits line contradicts them. Both entries do (disk-size's
// "a bare number is already GiB", bwlimit's "in KiB/s"), and the guard below makes a THIRD collision
// a deliberate decision about its Permits line.
var nameCollisionRules = []string{"bwlimit", "disk-size"}

// TestGuard_NameCollisionRulesStayTheKnownTwo is the ratchet under the
// decision to leave Divergence out of the payload.
//
// It is deliberately a list rather than a check on the prose: "does this
// Permits line warn a Proxmox-literate reader off the wrong semantics" is
// a judgement, and the useful thing a test can do is force someone to make
// it. Adding a collision is expected to update this list in the same
// change, having read the new entry's Permits line.
func TestGuard_NameCollisionRulesStayTheKnownTwo(t *testing.T) {
	var got []string
	for _, d := range apischema.Catalogue() {
		if strings.Contains(d.Divergence, "NAME COLLISION") {
			got = append(got, d.Name)
		}
	}
	slices.Sort(got)
	if !slices.Equal(got, nameCollisionRules) {
		t.Errorf("catalogue rules colliding with a DIFFERENT Proxmox rule of the same name = %v, "+
			"want %v.\nThe docs payload publishes the rule's NAME and not its divergence, so a "+
			"collision is invisible to a caller unless the rule's Permits line states the "+
			"difference in caller-visible terms (disk-size ends \"a bare number is already GiB\"; "+
			"bwlimit says \"in KiB/s\"). Read the new entry's Permits line, decide whether it does, "+
			"then update this list.", got, nameCollisionRules)
	}
}

// ruleFieldsFor spells a catalogue entry the way a declaration would
// reach it, so the guards above render every entry through the real
// docRule rather than reading the catalogue directly.
func ruleFieldsFor(d apischema.RuleDoc) (format, pattern string) {
	if d.Kind == apischema.KindFormat {
		return d.Name, ""
	}
	return "", d.Rule
}
