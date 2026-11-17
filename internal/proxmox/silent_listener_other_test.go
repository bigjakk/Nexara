//go:build !linux

package proxmox

import "testing"

// silentListener relies on Linux dropping a SYN to a full accept queue.
func silentListener(t *testing.T, _ string) { t.Skip("needs Linux") }
