package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// These drive POST …/disks/detach through its REAL declaration. The value of
// its disk parameter becomes the whole of Proxmox's `delete`, and PVE removes
// any config option named there, so what the declaration admits is what this
// route can take out of a VM's config.

const (
	detachRoutePath   = "/api/v1/clusters/:cluster_id/vms/:vm_id/disks/detach"
	detachRouteTarget = "/api/v1/clusters/" + testClusterID + "/vms/" + testVMID + "/disks/detach"
)

// detachBody is a detach request naming key. encoding/json writes it, so the
// control characters in the refusal table travel as JSON escapes.
func detachBody(t *testing.T, key string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"disk": key})
	if err != nil {
		t.Fatalf("encode %q: %v", key, err)
	}
	return string(b)
}

// TestDetachDiskAcceptsEveryDiskKey sends each end of every range in the set —
// qemu-server's drive and unused-disk keys plus vmstate — and requires it to
// reach the handler unchanged.
func TestDetachDiskAcceptsEveryDiskKey(t *testing.T) {
	for _, key := range []string{
		"ide0", "ide3", "sata0", "sata5", "scsi0", "scsi30", "virtio0", "virtio15",
		"efidisk0", "tpmstate0", "unused0", "unused255", "vmstate",
		// The CD-ROM by convention, and where a cloud-init drive usually
		// sits; a drive key upstream all the same.
		"ide2",
	} {
		t.Run(key, func(t *testing.T) {
			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPost, detachRoutePath, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, detachRouteTarget, detachBody(t, key)))
			if status != fiber.StatusNoContent {
				t.Fatalf("status = %d (%q), want 204", status, env.Message)
			}
			if got := cap.params.String("disk"); got != key {
				t.Errorf("disk reached the handler as %q, want %q", got, key)
			}
		})
	}
}

// preDetachRuleDiskParam is the disk parameter this route carried until it had
// a rule of its own: the shared diskKeyParam as it stood. It is frozen here
// rather than read from diskKeyParam because the claim it backs is historical —
// these are the keys the route USED to hand to Proxmox's delete — and the four
// routes still sharing diskKeyParam are free to change it.
var preDetachRuleDiskParam = apischema.Property{
	Type:        apischema.String,
	Pattern:     `^[a-z]+[0-9]*$`,
	MaxLength:   apischema.Ptr(32),
	Typetext:    "<config key>",
	Description: "The detach route's disk parameter before it had a rule of its own.",
}

// TestDetachDiskRefusesAnyKeyThatIsNotADisk requires a 400 that names the
// parameter, before the handler — and so the VM config read and the delete
// behind it — runs.
//
// Each row first sends the same request with preDetachRuleDiskParam in place.
// That precondition is what separates the rows this change newly refuses
// (reachedBefore) from the ones the old pattern already turned away, so a
// fixture cannot pass for a guard it never needed.
func TestDetachDiskRefusesAnyKeyThatIsNotADisk(t *testing.T) {
	tests := []struct {
		key           string
		reachedBefore bool
	}{
		// Config options that are not disks. PVE deletes each one it is
		// handed: the NIC, the boot order, the core count, autostart.
		{"net0", true}, {"boot", true}, {"cores", true}, {"onboot", true},
		// One past each end of the set.
		{"ide4", true}, {"sata6", true}, {"scsi31", true}, {"virtio16", true},
		{"unused256", true}, {"efidisk1", true}, {"tpmstate1", true},
		// Not the key upstream knows it by: option_exists is an exact lookup,
		// and "cdrom" is an alias PVE rewrites to ide2 that the audit lookup
		// cannot resolve.
		{"scsi01", true}, {"cdrom", true},
		// Refused before this change too, and still.
		{"SCSI0", false}, {"", false}, {"..", false}, {".", false},
		{"%2e%2e", false}, {"/", false}, {"scsi0\n", false}, {"scsi0,net0", false},
	}
	for _, tt := range tests {
		t.Run(strconv.Quote(tt.key), func(t *testing.T) {
			body := detachBody(t, tt.key)

			old := &capture{}
			twin := probeEndpoint(t, fiber.MethodPost, detachRoutePath, old)
			twin.Parameters = maps.Clone(twin.Parameters)
			twin.Parameters["disk"] = preDetachRuleDiskParam
			send(t, newRegistryApp(t, noAuth(), twin), jsonRequest(http.MethodPost, detachRouteTarget, body))
			if old.called != tt.reachedBefore {
				t.Fatalf("precondition: under the old pattern the handler ran = %v, want %v; this row's "+
					"reachedBefore is wrong", old.called, tt.reachedBefore)
			}

			cap := &capture{}
			app := newRegistryApp(t, noAuth(), probeEndpoint(t, fiber.MethodPost, detachRoutePath, cap))
			status, env := send(t, app, jsonRequest(http.MethodPost, detachRouteTarget, body))
			if status != fiber.StatusBadRequest {
				t.Fatalf("status = %d (%q), want 400", status, env.Message)
			}
			if !strings.HasPrefix(env.Message, "disk:") {
				t.Errorf("message = %q, want it to name the disk parameter", env.Message)
			}
			if cap.called {
				t.Error("the handler ran for a key the declaration should have refused")
			}
		})
	}
}

// TestDetachDiskKeyRuleIsTheClientsRule keeps the two layers enforcing the set
// on ONE definition. proxmox.Client.DetachDisk compiles the same constant, and
// the boundary tests above cannot see a pasted copy of it: the copy passes
// them today, and goes stale the day the set moves.
func TestDetachDiskKeyRuleIsTheClientsRule(t *testing.T) {
	disk := declaredEndpoint(t, fiber.MethodPost, detachRoutePath).Parameters["disk"]
	if disk.Pattern != proxmox.DetachableDiskKeyPattern {
		t.Errorf("the detach route's disk pattern is %q, want proxmox.DetachableDiskKeyPattern %q; the "+
			"declaration and Client.DetachDisk must refuse the same keys", disk.Pattern, proxmox.DetachableDiskKeyPattern)
	}
}
