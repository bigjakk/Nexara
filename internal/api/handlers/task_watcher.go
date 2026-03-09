package handlers

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// expectedStatusAfterAction maps a lifecycle action to the VM/CT status
// that Proxmox will report after the task completes successfully.
func expectedStatusAfterAction(action string) string {
	switch action {
	case "start", "reboot", "resume":
		return "running"
	case "stop", "shutdown":
		return "stopped"
	case "suspend":
		return "suspended"
	default:
		return ""
	}
}

// watchTaskAndUpdateStatus polls a Proxmox task until it completes, then
// updates the VM/CT status in the database and publishes an event so the
// frontend refetches with correct data. Runs in a background goroutine.
func watchTaskAndUpdateStatus(
	queries *db.Queries,
	eventPub *events.Publisher,
	pxClient *proxmox.Client,
	node string,
	upid string,
	vmID uuid.UUID,
	clusterID uuid.UUID,
	action string,
	resourceType string,
) {
	newStatus := expectedStatusAfterAction(action)
	if newStatus == "" {
		return // unknown action, nothing to update
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		// Poll until the task completes.
		for {
			status, err := pxClient.GetTaskStatus(ctx, node, upid)
			if err != nil {
				slog.Warn("task watcher: failed to get task status",
					"upid", upid, "error", err)
				return
			}

			if status.Status == "stopped" {
				if status.ExitStatus != "OK" {
					slog.Info("task watcher: task failed, not updating status",
						"upid", upid, "exit_status", status.ExitStatus)
					return
				}
				break
			}

			select {
			case <-ctx.Done():
				slog.Warn("task watcher: timed out waiting for task", "upid", upid)
				return
			case <-time.After(2 * time.Second):
			}
		}

		// Task completed successfully — update the DB.
		slog.Info("task watcher: task completed, updating DB status",
			"vm_id", vmID, "action", action, "new_status", newStatus)

		if err := queries.UpdateVMStatus(ctx, db.UpdateVMStatusParams{
			ID:     vmID,
			Status: newStatus,
		}); err != nil {
			slog.Warn("task watcher: failed to update VM status",
				"vm_id", vmID, "status", newStatus, "error", err)
			return
		}

		// Publish event so the frontend refetches with the correct data.
		eventPub.ClusterEvent(ctx, clusterID.String(),
			events.KindVMStateChange, resourceType, vmID.String(), action)
	}()
}
