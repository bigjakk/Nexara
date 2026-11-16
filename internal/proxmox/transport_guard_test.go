package proxmox

import (
	"context"
	"crypto/tls"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// guardedTypes are the connection-making types this package may build only
// inside the functions transportConstructorExempt names for them: a client, a
// transport, and three dialers. Each key is the type's import path and name,
// taken from the type itself, so a misspelt entry cannot quietly guard
// nothing.
var guardedTypes = func() map[string]bool {
	types := map[string]bool{}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[http.Transport](),
		reflect.TypeFor[http.Client](),
		reflect.TypeFor[net.Dialer](),
		reflect.TypeFor[tls.Dialer](),
		reflect.TypeFor[websocket.Dialer](),
	} {
		types[typ.PkgPath()+"."+typ.Name()] = true
	}
	return types
}()

// guardedUses are ready-made ways of connecting that carry none of the
// hardening, so using one at all is a fork: the default clients and dialer,
// which pin no certificate and pass no SSRF guard, and the functions that dial
// or fetch with them — or with a net.Dialer of their own — under the hood.
// These are values and functions, which reflect cannot name; the fixture
// TestGuard_SeesEveryForkedConstruction reads has to use each one and see it
// flagged, which is what keeps a misspelt entry from guarding nothing.
var guardedUses = map[string]bool{
	"net/http.DefaultClient":    true,
	"net/http.DefaultTransport": true,
	"net/http.Get":              true,
	"net/http.Head":             true,
	"net/http.Post":             true,
	"net/http.PostForm":         true,
	"net.Dial":                  true,
	"net.DialTimeout":           true,
	"net.DialTCP":               true,
	"net.DialUDP":               true,
	"net.DialIP":                true,
	"net.DialUnix":              true,
	"crypto/tls.Dial":           true,
	"crypto/tls.DialWithDialer": true,
	"github.com/gorilla/websocket.DefaultDialer": true,
}

// constructorExemption is what one function may build.
type constructorExemption struct {
	builds []string // keys of guardedTypes
	reason string
}

// transportConstructorExempt names who may build a guarded type, and which: buildHTTPClient
// owns fingerprint pinning and redirect refusal, guardedDialer the SSRF dial guard, and a
// second constructor silently forks that hardening. doMultipart Clone()s the hardened
// transport (own client for the upload timeout, refuseRedirect re-applied); consoleDialer
// reuses guardedDialer and the tls.Config. An exemption covers only the types it names.
var transportConstructorExempt = map[string]constructorExemption{
	"buildHTTPClient": {[]string{"net/http.Transport", "net/http.Client"}, "sole owner of the hardened transport"},
	"doMultipart":     {[]string{"net/http.Client"}, "clones the hardened transport; own client only to drop the upload timeout"},
	"guardedDialer":   {[]string{"net.Dialer"}, "sole owner of the SSRF dial guard"},
	"consoleDialer":   {[]string{"github.com/gorilla/websocket.Dialer"}, "the console's websocket dialer; connects through guardedDialer"},
}

// guardMention is one construction of a guarded type, type declaration
// holding one, or use of a guarded value or function.
type guardMention struct {
	fn   string // the enclosing function, or "type X" / "var X" at package level
	name string // a key of guardedTypes or guardedUses
	pos  token.Position
}

// guardedMentions returns what the guard judges: a construction of a guarded type (literal,
// new(T), a variable of the type itself), a type declaration holding one by value (alias,
// defined type, embedded or plain field), and any use of a guarded value or function.
// Selectors resolve through imports by path, so an alias cannot hide one; it reads syntax,
// not types, so a guarded value reached through a variable or a return is not seen.
func guardedMentions(fset *token.FileSet, files []*ast.File) []guardMention {
	var found []guardMention
	for _, file := range files {
		imports := map[string]string{} // local name → import path
		for _, imp := range file.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			local := path.Base(p)
			if imp.Name != nil {
				local = imp.Name.Name
			}
			imports[local] = p
		}
		resolve := func(e ast.Expr) (string, bool) {
			sel, ok := e.(*ast.SelectorExpr)
			if !ok {
				return "", false
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return "", false
			}
			p, ok := imports[pkg.Name]
			if !ok {
				return "", false
			}
			return p + "." + sel.Sel.Name, true
		}
		record := func(fn string, name string, at ast.Node) {
			found = append(found, guardMention{fn: fn, name: name, pos: fset.Position(at.Pos())})
		}
		// held reports the guarded types a type declaration holds by value.
		held := func(fn string, typ ast.Expr) {
			ast.Inspect(typ, func(n ast.Node) bool {
				switch n.(type) {
				case *ast.StarExpr, *ast.FuncType, *ast.InterfaceType:
					return false
				}
				if e, ok := n.(ast.Expr); ok {
					if name, ok := resolve(e); ok && guardedTypes[name] {
						record(fn, name, e)
						return false
					}
				}
				return true
			})
		}
		visit := func(fn string, node ast.Node) {
			ast.Inspect(node, func(n ast.Node) bool {
				var built ast.Expr
				switch x := n.(type) {
				case *ast.TypeSpec:
					held(fn, x.Type)
					return false
				case *ast.CompositeLit:
					built = x.Type
				case *ast.CallExpr:
					if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "new" && len(x.Args) == 1 {
						built = x.Args[0]
					}
				case *ast.ValueSpec:
					built = x.Type
				case *ast.SelectorExpr:
					if name, ok := resolve(x); ok && guardedUses[name] {
						record(fn, name, x)
					}
				}
				if name, ok := resolve(built); ok && guardedTypes[name] {
					record(fn, name, built)
				}
				return true
			})
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					visit(d.Name.Name, d.Body)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						visit("type "+s.Name.Name, s)
					case *ast.ValueSpec:
						visit("var "+s.Names[0].Name, s)
					}
				}
			}
		}
	}
	return found
}

// judgeMentions splits mentions into the forks — every one no exemption
// allows — and the exemptions actually used, as "function name" pairs.
func judgeMentions(mentions []guardMention, exempt map[string]constructorExemption) (forks []guardMention, used map[string]bool) {
	used = map[string]bool{}
	for _, m := range mentions {
		if e, ok := exempt[m.fn]; ok && slices.Contains(e.builds, m.name) {
			used[m.fn+" "+m.name] = true
			continue
		}
		forks = append(forks, m)
	}
	return forks, used
}

// parsePackageSources parses every non-test .go file in the package.
func parsePackageSources(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	var files []*ast.File
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		files = append(files, file)
	}
	if len(files) == 0 {
		t.Fatal("no non-test .go files parsed — the guard would pass vacuously")
	}
	return fset, files
}

func TestGuard_SingleTransportConstructor(t *testing.T) {
	fset, files := parsePackageSources(t)
	forks, used := judgeMentions(guardedMentions(fset, files), transportConstructorExempt)
	for _, m := range forks {
		t.Errorf(
			"%s: %s builds, declares or uses %s directly.\n"+
				"Connections in this package come from buildHTTPClient (TLS pinning, redirect\n"+
				"refusal), guardedDialer (the SSRF dial guard) and consoleDialer (the console's\n"+
				"websocket dialer, built on both). A second way of connecting forks that\n"+
				"hardening. If this is genuinely a special case, add it to\n"+
				"transportConstructorExempt with a reason.",
			m.pos, m.fn, m.name,
		)
	}

	// Every exemption has to be in use, type by type. One that no longer is
	// has stopped being a review surface — and is room for a fork to appear
	// under a name the list already waves through. An exempt function that
	// does not exist at all is TestGuard_ExemptConstructorsExist's to report.
	declared := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				declared[fn.Name.Name] = true
			}
		}
	}
	for fn, e := range transportConstructorExempt {
		for _, name := range e.builds {
			if !guardedTypes[name] {
				t.Errorf("transportConstructorExempt[%q] names %q, which is not a guarded type", fn, name)
				continue
			}
			if declared[fn] && !used[fn+" "+name] {
				t.Errorf("transportConstructorExempt[%q] allows %s, which it no longer builds — remove it from the entry", fn, name)
			}
		}
	}
}

// TestGuard_ExemptConstructorsExist fails when an entry in
// transportConstructorExempt no longer names a real function. An exemption list
// that drifts stops being a review surface.
func TestGuard_ExemptConstructorsExist(t *testing.T) {
	_, files := parsePackageSources(t)
	found := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				found[fn.Name.Name] = true
			}
		}
	}
	for name, e := range transportConstructorExempt {
		if !found[name] {
			t.Errorf("transportConstructorExempt names %q (%q) but no such function exists — remove the stale entry", name, e.reason)
		}
	}
}

// forkedSource is a file the guard has to read correctly: every shape of
// fork, under aliased imports, beside every exemption used as it is allowed
// to be and constructs that build nothing.
const forkedSource = `package fixture

import (
	stdtls "crypto/tls"
	gws "github.com/gorilla/websocket"
	"net"
	nethttp "net/http"
)

var packageLevelDialer = &net.Dialer{}

type aliasedDialer = gws.Dialer

type definedClient nethttp.Client

type embedsATransport struct{ nethttp.Transport }

type holdsATLSDialer struct{ d stdtls.Dialer }

type holdsAPointerOnly struct{ c *nethttp.Client }

type takesAClient func(nethttp.Client)

func buildHTTPClient() (*nethttp.Client, *nethttp.Transport) {
	return &nethttp.Client{}, &nethttp.Transport{}
}

func doMultipart(rt nethttp.RoundTripper) *nethttp.Client {
	_ = rt.(*nethttp.Transport)
	return &nethttp.Client{}
}

func guardedDialer() *net.Dialer { return &net.Dialer{} }

func consoleDialer() *gws.Dialer {
	_ = &net.Dialer{}
	return &gws.Dialer{}
}

func forksADialer() { _ = (&net.Dialer{}).DialContext }

func forksATLSDialer() { _ = &stdtls.Dialer{} }

func forksAWebsocketDialer() { _ = gws.Dialer{} }

func declaresALocalAlias() {
	type d = gws.Dialer
	_ = d{}
}

func usesTheDefaultDialer() { _ = gws.DefaultDialer }

func usesTheDefaultClient() { _ = nethttp.DefaultClient }

func usesTheDefaultTransport() { _ = nethttp.DefaultTransport }

func fetches() {
	_, _ = nethttp.Get("https://192.0.2.10")
	_, _ = nethttp.Head("https://192.0.2.10")
	_, _ = nethttp.Post("https://192.0.2.10", "", nil)
	_, _ = nethttp.PostForm("https://192.0.2.10", nil)
}

func dials() {
	_, _ = net.Dial("tcp", "192.0.2.10:8006")
	_, _ = net.DialTimeout("tcp", "192.0.2.10:8006", 0)
	_, _ = net.DialTCP("tcp", nil, nil)
	_, _ = net.DialUDP("udp", nil, nil)
	_, _ = net.DialIP("ip4:1", nil, nil)
	_, _ = net.DialUnix("unix", nil, nil)
	_, _ = stdtls.Dial("tcp", "192.0.2.10:8006", nil)
	_, _ = stdtls.DialWithDialer(nil, "tcp", "192.0.2.10:8006", nil)
}

func declaresATransport() { var t nethttp.Transport; _ = &t }

func newsAClient() { _ = new(nethttp.Client) }

func holdsAPointerVariable() { var c *nethttp.Client; _ = c }
`

// TestGuard_SeesEveryForkedConstruction is the positive control for
// TestGuard_SingleTransportConstructor: over forkedSource the guard has to
// report exactly the forks in it — each shape, including through an aliased
// import, at package level, in a type declaration, and an exempt function
// building a type its exemption does not name — and nothing that builds
// nothing. It also has to find every guarded name at least once, so no entry
// in guardedTypes or guardedUses can stop matching without this failing.
// Without it, a guard that had stopped seeing a shape would pass the real
// package quietly.
func TestGuard_SeesEveryForkedConstruction(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", forkedSource, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	forks, used := judgeMentions(guardedMentions(fset, []*ast.File{file}), transportConstructorExempt)

	got := make([]string, 0, len(forks))
	seen := map[string]bool{}
	for _, m := range forks {
		got = append(got, m.fn+" "+m.name)
		seen[m.name] = true
	}
	slices.Sort(got)
	want := []string{
		"consoleDialer net.Dialer",
		"declaresALocalAlias github.com/gorilla/websocket.Dialer",
		"declaresATransport net/http.Transport",
		"dials crypto/tls.Dial",
		"dials crypto/tls.DialWithDialer",
		"dials net.Dial",
		"dials net.DialIP",
		"dials net.DialTCP",
		"dials net.DialTimeout",
		"dials net.DialUDP",
		"dials net.DialUnix",
		"fetches net/http.Get",
		"fetches net/http.Head",
		"fetches net/http.Post",
		"fetches net/http.PostForm",
		"forksADialer net.Dialer",
		"forksATLSDialer crypto/tls.Dialer",
		"forksAWebsocketDialer github.com/gorilla/websocket.Dialer",
		"newsAClient net/http.Client",
		"type aliasedDialer github.com/gorilla/websocket.Dialer",
		"type definedClient net/http.Client",
		"type embedsATransport net/http.Transport",
		"type holdsATLSDialer crypto/tls.Dialer",
		"usesTheDefaultClient net/http.DefaultClient",
		"usesTheDefaultDialer github.com/gorilla/websocket.DefaultDialer",
		"usesTheDefaultTransport net/http.DefaultTransport",
		"var packageLevelDialer net.Dialer",
	}
	if !slices.Equal(got, want) {
		t.Errorf("forks = %q\nwant    %q", got, want)
	}
	for name := range guardedTypes {
		if !seen[name] {
			t.Errorf("the fixture never shows the guard catching a fork of guarded type %s", name)
		}
	}
	for name := range guardedUses {
		if !seen[name] {
			t.Errorf("the fixture never shows the guard catching a use of %s — a misspelt entry would look the same", name)
		}
	}
	for fn, e := range transportConstructorExempt {
		for _, name := range e.builds {
			if !used[fn+" "+name] {
				t.Errorf("the allowed %s in %s was not credited to its exemption", name, fn)
			}
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
