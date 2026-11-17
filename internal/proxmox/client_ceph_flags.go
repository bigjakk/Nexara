package proxmox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// CephFlagNoout keeps Ceph from marking a down OSD out, so the data on it is
// not re-replicated while its node reboots.
const CephFlagNoout = "noout"

// cephOSDFlags is pve-manager's get_possible_osd_flags (PVE/Ceph/Tools.pm), the
// enum both flag routes declare for {flag}.
var cephOSDFlags = map[string]bool{
	"pause": true, "noup": true, "nodown": true, "noout": true, "noin": true, "nobackfill": true,
	"norebalance": true, "norecover": true, "noscrub": true, "nodeep-scrub": true,
	"notieragent": true, "noautoscale": true,
}

// GetCephFlag reads GET /cluster/ceph/flags/{flag}, which answers a boolean:
// whether the flag is in `ceph osd dump`'s flags. pve-manager
// PVE/API2/Cluster/Ceph.pm (stable-6, stable-7, stable-8, master) declares it
// with check => ['perm', '/', ['Sys.Audit']] after check_ceph_configured.
func (c *Client) GetCephFlag(ctx context.Context, flag string) (bool, error) {
	if !cephOSDFlags[flag] {
		return false, fmt.Errorf("%w: unknown ceph flag %q", ErrInvalidInput, flag)
	}
	var raw json.RawMessage
	if err := c.do(ctx, "/cluster/ceph/flags/"+url.PathEscape(flag), &raw); err != nil {
		return false, fmt.Errorf("get ceph flag %s: %w", flag, err)
	}
	switch strings.Trim(strings.TrimSpace(string(raw)), `"`) {
	case "1", "true":
		return true, nil
	case "0", "false", "", "null":
		return false, nil
	}
	return false, fmt.Errorf("get ceph flag %s: unexpected value %s", flag, raw)
}

// SetCephFlag sets or unsets one flag through PUT /cluster/ceph/flags/{flag}
// with value=1|0, which runs `ceph osd set|unset {flag}` synchronously and
// returns null. Same file and versions as GetCephFlag; it needs Sys.Modify on /.
// value is a required boolean there, so 0 is sent rather than omitted.
func (c *Client) SetCephFlag(ctx context.Context, flag string, value bool) error {
	if !cephOSDFlags[flag] {
		return fmt.Errorf("%w: unknown ceph flag %q", ErrInvalidInput, flag)
	}
	form := url.Values{}
	form.Set("value", "0")
	if value {
		form.Set("value", "1")
	}
	if err := c.doPut(ctx, "/cluster/ceph/flags/"+url.PathEscape(flag), form, nil); err != nil {
		return fmt.Errorf("set ceph flag %s=%v: %w", flag, value, err)
	}
	return nil
}

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
