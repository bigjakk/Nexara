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

// clientCfgHolder stands in for a struct that holds a ClientConfig as an
// EXPORTED field. Nothing in the tree does today, and that is exactly why it
// is here: the redaction has to already work for the first thing that does,
// which will be written by someone who never reads this file.
//
// The live shape in the tree is the other one — internal/rolling's
// failoverTarget holds a ClientConfig in an UNEXPORTED field, and fmt cannot
// call a method through one (reflect.Value.CanInterface is false), so %+v and
// %#v of that outer struct still print the raw fields. That limitation is
// recorded on the type in client.go, on failoverTarget itself, and in the
// sweep notes in internal/api/handlers/proxmox_read_credentials_test.go. It is
// deliberately not asserted here: a case in this file that expects the secret
// to be PRESENT reads as an endorsement to anyone who greps it later.
type clientCfgHolder struct {
	Cluster string       `json:"cluster"`
	Cfg     ClientConfig `json:"cfg"`
}

// ClientConfig.TokenSecret is the cluster's live API token secret in
// plaintext, held between crypto.Decrypt and the Authorization header. Each
// rendering below is served by a different method — String, GoString,
// LogValue, MarshalJSON — so removing any one of them fails this test on its
// own shapes and no other.
//
// Every case also asserts that NON-credential values SURVIVE. Without that
// half, a String() returning "" or a MarshalJSON() returning {} would satisfy
// every absence assertion while destroying the diagnostics the redaction
// exists to preserve. All THREE survivors — base URL, token id and
// fingerprint — are asserted on every case, because a single survivor would
// let the other two be dropped silently by a redactor that got over-eager.
//
// The slog cases assert the GROUPED key (cfg.base_url=...), not merely the
// value. ClientConfig implements Stringer, so with LogValue deleted slog falls
// through to String and still does not leak — an absence-only slog assertion
// would be masked by String, and LogValue could never be killed on its own.
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

// Each redactor must be in the method set of the VALUE, not just the pointer.
// fmt and encoding/json skip a pointer-receiver method on a value they cannot
// address, and every construction site in the tree builds a ClientConfig
// literal and passes it by value — so a pointer receiver yields a fix that
// compiles, lints clean and redacts nothing.
//
// This is also what makes deleting a method fail a NAMED test rather than
// break compilation: NOTHING in this file calls cfg.String(), cfg.GoString(),
// cfg.LogValue() or cfg.MarshalJSON() directly — every case reaches them the
// way production does, through fmt, slog or encoding/json. Do not "simplify"
// a case into a direct call. A direct call turns the mutation that validates
// this file into a build failure, and a package that does not compile has not
// been shown to redact anything.
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

// clientCfgLogLine renders through a real slog pipeline and returns the
// emitted line, using the JSON handler when asJSON is set.
//
// Both handlers are exercised on purpose, and it is not symmetry for its own
// sake. For a value that is not itself a LogValuer the two take DIFFERENT
// routes: TextHandler formats it with %+v, which dispatches String, while
// JSONHandler MARSHALS it, which dispatches MarshalJSON. Production builds its
// logger with slog.NewJSONHandler (cmd/nexara/main.go:64 and :272, the only
// two handler constructions outside tests), so a Text-only probe does not
// resemble production — and that is exactly how an earlier hardening in this
// sweep passed while the JSON path leaked a live token through a wrapper
// struct. The wrapper cases are the ones that find it: slog.Any on the type
// ITSELF is safe under both handlers, because LogValue resolves before either
// route is taken.
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

// The mirror guard — "redact everything" must not take the credential the
// client actually authenticates with — is deliberately NOT written here.
// Re-reading cfg.TokenSecret back out of the struct fixtureClientConfig() just
// populated asserts nothing: it passes whatever the redactors do, because the
// redactors do not touch the fields. The property is only real when something
// authenticates with it, and TestAuthHeaderFormat (client_test.go) and
// TestTokenAuthRequestsUnchanged (transport_guard_test.go) already pin the
// exact Authorization header bytes against a live in-process server. If either
// is ever removed, this note is the record of what it was load-bearing for.
//
// There is no PropertyString hazard here either, which is the difference from
// TargetEndpoint: newAPIClient reads TokenID and TokenSecret as FIELDS and
// hands them to buildAuthHeader, so no call site can reach the credential —
// or fail to — through a method fmt would dispatch.
