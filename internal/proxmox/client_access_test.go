package proxmox

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestValidateUserID(t *testing.T) {
	tests := []struct {
		name    string
		userid  string
		wantErr bool
	}{
		{"plain pam user", "root@pam", false},
		{"pve realm user", "nexara@pve", false},
		{"dots and dashes", "first.last-1@pve", false},
		{"underscore realm", "svc@my_realm", false},

		{"empty", "", true},
		{"no realm", "root", true},
		{"empty name", "@pam", true},
		{"empty realm", "root@", true},
		{"double at", "root@pam@pve", true},
		{"realm starts with digit", "root@1pam", true},
		{"bare traversal", "../../../../access/users/root@pam", true},
		{"traversal in name", "..@pam", true},
		{"dot name", ".@pam", true},
		{"slash in name", "ro/ot@pam", true},
		{"backslash in name", `ro\ot@pam`, true},
		{"percent in name", "ro%2fot@pam", true},
		{"slash in realm", "root@pa/m", true},
		{"newline", "root\n@pam", true},
		{"null byte", "root\x00@pam", true},
		// A single rune above 0x7f, so a rune-wise control check catches it
		// where a byte-wise one would not.
		{"C1 control", "root\u0085@pam", true},
		{"too long", strings.Repeat("a", 61) + "@pam", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateUserID(tc.userid)
			if tc.wantErr && err == nil {
				t.Fatalf("validateUserID(%q) = nil, want error", tc.userid)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateUserID(%q) = %v, want nil", tc.userid, err)
			}
			if tc.wantErr && !errors.Is(err, ErrInvalidInput) {
				t.Errorf("validateUserID(%q) error = %v, want it to wrap ErrInvalidInput", tc.userid, err)
			}
		})
	}
}

func TestValidateTokenID(t *testing.T) {
	tests := []struct {
		name    string
		tokenid string
		wantErr bool
	}{
		{"simple", "nexara", false},
		{"with dash", "nexara-dr", false},
		{"with dot", "nexara.v2", false},
		{"with underscore", "nexara_2", false},

		{"empty", "", true},
		{"single char", "n", true}, // schema requires at least two
		{"leading digit", "1token", true},
		{"leading dash", "-token", true},
		{"traversal", "..", true},
		{"slash", "a/b", true},
		{"bang", "user@pam!tok", true}, // full token id, not a bare name
		{"space", "my token", true},
		{"newline", "tok\nen", true},
		{"too long", strings.Repeat("a", 65), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTokenID(tc.tokenid)
			if tc.wantErr != (err != nil) {
				t.Fatalf("validateTokenID(%q) = %v, wantErr %v", tc.tokenid, err, tc.wantErr)
			}
		})
	}
}

func TestValidateRealmAndNames(t *testing.T) {
	if err := validateRealm("pam"); err != nil {
		t.Errorf("validateRealm(pam) = %v", err)
	}
	for _, bad := range []string{"", "1pam", "pa/m", "..", strings.Repeat("a", 33)} {
		if err := validateRealm(bad); err == nil {
			t.Errorf("validateRealm(%q) = nil, want error", bad)
		}
	}

	if err := validateGroupID("admins"); err != nil {
		t.Errorf("validateGroupID(admins) = %v", err)
	}
	if err := validateRoleID("PVEAdmin"); err != nil {
		t.Errorf("validateRoleID(PVEAdmin) = %v", err)
	}
	for _, bad := range []string{"", "..", "a/b", "a b", "grp\x00"} {
		if err := validateGroupID(bad); err == nil {
			t.Errorf("validateGroupID(%q) = nil, want error", bad)
		}
		if err := validateRoleID(bad); err == nil {
			t.Errorf("validateRoleID(%q) = nil, want error", bad)
		}
	}
}

func TestValidateACLPath(t *testing.T) {
	for _, good := range []string{"/", "/vms/100", "/storage/local", "/nodes/pve1", "/access/groups"} {
		if err := validateACLPath(good); err != nil {
			t.Errorf("validateACLPath(%q) = %v, want nil", good, err)
		}
	}
	for _, bad := range []string{"", "vms/100", "/vms/../access", "/..", "/vms/\x00", strings.Repeat("/a", 200)} {
		if err := validateACLPath(bad); err == nil {
			t.Errorf("validateACLPath(%q) = nil, want error", bad)
		}
	}
}

// TestAccessMethodsRejectInjectionWithoutIssuingRequest is the important one.
//
// Rejection must happen before the request is built, not after Proxmox answers:
// the outbound call carries the cluster's API token, so "Proxmox will reject it"
// is not a defence. Any request reaching the server is a failure, whatever its
// status code.
func TestAccessMethodsRejectInjectionWithoutIssuingRequest(t *testing.T) {
	const attack = "../../../../access/users/root@pam"

	srv, seen := newCaptureServer(t, `{"data":{}}`)
	c := newTestClient(t, srv.URL)
	ctx := context.Background()

	calls := map[string]func() error{
		"GetAccessUser":       func() error { _, err := c.GetAccessUser(ctx, attack); return err },
		"UpdateAccessUser":    func() error { return c.UpdateAccessUser(ctx, attack, UpdateAccessUserParams{}) },
		"DeleteAccessUser":    func() error { return c.DeleteAccessUser(ctx, attack) },
		"CreateAccessUser":    func() error { return c.CreateAccessUser(ctx, CreateAccessUserParams{UserID: attack}) },
		"GetAccessUserTokens": func() error { _, err := c.GetAccessUserTokens(ctx, attack); return err },
		"GetAccessUserToken":  func() error { _, err := c.GetAccessUserToken(ctx, attack, "tok"); return err },
		"CreateAccessUserToken": func() error {
			_, err := c.CreateAccessUserToken(ctx, attack, "tok", CreateAccessTokenParams{})
			return err
		},
		"UpdateAccessUserToken": func() error {
			_, err := c.UpdateAccessUserToken(ctx, attack, "tok", UpdateAccessTokenParams{})
			return err
		},
		"DeleteAccessUserToken": func() error { return c.DeleteAccessUserToken(ctx, attack, "tok") },
		"tokenID injection":     func() error { return c.DeleteAccessUserToken(ctx, "root@pam", attack) },
		"GetAccessGroup":        func() error { _, err := c.GetAccessGroup(ctx, attack); return err },
		"CreateAccessGroup":     func() error { return c.CreateAccessGroup(ctx, attack, "") },
		"UpdateAccessGroup":     func() error { return c.UpdateAccessGroup(ctx, attack, UpdateAccessGroupParams{}) },
		"DeleteAccessGroup":     func() error { return c.DeleteAccessGroup(ctx, attack) },
		"GetAccessRole":         func() error { _, err := c.GetAccessRole(ctx, attack); return err },
		"CreateAccessRole":      func() error { return c.CreateAccessRole(ctx, attack, "VM.Audit") },
		"UpdateAccessRole": func() error {
			p := "VM.Audit"
			return c.UpdateAccessRole(ctx, attack, UpdateAccessRoleParams{Privs: &p})
		},
		"DeleteAccessRole": func() error { return c.DeleteAccessRole(ctx, attack) },
		"GetAccessDomain":  func() error { _, err := c.GetAccessDomain(ctx, attack); return err },
		"UpdateAccessACL(path)": func() error {
			return c.UpdateAccessACL(ctx, UpdateAccessACLParams{Path: "/vms/../access", Roles: "PVEAdmin", Users: "a@pve"})
		},
		"UpdateAccessACL(relat)": func() error {
			return c.UpdateAccessACL(ctx, UpdateAccessACLParams{Path: attack, Roles: "PVEAdmin", Users: "a@pve"})
		},
	}

	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			before := len(*seen)
			err := call()
			if err == nil {
				t.Fatalf("%s accepted an injection payload", name)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Errorf("%s error = %v, want it to wrap ErrInvalidInput", name, err)
			}
			if after := len(*seen); after != before {
				t.Errorf("%s issued %d request(s) before rejecting: %v", name, after-before, (*seen)[before:])
			}
		})
	}

	if len(*seen) != 0 {
		t.Fatalf("expected zero requests overall, got %d: %v", len(*seen), *seen)
	}
}

func TestUpdateAccessACLRequiresSubject(t *testing.T) {
	srv, seen := newCaptureServer(t, `{"data":{}}`)
	c := newTestClient(t, srv.URL)

	err := c.UpdateAccessACL(context.Background(), UpdateAccessACLParams{
		Path:  "/",
		Roles: "PVEAdmin",
	})
	if err == nil {
		t.Fatal("expected an error when no user, group or token is given")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("error = %v, want ErrInvalidInput", err)
	}
	if len(*seen) != 0 {
		t.Errorf("issued %d request(s) despite invalid input: %v", len(*seen), *seen)
	}

	err = c.UpdateAccessACL(context.Background(), UpdateAccessACLParams{
		Path:  "/",
		Users: "nexara@pve",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("missing roles: error = %v, want ErrInvalidInput", err)
	}
}

func TestGetAccessUsersParsesFlexEncodings(t *testing.T) {
	// enable/expire arrive differently across PVE releases; both shapes must
	// decode rather than failing the whole listing.
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/access/users": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]interface{}{
				{"userid": "root@pam", "enable": 1, "expire": 0, "comment": "super"},
				{"userid": "nexara@pve", "enable": "1", "expire": "1767225600"},
				{"userid": "off@pve", "enable": false},
			})
		},
	})
	c := newTestClient(t, srv.URL)

	users, err := c.GetAccessUsers(context.Background())
	if err != nil {
		t.Fatalf("GetAccessUsers: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("got %d users, want 3", len(users))
	}

	// Looked up by id rather than index: GetAccessUsers sorts, so positional
	// assertions would encode the sort order into an unrelated test.
	byID := map[string]AccessUser{}
	for _, u := range users {
		byID[u.UserID] = u
	}

	if root, ok := byID["root@pam"]; !ok || !bool(root.Enable) || root.Comment != "super" {
		t.Errorf("root@pam = %+v", root)
	}
	nexara, ok := byID["nexara@pve"]
	if !ok {
		t.Fatal("nexara@pve missing")
	}
	// enable arrived as the string "1" and expire as a quoted number.
	if !bool(nexara.Enable) || int64(nexara.Expire) != 1767225600 {
		t.Errorf("nexara@pve = %+v", nexara)
	}
	if off, ok := byID["off@pve"]; !ok || bool(off.Enable) {
		t.Errorf("off@pve Enable = true, want false")
	}
}

func TestCreateAccessUserTokenReturnsSecretOnce(t *testing.T) {
	var gotBody string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/access/users/nexara@pve/token/nexara": func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotBody = r.Form.Encode()
			jsonResponse(w, map[string]interface{}{
				"full-tokenid": "nexara@pve!nexara",
				"value":        "e7f1c0de-dead-beef-cafe-000000000001",
				"info":         map[string]interface{}{"privsep": "0", "comment": "Nexara"},
			})
		},
	})
	c := newTestClient(t, srv.URL)

	privsep := false
	created, err := c.CreateAccessUserToken(context.Background(), "nexara@pve", "nexara", CreateAccessTokenParams{
		Comment: "Nexara",
		PrivSep: &privsep,
	})
	if err != nil {
		t.Fatalf("CreateAccessUserToken: %v", err)
	}
	if created.FullTokenID != "nexara@pve!nexara" {
		t.Errorf("FullTokenID = %q", created.FullTokenID)
	}
	if created.Value != "e7f1c0de-dead-beef-cafe-000000000001" {
		t.Errorf("Value = %q", created.Value)
	}
	if bool(created.Info.PrivSep) {
		t.Errorf("Info.PrivSep = true, want false")
	}
	if !strings.Contains(gotBody, "privsep=0") {
		t.Errorf("request body = %q, want privsep=0", gotBody)
	}
}

func TestUpdateAccessACLSendsDeleteFlag(t *testing.T) {
	var gotForm string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/access/acl": func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotForm = r.Form.Encode()
			jsonResponse(w, map[string]interface{}{})
		},
	})
	c := newTestClient(t, srv.URL)

	propagate := true
	if err := c.UpdateAccessACL(context.Background(), UpdateAccessACLParams{
		Path:      "/vms/100",
		Roles:     "PVEVMUser",
		Users:     "alice@pve",
		Propagate: &propagate,
		Delete:    true,
	}); err != nil {
		t.Fatalf("UpdateAccessACL: %v", err)
	}

	for _, want := range []string{"delete=1", "propagate=1", "roles=PVEVMUser", "users=alice%40pve"} {
		if !strings.Contains(gotForm, want) {
			t.Errorf("form = %q, want it to contain %q", gotForm, want)
		}
	}
}

func TestGetEffectivePermissions(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/access/permissions": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, map[string]map[string]interface{}{
				"/":             {"Sys.Audit": 1, "VM.Allocate": 1},
				"/access":       {"Sys.Modify": 0},
				"/access/realm": {"Realm.AllocateUser": 1},
			})
		},
	})
	c := newTestClient(t, srv.URL)

	perms, err := c.GetEffectivePermissions(context.Background())
	if err != nil {
		t.Fatalf("GetEffectivePermissions: %v", err)
	}
	if !bool(perms["/"]["Sys.Audit"]) {
		t.Error(`expected Sys.Audit on "/"`)
	}
	if bool(perms["/access"]["Sys.Modify"]) {
		t.Error("expected Sys.Modify on /access to be false (propagate 0)")
	}
	if _, ok := perms["/access"]["Realm.Allocate"]; ok {
		t.Error("did not expect Realm.Allocate in the probe result")
	}
}

// TestGetAccessUserDecodesArrayGroups is a regression test.
//
// The /access/users INDEX returns groups as a comma-separated string, but
// GET /access/users/{userid} returns it as a JSON array. Sharing one struct
// across both made the detail call fail to unmarshal for any user that
// belonged to a group — i.e. exactly the users an operator most wants to look
// at.
func TestGetAccessUserDecodesArrayGroups(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/access/users/alice@pve": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, map[string]interface{}{
				"enable":    1,
				"expire":    0,
				"comment":   "ops",
				"groups":    []string{"admins", "oncall"},
				"firstname": "Alice",
			})
		},
	})
	c := newTestClient(t, srv.URL)

	user, err := c.GetAccessUser(context.Background(), "alice@pve")
	if err != nil {
		t.Fatalf("GetAccessUser: %v", err)
	}
	if user.UserID != "alice@pve" {
		t.Errorf("UserID = %q, want it restored from the request", user.UserID)
	}
	if len(user.Groups) != 2 || user.Groups[0] != "admins" || user.Groups[1] != "oncall" {
		t.Errorf("Groups = %v, want [admins oncall]", user.Groups)
	}
	if !bool(user.Enable) {
		t.Error("Enable = false, want true")
	}
}

// TestGetAccessUsersDecodesStringGroups is the index-side half of the split.
func TestGetAccessUsersDecodesStringGroups(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/access/users": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]interface{}{
				{"userid": "alice@pve", "groups": "admins,oncall", "totp-locked": 0, "tfa-locked-until": 1767225600},
			})
		},
	})
	c := newTestClient(t, srv.URL)

	users, err := c.GetAccessUsers(context.Background())
	if err != nil {
		t.Fatalf("GetAccessUsers: %v", err)
	}
	if users[0].Groups != "admins,oncall" {
		t.Errorf("Groups = %q, want the comma-separated form", users[0].Groups)
	}
	if bool(users[0].TOTPLocked) {
		t.Error("TOTPLocked = true, want false")
	}
	// tfa-locked-until is a timestamp, not a flag — typing it as a bool would
	// report "locked" for any account that has ever been locked.
	if int64(users[0].TFALockedUntil) != 1767225600 {
		t.Errorf("TFALockedUntil = %d, want the raw timestamp", int64(users[0].TFALockedUntil))
	}
}

// TestUpdateAccessRoleRejectsNilPrivs pins the destructive-default guard:
// an omitted privs field must not be sent as empty, because empty strips every
// privilege from the role.
func TestUpdateAccessRoleRejectsNilPrivs(t *testing.T) {
	srv, seen := newCaptureServer(t, `{"data":{}}`)
	c := newTestClient(t, srv.URL)

	err := c.UpdateAccessRole(context.Background(), "PVEAuditor", UpdateAccessRoleParams{})
	if err == nil {
		t.Fatal("expected an error for nil privs")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("error = %v, want ErrInvalidInput", err)
	}
	if len(*seen) != 0 {
		t.Errorf("issued %d request(s) with nil privs: %v", len(*seen), *seen)
	}

	// An explicit empty string is a deliberate clear and must go through.
	empty := ""
	if err := c.UpdateAccessRole(context.Background(), "CustomRole", UpdateAccessRoleParams{Privs: &empty}); err != nil {
		t.Fatalf("explicit clear should be allowed: %v", err)
	}
	if len(*seen) != 1 {
		t.Errorf("expected exactly one request for the explicit clear, got %d", len(*seen))
	}
}

// TestUpdateAccessGroupOmitsAbsentComment pins that a nil comment leaves the
// existing one alone rather than clearing it.
func TestUpdateAccessGroupOmitsAbsentComment(t *testing.T) {
	var gotForm string
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/access/groups/admins": func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			gotForm = r.Form.Encode()
			jsonResponse(w, map[string]interface{}{})
		},
	})
	c := newTestClient(t, srv.URL)

	if err := c.UpdateAccessGroup(context.Background(), "admins", UpdateAccessGroupParams{}); err != nil {
		t.Fatalf("UpdateAccessGroup: %v", err)
	}
	if strings.Contains(gotForm, "comment") {
		t.Errorf("form = %q, want no comment key when it was not supplied", gotForm)
	}

	cleared := ""
	if err := c.UpdateAccessGroup(context.Background(), "admins", UpdateAccessGroupParams{Comment: &cleared}); err != nil {
		t.Fatalf("UpdateAccessGroup(clear): %v", err)
	}
	if !strings.Contains(gotForm, "comment=") {
		t.Errorf("form = %q, want an explicit empty comment to be sent", gotForm)
	}
}

// TestAccessListsAreStablySorted is a regression test for a hazard found during
// browser verification.
//
// PVE's token_index returns `keys %$tokens` with no sort — unlike its sibling
// endpoints, which use `sort keys` — and Perl randomises hash order. The token
// table therefore reordered between refetches, and a refetch landing between
// render and click moved the rows: a delete aimed at one token hit another.
//
// The upstream order here is deliberately reversed/shuffled so an unsorted
// implementation cannot pass by accident.
func TestAccessListsAreStablySorted(t *testing.T) {
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/access/users": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]interface{}{
				{"userid": "zoe@pve"}, {"userid": "alice@pve"}, {"userid": "mallory@pve"},
			})
		},
		"/api2/json/access/users/root@pam/token": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]interface{}{
				{"tokenid": "zeta"}, {"tokenid": "alpha"}, {"tokenid": "mid"},
			})
		},
		"/api2/json/access/groups": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]interface{}{
				{"groupid": "ops"}, {"groupid": "admins"},
			})
		},
		"/api2/json/access/roles": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]interface{}{
				{"roleid": "PVEVMUser"}, {"roleid": "Administrator"}, {"roleid": "NoAccess"},
			})
		},
		"/api2/json/access/acl": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]interface{}{
				{"path": "/vms/100", "type": "user", "ugid": "b@pve", "roleid": "PVEAdmin"},
				{"path": "/", "type": "user", "ugid": "a@pve", "roleid": "PVEAdmin"},
				{"path": "/vms/100", "type": "user", "ugid": "a@pve", "roleid": "PVEAdmin"},
			})
		},
		"/api2/json/access/domains": func(w http.ResponseWriter, _ *http.Request) {
			jsonResponse(w, []map[string]interface{}{{"realm": "pve"}, {"realm": "ad"}, {"realm": "pam"}})
		},
	})
	c := newTestClient(t, srv.URL)
	ctx := context.Background()

	users, err := c.GetAccessUsers(ctx)
	if err != nil {
		t.Fatalf("GetAccessUsers: %v", err)
	}
	assertOrder(t, "users", []string{users[0].UserID, users[1].UserID, users[2].UserID},
		[]string{"alice@pve", "mallory@pve", "zoe@pve"})

	tokens, err := c.GetAccessUserTokens(ctx, "root@pam")
	if err != nil {
		t.Fatalf("GetAccessUserTokens: %v", err)
	}
	assertOrder(t, "tokens", []string{tokens[0].TokenID, tokens[1].TokenID, tokens[2].TokenID},
		[]string{"alpha", "mid", "zeta"})

	groups, err := c.GetAccessGroups(ctx)
	if err != nil {
		t.Fatalf("GetAccessGroups: %v", err)
	}
	assertOrder(t, "groups", []string{groups[0].GroupID, groups[1].GroupID},
		[]string{"admins", "ops"})

	roles, err := c.GetAccessRoles(ctx)
	if err != nil {
		t.Fatalf("GetAccessRoles: %v", err)
	}
	assertOrder(t, "roles", []string{roles[0].RoleID, roles[1].RoleID, roles[2].RoleID},
		[]string{"Administrator", "NoAccess", "PVEVMUser"})

	acl, err := c.GetAccessACL(ctx)
	if err != nil {
		t.Fatalf("GetAccessACL: %v", err)
	}
	// Sorted by path first, then subject — so the two /vms/100 entries stay
	// adjacent and in a fixed order relative to each other.
	assertOrder(t, "acl",
		[]string{acl[0].Path + "|" + acl[0].UGID, acl[1].Path + "|" + acl[1].UGID, acl[2].Path + "|" + acl[2].UGID},
		[]string{"/|a@pve", "/vms/100|a@pve", "/vms/100|b@pve"})

	domains, err := c.GetAccessDomains(ctx)
	if err != nil {
		t.Fatalf("GetAccessDomains: %v", err)
	}
	assertOrder(t, "domains", []string{domains[0].Realm, domains[1].Realm, domains[2].Realm},
		[]string{"ad", "pam", "pve"})
}

func assertOrder(t *testing.T, what string, got, want []string) {
	t.Helper()
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s order = %v, want %v", what, got, want)
			return
		}
	}
}
