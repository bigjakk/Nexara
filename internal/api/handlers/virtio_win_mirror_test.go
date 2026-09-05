package handlers

import (
	"testing"

	"github.com/bigjakk/nexara/internal/virtiowin"
)

// TestVirtioWinMirrorSettingKeyMatches ties the local literal to the one the
// engine reads. The literal exists only because
// TestGuard_GlobalSettingKeysClassified resolves keys statically within this
// package and cannot follow a qualified selector; if the two drift, the handler
// writes a row the engine never reads and the mirror silently does nothing.
func TestVirtioWinMirrorSettingKeyMatches(t *testing.T) {
	if virtioWinMirrorSettingKey != virtiowin.MirrorSettingKey {
		t.Errorf("handler key %q != virtiowin.MirrorSettingKey %q",
			virtioWinMirrorSettingKey, virtiowin.MirrorSettingKey)
	}
}

// TestVirtioWinMirrorSettingKeyIsReserved states the security property in a
// test rather than only in a comment: unreserved, the key is writable through
// the generic settings PUT, which performs none of SetMirror's URL validation.
func TestVirtioWinMirrorSettingKeyIsReserved(t *testing.T) {
	owner, reserved := reservedSettingOwner("global", virtioWinMirrorSettingKey)
	if !reserved {
		t.Fatalf("%q is not reserved; the generic settings endpoints would accept an unvalidated URL",
			virtioWinMirrorSettingKey)
	}
	if owner.Endpoint != "/api/v1/virtio-win/mirror" {
		t.Errorf("reserved owner endpoint = %q, want the mirror endpoint", owner.Endpoint)
	}
}
