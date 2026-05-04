package scanner

import "testing"

func TestDebianReleaseFromPVEVersion(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"pve-manager/8.2.4/abc123def456", "bookworm"},
		{"pve-manager/9.0.0/aaa", "trixie"},
		{"pve-manager/7.4-3/xyz", "bullseye"},
		{"pve-manager/6.4-15/aaa", "buster"},
		{"8.2.4", "bookworm"},
		{"7.4-3", "bullseye"},
		{"", ""},
		{"pve-manager/garbage", ""},
		{"pve-manager/12/foo", ""},
	}
	for _, c := range cases {
		got := DebianReleaseFromPVEVersion(c.in)
		if got != c.want {
			t.Errorf("DebianReleaseFromPVEVersion(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCompareDebianVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0", "1.0", 0},
		{"1.0", "2.0", -1},
		{"2.0", "1.0", 1},
		{"1.10", "1.9", 1},
		{"1.0-1", "1.0-2", -1},
		{"1.0-2", "1.0-1", 1},
		{"1:1.0", "2.0", 1},                   // epoch wins
		{"2.0", "1:1.0", -1},                  // epoch wins
		{"1.0~rc1", "1.0", -1},                // tilde sorts before empty
		{"1.0", "1.0~rc1", 1},                 // tilde sorts before empty
		{"1.0a", "1.0", 1},                    // letter sorts after empty
		{"3.1.7-1+deb12u1", "3.1.7-1", 1},     // revision suffix
		{"2.4.4-1", "2.4.3-1", 1},             // upstream wins over revision
		{"3.1.10-1", "3.1.9-2", 1},            // numeric: 10 > 9
		{"0.10", "0.9", 1},                    // numeric segments
		{"1.0-1+deb12u1", "1.0-1+deb12u2", -1}, // revision string compare
	}
	for _, c := range cases {
		got, ok := compareDebianVersion(c.a, c.b)
		if !ok {
			t.Errorf("compareDebianVersion(%q, %q) failed to parse", c.a, c.b)
			continue
		}
		if got != c.want {
			t.Errorf("compareDebianVersion(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestBestSeverity(t *testing.T) {
	cases := []struct {
		name     string
		releases map[string]debianRelease
		want     string
	}{
		{
			name: "highest wins",
			releases: map[string]debianRelease{
				"bookworm": {Urgency: "low"},
				"bullseye": {Urgency: "high"},
				"trixie":   {Urgency: "medium"},
			},
			want: "high",
		},
		{
			name:     "all empty",
			releases: map[string]debianRelease{"bookworm": {Urgency: ""}},
			want:     "unknown",
		},
		{
			name: "not yet assigned",
			releases: map[string]debianRelease{
				"bookworm": {Urgency: "not yet assigned"},
				"bullseye": {Urgency: "low"},
			},
			want: "low",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bestSeverity(c.releases)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestPickReleaseAndOpenStatus(t *testing.T) {
	releases := map[string]debianRelease{
		"bookworm": {Status: "resolved", Urgency: "medium", FixedVersion: "2.0"},
		"bullseye": {Status: "open", Urgency: "high"},
	}

	r, used := pickRelease(releases, "")
	if used {
		t.Errorf("expected used=false for empty release")
	}
	_ = r

	r, used = pickRelease(releases, "bookworm")
	if !used || r.Status != "resolved" {
		t.Errorf("expected bookworm resolved entry, got used=%v status=%q", used, r.Status)
	}

	r, used = pickRelease(releases, "missing")
	if !used || r.Status != "" {
		t.Errorf("expected used=true with empty entry for missing release")
	}

	if !hasOpenRelease(releases) {
		t.Errorf("expected open release present")
	}
	if hasOpenRelease(map[string]debianRelease{"x": {Status: "resolved"}}) {
		t.Errorf("expected no open release")
	}
}
