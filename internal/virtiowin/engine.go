package virtiowin

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// Engine owns the periodic side of virtio-win ISO acquisition: refreshing the
// upstream catalog, dispatching downloads for clusters that opted in, and
// reconciling the Proxmox tasks those downloads become.
//
// It deliberately never streams the ISO itself. download-url makes the Proxmox
// node fetch the ~837 MiB directly, so nothing large crosses the Nexara
// container, and the transfer survives a Nexara restart because it is a
// Proxmox task with a UPID we can re-poll.
type Engine struct {
	queries       *db.Queries
	encryptionKey string
	cache         *proxmox.ClientCache // nil-safe; falls back to per-call construction
	logger        *slog.Logger
	upstream      *Client
}

// NewEngine builds the virtio-win engine.
func NewEngine(queries *db.Queries, encryptionKey string, logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		queries:       queries,
		encryptionKey: encryptionKey,
		logger:        logger,
		upstream:      NewClient(logger.With("component", "virtio-win-client")),
	}
}

// SetProxmoxCache attaches the shared per-server client cache. Nil-safe.
func (e *Engine) SetProxmoxCache(cache *proxmox.ClientCache) { e.cache = cache }

// RefreshCatalog fetches the current upstream state and persists it, returning
// the release upstream considers current.
//
// The archive sweep is best-effort: it exists to give the UI a version list to
// pin against and to give prune a keep-set, and a failure there must not stop
// the stable release from being recorded.
func (e *Engine) RefreshCatalog(ctx context.Context) (Release, error) {
	latest, err := e.upstream.CheckLatest(ctx)
	if err != nil {
		return Release{}, fmt.Errorf("virtio-win: resolve latest: %w", err)
	}

	if archive, err := e.upstream.ListArchive(ctx); err != nil {
		e.logger.Warn("virtio-win: archive sweep failed; catalog limited to the current release", "error", err)
	} else {
		for _, rel := range archive {
			if rel.Version == latest.Version {
				continue
			}
			if _, err := e.storeRelease(ctx, rel); err != nil {
				e.logger.Warn("virtio-win: store archive release failed", "version", rel.Version, "error", err)
			}
		}
	}

	// Probe the size for display. Not fatal — a failed HEAD says nothing about
	// whether the node can fetch it, since the node dials out independently.
	if size, err := e.upstream.ProbeSize(ctx, latest.ISOURL); err == nil {
		latest.ISOSize = size
	} else {
		e.logger.Debug("virtio-win: ISO size probe failed", "version", latest.Version, "error", err)
	}

	if _, err := e.storeRelease(ctx, latest); err != nil {
		return Release{}, fmt.Errorf("virtio-win: store release %s: %w", latest.Version, err)
	}
	if latest.IsStable {
		// Clear first so the partial index on is_stable only ever matches one row.
		if err := e.queries.ClearVirtioWinStableFlag(ctx, latest.Version); err != nil {
			e.logger.Warn("virtio-win: clear previous stable flag failed", "error", err)
		}
	}
	return latest, nil
}

func (e *Engine) storeRelease(ctx context.Context, rel Release) (db.VirtioWinRelease, error) {
	return e.queries.UpsertVirtioWinRelease(ctx, db.UpsertVirtioWinReleaseParams{
		Version:     rel.Version,
		IsoVersion:  rel.ISOVersion,
		IsoFilename: rel.ISOFilename,
		IsoUrl:      rel.ISOURL,
		IsoSize:     rel.ISOSize,
		IsStable:    rel.IsStable,
	})
}

// ErrNoTarget reports that a cluster's effective target version could not be
// resolved — no pin and no known stable release.
var ErrNoTarget = errors.New("virtio-win: no target version available")

// ErrDownloadInFlight reports that this exact download is already running.
// Callers should treat it as success: the requested state is being reached.
var ErrDownloadInFlight = errors.New("virtio-win: this download is already in progress")

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ResolveTarget returns the version a cluster should hold.
//
// An explicit pin always wins, including over a newer stable release: pinning
// is the operator saying "this version, until I say otherwise", and silently
// moving past it would defeat the point. With no pin, follow upstream stable.
func (e *Engine) ResolveTarget(ctx context.Context, cfg db.VirtioWinConfig) (db.VirtioWinRelease, error) {
	if cfg.TargetVersion != "" {
		rel, err := e.queries.GetVirtioWinRelease(ctx, cfg.TargetVersion)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return db.VirtioWinRelease{}, fmt.Errorf("%w: pinned version %q is not in the catalog", ErrNoTarget, cfg.TargetVersion)
			}
			return db.VirtioWinRelease{}, fmt.Errorf("get pinned release %q: %w", cfg.TargetVersion, err)
		}
		return rel, nil
	}
	rel, err := e.queries.GetStableVirtioWinRelease(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.VirtioWinRelease{}, ErrNoTarget
		}
		return db.VirtioWinRelease{}, fmt.Errorf("get stable release: %w", err)
	}
	return rel, nil
}

// EnsureCatalog populates the release catalog when it is empty.
//
// The scheduler only reaches upstream once a cluster has opted in, which keeps
// installs that never use this feature from phoning home — but it also means
// the catalog is empty at the moment an operator first enables it, so the
// version list has nothing to pin against and the next sync is up to six hours
// away. Calling this on the enabling write closes that gap without reintroducing
// an unconditional fetch. A no-op once anything is known.
func (e *Engine) EnsureCatalog(ctx context.Context) error {
	if _, err := e.queries.GetStableVirtioWinRelease(ctx); err == nil {
		return nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check catalog: %w", err)
	}
	if _, err := e.RefreshCatalog(ctx); err != nil {
		return err
	}
	return nil
}

// SyncCluster brings one cluster's ISO storage in line with its target version.
// Returns the download row when one was dispatched, nil when nothing was needed.
func (e *Engine) SyncCluster(ctx context.Context, cfg db.VirtioWinConfig) (*db.VirtioWinDownload, error) {
	target, err := e.ResolveTarget(ctx, cfg)
	if err != nil {
		return nil, err
	}

	client, err := e.createClient(ctx, cfg.ClusterID)
	if err != nil {
		return nil, fmt.Errorf("proxmox client: %w", err)
	}

	node, err := e.pickNode(ctx, cfg)
	if err != nil {
		return nil, err
	}

	present, err := e.isoPresent(ctx, client, node, cfg.Storage, target.IsoFilename)
	if err != nil {
		return nil, fmt.Errorf("list %s content on %s: %w", cfg.Storage, node, err)
	}
	if present {
		// Already there. Prune still runs — a satisfied target is exactly when
		// superseded ISOs become removable.
		if cfg.PruneEnabled {
			e.pruneCluster(ctx, client, cfg, target)
		}
		return nil, nil
	}

	download, err := e.dispatch(ctx, client, cfg, node, target, "scheduler")
	if err != nil {
		if errors.Is(err, ErrDownloadInFlight) {
			// Already being fetched. Nothing to do and nothing to report.
			return nil, nil
		}
		return nil, err
	}
	return download, nil
}

// dispatch inserts the download row first, then asks Proxmox to start the
// fetch. Insert-then-call is deliberate: the partial unique index on
// (cluster, storage, version) for unfinished rows is what stops two scheduler
// ticks — or a tick racing a manual download — from queueing the same 837 MiB
// transfer twice. Losing that race here costs an insert; losing it the other
// way around costs a duplicate download.
func (e *Engine) dispatch(
	ctx context.Context,
	client *proxmox.Client,
	cfg db.VirtioWinConfig,
	node string,
	target db.VirtioWinRelease,
	triggeredBy string,
) (*db.VirtioWinDownload, error) {
	row, err := e.queries.InsertVirtioWinDownload(ctx, db.InsertVirtioWinDownloadParams{
		ClusterID:   cfg.ClusterID,
		Node:        node,
		Storage:     cfg.Storage,
		Version:     target.Version,
		Filename:    target.IsoFilename,
		Status:      "pending",
		Upid:        "",
		TriggeredBy: triggeredBy,
	})
	if err != nil {
		if isUniqueViolation(err) {
			// The partial unique index on unfinished (cluster, storage, version)
			// did its job: another tick — or an operator's "Download now" —
			// already has this exact fetch in flight. That is the intended
			// outcome, not a failure, and reporting it as one would surface a
			// raw Postgres message in the config card's error banner.
			return nil, ErrDownloadInFlight
		}
		return nil, fmt.Errorf("record download of %s: %w", target.Version, err)
	}

	verify := true
	params := proxmox.URLDownloadParams{
		URL:                target.IsoUrl,
		Content:            "iso",
		Filename:           target.IsoFilename,
		Checksum:           target.Checksum,
		ChecksumAlgorithm:  target.ChecksumAlgorithm,
		VerifyCertificates: &verify,
	}
	upid, err := client.DownloadURLToStorage(ctx, node, cfg.Storage, params)
	if err != nil {
		msg := describeDownloadError(err)
		if ferr := e.queries.FinishVirtioWinDownload(ctx, db.FinishVirtioWinDownloadParams{
			ID: row.ID, Status: "failed", Error: msg,
		}); ferr != nil {
			e.logger.Warn("virtio-win: mark dispatch failure", "id", row.ID, "error", ferr)
		}
		return nil, fmt.Errorf("start download of %s on %s/%s: %s", target.Version, node, cfg.Storage, msg)
	}

	row.Upid = upid
	row.Status = "running"
	if err := e.queries.SetVirtioWinDownloadUPID(ctx, db.SetVirtioWinDownloadUPIDParams{
		ID: row.ID, Upid: upid,
	}); err != nil {
		// The task is running on the node regardless; without the UPID the
		// reconcile loop cannot follow it, so say so loudly.
		e.logger.Error("virtio-win: download started but its UPID was not recorded",
			"id", row.ID, "upid", upid, "error", err)
	}
	return &row, nil
}

// describeDownloadError turns the two Proxmox permission failures operators
// actually hit into something actionable. download-url needs storage allocate
// AND network access, and the second is easy to miss when minting an API token.
func describeDownloadError(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "sys.accessnetwork"), strings.Contains(lower, "sys.modify"):
		return msg + " — the Proxmox API token needs Sys.AccessNetwork on the node (or Sys.Modify on /) " +
			"in addition to Datastore.AllocateTemplate on the storage"
	case strings.Contains(lower, "datastore.allocatetemplate"):
		return msg + " — the Proxmox API token needs Datastore.AllocateTemplate on this storage"
	default:
		return msg
	}
}

// pickNode returns the node that will perform the download.
//
// download-url is node-scoped even though the ISO lands on the storage, so the
// node has to be one that actually carries that storage. "Any online node" is
// only correct for shared storage; for a local directory pool restricted to a
// subset of nodes it dispatches somewhere the call cannot succeed.
//
// An explicit cfg.Node wins — an operator who named a node meant it, and
// second-guessing them would hide a misconfiguration behind a silent fallback.
func (e *Engine) pickNode(ctx context.Context, cfg db.VirtioWinConfig) (string, error) {
	if cfg.Node != "" {
		return cfg.Node, nil
	}

	hosts, err := e.queries.ListNodesWithStorage(ctx, db.ListNodesWithStorageParams{
		ClusterID: cfg.ClusterID,
		Storage:   cfg.Storage,
	})
	if err != nil {
		return "", fmt.Errorf("list nodes carrying storage %q: %w", cfg.Storage, err)
	}
	if len(hosts) > 0 {
		return hosts[0], nil
	}

	// Nothing in inventory claims the storage. That is usually a stale or
	// not-yet-collected storage_pools table rather than a real absence, so fall
	// back to any online node rather than refusing outright — Proxmox will
	// reject it clearly enough if the storage genuinely is not there.
	nodes, err := e.queries.ListNodesByCluster(ctx, cfg.ClusterID)
	if err != nil {
		return "", fmt.Errorf("list nodes: %w", err)
	}
	for _, n := range nodes {
		if strings.EqualFold(n.Status, "online") {
			e.logger.Warn("virtio-win: no node in inventory carries the target storage; falling back to an online node",
				"cluster_id", cfg.ClusterID, "storage", cfg.Storage, "node", n.Name)
			return n.Name, nil
		}
	}
	return "", fmt.Errorf("virtio-win: no online node in cluster %s to run the download", cfg.ClusterID)
}

func (e *Engine) isoPresent(ctx context.Context, client *proxmox.Client, node, storage, filename string) (bool, error) {
	items, err := client.GetStorageContentByType(ctx, node, storage, "iso")
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if volumeFilename(item.Volid) == filename {
			return true, nil
		}
	}
	return false, nil
}

// volumeFilename extracts the basename from a Proxmox volid such as
// "local:iso/virtio-win-0.1.302.iso".
func volumeFilename(volid string) string {
	if idx := strings.LastIndex(volid, "/"); idx >= 0 {
		return volid[idx+1:]
	}
	if idx := strings.Index(volid, ":"); idx >= 0 {
		return volid[idx+1:]
	}
	return volid
}

// Reconcile advances every unfinished download by polling its Proxmox task.
// This is the durable half of the dispatch: the POST returns immediately, and
// an 837 MiB fetch outlives any in-process watcher.
func (e *Engine) Reconcile(ctx context.Context) error {
	rows, err := e.queries.ListActiveVirtioWinDownloads(ctx)
	if err != nil {
		return fmt.Errorf("list active downloads: %w", err)
	}
	for _, row := range rows {
		e.reconcileOne(ctx, row)
	}
	return nil
}

func (e *Engine) reconcileOne(ctx context.Context, row db.VirtioWinDownload) {
	if row.Upid == "" {
		// Dispatch failed before Proxmox accepted the task, or the UPID write
		// lost. Either way nothing can be polled; stale rows here would pin the
		// in-flight unique index forever, so time them out.
		if time.Since(row.StartedAt) > upidGracePeriod {
			e.finish(ctx, row, "failed", "no Proxmox task was recorded for this download")
		}
		return
	}

	client, err := e.createClient(ctx, row.ClusterID)
	if err != nil {
		if time.Since(row.StartedAt) > downloadMaxAge {
			e.finish(ctx, row, "failed", "could not reach the cluster to check this download: "+err.Error())
			return
		}
		e.logger.Warn("virtio-win: reconcile client build failed", "id", row.ID, "error", err)
		return
	}

	status, err := client.GetTaskStatus(ctx, row.Node, row.Upid)
	if err != nil {
		// A transient poll failure is normal; an unresolvable one is not, and
		// the row must not be allowed to hold the in-flight slot forever.
		if time.Since(row.StartedAt) > downloadMaxAge {
			e.finish(ctx, row, "failed",
				"gave up polling the Proxmox task after "+downloadMaxAge.String()+": "+err.Error())
			return
		}
		e.logger.Warn("virtio-win: task status poll failed", "id", row.ID, "upid", row.Upid, "error", err)
		return
	}
	if status.Status != "stopped" {
		if time.Since(row.StartedAt) > downloadMaxAge {
			e.finish(ctx, row, "failed",
				"download still not finished after "+downloadMaxAge.String()+"; giving up tracking it")
		}
		return // still running
	}
	if proxmox.TaskSucceeded(status.ExitStatus) {
		e.finish(ctx, row, "succeeded", "")
		e.logger.Info("virtio-win: ISO downloaded",
			"cluster_id", row.ClusterID, "storage", row.Storage, "version", row.Version)
		e.pruneAfterDownload(ctx, client, row)
		return
	}
	e.finish(ctx, row, "failed", status.ExitStatus)
}

const (
	// upidGracePeriod bounds how long a download row with NO UPID may sit
	// in-flight before being failed. Long enough that a slow dispatch is not
	// mistaken for a lost one.
	upidGracePeriod = 15 * time.Minute

	// downloadMaxAge bounds an in-flight row whose UPID we DO have but can no
	// longer resolve. Without it, a task whose log has been rotated away, or
	// whose node was renamed, or whose token lost task-read permission, leaves
	// the row 'running' forever — and the partial unique index on unfinished
	// (cluster, storage, version) then rejects every future download of that
	// version to that storage, permanently. An 837 MiB fetch that has not
	// finished in six hours is not going to.
	downloadMaxAge = 6 * time.Hour
)

func (e *Engine) finish(ctx context.Context, row db.VirtioWinDownload, status, errMsg string) {
	if err := e.queries.FinishVirtioWinDownload(ctx, db.FinishVirtioWinDownloadParams{
		ID: row.ID, Status: status, Error: errMsg,
	}); err != nil {
		e.logger.Warn("virtio-win: finish download row failed", "id", row.ID, "error", err)
	}
}

func (e *Engine) pruneAfterDownload(ctx context.Context, client *proxmox.Client, row db.VirtioWinDownload) {
	cfg, err := e.queries.GetVirtioWinConfig(ctx, row.ClusterID)
	if err != nil || !cfg.PruneEnabled {
		return
	}
	target, err := e.ResolveTarget(ctx, cfg)
	if err != nil {
		return
	}
	e.pruneCluster(ctx, client, cfg, target)
}

// pruneCluster removes virtio-win ISOs that are neither the cluster's target
// nor pinned by any cluster.
//
// Two safety properties, both load-bearing:
//   - Only files matching the virtio-win ISO naming convention are ever
//     considered. An operator's unrelated ISO on the same storage is invisible
//     to this.
//   - An empty keep-set means something went wrong upstream (no catalog, no
//     target). Deleting everything is never the right answer to "I don't know
//     what to keep", so it refuses and logs.
func (e *Engine) pruneCluster(ctx context.Context, client *proxmox.Client, cfg db.VirtioWinConfig, target db.VirtioWinRelease) {
	keep, err := e.keepSet(ctx, target)
	if err != nil {
		e.logger.Warn("virtio-win: prune skipped, keep-set unavailable", "cluster_id", cfg.ClusterID, "error", err)
		return
	}
	if len(keep) == 0 {
		e.logger.Warn("virtio-win: prune refused, keep-set is empty", "cluster_id", cfg.ClusterID)
		return
	}

	node, err := e.pickNode(ctx, cfg)
	if err != nil {
		return
	}
	items, err := client.GetStorageContentByType(ctx, node, cfg.Storage, "iso")
	if err != nil {
		e.logger.Warn("virtio-win: prune skipped, storage listing failed", "cluster_id", cfg.ClusterID, "error", err)
		return
	}

	// Anything currently attached to a guest is off limits regardless of the
	// keep-set. Leaving a mounted virtio-win ISO on a Windows VM is the normal
	// state after installing drivers by hand, and deleting it out from under
	// the guest leaves a CD-ROM pointing at a missing volume — which blocks
	// migration and can fail the next start.
	inUse, err := e.attachedISOs(ctx, client, cfg.ClusterID)
	if err != nil {
		e.logger.Warn("virtio-win: prune skipped, could not determine which ISOs are in use",
			"cluster_id", cfg.ClusterID, "error", err)
		return
	}

	for _, item := range items {
		filename := volumeFilename(item.Volid)
		version := versionFromISOFilename(filename)
		if version == "" {
			continue // not a virtio-win ISO; not ours to delete
		}
		if _, kept := keep[version]; kept {
			continue
		}
		if _, mounted := inUse[item.Volid]; mounted {
			e.logger.Info("virtio-win: keeping superseded ISO, still attached to a guest",
				"cluster_id", cfg.ClusterID, "volid", item.Volid)
			continue
		}
		if _, err := client.DeleteStorageContent(ctx, node, cfg.Storage, item.Volid); err != nil {
			e.logger.Warn("virtio-win: prune delete failed", "volid", item.Volid, "error", err)
			continue
		}
		e.logger.Info("virtio-win: pruned superseded ISO",
			"cluster_id", cfg.ClusterID, "storage", cfg.Storage, "volid", item.Volid, "iso_version", version)
	}
}

// keepSet is keyed on ISO version (the suffix-less form) because that is what a
// filename on disk carries. A pin held by any cluster protects that ISO
// everywhere: storages are frequently shared, and one cluster pruning another's
// pinned ISO is a cross-cluster surprise.
func (e *Engine) keepSet(ctx context.Context, target db.VirtioWinRelease) (map[string]struct{}, error) {
	keep := make(map[string]struct{})
	if target.IsoVersion != "" {
		keep[target.IsoVersion] = struct{}{}
	}
	pinned, err := e.queries.ListPinnedVirtioWinVersions(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range pinned {
		_, isoVersion := SplitVersion(v)
		if isoVersion != "" {
			keep[isoVersion] = struct{}{}
		}
	}
	if stable, err := e.queries.GetStableVirtioWinRelease(ctx); err == nil && stable.IsoVersion != "" {
		keep[stable.IsoVersion] = struct{}{}
	}
	return keep, nil
}

// attachedISOs returns every volume id referenced by a CD-ROM device on any
// QEMU guest in the cluster.
//
// This costs one config read per guest, which is why it runs only inside prune
// — at most once per release, and only for clusters that opted into pruning.
// A single guest whose config cannot be read fails the whole lookup rather than
// being skipped: a partial answer here reads as "not in use" and would delete
// exactly the media we are trying to protect.
func (e *Engine) attachedISOs(ctx context.Context, client *proxmox.Client, clusterID uuid.UUID) (map[string]struct{}, error) {
	vms, err := e.queries.ListVMsByCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list guests: %w", err)
	}

	// vms rows carry node_id, not a node name, and the Proxmox call is
	// node-scoped — so resolve the names once rather than per guest.
	nodes, err := e.queries.ListNodesByCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	nodeName := make(map[uuid.UUID]string, len(nodes))
	for _, n := range nodes {
		nodeName[n.ID] = n.Name
	}

	inUse := make(map[string]struct{})
	for _, vm := range vms {
		if vm.Type != "qemu" {
			continue
		}
		name, ok := nodeName[vm.NodeID]
		if !ok {
			return nil, fmt.Errorf("guest %d references unknown node %s", vm.Vmid, vm.NodeID)
		}
		config, err := client.GetVMConfig(ctx, name, int(vm.Vmid))
		if err != nil {
			return nil, fmt.Errorf("read config for guest %d: %w", vm.Vmid, err)
		}
		for _, raw := range config {
			value, ok := raw.(string)
			if !ok || !strings.Contains(value, "media=cdrom") {
				continue
			}
			// "local:iso/virtio-win-0.1.302.iso,media=cdrom" -> the volid.
			volid, _, _ := strings.Cut(value, ",")
			if volid != "" && volid != "none" {
				inUse[volid] = struct{}{}
			}
		}
	}
	return inUse, nil
}

// isoNamePattern matches the virtio-win ISO filenames this feature creates.
// Anything else on the storage is left alone.
func versionFromISOFilename(filename string) string {
	const prefix = "virtio-win-"
	const suffix = ".iso"
	if !strings.HasPrefix(filename, prefix) || !strings.HasSuffix(filename, suffix) {
		return ""
	}
	version := filename[len(prefix) : len(filename)-len(suffix)]
	if !ValidVersion(version) {
		return ""
	}
	return version
}

// DownloadNow dispatches a download outside the schedule, for the manual
// "download this version" action. Returns the row so the caller can track it.
func (e *Engine) DownloadNow(ctx context.Context, cfg db.VirtioWinConfig, version string) (*db.VirtioWinDownload, error) {
	release, err := e.queries.GetVirtioWinRelease(ctx, version)
	if errors.Is(err, pgx.ErrNoRows) {
		// Most likely the catalog has simply never been fetched. Populate it
		// and look again before telling the caller the version does not exist.
		if refreshErr := e.EnsureCatalog(ctx); refreshErr != nil {
			return nil, fmt.Errorf("%w: %q is not in the catalog and it could not be refreshed: %w", ErrNoTarget, version, refreshErr)
		}
		release, err = e.queries.GetVirtioWinRelease(ctx, version)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: %q is not in the catalog", ErrNoTarget, version)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("get release %q: %w", version, err)
	}
	client, err := e.createClient(ctx, cfg.ClusterID)
	if err != nil {
		return nil, fmt.Errorf("proxmox client: %w", err)
	}
	node, err := e.pickNode(ctx, cfg)
	if err != nil {
		return nil, err
	}
	present, err := e.isoPresent(ctx, client, node, cfg.Storage, release.IsoFilename)
	if err != nil {
		return nil, fmt.Errorf("list %s content on %s: %w", cfg.Storage, node, err)
	}
	if present {
		return nil, nil
	}
	return e.dispatch(ctx, client, cfg, node, release, "manual")
}

// MarkChecked records the outcome of a cluster sync attempt so the UI can show
// when the check last ran and why it last failed.
func (e *Engine) MarkChecked(ctx context.Context, clusterID uuid.UUID, syncErr error) {
	msg := ""
	if syncErr != nil {
		msg = syncErr.Error()
	}
	if err := e.queries.MarkVirtioWinConfigChecked(ctx, db.MarkVirtioWinConfigCheckedParams{
		ClusterID: clusterID, LastError: msg,
	}); err != nil {
		e.logger.Warn("virtio-win: record check outcome failed", "cluster_id", clusterID, "error", err)
	}
}

// TrimHistory drops finished download rows older than the retention window.
func (e *Engine) TrimHistory(ctx context.Context, retain time.Duration) {
	cutoff := pgtype.Timestamptz{Time: time.Now().Add(-retain), Valid: true}
	if err := e.queries.DeleteOldVirtioWinDownloads(ctx, cutoff); err != nil {
		e.logger.Warn("virtio-win: trim download history failed", "error", err)
	}
}

func (e *Engine) createClient(ctx context.Context, clusterID uuid.UUID) (*proxmox.Client, error) {
	if e.cache != nil {
		client, err := e.cache.Get(ctx, clusterID)
		if err == nil {
			return client, nil
		}
		e.logger.Warn("virtio-win: proxmox cache get failed, building per-call",
			"cluster_id", clusterID, "error", err)
	}

	cluster, err := e.queries.GetCluster(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("get cluster %s: %w", clusterID, err)
	}
	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, e.encryptionKey)
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
