package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

func TestParseParamUUID(t *testing.T) {
	const canonical = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	got, err := parseParamUUID(canonical)
	if err != nil {
		t.Fatalf("parseParamUUID(%q) = %v", canonical, err)
	}
	if got != uuid.MustParse(canonical) {
		t.Errorf("uuid = %v, want %v", got, canonical)
	}

	// Unreachable through a schema that declares Format: "uuid" — which is
	// exactly why the branch exists. It fires when a declaration DROPS the
	// format, and the answer must blame us rather than the caller: a 400
	// here would tell whoever sent a perfectly good request that their
	// input was wrong.
	_, err = parseParamUUID("not-a-uuid")
	if err == nil {
		t.Fatal("a malformed uuid was accepted")
	}
	var fe *fiber.Error
	if !errors.As(err, &fe) {
		t.Fatalf("error %v is not a *fiber.Error", err)
	}
	if fe.Code != fiber.StatusInternalServerError {
		t.Errorf("status = %d, want 500 — a format the schema should have enforced is our bug, not the caller's", fe.Code)
	}
}

func TestOptPointerHelpers(t *testing.T) {
	if got := optBoolPtr(false, false); got != nil {
		t.Errorf("optBoolPtr(false, false) = %v, want nil — an unsupplied flag must not be sent as false", *got)
	}
	if got := optBoolPtr(false, true); got == nil || *got {
		t.Errorf("optBoolPtr(false, true) = %v, want a pointer to false", got)
	}
	if got := optBoolPtr(true, true); got == nil || !*got {
		t.Errorf("optBoolPtr(true, true) = %v, want a pointer to true", got)
	}

	if got := optIntPtr(0, false); got != nil {
		t.Errorf("optIntPtr(0, false) = %v, want nil", *got)
	}
	// 0 is meaningful for balloon — it disables the device — so "the
	// caller sent 0" must survive as a pointer to 0, not collapse to nil.
	if got := optIntPtr(0, true); got == nil || *got != 0 {
		t.Errorf("optIntPtr(0, true) = %v, want a pointer to 0", got)
	}
	if got := optIntPtr(2048, true); got == nil || *got != 2048 {
		t.Errorf("optIntPtr(2048, true) = %v, want a pointer to 2048", got)
	}
}

func TestStringMap(t *testing.T) {
	// UseNumber is how registry_params.go decodes a body, so whole numbers
	// arrive as json.Number and must stay integral rather than becoming
	// "2e+00" in a Proxmox config value.
	var decoded map[string]any
	dec := json.NewDecoder(strings.NewReader(`{"cores":2,"memory":4096,"onboot":true,"name":"linux11","cpulimit":1.5}`))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got, err := stringMap("fields", decoded)
	if err != nil {
		t.Fatalf("stringMap: %v", err)
	}
	want := map[string]string{
		"cores":    "2",
		"memory":   "4096",
		"onboot":   "true",
		"name":     "linux11",
		"cpulimit": "1.5",
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Errorf("%s = %q, want %q", key, got[key], wantValue)
		}
	}

	if _, err := stringMap("extra", nil); err != nil {
		t.Errorf("a nil object should flatten to an empty map, got %v", err)
	}

	for _, bad := range []map[string]any{
		{"net0": map[string]any{"bridge": "vmbr0"}},
		{"tags": []any{"a", "b"}},
	} {
		_, err := stringMap("fields", bad)
		if err == nil {
			t.Fatalf("a nested value was accepted: %v", bad)
		}
		var fe *fiber.Error
		if !errors.As(err, &fe) || fe.Code != fiber.StatusBadRequest {
			t.Errorf("error for %v = %v, want a 400", bad, err)
		}
	}
}

func TestScalarText(t *testing.T) {
	tests := []struct {
		raw  any
		want string
		ok   bool
	}{
		{"plain", "plain", true},
		{json.Number("42"), "42", true},
		{true, "true", true},
		{false, "false", true},
		{1.5, "1.5", true},
		{2.0, "2", true},
		{nil, "", true},
		{map[string]any{}, "", false},
		{[]any{}, "", false},
	}
	for _, tt := range tests {
		got, ok := scalarText(tt.raw)
		if ok != tt.ok || got != tt.want {
			t.Errorf("scalarText(%#v) = (%q, %v), want (%q, %v)", tt.raw, got, ok, tt.want, tt.ok)
		}
	}
}

// withRequestParams adapts a registry-shaped handler to a fiber.Handler for a
// test that drives a real REQUEST BODY, which list_scope_test.go's withParams
// cannot: that one validates an EMPTY request so every parameter arrives
// carrying its default, which is right for a handler whose test never reaches
// the values.
//
// The handlers this serves branch ON the body — the LDAP transport gate reads
// start_tls and server_url, the console-token gate picks its resource from
// `type` — so the body has to be decoded and validated the way
// api.Endpoint.extract does at runtime. This is a deliberately minimal stand-in
// for that: a JSON object, plus the route's :param segments. Query parameters
// are not read, because no handler tested through this takes one.
//
// props is a MIRROR of the route's declaration rather than the declaration
// itself, for the reason migrationListMirror gives: package api imports this
// package, not the other way round. It is compiled here so a mirror that is
// itself malformed fails loudly rather than silently accepting anything.
func withRequestParams(t *testing.T, props apischema.Properties, pathParams []string,
	h func(fiber.Ctx, *apischema.Params) error) fiber.Handler {
	t.Helper()
	if err := props.Compile(); err != nil {
		t.Fatalf("the mirror schema is itself invalid: %v", err)
	}
	return func(c fiber.Ctx) error {
		raw := map[string]any{}
		if body := c.Body(); len(body) > 0 {
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.UseNumber()
			if err := dec.Decode(&raw); err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "request body is not valid JSON")
			}
		}
		for _, name := range pathParams {
			if v := c.Params(name); v != "" {
				raw[name] = v
			}
		}
		params, err := props.Validate(raw)
		if err != nil {
			var verr *apischema.ValidationError
			if errors.As(err, &verr) {
				return fiber.NewError(fiber.StatusBadRequest, verr.Error())
			}
			return fiber.NewError(fiber.StatusInternalServerError, "Request validation failed")
		}
		return h(c, params)
	}
}
