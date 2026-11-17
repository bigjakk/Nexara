//go:build linux

package proxmox

import (
	"net"
	"net/netip"
	"syscall"
	"testing"
)

// silentListener makes addr ("ip:port") a TCP endpoint that never answers a
// connect: a socket listening with a backlog of 0 whose one queue slot is
// filled and never accepted. Linux drops a SYN to a full accept queue, so a
// dial to addr hangs until its own timeout, as one to a powered-off host does.
func silentListener(t *testing.T, addr string) {
	t.Helper()
	ap := netip.MustParseAddrPort(addr)
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	t.Cleanup(func() { _ = syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Port: int(ap.Port()), Addr: ap.Addr().As4()}); err != nil {
		skipUnlessCI(t, "bind %s: %v", addr, err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatalf("listen: %v", err)
	}
	filler, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("filling the accept queue: %v", err)
	}
	t.Cleanup(func() { _ = filler.Close() })
}
