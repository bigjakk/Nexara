package handlers

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/sync/singleflight"

	"github.com/bigjakk/nexara/internal/api/apischema"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/rolling"
)

// Node temperature sensors.
//
// Proxmox has no API for this: there is no /nodes/{node}/sensors endpoint, and
// stock PVE ships neither lm-sensors nor ipmitool, so anything that shells out
// to `sensors` works only on hosts where the operator installed it. The one
// source that needs nothing added to the host is the kernel's own hwmon tree
// under /sys/class/hwmon, read over SSH with the cluster's stored credentials.
//
// Everything here degrades instead of failing. A cluster with no SSH set up, a
// node whose host key was never pinned, a virtualised node, and a host whose
// chips simply expose no temperature all render as "not available" with a
// reason — never an error banner. Temperatures are a nice-to-have panel on a
// page full of things that do work; taking the page down over them would be
// the wrong trade.

const (
	// nodeSensorsTTL bounds how stale a reading may be. Every uncached request
	// costs an SSH handshake, and temperatures move slowly, so a short cache
	// turns "click through eight nodes in the inventory tree" from eight
	// sessions per visit into one per node per minute.
	nodeSensorsTTL = 60 * time.Second

	// nodeSensorsTimeout bounds the parts of the SSH round trip that honour a
	// context: the TCP dial and the command itself. Reading a handful of sysfs
	// files is near-instant; this exists for the unreachable-host case.
	//
	// It does NOT bound the whole call. golang.org/x/crypto/ssh's handshake
	// (ssh.NewClientConn) takes no context and is capped only by the hardcoded
	// 30s ssh.ClientConfig.Timeout in internal/ssh, so a host that accepts TCP
	// and then stalls the banner — sshd past its MaxStartups limit does exactly
	// this — holds a request for ~30s regardless of the value here. That is the
	// window the singleflight below exists to keep from multiplying.
	nodeSensorsTimeout = 20 * time.Second
)

// nodeSensorsCommand reads every hwmon temperature the kernel exposes in one
// round trip.
//
// Notes on the shape, each of which is load-bearing:
//
//   - `-H` forces the filename prefix. grep prints it only when reading more
//     than one file, and a host with a single hwmon device would otherwise
//     return bare values with nothing to attribute them to.
//   - `.` matches any non-empty line, i.e. "print every file's contents".
//   - `2>/dev/null` drops the per-file read errors some drivers return for
//     unpopulated sensors, and the "No such file" the shell produces when a
//     glob matches nothing and is passed through literally.
//   - `|| true` is required, not defensive: grep exits 1 when nothing matched,
//     which is the normal outcome on a virtualised node with no hwmon devices
//     at all. Without it, "this host has no sensors" arrives as a command
//     failure. It also masks grep's exit 2, so an unreadable hwmon tree is
//     reported as "no sensors" rather than as a fault — accepted, because the
//     alternative is treating the normal empty case as an error.
//
// The command is a constant — no caller input is interpolated into it.
const nodeSensorsCommand = `grep -H . ` +
	`/sys/class/hwmon/hwmon*/name ` +
	`/sys/class/hwmon/hwmon*/temp*_label ` +
	`/sys/class/hwmon/hwmon*/temp*_input ` +
	`/sys/class/hwmon/hwmon*/temp*_max ` +
	`/sys/class/hwmon/hwmon*/temp*_crit ` +
	`2>/dev/null || true`

// hwmonAttrRe matches the per-sensor attribute files, capturing the sensor
// index and which attribute it is. Anything else in the directory is ignored.
var hwmonAttrRe = regexp.MustCompile(`^temp(\d+)_(label|input|max|crit)$`)

// Plausible bounds for a hwmon reading in degrees Celsius. Drivers do report
// nonsense for unpopulated sensors — a value pegged at the type's limit, or a
// raw register that was never scaled — and a card claiming 2147483°C is worse
// than one that quietly omits the row.
//
// The window is deliberately wide: it exists to reject values that cannot be a
// temperature, not to second-guess a chip that reads unusually. A cold room and
// a throttling CPU both stay inside it.
const (
	minPlausibleTempC = -100.0
	maxPlausibleTempC = 250.0
)

// nodeSensorReading is one temperature the kernel exposes.
//
// Chip and Label are reported exactly as the kernel gives them rather than
// being prettified here: "coretemp"/"Package id 0" is what the operator sees in
// `sensors` output on the host, and matching that is more useful than a nicer
// name they then have to map back. Label is empty for chips that publish no
// tempN_label (acpitz, most single-sensor devices); Key is the stable
// within-device identity ("temp1") for the UI to fall back on.
//
// Device is the hwmon directory ("hwmon2"). It is the only thing separating two
// devices that share a name, which is the common case for a host with more than
// one NVMe drive.
type nodeSensorReading struct {
	Chip   string   `json:"chip"`
	Device string   `json:"device"`
	Key    string   `json:"key"`
	Label  string   `json:"label"`
	TempC  float64  `json:"temp_c"`
	HighC  *float64 `json:"high_c,omitempty"`
	CritC  *float64 `json:"crit_c,omitempty"`
}

// nodeSensorsResponse embeds ListResponse so the payload carries the same
// {items,total} envelope as every other collection, with the two fields a
// degrading endpoint needs on top.
//
// Available distinguishes "we could not read this node" from "we read it and it
// has nothing to report" — a distinction the item count alone cannot express,
// and one that matters because only the first is something the operator can fix.
// Reason is set only when Available is false.
type nodeSensorsResponse struct {
	ListResponse[nodeSensorReading]
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// nodeSensorsCache is a small in-process TTL cache of sensor responses.
//
// In-process rather than Redis on purpose: the value is worth pennies, it is
// identical for every caller, and a per-replica copy going stale for at most a
// minute is not a correctness problem. Nexara's default deployment is a single
// container in any case.
//
// Keys are (cluster, node) for nodes that were confirmed to exist — see
// GetNodeSensors for why that check has to happen before anything is stored
// under a caller-supplied name.
type nodeSensorsCache struct {
	mu      sync.Mutex
	entries map[string]nodeSensorsCacheEntry

	// sf collapses concurrent misses on the same key into one SSH session.
	// See fetch.
	sf singleflight.Group
}

type nodeSensorsCacheEntry struct {
	resp      nodeSensorsResponse
	expiresAt time.Time
}

func newNodeSensorsCache() *nodeSensorsCache {
	return &nodeSensorsCache{entries: make(map[string]nodeSensorsCacheEntry)}
}

// get returns a live entry, if one is present. A nil receiver misses, so a
// NodeHandler built without the constructor degrades to "no caching" rather
// than panicking.
func (c *nodeSensorsCache) get(key string) (nodeSensorsResponse, bool) {
	if c == nil {
		return nodeSensorsResponse{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Now().After(entry.expiresAt) {
		return nodeSensorsResponse{}, false
	}
	return entry.resp, true
}

// put stores a response for ttl, and sweeps entries that have already expired.
//
// The sweep is what keeps the map bounded over a long-running process: node
// count bounds the live keys, but a node removed from a cluster (or renamed)
// would otherwise leave its entry behind for the lifetime of the container.
// Doing it on write costs one pass over a map whose size is the estate's node
// count, and needs no background goroutine to own.
func (c *nodeSensorsCache) put(key string, resp nodeSensorsResponse, ttl time.Duration) {
	if c == nil {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, entry := range c.entries {
		if now.After(entry.expiresAt) {
			delete(c.entries, k)
		}
	}
	c.entries[key] = nodeSensorsCacheEntry{resp: resp, expiresAt: now.Add(ttl)}
}

// fetch returns the cached response for key, calling read at most once across
// all goroutines that miss at the same moment.
//
// Without this the cache would deduplicate only *completed* reads: K requests
// arriving on a cold key all miss, and all open their own SSH session to the
// same node. That is not a narrow race. A node that accepts TCP and stalls the
// banner holds each request for the handshake timeout (~30s, see
// nodeSensorsTimeout), and every request arriving in that window is another
// miss — so the pile-up is self-reinforcing, and it is bounded only by the
// general rate limiter. Exhausting sshd's unauthenticated-connection slots
// would then starve the other users of this path, node maintenance and the
// rolling-update orchestrator, which is a much worse outcome than a slow panel.
//
// This is the first view-level endpoint that spends an SSH login, so it is the
// first place a poll can drive that.
func (c *nodeSensorsCache) fetch(key string, read func() nodeSensorsResponse) nodeSensorsResponse {
	if c == nil {
		return read()
	}
	v, _, _ := c.sf.Do(key, func() (any, error) {
		// Re-check under the flight: a leader that finished between this
		// caller's miss and its arrival here has already stored a fresh entry.
		if cached, ok := c.get(key); ok {
			return cached, nil
		}
		resp := read()
		c.put(key, resp, nodeSensorsTTL)
		return resp, nil
	})
	resp, ok := v.(nodeSensorsResponse)
	if !ok {
		// Unreachable — the flight above only ever returns this type — but a
		// failed assertion must not panic a request or emit a null items list.
		return unavailableSensors("Could not read sensors over SSH from this node.")
	}
	return resp
}

// GetNodeSensors handles GET /api/v1/clusters/:cluster_id/nodes/:node_name/sensors.
//
// Gated on view:node, alongside the node's other read-only telemetry. It
// discloses less than the syslog and journal endpoints that already sit at this
// level, and a temperature is monitoring data — exactly what a read-only
// account exists to look at.
//
// Deliberately not audited. It is polled by every open node page, so an audit
// row per call would bury the log it shares with the actions worth reviewing,
// and it neither mutates anything nor discloses more than the numbers it
// returns.
func (h *NodeHandler) GetNodeSensors(c fiber.Ctx, p *apischema.Params) error {
	// The permission gate runs as route-attached middleware, ahead of this
	// handler and therefore ahead of the cache: a cached entry must never be
	// served to a caller who could not have fetched it themselves. See the
	// route's declaration in internal/api/registry_nodes.go.
	clusterID, nodeName, err := clusterAndNodeName(p)
	if err != nil {
		return err
	}

	// Resolve the node before touching the cache. A typo then gets a 404
	// instead of a puzzling "not available", and — the reason this is not
	// optional — the cache key space stays bounded by the nodes that exist.
	// :node_name is caller-controlled, so a map keyed on it without this check
	// is a memory leak that anyone holding view:node could drive.
	if _, err := h.queries.GetNodeByClusterAndName(c.Context(), db.GetNodeByClusterAndNameParams{
		ClusterID: clusterID,
		Name:      nodeName,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Node not found in this cluster")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to look up node")
	}

	key := clusterID.String() + "/" + nodeName
	if cached, ok := h.sensors.get(key); ok {
		return c.JSON(cached)
	}

	// fetch stores the result itself, and collapses a concurrent stampede on
	// this key into a single SSH session.
	//
	// The read takes c.Context(), which fiber.Ctx hands out as
	// context.Background() unless something calls SetContext — so the deadline
	// inside readNodeSensors is the only bound, and a follower cannot have its
	// work cancelled by the leader's client disconnecting.
	resp := h.sensors.fetch(key, func() nodeSensorsResponse {
		return h.readNodeSensors(c.Context(), clusterID, nodeName)
	})
	return c.JSON(resp)
}

// readNodeSensors runs the hwmon read and turns whatever happened into a
// response. It returns a value rather than an error because every failure here
// is a reason to show, not a request to fail.
func (h *NodeHandler) readNodeSensors(ctx context.Context, clusterID uuid.UUID, nodeName string) nodeSensorsResponse {
	sshCtx, cancel := context.WithTimeout(ctx, nodeSensorsTimeout)
	defer cancel()

	result, err := rolling.RunNodeCommand(sshCtx, h.queries, h.encryptionKey, clusterID, nodeName, nodeSensorsCommand)
	if err != nil {
		reason, classified := sensorsUnavailableReason(err)
		if !classified {
			// Unclassified failures carry node IPs, host-key fingerprints and
			// decrypt detail in their message, so the operator gets the generic
			// sentence and the detail goes to the log. Reached at most once per
			// node per TTL, so this cannot flood.
			slog.Warn("node sensor read failed",
				"cluster_id", clusterID, "node", nodeName, "error", err)
		}
		return unavailableSensors(reason)
	}
	if result.ExitCode != 0 {
		// `|| true` means this should be unreachable; a non-zero code here means
		// the remote shell itself failed, not that grep found nothing.
		slog.Warn("node sensor command exited non-zero",
			"cluster_id", clusterID, "node", nodeName,
			"exit_code", result.ExitCode, "stderr", strings.TrimSpace(result.Stderr))
		return unavailableSensors("Could not read sensors over SSH from this node.")
	}

	return availableSensors(parseHwmonTemps(result.Stdout))
}

// sensorsUnavailableReason maps a RunNodeCommand failure to a sentence for the
// operator, and reports whether it recognised the cause.
//
// The recognised cases are the ones the operator can act on, and their text is
// written here rather than reused from the error so nothing internal leaks into
// an API response. Anything else gets the generic sentence, and the caller logs
// the original.
func sensorsUnavailableReason(err error) (reason string, classified bool) {
	switch {
	case errors.Is(err, rolling.ErrSSHNotConfigured):
		return "SSH is not configured for this cluster. Add credentials under Settings → SSH Credentials to read hardware temperatures.", true
	case errors.Is(err, rolling.ErrHostKeyNotPinned):
		return "This node's SSH host key is not pinned. Open Settings → SSH Credentials, run Test Connection, and confirm the fingerprint.", true
	case errors.Is(err, rolling.ErrNodeAddressUnknown):
		return "No IP address is known for this node yet — the collector has not reported one.", true
	default:
		return "Could not read sensors over SSH from this node.", false
	}
}

func unavailableSensors(reason string) nodeSensorsResponse {
	return nodeSensorsResponse{
		ListResponse: ListResponse[nodeSensorReading]{Items: []nodeSensorReading{}, Total: 0},
		Available:    false,
		Reason:       reason,
	}
}

func availableSensors(readings []nodeSensorReading) nodeSensorsResponse {
	if readings == nil {
		readings = []nodeSensorReading{}
	}
	return nodeSensorsResponse{
		ListResponse: ListResponse[nodeSensorReading]{Items: readings, Total: int64(len(readings))},
		Available:    true,
	}
}

// hwmonDevice accumulates one /sys/class/hwmon/hwmonN directory as its files
// arrive, which is in whatever order grep listed them.
type hwmonDevice struct {
	name    string
	sensors map[int]map[string]string
}

// parseHwmonTemps turns the raw `grep -H .` output into sorted readings.
//
// Input lines are "<path>:<value>", e.g.
//
//	/sys/class/hwmon/hwmon1/name:coretemp
//	/sys/class/hwmon/hwmon1/temp1_label:Package id 0
//	/sys/class/hwmon/hwmon1/temp1_input:45000
//
// Values are millidegrees Celsius. Anything unrecognised, unparseable or
// outside the plausible window is dropped rather than guessed at: a missing row
// is a smaller lie than an invented number.
func parseHwmonTemps(raw string) []nodeSensorReading {
	devices := map[string]*hwmonDevice{}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		// Split on the first colon only: the path cannot contain one, but a
		// label very well might ("Sensor 1: Core").
		sep := strings.Index(line, ":")
		if sep < 0 {
			continue
		}
		filePath, value := line[:sep], strings.TrimSpace(line[sep+1:])
		dir, base := path.Dir(filePath), path.Base(filePath)
		if !strings.HasPrefix(dir, "/sys/class/hwmon/") {
			continue
		}

		dev, ok := devices[dir]
		if !ok {
			dev = &hwmonDevice{sensors: map[int]map[string]string{}}
			devices[dir] = dev
		}

		if base == "name" {
			dev.name = value
			continue
		}
		m := hwmonAttrRe.FindStringSubmatch(base)
		if m == nil {
			continue
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if dev.sensors[idx] == nil {
			dev.sensors[idx] = map[string]string{}
		}
		dev.sensors[idx][m[2]] = value
	}

	var readings []nodeSensorReading
	for dir, dev := range devices {
		device := path.Base(dir)
		// A device with no name file still has usable readings; fall back to the
		// directory so the row is attributable to something.
		chip := dev.name
		if chip == "" {
			chip = device
		}
		for idx, attrs := range dev.sensors {
			temp, ok := parseMilliDegrees(attrs["input"])
			if !ok {
				// A sensor exposing a label or a limit but no reading has
				// nothing to show.
				continue
			}
			reading := nodeSensorReading{
				Chip:   chip,
				Device: device,
				Key:    "temp" + strconv.Itoa(idx),
				Label:  attrs["label"],
				TempC:  temp,
			}
			if high, ok := parseThreshold(attrs["max"]); ok {
				reading.HighC = &high
			}
			if crit, ok := parseThreshold(attrs["crit"]); ok {
				reading.CritC = &crit
			}
			readings = append(readings, reading)
		}
	}

	sortReadings(readings)
	return readings
}

// sortReadings gives the list a stable, human order: chips alphabetically, then
// devices sharing a chip name in hwmon order, then sensors in numeric order.
//
// Numeric and not lexical throughout, so temp2 precedes temp10 and hwmon2
// precedes hwmon10. Map iteration is randomised, so without this the panel
// would reshuffle itself on every poll.
func sortReadings(readings []nodeSensorReading) {
	sort.Slice(readings, func(i, j int) bool {
		a, b := readings[i], readings[j]
		if a.Chip != b.Chip {
			return a.Chip < b.Chip
		}
		if ai, bi := trailingIndex(a.Device), trailingIndex(b.Device); ai != bi {
			return ai < bi
		}
		if a.Device != b.Device {
			return a.Device < b.Device
		}
		if ai, bi := trailingIndex(a.Key), trailingIndex(b.Key); ai != bi {
			return ai < bi
		}
		return a.Key < b.Key
	})
}

// trailingIndex extracts the trailing integer from names like "hwmon10" and
// "temp2", returning MaxInt for a name that has none (or whose digits overflow)
// so it sorts after every numbered sibling. Two such names compare equal here
// and are separated by the string compare the caller applies next.
func trailingIndex(s string) int {
	i := len(s)
	for i > 0 && s[i-1] >= '0' && s[i-1] <= '9' {
		i--
	}
	if i == len(s) {
		return math.MaxInt
	}
	n, err := strconv.Atoi(s[i:])
	if err != nil {
		return math.MaxInt
	}
	return n
}

// parseMilliDegrees converts a hwmon value in millidegrees Celsius to degrees,
// rounded to one decimal. It reports false for anything unparseable or outside
// the plausible window.
//
// Rounding here rather than in the UI keeps the JSON free of binary-float noise
// (38.849999999999994 for a perfectly ordinary NVMe reading).
func parseMilliDegrees(raw string) (float64, bool) {
	if raw == "" {
		return 0, false
	}
	milli, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	celsius := math.Round(milli/100) / 10
	if math.IsNaN(celsius) || celsius < minPlausibleTempC || celsius > maxPlausibleTempC {
		return 0, false
	}
	return celsius, true
}

// parseThreshold parses a tempN_max / tempN_crit limit.
//
// Stricter than parseMilliDegrees by one rule: a limit at or below zero is
// rejected. Drivers publish 0 for a threshold they do not actually implement,
// and a zero crit would mark every healthy reading on that chip as critical —
// the one failure mode of this panel that would actively mislead.
func parseThreshold(raw string) (float64, bool) {
	value, ok := parseMilliDegrees(raw)
	if !ok || value <= 0 {
		return 0, false
	}
	return value, true
}
