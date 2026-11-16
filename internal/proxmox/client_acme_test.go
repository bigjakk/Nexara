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

// TestCreateACMEAccountOmitsAnEmptyName: an empty Name must reach PVE as no name at all, since
// register_account falls back to 'default' only when the key is absent (`// 'default'`); a
// sent `name=` fails pve-configid and 400s. handlers.defaultACMEAccountName relies on it, and
// only this test reads the form body.
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
