package handlers

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
)

// nodeReportTimeout bounds the Proxmox call behind GetNodeReport on the
// uncached path.
//
// `pvereport` shells out to a long list of commands on the node — pvesm, zpool,
// lvs, pveversion, a dump of /etc/pve and more — so it routinely takes tens of
// seconds on a busy host and the default client timeout is not enough.
//
// It applies only when CreateProxmoxClient falls through to building a client:
// a cached client keeps the cache's own timeout (proxmox.CachedClientTimeout,
// currently 5 minutes) and ignores this, which the server does wire up. Both
// bounds are comfortably longer than a report takes, so the request is bounded
// either way — but this constant is not the number in force in production.
const nodeReportTimeout = 3 * time.Minute

// GetNodeReport handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/report.
//
// It returns the node's `pvereport` bundle as a plain-text download — the
// artefact to attach when asking for help with a cluster, so an operator does
// not have to SSH in to produce one.
//
// Gated on manage:node rather than view:node, and audited. The bundle is a full
// disclosure of the host: storage configuration including backing paths,
// network layout, the installed package inventory and every guest's config. A
// read-only account that can legitimately watch a node's CPU graph has no
// business exporting all of that, so this sits with the other operator actions
// (evacuate, reboot, shutdown) that migration 000044 defined manage:node for.
func (h *NodeHandler) GetNodeReport(c fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}

	pxClient, err := CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID, nodeReportTimeout)
	if err != nil {
		return err
	}

	report, err := pxClient.GetNodeReport(c.Context(), nodeName)
	if err != nil {
		// Mapped rather than flattened to 502: the likeliest real failure is the
		// cluster's API token lacking Sys.Audit on this node, and mapProxmoxError
		// surfaces Proxmox's own sentence with a 403 instead of an opaque gateway
		// error the operator cannot act on.
		return mapProxmoxError(err)
	}

	// Audited after the fetch succeeds: a failed attempt produced no
	// disclosure, and recording it as one would misreport what happened.
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "node", nodeName, "download_report", nil)

	c.Set("Content-Type", "text/plain; charset=utf-8")
	c.Set("X-Content-Type-Options", "nosniff")
	c.Set("Content-Disposition", "attachment; filename=\""+nodeReportFilename(nodeName)+"\"")
	return c.SendString(report)
}

// nodeReportFilename builds the download filename for a node's report.
//
// The node name reaches a Content-Disposition header here, so everything that
// is not plainly safe in a quoted filename is folded to "-". That covers the
// header-injection characters (CR, LF, ") as a class rather than by listing
// them, and keeps the result a sane filename on every platform. An empty or
// fully-stripped name still yields a usable download rather than a bare
// extension.
func nodeReportFilename(nodeName string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, nodeName)

	safe = strings.Trim(safe, "-.")
	if safe == "" {
		safe = "node"
	}
	return "nexara-report-" + safe + "-" + time.Now().UTC().Format("20060102-150405") + ".txt"
}
