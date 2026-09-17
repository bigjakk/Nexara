package apischema

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Params holds the values of one request after validation: coerced to the
// declared types, normalized by their formats, and with the declared
// defaults filled in.
//
// The three states a parameter can be in are all distinguishable, which
// is the whole point of the type:
//
//	declared with a Default, caller omitted it
//	    String/Int/… returns the default, Has is false, OptX returns (default, false)
//	declared Optional with no Default, caller omitted it
//	    String/Int/… returns the zero value, Has is false, OptX returns (zero, false)
//	caller supplied it
//	    String/Int/… returns the value, Has is true, OptX returns (value, true)
//
// The middle and last rows are what a hand-rolled `if req.Index == 0`
// check cannot tell apart.
//
// Reading an undeclared parameter, or reading one with the accessor for
// another type, panics — see the package comment.
type Params struct {
	props    Properties
	values   map[string]any
	supplied map[string]bool
}

// String returns the value of a string parameter.
func (p *Params) String(key string) string {
	p.mustProp("String", key, String)
	v, _ := p.values[key].(string)
	return v
}

// Int returns the value of an integer parameter.
func (p *Params) Int(key string) int64 {
	p.mustProp("Int", key, Integer)
	v, _ := p.values[key].(int64)
	return v
}

// Float returns the value of a number parameter. Integer parameters are
// accepted too: every integer within the declared bounds converts without
// loss, and refusing would make widening a parameter from integer to
// number a breaking change for its own handler.
func (p *Params) Float(key string) float64 {
	p.mustProp("Float", key, Number, Integer)
	switch v := p.values[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	default:
		return 0
	}
}

// Bool returns the value of a boolean parameter.
func (p *Params) Bool(key string) bool {
	p.mustProp("Bool", key, Boolean)
	v, _ := p.values[key].(bool)
	return v
}

// Strings returns the elements of an array parameter in their string
// form. The returned slice is freshly built on every call, so the caller
// may keep or modify it.
func (p *Params) Strings(key string) []string {
	p.mustProp("Strings", key, Array)
	elems, _ := p.values[key].([]any)
	if elems == nil {
		return nil
	}
	out := make([]string, 0, len(elems))
	for _, e := range elems {
		out = append(out, stringForm(e))
	}
	return out
}

// OptString returns a string parameter and whether the caller supplied
// it. supplied is false when the value came from the schema's default.
func (p *Params) OptString(key string) (string, bool) {
	p.mustProp("OptString", key, String)
	v, _ := p.values[key].(string)
	return v, p.supplied[key]
}

// OptInt returns an integer parameter and whether the caller supplied it.
func (p *Params) OptInt(key string) (int64, bool) {
	p.mustProp("OptInt", key, Integer)
	v, _ := p.values[key].(int64)
	return v, p.supplied[key]
}

// OptFloat returns a number parameter and whether the caller supplied it.
func (p *Params) OptFloat(key string) (float64, bool) {
	p.mustProp("OptFloat", key, Number, Integer)
	switch v := p.values[key].(type) {
	case float64:
		return v, p.supplied[key]
	case int64:
		return float64(v), p.supplied[key]
	default:
		return 0, p.supplied[key]
	}
}

// OptBool returns a boolean parameter and whether the caller supplied it.
func (p *Params) OptBool(key string) (value, supplied bool) {
	p.mustProp("OptBool", key, Boolean)
	v, _ := p.values[key].(bool)
	return v, p.supplied[key]
}

// Has reports whether the CALLER supplied key. A parameter that only got
// its declared default is not "had". Has panics on an undeclared key for
// the same reason the typed accessors do: silently answering false for a
// misspelled name is the failure this package exists to prevent.
func (p *Params) Has(key string) bool {
	if _, ok := p.props[key]; !ok {
		panic(fmt.Sprintf("apischema: Has(%q): parameter is not declared by this schema (declared: %s)", key, p.declared()))
	}
	return p.supplied[key]
}

// Raw returns a shallow copy of the validated values, defaults included.
// It is for debugging and for handlers that have to forward a whole
// parameter set verbatim; everything else should read through the typed
// accessors.
//
// It is NOT an audit payload. It contains every declared parameter,
// including any that carries a secret, and this project grants view:audit
// to every Viewer by default — so a handler that writes Raw() into an
// audit row publishes whatever the caller sent. Pick the fields to record
// by name. Only the top level is copied: a nested object value is shared
// with the params.
func (p *Params) Raw() map[string]any {
	return maps.Clone(p.values)
}

// mustProp resolves key and asserts its declared type, panicking on
// either kind of handler bug.
func (p *Params) mustProp(accessor, key string, want ...Type) {
	prop, ok := p.props[key]
	if !ok {
		panic(fmt.Sprintf("apischema: %s(%q): parameter is not declared by this schema (declared: %s)", accessor, key, p.declared()))
	}
	if slices.Contains(want, prop.Type) {
		return
	}
	names := make([]string, 0, len(want))
	for _, t := range want {
		names = append(names, string(t))
	}
	panic(fmt.Sprintf("apischema: %s(%q): parameter is declared as %s, not %s", accessor, key, prop.Type, strings.Join(names, " or ")))
}

// declared lists the schema's parameter names for a panic message.
func (p *Params) declared() string {
	if len(p.props) == 0 {
		return "none"
	}
	return strings.Join(slices.Sorted(maps.Keys(p.props)), ", ")
}
