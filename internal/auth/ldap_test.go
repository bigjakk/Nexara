package auth

import "testing"

// TestLDAPTLSConfig_NamesTheConfiguredHost: StartTLS verifies the directory's certificate
// against the host of the server URL, so the TLS config must carry it.
func TestLDAPTLSConfig_NamesTheConfiguredHost(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"ldap://dc01.example.com:389": "dc01.example.com",
		"ldaps://dc01.example.com":    "dc01.example.com",
		"ldap://[2001:db8::10]:389":   "2001:db8::10",
	} {
		if got := NewLDAPClient(LDAPConfig{ServerURL: url, StartTLS: true}).tlsConfig().ServerName; got != want {
			t.Errorf("%s: ServerName %q, want %q", url, got, want)
		}
	}
}
