package handlers

import (
	"context"
	"crypto/sha1" //nolint:gosec // the length of Proxmox's own digest is what is measured
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	"github.com/bigjakk/nexara/internal/proxmox"
)

const (
	// tokenTestKey is a 32-byte hex-encoded key, and tokenOtherKey a second one
	// unrelated to it.
	tokenTestKey   = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	tokenOtherKey  = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	tokenTestNode  = "pve-01"
	tokenDigest    = "0123456789abcdef0123456789abcdef01234567"
	tokenOtherDig  = "fedcba9876543210fedcba9876543210fedcba98"
	tokenClusterID = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
)

var tokenCluster = uuid.MustParse(tokenClusterID)

func mustToken(t *testing.T, key string, cluster uuid.UUID, node, digest string) string {
	t.Helper()
	tok, err := nodeConfigToken(key, cluster, node, digest)
	if err != nil {
		t.Fatalf("nodeConfigToken: %v", err)
	}
	return tok
}

// TestNodeConfigTokenKnownAnswer pins the token's whole construction to values
// computed independently, outside Go: the encoding (the cluster's 16 bytes, then
// the node and the digest each led by a big-endian uint32 length), HKDF-SHA256 over
// the key for the purpose "nexara node-config cas token v1", HMAC-SHA256 of the
// encoding under that subkey, and "v1." plus unpadded base64url. A token handed out
// by one release has to be the token the next one expects, for as long as a dialog
// stays open across an upgrade, so a change here is a decision, not a refactor.
func TestNodeConfigTokenKnownAnswer(t *testing.T) {
	for _, tt := range []struct {
		name    string
		key     string
		cluster uuid.UUID
		node    string
		want    string
	}{
		{"the vector", tokenTestKey, tokenCluster, tokenTestNode, "v1.Iu2yDwUGaP5ICN9gNLybaRGkU4hVupYLwfK7OpjUSg8"},
		{"another key", tokenOtherKey, tokenCluster, tokenTestNode, "v1.TNHuJbkx8zhq_uA8SDOZexalNk04KInt-lfuQ3dSCPo"},
		{"another node", tokenTestKey, tokenCluster, "pve-02", "v1.8ObhEydGwpSG9UfvoaFahGnHPCkJs2GhXZG47c352l8"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := mustToken(t, tt.key, tt.cluster, tt.node, tokenDigest); got != tt.want {
				t.Errorf("token = %q, want %q", got, tt.want)
			}
		})
	}
}

var tokenShape = regexp.MustCompile(`^v1\.[A-Za-z0-9_-]{43}$`)

func TestNodeConfigTokenShape(t *testing.T) {
	tok := mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, tokenDigest)
	if !tokenShape.MatchString(tok) {
		t.Errorf("token = %q, want the version prefix and 43 characters of unpadded base64url", tok)
	}
	if len(tok) > 128 {
		t.Errorf("token is %d characters, over the 128 the `digest` parameter allows", len(tok))
	}
	if strings.Contains(tok, tokenDigest) {
		t.Errorf("token %q contains Proxmox's digest", tok)
	}
	if strings.ContainsAny(tok, "=+/") {
		t.Errorf("token %q is not the unpadded base64url spelling", tok)
	}
}

func TestNodeConfigTokenIsDeterministic(t *testing.T) {
	a := mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, tokenDigest)
	b := mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, tokenDigest)
	if a != b {
		t.Errorf("the same inputs gave %q and %q", a, b)
	}
}

// TestNodeConfigTokenBindsEverything: the cluster, the node, Proxmox's digest and
// the key each change the token, which is what makes it good for one file at one
// moment on one node of one cluster and for nothing else.
func TestNodeConfigTokenBindsEverything(t *testing.T) {
	base := mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, tokenDigest)
	for _, tt := range []struct {
		name    string
		key     string
		cluster uuid.UUID
		node    string
		digest  string
	}{
		{"another cluster", tokenTestKey, uuid.MustParse("3f2504e0-4f89-11d3-9a0c-0305e82c3302"), tokenTestNode, tokenDigest},
		{"another node", tokenTestKey, tokenCluster, "pve-02", tokenDigest},
		{"a node named as a prefix", tokenTestKey, tokenCluster, "pve-0", tokenDigest},
		{"another digest", tokenTestKey, tokenCluster, tokenTestNode, tokenOtherDig},
		{"a digest one character off", tokenTestKey, tokenCluster, tokenTestNode, tokenDigest[:39] + "8"},
		{"another key", tokenOtherKey, tokenCluster, tokenTestNode, tokenDigest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := mustToken(t, tt.key, tt.cluster, tt.node, tt.digest); got == base {
				t.Errorf("%s gave the same token as the base case: %q", tt.name, got)
			}
		})
	}
}

// TestNodeConfigTokenMessageIsUnambiguous: the node and the digest are both
// variable in length, so a message that simply ran them together would make
// (node "ab", digest "c…") and (node "a", digest "bc…") one message and one token.
// Each is led by its length, and no two of these coincide — nor do their tokens.
func TestNodeConfigTokenMessageIsUnambiguous(t *testing.T) {
	pairs := [][2]string{
		{"a", "bcd"},
		{"ab", "cd"},
		{"abc", "d"},
		{"abcd", ""},
		{"", "abcd"},
		{"a\x00", "bcd"},
		{"a", "\x00bcd"},
	}
	seenMsg := map[string][2]string{}
	seenTok := map[string][2]string{}
	for _, pair := range pairs {
		raw, err := nodeConfigTokenMessage(tokenCluster, pair[0], pair[1])
		if err != nil {
			t.Fatalf("nodeConfigTokenMessage(%q, %q): %v", pair[0], pair[1], err)
		}
		msg := string(raw)
		if prev, dup := seenMsg[msg]; dup {
			t.Errorf("(node %q, digest %q) and (node %q, digest %q) encode to the same message", prev[0], prev[1], pair[0], pair[1])
		}
		seenMsg[msg] = pair
		if pair[1] == "" {
			continue // no digest, no token
		}
		tok := mustToken(t, tokenTestKey, tokenCluster, pair[0], pair[1])
		if prev, dup := seenTok[tok]; dup {
			t.Errorf("(node %q, digest %q) and (node %q, digest %q) have the same token", prev[0], prev[1], pair[0], pair[1])
		}
		seenTok[tok] = pair
	}
}

// TestAppendFieldLength: a length a uint32 cannot hold is refused and not truncated,
// and one it can hold is its four big-endian bytes. The largest ones only exist as an
// int on a 64-bit build, and are made at run time so that a 32-bit build still
// compiles.
func TestAppendFieldLength(t *testing.T) {
	var maxUint32 uint32 = math.MaxUint32
	cases := []struct {
		n    int
		want string
	}{
		{0, "\x00\x00\x00\x00"},
		{1, "\x00\x00\x00\x01"},
		{258, "\x00\x00\x01\x02"},
	}
	bad := []int{-1, math.MinInt32}
	if strconv.IntSize == 64 {
		cases = append(cases, struct {
			n    int
			want string
		}{int(maxUint32), "\xff\xff\xff\xff"})
		bad = append(bad, int(maxUint32)+1, math.MaxInt)
	}
	for _, tt := range cases {
		got, err := appendFieldLength([]byte{0xAA}, tt.n)
		if err != nil || string(got) != "\xAA"+tt.want {
			t.Errorf("appendFieldLength(%d) = %x, %v; want aa%x", tt.n, got, err, tt.want)
		}
	}
	for _, n := range bad {
		if got, err := appendFieldLength(nil, n); !errors.Is(err, errNodeConfigTokenField) || got != nil {
			t.Errorf("appendFieldLength(%d) = %x, %v; want the refusal", n, got, err)
		}
	}
}

// TestNodeConfigTokenOfNoDigestIsNone: a node with no config file has no digest,
// and so has no token to hand out and none to match. A read that returns no
// `digest` is how a caller learns there is nothing for a save to be based on.
func TestNodeConfigTokenOfNoDigestIsNone(t *testing.T) {
	tok, err := nodeConfigToken(tokenTestKey, tokenCluster, tokenTestNode, "")
	if err != nil || tok != "" {
		t.Errorf("nodeConfigToken of no digest = %q, %v; want no token and no error", tok, err)
	}
	for _, candidate := range []string{"", "v1.", mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, tokenDigest)} {
		ok, err := nodeConfigTokenMatches(tokenTestKey, tokenCluster, tokenTestNode, "", candidate)
		if err != nil || ok {
			t.Errorf("nodeConfigTokenMatches(no digest, %q) = %v, %v; want false and no error", candidate, ok, err)
		}
	}
}

// TestNodeConfigTokenMatches: the token for this cluster, node and digest matches,
// and everything else is simply "no" — with no error, since an error that said what
// was wrong with a guess would be a way to improve it.
func TestNodeConfigTokenMatches(t *testing.T) {
	good := mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, tokenDigest)

	t.Run("the token itself", func(t *testing.T) {
		ok, err := nodeConfigTokenMatches(tokenTestKey, tokenCluster, tokenTestNode, tokenDigest, good)
		if err != nil || !ok {
			t.Errorf("matches = %v, %v; want true and no error", ok, err)
		}
	})

	flipCase := func(s string) string {
		for i := len("v1."); i < len(s); i++ {
			switch c := s[i]; {
			case c >= 'a' && c <= 'z':
				return s[:i] + strings.ToUpper(string(c)) + s[i+1:]
			case c >= 'A' && c <= 'Z':
				return s[:i] + strings.ToLower(string(c)) + s[i+1:]
			}
		}
		return s
	}
	body := strings.TrimPrefix(good, "v1.")

	for _, tt := range []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"the prefix alone", "v1."},
		{"another version", "v2." + body},
		{"no prefix", body},
		{"the prefix in another case", "V1." + body},
		{"truncated", good[:len(good)-1]},
		{"too short to be a tag", "v1.abc"},
		{"one character too long", good + "A"},
		{"padded", good + "="},
		{"not base64 at all", "v1." + strings.Repeat("!", 43)},
		{"one character changed", flipCase(good)},
		{"leading space", " " + good},
		{"trailing newline", good + "\n"},
		{"Proxmox's raw digest", tokenDigest},
		{"the raw digest with the prefix", "v1." + tokenDigest},
		{"a long string", strings.Repeat("A", 128)},
		{"a NUL", good[:10] + "\x00" + good[11:]},
		{"the token of another digest", mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, tokenOtherDig)},
		{"the token of another node", mustToken(t, tokenTestKey, tokenCluster, "pve-02", tokenDigest)},
		{"the token of another cluster", mustToken(t, tokenTestKey, uuid.MustParse("3f2504e0-4f89-11d3-9a0c-0305e82c3302"), tokenTestNode, tokenDigest)},
		{"the token of another key", mustToken(t, tokenOtherKey, tokenCluster, tokenTestNode, tokenDigest)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ok, err := nodeConfigTokenMatches(tokenTestKey, tokenCluster, tokenTestNode, tokenDigest, tt.token)
			if err != nil {
				t.Errorf("err = %v; a token that does not match is not an error", err)
			}
			if ok {
				t.Errorf("%q matched", tt.token)
			}
		})
	}
}

// TestNodeConfigTokenIsBase64URLNotStandardBase64: a token is spelled with '-' and
// '_', never '+' and '/', and the standard spelling of the same tag is not
// accepted. The first vector's token happens to have neither character, so a digest
// is found whose token has one, and its standard spelling is tried against it.
func TestNodeConfigTokenIsBase64URLNotStandardBase64(t *testing.T) {
	found := 0
	for i := range 256 {
		digest := strings.Repeat("0", 32) + fmt.Sprintf("%08x", i)
		tok := mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, digest)
		if !strings.ContainsAny(tok, "-_") {
			continue
		}
		found++
		standard := "v1." + strings.NewReplacer("-", "+", "_", "/").Replace(strings.TrimPrefix(tok, "v1."))
		if ok, err := nodeConfigTokenMatches(tokenTestKey, tokenCluster, tokenTestNode, digest, tok); err != nil || !ok {
			t.Errorf("the token %q for digest %s did not match itself: %v, %v", tok, digest, ok, err)
		}
		if ok, err := nodeConfigTokenMatches(tokenTestKey, tokenCluster, tokenTestNode, digest, standard); err != nil || ok {
			t.Errorf("the standard-base64 spelling %q of %q matched: %v, %v", standard, tok, ok, err)
		}
	}
	if found == 0 {
		t.Fatal("no digest in the range gave a token with '-' or '_'; widen the range, or this test checks nothing")
	}
}

// TestNodeConfigTokenWithABadKey: the one error there is. A malformed
// ENCRYPTION_KEY is the server's fault and is reported as such, by both the mint
// and the check; neither falls back to anything.
func TestNodeConfigTokenWithABadKey(t *testing.T) {
	if tok, err := nodeConfigToken("not a key", tokenCluster, tokenTestNode, tokenDigest); !errors.Is(err, crypto.ErrInvalidKey) || tok != "" {
		t.Errorf("nodeConfigToken = %q, %v; want no token and ErrInvalidKey", tok, err)
	}
	if ok, err := nodeConfigTokenMatches("not a key", tokenCluster, tokenTestNode, tokenDigest, "v1."+strings.Repeat("A", 43)); !errors.Is(err, crypto.ErrInvalidKey) || ok {
		t.Errorf("nodeConfigTokenMatches = %v, %v; want false and ErrInvalidKey", ok, err)
	}
}

// tokenGuardSource parses one handler source file for the guards below.
func tokenGuardSource(t *testing.T, name string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return fset, file
}

// TestNodeConfigTokenIsComparedWithHMACEqual is the guard behind "constant time".
// A timing difference cannot be asserted from a test, but the way to get one can
// be forbidden in the source: the check must call hmac.Equal, and must not compare
// the token or the one it wants with ==, !=, bytes.Equal or strings.EqualFold, each
// of which stops at the first byte that differs and so tells a guesser how much of
// the guess was right.
//
// It reads the function's syntax, so it sees what is written and not what runs: it
// does not follow a comparison hidden in a helper, and a == against a string
// literal (the empty-string check) is allowed because that tells nothing about the
// token. It is aimed at the realistic edit, someone "simplifying" the check to
// `token == want`.
func TestNodeConfigTokenIsComparedWithHMACEqual(t *testing.T) {
	_, file := tokenGuardSource(t, "node_config_token.go")

	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "nodeConfigTokenMatches" {
			fn = d
		}
	}
	if fn == nil {
		t.Fatal("node_config_token.go has no nodeConfigTokenMatches; this guard would pass vacuously")
	}

	sawEqual := false
	isLiteral := func(e ast.Expr) bool { _, ok := e.(*ast.BasicLit); return ok }
	mentionsToken := func(e ast.Expr) bool {
		found := false
		ast.Inspect(e, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && (id.Name == "token" || id.Name == "want") {
				found = true
			}
			return true
		})
		return found
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					switch id.Name + "." + sel.Sel.Name {
					case "hmac.Equal":
						sawEqual = true
					case "bytes.Equal", "strings.EqualFold", "strings.Compare", "subtle.ConstantTimeEq":
						t.Errorf("nodeConfigTokenMatches calls %s.%s; compare with hmac.Equal", id.Name, sel.Sel.Name)
					}
				}
			}
		case *ast.BinaryExpr:
			if (x.Op == token.EQL || x.Op == token.NEQ) &&
				(mentionsToken(x.X) || mentionsToken(x.Y)) && !isLiteral(x.X) && !isLiteral(x.Y) {
				t.Errorf("nodeConfigTokenMatches compares the token with %s; compare with hmac.Equal, which does not stop at the first byte that differs", x.Op)
			}
		}
		return true
	})
	if !sawEqual {
		t.Error("nodeConfigTokenMatches does not call hmac.Equal")
	}
}

// TestGuard_NodeConfigWritesCheckTheSaveToken keeps the compare-and-swap from
// being bypassed by a handler that calls the client's node config writers
// directly. SetNodeOptions and SetNodeACMEConfig send `digest` to Proxmox as it is,
// and what a caller of the API sends is a save token, which Proxmox would refuse as
// stale every time; a handler that passed it through would break its saves, and one
// that dropped the check to make them work would make every save unconditional.
//
// Every function that calls either writer must, in this order, run the client's own
// refusals (ValidateNodeOptions or ValidateNodeACMEConfig) so that a refused request
// costs no Proxmox read, then nodeConfigSaveDigest, then the writer. The refusals are
// run on a copy of the request whose Digest is nodeConfigValidationDigest of the
// caller's, and not the request itself: the size refusal has to count the digest as
// Proxmox will be sent it, 40 characters, and not as a 46-character token or as none.
// (The ACME route's body cannot get near the size limit, so no route test can tell
// that copy from the request; this is what holds it.) The order is the position in
// the source: it sees what is written, not what runs, and says so.
func TestGuard_NodeConfigWritesCheckTheSaveToken(t *testing.T) {
	writers := map[string]string{
		"SetNodeOptions":    "ValidateNodeOptions",
		"SetNodeACMEConfig": "ValidateNodeACMEConfig",
	}

	_, files := parseGoFiles(t, ".")
	seen := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			pos := map[string][]token.Pos{}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if name := callName(call); name != "" {
					pos[name] = append(pos[name], call.Pos())
				}
				return true
			})
			for writer, validator := range writers {
				calls := pos[writer]
				if len(calls) == 0 {
					continue
				}
				seen[writer] = true
				name := qualifiedFuncName(fn)
				guard := pos["nodeConfigSaveDigest"]
				check := pos[validator]
				switch {
				case len(guard) == 0:
					t.Errorf("%s calls %s but never nodeConfigSaveDigest: its `digest` is a save token, and Proxmox would see it raw", name, writer)
				case len(check) == 0:
					t.Errorf("%s calls %s but never %s: a request that is going to be refused must not cost a Proxmox read", name, writer, validator)
				case check[0] > guard[0]:
					t.Errorf("%s calls %s after nodeConfigSaveDigest: the refusals come first, so a refused request costs no Proxmox read", name, validator)
				case guard[0] > calls[0]:
					t.Errorf("%s calls %s before nodeConfigSaveDigest: the save is written before it is checked", name, writer)
				default:
					// What the refusals are given: `x.Digest = nodeConfigValidationDigest(...)`,
					// before the call, and then x itself.
					checked := ""
					var assigned token.Pos
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						as, ok := n.(*ast.AssignStmt)
						if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 || as.Pos() >= check[0] {
							return true
						}
						sel, ok := as.Lhs[0].(*ast.SelectorExpr)
						rhs, isCall := as.Rhs[0].(*ast.CallExpr)
						if !ok || !isCall || sel.Sel.Name != "Digest" || callName(rhs) != "nodeConfigValidationDigest" {
							return true
						}
						if x, ok := sel.X.(*ast.Ident); ok {
							checked, assigned = x.Name, as.Pos()
						}
						return true
					})
					if checked == "" {
						t.Errorf("%s runs %s on a request whose Digest is not nodeConfigValidationDigest of the caller's: the size refusal would count the wrong number of bytes", name, validator)
						break
					}
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						c, ok := n.(*ast.CallExpr)
						if !ok || callName(c) != validator || c.Pos() < assigned {
							return true
						}
						if len(c.Args) != 1 {
							t.Errorf("%s calls %s with %d arguments", name, validator, len(c.Args))
							return true
						}
						if arg, ok := c.Args[0].(*ast.Ident); !ok || arg.Name != checked {
							t.Errorf("%s calls %s with something other than %s, the copy whose Digest is the validation digest", name, validator, checked)
						}
						return true
					})
				}
			}
		}
	}
	for writer := range writers {
		if !seen[writer] {
			t.Errorf("no handler calls %s; this guard would pass vacuously", writer)
		}
	}
}

// tokenStandIn is a Proxmox that answers GET /nodes/{node}/config with a digest the
// test controls, and counts what it was asked.
type tokenStandIn struct {
	mu     sync.Mutex
	status int
	body   string
	paths  []string
}

func (s *tokenStandIn) client(t *testing.T) *proxmox.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.paths = append(s.paths, r.Method+" "+r.URL.Path)
		status, body := s.status, s.body
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c, err := proxmox.NewClient(proxmox.ClientConfig{BaseURL: srv.URL, TokenID: "user@pam!test", TokenSecret: "secret-token-value"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func (s *tokenStandIn) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// TestNodeConfigSaveDigest drives the compare-and-swap's one decision against a
// stand-in Proxmox: what comes back for no token, a good token, a stale one, a raw
// digest and a file with no digest, what it costs Proxmox, and that no answer
// carries the digest or the token it was given.
func TestNodeConfigSaveDigest(t *testing.T) {
	fresh := tokenDigest
	reply := `{"data":{"description":"sentinel notes\n","digest":"` + fresh + `"}}`
	good := mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, fresh)
	stale := mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, tokenOtherDig)

	call := func(t *testing.T, s *tokenStandIn, tok string) (string, error) {
		t.Helper()
		return nodeConfigSaveDigest(context.Background(), s.client(t), tokenTestKey, tokenCluster, tokenTestNode, tok)
	}

	t.Run("no token is an unconditional save and asks Proxmox nothing", func(t *testing.T) {
		s := &tokenStandIn{body: reply}
		digest, err := call(t, s, "")
		if err != nil || digest != "" {
			t.Errorf("= %q, %v; want no digest and no error", digest, err)
		}
		if got := s.requests(); len(got) != 0 {
			t.Errorf("an unconditional save read %v from Proxmox", got)
		}
	})

	t.Run("a good token returns the FRESH digest, for Proxmox's own check to use", func(t *testing.T) {
		s := &tokenStandIn{body: reply}
		digest, err := call(t, s, good)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if digest != fresh {
			t.Errorf("digest = %q, want the digest Proxmox has now, %q", digest, fresh)
		}
		if got := s.requests(); len(got) != 1 || got[0] != "GET /api2/json/nodes/pve-01/config" {
			t.Errorf("Proxmox was asked %v, want one GET of the node's config", got)
		}
	})

	for _, tt := range []struct {
		name  string
		reply string
		token string
	}{
		{"a token for the file as it was is stale", reply, stale},
		{"Proxmox's raw digest is refused", reply, fresh},
		{"garbage is refused", reply, "v1.not-a-token"},
		{"a file with no digest has nothing to match", `{"data":{}}`, good},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &tokenStandIn{body: tt.reply}
			digest, err := call(t, s, tt.token)
			var fe *fiber.Error
			if !errors.As(err, &fe) || fe.Code != fiber.StatusConflict {
				t.Fatalf("err = %v, want a 409", err)
			}
			if fe.Message != nodeConfigChangedMessage {
				t.Errorf("message = %q, want the one Proxmox's own refusal gets, %q", fe.Message, nodeConfigChangedMessage)
			}
			if digest != "" {
				t.Errorf("digest = %q; a refused save is handed nothing to write with", digest)
			}
			for _, leak := range []string{fresh, tt.token, good} {
				if strings.Contains(fe.Message, leak) {
					t.Errorf("the refusal %q carries %q", fe.Message, leak)
				}
			}
		})
	}

	t.Run("a failed re-read is Proxmox's failure, mapped as any other", func(t *testing.T) {
		s := &tokenStandIn{status: http.StatusForbidden, body: "Permission check failed (/, Sys.Audit)"}
		_, err := call(t, s, good)
		var fe *fiber.Error
		if !errors.As(err, &fe) || fe.Code != fiber.StatusForbidden {
			t.Errorf("err = %v, want a 403", err)
		}
	})

	t.Run("an unusable encryption key is the server's, a 500", func(t *testing.T) {
		s := &tokenStandIn{body: reply}
		_, err := nodeConfigSaveDigest(context.Background(), s.client(t), "not a key", tokenCluster, tokenTestNode, good)
		var fe *fiber.Error
		if !errors.As(err, &fe) || fe.Code != fiber.StatusInternalServerError {
			t.Errorf("err = %v, want a 500", err)
		}
	})
}

// TestNodeConfigValidationDigest: the pre-save validation counts the request as
// Proxmox will be sent it. A save that carries a token is validated with a digest
// as long as Proxmox's own — sha1_hex of the file, 40 hex characters, which encode
// to themselves — whatever the token was: not the token, which is longer, and not
// nothing. A save that carries none is validated as it is.
func TestNodeConfigValidationDigest(t *testing.T) {
	sum := sha1.Sum([]byte("a node config file")) //nolint:gosec // measuring Proxmox's digest, not protecting anything
	proxmoxDigestLength := len(hex.EncodeToString(sum[:]))
	if proxmoxDigestLength != 40 {
		t.Fatalf("a sha1_hex is %d characters; the premise of the placeholder has changed", proxmoxDigestLength)
	}

	if got := nodeConfigValidationDigest(""); got != "" {
		t.Errorf("a save with no token is validated with the digest %q, want none: it carries no digest", got)
	}
	for _, tok := range []string{
		mustToken(t, tokenTestKey, tokenCluster, tokenTestNode, tokenDigest),
		tokenDigest, // Proxmox's own digest, sent as if it were a token
		"garbage",
		" ",
		strings.Repeat("a", 128),
	} {
		got := nodeConfigValidationDigest(tok)
		if len(got) != proxmoxDigestLength {
			t.Errorf("a save with the token %q is validated with a digest of %d characters, want Proxmox's %d", tok, len(got), proxmoxDigestLength)
		}
		if !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(got) {
			t.Errorf("the validation digest %q is not hex", got)
		}
		if url.QueryEscape(got) != got {
			t.Errorf("the validation digest %q changes when form-encoded, so it would be counted at another length", got)
		}
	}
	if len(nodeConfigDigestPlaceholder) != proxmoxDigestLength {
		t.Errorf("the placeholder is %d characters, want %d", len(nodeConfigDigestPlaceholder), proxmoxDigestLength)
	}
}

// fillNodeConfigStruct sets every exported field of the request struct behind ptr to
// a sentinel of its own, so that a copy that dropped or changed a field is seen.
func fillNodeConfigStruct(t *testing.T, ptr any) {
	t.Helper()
	v := reflect.ValueOf(ptr).Elem()
	for _, field := range reflect.VisibleFields(v.Type()) {
		if !field.IsExported() || field.Anonymous {
			continue
		}
		target := v.FieldByIndex(field.Index)
		switch {
		case field.Type == reflect.TypeOf((*proxmox.FlexInt)(nil)):
			target.Set(reflect.ValueOf(flex(424242)))
		case field.Type.Kind() == reflect.String:
			target.SetString("sentinel-value-of-" + field.Name)
		case field.Type == reflect.TypeOf([]string(nil)):
			target.Set(reflect.ValueOf([]string{"sentinel-value-of-" + field.Name}))
		default:
			t.Fatalf("%s is a %s, which this test does not know how to fill", field.Name, field.Type)
		}
	}
}

// TestNodeConfigForAuditDropsOnlyTheDigest: what a write's audit row is built from
// is the request without its digest — after the save check that is Proxmox's RAW
// digest, which the save token exists to keep from callers, and the row is readable
// by every Viewer — and otherwise the request exactly: a field it dropped would be a
// setting the row stopped naming, and the copy must not change the request the
// write is still to send.
func TestNodeConfigForAuditDropsOnlyTheDigest(t *testing.T) {
	t.Run("node options", func(t *testing.T) {
		var req proxmox.NodeOptions
		fillNodeConfigStruct(t, &req)
		if req.Digest == "" {
			t.Fatal("the fill left Digest empty; this test would pass without checking it")
		}
		raw := req.Digest

		got := nodeOptionsForAudit(req)
		if got.Digest != "" {
			t.Errorf("the audit copy carries the digest %q", got.Digest)
		}
		want := req
		want.Digest = ""
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the audit copy is %+v, want the request without its digest, %+v", got, want)
		}
		if req.Digest != raw {
			t.Errorf("building the audit copy changed the request: its digest is now %q, was %q", req.Digest, raw)
		}
	})

	t.Run("node ACME settings", func(t *testing.T) {
		var req proxmox.NodeACMEConfig
		fillNodeConfigStruct(t, &req)
		if req.Digest == "" {
			t.Fatal("the fill left Digest empty; this test would pass without checking it")
		}
		raw := req.Digest

		got := nodeACMEConfigForAudit(req)
		if got.Digest != "" {
			t.Errorf("the audit copy carries the digest %q", got.Digest)
		}
		want := req
		want.Digest = ""
		if !reflect.DeepEqual(got, want) {
			t.Errorf("the audit copy is %+v, want the request without its digest, %+v", got, want)
		}
		if req.Digest != raw {
			t.Errorf("building the audit copy changed the request: its digest is now %q, was %q", req.Digest, raw)
		}
	})
}

// TestGuard_NodeConfigAuditIsBuiltFromACopyWithoutTheDigest keeps the audit row's
// builders from ever being handed the request itself. After nodeConfigSaveDigest,
// the request's Digest is Proxmox's raw digest of the file; the builders do not
// record it today, but a builder that is given it is one reclassification of that
// field from writing, into a row every Viewer reads, the value the save token hides.
//
// In every function that calls a node config writer, then: the request is copied
// through the helper (nodeOptionsForAudit or nodeACMEConfigForAudit) before the
// write, and the request is not mentioned again after it, so the only thing the
// row can be built from is the copy. It reads the source — what is written, not
// what runs.
func TestGuard_NodeConfigAuditIsBuiltFromACopyWithoutTheDigest(t *testing.T) {
	writers := map[string]string{
		"SetNodeOptions":    "nodeOptionsForAudit",
		"SetNodeACMEConfig": "nodeACMEConfigForAudit",
	}

	_, files := parseGoFiles(t, ".")
	seen := map[string]bool{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			for writer, helper := range writers {
				var call *ast.CallExpr
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if c, ok := n.(*ast.CallExpr); ok && call == nil && callName(c) == writer {
						call = c
					}
					return true
				})
				if call == nil {
					continue
				}
				seen[writer] = true
				name := qualifiedFuncName(fn)

				if len(call.Args) == 0 {
					t.Errorf("%s calls %s with no arguments", name, writer)
					continue
				}
				reqIdent, ok := call.Args[len(call.Args)-1].(*ast.Ident)
				if !ok {
					t.Errorf("%s passes %s something other than a variable as its request; this guard cannot follow it", name, writer)
					continue
				}

				// The copy: `audited := helper(req)`, before the write.
				audited := ""
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					as, ok := n.(*ast.AssignStmt)
					if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 || as.Pos() >= call.Pos() {
						return true
					}
					c, ok := as.Rhs[0].(*ast.CallExpr)
					if !ok || callName(c) != helper || len(c.Args) != 1 {
						return true
					}
					if arg, ok := c.Args[0].(*ast.Ident); ok && arg.Name == reqIdent.Name {
						if lhs, ok := as.Lhs[0].(*ast.Ident); ok {
							audited = lhs.Name
						}
					}
					return true
				})
				if audited == "" {
					t.Errorf("%s writes with %s but never copies %s through %s before the write: its audit row would be built from the request itself, with Proxmox's raw digest in it",
						name, writer, reqIdent.Name, helper)
					continue
				}

				// Nothing after the write mentions the request; something mentions the copy.
				usesAudited := false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					id, ok := n.(*ast.Ident)
					if !ok || id.Pos() <= call.End() {
						return true
					}
					switch id.Name {
					case reqIdent.Name:
						t.Errorf("%s reads %s after the write: from there it holds Proxmox's raw digest, and the audit row is to be built from %s",
							name, reqIdent.Name, audited)
					case audited:
						usesAudited = true
					}
					return true
				})
				if !usesAudited {
					t.Errorf("%s never uses %s after the write, so the audit row is not built from it", name, audited)
				}
			}
		}
	}
	for writer := range writers {
		if !seen[writer] {
			t.Errorf("no handler calls %s; this guard would pass vacuously", writer)
		}
	}
}
