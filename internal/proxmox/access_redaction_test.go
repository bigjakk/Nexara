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

	for _, tc := range []struct {
		name     string
		v        any
		survives string
	}{
		{"AccessUser", user, "alice@pve"},
		{"AccessUserDetail", detail, "alice@pve"},
		{"AccessTokenCreated", token, "nexara@pve!ci"},
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
