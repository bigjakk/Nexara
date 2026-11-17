package proxmox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bigjakk/nexara/internal/pvefield"
)

// maxResponseSize caps how much data we read from a Proxmox API response (50 MB).
const maxResponseSize = 50 * 1024 * 1024

// apiClient provides shared HTTP infrastructure for Proxmox API clients.
// Both PVE (Client) and PBS (PBSClient) embed this struct.
type apiClient struct {
	httpClient *http.Client
	baseURL    string
	authHeader string
	tokenID    string // e.g. "user@pam!tokenname" — needed for terminal handshake
	tlsCfg     *tls.Config

	// tokenSecret is the bare API token secret, which authHeader already holds
	// in plaintext. It is kept apart so console.go can scrub it out of what a
	// peer echoes back even when the peer echoes it without the header around
	// it.
	tokenSecret string

	// uploadStall replaces UploadStallTimeout for this client's streamed
	// uploads when it is not zero; tests shorten it.
	uploadStall time.Duration

	// auth injects credentials into each outbound request. Set once at
	// construction and never mutated for cached clients; BootstrapClient
	// swaps it exactly once, in Login, before any concurrent use.
	//
	// May be nil on hand-built literals (some tests construct apiClient
	// directly). applyAuth falls back to authHeader in that case, so the
	// zero value behaves like the token auth this package has always used.
	auth requestAuth

	// onUnreachable is called when a request could not be connected at all
	// (dialFailed). ClientCache sets it on the PVE clients it builds, before
	// handing them out, so it can stop serving a client whose endpoint went
	// away; every other client leaves it nil and nothing changes for it.
	onUnreachable func()
}

// applyAuth attaches credentials to req. See apiClient.auth for why the nil
// case falls back to the raw authHeader.
func (a *apiClient) applyAuth(req *http.Request) {
	if a.auth == nil {
		if a.authHeader != "" {
			req.Header.Set("Authorization", a.authHeader)
		}
		return
	}
	a.auth.apply(req)
}

// newAPIClient creates an apiClient with TLS and auth configured.
// authPrefix is "PVEAPIToken" for PVE or "PBSAPIToken" for PBS.
func newAPIClient(cfg ClientConfig, authPrefix string) (*apiClient, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("proxmox: BaseURL is required")
	}
	if cfg.TokenID == "" {
		return nil, fmt.Errorf("proxmox: TokenID is required")
	}
	if cfg.TokenSecret == "" {
		return nil, fmt.Errorf("proxmox: TokenSecret is required")
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")

	httpClient, tlsCfg := buildHTTPClient(cfg.TLSFingerprint, cfg.Timeout)

	authHeader := buildAuthHeader(authPrefix, cfg.TokenID, cfg.TokenSecret)

	return &apiClient{
		httpClient: httpClient,
		baseURL:    baseURL,
		// authHeader is retained alongside auth because console.go builds a
		// websocket http.Header rather than an *http.Request and reads it
		// directly.
		authHeader:  authHeader,
		auth:        tokenAuth{header: authHeader},
		tokenID:     cfg.TokenID,
		tlsCfg:      tlsCfg,
		tokenSecret: cfg.TokenSecret,
	}, nil
}

// refuseRedirect tells net/http to NOT follow any redirect; the caller's
// Do() returns the 3xx response with the body so the caller can decide.
// We could return ErrUseLastResponse to surface the response intact, but
// the Proxmox API never legitimately redirects, so an explicit error is
// clearer in stack traces.
func refuseRedirect(req *http.Request, via []*http.Request) error {
	return fmt.Errorf("proxmox: refusing redirect from %s to %s", via[len(via)-1].URL, req.URL)
}

// buildAuthHeader constructs the Authorization header value.
// PVE uses "PVEAPIToken=user@realm!token=secret" (equals separator).
// PBS uses "PBSAPIToken=user@realm!token:secret" (colon separator).
func buildAuthHeader(prefix, tokenID, tokenSecret string) string {
	sep := "="
	if prefix == "PBSAPIToken" {
		sep = ":"
	}
	return fmt.Sprintf("%s=%s%s%s", prefix, tokenID, sep, tokenSecret)
}

// formatFingerprint returns the lowercase hex SHA-256 digest of a DER-encoded certificate.
func formatFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// do performs an authenticated GET request and unmarshals the response data into dst.
func (a *apiClient) do(ctx context.Context, path string, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	a.applyAuth(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return a.connectionFailed(err, nil)
	}
	defer resp.Body.Close()

	body, err := a.readBody(resp, nil)
	if err != nil {
		return err
	}

	return unmarshalData(body, dst)
}

// doPostRaw performs an authenticated POST request with a pre-encoded form body.
// Use this instead of doPost when url.Values.Encode() over-encodes characters
// that the target API requires unescaped (e.g. colons in Proxmox HA SIDs).
func (a *apiClient) doPostRaw(ctx context.Context, path string, rawBody string, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, strings.NewReader(rawBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	a.applyAuth(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// A body that does not parse sends nothing to scrub out but the
	// credentials; ParseQuery returns what it could read alongside the error.
	sent, _ := url.ParseQuery(rawBody)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return a.connectionFailed(err, sent)
	}
	defer resp.Body.Close()

	respBody, err := a.readBody(resp, sent)
	if err != nil {
		return err
	}

	return unmarshalData(respBody, dst)
}

// doPost performs an authenticated POST request with form-encoded body.
func (a *apiClient) doPost(ctx context.Context, path string, params url.Values, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	a.applyAuth(req)
	if params != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return a.connectionFailed(err, params)
	}
	defer resp.Body.Close()

	respBody, err := a.readBody(resp, params)
	if err != nil {
		return err
	}

	return unmarshalData(respBody, dst)
}

// doDelete performs an authenticated DELETE request.
func (a *apiClient) doDelete(ctx context.Context, path string, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	a.applyAuth(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return a.connectionFailed(err, nil)
	}
	defer resp.Body.Close()

	body, err := a.readBody(resp, nil)
	if err != nil {
		return err
	}

	return unmarshalData(body, dst)
}

// doPut performs an authenticated PUT request with form-encoded body.
func (a *apiClient) doPut(ctx context.Context, path string, params url.Values, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	a.applyAuth(req)
	if params != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return a.connectionFailed(err, params)
	}
	defer resp.Body.Close()

	respBody, err := a.readBody(resp, params)
	if err != nil {
		return err
	}

	return unmarshalData(respBody, dst)
}

// UploadStallTimeout is how long a streamed upload may go without a byte of the
// file moving before it is abandoned. It sits beside the server's own bounds
// (internal/api/body_framing.go: 60 s for a request head, five minutes for any
// other body, three minutes idle): an upload has no overall limit, since an
// ISO takes as long as it takes, but a stall this long means a peer has gone.
// doMultipart holds Proxmox's side to it, and the storage upload handler the
// browser's.
const UploadStallTimeout = 2 * time.Minute

var (
	// ErrUploadStalled is a streamed upload abandoned after UploadStallTimeout
	// with no byte of the file moving.
	ErrUploadStalled = errors.New("upload stalled")

	// ErrUploadBodyRead is a streamed upload whose file could not be read to
	// its end: the source, not Proxmox, failed. It rides alongside the read
	// error.
	ErrUploadBodyRead = errors.New("upload body read failed")

	// errUploadBodyReleased is what a read of an upload's file gets once
	// doMultipart has returned.
	errUploadBodyReleased = errors.New("upload body read after the upload returned")
)

// uploadBody is the file of a streamed upload as the transport reads it.
//
// net/http may go on reading a request body after Do has returned — when
// Proxmox answers before the body is all sent, say (RoundTrip "may do so in a
// separate goroutine even after RoundTrip returns"). The storage upload's file
// is fasthttp's request body stream, which fasthttp recycles once the handler
// returns, so a read after that reads another request's memory. release, which
// doMultipart runs before it returns, is what stops that: it waits for a read
// in progress to finish and refuses every read after it. A read is only ever
// waited for as long as UploadStallTimeout, which the handler arms on each read
// of the browser's connection.
//
// It also holds the watchdog: a timer, reset each time bytes move, that cancels
// the request when none have for the stall timeout — Proxmox has stopped
// taking them, or the file has stopped giving them. It is stopped at the end of
// the file, so waiting for Proxmox's answer is not a stall.
//
// It has a Read method and nothing else, so no io.Copy can read past it
// through a WriteTo or ReadFrom of its own.
type uploadBody struct {
	mu       sync.Mutex // held for each read of r, and by release
	r        io.Reader
	released bool

	stall   time.Duration
	timer   uploadTimer
	stalled atomic.Bool

	errMu   sync.Mutex
	readErr error
}

// uploadTimer is the watchdog's timer: a *time.Timer, or a test's fake.
type uploadTimer interface {
	Reset(time.Duration) bool
	Stop() bool
}

// afterFunc starts the watchdog's timer; tests pass a fake clock's.
type afterFunc func(time.Duration, func()) uploadTimer

func realAfterFunc(d time.Duration, f func()) uploadTimer { return time.AfterFunc(d, f) }

func newUploadBody(r io.Reader, stall time.Duration, abort context.CancelFunc, after afterFunc) *uploadBody {
	b := &uploadBody{r: r, stall: stall}
	b.timer = after(stall, func() {
		b.stalled.Store(true)
		abort()
	})
	return b
}

func (b *uploadBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.released {
		return 0, errUploadBodyReleased
	}
	n, err := b.r.Read(p)
	switch {
	case err == io.EOF:
		b.timer.Stop()
	case err != nil:
		b.errMu.Lock()
		if b.readErr == nil {
			b.readErr = err
		}
		b.errMu.Unlock()
	case n > 0:
		b.timer.Reset(b.stall)
	}
	return n, err
}

// release waits for a read in progress and refuses every read after it.
func (b *uploadBody) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.released = true
	b.timer.Stop()
}

// failure is why the upload's request failed, when it was the file's doing.
//
// A failed read of the file comes first, the watchdog second. When the browser
// stops sending, the watchdog (reset as a read returns) fires a moment before
// the handler's read deadline (armed as the next read starts), and that read
// is still going; release, which runs first, waits for it, so its timeout is
// here by now and the stall is reported as the browser's. Only a stall with no
// failed read is Proxmox's.
func (b *uploadBody) failure() error {
	b.errMu.Lock()
	readErr := b.readErr
	b.errMu.Unlock()
	if readErr != nil {
		return fmt.Errorf("%w: %w", ErrUploadBodyRead, readErr)
	}
	if b.stalled.Load() {
		return fmt.Errorf("%w: no data moved for %s", ErrUploadStalled, b.stall)
	}
	return nil
}

// doMultipart performs an authenticated POST request with multipart form data.
// It streams the file directly to Proxmox without buffering the entire file in
// memory or on disk. Content-Length is pre-computed from the multipart framing
// overhead + the known file size (Proxmox rejects chunked transfer encoding).
//
// Once it returns, nothing reads fileReader again (see uploadBody).
func (a *apiClient) doMultipart(ctx context.Context, path string, fields map[string]string, fileField, fileName string, fileReader io.Reader, fileSize int64, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	// Build the multipart framing (headers/footers) into a buffer,
	// substituting a placeholder for the actual file content.
	// This lets us compute Content-Length without reading the file.
	var prefix bytes.Buffer
	writer := multipart.NewWriter(&prefix)

	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			return fmt.Errorf("write field %s: %w", k, err)
		}
	}
	// CreateFormFile writes the part header; we capture it in prefix.
	if _, err := writer.CreateFormFile(fileField, fileName); err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	// prefix now contains: all field parts + the file part header.
	// Save prefix bytes before closing writer (which adds the closing boundary).
	prefixBytes := make([]byte, prefix.Len())
	copy(prefixBytes, prefix.Bytes())

	// Close writes the closing boundary.
	contentType := writer.FormDataContentType()
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	// suffix = everything after the file part header (closing boundary).
	suffix := prefix.Bytes()[len(prefixBytes):]

	totalSize := int64(len(prefixBytes)) + fileSize + int64(len(suffix))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stall := a.uploadStall
	if stall == 0 {
		stall = UploadStallTimeout
	}
	file := newUploadBody(fileReader, stall, cancel, realAfterFunc)
	defer file.release()
	body := io.MultiReader(
		bytes.NewReader(prefixBytes),
		file,
		bytes.NewReader(suffix),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	a.applyAuth(req)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = totalSize

	// Use a separate client with no timeout and a transport tuned for
	// large uploads (bigger write buffer, longer idle timeout).
	uploadTransport := a.httpClient.Transport.(*http.Transport).Clone()
	uploadTransport.WriteBufferSize = 256 * 1024 // 256KB write buffer (default 4KB)
	uploadTransport.ReadBufferSize = 64 * 1024   // 64KB read buffer for the response
	uploadClient := &http.Client{
		Transport:     uploadTransport,
		Timeout:       0, // no timeout; context controls cancellation
		CheckRedirect: refuseRedirect,
	}

	resp, err := uploadClient.Do(req)
	if err != nil {
		// After the read in progress, so its error is the last one.
		file.release()
		if failure := file.failure(); failure != nil {
			return failure
		}
		return a.connectionFailed(err, nil)
	}
	defer resp.Body.Close()

	respBody, err := a.readBody(resp, nil)
	if err != nil {
		return err
	}

	return unmarshalData(respBody, dst)
}

// What of a Proxmox error answer is read, and what is kept of it.
//
// The answer's text becomes the Go error, and from there an API answer
// (mapProxmoxError), a log line and, through the handlers that store an
// error, rows a Viewer reads. A responder that reflects its request — a
// proxy, a misconfigured endpoint, a debugging echo — would put whatever the
// request carried there, the API token first, so the text is cut, cleaned of
// control characters and scrubbed of the request's secrets (cleanErrorText)
// before anything else sees it.
//
// The read cap is wide enough for the JSON envelope of a die() that quotes a
// page of command output to parse whole; an envelope cut by it anyway still
// gives up its sentence (recoverMessage). What is kept is enough for every
// phrase Nexara matches ("is locked", "already exist", "not running",
// "migrated to rules", "for data too large", the mapMissingObjectError and
// mapProxmoxDieError phrases), all of which sit at the start of PVE's
// sentence.
const (
	maxErrorBodyRead = 64 << 10
	maxErrorTextKept = 2 << 10
)

// readBody reads resp's body, or the error its status maps to. A success is
// read up to maxResponseSize; an error answer only up to maxErrorBodyRead, and
// cleaned of what the request carried: the client's credentials and the
// values of sent's secret-bearing parameters (requestSecrets).
func (a *apiClient) readBody(resp *http.Response, sent url.Values) ([]byte, error) {
	limit := int64(maxResponseSize)
	if resp.StatusCode >= 400 {
		limit = maxErrorBodyRead
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, checkStatus(resp.StatusCode, body, a.requestSecrets(sent))
	}
	return body, nil
}

// connectionFailed is a request that got no answer, as ErrConnectionFailed.
// net/http's text can carry what the peer chose — the Location of a refused
// redirect, a malformed status line — so it is cleaned like an answer's.
func (a *apiClient) connectionFailed(err error, sent url.Values) error {
	if a.onUnreachable != nil && dialFailed(err) {
		a.onUnreachable()
	}
	return fmt.Errorf("%w: %s", ErrConnectionFailed, cleanErrorText(err.Error(), a.requestSecrets(sent)))
}

// dialFailed reports whether a request failed to connect to the endpoint at
// all: refused, no route, a dial that timed out. A certificate that fails the
// pin fails after the dial, in the handshake, and an HTTP answer of any status
// is not an error here, so neither counts; nor does a caller that cancelled.
func dialFailed(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// extraSecretFormKeys are the credential-bearing parameters this package sends
// that pvefield.Secret does not name: one-time codes, a token list, TFA keys.
// A remote migration's target-endpoint carries the target cluster's token too,
// and only its secret half is scrubbed (targetEndpointSecret).
var extraSecretFormKeys = map[string]bool{"otp": true, "tokens": true, "keys": true}

// targetEndpointSecret is the secret half of the API token in a target-endpoint
// value (TargetEndpoint.PropertyString: apitoken=PVEAPIToken=ID=SECRET,host=…),
// or "" when it names none. The token id and host around it stay readable, so a
// refused endpoint is still diagnosable.
func targetEndpointSecret(v string) string {
	for part := range strings.SplitSeq(v, ",") {
		if token, ok := strings.CutPrefix(part, "apitoken="); ok {
			return token[strings.LastIndex(token, "=")+1:]
		}
	}
	return ""
}

// requestSecrets is everything a request of this client can carry that an
// error text must not repeat: the API token's secret, a bootstrap session's
// ticket and CSRF token, the values of sent's secret parameters
// (pvefield.Secret, extraSecretFormKeys) — an ACME plugin's data decoded from
// base64 as well, which is how client_acme.go sends it — and the token secret
// in a target-endpoint.
//
// The secret, not the Authorization header around it: every token header
// starts "PVEAPIToken=", and the token id after it is not secret and is what
// makes a refusal diagnosable.
func (a *apiClient) requestSecrets(sent url.Values) *errorSecrets {
	secrets := []string{a.tokenSecret}
	if auth, ok := a.auth.(ticketAuth); ok {
		secrets = append(secrets, auth.ticket, auth.csrf)
	}
	for key, values := range sent {
		for _, v := range values {
			switch {
			case key == "target-endpoint":
				secrets = append(secrets, targetEndpointSecret(v))
			case pvefield.Secret(key) || extraSecretFormKeys[key]:
				secrets = append(secrets, v)
				switch key {
				case "env":
					secrets = append(secrets, secretEntries(v)...)
				case "data":
					if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
						secrets = append(secrets, string(decoded))
						secrets = append(secrets, secretEntries(string(decoded))...)
					}
				}
			}
		}
	}
	return newErrorSecrets(secrets...)
}

// errorSecrets are the values an error text must not repeat, each in the
// spellings it can take there: raw, query-escaped, and quoted as JSON or Go
// quotes it.
//
// An error text is not a stream, so a secret is matched whole, never by a run
// of its bytes: a run match turns a password "backup" into a scrub of "VM 100
// is locked (backup)", and one containing "password" into a scrub of the word
// in a rejection message. A value shorter than secretWindow is matched only
// where it is set off by punctuation or the text's ends — neither neighbour a
// letter, a digit or a space — as Proxmox quotes a value it repeats ("value
// 'x'", "x=y"), so that a short password that is also a word ("locked") does
// not take a phrase Nexara matches apart; the cost is that such a value
// repeated between spaces is kept.
type errorSecrets struct {
	spellings []string // longest first
}

// secretEntries are the entries of a value that holds several, one to a line
// or NUL-separated — a container's env, an ACME plugin's decoded data — and
// each entry's value after its first "=", so that an echo of one entry alone
// is scrubbed too.
func secretEntries(v string) []string {
	var out []string
	for _, entry := range strings.FieldsFunc(v, func(r rune) bool { return r == 0 || r == '\n' || r == '\r' }) {
		out = append(out, entry)
		if _, value, ok := strings.Cut(entry, "="); ok {
			out = append(out, value)
		}
	}
	return out
}

// controlsToSpaces is the control-character mapping cleanErrorText applies to
// a text, applied to a secret's spellings too, so that a secret holding a
// separator matches its own echo once the echo has been cleaned.
func controlsToSpaces(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) {
			return ' '
		}
		return r
	}, text))
}

func newErrorSecrets(secrets ...string) *errorSecrets {
	s := &errorSecrets{}
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		quoted := strconv.Quote(secret)
		for _, sp := range []string{secret, url.QueryEscape(secret), quoted[1 : len(quoted)-1]} {
			sp = controlsToSpaces(sp)
			if sp != "" && !slices.Contains(s.spellings, sp) {
				s.spellings = append(s.spellings, sp)
			}
		}
	}
	slices.SortStableFunc(s.spellings, func(a, b string) int { return len(b) - len(a) })
	return s
}

// scrub replaces every match of a secret in text with redactedSecret.
func (s *errorSecrets) scrub(text string) string {
	for _, sp := range s.spellings {
		if !strings.Contains(text, sp) {
			continue
		}
		if len(sp) >= secretWindow {
			text = strings.ReplaceAll(text, sp, redactedSecret)
			continue
		}
		var b strings.Builder
		for rest := text; ; {
			i := strings.Index(rest, sp)
			if i < 0 {
				b.WriteString(rest)
				break
			}
			before, _ := utf8.DecodeLastRuneInString(rest[:i])
			after, _ := utf8.DecodeRuneInString(rest[i+len(sp):])
			b.WriteString(rest[:i])
			if (i > 0 && joinsWord(before)) || (i+len(sp) < len(rest) && joinsWord(after)) {
				b.WriteString(sp)
			} else {
				b.WriteString(redactedSecret)
			}
			rest = rest[i+len(sp):]
		}
		text = b.String()
	}
	return text
}

// joinsWord reports whether r, next to a short secret, makes it part of the
// running text rather than a value set off in it.
func joinsWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) }

// minCutSecret is the shortest start of a secret trimCutTail removes from a
// text cut short: fewer bytes than that give nothing away, and matching them
// would take ordinary endings apart.
const minCutSecret = 4

// trimCutTail replaces the longest tail of a text just cut that is the start
// of a secret, at least minCutSecret bytes of it, with redactedSecret: a secret
// the cut went through is no longer whole, so scrub cannot see it.
func (s *errorSecrets) trimCutTail(text string) string {
	longest := 0
	for _, sp := range s.spellings {
		for n := min(len(sp), len(text)); n >= minCutSecret && n > longest; n-- {
			if strings.HasSuffix(text, sp[:n]) {
				longest = n
				break
			}
		}
	}
	if longest == 0 {
		return text
	}
	return text[:len(text)-longest] + redactedSecret
}

// cleanErrorText is text as an error may carry it: control characters (and the
// bidirectional overrides that reorder what a reader sees) turned into
// spaces, the ends trimmed, cut to maxErrorTextKept bytes on a character
// boundary, and every secret in secrets scrubbed out. It cuts before it
// scrubs, so the scrub sees exactly what is kept, and trims the start of a
// secret the cut left at the end.
func cleanErrorText(text string, secrets *errorSecrets) string {
	text = controlsToSpaces(text)
	cut := len(text) > maxErrorTextKept
	if cut {
		n := maxErrorTextKept
		for n > 0 && !utf8.RuneStart(text[n]) {
			n--
		}
		text = secrets.trimCutTail(text[:n])
	}
	text = secrets.scrub(text)
	if cut {
		text += " …"
	}
	return text
}

// checkStatus maps HTTP status codes to typed errors, with the body's text
// cleaned of secrets (see cleanErrorText).
func checkStatus(statusCode int, body []byte, secrets *errorSecrets) error {
	switch {
	case statusCode == http.StatusNotFound:
		return ErrNotFound
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		if msg := cleanErrorText(string(body), secrets); msg != "" {
			return fmt.Errorf("%w: %s", ErrForbidden, msg)
		}
		return ErrForbidden
	case statusCode >= 400:
		msg, fields := parseProxmoxError(body, secrets)
		return &APIError{
			StatusCode: statusCode,
			Message:    msg,
			Fields:     fields,
		}
	}
	return nil
}

// parseProxmoxError extracts a human-readable message from the Proxmox API
// error JSON envelope, plus the per-field rejection map when the response
// carried one, every text in both cleaned (cleanErrorText). Proxmox returns
// {"errors":{"field":"msg",...},"message":"..."} on validation failures.
//
// Without a rejection map the message is the envelope cut down to its
// sentence, {"message":"..."}, which is what mapProxmoxError reads it for: the
// sentence is cleaned as decoded, so an escape such as \u001b cannot bring a
// control character back, and a long one is cut without leaving JSON that no
// longer parses. An envelope the read cap cut gives up as much of its sentence
// as arrived (recoverMessage). A body that is neither is kept as its cleaned
// text.
func parseProxmoxError(body []byte, secrets *errorSecrets) (message string, fields map[string]string) {
	var envelope struct {
		Errors  map[string]string `json:"errors"`
		Message string            `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		sentence, ok := recoverMessage(body)
		if !ok {
			return cleanErrorText(string(body), secrets), nil
		}
		envelope.Message = sentence
	}
	if len(envelope.Errors) == 0 {
		sentence := cleanErrorText(envelope.Message, secrets)
		if sentence == "" {
			return cleanErrorText(string(body), secrets), nil
		}
		var b strings.Builder
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(struct {
			Message string `json:"message"`
		}{sentence}); err != nil {
			return sentence, nil
		}
		return strings.TrimSuffix(b.String(), "\n"), nil
	}
	fields = make(map[string]string, len(envelope.Errors))
	parts := make([]string, 0, len(envelope.Errors))
	for field, msg := range envelope.Errors {
		field, msg = cleanErrorText(field, secrets), cleanErrorText(msg, secrets)
		fields[field] = msg
		parts = append(parts, field+": "+msg)
	}
	// Map iteration order is randomised, so without this the operator is shown
	// the same rejected fields in a different order on every request.
	slices.Sort(parts)
	return cleanErrorText(strings.Join(parts, "; "), secrets), fields
}

// messageStart finds the opening of a JSON envelope's "message" string.
var messageStart = regexp.MustCompile(`(?s)^\s*\{.*?"message"\s*:\s*"`)

// recoverMessage is the decoded start of the "message" string of a JSON
// envelope that was cut short, or false when body is not one. The string is
// taken up to its closing quote or the end of body, and a cut escape at its
// end is dropped.
func recoverMessage(body []byte) (string, bool) {
	loc := messageStart.FindIndex(body)
	if loc == nil {
		return "", false
	}
	rest := body[loc[1]:]
	end := len(rest)
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\\' {
			i++
			continue
		}
		if rest[i] == '"' {
			end = i
			break
		}
	}
	raw := rest[:end]
	// An escape cut by the cap is at most "\uXXX" long: drop up to that many
	// bytes until what is left decodes.
	for drop := 0; drop <= 5 && drop <= len(raw); drop++ {
		var sentence string
		if json.Unmarshal(append(append([]byte{'"'}, raw[:len(raw)-drop]...), '"'), &sentence) == nil {
			return sentence, true
		}
	}
	return "", false
}

// unmarshalData unmarshals the standard Proxmox API response envelope.
func unmarshalData(body []byte, dst interface{}) error {
	var envelope response
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidResponse, err)
	}

	if dst != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidResponse, err)
		}
	}

	return nil
}

// unmarshalDataWithTotal unmarshals both data and total from the Proxmox response envelope.
func unmarshalDataWithTotal(body []byte, dst interface{}) (int, error) {
	var envelope response
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0, fmt.Errorf("%w: %s", ErrInvalidResponse, err)
	}
	if dst != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			return 0, fmt.Errorf("%w: %s", ErrInvalidResponse, err)
		}
	}
	if envelope.Total != nil {
		return *envelope.Total, nil
	}
	return 0, nil
}

// unmarshalTotal extracts only the "total" field from the Proxmox response envelope.
func unmarshalTotal(body []byte) (int, error) {
	var envelope response
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0, fmt.Errorf("%w: %s", ErrInvalidResponse, err)
	}
	if envelope.Total == nil {
		return 0, nil
	}
	return *envelope.Total, nil
}

// doWithTotal performs an authenticated GET and returns both the data and total count.
func (a *apiClient) doWithTotal(ctx context.Context, path string, dst interface{}) (int, error) {
	apiURL := a.baseURL + "/api2/json" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	a.applyAuth(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, a.connectionFailed(err, nil)
	}
	defer resp.Body.Close()

	body, err := a.readBody(resp, nil)
	if err != nil {
		return 0, err
	}

	return unmarshalDataWithTotal(body, dst)
}

// doGetTotal performs an authenticated GET and returns only the total count from the response.
func (a *apiClient) doGetTotal(ctx context.Context, path string) (int, error) {
	apiURL := a.baseURL + "/api2/json" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	a.applyAuth(req)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return 0, a.connectionFailed(err, nil)
	}
	defer resp.Body.Close()

	body, err := a.readBody(resp, nil)
	if err != nil {
		return 0, err
	}

	return unmarshalTotal(body)
}
