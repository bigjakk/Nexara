package ws

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	gorillaWs "github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5"

	"github.com/proxdash/proxdash/internal/auth"
	"github.com/proxdash/proxdash/internal/crypto"
	db "github.com/proxdash/proxdash/internal/db/generated"
	"github.com/proxdash/proxdash/internal/proxmox"

	fiberWs "github.com/gofiber/contrib/websocket"
	"github.com/google/uuid"
)

// VNCHandler manages VNC proxy connections for graphical VM consoles.
type VNCHandler struct {
	queries       *db.Queries
	encryptionKey string
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

// HandleVNC proxies a browser noVNC session to Proxmox via the vncwebsocket endpoint.
// The flow matches ProxCenter's approach: connect to vncwebsocket with the ticket
// in the URL query params, then bidirectionally forward all WebSocket messages.
func (h *VNCHandler) HandleVNC(conn *fiberWs.Conn) {
	clusterIDStr := conn.Query("cluster_id")
	node := conn.Query("node")
	vmidStr := conn.Query("vmid")

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

	tokenSecret, err := crypto.Decrypt(cluster.TokenSecretEncrypted, h.encryptionKey)
	if err != nil {
		h.writeError(conn, "failed to decrypt cluster credentials")
		return
	}

	pxClient, err := proxmox.NewClient(proxmox.ClientConfig{
		BaseURL:        cluster.ApiUrl,
		TokenID:        cluster.TokenID,
		TokenSecret:    tokenSecret,
		TLSFingerprint: cluster.TlsFingerprint,
		Timeout:        30 * time.Second,
	})
	if err != nil {
		h.writeError(conn, "failed to create Proxmox client")
		return
	}

	// Request VNC proxy ticket.
	vncResp, err := pxClient.VMVNCProxy(ctx, node, vmid)
	if err != nil {
		logger.Error("vncproxy request failed", "error", err)
		h.writeError(conn, "failed to create VNC session")
		return
	}

	// Connect to Proxmox vncwebsocket endpoint (ticket in URL query params).
	pxConn, err := pxClient.DialVNCWebSocket(ctx, node, vmid, vncResp.Ticket, int(vncResp.Port))
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
		defer pxConn.WriteMessage(gorillaWs.CloseMessage,
			gorillaWs.FormatCloseMessage(gorillaWs.CloseNormalClosure, ""))
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
		defer conn.WriteMessage(fiberWs.CloseMessage,
			fiberWs.FormatCloseMessage(fiberWs.CloseNormalClosure, ""))
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

// writeError sends a JSON error message to the browser and closes the connection.
func (h *VNCHandler) writeError(conn *fiberWs.Conn, msg string) {
	errMsg := `{"type":"error","message":` + strconv.Quote(msg) + `}`
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(errMsg))
	_ = conn.WriteMessage(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(fiberWs.CloseInternalServerErr, msg))
}
