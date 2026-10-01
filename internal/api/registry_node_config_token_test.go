package api

import (
	"cmp"
	"context"
	"crypto/sha1" //nolint:gosec // the stand-in reproduces Proxmox's own digest, which is a SHA1
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The node config save token, followed through the real routes.
//
// GET .../options, GET .../notes and GET .../acme-config return a `digest` that is
// a save token, never Proxmox's own digest of /etc/pve/nodes/<node>/config; PUT
// .../options and PUT .../acme-config take it back and check it against the file as
// it is when the save arrives. Proxmox's digest is the unsalted SHA1 of the WHOLE
// file, the notes included, and Proxmox writes that file deterministically (the
// notes as '#' lines first, then every other key sorted), so a caller who is shown
// it can hash a guess at the notes and compare, offline, and one who is not but may
// write can PUT candidate digests and tell a miss from a hit by the 409. The token
// is an HMAC of it under a key only Nexara has.
//
// The handlers' own tests (handlers/node_config_token_test.go) pin the token: its
// derivation, what it binds and how it is compared. What only a route can show is
// everything around it, which is what this file does, through the real
// declarations and their real permission gate, with the real handlers and stand-ins
// only for Proxmox and the database: who is handed a token and who is not, what a
// save sends to Proxmox in its place, what a save that is refused leaves behind
// (nothing: no write, no audit row), and that the two routes that share the file
// share one answer.
//
// The stand-in Proxmox here is not the canned one the access routes use: it HOLDS
// the file. A save changes what the next read sees, and its digest is what
// Proxmox's is, so "the file changed between the read and the save" is something a
// test does to the file and not a reply it scripts.

// errRBACLookupDown is the failure an RBAC engine reports when it cannot answer.
var errRBACLookupDown = errors.New("rbac lookup down")

// failingRBACEngine answers like stubRBACEngine, from grants keyed "action:resource",
// except for the one permission in failOn, whose lookup fails. It structurally
// satisfies the unexported permissionEngine interface in internal/api/handlers.
type failingRBACEngine struct {
	grants map[string]bool
	failOn string
}

func (e *failingRBACEngine) answer(action, resource string) (bool, error) {
	if action+":"+resource == e.failOn {
		return false, errRBACLookupDown
	}
	return e.grants[action+":"+resource], nil
}

func (e *failingRBACEngine) HasPermission(_ context.Context, _ uuid.UUID, action, resource, _ string, _ uuid.UUID) (bool, error) {
	return e.answer(action, resource)
}

func (e *failingRBACEngine) HasGlobalPermission(_ context.Context, _ uuid.UUID, action, resource string) (bool, error) {
	return e.answer(action, resource)
}

func (e *failingRBACEngine) LoadUserPermissions(_ context.Context, _ uuid.UUID) (*auth.UserPermissions, error) {
	return &auth.UserPermissions{}, nil
}

// scopedGrant is one grant of "action:resource": at one cluster, or at the global
// scope when cluster is uuid.Nil.
type scopedGrant struct {
	permission string
	cluster    uuid.UUID
}

// rbacLookup is one question the handler asked the engine.
type rbacLookup struct {
	global     bool
	permission string
	scopeType  string
	scopeID    uuid.UUID
}

// scopedRBACEngine is an RBAC engine that, unlike stubRBACEngine, tells the scopes
// apart, and records every lookup it is asked. stubRBACEngine answers
// HasPermission and HasGlobalPermission from one map whatever scope is named, so a
// handler that asked the wrong one — the global scope, or no cluster — would pass
// every test written against it. This one answers as internal/auth's RBACEngine
// does: a permission granted at a cluster holds for that cluster alone, and one
// granted globally holds for every cluster.
//
// globalOnly names permissions that only the global lookup says yes to. The real
// engine never does that (its cluster lookup honours a global grant), which is the
// point of having it here: a handler that decides through the global lookup shows
// the permission to a caller whom the cluster-scoped one refuses.
type scopedRBACEngine struct {
	mu         sync.Mutex
	grants     []scopedGrant
	globalOnly []string
	lookups    []rbacLookup
}

func (e *scopedRBACEngine) HasPermission(_ context.Context, _ uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
	perm := action + ":" + resource
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lookups = append(e.lookups, rbacLookup{permission: perm, scopeType: scopeType, scopeID: scopeID})
	for _, g := range e.grants {
		if g.permission != perm {
			continue
		}
		if g.cluster == uuid.Nil || (scopeType == "cluster" && g.cluster == scopeID) {
			return true, nil
		}
	}
	return false, nil
}

func (e *scopedRBACEngine) HasGlobalPermission(_ context.Context, _ uuid.UUID, action, resource string) (bool, error) {
	perm := action + ":" + resource
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lookups = append(e.lookups, rbacLookup{global: true, permission: perm})
	if slices.Contains(e.globalOnly, perm) {
		return true, nil
	}
	for _, g := range e.grants {
		if g.permission == perm && g.cluster == uuid.Nil {
			return true, nil
		}
	}
	return false, nil
}

func (e *scopedRBACEngine) LoadUserPermissions(_ context.Context, _ uuid.UUID) (*auth.UserPermissions, error) {
	return &auth.UserPermissions{}, nil
}

func (e *scopedRBACEngine) asked(permission string) []rbacLookup {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []rbacLookup
	for _, l := range e.lookups {
		if l.permission == permission {
			out = append(out, l)
		}
	}
	return out
}

// authWithEngine is stubAuth with the engine given: a request carrying no
// X-Test-User is refused as authRequired refuses one with no bearer token.
func authWithEngine(engine any) fiber.Handler {
	return func(c fiber.Ctx) error {
		if c.Get("X-Test-User") == "" {
			return fiber.NewError(fiber.StatusUnauthorized, "Missing authorization token")
		}
		c.Locals("user_id", uuid.MustParse(testUserID))
		c.Locals("rbac_engine", engine)
		return c.Next()
	}
}

// decodeKeys unmarshals a JSON object and returns it with its keys sorted.
func decodeKeys(t *testing.T, body []byte) (map[string]any, []string) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", body, err)
	}
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return got, keys
}

// saveTokenShape is what a save token looks like: its version in the clear, then
// the unpadded base64url of a 32-byte HMAC-SHA256.
var saveTokenShape = regexp.MustCompile(`^v1\.[A-Za-z0-9_-]{43}$`)

// rawDigestShape is Proxmox's own digest: 40 hex characters, which must not appear
// anywhere in a response.
var rawDigestShape = regexp.MustCompile(`[0-9a-f]{40}`)

// nodeConfigChangedAnswer is the one 409 a save is given when the file is not what
// it was based on, whichever of the two checks found out: Nexara's comparison of
// the token, or Proxmox's own assert_if_modified. They answer in the same words, so
// a caller cannot tell from the answer which of them caught it — and in particular
// cannot tell a token that is not one from a file that moved.
const nodeConfigChangedAnswer = "The node's configuration changed since it was read — reload and try again."

// requireSaveToken checks that the body carries the save token as its `digest`, and
// that nothing of Proxmox's own digest is in it, and returns the token.
func requireSaveToken(t *testing.T, body []byte, rawDigest string) string {
	t.Helper()
	got, _ := decodeKeys(t, body)
	token, ok := got["digest"].(string)
	if !ok {
		t.Fatalf("the body has no digest: %s", clipForFailure(string(body), 300))
	}
	if !saveTokenShape.MatchString(token) {
		t.Errorf("digest = %q, want a save token (v1. and 43 base64url characters)", token)
	}
	requireNoRawDigest(t, body, rawDigest)
	return token
}

// requireNoRawDigest fails when Proxmox's digest, or anything shaped like one, is
// in the body.
func requireNoRawDigest(t *testing.T, body []byte, rawDigest string) {
	t.Helper()
	if rawDigest != "" && strings.Contains(string(body), rawDigest) {
		t.Errorf("Proxmox's own digest %s reached the caller: %s", rawDigest, clipForFailure(string(body), 300))
	}
	if m := rawDigestShape.FindString(string(body)); m != "" {
		t.Errorf("something shaped like Proxmox's digest (%s) reached the caller: %s", m, clipForFailure(string(body), 300))
	}
}

// nodeConfigFile is one node's /etc/pve/nodes/<node>/config as the stand-in holds
// it: the notes, and every other key.
type nodeConfigFile struct {
	notes string
	keys  map[string]string
}

// noteLines are the lines write_node_config writes the notes as: it splits them on
// "\n" with Perl's split, which drops the trailing empty fields, so "a", "a\n" and
// "a\n\n" are one line and one file, and notes of nothing but line breaks are none.
func (f *nodeConfigFile) noteLines() []string {
	lines := strings.Split(f.notes, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// raw is the text of the file in the shape write_node_config (PVE/NodeConfig.pm)
// gives it: the notes as '#' lines first, then every other key, sorted. The notes'
// escaping is a stand-in for encode_text; all that matters here is that the text is
// a function of the content, as Proxmox's is.
func (f *nodeConfigFile) raw() string {
	var b strings.Builder
	for _, line := range f.noteLines() {
		b.WriteString("#" + url.PathEscape(line) + "\n")
	}
	keys := slices.Sorted(maps.Keys(f.keys))
	for _, k := range keys {
		b.WriteString(k + ": " + f.keys[k] + "\n")
	}
	return b.String()
}

// readNotes is the notes as a read returns them: parse_node_config ends every line
// of them with "\n".
func (f *nodeConfigFile) readNotes() string {
	var b strings.Builder
	for _, line := range f.noteLines() {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// digest is what Proxmox calls the file's digest: the SHA1 of all of it. A file
// with nothing in it has none: parse_node_config answers it with an empty config
// (`return {} if !$raw`), so a file nobody has written to and one that has been
// emptied, as when every setting and the notes were cleared, are the same to a read.
func (f *nodeConfigFile) digest() string {
	raw := f.raw()
	if raw == "" {
		return ""
	}
	sum := sha1.Sum([]byte(raw)) //nolint:gosec // Proxmox's digest is a SHA1
	return hex.EncodeToString(sum[:])
}

// nodeConfigPVE stands in for the node config of a cluster's Proxmox, both
// directions of GET and PUT /nodes/{node}/config, for every node, with the parts of
// Proxmox's behaviour that a save token depends on:
//
//   - A node it holds no file for has no config file, and so has one whose file holds
//     nothing: its read is an empty config with no digest, and the first write that
//     follows is not compared with anything. A file is created by a write.
//   - The digest is the SHA1 of the file's text, which is a function of its content
//     as write_node_config makes it: the notes as '#' lines, split on "\n" with the
//     trailing empty fields dropped, so "a" and "a\n" are one file, then every other
//     key, sorted. A read returns the notes with each line ended by "\n".
//   - The PUT does what set_options does: a digest is compared only when both sides
//     have one (PVE::Tools::assert_if_modified), a mismatch dies with the plain 500
//     Proxmox's does, the values are applied first and `delete` after, so a key named
//     in both is gone (which is why the client refuses such a request).
//
// What it does not do: check a value against $confdesc, re-check the ACME settings,
// forward the call to the node, or write the file byte for byte as Proxmox does — the
// notes' escaping is url.PathEscape where Proxmox's is encode_text — so its digests
// are Proxmox's in kind and not for the same content. It records every request, the
// PUT's form decoded.
type nodeConfigPVE struct {
	mu    sync.Mutex
	url   string
	files map[string]*nodeConfigFile
	seen  []accessUpdatePVERequest
	reads int
	// afterRead maps the number of a GET, the first being 1, to what happens to the
	// files once it has been answered: a change that lands in the window between
	// the handler's read of the digest and its write.
	afterRead map[int]func(files map[string]*nodeConfigFile)
	// readStatus and readBody, when set, are the refusal every GET is answered with.
	readStatus int
	readBody   string
}

func newNodeConfigPVE(t *testing.T) *nodeConfigPVE {
	t.Helper()
	p := &nodeConfigPVE{
		files:     map[string]*nodeConfigFile{},
		afterRead: map[int]func(map[string]*nodeConfigFile){},
	}
	srv := httptest.NewServer(http.HandlerFunc(p.handle))
	t.Cleanup(srv.Close)
	p.url = srv.URL
	return p
}

// configNodeOf returns the node a /api2/json/nodes/{node}/config path names.
func configNodeOf(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/api2/json/nodes/")
	if !ok {
		return "", false
	}
	node, tail, ok := strings.Cut(rest, "/")
	return node, ok && node != "" && tail == "config"
}

func answer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (p *nodeConfigPVE) handle(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, accessUpdatePVERequest{method: r.Method, path: r.URL.Path, form: r.PostForm})

	node, ok := configNodeOf(r.URL.Path)
	switch {
	case !ok:
		answer(w, http.StatusNotImplemented, "no such uri")
	case r.Method == http.MethodGet:
		p.get(w, node)
	case r.Method == http.MethodPut:
		p.put(w, node, r.PostForm)
	default:
		answer(w, http.StatusNotImplemented, "method not available")
	}
}

func (p *nodeConfigPVE) get(w http.ResponseWriter, node string) {
	if p.readStatus != 0 {
		answer(w, p.readStatus, p.readBody)
		return
	}
	data := map[string]any{}
	if f := p.files[node]; f != nil && f.digest() != "" {
		for k, v := range f.keys {
			data[k] = v
		}
		if notes := f.readNotes(); notes != "" {
			data["description"] = notes
		}
		data["digest"] = f.digest()
	}
	body, _ := json.Marshal(map[string]any{"data": data})
	answer(w, http.StatusOK, string(body))

	p.reads++
	if after := p.afterRead[p.reads]; after != nil {
		after(p.files)
	}
}

func (p *nodeConfigPVE) put(w http.ResponseWriter, node string, form url.Values) {
	f := p.files[node]
	// assert_if_modified is skipped unless both sides have a digest, and a file with
	// nothing in it has none.
	if given := form.Get("digest"); given != "" && f != nil && f.digest() != "" && given != f.digest() {
		answer(w, http.StatusInternalServerError, nodeOptionsStaleDigestBody)
		return
	}
	if f == nil {
		f = &nodeConfigFile{keys: map[string]string{}}
		p.files[node] = f
	}
	for k, vs := range form {
		switch k {
		case "digest", "delete":
		case "description":
			f.notes = vs[0]
		default:
			f.keys[k] = vs[0]
		}
	}
	// `delete` after the values, as set_options does.
	for _, k := range strings.Split(form.Get("delete"), ",") {
		switch k {
		case "":
		case "description":
			f.notes = ""
		default:
			delete(f.keys, k)
		}
	}
	answer(w, http.StatusOK, `{"data":null}`)
}

// setFile gives a node a config file with this content.
func (p *nodeConfigPVE) setFile(node, notes string, keys map[string]string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cloned := maps.Clone(keys)
	if cloned == nil {
		cloned = map[string]string{}
	}
	p.files[node] = &nodeConfigFile{notes: notes, keys: cloned}
}

// setNotes is somebody else's edit of the file: the Notes panel of Proxmox's own
// UI, say. It changes the file, and so its digest, without going through Nexara.
func (p *nodeConfigPVE) setNotes(node, notes string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.files[node] == nil {
		p.files[node] = &nodeConfigFile{keys: map[string]string{}}
	}
	p.files[node].notes = notes
}

// changeAfterRead has the nth GET (the first being 1) followed by a change to the
// files, once its answer has been computed.
func (p *nodeConfigPVE) changeAfterRead(n int, change func(files map[string]*nodeConfigFile)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.afterRead[n] = change
}

// failReads makes every GET from now on a refusal with this status and body.
func (p *nodeConfigPVE) failReads(status int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readStatus, p.readBody = status, body
}

// digestOf is the digest of a node's file as it is now, "" for a node with none or
// whose file holds nothing.
func (p *nodeConfigPVE) digestOf(node string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if f := p.files[node]; f != nil {
		return f.digest()
	}
	return ""
}

func (p *nodeConfigPVE) requests() []accessUpdatePVERequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.seen)
}

// mark and since split the request log: what a step did is what it added after the
// mark taken before it, which keeps the reads that minted a token out of the count.
func (p *nodeConfigPVE) mark() int { return len(p.requests()) }

func (p *nodeConfigPVE) since(mark int) []accessUpdatePVERequest { return p.requests()[mark:] }

// nodeConfigSettings is what a node's file holds in most of these tests: a setting
// of each half of it, the options' and the ACME settings'.
func nodeConfigSettings() map[string]string {
	return map[string]string{
		"ballooning-target": "75",
		"wakeonlan":         "02:00:00:00:00:01",
		"acme":              "account=sentinel-account",
		"acmedomain0":       "node.example.com",
	}
}

const (
	nodeConfigNotes = "sentinel notes\n"
	// otherTestClusterID is a second cluster, for the tests that need the same node
	// and the same file under another cluster's id.
	otherTestClusterID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	// otherTestEncKey is a second ENCRYPTION_KEY, for the tests that need the same
	// cluster, node and file under another key.
	otherTestEncKey = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	// otherTestNodeName is a second node, for the tests that need another node in
	// the same cluster.
	otherTestNodeName = "pve-02"
)

// nodeConfigWorld is the five real routes of the node config file, mounted on the
// real handlers, wired to a stand-in Proxmox and a stand-in database that holds
// one cluster row pointing at it.
type nodeConfigWorld struct {
	app       *fiber.App
	store     *accessUpdateDB
	clusterID string
}

type nodeConfigWorldOptions struct {
	// key is the handlers' ENCRYPTION_KEY; accessUpdateEncKey when empty.
	key string
	// clusterID is the cluster the stand-in database holds; testClusterID when empty.
	clusterID string
	// auth is the authentication middleware; one granting every permission of the
	// five routes when nil.
	auth fiber.Handler
}

// allNodeConfigGrants is every permission the five routes ask for.
var allNodeConfigGrants = []string{"view:node", "manage:node", "view:certificate", "manage:certificate"}

func newNodeConfigWorld(t *testing.T, pve *nodeConfigPVE, o nodeConfigWorldOptions) *nodeConfigWorld {
	t.Helper()
	key := cmp.Or(o.key, accessUpdateEncKey)
	clusterID := cmp.Or(o.clusterID, testClusterID)
	authn := o.auth
	if authn == nil {
		authn = stubAuth(nodeOptionsGrants(allNodeConfigGrants...))
	}

	secret, err := crypto.Encrypt("token-secret-value", key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	store := &accessUpdateDB{cluster: db.Cluster{
		ID:                   uuid.MustParse(clusterID),
		Name:                 "cluster01",
		ApiUrl:               pve.url,
		TokenID:              accessUpdateSelf + "!api",
		TokenSecretEncrypted: secret,
		IsActive:             true,
	}}
	nodes := handlers.NewNodeHandler(db.New(store), key, nil)
	acme := handlers.NewACMEHandler(db.New(store), key, nil)

	const acmeConfigPath = acmeNodeScope + "/acme-config"
	handlerOf := map[string]Handler{
		http.MethodGet + " " + nodeOptionsPath: nodes.GetNodeOptions,
		http.MethodGet + " " + nodeNotesPath:   nodes.GetNodeNotes,
		http.MethodPut + " " + nodeOptionsPath: nodes.SetNodeOptions,
		http.MethodGet + " " + acmeConfigPath:  acme.GetNodeACMEConfig,
		http.MethodPut + " " + acmeConfigPath:  acme.SetNodeACMEConfig,
	}
	var endpoints []Endpoint
	for _, e := range newRouteStubServer(t).registry.Endpoints() {
		if h, ok := handlerOf[e.Method+" "+e.Path]; ok {
			e.Handler = h
			endpoints = append(endpoints, e)
		}
	}
	if len(endpoints) != len(handlerOf) {
		t.Fatalf("the registry declares %d of the %d node config routes", len(endpoints), len(handlerOf))
	}
	return &nodeConfigWorld{app: newRegistryApp(t, authn, endpoints...), store: store, clusterID: clusterID}
}

// request is a request to one of the routes, for this node in this world's cluster,
// signed in as the stub session. fields, for a write, are the JSON body.
func (w *nodeConfigWorld) request(method, path, node string, fields map[string]any) *http.Request {
	target := strings.NewReplacer(":cluster_id", w.clusterID, ":node_name", node, ":node", node).Replace(path)
	var req *http.Request
	if method == http.MethodGet {
		req = httptest.NewRequest(method, target, nil)
	} else {
		body, err := json.Marshal(fields)
		if err != nil {
			panic(err)
		}
		req = jsonRequest(method, target, string(body))
	}
	req.Header.Set("X-Test-User", "yes")
	return req
}

func (w *nodeConfigWorld) optionsGET(node string) *http.Request {
	return w.request(http.MethodGet, nodeOptionsPath, node, nil)
}

func (w *nodeConfigWorld) notesGET(node string) *http.Request {
	return w.request(http.MethodGet, nodeNotesPath, node, nil)
}

func (w *nodeConfigWorld) acmeGET(node string) *http.Request {
	return w.request(http.MethodGet, acmeNodeScope+"/acme-config", node, nil)
}

func (w *nodeConfigWorld) optionsPUT(node string, fields map[string]any) *http.Request {
	return w.request(http.MethodPut, nodeOptionsPath, node, fields)
}

func (w *nodeConfigWorld) acmePUT(node string, fields map[string]any) *http.Request {
	return w.request(http.MethodPut, acmeNodeScope+"/acme-config", node, fields)
}

func (w *nodeConfigWorld) send(t *testing.T, req *http.Request) (int, []byte) {
	t.Helper()
	return nodeOptionsSend(t, w.app, req)
}

// tokenFrom makes a read and returns the save token it hands out.
func (w *nodeConfigWorld) tokenFrom(t *testing.T, req *http.Request) string {
	t.Helper()
	status, body := w.send(t, req)
	if status != fiber.StatusOK {
		t.Fatalf("%s %s: status = %d (%s), want 200", req.Method, req.URL.Path, status, clipForFailure(string(body), 300))
	}
	got, _ := decodeKeys(t, body)
	token, _ := got["digest"].(string)
	if token == "" {
		t.Fatalf("%s %s returned no digest: %s", req.Method, req.URL.Path, clipForFailure(string(body), 300))
	}
	return token
}

// nodeConfigWriter is one of the two routes that save the node config file, with
// a read that hands out the token for it, a save it accepts, and one it refuses
// itself.
type nodeConfigWriter struct {
	name  string
	read  func(w *nodeConfigWorld, node string) *http.Request
	write func(w *nodeConfigWorld, node string, fields map[string]any) *http.Request
	// change is a save the route accepts and that changes the file.
	change map[string]any
	// refused is a save the route refuses itself, with refusedMessage in the answer.
	refused        map[string]any
	refusedMessage string
	// auditType and auditAction are the one audit row a save that went through
	// writes.
	auditType, auditAction string
}

var nodeConfigWriters = []nodeConfigWriter{
	{
		name:           "the options",
		read:           (*nodeConfigWorld).optionsGET,
		write:          (*nodeConfigWorld).optionsPUT,
		change:         map[string]any{"ballooning-target": 80},
		refused:        map[string]any{"ballooning-target": 0, "delete": []string{"ballooning-target"}},
		refusedMessage: "set and cleared",
		auditType:      "node",
		auditAction:    "set_options",
	},
	{
		name:           "the ACME settings",
		read:           (*nodeConfigWorld).acmeGET,
		write:          (*nodeConfigWorld).acmePUT,
		change:         map[string]any{"acmedomain1": "other.example.com"},
		refused:        map[string]any{"acmedomain0": "node.example.com", "delete": []string{"acmedomain0"}},
		refusedMessage: "set and cleared",
		auditType:      "acme_config",
		auditAction:    "updated",
	},
}

// withDigest is fields with the save token added as `digest`.
func withDigest(fields map[string]any, token string) map[string]any {
	out := maps.Clone(fields)
	out["digest"] = token
	return out
}

// TestNodeConfigReadsReturnTheSaveToken: every read of the file returns the token
// where it used to return Proxmox's digest, and nothing of Proxmox's digest, in any
// form, comes out. The three reads are for one file, so they hand out one token: a
// token read through the notes saves through either PUT.
func TestNodeConfigReadsReturnTheSaveToken(t *testing.T) {
	reads := []struct {
		name string
		req  func(w *nodeConfigWorld, node string) *http.Request
	}{
		{"the options", (*nodeConfigWorld).optionsGET},
		{"the notes", (*nodeConfigWorld).notesGET},
		{"the ACME settings", (*nodeConfigWorld).acmeGET},
	}
	newWorld := func(t *testing.T) (*nodeConfigWorld, *nodeConfigPVE, string) {
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
		raw := pve.digestOf(testNodeName)
		if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(raw) {
			t.Fatalf("the stand-in's digest %q is not a SHA1 in hex", raw)
		}
		return newNodeConfigWorld(t, pve, nodeConfigWorldOptions{}), pve, raw
	}

	t.Run("a read returns the token and none of Proxmox's digest, and the three agree", func(t *testing.T) {
		w, _, raw := newWorld(t)
		tokens := make([]string, 0, len(reads))
		for _, r := range reads {
			status, body := w.send(t, r.req(w, testNodeName))
			if status != fiber.StatusOK {
				t.Fatalf("%s: status = %d (%s), want 200", r.name, status, clipForFailure(string(body), 300))
			}
			tokens = append(tokens, requireSaveToken(t, body, raw))
		}
		if tokens[0] != tokens[1] || tokens[1] != tokens[2] {
			t.Errorf("the three reads of one file returned %q: they share one compare-and-swap, so they share one token", tokens)
		}
	})

	t.Run("the same file reads as the same token, and a changed one as another", func(t *testing.T) {
		w, pve, _ := newWorld(t)
		first := w.tokenFrom(t, w.optionsGET(testNodeName))
		if again := w.tokenFrom(t, w.optionsGET(testNodeName)); again != first {
			t.Errorf("a second read of an unchanged file returned %q, then %q: a token that moved on its own could never be saved with", first, again)
		}
		pve.setNotes(testNodeName, "other notes\n")
		if changed := w.tokenFrom(t, w.optionsGET(testNodeName)); changed == first {
			t.Errorf("the token is %q before and after the file changed: it would not notice the change", first)
		}
	})

	t.Run("a node with no config file is handed no token", func(t *testing.T) {
		pve := newNodeConfigPVE(t)
		w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
		for _, r := range reads {
			status, body := w.send(t, r.req(w, testNodeName))
			if status != fiber.StatusOK {
				t.Fatalf("%s: status = %d (%s), want 200", r.name, status, clipForFailure(string(body), 300))
			}
			if string(body) != `{}` {
				t.Errorf("%s: body = %s, want {}: a token for no file would be one to send back for nothing", r.name, body)
			}
		}
	})

	t.Run("the token fits the parameter that carries it back", func(t *testing.T) {
		w, _, _ := newWorld(t)
		token := w.tokenFrom(t, w.optionsGET(testNodeName))
		for _, e := range []Endpoint{
			declaredEndpoint(t, fiber.MethodPut, nodeOptionsPath),
			declaredEndpoint(t, fiber.MethodPut, acmeNodeScope+"/acme-config"),
		} {
			bound := e.Parameters["digest"].MaxLength
			if bound == nil || len(token) > *bound {
				t.Errorf("PUT %s declares the digest bound %v, and the token is %d characters", e.Path, bound, len(token))
			}
		}
	})
}

// TestNodeConfigTokenIsBoundToItsClusterNodeAndKey: the same file, read under
// another cluster, another node or another ENCRYPTION_KEY, gives another token. This
// is what makes a token a statement about one node of one cluster, and not about a
// file anybody can produce a copy of.
func TestNodeConfigTokenIsBoundToItsClusterNodeAndKey(t *testing.T) {
	pve := newNodeConfigPVE(t)
	// Two nodes holding the same file, and so the same digest: the node name is all
	// that tells their tokens apart.
	pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
	pve.setFile(otherTestNodeName, nodeConfigNotes, nodeConfigSettings())
	if pve.digestOf(testNodeName) != pve.digestOf(otherTestNodeName) {
		t.Fatal("the two nodes' files differ, so the case does not isolate the node name")
	}

	base := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
	otherCluster := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{clusterID: otherTestClusterID})
	otherKey := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{key: otherTestEncKey})

	tokens := []struct{ name, token string }{
		{"the node, the cluster and the key of the base case", base.tokenFrom(t, base.optionsGET(testNodeName))},
		{"another node", base.tokenFrom(t, base.optionsGET(otherTestNodeName))},
		{"another cluster", otherCluster.tokenFrom(t, otherCluster.optionsGET(testNodeName))},
		{"another ENCRYPTION_KEY", otherKey.tokenFrom(t, otherKey.optionsGET(testNodeName))},
	}
	for i, a := range tokens {
		for _, b := range tokens[i+1:] {
			if a.token == b.token {
				t.Errorf("%s and %s gave the same token, %s: it is not bound to what tells them apart", a.name, b.name, a.token)
			}
		}
	}

	// Within one world the routes agree, whichever of them is read: it is the one
	// file's token.
	for name, w := range map[string]*nodeConfigWorld{"the base case": base, "another cluster": otherCluster, "another key": otherKey} {
		if opt, acme := w.tokenFrom(t, w.optionsGET(testNodeName)), w.tokenFrom(t, w.acmeGET(testNodeName)); opt != acme {
			t.Errorf("%s: the options read gave %q and the ACME read %q: one file, one token", name, opt, acme)
		}
	}
}

// TestNodeConfigTokenIsForWriters follows the token through GET .../options and GET
// .../acme-config for every kind of caller. A Viewer reads the settings and no
// digest at all — the key is absent, not empty, so the page cannot mistake a
// missing token for one to send back — and a manager reads the token. A caller
// holding the write permission of another resource is not a manager of this one,
// and a permission lookup that fails fails the request, before anything reaches
// Proxmox, rather than answering without the token: that would hand a manager's
// next save no token to send, and an unconditional write is the compare-and-swap
// failing open.
//
// The token reveals nothing about the file, so withholding it from a Viewer is not
// what keeps the notes safe any more. It stays, as defence in depth: a token that
// changed whenever the file did would still tell a Viewer, for free, that a node's
// notes had changed. And Proxmox's own digest is in nobody's answer.
func TestNodeConfigTokenIsForWriters(t *testing.T) {
	for _, rd := range []struct {
		name     string
		read     func(w *nodeConfigWorld, node string) *http.Request
		view     string
		manage   string
		other    string
		visible  []string
		answered string
	}{
		{"the options", (*nodeConfigWorld).optionsGET, "view:node", "manage:node", "manage:certificate",
			[]string{"ballooning-target", "wakeonlan"}, "ballooning"},
		{"the ACME settings", (*nodeConfigWorld).acmeGET, "view:certificate", "manage:certificate", "manage:node",
			[]string{"acme", "acmedomain0"}, "acmedomain0"},
	} {
		t.Run(rd.name, func(t *testing.T) {
			for _, tt := range []struct {
				name      string
				grants    []string
				wantToken bool
			}{
				{"a Viewer reads the settings and no digest", []string{rd.view}, false},
				{"a manager reads the settings and the token", []string{rd.view, rd.manage}, true},
				{"the write permission of another resource is not this one's", []string{rd.view, rd.other}, false},
				{"viewing is not managing, whatever else is held", []string{rd.view, "view:audit", "view:vm"}, false},
			} {
				t.Run(tt.name, func(t *testing.T) {
					pve := newNodeConfigPVE(t)
					pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
					w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: stubAuth(nodeOptionsGrants(tt.grants...))})

					status, body := w.send(t, rd.read(w, testNodeName))
					if status != fiber.StatusOK {
						t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
					}
					got, keys := decodeKeys(t, body)
					want := slices.Clone(rd.visible)
					if tt.wantToken {
						want = append(want, "digest")
					}
					slices.Sort(want)
					if !slices.Equal(keys, want) {
						t.Errorf("the body carries the keys %v, want exactly %v: %s", keys, want, body)
					}
					if tt.wantToken {
						requireSaveToken(t, body, pve.digestOf(testNodeName))
						return
					}
					if _, present := got["digest"]; present {
						t.Errorf("the body has a digest key for a caller who cannot write: %s", body)
					}
					requireNoRawDigest(t, body, pve.digestOf(testNodeName))
				})
			}

			t.Run("a permission lookup that fails fails the request, and nothing reaches Proxmox", func(t *testing.T) {
				// The view permission passes the declared gate; it is the handler's own
				// look at the write permission that fails.
				engine := &failingRBACEngine{grants: map[string]bool{rd.view: true}, failOn: rd.manage}
				pve := newNodeConfigPVE(t)
				pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
				w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: authWithEngine(engine)})

				status, body := w.send(t, rd.read(w, testNodeName))
				if status != fiber.StatusInternalServerError {
					t.Fatalf("status = %d (%s), want 500: a lookup that failed must not be answered as if it said no",
						status, clipForFailure(string(body), 300))
				}
				if !strings.Contains(string(body), "Permission check failed") {
					t.Errorf("the answer %s does not say the permission check failed", clipForFailure(string(body), 300))
				}
				if strings.Contains(string(body), "digest") || strings.Contains(string(body), rd.answered) {
					t.Errorf("the failed request still carried the config: %s", body)
				}
				if sent := pve.requests(); len(sent) != 0 {
					t.Errorf("the permission lookup failed and %d request(s) still reached Proxmox: %+v", len(sent), sent)
				}
			})
		})
	}

	t.Run("the notes read hands out the token, since its gate is the writer's permission", func(t *testing.T) {
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
		w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: stubAuth(nodeOptionsGrants("manage:node"))})

		status, body := w.send(t, w.notesGET(testNodeName))
		if status != fiber.StatusOK {
			t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		requireSaveToken(t, body, pve.digestOf(testNodeName))
	})

	t.Run("the declared gate still refuses a caller who cannot view certificates", func(t *testing.T) {
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
		w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: stubAuth(nodeOptionsGrants("view:node", "manage:certificate"))})

		status, body := w.send(t, w.acmeGET(testNodeName))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d (%s), want 403", status, clipForFailure(string(body), 300))
		}
		if sent := pve.requests(); len(sent) != 0 {
			t.Errorf("a refused read reached Proxmox: %+v", sent)
		}
	})
}

// TestNodeConfigTokenPermissionIsScopedToThePathsCluster holds the permission the
// token is shown under to the cluster in the path. The tests above run behind
// stubRBACEngine, which answers a cluster-scoped lookup and a global one from the
// same map, so a handler that asked the global scope, or asked about no cluster at
// all, would pass every one of them: a user granted manage:node on one cluster only
// would have the token of every node of every cluster — or none of their own.
//
// The engine here answers as the real one does. A grant at the path's cluster
// shows the token; a grant at another cluster, or none, does not; a global grant
// does, as the cluster-scoped lookup honours it; and a permission that only the
// global lookup finds does not, which pins WHICH lookup the handler makes.
func TestNodeConfigTokenPermissionIsScopedToThePathsCluster(t *testing.T) {
	pathCluster := uuid.MustParse(testClusterID)
	otherCluster := uuid.MustParse(otherTestClusterID)

	for _, rd := range []struct {
		name   string
		read   func(w *nodeConfigWorld, node string) *http.Request
		view   string
		manage string
	}{
		{"the options", (*nodeConfigWorld).optionsGET, "view:node", "manage:node"},
		{"the ACME settings", (*nodeConfigWorld).acmeGET, "view:certificate", "manage:certificate"},
	} {
		t.Run(rd.name, func(t *testing.T) {
			view := scopedGrant{permission: rd.view}
			for _, tt := range []struct {
				name       string
				grants     []scopedGrant
				globalOnly []string
				wantToken  bool
			}{
				{"manage held at the path's cluster only", []scopedGrant{view, {rd.manage, pathCluster}}, nil, true},
				{"manage held at another cluster only", []scopedGrant{view, {rd.manage, otherCluster}}, nil, false},
				{"manage held globally", []scopedGrant{view, {permission: rd.manage}}, nil, true},
				{"manage that only a global-scope lookup would find", []scopedGrant{view}, []string{rd.manage}, false},
				{"no manage at all", []scopedGrant{view}, nil, false},
			} {
				t.Run(tt.name, func(t *testing.T) {
					engine := &scopedRBACEngine{grants: tt.grants, globalOnly: tt.globalOnly}
					pve := newNodeConfigPVE(t)
					pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
					w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: authWithEngine(engine)})

					status, body := w.send(t, rd.read(w, testNodeName))
					if status != fiber.StatusOK {
						t.Fatalf("status = %d (%s), want 200: the settings are the Viewer's whatever the token is",
							status, clipForFailure(string(body), 300))
					}
					got, _ := decodeKeys(t, body)
					_, hasToken := got["digest"]
					if hasToken != tt.wantToken {
						t.Errorf("token present = %v, want %v: %s", hasToken, tt.wantToken, clipForFailure(string(body), 300))
					}
					requireNoRawDigest(t, body, pve.digestOf(testNodeName))

					// Which question was asked is part of the answer: the writer's permission,
					// at the cluster scope, about the cluster in the path.
					asked := engine.asked(rd.manage)
					if len(asked) == 0 {
						t.Fatalf("the handler never looked up %s, so it decided without it", rd.manage)
					}
					for _, l := range asked {
						if l.global || l.scopeType != "cluster" || l.scopeID != pathCluster {
							t.Errorf("the handler looked up %s as %+v, want the cluster scope and the path's cluster %s", rd.manage, l, pathCluster)
						}
					}
				})
			}
		})
	}
}

// TestNodeConfigSaveWithTheToken: a save that sends back the token a read returned
// is checked against the file as it is now, and what goes to Proxmox is the file's
// digest as it is now, in Proxmox's own form — so that Proxmox's own check still
// covers the moment between Nexara's read and its write. Neither the token nor the
// digest is in the audit row, which every Viewer reads.
func TestNodeConfigSaveWithTheToken(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		t.Run(wr.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})

			token := w.tokenFrom(t, wr.read(w, testNodeName))
			rawBefore := pve.digestOf(testNodeName)
			mark := pve.mark()

			status, body := w.send(t, wr.write(w, testNodeName, withDigest(wr.change, token)))
			if status != fiber.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
			}

			sent := pve.since(mark)
			if len(sent) != 2 || sent[0].method != http.MethodGet || sent[1].method != http.MethodPut {
				t.Fatalf("Proxmox received %+v, want the digest's read and then the save", sent)
			}
			for _, r := range sent {
				if r.path != nodeOptionsPVEPath {
					t.Errorf("%s %s: the node config is at %s", r.method, r.path, nodeOptionsPVEPath)
				}
			}
			form := sent[1].form
			if got := form.Get("digest"); got != rawBefore {
				t.Errorf("Proxmox was sent the digest %q, want the file's own, %q: the token must not reach it, and neither may nothing",
					got, rawBefore)
			}
			wantKeys := []string{"digest"}
			for k := range wr.change {
				wantKeys = append(wantKeys, k)
			}
			slices.Sort(wantKeys)
			if got := slices.Sorted(maps.Keys(form)); !slices.Equal(got, wantKeys) {
				t.Errorf("Proxmox was sent the keys %v, want %v", got, wantKeys)
			}
			if got := pve.digestOf(testNodeName); got == rawBefore {
				t.Error("the file is unchanged after a save that went through")
			}

			row := oneAccessAuditRow(t, w.store.auditRows())
			if row.resourceType != wr.auditType || row.action != wr.auditAction || row.resourceID != testNodeName {
				t.Errorf("the audit row is (%q, %q, %q), want (%s, %s, %s)",
					row.resourceType, row.resourceID, row.action, wr.auditType, testNodeName, wr.auditAction)
			}
			for _, secret := range []string{token, rawBefore, pve.digestOf(testNodeName)} {
				if strings.Contains(string(row.details), secret) {
					t.Errorf("the audit details %s carry %q, which view:audit would show every Viewer", row.details, secret)
				}
			}
		})
	}

	t.Run("a token read through the notes saves through either route", func(t *testing.T) {
		for _, wr := range nodeConfigWriters {
			t.Run(wr.name, func(t *testing.T) {
				pve := newNodeConfigPVE(t)
				pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
				w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})

				token := w.tokenFrom(t, w.notesGET(testNodeName))
				status, body := w.send(t, wr.write(w, testNodeName, withDigest(wr.change, token)))
				if status != fiber.StatusOK {
					t.Fatalf("status = %d (%s), want 200: the three routes are one file's compare-and-swap",
						status, clipForFailure(string(body), 300))
				}
			})
		}
	})

	t.Run("a save through one route moves the file under every token read before it", func(t *testing.T) {
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
		w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})

		token := w.tokenFrom(t, w.notesGET(testNodeName))
		first, second := nodeConfigWriters[0], nodeConfigWriters[1]
		if status, body := w.send(t, first.write(w, testNodeName, withDigest(first.change, token))); status != fiber.StatusOK {
			t.Fatalf("the first save: status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		mark := pve.mark()
		status, body := w.send(t, second.write(w, testNodeName, withDigest(second.change, token)))
		if status != fiber.StatusConflict {
			t.Fatalf("the second save with the same token: status = %d (%s), want 409", status, clipForFailure(string(body), 300))
		}
		if sent := pve.since(mark); len(sent) != 1 || sent[0].method != http.MethodGet {
			t.Errorf("Proxmox received %+v, want only the read of the digest", sent)
		}
	})
}

// TestNodeConfigSaveRefusesATokenThatIsNotTheFiles: whatever is sent as the digest
// that is not the token for THIS node of THIS cluster, under THIS key, for the file
// as it is NOW, is a 409 in the words of Proxmox's own refusal, with nothing
// written and no audit row — and the only request that reached Proxmox is the read
// of the file's digest. That includes Proxmox's own digest, which no read of
// Nexara's returns, so a caller that has one from elsewhere can no longer use it to
// probe the file: every guess is the same 409.
func TestNodeConfigSaveRefusesATokenThatIsNotTheFiles(t *testing.T) {
	type world struct {
		pve   *nodeConfigPVE
		w     *nodeConfigWorld
		wr    nodeConfigWriter
		token string
	}
	for _, tt := range []struct {
		name string
		// target is the node the save is sent for; testNodeName when empty.
		target string
		// via is the world the save is sent through, the one the token was read in
		// when nil.
		via func(t *testing.T, x world) *nodeConfigWorld
		// digest returns what the save sends, given a world whose file holds the
		// settings and the notes, before the file may be changed.
		digest func(t *testing.T, x world) string
	}{
		{name: "Proxmox's own digest of the file", digest: func(_ *testing.T, x world) string { return x.pve.digestOf(testNodeName) }},
		{name: "the token of another node holding the same file", digest: func(t *testing.T, x world) string {
			x.pve.setFile(otherTestNodeName, nodeConfigNotes, nodeConfigSettings())
			return x.w.tokenFrom(t, x.wr.read(x.w, otherTestNodeName))
		}},
		// And the other way round: a token read for this node, sent for another one
		// that holds the same file. A check that compared against one fixed node, and
		// not the one in the path, would pass the case above and fail this one.
		{name: "the token of this node sent for another node holding the same file", target: otherTestNodeName,
			digest: func(t *testing.T, x world) string {
				x.pve.setFile(otherTestNodeName, nodeConfigNotes, nodeConfigSettings())
				return x.token
			}},
		{name: "the token of another cluster for the same node and file", digest: func(t *testing.T, x world) string {
			other := newNodeConfigWorld(t, x.pve, nodeConfigWorldOptions{clusterID: otherTestClusterID})
			return other.tokenFrom(t, x.wr.read(other, testNodeName))
		}},
		{name: "the token of this cluster sent through another cluster for the same node and file",
			via: func(t *testing.T, x world) *nodeConfigWorld {
				return newNodeConfigWorld(t, x.pve, nodeConfigWorldOptions{clusterID: otherTestClusterID})
			},
			digest: func(_ *testing.T, x world) string { return x.token }},
		{name: "a token made under another ENCRYPTION_KEY", digest: func(t *testing.T, x world) string {
			other := newNodeConfigWorld(t, x.pve, nodeConfigWorldOptions{key: otherTestEncKey})
			return other.tokenFrom(t, x.wr.read(other, testNodeName))
		}},
		{name: "a token made under this ENCRYPTION_KEY sent through another one",
			via: func(t *testing.T, x world) *nodeConfigWorld {
				return newNodeConfigWorld(t, x.pve, nodeConfigWorldOptions{key: otherTestEncKey})
			},
			digest: func(_ *testing.T, x world) string { return x.token }},
		{name: "the token of the file as it was before it changed", digest: func(t *testing.T, x world) string {
			token := x.token
			x.pve.setNotes(testNodeName, "edited elsewhere\n")
			return token
		}},
		{name: "the token of a version that does not exist", digest: func(_ *testing.T, x world) string { return "v2." + strings.TrimPrefix(x.token, "v1.") }},
		{name: "the token without its version", digest: func(_ *testing.T, x world) string { return strings.TrimPrefix(x.token, "v1.") }},
		{name: "the token with its last character cut", digest: func(_ *testing.T, x world) string { return x.token[:len(x.token)-1] }},
		{name: "the token with a character added", digest: func(_ *testing.T, x world) string { return x.token + "A" }},
		{name: "the token with a space in front", digest: func(_ *testing.T, x world) string { return " " + x.token }},
		{name: "the token with a line break after", digest: func(_ *testing.T, x world) string { return x.token + "\n" }},
		{name: "the token in capitals", digest: func(_ *testing.T, x world) string { return strings.ToUpper(x.token) }},
		{name: "the version and nothing else", digest: func(_ *testing.T, _ world) string { return "v1." }},
		{name: "43 characters of the right alphabet that are no tag", digest: func(_ *testing.T, _ world) string { return "v1." + strings.Repeat("A", 43) }},
		{name: "a string that is not a token at all", digest: func(_ *testing.T, _ world) string { return "not-a-token" }},
		{name: "the longest string the parameter allows", digest: func(_ *testing.T, _ world) string { return strings.Repeat("a", 128) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, wr := range nodeConfigWriters {
				t.Run(wr.name, func(t *testing.T) {
					pve := newNodeConfigPVE(t)
					pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
					w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
					x := world{pve: pve, w: w, wr: wr, token: w.tokenFrom(t, wr.read(w, testNodeName))}

					sender, node := w, cmp.Or(tt.target, testNodeName)
					if tt.via != nil {
						sender = tt.via(t, x)
					}
					digest := tt.digest(t, x)
					fileBefore, mark := pve.digestOf(node), pve.mark()

					status, body := sender.send(t, wr.write(sender, node, withDigest(wr.change, digest)))
					if status != fiber.StatusConflict {
						t.Fatalf("status = %d (%s), want 409", status, clipForFailure(string(body), 300))
					}
					var env ErrorResponse
					if err := json.Unmarshal(body, &env); err != nil || env.Message != nodeConfigChangedAnswer {
						t.Errorf("the answer is %s, want the message %q: the same words Proxmox's own refusal gets",
							clipForFailure(string(body), 300), nodeConfigChangedAnswer)
					}
					sent := pve.since(mark)
					if len(sent) != 1 || sent[0].method != http.MethodGet || sent[0].path != "/api2/json/nodes/"+node+"/config" {
						t.Errorf("Proxmox received %+v, want only the read of the digest of %s: a refused token must not reach the write", sent, node)
					}
					if got := pve.digestOf(node); got != fileBefore {
						t.Error("the file changed under a save that was refused")
					}
					if rows := sender.store.auditRows(); len(rows) != 0 {
						t.Errorf("a save that did not happen wrote %d audit row(s), want none", len(rows))
					}
				})
			}
		})
	}
}

// TestNodeConfigSaveOfANodeWithNoConfigFile: a token is a statement about a file,
// and a node with none has no digest to make one of. So a save that names a token
// for it is refused, whatever the token, and a save that names none creates the
// file, after which the node has a digest and the reads hand out a token.
func TestNodeConfigSaveOfANodeWithNoConfigFile(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		t.Run(wr.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			// Another node's file gives a well-formed token, for a cluster and a key
			// that are the right ones, and a node that is not.
			pve.setFile(otherTestNodeName, nodeConfigNotes, nodeConfigSettings())
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			token := w.tokenFrom(t, wr.read(w, otherTestNodeName))

			mark := pve.mark()
			status, body := w.send(t, wr.write(w, testNodeName, withDigest(wr.change, token)))
			if status != fiber.StatusConflict {
				t.Fatalf("status = %d (%s), want 409", status, clipForFailure(string(body), 300))
			}
			if sent := pve.since(mark); len(sent) != 1 || sent[0].method != http.MethodGet {
				t.Errorf("Proxmox received %+v, want only the read of the digest", sent)
			}
			if got := pve.digestOf(testNodeName); got != "" {
				t.Error("the save that was refused created the file")
			}

			status, body = w.send(t, wr.write(w, testNodeName, wr.change))
			if status != fiber.StatusOK {
				t.Fatalf("the first save, with no token: status = %d (%s), want 200", status, clipForFailure(string(body), 300))
			}
			requireSaveToken(t, mustRead(t, w, wr.read(w, testNodeName)), pve.digestOf(testNodeName))
		})
	}
}

// mustRead makes a read that has to succeed and returns its body.
func mustRead(t *testing.T, w *nodeConfigWorld, req *http.Request) []byte {
	t.Helper()
	status, body := w.send(t, req)
	if status != fiber.StatusOK {
		t.Fatalf("%s %s: status = %d (%s), want 200", req.Method, req.URL.Path, status, clipForFailure(string(body), 300))
	}
	return body
}

// TestNodeConfigSaveWithoutATokenIsUnconditional: no digest, or an empty one, is a
// save that is not based on a read, as it always was: one request, the write, no
// read of the digest first, and no digest in what Proxmox is sent.
func TestNodeConfigSaveWithoutATokenIsUnconditional(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		for _, tt := range []struct {
			name   string
			fields map[string]any
		}{
			{"no digest", wr.change},
			{"an empty digest", withDigest(wr.change, "")},
		} {
			t.Run(wr.name+", "+tt.name, func(t *testing.T) {
				pve := newNodeConfigPVE(t)
				pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
				w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})

				// The file has moved on since anybody read it, which an unconditional
				// save does not care about.
				pve.setNotes(testNodeName, "edited elsewhere\n")
				status, body := w.send(t, wr.write(w, testNodeName, tt.fields))
				if status != fiber.StatusOK {
					t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
				}
				sent := pve.requests()
				if len(sent) != 1 || sent[0].method != http.MethodPut {
					t.Fatalf("Proxmox received %+v, want exactly the one write", sent)
				}
				if _, has := sent[0].form["digest"]; has {
					t.Errorf("the write carries a digest, %v: nothing was asked to be compared", sent[0].form["digest"])
				}
				if rows := w.store.auditRows(); len(rows) != 1 {
					t.Errorf("the save wrote %d audit rows, want 1", len(rows))
				}
			})
		}
	}
}

// TestNodeConfigProxmoxsOwnCheckStillCatchesAFileThatMovesAfterTheTokenMatched: the
// token is checked against a read, and the write follows it; a change that lands
// between the two is what Proxmox's assert_if_modified is for, which is why the
// FRESH digest is sent and not nothing. It answers with its plain 500, which is
// mapped to the same 409 in the same words, and nothing is audited.
func TestNodeConfigProxmoxsOwnCheckStillCatchesAFileThatMovesAfterTheTokenMatched(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		t.Run(wr.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})

			token := w.tokenFrom(t, wr.read(w, testNodeName))
			// The token's read was the first GET, so the handler's read of the digest
			// is the second: the file changes the moment after it has been answered.
			pve.changeAfterRead(2, func(files map[string]*nodeConfigFile) {
				files[testNodeName].notes = "edited elsewhere\n"
			})
			mark := pve.mark()

			status, body := w.send(t, wr.write(w, testNodeName, withDigest(wr.change, token)))
			if status != fiber.StatusConflict {
				t.Fatalf("status = %d (%s), want 409", status, clipForFailure(string(body), 300))
			}
			var env ErrorResponse
			if err := json.Unmarshal(body, &env); err != nil || env.Message != nodeConfigChangedAnswer {
				t.Errorf("the answer is %s, want the message %q", clipForFailure(string(body), 300), nodeConfigChangedAnswer)
			}
			sent := pve.since(mark)
			if len(sent) != 2 || sent[0].method != http.MethodGet || sent[1].method != http.MethodPut {
				t.Fatalf("Proxmox received %+v, want the read of the digest and then the write it refused", sent)
			}
			if sent[1].form.Get("digest") == "" {
				t.Error("the write carried no digest, so Proxmox had nothing to refuse it by")
			}
			if rows := w.store.auditRows(); len(rows) != 0 {
				t.Errorf("a save Proxmox refused wrote %d audit row(s), want none", len(rows))
			}
		})
	}
}

// TestNodeConfigSaveWhenTheDigestCannotBeRead: the save check reads the file's digest
// from Proxmox, and when Proxmox will not answer that read the save is not made. The
// failure is the read's own, in the words every other read of the node config gets —
// a token without Sys.Audit is a 403 that names it, an unreachable node a gateway
// failure — and not a 409 that a caller would answer by reloading, and not a write
// that went ahead for want of something to compare.
func TestNodeConfigSaveWhenTheDigestCannotBeRead(t *testing.T) {
	for _, tt := range []struct {
		name        string
		status      int
		body        string
		wantStatus  int
		wantMessage string
	}{
		{"a token without Sys.Audit", http.StatusForbidden, `Permission check failed (/, Sys.Audit)`, fiber.StatusForbidden, "Sys.Audit"},
		{"an unreachable node", http.StatusInternalServerError, `{"data":null,"message":"hostname lookup failed - no route to node\n"}`, fiber.StatusBadGateway, ""},
	} {
		for _, wr := range nodeConfigWriters {
			t.Run(tt.name+", "+wr.name, func(t *testing.T) {
				pve := newNodeConfigPVE(t)
				pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
				w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
				token := w.tokenFrom(t, wr.read(w, testNodeName))
				pve.failReads(tt.status, tt.body)
				fileBefore, mark := pve.digestOf(testNodeName), pve.mark()

				status, body := w.send(t, wr.write(w, testNodeName, withDigest(wr.change, token)))
				if status != tt.wantStatus {
					t.Fatalf("status = %d (%s), want %d", status, clipForFailure(string(body), 300), tt.wantStatus)
				}
				if tt.wantMessage != "" && !strings.Contains(string(body), tt.wantMessage) {
					t.Errorf("the answer %s does not say %q", clipForFailure(string(body), 300), tt.wantMessage)
				}
				if strings.Contains(string(body), nodeConfigChangedAnswer) {
					t.Errorf("the answer %s is the stale-file 409, which a caller would answer by reloading", clipForFailure(string(body), 300))
				}
				if sent := pve.since(mark); len(sent) != 1 || sent[0].method != http.MethodGet {
					t.Errorf("Proxmox received %+v, want only the read that was refused", sent)
				}
				if got := pve.digestOf(testNodeName); got != fileBefore {
					t.Error("the file changed under a save whose check could not be made")
				}
				if rows := w.store.auditRows(); len(rows) != 0 {
					t.Errorf("a save that did not happen wrote %d audit row(s), want none", len(rows))
				}
			})
		}
	}
}

// TestNodeConfigSaveRefusalsCostNoRead: what Nexara can refuse about the request
// itself — a key set and cleared, a key that cannot be cleared, a save that changes
// nothing, a control character — is refused before anything is sent, with a token
// or without, so a request that is going to fail does not cost Proxmox a read.
func TestNodeConfigSaveRefusalsCostNoRead(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		t.Run(wr.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			token := w.tokenFrom(t, wr.read(w, testNodeName))
			mark := pve.mark()

			status, body := w.send(t, wr.write(w, testNodeName, withDigest(wr.refused, token)))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%s), want 400", status, clipForFailure(string(body), 300))
			}
			if !strings.Contains(string(body), wr.refusedMessage) {
				t.Errorf("the answer %s does not say %q", clipForFailure(string(body), 300), wr.refusedMessage)
			}
			if sent := pve.since(mark); len(sent) != 0 {
				t.Errorf("a refusal Nexara makes reached Proxmox: %+v", sent)
			}
			if rows := w.store.auditRows(); len(rows) != 0 {
				t.Errorf("wrote %d audit row(s), want none", len(rows))
			}
		})
	}

	// The options route has more of its own.
	for _, tt := range []struct {
		name    string
		fields  map[string]any
		message string
	}{
		{"a save that changes nothing", map[string]any{}, "nothing to change"},
		{"a control character in a location", map[string]any{"location": "latitude=0,longitude=0,name=rack\u001b01"}, "control character"},
		{"an ACME key to clear", map[string]any{"delete": []string{"acmedomain0"}}, "ACME"},
	} {
		t.Run("the options: "+tt.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			token := w.tokenFrom(t, w.optionsGET(testNodeName))
			mark := pve.mark()

			status, body := w.send(t, w.optionsPUT(testNodeName, withDigest(tt.fields, token)))
			if status != fiber.StatusBadRequest || !strings.Contains(string(body), tt.message) {
				t.Fatalf("status = %d (%s), want a 400 saying %q", status, clipForFailure(string(body), 300), tt.message)
			}
			if sent := pve.since(mark); len(sent) != 0 {
				t.Errorf("a refusal Nexara makes reached Proxmox: %+v", sent)
			}
		})
	}

	t.Run("the ACME settings: a key that is no ACME setting cannot be cleared", func(t *testing.T) {
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
		w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
		token := w.tokenFrom(t, w.acmeGET(testNodeName))
		mark := pve.mark()

		status, body := w.send(t, w.acmePUT(testNodeName, withDigest(map[string]any{"delete": []string{"wakeonlan"}}, token)))
		if status != fiber.StatusBadRequest {
			t.Fatalf("status = %d (%s), want 400", status, clipForFailure(string(body), 300))
		}
		if sent := pve.since(mark); len(sent) != 0 {
			t.Errorf("a refusal Nexara makes reached Proxmox: %+v", sent)
		}
	})
}

// TestNodeConfigACMESaveRefusalsComeAfterTheClusterIsResolved is
// TestNodeOptionsRefusalsComeAfterTheClusterIsResolved for the ACME settings' PUT:
// its refusals, and the read of the digest its token takes, sit behind
// createProxmoxClient. The route sweep (registry_route_sweep_test.go) sends every
// declared parameter at once, `delete` included, and expects the handler to get as
// far as its dependencies before anything fails; a refusal ahead of the cluster
// lookup would answer it with a 400 that has nothing to do with the registry.
func TestNodeConfigACMESaveRefusalsComeAfterTheClusterIsResolved(t *testing.T) {
	token := "v1." + strings.Repeat("A", 43)
	for _, tt := range []struct {
		name   string
		fields map[string]any
	}{
		{"set and cleared", map[string]any{"acmedomain0": "node.example.com", "delete": []string{"acmedomain0"}}},
		{"a key that is no ACME setting", map[string]any{"delete": []string{"wakeonlan"}}},
		{"a save token on an ordinary save", map[string]any{"acmedomain0": "node.example.com", "digest": token}},
		// A write that changes nothing is the client's refusal too, and so is made
		// after the cluster is resolved: the route sweep's required-only request is
		// exactly this one.
		{"nothing at all", map[string]any{}},
		{"a save token alone", map[string]any{"digest": token}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			w.store.failClusterRead(1, pgx.ErrNoRows)

			status, body := w.send(t, w.acmePUT(testNodeName, tt.fields))
			if status != fiber.StatusNotFound {
				t.Fatalf("status = %d (%s), want the cluster's 404 — a 400 means a refusal now runs before the cluster is resolved",
					status, clipForFailure(string(body), 300))
			}
			if sent := pve.requests(); len(sent) != 0 {
				t.Errorf("%d request(s) reached Proxmox: %+v", len(sent), sent)
			}
			if rows := w.store.auditRows(); len(rows) != 0 {
				t.Errorf("wrote %d audit row(s), want none", len(rows))
			}
		})
	}
}

// TestNodeConfigACMESaveOfNothingIsRefused: a PUT .../acme-config that sets nothing
// and clears nothing — a body of nothing, fields left empty (which means "leave
// alone"), an empty list to clear, a save token alone — is refused 400 by the
// client's own check, ahead of the re-read and of any write, with a token or
// without. Proxmox would rewrite the whole node config file for it, moving the
// digest under every open dialog, and the audit would be left an "acme_config
// updated" row that names nothing.
func TestNodeConfigACMESaveOfNothingIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name   string
		fields func(token string) map[string]any
	}{
		{"an empty body", func(string) map[string]any { return map[string]any{} }},
		{"a save token alone", func(token string) map[string]any { return map[string]any{"digest": token} }},
		{"a stranger's token alone", func(string) map[string]any { return map[string]any{"digest": "v1." + strings.Repeat("A", 43)} }},
		{"empty fields, which leave the stored values alone", func(string) map[string]any {
			return map[string]any{"acme": "", "acmedomain0": ""}
		}},
		{"empty fields and a save token", func(token string) map[string]any {
			return map[string]any{"acme": "", "acmedomain3": "", "digest": token}
		}},
		{"an empty list to clear", func(string) map[string]any { return map[string]any{"delete": []string{}} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			token := w.tokenFrom(t, w.acmeGET(testNodeName))
			fileBefore, mark := pve.digestOf(testNodeName), pve.mark()

			status, body := w.send(t, w.acmePUT(testNodeName, tt.fields(token)))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%s), want 400", status, clipForFailure(string(body), 300))
			}
			if !strings.Contains(string(body), "nothing to change") {
				t.Errorf("the answer %s does not say there is nothing to change", clipForFailure(string(body), 300))
			}
			if sent := pve.since(mark); len(sent) != 0 {
				t.Errorf("a write that changes nothing reached Proxmox: %+v", sent)
			}
			if got := pve.digestOf(testNodeName); got != fileBefore {
				t.Error("the file changed under a write that was refused")
			}
			if rows := w.store.auditRows(); len(rows) != 0 {
				t.Errorf("wrote %d audit row(s) for a save that did not happen, want none", len(rows))
			}
		})
	}

	// And a request that does change something, however little, is not caught by it.
	for _, tt := range []struct {
		name   string
		fields map[string]any
	}{
		{"one setting", map[string]any{"acmedomain1": "other.example.com"}},
		{"one key to clear", map[string]any{"delete": []string{"acmedomain0"}}},
	} {
		t.Run(tt.name+" is still saved", func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			token := w.tokenFrom(t, w.acmeGET(testNodeName))

			if status, body := w.send(t, w.acmePUT(testNodeName, withDigest(tt.fields, token))); status != fiber.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
			}
			if rows := w.store.auditRows(); len(rows) != 1 {
				t.Errorf("the save wrote %d audit rows, want 1", len(rows))
			}
		})
	}
}

// TestNodeConfigSaveOfANodeWhoseFileWasEmptied: a node whose file has been emptied —
// every setting and the notes cleared — is, to Proxmox, a node with no file: its read
// is an empty config with no digest. So it is handed no token, a save that names one
// is a 409 with only the read of the digest spent, and the save that follows with
// none is unconditional, after which the node has a digest and a token again.
func TestNodeConfigSaveOfANodeWhoseFileWasEmptied(t *testing.T) {
	for _, tt := range []struct {
		name  string
		keys  map[string]string
		write func(w *nodeConfigWorld, node string, fields map[string]any) *http.Request
		read  func(w *nodeConfigWorld, node string) *http.Request
		empty map[string]any
		again map[string]any
	}{
		{
			name:  "the options",
			keys:  map[string]string{"ballooning-target": "75"},
			write: (*nodeConfigWorld).optionsPUT,
			read:  (*nodeConfigWorld).optionsGET,
			empty: map[string]any{"delete": []string{"ballooning-target"}},
			again: map[string]any{"ballooning-target": 60},
		},
		{
			name:  "the ACME settings",
			keys:  map[string]string{"acmedomain0": "node.example.com"},
			write: (*nodeConfigWorld).acmePUT,
			read:  (*nodeConfigWorld).acmeGET,
			empty: map[string]any{"delete": []string{"acmedomain0"}},
			again: map[string]any{"acmedomain1": "other.example.com"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, "", tt.keys)
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			token := w.tokenFrom(t, tt.read(w, testNodeName))

			// The save that empties the file, with the token its read returned.
			if status, body := w.send(t, tt.write(w, testNodeName, withDigest(tt.empty, token))); status != fiber.StatusOK {
				t.Fatalf("the save that empties the file: status = %d (%s), want 200", status, clipForFailure(string(body), 300))
			}
			if got := pve.digestOf(testNodeName); got != "" {
				t.Fatalf("the file still has the digest %q; the case did not empty it", got)
			}

			// The file is there and holds nothing; a read does not tell it from no file.
			status, body := w.send(t, tt.read(w, testNodeName))
			if status != fiber.StatusOK || string(body) != `{}` {
				t.Errorf("a read of the emptied file answers %d %s, want 200 {}: no digest, so no token", status, clipForFailure(string(body), 300))
			}

			// The token it was emptied with is of a file that is not there any more.
			mark := pve.mark()
			status, body = w.send(t, tt.write(w, testNodeName, withDigest(tt.again, token)))
			if status != fiber.StatusConflict {
				t.Fatalf("a save with the old token: status = %d (%s), want 409", status, clipForFailure(string(body), 300))
			}
			if sent := pve.since(mark); len(sent) != 1 || sent[0].method != http.MethodGet {
				t.Errorf("Proxmox received %+v, want only the read of the digest", sent)
			}

			// The next save is unconditional, and gives the node a digest again.
			if status, body = w.send(t, tt.write(w, testNodeName, tt.again)); status != fiber.StatusOK {
				t.Fatalf("a save with no token: status = %d (%s), want 200", status, clipForFailure(string(body), 300))
			}
			requireSaveToken(t, mustRead(t, w, tt.read(w, testNodeName)), pve.digestOf(testNodeName))
		})
	}
}

// TestNodeConfigPVEKeepsToProxmoxsFileModel holds the stand-in to the parts of
// Proxmox's behaviour its doc comment claims, since every route test above leans on
// them: an emptied file reads like no file and is not compared with, the digest is
// a function of the content as write_node_config writes it (a trailing line break in
// the notes adds nothing, a leading one does), and the values of a PUT are applied
// before its deletes.
func TestNodeConfigPVEKeepsToProxmoxsFileModel(t *testing.T) {
	file := func(notes string, keys map[string]string) *nodeConfigFile {
		return &nodeConfigFile{notes: notes, keys: keys}
	}
	keys := map[string]string{"ballooning-target": "75"}

	t.Run("notes split on line breaks, trailing empty lines dropped", func(t *testing.T) {
		same := file("a", keys).digest()
		for _, notes := range []string{"a\n", "a\n\n"} {
			if got := file(notes, keys).digest(); got != same {
				t.Errorf("notes %q have the digest %s, want that of %q, %s: a trailing line break adds no line", notes, got, "a", same)
			}
		}
		for _, notes := range []string{"\na", "a\nb", "a\n\nb", "b", ""} {
			if got := file(notes, keys).digest(); got == same {
				t.Errorf("notes %q have the digest of %q: they are another file", notes, "a")
			}
		}
		if got := file("a\n", keys).readNotes(); got != "a\n" {
			t.Errorf("a read returns the notes %q, want each line ended by a line break", got)
		}
		if got := file("\n\n", keys).readNotes(); got != "" {
			t.Errorf("notes of nothing but line breaks read back as %q, want none", got)
		}
	})

	t.Run("a file with nothing in it has no digest, and reads like no file", func(t *testing.T) {
		for name, f := range map[string]*nodeConfigFile{
			"no notes and no keys":      file("", map[string]string{}),
			"no notes and nil keys":     file("", nil),
			"notes of only line breaks": file("\n\n", nil),
		} {
			if got := f.digest(); got != "" {
				t.Errorf("%s: digest %q, want none", name, got)
			}
		}
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, "", nil)
		resp, err := http.Get(pve.url + "/api2/json/nodes/" + testNodeName + "/config")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		if string(body) != `{"data":{}}` {
			t.Errorf("a read of an emptied file is %s, want an empty config with no digest", body)
		}
	})

	t.Run("a write is not compared with an emptied file, and the values go in before the deletes", func(t *testing.T) {
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, "", nil)
		put := func(form url.Values) int {
			req, err := http.NewRequest(http.MethodPut, pve.url+"/api2/json/nodes/"+testNodeName+"/config", strings.NewReader(form.Encode()))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("PUT: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			return resp.StatusCode
		}

		// A digest with nothing to be compared against is accepted.
		if status := put(url.Values{"ballooning-target": {"75"}, "digest": {strings.Repeat("0", 40)}}); status != http.StatusOK {
			t.Fatalf("a write with a digest to an emptied file: status %d, want 200", status)
		}
		// Now the file has content, and a stale digest dies.
		if status := put(url.Values{"wakeonlan": {"02:00:00:00:00:01"}, "digest": {strings.Repeat("0", 40)}}); status != http.StatusInternalServerError {
			t.Errorf("a write with a stale digest: status %d, want the plain 500 assert_if_modified dies with", status)
		}
		// A key in both is deleted: the values are applied first.
		if status := put(url.Values{"ballooning-target": {"60"}, "delete": {"ballooning-target"}}); status != http.StatusOK {
			t.Fatalf("a write that sets and clears one key: status %d, want 200", status)
		}
		pve.mu.Lock()
		_, still := pve.files[testNodeName].keys["ballooning-target"]
		pve.mu.Unlock()
		if still {
			t.Error("a key that was set and cleared in one write is still there: set_options applies delete after the values")
		}
	})
}

// TestNodeConfigSaveIsGatedBeforeTheTokenIsLooked: a caller who may not write is
// refused by the declaration's gate, and nothing — not even the read of the digest
// — reaches Proxmox for them, so the token is not something a Viewer can use to make
// Nexara read the file on their behalf.
func TestNodeConfigSaveIsGatedBeforeTheTokenIsLooked(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		t.Run(wr.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
			manager := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			token := manager.tokenFrom(t, wr.read(manager, testNodeName))

			viewer := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: stubAuth(nodeOptionsGrants("view:node", "view:certificate"))})
			mark := pve.mark()
			status, body := viewer.send(t, wr.write(viewer, testNodeName, withDigest(wr.change, token)))
			if status != fiber.StatusForbidden {
				t.Fatalf("status = %d (%s), want 403", status, clipForFailure(string(body), 300))
			}
			if sent := pve.since(mark); len(sent) != 0 {
				t.Errorf("a refused save reached Proxmox: %+v", sent)
			}
		})
	}
}

// TestNodeConfigSaveRefusesABodyProxmoxWouldRefuse: the client refuses an encoded
// body over 512 KiB before sending it (Proxmox answers anything over its post limit
// 501, after reading no more than the head), and the route makes that a 413 in the
// words a 501 already got. The limit is on the ENCODED form, where a non-ASCII
// character takes up to nine bytes, so the notes are well inside the declared bound
// of 65536 characters and over the limit; and the digest a save sends counts toward
// it.
func TestNodeConfigSaveRefusesABodyProxmoxWouldRefuse(t *testing.T) {
	const limit = 512 * 1024
	// "description=" is 12 bytes, and each "東" is 9 once it is encoded: the form is
	// 12+9k+m bytes for k of them and m of "a".
	notesOfEncodedSize := func(total int) string {
		k := (total - 12) / 9
		return strings.Repeat("東", k) + strings.Repeat("a", total-12-9*k)
	}

	t.Run("without a token", func(t *testing.T) {
		for _, tt := range []struct {
			name  string
			size  int
			wantS int
		}{
			{"exactly at the limit", limit, fiber.StatusOK},
			{"one byte over", limit + 1, fiber.StatusRequestEntityTooLarge},
		} {
			t.Run(tt.name, func(t *testing.T) {
				pve := newNodeConfigPVE(t)
				w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
				notes := notesOfEncodedSize(tt.size)
				if got := len(url.Values{"description": {notes}}.Encode()); got != tt.size {
					t.Fatalf("the case builds a form of %d bytes, not %d", got, tt.size)
				}

				status, body := w.send(t, w.optionsPUT(testNodeName, map[string]any{"description": notes}))
				if status != tt.wantS {
					t.Fatalf("status = %d (%s), want %d", status, clipForFailure(string(body), 300), tt.wantS)
				}
				sent := pve.requests()
				if tt.wantS == fiber.StatusOK {
					if len(sent) != 1 {
						t.Fatalf("Proxmox received %d request(s), want the one write", len(sent))
					}
					if got := len(sent[0].form.Encode()); got != tt.size {
						t.Errorf("Proxmox was sent %d bytes, want %d", got, tt.size)
					}
					return
				}
				if len(sent) != 0 {
					t.Errorf("a body over the limit reached Proxmox: %d request(s)", len(sent))
				}
				if !strings.Contains(string(body), "too large for Proxmox") || !strings.Contains(string(body), "64 KiB") {
					t.Errorf("the answer %s does not say what the limit is", clipForFailure(string(body), 300))
				}
				if rows := w.store.auditRows(); len(rows) != 0 {
					t.Errorf("a save that was refused wrote %d audit row(s), want none", len(rows))
				}
			})
		}
	})

	t.Run("with a token, whose digest counts toward the limit before anything is read", func(t *testing.T) {
		// The digest Proxmox is sent is "&digest=" and 40 hex characters. The
		// pre-save validation counts it as that whatever the token was (a token is
		// 46 characters, and may be nothing like the file's), so a body the digest
		// takes over the limit is refused 413 before the re-read — with a token that
		// would have matched and with one that would not — and a body that fits with
		// it goes on to the re-read, where a token that does not match is a 409.
		const digestBytes = 1 + len("digest=") + 40
		stranger := "v1." + strings.Repeat("A", 43)
		for _, tt := range []struct {
			name string
			// size is the form's size without the digest; token is what the save sends,
			// "" for the one the node's read returned.
			size      int
			token     string
			wantS     int
			wantSteps int
		}{
			{"exactly at the limit once the digest is added", limit - digestBytes, "", fiber.StatusOK, 2},
			{"one byte over once the digest is added", limit - digestBytes + 1, "", fiber.StatusRequestEntityTooLarge, 0},
			{"one byte over once the digest is added, with a token that would not match", limit - digestBytes + 1, stranger, fiber.StatusRequestEntityTooLarge, 0},
			{"exactly at the limit once the digest is added, with a token that does not match", limit - digestBytes, stranger, fiber.StatusConflict, 1},
		} {
			t.Run(tt.name, func(t *testing.T) {
				pve := newNodeConfigPVE(t)
				pve.setFile(testNodeName, "", nodeConfigSettings())
				w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
				token := tt.token
				if token == "" {
					token = w.tokenFrom(t, w.optionsGET(testNodeName))
				}
				mark := pve.mark()

				status, body := w.send(t, w.optionsPUT(testNodeName, withDigest(map[string]any{"description": notesOfEncodedSize(tt.size)}, token)))
				if status != tt.wantS {
					t.Fatalf("status = %d (%s), want %d", status, clipForFailure(string(body), 300), tt.wantS)
				}
				sent := pve.since(mark)
				if len(sent) != tt.wantSteps {
					t.Fatalf("Proxmox received %d request(s), want %d: %+v", len(sent), tt.wantSteps, clipForFailure(strings.Join(requestLines(sent), ", "), 300))
				}
				if tt.wantS == fiber.StatusOK {
					if got := len(sent[1].form.Encode()); got != limit {
						t.Errorf("Proxmox was sent %d bytes, want exactly the limit, %d", got, limit)
					}
					return
				}
				if tt.wantS == fiber.StatusRequestEntityTooLarge && !strings.Contains(string(body), "too large for Proxmox") {
					t.Errorf("the answer %s does not say what the limit is", clipForFailure(string(body), 300))
				}
				if len(sent) == 1 && sent[0].method != http.MethodGet {
					t.Errorf("the one request was %s, want the read of the digest", sent[0].method)
				}
				if rows := w.store.auditRows(); len(rows) != 0 {
					t.Errorf("a save that was refused wrote %d audit row(s), want none", len(rows))
				}
			})
		}
	})

	t.Run("notes inside the declared bound and over the limit, with a token, cost no read at all", func(t *testing.T) {
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, "", nodeConfigSettings())
		w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
		token := w.tokenFrom(t, w.optionsGET(testNodeName))
		mark := pve.mark()

		// 60000 characters, as many as the bound allows within reason, and 540012 bytes.
		status, body := w.send(t, w.optionsPUT(testNodeName, withDigest(map[string]any{"description": strings.Repeat("東", 60000)}, token)))
		if status != fiber.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d (%s), want 413", status, clipForFailure(string(body), 300))
		}
		if sent := pve.since(mark); len(sent) != 0 {
			t.Errorf("a request the client refuses as too large reached Proxmox: %+v", sent)
		}
	})
}

// requestLines renders requests for a failure message.
func requestLines(reqs []accessUpdatePVERequest) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r.method + " " + r.path
	}
	return out
}
