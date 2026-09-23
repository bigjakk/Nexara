package apischema

import (
	"math"
	"strings"
	"testing"
)

func TestCompileAcceptsAWellFormedSchema(t *testing.T) {
	props := Properties{
		"cluster_id": StdOption("cluster-id"),
		"node":       StdOption("node-name"),
		"storage":    StdOption("storage-id").AsOptional(),
		"bwlimit":    StdOption("bwlimit"),
		"bus":        {Type: String, Enum: []string{"ide", "sata", "scsi", "virtio"}, Default: "scsi", Optional: true},
		"index":      {Type: Integer, Optional: true, Minimum: Ptr(0.0), Maximum: Ptr(30.0), Requires: []string{"bus"}},
		"size":       {Type: String, Format: "disk-size", Typetext: "<number><G|T>", Alias: "disk_size"},
		"tags":       {Type: Array, Optional: true, Items: &Property{Type: String, Pattern: `^[a-z]+$`}, MaxLength: Ptr(8)},
		"meta":       {Type: Object, Optional: true},
	}
	if err := props.Compile(); err != nil {
		t.Fatalf("Compile: %v", err)
	}
}

func TestCompileRejectsMalformedSchemas(t *testing.T) {
	cases := []struct {
		name  string
		props Properties
		want  string
	}{
		{
			name:  "unknown format",
			props: Properties{"v": {Type: String, Format: "no-such-format"}},
			want:  `unregistered format "no-such-format"`,
		},
		{
			name:  "enum on a non-string",
			props: Properties{"v": {Type: Integer, Enum: []string{"1", "2"}}},
			want:  "declares an enum but is integer",
		},
		{
			name:  "empty enum",
			props: Properties{"v": {Type: String, Enum: []string{}}},
			want:  "declares an empty enum",
		},
		{
			name:  "array without items",
			props: Properties{"v": {Type: Array}},
			want:  "declares no items schema",
		},
		{
			name:  "items on a non-array",
			props: Properties{"v": {Type: String, Items: &Property{Type: String}}},
			want:  "declares an items schema but is string",
		},
		{
			name:  "nested arrays are unsupported",
			props: Properties{"v": {Type: Array, Items: &Property{Type: Array, Items: &Property{Type: String}}}},
			want:  "only scalar element types are supported",
		},
		{
			name:  "items cannot carry a default",
			props: Properties{"v": {Type: Array, Items: &Property{Type: String, Default: "x"}}},
			want:  "items schema of \"v\" cannot declare a default",
		},
		{
			name:  "bad pattern",
			props: Properties{"v": {Type: String, Pattern: "([a-z"}},
			want:  "invalid pattern",
		},
		{
			name:  "pattern on a non-string",
			props: Properties{"v": {Type: Integer, Pattern: "^1$"}},
			want:  "declares a pattern but is integer",
		},
		{
			name:  "format on a non-string",
			props: Properties{"v": {Type: Integer, Format: "bwlimit"}},
			want:  "declares a format but is integer",
		},
		{
			name:  "default that fails its own rules",
			props: Properties{"v": {Type: String, Optional: true, Format: "disk-size", Default: "enormous"}},
			want:  "default for parameter \"v\" is invalid",
		},
		{
			name:  "default that fails its own bounds",
			props: Properties{"v": {Type: Integer, Optional: true, Maximum: Ptr(10.0), Default: 99}},
			want:  "default for parameter \"v\" is invalid",
		},
		{
			name:  "default of the wrong type",
			props: Properties{"v": {Type: Integer, Optional: true, Default: "abc"}},
			want:  "default for parameter \"v\" is invalid",
		},
		{
			name:  "default on a required parameter",
			props: Properties{"v": {Type: String, Default: "x"}},
			want:  "is required but declares a default",
		},
		{
			name:  "numeric bounds on a string",
			props: Properties{"v": {Type: String, Minimum: Ptr(1.0)}},
			want:  "declares numeric bounds but is string",
		},
		{
			name:  "length bounds on an integer",
			props: Properties{"v": {Type: Integer, MaxLength: Ptr(3)}},
			want:  "declares length bounds but is integer",
		},
		{
			name:  "inverted numeric bounds",
			props: Properties{"v": {Type: Integer, Minimum: Ptr(10.0), Maximum: Ptr(1.0)}},
			want:  "minimum 10 above maximum 1",
		},
		{
			name:  "inverted length bounds",
			props: Properties{"v": {Type: String, MinLength: Ptr(10), MaxLength: Ptr(1)}},
			want:  "minimum length 10 above maximum length 1",
		},
		{
			// A NaN bound compares false against everything, so the
			// restriction would look declared and enforce nothing.
			name:  "non-finite minimum",
			props: Properties{"v": {Type: Number, Minimum: Ptr(math.NaN())}},
			want:  "non-finite minimum",
		},
		{
			name:  "non-finite maximum",
			props: Properties{"v": {Type: Number, Maximum: Ptr(math.Inf(1))}},
			want:  "non-finite maximum",
		},
		{
			name:  "negative length bound",
			props: Properties{"v": {Type: String, MinLength: Ptr(-1)}},
			want:  "negative minimum length",
		},
		{
			name:  "missing type",
			props: Properties{"v": {}},
			want:  `unknown type ""`,
		},
		{
			name:  "unknown source",
			props: Properties{"v": {Type: String, Source: Source("header")}},
			want:  `unknown source "header"`,
		},
		{
			name:  "requires an undeclared parameter",
			props: Properties{"v": {Type: String, Requires: []string{"ghost"}}},
			want:  `requires undeclared parameter "ghost"`,
		},
		{
			name:  "requires itself",
			props: Properties{"v": {Type: String, Requires: []string{"v"}}},
			want:  "requires itself",
		},
		{
			// A required companion is present in every request that gets
			// this far, so the constraint could never fire.
			name:  "requires a parameter that is never absent",
			props: Properties{"v": {Type: String, Optional: true, Requires: []string{"w"}}, "w": {Type: String}},
			want:  "which is not optional and is therefore always supplied",
		},
		{
			name:  "alias collides with a declared parameter",
			props: Properties{"v": {Type: String, Alias: "w"}, "w": {Type: String}},
			want:  "collides with a declared parameter",
		},
		{
			name:  "alias claimed twice",
			props: Properties{"a": {Type: String, Alias: "shared"}, "b": {Type: String, Alias: "shared"}},
			want:  `alias "shared" is claimed by both`,
		},
		{
			name:  "self alias",
			props: Properties{"v": {Type: String, Alias: "v"}},
			want:  "is its own alias",
		},
		{
			name: "enum value that its own format would rewrite",
			props: Properties{"size": {
				Type:   String,
				Format: "disk-size",
				// "1T" normalizes to "1024" before the enum runs, so this
				// list could never match anything.
				Enum: []string{"1T"},
			}},
			want: "could never match",
		},
		{
			name:  "enum value that fails its own format",
			props: Properties{"size": {Type: String, Format: "disk-size", Enum: []string{"lots"}}},
			want:  "is invalid",
		},
		{
			name:  "empty parameter name",
			props: Properties{"": {Type: String}},
			want:  "empty parameter name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.props.Compile()
			if err == nil {
				t.Fatalf("Compile() = nil, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Compile() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestCompileWarmsThePatternCache(t *testing.T) {
	pattern := `^apischema-cache-probe-[0-9]+$`
	if _, ok := regexCache.Load(pattern); ok {
		t.Fatalf("pattern %q was already cached", pattern)
	}
	props := Properties{"v": {Type: String, Pattern: pattern}}
	if err := props.Compile(); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, ok := regexCache.Load(pattern); !ok {
		t.Error("Compile did not cache the compiled pattern")
	}
}

func TestValidateWorksWithoutCompile(t *testing.T) {
	// Compile is about timing, not correctness: a schema that never saw
	// it must still validate the same way.
	props := Properties{"v": {Type: String, Pattern: `^[a-z]+$`}}
	if _, err := props.Validate(map[string]any{"v": "abc"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err := props.Validate(map[string]any{"v": "ABC"})
	wantValidationError(t, err, "v", "does not match")
}

func TestValidateReportsAnInvalidPatternAsASchemaError(t *testing.T) {
	props := Properties{"v": {Type: String, Pattern: "([a-z"}}
	_, err := props.Validate(map[string]any{"v": "abc"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if _, ok := err.(*ValidationError); ok {
		t.Fatalf("an invalid pattern is a schema bug, not a caller error: %v", err)
	}
	if !strings.Contains(err.Error(), "invalid pattern") {
		t.Errorf("error = %v", err)
	}
}

func TestCompileRejectsMeaninglessItemFacets(t *testing.T) {
	cases := []struct {
		name string
		item Property
		want string
	}{
		{"optional", Property{Type: String, Optional: true}, "cannot be optional"},
		{"requires", Property{Type: String, Requires: []string{"other"}}, "cannot declare requires"},
		{"alias", Property{Type: String, Alias: "other"}, "cannot declare an alias"},
		{"source", Property{Type: String, Source: SourceQuery}, "cannot declare a source"},
		// Boolean, the one type the facet is otherwise allowed on, so the
		// refusal can only be the items rule.
		{"empty is absent", Property{Type: Boolean, EmptyIsAbsent: true}, "cannot count an empty value as absent"},
		{"object items", Property{Type: Object}, "only scalar element types are supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := tc.item
			err := Properties{"v": {Type: Array, Items: &item}, "other": {Type: String}}.Compile()
			if err == nil {
				t.Fatalf("Compile() = nil, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Compile() = %v, want an error containing %q", err, tc.want)
			}
		})
	}

	t.Run("item facets that do apply are checked", func(t *testing.T) {
		item := Property{Type: String, Format: "no-such-format"}
		err := Properties{"v": {Type: Array, Items: &item}}.Compile()
		if err == nil || !strings.Contains(err.Error(), `"v[]"`) {
			t.Errorf("Compile() = %v, want an error naming the element schema", err)
		}
	})
}

// TestCompileIsSufficientForStartup pins the declaration defects that
// Validate does NOT catch on its own, and proves Compile catches each of
// them. Three gaps are known and deliberately left lazy-unchecked:
// checkEnumReachable only guards the enum-miss branch, so a contradictory
// enum 400s the caller instead of reporting our bug; checkDeclaration only
// runs on parameters that carry a value, so a typo'd format name in an
// omitted optional is invisible until someone sends it; and compileItems
// is Compile-only, so a nested array renders its elements with %v instead
// of being refused.
//
// Adding more lazy checks is not the answer — the answer is that the
// router calls Compile() on every schema at registration, which turns
// each of these into a boot failure rather than a per-request 400 or 500.
// This table is the evidence that doing so is enough.
func TestCompileIsSufficientForStartup(t *testing.T) {
	cases := []struct {
		name  string
		props Properties
		want  string
	}{
		{
			name:  "an enum entry its own pattern rejects",
			props: Properties{"v": {Type: String, Pattern: `^[a-z]+$`, Enum: []string{"ABC"}}},
			want:  `enum value "ABC" of parameter "v" is invalid`,
		},
		{
			name:  "an enum entry its own length bound rejects",
			props: Properties{"v": {Type: String, MaxLength: Ptr(2), Enum: []string{"abc"}}},
			want:  `enum value "abc" of parameter "v" is invalid`,
		},
		{
			name:  "an unregistered format on a parameter nobody sends",
			props: Properties{"v": {Type: String, Optional: true, Format: "no-such-format"}},
			want:  `unregistered format "no-such-format"`,
		},
		{
			name:  "a nested array inside items",
			props: Properties{"v": {Type: Array, Items: &Property{Type: Array, Items: &Property{Type: String}}}},
			want:  "only scalar element types are supported",
		},
		{
			name:  "an object element type",
			props: Properties{"v": {Type: Array, Items: &Property{Type: Object}}},
			want:  "only scalar element types are supported",
		},
		{
			name:  "numeric bounds on a string",
			props: Properties{"v": {Type: String, Minimum: Ptr(1.0), Maximum: Ptr(10.0)}},
			want:  "declares numeric bounds but is string",
		},
		{
			name:  "a type that is not one of the six",
			props: Properties{"v": {Type: Type("str")}},
			want:  `unknown type "str"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.props.Compile()
			if err == nil {
				t.Fatalf("Compile() = nil, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Compile() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestDiskSizeCapIsNotAMaximum documents the trap the disk-size comment
// warns about: Maximum on a string parameter is a declaration error, so
// the natural-looking way to cap a disk size fails every request until
// Compile turns it into a startup failure.
func TestDiskSizeCapIsNotAMaximum(t *testing.T) {
	capped := Properties{"size": {Type: String, Format: "disk-size", Maximum: Ptr(2048.0)}}
	if err := capped.Compile(); err == nil || !strings.Contains(err.Error(), "declares numeric bounds but is string") {
		t.Fatalf("Compile() = %v, want it to refuse a numeric bound on a string", err)
	}

	// MaxLength on the normalized value is the mechanism that works: it
	// counts digits, so 4 admits up to 9999 GiB.
	byDigits := Properties{"size": {Type: String, Format: "disk-size", MaxLength: Ptr(4)}}
	if err := byDigits.Compile(); err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if _, err := byDigits.Validate(map[string]any{"size": "9T"}); err != nil {
		t.Errorf("9216 GiB should fit in 4 digits: %v", err)
	}
	if _, err := byDigits.Validate(map[string]any{"size": "10T"}); err == nil {
		t.Error("10240 GiB is 5 digits and should be refused")
	}
}
