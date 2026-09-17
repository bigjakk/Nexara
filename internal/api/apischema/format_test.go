package apischema

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// formatCases is one format's accept table (input -> normalized output)
// and its reject list.
type formatCases struct {
	accept map[string]string
	reject []string
}

// fingerprint corpus values, built rather than written out so they are
// visibly synthetic.
var (
	fpBare   = strings.Repeat("ab", 32)
	fpColons = strings.TrimSuffix(strings.Repeat("AB:", 32), ":")
)

// formatCorpus is the accept/reject table for every built-in format, kept
// package-level so that TestFormatsAreIdempotent can feed each format its
// own output. A format that does not normalize to a fixed point is a trap:
// a value validated on the way in stops validating when the record is read
// back and re-checked on an update.
var formatCorpus = map[string]formatCases{
	"disk-size": {
		accept: map[string]string{
			// The three string shapes the incident report collected. A
			// bare number is already GiB, so "512000" is 512000 GiB — a
			// caller who means mebibytes has to say so. (The fourth
			// shape, the JSON number 500, arrives as a number and is
			// covered in TestAttachDiskIncident.)
			"500G":   "500",
			"500":    "500",
			"512000": "512000",

			"500g":   "500",
			"500GB":  "500",
			"500gb":  "500",
			"500GiB": "500",
			"500 G":  "500",
			" 500G ": "500",
			"1T":     "1024",
			"1TB":    "1024",
			"1t":     "1024",
			"1.5T":   "1536",
			"0.5T":   "512",
			"1P":     "1048576",
			"1K":     "1",
			"1M":     "1",

			// Sub-GiB rounds UP: truncating "512M" to 0 asks the storage
			// backend for a zero-sized volume.
			"512M":  "1",
			"512m":  "1",
			"1024M": "1",
			"1025M": "2",

			"0007":    "7",
			"1048576": "1048576", // exactly the ceiling
		},
		reject: []string{
			"",
			"0",
			"0G",
			"0.0",
			"-5",
			"-5G",
			"+5",
			"abc",
			"500B", // bytes, not GiB — the unit letter is mandatory
			"500b",
			"500GG",
			"1e3",
			"NaN",
			"Inf",
			"1,000",
			"1.5.2",
			"G",
			"1048577", // one GiB over the ceiling
			"2P",
			strings.Repeat("9", 400), // overflows float64
		},
	},
	"storage-id": {
		accept: map[string]string{
			"store01":                "store01",
			"a":                      "a",
			"Store_01":               "Store_01",
			"s.t-o_r1":               "s.t-o_r1",
			strings.Repeat("s", 100): strings.Repeat("s", 100),
		},
		reject: []string{"", "1store", "-store", ".store", "store!", "sto rage", strings.Repeat("s", 101)},
	},
	"node-name": {
		accept: map[string]string{
			"pve-01":                "pve-01",
			"a":                     "a",
			"1":                     "1",
			"node1.example.com":     "node1.example.com",
			strings.Repeat("n", 63): strings.Repeat("n", 63),
		},
		reject: []string{"", "-pve", "pve-", ".pve", "pve_01", "pve 01", strings.Repeat("n", 64)},
	},
	"pve-configid": {
		accept: map[string]string{
			"snap1":                       "snap1",
			"Ab":                          "Ab",
			"my-snap_2":                   "my-snap_2",
			"s" + strings.Repeat("x", 39): "s" + strings.Repeat("x", 39),
		},
		reject: []string{"", "a", "1snap", "snap!", "snap.1", "s" + strings.Repeat("x", 40)},
	},
	"uuid": {
		accept: map[string]string{
			"3f2504e0-4f89-11d3-9a0c-0305e82c3301": "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
			"3F2504E0-4F89-11D3-9A0C-0305E82C3301": "3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		},
		reject: []string{
			"",
			"3f2504e0-4f89-11d3-9a0c-0305e82c330",
			"3f2504e04f8911d39a0c0305e82c3301",
			"{3f2504e0-4f89-11d3-9a0c-0305e82c3301}",
			"urn:uuid:3f2504e0-4f89-11d3-9a0c-0305e82c3301",
			"3f2504e0-4f89-11d3-9a0c-0305e82c330g",
		},
	},
	"email": {
		accept: map[string]string{
			"user@example.com":              "user@example.com",
			" user@example.com ":            "user@example.com",
			"<user@example.com>":            "user@example.com",
			"User.Name+tag@sub.example.com": "User.Name+tag@sub.example.com",
		},
		reject: []string{
			"",
			"not-an-email",
			"user@",
			"user@localhost", // no dot in the domain
			"Full Name <user@example.com>",
			"user@example.com, other@example.com",
			// A quoted local part unquotes into an address this package
			// would itself reject, and one that a comma-joined recipient
			// list would read as two addresses.
			`"a,b"@example.com`,
			`"a b"@example.com`,
			`" "@example.com`,
		},
	},
	"ip": {
		accept: map[string]string{
			"192.0.2.10":      "192.0.2.10",
			" 192.0.2.10 ":    "192.0.2.10",
			"2001:db8::1":     "2001:db8::1",
			"2001:0DB8::0001": "2001:db8::1",
		},
		reject: []string{"", "192.0.2.300", "192.0.2.010", "192.0.2.10/24", "hello", "192.0.2"},
	},
	"cidr": {
		accept: map[string]string{
			"192.0.2.0/24":  "192.0.2.0/24",
			"192.0.2.5/24":  "192.0.2.5/24", // host bits are kept, as PVE does
			"2001:db8::/32": "2001:db8::/32",
		},
		reject: []string{"", "192.0.2.0", "192.0.2.0/33", "192.0.2.0/-1", "hello/24"},
	},
	"mac-addr": {
		accept: map[string]string{
			"02:00:00:00:00:01": "02:00:00:00:00:01",
			"02-00-00-00-00-01": "02:00:00:00:00:01",
			"0200.0000.0001":    "02:00:00:00:00:01",
			"AA:BB:CC:DD:EE:FF": "aa:bb:cc:dd:ee:ff",
		},
		reject: []string{
			"",
			"02:00:00:00:00",
			"zz:00:00:00:00:01",
			"02:00:00:00:00:00:00:01", // EUI-64 is not a NIC address
		},
	},
	"fingerprint-sha256": {
		accept: map[string]string{
			fpBare:                    fpColons,
			strings.ToUpper(fpBare):   fpColons,
			fpColons:                  fpColons,
			strings.ToLower(fpColons): fpColons,
			" " + fpBare + " ":        fpColons,
		},
		reject: []string{
			"",
			strings.Repeat("ab", 31),
			strings.Repeat("ab", 33),
			strings.TrimSuffix(strings.Repeat("AB:", 31), ":"),
			strings.TrimSuffix(strings.Repeat("ZZ:", 32), ":"),
		},
	},
	"bwlimit": {
		accept: map[string]string{
			"0":     "0",
			"10240": "10240",
			" 42 ":  "42",
			"0007":  "7",
		},
		reject: []string{"", "-1", "1.5", "abc", "10240K", "9223372036854775808"},
	},
}

// builtinFormats is every format this package registers in its init.
var builtinFormats = []string{
	"disk-size", "storage-id", "node-name", "pve-configid", "uuid",
	"email", "ip", "cidr", "mac-addr", "fingerprint-sha256", "bwlimit",
}

// mustFormat looks a format up or fails the test.
func mustFormat(t *testing.T, name string) FormatFunc {
	t.Helper()
	fn, ok := LookupFormat(name)
	if !ok {
		t.Fatalf("format %q is not registered", name)
	}
	return fn
}

func TestFormats(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(formatCorpus)) {
		t.Run(name, func(t *testing.T) {
			fn := mustFormat(t, name)
			cases := formatCorpus[name]

			for in, want := range cases.accept {
				got, err := fn(in)
				if err != nil {
					t.Errorf("%s(%q): unexpected error: %v", name, in, err)
					continue
				}
				if got != want {
					t.Errorf("%s(%q) = %q, want %q", name, in, got, want)
				}
			}
			for _, in := range cases.reject {
				got, err := fn(in)
				if err == nil {
					t.Errorf("%s(%q) = %q, want an error", name, in, got)
				}
			}
		})
	}
}

// TestFormatsAreIdempotent asserts fn(fn(x)) == fn(x) for every accepted
// value of every format. Normalization has to be a fixed point: a stored
// value is re-validated whenever it is read back for an update, and a
// format that rewrites its own output rejects the row it just wrote.
func TestFormatsAreIdempotent(t *testing.T) {
	for _, name := range slices.Sorted(maps.Keys(formatCorpus)) {
		t.Run(name, func(t *testing.T) {
			fn := mustFormat(t, name)
			for in, once := range formatCorpus[name].accept {
				twice, err := fn(once)
				if err != nil {
					t.Errorf("%s(%q) = %q, but re-validating that output failed: %v", name, in, once, err)
					continue
				}
				if twice != once {
					t.Errorf("%s is not idempotent: %q -> %q -> %q", name, in, once, twice)
				}
			}
		})
	}
}

// TestDiskSizeCeilingIsExported pins the constant callers quote in their
// own error messages.
func TestDiskSizeCeilingIsExported(t *testing.T) {
	if MaxDiskSizeGiB != 1048576 {
		t.Fatalf("MaxDiskSizeGiB = %d, want 1048576 (1 PiB in GiB)", MaxDiskSizeGiB)
	}
	fn := mustFormat(t, "disk-size")
	if _, err := fn("1048577"); err == nil || !strings.Contains(err.Error(), "1048576") {
		t.Fatalf("over-ceiling error should quote the ceiling, got %v", err)
	}
}

func TestBuiltinFormatsAreRegistered(t *testing.T) {
	for _, name := range builtinFormats {
		if _, ok := LookupFormat(name); !ok {
			t.Errorf("built-in format %q is not registered", name)
		}
		if _, ok := formatCorpus[name]; !ok {
			t.Errorf("built-in format %q has no corpus, so nothing checks it or its idempotence", name)
		}
	}
	for name := range formatCorpus {
		if !slices.Contains(builtinFormats, name) {
			t.Errorf("corpus lists %q, which is not a built-in format", name)
		}
	}
	if _, ok := LookupFormat("no-such-format"); ok {
		t.Error("LookupFormat reported an unregistered name as present")
	}
}

func TestRegisterFormat(t *testing.T) {
	name := "apischema-test-format"
	RegisterFormat(name, func(v string) (string, error) { return strings.ToUpper(v), nil })

	fn := mustFormat(t, name)
	got, err := fn("abc")
	if err != nil || got != "ABC" {
		t.Fatalf("registered format returned (%q, %v), want (\"ABC\", nil)", got, err)
	}

	mustPanic(t, "already registered", func() {
		RegisterFormat(name, func(v string) (string, error) { return v, nil })
	})
	mustPanic(t, "empty name", func() {
		RegisterFormat("", func(v string) (string, error) { return v, nil })
	})
	mustPanic(t, "nil function", func() {
		RegisterFormat("apischema-test-nil-format", nil)
	})
}
