package ws

import (
	"strings"
	"testing"
	"time"

	gorillaws "github.com/gorilla/websocket"

	"github.com/bigjakk/nexara/internal/auth"
)

// TestConsoleHandlers_CloseOnAnOversizedBrowserFrame pins the browser-side
// read limit of each console handler: one frame a byte over
// MaxBrowserConsoleMessageBytes must close the session with 1009, not be
// relayed. Without SetReadLimit the frame is relayed and the socket stays open.
func TestConsoleHandlers_CloseOnAnOversizedBrowserFrame(t *testing.T) {
	t.Parallel()
	for name, tt := range map[string]struct {
		kind  string
		scope auth.ConsoleScope
	}{
		"serial console": {"console", auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_serial"}},
		"VNC":            {"vnc", auth.ConsoleScope{ClusterID: gateClusterA, Node: "pve-01", VMID: 100, Type: "vm_vnc"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			pve := newFakeProxmox(t, func(c *gorillaws.Conn) {
				for {
					if _, _, err := c.ReadMessage(); err != nil {
						break
					}
				}
				<-release
			})

			browser := openSession(t, tt.kind, tt.scope, pve, "")
			readConnected(t, browser)
			frame := strings.Repeat("x", int(MaxBrowserConsoleMessageBytes)+1)
			if err := browser.WriteMessage(gorillaws.BinaryMessage, []byte(frame)); err != nil {
				t.Fatalf("sending the oversized frame: %v", err)
			}
			_ = browser.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, _, err := browser.ReadMessage(); !gorillaws.IsCloseError(err, gorillaws.CloseMessageTooBig) {
				t.Fatalf("after a %d-byte frame: %v, want a Close %d", len(frame), err, gorillaws.CloseMessageTooBig)
			}
		})
	}
}
