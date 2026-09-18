package handlers

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/bigjakk/nexara/internal/api/apischema"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	"github.com/bigjakk/nexara/internal/notifications"
	"github.com/bigjakk/nexara/internal/safeconv"
)

// AlertHandler handles alert rules, history, notification channels, and maintenance windows.
type AlertHandler struct {
	queries       *db.Queries
	encryptionKey string
	eventPub      *events.Publisher
	registry      *notifications.Registry
}

// NewAlertHandler creates a new AlertHandler.
func NewAlertHandler(queries *db.Queries, encryptionKey string, eventPub *events.Publisher, registry *notifications.Registry) *AlertHandler {
	return &AlertHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		eventPub:      eventPub,
		registry:      registry,
	}
}

// --- Response types ---

type alertRuleResponse struct {
	ID              uuid.UUID       `json:"id"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Enabled         bool            `json:"enabled"`
	Severity        string          `json:"severity"`
	Metric          string          `json:"metric"`
	Operator        string          `json:"operator"`
	Threshold       float64         `json:"threshold"`
	DurationSeconds int32           `json:"duration_seconds"`
	ScopeType       string          `json:"scope_type"`
	ClusterID       string          `json:"cluster_id,omitempty"`
	NodeID          string          `json:"node_id,omitempty"`
	VMVmid          int32           `json:"vm_vmid,omitempty"`
	CooldownSeconds int32           `json:"cooldown_seconds"`
	EscalationChain json.RawMessage `json:"escalation_chain"`
	MessageTemplate string          `json:"message_template"`
	CreatedBy       uuid.UUID       `json:"created_by"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

func toAlertRuleResponse(r db.AlertRule) alertRuleResponse {
	resp := alertRuleResponse{
		ID:              r.ID,
		Name:            r.Name,
		Description:     r.Description,
		Enabled:         r.Enabled,
		Severity:        r.Severity,
		Metric:          r.Metric,
		Operator:        r.Operator,
		Threshold:       r.Threshold,
		DurationSeconds: r.DurationSeconds,
		ScopeType:       r.ScopeType,
		CooldownSeconds: r.CooldownSeconds,
		EscalationChain: r.EscalationChain,
		MessageTemplate: r.MessageTemplate,
		CreatedBy:       r.CreatedBy,
		CreatedAt:       r.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:       r.UpdatedAt.Format(time.RFC3339Nano),
	}
	if r.ClusterID.Valid {
		id, _ := uuid.FromBytes(r.ClusterID.Bytes[:])
		resp.ClusterID = id.String()
	}
	if r.NodeID.Valid {
		id, _ := uuid.FromBytes(r.NodeID.Bytes[:])
		resp.NodeID = id.String()
	}
	if r.VmVmid.Valid {
		resp.VMVmid = r.VmVmid.Int32
	}
	return resp
}

type alertHistoryResponse struct {
	ID              uuid.UUID `json:"id"`
	RuleID          uuid.UUID `json:"rule_id"`
	State           string    `json:"state"`
	Severity        string    `json:"severity"`
	ClusterID       string    `json:"cluster_id,omitempty"`
	NodeID          string    `json:"node_id,omitempty"`
	VMID            string    `json:"vm_id,omitempty"`
	VMVmid          *int32    `json:"vm_vmid,omitempty"`
	ResourceName    string    `json:"resource_name"`
	Metric          string    `json:"metric"`
	CurrentValue    float64   `json:"current_value"`
	Threshold       float64   `json:"threshold"`
	Message         string    `json:"message"`
	EscalationLevel int32     `json:"escalation_level"`
	ChannelID       string    `json:"channel_id,omitempty"`
	PendingAt       string    `json:"pending_at"`
	FiredAt         string    `json:"fired_at,omitempty"`
	AcknowledgedAt  string    `json:"acknowledged_at,omitempty"`
	AcknowledgedBy  string    `json:"acknowledged_by,omitempty"`
	ResolvedAt      string    `json:"resolved_at,omitempty"`
	ResolvedBy      string    `json:"resolved_by,omitempty"`
	CreatedAt       string    `json:"created_at"`
}

func toAlertHistoryResponse(a db.AlertHistory) alertHistoryResponse {
	resp := alertHistoryResponse{
		ID:              a.ID,
		RuleID:          a.RuleID,
		State:           a.State,
		Severity:        a.Severity,
		ResourceName:    a.ResourceName,
		Metric:          a.Metric,
		CurrentValue:    a.CurrentValue,
		Threshold:       a.Threshold,
		Message:         a.Message,
		EscalationLevel: a.EscalationLevel,
		PendingAt:       a.PendingAt.Format(time.RFC3339Nano),
		CreatedAt:       a.CreatedAt.Format(time.RFC3339Nano),
	}
	if a.ClusterID.Valid {
		id, _ := uuid.FromBytes(a.ClusterID.Bytes[:])
		resp.ClusterID = id.String()
	}
	if a.NodeID.Valid {
		id, _ := uuid.FromBytes(a.NodeID.Bytes[:])
		resp.NodeID = id.String()
	}
	if a.VmID.Valid {
		id, _ := uuid.FromBytes(a.VmID.Bytes[:])
		resp.VMID = id.String()
	}
	if a.VmVmid.Valid {
		v := a.VmVmid.Int32
		resp.VMVmid = &v
	}
	if a.ChannelID.Valid {
		id, _ := uuid.FromBytes(a.ChannelID.Bytes[:])
		resp.ChannelID = id.String()
	}
	if a.FiredAt.Valid {
		resp.FiredAt = a.FiredAt.Time.Format(time.RFC3339Nano)
	}
	if a.AcknowledgedAt.Valid {
		resp.AcknowledgedAt = a.AcknowledgedAt.Time.Format(time.RFC3339Nano)
	}
	if a.AcknowledgedBy.Valid {
		id, _ := uuid.FromBytes(a.AcknowledgedBy.Bytes[:])
		resp.AcknowledgedBy = id.String()
	}
	if a.ResolvedAt.Valid {
		resp.ResolvedAt = a.ResolvedAt.Time.Format(time.RFC3339Nano)
	}
	if a.ResolvedBy.Valid {
		id, _ := uuid.FromBytes(a.ResolvedBy.Bytes[:])
		resp.ResolvedBy = id.String()
	}
	return resp
}

type alertSummaryResponse struct {
	FiringCount       int64 `json:"firing_count"`
	PendingCount      int64 `json:"pending_count"`
	AcknowledgedCount int64 `json:"acknowledged_count"`
	CriticalFiring    int64 `json:"critical_firing"`
	WarningFiring     int64 `json:"warning_firing"`
	InfoFiring        int64 `json:"info_firing"`
}

type notificationChannelResponse struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	ChannelType string    `json:"channel_type"`
	Enabled     bool      `json:"enabled"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

func toNotificationChannelResponse(ch db.NotificationChannel) notificationChannelResponse {
	return notificationChannelResponse{
		ID:          ch.ID,
		Name:        ch.Name,
		ChannelType: ch.ChannelType,
		Enabled:     ch.Enabled,
		CreatedBy:   ch.CreatedBy,
		CreatedAt:   ch.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   ch.UpdatedAt.Format(time.RFC3339Nano),
	}
}

type maintenanceWindowResponse struct {
	ID          uuid.UUID `json:"id"`
	ClusterID   uuid.UUID `json:"cluster_id"`
	NodeID      string    `json:"node_id,omitempty"`
	Description string    `json:"description"`
	StartsAt    string    `json:"starts_at"`
	EndsAt      string    `json:"ends_at"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
}

func toMaintenanceWindowResponse(w db.MaintenanceWindow) maintenanceWindowResponse {
	resp := maintenanceWindowResponse{
		ID:          w.ID,
		ClusterID:   w.ClusterID,
		Description: w.Description,
		StartsAt:    w.StartsAt.Format(time.RFC3339Nano),
		EndsAt:      w.EndsAt.Format(time.RFC3339Nano),
		CreatedBy:   w.CreatedBy,
		CreatedAt:   w.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:   w.UpdatedAt.Format(time.RFC3339Nano),
	}
	if w.NodeID.Valid {
		id, _ := uuid.FromBytes(w.NodeID.Bytes[:])
		resp.NodeID = id.String()
	}
	return resp
}

// --- Request types ---

var validSeveritiesAlert = map[string]bool{
	"critical": true, "warning": true, "info": true,
}

var validOperators = map[string]bool{
	">": true, ">=": true, "<": true, "<=": true, "==": true, "!=": true,
}

var validScopeTypes = map[string]bool{
	// "global" is for metrics describing infrastructure no single cluster
	// owns — a Veeam repository holds every cluster's backups. A global rule
	// raises alerts with a NULL cluster_id, which the scoped history read
	// already hides from anyone but a holder of global view:alert.
	"cluster": true, "node": true, "vm": true, "global": true,
}

// NotificationChannelTypes is the complete set of channel types Nexara can
// deliver to. "expo_push" used to be rejected here while its dispatcher sat
// unused behind it; both the dispatcher and the mobile_devices table it read
// were removed in v1.9.x along with the React Native app, so the type no
// longer exists anywhere and needs no special-casing.
//
// It replaced a `map[string]bool` the create and update handlers looked a
// value up in. The declarations in internal/api/registry_alerts.go state it as
// the parameter's Enum, which refuses an unknown type before either handler
// runs — so a second copy here would be a lookup nothing consults, and the one
// remaining list is the one the API publishes.
var NotificationChannelTypes = []string{
	"email", "webhook", "slack", "discord", "pagerduty", "teams", "telegram",
}

// createAlertRuleRequest is the body of both POST and PUT. On an update every
// absent field keeps its stored value, so the free-text fields a caller may
// legitimately want to blank out are pointers: "" on a bare string is
// indistinguishable from absent, which left a rule's custom message_template
// impossible to clear (and "" is the sentinel the engine reads as "use the
// built-in message").
type createAlertRuleRequest struct {
	Name            string          `json:"name"`
	Description     *string         `json:"description"`
	Enabled         *bool           `json:"enabled"`
	Severity        string          `json:"severity"`
	Metric          string          `json:"metric"`
	Operator        string          `json:"operator"`
	Threshold       *float64        `json:"threshold"`
	DurationSeconds *int32          `json:"duration_seconds"`
	ScopeType       string          `json:"scope_type"`
	ClusterID       string          `json:"cluster_id"`
	NodeID          string          `json:"node_id"`
	VMVmid          *int32          `json:"vm_vmid"`
	CooldownSeconds *int32          `json:"cooldown_seconds"`
	EscalationChain json.RawMessage `json:"escalation_chain"`
	MessageTemplate *string         `json:"message_template"`
}

const maxTemplateLen = 4096

const (
	maxNameLen        = 255
	maxDescriptionLen = 1024
	maxDurationSec    = 86400  // 24 hours
	maxCooldownSec    = 604800 // 7 days

	// maxWindowedDurationSec caps duration_seconds for the metrics that count
	// events inside it instead of requiring a level to persist. 30 days covers
	// a weekly or monthly backup schedule; the 24-hour cap would leave a weekly
	// job permanently outside its own alert's window.
	maxWindowedDurationSec = 2592000 // 30 days
)

// maxDurationFor returns the duration_seconds cap for a metric. See
// notifications.IsWindowedMetric for why the two differ.
func maxDurationFor(metric string) int32 {
	if notifications.IsWindowedMetric(metric) {
		return maxWindowedDurationSec
	}
	return maxDurationSec
}

// validateEscalationChain validates the structure of an escalation chain.
func validateEscalationChain(data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	var chain []struct {
		ChannelID    string `json:"channel_id"`
		DelayMinutes int    `json:"delay_minutes"`
	}
	if err := json.Unmarshal(data, &chain); err != nil {
		return fmt.Errorf("invalid escalation chain JSON")
	}
	for _, step := range chain {
		if _, err := uuid.Parse(step.ChannelID); err != nil {
			return fmt.Errorf("invalid channel_id in escalation chain")
		}
		if step.DelayMinutes < 0 {
			return fmt.Errorf("delay_minutes must be non-negative")
		}
	}
	return nil
}

// channelConfigJSON renders the dispatcher settings object back to the JSON
// the encrypted column stores.
//
// The value is carried through unvalidated — apischema has no nested-object
// schema, and the accepted keys belong to whichever dispatcher channel_type
// selects — so this is a re-marshal of what the caller sent rather than a
// transformation. A failure is unreachable for a value that arrived as JSON,
// but a silent nil here would store "null" over a working webhook URL.
func channelConfigJSON(p *apischema.Params) (json.RawMessage, error) {
	raw, err := json.Marshal(p.Object("config"))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "Invalid config")
	}
	return raw, nil
}

// ====== Alert Rules ======

// ListRules lists all alert rules.
func (h *AlertHandler) ListRules(c fiber.Ctx, p *apischema.Params) error {
	access, err := accessibleClusters(c, "view", "alert")
	if err != nil {
		return err
	}

	limit := int(p.Int("limit"))
	offset := int(p.Int("offset"))

	// The EMPTY string means "do not filter", which is what the declaration's
	// empty-or-uuid pattern keeps expressible — the uuid format would refuse it.
	clusterIDQ := p.String("filter_cluster_id")
	if clusterIDQ != "" {
		cid, err := parseParamUUID(clusterIDQ)
		if err != nil {
			return err
		}
		if !access.PermitsCluster(cid) {
			return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
		}
		rules, err := h.queries.ListAlertRulesByCluster(c.Context(), db.ListAlertRulesByClusterParams{
			ClusterID: pgtype.UUID{Bytes: cid, Valid: true},
			Limit:     safeconv.Int32(limit),
			Offset:    safeconv.Int32(offset),
		})
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to list alert rules")
		}
		result := make([]alertRuleResponse, len(rules))
		for i, r := range rules {
			result[i] = toAlertRuleResponse(r)
		}
		return RespondItems(c, result)
	}

	// Scoped in SQL, not after the fetch: LIMIT/OFFSET run before the per-row
	// trim below, so paging over every cluster's rules and filtering afterwards
	// hands a scoped caller short pages with holes in them.
	scope, query := clusterScopeFilter(access)
	if !query {
		return RespondItems(c, []alertRuleResponse{})
	}

	rules, err := h.queries.ListAlertRules(c.Context(), db.ListAlertRulesParams{
		Limit:                safeconv.Int32(limit),
		Offset:               safeconv.Int32(offset),
		AccessibleClusterIds: scope,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list alert rules")
	}

	result := make([]alertRuleResponse, 0, len(rules))
	for _, r := range rules {
		// Defense-in-depth: the SQL scope already restricts rows to the caller's
		// clusters and excludes the NULL-cluster global rules from anyone
		// without global view:alert. Kept so a future edit to the query cannot
		// silently reopen either.
		if r.ClusterID.Valid {
			if !access.PermitsCluster(uuid.UUID(r.ClusterID.Bytes)) {
				continue
			}
		} else if !access.HasGlobal {
			continue
		}
		result = append(result, toAlertRuleResponse(r))
	}
	return RespondItems(c, result)
}

// alertRuleScope is an alert rule's resolved scope: the scope type plus the
// binding columns the engine selects on for that type.
type alertRuleScope struct {
	ScopeType string
	ClusterID pgtype.UUID
	NodeID    pgtype.UUID
	VMVmid    pgtype.Int4
}

// alertRuleFields is the fully-merged view of a rule that both the create and
// the update path validate. Create and update used to carry their own copies
// of these checks and had already drifted — vm_vmid's cluster requirement was
// enforced on POST only — so the merged values go through one validator.
type alertRuleFields struct {
	Name            string
	Description     string
	Severity        string
	Metric          string
	Operator        string
	Threshold       float64
	DurationSeconds int32
	CooldownSeconds int32
	MessageTemplate string
	EscalationChain json.RawMessage
	Scope           alertRuleScope
}

// validateAlertRuleFields checks everything that does not depend on resolved
// UUIDs. Callers pass merged values, so an update that changes one half of a
// constrained pair is checked against the stored other half.
func validateAlertRuleFields(f alertRuleFields) error {
	if len(f.Name) > maxNameLen {
		return fiber.NewError(fiber.StatusBadRequest, "Name must be <= 255 characters")
	}
	if len(f.Description) > maxDescriptionLen {
		return fiber.NewError(fiber.StatusBadRequest, "Description must be <= 1024 characters")
	}
	if !validSeveritiesAlert[f.Severity] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid severity")
	}
	if !notifications.ValidMetric(f.Metric) {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid metric")
	}
	if !validOperators[f.Operator] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid operator")
	}
	if maxDuration := maxDurationFor(f.Metric); f.DurationSeconds < 0 || f.DurationSeconds > maxDuration {
		return fiber.NewError(fiber.StatusBadRequest,
			fmt.Sprintf("duration_seconds must be between 0 and %d", maxDuration))
	}
	if f.CooldownSeconds < 0 || f.CooldownSeconds > maxCooldownSec {
		return fiber.NewError(fiber.StatusBadRequest, "cooldown_seconds must be between 0 and 604800")
	}
	if len(f.MessageTemplate) > maxTemplateLen {
		return fiber.NewError(fiber.StatusBadRequest, "message_template must be <= 4096 characters")
	}
	if len(f.EscalationChain) > 0 {
		if err := validateEscalationChain(f.EscalationChain); err != nil {
			return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("Invalid escalation_chain: %v", err))
		}
	}
	if !validScopeTypes[f.Scope.ScopeType] {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid scope_type")
	}
	// A bounded metric needs a threshold inside its range, or the rule can
	// never fire. veeam_malware_status maps a verdict to 0-3, and the form's
	// default threshold of 90 — carried over from the percentage metrics —
	// produced exactly that: a rule accepted, stored, evaluated every tick,
	// and incapable of ever being true.
	if bounds, ok := notifications.MetricBounds(f.Metric); ok {
		if f.Threshold < bounds.Min || f.Threshold > bounds.Max {
			return fiber.NewError(fiber.StatusBadRequest,
				fmt.Sprintf("%s threshold must be between %g and %g", f.Metric, bounds.Min, bounds.Max))
		}
	}
	// Inventory-backed metrics support only some scopes. Rejecting the pairing
	// here matters: the engine errors out on an unsupported one once per tick,
	// forever, and the rule silently never fires.
	if scopes := notifications.MetricScopes(f.Metric); scopes != nil {
		if !slices.Contains(scopes, f.Scope.ScopeType) {
			return fiber.NewError(fiber.StatusBadRequest,
				fmt.Sprintf("%s supports %s scope", f.Metric, strings.Join(scopes, " or ")))
		}
	} else if f.Scope.ScopeType == "global" {
		// The converse: a per-node or per-VM metric has nothing to read at
		// global scope, so it would never fire either.
		return fiber.NewError(fiber.StatusBadRequest,
			fmt.Sprintf("%s does not support global scope", f.Metric))
	}
	return nil
}

// normalizeAlertRuleScope drops the bindings a scope type does not select on.
// Rescoping used to leave the old binding in the row: a vm rule changed to
// cluster scope kept its vm_vmid, which the engine then stamped onto every
// alert it raised, so cluster-wide alerts were filed under whichever guest the
// rule used to watch. Clearing here is also the only way to drop a vm_vmid or
// node_id, neither of which has an "empty" value on the wire.
func normalizeAlertRuleScope(s alertRuleScope) alertRuleScope {
	switch s.ScopeType {
	case "cluster":
		s.NodeID = pgtype.UUID{}
		s.VMVmid = pgtype.Int4{}
	case "node":
		s.VMVmid = pgtype.Int4{}
	case "vm":
		s.NodeID = pgtype.UUID{}
	case "global":
		// No bindings at all. Leaving a cluster_id behind would file every
		// alert this rule raises under that cluster, which is the opposite of
		// what global means — and would hand a cluster-scoped viewer an alert
		// about infrastructure shared with clusters they cannot see.
		s.ClusterID = pgtype.UUID{}
		s.NodeID = pgtype.UUID{}
		s.VMVmid = pgtype.Int4{}
	}
	return s
}

// validateAlertRuleScope requires the binding each scope type evaluates on.
// evaluateRule errors out for a rule missing its binding, once per tick and
// forever, so a rule accepted without one silently never fires.
func validateAlertRuleScope(s alertRuleScope) error {
	switch s.ScopeType {
	case "cluster":
		if !s.ClusterID.Valid {
			return fiber.NewError(fiber.StatusBadRequest, "cluster scope requires cluster_id")
		}
	case "node":
		if !s.NodeID.Valid {
			return fiber.NewError(fiber.StatusBadRequest, "node scope requires node_id")
		}
	case "vm":
		// VM scope keys on the stable (cluster_id, vmid) identity. The guest
		// is allowed to not exist yet — the rule binds to the inventory slot
		// and the engine starts evaluating when the VMID appears.
		if !s.ClusterID.Valid {
			return fiber.NewError(fiber.StatusBadRequest, "vm scope requires cluster_id")
		}
		if !s.VMVmid.Valid {
			return fiber.NewError(fiber.StatusBadRequest, "vm scope requires vm_vmid")
		}
	case "global":
		// No binding, by definition — the metric describes infrastructure no
		// cluster owns. normalizeAlertRuleScope has already cleared any the
		// caller sent, so there is nothing left to require.
	}
	return nil
}

// alertRuleScopeTouched reports whether a request carries any scope field.
// Updates re-check the scope only when it does: rules stored before these
// checks existed may hold an incoherent scope, and a body that just flips
// `enabled` (the UI toggle) has to keep working against them.
func alertRuleScopeTouched(req createAlertRuleRequest) bool {
	return req.ScopeType != "" || req.ClusterID != "" || req.NodeID != "" || req.VMVmid != nil
}

// resolveNodeCluster looks up a node and authorizes the caller against the
// cluster that owns it. The owning cluster is the authority for anything
// pinned to a node: checking only the request's cluster_id let a caller who
// manages cluster A point a rule at a node in cluster B and read B's metrics
// and hostname back out of the alerts it raised.
func resolveNodeCluster(c fiber.Ctx, queries *db.Queries, nodeID uuid.UUID, resource string) (uuid.UUID, error) {
	node, err := queries.GetNode(c.Context(), nodeID)
	if err != nil {
		return uuid.Nil, fiber.NewError(fiber.StatusBadRequest, "Unknown node_id")
	}
	// Pinning something to a node is always a manage operation on the cluster
	// that owns it.
	if err := requireClusterPerm(c, "manage", resource, node.ClusterID); err != nil {
		return uuid.Nil, err
	}
	return node.ClusterID, nil
}

// resolveAlertRuleScope merges the request's scope fields onto a base scope —
// zero for a create, the stored row for an update — and authorizes the result.
// Binding to a node adopts that node's cluster, so the rule always lands in a
// cluster the caller was checked against.
//
// recheckInherited re-authorizes a node the request did not itself supply; see
// the block at the end for when a caller must ask for that.
//
// On error the returned scope is zero: a partially merged one has had the
// node's cluster written into it, and no caller should act on that.
func (h *AlertHandler) resolveAlertRuleScope(c fiber.Ctx, base alertRuleScope, req createAlertRuleRequest, recheckInherited bool) (alertRuleScope, error) {
	var zero alertRuleScope

	s := base
	if req.ScopeType != "" {
		s.ScopeType = req.ScopeType
	}

	clusterFromRequest := false
	if req.ClusterID != "" {
		cid, err := uuid.Parse(req.ClusterID)
		if err != nil {
			return zero, fiber.NewError(fiber.StatusBadRequest, "Invalid cluster_id")
		}
		// Reassigning to a different cluster also needs manage:alert there.
		if !s.ClusterID.Valid || uuid.UUID(s.ClusterID.Bytes) != cid {
			if err := requireClusterPerm(c, "manage", "alert", cid); err != nil {
				return zero, err
			}
		}
		s.ClusterID = ClusterUUID(cid)
		clusterFromRequest = true
	}

	if req.NodeID != "" {
		nid, err := uuid.Parse(req.NodeID)
		if err != nil {
			return zero, fiber.NewError(fiber.StatusBadRequest, "Invalid node_id")
		}
		owner, err := resolveNodeCluster(c, h.queries, nid, "alert")
		if err != nil {
			return zero, err
		}
		if clusterFromRequest && uuid.UUID(s.ClusterID.Bytes) != owner {
			return zero, fiber.NewError(fiber.StatusBadRequest, "node_id belongs to a different cluster than cluster_id")
		}
		// The node's cluster wins over the stored one: re-pinning a rule to a
		// node in another cluster moves the rule there, and both clusters have
		// been permission-checked by the time we get here.
		s.ClusterID = ClusterUUID(owner)
		s.NodeID = pgtype.UUID{Bytes: nid, Valid: true}
	}

	if req.VMVmid != nil {
		if *req.VMVmid <= 0 {
			return zero, fiber.NewError(fiber.StatusBadRequest, "vm_vmid must be a positive Proxmox VMID")
		}
		s.VMVmid = pgtype.Int4{Int32: *req.VMVmid, Valid: true}
	}

	if err := validateAlertRuleBindings(req, s.ScopeType); err != nil {
		return zero, err
	}

	// An INHERITED node binding — one the request did not supply — is only as
	// trustworthy as the row it came from, and a row written before these
	// checks existed may point at a node in another cluster. Re-authorize it
	// whenever the caller asks, which is whenever the update leaves the rule
	// live: otherwise "enable it" or "set the threshold to 0" would arm a
	// probe reporting cluster B's node telemetry into cluster A's alerts.
	if req.NodeID == "" && s.ScopeType == "node" && s.NodeID.Valid && recheckInherited {
		owner, err := resolveNodeCluster(c, h.queries, uuid.UUID(s.NodeID.Bytes), "alert")
		if err != nil {
			return zero, err
		}
		if s.ClusterID.Valid && uuid.UUID(s.ClusterID.Bytes) != owner {
			return zero, fiber.NewError(fiber.StatusBadRequest, "the rule's node_id belongs to a different cluster")
		}
		s.ClusterID = ClusterUUID(owner)
	}

	return s, nil
}

// validateAlertRuleBindings rejects a binding the merged scope does not select
// on. normalizeAlertRuleScope drops STORED bindings a rescope leaves behind,
// but quietly dropping one the caller just sent would answer 200 to a request
// we did not honour — and for a node_id that would also move the rule to the
// node's cluster while discarding the node itself.
func validateAlertRuleBindings(req createAlertRuleRequest, scopeType string) error {
	if req.NodeID != "" && scopeType != "node" {
		return fiber.NewError(fiber.StatusBadRequest, "node_id is only valid for node scope")
	}
	if req.VMVmid != nil && scopeType != "vm" {
		return fiber.NewError(fiber.StatusBadRequest, "vm_vmid is only valid for vm scope")
	}
	return nil
}

// CreateRule creates a new alert rule.
func (h *AlertHandler) CreateRule(c fiber.Ctx) error {
	var req createAlertRuleRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Required on create only: an update leaves an absent field alone, but a
	// new rule has no stored value to fall back to.
	if req.Name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "Name is required")
	}
	if req.Threshold == nil {
		return fiber.NewError(fiber.StatusBadRequest, "Threshold is required")
	}

	if req.Severity == "" {
		req.Severity = "warning"
	}
	if req.ScopeType == "" {
		req.ScopeType = "cluster"
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	durationSeconds := int32(300)
	if req.DurationSeconds != nil {
		durationSeconds = *req.DurationSeconds
	}
	cooldownSeconds := int32(3600)
	if req.CooldownSeconds != nil {
		cooldownSeconds = *req.CooldownSeconds
	}
	escalationChain := json.RawMessage("[]")
	if len(req.EscalationChain) > 0 {
		escalationChain = req.EscalationChain
	}
	description := ""
	if req.Description != nil {
		description = *req.Description
	}
	messageTemplate := ""
	if req.MessageTemplate != nil {
		messageTemplate = *req.MessageTemplate
	}

	// This route carries no RBAC middleware, so every path below has to reach a
	// permission check before it touches the database. Naming the cluster makes
	// that possible for a node-scoped create: resolveAlertRuleScope checks
	// manage:alert on the named cluster before it looks the node up, so an
	// unauthorized caller never learns from "Unknown node_id" versus 403
	// whether a node UUID exists. Updates are already gated on the stored rule.
	if req.NodeID != "" && req.ClusterID == "" {
		return fiber.NewError(fiber.StatusBadRequest, "node scope requires cluster_id")
	}
	// A GLOBAL rule watches infrastructure no cluster owns, and only a global
	// grant may create one. Checked HERE, before the scope is resolved,
	// because normalizeAlertRuleScope clears cluster_id for a global rule
	// AFTER resolveAlertRuleScope has authorized against it — so a request
	// naming a cluster the caller does manage would otherwise pass that gate
	// and then have the binding stripped, minting a global rule on the
	// strength of a cluster-scoped grant. The result is invisible and
	// unmanageable to its own creator, since every global-rule read and write
	// requires the global grant.
	if req.ScopeType == "global" {
		if err := requirePerm(c, "manage", "alert"); err != nil {
			return err
		}
	}
	if req.ClusterID == "" && req.NodeID == "" {
		// Names no cluster at all. That is either a GLOBAL rule, which is
		// exactly what global manage:alert is for — it watches infrastructure
		// shared across every cluster — or a malformed one the scope check
		// below rejects. Either way an unauthorized caller must not get far
		// enough to tell those apart.
		if err := requirePerm(c, "manage", "alert"); err != nil {
			return err
		}
	}

	// Resolving the scope carries the real RBAC gate: a cluster_id is checked
	// against manage:alert on that cluster, a node_id against the cluster that
	// owns the node. Every scope type resolves to a cluster — a node binding
	// adopts its node's — so a rule with none is rejected by the scope check
	// below; the engine could never evaluate one anyway. Rows with a NULL
	// cluster predating this still load, list, and update normally.
	// recheckInherited is moot on create: the base scope carries no node, so
	// the only node in play is the one this request supplies and resolves.
	scope, err := h.resolveAlertRuleScope(c, alertRuleScope{ScopeType: req.ScopeType}, req, true)
	if err != nil {
		return err
	}
	scope = normalizeAlertRuleScope(scope)
	if err := validateAlertRuleScope(scope); err != nil {
		return err
	}

	if err := validateAlertRuleFields(alertRuleFields{
		Name:            req.Name,
		Description:     description,
		Severity:        req.Severity,
		Metric:          req.Metric,
		Operator:        req.Operator,
		Threshold:       *req.Threshold,
		DurationSeconds: durationSeconds,
		CooldownSeconds: cooldownSeconds,
		MessageTemplate: messageTemplate,
		EscalationChain: escalationChain,
		Scope:           scope,
	}); err != nil {
		return err
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	rule, err := h.queries.InsertAlertRule(c.Context(), db.InsertAlertRuleParams{
		Name:            req.Name,
		Description:     description,
		Enabled:         enabled,
		Severity:        req.Severity,
		Metric:          req.Metric,
		Operator:        req.Operator,
		Threshold:       *req.Threshold,
		DurationSeconds: durationSeconds,
		ScopeType:       scope.ScopeType,
		ClusterID:       scope.ClusterID,
		NodeID:          scope.NodeID,
		VmVmid:          scope.VMVmid,
		CooldownSeconds: cooldownSeconds,
		EscalationChain: escalationChain,
		CreatedBy:       userID,
		MessageTemplate: messageTemplate,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create alert rule")
	}

	AuditLog(c, h.queries, h.eventPub, rule.ClusterID, "alert_rule", rule.ID.String(), "alert_rule_created", nil)

	return c.Status(fiber.StatusCreated).JSON(toAlertRuleResponse(rule))
}

// GetRule returns a single alert rule.
func (h *AlertHandler) GetRule(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	rule, err := h.queries.GetAlertRule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rule not found")
	}

	if rule.ClusterID.Valid {
		if err := requireClusterPerm(c, "view", "alert", uuid.UUID(rule.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "view", "alert"); err != nil {
		return err
	}

	return c.JSON(toAlertRuleResponse(rule))
}

// mergeAlertRuleUpdate merges a partial update onto the existing rule and
// validates the result. Absent fields — empty strings, nil pointers, an empty
// escalation chain — keep their stored values, so a body of just
// {"enabled":false} (the UI enable/disable toggle) must not disturb the
// threshold or anything else. A present zero is applied: {"threshold":0}
// really sets 0, and "" on a pointer field really clears it.
//
// scope arrives already resolved and authorized (resolveAlertRuleScope), which
// keeps this function pure and testable without a database.
func mergeAlertRuleUpdate(existing db.AlertRule, req createAlertRuleRequest, scope alertRuleScope) (db.UpdateAlertRuleParams, error) {
	var zero db.UpdateAlertRuleParams

	name := existing.Name
	if req.Name != "" {
		name = req.Name
	}
	description := existing.Description
	if req.Description != nil {
		description = *req.Description
	}
	enabled := existing.Enabled
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	severity := existing.Severity
	if req.Severity != "" {
		severity = req.Severity
	}
	metric := existing.Metric
	if req.Metric != "" {
		metric = req.Metric
	}
	operator := existing.Operator
	if req.Operator != "" {
		operator = req.Operator
	}
	threshold := existing.Threshold
	if req.Threshold != nil {
		threshold = *req.Threshold
	}
	durationSeconds := existing.DurationSeconds
	if req.DurationSeconds != nil {
		durationSeconds = *req.DurationSeconds
	}
	cooldownSeconds := existing.CooldownSeconds
	if req.CooldownSeconds != nil {
		cooldownSeconds = *req.CooldownSeconds
	}
	escalationChain := existing.EscalationChain
	if len(req.EscalationChain) > 0 {
		escalationChain = req.EscalationChain
	}
	messageTemplate := existing.MessageTemplate
	if req.MessageTemplate != nil {
		messageTemplate = *req.MessageTemplate
	}

	// Only a request that actually touches the scope has to answer for it:
	// rules stored before these checks existed may hold an incoherent scope,
	// and the enable/disable toggle has to keep working against them.
	if alertRuleScopeTouched(req) {
		scope = normalizeAlertRuleScope(scope)
		if err := validateAlertRuleScope(scope); err != nil {
			return zero, err
		}
	}

	if err := validateAlertRuleFields(alertRuleFields{
		Name:            name,
		Description:     description,
		Severity:        severity,
		Metric:          metric,
		Operator:        operator,
		Threshold:       threshold,
		DurationSeconds: durationSeconds,
		CooldownSeconds: cooldownSeconds,
		MessageTemplate: messageTemplate,
		EscalationChain: escalationChain,
		Scope:           scope,
	}); err != nil {
		return zero, err
	}

	return db.UpdateAlertRuleParams{
		ID:              existing.ID,
		Name:            name,
		Description:     description,
		Enabled:         enabled,
		Severity:        severity,
		Metric:          metric,
		Operator:        operator,
		Threshold:       threshold,
		DurationSeconds: durationSeconds,
		ScopeType:       scope.ScopeType,
		ClusterID:       scope.ClusterID,
		NodeID:          scope.NodeID,
		VmVmid:          scope.VMVmid,
		CooldownSeconds: cooldownSeconds,
		EscalationChain: escalationChain,
		MessageTemplate: messageTemplate,
	}, nil
}

// UpdateRule updates an existing alert rule.
func (h *AlertHandler) UpdateRule(c fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid rule ID")
	}

	existing, err := h.queries.GetAlertRule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rule not found")
	}

	if existing.ClusterID.Valid {
		if err := requireClusterPerm(c, "manage", "alert", uuid.UUID(existing.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "manage", "alert"); err != nil {
		return err
	}

	var req createAlertRuleRequest
	if err := c.Bind().Body(&req); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid request body")
	}

	// Converting an existing rule TO global needs the global grant, exactly as
	// creating one does. The gate above answers for the rule as it stands, and
	// a cluster-scoped holder converting their own cluster-A rule would
	// otherwise pass it and walk the rule out of their own reach — a global
	// rule can only be read, retuned or deleted with the global grant.
	if req.ScopeType == "global" && existing.ScopeType != "global" {
		if err := requirePerm(c, "manage", "alert"); err != nil {
			return err
		}
	}

	// Resolving the scope re-runs the RBAC gate for whatever the request
	// reassigns: a new cluster_id against that cluster, a node_id against the
	// cluster that owns the node.
	//
	// A stored node binding is re-authorized too, but only when this update
	// leaves the rule live. Turning a rule off has to stay possible for anyone
	// who can reach it — that is how an operator stops a bad one — while
	// enabling or retuning it must answer for the node it points at.
	staysEnabled := existing.Enabled
	if req.Enabled != nil {
		staysEnabled = *req.Enabled
	}
	scope, err := h.resolveAlertRuleScope(c, alertRuleScope{
		ScopeType: existing.ScopeType,
		ClusterID: existing.ClusterID,
		NodeID:    existing.NodeID,
		VMVmid:    existing.VmVmid,
	}, req, alertRuleScopeTouched(req) || staysEnabled)
	if err != nil {
		return err
	}

	params, err := mergeAlertRuleUpdate(existing, req, scope)
	if err != nil {
		return err
	}

	rule, err := h.queries.UpdateAlertRule(c.Context(), params)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update alert rule")
	}

	// Attribute to the rule's cluster after the merge, so a reassignment lands
	// in the new cluster's audit log. A cluster-less audit row is readable only
	// with global view:audit, which would hide the edit from the very operators
	// who can see the rule.
	AuditLog(c, h.queries, h.eventPub, rule.ClusterID, "alert_rule", id.String(), "alert_rule_updated", nil)

	return c.JSON(toAlertRuleResponse(rule))
}

// DeleteRule deletes an alert rule.
func (h *AlertHandler) DeleteRule(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	existing, err := h.queries.GetAlertRule(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Rule not found")
	}

	if existing.ClusterID.Valid {
		if err := requireClusterPerm(c, "manage", "alert", uuid.UUID(existing.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "manage", "alert"); err != nil {
		return err
	}

	if err := h.queries.DeleteAlertRule(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete alert rule")
	}

	AuditLog(c, h.queries, h.eventPub, existing.ClusterID, "alert_rule", id.String(), "alert_rule_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}

// ====== Alert History ======

// ListAlerts lists alert history with optional filters.
func (h *AlertHandler) ListAlerts(c fiber.Ctx, p *apischema.Params) error {
	access, err := accessibleClusters(c, "view", "alert")
	if err != nil {
		return err
	}

	limit := int(p.Int("limit"))
	offset := int(p.Int("offset"))

	// Both vocabularies are the declaration's enums now, and both include the
	// EMPTY string because "" has always reached SQL as "do not filter".
	state := p.String("state")
	severity := p.String("severity")
	clusterIDStr := p.String("filter_cluster_id")

	// No cluster filter must reach SQL as NULL (match all, RBAC-trimmed per
	// row below) — a zero uuid.UUID would instead match cluster_id = '0000…'
	// and return nothing.
	var clusterID pgtype.UUID
	if clusterIDStr != "" {
		parsed, parseErr := parseParamUUID(clusterIDStr)
		if parseErr != nil {
			return parseErr
		}
		if !access.PermitsCluster(parsed) {
			return fiber.NewError(fiber.StatusForbidden, "Insufficient permissions")
		}
		clusterID = pgtype.UUID{Bytes: parsed, Valid: true}
	}

	// Scoped in SQL for the same reason as ListRules: the page is cut before
	// the per-row trim, so an unscoped fetch pages over the global rowset.
	scope, query := clusterScopeFilter(access)
	if !query {
		return RespondItems(c, []alertHistoryResponse{})
	}

	alerts, err := h.queries.ListAlertHistoryFiltered(c.Context(), db.ListAlertHistoryFilteredParams{
		State:                state,
		Severity:             severity,
		ClusterID:            clusterID,
		LimitVal:             safeconv.Int32(limit),
		OffsetVal:            safeconv.Int32(offset),
		AccessibleClusterIds: scope,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list alerts")
	}

	result := make([]alertHistoryResponse, 0, len(alerts))
	for _, a := range alerts {
		// Defense-in-depth, as in ListRules.
		if a.ClusterID.Valid {
			if !access.PermitsCluster(uuid.UUID(a.ClusterID.Bytes)) {
				continue
			}
		} else if !access.HasGlobal {
			continue
		}
		result = append(result, toAlertHistoryResponse(a))
	}
	return RespondItems(c, result)
}

// GetAlert returns a single alert.
func (h *AlertHandler) GetAlert(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	alert, err := h.queries.GetAlertHistory(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Alert not found")
	}

	if alert.ClusterID.Valid {
		if err := requireClusterPerm(c, "view", "alert", uuid.UUID(alert.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "view", "alert"); err != nil {
		return err
	}

	return c.JSON(toAlertHistoryResponse(alert))
}

// GetAlertSummary returns active alert counts.
func (h *AlertHandler) GetAlertSummary(c fiber.Ctx, _ *apischema.Params) error {
	summary, err := h.queries.GetAlertSummary(c.Context())
	if err != nil {
		return c.JSON(alertSummaryResponse{})
	}

	return c.JSON(alertSummaryResponse{
		FiringCount:       summary.FiringCount,
		PendingCount:      summary.PendingCount,
		AcknowledgedCount: summary.AcknowledgedCount,
		CriticalFiring:    summary.CriticalFiring,
		WarningFiring:     summary.WarningFiring,
		InfoFiring:        summary.InfoFiring,
	})
}

// AcknowledgeAlert acknowledges a firing alert.
func (h *AlertHandler) AcknowledgeAlert(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	alert, err := h.queries.GetAlertHistory(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Alert not found")
	}

	if alert.ClusterID.Valid {
		if err := requireClusterPerm(c, "acknowledge", "alert", uuid.UUID(alert.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "acknowledge", "alert"); err != nil {
		return err
	}

	if alert.State != "firing" {
		return fiber.NewError(fiber.StatusConflict, "Alert is not in firing state")
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)
	if err := h.queries.AcknowledgeAlert(c.Context(), db.AcknowledgeAlertParams{
		ID:             id,
		AcknowledgedBy: pgtype.UUID{Bytes: userID, Valid: true},
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to acknowledge alert")
	}

	// Carry the alert's own cluster, the same value the permission check and the
	// event below already use. alert_history.cluster_id is nullable and a NULL
	// there means a global alert, so passing it through keeps the audit row as
	// visible as the alert itself — rather than hiding every acknowledgement
	// behind global view:audit.
	AuditLog(c, h.queries, h.eventPub, alert.ClusterID, "alert", id.String(), "alert_acknowledged", nil)

	if h.eventPub != nil {
		clusterID := ""
		if alert.ClusterID.Valid {
			cid, _ := uuid.FromBytes(alert.ClusterID.Bytes[:])
			clusterID = cid.String()
		}
		h.eventPub.ClusterEvent(c.Context(), clusterID, events.KindAlertStateChange, "alert", id.String(), "acknowledged")
	}

	return c.JSON(fiber.Map{"status": "acknowledged"})
}

// ResolveAlert resolves a firing or acknowledged alert.
func (h *AlertHandler) ResolveAlert(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	alert, err := h.queries.GetAlertHistory(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Alert not found")
	}

	if alert.ClusterID.Valid {
		if err := requireClusterPerm(c, "acknowledge", "alert", uuid.UUID(alert.ClusterID.Bytes)); err != nil {
			return err
		}
	} else if err := requirePerm(c, "acknowledge", "alert"); err != nil {
		return err
	}

	if alert.State != "firing" && alert.State != "acknowledged" {
		return fiber.NewError(fiber.StatusConflict, "Alert cannot be resolved from current state")
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)
	if err := h.queries.ResolveAlert(c.Context(), db.ResolveAlertParams{
		ID:         id,
		ResolvedBy: pgtype.UUID{Bytes: userID, Valid: true},
	}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to resolve alert")
	}

	AuditLog(c, h.queries, h.eventPub, alert.ClusterID, "alert", id.String(), "alert_resolved", nil)

	if h.eventPub != nil {
		clusterID := ""
		if alert.ClusterID.Valid {
			cid, _ := uuid.FromBytes(alert.ClusterID.Bytes[:])
			clusterID = cid.String()
		}
		h.eventPub.ClusterEvent(c.Context(), clusterID, events.KindAlertStateChange, "alert", id.String(), "resolved")
	}

	return c.JSON(fiber.Map{"status": "resolved"})
}

// ListAlertsByCluster lists alerts for a specific cluster.
func (h *AlertHandler) ListAlertsByCluster(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	limit := int(p.Int("limit"))
	offset := int(p.Int("offset"))

	alerts, err := h.queries.ListAlertHistoryByCluster(c.Context(), db.ListAlertHistoryByClusterParams{
		ClusterID: pgtype.UUID{Bytes: clusterID, Valid: true},
		Limit:     safeconv.Int32(limit),
		Offset:    safeconv.Int32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list alerts")
	}

	result := make([]alertHistoryResponse, len(alerts))
	for i, a := range alerts {
		result[i] = toAlertHistoryResponse(a)
	}
	return RespondItems(c, result)
}

// CountActiveAlertsByCluster returns active alert counts for a cluster.
func (h *AlertHandler) CountActiveAlertsByCluster(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	counts, err := h.queries.CountActiveAlertsByCluster(c.Context(), pgtype.UUID{Bytes: clusterID, Valid: true})
	if err != nil {
		return c.JSON(fiber.Map{"firing": 0, "pending": 0, "acknowledged": 0})
	}

	return c.JSON(fiber.Map{
		"firing":       counts.FiringCount,
		"pending":      counts.PendingCount,
		"acknowledged": counts.AcknowledgedCount,
	})
}

// ====== Notification Channels ======

// ListChannels lists all notification channels.
func (h *AlertHandler) ListChannels(c fiber.Ctx, _ *apischema.Params) error {
	channels, err := h.queries.ListNotificationChannels(c.Context())
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list channels")
	}

	result := make([]notificationChannelResponse, len(channels))
	for i, ch := range channels {
		result[i] = toNotificationChannelResponse(ch)
	}
	return RespondItems(c, result)
}

// CreateChannel creates a new notification channel.
func (h *AlertHandler) CreateChannel(c fiber.Ctx, p *apischema.Params) error {
	config, err := channelConfigJSON(p)
	if err != nil {
		return err
	}

	encrypted, err := crypto.Encrypt(string(config), h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt config")
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	ch, err := h.queries.InsertNotificationChannel(c.Context(), db.InsertNotificationChannelParams{
		Name:            p.String("name"),
		ChannelType:     p.String("channel_type"),
		ConfigEncrypted: encrypted,
		Enabled:         p.Bool("enabled"),
		CreatedBy:       userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create channel")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "notification_channel", ch.ID.String(), "channel_created", nil)

	return c.Status(fiber.StatusCreated).JSON(toNotificationChannelResponse(ch))
}

// GetChannel returns a single notification channel.
func (h *AlertHandler) GetChannel(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	ch, err := h.queries.GetNotificationChannel(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Channel not found")
	}

	return c.JSON(toNotificationChannelResponse(ch))
}

// UpdateChannel updates a notification channel.
func (h *AlertHandler) UpdateChannel(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	existing, err := h.queries.GetNotificationChannel(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Channel not found")
	}

	// Every field reads the EMPTY value as absent rather than as a clear,
	// which is what the pre-migration `if req.X != ""` tests did. `enabled` is
	// the one exception and the reason it carries no declared default: omitting
	// it has to leave the stored flag alone.
	name := existing.Name
	if v := p.String("name"); v != "" {
		name = v
	}
	channelType := existing.ChannelType
	if v := p.String("channel_type"); v != "" {
		channelType = v
	}
	configEncrypted := existing.ConfigEncrypted
	if p.Has("config") {
		config, cErr := channelConfigJSON(p)
		if cErr != nil {
			return cErr
		}
		enc, encErr := crypto.Encrypt(string(config), h.encryptionKey)
		if encErr != nil {
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to encrypt config")
		}
		configEncrypted = enc
	}
	enabled := existing.Enabled
	if v, supplied := p.OptBool("enabled"); supplied {
		enabled = v
	}

	ch, err := h.queries.UpdateNotificationChannel(c.Context(), db.UpdateNotificationChannelParams{
		ID:              id,
		Name:            name,
		ChannelType:     channelType,
		ConfigEncrypted: configEncrypted,
		Enabled:         enabled,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update channel")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "notification_channel", id.String(), "channel_updated", nil)

	return c.JSON(toNotificationChannelResponse(ch))
}

// DeleteChannel deletes a notification channel.
func (h *AlertHandler) DeleteChannel(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	if _, err := h.queries.GetNotificationChannel(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Channel not found")
	}

	if err := h.queries.DeleteNotificationChannel(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete channel")
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "notification_channel", id.String(), "channel_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}

// TestChannel sends a test notification through a channel.
func (h *AlertHandler) TestChannel(c fiber.Ctx, p *apischema.Params) error {
	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	ch, err := h.queries.GetNotificationChannel(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Channel not found")
	}

	if h.registry == nil {
		return fiber.NewError(fiber.StatusServiceUnavailable, "Notification dispatchers not available")
	}

	dispatcher, ok := h.registry.Get(ch.ChannelType)
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("No dispatcher for channel type: %s", ch.ChannelType))
	}

	configJSON, err := crypto.Decrypt(ch.ConfigEncrypted, h.encryptionKey)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to decrypt channel config")
	}

	payload := notifications.AlertPayload{
		RuleName:        "Test Alert Rule",
		RuleID:          "00000000-0000-0000-0000-000000000000",
		Severity:        "warning",
		State:           "firing",
		Metric:          "cpu_usage",
		Operator:        ">",
		Threshold:       90.0,
		CurrentValue:    95.5,
		ResourceName:    "test-node-01",
		NodeName:        "test-node-01",
		ClusterID:       "test-cluster",
		Message:         "This is a test notification from Nexara.",
		FiredAt:         time.Now().UTC().Format(time.RFC3339Nano),
		EscalationLevel: 0,
	}

	if err := dispatcher.Send(c.Context(), json.RawMessage(configJSON), payload); err != nil {
		// Log the full error for debugging; return generic message to client.
		h.eventPub.ClusterEvent(c.Context(), "", events.KindAlertFired, "notification_channel", id.String(), "test_failed")
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
			"success": false,
			"message": "Test notification failed. Check server logs for details.",
		})
	}

	AuditLog(c, h.queries, h.eventPub, pgtype.UUID{}, "notification_channel", id.String(), "channel_tested", nil)

	return c.JSON(fiber.Map{
		"success": true,
		"message": "Test notification sent successfully",
	})
}

// ====== Maintenance Windows ======

// ListMaintenanceWindows lists maintenance windows for a cluster.
func (h *AlertHandler) ListMaintenanceWindows(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	limit := int(p.Int("limit"))
	offset := int(p.Int("offset"))

	windows, err := h.queries.ListMaintenanceWindows(c.Context(), db.ListMaintenanceWindowsParams{
		ClusterID: clusterID,
		Limit:     safeconv.Int32(limit),
		Offset:    safeconv.Int32(offset),
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to list maintenance windows")
	}

	result := make([]maintenanceWindowResponse, len(windows))
	for i, w := range windows {
		result[i] = toMaintenanceWindowResponse(w)
	}
	return RespondItems(c, result)
}

// CreateMaintenanceWindow creates a new maintenance window.
func (h *AlertHandler) CreateMaintenanceWindow(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	startsAt, err := time.Parse(time.RFC3339, p.String("starts_at"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid starts_at format (use RFC3339)")
	}
	endsAt, err := time.Parse(time.RFC3339, p.String("ends_at"))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "Invalid ends_at format (use RFC3339)")
	}
	if !endsAt.After(startsAt) {
		return fiber.NewError(fiber.StatusBadRequest, "ends_at must be after starts_at")
	}

	// The EMPTY node_id means "the whole cluster", which the declaration keeps
	// expressible with an empty-or-uuid pattern rather than the uuid format.
	var nodeID pgtype.UUID
	if raw := p.String("node_id"); raw != "" {
		nid, parseErr := parseParamUUID(raw)
		if parseErr != nil {
			return parseErr
		}
		// The OWNING cluster is the authority, not the one in the path: this
		// call is what stops a caller who manages cluster A pinning a window to
		// a node in cluster B. It cannot hoist into the route's middleware,
		// which resolves the path's cluster and nothing else.
		owner, err := resolveNodeCluster(c, h.queries, nid, "maintenance_window")
		if err != nil {
			return err
		}
		if owner != clusterID {
			return fiber.NewError(fiber.StatusBadRequest, "node_id belongs to a different cluster")
		}
		nodeID = pgtype.UUID{Bytes: nid, Valid: true}
	}

	userID, _ := c.Locals("user_id").(uuid.UUID)

	window, err := h.queries.InsertMaintenanceWindow(c.Context(), db.InsertMaintenanceWindowParams{
		ClusterID:   clusterID,
		NodeID:      nodeID,
		Description: p.String("description"),
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		CreatedBy:   userID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to create maintenance window")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(clusterID), "maintenance_window", window.ID.String(), "maintenance_window_created", nil)

	return c.Status(fiber.StatusCreated).JSON(toMaintenanceWindowResponse(window))
}

// UpdateMaintenanceWindow updates a maintenance window.
func (h *AlertHandler) UpdateMaintenanceWindow(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	existing, err := h.queries.GetMaintenanceWindow(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Maintenance window not found")
	}

	// Verify the maintenance window belongs to the cluster in the URL.
	if existing.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Maintenance window not found")
	}

	// Every field reads the EMPTY value as absent rather than as a clear, which
	// is what the pre-migration `if req.X != ""` tests did — so there is no way
	// to blank a description through this route, and there never was.
	description := existing.Description
	if v := p.String("description"); v != "" {
		description = v
	}
	startsAt := existing.StartsAt
	if v := p.String("starts_at"); v != "" {
		parsed, parseErr := time.Parse(time.RFC3339, v)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid starts_at format")
		}
		startsAt = parsed
	}
	endsAt := existing.EndsAt
	if v := p.String("ends_at"); v != "" {
		parsed, parseErr := time.Parse(time.RFC3339, v)
		if parseErr != nil {
			return fiber.NewError(fiber.StatusBadRequest, "Invalid ends_at format")
		}
		endsAt = parsed
	}
	if !endsAt.After(startsAt) {
		return fiber.NewError(fiber.StatusBadRequest, "ends_at must be after starts_at")
	}
	nodeID := existing.NodeID
	if raw := p.String("node_id"); raw != "" {
		nid, parseErr := parseParamUUID(raw)
		if parseErr != nil {
			return parseErr
		}
		// The owning cluster is the authority here too — see the create.
		owner, ownerErr := resolveNodeCluster(c, h.queries, nid, "maintenance_window")
		if ownerErr != nil {
			return ownerErr
		}
		if owner != clusterID {
			return fiber.NewError(fiber.StatusBadRequest, "node_id belongs to a different cluster")
		}
		nodeID = pgtype.UUID{Bytes: nid, Valid: true}
	}

	window, err := h.queries.UpdateMaintenanceWindow(c.Context(), db.UpdateMaintenanceWindowParams{
		ID:          id,
		Description: description,
		StartsAt:    startsAt,
		EndsAt:      endsAt,
		NodeID:      nodeID,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to update maintenance window")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(existing.ClusterID), "maintenance_window", id.String(), "maintenance_window_updated", nil)

	return c.JSON(toMaintenanceWindowResponse(window))
}

// DeleteMaintenanceWindow deletes a maintenance window.
func (h *AlertHandler) DeleteMaintenanceWindow(c fiber.Ctx, p *apischema.Params) error {
	clusterID, err := parseParamUUID(p.String("cluster_id"))
	if err != nil {
		return err
	}

	id, err := parseParamUUID(p.String("id"))
	if err != nil {
		return err
	}

	existing, err := h.queries.GetMaintenanceWindow(c.Context(), id)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "Maintenance window not found")
	}

	// Verify the maintenance window belongs to the cluster in the URL.
	if existing.ClusterID != clusterID {
		return fiber.NewError(fiber.StatusNotFound, "Maintenance window not found")
	}

	if err := h.queries.DeleteMaintenanceWindow(c.Context(), id); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "Failed to delete maintenance window")
	}

	AuditLog(c, h.queries, h.eventPub, ClusterUUID(existing.ClusterID), "maintenance_window", id.String(), "maintenance_window_deleted", nil)

	return c.SendStatus(fiber.StatusNoContent)
}
