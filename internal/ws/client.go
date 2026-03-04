package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gofiber/contrib/websocket"
)

const (
	// Maximum message size allowed from peer.
	maxMessageSize = 4096

	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second
)

// Client represents a single WebSocket connection.
type Client struct {
	id        string
	conn      *websocket.Conn
	hub       *Hub
	send      chan []byte
	closeOnce sync.Once
	logger    *slog.Logger

	pingInterval time.Duration
	pongTimeout  time.Duration
}

// closeSend closes the send channel exactly once.
func (c *Client) closeSend() {
	c.closeOnce.Do(func() {
		close(c.send)
	})
}

// NewClient creates a new WebSocket client.
func NewClient(id string, conn *websocket.Conn, hub *Hub, logger *slog.Logger, pingInterval, pongTimeout time.Duration) *Client {
	return &Client{
		id:           id,
		conn:         conn,
		hub:          hub,
		send:         make(chan []byte, clientSendBuffer),
		logger:       logger,
		pingInterval: pingInterval,
		pongTimeout:  pongTimeout,
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

// trySend performs a non-blocking send to the client's send channel.
func (c *Client) trySend(data []byte) {
	select {
	case c.send <- data:
	default:
		// Buffer full — message dropped. The hub will evict if needed.
	}
}
