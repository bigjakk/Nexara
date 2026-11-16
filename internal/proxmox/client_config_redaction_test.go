package proxmox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// The fixture config. Every value is synthetic: the token id is the house
// fixture, the host is the house node name, the secret is a made-up UUID and
// the fingerprint is synthetic hex.
//
// The secret is deliberately SINGLE-LINE and free of any character Go escapes
// when it quotes a string. A multi-line or quote-bearing fixture would be
// printed by %#v with a literal backslash escape, so a strings.Contains for
// the raw fixture could not see the leak and every absence assertion below
// would pass against a completely broken GoString. The fixture shape is
// load-bearing, not cosmetic.
const (
	fixtureCfgBaseURL = "https://pve-01.example.com:8006"
	fixtureCfgTokenID = "nexara@pve!api"
	fixtureCfgSecret  = "ts-a1a1a1a1-b2b2-c3c3-d4d4-e5e5e5e5e5e5"
	fixtureCfgFP      = "1A:2B:3C:4D:5E:6F:70:81:92:A3:B4:C5:D6:E7:F8:09"
	fixtureCfgTimeout = 30 * time.Second
)

func fixtureClientConfig() ClientConfig {
	return ClientConfig{
		BaseURL:        fixtureCfgBaseURL,
		TokenID:        fixtureCfgTokenID,
		TokenSecret:    fixtureCfgSecret,
		TLSFingerprint: fixtureCfgFP,
		Timeout:        fixtureCfgTimeout,
	}
}

// clientCfgHolder holds a ClientConfig as an EXPORTED field, which nothing in the tree does
// yet: the redaction must already work for the first thing that does. The unexported-field
// case (internal/rolling's failoverTarget, where fmt cannot reach the methods) is recorded on
// the type in client.go and deliberately not asserted here.
type clientCfgHolder struct {
	Cluster string       `json:"cluster"`
	Cfg     ClientConfig `json:"cfg"`
}

// ClientConfig.TokenSecret is the live token secret. Each rendering (String, GoString,
// LogValue, MarshalJSON) is its own case, so removing one fails on its own shapes; every case
// asserts all three non-secret values (base URL, token id, fingerprint) survive, or a blank
// redactor would pass. The slog cases assert the grouped key: without LogValue slog falls
// back to String, which does not leak either, and would mask its loss.
func TestGuard_ClientConfigNeverPrintsItsTokenSecret(t *testing.T) {
	cfg := fixtureClientConfig()
	holder := clientCfgHolder{Cluster: "cluster01", Cfg: cfg}

	mustMarshal := func(v any) string {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		return string(raw)
	}

	// plain is the survivor set for a rendering that emits the field values
	// as-is; encoded is the same three values as they appear on the wire.
	plain := []string{fixtureCfgBaseURL, fixtureCfgTokenID, fixtureCfgFP}
	encoded := []string{
		`"base_url":"` + fixtureCfgBaseURL + `"`,
		`"token_id":"` + fixtureCfgTokenID + `"`,
		`"tls_fingerprint":"` + fixtureCfgFP + `"`,
	}

	cases := []struct {
		name string
		// rendered is the text the route produces.
		rendered string
		// survives are substrings proving the rendering still says something
		// useful. All must be present.
		survives []string
		// absent overrides the secret the case searches for. ANY rendering
		// that TRANSFORMS the text must override it, or the absence assertion
		// is vacuous — it would search for bytes that cannot appear and pass
		// against a completely open String. Only the hex verbs need it TODAY;
		// that is the table, not the rule. Note nothing enforces this the way
		// the len(survives) == 0 fatal enforces the other half.
		absent []string
	}{
		{name: "fmt %v", rendered: fmt.Sprintf("%v", cfg), survives: plain},
		{name: "fmt %v pointer", rendered: fmt.Sprintf("%v", &cfg), survives: plain},
		{name: "fmt %s", rendered: renderCfgVerb("%s", cfg), survives: plain},
		{name: "fmt %q", rendered: renderCfgVerb("%q", cfg), survives: plain},
		// fmt dispatches Stringer for %x and %X too — the least obvious
		// members of the set String() has to cover. Both the survivor and the
		// secret have to be hex-encoded to match, which is the whole reason
		// `absent` exists.
		{
			name:     "fmt %x",
			rendered: renderCfgVerb("%x", cfg),
			// All three, like every other case. Hex encoding is a per-byte
			// homomorphism, so each survivor's encoding is a contiguous
			// substring of the whole rendering — there is no reason for these
			// two cases to pin less than the rest, and when they did, dropping
			// token_id and tls_fingerprint from String() failed eight cases
			// and passed here.
			survives: []string{hexOf(fixtureCfgBaseURL), hexOf(fixtureCfgTokenID), hexOf(fixtureCfgFP)},
			absent:   []string{hexOf(fixtureCfgSecret)},
		},
		{
			name:     "fmt %X",
			rendered: renderCfgVerb("%X", cfg),
			survives: []string{
				strings.ToUpper(hexOf(fixtureCfgBaseURL)),
				strings.ToUpper(hexOf(fixtureCfgTokenID)),
				strings.ToUpper(hexOf(fixtureCfgFP)),
			},
			absent: []string{strings.ToUpper(hexOf(fixtureCfgSecret))},
		},
		{name: "fmt %+v", rendered: fmt.Sprintf("%+v", cfg), survives: plain},
		{name: "fmt.Sprint", rendered: fmt.Sprint(cfg), survives: plain},
		// %#v dispatches GoStringer, not Stringer. All three shapes print the
		// raw struct literal without it.
		{name: "fmt %#v", rendered: fmt.Sprintf("%#v", cfg), survives: plain},
		{name: "fmt %#v pointer", rendered: fmt.Sprintf("%#v", &cfg), survives: plain},
		{name: "fmt %#v holder", rendered: fmt.Sprintf("%#v", holder), survives: plain},
		{name: "fmt %+v holder", rendered: fmt.Sprintf("%+v", holder), survives: plain},
		// A config that lands inside an error is the route that reaches a
		// persisted, Viewer-readable column.
		{name: "error wrapping", rendered: fmt.Errorf("proxmox dial: %v", cfg).Error(), survives: plain},
		{name: "json.Marshal value", rendered: mustMarshal(cfg), survives: encoded},
		{name: "json.Marshal pointer", rendered: mustMarshal(&cfg), survives: encoded},
		{name: "json.Marshal holder", rendered: mustMarshal(holder), survives: encoded},
		// A slice element is ADDRESSABLE, so encoding/json would reach a
		// pointer-receiver MarshalJSON here even though it cannot on the bare
		// value above. Kept deliberately: it is the shape that shows the
		// value-receiver requirement is about addressability, not about
		// json.Marshal in general.
		{name: "json.Marshal slice", rendered: mustMarshal([]ClientConfig{cfg}), survives: encoded},
		{
			name:     "slog text Any",
			rendered: clientCfgLogLine(t, false, slog.Any("cfg", cfg)),
			survives: []string{
				"cfg.base_url=" + fixtureCfgBaseURL,
				"cfg.token_id=" + fixtureCfgTokenID,
				"cfg.tls_fingerprint=" + fixtureCfgFP,
			},
		},
		{
			name:     "slog text Any pointer",
			rendered: clientCfgLogLine(t, false, slog.Any("cfg", &cfg)),
			survives: []string{
				"cfg.base_url=" + fixtureCfgBaseURL,
				"cfg.token_id=" + fixtureCfgTokenID,
				"cfg.tls_fingerprint=" + fixtureCfgFP,
			},
		},
		// These two pin the PAIR, not either member. LogValue resolves before
		// the JSON handler would marshal, so deleting LogValue hands the shape
		// to MarshalJSON and deleting MarshalJSON leaves LogValue serving it —
		// neither deletion fails these cases, and no other case in this table
		// has that property. (A LogValue that survives but over-redacts does
		// fail them, through the survivor assertions.) Kept because the pair
		// is what production relies on for a ClientConfig logged directly
		// under the JSON handler; internal/ssh/config_redaction_test.go has
		// the identical property for the same reason.
		{name: "slog json Any", rendered: clientCfgLogLine(t, true, slog.Any("cfg", cfg)), survives: encoded},
		{name: "slog json Any pointer", rendered: clientCfgLogLine(t, true, slog.Any("cfg", &cfg)), survives: encoded},
		// The wrapper is the shape that goes through MarshalJSON rather than
		// String under the JSON handler. It is the one that catches a type
		// with no MarshalJSON at all.
		{name: "slog text Any holder", rendered: clientCfgLogLine(t, false, slog.Any("cfg", holder)), survives: plain},
		{name: "slog json Any holder", rendered: clientCfgLogLine(t, true, slog.Any("cfg", holder)), survives: encoded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.survives) == 0 {
				t.Fatal("no survivor asserted — an absence-only case passes against a rendering that emits nothing")
			}
			absent := tc.absent
			if len(absent) == 0 {
				absent = []string{fixtureCfgSecret}
			}
			for _, secret := range absent {
				if strings.Contains(tc.rendered, secret) {
					t.Errorf("the token secret is in the rendering: %s", tc.rendered)
				}
			}
			for _, want := range tc.survives {
				if !strings.Contains(tc.rendered, want) {
					t.Errorf("the rendering dropped %q and is no longer worth emitting: %s",
						want, tc.rendered)
				}
			}
		})
	}
}

// hexOf is what %x renders a string as. Both halves of a hex case have to go
// through it: the survivor so the assertion can find it, and the secret so the
// absence assertion is not vacuous.
func hexOf(s string) string { return fmt.Sprintf("%x", s) }

// Each redactor must be in the VALUE's method set: fmt and encoding/json skip a
// pointer-receiver method on a value they cannot address, and every site passes a ClientConfig
// by value. Nothing here calls a redactor directly, so deleting one still compiles and fails
// a named test; do not "simplify" a case into a direct call.
func TestClientConfigRedactorsAreInTheValueMethodSet(t *testing.T) {
	// Deliberately a value assigned to `any`. That is what strips
	// addressability, which is precisely the condition fmt and encoding/json
	// are in.
	var v any = fixtureClientConfig()

	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"fmt.Stringer (%v, %s, %q, %x, %X, fmt.Sprint, error wrapping)", func() bool { _, ok := v.(fmt.Stringer); return ok }()},
		{"fmt.GoStringer (%#v)", func() bool { _, ok := v.(fmt.GoStringer); return ok }()},
		{"slog.LogValuer (structured logs)", func() bool { _, ok := v.(slog.LogValuer); return ok }()},
		{"json.Marshaler (encoding/json, and slog's JSON handler via a wrapper)", func() bool { _, ok := v.(json.Marshaler); return ok }()},
	} {
		if !tc.ok {
			t.Errorf("ClientConfig (as a value) does not implement %s — the method is either "+
				"missing or has a pointer receiver, and the rendering it guards is open", tc.name)
		}
	}
}

// renderCfgVerb applies a format verb through a NON-CONSTANT format string.
//
// Do not inline this back into fmt.Sprintf("%s", cfg). The %s, %q, %x and %X
// verbs on a struct are a go vet printf error unless every field is printable
// with them — so with the verb written as a constant, the mutation that
// validates this file (delete String, or flip its receiver to a pointer) can
// stop the package BUILDING instead of failing a named test, and no other
// shape in the table is ever run. vet cannot check a non-constant format, and
// the signature is deliberately non-variadic so vet does not infer this as a
// printf wrapper and check the call sites instead.
func renderCfgVerb(verb string, v any) string { return fmt.Sprintf(verb, v) }

// clientCfgLogLine renders attr through a real slog pipeline, JSON handler when asJSON. Both
// run because they differ for a value that is not a LogValuer: Text dispatches String, JSON
// (production's) dispatches MarshalJSON. A Text-only probe once passed while a wrapper struct
// leaked a live token through the JSON path.
func clientCfgLogLine(t *testing.T, asJSON bool, attr slog.Attr) string {
	t.Helper()
	var buf bytes.Buffer
	var h slog.Handler
	if asJSON {
		h = slog.NewJSONHandler(&buf, nil)
	} else {
		h = slog.NewTextHandler(&buf, nil)
	}
	slog.New(h).Info("proxmox connect", attr)
	return buf.String()
}

// No "the client still authenticates" guard here: reading TokenSecret back out of the fixture
// asserts nothing. TestAuthHeaderFormat and TestTokenAuthRequestsUnchanged pin the exact
// Authorization bytes against a live server; remove neither. newAPIClient reads TokenID and
// TokenSecret as fields, so no call site reaches the credential through a method fmt dispatches.
