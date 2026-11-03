package api

// upsRouteCount is how many endpoints registerUPSEndpoints declares. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum of
// per-domain constants rather than one number.
const upsRouteCount = 18

// upsRoutesOutsideTheClusterCheckShape lists every UPS route: global, because a
// UPS powers machines rather than a cluster's objects and one NUT server can
// feed the nodes of several clusters. The routes that name a node are
// Deferred: their handler adds view:node on that node's cluster.
var upsRoutesOutsideTheClusterCheckShape = func() map[string]string {
	const global = "global: a NUT server and its UPS devices are not one cluster's objects; the path names no cluster"
	out := map[string]string{}
	for _, r := range []string{
		"POST /api/v1/nut-servers",
		"POST /api/v1/nut-servers/probe",
		"GET /api/v1/nut-servers",
		"GET /api/v1/nut-servers/:id",
		"PUT /api/v1/nut-servers/:id",
		"DELETE /api/v1/nut-servers/:id",
		"POST /api/v1/nut-servers/:id/test",
		"GET /api/v1/ups-devices",
		"GET /api/v1/ups-devices/:id",
		"GET /api/v1/ups-devices/:id/metrics",
		"GET /api/v1/ups-events",
		"GET /api/v1/ups-devices/:id/command-runs",
	} {
		out[r] = global
	}
	out["POST /api/v1/ups-devices/:id/commands"] = "deferred: global execute:ups, and for a command that cuts the UPS's power " +
		"manage:node on every cluster the UPS powers a node of, which the body's command decides"
	out["PUT /api/v1/ups-devices/:id/vars/:name"] = "deferred: global manage:ups, and for a variable that can change the " +
		"power the UPS supplies execute:ups and manage:node on every cluster it powers, which the path's variable decides"
	const byNode = "deferred: global manage:ups, and view:node on the cluster of the node it names, which is in the body or the feed, so no path cluster exists to gate on"
	for _, r := range []string{
		"POST /api/v1/ups-devices/:id/feeds",
		"PUT /api/v1/ups-devices/:id/feeds/:feed_id",
		"DELETE /api/v1/ups-devices/:id/feeds/:feed_id",
		"PUT /api/v1/nut-servers/:id/host-node",
	} {
		out[r] = byNode
	}
	return out
}()
