package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Onboarding tunables.
//
// mintVerifyAttempts/mintVerifyBackoff exist because a fresh ACL grant is
// written to the cluster's replicated user.cfg and the node answering the
// verification request may not have seen it yet. Without the retry, a normal
// few-hundred-millisecond replication lag would look like a failed mint and
// trigger the token rollback.
const (
	mintVerifyAttempts = 3
	mintVerifyBackoff  = 500 * time.Millisecond

	// tfaTicketPrefix marks a PVE two-factor challenge ticket, which is not a
	// usable credential.
	tfaTicketPrefix = "PVE:!tfa!"

	// maxBootstrapPasswordLen bounds what is forwarded to the hypervisor.
	// PVE's own pve-user-password format tops out at 64 characters, so
	// anything longer cannot be a real password and should not be relayed.
	maxBootstrapPasswordLen = 128
)

// BootstrapConfig configures a short-lived onboarding client.
//
// There is deliberately no TokenID/TokenSecret here: the whole point of this
// client is that no API token exists yet.
type BootstrapConfig struct {
	BaseURL        string
	TLSFingerprint string
	Timeout        time.Duration
}

// BootstrapClient is a short-lived, password-authenticated PVE client used
// only while onboarding a cluster.
//
// It exists because POST /access/ticket sets `allowtoken => 0` in the PVE
// source: an API token cannot mint another API token, so there is no
// token-authenticated path to the credential Nexara needs. A ticket is the
// only way in, which is why this is the one client in the package that ever
// holds a password.
//
// Not safe for concurrent use. The auth strategy is swapped in Login and again
// in Close, and the whole object is meant to live inside a single request.
type BootstrapClient struct {
	*apiClient

	cfg BootstrapConfig
}

// NewBootstrapClient builds an unauthenticated client aimed at a PVE host. The
// only call it can make in this state is Login.
func NewBootstrapClient(cfg BootstrapConfig) (*BootstrapClient, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("proxmox: BaseURL is required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	httpClient, tlsCfg := buildHTTPClient(cfg.TLSFingerprint, cfg.Timeout)

	return &BootstrapClient{
		apiClient: &apiClient{
			httpClient: httpClient,
			baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
			// noAuth rather than nil: applyAuth falls back to authHeader when
			// auth is nil, and an explicit strategy documents that sending no
			// credentials here is the intent, not an oversight.
			auth:   noAuth{},
			tlsCfg: tlsCfg,
		},
		cfg: cfg,
	}, nil
}

// ticketResponse is the PVE /access/ticket payload.
//
// NeedTFA is json.RawMessage on purpose: PVE returns it as the number 1 on
// some versions and the string "1" on others, so a typed field fails to decode
// against roughly half the fleet — and a decode failure here would look like a
// successful login on a cluster that actually demanded a second factor.
type ticketResponse struct {
	Ticket              string          `json:"ticket"`
	CSRFPreventionToken string          `json:"CSRFPreventionToken"`
	Username            string          `json:"username"`
	NeedTFA             json.RawMessage `json:"NeedTFA"`
}

// needsTFA reports whether PVE answered the ticket request with a second-factor
// challenge rather than a usable ticket.
//
// Two signals, because getting this wrong means adopting a partial ticket as a
// full one: the NeedTFA flag, and the shape of the ticket itself. PVE issues
// challenge tickets prefixed "PVE:!tfa!", so a future response that drops or
// renames the flag still cannot be mistaken for a completed login.
func (t ticketResponse) needsTFA() bool {
	if strings.HasPrefix(t.Ticket, tfaTicketPrefix) {
		return true
	}
	raw := strings.ToLower(strings.Trim(strings.TrimSpace(string(t.NeedTFA)), `"`))
	switch raw {
	case "", "0", "null", "false":
		return false
	}
	return true
}

// Login exchanges a username and password for a PVE ticket and swaps the
// client onto cookie authentication.
//
// otp is optional and rides the same request: PVE's /access/ticket accepts a
// TOTP code inline, which is why this never implements the two-step
// /access/tfa challenge exchange.
func (b *BootstrapClient) Login(ctx context.Context, username, password, otp string) error {
	if err := validateUserID(username); err != nil {
		// Context first, wrapped error last — otherwise the rendered 400 reads
		// as two sentences fighting each other.
		return fmt.Errorf("the bootstrap username must include its realm, e.g. root@pam: %w", err)
	}
	if password == "" {
		return fmt.Errorf("%w: password is required", ErrInvalidInput)
	}
	if len(password) > maxBootstrapPasswordLen {
		return fmt.Errorf("%w: password is too long (max %d)", ErrInvalidInput, maxBootstrapPasswordLen)
	}
	if otp != "" && (len(otp) > 64 || hasControlChar(otp)) {
		return fmt.Errorf("%w: one-time code is not a valid format", ErrInvalidInput)
	}

	form := url.Values{}
	form.Set("username", username)
	form.Set("password", password)
	if otp != "" {
		form.Set("otp", otp)
	}

	var ticket ticketResponse
	if err := b.doPost(ctx, "/access/ticket", form, &ticket); err != nil {
		// PVE answers bad credentials with 401. Map it to a typed error so the
		// handler can say "wrong password" rather than "Proxmox said no",
		// while genuine transport failures keep their own shape.
		if errors.Is(err, ErrForbidden) {
			return ErrBootstrapAuthFailed
		}
		return fmt.Errorf("request ticket for %s: %w", username, err)
	}

	if ticket.needsTFA() {
		return &TFARequiredError{Username: username}
	}
	if ticket.Ticket == "" || ticket.CSRFPreventionToken == "" {
		return fmt.Errorf("%w: ticket response carried no ticket", ErrInvalidResponse)
	}

	b.auth = ticketAuth{ticket: ticket.Ticket, csrf: ticket.CSRFPreventionToken}
	return nil
}

// MintParams describes the credential to create.
type MintParams struct {
	// UserID is the PVE user that will own the token, e.g. "nexara@pve".
	UserID string
	// TokenName is the bare token name, e.g. "nexara" in "nexara@pve!nexara".
	TokenName string
	// Comment is written onto both the user and the token so an operator
	// reading user.cfg can tell where the credential came from.
	Comment string
}

// MintStep is one line of the onboarding report shown to the operator.
type MintStep struct {
	// Step is the stage: "user", "acl", "token" or "verify".
	Step string `json:"step"`
	// Status is "created", "existed" or "verified".
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// MintResult is the outcome of Mint.
//
// Secret is tagged json:"-" deliberately. This struct travels through a
// handler that serialises most of what it touches, and the secret must never
// reach the browser, a log line or an audit row — Proxmox shows it exactly
// once, so the only correct destination is the encrypted column.
// TestMintResultNeverSerialisesSecret pins the tag.
type MintResult struct {
	TokenID string `json:"token_id"`
	Secret  string `json:"-"`

	// CreatedUser and CreatedACL record what this call brought into existence,
	// as opposed to what it found already there. Cluster deletion consults
	// them so revocation only ever removes what Nexara created.
	CreatedUser bool `json:"created_user"`
	CreatedACL  bool `json:"created_acl"`

	Steps []MintStep `json:"steps"`
}

// MintError carries a failed Mint's partial progress out to the caller.
//
// Without it, "the token name was taken" and "the token name was taken AFTER we
// created a user and granted it Administrator on /" are the same error value,
// and the second leaves objects on the hypervisor that nothing in Nexara knows
// about. The handler audits these fields so the residue is written down even
// though no cluster row is created.
type MintError struct {
	Err    error
	UserID string

	// CreatedUser and CreatedACL describe what this call brought into
	// existence before it failed — the same meaning they have on MintResult.
	CreatedUser bool
	CreatedACL  bool

	Steps []MintStep
}

func (e *MintError) Error() string { return e.Err.Error() }
func (e *MintError) Unwrap() error { return e.Err }

// Mint creates the dedicated user, grants it Administrator on /, and issues a
// privsep=0 API token, then proves the token works.
//
// The sequence is forward-idempotent rather than transactional: every step
// either creates its object or accepts that it already exists, and nothing is
// rolled back on failure. That is what makes a partial run inert. The user is
// created WITHOUT a password, so the worst a half-finished onboarding can
// leave behind is an account nobody can log in as.
//
// The single exception is post-mint verification: if the token cannot be used
// after it is issued, the token is deleted, because an unverified
// privsep=0 Administrator credential that nobody holds is exactly the artefact
// this whole design exists to avoid.
func (b *BootstrapClient) Mint(ctx context.Context, params MintParams) (*MintResult, error) {
	if err := validateUserID(params.UserID); err != nil {
		return nil, err
	}
	if err := validateTokenID(params.TokenName); err != nil {
		return nil, err
	}

	result := &MintResult{TokenID: params.UserID + "!" + params.TokenName}
	comment := params.Comment
	if comment == "" {
		comment = "Managed by Nexara"
	}

	// Every failure from here on may leave objects behind, so it reports what
	// it got as far as creating. Validation failures above cannot, and stay
	// plain; errors.Is/As still see through MintError either way via Unwrap.
	fail := func(err error) (*MintResult, error) {
		return nil, &MintError{
			Err:         err,
			UserID:      params.UserID,
			CreatedUser: result.CreatedUser,
			CreatedACL:  result.CreatedACL,
			Steps:       result.Steps,
		}
	}

	// 1. The owning user. Passwordless by design — see the doc comment.
	createErr := b.createUser(ctx, params.UserID, comment)
	switch {
	case createErr == nil:
		result.CreatedUser = true
		result.Steps = append(result.Steps, MintStep{Step: "user", Status: "created", Detail: params.UserID})
	case IsAlreadyExistsError(createErr):
		result.Steps = append(result.Steps, MintStep{Step: "user", Status: "existed", Detail: params.UserID})
	default:
		return fail(fmt.Errorf("create bootstrap user %s: %w", params.UserID, createErr))
	}

	// 2. Administrator on /. Checked before granting so that deletion can
	//    later distinguish "Nexara added this" from "the operator already had
	//    it" and revoke only the former.
	held, err := b.holdsAdministratorOnRoot(ctx, params.UserID)
	if err != nil {
		return fail(fmt.Errorf("read acl before granting %s: %w", params.UserID, err))
	}
	if held {
		result.Steps = append(result.Steps, MintStep{Step: "acl", Status: "existed", Detail: "Administrator on /"})
	} else {
		if err := b.grantAdministratorOnRoot(ctx, params.UserID); err != nil {
			return fail(fmt.Errorf("grant Administrator on / to %s: %w", params.UserID, err))
		}
		result.CreatedACL = true
		result.Steps = append(result.Steps, MintStep{Step: "acl", Status: "created", Detail: "Administrator on /"})
	}

	// 3. The token itself. A name collision is a hard 409 and never
	//    auto-suffixed: silently minting "nexara-2" every retry accumulates
	//    live privsep=0 Administrator credentials that nobody holds.
	created, err := b.mintToken(ctx, params, comment)
	if err != nil {
		return fail(err)
	}
	result.Secret = created.Value
	// Adopt the id Proxmox echoes back, but re-validate it first: it becomes an
	// Authorization header, a stored column and an audit value, and nothing
	// else re-checks a field that arrives from the network.
	//
	// A malformed echo is IGNORED rather than fatal. Failing here would return
	// before step 4, stranding the token that was just minted — a live
	// privsep=0 Administrator credential whose secret has been discarded, which
	// is the exact artefact the verification rollback exists to prevent. The
	// locally built id is the one we asked for and is already validated, so
	// falling back to it is both safe and correct.
	if created.FullTokenID != "" {
		if err := validateFullTokenID(created.FullTokenID); err != nil {
			result.Steps = append(result.Steps, MintStep{
				Step: "token", Status: "created",
				Detail: "ignored a malformed token id echoed by the server; using " + result.TokenID,
			})
		} else {
			result.TokenID = created.FullTokenID
		}
	}
	result.Steps = append(result.Steps, MintStep{Step: "token", Status: "created", Detail: result.TokenID})

	// 4. Prove it works before anyone stores it.
	if err := b.verifyMintedToken(ctx, result.TokenID, result.Secret); err != nil {
		rb := b.RollbackMint(ctx, params, result.CreatedUser, result.CreatedACL)
		result.Steps = append(result.Steps, rb.Steps...)
		// Report as residue only what the rollback could not remove, so the
		// audit row names objects that are genuinely still on the cluster.
		result.CreatedUser, result.CreatedACL = rb.RemainingUser, rb.RemainingACL
		return fail(fmt.Errorf("verify minted token: %w", err))
	}
	result.Steps = append(result.Steps, MintStep{Step: "verify", Status: "verified", Detail: "authenticated with the new token"})

	return result, nil
}

// createUser creates the token-owning account with no password.
func (b *BootstrapClient) createUser(ctx context.Context, userid, comment string) error {
	form := url.Values{}
	form.Set("userid", userid)
	form.Set("comment", comment)
	form.Set("enable", boolToPVE(true))
	return b.doPost(ctx, "/access/users", form, nil)
}

// holdsAdministratorOnRoot reports whether userid already carries the
// Administrator role at "/".
func (b *BootstrapClient) holdsAdministratorOnRoot(ctx context.Context, userid string) (bool, error) {
	var entries []AccessACLEntry
	if err := b.do(ctx, "/access/acl", &entries); err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Path == "/" && e.UGID == userid && e.RoleID == RoleAdministrator && e.Type == "user" {
			return true, nil
		}
	}
	return false, nil
}

// grantAdministratorOnRoot gives userid full control of the cluster, with
// propagate set so the grant reaches every path below "/".
func (b *BootstrapClient) grantAdministratorOnRoot(ctx context.Context, userid string) error {
	form := url.Values{}
	form.Set("path", "/")
	form.Set("roles", RoleAdministrator)
	form.Set("users", userid)
	form.Set("propagate", boolToPVE(true))
	return b.doPut(ctx, "/access/acl", form, nil)
}

// mintToken issues the API token, translating a name collision into a typed
// conflict the handler renders as 409.
func (b *BootstrapClient) mintToken(ctx context.Context, params MintParams, comment string) (*AccessTokenCreated, error) {
	form := url.Values{}
	form.Set("comment", comment)
	// privsep=0: the token carries its owner's privileges rather than needing
	// a second ACL of its own. That is the whole point of granting the user
	// Administrator on "/".
	form.Set("privsep", boolToPVE(false))

	var created AccessTokenCreated
	path := "/access/users/" + url.PathEscape(params.UserID) + "/token/" + url.PathEscape(params.TokenName)
	if err := b.doPost(ctx, path, form, &created); err != nil {
		if IsAlreadyExistsError(err) {
			return nil, &TokenExistsError{UserID: params.UserID, TokenName: params.TokenName}
		}
		return nil, fmt.Errorf("mint token %s!%s: %w", params.UserID, params.TokenName, err)
	}
	if created.Value == "" {
		return nil, fmt.Errorf("%w: token response carried no secret", ErrInvalidResponse)
	}
	return &created, nil
}

// verifyMintedToken authenticates with the brand-new credential and makes the
// same call the collector will make, so a token that cannot actually drive the
// cluster is caught here rather than at the first sync.
func (b *BootstrapClient) verifyMintedToken(ctx context.Context, tokenID, secret string) error {
	client, err := NewClient(ClientConfig{
		BaseURL:        b.cfg.BaseURL,
		TokenID:        tokenID,
		TokenSecret:    secret,
		TLSFingerprint: b.cfg.TLSFingerprint,
		Timeout:        b.cfg.Timeout,
	})
	if err != nil {
		return fmt.Errorf("build verification client: %w", err)
	}
	// This client is used for a handful of requests and then dropped. Without
	// this its keep-alive connections sit in the pool for IdleConnTimeout (90s)
	// per onboarding, holding a TLS session for a credential nothing else uses.
	defer func() {
		if tr, ok := client.httpClient.Transport.(*http.Transport); ok {
			tr.CloseIdleConnections()
		}
	}()

	var lastErr error
	for attempt := 0; attempt < mintVerifyAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(mintVerifyBackoff):
			}
		}
		if _, err := client.GetClusterStatus(ctx); err != nil {
			lastErr = err
			// Only a permission failure is worth retrying: that is the shape
			// an ACL still replicating across the cluster takes. Anything else
			// is not going to fix itself in a second and a half.
			if errors.Is(err, ErrForbidden) {
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}

// MintRollback reports the outcome of undoing a failed Mint.
type MintRollback struct {
	Steps []MintStep

	// RemainingUser and RemainingACL are true when the object was created by
	// this request but could NOT be removed, so it is still on the cluster.
	// The caller records these; they are the only genuine residue.
	RemainingUser bool
	RemainingACL  bool
}

// RollbackMint removes what a single failed Mint call created — and nothing
// else. createdUser/createdACL come from that call's own progress, so an
// adopted pre-existing user or a grant that was already there is never touched.
//
// Unlike the delete-time revocation in the handler layer, this runs on the
// bootstrap TICKET rather than on the token being removed. There is no
// self-destruction problem here: deleting the token does not affect the
// ticket's own authority, so the steps can run in any order.
//
// Best-effort throughout. The caller has already failed and is about to return
// an error; what matters is that anything left behind is named.
func (b *BootstrapClient) RollbackMint(ctx context.Context, params MintParams, createdUser, createdACL bool) MintRollback {
	var rb MintRollback

	// The token first: it is the only object here that is a usable credential.
	if err := b.DeleteAccessUserTokenForCleanup(ctx, params.UserID, params.TokenName); err != nil {
		rb.Steps = append(rb.Steps, MintStep{
			Step: "token", Status: "orphaned",
			Detail: params.UserID + "!" + params.TokenName + ": " + err.Error(),
		})
	} else {
		rb.Steps = append(rb.Steps, MintStep{Step: "token", Status: "removed", Detail: params.UserID + "!" + params.TokenName})
	}

	// Deleting the user takes its remaining tokens and ACL entries with it, so
	// it subsumes the grant.
	if createdUser {
		if err := b.deleteUserForCleanup(ctx, params.UserID); err != nil {
			rb.RemainingUser = true
			rb.RemainingACL = createdACL
			rb.Steps = append(rb.Steps, MintStep{Step: "user", Status: "orphaned", Detail: params.UserID + ": " + err.Error()})
		} else {
			rb.Steps = append(rb.Steps, MintStep{Step: "user", Status: "removed", Detail: params.UserID})
		}
		return rb
	}

	if createdACL {
		if err := b.revokeAdministratorOnRoot(ctx, params.UserID); err != nil {
			rb.RemainingACL = true
			rb.Steps = append(rb.Steps, MintStep{Step: "acl", Status: "orphaned", Detail: RoleAdministrator + " on / for " + params.UserID + ": " + err.Error()})
		} else {
			rb.Steps = append(rb.Steps, MintStep{Step: "acl", Status: "removed", Detail: RoleAdministrator + " on / for " + params.UserID})
		}
	}
	return rb
}

// DeleteAccessUserTokenForCleanup revokes a token using the bootstrap ticket.
//
// Named apart from Client.DeleteAccessUserToken because it is reachable while
// the client still holds a password-derived ticket rather than an API token.
func (b *BootstrapClient) DeleteAccessUserTokenForCleanup(ctx context.Context, userid, tokenName string) error {
	if err := validateUserID(userid); err != nil {
		return err
	}
	if err := validateTokenID(tokenName); err != nil {
		return err
	}
	path := "/access/users/" + url.PathEscape(userid) + "/token/" + url.PathEscape(tokenName)
	return b.doDelete(ctx, path, nil)
}

// deleteUserForCleanup removes a user this request created. PVE deletes the
// user's tokens and ACL entries along with it.
func (b *BootstrapClient) deleteUserForCleanup(ctx context.Context, userid string) error {
	if err := validateUserID(userid); err != nil {
		return err
	}
	return b.doDelete(ctx, "/access/users/"+url.PathEscape(userid), nil)
}

// revokeAdministratorOnRoot undoes grantAdministratorOnRoot.
func (b *BootstrapClient) revokeAdministratorOnRoot(ctx context.Context, userid string) error {
	form := url.Values{}
	form.Set("path", "/")
	form.Set("roles", RoleAdministrator)
	form.Set("users", userid)
	form.Set("delete", "1")
	return b.doPut(ctx, "/access/acl", form, nil)
}

// Close drops the ticket and releases the connections it travelled over.
//
// PVE has no ticket-revocation endpoint — tickets simply expire — so this
// cannot invalidate the credential remotely. What it can do is make sure no
// later call on this object accidentally reuses it, and that no idle TLS
// connection stays open holding the association.
func (b *BootstrapClient) Close() {
	b.auth = noAuth{}
	if tr, ok := b.httpClient.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}
