package ws

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strconv"
	"strings"
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

// HandleVNC proxies a browser noVNC session to Proxmox vncwebsocket.
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

	// Dial the Proxmox vncwebsocket.
	pxConn, err := pxClient.DialTerminal(ctx, node, vncResp.Ticket, vncResp.Port)
	if err != nil {
		logger.Error("dial VNC terminal failed", "error", err)
		h.writeError(conn, "failed to connect to VNC")
		return
	}
	defer pxConn.Close()

	logger.Info("VNC session established")

	// Send a connected status to the browser.
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(`{"type":"connected"}`))

	// Bidirectional proxy with protocol translation.
	var wg sync.WaitGroup
	wg.Add(2)

	// Browser → Proxmox: binary frames from noVNC → base64 encode → channel 0 text.
	go func() {
		defer wg.Done()
		defer pxConn.WriteMessage(gorillaWs.CloseMessage,
			gorillaWs.FormatCloseMessage(gorillaWs.CloseNormalClosure, ""))
		for {
			msgType, msg, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}

			if msgType == fiberWs.BinaryMessage || msgType == fiberWs.TextMessage {
				encoded := base64.StdEncoding.EncodeToString(msg)
				dataMsg := "0:" + encoded + "\n"
				if writeErr := pxConn.WriteMessage(gorillaWs.TextMessage, []byte(dataMsg)); writeErr != nil {
					return
				}
			}
		}
	}()

	// Proxmox → Browser: channel 0 base64 text → decode → binary frames for noVNC.
	go func() {
		defer wg.Done()
		defer conn.WriteMessage(fiberWs.CloseMessage,
			fiberWs.FormatCloseMessage(fiberWs.CloseNormalClosure, ""))
		for {
			_, msg, readErr := pxConn.ReadMessage()
			if readErr != nil {
				return
			}

			text := string(msg)
			if strings.HasPrefix(text, "0:") {
				data := strings.TrimPrefix(text, "0:")
				data = strings.TrimSuffix(data, "\n")
				decoded, decErr := base64.StdEncoding.DecodeString(data)
				if decErr != nil {
					continue
				}
				if writeErr := conn.WriteMessage(fiberWs.BinaryMessage, decoded); writeErr != nil {
					return
				}
			}
			// Ignore non-zero channels.
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
