//go:build linux

package proxmox

import (
	"context"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// synDroppingAddr returns the address of a listener whose accept queue is
// full, so a TCP connect to it hangs: the kernel drops the SYN instead of
// answering it, which is what the connect sees of a node that is up but not
// letting anyone in.
//
// A listen backlog of zero still queues one connection on Linux, and once the
// queue is full further SYNs are dropped. The loop connects until a connect
// hangs, so the fixture is shown to stall rather than assumed to.
func synDroppingAddr(t *testing.T) string {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	// Closing the listener is also what frees a connect this test failed to
	// bound: its next SYN is answered with a reset.
	t.Cleanup(func() { _ = syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatalf("listen: %v", err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatalf("getsockname: %v", err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", sa.(*syscall.SockaddrInet4).Port)

	for queued := 0; ; queued++ {
		c, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			t.Cleanup(func() { _ = c.Close() })
			if queued == 8 {
				t.Fatalf("precondition: %d connects to a listener with a backlog of zero all went through, so nothing here stalls a connect", queued+1)
			}
			continue
		}
		if !isTimeout(err) {
			t.Fatalf("precondition: filling the accept queue: %v", err)
		}
		return addr
	}
}

// TestConsoleDial_ConnectStallEndsAtTheBound: the handshake bound covers the
// TCP connect as well as what follows it. gorilla connects with a plain
// net.Dialer under the bound's context; without the bound, the connect waits
// out the kernel's SYN retries, which on Linux's defaults is over two minutes.
func TestConsoleDial_ConnectStallEndsAtTheBound(t *testing.T) {
	for name, dial := range consoleDials {
		t.Run(name, func(t *testing.T) {
			c := consoleClient(t, "http://"+synDroppingAddr(t))
			c.console = consoleTimeouts{mint: unhurried, handshake: stallBound, auth: unhurried}

			r := dialWithin(t, stallBound+stallSlack, func() (*websocket.Conn, error) {
				return dial(c, "PVEVNC:connect")
			})
			if r.err == nil {
				t.Fatal("the dial succeeded against a listener that answers no SYN")
			}
			if !isTimeout(r.err) {
				t.Errorf("err = %v, want the handshake bound's timeout", r.err)
			}
			if r.elapsed < stallBound {
				t.Errorf("the dial gave up after %v, inside the %v bound: something other than the bound ended it (%v)",
					r.elapsed, stallBound, r.err)
			}
		})
	}
}

// TestConsole_RefusesABlockedAddress points every way this package connects
// at 0.0.0.0, an address netguard.DialControlSSRFGuard always blocks, and
// requires each to be refused by the guard before a packet goes out: the
// console dials, which connect through consoleDialer, and an API request and
// a ticket mint, which connect through buildHTTPClient's transport. All of it
// is guardedDialer, so this is also what shows the two paths share it.
//
// Linux connects a dial to the unspecified address to the local host, so a
// listener on loopback is where an unguarded dial to 0.0.0.0 lands. The twin
// below shows that it does, which is what makes the listener staying empty
// mean the guard stopped the dial, rather than that nothing could have
// arrived.
func TestConsole_RefusesABlockedAddress(t *testing.T) {
	listener := newStallServer(t)
	accepted := &listener.accepted
	blocked := fmt.Sprintf("0.0.0.0:%d", listener.ln.Addr().(*net.TCPAddr).Port)

	raw, err := net.DialTimeout("tcp", blocked, time.Second)
	if err != nil {
		t.Fatalf("precondition: an unguarded dial to %s did not reach the loopback listener: %v", blocked, err)
	}
	_ = raw.Close()
	for deadline := time.Now().Add(time.Second); accepted.Load() == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("precondition: the listener never saw the unguarded dial to %s", blocked)
		}
	}

	calls := map[string]func(c *Client) error{
		"DialVNCWebSocket": func(c *Client) error {
			_, err := consoleDials["DialVNCWebSocket"](c, "PVEVNC:blocked")
			return err
		},
		"DialTerminal": func(c *Client) error {
			_, err := consoleDials["DialTerminal"](c, "PVEVNC:blocked")
			return err
		},
		"an API request": func(c *Client) error {
			var v struct{}
			return c.do(context.Background(), "/version", &v)
		},
		"a ticket mint": consoleMints["VMVNCProxy"],
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			before := accepted.Load()
			c, err := NewClient(ClientConfig{
				BaseURL:     "https://" + blocked,
				TokenID:     canaryTokenID,
				TokenSecret: canaryTokenSecret,
				Timeout:     stallBound,
			})
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			c.console = consoleTimeouts{mint: stallBound, handshake: stallBound, auth: stallBound}

			o := within(t, stallBound+stallSlack, func() (struct{}, error) { return struct{}{}, call(c) })
			if o.err == nil || !strings.Contains(o.err.Error(), "blocked SSRF target") {
				t.Errorf("err = %v, want the SSRF dial guard's refusal", o.err)
			}
			if got := accepted.Load(); got != before {
				t.Errorf("the listener accepted %d connection(s) during the call: the dial went out", got-before)
			}
		})
	}
}
