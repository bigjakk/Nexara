// Package handlers provides HTTP request handlers for the Nexara API.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/proxmox"
	"github.com/bigjakk/nexara/internal/pvefield"
	"github.com/bigjakk/nexara/internal/redact"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// AuditLog writes an audit log entry and publishes an audit_entry WS event.
// clusterID may be zero-value pgtype.UUID for actions without a cluster context.
// details may be nil (defaults to {}).
//
// When the request was authenticated via API key, the key's id is injected
// into the details JSON as `api_key_id` so an action can be correlated to a
// specific key (not just the owning user).
//
// resource_type is the kind of object and resource_id the key Proxmox and the UI
// know it by, within the cluster. An object's History reads its rows by that
// pair, so one object files under ONE pair whichever route changed it, and the
// collector files a task done outside Nexara under the same pair (task_mapping.go).
// The spellings Proxmox applies to a name live in internal/auditkey. For the
// objects of the cluster page the keys are:
//
//	ha_resource   the sid as Proxmox spells it, vm:100 or ct:100 — never a bare VMID
//	ha_group, ha_rule, pool, acme_account, acme_plugin, metric_server, pve_group, pve_role,
//	usb_mapping, pci_mapping, sdn_zone, sdn_vnet, sdn_controller, sdn_ipam, sdn_dns
//	                     the name or id
//	firewall_alias, firewall_ipset, firewall_security_group
//	                     the name, lower case: Proxmox folds it when it parses the config. The
//	                     alias listing keeps the case it was typed in (name: MyNet), so a
//	                     reader of this key lower-cases a listed name before looking it up
//	firewall_ipset_entry <set>/<entry>, the entry as the set stores it — a network with its host
//	                     bits cleared, a single host without /32 or /128; an alias as sent
//	firewall_security_group_rule   the group, lower case
//	sdn_subnet           <vnet>/<subnet id>, the id Proxmox derives (zone-ip-mask), not the CIDR
//	ceph_pool            the pool name;  ceph_osd, ceph_mon, ceph_mgr, ceph_mds  the number or name
//	pve_user user@realm; pve_token user@realm!tokenid; pve_acl the normalised path
//	certificate, acme_config   the node;  replication  the job id (100-0);  drs_rule  the rule's uuid
//	firewall_rule, firewall_options   the scope — cluster, vm/<vmid>, or (rules only) node/<node> —
//	                                  since a rule has no id of its own and its position moves;
//	                                  the position is a detail
//	firewall_template    its uuid
//	network  <node>/<iface>, or the node;  cluster_options, ha  the cluster's uuid;  drs  its config's
//	sdn  "cluster", for the apply
func AuditLog(c fiber.Ctx, queries *db.Queries, eventPub *events.Publisher, clusterID pgtype.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return
	}
	AuditLogAs(c, queries, eventPub, uid, clusterID, resourceType, resourceID, action, details)
}

// AuditLogAs is AuditLog for callers that know the actor but cannot read it
// from the request. It is the same audit path in every other respect — the row,
// the api_key_id enrichment, the audit_entry WS event and the syslog forward.
//
// It exists for authentication events. Those are audited at points where
// c.Locals("user_id") is not set yet or no longer applies — a login has not
// been through the auth middleware, a refresh is being denied, a logout has
// already torn the session down — so AuditLog would read no actor and return
// without recording anything.
//
// Auth handlers must call this rather than growing their own insert. A private
// wrapper is how `login`, `logout` and `password_changed` came to be missing
// from syslog forwarding entirely: the wrapper wrote the row directly and never
// reached the forwarder, so the events a SIEM most wants were the ones it never
// received. TestGuard_NoHandlerAuditLogWrappers enforces this.
func AuditLogAs(c fiber.Ctx, queries *db.Queries, eventPub *events.Publisher, actor uuid.UUID, clusterID pgtype.UUID, resourceType, resourceID, action string, details json.RawMessage) {
	uid := actor
	if details == nil {
		details = json.RawMessage(`{}`)
	}

	// One decode serves three purposes: redacting secrets, injecting
	// api_key_id, and lifting the guest identity out for the syslog
	// forwarder. Re-marshalled only when something was actually changed, so
	// an untouched details blob reaches the row byte-for-byte as the handler
	// wrote it.
	var m map[string]any
	if err := json.Unmarshal(details, &m); err != nil {
		m = nil
	}
	rewrite := redactAuditSecrets(m)

	// Inject api_key_id into details for API-key-authed requests so audit
	// rows can be attributed to the specific key, not just the user.
	if apiKeyID, ok := c.Locals(LocalsAPIKeyID).(uuid.UUID); ok {
		if m == nil {
			m = map[string]any{}
		}
		m["api_key_id"] = apiKeyID.String()
		rewrite = true
	}
	if rewrite {
		enriched, err := json.Marshal(m)
		if err != nil {
			enriched = json.RawMessage(`{}`) // never the bytes a redaction was to change
		}
		details = enriched
	}

	var cidStr string
	if clusterID.Valid {
		cid, _ := uuid.FromBytes(clusterID.Bytes[:])
		cidStr = cid.String()
	}

	// The action has already happened by the time this runs, so a failed insert
	// cannot fail the request — but it must not be silent either. Without this
	// line the failure mode is total: the action lands, the caller gets a
	// success, and no record of it exists anywhere. The log entry is then the
	// only remaining trace, and the only thing anyone reconciling audit_log
	// against what actually happened has to work from — hence cluster_id, which
	// on a multi-cluster install is what says where the unrecorded action
	// landed.
	//
	// `details` is deliberately not logged. It carries filenames, HA and
	// firewall comments, the syslog destination and api_key_id; application
	// logs have a different audience from audit_log, and the four identifying
	// fields already say which action went missing.
	//
	// Deliberately does not return the error: every audited handler calls this
	// after its mutation has committed, so there is no caller in a position to
	// undo anything.
	if err := queries.InsertAuditLog(c.Context(), db.InsertAuditLogParams{
		ClusterID:    clusterID,
		UserID:       UserUUID(uid),
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       action,
		Details:      details,
	}); err != nil {
		slog.Error("audit log insert failed; action performed but not recorded",
			"user_id", uid,
			"cluster_id", cidStr,
			"resource_type", resourceType,
			"resource_id", resourceID,
			"action", action,
			"error", err)
	}

	eventPub.ClusterEvent(c.Context(), cidStr, events.KindAuditEntry, resourceType, resourceID, action)

	// Forward to syslog if configured. vmid and resource_name are promoted to
	// first-class fields so a SIEM decoder can key a rule on the guest without
	// re-parsing the details JSON — the blind spot that made ~73% of forwarded
	// entries unmatchable by any guest-scoped rule.
	if fwd := eventPub.SyslogForwarder(); fwd != nil {
		fwd.Forward(proxsyslog.Message{
			Timestamp:    time.Now().UTC(),
			UserID:       uid.String(),
			ClusterID:    cidStr,
			ResourceType: resourceType,
			ResourceID:   resourceID,
			Action:       action,
			VMID:         auditDetailString(m, "vmid"),
			ResourceName: auditDetailString(m, "resource_name"),
			Details:      string(details),
		})
	}
}

// redactAuditSecrets replaces, in decoded audit details, the secrets a request
// carried, and reports whether it changed anything. It runs where the row, the
// syslog copy and (through the row) the History all read details, so no caller
// has to remember it. A fields object is how a row records the values it sent
// Proxmox, by parameter (the guest config writes): each secret one
// (pvefield.Secret) keeps its key, so the row still says the request set it, and
// is recorded as "(set)", or "(cleared)" when sent empty. A URL in any string of
// the details keeps no userinfo, query or fragment (redact.URLs).
func redactAuditSecrets(m map[string]any) bool {
	changed := false
	if fields, ok := m["fields"].(map[string]any); ok {
		for k, v := range fields {
			if !pvefield.Secret(k) {
				continue
			}
			r := redact.Set
			if s, isString := v.(string); v == nil || isString && s == "" {
				r = redact.Cleared
			}
			if v != r {
				fields[k], changed = r, true
			}
		}
	}
	var walk func(v any) (any, bool)
	walk = func(v any) (any, bool) {
		moved := false
		switch t := v.(type) {
		case string:
			r := redact.URLs(t)
			return r, r != t
		case map[string]any:
			for k, e := range t {
				if r, ok := walk(e); ok {
					t[k], moved = r, true
				}
			}
		case []any:
			for i, e := range t {
				if r, ok := walk(e); ok {
					t[i], moved = r, true
				}
			}
		}
		return v, moved
	}
	if _, ok := walk(m); ok {
		changed = true
	}
	return changed
}

// auditDetailString reads one detail field as a string for the syslog message.
//
// json.Unmarshal into map[string]any decodes every JSON number as float64, so
// a vmid written as 121 arrives as 121.0 and would render "121.000000" under
// %v. Integral floats are formatted without a fractional part; anything else
// falls back to its natural rendering.
func auditDetailString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1e15 {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return ""
	}
}

// auditTruncate bounds a caller-supplied string before it lands in an audit
// detail. Cut on a rune boundary so the recorded JSON stays valid UTF-8, and
// marked with an ellipsis so a reader can tell a cut string from one that
// happened to be exactly that long.
//
// Anything a caller controls that reaches a detail belongs here: audit_log is
// written once per request and never trimmed, so an uncapped string is a way to
// grow that table at will.
func auditTruncate(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + "…"
}

// maxAuditResourceNameLen bounds a guest name recorded in an audit detail.
// Proxmox hostnames cannot approach it, so it only ever truncates a value a
// caller supplied to grow the row.
const maxAuditResourceNameLen = 128

// TrackTaskParams describes a dispatched Proxmox task to record.
type TrackTaskParams struct {
	ClusterID    uuid.UUID
	Node         string
	ResourceType string         // "vm", "container", "node", "storage", "pbs_server", ...
	ResourceID   string         // internal resource id (or vmid as string); a storage's NAME, a PBS server's id
	ResourceName string         // optional display name
	Action       string         // audit action, e.g. "disk_move", "migrate", "backup"
	UPID         string         // Proxmox task UPID
	TaskType     string         // Proxmox task type; if empty, derived from the UPID
	Description  string         // human-readable, shown in the activity / task list
	Extra        map[string]any // optional extra detail fields merged into the audit JSON
}

// TrackTask is the single entry point for recording a dispatched Proxmox task.
// Call it immediately after a pxClient call returns a UPID. It writes the audit
// log entry (reusing AuditLog — which also handles API-key attribution, the
// audit_entry WS event, and syslog forwarding), inserts the task_history row as
// "running", and publishes a task_created event so the activity feed updates.
//
// This is the ONLY sanctioned way to record a task — handlers must not hand-roll
// the audit / InsertTaskHistory / event trio. The collector reconciler keeps the
// task_history.status accurate from "running" to completed/failed.
func TrackTask(c fiber.Ctx, queries *db.Queries, eventPub *events.Publisher, p TrackTaskParams) {
	details := map[string]any{"node": p.Node}
	if p.UPID != "" {
		details["upid"] = p.UPID
	}
	if p.ResourceName != "" {
		// Bounded here rather than at each call site: VM create and CT create
		// pass a caller-chosen req.Name/req.Hostname straight through, and this
		// value is no longer detail-only — it is a response field, a CSV column
		// and a syslog SD-PARAM. Capping at the funnel means a new caller
		// cannot reintroduce the gap.
		details["resource_name"] = auditTruncate(p.ResourceName, maxAuditResourceNameLen)
	}
	// The guest identity, recorded rather than left to be re-derived later.
	// resource_id holds a vms.id UUID that the collector churns on resync, so
	// anything resolved through it stops resolving at an arbitrary later time
	// (see migrations/000084). Extra is applied after, so a handler that knows
	// a better vmid than the UPID's id field still wins.
	if vmid := vmidFromUPID(p.UPID); vmid != 0 {
		details["vmid"] = vmid
	}
	for k, v := range p.Extra {
		details[k] = v
	}
	raw, _ := json.Marshal(details)

	// Audit log + audit_entry event + syslog (AuditLog self-guards on user_id).
	AuditLog(c, queries, eventPub, ClusterUUID(p.ClusterID), p.ResourceType, p.ResourceID, p.Action, raw)

	// Not every request Proxmox accepts becomes a task: a replication job's
	// "schedule now" only moves its next run up, and answers with nothing. The
	// audit row is owed; a task_history row that no task will ever finish is
	// not — it would stay "running" for a day before the reconciler gave up on it.
	if p.UPID == "" {
		return
	}

	uid, ok := c.Locals("user_id").(uuid.UUID)
	if !ok {
		return
	}
	taskType := p.TaskType
	if taskType == "" {
		taskType = taskTypeFromUPID(p.UPID)
	}
	_, _ = queries.InsertTaskHistory(c.Context(), db.InsertTaskHistoryParams{
		ClusterID:   p.ClusterID,
		UserID:      uid,
		Upid:        p.UPID,
		Description: redact.URLs(p.Description),
		Status:      "running",
		Node:        p.Node,
		TaskType:    taskType,
	})
	eventPub.ClusterEvent(c.Context(), p.ClusterID.String(), events.KindTaskCreated, "task", p.UPID, taskType)
}

// taskTypeFromUPID extracts the Proxmox worker type from a UPID.
// Format: UPID:node:pid:pstart:starttime:TYPE:id:user: — TYPE is field 5.
func taskTypeFromUPID(upid string) string {
	parts := strings.Split(upid, ":")
	if len(parts) >= 6 {
		return parts[5]
	}
	return ""
}

// guestUPIDPrefixes / guestUPIDExact are the Proxmox worker types whose UPID id
// field (index 6) holds a guest VMID. The Go mirror of the SQL is_guest_upid()
// defined in migrations/000084 — keep the two in step.
//
// A numeric id is NOT sufficient on its own, which is the whole reason this
// list exists: nothing promises that a worker type outside these families keeps
// a VMID in that field, and an audit row naming the wrong guest is worse than
// one naming none. A Ceph daemon's tasks carry the daemon's name — "osd.12",
// not 12 (cephdestroyosd's id is "osd.<n>" in pve-manager's OSD.pm) — and are
// filed under that daemon instead (collector/task_mapping.go).
//
// Prefix-matched for the three guest families (qm* QEMU, vz* LXC including
// vzdump, ha* HA manager), plus the two observed outliers that carry a VMID
// without one of those prefixes.
var (
	guestUPIDPrefixes = []string{"qm", "vz", "ha"}
	guestUPIDExact    = map[string]bool{"resize": true, "move_volume": true}
)

func isGuestUPIDType(taskType string) bool {
	if guestUPIDExact[taskType] {
		return true
	}
	for _, p := range guestUPIDPrefixes {
		if strings.HasPrefix(taskType, p) {
			return true
		}
	}
	return false
}

// vmidFromUPID extracts the guest VMID from a UPID's id field (index 6),
// returning 0 when the task names no guest.
//
// Fails closed: an unrecognised worker type yields 0 rather than a guess. An
// audit row naming the wrong guest is worse than one naming none. Bounded at 9
// digits to match the ::int guard the SQL side uses; Proxmox VMIDs top out
// below that.
//
// The int return is deliberate: every handler already writes details.vmid as a
// JSON number (containers.go, backup.go, guest_snapshots.go), and a string here
// would make the field's type depend on which path recorded the entry.
func vmidFromUPID(upid string) int {
	parts := strings.Split(upid, ":")
	if len(parts) < 7 {
		return 0
	}
	if !isGuestUPIDType(parts[5]) {
		return 0
	}
	id := parts[6]
	if id == "" || len(id) > 9 {
		return 0
	}
	n, err := strconv.Atoi(id)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// ClusterUUID is a convenience helper that converts a uuid.UUID to a valid pgtype.UUID.
func ClusterUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// requireCluster answers 404 for a cluster that does not exist, through the
// lookup the other cluster routes use (GetCluster). A route that reads the audit
// rows of the path's cluster calls it: those rows outlive the cluster (000122), so
// without it the route would answer for a cluster that is gone, to whoever holds
// view:audit instance-wide.
func requireCluster(ctx context.Context, queries *db.Queries, id uuid.UUID) error {
	if _, err := queries.GetCluster(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}
	return nil
}

// UserUUID wraps a uuid.UUID into a non-null pgtype.UUID for audit_log.user_id.
// The column is nullable (so audit rows can survive user deletion), but inserts
// always carry the user who performed the action.
func UserUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

// proxmoxCacheLocalsKey is the fiber Locals key under which the per-server
// *proxmox.ClientCache is exposed to handlers. server.go installs a
// middleware that sets this on every request; CreateProxmoxClient picks
// it up to route through the cache instead of constructing per-call.
const proxmoxCacheLocalsKey = "proxmoxCache"

// SetProxmoxCacheLocal stores the cache pointer on a fiber.Ctx Locals slot.
// Tests and the production middleware both call this to wire the cache in.
func SetProxmoxCacheLocal(c fiber.Ctx, cache *proxmox.ClientCache) {
	c.Locals(proxmoxCacheLocalsKey, cache)
}

// proxmoxCacheFromCtx retrieves the cache from Locals; returns nil if
// nothing was wired.
func proxmoxCacheFromCtx(c fiber.Ctx) *proxmox.ClientCache {
	v := c.Locals(proxmoxCacheLocalsKey)
	if v == nil {
		return nil
	}
	cache, _ := v.(*proxmox.ClientCache)
	return cache
}

// CreateProxmoxClient returns a Proxmox API client for the given cluster.
// When a *proxmox.ClientCache has been wired into fiber Locals (via the
// server's middleware), it's used so repeated requests for the same
// cluster reuse the http.Transport's idle connection pool. Falls back
// to a per-call NewClient construction otherwise (tests / unwired paths).
//
// The optional timeout argument overrides the default on the fallback
// path; cached clients always use the cache's internal timeout. ISO
// upload callers (which want a 30-minute floor) hit the multipart
// escape hatch inside the apiClient that already runs without an
// http.Client.Timeout.
func CreateProxmoxClient(c fiber.Ctx, queries *db.Queries, encryptionKey string, clusterID uuid.UUID, timeout ...time.Duration) (*proxmox.Client, error) {
	if cache := proxmoxCacheFromCtx(c); cache != nil {
		client, err := cache.Get(c.Context(), clusterID)
		if err == nil {
			return client, nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		// Decrypt / construction failures surface as 500. The cache
		// already drops failed entries, so retrying after fixing the
		// root cause works without further intervention.
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	cluster, err := queries.GetCluster(c.Context(), clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fiber.NewError(fiber.StatusNotFound, "Cluster not found")
		}
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to get cluster")
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, encryptionKey)
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt cluster credentials")
	}

	t := 30 * time.Second
	if len(timeout) > 0 {
		t = timeout[0]
	}

	pxClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        t,
	})
	if err != nil {
		return nil, fiber.NewError(fiber.StatusInternalServerError, "Failed to create Proxmox client")
	}

	return pxClient, nil
}
