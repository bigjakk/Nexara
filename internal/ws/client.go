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
	closing   chan struct{} // closed by closeSend to signal the writer to exit
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
	// per-subscribe. May be nil in test setups that exercise non-RBAC
	// paths; in production NewServer always populates it from the API
	// server's RBAC engine. When nil, cluster channel subscribes fail
	// closed — there is no synthetic-admin fallback (that path was
	// removed in 5.1 along with the handlers' requireAdmin fallback).
	checkPermission PermissionChecker
}

// closeSend signals the writer to exit. Idempotent.
//
// The send channel itself is never closed — closing it would race with any
// goroutine that holds a reference to the client (e.g. handleWS sending the
// welcome message, or readPump dispatching an error response) and is
// concurrently calling trySend. Instead, closing `closing` tells writePump to
// stop and trySend to drop the message; the send buffer is GC'd with the
// Client.
func (c *Client) closeSend() {
	c.closeOnce.Do(func() {
		close(c.closing)
	})
}

// NewClient creates a new WebSocket client.
//
// `userID` and `checkPermission` are required for cross-tenant
// subscription safety (security review H1): without them, any
// authenticated user could subscribe to any cluster's metric / event /
// alert channel just by knowing the cluster UUID. Tests that don't
// exercise the subscribe gate may pass nil — cluster channels then
// fail closed (a server-side warning is logged on each attempt).
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
		closing:         make(chan struct{}),
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
		case <-c.closing:
			// Hub asked us to shut down (or the connection's readPump exited
			// and the hub unregistered us). Send a graceful close frame and
			// exit. Any buffered messages in c.send are discarded.
			_ = c.conn.WriteControl(websocket.CloseMessage, nil, time.Now().Add(writeWait))
			return

		case message := <-c.send:
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
// If the RBAC engine is nil (test fixtures only — production main.go
// always wires it), cluster channel subscribes fail CLOSED. This is the
// 5.1-aligned behaviour: there is no synthetic-admin fallback in either
// the HTTP or WS path. A server with a missing engine in production is
// a misconfiguration that must surface loudly.
func (c *Client) canSubscribe(channel string) bool {
	clusterID, isCluster := ChannelClusterID(channel)
	if !isCluster {
		// system:events — allowed for any authenticated user. Auth was
		// already enforced at the WS upgrade step.
		return true
	}

	if c.checkPermission == nil {
		c.logger.Warn("ws subscribe: no permission checker, denying cluster channel",
			"client", c.id,
			"channel", channel,
		)
		return false
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
//
// The `closing` case prevents a race against closeSend: if the client is
// shutting down, the send is dropped instead of going into a buffer no one
// will drain. Because c.send is never closed (see closeSend), there is no
// possibility of a send-on-closed-channel panic here.
func (c *Client) trySend(data []byte) {
	select {
	case <-c.closing:
		// Client is shutting down; drop the message.
	case c.send <- data:
	default:
		// Buffer full — message dropped. The hub will evict if needed.
	}
}
