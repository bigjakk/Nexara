package auth

import "testing"

// TestEmailUnverified: only a claim that says false refuses the sign-in, the string
// some providers send included; a missing claim does not.
func TestEmailUnverified(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		claims map[string]interface{}
		want   bool
	}{
		{"missing", map[string]interface{}{}, false},
		{"true", map[string]interface{}{"email_verified": true}, false},
		{"false", map[string]interface{}{"email_verified": false}, true},
		{"the string false", map[string]interface{}{"email_verified": "False"}, true},
		{"the string true", map[string]interface{}{"email_verified": "true"}, false},
	} {
		if got := emailUnverified(tc.claims); got != tc.want {
			t.Errorf("%s: emailUnverified = %v, want %v", tc.name, got, tc.want)
		}
	}
}
