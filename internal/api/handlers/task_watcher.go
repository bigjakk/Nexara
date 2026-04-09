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

// Public error codes published in the WS event stream when a Proxmox
// task watcher detects a failure path. These are sanitized — the raw
// Proxmox `ExitStatus` text, internal hostnames, filesystem paths, and
// pgx error messages stay server-side via `slog.Warn` only.
//
// Per security review H2: publishing the raw text leaks internal
// state (paths, hosts, fencing agent stack traces, schema names) to
// any WS subscriber that can correlate to a tracked task. The mobile
// task bar and desktop task list both consume the `error` field; both
// can render whatever code we ship as a friendly label without ever
// seeing the verbose original.
const (
	ErrTaskQueryFailed = "task_query_failed"
	ErrTaskFailed      = "task_failed"
	ErrTaskTimeout     = "task_timeout"
	ErrDBUpdateFailed  = "db_update_failed"
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
				// Verbose Proxmox/transport error stays server-side.
				// Clients receive a sanitized public code only —
				// security review H2 found that earlier versions of
				// this handler leaked Proxmox URLs, internal hostnames,
				// and PVE stack traces verbatim into the WS stream.
				slog.Warn("task watcher: failed to get task status",
					"upid", upid, "error", err)
				eventPub.ClusterEventWithError(context.Background(), clusterID.String(),
					events.KindVMStateChange, resourceType, vmID.String(), action,
					ErrTaskQueryFailed)
				return
			}

			if status.Status == "stopped" {
				if status.ExitStatus != "OK" {
					// Log the verbose Proxmox exit status server-side
					// for debugging. Clients only see the sanitized
					// `task_failed` code; mapping back to a friendly
					// message is the client's responsibility.
					slog.Info("task watcher: task failed, publishing failure event",
						"upid", upid, "exit_status", status.ExitStatus)
					eventPub.ClusterEventWithError(context.Background(), clusterID.String(),
						events.KindVMStateChange, resourceType, vmID.String(), action,
						ErrTaskFailed)
					return
				}
				break
			}

			select {
			case <-ctx.Done():
				slog.Warn("task watcher: timed out waiting for task", "upid", upid)
				eventPub.ClusterEventWithError(context.Background(), clusterID.String(),
					events.KindVMStateChange, resourceType, vmID.String(), action,
					ErrTaskTimeout)
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
			// Log the verbose pgx error server-side; the WS event only
			// carries the sanitized code so we don't leak schema names
			// or internal pgx text.
			slog.Warn("task watcher: failed to update VM status",
				"vm_id", vmID, "status", newStatus, "error", err)
			eventPub.ClusterEventWithError(context.Background(), clusterID.String(),
				events.KindVMStateChange, resourceType, vmID.String(), action,
				ErrDBUpdateFailed)
			return
		}

		// Publish event so the frontend refetches with the correct data.
		eventPub.ClusterEvent(ctx, clusterID.String(),
			events.KindVMStateChange, resourceType, vmID.String(), action)
	}()
}
