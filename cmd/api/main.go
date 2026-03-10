package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/bigjakk/nexara/internal/api"
	"github.com/bigjakk/nexara/internal/config"
	"github.com/bigjakk/nexara/internal/db"
	"github.com/bigjakk/nexara/internal/debug"
)

func main() {
	// Healthcheck CLI mode for Docker HEALTHCHECK.
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		runHealthcheck()
		return
	}

	// Load configuration.
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	ctx := context.Background()

	// Connect to PostgreSQL (required).
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("failed to ping database: %v", err)
	}
	log.Println("connected to database")

	// Create schema on fresh database.
	if err := db.EnsureSchema(ctx, pool); err != nil {
		log.Fatalf("failed to ensure database schema: %v", err)
	}

	// Connect to Redis (optional).
	var rdb *redis.Client
	opts, err := redis.ParseURL(cfg.RedisURL)
	if err != nil {
		log.Printf("warning: invalid REDIS_URL, skipping Redis: %v", err)
	} else {
		rdb = redis.NewClient(opts)
		if err := rdb.Ping(ctx).Err(); err != nil {
			log.Printf("warning: failed to connect to Redis: %v", err)
			rdb = nil
		} else {
			log.Println("connected to Redis")
			defer rdb.Close()
		}
	}

	// Start pprof if enabled.
	if cfg.PprofEnabled {
		debug.StartPprof(cfg.PprofPort, slog.Default())
	}

	// Create and start the server.
	srv := api.New(cfg, pool, rdb)
	addr := fmt.Sprintf(":%d", cfg.APIPort)

	go func() {
		log.Printf("starting API server on %s", addr)
		if err := srv.Listen(addr); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT/SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down server...")
	if err := srv.Shutdown(); err != nil {
		log.Fatalf("server shutdown error: %v", err)
	}
	log.Println("server stopped")
}

func runHealthcheck() {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost:8080/healthz")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck failed: status %d\n", resp.StatusCode)
		os.Exit(1)
	}
}
