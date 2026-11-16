package proxmox

import "testing"

// TestValidateFullTokenID_SplitsAtTheLastBang: a user name may hold "!" and a token
// name may not, so the token part is after the LAST "!", as Proxmox and the cluster
// sharing count (queries/clusters.sql) both read it.
func TestValidateFullTokenID_SplitsAtTheLastBang(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		full string
		ok   bool
	}{
		{"nexara@pve!ci", true},
		{"ops!lab@pve!ci", true},
		{"nexara@pve", false},
	} {
		if err := validateFullTokenID(tc.full); (err == nil) != tc.ok {
			t.Errorf("validateFullTokenID(%q) = %v, want ok %v", tc.full, err, tc.ok)
		}
	}
}
