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

// IsCephNotInitialized reports pve-manager's "pveceph configuration not
// initialized" (PVE/Ceph/Tools.pm check_ceph_inited): /etc/pve/ceph.conf, or
// /etc/pve/ceph, is missing. Both live in /etc/pve, the cluster's shared
// filesystem, so every node gives the same answer: the cluster has no Ceph.
func IsCephNotInitialized(err error) bool {
	return err != nil && strings.Contains(err.Error(), "pveceph configuration not initialized")
}

// isCephNotInstalled reports check_ceph_installed's "binary not installed": the
// node that answered has no ceph-mon. That node alone; another may run Ceph.
// Not ErrNotFound: checkStatus drops a 404's body, and a proxy's 404 would read
// as an answer (see IsHARulesUnsupportedError).
func isCephNotInstalled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "binary not installed")
}

// CephPresence is what ReadCephStatus learned about whether the cluster runs Ceph.
type CephPresence int

const (
	// CephPresenceUnknown: a read failed, or a member could not be asked.
	CephPresenceUnknown CephPresence = iota
	CephPresent
	CephAbsent
)

// CephStatusRead is the whole of what ReadCephStatusDetail learned.
type CephStatusRead struct {
	Status   *CephStatus
	Presence CephPresence
	// Err is set whenever Status is nil: the absent answer itself when absent.
	Err error
	// NotInstalled is the endpoint's own "binary not installed" answer, when
	// that is what the cluster route gave.
	NotInstalled error
	// Unreachable counts the members that could not be asked (pveproxy's 595
	// or 596, or no connection); Failed the members that failed otherwise.
	Unreachable, Failed int
}

// ReadCephStatus reads the cluster's Ceph status from a node that runs Ceph;
// see ReadCephStatusDetail.
func (c *Client) ReadCephStatus(ctx context.Context) (*CephStatus, CephPresence, error) {
	r := c.ReadCephStatusDetail(ctx)
	return r.Status, r.Presence, r.Err
}

// ReadCephStatusDetail reads the cluster's Ceph status from a node that runs
// Ceph. GET /cluster/ceph/status has no proxyto, so it is answered by the node
// the client is connected to; on a cluster where that node has no Ceph (a
// compute-only member behind the API address, or the one a failover reached)
// it says "binary not installed" though the other members run Ceph. On that
// answer each member is asked in turn through GET /nodes/{node}/ceph/status,
// which pveproxy forwards (proxyto => 'node'). The cluster is absent only when
// every member says it has no Ceph, or one says the cluster-wide configuration
// is missing; a member that could not be asked, or a member list that is empty
// or unreadable, leaves it unknown.
func (c *Client) ReadCephStatusDetail(ctx context.Context) CephStatusRead {
	status, err := c.GetClusterCephStatus(ctx)
	switch {
	case err == nil:
		return CephStatusRead{Status: status, Presence: CephPresent}
	case IsCephNotInitialized(err):
		return CephStatusRead{Presence: CephAbsent, Err: err}
	case !isCephNotInstalled(err):
		return CephStatusRead{Presence: CephPresenceUnknown, Err: err}
	}
	r := CephStatusRead{Presence: CephAbsent, Err: err, NotInstalled: err}
	nodes, lerr := c.GetNodes(ctx)
	if lerr != nil || len(nodes) == 0 {
		r.Presence, r.Failed = CephPresenceUnknown, 1
		if lerr != nil {
			r.Err = lerr
		}
		return r
	}
	for _, n := range nodes {
		s, nerr := c.GetCephStatus(ctx, n.Node)
		switch {
		case nerr == nil:
			return CephStatusRead{Status: s, Presence: CephPresent}
		case IsCephNotInitialized(nerr):
			return CephStatusRead{Presence: CephAbsent, Err: nerr}
		case isCephNotInstalled(nerr):
			continue
		case memberUnreachable(nerr):
			r.Unreachable++
		default:
			r.Failed++
		}
		r.Presence, r.Err = CephPresenceUnknown, nerr
	}
	return r
}

// memberUnreachable reports a member the endpoint could not forward to (595 or
// 596, AnyEvent::HTTP's own failures that pveproxy relays) or no connection.
func memberUnreachable(err error) bool {
	var apiErr *APIError
	return errors.Is(err, ErrConnectionFailed) ||
		errors.As(err, &apiErr) && (apiErr.StatusCode == 595 || apiErr.StatusCode == 596)
}

// CephFlagReason says, in words the job view can show, why a Ceph flag could
// not be read or changed. Proxmox has the flag routes only under /cluster/ceph
// (pve-manager PVE/API2/Cluster/Ceph.pm; PVE/API2/Ceph.pm, the node routes, has
// none), answered by the node behind the API address with no proxyto, so one
// without Ceph cannot change them.
func CephFlagReason(err error) string {
	switch {
	case isCephNotInstalled(err):
		return "the node behind the cluster's API address has no Ceph, and Proxmox changes Ceph flags only through it"
	case errors.Is(err, ErrForbidden):
		return "the cluster's API token lacks Sys.Modify on /"
	}
	return "Proxmox did not answer"
}
