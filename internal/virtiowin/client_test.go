package virtiowin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.base = srv.URL
	return c
}

const archiveIndex = `<html><head><title>Index</title></head><body>
<h1>Index of /groups/virt/virtio-win/direct-downloads/archive-virtio</h1>
<a href="?C=N;O=D">Name</a>
<a href="/groups/virt/virtio-win/direct-downloads/">Parent Directory</a>
<a href="virtio-win-0.1.96/">virtio-win-0.1.96/</a>
<a href="virtio-win-0.1.271-1/">virtio-win-0.1.271-1/</a>
<a href="virtio-win-0.1.302-1/">virtio-win-0.1.302-1/</a>
<a href="virtio-win-0.1.285-1/">virtio-win-0.1.285-1/</a>
<a href="https://getfedora.org/">Fedora</a>
</body></html>`

// Upstream answers the stable pointer with a 301 whose Location is http://.
// The version must be read out of it and the ISO URL rebuilt, never inheriting
// the downgraded scheme.
func TestCheckStable(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stable-virtio/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Location", "http://fedorapeople.org/groups/virt/virtio-win/direct-downloads/archive-virtio/virtio-win-0.1.302-1/")
		w.WriteHeader(http.StatusMovedPermanently)
	})

	rel, err := c.CheckStable(context.Background())
	if err != nil {
		t.Fatalf("CheckStable: %v", err)
	}
	if rel.Version != "0.1.302-1" {
		t.Errorf("Version = %q, want %q", rel.Version, "0.1.302-1")
	}
	if rel.ISOVersion != "0.1.302" {
		t.Errorf("ISOVersion = %q, want %q", rel.ISOVersion, "0.1.302")
	}
	if rel.ISOFilename != "virtio-win-0.1.302.iso" {
		t.Errorf("ISOFilename = %q", rel.ISOFilename)
	}
	if !rel.IsStable {
		t.Error("IsStable = false, want true")
	}
}

func TestCheckStableRejectsNonRedirect(t *testing.T) {
	// A 200 here means Anubis served a challenge page, or upstream stopped
	// redirecting. Either way there is no version to read; fail rather than
	// parse whatever HTML came back.
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>Access Denied</html>"))
	})
	if _, err := c.CheckStable(context.Background()); err == nil {
		t.Fatal("expected an error for a 200 response, got nil")
	}
}

func TestCheckStableRejectsUnparseableLocation(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "http://example.com/somewhere/else/")
		w.WriteHeader(http.StatusFound)
	})
	if _, err := c.CheckStable(context.Background()); err == nil {
		t.Fatal("expected an error when Location carries no version, got nil")
	}
}

func TestListArchive(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/archive-virtio/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(archiveIndex))
	})

	releases, err := c.ListArchive(context.Background())
	if err != nil {
		t.Fatalf("ListArchive: %v", err)
	}
	if len(releases) != 4 {
		t.Fatalf("got %d releases, want 4: %+v", len(releases), releases)
	}
	// Absolute links and sort links in the page must not become "versions".
	for _, r := range releases {
		if !ValidVersion(r.Version) {
			t.Errorf("parsed an invalid version %q", r.Version)
		}
	}
}

// The stable redirect is a single point of failure for a job that runs
// unattended for months; a shape change upstream must degrade, not break.
func TestCheckLatestFallsBackToArchive(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stable-virtio/":
			w.WriteHeader(http.StatusInternalServerError)
		case "/archive-virtio/":
			_, _ = w.Write([]byte(archiveIndex))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	rel, err := c.CheckLatest(context.Background())
	if err != nil {
		t.Fatalf("CheckLatest: %v", err)
	}
	if rel.Version != "0.1.302-1" {
		t.Errorf("Version = %q, want the numerically newest %q", rel.Version, "0.1.302-1")
	}
	if rel.IsStable {
		t.Error("IsStable = true, but a version guessed from the archive index is not upstream's statement of stable")
	}
}

func TestCheckLatestErrorsWhenBothPathsFail(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if _, err := c.CheckLatest(context.Background()); err == nil {
		t.Fatal("expected an error when both discovery paths fail, got nil")
	}
}

func TestProbeSize(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Length", "877373440")
		w.WriteHeader(http.StatusOK)
	})

	size, err := c.ProbeSize(context.Background(), c.base+"/archive-virtio/virtio-win-0.1.302-1/virtio-win-0.1.302.iso")
	if err != nil {
		t.Fatalf("ProbeSize: %v", err)
	}
	if size != 877373440 {
		t.Errorf("size = %d, want 877373440", size)
	}
}

// A non-browser User-Agent is what gets us past Anubis; a Mozilla-shaped one
// receives a proof-of-work challenge page instead of the redirect.
func TestRequestsSendNonBrowserUserAgent(t *testing.T) {
	var got string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.Header().Set("Location", "http://x/archive-virtio/virtio-win-0.1.302-1/")
		w.WriteHeader(http.StatusMovedPermanently)
	})
	if _, err := c.CheckStable(context.Background()); err != nil {
		t.Fatalf("CheckStable: %v", err)
	}
	if got == "" {
		t.Fatal("no User-Agent sent")
	}
	if strings.Contains(got, "Mozilla") {
		t.Errorf("User-Agent %q is browser-shaped; Anubis would challenge it", got)
	}
}

// TestWithBaseTargetsAMirror covers the shape an air-gapped install actually
// gets: a copy of the upstream tree made with `wget -m -np`, which has the
// archive directory but NOT the stable-virtio/ redirect (wget follows it and
// saves the result under the archive path instead of reproducing the 301).
//
// CheckLatest has to degrade to the archive index there, and every URL it
// builds has to point at the mirror rather than at fedorapeople.
func TestWithBaseTargetsAMirror(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/archive-virtio/" {
			// Notably including /stable-virtio/ — the mirror has no redirect.
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(archiveIndex))
	}))
	t.Cleanup(srv.Close)

	base := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil))).WithBase(srv.URL)

	rel, err := base.CheckLatest(context.Background())
	if err != nil {
		t.Fatalf("CheckLatest against a mirror: %v", err)
	}
	if rel.Version != "0.1.302-1" {
		t.Errorf("Version = %q, want the newest in the index (0.1.302-1)", rel.Version)
	}
	// Not upstream's own statement of stable, so not flagged as such. Engine
	// resolution falls back to the newest release for exactly this reason.
	if rel.IsStable {
		t.Error("IsStable = true; a version guessed from the archive index must not claim it")
	}
	if !strings.HasPrefix(rel.ISOURL, srv.URL) {
		t.Errorf("ISOURL = %q, want it under the mirror %q", rel.ISOURL, srv.URL)
	}
	if !strings.HasSuffix(rel.ISOURL, "/archive-virtio/virtio-win-0.1.302-1/virtio-win-0.1.302.iso") {
		t.Errorf("ISOURL = %q, want the upstream layout below the mirror root", rel.ISOURL)
	}
}

// TestWithBaseDoesNotMutateTheOriginal: the engine derives a per-refresh client
// from one long-lived instance, so a mirror configured for one refresh must not
// leak into the shared client.
func TestWithBaseDoesNotMutateTheOriginal(t *testing.T) {
	original := NewClient(slog.New(slog.NewTextHandler(io.Discard, nil)))
	derived := original.WithBase("https://mirror.internal/virtio")

	if original.base != BaseURL {
		t.Errorf("original base = %q, want it untouched at %q", original.base, BaseURL)
	}
	if derived.base != "https://mirror.internal/virtio" {
		t.Errorf("derived base = %q", derived.base)
	}
	if derived.http != original.http {
		t.Error("derived client built a new http.Client; the connection pool should be shared")
	}
	// An empty base is "no override", not "no base at all".
	if back := original.WithBase(""); back.base != BaseURL {
		t.Errorf("WithBase(\"\") base = %q, want upstream", back.base)
	}
}
