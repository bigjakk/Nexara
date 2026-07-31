package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/events"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// authEventActor is the user an authentication event is recorded against. Auth
// handlers know it from the credentials or the session, never from
// c.Locals("user_id").
var authEventActor = uuid.MustParse("5e5e5e5e-5e5e-45e5-8e5e-5e5e5e5e5e5e")

// newCollector starts a UDP listener and a Forwarder pointed at it, returning
// a read function that fails the test if nothing arrives.
func newCollector(t *testing.T) (*events.Publisher, func(wantRecord bool) string) {
	t.Helper()

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the collector: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	port := conn.LocalAddr().(*net.UDPAddr).Port

	fwd := proxsyslog.NewForwarder(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(fwd.Close)
	if err := fwd.Configure(proxsyslog.Config{
		Enabled: true, Host: "127.0.0.1", Port: port, Protocol: "udp", Facility: 16,
	}); err != nil {
		t.Fatalf("configure forwarder: %v", err)
	}

	pub := events.NewPublisher(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	pub.SetSyslogForwarder(fwd)

	read := func(wantRecord bool) string {
		t.Helper()
		// Short deadline on the negative case so a test asserting silence does
		// not pay the full timeout.
		d := 5 * time.Second
		if !wantRecord {
			d = 700 * time.Millisecond
		}
		if err := conn.SetReadDeadline(time.Now().Add(d)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		buf := make([]byte, 8192)
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if wantRecord {
				t.Fatalf("the collector received nothing: %v\n"+
					"\tAuthentication events must reach the syslog forwarder — login, logout and "+
					"password_changed are the records a SIEM is deployed to collect.", err)
			}
			return ""
		}
		return string(buf[:n])
	}

	return pub, read
}

// TestAuditLogAsForwardsWithoutRequestActor is the regression lock behind
// AuditLogAs, and behind auth.go no longer owning a private audit wrapper.
//
// Authentication events are audited where c.Locals("user_id") is unset — a
// login has not passed the auth middleware, a logout has already torn the
// session down. The subtests contrast the two helpers on exactly that request:
// AuditLog correctly declines to invent an actor, and AuditLogAs records and
// forwards the one the caller supplies. Before this, auth.go resolved that
// tension with its own InsertAuditLog, which reached neither the WS event nor
// the collector.
func TestAuditLogAsForwardsWithoutRequestActor(t *testing.T) {
	t.Run("AuditLogAs records and forwards", func(t *testing.T) {
		pub, read := newCollector(t)
		capture := &captureDBTX{}
		queries := db.New(capture)

		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Post("/login", func(c fiber.Ctx) error {
			// Deliberately no c.Locals("user_id") — this is the point.
			AuditLogAs(c, queries, pub, authEventActor, pgtype.UUID{},
				"auth", authEventActor.String(), "login", json.RawMessage(`{"method":"password"}`))
			return c.SendStatus(http.StatusNoContent)
		})

		resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/login", nil))
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		entries := capture.auditInserts(t)
		if len(entries) != 1 {
			t.Fatalf("wrote %d audit rows, want exactly 1: %+v", len(entries), entries)
		}
		if got := entries[0].Action; got != "login" {
			t.Errorf("action = %q, want %q", got, "login")
		}
		if got := uuid.UUID(entries[0].UserID.Bytes); got != authEventActor {
			t.Errorf("user_id = %v, want %v — the explicit actor was dropped", got, authEventActor)
		}

		line := read(true)
		if !strings.Contains(line, `action="login"`) {
			t.Errorf("collector received %q, want a login record", line)
		}
		if !strings.Contains(line, authEventActor.String()) {
			t.Errorf("forwarded record does not name the actor: %s", line)
		}
	})

	t.Run("AuditLog on the same request records nothing", func(t *testing.T) {
		pub, read := newCollector(t)
		capture := &captureDBTX{}
		queries := db.New(capture)

		app := fiber.New(fiber.Config{ErrorHandler: testErrorHandler})
		app.Post("/login", func(c fiber.Ctx) error {
			AuditLog(c, queries, pub, pgtype.UUID{},
				"auth", authEventActor.String(), "login", nil)
			return c.SendStatus(http.StatusNoContent)
		})

		resp, err := app.Test(httptest.NewRequest(http.MethodPost, "/login", nil))
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		// Not a defect in AuditLog — it has no actor to record and correctly
		// declines to guess. It is why AuditLogAs has to exist, and why routing
		// auth events through AuditLog instead would silently lose all of them.
		if entries := capture.auditInserts(t); len(entries) != 0 {
			t.Fatalf("AuditLog invented an actor on a request with no user_id: %+v", entries)
		}
		if line := read(false); line != "" {
			t.Errorf("collector received %q for an entry that was never written", line)
		}
	})
}
