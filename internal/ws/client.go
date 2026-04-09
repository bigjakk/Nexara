package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

// PermissionChecker resolves "does this user have <action>:<resource>
// at the given scope". Injected into each Client at construction time
// so the WS package doesn't depend on a concrete RBAC engine type and
// the subscribe-time check is testable without spinning up the real
// engine. In production, NewServer wires this from
// `*auth.RBACEngine.HasPermission`. In tests, pass a closure.
type PermissionChecker func(
	ctx context.Context,
	userID uuid.UUID,
	action, resource, scopeType string,
	scopeID uuid.UUID,
) (bool, error)

const (
	// Maximum message size allowed from peer.
	maxMessageSize = 4096

	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Maximum time spent on a single subscribe-time RBAC check. The
	// engine reads from Redis (cached) or falls back to Postgres; both
	// should resolve in single-digit ms in normal operation.
	subscribeAuthTimeout = 3 * time.Second
)

// Client represents a single WebSocket connection.
type Client struct {
	id        string
	conn      *websocket.Conn
	hub       *Hub
	send      chan []byte
	done      chan struct{} // closed when writePump exits
	closeOnce sync.Once
	logger    *slog.Logger

	pingInterval time.Duration
	pongTimeout  time.Duration

	// userID identifies the authenticated user behind this connection.
	// Set at construction time from the JWT validated by authMiddleware.
	// Used for the subscribe-time RBAC check.
	userID uuid.UUID

	// checkPermission resolves "can this user view this specific cluster"
	// per-subscribe. May be nil in test setups; in production it's
	// always populated by NewServer when the API server's engine is
	// available. When nil, the subscribe path falls open — same as the
	// API request handlers' `requireAdmin` fallback path.
	checkPermission PermissionChecker
}

// closeSend closes the send channel exactly once.
func (c *Client) closeSend() {
	c.closeOnce.Do(func() {
		close(c.send)
	})
}

// NewClient creates a new WebSocket client.
//
// `userID` and `checkPermission` are required for cross-tenant
// subscription safety (security review H1): without them, any
// authenticated user could subscribe to any cluster's metric / event /
// alert channel just by knowing the cluster UUID. Pass them in even
// when checkPermission is nil — the subscribe path tolerates that for
// tests but flags it loudly server-side via `slog.Warn`.
func NewClient(
	id string,
	conn *websocket.Conn,
	hub *Hub,
	logger *slog.Logger,
	pingInterval, pongTimeout time.Duration,
	userID uuid.UUID,
	checkPermission PermissionChecker,
) *Client {
	return &Client{
		id:              id,
		conn:            conn,
		hub:             hub,
		send:            make(chan []byte, clientSendBuffer),
		done:            make(chan struct{}),
		logger:          logger,
		pingInterval:    pingInterval,
		pongTimeout:     pongTimeout,
		userID:          userID,
		checkPermission: checkPermission,
	}
}

// readPump reads messages from the WebSocket and dispatches them.
func (c *Client) readPump() {
	defer func() {
		c.hub.Unregister(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	if err := c.conn.SetReadDeadline(time.Now().Add(c.pongTimeout)); err != nil {
		c.logger.Error("failed to set read deadline", "client", c.id, "error", err)
		return
	}
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(c.pongTimeout))
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				c.logger.Warn("unexpected close", "client", c.id, "error", err)
			}
			return
		}

		var msg IncomingMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			c.trySend(newErrorMsg("invalid JSON"))
			continue
		}

		c.handleMessage(msg)
	}
}

// writePump writes messages from the send channel to the WebSocket.
func (c *Client) writePump() {
	ticker := time.NewTicker(c.pingInterval)
	defer func() {
		ticker.Stop()
		c.conn.Close()
		close(c.done)
	}()

	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				// Hub closed the channel.
				_ = c.conn.WriteControl(websocket.CloseMessage, nil, time.Now().Add(writeWait))
				return
			}
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage processes a parsed incoming message.
func (c *Client) handleMessage(msg IncomingMessage) {
	switch msg.Type {
	case MsgTypePing:
		c.trySend(newPongMsg())

	case MsgTypeSubscribe:
		for _, ch := range msg.Channels {
			if !ValidateChannel(ch) {
				c.trySend(newErrorMsg("invalid channel format"))
				continue
			}
			if !c.canSubscribe(ch) {
				// Generic error message — don't reveal whether the
				// cluster exists, just that this user can't subscribe.
				// The actual reason (no view:cluster, RBAC engine
				// errored, etc.) is logged server-side only.
				c.trySend(newErrorMsg("forbidden"))
				continue
			}
			c.hub.Subscribe(c, ch)
		}

	case MsgTypeUnsubscribe:
		for _, ch := range msg.Channels {
			c.hub.Unsubscribe(c, ch)
		}

	default:
		c.trySend(newErrorMsg("unknown message type"))
	}
}

// canSubscribe enforces the subscribe-time RBAC check (security review H1).
//
// Cluster-scoped channels (`cluster:<uuid>:metrics|alerts|events`) require
// the user to have `view:cluster` permission for that specific cluster.
// Without this gate, any authenticated user could subscribe to any cluster
// they can guess the UUID for and stream live metrics, events, and alerts
// — a cross-tenant information disclosure.
//
// `system:events` is allowed for any authenticated session because it
// only carries non-cluster events (task_created, audit_entry, etc.).
//
// If the RBAC engine is nil (e.g. test fixtures, or a misconfigured
// production server), we fail OPEN and log a warning. This matches the
// behavior of `requirePerm` in `internal/api/handlers/permission.go`,
// which falls back to a legacy admin check when RBAC isn't available.
// Production deploys MUST have the engine wired (verified by an init
// check in main.go) — the open fallback only exists so test harnesses
// don't have to spin up a full RBAC stack.
func (c *Client) canSubscribe(channel string) bool {
	clusterID, isCluster := ChannelClusterID(channel)
	if !isCluster {
		// system:events — allowed for any authenticated user. Auth was
		// already enforced at the WS upgrade step.
		return true
	}

	if c.checkPermission == nil {
		c.logger.Warn("ws subscribe: no permission checker, allowing cluster channel",
			"client", c.id,
			"channel", channel,
		)
		return true
	}

	clusterUUID, err := uuid.Parse(clusterID)
	if err != nil {
		// Should be caught by ValidateChannel already, but be defensive.
		c.logger.Warn("ws subscribe: invalid cluster uuid in channel",
			"client", c.id,
			"channel", channel,
			"error", err,
		)
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), subscribeAuthTimeout)
	defer cancel()

	ok, err := c.checkPermission(
		ctx, c.userID, "view", "cluster", "cluster", clusterUUID,
	)
	if err != nil {
		// Fail closed on error — don't leak data because the RBAC
		// engine had a transient failure.
		c.logger.Warn("ws subscribe: rbac check errored",
			"client", c.id,
			"user_id", c.userID,
			"channel", channel,
			"error", err,
		)
		return false
	}
	if !ok {
		c.logger.Info("ws subscribe: forbidden",
			"client", c.id,
			"user_id", c.userID,
			"channel", channel,
			"cluster_id", clusterID,
		)
	}
	return ok
}

// trySend performs a non-blocking send to the client's send channel.
func (c *Client) trySend(data []byte) {
	select {
	case c.send <- data:
	default:
		// Buffer full — message dropped. The hub will evict if needed.
	}
}
