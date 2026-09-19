package api

import (
	"cmp"
	"fmt"
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
			Rule:      docRule(prop.Format, prop.Pattern),
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
		Rule:        docRule(item.Format, item.Pattern),
	}
}

// ruleByPattern maps a catalogued PATTERN's regex back to the rule's name.
//
// A parameter reaches a format BY NAME (Property.Format) and a pattern BY
// VALUE (Property.Pattern), so the two look up in opposite directions and
// the pattern direction needs an index. Building it here rather than
// asking apischema for one keeps the catalogue's exported surface to the
// two functions the declarations use.
//
// FORMATS are deliberately excluded, and the exclusion is load-bearing
// rather than tidy. A format validates AND NORMALIZES, and a value that
// merely matches a format's regex has not been through its normalization
// — so attributing a bare Pattern to a format would document a rewrite the
// route does not perform.
//
// It is not enough to say the registry refuses that shape. apischema.Rule
// panics when asked for a format, but only for THAT spelling: a regex
// pasted out in full reaches Property.Pattern with nothing to stop it, and
// one does — accessRealmPattern in registry_access.go is
// `^[A-Za-z][A-Za-z0-9._-]*$`, byte-identical to the storage-id format's
// rule. Were formats in this index, every realm parameter would publish
// itself as storage-id and promise a normalization it never receives.
var ruleByPattern = buildRuleByPattern(apischema.Catalogue())

// buildRuleByPattern indexes the catalogue's pattern rules by regex.
//
// It panics on two names sharing one regex, at init, for the reason
// buildCatalogue panics on a duplicated name: with two candidates the
// lookup would pick one by map order and the docs would attribute the
// parameter to whichever the runtime happened to hash first — a silent
// wrong answer rather than a loud one. It takes the catalogue as an
// argument so a test can hand it that collision.
func buildRuleByPattern(all []apischema.RuleDoc) map[string]string {
	out := make(map[string]string, len(all))
	for _, d := range all {
		if d.Kind != apischema.KindPattern {
			continue
		}
		if prev, dup := out[d.Rule]; dup {
			panic(fmt.Sprintf("api: catalogue rules %q and %q share the regex %s, so a declared "+
				"Pattern cannot be attributed to either", prev, d.Name, d.Rule))
		}
		out[d.Rule] = d.Name
	}
	return out
}

// docRule renders the catalogued rule behind a parameter's format or
// pattern — the payload's answer to "the docs name a rule; what is it?".
//
// Format is asked first because it is the stronger claim: it names the
// rule outright, and no declaration carries both (a format already
// implies its own shape check, so a second Pattern beside it would be a
// second, unrelated rule on one value). A pattern nobody catalogued
// renders nothing, which is the same honest gap an unmigrated route's
// empty Parameters is: the rule is still published as a regex, and the
// missing prose is what marks it as not yet promoted to the catalogue.
func docRule(format, pattern string) *handlers.APIRule {
	name := format
	if name == "" {
		name = ruleByPattern[pattern]
	}
	if name == "" {
		return nil
	}
	d, ok := apischema.LookupRule(name)
	if !ok {
		// Unreachable for a format, for two reasons that stack: a
		// declaration naming an unregistered format is refused at
		// registration (compileProperty), and RegisterFormat itself panics
		// for a name the catalogue does not carry — pinned by
		// TestRegisterFormatRequiresACatalogueEntry in
		// apischema/format_test.go. (Not by
		// TestEveryCataloguedFormatIsRegistered, which checks the opposite
		// direction: that every CATALOGUE entry has a registered format.)
		// Rendering nothing rather than panicking keeps a catalogue gap
		// from taking the whole docs payload down with it.
		return nil
	}
	// Origin is NOT copied across. See the note on handlers.APIRule for
	// why the catalogue keeps it and the payload does not.
	r := &handlers.APIRule{
		Name:    d.Name,
		Permits: d.Permits,
	}
	// Only a rule whose regex IS the whole shape check publishes one. See
	// the note on handlers.APIRule.Regex: a part-prose rule has no
	// compilable form, and handing a consumer one that only looks
	// compilable is worse than handing them none.
	if d.RuleIsRegex {
		r.Regex = d.Rule
	}
	return r
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
