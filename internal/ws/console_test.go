package ws

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"

	"github.com/bigjakk/nexara/internal/auth"
)

func TestConsoleEndpointRequiresAuth(t *testing.T) {
	// Set up a server with console handler set to nil (no DB).
	// The /ws/console route should still require auth.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	hub := NewHub(logger, 0)
	hub.Run()
	defer hub.Stop()

	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	// Create a console handler with nil queries (it won't be reached).
	consoleHandler := NewConsoleHandler(nil, "0000000000000000000000000000000000000000000000000000000000000000", jwtSvc, logger)

	server := NewServer(hub, jwtSvc, logger, 25*time.Second, 30*time.Second, ServerConfig{
		ConsoleHandler: consoleHandler,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	go func() {
		_ = server.Listen(port)
	}()
	defer server.Shutdown()

	// Wait for server to be ready.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Test: no token should be rejected.
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws/console", port)
	_, resp, err := gorillaws.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected error for missing token on /ws/console")
	}
	if resp != nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", resp.StatusCode)
		}
	}

	// Test: invalid token should be rejected.
	url = fmt.Sprintf("ws://127.0.0.1:%d/ws/console?token=bad-token", port)
	_, resp, err = gorillaws.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected error for bad token on /ws/console")
	}
	if resp != nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", resp.StatusCode)
		}
	}
}

func TestConsoleRouteRegistered(t *testing.T) {
	// Verify the /ws/console endpoint exists (responds to HTTP GET, even if not a WS upgrade).
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	hub := NewHub(logger, 0)
	hub.Run()
	defer hub.Stop()

	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)
	consoleHandler := NewConsoleHandler(nil, "0000000000000000000000000000000000000000000000000000000000000000", jwtSvc, logger)
	server := NewServer(hub, jwtSvc, logger, 25*time.Second, 30*time.Second, ServerConfig{
		ConsoleHandler: consoleHandler,
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	go func() {
		_ = server.Listen(port)
	}()
	defer server.Shutdown()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if dialErr == nil {
			conn.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// HTTP GET to /ws/console (not WS upgrade) should return 426 Upgrade Required.
	url := fmt.Sprintf("http://127.0.0.1:%d/ws/console?token=test", port)
	resp, err := http.Get(url) //nolint:gosec // test
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUpgradeRequired {
		t.Errorf("expected 426, got %d", resp.StatusCode)
	}
}
