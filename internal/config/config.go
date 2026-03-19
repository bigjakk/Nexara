package config

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Config holds all configuration for the application.
type Config struct {
	APIPort                int           `envconfig:"API_PORT" default:"8080"`
	LogLevel               string        `envconfig:"LOG_LEVEL" default:"info"`
	DatabaseURL            string        `envconfig:"DATABASE_URL" required:"true"`
	RedisURL               string        `envconfig:"REDIS_URL" default:"redis://localhost:6379"`
	JWTSecret              string        `envconfig:"JWT_SECRET"`
	EncryptionKey          string        `envconfig:"ENCRYPTION_KEY"`
	AccessTokenTTL         time.Duration `envconfig:"ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTokenTTL        time.Duration `envconfig:"REFRESH_TOKEN_TTL" default:"168h"`
	RateLimitMax           int           `envconfig:"RATE_LIMIT_MAX" default:"600"`
	RateLimitExpiration    time.Duration `envconfig:"RATE_LIMIT_EXPIRATION" default:"1m"`
	CORSAllowOrigins       string        `envconfig:"CORS_ALLOW_ORIGINS" default:"http://localhost:3001"`
	MetricsCollectInterval time.Duration `envconfig:"METRICS_COLLECT_INTERVAL" default:"30s"`
	WSPort                 int           `envconfig:"WS_PORT" default:"8081"`
	WSPingInterval         time.Duration `envconfig:"WS_PING_INTERVAL" default:"25s"`
	WSPongTimeout          time.Duration `envconfig:"WS_PONG_TIMEOUT" default:"30s"`
	DataDir                string        `envconfig:"DATA_DIR" default:"/data/nexara"`
	MaxUploadSize          int64         `envconfig:"MAX_UPLOAD_SIZE" default:"16106127360"`
	WSMaxConnections       int           `envconfig:"WS_MAX_CONNECTIONS" default:"1000"`
	PprofEnabled           bool          `envconfig:"PPROF_ENABLED" default:"false"`
	PprofPort              string        `envconfig:"PPROF_PORT" default:"6060"`
}

// NewMetricsTicker creates a time.Ticker using the configured metrics collection interval.
func (c *Config) NewMetricsTicker() *time.Ticker {
	return time.NewTicker(c.MetricsCollectInterval)
}

// SlogLevel parses the configured LOG_LEVEL string into a slog.Level.
// Supported values: debug, info, warn, error. Defaults to info.
func (c *Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Load reads configuration from environment variables, auto-generates
// any missing secrets, and validates the result.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}
	if err := cfg.ResolveSecrets(slog.Default()); err != nil {
		return nil, fmt.Errorf("resolve secrets: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.JWTSecret == "" {
		return fmt.Errorf("JWT_SECRET is empty (should have been resolved by ResolveSecrets)")
	}
	if len(c.JWTSecret) < 16 {
		return fmt.Errorf("JWT_SECRET must be at least 16 characters")
	}
	if c.CORSAllowOrigins == "*" {
		slog.Warn("CORS_ALLOW_ORIGINS is set to wildcard '*' — this is insecure for production deployments")
	}
	if c.EncryptionKey == "" {
		return fmt.Errorf("ENCRYPTION_KEY is empty (should have been resolved by ResolveSecrets)")
	}
	if _, err := hex.DecodeString(c.EncryptionKey); err != nil || len(c.EncryptionKey) != 64 {
		return fmt.Errorf("ENCRYPTION_KEY must be exactly 64 hex characters (32 bytes). Generate with: openssl rand -hex 32")
	}
	return nil
}
