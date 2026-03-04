package ws

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Client → Server message types.
const (
	MsgTypeSubscribe   = "subscribe"
	MsgTypeUnsubscribe = "unsubscribe"
	MsgTypePing        = "ping"
)

// Server → Client message types.
const (
	MsgTypeWelcome    = "welcome"
	MsgTypeSubscribed = "subscribed"
	MsgTypeData       = "data"
	MsgTypeError      = "error"
	MsgTypePong       = "pong"
)

// channelPattern validates client-facing channel names like "cluster:<uuid>:metrics".
var channelPattern = regexp.MustCompile(
	`^cluster:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}:(metrics|alerts)$`,
)

// IncomingMessage is a message sent from a WebSocket client.
type IncomingMessage struct {
	Type     string   `json:"type"`
	Channels []string `json:"channels,omitempty"`
}

// OutgoingMessage is a message sent from the server to a WebSocket client.
type OutgoingMessage struct {
	Type    string          `json:"type"`
	Channel string          `json:"channel,omitempty"`
	Message string          `json:"message,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ValidateChannel checks whether a client-facing channel name is valid.
func ValidateChannel(ch string) bool {
	return channelPattern.MatchString(ch)
}

// ClientChannelToRedis converts a client-facing channel name to the corresponding Redis pub/sub channel.
// Example: "cluster:<uuid>:metrics" → "proxdash:metrics:<uuid>"
func ClientChannelToRedis(ch string) (string, error) {
	if !ValidateChannel(ch) {
		return "", fmt.Errorf("invalid channel format: %s", ch)
	}
	parts := strings.SplitN(ch, ":", 3)
	// parts = ["cluster", "<uuid>", "metrics|alerts"]
	return fmt.Sprintf("proxdash:%s:%s", parts[2], parts[1]), nil
}

// RedisChannelToClient converts a Redis pub/sub channel name to the client-facing channel name.
// Example: "proxdash:metrics:<uuid>" → "cluster:<uuid>:metrics"
func RedisChannelToClient(ch string) (string, error) {
	// Strip "proxdash:" prefix.
	if !strings.HasPrefix(ch, "proxdash:") {
		return "", fmt.Errorf("unknown Redis channel: %s", ch)
	}
	rest := strings.TrimPrefix(ch, "proxdash:")
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("malformed Redis channel: %s", ch)
	}
	kind := parts[0]   // "metrics" or "alerts"
	clusterUUID := parts[1]
	if kind != "metrics" && kind != "alerts" {
		return "", fmt.Errorf("unknown channel kind: %s", kind)
	}
	return fmt.Sprintf("cluster:%s:%s", clusterUUID, kind), nil
}

// newWelcomeMsg creates a welcome message.
func newWelcomeMsg() []byte {
	b, _ := json.Marshal(OutgoingMessage{Type: MsgTypeWelcome, Message: "connected"})
	return b
}

// newSubscribedMsg creates a subscribed confirmation message.
func newSubscribedMsg(channel string) []byte {
	b, _ := json.Marshal(OutgoingMessage{Type: MsgTypeSubscribed, Channel: channel})
	return b
}

// newDataMsg creates a data message with a payload.
func newDataMsg(channel string, payload json.RawMessage) []byte {
	b, _ := json.Marshal(OutgoingMessage{Type: MsgTypeData, Channel: channel, Payload: payload})
	return b
}

// newErrorMsg creates an error message.
func newErrorMsg(msg string) []byte {
	b, _ := json.Marshal(OutgoingMessage{Type: MsgTypeError, Message: msg})
	return b
}

// newPongMsg creates a pong message.
func newPongMsg() []byte {
	b, _ := json.Marshal(OutgoingMessage{Type: MsgTypePong})
	return b
}
