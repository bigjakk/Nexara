package apischema

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
)

// wantValidationError asserts that err is a *ValidationError naming field
// and mentioning want.
func wantValidationError(t *testing.T, err error, field, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a validation error for %q, got nil", field)
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error %v is %T, want *ValidationError (so the API layer can map it to a 400)", err, err)
	}
	if ve.Field != field {
		t.Errorf("error field = %q, want %q (error: %v)", ve.Field, field, err)
	}
	if !strings.Contains(ve.Message, want) {
		t.Errorf("error message %q does not contain %q", ve.Message, want)
	}
	if got := ve.Error(); got != ve.Field+": "+ve.Message {
		t.Errorf("Error() = %q, want %q", got, ve.Field+": "+ve.Message)
	}
}

// attachDiskProps is the schema the disk-attach endpoint should have had.
func attachDiskProps() Properties {
	return Properties{
		"bus":     {Type: String, Enum: []string{"ide", "sata", "scsi", "virtio"}},
		"storage": {Type: String, Format: "storage-id"},
		"size":    {Type: String, Format: "disk-size", Typetext: "<number><G|T>"},
		"index":   {Type: Integer, Optional: true, Minimum: Ptr(0.0), Maximum: Ptr(30.0)},
		"format":  {Type: String, Optional: true, Enum: []string{"qcow2", "raw", "vmdk"}},
	}
}

// TestAttachDiskIncident walks the endpoint that destroyed a boot disk.
func TestAttachDiskIncident(t *testing.T) {
	props := attachDiskProps()
	if err := props.Compile(); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	t.Run("every size shape reaches Proxmox as bare GiB", func(t *testing.T) {
		// The four inputs the incident report collected, including the
		// JSON number, which the previous layer rejected outright.
		cases := []struct {
			in   any
			want string
		}{
			{"500G", "500"},
			{float64(500), "500"},
			{"500", "500"},
			{"512000", "512000"},
			{json.Number("500"), "500"},
		}
		for _, tc := range cases {
			p, err := props.Validate(map[string]any{"bus": "scsi", "storage": "store01", "size": tc.in})
			if err != nil {
				t.Errorf("size %#v: unexpected error: %v", tc.in, err)
				continue
			}
			if got := p.String("size"); got != tc.want {
				t.Errorf("size %#v normalized to %q, want %q", tc.in, got, tc.want)
			}
		}
	})

	t.Run("an omitted index is not index 0", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"bus": "scsi", "storage": "store01", "size": "500G"})
		if idx, supplied := p.OptInt("index"); supplied {
			t.Errorf("OptInt(index) = (%d, true) for an omitted index; the handler would overwrite scsi0", idx)
		}
	})

	t.Run("a raw size is rejected rather than concatenated", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"bus": "scsi", "storage": "store01", "size": "500 GB or so"})
		wantValidationError(t, err, "size", "expected a disk size such as 500, 500G or 1T")
	})

	t.Run("an out-of-range index is rejected", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"bus": "scsi", "storage": "store01", "size": "500G", "index": 99})
		wantValidationError(t, err, "index", "must be at most 30")
	})
}

func TestValidateRequiredAndUnknown(t *testing.T) {
	props := attachDiskProps()

	t.Run("missing required parameter", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"bus": "scsi", "storage": "store01"})
		wantValidationError(t, err, "size", "missing required parameter")
	})

	t.Run("unknown parameter is rejected", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"bus": "scsi", "storage": "store01", "size": "1G", "discard": "on"})
		wantValidationError(t, err, "discard", "unknown parameter")
	})

	t.Run("a misspelled parameter reports the typo, not the absence", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"bus": "scsi", "storage": "store01", "siz": "1G"})
		wantValidationError(t, err, "siz", "unknown parameter")
	})

	t.Run("nil input is an empty request", func(t *testing.T) {
		_, err := Properties{"a": {Type: String, Optional: true}}.Validate(nil)
		if err != nil {
			t.Errorf("Validate(nil) = %v, want no error", err)
		}
	})
}

func TestValidateCoercion(t *testing.T) {
	cases := []struct {
		name   string
		prop   Property
		in     any
		want   any
		errStr string
	}{
		// String: real clients send numbers for string fields.
		{name: "json number to string", prop: Property{Type: String}, in: float64(500), want: "500"},
		{name: "float to string keeps the fraction", prop: Property{Type: String}, in: 1.5, want: "1.5"},
		{name: "int to string", prop: Property{Type: String}, in: 42, want: "42"},
		{name: "json.Number to string", prop: Property{Type: String}, in: json.Number("7"), want: "7"},
		{name: "string to string", prop: Property{Type: String}, in: "abc", want: "abc"},
		{name: "bool is not a string", prop: Property{Type: String}, in: true, errStr: "expected a string"},

		// Integer.
		{name: "whole float to integer", prop: Property{Type: Integer}, in: float64(500), want: int64(500)},
		{name: "numeric string to integer", prop: Property{Type: Integer}, in: "500", want: int64(500)},
		{name: "negative integer", prop: Property{Type: Integer}, in: "-3", want: int64(-3)},
		{name: "fractional float is not an integer", prop: Property{Type: Integer}, in: 1.5, errStr: "expected an integer"},
		{name: "text is not an integer", prop: Property{Type: Integer}, in: "abc", errStr: "expected an integer"},
		{name: "bool is not an integer", prop: Property{Type: Integer}, in: true, errStr: "expected an integer"},
		{name: "oversized integer string", prop: Property{Type: Integer}, in: "9223372036854775808", errStr: "out of range"},
		{name: "oversized float", prop: Property{Type: Integer}, in: 1e19, errStr: "out of range"},

		// Number.
		{name: "int to number", prop: Property{Type: Number}, in: 2, want: float64(2)},
		{name: "numeric string to number", prop: Property{Type: Number}, in: "1.5", want: 1.5},
		{name: "text is not a number", prop: Property{Type: Number}, in: "abc", errStr: "expected a number"},

		// Boolean: PVE's spellings as well as JSON's.
		{name: "true string", prop: Property{Type: Boolean}, in: "true", want: true},
		{name: "false string", prop: Property{Type: Boolean}, in: "false", want: false},
		{name: "one string", prop: Property{Type: Boolean}, in: "1", want: true},
		{name: "off string", prop: Property{Type: Boolean}, in: "OFF", want: false},
		{name: "yes string", prop: Property{Type: Boolean}, in: "yes", want: true},
		{name: "number one", prop: Property{Type: Boolean}, in: float64(1), want: true},
		{name: "number two is not a boolean", prop: Property{Type: Boolean}, in: float64(2), errStr: "expected a boolean"},
		{name: "maybe is not a boolean", prop: Property{Type: Boolean}, in: "maybe", errStr: "expected a boolean"},

		// Object.
		{name: "object passes through", prop: Property{Type: Object}, in: map[string]any{"a": 1}, want: map[string]any{"a": 1}},
		{name: "string is not an object", prop: Property{Type: Object}, in: "a", errStr: "expected an object"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := Properties{"v": tc.prop}
			p, err := props.Validate(map[string]any{"v": tc.in})
			if tc.errStr != "" {
				wantValidationError(t, err, "v", tc.errStr)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := p.Raw()["v"]
			if _, isMap := tc.want.(map[string]any); isMap {
				if _, ok := got.(map[string]any); !ok {
					t.Fatalf("got %#v, want a map", got)
				}
				return
			}
			if got != tc.want {
				t.Errorf("got %#v (%T), want %#v (%T)", got, got, tc.want, tc.want)
			}
		})
	}
}

func TestValidateArrays(t *testing.T) {
	props := Properties{
		"tags":  {Type: Array, Optional: true, Items: &Property{Type: String}, MinLength: Ptr(1), MaxLength: Ptr(3)},
		"vmids": {Type: Array, Optional: true, Items: &Property{Type: Integer, Minimum: Ptr(100.0)}},
	}

	t.Run("json array", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"tags": []any{"a", "b"}})
		if got := strings.Join(p.Strings("tags"), ","); got != "a,b" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("go string slice", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"tags": []string{"a"}})
		if got := strings.Join(p.Strings("tags"), ","); got != "a" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("a lone scalar is a one-element array", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"tags": "solo"})
		if got := p.Strings("tags"); len(got) != 1 || got[0] != "solo" {
			t.Errorf("got %v, want [solo]", got)
		}
	})

	t.Run("commas are not split", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"tags": "a,b"})
		if got := p.Strings("tags"); len(got) != 1 || got[0] != "a,b" {
			t.Errorf("got %v, want one element [a,b]", got)
		}
	})

	t.Run("element rules apply and name the index", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"vmids": []any{101, 99}})
		wantValidationError(t, err, "vmids[1]", "must be at least 100")
	})

	t.Run("elements are coerced", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"vmids": []any{"101", float64(102)}})
		if got := strings.Join(p.Strings("vmids"), ","); got != "101,102" {
			t.Errorf("got %q, want 101,102", got)
		}
	})

	t.Run("item count bounds", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"tags": []any{"a", "b", "c", "d"}})
		wantValidationError(t, err, "tags", "must have at most 3 items")

		_, err = props.Validate(map[string]any{"tags": []any{}})
		wantValidationError(t, err, "tags", "must have at least 1 item")
	})

	t.Run("an object is not an array", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"tags": map[string]any{"a": 1}})
		wantValidationError(t, err, "tags", "expected an array")
	})
}

func TestValidateConstraints(t *testing.T) {
	cases := []struct {
		name   string
		prop   Property
		in     any
		errStr string
	}{
		{"enum", Property{Type: String, Enum: []string{"ide", "sata", "scsi", "virtio"}}, "usb", "expected one of: ide, sata, scsi, virtio"},
		{"pattern", Property{Type: String, Pattern: `^[a-z]+$`}, "Abc", "does not match the expected pattern ^[a-z]+$"},
		{"minimum", Property{Type: Integer, Minimum: Ptr(1.0)}, 0, "must be at least 1 (got 0)"},
		{"maximum", Property{Type: Integer, Maximum: Ptr(30.0)}, 31, "must be at most 30 (got 31)"},
		{"number minimum", Property{Type: Number, Minimum: Ptr(0.5)}, 0.25, "must be at least 0.5"},
		{"number maximum", Property{Type: Number, Maximum: Ptr(0.5)}, 0.75, "must be at most 0.5 (got 0.75)"},
		{"min length", Property{Type: String, MinLength: Ptr(3)}, "ab", "must have at least 3 characters"},
		{"max length", Property{Type: String, MaxLength: Ptr(3)}, "abcd", "must have at most 3 characters"},
		{"format", Property{Type: String, Format: "node-name"}, "bad node!", "expected a node name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Properties{"v": tc.prop}.Validate(map[string]any{"v": tc.in})
			wantValidationError(t, err, "v", tc.errStr)
		})
	}

	t.Run("length counts characters, not bytes", func(t *testing.T) {
		props := Properties{"v": {Type: String, MaxLength: Ptr(3)}}
		if _, err := props.Validate(map[string]any{"v": "héé"}); err != nil {
			t.Errorf("3 multi-byte characters should fit in MaxLength 3: %v", err)
		}
	})

	t.Run("the format runs before the enum", func(t *testing.T) {
		// The enum lists normalized values, so an unnormalized input
		// still matches.
		props := Properties{"size": {Type: String, Format: "disk-size", Enum: []string{"500", "1024"}}}
		p := mustValidate(t, props, map[string]any{"size": "1T"})
		if got := p.String("size"); got != "1024" {
			t.Errorf("got %q, want 1024", got)
		}
	})
}

func TestValidateDefaults(t *testing.T) {
	t.Run("a default is normalized like any other value", func(t *testing.T) {
		props := Properties{"size": {Type: String, Optional: true, Format: "disk-size", Default: "32G"}}
		p := mustValidate(t, props, nil)
		if got := p.String("size"); got != "32" {
			t.Errorf(`String("size") = %q, want the normalized default "32"`, got)
		}
	})

	t.Run("a default is coerced like any other value", func(t *testing.T) {
		props := Properties{"count": {Type: Integer, Optional: true, Default: 5}}
		p := mustValidate(t, props, nil)
		if got := p.Int("count"); got != 5 {
			t.Errorf(`Int("count") = %d, want 5`, got)
		}
	})

	t.Run("a broken default is a schema error, not a validation error", func(t *testing.T) {
		props := Properties{"size": {Type: String, Optional: true, Format: "disk-size", Default: "huge"}}
		_, err := props.Validate(nil)
		if err == nil {
			t.Fatal("expected an error")
		}
		// It must not present as a *ValidationError, or the API layer
		// would answer 400 for a bug in our own declaration.
		var ve *ValidationError
		if errors.As(err, &ve) {
			t.Fatalf("a bad default must not surface as a caller-facing error: %v", err)
		}
		if !strings.Contains(err.Error(), "default for parameter") {
			t.Errorf("error %v should say the default is at fault", err)
		}
	})
}

func TestValidateAlias(t *testing.T) {
	props := Properties{
		"size": {Type: String, Format: "disk-size", Alias: "disk_size"},
	}

	t.Run("the alias supplies the value", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"disk_size": "1T"})
		if got := p.String("size"); got != "1024" {
			t.Errorf("got %q, want 1024", got)
		}
		if !p.Has("size") {
			t.Error("a value given under the alias counts as supplied")
		}
	})

	t.Run("the canonical name still works", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"size": "1T"})
		if got := p.String("size"); got != "1024" {
			t.Errorf("got %q, want 1024", got)
		}
	})

	t.Run("giving both is an error", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"size": "1T", "disk_size": "2T"})
		wantValidationError(t, err, "size", "cannot be combined with its alias disk_size")
	})

	t.Run("the alias satisfies a required parameter", func(t *testing.T) {
		if _, err := props.Validate(map[string]any{}); err == nil {
			t.Error("expected the required parameter to be missing")
		}
	})
}

func TestValidateRequires(t *testing.T) {
	props := Properties{
		"bus":     {Type: String, Optional: true},
		"index":   {Type: Integer, Optional: true, Requires: []string{"bus"}},
		"storage": {Type: String, Optional: true, Default: "store01"},
		"size":    {Type: String, Optional: true, Format: "disk-size", Requires: []string{"storage"}},
	}

	t.Run("satisfied by a supplied companion", func(t *testing.T) {
		mustValidate(t, props, map[string]any{"index": 1, "bus": "scsi"})
	})

	t.Run("unsatisfied", func(t *testing.T) {
		_, err := props.Validate(map[string]any{"index": 1})
		wantValidationError(t, err, "index", "requires the parameter bus to also be set")
	})

	t.Run("not triggered when the requiring parameter is absent", func(t *testing.T) {
		mustValidate(t, props, map[string]any{"bus": "scsi"})
	})

	t.Run("a defaulted companion does not satisfy it", func(t *testing.T) {
		// storage always has a value, but the caller did not choose it —
		// and "if you give me a size, tell me which storage" has to mean
		// the caller named the storage.
		_, err := props.Validate(map[string]any{"size": "1G"})
		wantValidationError(t, err, "size", "requires the parameter storage to also be set")
	})

	t.Run("an explicitly supplied companion satisfies it", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"size": "1G", "storage": "store01"})
		if got := p.String("storage"); got != "store01" {
			t.Errorf("got %q", got)
		}
	})
}

// TestSchemaDefectsFailClosedAtRequestTime pins the half of Compile that
// Validate has to repeat. A schema that never saw Compile must not
// silently drop a facet it cannot honor, must not fill two parameters
// from one value, and must not blame the caller for our own declaration
// bug — the API layer reads *ValidationError as "answer 400".
func TestSchemaDefectsFailClosedAtRequestTime(t *testing.T) {
	cases := []struct {
		name  string
		props Properties
		in    map[string]any
		want  string
	}{
		{
			name:  "enum on an integer is not silently dropped",
			props: Properties{"v": {Type: Integer, Enum: []string{"1", "2"}}},
			in:    map[string]any{"v": 99},
			want:  "declares an enum but is integer",
		},
		{
			name:  "format on an integer is not silently dropped",
			props: Properties{"v": {Type: Integer, Format: "bwlimit"}},
			in:    map[string]any{"v": -5},
			want:  "declares a format but is integer",
		},
		{
			name:  "numeric bounds on a string are not silently dropped",
			props: Properties{"v": {Type: String, Minimum: Ptr(10.0)}},
			in:    map[string]any{"v": "x"},
			want:  "declares numeric bounds but is string",
		},
		{
			name:  "an empty enum is not silently dropped",
			props: Properties{"v": {Type: String, Enum: []string{}}},
			in:    map[string]any{"v": "x"},
			want:  "declares an empty enum",
		},
		{
			name:  "inverted bounds are not the caller's fault",
			props: Properties{"v": {Type: Integer, Minimum: Ptr(10.0), Maximum: Ptr(1.0)}},
			in:    map[string]any{"v": 5},
			want:  "minimum 10 above maximum 1",
		},
		{
			name:  "an alias colliding with a declared parameter cannot fill both",
			props: Properties{"size": {Type: String, Optional: true, Alias: "count"}, "count": {Type: Integer, Optional: true}},
			in:    map[string]any{"count": 5},
			want:  "collides with a declared parameter",
		},
		{
			name:  "two parameters cannot share one alias",
			props: Properties{"a": {Type: String, Optional: true, Alias: "s"}, "b": {Type: String, Optional: true, Alias: "s"}},
			in:    map[string]any{"s": "x"},
			want:  `alias "s" is claimed by both`,
		},
		{
			name:  "a self alias is not a caller conflict",
			props: Properties{"v": {Type: String, Alias: "v"}},
			in:    map[string]any{"v": "x"},
			want:  "is its own alias",
		},
		{
			name:  "a required parameter carrying a default is not a missing parameter",
			props: Properties{"v": {Type: String, Default: "x"}},
			in:    map[string]any{},
			want:  "is required but declares a default",
		},
		{
			name:  "an unreachable enum is not the caller's fault",
			props: Properties{"size": {Type: String, Format: "disk-size", Enum: []string{"1T"}}},
			in:    map[string]any{"size": "1T"},
			want:  "could never match",
		},
		{
			name:  "an items schema on a non-array",
			props: Properties{"v": {Type: String, Items: &Property{Type: String}}},
			in:    map[string]any{"v": "x"},
			want:  "declares an items schema but is string",
		},
		{
			name:  "an array with no items schema",
			props: Properties{"v": {Type: Array}},
			in:    map[string]any{"v": []any{"x"}},
			want:  "declares no items schema",
		},
		{
			name:  "an empty parameter name",
			props: Properties{"": {Type: String}},
			in:    map[string]any{},
			want:  "empty parameter name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Compile must reject it too — that is the whole point of
			// running it at startup.
			if err := tc.props.Compile(); err == nil {
				t.Error("Compile() accepted a malformed schema")
			}

			_, err := tc.props.Validate(tc.in)
			if err == nil {
				t.Fatalf("Validate() = nil, want an error containing %q", tc.want)
			}
			var ve *ValidationError
			if errors.As(err, &ve) {
				t.Errorf("Validate() returned a caller-facing error for a schema bug: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestValidateRejectsUnregisteredFormatAtRequestTime(t *testing.T) {
	props := Properties{"v": {Type: String, Format: "no-such-format"}}
	_, err := props.Validate(map[string]any{"v": "x"})
	if err == nil {
		t.Fatal("expected an error")
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		t.Fatalf("an unregistered format is a schema bug, not a caller error: %v", err)
	}
	if !strings.Contains(err.Error(), "unregistered format") {
		t.Errorf("error = %v", err)
	}
}

func TestValidateIsDeterministic(t *testing.T) {
	// Two parameters are wrong at once; the reported one must not depend
	// on map iteration order.
	props := Properties{
		"alpha": {Type: Integer},
		"beta":  {Type: Integer},
	}
	for i := 0; i < 50; i++ {
		_, err := props.Validate(map[string]any{"alpha": "x", "beta": "y"})
		wantValidationError(t, err, "alpha", "expected an integer")
	}
}

// TestCoerceGoNativeNumericTypes covers the widths a caller can hand in
// when it builds the map itself rather than decoding JSON — which is what
// the path-parameter and query-string extractors upstream will do.
func TestCoerceGoNativeNumericTypes(t *testing.T) {
	values := []any{
		int(7), int8(7), int16(7), int32(7), int64(7),
		uint(7), uint8(7), uint16(7), uint32(7), uint64(7),
		float32(7), float64(7), json.Number("7"),
	}

	for _, v := range values {
		t.Run(fmt.Sprintf("%T", v), func(t *testing.T) {
			p := mustValidate(t, Properties{
				"i": {Type: Integer},
				"f": {Type: Number},
				"s": {Type: String},
			}, map[string]any{"i": v, "f": v, "s": v})

			if got := p.Int("i"); got != 7 {
				t.Errorf("Int = %d, want 7", got)
			}
			if got := p.Float("f"); got != 7 {
				t.Errorf("Float = %v, want 7", got)
			}
			if got := p.String("s"); got != "7" {
				t.Errorf("String = %q, want \"7\"", got)
			}
		})
	}

	t.Run("booleans from every width", func(t *testing.T) {
		for _, v := range []any{int8(1), uint16(1), float32(1), json.Number("1")} {
			p := mustValidate(t, Properties{"b": {Type: Boolean}}, map[string]any{"b": v})
			if !p.Bool("b") {
				t.Errorf("%T(1) did not coerce to true", v)
			}
		}
	})

	t.Run("an unsigned value past int64 is out of range", func(t *testing.T) {
		_, err := Properties{"i": {Type: Integer}}.Validate(map[string]any{"i": uint64(1) << 63})
		wantValidationError(t, err, "i", "out of range")
	})

	t.Run("a non-finite number is not a number", func(t *testing.T) {
		_, err := Properties{"f": {Type: Number}}.Validate(map[string]any{"f": math.Inf(1)})
		wantValidationError(t, err, "f", "expected a number")

		_, err = Properties{"f": {Type: Number}}.Validate(map[string]any{"f": "NaN"})
		wantValidationError(t, err, "f", "expected a number")
	})

	t.Run("unsupported shapes are rejected per type", func(t *testing.T) {
		type custom struct{ A int }
		for _, tc := range []struct {
			prop Property
			want string
		}{
			{Property{Type: String}, "expected a string"},
			{Property{Type: Integer}, "expected an integer"},
			{Property{Type: Number}, "expected a number"},
			{Property{Type: Boolean}, "expected a boolean"},
		} {
			_, err := Properties{"v": tc.prop}.Validate(map[string]any{"v": custom{A: 1}})
			wantValidationError(t, err, "v", tc.want)
		}
	})

	t.Run("strings of arrays render their elements", func(t *testing.T) {
		props := Properties{"v": {Type: Array, Items: &Property{Type: Boolean}}}
		p := mustValidate(t, props, map[string]any{"v": []any{true, "off"}})
		if got := strings.Join(p.Strings("v"), ","); got != "true,false" {
			t.Errorf("Strings = %q, want \"true,false\"", got)
		}
	})
}
