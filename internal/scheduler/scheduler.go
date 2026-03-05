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
	"github.com/proxdash/proxdash/internal/proxmox"
)

// Scheduler runs due scheduled tasks.
type Scheduler struct {
	queries       *db.Queries
	encryptionKey string
	logger        *slog.Logger
}

// New creates a new Scheduler.
func New(queries *db.Queries, encryptionKey string, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
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
