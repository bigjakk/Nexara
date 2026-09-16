package proxmox

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
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
