package proxmox

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// recordedRequest is one request a recording stand-in Proxmox received. The
// path is the decoded one (r.URL.Path), which is what the callers compare: none
// of the names they send has anything in it to decode.
type recordedRequest struct {
	method, path string
	query, form  url.Values
}

// newRecordingServer starts a stand-in Proxmox that records every request —
// method, path, query and form body — and answers each with body. It is a bare
// handler and not a ServeMux, which would clean dot segments and so hide a request
// that should never have been sent.
func newRecordingServer(t *testing.T, body string) (*httptest.Server, *[]recordedRequest) {
	t.Helper()
	var seen []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		seen = append(seen, recordedRequest{r.Method, r.URL.Path, r.URL.Query(), r.PostForm})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// sentRequests runs call against a stand-in that answers {"data":null}, and
// returns the requests it received along with call's error.
func sentRequests(t *testing.T, call func(*Client) error) ([]recordedRequest, error) {
	t.Helper()
	srv, seen := newRecordingServer(t, `{"data":null}`)
	err := call(newTestClient(t, srv.URL))
	return *seen, err
}

// requireSentOnce fails unless call succeeded by sending exactly one request,
// and returns that request.
func requireSentOnce(t *testing.T, call func(*Client) error) recordedRequest {
	t.Helper()
	got, err := sentRequests(t, call)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("made %d requests %v, want 1", len(got), got)
	}
	return got[0]
}

// requireRefusedUnsent fails unless call returned an error that is want and
// nothing reached the stand-in. It returns the error for a message check, nil
// when there was none.
func requireRefusedUnsent(t *testing.T, call func(*Client) error, want error) error {
	t.Helper()
	got, err := sentRequests(t, call)
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if len(got) != 0 {
		t.Errorf("%d request(s) reached Proxmox, want none: %v", len(got), got)
	}
	return err
}
