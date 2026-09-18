package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, vmParams and withParams — lives in
// registry_vms.go, where the first migrated domain defined it.

// metricRangeParam is the time window all three historical-metric routes take.
//
// The Enum is the handler's own rangeDurations map, restated: the value also
// SELECTS THE HYPERTABLE (1h and 6h read the 5-minute continuous aggregate, 24h
// and 7d the hourly one), so an unlisted value is not a slower query but a
// switch statement that matches nothing and returns an empty series. The
// handler answered 400 for that already; the Enum moves the same answer one
// layer earlier and puts the accepted set in the documentation.
var metricRangeParam = apischema.Property{
	Type:        apischema.String,
	Optional:    true,
	Default:     "1h",
	Enum:        []string{"1h", "6h", "24h", "7d"},
	Typetext:    "<1h|6h|24h|7d>",
	Description: "Window to report, ending now. 1h and 6h are served at 5-minute resolution, 24h and 7d hourly.",
}

// registerMetricsEndpoints declares MetricsHandler's three routes.
//
// All three are a plain cluster-scoped Check, and each one gates on the
// RESOURCE it reports rather than on the cluster: view:cluster for the
// cluster-wide series, view:vm for a guest's, view:node for a node's. That
// split is load-bearing — the per-guest and per-node queries key on the GUEST's
// or NODE's row id alone, so a caller holding view on cluster A could otherwise
// read a series belonging to cluster B by sending its id. What the declaration
// cannot express, and what therefore stays in the handlers, is the second half
// of that fix: each one re-reads the row and refuses it when its cluster_id is
// not the authorized one.
func registerMetricsEndpoints(reg *Registry, h *handlers.MetricsHandler) {
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/metrics",
		Description: "Return the cluster's historical CPU, memory and I/O series, with I/O counters differenced into per-second rates.",
		Group:       "Metrics",
		Permissions: clusterCheck("view", "cluster"),
		Parameters:  clusterParams(apischema.Properties{"range": metricRangeParam}),
		Handler:     h.GetClusterHistorical,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/vms/:vm_id/metrics",
		Description: "Return one guest's historical CPU, memory and I/O series. The guest must belong to the cluster in the path.",
		Group:       "Metrics",
		Permissions: clusterCheck("view", "vm"),
		Parameters:  vmParams(apischema.Properties{"range": metricRangeParam}),
		Handler:     h.GetVMHistorical,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        clusterScope + "/nodes/:node_id/metrics",
		Description: "Return one node's historical CPU, memory and I/O series. The node must belong to the cluster in the path.",
		Group:       "Metrics",
		Permissions: clusterCheck("view", "node"),
		Parameters: clusterParams(apischema.Properties{
			// :node_id, not :node_name — this is Nexara's own nodes row id,
			// which is why the route can be reached with a node from another
			// cluster and why the handler re-checks the row's cluster_id.
			"node_id": {
				Type:        apischema.String,
				Format:      "uuid",
				Source:      apischema.SourcePath,
				Typetext:    "<uuid>",
				Description: "Nexara node identifier (not the Proxmox node name).",
			},
			"range": metricRangeParam,
		}),
		Handler: h.GetNodeHistorical,
	})
}
