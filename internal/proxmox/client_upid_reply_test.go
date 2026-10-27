package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The calls that start a task (an ACME account's update and deactivation, the
// SDN apply, a node's network apply) read their UPID from a reply that arrives
// after the change was made. A reply that is not a string is "no UPID", not an
// error: failing the call would report a failure for work that was done, and
// leave it unaudited.
func TestTaskStartingCallsTolerateAReplyWithoutAUPID(t *testing.T) {
	t.Parallel()
	const upid = "UPID:pve-01:00001A2B:00C0FFEE:6540A1B2:acmeupdate::root@pam:"
	ctx := context.Background()
	calls := map[string]func(*Client) (string, error){
		"UpdateACMEAccount": func(c *Client) (string, error) {
			return c.UpdateACMEAccount(ctx, "default", UpdateACMEAccountParams{Contact: "admin@example.com"})
		},
		"DeleteACMEAccount":  func(c *Client) (string, error) { return c.DeleteACMEAccount(ctx, "default") },
		"ApplySDN":           func(c *Client) (string, error) { return c.ApplySDN(ctx) },
		"ApplyNetworkConfig": func(c *Client) (string, error) { return c.ApplyNetworkConfig(ctx, "pve-01") },
	}
	for _, reply := range []struct{ name, data, want string }{
		{"a UPID", `"` + upid + `"`, upid},
		{"null", `null`, ""},
		{"an object", `{"status":"ok"}`, ""},
		{"a number", `0`, ""},
		{"text that is not a UPID", `"ok"`, ""},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":` + reply.data + `}`))
		}))
		t.Cleanup(srv.Close)
		client := newTestClient(t, srv.URL)
		for name, call := range calls {
			t.Run(name+"/"+reply.name, func(t *testing.T) {
				t.Parallel()
				got, err := call(client)
				if err != nil || got != reply.want {
					t.Errorf("%s = (%q, %v), want (%q, nil)", name, got, err, reply.want)
				}
			})
		}
	}
}
