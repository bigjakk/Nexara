package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	db "github.com/bigjakk/nexara/internal/db/generated"
)

const (
	// expoPushURL is Expo's hosted push notification gateway. It fans out
	// to FCM (Android) and APNs (iOS) so we don't need to manage either
	// platform's credentials directly.
	expoPushURL = "https://exp.host/--/api/v2/push/send"

	// expoPushBatchSize is the maximum number of messages Expo accepts per
	// HTTP request.
	expoPushBatchSize = 100

	// expoPushTimeout caps individual HTTP calls to Expo. They're usually
	// fast (sub-second) but the timeout protects against the gateway
	// hanging.
	expoPushTimeout = 15 * time.Second

	// maxDevicesPerDispatch caps fan-out per alert as a defense-in-depth
	// against the per-user device cap being bypassed (security review M2).
	// Mirrors `handlers.maxDevicesPerUser`.
	maxDevicesPerDispatch = 20

	// maxExpoResponseBytes limits how much data we'll decode from a push
	// gateway response, to prevent OOM if the gateway misbehaves
	// (security review L1). 256 KiB is plenty for 100 tickets.
	maxExpoResponseBytes = 256 * 1024
)

// expoPushConfig is the channel-config payload stored encrypted in the DB.
// Today the dispatcher only supports per-user routing — given a user UUID
// it fans out to every device that user has registered. Multi-user / role
// routing can be added later by extending this struct.
type expoPushConfig struct {
	UserID string `json:"user_id"`
}

// ExpoPushDispatcher delivers an alert to every mobile device a user has
// registered. It implements the `Dispatcher` interface registered in the
// notifications.Registry.
//
// The dispatcher is constructed with a `*db.Queries` so it can look up the
// recipient's devices and prune invalid push tokens reported by Expo.
type ExpoPushDispatcher struct {
	queries *db.Queries
}

// NewExpoPushDispatcher creates a new dispatcher backed by the given query
// set. The same queries instance must be shared with the rest of the API
// server (it's just the sqlc-generated wrapper).
func NewExpoPushDispatcher(queries *db.Queries) *ExpoPushDispatcher {
	return &ExpoPushDispatcher{queries: queries}
}

// Type returns the channel type identifier used by the registry. The web
// frontend's ChannelForm and the alerts.go validChannelTypes map must
// reference this same string.
func (d *ExpoPushDispatcher) Type() string { return "expo_push" }

// Send delivers the alert payload to every device the configured user has
// registered. Returns an error if the user has no devices, the config is
// invalid, or the push gateway can't be reached.
//
// Per-device delivery failures (e.g. one expired token among many devices)
// are handled by pruning the offending row but do not fail the call.
func (d *ExpoPushDispatcher) Send(ctx context.Context, config json.RawMessage, payload AlertPayload) error {
	var cfg expoPushConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("parse expo_push config: %w", err)
	}
	if cfg.UserID == "" {
		return fmt.Errorf("expo_push config: user_id required")
	}
	userID, err := uuid.Parse(cfg.UserID)
	if err != nil {
		return fmt.Errorf("expo_push config: user_id must be a UUID")
	}

	if d.queries == nil {
		return fmt.Errorf("expo_push: dispatcher has no DB handle")
	}

	devices, err := d.queries.ListMobileDevicesByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("list devices for user: %w", err)
	}
	if len(devices) == 0 {
		return fmt.Errorf("expo_push: user %s has no registered devices", cfg.UserID)
	}
	if len(devices) > maxDevicesPerDispatch {
		// Defense in depth: if somehow more than the per-user cap exists
		// in the table, only fan out to the most-recently-seen subset.
		// `ListMobileDevicesByUser` orders by last_seen_at DESC.
		devices = devices[:maxDevicesPerDispatch]
	}

	title := payload.RuleName
	if title == "" {
		title = strings.ToUpper(payload.Severity) + " alert"
	}
	body := defaultMessage(payload)

	// Tap-handler payload — the mobile app routes to /(app)/alerts/:id
	// when a user taps the notification.
	data := map[string]any{
		"alert_id":      payload.RuleID,
		"cluster_id":    payload.ClusterID,
		"severity":      payload.Severity,
		"resource_name": payload.ResourceName,
	}

	priority := "default"
	switch strings.ToLower(payload.Severity) {
	case "critical", "warning":
		priority = "high"
	}

	// Build one Expo message per device. Expo accepts up to 100 messages
	// in a single HTTP call.
	messages := make([]expoMessage, 0, len(devices))
	for _, dev := range devices {
		messages = append(messages, expoMessage{
			To:       dev.ExpoPushToken,
			Title:    title,
			Body:     body,
			Sound:    "default",
			Priority: priority,
			Data:     data,
		})
	}

	for i := 0; i < len(messages); i += expoPushBatchSize {
		end := i + expoPushBatchSize
		if end > len(messages) {
			end = len(messages)
		}
		batch := messages[i:end]
		resp, err := d.sendBatch(ctx, batch)
		if err != nil {
			return fmt.Errorf("send batch %d-%d: %w", i, end, err)
		}
		// Prune any tokens Expo rejected as invalid. We do this best-effort:
		// errors here are logged-and-ignored at the dispatcher boundary
		// because the alert was still delivered to the other devices.
		d.pruneInvalidTokens(ctx, batch, resp)
	}

	return nil
}

// expoMessage matches Expo's request schema for a single push notification.
// See https://docs.expo.dev/push-notifications/sending-notifications/
type expoMessage struct {
	To       string         `json:"to"`
	Title    string         `json:"title"`
	Body     string         `json:"body"`
	Sound    string         `json:"sound,omitempty"`
	Priority string         `json:"priority,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// expoTicket is one element of Expo's push response array. The status is
// either "ok" or "error"; on error the details object may include a code
// like "DeviceNotRegistered" which we use to prune the row.
type expoTicket struct {
	Status  string `json:"status"`
	ID      string `json:"id,omitempty"`
	Message string `json:"message,omitempty"`
	Details struct {
		Error string `json:"error,omitempty"`
	} `json:"details,omitempty"`
}

type expoPushResponse struct {
	Data []expoTicket `json:"data"`
}

// sendBatch posts a slice of messages to the Expo Push gateway and returns
// the parsed response so the caller can prune invalid tokens.
//
// Note: we deliberately do NOT call `validateExternalURL` here because the
// target URL is a hardcoded constant — running it through SSRF validation
// would only succeed-or-misleadingly-fail and provide a false sense of
// security. The real defense is `SafeHTTPClient`, which controls
// `DialContext` to block private IPs and refuses redirects (security
// review M4).
func (d *ExpoPushDispatcher) sendBatch(ctx context.Context, batch []expoMessage) (*expoPushResponse, error) {
	jsonBytes, err := json.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("marshal batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, expoPushURL, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "gzip, deflate")

	client := SafeHTTPClient(expoPushTimeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("expo gateway returned %d", resp.StatusCode)
	}

	// Bound the body size we'll decode (security review L1).
	body := io.LimitReader(resp.Body, maxExpoResponseBytes)

	var parsed expoPushResponse
	if err := json.NewDecoder(body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &parsed, nil
}

// pruneInvalidTokens removes any device rows whose token Expo reports as
// invalid (DeviceNotRegistered, InvalidCredentials, etc.). Errors are
// swallowed since the alert was already delivered to the other devices.
//
// Defense against MITM mass-prune (security review M1): if more than half
// of the batch comes back as errors, we treat the response as suspicious
// and skip pruning entirely. A normal Expo response has very few errors
// per batch — bursts of errors usually indicate a downstream problem
// (gateway outage) or a tampered response.
func (d *ExpoPushDispatcher) pruneInvalidTokens(ctx context.Context, batch []expoMessage, resp *expoPushResponse) {
	if resp == nil || len(resp.Data) != len(batch) {
		return
	}

	errCount := 0
	for _, t := range resp.Data {
		if t.Status == "error" {
			errCount++
		}
	}
	if errCount*2 > len(batch) {
		// Suspicious — Expo doesn't normally fail this many at once.
		// Skip pruning to avoid mass-deleting tokens on a tampered or
		// degraded response.
		return
	}

	for i, ticket := range resp.Data {
		if ticket.Status != "error" {
			continue
		}
		if ticket.Details.Error == "DeviceNotRegistered" {
			_ = d.queries.DeleteMobileDeviceByExpoToken(ctx, batch[i].To)
		}
	}
}
