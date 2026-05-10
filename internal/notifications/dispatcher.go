package notifications

import (
	"context"
	"encoding/json"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

// AlertPayload contains all template variables for notification rendering.
type AlertPayload struct {
	RuleName        string  `json:"rule_name"`
	RuleID          string  `json:"rule_id"`
	Severity        string  `json:"severity"`
	State           string  `json:"state"`
	Metric          string  `json:"metric"`
	Operator        string  `json:"operator"`
	Threshold       float64 `json:"threshold"`
	CurrentValue    float64 `json:"current_value"`
	ResourceName    string  `json:"resource_name"`
	NodeName        string  `json:"node_name"`
	ClusterID       string  `json:"cluster_id"`
	Message         string  `json:"message"`
	FiredAt         string  `json:"fired_at"`
	EscalationLevel int     `json:"escalation_level"`
}

// Dispatcher sends a notification to a configured channel.
type Dispatcher interface {
	Send(ctx context.Context, config json.RawMessage, payload AlertPayload) error
	Type() string
}

// Registry maps channel_type strings to Dispatcher implementations.
type Registry struct {
	dispatchers map[string]Dispatcher
}

// NewRegistry creates a new dispatcher registry.
func NewRegistry() *Registry {
	return &Registry{dispatchers: make(map[string]Dispatcher)}
}

// Register adds a dispatcher to the registry.
func (r *Registry) Register(d Dispatcher) {
	r.dispatchers[d.Type()] = d
}

// Get returns the dispatcher for a channel type.
func (r *Registry) Get(channelType string) (Dispatcher, bool) {
	d, ok := r.dispatchers[channelType]
	return d, ok
}

// Types returns the registered channel types in no particular order.
// Used by the DLQ replay handler to validate that a stored channel_type is
// still serviceable before attempting a replay.
func (r *Registry) Types() []string {
	out := make([]string, 0, len(r.dispatchers))
	for t := range r.dispatchers {
		out = append(out, t)
	}
	return out
}

// BuildRegistry constructs a Registry pre-populated with every dispatcher
// shipped by Nexara. Both the API server and the scheduler call this so that
// adding a new dispatcher is a single-site change.
func BuildRegistry(queries *db.Queries) *Registry {
	r := NewRegistry()
	r.Register(&SMTPDispatcher{})
	r.Register(&SlackDispatcher{})
	r.Register(&DiscordDispatcher{})
	r.Register(&TeamsDispatcher{})
	r.Register(&TelegramDispatcher{})
	r.Register(&WebhookDispatcher{})
	r.Register(&PagerDutyDispatcher{})
	r.Register(NewExpoPushDispatcher(queries))
	return r
}
