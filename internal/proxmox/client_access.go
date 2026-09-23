package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Identifier patterns, taken from the PVE API schema's declared formats so the
// client rejects exactly what pveproxy would reject, and no more.
//
//	pve-userid   name@realm, maxLength 64 (PVE::AccessControl::verify_username)
//	token-subid  ^[A-Za-z][A-Za-z0-9.\-_]+$   (declared inline on the token endpoints)
//	pve-groupid  / pve-roleid   [A-Za-z0-9.\-_]+
//	pve-realm    ^[A-Za-z][A-Za-z0-9.\-_]+$, maxLength 32
var (
	accessRealmPattern   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]*$`)
	accessTokenIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]+$`)
	accessNamePattern    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// RoleAdministrator is Proxmox's built-in superuser role: every privilege the
// cluster defines, including the seven that PVEAdmin lacks
// (Mapping.Modify, Permissions.Modify, Realm.Allocate, Sys.AccessNetwork,
// Sys.Incoming, Sys.Modify, Sys.PowerMgmt). Nexara needs the full set — node
// power actions alone require Sys.PowerMgmt — so this is what onboarding
// grants and what cluster deletion looks for when revoking.
const RoleAdministrator = "Administrator"

// validateUserID guards a caller-supplied PVE user id ("name@realm") that
// becomes one segment of a request path.
//
// Every /access/* identifier gets a validator rather than relying on
// url.PathEscape, for the reason spelled out on validatePathSegment: escaping
// leaves ".." intact, and while pveproxy takes it literally, a normalising
// proxy in front of it resolves it upward onto a different endpoint with
// different permissions. The outbound call carries the cluster's API token, so
// "Proxmox will reject it" is not a defence — the request must never be built
// at all.
func validateUserID(userid string) error {
	if userid == "" {
		return fmt.Errorf("%w: user id is required", ErrInvalidInput)
	}
	if len(userid) > 64 {
		return fmt.Errorf("%w: user id is too long (max 64)", ErrInvalidInput)
	}
	if hasControlChar(userid) {
		return fmt.Errorf("%w: user id must not contain control characters", ErrInvalidInput)
	}
	name, realm, ok := strings.Cut(userid, "@")
	if !ok {
		return fmt.Errorf("%w: user id %q must be in name@realm form", ErrInvalidInput, userid)
	}
	// Cut splits on the FIRST "@", so a second one lands in realm and is
	// caught by accessRealmPattern below.
	if name == "" {
		return fmt.Errorf("%w: user id %q has an empty name", ErrInvalidInput, userid)
	}
	if strings.ContainsAny(name, `/\%`) || name == "." || name == ".." {
		return fmt.Errorf("%w: user id %q has an invalid name", ErrInvalidInput, userid)
	}
	if !accessRealmPattern.MatchString(realm) {
		return fmt.Errorf("%w: user id %q has an invalid realm", ErrInvalidInput, userid)
	}
	return nil
}

// validateTokenID guards a caller-supplied API token name — the part after the
// "!" in "user@realm!tokenname". It is a bare name here, not the full token id.
func validateTokenID(tokenid string) error {
	if tokenid == "" {
		return fmt.Errorf("%w: token name is required", ErrInvalidInput)
	}
	if len(tokenid) > 64 {
		return fmt.Errorf("%w: token name is too long (max 64)", ErrInvalidInput)
	}
	if !accessTokenIDPattern.MatchString(tokenid) {
		return fmt.Errorf("%w: token name %q must start with a letter and contain only letters, digits, dot, dash or underscore", ErrInvalidInput, tokenid)
	}
	return nil
}

// validateFullTokenID guards a complete token id ("user@realm!tokenname").
//
// Used on the one that comes BACK from Proxmox rather than the ones going out:
// the mint response echoes a full-tokenid that becomes an Authorization header,
// a stored column and an audit value, and no other validator sees it.
func validateFullTokenID(full string) error {
	userid, name, ok := strings.Cut(full, "!")
	if !ok {
		return fmt.Errorf("%w: token id %q must be in user@realm!name form", ErrInvalidInput, full)
	}
	if err := validateUserID(userid); err != nil {
		return err
	}
	return validateTokenID(name)
}

// validateGroupID guards a caller-supplied PVE group id.
func validateGroupID(groupid string) error {
	return validateAccessName("group id", groupid, 64)
}

// validateRoleID guards a caller-supplied PVE role id.
func validateRoleID(roleid string) error {
	return validateAccessName("role id", roleid, 64)
}

// validateRealm guards a caller-supplied authentication realm id.
func validateRealm(realm string) error {
	if realm == "" {
		return fmt.Errorf("%w: realm is required", ErrInvalidInput)
	}
	if len(realm) > 32 {
		return fmt.Errorf("%w: realm is too long (max 32)", ErrInvalidInput)
	}
	if !accessRealmPattern.MatchString(realm) {
		return fmt.Errorf("%w: realm %q must start with a letter and contain only letters, digits, dot, dash or underscore", ErrInvalidInput, realm)
	}
	return nil
}

func validateAccessName(kind, value string, maxLen int) error {
	if value == "" {
		return fmt.Errorf("%w: %s is required", ErrInvalidInput, kind)
	}
	if len(value) > maxLen {
		return fmt.Errorf("%w: %s is too long (max %d)", ErrInvalidInput, kind, maxLen)
	}
	// "." and ".." pass accessNamePattern because dot is a legal character in a
	// group or role id — PVE's own pve-groupid and pve-roleid formats admit
	// both as whole names, and pveproxy, which takes a dot segment literally
	// (see validatePathSegment), would read them as a group or role of that
	// name. They are rejected outright anyway, escaped or not: a normalising
	// proxy in front of pveproxy resolves "/access/groups/." onto
	// "/access/groups", the collection endpoint rather than the member one, and
	// "/access/groups/.." one level further up, onto "/access". The cost is a
	// group or role literally named "." or "..", which Nexara cannot address.
	if value == "." || value == ".." {
		return fmt.Errorf("%w: %q is not a valid %s", ErrInvalidInput, value, kind)
	}
	if !accessNamePattern.MatchString(value) {
		return fmt.Errorf("%w: %s %q must contain only letters, digits, dot, dash or underscore", ErrInvalidInput, kind, value)
	}
	return nil
}

// validateACLPath guards the ACL path. It travels in the request BODY rather
// than the URL, so traversal is not the concern here — a malformed path is.
// PVE ACL paths are absolute and slash-separated ("/", "/vms/100", "/storage/local").
func validateACLPath(path string) error {
	if path == "" {
		return fmt.Errorf("%w: ACL path is required", ErrInvalidInput)
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("%w: ACL path %q must start with /", ErrInvalidInput, path)
	}
	if len(path) > 256 {
		return fmt.Errorf("%w: ACL path is too long (max 256)", ErrInvalidInput)
	}
	if hasControlChar(path) {
		return fmt.Errorf("%w: ACL path must not contain control characters", ErrInvalidInput)
	}
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%w: ACL path %q must not contain relative segments", ErrInvalidInput, path)
		}
	}
	return nil
}

// sortAccessList orders a collection by a stable key.
//
// Not cosmetic. Proxmox builds several of these responses by iterating a Perl
// hash — GET /access/users/{userid}/token is literally `keys %$tokens` with no
// sort, where the sibling endpoints do use `sort keys` — and Perl randomises
// hash order per process. The list therefore comes back in a different order on
// almost every request.
//
// In a table with per-row delete buttons that is a hazard, not an annoyance: a
// refetch landing between render and click moves the rows under the pointer, so
// the operator revokes a different credential than the one they aimed at. That
// happened during verification of this feature.
//
// Sorting here rather than in the UI keeps the ordering guarantee in one place
// and gives every API consumer the same stable list.
func sortAccessList[T any](items []T, keyOf func(T) string) {
	sort.SliceStable(items, func(i, j int) bool { return keyOf(items[i]) < keyOf(items[j]) })
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

// GetAccessUsers lists the cluster's PVE users.
func (c *Client) GetAccessUsers(ctx context.Context) ([]AccessUser, error) {
	var users []AccessUser
	if err := c.do(ctx, "/access/users", &users); err != nil {
		return nil, fmt.Errorf("get access users: %w", err)
	}
	sortAccessList(users, func(u AccessUser) string { return u.UserID })
	return users, nil
}

// GetAccessUser returns a single PVE user.
//
// The result type differs from the one GetAccessUsers returns: this endpoint
// sends groups as an array where the index sends a comma-separated string. See
// AccessUserDetail.
func (c *Client) GetAccessUser(ctx context.Context, userid string) (*AccessUserDetail, error) {
	if err := validateUserID(userid); err != nil {
		return nil, err
	}
	var user AccessUserDetail
	if err := c.do(ctx, "/access/users/"+url.PathEscape(userid), &user); err != nil {
		return nil, fmt.Errorf("get access user %s: %w", userid, err)
	}
	// The detail endpoint omits userid from its body; restore it so callers
	// can identify the user they just fetched.
	user.UserID = userid
	return &user, nil
}

// CreateAccessUser creates a PVE user.
func (c *Client) CreateAccessUser(ctx context.Context, params CreateAccessUserParams) error {
	if err := validateUserID(params.UserID); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("userid", params.UserID)
	setAccessUserFields(form, params.AccessUserFields)
	if params.Password != "" {
		form.Set("password", params.Password)
	}
	if err := c.doPost(ctx, "/access/users", form, nil); err != nil {
		return fmt.Errorf("create access user %s: %w", params.UserID, err)
	}
	return nil
}

// UpdateAccessUser updates a PVE user.
func (c *Client) UpdateAccessUser(ctx context.Context, userid string, params UpdateAccessUserParams) error {
	if err := validateUserID(userid); err != nil {
		return err
	}
	form := url.Values{}
	setAccessUserFields(form, params.AccessUserFields)
	if params.Append {
		form.Set("append", "1")
	}
	if err := c.doPut(ctx, "/access/users/"+url.PathEscape(userid), form, nil); err != nil {
		return fmt.Errorf("update access user %s: %w", userid, err)
	}
	return nil
}

// DeleteAccessUser removes a PVE user and, with it, every token it owns.
func (c *Client) DeleteAccessUser(ctx context.Context, userid string) error {
	if err := validateUserID(userid); err != nil {
		return err
	}
	if err := c.doDelete(ctx, "/access/users/"+url.PathEscape(userid), nil); err != nil {
		return fmt.Errorf("delete access user %s: %w", userid, err)
	}
	return nil
}

// setAccessUserFields writes the fields shared by user create and update.
// Pointer fields are tri-state: nil omits the key entirely (leave unchanged),
// a set pointer writes it — including an empty string, which clears it.
func setAccessUserFields(form url.Values, f AccessUserFields) {
	if f.Comment != nil {
		form.Set("comment", *f.Comment)
	}
	if f.Email != nil {
		form.Set("email", *f.Email)
	}
	if f.FirstName != nil {
		form.Set("firstname", *f.FirstName)
	}
	if f.LastName != nil {
		form.Set("lastname", *f.LastName)
	}
	if f.Groups != nil {
		form.Set("groups", *f.Groups)
	}
	if f.Keys != nil {
		form.Set("keys", *f.Keys)
	}
	if f.Enable != nil {
		form.Set("enable", boolToPVE(*f.Enable))
	}
	if f.Expire != nil {
		form.Set("expire", strconv.FormatInt(*f.Expire, 10))
	}
}

// boolToPVE renders a Go bool the way Proxmox expects it in a form body.
func boolToPVE(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// ---------------------------------------------------------------------------
// API tokens
// ---------------------------------------------------------------------------

// GetAccessUserTokens lists the API tokens owned by a user.
func (c *Client) GetAccessUserTokens(ctx context.Context, userid string) ([]AccessToken, error) {
	if err := validateUserID(userid); err != nil {
		return nil, err
	}
	var tokens []AccessToken
	path := "/access/users/" + url.PathEscape(userid) + "/token"
	if err := c.do(ctx, path, &tokens); err != nil {
		return nil, fmt.Errorf("get tokens for %s: %w", userid, err)
	}
	for i := range tokens {
		tokens[i].UserID = userid
	}
	sortAccessList(tokens, func(t AccessToken) string { return t.TokenID })
	return tokens, nil
}

// GetAccessUserToken returns one API token's metadata. The secret is not
// included — Proxmox returns it only once, at creation.
func (c *Client) GetAccessUserToken(ctx context.Context, userid, tokenid string) (*AccessToken, error) {
	if err := validateUserID(userid); err != nil {
		return nil, err
	}
	if err := validateTokenID(tokenid); err != nil {
		return nil, err
	}
	var token AccessToken
	path := "/access/users/" + url.PathEscape(userid) + "/token/" + url.PathEscape(tokenid)
	if err := c.do(ctx, path, &token); err != nil {
		return nil, fmt.Errorf("get token %s!%s: %w", userid, tokenid, err)
	}
	token.UserID = userid
	token.TokenID = tokenid
	return &token, nil
}

// CreateAccessUserToken mints a new API token.
//
// The returned Value is the token secret and Proxmox will never show it again —
// there is no read-back endpoint. Callers must either hand it to the operator
// once or store it encrypted; it must never reach a log or an audit row.
//
// This deliberately returns a struct rather than (string, error): a bare
// (string, error) on *Client is what TestGuard_UPIDMethodListInSync watches for,
// and the guard is right that such a signature reads as a task UPID.
func (c *Client) CreateAccessUserToken(ctx context.Context, userid, tokenid string, params CreateAccessTokenParams) (*AccessTokenCreated, error) {
	if err := validateUserID(userid); err != nil {
		return nil, err
	}
	if err := validateTokenID(tokenid); err != nil {
		return nil, err
	}
	form := url.Values{}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Expire != nil {
		form.Set("expire", strconv.FormatInt(*params.Expire, 10))
	}
	if params.PrivSep != nil {
		form.Set("privsep", boolToPVE(*params.PrivSep))
	}

	var created AccessTokenCreated
	path := "/access/users/" + url.PathEscape(userid) + "/token/" + url.PathEscape(tokenid)
	if err := c.doPost(ctx, path, form, &created); err != nil {
		return nil, fmt.Errorf("create token %s!%s: %w", userid, tokenid, err)
	}
	return &created, nil
}

// UpdateAccessUserToken updates a token's metadata. When params.Regenerate is
// set, the response carries a fresh secret and the previous one stops working
// immediately — same one-shot handling as CreateAccessUserToken.
func (c *Client) UpdateAccessUserToken(ctx context.Context, userid, tokenid string, params UpdateAccessTokenParams) (*AccessTokenCreated, error) {
	if err := validateUserID(userid); err != nil {
		return nil, err
	}
	if err := validateTokenID(tokenid); err != nil {
		return nil, err
	}
	form := url.Values{}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if params.Expire != nil {
		form.Set("expire", strconv.FormatInt(*params.Expire, 10))
	}
	if params.PrivSep != nil {
		form.Set("privsep", boolToPVE(*params.PrivSep))
	}
	if params.Regenerate {
		form.Set("regenerate", "1")
	}

	var updated AccessTokenCreated
	path := "/access/users/" + url.PathEscape(userid) + "/token/" + url.PathEscape(tokenid)
	if err := c.doPut(ctx, path, form, &updated); err != nil {
		return nil, fmt.Errorf("update token %s!%s: %w", userid, tokenid, err)
	}
	return &updated, nil
}

// DeleteAccessUserToken revokes an API token.
func (c *Client) DeleteAccessUserToken(ctx context.Context, userid, tokenid string) error {
	if err := validateUserID(userid); err != nil {
		return err
	}
	if err := validateTokenID(tokenid); err != nil {
		return err
	}
	path := "/access/users/" + url.PathEscape(userid) + "/token/" + url.PathEscape(tokenid)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete token %s!%s: %w", userid, tokenid, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Groups
// ---------------------------------------------------------------------------

// GetAccessGroups lists the cluster's PVE groups.
func (c *Client) GetAccessGroups(ctx context.Context) ([]AccessGroup, error) {
	var groups []AccessGroup
	if err := c.do(ctx, "/access/groups", &groups); err != nil {
		return nil, fmt.Errorf("get access groups: %w", err)
	}
	sortAccessList(groups, func(g AccessGroup) string { return g.GroupID })
	return groups, nil
}

// GetAccessGroup returns one group, including its member list.
func (c *Client) GetAccessGroup(ctx context.Context, groupid string) (*AccessGroupDetail, error) {
	if err := validateGroupID(groupid); err != nil {
		return nil, err
	}
	var group AccessGroupDetail
	if err := c.do(ctx, "/access/groups/"+url.PathEscape(groupid), &group); err != nil {
		return nil, fmt.Errorf("get access group %s: %w", groupid, err)
	}
	group.GroupID = groupid
	return &group, nil
}

// CreateAccessGroup creates a PVE group.
func (c *Client) CreateAccessGroup(ctx context.Context, groupid, comment string) error {
	if err := validateGroupID(groupid); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("groupid", groupid)
	if comment != "" {
		form.Set("comment", comment)
	}
	if err := c.doPost(ctx, "/access/groups", form, nil); err != nil {
		return fmt.Errorf("create access group %s: %w", groupid, err)
	}
	return nil
}

// UpdateAccessGroup updates a group. A nil Comment leaves the existing one
// alone; an empty non-nil Comment clears it.
func (c *Client) UpdateAccessGroup(ctx context.Context, groupid string, params UpdateAccessGroupParams) error {
	if err := validateGroupID(groupid); err != nil {
		return err
	}
	form := url.Values{}
	if params.Comment != nil {
		form.Set("comment", *params.Comment)
	}
	if err := c.doPut(ctx, "/access/groups/"+url.PathEscape(groupid), form, nil); err != nil {
		return fmt.Errorf("update access group %s: %w", groupid, err)
	}
	return nil
}

// DeleteAccessGroup removes a PVE group.
func (c *Client) DeleteAccessGroup(ctx context.Context, groupid string) error {
	if err := validateGroupID(groupid); err != nil {
		return err
	}
	if err := c.doDelete(ctx, "/access/groups/"+url.PathEscape(groupid), nil); err != nil {
		return fmt.Errorf("delete access group %s: %w", groupid, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Roles
// ---------------------------------------------------------------------------

// GetAccessRoles lists the cluster's roles and their privileges.
//
// The built-in "Administrator" role is defined by Proxmox as the complete set
// of valid privileges, so its Privs field doubles as an authoritative, version-
// current privilege catalogue for a role editor — no hardcoded list needed.
func (c *Client) GetAccessRoles(ctx context.Context) ([]AccessRole, error) {
	var roles []AccessRole
	if err := c.do(ctx, "/access/roles", &roles); err != nil {
		return nil, fmt.Errorf("get access roles: %w", err)
	}
	sortAccessList(roles, func(r AccessRole) string { return r.RoleID })
	return roles, nil
}

// GetAccessRole returns one role's privilege map.
func (c *Client) GetAccessRole(ctx context.Context, roleid string) (map[string]FlexBool, error) {
	if err := validateRoleID(roleid); err != nil {
		return nil, err
	}
	privs := map[string]FlexBool{}
	if err := c.do(ctx, "/access/roles/"+url.PathEscape(roleid), &privs); err != nil {
		return nil, fmt.Errorf("get access role %s: %w", roleid, err)
	}
	return privs, nil
}

// CreateAccessRole creates a custom role. privs is a comma-separated
// privilege list, e.g. "VM.Audit,VM.PowerMgmt".
func (c *Client) CreateAccessRole(ctx context.Context, roleid, privs string) error {
	if err := validateRoleID(roleid); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("roleid", roleid)
	if privs != "" {
		form.Set("privs", privs)
	}
	if err := c.doPost(ctx, "/access/roles", form, nil); err != nil {
		return fmt.Errorf("create access role %s: %w", roleid, err)
	}
	return nil
}

// UpdateAccessRole replaces a role's privilege list, or appends to it when
// params.Append is set.
//
// A nil Privs is rejected rather than sent as empty: an empty privs value
// strips every privilege from the role, so treating "field omitted" as "remove
// everything" would let a malformed request silently disarm a role that guards
// real resources. An explicit "" still clears it.
func (c *Client) UpdateAccessRole(ctx context.Context, roleid string, params UpdateAccessRoleParams) error {
	if err := validateRoleID(roleid); err != nil {
		return err
	}
	if params.Privs == nil {
		return fmt.Errorf("%w: privs is required (send an empty string to clear the role's privileges)", ErrInvalidInput)
	}
	form := url.Values{}
	form.Set("privs", *params.Privs)
	if params.Append {
		form.Set("append", "1")
	}
	if err := c.doPut(ctx, "/access/roles/"+url.PathEscape(roleid), form, nil); err != nil {
		return fmt.Errorf("update access role %s: %w", roleid, err)
	}
	return nil
}

// DeleteAccessRole removes a custom role.
func (c *Client) DeleteAccessRole(ctx context.Context, roleid string) error {
	if err := validateRoleID(roleid); err != nil {
		return err
	}
	if err := c.doDelete(ctx, "/access/roles/"+url.PathEscape(roleid), nil); err != nil {
		return fmt.Errorf("delete access role %s: %w", roleid, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// ACL
// ---------------------------------------------------------------------------

// GetAccessACL lists the cluster's access control entries.
func (c *Client) GetAccessACL(ctx context.Context) ([]AccessACLEntry, error) {
	var entries []AccessACLEntry
	if err := c.do(ctx, "/access/acl", &entries); err != nil {
		return nil, fmt.Errorf("get access acl: %w", err)
	}
	sortAccessList(entries, func(e AccessACLEntry) string {
		return e.Path + "\x00" + e.Type + "\x00" + e.UGID + "\x00" + e.RoleID
	})
	return entries, nil
}

// UpdateAccessACL grants or revokes roles on a path. Proxmox uses one endpoint
// for both: params.Delete flips it from grant to revoke.
func (c *Client) UpdateAccessACL(ctx context.Context, params UpdateAccessACLParams) error {
	if err := validateACLPath(params.Path); err != nil {
		return err
	}
	if params.Roles == "" {
		return fmt.Errorf("%w: at least one role is required", ErrInvalidInput)
	}
	if params.Users == "" && params.Groups == "" && params.Tokens == "" {
		return fmt.Errorf("%w: an ACL update needs at least one user, group or token", ErrInvalidInput)
	}

	form := url.Values{}
	form.Set("path", params.Path)
	form.Set("roles", params.Roles)
	if params.Users != "" {
		form.Set("users", params.Users)
	}
	if params.Groups != "" {
		form.Set("groups", params.Groups)
	}
	if params.Tokens != "" {
		form.Set("tokens", params.Tokens)
	}
	if params.Propagate != nil {
		form.Set("propagate", boolToPVE(*params.Propagate))
	}
	if params.Delete {
		form.Set("delete", "1")
	}

	if err := c.doPut(ctx, "/access/acl", form, nil); err != nil {
		verb := "grant"
		if params.Delete {
			verb = "revoke"
		}
		return fmt.Errorf("%s acl on %s: %w", verb, params.Path, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Realms (read-only) and effective permissions
// ---------------------------------------------------------------------------

// GetAccessDomains lists the cluster's authentication realms.
//
// Read-only by design: realm create/update/delete requires the Realm.Allocate
// privilege, which sits in Proxmox's root privilege tier and is carried by no
// built-in role except Administrator.
func (c *Client) GetAccessDomains(ctx context.Context) ([]AccessDomain, error) {
	var domains []AccessDomain
	if err := c.do(ctx, "/access/domains", &domains); err != nil {
		return nil, fmt.Errorf("get access domains: %w", err)
	}
	sortAccessList(domains, func(d AccessDomain) string { return d.Realm })
	return domains, nil
}

// GetAccessDomain returns one realm's configuration.
//
// Callers must blank any credential-bearing field before this reaches an API
// response: LDAP/AD realms can carry a bind password in their config.
func (c *Client) GetAccessDomain(ctx context.Context, realm string) (*AccessDomain, error) {
	if err := validateRealm(realm); err != nil {
		return nil, err
	}
	var domain AccessDomain
	if err := c.do(ctx, "/access/domains/"+url.PathEscape(realm), &domain); err != nil {
		return nil, fmt.Errorf("get access domain %s: %w", realm, err)
	}
	domain.Realm = realm
	return &domain, nil
}

// GetEffectivePermissions returns the privilege map for the credential this
// client authenticates with: path -> privilege -> propagate.
//
// The underlying endpoint is declared `user => 'all'` in Proxmox, so it answers
// for any authenticated caller regardless of what that caller can otherwise do.
// That makes it a reliable capability probe: the UI can disable what Nexara's
// token cannot perform instead of surfacing a bare 403 after a form submit.
func (c *Client) GetEffectivePermissions(ctx context.Context) (AccessPermissions, error) {
	perms := AccessPermissions{}
	if err := c.do(ctx, "/access/permissions", &perms); err != nil {
		return nil, fmt.Errorf("get effective permissions: %w", err)
	}
	return perms, nil
}
