package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
)

// Extraction builds the map[string]any that apischema.Properties.Validate
// consumes. It is the seam between Fiber and the engine, and the engine
// deliberately knows nothing about HTTP — so every decision about what the
// wire form of a parameter MEANS is made here.
//
// The rule that everything else follows from: a key the request did not
// carry is OMITTED from the map. It is never inserted as "".
//
// apischema treats an empty string as a supplied value that does NOT fall
// back to the default (see present() in validate.go, and the note on it).
// So extracting an absent query parameter as "" would mark every optional
// parameter on every request as supplied, and Params.Has would answer true
// for a parameter nobody sent — collapsing the three-state read that this
// whole effort exists to provide back into the two states a hand-rolled
// `if req.X == ""` check already had. c.Params and c.Query both return ""
// for "absent", so every read here has to ask a presence question rather
// than test the returned string.

// extract reads one request into the parameter map.
func (e Endpoint) extract(c fiber.Ctx) (map[string]any, error) {
	query := queryValues(c)
	body, err := e.bodyValues(c)
	if err != nil {
		return nil, err
	}

	out := make(map[string]any, len(e.Parameters))
	// Each name the endpoint answers to, mapped to the source it is
	// declared to arrive from. Iteration order below does not matter:
	// Compile has already rejected an alias that collides with a declared
	// parameter or that two parameters both claim, so no two parameters
	// can share a wire name and no entry can overwrite another.
	declared := make(map[string]apischema.Source, len(e.Parameters))

	read := func(wire string, src apischema.Source) {
		declared[wire] = src
		if v, ok := readSource(c, src, wire, query, body); ok {
			out[wire] = v
		}
	}
	for name, prop := range e.Parameters {
		src := apischema.ResolveSource(name, prop, e.Method, e.pathParams)
		read(name, src)
		// The alias is read from the SAME source: it is a second spelling
		// of one parameter, not a second place to put it. Both are copied
		// through when both are present, so that Validate reports the
		// collision itself rather than one of them silently winning.
		if prop.Alias != "" {
			read(prop.Alias, src)
		}
	}

	// A declared parameter sent to the wrong place. Dropping it silently
	// is what makes a caller's request "not work for no reason", and
	// accepting it would let a body-only parameter — a password, a token
	// — be moved into a URL that proxies and access logs record.
	if err := checkMisplaced(declared, query, body); err != nil {
		return nil, err
	}

	// Everything the caller sent that the endpoint does not declare is
	// carried through so that Validate rejects it (PVE's
	// additionalProperties => 0). Filtering it out here instead would
	// make that rejection vacuous, and a misspelled parameter would go
	// back to being silently ignored — which is the single most common
	// way a request "succeeds" while doing nothing the caller asked for.
	//
	// Order is not fixed here and does not need to be: every unknown key
	// ends up in the map, and Validate visits the merged map in name
	// order, so which one it names is deterministic either way. Body
	// precedes query only so that a key sent twice resolves the same way
	// every time.
	for _, src := range []map[string]any{body, query} {
		for key, v := range src {
			if _, isDeclared := declared[key]; isDeclared {
				continue
			}
			if _, dup := out[key]; dup {
				continue
			}
			out[key] = v
		}
	}

	return out, nil
}

// readSource fetches one key from one source, reporting presence
// separately from the value. The bool is the whole point — see the file
// comment.
func readSource(c fiber.Ctx, src apischema.Source, key string, query, body map[string]any) (any, bool) {
	switch src {
	case apischema.SourcePath:
		// Fiber returns "" both for a param the route does not declare
		// and for one matched against an empty segment. Neither is a
		// value, and a required parameter is better reported as missing
		// than as an empty string that fails some format check.
		v := c.Params(key)
		if v == "" {
			return nil, false
		}
		return v, true
	case apischema.SourceQuery:
		v, ok := query[key]
		return v, ok
	case apischema.SourceBody:
		v, ok := body[key]
		return v, ok
	default:
		return nil, false
	}
}

// checkMisplaced reports a declared parameter that arrived in a source it
// was not declared in. Names are visited in order so that a request with
// several misplaced parameters always reports the same one.
func checkMisplaced(declared map[string]apischema.Source, query, body map[string]any) error {
	for _, wire := range slices.Sorted(maps.Keys(declared)) {
		src := declared[wire]
		for _, other := range []struct {
			src    apischema.Source
			values map[string]any
			where  string
		}{
			{apischema.SourceQuery, query, "as a query parameter"},
			{apischema.SourceBody, body, "in the request body"},
		} {
			if other.src == src {
				continue
			}
			if _, ok := other.values[wire]; !ok {
				continue
			}
			return fiber.NewError(fiber.StatusBadRequest,
				fmt.Sprintf("%s: must be sent %s, not %s", wire, sourceDescription(src), other.where))
		}
	}
	return nil
}

func sourceDescription(src apischema.Source) string {
	switch src {
	case apischema.SourcePath:
		return "in the request path"
	case apischema.SourceBody:
		return "in the request body"
	case apischema.SourceQuery:
		return "as a query parameter"
	default:
		return string(src)
	}
}

// queryValues reads the query string into the map Validate consumes.
//
// It walks the raw args rather than calling c.Queries(), which flattens a
// repeated key to its last value — and a repeated key is exactly how a
// query string spells an array. One occurrence stays a string (apischema
// accepts a lone scalar as a one-element array), several become []string.
//
// "?flag" with no "=" arrives as the empty string, and an empty string is
// a supplied value here, not an absent one. It will fail whatever format,
// pattern or bound the parameter declares, which is the honest answer:
// Proxmox spells a true boolean "flag=1", and guessing that a bare key
// means true would be inventing a value the caller did not send.
func queryValues(c fiber.Ctx) map[string]any {
	args := c.Request().URI().QueryArgs()
	if args.Len() == 0 {
		return nil
	}
	// fasthttp's iterator yields slices that are invalid outside the
	// loop, so both the key and the value are converted to string (which
	// copies) before anything keeps a reference to them.
	multi := make(map[string][]string, args.Len())
	order := make([]string, 0, args.Len())
	for k, v := range args.All() {
		key := string(k)
		if _, seen := multi[key]; !seen {
			order = append(order, key)
		}
		multi[key] = append(multi[key], string(v))
	}

	out := make(map[string]any, len(order))
	for _, key := range order {
		values := multi[key]
		if len(values) == 1 {
			out[key] = values[0]
			continue
		}
		out[key] = values
	}
	return out
}

// maxUndeclaredBodyBytes bounds the body read on an endpoint that declares
// no body parameter. See the comment at its use in bodyValues: without a
// bound, a Deferred route hands an unauthorized caller a 32 MiB buffer.
const maxUndeclaredBodyBytes = 64 << 10

// hasContentCoding reports whether the request names any content coding
// other than identity, on ANY of its Content-Encoding field lines.
//
// It walks the headers the way c.Body() does before it decodes — every field
// line whose name matches case-insensitively, combined into one list (RFC 9110
// §5.2) — because anything narrower is a bypass. fasthttp's
// Header.ContentEncoding() returns the first line alone, so a check built on
// it would pass "identity" followed by a second "gzip" line, and an empty
// line followed by "gzip", and c.Body() would inflate both. Header.PeekAll
// would be no better once header normalising is off: it matches the name
// exactly, so "content-encoding: gzip" after "Content-Encoding: identity"
// would hide from it and not from c.Body(). Empty list elements are
// skipped, as §5.6.1.2 requires and as c.Body() does too, so an empty
// element can hide nothing. "identity" is §12.5.3's synonym for no
// encoding: §8.4 says a sender SHOULD NOT send it, but c.Body() reads it as
// the no-op it is, and refusing it would refuse a body that decodes to
// itself.
func hasContentCoding(c fiber.Ctx) bool {
	for name, line := range c.Request().Header.All() {
		if !bytes.EqualFold(name, []byte(fiber.HeaderContentEncoding)) {
			continue
		}
		for coding := range bytes.SplitSeq(line, []byte{','}) {
			if coding = bytes.TrimSpace(coding); len(coding) > 0 && !bytes.EqualFold(coding, []byte("identity")) {
				return true
			}
		}
	}
	return false
}

// bodyValues decodes the request body, or reports that there is nothing to
// decode.
//
// Three states have to stay distinct, and only one of them is an error:
//
//	no body at all      an empty map — a required body parameter is then
//	                    reported as missing, naming the field
//	"{}"                the same empty map; the caller sent an object and
//	                    filled nothing in
//	malformed JSON      400, because we cannot tell which of the two it
//	                    meant to be
//
// A body is decoded when the schema declares a body parameter, and — for
// the mutating verbs — whenever one was sent at all, so that a payload
// the endpoint declares nothing for comes back as "unknown parameter"
// rather than being silently discarded. A JSON body on a GET is ignored
// unless a parameter explicitly declares SourceBody, which is what makes
// the GET case answerable at all: GET semantics say the body carries no
// meaning, and inventing one for it would let a URL and a body disagree.
func (e Endpoint) bodyValues(c fiber.Ctx) (map[string]any, error) {
	required := e.declaresBodyParam()
	if !required && !isMutatingMethod(e.Method) {
		return nil, nil
	}

	// Everything below decides whether to decode WITHOUT touching the
	// body, and that ordering is load-bearing rather than tidy. Nexara
	// runs Fiber with StreamRequestBody and DisablePreParseMultipartForm
	// so that an ISO upload is never buffered; c.Body() defeats both —
	// fasthttp drains the whole stream into a 32 MiB-capped buffer AND
	// closes it, so the upload handler that reads the stream itself finds
	// it already consumed.

	// A declared-empty body is asked about through the header, not by
	// reading: this is the "no body at all" case, and it must not become
	// a content-type complaint. A chunked body reports -1 and falls
	// through to the check below.
	if c.Request().Header.ContentLength() == 0 {
		return nil, nil
	}

	// An ABSENT content type counts as non-JSON. RFC 9110 §8.3 allows a
	// body with no type, so the header is caller-controlled — treating
	// absent as "probably JSON, let's look" would hand any caller the
	// drain this gate exists to prevent, and on a Deferred route no
	// permission check has run yet.
	if !isJSONContentType(c.Get(fiber.HeaderContentType)) {
		if required {
			return nil, fiber.NewError(fiber.StatusBadRequest,
				"request body must be sent as application/json")
		}
		return nil, nil
	}

	// An endpoint that declares no body parameter still parses a SMALL
	// body, because silently discarding a payload the caller meant is the
	// failure this package exists to prevent — a body here becomes an
	// "unknown parameter" 400 further down.
	//
	// The BOUND is the load-bearing part. c.Body() drains up to Fiber's
	// 32 MiB limit, the upload route is exempt from the 10 MiB body-limit
	// middleware, and a Deferred route runs extraction BEFORE any
	// permission check — so an unbounded read here lets an authenticated
	// caller holding no grant force a 32 MiB buffer per concurrent
	// request, just by sending a JSON content type to a route that wants
	// multipart. A chunked body reports -1 and cannot be sized before
	// reading, so it is refused unread rather than trusted.
	//
	// 64 KiB is far above any real "you sent something we do not take"
	// payload and far below anything worth buffering unauthenticated.
	//
	// The bound is on Content-Length, so it measures the body as it CROSSES
	// THE WIRE — and c.Body() transparently decodes a Content-Encoding (gzip,
	// deflate, br, zstd) up to Fiber's 32 MiB BodyLimit. Left alone, a gzip
	// body of about 32 KiB — or about 1 KiB of zstd — passed the check and
	// inflated to 32 MiB of heap before any permission check ran, which is
	// the buffer this bound exists to deny. An endpoint that takes no body has
	// no use for an encoded one, so an encoded body is refused here, unread:
	// RFC 9110 §8.4 permits a 415 for a content coding the server will not
	// take, and §12.5.3 asks that the refusal name what it would have taken
	// in Accept-Encoding.
	//
	// This path then reads c.BodyRaw(), which never decodes. That is a second
	// layer, not the check: it matters only if hasContentCoding ever misses a
	// coding c.Body() would have decoded, and it turns that miss into a JSON
	// 400 on compressed bytes instead of 32 MiB of heap. Nothing can test it
	// while the check above holds.
	if !required {
		if n := c.Request().Header.ContentLength(); n < 0 || n > maxUndeclaredBodyBytes {
			return nil, fiber.NewError(fiber.StatusBadRequest,
				"this endpoint accepts no request body")
		}
		if hasContentCoding(c) {
			c.Set(fiber.HeaderAcceptEncoding, "identity")
			return nil, fiber.NewError(fiber.StatusUnsupportedMediaType,
				"this endpoint accepts no encoded request body")
		}
	}

	var body []byte
	if required {
		body = c.Body()
	} else {
		body = c.BodyRaw()
	}
	raw := bytes.TrimSpace(body)
	if len(raw) == 0 {
		return nil, nil
	}

	// UseNumber keeps an integer exact. Without it encoding/json widens
	// every number to float64, and past 2^53 that loses precision — which
	// would quietly defeat apischema's checkIntBounds, written precisely
	// so that a value one above a declared maximum cannot compare equal
	// to it. Byte counts and nanosecond timestamps both live up there.
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var decoded any
	if err := dec.Decode(&decoded); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "request body is not valid JSON")
	}
	if dec.More() {
		return nil, fiber.NewError(fiber.StatusBadRequest, "request body is not valid JSON")
	}

	switch v := decoded.(type) {
	case nil:
		// Literal "null". Every client that produces it means "nothing",
		// the same way apischema's present() reads a null value.
		return nil, nil
	case map[string]any:
		return v, nil
	default:
		return nil, fiber.NewError(fiber.StatusBadRequest, "request body must be a JSON object")
	}
}

// declaresBodyParam reports whether any parameter is read from the body.
func (e Endpoint) declaresBodyParam() bool {
	for name, prop := range e.Parameters {
		if apischema.ResolveSource(name, prop, e.Method, e.pathParams) == apischema.SourceBody {
			return true
		}
	}
	return false
}

func isMutatingMethod(method string) bool {
	switch method {
	case fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch:
		return true
	default:
		return false
	}
}

// isJSONContentType accepts application/json and the +json structured
// suffix (application/merge-patch+json and friends), with or without
// parameters such as "; charset=utf-8".
func isJSONContentType(ct string) bool {
	media, _, _ := strings.Cut(ct, ";")
	media = strings.ToLower(strings.TrimSpace(media))
	return media == fiber.MIMEApplicationJSON || strings.HasSuffix(media, "+json")
}
