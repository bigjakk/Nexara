package veeam

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// pageSize is how many rows a listing asks for per request. 200 is what the
// spike used and what every captured fixture reflects; larger pages are
// accepted by the server but nothing has been tested against them.
const pageSize = 200

// maxPages bounds a single listing so a server reporting a nonsensical total —
// or a filter that fails to narrow — cannot spin forever. Callers that need a
// tighter bound pass maxRows; this is the backstop for the ones that want
// everything.
const maxPages = 200

// maxInventoryRows bounds the listings that are meant to return everything.
// Proxmox inventories are small — the lab has 27 backup objects — so this only
// ever bites on a server answering nonsense.
const maxInventoryRows = maxPages * pageSize

// pagination is the envelope every list endpoint wraps its rows in.
type pagination struct {
	Total int `json:"total"`
	Count int `json:"count"`
	Skip  int `json:"skip"`
	Limit int `json:"limit"`
}

// listPaged walks a paginated endpoint, appending each page's rows.
//
// Termination is deliberately belt-and-braces: a short page ends the walk, a
// page with zero rows ends it, and maxPages caps it regardless. Relying on
// `skip+count >= total` alone would loop forever against a server whose total
// is stale or wrong, which is exactly the kind of thing this API has already
// been caught doing elsewhere.
func listPaged[T any](ctx context.Context, c *Client, path string, params url.Values, maxRows int) ([]T, error) {
	limit := min(maxRows, pageSize)
	if limit <= 0 {
		limit = pageSize
	}

	var out []T
	skip := 0
	for page := range maxPages {
		q := url.Values{}
		for k, v := range params {
			q[k] = v
		}
		q.Set("limit", strconv.Itoa(limit))
		q.Set("skip", strconv.Itoa(skip))

		var body struct {
			Data       []T        `json:"data"`
			Pagination pagination `json:"pagination"`
		}
		if err := c.get(ctx, path+"?"+q.Encode(), &body); err != nil {
			return nil, fmt.Errorf("veeam: list %s page %d: %w", path, page, err)
		}

		out = append(out, body.Data...)
		// maxRows is a TOTAL, not a page size — and the cap is applied BEFORE
		// the short-page return, or a final page that overshoots the cap slips
		// through it.
		if maxRows > 0 && len(out) >= maxRows {
			return out[:maxRows], nil
		}
		if len(body.Data) < limit {
			return out, nil
		}
		skip += len(body.Data)
		if body.Pagination.Total > 0 && skip >= body.Pagination.Total {
			return out, nil
		}
	}
	return out, nil
}

// Repository is one backup target from
// GET /api/v1/backupInfrastructure/repositories/states.
//
// Note the path: Veeam's published reference documents this as
// /api/v1/repositories, which 404s. The `states` variant is also the only one
// that carries usage.
type Repository struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	HostName    string `json:"hostName"`
	Path        string `json:"path"`
	// Capacity arrives as floating-point GIGABYTES, not bytes. Converted at
	// the edge by the Bytes accessors so nothing downstream has to remember.
	CapacityGB  float64 `json:"capacityGB"`
	FreeGB      float64 `json:"freeGB"`
	UsedSpaceGB float64 `json:"usedSpaceGB"`
	IsOnline    bool    `json:"isOnline"`
	IsOutOfDate bool    `json:"isOutOfDate"`
}

// gigabyte is the binary GB the API reports in.
const gigabyte = 1024 * 1024 * 1024

func gbToBytes(gb float64) int64 {
	if gb <= 0 {
		return 0
	}
	return int64(gb * gigabyte)
}

// CapacityBytes returns the repository's total size in bytes.
func (r Repository) CapacityBytes() int64 { return gbToBytes(r.CapacityGB) }

// FreeBytes returns the repository's free space in bytes.
func (r Repository) FreeBytes() int64 { return gbToBytes(r.FreeGB) }

// UsedBytes returns the repository's used space in bytes.
func (r Repository) UsedBytes() int64 { return gbToBytes(r.UsedSpaceGB) }

// Repositories lists every repository with its current usage.
func (c *Client) Repositories(ctx context.Context) ([]Repository, error) {
	return listPaged[Repository](ctx, c, "/api/v1/backupInfrastructure/repositories/states", nil, maxInventoryRows)
}

// JobState is one row from GET /api/v1/jobs/states.
//
// This is the ONLY view of a Proxmox job the REST API offers on 13.1: Proxmox
// jobs are absent from GET /api/v1/jobs entirely, and GET /jobs/{id} rejects
// them with "Specify job of supported platform type." Job configuration —
// schedule, retention — is therefore unreadable, and nothing here tries.
//
// Note the absence of platformId. Sessions are the only bridge from a job to
// the Proxmox connection it backs up.
type JobState struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Type            string     `json:"type"`
	Status          string     `json:"status"`
	LastResult      string     `json:"lastResult"`
	Workload        string     `json:"workload"`
	Description     string     `json:"description"`
	LastRun         *Timestamp `json:"lastRun"`
	NextRun         *Timestamp `json:"nextRun"`
	NextRunPolicy   string     `json:"nextRunPolicy"`
	RepositoryID    string     `json:"repositoryId"`
	RepositoryName  string     `json:"repositoryName"`
	TargetName      string     `json:"targetName"`
	ObjectsCount    int        `json:"objectsCount"`
	SessionID       string     `json:"sessionId"`
	ProgressPercent int        `json:"progressPercent"`
	SessionProgress Progress   `json:"sessionProgress"`
}

// IsProxmox reports whether this is a Proxmox backup job.
func (j JobState) IsProxmox() bool { return j.Type == ProxmoxJobType }

// ProxmoxJobType is the EJobType value for a Proxmox backup job on 13.1.
// Pre-13.1 builds report "Unknown" here, which is the reason for the version
// floor.
const ProxmoxJobType = "ProxmoxBackupJob"

// Progress is Veeam's own throughput and bottleneck analysis, shared by job
// states and sessions.
//
// Duration and ProcessingRate are pre-formatted display strings ("00:18:27",
// "268 MB"), not quantities — Veeam gives no machine-readable form, so they
// are carried through as-is rather than parsed into something that would only
// be a guess.
type Progress struct {
	Bottleneck      string `json:"bottleneck"`
	Duration        string `json:"duration"`
	ProcessingRate  string `json:"processingRate"`
	ProcessedSize   int64  `json:"processedSize"`
	ReadSize        int64  `json:"readSize"`
	TransferredSize int64  `json:"transferredSize"`
	ProgressPercent int    `json:"progressPercent"`
}

// JobStates lists every job's current state.
func (c *Client) JobStates(ctx context.Context) ([]JobState, error) {
	return listPaged[JobState](ctx, c, "/api/v1/jobs/states", nil, maxInventoryRows)
}

// BackupObject is one row from GET /api/v1/backupObjects — a guest as it
// appears inside one backup.
//
// One row per (guest × backup), and the SAME ID repeats across them: verified
// live, where 27 rows carried 18 distinct ids. ID is the guest's identity
// within Veeam and BackupID is what differs, so callers that want per-guest
// figures must fold the rows — and RestorePointsCount, being per-backup, must
// be summed when they do.
type BackupObject struct {
	// ID identifies the GUEST within Veeam, not this row: it repeats across
	// every backup the guest appears in.
	ID string `json:"id"`
	// ObjectID is the platform's native VM id. For Proxmox it IS the smbios1
	// uuid, which is what makes guest correlation deterministic.
	ObjectID     string `json:"objectId"`
	PlatformName string `json:"platformName"`
	PlatformID   string `json:"platformId"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	// BackupID does NOT resolve into GET /api/v1/backups — verified against a
	// live server, where none of 27 values matched. Carried for reference; the
	// authoritative object-to-restore-point link is RestorePointsForObject.
	BackupID           string `json:"backupId"`
	RestorePointsCount int    `json:"restorePointsCount"`
	LastRunFailed      bool   `json:"lastRunFailed"`
	Size               int64  `json:"size"`
}

// IsProxmox reports whether this object belongs to a Proxmox platform.
func (o BackupObject) IsProxmox() bool { return o.PlatformName == ProxmoxPlatformName }

// BackupObjects lists every backup object on the server.
func (c *Client) BackupObjects(ctx context.Context) ([]BackupObject, error) {
	return listPaged[BackupObject](ctx, c, "/api/v1/backupObjects", nil, maxInventoryRows)
}

// RestorePoint is one recoverable point in time for a backup object.
type RestorePoint struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	PlatformName string `json:"platformName"`
	PlatformID   string `json:"platformId"`
	// MalwareStatus rides on every restore point, so per-guest malware state
	// needs no separate /malwareDetection sync.
	MalwareStatus string    `json:"malwareStatus"`
	GuestOSFamily string    `json:"guestOsFamily"`
	CreationTime  Timestamp `json:"creationTime"`
	BackupID      string    `json:"backupId"`
	SessionID     string    `json:"sessionId"`
	BackupFileID  string    `json:"backupFileId"`
	OriginalSize  int64     `json:"originalSize"`
	// AllowedOperations is where file-level restore availability shows up.
	// Entire-VM restore and instant recovery simply do not exist for Proxmox
	// on 1.3-rev2, so this never contains them.
	AllowedOperations []string `json:"allowedOperations"`
}

// SupportsFLR reports whether this point can be file-level restored.
func (p RestorePoint) SupportsFLR() bool {
	for _, op := range p.AllowedOperations {
		if op == "StartFlrRestore" {
			return true
		}
	}
	return false
}

// RestorePointsForObject lists the restore points belonging to one backup
// object.
//
// Per-object rather than the bulk GET /api/v1/restorePoints, which carries no
// object id: its rows could only be linked back by (platformId, name), and a
// rebuilt guest reuses its name. That would merge a replaced machine's restore
// points into its successor and destroy the orphan detection that is the point
// of the whole correlation design.
func (c *Client) RestorePointsForObject(ctx context.Context, objectID string) ([]RestorePoint, error) {
	if objectID == "" {
		return nil, fmt.Errorf("%w: objectID is required", ErrInvalidInput)
	}
	path := "/api/v1/backupObjects/" + url.PathEscape(objectID) + "/restorePoints"
	return listPaged[RestorePoint](ctx, c, path, nil, maxInventoryRows)
}

// SessionResult is the outcome block on a finished session.
type SessionResult struct {
	Result  string `json:"result"`
	Message string `json:"message"`
	// IsCanceled is FALSE even for a session cancelled through Veeam's own
	// API, which records the run as Result "Failed" with an empty log. It
	// cannot be read as "this was not cancelled".
	IsCanceled bool `json:"isCanceled"`
}

// Session is one job run from GET /api/v1/sessions.
type Session struct {
	ID string `json:"id"`
	// SessionType for a Proxmox backup is "PlatformBackupJob" — there is no
	// Proxmox value in ESessionType. PlatformName is what narrows it.
	SessionType  string        `json:"sessionType"`
	PlatformName string        `json:"platformName"`
	PlatformID   string        `json:"platformId"`
	Name         string        `json:"name"`
	JobID        string        `json:"jobId"`
	State        string        `json:"state"`
	Algorithm    string        `json:"algorithm"`
	CreationTime Timestamp     `json:"creationTime"`
	EndTime      *Timestamp    `json:"endTime"`
	Progress     Progress      `json:"progress"`
	Result       SessionResult `json:"result"`
	InitiatedBy  string        `json:"initiatedBy"`
	// ProgressPercent duplicates Progress.ProgressPercent at the top level;
	// both are present on real payloads.
	ProgressPercent int `json:"progressPercent"`
}

// PlatformBackupSessionType is the ESessionType a Proxmox backup run reports.
const PlatformBackupSessionType = "PlatformBackupJob"

// IsProxmoxBackup reports whether this session is a Proxmox backup run.
func (s Session) IsProxmoxBackup() bool {
	return s.SessionType == PlatformBackupSessionType && s.PlatformName == ProxmoxPlatformName
}

// Sessions lists backup sessions created after `since`, newest first.
//
// typeFilter is applied SERVER-side and is not optional in practice:
// ConfigurationResynchronize accounts for 109 of every 200 rows on a real
// server, so an unfiltered poll spends its entire page budget on noise before
// reaching a single backup run.
//
// maxRows is a TOTAL row cap, and rows arrive newest-first, so a caller that
// asks for 500 gets the 500 most recent — not 500 per page.
//
// A zero `since` omits the filter and pulls whatever maxRows allows, which is
// only appropriate for a first sync with a bounded cap.
func (c *Client) Sessions(ctx context.Context, since time.Time, maxRows int) ([]Session, error) {
	params := url.Values{
		"typeFilter":  {PlatformBackupSessionType},
		"orderColumn": {"CreationTime"},
		"orderAsc":    {"false"},
	}
	if !since.IsZero() {
		params.Set("createdAfterFilter", since.UTC().Format(time.RFC3339))
	}
	return listPaged[Session](ctx, c, "/api/v1/sessions", params, maxRows)
}

// Session re-reads one session by id.
//
// Sessions lists by createdAfterFilter, and a session's STATE keeps changing
// long after its creationTime — so that window can never re-observe a run it
// has already scrolled past. This is how a run still in flight when the window
// moved on is caught up with, and it is the only call that can report the
// final state of one.
//
// A run VBR has forgotten comes back as ErrSessionNotFound, which the caller
// must tell apart from a transient failure: the two mean opposite things about
// whether the run is still out there, and the collector deletes on one.
func (c *Client) Session(ctx context.Context, sessionID string) (*Session, error) {
	id, err := requireUUID("session id", sessionID)
	if err != nil {
		return nil, err
	}
	// c.do rather than c.get: the raw status is the ONLY thing that
	// distinguishes this endpoint's own 404 from one raised anywhere else on
	// the way here. do reports status 0 for every failure that happened
	// before the request was answered — a failed token grant included — so a
	// 404 here came from /sessions/{id} and nowhere else.
	body, status, err := c.do(ctx, http.MethodGet, "/api/v1/sessions/"+id)
	if err != nil {
		if status == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
		}
		return nil, err
	}

	var s Session
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("veeam: decode session %s response: %w", id, err)
	}
	return &s, nil
}

// ProxmoxProxyType is the EProxyType value for a Veeam worker appliance
// deployed onto a Proxmox cluster.
const ProxmoxProxyType = "PVE"

// ProxyState is one row from GET /api/v1/backupInfrastructure/proxies/states.
//
// ⚠️ /states, NOT the plain /backupInfrastructure/proxies listing. Verified
// against a live 13.1 server: the plain listing returned 2 rows and NONE of
// them were Proxmox, while /states returned 5 including all three PVE
// appliances. This is the same shape jobs take — Proxmox rows exist only in
// the /states variant — and it is not documented anywhere.
//
// There is deliberately no VM identity here to correlate on. The model carries
// a name and the Proxmox NODE it was deployed to (HostName), and nothing else:
// no smbios uuid, no vmid. Name is therefore the only key, which is why the
// resolution insists on an exact, unique, case-insensitive match rather than
// the substring test a human would reach for — "Veeam" appears in the lab's
// worker names AND in the name of an unrelated guest.
type ProxyState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	HostID      string `json:"hostId"`
	// HostName is the Proxmox node the appliance runs on for a PVE proxy
	// ("hv01.example.lan"), and the literal string "This server" for a proxy
	// role the VBR server fills itself.
	HostName    string `json:"hostName"`
	IsDisabled  bool   `json:"isDisabled"`
	IsOnline    bool   `json:"isOnline"`
	IsOutOfDate bool   `json:"isOutOfDate"`
}

// IsProxmox reports whether this proxy is a Proxmox worker appliance.
func (p ProxyState) IsProxmox() bool { return p.Type == ProxmoxProxyType }

// ProxyStates lists every backup proxy's current state.
//
// Worker appliances are normally OFFLINE between runs — all three on the lab
// report isOnline false — because Veeam powers them on for a job and off
// again afterwards. Do not read that as a fault.
func (c *Client) ProxyStates(ctx context.Context) ([]ProxyState, error) {
	return listPaged[ProxyState](ctx, c, "/api/v1/backupInfrastructure/proxies/states", nil, maxInventoryRows)
}

// ManagedServer is one row from GET /api/v1/backupInfrastructure/managedServers.
//
// Only used to find the VBR server itself. When it runs as a guest on the very
// cluster it protects — which the lab does — it is not an unprotected VM, it
// is infrastructure, and a coverage report that flags it teaches operators to
// ignore the report.
//
// Name is the FQDN Veeam knows the server by ("Veeam01.example.lan") and is
// what matches a Proxmox guest name. serverInfo.name is NOT interchangeable
// with it: on the lab that field is the short "Veeam01", which matches no
// guest at all.
type ManagedServer struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Status         string `json:"status"`
	Description    string `json:"description"`
	IsBackupServer bool   `json:"isBackupServer"`
}

// ManagedServers lists the servers registered with this VBR installation.
func (c *Client) ManagedServers(ctx context.Context) ([]ManagedServer, error) {
	return listPaged[ManagedServer](ctx, c, "/api/v1/backupInfrastructure/managedServers", nil, maxInventoryRows)
}
