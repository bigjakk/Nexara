package handlers

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// nodeOptionsDetailsJSON is the bytes a node options write's audit details
// marshal to, which is what the audit row stores and a Viewer reads.
func nodeOptionsDetailsJSON(t *testing.T, req proxmox.NodeOptions) string {
	t.Helper()
	out, err := json.Marshal(nodeOptionsAuditDetails(req))
	if err != nil {
		t.Fatalf("marshal the audit details: %v", err)
	}
	return string(out)
}

func flex(n int) *proxmox.FlexInt {
	v := proxmox.FlexInt(n)
	return &v
}

// Values of the settings the row must never carry, each one recognisable in the
// output whatever key it might be recorded under. The MAC and the position are
// synthetic, and the notes are a plain marker: what matters is that they are
// distinct from every key name and from each other.
const (
	sentinelNodeNotes    = "sentinel notes"
	sentinelNodeWake     = "mac=02:00:00:00:00:77,bind-interface=vmbr0,broadcast-address=192.0.2.255"
	sentinelNodeLocation = "latitude=12.5,longitude=-45.25,name=sentinel-site"
)

// TestNodeOptionsAuditDetails pins a node options write's audit details to the
// exact JSON that reaches the row, one case for each thing the builder decides.
//
// The comparison is on the marshalled bytes because they are what the row stores
// and a Viewer reads (view:audit is granted to every built-in Viewer). It is
// exact, so a key the builder gains fails every case that sets the field it would
// come from — whether or not it looks like free text.
//
// One of the guards for these keys. TestNodeOptionsAuditDetailsRecordsOnlyTheAllowedFields
// walks the request type, so a field added to it is classified on purpose, and
// TestNodeOptionsWriteAuditRow in internal/api follows a value through the real
// route, so a handler that stops calling the builder fails too.
func TestNodeOptionsAuditDetails(t *testing.T) {
	unrecorded := []string{sentinelNodeNotes, sentinelNodeWake, sentinelNodeLocation, "02:00:00:00:00:77", "sentinel-site", "sentinel free text"}

	tests := []struct {
		name string
		req  proxmox.NodeOptions
		want string
	}{
		{"nothing set", proxmox.NodeOptions{},
			`{"cleared":[],"settings":[]}`},
		{"only a digest names nothing", proxmox.NodeOptions{Digest: "0123456789abcdef"},
			`{"cleared":[],"settings":[]}`},

		{"every setting at once: names, and the values of the two integers only",
			proxmox.NodeOptions{
				StartallOnbootDelay: flex(37), BallooningTarget: flex(63),
				WakeOnLAN: sentinelNodeWake, Location: sentinelNodeLocation, Description: sentinelNodeNotes,
				Digest: "0123456789abcdef",
			},
			`{"ballooning-target":63,"cleared":[],"settings":["startall-onboot-delay","ballooning-target","wakeonlan","location","description"],"startall-onboot-delay":37}`},

		{"both integers at 0 are recorded as set, with their 0",
			proxmox.NodeOptions{StartallOnbootDelay: flex(0), BallooningTarget: flex(0)},
			`{"ballooning-target":0,"cleared":[],"settings":["startall-onboot-delay","ballooning-target"],"startall-onboot-delay":0}`},
		{"the delay alone",
			proxmox.NodeOptions{StartallOnbootDelay: flex(120)},
			`{"cleared":[],"settings":["startall-onboot-delay"],"startall-onboot-delay":120}`},
		{"the target alone",
			proxmox.NodeOptions{BallooningTarget: flex(80)},
			`{"ballooning-target":80,"cleared":[],"settings":["ballooning-target"]}`},

		{"strings only: names and no value, and no integer keys",
			proxmox.NodeOptions{WakeOnLAN: sentinelNodeWake, Location: sentinelNodeLocation, Description: sentinelNodeNotes},
			`{"cleared":[],"settings":["wakeonlan","location","description"]}`},
		{"the notes alone",
			proxmox.NodeOptions{Description: sentinelNodeNotes},
			`{"cleared":[],"settings":["description"]}`},

		{"a clear and nothing set",
			proxmox.NodeOptions{Delete: []string{"wakeonlan", "location"}},
			`{"cleared":["wakeonlan","location"],"settings":[]}`},
		{"every key cleared",
			proxmox.NodeOptions{Delete: []string{"startall-onboot-delay", "ballooning-target", "wakeonlan", "location", "description"}},
			`{"cleared":["startall-onboot-delay","ballooning-target","wakeonlan","location","description"],"settings":[]}`},
		{"a clear beside a write of a different key",
			proxmox.NodeOptions{BallooningTarget: flex(75), Delete: []string{"wakeonlan"}},
			`{"ballooning-target":75,"cleared":["wakeonlan"],"settings":["ballooning-target"]}`},

		// The row records names, filtered through the allow-list: a string in
		// Delete that is no setting's name is the caller's own text, and every
		// Viewer reads the row.
		{"free text in delete never reaches the row",
			proxmox.NodeOptions{Delete: []string{"wakeonlan", "sentinel free text", "location"}},
			`{"cleared":["wakeonlan","location"],"settings":[]}`},
		{"only free text in delete clears nothing",
			proxmox.NodeOptions{Delete: []string{"sentinel free text"}},
			`{"cleared":[],"settings":[]}`},
		{"an ACME key in delete is not a cleared setting",
			proxmox.NodeOptions{Delete: []string{"acmedomain0", "description"}},
			`{"cleared":["description"],"settings":[]}`},

		// Both spellings of "nothing cleared" read the same: a reader filtering on
		// the row should not have to handle null as well as [].
		{"a nil delete is [], not null", proxmox.NodeOptions{Delete: nil, WakeOnLAN: sentinelNodeWake},
			`{"cleared":[],"settings":["wakeonlan"]}`},
		{"an empty delete is []", proxmox.NodeOptions{Delete: []string{}, WakeOnLAN: sentinelNodeWake},
			`{"cleared":[],"settings":["wakeonlan"]}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nodeOptionsDetailsJSON(t, tc.req)
			if got != tc.want {
				t.Errorf("details = %s, want %s", got, tc.want)
			}
			for _, secret := range unrecorded {
				if strings.Contains(got, secret) {
					t.Errorf("the details %s carry %q, which view:audit would show every Viewer", got, secret)
				}
			}
		})
	}
}

// TestNodeOptionsAuditDetailsRecordsOnlyTheAllowedFields is the allow-list
// checked against the request type rather than against a list of the fields that
// exist today.
//
// It walks every field of proxmox.NodeOptions, sets each on its own to a value
// that stands out, and holds the details to what the field is classified as:
//
//   - the two integers are recorded by name and by value;
//   - Delete is recorded as the key names it carries;
//   - the three free-text settings are recorded by NAME only, never by value;
//   - the digest is not recorded at all.
//
// A field added to the struct is in none of those classes, and fails here until
// somebody decides which it is — which is the point, since the row is readable by
// every Viewer and a new field's default is to be a value that must not be in it.
//
// Three checks are on the test itself, each one a slip in how a field is set: the
// filled request must differ from an empty one, a field of a type the fill does
// not know fails outright, and the walk must still see the fields it exists for.
func TestNodeOptionsAuditDetailsRecordsOnlyTheAllowedFields(t *testing.T) {
	type recording int
	const (
		nothing    recording = iota // the details stay what an empty write's are
		nameOnly                    // its key name is in "settings", its value nowhere
		nameAndInt                  // its key name is in "settings" and its value is recorded
		clearedKey                  // its entries are the "cleared" key names
	)
	classes := map[string]recording{
		"StartallOnbootDelay": nameAndInt,
		"BallooningTarget":    nameAndInt,
		"WakeOnLAN":           nameOnly,
		"Location":            nameOnly,
		"Description":         nameOnly,
		"Delete":              clearedKey,
		"Digest":              nothing,
	}

	empty := nodeOptionsDetailsJSON(t, proxmox.NodeOptions{})

	visited := 0
	for _, field := range reflect.VisibleFields(reflect.TypeOf(proxmox.NodeOptions{})) {
		if !field.IsExported() || field.Anonymous {
			continue
		}
		visited++
		t.Run(field.Name, func(t *testing.T) {
			class, ok := classes[field.Name]
			if !ok {
				t.Fatalf("proxmox.NodeOptions has a field %s this test does not classify — decide what the "+
					"audit row may record of it (view:audit is granted to every Viewer) and add it to classes, "+
					"to nodeOptionsAuditDetails and to its comment", field.Name)
			}
			key := strings.Split(field.Tag.Get("json"), ",")[0]
			const sentinel = "sentinel-value-of-"

			var req proxmox.NodeOptions
			target := reflect.ValueOf(&req).Elem().FieldByIndex(field.Index)
			switch {
			case field.Type == reflect.TypeOf((*proxmox.FlexInt)(nil)):
				target.Set(reflect.ValueOf(flex(424242)))
			case field.Type.Kind() == reflect.String:
				target.SetString(sentinel + field.Name)
			case field.Type == reflect.TypeOf([]string(nil)):
				// A key the client would have accepted: the builder records
				// what it is given, and the client has already refused the rest.
				target.Set(reflect.ValueOf([]string{"wakeonlan"}))
			default:
				t.Fatalf("%s is a %s, which this test does not know how to fill", field.Name, field.Type)
			}
			if reflect.DeepEqual(req, proxmox.NodeOptions{}) {
				t.Fatalf("filling %s left the request empty; this test would pass without checking it", field.Name)
			}

			got := nodeOptionsDetailsJSON(t, req)
			var details map[string]any
			if err := json.Unmarshal([]byte(got), &details); err != nil {
				t.Fatalf("unmarshal %s: %v", got, err)
			}
			settings, _ := details["settings"].([]any)
			cleared, _ := details["cleared"].([]any)

			switch class {
			case nothing:
				if got != empty {
					t.Errorf("setting %s changed the audit details to %s, from %s", field.Name, got, empty)
				}
			case nameOnly:
				if !reflect.DeepEqual(settings, []any{key}) {
					t.Errorf("settings = %v, want just %q", settings, key)
				}
				if strings.Contains(got, sentinel) {
					t.Errorf("setting %s put its value into the audit details %s — audit rows are readable by "+
						"every Viewer, so the row records the key name only", field.Name, got)
				}
				if len(details) != 2 {
					t.Errorf("details = %s, want only settings and cleared", got)
				}
			case nameAndInt:
				if !reflect.DeepEqual(settings, []any{key}) {
					t.Errorf("settings = %v, want just %q", settings, key)
				}
				if v, ok := details[key].(float64); !ok || v != 424242 {
					t.Errorf("details[%q] = %#v, want the integer 424242 recorded: %s", key, details[key], got)
				}
				if len(details) != 3 {
					t.Errorf("details = %s, want settings, cleared and %s only", got, key)
				}
			case clearedKey:
				if !reflect.DeepEqual(cleared, []any{"wakeonlan"}) {
					t.Errorf("cleared = %v, want just %q", cleared, "wakeonlan")
				}
				if len(settings) != 0 {
					t.Errorf("clearing %s named a setting as written: %v", field.Name, settings)
				}
			}
		})
	}

	// Seven fields exist today; five of them carry a setting. Fewer than that means
	// the walk stopped seeing them and every subtest above is vacuous.
	if visited < 7 {
		t.Fatalf("visited %d fields of proxmox.NodeOptions, want at least the seven it has", visited)
	}
}

// nodeViewJSON marshals one of the two read views, which is what a caller of the
// route receives.
func nodeViewJSON(t *testing.T, view any) string {
	t.Helper()
	out, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal the view: %v", err)
	}
	return string(out)
}

// TestNodeOptionsView pins what GET .../options sends, to the exact JSON: the
// four settings and, for a caller who may write, the save token as `digest`, in
// that order — and never the notes, which GET .../notes serves under manage:node,
// where this route is gated on view:node, held by every built-in Viewer. The notes
// are in every input that has them, so a view that started carrying them would
// show in the case that sets them as well as in the one that sets only them.
//
// The view is handed the token and never reads opts.Digest, Proxmox's own digest of
// the whole file. That digest hashes the notes, and is written deterministically, so
// to a caller who cannot read them it is an offline oracle for them; every input
// here carries one, and none may come out, with a token or without.
func TestNodeOptionsView(t *testing.T) {
	const (
		rawDigest = "0123456789abcdef0123456789abcdef01234567"
		token     = "v1.sentinel-save-token"
	)
	everything := proxmox.NodeOptions{
		StartallOnbootDelay: flex(37), BallooningTarget: flex(63),
		WakeOnLAN: sentinelNodeWake, Location: sentinelNodeLocation, Description: sentinelNodeNotes,
		Digest: rawDigest,
	}
	settings := `"startall-onboot-delay":37,"ballooning-target":63,"wakeonlan":"` + sentinelNodeWake +
		`","location":"` + sentinelNodeLocation + `"`
	tests := []struct {
		name  string
		opts  proxmox.NodeOptions
		token string
		want  string
	}{
		{"nothing read", proxmox.NodeOptions{}, "", `{}`},
		{"nothing read, for a caller who may write", proxmox.NodeOptions{}, token, `{"digest":"` + token + `"}`},
		{"every setting, the notes among them, for a caller who may write",
			everything, token,
			`{` + settings + `,"digest":"` + token + `"}`},
		{"the same read for a caller who cannot write has no digest at all",
			everything, "",
			`{` + settings + `}`},
		{"both integers at 0 are present, as 0",
			proxmox.NodeOptions{StartallOnbootDelay: flex(0), BallooningTarget: flex(0)}, "",
			`{"startall-onboot-delay":0,"ballooning-target":0}`},
		{"an integer that is unset is absent, not 0",
			proxmox.NodeOptions{BallooningTarget: flex(80)}, "",
			`{"ballooning-target":80}`},
		{"the notes alone leave the view empty",
			proxmox.NodeOptions{Description: sentinelNodeNotes}, "",
			`{}`},
		{"the notes and Proxmox's digest leave only the token",
			proxmox.NodeOptions{Description: sentinelNodeNotes, Digest: rawDigest}, token,
			`{"digest":"` + token + `"}`},
		{"the notes and Proxmox's digest leave nothing for a caller who cannot write",
			proxmox.NodeOptions{Description: sentinelNodeNotes, Digest: rawDigest}, "",
			`{}`},
		{"what is only ever a write never comes back",
			proxmox.NodeOptions{Delete: []string{"wakeonlan"}, Location: sentinelNodeLocation}, "",
			`{"location":"` + sentinelNodeLocation + `"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nodeViewJSON(t, nodeOptionsView(tc.opts, tc.token))
			if got != tc.want {
				t.Errorf("view = %s, want %s", got, tc.want)
			}
			if strings.Contains(got, sentinelNodeNotes) || strings.Contains(got, `"description"`) {
				t.Errorf("the options view %s carries the notes, which every Viewer could read", got)
			}
			if strings.Contains(got, rawDigest) {
				t.Errorf("the options view %s carries Proxmox's own digest, an offline oracle for the notes", got)
			}
		})
	}
}

// TestNodeNotesView pins what GET .../notes sends, to the exact JSON: the notes
// and the save token as `digest` and nothing else — none of the settings, which
// are read under view:node and have a route of their own. The notes come through
// byte for byte: the view does not trim, wrap or re-encode them, and a note that is
// only whitespace is still a note. Like the options view, it is handed the token and
// never reads opts.Digest.
func TestNodeNotesView(t *testing.T) {
	const (
		rawDigest = "0123456789abcdef0123456789abcdef01234567"
		token     = "v1.sentinel-save-token"
	)
	tests := []struct {
		name  string
		opts  proxmox.NodeOptions
		token string
		want  string
	}{
		{"nothing read", proxmox.NodeOptions{}, "", `{}`},
		{"the notes and a token, among every setting",
			proxmox.NodeOptions{
				StartallOnbootDelay: flex(37), BallooningTarget: flex(63),
				WakeOnLAN: sentinelNodeWake, Location: sentinelNodeLocation, Description: sentinelNodeNotes,
				Digest: rawDigest,
			}, token,
			`{"description":"` + sentinelNodeNotes + `","digest":"` + token + `"}`},
		{"a node with settings and no notes has only its token",
			proxmox.NodeOptions{StartallOnbootDelay: flex(0), WakeOnLAN: sentinelNodeWake, Digest: rawDigest}, token,
			`{"digest":"` + token + `"}`},
		{"a node with no config file has neither",
			proxmox.NodeOptions{}, "",
			`{}`},
		{"the notes as Proxmox hands them back, line breaks and a trailing one included",
			proxmox.NodeOptions{Description: "sentinel notes\n  indented\n\nafter a blank line\n"}, "",
			`{"description":"sentinel notes\n  indented\n\nafter a blank line\n"}`},
		{"the settings alone leave the view empty",
			proxmox.NodeOptions{BallooningTarget: flex(75), Location: sentinelNodeLocation}, "",
			`{}`},
		{"Proxmox's digest is never shown, with or without a token",
			proxmox.NodeOptions{Description: sentinelNodeNotes, Digest: rawDigest}, "",
			`{"description":"` + sentinelNodeNotes + `"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := nodeViewJSON(t, nodeNotesView(tc.opts, tc.token))
			if got != tc.want {
				t.Errorf("view = %s, want %s", got, tc.want)
			}
			for _, setting := range []string{sentinelNodeWake, sentinelNodeLocation, "startall-onboot-delay", "ballooning-target", "wakeonlan", `"location"`} {
				if strings.Contains(got, setting) {
					t.Errorf("the notes view %s carries %q, a setting that belongs to the options view", got, setting)
				}
			}
			if strings.Contains(got, rawDigest) {
				t.Errorf("the notes view %s carries Proxmox's own digest", got)
			}
		})
	}

	t.Run("a description round-trips byte for byte", func(t *testing.T) {
		for _, notes := range []string{
			"  leading and trailing space  ",
			"\n",
			"\t\ttabs\r\nand a carriage return",
			"<b>markup</b> & an ampersand",
			"Gebäude Ost 東棟 and a combining accent: e\u0301",
			sentinelNodeNotes,
		} {
			var back nodeNotesResponse
			if err := json.Unmarshal([]byte(nodeViewJSON(t, nodeNotesView(proxmox.NodeOptions{Description: notes}, ""))), &back); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if back.Description != notes {
				t.Errorf("notes %q came back as %q", notes, back.Description)
			}
		}
	})
}

// TestNodeViewsClassifyEveryNodeOptionsField is the two views checked against the
// type they are made from rather than against the fields that exist today.
//
// It walks every field of proxmox.NodeOptions, sets each on its own to a value
// that stands out, and holds both views to the class the field is in: the four
// settings are in the options view, the notes in the notes view, and the digest
// and the delete list in neither — the digest because it is Proxmox's own, and an
// oracle for the notes, and what the views show as `digest` is the token they are
// handed instead; the delete list because it is only ever a write. A field added
// to the struct is in none of those classes and fails here until somebody decides
// which view it belongs to — which is the point: the options view is served to
// every Viewer, so a field's default must be to reach nobody, and a view that
// serialised NodeOptions whole would hand it to everybody instead.
//
// Three checks are on the test itself, each one a slip in how a field is set: the
// filled value must differ from an empty one, a field of a type the fill does
// not know fails outright, and the walk must still see the fields it exists for.
func TestNodeViewsClassifyEveryNodeOptionsField(t *testing.T) {
	type class struct{ options, notes bool }
	classes := map[string]class{
		"StartallOnbootDelay": {options: true},
		"BallooningTarget":    {options: true},
		"WakeOnLAN":           {options: true},
		"Location":            {options: true},
		"Description":         {notes: true},
		"Digest":              {},
		"Delete":              {},
	}

	visited := 0
	for _, field := range reflect.VisibleFields(reflect.TypeOf(proxmox.NodeOptions{})) {
		if !field.IsExported() || field.Anonymous {
			continue
		}
		visited++
		t.Run(field.Name, func(t *testing.T) {
			want, ok := classes[field.Name]
			if !ok {
				t.Fatalf("proxmox.NodeOptions has a field %s this test does not classify — decide which read view "+
					"it belongs to (the options view reaches every Viewer, the notes view only manage:node) and add it "+
					"to classes, to nodeOptionsView or nodeNotesView, and to their comments", field.Name)
			}
			const sentinel = "sentinel-value-of-"
			var opts proxmox.NodeOptions
			target := reflect.ValueOf(&opts).Elem().FieldByIndex(field.Index)
			switch {
			case field.Type == reflect.TypeOf((*proxmox.FlexInt)(nil)):
				target.Set(reflect.ValueOf(flex(424242)))
			case field.Type.Kind() == reflect.String:
				target.SetString(sentinel + field.Name)
			case field.Type == reflect.TypeOf([]string(nil)):
				target.Set(reflect.ValueOf([]string{sentinel + field.Name}))
			default:
				t.Fatalf("%s is a %s, which this test does not know how to fill", field.Name, field.Type)
			}
			if reflect.DeepEqual(opts, proxmox.NodeOptions{}) {
				t.Fatalf("filling %s left the options empty; this test would pass without checking it", field.Name)
			}

			marker := sentinel + field.Name
			if field.Type == reflect.TypeOf((*proxmox.FlexInt)(nil)) {
				marker = "424242"
			}
			// With a token and without one: a view that falls back to the field when
			// it has no token is the leak this is for.
			for _, tok := range []string{"", "v1.sentinel-save-token"} {
				for _, view := range []struct {
					name string
					got  string
					want bool
				}{
					{"options", nodeViewJSON(t, nodeOptionsView(opts, tok)), want.options},
					{"notes", nodeViewJSON(t, nodeNotesView(opts, tok)), want.notes},
				} {
					if has := strings.Contains(view.got, marker); has != view.want {
						t.Errorf("the %s view is %s with %s set and the token %q: it carries the value = %v, want %v",
							view.name, view.got, field.Name, tok, has, view.want)
					}
				}
			}
		})
	}
	if visited < 7 {
		t.Fatalf("visited %d fields of proxmox.NodeOptions, want at least the seven it has", visited)
	}

	// What both views do show as `digest` is the token they are given, and only
	// that.
	t.Run("the token is the digest both views show", func(t *testing.T) {
		const tok = "v1.sentinel-save-token"
		if got := nodeViewJSON(t, nodeOptionsView(proxmox.NodeOptions{}, tok)); got != `{"digest":"`+tok+`"}` {
			t.Errorf("options view = %s", got)
		}
		if got := nodeViewJSON(t, nodeNotesView(proxmox.NodeOptions{}, tok)); got != `{"digest":"`+tok+`"}` {
			t.Errorf("notes view = %s", got)
		}
	})
}
