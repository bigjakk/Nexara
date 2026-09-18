package apischema

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// The rule catalogue answers the one question a bare `format: "pve-configid"`
// cannot: what does that actually permit, and who says so?
//
// A named rule with no stated rule is a lookup task. Finding out what
// `pve-configid` allows meant opening this package's format registry, and
// finding out whether it was RIGHT meant opening pve-common and reading
// Perl. Nobody does the second one, so the rules drifted: the `pool`
// parameter was once anchored to an invented `^[A-Za-z0-9][A-Za-z0-9._-]*$`
// because that is what a well-formed id "obviously" looks like, while
// Proxmox's own verify_poolname allows a leading dot and nests three levels
// deep. That invention would have turned working requests into 400s on five
// routes, and a test had already frozen the guess.
//
// So every named rule carries its own rule text, a one-line statement of
// what it permits, and — when the rule is Proxmox's — the upstream file and
// function it was transcribed from, the upstream rule verbatim, and an
// explicit note wherever ours differs. A reader settles "what does this
// accept" without leaving the repo, and settles "is that still what Proxmox
// does" by diffing UpstreamRule against the file named in Upstream.
//
// # Two kinds of rule
//
// A FORMAT is registered in the format registry, validates AND normalizes,
// and is named by [Property.Format]. A PATTERN is a regex a declaration
// carries as [Property.Pattern]; it never rewrites the value. The
// distinction is load-bearing rather than stylistic — several routes take a
// value that must NOT be normalized (a "+8G" resize delta, a snapshot name
// that already exists on the cluster), and guard tests in internal/api pin
// those to a pattern for exactly that reason. Do not "upgrade" a pattern to
// a format to tidy it up.
//
// # Why the catalogue is load-bearing and not a comment
//
// A catalogue that can disagree with the compiled rule is worse than none,
// so it is wired in rather than written alongside:
//
//   - Every regex rule here is the ONE definition. format.go compiles its
//     regexes from these entries (see ruleRegexp) and every declaration in
//     internal/api spells its pattern [Rule]("<name>") rather than repeating
//     the regex. There is no second copy to drift from.
//   - Every "<base>-or-empty" rule is DERIVED from its base by orEmpty, so
//     a fix to the base reaches the sentinel variant automatically. That
//     pairing is the drift this catalogue was built to stop: emptyOrNodeName
//     and the node-name format were two hand-written copies of one rule.
//   - Every entry carries witnesses — Accepts and Rejects — that
//     catalogue_test.go runs through the rule as it is actually enforced. A
//     prose line that stops being true fails a test.
//
// # Scope
//
// Every registered format is here; catalogue_test.go fails if one is
// missing. Patterns are here when more than one declaration site uses them,
// which is the case where a second, subtly different copy is the likely
// mistake. A pattern used once stays at its declaration with its reasoning;
// promote it when a second site wants it.

// RuleKind says how a named rule is applied to a value.
type RuleKind string

// The two kinds of named rule.
const (
	// KindFormat is registered in the format registry, reached by
	// Property.Format, and may normalize the value it validates.
	KindFormat RuleKind = "format"
	// KindPattern is a regex reached by Property.Pattern, which checks the
	// value's shape and never rewrites it.
	KindPattern RuleKind = "pattern"
)

// Origin says who owns a rule's definition.
type Origin string

// Where a rule came from.
const (
	// OriginProxmox means the rule was transcribed from a Proxmox
	// validator, which remains the source of truth for it.
	OriginProxmox Origin = "proxmox"
	// OriginNexara means Nexara defines the rule itself. There is no
	// upstream to check it against.
	OriginNexara Origin = "nexara"
)

// RuleDoc is one named rule's catalogue entry.
type RuleDoc struct {
	// Name is the format name (KindFormat) or the key [Rule] answers to
	// (KindPattern).
	Name string
	Kind RuleKind

	// Permits states in one line what the rule allows.
	Permits string

	// Rule is the rule itself: the regex source when RuleIsRegex, and
	// otherwise a precise prose statement of a check that is not one regex
	// (an address parse, a unit conversion).
	Rule string

	// RuleIsRegex reports whether Rule is a regex that is the WHOLE shape
	// check. A format may carry extra bounds alongside it — a length cap,
	// a numeric ceiling — which Permits states and the witnesses pin.
	RuleIsRegex bool

	Origin Origin

	// Upstream names where a Proxmox rule was transcribed from, as
	// "<repo> <path> <function>", e.g.
	// "pve-access-control src/PVE/AccessControl.pm verify_poolname".
	// Fetch it with
	// https://git.proxmox.com/?p=<repo>.git;a=blob_plain;f=<path>;hb=HEAD
	// and grep the function name. Empty for OriginNexara.
	Upstream string

	// UpstreamRule is the upstream rule verbatim, so that re-verifying is
	// a diff against the file Upstream names rather than a fresh reading.
	// Empty for OriginNexara.
	UpstreamRule string

	// Divergence says how this rule differs from the upstream rule of the
	// same name, and why. Empty means the two agree. A divergence is
	// recorded, not silently carried: changing what an endpoint accepts is
	// a decision for the operator, not for whoever next reads the regex.
	//
	// A Nexara rule can carry one too, and the case that matters is a NAME
	// COLLISION — Proxmox registers a format under this name whose rule is
	// a different rule entirely. That is worth more warning than a
	// difference of one character, not less, because the name invites the
	// assumption and nothing contradicts it.
	Divergence string

	// Accepts and Rejects are witnesses for Permits, not a behaviour
	// suite — format_test.go's corpus is that. They exist so a prose line
	// cannot quietly stop being true, so they name the boundaries Permits
	// claims and the cases where this rule and UpstreamRule part company.
	Accepts []string
	Rejects []string
}

// orEmpty widens an anchored rule with the empty string.
//
// Both branches keep their own anchors, and that is not tidiness: Go's
// regexp is a SUBSTRING search, so `^$|[A-Za-z0-9]...$` matches "..foo"
// from index 2 and hands the traversal straight back. base is always a
// fully anchored `^...$`, so prefixing `^$|` is the whole job.
func orEmpty(base string) string { return `^$|` + base }

// Empty-string sentinels exist because an empty value MEANS something on
// the routes that carry one: the clone dialog sends storage:"" for a linked
// clone, the pool selector sends pool:"" to remove a guest from its pool,
// and the PBS listings read datastore:"" as "do not filter". Validate
// treats "" as a value the caller SUPPLIED rather than as an absent one
// (see present() in validate.go) and every registered format rejects it, so
// those parameters cannot borrow the format and have to spell the rule as a
// pattern. Deriving the pattern from the format's own rule is what keeps
// the two from parting company.

// catalogue is built rather than declared so that the -or-empty rules can
// read the base rule they widen. buildCatalogue panics on a malformed or
// duplicated entry, which is a programming error in this file.
var catalogue = buildCatalogue()

func buildCatalogue() map[string]RuleDoc {
	out := make(map[string]RuleDoc, 32)
	add := func(docs ...RuleDoc) {
		for _, d := range docs {
			if d.Name == "" {
				panic("apischema: catalogue entry with an empty name")
			}
			if _, dup := out[d.Name]; dup {
				panic(fmt.Sprintf("apischema: catalogue entry %q declared twice", d.Name))
			}
			out[d.Name] = d
		}
	}

	add(formatRules()...)
	add(patternRules()...)
	// The sentinel variants are DERIVED from the entries above, so they
	// are added last and read their base out of the map.
	add(orEmptyRules(out)...)
	return out
}

// formatRules is every rule reached by Property.Format.
func formatRules() []RuleDoc {
	return []RuleDoc{
		{
			Name:    "disk-size",
			Kind:    KindFormat,
			Permits: `a decimal size with an optional binary unit (K, M, G, T, P; case-insensitive, an "i" and a "B" may follow), normalized to a whole GiB count between 1 and 1048576; a bare number is already GiB.`,
			Rule: `^(\d+(?:\.\d+)?)\s*(?:([KMGTPkmgtp])[iI]?[bB]?)?$` + " on the trimmed value, then converted to GiB, " +
				"rounded UP so that \"512M\" is 1 rather than a zero-sized allocation request, and rejected " +
				"outside 1..MaxDiskSizeGiB.",
			RuleIsRegex: false,
			Origin:      OriginNexara,
			Divergence: "NAME COLLISION, not a variant. PVE registers a `disk-size` format of its own " +
				"(pve-common src/PVE/JSONSchema.pm pve_verify_disk_size, via parse_size) which accepts only " +
				"UPPERCASE K/M/G/T, has no P, and reads a bare number as BYTES. This one normalizes to the " +
				"GiB count the \"storage:N\" volume spec takes, so a bare number here is GiB and \"512M\" " +
				"rounds UP to 1 rather than truncating to a zero-sized allocation request. Do not \"align\" " +
				"the two: they normalize to different units for different consumers.",
			Accepts: []string{"500", "500G", "500gb", "1T", " 500G ", "512M", "1P"},
			Rejects: []string{"", "0", "-1", "500B", "1048577", "abc", "1,5G"},
		},
		{
			Name:         "storage-id",
			Kind:         KindFormat,
			Permits:      "a Proxmox storage pool id: a leading letter, then letters, digits, dot, underscore and dash, up to 100 characters.",
			Rule:         `^[A-Za-z][A-Za-z0-9._-]*$`,
			RuleIsRegex:  true,
			Origin:       OriginProxmox,
			Upstream:     "pve-common src/PVE/JSONSchema.pm parse_storage_id (via parse_id)",
			UpstreamRule: `length >= 2 AND ^[a-z][a-z0-9\-\_\.]*[a-z0-9]\z (case-insensitive)`,
			Divergence: "LOOSER at both ends. PVE requires at least 2 characters and a trailing letter or " +
				"digit; this accepts the single character \"a\" and a trailing separator such as \"store-\". " +
				"Neither can exist as a PVE storage id, so the looseness costs no safety here and refusing " +
				"them would only 400 a caller ahead of Proxmox's own message. The 100-character cap is this " +
				"package's, bounding a path segment; PVE states none.",
			Accepts: []string{"store01", "local-lvm", "a", "store-"},
			Rejects: []string{"", "0store", "-store", "store/01", strings.Repeat("s", 101)},
		},
		{
			Name:         "node-name",
			Kind:         KindFormat,
			Permits:      "a Proxmox node name: letters, digits, dot and dash, starting and ending alphanumeric, up to 63 characters.",
			Rule:         `^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?$`,
			RuleIsRegex:  true,
			Origin:       OriginProxmox,
			Upstream:     "pve-common src/PVE/JSONSchema.pm pve_verify_node_name",
			UpstreamRule: `^([a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?)\z`,
			Divergence: "LOOSER: PVE's class is letters, digits and DASH only, so \"pve.01\" is a node name " +
				"here and not to Proxmox. The 63-character cap is this package's, bounding a path segment; " +
				"PVE states none.",
			Accepts: []string{"pve-01", "n", "node1", "pve.01"},
			Rejects: []string{"", "-pve", "pve-", "pve_01", strings.Repeat("n", 64)},
		},
		{
			Name:         "pve-configid",
			Kind:         KindFormat,
			Permits:      "a PVE configuration id: a leading letter, then letters, digits, underscore and dash, 2 to 40 characters.",
			Rule:         `^[A-Za-z][A-Za-z0-9_-]{1,39}$`,
			RuleIsRegex:  true,
			Origin:       OriginProxmox,
			Upstream:     "pve-common src/PVE/JSONSchema.pm pve_verify_configid ($CONFIGID_RE)",
			UpstreamRule: `^[a-z][a-z0-9_-]+\z (case-insensitive) — a letter then ONE OR MORE, so minimum 2 and no maximum`,
			Divergence: "STRICTER: the 40-character ceiling is this package's, and PVE imposes none. It bounds " +
				"a value that becomes a path segment. An id longer than 40 that PVE would accept is refused " +
				"here — see pve-configid-existing, which is what the routes that must address an EXISTING " +
				"object use.",
			Accepts: []string{"snap1", "ab", "a-b_c", strings.Repeat("s", 40)},
			Rejects: []string{"", "a", "1snap", "snap.1", strings.Repeat("s", 41)},
		},
		{
			Name:        "uuid",
			Kind:        KindFormat,
			Permits:     "a canonical 8-4-4-4-12 hexadecimal UUID in either case, normalized to lowercase.",
			Rule:        `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`,
			RuleIsRegex: true,
			Origin:      OriginNexara,
			// Nexara's own row ids, not a Proxmox identifier: the braced
			// and urn: forms some parsers take are refused because they
			// never round-trip through our own URLs.
			Accepts: []string{"3f2504e0-4f89-11d3-9a0c-0305e82c3301", "3F2504E0-4F89-11D3-9A0C-0305E82C3301"},
			Rejects: []string{"", "3f2504e04f8911d39a0c0305e82c3301", "{3f2504e0-4f89-11d3-9a0c-0305e82c3301}", "urn:uuid:3f2504e0-4f89-11d3-9a0c-0305e82c3301"},
		},
		{
			Name:    "email",
			Kind:    KindFormat,
			Permits: "a bare email address, optionally in angle brackets, whose local part is an unquoted RFC 5322 dot-atom and whose domain contains a dot; a display name is refused.",
			Rule: "net/mail.ParseAddress on the trimmed value, then: the parsed Name must be empty (no display " +
				"name), the domain must contain a dot, and the local part must match " +
				"^[A-Za-z0-9!#$%&'*+/=?^_`{|}~-]+(?:\\.[...]+)*$ so that a QUOTED local part — which " +
				"ParseAddress unquotes, turning `\"a,b\"@example.com` into a,b@example.com — cannot make " +
				"normalization a non-fixed-point or split a comma-joined recipient list in two.",
			RuleIsRegex:  false,
			Origin:       OriginProxmox,
			Upstream:     "pve-common src/PVE/JSONSchema.pm pve_verify_email (via PVE::ParseUtils $EMAIL_RE)",
			UpstreamRule: `^[\w\+\-\~]+(\.[\w\+\-\~]+)*@[a-zA-Z0-9\-]+(\.[a-zA-Z0-9\-]+)*\z`,
			Divergence: "Differs in BOTH directions. LOOSER in the local part: PVE allows only word characters, " +
				"plus, dash and tilde, so \"o'brien@example.com\" is an address here and not to PVE. STRICTER " +
				"in the domain: PVE's domain needs no dot, so \"admin@localhost\" is a PVE-valid notification " +
				"target that this refuses. The stricter half is the one that can 400 a value Proxmox accepts.",
			Accepts: []string{"user@example.com", "<user@example.com>", " user@example.com ", "o'brien@example.com"},
			Rejects: []string{"", "user", "user@localhost", "User <user@example.com>", `"a,b"@example.com`},
		},
		{
			Name:         "ip",
			Kind:         KindFormat,
			Permits:      "an IPv4 or IPv6 address, normalized to its canonical form (IPv6 lowercased and zero-compressed).",
			Rule:         "net/netip.ParseAddr on the trimmed value, rendered back with Addr.String.",
			RuleIsRegex:  false,
			Origin:       OriginProxmox,
			Upstream:     "pve-common src/PVE/JSONSchema.pm pve_verify_ip (via PVE::ParseUtils $IPV4RE and $IPV6RE)",
			UpstreamRule: `^(?:$IPV4RE|$IPV6RE)\z — dotted quad or RFC 4291 IPv6 text, with NO zone identifier`,
			Divergence: "LOOSER: netip accepts a zone, so \"fe80::1%eth0\" is an address here and not to PVE. " +
				"It also NORMALIZES, which PVE does not — PVE returns the value untouched.",
			Accepts: []string{"192.0.2.10", "2001:db8::1", "2001:0DB8:0000::1", " 192.0.2.10 ", "fe80::1%eth0"},
			Rejects: []string{"", "192.0.2.300", "192.0.2.10/24", "192.0.2.010", "example.com"},
		},
		{
			Name:         "cidr",
			Kind:         KindFormat,
			Permits:      "an address with a prefix length, host bits allowed and preserved, normalized to canonical text.",
			Rule:         "net/netip.ParsePrefix on the trimmed value, rendered back with Prefix.String.",
			RuleIsRegex:  false,
			Origin:       OriginProxmox,
			Upstream:     "pve-common src/PVE/JSONSchema.pm pve_verify_cidr (via pve_verify_cidrv4 and pve_verify_cidrv6)",
			UpstreamRule: `IPv4: ^$IPV4RE/(\d+)\z with the length > 7 and <= 32. IPv6: the same shape with the length > 7 and <= 128.`,
			Divergence: "LOOSER at the low end: PVE refuses a prefix length of 0 through 7 on both families, " +
				"and this accepts them, so \"192.0.2.0/0\" passes here and not to PVE. Host bits are kept by " +
				"both — Proxmox interface configuration relies on that.",
			Accepts: []string{"192.0.2.0/24", "192.0.2.10/24", "2001:db8::/32", " 192.0.2.0/24 ", "192.0.2.0/0"},
			Rejects: []string{"", "192.0.2.0", "192.0.2.0/33", "192.0.2.0/-1", "example.com/24"},
		},
		{
			Name:         "mac-addr",
			Kind:         KindFormat,
			Permits:      "an EUI-48 MAC address in colon, dash or Cisco dotted form, normalized to lowercase colon-separated.",
			Rule:         "net.ParseMAC on the trimmed value, requiring exactly 6 bytes, rendered back with HardwareAddr.String.",
			RuleIsRegex:  false,
			Origin:       OriginProxmox,
			Upstream:     "pve-common src/PVE/JSONSchema.pm pve_verify_mac_addr",
			UpstreamRule: `^[a-f0-9][02468ace](?::[a-f0-9]{2}){5}\z (case-insensitive) — colon form only, and the I/G bit MUST be clear`,
			Divergence: "LOOSER in two ways. PVE takes only the colon form; this also takes the dash and Cisco " +
				"dotted forms — harmless, because it normalizes to colons before the value goes anywhere. " +
				"PVE also refuses a MAC whose I/G (Individual/Group) bit is set, i.e. whose second hex digit " +
				"is odd, because a multicast address breaks most things it is assigned to; this accepts " +
				"\"01:00:5e:00:00:01\" and forwards it. That second one is a real gap, not a formatting one.",
			Accepts: []string{"02:00:00:00:00:01", "02-00-00-00-00-01", "0200.0000.0001", "01:00:5e:00:00:01"},
			Rejects: []string{"", "02:00:00:00:00", "02:00:00:00:00:01:02", "zz:00:00:00:00:01"},
		},
		{
			Name:         "fingerprint-sha256",
			Kind:         KindFormat,
			Permits:      "a SHA-256 fingerprint as 32 colon-separated hex pairs or as 64 bare hex characters, normalized to uppercase colon-separated pairs.",
			Rule:         `^[0-9a-fA-F]{2}(?::[0-9a-fA-F]{2}){31}$ or ^[0-9a-fA-F]{64}$, uppercased and re-joined with colons.`,
			RuleIsRegex:  false,
			Origin:       OriginProxmox,
			Upstream:     "pve-common src/PVE/JSONSchema.pm register_standard_option('fingerprint-sha256')",
			UpstreamRule: `pattern ([A-Fa-f0-9]{2}:){31}[A-Fa-f0-9]{2} — the colon form only`,
			Divergence: "LOOSER on input only: the bare 64-hex form is accepted and then rewritten into PVE's " +
				"own colon form, so nothing that leaves here is outside the upstream pattern. The bare form " +
				"is what `openssl dgst` prints and what operators paste.",
			Accepts: []string{strings.Repeat("ab", 32), strings.TrimSuffix(strings.Repeat("AB:", 32), ":")},
			Rejects: []string{"", strings.Repeat("ab", 31), strings.Repeat("ab", 33), strings.TrimSuffix(strings.Repeat("AB:", 31), ":")},
		},
		{
			Name:        "bwlimit",
			Kind:        KindFormat,
			Permits:     "a non-negative decimal bandwidth limit in KiB/s, normalized to its canonical digits; 0 means no limit.",
			Rule:        `^\d+$ on the trimmed value, then parsed as a signed 64-bit integer and re-rendered, so "0007" becomes "7".`,
			RuleIsRegex: false,
			Origin:      OriginNexara,
			Divergence: "NAME COLLISION, not a variant. PVE registers a `bwlimit` format of its own " +
				"(pve-common src/PVE/JSONSchema.pm, $bwlimit_format) which is a PROPERTY STRING with a key " +
				"per operation — default, restore, migration, clone, move — used for the datacenter-wide " +
				"default. This one is a single limit on a single request, for the declarations that carry " +
				"it as text. The `bwlimit` standard option in stdoption.go is an integer and does not use " +
				"this format at all.",
			Accepts: []string{"0", "10240", " 42 ", "0007"},
			Rejects: []string{"", "-1", "1.5", "10240K", "9223372036854775808"},
		},
	}
}

// patternRules is every rule reached by [Rule] and spelled at a declaration
// as Property.Pattern.
//
// Each one is here because MORE THAN ONE declaration wants it. That is the
// case where a second, hand-written copy is the likely mistake, and where
// the copies had already begun to differ.
func patternRules() []RuleDoc {
	return []RuleDoc{
		{
			Name: "pve-object-id",
			Kind: KindPattern,
			Permits: "a PVE object id: a leading letter or digit, then letters, digits, dot, underscore and " +
				"dash. Used for firewall aliases and IP sets, SDN objects, metric server sections and PVE " +
				"backup job ids.",
			Rule:        `^[A-Za-z0-9][A-Za-z0-9._-]*$`,
			RuleIsRegex: true,
			Origin:      OriginNexara,
			// Nexara's own, and deliberately a SUPERSET of every Proxmox
			// format it stands in for — pve-fw-alias-name is
			// [A-Za-z][A-Za-z0-9_-]*, pve-sdn-zone-id is [a-z][a-z0-9]*
			// capped at 8, a metric server id is pve-configid. Nothing
			// Proxmox would accept is refused, so an object created
			// outside Nexara cannot become unaddressable through it.
			//
			// What it DOES enforce is the leading alphanumeric, and that
			// is the whole point: every consumer concatenates the value
			// into a Proxmox path with url.PathEscape, which escapes "/"
			// and leaves "." and ".." alone. RE2 has no negative
			// lookahead, so "." and ".." are excluded by anchoring the
			// first character instead.
			Accepts: []string{"alias01", "0net", "a.b_c-d", "backup-a1b2c3"},
			Rejects: []string{"", ".", "..", ".hidden", "-lead", "a/b", "a b"},
		},
		{
			Name: "pve-object-id-colon",
			Kind: KindPattern,
			Permits: "pve-object-id widened with the colon, for the two ids that carry one: a network " +
				"interface name and an SDN subnet id.",
			Rule:        `^[A-Za-z0-9][A-Za-z0-9.:_-]*$`,
			RuleIsRegex: true,
			Origin:      OriginNexara,
			// The colon earns its place twice over: PVE's `alias`
			// interface type is named "eth0:0", and an SDN subnet's
			// DERIVED id carries the colons of an IPv6 address. Refusing
			// it would make an existing alias interface or IPv6 subnet
			// un-editable and un-deletable. A colon is not a path
			// separator and cannot introduce a traversal, and the leading
			// alphanumeric still keeps "." and ".." out.
			//
			// Both consumers share one rule, so a future narrowing has to
			// consider both. It is deliberately WIDER than PVE's pve-iface
			// (^[a-z][a-z0-9_]{1,20}([:.]\d+)?$), which allows neither a
			// dash nor a leading digit.
			Accepts: []string{"vmbr0", "vmbr0.100", "eth0:0", "myzone-2001:db8::-64"},
			Rejects: []string{"", ".", "..", ".eth0", "-eth0", "vmbr0/1"},
		},
		{
			Name: "pve-configid-existing",
			Kind: KindPattern,
			Permits: "a PVE configuration id WITHOUT the two-character minimum: a leading letter, then " +
				"letters, digits, underscore and dash, one character or more.",
			Rule:         `^[A-Za-z][A-Za-z0-9_-]*$`,
			RuleIsRegex:  true,
			Origin:       OriginProxmox,
			Upstream:     "pve-common src/PVE/JSONSchema.pm pve_verify_configid ($CONFIGID_RE)",
			UpstreamRule: `^[a-z][a-z0-9_-]+\z (case-insensitive) — a letter then ONE OR MORE, so minimum 2`,
			Divergence: "LOOSER by one character, on purpose. Proxmox's own configid allows a single " +
				"character where Nexara's create rule (validateSnapshotName) requires two, so a snapshot, HA " +
				"group or HA rule made OUTSIDE Nexara can carry a name our own create refuses. This is the " +
				"rule the routes that ADDRESS an existing object use, so that such an object stays " +
				"deletable; the create bodies keep the pve-configid format.",
			Accepts: []string{"a", "snap1", "pre-upgrade_2"},
			Rejects: []string{"", "1snap", "snap.1", ".", "..", "-snap"},
		},
		{
			Name:        "ceph-pool-name",
			Kind:        KindPattern,
			Permits:     "a Ceph pool name: an optional leading dot, then a letter or digit, then letters, digits, dot, underscore and dash.",
			Rule:        `^\.?[A-Za-z0-9][A-Za-z0-9._-]*$`,
			RuleIsRegex: true,
			Origin:      OriginProxmox,
			Upstream:    "pve-manager PVE/API2/Ceph/Pool.pm $ceph_pool_common_options (the `name` parameter)",
			UpstreamRule: `pattern ^[^:/\s]+$ — anything at all except a colon, a slash or whitespace. ` +
				`Grep for $ceph_pool_common_options, NOT for a route: the closure is where the rule ` +
				`lives, and createpool and setpool splice it in. The routes that ADDRESS an existing ` +
				`pool — destroypool, getpool, poolindex — declare name as a bare type => 'string' with ` +
				`no pattern at all.`,
			Divergence: "STRICTER, and by a wide margin. PVE excludes three characters on CREATE and " +
				"constrains the addressing routes not at all; this admits one character class on both. A " +
				"pool created outside Nexara — \"rbd+meta\", \"pool!1\" — matches PVE's rule and not this " +
				"one, and would be un-gettable, un-editable and un-deletable through this " +
				"API, which is the exact failure the leading-dot allowance below exists to avoid. The " +
				"leading dot IS deliberate: Ceph's own internal pools are named \".mgr\" and friends, and " +
				"requiring an alphanumeric AFTER it is what keeps \"..\" out, since RE2 has no negative " +
				"lookahead and DeleteCephPool concatenates the value into a path.",
			Accepts: []string{".mgr", "rbd", "cephfs_data", "pool-01"},
			Rejects: []string{"", ".", "..", "-pool", "rbd+meta", "a/b"},
		},
		{
			Name:         "pbs-safe-id",
			Kind:         KindPattern,
			Permits:      "a Proxmox Backup Server id: a leading letter, digit or underscore, then letters, digits, dot, underscore and dash.",
			Rule:         `^[A-Za-z0-9_][A-Za-z0-9._-]*$`,
			RuleIsRegex:  true,
			Origin:       OriginProxmox,
			Upstream:     "proxmox proxmox-schema/src/api_types.rs SAFE_ID_REGEX_STR (re-exported by pbs-api-types as PROXMOX_SAFE_ID_REGEX)",
			UpstreamRule: `^(?:[A-Za-z0-9_][A-Za-z0-9._\-]*)$`,
			// Character-for-character upstream's, which is what lets the
			// routes that address an existing datastore or job be certain
			// they cannot refuse one PBS created. The leading class is
			// also what keeps "." and ".." out of a path PBS clients build
			// by concatenation with url.PathEscape, which escapes "/" and
			// leaves both alone.
			Accepts: []string{"datastore01", "_internal", "0store", "a.b-c_d"},
			Rejects: []string{"", ".", "..", ".hidden", "-store", "a/b"},
		},
		{
			Name:         "disk-resize",
			Kind:         KindPattern,
			Permits:      `a resize target: an optional leading "+" meaning "grow BY", then a decimal number with an optional K, M, G or T unit.`,
			Rule:         `^\+?\d+(\.\d+)?[KMGTkmgt]?$`,
			RuleIsRegex:  true,
			Origin:       OriginProxmox,
			Upstream:     "qemu-server src/PVE/API2/Qemu.pm resize_vm (the `size` parameter)",
			UpstreamRule: `pattern \+?\d+(\.\d+)?[KMGT]? — UPPERCASE units only`,
			Divergence: "LOOSER: the lowercase units are this package's addition, so \"+8g\" passes here and " +
				"is then refused by Proxmox with Proxmox's own message. It is a pattern and NOT the " +
				"disk-size format, and that is the load-bearing part: disk-size normalizes to a bare GiB " +
				"count, which would turn \"+8G\" (grow BY 8 GiB) into \"8\" (grow TO 8 GiB) — a shrink on " +
				"anything already larger. Guard tests in internal/api pin both resize routes to a pattern " +
				"for that reason.",
			Accepts: []string{"+8G", "64G", "1T", "512M", "100", "1.5T", "+8g"},
			Rejects: []string{"", "-8G", "8GB", "8 G", "+", "abc"},
		},
		{
			Name: "pve-poolid",
			Kind: KindPattern,
			Permits: "a PVE resource pool id, nesting included: one to three segments of letters, digits, " +
				"dot, underscore and dash, separated by slashes.",
			Rule:         `^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+){0,2}$`,
			RuleIsRegex:  true,
			Origin:       OriginProxmox,
			Upstream:     "pve-access-control src/PVE/AccessControl.pm verify_poolname",
			UpstreamRule: `^[A-Za-z0-9\.\-_]+(?:/[A-Za-z0-9\.\-_]+){0,2}\z, plus an explicit "nested too deeply" check at more than 3 levels`,
			// Character-for-character upstream's. Transcribed rather than
			// tightened, and the tidier-looking
			// ^[A-Za-z0-9][A-Za-z0-9._-]*$ that a reader invents instead is
			// wrong twice: a leading dot or dash IS a valid pool name, and
			// pools really do nest up to three levels. That invention
			// would have 400'd pool names Proxmox accepts on five routes.
			Accepts: []string{"infra", ".hidden", "-lead", "infra/prod", "infra/prod/db"},
			Rejects: []string{"", "/infra", "infra/", "infra/prod/db/extra", "infra prod"},
		},
		{
			Name: "pve-poolid-segment",
			Kind: KindPattern,
			Permits: "a single-segment pve-poolid — the same charset with no slash, so that it fits one " +
				"URL path segment.",
			Rule:         `^[A-Za-z0-9._-]+$`,
			RuleIsRegex:  true,
			Origin:       OriginProxmox,
			Upstream:     "pve-access-control src/PVE/AccessControl.pm verify_poolname",
			UpstreamRule: `^[A-Za-z0-9\.\-_]+(?:/[A-Za-z0-9\.\-_]+){0,2}\z`,
			Divergence: "STRICTER by design, and not a judgement about pool names. A nested id contains a " +
				"slash, so it matches neither a Fiber path segment nor the \"/pools/{poolid}\" form the " +
				"client builds — a nested pool is unreachable through the per-pool routes whatever rule " +
				"they carry. The fix is the query form PVE moved to (\"PUT /pools?poolid=…\"), not a looser " +
				"rule here. The routes that are NOT bound by a path segment — POST /pools, and the `pool` " +
				"body parameter — take pve-poolid whole.",
			Accepts: []string{"infra", ".hidden", "-lead", "a.b_c-d"},
			Rejects: []string{"", "infra/prod", "infra prod", "a+b"},
		},
	}
}

// orEmptyRules are the sentinel variants, each DERIVED from the entry it
// widens so that a fix to the base rule reaches the variant too.
func orEmptyRules(base map[string]RuleDoc) []RuleDoc {
	of := func(name string) RuleDoc {
		d, ok := base[name]
		if !ok {
			panic(fmt.Sprintf("apischema: -or-empty rule derived from unknown base %q", name))
		}
		if !d.RuleIsRegex {
			panic(fmt.Sprintf("apischema: -or-empty rule derived from %q, whose rule is not a regex", name))
		}
		return d
	}
	derive := func(baseName, permits, why string) RuleDoc {
		d := of(baseName)
		return RuleDoc{
			Name:         baseName + "-or-empty",
			Kind:         KindPattern,
			Permits:      permits,
			Rule:         orEmpty(d.Rule),
			RuleIsRegex:  true,
			Origin:       d.Origin,
			Upstream:     d.Upstream,
			UpstreamRule: d.UpstreamRule,
			Divergence:   strings.TrimSpace(why + " " + d.Divergence),
			// The witnesses are the base's, plus the empty string the
			// variant exists for. Two of the base's rejects cannot come
			// across: "" is the whole point of the variant, and a value
			// the base turns away on a LENGTH cap rather than on its regex
			// — a 64-character node name — is not refused by the regex
			// alone, and the declaration carries its own MaxLength for it.
			// carryRejects drops both by asking the base's regex rather
			// than by listing exceptions, so a new base witness is
			// classified correctly without anyone remembering to.
			Accepts: append([]string{""}, d.Accepts...),
			Rejects: carryRejects(d),
		}
	}

	return []RuleDoc{
		derive("node-name",
			"a node name, or the empty string.",
			`The empty string means "let Proxmox choose the node" on the routes that carry it.`),
		derive("storage-id",
			"a storage id, or the empty string.",
			`The empty string means "leave it unset" — the clone dialog sends storage:"" for a linked clone.`),
		derive("uuid",
			"a UUID, or the empty string.",
			`The empty string means "not attached to a cluster" on the PBS server create body.`),
		derive("pbs-safe-id",
			"a PBS id, or the empty string.",
			`The empty string means "do not filter" on the two PBS listing query parameters.`),
		derive("pve-poolid",
			"a resource pool id, nesting included, or the empty string.",
			`The empty string means "remove the guest from its pool" — the pool selector sends pool:"".`),
	}
}

// carryRejects is the subset of a base rule's rejects that its -or-empty
// variant still rejects: the ones its REGEX turns away. It panics if that
// leaves none, because an entry with no reject witness states a rule
// nothing holds it to.
func carryRejects(base RuleDoc) []string {
	re := regexp.MustCompile(base.Rule)
	out := make([]string, 0, len(base.Rejects))
	for _, v := range base.Rejects {
		if v == "" || re.MatchString(v) {
			continue
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		panic(fmt.Sprintf("apischema: %q-or-empty inherits no reject witness from its base", base.Name))
	}
	return out
}

// Catalogue returns every rule, ordered by name. The slice and its entries
// are copies: a caller may sort, filter or render them freely.
func Catalogue() []RuleDoc {
	out := make([]RuleDoc, 0, len(catalogue))
	for _, name := range slices.Sorted(maps.Keys(catalogue)) {
		d := catalogue[name]
		d.Accepts = slices.Clone(d.Accepts)
		d.Rejects = slices.Clone(d.Rejects)
		out = append(out, d)
	}
	return out
}

// LookupRule returns one rule's entry.
func LookupRule(name string) (RuleDoc, bool) {
	d, ok := catalogue[name]
	if !ok {
		return RuleDoc{}, false
	}
	d.Accepts = slices.Clone(d.Accepts)
	d.Rejects = slices.Clone(d.Rejects)
	return d, true
}

// Rule returns the regex source a declaration puts in Property.Pattern:
//
//	"iface": {Type: apischema.String, Pattern: apischema.Rule("pve-object-id-colon")}
//
// It mirrors [StdOption] and panics for the same reasons — an unknown name
// is a typo in a declaration that a package-level var evaluates at startup,
// so it should stop the process rather than fail one request.
//
// It panics for a FORMAT too, naming the field to use instead. A format
// normalizes, and reaching it through Pattern would check the shape while
// silently dropping the normalization the format exists for.
func Rule(name string) string {
	d, ok := catalogue[name]
	if !ok {
		patterns := make([]string, 0, len(catalogue))
		for n, e := range catalogue {
			if e.Kind == KindPattern {
				patterns = append(patterns, n)
			}
		}
		slices.Sort(patterns)
		panic(fmt.Sprintf("apischema: unknown pattern rule %q (catalogued: %s)", name, strings.Join(patterns, ", ")))
	}
	if d.Kind != KindPattern {
		panic(fmt.Sprintf("apischema: rule %q is a format — declare it as Format: %q, not as a Pattern", name, name))
	}
	return d.Rule
}

// ruleRegexp compiles a catalogued regex rule. format.go builds its
// validators from it, which is what makes the catalogue the single
// definition of those rules rather than a second copy of them.
func ruleRegexp(name string) *regexp.Regexp {
	d, ok := catalogue[name]
	if !ok {
		panic(fmt.Sprintf("apischema: no catalogue entry for %q", name))
	}
	if !d.RuleIsRegex {
		panic(fmt.Sprintf("apischema: catalogue entry %q does not state its rule as a regex", name))
	}
	return regexp.MustCompile(d.Rule)
}
