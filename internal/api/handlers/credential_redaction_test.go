package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// Fixtures. Every value is synthetic; nexara@pve!api is the documented house
// token id.
//
// The secrets are deliberately single-line and free of any character Go
// escapes when quoting a string. A multi-line fixture would be printed by %#v
// with a literal backslash-n, so a strings.Contains for the raw value could
// not see the leak and every absence assertion here would pass against a
// completely broken GoString. The fixture shape is load-bearing.
const (
	fixtureCredTokenID   = "nexara@pve!api"
	fixtureCredSecret    = "cs-11111111-2222-3333-4444-555555555555"
	fixtureAccessToken   = "at-66666666-7777-8888-9999-aaaaaaaaaaaa"
	fixtureRefreshToken  = "rt-bbbbbbbb-cccc-dddd-eeee-ffffffffffff"
	fixtureBootstrapPass = "bp-12121212-3434-5656-7878-909090909090"
	fixtureBootstrapOTP  = "246813"
)

func fixtureClusterCredential() clusterCredential {
	return clusterCredential{
		TokenID:     fixtureCredTokenID,
		Secret:      fixtureCredSecret,
		Source:      credentialSourceBootstrap,
		UserID:      "nexara@pve",
		TokenName:   "api",
		CreatedUser: true,
		CreatedACL:  true,
		MintedAt:    time.Unix(1767225600, 0).UTC(),
		Steps:       []proxmox.MintStep{{Step: "token", Status: "created"}},
	}
}

// credHolder stands in for a struct that holds a clusterCredential as a field.
// Nothing in the tree does today, which is the point: the redaction has to
// already work for the first thing that does.
type credHolder struct {
	Cluster string            `json:"cluster"`
	Cred    clusterCredential `json:"cred"`
}

// clusterCredential.Secret is the minted (or operator-supplied) Proxmox API
// token in plaintext, alive between resolution and the encrypted column. It
// sat in this file next to bootstrapRequest, which has been redacted since it
// was written, with no protection of its own — closing that asymmetry is what
// this guards.
//
// Each rendering is served by a different method — String, GoString, LogValue,
// MarshalJSON — so removing any one of them fails this test on its own shapes
// and no others.
//
// Every case also asserts a NON-credential value SURVIVES. Without that half a
// String() returning "" or a MarshalJSON() returning {} would satisfy every
// absence assertion while destroying the diagnostics the redaction exists to
// preserve.
//
// The slog case asserts the GROUPED key (cred.token_id=...), not merely the
// token id. clusterCredential implements Stringer, so with LogValue deleted
// slog falls through to String and still does not leak — an absence-only slog
// assertion would be masked by String, and LogValue could never be killed on
// its own.
func TestGuard_ClusterCredentialNeverPrintsItsSecret(t *testing.T) {
	cred := fixtureClusterCredential()
	// Every real call site holds a *clusterCredential, so the pointer shapes
	// below are the live ones and the value shapes are the latent ones. Value
	// receivers are what make both work.
	ptr := &cred
	holder := credHolder{Cluster: "cluster01", Cred: cred}

	mustMarshal := func(v any) string {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		return string(raw)
	}

	cases := []struct {
		name     string
		rendered string
		survives string
	}{
		{"fmt %v", fmt.Sprintf("%v", cred), fixtureCredTokenID},
		{"fmt %v pointer", fmt.Sprintf("%v", ptr), fixtureCredTokenID},
		{"fmt %s", renderCredVerb("%s", cred), fixtureCredTokenID},
		{"fmt %+v", fmt.Sprintf("%+v", cred), fixtureCredTokenID},
		{"fmt %q", renderCredVerb("%q", cred), fixtureCredTokenID},
		{"fmt.Sprint", fmt.Sprint(cred), fixtureCredTokenID},
		// %#v dispatches GoStringer, not Stringer.
		{"fmt %#v", fmt.Sprintf("%#v", cred), fixtureCredTokenID},
		{"fmt %#v pointer", fmt.Sprintf("%#v", ptr), fixtureCredTokenID},
		{"fmt %#v holder", fmt.Sprintf("%#v", holder), fixtureCredTokenID},
		{"fmt %+v holder", fmt.Sprintf("%+v", holder), fixtureCredTokenID},
		// An error is the route that reaches a persisted, Viewer-readable
		// audit column.
		{"error wrapping", fmt.Errorf("cluster create: %v", cred).Error(), fixtureCredTokenID},
		{"json.Marshal value", mustMarshal(cred), `"token_id":"` + fixtureCredTokenID + `"`},
		{"json.Marshal pointer", mustMarshal(ptr), `"token_id":"` + fixtureCredTokenID + `"`},
		{"json.Marshal holder", mustMarshal(holder), `"token_id":"` + fixtureCredTokenID + `"`},
		{"json.Marshal slice", mustMarshal([]clusterCredential{cred}), `"token_id":"` + fixtureCredTokenID + `"`},
		{"json.Marshal map", mustMarshal(map[string]clusterCredential{"c": cred}), `"token_id":"` + fixtureCredTokenID + `"`},
		{"slog text Any", credLogLine(t, false, slog.Any("cred", cred)), "cred.token_id=" + fixtureCredTokenID},
		{"slog text Any pointer", credLogLine(t, false, slog.Any("cred", ptr)), "cred.token_id=" + fixtureCredTokenID},
		{"slog json Any", credLogLine(t, true, slog.Any("cred", cred)), `"token_id":"` + fixtureCredTokenID + `"`},
		{"slog json Any pointer", credLogLine(t, true, slog.Any("cred", ptr)), `"token_id":"` + fixtureCredTokenID + `"`},
		// The wrapper under the JSON handler goes through MarshalJSON rather
		// than String. It is the shape that catches a missing MarshalJSON.
		{"slog text Any holder", credLogLine(t, false, slog.Any("cred", holder)), fixtureCredTokenID},
		{"slog json Any holder", credLogLine(t, true, slog.Any("cred", holder)), `"token_id":"` + fixtureCredTokenID + `"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.rendered, fixtureCredSecret) {
				t.Errorf("the token secret is in the rendering: %s", tc.rendered)
			}
			if !strings.Contains(tc.rendered, tc.survives) {
				t.Errorf("the rendering dropped %q and is no longer worth emitting: %s",
					tc.survives, tc.rendered)
			}
		})
	}
}

// The manual path (clusters.go) builds a clusterCredential with TokenID,
// Secret and Source only, so MintedAt is the zero time. `omitempty` does
// nothing on a time.Time — encoding/json never treats a struct as empty — so
// the obvious spelling of MarshalJSON emits "0001-01-01T00:00:00Z" and reads
// as a real mint date. mintedAtColumn already exists to stop exactly that
// confusion reaching the DB; this stops it reaching a JSON body.
func TestClusterCredentialManualPathEmitsNoMintDate(t *testing.T) {
	manual := clusterCredential{
		TokenID: fixtureCredTokenID,
		Secret:  fixtureCredSecret,
		Source:  credentialSourceManual,
	}
	raw, err := json.Marshal(manual)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out := string(raw)

	// Non-vacuity: prove a credential really was serialised before concluding
	// anything from the absence of a key.
	if !strings.Contains(out, `"token_id":"`+fixtureCredTokenID+`"`) {
		t.Fatalf("marshalled to %s — no token_id, so nothing was exercised", out)
	}
	if strings.Contains(out, "minted_at") {
		t.Errorf("%s — a manually supplied credential was never minted; the key must be "+
			"omitted, not emitted as a zero time", out)
	}
	if strings.Contains(out, "0001-01-01") {
		t.Errorf("%s — the zero time reached the body and reads as a real date", out)
	}

	// The minted path must still carry it, or the omission above would be
	// indistinguishable from dropping the field altogether.
	raw, err = json.Marshal(fixtureClusterCredential())
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"minted_at":"`) {
		t.Errorf("%s — a minted credential must still report when it was minted", raw)
	}
}

// logoutRequest is the one credential holder in this file with no
// non-credential field, so it cannot use the "a non-credential value
// survives" pairing the tables above rely on. The substitute is the marker:
// REDACTED must be PRESENT. That kills a String() returning "" and a String()
// that prints the field, while surviving any rewording — which exact equality
// against a literal would not.
//
// The slog case asserts the GROUPED key. logoutRequest implements Stringer,
// so with LogValue deleted slog falls through to String, still emits REDACTED
// and still does not leak; only the grouped key can kill LogValue on its own.
func TestGuard_LogoutRequestNeverPrintsTheRefreshToken(t *testing.T) {
	const secret = "lr-dddddddd-eeee-ffff-0000-111111111111"
	req := logoutRequest{RefreshToken: secret}

	for _, tc := range []struct {
		name     string
		rendered string
		survives string
	}{
		{"fmt %v", fmt.Sprintf("%v", req), "REDACTED"},
		{"fmt %s", renderCredVerb("%s", req), "REDACTED"},
		{"fmt %+v", fmt.Sprintf("%+v", req), "REDACTED"},
		{"fmt %q", renderCredVerb("%q", req), "REDACTED"},
		{"fmt.Sprint", fmt.Sprint(req), "REDACTED"},
		{"fmt %#v", fmt.Sprintf("%#v", req), "REDACTED"},
		{"fmt %#v pointer", fmt.Sprintf("%#v", &req), "REDACTED"},
		{"error wrapping", fmt.Errorf("logout: %v", req).Error(), "REDACTED"},
		{"slog text Any", credLogLine(t, false, slog.Any("req", req)), "req.refresh_token=REDACTED"},
		{"slog json Any", credLogLine(t, true, slog.Any("req", req)), `"refresh_token":"REDACTED"`},
		// The shape the text handler cannot see. Under the JSON handler the
		// wrapper is MARSHALLED, so it reaches MarshalJSON and not String —
		// and before MarshalJSON existed it wrote the live token to stdout.
		{"slog text Any holder", credLogLine(t, false, slog.Any("req", logoutHolder{"192.0.2.10", req})), "REDACTED"},
		{"slog json Any holder", credLogLine(t, true, slog.Any("req", logoutHolder{"192.0.2.10", req})), `"refresh_token":"REDACTED"`},
		{"json.Marshal value", mustMarshalCred(t, req), `"refresh_token":"REDACTED"`},
		{"json.Marshal holder", mustMarshalCred(t, logoutHolder{"192.0.2.10", req}), `"refresh_token":"REDACTED"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.rendered, secret) {
				t.Errorf("the refresh token is in the rendering: %s", tc.rendered)
			}
			if !strings.Contains(tc.rendered, tc.survives) {
				t.Errorf("the rendering dropped %q, so its absence of the token proves nothing: %s",
					tc.survives, tc.rendered)
			}
		})
	}
}

// bootstrapRequest has had String, LogValue and MarshalJSON since it was
// hardened, but not GoString — and fmt dispatches GoStringer, not Stringer,
// for %#v. Until that method existed, `%#v` of the request printed the struct
// literal with the operator's password and OTP in it, for the value, for a
// pointer, and for anything holding one.
//
// This lives here rather than in TestBootstrapRequestNeverSerialisesTheCredential
// so that test stays as it was released; the gap it did not cover is covered
// here.
func TestGuard_BootstrapRequestGoStringRedactsThePassword(t *testing.T) {
	req := bootstrapRequest{
		Username: "root@pam", Password: fixtureBootstrapPass, OTP: fixtureBootstrapOTP,
		UserID: "nexara@pve", TokenName: "api",
	}

	for _, tc := range []struct {
		name     string
		rendered string
	}{
		{"fmt %#v", fmt.Sprintf("%#v", req)},
		{"fmt %#v pointer", fmt.Sprintf("%#v", &req)},
		{"fmt %#v field", fmt.Sprintf("%#v", struct{ Req bootstrapRequest }{req})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.rendered, fixtureBootstrapPass) {
				t.Errorf("the bootstrap password is in the rendering: %s", tc.rendered)
			}
			if strings.Contains(tc.rendered, fixtureBootstrapOTP) {
				t.Errorf("the OTP is in the rendering: %s", tc.rendered)
			}
			if !strings.Contains(tc.rendered, "root@pam") {
				t.Errorf("the rendering dropped the username and is no longer worth emitting: %s",
					tc.rendered)
			}
		})
	}
}

// The redactors must be in the method set of the VALUE, not just the pointer.
// fmt and encoding/json skip a pointer-receiver method on a value they cannot
// address, and every one of these four types is rendered or marshalled as a
// value somewhere: clusterCredential and bootstrapRequest in the tables above,
// authResponse at its three response sites (two `c.JSON(authResponse{...})`
// and one `c.Status(...).JSON(...)` in Register — same encoder, different
// spelling), and logoutRequest wherever the decoded body reaches a log line.
//
// It is what makes deleting any
// redactor fail a NAMED test rather than break compilation: nothing above
// calls cred.String() or req.GoString() directly, so the package still builds
// with one removed and reports which one went missing.
func TestCredentialRedactorsAreInTheValueMethodSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    any
		// wantMarshaler is true for all four. An earlier version of this
		// test set it false for logoutRequest on the grounds that nothing
		// marshals one — which was wrong, because slog's JSON handler
		// marshals any non-LogValuer it is handed, including a wrapper that
		// holds one. The field is kept rather than deleted so the next type
		// added here has to answer the question deliberately.
		wantMarshaler bool
	}{
		{"clusterCredential", fixtureClusterCredential(), true},
		{"bootstrapRequest", bootstrapRequest{Username: "root@pam"}, true},
		{"authResponse", authResponse{}, true},
		{"logoutRequest", logoutRequest{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := tc.v.(fmt.Stringer); !ok {
				t.Errorf("%s (as a value) does not implement fmt.Stringer — the v, s and q "+
					"verbs, fmt.Sprint and error wrapping all print the raw fields", tc.name)
			}
			if _, ok := tc.v.(fmt.GoStringer); !ok {
				t.Errorf("%s (as a value) does not implement fmt.GoStringer — the sharp-v verb "+
					"prints the struct literal with the credential in it", tc.name)
			}
			if _, ok := tc.v.(slog.LogValuer); !ok {
				t.Errorf("%s (as a value) does not implement slog.LogValuer — structured logs "+
					"fall through to String instead of emitting fields", tc.name)
			}
			_, isMarshaler := tc.v.(json.Marshaler)
			if tc.wantMarshaler && !isMarshaler {
				t.Errorf("%s (as a value) does not implement json.Marshaler — encoding/json "+
					"emits every exported field, credential included", tc.name)
			}
			// No type here is exempt today. The branch stays so that a
			// future decode-only type can be added with wantMarshaler:false
			// and have that choice enforced rather than assumed.
			if !tc.wantMarshaler && isMarshaler {
				t.Errorf("%s implements json.Marshaler but is declared to have no emission "+
					"route — reconcile the declaration with the method", tc.name)
			}
		})
	}
}

// authHolder stands in for a context struct holding an auth response.
// Nothing in the tree logs one; the shape is here to pin which handler is
// covered and which is not.
type authHolder struct {
	Stage string       `json:"stage"`
	Resp  authResponse `json:"resp"`
}

// logoutHolder stands in for the context struct a failing logout would be
// logged with. Nothing in the tree holds a logoutRequest this way today; the
// shape exists because it is the one slog's JSON handler marshals rather than
// formats, and therefore the one that needs MarshalJSON.
type logoutHolder struct {
	IP  string        `json:"ip"`
	Req logoutRequest `json:"req"`
}

func mustMarshalCred(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return string(raw)
}

// renderCredVerb applies a format verb through a non-constant format string.
//
// Do not inline this back into fmt.Sprintf("%s", cred). The %s and %q verbs on
// a struct are a go vet printf error unless the type implements Stringer — so
// with the verb written as a constant, the mutation that validates this file
// (delete String, or flip its receiver to a pointer) stops the package
// BUILDING instead of failing a named test, and none of the other shapes ever
// run. The signature is deliberately non-variadic so vet does not infer this
// as a printf wrapper and check the call sites instead.
func renderCredVerb(verb string, v any) string { return fmt.Sprintf(verb, v) }

// credLogLine renders through a real slog pipeline and returns the line,
// using the JSON handler when asJSON is set.
//
// Both handlers are exercised on purpose, and it is not symmetry for its own
// sake. For a value that is not itself a LogValuer the two take DIFFERENT
// routes: TextHandler formats it with %+v, which dispatches String, while
// JSONHandler MARSHALS it, which dispatches MarshalJSON. Production builds
// its logger with slog.NewJSONHandler (cmd/nexara/main.go:64 and :272), so a
// Text-only probe does not resemble production — and that is exactly how an
// earlier version of this file passed while the JSON path leaked a live
// token through a wrapper struct. The wrapper cases are the ones that found
// it: slog.Any on the type ITSELF is safe under both handlers, because
// LogValue resolves before either route is taken.
func credLogLine(t *testing.T, asJSON bool, attr slog.Attr) string {
	t.Helper()
	var buf bytes.Buffer
	var h slog.Handler
	if asJSON {
		h = slog.NewJSONHandler(&buf, nil)
	} else {
		h = slog.NewTextHandler(&buf, nil)
	}
	slog.New(h).Info("cluster onboarding", attr)
	return buf.String()
}

func fixtureAuthResponse() authResponse {
	return authResponse{
		User: authUserResponse{
			ID:          uuid.MustParse("00000000-0000-4000-8000-000000000001"),
			Email:       "admin@example.com",
			DisplayName: "Admin",
			Role:        "admin",
		},
		AccessToken: fixtureAccessToken,
		// Deliberately NOT empty. The three construction sites pass "", so a
		// fixture that also passed "" would prove only that "" marshals as "".
		// The property under test is that the TYPE blanks whatever it is
		// given, which is what a fourth construction site will rely on.
		RefreshToken: fixtureRefreshToken,
		ExpiresAt:    1767225600,
		Permissions:  []string{"view:cluster", "view:vm"},
	}
}

// fixtureAuthResponseJSON is the exact body the three auth endpoints must
// emit, written out by hand rather than built from the struct so the
// assertion cannot agree with a broken implementation.
//
// Byte identity, not a field check: refresh_token must still be PRESENT, still
// be the empty string, and still sit between access_token and expires_at.
// frontend/src/types/api.ts declares refresh_token non-optional and
// docs/api-reference.md documents it as present-and-empty, so dropping the key
// or reordering the object is a contract break, not a tidy-up. access_token
// must survive in full — it is the thing the client came for, and blanking it
// alongside the refresh token is the plausible over-correction this pins shut.
const fixtureAuthResponseJSON = `{"user":{"id":"00000000-0000-4000-8000-000000000001",` +
	`"email":"admin@example.com","display_name":"Admin","role":"admin"},` +
	`"access_token":"at-66666666-7777-8888-9999-aaaaaaaaaaaa","refresh_token":"",` +
	`"expires_at":1767225600,"permissions":["view:cluster","view:vm"]}`

// authResponse.MarshalJSON turns three call-site invariants into one type
// invariant: the v1.9.x refresh-token-in-body leak can no longer be reopened
// by a fourth construction site forgetting `RefreshToken: ""`.
//
// The shapes are not a catalogue. `value` is literally what Register, Login
// and Refresh do — c.JSON(authResponse{...}) hands an unaddressable value to
// encoding/json — and it is the one that breaks if the receiver is ever
// changed to a pointer. `slice` is included with a warning attached: slice
// elements ARE addressable, so encoding/json will still dispatch a
// POINTER-receiver MarshalJSON for it. It therefore does not discriminate on
// receiver kind, and deleting the value/map/wrapper cases because "slice
// covers it" would quietly remove the only cases that do.
func TestGuard_AuthResponseAlwaysBlanksTheRefreshToken(t *testing.T) {
	resp := fixtureAuthResponse()

	t.Run("golden body", func(t *testing.T) {
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(raw) != fixtureAuthResponseJSON {
			t.Errorf("body =\n  %s\nwant\n  %s", raw, fixtureAuthResponseJSON)
		}
	})

	// The same bytes through the real Fiber path. fiber.New() leaves
	// Config.JSONEncoder nil and Fiber fills it with encoding/json's
	// json.Marshal, so c.JSON honours MarshalJSON exactly as above — this
	// proves the production route rather than asserting it in a comment.
	t.Run("through c.JSON", func(t *testing.T) {
		app := fiber.New()
		app.Get("/x", func(c fiber.Ctx) error { return c.JSON(fixtureAuthResponse()) })

		httpResp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
		if err != nil {
			t.Fatalf("Test: %v", err)
		}
		defer func() { _ = httpResp.Body.Close() }()
		body, err := io.ReadAll(httpResp.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		if string(body) != fixtureAuthResponseJSON {
			t.Errorf("c.JSON body =\n  %s\nwant\n  %s", body, fixtureAuthResponseJSON)
		}
	})

	type envelope struct {
		Data authResponse `json:"data"`
	}
	shapes := map[string]any{
		"value":   resp,
		"pointer": &resp,
		"slice":   []authResponse{resp},
		"map":     map[string]authResponse{"auth": resp},
		"wrapper": envelope{Data: resp},
	}
	for name, v := range shapes {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("Marshal(%s): %v", name, err)
			}
			out := string(raw)

			// Non-vacuity: prove an authResponse was really serialised before
			// concluding anything from the refresh token's absence. An empty
			// object, or a type that stopped being an authResponse, would
			// satisfy every assertion below without exercising anything.
			if !strings.Contains(out, `"access_token":"`+fixtureAccessToken+`"`) {
				t.Fatalf("%s: marshalled to %s — the access token is missing, so nothing was "+
					"exercised and the assertions below would pass vacuously", name, out)
			}
			if !strings.Contains(out, `"refresh_token":""`) {
				t.Errorf("%s: %s — refresh_token must remain present and empty; the key is a "+
					"published part of the response shape", name, out)
			}
			if strings.Contains(out, fixtureRefreshToken) {
				t.Errorf("%s: the refresh token leaked into the body: %s", name, out)
			}
		})
	}
}

// authResponse also holds a live bearer JWT in AccessToken for its whole TTL.
// MarshalJSON must keep it — that is the wire contract — but String, GoString
// and LogValue must not: a %v of the response in an error, or a slog.Any on
// the way out of a login handler, hands out a working session.
//
// This is the asymmetry the type deliberately carries, so it is asserted
// explicitly rather than left to be re-derived.
func TestGuard_AuthResponseNeverPrintsEitherToken(t *testing.T) {
	resp := fixtureAuthResponse()

	cases := []struct {
		name     string
		rendered string
		survives string
	}{
		{"fmt %v", fmt.Sprintf("%v", resp), "admin@example.com"},
		{"fmt %s", renderCredVerb("%s", resp), "admin@example.com"},
		{"fmt %+v", fmt.Sprintf("%+v", resp), "admin@example.com"},
		{"fmt %q", renderCredVerb("%q", resp), "admin@example.com"},
		{"fmt.Sprint", fmt.Sprint(resp), "admin@example.com"},
		{"fmt %#v", fmt.Sprintf("%#v", resp), "admin@example.com"},
		{"fmt %#v pointer", fmt.Sprintf("%#v", &resp), "admin@example.com"},
		{"error wrapping", fmt.Errorf("login: %v", resp).Error(), "admin@example.com"},
		{"slog text Any", credLogLine(t, false, slog.Any("resp", resp)), "resp.email=admin@example.com"},
		{"slog json Any", credLogLine(t, true, slog.Any("resp", resp)), `"email":"admin@example.com"`},
		// A wrapper under the TEXT handler: %+v of the outer struct reaches
		// String on the inner response. The JSON handler is deliberately NOT
		// asserted here — it marshals the wrapper, which reaches MarshalJSON,
		// which keeps AccessToken by design. See the note on the method.
		{"slog text Any holder", credLogLine(t, false, slog.Any("resp", authHolder{"login", resp})), "admin@example.com"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Contains(tc.rendered, fixtureAccessToken) {
				t.Errorf("the access token is in the rendering: %s", tc.rendered)
			}
			if strings.Contains(tc.rendered, fixtureRefreshToken) {
				t.Errorf("the refresh token is in the rendering: %s", tc.rendered)
			}
			if !strings.Contains(tc.rendered, tc.survives) {
				t.Errorf("the rendering dropped %q and is no longer worth emitting: %s",
					tc.survives, tc.rendered)
			}
		})
	}
}
