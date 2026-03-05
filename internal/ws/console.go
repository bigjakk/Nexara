package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// ConsoleHandler manages terminal proxy connections.
type ConsoleHandler struct {
	queries       *db.Queries
	encryptionKey string
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

// consoleResizeMsg is sent by the browser when the terminal is resized.
type consoleResizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// HandleConsole proxies a browser terminal session to Proxmox vncwebsocket.
func (h *ConsoleHandler) HandleConsole(conn *fiberWs.Conn) {
	clusterIDStr := conn.Query("cluster_id")
	node := conn.Query("node")
	consoleType := conn.Query("type")
	vmidStr := conn.Query("vmid")

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

	if consoleType == "" {
		consoleType = "node_shell"
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

	// Call the appropriate vncproxy endpoint.
	// We use vncproxy (not termproxy) because termproxy ticket validation
	// does not work with API tokens (Proxmox bug #6079).
	var vncResp *proxmox.TermProxyResponse
	var vncPath string // resource path for vncwebsocket URL
	switch consoleType {
	case "node_shell":
		vncResp, err = pxClient.NodeVNCProxy(ctx, node)
		// node-level: no extra path
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
		logger.Error("vncproxy request failed", "error", err)
		h.writeError(conn, "failed to create console session")
		return
	}

	logger.Info("vncproxy response",
		"port", int(vncResp.Port),
		"user", vncResp.User,
		"upid", vncResp.UPID,
		"vncPath", vncPath,
		"ticket_len", len(vncResp.Ticket),
	)

	// Dial the Proxmox vncwebsocket — no handshake needed with vncproxy.
	// The ticket is passed as a query parameter and API token authenticates the upgrade.
	pxConn, err := pxClient.DialVNCWebSocket(ctx, node, vncResp.Ticket, int(vncResp.Port), vncPath)
	if err != nil {
		logger.Error("dial vncwebsocket failed", "error", err)
		h.writeError(conn, "failed to connect to console")
		return
	}
	defer pxConn.Close()

	logger.Info("console session established")

	// Send a connected status to the browser.
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(`{"type":"connected"}`))

	// Bidirectional raw proxy — vncproxy uses raw data (no channel encoding).
	// Resize is still sent as JSON from the browser; we ignore it for vncproxy
	// since VNC handles its own framebuffer sizing.
	var wg sync.WaitGroup
	wg.Add(2)

	// Browser → Proxmox
	go func() {
		defer wg.Done()
		defer pxConn.WriteMessage(gorillaWs.CloseMessage,
			gorillaWs.FormatCloseMessage(gorillaWs.CloseNormalClosure, ""))
		for {
			msgType, msg, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}

			switch msgType {
			case fiberWs.TextMessage:
				// Check if it's a resize message — skip for vncproxy.
				var resize consoleResizeMsg
				if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
					continue
				}
				// Terminal input — relay raw.
				if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, msg); writeErr != nil {
					return
				}
			case fiberWs.BinaryMessage:
				// Raw binary input — relay directly.
				if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, msg); writeErr != nil {
					return
				}
			}
		}
	}()

	// Proxmox → Browser
	go func() {
		defer wg.Done()
		defer conn.WriteMessage(fiberWs.CloseMessage,
			fiberWs.FormatCloseMessage(fiberWs.CloseNormalClosure, ""))
		for {
			msgType, msg, readErr := pxConn.ReadMessage()
			if readErr != nil {
				logger.Debug("proxmox read error", "error", readErr)
				return
			}
			logger.Debug("proxmox data", "type", msgType, "len", len(msg), "preview", string(msg[:min(len(msg), 64)]))

			// Relay raw data to browser.
			if writeErr := conn.WriteMessage(fiberWs.BinaryMessage, msg); writeErr != nil {
				return
			}
		}
	}()

	wg.Wait()
	logger.Info("console session closed")
}

// writeError sends a JSON error message to the browser and closes the connection.
func (h *ConsoleHandler) writeError(conn *fiberWs.Conn, msg string) {
	errMsg := fmt.Sprintf(`{"type":"error","message":%q}`, msg)
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(errMsg))
	_ = conn.WriteMessage(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(fiberWs.CloseInternalServerErr, msg))
}
