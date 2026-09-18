package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/api/handlers"
)

// The shared declaration vocabulary this file uses — clusterScope,
// clusterCheck, clusterParams, withParams, optString and optCount — lives in
// registry_vms.go, where the first migrated domain defined it.

// metricServerScope is the collection all five routes hang off.
const metricServerScope = clusterScope + "/metric-servers"

// metricServerIDParam is a metric server's id in status.cfg.
//
// Deliberately looser than pve-configid, for the reason poolIDParam gives: the
// section may have been created outside Nexara, and a format this API refuses
// would make an existing server un-gettable, un-editable and un-deletable. The
// bound matches the section-config ceiling Proxmox applies generally.
//
// The rule is the catalogue's pve-object-id, the same one the firewall and
// SDN object names take; its entry records that PVE's own rule for this id
// is pve-configid, which this is a superset of.
func metricServerIDParam(source apischema.Source, description string) apischema.Property {
	return apischema.Property{
		Type:        apischema.String,
		Source:      source,
		Pattern:     apischema.Rule("pve-object-id"),
		MaxLength:   apischema.Ptr(100),
		Typetext:    "<id>",
		Description: description,
	}
}

// metricServerPathParams is the pair every per-server route carries.
func metricServerPathParams() apischema.Properties {
	return clusterParams(apischema.Properties{
		"server_id": metricServerIDParam(apischema.SourcePath, "Metric server id, as the listing reports it."),
	})
}

// metricServerTokenParam is the InfluxDB v2 API token.
//
// WRITE-ONLY, and both halves of that are enforced elsewhere rather than here:
// the two read endpoints blank Token before responding, and the audit rows for
// all three writes record only the id (and the type, on create) rather than
// Params.Raw() — which matters because view:audit is a default Viewer grant, so
// a Raw() payload would publish this token to every Viewer on the instance.
var metricServerTokenParam = apischema.Property{
	Type:      apischema.String,
	Optional:  true,
	MaxLength: apischema.Ptr(1024),
	Typetext:  "<token>",
	Description: "InfluxDB v2 API token. Write-only: never returned by a read, and never recorded in an " +
		"audit row.",
}

// metricServerTransportParams are the fields create and update share.
//
// They are Proxmox's own status.cfg vocabulary, and the values are theirs to
// version and reject — so this closes the parameter SET without restating an
// enum that would date on the next PVE release. What it does pin is the two
// PVE-style 0/1 flags, which are declared as integers with NO default so that
// "the caller said nothing" stays distinguishable from "the caller said 0": the
// handler forwards them as *int, and a default would start writing
// disable=0 onto every server whose editor never touched the checkbox.
func metricServerTransportParams() apischema.Properties {
	return apischema.Properties{
		"disable": {
			Type:        apischema.Integer,
			Optional:    true,
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(1.0),
			Typetext:    "<0|1>",
			Description: "1 stops Proxmox sending to this server without removing it. Omitted leaves the stored value alone.",
		},
		"verify-certificate": {
			Type:        apischema.Integer,
			Optional:    true,
			Minimum:     apischema.Ptr(0.0),
			Maximum:     apischema.Ptr(1.0),
			Typetext:    "<0|1>",
			Description: "0 accepts the server's TLS certificate without verifying it. Omitted leaves the stored value alone.",
		},
		"mtu":           optCount(65535, "UDP datagram size for the influxdb/graphite UDP transports."),
		"timeout":       optCount(86400, "Connection timeout in seconds, for the HTTP transports."),
		"proto":         optString(16, "<udp|tcp|http|https>", "Transport Proxmox sends over."),
		"path":          optString(256, "<path>", "URL path on the server, for the graphite HTTP transport."),
		"influxdbproto": optString(32, "<udp|http|https>", "InfluxDB transport variant."),
		"organization":  optString(256, "<string>", "InfluxDB v2 organization."),
		"bucket":        optString(256, "<string>", "InfluxDB v2 bucket."),
		"token":         metricServerTokenParam,
		"max-body-size": optCount(1<<30, "Maximum HTTP body size in bytes for the InfluxDB v2 transport."),
	}
}

// registerMetricServerEndpoints declares MetricServerHandler's five routes.
//
// Every one is a plain cluster-scoped Check, and the resource is CLUSTER rather
// than a metric_server of its own: these endpoints edit the cluster's
// status.cfg, so the permission that opens them is the one that edits the
// cluster. view:cluster for the two reads, manage:cluster for the three writes.
// That was the hand-placed check and it is carried across unchanged — widening
// or narrowing it is a separate decision, not a migration.
func registerMetricServerEndpoints(reg *Registry, h *handlers.MetricServerHandler) {
	reg.Register(Endpoint{
		Method: fiber.MethodGet,
		Path:   metricServerScope,
		Description: "List the cluster's external metric servers (InfluxDB, Graphite). The InfluxDB token " +
			"is blanked on every row — it is write-only.",
		Group:       "Metrics",
		Permissions: clusterCheck("view", "cluster"),
		Parameters:  clusterParams(nil),
		Handler:     h.ListServers,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodPost,
		Path:        metricServerScope,
		Description: "Register an external metric server on the cluster.",
		Group:       "Metrics",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters: clusterParams(withParams(apischema.Properties{
			// Named server_id rather than "id", with "id" kept as an ALIAS so
			// every existing caller and Nexara's own dialog keep working.
			// checkPathParams refuses a parameter NAMED id that resolves to
			// anything but the path, because clusterIDFromParam falls back to
			// that name when a route has no :cluster_id — and the refusal is
			// about the NAME rather than about whether this particular path
			// happens to make it safe. An "id" alias stays legal for the same
			// reason registry_replication.go's job_id does: it does not claim
			// to name a cluster.
			"server_id": func() apischema.Property {
				p := metricServerIDParam(apischema.SourceAuto, "Id for the new server, as it will appear in status.cfg. Also accepted as \"id\".")
				p.Alias = "id"
				return p
			}(),
			"type": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(32),
				Typetext:    "<influxdb|graphite>",
				Description: "Server kind. Proxmox owns this vocabulary and rejects one it does not know.",
			},
			"server": {
				Type:        apischema.String,
				MinLength:   apischema.Ptr(1),
				MaxLength:   apischema.Ptr(256),
				Typetext:    "<host>",
				Description: "Hostname or address Proxmox sends to.",
			},
			"port": {
				Type:        apischema.Integer,
				Minimum:     apischema.Ptr(1.0),
				Maximum:     apischema.Ptr(65535.0),
				Typetext:    "<integer>",
				Description: "Destination port.",
			},
		}, metricServerTransportParams())),
		Handler: h.CreateServer,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodGet,
		Path:        metricServerScope + "/:server_id",
		Description: "Get one metric server's configuration. The InfluxDB token is blanked — it is write-only.",
		Group:       "Metrics",
		Permissions: clusterCheck("view", "cluster"),
		Parameters:  metricServerPathParams(),
		Handler:     h.GetServer,
	})
	reg.Register(Endpoint{
		Method: fiber.MethodPut,
		Path:   metricServerScope + "/:server_id",
		Description: "Update a metric server. Fields left out keep their stored value; delete names the " +
			"options to reset, comma-separated.",
		Group:       "Metrics",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters: withParams(metricServerPathParams(), withParams(apischema.Properties{
			"server": optString(256, "<host>", "Replacement hostname or address."),
			"port": {
				Type:     apischema.Integer,
				Optional: true,
				Minimum:  apischema.Ptr(1.0),
				Maximum:  apischema.Ptr(65535.0),
				Typetext: "<integer>",
				// No Default, so p.OptInt reports whether the caller chose: the
				// handler forwards a *int, and a default would rewrite the port
				// on every save that did not mention it.
				Description: "Replacement port. Omitted leaves the stored one alone.",
			},
			"delete": optString(1024, "<option[,option...]>", "Options to reset to their defaults, comma-separated, as Proxmox spells them."),
		}, metricServerTransportParams())),
		Handler: h.UpdateServer,
	})
	reg.Register(Endpoint{
		Method:      fiber.MethodDelete,
		Path:        metricServerScope + "/:server_id",
		Description: "Remove a metric server from the cluster's status.cfg.",
		Group:       "Metrics",
		Permissions: clusterCheck("manage", "cluster"),
		Parameters:  metricServerPathParams(),
		Handler:     h.DeleteServer,
	})
}
