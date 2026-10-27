package handlers

import (
	"context"
)

// AccessHistory is the Access Control tab's kind: the cluster's Proxmox users,
// API tokens, groups, roles and permissions, told apart by type.
func AccessHistory() HistoryKind {
	return HistoryKind{
		Name:       "access",
		Categories: []string{"users", "tokens", "groups", "roles", "permissions", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("users", "pve_user"),
			typeRule("tokens", "pve_token"),
			typeRule("groups", "pve_group"),
			typeRule("roles", "pve_role"),
			typeRule("permissions", "pve_acl"),
		},
		Select:        SelectTypes("pve_user", "pve_token", "pve_group", "pve_role", "pve_acl"),
		ObjectTypes:   []string{"pve_user", "pve_token", "pve_group", "pve_role", "pve_acl"},
		ObservedTypes: observedOf("pve_user", "pve_token", "pve_group", "pve_role", "pve_acl"),
	}
}

// AccessUserHistory is one Proxmox user's kind: its own rows, its API tokens'
// (a token row names its user), and the permissions granted to it directly or
// revoked from it — an ACL row lists its users, groups and roles, so the
// user's are found by the list. A grant through a group or a token is the
// group's or the token's.
func AccessUserHistory() HistoryKind {
	return HistoryKind{
		Name:       "access-user",
		Categories: []string{"lifecycle", "config", "tokens", "permissions", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("tokens", "pve_token"),
			typeRule("permissions", "pve_acl"),
			ruleCreatedDeleted, ruleUpdated, ruleObserved,
		},
		Select: func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
			userid, err := accessParam(r.Params.String("userid"), "userid")
			if err != nil {
				return HistorySelection{}, err
			}
			return HistorySelection{Objects: []HistoryObject{
				{ResourceType: "pve_user", Value: userid},
				{ResourceType: "pve_token", DetailKey: "userid", Value: userid},
				{ResourceType: "pve_acl", DetailKey: "users", Value: userid, InList: true},
			}}, nil
		},
		ObjectTypes:   []string{"pve_user", "pve_token", "pve_acl"},
		ObservedTypes: observedOf("pve_user"),
	}
}

// AccessGroupHistory is one Proxmox group's kind: its own rows and the
// permissions granted to it or revoked from it.
func AccessGroupHistory() HistoryKind {
	return accessSubjectHistory("access-group", "groupid", "pve_group", "groups")
}

// AccessRoleHistory is one Proxmox role's kind: its own rows and the
// permissions that grant it or revoke it.
func AccessRoleHistory() HistoryKind {
	return accessSubjectHistory("access-role", "roleid", "pve_role", "roles")
}

// accessSubjectHistory is the kind of a group or a role: the object, and the
// ACL rows whose list under aclKey names it.
func accessSubjectHistory(name, param, resourceType, aclKey string) HistoryKind {
	return HistoryKind{
		Name:       name,
		Categories: []string{"lifecycle", "config", "permissions", "other"},
		Default:    "other",
		Rules: []HistoryRule{
			typeRule("permissions", "pve_acl"),
			ruleCreatedDeleted, ruleUpdated, ruleObserved,
		},
		Select: func(_ context.Context, r HistoryRequest) (HistorySelection, error) {
			id, err := accessParam(r.Params.String(param), param)
			if err != nil {
				return HistorySelection{}, err
			}
			return HistorySelection{Objects: []HistoryObject{
				{ResourceType: resourceType, Value: id},
				{ResourceType: "pve_acl", DetailKey: aclKey, Value: id, InList: true},
			}}, nil
		},
		ObjectTypes:   []string{resourceType, "pve_acl"},
		ObservedTypes: observedOf(resourceType),
	}
}
