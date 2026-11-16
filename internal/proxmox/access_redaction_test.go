package proxmox

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// A user's keys (two-factor secrets on a legacy OATH realm) and a minted token's
// value must not reach a log line or an error through any rendering a person reaches
// for by reflex; the user or token id must survive, so the line is still worth writing.
func TestGuard_AccessTypesNeverPrintTheirSecrets(t *testing.T) {
	t.Parallel()
	const secret = "JBSWY3DPEHPK3PXP-synthetic"
	user := AccessUser{UserID: "alice@pve", Keys: secret}
	detail := AccessUserDetail{UserID: "alice@pve", Keys: secret}
	token := AccessTokenCreated{FullTokenID: "nexara@pve!ci", Value: secret}
	keys := secret
	create := CreateAccessUserParams{UserID: "alice@pve", Password: secret, AccessUserFields: AccessUserFields{Keys: &keys}}
	update := UpdateAccessUserParams{AccessUserFields: AccessUserFields{Keys: &keys}}

	for _, tc := range []struct {
		name     string
		v        any
		survives string
	}{
		{"AccessUser", user, "alice@pve"},
		{"AccessUserDetail", detail, "alice@pve"},
		{"AccessTokenCreated", token, "nexara@pve!ci"},
		{"CreateAccessUserParams", create, "alice@pve"},
		{"UpdateAccessUserParams", update, "REDACTED"},
		// The other credential-bearing Proxmox types (types_redaction.go), one row per secret field.
		{"TermProxyResponse ticket", TermProxyResponse{UPID: "UPID:pve-01:x", Ticket: secret}, "UPID:pve-01:x"},
		{"TermProxyResponse password", TermProxyResponse{UPID: "UPID:pve-01:x", Password: secret}, "UPID:pve-01:x"},
		{"StorageConfig password", StorageConfig{Storage: "store01", Password: secret}, "store01"},
		{"StorageConfig encryption key", StorageConfig{Storage: "store01", EncryptionKey: secret}, "store01"},
		{"StorageConfig keyring", StorageConfig{Storage: "store01", Keyring: secret}, "store01"},
		{"CreateCTParams", CreateCTParams{Hostname: "linux01", Password: secret}, "linux01"},
		{"SDNIPAM", SDNIPAM{IPAM: "ipam01", Token: secret}, "ipam01"},
		{"CreateSDNIPAMParams", CreateSDNIPAMParams{IPAM: "ipam01", Token: secret}, "ipam01"},
		{"UpdateSDNIPAMParams", UpdateSDNIPAMParams{URL: "https://ipam.example.com", Token: secret}, "ipam.example.com"},
		{"SDNDNS", SDNDNS{DNS: "dns01", Key: secret}, "dns01"},
		{"CreateSDNDNSParams", CreateSDNDNSParams{DNS: "dns01", Key: secret}, "dns01"},
		{"UpdateSDNDNSParams", UpdateSDNDNSParams{URL: "https://dns.example.com", Key: secret}, "dns.example.com"},
		{"MetricServerConfig", MetricServerConfig{ID: "metrics01", Token: secret}, "metrics01"},
		{"CreateMetricServerParams", CreateMetricServerParams{ID: "metrics01", Token: secret}, "metrics01"},
		{"UpdateMetricServerParams", UpdateMetricServerParams{Server: "192.0.2.30", Token: secret}, "192.0.2.30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			slog.New(slog.NewTextHandler(&buf, nil)).Info("x", slog.Any("v", tc.v))
			for route, out := range map[string]string{
				"%v":    fmt.Sprintf("%v", tc.v),
				"%+v":   fmt.Sprintf("%+v", tc.v),
				"%#v":   fmt.Sprintf("%#v", tc.v),
				"slice": fmt.Sprintf("%v", []any{tc.v}),
				"error": fmt.Errorf("op: %v", tc.v).Error(),
				"slog":  buf.String(),
			} {
				if strings.Contains(out, secret) || !strings.Contains(out, tc.survives) {
					t.Errorf("%s rendered %q: want no secret and %q kept", route, out, tc.survives)
				}
			}
		})
	}
}
