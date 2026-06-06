package config

import (
	"os"
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("JWT_SECRET", "test-jwt-secret-at-least-16-chars")
	t.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
}

func TestLoad_Defaults(t *testing.T) {
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.APIPort != 8080 {
		t.Errorf("APIPort = %d, want 8080", cfg.APIPort)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.RedisURL != "redis://localhost:6379" {
		t.Errorf("RedisURL = %q, want %q", cfg.RedisURL, "redis://localhost:6379")
	}
	if cfg.RateLimitMax != 600 {
		t.Errorf("RateLimitMax = %d, want 600", cfg.RateLimitMax)
	}
	if cfg.RateLimitExpiration != time.Minute {
		t.Errorf("RateLimitExpiration = %v, want 1m", cfg.RateLimitExpiration)
	}
	if cfg.CORSAllowOrigins != "" {
		t.Errorf("CORSAllowOrigins = %q, want empty (operator must opt in to a public origin)", cfg.CORSAllowOrigins)
	}
	if cfg.AccessTokenTTL != 15*time.Minute {
		t.Errorf("AccessTokenTTL = %v, want 15m", cfg.AccessTokenTTL)
	}
	if cfg.RefreshTokenTTL != 168*time.Hour {
		t.Errorf("RefreshTokenTTL = %v, want 168h", cfg.RefreshTokenTTL)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want empty (XFF must be untrusted by default)", cfg.TrustedProxies)
	}
	if cfg.ProxyHeader != "X-Forwarded-For" {
		t.Errorf("ProxyHeader = %q, want %q", cfg.ProxyHeader, "X-Forwarded-For")
	}
}

func TestLoad_CustomValues(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("API_PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("RATE_LIMIT_MAX", "200")
	t.Setenv("RATE_LIMIT_EXPIRATION", "5m")
	t.Setenv("CORS_ALLOW_ORIGINS", "http://localhost:3000,http://localhost:5173")
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1,10.0.0.0/8")
	t.Setenv("PROXY_HEADER", "X-Real-IP")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.APIPort != 9090 {
		t.Errorf("APIPort = %d, want 9090", cfg.APIPort)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.RateLimitMax != 200 {
		t.Errorf("RateLimitMax = %d, want 200", cfg.RateLimitMax)
	}
	if cfg.RateLimitExpiration != 5*time.Minute {
		t.Errorf("RateLimitExpiration = %v, want 5m", cfg.RateLimitExpiration)
	}
	if cfg.CORSAllowOrigins != "http://localhost:3000,http://localhost:5173" {
		t.Errorf("CORSAllowOrigins = %q", cfg.CORSAllowOrigins)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0] != "127.0.0.1" || cfg.TrustedProxies[1] != "10.0.0.0/8" {
		t.Errorf("TrustedProxies = %v, want [127.0.0.1, 10.0.0.0/8]", cfg.TrustedProxies)
	}
	if cfg.ProxyHeader != "X-Real-IP" {
		t.Errorf("ProxyHeader = %q, want %q", cfg.ProxyHeader, "X-Real-IP")
	}
}

func TestLoad_TaskHistoryRetention(t *testing.T) {
	t.Run("defaults to 24h", func(t *testing.T) {
		setRequiredEnv(t)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		if cfg.TaskHistoryRetention != 24*time.Hour {
			t.Errorf("TaskHistoryRetention = %v, want 24h", cfg.TaskHistoryRetention)
		}
	})

	t.Run("honors a positive override", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("TASK_HISTORY_RETENTION", "168h")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() error: %v", err)
		}
		if cfg.TaskHistoryRetention != 168*time.Hour {
			t.Errorf("TaskHistoryRetention = %v, want 168h", cfg.TaskHistoryRetention)
		}
	})

	// Zero or negative would make the sweep delete all finished task history each
	// tick; validate() must clamp it back to the safe default.
	for _, v := range []string{"0", "0s", "-1h"} {
		t.Run("clamps non-positive "+v, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("TASK_HISTORY_RETENTION", v)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error: %v", err)
			}
			if cfg.TaskHistoryRetention != 24*time.Hour {
				t.Errorf("TaskHistoryRetention = %v, want clamp to 24h", cfg.TaskHistoryRetention)
			}
		})
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	// Only set some required vars, omit DATABASE_URL.
	t.Setenv("JWT_SECRET", "test")
	t.Setenv("ENCRYPTION_KEY", "test")
	os.Unsetenv("DATABASE_URL")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when DATABASE_URL is missing")
	}
}

func TestLoad_MissingJWTSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("ENCRYPTION_KEY", "test")
	os.Unsetenv("JWT_SECRET")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when JWT_SECRET is missing")
	}
}

func TestLoad_MissingEncryptionKey(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("JWT_SECRET", "test")
	os.Unsetenv("ENCRYPTION_KEY")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should fail when ENCRYPTION_KEY is missing")
	}
}
