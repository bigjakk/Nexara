package api

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"

	"github.com/bigjakk/nexara/internal/api/handlers"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// These tests drive the REAL declarations — the ones setupRoutes mounts —
// rather than a fixture shaped like them, so a change to registry_vms.go
// that quietly loosened a parameter would show up here.

const (
	testVMID          = "8a1d2ab3-3f0e-4b4c-9f8a-000000000001"
	attachRoutePath   = "/api/v1/clusters/:cluster_id/vms/:vm_id/disks/attach"
	attachRouteTarget = "/api/v1/clusters/" + testClusterID + "/vms/" + testVMID + "/disks/attach"
)

// declaredEndpoint returns the production declaration for one route, and
// fails if the registry does not have it.
func declaredEndpoint(t *testing.T, method, path string) Endpoint {
	t.Helper()
	s := newRouteStubServer(t)
	for _, e := range s.registry.Endpoints() {
		if e.Method == method && e.Path == path {
			return e
		}
	}
	t.Fatalf("%s %s is not declared in the registry", method, path)
	return Endpoint{}
}

// probeEndpoint is a declared endpoint with its handler swapped for a
// capture, so a test can see exactly what the schema handed over without
// needing the handler's database and Proxmox client.
func probeEndpoint(t *testing.T, method, path string, cap *capture) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Handler = cap.handler()
	// The real declaration gates on manage:vm; these tests are about the
	// parameters, and the gate is exercised by TestRegistryChainOrder and
	// TestSetupRoutesMountsTheRegistry.
	e.Permissions = Permissions{SelfService: "parameter fixture; authorization is exercised separately"}
	return e
}

// TestAttachDiskSizeAcceptsEveryIncidentSpelling drives the four size
// values the API consumer tried, through the declared schema, in one
// place.
//
// Before this endpoint carried a format, each one failed differently and
// none of them said so: "500G" reached Proxmox verbatim and came back as
// a parse error, the JSON number 500 was refused by the struct binder,
// "500" worked, and "512000" provisioned 500 TiB without complaint. All
// four now mean the same thing and arrive as the bare GiB count Proxmox's
// "storage:N" form requires.
func TestAttachDiskSizeAcceptsEveryIncidentSpelling(t *testing.T) {
	tests := []struct {
		name string
		size string // as it appears in the JSON body, quoting included
		want string
	}{
		{name: "the suffixed form the old comment recommended", size: `"500G"`, want: "500"},
		{name: "a JSON number the old binder rejected", size: `500`, want: "500"},
		{name: "the one spelling that used to work", size: `"500"`, want: "500"},
		{name: "the megabyte-shaped value that meant gigabytes", size: `"512000"`, want: "512000"},
		{name: "terabytes", size: `"1T"`, want: "1024"},
		{name: "a sub-gigabyte size rounds up rather than to zero", size: `"512M"`, want: "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPost, attachRoutePath, cap))

			body := `{"bus":"scsi","storage":"store01","size":` + tt.size + `}`
			status, env := send(t, app, jsonRequest(http.MethodPost, attachRouteTarget, body))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			got := cap.params.String("size")
			if got != tt.want {
				t.Errorf("size = %q, want %q", got, tt.want)
			}
			// The seam between the schema and the handler: AttachDisk
			// parses this string with strconv.ParseInt to capacity-check
			// it. If the format ever stopped normalizing, that parse would
			// 500 every request rather than silently pass a suffixed size
			// through — but it is cheaper to notice here.
			gib, err := strconv.ParseInt(got, 10, 64)
			if err != nil {
				t.Fatalf("size %q is not the bare GiB count the handler parses: %v", got, err)
			}
			if gib <= 0 {
				t.Errorf("size parsed to %d GiB, want a positive count", gib)
			}
		})
	}
}

// TestAttachDiskSizeRejectsWhatProxmoxWould pins the other half: values
// that used to be forwarded and fail somewhere downstream now fail here,
// naming the field.
func TestAttachDiskSizeRejectsWhatProxmoxWould(t *testing.T) {
	for _, size := range []string{`""`, `"0"`, `"-5"`, `"big"`, `"20 GB please"`, `"2P"`} {
		t.Run(size, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPost, attachRoutePath, cap))

			body := `{"bus":"scsi","storage":"store01","size":` + size + `}`
			status, env := send(t, app, jsonRequest(http.MethodPost, attachRouteTarget, body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, "size:") {
				t.Errorf("message = %q, want it to name the size field", env.Message)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}
}

// TestAttachDiskIndexIsThreeState is the declaration this whole phase
// turns on. index must be Optional with NO default, so that omitting it
// is distinguishable from asking for slot 0 — the ambiguity that let an
// omitted index overwrite a boot disk.
func TestAttachDiskIndexIsThreeState(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, attachRoutePath)
	index, ok := e.Parameters["index"]
	if !ok {
		t.Fatal("the attach endpoint declares no index parameter")
	}
	if !index.Optional {
		t.Error("index is required; the caller must be able to omit it and let the handler pick a free slot")
	}
	if index.Default != nil {
		t.Errorf("index declares a default (%v); a default makes an omitted index indistinguishable from an "+
			"explicit one, which is the ambiguity this endpoint was rewritten to remove", index.Default)
	}

	tests := []struct {
		name         string
		body         string
		wantSupplied bool
		wantValue    int64
	}{
		{
			name:         "omitted",
			body:         `{"bus":"scsi","storage":"store01","size":"32"}`,
			wantSupplied: false,
			wantValue:    0,
		},
		{
			name: "explicitly zero",
			// Identical to the row above once read as a plain int, and the
			// whole reason OptInt exists.
			body:         `{"bus":"scsi","storage":"store01","size":"32","index":0}`,
			wantSupplied: true,
			wantValue:    0,
		},
		{
			name:         "explicitly non-zero",
			body:         `{"bus":"scsi","storage":"store01","size":"32","index":3}`,
			wantSupplied: true,
			wantValue:    3,
		},
		{
			name:         "explicitly null means omitted",
			body:         `{"bus":"scsi","storage":"store01","size":"32","index":null}`,
			wantSupplied: false,
			wantValue:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPost, attachRoutePath, cap))

			status, env := send(t, app, jsonRequest(http.MethodPost, attachRouteTarget, tt.body))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			value, supplied := cap.params.OptInt("index")
			if supplied != tt.wantSupplied {
				t.Errorf("supplied = %v, want %v", supplied, tt.wantSupplied)
			}
			if value != tt.wantValue {
				t.Errorf("index = %d, want %d", value, tt.wantValue)
			}
		})
	}
}

// TestAttachDiskRejectsMalformedRequests covers the rest of the declared
// contract at the edge of the request.
func TestAttachDiskRejectsMalformedRequests(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantField string
	}{
		{name: "missing bus", body: `{"storage":"store01","size":"32"}`, wantField: "bus:"},
		{name: "missing storage", body: `{"bus":"scsi","size":"32"}`, wantField: "storage:"},
		{name: "missing size", body: `{"bus":"scsi","storage":"store01"}`, wantField: "size:"},
		{name: "unknown bus", body: `{"bus":"nvme","storage":"store01","size":"32"}`, wantField: "bus:"},
		{
			name:      "index past the widest bus",
			body:      `{"bus":"scsi","storage":"store01","size":"32","index":31}`,
			wantField: "index:",
		},
		{
			name:      "negative index",
			body:      `{"bus":"scsi","storage":"store01","size":"32","index":-1}`,
			wantField: "index:",
		},
		{
			// A storage id that would restructure the volume spec. The
			// client refuses it too, but the schema should not have let it
			// travel that far.
			name:      "storage carrying a colon",
			body:      `{"bus":"scsi","storage":"store01:vm-9-disk-0","size":"32"}`,
			wantField: "storage:",
		},
		{
			// PVE's additionalProperties => 0. A misspelled parameter used
			// to be silently dropped, so a request could "succeed" while
			// doing something other than what was asked.
			name:      "a misspelled parameter",
			body:      `{"bus":"scsi","storage":"store01","size":"32","idx":1}`,
			wantField: "idx:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPost, attachRoutePath, cap))

			status, env := send(t, app, jsonRequest(http.MethodPost, attachRouteTarget, tt.body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, tt.wantField) {
				t.Errorf("message = %q, want it to start with %q", env.Message, tt.wantField)
			}
			if cap.called {
				t.Error("the handler ran for a request the schema rejected")
			}
		})
	}
}

// TestAttachDiskIsGatedByItsDeclaration proves the permission the
// declaration states is the permission the route enforces, end to end, on
// a REAL endpoint rather than a synthetic one.
//
// It matters more here than on most routes: the manage:vm check used to
// be the first two statements of AttachDisk's body, and this change
// deleted them. If the declaration and the middleware ever disagreed,
// every guard in this package would still pass — they read the
// declaration — and the route would ship authenticated but ungated.
func TestAttachDiskIsGatedByItsDeclaration(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, attachRoutePath)
	if e.Permissions.Describe() != "manage:vm" {
		t.Fatalf("the attach endpoint declares %q, want manage:vm", e.Permissions.Describe())
	}

	cap := &capture{}
	gated := e
	gated.Handler = cap.handler()
	body := `{"bus":"scsi","storage":"store01","size":"32"}`

	t.Run("a caller holding only view:vm is refused", func(t *testing.T) {
		app := newRegistryApp(t, stubAuth(map[string]bool{"view:vm": true}), gated)
		req := jsonRequest(http.MethodPost, attachRouteTarget, body)
		req.Header.Set("X-Test-User", "yes")
		status, _ := send(t, app, req)
		if status != fiber.StatusForbidden {
			t.Fatalf("status = %d, want 403", status)
		}
		if cap.called {
			t.Error("the handler ran for a caller without the declared permission")
		}
	})

	t.Run("a caller holding manage:vm gets through", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:vm": true}), gated)
		req := jsonRequest(http.MethodPost, attachRouteTarget, body)
		req.Header.Set("X-Test-User", "yes")
		status, env := send(t, app, req)
		if status != fiber.StatusNoContent {
			t.Fatalf("status = %d (%q), want 204", status, env.Message)
		}
		if !cap.called {
			t.Error("the handler did not run for a caller holding the declared permission")
		}
	})

	t.Run("an anonymous caller is refused before the gate", func(t *testing.T) {
		cap.called = false
		app := newRegistryApp(t, stubAuth(map[string]bool{"manage:vm": true}), gated)
		status, _ := send(t, app, jsonRequest(http.MethodPost, attachRouteTarget, body))
		if status != fiber.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", status)
		}
		if cap.called {
			t.Error("the handler ran for a request carrying no session")
		}
	})
}

// vmRoutesWithDeferredPermission are the only two VM routes whose
// permission is NOT statically known, listed here so that a third one
// cannot appear without this decision being re-made.
//
// Both serve either guest kind through a /vms/ path and pick the Proxmox
// client method off the loaded row's Type, so the RESOURCE half of the
// permission is unknowable until after the lookup — which is after any
// middleware would have run. Nexara keeps VMs and containers in one
// inventory table, which is why the path says "vms" for an object that
// may be a container.
var vmRoutesWithDeferredPermission = map[string]string{
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/convert-to-template": "converts a container with ConvertCTToTemplate when the row is lxc",
	"POST /api/v1/clusters/:cluster_id/vms/:vm_id/clone-to-template":   "clones a container with CloneCT when the row is lxc",
}

// routesOutsideTheClusterCheckShape names every declared route that is
// deliberately NOT a plain cluster-scoped Check, with the reason, merged
// from the per-domain tables each migration writes.
//
// TestVMRoutesDeclareACheck holds "a declared route is a cluster-scoped
// Check unless it is listed here" across the whole registry, and up to
// Phase 6b every route in it was one — the two VM exceptions above were
// still cluster-scoped, just Deferred. Phase 6c is the first batch with
// routes whose subject is not a cluster at all (a migration job, a PBS
// server, the instance-wide virtio-win catalog), so the exception surface
// has to be able to say that. It stays an enumerated list with a reason
// per entry for the same purpose it always had: a shape nobody listed is a
// shape nobody re-reads.
//
// The check runs in both directions — an entry whose route IS a plain
// cluster Check is reported as stale, so this cannot rot into a blanket
// waiver.
var routesOutsideTheClusterCheckShape = func() map[string]string {
	out := map[string]string{}
	for _, m := range []map[string]string{
		migrationRoutesOutsideTheClusterCheckShape,
		storageRoutesOutsideTheClusterCheckShape,
		virtioWinRoutesOutsideTheClusterCheckShape,
		pbsRoutesOutsideTheClusterCheckShape,
		firewallTemplateRoutesOutsideTheClusterCheckShape,
		backupRoutesOutsideTheClusterCheckShape,
		reportRoutesOutsideTheClusterCheckShape,
		veeamRoutesOutsideTheClusterCheckShape,
		alertRoutesOutsideTheClusterCheckShape,
		rbacRoutesOutsideTheClusterCheckShape,
		userRoutesOutsideTheClusterCheckShape,
		apiKeyRoutesOutsideTheClusterCheckShape,
		ldapRoutesOutsideTheClusterCheckShape,
		oidcRoutesOutsideTheClusterCheckShape,
		totpRoutesOutsideTheClusterCheckShape,
		authRoutesOutsideTheClusterCheckShape,
		searchRoutesOutsideTheClusterCheckShape,
		anonymousRoutesOutsideTheClusterCheckShape,
		guestSnapshotRoutesOutsideTheClusterCheckShape,
		favoritesRoutesOutsideTheClusterCheckShape,
		dlqRoutesOutsideTheClusterCheckShape,
		taskRoutesOutsideTheClusterCheckShape,
		clusterRoutesOutsideTheClusterCheckShape,
		auditRoutesOutsideTheClusterCheckShape,
		settingsRoutesOutsideTheClusterCheckShape,
	} {
		maps.Copy(out, m)
	}
	return out
}()

// vmRouteCount is how many endpoints registerVMEndpoints declares.
//
// It is stated per domain, and summed by registryRouteCount, so that a
// later migration adding its own routes cannot make this number drift
// without saying so: a single total would be a number anyone could raise
// to make the test pass again.
const vmRouteCount = 33

// registryDomainRouteCounts names every migrated domain and how many
// routes it declares, keyed by the function that declares them.
//
// The map — rather than an expression summing the constants — is what
// makes the failure message useful: a mismatch prints the per-domain
// breakdown, so "the registry holds 68, want 51" says WHICH domain is
// unaccounted for instead of leaving the reader to subtract.
var registryDomainRouteCounts = map[string]int{
	"registerVMEndpoints":             vmRouteCount,
	"registerContainerEndpoints":      containerRouteCount,
	"registerNodeEndpoints":           nodeRouteCount,
	"registerStorageEndpoints":        storageRouteCount,
	"registerCephEndpoints":           cephRouteCount,
	"registerHAEndpoints":             haRouteCount,
	"registerDRSEndpoints":            drsRouteCount,
	"registerCVEEndpoints":            cveRouteCount,
	"registerReplicationEndpoints":    replicationRouteCount,
	"registerMigrationEndpoints":      migrationRouteCount,
	"registerClusterOptionsEndpoints": clusterOptionsRouteCount,
	"registerGuestToolsEndpoints":     guestToolsRouteCount,
	"registerVirtioWinEndpoints":      virtioWinRouteCount,
	"registerPBSEndpoints":            pbsRouteCount,
	"registerBackupEndpoints":         backupRouteCount,
	"registerVMImportEndpoints":       vmImportRouteCount,
	"registerReportEndpoints":         reportRouteCount,
	"registerVeeamEndpoints":          veeamRouteCount,
	"registerAlertEndpoints":          alertRouteCount,
	"registerAccessEndpoints":         accessRouteCount,
	"registerACMEEndpoints":           acmeRouteCount,
	"registerRollingUpdateEndpoints":  rollingRouteCount,
	"registerRBACEndpoints":           rbacRouteCount,
	"registerUserEndpoints":           userRouteCount,
	"registerAPIKeyEndpoints":         apiKeyRouteCount,
	"registerLDAPEndpoints":           ldapRouteCount,
	"registerOIDCEndpoints":           oidcRouteCount,
	"registerTOTPEndpoints":           totpRouteCount,
	"registerAuthEndpoints":           authRouteCount,
	// NetworkHandler's 66 routes are declared across four files and four
	// functions rather than one, so each group carries its own count here —
	// see the file comment in registry_networks.go.
	"registerNetworkInterfaceEndpoints": networkInterfaceRouteCount,
	"registerFirewallEndpoints":         firewallRouteCount,
	"registerSDNEndpoints":              sdnRouteCount,
	"registerFirewallTemplateEndpoints": firewallTemplateRouteCount,

	"registerMetricsEndpoints":         metricsRouteCount,
	"registerPoolEndpoints":            poolRouteCount,
	"registerAptRepositoryEndpoints":   aptRouteCount,
	"registerMetricServerEndpoints":    metricServerRouteCount,
	"registerScheduleEndpoints":        scheduleRouteCount,
	"registerSearchEndpoints":          searchRouteCount,
	"registerGuestSnapshotEndpoints":   guestSnapshotRouteCount,
	"registerFavoritesEndpoints":       favoritesRouteCount,
	"registerVMFolderEndpoints":        vmFolderRouteCount,
	"registerNotificationDLQEndpoints": dlqRouteCount,
	"registerTaskEndpoints":            taskRouteCount,
	"registerClusterEndpoints":         clusterRouteCount,
	"registerAuditEndpoints":           auditRouteCount,
	"registerSettingsEndpoints":        settingsRouteCount,
	"registerChangelogEndpoints":       changelogRouteCount,
	"registerVersionEndpoint":          versionRouteCount,
}

// registryRouteCount is the total the registry must hold.
func registryRouteCount() int {
	total := 0
	for _, n := range registryDomainRouteCounts {
		total += n
	}
	return total
}

// TestRegistryDomainCountsAreIndividuallyRight checks each domain's
// constant against the declarations it actually names, so that a wrong
// per-domain number cannot hide inside a correct total. Two domains
// drifting by +1 and -1 would leave registryRouteCount right and both
// tables wrong.
func TestRegistryDomainCountsAreIndividuallyRight(t *testing.T) {
	declared := map[string]int{
		"registerVMEndpoints":             0,
		"registerContainerEndpoints":      0,
		"registerNodeEndpoints":           0,
		"registerStorageEndpoints":        0,
		"registerCephEndpoints":           0,
		"registerHAEndpoints":             0,
		"registerDRSEndpoints":            0,
		"registerCVEEndpoints":            0,
		"registerReplicationEndpoints":    0,
		"registerMigrationEndpoints":      0,
		"registerClusterOptionsEndpoints": 0,
		"registerGuestToolsEndpoints":     0,
		"registerVirtioWinEndpoints":      0,
		"registerPBSEndpoints":            0,
		"registerBackupEndpoints":         0,
		"registerVMImportEndpoints":       0,
		"registerReportEndpoints":         0,
		"registerVeeamEndpoints":          0,
		"registerAlertEndpoints":          0,
		"registerAccessEndpoints":         0,
		"registerACMEEndpoints":           0,
		"registerRollingUpdateEndpoints":  0,
		"registerRBACEndpoints":           0,
		"registerUserEndpoints":           0,
		"registerAPIKeyEndpoints":         0,
		"registerLDAPEndpoints":           0,
		"registerOIDCEndpoints":           0,
		"registerTOTPEndpoints":           0,
		"registerAuthEndpoints":           0,

		"registerNetworkInterfaceEndpoints": 0,
		"registerFirewallEndpoints":         0,
		"registerSDNEndpoints":              0,
		"registerFirewallTemplateEndpoints": 0,

		"registerMetricsEndpoints":         0,
		"registerPoolEndpoints":            0,
		"registerAptRepositoryEndpoints":   0,
		"registerMetricServerEndpoints":    0,
		"registerScheduleEndpoints":        0,
		"registerSearchEndpoints":          0,
		"registerGuestSnapshotEndpoints":   0,
		"registerFavoritesEndpoints":       0,
		"registerVMFolderEndpoints":        0,
		"registerNotificationDLQEndpoints": 0,
		"registerTaskEndpoints":            0,
		"registerClusterEndpoints":         0,
		"registerAuditEndpoints":           0,
		"registerSettingsEndpoints":        0,
		"registerChangelogEndpoints":       0,
		"registerVersionEndpoint":          0,
	}
	reg := NewRegistry()
	s := newRouteStubServer(t)

	registerVMEndpoints(reg, s.vmHandler)
	declared["registerVMEndpoints"] = reg.Len()

	before := reg.Len()
	registerContainerEndpoints(reg, s.containerHandler)
	declared["registerContainerEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerNodeEndpoints(reg, s.nodeHandler)
	declared["registerNodeEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerStorageEndpoints(reg, s.storageHandler)
	declared["registerStorageEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerCephEndpoints(reg, s.cephHandler)
	declared["registerCephEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerHAEndpoints(reg, s.haHandler)
	declared["registerHAEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerDRSEndpoints(reg, s.drsHandler)
	declared["registerDRSEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerCVEEndpoints(reg, s.cveHandler)
	declared["registerCVEEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerReplicationEndpoints(reg, s.replicationHandler)
	declared["registerReplicationEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerMigrationEndpoints(reg, s.migrationHandler)
	declared["registerMigrationEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerClusterOptionsEndpoints(reg, s.clusterOptionsHandler)
	declared["registerClusterOptionsEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerGuestToolsEndpoints(reg, s.guestToolsHandler)
	declared["registerGuestToolsEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerVirtioWinEndpoints(reg, s.virtioWinHandler)
	declared["registerVirtioWinEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerPBSEndpoints(reg, s.pbsHandler)
	declared["registerPBSEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerBackupEndpoints(reg, s.backupHandler)
	declared["registerBackupEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerVMImportEndpoints(reg, s.vmImportHandler)
	declared["registerVMImportEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerReportEndpoints(reg, s.reportHandler)
	declared["registerReportEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerVeeamEndpoints(reg, s.veeamHandler, nil, nil)
	declared["registerVeeamEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerAlertEndpoints(reg, s.alertHandler)
	declared["registerAlertEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerAccessEndpoints(reg, s.accessHandler)
	declared["registerAccessEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerACMEEndpoints(reg, s.acmeHandler)
	declared["registerACMEEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerRollingUpdateEndpoints(reg, s.rollingUpdateHandler)
	declared["registerRollingUpdateEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerRBACEndpoints(reg, s.rbacHandler)
	declared["registerRBACEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerUserEndpoints(reg, s.userHandler)
	declared["registerUserEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerAPIKeyEndpoints(reg, s.apiKeyHandler)
	declared["registerAPIKeyEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerLDAPEndpoints(reg, s.ldapHandler)
	declared["registerLDAPEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerOIDCEndpoints(reg, s.oidcHandler)
	declared["registerOIDCEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerTOTPEndpoints(reg, s.totpHandler)
	declared["registerTOTPEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerAuthEndpoints(reg, s.authHandler)
	declared["registerAuthEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerNetworkInterfaceEndpoints(reg, s.networkHandler)
	declared["registerNetworkInterfaceEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerFirewallEndpoints(reg, s.networkHandler)
	declared["registerFirewallEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerSDNEndpoints(reg, s.networkHandler)
	declared["registerSDNEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerFirewallTemplateEndpoints(reg, s.networkHandler)
	declared["registerFirewallTemplateEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerMetricsEndpoints(reg, s.metricsHandler)
	declared["registerMetricsEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerPoolEndpoints(reg, s.poolHandler)
	declared["registerPoolEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerAptRepositoryEndpoints(reg, s.aptRepositoryHandler)
	declared["registerAptRepositoryEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerMetricServerEndpoints(reg, s.metricServerHandler)
	declared["registerMetricServerEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerScheduleEndpoints(reg, s.scheduleHandler)
	declared["registerScheduleEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerSearchEndpoints(reg, s.searchHandler)
	declared["registerSearchEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerGuestSnapshotEndpoints(reg, s.guestSnapshotHandler)
	declared["registerGuestSnapshotEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerFavoritesEndpoints(reg, s.favoritesHandler)
	declared["registerFavoritesEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerVMFolderEndpoints(reg, s.vmFoldersHandler)
	declared["registerVMFolderEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerNotificationDLQEndpoints(reg, s.notificationDLQHandler)
	declared["registerNotificationDLQEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerTaskEndpoints(reg, s.taskHandler)
	declared["registerTaskEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerClusterEndpoints(reg, s.clusterHandler, nil, nil, nil)
	declared["registerClusterEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerAuditEndpoints(reg, s.auditHandler)
	declared["registerAuditEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerSettingsEndpoints(reg, s.settingsHandler)
	declared["registerSettingsEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerChangelogEndpoints(reg, s.changelogHandler)
	declared["registerChangelogEndpoints"] = reg.Len() - before

	before = reg.Len()
	registerVersionEndpoint(reg, s)
	declared["registerVersionEndpoint"] = reg.Len() - before

	if len(declared) != len(registryDomainRouteCounts) {
		t.Fatalf("this test drives %d domains but registryDomainRouteCounts names %d — "+
			"add the new domain here too, or its count is unchecked",
			len(declared), len(registryDomainRouteCounts))
	}
	for name, got := range declared {
		want, listed := registryDomainRouteCounts[name]
		if !listed {
			t.Errorf("%s is not in registryDomainRouteCounts", name)
			continue
		}
		if got != want {
			t.Errorf("%s declares %d routes, but its constant says %d", name, got, want)
		}
	}
}

// TestVMRoutesDeclareACheck records what the survey of these 33 handlers
// found: all but two resolved the cluster from the path and then made one
// static requireClusterPerm call, so all but two are declarable as a
// plain Check. None needed Advisory (a listing filtered rather than
// gated).
//
// It is a record rather than a rule: a VM route that genuinely computes
// its permission SHOULD declare Deferred, and this test is where that
// gets noticed and re-justified instead of slipping through. That is
// exactly how the two exceptions got here — they were declared as plain
// manage:vm Checks first, and a security review found that a caller with
// manage:vm and no container rights could irreversibly convert a
// container through them.
//
// It walks the WHOLE registry rather than only the VM declarations, and
// keeps doing so as later phases add domains: "every declared route is a
// cluster-scoped Check unless it is on a listed exception" is an invariant
// worth holding across the registry, and a per-domain filter here would
// let a new domain's route escape it by simply not being a VM route. The
// count is the sum of the per-domain constants for the same reason.
func TestVMRoutesDeclareACheck(t *testing.T) {
	s := newRouteStubServer(t)
	endpoints := s.registry.Endpoints()
	if want := registryRouteCount(); len(endpoints) != want {
		t.Errorf("the registry holds %d endpoints, want %d — the sum of the per-domain counts in "+
			"registryDomainRouteCounts", len(endpoints), want)
	}

	seenDeferred := map[string]bool{}
	seenOutside := map[string]bool{}
	for _, e := range endpoints {
		key := e.Method + " " + e.Path
		isClusterCheck := e.Permissions.Check != nil && e.Permissions.Check.Scope == ScopeCluster

		if why, expected := vmRoutesWithDeferredPermission[key]; expected {
			seenDeferred[key] = true
			if e.Permissions.Deferred == "" {
				t.Errorf("%s declares %q, but it %s — it needs Deferred, because the resource is not "+
					"knowable before the guest row is read", key, e.Permissions.Describe(), why)
				continue
			}
			// The reason has to name the permission the handler adds, or
			// it documents nothing an operator could act on.
			if !strings.Contains(e.Permissions.Deferred, "manage:container") {
				t.Errorf("%s: the Deferred reason does not name manage:container, which is the permission "+
					"the handler checks for a container: %q", key, e.Permissions.Deferred)
			}
			continue
		}

		if why, listed := routesOutsideTheClusterCheckShape[key]; listed {
			seenOutside[key] = true
			if strings.TrimSpace(why) == "" {
				t.Errorf("%s is listed in routesOutsideTheClusterCheckShape with a blank reason; "+
					"an exemption nobody justified is one nobody re-reads", key)
			}
			// The other direction: an entry whose route IS the ordinary
			// shape is stale, and leaving it would quietly exempt a route
			// that no longer needs exempting.
			if isClusterCheck {
				t.Errorf("%s declares a plain cluster-scoped Check but is still listed in "+
					"routesOutsideTheClusterCheckShape (%q) — drop the stale entry", key, why)
			}
			continue
		}

		if e.Permissions.Check == nil {
			t.Errorf("%s declares %q rather than a Check — if that is deliberate, add it to "+
				"vmRoutesWithDeferredPermission (a guest-kind Deferred) or to "+
				"routesOutsideTheClusterCheckShape, with the reason", key, e.Permissions.Describe())
			continue
		}
		if e.Permissions.Check.Scope != ScopeCluster {
			t.Errorf("%s is %s-scoped; a route that does not act on one cluster belongs in "+
				"routesOutsideTheClusterCheckShape with the reason", key, e.Permissions.Check.Scope)
		}
	}

	for key := range vmRoutesWithDeferredPermission {
		if !seenDeferred[key] {
			t.Errorf("vmRoutesWithDeferredPermission lists %s but no such route is declared — "+
				"drop the stale entry, or a list of exceptions stops being a review surface", key)
		}
	}
	for key := range routesOutsideTheClusterCheckShape {
		if !seenOutside[key] {
			t.Errorf("routesOutsideTheClusterCheckShape lists %s but no such route is declared — "+
				"drop the stale entry, or a list of exceptions stops being a review surface", key)
		}
	}
}

// TestVMRoutesDeclareEveryPathParameter is checkPathParams' rule read
// from the other side: Register already refuses a :param with no entry in
// Parameters, so this asserts the converse — that no declaration carries
// a path parameter the path does not name, which Register catches too but
// only for the exact spelling.
func TestVMRoutesDeclareEveryPathParameter(t *testing.T) {
	s := newRouteStubServer(t)
	for _, e := range s.registry.Endpoints() {
		for _, name := range pathParamNames(e.Path) {
			prop, ok := e.Parameters[name]
			if !ok {
				t.Errorf("%s %s has :%s with no entry in Parameters", e.Method, e.Path, name)
				continue
			}
			if prop.Optional {
				t.Errorf("%s %s declares the path parameter %q optional; a URL segment is always present",
					e.Method, e.Path, name)
			}
		}
	}
}

// TestCloneParametersTolerateTheEmptySentinel guards the compatibility
// decision behind emptyOrStorageID and emptyOrNodeName: the clone dialog
// sends storage:"" for a linked clone, and apischema treats "" as a
// supplied value that every registered format rejects. Borrowing the
// storage-id format here would 400 a request that has always worked.
func TestCloneParametersTolerateTheEmptySentinel(t *testing.T) {
	for _, path := range []string{
		"/api/v1/clusters/:cluster_id/vms/:vm_id/clone",
		"/api/v1/clusters/:cluster_id/vms/:vm_id/clone-to-template",
	} {
		t.Run(path, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPost, path, cap))

			target := strings.NewReplacer(
				":cluster_id", testClusterID,
				":vm_id", testVMID,
			).Replace(path)
			body := `{"new_id":9001,"name":"","target":"","full":false,"storage":""}`

			status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — the clone dialog sends every key, empty ones included",
					status, env.Message)
			}
			for _, key := range []string{"name", "target", "storage"} {
				if got := cap.params.String(key); got != "" {
					t.Errorf("%s = %q, want the empty sentinel to survive", key, got)
				}
			}
		})
	}

	// A non-empty value is still held to its shape.
	cap := &capture{}
	app := newRegistryApp(t, noAuth(),
		probeEndpoint(t, fiber.MethodPost, "/api/v1/clusters/:cluster_id/vms/:vm_id/clone", cap))
	body := `{"new_id":9001,"storage":"store01:vm-9-disk-0"}`
	target := "/api/v1/clusters/" + testClusterID + "/vms/" + testVMID + "/clone"
	if status, env := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusBadRequest {
		t.Errorf("status = %d (%q), want 400 for a storage id carrying a colon", status, env.Message)
	}
}

// TestPoolParameterKeepsTheRemovalSentinel is the compatibility half of
// emptyOrPoolID, and the reason the pattern has an empty alternative at
// all: PUT .../pool with pool:"" is how the SPA takes a guest OUT of its
// resource pool. Anchoring the parameter without that alternative would
// make removal impossible — a 400 on the one value the endpoint's own
// description promises.
func TestPoolParameterKeepsTheRemovalSentinel(t *testing.T) {
	const path = "/api/v1/clusters/:cluster_id/vms/:vm_id/pool"
	target := "/api/v1/clusters/" + testClusterID + "/vms/" + testVMID + "/pool"

	// The last four are the shapes PVE's own verify_poolname accepts and
	// an earlier, tidier-looking pattern of ours did not: a leading dot, a
	// leading dash, and nesting two and three levels deep.
	for _, pool := range []string{"", "prod01", "Pool_2", "a.b-c", ".hidden", "-dash", "infra/prod", "infra/prod/db"} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPut, path, cap))
		body := `{"pool":"` + pool + `"}`
		status, env := send(t, app, jsonRequest(http.MethodPut, target, body))
		if status != fiber.StatusNoContent {
			t.Errorf("pool %q: status = %d (%q), want 204", pool, status, env.Message)
			continue
		}
		if got := cap.params.String("pool"); got != pool {
			t.Errorf("pool %q reached the handler as %q; it must be passed through untouched", pool, got)
		}
	}
}

// TestPoolParameterRejectsWhatProxmoxWouldBounce is the other half. Each
// value here previously reached Proxmox and came back as a 502 quoting a
// URL the caller never wrote, because SetVMPool interpolates the value
// into "/pools/{pool}" (internal/proxmox/client_admin.go).
//
// Traversal is NOT what this rejects, and the cases are chosen so nobody
// reads it that way: url.PathEscape encodes "/" as %2F, so
// "../../access/users" was always one inert literal segment rather than a
// path escape — it is rejected here for being four levels deep with "."
// as a segment, not for looking dangerous. What was wrong was the ERROR: a
// 502 naming Proxmox for a request this API could have refused itself.
//
// Note "." and ".." are NOT in this table. pve-poolid's segment charset
// allows a bare dot, so they are pool ids Proxmox accepts; rejecting them
// would be the invented-strictness mistake this pattern avoids.
func TestPoolParameterRejectsWhatProxmoxWouldBounce(t *testing.T) {
	const path = "/api/v1/clusters/:cluster_id/vms/:vm_id/pool"
	target := "/api/v1/clusters/" + testClusterID + "/vms/" + testVMID + "/pool"

	for _, pool := range []string{
		"has spaces!",          // the value that produced the observed 502
		"../../access/users",   // four levels, and "." is not a segment
		"infra/prod/db/deeper", // one level past pve-poolid's max of three
		"has//empty",           // an empty segment
		"trailing/",            // ditto, at the end
		"semi;colon",
		"star*",
	} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPut, path, cap))
		body, err := json.Marshal(map[string]string{"pool": pool})
		if err != nil {
			t.Fatalf("encoding body for %q: %v", pool, err)
		}
		status, env := send(t, app, jsonRequest(http.MethodPut, target, string(body)))
		if status != fiber.StatusBadRequest {
			t.Errorf("pool %q: status = %d (%q), want 400 — this never was a valid pool id",
				pool, status, env.Message)
		}
	}
}

// TestEveryPoolParameterCarriesThePattern stops the fix from being applied
// to the one route it was found on. Only SetVMPool's `pool` becomes a
// Proxmox path segment, but a pool id that is not a pool id is worth
// refusing on all of them.
//
// SIX routes, from five declaration sites: backupJobParams serves both
// POST /backup-jobs and PUT /backup-jobs/:job_id, which is exactly why the
// count is asserted over the REGISTRY rather than over the source. Editing
// the five sites and counting five would have left a route unchecked and
// still passed.
func TestEveryPoolParameterCarriesThePattern(t *testing.T) {
	var found int
	for _, e := range newRouteStubServer(t).registry.Endpoints() {
		prop, ok := e.Parameters["pool"]
		if !ok {
			continue
		}
		found++
		if prop.Pattern != emptyOrPoolID {
			t.Errorf("%s %s declares pool with pattern %q, want emptyOrPoolID",
				e.Method, e.Path, prop.Pattern)
		}
	}
	if found != 6 {
		t.Errorf("found %d routes taking a `pool` body parameter, want 6 — "+
			"a new one must carry the pattern too", found)
	}
}

// TestResizeKeepsProxmoxDeltaSyntax pins that the resize endpoint did NOT
// borrow the attach endpoint's disk-size format. They look alike and are
// not: "+8G" means "grow by 8 GiB", and normalizing it to a bare count
// would turn it into "grow to 8 GiB" — a shrink Proxmox refuses, or worse
// on a disk already smaller than that.
func TestResizeKeepsProxmoxDeltaSyntax(t *testing.T) {
	const path = "/api/v1/clusters/:cluster_id/vms/:vm_id/disks/resize"
	e := declaredEndpoint(t, fiber.MethodPost, path)
	if e.Parameters["size"].Format != "" {
		t.Errorf("resize declares format %q; the delta form cannot survive normalization", e.Parameters["size"].Format)
	}

	target := "/api/v1/clusters/" + testClusterID + "/vms/" + testVMID + "/disks/resize"
	accepted := []string{"+8G", "64G", "1T", "512M", "100"}
	for _, size := range accepted {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPost, path, cap))
		body := `{"disk":"scsi0","size":"` + size + `"}`
		status, env := send(t, app, jsonRequest(http.MethodPost, target, body))
		if status != fiber.StatusNoContent {
			t.Errorf("size %q: status = %d (%q), want 204", size, status, env.Message)
			continue
		}
		if got := cap.params.String("size"); got != size {
			t.Errorf("size %q reached the handler as %q; it must be passed through untouched", size, got)
		}
	}

	for _, size := range []string{"-8G", "8 G", "8GB", ""} {
		cap := &capture{}
		app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPost, path, cap))
		body := `{"disk":"scsi0","size":"` + size + `"}`
		if status, _ := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusBadRequest {
			t.Errorf("size %q: status = %d, want 400", size, status)
		}
	}
}

// TestBusEnumMatchesTheClient keeps the declared bus vocabulary and the
// client's slot ceilings from drifting apart: a bus the schema accepts
// but proxmox.MaxDiskIndex does not know would pass validation and then
// produce a config key Proxmox rejects.
//
// The length assertion is a real comparison rather than a slice against
// itself: the declaration clones proxmox.DiskBuses, so this catches
// someone replacing it with a hand-written literal — which is the drift
// worth catching, since that is how the two lists would part ways.
func TestBusEnumMatchesTheClient(t *testing.T) {
	e := declaredEndpoint(t, fiber.MethodPost, attachRoutePath)
	bus := e.Parameters["bus"]
	if len(bus.Enum) == 0 {
		t.Fatal("bus declares no enum")
	}
	for _, name := range bus.Enum {
		if _, ok := proxmox.MaxDiskIndex(name); !ok {
			t.Errorf("the schema accepts bus %q but the client has no slot ceiling for it", name)
		}
	}
	if len(bus.Enum) != len(proxmox.DiskBuses) {
		t.Errorf("bus enum has %d entries, proxmox.DiskBuses has %d", len(bus.Enum), len(proxmox.DiskBuses))
	}
}

// TestEveryVMEndpointCompiles is belt and braces around Register, which
// compiles each schema as it is declared: if buildRegistry were ever
// changed to report rather than panic, this would still fail.
func TestEveryVMEndpointCompiles(t *testing.T) {
	s := newRouteStubServer(t)
	for _, e := range s.registry.Endpoints() {
		if err := e.Parameters.Compile(); err != nil {
			t.Errorf("%s %s: %v", e.Method, e.Path, err)
		}
		if strings.TrimSpace(e.Description) == "" {
			t.Errorf("%s %s has no description; the declaration IS the documentation", e.Method, e.Path)
		}
		for name, prop := range e.Parameters {
			if strings.TrimSpace(prop.Description) == "" {
				t.Errorf("%s %s: parameter %q has no description", e.Method, e.Path, name)
			}
		}
	}
}

// snapshotCreateRoutes are the two routes that take a snapshot NAME in the
// body, with the reserved name Proxmox applies to that guest kind and only
// that one.
//
// Both entries are needed because the rules are not the same on both
// routes: the length cap is, the reserved set is not.
var snapshotCreateRoutes = []struct {
	kind string
	path string
	// reserved is the kind-specific reserved name; "current" is reserved
	// for both and is asserted separately.
	reserved string
	// otherKind is the OTHER route's reserved name, which this route's
	// description must not claim. It is what catches a description copied
	// across from the sibling declaration.
	otherKind string
	// casingNote is prose the description must carry when a reservation on
	// this route is NOT exact-match, so a caller can predict the 400. Empty
	// when every reservation here is exact.
	casingNote string
	probe      func(t *testing.T, method, path string, cap *capture) Endpoint
	render     func(path string) string
}{
	{
		kind: "vm", path: clusterScope + "/vms/:vm_id/snapshots",
		reserved: "pending", otherKind: "vzdump",
		// PVE compares "pending" with lc(), so "Pending" is refused too.
		casingNote: "casing",
		probe:      probeEndpoint,
		render: func(p string) string {
			return strings.NewReplacer(":cluster_id", testClusterID, ":vm_id", testVMID).Replace(p)
		},
	},
	{
		// Both container reservations are exact-match upstream, so there is
		// no casing caveat to state.
		kind: "container", path: containerScope + "/:ct_id/snapshots",
		reserved: "vzdump", otherKind: "pending",
		probe: probeCTEndpoint, render: ctRoute,
	},
}

// TestSnapshotNameCapAgreesAcrossLayers is the pin under a number that
// went years without anyone checking it.
//
// 40 is not Nexara's: pve-common registers pve-snapshot-name with
// `maxLength => 40`, and every snapname parameter upstream uses that
// standard option (see handlers.SnapshotMaxNameLen for the citation). The
// risk is no longer that the number is wrong — it is that the three places
// stating it drift apart, which is exactly what happened before the
// declaration carried a MaxLength at all: the payload published "2 to 128
// characters" for a route that answered 400 at 41.
//
// So this asserts all three say the same thing: the declared MaxLength,
// the published prose, and the boundary the route actually enforces. The
// MaxLength now references the constant rather than restating it, which
// removes one drift axis; the prose is still hand-written, and is the one
// this test is really holding.
func TestSnapshotNameCapAgreesAcrossLayers(t *testing.T) {
	wantPhrase := fmt.Sprintf("2-%d characters", handlers.SnapshotMaxNameLen)

	for _, rt := range snapshotCreateRoutes {
		t.Run(rt.kind, func(t *testing.T) {
			e := declaredEndpoint(t, fiber.MethodPost, rt.path)
			prop := e.Parameters["snap_name"]

			if prop.MaxLength == nil {
				t.Fatal("snap_name declares no MaxLength; the docs then publish pve-configid's " +
					"128 for a route that refuses 41")
			}
			if *prop.MaxLength != handlers.SnapshotMaxNameLen {
				t.Errorf("snap_name MaxLength = %d, want handlers.SnapshotMaxNameLen (%d) — "+
					"the schema and the handler must refuse the same names",
					*prop.MaxLength, handlers.SnapshotMaxNameLen)
			}
			if !strings.Contains(prop.Description, wantPhrase) {
				t.Errorf("snap_name description = %q, want it to state %q — a caller reads the "+
					"prose, and it is the only one of the three layers nothing derives",
					prop.Description, wantPhrase)
			}
		})
	}
}

// TestSnapshotNameDescriptionNamesItsOwnReservedSet holds the prose to the
// reserved names that actually apply to that guest kind.
//
// Proxmox reserves "current" for both, "pending" for VMs only and "vzdump"
// for containers only, and the two declarations sit in different files, so
// the cheap mistake is to copy one description onto the other route and
// publish a rule that does not hold there.
func TestSnapshotNameDescriptionNamesItsOwnReservedSet(t *testing.T) {
	for _, rt := range snapshotCreateRoutes {
		t.Run(rt.kind, func(t *testing.T) {
			desc := declaredEndpoint(t, fiber.MethodPost, rt.path).Parameters["snap_name"].Description

			for _, want := range []string{"current", rt.reserved} {
				if !strings.Contains(desc, want) {
					t.Errorf("snap_name description = %q, want it to name the reserved %q",
						desc, want)
				}
			}
			if strings.Contains(desc, rt.otherKind) {
				t.Errorf("snap_name description = %q names %q, which Proxmox reserves for the "+
					"OTHER guest kind and accepts here", desc, rt.otherKind)
			}
			if rt.casingNote != "" && !strings.Contains(desc, rt.casingNote) {
				t.Errorf("snap_name description = %q, want it to say %q — a reservation on this "+
					"route is case-insensitive upstream, and a caller who reads only the bare name "+
					"cannot predict the 400 that %q earns",
					desc, rt.casingNote, strings.ToUpper(rt.reserved[:1])+rt.reserved[1:])
			}
		})
	}
}

// TestSnapshotNameCapIsEnforcedBySchema answers "which layer refuses it".
//
// The probe swaps the real handler out, so reaching it means the schema
// let the value through. A name at the cap must reach the handler and one
// character over must not — which puts the refusal in the declaration,
// where the docs can show it, rather than only in validateSnapshotName
// where a caller reading the payload could not predict it.
func TestSnapshotNameCapIsEnforcedBySchema(t *testing.T) {
	for _, rt := range snapshotCreateRoutes {
		t.Run(rt.kind, func(t *testing.T) {
			atCap := strings.Repeat("a", handlers.SnapshotMaxNameLen)
			overCap := strings.Repeat("a", handlers.SnapshotMaxNameLen+1)

			cap := &capture{}
			app := newRegistryApp(t, noAuth(), rt.probe(t, fiber.MethodPost, rt.path, cap))
			body := `{"snap_name":"` + atCap + `"}`
			if status, env := send(t, app, jsonRequest(http.MethodPost, rt.render(rt.path), body)); status != fiber.StatusNoContent {
				t.Fatalf("a %d-character name: status = %d (%q), want 204 — the cap is %d, so this one is legal",
					len(atCap), status, env.Message, handlers.SnapshotMaxNameLen)
			}
			if !cap.called {
				t.Error("a name at the cap did not reach the handler")
			}

			cap = &capture{}
			app = newRegistryApp(t, noAuth(), rt.probe(t, fiber.MethodPost, rt.path, cap))
			body = `{"snap_name":"` + overCap + `"}`
			status, env := send(t, app, jsonRequest(http.MethodPost, rt.render(rt.path), body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("a %d-character name: status = %d (%q), want 400", len(overCap), status, env.Message)
			}
			if cap.called {
				t.Error("a name over the cap reached the handler; the schema has to be the layer that " +
					"refuses it, or the docs payload cannot show the rule")
			}
			if !strings.Contains(env.Message, "snap_name") {
				t.Errorf("message = %q, want it to name snap_name", env.Message)
			}
		})
	}
}

// realHandlerEndpoint is the production declaration with ONLY its
// permission gate relaxed — the handler itself is the real one, bound to
// the stub server's zero-valued handler structs. That is enough for any
// request the handler refuses before it reaches its database.
func realHandlerEndpoint(t *testing.T, method, path string) Endpoint {
	t.Helper()
	e := declaredEndpoint(t, method, path)
	e.Permissions = Permissions{SelfService: "reserved-name fixture; authorization is exercised separately"}
	return e
}

// newRecoveringRegistryApp is newRegistryApp with a recover in front.
//
// It exists for the test below, whose failure mode is a real handler
// running FURTHER than it should: against the stub server's zero-valued
// handler structs that is a nil dereference, and an unrecovered panic
// takes the whole package's test binary down instead of failing one
// assertion. With the recover in place the same mistake reports as a 500,
// which is a legible failure and does not hide anything else in the run.
func newRecoveringRegistryApp(t *testing.T, es ...Endpoint) *fiber.App {
	t.Helper()
	reg := NewRegistry()
	for _, e := range es {
		reg.Register(e)
	}
	app := fiber.New(fiber.Config{ErrorHandler: errorHandler})
	app.Use(recover.New())
	mountRegistry(app, reg, noAuth())
	return app
}

// TestSnapshotCreateHandlerPassesItsOwnGuestKind closes the gap the
// reserved-name split opens: validateSnapshotName now takes the guest kind
// from its CALLER, and a caller that passes the wrong one is silent.
//
// Swap the two constants and every test in the handlers package still
// passes — they call validateSnapshotName directly and never see which
// kind the handler chose. So this drives the REAL handler, with a name
// that is reserved for exactly one of the two kinds.
//
// The schema cannot be what refuses these: "pending" and "vzdump" are both
// valid pve-configid values, which the first subtest asserts rather than
// assumes. A 400 from these routes therefore came from the handler, and
// naming the wrong kind would let the name through to a nil database
// instead.
func TestSnapshotCreateHandlerPassesItsOwnGuestKind(t *testing.T) {
	for _, rt := range snapshotCreateRoutes {
		t.Run(rt.kind, func(t *testing.T) {
			target := rt.render(rt.path)
			body := `{"snap_name":"` + rt.reserved + `"}`

			// The schema must NOT be what refuses it: rt.reserved is a valid
			// pve-configid, so a probe that replaces the handler has to see
			// the request arrive. Asserting this rather than assuming it is
			// what makes the next subtest's 400 attributable to the handler.
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), rt.probe(t, fiber.MethodPost, rt.path, cap))
			if status, env := send(t, app, jsonRequest(http.MethodPost, target, body)); status != fiber.StatusNoContent {
				t.Fatalf("the schema refused %q: status = %d (%q), want 204 — it is a valid "+
					"pve-configid, so the handler has to be the layer that refuses it",
					rt.reserved, status, env.Message)
			}

			// The real handler, with only its permission gate relaxed. It
			// refuses before it reaches its (nil) database, so a 400 here
			// can only have come from validateSnapshotName — and only if the
			// handler named the guest kind whose reserved set contains this
			// name. Naming the other kind lets it through to the database
			// and reports 500.
			realApp := newRecoveringRegistryApp(t, realHandlerEndpoint(t, fiber.MethodPost, rt.path))
			status, env := send(t, realApp, jsonRequest(http.MethodPost, target, body))
			if status != fiber.StatusBadRequest || !strings.Contains(env.Message, "reserved") {
				t.Errorf("status = %d (%q), want 400 naming the reserved name — Proxmox refuses "+
					"%q for a %s, so this handler must be naming that guest kind",
					status, env.Message, rt.reserved, rt.kind)
			}

			// The OTHER kind's reserved name is legal here and must get PAST
			// validation. This is the half that catches a union of the two
			// reserved sets, which the 400 above would be perfectly happy
			// with.
			//
			// 500 is the assertion rather than "not 400" because it says
			// where the request got to: the stub server's handler structs
			// have no database, so reaching one is a recovered nil
			// dereference. Anything else — a 400, a 204 — means the request
			// stopped somewhere before that, which is the failure.
			otherBody := `{"snap_name":"` + rt.otherKind + `"}`
			status, env = send(t, realApp, jsonRequest(http.MethodPost, target, otherBody))
			if status != fiber.StatusInternalServerError {
				t.Errorf("status = %d (%q) for %q, want 500 — Proxmox reserves that name for the "+
					"OTHER guest kind and accepts it for a %s, so it must reach the database",
					status, env.Message, rt.otherKind, rt.kind)
			}
		})
	}
}

// TestSnapshotNameParamRefusesTraversal proves the claim snapshotNameParam's
// doc comment makes: its Pattern, not percent-escaping, is what keeps a
// path segment from walking out of the snapshot it addresses.
//
// Asserting the Pattern is DECLARED is a different and weaker statement
// than asserting it REFUSES this — a rule can be present and still admit
// the thing it was put there for. So this sends the payload.
//
// Escaping is explicitly not the guard. Proxmox decodes a percent-escape
// before it resolves the path (the capture-server run recorded at
// internal/proxmox/client.go and in validateVolumeID's doc comment proved
// "%2e%2e%2f" arrives byte-for-byte and becomes "../" on the far side), so
// a value that reaches the client is a value that reaches the path. Both
// spellings below must be refused here, at the declaration, whether Fiber
// hands the decoded form to validation or the raw one: the decoded form
// carries separators and dots, the raw form carries percents, and the
// pve-configid-existing pattern admits neither.
func TestSnapshotNameParamRefusesTraversal(t *testing.T) {
	routes := []struct {
		method string
		path   string
		render func(string) string
	}{
		{fiber.MethodDelete, clusterScope + "/vms/:vm_id/snapshots/:snap_name", nil},
		{fiber.MethodPost, clusterScope + "/vms/:vm_id/snapshots/:snap_name/rollback", nil},
		{fiber.MethodDelete, containerScope + "/:ct_id/snapshots/:snap_name", ctRoute},
		{fiber.MethodPost, containerScope + "/:ct_id/snapshots/:snap_name/rollback", ctRoute},
	}
	// Each is a snapshot name a caller could put in the path segment, with
	// the layer that must refuse it. Naming the layer is the point: a test
	// that accepted "400 or 404" would pass just as happily if the Pattern
	// disappeared and the router happened to miss, which is the failure
	// this whole file exists to catch.
	payloads := []struct {
		value string
		want  int
		why   string
	}{
		// NOT because Fiber decodes it — Fiber's UnescapePath is false and
		// validation runs on the RAW segment. The declaration refuses this
		// on the leading "%": pve-configid-existing is
		// ^[A-Za-z][A-Za-z0-9_-]*$, which admits no percent at all. That is
		// why the snapshot routes need no handler-side decode to be safe,
		// and it is a different mechanism from the ceph pool route, whose
		// rule DOES admit "%" and which relies on the client guard instead.
		{"%2e%2e%2f", fiber.StatusBadRequest, "the encoded traversal decodes and the Pattern refuses it"},
		{"..%2fetc", fiber.StatusBadRequest, "half-encoded traversal"},
		{"..", fiber.StatusBadRequest, "the bare parent-directory segment"},
		{`a\b`, fiber.StatusBadRequest, "a Windows-style separator"},
		{"a%00", fiber.StatusBadRequest, "a NUL escape"},
		// The one case the ROUTER handles: a literal slash makes the URL
		// stop matching this route's shape before any parameter is read.
		{"a/b", fiber.StatusNotFound, "a literal separator does not match the route"},
	}

	for _, rt := range routes {
		for _, payload := range payloads {
			name := rt.method + " " + rt.path + " " + payload.value
			t.Run(name, func(t *testing.T) {
				cap := &capture{}
				probe := probeEndpoint
				if rt.render != nil {
					probe = probeCTEndpoint
				}
				e := probe(t, rt.method, rt.path, cap)
				app := newRegistryApp(t, noAuth(), e)

				render := rt.render
				if render == nil {
					render = func(p string) string {
						return strings.NewReplacer(":cluster_id", testClusterID, ":vm_id", testVMID).Replace(p)
					}
				}
				// Substitute :snap_name BEFORE render. ctRoute renders it as
				// "snap01" and the VM renderer leaves it alone, so patching
				// the rendered string would miss the VM routes entirely —
				// they would test the literal ":snap_name", which the
				// Pattern also refuses, and pass for the wrong reason.
				target := render(strings.Replace(rt.path, ":snap_name", payload.value, 1))

				status, env := send(t, app, httptest.NewRequest(rt.method, target, nil))
				if cap.called {
					t.Fatalf("snap_name %q reached the handler; it would be interpolated into a "+
						"Proxmox path, and Proxmox decodes the escape before it resolves that path",
						payload.value)
				}
				if status != payload.want {
					t.Errorf("snap_name %q: status = %d (%q), want %d — %s",
						payload.value, status, env.Message, payload.want, payload.why)
				}
				if payload.want == fiber.StatusBadRequest && !strings.Contains(env.Message, "snap_name") {
					t.Errorf("snap_name %q: message = %q, want the DECLARATION to be what refused "+
						"it, naming the parameter", payload.value, env.Message)
				}
			})
		}

		// The control. Without it every assertion above would still hold if
		// the route refused everything, and the test would be measuring
		// nothing.
		t.Run(rt.method+" "+rt.path+" accepts a real name", func(t *testing.T) {
			cap := &capture{}
			probe := probeEndpoint
			if rt.render != nil {
				probe = probeCTEndpoint
			}
			app := newRegistryApp(t, noAuth(), probe(t, rt.method, rt.path, cap))
			render := rt.render
			if render == nil {
				render = func(p string) string {
					return strings.NewReplacer(":cluster_id", testClusterID, ":vm_id", testVMID).Replace(p)
				}
			}
			target := strings.Replace(render(rt.path), ":snap_name", "snap01", 1)
			if status, env := send(t, app, httptest.NewRequest(rt.method, target, nil)); status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204 — a legitimate snapshot name must reach the "+
					"handler, or the refusals above prove nothing", status, env.Message)
			}
			if !cap.called {
				t.Error("a legitimate snapshot name did not reach the handler")
			}
		})
	}
}
