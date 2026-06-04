package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

func (c *Client) GetTaskStatus(ctx context.Context, node string, upid string) (*TaskStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if upid == "" {
		return nil, fmt.Errorf("UPID cannot be empty")
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"
	var status TaskStatus
	if err := c.do(ctx, path, &status); err != nil {
		return nil, fmt.Errorf("get task status on %s: %w", node, err)
	}
	return &status, nil
}
func (c *Client) GetTaskLog(ctx context.Context, node string, upid string, start int) ([]TaskLogEntry, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if upid == "" {
		return nil, fmt.Errorf("UPID cannot be empty")
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/log?start=" + strconv.Itoa(start) + "&limit=5000"
	var entries []TaskLogEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get task log on %s: %w", node, err)
	}
	return entries, nil
}
func (c *Client) GetNodeTasks(ctx context.Context, node string, since int64, limit int) ([]NodeTask, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 500
	}
	// source=all returns both active (running) and archived (finished) tasks;
	// PVE's default is archive-only, which would hide in-progress tasks the
	// collector ingests for live status tracking.
	path := "/nodes/" + url.PathEscape(node) + "/tasks?limit=" + strconv.Itoa(limit) + "&since=" + strconv.FormatInt(since, 10) + "&start=0&source=all"
	var tasks []NodeTask
	if err := c.do(ctx, path, &tasks); err != nil {
		return nil, fmt.Errorf("get tasks on %s: %w", node, err)
	}
	return tasks, nil
}
