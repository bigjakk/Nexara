package proxmox

import (
	"context"
	"encoding/base64"
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
	if !errors.As(checkStatus(http.StatusBadRequest, body, newErrorSecrets()), &apiErr) {
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
	if !errors.As(checkStatus(http.StatusServiceUnavailable, body, newErrorSecrets()), &apiErr) {
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
		if !errors.As(checkStatus(http.StatusBadRequest, body, newErrorSecrets()), &apiErr) {
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
			if !errors.As(checkStatus(tt.status, []byte(tt.body), newErrorSecrets()), &apiErr) {
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
	if err := checkStatus(http.StatusNotFound, []byte(`{"errors":{"a":"b"}}`), newErrorSecrets()); !errors.Is(err, ErrNotFound) {
		t.Errorf("404 = %v, want ErrNotFound", err)
	}
	if err := checkStatus(http.StatusForbidden, []byte("denied"), newErrorSecrets()); !errors.Is(err, ErrForbidden) {
		t.Errorf("403 = %v, want ErrForbidden", err)
	}
	if err := checkStatus(http.StatusOK, []byte("{}"), newErrorSecrets()); err != nil {
		t.Errorf("200 = %v, want nil", err)
	}
}

// TestErrorTextHoldsNoReflectedSecret runs requests against a stand-in that
// reflects them — the Authorization header and the secret parameter sent — in
// each shape an error answer comes in, and requires the error to carry
// neither, no control character, and no more than maxErrorTextKept of the
// text, while the phrase the Is* helpers match on, and the sentence an
// envelope carries, survive.
func TestErrorTextHoldsNoReflectedSecret(t *testing.T) {
	t.Parallel()
	const password = "pa55word-of-alice"
	pw := url.Values{"password": {password}}
	post := func(form url.Values) func(*Client) error {
		return func(c *Client) error { return c.doPost(context.Background(), "/access/users", form, nil) }
	}
	blockJobs := func(c *Client) error {
		_, err := c.GetBlockJobs(context.Background(), "pve-01", 101)
		return err
	}
	isRejection := func(err error) bool { var e *APIError; return errors.As(err, &e) && e.IsParameterRejection() }
	pad := strings.Repeat("x", 3*maxErrorTextKept)
	const acmeCreds = "CF_Token=cloudflare-credential"
	for _, tt := range []struct {
		name   string
		status int
		form   url.Values
		// body is the answer, from the request's Authorization header and the
		// one secret it sent; a Location answer refuses as a redirect instead.
		body     func(auth, secret string) string
		call     func(*Client) error
		is       func(error) bool
		sentence string   // the start of the decoded sentence, when there is one
		endless  bool     // holds the answer open after the body, so only a capped read returns
		leaks    []string // further strings the error must not carry
	}{
		{"plain 500", 500, pw, func(a, s string) string { return "VM 100 is locked (backup)\n" + a + "\x1b[2J" + s }, nil, IsLockedError, "", false, nil},
		{"JSON sentence", 500, pw, func(a, s string) string {
			return `{"data":null,"message":"VM 100 is locked: \u001b]0;` + a + ` ` + s + `\n"}`
		}, nil, IsLockedError, "VM 100 is locked:", false, nil},
		{"rejection map", 400, pw, func(a, s string) string {
			return `{"errors":{"password":"value '` + s + `' is invalid","header":"` + a + `"}}`
		}, nil, isRejection, "", false, nil},
		{"403", 403, pw, func(a, s string) string { return "permission denied: " + a + " " + s }, nil,
			func(err error) bool { return errors.Is(err, ErrForbidden) }, "", false, nil},
		{"long, the secret past the cut", 500, pw, func(a, s string) string { return "VM 100 is locked " + pad + a + s }, nil, IsLockedError, "", false, nil},
		{"a 10 KiB die()", 500, pw, func(a, s string) string {
			return `{"data":null,"message":"command 'zfs list' failed: ` + a + s + strings.Repeat("y", 10<<10) + `\n"}`
		}, nil, func(err error) bool { var e *APIError; return errors.As(err, &e) }, "command 'zfs list' failed: PVEAPIToken=user@pam!test=REDACTEDREDACTEDyyy", false, nil},
		{"a die() past the read cap", 500, pw, func(a, s string) string {
			return `{"data":null,"message":"VM 100 is locked \"really\" ` + a + s + strings.Repeat(`é`, maxErrorBodyRead) + `\n"}`
		}, nil, IsLockedError, `VM 100 is locked "really" PVEAPIToken=user@pam!test=REDACTEDREDACTEDééé`, false, nil},
		{"monitor ERROR reply", 200, nil, func(a, _ string) string {
			return `{"data":"ERROR: VM 101 not running \u0007` + a + `"}`
		}, blockJobs, IsGuestNotRunningError, "", false, nil},
		{"never ends", 500, pw, func(a, _ string) string { return "VM 100 is locked " + a + strings.Repeat(pad, 40) }, nil, IsLockedError, "", true, nil},
		{"a container's env", 400, url.Values{"env": {"DB_PASS=hunter-two"}}, func(_, s string) string {
			return `{"errors":{"env":"invalid format - '` + s + `'"}}`
		}, nil, isRejection, "", false, nil},
		{"ACME plugin data, decoded", 500, url.Values{"data": {base64.StdEncoding.EncodeToString([]byte(acmeCreds))}},
			func(string, string) string { return "plugin rejected " + acmeCreds }, nil,
			func(err error) bool { var e *APIError; return errors.As(err, &e) }, "", false, nil},
		{"a NUL-separated env, echoed whole", 400, url.Values{"env": {"API_KEY=s3cretvalue123\x00OTHER=x"}},
			func(_, s string) string {
				return `{"errors":{"env":"invalid env: ` + strings.ReplaceAll(s, "\x00", `\u0000`) + `"}}`
			},
			nil, isRejection, "", false, []string{"s3cretvalue123"}},
		{"a password holding a tab", 400, url.Values{"password": {"tab\tseparated-pass"}}, func(_, s string) string {
			return `{"errors":{"password":"value '` + strings.ReplaceAll(s, "\t", `\t`) + `' is invalid"}}`
		}, nil, isRejection, "", false, []string{"separated-pass"}},
		{"one entry of an env, echoed alone", 400, url.Values{"env": {"API_KEY=s3cretvalue123\x00OTHER=x"}},
			func(string, string) string { return `{"errors":{"env":"bad entry 'API_KEY=s3cretvalue123'"}}` },
			nil, isRejection, "", false, []string{"s3cretvalue123"}},
		{"multi-line ACME data, decoded", 500,
			url.Values{"data": {base64.StdEncoding.EncodeToString([]byte("CF_Token=s3cretvalue123\nCF_Account_ID=abc"))}},
			func(string, string) string { return "plugin rejected CF_Token=s3cretvalue123\nCF_Account_ID=abc" }, nil,
			func(err error) bool { var e *APIError; return errors.As(err, &e) }, "", false, []string{"s3cretvalue123"}},
		{"a refused redirect", 302, pw, nil, nil, func(err error) bool { return errors.Is(err, ErrConnectionFailed) }, "", false, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = r.ParseForm()
				var secret string
				for _, v := range r.PostForm {
					secret = v[0]
				}
				if tt.body == nil {
					w.Header().Set("Location", "https://192.0.2.10/echo?"+url.QueryEscape(r.Header.Get("Authorization")))
					w.WriteHeader(tt.status)
					return
				}
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body(r.Header.Get("Authorization"), secret))
				if tt.endless {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			t.Cleanup(srv.Close)
			c := newTestClient(t, srv.URL)
			call := tt.call
			if call == nil {
				call = post(tt.form)
			}
			err := call(c)
			if err == nil || !tt.is(err) {
				t.Fatalf("err = %v, want one the phrase is still recognised in", err)
			}
			secrets := []string{c.tokenSecret, url.QueryEscape(c.tokenSecret), "\x1b", "\x07", "\n", acmeCreds}
			for _, v := range tt.form {
				secrets = append(secrets, v[0])
			}
			secrets = append(secrets, tt.leaks...)
			texts := []string{err.Error()}
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				texts = append(texts, apiErr.Message)
				// The sentence as mapProxmoxError decodes it for the answer.
				var envelope struct{ Message string }
				if json.Unmarshal([]byte(apiErr.Message), &envelope) == nil {
					texts = append(texts, envelope.Message)
				}
				if !strings.HasPrefix(envelope.Message, tt.sentence) {
					t.Errorf("decoded sentence = %.80q…, want it to start %q", envelope.Message, tt.sentence)
				}
				for k, v := range apiErr.Fields {
					texts = append(texts, k, v)
				}
				if len(apiErr.Message) > maxErrorTextKept+len(` …"}`)+len(`{"message":`) {
					t.Errorf("Message is %d bytes, want at most about %d", len(apiErr.Message), maxErrorTextKept)
				}
			}
			for _, text := range texts {
				for _, secret := range secrets {
					if strings.Contains(text, secret) {
						t.Errorf("error text %.120q… carries %q", text, secret)
					}
				}
			}
		})
	}
}

// TestErrorSecretsScrubWholeValuesOnly: a secret is matched whole, and a short
// one only set off by punctuation, as Proxmox quotes a value, so words and the
// phrases the Is* helpers match survive a password that is also a word.
func TestErrorSecretsScrubWholeValuesOnly(t *testing.T) {
	t.Parallel()
	s := newErrorSecrets("backup", "locked", "password123", "a0b1c2d3-e4f5")
	for _, tt := range []struct{ in, want string }{
		{"VM 100 is locked (backup)", "VM 100 is locked (REDACTED)"},
		{"password: value 'password123' is invalid", "password: value 'REDACTED' is invalid"},
		{"value 'backup' is not allowed", "value 'REDACTED' is not allowed"},
		{"token=a0b1c2d3-e4f5x", "token=REDACTEDx"},
		{"backups are lockedown", "backups are lockedown"},
	} {
		if got := s.scrub(tt.in); got != tt.want {
			t.Errorf("scrub(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// A storage password that is also the word Proxmox says must not stop the
// lock from being recognised.
func TestShortPasswordLeavesIsLockedError(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"data":null,"message":"VM 100 is locked (backup)\n"}`)
	}))
	t.Cleanup(srv.Close)
	err := newTestClient(t, srv.URL).doPost(context.Background(), "/storage", url.Values{"password": {"locked"}}, nil)
	if !IsLockedError(err) {
		t.Errorf("err = %v, want IsLockedError", err)
	}
}
