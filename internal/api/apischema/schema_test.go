package apischema

import "testing"

func TestResolveSource(t *testing.T) {
	pathParams := []string{"cluster_id", "vm_id"}

	cases := []struct {
		name   string
		param  string
		prop   Property
		method string
		want   Source
	}{
		{"explicit source wins", "cluster_id", Property{Type: String, Source: SourceQuery}, "POST", SourceQuery},
		{"route placeholder is a path param", "cluster_id", Property{Type: String}, "POST", SourcePath},
		{"route placeholder on a GET", "vm_id", Property{Type: String}, "GET", SourcePath},
		{"post body", "size", Property{Type: String}, "POST", SourceBody},
		{"put body", "size", Property{Type: String}, "PUT", SourceBody},
		{"patch body", "size", Property{Type: String}, "PATCH", SourceBody},
		{"get query", "limit", Property{Type: Integer}, "GET", SourceQuery},
		{"delete query", "purge", Property{Type: Boolean}, "DELETE", SourceQuery},
		{"head query", "limit", Property{Type: Integer}, "HEAD", SourceQuery},
		{"lowercase method", "limit", Property{Type: Integer}, "get", SourceQuery},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveSource(tc.param, tc.prop, tc.method, pathParams); got != tc.want {
				t.Errorf("ResolveSource(%q, %s) = %q, want %q", tc.param, tc.method, got, tc.want)
			}
		})
	}
}

func TestAsOptionalDoesNotMutateTheOriginal(t *testing.T) {
	base := Property{Type: String, Format: "node-name", Enum: []string{"a"}}
	opt := base.AsOptional()

	if !opt.Optional {
		t.Error("AsOptional did not set Optional")
	}
	if base.Optional {
		t.Error("AsOptional mutated the receiver")
	}
	opt.Enum[0] = "mutated"
	if base.Enum[0] != "a" {
		t.Error("AsOptional returned a Property sharing the original's enum slice")
	}
}

func TestCloneIsDeep(t *testing.T) {
	item := Property{Type: String}
	base := Property{
		Type:      Array,
		Items:     &item,
		Minimum:   Ptr(1.0),
		Maximum:   Ptr(2.0),
		MinLength: Ptr(1),
		MaxLength: Ptr(2),
		Enum:      nil,
		Requires:  []string{"other"},
	}
	c := base.clone()

	c.Items.Type = Integer
	c.Requires[0] = "changed"
	*c.Minimum = 99
	*c.MaxLength = 99

	if base.Items.Type != String {
		t.Error("clone shares the items schema")
	}
	if base.Requires[0] != "other" {
		t.Error("clone shares the requires slice")
	}
	if *base.Minimum != 1 {
		t.Error("clone shares the minimum pointer")
	}
	if *base.MaxLength != 2 {
		t.Error("clone shares the maxlength pointer")
	}
}

func TestPtr(t *testing.T) {
	if got := Ptr(1.5); *got != 1.5 {
		t.Errorf("Ptr(1.5) = %v", *got)
	}
	if got := Ptr(3); *got != 3 {
		t.Errorf("Ptr(3) = %v", *got)
	}
}
