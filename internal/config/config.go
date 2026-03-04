package config

import (
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all configuration for the application.
type Config struct {
	APIPort                int           `envconfig:"API_PORT" default:"8080"`
	LogLevel               string        `envconfig:"LOG_LEVEL" default:"info"`
	DatabaseURL            string        `envconfig:"DATABASE_URL" required:"true"`
	RedisURL               string        `envconfig:"REDIS_URL" default:"redis://localhost:6379"`
	JWTSecret              string        `envconfig:"JWT_SECRET" required:"true"`
	EncryptionKey          string        `envconfig:"ENCRYPTION_KEY" required:"true"`
	AccessTokenTTL         time.Duration `envconfig:"ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTokenTTL        time.Duration `envconfig:"REFRESH_TOKEN_TTL" default:"168h"`
	RateLimitMax           int           `envconfig:"RATE_LIMIT_MAX" default:"100"`
	RateLimitExpiration    time.Duration `envconfig:"RATE_LIMIT_EXPIRATION" default:"1m"`
	CORSAllowOrigins       string        `envconfig:"CORS_ALLOW_ORIGINS" default:"*"`
	MetricsCollectInterval time.Duration `envconfig:"METRICS_COLLECT_INTERVAL" default:"30s"`
	WSPort                 int           `envconfig:"WS_PORT" default:"8081"`
	WSPingInterval         time.Duration `envconfig:"WS_PING_INTERVAL" default:"25s"`
	WSPongTimeout          time.Duration `envconfig:"WS_PONG_TIMEOUT" default:"30s"`
}

// NewMetricsTicker creates a time.Ticker using the configured metrics collection interval.
func (c *Config) NewMetricsTicker() *time.Ticker {
	return time.NewTicker(c.MetricsCollectInterval)
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
