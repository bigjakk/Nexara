package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/nut"
	"github.com/bigjakk/nexara/internal/upscontrol"
)

// UPS commands and settings (2b): an instant command, or a variable set, run
// on a UPS through its NUT server's stored account. The request records the
// run, sends it to upsd and audits upsd's answer, as TrackTask does for a
// Proxmox task; a run upsd tracks is then watched for the driver's answer on
// the connection that sent it (P8), and its outcome lands in ups_commands.

const (
	// upsDispatchTimeout bounds what the request waits for: dialling, tracking
	// on, and upsd's answer to the command.
	upsDispatchTimeout = 15 * time.Second
	// upsRunsInFlight caps the runs being watched at once, each holding a
	// connection to upsd, and upsRunsPerServer the runs on one NUT server, so
	// a server whose driver never answers cannot take them all.
	upsRunsInFlight  = 16
	upsRunsPerServer = 4
	// upsRunAbandoned: a run unfinished this long has no watch left. The
	// longest a run is watched is about 95 s (dispatch, the one-minute wait,
	// the read-back, the record).
	upsRunAbandoned = 3 * time.Minute
	// upsRunsListed is how many runs the history returns.
	upsRunsListed = 50
)

// upsConfirmRefusal is the 400 for a command or setting that needs the UPS's
// name typed, sent without it.
const upsConfirmRefusal = "confirm must be the UPS's name: this cuts the power the UPS supplies"

// upsRunResponse is one run in the API.
type upsRunResponse struct {
	ID              uuid.UUID  `json:"id"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	Argument        string     `json:"argument"`
	Previous        *string    `json:"previous"`
	Status          string     `json:"status"`
	Result          string     `json:"result"`
	RequestedAt     time.Time  `json:"requested_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	UserEmail       string     `json:"user_email"`
	UserDisplayName string     `json:"user_display_name"`
}

func toUpsRunResponse(r db.UpsCommand, email, name string) upsRunResponse {
	out := upsRunResponse{
		ID: r.ID, Kind: r.Kind, Name: r.Name, Argument: r.Argument, Status: r.Status, Result: r.Result,
		RequestedAt: r.RequestedAt, UserEmail: email, UserDisplayName: name,
	}
	if r.Previous.Valid {
		out.Previous = &r.Previous.String
	}
	if r.FinishedAt.Valid {
		out.FinishedAt = &r.FinishedAt.Time
	}
	return out
}

// RunCommand handles POST /api/v1/ups-devices/:id/commands.
func (h *NutHandler) RunCommand(c fiber.Ctx, p *apischema.Params) error {
	if err := requirePerm(c, "execute", "ups"); err != nil {
		return err
	}
	dev, err := h.fetchDevice(c, p)
	if err != nil {
		return err
	}
	name := p.String("command")
	param := p.String("parameter")
	if !offersCommand(dev, name) {
		return fiber.NewError(fiber.StatusNotFound, "This UPS does not offer that command")
	}
	if err := nut.CheckCommand(name, param); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if upscontrol.CommandTier(name) == upscontrol.TierCutsPower {
		if err := h.requireNodesItPowers(c, dev.ID); err != nil {
			return err
		}
	}
	// Last before anything is written, after the refusals that only read.
	if upscontrol.CommandConfirm(name) == upscontrol.ConfirmTyped && p.String("confirm") != dev.UpsName {
		return fiber.NewError(fiber.StatusBadRequest, upsConfirmRefusal)
	}
	return h.dispatch(c, dev, "command", name, param, pgtype.Text{})
}

// SetVariable handles PUT /api/v1/ups-devices/:id/vars/:name. A variable that
// can change the power the UPS supplies, which is any but the few that only
// describe it, is gated as cutting power is: battery.charge.low at 0 lets the
// next outage run the battery flat with no warning.
func (h *NutHandler) SetVariable(c fiber.Ctx, p *apischema.Params) error {
	if err := requirePerm(c, "manage", "ups"); err != nil {
		return err
	}
	dev, err := h.fetchDevice(c, p)
	if err != nil {
		return err
	}
	name, value := p.String("name"), p.String("value")
	v, ok := writableVars(dev)[name]
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, "This UPS does not let that variable be set")
	}
	if err := v.Validate(name, value); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if upscontrol.VarAffectsPower(name) {
		if err := requirePerm(c, "execute", "ups"); err != nil {
			return err
		}
		if err := h.requireNodesItPowers(c, dev.ID); err != nil {
			return err
		}
	}
	if upscontrol.VarConfirm(name) == upscontrol.ConfirmTyped && p.String("confirm") != dev.UpsName {
		return fiber.NewError(fiber.StatusBadRequest, upsConfirmRefusal)
	}
	var vars map[string]string
	_ = json.Unmarshal(dev.Vars, &vars)
	previous := pgtype.Text{}
	if old, ok := vars[name]; ok {
		previous = pgtype.Text{String: old, Valid: true}
	}
	return h.dispatch(c, dev, "set", name, value, previous)
}

// ListCommandRuns handles GET /api/v1/ups-devices/:id/command-runs.
func (h *NutHandler) ListCommandRuns(c fiber.Ctx, p *apischema.Params) error {
	dev, err := h.fetchDevice(c, p)
	if err != nil {
		return err
	}
	if _, err := h.queries.MarkUnfinishedUpsCommandsUnconfirmed(c.Context(), db.MarkUnfinishedUpsCommandsUnconfirmedParams{
		UpsID: dev.ID, Before: time.Now().Add(-upsRunAbandoned),
	}); err != nil {
		slog.Warn("ups: marking abandoned command runs failed", "ups", dev.UpsName, "error", err)
	}
	rows, err := h.queries.ListUpsCommands(c.Context(), db.ListUpsCommandsParams{UpsID: dev.ID, RowLimit: upsRunsListed})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list UPS command runs")
	}
	items := make([]upsRunResponse, 0, len(rows))
	for _, r := range rows {
		items = append(items, toUpsRunResponse(db.UpsCommand{
			ID: r.ID, UpsID: r.UpsID, UserID: r.UserID, Kind: r.Kind, Name: r.Name, Argument: r.Argument,
			Previous: r.Previous, Status: r.Status, TrackingID: r.TrackingID, Result: r.Result,
			RequestedAt: r.RequestedAt, FinishedAt: r.FinishedAt,
		}, r.UserEmail, r.UserDisplayName))
	}
	return c.JSON(fiber.Map{"items": items, "total": len(items)})
}

func offersCommand(dev db.ListUpsDevicesRow, name string) bool {
	var cmds []upscontrol.Command
	_ = json.Unmarshal(dev.Commands, &cmds)
	for _, cmd := range cmds {
		if cmd.Name == name {
			return true
		}
	}
	return false
}

func writableVars(dev db.ListUpsDevicesRow) map[string]upscontrol.Var {
	var vars map[string]upscontrol.Var
	_ = json.Unmarshal(dev.RwVars, &vars)
	return vars
}

// requireNodesItPowers refuses a command that cuts a UPS's power to a caller
// who could not shut down every node it powers or has powered: execute:ups is
// global, and the nodes may be in any cluster. A UPS Nexara knows nothing it
// powers could power anything, so it takes manage:node everywhere.
func (h *NutHandler) requireNodesItPowers(c fiber.Ctx, ups uuid.UUID) error {
	clusters, err := h.queries.ListUpsFedClusters(c.Context(), ups)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list what the UPS powers")
	}
	if len(clusters) == 0 {
		return requirePerm(c, "manage", "node")
	}
	for _, cluster := range clusters {
		if err := requireClusterPerm(c, "manage", "node", cluster); err != nil {
			return err
		}
	}
	return nil
}

// dispatch records a run, sends it to upsd and audits upsd's answer, leaving
// a run upsd tracks to watchRun. It answers 202 with the run.
func (h *NutHandler) dispatch(c fiber.Ctx, dev db.ListUpsDevicesRow, kind, name, arg string, previous pgtype.Text) error {
	server, err := h.queries.GetNutServer(c.Context(), dev.ServerID)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get NUT server")
	}
	if server.Username == "" || server.PasswordEncrypted == "" {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error":   "ups_no_account",
			"message": "Add an account from the NUT server's upsd.users to it in Nexara: upsd runs commands and sets variables only for one.",
		})
	}
	password, err := crypto.Decrypt(server.PasswordEncrypted, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt stored password")
	}
	// A stop, such as cancelling a shutdown in progress, still goes out with
	// every watch taken; it is sent untracked.
	watched := h.takeRun(server.ID)
	if !watched && (kind != "command" || !upscontrol.IsStop(name)) {
		return fiber.NewError(fiber.StatusTooManyRequests, "Too many UPS commands are waiting on a driver; try again shortly")
	}
	var conn *nut.Client
	handedOff := false
	defer func() {
		// Every way out but the hand-off to watchRun, a panic included.
		if handedOff {
			return
		}
		if conn != nil {
			_ = conn.Close()
		}
		if watched {
			h.releaseRun(server.ID)
		}
	}()
	userID, _ := c.Locals("user_id").(uuid.UUID)
	run, err := h.queries.InsertUpsCommand(c.Context(), db.InsertUpsCommandParams{
		UpsID: dev.ID, UserID: UserUUID(userID), Kind: kind, Name: name, Argument: arg, Previous: previous,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to record the command")
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Context()), upsDispatchTimeout)
	var out upsSendOutcome
	conn, out = h.send(ctx, server, password, dev.UpsName, kind, name, arg, watched)
	cancel()
	run.Status, run.TrackingID, run.Result = out.status, out.trackingID, out.result
	if run.Status != "pending" {
		run.FinishedAt = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	h.recordRun(c.Context(), run)
	if out.accepted || out.denied {
		if err := h.queries.SetNutServerCommandsVerified(c.Context(), db.SetNutServerCommandsVerifiedParams{ID: server.ID, Verified: out.accepted}); err != nil {
			slog.Warn("ups: recording the account's check failed", "server", server.Name, "error", err)
		}
	}
	action := "ups_command_sent"
	if run.Status == "failed" {
		action = "ups_command_failed"
	}
	details := map[string]any{
		"run_id": run.ID, "ups": dev.UpsName, "server": dev.ServerName, "kind": kind, "name": name,
		"tier": upscontrol.CommandTier(name), "status": run.Status,
	}
	if kind == "set" {
		delete(details, "tier") // a variable's values stay in ups_commands, read on view:ups
	}
	if out.code != "" {
		details["error"] = out.code
	}
	blob, _ := json.Marshal(details)
	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "ups_device", dev.ID.String(), action, blob)
	if conn != nil && watched {
		handedOff = true
		go h.watchRun(conn, run, dev.UpsName, server.ID)
	}
	h.runChanged(c.Context())
	return c.Status(fiber.StatusAccepted).JSON(toUpsRunResponse(run, "", ""))
}

// upsSendOutcome is how far a run got with upsd.
type upsSendOutcome struct {
	status, trackingID, result string
	// accepted: upsd took the command, so the account may run commands;
	// denied: upsd refused the account.
	accepted, denied bool
	// code is upsd's ERR code, or "unreachable", for the audit row.
	code string
}

// send dials and sends a run, tracked when track is set and upsd can track.
func (h *NutHandler) send(ctx context.Context, server db.NutServer, password, ups, kind, name, arg string, track bool) (*nut.Client, upsSendOutcome) {
	conn, err := h.dial(ctx, nut.Config{
		Host: server.Host, Port: int(server.Port), Username: server.Username, Password: password,
		TLS: nut.TLSMode(server.TlsMode), VerifyTLS: server.VerifyTls, Fingerprint: server.TlsFingerprint,
		Commands: true,
	})
	if err != nil {
		return nil, upsSendOutcome{status: "failed", result: "Could not reach the NUT server: " + auditSafe(err.Error()), code: "unreachable"}
	}
	_, protocol, err := conn.Versions(ctx)
	tracked := track && err == nil && trackable(protocol)
	if tracked {
		err = conn.SetTracking(ctx, true)
	}
	if err != nil {
		_ = conn.Close()
		return nil, refusal(err, kind)
	}
	var id string
	if kind == "set" {
		id, err = conn.SetVar(ctx, ups, name, arg)
	} else {
		id, err = conn.InstantCommand(ctx, ups, name, arg)
	}
	var pe *nut.ProtocolError
	switch {
	case errors.As(err, &pe):
		_ = conn.Close()
		return nil, refusal(err, kind)
	case err != nil:
		_ = conn.Close()
		return nil, upsSendOutcome{status: "unconfirmed", result: "The connection to upsd dropped before it answered: " + auditSafe(err.Error()), code: "connection_lost"}
	case !track:
		// Never handed to a watch, whatever upsd answered: it holds no slot.
		_ = conn.Close()
		return nil, upsSendOutcome{status: "unconfirmed", accepted: true,
			result: "upsd took it. Nexara did not wait for the driver's answer: as many commands as it watches at once were already waiting."}
	case id == "":
		_ = conn.Close()
		return nil, upsSendOutcome{status: "unconfirmed", accepted: true,
			result: "upsd took it. This NUT server cannot report whether the driver did: that needs NUT 2.8."}
	}
	return conn, upsSendOutcome{status: "pending", trackingID: id, accepted: true}
}

// trackable says whether upsd's protocol has command tracking (1.3, NUT 2.8).
func trackable(protocol string) bool {
	major, minor, _ := strings.Cut(strings.TrimSpace(protocol), ".")
	ma, err1 := strconv.Atoi(major)
	mi, err2 := strconv.Atoi(minor)
	return err1 == nil && err2 == nil && (ma > 1 || ma == 1 && mi >= 3)
}

// refusal words upsd's ERR for a run, or a connection that broke before the
// command was sent.
func refusal(err error, kind string) upsSendOutcome {
	var pe *nut.ProtocolError
	if !errors.As(err, &pe) {
		return upsSendOutcome{status: "failed", result: "Could not reach the NUT server: " + auditSafe(err.Error()), code: "unreachable"}
	}
	out := upsSendOutcome{status: "failed", code: auditSafe(pe.Code)}
	switch pe.Code {
	case "ACCESS-DENIED":
		out.denied = true
		out.result = "upsd refused the stored account: give it instcmds in upsd.users to run commands"
		if kind == "set" {
			out.result = "upsd refused the stored account: give it actions = SET in upsd.users to set variables"
		}
	case "CMD-NOT-SUPPORTED":
		out.result = "The UPS no longer offers this command"
	case "VAR-NOT-SUPPORTED":
		out.result = "The UPS no longer has this variable"
	case "READONLY":
		out.result = "upsd says this variable cannot be set"
	case "TOO-LONG":
		out.result = "upsd says the value is too long"
	case "INVALID-VALUE":
		out.result = "upsd refused the value: it is not one the UPS allows"
	case "DATA-STALE", "DRIVER-NOT-CONNECTED":
		out.result = "upsd has no live connection to this UPS's driver"
	case "UNKNOWN-UPS":
		out.result = "upsd no longer knows this UPS"
	default:
		out.result = "upsd refused it (ERR " + auditSafe(pe.Code) + ")"
	}
	return out
}

// watchRun asks for a tracked run's outcome once a trackingPoll, on the
// connection that sent it (an id repeats across upsd restarts), until the
// driver answers or trackingWait passes, and records it. A variable that
// took is read back.
func (h *NutHandler) watchRun(conn *nut.Client, run db.UpsCommand, ups string, server uuid.UUID) {
	status, result := "unconfirmed", "The driver had not answered after a minute"
	defer func() {
		if r := recover(); r != nil {
			slog.Error("ups: watching a command run panicked", "run", run.ID, "panic", r)
			status, result = "unconfirmed", "Nexara stopped watching before the driver answered"
		}
		_ = conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		run.Status, run.Result = status, result
		h.recordRun(ctx, run)
		h.runChanged(ctx)
		cancel()
		h.releaseRun(server)
	}()
	ctx, cancel := context.WithTimeout(h.shutdownCtx, h.trackingWait)
	defer cancel()
	got, err := conn.Tracking(ctx, run.TrackingID)
	for err == nil && got == nut.TrackingPending {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		case <-time.After(h.trackingPoll):
			got, err = conn.Tracking(ctx, run.TrackingID)
		}
	}
	switch {
	case h.shutdownCtx.Err() != nil:
		result = "Nexara stopped before the driver answered"
	case ctx.Err() != nil:
		// The wait ran out: status and result as set above.
	case err != nil:
		result = "The connection to upsd dropped before the driver answered: " + auditSafe(err.Error())
	default:
		status, result = trackedOutcome(got, run.Kind)
	}
	if status == "succeeded" && run.Kind == "set" {
		rctx, rcancel := context.WithTimeout(h.shutdownCtx, upsDispatchTimeout)
		if v, err := conn.GetVar(rctx, ups, run.Name); err == nil && v != run.Argument {
			result = "The UPS now reports " + strconv.Quote(v)
		}
		rcancel()
	}
}

// trackedOutcome words the driver's answer.
func trackedOutcome(t nut.TrackingStatus, kind string) (status, result string) {
	switch t {
	case nut.TrackingSuccess:
		return "succeeded", ""
	case nut.TrackingFailed:
		return "failed", "The driver reported that it failed"
	case nut.TrackingInvalid:
		if kind == "set" {
			return "failed", "The driver refused the value"
		}
		return "failed", "The driver refused it: not valid now, or with that parameter"
	case nut.TrackingUnknown:
		return "failed", "The driver does not carry it out"
	}
	return "unconfirmed", "The driver had not answered after a minute"
}

// recordRun stores a run's progress; one already finished does not move.
// Text upsd sent is made valid UTF-8 first, or Postgres refuses the row and the
// run stays unfinished.
func (h *NutHandler) recordRun(ctx context.Context, run db.UpsCommand) {
	if _, err := h.queries.UpdateUpsCommand(ctx, db.UpdateUpsCommandParams{
		ID: run.ID, Status: run.Status, TrackingID: strings.ToValidUTF8(run.TrackingID, "?"),
		Result: strings.ToValidUTF8(run.Result, "?"),
	}); err != nil {
		slog.Warn("ups: recording a command run failed", "run", run.ID, "error", err)
	}
}

// takeRun takes a slot for watching a run on a server, if one is free.
func (h *NutHandler) takeRun(server uuid.UUID) bool {
	h.runMu.Lock()
	defer h.runMu.Unlock()
	if h.serverRuns[server] >= upsRunsPerServer {
		return false
	}
	select {
	case h.runSlots <- struct{}{}:
		h.serverRuns[server]++
		return true
	default:
		return false
	}
}

// releaseRun gives back a slot takeRun took. It never blocks, so a release
// with no slot to give back cannot hold runMu and stop every later run.
func (h *NutHandler) releaseRun(server uuid.UUID) {
	h.runMu.Lock()
	defer h.runMu.Unlock()
	h.serverRuns[server]--
	if h.serverRuns[server] <= 0 {
		delete(h.serverRuns, server)
	}
	select {
	case <-h.runSlots:
	default:
	}
}

func (h *NutHandler) runChanged(ctx context.Context) {
	if h.eventPub != nil {
		h.eventPub.SystemEvent(ctx, events.KindUPSChange, "command_run")
	}
}
