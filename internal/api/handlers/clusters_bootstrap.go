package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Credential provenance values stored in clusters.credential_source.
const (
	// credentialSourceManual: the operator pasted an existing API token.
	credentialSourceManual = "manual"
	// credentialSourceBootstrap: Nexara minted the token during onboarding.
	credentialSourceBootstrap = "bootstrap"
)

// Defaults for the credential Nexara mints.
//
// The realm is "pve" rather than "pam": a PVE-realm user is a Proxmox-internal
// account with no shell, no home directory and no presence in /etc/passwd, so
// it cannot be used to log into a node even if the cluster later gains a
// password for it.
const (
	defaultBootstrapUserID    = "nexara@pve"
	defaultBootstrapTokenName = "nexara"
)

// bootstrapTimeout bounds the whole onboarding conversation: login, user
// create, ACL grant, token mint and verification, including the verification
// retries that ride out ACL replication.
const bootstrapTimeout = 45 * time.Second

// bootstrapRequest is the `bootstrap` sub-object on POST /api/v1/clusters.
//
// Supplying it asks Nexara to create its own credential instead of being handed
// one, which is why it is mutually exclusive with token_id/token_secret.
//
// Password and OTP are write-only in the strictest sense: they exist for the
// duration of one request, are never persisted, never logged and never
// audited. What survives is the minted token's ciphertext.
type bootstrapRequest struct {
	// Username is a privileged PVE account in name@realm form, e.g. root@pam.
	Username string `json:"username"`
	Password string `json:"password"`
	// OTP is the account's current two-factor code, when it has one. PVE's
	// /access/ticket takes it inline, so no challenge exchange is needed.
	OTP string `json:"otp,omitempty"`
	// UserID and TokenName override what gets created. Defaulted rather than
	// required so the common case is a password and nothing else.
	UserID    string `json:"user_id,omitempty"`
	TokenName string `json:"token_name,omitempty"`
}

// bootstrapSummary is the operator-facing report of what onboarding did.
// It carries object names only — the minted secret is never in it.
type bootstrapSummary struct {
	TokenID string             `json:"token_id"`
	Steps   []proxmox.MintStep `json:"steps"`
}

// clusterCredential is the resolved credential plus its provenance, ready to
// be written to the clusters row.
type clusterCredential struct {
	TokenID string
	// Secret is plaintext and lives only until the caller encrypts it.
	Secret string

	Source      string
	UserID      string
	TokenName   string
	CreatedUser bool
	CreatedACL  bool
	MintedAt    time.Time

	Steps []proxmox.MintStep
}

// String, LogValue and MarshalJSON make the credential impossible to leak by
// accident, rather than merely un-leaked today.
//
// TestGuard_BootstrapPasswordReachesOnlyLogin catches a direct `.Password`
// read, but it cannot see `fmt.Sprintf("%+v", req)`, `slog.Any("req", req)` or
// `json.Marshal(req)` — each of which serialises the whole struct, and each of
// which is a natural thing to write while debugging a failed onboarding.
// renderBootstrapError has the whole struct in scope at exactly the point error
// text is assembled, so the redaction goes on the type.
//
// Only the marshal direction is overridden; request-body decoding is
// unaffected, since UnmarshalJSON is a separate interface.
func (b bootstrapRequest) String() string {
	return "bootstrapRequest{username:" + b.Username + " password:REDACTED otp:REDACTED}"
}

// GoString closes the one route String does not reach. fmt dispatches
// Stringer for %v, %s, %q, %x and %X but GoStringer for %#v — so without this
// a `%#v` written while debugging a failed onboarding prints the struct
// literal with the password and OTP in it, for the value, for a pointer to it
// and for anything holding one as an EXPORTED field. (An unexported field is
// not covered: fmt cannot call a method through one. See the note on
// clusterCredential below.) String has been here since the type was hardened;
// GoString was the gap.
func (b bootstrapRequest) GoString() string {
	return `handlers.bootstrapRequest{Username:"` + b.Username +
		`", Password:"REDACTED", OTP:"REDACTED", UserID:"` + b.UserID +
		`", TokenName:"` + b.TokenName + `"}`
}

func (b bootstrapRequest) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("username", b.Username),
		slog.String("user_id", b.UserID),
		slog.String("token_name", b.TokenName),
	)
}

func (b bootstrapRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Username  string `json:"username"`
		UserID    string `json:"user_id,omitempty"`
		TokenName string `json:"token_name,omitempty"`
	}{b.Username, b.UserID, b.TokenName})
}

// String, GoString, LogValue and MarshalJSON give clusterCredential the same
// protection bootstrapRequest has had, and closing that asymmetry is the whole
// point: the two types live in this file, are built within a few lines of each
// other, and one of them held a plaintext credential that any rendering would
// publish while the other did not.
//
// Secret is the minted (or operator-supplied) API token in plaintext. It exists
// between resolution and the encrypted column, and everything in that window
// has the whole struct in lexical scope: the audit-detail map assembled in
// CreateCluster (clusters.go), the slog.Error on the failed-insert rollback
// path a few lines above it, and the connectivity test that follows. Nothing
// renders one today; these methods mean the first thing that does cannot leak.
//
// Between them the four cover every route that dispatches on the type:
// %v/%s/%q/%x/%X, %+v, fmt.Sprint and error wrapping via String; %#v via
// GoString; structured logs via LogValue; encoding/json via MarshalJSON —
// including a credential held as an EXPORTED field of a larger struct.
//
// What is NOT covered, listed so the set above is not read as "everything".
// None is reachable today; they are recorded because the next person to
// create one of these shapes should know it is not covered:
//
//   - A verb fmt cannot dispatch these for — %d, %t, %p, %c, %f — falls back
//     to printing the fields and shows Secret.
//   - A credential in an UNEXPORTED field. fmt cannot call a method through
//     one, so %+v and %#v of the outer struct print the raw fields. This is a
//     real shape in this tree: internal/rolling/orchestrator.go holds a
//     credential-bearing config in an unexported field today.
//   - An ANONYMOUS embed. `struct{ clusterCredential; Extra string }` promotes
//     these methods to the outer type, so json.Marshal emits only the redacted
//     object and silently drops Extra, and %v renders only the inner. Safe for
//     the secret, wrong for everything else — embed it as a NAMED field.
//   - Reflection encoders that ignore MarshalJSON: encoding/xml and
//     encoding/gob both emit Secret verbatim.
//
// TokenID, Source and the provenance fields stay visible. None is a secret, and
// a rendering that identifies nothing is not worth emitting.
//
// VALUE receivers on all four, even though every call site holds a
// *clusterCredential. A value receiver is in the method set of both the value
// and the pointer; a pointer receiver is in the pointer's only, and fmt and
// encoding/json skip it on any value they cannot address — so a pointer
// receiver here would compile, lint clean and silently leave `%v` of a
// dereferenced credential wide open.
//
// Only the marshal direction is overridden. UnmarshalJSON is a separate
// interface, and nothing decodes this type — it is assembled in Go, never
// parsed.
func (c clusterCredential) String() string {
	return "clusterCredential{token_id:" + c.TokenID + " secret:REDACTED source:" + c.Source +
		" user_id:" + c.UserID + " token_name:" + c.TokenName + "}"
}

func (c clusterCredential) GoString() string {
	return `handlers.clusterCredential{TokenID:"` + c.TokenID +
		`", Secret:"REDACTED", Source:"` + c.Source +
		`", UserID:"` + c.UserID + `", TokenName:"` + c.TokenName + `"}`
}

func (c clusterCredential) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("token_id", c.TokenID),
		slog.String("source", c.Source),
		slog.String("user_id", c.UserID),
		slog.String("token_name", c.TokenName),
		slog.Bool("created_user", c.CreatedUser),
		slog.Bool("created_acl", c.CreatedACL),
	)
}

func (c clusterCredential) MarshalJSON() ([]byte, error) {
	// A POINTER, not a time.Time with omitempty. encoding/json never treats a
	// struct as empty, so omitempty does nothing on a time.Time and the
	// manual path — which never mints, and so never sets MintedAt — would
	// emit "0001-01-01T00:00:00Z" and read as a real date. That is the exact
	// confusion mintedAtColumn exists to avoid on the DB side; the pointer
	// does the same job on the JSON side.
	var mintedAt *time.Time
	if !c.MintedAt.IsZero() {
		mintedAt = &c.MintedAt
	}
	return json.Marshal(struct {
		TokenID     string             `json:"token_id"`
		Source      string             `json:"source"`
		UserID      string             `json:"user_id,omitempty"`
		TokenName   string             `json:"token_name,omitempty"`
		CreatedUser bool               `json:"created_user"`
		CreatedACL  bool               `json:"created_acl"`
		MintedAt    *time.Time         `json:"minted_at,omitempty"`
		Steps       []proxmox.MintStep `json:"steps,omitempty"`
	}{c.TokenID, c.Source, c.UserID, c.TokenName, c.CreatedUser, c.CreatedACL, mintedAt, c.Steps})
}

// validate checks the sub-object and fills in the defaults.
func (b *bootstrapRequest) validate() error {
	if b.Username == "" || b.Password == "" {
		return fiber.NewError(fiber.StatusBadRequest, "bootstrap.username and bootstrap.password are required")
	}
	if b.UserID == "" {
		b.UserID = defaultBootstrapUserID
	}
	if b.TokenName == "" {
		b.TokenName = defaultBootstrapTokenName
	}

	// The account being CREATED must live in a Proxmox-internal realm.
	// A @pam user id maps to a real Unix account on every node, so minting one
	// here would either fail or, worse, attach cluster-wide Administrator to a
	// shell account. The login account (b.Username) is unrestricted — root@pam
	// is the normal answer there.
	if strings.HasSuffix(b.UserID, "@pam") {
		return fiber.NewError(fiber.StatusBadRequest,
			"bootstrap.user_id must not be a @pam account — that realm maps to real shell users on the node. Use a PVE-realm id such as nexara@pve.")
	}
	return nil
}

// runClusterBootstrap logs in with the operator's password and mints the
// cluster's own credential.
//
// The mint deliberately runs BEFORE the clusters row is inserted. If anything
// here fails there is no half-configured cluster to clean up, and the operator
// sees the Proxmox-side reason rather than a cluster that exists but cannot
// talk to anything.
//
// The still-authenticated client is returned alongside the credential so the
// caller can revoke the token it just minted if the insert then fails. The
// caller owns it and must Close it; it is returned even on error, and is nil
// only when the client could not be constructed at all.
func runClusterBootstrap(ctx context.Context, apiURL, tlsFingerprint string, req *bootstrapRequest) (*clusterCredential, *proxmox.BootstrapClient, error) {
	client, err := proxmox.NewBootstrapClient(proxmox.BootstrapConfig{
		BaseURL:        apiURL,
		TLSFingerprint: tlsFingerprint,
		Timeout:        bootstrapTimeout,
	})
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, bootstrapTimeout)
	defer cancel()

	if err := client.Login(ctx, req.Username, req.Password, req.OTP); err != nil {
		return nil, client, err
	}

	result, err := client.Mint(ctx, proxmox.MintParams{
		UserID:    req.UserID,
		TokenName: req.TokenName,
		Comment:   "Managed by Nexara",
	})
	if err != nil {
		return nil, client, err
	}

	return &clusterCredential{
		TokenID:     result.TokenID,
		Secret:      result.Secret,
		Source:      credentialSourceBootstrap,
		UserID:      req.UserID,
		TokenName:   req.TokenName,
		CreatedUser: result.CreatedUser,
		CreatedACL:  result.CreatedACL,
		MintedAt:    time.Now().UTC(),
		Steps:       result.Steps,
	}, client, nil
}

// mintedAtColumn renders the mint timestamp for the clusters row. A manually
// supplied credential was never minted, so it stores SQL NULL rather than a
// zero time that would read as 0001-01-01.
func (c *clusterCredential) mintedAtColumn() pgtype.Timestamptz {
	if c.MintedAt.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: c.MintedAt, Valid: true}
}

// renderBootstrapError maps an onboarding failure onto a status the UI can act
// on, rather than collapsing everything into a 502.
//
// Anything wrong with the SUPPLIED Proxmox credential answers 422, never 401 or
// 403. Two different authentication subjects are in play on this endpoint — the
// Nexara caller, who is authenticated and holds manage:cluster, and the Proxmox
// account named in the request body — and the usual codes describe the first.
// Using them for the second is not merely imprecise: the frontend api-client
// treats any 401 as an expired session, refreshes, and replays the request. A
// wrong password would therefore cost two /access/ticket attempts per click,
// which is how an operator gets their root@pam account locked out or their IP
// banned by pveproxy's fail2ban jail for what looked like one typo.
//
// 422 is the same shape the private-address gate already uses: "the request is
// well-formed, but something in it needs your attention".
func renderBootstrapError(c fiber.Ctx, err error, req *bootstrapRequest) error {
	var tfaErr *proxmox.TFARequiredError
	if errors.As(err, &tfaErr) {
		// The dialog reveals its one-time code field on this marker and the
		// operator resubmits the same form.
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "tfa_required",
			// Names the mechanism, because only a TOTP code can be answered
			// here: PVE takes it inline on /access/ticket, whereas WebAuthn or
			// a U2F key needs the challenge exchange this flow does not
			// implement. An operator whose account has only a hardware factor
			// would otherwise retry forever.
			"message": fmt.Sprintf(
				"%s has two-factor authentication enabled. Enter the current code from its authenticator app. Hardware keys (WebAuthn/U2F) cannot be used here — add the cluster with an API token instead.",
				req.Username),
		})
	}

	var existsErr *proxmox.TokenExistsError
	if errors.As(err, &existsErr) {
		// 409, never an auto-suffixed retry. See TokenExistsError for why.
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "token_exists",
			"message": fmt.Sprintf(
				"The API token %s!%s already exists on this cluster. Choose a different token name, or delete the existing token in Proxmox first.",
				existsErr.UserID, existsErr.TokenName),
			"details": fiber.Map{
				"user_id":    existsErr.UserID,
				"token_name": existsErr.TokenName,
			},
		})
	}

	switch {
	case errors.Is(err, proxmox.ErrBootstrapAuthFailed):
		// PVE answers a wrong password and a wrong TOTP code with the same 401,
		// so once a code is in play the message must not point at one field.
		msg := "Proxmox rejected that username or password."
		if req.OTP != "" {
			msg = "Proxmox rejected that login. Check the one-time code — it may have expired — and the password."
		}
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error":   "bootstrap_auth_failed",
			"message": msg,
		})
	case errors.Is(err, proxmox.ErrForbidden):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error": "bootstrap_forbidden",
			"message": fmt.Sprintf(
				"%s cannot create users or tokens on this cluster. Onboarding needs an account with the Administrator role.", req.Username),
		})
	case errors.Is(err, proxmox.ErrInvalidInput):
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	case errors.Is(err, proxmox.ErrConnectionFailed):
		return fiber.NewError(fiber.StatusBadGateway, "Could not reach the Proxmox host to set up the credential.")
	default:
		return fiber.NewError(fiber.StatusBadGateway, fmt.Sprintf("Proxmox rejected the credential setup: %s", err.Error()))
	}
}

// maxAuditDetail bounds upstream-controlled text before it lands in an audit
// row. The Proxmox error body is echoed into these details, view:audit is held
// by every built-in Viewer, and nothing upstream bounds that body.
const maxAuditDetail = 300

// auditSafe trims a message to a length an audit row can carry and strips the
// control characters that would corrupt a log line or a CSV export.
func auditSafe(msg string) string {
	msg = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, msg)
	msg = strings.TrimSpace(msg)
	if len(msg) > maxAuditDetail {
		return msg[:maxAuditDetail] + "…"
	}
	return msg
}

// revocationStep records one action taken while removing a cluster's
// Nexara-created Proxmox credential.
type revocationStep struct {
	Step   string `json:"step"`
	Status string `json:"status"` // "revoked", "skipped" or "failed"
	Detail string `json:"detail,omitempty"`
}

// revokeBootstrapCredentials removes the PVE objects Nexara created for this
// cluster, and only those.
//
// The governing rule is one question, asked once: does anything OTHER than this
// cluster's own token depend on the account?
//
//   - Another Nexara cluster row authenticating as it.
//   - Any API token on it that Nexara did not mint for this cluster.
//   - Or we could not find out, because the listing failed.
//
// If yes, remove this cluster's token and nothing else — not the user, and NOT
// the role grant. Both are load-bearing for the other credentials: a privsep=0
// token inherits its owner's privileges outright, and a privsep=1 token's
// effective rights are the intersection with its owner's, so stripping the
// account's only grant kills them exactly as dead as deleting the account
// would. Guarding the user delete alone leaves that door wide open.
//
// If no, the account exists solely for this cluster, and deleting the user is a
// single call — PVE takes its tokens and ACL entries with it.
//
// Every step is best-effort. A cluster being deleted is frequently one that is
// already gone, and an unreachable host must not block the deletion, so
// failures are recorded for the audit trail rather than returned.
func revokeBootstrapCredentials(ctx context.Context, cluster db.Cluster, tokenSecret string, sharedWithAnotherCluster bool) []revocationStep {
	if cluster.CredentialSource != credentialSourceBootstrap || cluster.BootstrapUserID == "" {
		return []revocationStep{{
			Step:   "revoke",
			Status: "skipped",
			Detail: "Nexara did not create this cluster's credential",
		}}
	}

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        15 * time.Second,
	})
	if err != nil {
		return []revocationStep{{Step: "revoke", Status: "failed", Detail: auditSafe(err.Error())}}
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if shared, reason := accountHasOtherDependents(ctx, client, cluster, sharedWithAnotherCluster); shared {
		return []revocationStep{
			{Step: "user", Status: "skipped", Detail: reason},
			revokeOwnToken(ctx, client, cluster),
		}
	}

	if cluster.BootstrapCreatedUser {
		step := revocationStep{Step: "user", Detail: cluster.BootstrapUserID}
		if err := client.DeleteAccessUser(ctx, cluster.BootstrapUserID); err != nil {
			step.Status = "failed"
			step.Detail = auditSafe(fmt.Sprintf("%s: %s", cluster.BootstrapUserID, err.Error()))
		} else {
			// The user took its tokens and ACL entries with it.
			step.Status = "revoked"
		}
		return []revocationStep{step}
	}

	// The user was adopted, so only the grant and the token are ours to remove.
	// The ACL goes first: it still carries full privileges at this point,
	// whereas the token delete may 403 afterwards — and the residue of a failed
	// token delete (a credential whose owner has no rights) is inert, where a
	// live Administrator grant is not.
	steps := make([]revocationStep, 0, 2)
	if cluster.BootstrapCreatedAcl {
		step := revocationStep{Step: "acl", Detail: proxmox.RoleAdministrator + " on / for " + cluster.BootstrapUserID}
		err := client.UpdateAccessACL(ctx, proxmox.UpdateAccessACLParams{
			Path:   "/",
			Roles:  proxmox.RoleAdministrator,
			Users:  cluster.BootstrapUserID,
			Delete: true,
		})
		if err != nil {
			step.Status = "failed"
			step.Detail = auditSafe(step.Detail + ": " + err.Error())
		} else {
			step.Status = "revoked"
		}
		steps = append(steps, step)
	}
	return append(steps, revokeOwnToken(ctx, client, cluster))
}

// accountHasOtherDependents reports whether anything besides this cluster's own
// token relies on the PVE account, with a reason fit for an audit row.
//
// Fails closed: a failed listing means we could not rule a dependent out, and
// the reason says exactly that rather than claiming a sibling exists — this
// text is the only durable record an operator reconciles against.
func accountHasOtherDependents(ctx context.Context, client *proxmox.Client, cluster db.Cluster, sharedWithAnotherCluster bool) (shared bool, reason string) {
	if sharedWithAnotherCluster {
		return true, cluster.BootstrapUserID + " is still in use by another cluster in Nexara; left intact along with its Administrator grant"
	}

	tokens, err := client.GetAccessUserTokens(ctx, cluster.BootstrapUserID)
	if err != nil {
		return true, auditSafe(fmt.Sprintf(
			"could not list the API tokens on %s (%s), so another credential depending on it could not be ruled out; left intact along with its Administrator grant",
			cluster.BootstrapUserID, err.Error()))
	}
	for _, t := range tokens {
		if t.TokenID != cluster.BootstrapTokenName {
			return true, cluster.BootstrapUserID + " owns API tokens Nexara did not mint for this cluster; left intact along with its Administrator grant"
		}
	}
	return false, ""
}

// revokeOwnToken removes just this cluster's API token.
//
// May fail with 403 when an ACL revoke earlier in the sequence has already
// stripped the token's privileges. PVE lets a token manage its own user's
// tokens in some configurations, so it is worth attempting either way; when it
// does fail, what remains is a credential that can no longer do anything, and
// the audit row names it.
func revokeOwnToken(ctx context.Context, client *proxmox.Client, cluster db.Cluster) revocationStep {
	step := revocationStep{Step: "token", Detail: cluster.BootstrapUserID + "!" + cluster.BootstrapTokenName}
	if cluster.BootstrapTokenName == "" {
		step.Status = "skipped"
		step.Detail = "no token name recorded"
		return step
	}
	if err := client.DeleteAccessUserToken(ctx, cluster.BootstrapUserID, cluster.BootstrapTokenName); err != nil {
		step.Status = "failed"
		step.Detail = auditSafe(step.Detail + ": " + err.Error())
		return step
	}
	step.Status = "revoked"
	return step
}

// wantsCredentialRevocation reports whether the caller opted into removing the
// cluster's Proxmox-side credential. Opt-in only, and never the default.
//
// It takes the VALIDATED string rather than reading the query itself, and the
// parameter stays a string in the declaration rather than becoming a boolean:
// apischema's toBool also accepts "on" and "off", which this has never read as
// an opt-in, and on an endpoint that deletes users and tokens from a live
// hypervisor a schema that accepts a spelling the code then ignores is the
// wrong kind of tidy-up.
func wantsCredentialRevocation(value string) bool {
	switch value {
	case "1", "true", "yes":
		return true
	}
	return false
}

// revocationOutcome runs the revocation and returns a report for the audit row.
//
// It never returns an error: a cluster being deleted is frequently one that is
// already unreachable, and a failed cleanup must not strand the operator with an
// undeletable cluster. What it does guarantee is that the failure is written
// down, naming the objects left behind on the Proxmox side.
func revocationOutcome(ctx context.Context, queries *db.Queries, cluster db.Cluster, encryptionKey string) []revocationStep {
	secret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, encryptionKey)
	if err != nil {
		return []revocationStep{{
			Step:   "revoke",
			Status: "failed",
			Detail: "the stored credential could not be decrypted",
		}}
	}

	// Does another cluster row authenticate as the same PVE account? The query
	// excludes this cluster explicitly, so it is correct whether or not the row
	// has already been deleted. A query error fails CLOSED — "could not rule out
	// a sibling" must never read as "there is none".
	shared := true
	if cluster.BootstrapUserID != "" {
		n, countErr := queries.CountClustersSharingBootstrapUser(ctx, db.CountClustersSharingBootstrapUserParams{
			ExcludeClusterID: cluster.ID,
			BootstrapUserID:  cluster.BootstrapUserID,
		})
		shared = countErr != nil || n > 0
	}

	return revokeBootstrapCredentials(ctx, cluster, secret, shared)
}

// auditBootstrapFailure records an onboarding attempt that did not produce a
// cluster, so a failed run is never silent.
//
// Two reasons this is not optional. First, a bootstrap failure can leave real
// objects on the hypervisor — a user, an Administrator grant — that no cluster
// row will ever account for, and RollbackMint cannot always remove them.
// Second, this endpoint drives password authentication against the operator's
// hypervisor, and repeated failures are exactly what an operator needs to see.
//
// Names and step statuses only: never the password, the OTP or a token secret.
//
// The row carries a NULL cluster because no cluster exists — the attempt is what
// is being recorded. That is sound here because POST /clusters is gated on
// GLOBAL manage:cluster (requirePerm, not requireClusterPerm), so the
// global-only readership of a NULL-cluster row matches exactly who can perform
// the action. auditClusterExempt in audit_cluster_guard_test.go carries the
// matching entry.
func (h *ClusterHandler) auditBootstrapFailure(c fiber.Ctx, apiURL string, req *bootstrapRequest, err error) {
	fields := map[string]any{
		"api_url":    apiURL,
		"username":   req.Username,
		"user_id":    req.UserID,
		"token_name": req.TokenName,
		"reason":     auditSafe(err.Error()),
	}

	// Residue: objects this attempt created and could not take back. These are
	// the fields an operator needs when reconciling the cluster by hand.
	var mintErr *proxmox.MintError
	if errors.As(err, &mintErr) {
		fields["created_user"] = mintErr.CreatedUser
		fields["created_acl"] = mintErr.CreatedACL
		if len(mintErr.Steps) > 0 {
			fields["steps"] = mintErr.Steps
		}
		if mintErr.CreatedUser || mintErr.CreatedACL {
			fields["warning"] = "onboarding failed after creating Proxmox objects that could not be removed; they are still on the cluster"
		}
	}

	details, _ := json.Marshal(fields)
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "cluster", "", "cluster_bootstrap_failed", details)
}
