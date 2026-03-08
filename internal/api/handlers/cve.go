package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/scanner"
)

// CVEHandler handles CVE scanning endpoints.
type CVEHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
}

// NewCVEHandler creates a new CVE handler.
func NewCVEHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *CVEHandler {
	return &CVEHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
	}
}

func (h *CVEHandler) auditLog(c *fiber.Ctx, clusterID uuid.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), resourceType, resourceID, action, details)
}

// --- Response types ---

type cveScanResponse struct {
	ID            uuid.UUID `json:"id"`
	ClusterID     uuid.UUID `json:"cluster_id"`
	Status        string    `json:"status"`
	TotalNodes    int32     `json:"total_nodes"`
	ScannedNodes  int32     `json:"scanned_nodes"`
	TotalVulns    int32     `json:"total_vulns"`
	CriticalCount int32     `json:"critical_count"`
	HighCount     int32     `json:"high_count"`
	MediumCount   int32     `json:"medium_count"`
	LowCount      int32     `json:"low_count"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	StartedAt     string    `json:"started_at"`
	CompletedAt   string    `json:"completed_at,omitempty"`
	CreatedAt     string    `json:"created_at"`
}

func toCVEScanResponse(s db.CveScan) cveScanResponse {
	r := cveScanResponse{
		ID:            s.ID,
		ClusterID:     s.ClusterID,
		Status:        s.Status,
		TotalNodes:    s.TotalNodes,
		ScannedNodes:  s.ScannedNodes,
		TotalVulns:    s.TotalVulns,
		CriticalCount: s.CriticalCount,
		HighCount:     s.HighCount,
		MediumCount:   s.MediumCount,
		LowCount:      s.LowCount,
		StartedAt:     s.StartedAt.Format("2006-01-02T15:04:05Z"),
		CreatedAt:     s.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}
	if s.ErrorMessage.Valid {
		r.ErrorMessage = s.ErrorMessage.String
	}
	if s.CompletedAt.Valid {
		r.CompletedAt = s.CompletedAt.Time.Format("2006-01-02T15:04:05Z")
	}
	return r
}

type cveScanNodeResponse struct {
	ID            uuid.UUID `json:"id"`
	ScanID        uuid.UUID `json:"scan_id"`
	NodeID        uuid.UUID `json:"node_id"`
	NodeName      string    `json:"node_name"`
	Status        string    `json:"status"`
	PackagesTotal int32     `json:"packages_total"`
	VulnsFound    int32     `json:"vulns_found"`
	PostureScore  float32   `json:"posture_score"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	ScannedAt     string    `json:"scanned_at,omitempty"`
}

func toCVEScanNodeResponse(n db.CveScanNode) cveScanNodeResponse {
	r := cveScanNodeResponse{
		ID:            n.ID,
		ScanID:        n.ScanID,
		NodeID:        n.NodeID,
		NodeName:      n.NodeName,
		Status:        n.Status,
		PackagesTotal: n.PackagesTotal,
		VulnsFound:    n.VulnsFound,
	}
	if n.PostureScore.Valid {
		r.PostureScore = n.PostureScore.Float32
	}
	if n.ErrorMessage.Valid {
		r.ErrorMessage = n.ErrorMessage.String
	}
	if n.ScannedAt.Valid {
		r.ScannedAt = n.ScannedAt.Time.Format("2006-01-02T15:04:05Z")
	}
	return r
}

type cveScanVulnResponse struct {
	ID             uuid.UUID `json:"id"`
	ScanID         uuid.UUID `json:"scan_id"`
	ScanNodeID     uuid.UUID `json:"scan_node_id"`
	CVEID          string    `json:"cve_id"`
	PackageName    string    `json:"package_name"`
	CurrentVersion string    `json:"current_version"`
	FixedVersion   string    `json:"fixed_version,omitempty"`
	Severity       string    `json:"severity"`
	CVSSScore      float32   `json:"cvss_score"`
	Description    string    `json:"description"`
}

func toCVEScanVulnResponse(v db.CveScanVuln) cveScanVulnResponse {
	r := cveScanVulnResponse{
		ID:             v.ID,
		ScanID:         v.ScanID,
		ScanNodeID:     v.ScanNodeID,
		CVEID:          v.CveID,
		PackageName:    v.PackageName,
		CurrentVersion: v.CurrentVersion,
		Severity:       v.Severity,
		Description:    v.Description,
	}
	if v.FixedVersion.Valid {
		r.FixedVersion = v.FixedVersion.String
	}
	if v.CvssScore.Valid {
		r.CVSSScore = v.CvssScore.Float32
	}
	return r
}

type securityPostureResponse struct {
	ScanID        uuid.UUID `json:"scan_id"`
	Status        string    `json:"status"`
	TotalVulns    int32     `json:"total_vulns"`
	CriticalCount int32     `json:"critical_count"`
	HighCount     int32     `json:"high_count"`
	MediumCount   int32     `json:"medium_count"`
	LowCount      int32     `json:"low_count"`
	TotalNodes    int32     `json:"total_nodes"`
	ScannedNodes  int32     `json:"scanned_nodes"`
	PostureScore  float32   `json:"posture_score"`
	StartedAt     string    `json:"started_at"`
	CompletedAt   string    `json:"completed_at,omitempty"`
}

var validSeverities = map[string]bool{
	"critical": true, "high": true, "medium": true, "low": true, "unknown": true,
}

// --- Handlers ---

// ListScans lists CVE scans for a cluster.
func (h *CVEHandler) ListScans(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cve_scan"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	limit, _ := strconv.Atoi(c.Query("limit", "20"))
	offset, _ := strconv.Atoi(c.Query("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	scans, err := h.queries.ListCVEScans(c.Context(), db.ListCVEScansParams{
		ClusterID: clusterID,
		Limit:     int32(limit),
		Offset:    int32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list scans")
	}

	result := make([]cveScanResponse, len(scans))
	for i, s := range scans {
		result[i] = toCVEScanResponse(s)
	}
	return c.JSON(result)
}

// TriggerScan starts a new CVE scan for a cluster.
func (h *CVEHandler) TriggerScan(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cve_scan"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	// Prevent concurrent scans on the same cluster
	latestScan, err := h.queries.GetLatestCVEScan(c.Context(), clusterID)
	if err == nil && (latestScan.Status == "running" || latestScan.Status == "pending") {
		return fiber.NewError(fiber.StatusConflict, "A scan is already in progress for this cluster")
	}

	// Create scan record upfront so the frontend can see it immediately
	scan, err := h.queries.InsertCVEScan(c.Context(), db.InsertCVEScanParams{
		ClusterID:  clusterID,
		Status:     "pending",
		TotalNodes: 0,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create scan record")
	}

	// Create the scan engine with a proper logger
	eng := scanner.NewEngine(h.queries, h.encryptionKey, slog.Default())

	h.auditLog(c, clusterID, "cve_scan", scan.ID.String(), "cve_scan_triggered", nil)

	// Use a detached context for the background goroutine (Fiber recycles its context)
	bgCtx := context.Background()

	// Run scan in background goroutine with panic recovery
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("CVE scan panicked", "cluster_id", clusterID, "scan_id", scan.ID, "panic", r)
			}
		}()

		_, scanErr := eng.RunScanWithID(bgCtx, clusterID, scan.ID)
		if scanErr != nil {
			slog.Error("CVE scan failed", "cluster_id", clusterID, "scan_id", scan.ID, "error", scanErr)
			h.eventPub.ClusterEvent(bgCtx, clusterID.String(), events.KindCVEScan, "cve_scan", scan.ID.String(), "failed")
			return
		}
		h.eventPub.ClusterEvent(bgCtx, clusterID.String(), events.KindCVEScan, "cve_scan", scan.ID.String(), "completed")
	}()

	return c.Status(fiber.StatusAccepted).JSON(toCVEScanResponse(scan))
}

// GetScan returns a single CVE scan with its node results.
func (h *CVEHandler) GetScan(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cve_scan"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	scanID, err := uuid.Parse(c.Params("scan_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid scan ID")
	}

	scan, err := h.queries.GetCVEScan(c.Context(), scanID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Scan not found")
	}

	// Verify the scan belongs to this cluster
	if scan.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Scan not found")
	}

	nodes, err := h.queries.ListCVEScanNodes(c.Context(), scanID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list scan nodes")
	}

	nodeResponses := make([]cveScanNodeResponse, len(nodes))
	for i, n := range nodes {
		nodeResponses[i] = toCVEScanNodeResponse(n)
	}

	return c.JSON(fiber.Map{
		"scan":  toCVEScanResponse(scan),
		"nodes": nodeResponses,
	})
}

// ListVulnerabilities returns vulnerabilities for a scan.
func (h *CVEHandler) ListVulnerabilities(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cve_scan"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	scanID, err := uuid.Parse(c.Params("scan_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid scan ID")
	}

	// Verify the scan belongs to this cluster
	scan, err := h.queries.GetCVEScan(c.Context(), scanID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Scan not found")
	}
	if scan.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Scan not found")
	}

	severity := c.Query("severity")
	nodeID := c.Query("node_id")

	// Validate severity if provided
	if severity != "" && !validSeverities[severity] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid severity value")
	}

	var vulns []db.CveScanVuln

	if nodeID != "" {
		nid, parseErr := uuid.Parse(nodeID)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid node ID")
		}
		vulns, err = h.queries.ListCVEScanVulnsByNode(c.Context(), nid)
	} else if severity != "" {
		vulns, err = h.queries.ListCVEScanVulnsBySeverity(c.Context(), db.ListCVEScanVulnsBySeverityParams{
			ScanID:   scanID,
			Severity: severity,
		})
	} else {
		vulns, err = h.queries.ListCVEScanVulns(c.Context(), scanID)
	}

	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list vulnerabilities")
	}

	result := make([]cveScanVulnResponse, len(vulns))
	for i, v := range vulns {
		result[i] = toCVEScanVulnResponse(v)
	}
	return c.JSON(result)
}

// DeleteScan deletes a CVE scan and its results.
func (h *CVEHandler) DeleteScan(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cve_scan"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	scanID, err := uuid.Parse(c.Params("scan_id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid scan ID")
	}

	// Verify the scan belongs to this cluster
	scan, err := h.queries.GetCVEScan(c.Context(), scanID)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Scan not found")
	}
	if scan.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Scan not found")
	}

	if err := h.queries.DeleteCVEScan(c.Context(), scanID); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete scan")
	}

	h.auditLog(c, clusterID, "cve_scan", scanID.String(), "cve_scan_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}

// GetSecurityPosture returns the security posture summary for a cluster.
func (h *CVEHandler) GetSecurityPosture(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cve_scan"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	summary, err := h.queries.GetClusterSecuritySummary(c.Context(), clusterID)
	if err != nil {
		// No scans yet
		return c.JSON(securityPostureResponse{
			Status:       "no_scans",
			PostureScore: 100,
		})
	}

	score := computePostureScore(summary.CriticalCount, summary.HighCount, summary.MediumCount, summary.LowCount)

	resp := securityPostureResponse{
		ScanID:        summary.ScanID,
		Status:        summary.Status,
		TotalVulns:    summary.TotalVulns,
		CriticalCount: summary.CriticalCount,
		HighCount:     summary.HighCount,
		MediumCount:   summary.MediumCount,
		LowCount:      summary.LowCount,
		TotalNodes:    summary.TotalNodes,
		ScannedNodes:  summary.ScannedNodes,
		PostureScore:  score,
		StartedAt:     summary.StartedAt.Format("2006-01-02T15:04:05Z"),
	}
	if summary.CompletedAt.Valid {
		resp.CompletedAt = summary.CompletedAt.Time.Format("2006-01-02T15:04:05Z")
	}

	return c.JSON(resp)
}

// --- Schedule endpoints ---

type cveScanScheduleResponse struct {
	ClusterID     uuid.UUID `json:"cluster_id"`
	Enabled       bool      `json:"enabled"`
	IntervalHours int32     `json:"interval_hours"`
	UpdatedAt     string    `json:"updated_at"`
}

type updateCVEScheduleRequest struct {
	Enabled       *bool  `json:"enabled"`
	IntervalHours *int32 `json:"interval_hours"`
}

// GetSchedule returns the CVE scan schedule for a cluster.
func (h *CVEHandler) GetSchedule(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "cve_scan"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	schedule, err := h.queries.GetCVEScanSchedule(c.Context(), clusterID)
	if err != nil {
		// No schedule configured yet — return defaults
		return c.JSON(cveScanScheduleResponse{
			ClusterID:     clusterID,
			Enabled:       true,
			IntervalHours: 24,
		})
	}

	return c.JSON(cveScanScheduleResponse{
		ClusterID:     schedule.ClusterID,
		Enabled:       schedule.Enabled,
		IntervalHours: schedule.IntervalHours,
		UpdatedAt:     schedule.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

// UpdateSchedule updates the CVE scan schedule for a cluster.
func (h *CVEHandler) UpdateSchedule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "cve_scan"); err != nil {
		return err
	}

	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}

	var req updateCVEScheduleRequest
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Get current values for defaults
	enabled := true
	intervalHours := int32(24)
	existing, err := h.queries.GetCVEScanSchedule(c.Context(), clusterID)
	if err == nil {
		enabled = existing.Enabled
		intervalHours = existing.IntervalHours
	}

	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.IntervalHours != nil {
		intervalHours = *req.IntervalHours
	}

	// Validate interval
	if intervalHours < 1 || intervalHours > 168 {
		return fiber.NewError(fiber.StatusBadRequest, "Interval must be between 1 and 168 hours")
	}

	schedule, err := h.queries.UpsertCVEScanSchedule(c.Context(), db.UpsertCVEScanScheduleParams{
		ClusterID:     clusterID,
		Enabled:       enabled,
		IntervalHours: intervalHours,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update schedule")
	}

	h.auditLog(c, clusterID, "cve_scan", clusterID.String(), "cve_scan_schedule_updated", nil)

	return c.JSON(cveScanScheduleResponse{
		ClusterID:     schedule.ClusterID,
		Enabled:       schedule.Enabled,
		IntervalHours: schedule.IntervalHours,
		UpdatedAt:     schedule.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	})
}

func computePostureScore(critical, high, medium, low int32) float32 {
	score := float32(100) - float32(critical*25+high*10+medium*3+low*1)
	if score < 0 {
		return 0
	}
	return score
}
