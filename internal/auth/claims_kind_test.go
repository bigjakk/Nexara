package auth

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fieldRole is what a field of Claims is FOR, as far as telling the kinds of token
// apart goes.
type fieldRole string

const (
	// roleStandard is the registered JWT claims (expiry, subject, issuer, id): they
	// are the same in every kind.
	roleStandard fieldRole = "standard"
	// roleIdentity is who the token is for: copied to the request, never a reason to
	// treat a token as another kind.
	roleIdentity fieldRole = "identity"
	// roleKindMarker decides the kind: setting one takes the token out of the session
	// kind, and Kind must say so.
	roleKindMarker fieldRole = "kind marker"
)

// claimsFieldRoles is every field of Claims and its role. It is restated here, by
// hand, because the point is that adding a field makes this test fail until
// somebody has decided which of the three it is.
var claimsFieldRoles = map[string]fieldRole{
	"RegisteredClaims": roleStandard,
	"UserID":           roleIdentity,
	"Email":            roleIdentity,
	"Role":             roleIdentity,
	"ConsoleScope":     roleKindMarker,
	"WSScope":          roleKindMarker,
}

// setNonZero sets a field to a value that is not its zero value, for the kinds of
// field a marker can be. A field of any other kind is a reason to teach this
// helper, not to skip the field.
func setNonZero(t *testing.T, name string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Array:
		// A uuid.UUID, say: one non-zero element makes the value non-zero.
		setNonZero(t, name+"[0]", v.Index(0))
	case reflect.Struct:
		// The registered claims, say: the first field that can be set.
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				setNonZero(t, name+"."+v.Type().Field(i).Name, v.Field(i))
				return
			}
		}
		t.Fatalf("Claims.%s is a struct with no field that can be set", name)
	default:
		t.Fatalf("Claims.%s is a %s: teach setNonZero to set one, so that the marker can be tested", name, v.Kind())
	}
}

// TestClaimsKind_EveryFieldIsClassified fails when a field is added to Claims that
// this file does not classify, or removed while still listed, so that a new claim is
// never silently a thing the API's authentication ignores. And it holds Kind to what
// the classification says: no marker is a session; each marker set alone is not; a
// field that only says who the token is never changes the kind.
func TestClaimsKind_EveryFieldIsClassified(t *testing.T) {
	typ := reflect.TypeOf(Claims{})

	seen := map[string]bool{}
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		seen[name] = true
		if _, ok := claimsFieldRoles[name]; !ok {
			t.Errorf("Claims has a field %q that claims_kind_test.go does not classify: say whether it is standard, identity or a kind marker — "+
				"and if it is a marker, Kind() must treat it, or a token carrying it is a session", name)
		}
	}
	for name := range claimsFieldRoles {
		if !seen[name] {
			t.Errorf("claimsFieldRoles lists %q, which Claims no longer has", name)
		}
	}

	var zero Claims
	if !zero.IsSession() {
		t.Error("claims with no scope marker are not a session: an ordinary access token must be")
	}

	for name, role := range claimsFieldRoles {
		field, ok := typ.FieldByName(name)
		if !ok {
			continue
		}
		var c Claims
		setNonZero(t, name, reflect.ValueOf(&c).Elem().FieldByIndex(field.Index))
		switch role {
		case roleKindMarker:
			if c.IsSession() {
				t.Errorf("claims with only %s set are a session: a marker must take a token out of the session kind", name)
			}
			if c.Kind() == TokenKindSession {
				t.Errorf("Kind() of claims with only %s set is the session kind", name)
			}
		case roleIdentity, roleStandard:
			if !c.IsSession() {
				t.Errorf("claims with only %s set are not a session: %s is %s, which says who the token is for and never what kind it is", name, name, role)
			}
		}
	}
}

// TestClaimsKind_Kinds is the table of what Kind says: each kind is the one thing it
// is, and whatever fits none — both markers, or a hub marker of a value this code
// does not issue — is unknown, which nothing accepts.
func TestClaimsKind_Kinds(t *testing.T) {
	scope := &ConsoleScope{ClusterID: "cluster01", Node: "pve-01", Type: "node_shell"}
	for _, tc := range []struct {
		name   string
		claims Claims
		want   TokenKind
	}{
		{"no marker: an interactive session", Claims{}, TokenKindSession},
		{"a console scope", Claims{ConsoleScope: scope}, TokenKindConsole},
		{"the hub scope", Claims{WSScope: WSScopeHub}, TokenKindWSHub},
		{"both markers at once", Claims{ConsoleScope: scope, WSScope: WSScopeHub}, TokenKindUnknown},
		{"a WS scope this code does not issue", Claims{WSScope: "elsewhere"}, TokenKindUnknown},
		{"a console scope beside an unknown WS scope", Claims{ConsoleScope: scope, WSScope: "elsewhere"}, TokenKindUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.claims.Kind(); got != tc.want {
				t.Errorf("Kind() = %v, want %v", got, tc.want)
			}
			if got, want := tc.claims.IsSession(), tc.want == TokenKindSession; got != want {
				t.Errorf("IsSession() = %v, want %v", got, want)
			}
		})
	}

	t.Run("the zero TokenKind is unknown", func(t *testing.T) {
		if got := TokenKind(0); got != TokenKindUnknown {
			t.Errorf("the zero TokenKind is %v, want TokenKindUnknown: a kind nobody filled in must not be a session", got)
		}
	})
}

// TestClaimsKind_TokensTheServiceIssuesHaveTheKindTheyWereIssuedFor ties Kind to the
// issuers: what GenerateAccessToken, GenerateConsoleToken and GenerateWSHubToken
// sign is what Kind then reads back, through validation, as the same kind.
func TestClaimsKind_TokensTheServiceIssuesHaveTheKindTheyWereIssuedFor(t *testing.T) {
	svc := NewJWTService("claims-kind-test-secret", 15*time.Minute, 24*time.Hour)
	user := uuid.New()
	scope := ConsoleScope{ClusterID: "cluster01", Node: "pve-01", Type: "node_shell"}

	access, _, err := svc.GenerateAccessToken(user, "alice@example.com", "admin")
	if err != nil {
		t.Fatalf("GenerateAccessToken: %v", err)
	}
	console, _, err := svc.GenerateConsoleToken(user, "alice@example.com", "admin", scope, time.Minute)
	if err != nil {
		t.Fatalf("GenerateConsoleToken: %v", err)
	}
	hub, _, err := svc.GenerateWSHubToken(user, "alice@example.com", "admin", time.Minute)
	if err != nil {
		t.Fatalf("GenerateWSHubToken: %v", err)
	}

	for _, tc := range []struct {
		name, token string
		want        TokenKind
	}{
		{"an access token", access, TokenKindSession},
		{"a console token", console, TokenKindConsole},
		{"a hub token", hub, TokenKindWSHub},
	} {
		claims, err := svc.ValidateAccessToken(tc.token)
		if err != nil {
			t.Fatalf("%s: ValidateAccessToken: %v", tc.name, err)
		}
		if got := claims.Kind(); got != tc.want {
			t.Errorf("%s: Kind() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestGuard_OnlyClaimsKindAndTheWebSocketUpgradeReadTheScopeMarkers holds the one-
// predicate rule mechanically. "Is this token a session?" is Claims.IsSession's
// answer and nobody else's: the middleware, the logout ownership check and anything
// added later ask it, and none re-derives it from ConsoleScope and WSScope. That is
// the whole point of the predicate — a marker added to Claims is then one change to
// Kind and not one more test to remember in every function that had copied the
// others — and with the two exclusions equivalent today, nothing behavioural can
// tell a function that asks from one that has quietly gone back to the fields. So
// this reads the source: outside claims_kind.go, a non-test file may read a scope
// marker (a selector, x.ConsoleScope or x.WSScope) only under internal/ws.
//
// internal/ws is the exception and a recorded follow-up, not a decision. The
// WebSocket upgrade does not ask "a session?" but "which of the scoped kinds?" —
// a console token on /ws/console, a hub token on /ws — and still checks the markers
// itself (ws/server.go). It would be better asked of Kind; it is not edited here.
// The type auth.ConsoleScope, which the console handlers name, is not a read of a
// marker, and the issuers set the markers in composite literals, which are not
// reads either.
func TestGuard_OnlyClaimsKindAndTheWebSocketUpgradeReadTheScopeMarkers(t *testing.T) {
	root := filepath.Join("..", "..")
	const allowedFile = "internal/auth/claims_kind.go"
	const allowedTree = "internal/ws/"

	reads := map[string][]string{}
	fset := token.NewFileSet()
	for _, dir := range []string{"internal", "cmd", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Contains(raw, []byte("ConsoleScope")) && !bytes.Contains(raw, []byte("WSScope")) {
				return nil
			}
			file, err := parser.ParseFile(fset, path, raw, 0)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			ast.Inspect(file, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "ConsoleScope" && sel.Sel.Name != "WSScope") {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "auth" {
					return true // auth.ConsoleScope: the type, qualified by its package
				}
				reads[rel] = append(reads[rel], fset.Position(sel.Pos()).String())
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	if len(reads[allowedFile]) == 0 {
		t.Fatalf("%s reads no scope marker: the scan does not see field reads, or Kind no longer decides — this guard has gone stale", allowedFile)
	}
	for file, at := range reads {
		if file == allowedFile || strings.HasPrefix(file, allowedTree) {
			continue
		}
		t.Errorf("%s reads a token's scope marker (%s): ask Claims.IsSession or Claims.Kind instead, so that "+
			"\"is this a session\" has one answer, in %s", file, strings.Join(at, ", "), allowedFile)
	}
}
