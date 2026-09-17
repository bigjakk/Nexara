package api

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// noopHandler is a stand-in for a real handler in declaration tests: none
// of them issues a request, so the body never runs.
func noopHandler(_ fiber.Ctx, _ *apischema.Params) error { return nil }

// validEndpoint is the shape every case in this file starts from, so that
// each one differs from a working declaration in exactly one way.
func validEndpoint() Endpoint {
	return Endpoint{
		Method:      fiber.MethodGet,
		Path:        "/api/v1/clusters/:cluster_id/widgets",
		Description: "List the widgets of a cluster.",
		Group:       "Widgets",
		Permissions: Permissions{Check: &Check{Action: "view", Resource: "widget", Scope: ScopeCluster}},
		Parameters: apischema.Properties{
			"cluster_id": apischema.StdOption("cluster-id"),
			"limit":      {Type: apischema.Integer, Optional: true, Default: 50, Minimum: apischema.Ptr(1.0), Maximum: apischema.Ptr(500.0)},
		},
		Handler: noopHandler,
	}
}

// registerPanic registers e and returns the panic message, or "" if the
// declaration was accepted. It drives the exported Register rather than
// the internal register, because panicking IS the contract: an error
// value a caller could ignore is how a malformed declaration reaches a
// request in the first place.
func registerPanic(t *testing.T, e Endpoint) (msg string) {
	t.Helper()
	reg := NewRegistry()
	defer func() {
		if r := recover(); r != nil {
			msg = fmt.Sprint(r)
		}
	}()
	reg.Register(e)
	return ""
}

func TestRegisterAcceptsAWellFormedEndpoint(t *testing.T) {
	if msg := registerPanic(t, validEndpoint()); msg != "" {
		t.Fatalf("Register panicked on a valid endpoint: %s", msg)
	}
}

func TestRegisterRejectsMalformedDeclarations(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Endpoint)
		want string
	}{
		{
			name: "unknown method",
			edit: func(e *Endpoint) { e.Method = "FETCH" },
			want: "not a known HTTP verb",
		},
		{
			name: "lower-case method",
			edit: func(e *Endpoint) { e.Method = "get" },
			want: "not a known HTTP verb",
		},
		{
			name: "path outside the API prefix",
			edit: func(e *Endpoint) { e.Path = "/internal/widgets" },
			want: "does not start with /api/v1/",
		},
		{
			name: "no description",
			edit: func(e *Endpoint) { e.Description = "  " },
			want: "has no Description",
		},
		{
			name: "no group",
			edit: func(e *Endpoint) { e.Group = "" },
			want: "has no Group",
		},
		{
			name: "no handler",
			edit: func(e *Endpoint) { e.Handler = nil },
			want: "has no Handler",
		},
		{
			// Params panics on an undeclared key, so this route would 500
			// on its first request rather than fail at boot.
			name: "path parameter missing from the schema",
			edit: func(e *Endpoint) { e.Path = "/api/v1/clusters/:cluster_id/widgets/:widget_id" },
			want: `path parameter "widget_id" with no matching entry in Parameters`,
		},
		{
			// The quieter direction: nothing ever supplies it, so a
			// required one rejects everybody and an optional one carries
			// its default forever.
			name: "path-sourced parameter missing from the path",
			edit: func(e *Endpoint) {
				e.Parameters["node"] = apischema.Property{Type: apischema.String, Source: apischema.SourcePath}
			},
			want: `declares parameter "node" as a path parameter, but the path has no :node`,
		},
		{
			name: "no permissions declared",
			edit: func(e *Endpoint) { e.Permissions = Permissions{} },
			want: "declares no Permissions field",
		},
		{
			name: "two permissions fields",
			edit: func(e *Endpoint) {
				e.Permissions.Deferred = "the handler picks the resource from the body"
			},
			want: "declares 2 Permissions fields (Check, Deferred)",
		},
		{
			name: "deferred with a blank reason",
			edit: func(e *Endpoint) { e.Permissions = Permissions{Deferred: "   "} },
			want: "Deferred has a blank reason",
		},
		{
			name: "public with a blank reason",
			edit: func(e *Endpoint) { e.Permissions = Permissions{Public: " "} },
			want: "Public has a blank reason",
		},
		{
			name: "self-service with a blank reason",
			edit: func(e *Endpoint) { e.Permissions = Permissions{SelfService: " "} },
			want: "SelfService has a blank reason",
		},
		{
			name: "advisory without a reason",
			edit: func(e *Endpoint) {
				e.Permissions = Permissions{Advisory: &AdvisoryCheck{
					Check: Check{Action: "view", Resource: "cluster", Scope: ScopeGlobal},
				}}
			},
			want: "Advisory has no Reason",
		},
		{
			name: "check without an action",
			edit: func(e *Endpoint) {
				e.Permissions = Permissions{Check: &Check{Resource: "widget", Scope: ScopeCluster}}
			},
			want: "Check has no Action",
		},
		{
			name: "check without a resource",
			edit: func(e *Endpoint) {
				e.Permissions = Permissions{Check: &Check{Action: "view", Scope: ScopeCluster}}
			},
			want: "Check has no Resource",
		},
		{
			// The zero ScopeKind must not quietly mean "global": a
			// declaration that forgot its scope would then enforce a
			// different permission than the one it looks like it
			// enforces, and nothing would say so.
			name: "check with no scope",
			edit: func(e *Endpoint) {
				e.Permissions = Permissions{Check: &Check{Action: "view", Resource: "widget"}}
			},
			want: `Check has scope ""`,
		},
		{
			// clusterIDFromParam answers 400 when the path names no
			// cluster, so this gate would reject every caller rather than
			// authorize any of them.
			name: "cluster-scoped check on a path with no cluster",
			edit: func(e *Endpoint) {
				e.Path = "/api/v1/widgets"
				delete(e.Parameters, "cluster_id")
			},
			want: "is cluster-scoped but the path does not name the cluster it acts on",
		},
		{
			// clusterIDFromParam falls back to :id, so this registers if
			// the guard only asks "is there an :id". At runtime it would
			// look up the GUEST's uuid as a cluster: a global grant
			// short-circuits and passes, every cluster-scoped operator is
			// refused, and the declaration reads as cluster enforcement
			// while enforcing nothing of the kind. It fails closed, which
			// is exactly why it would survive review.
			name: "cluster-scoped check on an :id that is not a cluster",
			edit: func(e *Endpoint) {
				e.Path = "/api/v1/vms/:id"
				delete(e.Parameters, "cluster_id")
				e.Parameters["id"] = apischema.Property{Type: apischema.String, Format: "uuid", Source: apischema.SourcePath}
			},
			want: "is cluster-scoped but the path does not name the cluster it acts on",
		},
		{
			// An unanchored substring test would accept this: the path
			// contains "/clusters/:id" but c.Params("id") returns the
			// GUEST uuid, because :id comes first.
			name: "an /clusters/:id that is not at the start of the path",
			edit: func(e *Endpoint) {
				e.Path = "/api/v1/vms/:id/clusters/:idx/attach"
				delete(e.Parameters, "cluster_id")
				e.Parameters["id"] = apischema.Property{Type: apischema.String, Format: "uuid", Source: apischema.SourcePath}
				e.Parameters["idx"] = apischema.Property{Type: apischema.String, Format: "uuid", Source: apischema.SourcePath}
			},
			want: "is cluster-scoped but the path does not name the cluster it acts on",
		},
		{
			// The cluster named here is the migration DESTINATION, so
			// gating on it would let a caller holding manage on the
			// destination move a guest out of a cluster they hold nothing
			// on. A cluster that is not the subject must be named
			// something else.
			name: "a :cluster_id that is the destination, not the subject",
			edit: func(e *Endpoint) {
				e.Path = "/api/v1/vms/:vm_id/migrate/:cluster_id"
				e.Parameters["vm_id"] = apischema.StdOption("vm-id")
			},
			want: "is cluster-scoped but the path does not name the cluster it acts on",
		},
		{
			// The other half of the same hole, and the one that escalates
			// rather than merely confuses: the path spells :id, so a guard
			// that only protects the spelled name leaves cluster_id free
			// to be declared in the body. The gate then reads cluster A
			// from the URL while the handler reads cluster B from the body.
			name: "the gate's other name declared in the body",
			edit: func(e *Endpoint) {
				e.Path = "/api/v1/clusters/:id/widgets"
				delete(e.Parameters, "cluster_id")
				e.Parameters["id"] = apischema.StdOption("cluster-id")
				e.Parameters["cluster_id"] = apischema.Property{
					Type: apischema.String, Format: "uuid", Optional: true, Source: apischema.SourceBody,
				}
			},
			want: `declares "cluster_id" as a body parameter, but the permission middleware`,
		},
		{
			// Fiber matches path parameters case-insensitively
			// (CaseSensitive is false), so c.Params("cluster_id") reads
			// :CLUSTER_ID — and a case-sensitive guard would not see it.
			name: "the gate's name in a different case",
			edit: func(e *Endpoint) {
				e.Parameters["CLUSTER_ID"] = apischema.Property{
					Type: apischema.String, Format: "uuid", Optional: true, Source: apischema.SourceQuery,
				}
			},
			want: `declares "CLUSTER_ID" as a query parameter, but the permission middleware`,
		},
		{
			// The alias would be read via c.Params(alias), which is always
			// empty, and checkMisplaced would then tell the caller to send
			// it "in the request path" — something a URL cannot express.
			name: "an alias on a path parameter",
			edit: func(e *Endpoint) {
				prop := e.Parameters["cluster_id"]
				prop.Alias = "cluster"
				e.Parameters["cluster_id"] = prop
			},
			want: `declares an alias "cluster" on the path parameter "cluster_id"`,
		},
		{
			// pathParamNames skips wildcards, so a wildcard segment is the
			// one part of a path that reaches a handler unvalidated — and
			// path traversal lives exactly there.
			name: "a greedy wildcard",
			edit: func(e *Endpoint) {
				e.Path = "/api/v1/clusters/:cluster_id/files/*"
			},
			want: `uses the wildcard "*"`,
		},
		{
			name: "a one-or-more wildcard",
			edit: func(e *Endpoint) {
				e.Path = "/api/v1/clusters/:cluster_id/files/+"
			},
			want: `uses the wildcard "+"`,
		},
		{
			// ResolveSource gives an explicit Source priority over the
			// path match, so extraction would read cluster_id out of the
			// query while clusterIDFromParam — and therefore the gate —
			// still read it from the path. The route would authorize
			// against one cluster and act on another.
			name: "a path parameter re-sourced to the query",
			edit: func(e *Endpoint) {
				prop := e.Parameters["cluster_id"]
				prop.Source = apischema.SourceQuery
				e.Parameters["cluster_id"] = prop
			},
			want: `declares parameter "cluster_id" with source "query", but :cluster_id is a path parameter`,
		},
		{
			name: "a single alternative is not an alternative",
			edit: func(e *Endpoint) {
				e.Permissions = Permissions{Alternatives: []Check{
					{Action: "view", Resource: "vm", Scope: ScopeCluster},
				}}
			},
			want: "declares a single Alternatives entry",
		},
		{
			name: "an alternative with no scope",
			edit: func(e *Endpoint) {
				e.Permissions = Permissions{Alternatives: []Check{
					{Action: "view", Resource: "vm", Scope: ScopeCluster},
					{Action: "view", Resource: "container"},
				}}
			},
			want: `Alternatives[1] has scope ""`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := validEndpoint()
			tc.edit(&e)
			msg := registerPanic(t, e)
			if msg == "" {
				t.Fatalf("Register accepted the declaration; want a panic containing %q", tc.want)
			}
			if !strings.Contains(msg, tc.want) {
				t.Errorf("panic = %q, want it to contain %q", msg, tc.want)
			}
		})
	}
}

// TestRegisterAcceptsAClusterIDUnderTheLegacyIDSpelling keeps the
// previous case from over-correcting: /api/v1/clusters/:id is the shape
// the legacy cluster routes use, and there :id really is a cluster.
func TestRegisterAcceptsAClusterIDUnderTheLegacyIDSpelling(t *testing.T) {
	e := validEndpoint()
	e.Path = "/api/v1/clusters/:id"
	delete(e.Parameters, "cluster_id")
	e.Parameters["id"] = apischema.StdOption("cluster-id")

	if msg := registerPanic(t, e); msg != "" {
		t.Fatalf("Register rejected a cluster route spelled with :id: %s", msg)
	}
}

// TestRegisterAcceptsAnAdvisoryOnAPathWithNoCluster keeps the
// cluster-scope guard off the one shape it must not touch. Advisory
// installs no middleware, so nothing calls clusterIDFromParam and the
// Scope is documentation. GET /api/v1/clusters — the accessibleClusters
// listing AdvisoryCheck's own doc names — has neither a :cluster_id nor
// a /clusters/:id, so requiring one would make the shape undeclarable on
// the endpoints it exists for, and push authors to write ScopeGlobal,
// which Describe() would then report as an instance-wide requirement
// that is not real.
func TestRegisterAcceptsAnAdvisoryOnAPathWithNoCluster(t *testing.T) {
	e := validEndpoint()
	e.Path = "/api/v1/clusters"
	delete(e.Parameters, "cluster_id")
	e.Permissions = Permissions{Advisory: &AdvisoryCheck{
		Check:  Check{Action: "view", Resource: "cluster", Scope: ScopeCluster},
		Reason: "accessibleClusters filters the listing to the caller's granted clusters",
	}}

	if msg := registerPanic(t, e); msg != "" {
		t.Fatalf("Register rejected an advisory listing endpoint: %s", msg)
	}
}

// TestRegisterStillChecksAdvisoryVocabulary is the other side of that
// exemption: skipping the PATH requirement must not skip the rest.
func TestRegisterStillChecksAdvisoryVocabulary(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check Check
		want  string
	}{
		{"no action", Check{Resource: "cluster", Scope: ScopeGlobal}, "Advisory has no Action"},
		{"no resource", Check{Action: "view", Scope: ScopeGlobal}, "Advisory has no Resource"},
		{"no scope", Check{Action: "view", Resource: "cluster"}, `Advisory has scope ""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := validEndpoint()
			e.Path = "/api/v1/clusters"
			delete(e.Parameters, "cluster_id")
			e.Permissions = Permissions{Advisory: &AdvisoryCheck{Check: tc.check, Reason: "filtered in the handler"}}

			msg := registerPanic(t, e)
			if msg == "" {
				t.Fatalf("Register accepted the declaration; want a panic containing %q", tc.want)
			}
			if !strings.Contains(msg, tc.want) {
				t.Errorf("panic = %q, want it to contain %q", msg, tc.want)
			}
		})
	}
}

func TestRegisterRejectsADuplicateRoute(t *testing.T) {
	reg := NewRegistry()
	reg.Register(validEndpoint())

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("Register accepted the same method+path twice")
		}
		if msg := fmt.Sprint(r); !strings.Contains(msg, "is already registered") {
			t.Errorf("panic = %q, want it to name the duplicate", msg)
		}
	}()
	reg.Register(validEndpoint())
}

// TestRegisterCompilesEverySchema is the reason Register exists in this
// shape. It mirrors apischema's TestCompileIsSufficientForStartup case for
// case: each of these is a declaration defect that Validate does NOT catch
// on its own, so without a Compile at registration each one reaches a
// request — as a 400 that blames the caller for our bug, or as a 500 on
// whichever request first happens to supply the parameter.
//
// If this test ever goes quiet, check that Register still calls
// Parameters.Compile before it accepts the endpoint.
func TestRegisterCompilesEverySchema(t *testing.T) {
	cases := []struct {
		name  string
		props apischema.Properties
		want  string
	}{
		{
			name:  "an enum entry its own pattern rejects",
			props: apischema.Properties{"v": {Type: apischema.String, Pattern: `^[a-z]+$`, Enum: []string{"ABC"}}},
			want:  `enum value "ABC" of parameter "v" is invalid`,
		},
		{
			name:  "an enum entry its own length bound rejects",
			props: apischema.Properties{"v": {Type: apischema.String, MaxLength: apischema.Ptr(2), Enum: []string{"abc"}}},
			want:  `enum value "abc" of parameter "v" is invalid`,
		},
		{
			name:  "an unregistered format on a parameter nobody sends",
			props: apischema.Properties{"v": {Type: apischema.String, Optional: true, Format: "no-such-format"}},
			want:  `unregistered format "no-such-format"`,
		},
		{
			name: "a nested array inside items",
			props: apischema.Properties{"v": {Type: apischema.Array, Items: &apischema.Property{
				Type: apischema.Array, Items: &apischema.Property{Type: apischema.String},
			}}},
			want: "only scalar element types are supported",
		},
		{
			name:  "an object element type",
			props: apischema.Properties{"v": {Type: apischema.Array, Items: &apischema.Property{Type: apischema.Object}}},
			want:  "only scalar element types are supported",
		},
		{
			name:  "numeric bounds on a string",
			props: apischema.Properties{"v": {Type: apischema.String, Minimum: apischema.Ptr(1.0), Maximum: apischema.Ptr(10.0)}},
			want:  "declares numeric bounds but is string",
		},
		{
			name:  "a type that is not one of the six",
			props: apischema.Properties{"v": {Type: apischema.Type("str")}},
			want:  `unknown type "str"`,
		},
		// Beyond the seven: a contradictory enum, which Validate reports
		// as a 400 against the caller because checkEnumReachable only
		// guards the enum-miss branch.
		{
			name:  "an enum entry its own format would rewrite",
			props: apischema.Properties{"v": {Type: apischema.String, Format: "disk-size", Enum: []string{"1T"}}},
			want:  "could never match",
		},
		{
			name:  "a default that fails its own bounds",
			props: apischema.Properties{"v": {Type: apischema.Integer, Optional: true, Maximum: apischema.Ptr(10.0), Default: 99}},
			want:  `default for parameter "v" is invalid`,
		},
		{
			name:  "a non-finite bound, which would enforce nothing",
			props: apischema.Properties{"v": {Type: apischema.Number, Optional: true, Minimum: apischema.Ptr(math.NaN())}},
			want:  "non-finite minimum",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := validEndpoint()
			e.Path = "/api/v1/widgets"
			e.Permissions = Permissions{Check: &Check{Action: "view", Resource: "widget", Scope: ScopeGlobal}}
			e.Parameters = tc.props

			msg := registerPanic(t, e)
			if msg == "" {
				t.Fatalf("Register accepted a schema Compile rejects; want a panic containing %q", tc.want)
			}
			if !strings.Contains(msg, tc.want) {
				t.Errorf("panic = %q, want it to contain %q", msg, tc.want)
			}
		})
	}
}

func TestPathParamNames(t *testing.T) {
	cases := []struct {
		path string
		want []string
	}{
		{"/api/v1/widgets", nil},
		{"/api/v1/clusters/:cluster_id/widgets", []string{"cluster_id"}},
		{"/api/v1/clusters/:cluster_id/nodes/:node_name/disks/:id", []string{"cluster_id", "node_name", "id"}},
		// Fiber allows several params in one segment and suffixes on a
		// param, so the name is the identifier run after the colon.
		{"/api/v1/f/:a-:b", []string{"a", "b"}},
		{"/api/v1/f/:id<int>", []string{"id"}},
		{"/api/v1/f/:name?", []string{"name"}},
		// A wildcard is not a named parameter; Fiber calls it "*1" and no
		// schema declares it.
		{"/api/v1/files/*", nil},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			got := pathParamNames(tc.path)
			if len(got) != len(tc.want) {
				t.Fatalf("pathParamNames(%q) = %v, want %v", tc.path, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("pathParamNames(%q) = %v, want %v", tc.path, got, tc.want)
				}
			}
		})
	}
}

func TestPermissionsDescribe(t *testing.T) {
	cases := []struct {
		name  string
		perms Permissions
		want  string
	}{
		{"check", Permissions{Check: &Check{Action: "view", Resource: "vm", Scope: ScopeCluster}}, "view:vm"},
		{"alternatives", Permissions{Alternatives: []Check{
			{Action: "view", Resource: "vm", Scope: ScopeCluster},
			{Action: "view", Resource: "container", Scope: ScopeCluster},
		}}, "view:vm | view:container"},
		{"advisory", Permissions{Advisory: &AdvisoryCheck{
			Check:  Check{Action: "view", Resource: "cluster", Scope: ScopeGlobal},
			Reason: "accessibleClusters filters the listing",
		}}, "view:cluster (filtered)"},
		{"deferred", Permissions{Deferred: "resource depends on the upload's content type"}, "deferred"},
		{"public", Permissions{Public: "issues the session"}, "public"},
		{"self-service", Permissions{SelfService: "the caller's own profile"}, "self-service"},
		{"undeclared", Permissions{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.perms.Describe(); got != tc.want {
				t.Errorf("Describe() = %q, want %q", got, tc.want)
			}
		})
	}
}
