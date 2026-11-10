package scanner

import (
	"bytes"
	"compress/zlib"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/netguard"
)

// TestBinarySources reads the binary → source map from the tracker's debsecan
// export, and fetches a release's list once per TTL, or once per retry window
// after a failure, rather than once per node scanned.
func TestBinarySources(t *testing.T) {
	t.Parallel()
	// The issue list, the vulnerability list and the source map, as served.
	var file bytes.Buffer
	zw := zlib.NewWriter(&file)
	_, _ = zw.Write([]byte("VERSION 1\nCVE-2026-0001,,synthetic issue\n\nopenssl,0,SM ,,\n\n" +
		"openssl,libssl3 libssl3 openssl\nfoo,libfoo1\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		status int
		want   map[string]string
	}{
		{"each binary's source", http.StatusOK, map[string]string{"libssl3": "openssl", "openssl": "openssl", "libfoo1": "foo"}},
		{"none while the export cannot be fetched", http.StatusServiceUnavailable, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var fetches atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fetches.Add(1)
				if r.URL.Path != "/bookworm" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write(file.Bytes())
			}))
			t.Cleanup(srv.Close)
			c := NewCVEClient(nil, netguard.NewHTTPClient(5*time.Second), slog.New(slog.DiscardHandler))
			c.debsecanURL = srv.URL + "/"

			for range 2 {
				if got := c.binarySources(context.Background(), "bookworm"); !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
			if n := fetches.Load(); n != 1 {
				t.Errorf("%d fetches for two lookups, want 1", n)
			}
		})
	}
}
