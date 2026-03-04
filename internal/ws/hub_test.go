package ws

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestClient() *Client {
	return &Client{
		id:   "test-" + time.Now().Format("150405.000"),
		send: make(chan []byte, clientSendBuffer),
	}
}

func drainOne(t *testing.T, c *Client, timeout time.Duration) []byte {
	t.Helper()
	select {
	case msg := <-c.send:
		return msg
	case <-time.After(timeout):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

func assertNoMessage(t *testing.T, c *Client) {
	t.Helper()
	select {
	case msg := <-c.send:
		t.Fatalf("unexpected message: %s", msg)
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func TestHubSubscribeAndBroadcast(t *testing.T) {
	h := NewHub(testLogger())
	h.Run()
	defer h.Stop()

	c1 := newTestClient()
	c1.id = "c1"
	c2 := newTestClient()
	c2.id = "c2"

	h.Register(c1)
	h.Register(c2)

	room := "cluster:550e8400-e29b-41d4-a716-446655440000:metrics"
	h.Subscribe(c1, room)
	// Wait for subscribe confirmation.
	msg := drainOne(t, c1, time.Second)
	var out OutgoingMessage
	if err := json.Unmarshal(msg, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Type != MsgTypeSubscribed || out.Channel != room {
		t.Fatalf("expected subscribed msg, got %+v", out)
	}

	// Broadcast to room — only c1 should receive.
	payload := json.RawMessage(`{"cpu":0.5}`)
	h.Broadcast(room, payload)

	data := drainOne(t, c1, time.Second)
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Type != MsgTypeData || out.Channel != room {
		t.Errorf("expected data msg for room, got %+v", out)
	}

	// c2 should not receive anything.
	assertNoMessage(t, c2)
}

func TestHubUnsubscribe(t *testing.T) {
	h := NewHub(testLogger())
	h.Run()
	defer h.Stop()

	c := newTestClient()
	c.id = "unsub-test"

	h.Register(c)

	room := "cluster:550e8400-e29b-41d4-a716-446655440000:alerts"
	h.Subscribe(c, room)
	drainOne(t, c, time.Second) // subscribed confirmation

	h.Unsubscribe(c, room)
	// Small delay for event loop to process.
	time.Sleep(50 * time.Millisecond)

	h.Broadcast(room, json.RawMessage(`{}`))
	assertNoMessage(t, c)
}

func TestHubUnregisterCleansUpRooms(t *testing.T) {
	h := NewHub(testLogger())
	h.Run()
	defer h.Stop()

	c := newTestClient()
	c.id = "unreg-test"

	h.Register(c)

	room := "cluster:550e8400-e29b-41d4-a716-446655440000:metrics"
	h.Subscribe(c, room)
	drainOne(t, c, time.Second) // subscribed

	h.Unregister(c)
	// Channel should be closed after unregister.
	time.Sleep(50 * time.Millisecond)

	// Verify send channel is closed.
	_, ok := <-c.send
	if ok {
		t.Error("expected send channel to be closed")
	}
}

func TestHubReconnectBuffer(t *testing.T) {
	h := NewHub(testLogger())
	h.Run()
	defer h.Stop()

	room := "cluster:550e8400-e29b-41d4-a716-446655440000:metrics"

	// Publish a message to the room before any clients.
	c1 := newTestClient()
	c1.id = "buffer-writer"
	h.Register(c1)
	h.Subscribe(c1, room)
	drainOne(t, c1, time.Second) // subscribed

	payload := json.RawMessage(`{"cached":true}`)
	h.Broadcast(room, payload)
	drainOne(t, c1, time.Second) // data message

	// Now a new client subscribes — should get the cached message.
	c2 := newTestClient()
	c2.id = "buffer-reader"
	h.Register(c2)
	h.Subscribe(c2, room)

	// Should receive subscribed + cached data.
	sub := drainOne(t, c2, time.Second)
	var subMsg OutgoingMessage
	if err := json.Unmarshal(sub, &subMsg); err != nil {
		t.Fatalf("unmarshal subscribed: %v", err)
	}
	if subMsg.Type != MsgTypeSubscribed {
		t.Fatalf("expected subscribed, got %s", subMsg.Type)
	}

	cached := drainOne(t, c2, time.Second)
	var dataMsg OutgoingMessage
	if err := json.Unmarshal(cached, &dataMsg); err != nil {
		t.Fatalf("unmarshal cached: %v", err)
	}
	if dataMsg.Type != MsgTypeData {
		t.Fatalf("expected data, got %s", dataMsg.Type)
	}
}

func TestHubSlowClientEviction(t *testing.T) {
	h := NewHub(testLogger())
	h.Run()
	defer h.Stop()

	// Create a client with a tiny buffer to simulate slow client.
	slow := &Client{
		id:   "slow-client",
		send: make(chan []byte, 1), // Very small buffer.
	}

	h.Register(slow)

	room := "cluster:550e8400-e29b-41d4-a716-446655440000:metrics"
	h.Subscribe(slow, room)
	// Don't drain the subscribed message — buffer will be full.

	// Flood with broadcasts to trigger eviction.
	for i := 0; i < 5; i++ {
		h.Broadcast(room, json.RawMessage(`{"i":1}`))
	}

	// Wait for the hub to process.
	time.Sleep(100 * time.Millisecond)

	// After eviction, send channel should be closed.
	// Drain whatever is buffered first.
	for {
		_, ok := <-slow.send
		if !ok {
			break
		}
	}
	// If we get here, channel was closed — test passes.
}
