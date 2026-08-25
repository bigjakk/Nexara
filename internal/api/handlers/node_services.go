package handlers

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// --- Node Services ---

// ListNodeServices handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/services.
func (h *NodeHandler) ListNodeServices(c fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	services, err := pxClient.GetNodeServices(c.Context(), nodeName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list node services")
	}
	return RespondItems(c, services)
}

// ServiceAction handles POST /api/v1/clusters/:cluster_id/nodes/:node_name/services/:service/:action.
func (h *NodeHandler) ServiceAction(c fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "manage", "node", clusterID); err != nil {
		return err
	}
	service := c.Params("service")
	action := c.Params("action")
	if service == "" || action == "" {
		return fiber.NewError(fiber.StatusBadRequest, "service and action are required")
	}
	switch action {
	case "start", "stop", "restart", "reload":
		// valid
	default:
		return fiber.NewError(fiber.StatusBadRequest, "action must be start, stop, restart, or reload")
	}
	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	upid, err := pxClient.ServiceAction(c.Context(), nodeName, service, action)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to "+action+" service "+service)
	}
	TrackTask(c, h.queries, h.eventPub, TrackTaskParams{
		ClusterID:    clusterID,
		Node:         nodeName,
		ResourceType: "node",
		ResourceID:   nodeName,
		Action:       "service_" + action,
		UPID:         upid,
		Description:  action + " service " + service + " on " + nodeName,
		Extra:        map[string]any{"service": service, "action": action},
	})
	return c.JSON(fiber.Map{"status": "ok", "upid": upid})
}

// --- Node Syslog / Journal ---

// pveWallClock matches the wall-clock forms Proxmox accepts for the syslog
// endpoint's since/until: "YYYY-MM-DD" with an optional " HH:MM" or
// " HH:MM:SS". Anything else is rejected here rather than relayed upstream.
var pveWallClock = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}( \d{2}:\d{2}(:\d{2})?)?$`)

// relativeOffset matches the relative forms this API accepts on top of
// Proxmox's own: "-90m", "2h", "7d ago". Sign and " ago" both mean "into the
// past"; there is no forward form, since a future window returns nothing.
// {1,7} digits, not {1,}: `time.Duration(n) * unit` is int64 nanoseconds and
// overflows silently. Unbounded, `?since=9999999999999d` wraps to an arbitrary
// — frequently FUTURE — instant, which is the "-- No entries --" symptom this
// validation exists to remove, reachable straight from the query string.
// 7 digits caps a week-unit offset at ~190,000 years, well inside int64.
var relativeOffset = regexp.MustCompile(`^-?(\d{1,7})\s*([smhdw])(\s+ago)?$`)

var relativeUnit = map[string]time.Duration{
	"s": time.Second,
	"m": time.Minute,
	"h": time.Hour,
	"d": 24 * time.Hour,
	"w": 7 * 24 * time.Hour,
}

// normalizeSyslogTime validates one since/until value and returns the string to
// send to Proxmox.
//
// Three accepted forms:
//
//   - Proxmox's own wall-clock strings, passed through unchanged;
//   - a relative offset ("-1h", "30m ago"), which is what people reach for
//     first and which Proxmox rejects outright;
//   - a unix timestamp, which is unambiguous and trivially machine-generated.
//
// The latter two are rendered as wall-clock UTC. Note the asymmetry that
// creates and that no amount of validation here can remove: the node runs
// journalctl, which reads a wall-clock string in the *node's* local timezone.
// So "-1h" means "one hour before now, read as if the node were on UTC" —
// documented in docs/api-reference.md, and the reason the caller-less default
// below is a whole day back rather than an hour.
func normalizeSyslogTime(param, value string) (string, error) {
	if pveWallClock.MatchString(value) {
		// Passed through in Proxmox's own form, but still bounded: an
		// unbounded `since` makes journalctl walk the whole journal, and with
		// start=-1 the client asks PVE to count every matching line before
		// fetching any — a full scan on the hypervisor per request.
		if err := withinLookback(param, value); err != nil {
			return "", err
		}
		return value, nil
	}
	if m := relativeOffset.FindStringSubmatch(value); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return "", syslogTimeError(param)
		}
		d := time.Duration(n) * relativeUnit[m[2]]
		if d > maxSyslogLookback {
			return "", syslogLookbackError(param)
		}
		return time.Now().UTC().Add(-d).Format("2006-01-02 15:04:05"), nil
	}
	if ts, err := strconv.ParseInt(value, 10, 64); err == nil {
		if !plausibleUnixSeconds(ts) {
			return "", syslogTimeError(param)
		}
		t := time.Unix(ts, 0).UTC()
		if time.Since(t) > maxSyslogLookback {
			return "", syslogLookbackError(param)
		}
		return t.Format("2006-01-02 15:04:05"), nil
	}
	return "", syslogTimeError(param)
}

// maxSyslogLookback bounds how far back a syslog window may reach. Journals are
// rotated well inside this, so the cap costs nothing a caller wanted while
// keeping `?since=1` (the epoch) from turning into a full-journal scan.
const maxSyslogLookback = 90 * 24 * time.Hour

func withinLookback(param, wallClock string) error {
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		t, err := time.ParseInLocation(layout, wallClock, time.UTC)
		if err != nil {
			continue
		}
		if time.Since(t) > maxSyslogLookback {
			return syslogLookbackError(param)
		}
		return nil
	}
	return syslogTimeError(param)
}

func syslogLookbackError(param string) error {
	return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf(
		"%q reaches further back than %d days", param, int(maxSyslogLookback.Hours()/24)))
}

// maxUnixSeconds is the upper bound on an accepted unix timestamp, ~year 5138.
// Beyond it the value is almost certainly milliseconds, which would render a
// five-digit year — rejected by the pattern Proxmox matches `since` against,
// turning a client's unit mistake back into the 500 this validation removes.
const maxUnixSeconds = 99999999999

func plausibleUnixSeconds(ts int64) bool {
	return ts > 0 && ts < maxUnixSeconds
}

func syslogTimeError(param string) error {
	return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf(
		"invalid %q: expected 'YYYY-MM-DD', 'YYYY-MM-DD HH:MM:SS', a relative offset such as '-1h' or '30m ago', or a unix timestamp",
		param))
}

// defaultSyslogSince is the window used when the caller names none.
//
// A full day back, and date-only on purpose. The node parses this string in
// its own local timezone, so the previous default — today's date in UTC — was
// a *future* timestamp for every node west of UTC, and journalctl answered
// with the literal "-- No entries --" that reads like a node with no logs.
// A day covers the largest real UTC offset (±14h) with room to spare, so the
// window starts in the past wherever the node is.
func defaultSyslogSince() string {
	return time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02")
}

// GetNodeSyslog handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/syslog.
func (h *NodeHandler) GetNodeSyslog(c fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}
	// Default start to -1 (fetch newest entries) unless explicitly provided.
	startStr := c.Query("start")
	start := -1
	if startStr != "" {
		start, _ = strconv.Atoi(startStr)
	}
	// Clamped at both ends: Atoi maps a non-numeric value to 0, and a negative
	// one passed straight through to Proxmox.
	limit, err := strconv.Atoi(c.Query("limit", "500"))
	if err != nil || limit < 1 {
		limit = 500
	} else if limit > maxSyslogEntries {
		limit = maxSyslogEntries
	}

	since := c.Query("since")
	if since == "" {
		since = defaultSyslogSince()
	} else if since, err = normalizeSyslogTime("since", since); err != nil {
		return err
	}
	until := c.Query("until")
	if until != "" {
		if until, err = normalizeSyslogTime("until", until); err != nil {
			return err
		}
	}
	service := c.Query("service")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	entries, total, err := pxClient.GetNodeSyslog(c.Context(), nodeName, start, limit, since, until, service)
	if err != nil {
		// Logged, not returned: the upstream string names internal paths and
		// the Proxmox endpoint being called, and the caller has already been
		// told which of their inputs could be at fault by the 400s above.
		slog.Error("node syslog read failed", "cluster_id", clusterID, "node", nodeName, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node syslog")
	}
	return RespondList(c, entries, int64(total))
}

// GetNodeJournal handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/journal.
//
// The counterpart to GetNodeSyslog for the case it cannot express: ?lastentries=N,
// "just show me the last N lines", which is the common ask. Its since/until are
// unix timestamps, so unlike syslog they carry no dependence on the node's
// timezone; relative offsets are accepted and converted here.
func (h *NodeHandler) GetNodeJournal(c fiber.Ctx) error {
	clusterID, nodeName, err := h.resolveNodeName(c)
	if err != nil {
		return err
	}
	if err := requireClusterPerm(c, "view", "node", clusterID); err != nil {
		return err
	}

	var opts proxmox.JournalOptions
	if v := c.Query("since"); v != "" {
		if opts.Since, err = parseJournalTime("since", v); err != nil {
			return err
		}
	}
	if v := c.Query("until"); v != "" {
		if opts.Until, err = parseJournalTime("until", v); err != nil {
			return err
		}
	}
	if v := c.Query("lastentries"); v != "" {
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 1 {
			return fiber.NewError(fiber.StatusBadRequest, "invalid \"lastentries\": expected a positive integer")
		}
		opts.LastEntries = min(n, maxJournalEntries)
	}
	if opts.Since == 0 && opts.Until == 0 && opts.LastEntries == 0 && c.Query("startcursor") == "" && c.Query("endcursor") == "" {
		// Unbounded, this walks the node's entire journal. The syslog endpoint
		// defaults to a time window for the same reason; here the natural
		// bound is a line count.
		opts.LastEntries = defaultJournalEntries
	}
	opts.StartCursor = c.Query("startcursor")
	opts.EndCursor = c.Query("endcursor")

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}
	lines, err := pxClient.GetNodeJournal(c.Context(), nodeName, opts)
	if err != nil {
		slog.Error("node journal read failed", "cluster_id", clusterID, "node", nodeName, "error", err)
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get node journal")
	}
	return RespondItems(c, lines)
}

const (
	defaultJournalEntries = 500
	maxJournalEntries     = 5000
	maxSyslogEntries      = 5000
)

// parseJournalTime resolves one journal since/until to a unix timestamp,
// accepting the same forms as the syslog endpoint.
func parseJournalTime(param, value string) (int64, error) {
	if ts, err := strconv.ParseInt(value, 10, 64); err == nil {
		if !plausibleUnixSeconds(ts) {
			return 0, syslogTimeError(param)
		}
		return ts, nil
	}
	if m := relativeOffset.FindStringSubmatch(value); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, syslogTimeError(param)
		}
		return time.Now().Add(-time.Duration(n) * relativeUnit[m[2]]).Unix(), nil
	}
	// Wall-clock forms are read as UTC. journalctl would read them as node
	// local time, but this endpoint sends an absolute timestamp, so the
	// interpretation has to be pinned somewhere — UTC, matching the rest of
	// the API's timestamps.
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, value, time.UTC); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, syslogTimeError(param)
}
