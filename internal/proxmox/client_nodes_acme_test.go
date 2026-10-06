package proxmox

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

func setNodeACMEConfig(cfg NodeACMEConfig) func(*Client) error {
	return func(c *Client) error { return c.SetNodeACMEConfig(context.Background(), "pve-01", cfg) }
}

// nodeConfigControlCharacters is every kind of character the control-character
// refusal has to name: C0 (LF, CR, TAB, VT, FF, NUL and ESC are each one of it),
// DEL, C1 (U+0085 and the CSI, U+009B, are two of it), and U+2028 and U+2029, which
// unicode.IsControl does not count.
var nodeConfigControlCharacters = []struct{ name, char string }{
	{"a line feed", "\n"}, {"a carriage return", "\r"}, {"a tab", "\t"}, {"a vertical tab", "\v"},
	{"a form feed", "\f"}, {"a NUL", "\x00"}, {"ESC", "\x1b"}, {"DEL", "\x7f"},
	{"U+0085, the next-line character", "\u0085"}, {"U+009B, the C1 control sequence introducer", "\u009b"},
	{"U+2028, the line separator", "\u2028"}, {"U+2029, the paragraph separator", "\u2029"},
}

// TestHasLineBreakOrControl is the character classification both node config
// writers share: every control character is refused wherever it sits, and text
// that is merely unusual (spaces of any width, other scripts) is not.
func TestHasLineBreakOrControl(t *testing.T) {
	for _, c := range nodeConfigControlCharacters {
		for _, v := range []string{c.char, "a" + c.char, c.char + "a", "a" + c.char + "b"} {
			if !hasLineBreakOrControl(v) {
				t.Errorf("hasLineBreakOrControl(%q) = false for %s", v, c.name)
			}
		}
	}
	for _, v := range []string{"", "plain", "a b", "Gebäude Ost 東棟", "e\u0301", "a\u00a0b", "a\u3000b", "<b>&</b>"} {
		if hasLineBreakOrControl(v) {
			t.Errorf("hasLineBreakOrControl(%q) = true, want an ordinary value accepted", v)
		}
	}
}

// nodeACMEConfigSetting is a config that sets the one ACME key to v and nothing
// else. The field is found by the json tag it is bound under, and read back
// through the table's own accessor, so a row bound to another key's field is
// caught here and not by a refusal that names the wrong key.
func nodeACMEConfigSetting(t *testing.T, key, v string) NodeACMEConfig {
	t.Helper()
	var cfg NodeACMEConfig
	fields := reflect.ValueOf(&cfg).Elem()
	for i := range fields.NumField() {
		if strings.Split(fields.Type().Field(i).Tag.Get("json"), ",")[0] != key {
			continue
		}
		fields.Field(i).SetString(v)
		for _, s := range nodeACMESettings {
			if s.key != key {
				continue
			}
			if got := s.value(cfg); got != v {
				t.Fatalf("nodeACMESettings reads %q for %s, want the %q its field holds: the row is bound to another field", got, key, v)
			}
			return cfg
		}
		t.Fatalf("nodeACMESettings has no row for %q", key)
	}
	t.Fatalf("NodeACMEConfig has no field bound to %q", key)
	return cfg
}

// nodeACMEValueBase is a value the key could hold, cut off where a control
// character does its harm: an acmedomainN ends in the comma after which Proxmox's
// property-string parser skips a segment of nothing but whitespace, and acme ends in
// its domain list, which it splits on any whitespace.
func nodeACMEValueBase(key string) string {
	if key == "acme" {
		return "account=default,domains=a.example.com;b.example.com"
	}
	return "domain=a.example.com,"
}

// TestSetNodeACMEConfig_SendsExactly compares the whole form of each kind of
// write, as the options writer's does. Every case is also accepted by
// ValidateNodeACMEConfig.
func TestSetNodeACMEConfig_SendsExactly(t *testing.T) {
	type row struct {
		name string
		cfg  NodeACMEConfig
		want url.Values
	}
	rows := make([]row, 0, 128)
	rows = append(rows, []row{
		// Seven distinct values: a row missing from nodeACMESettings stops its key
		// being sent, and its set-and-clear guard with it.
		{"every setting",
			NodeACMEConfig{ACME: "account=acme-account-01", ACMEDomain0: "d0.example.com", ACMEDomain1: "d1.example.com",
				ACMEDomain2: "d2.example.com", ACMEDomain3: "d3.example.com", ACMEDomain4: "d4.example.com", ACMEDomain5: "d5.example.com"},
			url.Values{"acme": {"account=acme-account-01"}, "acmedomain0": {"d0.example.com"}, "acmedomain1": {"d1.example.com"},
				"acmedomain2": {"d2.example.com"}, "acmedomain3": {"d3.example.com"}, "acmedomain4": {"d4.example.com"},
				"acmedomain5": {"d5.example.com"}}},
		{"an ordinary edit carries no delete and no digest",
			NodeACMEConfig{ACME: "account=default"}, url.Values{"acme": {"account=default"}}},
		{"the digest is passed through",
			NodeACMEConfig{ACMEDomain0: "node.example.com", Digest: nodeConfigTestDigest},
			url.Values{"acmedomain0": {"node.example.com"}, "digest": {nodeConfigTestDigest}}},
		// An empty field is "leave alone": the only spelling of "remove" is `delete`,
		// and it leaves a write beside it alone (one save clears a domain and edits another).
		{"a clear beside another key's write",
			NodeACMEConfig{ACMEDomain0: "node.example.com", Delete: []string{"acmedomain1", "acmedomain2"}},
			url.Values{"acmedomain0": {"node.example.com"}, "delete": {"acmedomain1,acmedomain2"}}},
		{"the account cleared, with a digest", NodeACMEConfig{Delete: []string{"acme"}, Digest: nodeConfigTestDigest},
			url.Values{"delete": {"acme"}, "digest": {nodeConfigTestDigest}}},
		{"the last domain slot, with a digest", NodeACMEConfig{ACMEDomain5: "d5.example.com", Digest: nodeConfigTestDigest},
			url.Values{"acmedomain5": {"d5.example.com"}, "digest": {nodeConfigTestDigest}}},

		// The control-character refusal is not a blanket one.
		{"an acme with a space and other scripts", NodeACMEConfig{ACME: "account=default,domains=bücher.example.com; 東棟.example.com"},
			url.Values{"acme": {"account=default,domains=bücher.example.com; 東棟.example.com"}}},
		{"an acmedomain with a space and other scripts", NodeACMEConfig{ACMEDomain1: "domain=bücher.example.com, plugin=dns-example"},
			url.Values{"acmedomain1": {"domain=bücher.example.com, plugin=dns-example"}}},
		{"a no-break space ends the domains list, by decision", NodeACMEConfig{ACME: nodeACMEValueBase("acme") + "\u00a0"},
			url.Values{"acme": {nodeACMEValueBase("acme") + "\u00a0"}}},
	}...)
	for _, s := range nodeACMESettings {
		rows = append(rows,
			row{"clears " + s.key, NodeACMEConfig{Delete: []string{s.key}}, url.Values{"delete": {s.key}}},
			row{s.key + " without a control character", nodeACMEConfigSetting(t, s.key, nodeACMEValueBase(s.key)),
				url.Values{s.key: {nodeACMEValueBase(s.key)}}})
	}

	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := requireSentOnce(t, setNodeACMEConfig(tt.cfg))
			if req.method != http.MethodPut || req.path != nodeConfigTestPath {
				t.Errorf("sent %s %s, want PUT %s", req.method, req.path, nodeConfigTestPath)
			}
			if !reflect.DeepEqual(req.form, tt.want) {
				t.Errorf("form = %v, want %v", req.form, tt.want)
			}
			if err := ValidateNodeACMEConfig(tt.cfg); err != nil {
				t.Errorf("ValidateNodeACMEConfig = %v, want it to accept what SetNodeACMEConfig sent", err)
			}
		})
	}
}

// TestSetNodeACMEConfig_Refusals: each refusal is made before anything is sent, with
// ErrInvalidInput (a 400), and ValidateNodeACMEConfig says the same words.
//
// Control characters (49e5df4): Proxmox's own checks pass a whitespace-class character
// or a NUL in a whitespace-only segment of a property string (acme as much as an
// acmedomainN), at the end of the domains list, and in split_list's NUL branch, and the
// settings are written raw into a line-oriented file where "\n" alone is refused (a 500,
// shown as a 502). The characters are TestHasLineBreakOrControl's; these hold the places
// and that every setting is checked.
func TestSetNodeACMEConfig_Refusals(t *testing.T) {
	type row struct {
		name string
		cfg  NodeACMEConfig
		msg  string
	}
	control := func(key string) string {
		return strconv.Quote(key) + " cannot contain a line break or a control character"
	}
	notUTF8 := func(key string) string { return strconv.Quote(key) + " is not valid UTF-8" }

	rows := make([]row, 0, 128)
	rows = append(rows, []row{
		{"nothing at all", NodeACMEConfig{}, "nothing to change"},
		{"a digest alone", NodeACMEConfig{Digest: nodeConfigTestDigest}, "nothing to change"},
		{"an empty delete list", NodeACMEConfig{Delete: []string{}}, "nothing to change"},
		{"an empty delete list and a digest", NodeACMEConfig{Delete: []string{}, Digest: nodeConfigTestDigest}, "nothing to change"},
	}...)
	// PUT /nodes/{node}/config applies `delete` to the whole file, so the allow-list
	// keeps a manage:certificate caller from erasing settings that are not ACME's.
	for _, key := range []string{"description", "location", "wakeonlan", "acmedomain6", "ACMEDOMAIN0", "acme,acmeold", ""} {
		rows = append(rows, row{"cannot clear " + strconv.Quote(key), NodeACMEConfig{Delete: []string{key}}, "is not an ACME setting that can be cleared"})
	}
	// PVE applies `delete` after the assignments, so a key in both is dropped silently.
	for _, s := range nodeACMESettings {
		cfg := nodeACMEConfigSetting(t, s.key, "x")
		cfg.Delete = []string{s.key}
		rows = append(rows, row{"sets and clears " + s.key, cfg, "set and cleared"})
	}

	for _, tt := range []struct{ name, key, value string }{
		{"a carriage return in the whitespace-only segment of an acmedomain", "acmedomain0", "domain=a.example.com,\r"},
		{"a carriage return in the whitespace-only segment of acme", "acme", "account=default,\r"},
		{"a carriage return after the last domain of acme", "acme", "account=default,domains=a.example.com;b.example.com\r"},
		{"a NUL between two domains of acme, where split_list splits on NUL alone", "acme", "account=default,domains=a.example.com\x00b.example.com"},
		{"a trailing line feed in an acmedomain", "acmedomain1", "domain=a.example.com,\n"},
		{"a tab in the whitespace-only segment of an acmedomain", "acmedomain2", "domain=a.example.com,\t"},
		{"a line feed between two properties of an acmedomain", "acmedomain3", "domain=a.example.com\nplugin=dns-example"},
		{"a leading line feed in acme", "acme", "\naccount=default"},
		{"an escape sequence in the alias of an acmedomain", "acmedomain4", "domain=a.example.com,alias=_acme\x1b[31m.example.com"},
		{"U+0085 inside the account of acme", "acme", "account=de\u0085fault,domains=a.example.com"},
		{"U+2028 between two properties of an acmedomain", "acmedomain5", "domain=a.example.com\u2028plugin=dns-example"},
	} {
		rows = append(rows, row{tt.name, nodeACMEConfigSetting(t, tt.key, tt.value), control(tt.key)})
	}
	// Every kind of character through the send path, on the key whose value ends in a list.
	for _, c := range nodeConfigControlCharacters {
		rows = append(rows, row{"acme with " + c.name, nodeACMEConfigSetting(t, "acme", nodeACMEValueBase("acme")+c.char), control("acme")})
	}
	for _, s := range nodeACMESettings {
		rows = append(rows,
			row{s.key + " with a control character", nodeACMEConfigSetting(t, s.key, nodeACMEValueBase(s.key)+"\r"), control(s.key)},
			row{s.key + " with invalid UTF-8", nodeACMEConfigSetting(t, s.key, nodeACMEValueBase(s.key)+"\xff"), notUTF8(s.key)})
	}
	rows = append(rows, row{"a bad value is the one named, beside good ones",
		NodeACMEConfig{ACME: "account=default", ACMEDomain1: "domain=a.example.com", ACMEDomain3: "domain=b.example.com,\r"}, control("acmedomain3")})
	// The client refuses these for its direct callers: over HTTP, encoding/json has
	// already replaced a bad byte with U+FFFD before the handler sees the body.
	for _, bad := range []struct{ name, v string }{
		{"a lone continuation byte", "a\x80b"}, {"a truncated sequence", "a\xc3"}, {"an overlong encoding", "a\xc0\xafb"},
		{"a UTF-16 surrogate", "a\xed\xa0\x80b"},
	} {
		rows = append(rows, row{"acme with " + bad.name, nodeACMEConfigSetting(t, "acme", nodeACMEValueBase("acme")+bad.v), notUTF8("acme")})
	}

	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := requireRefusedUnsent(t, setNodeACMEConfig(tt.cfg), ErrInvalidInput)
			verr := ValidateNodeACMEConfig(tt.cfg)
			if err == nil || verr == nil {
				t.Fatalf("SetNodeACMEConfig = %v, ValidateNodeACMEConfig = %v: both must refuse", err, verr)
			}
			if !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("err = %v, want it to say %q and not another refusal", err, tt.msg)
			}
			if verr.Error() != err.Error() || !errors.Is(verr, ErrInvalidInput) {
				t.Errorf("ValidateNodeACMEConfig says %q, SetNodeACMEConfig %q: they are one decision", verr, err)
			}
		})
	}
}

// TestSetNodeACMEConfig_AcceptsUnicodeSpaceSeparators pins a decision so that a change
// of it is a deliberate one: the Unicode space separators that are not control
// characters (U+00A0, U+1680, U+2000-U+200A, U+202F, U+205F, U+3000) are accepted in an
// ACME setting, as the plain space is. They have no line structure or terminal effect,
// and refusing them would be stricter than the options form, whose location name is
// free text. A walk over unicode.White_Space holds the two lists to the whole property:
// a member a later Unicode version adds fails here until it is named in one of them.
func TestSetNodeACMEConfig_AcceptsUnicodeSpaceSeparators(t *testing.T) {
	accepted := []rune{0x00A0, 0x1680, 0x202F, 0x205F, 0x3000}
	for r := rune(0x2000); r <= 0x200A; r++ {
		accepted = append(accepted, r)
	}
	refused := []rune{0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0085, 0x2028, 0x2029}

	listed := map[rune]bool{}
	for _, r := range accepted {
		listed[r] = true
		if !unicode.Is(unicode.White_Space, r) || hasLineBreakOrControl(string(r)) {
			t.Errorf("U+%04X is listed as accepted, but is not White_Space or hasLineBreakOrControl refuses it", r)
		}
	}
	for _, r := range refused {
		listed[r] = true
		if !unicode.Is(unicode.White_Space, r) || !hasLineBreakOrControl(string(r)) {
			t.Errorf("U+%04X is listed as refused, but is not White_Space or hasLineBreakOrControl accepts it", r)
		}
	}
	undecided := func(lo, hi, stride rune) {
		for r := lo; r <= hi; r += stride {
			if r != ' ' && !listed[r] {
				t.Errorf("Unicode White_Space has U+%04X, which is listed as neither accepted nor refused: decide whether an "+
					"ACME setting may hold it and name it in a list; to refuse it, change buildNodeACMEForm as well", r)
			}
		}
	}
	for _, rg := range unicode.White_Space.R16 {
		undecided(rune(rg.Lo), rune(rg.Hi), rune(rg.Stride))
	}
	for _, rg := range unicode.White_Space.R32 {
		undecided(rune(rg.Lo), rune(rg.Hi), rune(rg.Stride))
	}

	// Where a value is not held to an anchored format: a whitespace-only segment of
	// a property string, and the end of the domains list.
	for _, p := range []struct{ key, base string }{
		{"acmedomain0", "domain=a.example.com,"},
		{"acme", "account=default,"},
		{"acme", "account=default,domains=a.example.com;b.example.com"},
	} {
		for _, r := range accepted {
			if err := ValidateNodeACMEConfig(nodeACMEConfigSetting(t, p.key, p.base+string(r))); err != nil {
				t.Errorf("U+%04X after %q in %s was refused: %v", r, p.base, p.key, err)
			}
		}
		for _, r := range refused {
			if err := ValidateNodeACMEConfig(nodeACMEConfigSetting(t, p.key, p.base+string(r))); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("U+%04X after %q in %s: err = %v, want ErrInvalidInput", r, p.base, p.key, err)
			}
		}
	}
}

// TestSetNodeACMEConfig_RefusesABodyOverTheLimit: the ACME write has the options
// writer's size refusal (512 KiB, encoded), at the same boundary.
func TestSetNodeACMEConfig_RefusesABodyOverTheLimit(t *testing.T) {
	const limit = 512 * 1024
	const prefix = len("acmedomain0=")
	for _, tt := range []struct {
		name    string
		size    int
		refused bool
	}{{"exactly the limit", limit, false}, {"one byte over", limit + 1, true}} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NodeACMEConfig{ACMEDomain0: strings.Repeat("a", tt.size-prefix)}
			verr := ValidateNodeACMEConfig(cfg)
			if !tt.refused {
				requireSentOnce(t, setNodeACMEConfig(cfg))
				if verr != nil {
					t.Errorf("ValidateNodeACMEConfig = %v, want it to accept what SetNodeACMEConfig accepts", verr)
				}
				return
			}
			requireRefusedUnsent(t, setNodeACMEConfig(cfg), ErrRequestTooLarge)
			if !errors.Is(verr, ErrRequestTooLarge) {
				t.Errorf("ValidateNodeACMEConfig = %v, want ErrRequestTooLarge", verr)
			}
		})
	}
}

// TestNodeACMESettingsCoverTheStruct: adding an ACMEDomain6 field compiles, is
// silently never sent and cannot be cleared. This reads the struct, promoted and
// untagged fields included, and not a second hand-written list of the names.
func TestNodeACMESettingsCoverTheStruct(t *testing.T) {
	keys := make([]string, 0, len(nodeACMESettings))
	for _, s := range nodeACMESettings {
		keys = append(keys, s.key)
	}
	requireSettingsCoverStruct(t, reflect.TypeOf(NodeACMEConfig{}), keys, deletableNodeACMEKeys)
}

// TestNodeACMESetKeys_NamesKeysWithoutValues: the values are ACME domains and
// view:audit is granted to every Viewer, so the audit row carries names only.
func TestNodeACMESetKeys_NamesKeysWithoutValues(t *testing.T) {
	keys := NodeACMESetKeys(NodeACMEConfig{ACME: "account=acme-account-01", ACMEDomain2: "d2.example.com"})
	if want := []string{"acme", "acmedomain2"}; !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for _, k := range keys {
		if strings.Contains(k, "example.com") || strings.Contains(k, "account=") {
			t.Errorf("%q carries a value into the audit log, not just a key name", k)
		}
	}
	if got := NodeACMESetKeys(NodeACMEConfig{}); len(got) != 0 {
		t.Errorf("an empty config names %v, want nothing", got)
	}
}
