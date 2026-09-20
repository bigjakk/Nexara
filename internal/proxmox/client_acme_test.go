package proxmox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// newACMEAccountCaptureServer records the POST form and answers with a UPID,
// which is what CreateACMEAccount decodes its return value out of.
// newFormCaptureServer answers `{"data":null}` and cannot stand in here.
func newACMEAccountCaptureServer(t *testing.T) (*httptest.Server, *[]url.Values) {
	t.Helper()
	var seen []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		seen = append(seen, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":"UPID:pve-01:00001234:0000ABCD:66000000:acmeregister::root@pam:"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// TestCreateACMEAccountOmitsAnEmptyName pins the premise the whole
// empty-name path rests on: an empty Name must reach PVE as NO name at all.
//
// PVE's register_account resolves the account name with
// `extract_param($param, 'name') // 'default'` — defined-or, so it falls back
// only when the key is ABSENT. Send `name=` instead and the value is defined,
// fails the pve-configid format (`[a-z][a-z0-9_-]+`) before the body runs, and
// a request that has always worked starts 400ing.
//
// handlers.defaultACMEAccountName documents that dependency in a comment; this
// is what makes the comment bite. Nothing else asserts it — the handler-side
// test drives a capture server that records the request TARGET only, never the
// form body.
func TestCreateACMEAccountOmitsAnEmptyName(t *testing.T) {
	for _, tt := range []struct {
		name     string
		supplied string
		wantKey  bool
	}{
		{"empty name is omitted entirely", "", false},
		{"a chosen name is sent", "letsencrypt-prod", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv, seen := newACMEAccountCaptureServer(t)
			c := newTestClient(t, srv.URL)

			upid, err := c.CreateACMEAccount(context.Background(), CreateACMEAccountParams{
				Name:    tt.supplied,
				Contact: "admin@example.com",
			})
			if err != nil {
				t.Fatalf("CreateACMEAccount: %v", err)
			}
			if upid == "" {
				t.Fatal("CreateACMEAccount returned no UPID; the capture server was not reached")
			}
			if len(*seen) != 1 {
				t.Fatalf("issued %d requests, want 1", len(*seen))
			}
			form := (*seen)[0]
			if _, ok := form["name"]; ok != tt.wantKey {
				t.Errorf("form carries a name key = %t, want %t (value %q) — an empty name sent "+
					"as `name=` is DEFINED to PVE, so it neither defaults to \"default\" nor "+
					"survives the pve-configid format", ok, tt.wantKey, form.Get("name"))
			}
			if tt.wantKey && form.Get("name") != tt.supplied {
				t.Errorf("name = %q, want %q", form.Get("name"), tt.supplied)
			}
			// The control: the request really was made and carries the field
			// that IS required, so a form that lost everything cannot pass the
			// check above by being empty.
			if got := form.Get("contact"); got != "admin@example.com" {
				t.Errorf("contact = %q, want it sent alongside", got)
			}
		})
	}
}
