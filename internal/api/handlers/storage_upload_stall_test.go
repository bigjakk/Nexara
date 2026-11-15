package handlers

import (
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/bigjakk/nexara/internal/proxmox"
)

// TestStallBoundedBody_CutsABrowserThatStopsSending: the server arms no read
// deadline for a streamed upload, so without this bound a read of a silent
// browser's stream never returns, and the upload waits on it for ever.
func TestStallBoundedBody_CutsABrowserThatStopsSending(t *testing.T) {
	t.Parallel()
	server, browser := net.Pipe()
	t.Cleanup(func() { _ = server.Close(); _ = browser.Close() })
	body := stallBoundedBody{r: server, conn: server, stall: 50 * time.Millisecond}

	done := make(chan error, 1)
	go func() {
		_, err := body.Read(make([]byte, 1))
		done <- err
	}()
	select {
	case err := <-done:
		if !isReadTimeout(err) {
			t.Errorf("read = %v, want a read timeout", err)
		}
	case <-time.After(30 * time.Second): // 600x the stall: only a missing deadline gets here
		t.Fatal("the read of a silent browser was never cut")
	}
}

func TestMapUploadError(t *testing.T) {
	t.Parallel()
	timeout := &net.OpError{Op: "read", Net: "tcp", Err: timeoutErr{}}
	for _, tt := range []struct {
		name string
		err  error
		want int
	}{
		{"Proxmox stopped taking the file", fmt.Errorf("upload to pve-01/store01: %w", proxmox.ErrUploadStalled), fiber.StatusBadGateway},
		{"the browser stopped sending", fmt.Errorf("upload: %w: %w", proxmox.ErrUploadBodyRead, timeout), fiber.StatusRequestTimeout},
		{"the browser went away", fmt.Errorf("upload: %w: %w", proxmox.ErrUploadBodyRead, errors.New("unexpected EOF")), fiber.StatusBadRequest},
		{"Proxmox unreachable", fmt.Errorf("upload: %w: dial", proxmox.ErrConnectionFailed), fiber.StatusBadGateway},
	} {
		var fe *fiber.Error
		if !errors.As(mapUploadError(tt.err, time.Minute), &fe) || fe.Code != tt.want {
			t.Errorf("%s: mapUploadError = %v, want status %d", tt.name, fe, tt.want)
		}
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
