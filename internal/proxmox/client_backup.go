package proxmox

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

func (c *Client) TriggerBackup(ctx context.Context, node string, params BackupParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID == "" {
		return "", fmt.Errorf("vzdump requires vmid")
	}
	form := url.Values{}
	form.Set("vmid", params.VMID)
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Mode != "" {
		form.Set("mode", params.Mode)
	}
	if params.Compress != "" {
		form.Set("compress", params.Compress)
	}
	path := "/nodes/" + url.PathEscape(node) + "/vzdump"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("trigger backup on %s: %w", node, err)
	}
	return upid, nil
}
func (c *Client) ListBackupJobs(ctx context.Context) ([]BackupJob, error) {
	var jobs []BackupJob
	if err := c.do(ctx, "/cluster/backup", &jobs); err != nil {
		return nil, fmt.Errorf("list backup jobs: %w", err)
	}
	return jobs, nil
}
func (c *Client) GetBackupJob(ctx context.Context, id string) (*BackupJob, error) {
	if id == "" {
		return nil, fmt.Errorf("backup job ID is required")
	}
	var job BackupJob
	if err := c.do(ctx, "/cluster/backup/"+url.PathEscape(id), &job); err != nil {
		return nil, fmt.Errorf("get backup job %s: %w", id, err)
	}
	return &job, nil
}
func (c *Client) CreateBackupJob(ctx context.Context, params BackupJobParams) error {
	form := url.Values{}
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	if params.VMID != "" {
		form.Set("vmid", params.VMID)
	}
	if params.Mode != "" {
		form.Set("mode", params.Mode)
	}
	if params.Compress != "" {
		form.Set("compress", params.Compress)
	}
	if params.Enabled != nil {
		form.Set("enabled", strconv.Itoa(*params.Enabled))
	}
	if params.MailNotification != "" {
		form.Set("mailnotification", params.MailNotification)
	}
	if params.MailTo != "" {
		form.Set("mailto", params.MailTo)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Type != "" {
		form.Set("type", params.Type)
	}
	if err := c.doPost(ctx, "/cluster/backup", form, nil); err != nil {
		return fmt.Errorf("create backup job: %w", err)
	}
	return nil
}
func (c *Client) UpdateBackupJob(ctx context.Context, id string, params BackupJobParams) error {
	if id == "" {
		return fmt.Errorf("backup job ID is required")
	}
	form := url.Values{}
	if params.Schedule != "" {
		form.Set("schedule", params.Schedule)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Node != "" {
		form.Set("node", params.Node)
	}
	if params.VMID != "" {
		form.Set("vmid", params.VMID)
	}
	if params.Mode != "" {
		form.Set("mode", params.Mode)
	}
	if params.Compress != "" {
		form.Set("compress", params.Compress)
	}
	if params.Enabled != nil {
		form.Set("enabled", strconv.Itoa(*params.Enabled))
	}
	if params.MailNotification != "" {
		form.Set("mailnotification", params.MailNotification)
	}
	if params.MailTo != "" {
		form.Set("mailto", params.MailTo)
	}
	if params.Comment != "" {
		form.Set("comment", params.Comment)
	}
	if params.Type != "" {
		form.Set("type", params.Type)
	}
	if err := c.doPut(ctx, "/cluster/backup/"+url.PathEscape(id), form, nil); err != nil {
		return fmt.Errorf("update backup job %s: %w", id, err)
	}
	return nil
}
func (c *Client) RunBackupJob(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("backup job ID is required")
	}
	var upid string
	if err := c.doPost(ctx, "/cluster/backup/"+url.PathEscape(id)+"/run", nil, &upid); err != nil {
		return "", fmt.Errorf("run backup job %s: %w", id, err)
	}
	return upid, nil
}
func (c *Client) DeleteBackupJob(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("backup job ID is required")
	}
	if err := c.doDelete(ctx, "/cluster/backup/"+url.PathEscape(id), nil); err != nil {
		return fmt.Errorf("delete backup job %s: %w", id, err)
	}
	return nil
}
