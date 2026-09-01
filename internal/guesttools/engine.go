package guesttools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/virtiowin"
)

// Engine detects installed guest tools and stages updates for Windows guests.
type Engine struct {
	queries       *db.Queries
	encryptionKey string
	cache         *proxmox.ClientCache // nil-safe
	logger        *slog.Logger
}

// NewEngine builds the guest tools engine.
func NewEngine(queries *db.Queries, encryptionKey string, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{queries: queries, encryptionKey: encryptionKey, logger: logger}
}

// SetProxmoxCache attaches the shared per-server client cache. Nil-safe.
func (e *Engine) SetProxmoxCache(cache *proxmox.ClientCache) { e.cache = cache }

var (
	// ErrNoTargetVersion means no version could be resolved for a guest.
	ErrNoTargetVersion = errors.New("guesttools: no target version resolved")
	// ErrISOUnavailable means the target ISO is not on the cluster's storage.
	ErrISOUnavailable = errors.New("guesttools: target virtio-win ISO is not available on the configured storage")
	// ErrNoCDROMSlot means the guest has no drive the ISO can be attached to.
	ErrNoCDROMSlot = errors.New("guesttools: guest has no free CD-ROM drive for the virtio-win ISO")
	// ErrNotEligible means the guest is out of scope (excluded, not Windows,
	// not running, no agent, or already at the target).
	ErrNotEligible = errors.New("guesttools: guest is not eligible for a staged update")
)

// exec timeouts. Detection is a registry read; staging writes a file and
// registers a task. Neither should take long, and a guest that hangs must not
// hold a scheduler tick open.
const (
	guestExecTimeout   = 90 * time.Second
	guestExecPollEvery = 2 * time.Second

	// snapshotWaitTimeout bounds waiting for a pre-update snapshot. Without
	// vmstate a snapshot is fast even on large disks; this is the "something is
	// wrong" ceiling, not an expected duration.
	snapshotWaitTimeout = 10 * time.Minute
)

// Target is the version a guest should be brought to.
type Target struct {
	Version     string // upstream directory version, "0.1.302-1"
	ISOVersion  string // ISO-filename form, "0.1.302"
	ISOFilename string
}

// ResolveTarget determines the version a guest should hold, most specific
// first: the guest's own pin, then the cluster's guest-tools pin, then the
// cluster's virtio-win ISO pin, then upstream stable.
//
// Each layer is an operator statement of intent, and a more specific one always
// wins — including over a newer version. Pinning that silently drifts forward
// is not pinning.
func (e *Engine) ResolveTarget(ctx context.Context, clusterID uuid.UUID, cfg db.GuestToolsConfig, policy *db.GuestToolsPolicy) (Target, error) {
	candidates := []string{}
	if policy != nil && policy.TargetVersion != "" {
		candidates = append(candidates, policy.TargetVersion)
	}
	if cfg.TargetVersion != "" {
		candidates = append(candidates, cfg.TargetVersion)
	}
	if vwCfg, err := e.queries.GetVirtioWinConfig(ctx, clusterID); err == nil && vwCfg.TargetVersion != "" {
		candidates = append(candidates, vwCfg.TargetVersion)
	}

	// Only the most specific pin matters, and candidates is already ordered
	// most-specific-first. A pin naming a version we have never seen is a
	// misconfiguration to report, NOT a reason to quietly fall back to a
	// broader pin — that would install a version nobody asked for.
	if len(candidates) > 0 {
		version := candidates[0]
		release, err := e.queries.GetVirtioWinRelease(ctx, version)
		switch {
		case err == nil:
			return Target{Version: release.Version, ISOVersion: release.IsoVersion, ISOFilename: release.IsoFilename}, nil
		case errors.Is(err, pgx.ErrNoRows):
			return Target{}, fmt.Errorf("%w: pinned version %q is not in the catalog", ErrNoTargetVersion, version)
		default:
			return Target{}, fmt.Errorf("look up pinned release %q: %w", version, err)
		}
	}

	stable, err := e.queries.GetStableVirtioWinRelease(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Target{}, ErrNoTargetVersion
		}
		return Target{}, fmt.Errorf("get stable release: %w", err)
	}
	return Target{Version: stable.Version, ISOVersion: stable.IsoVersion, ISOFilename: stable.IsoFilename}, nil
}

// Detect probes one guest for its installed guest tools and records the result.
//
// Returns the detection, or a zero Detection when the agent is unreachable —
// that is a normal state for a stopped or agent-less guest, not an error.
func (e *Engine) Detect(ctx context.Context, client *proxmox.Client, clusterID uuid.UUID, node string, vmid int, uptime int64) (Detection, error) {
	out, err := e.runScript(ctx, client, node, vmid, detectScript)
	if err != nil {
		return Detection{}, err
	}
	detection, err := ParseDetection([]byte(out))
	if err != nil {
		return Detection{}, err
	}

	if err := e.queries.UpsertGuestToolsDetection(ctx, db.UpsertGuestToolsDetectionParams{
		ClusterID:        clusterID,
		Vmid:             int32(vmid), //nolint:gosec // vmid is bounded by Proxmox at 999999999
		InstalledVersion: detection.InstalledVersion(),
		AgentVersion:     detection.Agent,
		AgentRunning:     detection.AgentRunning(),
		LastUptime:       uptime,
	}); err != nil {
		return detection, fmt.Errorf("record detection for guest %d: %w", vmid, err)
	}
	return detection, nil
}

// runScript executes a PowerShell script in the guest and returns its stdout.
//
// The command is passed as argv, not a shell line: Proxmox takes `command` as a
// repeated parameter and would otherwise treat the whole string as a program
// name.
func (e *Engine) runScript(ctx context.Context, client *proxmox.Client, node string, vmid int, script string) (string, error) {
	pid, err := client.GuestAgentExec(ctx, node, vmid,
		[]string{"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script}, "")
	if err != nil {
		return "", fmt.Errorf("exec in guest %d: %w", vmid, err)
	}
	if pid == 0 {
		return "", proxmox.ErrGuestAgentUnavailable
	}

	deadline := time.Now().Add(guestExecTimeout)
	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("guest %d: command did not finish within %s", vmid, guestExecTimeout)
		}
		status, err := client.GuestAgentExecStatus(ctx, node, vmid, pid)
		if err != nil {
			return "", fmt.Errorf("exec-status in guest %d: %w", vmid, err)
		}
		if status == nil {
			return "", proxmox.ErrGuestAgentUnavailable
		}
		if !status.Exited {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(guestExecPollEvery):
			}
			continue
		}
		if status.ExitCode != 0 {
			return "", fmt.Errorf("guest %d: command exited %d: %s",
				vmid, int(status.ExitCode), firstNonEmpty(strings.TrimSpace(status.ErrData), strings.TrimSpace(status.OutData)))
		}
		return strings.TrimSpace(status.OutData), nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// cdromSlots are the device keys a CD-ROM may occupy, in the order Proxmox's
// own UI offers them.
var cdromSlots = []string{"ide2", "ide0", "ide1", "ide3",
	"sata0", "sata1", "sata2", "sata3", "sata4", "sata5"}

// Sentinel values stored in guest_tools_state.prior_cdrom_value, describing what
// to do with the drive once the update finishes. Neither can collide with a real
// Proxmox volume id, which always contains a colon.
const (
	// cdromEject empties a drive that already existed, leaving the drive itself.
	cdromEject = "none"
	// cdromRemove deletes a drive that Nexara added, so the guest ends up with
	// the hardware it started with.
	cdromRemove = "remove"
)

// cdromPlacement is where the virtio-win ISO will be attached, and what was
// there before.
type cdromPlacement struct {
	Key string
	// PriorValue is what to restore when the update finishes. Empty means
	// nothing needs restoring: either the slot was free, or it already held a
	// virtio-win ISO that this one supersedes.
	PriorValue string
	// Eject is set when the slot was free and should be emptied afterwards.
	Eject bool
	// AlreadyAttached is set when this drive already holds the exact target
	// ISO, so the media change can be skipped.
	AlreadyAttached bool
}

// planCDROM decides which drive to attach the ISO to.
//
// Preference order, and the reasoning for each:
//  1. A drive that already holds a virtio-win ISO. Replacing it is an upgrade
//     of the same thing, and nothing is restored afterwards — putting a
//     superseded ISO back would be actively worse, especially once pruning has
//     removed it.
//  2. A free drive slot, ejected afterwards so the guest is left as found.
//  3. Nothing. Borrowing a drive that holds the operator's install media is
//     not worth the failure mode where restore never runs.
func planCDROM(config proxmox.VMConfig, isoVolid string) (cdromPlacement, error) {
	// Deterministic iteration: ranging a Go map picks an arbitrary drive on a
	// guest with two, which is exactly the test guest's shape.
	keys := make([]string, 0, len(config))
	for k := range config {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value, ok := config[key].(string)
		if !ok || !strings.Contains(value, "media=cdrom") {
			continue
		}
		if volid, _, _ := strings.Cut(value, ","); isVirtioWinVolid(volid) {
			// Reuse this drive whether it already holds the target ISO or a
			// superseded one. AlreadyAttached lets the caller skip a pointless
			// media change; nothing is restored either way, because putting a
			// superseded virtio-win ISO back would be worse than leaving the
			// new one.
			return cdromPlacement{Key: key, AlreadyAttached: volid == isoVolid}, nil
		}
	}

	for _, slot := range cdromSlots {
		if _, occupied := config[slot]; !occupied {
			return cdromPlacement{Key: slot, Eject: true}, nil
		}
	}
	return cdromPlacement{}, ErrNoCDROMSlot
}

// isVirtioWinVolid reports whether a volume id points at a virtio-win ISO.
func isVirtioWinVolid(volid string) bool {
	name := volid
	if i := strings.LastIndex(volid, "/"); i >= 0 {
		name = volid[i+1:]
	}
	if !strings.HasPrefix(name, "virtio-win-") || !strings.HasSuffix(name, ".iso") {
		return false
	}
	return virtiowin.ValidVersion(strings.TrimSuffix(strings.TrimPrefix(name, "virtio-win-"), ".iso"))
}

// restoreActionFor decides what should happen to the borrowed drive once the
// update finishes, recorded up front so a failure mid-stage cannot lose it.
//
// The virtio-win ISO always comes back off. It was mounted to install from and
// is useless afterwards, and leaving it attached would pin that ISO forever
// against pruning: prune deliberately skips media a guest still has mounted, so
// every updated guest would block cleanup of the release it just moved off.
func restoreActionFor(placement cdromPlacement, config proxmox.VMConfig) string {
	if placement.Eject {
		// Nexara added this drive, so take the whole drive away rather than
		// leaving behind an empty one the guest never had.
		return cdromRemove
	}
	if existing, ok := config[placement.Key].(string); ok && !isVirtioWinVolid(firstField(existing)) {
		// Defensive: planCDROM does not hand back an occupied non-virtio drive
		// today, but if that ever changes, put the operator's own media back.
		return existing
	}
	// A drive that already existed and held a virtio-win ISO: empty it, but
	// leave the drive itself, because the guest had it before.
	return cdromEject
}

// StageResult describes what staging did to a guest.
type StageResult struct {
	VMID       int
	Target     Target
	CDROMKey string
	RanNow   bool
	// SnapshotName and SnapshotUPID are set only when a snapshot was taken,
	// which happens on the immediate path only. The UPID is returned rather
	// than recorded here so the calling handler can TrackTask it, keeping task
	// bookkeeping in the one place that owns it.
	SnapshotName string
	SnapshotUPID string
}

// Stage prepares a guest to install the target version: attaches the ISO,
// writes the updater, and registers it to run at next boot.
//
// runNow additionally starts the task immediately. The task is started through
// the Task Scheduler rather than executed here on purpose — the installer
// restarts QEMU-GA, which would kill anything running as a child of the agent.
func (e *Engine) Stage(
	ctx context.Context,
	client *proxmox.Client,
	clusterID uuid.UUID,
	cfg db.GuestToolsConfig,
	node string,
	vmid int,
	target Target,
	storage string,
	runNow bool,
) (StageResult, error) {
	result := StageResult{VMID: vmid, Target: target}

	isoVolid := storage + ":iso/" + target.ISOFilename
	present, err := e.isoOnStorage(ctx, client, node, storage, target.ISOFilename)
	if err != nil {
		return result, fmt.Errorf("check ISO on %s: %w", storage, err)
	}
	if !present {
		return result, fmt.Errorf("%w: %s is not on %s", ErrISOUnavailable, target.ISOFilename, storage)
	}

	config, err := client.GetVMConfig(ctx, node, vmid)
	if err != nil {
		return result, fmt.Errorf("read config for guest %d: %w", vmid, err)
	}
	placement, err := planCDROM(config, isoVolid)
	if err != nil {
		return result, err
	}
	result.CDROMKey = placement.Key

	// Snapshot only on the immediate path, and this asymmetry is deliberate.
	//
	// A staged update runs at the guest's NEXT BOOT, which may be days away. A
	// snapshot taken now would be days stale by then, so rolling back after a
	// bad install would discard everything since staging — the protection would
	// cost more than the problem. There is no way to snapshot from inside the
	// guest at the moment the installer actually runs, so for staged updates
	// the honest answer is not to pretend.
	if cfg.SnapshotBefore && runNow {
		name := snapshotName(target.ISOVersion)
		upid, err := client.CreateVMSnapshot(ctx, node, vmid, proxmox.SnapshotParams{
			SnapName:    name,
			Description: "Before Nexara guest tools update to " + target.Version,
		})
		if err != nil {
			return result, fmt.Errorf("snapshot before update: %w", err)
		}
		result.SnapshotName = name
		result.SnapshotUPID = upid
		// Wait for it. Starting an installer while the snapshot is still being
		// written would capture a half-updated guest, which is the one state
		// the snapshot exists to avoid.
		if err := e.waitForTask(ctx, client, node, upid, snapshotWaitTimeout); err != nil {
			return result, fmt.Errorf("snapshot before update: %w", err)
		}
	}

	// Record the prior media BEFORE mutating anything, so a failure between
	// here and the config write cannot lose it.
	// Decide now what "done" should leave behind, and record it before mutating
	// anything so a failure in between cannot lose it.
	//
	// The ISO is always taken back off the guest once the update finishes. It
	// was mounted to install from and serves no purpose afterwards, and leaving
	// it attached would pin that ISO forever against pruning — prune skips any
	// media a guest still has mounted, so every updated guest would block
	// cleanup of the very release it just moved off.
	priorValue := restoreActionFor(placement, config)
	if err := e.queries.SetGuestToolsStage(ctx, db.SetGuestToolsStageParams{
		ClusterID:       clusterID,
		Vmid:            int32(vmid), //nolint:gosec // bounded by Proxmox
		Stage:           "staging",
		StagedVersion:   target.Version,
		PriorCdromKey:   placement.Key,
		PriorCdromValue: priorValue,
	}); err != nil {
		return result, fmt.Errorf("record staging state: %w", err)
	}

	// POST, not PUT: the sync variant hot-plugs the media change so the guest
	// sees the disc without a reboot. Skipped when the drive already holds the
	// exact ISO — re-attaching identical media yanks the disc out from under
	// anything reading it, for no gain.
	if !placement.AlreadyAttached {
		if err := client.UpdateVMConfigSync(ctx, node, vmid, map[string]string{
			placement.Key: isoVolid + ",media=cdrom",
		}); err != nil {
			return result, fmt.Errorf("attach %s to %s: %w", target.ISOFilename, placement.Key, err)
		}
	}

	// Clear any result from a previous run BEFORE registering the task.
	// The file is version-stamped and the reconciler checks that stamp, but a
	// retry of the SAME version would otherwise match the old file and be
	// resolved from it instantly — reporting the previous attempt's outcome for
	// an install that has not happened yet.
	if err := e.clearResultFile(ctx, client, node, vmid); err != nil {
		return result, fmt.Errorf("clear previous updater result in guest %d: %w", vmid, err)
	}

	script := BuildInstallScript(target.Version, ISOVolumeLabel(target.ISOVersion))
	if i := NonASCIIAt(script); i != -1 {
		// Proxmox's file-write dies on wide characters with a Perl error that
		// says nothing useful; fail here with something actionable instead.
		return result, fmt.Errorf("generated updater contains a non-ASCII byte at offset %d, which Proxmox cannot write into a guest", i)
	}
	if err := client.GuestAgentFileWrite(ctx, node, vmid, GuestScriptPath, []byte(script)); err != nil {
		return result, fmt.Errorf("write updater into guest %d: %w", vmid, err)
	}

	if _, err := e.runScript(ctx, client, node, vmid, buildRegisterTaskScript(GuestScriptPath)); err != nil {
		return result, fmt.Errorf("register scheduled task in guest %d: %w", vmid, err)
	}

	stage := "staged"
	if runNow {
		if err := e.runArgv(ctx, client, node, vmid, buildRunTaskCommand()); err != nil {
			return result, fmt.Errorf("start scheduled task in guest %d: %w", vmid, err)
		}
		stage = "running"
		result.RanNow = true
	}

	if err := e.queries.SetGuestToolsStage(ctx, db.SetGuestToolsStageParams{
		ClusterID:       clusterID,
		Vmid:            int32(vmid), //nolint:gosec // bounded by Proxmox
		Stage:           stage,
		StagedVersion:   target.Version,
		PriorCdromKey:   placement.Key,
		PriorCdromValue: priorValue,
	}); err != nil {
		return result, fmt.Errorf("record staged state: %w", err)
	}
	return result, nil
}

// snapshotName builds a Proxmox-legal snapshot name. Proxmox rejects dots in
// snapshot names, so the version's separators become dashes.
func snapshotName(isoVersion string) string {
	return "nexara-guesttools-" + strings.ReplaceAll(isoVersion, ".", "-")
}

// waitForTask blocks until a Proxmox task finishes, or the timeout elapses.
func (e *Engine) waitForTask(ctx context.Context, client *proxmox.Client, node, upid string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("task %s did not finish within %s", upid, timeout)
		}
		status, err := client.GetTaskStatus(ctx, node, upid)
		if err != nil {
			return fmt.Errorf("poll task %s: %w", upid, err)
		}
		if status.Status == "stopped" {
			if proxmox.TaskSucceeded(status.ExitStatus) {
				return nil
			}
			return fmt.Errorf("task %s failed: %s", upid, status.ExitStatus)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(guestExecPollEvery):
		}
	}
}

func firstField(value string) string {
	v, _, _ := strings.Cut(value, ",")
	return v
}

// runArgv runs a command in the guest, for commands that are already argv
// rather than a PowerShell script. Output is folded into the error on failure;
// nothing here reads stdout on success.
func (e *Engine) runArgv(ctx context.Context, client *proxmox.Client, node string, vmid int, argv []string) error {
	pid, err := client.GuestAgentExec(ctx, node, vmid, argv, "")
	if err != nil {
		return err
	}
	if pid == 0 {
		return proxmox.ErrGuestAgentUnavailable
	}
	deadline := time.Now().Add(guestExecTimeout)
	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("guest %d: %s did not finish within %s", vmid, argv[0], guestExecTimeout)
		}
		status, err := client.GuestAgentExecStatus(ctx, node, vmid, pid)
		if err != nil {
			return err
		}
		if status == nil {
			return proxmox.ErrGuestAgentUnavailable
		}
		if !status.Exited {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(guestExecPollEvery):
			}
			continue
		}
		if status.ExitCode != 0 {
			return fmt.Errorf("guest %d: %s exited %d: %s", vmid, argv[0], int(status.ExitCode),
				firstNonEmpty(strings.TrimSpace(status.ErrData), strings.TrimSpace(status.OutData)))
		}
		return nil
	}
}

func (e *Engine) isoOnStorage(ctx context.Context, client *proxmox.Client, node, storage, filename string) (bool, error) {
	items, err := client.GetStorageContentByType(ctx, node, storage, "iso")
	if err != nil {
		return false, err
	}
	for _, item := range items {
		name := item.Volid
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		if name == filename {
			return true, nil
		}
	}
	return false, nil
}

// clearResultFile removes a previous run's result from the guest, and VERIFIES
// the file is gone rather than trusting an exit code.
//
// This was originally `cmd.exe /c del /f /q "<path>" & exit 0`, which on a live
// guest returned success and left the file in place: cmd's quote handling broke
// the path, and the trailing `& exit 0` — added so a missing file would not read
// as failure — masked the real one. A delete that silently does nothing is the
// worst shape here, because a stale result resolves the next attempt instantly.
//
// PowerShell's Remove-Item takes the path literally, and the Test-Path check
// afterwards means the caller learns the truth either way.
func (e *Engine) clearResultFile(ctx context.Context, client *proxmox.Client, node string, vmid int) error {
	out, err := e.runScript(ctx, client, node, vmid,
		`Remove-Item -LiteralPath '`+GuestResultPath+`' -Force -ErrorAction SilentlyContinue
if (Test-Path -LiteralPath '`+GuestResultPath+`') { 'FAILED' } else { 'OK' }`)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "OK" {
		return fmt.Errorf("guest %d: %s still present after delete", vmid, GuestResultPath)
	}
	return nil
}

// ReadResult fetches and parses the updater's result file from a guest.
// Returns (nil, nil) when the file is not there yet.
func (e *Engine) ReadResult(ctx context.Context, client *proxmox.Client, node string, vmid int) (*GuestUpdateResult, error) {
	raw, err := client.GuestAgentFileRead(ctx, node, vmid, GuestResultPath)
	if err != nil {
		// Not-yet-written reads as a Proxmox error; treat it as "no result".
		if strings.Contains(strings.ToLower(err.Error()), "no such file") ||
			strings.Contains(strings.ToLower(err.Error()), "cannot find") ||
			strings.Contains(strings.ToLower(err.Error()), "failed to open") {
			return nil, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var result GuestUpdateResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unparseable updater result in guest %d: %w", vmid, err)
	}
	return &result, nil
}

// RestoreCDROM returns the borrowed drive to the state the guest had before,
// which always means the virtio-win ISO comes back off.
func (e *Engine) RestoreCDROM(ctx context.Context, client *proxmox.Client, node string, vmid int, key, priorValue string) error {
	if key == "" || priorValue == "" {
		return nil // nothing recorded, nothing to undo
	}

	var fields map[string]string
	switch priorValue {
	case cdromRemove:
		// Proxmox removes a device via the `delete` parameter rather than by
		// assigning it an empty value.
		fields = map[string]string{"delete": key}
	case cdromEject:
		fields = map[string]string{key: "none,media=cdrom"}
	default:
		fields = map[string]string{key: priorValue}
	}

	if err := client.UpdateVMConfigSync(ctx, node, vmid, fields); err != nil {
		return fmt.Errorf("restore %s on guest %d: %w", key, vmid, err)
	}
	return nil
}

// CancelStaged removes a staged update from a guest that has not run it yet.
func (e *Engine) CancelStaged(ctx context.Context, client *proxmox.Client, clusterID uuid.UUID, node string, vmid int, state db.GuestToolsState) error {
	// Best-effort: a guest that has already rebooted and run the task no longer
	// has one to delete, and that is not a failure to cancel.
	if err := e.runArgv(ctx, client, node, vmid, buildDeleteTaskCommand()); err != nil {
		e.logger.Debug("guest tools: delete scheduled task failed during cancel",
			"vmid", vmid, "error", err)
	}
	// Same rule as the reconcile path: only forget which drive to put back once
	// it has actually been put back, so a failure here leaves a record for
	// retryPendingCDROMRestores instead of stranding the ISO on the guest.
	cdromRestored := true
	if err := e.RestoreCDROM(ctx, client, node, vmid, state.PriorCdromKey, state.PriorCdromValue); err != nil {
		cdromRestored = false
		e.logger.Warn("guest tools: could not restore CD-ROM during cancel, will retry",
			"vmid", vmid, "error", err)
	}
	return e.queries.FinishGuestToolsUpdate(ctx, db.FinishGuestToolsUpdateParams{
		ClusterID:      clusterID,
		Vmid:           int32(vmid), //nolint:gosec // bounded by Proxmox
		Stage:          "idle",
		LastError:      "",
		RebootRequired: false,
		CdromRestored:  cdromRestored,
	})
}

// CreateClient returns a Proxmox client for a cluster.
func (e *Engine) CreateClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
	if e.cache != nil {
		client, err := e.cache.Get(ctx, clusterID)
		if err == nil {
			return client, nil
		}
		e.logger.Warn("guest tools: proxmox cache get failed, building per-call",
			"cluster_id", clusterID, "error", err)
	}
	cluster, err := e.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("get cluster %s: %w", clusterID, err)
	}
	secret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, e.encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt token: %w", err)
	}
	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    secret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        60 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}
	return client, nil
}
