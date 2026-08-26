package proxmox

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// transportConstructorExempt names the functions allowed to build an
// http.Transport or http.Client. Everything else in this package must go
// through buildHTTPClient.
//
// buildHTTPClient is the sole owner of the SSRF dial guard, TLS fingerprint
// pinning (with session tickets disabled), and redirect refusal. A second
// constructor anywhere in this package silently forks that hardening: the next
// person to tighten one copy has no way to know the other exists, and the
// forked client is the one that talks to a user-supplied URL.
//
// doMultipart is exempt because it does NOT build a fresh transport — it
// Clone()s the hardened one to widen the write buffer for uploads, so it
// inherits every setting by construction. It still needs its own http.Client
// to drop the request timeout for long uploads, and it re-applies
// refuseRedirect explicitly.
var transportConstructorExempt = map[string]string{
	"buildHTTPClient": "sole owner of the hardened transport",
	"doMultipart":     "clones the hardened transport; own client only to drop the upload timeout",
}

func TestGuard_SingleTransportConstructor(t *testing.T) {
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no .go files found — the guard would pass vacuously")
	}

	var checked int
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		checked++

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if _, exempt := transportConstructorExempt[fn.Name.Name]; exempt {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "http" {
					return true
				}
				if sel.Sel.Name != "Transport" && sel.Sel.Name != "Client" {
					return true
				}
				t.Errorf(
					"%s:%d: %s builds an http.%s directly.\n"+
						"Use buildHTTPClient — it owns the SSRF dial guard, TLS fingerprint\n"+
						"pinning and redirect refusal. A second constructor forks that hardening.\n"+
						"If this is genuinely a special case, add it to transportConstructorExempt\n"+
						"with a reason.",
					path, fset.Position(lit.Pos()).Line, fn.Name.Name, sel.Sel.Name,
				)
				return true
			})
		}
	}

	if checked == 0 {
		t.Fatal("no non-test .go files parsed — the guard would pass vacuously")
	}
}

// TestGuard_ExemptConstructorsExist fails when an entry in
// transportConstructorExempt no longer names a real function. An exemption list
// that drifts stops being a review surface.
func TestGuard_ExemptConstructorsExist(t *testing.T) {
	fset := token.NewFileSet()
	paths, _ := filepath.Glob("*.go")

	found := map[string]bool{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				found[fn.Name.Name] = true
			}
		}
	}

	for name, reason := range transportConstructorExempt {
		if !found[name] {
			t.Errorf("transportConstructorExempt names %q (%q) but no such function exists — remove the stale entry", name, reason)
		}
	}
}

// TestTokenAuthRequestsUnchanged pins the wire format of a token-authenticated
// request across the requestAuth refactor: the Authorization header must be
// byte-identical to what this package has always sent, and no Cookie or CSRF
// header may leak onto it.
func TestTokenAuthRequestsUnchanged(t *testing.T) {
	type captured struct {
		auth   string
		cookie string
		csrf   string
	}
	var got captured

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = captured{
			auth:   r.Header.Get("Authorization"),
			cookie: r.Header.Get("Cookie"),
			csrf:   r.Header.Get("CSRFPreventionToken"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(ClientConfig{
		BaseURL:     srv.URL,
		TokenID:     "user@pam!test",
		TokenSecret: "secret-token-value",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// Exercise both a GET and a POST — ticketAuth branches on method, so a
	// regression that swapped the strategy would show on one but not the other.
	var dst struct{}
	if err := c.do(context.Background(), "/version", &dst); err != nil {
		t.Fatalf("do: %v", err)
	}
	assertTokenOnly(t, "GET", got.auth, got.cookie, got.csrf)

	if err := c.doPost(context.Background(), "/version", nil, &dst); err != nil {
		t.Fatalf("doPost: %v", err)
	}
	assertTokenOnly(t, "POST", got.auth, got.cookie, got.csrf)
}

func assertTokenOnly(t *testing.T, method, auth, cookie, csrf string) {
	t.Helper()
	const want = "PVEAPIToken=user@pam!test=secret-token-value"
	if auth != want {
		t.Errorf("%s Authorization = %q, want %q", method, auth, want)
	}
	if cookie != "" {
		t.Errorf("%s leaked a Cookie header: %q", method, cookie)
	}
	if csrf != "" {
		t.Errorf("%s leaked a CSRFPreventionToken header: %q", method, csrf)
	}
}

// TestTicketAuthAppliesCSRFOnlyToWrites pins the method-dependent half of
// ticketAuth: the cookie rides every request, the CSRF token only rides the
// state-changing ones. PVE rejects a cookie-authenticated write that lacks the
// CSRF header, and sending it on reads is harmless but wrong.
func TestTicketAuthAppliesCSRFOnlyToWrites(t *testing.T) {
	auth := ticketAuth{ticket: "PVE:root@pam:TICKET", csrf: "CSRF-VALUE"}

	tests := []struct {
		method   string
		wantCSRF bool
	}{
		{http.MethodGet, false},
		{http.MethodHead, false},
		{http.MethodOptions, false},
		{http.MethodPost, true},
		{http.MethodPut, true},
		{http.MethodDelete, true},
	}

	for _, tc := range tests {
		t.Run(tc.method, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, "https://pve.example/api2/json/version", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			auth.apply(req)

			cookie, err := req.Cookie("PVEAuthCookie")
			if err != nil {
				t.Fatalf("PVEAuthCookie missing: %v", err)
			}
			if cookie.Value != "PVE:root@pam:TICKET" {
				t.Errorf("cookie = %q, want the ticket", cookie.Value)
			}

			gotCSRF := req.Header.Get("CSRFPreventionToken")
			if tc.wantCSRF && gotCSRF != "CSRF-VALUE" {
				t.Errorf("CSRFPreventionToken = %q, want it set on a write", gotCSRF)
			}
			if !tc.wantCSRF && gotCSRF != "" {
				t.Errorf("CSRFPreventionToken = %q, want it absent on a read", gotCSRF)
			}

			if got := req.Header.Get("Authorization"); got != "" {
				t.Errorf("ticket auth must not set Authorization, got %q", got)
			}
		})
	}
}

// TestNoCookieJar pins that the shared client keeps Jar nil. A jar would
// outlive the request and could re-attach a PVE ticket to a host it was never
// minted for.
func TestNoCookieJar(t *testing.T) {
	c, err := NewClient(ClientConfig{
		BaseURL:     "https://pve.example",
		TokenID:     "user@pam!test",
		TokenSecret: "secret",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.httpClient.Jar != nil {
		t.Error("http.Client.Jar must stay nil so tickets cannot outlive their request")
	}
}

// TestNoAuthSendsNoCredentials pins the strategy used for the one
// unauthenticated call this package makes, POST /access/ticket. That endpoint
// sets `allowtoken => 0` in the PVE source, so sending a token there is not
// merely useless — it is a credential on a request that cannot use it.
func TestNoAuthSendsNoCredentials(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "https://pve.example/api2/json/access/ticket", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	noAuth{}.apply(req)

	for _, h := range []string{"Authorization", "Cookie", "CSRFPreventionToken"} {
		if got := req.Header.Get(h); got != "" {
			t.Errorf("noAuth set %s = %q, want it absent", h, got)
		}
	}
}
