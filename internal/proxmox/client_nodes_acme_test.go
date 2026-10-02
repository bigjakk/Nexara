package proxmox

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

// TestSetNodeACMEConfig_ClearsViaDelete pins the only way to remove an ACME
// setting from a node.
//
// Every ACME key is optional in PVE::NodeConfig's schema, so an empty string is
// not a value that means "remove" — set_options assigns only the keys present
// in the request, and a key that is absent keeps whatever it held. Before this
// existed, a domain could be added and edited but never taken off: the write
// returned 200 and the old value came straight back on the next read.
func TestSetNodeACMEConfig_ClearsViaDelete(t *testing.T) {
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	err := c.SetNodeACMEConfig(context.Background(), "pve1", NodeACMEConfig{
		ACMEDomain0: "node.example.com",
		Delete:      []string{"acmedomain1", "acmedomain2"},
	})
	if err != nil {
		t.Fatalf("SetNodeACMEConfig: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("issued %d requests, want 1", len(*seen))
	}
	form := (*seen)[0]
	if got := form.Get("delete"); got != "acmedomain1,acmedomain2" {
		t.Errorf("delete = %q, want the comma-separated list PVE's pve-configid-list expects", got)
	}
	// The rest of the form has to survive the unset — clearing one domain and
	// editing another is a single save in the UI.
	if got := form.Get("acmedomain0"); got != "node.example.com" {
		t.Errorf("acmedomain0 = %q, want it sent alongside the delete", got)
	}
}

// TestSetNodeACMEConfig_OmitsDeleteWhenEmpty guards the default: an ordinary
// edit must not carry a `delete` key at all, or an empty list would start
// meaning something.
func TestSetNodeACMEConfig_OmitsDeleteWhenEmpty(t *testing.T) {
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	if err := c.SetNodeACMEConfig(context.Background(), "pve1", NodeACMEConfig{
		ACME: "account=default",
	}); err != nil {
		t.Fatalf("SetNodeACMEConfig: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("issued %d requests, want 1", len(*seen))
	}
	if _, ok := (*seen)[0]["delete"]; ok {
		t.Errorf("delete sent for an edit that clears nothing: %q", (*seen)[0].Get("delete"))
	}
	if _, ok := (*seen)[0]["digest"]; ok {
		t.Errorf("digest sent when none was supplied: %q", (*seen)[0].Get("digest"))
	}
}

// TestSetNodeACMEConfig_SendsDigest covers the compare-and-swap. PVE's
// assert_if_modified is skipped unless both digests are set, so passing one
// through is the difference between a blind overwrite and a safe one.
func TestSetNodeACMEConfig_SendsDigest(t *testing.T) {
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	if err := c.SetNodeACMEConfig(context.Background(), "pve1", NodeACMEConfig{
		ACMEDomain0: "node.example.com",
		Digest:      "da39a3ee5e6b4b0d3255bfef95601890afd80709",
	}); err != nil {
		t.Fatalf("SetNodeACMEConfig: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("issued %d requests, want 1", len(*seen))
	}
	if got := (*seen)[0].Get("digest"); got != "da39a3ee5e6b4b0d3255bfef95601890afd80709" {
		t.Errorf("digest = %q", got)
	}
}

// TestSetNodeACMEConfig_RejectsUndeletableKeys is the reason the allow-list
// exists. PUT /nodes/{node}/config applies `delete` to the whole node config,
// and the handler binds NodeACMEConfig straight from the request body — so
// without this, any API client holding manage:certificate could erase settings
// that have nothing to do with ACME.
func TestSetNodeACMEConfig_RejectsUndeletableKeys(t *testing.T) {
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	for _, key := range []string{
		"description",  // a real node config key, not ours to clear
		"location",     // ditto
		"wakeonlan",    // ditto
		"acmedomain6",  // one past $MAXDOMAINS
		"ACMEDOMAIN0",  // the allow-list is not case-folded, and neither is PVE
		"acme,acmeold", // a second key smuggled through one entry
		"",
	} {
		err := c.SetNodeACMEConfig(context.Background(), "pve1", NodeACMEConfig{
			Delete: []string{key},
		})
		if err == nil {
			t.Errorf("delete %q accepted, want rejection", key)
			continue
		}
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("delete %q: err = %v, want ErrInvalidInput (so the handler answers 400)", key, err)
		}
	}
	if len(*seen) != 0 {
		t.Errorf("%d request(s) reached Proxmox; a rejected key must not be sent at all", len(*seen))
	}
}

// TestSetNodeACMEConfig_RejectsSetAndClearTogether covers a footgun specific to
// this endpoint. PVE's set_options assigns every supplied key first and applies
// `delete` afterwards, so a key in both is silently removed — the caller gets a
// 200 and the value they just wrote is gone. Unlike SectionConfig's
// delete_from_config, PVE raises nothing here, so this client has to.
func TestSetNodeACMEConfig_RejectsSetAndClearTogether(t *testing.T) {
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	err := c.SetNodeACMEConfig(context.Background(), "pve1", NodeACMEConfig{
		ACMEDomain0: "node.example.com",
		Delete:      []string{"acmedomain0"},
	})
	if err == nil {
		t.Fatal("setting and clearing acmedomain0 together was accepted; PVE would drop the value silently")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("err = %v, want ErrInvalidInput", err)
	}
	if len(*seen) != 0 {
		t.Errorf("%d request(s) reached Proxmox, want none", len(*seen))
	}
}

// TestSetNodeACMEConfig_RefusesAWriteThatChangesNothing: a request that sets nothing
// and clears nothing is refused with ErrInvalidInput before anything is sent, as
// SetNodeOptions refuses its own. set_options would still rewrite the whole node
// config file for it, moving its digest under every open dialog, and the caller would
// be left an audit row that says "updated" and names nothing. A digest is neither a
// setting nor a clear, so a digest alone is such a request; an empty field means
// "leave alone", so it sets nothing either; an empty list clears nothing.
// ValidateNodeACMEConfig is the same decision, asked before a write.
func TestSetNodeACMEConfig_RefusesAWriteThatChangesNothing(t *testing.T) {
	const digest = "da39a3ee5e6b4b0d3255bfef95601890afd80709"

	for _, tt := range []struct {
		name    string
		cfg     NodeACMEConfig
		refused bool
	}{
		{"nothing at all", NodeACMEConfig{}, true},
		{"a digest alone", NodeACMEConfig{Digest: digest}, true},
		{"an empty list to clear", NodeACMEConfig{Delete: []string{}}, true},
		{"an empty list to clear and a digest", NodeACMEConfig{Delete: []string{}, Digest: digest}, true},
		{"one setting", NodeACMEConfig{ACME: "account=default"}, false},
		{"the last domain slot and a digest", NodeACMEConfig{ACMEDomain5: "d5.example.com", Digest: digest}, false},
		{"one key to clear", NodeACMEConfig{Delete: []string{"acmedomain1"}}, false},
		{"the account to clear and a digest", NodeACMEConfig{Delete: []string{"acme"}, Digest: digest}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newFormCaptureServer(t)
			c := newTestClient(t, srv.URL)

			err := c.SetNodeACMEConfig(context.Background(), "pve1", tt.cfg)
			verr := ValidateNodeACMEConfig(tt.cfg)
			if !tt.refused {
				if err != nil || verr != nil {
					t.Fatalf("SetNodeACMEConfig = %v, ValidateNodeACMEConfig = %v; want both to accept it", err, verr)
				}
				if len(*seen) != 1 {
					t.Errorf("issued %d requests, want 1", len(*seen))
				}
				return
			}
			for who, got := range map[string]error{"SetNodeACMEConfig": err, "ValidateNodeACMEConfig": verr} {
				if !errors.Is(got, ErrInvalidInput) {
					t.Errorf("%s = %v, want ErrInvalidInput so the handler answers 400", who, got)
				} else if !strings.Contains(got.Error(), "nothing to change") {
					t.Errorf("%s = %v, want it to say there is nothing to change", who, got)
				}
			}
			if len(*seen) != 0 {
				t.Errorf("%d request(s) reached Proxmox for a write that changes nothing", len(*seen))
			}
		})
	}
}

// nodeConfigControlCharacters is every kind of character the control-character
// refusal has to name: the C0 range (a line feed, a carriage return, a tab, a
// vertical tab, a form feed, NUL and ESC are each one of it), DEL, the C1 range
// (U+0085, the next-line character, and U+009B, the control sequence introducer,
// are two of it), and U+2028 and U+2029, the Unicode line and paragraph
// separators, which unicode.IsControl does not count.
var nodeConfigControlCharacters = []struct{ name, char string }{
	{"a line feed", "\n"},
	{"a carriage return", "\r"},
	{"a tab", "\t"},
	{"a vertical tab", "\v"},
	{"a form feed", "\f"},
	{"a NUL", "\x00"},
	{"ESC", "\x1b"},
	{"DEL", "\x7f"},
	{"U+0085, the next-line character", "\u0085"},
	{"U+009B, the C1 control sequence introducer", "\u009b"},
	{"U+2028, the line separator", "\u2028"},
	{"U+2029, the paragraph separator", "\u2029"},
}

// nodeACMEConfigSetting is a config that sets the one ACME key, to v, and nothing
// else. The field is found by the json tag it is bound under, so one helper serves
// every row of nodeACMESettings, and a key added to the table is held to the tests
// below without a second hand-written list of the keys. The table's own accessor
// reads the value back, so a row bound to another key's field is caught here, and
// not by a refusal that names the wrong key.
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

// nodeACMEValueBase is a value the key could hold, cut off where a control character
// does its harm, and the one the tests below add it to. An acmedomainN ends in the
// comma after which Proxmox's property-string parser skips a segment of nothing but
// whitespace; acme ends in its domain list, which Proxmox reads by splitting on any
// whitespace. A "\r" appended to it is one of the shapes that Proxmox's own checks pass
// (see TestSetNodeACMEConfig_RefusesControlCharacters), and the same value without it
// is the control the refusal is compared with.
func nodeACMEValueBase(key string) string {
	if key == "acme" {
		return "account=default,domains=a.example.com;b.example.com"
	}
	return "domain=a.example.com,"
}

// TestSetNodeACMEConfig_RefusesControlCharacters: a line break or any other control
// character in an ACME setting is refused with ErrInvalidInput, before anything is
// sent, by SetNodeACMEConfig and by ValidateNodeACMEConfig alike, for every setting
// nodeACMESettings names. It is SetNodeOptions's refusal, for the same reason, with
// the same words.
//
// Proxmox does not refuse it itself, or not in a way a caller can use. The settings
// are written raw into a line-oriented file, and write_node_config (pve-manager's
// PVE/NodeConfig.pm) dies on "\n" and on nothing else. What reaches it past Proxmox's
// own checks is a whitespace-class character or a NUL, in three places where a value
// is not held to an anchored format:
//
//   - a segment of a property string of nothing but whitespace, in acme as in an
//     acmedomainN. parse_property_string (pve-common's PVE/JSONSchema.pm) skips it, so
//     "domain=a.example.com,\r" and "account=default,\r" are valid, and the "\r" is
//     written. A "\n" there is valid too, and is what the write refuses with a plain
//     500, which this API would show as a 502.
//   - the domains list of acme. pve-acme-domain-list is the list form of
//     pve-acme-domain, and check_format (pve-common's PVE/JSONSchema.pm) checks a list
//     form by split_list (PVE/ParseUtils.pm) and a check of each entry, so a
//     whitespace-class character at its end is a separator that yields no entry, and
//     "account=default,domains=a.example.com;b.example.com\r" is valid.
//   - the NUL branch of split_list: a list that holds a NUL is split on NUL alone, so
//     "account=default,domains=a.example.com\x00b.example.com" is valid.
//
// So what reached the file, and is refused now, was TAB, VT, FF, CR, NEL, U+2028,
// U+2029 and NUL, and the refusal of those is the fix. ESC, CSI, OSC and DEL never got
// that far, because the anchored formats reject them wherever a value is checked
// (pve-acme-domain and pve-acme-alias in PVE/NodeConfig.pm, and pve-configid for the
// plugin there and for the account, which defaultACMEAccountName in
// internal/api/handlers/acme.go records from pve-manager's PVE/CertHelpers.pm), so
// refusing them is defence in depth. What the caller is told changes to match: a 400
// where there was a write for the first group, a 400 in place of a 502 for "\n", and
// for ESC, CSI, OSC and DEL the same status, which Proxmox's own 400 had given and
// which now comes from Nexara, before anything is sent, in its own words. The cases
// below are those, in each place and on every setting, and the rest of the control
// characters, beside values that pass.
func TestSetNodeACMEConfig_RefusesControlCharacters(t *testing.T) {
	// refused holds both methods to the same answer for cfg, whose one bad value is
	// under key, and to sending nothing.
	refused := func(t *testing.T, key string, cfg NodeACMEConfig) {
		t.Helper()
		srv, seen := newFormCaptureServer(t)
		c := newTestClient(t, srv.URL)

		serr := c.SetNodeACMEConfig(context.Background(), "pve-01", cfg)
		verr := ValidateNodeACMEConfig(cfg)
		for who, got := range map[string]error{"SetNodeACMEConfig": serr, "ValidateNodeACMEConfig": verr} {
			switch {
			case got == nil:
				t.Errorf("%s accepted a control character in %s; it would be written raw into the node config", who, key)
			case !errors.Is(got, ErrInvalidInput):
				t.Errorf("%s = %v, want ErrInvalidInput (so the handler answers 400)", who, got)
			case !strings.Contains(got.Error(), "line break or a control character") || !strings.Contains(got.Error(), `"`+key+`"`):
				t.Errorf("%s = %v, want the control-character refusal, naming %s", who, got, key)
			}
		}
		if serr != nil && verr != nil && serr.Error() != verr.Error() {
			t.Errorf("SetNodeACMEConfig says %q and ValidateNodeACMEConfig %q: they are one decision", serr, verr)
		}
		if len(*seen) != 0 {
			t.Errorf("%d request(s) reached Proxmox, want none: %v", len(*seen), *seen)
		}
	}

	// The three places Proxmox's own checks pass a whitespace-class character (the
	// whitespace-only segment of an acmedomain and of acme, the end of the domains list,
	// the NUL branch of split_list), the "\n" it refuses itself with a 500, and the same
	// refusal anywhere in a value and not only at its end.
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
		t.Run(tt.name, func(t *testing.T) {
			refused(t, tt.key, nodeACMEConfigSetting(t, tt.key, tt.value))
		})
	}

	// Every setting, and every kind of character, so that neither a key the check
	// skips nor a kind of character it does not name goes unnoticed.
	for _, s := range nodeACMESettings {
		for _, ch := range nodeConfigControlCharacters {
			t.Run(s.key+"/"+ch.name, func(t *testing.T) {
				refused(t, s.key, nodeACMEConfigSetting(t, s.key, nodeACMEValueBase(s.key)+ch.char))
			})
		}
	}

	// The bad value is the one that is named, beside values that are fine.
	t.Run("a bad value beside good ones", func(t *testing.T) {
		refused(t, "acmedomain3", NodeACMEConfig{
			ACME:        "account=default",
			ACMEDomain1: "domain=a.example.com",
			ACMEDomain3: "domain=b.example.com,\r",
		})
	})

	// The refusal is not a blanket one, and the requests above are refused for the
	// character and for nothing else: the same values without it, and ordinary text —
	// a space, letters of another script — are accepted and sent as they are.
	type acceptedCase struct{ name, key, value string }
	accepted := make([]acceptedCase, 0, 2+len(nodeACMESettings))
	accepted = append(accepted,
		acceptedCase{"an acme with a space and other scripts", "acme", "account=default,domains=bücher.example.com; 東棟.example.com"},
		acceptedCase{"an acmedomain with a space and other scripts", "acmedomain1", "domain=bücher.example.com, plugin=dns-example"},
	)
	for _, s := range nodeACMESettings {
		accepted = append(accepted, acceptedCase{s.key + " without the control character", s.key, nodeACMEValueBase(s.key)})
	}
	for _, tt := range accepted {
		t.Run("accepts "+tt.name, func(t *testing.T) {
			cfg := nodeACMEConfigSetting(t, tt.key, tt.value)
			srv, seen := newFormCaptureServer(t)
			c := newTestClient(t, srv.URL)

			if err := ValidateNodeACMEConfig(cfg); err != nil {
				t.Fatalf("ValidateNodeACMEConfig: %v", err)
			}
			if err := c.SetNodeACMEConfig(context.Background(), "pve-01", cfg); err != nil {
				t.Fatalf("SetNodeACMEConfig: %v", err)
			}
			if len(*seen) != 1 {
				t.Fatalf("issued %d requests, want 1", len(*seen))
			}
			if got := (*seen)[0]; len(got) != 1 || got.Get(tt.key) != tt.value {
				t.Errorf("sent %v, want %s = %q and nothing else", got, tt.key, tt.value)
			}
		})
	}
}

// TestSetNodeACMEConfig_AcceptsUnicodeSpaceSeparators pins a decision, so that a
// change of it is a deliberate one: the Unicode space separators that are not control
// characters — U+00A0, U+1680, U+2000 to U+200A, U+202F, U+205F and U+3000 — are NOT
// refused in an ACME setting, any more than the plain space is.
//
// Perl's \s reads them as whitespace in the two places a value is not held to an
// anchored format, a whitespace-only segment of a property string and the domains list
// of acme (see TestSetNodeACMEConfig_RefusesControlCharacters), so they can reach the
// file, as a "\r" can. They have no line structure and no terminal effect, though,
// every anchored position rejects them, and refusing them would be stricter than the
// options form's refusal, which this one matches and which hasLineBreakOrControl
// makes: a location's name is free text and holds them legitimately, so that function
// is not to be widened for them. In the domains list they do what a plain space does:
// Proxmox's check splits that list on any whitespace (split_list, pve-common's
// PVE/ParseUtils.pm), but get_acme_conf (PVE/NodeConfig.pm) reads the stored value
// split on ";" only, so a list validated as N domains is one identifier when it is
// read. That is Proxmox's, it is reachable with a plain space, and refusing these would
// not close it. The characters listed as refused below do end a line or are controls,
// and are refused in the same places beside them, which is what shows that the
// acceptance is a result and not a check that cannot fail.
//
// The decision is written out below as two lists, and a walk over unicode.White_Space,
// the property Perl's \s reads, holds them to the whole of it: every member is the plain
// space, or listed as accepted, or listed as refused and refused by
// hasLineBreakOrControl. A member that a later Unicode version adds is accepted in
// production, since nothing refuses it, and this walk is what makes someone notice: it
// fails until the character is named in one of the lists, so that it is decided and not
// accepted with nobody having looked.
func TestSetNodeACMEConfig_AcceptsUnicodeSpaceSeparators(t *testing.T) {
	// The decision, written out as numbers: the space separators that are not control
	// characters, which are accepted, and the rest of White_Space, which is TAB, LF, VT,
	// FF, CR, NEL, U+2028 and U+2029 and which hasLineBreakOrControl refuses.
	accepted := []rune{0x00A0, 0x1680, 0x202F, 0x205F, 0x3000}
	for r := rune(0x2000); r <= 0x200A; r++ {
		accepted = append(accepted, r)
	}
	refused := []rune{0x0009, 0x000A, 0x000B, 0x000C, 0x000D, 0x0085, 0x2028, 0x2029}

	listed := make(map[rune]bool, len(accepted)+len(refused))
	for _, r := range accepted {
		listed[r] = true
		if !unicode.Is(unicode.White_Space, r) {
			t.Errorf("U+%04X is listed here as accepted, and Unicode does not call it White_Space", r)
		}
		if hasLineBreakOrControl(string(r)) {
			t.Errorf("U+%04X is listed here as accepted, and hasLineBreakOrControl refuses it", r)
		}
	}
	for _, r := range refused {
		listed[r] = true
		if !unicode.Is(unicode.White_Space, r) {
			t.Errorf("U+%04X is listed here as refused, and Unicode does not call it White_Space", r)
		}
		if !hasLineBreakOrControl(string(r)) {
			t.Errorf("U+%04X is listed here as refused, and hasLineBreakOrControl does not refuse it", r)
		}
	}
	undecided := func(lo, hi, stride rune) {
		for r := lo; r <= hi; r += stride {
			if r != ' ' && !listed[r] {
				t.Errorf("Unicode White_Space has U+%04X, which this test lists as neither accepted nor refused: "+
					"decide whether an ACME setting may hold it, and name it in one of the two lists; "+
					"for a refusal change buildNodeACMEForm as well", r)
			}
		}
	}
	for _, rg := range unicode.White_Space.R16 {
		undecided(rune(rg.Lo), rune(rg.Hi), rune(rg.Stride))
	}
	for _, rg := range unicode.White_Space.R32 {
		undecided(rune(rg.Lo), rune(rg.Hi), rune(rg.Stride))
	}

	// The places a value is not held to an anchored format: where the character is a
	// whitespace-only segment of a property string, and where it ends the domains list.
	placements := []struct{ name, key, base string }{
		{"the whitespace-only segment of an acmedomain", "acmedomain0", "domain=a.example.com,"},
		{"the whitespace-only segment of acme", "acme", "account=default,"},
		{"the end of the domains list of acme", "acme", "account=default,domains=a.example.com;b.example.com"},
	}
	for _, r := range accepted {
		for _, p := range placements {
			t.Run(fmt.Sprintf("U+%04X in %s", r, p.name), func(t *testing.T) {
				value := p.base + string(r)
				cfg := nodeACMEConfigSetting(t, p.key, value)
				srv, seen := newFormCaptureServer(t)
				c := newTestClient(t, srv.URL)

				if err := ValidateNodeACMEConfig(cfg); err != nil {
					t.Fatalf("ValidateNodeACMEConfig: %v", err)
				}
				if err := c.SetNodeACMEConfig(context.Background(), "pve-01", cfg); err != nil {
					t.Fatalf("SetNodeACMEConfig: %v", err)
				}
				if len(*seen) != 1 {
					t.Fatalf("issued %d requests, want 1", len(*seen))
				}
				if got := (*seen)[0]; len(got) != 1 || got.Get(p.key) != value {
					t.Errorf("sent %v, want %s = %q and nothing else", got, p.key, value)
				}
			})
		}
	}

	// The same places refuse what is listed as refused: it ends a line, or is a control.
	for _, r := range refused {
		for _, p := range placements {
			t.Run(fmt.Sprintf("U+%04X in %s is refused", r, p.name), func(t *testing.T) {
				cfg := nodeACMEConfigSetting(t, p.key, p.base+string(r))
				if err := ValidateNodeACMEConfig(cfg); !errors.Is(err, ErrInvalidInput) {
					t.Errorf("ValidateNodeACMEConfig = %v, want ErrInvalidInput", err)
				}
			})
		}
	}
}

// TestSetNodeACMEConfig_RefusesInvalidUTF8: a value that is not valid UTF-8 is
// refused whichever ACME setting carries it, as SetNodeOptions refuses its own. It
// serves the direct callers of the client and not the HTTP route: over HTTP,
// encoding/json has already replaced a bad byte with U+FFFD before the handler sees
// the body, so no request can reach this refusal, and the route's own answer is for
// control characters only.
func TestSetNodeACMEConfig_RefusesInvalidUTF8(t *testing.T) {
	invalid := map[string]string{
		"a lone continuation byte":   "a\x80b",
		"a truncated sequence":       "a\xc3",
		"an overlong encoding":       "a\xc0\xafb",
		"a UTF-16 surrogate":         "a\xed\xa0\x80b",
		"a byte that is never UTF-8": "a\xffb",
	}
	for _, s := range nodeACMESettings {
		for name, bad := range invalid {
			t.Run(s.key+"/"+name, func(t *testing.T) {
				cfg := nodeACMEConfigSetting(t, s.key, nodeACMEValueBase(s.key)+bad)
				srv, seen := newFormCaptureServer(t)
				c := newTestClient(t, srv.URL)

				serr := c.SetNodeACMEConfig(context.Background(), "pve-01", cfg)
				verr := ValidateNodeACMEConfig(cfg)
				for who, got := range map[string]error{"SetNodeACMEConfig": serr, "ValidateNodeACMEConfig": verr} {
					switch {
					case got == nil:
						t.Errorf("%s accepted a value that is not valid UTF-8 in %s", who, s.key)
					case !errors.Is(got, ErrInvalidInput):
						t.Errorf("%s = %v, want ErrInvalidInput (so the handler answers 400)", who, got)
					case !strings.Contains(got.Error(), "not valid UTF-8") || !strings.Contains(got.Error(), `"`+s.key+`"`):
						t.Errorf("%s = %v, want it to name %s and say it is not valid UTF-8", who, got, s.key)
					}
				}
				if serr != nil && verr != nil && serr.Error() != verr.Error() {
					t.Errorf("SetNodeACMEConfig says %q and ValidateNodeACMEConfig %q: they are one decision", serr, verr)
				}
				if len(*seen) != 0 {
					t.Errorf("%d request(s) reached Proxmox, want none: %v", len(*seen), *seen)
				}
			})
		}
	}

	t.Run("multibyte text that is valid is not refused", func(t *testing.T) {
		srv, seen := newFormCaptureServer(t)
		c := newTestClient(t, srv.URL)
		const domain = "domain=bücher.example.com"
		if err := c.SetNodeACMEConfig(context.Background(), "pve-01", NodeACMEConfig{ACMEDomain0: domain}); err != nil {
			t.Fatalf("SetNodeACMEConfig: %v", err)
		}
		if len(*seen) != 1 || (*seen)[0].Get("acmedomain0") != domain {
			t.Errorf("sent %v, want acmedomain0 = %q", *seen, domain)
		}
	})
}

// TestNodeACMESettingsCoverTheStruct is the drift guard, and it reads the
// struct rather than a second hand-written list.
//
// The earlier version of this test compared the allow-list against a hardcoded
// slice of the same seven names. That is not circular, but it never touches
// NodeACMEConfig: adding an ACMEDomain6 field compiles, is silently never sent,
// cannot be cleared, and the test still passes — the exact bug this change
// fixes, one field later.
//
// Reflecting over json tags alone was not enough either. A field with no tag,
// and a field promoted from an embedded struct, are both bindable and were both
// invisible to the tag scan, so the walk below covers all three shapes.
func TestNodeACMESettingsCoverTheStruct(t *testing.T) {
	// delete and digest are request plumbing, not ACME settings: they are not
	// written as config keys and cannot themselves be cleared.
	plumbing := map[string]bool{"delete": true, "digest": true}

	inTable := make(map[string]bool, len(nodeACMESettings))
	for _, s := range nodeACMESettings {
		inTable[s.key] = true
	}

	// Walk promoted fields too, and fall back to the field name when there is
	// no json tag. Both are ways a field can be bindable — encoding/json
	// matches an untagged field by name, and an embedded struct's fields are
	// promoted — so a check that skipped either would call a settable key
	// covered while it was silently never sent.
	var fields []string
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		for i := range typ.NumField() {
			f := typ.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			if tag == "-" {
				continue
			}
			if f.Anonymous && f.Type.Kind() == reflect.Struct && tag == "" {
				walk(f.Type)
				continue
			}
			if tag == "" {
				tag = strings.ToLower(f.Name)
			}
			if plumbing[tag] {
				continue
			}
			fields = append(fields, tag)
		}
	}
	walk(reflect.TypeOf(NodeACMEConfig{}))

	for _, tag := range fields {
		if !inTable[tag] {
			t.Errorf("NodeACMEConfig has %q but nodeACMESettings does not: it would never be sent, and never be clearable", tag)
		}
		if !deletableNodeACMEKeys[tag] {
			t.Errorf("%q is a settable ACME key but is not in the delete allow-list", tag)
		}
	}
	if len(fields) != len(nodeACMESettings) {
		t.Errorf("struct has %d ACME fields (%v) but the table has %d rows — a row names a key the struct dropped",
			len(fields), fields, len(nodeACMESettings))
	}
}

// TestSetNodeACMEConfig_SendsEveryField is the test whose absence let a
// transcription slip through.
//
// nodeACMESettings maps seven keys to seven struct fields by hand. Dropping one
// row is invisible to every other test here — they set at most two fields — and
// it breaks two things at once: the key silently stops being sent, and because
// the set-and-clear check reads the same table, that key's delete guard stops
// firing too. Seven distinct sentinels is what makes a missing row fail.
func TestSetNodeACMEConfig_SendsEveryField(t *testing.T) {
	srv, seen := newFormCaptureServer(t)
	c := newTestClient(t, srv.URL)

	want := map[string]string{
		"acme":        "account=acme-account-01",
		"acmedomain0": "d0.example.com",
		"acmedomain1": "d1.example.com",
		"acmedomain2": "d2.example.com",
		"acmedomain3": "d3.example.com",
		"acmedomain4": "d4.example.com",
		"acmedomain5": "d5.example.com",
	}
	err := c.SetNodeACMEConfig(context.Background(), "pve1", NodeACMEConfig{
		ACME:        want["acme"],
		ACMEDomain0: want["acmedomain0"],
		ACMEDomain1: want["acmedomain1"],
		ACMEDomain2: want["acmedomain2"],
		ACMEDomain3: want["acmedomain3"],
		ACMEDomain4: want["acmedomain4"],
		ACMEDomain5: want["acmedomain5"],
	})
	if err != nil {
		t.Fatalf("SetNodeACMEConfig: %v", err)
	}
	if len(*seen) != 1 {
		t.Fatalf("issued %d requests, want 1", len(*seen))
	}
	form := (*seen)[0]
	for k, v := range want {
		if got := form.Get(k); got != v {
			t.Errorf("%s = %q, want %q — the field is not wired into nodeACMESettings", k, got, v)
		}
	}
}

// TestNodeACMESetKeys_NamesKeysWithoutValues pins what reaches the audit log.
// The values are ACME domains and view:audit is granted to every Viewer by
// default, so the row carries names only.
func TestNodeACMESetKeys_NamesKeysWithoutValues(t *testing.T) {
	keys := NodeACMESetKeys(NodeACMEConfig{
		ACME:        "account=acme-account-01",
		ACMEDomain2: "d2.example.com",
	})
	want := []string{"acme", "acmedomain2"}
	if len(keys) != len(want) {
		t.Fatalf("keys = %v, want %v", keys, want)
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("keys[%d] = %q, want %q", i, keys[i], want[i])
		}
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
