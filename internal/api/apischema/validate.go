package apischema

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// ValidationError is a caller's mistake: something in the request does
// not satisfy the schema. Field is always populated, so the layer that
// turns this into an HTTP response can name the parameter.
//
// Errors that are NOT of this type are the schema's own mistakes
// (an unregistered format, an invalid default) and deserve a 500 rather
// than a 400 — they mean the declaration is wrong, not the request.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Field + ": " + e.Message }

// maxFieldRunes caps the parameter name a ValidationError echoes back.
//
// Field is usually one of the schema's own names, which are a handful of
// characters — but the unknown-parameter rejection puts the CALLER's key
// there, and that key's size is bounded only by the request body limit.
// Echoing a multi-megabyte key back would allocate and JSON-encode it for
// whoever sent it, which on a public endpoint is an unauthenticated
// amplification. No real parameter comes close to this, so the cap never
// touches a name anyone meant to write.
const maxFieldRunes = 64

func newErr(field, message string) *ValidationError {
	return &ValidationError{Field: truncateField(field), Message: message}
}

// truncateField cuts field to maxFieldRunes without materializing it as
// a []rune — the whole point is not to allocate a second copy of
// something that may be megabytes long.
func truncateField(field string) string {
	n := 0
	for i := range field {
		if n == maxFieldRunes {
			return field[:i] + "…"
		}
		n++
	}
	return field
}

// regexCache keeps compiled patterns keyed by their source, so a pattern
// is compiled once per process rather than once per request. Compile
// populates it at registration time; Validate falls back to filling it
// lazily so that an uncompiled schema still behaves correctly.
var regexCache sync.Map // string -> *regexp.Regexp

func patternRegexp(pattern string) (*regexp.Regexp, error) {
	if v, ok := regexCache.Load(pattern); ok {
		if re, ok := v.(*regexp.Regexp); ok {
			return re, nil
		}
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	regexCache.Store(pattern, re)
	return re, nil
}

// Validate checks in against props and returns the coerced,
// format-normalized, default-filled values.
//
// Per parameter the order is: presence (including its alias) -> type
// coercion -> format normalization -> enum -> pattern -> numeric bounds
// -> length bounds. Cross-field Requires runs afterwards, once every
// parameter has a value. Unknown keys are rejected outright, matching
// PVE's additionalProperties => 0 default.
//
// Only the first error is returned. Parameters are visited in name order
// so that a request with several problems always reports the same one,
// and unknown keys are reported before missing ones: when a caller
// misspells "size" as "siz", "siz is not a parameter" is the useful
// answer and "size is required" is a riddle.
//
// A defect in the schema itself fails CLOSED here rather than quietly
// weakening the check — a facet the declared type cannot honor, a
// colliding alias, a default on a required parameter — and comes back as
// a plain error rather than a *ValidationError, so the API layer answers
// 500 instead of blaming the caller. Compile catches the same defects
// earlier, plus the few that are too expensive to re-check per request.
func (props Properties) Validate(in map[string]any) (*Params, error) {
	names := slices.Sorted(maps.Keys(props))
	aliases, err := aliasIndex(props, names)
	if err != nil {
		return nil, err
	}

	for _, key := range slices.Sorted(maps.Keys(in)) {
		if _, ok := props[key]; ok {
			continue
		}
		if _, ok := aliases[key]; ok {
			continue
		}
		return nil, newErr(key, "unknown parameter (not declared by this endpoint)")
	}

	out := &Params{
		props:    props,
		values:   make(map[string]any, len(props)),
		supplied: make(map[string]bool, len(props)),
	}

	for _, name := range names {
		prop := props[name]

		raw, ok := present(in, name)
		if prop.Alias != "" {
			aliasRaw, aliasOK := present(in, prop.Alias)
			switch {
			case ok && aliasOK:
				return nil, newErr(name, fmt.Sprintf("cannot be combined with its alias %s", prop.Alias))
			case aliasOK:
				raw, ok = aliasRaw, true
			}
		}

		if !ok {
			if !prop.Optional {
				if prop.Default != nil {
					// checkDeclaration only sees parameters that carry a
					// value, so the same contradiction is caught here: a
					// default that can never apply must not be reported as
					// the caller's missing parameter.
					return nil, fmt.Errorf("apischema: parameter %q is required but declares a default, which could never apply", name)
				}
				return nil, newErr(name, "missing required parameter")
			}
			if prop.Default == nil {
				continue
			}
			// Defaults go through the same pipeline as supplied values,
			// so a default of "32G" reads back as "32" like any other
			// disk size. A default that cannot survive its own property
			// is a declaration bug, not a request bug — so the failure is
			// re-stated rather than wrapped: an errors.As probe for
			// *ValidationError must not match it and answer 400.
			// The default is copied first: an Object default handed out
			// by reference would let one request's handler edit the
			// schema every later request reads.
			v, err := validateValue(name, prop, deepCopyValue(prop.Default))
			if err != nil {
				return nil, fmt.Errorf("apischema: default for parameter %q is invalid: %s", name, err)
			}
			out.values[name] = v
			continue
		}

		v, err := validateValue(name, prop, raw)
		if err != nil {
			return nil, err
		}
		out.values[name] = v
		out.supplied[name] = true
	}

	for _, name := range names {
		prop := props[name]
		if len(prop.Requires) == 0 || !out.supplied[name] {
			// Requires is about what the caller named, on both sides. It
			// fires only on a parameter they actually sent — one carrying
			// its default has not been "used" and should not drag its
			// companions in — and it is satisfied only by a companion they
			// also sent. Accepting a defaulted companion would make the
			// constraint vacuous exactly where it matters: "if you give me
			// an index, tell me which bus" has to mean the caller chose
			// the bus, not that a default supplied one.
			continue
		}
		for _, req := range prop.Requires {
			if !declared(props, req) {
				return nil, fmt.Errorf("apischema: parameter %q requires undeclared parameter %q", name, req)
			}
			if !out.supplied[req] {
				return nil, newErr(name, fmt.Sprintf("requires the parameter %s to also be set", req))
			}
		}
	}

	return out, nil
}

// aliasIndex maps each declared alias to the parameter it stands for,
// rejecting the shapes that would otherwise fill two parameters from one
// value: an alias that names another declared parameter, an alias two
// parameters both claim, and a parameter aliasing itself. names must be
// the schema's parameter names in sorted order, so the error a broken
// schema reports is the same one every time.
func aliasIndex(props Properties, names []string) (map[string]string, error) {
	var aliases map[string]string
	for _, name := range names {
		if name == "" {
			return nil, errors.New("apischema: schema declares an empty parameter name")
		}
		prop := props[name]
		if prop.Alias == "" {
			continue
		}
		switch {
		case prop.Alias == name:
			return nil, fmt.Errorf("apischema: parameter %q is its own alias", name)
		case declared(props, prop.Alias):
			return nil, fmt.Errorf("apischema: alias %q of parameter %q collides with a declared parameter", prop.Alias, name)
		case aliases[prop.Alias] != "":
			return nil, fmt.Errorf("apischema: alias %q is claimed by both %q and %q", prop.Alias, aliases[prop.Alias], name)
		}
		if aliases == nil {
			aliases = make(map[string]string, 1)
		}
		aliases[prop.Alias] = name
	}
	return aliases, nil
}

// present reports whether key carries a value. A JSON null counts as
// absent: "size": null and an omitted size mean the same thing to every
// client that produces them.
//
// An empty string does NOT count as absent — it is a value, and it does
// not fall back to the default. Every built-in format rejects it, so a
// parameter that must not be blank should carry a Format, a Pattern or a
// MinLength rather than rely on presence; whatever extracts query and
// form values should omit a key it did not see rather than pass "".
func present(in map[string]any, key string) (any, bool) {
	v, ok := in[key]
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

// validateValue runs one value through the whole per-property pipeline.
func validateValue(field string, prop Property, raw any) (any, error) {
	if err := checkDeclaration(field, prop); err != nil {
		return nil, err
	}
	v, err := coerce(field, prop, raw)
	if err != nil {
		return nil, err
	}

	switch prop.Type {
	case String:
		s, _ := v.(string)
		if prop.Format != "" {
			fn, ok := LookupFormat(prop.Format)
			if !ok {
				return nil, fmt.Errorf("apischema: parameter %q uses unregistered format %q", field, prop.Format)
			}
			normalized, ferr := fn(s)
			if ferr != nil {
				return nil, newErr(field, ferr.Error())
			}
			s = normalized
		}
		if err := checkString(field, prop, s); err != nil {
			return nil, err
		}
		return s, nil

	case Integer:
		n, _ := v.(int64)
		return n, checkIntBounds(field, prop, n)

	case Number:
		f, _ := v.(float64)
		return f, checkNumeric(field, prop, f, formatFloat(f))

	case Array:
		elems, _ := v.([]any)
		if err := checkLength(field, prop, len(elems), "item", "items"); err != nil {
			return nil, err
		}
		return elems, nil

	default: // Boolean, Object
		return v, nil
	}
}

// checkString applies the enum, pattern and length rules.
func checkString(field string, prop Property, s string) error {
	if len(prop.Enum) > 0 && !slices.Contains(prop.Enum, s) {
		if err := checkEnumReachable(field, prop); err != nil {
			return err
		}
		return newErr(field, "expected one of: "+strings.Join(prop.Enum, ", "))
	}
	if prop.Pattern != "" {
		re, err := patternRegexp(prop.Pattern)
		if err != nil {
			return fmt.Errorf("apischema: parameter %q has an invalid pattern %q: %w", field, prop.Pattern, err)
		}
		if !re.MatchString(s) {
			return newErr(field, fmt.Sprintf("value does not match the expected pattern %s", prop.Pattern))
		}
	}
	return checkLength(field, prop, utf8.RuneCountInString(s), "character", "characters")
}

// int64 at the edges of what a float64 bound can say about it. The
// conversion rounds, so maxInt64AsFloat is 2^63 — one above the largest
// int64, not equal to it.
const (
	maxInt64AsFloat = float64(math.MaxInt64)
	minInt64AsFloat = float64(math.MinInt64)
)

// checkIntBounds applies Minimum and Maximum to an integer without
// widening it to float64 first. Past 2^53 that widening loses precision:
// 9007199254740993 becomes 9007199254740992, so a value one above its
// declared maximum compares equal to it and passes. Byte counts and
// nanosecond timestamps both live up there.
func checkIntBounds(field string, prop Property, n int64) error {
	if m := prop.Minimum; m != nil {
		switch {
		case *m >= maxInt64AsFloat:
			// No int64 reaches the bound, so every value is below it.
			return newErr(field, fmt.Sprintf("must be at least %s (got %d)", formatFloat(*m), n))
		case *m >= minInt64AsFloat:
			// Ceil first: the smallest integer satisfying a fractional
			// bound is the one above it.
			if lo := int64(math.Ceil(*m)); n < lo {
				return newErr(field, fmt.Sprintf("must be at least %s (got %d)", formatFloat(*m), n))
			}
		}
	}
	if m := prop.Maximum; m != nil {
		switch {
		case *m < minInt64AsFloat:
			return newErr(field, fmt.Sprintf("must be at most %s (got %d)", formatFloat(*m), n))
		case *m < maxInt64AsFloat:
			if hi := int64(math.Floor(*m)); n > hi {
				return newErr(field, fmt.Sprintf("must be at most %s (got %d)", formatFloat(*m), n))
			}
		}
	}
	return nil
}

// checkNumeric applies the Minimum and Maximum bounds to a float. text
// carries the already-formatted value so that the caller's number is
// echoed the way it was declared.
func checkNumeric(field string, prop Property, v float64, text string) error {
	if prop.Minimum != nil && v < *prop.Minimum {
		return newErr(field, fmt.Sprintf("must be at least %s (got %s)", formatFloat(*prop.Minimum), text))
	}
	if prop.Maximum != nil && v > *prop.Maximum {
		return newErr(field, fmt.Sprintf("must be at most %s (got %s)", formatFloat(*prop.Maximum), text))
	}
	return nil
}

// checkLength applies MinLength and MaxLength, which count characters on
// a string and elements on an array.
//
// BESIDE A FORMAT, IT MEASURES THE NORMALIZED VALUE, not the one the
// caller sent — normalization runs first (see the order on Validate). A
// MinLength of 4 next to the disk-size format refuses "500G": four
// characters as typed, three once normalized to "500". The bound is real
// and the rejection is correct, but it is stated against a string the
// caller never sees, and /api/v1/api-docs publishes the two side by side
// with nothing saying which order they apply in.
//
// EVERY format that rewrites is a hazard here, so read this as the LIST
// and not as a sample of it: disk-size; bwlimit ("0007" -> "7",
// proportionally the largest rewrite of the lot); email, which trims and
// drops the angle brackets, so "<user@example.com>" arrives 18 characters
// long and is measured at 16; ip; cidr; mac-addr; and fingerprint-sha256.
// Prefer expressing the limit in the format itself, or check that the
// bound still means what you intend after the rewrite.
func checkLength(field string, prop Property, n int, unit, units string) error {
	if prop.MinLength != nil && n < *prop.MinLength {
		return newErr(field, fmt.Sprintf("must have at least %d %s", *prop.MinLength, plural(*prop.MinLength, unit, units)))
	}
	if prop.MaxLength != nil && n > *prop.MaxLength {
		return newErr(field, fmt.Sprintf("must have at most %d %s", *prop.MaxLength, plural(*prop.MaxLength, unit, units)))
	}
	return nil
}

func plural(n int, unit, units string) string {
	if n == 1 {
		return unit
	}
	return units
}

// coerce converts raw to the declared type. It is deliberately forgiving
// in the ways real clients are sloppy — this replaces a layer where a
// JSON number for a string field was rejected outright — but never in a
// way that changes the value: 500 becomes "500", "true" becomes true, and
// 1.5 is still not an integer.
func coerce(field string, prop Property, raw any) (any, error) {
	switch prop.Type {
	case String:
		s, ok := toString(raw)
		if !ok {
			return nil, newErr(field, "expected a string")
		}
		return s, nil

	case Integer:
		n, ok, ranged := toInt64(raw)
		if ranged {
			return nil, newErr(field, "integer is out of range")
		}
		if !ok {
			return nil, newErr(field, "expected an integer")
		}
		return n, nil

	case Number:
		f, ok := toFloat64(raw)
		if !ok {
			return nil, newErr(field, "expected a number")
		}
		return f, nil

	case Boolean:
		b, ok := toBool(raw)
		if !ok {
			return nil, newErr(field, "expected a boolean (true or false)")
		}
		return b, nil

	case Array:
		if prop.Items == nil {
			return nil, fmt.Errorf("apischema: array parameter %q declares no items schema", field)
		}
		in, ok := toSlice(raw)
		if !ok {
			return nil, newErr(field, "expected an array")
		}
		out := make([]any, 0, len(in))
		for i, e := range in {
			v, err := validateValue(fmt.Sprintf("%s[%d]", field, i), *prop.Items, e)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil

	case Object:
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, newErr(field, "expected an object")
		}
		return m, nil

	default:
		return nil, fmt.Errorf("apischema: parameter %q has unknown type %q", field, prop.Type)
	}
}

func toString(raw any) (string, bool) {
	switch v := raw.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return stringForm(v), true
	default:
		return "", false
	}
}

// toInt64 reports the coerced value, whether the shape was usable, and
// whether it was the right shape but out of int64's range — the last
// deserves its own message, because "expected an integer" is a lie when
// the caller sent one.
func toInt64(raw any) (value int64, ok, outOfRange bool) {
	switch v := raw.(type) {
	case int:
		return int64(v), true, false
	case int8:
		return int64(v), true, false
	case int16:
		return int64(v), true, false
	case int32:
		return int64(v), true, false
	case int64:
		return v, true, false
	case uint, uint8, uint16, uint32, uint64:
		u := widenUint(v)
		if u > math.MaxInt64 {
			return 0, false, true
		}
		return int64(u), true, false
	case float32:
		return floatToInt64(float64(v))
	case float64:
		return floatToInt64(v)
	case json.Number:
		return parseInt64(v.String())
	case string:
		return parseInt64(v)
	default:
		return 0, false, false
	}
}

func floatToInt64(f float64) (value int64, ok, outOfRange bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, false, false
	}
	if f > math.MaxInt64 || f < math.MinInt64 {
		return 0, false, true
	}
	return int64(f), true, false
}

func parseInt64(s string) (value int64, ok, outOfRange bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false, strings.Contains(err.Error(), "out of range")
	}
	return n, true, false
}

func toFloat64(raw any) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, finite(v)
	case float32:
		return float64(v), finite(float64(v))
	case uint, uint8, uint16, uint32, uint64:
		return float64(widenUint(v)), true
	case int, int8, int16, int32, int64:
		n, ok, _ := toInt64(v)
		return float64(n), ok
	case json.Number:
		return parseFloat(v.String())
	case string:
		return parseFloat(v)
	default:
		return 0, false
	}
}

func parseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return f, finite(f)
}

func finite(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }

// toBool accepts PVE's spelling of a boolean as well as JSON's: the
// query string and form bodies that feed this engine carry 1/0, yes/no
// and on/off just as often as true/false.
func toBool(raw any) (value, ok bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "on":
			return true, true
		case "false", "0", "no", "off":
			return false, true
		default:
			return false, false
		}
	case json.Number, float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		n, ok, _ := toInt64(v)
		if !ok || (n != 0 && n != 1) {
			return false, false
		}
		return n == 1, true
	default:
		return false, false
	}
}

// toSlice accepts a JSON array, a Go []string, or a lone scalar standing
// for a one-element array — which is what a query string produces when a
// repeatable parameter is given once. It deliberately does NOT split on
// commas: a value that legitimately contains one would be torn in half.
func toSlice(raw any) ([]any, bool) {
	switch v := raw.(type) {
	case []any:
		return v, true
	case []string:
		out := make([]any, 0, len(v))
		for _, s := range v {
			out = append(out, s)
		}
		return out, true
	case map[string]any:
		return nil, false
	case nil:
		return nil, false
	default:
		return []any{v}, true
	}
}

// widenUint widens any unsigned integer to uint64 without reflection.
func widenUint(raw any) uint64 {
	switch v := raw.(type) {
	case uint:
		return uint64(v)
	case uint8:
		return uint64(v)
	case uint16:
		return uint64(v)
	case uint32:
		return uint64(v)
	case uint64:
		return v
	default:
		return 0
	}
}

// stringForm renders a coerced value the way it should appear in a string
// parameter or in Strings(): integers without a decimal point, floats
// without a trailing zero.
func stringForm(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return formatFloat(t)
	case float32:
		return formatFloat(float64(t))
	case int:
		return strconv.FormatInt(int64(t), 10)
	case int8:
		return strconv.FormatInt(int64(t), 10)
	case int16:
		return strconv.FormatInt(int64(t), 10)
	case int32:
		return strconv.FormatInt(int64(t), 10)
	case int64:
		return strconv.FormatInt(t, 10)
	case uint, uint8, uint16, uint32, uint64:
		return strconv.FormatUint(widenUint(t), 10)
	case json.Number:
		return t.String()
	default:
		return fmt.Sprint(t)
	}
}

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// Compile validates the schema itself. It is meant to run once at
// registration time — at startup, and in a guard test — so that a
// malformed declaration fails loudly there rather than on the first
// request that happens to exercise it. It also warms the pattern cache.
//
// Validate does not depend on it: every defect that would change
// Validate's answer is re-checked per request (see checkDeclaration and
// aliasIndex), so a schema that never saw Compile still fails closed.
// What Compile adds is the timing, plus the three checks that are too
// expensive to repeat on every request and that a request can only
// stumble into: that each default and each enum value survives its own
// property's rules, that every pattern compiles, and that every Requires
// names a parameter it could actually fail on.
func (props Properties) Compile() error {
	names := slices.Sorted(maps.Keys(props))
	if _, err := aliasIndex(props, names); err != nil {
		return err
	}
	for _, name := range names {
		if err := compileProperty(name, props[name], props); err != nil {
			return err
		}
	}
	return nil
}

func declared(props Properties, name string) bool {
	_, ok := props[name]
	return ok
}

// checkDeclaration rejects a property whose own facets its declared type
// cannot honor. It runs on every value, not only from Compile, because
// the alternative is failing OPEN: an Enum on an integer or a Minimum on
// a string is simply never consulted, so a restriction the author wrote
// down would silently not exist. It is a handful of nil tests and
// allocates nothing.
//
// Every error here is the schema's fault rather than the caller's, so
// none of them is a *ValidationError.
func checkDeclaration(name string, prop Property) error {
	switch prop.Type {
	case String, Integer, Number, Boolean, Array, Object:
	default:
		return fmt.Errorf("apischema: parameter %q has unknown type %q", name, prop.Type)
	}
	switch prop.Source {
	case SourceAuto, SourcePath, SourceQuery, SourceBody:
	default:
		return fmt.Errorf("apischema: parameter %q has unknown source %q", name, prop.Source)
	}

	if prop.Enum != nil {
		if prop.Type != String {
			return fmt.Errorf("apischema: parameter %q declares an enum but is %s, not a string", name, prop.Type)
		}
		if len(prop.Enum) == 0 {
			return fmt.Errorf("apischema: parameter %q declares an empty enum, which nothing can satisfy", name)
		}
	}
	if prop.Type != String {
		if prop.Pattern != "" {
			return fmt.Errorf("apischema: parameter %q declares a pattern but is %s, not a string", name, prop.Type)
		}
		if prop.Format != "" {
			return fmt.Errorf("apischema: parameter %q declares a format but is %s, not a string", name, prop.Type)
		}
	}
	if prop.Type != String && prop.Type != Array && (prop.MinLength != nil || prop.MaxLength != nil) {
		return fmt.Errorf("apischema: parameter %q declares length bounds but is %s, not a string or array", name, prop.Type)
	}
	if prop.Type != Integer && prop.Type != Number && (prop.Minimum != nil || prop.Maximum != nil) {
		return fmt.Errorf("apischema: parameter %q declares numeric bounds but is %s, not a number", name, prop.Type)
	}
	if prop.Minimum != nil && !finite(*prop.Minimum) {
		return fmt.Errorf("apischema: parameter %q has a non-finite minimum", name)
	}
	if prop.Maximum != nil && !finite(*prop.Maximum) {
		return fmt.Errorf("apischema: parameter %q has a non-finite maximum", name)
	}
	if prop.Minimum != nil && prop.Maximum != nil && *prop.Minimum > *prop.Maximum {
		return fmt.Errorf("apischema: parameter %q has minimum %s above maximum %s", name, formatFloat(*prop.Minimum), formatFloat(*prop.Maximum))
	}
	if prop.MinLength != nil && *prop.MinLength < 0 {
		return fmt.Errorf("apischema: parameter %q has a negative minimum length", name)
	}
	if prop.MaxLength != nil && *prop.MaxLength < 0 {
		return fmt.Errorf("apischema: parameter %q has a negative maximum length", name)
	}
	if prop.MinLength != nil && prop.MaxLength != nil && *prop.MinLength > *prop.MaxLength {
		return fmt.Errorf("apischema: parameter %q has minimum length %d above maximum length %d", name, *prop.MinLength, *prop.MaxLength)
	}

	switch {
	case prop.Type == Array && prop.Items == nil:
		return fmt.Errorf("apischema: array parameter %q declares no items schema", name)
	case prop.Type != Array && prop.Items != nil:
		return fmt.Errorf("apischema: parameter %q declares an items schema but is %s, not an array", name, prop.Type)
	}
	if prop.Default != nil && !prop.Optional {
		return fmt.Errorf("apischema: parameter %q is required but declares a default, which could never apply", name)
	}
	return nil
}

// checkEnumReachable runs on the way to rejecting a value that matched no
// enum entry, and asks first whether any value could have matched. An
// entry the property's own format would rewrite — an unnormalized disk
// size, an uppercase UUID — can never appear at the comparison, because
// normalization happens first. That is our bug, and answering the caller
// "expected one of: 1T" when 1T is also refused would send them in
// circles.
func checkEnumReachable(field string, prop Property) error {
	if prop.Format == "" && prop.Pattern == "" && prop.MinLength == nil && prop.MaxLength == nil {
		return nil
	}
	for _, entry := range prop.Enum {
		if err := compileEnumEntry(field, prop, entry); err != nil {
			return err
		}
	}
	return nil
}

// compileProperty checks one property. props is the enclosing schema, or
// nil for an element schema, where cross-field rules do not apply.
func compileProperty(name string, prop Property, props Properties) error {
	if err := checkDeclaration(name, prop); err != nil {
		return err
	}

	if prop.Pattern != "" {
		if _, err := patternRegexp(prop.Pattern); err != nil {
			return fmt.Errorf("apischema: parameter %q has an invalid pattern %q: %w", name, prop.Pattern, err)
		}
	}
	if prop.Format != "" {
		if _, ok := LookupFormat(prop.Format); !ok {
			return fmt.Errorf("apischema: parameter %q uses unregistered format %q", name, prop.Format)
		}
	}
	if prop.Type == Array {
		if err := compileItems(name, *prop.Items); err != nil {
			return err
		}
	}

	for _, req := range prop.Requires {
		if req == name {
			return fmt.Errorf("apischema: parameter %q requires itself", name)
		}
		if props == nil {
			continue
		}
		target, ok := props[req]
		if !ok {
			return fmt.Errorf("apischema: parameter %q requires undeclared parameter %q", name, req)
		}
		// A companion the caller must always send cannot be the thing
		// that is missing, so the constraint could never fire — and a
		// requires that can never fire reads as protection and is none.
		if !target.Optional {
			return fmt.Errorf("apischema: parameter %q requires %q, which is not optional and is therefore always supplied", name, req)
		}
	}

	if prop.Default != nil {
		if _, err := validateValue(name, prop, prop.Default); err != nil {
			return fmt.Errorf("apischema: default for parameter %q is invalid: %s", name, err)
		}
	}

	for _, entry := range prop.Enum {
		if err := compileEnumEntry(name, prop, entry); err != nil {
			return err
		}
	}
	return nil
}

// compileEnumEntry checks that an enum value can actually be produced by
// the property's own pipeline. An entry that its format would rewrite —
// an uppercase UUID, an unnormalized disk size — can never match, because
// normalization runs before the enum comparison.
func compileEnumEntry(name string, prop Property, entry string) error {
	probe := prop
	probe.Enum = nil
	probe.Default = nil
	v, err := validateValue(name, probe, entry)
	if err != nil {
		return fmt.Errorf("apischema: enum value %q of parameter %q is invalid: %s", entry, name, err)
	}
	if s, _ := v.(string); s != entry {
		return fmt.Errorf("apischema: enum value %q of parameter %q normalizes to %q and could never match", entry, name, s)
	}
	return nil
}

// compileItems checks an array's element schema. Elements carry only the
// facets that shape a value; a default, an alias, a source or a Requires
// on an element is meaningless and is far more likely to be a mistake.
func compileItems(name string, item Property) error {
	switch item.Type {
	case String, Integer, Number, Boolean:
	default:
		return fmt.Errorf("apischema: array parameter %q declares %q items; only scalar element types are supported", name, item.Type)
	}
	switch {
	case item.Optional:
		return fmt.Errorf("apischema: items schema of %q cannot be optional", name)
	case item.Default != nil:
		return fmt.Errorf("apischema: items schema of %q cannot declare a default", name)
	case len(item.Requires) > 0:
		return fmt.Errorf("apischema: items schema of %q cannot declare requires", name)
	case item.Alias != "":
		return fmt.Errorf("apischema: items schema of %q cannot declare an alias", name)
	case item.Source != SourceAuto:
		return fmt.Errorf("apischema: items schema of %q cannot declare a source", name)
	}
	return compileProperty(name+"[]", item, nil)
}
