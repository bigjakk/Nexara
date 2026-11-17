package proxmox

import (
	"errors"
	"strings"
)

// IsCephNotConfigured reports whether a Ceph read failed because the answering
// node has no Ceph, rather than because the read could not be made. The
// sentences are pve-manager PVE/Ceph/Tools.pm's check_ceph_installed,
// check_ceph_inited and check_ceph_configured, which the Ceph routes call first.
func IsCephNotConfigured(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "binary not installed") ||
		strings.Contains(msg, "pveceph configuration not initialized") ||
		strings.Contains(msg, "ceph not fully configured")
}
