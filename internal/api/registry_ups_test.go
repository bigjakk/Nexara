package api

// upsRouteCount is how many endpoints registerUPSEndpoints declares. See
// vmRouteCount in registry_vms_test.go for why the registry total is a sum of
// per-domain constants rather than one number.
const upsRouteCount = 11

// upsRoutesOutsideTheClusterCheckShape lists every UPS route: all of them are
// global Checks, because a UPS powers machines rather than a cluster's objects
// and one NUT server can feed the nodes of several clusters.
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
	} {
		out[r] = global
	}
	return out
}()
