// Package veeam is a client for the Veeam Backup & Replication REST API
// (VBR 13.1+), used to monitor and control Veeam's Proxmox backup jobs from
// Nexara.
//
// It is deliberately separate from internal/proxmox rather than a sibling of
// PBSClient: VBR authenticates with a short-lived OAuth2 bearer token that has
// to be refreshed, pins its schema behind an x-api-version header that must be
// negotiated per server, and identifies work with plain UUIDs instead of
// UPIDs. None of that shares anything with apiClient beyond "it speaks HTTPS".
//
// Every path and payload shape here was verified against a live VBR 13.1.0.411
// server; the captured responses are in testdata/. Veeam's published HTML
// reference disagrees with the product in several load-bearing places (it
// strips group prefixes, so the real repository path is
// /api/v1/backupInfrastructure/repositories, not /api/v1/repositories) — check
// the server's own swagger.json before adding a path, not the docs.
package veeam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// maxResponseSize caps how much of a response body we read (50 MB). A full
// restore-point page runs to a few hundred KB; the cap exists so a hostile or
// broken upstream cannot exhaust memory.
const maxResponseSize = 50 * 1024 * 1024

// maxSwaggerIndexSize caps the unauthenticated Swagger bootstrap read. The real
// file is ~4 KB; 1 MB is generous and still bounded.
const maxSwaggerIndexSize = 1 * 1024 * 1024

const swaggerIndexPath = "/swagger/index.js"

// defaultTimeout bounds one request when Config.Timeout is unset.
const defaultTimeout = 30 * time.Second

// Config describes one Veeam server connection.
type Config struct {
	// BaseURL is the REST API root, e.g. https://vbr.example.com:9419.
	// Must be https and must not embed credentials.
	BaseURL string
	// Username may be domain-qualified (DOMAIN\user).
	Username string
	Password string
	// APIRevision pins x-api-version. Empty means negotiate on first use.
	APIRevision string
	// TLSFingerprint pins the leaf certificate's SHA-256, in either bare-hex
	// or colon-separated form. Empty means verify against the system CA pool.
	TLSFingerprint string
	// VerifyTLS=false disables certificate verification entirely. Ignored
	// when TLSFingerprint is set.
	VerifyTLS bool
	Timeout   time.Duration
}

// Client talks to one Veeam Backup & Replication server. Safe for concurrent
// use.
type Client struct {
	httpClient *http.Client
	baseURL    string
	username   string
	password   string
	// timeout bounds one detached token grant. Kept alongside the
	// http.Client's own timeout because the singleflight grant runs on a
	// context detached from any caller's deadline and needs its own.
	timeout time.Duration

	revMu    sync.RWMutex
	revision string

	tokMu sync.Mutex
	tok   cachedToken
	group singleflight.Group
}

// New builds a client. It performs no network I/O.
func New(cfg Config) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("%w: BaseURL is required", ErrInvalidInput)
	}
	if cfg.Username == "" {
		return nil, fmt.Errorf("%w: Username is required", ErrInvalidInput)
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("%w: Password is required", ErrInvalidInput)
	}

	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: BaseURL is not a valid URL", ErrInvalidInput)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%w: BaseURL must include a host", ErrInvalidInput)
	}
	// Refusing userinfo here is what lets every error in this package embed
	// the request URL verbatim without a redaction pass: the URL provably
	// cannot carry a credential, because the only other place one appears is
	// the token form body, which is never formatted into an error.
	if parsed.User != nil {
		return nil, fmt.Errorf("%w: BaseURL must not embed credentials", ErrInvalidInput)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	return &Client{
		httpClient: buildHTTPClient(cfg.TLSFingerprint, cfg.VerifyTLS, timeout),
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		username:   cfg.Username,
		password:   cfg.Password,
		timeout:    timeout,
		revision:   strings.ToLower(strings.TrimSpace(cfg.APIRevision)),
	}, nil
}

// Revision returns the x-api-version currently in use, or "" if none has been
// negotiated yet.
func (c *Client) Revision() string {
	c.revMu.RLock()
	defer c.revMu.RUnlock()
	return c.revision
}

func (c *Client) setRevision(rev string) {
	c.revMu.Lock()
	c.revision = rev
	c.revMu.Unlock()
}

// setRevisionHeader stamps x-api-version on a request.
//
// Always sent, even though VBR 13.1 answers 200 to a request that omits it —
// contrary to its own documentation, which says 400. Relying on that fallback
// would mean a VBR upgrade silently changes our response schema, which is
// exactly the failure this header exists to prevent. When no revision has been
// negotiated yet, the newest one this client was written against is sent, so
// the header is never absent.
func (c *Client) setRevisionHeader(req *http.Request) {
	rev := c.Revision()
	if rev == "" {
		rev = DefaultRevision
	}
	req.Header.Set("x-api-version", rev)
}

// NegotiateRevision determines which x-api-version to speak and records it on
// the client, returning the chosen value.
//
// Primary path is GET /swagger/index.js: unauthenticated, cheap, and it
// enumerates every revision the server supports rather than the first one that
// happens to work. Falls back to a descending probe of the token endpoint only
// when the bootstrap is missing or unparseable.
func (c *Client) NegotiateRevision(ctx context.Context) (string, error) {
	if supported, err := c.supportedRevisions(ctx); err == nil {
		if rev := pickRevision(supported); rev != "" {
			c.setRevision(rev)
			return rev, nil
		}
	}
	return c.probeRevision(ctx)
}

// supportedRevisions fetches and parses the Swagger UI bootstrap.
func (c *Client) supportedRevisions(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+swaggerIndexPath, nil)
	if err != nil {
		return nil, fmt.Errorf("veeam: build swagger index request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSwaggerIndexSize))
	if err != nil {
		return nil, fmt.Errorf("veeam: read swagger index: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{StatusCode: resp.StatusCode, Message: "swagger index unavailable"}
	}

	revisions := parseRevisions(string(body))
	if len(revisions) == 0 {
		return nil, ErrRevisionUnknown
	}
	return revisions, nil
}

// probeRevision walks probeRevisions newest-first, minting a token at each
// rung until one is accepted.
//
// A 401 aborts the walk immediately rather than trying the next revision:
// the credentials are wrong, every remaining rung would fail the same way, and
// against a domain account each extra attempt spends a lockout budget the
// operator cares about.
func (c *Client) probeRevision(ctx context.Context) (string, error) {
	var lastErr error
	for _, rev := range probeRevisions {
		c.setRevision(rev)
		tok, err := c.requestToken(ctx, url.Values{
			"grant_type": {"password"},
			"username":   {c.username},
			"password":   {c.password},
		})
		if err == nil {
			c.tokMu.Lock()
			c.tok = tok
			c.tokMu.Unlock()
			return rev, nil
		}
		if isTerminalProbeError(err) {
			c.setRevision("")
			return "", err
		}
		lastErr = err
	}

	c.setRevision("")
	if lastErr != nil {
		return "", fmt.Errorf("%w: %s", ErrRevisionUnknown, lastErr)
	}
	return "", ErrRevisionUnknown
}

// ServerInfo returns GET /api/v1/serverInfo.
func (c *Client) ServerInfo(ctx context.Context) (*ServerInfo, error) {
	var info ServerInfo
	if err := c.get(ctx, "/api/v1/serverInfo", &info); err != nil {
		return nil, fmt.Errorf("veeam: get server info: %w", err)
	}
	return &info, nil
}

// License returns GET /api/v1/license.
func (c *Client) License(ctx context.Context) (*License, error) {
	var lic License
	if err := c.get(ctx, "/api/v1/license", &lic); err != nil {
		return nil, fmt.Errorf("veeam: get license: %w", err)
	}
	return &lic, nil
}

// get performs an authenticated GET and decodes the JSON body into dst.
//
// A 401 mid-request triggers exactly one token refresh and one retry. Never a
// loop: if the second attempt is also rejected the credential is genuinely
// bad, and retrying past that turns a wrong password into an account lockout.
func (c *Client) get(ctx context.Context, path string, dst any) error {
	body, _, err := c.do(ctx, http.MethodGet, path)
	if err != nil {
		return err
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("veeam: decode %s response: %w", path, err)
	}
	return nil
}

// do performs one authenticated request, returning the body and the HTTP
// status. The status is handed back rather than swallowed because Veeam uses
// it semantically: POST /jobs/{id}/start answers 201 with a session inline, or
// 204 with no body at all when the job has nothing to process, and the two
// mean different things to an operator.
func (c *Client) do(ctx context.Context, method, path string) (body []byte, status int, err error) {
	body, status, access, err := c.attempt(ctx, method, path)
	if err != nil {
		return nil, status, err
	}
	if status != http.StatusUnauthorized {
		return body, status, checkStatus(status, body)
	}

	c.invalidate(access)
	body, status, _, err = c.attempt(ctx, method, path)
	if err != nil {
		return nil, status, err
	}
	return body, status, checkStatus(status, body)
}

// attempt performs one authenticated request, returning the body, status and
// the access token it used (so the caller can invalidate precisely that one).
func (c *Client) attempt(ctx context.Context, method, path string) (body []byte, status int, access string, err error) {
	access, err = c.accessToken(ctx)
	if err != nil {
		return nil, 0, "", err
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, 0, access, fmt.Errorf("veeam: build request for %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Accept", "application/json")
	// The control endpoints need `Content-Length: 0` — VBR rejects a bodiless
	// POST without it. net/http emits it for us: a nil Body gives
	// outgoingLength() == 0, and shouldSendContentLength() sends a zero length
	// for POST/PUT/PATCH specifically because so many servers require it. A
	// manually-set Content-Length header would be ignored (the transport reads
	// req.ContentLength, not the header map), so there is deliberately nothing
	// to set here — only something to not break by attaching an empty body.
	c.setRevisionHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, access, fmt.Errorf("%w: %s", ErrUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return nil, 0, access, fmt.Errorf("veeam: read %s response: %w", path, err)
	}
	return body, resp.StatusCode, access, nil
}

// checkStatus turns a non-2xx into the right typed error.
func checkStatus(status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	apiErr := decodeAPIError(status, body)
	switch {
	case status == http.StatusUnauthorized:
		return fmt.Errorf("veeam: %s: %w", apiErr.Message, ErrAuthFailed)
	case IsPlatformUnsupported(apiErr):
		return fmt.Errorf("veeam: %s: %w", apiErr.Message, ErrPlatformUnsupported)
	default:
		return apiErr
	}
}

// decodeAPIError parses VBR's error envelope, falling back to the raw body
// when the response isn't the shape we expect (a reverse proxy's HTML error
// page, say).
func decodeAPIError(status int, body []byte) *APIError {
	var env apiErrorBody
	if err := json.Unmarshal(body, &env); err == nil && (env.Message != "" || env.ErrorCode != "") {
		msg := env.Message
		if msg == "" {
			msg = env.Title
		}
		// Bounded even though the envelope shape suggests a real Veeam server:
		// plenty of other API servers answer in this shape, and this string
		// reaches an operator-facing error and an audit row.
		return &APIError{
			StatusCode: status,
			ErrorCode:  truncateUpstream(env.ErrorCode, maxUpstreamErrorCode),
			Message:    truncateUpstream(msg, maxUpstreamMessage),
		}
	}

	// The body is NOT echoed. A response that isn't Veeam's envelope came
	// from something that isn't Veeam, and this error reaches an API caller
	// who chose the URL — inlining the bytes would turn a wrong base_url into
	// a read primitive against whatever else is listening on the network.
	// The status class is enough for the operator to act on.
	msg := http.StatusText(status)
	if msg == "" {
		msg = "unexpected response"
	}
	return &APIError{
		StatusCode: status,
		Message:    msg + " (the server did not return a Veeam error response — check that base_url points at the VBR REST API on port 9419)",
	}
}

// Caps on the upstream-controlled text an APIError carries. Nothing on the far
// end bounds either field, and both surface in operator-facing errors.
const (
	maxUpstreamMessage   = 512
	maxUpstreamErrorCode = 64
)

// truncateUpstream bounds a string on a rune boundary so the result stays
// valid UTF-8.
func truncateUpstream(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

// isTerminalProbeError reports whether an error from the revision probe means
// "stop", as opposed to "this revision isn't supported, try the next".
func isTerminalProbeError(err error) bool {
	return errors.Is(err, ErrAuthFailed) || errors.Is(err, ErrUnreachable)
}
