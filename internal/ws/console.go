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
		h.writeError(conn, "failed to create console session")
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
					// termproxy: prefix with "0:" data channel.
					prefixed := append([]byte("0:"), msg...)
					if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, prefixed); writeErr != nil {
						return
					}
				} else {
					if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, msg); writeErr != nil {
						return
					}
				}
			case fiberWs.BinaryMessage:
				if useTermProxy {
					// termproxy: prefix with "0:" data channel.
					prefixed := append([]byte("0:"), msg...)
					if writeErr := pxConn.WriteMessage(gorillaWs.BinaryMessage, prefixed); writeErr != nil {
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
		defer conn.WriteMessage(fiberWs.CloseMessage,
			fiberWs.FormatCloseMessage(fiberWs.CloseNormalClosure, ""))
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

// writeError sends a JSON error message to the browser and closes the connection.
func (h *ConsoleHandler) writeError(conn *fiberWs.Conn, msg string) {
	errMsg := fmt.Sprintf(`{"type":"error","message":%q}`, msg)
	_ = conn.WriteMessage(fiberWs.TextMessage, []byte(errMsg))
	_ = conn.WriteMessage(fiberWs.CloseMessage,
		fiberWs.FormatCloseMessage(fiberWs.CloseInternalServerErr, msg))
}
