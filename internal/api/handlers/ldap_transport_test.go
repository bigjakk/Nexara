package handlers

import (
	"strings"
	"testing"
)

func TestLDAPTransportProtected(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		startTLS  bool
		want      bool
	}{
		{name: "ldaps is implicit TLS", serverURL: "ldaps://dc.example.com", want: true},
		{name: "ldaps stays protected without StartTLS", serverURL: "ldaps://dc.example.com:636", want: true},
		{name: "ldap with StartTLS upgrades", serverURL: "ldap://dc.example.com", startTLS: true, want: true},
		{name: "ldap without StartTLS is cleartext", serverURL: "ldap://dc.example.com", want: false},
		{
			// wasProtected is computed from a STORED server_url, so this must
			// not depend on validateLDAPServerURL's case-sensitivity having
			// always been there. A row reading as cleartext when it is really
			// ldaps would make a genuine downgrade look like no change.
			name:      "uppercase LDAPS is still implicit TLS",
			serverURL: "LDAPS://dc.example.com",
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ldapTransportProtected(tt.serverURL, tt.startTLS); got != tt.want {
				t.Errorf("ldapTransportProtected(%q, %v) = %v, want %v",
					tt.serverURL, tt.startTLS, got, tt.want)
			}
		})
	}
}

// LDAP binds AS THE USER, so every interactive login puts that person's
// password on this connection — which is why the gate does not depend on a
// stored bind password existing. These are the transitions that must prompt.
func TestLDAPTransportWeakened(t *testing.T) {
	tests := []struct {
		name              string
		wasProtected      bool
		nowProtected      bool
		wasSkippingVerify bool
		nowSkippingVerify bool
		wantEncryption    bool
		wantVerification  bool
	}{
		{
			name:         "no change, still protected",
			wasProtected: true, nowProtected: true,
		},
		{
			// The accident the gate exists for: start_tls is a non-pointer
			// bool on a full-body PUT, so a client that OMITS it sends false.
			name:         "StartTLS turned off",
			wasProtected: true, nowProtected: false,
			wantEncryption: true,
		},
		{
			name:              "verification turned off",
			wasProtected:      true,
			nowProtected:      true,
			wasSkippingVerify: false, nowSkippingVerify: true,
			wantVerification: true,
		},
		{
			name:         "both weakened at once",
			wasProtected: true, nowProtected: false,
			wasSkippingVerify: false, nowSkippingVerify: true,
			wantEncryption: true, wantVerification: true,
		},
		{
			// An install already running cleartext is not re-prompted on
			// every unrelated edit — the predicate is the change, not the
			// end state.
			name:         "already cleartext, unchanged",
			wasProtected: false, nowProtected: false,
		},
		{
			name:              "already skipping verification, unchanged",
			wasProtected:      true,
			nowProtected:      true,
			wasSkippingVerify: true, nowSkippingVerify: true,
		},
		{
			name:         "upgrading to protected is never a downgrade",
			wasProtected: false, nowProtected: true,
		},
		{
			name:              "turning verification back on is never a downgrade",
			wasProtected:      true,
			nowProtected:      true,
			wasSkippingVerify: true, nowSkippingVerify: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEnc, gotVer := ldapTransportWeakened(
				tt.wasProtected, tt.nowProtected, tt.wasSkippingVerify, tt.nowSkippingVerify)
			if gotEnc != tt.wantEncryption {
				t.Errorf("lostEncryption = %v, want %v", gotEnc, tt.wantEncryption)
			}
			if gotVer != tt.wantVerification {
				t.Errorf("lostVerification = %v, want %v", gotVer, tt.wantVerification)
			}
		})
	}
}

// Create has no previous state, so it passes a protected/verifying "before"
// and the gate reads the end state. Confirm that composition actually prompts
// on a fresh cleartext config and stays quiet on a fresh ldaps one.
func TestLDAPTransportWeakened_CreateReadsEndState(t *testing.T) {
	cleartext, _ := ldapTransportWeakened(
		true, ldapTransportProtected("ldap://dc.example.com", false), false, false)
	if !cleartext {
		t.Error("creating a cleartext config should prompt")
	}

	secure, _ := ldapTransportWeakened(
		true, ldapTransportProtected("ldaps://dc.example.com", false), false, false)
	if secure {
		t.Error("creating an ldaps config should not prompt")
	}

	_, unverified := ldapTransportWeakened(
		true, ldapTransportProtected("ldaps://dc.example.com", false), false, true)
	if !unverified {
		t.Error("creating a config that skips verification should prompt")
	}
}

// ldapTransportLabel feeds an audit row on both Create and Update. It must name
// the transport WITHOUT the server address: audit_log.details is readable by
// every Viewer, while GET on an LDAP config needs manage:user.
func TestLDAPTransportLabel(t *testing.T) {
	tests := []struct {
		name       string
		serverURL  string
		startTLS   bool
		skipVerify bool
		want       string
	}{
		{name: "ldaps", serverURL: "ldaps://dc.example.com", want: "ldaps"},
		{name: "uppercase ldaps", serverURL: "LDAPS://dc.example.com", want: "ldaps"},
		{
			name:      "starttls",
			serverURL: "ldap://dc.example.com",
			startTLS:  true,
			want:      "starttls",
		},
		{name: "cleartext", serverURL: "ldap://dc.example.com", want: "cleartext"},
		{
			// Unverified outranks the base only as a suffix — the operator
			// needs to know both which transport and that it is unchecked.
			name:       "ldaps unverified",
			serverURL:  "ldaps://dc.example.com",
			skipVerify: true,
			want:       "ldaps (unverified)",
		},
		{
			name:       "starttls unverified",
			serverURL:  "ldap://dc.example.com",
			startTLS:   true,
			skipVerify: true,
			want:       "starttls (unverified)",
		},
		{
			// No certificate to be unverified about.
			name:       "cleartext ignores skip_tls_verify",
			serverURL:  "ldap://dc.example.com",
			skipVerify: true,
			want:       "cleartext",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ldapTransportLabel(tt.serverURL, tt.startTLS, tt.skipVerify)
			if got != tt.want {
				t.Errorf("ldapTransportLabel(%q, %v, %v) = %q, want %q",
					tt.serverURL, tt.startTLS, tt.skipVerify, got, tt.want)
			}
			if strings.Contains(got, "dc.example.com") {
				t.Errorf("label %q leaks the server address into a Viewer-readable audit row", got)
			}
		})
	}
}
