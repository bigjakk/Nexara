package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
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

	// closeMu guards closeCode and closeReason: the Close writePump sends when it
	// is told to stop, set by closeWith. Zero sends a bare Close.
	closeMu     sync.Mutex
	closeCode   int
	closeReason string

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

	// subMu guards subs: the channels this client is subscribed to, each with
	// the generation it was subscribed at, which recheckSubscriptions re-checks.
	// It is held across each hub Subscribe/Unsubscribe send, so the hub applies
	// them in the order the records changed.
	subMu  sync.Mutex
	subs   map[string]uint64
	subGen uint64
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

// closeWith tells the writer to close the socket with code and reason — the
// credential watcher's end (Server.handleWS). Idempotent, as closeSend is.
func (c *Client) closeWith(code int, reason string) {
	c.closeMu.Lock()
	if c.closeCode == 0 {
		c.closeCode, c.closeReason = code, reason
	}
	c.closeMu.Unlock()
	c.closeSend()
}

// closeFrame is the payload of the Close writePump sends when it is told to stop.
func (c *Client) closeFrame() []byte {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closeCode == 0 {
		return nil
	}
	return websocket.FormatCloseMessage(c.closeCode, c.closeReason)
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
		subs:            make(map[string]uint64),
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
			_ = c.conn.WriteControl(websocket.CloseMessage, c.closeFrame(), time.Now().Add(writeWait))
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
			c.subMu.Lock()
			c.subGen++
			c.subs[ch] = c.subGen
			c.hub.Subscribe(c, ch)
			c.subMu.Unlock()
		}

	case MsgTypeUnsubscribe:
		for _, ch := range msg.Channels {
			c.subMu.Lock()
			delete(c.subs, ch)
			c.hub.Unsubscribe(c, ch)
			c.subMu.Unlock()
		}

	default:
		c.trySend(newErrorMsg("unknown message type"))
	}
}

// channelGrant is the permission a subscription to a channel needs.
type channelGrant struct {
	action, resource, scopeType string
	scopeID                     uuid.UUID
}

// grantFor says what a subscription to channel needs (security review H1): a
// grant, nothing (open), or refusal outright (ok false).
//
// Cluster-scoped channels (`cluster:<uuid>:metrics|alerts|events`) require
// `view:cluster` on that specific cluster. Without this gate, any
// authenticated user could subscribe to any cluster whose UUID they can guess
// and stream live metrics, events and alerts — a cross-tenant disclosure.
// cluster:<uuid>:audit carries the cluster's audit entries, so it takes
// view:audit on that cluster — mirroring AuditHandler.List, which filters
// rows through accessibleClusters(c, "view", "audit").
//
// `system:audit` requires global `view:audit`: it carries the audit entries for
// non-cluster administrative actions, naming the user whose password changed,
// the setting that was written, the role that was granted.
//
// `system:events` stays open to any authenticated session. It carries only
// operational events with no such subject — task updates, report completion,
// PBS changes — and gating it would cut the task feed for roles that
// legitimately cannot read the audit log.
//
// Non-cluster channels are an allow-list, never a default-allow: a channel added
// to channelPattern later must be classified here deliberately. Defaulting to
// allow is exactly how system:events came to be ungated in the first place.
func grantFor(channel string) (g channelGrant, open, ok bool) {
	clusterID, isCluster := ChannelClusterID(channel)
	if !isCluster {
		switch channel {
		case SystemEventsChannel:
			return channelGrant{}, true, true
		case SystemAuditChannel:
			return channelGrant{"view", "audit", "global", uuid.Nil}, false, true
		}
		return channelGrant{}, false, false
	}
	clusterUUID, err := uuid.Parse(clusterID)
	if err != nil {
		// Should be caught by ValidateChannel already, but be defensive.
		return channelGrant{}, false, false
	}
	resource := "cluster"
	if ChannelKind(channel) == AuditChannelKind {
		resource = "audit"
	}
	return channelGrant{"view", resource, "cluster", clusterUUID}, false, true
}

// canSubscribe enforces the subscribe-time RBAC check (grantFor).
//
// If the RBAC engine is nil (test fixtures only — production main.go always
// wires it), gated subscribes fail CLOSED. This is the 5.1-aligned behaviour:
// there is no synthetic-admin fallback in either the HTTP or WS path. A
// server with a missing engine in production is a misconfiguration that must
// surface loudly. An engine error fails closed too: a transient RBAC failure
// must not open a stream.
func (c *Client) canSubscribe(channel string) bool {
	g, open, ok := grantFor(channel)
	switch {
	case !ok:
		c.logger.Warn("ws subscribe: unclassified or invalid channel, denying",
			"client", c.id,
			"channel", channel,
		)
		return false
	case open:
		return true
	case c.checkPermission == nil:
		c.logger.Warn("ws subscribe: no permission checker, denying channel",
			"client", c.id,
			"channel", channel,
		)
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), subscribeAuthTimeout)
	defer cancel()

	allowed, err := c.checkPermission(ctx, c.userID, g.action, g.resource, g.scopeType, g.scopeID)
	if err != nil {
		c.logger.Warn("ws subscribe: rbac check errored",
			"client", c.id,
			"user_id", c.userID,
			"channel", channel,
			"error", err,
		)
		return false
	}
	if !allowed {
		c.logger.Info("ws subscribe: forbidden",
			"client", c.id,
			"user_id", c.userID,
			"channel", channel,
			"required", g.action+":"+g.resource,
		)
	}
	return allowed
}

// recheckSubscriptions asks again, for every channel this client is subscribed
// to, whether it may still be, and drops each it may not, telling the client
// with a "forbidden" error naming the channel. The hub's credential watcher calls
// it on each tick (Server.handleWS), so a revoked grant stops the stream within
// a tick of RBAC seeing it. A lookup that fails drops nothing: it is returned,
// and the watcher counts it as it counts a failed liveness check. It answers
// true when it could look, as the watcher's permission check.
func (c *Client) recheckSubscriptions(ctx context.Context) (bool, error) {
	c.subMu.Lock()
	snapshot := make(map[string]uint64, len(c.subs))
	for ch, gen := range c.subs {
		snapshot[ch] = gen
	}
	c.subMu.Unlock()

	answers := make(map[channelGrant]bool) // channels that need the same grant ask once
	var drop []string
	for ch := range snapshot {
		g, open, ok := grantFor(ch)
		if open {
			continue
		}
		allowed, asked := answers[g]
		if ok && !asked {
			if c.checkPermission == nil {
				return false, errors.New("no permission checker configured")
			}
			var err error
			if allowed, err = c.checkPermission(ctx, c.userID, g.action, g.resource, g.scopeType, g.scopeID); err != nil {
				return false, fmt.Errorf("re-checking subscription %s: %w", ch, err)
			}
			answers[g] = allowed
		}
		if !ok || !allowed {
			drop = append(drop, ch)
		}
	}

	c.subMu.Lock()
	defer c.subMu.Unlock()
	for _, ch := range drop {
		// A channel subscribed again since the snapshot passed a newer check.
		if c.subs[ch] != snapshot[ch] {
			continue
		}
		delete(c.subs, ch)
		c.hub.Unsubscribe(c, ch)
		c.trySend(newForbiddenChannelMsg(ch))
		c.logger.Info("ws: dropped a subscription its user may no longer hold",
			"client", c.id, "user_id", c.userID, "channel", ch)
	}
	return true, nil
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
