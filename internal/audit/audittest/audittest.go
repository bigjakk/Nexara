// Package audittest is the syslog collector tests point an audit writer at, so
// a test can see what a SIEM would receive. Used by every package whose audit
// entries are forwarded; extend it rather than standing up another listener.
package audittest

import (
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/bigjakk/nexara/internal/events"
	proxsyslog "github.com/bigjakk/nexara/internal/syslog"
)

// Collector starts a UDP listener on 127.0.0.1 and returns a publisher (no
// Redis) whose syslog forwarder sends to it, and next, which returns the next
// record that arrives within wait, or "" when none does. Both are torn down
// with the test.
func Collector(t testing.TB) (pub *events.Publisher, next func(wait time.Duration) string) {
	t.Helper()

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for the collector: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	fwd := proxsyslog.NewForwarder(discard)
	t.Cleanup(fwd.Close)
	if err := fwd.Configure(proxsyslog.Config{
		Enabled: true, Host: "127.0.0.1", Port: conn.LocalAddr().(*net.UDPAddr).Port, Protocol: "udp", Facility: 16,
	}); err != nil {
		t.Fatalf("configure forwarder: %v", err)
	}

	pub = events.NewPublisher(nil, discard)
	pub.SetSyslogForwarder(fwd)

	next = func(wait time.Duration) string {
		t.Helper()
		if err := conn.SetReadDeadline(time.Now().Add(wait)); err != nil {
			t.Fatalf("set read deadline: %v", err)
		}
		buf := make([]byte, 8192)
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return ""
		}
		return string(buf[:n])
	}
	return pub, next
}
