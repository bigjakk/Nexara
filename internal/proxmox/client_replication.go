package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

func (c *Client) GetReplicationJobs(ctx context.Context) ([]ReplicationJob, error) {
	var jobs []ReplicationJob
	if err := c.do(ctx, "/cluster/replication", &jobs); err != nil {
		return nil, fmt.Errorf("get replication jobs: %w", err)
	}
	return jobs, nil
}
func (c *Client) CreateReplicationJob(ctx context.Context, params CreateReplicationJobParams) error {
	form := url.Values{}
	form.Set("id", params.ID)
	form.Set("type", params.Type)
	form.Set("target", params.Target)
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Rate != "" {
		form.Set("rate", params.Rate)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if err := c.doPost(ctx, "/cluster/replication", form, nil); err != nil {
		return fmt.Errorf("create replication job %s: %w", params.ID, err)
	}
	return nil
}
func (c *Client) GetReplicationJob(ctx context.Context, id string) (*ReplicationJob, error) {
	path := "/cluster/replication/" + url.PathEscape(id)
	var job ReplicationJob
	if err := c.do(ctx, path, &job); err != nil {
		return nil, fmt.Errorf("get replication job %s: %w", id, err)
	}
	return &job, nil
}
func (c *Client) UpdateReplicationJob(ctx context.Context, id string, params UpdateReplicationJobParams) error {
	form := url.Values{}
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Rate != "" {
		form.Set("rate", params.Rate)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Disable != nil {
		form.Set("disable", strconv.Itoa(*params.Disable))
	}
	if params.RemoveJob != "" {
		form.Set("remove_job", params.RemoveJob)
	}
	path := "/cluster/replication/" + url.PathEscape(id)
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("update replication job %s: %w", id, err)
	}
	return nil
}
func (c *Client) DeleteReplicationJob(ctx context.Context, id string) error {
	path := "/cluster/replication/" + url.PathEscape(id)
	if err := c.doDelete(ctx, path, nil); err != nil {
		return fmt.Errorf("delete replication job %s: %w", id, err)
	}
	return nil
}
func (c *Client) TriggerReplication(ctx context.Context, node, id string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/replication/" + url.PathEscape(id) + "/schedule_now"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("trigger replication %s on %s: %w", id, node, err)
	}
	return upid, nil
}
func (c *Client) GetReplicationStatus(ctx context.Context, node, id string) (*ReplicationStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/replication/" + url.PathEscape(id) + "/status"
	var status ReplicationStatus
	if err := c.do(ctx, path, &status); err != nil {
		return nil, fmt.Errorf("get replication status %s on %s: %w", id, node, err)
	}
	return &status, nil
}
func (c *Client) GetReplicationLog(ctx context.Context, node, id string, limit int) ([]ReplicationLogEntry, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/replication/" + url.PathEscape(id) + "/log"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var entries []ReplicationLogEntry
	if err := c.do(ctx, path, &entries); err != nil {
		return nil, fmt.Errorf("get replication log %s on %s: %w", id, node, err)
	}
	return entries, nil
}
