package api

import (
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

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
