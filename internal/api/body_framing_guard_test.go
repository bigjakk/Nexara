package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestGuard_NothingDetachesAnUnreadRequestBody holds, over every non-test Go
// file of every package in this module — every one `go list ./...` reports,
// whether anything imports it or not, as the release image builds it:
// CGO_ENABLED=0, GOOS=linux, GOARCH=amd64, no build tags, the way
// docker/nexara/Dockerfile runs go build — what closeConnectionsLeftMidBody
// and bodyMayBeLeftUnread cannot check at run time. It reads the code as the
// compiler does: each package is type-checked (go/types, against the export
// data `go list -export` names), and a rule matches the function, method or
// field a name resolves to — whatever the receiver is called, however the
// value got there, and whether it is called, taken as a method value, named in
// a method expression, reached through an embedded field, or called on an
// interface or type parameter that *fasthttp.Request or
// *fasthttp.RequestHeader satisfies. It refuses:
//
//   - any reference to a method of *fasthttp.Request that releases, replaces,
//     reads a request over, or writes out the body (requestMethodsRefused):
//     bodyMayBeLeftUnread would read a body such a method let go of, with its
//     rest still on the connection, as one read to its end;
//   - any reference to a method of *fasthttp.RequestHeader that rewrites the
//     framing, or the path, method or Content-Type the upload exemption is
//     decided by (headerMethodsRefused), and any call of a keyed header
//     mutator (headerMethodsKeyed) whose key is Content-Length,
//     Transfer-Encoding or Content-Type, or is not a constant it can read — a
//     method value or method expression of one included;
//   - any assignment (=) over a fasthttp.Request, RequestHeader or Server
//     value, and any composite literal of one;
//   - any assignment to fasthttp.Server's Handler, ContinueHandler,
//     HeaderReceived or ErrorHandler field, or taking one's address: New sets
//     them, and nothing else may replace or bypass the wrapper and the
//     redaction it installs there;
//   - anything that rewrites the path or method a request is routed by after
//     closeConnectionsLeftMidBody decided whether its body is the storage
//     upload's: Fiber's Ctx.Path and Ctx.Method (and Req.Method) given an
//     argument, or referred to without being called, and Ctx.Reset, all of
//     which re-route the request; fasthttp's own path and method writers
//     (uriMethodsRefused — on every URI, the request's or not, as its comment
//     says — and the SetRequestURI, SetURI and SetMethod methods among the
//     request's and the header's); and Fiber's rewrite middleware,
//     which is built on Ctx.Path. The Content-Type writers above are refused
//     for the same reason: the exemption is decided by all three;
//   - any reference to what serves Fiber without the server New configured —
//     (*fiber.App).Handler, the adaptor's FiberApp, FiberHandler and
//     FiberHandlerFunc, and fasthttp's own Serve… and ListenAndServe…, which
//     build a default server around the handler they are given, dropping the
//     timeouts, the redaction and the upload rules — or swaps or suppresses
//     the answer the close rides on
//     — RequestCtx.HijackSetNoResponse and TimeoutError…,
//     fasthttp.TimeoutHandler and TimeoutWithCodeHandler — or hands the live
//     request elsewhere: RequestCtx.Init and Init2, fasthttp.ReleaseRequest,
//     every fasthttp function and method named Do or Do and a capitalised
//     word (the clients' Do, DoTimeout, DoDeadline, DoRedirects), and
//     RequestCtx.Conn, under which a deadline or a read would reach past the
//     body's stream;
//   - importing Fiber's timeout, proxy or rewrite middleware.
//
// What it cannot see, and does not claim to: code outside this module (a
// library handed the request may do any of this), test files, reflection and
// unsafe, and a request converted to an interface value that something else
// then calls a refused method on — fmt calling String for a %v, say; the call
// happens in the standard library, not here.
func TestGuard_NothingDetachesAnUnreadRequestBody(t *testing.T) {
	prog := loadBodyGuardProgram(t)
	if prog.files < 300 {
		t.Fatalf("precondition: scanned %d files; the module holds far more", prog.files)
	}
	for _, dir := range []string{"internal/", "cmd/", "pkg/", "migrations/"} {
		if !slices.ContainsFunc(prog.pkgs, func(p bodyGuardPackage) bool {
			return slices.ContainsFunc(p.names, func(n string) bool { return strings.HasPrefix(n, dir) })
		}) {
			t.Fatalf("precondition: no package under %s was scanned", dir)
		}
	}
	// Every package in the module is scanned, whether anything imports it or
	// not: the listing above is asked again, on its own, as `go list ./...`
	// reports it in the same build context.
	all, err := listModulePackages()
	if err != nil {
		t.Fatalf("listing the module's packages: %v", err)
	}
	if len(all) < 30 {
		t.Fatalf("precondition: go list ./... reports %d packages; the module holds more", len(all))
	}
	for _, path := range all {
		if !slices.ContainsFunc(prog.pkgs, func(p bodyGuardPackage) bool { return p.path == path }) {
			t.Errorf("%s is a package of this module, and the guard did not scan it", path)
		}
	}

	findings := make([]bodyGuardFinding, 0, len(prog.pkgs))
	for _, p := range prog.pkgs {
		findings = append(findings, scanForBodyDetaching(prog.fset, p.files, p.names, p.info, prog.rules)...)
	}
	matched := map[string]bool{}
	for _, f := range findings {
		if _, ok := bodyGuardAllowed[f.key()]; ok {
			matched[f.key()] = true
			continue
		}
		t.Errorf("%s:%d (%s) %s — %s. See TestGuard_NothingDetachesAnUnreadRequestBody for why it is refused, and "+
			"bodyGuardAllowed if it is meant", f.file, f.line, f.function, f.what, f.why)
	}
	for _, key := range slices.Sorted(maps.Keys(bodyGuardAllowed)) {
		if !matched[key] {
			t.Errorf("bodyGuardAllowed lists %s, which matches nothing any more: drop it", key)
		}
	}
}

// requestMethodsRefused are the methods of *fasthttp.Request the guard
// refuses, each with what it does to the body.
var requestMethodsRefused = map[string]string{
	"AppendBody":             "appends to the body, releasing its stream unread",
	"AppendBodyString":       "appends to the body, releasing its stream unread",
	"BodyWriteTo":            "copies the body's stream out and releases it — unread past a write that fails",
	"BodyWriter":             "hands out a writer that appends to the body, releasing its stream unread",
	"CloseBodyStream":        "releases the body's stream unread",
	"ContinueReadBody":       "reads a body into the request from a reader of its caller's",
	"ContinueReadBodyStream": "reads a body into the request from a reader of its caller's",
	"CopyTo":                 "resets the request it copies to, releasing that one's stream unread",
	"SetRequestURI":          "rewrites the path the request is routed by",
	"SetRequestURIBytes":     "rewrites the path the request is routed by",
	"SetURI":                 "rewrites the path the request is routed by",
	"Read":                   "reads a whole request over this one",
	"ReadBody":               "reads a body into the request from a reader of its caller's",
	"ReadLimitBody":          "reads a whole request over this one",
	"ReleaseBody":            "releases the body, and its stream unread",
	"Reset":                  "clears the request, releasing its stream unread",
	"ResetBody":              "clears the body, releasing its stream unread",
	"SetBody":                "sets the body, releasing its stream unread",
	"SetBodyRaw":             "sets the body, releasing its stream unread",
	"SetBodyStream":          "sets the body, releasing its stream unread",
	"SetBodyStreamWriter":    "sets the body, releasing its stream unread",
	"SetBodyString":          "sets the body, releasing its stream unread",
	"String":                 "writes the request, copying its body's stream out and releasing it",
	"SwapBody":               "swaps the body out, releasing its stream unread",
	"Write":                  "writes the request, copying its body's stream out and releasing it — unread past a write that fails",
	"WriteTo":                "writes the request, copying its body's stream out and releasing it — unread past a write that fails",
}

// requestMethodsKept are the rest of *fasthttp.Request's methods. They read the
// body through Request.bodyBytes — c.Body()'s path, which reads it to its end,
// or to the body deadline, which closes the connection — read the stream
// without releasing it, or leave the body alone.
var requestMethodsKept = []string{
	"Body", "BodyGunzip", "BodyGunzipWithLimit", "BodyInflate", "BodyInflateWithLimit", "BodyStream",
	"BodyUnbrotli", "BodyUnbrotliWithLimit", "BodyUncompressed", "BodyUncompressedWithLimit", "BodyUnzstd",
	"BodyUnzstdWithLimit", "ConnectionClose", "GetTimeOut", "Host", "IsBodyStream", "MayContinue", "MultipartForm",
	"MultipartFormWithLimit", "PostArgs", "RemoveMultipartFormFiles", "RemoveUserValue", "RemoveUserValueBytes",
	"RequestURI", "ResetUserValues", "SetConnectionClose", "SetHost", "SetHostBytes", "SetTimeout",
	"SetUserValue", "SetUserValueBytes", "URI", "UserValue", "UserValueBytes", "VisitUserValues",
	"VisitUserValuesAll",
}

// headerMethodsRefused are the methods of *fasthttp.RequestHeader the guard
// refuses, each with what it does to the framing bodyMayBeLeftUnread and the
// body's own reader go by.
var headerMethodsRefused = map[string]string{
	"CopyTo":                        "overwrites the header it copies to, framing included",
	"DisableSpecialHeader":          "stops the header from keeping its Content-Length and Transfer-Encoding where they are read",
	"Read":                          "reads a whole header over this one",
	"ReadTrailer":                   "reads fields into the header from a reader of its caller's",
	"Reset":                         "clears the header, framing included",
	"SetContentLength":              "rewrites the Content-Length the body is read by",
	"SetContentType":                "rewrites the Content-Type the upload exemption is decided by",
	"SetContentTypeBytes":           "rewrites the Content-Type the upload exemption is decided by",
	"SetMethod":                     "rewrites the method the request is routed by",
	"SetMethodBytes":                "rewrites the method the request is routed by",
	"SetMultipartFormBoundary":      "rewrites the Content-Type the upload exemption is decided by",
	"SetMultipartFormBoundaryBytes": "rewrites the Content-Type the upload exemption is decided by",
	"SetRequestURI":                 "rewrites the path the request is routed by",
	"SetRequestURIBytes":            "rewrites the path the request is routed by",
}

// headerMethodsKeyed are the methods of *fasthttp.RequestHeader that set,
// add or delete a field by name. The guard refuses a call whose key is one of
// headerKeysRefused, in any letter case, or is not a constant it can read —
// and a method value or method expression of one, whose key it cannot see at
// all.
var headerMethodsKeyed = []string{
	"Add", "AddBytesK", "AddBytesKV", "AddBytesV", "Del", "DelBytes", "Set", "SetBytesK", "SetBytesKV",
	"SetBytesV", "SetCanonical",
}

// headerKeysRefused are the fields a keyed header mutator may not set, add or
// delete: the two the body's framing is read by, and the one the upload
// exemption is decided by.
var headerKeysRefused = []string{"Content-Length", "Content-Type", "Transfer-Encoding"}

// headerMethodsKept are the rest of *fasthttp.RequestHeader's methods: none
// of them touches the framing, the path, the method or the Content-Type.
var headerMethodsKept = []string{
	"AddTrailer", "AddTrailerBytes", "All", "AllInOrder", "AppendBytes", "ConnectionClose", "ConnectionUpgrade",
	"ContentEncoding", "ContentLength", "ContentType", "Cookie", "CookieBytes", "Cookies", "DelAllCookies",
	"DelCookie", "DelCookieBytes", "DisableNormalizing", "EnableNormalizing", "EnableSpecialHeader",
	"HasAcceptEncoding", "HasAcceptEncodingBytes", "Header", "Host", "IsConnect", "IsDelete", "IsGet", "IsHTTP11",
	"IsHead", "IsOptions", "IsPatch", "IsPost", "IsPut", "IsTrace", "Len", "Method", "MultipartFormBoundary", "Peek",
	"PeekAll", "PeekBytes", "PeekKeys", "PeekTrailerKeys", "Protocol", "RawHeaders", "Referer", "RequestURI",
	"ResetConnectionClose", "SetByteRange", "SetConnectionClose", "SetContentEncoding", "SetContentEncodingBytes",
	"SetCookie", "SetCookieBytesK", "SetCookieBytesKV", "SetHost", "SetHostBytes", "SetNoDefaultContentType",
	"SetProtocol", "SetProtocolBytes", "SetReferer", "SetRefererBytes", "SetTrailer", "SetTrailerBytes",
	"SetUserAgent", "SetUserAgentBytes", "String",
	"TrailerHeader", "Trailers", "UserAgent", "VisitAll", "VisitAllCookie", "VisitAllInOrder", "VisitAllTrailer",
	"Write", "WriteTo",
}

// uriMethodsRefused are the methods of *fasthttp.URI the guard refuses: each
// writes a path into a URI, which on the request's own is the path it is
// routed by.
//
// They are refused on every *fasthttp.URI, the request's or not. Which URI a
// value is depends on where it came from — ctx.URI(), a fasthttp.AcquireURI()
// of a handler's own, a field — and go/types sees one type for all of them,
// so the guard cannot narrow these the way calledOn narrows the header's
// methods by type. A URI a handler builds and rewrites for itself — for a
// redirect, say — needs an entry in bodyGuardAllowed, with its reason. None
// does today.
var uriMethodsRefused = map[string]string{
	"CopyTo":       "writes this URI's path over another's",
	"Parse":        "parses a whole URI, path included, over this one",
	"Reset":        "clears the URI, path included",
	"SetPath":      "rewrites the path the request is routed by",
	"SetPathBytes": "rewrites the path the request is routed by",
	"Update":       "rewrites the path, relative to the one it had",
	"UpdateBytes":  "rewrites the path, relative to the one it had",
}

// uriMethodsKept are the rest of *fasthttp.URI's methods: none of them writes
// a path.
var uriMethodsKept = []string{
	"AppendBytes", "FullURI", "Hash", "Host", "LastPathSegment", "Password", "Path", "PathOriginal", "QueryArgs",
	"QueryString", "RequestURI", "Scheme", "SetHash", "SetHashBytes", "SetHost", "SetHostBytes", "SetPassword",
	"SetPasswordBytes", "SetQueryString", "SetQueryStringBytes", "SetScheme", "SetSchemeBytes", "SetUsername",
	"SetUsernameBytes", "String", "Username", "WriteTo",
}

// fiberOverrides are Fiber's accessors that re-route a request when given an
// argument — Path and Method take an override, and set the path or method the
// router matches from then on. The guard refuses a call that passes one, and
// a method value or method expression of one, which may. Each is named by the
// type that declares it, which fiberOverrideTypes lists.
var fiberOverrides = []string{"Method", "Path"}

// fiberOverrideTypes are the Fiber types whose Path and Method the guard
// holds to fiberOverrides, where they have one: the Ctx and Req interfaces
// handlers see, and DefaultCtx and DefaultReq, which implement them.
var fiberOverrideTypes = []string{"Ctx", "DefaultCtx", "DefaultReq", "Req"}

// requestCtxMethodsRefused are the methods of *fasthttp.RequestCtx the guard
// refuses.
var requestCtxMethodsRefused = map[string]string{
	"Conn":                     "hands out the connection itself, under which a deadline or a read reaches past the body's stream",
	"HijackSetNoResponse":      "suppresses the answer the Connection: close rides on",
	"Init":                     "copies another request over this one",
	"Init2":                    "puts another connection under this request",
	"TimeoutError":             "swaps the answer for one the handler never sees",
	"TimeoutErrorWithCode":     "swaps the answer for one the handler never sees",
	"TimeoutErrorWithResponse": "swaps the answer for one the handler never sees",
}

// fasthttpFuncsRefused are fasthttp's package-level functions the guard
// refuses besides the clients' Do… ones, which it finds by name.
var fasthttpFuncsRefused = map[string]string{
	"ReleaseRequest":         "resets the request it is given, releasing its stream unread",
	"TimeoutHandler":         "answers in place of the handler, dropping the answer the close rides on",
	"TimeoutWithCodeHandler": "answers in place of the handler, dropping the answer the close rides on",
}

// fasthttpServeFuncs are fasthttp's package-level functions that build a
// default server around the handler they are given — Serve, ServeConn,
// ServeTLS, ListenAndServe and the rest, found by their names and their
// RequestHandler parameter (fasthttpServers). These must be among them.
var fasthttpServeFuncs = []string{
	"ListenAndServe", "ListenAndServeTLS", "ListenAndServeTLSEmbed", "ListenAndServeUNIX", "Serve", "ServeConn",
	"ServeTLS", "ServeTLSEmbed",
}

// adaptorFuncsRefused are the functions of Fiber's adaptor middleware that
// serve a Fiber app or handler through net/http — outside the fasthttp server
// New configured, and so without closeConnectionsLeftMidBody.
var adaptorFuncsRefused = map[string]string{
	"FiberApp":         "serves the app outside the server New configured",
	"FiberHandler":     "serves a handler outside the server New configured",
	"FiberHandlerFunc": "serves a handler outside the server New configured",
}

// serverHandlerFields are the fields of fasthttp.Server through which a
// request reaches code: New sets Handler (closeConnectionsLeftMidBody) and
// ErrorHandler (redactServerErrorBodies), and leaves the other two nil.
var serverHandlerFields = []string{"ContinueHandler", "ErrorHandler", "Handler", "HeaderReceived"}

// importsRefused are the packages whose import the guard refuses.
var importsRefused = map[string]string{
	"github.com/gofiber/fiber/v3/middleware/proxy":   "forwards the request through a fasthttp client, which writes its body out and releases it unread past a write that fails",
	"github.com/gofiber/fiber/v3/middleware/rewrite": "rewrites the path a request is routed by (Ctx.Path), after closeConnectionsLeftMidBody decided its body deadline by the path it came with",
	"github.com/gofiber/fiber/v3/middleware/timeout": "answers in place of the handler, swapping the answer the close rides on",
}

// bodyGuardAllowed are the findings that are meant, keyed by
// bodyGuardFinding.key, each with its reason. A key that no longer matches
// anything fails the guard as well, so a moved or deleted site cannot leave
// an exception behind for the next one.
var bodyGuardAllowed = map[string]string{
	"internal/api/body_framing.go:closeConnectionsLeftMidBody:assigns fasthttp.Server.Handler": "this is " +
		"closeConnectionsLeftMidBody installing itself; New calls it once, on the server fiber.New made",
	"internal/api/body_framing.go:closeConnectionsLeftMidBody:refers to (*fasthttp.RequestCtx).Conn": "this is " +
		"where the body deadline is armed, on the connection, before the handler runs",
	"internal/api/errors.go:redactServerErrorBodies:assigns fasthttp.Server.ErrorHandler": "this is " +
		"redactServerErrorBodies installing itself around Fiber's handler; New calls it once, right after " +
		"closeConnectionsLeftMidBody",
}

// bodyGuardFinding is one site TestGuard_NothingDetachesAnUnreadRequestBody
// refuses.
type bodyGuardFinding struct {
	file, function, what, why string
	line                      int
}

// key is how bodyGuardAllowed names a finding: the file, the function it is
// in, and what it does — not the line, which moves.
func (f bodyGuardFinding) key() string { return f.file + ":" + f.function + ":" + f.what }

// bodyGuardRules are the guard's rules, resolved to the objects of the
// fasthttp and Fiber packages the code is type-checked against.
type bodyGuardRules struct {
	funcs        map[*types.Func]bodyGuardRule // functions and methods refused outright
	keyed        map[*types.Func]string        // header mutators refused by their key
	keyedOn      *types.TypeName               // the header type keyed is refused on
	overrides    map[*types.Func]string        // Fiber accessors refused when given an argument
	serverFields map[*types.Var]string         // fasthttp.Server's handler fields
	replaced     map[*types.TypeName]string    // types no value of which may be written over or built
	dispatch     []bodyGuardDispatch
	imports      map[string]string
}

// bodyGuardRule is what referring to a refused function or method does. recv
// is the type a refused method is refused on, or nil for a package-level
// function: fasthttp declares some of RequestHeader's methods on a header type
// it embeds in ResponseHeader as well, so the method alone does not say whose
// header it is called on.
type bodyGuardRule struct {
	what, why string
	recv      *types.TypeName
}

// bodyGuardDispatch is a set of method names refused when called through an
// interface or type parameter that implementer satisfies.
type bodyGuardDispatch struct {
	implementer types.Type
	names       map[string]bool
}

// newBodyGuardRules resolves the rules against the packages imp imports.
func newBodyGuardRules(imp types.Importer) (*bodyGuardRules, error) {
	r := &bodyGuardRules{
		funcs:        map[*types.Func]bodyGuardRule{},
		keyed:        map[*types.Func]string{},
		overrides:    map[*types.Func]string{},
		serverFields: map[*types.Var]string{},
		replaced:     map[*types.TypeName]string{},
		imports:      importsRefused,
	}
	fh, err := imp.Import(fasthttpImportPath)
	if err != nil {
		return nil, err
	}
	fb, err := imp.Import(fiberImportPath)
	if err != nil {
		return nil, err
	}
	ad, err := imp.Import(adaptorImportPath)
	if err != nil {
		return nil, err
	}
	typeName := func(pkg *types.Package, name string) (*types.TypeName, error) {
		tn, ok := pkg.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			return nil, fmt.Errorf("%s has no type %s", pkg.Path(), name)
		}
		return tn, nil
	}
	method := func(tn *types.TypeName, name string) (*types.Func, error) {
		obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(tn.Type()), false, tn.Pkg(), name)
		f, ok := obj.(*types.Func)
		if !ok {
			return nil, fmt.Errorf("*%s.%s has no method %s", tn.Pkg().Name(), tn.Name(), name)
		}
		return f, nil
	}
	refuseMethods := func(tn *types.TypeName, refused map[string]string) error {
		for _, name := range slices.Sorted(maps.Keys(refused)) {
			f, err := method(tn, name)
			if err != nil {
				return err
			}
			r.funcs[f] = bodyGuardRule{"refers to (*" + tn.Pkg().Name() + "." + tn.Name() + ")." + name, refused[name], tn}
		}
		return nil
	}

	request, err := typeName(fh, "Request")
	if err != nil {
		return nil, err
	}
	header, err := typeName(fh, "RequestHeader")
	if err != nil {
		return nil, err
	}
	requestCtx, err := typeName(fh, "RequestCtx")
	if err != nil {
		return nil, err
	}
	server, err := typeName(fh, "Server")
	if err != nil {
		return nil, err
	}
	uri, err := typeName(fh, "URI")
	if err != nil {
		return nil, err
	}
	for tn, refused := range map[*types.TypeName]map[string]string{
		request: requestMethodsRefused, header: headerMethodsRefused, requestCtx: requestCtxMethodsRefused,
		uri: uriMethodsRefused,
	} {
		if err := refuseMethods(tn, refused); err != nil {
			return nil, err
		}
	}
	for _, name := range headerMethodsKeyed {
		f, err := method(header, name)
		if err != nil {
			return nil, err
		}
		r.keyed[f] = "(*fasthttp.RequestHeader)." + name
	}
	r.keyedOn = header
	for pkg, refused := range map[*types.Package]map[string]string{fh: fasthttpFuncsRefused, ad: adaptorFuncsRefused} {
		for name, why := range refused {
			f, ok := pkg.Scope().Lookup(name).(*types.Func)
			if !ok {
				return nil, fmt.Errorf("%s has no function %s", pkg.Path(), name)
			}
			r.funcs[f] = bodyGuardRule{"refers to " + pkg.Name() + "." + name, why, nil}
		}
	}
	for _, call := range fasthttpClientCalls(fh) {
		r.funcs[call.f] = bodyGuardRule{"refers to " + funcName(call.f), "sends a request through a fasthttp " +
			"client, which writes its body out and releases it — unread past a write that fails", nil}
	}
	for _, f := range fasthttpServers(fh) {
		r.funcs[f] = bodyGuardRule{"refers to fasthttp." + f.Name(), "builds a default server around the handler " +
			"it is given, without the timeouts, the redaction and the upload rules New configures", nil}
	}
	for _, typ := range fiberOverrideTypes {
		tn, err := typeName(fb, typ)
		if err != nil {
			return nil, err
		}
		t, prefix := tn.Type(), "(fiber."+typ+")"
		if _, isInterface := t.Underlying().(*types.Interface); !isInterface {
			t, prefix = types.NewPointer(t), "(*fiber."+typ+")"
		}
		for _, name := range fiberOverrides {
			if f, ok := lookupMethod(t, fb, name); ok {
				r.overrides[f.Origin()] = prefix + "." + name
			}
		}
		if f, ok := lookupMethod(t, fb, "Reset"); ok {
			r.funcs[f.Origin()] = bodyGuardRule{"refers to " + prefix + ".Reset", "it re-derives the path and " +
				"method the request is routed by from a RequestCtx", nil}
		}
	}
	app, err := typeName(fb, "App")
	if err != nil {
		return nil, err
	}
	handler, err := method(app, "Handler")
	if err != nil {
		return nil, err
	}
	r.funcs[handler] = bodyGuardRule{"refers to (*fiber.App).Handler",
		"it is Fiber's handler without closeConnectionsLeftMidBody, which serving through it bypasses", app}

	fields, ok := server.Type().Underlying().(*types.Struct)
	if !ok {
		return nil, errors.New("fasthttp.Server is not a struct")
	}
	for i := range fields.NumFields() {
		if f := fields.Field(i); slices.Contains(serverHandlerFields, f.Name()) {
			r.serverFields[f] = "fasthttp.Server." + f.Name()
		}
	}
	if len(r.serverFields) != len(serverHandlerFields) {
		return nil, fmt.Errorf("fasthttp.Server has %d of the fields %v", len(r.serverFields), serverHandlerFields)
	}
	r.replaced[request] = "fasthttp.Request"
	r.replaced[header] = "fasthttp.RequestHeader"
	r.replaced[server] = "fasthttp.Server"

	requestNames := map[string]bool{}
	for name := range requestMethodsRefused {
		requestNames[name] = true
	}
	headerNames := map[string]bool{}
	for name := range headerMethodsRefused {
		headerNames[name] = true
	}
	for _, name := range headerMethodsKeyed {
		headerNames[name] = true
	}
	r.dispatch = []bodyGuardDispatch{
		{types.NewPointer(request.Type()), requestNames},
		{types.NewPointer(header.Type()), headerNames},
	}
	return r, nil
}

// fasthttpClientCall is a fasthttp function or method that sends a request
// through a client, and how code outside fasthttp names it.
type fasthttpClientCall struct {
	f   *types.Func
	ref string // fasthttp.Do, (*fasthttp.Client).Do, fasthttp.BalancingClient.DoDeadline
}

// fasthttpClientCalls are fasthttp's exported functions named Do or Do and a
// capitalised word — its Do, DoTimeout, DoDeadline and DoRedirects — and the
// methods so named of every exported type of it, the clients' among them:
// whatever fasthttp adds under that prefix is refused with them, and Done and
// Domain are not. A method a type has through an embedded field is listed
// under that type as well.
func fasthttpClientCalls(fh *types.Package) []fasthttpClientCall {
	var out []fasthttpClientCall
	for _, name := range fh.Scope().Names() {
		obj := fh.Scope().Lookup(name)
		if !obj.Exported() {
			continue
		}
		switch obj := obj.(type) {
		case *types.Func:
			if isClientCallName(name) {
				out = append(out, fasthttpClientCall{obj, "fasthttp." + name})
			}
		case *types.TypeName:
			t, ref := obj.Type(), "fasthttp."+name
			if _, isInterface := t.Underlying().(*types.Interface); !isInterface {
				t, ref = types.NewPointer(t), "(*fasthttp."+name+")"
			}
			ms := types.NewMethodSet(t)
			for i := range ms.Len() {
				if m := ms.At(i).Obj(); isClientCallName(m.Name()) && m.Pkg() == fh {
					out = append(out, fasthttpClientCall{m.(*types.Func), ref + "." + m.Name()})
				}
			}
		}
	}
	return out
}

// lookupMethod is the method name of a value of type t, if it has one.
func lookupMethod(t types.Type, pkg *types.Package, name string) (*types.Func, bool) {
	obj, _, _ := types.LookupFieldOrMethod(t, false, pkg, name)
	f, ok := obj.(*types.Func)
	return f, ok
}

// fasthttpServers are fasthttp's exported package-level functions named Serve…
// or ListenAndServe… that take a RequestHandler: each builds a default server
// around it. ServeFile and its kin, which answer a request rather than serve
// one, take none.
func fasthttpServers(fh *types.Package) []*types.Func {
	handler := fh.Scope().Lookup("RequestHandler")
	var out []*types.Func
	for _, name := range fh.Scope().Names() {
		f, ok := fh.Scope().Lookup(name).(*types.Func)
		serveNamed := strings.HasPrefix(name, "Serve") || strings.HasPrefix(name, "ListenAndServe")
		if !ok || !f.Exported() || !serveNamed {
			continue
		}
		params := f.Type().(*types.Signature).Params()
		for i := range params.Len() {
			if named, ok := params.At(i).Type().(*types.Named); ok && handler != nil && named.Obj() == handler {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// isClientCallName reports whether name is Do, or Do followed by a capital:
// DoTimeout, not Done.
func isClientCallName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Do")
	return ok && (rest == "" || rest[0] >= 'A' && rest[0] <= 'Z')
}

// funcName names f as a reference to it reads: fasthttp.Do,
// (*fasthttp.Client).Do, fasthttp.BalancingClient.DoDeadline.
func funcName(f *types.Func) string {
	sig, ok := f.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return f.Pkg().Name() + "." + f.Name()
	}
	recv := sig.Recv().Type()
	if p, isPointer := recv.(*types.Pointer); isPointer {
		if named, isNamed := p.Elem().(*types.Named); isNamed {
			return "(*" + f.Pkg().Name() + "." + named.Obj().Name() + ")." + f.Name()
		}
	}
	if named, isNamed := recv.(*types.Named); isNamed {
		return f.Pkg().Name() + "." + named.Obj().Name() + "." + f.Name()
	}
	return f.Pkg().Name() + "." + f.Name()
}

// scanForBodyDetaching reports what TestGuard_NothingDetachesAnUnreadRequestBody
// refuses in one type-checked package: files, named names[i] in findings, and
// info, the type information the checker recorded for them.
func scanForBodyDetaching(fset *token.FileSet, files []*ast.File, names []string, info *types.Info,
	rules *bodyGuardRules) []bodyGuardFinding {
	var out []bodyGuardFinding
	for i, f := range files {
		file := names[i]
		add := func(n ast.Node, function, what, why string) {
			out = append(out, bodyGuardFinding{file: file, function: function, what: what, why: why,
				line: fset.Position(n.Pos()).Line})
		}
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			if why, ok := rules.imports[path]; ok {
				add(imp, "", "imports "+path, why)
			}
		}
		for _, decl := range f.Decls {
			function := declName(decl)
			callee := map[*ast.Ident]*ast.CallExpr{}
			selector := map[*ast.Ident]*ast.SelectorExpr{}
			ast.Inspect(decl, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.CallExpr:
					if sel, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr); ok {
						callee[sel.Sel] = n
					}
				case *ast.SelectorExpr:
					selector[n.Sel] = n
				}
				return true
			})
			ast.Inspect(decl, func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.Ident:
					fn, ok := info.Uses[n].(*types.Func)
					if !ok {
						return true
					}
					fn = fn.Origin()
					if rule, ok := rules.funcs[fn]; ok && calledOn(info, selector[n], rule.recv) {
						add(n, function, rule.what, rule.why)
					}
					if name, ok := rules.keyed[fn]; ok && calledOn(info, selector[n], rules.keyedOn) {
						if what, why := keyedMutation(info, callee[n], name); what != "" {
							add(n, function, what, why)
						}
					}
					if name, ok := rules.overrides[fn]; ok {
						switch call := callee[n]; {
						case call == nil:
							add(n, function, "refers to "+name+" without calling it", "a method value or method "+
								"expression of it may be called with an override")
						case len(call.Args) > 0:
							add(n, function, "calls "+name+" with an argument", "it re-routes the request after "+
								"closeConnectionsLeftMidBody decided its body deadline")
						}
					}
				case *ast.SelectorExpr:
					sel, ok := info.Selections[n]
					if !ok || sel.Kind() != types.MethodVal {
						return true
					}
					iface, isInterface := sel.Recv().Underlying().(*types.Interface)
					if !isInterface {
						return true
					}
					for _, d := range rules.dispatch {
						if d.names[n.Sel.Name] && types.Implements(d.implementer, iface) {
							add(n.Sel, function, "calls "+n.Sel.Name+" through "+sel.Recv().String(),
								"it is satisfied by "+d.implementer.String()+", whose "+n.Sel.Name+" the guard refuses")
						}
					}
				case *ast.AssignStmt:
					if n.Tok != token.ASSIGN {
						return true
					}
					for _, lhs := range n.Lhs {
						if field := serverField(info, rules, lhs); field != "" {
							add(lhs, function, "assigns "+field, "New sets it, and nothing else may replace what it installed")
						}
						if name := replacedType(info, rules, lhs); name != "" {
							add(lhs, function, "writes over a "+name, "a request, header or server written over "+
								"releases or bypasses what the one in use holds")
						}
					}
				case *ast.UnaryExpr:
					if field := serverField(info, rules, n.X); n.Op == token.AND && field != "" {
						add(n, function, "takes the address of "+field, "New sets it, and nothing else may replace "+
							"what it installed")
					}
				case *ast.CompositeLit:
					if name := replacedType(info, rules, n); name != "" {
						add(n, function, "builds a "+name, "nothing in Nexara builds one: a request or header built "+
							"by hand is one forwarded or replaced, and a server built by hand serves without the "+
							"wrapper and the redaction")
					}
				}
				return true
			})
		}
	}
	return out
}

// calledOn reports whether sel — the selector a refused method is named
// through — reaches it on a value of type recv, or of a type holding one as
// an embedded field on the way to the method; a nil recv, or a name that is
// not a method selector, counts as reaching it. It is what keeps a method
// fasthttp declares once, on a header type RequestHeader and ResponseHeader
// both embed, refused on the request's header alone.
func calledOn(info *types.Info, sel *ast.SelectorExpr, recv *types.TypeName) bool {
	if recv == nil || sel == nil {
		return true
	}
	selection, ok := info.Selections[sel]
	if !ok {
		return true
	}
	t, index := selection.Recv(), selection.Index()
	for i := 0; ; i++ {
		if p, isPointer := t.(*types.Pointer); isPointer {
			t = p.Elem()
		}
		if named, isNamed := t.(*types.Named); isNamed && named.Obj() == recv {
			return true
		}
		st, isStruct := t.Underlying().(*types.Struct)
		if !isStruct || i >= len(index)-1 {
			return false
		}
		t = st.Field(index[i]).Type()
	}
}

// keyedMutation reports what a reference to a keyed header mutator does, when
// the guard refuses it: called — call is the call it is the callee of, or nil —
// with a key it reads as Content-Length or Transfer-Encoding, or with a key it
// cannot read, or not called here at all. A method expression's first argument
// is its receiver, never a key it can read, so every call of one is refused.
func keyedMutation(info *types.Info, call *ast.CallExpr, name string) (what, why string) {
	if call == nil || len(call.Args) == 0 {
		return "refers to " + name + " without calling it", "a method value or method expression of a keyed " +
			"header mutator hides its key"
	}
	key, ok := constantKey(info, call.Args[0])
	if !ok {
		return "calls " + name + " with a key it cannot read", "a key that is not a constant may be one of " +
			strings.Join(headerKeysRefused, ", ")
	}
	for _, refused := range headerKeysRefused {
		if strings.EqualFold(strings.TrimSpace(key), refused) {
			return "calls " + name + " on " + key, "it rewrites the framing the body is read by, or the Content-Type " +
				"the upload exemption is decided by"
		}
	}
	return "", ""
}

// constantKey is the string constant arg is, directly or through a
// conversion such as []byte("…").
func constantKey(info *types.Info, arg ast.Expr) (string, bool) {
	arg = ast.Unparen(arg)
	if tv, ok := info.Types[arg]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
		return constant.StringVal(tv.Value), true
	}
	if conv, ok := arg.(*ast.CallExpr); ok && len(conv.Args) == 1 {
		if tv, ok := info.Types[conv.Fun]; ok && tv.IsType() {
			return constantKey(info, conv.Args[0])
		}
	}
	return "", false
}

// serverField names the fasthttp.Server handler field e selects, if it does.
func serverField(info *types.Info, rules *bodyGuardRules, e ast.Expr) string {
	sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	field, ok := info.Uses[sel.Sel].(*types.Var)
	if !ok {
		return ""
	}
	return rules.serverFields[field.Origin()]
}

// replacedType names the fasthttp type e is a value of, if the guard refuses
// writing over or building one.
func replacedType(info *types.Info, rules *bodyGuardRules, e ast.Expr) string {
	tv, ok := info.Types[e]
	if !ok {
		return ""
	}
	named, ok := tv.Type.(*types.Named)
	if !ok {
		return ""
	}
	return rules.replaced[named.Obj()]
}

// declName is the name of the function decl declares — Recv.Name for a
// method — or "" for anything else.
func declName(decl ast.Decl) string {
	fd, ok := decl.(*ast.FuncDecl)
	if !ok {
		return ""
	}
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	recv := fd.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	if index, ok := recv.(*ast.IndexExpr); ok {
		recv = index.X
	}
	if id, ok := recv.(*ast.Ident); ok {
		return id.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

const (
	fasthttpImportPath = "github.com/valyala/fasthttp"
	fiberImportPath    = "github.com/gofiber/fiber/v3"
	adaptorImportPath  = "github.com/gofiber/fiber/v3/middleware/adaptor"
	moduleImportPath   = "github.com/bigjakk/nexara"
)

// bodyGuardProgram is every non-test package under internal/, cmd/ and pkg/,
// type-checked from source, with the importer and the rules they were
// checked against.
type bodyGuardProgram struct {
	fset  *token.FileSet
	imp   types.Importer
	rules *bodyGuardRules
	pkgs  []bodyGuardPackage
	files int
}

type bodyGuardPackage struct {
	path  string
	files []*ast.File
	names []string // each file's path from the repository root
	info  *types.Info
}

var (
	bodyGuardOnce    sync.Once
	bodyGuardProg    *bodyGuardProgram
	bodyGuardLoadErr error
)

// loadBodyGuardProgram loads the program once per test binary.
func loadBodyGuardProgram(t *testing.T) *bodyGuardProgram {
	t.Helper()
	bodyGuardOnce.Do(func() { bodyGuardProg, bodyGuardLoadErr = loadBodyGuardProgramOnce() })
	if bodyGuardLoadErr != nil {
		t.Fatalf("loading the packages the guard reads: %v", bodyGuardLoadErr)
	}
	return bodyGuardProg
}

// goListedPackage is what the guard reads of `go list -json`.
type goListedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	CgoFiles   []string
	Export     string
	Error      *struct{ Err string }
}

// listModulePackages is every package `go list ./...` reports in the module,
// in bodyGuardBuildEnv.
func listModulePackages() ([]string, error) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(goBin, "list", "./...")
	cmd.Dir = filepath.Join("..", "..")
	cmd.Env = append(os.Environ(), bodyGuardBuildEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list ./...: %w\n%s", err, stderr.String())
	}
	return strings.Fields(string(out)), nil
}

// bodyGuardBuildEnv is the build context the guard lists and type-checks the
// module in: the release image's, as docker/nexara/Dockerfile runs go build —
// CGO_ENABLED=0, GOOS=linux, GOARCH=amd64 — with GOFLAGS emptied so no build
// tag set in the environment changes which files are read. A file only the
// release build compiles (a !cgo one, say) is scanned; one it leaves out is
// not. TestGuard_NothingDetachesAnUnreadRequestBody_ClassifiesEveryMethod
// holds it to the Dockerfile.
var bodyGuardBuildEnv = []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "GOFLAGS="}

// loadBodyGuardProgramOnce asks go list, in bodyGuardBuildEnv, for every
// package in the module (./...) and the export data of everything they
// import, and type-checks each module package from its source. The Fiber
// middleware packages are listed too, for the self-test's probe to import.
//
// It checks the context took: under it the net package has no cgo files, and
// syscall has its linux/amd64 file. Listed on a host with a C compiler and
// the environment left as it was, net would have cgo files.
func loadBodyGuardProgramOnce() (*bodyGuardProgram, error) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return nil, err
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("the go command, which lists the packages: %w", err)
	}
	cmd := exec.Command(goBin, "list", "-export", "-deps", "-json=ImportPath,Dir,GoFiles,CgoFiles,Export,Error",
		"./...", adaptorImportPath, "github.com/gofiber/fiber/v3/middleware/proxy",
		"github.com/gofiber/fiber/v3/middleware/rewrite", "github.com/gofiber/fiber/v3/middleware/timeout")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), bodyGuardBuildEnv...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w\n%s", err, stderr.String())
	}

	exports := map[string]string{}
	listed := map[string]goListedPackage{}
	var targets []goListedPackage
	for dec := json.NewDecoder(bytes.NewReader(out)); dec.More(); {
		var p goListedPackage
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("decoding go list: %w", err)
		}
		if p.Error != nil {
			return nil, fmt.Errorf("go list: %s: %s", p.ImportPath, p.Error.Err)
		}
		exports[p.ImportPath] = p.Export
		listed[p.ImportPath] = p
		if p.ImportPath == moduleImportPath || strings.HasPrefix(p.ImportPath, moduleImportPath+"/") {
			targets = append(targets, p)
		}
	}
	if net := listed["net"]; len(net.CgoFiles) > 0 || !slices.Contains(listed["syscall"].GoFiles, "syscall_linux_amd64.go") {
		return nil, fmt.Errorf("go list did not run in the release build context %v: net has cgo files %v, and "+
			"syscall's files are %v", bodyGuardBuildEnv, net.CgoFiles, listed["syscall"].GoFiles)
	}

	prog := &bodyGuardProgram{fset: token.NewFileSet()}
	prog.imp = importer.ForCompiler(prog.fset, "gc", func(path string) (io.ReadCloser, error) {
		export := exports[path]
		if export == "" {
			return nil, fmt.Errorf("go list gave no export data for %s", path)
		}
		return os.Open(export)
	})
	if prog.rules, err = newBodyGuardRules(prog.imp); err != nil {
		return nil, err
	}
	for _, p := range targets {
		if len(p.CgoFiles) > 0 {
			return nil, fmt.Errorf("%s uses cgo, whose files the guard does not type-check", p.ImportPath)
		}
		pkg := bodyGuardPackage{path: p.ImportPath}
		for _, name := range p.GoFiles {
			full := filepath.Join(p.Dir, name)
			f, err := parser.ParseFile(prog.fset, full, nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, err
			}
			rel, err := filepath.Rel(root, full)
			if err != nil {
				return nil, err
			}
			pkg.files = append(pkg.files, f)
			pkg.names = append(pkg.names, filepath.ToSlash(rel))
		}
		pkg.info = newBodyGuardInfo()
		if _, err := (&types.Config{Importer: prog.imp}).Check(p.ImportPath, prog.fset, pkg.files, pkg.info); err != nil {
			return nil, fmt.Errorf("type-checking %s: %w", p.ImportPath, err)
		}
		prog.pkgs = append(prog.pkgs, pkg)
		prog.files += len(pkg.files)
	}
	return prog, nil
}

func newBodyGuardInfo() *types.Info {
	return &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
}

// TestGuard_NothingDetachesAnUnreadRequestBody_ClassifiesEveryMethod holds the
// guard's lists to fasthttp as go.mod pins it: every method of
// *fasthttp.Request is refused or kept, and every method of
// *fasthttp.RequestHeader refused, keyed or kept — one of them, never two —
// so a method a fasthttp upgrade adds fails here until someone decides which
// it is. And each method this layer's design depends on refusing is refused,
// so that moving one to a kept list is a failure rather than an edit.
func TestGuard_NothingDetachesAnUnreadRequestBody_ClassifiesEveryMethod(t *testing.T) {
	prog := loadBodyGuardProgram(t)
	fh, err := prog.imp.Import(fasthttpImportPath)
	if err != nil {
		t.Fatalf("importing fasthttp: %v", err)
	}
	methods := func(name string) []string {
		tn, ok := fh.Scope().Lookup(name).(*types.TypeName)
		if !ok {
			t.Fatalf("fasthttp has no type %s", name)
		}
		ms := types.NewMethodSet(types.NewPointer(tn.Type()))
		var names []string
		for i := range ms.Len() {
			if m := ms.At(i).Obj(); m.Exported() {
				names = append(names, m.Name())
			}
		}
		return names
	}
	partition := func(typeName string, lists map[string][]string) {
		seen := map[string]string{}
		for list, names := range lists {
			for _, name := range names {
				if other, ok := seen[name]; ok {
					t.Errorf("(*fasthttp.%s).%s is in both %s and %s", typeName, name, other, list)
				}
				seen[name] = list
			}
		}
		have := methods(typeName)
		for _, name := range have {
			if _, ok := seen[name]; !ok {
				t.Errorf("(*fasthttp.%s).%s is in none of the guard's lists: decide whether it releases, replaces, "+
					"reads over or writes out the body or its framing", typeName, name)
			}
		}
		for name, list := range seen {
			if !slices.Contains(have, name) {
				t.Errorf("%s names (*fasthttp.%s).%s, which fasthttp does not have", list, typeName, name)
			}
		}
	}
	partition("Request", map[string][]string{
		"requestMethodsRefused": slices.Collect(maps.Keys(requestMethodsRefused)),
		"requestMethodsKept":    requestMethodsKept,
	})
	partition("RequestHeader", map[string][]string{
		"headerMethodsRefused": slices.Collect(maps.Keys(headerMethodsRefused)),
		"headerMethodsKeyed":   headerMethodsKeyed,
		"headerMethodsKept":    headerMethodsKept,
	})
	partition("URI", map[string][]string{
		"uriMethodsRefused": slices.Collect(maps.Keys(uriMethodsRefused)),
		"uriMethodsKept":    uriMethodsKept,
	})

	for _, name := range []string{"AppendBody", "AppendBodyString", "CloseBodyStream", "CopyTo", "Read",
		"ReadLimitBody", "ReleaseBody", "Reset", "ResetBody", "SetBody", "SetBodyRaw", "SetBodyStream",
		"SetBodyStreamWriter", "SetBodyString", "SwapBody", "Write", "WriteTo", "SetRequestURI",
		"SetRequestURIBytes", "SetURI"} {
		if _, ok := requestMethodsRefused[name]; !ok {
			t.Errorf("(*fasthttp.Request).%s is not refused; it releases or overwrites the body, or rewrites the path", name)
		}
	}
	for _, name := range []string{"DisableSpecialHeader", "Reset", "SetContentLength", "SetContentType",
		"SetContentTypeBytes", "SetMethod", "SetMethodBytes", "SetMultipartFormBoundary", "SetRequestURI",
		"SetRequestURIBytes"} {
		if _, ok := headerMethodsRefused[name]; !ok {
			t.Errorf("(*fasthttp.RequestHeader).%s is not refused; it rewrites the framing, the path or the method", name)
		}
	}
	for _, name := range []string{"SetPath", "SetPathBytes", "Update", "UpdateBytes"} {
		if _, ok := uriMethodsRefused[name]; !ok {
			t.Errorf("(*fasthttp.URI).%s is not refused; it rewrites the path", name)
		}
	}
	for _, name := range []string{"Add", "Del", "Set", "SetCanonical"} {
		if !slices.Contains(headerMethodsKeyed, name) {
			t.Errorf("(*fasthttp.RequestHeader).%s is not keyed; it can set or delete Content-Length", name)
		}
	}
	for _, key := range []string{"Content-Length", "Content-Type", "Transfer-Encoding"} {
		if !slices.Contains(headerKeysRefused, key) {
			t.Errorf("%s is not among the keys a header mutator may not set: the framing or the upload exemption "+
				"is read from it", key)
		}
	}

	// The key check reads a keyed mutator's first argument: it has to be the key.
	header, _ := fh.Scope().Lookup("RequestHeader").(*types.TypeName)
	for _, name := range headerMethodsKeyed {
		obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(header.Type()), false, fh, name)
		sig, ok := obj.Type().(*types.Signature)
		if !ok || sig.Params().Len() == 0 || sig.Params().At(0).Name() != "key" {
			t.Errorf("(*fasthttp.RequestHeader).%s does not take its key first", name)
		}
	}
	calls := fasthttpClientCalls(fh)
	refs := make([]string, 0, len(calls))
	for _, call := range calls {
		refs = append(refs, call.ref)
	}
	for _, ref := range []string{"fasthttp.Do", "fasthttp.DoTimeout", "fasthttp.DoDeadline", "fasthttp.DoRedirects",
		"(*fasthttp.Client).Do", "(*fasthttp.HostClient).Do", "(*fasthttp.LBClient).Do", "(*fasthttp.PipelineClient).Do"} {
		if !slices.Contains(refs, ref) {
			t.Errorf("%s is not among the fasthttp client calls the guard refuses (%v)", ref, refs)
		}
	}

	serveFuncs := fasthttpServers(fh)
	servers := make([]string, 0, len(serveFuncs))
	for _, f := range serveFuncs {
		servers = append(servers, f.Name())
	}
	for _, name := range fasthttpServeFuncs {
		if !slices.Contains(servers, name) {
			t.Errorf("fasthttp.%s is not among the servers the guard refuses (%v)", name, servers)
		}
	}
	for _, name := range []string{"ServeFile", "ServeFS", "ServeFileBytes"} {
		if slices.Contains(servers, name) {
			t.Errorf("fasthttp.%s answers a request rather than serving one, and is refused as a server", name)
		}
	}

	rules := prog.rules
	overrides := slices.Collect(maps.Values(rules.overrides))
	for _, name := range []string{"(fiber.Ctx).Path", "(fiber.Ctx).Method", "(fiber.Req).Method",
		"(*fiber.DefaultCtx).Path", "(*fiber.DefaultReq).Method"} {
		if !slices.Contains(overrides, name) {
			t.Errorf("%s is not among the overrides the guard refuses (%v)", name, overrides)
		}
	}
	var resets []string
	for _, rule := range rules.funcs {
		if strings.HasSuffix(rule.what, ".Reset") && strings.Contains(rule.what, "fiber.") {
			resets = append(resets, rule.what)
		}
	}
	for _, what := range []string{"refers to (fiber.Ctx).Reset", "refers to (*fiber.DefaultCtx).Reset"} {
		if !slices.Contains(resets, what) {
			t.Errorf("%s is not refused (%v)", strings.TrimPrefix(what, "refers to "), resets)
		}
	}

	// The build context is the release image's: the go build the Dockerfile
	// runs, with the variables it sets and no -tags.
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "docker", "nexara", "Dockerfile"))
	if err != nil {
		t.Fatalf("reading the Dockerfile: %v", err)
	}
	var build string
	for instruction := range strings.SplitSeq(strings.ReplaceAll(string(dockerfile), "\\\n", " "), "\n") {
		if strings.HasPrefix(instruction, "RUN ") && strings.Contains(instruction, " go build ") {
			if build != "" {
				t.Fatalf("the Dockerfile runs go build twice; the guard reads one build context")
			}
			build = instruction
		}
	}
	env, _, found := strings.Cut(strings.TrimPrefix(build, "RUN "), " go build ")
	if !found {
		t.Fatalf("the Dockerfile has no RUN … go build line (%q)", build)
	}
	want := slices.DeleteFunc(slices.Clone(bodyGuardBuildEnv), func(v string) bool { return v == "GOFLAGS=" })
	if got := strings.Fields(env); !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Errorf("the Dockerfile builds with %v, the guard lists the module with %v", got, bodyGuardBuildEnv)
	}
	if strings.Contains(build, "-tags") {
		t.Errorf("the Dockerfile builds with build tags (%q); the guard lists the module with none", build)
	}
}

// TestGuard_NothingDetachesAnUnreadRequestBody_ReportsWhatItShould runs the
// guard's scanner over a package written to hold one of everything it
// refuses — each on a line marked "want", one line per entry of every list,
// generated by walking the list — and the look-alikes it must leave alone. It
// fails for a marked line with no finding and for a finding on an unmarked
// line, so each rule is shown to bite and none to overreach.
func TestGuard_NothingDetachesAnUnreadRequestBody_ReportsWhatItShould(t *testing.T) {
	prog := loadBodyGuardProgram(t)
	fh, err := prog.imp.Import(fasthttpImportPath)
	if err != nil {
		t.Fatalf("importing fasthttp: %v", err)
	}
	var b strings.Builder
	want := 0
	line := func(format string, args ...any) {
		fmt.Fprintf(&b, "\t"+format+"\n", args...)
	}
	wantLine := func(format string, args ...any) {
		line(format+" // want", args...)
		want++
	}
	b.WriteString(`package probe

import (
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	_ "github.com/gofiber/fiber/v3/middleware/proxy" // want
	_ "github.com/gofiber/fiber/v3/middleware/rewrite" // want
	_ "github.com/gofiber/fiber/v3/middleware/timeout" // want
	"github.com/valyala/fasthttp"
)

type wrapped struct{ *fasthttp.Request }

func resetThrough(r *fasthttp.Request) {
	r.Reset() // want
}

func resetThroughTypeParameter[T interface{ ResetBody() }](v T) {
	v.ResetBody() // want
}

func refused(c fiber.Ctx, ctx *fasthttp.RequestCtx, req *fasthttp.Request, h *fasthttp.RequestHeader, app *fiber.App,
	srv *fasthttp.Server, k string, kb []byte, w wrapped, u *fasthttp.URI, dc *fiber.DefaultCtx, dr *fiber.DefaultReq) {
`)
	want += 5
	for _, name := range slices.Sorted(maps.Keys(requestMethodsRefused)) {
		wantLine("_ = req.%s", name)
	}
	for _, name := range slices.Sorted(maps.Keys(headerMethodsRefused)) {
		wantLine("_ = h.%s", name)
	}
	header, _ := fh.Scope().Lookup("RequestHeader").(*types.TypeName)
	for _, name := range headerMethodsKeyed {
		obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(header.Type()), false, fh, name)
		sig, ok := obj.Type().(*types.Signature)
		if !ok {
			t.Fatalf("(*fasthttp.RequestHeader).%s is not a method", name)
		}
		args := func(key string) string {
			var parts []string
			for i := range sig.Params().Len() {
				isBytes := types.Identical(sig.Params().At(i).Type(), types.NewSlice(types.Typ[types.Byte]))
				switch {
				case i > 0 && isBytes:
					parts = append(parts, "nil")
				case i > 0:
					parts = append(parts, `""`)
				case key == "" && isBytes:
					parts = append(parts, "kb")
				case key == "":
					parts = append(parts, "k")
				case isBytes:
					parts = append(parts, "[]byte("+strconv.Quote(key)+")")
				default:
					parts = append(parts, strconv.Quote(key))
				}
			}
			return strings.Join(parts, ", ")
		}
		wantLine("h.%s(%s)", name, args("Content-Length"))
		wantLine("h.%s(%s)", name, args("transfer-encoding"))
		wantLine("h.%s(%s)", name, args("CONTENT-TYPE"))
		wantLine("h.%s(%s)", name, args(""))
		wantLine("_ = h.%s", name)
		line("h.%s(%s)", name, args("X-Other"))
	}
	for _, name := range slices.Sorted(maps.Keys(requestCtxMethodsRefused)) {
		wantLine("_ = ctx.%s", name)
	}
	for _, name := range slices.Sorted(maps.Keys(uriMethodsRefused)) {
		wantLine("_ = u.%s", name)
	}
	fb, err := prog.imp.Import(fiberImportPath)
	if err != nil {
		t.Fatalf("importing fiber: %v", err)
	}
	receivers := map[string]string{"Ctx": "c", "Req": "c.Req()", "DefaultCtx": "dc", "DefaultReq": "dr"}
	overridden := 0
	for _, typ := range fiberOverrideTypes {
		recv, ok := receivers[typ]
		tn, isType := fb.Scope().Lookup(typ).(*types.TypeName)
		if !ok || !isType {
			t.Fatalf("precondition: no probe receiver for fiber.%s", typ)
		}
		ty := tn.Type()
		if _, isInterface := ty.Underlying().(*types.Interface); !isInterface {
			ty = types.NewPointer(ty)
		}
		for _, name := range fiberOverrides {
			if _, has := lookupMethod(ty, fb, name); has {
				override := map[string]string{"Method": "fiber.MethodPut", "Path": `"/api/v1/other"`}[name]
				if override == "" {
					t.Fatalf("precondition: no probe override for %s", name)
				}
				wantLine("_ = %s.%s(%s)", recv, name, override)
				wantLine("_ = %s.%s", recv, name)
				line("_ = %s.%s()", recv, name)
				overridden++
			}
		}
		if _, has := lookupMethod(ty, fb, "Reset"); has {
			wantLine("_ = %s.Reset", recv)
		}
	}
	for _, f := range fasthttpServers(fh) {
		wantLine("_ = fasthttp.%s", f.Name())
	}
	for _, name := range slices.Sorted(maps.Keys(fasthttpFuncsRefused)) {
		wantLine("_ = fasthttp.%s", name)
	}
	for _, call := range fasthttpClientCalls(fh) {
		wantLine("_ = %s", call.ref)
	}
	for _, name := range slices.Sorted(maps.Keys(adaptorFuncsRefused)) {
		wantLine("_ = adaptor.%s", name)
	}
	for _, name := range serverHandlerFields {
		wantLine("srv.%s = nil", name)
		wantLine("_ = &srv.%s", name)
		line("_ = srv.%s", name)
	}
	for _, shape := range []string{
		"c.Request().SetBody(nil)",
		"req.ResetBody()",
		"(*fasthttp.Request).ResetBody(req)",
		"f := req.CloseBodyStream; _ = f",
		"r := c.Request(); r.Reset()",
		"c.Request().Header.Reset()",
		"c.Request().Header.SetContentLength(0)",
		`c.Request().Header.Del("Content-Length")`,
		`c.Request().Header.Set(fiber.HeaderTransferEncoding, "chunked")`,
		"(*fasthttp.RequestHeader).Del(h, fiber.HeaderAuthorization)",
		"w.SetBody(nil)",
		"req.CopyTo(&ctx.Request)",
		"_ = req.Read(nil)",
		"_, _ = req.WriteTo(nil)",
		"_ = c.Request().String()",
		"_ = fasthttp.Do(req, nil)",
		"_ = (&fasthttp.Client{}).Do(req, nil)",
		"_ = app.Handler()",
		"handle := app.Handler; _ = handle",
		"_ = (*fiber.App).Handler",
		"_ = &fasthttp.Server{Handler: nil}",
		"_ = fasthttp.Server{}",
		"_ = fasthttp.RequestHeader{}",
		"ctx.Request = *req",
		"*req = ctx.Request",
		"ctx.Request.Header = *h",
		"var x interface{ SetBody([]byte) } = req; x.SetBody(nil)",
		"var y interface{ Set(key, value string) } = h; y.Set(k, \"\")",
		"_ = c.Path(\"/api/v1/other\")",
		"c.Request().URI().SetPath(\"/api/v1/other\")",
		"c.Request().SetRequestURI(\"/api/v1/other\")",
		"c.Request().Header.SetMethod(fiber.MethodPut)",
		"_ = fasthttp.ListenAndServe(\":0\", app.Server().Handler)",
	} {
		wantLine("%s", shape)
	}
	b.WriteString("}\n\nfunc lookalikes(c fiber.Ctx, ctx *fasthttp.RequestCtx, req *fasthttp.Request, h *fasthttp.RequestHeader, srv *fasthttp.Server) {\n")
	for _, shape := range []string{
		"_ = ctx.Response.SetBody",
		"ctx.Response.ResetBody()",
		"resp := c.Response(); resp.SetBodyString(\"\")",
		"var sb strings.Builder; sb.Reset()",
		"_ = srv.ReadTimeout",
		"_ = req.Body()",
		"_ = req.IsBodyStream()",
		"_ = c.Body()",
		"_ = h.ContentLength()",
		"_ = h.Peek(fiber.HeaderContentLength)",
		`h.Set(fiber.HeaderAccept, "")`,
		`h.SetBytesKV([]byte("X-Other"), nil)`,
		"h.SetCookie(fiber.HeaderContentLength, \"\")",
		"ctx.SetBody(nil)",
		"_ = ctx.PostBody()",
		"_ = ctx.RequestBodyStream()",
		"var z interface{ SetBody(string) }; if z != nil { z.SetBody(\"\") }",
		"_ = fiber.New(fiber.Config{})",
		"_ = c.Next()",
		"_ = ctx.Request.Header",
		"_ = ctx.Done",
		"_ = (*fasthttp.Cookie).Domain",
		"ctx.Response.Header.SetContentType(\"\")",
		"ctx.Response.Header.SetContentTypeBytes(nil)",
		"_ = c.Path()",
		"_ = c.Method()",
		"_ = c.Req().Method()",
		"fasthttp.ServeFile(ctx, \"\")",
		"_ = ctx.URI().Path()",
		"_ = ctx.URI().PathOriginal()",
		"ctx.URI().SetQueryString(\"\")",
	} {
		line("%s", shape)
	}
	b.WriteString("}\n")
	src := b.String()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parsing the probe:\n%s\n%v", src, err)
	}
	info := newBodyGuardInfo()
	var typeErrs []error
	conf := types.Config{Importer: prog.imp, Error: func(err error) { typeErrs = append(typeErrs, err) }}
	_, _ = conf.Check("probe", fset, []*ast.File{f}, info)
	if len(typeErrs) > 0 {
		t.Fatalf("precondition: the probe does not type-check, so the scanner would read it wrongly: %v\n%s",
			typeErrs, src)
	}

	lines := strings.Split(src, "\n")
	marked := map[int]bool{}
	for i, l := range lines {
		if strings.HasSuffix(l, "// want") {
			marked[i+1] = true
		}
	}
	if len(marked) != want {
		t.Fatalf("precondition: %d lines marked want, and %d written as such", len(marked), want)
	}
	minimum := len(requestMethodsRefused) + len(headerMethodsRefused) + 5*len(headerMethodsKeyed) +
		len(requestCtxMethodsRefused) + len(fasthttpFuncsRefused) + len(adaptorFuncsRefused) +
		2*len(serverHandlerFields) + len(importsRefused) + len(uriMethodsRefused) + 2*overridden +
		len(fasthttpServeFuncs)
	if overridden < 6 {
		t.Fatalf("precondition: %d Fiber overrides written; Ctx's Path and Method, Req's Method, DefaultCtx's Path "+
			"and Method, and DefaultReq's Method are six", overridden)
	}
	if want < minimum {
		t.Fatalf("precondition: %d lines marked want; one for each entry of every list is %d", want, minimum)
	}

	got := map[int][]string{}
	for _, finding := range scanForBodyDetaching(fset, []*ast.File{f}, []string{"probe.go"}, info, prog.rules) {
		got[finding.line] = append(got[finding.line], finding.what)
	}
	for _, l := range slices.Sorted(maps.Keys(marked)) {
		if len(got[l]) == 0 {
			t.Errorf("line %d is marked want, and the scanner found nothing there: %q", l, lines[l-1])
		}
	}
	for _, l := range slices.Sorted(maps.Keys(got)) {
		if !marked[l] {
			t.Errorf("line %d is not marked, and the scanner found %v there: %q", l, got[l], lines[l-1])
		}
	}
}
