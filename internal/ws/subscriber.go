package ws

import (
	"context"
	"encoding/json"
	"log/slog"

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

// Run starts the Redis pattern subscription. It blocks until ctx is cancelled.
func (s *RedisSubscriber) Run(ctx context.Context) {
	pubsub := s.client.PSubscribe(ctx, "proxdash:metrics:*", "proxdash:alerts:*")
	defer pubsub.Close()

	ch := pubsub.Channel()
	s.logger.Info("Redis subscriber started", "patterns", []string{"proxdash:metrics:*", "proxdash:alerts:*"})

	for {
		select {
		case <-ctx.Done():
			s.logger.Info("Redis subscriber stopping")
			return
		case msg, ok := <-ch:
			if !ok {
				s.logger.Warn("Redis pub/sub channel closed")
				return
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
