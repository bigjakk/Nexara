package pvefield

import (
	"cmp"
	"encoding/json"
	"maps"
	"os"
	"regexp"
	"slices"
	"testing"
)

// A field sent is changed when Proxmox stores it otherwise than it was read,
// as the observer compares the two: a set or a member list in any order, a
// number as its value, a property string by its keys, each kind's own
// read-back; and a field the reading leaves out is absent, with no default
// but where Proxmox writes none.
func TestKindChanged(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		kind   Kind
		field  string
		before string // the field as read; "" absent
		sent   string
		also   map[string]string
		want   bool
	}{
		{"a zone's nodes, a set", SDNZone, "nodes", `"pve-02,pve-01"`, "pve-01;pve-02", nil, false},
		{"a controller's peers, not one", SDNController, "peers", `"192.0.2.21,192.0.2.22"`, "192.0.2.22,192.0.2.21", nil, true},
		{"an integer numified", SDNZone, "mtu", `1450`, "01450", nil, false},
		{"an integer changed", SDNZone, "mtu", `1450`, "1500", nil, true},
		{"a MAC's case kept", SDNZone, "mac", `"02:00:00:00:00:aa"`, "02:00:00:00:00:AA", nil, true},
		{"a DHCP range by its keys", SDNSubnet, "dhcp-range", `[{"start-address":"192.0.2.10","end-address":"192.0.2.20"}]`,
			"start-address=192.0.2.10,end-address=192.0.2.20", nil, false},
		{"a DHCP range moved", SDNSubnet, "dhcp-range", `[{"start-address":"192.0.2.10","end-address":"192.0.2.20"}]`,
			"start-address=192.0.2.10,end-address=192.0.2.30", nil, true},
		{"a job's enabled, no default", Backup, "enabled", "", "1", nil, true},
		{"a job's guests, members", Backup, "vmid", `"100,101"`, "101 100", nil, false},
		{"autostart off, left out", Network, "autostart", "", "0", nil, false},
		{"autostart on", Network, "autostart", "", "1", nil, true},
		{"an OVS port's autostart, an auto line whatever was sent", Network, "autostart", `1`, "0", map[string]string{"type": "OVSPort"}, false},
		{"comments, a line each", Network, "comments", `"uplink\n"`, "uplink  ", nil, false},
		{"comments with comments6, one", Network, "comments", `"uplink\nv6\n"`, "uplink", map[string]string{"comments6": "v6"}, false},
		{"ports, sorted once each", Network, "bridge_ports", `"eno1 eno2"`, "eno2,eno1;none eno1", nil, false},
		{"a dotted netmask, its length", Network, "netmask", `"24"`, "255.255.255.0", nil, false},
		{"STP yes, on", Network, "bridge_stp", `"on"`, "YES", nil, false},
		{"text as sent", SDNVNet, "alias", `"lab"`, "lab2", nil, true},
		{"a field the observer drops", Network, "priority", `"3"`, "4", nil, false},
		{"a plugin's nodes, a set", ACMEPlugin, "nodes", `"pve-01,pve-02"`, "pve-02,pve-01", nil, false},
		{"a storage's content, a set", Storage, "content", `"images,iso"`, "iso,images", nil, false},
		// comments6 is read back folded into comments, and compared there.
		{"comments6 re-sent, as read", Network, "comments6", `"uplink\nv6\n"`, "v6", map[string]string{"comments": "uplink"}, false},
		{"comments6 changed", Network, "comments6", `"uplink\nv6\n"`, "v6b", map[string]string{"comments": "uplink"}, true},
	} {
		before := map[string]json.RawMessage{}
		if tc.before != "" {
			before[cmp.Or(tc.kind.ReadIn[tc.field], tc.field)] = json.RawMessage(tc.before)
		}
		update := map[string]string{tc.field: tc.sent}
		for k, v := range tc.also {
			update[k] = v
		}
		if got := tc.kind.Changed(tc.field, before, update); got != tc.want {
			t.Errorf("%s: changed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// A user, token or grantee keeps every name Proxmox takes for one, but no
// control or format character.
func TestMemberIDs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		list string
		ok   bool
	}{
		{`["o'brien@ad"]`, true}, {`["o'brien@ad!ci01"]`, true}, {`["PVEAuditor:1:o'brien@ad"]`, true}, {`["ünal@ad"]`, true},
		{`["o'brien"]`, false}, {`["x\u200b@pve"]`, false}, {`["a\u0007b@pve"]`, false},
	} {
		if _, ok := MemberIDs(json.RawMessage(tc.list)); ok != tc.ok {
			t.Errorf("member ids %s: kept %v, want %v", tc.list, ok, tc.ok)
		}
	}
}

// split_list splits on NUL alone when the list holds one, and drops the empty
// fields at its end.
func TestSplitList(t *testing.T) {
	t.Parallel()
	for in, want := range map[string][]string{
		"a, b;c\td":    {"a", "b", "c", "d"},
		" a,,b ":       {"a", "b"},
		"a b\x00c\x00": {"a b", "c"},
	} {
		if got := SplitList(in); !slices.Equal(got, want) {
			t.Errorf("SplitList(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestKindMoved names what two readings of one object differ in, each field
// compared as the observer compares it.
func TestKindMoved(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		kind          Kind
		before, after string
		want          []string
	}{
		{"a set in another order", HAGroup, `{"nodes":"pve-01,pve-02"}`, `{"nodes":"pve-02,pve-01"}`, []string{}},
		{"members in another order", HARule, `{"resources":"vm:101,vm:100"}`, `{"resources":"vm:100,vm:101"}`, []string{}},
		{"a number read as text", QemuConfig, `{"memory":2048}`, `{"memory":"2048"}`, []string{}},
		{"what is ignored, and the digest", HARule, `{"order":1,"digest":"a"}`, `{"order":2,"digest":"b"}`, []string{}},
		{"one changed, one appeared, one gone", QemuConfig, `{"memory":2048,"scsi1":"x"}`, `{"memory":4096,"unused0":"x"}`,
			[]string{"memory", "scsi1", "unused0"}},
	} {
		var before, after map[string]json.RawMessage
		_ = json.Unmarshal([]byte(tc.before), &before)
		_ = json.Unmarshal([]byte(tc.after), &after)
		if got := tc.kind.Moved(before, after); !slices.Equal(got, tc.want) || got == nil {
			t.Errorf("%s: moved %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSecretParamsAreTheScrubs holds the list to the names 000127 scrubbed from
// the rows kept before it: a name added to one and not the other is a secret
// either kept in old rows or redacted under a name no scrub reached.
func TestSecretParamsAreTheScrubs(t *testing.T) {
	t.Parallel()
	sql, err := os.ReadFile("../../migrations/000127_redact_audit_secrets.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	arrays := regexp.MustCompile(`ARRAY\[('[^\]]*)\]`).FindAllSubmatch(sql, -1)
	if len(arrays) != 2 {
		t.Fatalf("found %d name lists in 000127, want the two of its fields statement", len(arrays))
	}
	for _, a := range arrays {
		names := regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(string(a[1]), -1)
		got := make([]string, 0, len(names))
		for _, n := range names {
			got = append(got, n[1])
		}
		if want := slices.Sorted(maps.Keys(secretParams)); !slices.Equal(slices.Sorted(slices.Values(got)), want) {
			t.Errorf("000127 scrubs %v, want secretParams %v", got, want)
		}
	}
}
