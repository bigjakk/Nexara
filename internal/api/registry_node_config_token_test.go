package api

import (
	"cmp"
	"context"
	"crypto/sha1" //nolint:gosec // the stand-in reproduces Proxmox's own digest, which is a SHA1
	"encoding/hex"
	"encoding/json"
	"errors"
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
	db "github.com/bigjakk/nexara/internal/db/generated"
)

// The node config save token, followed through the real routes: GET .../options, .../notes
// and .../acme-config return a `digest` that is a save token, never Proxmox's own (an
// unsalted SHA1 of the whole file, notes included, which can be hashed offline or probed for
// with candidate digests); PUT .../options and .../acme-config check it against the file as it
// is when the save arrives. handlers/node_config_token_test.go pins the token itself; here,
// who is handed one, what a save sends Proxmox in its place, and that a refused save leaves
// nothing behind. The stand-in Proxmox HOLDS the file, so "it changed between the read and
// the save" is something a test does to it.

// errRBACLookupDown is the failure an RBAC engine reports when it cannot answer.
var errRBACLookupDown = errors.New("rbac lookup down")

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

// scopedRBACEngine tells the scopes apart as internal/auth's RBACEngine does (a
// permission granted at a cluster holds for that cluster alone, one granted globally
// for every cluster) and records every lookup. stubRBACEngine answers both lookups from
// one map, so a handler that asked the wrong scope would pass against it. globalOnly
// names permissions only the global lookup says yes to, which the real engine never
// does: a handler that decides through it shows the permission to a caller the
// cluster-scoped lookup refuses. A lookup of failOn fails.
type scopedRBACEngine struct {
	mu         sync.Mutex
	grants     []scopedGrant
	globalOnly []string
	failOn     string
	lookups    []rbacLookup
}

func (e *scopedRBACEngine) HasPermission(_ context.Context, _ uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
	perm := action + ":" + resource
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lookups = append(e.lookups, rbacLookup{permission: perm, scopeType: scopeType, scopeID: scopeID})
	if perm == e.failOn {
		return false, errRBACLookupDown
	}
	for _, g := range e.grants {
		if g.permission == perm && (g.cluster == uuid.Nil || (scopeType == "cluster" && g.cluster == scopeID)) {
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
	if perm == e.failOn {
		return false, errRBACLookupDown
	}
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

// authWithEngine is stubAuth with the engine given.
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
	return got, slices.Sorted(maps.Keys(got))
}

var (
	// saveTokenShape is a save token: its version in the clear, then the unpadded
	// base64url of a 32-byte HMAC-SHA256.
	saveTokenShape = regexp.MustCompile(`^v1\.[A-Za-z0-9_-]{43}$`)
	// rawDigestShape is Proxmox's own digest, which must not appear in any response.
	rawDigestShape = regexp.MustCompile(`[0-9a-f]{40}`)
)

// nodeConfigChangedAnswer is the one 409 a save is given when the file is not what it
// was based on, whichever check found out (Nexara's comparison of the token, or
// Proxmox's own assert_if_modified), so a caller cannot tell a token that is not one
// from a file that moved.
const nodeConfigChangedAnswer = "The node's configuration changed since it was read — reload and try again."

// requireSaveToken checks that the body carries the save token as its `digest` and
// nothing of Proxmox's own, and returns the token.
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

// requireNoRawDigest fails when Proxmox's digest, or anything shaped like one, is in the body.
func requireNoRawDigest(t *testing.T, body []byte, rawDigest string) {
	t.Helper()
	if rawDigest != "" && strings.Contains(string(body), rawDigest) {
		t.Errorf("Proxmox's own digest %s reached the caller: %s", rawDigest, clipForFailure(string(body), 300))
	}
	if m := rawDigestShape.FindString(string(body)); m != "" {
		t.Errorf("something shaped like Proxmox's digest (%s) reached the caller: %s", m, clipForFailure(string(body), 300))
	}
}

// nodeConfigFile is one node's /etc/pve/nodes/<node>/config as the stand-in holds it.
type nodeConfigFile struct {
	notes string
	keys  map[string]string
}

// noteLines are the lines write_node_config writes the notes as: it splits on "\n"
// with Perl's split, which drops trailing empty fields, so "a", "a\n" and "a\n\n" are
// one file and notes of nothing but line breaks are none.
func (f *nodeConfigFile) noteLines() []string {
	lines := strings.Split(f.notes, "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// raw is the file's text as write_node_config shapes it: the notes as '#' lines
// first, then every other key sorted. The escaping stands in for encode_text; what
// matters is that the text is a function of the content, as Proxmox's is.
func (f *nodeConfigFile) raw() string {
	var b strings.Builder
	for _, line := range f.noteLines() {
		b.WriteString("#" + url.PathEscape(line) + "\n")
	}
	for _, k := range slices.Sorted(maps.Keys(f.keys)) {
		b.WriteString(k + ": " + f.keys[k] + "\n")
	}
	return b.String()
}

// readNotes is the notes as a read returns them: every line ended with "\n".
func (f *nodeConfigFile) readNotes() string {
	var b strings.Builder
	for _, line := range f.noteLines() {
		b.WriteString(line + "\n")
	}
	return b.String()
}

// digest is the SHA1 of the file. A file with nothing in it has none:
// parse_node_config answers it with an empty config, so one nobody has written to
// and one that has been emptied are the same to a read.
func (f *nodeConfigFile) digest() string {
	raw := f.raw()
	if raw == "" {
		return ""
	}
	sum := sha1.Sum([]byte(raw)) //nolint:gosec // Proxmox's digest is a SHA1
	return hex.EncodeToString(sum[:])
}

// nodeConfigPVE stands in for GET and PUT /nodes/{node}/config of a cluster's Proxmox,
// for every node, with the behaviour a save token depends on: a node it holds no
// file for reads as an empty config with no digest, and the first write creates the
// file; the digest is a function of the file's content; a PUT compares a digest only
// when both sides have one (assert_if_modified, a plain 500 on a mismatch) and
// applies the values before `delete`. It does not check values against $confdesc or
// forward to the node, and its escaping is not Proxmox's, so its digests are
// Proxmox's in kind and not for the same content. It records every request.
// TestNodeConfigPVEKeepsToProxmoxsFileModel holds it to the claims above.
type nodeConfigPVE struct {
	mu    sync.Mutex
	url   string
	files map[string]*nodeConfigFile
	seen  []accessUpdatePVERequest
	reads int
	// afterRead maps the number of a GET, the first being 1, to what happens to the
	// files once it has been answered: a change in the window between the handler's
	// read of the digest and its write.
	afterRead map[int]func(files map[string]*nodeConfigFile)
	// readStatus and readBody, when set, are the refusal every GET is answered with.
	readStatus int
	readBody   string
}

func newNodeConfigPVE(t *testing.T) *nodeConfigPVE {
	t.Helper()
	p := &nodeConfigPVE{files: map[string]*nodeConfigFile{}, afterRead: map[int]func(map[string]*nodeConfigFile){}}
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

// setNotes is somebody else's edit of the file (Proxmox's own Notes panel, say): it
// changes the file, and so its digest, without going through Nexara.
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

// TestNodeConfigPVEKeepsToProxmoxsFileModel holds the stand-in to what the route tests lean
// on of PVE's NodeConfig.pm, set_options and assert_if_modified: the notes are '#' lines
// split on "\n" with trailing empty fields dropped, and a read ends each with "\n"; a file
// with nothing in it reads like no file and is not compared with, while a stale digest on one
// with content dies with the plain 500; the values of a PUT go in before its `delete`.
func TestNodeConfigPVEKeepsToProxmoxsFileModel(t *testing.T) {
	type reply struct {
		code int
		body string
		keys int // the keys the file holds afterwards
	}
	// on sends a request to a stand-in Proxmox holding f.
	on := func(f *nodeConfigFile, method string, form url.Values) reply {
		pve := &nodeConfigPVE{files: map[string]*nodeConfigFile{}}
		pve.setFile(testNodeName, f.notes, f.keys)
		req := httptest.NewRequest(method, nodeOptionsPVEPath, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		pve.handle(rec, req)
		return reply{rec.Code, rec.Body.String(), len(pve.files[testNodeName].keys)}
	}
	file := func(notes string) *nodeConfigFile {
		return &nodeConfigFile{notes: notes, keys: map[string]string{"k": "1"}}
	}
	emptied, stale := &nodeConfigFile{notes: "\n\n"}, url.Values{"k": {"2"}, "digest": {strings.Repeat("0", 40)}}

	for _, tt := range []struct {
		name      string
		got, want any
	}{
		{"a trailing line break adds no line", file("a\n\n").digest() == file("a").digest(), true},
		{"a leading line break does, and other notes are other files",
			file("\na").digest() != file("a").digest() && file("a\nb").digest() != file("a").digest(), true},
		{"a read ends every note line with a line break", file("a\nb").readNotes(), "a\nb\n"},
		{"a file with nothing in it, notes of line breaks alone included, reads like no file",
			on(emptied, http.MethodGet, nil).body, `{"data":{}}`},
		{"a digest is not compared with such a file", on(emptied, http.MethodPut, stale).code, http.StatusOK},
		{"it is with one that has content, and a stale one dies with the plain 500",
			on(file("a"), http.MethodPut, stale).code, http.StatusInternalServerError},
		{"the values of a PUT go in before its deletes",
			on(file("a"), http.MethodPut, url.Values{"k": {"2"}, "delete": {"k"}}).keys, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

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
	// A second cluster, ENCRYPTION_KEY and node, and a node with no file, for the
	// tests that need the same file under another identity.
	otherTestClusterID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	otherTestEncKey    = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	otherTestNodeName  = "pve-02"
	noFileNodeName     = "pve-03"
)

// nodeConfigWorld is the five real routes of the node config file, mounted on the
// real handlers over a stand-in Proxmox and a stand-in database holding one cluster
// row that points at it.
type nodeConfigWorld struct {
	app       *fiber.App
	store     *accessUpdateDB
	clusterID string
}

type nodeConfigWorldOptions struct {
	// key is the handlers' ENCRYPTION_KEY and clusterID the cluster the database
	// holds; accessUpdateEncKey and testClusterID when empty.
	key, clusterID string
	// auth is the authentication middleware; one granting every permission of the
	// five routes when nil.
	auth fiber.Handler
}

// allNodeConfigGrants is every permission the five routes ask for.
var allNodeConfigGrants = []string{"view:node", "manage:node", "view:certificate", "manage:certificate"}

const acmeConfigPath = acmeNodeScope + "/acme-config"

func newNodeConfigWorld(t *testing.T, pve *nodeConfigPVE, o nodeConfigWorldOptions) *nodeConfigWorld {
	t.Helper()
	key := cmp.Or(o.key, accessUpdateEncKey)
	authn := o.auth
	if authn == nil {
		authn = stubAuth(grantsOf(allNodeConfigGrants...))
	}
	store := newClusterStore(t, pve.url, key, o.clusterID)
	nodes := handlers.NewNodeHandler(db.New(store), key, nil)
	acme := handlers.NewACMEHandler(db.New(store), key, nil)
	app := mountReal(t, authn,
		realRoute{http.MethodGet, nodeOptionsPath, nodes.GetNodeOptions},
		realRoute{http.MethodGet, nodeNotesPath, nodes.GetNodeNotes},
		realRoute{http.MethodPut, nodeOptionsPath, nodes.SetNodeOptions},
		realRoute{http.MethodGet, acmeConfigPath, acme.GetNodeACMEConfig},
		realRoute{http.MethodPut, acmeConfigPath, acme.SetNodeACMEConfig},
	)
	return &nodeConfigWorld{app: app, store: store, clusterID: store.cluster.ID.String()}
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
	return w.request(http.MethodGet, acmeConfigPath, node, nil)
}

func (w *nodeConfigWorld) optionsPUT(node string, fields map[string]any) *http.Request {
	return w.request(http.MethodPut, nodeOptionsPath, node, fields)
}

func (w *nodeConfigWorld) acmePUT(node string, fields map[string]any) *http.Request {
	return w.request(http.MethodPut, acmeConfigPath, node, fields)
}

func (w *nodeConfigWorld) send(t *testing.T, req *http.Request) (int, []byte) {
	t.Helper()
	return sendBody(t, w.app, req)
}

// mustRead makes a read that has to succeed and returns its body.
func (w *nodeConfigWorld) mustRead(t *testing.T, req *http.Request) []byte {
	t.Helper()
	status, body := w.send(t, req)
	if status != fiber.StatusOK {
		t.Fatalf("%s %s: status = %d (%s), want 200", req.Method, req.URL.Path, status, clipForFailure(string(body), 300))
	}
	return body
}

// tokenFrom makes a read and returns the save token it hands out.
func (w *nodeConfigWorld) tokenFrom(t *testing.T, req *http.Request) string {
	t.Helper()
	body := w.mustRead(t, req)
	got, _ := decodeKeys(t, body)
	token, _ := got["digest"].(string)
	if token == "" {
		t.Fatalf("%s %s returned no digest: %s", req.Method, req.URL.Path, clipForFailure(string(body), 300))
	}
	return token
}

// nodeConfigWriter is one of the two routes that save the node config file, with a
// read that hands out the token for it and a save it accepts.
type nodeConfigWriter struct {
	name  string
	read  func(w *nodeConfigWorld, node string) *http.Request
	write func(w *nodeConfigWorld, node string, fields map[string]any) *http.Request
	// change is a save the route accepts and that changes the file.
	change map[string]any
	// auditType and auditAction are the one audit row a save that went through writes.
	auditType, auditAction string
}

var nodeConfigWriters = []nodeConfigWriter{
	{
		name: "the options", read: (*nodeConfigWorld).optionsGET, write: (*nodeConfigWorld).optionsPUT,
		change: map[string]any{"ballooning-target": 80}, auditType: "node", auditAction: "set_options",
	},
	{
		name: "the ACME settings", read: (*nodeConfigWorld).acmeGET, write: (*nodeConfigWorld).acmePUT,
		change: map[string]any{"acmedomain1": "other.example.com"}, auditType: "acme_config", auditAction: "updated",
	},
}

var optionsWriter, acmeWriter = nodeConfigWriters[0], nodeConfigWriters[1]

// withDigest is fields with the save token added as `digest`.
func withDigest(fields map[string]any, token string) map[string]any {
	out := maps.Clone(fields)
	out["digest"] = token
	return out
}

// saveRun is one writer's route in a fresh world whose node holds a file, and a token
// read through that route.
type saveRun struct {
	t     *testing.T
	pve   *nodeConfigPVE
	w     *nodeConfigWorld
	wr    nodeConfigWriter
	token string
}

func newSaveRun(t *testing.T, wr nodeConfigWriter) *saveRun {
	t.Helper()
	pve := newNodeConfigPVE(t)
	pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
	w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
	return &saveRun{t: t, pve: pve, w: w, wr: wr, token: w.tokenFrom(t, wr.read(w, testNodeName))}
}

// refusal is what a save that must not happen answers and costs.
type refusal struct {
	status int
	// message is a fragment of the answer's message, or exact its whole; neither: unchecked.
	message, exact string
	// sent are the methods of the requests that may reach Proxmox, in order.
	sent []string
	// moved is a file that changes under the save for a reason of its own.
	moved bool
}

// requireRefused sends fields as a save of node (testNodeName when empty) through
// sender (the run's world when nil) and holds the refusal: the answer, the requests
// that reached Proxmox (all for that node's config), the file left as it was and no
// audit row. It returns what reached Proxmox.
func (x *saveRun) requireRefused(sender *nodeConfigWorld, node string, fields map[string]any, want refusal) []accessUpdatePVERequest {
	x.t.Helper()
	sender, node = cmp.Or(sender, x.w), cmp.Or(node, testNodeName)
	fileBefore, mark, rowsBefore := x.pve.digestOf(node), x.pve.mark(), len(sender.store.auditRows())

	status, body := sender.send(x.t, x.wr.write(sender, node, fields))
	msg := messageOf(body)
	if status != want.status {
		x.t.Fatalf("status = %d (%s), want %d", status, clipForFailure(string(body), 300), want.status)
	}
	if want.message != "" && !strings.Contains(msg, want.message) {
		x.t.Errorf("the answer %s does not say %q", clipForFailure(string(body), 300), want.message)
	}
	if want.exact != "" && msg != want.exact {
		x.t.Errorf("the answer is %s, want the message %q: the same words Proxmox's own refusal gets", clipForFailure(string(body), 300), want.exact)
	}
	if want.status != fiber.StatusConflict && strings.Contains(string(body), nodeConfigChangedAnswer) {
		x.t.Errorf("the answer %s is the stale-file 409, which a caller would answer by reloading", clipForFailure(string(body), 300))
	}
	sent := x.pve.since(mark)
	methods := make([]string, len(sent))
	for i, r := range sent {
		methods[i] = r.method
		if r.path != "/api2/json/nodes/"+node+"/config" {
			x.t.Errorf("%s %s: the node config of %s is at /api2/json/nodes/%s/config", r.method, r.path, node, node)
		}
	}
	if !slices.Equal(methods, want.sent) {
		x.t.Errorf("Proxmox received %v, want %v: a refused save must stop where the refusal is", methods, want.sent)
	}
	if got := x.pve.digestOf(node); got != fileBefore && !want.moved {
		x.t.Error("the file changed under a save that was refused")
	}
	if rows := len(sender.store.auditRows()); rows != rowsBefore {
		x.t.Errorf("a save that did not happen wrote %d audit row(s), want none", rows-rowsBefore)
	}
	return sent
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
			tokens = append(tokens, requireSaveToken(t, w.mustRead(t, r.req(w, testNodeName)), raw))
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
		w := newNodeConfigWorld(t, newNodeConfigPVE(t), nodeConfigWorldOptions{})
		for _, r := range reads {
			if body := w.mustRead(t, r.req(w, testNodeName)); string(body) != `{}` {
				t.Errorf("%s: body = %s, want {}: a token for no file would be one to send back for nothing", r.name, body)
			}
		}
	})

	t.Run("the token fits the parameter that carries it back", func(t *testing.T) {
		w, _, _ := newWorld(t)
		token := w.tokenFrom(t, w.optionsGET(testNodeName))
		for _, e := range []Endpoint{declaredEndpoint(t, fiber.MethodPut, nodeOptionsPath), declaredEndpoint(t, fiber.MethodPut, acmeConfigPath)} {
			if bound := e.Parameters["digest"].MaxLength; bound == nil || len(token) > *bound {
				t.Errorf("PUT %s declares the digest bound %v, and the token is %d characters", e.Path, bound, len(token))
			}
		}
	})
}

// TestNodeConfigTokenIsBoundToItsClusterNodeAndKey: the same file, read under another
// cluster, another node or another ENCRYPTION_KEY, gives another token, which makes
// a token a statement about one node of one cluster and not about a file anybody can copy.
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
	// Within one world the routes agree, whichever is read: it is the one file's token.
	for name, w := range map[string]*nodeConfigWorld{"the base case": base, "another cluster": otherCluster, "another key": otherKey} {
		if opt, acme := w.tokenFrom(t, w.optionsGET(testNodeName)), w.tokenFrom(t, w.acmeGET(testNodeName)); opt != acme {
			t.Errorf("%s: the options read gave %q and the ACME read %q: one file, one token", name, opt, acme)
		}
	}
}

// TestNodeConfigTokenIsForWriters: beyond who is handed a token (see
// TestNodeConfigTokenPermissionIsScopedToThePathsCluster), a permission lookup that
// fails fails the request before anything reaches Proxmox. Answering without the token
// would give a manager's next save none to send, and an unconditional write is the
// compare-and-swap failing open. The notes read hands out the token under the writer's
// permission alone, and the ACME read keeps its declared gate.
func TestNodeConfigTokenIsForWriters(t *testing.T) {
	for _, rd := range []struct {
		name         string
		read         func(w *nodeConfigWorld, node string) *http.Request
		view, manage string
		answered     string // something of the config that must not come out
	}{
		{"the options", (*nodeConfigWorld).optionsGET, "view:node", "manage:node", "ballooning"},
		{"the ACME settings", (*nodeConfigWorld).acmeGET, "view:certificate", "manage:certificate", "acmedomain0"},
	} {
		t.Run(rd.name+": a permission lookup that fails fails the request, and nothing reaches Proxmox", func(t *testing.T) {
			// The view permission passes the declared gate; the handler's own look at the
			// write permission is what fails.
			engine := &scopedRBACEngine{grants: []scopedGrant{{permission: rd.view}}, failOn: rd.manage}
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: authWithEngine(engine)})

			status, body := w.send(t, rd.read(w, testNodeName))
			if status != fiber.StatusInternalServerError || !strings.Contains(string(body), "Permission check failed") {
				t.Fatalf("status = %d (%s), want a 500 saying the permission check failed: a failed lookup must not be answered as if it said no",
					status, clipForFailure(string(body), 300))
			}
			if strings.Contains(string(body), "digest") || strings.Contains(string(body), rd.answered) {
				t.Errorf("the failed request still carried the config: %s", body)
			}
			if sent := pve.requests(); len(sent) != 0 {
				t.Errorf("the permission lookup failed and %d request(s) still reached Proxmox: %+v", len(sent), sent)
			}
		})
	}

	t.Run("the notes read hands out the token, since its gate is the writer's permission", func(t *testing.T) {
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
		w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: stubAuth(grantsOf("manage:node"))})
		requireSaveToken(t, w.mustRead(t, w.notesGET(testNodeName)), pve.digestOf(testNodeName))
	})

	t.Run("the declared gate still refuses a caller who cannot view certificates", func(t *testing.T) {
		pve := newNodeConfigPVE(t)
		pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
		w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: stubAuth(grantsOf("view:node", "manage:certificate"))})
		if status, body := w.send(t, w.acmeGET(testNodeName)); status != fiber.StatusForbidden {
			t.Fatalf("status = %d (%s), want 403", status, clipForFailure(string(body), 300))
		}
		if sent := pve.requests(); len(sent) != 0 {
			t.Errorf("a refused read reached Proxmox: %+v", sent)
		}
	})
}

// TestNodeConfigTokenPermissionIsScopedToThePathsCluster follows GET .../options and
// .../acme-config for every kind of caller and holds the permission the token is shown under
// to the cluster in the path. A Viewer reads the settings and no digest at all (the key is
// absent, not empty, so the page cannot mistake a missing token for one to send back), a
// manager reads the token, and another resource's write permission is not this one's.
// stubRBACEngine answers a cluster-scoped and a global lookup from one map, so a handler that
// asked the wrong scope would pass against it; this engine answers as the real one does, so
// WHICH lookup the handler makes is pinned too. (The token reveals nothing about the file;
// withholding it from a Viewer is defence in depth.)
func TestNodeConfigTokenPermissionIsScopedToThePathsCluster(t *testing.T) {
	pathCluster, otherCluster := uuid.MustParse(testClusterID), uuid.MustParse(otherTestClusterID)

	for _, rd := range []struct {
		name                string
		read                func(w *nodeConfigWorld, node string) *http.Request
		view, manage, other string
		visible             []string // the keys a Viewer reads
	}{
		{"the options", (*nodeConfigWorld).optionsGET, "view:node", "manage:node", "manage:certificate", []string{"ballooning-target", "wakeonlan"}},
		{"the ACME settings", (*nodeConfigWorld).acmeGET, "view:certificate", "manage:certificate", "manage:node", []string{"acme", "acmedomain0"}},
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
				{"the write permission of another resource", []scopedGrant{view, {permission: rd.other}}, nil, false},
				{"viewing, whatever else is held", []scopedGrant{view, {permission: "view:audit"}, {permission: "view:vm"}}, nil, false},
			} {
				t.Run(tt.name, func(t *testing.T) {
					engine := &scopedRBACEngine{grants: tt.grants, globalOnly: tt.globalOnly}
					pve := newNodeConfigPVE(t)
					pve.setFile(testNodeName, nodeConfigNotes, nodeConfigSettings())
					w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{auth: authWithEngine(engine)})

					// The settings are the Viewer's whatever the token is.
					body := w.mustRead(t, rd.read(w, testNodeName))
					_, keys := decodeKeys(t, body)
					want := slices.Clone(rd.visible)
					if tt.wantToken {
						want = append(want, "digest")
						requireSaveToken(t, body, pve.digestOf(testNodeName))
					}
					slices.Sort(want)
					if !slices.Equal(keys, want) {
						t.Errorf("the body carries the keys %v, want exactly %v: %s", keys, want, clipForFailure(string(body), 300))
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

// TestNodeConfigSaveWithTheToken: a save that sends back the token a read returned is
// checked against the file as it is now, and what goes to Proxmox is the file's digest
// as it is now, in Proxmox's own form, so that Proxmox's own check still covers the
// moment between Nexara's read and its write. Neither the token nor the digest is in
// the audit row, which every Viewer reads.
func TestNodeConfigSaveWithTheToken(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		t.Run(wr.name, func(t *testing.T) {
			x := newSaveRun(t, wr)
			rawBefore := x.pve.digestOf(testNodeName)
			mark := x.pve.mark()

			if status, body := x.w.send(t, wr.write(x.w, testNodeName, withDigest(wr.change, x.token))); status != fiber.StatusOK {
				t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
			}
			sent := x.pve.since(mark)
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
				t.Errorf("Proxmox was sent the digest %q, want the file's own, %q: the token must not reach it, and neither may nothing", got, rawBefore)
			}
			wantKeys := append([]string{"digest"}, slices.Collect(maps.Keys(wr.change))...)
			slices.Sort(wantKeys)
			if got := slices.Sorted(maps.Keys(form)); !slices.Equal(got, wantKeys) {
				t.Errorf("Proxmox was sent the keys %v, want %v", got, wantKeys)
			}
			if x.pve.digestOf(testNodeName) == rawBefore {
				t.Error("the file is unchanged after a save that went through")
			}

			row := oneAccessAuditRow(t, x.w.store.auditRows())
			if row.resourceType != wr.auditType || row.action != wr.auditAction || row.resourceID != testNodeName {
				t.Errorf("the audit row is (%q, %q, %q), want (%s, %s, %s)",
					row.resourceType, row.resourceID, row.action, wr.auditType, testNodeName, wr.auditAction)
			}
			for _, secret := range []string{x.token, rawBefore, x.pve.digestOf(testNodeName)} {
				if strings.Contains(row.details, secret) {
					t.Errorf("the audit details %s carry %q, which view:audit would show every Viewer", row.details, secret)
				}
			}
		})
	}

	t.Run("a token read through the notes saves through either route", func(t *testing.T) {
		for _, wr := range nodeConfigWriters {
			x := newSaveRun(t, wr)
			token := x.w.tokenFrom(t, x.w.notesGET(testNodeName))
			if status, body := x.w.send(t, wr.write(x.w, testNodeName, withDigest(wr.change, token))); status != fiber.StatusOK {
				t.Errorf("%s: status = %d (%s), want 200: the three routes are one file's compare-and-swap", wr.name, status, clipForFailure(string(body), 300))
			}
		}
	})

	t.Run("a save through one route moves the file under every token read before it", func(t *testing.T) {
		x := newSaveRun(t, optionsWriter)
		token := x.w.tokenFrom(t, x.w.notesGET(testNodeName))
		if status, body := x.w.send(t, optionsWriter.write(x.w, testNodeName, withDigest(optionsWriter.change, token))); status != fiber.StatusOK {
			t.Fatalf("the first save: status = %d (%s), want 200", status, clipForFailure(string(body), 300))
		}
		x.wr = acmeWriter
		x.requireRefused(nil, "", withDigest(acmeWriter.change, token), refusal{status: fiber.StatusConflict, exact: nodeConfigChangedAnswer, sent: []string{http.MethodGet}})
	})
}

// TestNodeConfigSaveRefusesATokenThatIsNotTheFiles: whatever is sent as the digest
// that is not the token for THIS node of THIS cluster, under THIS key, for the file as
// it is NOW, is a 409 in the words of Proxmox's own refusal, with nothing written and
// no audit row, and the only request to reach Proxmox is the read of the file's digest.
// That includes Proxmox's own digest, which no read of Nexara's returns, so a caller
// who has one from elsewhere cannot use it to probe the file: every guess is the same
// 409. (The comparison itself is pinned in the handlers' TestNodeConfigTokenMatches;
// these are the inputs the route feeds it, and the spellings the schema must let by.)
func TestNodeConfigSaveRefusesATokenThatIsNotTheFiles(t *testing.T) {
	spell := func(f func(token string) string) func(*testing.T, *saveRun) string {
		return func(_ *testing.T, x *saveRun) string { return f(x.token) }
	}
	for _, tt := range []struct {
		name string
		// target is the node the save is for; testNodeName when empty.
		target string
		// via is the world the save is sent through; the one the token was read in when nil.
		via func(t *testing.T, x *saveRun) *nodeConfigWorld
		// digest returns what the save sends; it may first change the files.
		digest func(t *testing.T, x *saveRun) string
	}{
		{name: "Proxmox's own digest of the file", digest: func(_ *testing.T, x *saveRun) string { return x.pve.digestOf(testNodeName) }},
		{name: "the token of another node holding the same file", digest: func(t *testing.T, x *saveRun) string {
			x.pve.setFile(otherTestNodeName, nodeConfigNotes, nodeConfigSettings())
			return x.w.tokenFrom(t, x.wr.read(x.w, otherTestNodeName))
		}},
		// And the other way round: a check that compared against one fixed node, and not
		// the one in the path, would pass the case above and fail this one.
		{name: "the token of this node sent for another node holding the same file", target: otherTestNodeName,
			digest: func(_ *testing.T, x *saveRun) string {
				x.pve.setFile(otherTestNodeName, nodeConfigNotes, nodeConfigSettings())
				return x.token
			}},
		{name: "the token of this node sent for a node with no config file", target: noFileNodeName, digest: spell(func(tok string) string { return tok })},
		{name: "the token of another cluster for the same node and file", digest: func(t *testing.T, x *saveRun) string {
			other := newNodeConfigWorld(t, x.pve, nodeConfigWorldOptions{clusterID: otherTestClusterID})
			return other.tokenFrom(t, x.wr.read(other, testNodeName))
		}},
		{name: "the token of this cluster sent through another cluster for the same node and file",
			via: func(t *testing.T, x *saveRun) *nodeConfigWorld {
				return newNodeConfigWorld(t, x.pve, nodeConfigWorldOptions{clusterID: otherTestClusterID})
			},
			digest: spell(func(tok string) string { return tok })},
		{name: "a token made under another ENCRYPTION_KEY", digest: func(t *testing.T, x *saveRun) string {
			other := newNodeConfigWorld(t, x.pve, nodeConfigWorldOptions{key: otherTestEncKey})
			return other.tokenFrom(t, x.wr.read(other, testNodeName))
		}},
		{name: "a token made under this ENCRYPTION_KEY sent through another one",
			via: func(t *testing.T, x *saveRun) *nodeConfigWorld {
				return newNodeConfigWorld(t, x.pve, nodeConfigWorldOptions{key: otherTestEncKey})
			},
			digest: spell(func(tok string) string { return tok })},
		{name: "the token of the file as it was before it changed", digest: func(_ *testing.T, x *saveRun) string {
			x.pve.setNotes(testNodeName, "edited elsewhere\n")
			return x.token
		}},
		{name: "the token of a version that does not exist", digest: spell(func(tok string) string { return "v2." + strings.TrimPrefix(tok, "v1.") })},
		{name: "the token without its version", digest: spell(func(tok string) string { return strings.TrimPrefix(tok, "v1.") })},
		{name: "the token with its last character cut", digest: spell(func(tok string) string { return tok[:len(tok)-1] })},
		{name: "the token with a character added", digest: spell(func(tok string) string { return tok + "A" })},
		{name: "the token with a space in front", digest: spell(func(tok string) string { return " " + tok })},
		{name: "the token with a line break after", digest: spell(func(tok string) string { return tok + "\n" })},
		{name: "the token in capitals", digest: spell(strings.ToUpper)},
		{name: "the version and nothing else", digest: spell(func(string) string { return "v1." })},
		{name: "43 characters of the right alphabet that are no tag", digest: spell(func(string) string { return "v1." + strings.Repeat("A", 43) })},
		{name: "a string that is not a token at all", digest: spell(func(string) string { return "not-a-token" })},
		{name: "the longest string the parameter allows", digest: spell(func(string) string { return strings.Repeat("a", 128) })},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, wr := range nodeConfigWriters {
				t.Run(wr.name, func(t *testing.T) {
					x := newSaveRun(t, wr)
					var sender *nodeConfigWorld
					if tt.via != nil {
						sender = tt.via(t, x)
					}
					digest := tt.digest(t, x)
					x.requireRefused(sender, tt.target, withDigest(wr.change, digest),
						refusal{status: fiber.StatusConflict, exact: nodeConfigChangedAnswer, sent: []string{http.MethodGet}})
				})
			}
		})
	}
}

// TestNodeConfigFirstSaveCreatesTheFile: a node with no config file has no digest to
// make a token of, so its reads hand out none, and a save that names none creates the
// file, after which the node has a digest and the reads hand out a token.
func TestNodeConfigFirstSaveCreatesTheFile(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		t.Run(wr.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			if body := w.mustRead(t, wr.read(w, testNodeName)); string(body) != `{}` {
				t.Fatalf("a read of a node with no file answers %s, want {}", body)
			}
			if status, body := w.send(t, wr.write(w, testNodeName, wr.change)); status != fiber.StatusOK {
				t.Fatalf("the first save, with no token: status = %d (%s), want 200", status, clipForFailure(string(body), 300))
			}
			requireSaveToken(t, w.mustRead(t, wr.read(w, testNodeName)), pve.digestOf(testNodeName))
		})
	}
}

// TestNodeConfigSaveWithoutATokenIsUnconditional: no digest, or an empty one, is a
// save that is not based on a read, as it always was: one request, the write, no read
// of the digest first, and no digest in what Proxmox is sent.
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
				x := newSaveRun(t, wr)
				// The file has moved on since anybody read it, which an unconditional save does not care about.
				x.pve.setNotes(testNodeName, "edited elsewhere\n")
				mark := x.pve.mark()

				if status, body := x.w.send(t, wr.write(x.w, testNodeName, tt.fields)); status != fiber.StatusOK {
					t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
				}
				sent := x.pve.since(mark)
				if len(sent) != 1 || sent[0].method != http.MethodPut {
					t.Fatalf("Proxmox received %+v, want exactly the one write", sent)
				}
				if _, has := sent[0].form["digest"]; has {
					t.Errorf("the write carries a digest, %v: nothing was asked to be compared", sent[0].form["digest"])
				}
				if rows := x.w.store.auditRows(); len(rows) != 1 {
					t.Errorf("the save wrote %d audit rows, want 1", len(rows))
				}
			})
		}
	}
}

// TestNodeConfigProxmoxsOwnCheckStillCatchesAFileThatMovesAfterTheTokenMatched: the
// token is checked against a read and the write follows it; a change in between is what
// Proxmox's assert_if_modified is for, which is why the FRESH digest is sent and not
// nothing. It answers with its plain 500, mapped to the same 409 in the same words.
func TestNodeConfigProxmoxsOwnCheckStillCatchesAFileThatMovesAfterTheTokenMatched(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		t.Run(wr.name, func(t *testing.T) {
			x := newSaveRun(t, wr)
			// The token's read was the first GET, so the handler's is the second: the file
			// changes the moment after it has been answered.
			x.pve.changeAfterRead(2, func(files map[string]*nodeConfigFile) { files[testNodeName].notes = "edited elsewhere\n" })

			sent := x.requireRefused(nil, "", withDigest(wr.change, x.token),
				refusal{status: fiber.StatusConflict, exact: nodeConfigChangedAnswer, sent: []string{http.MethodGet, http.MethodPut}, moved: true})
			if len(sent) == 2 && sent[1].form.Get("digest") == "" {
				t.Error("the write carried no digest, so Proxmox had nothing to refuse it by")
			}
		})
	}
}

// TestNodeConfigSaveWhenTheDigestCannotBeRead: the save check reads the file's digest
// from Proxmox, and when that read is refused the save is not made. The failure is the
// read's own, in the words every other read of the node config gets (a token without
// Sys.Audit is a 403 that names it, an unreachable node a gateway failure), not a 409
// that a caller would answer by reloading, and not a write that went ahead for want
// of something to compare.
func TestNodeConfigSaveWhenTheDigestCannotBeRead(t *testing.T) {
	for _, tt := range []struct {
		name         string
		pveStatus    int
		pveBody      string
		want         int
		wantFragment string
	}{
		{"a token without Sys.Audit", http.StatusForbidden, `Permission check failed (/, Sys.Audit)`, fiber.StatusForbidden, "Sys.Audit"},
		{"an unreachable node", http.StatusInternalServerError, `{"data":null,"message":"hostname lookup failed - no route to node\n"}`, fiber.StatusBadGateway, ""},
	} {
		for _, wr := range nodeConfigWriters {
			t.Run(tt.name+", "+wr.name, func(t *testing.T) {
				x := newSaveRun(t, wr)
				x.pve.failReads(tt.pveStatus, tt.pveBody)
				x.requireRefused(nil, "", withDigest(wr.change, x.token), refusal{status: tt.want, message: tt.wantFragment, sent: []string{http.MethodGet}})
			})
		}
	}
}

// TestNodeConfigSaveRefusalsCostNoRead: what Nexara can refuse about the request
// itself (a key set and cleared, a key that cannot be cleared, a save that changes
// nothing, a control character) is refused 400 before anything is sent, with a token or
// without, so a request that is going to fail does not cost Proxmox a read. An ACME
// save of nothing (an empty body, fields left empty, which mean "leave alone", an
// empty list to clear, a token alone) is refused ahead of the re-read and any write:
// Proxmox would rewrite the whole file for it, moving the digest under every open
// dialog, and the audit would be left a row that names nothing.
func TestNodeConfigSaveRefusalsCostNoRead(t *testing.T) {
	stranger := "v1." + strings.Repeat("A", 43)
	plain := func(m map[string]any) func(string) map[string]any { return func(string) map[string]any { return m } }
	signed := func(m map[string]any) func(string) map[string]any {
		return func(token string) map[string]any { return withDigest(m, token) }
	}
	tokenAlone := func(token string) map[string]any { return map[string]any{"digest": token} }
	const nothing = "nothing to change"

	for _, tt := range []struct {
		name    string
		wr      nodeConfigWriter
		fields  func(token string) map[string]any
		message string
	}{
		{"the options: set and cleared", optionsWriter, signed(map[string]any{"ballooning-target": 0, "delete": []string{"ballooning-target"}}), "set and cleared"},
		{"the options: a save that changes nothing", optionsWriter, signed(map[string]any{}), nothing},
		{"the options: a control character in a location", optionsWriter,
			signed(map[string]any{"location": "latitude=0,longitude=0,name=rack\u001b01"}), "control character"},
		{"the options: an ACME key to clear", optionsWriter, signed(map[string]any{"delete": []string{"acmedomain0"}}), "ACME"},
		{"the ACME settings: set and cleared", acmeWriter,
			signed(map[string]any{"acmedomain0": "node.example.com", "delete": []string{"acmedomain0"}}), "set and cleared"},
		{"the ACME settings: a key that is no ACME setting cannot be cleared", acmeWriter, signed(map[string]any{"delete": []string{"wakeonlan"}}), ""},
		{"the ACME settings: an empty body", acmeWriter, plain(map[string]any{}), nothing},
		{"the ACME settings: a save token alone", acmeWriter, tokenAlone, nothing},
		{"the ACME settings: a stranger's token alone", acmeWriter, plain(map[string]any{"digest": stranger}), nothing},
		{"the ACME settings: empty fields, which leave the stored values alone", acmeWriter, plain(map[string]any{"acme": "", "acmedomain0": ""}), nothing},
		{"the ACME settings: empty fields and a save token", acmeWriter, signed(map[string]any{"acme": "", "acmedomain3": ""}), nothing},
		{"the ACME settings: an empty list to clear", acmeWriter, plain(map[string]any{"delete": []string{}}), nothing},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x := newSaveRun(t, tt.wr)
			x.requireRefused(nil, "", tt.fields(x.token), refusal{status: fiber.StatusBadRequest, message: tt.message})
		})
	}
}

// TestNodeConfigSaveRefusalsComeAfterTheClusterIsResolved: the client's refusals, and the read
// of the digest a token takes, sit behind createProxmoxClient. The route sweep
// (registry_route_sweep_test.go) sends every declared parameter at once, `delete` included,
// and in its required-only pass none, which is a write that changes nothing; it expects the
// handler to get as far as its dependencies before anything fails, and a refusal ahead of the
// cluster lookup would answer either with a 400 that has nothing to do with the registry.
func TestNodeConfigSaveRefusalsComeAfterTheClusterIsResolved(t *testing.T) {
	token := "v1." + strings.Repeat("A", 43)
	for _, tt := range []struct {
		name   string
		wr     nodeConfigWriter
		fields map[string]any
	}{
		{"the options: set and cleared, an ACME key to clear and a line break, all at once", optionsWriter,
			map[string]any{"ballooning-target": 0, "delete": []string{"ballooning-target", "acmedomain0"}, "wakeonlan": "02:00:00:00:00:01,\n"}},
		{"the options: a request that changes nothing", optionsWriter, map[string]any{}},
		{"the options: empty strings alone", optionsWriter, map[string]any{"wakeonlan": "", "location": "", "description": ""}},
		{"the options: a control character in a location", optionsWriter, map[string]any{"location": "latitude=0,longitude=0,name=rack\u001b01"}},
		{"the options: a save token with a request that changes nothing", optionsWriter, map[string]any{"digest": token}},
		{"the options: a save token on an ordinary save", optionsWriter, map[string]any{"ballooning-target": 75, "digest": token}},
		{"the ACME settings: set and cleared", acmeWriter, map[string]any{"acmedomain0": "node.example.com", "delete": []string{"acmedomain0"}}},
		{"the ACME settings: a key that is no ACME setting", acmeWriter, map[string]any{"delete": []string{"wakeonlan"}}},
		{"the ACME settings: a control character in a setting", acmeWriter, map[string]any{"acmedomain0": "domain=a.example.com,\r"}},
		{"the ACME settings: a save token on an ordinary save", acmeWriter, map[string]any{"acmedomain0": "node.example.com", "digest": token}},
		{"the ACME settings: nothing at all", acmeWriter, map[string]any{}},
		{"the ACME settings: a save token alone", acmeWriter, map[string]any{"digest": token}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			w.store.failClusterRead(1, pgx.ErrNoRows)

			status, body := w.send(t, tt.wr.write(w, testNodeName, tt.fields))
			if status != fiber.StatusNotFound {
				t.Fatalf("status = %d (%s), want the cluster's 404 — a 400 means a refusal now runs before the cluster is resolved", status, clipForFailure(string(body), 300))
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

// TestNodeConfigACMESaveRefusesControlCharacters: a PUT .../acme-config whose acme or
// acmedomainN holds a line break or any other control character is refused 400, with a token
// or without, ahead of everything that would cost Proxmox a request. Proxmox's own checks
// would let it through (an acmedomain of "domain=a.example.com,\r" is valid and is written
// with its "\r") or answer a "\n" with a plain 500 the caller is told is a 502. The answer
// is the client's refusal, not the registry's (the declaration bounds a value's length and
// nothing of what it holds), and the words tell the two apart. A body carries a C0 character
// as a JSON escape and DEL, the C1 range and U+2028/9 as themselves. Each refusal has a
// control on the same setup: the same setting without the character is saved, so "nothing
// reached Proxmox" is something the route can be seen to do otherwise.
func TestNodeConfigACMESaveRefusesControlCharacters(t *testing.T) {
	// The settings are the declaration's own (everything on the PUT that is neither a
	// path parameter nor plumbing), so a key added to the route is held to this test too.
	var keys []string
	for name := range declaredEndpoint(t, fiber.MethodPut, acmeConfigPath).Parameters {
		if !slices.Contains([]string{"cluster_id", "node", "delete", "digest"}, name) {
			keys = append(keys, name)
		}
	}
	slices.Sort(keys)
	if len(keys) == 0 {
		t.Fatal("PUT .../acme-config declares no setting to hold the test to")
	}
	// base is a value the key could hold, cut off where a control character does its
	// harm: an acmedomainN ends in the comma after which Proxmox skips a whitespace-only
	// segment, and acme in the domain list it splits on any whitespace.
	base := func(key string) string {
		if key == "acme" {
			return "account=default,domains=a.example.com;b.example.com"
		}
		return "domain=a.example.com,"
	}
	refused := func(t *testing.T, key, value string, withToken bool) {
		t.Helper()
		x := newSaveRun(t, acmeWriter)
		fields := map[string]any{key: value}
		if withToken {
			fields = withDigest(fields, x.token)
		}
		sent := x.requireRefused(nil, "", fields, refusal{status: fiber.StatusBadRequest, message: `"` + key + `" cannot contain a line break or a control character`})
		if len(sent) != 0 {
			t.Errorf("the read of the digest was spent on a refusal Nexara makes: %+v", sent)
		}
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			refused(t, key, base(key)+"\r", true)

			t.Run("the same value without the control character is saved", func(t *testing.T) {
				x := newSaveRun(t, acmeWriter)
				fileBefore, mark := x.pve.digestOf(testNodeName), x.pve.mark()
				if status, body := x.w.send(t, x.w.acmePUT(testNodeName, withDigest(map[string]any{key: base(key)}, x.token))); status != fiber.StatusOK {
					t.Fatalf("status = %d (%s), want 200", status, clipForFailure(string(body), 300))
				}
				sent := x.pve.since(mark)
				if len(sent) != 2 || sent[0].method != http.MethodGet || sent[1].method != http.MethodPut {
					t.Fatalf("Proxmox received %+v, want the digest's read and then the save", sent)
				}
				if got := sent[1].form.Get(key); got != base(key) {
					t.Errorf("Proxmox was sent %s = %q, want %q", key, got, base(key))
				}
				if x.pve.digestOf(testNodeName) == fileBefore {
					t.Error("the file is unchanged after a save that went through")
				}
				if rows := x.w.store.auditRows(); len(rows) != 1 {
					t.Errorf("the save wrote %d audit rows, want 1", len(rows))
				}
			})
		})
	}

	for _, key := range []string{"acme", "acmedomain0"} {
		for _, ch := range []struct{ name, char string }{
			{"a line feed", "\n"}, {"a tab", "\t"}, {"a NUL", "\x00"}, {"ESC", "\x1b"}, {"DEL", "\x7f"},
			{"U+0085, the next-line character", "\u0085"}, {"U+2028, the line separator", "\u2028"}, {"U+2029, the paragraph separator", "\u2029"},
		} {
			t.Run(key+"/"+ch.name, func(t *testing.T) { refused(t, key, base(key)+ch.char, true) })
		}
		// Without a token there is no read of the digest to spare: the write is what is refused.
		t.Run(key+"/without a save token", func(t *testing.T) { refused(t, key, base(key)+"\r", false) })
	}
}

// TestNodeConfigSaveOfANodeWhoseFileWasEmptied: a node whose file has been emptied
// (every setting and the notes cleared) is, to Proxmox, a node with no file: its read
// is an empty config with no digest. So it is handed no token, a save that names one
// is a 409 with only the read of the digest spent, and the save that follows with none
// is unconditional, after which the node has a digest and a token again.
func TestNodeConfigSaveOfANodeWhoseFileWasEmptied(t *testing.T) {
	for _, tt := range []struct {
		wr    nodeConfigWriter
		keys  map[string]string
		empty map[string]any
		again map[string]any
	}{
		{optionsWriter, map[string]string{"ballooning-target": "75"}, map[string]any{"delete": []string{"ballooning-target"}}, map[string]any{"ballooning-target": 60}},
		{acmeWriter, map[string]string{"acmedomain0": "node.example.com"}, map[string]any{"delete": []string{"acmedomain0"}}, map[string]any{"acmedomain1": "other.example.com"}},
	} {
		t.Run(tt.wr.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, "", tt.keys)
			x := &saveRun{t: t, pve: pve, w: newNodeConfigWorld(t, pve, nodeConfigWorldOptions{}), wr: tt.wr}
			x.token = x.w.tokenFrom(t, tt.wr.read(x.w, testNodeName))

			// The save that empties the file, with the token its read returned.
			if status, body := x.w.send(t, tt.wr.write(x.w, testNodeName, withDigest(tt.empty, x.token))); status != fiber.StatusOK {
				t.Fatalf("the save that empties the file: status = %d (%s), want 200", status, clipForFailure(string(body), 300))
			}
			if got := pve.digestOf(testNodeName); got != "" {
				t.Fatalf("the file still has the digest %q; the case did not empty it", got)
			}
			// The file is there and holds nothing; a read does not tell it from no file.
			if status, body := x.w.send(t, tt.wr.read(x.w, testNodeName)); status != fiber.StatusOK || string(body) != `{}` {
				t.Errorf("a read of the emptied file answers %d %s, want 200 {}: no digest, so no token", status, clipForFailure(string(body), 300))
			}
			// The token it was emptied with is of a file that is not there any more.
			x.requireRefused(nil, "", withDigest(tt.again, x.token), refusal{status: fiber.StatusConflict, exact: nodeConfigChangedAnswer, sent: []string{http.MethodGet}})
			// The next save is unconditional, and gives the node a digest again.
			if status, body := x.w.send(t, tt.wr.write(x.w, testNodeName, tt.again)); status != fiber.StatusOK {
				t.Fatalf("a save with no token: status = %d (%s), want 200", status, clipForFailure(string(body), 300))
			}
			requireSaveToken(t, x.w.mustRead(t, tt.wr.read(x.w, testNodeName)), pve.digestOf(testNodeName))
		})
	}
}

// TestNodeConfigSaveIsGatedBeforeTheTokenIsLooked: a caller who may not write is
// refused by the declaration's gate, and nothing, not even the read of the digest,
// reaches Proxmox for them, so the token is not something a Viewer can use to make
// Nexara read the file on their behalf.
func TestNodeConfigSaveIsGatedBeforeTheTokenIsLooked(t *testing.T) {
	for _, wr := range nodeConfigWriters {
		t.Run(wr.name, func(t *testing.T) {
			x := newSaveRun(t, wr)
			viewer := newNodeConfigWorld(t, x.pve, nodeConfigWorldOptions{auth: stubAuth(grantsOf("view:node", "view:certificate"))})
			mark := x.pve.mark()
			if status, body := viewer.send(t, wr.write(viewer, testNodeName, withDigest(wr.change, x.token))); status != fiber.StatusForbidden {
				t.Fatalf("status = %d (%s), want 403", status, clipForFailure(string(body), 300))
			}
			if sent := x.pve.since(mark); len(sent) != 0 {
				t.Errorf("a refused save reached Proxmox: %+v", sent)
			}
		})
	}
}

// TestNodeConfigSaveRefusesABodyProxmoxWouldRefuse: the client refuses an encoded body
// over 512 KiB before sending it (Proxmox answers anything over its post limit 501,
// after reading no more than the head), and the route makes that a 413 in the words a
// 501 already got. The limit is on the ENCODED form, where a non-ASCII character takes
// up to nine bytes, so notes well inside the declared bound of 65536 characters are
// over it; and the digest a save sends counts toward it.
func TestNodeConfigSaveRefusesABodyProxmoxWouldRefuse(t *testing.T) {
	const limit = 512 * 1024
	// "description=" is 12 bytes, and each "東" is 9 once encoded: the form is 12+9k+m
	// bytes for k of them and m of "a".
	notesOfEncodedSize := func(total int) string {
		k := (total - 12) / 9
		return strings.Repeat("東", k) + strings.Repeat("a", total-12-9*k)
	}
	// The digest Proxmox is sent is "&digest=" and 40 hex characters. The pre-save
	// validation counts it as that whatever the token was, so a body the digest takes
	// over the limit is refused 413 before the re-read, with a token that would have
	// matched and with one that would not, and a body that fits with it goes on to the
	// re-read, where a token that does not match is a 409.
	const digestBytes = 1 + len("digest=") + 40
	stranger := "v1." + strings.Repeat("A", 43)

	for _, tt := range []struct {
		name string
		// size is the form's size without the digest; token is what the save sends:
		// "" for no digest, matching for the one the node's read returned.
		size        int
		token       string
		wantStatus  int
		wantRequest []string
	}{
		{"without a token, exactly at the limit", limit, "", fiber.StatusOK, []string{"PUT"}},
		{"without a token, one byte over", limit + 1, "", fiber.StatusRequestEntityTooLarge, nil},
		{"exactly at the limit once the digest is added", limit - digestBytes, "matching", fiber.StatusOK, []string{"GET", "PUT"}},
		{"one byte over once the digest is added", limit - digestBytes + 1, "matching", fiber.StatusRequestEntityTooLarge, nil},
		{"one byte over once the digest is added, with a token that would not match", limit - digestBytes + 1, stranger, fiber.StatusRequestEntityTooLarge, nil},
		{"exactly at the limit once the digest is added, with a token that does not match", limit - digestBytes, stranger, fiber.StatusConflict, []string{"GET"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pve := newNodeConfigPVE(t)
			pve.setFile(testNodeName, "", nodeConfigSettings())
			w := newNodeConfigWorld(t, pve, nodeConfigWorldOptions{})
			fields := map[string]any{"description": notesOfEncodedSize(tt.size)}
			if tt.token != "" {
				token := tt.token
				if token == "matching" {
					token = w.tokenFrom(t, w.optionsGET(testNodeName))
				}
				fields = withDigest(fields, token)
			}
			if got := len(url.Values{"description": {fields["description"].(string)}}.Encode()); got != tt.size {
				t.Fatalf("the case builds a form of %d bytes, not %d", got, tt.size)
			}
			mark := pve.mark()

			status, body := w.send(t, w.optionsPUT(testNodeName, fields))
			if status != tt.wantStatus {
				t.Fatalf("status = %d (%s), want %d", status, clipForFailure(string(body), 300), tt.wantStatus)
			}
			sent := pve.since(mark)
			methods := make([]string, len(sent))
			for i, r := range sent {
				methods[i] = r.method
			}
			if !slices.Equal(methods, tt.wantRequest) {
				t.Fatalf("Proxmox received %v, want %v", methods, tt.wantRequest)
			}
			if tt.wantStatus == fiber.StatusOK {
				if got := len(sent[len(sent)-1].form.Encode()); got != limit {
					t.Errorf("Proxmox was sent %d bytes, want exactly the limit, %d", got, limit)
				}
				return
			}
			if tt.wantStatus == fiber.StatusRequestEntityTooLarge &&
				(!strings.Contains(string(body), "too large for Proxmox") || !strings.Contains(string(body), "64 KiB")) {
				t.Errorf("the answer %s does not say what the limit is", clipForFailure(string(body), 300))
			}
			if rows := w.store.auditRows(); len(rows) != 0 {
				t.Errorf("a save that was refused wrote %d audit row(s), want none", len(rows))
			}
		})
	}

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
