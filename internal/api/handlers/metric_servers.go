package handlers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// All five routes are declared in internal/api/registry_metric_servers.go,
// which states their cluster-scoped permission (view:cluster for the two reads,
// manage:cluster for the three writes) and their parameters; nothing below
// re-checks either.
//
// What stays here is what the declaration cannot do: blanking the write-only
// InfluxDB token on every read, mapping PVE's several spellings of "no such
// server" onto a 404, and recording an audit row that names the server and
// never its credentials.

// metricServerMissingPhrases are the ways PVE reports a metric server id that
// is not in status.cfg, from pve-manager PVE/API2/Cluster/MetricServer.pm:
//
//	read    "status server entry '<id>' does not exist"
//	update  "no such server '<id>'"
//	delete  no check at all — it reads the absent entry, then hands its undef
//	        type to PVE::Status::Plugin->lookup, which croaks "cannot lookup
//	        undefined type!" (pve-common src/PVE/SectionConfig.pm).
//
// The croak is safe to read as "no entry with that id" here: a parsed section
// always has a type, because the type *is* the section header, so on this
// endpoint an undefined one means the lookup found nothing.
//
// "no such server" is spelled out rather than shortened to "no such ": the same
// update sub also dies "no such option '<k>'" for a bad delete=, which is the
// operator's own parameter and must keep its 400/502 rather than becoming a 404.
var metricServerMissingPhrases = []string{
	"does not exist",
	"no such server",
	"cannot lookup undefined type",
}

// mapMetricServerError is the metric-server half of mapMissingObjectError. One
// sentence covers read, update and delete: the operator's next move is the same
// whichever verb found the id gone.
func mapMetricServerError(err error) error {
	return mapMissingObjectError("No metric server with that ID — the list may be out of date",
		metricServerMissingPhrases, err)
}

// MetricServerHandler handles metric server configuration endpoints.
type MetricServerHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewMetricServerHandler creates a new MetricServerHandler.
func NewMetricServerHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *MetricServerHandler {
	return &MetricServerHandler{queries: queries, encryptionKey: encryptionKey, eventPub: eventPub}
}

func (h *MetricServerHandler) createProxmoxClient(c fiber.Ctx, clusterID uuid.UUID) (*proxmox.Client, error) {
	return CreateProxmoxClient(c, h.queries, h.encryptionKey, clusterID)
}

// ListServers handles GET /clusters/:cluster_id/metric-servers.
func (h *MetricServerHandler) ListServers(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	servers, err := pxClient.GetMetricServers(c.Context())
	if err != nil {
		return mapProxmoxError(err)
	}
	// The InfluxDB token is a write-only credential — never return it on a read.
	for i := range servers {
		servers[i].Token = ""
	}
	return RespondItems(c, servers)
}

// CreateServer handles POST /clusters/:cluster_id/metric-servers.
func (h *MetricServerHandler) CreateServer(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	req := proxmox.CreateMetricServerParams{
		ID:     p.String("server_id"),
		Type:   p.String("type"),
		Server: p.String("server"),
		Port:   int(p.Int("port")),
	}
	t := readMetricServerTransport(p)
	req.Disable, req.VerifyCert = t.Disable, t.VerifyCert
	req.MTU, req.Timeout, req.MaxBodySize = t.MTU, t.Timeout, t.MaxBodySize
	req.Proto, req.Path, req.InfluxDBProto = t.Proto, t.Path, t.InfluxDBProto
	req.Organization, req.Bucket, req.Token = t.Organization, t.Bucket, t.Token

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.CreateMetricServer(c.Context(), req); err != nil {
		return mapDuplicateNameError("A metric server with that ID already exists", err)
	}
	// Names only. The token is write-only and view:audit is a default Viewer
	// grant, so the details payload is picked field by field rather than taken
	// from Params.Raw().
	details, _ := json.Marshal(map[string]string{"id": req.ID, "type": req.Type})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "metric_server", req.ID, "created", details)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"status": "ok"})
}

// GetServer handles GET /clusters/:cluster_id/metric-servers/:server_id.
func (h *MetricServerHandler) GetServer(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	serverID := p.String("server_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	server, err := pxClient.GetMetricServer(c.Context(), serverID)
	if err != nil {
		return mapMetricServerError(err)
	}
	// The InfluxDB token is a write-only credential — never return it on a read.
	if server != nil {
		server.Token = ""
	}
	return c.JSON(server)
}

// UpdateServer handles PUT /clusters/:cluster_id/metric-servers/:server_id.
func (h *MetricServerHandler) UpdateServer(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	serverID := p.String("server_id")
	req := proxmox.UpdateMetricServerParams{
		Server: p.String("server"),
		// *int rather than int: omitting the port must leave the stored one
		// alone, which a zero value cannot say.
		Port:   optIntPtr(p.OptInt("port")),
		Delete: p.String("delete"),
	}
	t := readMetricServerTransport(p)
	req.Disable, req.VerifyCert = t.Disable, t.VerifyCert
	req.MTU, req.Timeout, req.MaxBodySize = t.MTU, t.Timeout, t.MaxBodySize
	req.Proto, req.Path, req.InfluxDBProto = t.Proto, t.Path, t.InfluxDBProto
	req.Organization, req.Bucket, req.Token = t.Organization, t.Bucket, t.Token

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.UpdateMetricServer(c.Context(), serverID, req); err != nil {
		return mapMetricServerError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": serverID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "metric_server", serverID, "updated", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// DeleteServer handles DELETE /clusters/:cluster_id/metric-servers/:server_id.
func (h *MetricServerHandler) DeleteServer(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}
	serverID := p.String("server_id")
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	if err := pxClient.DeleteMetricServer(c.Context(), serverID); err != nil {
		return mapMetricServerError(err)
	}
	details, _ := json.Marshal(map[string]string{"id": serverID})
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "metric_server", serverID, "deleted", details)
	return c.JSON(fiber.Map{"status": "ok"})
}

// metricServerTransport is the block of status.cfg options create and update
// share, read once out of the validated parameters.
//
// One reader for both so the two cannot drift on which fields they accept —
// they are the same block, declared once in metricServerTransportParams
// (internal/api/registry_metric_servers.go), so a field added there has exactly
// one place to be wired in.
//
// Disable and VerifyCert are *int because Proxmox reads "absent" as "leave the
// stored value alone" and "0" as "turn it off"; collapsing the two would write
// disable=0 onto every server whose editor never touched the checkbox.
type metricServerTransport struct {
	Disable       *int
	VerifyCert    *int
	MTU           int
	Timeout       int
	MaxBodySize   int
	Proto         string
	Path          string
	InfluxDBProto string
	Organization  string
	Bucket        string
	Token         string
}

func readMetricServerTransport(p *apischema.Params) metricServerTransport {
	return metricServerTransport{
		Disable:       optIntPtr(p.OptInt("disable")),
		VerifyCert:    optIntPtr(p.OptInt("verify-certificate")),
		MTU:           int(p.Int("mtu")),
		Timeout:       int(p.Int("timeout")),
		MaxBodySize:   int(p.Int("max-body-size")),
		Proto:         p.String("proto"),
		Path:          p.String("path"),
		InfluxDBProto: p.String("influxdbproto"),
		Organization:  p.String("organization"),
		Bucket:        p.String("bucket"),
		Token:         p.String("token"),
	}
}
