package ws

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// TestChannelClusterID covers the channel-name parsing helper that the
// subscribe-time RBAC check relies on. ValidateChannel already gates
// the format upstream, but ChannelClusterID's defensive returns are
// part of the contract — make sure malformed input doesn't accidentally
// extract a partial UUID that the permission check would then succeed on.
func TestChannelClusterID(t *testing.T) {
	cases := []struct {
		name      string
		channel   string
		wantID    string
		wantOK    bool
	}{
		{
			name:    "valid cluster metrics channel",
			channel: "cluster:550e8400-e29b-41d4-a716-446655440000:metrics",
			wantID:  "550e8400-e29b-41d4-a716-446655440000",
			wantOK:  true,
		},
		{
			name:    "valid cluster events channel",
			channel: "cluster:11111111-2222-3333-4444-555555555555:events",
			wantID:  "11111111-2222-3333-4444-555555555555",
			wantOK:  true,
		},
		{
			name:    "valid cluster alerts channel",
			channel: "cluster:aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee:alerts",
			wantID:  "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			wantOK:  true,
		},
		{
			name:    "system events is not a cluster channel",
			channel: "system:events",
			wantID:  "",
			wantOK:  false,
		},
		{
			name:    "missing prefix",
			channel: "550e8400-e29b-41d4-a716-446655440000:metrics",
			wantID:  "",
			wantOK:  false,
		},
		{
			name:    "no colons",
			channel: "cluster",
			wantID:  "",
			wantOK:  false,
		},
		{
			name:    "two colons but malformed",
			channel: "cluster::metrics",
			// SplitN gives 3 parts: ["cluster", "", "metrics"] — the
			// empty string IS returned, but uuid.Parse will reject it
			// downstream in canSubscribe. This is acceptable: the
			// helper trusts ValidateChannel to gate format and only
			// extracts the substring; the permission path catches
			// invalid UUIDs.
			wantID: "",
			wantOK: true,
		},
		{
			name:    "empty string",
			channel: "",
			wantID:  "",
			wantOK:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := ChannelClusterID(tc.channel)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if id != tc.wantID {
				t.Errorf("id = %q, want %q", id, tc.wantID)
			}
		})
	}
}

// TestClientCanSubscribe_SystemEventsAlwaysAllowed verifies that the
// non-cluster `system:events` channel doesn't go through the RBAC
// check at all — it's a global channel that any authenticated session
// can subscribe to.
func TestClientCanSubscribe_SystemEventsAlwaysAllowed(t *testing.T) {
	calls := atomic.Int32{}
	checker := func(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID) (bool, error) {
		calls.Add(1)
		return false, nil // always deny — should never be called
	}
	c := &Client{
		id:              "test",
		logger:          testLogger(),
		userID:          uuid.New(),
		checkPermission: checker,
	}
	if !c.canSubscribe("system:events") {
		t.Errorf("system:events should always be allowed for authenticated users")
	}
	if calls.Load() != 0 {
		t.Errorf("permission checker was called %d times for system:events; expected 0", calls.Load())
	}
}

// TestClientCanSubscribe_ClusterChannelChecksRBAC verifies that
// cluster-scoped channels invoke the permission checker with the
// correct (action, resource, scope_type, scope_id) tuple, AND that the
// result determines whether the subscribe is allowed.
//
// This is the core regression test for security review H1: without
// this gate, any authenticated user can subscribe to any cluster's
// metric channel.
func TestClientCanSubscribe_ClusterChannelChecksRBAC(t *testing.T) {
	clusterUUID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	userID := uuid.New()
	channel := "cluster:550e8400-e29b-41d4-a716-446655440000:metrics"

	t.Run("permitted user can subscribe", func(t *testing.T) {
		var sawAction, sawResource, sawScopeType string
		var sawScopeID uuid.UUID
		var sawUserID uuid.UUID

		checker := func(_ context.Context, uid uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
			sawUserID = uid
			sawAction = action
			sawResource = resource
			sawScopeType = scopeType
			sawScopeID = scopeID
			return true, nil
		}
		c := &Client{
			id:              "test",
			logger:          testLogger(),
			userID:          userID,
			checkPermission: checker,
		}
		if !c.canSubscribe(channel) {
			t.Fatalf("expected canSubscribe to allow permitted user")
		}
		if sawUserID != userID {
			t.Errorf("user_id = %v, want %v", sawUserID, userID)
		}
		if sawAction != "view" {
			t.Errorf("action = %q, want %q", sawAction, "view")
		}
		if sawResource != "cluster" {
			t.Errorf("resource = %q, want %q", sawResource, "cluster")
		}
		if sawScopeType != "cluster" {
			t.Errorf("scope_type = %q, want %q", sawScopeType, "cluster")
		}
		if sawScopeID != clusterUUID {
			t.Errorf("scope_id = %v, want %v", sawScopeID, clusterUUID)
		}
	})

	t.Run("denied user cannot subscribe", func(t *testing.T) {
		checker := func(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID) (bool, error) {
			return false, nil
		}
		c := &Client{
			id:              "test",
			logger:          testLogger(),
			userID:          userID,
			checkPermission: checker,
		}
		if c.canSubscribe(channel) {
			t.Errorf("expected canSubscribe to deny user without view:cluster")
		}
	})

	t.Run("checker error fails closed", func(t *testing.T) {
		checker := func(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID) (bool, error) {
			return false, errors.New("redis down")
		}
		c := &Client{
			id:              "test",
			logger:          testLogger(),
			userID:          userID,
			checkPermission: checker,
		}
		if c.canSubscribe(channel) {
			t.Errorf("expected canSubscribe to fail closed on engine error, got allowed")
		}
	})
}

// TestClientCanSubscribe_NoCheckerFallsOpen documents the test-fixture
// fallback: if no permission checker is wired (e.g. integration tests
// constructing the server without an RBAC engine), the subscribe path
// allows the channel and logs a warning. Production deploys MUST set
// the engine — main.go pulls it from `srv.RBACEngine()` and the WS
// server logs a warning at startup when it's nil.
func TestClientCanSubscribe_NoCheckerFallsOpen(t *testing.T) {
	c := &Client{
		id:              "test",
		logger:          testLogger(),
		userID:          uuid.New(),
		checkPermission: nil,
	}
	if !c.canSubscribe("cluster:550e8400-e29b-41d4-a716-446655440000:metrics") {
		t.Errorf("expected nil-checker fall-open for test fixtures")
	}
}

// TestClientCanSubscribe_InvalidUUIDFailsClosed verifies that even
// though ValidateChannel should have rejected a malformed channel
// upstream, the defensive uuid.Parse in canSubscribe won't accidentally
// allow a channel with a syntactically valid prefix but a bogus UUID.
func TestClientCanSubscribe_InvalidUUIDFailsClosed(t *testing.T) {
	checker := func(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID) (bool, error) {
		return true, nil // would allow if reached
	}
	c := &Client{
		id:              "test",
		logger:          testLogger(),
		userID:          uuid.New(),
		checkPermission: checker,
	}
	if c.canSubscribe("cluster:not-a-uuid:metrics") {
		t.Errorf("expected canSubscribe to reject invalid cluster UUID")
	}
}
