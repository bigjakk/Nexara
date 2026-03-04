package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	gorillaws "github.com/gorilla/websocket"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/proxdash/proxdash/internal/auth"
)

type testEnv struct {
	server      *Server
	redisClient *redis.Client
	jwtSvc      *auth.JWTService
	port        int
}

func setupIntegration(t *testing.T) (*testEnv, func()) {
	t.Helper()

	mr := miniredis.RunT(t)

	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	hub := NewHub(logger)
	hub.Run()

	jwtSvc := auth.NewJWTService("test-secret-key-for-testing-only", 15*time.Minute, 168*time.Hour)

	server := NewServer(hub, jwtSvc, logger, 25*time.Second, 30*time.Second)

	// Start Redis subscriber.
	ctx, cancel := context.WithCancel(context.Background())
	sub := NewRedisSubscriber(redisClient, hub, logger)
	go sub.Run(ctx)

	// Find a free port and start the server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	go func() {
		_ = server.Listen(port)
	}()

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

	cleanup := func() {
		cancel()
		server.Shutdown()
		hub.Stop()
		redisClient.Close()
	}

	return &testEnv{
		server:      server,
		redisClient: redisClient,
		jwtSvc:      jwtSvc,
		port:        port,
	}, cleanup
}

func (e *testEnv) generateToken(t *testing.T) string {
	t.Helper()
	userID := uuid.New()
	token, _, err := e.jwtSvc.GenerateAccessToken(userID, "test@example.com", "admin")
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return token
}

func (e *testEnv) dialWS(t *testing.T, token string) *gorillaws.Conn {
	t.Helper()
	url := fmt.Sprintf("ws://127.0.0.1:%d/ws?token=%s", e.port, token)
	conn, _, err := gorillaws.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	return conn
}

func readMsg(t *testing.T, conn *gorillaws.Conn, timeout time.Duration) OutgoingMessage {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read message: %v", err)
	}
	var msg OutgoingMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return msg
}

func writeMsg(t *testing.T, conn *gorillaws.Conn, msg IncomingMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.WriteMessage(gorillaws.TextMessage, data); err != nil {
		t.Fatalf("write message: %v", err)
	}
}

func TestIntegrationHealthz(t *testing.T) {
	env, cleanup := setupIntegration(t)
	defer cleanup()

	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", env.port)
	resp, err := http.Get(url) //nolint:gosec // test
	if err != nil {
		t.Fatalf("healthz request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestIntegrationWSConnectAndWelcome(t *testing.T) {
	env, cleanup := setupIntegration(t)
	defer cleanup()

	token := env.generateToken(t)
	conn := env.dialWS(t, token)
	defer conn.Close()

	msg := readMsg(t, conn, 3*time.Second)
	if msg.Type != MsgTypeWelcome {
		t.Errorf("expected welcome, got %s", msg.Type)
	}
}

func TestIntegrationWSNoToken(t *testing.T) {
	env, cleanup := setupIntegration(t)
	defer cleanup()

	url := fmt.Sprintf("ws://127.0.0.1:%d/ws", env.port)
	_, resp, err := gorillaws.DefaultDialer.Dial(url, nil)
	if err == nil {
		t.Fatal("expected error for missing token")
	}
	if resp != nil && resp.StatusCode != 401 {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestIntegrationSubscribeAndReceive(t *testing.T) {
	env, cleanup := setupIntegration(t)
	defer cleanup()

	token := env.generateToken(t)
	conn := env.dialWS(t, token)
	defer conn.Close()

	// Read welcome.
	welcome := readMsg(t, conn, 3*time.Second)
	if welcome.Type != MsgTypeWelcome {
		t.Fatalf("expected welcome, got %s", welcome.Type)
	}

	clusterUUID := "550e8400-e29b-41d4-a716-446655440000"
	room := fmt.Sprintf("cluster:%s:metrics", clusterUUID)

	// Subscribe.
	writeMsg(t, conn, IncomingMessage{
		Type:     MsgTypeSubscribe,
		Channels: []string{room},
	})

	// Read subscribed confirmation.
	sub := readMsg(t, conn, 3*time.Second)
	if sub.Type != MsgTypeSubscribed || sub.Channel != room {
		t.Fatalf("expected subscribed for %s, got %+v", room, sub)
	}

	// Publish metrics via Redis (like the collector does).
	redisChannel := fmt.Sprintf("proxdash:metrics:%s", clusterUUID)
	payload := `{"cpu":0.75,"mem_used":1024}`
	if err := env.redisClient.Publish(context.Background(), redisChannel, payload).Err(); err != nil {
		t.Fatalf("redis publish: %v", err)
	}

	// Read the data message.
	data := readMsg(t, conn, 3*time.Second)
	if data.Type != MsgTypeData {
		t.Fatalf("expected data, got %s", data.Type)
	}
	if data.Channel != room {
		t.Errorf("expected channel %s, got %s", room, data.Channel)
	}
}

func TestIntegrationPingPong(t *testing.T) {
	env, cleanup := setupIntegration(t)
	defer cleanup()

	token := env.generateToken(t)
	conn := env.dialWS(t, token)
	defer conn.Close()

	// Read welcome.
	readMsg(t, conn, 3*time.Second)

	// Send application-level ping.
	writeMsg(t, conn, IncomingMessage{Type: MsgTypePing})

	// Read pong.
	pong := readMsg(t, conn, 3*time.Second)
	if pong.Type != MsgTypePong {
		t.Errorf("expected pong, got %s", pong.Type)
	}
}
