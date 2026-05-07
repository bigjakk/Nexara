package scanner

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewScannerHTTPClient_RefusesRedirects(t *testing.T) {
	t.Parallel()
	// A test server that 302s to a different host. The client must
	// surface the 3xx as a response (because CheckRedirect returned
	// http.ErrUseLastResponse) rather than following it.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://attacker.example/")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	client := newScannerHTTPClient(5 * time.Second)
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("expected no transport error, got %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("expected 302 surfaced to caller, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "https://attacker.example/" {
		t.Fatalf("expected Location preserved for caller, got %q", loc)
	}
}

func TestCheckUpstreamStatus_OK(t *testing.T) {
	t.Parallel()
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}}
	if err := checkUpstreamStatus(resp); err != nil {
		t.Fatalf("expected nil for 200, got %v", err)
	}
}

func TestCheckUpstreamStatus_RedirectIsTyped(t *testing.T) {
	t.Parallel()
	resp := &http.Response{StatusCode: http.StatusFound, Header: http.Header{}}
	resp.Header.Set("Location", "https://elsewhere.example/")
	err := checkUpstreamStatus(resp)
	if err == nil {
		t.Fatal("expected error for 302")
	}
	if !errors.Is(err, errUnexpectedRedirect) {
		t.Fatalf("expected errUnexpectedRedirect, got %v", err)
	}
	if !strings.Contains(err.Error(), "elsewhere.example") {
		t.Fatalf("expected Location in error, got %q", err.Error())
	}
}

func TestCheckUpstreamStatus_RedirectNoLocation(t *testing.T) {
	t.Parallel()
	resp := &http.Response{StatusCode: http.StatusMovedPermanently, Header: http.Header{}}
	err := checkUpstreamStatus(resp)
	if err == nil {
		t.Fatal("expected error for 301 without Location")
	}
	if !errors.Is(err, errUnexpectedRedirect) {
		t.Fatalf("expected errUnexpectedRedirect, got %v", err)
	}
}

func TestCheckUpstreamStatus_4xx5xx(t *testing.T) {
	t.Parallel()
	for _, code := range []int{http.StatusForbidden, http.StatusInternalServerError, http.StatusBadGateway} {
		resp := &http.Response{StatusCode: code, Header: http.Header{}}
		err := checkUpstreamStatus(resp)
		if err == nil {
			t.Errorf("expected error for status %d", code)
			continue
		}
		if errors.Is(err, errUnexpectedRedirect) {
			t.Errorf("status %d should not be classified as redirect", code)
		}
	}
}
