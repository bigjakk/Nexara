package apischema

import (
	"fmt"
	"strings"
	"testing"
)

// mustPanic asserts that fn panics with a message containing want.
func mustPanic(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected a panic containing %q, got none", want)
			return
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, want) {
			t.Errorf("panic message %q does not contain %q", msg, want)
		}
	}()
	fn()
}

// mustValidate validates in against props and fails on error.
func mustValidate(t *testing.T, props Properties, in map[string]any) *Params {
	t.Helper()
	p, err := props.Validate(in)
	if err != nil {
		t.Fatalf("Validate(%v): unexpected error: %v", in, err)
	}
	return p
}

// threeStateProps covers the three ways a parameter can reach a handler.
func threeStateProps() Properties {
	return Properties{
		// Required.
		"storage": {Type: String, Format: "storage-id"},
		// Optional with a default.
		"bus": {Type: String, Optional: true, Default: "scsi", Enum: []string{"ide", "sata", "scsi", "virtio"}},
		// Optional with NO default: the shape the disk-attach incident
		// needed, where 0 is a legal value and "omitted" is not 0.
		"index":  {Type: Integer, Optional: true},
		"cache":  {Type: Boolean, Optional: true, Default: true},
		"ratio":  {Type: Number, Optional: true, Default: 1.5},
		"weight": {Type: Number, Optional: true},
		"tags":   {Type: Array, Optional: true, Items: &Property{Type: String}},
	}
}

// TestThreeStateOptionalReads is the highest-value test in the package:
// it pins the distinction a hand-rolled zero-value check cannot make.
func TestThreeStateOptionalReads(t *testing.T) {
	props := threeStateProps()

	t.Run("defaulted parameter reads as its default but was not supplied", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"storage": "store01"})

		if got := p.String("bus"); got != "scsi" {
			t.Errorf(`String("bus") = %q, want the default "scsi"`, got)
		}
		if p.Has("bus") {
			t.Error(`Has("bus") = true, want false: the caller omitted it`)
		}
		if got, supplied := p.OptString("bus"); got != "scsi" || supplied {
			t.Errorf(`OptString("bus") = (%q, %v), want ("scsi", false)`, got, supplied)
		}
		if got, supplied := p.OptBool("cache"); got != true || supplied {
			t.Errorf(`OptBool("cache") = (%v, %v), want (true, false)`, got, supplied)
		}
		if got, supplied := p.OptFloat("ratio"); got != 1.5 || supplied {
			t.Errorf(`OptFloat("ratio") = (%v, %v), want (1.5, false)`, got, supplied)
		}
	})

	t.Run("optional parameter without a default reads as its zero value", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"storage": "store01"})

		if got := p.Int("index"); got != 0 {
			t.Errorf(`Int("index") = %d, want 0`, got)
		}
		if p.Has("index") {
			t.Error(`Has("index") = true, want false`)
		}
		if got, supplied := p.OptInt("index"); got != 0 || supplied {
			t.Errorf(`OptInt("index") = (%d, %v), want (0, false)`, got, supplied)
		}
		if got, supplied := p.OptFloat("weight"); got != 0 || supplied {
			t.Errorf(`OptFloat("weight") = (%v, %v), want (0, false)`, got, supplied)
		}
		if got := p.Strings("tags"); got != nil {
			t.Errorf(`Strings("tags") = %v, want nil`, got)
		}
	})

	t.Run("an explicit zero is distinguishable from an omitted one", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"storage": "store01", "index": 0})

		if got := p.Int("index"); got != 0 {
			t.Errorf(`Int("index") = %d, want 0`, got)
		}
		if !p.Has("index") {
			t.Error(`Has("index") = false, want true: the caller sent 0 explicitly`)
		}
		if got, supplied := p.OptInt("index"); got != 0 || !supplied {
			t.Errorf(`OptInt("index") = (%d, %v), want (0, true)`, got, supplied)
		}
	})

	t.Run("a supplied value overrides the default and is marked supplied", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"storage": "store01", "bus": "virtio", "cache": false})

		if got, supplied := p.OptString("bus"); got != "virtio" || !supplied {
			t.Errorf(`OptString("bus") = (%q, %v), want ("virtio", true)`, got, supplied)
		}
		if got, supplied := p.OptBool("cache"); got != false || !supplied {
			t.Errorf(`OptBool("cache") = (%v, %v), want (false, true)`, got, supplied)
		}
		// A supplied value equal to the default still counts as supplied.
		p2 := mustValidate(t, props, map[string]any{"storage": "store01", "bus": "scsi"})
		if !p2.Has("bus") {
			t.Error(`Has("bus") = false after the caller sent the default value, want true`)
		}
	})

	t.Run("a null is treated as omitted", func(t *testing.T) {
		p := mustValidate(t, props, map[string]any{"storage": "store01", "bus": nil, "index": nil})
		if got := p.String("bus"); got != "scsi" || p.Has("bus") {
			t.Errorf(`String("bus") = %q, Has = %v; want the default and false`, got, p.Has("bus"))
		}
		if p.Has("index") {
			t.Error(`Has("index") = true for an explicit null, want false`)
		}
	})
}

func TestAccessorsReturnCoercedValues(t *testing.T) {
	props := Properties{
		"name":    {Type: String},
		"count":   {Type: Integer},
		"ratio":   {Type: Number},
		"enabled": {Type: Boolean},
		"tags":    {Type: Array, Items: &Property{Type: String}},
		"vmids":   {Type: Array, Items: &Property{Type: Integer}},
	}
	p := mustValidate(t, props, map[string]any{
		"name":    "linux01",
		"count":   float64(7),
		"ratio":   "2.5",
		"enabled": "yes",
		"tags":    []any{"a", "b"},
		"vmids":   []any{101, "102"},
	})

	if got := p.String("name"); got != "linux01" {
		t.Errorf(`String("name") = %q`, got)
	}
	if got := p.Int("count"); got != 7 {
		t.Errorf(`Int("count") = %d, want 7`, got)
	}
	if got := p.Float("count"); got != 7 {
		t.Errorf(`Float("count") = %v, want 7: Float reads integers too`, got)
	}
	if got := p.Float("ratio"); got != 2.5 {
		t.Errorf(`Float("ratio") = %v, want 2.5`, got)
	}
	if got := p.Bool("enabled"); !got {
		t.Error(`Bool("enabled") = false, want true`)
	}
	if got := strings.Join(p.Strings("tags"), ","); got != "a,b" {
		t.Errorf(`Strings("tags") = %q, want "a,b"`, got)
	}
	if got := strings.Join(p.Strings("vmids"), ","); got != "101,102" {
		t.Errorf(`Strings("vmids") = %q, want "101,102"`, got)
	}
}

func TestStringsReturnsAFreshSlice(t *testing.T) {
	props := Properties{"tags": {Type: Array, Items: &Property{Type: String}}}
	p := mustValidate(t, props, map[string]any{"tags": []any{"a", "b"}})

	first := p.Strings("tags")
	first[0] = "mutated"
	if second := p.Strings("tags"); second[0] != "a" {
		t.Errorf("Strings returned an aliased slice: second read = %v", second)
	}
}

func TestRawIncludesDefaultsAndIsACopy(t *testing.T) {
	props := threeStateProps()
	p := mustValidate(t, props, map[string]any{"storage": "store01", "index": 3})

	raw := p.Raw()
	if raw["storage"] != "store01" || raw["bus"] != "scsi" || raw["index"] != int64(3) {
		t.Errorf("Raw() = %#v, want the supplied values plus the defaults", raw)
	}
	if _, ok := raw["weight"]; ok {
		t.Error("Raw() contains an optional parameter that was never supplied or defaulted")
	}

	raw["storage"] = "tampered"
	if p.String("storage") != "store01" {
		t.Error("Raw() handed back the internal map: mutating it changed the params")
	}
}

func TestUndeclaredKeyPanics(t *testing.T) {
	props := Properties{"bus": {Type: String}, "size": {Type: String}}
	p := mustValidate(t, props, map[string]any{"bus": "scsi", "size": "1"})

	cases := map[string]func(){
		"String":    func() { p.String("siz") },
		"Int":       func() { p.Int("siz") },
		"Float":     func() { p.Float("siz") },
		"Bool":      func() { p.Bool("siz") },
		"Strings":   func() { p.Strings("siz") },
		"OptString": func() { p.OptString("siz") },
		"OptInt":    func() { p.OptInt("siz") },
		"OptFloat":  func() { p.OptFloat("siz") },
		"OptBool":   func() { p.OptBool("siz") },
		"Has":       func() { p.Has("siz") },
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			// The message names the key and lists what IS declared, so
			// the typo is obvious from the panic alone.
			mustPanic(t, `"siz"`, fn)
			mustPanic(t, "declared: bus, size", fn)
		})
	}
}

func TestWrongAccessorTypePanics(t *testing.T) {
	props := Properties{
		"name":    {Type: String},
		"index":   {Type: Integer},
		"ratio":   {Type: Number},
		"enabled": {Type: Boolean},
		"tags":    {Type: Array, Items: &Property{Type: String}},
	}
	p := mustValidate(t, props, map[string]any{
		"name": "n", "index": 1, "ratio": 1.5, "enabled": true, "tags": []any{"a"},
	})

	cases := []struct {
		name string
		want string
		fn   func()
	}{
		{"String on integer", "declared as integer, not string", func() { p.String("index") }},
		{"Int on string", "declared as string, not integer", func() { p.Int("name") }},
		{"Bool on string", "declared as string, not boolean", func() { p.Bool("name") }},
		{"Float on string", "declared as string, not number or integer", func() { p.Float("name") }},
		{"Strings on string", "declared as string, not array", func() { p.Strings("name") }},
		{"String on array", "declared as array, not string", func() { p.String("tags") }},
		{"OptInt on number", "declared as number, not integer", func() { p.OptInt("ratio") }},
		{"OptBool on integer", "declared as integer, not boolean", func() { p.OptBool("index") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { mustPanic(t, tc.want, tc.fn) })
	}
}

func TestEmptySchemaPanicMessage(t *testing.T) {
	props := Properties{}
	p := mustValidate(t, props, nil)
	mustPanic(t, "declared: none", func() { p.String("anything") })
}

// TestObjectDefaultIsNotSharedAcrossRequests pins the sharpest edge of a
// default: it is schema state handed to a request. An Object default
// passed by reference would let one handler's write into the map it was
// given edit the schema itself — permanently, for every later request,
// and concurrently with every request in flight.
func TestObjectDefaultIsNotSharedAcrossRequests(t *testing.T) {
	props := Properties{
		"meta": {Type: Object, Optional: true, Default: map[string]any{"replication": "off"}},
		"tags": {Type: Array, Optional: true, Items: &Property{Type: String}, Default: []any{"managed"}},
	}
	if err := props.Compile(); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	t.Run("a handler's mutation does not reach the next request", func(t *testing.T) {
		first := mustValidate(t, props, nil)
		meta, ok := first.Raw()["meta"].(map[string]any)
		if !ok {
			t.Fatalf(`Raw()["meta"] = %#v, want a map`, first.Raw()["meta"])
		}
		meta["replication"] = "tampered"
		meta["injected"] = true

		tags := first.Strings("tags")
		tags[0] = "tampered"

		second := mustValidate(t, props, nil)
		got, _ := second.Raw()["meta"].(map[string]any)
		if got["replication"] != "off" {
			t.Errorf(`second request sees replication=%v, want "off": the default is shared by reference`, got["replication"])
		}
		if _, injected := got["injected"]; injected {
			t.Error("second request sees a key the first request added to the default")
		}
		if v := second.Strings("tags"); len(v) != 1 || v[0] != "managed" {
			t.Errorf("second request sees tags %v, want [managed]", v)
		}
	})

	t.Run("concurrent requests do not share the default", func(t *testing.T) {
		// Under -race, a shared default map turns this into a reported
		// data race rather than a silent corruption.
		const workers = 8
		done := make(chan struct{})
		for i := range workers {
			go func(n int) {
				defer func() { done <- struct{}{} }()
				p, err := props.Validate(nil)
				if err != nil {
					t.Errorf("worker %d: %v", n, err)
					return
				}
				m, _ := p.Raw()["meta"].(map[string]any)
				m["replication"] = "worker"
				m["worker"] = n
			}(i)
		}
		for range workers {
			<-done
		}

		after := mustValidate(t, props, nil)
		m, _ := after.Raw()["meta"].(map[string]any)
		if len(m) != 1 || m["replication"] != "off" {
			t.Errorf("default is %#v after concurrent requests, want the original single key", m)
		}
	})

	t.Run("a standard option's default is not shared either", func(t *testing.T) {
		RegisterStdOption("apischema-test-object-default", Property{
			Type: Object, Optional: true, Default: map[string]any{"k": "v"},
		})
		a := StdOption("apischema-test-object-default")
		am, _ := a.Default.(map[string]any)
		am["k"] = "tampered"

		b := StdOption("apischema-test-object-default")
		bm, _ := b.Default.(map[string]any)
		if bm["k"] != "v" {
			t.Errorf("StdOption handed out a shared default: second copy reads %v", bm["k"])
		}
	})
}

// TestIntegerBoundsAreComparedAsIntegers covers the range where a float64
// comparison silently rounds: 2^53 and up, where a byte count or a
// nanosecond timestamp lives.
func TestIntegerBoundsAreComparedAsIntegers(t *testing.T) {
	const twoPow53 = 9007199254740992 // the last integer float64 counts exactly

	cases := []struct {
		name    string
		prop    Property
		in      any
		wantErr string
	}{
		{
			name:    "one past a maximum at 2^53",
			prop:    Property{Type: Integer, Maximum: Ptr(float64(twoPow53))},
			in:      "9007199254740993",
			wantErr: "must be at most 9007199254740992",
		},
		{
			name: "exactly the maximum at 2^53",
			prop: Property{Type: Integer, Maximum: Ptr(float64(twoPow53))},
			in:   "9007199254740992",
		},
		{
			name:    "one below a minimum at 2^53",
			prop:    Property{Type: Integer, Minimum: Ptr(float64(twoPow53))},
			in:      "9007199254740991",
			wantErr: "must be at least 9007199254740992",
		},
		{
			name: "a bound beyond int64 cannot be exceeded",
			prop: Property{Type: Integer, Maximum: Ptr(1e19)},
			in:   "9223372036854775807",
		},
		{
			name:    "a minimum beyond int64 can never be met",
			prop:    Property{Type: Integer, Minimum: Ptr(1e19)},
			in:      "9223372036854775807",
			wantErr: "must be at least",
		},
		{
			name: "a bound below int64 is always satisfied",
			prop: Property{Type: Integer, Minimum: Ptr(-1e19)},
			in:   "-9223372036854775808",
		},
		{
			name:    "a fractional minimum rounds up, not down",
			prop:    Property{Type: Integer, Minimum: Ptr(1.5)},
			in:      1,
			wantErr: "must be at least 1.5",
		},
		{
			name: "a fractional minimum admits the integer above it",
			prop: Property{Type: Integer, Minimum: Ptr(1.5)},
			in:   2,
		},
		{
			name:    "a fractional maximum rounds down, not up",
			prop:    Property{Type: Integer, Maximum: Ptr(1.5)},
			in:      2,
			wantErr: "must be at most 1.5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Properties{"v": tc.prop}.Validate(map[string]any{"v": tc.in})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			wantValidationError(t, err, "v", tc.wantErr)
		})
	}
}
