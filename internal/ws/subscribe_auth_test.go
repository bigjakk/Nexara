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

// TestClientCanSubscribe_NoCheckerFailsClosed locks down the 5.1
// posture: when no permission checker is wired (test fixtures, or a
// misconfigured server), cluster channel subscribes are denied. There
// is no longer a synthetic-admin fall-open — production main.go always
// installs the engine, and a missing engine is a misconfiguration that
// must not silently expose cross-tenant data.
func TestClientCanSubscribe_NoCheckerFailsClosed(t *testing.T) {
	c := &Client{
		id:              "test",
		logger:          testLogger(),
		userID:          uuid.New(),
		checkPermission: nil,
	}
	if c.canSubscribe("cluster:550e8400-e29b-41d4-a716-446655440000:metrics") {
		t.Errorf("expected canSubscribe to deny when no checker wired")
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

// TestClientCanSubscribe_SystemAuditRequiresViewAudit is the regression test
// for the audit-stream gap: before the system room was split, any
// authenticated session — including a custom role holding no grants at all —
// could subscribe to the non-cluster event stream and watch live metadata for
// every login, password change, TOTP enrollment, settings write, and
// user/role/API-key administration.
//
// The audit half now requires the same view:audit the REST audit endpoints do.
func TestClientCanSubscribe_SystemAuditRequiresViewAudit(t *testing.T) {
	userID := uuid.New()

	t.Run("holder of view:audit may subscribe", func(t *testing.T) {
		var sawAction, sawResource, sawScopeType string
		var sawScopeID, sawUserID uuid.UUID

		c := &Client{
			id:     "test",
			logger: testLogger(),
			userID: userID,
			checkPermission: func(_ context.Context, uid uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
				sawUserID, sawAction, sawResource, sawScopeType, sawScopeID = uid, action, resource, scopeType, scopeID
				return true, nil
			},
		}

		if !c.canSubscribe(SystemAuditChannel) {
			t.Fatal("expected a view:audit holder to be allowed")
		}
		if sawUserID != userID {
			t.Errorf("checker got user %v, want %v", sawUserID, userID)
		}
		if sawAction != "view" || sawResource != "audit" {
			t.Errorf("checked %s:%s, want view:audit", sawAction, sawResource)
		}
		if sawScopeType != "global" || sawScopeID != uuid.Nil {
			t.Errorf("checked scope %s/%v, want global/%v", sawScopeType, sawScopeID, uuid.Nil)
		}
	})

	t.Run("user without view:audit is denied", func(t *testing.T) {
		c := &Client{
			id:     "test",
			logger: testLogger(),
			userID: userID,
			checkPermission: func(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID) (bool, error) {
				return false, nil
			},
		}
		if c.canSubscribe(SystemAuditChannel) {
			t.Error("a user without view:audit must not receive the audit stream")
		}
	})

	t.Run("rbac error fails closed", func(t *testing.T) {
		c := &Client{
			id:     "test",
			logger: testLogger(),
			userID: userID,
			checkPermission: func(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID) (bool, error) {
				return false, errors.New("engine down")
			},
		}
		if c.canSubscribe(SystemAuditChannel) {
			t.Error("a transient RBAC failure must not open the audit stream")
		}
	})

	t.Run("nil checker fails closed", func(t *testing.T) {
		c := &Client{id: "test", logger: testLogger(), userID: userID}
		if c.canSubscribe(SystemAuditChannel) {
			t.Error("a missing permission checker must not open the audit stream")
		}
	})
}

// TestSystemAuditChannelRoundTrip pins the client↔Redis channel mapping for
// the audit room. The Redis identifier uses a hyphen because
// RedisChannelToClient splits on the first colon after the kind — a colon
// there would parse as a cluster id.
func TestSystemAuditChannelRoundTrip(t *testing.T) {
	if !ValidateChannel(SystemAuditChannel) {
		t.Fatalf("%s must be a valid client channel", SystemAuditChannel)
	}

	redisCh, err := ClientChannelToRedis(SystemAuditChannel)
	if err != nil {
		t.Fatalf("ClientChannelToRedis: %v", err)
	}
	if redisCh != "nexara:events:system-audit" {
		t.Errorf("redis channel = %q, want nexara:events:system-audit", redisCh)
	}

	back, err := RedisChannelToClient(redisCh)
	if err != nil {
		t.Fatalf("RedisChannelToClient: %v", err)
	}
	if back != SystemAuditChannel {
		t.Errorf("round trip = %q, want %q", back, SystemAuditChannel)
	}

	// The operational room must remain distinct and still map cleanly.
	if got, _ := ClientChannelToRedis("system:events"); got != "nexara:events:system" {
		t.Errorf("system:events maps to %q, want nexara:events:system", got)
	}
}

// TestClientCanSubscribe_ClusterAuditRequiresViewAudit closes the larger half
// of the audit-stream gap: cluster-scoped audit entries outnumber non-cluster
// ones roughly 150 call sites to 58, and they used to ride
// cluster:<uuid>:events, gated only on view:cluster. A role with view:cluster
// but not view:audit therefore streamed token rotations, SSH credential
// updates, node shutdowns and firewall changes live.
//
// The room now mirrors AuditHandler.List, which filters rows through
// accessibleClusters(c, "view", "audit").
func TestClientCanSubscribe_ClusterAuditRequiresViewAudit(t *testing.T) {
	clusterUUID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
	auditChannel := "cluster:550e8400-e29b-41d4-a716-446655440000:audit"
	eventsChannel := "cluster:550e8400-e29b-41d4-a716-446655440000:events"

	t.Run("audit room checks view:audit on that cluster", func(t *testing.T) {
		var sawAction, sawResource, sawScopeType string
		var sawScopeID uuid.UUID

		c := &Client{
			id:     "test",
			logger: testLogger(),
			userID: uuid.New(),
			checkPermission: func(_ context.Context, _ uuid.UUID, action, resource, scopeType string, scopeID uuid.UUID) (bool, error) {
				sawAction, sawResource, sawScopeType, sawScopeID = action, resource, scopeType, scopeID
				return true, nil
			},
		}

		if !c.canSubscribe(auditChannel) {
			t.Fatal("expected a view:audit holder to be allowed")
		}
		if sawAction != "view" || sawResource != "audit" {
			t.Errorf("checked %s:%s, want view:audit", sawAction, sawResource)
		}
		if sawScopeType != "cluster" || sawScopeID != clusterUUID {
			t.Errorf("checked scope %s/%v, want cluster/%v", sawScopeType, sawScopeID, clusterUUID)
		}
	})

	t.Run("events room still checks view:cluster", func(t *testing.T) {
		var sawResource string
		c := &Client{
			id:     "test",
			logger: testLogger(),
			userID: uuid.New(),
			checkPermission: func(_ context.Context, _ uuid.UUID, _, resource, _ string, _ uuid.UUID) (bool, error) {
				sawResource = resource
				return true, nil
			},
		}
		if !c.canSubscribe(eventsChannel) {
			t.Fatal("expected a view:cluster holder to be allowed")
		}
		if sawResource != "cluster" {
			t.Errorf("events room checked resource %q, want cluster", sawResource)
		}
	})

	t.Run("view:cluster alone does not open the audit room", func(t *testing.T) {
		c := &Client{
			id:     "test",
			logger: testLogger(),
			userID: uuid.New(),
			// Grants view:cluster but not view:audit.
			checkPermission: func(_ context.Context, _ uuid.UUID, _, resource, _ string, _ uuid.UUID) (bool, error) {
				return resource == "cluster", nil
			},
		}
		if c.canSubscribe(auditChannel) {
			t.Error("view:cluster must not grant the cluster audit stream")
		}
		if !c.canSubscribe(eventsChannel) {
			t.Error("view:cluster should still grant the cluster events stream")
		}
	})
}

// TestClientCanSubscribe_UnclassifiedSystemChannelDenied pins the allow-list.
// The non-cluster arm used to default to allow, which is how the system room
// shipped ungated; a channel added to channelPattern without being classified
// must now be denied rather than silently world-readable.
func TestClientCanSubscribe_UnclassifiedSystemChannelDenied(t *testing.T) {
	c := &Client{
		id:     "test",
		logger: testLogger(),
		userID: uuid.New(),
		checkPermission: func(_ context.Context, _ uuid.UUID, _, _, _ string, _ uuid.UUID) (bool, error) {
			return true, nil
		},
	}
	if c.canSubscribe("system:something-new") {
		t.Error("an unclassified non-cluster channel must be denied, not allowed by default")
	}
}
