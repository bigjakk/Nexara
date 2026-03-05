package proxmox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxResponseSize caps how much data we read from a Proxmox API response (50 MB).
const maxResponseSize = 50 * 1024 * 1024

// apiClient provides shared HTTP infrastructure for Proxmox API clients.
// Both PVE (Client) and PBS (PBSClient) embed this struct.
type apiClient struct {
	httpClient *http.Client
	baseURL    string
	authHeader string
	tokenID    string // e.g. "user@pam!tokenname" — needed for terminal handshake
	tlsCfg     *tls.Config
}

// newAPIClient creates an apiClient with TLS and auth configured.
// authPrefix is "PVEAPIToken" for PVE or "PBSAPIToken" for PBS.
func newAPIClient(cfg ClientConfig, authPrefix string) (*apiClient, error) {
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

	return &apiClient{
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   cfg.Timeout,
		},
		baseURL:    baseURL,
		authHeader: fmt.Sprintf("%s=%s=%s", authPrefix, cfg.TokenID, cfg.TokenSecret),
		tokenID:    cfg.TokenID,
		tlsCfg:     tlsCfg,
	}, nil
}

// formatFingerprint returns the lowercase hex SHA-256 digest of a DER-encoded certificate.
func formatFingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// do performs an authenticated GET request and unmarshals the response data into dst.
func (a *apiClient) do(ctx context.Context, path string, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", a.authHeader)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConnectionFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if err := checkStatus(resp.StatusCode, body); err != nil {
		return err
	}

	return unmarshalData(body, dst)
}

// doPost performs an authenticated POST request with form-encoded body.
func (a *apiClient) doPost(ctx context.Context, path string, params url.Values, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", a.authHeader)
	if params != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConnectionFailed, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if err := checkStatus(resp.StatusCode, respBody); err != nil {
		return err
	}

	return unmarshalData(respBody, dst)
}

// doDelete performs an authenticated DELETE request.
func (a *apiClient) doDelete(ctx context.Context, path string, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, apiURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", a.authHeader)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConnectionFailed, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if err := checkStatus(resp.StatusCode, body); err != nil {
		return err
	}

	return unmarshalData(body, dst)
}

// doPut performs an authenticated PUT request with form-encoded body.
func (a *apiClient) doPut(ctx context.Context, path string, params url.Values, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	var body io.Reader
	if params != nil {
		body = strings.NewReader(params.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", a.authHeader)
	if params != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConnectionFailed, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if err := checkStatus(resp.StatusCode, respBody); err != nil {
		return err
	}

	return unmarshalData(respBody, dst)
}

// doMultipart performs an authenticated POST request with multipart form data.
// It streams the file directly to Proxmox without buffering the entire file in
// memory or on disk. Content-Length is pre-computed from the multipart framing
// overhead + the known file size (Proxmox rejects chunked transfer encoding).
func (a *apiClient) doMultipart(ctx context.Context, path string, fields map[string]string, fileField, fileName string, fileReader io.Reader, fileSize int64, dst interface{}) error {
	apiURL := a.baseURL + "/api2/json" + path

	// Build the multipart framing (headers/footers) into a buffer,
	// substituting a placeholder for the actual file content.
	// This lets us compute Content-Length without reading the file.
	var prefix bytes.Buffer
	writer := multipart.NewWriter(&prefix)

	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			return fmt.Errorf("write field %s: %w", k, err)
		}
	}
	// CreateFormFile writes the part header; we capture it in prefix.
	if _, err := writer.CreateFormFile(fileField, fileName); err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	// prefix now contains: all field parts + the file part header.
	// Save prefix bytes before closing writer (which adds the closing boundary).
	prefixBytes := make([]byte, prefix.Len())
	copy(prefixBytes, prefix.Bytes())

	// Close writes the closing boundary.
	contentType := writer.FormDataContentType()
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close multipart writer: %w", err)
	}

	// suffix = everything after the file part header (closing boundary).
	suffix := prefix.Bytes()[len(prefixBytes):]

	totalSize := int64(len(prefixBytes)) + fileSize + int64(len(suffix))
	body := io.MultiReader(
		bytes.NewReader(prefixBytes),
		fileReader,
		bytes.NewReader(suffix),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", a.authHeader)
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = totalSize

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrConnectionFailed, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if err := checkStatus(resp.StatusCode, respBody); err != nil {
		return err
	}

	return unmarshalData(respBody, dst)
}

// checkStatus maps HTTP status codes to typed errors.
func checkStatus(statusCode int, body []byte) error {
	switch {
	case statusCode == http.StatusNotFound:
		return ErrNotFound
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return ErrForbidden
	case statusCode >= 400:
		return &APIError{
			StatusCode: statusCode,
			Message:    strings.TrimSpace(string(body)),
		}
	}
	return nil
}

// unmarshalData unmarshals the standard Proxmox API response envelope.
func unmarshalData(body []byte, dst interface{}) error {
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
