package events

import (
	"testing"

	"github.com/google/uuid"
)

// TestPublishChannelRouting pins the routing switch in Publish. It is the
// whole of the audit-stream gate: which Redis channel an event lands on
// decides which WS room fans it out, and therefore which permission a
// subscriber needs. A reordering of these cases regresses the gate silently —
// every internal/ws test still passes, because they never see the publisher.
//
// The rule: audit entries ride an audit room (gated on view:audit, per cluster
// or global); everything else rides the operational room for its scope.
func TestPublishChannelRouting(t *testing.T) {
	clusterID := "550e8400-e29b-41d4-a716-446655440000"

	tests := []struct {
		name  string
		event Event
		want  string
	}{
		{
			name:  "non-cluster audit entry goes to the gated system audit room",
			event: Event{Kind: KindAuditEntry},
			want:  SystemAuditRedisChannel,
		},
		{
			name:  "non-cluster operational event stays on the open system room",
			event: Event{Kind: KindTaskUpdate},
			want:  SystemRedisChannel,
		},
		{
			name:  "cluster audit entry goes to that cluster's audit room",
			event: Event{Kind: KindAuditEntry, ClusterID: clusterID},
			want:  "nexara:audit:" + clusterID,
		},
		{
			name:  "cluster operational event goes to that cluster's events room",
			event: Event{Kind: KindVMStateChange, ClusterID: clusterID},
			want:  "nexara:events:" + clusterID,
		},
		{
			// ClusterUUID(uuid.Nil) yields a valid-but-zero UUID, which is not
			// a cluster. Routing it as one would put audit metadata in a room
			// any global view:cluster holder can join.
			name:  "nil-uuid cluster audit entry is treated as non-cluster",
			event: Event{Kind: KindAuditEntry, ClusterID: uuid.Nil.String()},
			want:  SystemAuditRedisChannel,
		},
		{
			name:  "nil-uuid cluster operational event is treated as non-cluster",
			event: Event{Kind: KindPBSChange, ClusterID: uuid.Nil.String()},
			want:  SystemRedisChannel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := publishChannel(tt.event); got != tt.want {
				t.Errorf("publishChannel(%+v) = %q, want %q", tt.event, got, tt.want)
			}
		})
	}
}
