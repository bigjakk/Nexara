package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	gorillaWs "github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"

	fiberWs "github.com/gofiber/contrib/v3/websocket"
	"github.com/google/uuid"
)

// MaxBrowserConsoleMessageBytes caps the size of a single WebSocket message
// we'll accept from the browser side of a console / VNC proxy.
//
// This is the per-frame limit applied via fasthttp/websocket's SetReadLimit.
// Hitting it sends a close frame to the peer and returns ErrReadLimit on the
// next read — i.e. a single oversized frame cleanly tears down the
// connection rather than letting the read loop allocate unbounded memory.
//
// Sized for the actual workload:
//   - terminal keystrokes / resize JSON: tens of bytes
//   - clipboard paste into a terminal: occasional KBs, not MBs
//   - noVNC RFB client→server messages: <100 bytes (key/mouse events),
//     ClientCutText for clipboard could exceed this on large pastes — at
//     which point noVNC chunks or the user retries. 64 KiB is the standard
//     industry cap and matches gorilla/websocket's recommended default.
const MaxBrowserConsoleMessageBytes int64 = 64 * 1024

// ConsoleHandler manages terminal proxy connections.
type ConsoleHandler struct {
	queries       *db.Queries
	encryptionKey string
	cache         *proxmox.ClientCache // nil-safe; falls back to per-call construction
	jwt           *auth.JWTService
	logger        *slog.Logger
}

// NewConsoleHandler creates a new ConsoleHandler.
func NewConsoleHandler(queries *db.Queries, encryptionKey string, jwt *auth.JWTService, logger *slog.Logger) *ConsoleHandler {
	return &ConsoleHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		jwt:           jwt,
		logger:        logger,
	}
}

// SetProxmoxCache wires a shared cache into the handler. Called from
// cmd/nexara/main.go after construction so a single ClientCache backs
// every HTTP and WS handler.
func (h *ConsoleHandler) SetProxmoxCache(cache *proxmox.ClientCache) {
	h.cache = cache
}

// consoleResizeMsg is sent by the browser when the terminal is resized.
type consoleResizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// HandleConsole proxies a browser terminal session to Proxmox vncwebsocket.
func (h *ConsoleHandler) HandleConsole(conn *fiberWs.Conn) {
	// Cap any single browser→backend frame so a malicious tab cannot push a
	// multi-megabyte payload at us. Applied before any ReadMessage call.
	conn.SetReadLimit(MaxBrowserConsoleMessageBytes)

	// Close the browser socket on EVERY return; see closeBrowserSocket.
	defer closeBrowserSocket(conn)

	// Every value below comes from the scope the gate VALIDATED, not from a
	// second read of the query string. The gate checked c.Query, which returns
	// the first copy of a repeated key; conn.Query is this package's own
	// capture, where the last copy wins — so reading it here let
	// "?cluster_id=A&…&cluster_id=B" pass a check for cluster A and open a
	// console on B. No scope means this route was reached without the console
	// gate (see mountGated), and nothing below may run on that.
	scope, _ := conn.Locals(consoleScopeLocal).(*auth.ConsoleScope)
	if scope == nil {
		h.writeError(conn, "console scope missing")
		return
	}
	clusterIDStr := scope.ClusterID
	node := scope.Node
	consoleType := scope.Type
	vmidStr := strconv.Itoa(scope.VMID)

	logger := h.logger.With(
		"cluster_id", clusterIDStr,
		"node", node,
		"type", consoleType,
		"vmid", vmidStr,
	)

	clusterID, err := uuid.Parse(clusterIDStr)
	if err != nil {
		h.writeError(conn, "invalid cluster_id")
		return
	}

	if node == "" {
		h.writeError(conn, "node is required")
		return
	}

	// No default. The type is one the gate validated, and an unknown or empty
	// one is refused here rather than read as node_shell — the most powerful
	// console there is — before anything is looked up.
	switch consoleType {
	case "node_shell", "vm_serial", "ct_attach":
	default:
		h.writeError(conn, "invalid console type")
		return
	}

	ctx := context.Background()

	// Look up cluster and create Proxmox client.
	cluster, err := h.queries.GetCluster(ctx, clusterID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			h.writeError(conn, "cluster not found")
		} else {
			h.writeError(conn, "failed to look up cluster")
		}
		return
	}

	pxClient, err := h.proxmoxClientFor(ctx, clusterID, cluster)
	if err != nil {
		h.writeError(conn, err.Error())
		return
	}

	// Call the appropriate proxy endpoint.
	// node_shell uses termproxy for a text terminal.
	// vm_serial and ct_attach use vncproxy because termproxy ticket validation
	// has issues with API tokens on some Proxmox versions (bug #6079).
	var vncResp *proxmox.TermProxyResponse
	var vncPath string // resource path for vncwebsocket URL
	useTermProxy := false
	switch consoleType {
	case "node_shell":
		vncResp, err = pxClient.NodeTermProxy(ctx, node)
		useTermProxy = true
	case "vm_serial":
		vmid, parseErr := strconv.Atoi(vmidStr)
		if parseErr != nil {
			h.writeError(conn, "invalid vmid")
			return
		}
		vncResp, err = pxClient.VMVNCProxy(ctx, node, vmid)
		vncPath = "qemu/" + strconv.Itoa(vmid)
	case "ct_attach":
		vmid, parseErr := strconv.Atoi(vmidStr)
		if parseErr != nil {
			h.writeError(conn, "invalid vmid")
			return
		}
		vncResp, err = pxClient.CTVNCProxy(ctx, node, vmid)
		vncPath = "lxc/" + strconv.Itoa(vmid)
	default:
		h.writeError(conn, "invalid console type")
		return
	}
	if err != nil {
		logger.Error("proxy request failed", "error", err)
		if proxmox.IsGuestNotRunningError(err) {
			// Tell the browser the guest is powered off so it can park the
			// console instead of reconnect-looping against a dead guest.
			h.writeErrorCode(conn, "guest_not_running", "guest is not running")
		} else {
			h.writeError(conn, "failed to create console session")
		}
		return
	}

	logger.Info("proxy response",
		"port", int(vncResp.Port),
		"user", vncResp.User,
		"upid", vncResp.UPID,
		"vncPath", vncPath,
		"useTermProxy", useTermProxy,
		"ticket_len", len(vncResp.Ticket),
	)

	// Dial the Proxmox vncwebsocket.
	var pxConn *gorillaWs.Conn
	if useTermProxy {
		// termproxy: DialTerminal does user:ticket handshake.
		pxConn, err = pxClient.DialTerminal(ctx, node, vncResp.Ticket, int(vncResp.Port), vncPath, vncResp.User)
	} else {
		// vncproxy: no handshake needed.
		pxConn, err = pxClient.DialVNCWebSocket(ctx, node, vncResp.Ticket, int(vncResp.Port), vncPath)
	}
	if err != nil {
		logger.Error("dial websocket failed", "error", err)
		h.writeError(conn, "failed to connect to console")
		return
	}
	defer pxConn.Close()

	logger.Info("console session established")

	// Send a connected status to the browser.
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(`{"type":"connected"}`))

	var wg sync.WaitGroup
	wg.Add(2)

	// Browser → Proxmox
	go func() {
		defer wg.Done()
		defer closeRelayLeg(pxConn)
		for {
			msgType, msg, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}

			switch msgType {
			case fiberWs.TextMessage:
				// Check if it's a resize message.
				var resize consoleResizeMsg
				if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
					if useTermProxy {
						// termproxy resize: send "1:cols:rows:" format.
						resizeMsg := "1:" + strconv.Itoa(resize.Cols) + ":" + strconv.Itoa(resize.Rows) + ":"
						if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, []byte(resizeMsg)); writeErr != nil {
							return
						}
					}
					// For vncproxy, skip — VNC handles its own framebuffer sizing.
					continue
				}
				if useTermProxy {
					// termproxy data channel: "0:LENGTH:DATA" format.
					prefixed := "0:" + strconv.Itoa(len(msg)) + ":" + string(msg)
					if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, []byte(prefixed)); writeErr != nil {
						return
					}
				} else {
					if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, msg); writeErr != nil {
						return
					}
				}
			case fiberWs.BinaryMessage:
				if useTermProxy {
					// termproxy data channel: "0:LENGTH:DATA" format.
					prefixed := "0:" + strconv.Itoa(len(msg)) + ":" + string(msg)
					if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, []byte(prefixed)); writeErr != nil {
						return
					}
				} else {
					if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, msg); writeErr != nil {
						return
					}
				}
			}
		}
	}()

	// Proxmox → Browser
	go func() {
		defer wg.Done()
		defer closeRelayLeg(conn)
		for {
			_, msg, readErr := pxConn.ReadMessage()
			if readErr != nil {
				logger.Debug("proxmox read error", "error", readErr)
				return
			}

			if useTermProxy && len(msg) > 2 && msg[1] == ':' {
				// termproxy channel protocol: "0:data" for terminal output.
				// Strip the channel prefix before sending to browser.
				if writeErr := conn.WriteMessage(fiberWs.BinaryMessage, msg[2:]); writeErr != nil {
					return
				}
			} else {
				if writeErr := conn.WriteMessage(fiberWs.BinaryMessage, msg); writeErr != nil {
					return
				}
			}
		}
	}()

	wg.Wait()
	logger.Info("console session closed")
}

// closeDrainTimeout bounds how long closing a console waits on a peer that
// has been sent a Close frame: the browser, before its socket is closed, and
// either side of the relay, before the other side's reader gives up on it.
const closeDrainTimeout = 3 * time.Second

// peerConn is the part of a websocket connection closeRelayLeg needs. The
// browser's connection and the Proxmox one are different types from two
// websocket packages, and both have these, with these signatures.
type peerConn interface {
	NetConn() net.Conn
	WriteControl(messageType int, data []byte, deadline time.Time) error
}

// closeBrowserSocket is every console handler's deferred close.
//
// The websocket package leaves a hijacked connection open when its handler
// returns normally — it closes one only after a panic — and fasthttp does not
// close it either (KeepHijackedConns), so until this every early refusal, and
// every session end, left the socket for the client to close or the garbage
// collector to find.
//
// It sends a Close frame first. A refusal has normally written one already
// (writeError), as has the relay's Proxmox-to-browser leg on its way out, and
// then this one is refused as a second Close and changes nothing. It is here
// for the path that has not: a panic, which unwinds through this before the
// websocket package's recover handler can write its {"error":"internal
// error"} — so the Close carries that reason instead — and which would
// otherwise wait out the whole drain with no Close to answer.
//
// It then reads before it closes. Closing a TCP socket that still holds unread
// input makes the kernel answer with a reset, which on some client stacks
// overtakes the error text and the Close frame, so the client reports an
// abnormal closure instead of the reason. Reading until the client's own
// Close arrives, or closeDrainTimeout passes, empties that input first; the
// deadline is what a client that never answers can cost. After a relay the
// read side has usually failed already — fasthttp's read errors are
// permanent — so there the drain returns at once. By the time this runs the
// handler's readers have all returned, so this is the only one.
func closeBrowserSocket(conn *fiberWs.Conn) {
	deadline := time.Now().Add(closeDrainTimeout)
	_ = conn.WriteControl(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(fiberWs.CloseInternalServerErr, "internal error"), deadline)
	_ = conn.SetReadDeadline(deadline)
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
	_ = conn.Close()
}

// closeRelayLeg ends one direction of a console relay: it bounds how long the
// OTHER leg — the one reading from `to` — waits for an answer, then sends `to`
// the Close frame that asks for one.
//
// Without the bound a peer that never answers the Close kept the other leg in
// ReadMessage forever, so the handler never got past wg.Wait: no deferred
// close, no pxConn.Close, two goroutines and two sockets held for as long as
// that peer stayed connected. The read deadline goes on the net.Conn rather
// than through the websocket's SetReadDeadline because both websocket
// packages count that among the read methods one goroutine may use at a
// time, and the other leg is reading; a net.Conn's methods may be called from
// any goroutine. The Close goes out through WriteControl, which both packages
// let any goroutine call and which takes the same deadline, so a peer that
// has stopped reading cannot hold this leg in the write either. The frame is
// built with gorilla's helpers for both connections: CloseMessage and the
// close-code encoding are RFC 6455's, identical in the two packages.
func closeRelayLeg(to peerConn) {
	deadline := time.Now().Add(closeDrainTimeout)
	_ = to.NetConn().SetReadDeadline(deadline)
	_ = to.WriteControl(gorillaWs.CloseMessage,
		gorillaWs.FormatCloseMessage(gorillaWs.CloseNormalClosure, ""), deadline)
}

// writeError sends a JSON error message to the browser, then a Close frame.
// It does not close the connection: closeBrowserSocket, which the handler
// defers, does that once the handler returns.
func (h *ConsoleHandler) writeError(conn *fiberWs.Conn, msg string) {
	errMsg := fmt.Sprintf(`{"type":"error","message":%q}`, msg)
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(errMsg))
	_ = conn.WriteMessage(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(fiberWs.CloseInternalServerErr, msg))
}

// writeErrorCode is writeError with a machine-readable code the frontend can
// branch on (e.g. "guest_not_running").
func (h *ConsoleHandler) writeErrorCode(conn *fiberWs.Conn, code, msg string) {
	errMsg := fmt.Sprintf(`{"type":"error","code":%q,"message":%q}`, code, msg)
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(errMsg))
	_ = conn.WriteMessage(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(fiberWs.CloseInternalServerErr, msg))
}

// proxmoxClientFor returns a Proxmox client backed by the shared cache when
// available; falls back to per-call construction otherwise. The cluster row
// is already looked up by the caller and threaded in so the cache lookup
// + the not-found path stay readable in HandleConsole.
func (h *ConsoleHandler) proxmoxClientFor(ctx context.Context, clusterID uuid.UUID, cluster db.Cluster) (*proxmox.Client, error) {
	if h.cache != nil {
		client, err := h.cache.Get(ctx, clusterID)
		if err == nil {
			return client, nil
		}
		// Fall through to per-call build on cache failure so a transient
		// pub/sub or DB blip doesn't break console connections.
		h.logger.Warn("console: proxmox cache get failed, building per-call",
			"cluster_id", clusterID, "error", err)
	}

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		return nil, errors.New("failed to decrypt cluster credentials")
	}
	client, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		return nil, errors.New("failed to create Proxmox client")
	}
	return client, nil
}
