package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/config"
	"github.com/bigjakk/nexara/internal/debug"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/ws"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		healthcheck()
		return
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	// Parse Redis URL.
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		logger.Error("failed to parse Redis URL", "error", err)
		os.Exit(1)
	}
	redisClient := redis.NewClient(opts)

	// Verify Redis connection.
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		logger.Error("failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	logger.Info("connected to Redis")

	// Connect to PostgreSQL for console proxy.
	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		logger.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	if err := pool.Ping(context.Background()); err != nil {
		logger.Error("failed to ping database", "error", err)
		os.Exit(1)
	}
	logger.Info("connected to database")

	// Start pprof if enabled.
	if cfg.PprofEnabled {
		debug.StartPprof(cfg.PprofPort, logger)
	}

	// Create JWT service for token validation.
	jwtSvc := auth.NewJWTService(cfg.JWTSecret, cfg.AccessTokenTTL, cfg.RefreshTokenTTL)

	// Create console and VNC handlers for terminal/graphical proxy.
	queries := db.New(pool)
	consoleHandler := ws.NewConsoleHandler(queries, cfg.EncryptionKey, jwtSvc, logger)
	vncHandler := ws.NewVNCHandler(queries, cfg.EncryptionKey, jwtSvc, logger)

	// Create and start Hub.
	hub := ws.NewHub(logger, cfg.WSMaxConnections)
	hub.Run()

	// Create and start Redis subscriber.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	subscriber := ws.NewRedisSubscriber(redisClient, hub, logger)
	go subscriber.Run(ctx)

	// Create and start WebSocket server.
	server := ws.NewServer(hub, jwtSvc, logger, cfg.WSPingInterval, cfg.WSPongTimeout, ws.ServerConfig{
		ConsoleHandler: consoleHandler,
		VNCHandler:     vncHandler,
	})

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
		if err := server.Shutdown(); err != nil {
			logger.Error("server shutdown error", "error", err)
		}
		hub.Stop()
		pool.Close()
		redisClient.Close()
	}()

	logger.Info("starting WebSocket server", "port", cfg.WSPort)
	if err := server.Listen(cfg.WSPort); err != nil {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

// healthcheck performs a simple HTTP health check against the local server.
func healthcheck() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}
	url := fmt.Sprintf("http://localhost:%d/healthz", cfg.WSPort)
	resp, err := http.Get(url) //nolint:gosec // localhost health check
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck returned %d\n", resp.StatusCode)
		os.Exit(1)
	}
	fmt.Println("ok")
}
