package api

import (
	"bufio"
	"cmp"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// TestStatusTextSlugs pins every slug statusText sends, one status at a time,
// and the fallback for a status it does not list.
func TestStatusTextSlugs(t *testing.T) {
	for code, want := range map[int]string{
		400: "bad_request",
		401: "unauthorized",
		403: "forbidden",
		404: "not_found",
		405: "method_not_allowed",
		408: "request_timeout",
		409: "conflict",
		411: "length_required",
		412: "precondition_failed",
		413: "request_entity_too_large",
		415: "unsupported_media_type",
		422: "unprocessable_entity",
		426: "upgrade_required",
		429: "too_many_requests",
		431: "request_header_fields_too_large",
		501: "not_implemented",
		502: "bad_gateway",
		503: "service_unavailable",
		// Nothing the API sends today; each falls back.
		500: "internal_server_error",
		504: "internal_server_error",
		599: "internal_server_error",
	} {
		if got := statusText(code); got != want {
			t.Errorf("statusText(%d) = %q, want %q", code, got, want)
		}
	}
}

// statusNamesTheAPISends is every status name the API's source refers to —
// fiber.StatusX, fiber.ErrX, net/http's StatusX — with the status it stands
// for. TestGuard_EveryErrorStatusTheAPISendsHasASlug holds it to the source
// in both directions, so it is exactly the set of statuses the code can
// name, and the success statuses are here only so that every name is
// accounted for.
var statusNamesTheAPISends = map[string]int{
	"StatusOK":        fiber.StatusOK,
	"StatusCreated":   fiber.StatusCreated,
	"StatusAccepted":  fiber.StatusAccepted,
	"StatusNoContent": fiber.StatusNoContent,
	"StatusFound":     fiber.StatusFound,

	"StatusBadRequest":           fiber.StatusBadRequest,
	"StatusUnauthorized":         fiber.StatusUnauthorized,
	"StatusForbidden":            fiber.StatusForbidden,
	"StatusNotFound":             fiber.StatusNotFound,
	"StatusConflict":             fiber.StatusConflict,
	"StatusLengthRequired":       fiber.StatusLengthRequired,
	"StatusPreconditionFailed":   fiber.StatusPreconditionFailed,
	"StatusUnsupportedMediaType": fiber.StatusUnsupportedMediaType,
	"StatusUnprocessableEntity":  fiber.StatusUnprocessableEntity,
	"StatusTooManyRequests":      fiber.StatusTooManyRequests,
	"StatusInternalServerError":  fiber.StatusInternalServerError,
	"StatusBadGateway":           fiber.StatusBadGateway,
	"StatusServiceUnavailable":   fiber.StatusServiceUnavailable,
	"ErrRequestEntityTooLarge":   fiber.ErrRequestEntityTooLarge.Code,
	"ErrUpgradeRequired":         fiber.ErrUpgradeRequired.Code,
}

// statusesFiberSendsThroughErrorHandler are the error statuses Fiber hands
// errorHandler on its own, which no Nexara source names. Read off Fiber
// v3.5.0 — the router in router.go (App.next) and serverErrorHandler in
// app.go, which turns a request fasthttp could not read into a *fiber.Error.
// TestFiberSentErrorStatusesCarryTheirSlug sends each one it can for real.
//
// One of serverErrorHandler's is left out because this server cannot send it:
// the 413 it makes of fasthttp.ErrBodyTooLarge never comes, since
// StreamRequestBody streams an oversized body instead of refusing it. Its 408
// is in: buildFiberConfig's ReadTimeout (headReadTimeout) expires on a head, or
// on fasthttp's read-ahead of a body, that does not arrive in time. The wait
// between requests is bounded by IdleTimeout instead, and Server.serveConn
// closes a connection that times out there without a response.
var statusesFiberSendsThroughErrorHandler = map[int]string{
	fiber.StatusBadRequest:                  "serverErrorHandler: a request fasthttp cannot parse",
	fiber.StatusNotFound:                    "the router: no route matches the path",
	fiber.StatusRequestTimeout:              "serverErrorHandler: a head or read-ahead the read deadline cut short",
	fiber.StatusMethodNotAllowed:            "the router: a route matches the path but not the method",
	fiber.StatusRequestHeaderFieldsTooLarge: "serverErrorHandler: a head larger than ReadBufferSize",
	fiber.StatusNotImplemented:              "serverErrorHandler: a method with a byte outside the token set",
	fiber.StatusBadGateway:                  "serverErrorHandler: a network error while reading the request",
}

// dynamicStatusSites are the functions that pass fiber.NewError a status
// they did not name — each re-raises one that mapProxmoxError or its caller
// chose from the named constants above, so the named constants cover what
// they can send. A new site is refused until someone decides the same of it.
var dynamicStatusSites = map[string]string{
	"mapNamedOpError":      "keeps the status mapProxmoxError chose",
	"mapProxmoxDieError":   "sends the status its caller names: fiber.StatusNotFound or fiber.StatusConflict",
	"describeUnverifiable": "keeps the status mapProxmoxError chose",
}

// statusRef is one place the source names or passes a status.
type statusRef struct{ name, where string }

// statusSource is what scanStatusSources found.
type statusSource struct {
	files    int
	names    []statusRef // fiber.StatusX, fiber.ErrX, net/http StatusX
	literals []statusRef // an integer literal passed as a status
	dynamic  []statusRef // fiber.NewError with a status that is neither; name is the enclosing function
}

var (
	fiberStatusNameRe = regexp.MustCompile(`^(Status|Err)[A-Z]`)
	httpStatusNameRe  = regexp.MustCompile(`^Status[A-Z]`)
)

// scanStatusSources parses every non-test Go file under dirs and reports each
// status the code names, each integer literal it passes as a status, and each
// fiber.NewError whose status is computed.
func scanStatusSources(t *testing.T, dirs ...string) statusSource {
	t.Helper()
	var src statusSource
	fset := token.NewFileSet()
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			src.files++
			scanStatusFile(fset, f, &src)
			return nil
		})
		if err != nil {
			t.Fatalf("scanning %s: %v", dir, err)
		}
	}
	return src
}

func scanStatusFile(fset *token.FileSet, f *ast.File, src *statusSource) {
	fiberName, httpName := "", ""
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch path {
		case "github.com/gofiber/fiber/v3":
			fiberName = cmp.Or(name, "fiber")
		case "net/http":
			httpName = cmp.Or(name, "http")
		}
	}
	isPkg := func(e ast.Expr, pkg string) bool {
		id, ok := e.(*ast.Ident)
		return ok && pkg != "" && id.Name == pkg
	}
	isNamedStatus := func(e ast.Expr) bool {
		sel, ok := e.(*ast.SelectorExpr)
		return ok && ((isPkg(sel.X, fiberName) && fiberStatusNameRe.MatchString(sel.Sel.Name)) ||
			(isPkg(sel.X, httpName) && httpStatusNameRe.MatchString(sel.Sel.Name)))
	}
	where := func(n ast.Node) string {
		p := fset.Position(n.Pos())
		return fmt.Sprintf("%s:%d", p.Filename, p.Line)
	}

	for _, decl := range f.Decls {
		enclosing := ""
		if fd, ok := decl.(*ast.FuncDecl); ok {
			enclosing = fd.Name.Name
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				if isNamedStatus(n) {
					src.names = append(src.names, statusRef{n.Sel.Name, where(n)})
				}
			case *ast.CallExpr:
				fun, ok := n.Fun.(*ast.SelectorExpr)
				if !ok || len(n.Args) == 0 {
					return true
				}
				isNewError := isPkg(fun.X, fiberName) && fun.Sel.Name == "NewError"
				if !isNewError && fun.Sel.Name != "Status" && fun.Sel.Name != "SendStatus" {
					return true
				}
				arg := n.Args[0]
				if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.INT {
					src.literals = append(src.literals, statusRef{lit.Value, where(lit)})
				} else if isNewError && !isNamedStatus(arg) {
					src.dynamic = append(src.dynamic, statusRef{enclosing, where(arg)})
				}
			}
			return true
		})
	}
}

// TestGuard_EveryErrorStatusTheAPISendsHasASlug is what keeps a status from
// going out as internal_server_error again. It reads the API's source —
// internal/api, its handlers, and internal/ws, whose routes share the app and
// its errorHandler — for every status the code names, and requires:
//
//   - every name to be in statusNamesTheAPISends, so a status nobody has
//     looked at cannot slip in, and every entry there to still be named
//     somewhere, so the table cannot go stale; together they also make the
//     scan fail loudly if it stops finding anything;
//   - no integer literal to be passed as a status, which the scan could not
//     tell apart from any other number;
//   - every fiber.NewError with a computed status to be one of
//     dynamicStatusSites, whose statuses come from the named ones; and
//   - every error status in the table, and every one Fiber sends through
//     errorHandler by itself, to have a slug of its own.
//
// A status written with c.Status(…).JSON(…) carries the handler's own body
// rather than statusText's slug; it is held to the same rule anyway, because
// "a status the API sends has a slug" is simpler to keep than an exception
// for the ones that do not need it today.
func TestGuard_EveryErrorStatusTheAPISendsHasASlug(t *testing.T) {
	src := scanStatusSources(t, ".", filepath.Join("..", "ws"))
	if src.files < 100 {
		t.Fatalf("precondition: scanned %d files; internal/api and internal/ws hold far more", src.files)
	}

	named := map[string]string{}
	for _, ref := range src.names {
		if _, known := statusNamesTheAPISends[ref.name]; !known {
			t.Errorf("%s names %s, which statusNamesTheAPISends does not list: add it with its status, "+
				"and if it is an error, give statusText a case for it", ref.where, ref.name)
		}
		named[ref.name] = ref.where
	}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range statusNamesTheAPISends {
			if !yield(n) {
				return
			}
		}
	}) {
		if _, ok := named[name]; !ok {
			t.Errorf("statusNamesTheAPISends lists %s, which no source names any more (or the scan found "+
				"nothing): drop the entry, or fix the scan", name)
		}
	}

	for _, lit := range src.literals {
		t.Errorf("%s passes the status %s as a literal: use Fiber's named constant, so this guard can see it",
			lit.where, lit.name)
	}

	sites := map[string]bool{}
	for _, d := range src.dynamic {
		if _, ok := dynamicStatusSites[d.name]; !ok {
			t.Errorf("%s: %s passes fiber.NewError a computed status; if it can be anything but a status "+
				"the source names, statusText needs a case for it — then add the function to dynamicStatusSites",
				d.where, cmp.Or(d.name, "(package level)"))
		}
		sites[d.name] = true
	}
	for name := range dynamicStatusSites {
		if !sites[name] {
			t.Errorf("dynamicStatusSites lists %s, which no longer passes fiber.NewError a computed status", name)
		}
	}

	reasons := map[int][]string{}
	for name, code := range statusNamesTheAPISends {
		reasons[code] = append(reasons[code], "named as "+name+" at "+named[name])
	}
	for code, why := range statusesFiberSendsThroughErrorHandler {
		reasons[code] = append(reasons[code], "sent by Fiber — "+why)
	}
	codes := map[int]string{}
	for code, why := range reasons {
		slices.Sort(why)
		codes[code] = strings.Join(why, "; ")
	}
	for _, code := range slices.Sorted(func(yield func(int) bool) {
		for c := range codes {
			if !yield(c) {
				return
			}
		}
	}) {
		if code < 400 || code == fiber.StatusInternalServerError {
			continue
		}
		if slug := statusText(code); slug == "internal_server_error" {
			t.Errorf("status %d (%s) goes out as internal_server_error: give statusText a case for it", code, codes[code])
		}
	}
}

// TestFiberSentErrorStatusesCarryTheirSlug sends, over a real TCP connection
// to the assembled server, one request for each error status that reaches
// errorHandler without any Nexara code naming it — Fiber's router and its
// serverErrorHandler — plus the WebSocket gate's 426, which reaches it as
// Fiber's own sentinel. Each is what puts its status in
// statusesFiberSendsThroughErrorHandler, or keeps it out of internal_server_error.
//
// The server's ReadTimeout is shortened, so that a head the client stops
// sending is cut in a second rather than a minute; every other request here
// goes out whole at once.
func TestFiberSentErrorStatusesCarryTheirSlug(t *testing.T) {
	s := newAssembledServer(t)
	s.app.Server().ReadTimeout = time.Second
	addr := serveOnLoopback(t, s)

	for _, tt := range []struct {
		name string
		raw  string
		want int
	}{
		{"a method the route does not take", "PUT /api/v1/version HTTP/1.1\r\nHost: example.com\r\nContent-Length: 0\r\n\r\n",
			fiber.StatusMethodNotAllowed},
		{"a WebSocket route asked for without an upgrade", "GET /ws HTTP/1.1\r\nHost: example.com\r\n\r\n",
			fiber.StatusUpgradeRequired},
		{"a head larger than ReadBufferSize", "GET /api/v1/version HTTP/1.1\r\nHost: example.com\r\nX-Pad: " +
			strings.Repeat("p", 20<<10) + "\r\n\r\n", fiber.StatusRequestHeaderFieldsTooLarge},
		{"a method with a byte outside the token set", "G@T /api/v1/version HTTP/1.1\r\nHost: example.com\r\n\r\n",
			fiber.StatusNotImplemented},
		{"a head the client stops sending", "GET /api/v1/version HTTP/1.1\r\nHost: exa", fiber.StatusRequestTimeout},
	} {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := net.Dial("tcp", addr)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer func() { _ = conn.Close() }()
			if _, err := conn.Write([]byte(tt.raw)); err != nil {
				t.Fatalf("write: %v", err)
			}
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatalf("set read deadline: %v", err)
			}
			res, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			raw, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			var env ErrorResponse
			if err := json.Unmarshal(raw, &env); err != nil {
				t.Fatalf("status %d, body %.200q is not errorHandler's envelope: %v", res.StatusCode, raw, err)
			}
			if res.StatusCode != tt.want || env.Error != statusText(tt.want) || env.Error == "internal_server_error" {
				t.Errorf("status %d, error %q; want %d with its own slug", res.StatusCode, env.Error, tt.want)
			}
		})
	}
}
