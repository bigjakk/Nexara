package veeam

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// The fixture grant body. Both tokens are synthetic and deliberately
// single-line with no character Go escapes when quoting a string — a
// multi-line fixture would be printed by %#v with a literal backslash-n, so a
// strings.Contains for the raw value could not see the leak and every absence
// assertion below would pass against a completely broken GoString.
const (
	fixtureTRAccess  = "va-11111111-2222-3333-4444-555555555555"
	fixtureTRRefresh = "vr-66666666-7777-8888-9999-aaaaaaaaaaaa"
	fixtureTRType    = "bearer"
	fixtureTRExpires = 900
)

func fixtureTokenResponse() tokenResponse {
	return tokenResponse{
		AccessToken:  fixtureTRAccess,
		TokenType:    fixtureTRType,
		RefreshToken: fixtureTRRefresh,
		ExpiresIn:    fixtureTRExpires,
	}
}

// tokenResponse holds two live VBR credentials, decoded in requestToken() just above two error
// returns; nothing renders one yet. Its two non-secret fields make a non-vacuous test possible:
// every case proves one survives. The slog case asserts the grouped key: without LogValue slog
// falls back to String, which does not leak either, and would mask its loss.
func TestGuard_VeeamTokenResponseNeverPrintsItsTokens(t *testing.T) {
	tr := fixtureTokenResponse()
	holder := trHolder{URL: "https://vbr.example.com", Body: tr}

	cases := []struct {
		name string
		// rendered is the text the route produces.
		rendered string
		// survives proves the rendering still says something useful.
		survives string
	}{
		{"fmt %v", fmt.Sprintf("%v", tr), fixtureTRType},
		{"fmt %s", renderTokenVerb("%s", tr), fixtureTRType},
		{"fmt %+v", fmt.Sprintf("%+v", tr), fixtureTRType},
		{"fmt %q", renderTokenVerb("%q", tr), fixtureTRType},
		{"fmt.Sprint", fmt.Sprint(tr), fixtureTRType},
		// %#v dispatches GoStringer, not Stringer.
		{"fmt %#v", fmt.Sprintf("%#v", tr), fixtureTRType},
		{"fmt %#v pointer", fmt.Sprintf("%#v", &tr), fixtureTRType},
		// The route that actually exists: an error assembled next to the
		// decoded body in requestToken().
		{"error wrapping", fmt.Errorf("veeam: decode token response: %v", tr).Error(), fixtureTRType},
		{"slog text Any", tokenLogLine(t, false, slog.Any("tr", tr)), "tr.token_type=" + fixtureTRType},
		{"slog json Any", tokenLogLine(t, true, slog.Any("tr", tr)), `"token_type":"` + fixtureTRType + `"`},
		// A tokenResponse reached as an exported field of a context struct.
		// Under the JSON handler this goes through MarshalJSON, not String —
		// the route that leaked both tokens before MarshalJSON existed.
		{"slog text Any holder", tokenLogLine(t, false, slog.Any("tr", holder)), fixtureTRType},
		{"slog json Any holder", tokenLogLine(t, true, slog.Any("tr", holder)), `"token_type":"` + fixtureTRType + `"`},
		{"json.Marshal value", mustMarshalTR(t, tr), `"token_type":"` + fixtureTRType + `"`},
		{"json.Marshal holder", mustMarshalTR(t, holder), `"token_type":"` + fixtureTRType + `"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.rendered, fixtureTRAccess) {
				t.Errorf("the access token is in the rendering: %s", tc.rendered)
			}
			if strings.Contains(tc.rendered, fixtureTRRefresh) {
				t.Errorf("the refresh token is in the rendering: %s", tc.rendered)
			}
			if !strings.Contains(tc.rendered, tc.survives) {
				t.Errorf("the rendering dropped %q and is no longer worth emitting: %s",
					tc.survives, tc.rendered)
			}
		})
	}
}

// The redactors must be in the VALUE's method set (requestToken holds a value), and nothing
// calls them directly, so deleting one fails a named test. json.Marshaler is required: slog's
// JSON handler, production's, marshals a tokenResponse in a wrapper's exported field rather
// than calling String (see tokenLogLine).
func TestVeeamTokenResponseRedactorsAreInTheValueMethodSet(t *testing.T) {
	var v any = fixtureTokenResponse()

	if _, ok := v.(fmt.Stringer); !ok {
		t.Error("tokenResponse (as a value) does not implement fmt.Stringer — the v, s and " +
			"q verbs, fmt.Sprint and error wrapping all print the raw tokens")
	}
	if _, ok := v.(fmt.GoStringer); !ok {
		t.Error("tokenResponse (as a value) does not implement fmt.GoStringer — the sharp-v " +
			"verb prints the struct literal with both tokens in it")
	}
	if _, ok := v.(slog.LogValuer); !ok {
		t.Error("tokenResponse (as a value) does not implement slog.LogValuer — structured " +
			"logs fall through to String instead of emitting fields")
	}
	if _, ok := v.(json.Marshaler); !ok {
		t.Error("tokenResponse (as a value) does not implement json.Marshaler — slog's JSON " +
			"handler marshals a wrapper holding one, so both tokens reach stdout in cleartext")
	}
}

// The fields requestToken() actually reads must survive the redaction: this is
// the mirror guard against a "redact everything" change that also blanks the
// token the client needs.
func TestVeeamTokenResponseStillCarriesItsTokens(t *testing.T) {
	tr := fixtureTokenResponse()
	if tr.AccessToken != fixtureTRAccess {
		t.Errorf("AccessToken = %q, want the fixture access token", tr.AccessToken)
	}
	if tr.RefreshToken != fixtureTRRefresh {
		t.Errorf("RefreshToken = %q, want the fixture refresh token", tr.RefreshToken)
	}

	// The live json tags are load-bearing: the struct exists only to be
	// decoded from VBR's grant body, and json:"-" would break authentication.
	var decoded tokenResponse
	body := `{"access_token":"` + fixtureTRAccess + `","token_type":"` + fixtureTRType +
		`","refresh_token":"` + fixtureTRRefresh + `","expires_in":900}`
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.AccessToken != fixtureTRAccess || decoded.RefreshToken != fixtureTRRefresh {
		t.Errorf("decoded = %v, want both tokens to still arrive from the wire", decoded)
	}
	if decoded.ExpiresIn != fixtureTRExpires {
		t.Errorf("decoded.ExpiresIn = %d, want %d", decoded.ExpiresIn, fixtureTRExpires)
	}
}

// renderTokenVerb applies a format verb through a non-constant format string.
//
// Do not inline this back into fmt.Sprintf("%s", tr). The %s and %q verbs on a
// struct are a go vet printf error unless the type implements Stringer — so
// with the verb written as a constant, the mutation that validates this file
// (delete String, or flip its receiver to a pointer) stops the package
// BUILDING instead of failing a named test, and none of the other shapes in
// the table ever run. The signature is deliberately non-variadic so vet does
// not infer this as a printf wrapper and check the call sites instead.
func renderTokenVerb(verb string, v any) string { return fmt.Sprintf(verb, v) }

// trHolder stands in for the context struct a failing VBR login would be
// logged with. Nothing in the tree holds a tokenResponse this way today; the
// shape exists because it is the one the JSON handler marshals rather than
// formats, and therefore the one that needs MarshalJSON.
type trHolder struct {
	URL  string        `json:"url"`
	Body tokenResponse `json:"body"`
}

func mustMarshalTR(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(raw)
}

// tokenLogLine renders attr through a real slog pipeline, JSON handler when asJSON. Both run
// because they differ for a value that is not a LogValuer: Text dispatches String, JSON
// (production's) dispatches MarshalJSON. A Text-only version once passed while a wrapper struct
// leaked a live token through the JSON path.
func tokenLogLine(t *testing.T, asJSON bool, attr slog.Attr) string {
	t.Helper()
	var buf bytes.Buffer
	var h slog.Handler
	if asJSON {
		h = slog.NewJSONHandler(&buf, nil)
	} else {
		h = slog.NewTextHandler(&buf, nil)
	}
	slog.New(h).Info("veeam requestToken", attr)
	return buf.String()
}
