package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/cronspec"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/reports"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// reportSemaphore limits concurrent report generations.
var reportSemaphore = make(chan struct{}, 3)

// EmailAddressPattern is the recipient rule these endpoints have always
// enforced, exported so the declarations in internal/api/registry_reports.go
// state it as the parameter's Pattern instead of keeping a second copy that
// drifts.
//
// It is NOT apischema's registered "email" format, and the difference runs
// in BOTH directions, which is why swapping to the format would have been a
// behaviour change rather than a tidy-up. The format is built on
// mail.ParseAddress and additionally refuses a quoted or dot-irregular local
// part, so it rejects "a..b@example.com", which this accepts; and it accepts
// an angle-bracketed address, a one-character TLD and an IP-literal domain,
// all of which this rejects. It also NORMALISES what it validates, which
// would rewrite a stored recipient list. The migration keeps the rule that
// was here; picking one of the two for the whole API is a separate decision.
const EmailAddressPattern = `^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`

// MaxEmailRecipients is the recipient cap both bodies enforced, exported for
// the same reason as the pattern above.
const MaxEmailRecipients = 50

var emailRegex = regexp.MustCompile(EmailAddressPattern)

// ReportHandler handles report schedules, generation, and run history.
type ReportHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
	generator     *reports.Generator
	logger        *slog.Logger
}

// NewReportHandler creates a new ReportHandler. generator comes from the
// composition root, so an on-demand report renders through the same instance
// as a scheduled one.
func NewReportHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher, generator *reports.Generator) *ReportHandler {
	return &ReportHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
		generator:     generator,
		logger:        slog.Default().With("handler", "reports"),
	}
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
	// RunAs is the user whose grants each scheduled run reads under: the
	// last person to save the schedule.
	RunAs     uuid.UUID `json:"run_as"`
	CreatedAt string    `json:"created_at"`
	UpdatedAt string    `json:"updated_at"`
}

type reportRunResponse struct {
	ID             uuid.UUID       `json:"id"`
	ScheduleID     *uuid.UUID      `json:"schedule_id,omitempty"`
	ReportType     string          `json:"report_type"`
	ClusterID      uuid.UUID       `json:"cluster_id"`
	Status         string          `json:"status"`
	TimeRangeHours int32           `json:"time_range_hours"`
	Parameters     json.RawMessage `json:"parameters"`
	ErrorMessage   string          `json:"error_message,omitempty"`
	CreatedBy      uuid.UUID       `json:"created_by"`
	StartedAt      *string         `json:"started_at,omitempty"`
	CompletedAt    *string         `json:"completed_at,omitempty"`
	CreatedAt      string          `json:"created_at"`
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
		RunAs:           s.RunAs,
		CreatedAt:       s.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:       s.UpdatedAt.Format(time.RFC3339Nano),
	}
	if s.EmailChannelID.Valid {
		id, _ := uuid.FromBytes(s.EmailChannelID.Bytes[:])
		r.EmailChannelID = &id
	}
	if s.LastRunAt.Valid {
		t := s.LastRunAt.Time.Format(time.RFC3339Nano)
		r.LastRunAt = &t
	}
	if s.NextRunAt.Valid {
		t := s.NextRunAt.Time.Format(time.RFC3339Nano)
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
		Parameters:     r.Parameters,
		ErrorMessage:   r.ErrorMessage,
		CreatedBy:      r.CreatedBy,
		CreatedAt:      r.CreatedAt.Format(time.RFC3339Nano),
	}
	if len(resp.Parameters) == 0 {
		resp.Parameters = json.RawMessage(`{}`)
	}
	if r.ScheduleID.Valid {
		id, _ := uuid.FromBytes(r.ScheduleID.Bytes[:])
		resp.ScheduleID = &id
	}
	if r.StartedAt.Valid {
		t := r.StartedAt.Time.Format(time.RFC3339Nano)
		resp.StartedAt = &t
	}
	if r.CompletedAt.Valid {
		t := r.CompletedAt.Time.Format(time.RFC3339Nano)
		resp.CompletedAt = &t
	}
	return resp
}

// --- Schedule CRUD ---

// ListSchedules handles GET /api/v1/reports/schedules
func (h *ReportHandler) ListSchedules(c fiber.Ctx, _ *apischema.Params) error {
	access, err := accessibleClusters(c, "view", "report")
	if err != nil {
		return err
	}

	// Scoped in SQL, not after the fetch: the 100-row cap is applied first, so
	// an unscoped fetch silently hides a scoped caller's schedules once the
	// install has more than 100 of them, with nothing in the response marking
	// the result incomplete.
	scope, query := clusterScopeFilter(access)
	if !query {
		return RespondItems(c, []reportScheduleResponse{})
	}

	schedules, err := h.queries.ListReportSchedules(c.Context(), db.ListReportSchedulesParams{
		Limit:                100,
		Offset:               0,
		AccessibleClusterIds: scope,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list schedules")
	}

	out := make([]reportScheduleResponse, 0, len(schedules))
	for _, s := range schedules {
		// Defense-in-depth, as in the audit and alert listings.
		if !access.PermitsCluster(s.ClusterID) {
			continue
		}
		out = append(out, toReportScheduleResponse(s))
	}
	return RespondItems(c, out)
}

// CreateSchedule handles POST /api/v1/reports/schedules
func (h *ReportHandler) CreateSchedule(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("report_cluster_id"))
	if err != nil {
		return err
	}
	fields := scheduleFields{
		Name:            p.String("name"),
		ReportType:      p.String("report_type"),
		ClusterID:       clusterID,
		TimeRangeHours:  int(p.Int("time_range_hours")),
		Schedule:        p.String("schedule"),
		Format:          p.String("format"),
		EmailEnabled:    p.Bool("email_enabled"),
		EmailChannelID:  p.String("email_channel_id"),
		EmailRecipients: p.Strings("email_recipients"),
	}
	rawParams, err := rawReportParams(p)
	if err != nil {
		return err
	}
	fields.Parameters = rawParams

	// Authorize BEFORE validating. validateScheduleFields reads the
	// notification channel out of the database and distinguishes "not
	// found" from "not an email channel", so running it first answered
	// that question for any authenticated caller holding no report grant
	// at all — an existence oracle over a table otherwise gated on
	// view:notification_channel. This route is Deferred, so no middleware
	// stands in front of the handler and the order here IS the gate.
	// EmailRun (below) already had it this way round.
	if err := requireClusterPerm(c, "manage", "report", clusterID); err != nil {
		return err
	}

	if err := h.validateScheduleFields(c, fields); err != nil {
		return err
	}
	userID, _ := c.Locals("user_id").(uuid.UUID)

	enabled := true
	if v, supplied := p.OptBool("enabled"); supplied {
		enabled = v
	}
	format := fields.Format
	if format == "" {
		format = "html"
	}
	// Stored in canonical form: only the keys the validator saw.
	_, paramsJSON, pErr := reports.NormalizeParams(fields.Parameters)
	if pErr != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid parameters: "+pErr.Error())
	}
	recipients := fields.EmailRecipients
	if recipients == nil {
		recipients = []string{}
	}

	var emailChannelID pgtype.UUID
	if fields.EmailChannelID != "" {
		id, parseErr := parseParamUUID(fields.EmailChannelID)
		if parseErr != nil {
			return parseErr
		}
		emailChannelID = pgtype.UUID{Bytes: id, Valid: true}
	}

	var nextRunAt pgtype.Timestamptz
	if fields.Schedule != "" && enabled {
		next, nextErr := cronspec.NextRunTime(fields.Schedule, time.Now())
		if nextErr == nil {
			nextRunAt = pgtype.Timestamptz{Time: next, Valid: true}
		}
	}

	schedule, err := h.queries.InsertReportSchedule(c.Context(), db.InsertReportScheduleParams{
		Name:            fields.Name,
		ReportType:      fields.ReportType,
		ClusterID:       clusterID,
		TimeRangeHours:  safeconv.Int32(fields.TimeRangeHours),
		Schedule:        fields.Schedule,
		Format:          format,
		EmailEnabled:    fields.EmailEnabled,
		EmailChannelID:  emailChannelID,
		EmailRecipients: recipients,
		Parameters:      paramsJSON,
		Enabled:         enabled,
		NextRunAt:       nextRunAt,
		CreatedBy:       userID,
		RunAs:           userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create schedule")
	}

	// report_schedules.cluster_id is NOT NULL, so the schedule always belongs to
	// one cluster and the audit row must say which. A cluster-less audit row is
	// readable only with global view:audit (a NULL cluster_id marks a global
	// entry, and the scoped reads exclude it), which would hide the schedule
	// from the very operators who can see and manage it.
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(schedule.ClusterID), "report_schedule", schedule.ID.String(), "created", nil)
	return c.Status(fiber.StatusCreated).JSON(toReportScheduleResponse(schedule))
}

// GetSchedule handles GET /api/v1/reports/schedules/:id
func (h *ReportHandler) GetSchedule(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	schedule, err := h.queries.GetReportSchedule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}

	if err := requireClusterPerm(c, "view", "report", schedule.ClusterID); err != nil {
		return err
	}

	return c.JSON(toReportScheduleResponse(schedule))
}

// UpdateSchedule handles PUT /api/v1/reports/schedules/:id
func (h *ReportHandler) UpdateSchedule(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	existing, err := h.queries.GetReportSchedule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}

	// Caller must hold manage:report on the schedule's current cluster.
	if err := requireClusterPerm(c, "manage", "report", existing.ClusterID); err != nil {
		return err
	}

	// Apply defaults from existing record. Each read is an Opt accessor
	// because "the caller did not mention this field" has to stay distinct
	// from "the caller sent the zero value" — the enable/disable toggle PUTs
	// only `enabled`, and a plain read would rewrite every other column with
	// an empty string.
	name := existing.Name
	if v, supplied := p.OptString("name"); supplied {
		name = v
	}
	reportType := existing.ReportType
	if v, supplied := p.OptString("report_type"); supplied {
		reportType = v
	}
	clusterID := existing.ClusterID
	if v, supplied := p.OptString("report_cluster_id"); supplied {
		cid, parseErr := parseParamUUID(v)
		if parseErr != nil {
			return parseErr
		}
		// If the user is moving the schedule to a different cluster, gate on
		// manage:report on the target cluster too.
		if cid != existing.ClusterID {
			if err := requireClusterPerm(c, "manage", "report", cid); err != nil {
				return err
			}
		}
		clusterID = cid
	}
	timeRangeHours := int(existing.TimeRangeHours)
	if v, supplied := p.OptInt("time_range_hours"); supplied {
		timeRangeHours = int(v)
	}
	scheduleStr := existing.Schedule
	suppliedSchedule := ""
	if v, supplied := p.OptString("schedule"); supplied {
		scheduleStr = v
		suppliedSchedule = v
	}
	format := existing.Format
	if v, supplied := p.OptString("format"); supplied {
		format = v
	}
	emailEnabled := existing.EmailEnabled
	if v, supplied := p.OptBool("email_enabled"); supplied {
		emailEnabled = v
	}
	emailRecipients := existing.EmailRecipients
	if p.Has("email_recipients") {
		emailRecipients = p.Strings("email_recipients")
	}
	parameters := existing.Parameters
	if p.Has("parameters") {
		raw, rawErr := rawReportParams(p)
		if rawErr != nil {
			return rawErr
		}
		_, canonical, pErr := reports.NormalizeParams(raw)
		if pErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid parameters: "+pErr.Error())
		}
		parameters = canonical
	}
	enabled := existing.Enabled
	if v, supplied := p.OptBool("enabled"); supplied {
		enabled = v
	}

	emailChannelID := existing.EmailChannelID
	if v, supplied := p.OptString("email_channel_id"); supplied {
		if v == "" {
			emailChannelID = pgtype.UUID{}
		} else {
			eid, parseErr := parseParamUUID(v)
			if parseErr != nil {
				return parseErr
			}
			emailChannelID = pgtype.UUID{Bytes: eid, Valid: true}
		}
	}

	// The cron validated is the one the CALLER supplied, not the effective
	// one. An update that leaves the schedule alone — the enable/disable
	// toggle sends only `enabled` — must not be rejected because of an
	// expression already in the row. A stored expression that can never fire
	// predates this validation, and disabling it is precisely the action an
	// operator needs to reach. It stays inert either way: the scheduler
	// writes next_run_at NULL, which this table's due predicate never
	// matches.
	//
	// EmailChannelID is deliberately left empty here rather than passed
	// through. The channel lookup is a create-time check and always has been:
	// CreateSchedule passed the caller's value and UpdateSchedule passed nil,
	// so an update has never re-validated the stored channel. Passing it now
	// would start rejecting a save on a channel that was deleted or retyped
	// since — which is exactly the save an operator makes to fix it.
	if err := h.validateScheduleFields(c, scheduleFields{
		Name:            name,
		ReportType:      reportType,
		ClusterID:       clusterID,
		TimeRangeHours:  timeRangeHours,
		Schedule:        suppliedSchedule,
		Format:          format,
		EmailEnabled:    emailEnabled,
		EmailRecipients: emailRecipients,
		Parameters:      parameters,
	}); err != nil {
		return err
	}

	var nextRunAt pgtype.Timestamptz
	if scheduleStr != "" && enabled {
		next, err := cronspec.NextRunTime(scheduleStr, time.Now())
		if err == nil {
			nextRunAt = pgtype.Timestamptz{Time: next, Valid: true}
		}
	}

	// Whoever saves the schedule becomes the user its runs read under. Any
	// manage:report holder may retarget a schedule to a report type or a
	// cluster; without this stamp the run would keep reading under the
	// original creator's grants, which may reach further than the editor's.
	editorID, _ := c.Locals("user_id").(uuid.UUID)
	if editorID == uuid.Nil {
		return fiber.NewError(fiber.StatusInternalServerError, "No user in request context")
	}

	updated, err := h.queries.UpdateReportSchedule(c.Context(), db.UpdateReportScheduleParams{
		ID:              id,
		Name:            name,
		ReportType:      reportType,
		ClusterID:       clusterID,
		TimeRangeHours:  safeconv.Int32(timeRangeHours),
		Schedule:        scheduleStr,
		Format:          format,
		EmailEnabled:    emailEnabled,
		EmailChannelID:  emailChannelID,
		EmailRecipients: emailRecipients,
		Parameters:      parameters,
		Enabled:         enabled,
		NextRunAt:       nextRunAt,
		RunAs:           editorID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update schedule")
	}

	// Attribute to the cluster the schedule now belongs to, so a reassignment
	// lands in the new cluster's audit log.
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(updated.ClusterID), "report_schedule", id.String(), "updated", nil)
	return c.JSON(toReportScheduleResponse(updated))
}

// DeleteSchedule handles DELETE /api/v1/reports/schedules/:id
func (h *ReportHandler) DeleteSchedule(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	existing, err := h.queries.GetReportSchedule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Schedule not found")
	}

	if err := requireClusterPerm(c, "manage", "report", existing.ClusterID); err != nil {
		return err
	}

	if err := h.queries.DeleteReportSchedule(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete schedule")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(existing.ClusterID), "report_schedule", id.String(), "deleted", nil)
	return c.SendStatus(fiber.StatusNoContent)
}

// --- Report Generation ---

// GenerateReport handles POST /api/v1/reports/generate
func (h *ReportHandler) GenerateReport(c fiber.Ctx, p *apischema.Params) error {
	reportType := p.String("report_type")
	rawParams, err := rawReportParams(p)
	if err != nil {
		return err
	}
	params, paramsJSON, err := reports.NormalizeParams(rawParams)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid parameters: "+err.Error())
	}
	clusterID, err := parseParamUUID(p.String("report_cluster_id"))
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "generate", "report", clusterID); err != nil {
		return err
	}
	timeRangeHours := int(p.Int("time_range_hours"))

	// Limit concurrent report generations.
	select {
	case reportSemaphore <- struct{}{}:
		defer func() { <-reportSemaphore }()
	default:
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many concurrent report generations, try again later")
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	run, err := h.queries.InsertReportRun(c.Context(), db.InsertReportRunParams{
		ReportType:     reportType,
		ClusterID:      clusterID,
		Status:         "running",
		TimeRangeHours: safeconv.Int32(timeRangeHours),
		Parameters:     paramsJSON,
		CreatedBy:      userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create report run")
	}

	_ = h.queries.UpdateReportRunStarted(c.Context(), run.ID)

	// The requester's own grants decide what the report may read (Veeam
	// data in particular); the stored run is then readable under view:report.
	data, err := h.generator.Generate(c.Context(), reports.Request{
		Type:           reportType,
		ClusterID:      clusterID,
		TimeRangeHours: timeRangeHours,
		Params:         params,
		RequestedBy:    userID,
	})
	if err != nil {
		_ = h.queries.UpdateReportRunFailed(c.Context(), db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: err.Error(),
		})
		h.logger.Error("report generation failed", "error", err, "cluster_id", clusterID, "type", reportType)
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

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(run.ClusterID), "report", run.ID.String(), "generated", nil)
	return c.Status(fiber.StatusCreated).JSON(toRunResponse(completed))
}

// --- Report Runs ---

// ListRuns handles GET /api/v1/reports/runs
func (h *ReportHandler) ListRuns(c fiber.Ctx, _ *apischema.Params) error {
	access, err := accessibleClusters(c, "view", "report")
	if err != nil {
		return err
	}

	// Scoped in SQL for the same reason as ListSchedules — the cap runs before
	// the trim, so run history is the first thing to disappear on a busy
	// install.
	scope, query := clusterScopeFilter(access)
	if !query {
		return RespondItems(c, []reportRunResponse{})
	}

	runs, err := h.queries.ListReportRuns(c.Context(), db.ListReportRunsParams{
		Limit:                100,
		Offset:               0,
		AccessibleClusterIds: scope,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list runs")
	}

	out := make([]reportRunResponse, 0, len(runs))
	for _, r := range runs {
		// Defense-in-depth, as in the audit and alert listings.
		if !access.PermitsCluster(r.ClusterID) {
			continue
		}
		out = append(out, toRunResponse(r))
	}
	return RespondItems(c, out)
}

// GetRun handles GET /api/v1/reports/runs/:id
func (h *ReportHandler) GetRun(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	run, err := h.queries.GetReportRun(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Run not found")
	}

	if err := requireClusterPerm(c, "view", "report", run.ClusterID); err != nil {
		return err
	}

	return c.JSON(toRunResponse(run))
}

// GetRunHTML handles GET /api/v1/reports/runs/:id/html
func (h *ReportHandler) GetRunHTML(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	row, err := h.queries.GetReportRunHTML(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Run not found")
	}

	if err := requireClusterPerm(c, "view", "report", row.ClusterID); err != nil {
		return err
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
func (h *ReportHandler) GetRunCSV(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	row, err := h.queries.GetReportRunCSV(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Run not found")
	}

	if err := requireClusterPerm(c, "view", "report", row.ClusterID); err != nil {
		return err
	}

	if !row.ReportCsv.Valid || row.ReportCsv.String == "" {
		return fiber.NewError(fiber.StatusNotFound, "CSV report not available")
	}

	c.Set("Content-Type", "text/csv; charset=utf-8")
	c.Set("Content-Disposition", fmt.Sprintf("attachment; filename=report-%s.csv", id.String()))
	return c.SendString(row.ReportCsv.String)
}

// --- Validation ---

// scheduleFields is the EFFECTIVE schedule a create or an update would
// store: on create it is what the caller sent, and on update it is the
// stored row with the caller's changes applied.
//
// It replaces the twelve positional arguments validateScheduleRequest used
// to take. Six of them were strings, three of them adjacent, so a
// transposition compiled and validated the wrong field against the wrong
// rule — and the one that mattered, emailChannelID, was passed nil from one
// caller and a pointer from the other with nothing at the call site saying
// why.
type scheduleFields struct {
	Name           string
	ReportType     string
	ClusterID      uuid.UUID
	TimeRangeHours int
	Schedule       string
	Format         string
	EmailEnabled   bool
	// EmailChannelID is checked against the notification_channels table, and
	// only when EmailEnabled. Empty means "do not look it up", which is what
	// UpdateSchedule has always passed — see the note at its call site.
	EmailChannelID  string
	EmailRecipients []string
	Parameters      json.RawMessage
}

// validateScheduleFields is what survives of the old validator once the
// declarations state the rest.
//
// The name length, the report-type vocabulary, the time range, the format
// enum, the recipient cap and the address rule, and the cluster's uuid shape
// are all in the parameter schema now — but they stay HERE as well, and that
// is the point rather than duplication. The schema validates what the CALLER
// sent; on an update the effective value may come from the stored row
// instead, and a row written before a rule existed has to be caught on the
// way back out. Only the two checks a parameter schema cannot make are
// unconditionally handler-side: the cron expression (a Go function over a
// clock) and the notification channel (a DB lookup).
func (h *ReportHandler) validateScheduleFields(c fiber.Ctx, f scheduleFields) error {
	if f.Name == "" || len(f.Name) > 200 {
		return fiber.NewError(fiber.StatusBadRequest, "name must be 1-200 characters")
	}
	if !reports.ValidReportType(f.ReportType) {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid report_type")
	}
	if f.ClusterID == uuid.Nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
	}
	if f.TimeRangeHours < 1 || f.TimeRangeHours > 8760 {
		return fiber.NewError(fiber.StatusBadRequest, "time_range_hours must be between 1 and 8760")
	}
	if f.Schedule != "" {
		if err := cronspec.ValidateCron(f.Schedule); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid schedule: %v", err))
		}
	}
	if f.Format != "" && f.Format != "html" && f.Format != "csv" {
		return fiber.NewError(fiber.StatusBadRequest, "format must be 'html' or 'csv'")
	}
	if f.EmailEnabled && f.EmailChannelID != "" {
		id, err := uuid.Parse(f.EmailChannelID)
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
	if len(f.EmailRecipients) > MaxEmailRecipients {
		return fiber.NewError(fiber.StatusBadRequest,
			fmt.Sprintf("email_recipients limited to %d addresses", MaxEmailRecipients))
	}
	for _, addr := range f.EmailRecipients {
		if !emailRegex.MatchString(addr) {
			return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid email recipient: %s", addr))
		}
	}
	if len(f.Parameters) > MaxReportParametersBytes {
		return fiber.NewError(fiber.StatusBadRequest, "parameters must be under 64KB")
	}
	if _, err := reports.ParseParams(f.Parameters); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid parameters: "+err.Error())
	}
	return nil
}

// MaxReportParametersBytes caps the stored `parameters` object.
//
// It is enforced here rather than in the declaration because apischema has
// no serialized-size facet for an Object — MinLength/MaxLength count
// characters on a string and elements on an array, and checkDeclaration
// refuses them on anything else. The bound is measured on the RE-SERIALISED
// form rawReportParams produces, which is the same object with insignificant
// whitespace removed, so it is never looser than the byte count the bound
// request used to carry.
const MaxReportParametersBytes = 65536

// rawReportParams re-serialises the declared `parameters` object into the
// json.RawMessage that reports.NormalizeParams and ParseParams consume.
//
// Returning nil for an absent parameter is what keeps the update path's
// three-state read: NormalizeParams treats nil as "{}" and UpdateSchedule
// asks p.Has before calling this at all, so "the caller sent no parameters"
// and "the caller sent an empty object" stay distinct.
//
// ONE wire spelling changes meaning on the update, and it is the JSON null.
// The bound json.RawMessage read "null" as a four-byte value, so
// `"parameters": null` used to clear a schedule's options; apischema reads a
// null as ABSENT (present() in validate.go), so it now leaves them alone.
// Every other optional field on that body was a POINTER and therefore always
// read a null as absent, so this brings the one outlier into line rather
// than away from it, and no Nexara dialog has ever sent it — an explicit
// `{}` still clears them.
func rawReportParams(p *apischema.Params) (json.RawMessage, error) {
	if !p.Has("parameters") {
		return nil, nil
	}
	raw, err := json.Marshal(p.Object("parameters"))
	if err != nil {
		// Unreachable for a value that arrived as JSON, but a silent nil here
		// would store "{}" over whatever the caller sent.
		return nil, fiber.NewError(fiber.StatusBadRequest, "Invalid parameters")
	}
	return raw, nil
}

// DeleteRun handles DELETE /api/v1/reports/runs/:id
//
// manage:report on the run's cluster, the same grant that owns schedules: a
// run is the durable record of a report, and removing one is a management
// act rather than a viewing one.
func (h *ReportHandler) DeleteRun(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}
	run, err := h.queries.GetReportRun(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Run not found")
	}
	if err := requireClusterPerm(c, "manage", "report", run.ClusterID); err != nil {
		return err
	}
	if err := h.queries.DeleteReportRun(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete run")
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(run.ClusterID), "report", id.String(), "deleted", nil)
	return c.SendStatus(fiber.StatusNoContent)
}

// EmailRun handles POST /api/v1/reports/runs/:id/email
//
// Sends a finished run through an email channel: the digest as the body, the
// stored HTML attached, and the CSV when asked for. Gated on generate:report,
// the grant that produces reports — delivering one is the same act as making
// one, and nothing here reads anything the caller could not already download.
func (h *ReportHandler) EmailRun(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}
	channelID, err := parseParamUUID(p.String("channel_id"))
	if err != nil {
		return err
	}
	// The recipient cap and the address rule are the declaration's now: this
	// body is the ONLY source of the list, unlike the schedule bodies, whose
	// effective value may come from the stored row.
	recipients := p.Strings("recipients")

	row, err := h.queries.GetReportRunForEmail(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Run not found")
	}
	if err := requireClusterPerm(c, "generate", "report", row.ClusterID); err != nil {
		return err
	}
	if row.Status != "completed" || !row.ReportHtml.Valid || row.ReportHtml.String == "" {
		return fiber.NewError(fiber.StatusConflict, "Run has no finished report to send")
	}
	ch, err := h.queries.GetNotificationChannel(c.Context(), channelID)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Email channel not found")
	}
	if ch.ChannelType != "email" {
		return fiber.NewError(fiber.StatusBadRequest, "Channel must be of type 'email'")
	}

	// The stored ReportData drives the digest so the email says exactly what
	// the run said. A run from before typed reports has no usable data; it is
	// sent as a plain attachment with a one-line body.
	var data reports.ReportData
	if len(row.ReportData) == 0 || json.Unmarshal(row.ReportData, &data) != nil || data.Schema < reports.SchemaVersion {
		data = reports.ReportData{
			Schema:      reports.SchemaVersion,
			Title:       reports.ReportType(row.ReportType).Name() + " · run " + id.String()[:8],
			Kicker:      reports.ReportType(row.ReportType).Name(),
			Heading:     "Report run " + id.String()[:8],
			ReportType:  row.ReportType,
			ClusterName: row.ClusterID.String(),
			GeneratedAt: row.CompletedAt.Time.UTC().Format("2006-01-02 15:04") + " UTC",
		}
	}
	msg := reports.ReportMessage(&data, row.ReportHtml.String, row.ReportCsv.String, p.Bool("with_csv") && row.ReportCsv.Valid, reports.DigestOptions{RunID: id.String()})
	if err := reports.SendReportEmail(c.Context(), h.queries, h.encryptionKey, channelID, recipients, msg, h.logger); err != nil {
		h.logger.Error("report email failed", "run_id", id, "error", err)
		return fiber.NewError(fiber.StatusBadGateway, "Failed to send report email")
	}
	AuditLog(c, h.queries, h.eventPub, ClusterUUID(row.ClusterID), "report", id.String(), "emailed", nil)
	return c.SendStatus(fiber.StatusNoContent)
}
