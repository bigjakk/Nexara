package drs

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/auth"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/redact"
	"github.com/bigjakk/nexara/internal/safeconv"
)

const (
	// statusInterrupted is the history status of an attempt whose end DRS did not
	// see. It is not a failure, so the back-off does not count it: the migration
	// carries on without Nexara and may well succeed.
	statusInterrupted = "interrupted"
	interruptedNote   = "Nexara stopped following this migration before seeing how it ended (a shutdown, or the scheduler role moving). " +
		"It may still be running on Proxmox; its task shows the result."

	// finalLookTimeout is short because a shutdown has to fit this last status read
	// and the writes after it before the SIGKILL that `docker stop` sends after 10 s.
	finalLookTimeout = 5 * time.Second
	// recordTimeout rides out a brief database outage. At shutdown Drain is the bound.
	recordTimeout = 30 * time.Second

	failureLogTimeout  = 10 * time.Second
	failureLogMaxLines = 10

	drainPollInterval = 50 * time.Millisecond
)

// taskLogError is a log line that says why a migration failed. Proxmox's migration
// code logs those at error level (PVE::AbstractMigrate): the time, "ERROR:", then
// one line of the message. Two reasons carry no such mark, and their error lines say
// nothing (PVE::QemuMigrate): a volume it cannot move, as with a VM's local disk,
// where the error says "can't migrate VM - check log", and QEMU failing a live
// migration, where it says "online migrate failure - aborting".
var taskLogError = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} (?:ERROR: (.+)|((?:can't migrate|migration status error:) .+))$`)

// executorQueries is the database surface Execute uses, behind an interface so
// its failure paths can be tested without a database, as with evalQueries.
type executorQueries interface {
	InsertDRSHistory(ctx context.Context, arg db.InsertDRSHistoryParams) (db.DrsHistory, error)
	UpdateDRSHistoryStatus(ctx context.Context, arg db.UpdateDRSHistoryStatusParams) error
	InsertTaskHistory(ctx context.Context, arg db.InsertTaskHistoryParams) (db.TaskHistory, error)
	ReconcileTaskHistory(ctx context.Context, arg db.ReconcileTaskHistoryParams) (int64, error)
	GetVMByClusterAndVmid(ctx context.Context, arg db.GetVMByClusterAndVmidParams) (db.Vm, error)
	InsertAuditLog(ctx context.Context, arg db.InsertAuditLogParams) error
}

// Executor handles DRS migration execution and history recording.
type Executor struct {
	queries  executorQueries
	logger   *slog.Logger
	eventPub *events.Publisher
	// shutdownCtx is the parent for any context that must outlive a
	// scheduler tick but should still be cancelled on graceful shutdown
	// (SIGTERM). nil falls back to context.Background() so a partially
	// constructed Executor (tests) still works.
	shutdownCtx context.Context
	// taskPollInterval is how often Execute asks Proxmox about a migration.
	taskPollInterval time.Duration
	// haStartGrace is how long followHAMigration waits for a migration to start.
	haStartGrace time.Duration
	// executing counts the Execute calls in progress, for Drain.
	executing atomic.Int64
}

// NewExecutor creates a new DRS executor. shutdownCtx should be the
// per-server context that's cancelled on SIGTERM; pass context.Background()
// from contexts that don't have one (e.g. tests).
func NewExecutor(shutdownCtx context.Context, queries *db.Queries, logger *slog.Logger, eventPub *events.Publisher) *Executor {
	if shutdownCtx == nil {
		shutdownCtx = context.Background()
	}
	return &Executor{
		queries:          queries,
		logger:           logger,
		eventPub:         eventPub,
		shutdownCtx:      shutdownCtx,
		taskPollInterval: 5 * time.Second,
		haStartGrace:     2 * time.Minute,
	}
}

// Drain waits up to d for Execute calls in progress to finish and reports whether
// they did. The process calls it after cancelling shutdownCtx: nothing else waits
// for the scheduler's goroutines, so without it the process exits before the
// outcome of a migration it was following is written.
func (e *Executor) Drain(d time.Duration) bool {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	ticker := time.NewTicker(drainPollInterval)
	defer ticker.Stop()

	for e.executing.Load() > 0 {
		select {
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
	return true
}

// Execute processes recommendations based on the DRS mode.
// In "advisory" mode, only records to history. In "automatic" mode, executes migrations.
func (e *Executor) Execute(ctx context.Context, client *proxmox.Client, clusterID uuid.UUID, mode string, recommendations []Recommendation) error {
	e.executing.Add(1)
	defer e.executing.Add(-1)

	for _, rec := range recommendations {
		// The process or the leader role is going: start nothing more.
		if ctx.Err() != nil {
			break
		}

		history, err := e.queries.InsertDRSHistory(ctx, db.InsertDRSHistoryParams{
			ClusterID:   clusterID,
			SourceNode:  rec.SourceNode,
			TargetNode:  rec.TargetNode,
			VmID:        safeconv.Int32(rec.VMID),
			VmType:      rec.VMType,
			Reason:      rec.Reason,
			ScoreBefore: rec.ScoreBefore,
			ScoreAfter:  rec.ScoreAfter,
			Status:      "pending",
			ExecutedAt:  pgtype.Timestamptz{Time: time.Now(), Valid: true},
		})
		if err != nil {
			e.logger.Error("failed to record DRS history", "error", err)
			continue
		}

		// Resolve VM database ID for audit log linking.
		vmDBID := e.resolveVMDBID(ctx, clusterID, rec.VMID)

		if mode == "advisory" {
			e.logger.Info("DRS advisory recommendation",
				"vmid", rec.VMID, "type", rec.VMType,
				"from", rec.SourceNode, "to", rec.TargetNode,
				"reason", rec.Reason)
			_ = e.queries.UpdateDRSHistoryStatus(ctx, db.UpdateDRSHistoryStatusParams{
				ID:         history.ID,
				Status:     "advisory",
				ExecutedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
			})
			e.auditLog(ctx, clusterID, vmDBID, "drs_advisory", rec, nil)
			e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "advisory")
			continue
		}

		e.migrate(ctx, client, clusterID, history.ID, vmDBID, rec)
	}

	return nil
}

// migrate runs one attempt and records how it ended. ctx ends with the process or
// the leader role, so it can be gone by the time there is something to record:
// everything after the request goes through recordContext.
func (e *Executor) migrate(ctx context.Context, client *proxmox.Client, clusterID, historyID uuid.UUID, vmDBID string, rec Recommendation) {
	e.logger.Info("DRS executing migration",
		"vmid", rec.VMID, "type", rec.VMType,
		"from", rec.SourceNode, "to", rec.TargetNode)

	params := proxmox.MigrateParams{
		Target: rec.TargetNode,
		Online: true,
	}

	var upid string
	var migrateErr error

	switch rec.VMType {
	case "qemu":
		upid, migrateErr = client.MigrateVM(ctx, rec.SourceNode, rec.VMID, params)
	case "lxc":
		upid, migrateErr = client.MigrateCT(ctx, rec.SourceNode, rec.VMID, params)
	default:
		migrateErr = fmt.Errorf("unsupported VM type: %s", rec.VMType)
	}

	if migrateErr != nil {
		e.logger.Error("DRS migration failed",
			"vmid", rec.VMID, "error", migrateErr)
		status, text := "failed", migrateErr.Error()
		// Cancelled while asking, so Proxmox may have started it all the same.
		if ctx.Err() != nil {
			status, text = statusInterrupted, interruptedNote
		}
		e.recordOutcome(ctx, clusterID, historyID, vmDBID, rec, status, text)
		return
	}

	e.recordStart(ctx, clusterID, vmDBID, rec, upid)

	// Poll task status until completion.
	status, exitStatus := e.waitForTask(ctx, client, rec.SourceNode, upid)
	taskStatus := status
	e.finishTask(ctx, upid, status, exitStatus)

	// The history and the audit log record the migration, not the task that
	// asked for it, so an HA request is followed to the task that does it.
	migrationUPID := upid
	if since, isHA := haRequestStart(upid); isHA && status == "completed" {
		migrationUPID, status, exitStatus = e.followHAMigration(client, rec, since)
	}

	text := exitStatus
	if status == "failed" {
		text = e.failureReason(client, rec.SourceNode, migrationUPID, exitStatus)
	}
	e.recordOutcome(ctx, clusterID, historyID, vmDBID, rec, status, text)

	// Last, because this is what makes an open History tab read again: the outcome's
	// audit row has to be there by then.
	eventCtx, cancel := recordContext(ctx)
	defer cancel()
	e.eventPub.ClusterEvent(eventCtx, clusterID.String(), events.KindTaskUpdate, "task", upid, taskStatus)

	e.logger.Info("DRS migration ended",
		"vmid", rec.VMID, "status", status, "upid", upid)
}

// recordContext is for what DRS writes about a migration it has asked Proxmox for,
// which has to land however ctx has ended. Drain keeps the process alive for it.
func recordContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
}

// recordStart records a migration Proxmox has accepted.
func (e *Executor) recordStart(ctx context.Context, clusterID uuid.UUID, vmDBID string, rec Recommendation, upid string) {
	ctx, cancel := recordContext(ctx)
	defer cancel()

	// Insert into task_history so the task panel shows this migration.
	description := fmt.Sprintf("DRS Migrate %s %d: %s → %s",
		rec.VMType, rec.VMID, rec.SourceNode, rec.TargetNode)
	_, taskErr := e.queries.InsertTaskHistory(ctx, db.InsertTaskHistoryParams{
		ClusterID:   clusterID,
		UserID:      auth.SystemUserID,
		Upid:        upid,
		Description: description,
		Status:      "running",
		Node:        rec.SourceNode,
		TaskType:    "drs_migrate",
	})
	if taskErr != nil {
		e.logger.Warn("failed to insert task history for DRS migration",
			"upid", upid, "error", taskErr)
	}

	// Audit log with UPID so the activity pane can show a progress bar. Before the
	// events, which share this deadline: a hung Redis must not cost the row.
	e.auditLog(ctx, clusterID, vmDBID, "drs_migrate_running", rec, map[string]any{"upid": upid, "node": rec.SourceNode})

	e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindTaskCreated, "task", upid, "drs_migrate")
	e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_started")
}

// finishTask finalizes the task_history row of the task Proxmox answered with. An
// interrupted task is still running, so its row stays open for the collector's
// reconciler, which finalizes it from Proxmox on whichever replica leads next.
func (e *Executor) finishTask(ctx context.Context, upid, status, exitStatus string) {
	if status == statusInterrupted {
		return
	}
	ctx, cancel := recordContext(ctx)
	defer cancel()

	// ReconcileTaskHistory is guarded on status='running', so it won't clobber
	// a row the collector reconciler already finalized; status is
	// "completed"/"failed" from waitForTask.
	//
	// exitStatus is PVE's text verbatim, UNSCRUBBED. Safe only because DRS
	// issues MigrateVM/MigrateCT and never RemoteMigrate, so no target
	// cluster's API token is ever handed to Proxmox on this path and its
	// die message cannot echo one back. Note DRS also creates no
	// migration_jobs rows, so neither credential guard that protects
	// cross-cluster migration (the anti-join in
	// ListRunningTaskHistoryByCluster, the lookup in seenTaskUPIDs) would
	// cover this write — giving DRS a remote target needs both, plus a
	// scrub here and in failureReason.
	_, _ = e.queries.ReconcileTaskHistory(ctx, db.ReconcileTaskHistoryParams{
		Upid:       upid,
		Status:     status,
		ExitStatus: exitStatus,
		FinishedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
}

// recordOutcome records how an attempt ended, in the history the back-off reads and
// in the audit log. text is the failure's reason, or the note on an interruption.
func (e *Executor) recordOutcome(ctx context.Context, clusterID, historyID uuid.UUID, vmDBID string, rec Recommendation, status, text string) {
	ctx, cancel := recordContext(ctx)
	defer cancel()

	if err := e.queries.UpdateDRSHistoryStatus(ctx, db.UpdateDRSHistoryStatusParams{
		ID:         historyID,
		Status:     status,
		ExecutedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}); err != nil {
		e.logger.Warn("failed to record how a DRS migration ended",
			"vmid", rec.VMID, "status", status, "error", err)
	}

	switch status {
	case "completed":
		e.auditLog(ctx, clusterID, vmDBID, "drs_migrate", rec, nil)
		e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_completed")
		e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindInventoryChange, "vm", vmDBID, "drs_migrated")
	case statusInterrupted:
		e.auditLog(ctx, clusterID, vmDBID, "drs_migrate_interrupted", rec, map[string]any{"note": text})
		e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_interrupted")
	default:
		e.auditLog(ctx, clusterID, vmDBID, "drs_migrate_failed", rec, failureDetails(text))
		e.eventPub.ClusterEvent(ctx, clusterID.String(), events.KindDRSAction, "drs", vmDBID, "migrate_failed")
	}
}

// failureReason says why a migration task failed. Its exit text rarely does, being
// only "migration aborted" or "migration problems", so it stands only when the log
// names nothing or cannot be read. shutdownCtx parents the read because a shutdown
// has no time for it.
//
// Keeping every error line covers both endings: "migration aborted" repeats the
// reason on its own last line, and a line it repeats adds nothing, while "migration
// problems" gives it on the lines before.
func (e *Executor) failureReason(client *proxmox.Client, node, upid, exitStatus string) string {
	ctx, cancel := context.WithTimeout(e.shutdownCtx, failureLogTimeout)
	defer cancel()

	lines, err := taskLogErrors(ctx, client, node, upid)
	if err != nil {
		e.logger.Warn("failed to read a failed migration's task log", "upid", upid, "error", err)
	}

	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if !slices.ContainsFunc(lines[i+1:], func(later string) bool { return strings.Contains(later, line) }) {
			kept = append(kept, line)
		}
	}
	if len(kept) == 0 {
		return exitStatus
	}
	return strings.Join(kept, "; ")
}

// taskLogErrors returns the messages on a task log's error lines, as far as it could
// be read. Past failureLogMaxLines it drops from the middle: the reason is at one
// end or the other.
func taskLogErrors(ctx context.Context, client *proxmox.Client, node, upid string) ([]string, error) {
	var lines []string
	for start := 0; ; {
		entries, err := client.GetTaskLog(ctx, node, upid, start)
		if err != nil {
			return lines, err
		}
		for _, entry := range entries {
			if m := taskLogError.FindStringSubmatch(entry.T); m != nil {
				lines = append(lines, cmp.Or(m[1], m[2]))
				if len(lines) > failureLogMaxLines {
					lines = slices.Delete(lines, failureLogMaxLines/2, failureLogMaxLines/2+1)
				}
			}
		}
		if len(entries) < proxmox.TaskLogPageLines {
			return lines, nil
		}
		start += len(entries)
	}
}

// haRequestStart returns the start time of a task that only asks the HA manager
// for a migration, and whether upid is one. For an HA-managed guest Proxmox answers
// the migrate call with an hamigrate task, which queues the request and ends OK
// within a second, whatever becomes of the migration.
func haRequestStart(upid string) (int64, bool) {
	parts := strings.Split(upid, ":")
	if len(parts) < 7 || parts[5] != "hamigrate" {
		return 0, false
	}
	start, err := strconv.ParseInt(parts[4], 16, 64)
	return start, err == nil
}

// followHAMigration waits for the migration the HA manager starts after an
// hamigrate task and returns that task and how it ended. Taking the hamigrate's OK
// instead records a migration that fails as completed, so the guest is retried on
// the next tick with nothing in the audit log to say why.
//
// The HA manager runs it on the guest's node, usually within 20 s, though it shuts
// a container down first, for up to a minute. If none starts within the grace the
// outcome is unknown, reported as "timeout" like a task DRS stops waiting for. The
// task list is read from since, the hamigrate's start, so an earlier attempt's task
// cannot be taken for this one's.
func (e *Executor) followHAMigration(client *proxmox.Client, rec Recommendation, since int64) (upid, status, detail string) {
	ctx, cancel := context.WithTimeout(e.shutdownCtx, e.haStartGrace)
	defer cancel()

	ticker := time.NewTicker(e.taskPollInterval)
	defer ticker.Stop()

	vmid := strconv.Itoa(rec.VMID)
	for {
		select {
		case <-ctx.Done():
			if e.shutdownCtx.Err() != nil {
				return "", statusInterrupted, interruptedNote
			}
			return "", "timeout", fmt.Sprintf("the HA manager started no migration within %s", e.haStartGrace)
		case <-ticker.C:
			tasks, err := client.GetNodeTasks(ctx, rec.SourceNode, since, 0)
			if err != nil {
				e.logger.Warn("failed to list tasks for an HA migration", "vmid", rec.VMID, "error", err)
				continue
			}
			for _, t := range tasks {
				if t.ID == vmid && (t.Type == "qmigrate" || t.Type == "vzmigrate") {
					status, detail = e.waitForTask(ctx, client, rec.SourceNode, t.UPID)
					return t.UPID, status, detail
				}
			}
		}
	}
}

func (e *Executor) waitForTask(_ context.Context, client *proxmox.Client, node string, upid string) (status string, detail string) {
	// Use a dedicated timeout context derived from the per-server shutdown
	// context. Live migrations can take 15+ minutes for large VMs, so we
	// don't tie this to the scheduler tick context. We DO honor SIGTERM via
	// shutdownCtx so a `docker compose stop` cancels the poll loop instead
	// of orphaning it.
	pollCtx, cancel := context.WithTimeout(e.shutdownCtx, 30*time.Minute)
	defer cancel()

	ticker := time.NewTicker(e.taskPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pollCtx.Done():
			// Do one final check — the migration may have finished while we
			// were waiting. Use a short independent context (NOT derived from
			// shutdownCtx) so we still record the outcome during graceful
			// shutdown rather than orphan a tracked task.
			finalCtx, finalCancel := context.WithTimeout(context.Background(), finalLookTimeout)
			ts, err := client.GetTaskStatus(finalCtx, node, upid)
			finalCancel()
			if err == nil && ts.Status == "stopped" {
				if proxmox.TaskSucceeded(ts.ExitStatus) {
					return "completed", ts.ExitStatus
				}
				return "failed", ts.ExitStatus
			}
			if e.shutdownCtx.Err() != nil {
				return statusInterrupted, interruptedNote
			}
			return "timeout", "task timed out after 30 minutes"
		case <-ticker.C:
			ts, err := client.GetTaskStatus(pollCtx, node, upid)
			if err != nil {
				e.logger.Warn("failed to poll task status", "upid", upid, "error", err)
				continue
			}
			if ts.Status == "stopped" {
				if proxmox.TaskSucceeded(ts.ExitStatus) {
					return "completed", ts.ExitStatus
				}
				return "failed", ts.ExitStatus
			}
		}
	}
}

// resolveVMDBID looks up the VM's database UUID for audit log linking.
func (e *Executor) resolveVMDBID(ctx context.Context, clusterID uuid.UUID, vmid int) string {
	vm, err := e.queries.GetVMByClusterAndVmid(ctx, db.GetVMByClusterAndVmidParams{
		ClusterID: clusterID,
		Vmid:      safeconv.Int32(vmid),
	})
	if err != nil {
		return ""
	}
	return vm.ID.String()
}

// failureDetails is what a drs_migrate_failed row adds: Proxmox's own text. Audit
// details are readable by every Viewer, so a URL in it keeps no userinfo, query or
// fragment, as when GetTaskStatus and GetTaskLog serve the same text.
func failureDetails(text string) map[string]any {
	return map[string]any{"error": redact.URLs(text)}
}

// auditLog inserts an audit log entry for a DRS action, with extra added to the
// details every entry carries.
func (e *Executor) auditLog(ctx context.Context, clusterID uuid.UUID, resourceID, action string, rec Recommendation, extra map[string]any) {
	fields := map[string]any{
		"vmid":        rec.VMID,
		"vm_type":     rec.VMType,
		"source_node": rec.SourceNode,
		"target_node": rec.TargetNode,
		"reason":      rec.Reason,
		"improvement": fmt.Sprintf("%.1f%%", rec.ExpectedImprovement*100),
	}
	maps.Copy(fields, extra)
	details, _ := json.Marshal(fields)

	if resourceID == "" {
		resourceID = fmt.Sprintf("vmid:%d", rec.VMID)
	}

	err := e.queries.InsertAuditLog(ctx, db.InsertAuditLogParams{
		ClusterID:    pgtype.UUID{Bytes: clusterID, Valid: true},
		UserID:       pgtype.UUID{Bytes: auth.SystemUserID, Valid: true},
		ResourceType: "vm",
		ResourceID:   resourceID,
		Action:       action,
		Details:      details,
	})
	if err != nil {
		e.logger.Warn("failed to insert DRS audit log", "action", action, "error", err)
	}
}
