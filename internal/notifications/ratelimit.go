package notifications

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// ChannelRateLimitBurst is how many notifications a single channel may emit
// in quick succession before being throttled. Sized to absorb a normal alert
// + immediate-resolve pair plus a few escalations without rate-limiting in
// normal operation, while still bounding flapping rules.
const ChannelRateLimitBurst = 10

// ChannelRateLimitRefillSeconds is the steady-state refill rate per token,
// in seconds. With burst=10 and refill=15s, a flapping rule cannot sustain
// more than 4 notifications/min once the burst is exhausted.
const ChannelRateLimitRefillSeconds = 15

// channelBucket is a per-channel token bucket. Tokens replenish lazily on
// each Allow() call so we don't need a background goroutine to drain timers.
type channelBucket struct {
	tokens     float64
	lastRefill time.Time
}

// channelRateLimiter is a token-bucket rate limiter keyed by channel UUID.
// Buckets are created on first use and never evicted — the table size is
// bounded by the number of configured notification channels (small in
// practice, max ~hundreds).
type channelRateLimiter struct {
	mu              sync.Mutex
	buckets         map[uuid.UUID]*channelBucket
	burst           float64
	refillPerSecond float64
	now             func() time.Time
}

// newChannelRateLimiter builds a limiter with the package defaults.
// Exposed so tests can override the clock and refill rate.
func newChannelRateLimiter() *channelRateLimiter {
	return &channelRateLimiter{
		buckets:         make(map[uuid.UUID]*channelBucket),
		burst:           float64(ChannelRateLimitBurst),
		refillPerSecond: 1.0 / float64(ChannelRateLimitRefillSeconds),
		now:             time.Now,
	}
}

// Allow attempts to consume one token for the given channel. Returns true
// on success and false if the channel is rate-limited.
func (l *channelRateLimiter) Allow(channelID uuid.UUID) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bucket, ok := l.buckets[channelID]
	if !ok {
		bucket = &channelBucket{tokens: l.burst, lastRefill: now}
		l.buckets[channelID] = bucket
	}

	elapsed := now.Sub(bucket.lastRefill).Seconds()
	if elapsed > 0 {
		bucket.tokens += elapsed * l.refillPerSecond
		if bucket.tokens > l.burst {
			bucket.tokens = l.burst
		}
		bucket.lastRefill = now
	}

	if bucket.tokens >= 1 {
		bucket.tokens--
		return true
	}
	return false
}
