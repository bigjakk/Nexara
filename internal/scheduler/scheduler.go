package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/drs"
	"github.com/proxdash/proxdash/internal/events"
	"github.com/proxdash/proxdash/internal/notifications"
	"github.com/proxdash/proxdash/internal/proxmox"
	"github.com/proxdash/proxdash/internal/reports"
	"github.com/proxdash/proxdash/internal/scanner"
)

// Scheduler runs due scheduled tasks.
type Scheduler struct {
	queries       *db.Queries
	encryptionKey string
	logger        *slog.Logger
	drsEngine     *drs.Engine
	drsExecutor   *drs.Executor
	cveScanner    *scanner.Engine
	alertEngine   *notifications.Engine
	reportGen     *reports.Generator
	eventPub      *events.Publisher
}

// New creates a new Scheduler.
func New(queries *db.Queries, encryptionKey string, logger *slog.Logger, eventPub *events.Publisher) *Scheduler {
	return &Scheduler{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
		drsEngine:     drs.NewEngine(queries, encryptionKey, logger.With("component", "drs-engine")),
		drsExecutor:   drs.NewExecutor(queries, logger.With("component", "drs-executor"), eventPub),
		cveScanner:    scanner.NewEngine(queries, encryptionKey, logger.With("component", "cve-scanner")),
		alertEngine:   notifications.NewEngine(queries, logger.With("component", "alert-engine"), eventPub, newDispatcherRegistry(), encryptionKey),
		reportGen:     reports.NewGenerator(queries, logger.With("component", "report-gen")),
		eventPub:      eventPub,
	}
}

// Run finds all due tasks and executes them.
func (s *Scheduler) Run(ctx context.Context) {
	tasks, err := s.queries.ListDueTasks(ctx)
	if err != nil {
		s.logger.Error("failed to list due tasks", "error", err)
		return
	}

	if len(tasks) == 0 {
		return
	}

	s.logger.Info("executing due scheduled tasks", "count", len(tasks))

	// Group tasks by cluster for client reuse.
	byCluster := make(map[uuid.UUID][]db.ScheduledTask)
	for _, t := range tasks {
		byCluster[t.ClusterID] = append(byCluster[t.ClusterID], t)
	}

	for clusterID, clusterTasks := range byCluster {
		client, err := s.createClient(ctx, clusterID)
		if err != nil {
			s.logger.Error("failed to create proxmox client for cluster",
				"cluster_id", clusterID, "error", err)
			for _, t := range clusterTasks {
				s.markFailed(ctx, t, fmt.Sprintf("create client: %v", err))
			}
			continue
		}

		for _, t := range clusterTasks {
			s.executeTask(ctx, client, t)
		}
	}
}

// RunDRS evaluates DRS for all enabled clusters.
func (s *Scheduler) RunDRS(ctx context.Context) {
	configs, err := s.queries.ListEnabledDRSConfigs(ctx)
	if err != nil {
		s.logger.Error("failed to list enabled DRS configs", "error", err)
		return
	}

	if len(configs) == 0 {
		return
	}

	for _, cfg := range configs {
		recommendations, err := s.drsEngine.Evaluate(ctx, cfg.ClusterID)
		if err != nil {
			s.logger.Error("DRS evaluation failed",
				"cluster_id", cfg.ClusterID, "error", err)
			continue
		}

		if len(recommendations) == 0 {
			continue
		}

		s.logger.Info("DRS produced recommendations",
			"cluster_id", cfg.ClusterID, "count", len(recommendations))

		client, err := s.createClient(ctx, cfg.ClusterID)
		if err != nil {
			s.logger.Error("failed to create proxmox client for DRS",
				"cluster_id", cfg.ClusterID, "error", err)
			continue
		}

		if err := s.drsExecutor.Execute(ctx, client, cfg.ClusterID, cfg.Mode, recommendations); err != nil {
			s.logger.Error("DRS execution failed",
				"cluster_id", cfg.ClusterID, "error", err)
		}
	}
}

// RunCVEScanning runs CVE scans for clusters based on their schedule configuration.
// Clusters with no schedule config default to enabled with a 24-hour interval.
func (s *Scheduler) RunCVEScanning(ctx context.Context) {
	clusters, err := s.queries.ListClusters(ctx)
	if err != nil {
		s.logger.Error("failed to list clusters for CVE scanning", "error", err)
		return
	}

	// Build a map of schedule configs for quick lookup
	schedules := make(map[uuid.UUID]struct {
		enabled  bool
		interval time.Duration
	})
	scheds, err := s.queries.ListEnabledCVEScanSchedules(ctx)
	if err != nil {
		s.logger.Warn("failed to list CVE scan schedules, using defaults", "error", err)
	}
	for _, sc := range scheds {
		schedules[sc.ClusterID] = struct {
			enabled  bool
			interval time.Duration
		}{enabled: sc.Enabled, interval: time.Duration(sc.IntervalHours) * time.Hour}
	}

	for _, cluster := range clusters {
		if !cluster.IsActive {
			continue
		}

		// Look up schedule config; default to enabled / 24h
		sched, hasConfig := schedules[cluster.ID]
		if hasConfig {
			if !sched.enabled {
				continue
			}
		} else {
			// Check if there's a disabled schedule (not in the "enabled" list)
			cfg, cfgErr := s.queries.GetCVEScanSchedule(ctx, cluster.ID)
			if cfgErr == nil && !cfg.Enabled {
				continue
			}
			sched.interval = 24 * time.Hour
		}

		// Check if last scan was within the configured interval
		lastScan, err := s.queries.GetLatestCVEScan(ctx, cluster.ID)
		if err == nil && time.Since(lastScan.CreatedAt) < sched.interval {
			continue
		}

		s.logger.Info("starting CVE scan", "cluster_id", cluster.ID, "cluster_name", cluster.Name)

		scanID, err := s.cveScanner.ScanCluster(ctx, cluster.ID)
		if err != nil {
			s.logger.Error("CVE scan failed",
				"cluster_id", cluster.ID, "error", err)
			continue
		}

		s.logger.Info("CVE scan completed",
			"cluster_id", cluster.ID, "scan_id", scanID)

		if s.eventPub != nil {
			s.eventPub.ClusterEvent(ctx, cluster.ID.String(), events.KindCVEScan, "cve_scan", scanID.String(), "completed")
		}
	}
}

// snapshotParams holds decoded params for snapshot actions.
type snapshotParams struct {
	SnapName    string `json:"snap_name"`
	Description string `json:"description"`
	VMState     bool   `json:"vmstate"`
}

func (s *Scheduler) executeTask(ctx context.Context, client *proxmox.Client, task db.ScheduledTask) {
	now := time.Now()
	var execErr error

	switch task.Action {
	case "snapshot":
		execErr = s.executeSnapshot(ctx, client, task)
	case "reboot":
		execErr = s.executeReboot(ctx, client, task)
	default:
		execErr = fmt.Errorf("unsupported action: %s", task.Action)
	}

	status := "success"
	errMsg := ""
	if execErr != nil {
		status = "failed"
		errMsg = execErr.Error()
		s.logger.Error("scheduled task failed",
			"task_id", task.ID, "action", task.Action, "error", execErr)
	} else {
		s.logger.Info("scheduled task completed",
			"task_id", task.ID, "action", task.Action)
	}

	nextRun, cronErr := NextRunTime(task.Schedule, now)
	if cronErr != nil {
		s.logger.Error("failed to compute next run time",
			"task_id", task.ID, "error", cronErr)
	}

	if err := s.queries.UpdateTaskLastRun(ctx, db.UpdateTaskLastRunParams{
		ID:        task.ID,
		LastRunAt: pgtype.Timestamptz{Time: now, Valid: true},
		NextRunAt: pgtype.Timestamptz{Time: nextRun, Valid: cronErr == nil},
		LastStatus: pgtype.Text{String: status, Valid: true},
		LastError:  pgtype.Text{String: errMsg, Valid: errMsg != ""},
	}); err != nil {
		s.logger.Error("failed to update task last run", "task_id", task.ID, "error", err)
	}
}

func (s *Scheduler) executeSnapshot(ctx context.Context, client *proxmox.Client, task db.ScheduledTask) error {
	var params snapshotParams
	if err := json.Unmarshal(task.Params, &params); err != nil {
		return fmt.Errorf("unmarshal snapshot params: %w", err)
	}

	snapName := params.SnapName
	if snapName == "" {
		snapName = fmt.Sprintf("auto-%s", time.Now().Format("20060102-150405"))
	}

	sp := proxmox.SnapshotParams{
		SnapName:    snapName,
		Description: params.Description,
		VMState:     params.VMState,
	}

	switch task.ResourceType {
	case "vm":
		_, err := client.CreateVMSnapshot(ctx, task.Node, mustAtoi(task.ResourceID), sp)
		return err
	case "ct":
		_, err := client.CreateCTSnapshot(ctx, task.Node, mustAtoi(task.ResourceID), sp)
		return err
	default:
		return fmt.Errorf("unsupported resource type for snapshot: %s", task.ResourceType)
	}
}

func (s *Scheduler) executeReboot(ctx context.Context, client *proxmox.Client, task db.ScheduledTask) error {
	vmid := mustAtoi(task.ResourceID)
	switch task.ResourceType {
	case "vm":
		_, err := client.RebootVM(ctx, task.Node, vmid)
		return err
	case "ct":
		_, err := client.RebootCT(ctx, task.Node, vmid)
		return err
	default:
		return fmt.Errorf("unsupported resource type for reboot: %s", task.ResourceType)
	}
}

func (s *Scheduler) createClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
	cluster, err := s.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("get cluster %s: %w", clusterID, err)
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, s.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}

	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        60 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	return client, nil
}

func (s *Scheduler) markFailed(ctx context.Context, task db.ScheduledTask, errMsg string) {
	now := time.Now()
	nextRun, _ := NextRunTime(task.Schedule, now)

	_ = s.queries.UpdateTaskLastRun(ctx, db.UpdateTaskLastRunParams{
		ID:         task.ID,
		LastRunAt:  pgtype.Timestamptz{Time: now, Valid: true},
		NextRunAt:  pgtype.Timestamptz{Time: nextRun, Valid: true},
		LastStatus: pgtype.Text{String: "failed", Valid: true},
		LastError:  pgtype.Text{String: errMsg, Valid: true},
	})
}

// RunAlertEvaluation evaluates all enabled alert rules against current metrics.
func (s *Scheduler) RunAlertEvaluation(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("alert evaluation panicked", "panic", r)
		}
	}()
	s.alertEngine.Evaluate(ctx)
}

// RunReportGeneration checks for due report schedules and generates reports.
func (s *Scheduler) RunReportGeneration(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("report generation panicked", "panic", r)
		}
	}()

	schedules, err := s.queries.ListDueReportSchedules(ctx)
	if err != nil {
		s.logger.Error("failed to list due report schedules", "error", err)
		return
	}

	if len(schedules) == 0 {
		return
	}

	s.logger.Info("processing due report schedules", "count", len(schedules))

	for _, sched := range schedules {
		s.generateScheduledReport(ctx, sched)
	}
}

func (s *Scheduler) generateScheduledReport(ctx context.Context, sched db.ReportSchedule) {
	run, err := s.queries.InsertReportRun(ctx, db.InsertReportRunParams{
		ScheduleID:     pgtype.UUID{Bytes: sched.ID, Valid: true},
		ReportType:     sched.ReportType,
		ClusterID:      sched.ClusterID,
		Status:         "running",
		TimeRangeHours: sched.TimeRangeHours,
		CreatedBy:      sched.CreatedBy,
	})
	if err != nil {
		s.logger.Error("failed to create report run", "schedule_id", sched.ID, "error", err)
		return
	}

	_ = s.queries.UpdateReportRunStarted(ctx, run.ID)

	data, err := s.reportGen.Generate(ctx, sched.ReportType, sched.ClusterID, int(sched.TimeRangeHours))
	if err != nil {
		_ = s.queries.UpdateReportRunFailed(ctx, db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: err.Error(),
		})
		s.logger.Error("report generation failed", "schedule_id", sched.ID, "error", err)
		s.updateScheduleNextRun(ctx, sched)
		return
	}

	htmlOutput, err := reports.RenderHTML(data)
	if err != nil {
		_ = s.queries.UpdateReportRunFailed(ctx, db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: fmt.Sprintf("render HTML: %v", err),
		})
		s.updateScheduleNextRun(ctx, sched)
		return
	}

	csvOutput, err := reports.RenderCSV(data)
	if err != nil {
		_ = s.queries.UpdateReportRunFailed(ctx, db.UpdateReportRunFailedParams{
			ID:           run.ID,
			ErrorMessage: fmt.Sprintf("render CSV: %v", err),
		})
		s.updateScheduleNextRun(ctx, sched)
		return
	}

	dataJSON, _ := json.Marshal(data)

	if err := s.queries.UpdateReportRunCompleted(ctx, db.UpdateReportRunCompletedParams{
		ID:         run.ID,
		ReportData: dataJSON,
		ReportHtml: pgtype.Text{String: htmlOutput, Valid: true},
		ReportCsv:  pgtype.Text{String: csvOutput, Valid: true},
	}); err != nil {
		s.logger.Error("failed to save report", "run_id", run.ID, "error", err)
	}

	s.logger.Info("scheduled report generated",
		"schedule_id", sched.ID, "run_id", run.ID, "type", sched.ReportType)

	// Send email if configured.
	if sched.EmailEnabled && sched.EmailChannelID.Valid {
		channelID, _ := uuid.FromBytes(sched.EmailChannelID.Bytes[:])
		subject := fmt.Sprintf("ProxDash Report: %s", data.Title)
		if err := reports.SendReportEmail(ctx, s.queries, s.encryptionKey, channelID, sched.EmailRecipients, subject, htmlOutput, s.logger); err != nil {
			s.logger.Error("failed to send report email", "schedule_id", sched.ID, "error", err)
		}
	}

	if s.eventPub != nil {
		s.eventPub.SystemEvent(ctx, events.KindReportGenerated, "completed")
	}

	s.updateScheduleNextRun(ctx, sched)
}

func (s *Scheduler) updateScheduleNextRun(ctx context.Context, sched db.ReportSchedule) {
	now := time.Now()
	nextRun, err := NextRunTime(sched.Schedule, now)
	next := pgtype.Timestamptz{Time: nextRun, Valid: err == nil}

	if uErr := s.queries.UpdateReportScheduleLastRun(ctx, db.UpdateReportScheduleLastRunParams{
		ID:        sched.ID,
		LastRunAt: pgtype.Timestamptz{Time: now, Valid: true},
		NextRunAt: next,
	}); uErr != nil {
		s.logger.Error("failed to update schedule last run", "schedule_id", sched.ID, "error", uErr)
	}
}

// newDispatcherRegistry creates a registry with all notification dispatchers.
func newDispatcherRegistry() *notifications.Registry {
	r := notifications.NewRegistry()
	r.Register(&notifications.SMTPDispatcher{})
	r.Register(&notifications.SlackDispatcher{})
	r.Register(&notifications.DiscordDispatcher{})
	r.Register(&notifications.TeamsDispatcher{})
	r.Register(&notifications.TelegramDispatcher{})
	r.Register(&notifications.WebhookDispatcher{})
	r.Register(&notifications.PagerDutyDispatcher{})
	return r
}

// mustAtoi converts a string to int, returning 0 on failure.
func mustAtoi(s string) int {
	var n int
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}
