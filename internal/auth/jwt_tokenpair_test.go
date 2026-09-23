package auth

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestTokenPair_RefreshTokenIsNeverMarshalled is the guard for the half of the
// v1.9.x refresh-token-in-body fix that does not live in a call site.
//
// The other half is authResponse.MarshalJSON in internal/api/handlers/auth.go,
// which blanks the field on every marshal of that type. It cannot stop a
// DIFFERENT type from being marshalled — an auth.TokenPair reached by a new
// endpoint, a debug handler, an error path, a wrapper struct that embeds it —
// and with a live `json:"refresh_token"` tag that would put the token back in
// a response body with nothing failing. The tag is the latent path; this test
// is what holds it shut.
//
// The shapes below are the ones a careless caller actually reaches for, not a
// catalogue for its own sake: the value and the pointer are `return
// c.JSON(pair)`, the slice and the map are a listing endpoint, the wrapper is
// an envelope type, and the Encoder is a hand-rolled http.ResponseWriter path.
// Struct tags are resolved per type, not per call site, so one of them failing
// means all of them would — which is precisely why closing this in the type
// works and closing it in call sites does not.
func TestTokenPair_RefreshTokenIsNeverMarshalled(t *testing.T) {
	// Synthetic, and deliberately unmistakable: if it ever appears in output
	// the failure message should be unambiguous about what leaked.
	const secret = "refresh-token-that-must-never-be-serialised"

	pair := TokenPair{
		AccessToken:  "access-token-placeholder",
		RefreshToken: secret,
		ExpiresAt:    1234567890,
	}

	type envelope struct {
		Data TokenPair `json:"data"`
	}

	shapes := map[string]any{
		"value":   pair,
		"pointer": &pair,
		"slice":   []TokenPair{pair},
		"map":     map[string]TokenPair{"tokens": pair},
		"wrapper": envelope{Data: pair},
	}

	for name, v := range shapes {
		t.Run(name, func(t *testing.T) {
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatalf("json.Marshal(%s): %v", name, err)
			}
			out := string(b)

			// Non-vacuity: a marshal that produced nothing recognisable — an
			// empty object, an error swallowed upstream, a type that stopped
			// being a TokenPair — would satisfy every assertion below without
			// the type ever having been exercised. This is the shape of guard
			// this repo has been bitten by, so prove the pair was really
			// serialised before concluding anything from its absence.
			if !strings.Contains(out, "access_token") {
				t.Fatalf("%s: marshalled to %s — no access_token, so this never serialised a "+
					"TokenPair and the assertions below would pass vacuously", name, out)
			}

			if strings.Contains(out, secret) {
				t.Errorf("%s: refresh token leaked into JSON: %s", name, out)
			}
			if strings.Contains(out, "refresh_token") {
				t.Errorf("%s: JSON carries a refresh_token key: %s — the field must stay json:\"-\"; "+
					"the refresh token is delivered only as an HttpOnly cookie", name, out)
			}
		})
	}

	// json.Encoder is the same tag resolution by a different door; a caller
	// writing straight to an http.ResponseWriter never calls Marshal.
	t.Run("encoder", func(t *testing.T) {
		var buf bytes.Buffer
		if err := json.NewEncoder(&buf).Encode(pair); err != nil {
			t.Fatalf("Encode: %v", err)
		}
		out := buf.String()
		if !strings.Contains(out, "access_token") {
			t.Fatalf("encoded to %s — no access_token, so nothing was exercised", out)
		}
		if strings.Contains(out, secret) || strings.Contains(out, "refresh_token") {
			t.Errorf("refresh token leaked through json.Encoder: %s", out)
		}
	})
}
