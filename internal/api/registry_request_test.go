package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/valyala/fasthttp"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// These tests drive synthetic endpoints through a real Fiber app, with
// the production error handler wired in, so that what they assert is the
// status and envelope a client actually receives.

// capture records what the handler was handed.
type capture struct {
	called bool
	params *apischema.Params
}

func (c *capture) handler() Handler {
	return func(ctx fiber.Ctx, p *apischema.Params) error {
		c.called = true
		c.params = p
		return ctx.SendStatus(fiber.StatusNoContent)
	}
}

// noAuth is the explicit pass-through the parameter tests mount in place
// of authentication. mountRegistry refuses a nil, so opting out of a
// session check has to be written down rather than defaulted into.
func noAuth() fiber.Handler {
	return func(c fiber.Ctx) error { return c.Next() }
}

// newRegistryApp mounts es on a fresh app. auth is the authentication
// middleware; most of these tests pass noAuth() because they are about
// parameters, not sessions.
func newRegistryApp(t *testing.T, auth fiber.Handler, es ...Endpoint) *fiber.App {
	t.Helper()
	reg := NewRegistry()
	for _, e := range es {
		reg.Register(e)
	}
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	mountRegistry(app, reg, auth)
	return app
}

// send runs one request and returns the status and the decoded error
// envelope (zero-valued when the response carried no envelope).
func send(t *testing.T, app *fiber.App, req *http.Request) (int, ErrorResponse) {
	t.Helper()
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var env ErrorResponse
	_ = json.Unmarshal(body, &env)
	return resp.StatusCode, env
}

func jsonRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSON)
	return req
}

// widgetEndpoint is the fixture most request tests mount: one required
// path parameter, a mix of optional query parameters covering every
// facet that can reject a value, and an array.
func widgetEndpoint(cap *capture) Endpoint {
	return Endpoint{
		Method:      fiber.MethodGet,
		Path:        "/api/v1/clusters/:cluster_id/widgets",
		Description: "List widgets.",
		Group:       "Widgets",
		Permissions: Permissions{SelfService: "synthetic route; authorization is exercised separately"},
		Parameters: apischema.Properties{
			"cluster_id": apischema.StdOption("cluster-id"),
			"node":       apischema.StdOption("node-name").AsOptional(),
			"limit":      {Type: apischema.Integer, Optional: true, Default: 50, Minimum: apischema.Ptr(1.0), Maximum: apischema.Ptr(500.0)},
			"bus":        {Type: apischema.String, Optional: true, Enum: []string{"ide", "sata", "scsi", "virtio"}},
			"name":       {Type: apischema.String, Optional: true, Pattern: `^[a-z0-9-]+$`},
			"tags":       {Type: apischema.Array, Optional: true, Items: &apischema.Property{Type: apischema.String}},
		},
		Handler: cap.handler(),
	}
}

const testClusterID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

func TestRegistryRejectsInvalidRequestsWith400(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   string
	}{
		{
			name:   "wrong type",
			target: "/api/v1/clusters/" + testClusterID + "/widgets?limit=many",
			want:   "limit: expected an integer",
		},
		{
			name:   "out of bounds",
			target: "/api/v1/clusters/" + testClusterID + "/widgets?limit=9000",
			want:   "limit: must be at most 500",
		},
		{
			name:   "enum miss",
			target: "/api/v1/clusters/" + testClusterID + "/widgets?bus=usb",
			want:   "bus: expected one of: ide, sata, scsi, virtio",
		},
		{
			name:   "pattern miss",
			target: "/api/v1/clusters/" + testClusterID + "/widgets?name=Widget_One",
			want:   "name: value does not match the expected pattern",
		},
		{
			name:   "format rejection",
			target: "/api/v1/clusters/" + testClusterID + "/widgets?node=not%20a%20node",
			want:   "node: expected a node name",
		},
		{
			// PVE's additionalProperties => 0. Without the extraction
			// carrying unknown keys through, this would be silently
			// ignored and the caller would get a 200 that did nothing
			// they asked for.
			name:   "unknown parameter",
			target: "/api/v1/clusters/" + testClusterID + "/widgets?limti=10",
			want:   "limti: unknown parameter",
		},
		{
			name:   "path parameter that fails its own format",
			target: "/api/v1/clusters/not-a-uuid/widgets",
			want:   "cluster_id: expected a UUID",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), widgetEndpoint(cap))

			status, env := send(t, app, httptest.NewRequest(http.MethodGet, tc.target, nil))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (%q)", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, strings.SplitN(tc.want, ":", 2)[0]+":") {
				t.Errorf("message = %q, want it to name the field first", env.Message)
			}
			if !strings.Contains(env.Message, tc.want) {
				t.Errorf("message = %q, want it to contain %q", env.Message, tc.want)
			}
			if env.Error != "bad_request" {
				t.Errorf("error = %q, want bad_request", env.Error)
			}
			if cap.called {
				t.Error("the handler ran on an invalid request")
			}
		})
	}
}

func TestRegistryRejectsAMissingRequiredParameter(t *testing.T) {
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), Endpoint{
		Method:      fiber.MethodPost,
		Path:        "/api/v1/widgets",
		Description: "Create a widget.",
		Group:       "Widgets",
		Permissions: Permissions{SelfService: "synthetic route"},
		Parameters: apischema.Properties{
			"name": {Type: apischema.String, MinLength: apischema.Ptr(1)},
		},
		Handler: cap.handler(),
	})

	status, env := send(t, app, jsonRequest(http.MethodPost, "/api/v1/widgets", `{}`))
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if env.Message != "name: missing required parameter" {
		t.Errorf("message = %q, want it to name the missing field", env.Message)
	}
}

// TestRegistryAnswers500ForADeclarationBug pins the OTHER half of the
// error mapping: a plain error out of Validate is OUR mistake, not the
// caller's, so it must not come back as a 400 blaming them — and the
// detail must not come back at all, since it describes internal parameter
// plumbing to whoever happened to send the request.
//
// Reaching this needs Register to be bypassed, because Register compiles
// every schema and Compile rejects a numeric bound on a string. That is
// the point: after registration this branch is unreachable, and the
// bypass here is what proves the branch is still correct if it ever is
// reached.
func TestRegistryAnswers500ForADeclarationBug(t *testing.T) {
	cap := &capture{}
	broken := Endpoint{
		Method:      fiber.MethodGet,
		Path:        "/api/v1/widgets",
		Description: "Broken on purpose.",
		Group:       "Widgets",
		Permissions: Permissions{SelfService: "synthetic route"},
		Parameters: apischema.Properties{
			// A numeric bound on a string: checkDeclaration refuses it on
			// every request as a plain error, never a *ValidationError.
			"name": {Type: apischema.String, Minimum: apischema.Ptr(1.0)},
		},
		Handler: cap.handler(),
	}
	if err := broken.Parameters.Compile(); err == nil {
		t.Fatal("this fixture is supposed to be a schema Compile rejects")
	}

	reg := NewRegistry()
	// Deliberately bypassing Register: it would panic, which is exactly
	// the protection this test is here to show the absence of.
	broken.pathParams = pathParamNames(broken.Path)
	reg.endpoints = append(reg.endpoints, broken)

	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	mountRegistry(app, reg, noAuth())

	logged := captureSlog(t)

	status, env := send(t, app, httptest.NewRequest(http.MethodGet, "/api/v1/widgets?name=x", nil))
	if status != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — a schema defect is not the caller's fault", status)
	}
	if env.Message != "Request validation failed" {
		t.Errorf("message = %q, want the constant that discloses nothing about the schema", env.Message)
	}
	if cap.called {
		t.Error("the handler ran despite an invalid schema")
	}

	// The detail has to go SOMEWHERE — a 500 whose cause is nowhere is an
	// outage nobody can diagnose. Server-side is the only place it may go.
	out := logged()
	for _, want := range []string{"numeric bounds", "/api/v1/widgets"} {
		if !strings.Contains(out, want) {
			t.Errorf("server log = %q, want it to record %q", out, want)
		}
	}
}

// captureSlog redirects the default logger for the duration of a test and
// returns an accessor for what was written to it.
func captureSlog(t *testing.T) func() string {
	t.Helper()
	var buf bytes.Buffer
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(saved) })
	return buf.String
}

func TestRegistryAppliesDeclaredDefaults(t *testing.T) {
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), widgetEndpoint(cap))

	status, env := send(t, app, httptest.NewRequest(http.MethodGet,
		"/api/v1/clusters/"+testClusterID+"/widgets", nil))
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204", status, env.Message)
	}
	if !cap.called {
		t.Fatal("the handler did not run")
	}
	if got := cap.params.Int("limit"); got != 50 {
		t.Errorf("limit = %d, want the declared default 50", got)
	}
	if cap.params.Has("limit") {
		t.Error("Has(\"limit\") is true for a parameter the caller never sent — " +
			"a default is not something the caller supplied")
	}
	if got := cap.params.String("cluster_id"); got != testClusterID {
		t.Errorf("cluster_id = %q, want the path value", got)
	}
	if !cap.params.Has("cluster_id") {
		t.Error("Has(\"cluster_id\") is false for a path parameter that was supplied")
	}
}

// TestRegistryOmitsUnseenKeysRatherThanPassingEmptyStrings is the
// invariant the whole extraction layer is built around.
//
// apischema treats an empty string as a SUPPLIED value that does not fall
// back to the default. So extracting an absent query parameter as "" —
// which is what c.Query returns for one — would mark every optional
// parameter on every request as supplied, and collapse the three-state
// read (default / unset / supplied) back into the two states a
// hand-rolled `if req.X == ""` check already had. That three-state read
// is the reason apischema exists.
func TestRegistryOmitsUnseenKeysRatherThanPassingEmptyStrings(t *testing.T) {
	newApp := func(cap *capture) *fiber.App {
		return newRegistryApp(t, noAuth(), Endpoint{
			Method:      fiber.MethodGet,
			Path:        "/api/v1/widgets",
			Description: "List widgets.",
			Group:       "Widgets",
			Permissions: Permissions{SelfService: "synthetic route"},
			Parameters: apischema.Properties{
				"filter": {Type: apischema.String, Optional: true, Default: "all"},
				"bare":   {Type: apischema.String, Optional: true},
			},
			Handler: cap.handler(),
		})
	}

	t.Run("omitted", func(t *testing.T) {
		cap := &capture{}
		status, env := send(t, newApp(cap), httptest.NewRequest(http.MethodGet, "/api/v1/widgets", nil))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if v, supplied := cap.params.OptString("filter"); v != "all" || supplied {
			t.Errorf("OptString(\"filter\") = (%q, %v), want (\"all\", false) — "+
				"an omitted parameter must take its default and report itself unsupplied", v, supplied)
		}
		if v, supplied := cap.params.OptString("bare"); v != "" || supplied {
			t.Errorf("OptString(\"bare\") = (%q, %v), want (\"\", false)", v, supplied)
		}
	})

	t.Run("supplied empty", func(t *testing.T) {
		cap := &capture{}
		status, env := send(t, newApp(cap), httptest.NewRequest(http.MethodGet, "/api/v1/widgets?filter=", nil))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if v, supplied := cap.params.OptString("filter"); v != "" || !supplied {
			t.Errorf("OptString(\"filter\") = (%q, %v), want (\"\", true) — "+
				"an explicitly empty value is supplied and must NOT fall back to the default", v, supplied)
		}
		// The parameter the caller did not name stays unsupplied even
		// though another one on the same request was.
		if cap.params.Has("bare") {
			t.Error("Has(\"bare\") is true for a key the query string never carried")
		}
	})

	t.Run("an optional path segment that was not supplied", func(t *testing.T) {
		// The path branch of the same rule. c.Params returns "" both for
		// an unmatched optional segment and for one matched empty, and
		// passing that "" on would mark the parameter supplied.
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), Endpoint{
			Method:      fiber.MethodGet,
			Path:        "/api/v1/widgets/:name?",
			Description: "Get a widget, or all of them.",
			Group:       "Widgets",
			Permissions: Permissions{SelfService: "synthetic route"},
			Parameters: apischema.Properties{
				"name": {Type: apischema.String, Optional: true, Default: "all", Source: apischema.SourcePath},
			},
			Handler: cap.handler(),
		})

		status, env := send(t, app, httptest.NewRequest(http.MethodGet, "/api/v1/widgets", nil))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if v, supplied := cap.params.OptString("name"); v != "all" || supplied {
			t.Errorf("OptString(\"name\") = (%q, %v), want (\"all\", false) — "+
				"an unmatched optional path segment must not read as supplied", v, supplied)
		}

		cap.called = false
		status, env = send(t, app, httptest.NewRequest(http.MethodGet, "/api/v1/widgets/alpha", nil))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if v, supplied := cap.params.OptString("name"); v != "alpha" || !supplied {
			t.Errorf("OptString(\"name\") = (%q, %v), want (\"alpha\", true)", v, supplied)
		}
	})

	t.Run("a bare key with no equals is an empty value, not a missing one", func(t *testing.T) {
		cap := &capture{}
		status, env := send(t, newApp(cap), httptest.NewRequest(http.MethodGet, "/api/v1/widgets?bare", nil))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if v, supplied := cap.params.OptString("bare"); v != "" || !supplied {
			t.Errorf("OptString(\"bare\") = (%q, %v), want (\"\", true)", v, supplied)
		}
	})
}

func TestRegistryReadsRepeatedQueryKeysAsAnArray(t *testing.T) {
	cases := []struct {
		name   string
		target string
		want   []string
	}{
		{"one occurrence", "?tags=alpha", []string{"alpha"}},
		{"several occurrences", "?tags=alpha&tags=beta&tags=gamma", []string{"alpha", "beta", "gamma"}},
		// Deliberately NOT split on commas: a value that legitimately
		// contains one would be torn in half (see apischema's toSlice).
		{"a comma is part of the value", "?tags=alpha,beta", []string{"alpha,beta"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), widgetEndpoint(cap))

			status, env := send(t, app, httptest.NewRequest(http.MethodGet,
				"/api/v1/clusters/"+testClusterID+"/widgets"+tc.target, nil))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			got := cap.params.Strings("tags")
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("tags = %v, want %v", got, tc.want)
			}
		})
	}
}

func bodyEndpoint(cap *capture) Endpoint {
	return Endpoint{
		Method:      fiber.MethodPost,
		Path:        "/api/v1/widgets",
		Description: "Create a widget.",
		Group:       "Widgets",
		Permissions: Permissions{SelfService: "synthetic route"},
		Parameters: apischema.Properties{
			"name":  {Type: apischema.String, Optional: true, Default: "unnamed"},
			"count": {Type: apischema.Integer, Optional: true},
		},
		Handler: cap.handler(),
	}
}

func TestRegistryBodyStates(t *testing.T) {
	t.Run("absent body is an empty map", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), bodyEndpoint(cap))
		status, env := send(t, app, httptest.NewRequest(http.MethodPost, "/api/v1/widgets", nil))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if v, supplied := cap.params.OptString("name"); v != "unnamed" || supplied {
			t.Errorf("OptString(\"name\") = (%q, %v), want the default and unsupplied", v, supplied)
		}
	})

	t.Run("an empty object is the same as no body", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), bodyEndpoint(cap))
		status, env := send(t, app, jsonRequest(http.MethodPost, "/api/v1/widgets", `{}`))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if cap.params.Has("name") {
			t.Error("Has(\"name\") is true for a key {} does not carry")
		}
	})

	t.Run("malformed JSON is a 400", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), bodyEndpoint(cap))
		status, env := send(t, app, jsonRequest(http.MethodPost, "/api/v1/widgets", `{"name":`))
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		if !strings.Contains(env.Message, "not valid JSON") {
			t.Errorf("message = %q, want it to say the body is not valid JSON", env.Message)
		}
		if cap.called {
			t.Error("the handler ran on a malformed body")
		}
	})

	t.Run("a JSON array body is a 400", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), bodyEndpoint(cap))
		status, env := send(t, app, jsonRequest(http.MethodPost, "/api/v1/widgets", `[1,2]`))
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		if !strings.Contains(env.Message, "must be a JSON object") {
			t.Errorf("message = %q", env.Message)
		}
	})

	t.Run("a null body is the same as no body", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), bodyEndpoint(cap))
		status, env := send(t, app, jsonRequest(http.MethodPost, "/api/v1/widgets", `null`))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if cap.params.Has("name") {
			t.Error("Has(\"name\") is true for a null body")
		}
	})

	t.Run("an integer keeps its precision", func(t *testing.T) {
		// encoding/json without UseNumber widens every number to float64,
		// and past 2^53 that loses precision — which would defeat
		// apischema's checkIntBounds, written so a value one above a
		// declared maximum cannot compare equal to it.
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), bodyEndpoint(cap))
		status, env := send(t, app, jsonRequest(http.MethodPost, "/api/v1/widgets", `{"count":9007199254740993}`))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got := cap.params.Int("count"); got != 9007199254740993 {
			t.Errorf("count = %d, want 9007199254740993 — the body decoder lost precision", got)
		}
	})

	t.Run("a body the endpoint declares nothing for is rejected", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), Endpoint{
			Method:      fiber.MethodPost,
			Path:        "/api/v1/widgets/:id/start",
			Description: "Start a widget.",
			Group:       "Widgets",
			Permissions: Permissions{SelfService: "synthetic route"},
			Parameters: apischema.Properties{
				"id": {Type: apischema.String, Format: "uuid", Source: apischema.SourcePath},
			},
			Handler: cap.handler(),
		})
		status, env := send(t, app, jsonRequest(http.MethodPost,
			"/api/v1/widgets/"+testClusterID+"/start", `{"force":true}`))
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want 400 — a payload nobody reads must not pass silently", status)
		}
		if !strings.Contains(env.Message, "force: unknown parameter") {
			t.Errorf("message = %q, want it to name the unread field", env.Message)
		}
	})

	t.Run("a JSON body on a GET is ignored", func(t *testing.T) {
		// GET semantics say the body carries no meaning. Reading one
		// would let a URL and a body disagree about the same parameter,
		// with nothing to say which wins.
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), widgetEndpoint(cap))
		req := jsonRequest(http.MethodGet,
			"/api/v1/clusters/"+testClusterID+"/widgets?limit=7", `{"limit":1,"nonsense":true}`)
		status, env := send(t, app, req)
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got := cap.params.Int("limit"); got != 7 {
			t.Errorf("limit = %d, want 7 from the query string", got)
		}
	})

	t.Run("a non-JSON body is left to the handler", func(t *testing.T) {
		// Multipart uploads keep working: nothing is decoded, and the
		// endpoint's parameters come from the path and the query.
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), Endpoint{
			Method:      fiber.MethodPost,
			Path:        "/api/v1/uploads",
			Description: "Upload a file.",
			Group:       "Widgets",
			Permissions: Permissions{Deferred: "the content type decides which permission applies"},
			Parameters: apischema.Properties{
				"filename": {Type: apischema.String, Optional: true, Source: apischema.SourceQuery},
			},
			Handler: cap.handler(),
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads?filename=disk.img", strings.NewReader("binary"))
		req.Header.Set(fiber.HeaderContentType, fiber.MIMEOctetStream)
		status, env := send(t, app, req)
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if got := cap.params.String("filename"); got != "disk.img" {
			t.Errorf("filename = %q, want it read from the query", got)
		}
	})

	t.Run("a non-JSON body where one was declared is a 400", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), bodyEndpoint(cap))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/widgets", strings.NewReader("name=x"))
		req.Header.Set(fiber.HeaderContentType, "application/x-www-form-urlencoded")
		status, env := send(t, app, req)
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		if !strings.Contains(env.Message, "application/json") {
			t.Errorf("message = %q", env.Message)
		}
	})
}

// TestRegistryLeavesAStreamedNonJSONBodyAlone is the reason bodyValues
// settles the content type BEFORE it asks for the body.
//
// Production runs Fiber with StreamRequestBody and
// DisablePreParseMultipartForm so an ISO upload is never buffered.
// c.Body() defeats both: fasthttp drains the stream into a buffer and
// CLOSES it, so an upload handler that reads the stream itself finds
// nothing there. Extraction must therefore decline a non-JSON body
// without ever asking for it.
func TestRegistryLeavesAStreamedNonJSONBodyAlone(t *testing.T) {
	const payload = "the bytes an upload handler has to read for itself"

	// The content type is caller-controlled, and an ABSENT one is the
	// case that matters most: RFC 9110 §8.3 permits a body with no type,
	// so "no header means probably JSON, let's look" would hand any
	// caller the drain this gate exists to prevent. On a Deferred route
	// no permission check has run yet, so a Viewer with no grants at all
	// reaches it.
	for _, contentType := range []string{fiber.MIMEOctetStream, ""} {
		name := contentType
		if name == "" {
			name = "absent content type"
		}
		t.Run(name, func(t *testing.T) {
			var sawStream bool
			var got []byte
			app := fiber.New(fiber.Config{
				ErrorHandler:                 errorHandler,
				StreamRequestBody:            true,
				DisablePreParseMultipartForm: true,
			})
			reg := NewRegistry()
			reg.Register(Endpoint{
				Method:      fiber.MethodPost,
				Path:        "/api/v1/uploads",
				Description: "Upload a file.",
				Group:       "Widgets",
				Permissions: Permissions{Deferred: "the content type decides which permission applies"},
				Parameters: apischema.Properties{
					"filename": {Type: apischema.String, Optional: true, Source: apischema.SourceQuery},
				},
				Handler: func(c fiber.Ctx, _ *apischema.Params) error {
					if s := c.Request().BodyStream(); s != nil {
						sawStream = true
						got, _ = io.ReadAll(s)
					}
					return c.SendStatus(fiber.StatusNoContent)
				},
			})
			mountRegistry(app, reg, noAuth())

			req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads?filename=disk.img", strings.NewReader(payload))
			if contentType != "" {
				req.Header.Set(fiber.HeaderContentType, contentType)
			} else {
				req.Header.Del(fiber.HeaderContentType)
			}
			if status, env := send(t, app, req); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			if !sawStream {
				t.Fatal("the handler found no body stream — extraction consumed and closed it, " +
					"which is what would break every streamed upload endpoint")
			}
			if string(got) != payload {
				t.Errorf("stream = %q, want %q", got, payload)
			}
		})
	}
}

// TestRegistryRefusesAnUntypedBodyWhereJSONWasDeclared is the other side
// of treating an absent content type as non-JSON: where the schema really
// did want a body, the caller is told so rather than having their bytes
// guessed at.
func TestRegistryRefusesAnUntypedBodyWhereJSONWasDeclared(t *testing.T) {
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), bodyEndpoint(cap))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/widgets", strings.NewReader(`{"name":"x"}`))
	req.Header.Del(fiber.HeaderContentType)
	status, env := send(t, app, req)
	if status != fiber.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if !strings.Contains(env.Message, "application/json") {
		t.Errorf("message = %q, want it to name the content type required", env.Message)
	}
	if cap.called {
		t.Error("the handler ran on an untyped body")
	}
}

// TestRegistryLogsAHandlerPanic covers the diagnosis gap the Params
// accessors create by design: they panic on a handler bug, the app-level
// recover middleware runs with EnableStackTrace false, and errorHandler
// drops everything that is not a *fiber.Error. Without this the operator
// gets a bare 500 and the server records nothing at all.
func TestRegistryLogsAHandlerPanic(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Use(recover.New())

	reg := NewRegistry()
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        "/api/v1/widgets",
		Description: "Read a parameter it does not declare.",
		Group:       "Widgets",
		Permissions: Permissions{SelfService: "synthetic route"},
		Parameters:  apischema.Properties{},
		Handler: func(_ fiber.Ctx, p *apischema.Params) error {
			// The handler bug Params is built to make loud.
			return errors.New(p.String("undeclared"))
		},
	})
	mountRegistry(app, reg, noAuth())

	logged := captureSlog(t)

	status, _ := send(t, app, httptest.NewRequest(http.MethodGet, "/api/v1/widgets", nil))
	if status != fiber.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", status)
	}

	out := logged()
	for _, want := range []string{"registry handler panicked", "/api/v1/widgets", "undeclared"} {
		if !strings.Contains(out, want) {
			t.Errorf("server log = %q, want it to record %q", out, want)
		}
	}
}

// TestRegistryRefusesAParameterSentToTheWrongPlace covers the hole that
// carrying unknown keys through would otherwise open: a body-only
// parameter must not be satisfiable from the URL, where proxies and
// access logs record it.
func TestRegistryRefusesAParameterSentToTheWrongPlace(t *testing.T) {
	t.Run("a body parameter smuggled into the query", func(t *testing.T) {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), bodyEndpoint(cap))

		status, env := send(t, app, httptest.NewRequest(http.MethodPost, "/api/v1/widgets?name=smuggled", nil))
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		if !strings.Contains(env.Message, "must be sent in the request body") {
			t.Errorf("message = %q, want it to say where the parameter belongs", env.Message)
		}
		if cap.called {
			t.Error("the handler ran with a misplaced parameter")
		}
	})

	t.Run("a path parameter also sent in the query", func(t *testing.T) {
		// The direction that matters most: the permission middleware
		// reads the PATH, so a second spelling in the query is either
		// ignored (and the caller is baffled) or honoured (and the gate
		// and the handler act on different clusters).
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), widgetEndpoint(cap))

		status, env := send(t, app, httptest.NewRequest(http.MethodGet,
			"/api/v1/clusters/"+testClusterID+"/widgets?cluster_id="+testClusterID, nil))
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		if !strings.Contains(env.Message, "must be sent in the request path") {
			t.Errorf("message = %q, want it to say the parameter belongs in the path", env.Message)
		}
		if cap.called {
			t.Error("the handler ran with a path parameter restated in the query")
		}
	})
}

func TestRegistryNormalizesThroughTheDeclaredFormat(t *testing.T) {
	// The half a hand-rolled check never does: "500G" and the JSON number
	// 500 both have to reach Proxmox as the bare GiB count.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), Endpoint{
		Method:      fiber.MethodPost,
		Path:        "/api/v1/disks",
		Description: "Attach a disk.",
		Group:       "Widgets",
		Permissions: Permissions{SelfService: "synthetic route"},
		Parameters: apischema.Properties{
			"size": {Type: apischema.String, Format: "disk-size", Alias: "disk_size"},
		},
		Handler: cap.handler(),
	})

	for _, tc := range []struct{ body, want string }{
		{`{"size":"500G"}`, "500"},
		{`{"size":500}`, "500"},
		{`{"size":"1T"}`, "1024"},
		{`{"disk_size":"2T"}`, "2048"}, // the alias fills the same parameter
	} {
		cap.called = false
		status, env := send(t, app, jsonRequest(http.MethodPost, "/api/v1/disks", tc.body))
		if status != fiber.StatusNoContent {
			t.Fatalf("%s: status = %d (%q), want 204", tc.body, status, env.Message)
		}
		if got := cap.params.String("size"); got != tc.want {
			t.Errorf("%s: size = %q, want %q", tc.body, got, tc.want)
		}
	}

	t.Run("a name and its alias together are refused", func(t *testing.T) {
		status, env := send(t, app, jsonRequest(http.MethodPost, "/api/v1/disks",
			`{"size":"500G","disk_size":"1T"}`))
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d, want 400", status)
		}
		if !strings.Contains(env.Message, "alias") {
			t.Errorf("message = %q", env.Message)
		}
	})
}

// A Deferred route runs extraction BEFORE any permission check, so an
// unbounded body read there is reachable by an authenticated caller holding
// no grant. The storage upload is the live case: it declares no body
// parameter, it is exempt from the 10 MiB body-limit middleware, and Fiber's
// cap is 32 MiB — so sending a JSON content type to a route that wants
// multipart used to buy a 32 MiB buffer per concurrent request.
//
// The Phase 2 security review predicted this before any route was Deferred;
// migrating the upload made it live.
func TestBodyIsBoundedOnAnEndpointThatDeclaresNoBodyParameter(t *testing.T) {
	app := fiber.New()
	reg := NewRegistry()
	reg.Register(Endpoint{
		Method: "POST", Path: "/api/v1/clusters/:cluster_id/nobody",
		Description: "Declares no body parameter.", Group: "Test",
		Permissions: Permissions{Deferred: "the handler decides once it reads the stream"},
		Parameters:  apischema.Properties{"cluster_id": apischema.StdOption("cluster-id")},
		Handler:     func(c fiber.Ctx, _ *apischema.Params) error { return c.SendString("ok") },
	})
	mountRegistry(app, reg, func(c fiber.Ctx) error { return c.Next() })

	path := "/api/v1/clusters/" + testClusterID + "/nobody"

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		// The one real client shape: /auth/refresh posts exactly this, so a
		// blanket refusal would break token refresh for every caller.
		{"a tiny {} body is still parsed", "{}", fiber.StatusOK},
		{"a small unknown payload is still reported", `{"surprise":1}`, fiber.StatusBadRequest},
		{"an oversized body is refused unread", "{\"pad\":\"" + strings.Repeat("x", maxUndeclaredBodyBytes+1) + "\"}", fiber.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// httptest.NewRequest sizes the body only for the reader types
			// it recognises. An opaque reader leaves ContentLength at -1,
			// which is what a genuinely chunked request looks like — and -1
			// is exactly the case that cannot be bounded without reading.
			req := httptest.NewRequest("POST", path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			res, err := app.Test(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = res.Body.Close() }()
			if res.StatusCode != tt.wantStatus {
				got, _ := io.ReadAll(res.Body)
				t.Errorf("status = %d, want %d (body %s)", res.StatusCode, tt.wantStatus, got)
			}
		})
	}
}

// A chunked body reports a Content-Length of -1 and cannot be sized before
// reading, so it is the one shape the bound above cannot measure — and
// therefore the one an attacker would reach for. It is tested here rather
// than through app.Test because Fiber's test harness serialises
// ContentLength verbatim, emitting a literal "Content-Length: -1" header
// that fasthttp rejects while parsing, before any of this code runs.
func TestUnsizeableBodyIsRefusedUnreadOnAnEndpointDeclaringNoBodyParameter(t *testing.T) {
	app := fiber.New()
	e := Endpoint{
		Method: "POST", Path: "/api/v1/clusters/:cluster_id/nobody",
		Parameters: apischema.Properties{"cluster_id": apischema.StdOption("cluster-id")},
	}

	fctx := &fasthttp.RequestCtx{}
	fctx.Request.Header.SetMethod("POST")
	fctx.Request.Header.SetContentType("application/json")
	fctx.Request.SetBodyString(`{"pad":"x"}`)
	// Chunked is how a body arrives with no declared size. SetContentLength(-1)
	// is fasthttp's spelling of it, and must follow SetBodyString, which
	// otherwise writes a real length.
	fctx.Request.Header.SetContentLength(-1)

	ctx := app.AcquireCtx(fctx)
	defer app.ReleaseCtx(ctx)

	if got := fctx.Request.Header.ContentLength(); got != -1 {
		t.Fatalf("precondition: ContentLength = %d, want -1 — this test is not exercising the unsizeable path", got)
	}

	_, err := e.bodyValues(ctx)
	if err == nil {
		t.Fatal("an unsizeable body was accepted; it cannot be bounded before reading, so it must be refused")
	}
	var fe *fiber.Error
	if !errors.As(err, &fe) || fe.Code != fiber.StatusBadRequest {
		t.Fatalf("err = %v, want a 400", err)
	}
}
