package proxmox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
)

// NodeTermProxy requests a terminal proxy ticket for a node shell.
func (c *Client) NodeTermProxy(ctx context.Context, node string) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/termproxy"
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("node termproxy on %s: %w", node, err)
	}
	return &resp, nil
}

// VMTermProxy requests a terminal proxy ticket for a QEMU VM serial console.
func (c *Client) VMTermProxy(ctx context.Context, node string, vmid int) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/termproxy"
	params := url.Values{}
	params.Set("serial", "serial0")
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, params, &resp); err != nil {
		return nil, fmt.Errorf("VM %d termproxy on %s: %w", vmid, node, err)
	}
	return &resp, nil
}

// CTTermProxy requests a terminal proxy ticket for an LXC container console.
func (c *Client) CTTermProxy(ctx context.Context, node string, vmid int) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/termproxy"
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, nil, &resp); err != nil {
		return nil, fmt.Errorf("CT %d termproxy on %s: %w", vmid, node, err)
	}
	return &resp, nil
}

// VMVNCProxy requests a VNC proxy ticket for a QEMU VM graphical console.
// Uses websocket=1 so we can connect via the vncwebsocket endpoint.
func (c *Client) VMVNCProxy(ctx context.Context, node string, vmid int) (*TermProxyResponse, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if err := validateVMID(vmid); err != nil {
		return nil, err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/vncproxy"
	params := url.Values{}
	params.Set("websocket", "1")
	var resp TermProxyResponse
	if err := c.doPost(ctx, path, params, &resp); err != nil {
		return nil, fmt.Errorf("VM %d vncproxy on %s: %w", vmid, node, err)
	}
	return &resp, nil
}

// DialVNCWebSocket opens a WebSocket connection to the Proxmox vncwebsocket
// endpoint for VNC console access. Unlike DialTerminal (used for shell/terminal),
// the VNC websocket does not require a ticket+OK handshake — the ticket is passed
// as a URL query parameter and Proxmox handles authentication from that alone.
func (c *Client) DialVNCWebSocket(ctx context.Context, node string, vmid int, vncTicket string, port int) (*websocket.Conn, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}

	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}

	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		parsed.Scheme = "wss"
	}

	parsed.Path = "/api2/json/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/vncwebsocket"
	q := url.Values{}
	q.Set("port", strconv.Itoa(port))
	q.Set("vncticket", vncTicket)
	parsed.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		TLSClientConfig: c.tlsCfg,
	}

	header := http.Header{}
	header.Set("Authorization", c.authHeader)

	conn, resp, err := dialer.DialContext(ctx, parsed.String(), header)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return nil, fmt.Errorf("dial VNC websocket (status %d, body %s): %w",
				resp.StatusCode, string(body), err)
		}
		return nil, fmt.Errorf("dial VNC websocket: %w", err)
	}
	if resp != nil {
		resp.Body.Close()
	}

	return conn, nil
}

// DialTerminal opens a WebSocket connection to the Proxmox vncwebsocket endpoint.
// It returns the gorilla/websocket connection for bidirectional communication.
func (c *Client) DialTerminal(ctx context.Context, node string, vncTicket string, port int) (*websocket.Conn, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}

	// Build the WebSocket URL from the base URL.
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}

	// Switch scheme to wss/ws.
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	default:
		parsed.Scheme = "wss"
	}

	parsed.Path = "/api2/json/nodes/" + url.PathEscape(node) + "/vncwebsocket"
	q := url.Values{}
	q.Set("port", strconv.Itoa(port))
	q.Set("vncticket", vncTicket)
	parsed.RawQuery = q.Encode()

	dialer := websocket.Dialer{
		TLSClientConfig: c.tlsCfg,
	}

	header := http.Header{}
	header.Set("Authorization", c.authHeader)

	conn, resp, err := dialer.DialContext(ctx, parsed.String(), header)
	if err != nil {
		if resp != nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			return nil, fmt.Errorf("dial Proxmox vncwebsocket (status %d, url %s, body %s): %w",
				resp.StatusCode, parsed.String(), string(body), err)
		}
		return nil, fmt.Errorf("dial Proxmox vncwebsocket (url %s): %w", parsed.String(), err)
	}
	if resp != nil {
		resp.Body.Close()
	}

	// Send the vncticket as the first message to authenticate the session.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(vncTicket)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("send vnc ticket: %w", err)
	}

	// Read the OK response.
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read vnc auth response: %w", err)
	}
	if !strings.HasPrefix(string(msg), "OK") {
		conn.Close()
		return nil, fmt.Errorf("vnc auth failed: %s", string(msg))
	}

	return conn, nil
}
