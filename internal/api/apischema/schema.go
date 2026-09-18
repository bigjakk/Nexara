// Package apischema declares REST parameter schemas the way Proxmox VE
// does, and validates requests against them.
//
// PVE's PVE::RESTHandler::register_method() declares a JSON Schema for a
// route's parameters inline with the route itself, and that single
// declaration is what validates incoming requests, renders the API viewer
// and generates the CLI parsers. Nexara adopts the same model, and
// deliberately borrows PVE's vocabulary rather than inventing one:
// properties are "optional" rather than "required", string values carry a
// named "format" that both validates and normalizes them, reusable
// property definitions are "standard options", and unknown parameters are
// rejected (PVE's additionalProperties => 0 default).
//
// The engine operates on a plain map[string]any so that it is unit
// testable with no HTTP machinery: this package must not import Fiber.
// Extracting the map out of a request (path params, query string, body)
// happens one layer up.
//
// # Why this exists
//
// The layer this replaces bound request bodies to ad-hoc structs and
// checked them with hand-rolled `if req.X == ""` tests. That cannot
// express the difference between "the caller sent 0" and "the caller sent
// nothing", because Go's zero value is the same in both cases — a disk
// attach whose index was omitted silently became index 0, i.e. the boot
// disk. [Params] keeps those two states apart: the plain accessors return
// a value (the declared default, or the zero value), and the Opt
// accessors additionally report whether the caller actually supplied it.
//
// # Panics are for programming errors
//
// Reading a parameter that the schema does not declare, or reading it
// with an accessor for the wrong type, panics. Both are mistakes in the
// handler's own source that no request can cause and no runtime condition
// can fix: the schema is a compile-time-ish constant that sits a few lines
// above the accessor call. Panicking makes the mismatch impossible to
// miss in development and in tests, where a silent zero value would
// reintroduce exactly the bug this package exists to prevent. Everything
// that a caller can get wrong is returned as a *ValidationError instead.
//
// For the same reason, declare a route's schema as a package-level var
// and call [Properties.Compile] on it at startup. StdOption panics on an
// unknown name and Compile reports a malformed declaration: both are
// worth having at process start, and a schema built inside a handler body
// turns them into a per-request failure instead. The one ordering caveat
// is noted on [StdOption]: a var initializer runs before its own
// package's init, so a package cannot read a standard option it registers
// itself that way.
package apischema

import (
	"slices"
	"strings"
)

// Type is the JSON Schema type of a parameter.
type Type string

// The parameter types this engine understands. Object values are carried
// through unvalidated: there is no nested-properties field yet, so an
// Object parameter only asserts "this is a JSON object".
const (
	String  Type = "string"
	Integer Type = "integer"
	Number  Type = "number"
	Boolean Type = "boolean"
	Array   Type = "array"
	Object  Type = "object"
)

// Source says where a parameter is read from. Empty means "infer": a name
// matching a :param in the route path is a path param; otherwise body for
// mutating methods, query for GET/DELETE. See [ResolveSource].
type Source string

// Where a parameter's value comes from.
const (
	SourceAuto  Source = ""
	SourcePath  Source = "path"
	SourceQuery Source = "query"
	SourceBody  Source = "body"
)

// Property describes one parameter. The field names follow PVE's
// JSONSchema keys so that a Perl declaration translates across by
// inspection.
type Property struct {
	Type        Type
	Description string
	Optional    bool // PVE uses `optional`, NOT `required` — match that
	Default     any
	Enum        []string
	// Pattern is a regex, compiled once at registration, that checks the
	// value's shape and never rewrites it. A regex more than one
	// declaration wants belongs in the rule catalogue (catalogue.go) and
	// is spelled Pattern: Rule("<name>") — see the note there on why a
	// second hand-written copy is the mistake it exists to stop.
	Pattern   string
	Minimum   *float64
	Maximum   *float64
	MinLength *int
	MaxLength *int
	// Format is a key into the format registry. A format validates AND
	// normalizes; what each one permits, and where its rule came from, is
	// in the catalogue (catalogue.go). Reach for Pattern instead when the
	// value must survive untouched.
	Format   string
	Typetext string // human-readable form for docs, e.g. "<number><G|T>"
	// Requires names parameters the CALLER must supply alongside this
	// one. A companion that merely carries its default does not satisfy
	// it, and Compile rejects a Requires naming a non-optional parameter,
	// which could never fail.
	Requires []string
	// Alias is a second name this parameter also answers to, for renaming
	// a parameter without breaking existing clients. Supplying both is an
	// error.
	Alias  string
	Source Source
	Items  *Property // element schema when Type == Array
}

// Properties is a route's parameter schema, keyed by parameter name.
type Properties map[string]Property

// Ptr returns a pointer to v. It exists so that the pointer-valued bounds
// can be written inline in a schema literal, where &1.0 is not legal Go:
//
//	"cores": {Type: apischema.Integer, Minimum: apischema.Ptr(1.0), Maximum: apischema.Ptr(128.0)}
func Ptr[T any](v T) *T { return &v }

// AsOptional returns a copy of p marked optional. It mirrors PVE's
// get_standard_option($name, { optional => 1 }): a standard option is
// required in one route's path and optional in another route's body, and
// Property is a value type, so the override is just a copy.
func (p Property) AsOptional() Property {
	c := p.clone()
	c.Optional = true
	return c
}

// clone deep-copies every field a caller could reach through and mutate,
// so that two schemas sharing a standard option cannot corrupt each
// other.
func (p Property) clone() Property {
	p.Default = deepCopyValue(p.Default)
	if p.Enum != nil {
		p.Enum = slices.Clone(p.Enum)
	}
	if p.Requires != nil {
		p.Requires = slices.Clone(p.Requires)
	}
	if p.Minimum != nil {
		p.Minimum = Ptr(*p.Minimum)
	}
	if p.Maximum != nil {
		p.Maximum = Ptr(*p.Maximum)
	}
	if p.MinLength != nil {
		p.MinLength = Ptr(*p.MinLength)
	}
	if p.MaxLength != nil {
		p.MaxLength = Ptr(*p.MaxLength)
	}
	if p.Items != nil {
		p.Items = Ptr(p.Items.clone())
	}
	return p
}

// deepCopyValue copies the composite shapes a Default can hold. A default
// is handed to a request, and an Object default is handed over as a map:
// without this, a handler that writes into the map it was given edits the
// SCHEMA — permanently, for every later request, and concurrently with
// every request in flight. Scalars are returned unchanged.
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopyValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	case []string:
		return slices.Clone(t)
	default:
		return v
	}
}

// ResolveSource returns the concrete source a parameter is read from.
// An explicit Property.Source wins; otherwise a name that appears as a
// :param in the route path is a path parameter, and everything else comes
// from the query string on read methods (GET, HEAD, DELETE, OPTIONS) or
// from the body on mutating ones.
//
// pathParams holds the route's placeholder names without the leading
// colon, e.g. []string{"cluster_id", "vm_id"} for
// /clusters/:cluster_id/vms/:vm_id.
func ResolveSource(name string, p Property, method string, pathParams []string) Source {
	if p.Source != SourceAuto {
		return p.Source
	}
	if slices.Contains(pathParams, name) {
		return SourcePath
	}
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "DELETE", "OPTIONS":
		return SourceQuery
	default:
		return SourceBody
	}
}
