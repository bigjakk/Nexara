package config

import (
	"encoding/hex"
	"fmt"
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

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.JWTSecret == "" || c.JWTSecret == "change-this-to-a-secure-random-string" {
		return fmt.Errorf("JWT_SECRET must be set to a secure random value (see .env.example)")
	}
	if len(c.JWTSecret) < 16 {
		return fmt.Errorf("JWT_SECRET must be at least 16 characters")
	}
	if c.EncryptionKey == "" || c.EncryptionKey == "change-this-to-a-32-byte-hex-key" {
		return fmt.Errorf("ENCRYPTION_KEY must be set to a 64-character hex string (32 bytes). Generate with: openssl rand -hex 32")
	}
	if _, err := hex.DecodeString(c.EncryptionKey); err != nil || len(c.EncryptionKey) != 64 {
		return fmt.Errorf("ENCRYPTION_KEY must be exactly 64 hex characters (32 bytes). Generate with: openssl rand -hex 32")
	}
	return nil
}
