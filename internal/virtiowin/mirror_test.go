package virtiowin

import "testing"

func TestNormalizeBase(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty means upstream", in: "", want: ""},
		{name: "whitespace is trimmed to empty", in: "   ", want: ""},
		{name: "trailing slash removed", in: "https://mirror.internal/virtio/", want: "https://mirror.internal/virtio"},
		// Every trailing slash, not just the last one: callers append a path to
		// this result without trimming again, so one survivor builds "//…".
		{name: "several trailing slashes removed", in: "https://mirror.internal/virtio////", want: "https://mirror.internal/virtio"},
		{name: "root path of slashes removed", in: "https://mirror.internal//", want: "https://mirror.internal"},
		// Percent-encoded slashes decode into the path, so they have to be
		// trimmed on the decoded form and RawPath dropped with it.
		{name: "encoded trailing slash removed", in: "https://mirror.internal/virtio%2f", want: "https://mirror.internal/virtio"},
		{name: "bare host kept", in: "https://mirror.internal", want: "https://mirror.internal"},
		{name: "plain http allowed here", in: "http://10.0.0.5/isos", want: "http://10.0.0.5/isos"},
		// The handler decides whether to demand the insecure confirmation by
		// prefix-matching "http://" on this output, so the scheme has to come
		// back lowercased however it was typed.
		{name: "scheme is normalised to lower case", in: "HTTP://10.0.0.5/isos", want: "http://10.0.0.5/isos"},
		{name: "https likewise", in: "HTTPS://Mirror.Internal/x", want: "https://Mirror.Internal/x"},
		{name: "port kept", in: "http://10.0.0.5:8080/isos", want: "http://10.0.0.5:8080/isos"},
		{name: "scheme required", in: "mirror.internal/virtio", wantErr: true},
		{name: "ftp rejected", in: "ftp://mirror.internal/virtio", wantErr: true},
		{name: "file rejected", in: "file:///srv/isos", wantErr: true},
		{name: "credentials rejected", in: "https://user:pw@mirror.internal", wantErr: true},
		{name: "query rejected", in: "https://mirror.internal/virtio?token=x", wantErr: true},
		{name: "fragment rejected", in: "https://mirror.internal/virtio#frag", wantErr: true},
		// A bare "?" leaves RawQuery empty but ForceQuery set, so it survives
		// the RawQuery check alone and turns the appended path into a query.
		{name: "bare question mark rejected", in: "https://mirror.internal/virtio?", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeBase(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeBase(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("NormalizeBase(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestBuildISOURLFrom pins the two things a mirror URL has to get right: the
// upstream directory layout below the root, and the release-suffix split that
// 404s every download when it is wrong (dir "…-0.1.302-1", file "…-0.1.302.iso").
func TestBuildISOURLFrom(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		version string
		want    string
		wantErr bool
	}{
		{
			name: "mirror keeps the upstream layout", base: "https://mirror.internal/virtio", version: "0.1.302-1",
			want: "https://mirror.internal/virtio/archive-virtio/virtio-win-0.1.302-1/virtio-win-0.1.302.iso",
		},
		{
			name: "trailing slash on the base does not double up", base: "https://mirror.internal/virtio/", version: "0.1.302-1",
			want: "https://mirror.internal/virtio/archive-virtio/virtio-win-0.1.302-1/virtio-win-0.1.302.iso",
		},
		{
			name: "no release suffix repeats the version", base: "http://10.0.0.5", version: "0.1.271",
			want: "http://10.0.0.5/archive-virtio/virtio-win-0.1.271/virtio-win-0.1.271.iso",
		},
		{
			// Spelled out rather than built from BaseURL: this is the one case
			// that would catch a wrong host in the constant itself.
			name: "empty base falls back to upstream", base: "", version: "0.1.302-1",
			want: "https://fedorapeople.org/groups/virt/virtio-win/direct-downloads" +
				"/archive-virtio/virtio-win-0.1.302-1/virtio-win-0.1.302.iso",
		},
		{name: "invalid version refuses to build", base: "https://mirror.internal", version: "../../etc", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildISOURLFrom(tt.base, tt.version)
			if (err != nil) != tt.wantErr {
				t.Fatalf("BuildISOURLFrom(%q, %q) error = %v, wantErr %v", tt.base, tt.version, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("BuildISOURLFrom(%q, %q) = %q, want %q", tt.base, tt.version, got, tt.want)
			}
		})
	}
}

// TestEffectiveBase pins the resolution rule ResolveBase and the mirror
// endpoint now share. The endpoint reaches it without a database, so this is
// the only place the "malformed falls back to upstream" contract is stated
// against the pure function rather than through the engine.
func TestEffectiveBase(t *testing.T) {
	tests := []struct {
		name   string
		stored string
		want   string
	}{
		{"unset follows upstream", "", BaseURL},
		{"whitespace only follows upstream", "   ", BaseURL},
		{"malformed falls back to upstream", "not a url", BaseURL},
		{"wrong scheme falls back to upstream", "ftp://mirror.invalid", BaseURL},
		{"no host falls back to upstream", "https://", BaseURL},
		{"credentials fall back to upstream", "https://u:p@mirror.invalid", BaseURL},
		{"query falls back to upstream", "https://mirror.invalid/?x=1", BaseURL},
		{"usable base is normalized", "https://mirror.invalid/pub///", "https://mirror.invalid/pub"},
		{"usable base is returned as-is", "http://10.0.0.5:8080/virt", "http://10.0.0.5:8080/virt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveBase(tt.stored); got != tt.want {
				t.Errorf("EffectiveBase(%q) = %q, want %q", tt.stored, got, tt.want)
			}
		})
	}
}
