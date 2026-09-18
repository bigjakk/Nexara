package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/handlers"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to
// registry_storage.go that quietly loosened a parameter would show up here.

// storageRouteCount is how many endpoints registerStorageEndpoints
// declares. See vmRouteCount in registry_vms_test.go for why the registry
// total is a sum of per-domain constants rather than one number.
//
// TWELVE, not thirteen: DELETE .../storage/:storage_id/content/* is still
// registered in router.go because its volume id is a greedy wildcard the
// parameter schema cannot describe. See
// TestStorageDeleteContentIsStillLegacy, which pins that on purpose rather
// than leaving it to be noticed as a gap.
const storageRouteCount = 12

const testStorageID = "9e8d7c6b-5a49-4382-9271-000000000012"

// storageRoute renders one storage route's path with the test ids
// substituted.
func storageRoute(path string) string {
	return strings.NewReplacer(
		":cluster_id", testClusterID,
		":storage_id", testStorageID,
	).Replace(path)
}

// storageRoutesOutsideTheClusterCheckShape is this domain's half of the
// registry-wide exception list in registry_vms_test.go.
//
// Exactly one entry, and it is the only route in either Phase 6d domain
// that does not hoist.
var storageRoutesOutsideTheClusterCheckShape = map[string]string{
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/upload": "Deferred: the grant depends on the " +
		"content type, which does not exist until the multipart body is being read",
}

// storageLegacyPermissions is what each handler checked with hand-placed
// calls BEFORE Phase 6d, transcribed from `git show HEAD:` over
// internal/api/handlers/storage.go and storage_templates.go at commit
// be1379f: 12 requireClusterPerm calls and 2 hasClusterPerm calls across
// the 13 routes.
//
// shape says what the declaration must be, and calls is how many
// permission calls the handler made. Eleven of the twelve migrated routes
// hoist their single call into middleware; the upload keeps BOTH of its
// own, which is what Deferred means. The thirteenth route's call is not in
// this table at all — see TestStorageDeleteContentIsStillLegacy.
var storageLegacyPermissions = map[string]struct {
	permission string
	shape      string
	calls      int
}{
	"GET /api/v1/clusters/:cluster_id/storage":                           {"view:storage", "Check", 1},
	"POST /api/v1/clusters/:cluster_id/storage":                          {"manage:storage", "Check", 1},
	"GET /api/v1/clusters/:cluster_id/storage/:storage_id/config":        {"view:storage", "Check", 1},
	"PUT /api/v1/clusters/:cluster_id/storage/:storage_id":               {"manage:storage", "Check", 1},
	"DELETE /api/v1/clusters/:cluster_id/storage/:storage_id":            {"delete:storage", "Check", 1},
	"GET /api/v1/clusters/:cluster_id/storage/:storage_id/content":       {"view:storage", "Check", 1},
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/upload":       {"manage:storage", "Deferred", 2},
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/oci-pull":     {"manage:storage", "Check", 1},
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/download-url": {"manage:storage", "Check", 1},
	"POST /api/v1/clusters/:cluster_id/storage/:storage_id/appliances":   {"manage:storage", "Check", 1},
	"GET /api/v1/clusters/:cluster_id/appliances":                        {"view:storage", "Check", 1},
	"GET /api/v1/clusters/:cluster_id/scan/iscsi":                        {"manage:storage", "Check", 1},
}

// declaredStorageEndpoints returns every declaration in this domain, keyed
// "METHOD path".
func declaredStorageEndpoints(t *testing.T) map[string]Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	out := map[string]Endpoint{}
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if _, ours := storageLegacyPermissions[key]; ours {
			out[key] = e
		}
	}
	return out
}

// TestStorageRoutesDeclareTheSamePermissionTheyEnforced is the tally that
// makes this migration a refactor rather than a change.
func TestStorageRoutesDeclareTheSamePermissionTheyEnforced(t *testing.T) {
	declared := declaredStorageEndpoints(t)
	if len(declared) != storageRouteCount {
		t.Fatalf("the registry declares %d storage routes, want %d", len(declared), storageRouteCount)
	}
	if len(storageLegacyPermissions) != storageRouteCount {
		t.Fatalf("storageLegacyPermissions has %d entries, want %d — the table must cover every route",
			len(storageLegacyPermissions), storageRouteCount)
	}

	var hoisted, kept int
	byPermission := map[string]int{}
	for key, want := range storageLegacyPermissions {
		e, ok := declared[key]
		if !ok {
			t.Errorf("%s was gated %s before the migration but is not declared in the registry", key, want.permission)
			continue
		}
		switch want.shape {
		case "Check":
			hoisted += want.calls
			if e.Permissions.Check == nil {
				t.Errorf("%s declares %q rather than a Check; its cluster is the first parameter in its own path",
					key, e.Permissions.Describe())
				continue
			}
			if got := e.Permissions.Describe(); got != want.permission {
				t.Errorf("%s declares %q but the handler enforced %q before the migration", key, got, want.permission)
			}
			if e.Permissions.Check.Scope != ScopeCluster {
				t.Errorf("%s is %s-scoped; requireClusterPerm resolved the cluster from the path",
					key, e.Permissions.Check.Scope)
			}
			byPermission[want.permission]++
		case "Deferred":
			kept += want.calls
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, want Deferred: its grant depends on the multipart body",
					key, e.Permissions.Describe())
				continue
			}
			// A Deferred route renders as the bare word "deferred", so the
			// permissions an operator needs have to survive in the prose —
			// BOTH of them, since holding only one of the two opens only part
			// of the endpoint.
			for _, perm := range []string{"manage:storage", "manage:vm_import"} {
				if !strings.Contains(e.Description, perm) {
					t.Errorf("%s is Deferred but its Description never names %q, so the docs tell an "+
						"operator building a role nothing: %q", key, perm, e.Description)
				}
			}
			// And the reason has to name what the handler actually does, so
			// the next reader can go and check that it still does it.
			if !strings.Contains(e.Permissions.Deferred, "hasClusterPerm") {
				t.Errorf("%s: the Deferred reason does not name hasClusterPerm, which is what makes the "+
					"decision: %q", key, e.Permissions.Deferred)
			}
		default:
			t.Fatalf("%s: unknown shape %q in the tally", key, want.shape)
		}
	}
	if hoisted != 11 || kept != 2 {
		t.Errorf("the tally moves %d permission call(s) into middleware and keeps %d in handlers, want 11 / 2",
			hoisted, kept)
	}

	// The per-permission breakdown of the hoisted eleven, so a failure says
	// WHICH pair drifted rather than only that the total moved.
	for _, tt := range []struct {
		permission string
		routes     int
	}{
		{"view:storage", 4},
		{"manage:storage", 6},
		{"delete:storage", 1},
	} {
		if byPermission[tt.permission] != tt.routes {
			t.Errorf("%d routes declare %s, want %d", byPermission[tt.permission], tt.permission, tt.routes)
		}
	}

}

// TestEveryDeclaredStorageRouteIsInTheTally is the other direction of the
// tally, and it is a SEPARATE test on purpose.
//
// Two things would make it vacuous if it lived inside the tally above.
// Reading `declared` rather than the registry is the first:
// declaredStorageEndpoints builds that map by filtering on membership in
// storageLegacyPermissions, so every key in it is listed by construction
// and the branch could never fire. Sharing the tally's body is the second:
// the tally opens with two t.Fatalf count checks, and a newly declared
// route trips the first of them — so the loop would never be reached in
// the one situation it exists for, and would report the count instead of
// naming the route.
//
// Two of this domain's routes hang off the cluster rather than off
// storageScope, so they are named rather than swept by a prefix.
func TestEveryDeclaredStorageRouteIsInTheTally(t *testing.T) {
	clusterRooted := map[string]bool{
		"GET " + clusterScope + "/appliances": true,
		"GET " + clusterScope + "/scan/iscsi": true,
	}
	s := newRouteStubServer(t)
	seen := 0
	for _, e := range s.registry.Endpoints() {
		key := e.Method + " " + e.Path
		if !strings.HasPrefix(e.Path, storageScope) && !clusterRooted[key] {
			continue
		}
		seen++
		if _, listed := storageLegacyPermissions[key]; listed {
			continue
		}
		t.Errorf("%s is declared but is not in storageLegacyPermissions — a new storage route must be "+
			"added to the tally, or the tally stops being a review surface", key)
	}
	if seen == 0 {
		t.Fatal("no declared route matched the storage scope; this guard would pass vacuously")
	}
}

// TestStorageDeleteContentIsStillLegacy pins the one route this migration
// deliberately left behind, so that "12 of 13" is an assertion rather than
// a thing a reader has to notice.
//
// Its volume id is a greedy wildcard segment, and checkPathParams refuses
// one outright — a wildcard is the single piece of a path that reaches a
// handler unvalidated and un-normalized. Making it declarable means
// reshaping the route, which every caller's percent-encoding would have to
// agree on.
//
// Both halves are asserted: the route is NOT in the registry, and the
// registry would refuse it if someone declared it as-is. The second half is
// what keeps this from being a note nobody re-reads.
func TestStorageDeleteContentIsStillLegacy(t *testing.T) {
	const path = clusterScope + "/storage/:storage_id/content/*"

	s := newRouteStubServer(t)
	for _, e := range s.registry.Endpoints() {
		if e.Path == path {
			t.Fatalf("%s %s is declared, but a wildcard path cannot carry a parameter schema", e.Method, e.Path)
		}
	}

	// It is still mounted — as a LEGACY route, which is what the ratchet
	// records. A route that vanished entirely would be an outage, not a
	// migration.
	var mounted bool
	for _, r := range s.app.GetRoutes(true) {
		if r.Method == fiber.MethodDelete && normalizeRoutePath(r.Path) == path {
			mounted = true
		}
	}
	if !mounted {
		t.Error("the delete-content route is neither declared nor mounted; it has been lost, not deferred")
	}

	// And declaring it would be refused, rather than silently accepted with
	// an unvalidated segment.
	err := NewRegistry().register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        path,
		Description: "synthetic probe for the wildcard refusal",
		Group:       "Storage",
		Permissions: clusterCheck("delete", "storage"),
		Parameters:  clusterParams(nil),
		Handler:     noopParamsHandler,
	})
	if err == nil {
		t.Fatal("Register accepted a wildcard path; the reason this route stays legacy no longer holds")
	}
	if !strings.Contains(err.Error(), "wildcard") {
		t.Errorf("Register refused the wildcard path for the wrong reason: %v", err)
	}
}

// probeStorageEndpoint is a declared storage endpoint with its handler
// swapped for a capture.
func probeStorageEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestStorageUploadDeclaresNoBodyParameter is the assertion that keeps the
// streamed upload streaming.
//
// Nexara runs Fiber with StreamRequestBody and DisablePreParseMultipartForm
// so an ISO is never buffered. c.Body() defeats both — fasthttp drains the
// whole stream into a 32 MiB-capped buffer AND closes it — and
// Endpoint.extract calls bodyValues on every mutating verb. bodyValues
// gates on the Content-Type header BEFORE asking for the body, so a
// multipart request never reaches c.Body(); declaring a body parameter
// here would ALSO make the body "required", turning that gate into a 400
// for every upload.
//
// The declaration is therefore path-parameters-only, and this pins it.
func TestStorageUploadDeclaresNoBodyParameter(t *testing.T) {
	const path = clusterScope + "/storage/:storage_id/upload"
	e := declaredEndpoint(t, fiber.MethodPost, path)

	names := make([]string, 0, len(e.Parameters))
	for name := range e.Parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	if want := []string{"cluster_id", "storage_id"}; !slices.Equal(names, want) {
		t.Fatalf("the upload declares parameters %v, want only the path ones %v — a body parameter here "+
			"would make bodyValues demand a JSON body and 400 every multipart upload", names, want)
	}
	if e.declaresBodyParam() {
		t.Error("the upload declares a body parameter; extract would then require a JSON body")
	}

	// End to end: a multipart request reaches the handler with the stream
	// intact, which is the behaviour the two facts above exist to protect.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(), probeStorageEndpoint(t, fiber.MethodPost, path, cap))
	body := "--b\r\nContent-Disposition: form-data; name=\"content\"\r\n\r\niso\r\n--b--\r\n"
	req := httptest.NewRequest(http.MethodPost, storageRoute(path), strings.NewReader(body))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=b")
	status, env := send(t, app, req)
	if status != fiber.StatusNoContent {
		t.Fatalf("status = %d (%q), want 204 — a multipart upload must not be read as JSON", status, env.Message)
	}
	if !cap.called {
		t.Error("the handler did not run for a multipart upload")
	}
}

// TestStorageCreateRequiresOnlyWhatTheHandlerDid pins the create body's
// required SET against what the handler refused, derived from
// `git show HEAD:internal/api/handlers/storage.go`: an empty `storage` and
// a `type` outside validStorageTypes. `params` was never required — a
// plugin with no settings is created with none.
func TestStorageCreateRequiresOnlyWhatTheHandlerDid(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, storageScope)
	var got []string
	for name, prop := range e.Parameters {
		if name == "cluster_id" {
			continue
		}
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	if want := []string{"storage", "type"}; !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}

	// The type enum is the membership check the handler made, and it has to
	// stay the same list.
	if !slices.Equal(e.Parameters["type"].Enum, handlers.StorageTypes) {
		t.Errorf("the type enum is %v, want handlers.StorageTypes %v", e.Parameters["type"].Enum, handlers.StorageTypes)
	}

	for _, tt := range []struct {
		name string
		body string
		want int
	}{
		{"a dir pool with no settings", `{"storage":"store01","type":"dir"}`, fiber.StatusNoContent},
		{"a dir pool with settings", `{"storage":"store01","type":"dir","params":{"path":"/mnt/store01"}}`, fiber.StatusNoContent},
		{"no storage name", `{"type":"dir"}`, fiber.StatusBadRequest},
		{"an empty storage name", `{"storage":"","type":"dir"}`, fiber.StatusBadRequest},
		{"no type", `{"storage":"store01"}`, fiber.StatusBadRequest},
		{"a type Proxmox does not have", `{"storage":"store01","type":"ntfs"}`, fiber.StatusBadRequest},
		// The settings can only arrive inside params now; spreading them
		// across the top level used to be silently dropped.
		{"a setting at the top level", `{"storage":"store01","type":"dir","path":"/mnt/store01"}`, fiber.StatusBadRequest},
		{"a misspelled key", `{"storage":"store01","type":"dir","parms":{}}`, fiber.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeStorageEndpoint(t, fiber.MethodPost, storageScope, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, storageRoute(storageScope), tt.body))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.want == fiber.StatusBadRequest && cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}
}

// TestStorageUpdateRequiresNothing pins the other half of the asymmetry:
// the update handler bound its body and required NOTHING, so a PUT
// carrying only `delete`, or an empty object, is a working request.
func TestStorageUpdateRequiresNothing(t *testing.T) {
	const path = storageScope + "/:storage_id"
	e := declaredEndpoint(t, fiber.MethodPut, path)
	var required []string
	for name, prop := range e.Parameters {
		if !prop.Optional {
			required = append(required, name)
		}
	}
	sort.Strings(required)
	if want := []string{"cluster_id", "storage_id"}; !slices.Equal(required, want) {
		t.Errorf("the update's required set is %v, want only the path parameters %v", required, want)
	}

	for _, tt := range []struct {
		body string
		want int
	}{
		{`{}`, fiber.StatusNoContent},
		{`{"delete":"path,mkdir"}`, fiber.StatusNoContent},
		{`{"params":{"path":"/mnt/store01"}}`, fiber.StatusNoContent},
		{`{"params":{"path":"/mnt/store01"},"delete":"mkdir"}`, fiber.StatusNoContent},
		// An Object parameter asserts only "this is a JSON object", so a
		// nested value inside params passes the SCHEMA and is refused one
		// layer on by stringMap, which names the key. That split is
		// deliberate — apischema has no nested-properties field — and
		// stringMap's own rejection is covered in
		// internal/api/handlers/params_test.go rather than here, where the
		// probe has swapped the real handler out.
		{`{"params":{"path":"/mnt/store01","mkdir":"1"}}`, fiber.StatusNoContent},
		{`{"storage":"store01"}`, fiber.StatusBadRequest},
		{`{"params":"not-an-object"}`, fiber.StatusBadRequest},
	} {
		t.Run(tt.body, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeStorageEndpoint(t, fiber.MethodPut, path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPut, storageRoute(path), tt.body))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
		})
	}
}

// TestStorageDownloadURLRequiredSetAndChecksumIsNotPaired pins the
// download body against what the handler AND the Proxmox client enforced
// between them.
//
// url was never checked by the handler but IS refused empty by
// DownloadURLToStorage, so it is required. filename is NOT, because an OVA
// import may omit it and deriveURLFilename fills it in — which is the whole
// reason that function exists.
//
// And checksum deliberately carries NO Requires, tempting as the pairing
// is: apischema counts "" as supplied, while the client's rule is
// `if params.Checksum != ""`. A Requires would therefore 400 a caller
// sending checksum:"" as a placeholder — a request this API has always
// accepted and ignored. The pairing stays in DownloadURLToStorage, whose
// message already names both halves.
func TestStorageDownloadURLRequiredSetAndChecksumIsNotPaired(t *testing.T) {
	const path = storageScope + "/:storage_id/download-url"
	e := declaredEndpoint(t, fiber.MethodPost, path)

	var got []string
	for name, prop := range e.Parameters {
		if name == "cluster_id" || name == "storage_id" {
			continue
		}
		if !prop.Optional {
			got = append(got, name)
		}
	}
	sort.Strings(got)
	if want := []string{"content", "url"}; !slices.Equal(got, want) {
		t.Errorf("required parameters = %v, want %v", got, want)
	}
	if !slices.Equal(e.Parameters["content"].Enum, handlers.StorageDownloadContents) {
		t.Errorf("the content enum is %v, want handlers.StorageDownloadContents %v",
			e.Parameters["content"].Enum, handlers.StorageDownloadContents)
	}
	// checksum must NOT declare Requires: apischema counts the empty string
	// as "supplied", while the client's rule is `if params.Checksum != ""`,
	// so a Requires here would 400 a caller sending checksum:"" as a
	// placeholder — a request this API has always accepted and ignored.
	if len(e.Parameters["checksum"].Requires) != 0 {
		t.Errorf("checksum declares Requires %v; an empty checksum reads as supplied, so that would "+
			"reject a request that has always worked", e.Parameters["checksum"].Requires)
	}
	// verify_certificates must carry NO default: the client sends the key
	// only when the caller chose, and Proxmox's own default is to verify.
	if d := e.Parameters["verify_certificates"].Default; d != nil {
		t.Errorf("verify_certificates declares default %#v; it must have NONE, so that omitting it "+
			"leaves Proxmox verifying rather than sending verify-certificates=0", d)
	}

	for _, tt := range []struct {
		name string
		body string
		want int
	}{
		{"a bare OVA url", `{"url":"https://example.com/a.ova","content":"import"}`, fiber.StatusNoContent},
		{"an iso with a filename", `{"url":"https://example.com/a.iso","content":"iso","filename":"a.iso"}`, fiber.StatusNoContent},
		{"a checksum with its algorithm", `{"url":"https://example.com/a.iso","content":"iso","checksum":"abc","checksum_algorithm":"sha256"}`, fiber.StatusNoContent},
		// Both of these reach the handler: the pairing rule lives in the
		// Proxmox client, which refuses the first and ignores the second. See
		// the Requires note above for why the schema deliberately does not
		// make that call.
		{"a checksum without its algorithm", `{"url":"https://example.com/a.iso","content":"iso","checksum":"abc"}`, fiber.StatusNoContent},
		{"an empty checksum", `{"url":"https://example.com/a.iso","content":"iso","checksum":""}`, fiber.StatusNoContent},
		{"no url", `{"content":"iso"}`, fiber.StatusBadRequest},
		{"an empty url", `{"url":"","content":"iso"}`, fiber.StatusBadRequest},
		{"no content", `{"url":"https://example.com/a.iso"}`, fiber.StatusBadRequest},
		{"a content kind that is none of the three", `{"url":"https://example.com/a.iso","content":"backup"}`, fiber.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeStorageEndpoint(t, fiber.MethodPost, path, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, storageRoute(path), tt.body))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
			if tt.want == fiber.StatusBadRequest && cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}
}

// TestStorageIDIsDocumentedAsTheNexaraID is the docs assertion an external
// consumer asked for: :storage_id is a Nexara uuid, not the Proxmox
// storage name, and every route that takes one has to say so.
func TestStorageIDIsDocumentedAsTheNexaraID(t *testing.T) {
	seen := 0
	for key, e := range declaredStorageEndpoints(t) {
		prop, ok := e.Parameters["storage_id"]
		if !ok {
			continue
		}
		seen++
		if prop.Format != "uuid" {
			t.Errorf("%s declares storage_id with format %q, want uuid", key, prop.Format)
		}
		if !strings.Contains(prop.Description, "NOT the Proxmox storage name") {
			t.Errorf("%s: storage_id's description does not say it is not the Proxmox storage name, which "+
				"is the confusion an external consumer reported: %q", key, prop.Description)
		}
		if !strings.Contains(prop.Description, "/clusters/{cluster_id}/storage") {
			t.Errorf("%s: storage_id's description does not name the listing that yields one: %q",
				key, prop.Description)
		}
	}
	// config, PUT, DELETE, content, upload, oci-pull, download-url and
	// appliances — every declared route under /storage/:storage_id.
	if seen != 8 {
		t.Errorf("%d declared storage routes take a :storage_id, want 8; the description check above would "+
			"otherwise cover fewer routes than it claims", seen)
	}
}

// TestStorageScanISCSIPortalIsDeclared covers the one query parameter in
// this domain — the address a NODE is made to dial.
func TestStorageScanISCSIPortalIsDeclared(t *testing.T) {
	const path = clusterScope + "/scan/iscsi"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	if e.Parameters["portal"].Optional {
		t.Error("portal is optional, but the handler refused an empty one")
	}
	if e.Permissions.Describe() != "manage:storage" {
		t.Errorf("the scan declares %q, want manage:storage — discovery makes a node dial a "+
			"caller-supplied address", e.Permissions.Describe())
	}

	for _, tt := range []struct {
		query string
		want  int
	}{
		{"?portal=192.0.2.10", fiber.StatusNoContent},
		{"?portal=192.0.2.10:3260", fiber.StatusNoContent},
		{"", fiber.StatusBadRequest},
		{"?portal=", fiber.StatusBadRequest},
		{"?portl=192.0.2.10", fiber.StatusBadRequest},
	} {
		t.Run(tt.query, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeStorageEndpoint(t, fiber.MethodGet, path, cap))
			status, env := send(t, app, httptest.NewRequest(http.MethodGet, storageRoute(path)+tt.query, nil))
			if status != tt.want {
				t.Fatalf("status = %d (%q), want %d", status, env.Message, tt.want)
			}
		})
	}
}

// TestStorageContentRouteIsGatedByItsDeclaration proves one of this
// domain's gates end to end — that the declaration is what refuses a
// caller, not a call the handler still makes.
func TestStorageContentRouteIsGatedByItsDeclaration(t *testing.T) {
	const path = storageScope + "/:storage_id/content"
	e := declaredEndpoint(t, fiber.MethodGet, path)
	if e.Permissions.Describe() != "view:storage" {
		t.Fatalf("the content listing declares %q, want view:storage", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	target := storageRoute(path)

	t.Run("a caller holding an unrelated grant is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:vm": true}), gated)
		status, _ := send(t, app, authedRequest(http.MethodGet, target))
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding view:storage gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:storage": true}), gated)
		status, env := send(t, app, authedRequest(http.MethodGet, target))
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:storage": true}), gated)
		status, _ := send(t, app, httptest.NewRequest(http.MethodGet, target, nil))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// TestEveryStorageEndpointIsDocumented holds the declarations to the
// standard that makes this whole effort worth doing.
func TestEveryStorageEndpointIsDocumented(t *testing.T) {
	for key, e := range declaredStorageEndpoints(t) {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s: %v", key, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s has no description", key)
		}
		if e.Group != "Storage" {
			t.Errorf("%s is in group %q, want Storage", key, e.Group)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s: parameter %q has no description", key, name)
			}
		}
	}
}
