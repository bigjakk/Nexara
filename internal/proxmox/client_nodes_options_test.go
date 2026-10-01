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
	"strings"
	"testing"
)

// nodeOptionsRequest is one request the stand-in Proxmox received.
type nodeOptionsRequest struct {
	method, path string
	form         url.Values
}

// newNodeOptionsServer records every request — its method, its decoded path and
// its form body — and answers each with body. The path is the decoded one
// (r.URL.Path), which is what these tests compare: the node name they use has
// nothing in it to decode.
func newNodeOptionsServer(t *testing.T, body string) (*httptest.Server, *[]nodeOptionsRequest) {
	t.Helper()
	var seen []nodeOptionsRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		seen = append(seen, nodeOptionsRequest{r.Method, r.URL.Path, r.PostForm})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// flexPtr is a pointer to a FlexInt, the shape NodeOptions holds an integer in.
func flexPtr(n int) *FlexInt {
	v := FlexInt(n)
	return &v
}

// oneNodeOptionsWrite fails unless the stand-in saw exactly one request and it
// was the node config PUT, and returns its form.
func oneNodeOptionsWrite(t *testing.T, seen *[]nodeOptionsRequest) url.Values {
	t.Helper()
	if len(*seen) != 1 {
		t.Fatalf("issued %d requests %v, want 1", len(*seen), *seen)
	}
	req := (*seen)[0]
	if req.method != http.MethodPut || req.path != "/api2/json/nodes/pve-01/config" {
		t.Fatalf("sent %s %s, want PUT /api2/json/nodes/pve-01/config", req.method, req.path)
	}
	return req.form
}

// TestSetNodeOptions_SendsEveryField is the test whose absence lets a table row
// vanish unnoticed.
//
// nodeOptionSettings maps five keys to five struct fields by hand. Dropping one
// row is invisible to every other test here, since they set one or two fields,
// and it breaks two things at once: the key silently stops being sent, and
// because the set-and-clear check reads the same table, its delete guard stops
// firing too. Five distinct sentinels, compared as the whole form, is what makes
// a missing row fail — and a stray extra key, since the comparison is exact.
func TestSetNodeOptions_SendsEveryField(t *testing.T) {
	srv, seen := newNodeOptionsServer(t, `{"data":null}`)
	c := newTestClient(t, srv.URL)

	err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{
		StartallOnbootDelay: flexPtr(37),
		BallooningTarget:    flexPtr(63),
		WakeOnLAN:           "mac=02:00:00:00:00:01,bind-interface=vmbr0,broadcast-address=192.0.2.255",
		Location:            "latitude=0,longitude=0,name=rack01",
		Description:         "sentinel notes\nsecond line",
	})
	if err != nil {
		t.Fatalf("SetNodeOptions: %v", err)
	}
	want := url.Values{
		"startall-onboot-delay": {"37"},
		"ballooning-target":     {"63"},
		"wakeonlan":             {"mac=02:00:00:00:00:01,bind-interface=vmbr0,broadcast-address=192.0.2.255"},
		"location":              {"latitude=0,longitude=0,name=rack01"},
		"description":           {"sentinel notes\nsecond line"},
	}
	if got := oneNodeOptionsWrite(t, seen); !reflect.DeepEqual(got, want) {
		t.Errorf("form = %v, want %v", got, want)
	}
}

// TestSetNodeOptions_ForwardsZero pins what makes the integers different from
// the strings: 0 is a value. A delay of 0 is how the delay is turned off, and a
// ballooning target of 0 is a legal setting, so a pointer to 0 has to reach the
// wire as "0" while a nil pointer sends nothing.
func TestSetNodeOptions_ForwardsZero(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts NodeOptions
		want url.Values
	}{
		{"both at 0", NodeOptions{StartallOnbootDelay: flexPtr(0), BallooningTarget: flexPtr(0)},
			url.Values{"startall-onboot-delay": {"0"}, "ballooning-target": {"0"}}},
		{"the delay alone", NodeOptions{StartallOnbootDelay: flexPtr(0)},
			url.Values{"startall-onboot-delay": {"0"}}},
		{"the target alone", NodeOptions{BallooningTarget: flexPtr(0)},
			url.Values{"ballooning-target": {"0"}}},
		{"the delay at 0 beside another setting", NodeOptions{StartallOnbootDelay: flexPtr(0), WakeOnLAN: "02:00:00:00:00:01"},
			url.Values{"startall-onboot-delay": {"0"}, "wakeonlan": {"02:00:00:00:00:01"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.SetNodeOptions(context.Background(), "pve-01", tt.opts); err != nil {
				t.Fatalf("SetNodeOptions: %v", err)
			}
			if got := oneNodeOptionsWrite(t, seen); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("form = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSetNodeOptions_OmittedAndEmptyAreNotSent: every key is optional in PVE's
// schema, so "" is not a value that means "remove" — set_options would write an
// empty string into the file. An omitted field and an empty one are both "leave
// alone", and neither may reach the form. Each case carries one real setting, so
// that it is the empties being left out that is under test and not a write that
// changes nothing, which is refused (TestSetNodeOptions_RefusesAWriteThatChangesNothing).
func TestSetNodeOptions_OmittedAndEmptyAreNotSent(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts NodeOptions
		want url.Values
	}{
		{"every string empty beside an integer",
			NodeOptions{BallooningTarget: flexPtr(75), WakeOnLAN: "", Location: "", Description: ""},
			url.Values{"ballooning-target": {"75"}}},
		{"an empty delete and digest beside an integer",
			NodeOptions{BallooningTarget: flexPtr(75), Delete: []string{}, Digest: ""},
			url.Values{"ballooning-target": {"75"}}},
		{"both integers absent beside a string",
			NodeOptions{Location: "latitude=0,longitude=0"},
			url.Values{"location": {"latitude=0,longitude=0"}}},
		{"empty strings beside a clear",
			NodeOptions{Delete: []string{"location"}, WakeOnLAN: "", Description: ""},
			url.Values{"delete": {"location"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.SetNodeOptions(context.Background(), "pve-01", tt.opts); err != nil {
				t.Fatalf("SetNodeOptions: %v", err)
			}
			if got := oneNodeOptionsWrite(t, seen); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("form = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSetNodeOptions_RefusesAWriteThatChangesNothing: a request that sets no
// setting and clears none — a digest alone being neither — is refused before
// anything is sent. Proxmox would answer it 200 and still rewrite the whole node
// config file from what it parsed (set_options ends in write_config), which can
// move the digest under every dialog open on the node, for a save that changed
// nothing; and the handler would record an audit row that names nothing.
//
// The control is a write that is not empty only because of a clear, or only
// because of an integer at 0: neither may be mistaken for nothing.
func TestSetNodeOptions_RefusesAWriteThatChangesNothing(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts NodeOptions
	}{
		{"nothing at all", NodeOptions{}},
		{"every string empty", NodeOptions{WakeOnLAN: "", Location: "", Description: ""}},
		{"an empty delete", NodeOptions{Delete: []string{}}},
		{"a digest alone", NodeOptions{Digest: "da39a3ee5e6b4b0d3255bfef95601890afd80709"}},
		{"a digest and empty strings", NodeOptions{Digest: "da39a3ee5e6b4b0d3255bfef95601890afd80709", Location: "", Delete: []string{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			err := c.SetNodeOptions(context.Background(), "pve-01", tt.opts)
			if err == nil {
				t.Fatal("a write that changes nothing was accepted; Proxmox would rewrite the config file for it")
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput (so the handler answers 400)", err)
			}
			if !strings.Contains(err.Error(), "nothing to change") {
				t.Errorf("err = %v, want the empty-write refusal and not another one", err)
			}
			if len(*seen) != 0 {
				t.Errorf("%d request(s) reached Proxmox, want none: %v", len(*seen), *seen)
			}
		})
	}

	for _, tt := range []struct {
		name string
		opts NodeOptions
		want url.Values
	}{
		{"an integer at 0", NodeOptions{BallooningTarget: flexPtr(0)}, url.Values{"ballooning-target": {"0"}}},
		{"a clear alone", NodeOptions{Delete: []string{"description"}}, url.Values{"delete": {"description"}}},
		{"a digest beside a clear", NodeOptions{Delete: []string{"location"}, Digest: "d1"}, url.Values{"delete": {"location"}, "digest": {"d1"}}},
	} {
		t.Run("not empty: "+tt.name, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.SetNodeOptions(context.Background(), "pve-01", tt.opts); err != nil {
				t.Fatalf("SetNodeOptions: %v", err)
			}
			if got := oneNodeOptionsWrite(t, seen); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("form = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestSetNodeOptions_ClearsViaDelete pins the only way to remove a setting, for
// each of the five, and that clearing one does not disturb a value written in the
// same save — the node page clears one field and edits another in one request.
func TestSetNodeOptions_ClearsViaDelete(t *testing.T) {
	for _, s := range nodeOptionSettings {
		t.Run(s.key, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{Delete: []string{s.key}}); err != nil {
				t.Fatalf("SetNodeOptions: %v", err)
			}
			if got, want := oneNodeOptionsWrite(t, seen), (url.Values{"delete": {s.key}}); !reflect.DeepEqual(got, want) {
				t.Errorf("form = %v, want %v", got, want)
			}
		})
	}

	t.Run("a clear beside a different key's write", func(t *testing.T) {
		srv, seen := newNodeOptionsServer(t, `{"data":null}`)
		c := newTestClient(t, srv.URL)
		err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{
			BallooningTarget: flexPtr(75),
			Delete:           []string{"wakeonlan", "location"},
		})
		if err != nil {
			t.Fatalf("SetNodeOptions: %v", err)
		}
		want := url.Values{"ballooning-target": {"75"}, "delete": {"wakeonlan,location"}}
		if got := oneNodeOptionsWrite(t, seen); !reflect.DeepEqual(got, want) {
			t.Errorf("form = %v, want the comma-separated list PVE's pve-configid-list expects, beside the write: %v", got, want)
		}
	})
}

// TestSetNodeOptions_OmitsDeleteAndDigestWhenEmpty guards the default: an
// ordinary edit must not carry a `delete` or a `digest` key at all, or an empty
// list would start meaning something and an empty digest would be compared.
func TestSetNodeOptions_OmitsDeleteAndDigestWhenEmpty(t *testing.T) {
	srv, seen := newNodeOptionsServer(t, `{"data":null}`)
	c := newTestClient(t, srv.URL)

	if err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{BallooningTarget: flexPtr(75)}); err != nil {
		t.Fatalf("SetNodeOptions: %v", err)
	}
	form := oneNodeOptionsWrite(t, seen)
	if _, ok := form["delete"]; ok {
		t.Errorf("delete sent for an edit that clears nothing: %q", form.Get("delete"))
	}
	if _, ok := form["digest"]; ok {
		t.Errorf("digest sent when none was supplied: %q", form.Get("digest"))
	}
}

// TestSetNodeOptions_SendsDigest covers the compare-and-swap. PVE's
// assert_if_modified is skipped unless both digests are set, so passing one
// through is the difference between a blind overwrite and a safe one.
func TestSetNodeOptions_SendsDigest(t *testing.T) {
	srv, seen := newNodeOptionsServer(t, `{"data":null}`)
	c := newTestClient(t, srv.URL)

	if err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{
		BallooningTarget: flexPtr(75),
		Digest:           "da39a3ee5e6b4b0d3255bfef95601890afd80709",
	}); err != nil {
		t.Fatalf("SetNodeOptions: %v", err)
	}
	if got := oneNodeOptionsWrite(t, seen).Get("digest"); got != "da39a3ee5e6b4b0d3255bfef95601890afd80709" {
		t.Errorf("digest = %q", got)
	}
}

// TestSetNodeOptions_RejectsUndeletableKeys is the reason the allow-list exists.
// PUT /nodes/{node}/config applies `delete` to the whole node config file, and
// the handler binds the list straight from the request body — so without this,
// any API client holding manage:node could erase the ACME settings that sit in
// the same file, and with them the node's certificate configuration.
//
// Every ACME key is looped from nodeACMESettings rather than named here, so an
// ACME key added there is a key this starts demanding a refusal for. The refusal
// must name the ACME config as where to clear it: the allow-list is the
// authority, and the ACME name only picks the message.
func TestSetNodeOptions_RejectsUndeletableKeys(t *testing.T) {
	type refusal struct{ key, wantInMessage string }
	notSettings := []string{
		"digest",             // request plumbing, not a setting
		"delete",             // ditto
		"node",               // the path parameter
		"Description",        // the allow-list is not case-folded, and neither is PVE
		"WAKEONLAN",          // ditto
		" description",       // nor trimmed
		"description ",       // ditto
		"wakeonlan,location", // a second key smuggled through one entry
		"acmedomain6",        // one past $MAXDOMAINS
		"",
	}
	cases := make([]refusal, 0, len(nodeACMESettings)+len(notSettings))
	for _, s := range nodeACMESettings {
		cases = append(cases, refusal{s.key, "ACME setting"})
	}
	for _, key := range notSettings {
		cases = append(cases, refusal{key, "not a node option that can be cleared"})
	}

	for _, tt := range cases {
		t.Run(tt.key, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{Delete: []string{tt.key}})
			if err == nil {
				t.Fatalf("delete %q accepted, want rejection", tt.key)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("delete %q: err = %v, want ErrInvalidInput (so the handler answers 400)", tt.key, err)
			}
			if !strings.Contains(err.Error(), tt.wantInMessage) {
				t.Errorf("delete %q: err = %v, want it to say %q", tt.key, err, tt.wantInMessage)
			}
			if len(*seen) != 0 {
				t.Errorf("%d request(s) reached Proxmox; a rejected key must not be sent at all: %v", len(*seen), *seen)
			}
		})
	}

	// One bad entry among good ones is still a refusal of the whole request:
	// nothing is sent, so the good clears are not half-applied.
	t.Run("one bad entry among good ones", func(t *testing.T) {
		srv, seen := newNodeOptionsServer(t, `{"data":null}`)
		c := newTestClient(t, srv.URL)
		err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{
			Delete: []string{"wakeonlan", "acmedomain0", "location"},
		})
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("err = %v, want ErrInvalidInput", err)
		}
		if len(*seen) != 0 {
			t.Errorf("%d request(s) reached Proxmox, want none: %v", len(*seen), *seen)
		}
	})
}

// TestSetNodeOptions_RejectsSetAndClearTogether covers the footgun PVE leaves
// open. set_options assigns every supplied key first and applies `delete`
// afterwards, so a key in both is silently removed — the caller gets a 200 and
// the value they just wrote is gone. PVE raises nothing here, so this client has
// to.
//
// The integers are set to 0 on purpose: 0 is a value, and a check that read "set"
// off what the value renders as would let the case that matters most through.
func TestSetNodeOptions_RejectsSetAndClearTogether(t *testing.T) {
	cases := map[string]NodeOptions{
		"startall-onboot-delay": {StartallOnbootDelay: flexPtr(0)},
		"ballooning-target":     {BallooningTarget: flexPtr(0)},
		"wakeonlan":             {WakeOnLAN: "02:00:00:00:00:01"},
		"location":              {Location: "latitude=0,longitude=0"},
		"description":           {Description: "sentinel notes"},
	}
	// A key added to the table without a case here would be a key whose guard
	// nothing exercises.
	for _, s := range nodeOptionSettings {
		if _, ok := cases[s.key]; !ok {
			t.Errorf("nodeOptionSettings has %q but this test has no case for setting and clearing it", s.key)
		}
	}

	for key, opts := range cases {
		t.Run(key, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			opts.Delete = []string{key}
			err := c.SetNodeOptions(context.Background(), "pve-01", opts)
			if err == nil {
				t.Fatalf("setting and clearing %s together was accepted; PVE would drop the value silently", key)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), "set and cleared") {
				t.Errorf("err = %v, want the set-and-cleared refusal and not another one", err)
			}
			if len(*seen) != 0 {
				t.Errorf("%d request(s) reached Proxmox, want none: %v", len(*seen), *seen)
			}
		})
	}

	// The control that keeps the refusal specific: clearing one key while
	// setting a DIFFERENT one is an ordinary save.
	t.Run("a different key is not a conflict", func(t *testing.T) {
		srv, seen := newNodeOptionsServer(t, `{"data":null}`)
		c := newTestClient(t, srv.URL)
		err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{
			StartallOnbootDelay: flexPtr(0),
			Delete:              []string{"ballooning-target"},
		})
		if err != nil {
			t.Fatalf("SetNodeOptions: %v", err)
		}
		want := url.Values{"startall-onboot-delay": {"0"}, "delete": {"ballooning-target"}}
		if got := oneNodeOptionsWrite(t, seen); !reflect.DeepEqual(got, want) {
			t.Errorf("form = %v, want %v", got, want)
		}
	})
}

// TestSetNodeOptions_RefusesControlCharactersOutsideDescription pins two rules.
//
// The first is transcribed from write_node_config in pve-manager's
// PVE/NodeConfig.pm, which dies with "detected invalid newline inside property
// '<key>'" on a "\n" in any value but the description, and answers a plain 500.
// It is reachable because the format check lets one through: pve-common's
// parse_property_string skips a segment that is whitespace-only (`next if $part
// =~ /^\s*\z/`), so "<mac>,\n" is a valid wakeonlan and a location ending in
// ",\n" is a valid location, and both die at the write.
//
// The second is NOT Proxmox's, and is deliberately stricter: the same skip lets
// "<mac>,\r" and "<mac>,\t" through the format check and then WRITES them, and a
// location's name is free text, so a manage:node caller could plant an escape
// sequence (ESC, U+0085, DEL), a carriage return or a Unicode line separator in
// a file that `pvenode config get` and `cat` print to a terminal and that other
// line-oriented readers split on. No legitimate wakeonlan or location holds one.
// Every control character is refused — C0, DEL and C1 — and U+2028 and U+2029,
// which unicode.IsControl does not count, with them.
//
// The description is exempt on both counts. It is the one value stored as lines,
// and write_node_config writes each line as '#' plus encode_text (pve-common's
// PVE/ParseUtils.pm), which %-escapes control characters, so a tab and a carriage
// return in a note arrive intact, and so does a trailing break, which is what a
// read hands back.
func TestSetNodeOptions_RefusesControlCharactersOutsideDescription(t *testing.T) {
	for _, tt := range []struct {
		name string
		opts NodeOptions
	}{
		{"a trailing line feed in wakeonlan", NodeOptions{WakeOnLAN: "02:00:00:00:00:01,\n"}},
		{"a line feed in the middle of wakeonlan", NodeOptions{WakeOnLAN: "02:00:00:00:00:01,\nbind-interface=vmbr0"}},
		{"a leading line feed in wakeonlan", NodeOptions{WakeOnLAN: "\n02:00:00:00:00:01"}},
		{"a carriage return in a whitespace-only segment of wakeonlan", NodeOptions{WakeOnLAN: "02:00:00:00:00:01,\r"}},
		{"a tab in a whitespace-only segment of wakeonlan", NodeOptions{WakeOnLAN: "02:00:00:00:00:01,\t"}},
		{"a vertical tab in wakeonlan", NodeOptions{WakeOnLAN: "02:00:00:00:00:01,\v"}},
		{"a form feed in wakeonlan", NodeOptions{WakeOnLAN: "02:00:00:00:00:01,\f"}},
		{"a NUL in wakeonlan", NodeOptions{WakeOnLAN: "02:00:00:00:00:01,\x00"}},
		{"a trailing line feed in location", NodeOptions{Location: "latitude=0,longitude=0,\n"}},
		{"a line feed in the name of a location", NodeOptions{Location: "latitude=0,longitude=0,name=rack\n01"}},
		{"a carriage return in the name of a location", NodeOptions{Location: "latitude=0,longitude=0,name=rack\r01"}},
		{"a tab in the name of a location", NodeOptions{Location: "latitude=0,longitude=0,name=rack\t01"}},
		{"an escape sequence in the name of a location", NodeOptions{Location: "latitude=0,longitude=0,name=rack\x1b[31m01"}},
		{"DEL in the name of a location", NodeOptions{Location: "latitude=0,longitude=0,name=rack\x7f01"}},
		{"U+0085 in the name of a location", NodeOptions{Location: "latitude=0,longitude=0,name=rack\u008501"}},
		{"U+009B, the C1 control sequence introducer, in the name of a location", NodeOptions{Location: "latitude=0,longitude=0,name=rack\u009b31m01"}},
		{"U+2028 in the name of a location", NodeOptions{Location: "latitude=0,longitude=0,name=rack\u202801"}},
		{"U+2029 in the name of a location", NodeOptions{Location: "latitude=0,longitude=0,name=rack\u202901"}},
		// Beside values that are fine, so that it is the one value being refused.
		{"a bad wakeonlan beside good values", NodeOptions{
			StartallOnbootDelay: flexPtr(30), Description: "sentinel notes", WakeOnLAN: "02:00:00:00:00:01,\n"}},
		{"a bad location beside good values", NodeOptions{
			BallooningTarget: flexPtr(75), Description: "sentinel notes", Location: "latitude=0,longitude=0,name=rack\x1b01"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			err := c.SetNodeOptions(context.Background(), "pve-01", tt.opts)
			if err == nil {
				t.Fatal("a control character outside the description was accepted; it would be written raw into the node config")
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v, want ErrInvalidInput (so the handler answers 400)", err)
			}
			if !strings.Contains(err.Error(), "line break or a control character") {
				t.Errorf("err = %v, want the control-character refusal and not another one", err)
			}
			if len(*seen) != 0 {
				t.Errorf("%d request(s) reached Proxmox, want none: %v", len(*seen), *seen)
			}
		})
	}

	// The refusal is not a blanket one: an ordinary value, and ordinary text in a
	// name — spaces and letters of any script — pass untouched.
	for _, tt := range []struct {
		name string
		opts NodeOptions
		want url.Values
	}{
		{"a keyed wakeonlan", NodeOptions{WakeOnLAN: "mac=02:00:00:00:00:01,bind-interface=vmbr0,broadcast-address=192.0.2.255"},
			url.Values{"wakeonlan": {"mac=02:00:00:00:00:01,bind-interface=vmbr0,broadcast-address=192.0.2.255"}}},
		{"a name with a space", NodeOptions{Location: "latitude=0,longitude=0,name=Site A"},
			url.Values{"location": {"latitude=0,longitude=0,name=Site A"}}},
		{"a name in other scripts", NodeOptions{Location: "latitude=12.5,longitude=-45.25,name=Gebäude Ost 東棟"},
			url.Values{"location": {"latitude=12.5,longitude=-45.25,name=Gebäude Ost 東棟"}}},
	} {
		t.Run("accepts "+tt.name, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.SetNodeOptions(context.Background(), "pve-01", tt.opts); err != nil {
				t.Fatalf("SetNodeOptions: %v", err)
			}
			if got := oneNodeOptionsWrite(t, seen); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("form = %v, want %v", got, tt.want)
			}
		})
	}

	for _, description := range []string{
		"a\nb",
		"sentinel notes\nsecond line\n", // what a read of two lines hands back
		"\n\nleading blank lines",
		"a\tb",
		"a\rb",
		"windows\r\nline endings\r\n",
		"\n  indented\nsecond line\n",
		"tab\there\nand a carriage\rreturn\n",
	} {
		t.Run("the description keeps "+url.QueryEscape(description), func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			if err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{Description: description}); err != nil {
				t.Fatalf("SetNodeOptions: %v", err)
			}
			if got := oneNodeOptionsWrite(t, seen).Get("description"); got != description {
				t.Errorf("description = %q, want it intact: %q", got, description)
			}
		})
	}
}

// TestNodeOptionsClearKeys_NamesOnlySettings pins what reaches the audit row for
// a clear. opts.Delete is the caller's own strings and the row is readable by
// every Viewer, so the names are filtered through the allow-list: a string that
// is not a setting's name never comes out, whatever it says.
func TestNodeOptionsClearKeys_NamesOnlySettings(t *testing.T) {
	const freeText = "sentinel free text with a credential"
	for _, tt := range []struct {
		name string
		opts NodeOptions
		want []string
	}{
		{"nothing to clear", NodeOptions{}, []string{}},
		{"an empty list", NodeOptions{Delete: []string{}}, []string{}},
		{"every setting, in request order",
			NodeOptions{Delete: []string{"description", "location", "wakeonlan", "ballooning-target", "startall-onboot-delay"}},
			[]string{"description", "location", "wakeonlan", "ballooning-target", "startall-onboot-delay"}},
		{"free text is dropped",
			NodeOptions{Delete: []string{"wakeonlan", freeText, "location"}},
			[]string{"wakeonlan", "location"}},
		{"nothing but free text",
			NodeOptions{Delete: []string{freeText}},
			[]string{}},
		{"an ACME key is not a setting here",
			NodeOptions{Delete: []string{"acmedomain0", "acme", "location"}},
			[]string{"location"}},
		{"the request's plumbing is no setting",
			NodeOptions{Delete: []string{"digest", "delete", ""}},
			[]string{}},
		{"names are matched exactly",
			NodeOptions{Delete: []string{"Location", " location", "location "}},
			[]string{}},
		{"a key named twice is named twice, as it was sent",
			NodeOptions{Delete: []string{"location", "location"}},
			[]string{"location", "location"}},
		{"what is set is not what is cleared",
			NodeOptions{BallooningTarget: flexPtr(75), Location: "latitude=0,longitude=0", Delete: []string{"wakeonlan"}},
			[]string{"wakeonlan"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := NodeOptionsClearKeys(tt.opts)
			if got == nil {
				t.Fatal("NodeOptionsClearKeys returned nil; the audit row would carry null where it carries [] elsewhere")
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("keys = %v, want %v", got, tt.want)
			}
			for _, k := range got {
				if strings.Contains(k, freeText) {
					t.Errorf("%q carries the caller's text into the audit log", k)
				}
			}
		})
	}
}

// TestNodeOptionsRefuseAnUnsafeNodeName drives the two exported methods rather
// than the validator, for the reason client_node_name_test.go gives: asserting on
// validateNodeName would still pass on the day someone dropped the call from one
// of these.
func TestNodeOptionsRefuseAnUnsafeNodeName(t *testing.T) {
	for _, tc := range refusedNodeNames {
		label := tc.name + "/" + url.PathEscape(tc.node)

		t.Run("get/"+label, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":{}}`)
			c := newTestClient(t, srv.URL)
			_, err := c.GetNodeOptions(context.Background(), tc.node)
			assertNodeNameRefused(t, err, seen)
		})

		t.Run("set/"+label, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			err := c.SetNodeOptions(context.Background(), tc.node, NodeOptions{BallooningTarget: flexPtr(75)})
			assertNodeNameRefused(t, err, seen)
		})

		t.Run("digest/"+label, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":{}}`)
			c := newTestClient(t, srv.URL)
			_, err := c.GetNodeConfigDigest(context.Background(), tc.node)
			assertNodeNameRefused(t, err, seen)
		})

		t.Run("acme set/"+label, func(t *testing.T) {
			srv, seen := newCaptureServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			err := c.SetNodeACMEConfig(context.Background(), tc.node, NodeACMEConfig{ACME: "account=default"})
			assertNodeNameRefused(t, err, seen)
		})
	}
}

// TestNodeOptionSettingsCoverTheStruct is the drift guard, and it reads the
// struct rather than a second hand-written list: it is
// TestNodeACMESettingsCoverTheStruct for this table.
//
// A field with no json tag, and one promoted from an embedded struct, are both
// bindable and both invisible to a scan of the tags alone, so the walk covers all
// three shapes. Adding a sixth field to NodeOptions compiles, is silently never
// sent, cannot be cleared and is never audited — and this is what stops it.
func TestNodeOptionSettingsCoverTheStruct(t *testing.T) {
	// delete and digest are request plumbing, not settings: they are not written
	// as config keys and cannot themselves be cleared.
	plumbing := map[string]bool{"delete": true, "digest": true}

	inTable := make(map[string]bool, len(nodeOptionSettings))
	for _, s := range nodeOptionSettings {
		if inTable[s.key] {
			t.Errorf("nodeOptionSettings names %q twice", s.key)
		}
		inTable[s.key] = true
	}

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
	walk(reflect.TypeOf(NodeOptions{}))

	for _, tag := range fields {
		if !inTable[tag] {
			t.Errorf("NodeOptions has %q but nodeOptionSettings does not: it would never be sent, never be clearable and never be audited", tag)
		}
		if !deletableNodeOptionKeys[tag] {
			t.Errorf("%q is a settable option but is not in the delete allow-list", tag)
		}
	}
	if len(fields) != len(nodeOptionSettings) {
		t.Errorf("struct has %d option fields (%v) but the table has %d rows — a row names a key the struct dropped",
			len(fields), fields, len(nodeOptionSettings))
	}
	if len(deletableNodeOptionKeys) != len(nodeOptionSettings) {
		t.Errorf("the allow-list has %d keys, want the table's %d and nothing else", len(deletableNodeOptionKeys), len(nodeOptionSettings))
	}
}

// pveNodeConfigKeys is the complete key set of PVE's node config, transcribed
// from `$confdesc` in pve-manager's PVE/NodeConfig.pm (read 2026-10-01):
// description, startall-onboot-delay, ballooning-target, wakeonlan, acme and
// location are written out there, and the loop
//
//	for my $i (0 .. $MAXDOMAINS) { $confdesc->{"acmedomain$i"} = {...} }
//
// adds acmedomain0..acmedomain5, because `my $MAXDOMAINS = 5`.
var pveNodeConfigKeys = []string{
	"description", "startall-onboot-delay", "ballooning-target", "wakeonlan", "acme", "location",
	"acmedomain0", "acmedomain1", "acmedomain2", "acmedomain3", "acmedomain4", "acmedomain5",
}

// TestNodeConfigAllowListsPartitionPVEsKeys is what holds the two clients of one
// endpoint to each other. SetNodeACMEConfig and SetNodeOptions both PUT
// /nodes/{node}/config, whose `delete` reaches every key in the file, and each
// may clear only its own. Together they must cover PVE's key set exactly, with
// nothing in both: a key in neither is one the node page cannot clear, and a key
// in both is one a request for the other family could erase.
//
// It is also the test a PVE release breaks on purpose. A key added to $confdesc
// is not in pveNodeConfigKeys, so the day this transcription is refreshed the
// new key fails here until it is given to one of the two clients.
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

	known := make(map[string]bool, len(pveNodeConfigKeys))
	for _, key := range pveNodeConfigKeys {
		known[key] = true
	}
	for _, list := range []struct {
		name string
		keys map[string]bool
	}{
		{"deletableNodeACMEKeys", deletableNodeACMEKeys},
		{"deletableNodeOptionKeys", deletableNodeOptionKeys},
	} {
		for key := range list.keys {
			if !known[key] {
				t.Errorf("%s holds %q, which is not a key of PVE's node config", list.name, key)
			}
		}
	}
	if got, want := len(deletableNodeACMEKeys)+len(deletableNodeOptionKeys), len(pveNodeConfigKeys); got != want {
		t.Errorf("the allow-lists hold %d keys between them, PVE's node config has %d", got, want)
	}
}

// TestNodeOptionsSetKeys_NamesKeysWithoutValues pins what reaches the audit log.
// The notes, the location and the Wake-on-LAN MAC are readable by every Viewer
// through view:audit, so the row carries names only.
func TestNodeOptionsSetKeys_NamesKeysWithoutValues(t *testing.T) {
	const (
		sentinelWake     = "02:00:00:00:00:77"
		sentinelLocation = "latitude=12.5,longitude=-45.25,name=sentinel-site"
		sentinelNotes    = "sentinel notes"
	)
	keys := NodeOptionsSetKeys(NodeOptions{
		StartallOnbootDelay: flexPtr(37),
		BallooningTarget:    flexPtr(63),
		WakeOnLAN:           sentinelWake,
		Location:            sentinelLocation,
		Description:         sentinelNotes,
		Delete:              []string{"not-a-setting-written"},
		Digest:              "0123456789abcdef",
	})
	want := []string{"startall-onboot-delay", "ballooning-target", "wakeonlan", "location", "description"}
	if !slices.Equal(keys, want) {
		t.Fatalf("keys = %v, want %v, in table order and without delete or digest", keys, want)
	}
	for _, k := range keys {
		for _, secret := range []string{sentinelWake, sentinelLocation, sentinelNotes, "37", "63"} {
			if strings.Contains(k, secret) {
				t.Errorf("%q carries %q into the audit log, not just a key name", k, secret)
			}
		}
	}

	t.Run("0 counts as set", func(t *testing.T) {
		got := NodeOptionsSetKeys(NodeOptions{StartallOnbootDelay: flexPtr(0), BallooningTarget: flexPtr(0)})
		if want := []string{"startall-onboot-delay", "ballooning-target"}; !slices.Equal(got, want) {
			t.Errorf("keys = %v, want %v", got, want)
		}
	})
	t.Run("nothing set is an empty list, not nil", func(t *testing.T) {
		got := NodeOptionsSetKeys(NodeOptions{})
		if got == nil {
			t.Fatal("an empty config names nil; the audit row would carry null where it carries [] elsewhere")
		}
		if len(got) != 0 {
			t.Errorf("an empty config names %v, want nothing", got)
		}
		if out, err := json.Marshal(got); err != nil || string(out) != "[]" {
			t.Errorf("marshals to %s (%v), want []", out, err)
		}
	})
	t.Run("empty strings are not set", func(t *testing.T) {
		if got := NodeOptionsSetKeys(NodeOptions{WakeOnLAN: "", Location: "", Description: ""}); len(got) != 0 {
			t.Errorf("empty strings name %v, want nothing", got)
		}
	})
}

// TestGetNodeOptions_DecodesTheNodeConfigReply follows a read from the wire to
// the struct, in the shapes PVE sends: an integer as a quoted string or a bare
// number, a description with the "\n" parse_config puts after every line, and the
// ACME keys of the same file beside the five that are wanted.
func TestGetNodeOptions_DecodesTheNodeConfigReply(t *testing.T) {
	const reply = `{"data":{
		"ballooning-target":"75",
		"startall-onboot-delay":0,
		"wakeonlan":"02:00:00:00:00:01,bind-interface=vmbr0",
		"location":"latitude=0,longitude=0,name=rack01",
		"description":"sentinel notes\nsecond line\n",
		"acme":"account=sentinel-account",
		"acmedomain0":"node.example.com",
		"acmedomain5":"other.example.com",
		"digest":"da39a3ee5e6b4b0d3255bfef95601890afd80709"}}`
	srv, seen := newNodeOptionsServer(t, reply)
	c := newTestClient(t, srv.URL)

	opts, err := c.GetNodeOptions(context.Background(), "pve-01")
	if err != nil {
		t.Fatalf("GetNodeOptions: %v", err)
	}
	if len(*seen) != 1 || (*seen)[0].method != http.MethodGet || (*seen)[0].path != "/api2/json/nodes/pve-01/config" {
		t.Fatalf("requests = %v, want one GET /api2/json/nodes/pve-01/config", *seen)
	}

	if opts.BallooningTarget == nil || *opts.BallooningTarget != 75 {
		t.Errorf("ballooning-target = %v, want 75 from the quoted string", opts.BallooningTarget)
	}
	if opts.StartallOnbootDelay == nil || *opts.StartallOnbootDelay != 0 {
		t.Errorf("startall-onboot-delay = %v, want a pointer to 0 from the bare number — 0 is a value", opts.StartallOnbootDelay)
	}
	if opts.WakeOnLAN != "02:00:00:00:00:01,bind-interface=vmbr0" {
		t.Errorf("wakeonlan = %q", opts.WakeOnLAN)
	}
	if opts.Location != "latitude=0,longitude=0,name=rack01" {
		t.Errorf("location = %q", opts.Location)
	}
	if opts.Description != "sentinel notes\nsecond line\n" {
		t.Errorf("description = %q, want it exactly as PVE hands it back, trailing newline included", opts.Description)
	}
	if opts.Digest != "da39a3ee5e6b4b0d3255bfef95601890afd80709" {
		t.Errorf("digest = %q", opts.Digest)
	}

	// What the struct carries onward is the five settings and the digest and
	// nothing else: the ACME keys of the same reply do not survive, so nothing
	// the handler serialises can disclose them through this route.
	out, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var keys map[string]any
	if err := json.Unmarshal(out, &keys); err != nil {
		t.Fatalf("unmarshal %s: %v", out, err)
	}
	got := make([]string, 0, len(keys))
	for k := range keys {
		got = append(got, k)
	}
	slices.Sort(got)
	want := []string{"ballooning-target", "description", "digest", "location", "startall-onboot-delay", "wakeonlan"}
	if !slices.Equal(got, want) {
		t.Errorf("serialised keys = %v, want exactly %v (%s)", got, want, out)
	}
	if n, ok := keys["startall-onboot-delay"].(float64); !ok || n != 0 {
		t.Errorf("startall-onboot-delay serialises as %#v, want the number 0 — omitempty must not drop a pointer to 0", keys["startall-onboot-delay"])
	}
	if n, ok := keys["ballooning-target"].(float64); !ok || n != 75 {
		t.Errorf("ballooning-target serialises as %#v, want the number 75, not a string", keys["ballooning-target"])
	}
}

// TestGetNodeOptions_AbsentStaysAbsent: a node's config holds only the keys that
// were set, so the reply of a node with one setting must not read as a node with
// five — in particular an absent integer is nil, not a pointer to 0, and not the
// default PVE applies when the key is unset.
func TestGetNodeOptions_AbsentStaysAbsent(t *testing.T) {
	srv, _ := newNodeOptionsServer(t, `{"data":{"description":"sentinel notes\n","digest":"da39a3ee5e6b4b0d3255bfef95601890afd80709"}}`)
	c := newTestClient(t, srv.URL)

	opts, err := c.GetNodeOptions(context.Background(), "pve-01")
	if err != nil {
		t.Fatalf("GetNodeOptions: %v", err)
	}
	if opts.StartallOnbootDelay != nil || opts.BallooningTarget != nil {
		t.Errorf("absent integers read as %v and %v, want nil for both", opts.StartallOnbootDelay, opts.BallooningTarget)
	}
	if opts.WakeOnLAN != "" || opts.Location != "" {
		t.Errorf("absent strings read as %q and %q", opts.WakeOnLAN, opts.Location)
	}
	out, err := json.Marshal(opts)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(out); got != `{"description":"sentinel notes\n","digest":"da39a3ee5e6b4b0d3255bfef95601890afd80709"}` {
		t.Errorf("serialised = %s, want only the keys the reply held", got)
	}
}

// TestGetNodeOptions_NoConfigFile: load_config returns {} with no digest for a
// node that has no config file at all, which is most of them. It reads back as
// the zero struct, and the empty digest is what lets the caller's save go out
// without one (assert_if_modified is skipped unless both sides are set).
func TestGetNodeOptions_NoConfigFile(t *testing.T) {
	srv, _ := newNodeOptionsServer(t, `{"data":{}}`)
	c := newTestClient(t, srv.URL)

	opts, err := c.GetNodeOptions(context.Background(), "pve-01")
	if err != nil {
		t.Fatalf("GetNodeOptions: %v", err)
	}
	if !reflect.DeepEqual(*opts, NodeOptions{}) {
		t.Errorf("an empty config read as %+v, want the zero struct", *opts)
	}
	if out, _ := json.Marshal(opts); string(out) != "{}" {
		t.Errorf("serialised = %s, want {}", out)
	}
}

// TestGetNodeConfigDigest: the save check's one read. It asks for the node's
// config, takes the digest out of the reply and drops everything else — the notes
// and the ACME settings come back in the same reply, and none of it is the
// caller's business.
func TestGetNodeConfigDigest(t *testing.T) {
	const reply = `{"data":{
		"acme":"account=sentinel-account","acmedomain0":"node.example.com",
		"ballooning-target":"75","description":"sentinel notes\n",
		"digest":"da39a3ee5e6b4b0d3255bfef95601890afd80709"}}`

	t.Run("a node with a config file", func(t *testing.T) {
		srv, seen := newNodeOptionsServer(t, reply)
		c := newTestClient(t, srv.URL)

		digest, err := c.GetNodeConfigDigest(context.Background(), "pve-01")
		if err != nil {
			t.Fatalf("GetNodeConfigDigest: %v", err)
		}
		if digest != "da39a3ee5e6b4b0d3255bfef95601890afd80709" {
			t.Errorf("digest = %q", digest)
		}
		if len(*seen) != 1 || (*seen)[0].method != http.MethodGet || (*seen)[0].path != "/api2/json/nodes/pve-01/config" {
			t.Errorf("requests = %v, want one GET /api2/json/nodes/pve-01/config", *seen)
		}
	})

	t.Run("a node with no config file has no digest", func(t *testing.T) {
		srv, _ := newNodeOptionsServer(t, `{"data":{}}`)
		c := newTestClient(t, srv.URL)
		digest, err := c.GetNodeConfigDigest(context.Background(), "pve-01")
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
		c := newTestClient(t, srv.URL)

		_, err := c.GetNodeConfigDigest(context.Background(), "pve-01")
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if !strings.Contains(err.Error(), "pve-01") {
			t.Errorf("err = %v, want the node named", err)
		}
	})
}

// TestSetNodeOptions_RefusesInvalidUTF8: a value that is not valid UTF-8 is refused
// whichever key carries it, the description included. The refusal is the client's
// for the sake of every future caller — what arrives over the API's JSON never
// holds one.
func TestSetNodeOptions_RefusesInvalidUTF8(t *testing.T) {
	invalid := map[string]string{
		"a lone continuation byte":   "a\x80b",
		"a truncated sequence":       "a\xc3",
		"an overlong encoding":       "a\xc0\xafb",
		"a UTF-16 surrogate":         "a\xed\xa0\x80b",
		"a byte that is never UTF-8": "a\xffb",
	}
	for name, v := range invalid {
		for _, key := range []string{"wakeonlan", "location", "description"} {
			t.Run(key+"/"+name, func(t *testing.T) {
				var opts NodeOptions
				switch key {
				case "wakeonlan":
					opts.WakeOnLAN = v
				case "location":
					opts.Location = v
				case "description":
					opts.Description = v
				}
				srv, seen := newNodeOptionsServer(t, `{"data":null}`)
				c := newTestClient(t, srv.URL)

				err := c.SetNodeOptions(context.Background(), "pve-01", opts)
				if !errors.Is(err, ErrInvalidInput) {
					t.Fatalf("err = %v, want ErrInvalidInput", err)
				}
				if !strings.Contains(err.Error(), "not valid UTF-8") || !strings.Contains(err.Error(), key) {
					t.Errorf("err = %v, want it to name %s and say it is not valid UTF-8", err, key)
				}
				if len(*seen) != 0 {
					t.Errorf("%d request(s) reached Proxmox, want none: %v", len(*seen), *seen)
				}
				if verr := ValidateNodeOptions(opts); !errors.Is(verr, ErrInvalidInput) {
					t.Errorf("ValidateNodeOptions = %v, want the same refusal", verr)
				}
			})
		}
	}

	t.Run("multibyte text that is valid is not refused", func(t *testing.T) {
		srv, seen := newNodeOptionsServer(t, `{"data":null}`)
		c := newTestClient(t, srv.URL)
		const notes = "Gebäude Ost 東棟\n"
		if err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{Description: notes}); err != nil {
			t.Fatalf("SetNodeOptions: %v", err)
		}
		if got := oneNodeOptionsWrite(t, seen).Get("description"); got != notes {
			t.Errorf("description = %q, want %q", got, notes)
		}
	})
}

// TestSetNodeOptions_RefusesABodyOverTheLimit holds the client's own size refusal
// to the byte. pveproxy refuses a body over $limit_max_post — 512 KiB since
// libpve-http-server-perl 5.2.1, and before that 64 KiB — and refuses it with a
// 501 after the request head, without reading the body, so a sender still writing
// may see the connection reset in place of the answer. Refusing in the client what
// every version refuses gives the same answer whatever the network does, and is
// never stricter than Proxmox: the test is `$len > $limit_max_post`, so a body of
// exactly the limit goes through.
//
// The size is the ENCODED form, the length doPut sends: "description=" and the
// value, escaped.
func TestSetNodeOptions_RefusesABodyOverTheLimit(t *testing.T) {
	const limit = 512 * 1024
	const prefix = len("description=")

	for _, tt := range []struct {
		name      string
		notes     string
		encodedAs int // the length of the encoded form, for the case's own sanity check
		refused   bool
	}{
		{"exactly the limit", strings.Repeat("a", limit-prefix), limit, false},
		{"one byte over", strings.Repeat("a", limit-prefix+1), limit + 1, true},
		// "é" is two bytes and six once escaped (%C3%A9): 87379 of them encode to 2
		// bytes under the limit and 87380 to 4 over, though neither is near it in
		// characters or in the bytes the value occupies as UTF-8.
		{"accented text is counted after encoding, just under", strings.Repeat("é", 87379), prefix + 87379*6, false},
		{"accented text is counted after encoding, over", strings.Repeat("é", 87380), prefix + 87380*6, true},
		// A line break is three bytes escaped (%0A).
		{"line breaks are counted after encoding", strings.Repeat("\n", (limit-prefix)/3+1), prefix + ((limit-prefix)/3+1)*3, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(url.Values{"description": {tt.notes}}.Encode()); got != tt.encodedAs {
				t.Fatalf("the case is built wrong: the encoded form is %d bytes, the case says %d", got, tt.encodedAs)
			}
			if over := tt.encodedAs > limit; over != tt.refused {
				t.Fatalf("the case is built wrong: %d bytes against a limit of %d, refused = %v", tt.encodedAs, limit, tt.refused)
			}

			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)
			err := c.SetNodeOptions(context.Background(), "pve-01", NodeOptions{Description: tt.notes})
			verr := ValidateNodeOptions(NodeOptions{Description: tt.notes})

			if !tt.refused {
				if err != nil {
					t.Fatalf("SetNodeOptions: %v", err)
				}
				if verr != nil {
					t.Errorf("ValidateNodeOptions = %v, want it to accept what SetNodeOptions accepts", verr)
				}
				if got := oneNodeOptionsWrite(t, seen).Get("description"); got != tt.notes {
					t.Errorf("the notes did not arrive intact (%d characters sent, %d received)", len(tt.notes), len(got))
				}
				return
			}
			if !errors.Is(err, ErrRequestTooLarge) {
				t.Fatalf("err = %v, want ErrRequestTooLarge", err)
			}
			if errors.Is(err, ErrInvalidInput) {
				t.Errorf("err = %v also reads as ErrInvalidInput; a body that is too large is its own refusal (413, not 400)", err)
			}
			if !errors.Is(verr, ErrRequestTooLarge) {
				t.Errorf("ValidateNodeOptions = %v, want the same refusal", verr)
			}
			if len(*seen) != 0 {
				t.Errorf("%d request(s) reached Proxmox, want none: %d bytes was not worth sending", len(*seen), tt.encodedAs)
			}
		})
	}

	// Everything in the body counts, not the notes alone: a digest and a delete
	// list are in it too.
	t.Run("the other keys count towards the limit", func(t *testing.T) {
		const digest = "da39a3ee5e6b4b0d3255bfef95601890afd80709"
		opts := NodeOptions{
			Description: strings.Repeat("a", limit-prefix-len("&digest=")-len(digest)),
			Digest:      digest,
		}
		if got := len(buildNodeOptionsFormForTest(t, opts).Encode()); got != limit {
			t.Fatalf("the case is built wrong: %d bytes, want exactly the limit", got)
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

// buildNodeOptionsFormForTest is buildNodeOptionsForm for a case that is known to
// be valid, failing the test if it is not.
func buildNodeOptionsFormForTest(t *testing.T, opts NodeOptions) url.Values {
	t.Helper()
	form, err := buildNodeOptionsForm(opts)
	if err != nil {
		t.Fatalf("buildNodeOptionsForm: %v", err)
	}
	return form
}

// TestSetNodeACMEConfig_RefusesABodyOverTheLimit: the ACME write is the other
// writer of the file and has the same refusal, for the same reason, at the same
// boundary.
func TestSetNodeACMEConfig_RefusesABodyOverTheLimit(t *testing.T) {
	const limit = 512 * 1024
	const prefix = len("acmedomain0=")

	t.Run("exactly the limit", func(t *testing.T) {
		srv, seen := newFormCaptureServer(t)
		c := newTestClient(t, srv.URL)
		cfg := NodeACMEConfig{ACMEDomain0: strings.Repeat("a", limit-prefix)}
		if err := ValidateNodeACMEConfig(cfg); err != nil {
			t.Fatalf("ValidateNodeACMEConfig: %v", err)
		}
		if err := c.SetNodeACMEConfig(context.Background(), "pve-01", cfg); err != nil {
			t.Fatalf("SetNodeACMEConfig: %v", err)
		}
		if len(*seen) != 1 {
			t.Errorf("issued %d requests, want 1", len(*seen))
		}
	})

	t.Run("one byte over", func(t *testing.T) {
		srv, seen := newFormCaptureServer(t)
		c := newTestClient(t, srv.URL)
		cfg := NodeACMEConfig{ACMEDomain0: strings.Repeat("a", limit-prefix+1)}
		if err := ValidateNodeACMEConfig(cfg); !errors.Is(err, ErrRequestTooLarge) {
			t.Errorf("ValidateNodeACMEConfig = %v, want ErrRequestTooLarge", err)
		}
		if err := c.SetNodeACMEConfig(context.Background(), "pve-01", cfg); !errors.Is(err, ErrRequestTooLarge) {
			t.Fatalf("SetNodeACMEConfig = %v, want ErrRequestTooLarge", err)
		}
		if len(*seen) != 0 {
			t.Errorf("%d request(s) reached Proxmox, want none", len(*seen))
		}
	})
}

// TestValidateNodeOptionsRefusesWhatSetNodeOptionsRefuses: the validator is the
// writer's own check run ahead of time, so the two cannot disagree. Every case is
// run through both, against a stand-in that would answer 200, and must be refused
// by both with the same sentinel and the same words — or accepted by both.
func TestValidateNodeOptionsRefusesWhatSetNodeOptionsRefuses(t *testing.T) {
	cases := map[string]NodeOptions{
		"nothing":                       {},
		"a digest alone":                {Digest: "da39a3ee5e6b4b0d3255bfef95601890afd80709"},
		"set and cleared":               {BallooningTarget: flexPtr(0), Delete: []string{"ballooning-target"}},
		"an ACME key to clear":          {Delete: []string{"acmedomain0"}},
		"a key that is no setting":      {Delete: []string{"digest"}},
		"a control character":           {WakeOnLAN: "02:00:00:00:00:01,\r"},
		"invalid UTF-8":                 {Location: "latitude=0,longitude=0,name=a\xffb"},
		"too large":                     {Description: strings.Repeat("a", 512*1024)},
		"an ordinary write":             {BallooningTarget: flexPtr(75), Description: "sentinel notes\n"},
		"an ordinary clear":             {Delete: []string{"description"}},
		"a write with a digest":         {BallooningTarget: flexPtr(75), Digest: "da39a3ee5e6b4b0d3255bfef95601890afd80709"},
		"notes with controls, which ok": {Description: "a\tb\rc\n"},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			srv, seen := newNodeOptionsServer(t, `{"data":null}`)
			c := newTestClient(t, srv.URL)

			verr := ValidateNodeOptions(opts)
			serr := c.SetNodeOptions(context.Background(), "pve-01", opts)

			if (verr == nil) != (serr == nil) {
				t.Fatalf("ValidateNodeOptions = %v but SetNodeOptions = %v: they must agree", verr, serr)
			}
			if verr == nil {
				return
			}
			if errors.Is(verr, ErrInvalidInput) != errors.Is(serr, ErrInvalidInput) ||
				errors.Is(verr, ErrRequestTooLarge) != errors.Is(serr, ErrRequestTooLarge) {
				t.Errorf("ValidateNodeOptions = %v, SetNodeOptions = %v: not the same refusal", verr, serr)
			}
			if verr.Error() != serr.Error() {
				t.Errorf("ValidateNodeOptions says %q, SetNodeOptions %q", verr, serr)
			}
			if len(*seen) != 0 {
				t.Errorf("a refused request reached Proxmox: %v", *seen)
			}
		})
	}
}
