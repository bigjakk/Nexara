package handlers

import (
	"context"
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
		return mapProxmoxError(err)
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
		return mapProxmoxError(err)
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
// The digit bound is a sanity limit on the input, NOT the overflow guard —
// see boundedOffset. time.Duration is int64 nanoseconds and tops out at
// ~292 years (2562047h47m16s), so `15251w` — five digits, comfortably inside
// this pattern — wraps negative and yields a wall clock in 2318. A future
// `since` is the "-- No entries --" symptom this validation exists to remove,
// so the real check has to happen before the multiply.
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
//   - Proxmox's own wall-clock strings, passed through unchanged — the caller
//     wrote a wall clock, and the node reads it as one;
//   - a relative offset ("-1h", "30m ago"), which is what people reach for
//     first and which Proxmox rejects outright;
//   - a unix timestamp, which is unambiguous and trivially machine-generated.
//
// The latter two name an absolute instant, and are rendered into node-local
// wall clock using nodeOffset — because the node hands the string to
// journalctl, which reads it in the NODE's timezone.
//
// Getting this wrong is not cosmetic. Rendering "1h ago" as UTC against a node
// on a UTC-7 zone produces a wall clock the node reads as seven hours in its
// own FUTURE, and journalctl answers with its literal "-- No entries --" — the
// exact symptom this endpoint was fixed to stop producing. Verified against a
// node running a zone behind UTC.
//
// DST: nodeOffset is the offset *now*, so a window spanning a transition is off
// by the DST delta for part of its span. Harmless in the autumn direction (the
// window merely widens). In the spring direction it shifts `since` up to an
// hour later, and a short lookback landing just after spring-forward can render
// a wall clock inside the nonexistent local hour — which glibc resolves with
// the pre-transition offset, i.e. slightly after now, giving an empty result.
// Reachable only by the shortest preset, twice a year; the cost of fixing it is
// a full tzdata resolution at the target instant, which is not worth it. If
// someone reports an empty "Last 1h" on a DST Sunday, this is why.
func normalizeSyslogTime(param, value string, nodeOffset time.Duration) (string, error) {
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
		d, ok := boundedOffset(n, relativeUnit[m[2]])
		if !ok {
			return "", syslogLookbackError(param)
		}
		return nodeWallClock(time.Now().Add(-d), nodeOffset), nil
	}
	if ts, err := strconv.ParseInt(value, 10, 64); err == nil {
		if !plausibleUnixSeconds(ts) {
			return "", syslogTimeError(param)
		}
		t := time.Unix(ts, 0)
		if time.Since(t) > maxSyslogLookback {
			return "", syslogLookbackError(param)
		}
		return nodeWallClock(t, nodeOffset), nil
	}
	return "", syslogTimeError(param)
}

// needsNodeClock reports whether a since/until value names an absolute instant
// that has to be re-rendered into the node's wall clock.
//
// A value already in Proxmox's wall-clock form does not — the caller wrote it
// in the node's terms. An empty one uses the default, which is a whole date and
// safe at any offset. Malformed input does not either: it is about to be
// rejected with a 400, and matching it here bought a Proxmox round trip on
// every typo. TestNeedsNodeClock_MatchesNormalization pins this to the set of
// values normalizeSyslogTime actually re-renders.
func needsNodeClock(value string) bool {
	if value == "" || pveWallClock.MatchString(value) {
		return false
	}
	if relativeOffset.MatchString(value) {
		return true
	}
	ts, err := strconv.ParseInt(value, 10, 64)
	return err == nil && plausibleUnixSeconds(ts)
}

// nodeWallClock renders an absolute instant as the wall clock a node on the
// given UTC offset would show for it.
func nodeWallClock(t time.Time, nodeOffset time.Duration) string {
	return t.UTC().Add(nodeOffset).Format("2006-01-02 15:04:05")
}

// nodeUTCOffset asks the node what time it thinks it is.
//
// PVE's /nodes/{node}/time returns both the UTC epoch and the same instant
// shifted into node-local time, so the offset falls out by subtraction — no
// timezone database and no guessing from the reported zone name. A failure is
// not fatal: 0 means "assume UTC", which is the behaviour this had before.
func nodeUTCOffset(ctx context.Context, pxClient *proxmox.Client, node string) time.Duration {
	nt, err := pxClient.GetNodeTime(ctx, node)
	if err != nil {
		slog.Warn("could not read node time; treating syslog timestamps as UTC",
			"node", node, "error", err)
		return 0
	}
	if nt == nil {
		slog.Warn("node time response was empty; treating syslog timestamps as UTC", "node", node)
		return 0
	}
	offset := time.Duration(nt.Localtime-nt.Time) * time.Second
	// Clamped because the offset is applied AFTER the lookback cap has been
	// enforced, so a nonsense value walks the window straight back out of it.
	// A response missing `localtime` decodes as 0, making the offset ~-56
	// years — which renders `since` as a 1970 date and asks the node to scan
	// its entire journal, the very thing maxSyslogLookback exists to prevent.
	// 26h rather than 14h leaves slack for a node whose clock is simply skewed.
	if offset < -maxNodeUTCOffset || offset > maxNodeUTCOffset {
		slog.Warn("node reported an implausible UTC offset; treating syslog timestamps as UTC",
			"node", node, "offset", offset)
		return 0
	}
	return offset
}

// maxNodeUTCOffset bounds a plausible node UTC offset. Real zones span
// -12h..+14h; the extra slack absorbs clock skew.
const maxNodeUTCOffset = 26 * time.Hour

// nodeTimeTimeout bounds the offset lookup. Short, because failing it just
// means "assume UTC" and the syslog read itself still proceeds.
const nodeTimeTimeout = 5 * time.Second

// boundedOffset multiplies a count by a unit, rejecting anything past the
// lookback cap — and doing the comparison on the COUNT, before the multiply,
// because that is where int64 overflow happens. Checking the product instead
// lets a wrapped-negative duration sail through as "not greater than the cap".
func boundedOffset(n int, unit time.Duration) (time.Duration, bool) {
	if unit <= 0 || n < 0 || n > int(maxSyslogLookback/unit) {
		return 0, false
	}
	return time.Duration(n) * unit, true
}

// maxSyslogLookback bounds how far back a syslog window may reach. Journals are
// rotated well inside this, so the cap costs nothing a caller wanted while
// keeping `?since=1` (the epoch) from turning into a full-journal scan.
const maxSyslogLookback = 90 * 24 * time.Hour

// Parsed as UTC even though the caller wrote node-local. The two differ by at
// most ±14h against a 90-day cap, so the boundary is fuzzy by that much and
// nothing else — not worth a round trip to sharpen. (In a mixed request the
// offset has been fetched already, but it stays unused here so that the
// wall-clock branch behaves identically whether or not the other parameter
// happened to need one.)
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

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	rawSince, rawUntil := c.Query("since"), c.Query("until")

	// The node's UTC offset is only needed to render an absolute instant as the
	// wall clock that node would show. A caller who wrote a wall clock already
	// wrote it in the node's terms, so that case costs no extra round trip.
	var nodeOffset time.Duration
	if needsNodeClock(rawSince) || needsNodeClock(rawUntil) {
		// Deadlined: this is best-effort (a failure degrades to UTC), but
		// fiber.Ctx.Context() is context.Background() unless something calls
		// SetContext, and nothing here does — so without this the call inherits
		// only the client's 5-minute timeout and doubles the worst case against
		// a wedged node.
		//
		// Re-fetched per request rather than cached. The value is near-static,
		// so a per-(cluster,node) TTL cache would remove a round trip from
		// every syslog page load; not built yet because /nodes/{node}/time is
		// a cheap local call and the cache is state to invalidate.
		tctx, cancel := context.WithTimeout(c.Context(), nodeTimeTimeout)
		nodeOffset = nodeUTCOffset(tctx, pxClient, nodeName)
		cancel()
	}

	since := rawSince
	if since == "" {
		since = defaultSyslogSince()
	} else if since, err = normalizeSyslogTime("since", since, nodeOffset); err != nil {
		return err
	}
	until := rawUntil
	if until != "" {
		if until, err = normalizeSyslogTime("until", until, nodeOffset); err != nil {
			return err
		}
	}
	service := c.Query("service")

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
		// Same overflow guard as the syslog side. Without it the wrapped
		// duration goes out as a future unix timestamp, which the journal
		// endpoint has no wall-clock cap to catch.
		d, ok := boundedOffset(n, relativeUnit[m[2]])
		if !ok {
			return 0, syslogLookbackError(param)
		}
		return time.Now().Add(-d).Unix(), nil
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
