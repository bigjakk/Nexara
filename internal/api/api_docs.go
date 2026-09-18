package api

import (
	"cmp"
	"slices"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// This file is the seam between the endpoint registry and the API docs,
// and it points the dependency the only way it can point.
//
// GetDocs lives in package handlers; the registry lives here, in package
// api, which already imports handlers. handlers importing api back is an
// import cycle, which is why the migration left a route's prose written
// out twice — once in the declaration, once in handlers.endpointMeta —
// with a guard test holding the two copies together.
//
// So the payload TYPE is defined in handlers and the payload VALUE is
// built here, at startup, and pushed across (SetDeclaredEndpoints). The
// declaration becomes the single source of truth without handlers
// learning anything about Endpoint, Permissions or apischema.

// docEndpoints renders a registry's declarations into the docs payload.
//
// It is called once from setupRoutes rather than per request: the
// declarations are fixed at registration, and building them on the hot
// path would re-sort every parameter of every endpoint for each reader.
func docEndpoints(reg *Registry) []handlers.APIEndpoint {
	if reg == nil {
		return nil
	}
	eps := reg.Endpoints()
	out := make([]handlers.APIEndpoint, 0, len(eps))
	for _, e := range eps {
		out = append(out, handlers.APIEndpoint{
			Method:      e.Method,
			Path:        e.Path,
			Description: e.Description,
			// Describe() is what an operator building a role reads:
			// "manage:vm", "view:vm | view:container", or the word for a
			// shape that installs no gate at all ("deferred", "public",
			// "self-service").
			//
			// Those last words say nothing an operator can act on, so a
			// route whose permission cannot be stated here has to state it
			// in its Description instead — which is what endpointMeta did
			// for the two convert/clone-to-template routes, and what
			// TestGuard_DeclarationDropsNoDocumentedPermission now holds
			// every declaration to.
			Permission: e.Permissions.Describe(),
			Group:      e.Group,
			Parameters: docParameters(e),
		})
	}
	return out
}

// sourceRank orders parameters by where they go on the wire, so that a
// reader forming a request meets the URL's own segments first.
var sourceRank = map[apischema.Source]int{
	apischema.SourcePath:  0,
	apischema.SourceQuery: 1,
	apischema.SourceBody:  2,
}

// docParameters renders one endpoint's schema.
//
// Order is (source, required-before-optional, name) — deterministic,
// because the docs are read side by side with a previous version often
// enough that a map's iteration order would be noise, and grouped the
// way a caller assembles a request rather than the way Go happens to
// hash it.
//
// ResolveSource is called with the endpoint's cached pathParams rather
// than re-derived here, so the source shown is the one extraction will
// actually read from — the two cannot drift, because they are the same
// function over the same input.
func docParameters(e Endpoint) []handlers.APIParameter {
	if len(e.Parameters) == 0 {
		return nil
	}
	out := make([]handlers.APIParameter, 0, len(e.Parameters))
	for name, prop := range e.Parameters {
		out = append(out, handlers.APIParameter{
			Name:     name,
			Type:     string(prop.Type),
			Source:   string(apischema.ResolveSource(name, prop, e.Method, e.pathParams)),
			Optional: prop.Optional,
			// Copied as-is. A nil Default means the parameter has none,
			// and handlers.APIParameter omits the JSON key in that case —
			// which is the whole distinction, so do NOT substitute a zero
			// value here for a parameter that declined to name one.
			//
			// A composite default (a map, a slice) is ALIASED rather than
			// deep-copied — the one field below that is. That is safe only
			// because this payload is built once at startup and never
			// written to afterwards; apischema.Property.clone exists
			// precisely because a shared default map is a schema-corruption
			// vector, so deep-copy it here the moment anything mutates a
			// rendered parameter.
			Default:     prop.Default,
			Enum:        slices.Clone(prop.Enum),
			Format:      prop.Format,
			Typetext:    prop.Typetext,
			Description: prop.Description,
			// The bounds are cloned rather than aliased. They are the one
			// group where aliasing would be a live hazard rather than a
			// theoretical one: StdOption hands every route a deep copy
			// precisely so two routes cannot share a *float64, and the
			// payload must not quietly reintroduce the sharing.
			Pattern:   prop.Pattern,
			Minimum:   clonePtr(prop.Minimum),
			Maximum:   clonePtr(prop.Maximum),
			MinLength: clonePtr(prop.MinLength),
			MaxLength: clonePtr(prop.MaxLength),
			Alias:     prop.Alias,
			Items:     docItems(prop.Items),
			Requires:  slices.Clone(prop.Requires),
		})
	}
	sortDocParameters(out)
	return out
}

// clonePtr copies a pointer-valued bound so the payload never aliases the
// schema's own. nil stays nil, which is what carries "no bound" — see the
// note on the bounds in handlers.APIParameter.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	return apischema.Ptr(*p)
}

// docItems renders an array parameter's element schema.
//
// It carries only the facets apischema's compileItems permits on an
// element: scalar type, and the rules that shape a value. An element
// cannot be optional, carry a default, name a source, declare an alias or
// require a companion — compileItems rejects all five — so there is
// nothing else to render and no recursion to worry about, since an
// element may not itself be an array.
func docItems(item *apischema.Property) *handlers.APIItems {
	if item == nil {
		return nil
	}
	return &handlers.APIItems{
		Type:        string(item.Type),
		Enum:        slices.Clone(item.Enum),
		Format:      item.Format,
		Pattern:     item.Pattern,
		Typetext:    item.Typetext,
		Description: item.Description,
		Minimum:     clonePtr(item.Minimum),
		Maximum:     clonePtr(item.Maximum),
		MinLength:   clonePtr(item.MinLength),
		MaxLength:   clonePtr(item.MaxLength),
	}
}

// sortDocParameters puts the rendered parameters in the reading order
// described on docParameters.
func sortDocParameters(out []handlers.APIParameter) {
	slices.SortFunc(out, func(a, b handlers.APIParameter) int {
		if c := cmp.Compare(sourceRank[apischema.Source(a.Source)], sourceRank[apischema.Source(b.Source)]); c != 0 {
			return c
		}
		// Required first: a caller reading top-down meets everything they
		// must send before anything they may.
		if a.Optional != b.Optional {
			if a.Optional {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.Name, b.Name)
	})
}
