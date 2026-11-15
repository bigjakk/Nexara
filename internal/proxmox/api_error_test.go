package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// checkStatus is where a Proxmox response body becomes a typed error, and it is
// the layer the handler's status mapping depends on. Testing mapProxmoxError in
// isolation is what let a parameter-verification failure reach the operator as
// 502 for so long: the handler looked for a rejection map that checkStatus had
// already flattened away.
func TestCheckStatus_ParameterVerificationKeepsTheFieldMap(t *testing.T) {
	body := []byte(`{"errors":{"vmid":"property is not defined in schema"},"message":"Parameter verification failed.\n","data":null}`)

	var apiErr *APIError
	if !errors.As(checkStatus(http.StatusBadRequest, body, newSecretSet()), &apiErr) {
		t.Fatal("want *APIError")
	}
	if got := len(apiErr.Fields); got != 1 {
		t.Fatalf("Fields has %d entries, want 1 — the handler keys its 400 on this", got)
	}
	if got := apiErr.Fields["vmid"]; got != "property is not defined in schema" {
		t.Errorf("Fields[vmid] = %q", got)
	}
	// Message stays the flattened form the Contains-based helpers in errors.go
	// and the collector already read.
	if want := "vmid: property is not defined in schema"; apiErr.Message != want {
		t.Errorf("Message = %q, want %q", apiErr.Message, want)
	}
	if !apiErr.IsParameterRejection() {
		t.Error("IsParameterRejection() = false; the handler's 400 keys on this")
	}
}

// checkStatus parses the envelope on everything from 400 up, so a rejection map
// can arrive attached to a status that is not Proxmox rejecting parameters.
func TestAPIError_IsParameterRejectionNeedsA400(t *testing.T) {
	body := []byte(`{"errors":{"detail":"no healthy upstream"},"message":"Service Unavailable"}`)

	var apiErr *APIError
	if !errors.As(checkStatus(http.StatusServiceUnavailable, body, newSecretSet()), &apiErr) {
		t.Fatal("want *APIError")
	}
	if len(apiErr.Fields) == 0 {
		t.Fatal("Fields should still be captured; only the classification differs")
	}
	if apiErr.IsParameterRejection() {
		t.Error("a 503 carrying a rejection map is the gateway's failure, not the caller's")
	}
}

func TestCheckStatus_MultiFieldRejectionIsOrdered(t *testing.T) {
	body := []byte(`{"errors":{"vmid":"bad","name":"bad","cores":"bad","memory":"bad"},"message":"Parameter verification failed.\n"}`)
	want := "cores: bad; memory: bad; name: bad; vmid: bad"

	// Map iteration is randomised per run and per range, so repeat: an unsorted
	// implementation shows the operator a different order on every request.
	for i := range 20 {
		var apiErr *APIError
		if !errors.As(checkStatus(http.StatusBadRequest, body, newSecretSet()), &apiErr) {
			t.Fatal("want *APIError")
		}
		if apiErr.Message != want {
			t.Fatalf("iteration %d: Message = %q, want %q", i, apiErr.Message, want)
		}
	}
}

func TestCheckStatus_NonValidationBodiesCarryNoFields(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		wantMessage string
	}{
		{
			// The shape a cluster without Ceph returns. No rejection map, so
			// the handler must not call this the operator's fault.
			name:        "envelope without an errors map",
			status:      http.StatusInternalServerError,
			body:        `{"message":"binary not installed: /usr/bin/ceph-mon\n","data":null}`,
			wantMessage: `{"message":"binary not installed: /usr/bin/ceph-mon"}`,
		},
		{
			name:        "plain text body",
			status:      http.StatusInternalServerError,
			body:        "500 Internal Server Error",
			wantMessage: "500 Internal Server Error",
		},
		{
			name:        "empty errors map is not a rejection",
			status:      http.StatusBadRequest,
			body:        `{"errors":{},"message":"nope\n"}`,
			wantMessage: `{"message":"nope"}`,
		},
		{
			name:        "empty body",
			status:      http.StatusInternalServerError,
			body:        "",
			wantMessage: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var apiErr *APIError
			if !errors.As(checkStatus(tt.status, []byte(tt.body), newSecretSet()), &apiErr) {
				t.Fatal("want *APIError")
			}
			if len(apiErr.Fields) != 0 {
				t.Errorf("Fields = %v, want none", apiErr.Fields)
			}
			if apiErr.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", apiErr.Message, tt.wantMessage)
			}
			if apiErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.status)
			}
		})
	}
}

// The sentinel branches run before the APIError one, so they never carry Fields.
func TestCheckStatus_SentinelsAreUnchanged(t *testing.T) {
	if err := checkStatus(http.StatusNotFound, []byte(`{"errors":{"a":"b"}}`), newSecretSet()); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 = %v, want ErrNotFound", err)
	}
	if err := checkStatus(http.StatusForbidden, []byte("denied"), newSecretSet()); !errors.Is(err, ErrForbidden) {
		t.Errorf("403 = %v, want ErrForbidden", err)
	}
	if err := checkStatus(http.StatusOK, []byte("{}"), newSecretSet()); err != nil {
		t.Errorf("200 = %v, want nil", err)
	}
}

// TestErrorTextHoldsNoReflectedSecret runs requests against a stand-in that
// reflects them — the Authorization header and a password parameter — in each
// shape an error answer comes in, and requires the error to carry neither, no
// control character, and no more than maxErrorTextKept of the text, while the
// phrase the Is* helpers match on survives.
func TestErrorTextHoldsNoReflectedSecret(t *testing.T) {
	t.Parallel()
	const password = "pa55word-of-alice"
	post := func(c *Client) error {
		return c.doPost(context.Background(), "/access/users", url.Values{"password": {password}}, nil)
	}
	blockJobs := func(c *Client) error {
		_, err := c.GetBlockJobs(context.Background(), "pve-01", 101)
		return err
	}
	pad := strings.Repeat("x", 3*maxErrorTextKept)
	for _, tt := range []struct {
		name   string
		status int
		body   func(auth, pw string) string
		call   func(*Client) error
		is     func(error) bool
		// endless holds the answer open after the body, so only a capped read returns.
		endless bool
	}{
		{"plain 500", 500, func(a, pw string) string { return "VM 100 is locked (backup)\n" + a + "\x1b[2J" + pw }, post, IsLockedError, false},
		{"JSON sentence", 500, func(a, pw string) string {
			return `{"data":null,"message":"VM 100 is locked: \u001b]0;` + a + ` ` + pw + `\n"}`
		}, post, IsLockedError, false},
		{"rejection map", 400, func(a, pw string) string {
			return `{"errors":{"password":"value '` + pw + `' is invalid","header":"` + a + `"}}`
		}, post, func(err error) bool { var e *APIError; return errors.As(err, &e) && e.IsParameterRejection() }, false},
		{"403", 403, func(a, pw string) string { return "permission denied: " + a + " " + pw }, post,
			func(err error) bool { return errors.Is(err, ErrForbidden) }, false},
		{"long, the secret past the cut", 500, func(a, pw string) string { return "VM 100 is locked " + pad + a + pw }, post, IsLockedError, false},
		{"monitor ERROR reply", 200, func(a, _ string) string {
			return `{"data":"ERROR: VM 101 not running \u0007` + a + `"}`
		}, blockJobs, IsGuestNotRunningError, false},
		{"never ends", 500, func(a, _ string) string { return "VM 100 is locked " + a + pad + pad + pad + pad }, post, IsLockedError, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body(r.Header.Get("Authorization"), r.PostForm.Get("password")))
				if tt.endless {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			t.Cleanup(srv.Close)
			c := newTestClient(t, srv.URL)
			err := tt.call(c)
			if err == nil || !tt.is(err) {
				t.Fatalf("err = %v, want one the phrase is still recognised in", err)
			}
			texts := []string{err.Error()}
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				texts = append(texts, apiErr.Message)
				// The sentence as mapProxmoxError decodes it for the answer.
				var envelope struct{ Message string }
				if json.Unmarshal([]byte(apiErr.Message), &envelope) == nil {
					texts = append(texts, envelope.Message)
				}
				for k, v := range apiErr.Fields {
					texts = append(texts, k, v)
				}
				if len(apiErr.Message) > maxErrorTextKept+len(" …") {
					t.Errorf("Message is %d bytes, want at most %d", len(apiErr.Message), maxErrorTextKept)
				}
			}
			for _, text := range texts {
				for _, secret := range []string{c.tokenSecret, password, "\x1b", "\x07", "\n"} {
					if strings.Contains(text, secret) {
						t.Errorf("error text %.120q… carries %q", text, secret)
					}
				}
			}
		})
	}
}
