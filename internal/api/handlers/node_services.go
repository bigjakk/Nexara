package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/proxmox"
)

// --- Node Services ---

// NodeServiceActions is the set of service actions
// POST .../nodes/:node_name/services/:service/:action accepts, in the order
// an operator reaches for them.
//
// Exported because the route's parameter schema declares it as the :action
// enum (internal/api/registry_nodes.go): one list, so the schema and the
// handler cannot drift on what "a valid action" means.
var NodeServiceActions = []string{"start", "stop", "restart", "reload"}

// ListNodeServices handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/services.
func (h *NodeHandler) ListNodeServices(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
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
func (h *NodeHandler) ServiceAction(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	// The schema's enum is NodeServiceActions, so an action outside the four
	// is a 400 that names the parameter before this handler runs.
	service, action := p.String("service"), p.String("action")
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
// on a zone behind UTC produces a wall clock the node reads as being in its
// own FUTURE, and journalctl answers with its literal "-- No entries --" — the
// exact symptom this endpoint was fixed to stop producing. Verified against a
// real node in such a zone, not only in the unit test.
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
func (h *NodeHandler) GetNodeSyslog(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}
	// Both bounds now live in the route's parameter schema: start defaults to
	// -1 (Proxmox's "newest entries") and floors there, limit defaults to 500
	// and is capped at MaxSyslogEntries. They used to be CLAMPED here, so
	// ?limit=50000 answered with a page size the caller never asked for and
	// could not tell from their own — see the declaration for the trade.
	start := int(p.Int("start"))
	limit := int(p.Int("limit"))

	pxClient, err := h.createProxmoxClient(c, clusterID)
	if err != nil {
		return err
	}

	rawSince, rawUntil := p.String("since"), p.String("until")

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
	service := p.String("service")

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
func (h *NodeHandler) GetNodeJournal(c fiber.Ctx, p *apischema.Params) error {
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}

	var opts proxmox.JournalOptions
	if v := p.String("since"); v != "" {
		if opts.Since, err = parseJournalTime("since", v); err != nil {
			return err
		}
	}
	if v := p.String("until"); v != "" {
		if opts.Until, err = parseJournalTime("until", v); err != nil {
			return err
		}
	}
	// Read with OptInt rather than Int because the parameter deliberately
	// carries NO default: 1..MaxJournalEntries is the schema's business, but
	// "the caller named no line count" is what the fallback below turns on,
	// and a default would make every windowed or cursor-paged request look
	// like one that asked for 500 lines.
	if n, supplied := p.OptInt("lastentries"); supplied {
		opts.LastEntries = int(n)
	}
	opts.StartCursor = p.String("startcursor")
	opts.EndCursor = p.String("endcursor")
	if opts.Since == 0 && opts.Until == 0 && opts.LastEntries == 0 &&
		opts.StartCursor == "" && opts.EndCursor == "" {
		// Unbounded, this walks the node's entire journal. The syslog endpoint
		// defaults to a time window for the same reason; here the natural
		// bound is a line count.
		opts.LastEntries = defaultJournalEntries
	}

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

// The line-count bounds the syslog and journal routes carry.
//
// Three of the four are EXPORTED because the routes' parameter schemas
// declare them (internal/api/registry_nodes.go): the bound and the
// handler's own notion of it are one constant, so a future change cannot
// move the cap in one place and leave the docs stating the other.
// defaultJournalEntries stays unexported because it is not a per-parameter
// default — it is the fallback for a request that bounded itself no other
// way, which is a cross-field rule the schema deliberately does not carry.
const (
	defaultJournalEntries = 500

	// MaxJournalEntries caps ?lastentries= on the journal route.
	MaxJournalEntries = 5000
	// MaxSyslogEntries caps ?limit= on the syslog route.
	MaxSyslogEntries = 5000
	// DefaultSyslogLimit is the page size the syslog route uses when the
	// caller names none.
	DefaultSyslogLimit = 500
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
