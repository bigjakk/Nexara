package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/veeam"
)

// Job control — the write side of the Veeam integration.
//
// NOT TrackTask. A Veeam session is a uuid from a foreign scheduler: there is
// no node, no `pid:starttime`, and nothing to reconcile against
// /nodes/*/tasks, so it has its own table and audits through the shared
// AuditLog. handlers.TrackTask is Proxmox-only and its guard test enumerates
// proxmox.Client methods, which is why a veeam.Client method does not trip it —
// that is a fact about the guard, not permission to reach for TrackTask here.
//
// Every call is ASYNC. Veeam answers before it has acted (the lab job took
// ~30s to reach Stopped), so what these handlers persist is a starting state
// for the poll loop to settle, never a final one.

// veeamControlTimeout bounds one control call. It covers a password grant, the
// POST itself and a logout — generous, because the grant is against a
// domain-backed server that may not be warm, and short of the probe's 45s
// because there is no version negotiation in the path.
const veeamControlTimeout = 30 * time.Second

// StartJob handles POST /api/v1/veeam-servers/:id/jobs/:job_id/start.
func (h *VeeamHandler) StartJob(c fiber.Ctx, p *apischema.Params) error {
	target, err := h.resolveJobForControl(c, p)
	if err != nil {
		return err
	}

	client, release, err := h.controlClient(c.Context(), target.server)
	if err != nil {
		return renderVeeamControlError(c, err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(c.Context(), veeamControlTimeout)
	defer cancel()

	run, err := client.StartJob(ctx, target.job.VeeamID.String())
	if err != nil {
		h.auditJob(c, target, "veeam_job_start_failed", map[string]any{"error": auditSafe(err.Error())})
		return renderVeeamControlError(c, err)
	}

	// 204: accepted, but the job had nothing to process, so Veeam created no
	// run. Reported as a distinct outcome rather than a success — an operator
	// who clicked "Run now" would otherwise wait for a run that is never going
	// to appear, and conclude Nexara had dropped the request.
	if run.NoObjects {
		h.auditJob(c, target, "veeam_job_start_requested", map[string]any{"started": false, "reason": "no_objects"})
		h.publishVeeamChange(c, "job_started")
		return c.JSON(fiber.Map{
			"started": false,
			"message": "Veeam accepted the request but the job has no objects to process, so no run was started.",
		})
	}

	// Accepted, but the response carried no session to key on. The job IS
	// starting, so this is a success with a caveat, not a failure: saying
	// otherwise would invite a retry that starts a second run. The poll loop
	// picks the run up on its next pass.
	if run.Session == nil {
		h.auditJob(c, target, "veeam_job_started", map[string]any{
			"started":          true,
			"session_id":       nil,
			"untracked_reason": "veeam_named_no_session",
		})
		h.publishVeeamChange(c, "job_started")
		return c.JSON(fiber.Map{
			"started": true,
			"message": "Veeam accepted the request but reported no run to track. It will appear once the next sync completes.",
		})
	}

	// nexara_initiated, NOT nexara_stopped. This run is Nexara's, but if it
	// goes on to fail for a real reason that failure must still alert — only a
	// stop makes a "Failed" result untrustworthy. See migration 000094.
	h.recordControlSession(c, target.server, run.Session, true, false)

	h.auditJob(c, target, "veeam_job_started", map[string]any{
		"started":    true,
		"session_id": run.Session.ID,
	})
	h.publishVeeamChange(c, "job_started")

	return c.JSON(fiber.Map{
		"started":    true,
		"session_id": run.Session.ID,
		"state":      auditSafe(run.Session.State),
	})
}

// StopJob handles POST /api/v1/veeam-servers/:id/jobs/:job_id/stop.
//
// Disruptive: the run is abandoned and the guests it had not reached yet keep
// whatever recovery point they already had. The UI confirms it.
func (h *VeeamHandler) StopJob(c fiber.Ctx, p *apischema.Params) error {
	target, err := h.resolveJobForControl(c, p)
	if err != nil {
		return err
	}

	client, release, err := h.controlClient(c.Context(), target.server)
	if err != nil {
		return renderVeeamControlError(c, err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(c.Context(), veeamControlTimeout)
	defer cancel()

	run, err := client.StopJob(ctx, target.job.VeeamID.String())
	if err != nil {
		h.auditJob(c, target, "veeam_job_stop_failed", map[string]any{"error": auditSafe(err.Error())})
		return renderVeeamControlError(c, err)
	}

	// A 2xx means the stop was accepted, whatever came back with it. The run
	// is stopping either way, so `stopping` is unconditionally true — the only
	// question left is which session to hang nexara_stopped on.
	details := map[string]any{"stopping": true}
	resp := fiber.Map{"stopping": true}

	if run.Session != nil {
		// nexara_stopped is the whole point of the call: Veeam will record
		// this run as result "Failed" with isCanceled false and an empty log,
		// and this flag is the only thing that will later tell veeam_job_failed
		// that an operator asked for it.
		h.recordControlSession(c, target.server, run.Session, false, true)
		details["session_id"] = run.Session.ID
		resp["session_id"] = run.Session.ID
		resp["state"] = auditSafe(run.Session.State)
	} else {
		// Veeam accepted the stop but named no session, so there is nothing to
		// flag and veeam_job_failed will fire once for a cancellation the
		// operator asked for.
		//
		// veeam_jobs.last_session_id is deliberately NOT used as a fallback.
		// It is refreshed by the inventory pass, so for a job started since
		// the last one it still points at the PREVIOUS run — and flagging that
		// would mark a genuinely failed earlier run as operator-stopped,
		// suppressing a real backup failure, while still leaving the run that
		// was actually stopped unflagged. A false alert is recoverable; a
		// silently swallowed failure is what this whole feature exists to
		// prevent, so the ambiguity is recorded rather than guessed at.
		details["session_id"] = nil
		details["untracked_reason"] = "veeam_named_no_session"
		slog.Warn("veeam: stop accepted with no session to flag; veeam_job_failed may fire for it",
			"veeam_server_id", target.server.ID, "job", target.job.VeeamID)
	}

	h.auditJob(c, target, "veeam_job_stopped", details)
	h.publishVeeamChange(c, "job_stopped")

	return c.JSON(resp)
}

// EnableJob handles POST /api/v1/veeam-servers/:id/jobs/:job_id/enable.
func (h *VeeamHandler) EnableJob(c fiber.Ctx, p *apischema.Params) error {
	return h.setJobEnabled(c, p, true)
}

// DisableJob handles POST /api/v1/veeam-servers/:id/jobs/:job_id/disable.
//
// The quietest destructive action in the integration. Nothing is deleted and
// no alarm is raised anywhere — the job simply stops running, so protection
// stops accruing while every existing restore point sits there looking
// healthy. Confirmed in the UI for that reason.
func (h *VeeamHandler) DisableJob(c fiber.Ctx, p *apischema.Params) error {
	return h.setJobEnabled(c, p, false)
}

func (h *VeeamHandler) setJobEnabled(c fiber.Ctx, p *apischema.Params, enable bool) error {
	target, err := h.resolveJobForControl(c, p)
	if err != nil {
		return err
	}

	client, release, err := h.controlClient(c.Context(), target.server)
	if err != nil {
		return renderVeeamControlError(c, err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(c.Context(), veeamControlTimeout)
	defer cancel()

	action := "veeam_job_disabled"
	if enable {
		action = "veeam_job_enabled"
		err = client.EnableJob(ctx, target.job.VeeamID.String())
	} else {
		err = client.DisableJob(ctx, target.job.VeeamID.String())
	}
	if err != nil {
		h.auditJob(c, target, action+"_failed", map[string]any{"error": auditSafe(err.Error())})
		return renderVeeamControlError(c, err)
	}

	h.auditJob(c, target, action, nil)
	h.publishVeeamChange(c, action)

	// The stored job row still reads the old status until the next inventory
	// poll. Deliberately not patched here: veeam_jobs mirrors what Veeam
	// reports, and writing a status Veeam has not confirmed would make the
	// table disagree with its source on the one field an operator is
	// watching. The UI refetches on the invalidation this publishes.
	return c.JSON(fiber.Map{"enabled": enable})
}

// StopSession handles POST /api/v1/veeam-servers/:id/sessions/:session_id/stop.
func (h *VeeamHandler) StopSession(c fiber.Ctx, p *apischema.Params) error {
	target, err := h.resolveSessionForControl(c, p, "execute")
	if err != nil {
		return err
	}

	client, release, err := h.controlClient(c.Context(), target.server)
	if err != nil {
		return renderVeeamControlError(c, err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(c.Context(), veeamControlTimeout)
	defer cancel()

	if err := client.StopSession(ctx, target.session.VeeamID.String()); err != nil {
		h.auditSession(c, target, "veeam_session_stop_failed", map[string]any{"error": auditSafe(err.Error())})
		return renderVeeamControlError(c, err)
	}

	// Nothing comes back from this call — 200 with an empty envelope — so the
	// flag goes onto the row the caller named. Marked AFTER the stop
	// succeeded: a flag set for a stop that was refused would suppress a real
	// failure on a run that is still going.
	//
	// On a context DETACHED from the request, for the same reason the logout
	// in controlClient is. Veeam has already accepted the stop by the time
	// this runs, so if the operator navigates away and Fiber cancels the
	// request, letting this write be cancelled with it would lose the one flag
	// that stops veeam_job_failed firing for a cancellation they asked for.
	// The whole phase exists to prevent that alert.
	h.markSessionStopped(c, target.server.ID, target.session.VeeamID)

	h.auditSession(c, target, "veeam_session_stopped", nil)
	h.publishVeeamChange(c, "session_stopped")

	return c.JSON(fiber.Map{"stopping": true})
}

// veeamSessionLogResponse is one line of a session's log.
type veeamSessionLogResponse struct {
	ID          int        `json:"id"`
	Status      string     `json:"status"`
	StartTime   *time.Time `json:"start_time"`
	UpdateTime  *time.Time `json:"update_time"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
}

// GetSessionLogs handles GET /api/v1/veeam-servers/:id/sessions/:session_id/logs.
//
// Read-through to Veeam rather than stored: logs are large, per-run, and
// wanted only when someone opens one run. view:veeam, cluster-scoped through
// the session's platform like every other read.
//
// AN EMPTY LOG IS NORMAL for a stopped run — Veeam returns no records at all
// for a killed session. The UI says so rather than showing a bare "no data",
// because "the log is empty" and "we failed to fetch the log" look identical
// otherwise.
func (h *VeeamHandler) GetSessionLogs(c fiber.Ctx, p *apischema.Params) error {
	target, err := h.resolveSessionForControl(c, p, "view")
	if err != nil {
		return err
	}

	client, release, err := h.controlClient(c.Context(), target.server)
	if err != nil {
		return renderVeeamControlError(c, err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(c.Context(), veeamControlTimeout)
	defer cancel()

	records, err := client.SessionLogs(ctx, target.session.VeeamID.String())
	if err != nil {
		return renderVeeamControlError(c, err)
	}

	resp := make([]veeamSessionLogResponse, 0, len(records))
	for _, r := range records {
		row := veeamSessionLogResponse{
			ID: r.ID,
			// Bounded: every one of these is upstream text from a server the
			// operator chose, and nothing on the far end caps them.
			Status:      auditSafe(r.Status),
			Title:       auditSafe(r.Title),
			Description: auditSafe(r.Description),
		}
		if r.StartTime != nil && !r.StartTime.IsZero() {
			t := r.StartTime.Time
			row.StartTime = &t
		}
		if r.UpdateTime != nil && !r.UpdateTime.IsZero() {
			t := r.UpdateTime.Time
			row.UpdateTime = &t
		}
		resp = append(resp, row)
	}
	return RespondItems(c, resp)
}

// veeamTaskSessionResponse is one guest's outcome inside a run.
type veeamTaskSessionResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// State and Result are the guest's own, not the run's — a run reported as
	// Failed can have most of its guests Succeeded, and which ones did not is
	// the whole question this endpoint answers.
	State           string     `json:"state"`
	Result          string     `json:"result"`
	ResultMessage   string     `json:"result_message"`
	Algorithm       string     `json:"algorithm"`
	Duration        string     `json:"duration"`
	ProcessedSize   int64      `json:"processed_size"`
	TransferredSize int64      `json:"transferred_size"`
	CreationTime    *time.Time `json:"creation_time"`
	EndTime         *time.Time `json:"end_time"`
	// ClusterID and Vmid are the Nexara guest this task processed, when the
	// name resolves to exactly one. NULL otherwise, and the UI shows the bare
	// name — a task session carries no uuid, so a guess here would be a guess.
	ClusterID *uuid.UUID `json:"cluster_id"`
	Vmid      *int32     `json:"vmid"`
}

// GetSessionTasks handles
// GET /api/v1/veeam-servers/:id/sessions/:session_id/tasks.
//
// The per-guest breakdown of one run: which guests it processed and which of
// them failed. Veeam's console shows this; none of its plain listings do, and
// a Proxmox job's object list is unreadable (GET /jobs/{id} is a 400), so this
// is the only route to "what failed inside that job".
//
// Read-through to Veeam rather than stored, like the session log: wanted only
// when someone opens one run, and it costs a logon per call.
//
// ⚠️ AN EMPTY LIST IS TWO DIFFERENT FACTS. Veeam reports no task rows for a
// run still in flight — they appear as tasks finish — and none for a finished
// run it kept no detail for. The response says which, because an operator
// reads the wrong one otherwise.
func (h *VeeamHandler) GetSessionTasks(c fiber.Ctx, p *apischema.Params) error {
	target, err := h.resolveSessionForControl(c, p, "view")
	if err != nil {
		return err
	}

	client, release, err := h.controlClient(c.Context(), target.server)
	if err != nil {
		return renderVeeamControlError(c, err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(c.Context(), veeamControlTimeout)
	defer cancel()

	tasks, err := client.TaskSessions(ctx, target.session.VeeamID.String())
	if err != nil {
		return renderVeeamControlError(c, err)
	}

	guests := h.resolveTaskGuests(c, target)

	resp := make([]veeamTaskSessionResponse, 0, len(tasks))
	for _, t := range tasks {
		// Backup tasks only. The same endpoint carries Restore, Antivirus and
		// Replica rows, none of which are a guest's outcome in this run.
		if !t.IsBackup() {
			continue
		}
		// And only tasks on the platform this caller was authorized for.
		//
		// The request was permitted against the SESSION's platform, but a task
		// row carries a platformId of its own — which is what makes it
		// independently cluster-attributable, and therefore what has to agree.
		// A row naming a different platform would be a guest name and backup
		// result from a cluster the caller may hold no grant on. It should not
		// happen (a run targets one platform), which is exactly why it is
		// dropped silently rather than reasoned about: everything else in this
		// integration answers "could this belong to another cluster?" with
		// fail-closed, and a row that cannot be proved to belong here does not
		// get served from here.
		if !sameVeeamPlatform(t.PlatformID, target.session.PlatformID) {
			continue
		}
		row := veeamTaskSessionResponse{
			ID: t.ID,
			// Bounded: every one of these is upstream text from a server the
			// operator chose, and nothing on the far end caps them.
			Name:            auditSafe(t.Name),
			State:           auditSafe(t.State),
			Result:          auditSafe(t.Result.Result),
			ResultMessage:   auditSafe(t.Result.Message),
			Algorithm:       auditSafe(t.Algorithm),
			Duration:        auditSafe(t.Progress.Duration),
			ProcessedSize:   t.Progress.ProcessedSize,
			TransferredSize: t.Progress.TransferredSize,
		}
		if !t.CreationTime.IsZero() {
			ts := t.CreationTime.Time
			row.CreationTime = &ts
		}
		if end := t.EndTime.Or(); !end.IsZero() {
			row.EndTime = &end
		}
		if g, ok := guests[strings.ToLower(t.Name)]; ok {
			cluster := g.cluster
			vmid := g.vmid
			row.ClusterID = &cluster
			row.Vmid = &vmid
		}
		resp = append(resp, row)
	}

	// Plain envelope, no bespoke "running" field beside it. Telling the two
	// empty cases apart needs the run's state, and the caller already holds
	// it — the same row, read from the same table, so a flag here would be
	// exactly as stale and would fork the response shape for nothing.
	return RespondItems(c, resp)
}

// sameVeeamPlatform reports whether an upstream platform id may be served to a
// caller authorized against `authorized`.
//
// A task row naming a DIFFERENT platform, or none at all, cannot be shown to
// belong to the cluster the request was permitted for, so it is not served.
//
// An unattributable SESSION is the one case that opens up rather than closes
// down, and it is not a hole: permitsPlatform answers a NULL platform with
// HasGlobal alone, so reaching here with `authorized` invalid means the caller
// holds global view:veeam and is entitled to every cluster's rows already.
// Filtering there would hide data from the only person who can see all of it,
// and buy nothing.
func sameVeeamPlatform(upstream string, authorized pgtype.UUID) bool {
	if !authorized.Valid {
		return true
	}
	id, err := uuid.Parse(upstream)
	if err != nil {
		return false
	}
	return id == uuid.UUID(authorized.Bytes)
}

// taskGuest is a resolved (cluster, vmid) for one guest name.
type taskGuest struct {
	cluster uuid.UUID
	vmid    int32
}

// resolveTaskGuests maps lowercased guest names on this session's platform to
// the guests they resolve to.
//
// Best-effort: a failure here costs the guest links, not the breakdown, and
// the breakdown is what the caller came for. An unresolvable name renders as a
// bare name, which is also what an ambiguous one does.
func (h *VeeamHandler) resolveTaskGuests(c fiber.Ctx, target veeamSessionTarget) map[string]taskGuest {
	if !target.session.PlatformID.Valid {
		return nil
	}
	rows, err := h.queries.ResolveVeeamTaskGuests(c.Context(), db.ResolveVeeamTaskGuestsParams{
		VeeamServerID: target.server.ID,
		PlatformID:    uuid.UUID(target.session.PlatformID.Bytes),
	})
	if err != nil {
		slog.Warn("veeam: resolving task guests failed; the breakdown will show bare names",
			"veeam_server_id", target.server.ID, "session", target.session.VeeamID, "error", err)
		return nil
	}

	guests := make(map[string]taskGuest, len(rows))
	for _, r := range rows {
		if !r.ClusterID.Valid || !r.Vmid.Valid {
			continue
		}
		guests[r.GuestName] = taskGuest{cluster: uuid.UUID(r.ClusterID.Bytes), vmid: r.Vmid.Int32}
	}
	return guests
}

// ---------------------------------------------------------------------------
// Resolution and authorization
// ---------------------------------------------------------------------------

// veeamJobTarget is a job resolved for a control call, with the cluster it was
// authorized against.
type veeamJobTarget struct {
	server db.VeeamServer
	job    db.VeeamJob
	// cluster is the Nexara cluster the job's Veeam platform maps to, or the
	// NULL pgtype.UUID for a job that is unattributable. It is what the audit
	// row is filed under, so a cluster-scoped reader can see the action that
	// happened on their cluster.
	cluster pgtype.UUID
}

type veeamSessionTarget struct {
	server  db.VeeamServer
	session db.VeeamSession
	cluster pgtype.UUID
}

// resolveJobForControl parses, loads and authorizes the job a control call
// names.
//
// ORDER MATTERS. The coarse "does this caller hold execute:veeam anywhere"
// gate runs BEFORE the row is loaded, so a caller with no grant at all cannot
// tell a job that exists from one that does not — the same 404-vs-403 oracle
// the read endpoints close. The precise per-cluster check can only run after
// the row is loaded, because the job's platform is what names the cluster.
func (h *VeeamHandler) resolveJobForControl(c fiber.Ctx, p *apischema.Params) (veeamJobTarget, error) {
	serverID, err := veeamServerID(p)
	if err != nil {
		return veeamJobTarget{}, err
	}
	jobID, err := parseParamUUID(p.String("job_id"))
	if err != nil {
		return veeamJobTarget{}, err
	}

	scope, err := h.veeamScopeForAction(c, "execute", serverID)
	if err != nil {
		return veeamJobTarget{}, err
	}

	server, err := h.fetch(c, p)
	if err != nil {
		return veeamJobTarget{}, err
	}

	job, err := h.queries.GetVeeamJobByVeeamID(c.Context(), db.GetVeeamJobByVeeamIDParams{
		VeeamServerID: server.ID,
		VeeamID:       jobID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return veeamJobTarget{}, fiber.NewError(fiber.StatusNotFound, "Veeam job not found")
		}
		return veeamJobTarget{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get Veeam job")
	}

	platform := uuid.UUID(job.PlatformID.Bytes)
	// A job that has never run has no platform, so it could belong to any
	// cluster this server protects. Global-only is the honest answer, and it
	// is the same fail-closed posture the read endpoints take.
	if !scope.permitsPlatform(platform, job.PlatformID.Valid) {
		return veeamJobTarget{}, fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}

	return veeamJobTarget{server: server, job: job, cluster: scope.clusterFor(platform, job.PlatformID.Valid)}, nil
}

// resolveSessionForControl is resolveJobForControl for a session. The action
// differs by endpoint: stopping a run is execute:veeam, reading its log is
// view:veeam.
func (h *VeeamHandler) resolveSessionForControl(c fiber.Ctx, p *apischema.Params, action string) (veeamSessionTarget, error) {
	serverID, err := veeamServerID(p)
	if err != nil {
		return veeamSessionTarget{}, err
	}
	sessionID, err := parseParamUUID(p.String("session_id"))
	if err != nil {
		return veeamSessionTarget{}, err
	}

	scope, err := h.veeamScopeForAction(c, action, serverID)
	if err != nil {
		return veeamSessionTarget{}, err
	}

	server, err := h.fetch(c, p)
	if err != nil {
		return veeamSessionTarget{}, err
	}

	session, err := h.queries.GetVeeamSessionByVeeamID(c.Context(), db.GetVeeamSessionByVeeamIDParams{
		VeeamServerID: server.ID,
		VeeamID:       sessionID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return veeamSessionTarget{}, fiber.NewError(fiber.StatusNotFound, "Veeam session not found")
		}
		return veeamSessionTarget{}, fiber.NewError(fiber.StatusInternalServerError, "Failed to get Veeam session")
	}

	platform := uuid.UUID(session.PlatformID.Bytes)
	if !scope.permitsPlatform(platform, session.PlatformID.Valid) {
		return veeamSessionTarget{}, fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
	}

	return veeamSessionTarget{
		server:  server,
		session: session,
		cluster: scope.clusterFor(platform, session.PlatformID.Valid),
	}, nil
}

// ---------------------------------------------------------------------------
// Client, persistence, audit
// ---------------------------------------------------------------------------

// controlClient builds a throwaway client for one control call and returns the
// release function that ends its Veeam session.
//
// Not cached, matching probe: a per-call client means the plaintext password
// has no lifetime beyond the request, and the token it mints is handed back
// rather than left to expire on its own in 15 minutes.
func (h *VeeamHandler) controlClient(ctx context.Context, server db.VeeamServer) (*veeam.Client, func(), error) {
	password, err := crypto.Decrypt(server.PasswordEncrypted, h.encryptionKey)
	if err != nil {
		return nil, nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt stored password")
	}

	client, err := veeam.New(veeam.Config{
		BaseURL:        server.BaseUrl,
		Username:       server.Username,
		Password:       password,
		APIRevision:    server.ApiRevision,
		TLSFingerprint: server.TlsFingerprint,
		VerifyTLS:      server.VerifyTls,
		Timeout:        veeamControlTimeout,
	})
	if err != nil {
		return nil, nil, err
	}

	release := func() {
		// Its own context: the request's may already be cancelled by the time
		// this runs, and a logout skipped on cancellation leaves a live token
		// behind on every cancelled control call.
		logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), veeamControlTimeout)
		defer cancel()
		_ = client.Logout(logoutCtx)
	}
	return client, release, nil
}

// recordControlSession writes the session a start or stop returned inline.
//
// Best-effort, and deliberately so: the action has already happened on the
// Veeam side by the time this runs, so a failed write must not fail the
// request and hand the operator a "the job did not start" for a job that did.
// The poll loop picks the session up within the sync interval regardless — what
// is lost until then is only the nexara_initiated / nexara_stopped flag, which
// the audit row below records independently.
func (h *VeeamHandler) recordControlSession(c fiber.Ctx, server db.VeeamServer, s *veeam.Session, initiated, stopped bool) {
	sessionID, err := uuid.Parse(s.ID)
	if err != nil {
		return
	}
	// creation_time is NOT NULL, and a zero time would be pruned by the very
	// next retention sweep and would corrupt the session watermark that drives
	// the whole session poll. Fall back to now(): this session was created by
	// the call that just returned, so "now" is right to within the round trip.
	created := s.CreationTime.Time
	if s.CreationTime.IsZero() {
		created = time.Now()
	}

	// Detached from the request for the reason StopSession spells out: the
	// action has already happened on the Veeam side, and a cancelled request
	// must not be what loses the nexara_stopped flag.
	writeCtx, cancelWrite := detachedWriteContext(c)
	defer cancelWrite()

	if err := h.queries.UpsertVeeamControlSession(writeCtx, db.UpsertVeeamControlSessionParams{
		VeeamServerID:   server.ID,
		VeeamID:         sessionID,
		JobVeeamID:      optionalUUIDFromString(s.JobID),
		Name:            auditSafe(s.Name),
		SessionType:     auditSafe(s.SessionType),
		PlatformName:    auditSafe(s.PlatformName),
		PlatformID:      optionalUUIDFromString(s.PlatformID),
		State:           auditSafe(s.State),
		Result:          auditSafe(s.Result.Result),
		ResultMessage:   auditSafe(s.Result.Message),
		IsCanceled:      s.Result.IsCanceled,
		Algorithm:       auditSafe(s.Algorithm),
		Bottleneck:      auditSafe(s.Progress.Bottleneck),
		Duration:        auditSafe(s.Progress.Duration),
		ProcessingRate:  auditSafe(s.Progress.ProcessingRate),
		ProcessedSize:   s.Progress.ProcessedSize,
		ReadSize:        s.Progress.ReadSize,
		TransferredSize: s.Progress.TransferredSize,
		ProgressPercent: clampPercent(s.ProgressPercent),
		CreationTime:    created,
		EndTime:         optionalTimestampFromTime(s.EndTime.Or()),
		InitiatedBy:     auditSafe(s.InitiatedBy),
		NexaraInitiated: initiated,
		NexaraStopped:   stopped,
	}); err != nil {
		slog.Error("veeam: recording a control session failed; the action itself succeeded",
			"veeam_server_id", server.ID, "session", sessionID, "error", err)
	}
}

// markSessionStopped records that Nexara asked one session to stop.
//
// Best-effort: the stop has already been accepted by Veeam, so a failed write
// cannot fail the request. It does mean veeam_job_failed may fire once for a
// cancellation the operator asked for, which is why the failure is logged
// loudly rather than swallowed — the audit row is then the only record that
// the "failure" was deliberate.
func (h *VeeamHandler) markSessionStopped(c fiber.Ctx, serverID, sessionID uuid.UUID) {
	writeCtx, cancel := detachedWriteContext(c)
	defer cancel()

	if _, err := h.queries.MarkVeeamSessionStopped(writeCtx, db.MarkVeeamSessionStoppedParams{
		VeeamServerID: serverID,
		VeeamID:       sessionID,
	}); err != nil {
		slog.Error("veeam: recording an operator's session stop failed; the stop itself succeeded",
			"veeam_server_id", serverID, "session", sessionID, "error", err)
	}
}

// detachedWriteContext returns a context for a write that must survive the
// request being cancelled.
//
// Everything these handlers persist describes a mutation Veeam has ALREADY
// accepted, so a client disconnect can only ever lose Nexara's record of it —
// never undo it. Bounded rather than unbounded: a detached write still has to
// stop if the database is unreachable.
func detachedWriteContext(c fiber.Ctx) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(c.Context()), veeamControlWriteTimeout)
}

// veeamControlWriteTimeout bounds a detached write. Short: these are single
// upserts against a local pool, and a long one would keep a goroutine alive
// well past the request that spawned it.
const veeamControlWriteTimeout = 10 * time.Second

// auditJob records a job control action against the cluster it happened on.
//
// Never through VeeamHandler.audit: that files under a NULL cluster because a
// Veeam SERVER is not a per-cluster resource, but a job run is — filing it
// globally would hide it from exactly the cluster-scoped operator whose
// workload it just touched.
func (h *VeeamHandler) auditJob(c fiber.Ctx, target veeamJobTarget, action string, extra map[string]any) {
	details := map[string]any{
		"veeam_server_id": target.server.ID.String(),
		"server_name":     auditSafe(target.server.Name),
		"job_name":        auditSafe(target.job.Name),
	}
	for k, v := range extra {
		details[k] = v
	}
	AuditLog(c, h.queries, h.eventPub, target.cluster,
		"veeam_job", target.job.VeeamID.String(), action, marshalAuditDetails(details))
}

func (h *VeeamHandler) auditSession(c fiber.Ctx, target veeamSessionTarget, action string, extra map[string]any) {
	details := map[string]any{
		"veeam_server_id": target.server.ID.String(),
		"server_name":     auditSafe(target.server.Name),
		"session_name":    auditSafe(target.session.Name),
	}
	for k, v := range extra {
		details[k] = v
	}
	AuditLog(c, h.queries, h.eventPub, target.cluster,
		"veeam_session", target.session.VeeamID.String(), action, marshalAuditDetails(details))
}

func (h *VeeamHandler) publishVeeamChange(c fiber.Ctx, action string) {
	h.eventPub.SystemEvent(c.Context(), events.KindVeeamChange, action)
}

// renderVeeamControlError maps a client error to the status the SPA should
// act on.
//
// The 422-not-401 rule is the same one renderVeeamProbeError documents: the
// SPA reads a 401 as ITS OWN session expiring, refreshes and replays the
// request, which spends a second failed domain logon per click and logs the
// operator out if the refresh fails.
//
// A 400 from Veeam becomes 409, not 400. "…job is not currently running" is
// not a malformed request — it is a request that was fine when the operator
// clicked and is no longer true, which is what 409 exists to say and what
// tells the UI to refetch rather than to blame the input.
func renderVeeamControlError(c fiber.Ctx, err error) error {
	var apiErr *veeam.APIError
	switch {
	case errors.Is(err, veeam.ErrAuthFailed):
		return c.Status(fiber.StatusUnprocessableEntity).JSON(fiber.Map{
			"error":   "veeam_auth_failed",
			"message": err.Error(),
		})
	case errors.Is(err, veeam.ErrInvalidInput):
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	case errors.Is(err, veeam.ErrPlatformUnsupported):
		// The Proxmox-job 400. Reachable here if Veeam ever narrows which job
		// types these verbs accept, and worth its own message: nothing the
		// operator can do to the request would fix it.
		return fiber.NewError(fiber.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, veeam.ErrUnreachable):
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	case errors.As(err, &apiErr) && apiErr.StatusCode == fiber.StatusBadRequest:
		return fiber.NewError(fiber.StatusConflict, err.Error())
	case errors.As(err, &apiErr) && apiErr.StatusCode == fiber.StatusNotFound:
		return fiber.NewError(fiber.StatusNotFound,
			"The Veeam server no longer has this job or session. "+err.Error())
	default:
		// A fiber.Error built above by controlClient must pass through with
		// its own status rather than being flattened to 502.
		var fe *fiber.Error
		if errors.As(err, &fe) {
			return fe
		}
		return fiber.NewError(fiber.StatusBadGateway, err.Error())
	}
}

// marshalAuditDetails renders an audit details map, falling back to an empty
// object rather than losing the row. Marshalled rather than concatenated: a
// job name containing a quote would otherwise produce a malformed blob.
func marshalAuditDetails(details map[string]any) json.RawMessage {
	encoded, err := json.Marshal(details)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

// optionalUUIDFromString maps a Veeam id string to a nullable column. An
// unparseable or absent id becomes SQL NULL rather than the zero uuid, which
// would collide with every other row that failed to parse.
func optionalUUIDFromString(s string) pgtype.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: id, Valid: true}
}

func optionalTimestampFromTime(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// clampPercent bounds a percentage from an upstream server into the int32 the
// column holds. Veeam reports 0-100; anything else is a server inventing
// values, and a silent int32 conversion of one would be a gosec finding rather
// than a number worth storing.
func clampPercent(v int) int32 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return int32(v)
}
