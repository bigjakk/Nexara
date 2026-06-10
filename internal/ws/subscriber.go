package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisSubscriber bridges Redis pub/sub to the Hub.
type RedisSubscriber struct {
	client *redis.Client
	hub    *Hub
	logger *slog.Logger
}

// NewRedisSubscriber creates a new RedisSubscriber.
func NewRedisSubscriber(client *redis.Client, hub *Hub, logger *slog.Logger) *RedisSubscriber {
	return &RedisSubscriber{
		client: client,
		hub:    hub,
		logger: logger,
	}
}

// Run starts the Redis pattern subscription. It blocks until ctx is
// cancelled. If the pub/sub channel closes (Redis restart, a network drop
// beyond go-redis's internal retries), the subscription is re-established
// with backoff — without this, a single closure silently blacked out all
// realtime events and metrics until process restart.
func (s *RedisSubscriber) Run(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		started := time.Now()
		if err := s.runOnce(ctx); err == nil {
			return // clean ctx shutdown
		}
		if time.Since(started) > time.Minute {
			// The previous subscription ran fine for a while — treat this
			// closure as fresh rather than continuing to escalate.
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		s.logger.Warn("Redis subscriber reconnecting", "backoff", backoff.String())
		backoff = min(backoff*2, maxBackoff)
	}
}

// runOnce subscribes and consumes until ctx is cancelled (returns nil) or
// the pub/sub channel closes (returns an error so Run reconnects).
func (s *RedisSubscriber) runOnce(ctx context.Context) error {
	patterns := []string{"nexara:metrics:*", "nexara:alerts:*", "nexara:events:*"}
	pubsub := s.client.PSubscribe(ctx, patterns...)
	defer pubsub.Close()

	ch := pubsub.Channel()
	s.logger.Info("Redis subscriber started", "patterns", patterns)

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Redis subscriber stopping")
			return nil
		case msg, ok := <-ch:
			if !ok {
				s.logger.Warn("Redis pub/sub channel closed")
				return errors.New("pub/sub channel closed")
			}
			s.handleMessage(msg)
		}
	}
}

func (s *RedisSubscriber) handleMessage(msg *redis.Message) {
	room, err := RedisChannelToClient(msg.Channel)
	if err != nil {
		s.logger.Warn("ignoring unknown Redis channel", "channel", msg.Channel, "error", err)
		return
	}

	// Validate that the payload is valid JSON before broadcasting.
	if !json.Valid([]byte(msg.Payload)) {
		s.logger.Warn("ignoring invalid JSON from Redis", "channel", msg.Channel)
		return
	}

	s.hub.Broadcast(room, json.RawMessage(msg.Payload))
}
