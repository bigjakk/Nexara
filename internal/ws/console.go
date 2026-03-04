package ws

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
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

	// Call the appropriate termproxy endpoint.
	var termResp *proxmox.TermProxyResponse
	switch consoleType {
	case "node_shell":
		termResp, err = pxClient.NodeTermProxy(ctx, node)
	case "vm_serial":
		vmid, parseErr := strconv.Atoi(vmidStr)
		if parseErr != nil {
			h.writeError(conn, "invalid vmid")
			return
		}
		termResp, err = pxClient.VMTermProxy(ctx, node, vmid)
	case "ct_attach":
		vmid, parseErr := strconv.Atoi(vmidStr)
		if parseErr != nil {
			h.writeError(conn, "invalid vmid")
			return
		}
		termResp, err = pxClient.CTTermProxy(ctx, node, vmid)
	default:
		h.writeError(conn, "invalid console type")
		return
	}
	if err != nil {
		logger.Error("termproxy request failed", "error", err)
		h.writeError(conn, "failed to create terminal session")
		return
	}

	// Dial the Proxmox vncwebsocket.
	pxConn, err := pxClient.DialTerminal(ctx, node, termResp.Ticket, termResp.Port)
	if err != nil {
		logger.Error("dial terminal failed", "error", err)
		h.writeError(conn, "failed to connect to terminal")
		return
	}
	defer pxConn.Close()

	logger.Info("console session established")

	// Send a connected status to the browser.
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(`{"type":"connected"}`))

	// Bidirectional proxy with protocol translation.
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
				// Check if it's a resize message.
				var resize consoleResizeMsg
				if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
					// Send resize as Proxmox channel 1: "1:<cols>:<rows>:\n"
					resizeMsg := fmt.Sprintf("1:%d:%d:\n", resize.Cols, resize.Rows)
					if writeErr := pxConn.WriteMessage(gorillaWs.TextMessage, []byte(resizeMsg)); writeErr != nil {
						return
					}
					continue
				}
				// Otherwise treat as terminal input — encode as channel 0.
				encoded := base64.StdEncoding.EncodeToString(msg)
				dataMsg := "0:" + encoded + "\n"
				if writeErr := pxConn.WriteMessage(gorillaWs.TextMessage, []byte(dataMsg)); writeErr != nil {
					return
				}
			case fiberWs.BinaryMessage:
				// Raw binary input — encode as channel 0.
				encoded := base64.StdEncoding.EncodeToString(msg)
				dataMsg := "0:" + encoded + "\n"
				if writeErr := pxConn.WriteMessage(gorillaWs.TextMessage, []byte(dataMsg)); writeErr != nil {
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
			_, msg, readErr := pxConn.ReadMessage()
			if readErr != nil {
				return
			}

			text := string(msg)
			// Proxmox sends channel-prefixed messages: "0:<base64data>\n"
			if strings.HasPrefix(text, "0:") {
				// Strip the "0:" prefix and trailing newline.
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
			// Ignore other channels (resize acks, etc.)
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
