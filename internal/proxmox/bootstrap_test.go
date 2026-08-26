package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakePVE is a minimal stand-in for the /access/* surface onboarding drives,
// plus the /cluster/status call used to verify the minted token.
//
// It records what was asked of it so a test can assert on the sequence — the
// forward-idempotent design means "which calls did NOT happen" is as much the
// contract as the returned values.
type fakePVE struct {
	t *testing.T

	// Behaviour switches.
	needTFA        json.RawMessage // non-nil → answer the ticket with a challenge
	badPassword    bool
	userExists     bool
	tokenExists    bool
	aclAlreadyHeld bool
	refuseCleanup  bool // fail every DELETE, stranding what the mint created
	// malformedTokenEcho makes the mint response echo a full-tokenid that
	// cannot be trusted.
	malformedTokenEcho bool
	verifyForbids      int // fail this many /cluster/status calls with 403 first

	// Recorded traffic.
	calls        []string
	sawCSRF      map[string]string
	verifyAuth   string
	deletedToken bool
	deletedUser  bool
	revokedACL   bool
	grantedACL   bool
	createdUser  bool

	// extraTokens are returned by the token index in addition to "nexara",
	// standing in for credentials Nexara did not mint.
	extraTokens []string
}

func (f *fakePVE) record(r *http.Request) {
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if f.sawCSRF == nil {
		f.sawCSRF = map[string]string{}
	}
	f.sawCSRF[r.Method+" "+r.URL.Path] = r.Header.Get("CSRFPreventionToken")
}

func (f *fakePVE) fail(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"data":null,"errors":{"detail":` + strconvQuote(msg) + `}}`))
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (f *fakePVE) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	path := strings.TrimPrefix(r.URL.Path, "/api2/json")

	switch {
	case r.Method == http.MethodPost && path == "/access/ticket":
		if f.badPassword {
			f.fail(w, http.StatusUnauthorized, "authentication failure")
			return
		}
		body := map[string]any{
			"ticket":              "PVE:root@pam:TICKETVALUE",
			"CSRFPreventionToken": "CSRF123",
			"username":            r.FormValue("username"),
		}
		if f.needTFA != nil {
			body["NeedTFA"] = f.needTFA
			body["ticket"] = "PVE:!tfa!challenge"
		}
		jsonResponse(w, body)

	case r.Method == http.MethodPost && path == "/access/users":
		if f.userExists {
			f.fail(w, http.StatusInternalServerError, "user '"+r.FormValue("userid")+"' already exists")
			return
		}
		f.createdUser = true
		jsonResponse(w, nil)

	case r.Method == http.MethodGet && path == "/access/acl":
		if f.aclAlreadyHeld {
			jsonResponse(w, []AccessACLEntry{
				{Path: "/", Type: "user", UGID: "nexara@pve", RoleID: RoleAdministrator},
			})
			return
		}
		jsonResponse(w, []AccessACLEntry{})

	case r.Method == http.MethodPut && path == "/access/acl":
		if r.FormValue("delete") == "1" {
			f.revokedACL = true
		} else {
			f.grantedACL = true
		}
		jsonResponse(w, nil)

	case r.Method == http.MethodPost && strings.Contains(path, "/token/"):
		if f.tokenExists {
			f.fail(w, http.StatusInternalServerError, "token with same name already exists")
			return
		}
		full := "nexara@pve!nexara"
		if f.malformedTokenEcho {
			full = "../../etc/passwd"
		}
		jsonResponse(w, AccessTokenCreated{FullTokenID: full, Value: "s3cr3t-value"})

	case r.Method == http.MethodGet && strings.HasSuffix(path, "/token"):
		tokens := []AccessToken{{TokenID: "nexara"}}
		for _, extra := range f.extraTokens {
			tokens = append(tokens, AccessToken{TokenID: extra})
		}
		jsonResponse(w, tokens)

	case r.Method == http.MethodDelete && strings.Contains(path, "/token/"):
		if f.refuseCleanup {
			f.fail(w, http.StatusForbidden, "permission check failed")
			return
		}
		f.deletedToken = true
		jsonResponse(w, nil)

	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/access/users/"):
		if f.refuseCleanup {
			f.fail(w, http.StatusForbidden, "permission check failed")
			return
		}
		f.deletedUser = true
		jsonResponse(w, nil)

	case r.Method == http.MethodGet && path == "/cluster/status":
		f.verifyAuth = r.Header.Get("Authorization")
		if f.verifyForbids > 0 {
			f.verifyForbids--
			f.fail(w, http.StatusForbidden, "permission check failed")
			return
		}
		jsonResponse(w, []ClusterStatusEntry{{Name: "pve1", Type: "node"}})

	default:
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		f.fail(w, http.StatusNotFound, "no such endpoint")
	}
}

func newFakePVE(t *testing.T, f *fakePVE) *BootstrapClient {
	t.Helper()
	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	// Short timeout so a hung stub fails fast rather than stalling the suite.
	c, err := NewBootstrapClient(BootstrapConfig{BaseURL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewBootstrapClient: %v", err)
	}
	return c
}

func (f *fakePVE) called(method, path string) bool {
	for _, c := range f.calls {
		if c == method+" /api2/json"+path {
			return true
		}
	}
	return false
}

func TestBootstrapLoginSwapsToTicketAuth(t *testing.T) {
	fake := &fakePVE{}
	c := newFakePVE(t, fake)

	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	auth, ok := c.auth.(ticketAuth)
	if !ok {
		t.Fatalf("auth = %T, want ticketAuth after a successful login", c.auth)
	}
	if auth.ticket != "PVE:root@pam:TICKETVALUE" || auth.csrf != "CSRF123" {
		t.Errorf("ticketAuth = %+v, want the ticket and CSRF token from the response", auth)
	}
	// The login itself must carry no credentials of its own: /access/ticket
	// sets allowtoken => 0, so anything sent there is a credential on a
	// request that cannot use it.
	if got := fake.sawCSRF["POST /api2/json/access/ticket"]; got != "" {
		t.Errorf("login request carried CSRFPreventionToken %q, want none", got)
	}
}

func TestBootstrapLoginRejectsBadPassword(t *testing.T) {
	c := newFakePVE(t, &fakePVE{badPassword: true})

	err := c.Login(context.Background(), "root@pam", "wrong", "")
	if !errors.Is(err, ErrBootstrapAuthFailed) {
		t.Fatalf("Login error = %v, want ErrBootstrapAuthFailed", err)
	}
	// A rejected login must leave the client unauthenticated rather than
	// half-swapped onto an empty ticket.
	if _, ok := c.auth.(noAuth); !ok {
		t.Errorf("auth = %T after a failed login, want noAuth", c.auth)
	}
}

// TestBootstrapLoginDetectsTFABothEncodings covers the reason NeedTFA is
// decoded as json.RawMessage: PVE returns the bare number on some versions and
// the quoted string on others, and a decode failure would look like a
// successful login on a cluster that demanded a second factor.
func TestBootstrapLoginDetectsTFABothEncodings(t *testing.T) {
	for _, raw := range []string{`1`, `"1"`} {
		t.Run(raw, func(t *testing.T) {
			c := newFakePVE(t, &fakePVE{needTFA: json.RawMessage(raw)})

			err := c.Login(context.Background(), "root@pam", "hunter2", "")
			var tfaErr *TFARequiredError
			if !errors.As(err, &tfaErr) {
				t.Fatalf("Login error = %v, want TFARequiredError", err)
			}
			if tfaErr.Username != "root@pam" {
				t.Errorf("TFARequiredError.Username = %q, want root@pam", tfaErr.Username)
			}
			if _, ok := c.auth.(noAuth); !ok {
				t.Errorf("auth = %T after a TFA challenge, want noAuth — the partial ticket must not be adopted", c.auth)
			}
		})
	}
}

func TestBootstrapLoginRequiresRealmAndPassword(t *testing.T) {
	c := newFakePVE(t, &fakePVE{})

	if err := c.Login(context.Background(), "root", "hunter2", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Login(no realm) = %v, want ErrInvalidInput", err)
	}
	if err := c.Login(context.Background(), "root@pam", "", ""); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Login(no password) = %v, want ErrInvalidInput", err)
	}
}

func TestMintCreatesUserACLAndToken(t *testing.T) {
	fake := &fakePVE{}
	c := newFakePVE(t, fake)
	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	res, err := c.Mint(context.Background(), MintParams{UserID: "nexara@pve", TokenName: "nexara"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	if res.TokenID != "nexara@pve!nexara" {
		t.Errorf("TokenID = %q", res.TokenID)
	}
	if res.Secret != "s3cr3t-value" {
		t.Errorf("Secret = %q, want the value PVE returned", res.Secret)
	}
	if !res.CreatedUser || !res.CreatedACL {
		t.Errorf("CreatedUser=%v CreatedACL=%v, want both true", res.CreatedUser, res.CreatedACL)
	}
	if !fake.createdUser || !fake.grantedACL {
		t.Errorf("stub saw createdUser=%v grantedACL=%v", fake.createdUser, fake.grantedACL)
	}
	if fake.deletedToken {
		t.Error("a successful mint must not delete the token it just created")
	}

	// The user is created without a password so a half-finished onboarding
	// leaves an account nobody can log in as.
	if got := passwordSentOnUserCreate(t); got != "" {
		t.Errorf("user create sent password %q, want none", got)
	}

	// Verification must use the NEW token, not the ticket.
	if !strings.Contains(fake.verifyAuth, "nexara@pve!nexara=s3cr3t-value") {
		t.Errorf("verification Authorization = %q, want the freshly minted token", fake.verifyAuth)
	}

	wantSteps := []MintStep{
		{Step: "user", Status: "created", Detail: "nexara@pve"},
		{Step: "acl", Status: "created", Detail: "Administrator on /"},
		{Step: "token", Status: "created", Detail: "nexara@pve!nexara"},
		{Step: "verify", Status: "verified", Detail: "authenticated with the new token"},
	}
	if len(res.Steps) != len(wantSteps) {
		t.Fatalf("Steps = %+v, want %d entries", res.Steps, len(wantSteps))
	}
	for i, want := range wantSteps {
		if res.Steps[i] != want {
			t.Errorf("Steps[%d] = %+v, want %+v", i, res.Steps[i], want)
		}
	}
}

// passwordSentOnUserCreate replays the user-create call against a
// body-capturing server. fakePVE records paths only, and the absence of a
// password field is the property that makes a half-finished onboarding inert,
// so it is worth asserting on the wire rather than on the caller.
func passwordSentOnUserCreate(t *testing.T) string {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.FormValue("password")
		jsonResponse(w, nil)
	}))
	t.Cleanup(srv.Close)

	c, err := NewBootstrapClient(BootstrapConfig{BaseURL: srv.URL, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("NewBootstrapClient: %v", err)
	}
	if err := c.createUser(context.Background(), "nexara@pve", "Managed by Nexara"); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	return got
}

// TestMintAdoptsExistingUserAndACL is the forward-idempotent case: a rerun
// after a partial failure must succeed, and must NOT claim it created objects
// that were already there — cluster deletion revokes based on those flags.
func TestMintAdoptsExistingUserAndACL(t *testing.T) {
	fake := &fakePVE{userExists: true, aclAlreadyHeld: true}
	c := newFakePVE(t, fake)
	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	res, err := c.Mint(context.Background(), MintParams{UserID: "nexara@pve", TokenName: "nexara"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	if res.CreatedUser || res.CreatedACL {
		t.Errorf("CreatedUser=%v CreatedACL=%v, want both false when the objects already existed",
			res.CreatedUser, res.CreatedACL)
	}
	if fake.grantedACL {
		t.Error("Mint re-granted an ACL that was already held")
	}
	if res.Steps[0].Status != "existed" || res.Steps[1].Status != "existed" {
		t.Errorf("Steps = %+v, want the user and acl steps reported as existed", res.Steps)
	}
}

// TestMintRefusesToSuffixAnExistingToken pins the 409-not-retry rule:
// auto-suffixing would leave another live privsep=0 Administrator credential
// behind on every attempt.
func TestMintRefusesToSuffixAnExistingToken(t *testing.T) {
	fake := &fakePVE{tokenExists: true}
	c := newFakePVE(t, fake)
	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	_, err := c.Mint(context.Background(), MintParams{UserID: "nexara@pve", TokenName: "nexara"})
	var existsErr *TokenExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("Mint error = %v, want TokenExistsError", err)
	}
	if existsErr.UserID != "nexara@pve" || existsErr.TokenName != "nexara" {
		t.Errorf("TokenExistsError = %+v", existsErr)
	}
	if fake.deletedToken {
		t.Error("Mint deleted a token it did not create")
	}
}

// TestMintRollsBackWhatItCreatedWhenVerificationFails covers the exception to
// the no-rollback rule: everything THIS call created comes back out, so a
// failed onboarding leaves no orphaned Administrator credential and no
// unrecorded account.
func TestMintRollsBackWhatItCreatedWhenVerificationFails(t *testing.T) {
	fake := &fakePVE{verifyForbids: mintVerifyAttempts}
	c := newFakePVE(t, fake)
	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	_, err := c.Mint(context.Background(), MintParams{UserID: "nexara@pve", TokenName: "nexara"})
	if err == nil {
		t.Fatal("Mint succeeded despite verification failing every attempt")
	}
	if !fake.deletedToken {
		t.Error("verification failed but the minted token was not revoked")
	}
	if !fake.deletedUser {
		t.Error("verification failed but the user this call created was left behind")
	}
	if !fake.called(http.MethodDelete, "/access/users/nexara@pve/token/nexara") {
		t.Errorf("calls = %v, want a DELETE of the minted token", fake.calls)
	}

	// Nothing survived, so the error must report no residue.
	var mintErr *MintError
	if !errors.As(err, &mintErr) {
		t.Fatalf("error = %v, want a MintError carrying the partial report", err)
	}
	if mintErr.CreatedUser || mintErr.CreatedACL {
		t.Errorf("MintError reports residue (user=%v acl=%v) after a clean rollback",
			mintErr.CreatedUser, mintErr.CreatedACL)
	}
}

// TestMintLeavesAdoptedObjectsAloneOnRollback: the rollback may only remove
// what this call created. A user and grant that were already there predate the
// request and are not ours to delete.
func TestMintLeavesAdoptedObjectsAloneOnRollback(t *testing.T) {
	fake := &fakePVE{userExists: true, aclAlreadyHeld: true, verifyForbids: mintVerifyAttempts}
	c := newFakePVE(t, fake)
	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	if _, err := c.Mint(context.Background(), MintParams{UserID: "nexara@pve", TokenName: "nexara"}); err == nil {
		t.Fatal("Mint succeeded despite verification failing every attempt")
	}
	if fake.deletedUser {
		t.Error("rollback deleted a user it did not create")
	}
	if fake.revokedACL {
		t.Error("rollback revoked an ACL grant it did not create")
	}
	if !fake.deletedToken {
		t.Error("rollback left the token it did create")
	}
}

// TestMintReportsResidueItCannotRemove is what makes a failed onboarding
// auditable: when the rollback itself fails, the caller must learn exactly
// which objects are still on the cluster.
func TestMintReportsResidueItCannotRemove(t *testing.T) {
	fake := &fakePVE{verifyForbids: mintVerifyAttempts, refuseCleanup: true}
	c := newFakePVE(t, fake)
	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	_, err := c.Mint(context.Background(), MintParams{UserID: "nexara@pve", TokenName: "nexara"})
	var mintErr *MintError
	if !errors.As(err, &mintErr) {
		t.Fatalf("error = %v, want a MintError", err)
	}
	if !mintErr.CreatedUser {
		t.Error("the user could not be deleted but is not reported as residue")
	}
	if mintErr.UserID != "nexara@pve" {
		t.Errorf("MintError.UserID = %q", mintErr.UserID)
	}
	var orphaned int
	for _, step := range mintErr.Steps {
		if step.Status == "orphaned" {
			orphaned++
		}
	}
	if orphaned == 0 {
		t.Errorf("Steps = %+v, want the objects left behind marked orphaned", mintErr.Steps)
	}
}

// TestMintRetriesVerificationThroughACLReplication: a fresh grant reaches the
// answering node a moment late, and a single 403 must not trigger the rollback.
func TestMintRetriesVerificationThroughACLReplication(t *testing.T) {
	fake := &fakePVE{verifyForbids: mintVerifyAttempts - 1}
	c := newFakePVE(t, fake)
	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	res, err := c.Mint(context.Background(), MintParams{UserID: "nexara@pve", TokenName: "nexara"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if fake.deletedToken {
		t.Error("the token was revoked even though verification eventually succeeded")
	}
	if res.Secret == "" {
		t.Error("Secret is empty")
	}
}

func TestMintValidatesIdentifiers(t *testing.T) {
	c := newFakePVE(t, &fakePVE{})

	if _, err := c.Mint(context.Background(), MintParams{UserID: "../root@pam", TokenName: "nexara"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Mint(bad user) = %v, want ErrInvalidInput", err)
	}
	if _, err := c.Mint(context.Background(), MintParams{UserID: "nexara@pve", TokenName: "a/b"}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Mint(bad token name) = %v, want ErrInvalidInput", err)
	}
}

// TestMintResultNeverSerialisesSecret guards the json:"-" tag on MintResult.
// The struct travels through a handler that serialises most of what it touches,
// and Proxmox shows the secret exactly once.
func TestMintResultNeverSerialisesSecret(t *testing.T) {
	raw, err := json.Marshal(MintResult{TokenID: "nexara@pve!nexara", Secret: "s3cr3t-value"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if strings.Contains(string(raw), "s3cr3t-value") {
		t.Fatalf("MintResult serialised its secret: %s", raw)
	}
}

func TestBootstrapCloseDropsTheTicket(t *testing.T) {
	c := newFakePVE(t, &fakePVE{})
	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	c.Close()
	if _, ok := c.auth.(noAuth); !ok {
		t.Errorf("auth = %T after Close, want noAuth so no later call reuses the ticket", c.auth)
	}
}

func TestIsAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"pve user phrasing", errors.New("proxmox API error 500: user 'nexara@pve' already exists"), true},
		{"pve token phrasing", errors.New("proxmox API error 500: token with same name already exists"), true},
		{"mixed case", errors.New("Already Exists"), true},
		{"unrelated", ErrForbidden, false},
		{"not found", ErrNotFound, false},
		{"wrapped", errors.New("mint token: user already exists"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAlreadyExistsError(tc.err); got != tc.want {
				t.Errorf("IsAlreadyExistsError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestNeedsTFAEncodings(t *testing.T) {
	tests := []struct {
		raw    string
		ticket string
		want   bool
	}{
		{"", "PVE:root@pam:OK", false},
		{"0", "PVE:root@pam:OK", false},
		{"null", "PVE:root@pam:OK", false},
		{"false", "PVE:root@pam:OK", false},
		{`"false"`, "PVE:root@pam:OK", false},
		{"1", "PVE:!tfa!x", true},
		{`"1"`, "PVE:!tfa!x", true},
		{"true", "PVE:root@pam:OK", true},
		// The ticket shape alone is enough: a challenge ticket must never be
		// adopted as a completed login even if the flag is missing.
		{"", "PVE:!tfa!challenge", true},
	}
	for _, tc := range tests {
		t.Run(tc.raw+"|"+tc.ticket, func(t *testing.T) {
			resp := ticketResponse{Ticket: tc.ticket}
			if tc.raw != "" {
				resp.NeedTFA = json.RawMessage(tc.raw)
			}
			if got := resp.needsTFA(); got != tc.want {
				t.Errorf("needsTFA() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoginRejectsOversizedPassword(t *testing.T) {
	c := newFakePVE(t, &fakePVE{})
	err := c.Login(context.Background(), "root@pam", strings.Repeat("a", maxBootstrapPasswordLen+1), "")
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("Login(oversized password) = %v, want ErrInvalidInput", err)
	}
}

func TestValidateFullTokenID(t *testing.T) {
	tests := []struct {
		full    string
		wantErr bool
	}{
		{"nexara@pve!nexara", false},
		{"root@pam!nexara-dr", false},
		{"", true},
		{"nexara@pve", true},      // no "!"
		{"nexara!nexara", true},   // no realm
		{"../x@pve!nexara", true}, // traversal in the user part
		{"nexara@pve!../x", true}, // traversal in the token part
		{"nexara@pve!a/b", true},  // separator in the token part
	}
	for _, tc := range tests {
		t.Run(tc.full, func(t *testing.T) {
			err := validateFullTokenID(tc.full)
			if tc.wantErr != (err != nil) {
				t.Errorf("validateFullTokenID(%q) = %v, wantErr %v", tc.full, err, tc.wantErr)
			}
		})
	}
}

// TestMintIgnoresMalformedTokenIDEcho pins the reason a bad echo is not fatal:
// failing here would return AFTER the token was minted and BEFORE verification,
// stranding a live privsep=0 Administrator credential whose secret was thrown
// away.
func TestMintIgnoresMalformedTokenIDEcho(t *testing.T) {
	fake := &fakePVE{malformedTokenEcho: true}
	c := newFakePVE(t, fake)
	if err := c.Login(context.Background(), "root@pam", "hunter2", ""); err != nil {
		t.Fatalf("Login: %v", err)
	}

	res, err := c.Mint(context.Background(), MintParams{UserID: "nexara@pve", TokenName: "nexara"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if res.TokenID != "nexara@pve!nexara" {
		t.Errorf("TokenID = %q, want the locally built id", res.TokenID)
	}
	if fake.deletedToken {
		t.Error("the token was revoked over a cosmetic echo problem")
	}
}
