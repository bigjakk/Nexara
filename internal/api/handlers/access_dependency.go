package handlers

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/pvefield"
)

// This file is the second half of the self-credential guard: the ACL, group and
// role changes that leave Nexara's token in place but can take away what it may
// do. guardSelfCredential covers the token and its user; these cover what the
// token's permissions rest on.
//
// The rule is pve-access-control's, transcribed from src/PVE/RPCEnvironment.pm
// (permissions, compile_acl_path) and src/PVE/AccessControl.pm (roles):
//
//   - root@pam holds every privilege whatever the ACL says, so for a token of
//     root@pam no user or group entry matters, only the token's own entries;
//   - a token without privilege separation holds exactly its user's roles: the
//     ACL entries for that user and for every group the user is in;
//   - a separated token holds the intersection of its own entries' privileges
//     and its user's, so the token's entries, the user's and the user's groups'
//     all matter;
//   - along a path the deepest entry wins, a token entry over a user entry over
//     a group entry, and NoAccess cancels every other role: a GRANT can take
//     permissions away as surely as a revoke, so both directions are covered;
//   - a role's privileges are looked up when the ACL is compiled, so editing or
//     deleting a role changes every entry that grants it. Deleting a group
//     (API2/Group.pm delete_group) also deletes its ACL entries.
//
// Deliberately broader than exact, each of these warns in a case where the token
// would in fact keep its permissions, at the cost of one confirmation:
//
//   - the path is not compared: an entry anywhere in the tree can be the one
//     that decides a path Nexara uses (propagation, pool ACLs on /pool/*);
//   - a token entry warns without reading the token's privsep flag, though it
//     only counts for a separated token;
//   - ids are compared without case, as guardSelfCredential compares them;
//   - a role edit that removes a privilege warns whether or not Nexara needed
//     that privilege.
//
// Exact, and so not warned: a role edit with append=true, or one whose new list
// keeps every privilege the role had (neither can take anything away); a change
// that names none of the subjects above; and user and group entries when the
// token belongs to root@pam.

// rootUser is the account pve-access-control exempts from every ACL check
// (RPCEnvironment.pm permissions: $user eq 'root@pam'). The comparison there is
// exact, so it is here.
const rootUser = "root@pam"

// ownCredential is the token Nexara authenticates with, split into its parts.
type ownCredential struct {
	tokenID string // user@realm!name
	user    string // user@realm
}

// parseOwnCredential splits the cluster row's token id; ok=false for one that is
// not a token id, which leaves nothing for these guards to protect.
func parseOwnCredential(tokenID string) (ownCredential, bool) {
	user, _, ok := splitFullTokenID(tokenID)
	if !ok {
		return ownCredential{}, false
	}
	return ownCredential{tokenID: tokenID, user: user}, true
}

// userEntriesCount reports whether entries for the token's user and the user's
// groups decide anything: they do for every user but root@pam.
func (o ownCredential) userEntriesCount() bool { return o.user != rootUser }

func (o ownCredential) isToken(id string) bool { return strings.EqualFold(id, o.tokenID) }

func (o ownCredential) isUser(id string) bool {
	return o.userEntriesCount() && strings.EqualFold(id, o.user)
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// isUserGroup reports whether group is one of userGroups, the groups the token's
// user is in, and counts.
func (o ownCredential) isUserGroup(userGroups []string, group string) bool {
	return o.userEntriesCount() && containsFold(userGroups, group)
}

// aclChangeNeedsGroups reports whether deciding req needs the groups the token's
// user is in: only when it names groups that could count.
func aclChangeNeedsGroups(o ownCredential, req proxmox.UpdateAccessACLParams) bool {
	return o.userEntriesCount() && len(pvefield.SplitList(req.Groups)) > 0
}

// aclChangeDependency is why an ACL grant or revoke touches what the token's
// permissions rest on, or "" when it cannot. Lists are split as Proxmox splits
// them (split_list). userGroups is needed only when aclChangeNeedsGroups says so.
func aclChangeDependency(o ownCredential, userGroups []string, req proxmox.UpdateAccessACLParams) string {
	for _, t := range pvefield.SplitList(req.Tokens) {
		if o.isToken(t) {
			return "it changes an ACL entry for that token"
		}
	}
	for _, u := range pvefield.SplitList(req.Users) {
		if o.isUser(u) {
			return "it changes an ACL entry for the token's user, " + o.user
		}
	}
	for _, g := range pvefield.SplitList(req.Groups) {
		if o.isUserGroup(userGroups, g) {
			return "it changes an ACL entry for group " + g + ", which " + o.user + " is in"
		}
	}
	return ""
}

// groupDeleteDependency is why deleting groupid touches the token, or "": the
// token's user is a member, so the group's ACL entries stop applying to it.
func groupDeleteDependency(o ownCredential, groupid string, members []string) string {
	if !o.userEntriesCount() || !containsFold(members, o.user) {
		return ""
	}
	return o.user + " is in group " + groupid + ", and deleting it removes the group's ACL entries"
}

// roleUpdateCanTakeAway reports whether a role edit can remove a privilege: a
// replacement (append=false) whose list lacks one the role holds now. Proxmox
// replaces the role's set with the split list, or adds the list to it
// (API2/Role.pm update_role, AccessControl.pm add_role_privs).
func roleUpdateCanTakeAway(appendPrivs bool, current map[string]proxmox.FlexBool, privs string) bool {
	if appendPrivs {
		return false
	}
	next := map[string]bool{}
	for _, p := range pvefield.SplitList(privs) {
		next[p] = true
	}
	for p := range current {
		if !next[p] {
			return true
		}
	}
	return false
}

// roleDependency is why changing roleid touches the token, or "": an ACL entry
// grants it to the token, its user or one of the user's groups.
//
// unknown reports that the listing cannot be trusted to be whole. Proxmox lists
// only the entries on paths where the caller may modify permissions, unless it
// holds Sys.Audit on /access (API2/ACL.pm read_acl). A token that may edit roles
// holds Sys.Modify on /access through some entry of its own, its user's or a
// group's, so a listing that shows none of those is a partial one, and the role
// may be granted where Nexara cannot see. (For root@pam, which needs no entry,
// that inference does not hold, and is not drawn.)
func roleDependency(o ownCredential, userGroups []string, acl []proxmox.AccessACLEntry, roleid string) (reason string, unknown bool) {
	sawOwn := false
	for _, e := range acl {
		var whom string
		switch {
		case e.Type == "token" && o.isToken(e.UGID):
			whom = "that token"
		case e.Type == "user" && o.isUser(e.UGID):
			whom = "the token's user, " + o.user
		case e.Type == "group" && o.isUserGroup(userGroups, e.UGID):
			whom = "group " + e.UGID + ", which " + o.user + " is in"
		default:
			continue
		}
		sawOwn = true
		if e.RoleID == roleid {
			return "role " + roleid + " is granted to " + whom + " on " + e.Path, false
		}
	}
	return "", !sawOwn && o.userEntriesCount()
}

// credentialDependencyTail is what every refusal below ends with: what can follow,
// how to recover, and how to go ahead.
const credentialDependencyTail = " Continuing can take away permissions Nexara relies on, or cut it off, " +
	"until the change is undone in Proxmox. Retry with force=true to proceed anyway."

// credentialDependencyRefusal is the whole decision of guardCredentialDependency
// once the cluster row has been read: nil when the change cannot touch the
// token, and a 409 when it does, or when Nexara could not read what it would
// have needed to tell (checkErr). That last is a 409 and not a 500 on purpose:
// the change may be fine, the operator can say so, and a refusal they could not
// get past would block every such change on a token that may not read the ACL.
func credentialDependencyRefusal(o ownCredential, reason string, checkErr error) error {
	if checkErr != nil {
		return fiber.NewError(fiber.StatusConflict,
			"Nexara could not check whether this change touches the permissions of token "+o.tokenID+
				", which it uses to reach this cluster."+credentialDependencyTail)
	}
	if reason == "" {
		return nil
	}
	return fiber.NewError(fiber.StatusConflict,
		"This change touches the permissions of token "+o.tokenID+", which Nexara uses to reach this "+
			"cluster: "+reason+"."+credentialDependencyTail)
}

// errCredentialUnknown is the checkErr a decide function reports when what it
// read cannot settle the question (roleDependency's unknown).
var errCredentialUnknown = errors.New("the ACL listing may be partial")

// guardCredentialDependency refuses an ACL, group or role change that touches
// what the token Nexara authenticates with depends on, unless forced. decide
// reads what it needs from Proxmox and returns the reason, or "".
//
// force skips everything, as it does for guardSelfCredential. The cluster row is
// read for the token id; a failure there is the refusal guardSelfCredential gives
// one (404 or 500), so that one failure has one answer. A failure of decide's
// Proxmox reads is logged and answered with the could-not-check 409.
func (h *AccessHandler) guardCredentialDependency(c fiber.Ctx, clusterID uuid.UUID, force bool,
	decide func(ctx context.Context, o ownCredential) (string, error),
) error {
	if force {
		return nil
	}
	ownTokenID, lookupErr := h.clusterTokenID(c.Context(), clusterID)
	if lookupErr != nil {
		logGuardLookupFailure(c.Context(), clusterID, lookupErr)
		return selfCredentialRefusal(false, lookupErr, "", "", "")
	}
	o, ok := parseOwnCredential(ownTokenID)
	if !ok {
		return nil
	}
	reason, err := decide(c.Context(), o)
	if err != nil && !errors.Is(err, errCredentialUnknown) {
		slog.Warn("credential guard could not read the cluster's access settings; asking for confirmation",
			"cluster_id", clusterID, "error", err)
	}
	return credentialDependencyRefusal(o, reason, err)
}

// userGroupsOf reads the groups the token's user is in.
func userGroupsOf(ctx context.Context, px *proxmox.Client, o ownCredential) ([]string, error) {
	u, err := px.GetAccessUser(ctx, o.user)
	if err != nil {
		return nil, err
	}
	return u.Groups, nil
}

// roleChangeDecision is decide for a role edit or delete: read the ACL and, when
// the user's entries count, the user's groups, and ask roleDependency.
func roleChangeDecision(px *proxmox.Client, roleid string) func(context.Context, ownCredential) (string, error) {
	return func(ctx context.Context, o ownCredential) (string, error) {
		acl, err := px.GetAccessACL(ctx)
		if err != nil {
			return "", err
		}
		var groups []string
		if o.userEntriesCount() {
			if groups, err = userGroupsOf(ctx, px, o); err != nil {
				return "", err
			}
		}
		reason, unknown := roleDependency(o, groups, acl, roleid)
		if unknown {
			return "", errCredentialUnknown
		}
		return reason, nil
	}
}
