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
	APIPort             int           `envconfig:"API_PORT" default:"8080"`
	LogLevel            string        `envconfig:"LOG_LEVEL" default:"info"`
	DatabaseURL         string        `envconfig:"DATABASE_URL" required:"true"`
	RedisURL            string        `envconfig:"REDIS_URL" default:"redis://localhost:6379"`
	JWTSecret           string        `envconfig:"JWT_SECRET"`
	EncryptionKey       string        `envconfig:"ENCRYPTION_KEY"`
	AccessTokenTTL      time.Duration `envconfig:"ACCESS_TOKEN_TTL" default:"15m"`
	RefreshTokenTTL     time.Duration `envconfig:"REFRESH_TOKEN_TTL" default:"168h"`
	RateLimitMax        int           `envconfig:"RATE_LIMIT_MAX" default:"600"`
	RateLimitExpiration time.Duration `envconfig:"RATE_LIMIT_EXPIRATION" default:"1m"`
	// TrustedProxies is a comma-separated list of IPs/CIDRs whose
	// X-Forwarded-For header may be trusted. Leave empty in deployments
	// where Nexara is exposed directly; set to the reverse-proxy network
	// (e.g. "127.0.0.1,10.0.0.0/8") when fronted by nginx/Traefik so the
	// auth/general rate limiters key on the real client IP rather than the
	// proxy. Untrusted sources have their XFF header ignored — a spoofed
	// header will not pivot the limiter to a different bucket.
	TrustedProxies []string `envconfig:"TRUSTED_PROXIES"`
	// ProxyHeader is the request header consulted for the client IP when
	// the remote address is on the TrustedProxies list. Override only if
	// the upstream proxy uses a non-standard header.
	ProxyHeader            string        `envconfig:"PROXY_HEADER" default:"X-Forwarded-For"`
	// CORSAllowOrigins is the comma-separated list of `Origin:` values the
	// browser API will accept (and echo back in Access-Control-Allow-Origin).
	// Empty default — cross-origin browsers (mobile apps, separately-served
	// SPAs, Docker deployments where the SPA lives on a different host)
	// MUST set CORS_ALLOW_ORIGINS explicitly to the public origin
	// (e.g. "https://nexara.example.com"). The Vite dev server at
	// http://localhost:3000 proxies /api and /ws to the backend, so dev
	// frontends are same-origin and do NOT require CORS to be set. A
	// literal "*" allows any origin; a startup warning fires for both
	// empty and "*" so the operator can see what posture they're running.
	CORSAllowOrigins       string        `envconfig:"CORS_ALLOW_ORIGINS"`
	MetricsCollectInterval time.Duration `envconfig:"METRICS_COLLECT_INTERVAL" default:"30s"`
	WSPort                 int           `envconfig:"WS_PORT" default:"8081"`
	WSPingInterval         time.Duration `envconfig:"WS_PING_INTERVAL" default:"25s"`
	WSPongTimeout          time.Duration `envconfig:"WS_PONG_TIMEOUT" default:"30s"`
	// WSAllowedOrigins is a comma-separated list of origins (scheme +
	// host + optional port, e.g. "https://nexara.example.com") that the
	// /ws, /ws/console, and /ws/vnc upgrade endpoints accept the
	// `Origin:` request header from. A literal `*` or an empty value
	// accepts all origins — appropriate for self-hosted dev/lab installs
	// but logged at startup so production operators see the gap. Match
	// is exact (no wildcard subdomain support) per
	// gofiber/contrib/websocket's Origins field.
	WSAllowedOrigins string `envconfig:"WS_ALLOWED_ORIGINS"`
	DataDir          string `envconfig:"DATA_DIR" default:"/data/nexara"`
	MaxUploadSize    int64  `envconfig:"MAX_UPLOAD_SIZE" default:"16106127360"`
	WSMaxConnections int    `envconfig:"WS_MAX_CONNECTIONS" default:"1000"`
	PprofEnabled     bool   `envconfig:"PPROF_ENABLED" default:"false"`
	PprofPort        string `envconfig:"PPROF_PORT" default:"6060"`
	ChangelogRepo    string `envconfig:"CHANGELOG_REPO" default:"bigjakk/Nexara"`
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
	switch c.CORSAllowOrigins {
	case "":
		slog.Warn("CORS_ALLOW_ORIGINS is empty — browser fetches from cross-origin SPAs will fail preflight. Set this to your SPA's public origin in production.")
	case "*":
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
