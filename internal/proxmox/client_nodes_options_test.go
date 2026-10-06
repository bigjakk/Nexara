package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	nodeConfigTestPath   = "/api2/json/nodes/pve-01/config"
	nodeConfigTestDigest = "da39a3ee5e6b4b0d3255bfef95601890afd80709"
)

func flexPtr(n int) *FlexInt {
	v := FlexInt(n)
	return &v
}

func setNodeOptions(opts NodeOptions) func(*Client) error {
	return func(c *Client) error { return c.SetNodeOptions(context.Background(), "pve-01", opts) }
}

// TestSetNodeOptions_SendsExactly compares the whole form of each kind of write,
// so a dropped nodeOptionSettings row, a stray key and an empty value that should
// have been left out all fail. Every case is also accepted by ValidateNodeOptions.
func TestSetNodeOptions_SendsExactly(t *testing.T) {
	const wake = "mac=02:00:00:00:00:01,bind-interface=vmbr0,broadcast-address=192.0.2.255"
	type row struct {
		name string
		opts NodeOptions
		want url.Values
	}
	rows := make([]row, 0, 128)
	rows = append(rows, []row{
		// Five distinct values: a row missing from the table stops its key being sent.
		{"every setting",
			NodeOptions{StartallOnbootDelay: flexPtr(37), BallooningTarget: flexPtr(63), WakeOnLAN: wake,
				Location: "latitude=0,longitude=0,name=rack01", Description: "sentinel notes\nsecond line"},
			url.Values{"startall-onboot-delay": {"37"}, "ballooning-target": {"63"}, "wakeonlan": {wake},
				"location": {"latitude=0,longitude=0,name=rack01"}, "description": {"sentinel notes\nsecond line"}}},
		{"an ordinary edit carries no delete and no digest",
			NodeOptions{BallooningTarget: flexPtr(75)}, url.Values{"ballooning-target": {"75"}}},
		{"the digest is passed through",
			NodeOptions{BallooningTarget: flexPtr(75), Digest: nodeConfigTestDigest},
			url.Values{"ballooning-target": {"75"}, "digest": {nodeConfigTestDigest}}},

		// 0 is a value: a delay of 0 is how the delay is turned off.
		{"both integers at 0", NodeOptions{StartallOnbootDelay: flexPtr(0), BallooningTarget: flexPtr(0)},
			url.Values{"startall-onboot-delay": {"0"}, "ballooning-target": {"0"}}},
		{"the delay alone at 0", NodeOptions{StartallOnbootDelay: flexPtr(0)}, url.Values{"startall-onboot-delay": {"0"}}},
		{"the target alone at 0", NodeOptions{BallooningTarget: flexPtr(0)}, url.Values{"ballooning-target": {"0"}}},
		{"the delay at 0 beside a string", NodeOptions{StartallOnbootDelay: flexPtr(0), WakeOnLAN: "02:00:00:00:00:01"},
			url.Values{"startall-onboot-delay": {"0"}, "wakeonlan": {"02:00:00:00:00:01"}}},

		// An empty string is "leave alone", not "remove": it must not reach the form.
		{"empty strings beside an integer", NodeOptions{BallooningTarget: flexPtr(75), WakeOnLAN: "", Location: "", Description: ""},
			url.Values{"ballooning-target": {"75"}}},
		{"an empty delete and digest beside an integer", NodeOptions{BallooningTarget: flexPtr(75), Delete: []string{}, Digest: ""},
			url.Values{"ballooning-target": {"75"}}},
		{"absent integers beside a string", NodeOptions{Location: "latitude=0,longitude=0"},
			url.Values{"location": {"latitude=0,longitude=0"}}},
		{"empty strings beside a clear", NodeOptions{Delete: []string{"location"}, WakeOnLAN: "", Description: ""},
			url.Values{"delete": {"location"}}},

		// Clearing is the only way to remove a setting, and it leaves a write beside it alone.
		{"a clear beside another key's write", NodeOptions{BallooningTarget: flexPtr(75), Delete: []string{"wakeonlan", "location"}},
			url.Values{"ballooning-target": {"75"}, "delete": {"wakeonlan,location"}}},
		{"a clear beside another key's write of 0", NodeOptions{StartallOnbootDelay: flexPtr(0), Delete: []string{"ballooning-target"}},
			url.Values{"startall-onboot-delay": {"0"}, "delete": {"ballooning-target"}}},
		{"a digest beside a clear", NodeOptions{Delete: []string{"location"}, Digest: "d1"},
			url.Values{"delete": {"location"}, "digest": {"d1"}}},

		// The control-character refusal is not a blanket one.
		{"a keyed wakeonlan", NodeOptions{WakeOnLAN: wake}, url.Values{"wakeonlan": {wake}}},
		{"a location name with a space", NodeOptions{Location: "latitude=0,longitude=0,name=Site A"},
			url.Values{"location": {"latitude=0,longitude=0,name=Site A"}}},
		{"a location name in other scripts", NodeOptions{Location: "latitude=12.5,longitude=-45.25,name=Gebäude Ost 東棟"},
			url.Values{"location": {"latitude=12.5,longitude=-45.25,name=Gebäude Ost 東棟"}}},
	}...)
	for _, s := range nodeOptionSettings {
		rows = append(rows, row{"clears " + s.key, NodeOptions{Delete: []string{s.key}}, url.Values{"delete": {s.key}}})
	}
	// The description is the one value stored as lines, so it keeps every break and tab.
	for _, notes := range []string{"a\nb", "sentinel notes\nsecond line\n", "\n\nleading blank lines", "a\tb", "a\rb",
		"windows\r\nline endings\r\n", "\n  indented\nsecond line\n", "tab\there\nand a carriage\rreturn\n", "Gebäude Ost 東棟\n"} {
		rows = append(rows, row{"the description keeps " + url.QueryEscape(notes), NodeOptions{Description: notes},
			url.Values{"description": {notes}}})
	}

	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := requireSentOnce(t, setNodeOptions(tt.opts))
			if req.method != http.MethodPut || req.path != nodeConfigTestPath {
				t.Errorf("sent %s %s, want PUT %s", req.method, req.path, nodeConfigTestPath)
			}
			if !reflect.DeepEqual(req.form, tt.want) {
				t.Errorf("form = %v, want %v", req.form, tt.want)
			}
			if err := ValidateNodeOptions(tt.opts); err != nil {
				t.Errorf("ValidateNodeOptions = %v, want it to accept what SetNodeOptions sent", err)
			}
		})
	}
}

// TestSetNodeOptions_Refusals: each refusal is made before anything is sent, with
// ErrInvalidInput (so the handler answers 400), and ValidateNodeOptions, which is
// the same check run ahead of a write, says the same words.
func TestSetNodeOptions_Refusals(t *testing.T) {
	const (
		nothing = "nothing to change"
		both    = "set and cleared"
		control = "line break or a control character"
	)
	type row struct {
		name string
		opts NodeOptions
		msg  string
	}
	// Proxmox would answer these 200 and still rewrite the node config file.
	rows := make([]row, 0, 128)
	rows = append(rows, []row{
		{"nothing at all", NodeOptions{}, nothing},
		{"an empty delete list", NodeOptions{Delete: []string{}}, nothing},
		{"a digest alone", NodeOptions{Digest: nodeConfigTestDigest}, nothing},
		{"a digest beside an empty delete list", NodeOptions{Digest: nodeConfigTestDigest, Delete: []string{}}, nothing},
	}...)

	// PUT /nodes/{node}/config applies `delete` to the whole file, so the allow-list
	// keeps a manage:node caller from erasing the ACME settings beside these. The ACME
	// keys are looped from their table, so a key added there is demanded a refusal.
	for _, s := range nodeACMESettings {
		rows = append(rows, row{"cannot clear the ACME key " + s.key, NodeOptions{Delete: []string{s.key}}, "ACME setting"})
	}
	for _, key := range []string{"digest", "delete", "node", "Description", "WAKEONLAN", " description", "description ",
		"wakeonlan,location", "acmedomain6", ""} {
		rows = append(rows, row{"cannot clear " + strconv.Quote(key), NodeOptions{Delete: []string{key}}, "not a node option that can be cleared"})
	}
	rows = append(rows, row{"one bad entry among good ones refuses all",
		NodeOptions{Delete: []string{"wakeonlan", "acmedomain0", "location"}}, "ACME setting"})

	// PVE assigns every supplied key and applies `delete` afterwards, so a key in both
	// would be dropped silently. The integers are 0 on purpose: a check that read "set"
	// off the rendered value would let the case that matters most through.
	setAndClear := map[string]NodeOptions{
		"startall-onboot-delay": {StartallOnbootDelay: flexPtr(0)},
		"ballooning-target":     {BallooningTarget: flexPtr(0)},
		"wakeonlan":             {WakeOnLAN: "02:00:00:00:00:01"},
		"location":              {Location: "latitude=0,longitude=0"},
		"description":           {Description: "sentinel notes"},
	}
	for _, s := range nodeOptionSettings {
		opts, ok := setAndClear[s.key]
		if !ok {
			t.Fatalf("nodeOptionSettings has %q but this test has no case for setting and clearing it", s.key)
		}
		opts.Delete = []string{s.key}
		rows = append(rows, row{"sets and clears " + s.key, opts, both})
	}

	// Stricter than Proxmox, which dies only on "\n": a whitespace-only segment of a
	// property string slips past its format check, and "\r", "\t" or ESC would be
	// written. Every kind of character is held against both values, here at their end
	// where Proxmox lets one through; the rows after it hold that the check reads the
	// whole value, and beside values that are fine.
	for _, c := range nodeConfigControlCharacters {
		rows = append(rows,
			row{"wakeonlan with " + c.name, NodeOptions{WakeOnLAN: "02:00:00:00:00:01," + c.char}, control},
			row{"location with " + c.name, NodeOptions{Location: "latitude=0,longitude=0,name=rack" + c.char}, control})
	}
	rows = append(rows,
		row{"a line feed inside wakeonlan", NodeOptions{WakeOnLAN: "02:00:00:00:00:01,\nbind-interface=vmbr0"}, control},
		row{"a leading line feed in wakeonlan", NodeOptions{WakeOnLAN: "\n02:00:00:00:00:01"}, control},
		row{"a bad wakeonlan beside good values",
			NodeOptions{StartallOnbootDelay: flexPtr(30), Description: "sentinel notes", WakeOnLAN: "02:00:00:00:00:01,\n"}, control},
		row{"a bad location beside good values",
			NodeOptions{BallooningTarget: flexPtr(75), Description: "sentinel notes", Location: "latitude=0,longitude=0,name=rack\x1b01"}, control},
	)

	// The client refuses it for every future caller; nothing from the API's JSON can be one.
	for _, bad := range []struct{ name, v string }{
		{"a lone continuation byte", "a\x80b"}, {"a truncated sequence", "a\xc3"}, {"an overlong encoding", "a\xc0\xafb"},
		{"a UTF-16 surrogate", "a\xed\xa0\x80b"}, {"a byte that is never UTF-8", "a\xffb"},
	} {
		rows = append(rows, row{"wakeonlan with " + bad.name, NodeOptions{WakeOnLAN: bad.v}, `"wakeonlan" is not valid UTF-8`})
	}
	rows = append(rows,
		row{"location with a lone continuation byte", NodeOptions{Location: "a\x80b"}, `"location" is not valid UTF-8`},
		row{"description with a lone continuation byte", NodeOptions{Description: "a\x80b"}, `"description" is not valid UTF-8`})

	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := requireRefusedUnsent(t, setNodeOptions(tt.opts), ErrInvalidInput)
			verr := ValidateNodeOptions(tt.opts)
			if err == nil || verr == nil {
				t.Fatalf("SetNodeOptions = %v, ValidateNodeOptions = %v: both must refuse", err, verr)
			}
			if !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("err = %v, want it to say %q and not another refusal", err, tt.msg)
			}
			if verr.Error() != err.Error() || !errors.Is(verr, ErrInvalidInput) {
				t.Errorf("ValidateNodeOptions says %q, SetNodeOptions %q: they are one decision", verr, err)
			}
		})
	}
}

// TestSetNodeOptions_RefusesABodyOverTheLimit holds the size refusal to the byte.
// pveproxy refuses a body over $limit_max_post (512 KiB since libpve-http-server-perl
// 5.2.1) with a 501 before reading it, so a sender may see a reset in place of the
// answer; refusing here is never stricter than Proxmox (`$len > $limit_max_post`).
// The size is the ENCODED form: "description=" and the value, escaped.
func TestSetNodeOptions_RefusesABodyOverTheLimit(t *testing.T) {
	const limit = 512 * 1024
	const prefix = len("description=")

	for _, tt := range []struct {
		name      string
		notes     string
		encodedAs int // the encoded length, for the case's own sanity check
		refused   bool
	}{
		{"exactly the limit", strings.Repeat("a", limit-prefix), limit, false},
		{"one byte over", strings.Repeat("a", limit-prefix+1), limit + 1, true},
		// "é" is two bytes and six once escaped (%C3%A9): neither count is near the limit in characters.
		{"accented text just under, counted after encoding", strings.Repeat("é", 87379), prefix + 87379*6, false},
		{"accented text over, counted after encoding", strings.Repeat("é", 87380), prefix + 87380*6, true},
		{"line breaks (%0A) counted after encoding", strings.Repeat("\n", (limit-prefix)/3+1), prefix + ((limit-prefix)/3+1)*3, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(url.Values{"description": {tt.notes}}.Encode()); got != tt.encodedAs {
				t.Fatalf("the case is built wrong: the encoded form is %d bytes, the case says %d", got, tt.encodedAs)
			}
			if over := tt.encodedAs > limit; over != tt.refused {
				t.Fatalf("the case is built wrong: %d bytes against a limit of %d, refused = %v", tt.encodedAs, limit, tt.refused)
			}
			opts := NodeOptions{Description: tt.notes}
			verr := ValidateNodeOptions(opts)

			if !tt.refused {
				req := requireSentOnce(t, setNodeOptions(opts))
				if verr != nil {
					t.Errorf("ValidateNodeOptions = %v, want it to accept what SetNodeOptions accepts", verr)
				}
				if got := req.form.Get("description"); got != tt.notes {
					t.Errorf("the notes did not arrive intact (%d bytes sent, %d received)", len(tt.notes), len(got))
				}
				return
			}
			err := requireRefusedUnsent(t, setNodeOptions(opts), ErrRequestTooLarge)
			if errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v also reads as ErrInvalidInput; too large is its own refusal (413, not 400)", err)
			}
			if !errors.Is(verr, ErrRequestTooLarge) {
				t.Errorf("ValidateNodeOptions = %v, want the same refusal", verr)
			}
		})
	}

	// Everything in the body counts, not the notes alone: a digest is in it too.
	t.Run("the other keys count towards the limit", func(t *testing.T) {
		opts := NodeOptions{
			Description: strings.Repeat("a", limit-prefix-len("&digest=")-len(nodeConfigTestDigest)),
			Digest:      nodeConfigTestDigest,
		}
		form, err := buildNodeOptionsForm(opts)
		if err != nil || len(form.Encode()) != limit {
			t.Fatalf("the case is built wrong: %d bytes (%v), want exactly the limit", len(form.Encode()), err)
		}
		if err := ValidateNodeOptions(opts); err != nil {
			t.Errorf("a body of exactly the limit, digest included, was refused: %v", err)
		}
		opts.Description += "a"
		if err := ValidateNodeOptions(opts); !errors.Is(err, ErrRequestTooLarge) {
			t.Errorf("a body one byte over the limit was not refused as too large: %v", err)
		}
	})
}

// TestNodeOptionsClearKeys_NamesOnlySettings: opts.Delete is the caller's own
// strings and the audit row is readable by every Viewer, so what reaches the row
// is filtered through the allow-list.
func TestNodeOptionsClearKeys_NamesOnlySettings(t *testing.T) {
	const freeText = "sentinel free text with a credential"
	all := []string{"description", "location", "wakeonlan", "ballooning-target", "startall-onboot-delay"}
	for _, tt := range []struct {
		name string
		opts NodeOptions
		want []string
	}{
		{"nothing to clear", NodeOptions{}, []string{}},
		{"an empty list", NodeOptions{Delete: []string{}}, []string{}},
		{"every setting, in request order", NodeOptions{Delete: all}, all},
		{"free text is dropped", NodeOptions{Delete: []string{"wakeonlan", freeText, "location"}}, []string{"wakeonlan", "location"}},
		{"an ACME key is not a setting here", NodeOptions{Delete: []string{"acmedomain0", "acme", "location"}}, []string{"location"}},
		{"plumbing is no setting", NodeOptions{Delete: []string{"digest", "delete", ""}}, []string{}},
		{"names are matched exactly", NodeOptions{Delete: []string{"Location", " location", "location "}}, []string{}},
		{"a key named twice is named twice", NodeOptions{Delete: []string{"location", "location"}}, []string{"location", "location"}},
		{"what is set is not what is cleared",
			NodeOptions{BallooningTarget: flexPtr(75), Location: "latitude=0,longitude=0", Delete: []string{"wakeonlan"}}, []string{"wakeonlan"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := NodeOptionsClearKeys(tt.opts)
			if got == nil || !slices.Equal(got, tt.want) {
				t.Errorf("keys = %#v, want %v (never nil: the row would carry null where it carries [] elsewhere)", got, tt.want)
			}
			for _, k := range got {
				if strings.Contains(k, freeText) {
					t.Errorf("%q carries the caller's text into the audit log", k)
				}
			}
		})
	}
}

// TestNodeOptionsSetKeys_NamesKeysWithoutValues: the notes, the location and the
// Wake-on-LAN MAC are readable by every Viewer through view:audit, so the row
// carries names only.
func TestNodeOptionsSetKeys_NamesKeysWithoutValues(t *testing.T) {
	const (
		wake     = "02:00:00:00:00:77"
		location = "latitude=12.5,longitude=-45.25,name=sentinel-site"
		notes    = "sentinel notes"
	)
	keys := NodeOptionsSetKeys(NodeOptions{StartallOnbootDelay: flexPtr(37), BallooningTarget: flexPtr(63),
		WakeOnLAN: wake, Location: location, Description: notes, Delete: []string{"not-a-setting-written"}, Digest: "0123456789abcdef"})
	want := []string{"startall-onboot-delay", "ballooning-target", "wakeonlan", "location", "description"}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v, in table order and without delete or digest", keys, want)
	}
	for _, k := range keys {
		for _, secret := range []string{wake, location, notes, "37", "63"} {
			if strings.Contains(k, secret) {
				t.Errorf("%q carries %q into the audit log, not just a key name", k, secret)
			}
		}
	}
	if got := NodeOptionsSetKeys(NodeOptions{StartallOnbootDelay: flexPtr(0), BallooningTarget: flexPtr(0)}); !slices.Equal(got, want[:2]) {
		t.Errorf("0 counts as set: keys = %v, want %v", got, want[:2])
	}
	got := NodeOptionsSetKeys(NodeOptions{WakeOnLAN: "", Location: "", Description: ""})
	if out, err := json.Marshal(got); got == nil || err != nil || string(out) != "[]" {
		t.Errorf("nothing set marshals to %s (%v), want [] and not null", out, err)
	}
}

// TestNodeOptionsRefuseAnUnsafeNodeName holds that each exported method calls the
// node-name check; the check's own matrix is client_node_name_test.go's. It drives
// the methods and not the validator, which would still pass if a call were dropped.
func TestNodeOptionsRefuseAnUnsafeNodeName(t *testing.T) {
	ctx := context.Background()
	calls := map[string]func(*Client, string) error{
		"GetNodeOptions": func(c *Client, node string) error { _, err := c.GetNodeOptions(ctx, node); return err },
		"SetNodeOptions": func(c *Client, node string) error {
			return c.SetNodeOptions(ctx, node, NodeOptions{BallooningTarget: flexPtr(75)})
		},
		"GetNodeConfigDigest": func(c *Client, node string) error { _, err := c.GetNodeConfigDigest(ctx, node); return err },
		"SetNodeACMEConfig": func(c *Client, node string) error {
			return c.SetNodeACMEConfig(ctx, node, NodeACMEConfig{ACME: "account=default"})
		},
	}
	for name, call := range calls {
		for _, node := range []string{"..", "a/b", "a\nb"} {
			t.Run(name+"/"+url.PathEscape(node), func(t *testing.T) {
				t.Parallel()
				requireRefusedUnsent(t, func(c *Client) error { return call(c, node) }, ErrInvalidInput)
			})
		}
	}
}

// settableKeys is every key a request struct binds, other than the request
// plumbing (delete and digest, which are written as parameters, not config keys):
// its json tag, or the lower-cased field name when it has none, and through an
// embedded struct. A tag scan alone misses an untagged or promoted field, which
// encoding/json binds all the same.
func settableKeys(typ reflect.Type) []string {
	var keys []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		switch {
		case tag == "-" || tag == "delete" || tag == "digest":
		case f.Anonymous && f.Type.Kind() == reflect.Struct && tag == "":
			keys = append(keys, settableKeys(f.Type)...)
		case tag == "":
			keys = append(keys, strings.ToLower(f.Name))
		default:
			keys = append(keys, tag)
		}
	}
	return keys
}

// requireSettingsCoverStruct is the drift guard both tables share: every key the
// struct binds has a table row and is on the delete allow-list, and the table has
// no row for a key the struct dropped.
func requireSettingsCoverStruct(t *testing.T, typ reflect.Type, tableKeys []string, allowed map[string]bool) {
	t.Helper()
	fields := settableKeys(typ)
	for _, tag := range fields {
		if !slices.Contains(tableKeys, tag) {
			t.Errorf("%s has %q but its settings table does not: it would never be sent and never be clearable", typ.Name(), tag)
		}
		if !allowed[tag] {
			t.Errorf("%q is a settable key but is not in the delete allow-list", tag)
		}
	}
	if len(fields) != len(tableKeys) {
		t.Errorf("%s has %d settable fields (%v) but the table has %d rows: a row names a key the struct dropped",
			typ.Name(), len(fields), fields, len(tableKeys))
	}
}

// TestNodeOptionSettingsCoverTheStruct: adding a sixth field to NodeOptions
// compiles, is silently never sent, cannot be cleared and is never audited. This
// is what stops it, by reading the struct and not a second hand-written list.
func TestNodeOptionSettingsCoverTheStruct(t *testing.T) {
	keys := make([]string, 0, len(nodeOptionSettings))
	for _, s := range nodeOptionSettings {
		if slices.Contains(keys, s.key) {
			t.Errorf("nodeOptionSettings names %q twice", s.key)
		}
		keys = append(keys, s.key)
	}
	requireSettingsCoverStruct(t, reflect.TypeOf(NodeOptions{}), keys, deletableNodeOptionKeys)
	if len(deletableNodeOptionKeys) != len(keys) {
		t.Errorf("the allow-list has %d keys, want the table's %d and nothing else", len(deletableNodeOptionKeys), len(keys))
	}
}

// pveNodeConfigKeys is the complete key set of PVE's node config, transcribed
// from `$confdesc` in pve-manager's PVE/NodeConfig.pm (read 2026-10-01): six
// written out there, and acmedomain0..acmedomain5 from `my $MAXDOMAINS = 5`.
var pveNodeConfigKeys = []string{
	"description", "startall-onboot-delay", "ballooning-target", "wakeonlan", "acme", "location",
	"acmedomain0", "acmedomain1", "acmedomain2", "acmedomain3", "acmedomain4", "acmedomain5",
}

// TestNodeConfigAllowListsPartitionPVEsKeys: SetNodeACMEConfig and SetNodeOptions
// both PUT /nodes/{node}/config, whose `delete` reaches every key in the file.
// Together their allow-lists must cover PVE's key set exactly with nothing in both;
// a key added to $confdesc fails here until it is given to one of the two clients.
func TestNodeConfigAllowListsPartitionPVEsKeys(t *testing.T) {
	for _, key := range pveNodeConfigKeys {
		inACME, inOptions := deletableNodeACMEKeys[key], deletableNodeOptionKeys[key]
		switch {
		case inACME && inOptions:
			t.Errorf("%q is in both allow-lists: each client could clear the other's key", key)
		case !inACME && !inOptions:
			t.Errorf("%q is a PVE node config key that neither client may clear", key)
		}
	}
	for name, list := range map[string]map[string]bool{"deletableNodeACMEKeys": deletableNodeACMEKeys, "deletableNodeOptionKeys": deletableNodeOptionKeys} {
		for key := range list {
			if !slices.Contains(pveNodeConfigKeys, key) {
				t.Errorf("%s holds %q, which is not a key of PVE's node config", name, key)
			}
		}
	}
	if got, want := len(deletableNodeACMEKeys)+len(deletableNodeOptionKeys), len(pveNodeConfigKeys); got != want {
		t.Errorf("the allow-lists hold %d keys between them, PVE's node config has %d", got, want)
	}
}

// TestGetNodeOptions follows a read from the wire to the struct and back out as
// JSON, in the shapes PVE sends: an integer as a quoted string or a bare number, a
// description with the "\n" parse_config puts after every line, the ACME keys of
// the same file (which must not survive), keys that are absent (nil, not 0) and a
// node with no config file at all, which answers {} with no digest.
func TestGetNodeOptions(t *testing.T) {
	for _, tt := range []struct{ name, reply, want string }{
		{"every key",
			`{"data":{"ballooning-target":"75","startall-onboot-delay":0,
				"wakeonlan":"02:00:00:00:00:01,bind-interface=vmbr0","location":"latitude=0,longitude=0,name=rack01",
				"description":"sentinel notes\nsecond line\n","acme":"account=sentinel-account",
				"acmedomain0":"node.example.com","acmedomain5":"other.example.com","digest":"` + nodeConfigTestDigest + `"}}`,
			`{"startall-onboot-delay":0,"ballooning-target":75,"wakeonlan":"02:00:00:00:00:01,bind-interface=vmbr0",` +
				`"location":"latitude=0,longitude=0,name=rack01","description":"sentinel notes\nsecond line\n","digest":"` + nodeConfigTestDigest + `"}`},
		{"only the keys that were set",
			`{"data":{"description":"sentinel notes\n","digest":"` + nodeConfigTestDigest + `"}}`,
			`{"description":"sentinel notes\n","digest":"` + nodeConfigTestDigest + `"}`},
		{"no config file", `{"data":{}}`, `{}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, seen := newRecordingServer(t, tt.reply)
			opts, err := newTestClient(t, srv.URL).GetNodeOptions(context.Background(), "pve-01")
			if err != nil {
				t.Fatalf("GetNodeOptions: %v", err)
			}
			if len(*seen) != 1 || (*seen)[0].method != http.MethodGet || (*seen)[0].path != nodeConfigTestPath {
				t.Fatalf("requests = %v, want one GET %s", *seen, nodeConfigTestPath)
			}
			out, err := json.Marshal(opts)
			if err != nil || string(out) != tt.want {
				t.Errorf("serialised = %s (%v), want %s", out, err, tt.want)
			}
		})
	}
}

// TestGetNodeConfigDigest: the save check's one read takes the digest out of the
// reply and drops the rest — the notes and the ACME settings come back too.
func TestGetNodeConfigDigest(t *testing.T) {
	t.Run("a node with a config file", func(t *testing.T) {
		srv, seen := newRecordingServer(t, `{"data":{"acme":"account=sentinel-account","acmedomain0":"node.example.com",
			"ballooning-target":"75","description":"sentinel notes\n","digest":"`+nodeConfigTestDigest+`"}}`)
		digest, err := newTestClient(t, srv.URL).GetNodeConfigDigest(context.Background(), "pve-01")
		if err != nil || digest != nodeConfigTestDigest {
			t.Errorf("digest = %q, %v", digest, err)
		}
		if len(*seen) != 1 || (*seen)[0].method != http.MethodGet || (*seen)[0].path != nodeConfigTestPath {
			t.Errorf("requests = %v, want one GET %s", *seen, nodeConfigTestPath)
		}
	})
	t.Run("a node with no config file has no digest", func(t *testing.T) {
		srv, _ := newRecordingServer(t, `{"data":{}}`)
		digest, err := newTestClient(t, srv.URL).GetNodeConfigDigest(context.Background(), "pve-01")
		if err != nil || digest != "" {
			t.Errorf("GetNodeConfigDigest = %q, %v; want an empty digest and no error", digest, err)
		}
	})
	t.Run("a failure is wrapped with the node", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Permission check failed (/, Sys.Audit)"))
		}))
		t.Cleanup(srv.Close)
		_, err := newTestClient(t, srv.URL).GetNodeConfigDigest(context.Background(), "pve-01")
		if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "pve-01") {
			t.Errorf("err = %v, want ErrForbidden naming the node", err)
		}
	})
}
