package handlers

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// The 25 access routes are declared endpoints now
// (internal/api/registry_access.go), so what used to be tested here splits in
// two, the same way the Veeam and PBS routes did.
//
// The PERMISSION each route enforces is a middleware Check attached from its
// declaration, and the parameter rules — a required userid, a malformed body, a
// cluster id that is not a UUID — are the schema's. Both are tested against the
// REAL declarations in internal/api/registry_access_test.go; a bare handler
// mount here could not exercise a gate that no longer sits inside the handler,
// and a test that mounted one anyway would pass while proving nothing.
//
// What stays here is what is still this file's own: the percent-decode of a
// path identifier, the self-credential guard's decision, which user edits count
// as capable of severing access, the exact audit details of a user edit, the
// static audit-detail guard, and what the two user reads make of a user's
// two-factor keys.

func TestSplitFullTokenID(t *testing.T) {
	tests := []struct {
		in        string
		user, tok string
		ok        bool
	}{
		{"nexara@pve!api", "nexara@pve", "api", true},
		{"root@pam!nexara", "root@pam", "nexara", true},
		{"root@pam", "", "", false},
		{"!api", "", "", false},
		{"root@pam!", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range tests {
		u, k, ok := splitFullTokenID(tc.in)
		if ok != tc.ok || u != tc.user || k != tc.tok {
			t.Errorf("splitFullTokenID(%q) = (%q,%q,%v), want (%q,%q,%v)", tc.in, u, k, ok, tc.user, tc.tok, tc.ok)
		}
	}
}

// TestSelfCredentialSubject covers the guard that stops Nexara destroying its
// own cluster credential. The empty-tokenid cases matter most: deleting a user
// takes its tokens with it, so that collides on the user alone.
func TestSelfCredentialSubject(t *testing.T) {
	const own = "nexara@pve!api"

	tests := []struct {
		name            string
		ownTokenID      string
		userid, tokenid string
		wantConflict    bool
		wantSubject     string
	}{
		{"exact token match", own, "nexara@pve", "api", true, "token nexara@pve!api"},
		{"case-insensitive", own, "NEXARA@PVE", "API", true, "token nexara@pve!api"},
		{"user delete takes our token", own, "nexara@pve", "", true, "user nexara@pve"},
		{"different token, same user", own, "nexara@pve", "other", false, ""},
		{"different user", own, "alice@pve", "api", false, ""},
		{"different user, whole-user delete", own, "alice@pve", "", false, ""},
		{"cluster token id has no bang", "nexara@pve", "nexara@pve", "api", false, ""},
		{"cluster token id empty", "", "nexara@pve", "api", false, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			subject, conflict := selfCredentialSubject(tc.ownTokenID, tc.userid, tc.tokenid)
			if conflict != tc.wantConflict {
				t.Fatalf("conflict = %v, want %v", conflict, tc.wantConflict)
			}
			if conflict && subject != tc.wantSubject {
				t.Errorf("subject = %q, want %q", subject, tc.wantSubject)
			}
		})
	}
}

// secretishKey matches audit-detail keys that would leak a credential.
var secretishKey = []string{"password", "secret", "token_secret", "value", "ticket", "otp", "csrf", "privatekey", "bind_password"}

// accessAuditBuilders are the functions whose result access.go may hand to
// json.Marshal in place of a map literal.
//
// The guard below cannot read a builder's keys; it scans only the arguments a
// builder is called with. An entry is therefore the decision to trust other tests
// instead, and is acceptable only with all of these: tests that compare what the
// builder returns exactly, so that a key added to it fails one whether or not it
// looks like a credential; one of them driven through the real route, so that a
// handler that stops calling the builder fails too; and a comment on the builder
// naming them.
//
// Today that is accessUserUpdateDetails, pinned by TestAccessUserUpdateDetails,
// TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields and, through the real
// route, TestAccessUpdateUserAuditRow in internal/api.
var accessAuditBuilders = map[string]bool{
	"accessUserUpdateDetails": true,
}

// isAccessAuditBuilderCall reports whether arg is a call to a function listed in
// accessAuditBuilders. Only a bare call counts: a method or a package-qualified
// function of the same name is some other function.
func isAccessAuditBuilderCall(arg ast.Expr) bool {
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return false
	}
	name, ok := call.Fun.(*ast.Ident)
	return ok && accessAuditBuilders[name.Name]
}

// TestGuard_AccessAuditDetailsCarryNoSecrets is a static guard over access.go.
// Every one-argument json.Marshal must be handed a map literal or a call to a
// builder in accessAuditBuilders. A map literal may carry no credential-shaped
// key, and nothing in it, or in a builder call's arguments, may read a
// credential-bearing field.
//
// This matters more here than almost anywhere else in the codebase. Proxmox
// returns a token secret exactly once, this file is where that value lives, and
// audit rows are readable by anyone holding view:audit — which every built-in
// Viewer does. A secret reaching an audit detail would be readable by
// accounts that cannot even see the token list.
//
// Static rather than behavioural because the detail maps are built inline at
// twelve call sites; a runtime test would have to reach Proxmox at each one.
//
// The marshal argument is enforced, not assumed. A map assigned to a local
// first, a struct or slice literal, or a call that is not in accessAuditBuilders
// hides its keys from the checks on a map literal, so it is refused: a guard
// that skipped what it could not read would pass exactly the shape a secret is
// laundered through.
//
// What it cannot see, stated plainly. It inspects one-argument calls to a function
// named Marshal (json.Marshal, here) and nothing else, so json.MarshalIndent, an
// Encoder, or a json.RawMessage built by hand bypass it. Within a call it matches
// string-literal key names exactly against secretishKey (has_password is
// legitimate, so user_password would pass, and so would a constant key) and
// refuses reads of .Value, .Password, .TokenSecret and .Secret; a credential held
// in a local, reached through a call, or kept in a field of another name passes.
// It is aimed at the realistic mistake — someone adding `"secret": created.Value`
// while wiring up a new endpoint — not at deliberate laundering. Keeping the rule
// absolute (never read .Value/.Password here at all) is what makes it worth
// having; the one legitimate derived value, has_password, is computed before the
// literal and commented as such.
func TestGuard_AccessAuditDetailsCarryNoSecrets(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "access.go", nil, 0)
	if err != nil {
		t.Fatalf("parse access.go: %v", err)
	}
	line := func(n ast.Node) int { return fset.Position(n.Pos()).Line }

	// An entry names a function this file declares: one that outlived its
	// function would exempt whatever is later given that name.
	declared := map[string]bool{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			declared[fn.Name.Name] = true
		}
	}
	for name := range accessAuditBuilders {
		if !declared[name] {
			t.Errorf("accessAuditBuilders lists %s, but access.go declares no such function — "+
				"an entry that outlives its function exempts whatever is later given that name", name)
		}
	}

	// scanKeys refuses a credential-shaped name on any string-literal key under
	// lit, the keys of a map nested in a value included.
	scanKeys := func(lit *ast.CompositeLit) {
		ast.Inspect(lit, func(n ast.Node) bool {
			kv, ok := n.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, ok := kv.Key.(*ast.BasicLit)
			if !ok || key.Kind != token.STRING {
				return true
			}
			name, err := strconv.Unquote(key.Value) // a raw string is a string literal too
			if err != nil {
				// Unreachable: ParseFile rejects a malformed string literal before
				// this runs, and a name still wrapped in its quotes could never match
				// secretishKey anyway.
				name = key.Value
			}
			name = strings.ToLower(name)
			for _, bad := range secretishKey {
				if name == bad {
					t.Errorf("%s:%d: audit details carry key %q — token secrets and passwords must never reach an audit row (view:audit is held by every Viewer)",
						"access.go", line(key), name)
				}
			}
			return true
		})
	}

	// scanReads refuses a read of a credential-bearing field anywhere under node,
	// whatever key the value sits under: details{"x": created.Value} is the leak.
	scanReads := func(node ast.Node) {
		ast.Inspect(node, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch strings.ToLower(sel.Sel.Name) {
			case "value", "password", "tokensecret", "secret":
				t.Errorf("%s:%d: audit details read .%s — that is a credential, keep it out of the audit row",
					"access.go", line(sel), sel.Sel.Name)
			}
			return true
		})
	}

	var checked int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || callName(call) != "Marshal" || len(call.Args) != 1 {
			return true
		}
		switch arg := call.Args[0].(type) {
		case *ast.CompositeLit:
			if _, isMap := arg.Type.(*ast.MapType); isMap {
				checked++
				scanKeys(arg)
				scanReads(arg)
				return true
			}
		case *ast.CallExpr:
			if isAccessAuditBuilderCall(arg) {
				for _, a := range arg.Args {
					scanReads(a)
				}
				return true
			}
		}
		t.Errorf("%s:%d: json.Marshal(%s) — an audit detail must be a `map[…]…{…}` literal "+
			"(a named map type such as fiber.Map is refused too), whose keys and values this guard reads, "+
			"or a call to a builder listed in accessAuditBuilders, whose keys exact-match tests pin; "+
			"a struct or slice literal, a local, or any other call hides them from both, and audit rows are readable by every Viewer",
			"access.go", line(call), types.ExprString(call.Args[0]))
		return true
	})

	if checked == 0 {
		t.Fatal("found no json.Marshal(map literal) in access.go — the guard would pass vacuously")
	}
}

// TestGuard_AccessAuditDetailsNeverReadTheRawParams is the other half of the
// guard above, and it exists because the migration to declared parameters
// created a NEW way to leak the same secret.
//
// apischema.Params.Raw() returns every validated parameter, including
// `password` on the user-create route, and handing it to json.Marshal would
// write that password into a row every Viewer can read — which is exactly what
// Raw's own doc comment warns against. No call site does this today; the guard
// is here so none appears.
func TestGuard_AccessAuditDetailsNeverReadTheRawParams(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "access.go", nil, 0)
	if err != nil {
		t.Fatalf("parse access.go: %v", err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || callName(call) != "Raw" {
			return true
		}
		t.Errorf("access.go:%d: calls Params.Raw() — it carries every declared parameter, "+
			"including the create route's password, and this file's audit rows are readable by every Viewer",
			fset.Position(call.Pos()).Line)
		return true
	})
}

// TestAccessParamDecodesPercentEncoding is a regression test for a
// feature-breaking bug.
//
// Fiber v3 does not percent-decode path params, and the registry hands the
// handler what Fiber matched. A PVE user id is "name@realm", so any correct
// client sends encodeURIComponent("nexara@pve") = "nexara%40pve". Using that
// raw value handed the validator a string containing "%" and no "@", which it
// rejected — so every user, token, group, role and realm lookup 400'd for
// clients doing exactly the right thing.
//
// The decode must stay paired with validation happening afterwards: "%2e%2e"
// decodes to ".." and is rejected by the proxmox client's validators, and the
// outbound path is re-escaped. This test pins the decode; the traversal half is
// pinned by TestAccessMethodsRejectInjectionWithoutIssuingRequest.
func TestAccessParamDecodesPercentEncoding(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"nexara%40pve", "nexara@pve"},
		{"nexara@pve", "nexara@pve"},
		{"root%40pam", "root@pam"},
		{"first.last-1%40pve", "first.last-1@pve"},
		// Decodes to a traversal; the proxmox-client validators reject it
		// downstream, which is why decoding first is safe.
		{"%2e%2e", ".."},
	}

	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := accessParam(tc.raw, "userid")
			if err != nil {
				t.Fatalf("accessParam(%q) returned %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("accessParam(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}

	// The malformed-escape branch. The declared patterns admit only a
	// well-formed "%XX", so no request can reach this today — it stays as
	// defence in depth, and naming the parameter is what makes the 400
	// actionable when something else starts calling this.
	if _, err := accessParam("nexara%zz", "userid"); err == nil {
		t.Error("accessParam accepted a malformed percent-escape")
	} else if !strings.Contains(err.Error(), "userid") {
		t.Errorf("the rejection does not name the parameter: %v", err)
	}
}

// TestAccessUpdateAffectsAccess covers which user edits are treated as capable
// of severing Nexara's own cluster access.
//
// The distinction matters in both directions: guarding too little lets a PUT
// disable nexara@pve with no confirmation (PVE validates the owning user when
// verifying a token, so every later call 401s), while guarding too much makes
// routine comment edits demand a force flag and trains operators to pass it
// without reading.
func TestAccessUpdateAffectsAccess(t *testing.T) {
	str := func(s string) *string { return &s }
	b := func(v bool) *bool { return &v }
	i64 := func(v int64) *int64 { return &v }

	tests := []struct {
		name string
		req  proxmox.UpdateAccessUserParams
		want bool
	}{
		{"empty update", proxmox.UpdateAccessUserParams{}, false},
		{"comment only", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Comment: str("hi")},
		}, false},
		{"email only", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Email: str("a@b.c")},
		}, false},
		{"explicitly enabling", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Enable: b(true)},
		}, false},
		{"expire cleared to never", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Expire: i64(0)},
		}, false},

		{"disabling", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Enable: b(false)},
		}, true},
		{"setting an expiry", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Expire: i64(1767225600)},
		}, true},
		{"changing groups", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Groups: str("admins")},
		}, true},
		{"clearing groups", proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Groups: str("")},
		}, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := accessUpdateAffectsAccess(tc.req); got != tc.want {
				t.Errorf("accessUpdateAffectsAccess = %v, want %v", got, tc.want)
			}
		})
	}
}

// accessUpdateDetailsJSON is the bytes a user update's audit details marshal to,
// which is what the audit row stores and a Viewer reads.
func accessUpdateDetailsJSON(t *testing.T, userid string, force bool, req proxmox.UpdateAccessUserParams) string {
	t.Helper()
	out, err := json.Marshal(accessUserUpdateDetails(userid, force, req))
	if err != nil {
		t.Fatalf("marshal the audit details: %v", err)
	}
	return string(out)
}

// TestAccessUserUpdateDetails pins a user update's audit details to the exact
// JSON that reaches the row, one case for each thing the builder decides.
//
// The comparison is on the marshalled bytes because they are what the row stores
// and a Viewer reads. It is exact, so a key the builder gains fails every case
// that sets the field it would come from.
//
// One of the guards for these keys. TestGuard_AccessAuditDetailsCarryNoSecrets
// admits the builder without reading it (accessAuditBuilders), so what it
// returns is pinned here, across the request type in
// TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields, and through the real
// route in TestAccessUpdateUserAuditRow.
func TestAccessUserUpdateDetails(t *testing.T) {
	const self = "nexara@pve"

	// Values of the fields the row must never carry, each one recognisable in
	// the output whatever key it might be recorded under. The group list is here
	// too: the row says a membership was set, not what it became.
	const (
		sentinelComment = "sentinel-comment"
		sentinelEmail   = "sentinel-email@example.com"
		sentinelFirst   = "sentinel-first"
		sentinelLast    = "sentinel-last"
		sentinelKeys    = "sentinel-keys"
		sentinelGroups  = "sentinel-group-list"
	)
	unrecorded := []string{sentinelComment, sentinelEmail, sentinelFirst, sentinelLast, sentinelKeys, sentinelGroups}

	str := func(s string) *string { return &s }
	b := func(v bool) *bool { return &v }
	i64 := func(v int64) *int64 { return &v }
	edit := func(f proxmox.AccessUserFields) proxmox.UpdateAccessUserParams {
		return proxmox.UpdateAccessUserParams{AccessUserFields: f}
	}

	tests := []struct {
		name   string
		userid string
		force  bool
		req    proxmox.UpdateAccessUserParams
		want   string
	}{
		{"nothing set", self, false, edit(proxmox.AccessUserFields{}),
			`{"forced":false,"userid":"nexara@pve"}`},
		{"force on an edit that sets nothing overrides nothing", self, true, edit(proxmox.AccessUserFields{}),
			`{"forced":false,"userid":"nexara@pve"}`},
		{"force on a comment change overrides nothing", self, true, edit(proxmox.AccessUserFields{Comment: str(sentinelComment)}),
			`{"forced":false,"userid":"nexara@pve"}`},

		{"disabling", self, false, edit(proxmox.AccessUserFields{Enable: b(false)}),
			`{"enable":false,"forced":false,"userid":"nexara@pve"}`},
		{"disabling, forced", self, true, edit(proxmox.AccessUserFields{Enable: b(false)}),
			`{"enable":false,"forced":true,"userid":"nexara@pve"}`},
		{"enabling is recorded although the guard ignores it", self, true, edit(proxmox.AccessUserFields{Enable: b(true)}),
			`{"enable":true,"forced":false,"userid":"nexara@pve"}`},

		{"giving an expiry, forced", self, true, edit(proxmox.AccessUserFields{Expire: i64(1767225600)}),
			`{"expire":1767225600,"forced":true,"userid":"nexara@pve"}`},
		{"clearing the expiry is recorded as 0", self, false, edit(proxmox.AccessUserFields{Expire: i64(0)}),
			`{"expire":0,"forced":false,"userid":"nexara@pve"}`},

		{"replacing groups, forced: the list is not recorded", self, true, edit(proxmox.AccessUserFields{Groups: str(sentinelGroups)}),
			`{"forced":true,"groups_changed":true,"userid":"nexara@pve"}`},
		{"removing every group", self, false, edit(proxmox.AccessUserFields{Groups: str("")}),
			`{"forced":false,"groups_changed":true,"userid":"nexara@pve"}`},
		{"appending groups records what replacing them does", self, true, proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{Groups: str(sentinelGroups)}, Append: true},
			`{"forced":true,"groups_changed":true,"userid":"nexara@pve"}`},
		{"append without groups sets no groups", self, true, proxmox.UpdateAccessUserParams{Append: true},
			`{"forced":false,"userid":"nexara@pve"}`},

		{"the account named is the one edited", "alice@pve", false, edit(proxmox.AccessUserFields{Enable: b(false)}),
			`{"enable":false,"forced":false,"userid":"alice@pve"}`},

		{"every field at once carries only the three", self, true, proxmox.UpdateAccessUserParams{
			AccessUserFields: proxmox.AccessUserFields{
				Comment:   str(sentinelComment),
				Email:     str(sentinelEmail),
				FirstName: str(sentinelFirst),
				LastName:  str(sentinelLast),
				Keys:      str(sentinelKeys),
				Groups:    str(sentinelGroups),
				Enable:    b(false),
				Expire:    i64(1767225600),
			},
			Append: true,
		}, `{"enable":false,"expire":1767225600,"forced":true,"groups_changed":true,"userid":"nexara@pve"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := accessUpdateDetailsJSON(t, tc.userid, tc.force, tc.req)
			if got != tc.want {
				t.Errorf("details = %s, want %s", got, tc.want)
			}
			for _, secret := range unrecorded {
				if strings.Contains(got, secret) {
					t.Errorf("details %s carry %q — audit rows are readable by every Viewer", got, secret)
				}
			}
		})
	}
}

// TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields is the allow-list
// checked against the request type rather than against a list of the fields that
// exist today.
//
// It walks every field of proxmox.UpdateAccessUserParams, the embedded
// AccessUserFields flattened and Append beside it, and sets each one that is not
// recorded, on its own, to a value that stands out. The details must stay what an
// empty edit's are. A field added later, to either struct, and recorded by
// mistake fails here without anyone having had to think of a case for it. A field
// the row is meant to carry belongs in recorded below, in the builder and in its
// comment.
//
// Three checks are on the test itself, each one a slip in how a field is set: the
// filled request must differ from an empty one (a fill that sets nothing, sets a
// zero, or sets a copy would otherwise pass every subtest), a field of a type the
// fill does not know fails outright, and the walk must still see the fields it
// exists for.
func TestAccessUserUpdateDetailsRecordsOnlyTheGuardedFields(t *testing.T) {
	const userid = "nexara@pve"
	recorded := map[string]bool{"Enable": true, "Expire": true, "Groups": true}

	empty := accessUpdateDetailsJSON(t, userid, false, proxmox.UpdateAccessUserParams{})

	// fill sets slot, a pointer or a plain value, to something that stands out.
	fill := func(t *testing.T, slot reflect.Value, name string) {
		t.Helper()
		value := slot
		if slot.Kind() == reflect.Pointer {
			slot.Set(reflect.New(slot.Type().Elem()))
			value = slot.Elem()
		}
		switch value.Kind() {
		case reflect.String:
			value.SetString("sentinel-" + name)
		case reflect.Bool:
			value.SetBool(true)
		case reflect.Int64:
			value.SetInt(1700000000)
		default:
			t.Fatalf("%s is a %s; teach this test to set it", name, slot.Type())
		}
	}

	visited := 0
	for _, field := range reflect.VisibleFields(reflect.TypeOf(proxmox.UpdateAccessUserParams{})) {
		// The embedded AccessUserFields is walked through its own fields.
		if !field.IsExported() || field.Anonymous || recorded[field.Name] {
			continue
		}
		visited++
		t.Run(field.Name, func(t *testing.T) {
			var req proxmox.UpdateAccessUserParams
			fill(t, reflect.ValueOf(&req).Elem().FieldByIndex(field.Index), field.Name)
			if reflect.DeepEqual(req, proxmox.UpdateAccessUserParams{}) {
				t.Fatalf("filling %s left the request empty; this test would pass without checking it", field.Name)
			}

			if got := accessUpdateDetailsJSON(t, userid, false, req); got != empty {
				t.Errorf("setting %s changed the audit details to %s, from %s — "+
					"audit rows are readable by every Viewer, so only enable, expire and groups may be recorded",
					field.Name, got, empty)
			}
		})
	}

	// The five fields that are never recorded, and Append, are the reason this
	// test exists. Fewer than that means the walk stopped seeing them and every
	// subtest above is vacuous.
	if visited < 6 {
		t.Fatalf("visited %d fields of proxmox.UpdateAccessUserParams, want at least the six that are never "+
			"recorded (comment, email, firstname, lastname, keys, append)", visited)
	}
}

// accessKeysProbe is the fixture value of a user's keys: findable in a body
// whatever key it is under, and obviously not a real key.
const accessKeysProbe = "PROBE-ACCESS-KEYS-NOT-A-REAL-VALUE"

// accessWire marshals what a user shaper returns and reads back the one user in
// it: the object itself, or the only element of an array. It returns the JSON as
// well, for a message or a substring check.
//
// The list shaper's result is passed as the slice it is, which is what
// RespondItems hands the encoder and whose elements are addressable, so a
// pointer-receiver MarshalJSON added to the proxmox type later is dispatched
// here as it would be in production; a copy of one element would hide it. The
// detail shaper's is passed as the value c.JSON is given.
func accessWire(t *testing.T, v any) ([]byte, map[string]json.RawMessage) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	if len(raw) > 0 && raw[0] == '[' {
		var users []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &users); err != nil || len(users) != 1 {
			t.Fatalf("%s is not an array of one user (%v)", raw, err)
		}
		return raw, users[0]
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("%s is not a JSON object: %v", raw, err)
	}
	return raw, fields
}

// TestAccessUserReadsWithholdKeys pins what the two user reads make of a user's
// keys, one case for each thing has_keys has to tell apart. See
// accessUserResponse for what keys holds and why both reads, which every Viewer
// can make, must not send it.
//
// Each response is read back as the JSON a Viewer receives: the value is not in
// it, the key is not there even blank, and has_keys says whether Proxmox sent
// anything but whitespace. The response is also examined as a value, because it
// must hold no copy of keys: that is what keeps the body's silence from resting
// on the json tag alone. TestReadStructsStripCredentials makes the same demand
// of a probe in every field, and TestAccessUserReadsCarryEveryOtherField checks
// that nothing else went with it.
func TestAccessUserReadsWithholdKeys(t *testing.T) {
	tests := []struct {
		name    string
		keys    string
		hasKeys bool
	}{
		{"a value", accessKeysProbe, true},
		{"a value with whitespace around it", " \t" + accessKeysProbe + "\n", true},
		{"none", "", false},
		{"spaces only", "   ", false},
		{"a tab and a newline only", "\t\n", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			list := accessUsersForRead([]proxmox.AccessUser{{UserID: "alice@pve", Keys: tc.keys}})
			if len(list) != 1 {
				t.Fatalf("the list shaper turned one user into %d entries", len(list))
			}
			detail := accessUserDetailForRead(proxmox.AccessUserDetail{UserID: "alice@pve", Keys: tc.keys})

			responses := map[string]struct {
				body any
				// held is the keys value the response still carries as a value.
				held string
			}{
				"list":   {list, list[0].AccessUser.Keys},
				"detail": {detail, detail.AccessUserDetail.Keys},
			}
			for name, resp := range responses {
				raw, fields := accessWire(t, resp.body)

				if strings.Contains(string(raw), accessKeysProbe) {
					t.Errorf("%s: the keys value reached the body: %s", name, raw)
				}
				if _, present := fields["keys"]; present {
					t.Errorf("%s: the body carries a keys key; it should be absent, not blank: %s", name, raw)
				}
				if resp.held != "" {
					t.Errorf("%s: the response still holds the keys value %q", name, resp.held)
				}
				if got, want := string(fields["has_keys"]), strconv.FormatBool(tc.hasKeys); got != want {
					t.Errorf("%s: has_keys = %q, want %s for keys %q: %s", name, got, want, tc.keys, raw)
				}
			}
		})
	}
}

// accessUserFilled returns a T, one of the two Proxmox user structs, with every
// field set to something that stands out — the keys included, as a probe.
func accessUserFilled[T any](t *testing.T) T {
	t.Helper()
	var user T
	v := reflect.ValueOf(&user).Elem()
	for i := range v.NumField() {
		field, slot := v.Type().Field(i), v.Field(i)
		switch {
		case slot.Kind() == reflect.String:
			slot.SetString(probeValue(v.Type().Name(), jsonName(field)))
		case slot.Kind() == reflect.Bool:
			slot.SetBool(true)
		case slot.CanInt():
			slot.SetInt(7)
		case slot.Kind() == reflect.Slice && slot.Type().Elem().Kind() == reflect.String:
			slot.Set(reflect.ValueOf([]string{"group-a", "group-b"}))
		default:
			t.Fatalf("%s.%s is a %s; teach this test to fill it", v.Type().Name(), field.Name, slot.Type())
		}
	}
	return user
}

// requireOnlyKeysChanged runs a filled T through shape and requires the JSON it
// marshals to be the struct's own JSON without keys, and with has_keys.
func requireOnlyKeysChanged[T any](t *testing.T, shape func(T) any) {
	t.Helper()
	for _, hasKeys := range []bool{true, false} {
		user := accessUserFilled[T](t)
		if !hasKeys {
			reflect.ValueOf(&user).Elem().FieldByName("Keys").SetString("")
		}

		_, want := accessWire(t, user)
		// A fill that reached nothing would leave both sides near-empty and the
		// comparison below true of anything.
		if _, ok := want["keys"]; !ok || len(want) < 6 {
			t.Fatalf("the filled %T marshals to %d fields, keys among them or not: the fill did not reach them", user, len(want))
		}
		delete(want, "keys")
		want["has_keys"] = json.RawMessage(strconv.FormatBool(hasKeys))

		_, got := accessWire(t, shape(user))
		for name, w := range want {
			switch g, ok := got[name]; {
			case !ok:
				t.Errorf("%T (has_keys %v): %q is missing from the response", user, hasKeys, name)
			case string(g) != string(w):
				t.Errorf("%T (has_keys %v): %q is %s in the response, want %s", user, hasKeys, name, g, w)
			}
		}
		for name := range got {
			if _, ok := want[name]; !ok {
				t.Errorf("%T (has_keys %v): the response carries %q, which is not a field of the user", user, hasKeys, name)
			}
		}
	}
}

// TestAccessUserReadsCarryEveryOtherField compares each response with the
// Proxmox struct's own JSON: the same fields with the same values, minus keys,
// plus has_keys. The SPA reads the rest of a user from these responses.
//
// Both structs are filled by reflection, so a field Proxmox adds to either later
// has to reach the response to pass. That is what stops the embedding in
// accessUserResponse from being swapped for a hand-copied field list that leaves
// one out. A field of a kind the fill does not know fails outright rather than
// being skipped. The list is marshalled as the slice the shaper returns, and the
// detail as a value: see accessWire.
func TestAccessUserReadsCarryEveryOtherField(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		requireOnlyKeysChanged(t, func(u proxmox.AccessUser) any {
			return accessUsersForRead([]proxmox.AccessUser{u})
		})
	})
	t.Run("detail", func(t *testing.T) {
		requireOnlyKeysChanged(t, func(u proxmox.AccessUserDetail) any {
			return accessUserDetailForRead(u)
		})
	})
}
