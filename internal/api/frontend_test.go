package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// spaTestFS is a minimal in-memory frontend bundle: an index.html shell plus
// one hashed asset, mirroring what the real `dist` embed looks like.
const indexHTMLMarker = "<!doctype html><title>nexara-spa-shell</title>"

func spaTestFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":           {Data: []byte(indexHTMLMarker)},
		"assets/app-123.js":    {Data: []byte("console.log('app')")},
		"manifest.webmanifest": {Data: []byte(`{"name":"Nexara"}`)},
	}
}

// TestRegisterFrontend_UnmatchedAPIPathReturnsJSON404 is the regression guard
// for the SPA catch-all swallowing unmatched /api/* requests: such paths must
// return a JSON 404 envelope, never the 200 + text/html SPA shell.
func TestRegisterFrontend_UnmatchedAPIPathReturnsJSON404(t *testing.T) {
	s := newTestServer(t)
	s.RegisterFrontend(spaTestFS())

	cases := []string{
		"/api/v1/does-not-exist",
		"/api/v1/clusters/abc/bananas",
		"/api/v1/version/extra", // partial match of a real prefix, still no route
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			resp, err := s.App().Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusNotFound)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}

			body, _ := io.ReadAll(resp.Body)
			if strings.Contains(string(body), indexHTMLMarker) {
				t.Fatalf("response served the SPA shell instead of a JSON 404: %s", body)
			}
			var env ErrorResponse
			if err := json.Unmarshal(body, &env); err != nil {
				t.Fatalf("body is not the JSON error envelope: %v (%s)", err, body)
			}
			if env.Error != "not_found" {
				t.Errorf("error = %q, want %q", env.Error, "not_found")
			}
		})
	}
}

// TestRegisterFrontend_DeepLinkServesSPA ensures the guard doesn't over-reach:
// non-API paths (browser deep links into the React router) still get the shell.
func TestRegisterFrontend_DeepLinkServesSPA(t *testing.T) {
	s := newTestServer(t)
	s.RegisterFrontend(spaTestFS())

	req := httptest.NewRequest(http.MethodGet, "/clusters/123/vms", nil)
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), indexHTMLMarker) {
		t.Errorf("deep link did not serve the SPA shell: %s", body)
	}
}

// TestRegisterFrontend_StaticAssetServed ensures real bundle files are still
// served as-is (the fallback only kicks in when the file is absent).
func TestRegisterFrontend_StaticAssetServed(t *testing.T) {
	s := newTestServer(t)
	s.RegisterFrontend(spaTestFS())

	req := httptest.NewRequest(http.MethodGet, "/assets/app-123.js", nil)
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "console.log('app')" {
		t.Errorf("asset body = %q, want the JS file contents", body)
	}
}

// TestRegisterFrontend_CacheHeaders pins the SPA cache policy: content-hashed
// assets are immutable, while the shell — and ANY path that falls back to it,
// including a stale asset hash — must always revalidate. Without this, a
// browser (mobile especially) heuristically caches the shell, which after an
// upgrade references deleted asset hashes and renders a blank page.
func TestRegisterFrontend_CacheHeaders(t *testing.T) {
	s := newTestServer(t)
	s.RegisterFrontend(spaTestFS())

	cases := []struct {
		path string
		want string
	}{
		{"/assets/app-123.js", "public, max-age=31536000, immutable"},
		{"/", "no-cache"},
		{"/clusters/123/vms", "no-cache"}, // deep link → shell
		// A no-longer-existing hash serves the HTML shell via NotFoundFile —
		// it must NOT inherit the immutable policy or the wrong body gets
		// cached under the asset URL for a year.
		{"/assets/app-oldhash.js", "no-cache"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			resp, err := s.App().Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if got := resp.Header.Get("Cache-Control"); got != tc.want {
				t.Errorf("Cache-Control = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRegisterFrontend_ManifestContentType pins the PWA manifest's MIME type:
// Go's mime table doesn't know .webmanifest, and the octet-stream fallback is
// refused by browsers under X-Content-Type-Options: nosniff.
func TestRegisterFrontend_ManifestContentType(t *testing.T) {
	s := newTestServer(t)
	s.RegisterFrontend(spaTestFS())

	req := httptest.NewRequest(http.MethodGet, "/manifest.webmanifest", nil)
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Errorf("Content-Type = %q, want application/manifest+json", ct)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
}

// TestRegisterFrontend_RealAPIRouteUnchanged confirms a matching API route is
// untouched by the frontend mount: it still returns its JSON 200.
func TestRegisterFrontend_RealAPIRouteUnchanged(t *testing.T) {
	s := newTestServer(t)
	s.RegisterFrontend(spaTestFS())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

// TestRegisterFrontend_WrongMethodStill405 confirms the fix preserves Fiber's
// native method-not-allowed handling for a real route (the SPA mount must not
// turn a 405 into a 404 or an HTML shell).
func TestRegisterFrontend_WrongMethodStill405(t *testing.T) {
	s := newTestServer(t)
	s.RegisterFrontend(spaTestFS())

	// /api/v1/version is registered for GET only.
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/version", nil)
	resp, err := s.App().Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
	body, _ := io.ReadAll(resp.Body)
	var env ErrorResponse
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("body is not the JSON error envelope: %v (%s)", err, body)
	}
	if env.Error != "method_not_allowed" {
		t.Errorf("error = %q, want %q", env.Error, "method_not_allowed")
	}
}
