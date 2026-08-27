package veeam

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixture reads a captured VBR response from testdata/.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// fakeVBR is a stand-in Veeam server built from the captured fixtures.
//
// It asserts the parts of the contract the client is responsible for holding
// up — x-api-version on every request, a bearer token on every authenticated
// one, form-encoded credentials on the token endpoint — so a regression shows
// up as a test failure here rather than as a 400 from a real server.
type fakeVBR struct {
	t *testing.T

	mu sync.Mutex
	// requests records "METHOD path" for every call received.
	requests []string
	// revisions records the x-api-version header seen on each call.
	revisions []string
	// lastForm is the decoded body of the most recent token request.
	lastForm map[string]string

	tokenGrants atomic.Int32

	// rejectAuth makes the token endpoint answer 401.
	rejectAuth bool
	// swaggerStatus overrides the /swagger/index.js response code.
	swaggerStatus int
	// swaggerBody overrides the /swagger/index.js body.
	swaggerBody []byte
	// unsupportedRevisions answers 400 for these x-api-version values on
	// the token endpoint, emulating a server that predates them.
	unsupportedRevisions map[string]bool
	// serverInfo overrides the /serverInfo body.
	serverInfo []byte
	// licenseBody overrides the /license body.
	licenseBody []byte
	// licenseStatus overrides the /license response code.
	licenseStatus int
	// rejectAllAPI answers 401 to every /api/v1 call while still issuing
	// tokens — the pathological case a naive retry loop turns into an
	// unbounded credential spray.
	rejectAllAPI bool
	// expireAfter, when > 0, makes issued tokens stop being accepted after
	// that many authenticated requests, forcing a 401-refresh-retry.
	expireAfter int32
	authedCalls atomic.Int32

	// lists holds paginated listing rows per path, served with limit/skip
	// honoured so the client's pagination walk is genuinely exercised.
	lists map[string][]json.RawMessage
	// queriesByPath records every query string seen per path, so a test can
	// assert what the client asked for and not merely what it got back.
	queriesByPath map[string][]url.Values
	// repeatFullPages makes every listing answer a full page forever,
	// emulating a server whose pagination never terminates.
	repeatFullPages bool
}

func (f *fakeVBR) pathCalls(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queriesByPath[path])
}

// lastQuery returns the query string of the most recent request to path.
func (f *fakeVBR) lastQuery(path string) url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := f.queriesByPath[path]
	if len(seen) == 0 {
		return url.Values{}
	}
	return seen[len(seen)-1]
}

func newFakeVBR(t *testing.T) (*fakeVBR, *httptest.Server) {
	t.Helper()
	f := &fakeVBR{
		t:             t,
		lists:         map[string][]json.RawMessage{},
		queriesByPath: map[string][]url.Values{},
	}
	srv := httptest.NewUnstartedServer(f)
	// Silence the handshake-failure lines the fingerprint-mismatch test
	// deliberately provokes; they are the expected outcome there and pure
	// noise everywhere else.
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeVBR) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

func (f *fakeVBR) seenRevisions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.revisions...)
}

func (f *fakeVBR) form() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastForm
}

func (f *fakeVBR) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rev := r.Header.Get("x-api-version")

	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	f.revisions = append(f.revisions, rev)
	f.mu.Unlock()

	// Every API request must carry the revision header. The client sends
	// DefaultRevision before negotiation rather than omitting it, because
	// VBR silently falls back to a server-chosen default when it is absent.
	// The Swagger bootstrap is exempt: it is a static asset, served before
	// any revision is known, and is not versioned.
	if rev == "" && strings.HasPrefix(r.URL.Path, "/api/") {
		f.t.Errorf("%s %s: x-api-version header missing", r.Method, r.URL.Path)
	}

	switch r.URL.Path {
	case swaggerIndexPath:
		f.serveSwagger(w)
	case tokenPath:
		f.serveToken(w, r)
	case logoutPath:
		w.WriteHeader(http.StatusOK)
	default:
		f.serveAPI(w, r)
	}
}

func (f *fakeVBR) serveSwagger(w http.ResponseWriter) {
	if f.swaggerStatus != 0 && f.swaggerStatus != http.StatusOK {
		w.WriteHeader(f.swaggerStatus)
		return
	}
	body := f.swaggerBody
	if body == nil {
		body = fixture(f.t, "swagger_index.js")
	}
	w.Header().Set("Content-Type", "application/javascript")
	_, _ = w.Write(body)
}

func (f *fakeVBR) serveToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("token request body did not parse as a form: %v", err)
	}
	values := make(map[string]string, len(r.PostForm))
	for k := range r.PostForm {
		values[k] = r.PostForm.Get(k)
	}
	f.mu.Lock()
	f.lastForm = values
	f.mu.Unlock()

	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		f.t.Errorf("token request Content-Type = %q, want form encoding", ct)
	}

	if f.unsupportedRevisions[r.Header.Get("x-api-version")] {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(fixture(f.t, "error_bad_api_version.json"))
		return
	}
	if f.rejectAuth {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(fixture(f.t, "error_bad_credentials.json"))
		return
	}

	f.tokenGrants.Add(1)
	f.authedCalls.Store(0)
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(fixture(f.t, "token_password_grant.json"))
}

func (f *fakeVBR) serveAPI(w http.ResponseWriter, r *http.Request) {
	if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "Bearer ") {
		f.t.Errorf("%s: Authorization = %q, want a bearer token", r.URL.Path, auth)
	}

	if f.rejectAllAPI || (f.expireAfter > 0 && f.authedCalls.Add(1) > f.expireAfter) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(fixture(f.t, "error_bad_credentials.json"))
		return
	}

	f.mu.Lock()
	f.queriesByPath[r.URL.Path] = append(f.queriesByPath[r.URL.Path], r.URL.Query())
	rows, isList := f.lists[r.URL.Path]
	f.mu.Unlock()

	if isList || f.repeatFullPages {
		q := r.URL.Query()
		if f.repeatFullPages {
			full := make([]json.RawMessage, pageSize)
			for i := range full {
				full[i] = json.RawMessage(`{}`)
			}
			writeListPage(w, full, pageSize*maxPages*2, 0, pageSize)
			return
		}
		page, total := paginate(rows, q)
		skip, _ := strconv.Atoi(q.Get("skip"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		writeListPage(w, page, total, skip, limit)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/serverInfo":
		body := f.serverInfo
		if body == nil {
			body = fixture(f.t, "serverinfo.json")
		}
		_, _ = w.Write(body)
	case "/api/v1/license":
		if f.licenseStatus != 0 && f.licenseStatus != http.StatusOK {
			w.WriteHeader(f.licenseStatus)
			_, _ = w.Write([]byte(`{"errorCode":"UnknownError","message":"licence unavailable","status":500}`))
			return
		}
		body := f.licenseBody
		if body == nil {
			body = fixture(f.t, "license.json")
		}
		_, _ = w.Write(body)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errorCode":"ResourceNotFound","message":"not found","status":404}`))
	}
}

// newTestClient builds a client wired to srv, trusting its ephemeral
// certificate by pinning the fingerprint httptest generated.
func newTestClient(t *testing.T, srv *httptest.Server, username string) *Client {
	t.Helper()
	c, err := New(Config{
		BaseURL:        srv.URL,
		Username:       username,
		Password:       "correct-horse",
		TLSFingerprint: formatFingerprint(srv.Certificate().Raw),
		Timeout:        10 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestProbe_HappyPath(t *testing.T) {
	f, srv := newFakeVBR(t)
	c := newTestClient(t, srv, `ad\jdoe`)

	res, err := c.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}

	if res.APIRevision != DefaultRevision {
		t.Errorf("APIRevision = %q, want %q", res.APIRevision, DefaultRevision)
	}
	if res.BuildVersion != "13.1.0.411" {
		t.Errorf("BuildVersion = %q, want 13.1.0.411", res.BuildVersion)
	}
	if res.ServerName != "vbr01" {
		t.Errorf("ServerName = %q, want vbr01", res.ServerName)
	}
	if res.LicenseEdition != RequiredEdition {
		t.Errorf("LicenseEdition = %q, want %q", res.LicenseEdition, RequiredEdition)
	}
	if res.LicenseType != "NFR" {
		t.Errorf("LicenseType = %q, want NFR", res.LicenseType)
	}
	if res.LicenseExpiration != "2027-04-27T00:00:00Z" {
		t.Errorf("LicenseExpiration = %q", res.LicenseExpiration)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", res.Warnings)
	}

	// The licence lists 14 Proxmox VMs, all reporting the same hostName.
	if len(res.ProxmoxClusters) != 1 {
		t.Fatalf("ProxmoxClusters = %+v, want exactly one", res.ProxmoxClusters)
	}
	if got := res.ProxmoxClusters[0]; got.Name != "EXAMPLE" || got.VMCount != 14 {
		t.Errorf("ProxmoxClusters[0] = %+v, want {EXAMPLE 14}", got)
	}

	// Discovery must be the unauthenticated bootstrap, not a probe ladder:
	// exactly one token is minted for the whole sequence.
	want := []string{
		"GET " + swaggerIndexPath,
		"POST " + tokenPath,
		"GET /api/v1/serverInfo",
		"GET /api/v1/license",
	}
	if got := f.seen(); !equalStrings(got, want) {
		t.Errorf("request sequence = %v, want %v", got, want)
	}
	if n := f.tokenGrants.Load(); n != 1 {
		t.Errorf("token grants = %d, want 1", n)
	}
	// The bootstrap is unversioned; every /api/ call is pinned.
	for i, req := range f.seen() {
		rev := f.seenRevisions()[i]
		if strings.HasPrefix(strings.TrimPrefix(req, "GET "), "/api/") && rev != DefaultRevision {
			t.Errorf("%s carried x-api-version %q, want %q", req, rev, DefaultRevision)
		}
	}
}

// A domain-qualified username has to reach the form body with its backslash
// intact. Losing it produces a 401 identical to a wrong password, which is
// exactly the failure the spike spent half an hour on.
func TestProbe_DomainUsernameSurvivesFormEncoding(t *testing.T) {
	f, srv := newFakeVBR(t)
	c := newTestClient(t, srv, `ad\jdoe`)

	if _, err := c.Probe(context.Background()); err != nil {
		t.Fatalf("Probe: %v", err)
	}

	form := f.form()
	if got := form["username"]; got != `ad\jdoe` {
		t.Errorf("username reached the server as %q, want %q", got, `ad\jdoe`)
	}
	if got := form["grant_type"]; got != "password" {
		t.Errorf("grant_type = %q, want password", got)
	}
}

func TestProbe_AuthFailure(t *testing.T) {
	tests := []struct {
		name         string
		username     string
		wantHintWord string
	}{
		{name: "local account", username: "administrator"},
		{name: "domain account", username: `ad\jdoe`, wantHintWord: "domain-qualified"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeVBR(t)
			f.rejectAuth = true
			c := newTestClient(t, srv, tc.username)

			_, err := c.Probe(context.Background())
			if !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("Probe error = %v, want ErrAuthFailed", err)
			}
			if !strings.Contains(err.Error(), "credentials are invalid") {
				t.Errorf("error %q does not carry Veeam's own message", err)
			}
			hinted := strings.Contains(err.Error(), tc.wantHintWord)
			if tc.wantHintWord != "" && !hinted {
				t.Errorf("error %q should hint at the domain-qualifier format", err)
			}
			if tc.wantHintWord == "" && strings.Contains(err.Error(), "domain-qualified") {
				t.Errorf("error %q should not hint at domains for a local account", err)
			}
		})
	}
}

// A bad credential must not be retried across the probe ladder — each rung
// spends a domain lockout budget for no chance of success.
func TestNegotiateRevision_AuthFailureStopsProbeLadder(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.swaggerStatus = http.StatusNotFound
	f.rejectAuth = true
	c := newTestClient(t, srv, `ad\jdoe`)

	_, err := c.NegotiateRevision(context.Background())
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("NegotiateRevision error = %v, want ErrAuthFailed", err)
	}

	var tokenCalls int
	for _, req := range f.seen() {
		if req == "POST "+tokenPath {
			tokenCalls++
		}
	}
	if tokenCalls != 1 {
		t.Errorf("token endpoint called %d times, want 1 — the ladder kept walking after a 401", tokenCalls)
	}
}

// With no Swagger bootstrap, the client walks the revision ladder downward and
// settles on the newest revision the server accepts.
func TestNegotiateRevision_FallsBackToProbeLadder(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.swaggerStatus = http.StatusNotFound
	f.unsupportedRevisions = map[string]bool{"1.3-rev2": true, "1.3-rev1": true}
	c := newTestClient(t, srv, "administrator")

	rev, err := c.NegotiateRevision(context.Background())
	if err != nil {
		t.Fatalf("NegotiateRevision: %v", err)
	}
	if rev != "1.3-rev0" {
		t.Errorf("revision = %q, want 1.3-rev0", rev)
	}
	if c.Revision() != "1.3-rev0" {
		t.Errorf("client revision = %q, want 1.3-rev0", c.Revision())
	}
}

// A server offering only revisions newer than this client understands is not
// usable: taking one would opt into schema changes nothing has been tested
// against.
func TestNegotiateRevision_RejectsOnlyNewerRevisions(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.swaggerBody = []byte(`{"urls":[{"url":"/swagger/v2.0-rev0/swagger.json","name":"V2.0-REV0"}]}`)
	f.unsupportedRevisions = map[string]bool{"1.3-rev2": true, "1.3-rev1": true, "1.3-rev0": true}
	c := newTestClient(t, srv, "administrator")

	_, err := c.NegotiateRevision(context.Background())
	if !errors.Is(err, ErrRevisionUnknown) {
		t.Fatalf("NegotiateRevision error = %v, want ErrRevisionUnknown", err)
	}
}

func TestProbe_RefusesOldVersion(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.serverInfo = []byte(`{"platform":"Windows","name":"vbr01","buildVersion":"13.0.1.204"}`)
	c := newTestClient(t, srv, "administrator")

	_, err := c.Probe(context.Background())
	if !errors.Is(err, ErrVersionUnsupported) {
		t.Fatalf("Probe error = %v, want ErrVersionUnsupported", err)
	}

	var verr *VersionError
	if !errors.As(err, &verr) {
		t.Fatalf("error %v is not a *VersionError", err)
	}
	if verr.Got != "13.0.1.204" || verr.Minimum != MinBuildVersion {
		t.Errorf("VersionError = %+v, want {13.0.1.204 %s}", verr, MinBuildVersion)
	}

	// The licence must not be read once the version gate fails — there is
	// nothing to do with the answer.
	for _, req := range f.seen() {
		if req == "GET /api/v1/license" {
			t.Error("licence was fetched after the version gate rejected the server")
		}
	}
}

func TestProbe_WarnsOnWrongEditionAndMissingLicence(t *testing.T) {
	t.Run("licence unreadable", func(t *testing.T) {
		f, srv := newFakeVBR(t)
		f.licenseStatus = http.StatusInternalServerError
		c := newTestClient(t, srv, "administrator")

		res, err := c.Probe(context.Background())
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Could not read the licence") {
			t.Errorf("Warnings = %v, want one licence-unreadable warning", res.Warnings)
		}
		if res.BuildVersion != "13.1.0.411" {
			t.Errorf("BuildVersion = %q — a missing licence must not lose the version", res.BuildVersion)
		}
	})

	t.Run("community edition warns but does not fail", func(t *testing.T) {
		lic := strings.Replace(string(fixture(t, "license.json")),
			`"edition": "EnterprisePlus"`, `"edition": "Community"`, 1)
		if !strings.Contains(lic, `"Community"`) {
			t.Fatal("fixture rewrite did not take — the edition field shape changed")
		}

		f, srv := newFakeVBR(t)
		f.licenseBody = []byte(lic)
		c := newTestClient(t, srv, "administrator")

		res, err := c.Probe(context.Background())
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if res.LicenseEdition != "Community" {
			t.Errorf("LicenseEdition = %q, want Community", res.LicenseEdition)
		}
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], RequiredEdition) {
			t.Errorf("Warnings = %v, want one edition warning naming %s", res.Warnings, RequiredEdition)
		}
	})
}

// A 401 mid-request refreshes the token and retries exactly once. Not zero
// (a 15-minute token expiring mid-sync is routine), and not a loop (that turns
// a wrong password into an account lockout).
func TestGet_RefreshesOnceOn401(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.expireAfter = 1
	c := newTestClient(t, srv, "administrator")

	if _, err := c.ServerInfo(context.Background()); err != nil {
		t.Fatalf("first ServerInfo: %v", err)
	}
	if n := f.tokenGrants.Load(); n != 1 {
		t.Fatalf("token grants after first call = %d, want 1", n)
	}

	// The second call's token is stale; the client must mint a new one and
	// retry rather than surfacing the 401.
	if _, err := c.ServerInfo(context.Background()); err != nil {
		t.Fatalf("second ServerInfo: %v", err)
	}
	if n := f.tokenGrants.Load(); n != 2 {
		t.Errorf("token grants after retry = %d, want 2", n)
	}
}

func TestGet_DoesNotLoopOnPersistent401(t *testing.T) {
	f, srv := newFakeVBR(t)
	f.rejectAllAPI = true
	c := newTestClient(t, srv, "administrator")

	_, err := c.ServerInfo(context.Background())
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("ServerInfo error = %v, want ErrAuthFailed", err)
	}

	var apiCalls int
	for _, req := range f.seen() {
		if req == "GET /api/v1/serverInfo" {
			apiCalls++
		}
	}
	if apiCalls != 2 {
		t.Errorf("serverInfo attempted %d times, want exactly 2 (original + one retry)", apiCalls)
	}
}

// Concurrent callers must collapse onto a single grant. Without singleflight a
// sync fan-out mints one token per endpoint on the first tick.
func TestAccessToken_ConcurrentCallersShareOneGrant(t *testing.T) {
	f, srv := newFakeVBR(t)
	c := newTestClient(t, srv, "administrator")

	const callers = 16
	var wg sync.WaitGroup
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = c.ServerInfo(context.Background())
		}()
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if n := f.tokenGrants.Load(); n != 1 {
		t.Errorf("token grants = %d across %d concurrent callers, want 1", n, callers)
	}
}

// The token a grant mints is shared state, so the grant must not inherit any
// one caller's cancellation.
//
// singleflight hands a single flight's error to every goroutine that joined
// it. If the flight ran on the first caller's context, one caller timing out
// would fail every other caller that had queued behind it — including callers
// with minutes of budget left. An already-cancelled caller is the
// deterministic stand-in for that race: if the grant were bound to it, the
// request would abort before it left the process.
func TestAccessToken_GrantIsDetachedFromCallerCancellation(t *testing.T) {
	f, srv := newFakeVBR(t)
	c := newTestClient(t, srv, "administrator")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	access, err := c.accessToken(ctx)
	if err != nil {
		t.Fatalf("accessToken with a cancelled caller: %v", err)
	}
	if access == "" {
		t.Error("accessToken returned an empty token")
	}
	if n := f.tokenGrants.Load(); n != 1 {
		t.Errorf("token grants = %d, want 1", n)
	}
}

func TestLogout_ClearsCachedToken(t *testing.T) {
	f, srv := newFakeVBR(t)
	c := newTestClient(t, srv, "administrator")

	if _, err := c.ServerInfo(context.Background()); err != nil {
		t.Fatalf("ServerInfo: %v", err)
	}
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("Logout: %v", err)
	}

	// A second logout is a no-op rather than a second call: there is no
	// token left to invalidate.
	if err := c.Logout(context.Background()); err != nil {
		t.Fatalf("second Logout: %v", err)
	}
	var logouts int
	for _, req := range f.seen() {
		if req == "POST "+logoutPath {
			logouts++
		}
	}
	if logouts != 1 {
		t.Errorf("logout called %d times, want 1", logouts)
	}

	// The next request has to mint a fresh token.
	if _, err := c.ServerInfo(context.Background()); err != nil {
		t.Fatalf("ServerInfo after logout: %v", err)
	}
	if n := f.tokenGrants.Load(); n != 2 {
		t.Errorf("token grants = %d, want 2 — logout did not clear the cache", n)
	}
}

func TestNew_Validation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"no base URL", Config{Username: "u", Password: "p"}},
		{"no username", Config{BaseURL: "https://vbr:9419", Password: "p"}},
		{"no password", Config{BaseURL: "https://vbr:9419", Username: "u"}},
		{"no host", Config{BaseURL: "https:///path", Username: "u", Password: "p"}},
		{"credentials in URL", Config{BaseURL: "https://u:p@vbr:9419", Username: "u", Password: "p"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("New(%+v) error = %v, want ErrInvalidInput", tc.cfg, err)
			}
		})
	}
}

// A pinned fingerprint that does not match must fail as unreachable rather
// than falling through to an unverified connection.
func TestTransport_FingerprintMismatchIsFatal(t *testing.T) {
	_, srv := newFakeVBR(t)
	c, err := New(Config{
		BaseURL:        srv.URL,
		Username:       "administrator",
		Password:       "correct-horse",
		TLSFingerprint: strings.Repeat("ab", 32),
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.ServerInfo(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("ServerInfo error = %v, want ErrUnreachable", err)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
