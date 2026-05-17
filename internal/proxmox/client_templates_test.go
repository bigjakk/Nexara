package proxmox

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// --- GetVersion ---

func TestGetVersion(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/version": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}
			jsonResponse(w, map[string]string{
				"release": "9.1.2",
				"repoid":  "abc123",
				"version": "9.1",
			})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	v, err := c.GetVersion(context.Background())
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if v.Release != "9.1.2" || v.Version != "9.1" || v.RepoID != "abc123" {
		t.Errorf("unexpected version response: %+v", v)
	}
}

// --- PullOCIImage ---

func TestPullOCIImage_Success(t *testing.T) {
	var capturedBody string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/storage/local/oci-registry-pull": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			capturedBody = string(body)
			jsonResponse(w, "UPID:pve1:00001234:0001ABCD:65000000:download:local:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	upid, err := c.PullOCIImage(context.Background(), "pve1", "local", OCIPullParams{
		Reference: "docker.io/library/hello-world:latest",
	})
	if err != nil {
		t.Fatalf("PullOCIImage: %v", err)
	}
	if !strings.HasPrefix(upid, "UPID:") {
		t.Errorf("unexpected UPID: %q", upid)
	}
	form, err := url.ParseQuery(capturedBody)
	if err != nil {
		t.Fatalf("body not url-encoded: %v", err)
	}
	if got := form.Get("reference"); got != "docker.io/library/hello-world:latest" {
		t.Errorf("unexpected reference: %q", got)
	}
	if form.Has("file-name") {
		t.Errorf("file-name should be omitted when blank")
	}
}

func TestPullOCIImage_WithFilename(t *testing.T) {
	var capturedBody string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/storage/local/oci-registry-pull": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			capturedBody = string(body)
			jsonResponse(w, "UPID:pve1:1:1:1:download:local:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.PullOCIImage(context.Background(), "pve1", "local", OCIPullParams{
		Reference: "ghcr.io/owner/repo:v1.2.3",
		FileName:  "myimage",
	})
	if err != nil {
		t.Fatalf("PullOCIImage: %v", err)
	}
	form, _ := url.ParseQuery(capturedBody)
	if got := form.Get("file-name"); got != "myimage" {
		t.Errorf("file-name not forwarded: got %q", got)
	}
}

func TestPullOCIImage_Validation(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "http://localhost",
		TokenID:     "u@p!t",
		TokenSecret: "s",
	})
	tests := []struct {
		name    string
		node    string
		storage string
		params  OCIPullParams
		wantSub string
	}{
		{"empty node", "", "local", OCIPullParams{Reference: "x:y"}, "node"},
		{"empty storage", "pve1", "", OCIPullParams{Reference: "x:y"}, "storage"},
		{"empty reference", "pve1", "local", OCIPullParams{}, "reference"},
		{"reference too long", "pve1", "local", OCIPullParams{Reference: strings.Repeat("a", 513)}, "512"},
		{"bad reference chars", "pve1", "local", OCIPullParams{Reference: "bad ref$with spaces"}, "invalid"},
		{"filename too long", "pve1", "local", OCIPullParams{Reference: "x:y", FileName: strings.Repeat("a", 65)}, "64"},
		{"bad filename chars", "pve1", "local", OCIPullParams{Reference: "x:y", FileName: "../etc/passwd"}, "invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.PullOCIImage(context.Background(), tc.node, tc.storage, tc.params)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q missing substring %q", err, tc.wantSub)
			}
		})
	}
}

// --- DownloadURLToStorage ---

func TestDownloadURLToStorage_Success(t *testing.T) {
	var capturedBody string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/storage/local/download-url": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			capturedBody = string(body)
			jsonResponse(w, "UPID:pve1:1:1:1:download:local:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	upid, err := c.DownloadURLToStorage(context.Background(), "pve1", "local", URLDownloadParams{
		URL:      "https://example.com/debian.tar.gz",
		Content:  "vztmpl",
		Filename: "debian-12-standard.tar.gz",
	})
	if err != nil {
		t.Fatalf("DownloadURLToStorage: %v", err)
	}
	if upid == "" {
		t.Errorf("expected UPID")
	}
	form, _ := url.ParseQuery(capturedBody)
	if form.Get("url") != "https://example.com/debian.tar.gz" {
		t.Errorf("url mismatch: %q", form.Get("url"))
	}
	if form.Get("content") != "vztmpl" {
		t.Errorf("content mismatch: %q", form.Get("content"))
	}
	if form.Get("filename") != "debian-12-standard.tar.gz" {
		t.Errorf("filename mismatch: %q", form.Get("filename"))
	}
}

func TestDownloadURLToStorage_FullParams(t *testing.T) {
	var capturedBody string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/storage/local/download-url": func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			capturedBody = string(body)
			jsonResponse(w, "UPID:x")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	verify := false
	_, err := c.DownloadURLToStorage(context.Background(), "pve1", "local", URLDownloadParams{
		URL:                    "https://example.com/iso.img",
		Content:                "iso",
		Filename:               "x.iso",
		Checksum:               "deadbeef",
		ChecksumAlgorithm:      "sha256",
		DecompressionAlgorithm: "gz",
		VerifyCertificates:     &verify,
	})
	if err != nil {
		t.Fatalf("DownloadURLToStorage: %v", err)
	}
	form, _ := url.ParseQuery(capturedBody)
	if form.Get("checksum") != "deadbeef" {
		t.Errorf("checksum missing")
	}
	if form.Get("checksum-algorithm") != "sha256" {
		t.Errorf("checksum-algorithm missing")
	}
	if form.Get("compression") != "gz" {
		t.Errorf("compression missing")
	}
	if form.Get("verify-certificates") != "0" {
		t.Errorf("verify-certificates=0 expected, got %q", form.Get("verify-certificates"))
	}
}

func TestDownloadURLToStorage_Validation(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "http://localhost",
		TokenID:     "u@p!t",
		TokenSecret: "s",
	})
	tests := []struct {
		name    string
		params  URLDownloadParams
		wantSub string
	}{
		{"empty url", URLDownloadParams{Content: "iso", Filename: "x.iso"}, "URL"},
		{"bad content", URLDownloadParams{URL: "https://x", Content: "exe", Filename: "x"}, "content"},
		{"empty filename", URLDownloadParams{URL: "https://x", Content: "iso"}, "filename"},
		{"slash in filename", URLDownloadParams{URL: "https://x", Content: "iso", Filename: "a/b"}, "separators"},
		{"dotdot in filename", URLDownloadParams{URL: "https://x", Content: "iso", Filename: ".."}, "separators"},
		{"checksum without algorithm", URLDownloadParams{URL: "https://x", Content: "iso", Filename: "x.iso", Checksum: "abc"}, "algorithm"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.DownloadURLToStorage(context.Background(), "pve1", "local", tc.params)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q missing substring %q", err, tc.wantSub)
			}
		})
	}
}

// --- GetAppliances ---

func TestGetAppliances(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/aplinfo": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}
			jsonResponse(w, []map[string]string{
				{
					"template": "debian-12-standard_12.7-1_amd64.tar.zst",
					"os":       "debian-12",
					"type":     "lxc",
					"version":  "12.7-1",
					"section":  "system",
				},
				{
					"template": "alpine-3.20-default_20240908_amd64.tar.xz",
					"os":       "alpine-3.20",
					"type":     "lxc",
					"version":  "20240908",
					"section":  "system",
				},
			})
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	apps, err := c.GetAppliances(context.Background(), "pve1")
	if err != nil {
		t.Fatalf("GetAppliances: %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("expected 2 appliances, got %d", len(apps))
	}
	if apps[0].Template != "debian-12-standard_12.7-1_amd64.tar.zst" || apps[0].OS != "debian-12" {
		t.Errorf("unexpected first appliance: %+v", apps[0])
	}
}

// --- DownloadAppliance ---

func TestDownloadAppliance_Success(t *testing.T) {
	var capturedBody string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve1/aplinfo": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			capturedBody = string(body)
			jsonResponse(w, "UPID:pve1:1:1:1:download:local:user@pam:")
		},
	})
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	upid, err := c.DownloadAppliance(context.Background(), "pve1", "local", "debian-12-standard_12.7-1_amd64.tar.zst")
	if err != nil {
		t.Fatalf("DownloadAppliance: %v", err)
	}
	if upid == "" {
		t.Errorf("expected UPID")
	}
	form, _ := url.ParseQuery(capturedBody)
	if form.Get("storage") != "local" {
		t.Errorf("storage mismatch: %q", form.Get("storage"))
	}
	if form.Get("template") != "debian-12-standard_12.7-1_amd64.tar.zst" {
		t.Errorf("template mismatch: %q", form.Get("template"))
	}
}

func TestDownloadAppliance_Validation(t *testing.T) {
	c, _ := NewClient(ClientConfig{
		BaseURL:     "http://localhost",
		TokenID:     "u@p!t",
		TokenSecret: "s",
	})
	tests := []struct {
		name     string
		node     string
		storage  string
		template string
		wantSub  string
	}{
		{"empty node", "", "local", "x", "node"},
		{"empty storage", "pve1", "", "x", "storage"},
		{"empty template", "pve1", "local", "", "template"},
		{"template too long", "pve1", "local", strings.Repeat("a", 256), "255"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.DownloadAppliance(context.Background(), tc.node, tc.storage, tc.template)
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q missing substring %q", err, tc.wantSub)
			}
		})
	}
}

// --- invalidFilename helper ---

func TestInvalidFilename(t *testing.T) {
	cases := map[string]bool{
		"":               true,
		".":              true,
		"..":             true,
		"a/b":            true,
		"a\\b":           true,
		"..hidden":       true, // disallow consecutive dots anywhere — defensive
		"hello.tar.gz":   false,
		"debian-12_amd64": false,
	}
	for in, want := range cases {
		got := invalidFilename(in)
		if got != want {
			t.Errorf("invalidFilename(%q) = %v, want %v", in, got, want)
		}
	}
}
