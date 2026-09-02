package virtiowin

import "testing"

func TestISOVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{"release suffix is dropped", "0.1.302-1", "0.1.302"},
		{"second respin", "0.1.262-2", "0.1.262"},
		{"no suffix is left as-is", "0.1.96", "0.1.96"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ISOVersion(tt.version); got != tt.want {
				t.Errorf("ISOVersion(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

func TestISOFilename(t *testing.T) {
	if got, want := ISOFilename("0.1.302-1"), "virtio-win-0.1.302.iso"; got != want {
		t.Errorf("ISOFilename = %q, want %q", got, want)
	}
}

// Lexical ordering gets this wrong: "0.1.96" > "0.1.302" as strings, but 96 is
// an older release than 302.
func TestCompare(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want int
	}{
		{"numeric not lexical", "0.1.96", "0.1.302", -1},
		{"numeric not lexical, reversed", "0.1.302", "0.1.96", 1},
		{"equal", "0.1.302-1", "0.1.302-1", 0},
		{"release suffix breaks the tie", "0.1.262-2", "0.1.262-1", 1},
		{"absent suffix reads as release 0", "0.1.262", "0.1.262-1", -1},
		{"suffix ignored when base differs", "0.1.271-1", "0.1.285-1", -1},
		{"differing component counts", "0.1", "0.1.0", 0},
		{"major beats minor", "1.0.0", "0.9.9", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Compare(tt.a, tt.b); got != tt.want {
				t.Errorf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestParseVersionFromPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			name: "http redirect Location from upstream",
			path: "http://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.302-1/",
			want: "0.1.302-1",
		},
		{"bare autoindex href", "virtio-win-0.1.285-1/", "0.1.285-1"},
		{"no trailing slash", "virtio-win-0.1.96", "0.1.96"},
		{"unrelated href", "?C=N;O=D", ""},
		{"parent link", "/groups/virt/", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseVersionFromPath(tt.path); got != tt.want {
				t.Errorf("ParseVersionFromPath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

func TestValidVersion(t *testing.T) {
	valid := []string{"0.1.302-1", "0.1.96", "1.2.3.4", "0.1.262-2"}
	invalid := []string{"", "latest", "0.1.302-", "-1", "0.1.302/x", "../0.1.302", "0.1.302 "}
	for _, v := range valid {
		if !ValidVersion(v) {
			t.Errorf("ValidVersion(%q) = false, want true", v)
		}
	}
	for _, v := range invalid {
		if ValidVersion(v) {
			t.Errorf("ValidVersion(%q) = true, want false", v)
		}
	}
}
