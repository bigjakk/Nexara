package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// GetDocs had no handler-level test at all before this file: the only
// coverage was the drift guard in internal/api, which checks that
// endpointMeta's KEYS still match registered routes and never looks at
// what the endpoint renders. That is why the whole payload could ship
// with 333 blank descriptions and no request contract anywhere, with
// every test green.
//
// These tests drive the real Fiber route table, so the precedence they
// assert is the precedence an external consumer gets.

// f64 and intp are the pointer constructors the bound fields take. They
// exist so a bound of ZERO can be written inline: &0 is not legal Go, and
// a test that could only express non-zero bounds would not be able to
// pin the distinction these fields are pointers for.
func f64(v float64) *float64 { return &v }
func intp(v int) *int        { return &v }

// okHandler is a route body that is never called: GetDocs reads the route
// TABLE, not the routes.
func okHandler(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }

// docsFixture registers routes on a fresh app, wires a docs handler to
// it, and returns both. The docs endpoint itself is mounted outside
// /api/v1/ so it does not appear in its own output.
func docsFixture(t *testing.T, declared []APIEndpoint, routes ...[2]string) *fiber.App {
	t.Helper()
	app := fiber.New()
	for _, r := range routes {
		app.Add([]string{r[0]}, r[1], okHandler)
	}
	h := NewAPIDocsHandler()
	h.SetApp(app)
	h.SetDeclaredEndpoints(declared)
	app.Get("/docs", h.GetDocs)
	return app
}

// fetchDocs calls the docs endpoint and returns the decoded envelope.
func fetchDocs[T any](t *testing.T, app *fiber.App) ListResponse[T] {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/docs", nil))
	if err != nil {
		t.Fatalf("docs request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("docs status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	var out ListResponse[T]
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode docs: %v", err)
	}
	return out
}

// overlaidPath is a SYNTHETIC path, not a real route. It used to be a real
// one — first the disk-attach route, then, after that entry was removed,
// one of the 6 API Keys routes endpointMeta still carried an entry for —
// but as of the endpointMeta cleanup (internal/api/handlers/api_docs.go),
// NO real route is declared by the registry AND ALSO carries an
// endpointMeta entry any more: every entry left in endpointMeta is for one
// of the 17 legacy routes, which by definition have no declaration to
// prove precedence against. So this sub-test seeds a temporary entry onto
// this made-up key itself (see below) rather than depending on a
// production route that cleanup keeps making disappear out from under it.
const overlaidPath = "/api/v1/made-up/overlaid-thing"

// stillLegacyPath is a route neither this phase nor any registry migration
// has reached: POST/PUT .../alert-rules carry a JSON array of OBJECTS
// (`rules`), which apischema's Property.Items cannot describe (see
// TestAlertRuleWritesAreStillLegacy in internal/api). It stands in for
// "a route only endpointMeta documents" more durably than a route that
// merely hasn't been migrated YET.
const stillLegacyPath = "/api/v1/alert-rules"

func TestGetDocs_DeclarationBeatsEndpointMeta(t *testing.T) {
	overlaidKey := "DELETE " + overlaidPath
	if _, exists := endpointMeta[overlaidKey]; exists {
		t.Fatalf("endpointMeta already has a real entry for the synthetic key %q; "+
			"pick a different made-up path", overlaidKey)
	}
	// Seed the overlay this sub-test needs to prove "declared wins" against,
	// and remove it again once the test ends — endpointMeta is a package
	// var, so leaving this in would leak into every other test in the
	// package.
	endpointMeta[overlaidKey] = APIEndpoint{
		Description: "OVERLAY description", Permission: "overlay:perm", Group: "Overlay Group",
	}
	t.Cleanup(func() { delete(endpointMeta, overlaidKey) })

	declared := []APIEndpoint{{
		Method:      fiber.MethodDelete,
		Path:        overlaidPath,
		Description: "DECLARED description",
		Permission:  "declared:perm",
		Group:       "Declared Group",
		Parameters: []APIParameter{
			{Name: "id", Type: "string", Source: "path"},
		},
	}}

	app := docsFixture(t, declared,
		[2]string{fiber.MethodDelete, overlaidPath},
		// A legacy route: endpointMeta has it, the registry does not.
		[2]string{fiber.MethodPost, stillLegacyPath},
		// A route neither source knows about.
		[2]string{fiber.MethodGet, "/api/v1/made-up/thing"},
	)

	byKey := make(map[string]APIEndpoint)
	for _, ep := range fetchDocs[APIEndpoint](t, app).Items {
		byKey[ep.Method+" "+ep.Path] = ep
	}

	tests := []struct {
		name       string
		key        string
		wantDesc   string
		wantPerm   string
		wantGroup  string
		wantParams int
	}{
		{
			name: "declared route renders its declaration, not the overlay",
			key:  overlaidKey,
			// The overlay seeded above says "OVERLAY description" /
			// overlay:perm / "Overlay Group"; none of it may appear.
			wantDesc:   "DECLARED description",
			wantPerm:   "declared:perm",
			wantGroup:  "Declared Group",
			wantParams: 1,
		},
		{
			name: "legacy route still renders from endpointMeta",
			key:  "POST " + stillLegacyPath,
			// Spelled out rather than read back out of endpointMeta: a
			// lookup that returned the zero value would compare "" to ""
			// and assert nothing at all, which is how this sub-test would
			// quietly stop covering the legacy path.
			wantDesc:   "Create an alert rule",
			wantPerm:   "manage:alert",
			wantGroup:  "Alerts",
			wantParams: 0,
		},
		{
			name:       "route in neither source gets a derived group and nothing else",
			key:        "GET /api/v1/made-up/thing",
			wantDesc:   "",
			wantPerm:   "",
			wantGroup:  "Made Up",
			wantParams: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ep, ok := byKey[tc.key]
			if !ok {
				t.Fatalf("%q missing from the docs payload", tc.key)
			}
			if ep.Description != tc.wantDesc {
				t.Errorf("description = %q, want %q", ep.Description, tc.wantDesc)
			}
			if ep.Permission != tc.wantPerm {
				t.Errorf("permission = %q, want %q", ep.Permission, tc.wantPerm)
			}
			if ep.Group != tc.wantGroup {
				t.Errorf("group = %q, want %q", ep.Group, tc.wantGroup)
			}
			if len(ep.Parameters) != tc.wantParams {
				t.Errorf("parameters = %d, want %d (%+v)", len(ep.Parameters), tc.wantParams, ep.Parameters)
			}
		})
	}
}

// TestGetDocs_ParameterContract is the test that would have saved the
// boot disk. It asserts on the raw JSON rather than on the decoded
// struct, because the distinction it guards — optional WITH a default
// versus optional WITHOUT one — is carried by the PRESENCE of the
// `default` key. Decoding into a struct turns both into a nil `any` and
// the assertion becomes vacuous.
func TestGetDocs_ParameterContract(t *testing.T) {
	const path = "/api/v1/probe/:id"

	app := docsFixture(t, []APIEndpoint{{
		Method: fiber.MethodPost, Path: path,
		Description: "probe", Permission: "manage:probe", Group: "Probe",
		Parameters: []APIParameter{
			{Name: "id", Type: "string", Source: "path", Format: "uuid", Typetext: "<uuid>",
				Rule: &APIRule{
					Name:    "uuid",
					Permits: "a canonical 8-4-4-4-12 hexadecimal UUID in either case, normalized to lowercase.",
					Regex:   `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
				}},
			{Name: "verbose", Type: "boolean", Source: "query", Optional: true, Default: false},
			{Name: "bus", Type: "string", Source: "body", Enum: []string{"scsi", "sata"}},
			{Name: "index", Type: "integer", Source: "body", Optional: true,
				Description: "Omit to take the lowest free slot."},
			{Name: "retries", Type: "integer", Source: "body", Optional: true, Default: 0},
			// A rule that validates by parsing rather than by matching: it
			// has no regex to publish, and the KEY must be absent rather
			// than present-and-empty. A consumer reads presence as "you may
			// compile this".
			{Name: "size", Type: "string", Source: "body", Optional: true, Format: "disk-size",
				Rule: &APIRule{
					Name:    "disk-size",
					Permits: "a decimal size with an optional binary unit, normalized to a whole GiB count.",
				}},
			{Name: "format", Type: "string", Source: "body", Optional: true, Requires: []string{"bus"}},
			// The bounds. `floor` carries a ZERO minimum and `blank` a
			// ZERO max_length: both are real constraints and both marshal
			// through a pointer, which is the only reason omitempty does
			// not eat them.
			{Name: "floor", Type: "integer", Source: "body", Optional: true, Minimum: f64(0)},
			{Name: "blank", Type: "string", Source: "body", Optional: true, MaxLength: intp(0)},
			{
				Name: "snap_name", Type: "string", Source: "body",
				Pattern: `^[A-Za-z][A-Za-z0-9_-]*$`, MinLength: intp(2), MaxLength: intp(40),
			},
			{Name: "newname", Type: "string", Source: "body", Optional: true, Alias: "oldname"},
			{
				Name: "tags", Type: "array", Source: "body", Optional: true,
				Items: &APIItems{Type: "string", Enum: []string{"red", "green"}},
			},
		},
	}}, [2]string{fiber.MethodPost, path})

	type rawEndpoint struct {
		Path       string                       `json:"path"`
		Parameters []map[string]json.RawMessage `json:"parameters"`
	}
	var probe *rawEndpoint
	for _, ep := range fetchDocs[rawEndpoint](t, app).Items {
		if ep.Path == path {
			probe = &ep
			break
		}
	}
	if probe == nil {
		t.Fatalf("%q missing from the docs payload", path)
	}

	byName := make(map[string]map[string]json.RawMessage, len(probe.Parameters))
	for _, p := range probe.Parameters {
		var name string
		if err := json.Unmarshal(p["name"], &name); err != nil {
			t.Fatalf("parameter name: %v", err)
		}
		byName[name] = p
	}

	tests := []struct {
		name         string
		param        string
		wantOptional string // the raw `optional` value
		wantDefault  string // "" means the key must be ABSENT
		wantSource   string
		wantExtra    map[string]any // other keys, compared after decoding
		wantAbsent   []string       // keys that must NOT appear at all
	}{
		{
			name: "required path parameter carries no default",
			// A required parameter cannot have one: apischema refuses the
			// declaration outright (checkDeclaration in validate.go).
			param: "id", wantOptional: "false", wantDefault: "", wantSource: `"path"`,
			wantExtra: map[string]any{"format": "uuid", "typetext": "<uuid>"},
		},
		{
			name:  "optional with NO default omits the key entirely",
			param: "index", wantOptional: "true", wantDefault: "", wantSource: `"body"`,
			wantExtra: map[string]any{"description": "Omit to take the lowest free slot."},
		},
		{
			name: "optional with a FALSE default still renders it",
			// The case omitempty gets wrong if `default` is ever typed as
			// anything narrower than `any`: a bool false would vanish and
			// read as "no default".
			param: "verbose", wantOptional: "true", wantDefault: "false", wantSource: `"query"`,
		},
		{
			name:  "optional with a ZERO default still renders it",
			param: "retries", wantOptional: "true", wantDefault: "0", wantSource: `"body"`,
		},
		{
			name:  "enum survives",
			param: "bus", wantOptional: "false", wantDefault: "", wantSource: `"body"`,
			wantExtra: map[string]any{"enum": []any{"scsi", "sata"}},
		},
		{
			name:  "requires survives",
			param: "format", wantOptional: "true", wantDefault: "", wantSource: `"body"`,
			wantExtra: map[string]any{"requires": []any{"bus"}},
		},
		{
			// The bound equivalent of the default case above: a floor of 0
			// is a rule a caller must satisfy, and it must not be
			// indistinguishable from having no floor.
			name:  "a ZERO minimum renders rather than vanishing",
			param: "floor", wantOptional: "true", wantDefault: "", wantSource: `"body"`,
			wantExtra:  map[string]any{"minimum": float64(0)},
			wantAbsent: []string{"maximum", "min_length", "max_length", "pattern"},
		},
		{
			name:  "a ZERO max_length renders rather than vanishing",
			param: "blank", wantOptional: "true", wantDefault: "", wantSource: `"body"`,
			wantExtra:  map[string]any{"max_length": float64(0)},
			wantAbsent: []string{"minimum", "maximum", "min_length"},
		},
		{
			name:  "pattern and a length range survive",
			param: "snap_name", wantOptional: "false", wantDefault: "", wantSource: `"body"`,
			wantExtra: map[string]any{
				"pattern":    `^[A-Za-z][A-Za-z0-9_-]*$`,
				"min_length": float64(2),
				"max_length": float64(40),
			},
		},
		{
			name: "a format publishes the rule it names",
			// The payoff of the rule catalogue: `format: "uuid"` states
			// that a rule applies, and the rule block states what it is.
			// Before it, this payload named a rule and left a caller to go
			// and read the server's source for what it permits — and an
			// external consumer could not do even that.
			param: "id", wantOptional: "false", wantDefault: "", wantSource: `"path"`,
			wantExtra: map[string]any{
				"format": "uuid",
				"rule": map[string]any{
					"name":    "uuid",
					"permits": "a canonical 8-4-4-4-12 hexadecimal UUID in either case, normalized to lowercase.",
					"regex":   `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
				},
			},
		},
		{
			name: "a rule with no regex omits the key rather than blanking it",
			// The absent-versus-empty rule the bounds already follow, on
			// the one field a consumer is meant to compile: a `regex` key
			// that is present and empty reads as "matches nothing".
			param: "size", wantOptional: "true", wantDefault: "", wantSource: `"body"`,
			wantExtra: map[string]any{
				"rule": map[string]any{
					"name":    "disk-size",
					"permits": "a decimal size with an optional binary unit, normalized to a whole GiB count.",
				},
			},
		},
		{
			name:  "an alias survives",
			param: "newname", wantOptional: "true", wantDefault: "", wantSource: `"body"`,
			wantExtra: map[string]any{"alias": "oldname"},
		},
		{
			name:  "an array carries its element schema",
			param: "tags", wantOptional: "true", wantDefault: "", wantSource: `"body"`,
			wantExtra: map[string]any{
				"items": map[string]any{"type": "string", "enum": []any{"red", "green"}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, ok := byName[tc.param]
			if !ok {
				t.Fatalf("parameter %q missing", tc.param)
			}
			if got := string(p["optional"]); got != tc.wantOptional {
				t.Errorf("optional = %s, want %s", got, tc.wantOptional)
			}
			if got := string(p["source"]); got != tc.wantSource {
				t.Errorf("source = %s, want %s", got, tc.wantSource)
			}
			raw, present := p["default"]
			switch {
			case tc.wantDefault == "" && present:
				t.Errorf("default key is present (%s) but this parameter declares none — "+
					"a caller cannot tell it apart from one that defaults to the zero value", raw)
			case tc.wantDefault != "" && !present:
				t.Errorf("default key is absent, want %s", tc.wantDefault)
			case tc.wantDefault != "" && string(raw) != tc.wantDefault:
				t.Errorf("default = %s, want %s", raw, tc.wantDefault)
			}
			for key, want := range tc.wantExtra {
				raw, present := p[key]
				if !present {
					t.Errorf("%s is absent, want %v", key, want)
					continue
				}
				var got any
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatalf("%s: %v", key, err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %#v, want %#v", key, got, want)
				}
			}
			for _, key := range tc.wantAbsent {
				if raw, present := p[key]; present {
					t.Errorf("%s is present (%s) but this parameter declares none — "+
						"an absent bound must not be indistinguishable from a zero one", key, raw)
				}
			}
		})
	}

	t.Run("every source appears", func(t *testing.T) {
		seen := map[string]bool{}
		for _, p := range probe.Parameters {
			seen[string(p["source"])] = true
		}
		for _, want := range []string{`"path"`, `"query"`, `"body"`} {
			if !seen[want] {
				t.Errorf("no parameter rendered source %s; a source the docs never show is "+
					"a source a caller cannot discover", want)
			}
		}
	})

	t.Run("empty strings are omitted, not rendered as blanks", func(t *testing.T) {
		// Presence first: byName["index"] on a missing key is a nil map,
		// against which every `_, present :=` below answers false and the
		// whole subtest passes having asserted nothing.
		index, ok := byName["index"]
		if !ok {
			t.Fatal("the index parameter left the fixture; this subtest would pass vacuously")
		}
		// `index` declares no format, typetext, enum, requires or rule.
		// `rule` belongs here rather than only in the positive cases: an
		// integer parameter naming no rule must not carry an empty rule
		// object, which would read as "a rule applies and permits nothing".
		for _, key := range []string{"format", "typetext", "enum", "requires", "rule"} {
			if _, present := index[key]; present {
				t.Errorf("index carries an empty %q key; omitempty should have dropped it", key)
			}
		}
	})
}

func TestGetDocs_EnvelopeAndRouteFiltering(t *testing.T) {
	app := docsFixture(t, nil,
		[2]string{fiber.MethodGet, "/api/v1/clusters"},
		[2]string{fiber.MethodPost, "/api/v1/clusters"},
		// Group(...) + .Post("/") shapes register a trailing slash; the
		// docs normalise it away so curated keys stay readable.
		[2]string{fiber.MethodDelete, "/api/v1/api-keys/"},
		// Outside /api/v1/ — not an API endpoint.
		[2]string{fiber.MethodGet, "/healthz"},
	)

	got := fetchDocs[APIEndpoint](t, app)
	keys := map[string]bool{}
	for _, ep := range got.Items {
		keys[ep.Method+" "+ep.Path] = true
	}

	// RespondItems' envelope. api-client.ts throws on a bare array and
	// TestGuard_ListEndpointsUseEnvelope forbids one.
	if got.Total != int64(len(got.Items)) {
		t.Errorf("total = %d, want %d", got.Total, len(got.Items))
	}
	for _, want := range []string{"GET /api/v1/clusters", "POST /api/v1/clusters", "DELETE /api/v1/api-keys"} {
		if !keys[want] {
			t.Errorf("%q missing from the payload (got %v)", want, keys)
		}
	}
	if keys["DELETE /api/v1/api-keys/"] {
		t.Error("trailing slash was not normalised away")
	}
	for _, unwanted := range []string{"GET /healthz", "HEAD /api/v1/clusters", "OPTIONS /api/v1/clusters"} {
		if keys[unwanted] {
			t.Errorf("%q should not be documented", unwanted)
		}
	}
}

// TestGetDocs_WithoutAppFailsLoudly pins the existing behaviour: an
// unwired handler 500s rather than serving an empty catalog that reads
// as "this server has no endpoints".
func TestGetDocs_WithoutAppFailsLoudly(t *testing.T) {
	app := fiber.New()
	app.Get("/docs", NewAPIDocsHandler().GetDocs)

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/docs", nil))
	if err != nil {
		t.Fatalf("docs request: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

func TestDeclaredEndpointKeys(t *testing.T) {
	h := NewAPIDocsHandler()
	if got := h.DeclaredEndpointKeys(); len(got) != 0 {
		t.Errorf("unwired handler declares %v, want none", got)
	}
	h.SetDeclaredEndpoints([]APIEndpoint{
		{Method: fiber.MethodPost, Path: "/api/v1/z"},
		{Method: fiber.MethodGet, Path: "/api/v1/a"},
	})
	got := h.DeclaredEndpointKeys()
	want := []string{"GET /api/v1/a", "POST /api/v1/z"}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("keys[%d] = %q, want %q (sorted order)", i, got[i], want[i])
		}
	}
}
