package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/reports"
	"github.com/bigjakk/nexara/internal/scheduler"
)

// reportSemaphore limits concurrent report generations.
var reportSemaphore = make(chan struct{}, 3)

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// ReportHandler handles report schedules, generation, and run history.
type ReportHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
	generator     *reports.Generator
	logger        *slog.Logger
}

// NewReportHandler creates a new ReportHandler.
func NewReportHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher) *ReportHandler {
	logger := slog.Default().With("handler", "reports")
	return &ReportHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
		generator:     reports.NewGenerator(queries, logger),
		logger:        logger,
	}
}

func (h *ReportHandler) auditLogGlobal(c *fiber.Ctx, resourceType, resourceID, action string) {
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, resourceType, resourceID, action, nil)
}

// --- Response types ---

type reportScheduleResponse struct {
	ID              uuid.UUID       `json:"id"`
	Name            string          `json:"name"`
	ReportType      string          `json:"report_type"`
	ClusterID       uuid.UUID       `json:"cluster_id"`
	TimeRangeHours  int32           `json:"time_range_hours"`
	Schedule        string          `json:"schedule"`
	Format          string          `json:"format"`
	EmailEnabled    bool            `json:"email_enabled"`
	EmailChannelID  *uuid.UUID      `json:"email_channel_id,omitempty"`
	EmailRecipients []string        `json:"email_recipients"`
	Parameters      json.RawMessage `json:"parameters"`
	Enabled         bool            `json:"enabled"`
	LastRunAt       *string         `json:"last_run_at,omitempty"`
	NextRunAt       *string         `json:"next_run_at,omitempty"`
	CreatedBy       uuid.UUID       `json:"created_by"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

type reportRunResponse struct {
	ID             uuid.UUID  `json:"id"`
	ScheduleID     *uuid.UUID `json:"schedule_id,omitempty"`
	ReportType     string     `json:"report_type"`
	ClusterID      uuid.UUID  `json:"cluster_id"`
	Status         string     `json:"status"`
	TimeRangeHours int32      `json:"time_range_hours"`
	ErrorMessage   string     `json:"error_message,omitempty"`
	CreatedBy      uuid.UUID  `json:"created_by"`
	StartedAt      *string    `json:"started_at,omitempty"`
	CompletedAt    *string    `json:"completed_at,omitempty"`
	CreatedAt      string     `json:"created_at"`
}

func toReportScheduleResponse(s db.ReportSchedule) reportScheduleResponse {
	r := reportScheduleResponse{
		ID:              s.ID,
		Name:            s.Name,
		ReportType:      s.ReportType,
		ClusterID:       s.ClusterID,
		TimeRangeHours:  s.TimeRangeHours,
		Schedule:        s.Schedule,
		Format:          s.Format,
		EmailEnabled:    s.EmailEnabled,
		EmailRecipients: s.EmailRecipients,
		Parameters:      s.Parameters,
		Enabled:         s.Enabled,
		CreatedBy:       s.CreatedBy,
		CreatedAt:       s.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       s.UpdatedAt.Format(time.RFC3339),
	}
	if s.EmailChannelID.Valid {
		id, _ := uuid.FromBytes(s.EmailChannelID.Bytes[:])
		r.EmailChannelID = &id
	}
	if s.LastRunAt.Valid {
		t := s.LastRunAt.Time.Format(time.RFC3339)
		r.LastRunAt = &t
	}
	if s.NextRunAt.Valid {
		t := s.NextRunAt.Time.Format(time.RFC3339)
		r.NextRunAt = &t
	}
	if r.EmailRecipients == nil {
		r.EmailRecipients = []string{}
	}
	return r
}

func toRunResponse(r db.ReportRun) reportRunResponse {
	resp := reportRunResponse{
		ID:             r.ID,
		ReportType:     r.ReportType,
		ClusterID:      r.ClusterID,
		Status:         r.Status,
		TimeRangeHours: r.TimeRangeHours,
		ErrorMessage:   r.ErrorMessage,
		CreatedBy:      r.CreatedBy,
		CreatedAt:      r.CreatedAt.Format(time.RFC3339),
	}
	if r.ScheduleID.Valid {
		id, _ := uuid.FromBytes(r.ScheduleID.Bytes[:])
		resp.ScheduleID = &id
	}
	if r.StartedAt.Valid {
		t := r.StartedAt.Time.Format(time.RFC3339)
		resp.StartedAt = &t
	}
	if r.CompletedAt.Valid {
		t := r.CompletedAt.Time.Format(time.RFC3339)
		resp.CompletedAt = &t
	}
	return resp
}

// --- Schedule CRUD ---

// ListSchedules handles GET /api/v1/reports/schedules
func (h *ReportHandler) ListSchedules(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "report"); err != nil {
		return err
	}

	schedules, err := h.queries.ListReportSchedules(c.Context(), db.ListReportSchedulesParams{
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list schedules")
	}

	out := make([]reportScheduleResponse, len(schedules))
	for i, s := range schedules {
		out[i] = toReportScheduleResponse(s)
	}
	return c.JSON(out)
}

// CreateSchedule handles POST /api/v1/reports/schedules
func (h *ReportHandler) CreateSchedule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "report"); err != nil {
		return err
	}

	var req struct {
		Name            string          `json:"name"`
		ReportType      string          `json:"report_type"`
		ClusterID       string          `json:"cluster_id"`
		TimeRangeHours  int             `json:"time_range_hours"`
		Schedule        string          `json:"schedule"`
		Format          string          `json:"format"`
		EmailEnabled    bool            `json:"email_enabled"`
		EmailChannelID  *string         `json:"email_channel_id"`
		EmailRecipients []string        `json:"email_recipients"`
		Parameters      json.RawMessage `json:"parameters"`
		Enabled         *bool           `json:"enabled"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if err := h.validateScheduleRequest(c, req.Name, req.ReportType, req.ClusterID, req.TimeRangeHours, req.Schedule, req.Format, req.EmailEnabled, req.EmailChannelID, req.EmailRecipients, req.Parameters); err != nil {
		return err
	}

	clusterID, _ := uuid.Parse(req.ClusterID)
	userID, _ := c.Locals("user_id").(uuid.UUID)

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	if req.Format == "" {
		req.Format = "html"
	}
	if req.TimeRangeHours == 0 {
		req.TimeRangeHours = 168
	}
	if req.Parameters == nil {
		req.Parameters = json.RawMessage(`{}`)
	}
	if req.EmailRecipients == nil {
		req.EmailRecipients = []string{}
	}

	var emailChannelID pgtype.UUID
	if req.EmailChannelID != nil && *req.EmailChannelID != "" {
		id, err := uuid.Parse(*req.EmailChannelID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid email_channel_id")
		}
		emailChannelID = pgtype.UUID{Bytes: id, Valid: true}
	}

	var nextRunAt pgtype.Timestamptz
	if req.Schedule != "" && enabled {
		next, err := scheduler.NextRunTime(req.Schedule, time.Now())
		if err == nil {
			nextRunAt = pgtype.Timestamptz{Time: next, Valid: true}
		}
	}

	schedule, err := h.queries.InsertReportSchedule(c.Context(), db.InsertReportScheduleParams{
		Name:            req.Name,
		ReportType:      req.ReportType,
		ClusterID:       clusterID,
		TimeRangeHours:  safeInt32(req.TimeRangeHours),
		Schedule:        req.Schedule,
		Format:          req.Format,
		EmailEnabled:    req.EmailEnabled,
		EmailChannelID:  emailChannelID,
		EmailRecipients: req.EmailRecipients,
		Parameters:      req.Parameters,
		Enabled:         enabled,
		NextRunAt:       nextRunAt,
		CreatedBy:       userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create schedule")
	}

	h.auditLogGlobal(c, "report_schedule", schedule.ID.String(), "created")
	return c.Status(fiber.StatusCreated).JSON(toReportScheduleResponse(schedule))
}

// GetSchedule handles GET /api/v1/reports/schedules/:id
func (h *ReportHandler) GetSchedule(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "report"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid schedule ID")
	}

	schedule, err := h.queries.GetReportSchedule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}

	return c.JSON(toReportScheduleResponse(schedule))
}

// UpdateSchedule handles PUT /api/v1/reports/schedules/:id
func (h *ReportHandler) UpdateSchedule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "report"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid schedule ID")
	}

	existing, err := h.queries.GetReportSchedule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}

	var req struct {
		Name            *string          `json:"name"`
		ReportType      *string          `json:"report_type"`
		ClusterID       *string          `json:"cluster_id"`
		TimeRangeHours  *int             `json:"time_range_hours"`
		Schedule        *string          `json:"schedule"`
		Format          *string          `json:"format"`
		EmailEnabled    *bool            `json:"email_enabled"`
		EmailChannelID  *string          `json:"email_channel_id"`
		EmailRecipients []string         `json:"email_recipients"`
		Parameters      json.RawMessage  `json:"parameters"`
		Enabled         *bool            `json:"enabled"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Apply defaults from existing record.
	name := existing.Name
	if req.Name != nil { name = *req.Name }
	reportType := existing.ReportType
	if req.ReportType != nil { reportType = *req.ReportType }
	clusterID := existing.ClusterID
	if req.ClusterID != nil {
		cid, err := uuid.Parse(*req.ClusterID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
		}
		clusterID = cid
	}
	timeRangeHours := int(existing.TimeRangeHours)
	if req.TimeRangeHours != nil { timeRangeHours = *req.TimeRangeHours }
	scheduleStr := existing.Schedule
	if req.Schedule != nil { scheduleStr = *req.Schedule }
	format := existing.Format
	if req.Format != nil { format = *req.Format }
	emailEnabled := existing.EmailEnabled
	if req.EmailEnabled != nil { emailEnabled = *req.EmailEnabled }
	emailRecipients := existing.EmailRecipients
	if req.EmailRecipients != nil { emailRecipients = req.EmailRecipients }
	parameters := existing.Parameters
	if req.Parameters != nil { parameters = req.Parameters }
	enabled := existing.Enabled
	if req.Enabled != nil { enabled = *req.Enabled }

	emailChannelID := existing.EmailChannelID
	if req.EmailChannelID != nil {
		if *req.EmailChannelID == "" {
			emailChannelID = pgtype.UUID{}
		} else {
			eid, err := uuid.Parse(*req.EmailChannelID)
			if err != nil {
				return fiber.NewError(fiber.StatusBadRequest, "Invalid email_channel_id")
			}
			emailChannelID = pgtype.UUID{Bytes: eid, Valid: true}
		}
	}

	if err := h.validateScheduleRequest(c, name, reportType, clusterID.String(), timeRangeHours, scheduleStr, format, emailEnabled, nil, emailRecipients, parameters); err != nil {
		return err
	}

	var nextRunAt pgtype.Timestamptz
	if scheduleStr != "" && enabled {
		next, err := scheduler.NextRunTime(scheduleStr, time.Now())
		if err == nil {
			nextRunAt = pgtype.Timestamptz{Time: next, Valid: true}
		}
	}

	updated, err := h.queries.UpdateReportSchedule(c.Context(), db.UpdateReportScheduleParams{
		ID:              id,
		Name:            name,
		ReportType:      reportType,
		ClusterID:       clusterID,
		TimeRangeHours:  safeInt32(timeRangeHours),
		Schedule:        scheduleStr,
		Format:          format,
		EmailEnabled:    emailEnabled,
		EmailChannelID:  emailChannelID,
		EmailRecipients: emailRecipients,
		Parameters:      parameters,
		Enabled:         enabled,
		NextRunAt:       nextRunAt,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update schedule")
	}

	h.auditLogGlobal(c, "report_schedule", id.String(), "updated")
	return c.JSON(toReportScheduleResponse(updated))
}

// DeleteSchedule handles DELETE /api/v1/reports/schedules/:id
func (h *ReportHandler) DeleteSchedule(c *fiber.Ctx) error {
	if err := requirePerm(c, "manage", "report"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid schedule ID")
	}

	if _, err := h.queries.GetReportSchedule(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}

	if err := h.queries.DeleteReportSchedule(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete schedule")
	}

	h.auditLogGlobal(c, "report_schedule", id.String(), "deleted")
	return c.SendStatus(fiber.StatusNoContent)
}

// --- Report Generation ---

// GenerateReport handles POST /api/v1/reports/generate
func (h *ReportHandler) GenerateReport(c *fiber.Ctx) error {
	if err := requirePerm(c, "generate", "report"); err != nil {
		return err
	}

	var req struct {
		ReportType     string `json:"report_type"`
		ClusterID      string `json:"cluster_id"`
		TimeRangeHours int    `json:"time_range_hours"`
		Format         string `json:"format"`
	}
	if err := c.BodyParser(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	if !reports.ValidReportType(req.ReportType) {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid report_type")
	}
	clusterID, err := uuid.Parse(req.ClusterID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
	}
	if req.TimeRangeHours <= 0 {
		req.TimeRangeHours = 168
	}
	if req.TimeRangeHours > 8760 {
		return fiber.NewError(fiber.StatusBadRequest, "time_range_hours must be between 1 and 8760")
	}
	if req.Format == "" {
		req.Format = "html"
	}
	if req.Format != "html" && req.Format != "csv" {
		return fiber.NewError(fiber.StatusBadRequest, "format must be 'html' or 'csv'")
	}

	// Limit concurrent report generations.
	select {
	case reportSemaphore <- struct{}{}:
		defer func() { <-reportSemaphore }()
	default:
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many concurrent report generations, try again later")
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	run, err := h.queries.InsertReportRun(c.Context(), db.InsertReportRunParams{
		ReportType:     req.ReportType,
		ClusterID:      clusterID,
		Status:         "running",
		TimeRangeHours: safeInt32(req.TimeRangeHours),
		CreatedBy:      userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create report run")
	}

	_ = h.queries.UpdateReportRunStarted(c.Context(), run.ID)

	data, err := h.generator.Generate(c.Context(), req.ReportType, clusterID, req.TimeRangeHours)
	if err != nil {
		_ = h.queries.UpdateReportRunFailed(c.Context(), db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: err.Error(),
		})
		h.logger.Error("report generation failed", "error", err, "cluster_id", clusterID, "type", req.ReportType)
		return fiber.NewError(fiber.StatusInternalServerError, "Report generation failed")
	}

	htmlOutput, err := reports.RenderHTML(data)
	if err != nil {
		_ = h.queries.UpdateReportRunFailed(c.Context(), db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: fmt.Sprintf("render HTML: %v", err),
		})
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to render report")
	}

	csvOutput, err := reports.RenderCSV(data)
	if err != nil {
		_ = h.queries.UpdateReportRunFailed(c.Context(), db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: fmt.Sprintf("render CSV: %v", err),
		})
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to render report")
	}

	dataJSON, _ := json.Marshal(data)

	if err := h.queries.UpdateReportRunCompleted(c.Context(), db.UpdateReportRunCompletedParams{
		ID:         run.ID,
		ReportData: dataJSON,
		ReportHtml: pgtype.Text{String: htmlOutput, Valid: true},
		ReportCsv:  pgtype.Text{String: csvOutput, Valid: true},
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to save report")
	}

	// Refresh run for response.
	completed, err := h.queries.GetReportRun(c.Context(), run.ID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get report run")
	}

	if h.eventPub != nil {
		h.eventPub.SystemEvent(c.Context(), events.KindReportGenerated, "completed")
	}

	h.auditLogGlobal(c, "report", run.ID.String(), "generated")
	return c.Status(fiber.StatusCreated).JSON(toRunResponse(completed))
}

// --- Report Runs ---

// ListRuns handles GET /api/v1/reports/runs
func (h *ReportHandler) ListRuns(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "report"); err != nil {
		return err
	}

	runs, err := h.queries.ListReportRuns(c.Context(), db.ListReportRunsParams{
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list runs")
	}

	out := make([]reportRunResponse, len(runs))
	for i, r := range runs {
		out[i] = toRunResponse(r)
	}
	return c.JSON(out)
}

// GetRun handles GET /api/v1/reports/runs/:id
func (h *ReportHandler) GetRun(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "report"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid run ID")
	}

	run, err := h.queries.GetReportRun(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Run not found")
	}

	return c.JSON(toRunResponse(run))
}

// GetRunHTML handles GET /api/v1/reports/runs/:id/html
func (h *ReportHandler) GetRunHTML(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "report"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid run ID")
	}

	row, err := h.queries.GetReportRunHTML(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Run not found")
	}

	if !row.ReportHtml.Valid || row.ReportHtml.String == "" {
		return fiber.NewError(fiber.StatusNotFound, "HTML report not available")
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	c.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	c.Set("X-Content-Type-Options", "nosniff")
	return c.SendString(row.ReportHtml.String)
}

// GetRunCSV handles GET /api/v1/reports/runs/:id/csv
func (h *ReportHandler) GetRunCSV(c *fiber.Ctx) error {
	if err := requirePerm(c, "view", "report"); err != nil {
		return err
	}

	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid run ID")
	}

	row, err := h.queries.GetReportRunCSV(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Run not found")
	}

	if !row.ReportCsv.Valid || row.ReportCsv.String == "" {
		return fiber.NewError(fiber.StatusNotFound, "CSV report not available")
	}

	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=report-%s.csv", id.String()))
	return c.SendString(row.ReportCsv.String)
}

// --- Validation ---

func (h *ReportHandler) validateScheduleRequest(c *fiber.Ctx, name, reportType, clusterID string, timeRangeHours int, schedule, format string, emailEnabled bool, emailChannelID *string, emailRecipients []string, parameters json.RawMessage) error {
	if name == "" || len(name) > 200 {
		return fiber.NewError(fiber.StatusBadRequest, "name must be 1-200 characters")
	}
	if !reports.ValidReportType(reportType) {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid report_type")
	}
	if _, err := uuid.Parse(clusterID); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
	}
	if timeRangeHours < 1 || timeRangeHours > 8760 {
		return fiber.NewError(fiber.StatusBadRequest, "time_range_hours must be between 1 and 8760")
	}
	if schedule != "" {
		if err := scheduler.ValidateCron(schedule); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid schedule: %v", err))
		}
	}
	if format != "" && format != "html" && format != "csv" {
		return fiber.NewError(fiber.StatusBadRequest, "format must be 'html' or 'csv'")
	}
	if emailEnabled && emailChannelID != nil && *emailChannelID != "" {
		id, err := uuid.Parse(*emailChannelID)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid email_channel_id")
		}
		ch, err := h.queries.GetNotificationChannel(c.Context(), id)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Email channel not found")
		}
		if ch.ChannelType != "email" {
			return fiber.NewError(fiber.StatusBadRequest, "Channel must be of type 'email'")
		}
	}
	if len(emailRecipients) > 50 {
		return fiber.NewError(fiber.StatusBadRequest, "email_recipients limited to 50 addresses")
	}
	for _, addr := range emailRecipients {
		if !emailRegex.MatchString(addr) {
			return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid email recipient: %s", addr))
		}
	}
	if len(parameters) > 65536 {
		return fiber.NewError(fiber.StatusBadRequest, "parameters must be under 64KB")
	}
	return nil
}
