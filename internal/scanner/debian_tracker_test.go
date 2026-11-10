package scanner

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"testing"
	"time"
)

// TestLookupPackageUpdates holds the tracker match to a Debian binary's source
// package and a release's entry to what it means. The tracker is keyed by
// SOURCE, so the binary name alone found no CVE of libssl3 (built from
// openssl); a resolved entry whose fixed_version is "0" (not affected) or
// empty was reported as if the node were vulnerable; and an entry still open
// in the node's release showed another release's fixed version, or the
// pending one, as Fixed In.
func TestLookupPackageUpdates(t *testing.T) {
	t.Parallel()
	cve := func(status, fixed string) map[string]debianCVEEntry {
		return map[string]debianCVEEntry{"CVE-2026-0001": {Releases: map[string]debianRelease{
			"bookworm": {Status: status, FixedVersion: fixed, Urgency: "high"},
		}}}
	}
	libfoo := AptUpdateInfo{Package: "libfoo1", SourcePackage: "foo", Origin: "Debian", OldVersion: "1.0-1", NewVersion: "1.0-2"}
	proxmoxBuild := libfoo
	proxmoxBuild.Origin = "Proxmox"
	cases := []struct {
		name    string
		tracker map[string]map[string]debianCVEEntry
		upd     AptUpdateInfo
		want    []string // "package cve fixed in"
	}{
		{
			name:    "a binary is found under its source package",
			tracker: map[string]map[string]debianCVEEntry{"openssl": cve("open", "")},
			upd:     AptUpdateInfo{Package: "libssl3", SourcePackage: "openssl", Origin: "Debian", OldVersion: "3.0.1-1"},
			want:    []string{`libssl3 CVE-2026-0001 fixed in ""`},
		},
		{
			name:    "the binary name when the source is not in the tracker",
			tracker: map[string]map[string]debianCVEEntry{"libfoo1": cve("open", "")},
			upd:     libfoo,
			want:    []string{`libfoo1 CVE-2026-0001 fixed in ""`},
		},
		{
			name: "open in the node's release has no fixed version, though another release has one",
			tracker: map[string]map[string]debianCVEEntry{"foo": {"CVE-2026-0001": {Releases: map[string]debianRelease{
				"bookworm": {Status: "open", Urgency: "high"},
				"trixie":   {Status: "resolved", FixedVersion: "1.1-1", Urgency: "high"},
			}}}},
			upd:  libfoo,
			want: []string{`libfoo1 CVE-2026-0001 fixed in ""`},
		},
		{
			name:    "not Debian's kernel, which no Proxmox node runs",
			tracker: map[string]map[string]debianCVEEntry{"linux": cve("open", "")},
			upd:     AptUpdateInfo{Package: "linux-libc-dev", SourcePackage: "linux", Origin: "Debian", OldVersion: "6.1.1-1"},
		},
		{
			name:    "not a Proxmox build, which Debian's release status does not describe",
			tracker: map[string]map[string]debianCVEEntry{"foo": cve("open", "")},
			upd:     proxmoxBuild,
		},
		{name: "resolved at 0 is not affected", tracker: map[string]map[string]debianCVEEntry{"foo": cve("resolved", "0")}, upd: libfoo},
		{name: "resolved with no fixed version", tracker: map[string]map[string]debianCVEEntry{"foo": cve("resolved", "")}, upd: libfoo},
		{
			name:    "resolved above the installed version is still open on the node",
			tracker: map[string]map[string]debianCVEEntry{"foo": cve("resolved", "1.0-2")},
			upd:     libfoo,
			want:    []string{`libfoo1 CVE-2026-0001 fixed in "1.0-2"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := NewCVEClient(nil, nil, slog.New(slog.DiscardHandler))
			c.trackerData, c.trackerLoadedAt = tc.tracker, time.Now()
			vulns, err := c.LookupPackageUpdates(context.Background(), []AptUpdateInfo{tc.upd}, "bookworm")
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, v := range vulns {
				got = append(got, fmt.Sprintf("%s %s fixed in %q", v.PackageName, v.CVEID, v.FixedVersion))
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

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
		{"1:1.0", "2.0", 1},                    // epoch wins
		{"2.0", "1:1.0", -1},                   // epoch wins
		{"1.0~rc1", "1.0", -1},                 // tilde sorts before empty
		{"1.0", "1.0~rc1", 1},                  // tilde sorts before empty
		{"1.0a", "1.0", 1},                     // letter sorts after empty
		{"3.1.7-1+deb12u1", "3.1.7-1", 1},      // revision suffix
		{"2.4.4-1", "2.4.3-1", 1},              // upstream wins over revision
		{"3.1.10-1", "3.1.9-2", 1},             // numeric: 10 > 9
		{"0.10", "0.9", 1},                     // numeric segments
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
