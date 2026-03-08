package notifications

import (
	"context"
	"encoding/json"
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
