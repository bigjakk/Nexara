package handlers

import (
	"errors"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// The rule of access_dependency.go, as pve-access-control resolves a token's
// permissions: which ACL, group and role changes touch Nexara's own token. Each
// case that must not warn is as much the rule as one that must.

var (
	ownNexara = ownCredential{tokenID: "nexara@pve!api", user: "nexara@pve"}
	ownRoot   = ownCredential{tokenID: "root@pam!api", user: "root@pam"}
)

func TestACLChangeDependency(t *testing.T) {
	t.Parallel()
	groups := []string{"ops"}
	for _, tc := range []struct {
		name       string
		own        ownCredential
		req        proxmox.UpdateAccessACLParams
		wantGroups bool
		want       string // a fragment of the reason; "" is no reason
	}{
		{"the token itself", ownNexara, proxmox.UpdateAccessACLParams{Tokens: "alice@pve!x,NEXARA@pve!api"}, false, "that token"},
		{"another token of the same user", ownNexara, proxmox.UpdateAccessACLParams{Tokens: "nexara@pve!other"}, false, ""},
		{"the token's user, in a list split as Proxmox splits one", ownNexara, proxmox.UpdateAccessACLParams{Users: "alice@pve; nexara@pve"}, false, "user, nexara@pve"},
		{"another user", ownNexara, proxmox.UpdateAccessACLParams{Users: "alice@pve"}, false, ""},
		{"a group the user is in", ownNexara, proxmox.UpdateAccessACLParams{Groups: "admins ops"}, true, "group ops"},
		{"a group the user is not in", ownNexara, proxmox.UpdateAccessACLParams{Groups: "admins"}, true, ""},
		{"root@pam: its user's entries decide nothing", ownRoot, proxmox.UpdateAccessACLParams{Users: "root@pam", Groups: "ops"}, false, ""},
		{"root@pam: its token's do", ownRoot, proxmox.UpdateAccessACLParams{Tokens: "root@pam!api"}, false, "that token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := aclChangeNeedsGroups(tc.own, tc.req); got != tc.wantGroups {
				t.Errorf("aclChangeNeedsGroups = %v, want %v", got, tc.wantGroups)
			}
			got := aclChangeDependency(tc.own, groups, tc.req)
			if (got == "") != (tc.want == "") || !strings.Contains(got, tc.want) {
				t.Errorf("reason = %q, want one carrying %q", got, tc.want)
			}
		})
	}
}

func TestGroupDeleteDependency(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		own     ownCredential
		members []string
		want    bool
	}{
		{"the user is a member", ownNexara, []string{"alice@pve", "Nexara@pve"}, true},
		{"the user is not", ownNexara, []string{"alice@pve"}, false},
		{"root@pam is a member, but needs no group", ownRoot, []string{"root@pam"}, false},
	} {
		if got := groupDeleteDependency(tc.own, "ops", tc.members) != ""; got != tc.want {
			t.Errorf("%s: touches = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRoleUpdateCanTakeAway(t *testing.T) {
	t.Parallel()
	current := map[string]proxmox.FlexBool{"VM.Audit": true, "Sys.Audit": true}
	for _, tc := range []struct {
		name   string
		append bool
		privs  string
		want   bool
	}{
		{"a superset adds only", false, "Sys.Audit,VM.Audit,VM.PowerMgmt", false},
		{"the same list, reordered and spaced", false, "VM.Audit Sys.Audit", false},
		{"a list that drops one", false, "VM.Audit", true},
		{"the empty list clears the role", false, "", true},
		{"append can only add", true, "", false},
	} {
		if got := roleUpdateCanTakeAway(tc.append, current, tc.privs); got != tc.want {
			t.Errorf("%s: can take away = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRoleDependency(t *testing.T) {
	t.Parallel()
	entry := func(typ, ugid, role string) proxmox.AccessACLEntry {
		return proxmox.AccessACLEntry{Path: "/", Type: typ, UGID: ugid, RoleID: role}
	}
	groups := []string{"ops"}
	for _, tc := range []struct {
		name        string
		own         ownCredential
		acl         []proxmox.AccessACLEntry
		want        string
		wantUnknown bool
	}{
		{"granted to the token", ownNexara, []proxmox.AccessACLEntry{entry("token", "nexara@pve!api", "R")}, "to that token", false},
		{"granted to the user", ownNexara, []proxmox.AccessACLEntry{entry("user", "nexara@pve", "R")}, "the token's user", false},
		{"granted to a group the user is in", ownNexara, []proxmox.AccessACLEntry{entry("group", "ops", "R")}, "group ops", false},
		{"granted only to others, Nexara's own entries hold another role", ownNexara,
			[]proxmox.AccessACLEntry{entry("user", "alice@pve", "R"), entry("group", "admins", "R"), entry("user", "nexara@pve", "Other")}, "", false},
		{"a listing that shows none of Nexara's own entries cannot be trusted", ownNexara,
			[]proxmox.AccessACLEntry{entry("user", "alice@pve", "R")}, "", true},
		{"root@pam needs no entry, so none is no sign of a partial listing", ownRoot, nil, "", false},
		{"root@pam's user entry decides nothing", ownRoot, []proxmox.AccessACLEntry{entry("user", "root@pam", "R")}, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, unknown := roleDependency(tc.own, groups, tc.acl, "R")
			if (got == "") != (tc.want == "") || !strings.Contains(got, tc.want) {
				t.Errorf("reason = %q, want one carrying %q", got, tc.want)
			}
			if unknown != tc.wantUnknown {
				t.Errorf("unknown = %v, want %v", unknown, tc.wantUnknown)
			}
		})
	}
}

// TestCredentialDependencyRefusal: the three outcomes once the cluster row is
// read. Could-not-check is the 409 too, so the operator can go ahead, and is not
// waved through.
func TestCredentialDependencyRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		reason   string
		checkErr error
		wantIn   string // "" is no refusal
	}{
		{"nothing touched", "", nil, ""},
		{"touched", "role R is granted to that token on /", nil, "role R is granted"},
		{"could not check", "", errors.New("403 Permission check failed"), "could not check"},
	} {
		err := credentialDependencyRefusal(ownNexara, tc.reason, tc.checkErr)
		if tc.wantIn == "" {
			if err != nil {
				t.Errorf("%s: refused with %v, want nil", tc.name, err)
			}
			continue
		}
		var fe *fiber.Error
		if !errors.As(err, &fe) || fe.Code != fiber.StatusConflict {
			t.Fatalf("%s: err = %v, want a 409", tc.name, err)
		}
		for _, f := range []string{tc.wantIn, "nexara@pve!api", "force=true", "undone in Proxmox"} {
			if !strings.Contains(fe.Message, f) {
				t.Errorf("%s: %q does not carry %q", tc.name, fe.Message, f)
			}
		}
		if strings.Contains(fe.Message, "403") {
			t.Errorf("%s: %q carries the read's own error", tc.name, fe.Message)
		}
	}
}
