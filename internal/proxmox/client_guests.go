package proxmox

import (
	"cmp"
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func (c *Client) vmStatusAction(ctx context.Context, node string, vmid int, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/status/" + action
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("%s VM %d on %s: %w", action, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) StartVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "start")
}
func (c *Client) StopVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "stop")
}
func (c *Client) ShutdownVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "shutdown")
}
func (c *Client) RebootVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "reboot")
}
func (c *Client) ResetVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "reset")
}
func (c *Client) SuspendVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "suspend")
}
func (c *Client) ResumeVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "resume")
}
func (c *Client) CloneVM(ctx context.Context, node string, vmid int, params CloneParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.NewID <= 0 {
		return "", fmt.Errorf("clone requires a positive newid")
	}

	form := url.Values{}
	form.Set("newid", strconv.Itoa(params.NewID))
	if params.Name != "" {
		form.Set("name", params.Name)
	}
	if params.Target != "" {
		form.Set("target", params.Target)
	}
	if params.Full {
		form.Set("full", "1")
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/clone"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("clone VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) DestroyVM(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("destroy VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) ConvertVMToTemplate(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/template"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("convert VM %d to template on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) ctStatusAction(ctx context.Context, node string, vmid int, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/status/" + action
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("%s CT %d on %s: %w", action, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) StartCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "start")
}
func (c *Client) StopCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "stop")
}
func (c *Client) ShutdownCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "shutdown")
}
func (c *Client) RebootCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "reboot")
}
func (c *Client) SuspendCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "suspend")
}
func (c *Client) ResumeCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "resume")
}
func (c *Client) CloneCT(ctx context.Context, node string, vmid int, params CloneParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.NewID <= 0 {
		return "", fmt.Errorf("clone requires a positive newid")
	}

	form := url.Values{}
	form.Set("newid", strconv.Itoa(params.NewID))
	if params.Name != "" {
		form.Set("hostname", params.Name)
	}
	if params.Target != "" {
		form.Set("target", params.Target)
	}
	if params.Full {
		form.Set("full", "1")
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/clone"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("clone CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) DestroyCT(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("destroy CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) ConvertCTToTemplate(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/template"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("convert CT %d to template on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) MigrateCT(ctx context.Context, node string, vmid int, params MigrateParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.Target == "" {
		return "", fmt.Errorf("migrate requires a target node")
	}

	form := url.Values{}
	form.Set("target", params.Target)
	if params.Online {
		form.Set("restart", "1")
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("migrate CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) MigrateVM(ctx context.Context, node string, vmid int, params MigrateParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.Target == "" {
		return "", fmt.Errorf("migrate requires a target node")
	}

	form := url.Values{}
	form.Set("target", params.Target)
	if params.Online {
		form.Set("online", "1")
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("migrate VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) ResizeDisk(ctx context.Context, node string, vmid int, params DiskResizeParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("disk", params.Disk)
	form.Set("size", params.Size)
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/resize"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("resize disk on VM %d: %w", vmid, err)
	}
	return nil
}
func (c *Client) ResizeCTDisk(ctx context.Context, node string, vmid int, params DiskResizeParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	form.Set("disk", params.Disk)
	form.Set("size", params.Size)
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/resize"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("resize disk on CT %d: %w", vmid, err)
	}
	return nil
}
func (c *Client) MoveDisk(ctx context.Context, node string, vmid int, params DiskMoveParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("disk", params.Disk)
	form.Set("storage", params.Storage)
	if params.Format != "" {
		form.Set("format", params.Format)
	}
	if params.Delete {
		form.Set("delete", "1")
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/move_disk"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("move disk on VM %d: %w", vmid, err)
	}
	return upid, nil
}
func (c *Client) MoveCTVolume(ctx context.Context, node string, vmid int, params CTVolumeMoveParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("volume", params.Volume)
	form.Set("storage", params.Storage)
	if params.Delete {
		form.Set("delete", "1")
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/move_volume"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("move volume on CT %d: %w", vmid, err)
	}
	return upid, nil
}
func (c *Client) AttachDisk(ctx context.Context, node string, vmid int, params DiskAttachParams) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	if params.Bus == "" || params.Storage == "" || params.Size == "" {
		return fmt.Errorf("bus, storage, and size are required")
	}

	// Build the volume spec: "storage:size[,format=fmt]"
	volume := params.Storage + ":" + params.Size
	if params.Format != "" {
		volume += ",format=" + params.Format
	}

	diskKey := params.Bus + strconv.Itoa(params.Index)
	fields := map[string]string{
		diskKey: volume,
	}

	return c.SetVMConfig(ctx, node, vmid, fields)
}
func (c *Client) DetachDisk(ctx context.Context, node string, vmid int, disk string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	if disk == "" {
		return fmt.Errorf("disk name is required")
	}

	fields := map[string]string{
		"delete": disk,
	}

	return c.SetVMConfig(ctx, node, vmid, fields)
}
func (c *Client) RestoreVM(ctx context.Context, node string, params RestoreParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("restore requires a positive VMID")
	}
	if params.Archive == "" {
		return "", fmt.Errorf("restore requires an archive path")
	}

	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	form.Set("archive", params.Archive)
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Unique {
		form.Set("unique", "1")
	}
	if params.Force {
		form.Set("force", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("restore VM %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}
func (c *Client) RestoreCT(ctx context.Context, node string, params RestoreParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("restore requires a positive VMID")
	}
	if params.Archive == "" {
		return "", fmt.Errorf("restore requires an archive path")
	}

	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	form.Set("ostemplate", params.Archive)
	form.Set("restore", "1")
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.Unique {
		form.Set("unique", "1")
	}
	if params.Force {
		form.Set("force", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("restore CT %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}
func (c *Client) ListVMSnapshots(ctx context.Context, node string, vmid int) ([]Snapshot, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot"
	var snaps []Snapshot
	if err := c.do(ctx, path, &snaps); err != nil {
		return nil, fmt.Errorf("list VM %d snapshots on %s: %w", vmid, node, err)
	}
	return snaps, nil
}
func (c *Client) CreateVMSnapshot(ctx context.Context, node string, vmid int, params SnapshotParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.SnapName == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	form := url.Values{}
	form.Set("snapname", params.SnapName)
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.VMState {
		form.Set("vmstate", "1")
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create snapshot on VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) DeleteVMSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if snapname == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete snapshot %s on VM %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) RollbackVMSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if snapname == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname) + "/rollback"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("rollback snapshot %s on VM %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}

// featureCheck is the response of GET /nodes/{node}/{qemu|lxc}/{vmid}/feature.
type featureCheck struct {
	HasFeature FlexBool `json:"hasFeature"`
}

// GetVMSnapshotFeature reports whether a VM's current configuration supports
// taking snapshots — the same check the native PVE UI uses to enable its
// snapshot button (false e.g. for raw disks or TPM state on file storage).
func (c *Client) GetVMSnapshotFeature(ctx context.Context, node string, vmid int) (bool, error) {
	if err := validateNodeName(node); err != nil {
		return false, err
	}
	if err := validateVMID(vmid); err != nil {
		return false, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/feature?feature=snapshot"
	var res featureCheck
	if err := c.do(ctx, path, &res); err != nil {
		return false, fmt.Errorf("get snapshot feature for VM %d on %s: %w", vmid, node, err)
	}
	return bool(res.HasFeature), nil
}

// GetCTSnapshotFeature is the container counterpart of GetVMSnapshotFeature.
func (c *Client) GetCTSnapshotFeature(ctx context.Context, node string, vmid int) (bool, error) {
	if err := validateNodeName(node); err != nil {
		return false, err
	}
	if err := validateVMID(vmid); err != nil {
		return false, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/feature?feature=snapshot"
	var res featureCheck
	if err := c.do(ctx, path, &res); err != nil {
		return false, fmt.Errorf("get snapshot feature for CT %d on %s: %w", vmid, node, err)
	}
	return bool(res.HasFeature), nil
}

func (c *Client) ListCTSnapshots(ctx context.Context, node string, vmid int) ([]Snapshot, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot"
	var snaps []Snapshot
	if err := c.do(ctx, path, &snaps); err != nil {
		return nil, fmt.Errorf("list CT %d snapshots on %s: %w", vmid, node, err)
	}
	return snaps, nil
}
func (c *Client) CreateCTSnapshot(ctx context.Context, node string, vmid int, params SnapshotParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.SnapName == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	form := url.Values{}
	form.Set("snapname", params.SnapName)
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create snapshot on CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) DeleteCTSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if snapname == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("delete snapshot %s on CT %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) RollbackCTSnapshot(ctx context.Context, node string, vmid int, snapname string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if snapname == "" {
		return "", fmt.Errorf("snapshot name is required")
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/snapshot/" + url.PathEscape(snapname) + "/rollback"
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("rollback snapshot %s on CT %d on %s: %w", snapname, vmid, node, err)
	}
	return upid, nil
}
func (c *Client) CreateVM(ctx context.Context, node string, params CreateVMParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("VMID is required and must be positive")
	}
	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	if params.Name != "" {
		form.Set("name", params.Name)
	}
	if params.Memory > 0 {
		form.Set("memory", strconv.Itoa(params.Memory))
	}
	if params.Cores > 0 {
		form.Set("cores", strconv.Itoa(params.Cores))
	}
	if params.Sockets > 0 {
		form.Set("sockets", strconv.Itoa(params.Sockets))
	}
	if params.SCSI0 != "" {
		form.Set("scsi0", params.SCSI0)
	}
	if params.IDE2 != "" {
		form.Set("ide2", params.IDE2)
	}
	if params.Net0 != "" {
		form.Set("net0", params.Net0)
	}
	if params.OSType != "" {
		form.Set("ostype", params.OSType)
	}
	if params.Boot != "" {
		form.Set("boot", params.Boot)
	}
	if params.CDRom != "" {
		form.Set("cdrom", params.CDRom)
	}
	if params.Start {
		form.Set("start", "1")
	}
	if params.CIUser != "" {
		form.Set("ciuser", params.CIUser)
	}
	if params.CIPassword != "" {
		form.Set("cipassword", params.CIPassword)
	}
	if params.IPConfig0 != "" {
		form.Set("ipconfig0", params.IPConfig0)
	}
	if params.SSHKeys != "" {
		form.Set("sshkeys", url.QueryEscape(params.SSHKeys))
	}
	if params.CIType != "" {
		form.Set("citype", params.CIType)
	}
	if params.Nameserver != "" {
		form.Set("nameserver", params.Nameserver)
	}
	if params.Searchdomain != "" {
		form.Set("searchdomain", params.Searchdomain)
	}
	// System
	if params.BIOS != "" {
		form.Set("bios", params.BIOS)
	}
	if params.Machine != "" {
		form.Set("machine", params.Machine)
	}
	if params.ScsiHW != "" {
		form.Set("scsihw", params.ScsiHW)
	} else {
		form.Set("scsihw", "virtio-scsi-pci")
	}
	if params.EFIDisk0 != "" {
		form.Set("efidisk0", params.EFIDisk0)
	}
	if params.TPMState0 != "" {
		form.Set("tpmstate0", params.TPMState0)
	}
	if params.Agent != "" {
		form.Set("agent", params.Agent)
	}
	// CPU
	if params.CPUType != "" {
		form.Set("cpu", params.CPUType)
	}
	if params.Numa != nil {
		if *params.Numa {
			form.Set("numa", "1")
		} else {
			form.Set("numa", "0")
		}
	}
	// Memory
	if params.Balloon != nil {
		form.Set("balloon", strconv.Itoa(*params.Balloon))
	}
	// Display
	if params.VGA != "" {
		form.Set("vga", params.VGA)
	}
	// Boot / Options
	if params.OnBoot != nil {
		if *params.OnBoot {
			form.Set("onboot", "1")
		} else {
			form.Set("onboot", "0")
		}
	}
	if params.Hotplug != "" {
		form.Set("hotplug", params.Hotplug)
	}
	if params.Tablet != nil {
		if *params.Tablet {
			form.Set("tablet", "1")
		} else {
			form.Set("tablet", "0")
		}
	}
	// Description / Tags / Pool
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.Tags != "" {
		form.Set("tags", params.Tags)
	}
	if params.Pool != "" {
		form.Set("pool", params.Pool)
	}
	// Forward any extra fields (additional disks, CD-ROMs, etc.)
	for k, v := range params.Extra {
		if v != "" {
			form.Set(k, v)
		}
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create VM %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}
func (c *Client) CreateCT(ctx context.Context, node string, params CreateCTParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if params.VMID <= 0 {
		return "", fmt.Errorf("VMID is required and must be positive")
	}
	if params.OSTemplate == "" {
		return "", fmt.Errorf("ostemplate is required")
	}
	form := url.Values{}
	form.Set("vmid", strconv.Itoa(params.VMID))
	form.Set("ostemplate", params.OSTemplate)
	if params.Hostname != "" {
		form.Set("hostname", params.Hostname)
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}
	if params.RootFS != "" {
		form.Set("rootfs", params.RootFS)
	}
	if params.Memory > 0 {
		form.Set("memory", strconv.Itoa(params.Memory))
	}
	if params.Swap > 0 {
		form.Set("swap", strconv.Itoa(params.Swap))
	}
	if params.Cores > 0 {
		form.Set("cores", strconv.Itoa(params.Cores))
	}
	if params.Net0 != "" {
		form.Set("net0", params.Net0)
	}
	if params.Password != "" {
		form.Set("password", params.Password)
	}
	if params.SSHKeys != "" {
		form.Set("ssh-public-keys", url.QueryEscape(params.SSHKeys))
	}
	if params.Unprivileged {
		form.Set("unprivileged", "1")
	}
	if params.Start {
		form.Set("start", "1")
	}
	if params.Description != "" {
		form.Set("description", params.Description)
	}
	if params.Tags != "" {
		form.Set("tags", params.Tags)
	}
	if params.Pool != "" {
		form.Set("pool", params.Pool)
	}
	if params.Nameserver != "" {
		form.Set("nameserver", params.Nameserver)
	}
	if params.Searchdomain != "" {
		form.Set("searchdomain", params.Searchdomain)
	}
	for k, v := range params.Extra {
		if v != "" {
			form.Set(k, v)
		}
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("create CT %d on %s: %w", params.VMID, node, err)
	}
	return upid, nil
}
func (c *Client) RemoteMigrateVM(ctx context.Context, node string, vmid int, params RemoteMigrateVMParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("target-endpoint", params.TargetEndpoint.String())
	form.Set("target-bridge", params.TargetBridge)
	if params.TargetStorage != "" {
		form.Set("target-storage", params.TargetStorage)
	}
	if params.TargetVMID > 0 {
		form.Set("target-vmid", strconv.Itoa(params.TargetVMID))
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	if params.Online {
		form.Set("online", "1")
	}
	if params.Delete {
		form.Set("delete", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/remote_migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("remote migrate VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) RemoteMigrateCT(ctx context.Context, node string, vmid int, params RemoteMigrateCTParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}

	form := url.Values{}
	form.Set("target-endpoint", params.TargetEndpoint.String())
	form.Set("target-bridge", params.TargetBridge)
	if params.TargetStorage != "" {
		form.Set("target-storage", params.TargetStorage)
	}
	if params.TargetVMID > 0 {
		form.Set("target-vmid", strconv.Itoa(params.TargetVMID))
	}
	if params.BWLimit > 0 {
		form.Set("bwlimit", strconv.Itoa(params.BWLimit))
	}
	if params.Restart {
		form.Set("restart", "1")
	}
	if params.Delete {
		form.Set("delete", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/remote_migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("remote migrate CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}
func (c *Client) GetVMConfig(ctx context.Context, node string, vmid int) (VMConfig, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/config"
	var config VMConfig
	if err := c.do(ctx, path, &config); err != nil {
		return nil, fmt.Errorf("get VM %d config on %s: %w", vmid, node, err)
	}
	return config, nil
}
func (c *Client) GetGuestAgentOSInfo(ctx context.Context, node string, vmid int) (*GuestOSInfo, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/get-osinfo"
	var wrapper struct {
		Result GuestOSInfo `json:"result"`
	}
	if err := c.do(ctx, path, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get guest agent OS info for VM %d on %s: %w", vmid, node, err)
	}
	return &wrapper.Result, nil
}
func (c *Client) GetGuestAgentNetworkInterfaces(ctx context.Context, node string, vmid int) ([]GuestNetworkInterface, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/network-get-interfaces"
	var wrapper struct {
		Result []GuestNetworkInterface `json:"result"`
	}
	if err := c.do(ctx, path, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get guest agent network interfaces for VM %d on %s: %w", vmid, node, err)
	}
	return wrapper.Result, nil
}

// maxGuestFileWriteBytes bounds a single agent/file-write. Proxmox validates
// the content parameter at 61440 characters, so anything larger is rejected by
// the API rather than by the guest. Callers that need more must chunk; nothing
// here does, because the only thing written into a guest is a short script.
const maxGuestFileWriteBytes = 60 * 1024

// GuestAgentExec starts a command inside the guest via the QEMU guest agent and
// returns its PID, to be polled with GuestAgentExecStatus.
//
// Returns (0, nil) when the agent is not running, matching the other agent
// helpers: an absent agent is a state, not a failure.
//
// The PID is deliberately an int rather than a string. Proxmox returns a
// process id here, not a UPID, and typing it as a string would both invite that
// confusion and drag the method into the UPID/TrackTask guard list.
//
// IMPORTANT: whatever is started here is a child of the guest agent. A command
// that restarts the agent's own service — which installing virtio-win guest
// tools does — kills the process tree and loses the PID table with it. Run such
// things detached (a scheduled task), not directly through this.
func (c *Client) GuestAgentExec(ctx context.Context, node string, vmid int, command []string) (int, error) {
	if err := validateNodeName(node); err != nil {
		return 0, err
	}
	if err := validateVMID(vmid); err != nil {
		return 0, err
	}
	if len(command) == 0 {
		return 0, fmt.Errorf("command is required")
	}

	form := url.Values{}
	// Proxmox takes the command as a repeated parameter, one element per
	// argument — NOT a single shell string. Passing it as one value makes the
	// agent try to execute the whole line as a program name.
	for _, arg := range command {
		form.Add("command", arg)
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/exec"
	var wrapper struct {
		PID int `json:"pid"`
	}
	if err := c.doPost(ctx, path, form, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("guest agent exec on VM %d on %s: %w", vmid, node, err)
	}
	return wrapper.PID, nil
}

// GuestAgentExecStatus polls a command started by GuestAgentExec.
// Returns (nil, nil) when the agent is not running.
func (c *Client) GuestAgentExecStatus(ctx context.Context, node string, vmid, pid int) (*GuestExecStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	if pid <= 0 {
		return nil, fmt.Errorf("pid must be positive")
	}

	q := url.Values{}
	q.Set("pid", strconv.Itoa(pid))
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) +
		"/agent/exec-status?" + q.Encode()

	var status GuestExecStatus
	if err := c.do(ctx, path, &status); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("guest agent exec-status for pid %d on VM %d on %s: %w", pid, vmid, node, err)
	}
	return &status, nil
}

// GuestAgentExecWait runs a command in the guest and blocks until it exits,
// returning its final status. It is GuestAgentExec followed by a poll of
// GuestAgentExecStatus every poll interval until the command exits or timeout
// elapses.
//
// A command that exits non-zero returns both the status and an error naming the
// exit code and whatever the guest wrote — stderr if there is any, stdout
// otherwise, since PowerShell routinely reports failures on stdout.
//
// ErrGuestAgentUnavailable comes back unwrapped when the agent is not running,
// either at exec time (pid 0) or when it disappears mid-poll, so callers can
// errors.Is it without unwrapping. The same caveat as GuestAgentExec applies:
// a command that restarts the agent's own service loses its PID table, and must
// be run detached rather than waited on here.
func (c *Client) GuestAgentExecWait(ctx context.Context, node string, vmid int, argv []string, timeout, poll time.Duration) (*GuestExecStatus, error) {
	pid, err := c.GuestAgentExec(ctx, node, vmid, argv)
	if err != nil {
		return nil, err
	}
	if pid == 0 {
		return nil, ErrGuestAgentUnavailable
	}

	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("guest %d: %s did not finish within %s", vmid, argv[0], timeout)
		}
		status, err := c.GuestAgentExecStatus(ctx, node, vmid, pid)
		if err != nil {
			return nil, err
		}
		if status == nil {
			return nil, ErrGuestAgentUnavailable
		}
		if status.Exited {
			if status.ExitCode != 0 {
				return status, fmt.Errorf("guest %d: %s exited %d: %s", vmid, argv[0], int(status.ExitCode),
					cmp.Or(strings.TrimSpace(status.ErrData), strings.TrimSpace(status.OutData)))
			}
			return status, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// GuestAgentFileWrite writes content to a file inside the guest.
//
// Proxmox base64-encodes the content on our behalf (encode=1), so callers pass
// plain bytes. The size cap is the API's, not the agent's.
func (c *Client) GuestAgentFileWrite(ctx context.Context, node string, vmid int, file string, content []byte) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	if file == "" {
		return fmt.Errorf("file path is required")
	}
	if len(content) > maxGuestFileWriteBytes {
		return fmt.Errorf("content is %d bytes, over the %d-byte limit Proxmox accepts for a single write",
			len(content), maxGuestFileWriteBytes)
	}

	form := url.Values{}
	form.Set("file", file)
	form.Set("content", string(content))
	form.Set("encode", "1")

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/agent/file-write"
	if err := c.doPost(ctx, path, form, nil); err != nil {
		if isAgentNotRunning(err) {
			return ErrGuestAgentUnavailable
		}
		return fmt.Errorf("guest agent file-write %s on VM %d on %s: %w", file, vmid, node, err)
	}
	return nil
}

// GuestAgentFileRead reads a file from inside the guest.
// Returns (nil, nil) when the agent is not running.
//
// Proxmox decodes the agent's base64 for us (decode=1). A file larger than the
// agent's own limit comes back truncated, which is surfaced as an error rather
// than silently handing back a partial file — callers here parse JSON, and half
// a JSON document is worse than none.
func (c *Client) GuestAgentFileRead(ctx context.Context, node string, vmid int, file string) ([]byte, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	if file == "" {
		return nil, fmt.Errorf("file path is required")
	}

	q := url.Values{}
	q.Set("file", file)
	q.Set("decode", "1")
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) +
		"/agent/file-read?" + q.Encode()

	var wrapper struct {
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	}
	if err := c.do(ctx, path, &wrapper); err != nil {
		if isAgentNotRunning(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("guest agent file-read %s on VM %d on %s: %w", file, vmid, node, err)
	}
	if wrapper.Truncated {
		return nil, fmt.Errorf("guest agent file-read %s on VM %d on %s: file was truncated", file, vmid, node)
	}
	return []byte(wrapper.Content), nil
}

func (c *Client) GetCTConfig(ctx context.Context, node string, vmid int) (VMConfig, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/config"
	var config VMConfig
	if err := c.do(ctx, path, &config); err != nil {
		return nil, fmt.Errorf("get CT %d config on %s: %w", vmid, node, err)
	}
	return config, nil
}

// SetVMConfig applies configuration changes to a VM and returns once Proxmox
// has actually applied them.
//
// PUT is Proxmox's synchronous config API. POST on the same path is the
// asynchronous one: it forks a worker and answers with a task UPID, so it
// returns the moment the work is QUEUED. Both verbs run the identical update,
// hotplug included — the only difference is whether the change has happened
// when the call returns.
//
// Every caller here needs it to have happened: they go straight on to act on
// the new config — mount the disc that was just attached, record the media
// change as done. A queued worker gives them none of that, and its failure
// would never reach them.
//
// qemu-server's own PUT description does steer callers to POST "for any
// actions involving hotplug or storage allocation", and that advice is about
// duration, not behaviour: the sync verb runs the whole update inside the HTTP
// request, so a slow allocation can outlive it. Nexara accepts that. A cached
// client allows 5 minutes (proxmox.CachedClientTimeout), which is far past any
// config write that is going to succeed, and every other config-writing method
// here already waits the same way. Trading a bounded wait for an answer the
// caller can act on is the right side of that deal.
//
// If an asynchronous variant is genuinely needed, it belongs in a separate
// method returning (string, error) whose UPID the handler records via
// TrackTask; upid_signature_guard_test.go enforces that shape for a POST to
// this path.
func (c *Client) SetVMConfig(ctx context.Context, node string, vmid int, fields map[string]string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/config"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("set VM %d config on %s: %w", vmid, node, err)
	}
	return nil
}
func (c *Client) SetContainerConfig(ctx context.Context, node string, vmid int, fields map[string]string) error {
	if err := validateNodeName(node); err != nil {
		return err
	}
	if err := validateVMID(vmid); err != nil {
		return err
	}
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/config"
	if err := c.doPut(ctx, path, form, nil); err != nil {
		return fmt.Errorf("set container %d config on %s: %w", vmid, node, err)
	}
	return nil
}
