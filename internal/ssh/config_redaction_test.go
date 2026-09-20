package ssh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// The fixture connection. Every value is synthetic.
//
// Both secrets are deliberately SINGLE-LINE and free of any character Go
// escapes when it quotes a string. A multi-line PEM fixture would be printed
// by %#v as "-----BEGIN...\nNOT-A-REAL-KEY\n..." — with literal backslash-n —
// so a strings.Contains for the raw fixture could not see the leak, and every
// absence assertion below would pass against a completely broken GoString.
// The fixture shape is load-bearing, not cosmetic.
const (
	fixtureCfgHost     = "pve-01.example.com"
	fixtureCfgPort     = 22
	fixtureCfgUser     = "nexara"
	fixtureCfgPassword = "pw-22222222-3333-4444-5555-666666666666"
	fixtureCfgKey      = "pk-77777777-8888-9999-aaaa-bbbbbbbbbbbb"
)

func fixtureCfg() Config {
	return Config{
		Host:       fixtureCfgHost,
		Port:       fixtureCfgPort,
		Username:   fixtureCfgUser,
		Password:   fixtureCfgPassword,
		PrivateKey: fixtureCfgKey,
	}
}

// cfgHolder stands in for a struct that holds a Config as a field. Nothing in
// the tree does today, and that is exactly why it is here: the redaction has
// to already work for the first thing that does, which will be written by
// someone who never reads this file.
type cfgHolder struct {
	Node string `json:"node"`
	Cfg  Config `json:"cfg"`
}

// Config.Password and Config.PrivateKey are live node credentials, and the
// type is passed by value through three construction sites that log heavily
// around it. Each rendering below is served by a different method — String,
// GoString, LogValue, MarshalJSON — so removing any one of them fails this
// test on its own shape and no other.
//
// Every case also asserts a NON-credential value SURVIVES. Without that half,
// a String() returning "" or a MarshalJSON() returning {} would satisfy every
// absence assertion while destroying the diagnostics the redaction exists to
// preserve.
//
// The slog case asserts the GROUPED key (cfg.host=...), not merely the host.
// Config implements Stringer, so with LogValue deleted slog falls through to
// String and still does not leak — an absence-only slog assertion would be
// masked by String and LogValue could never be killed on its own.
func TestGuard_SSHConfigNeverPrintsItsCredentials(t *testing.T) {
	cfg := fixtureCfg()
	holder := cfgHolder{Node: "pve-01", Cfg: cfg}

	mustMarshal := func(v any) string {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		return string(raw)
	}

	cases := []struct {
		name string
		// rendered is the text the route produces.
		rendered string
		// survives proves the rendering still says something useful.
		survives string
	}{
		{"fmt %v", fmt.Sprintf("%v", cfg), fixtureCfgHost},
		{"fmt %v pointer", fmt.Sprintf("%v", &cfg), fixtureCfgHost},
		{"fmt %s", renderVerb("%s", cfg), fixtureCfgHost},
		{"fmt %+v", fmt.Sprintf("%+v", cfg), fixtureCfgHost},
		{"fmt %q", renderVerb("%q", cfg), fixtureCfgHost},
		{"fmt.Sprint", fmt.Sprint(cfg), fixtureCfgHost},
		// %#v dispatches GoStringer, not Stringer. All three shapes print the
		// raw struct literal without it.
		{"fmt %#v", fmt.Sprintf("%#v", cfg), fixtureCfgHost},
		{"fmt %#v pointer", fmt.Sprintf("%#v", &cfg), fixtureCfgHost},
		{"fmt %#v holder", fmt.Sprintf("%#v", holder), fixtureCfgHost},
		{"fmt %+v holder", fmt.Sprintf("%+v", holder), fixtureCfgHost},
		// A Config that lands inside an error is the route that reaches a
		// persisted, Viewer-readable column.
		{"error wrapping", fmt.Errorf("ssh dial: %v", cfg).Error(), fixtureCfgHost},
		{"json.Marshal value", mustMarshal(cfg), `"host":"` + fixtureCfgHost + `"`},
		{"json.Marshal pointer", mustMarshal(&cfg), `"host":"` + fixtureCfgHost + `"`},
		{"json.Marshal holder", mustMarshal(holder), `"host":"` + fixtureCfgHost + `"`},
		{"json.Marshal slice", mustMarshal([]Config{cfg}), `"host":"` + fixtureCfgHost + `"`},
		{"slog text Any", cfgLogLine(t, false, slog.Any("cfg", cfg)), "cfg.host=" + fixtureCfgHost},
		{"slog text Any pointer", cfgLogLine(t, false, slog.Any("cfg", &cfg)), "cfg.host=" + fixtureCfgHost},
		{"slog json Any", cfgLogLine(t, true, slog.Any("cfg", cfg)), `"host":"` + fixtureCfgHost + `"`},
		{"slog json Any pointer", cfgLogLine(t, true, slog.Any("cfg", &cfg)), `"host":"` + fixtureCfgHost + `"`},
		// The wrapper under the JSON handler is the shape that goes through
		// MarshalJSON rather than String. It is the one that catches a type
		// with no MarshalJSON at all.
		{"slog text Any holder", cfgLogLine(t, false, slog.Any("cfg", holder)), fixtureCfgHost},
		{"slog json Any holder", cfgLogLine(t, true, slog.Any("cfg", holder)), `"host":"` + fixtureCfgHost + `"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.rendered, fixtureCfgPassword) {
				t.Errorf("the password is in the rendering: %s", tc.rendered)
			}
			if strings.Contains(tc.rendered, fixtureCfgKey) {
				t.Errorf("the private key is in the rendering: %s", tc.rendered)
			}
			if !strings.Contains(tc.rendered, tc.survives) {
				t.Errorf("the rendering dropped %q and is no longer worth emitting: %s",
					tc.survives, tc.rendered)
			}
		})
	}
}

// Each redactor must be in the method set of the VALUE, not just the pointer.
// fmt and encoding/json skip a pointer-receiver method on a value they cannot
// address, and all three construction sites pass a Config by value — so a
// pointer receiver yields a fix that compiles, lints clean and redacts
// nothing.
//
// This is also what makes deleting a method fail a NAMED test rather than
// break compilation: nothing above calls cfg.String() or cfg.GoString()
// directly, so the whole suite still builds with any one redactor removed and
// reports which one went missing.
func TestSSHConfigRedactorsAreInTheValueMethodSet(t *testing.T) {
	// Deliberately a value. Assigning to `any` is what strips addressability,
	// which is precisely the condition fmt and encoding/json are in.
	var v any = fixtureCfg()

	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"fmt.Stringer (%v, %s, fmt.Sprint, error wrapping)", func() bool { _, ok := v.(fmt.Stringer); return ok }()},
		{"fmt.GoStringer (%#v)", func() bool { _, ok := v.(fmt.GoStringer); return ok }()},
		{"slog.LogValuer (structured logs)", func() bool { _, ok := v.(slog.LogValuer); return ok }()},
		{"json.Marshaler (encoding/json)", func() bool { _, ok := v.(json.Marshaler); return ok }()},
	} {
		if !tc.ok {
			t.Errorf("Config (as a value) does not implement %s — the method is either "+
				"missing or has a pointer receiver, and the rendering it guards is open", tc.name)
		}
	}
}

// renderVerb applies a format verb through a non-constant format string.
//
// Do not inline this back into fmt.Sprintf("%s", cfg). The %s and %q verbs on
// a struct are a go vet printf error unless the type implements Stringer — so
// with the verb written as a constant, the mutation that validates this file
// (delete String, or flip its receiver to a pointer) stops the package
// BUILDING instead of failing a named test, and no other shape in the table
// is ever run. vet cannot check a non-constant format, and
// the signature is deliberately non-variadic so vet does not infer this as a
// printf wrapper and check the call sites instead.
func renderVerb(verb string, v any) string { return fmt.Sprintf(verb, v) }

// cfgLogLine renders through a real slog pipeline and returns the emitted
// line, using the JSON handler when asJSON is set.
//
// Both handlers are exercised on purpose, and it is not symmetry for its own
// sake. For a value that is not itself a LogValuer the two take DIFFERENT
// routes: TextHandler formats it with %+v, which dispatches String, while
// JSONHandler MARSHALS it, which dispatches MarshalJSON. Production builds
// its logger with slog.NewJSONHandler (cmd/nexara/main.go:64 and :272), so a
// Text-only probe does not resemble production — and that is exactly how an
// earlier version of these files passed while the JSON path leaked a live
// token through a wrapper struct. The wrapper cases are the ones that found
// it: slog.Any on the type ITSELF is safe under both handlers, because
// LogValue resolves before either route is taken.
func cfgLogLine(t *testing.T, asJSON bool, attr slog.Attr) string {
	t.Helper()
	var buf bytes.Buffer
	var h slog.Handler
	if asJSON {
		h = slog.NewJSONHandler(&buf, nil)
	} else {
		h = slog.NewTextHandler(&buf, nil)
	}
	slog.New(h).Info("ssh connect", attr)
	return buf.String()
}

// The mirror guard — "redact everything" must not take the credentials the
// handshake needs — is deliberately NOT written here. Re-reading cfg.Password
// back out of the struct that fixtureCfg() just populated asserts nothing: it
// passes whatever the redactors do, because the redactors do not touch the
// fields. The property is only real when something authenticates with them,
// and TestExecute_succeedsWithMatchingKey in client_test.go already does that
// against a live in-process SSH server with a password set. If that test is
// ever removed, this one is the note saying what it was load-bearing for.
