package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/safeconv"
	"github.com/bigjakk/nexara/internal/scanner"
)

// CVEHandler handles CVE scanning endpoints.
type CVEHandler struct {
	pool          *pgxpool.Pool
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
	registry      *notifications.Registry

	// engine is the long-lived scanner used by every API-triggered CVE scan
	// in this process. Building it once here (rather than per request) is
	// what makes the in-memory tracker cache effective — without this the
	// CVEClient.trackerData map would be empty on every scan and we'd hit
	// the DB cache on every API trigger even within the cacheTTL.
	engine *scanner.Engine
}

// NewCVEHandler creates a new CVE handler. pool is required for the dual-write
// transaction that keeps cve_notification_configs.channel_ids and the new
// cve_notification_config_channels join table in lockstep; passing nil is
// supported for unit tests that exercise paths above the DB layer.
func NewCVEHandler(pool *pgxpool.Pool, queries *db.Queries, encryptionKey string, eventPub *events.Publisher, registry *notifications.Registry) *CVEHandler {
	return &CVEHandler{
		pool:          pool,
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
		registry:      registry,
		engine:        scanner.NewEngine(queries, encryptionKey, slog.Default().With("component", "cve-engine"), registry),
	}
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
		StartedAt:     s.StartedAt.Format(time.RFC3339Nano),
		CreatedAt:     s.CreatedAt.Format(time.RFC3339Nano),
	}
	if s.ErrorMessage.Valid {
		r.ErrorMessage = s.ErrorMessage.String
	}
	if s.CompletedAt.Valid {
		r.CompletedAt = s.CompletedAt.Time.Format(time.RFC3339Nano)
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
		r.ScannedAt = n.ScannedAt.Time.Format(time.RFC3339Nano)
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
	Severity       string    `json:"severity"`      // Debian tracker urgency
	RiskSeverity   string    `json:"risk_severity"` // bucket derived from risk_score
	RiskScore      float32   `json:"risk_score"`    // 0–10, drives posture
	SSVCLabel      string    `json:"ssvc_label"`    // act/attend/track_star/track
	CVSSScore      float32   `json:"cvss_score"`
	EPSS           *float32  `json:"epss,omitempty"`
	EPSSPercentile *float32  `json:"epss_percentile,omitempty"`
	KEV            bool      `json:"kev"`
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
		RiskSeverity:   v.RiskSeverity,
		RiskScore:      v.RiskScore,
		SSVCLabel:      v.SsvcLabel,
		KEV:            v.Kev,
		Description:    v.Description,
	}
	if v.FixedVersion.Valid {
		r.FixedVersion = v.FixedVersion.String
	}
	if v.CvssScore.Valid {
		r.CVSSScore = v.CvssScore.Float32
	}
	if v.Epss.Valid {
		s := v.Epss.Float32
		r.EPSS = &s
	}
	if v.EpssPercentile.Valid {
		p := v.EpssPercentile.Float32
		r.EPSSPercentile = &p
	}
	return r
}

type securityPostureResponse struct {
	ScanID         uuid.UUID `json:"scan_id"`
	Status         string    `json:"status"`
	TotalVulns     int32     `json:"total_vulns"`
	CriticalCount  int32     `json:"critical_count"`
	HighCount      int32     `json:"high_count"`
	MediumCount    int32     `json:"medium_count"`
	LowCount       int32     `json:"low_count"`
	UnknownCount   int32     `json:"unknown_count"`
	KEVCount       int32     `json:"kev_count"`
	ActCount       int32     `json:"act_count"`
	AttendCount    int32     `json:"attend_count"`
	TrackStarCount int32     `json:"track_star_count"`
	TrackCount     int32     `json:"track_count"`
	TotalNodes     int32     `json:"total_nodes"`
	ScannedNodes   int32     `json:"scanned_nodes"`
	PostureScore   float32   `json:"posture_score"`
	StartedAt      string    `json:"started_at"`
	CompletedAt    string    `json:"completed_at,omitempty"`
}

var validSeverities = map[string]bool{
	"critical": true, "high": true, "medium": true, "low": true, "unknown": true,
}

// --- Handlers ---

// ListScans lists CVE scans for a cluster.
func (h *CVEHandler) ListScans(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "cve_scan", clusterID); err != nil {
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
		Limit:     safeconv.Int32(limit),
		Offset:    safeconv.Int32(offset),
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
func (h *CVEHandler) TriggerScan(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "cve_scan", clusterID); err != nil {
		return err
	}

	// Prevent concurrent scans on the same cluster — but only while the
	// in-flight scan is plausibly alive. A panic or restart mid-scan leaves
	// the row in running/pending forever; without the age cutoff that
	// phantom row blocked every future manual trigger.
	latestScan, err := h.queries.GetLatestCVEScan(c.Context(), clusterID)
	if err == nil && (latestScan.Status == "running" || latestScan.Status == "pending") {
		if time.Since(latestScan.StartedAt) < 2*time.Hour {
			return fiber.NewError(fiber.StatusConflict, "A scan is already in progress for this cluster")
		}
		_ = h.queries.UpdateCVEScanStatus(c.Context(), db.UpdateCVEScanStatusParams{
			ID:           latestScan.ID,
			Status:       "failed",
			ErrorMessage: pgtype.Text{String: "scan abandoned (interrupted by restart or stuck >2h)", Valid: true},
			CompletedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
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

	// Reuse the handler's long-lived scanner Engine so the in-memory
	// Debian-tracker cache survives across API-triggered scans.
	eng := h.engine

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "cve_scan", scan.ID.String(), "cve_scan_triggered", nil)

	// Use a detached context for the background goroutine (Fiber recycles its context)
	bgCtx := context.Background() //nolint:gosec // G118: intentionally detached from request scope for background scan

	// Run scan in background goroutine with panic recovery
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("CVE scan panicked", "cluster_id", clusterID, "scan_id", scan.ID, "panic", r)
				// Without this the row stays "running" forever and blocks
				// future manual scans via the concurrency guard above.
				_ = h.queries.UpdateCVEScanStatus(bgCtx, db.UpdateCVEScanStatusParams{
					ID:           scan.ID,
					Status:       "failed",
					ErrorMessage: pgtype.Text{String: fmt.Sprintf("scan panicked: %v", r), Valid: true},
					CompletedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
				})
				h.eventPub.ClusterEvent(bgCtx, clusterID.String(), events.KindCVEScan, "cve_scan", scan.ID.String(), "failed")
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
func (h *CVEHandler) GetScan(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "cve_scan", clusterID); err != nil {
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
func (h *CVEHandler) ListVulnerabilities(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "cve_scan", clusterID); err != nil {
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
	kevOnly := c.Query("kev") == "true"

	// Validate severity if provided
	if severity != "" && !validSeverities[severity] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid severity value")
	}

	var vulns []db.CveScanVuln

	switch {
	case kevOnly:
		// "Actively exploited" filter — surfaces only KEV-listed rows.
		// Applied independently of severity/nodeID since the dashboard
		// callout deep-links straight here.
		vulns, err = h.queries.ListCVEScanVulnsKEV(c.Context(), scanID)
	case nodeID != "":
		nid, parseErr := uuid.Parse(nodeID)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid node ID")
		}
		vulns, err = h.queries.ListCVEScanVulnsByNode(c.Context(), nid)
	case severity != "":
		vulns, err = h.queries.ListCVEScanVulnsBySeverity(c.Context(), db.ListCVEScanVulnsBySeverityParams{
			ScanID:   scanID,
			Severity: severity,
		})
	default:
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
func (h *CVEHandler) DeleteScan(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "cve_scan", clusterID); err != nil {
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

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "cve_scan", scanID.String(), "cve_scan_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}

// GetSecurityPosture returns the security posture summary for a cluster.
func (h *CVEHandler) GetSecurityPosture(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "cve_scan", clusterID); err != nil {
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

	// Phase 2 stores unknown_count and kev_count directly. Older scans
	// pre-Phase-2 have unknown_count=0 (default); fall back to derivation.
	unknown := summary.UnknownCount
	if unknown == 0 {
		derived := summary.TotalVulns - summary.CriticalCount - summary.HighCount - summary.MediumCount - summary.LowCount
		if derived > 0 {
			unknown = derived
		}
	}

	score := scanner.ComputePostureScore(summary.CriticalCount, summary.HighCount, summary.MediumCount, summary.LowCount, unknown)

	resp := securityPostureResponse{
		ScanID:         summary.ScanID,
		Status:         summary.Status,
		TotalVulns:     summary.TotalVulns,
		CriticalCount:  summary.CriticalCount,
		HighCount:      summary.HighCount,
		MediumCount:    summary.MediumCount,
		LowCount:       summary.LowCount,
		KEVCount:       summary.KevCount,
		UnknownCount:   unknown,
		ActCount:       summary.ActCount,
		AttendCount:    summary.AttendCount,
		TrackStarCount: summary.TrackStarCount,
		TrackCount:     summary.TrackCount,
		TotalNodes:     summary.TotalNodes,
		ScannedNodes:   summary.ScannedNodes,
		PostureScore:   score,
		StartedAt:      summary.StartedAt.Format(time.RFC3339Nano),
	}
	if summary.CompletedAt.Valid {
		resp.CompletedAt = summary.CompletedAt.Time.Format(time.RFC3339Nano)
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
func (h *CVEHandler) GetSchedule(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "cve_scan", clusterID); err != nil {
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
		UpdatedAt:     schedule.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// UpdateSchedule updates the CVE scan schedule for a cluster.
func (h *CVEHandler) UpdateSchedule(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "cve_scan", clusterID); err != nil {
		return err
	}

	var req updateCVEScheduleRequest
	if err := c.Bind().Body(&req); err != nil {
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

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "cve_scan", clusterID.String(), "cve_scan_schedule_updated", nil)

	return c.JSON(cveScanScheduleResponse{
		ClusterID:     schedule.ClusterID,
		Enabled:       schedule.Enabled,
		IntervalHours: schedule.IntervalHours,
		UpdatedAt:     schedule.UpdatedAt.Format(time.RFC3339Nano),
	})
}

// --- CVE notification config ---

type cveNotifyConfigResponse struct {
	ClusterID       uuid.UUID   `json:"cluster_id"`
	Enabled         bool        `json:"enabled"`
	NotifyOnAct     bool        `json:"notify_on_act"`
	NotifyOnAttend  bool        `json:"notify_on_attend"`
	ChannelIDs      []uuid.UUID `json:"channel_ids"`
	CooldownMinutes int32       `json:"cooldown_minutes"`
	LastNotifiedAt  string      `json:"last_notified_at,omitempty"`
}

type updateCVENotifyConfigRequest struct {
	Enabled         *bool       `json:"enabled"`
	NotifyOnAct     *bool       `json:"notify_on_act"`
	NotifyOnAttend  *bool       `json:"notify_on_attend"`
	ChannelIDs      []uuid.UUID `json:"channel_ids"`
	CooldownMinutes *int32      `json:"cooldown_minutes"`
}

// GetCVENotificationConfig returns the per-cluster CVE notification config.
// Falls back to disabled defaults when no config exists yet.
func (h *CVEHandler) GetCVENotificationConfig(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "cve_scan", clusterID); err != nil {
		return err
	}

	cfg, err := h.queries.GetCVENotificationConfig(c.Context(), clusterID)
	if err != nil {
		return c.JSON(cveNotifyConfigResponse{
			ClusterID:       clusterID,
			Enabled:         false,
			NotifyOnAct:     true,
			NotifyOnAttend:  false,
			ChannelIDs:      []uuid.UUID{},
			CooldownMinutes: 60,
		})
	}

	// 4.8b read-flip: channel list comes from the join table, not the
	// dual-written array. The array can hold stale UUIDs if a channel was
	// deleted (FK on the join cleans up; array has none).
	channelIDs, err := h.queries.ListCVENotificationConfigChannels(c.Context(), clusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to load notification channels")
	}
	if channelIDs == nil {
		channelIDs = []uuid.UUID{}
	}

	resp := cveNotifyConfigResponse{
		ClusterID:       cfg.ClusterID,
		Enabled:         cfg.Enabled,
		NotifyOnAct:     cfg.NotifyOnAct,
		NotifyOnAttend:  cfg.NotifyOnAttend,
		ChannelIDs:      channelIDs,
		CooldownMinutes: cfg.CooldownMinutes,
	}
	if cfg.LastNotifiedAt.Valid {
		resp.LastNotifiedAt = cfg.LastNotifiedAt.Time.Format(time.RFC3339Nano)
	}
	return c.JSON(resp)
}

// UpdateCVENotificationConfig upserts the per-cluster CVE notification config.
func (h *CVEHandler) UpdateCVENotificationConfig(c fiber.Ctx) error {
	clusterID, err := clusterIDFromParam(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "cve_scan", clusterID); err != nil {
		return err
	}

	var req updateCVENotifyConfigRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Defaults / merge from existing.
	enabled := false
	notifyOnAct := true
	notifyOnAttend := false
	channelIDs := []uuid.UUID{}
	cooldownMinutes := int32(60)
	if existing, err := h.queries.GetCVENotificationConfig(c.Context(), clusterID); err == nil {
		enabled = existing.Enabled
		notifyOnAct = existing.NotifyOnAct
		notifyOnAttend = existing.NotifyOnAttend
		cooldownMinutes = existing.CooldownMinutes
		// 4.8c: the legacy array column is gone; the join table is the
		// single source of truth for the existing channel set. A read
		// error here surfaces as a 500 because there's no other
		// representation to fall back to — losing the existing channels
		// silently and rewriting the row with an empty default is worse
		// than a transient failure that the user can retry.
		existingChannels, lerr := h.queries.ListCVENotificationConfigChannels(c.Context(), clusterID)
		if lerr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to load existing notification channels")
		}
		channelIDs = existingChannels
	}
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.NotifyOnAct != nil {
		notifyOnAct = *req.NotifyOnAct
	}
	if req.NotifyOnAttend != nil {
		notifyOnAttend = *req.NotifyOnAttend
	}
	if req.ChannelIDs != nil {
		channelIDs = req.ChannelIDs
	}
	if req.CooldownMinutes != nil {
		cooldownMinutes = *req.CooldownMinutes
	}

	if cooldownMinutes < 0 || cooldownMinutes > 10080 {
		return fiber.NewError(fiber.StatusBadRequest, "Cooldown must be 0–10080 minutes")
	}
	if enabled && len(channelIDs) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "At least one channel is required when enabled")
	}
	if enabled && !notifyOnAct && !notifyOnAttend {
		return fiber.NewError(fiber.StatusBadRequest, "At least one severity (Act or Attend) must be selected")
	}

	for _, cid := range channelIDs {
		if _, err := h.queries.GetNotificationChannel(c.Context(), cid); err != nil {
			return fiber.NewError(fiber.StatusBadRequest,
				fmt.Sprintf("Channel %s does not exist", cid))
		}
	}

	// 4.8c: array column dropped; the join table is the single source of
	// truth. The upsert + clear children + per-row insert still all run in
	// one transaction so the config row and its channels commit together.
	// If the pool isn't wired (unit-test path) fall back to the legacy
	// single-statement upsert without a join-table write.
	var cfg db.CveNotificationConfig
	if h.pool != nil {
		tx, err := h.pool.Begin(c.Context())
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to start transaction")
		}
		defer func() {
			rbCtx, rbCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer rbCancel()
			if rbErr := tx.Rollback(rbCtx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
				slog.Warn("update cve notification config: rollback failed", "error", rbErr)
			}
		}()

		qx := h.queries.WithTx(tx)
		cfg, err = qx.UpsertCVENotificationConfig(c.Context(), db.UpsertCVENotificationConfigParams{
			ClusterID:       clusterID,
			Enabled:         enabled,
			NotifyOnAct:     notifyOnAct,
			NotifyOnAttend:  notifyOnAttend,
			CooldownMinutes: cooldownMinutes,
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to update notification config")
		}
		if err := qx.DeleteCVENotificationConfigChannels(c.Context(), clusterID); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to update notification channels")
		}
		for _, chID := range channelIDs {
			if err := qx.InsertCVENotificationConfigChannel(c.Context(), db.InsertCVENotificationConfigChannelParams{
				ConfigID:  clusterID,
				ChannelID: chID,
			}); err != nil {
				// Pre-flight GetNotificationChannel runs outside the tx; a
				// concurrent channel delete between then and now races the
				// FK insert. Surface as 409 with an actionable message
				// instead of an opaque 500.
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.ForeignKeyViolation {
					return fiber.NewError(fiber.StatusConflict,
						"One or more channels were deleted while saving; refresh and retry")
				}
				return fiber.NewError(fiber.StatusInternalServerError, "Failed to update notification channels")
			}
		}
		if err := tx.Commit(c.Context()); err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to commit notification config update")
		}
	} else {
		var err error
		cfg, err = h.queries.UpsertCVENotificationConfig(c.Context(), db.UpsertCVENotificationConfigParams{
			ClusterID:       clusterID,
			Enabled:         enabled,
			NotifyOnAct:     notifyOnAct,
			NotifyOnAttend:  notifyOnAttend,
			CooldownMinutes: cooldownMinutes,
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to update notification config")
		}
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "cve_scan", clusterID.String(), "cve_notify_config_updated", nil)

	resp := cveNotifyConfigResponse{
		ClusterID:      cfg.ClusterID,
		Enabled:        cfg.Enabled,
		NotifyOnAct:    cfg.NotifyOnAct,
		NotifyOnAttend: cfg.NotifyOnAttend,
		// 4.8b read-flip: return the in-flight slice we just dual-wrote.
		// The transaction has committed, so this matches the join table
		// exactly and avoids a redundant SELECT.
		ChannelIDs:      channelIDs,
		CooldownMinutes: cfg.CooldownMinutes,
	}
	if cfg.LastNotifiedAt.Valid {
		resp.LastNotifiedAt = cfg.LastNotifiedAt.Time.Format(time.RFC3339Nano)
	}
	return c.JSON(resp)
}
