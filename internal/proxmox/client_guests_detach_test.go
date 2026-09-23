package proxmox

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

// upstreamMaxUnusedDisks is qemu-server's $MAX_UNUSED_DISKS
// (src/PVE/QemuServer/Drive.pm): a VM config holds unused0 through unused255.
// maxDiskIndex carries the four drive-bus ceilings; this is the one bound
// DetachableDiskKeyPattern encodes that nothing else in the package states.
const upstreamMaxUnusedDisks = 256

// qemuServerDetachableKeys is the set DetachableDiskKeyPattern must admit,
// built the way qemu-server builds it — valid_drive_names_with_unused, plus
// vmstate — from maxDiskIndex rather than from the pattern, so the two
// encodings of the same upstream ceilings are checked against each other.
func qemuServerDetachableKeys() map[string]bool {
	want := map[string]bool{"efidisk0": true, "tpmstate0": true, "vmstate": true}
	for bus, maxIndex := range maxDiskIndex {
		for i := 0; i <= maxIndex; i++ {
			want[bus+strconv.Itoa(i)] = true
		}
	}
	for i := 0; i < upstreamMaxUnusedDisks; i++ {
		want["unused"+strconv.Itoa(i)] = true
	}
	return want
}

// TestDetachableDiskKeyPatternIsQemuServersKeySet holds the pattern to the
// exact set upstream defines, over a universe wide enough to reach past every
// boundary: each prefix with every number to 999, the same numbers again with
// a leading zero, and the neighbouring config keys a detach must never touch.
//
// It is the pin between the pattern and maxDiskIndex. AttachDisk reads the
// ceilings from the map and DetachDisk from the pattern, so if one of them
// moved alone a VM could be given a disk it could not then detach.
func TestDetachableDiskKeyPatternIsQemuServersKeySet(t *testing.T) {
	want := qemuServerDetachableKeys()
	// ide 4 + sata 6 + scsi 31 + virtio 16 + efidisk0 + tpmstate0 +
	// unused 256 + vmstate. Stated as a number so the builder above cannot
	// shrink along with a shrunken maxDiskIndex and agree with it.
	if len(want) != 316 {
		t.Fatalf("qemu-server's detachable key set has %d keys, want 316", len(want))
	}

	prefixes := []string{
		"ide", "sata", "scsi", "virtio", "unused", "efidisk", "tpmstate", "vmstate",
		// Config keys a detach must never reach, several of them ending in a
		// number exactly like a drive key.
		"net", "usb", "hostpci", "serial", "virtiofs", "rng", "mp", "ipconfig", "numa",
	}
	candidates := []string{
		"", "cdrom", "boot", "bootdisk", "cores", "onboot", "vmstatestorage", "rootfs",
		"SCSI0", "Scsi0", " scsi0", "scsi0 ", "scsi0\n", "scsi0,net0", "scsi0;net0", "scsi0 net0",
		"scsi+1", "scsi-1", "scsi1.0", "..", ".", "/", "%2e%2e", "scsi0/..",
	}
	for _, p := range prefixes {
		candidates = append(candidates, p, p+"00", p+"0000")
		for n := 0; n <= 999; n++ {
			candidates = append(candidates, p+strconv.Itoa(n), p+"0"+strconv.Itoa(n))
		}
	}

	// The universe has to REACH every key of the set, or "no mismatch" says
	// nothing about the keys it never tried. That is a property of the
	// candidates, so it is counted without asking the pattern.
	reached := make(map[string]bool, len(want))
	for _, c := range candidates {
		if want[c] {
			reached[c] = true
		}
		if got := detachableDiskKeyRe.MatchString(c); got != want[c] {
			t.Errorf("DetachableDiskKeyPattern matches %q = %v, want %v", c, got, want[c])
		}
	}
	if len(reached) != len(want) {
		t.Errorf("the candidates reach %d of the %d keys in the set; widen them", len(reached), len(want))
	}
}

// detachServer is a stand-in Proxmox that records every config write it is
// sent. Nothing else is routed, so a request anywhere else is a 404 the test
// would see as an error.
func detachServer(t *testing.T) (*Client, *[]url.Values) {
	t.Helper()
	var writes []url.Values
	srv := newTestServer(t, map[string]http.HandlerFunc{
		"/api2/json/nodes/pve-01/qemu/101/config": func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("config request method = %s, want PUT", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			form, err := url.ParseQuery(string(body))
			if err != nil {
				t.Errorf("parse config write %q: %v", body, err)
			}
			writes = append(writes, form)
			jsonResponse(w, nil)
		},
	})
	t.Cleanup(srv.Close)
	return newTestClient(t, srv.URL), &writes
}

// TestDetachDiskSendsTheKeyAsTheWholeDelete drives every range boundary of the
// set through the protected method, and pins what reaches Proxmox: the key as
// the ENTIRE delete field, and the digest only when there is one.
func TestDetachDiskSendsTheKeyAsTheWholeDelete(t *testing.T) {
	keys := []string{
		"ide0", "ide3", "sata0", "sata5", "scsi0", "scsi30", "virtio0", "virtio15",
		"efidisk0", "tpmstate0", "unused0", "unused255", "vmstate",
		// The CD-ROM by convention, and a drive key upstream all the same.
		"ide2",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			c, writes := detachServer(t)
			if err := c.DetachDisk(context.Background(), "pve-01", 101, key, testConfigDigest); err != nil {
				t.Fatalf("DetachDisk(%q): %v", key, err)
			}
			if len(*writes) != 1 {
				t.Fatalf("Proxmox received %d config writes, want 1", len(*writes))
			}
			got := (*writes)[0]
			want := url.Values{"delete": {key}, "digest": {testConfigDigest}}
			if got.Encode() != want.Encode() {
				t.Errorf("config write = %q, want %q", got.Encode(), want.Encode())
			}
		})
	}

	t.Run("an empty digest writes unpinned", func(t *testing.T) {
		c, writes := detachServer(t)
		if err := c.DetachDisk(context.Background(), "pve-01", 101, "unused0", ""); err != nil {
			t.Fatalf("DetachDisk: %v", err)
		}
		if len(*writes) != 1 {
			t.Fatalf("Proxmox received %d config writes, want 1", len(*writes))
		}
		if got, want := (*writes)[0].Encode(), (url.Values{"delete": {"unused0"}}).Encode(); got != want {
			t.Errorf("config write = %q, want %q", got, want)
		}
	})
}

// TestDetachDiskRefusesAKeyThatIsNotADisk is the choke point's own test: a key
// outside the set must not leave the process, whoever the caller is.
//
// Each fixture is one Proxmox would ACT on or at least parse, not merely one
// that looks odd: $update_vm_api deletes any key option_exists knows (net0,
// boot, cores, onboot), split_list turns a comma, semicolon or space into a
// second key, and "cdrom" is an alias it rewrites to ide2. The first subtest
// is the precondition that gives the "nothing was sent" assertions teeth — the
// same client and server DO carry a detach when the key is one.
func TestDetachDiskRefusesAKeyThatIsNotADisk(t *testing.T) {
	c, writes := detachServer(t)

	t.Run("precondition: a disk key reaches the server", func(t *testing.T) {
		if err := c.DetachDisk(context.Background(), "pve-01", 101, "scsi1", testConfigDigest); err != nil {
			t.Fatalf("DetachDisk(scsi1): %v", err)
		}
		if len(*writes) != 1 {
			t.Fatalf("Proxmox received %d config writes, want 1", len(*writes))
		}
	})

	refused := []string{
		// Config options that are not disks at all.
		"net0", "boot", "cores", "onboot", "vmstatestorage",
		// One past each end of the set.
		"ide4", "sata6", "scsi31", "virtio16", "unused256", "efidisk1", "tpmstate1",
		// Not the key upstream knows it by.
		"scsi01", "unused00", "SCSI0", "cdrom",
		// One key smuggling a second one past split_list.
		"scsi0,net0", "scsi0;net0", "scsi0 net0",
		"", "..", ".", "%2e%2e", "/", "scsi0\n",
	}
	for _, key := range refused {
		t.Run(strconv.Quote(key), func(t *testing.T) {
			before := len(*writes)
			err := c.DetachDisk(context.Background(), "pve-01", 101, key, testConfigDigest)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("DetachDisk(%q) = %v, want an error wrapping ErrInvalidInput", key, err)
			}
			if len(*writes) != before {
				t.Errorf("DetachDisk(%q) reached Proxmox although the key was refused: %v", key, (*writes)[before:])
			}
		})
	}
}
