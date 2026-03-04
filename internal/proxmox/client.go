package proxmox

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClientConfig holds the configuration for creating a new Proxmox API client.
type ClientConfig struct {
	BaseURL        string
	TokenID        string
	TokenSecret    string
	TLSFingerprint string // SHA-256 fingerprint; empty = use system CA pool.
	Timeout        time.Duration
}

// Client communicates with a single Proxmox VE host.
type Client struct {
	httpClient *http.Client
	baseURL    string
	authHeader string
}

// NewClient creates a Client from the given config.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("proxmox: BaseURL is required")
	}
	if cfg.TokenID == "" {
		return nil, fmt.Errorf("proxmox: TokenID is required")
	}
	if cfg.TokenSecret == "" {
		return nil, fmt.Errorf("proxmox: TokenSecret is required")
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	baseURL := strings.TrimRight(cfg.BaseURL, "/")

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	if cfg.TLSFingerprint != "" {
		expected := strings.ToLower(strings.ReplaceAll(cfg.TLSFingerprint, ":", ""))

		tlsCfg.InsecureSkipVerify = true //nolint:gosec // Custom VerifyPeerCertificate provides fingerprint verification
		tlsCfg.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("proxmox: server presented no certificates")
			}
			actual := formatFingerprint(rawCerts[0])
			if actual != expected {
				return fmt.Errorf("proxmox: TLS fingerprint mismatch: got %s, want %s", actual, expected)
			}
			return nil
		}
	}

	transport := &http.Transport{
		TLSClientConfig: tlsCfg,
		DialContext: (&net.Dialer{
			Timeout:   cfg.Timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	return &Client{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   cfg.Timeout,
		},
		baseURL:    baseURL,
		authHeader: fmt.Sprintf("PVEAPIToken=%s=%s", cfg.TokenID, cfg.TokenSecret),
	}, nil
}

// formatFingerprint returns the lowercase hex SHA-256 digest of a DER-encoded certificate.
func formatFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// do performs an authenticated GET request and unmarshals the response data into dst.
func (c *Client) do(ctx context.Context, path string, dst interface{}) error {
	url := c.baseURL + "/api2/json" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConnectionFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrForbidden
	case resp.StatusCode >= 400:
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body)),
		}
	}

	var envelope response
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidResponse, err)
	}

	if dst != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidResponse, err)
		}
	}

	return nil
}

// doPost performs an authenticated POST request with form-encoded body and unmarshals the response into dst.
func (c *Client) doPost(ctx context.Context, path string, params url.Values, dst interface{}) error {
	apiURL := c.baseURL + "/api2/json" + path

	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader)
	if params != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConnectionFailed, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrForbidden
	case resp.StatusCode >= 400:
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(respBody)),
		}
	}

	var envelope response
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidResponse, err)
	}

	if dst != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidResponse, err)
		}
	}

	return nil
}

// doDelete performs an authenticated DELETE request and unmarshals the response into dst.
func (c *Client) doDelete(ctx context.Context, path string, dst interface{}) error {
	apiURL := c.baseURL + "/api2/json" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConnectionFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return ErrForbidden
	case resp.StatusCode >= 400:
		return &APIError{
			StatusCode: resp.StatusCode,
			Message:    strings.TrimSpace(string(body)),
		}
	}

	var envelope response
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidResponse, err)
	}

	if dst != nil && len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidResponse, err)
		}
	}

	return nil
}

// validateVMID rejects non-positive VM IDs.
func validateVMID(vmid int) error {
	if vmid <= 0 {
		return fmt.Errorf("invalid VMID: %d", vmid)
	}
	return nil
}

// vmStatusAction sends a POST to /nodes/{node}/qemu/{vmid}/status/{action} and returns the UPID.
func (c *Client) vmStatusAction(ctx context.Context, node string, vmid int, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/status/" + action
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("%s VM %d on %s: %w", action, vmid, node, err)
	}
	return upid, nil
}

// StartVM starts a QEMU VM and returns the task UPID.
func (c *Client) StartVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "start")
}

// StopVM forcefully stops a QEMU VM and returns the task UPID.
func (c *Client) StopVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "stop")
}

// ShutdownVM sends an ACPI shutdown to a QEMU VM and returns the task UPID.
func (c *Client) ShutdownVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "shutdown")
}

// RebootVM sends an ACPI reboot to a QEMU VM and returns the task UPID.
func (c *Client) RebootVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "reboot")
}

// ResetVM forcefully resets a QEMU VM and returns the task UPID.
func (c *Client) ResetVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "reset")
}

// SuspendVM suspends a QEMU VM and returns the task UPID.
func (c *Client) SuspendVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "suspend")
}

// ResumeVM resumes a suspended QEMU VM and returns the task UPID.
func (c *Client) ResumeVM(ctx context.Context, node string, vmid int) (string, error) {
	return c.vmStatusAction(ctx, node, vmid, "resume")
}

// CloneVM clones a QEMU VM and returns the task UPID.
func (c *Client) CloneVM(ctx context.Context, node string, vmid int, params CloneParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.NewID <= 0 {
		return "", fmt.Errorf("clone requires a positive newid")
	}

	form := url.Values{}
	form.Set("newid", strconv.Itoa(params.NewID))
	if params.Name != "" {
		form.Set("name", params.Name)
	}
	if params.Target != "" {
		form.Set("target", params.Target)
	}
	if params.Full {
		form.Set("full", "1")
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}

	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid) + "/clone"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("clone VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// DestroyVM deletes a QEMU VM and returns the task UPID.
func (c *Client) DestroyVM(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/qemu/" + strconv.Itoa(vmid)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("destroy VM %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// ctStatusAction sends a POST to /nodes/{node}/lxc/{vmid}/status/{action} and returns the UPID.
func (c *Client) ctStatusAction(ctx context.Context, node string, vmid int, action string) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/status/" + action
	var upid string
	if err := c.doPost(ctx, path, nil, &upid); err != nil {
		return "", fmt.Errorf("%s CT %d on %s: %w", action, vmid, node, err)
	}
	return upid, nil
}

// StartCT starts an LXC container and returns the task UPID.
func (c *Client) StartCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "start")
}

// StopCT forcefully stops an LXC container and returns the task UPID.
func (c *Client) StopCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "stop")
}

// ShutdownCT sends a shutdown signal to an LXC container and returns the task UPID.
func (c *Client) ShutdownCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "shutdown")
}

// RebootCT reboots an LXC container and returns the task UPID.
func (c *Client) RebootCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "reboot")
}

// SuspendCT suspends (freezes) an LXC container and returns the task UPID.
func (c *Client) SuspendCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "suspend")
}

// ResumeCT resumes a suspended LXC container and returns the task UPID.
func (c *Client) ResumeCT(ctx context.Context, node string, vmid int) (string, error) {
	return c.ctStatusAction(ctx, node, vmid, "resume")
}

// CloneCT clones an LXC container and returns the task UPID.
func (c *Client) CloneCT(ctx context.Context, node string, vmid int, params CloneParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.NewID <= 0 {
		return "", fmt.Errorf("clone requires a positive newid")
	}

	form := url.Values{}
	form.Set("newid", strconv.Itoa(params.NewID))
	if params.Name != "" {
		form.Set("hostname", params.Name)
	}
	if params.Target != "" {
		form.Set("target", params.Target)
	}
	if params.Full {
		form.Set("full", "1")
	}
	if params.Storage != "" {
		form.Set("storage", params.Storage)
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/clone"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("clone CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// DestroyCT deletes an LXC container and returns the task UPID.
func (c *Client) DestroyCT(ctx context.Context, node string, vmid int) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid)
	var upid string
	if err := c.doDelete(ctx, path, &upid); err != nil {
		return "", fmt.Errorf("destroy CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// MigrateCT migrates an LXC container to another node and returns the task UPID.
func (c *Client) MigrateCT(ctx context.Context, node string, vmid int, params MigrateParams) (string, error) {
	if err := validateNodeName(node); err != nil {
		return "", err
	}
	if err := validateVMID(vmid); err != nil {
		return "", err
	}
	if params.Target == "" {
		return "", fmt.Errorf("migrate requires a target node")
	}

	form := url.Values{}
	form.Set("target", params.Target)
	if params.Online {
		form.Set("restart", "1")
	}

	path := "/nodes/" + url.PathEscape(node) + "/lxc/" + strconv.Itoa(vmid) + "/migrate"
	var upid string
	if err := c.doPost(ctx, path, form, &upid); err != nil {
		return "", fmt.Errorf("migrate CT %d on %s: %w", vmid, node, err)
	}
	return upid, nil
}

// GetTaskStatus returns the status of an async task by its UPID.
func (c *Client) GetTaskStatus(ctx context.Context, node string, upid string) (*TaskStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	if upid == "" {
		return nil, fmt.Errorf("UPID cannot be empty")
	}
	path := "/nodes/" + url.PathEscape(node) + "/tasks/" + url.PathEscape(upid) + "/status"
	var status TaskStatus
	if err := c.do(ctx, path, &status); err != nil {
		return nil, fmt.Errorf("get task status on %s: %w", node, err)
	}
	return &status, nil
}

// validateNodeName rejects empty names and path traversal attempts.
func validateNodeName(node string) error {
	if node == "" {
		return fmt.Errorf("node name cannot be empty")
	}
	if strings.Contains(node, "/") || strings.Contains(node, "..") {
		return fmt.Errorf("invalid node name: %q", node)
	}
	return nil
}

// GetNodes returns all nodes in the cluster.
func (c *Client) GetNodes(ctx context.Context) ([]NodeListEntry, error) {
	var nodes []NodeListEntry
	if err := c.do(ctx, "/nodes", &nodes); err != nil {
		return nil, fmt.Errorf("get nodes: %w", err)
	}
	return nodes, nil
}

// GetNodeStatus returns the detailed status of a single node.
func (c *Client) GetNodeStatus(ctx context.Context, node string) (*NodeStatus, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var status NodeStatus
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/status", &status); err != nil {
		return nil, fmt.Errorf("get node %s status: %w", node, err)
	}
	return &status, nil
}

// GetVMs returns all QEMU virtual machines on a node.
func (c *Client) GetVMs(ctx context.Context, node string) ([]VirtualMachine, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var vms []VirtualMachine
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/qemu?full=1", &vms); err != nil {
		return nil, fmt.Errorf("get VMs on %s: %w", node, err)
	}
	for i := range vms {
		vms[i].Node = node
	}
	return vms, nil
}

// GetContainers returns all LXC containers on a node.
func (c *Client) GetContainers(ctx context.Context, node string) ([]Container, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var cts []Container
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/lxc?full=1", &cts); err != nil {
		return nil, fmt.Errorf("get containers on %s: %w", node, err)
	}
	for i := range cts {
		cts[i].Node = node
	}
	return cts, nil
}

// GetClusterResources returns resources across the cluster, optionally filtered by type.
// Pass an empty string to get all resource types.
func (c *Client) GetClusterResources(ctx context.Context, resourceType string) ([]ClusterResource, error) {
	path := "/cluster/resources"
	if resourceType != "" {
		q := url.Values{}
		q.Set("type", resourceType)
		path += "?" + q.Encode()
	}
	var resources []ClusterResource
	if err := c.do(ctx, path, &resources); err != nil {
		return nil, fmt.Errorf("get cluster resources: %w", err)
	}
	return resources, nil
}

// GetStoragePools returns all storage pools on a node.
func (c *Client) GetStoragePools(ctx context.Context, node string) ([]StoragePool, error) {
	if err := validateNodeName(node); err != nil {
		return nil, err
	}
	var pools []StoragePool
	if err := c.do(ctx, "/nodes/"+url.PathEscape(node)+"/storage", &pools); err != nil {
		return nil, fmt.Errorf("get storage pools on %s: %w", node, err)
	}
	return pools, nil
}

// GetClusterStatus returns the cluster status including node membership.
func (c *Client) GetClusterStatus(ctx context.Context) ([]ClusterStatusEntry, error) {
	var entries []ClusterStatusEntry
	if err := c.do(ctx, "/cluster/status", &entries); err != nil {
		return nil, fmt.Errorf("get cluster status: %w", err)
	}
	return entries, nil
}
