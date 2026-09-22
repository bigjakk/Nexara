package ws

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/bigjakk/nexara/internal/auth"
	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/proxmox"

	fiberWs "github.com/gofiber/contrib/v3/websocket"
	"github.com/google/uuid"
)

// VNCHandler manages VNC proxy connections for graphical VM consoles.
type VNCHandler struct {
	queries       *db.Queries
	encryptionKey string
	cache         *proxmox.ClientCache // nil-safe; falls back to per-call construction
	jwt           *auth.JWTService
	logger        *slog.Logger
}

// NewVNCHandler creates a new VNCHandler.
func NewVNCHandler(queries *db.Queries, encryptionKey string, jwt *auth.JWTService, logger *slog.Logger) *VNCHandler {
	return &VNCHandler{
		queries:       queries,
		encryptionKey: encryptionKey,
		jwt:           jwt,
		logger:        logger,
	}
}

// SetProxmoxCache wires a shared cache into the handler. Called from
// cmd/nexara/main.go after construction.
func (h *VNCHandler) SetProxmoxCache(cache *proxmox.ClientCache) {
	h.cache = cache
}

// HandleVNC proxies a browser noVNC session to Proxmox via the vncwebsocket endpoint.
// The flow matches ProxCenter's approach: connect to vncwebsocket with the ticket
// in the URL query params, then bidirectionally forward all WebSocket messages.
func (h *VNCHandler) HandleVNC(conn *fiberWs.Conn) {
	// Cap any single browser→backend frame; see MaxBrowserConsoleMessageBytes.
	conn.SetReadLimit(MaxBrowserConsoleMessageBytes)

	// Close the browser socket on every return; see closeBrowserSocket.
	defer closeBrowserSocket(conn)

	// From the validated scope, not the query — for the reason HandleConsole
	// gives: the gate checks the first copy of a repeated key and conn.Query
	// holds the last. That includes the guest kind below, which a duplicate
	// ?type=lxc used to flip from the VM the token named to a container
	// sharing its VMID.
	scope, _ := conn.Locals(consoleScopeLocal).(*auth.ConsoleScope)
	if scope == nil {
		h.writeError(conn, "console scope missing")
		return
	}
	clusterIDStr := scope.ClusterID
	node := scope.Node
	vmidStr := strconv.Itoa(scope.VMID)

	logger := h.logger.With(
		"cluster_id", clusterIDStr,
		"node", node,
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

	vmid, err := strconv.Atoi(vmidStr)
	if err != nil {
		h.writeError(conn, "invalid vmid")
		return
	}

	// The guest kind is the scope's, and only the two VNC types name one. Any
	// other type is refused rather than read as a VM, before anything is
	// looked up.
	var guestType string
	switch scope.Type {
	case "vm_vnc":
		guestType = "qemu"
	case "ct_vnc":
		guestType = "lxc"
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

	// Request VNC proxy ticket — support both QEMU VMs and LXC containers.
	var vncResp *proxmox.TermProxyResponse
	var vncPath string
	switch guestType {
	case "lxc":
		vncResp, err = pxClient.CTVNCProxy(ctx, node, vmid)
		vncPath = "lxc/" + strconv.Itoa(vmid)
	default:
		vncResp, err = pxClient.VMVNCProxy(ctx, node, vmid)
		vncPath = "qemu/" + strconv.Itoa(vmid)
	}
	if err != nil {
		logger.Error("vncproxy request failed", "error", err)
		if proxmox.IsGuestNotRunningError(err) {
			// Tell the browser the guest is powered off so it can park the
			// console instead of reconnect-looping against a dead guest.
			h.writeErrorCode(conn, "guest_not_running", "guest is not running")
		} else {
			h.writeError(conn, "failed to create VNC session")
		}
		return
	}

	// Connect to Proxmox vncwebsocket endpoint (ticket in URL query params).
	pxConn, err := pxClient.DialVNCWebSocket(ctx, node, vncResp.Ticket, int(vncResp.Port), vncPath)
	if err != nil {
		logger.Error("dial VNC websocket failed", "error", err)
		h.writeError(conn, "failed to connect to VNC")
		return
	}
	defer pxConn.Close()

	logger.Info("VNC session established")

	// Send connected status with the VNC ticket as the password for noVNC RFB auth.
	connectedMsg := `{"type":"connected","password":` + strconv.Quote(vncResp.Ticket) + `}`
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(connectedMsg))

	// Bidirectional proxy: browser WebSocket ↔ Proxmox WebSocket.
	var wg sync.WaitGroup
	wg.Add(2)

	// Browser → Proxmox: forward all messages.
	go func() {
		defer wg.Done()
		defer closeRelayLeg(pxConn)
		for {
			msgType, msg, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}
			if writeErr := pxConn.WriteMessage(msgType, msg); writeErr != nil {
				return
			}
		}
	}()

	// Proxmox → Browser: forward all messages.
	go func() {
		defer wg.Done()
		defer closeRelayLeg(conn)
		for {
			msgType, msg, readErr := pxConn.ReadMessage()
			if readErr != nil {
				return
			}
			if writeErr := conn.WriteMessage(msgType, msg); writeErr != nil {
				return
			}
		}
	}()

	wg.Wait()
	logger.Info("VNC session closed")
}

// writeError sends a JSON error message to the browser, then a Close frame.
// It does not close the connection: closeBrowserSocket, which the handler
// defers, does that once the handler returns.
func (h *VNCHandler) writeError(conn *fiberWs.Conn, msg string) {
	errMsg := `{"type":"error","message":` + strconv.Quote(msg) + `}`
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(errMsg))
	_ = conn.WriteMessage(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(fiberWs.CloseInternalServerErr, msg))
}

// writeErrorCode is writeError with a machine-readable code the frontend can
// branch on (e.g. "guest_not_running").
func (h *VNCHandler) writeErrorCode(conn *fiberWs.Conn, code, msg string) {
	errMsg := `{"type":"error","code":` + strconv.Quote(code) + `,"message":` + strconv.Quote(msg) + `}`
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(errMsg))
	_ = conn.WriteMessage(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(fiberWs.CloseInternalServerErr, msg))
}

// proxmoxClientFor returns a Proxmox client backed by the shared cache when
// available; falls back to per-call construction otherwise.
func (h *VNCHandler) proxmoxClientFor(ctx context.Context, clusterID uuid.UUID, cluster db.Cluster) (*proxmox.Client, error) {
	if h.cache != nil {
		client, err := h.cache.Get(ctx, clusterID)
		if err == nil {
			return client, nil
		}
		h.logger.Warn("vnc: proxmox cache get failed, building per-call",
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
