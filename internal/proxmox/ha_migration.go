package proxmox

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// HAMigrationRequestStart returns the start time of a task that only asks the
// HA manager for a migration, and whether upid is one. For an HA-managed guest
// Proxmox answers the migrate call with an hamigrate task, which queues the
// request and ends OK within a second, whatever becomes of the migration.
func HAMigrationRequestStart(upid string) (int64, bool) {
	parts := strings.Split(upid, ":")
	if len(parts) < 7 || parts[5] != "hamigrate" {
		return 0, false
	}
	start, err := strconv.ParseInt(parts[4], 16, 64)
	return start, err == nil
}

// HAStateRequest reports whether upid is a task that only asks the HA manager
// to stop or start a guest. For an HA-managed guest Proxmox answers a shutdown,
// stop or start with an hastop or hastart task, which queues the request and
// ends OK within a second; HA acts on it on its next round.
func HAStateRequest(upid string) bool {
	parts := strings.Split(upid, ":")
	return len(parts) >= 7 && (parts[5] == "hastop" || parts[5] == "hastart")
}

// AwaitHAMigration waits for the migration the HA manager starts for vmid after
// an hamigrate that started at since, and returns its UPID. It reads node's task
// list from since every interval until a qmigrate or vzmigrate for the guest is
// listed, so an earlier attempt's task cannot be taken for this one's. The HA
// manager runs it on the guest's node. A function, not a method: the UPID it
// returns is of a task the HA manager started, not one the call started.
//
// When ctx ends first it returns ctx's error, and unread is the error of the
// last read when that read failed (nil when it succeeded): a migration can start
// after the last good look, so could not look is not the same as no migration.
// onReadErr, when set, sees each failed read.
func AwaitHAMigration(ctx context.Context, c *Client, node string, vmid int, since int64, interval time.Duration,
	onReadErr func(error)) (upid string, unread, err error) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	id := strconv.Itoa(vmid)
	for {
		select {
		case <-ctx.Done():
			return "", unread, ctx.Err()
		case <-ticker.C:
			tasks, listErr := c.GetNodeTasks(ctx, node, since, 0)
			if listErr != nil {
				if onReadErr != nil {
					onReadErr(listErr)
				}
				// A read cut off by ctx ending says nothing about the list.
				if ctx.Err() == nil {
					unread = listErr
				}
				continue
			}
			unread = nil
			for _, t := range tasks {
				if t.ID == id && (t.Type == "qmigrate" || t.Type == "vzmigrate") {
					return t.UPID, nil, nil
				}
			}
		}
	}
}
