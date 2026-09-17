package apischema

import "testing"

func TestBuiltinStdOptions(t *testing.T) {
	cases := []struct {
		name       string
		wantType   Type
		wantFormat string
		wantSource Source
	}{
		{"cluster-id", String, "uuid", SourcePath},
		{"vm-id", String, "uuid", SourcePath},
		{"ct-id", String, "uuid", SourcePath},
		{"node-name", String, "node-name", SourceAuto},
		{"storage-id", String, "storage-id", SourceAuto},
		{"pbs-id", String, "uuid", SourceAuto},
		{"bwlimit", Integer, "", SourceAuto},
		{"fingerprint", String, "fingerprint-sha256", SourceAuto},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := StdOption(tc.name)
			if p.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", p.Type, tc.wantType)
			}
			if p.Format != tc.wantFormat {
				t.Errorf("Format = %q, want %q", p.Format, tc.wantFormat)
			}
			if p.Source != tc.wantSource {
				t.Errorf("Source = %q, want %q", p.Source, tc.wantSource)
			}
			if p.Description == "" {
				t.Error("standard options must carry a description; the API docs render it")
			}
			if err := (Properties{tc.name: p}).Compile(); err != nil {
				t.Errorf("standard option does not compile: %v", err)
			}
		})
	}
}

func TestStdOptionValidatesLikeItsFormat(t *testing.T) {
	props := Properties{
		"cluster_id": StdOption("cluster-id"),
		"node":       StdOption("node-name"),
		"bwlimit":    StdOption("bwlimit"),
	}
	if err := props.Compile(); err != nil {
		t.Fatalf("Compile: %v", err)
	}

	p := mustValidate(t, props, map[string]any{
		"cluster_id": "3F2504E0-4F89-11D3-9A0C-0305E82C3301",
		"node":       "pve-01",
		"bwlimit":    "10240",
	})
	if got := p.String("cluster_id"); got != "3f2504e0-4f89-11d3-9a0c-0305e82c3301" {
		t.Errorf("cluster_id = %q, want the lowercased UUID", got)
	}
	if got := p.Int("bwlimit"); got != 10240 {
		t.Errorf("bwlimit = %d, want 10240", got)
	}

	_, err := props.Validate(map[string]any{"cluster_id": "not-a-uuid", "node": "pve-01"})
	wantValidationError(t, err, "cluster_id", "expected a UUID")

	_, err = props.Validate(map[string]any{
		"cluster_id": "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		"node":       "pve-01",
		"bwlimit":    -1,
	})
	wantValidationError(t, err, "bwlimit", "must be at least 0")
}

func TestStdOptionIsIndependentPerCaller(t *testing.T) {
	a := StdOption("node-name")
	a.Description = "changed"
	a.Optional = true

	b := StdOption("node-name")
	if b.Description == "changed" || b.Optional {
		t.Error("StdOption handed out a shared Property: one route's override leaked into another")
	}
}

func TestRegisterStdOption(t *testing.T) {
	name := "apischema-test-option"
	RegisterStdOption(name, Property{Type: String, Description: "test"})

	if got := StdOption(name); got.Type != String {
		t.Errorf("Type = %q", got.Type)
	}
	mustPanic(t, "already registered", func() {
		RegisterStdOption(name, Property{Type: String})
	})
	mustPanic(t, "empty name", func() {
		RegisterStdOption("", Property{Type: String})
	})
	mustPanic(t, `unknown standard option "no-such-option"`, func() {
		StdOption("no-such-option")
	})
	// The panic lists what IS registered, so a typo is self-diagnosing.
	mustPanic(t, "cluster-id", func() { StdOption("cluster_id") })
}
