package scanner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bigjakk/nexara/internal/crypto"
	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/notifications"
)

// Notifier fires notifications when a CVE scan turns up Act-class (or
// optionally Attend-class) vulnerabilities. It dedups by the set of CVE
// IDs at the configured severity: re-firing only when the set changes
// since the last notification, OR when the cooldown has elapsed and the
// set is still non-empty.
//
// The encryption key is needed to decrypt notification_channels.config_encrypted;
// it's set lazily by SetEncryptionKey to avoid leaking it through the
// notifier's public constructor (the scan engine already owns it).
type Notifier struct {
	queries       *db.Queries
	registry      *notifications.Registry
	logger        *slog.Logger
	encryptionKey string
}

// NewNotifier creates a new CVE notifier. The encryption key must be set
// via SetEncryptionKey before dispatching, otherwise channel configs cannot
// be decrypted.
func NewNotifier(queries *db.Queries, registry *notifications.Registry, logger *slog.Logger) *Notifier {
	return &Notifier{
		queries:  queries,
		registry: registry,
		logger:   logger,
	}
}

// SetEncryptionKey sets the AES-256-GCM key used to decrypt notification
// channel configs. The scanner Engine already holds the key and passes it in
// before each scan.
func (n *Notifier) SetEncryptionKey(key string) {
	n.encryptionKey = key
}

// MaybeNotify is invoked at the end of a scan. It loads the cluster's CVE
// notification config, decides whether to fire based on the SSVC label
// counts and the dedup state, and dispatches via the registry on a hit.
// All failures are logged but never propagate — notification is best-effort.
func (n *Notifier) MaybeNotify(ctx context.Context, clusterID uuid.UUID, scanID uuid.UUID) {
	if n == nil || n.registry == nil {
		return
	}

	defer func() {
		if r := recover(); r != nil {
			n.logger.Error("CVE notifier panicked", "panic", r, "cluster_id", clusterID)
		}
	}()

	cfg, err := n.queries.GetCVENotificationConfig(ctx, clusterID)
	if err != nil {
		// No config = feature disabled for this cluster.
		return
	}
	if !cfg.Enabled {
		return
	}

	// Read channels from the join table (4.8b read-flip). The legacy
	// cfg.ChannelIds array is still dual-written by the handler but is no
	// longer authoritative — when a notification_channel is deleted the FK
	// cascade clears the join row but leaves the array referencing the
	// stale UUID. Reading from the join table gives us the live set.
	channelIDs, err := n.queries.ListCVENotificationConfigChannels(ctx, clusterID)
	if err != nil {
		n.logger.Warn("failed to list cve notification channels", "cluster_id", clusterID, "error", err)
		return
	}
	if len(channelIDs) == 0 {
		return
	}

	labels := []string{}
	if cfg.NotifyOnAct {
		labels = append(labels, SSVCAct)
	}
	if cfg.NotifyOnAttend {
		labels = append(labels, SSVCAttend)
	}
	if len(labels) == 0 {
		return
	}

	vulns, err := n.queries.ListVulnsBySSVCInScan(ctx, db.ListVulnsBySSVCInScanParams{
		ScanID:  scanID,
		Column2: labels,
	})
	if err != nil {
		n.logger.Warn("failed to list SSVC vulns for notification", "scan_id", scanID, "error", err)
		return
	}

	if len(vulns) == 0 {
		return
	}

	signature := vulnSetSignature(vulns)
	cooldownExpired := !cfg.LastNotifiedAt.Valid ||
		time.Since(cfg.LastNotifiedAt.Time) >= time.Duration(cfg.CooldownMinutes)*time.Minute
	signatureChanged := signature != cfg.LastNotifiedSignature

	if !signatureChanged && !cooldownExpired {
		return
	}

	cluster, err := n.queries.GetCluster(ctx, clusterID)
	clusterName := clusterID.String()
	if err == nil {
		clusterName = cluster.Name
	}

	message := formatVulnList(vulns)
	payload := notifications.AlertPayload{
		RuleName:     fmt.Sprintf("CVE: action required on %s", clusterName),
		Severity:     "critical",
		State:        "fired",
		Metric:       "cve_action_required",
		CurrentValue: float64(len(vulns)),
		ResourceName: clusterName,
		ClusterID:    clusterID.String(),
		Message:      message,
		FiredAt:      time.Now().UTC().Format(time.RFC3339),
	}

	dispatched := 0
	for _, channelID := range channelIDs {
		if n.dispatch(ctx, channelID, payload) {
			dispatched++
		}
	}
	if dispatched == 0 {
		n.logger.Warn("CVE notification: no channels accepted dispatch",
			"cluster_id", clusterID, "vuln_count", len(vulns))
		return
	}

	if err := n.queries.UpdateCVENotificationSent(ctx, db.UpdateCVENotificationSentParams{
		ClusterID:             clusterID,
		LastNotifiedSignature: signature,
	}); err != nil {
		n.logger.Warn("failed to update notification dedup state",
			"cluster_id", clusterID, "error", err)
	}

	n.logger.Info("CVE notification dispatched",
		"cluster_id", clusterID,
		"channels", dispatched,
		"vulns", len(vulns),
		"signature_changed", signatureChanged,
		"cooldown_expired", cooldownExpired,
	)
}

func (n *Notifier) dispatch(ctx context.Context, channelID uuid.UUID, payload notifications.AlertPayload) bool {
	if channelID == uuid.Nil {
		return false
	}

	channel, err := n.queries.GetNotificationChannelEnabled(ctx, channelID)
	if err != nil {
		n.logger.Warn("CVE notification: channel not found or disabled",
			"channel_id", channelID, "error", err)
		return false
	}

	configJSON, err := crypto.Decrypt(channel.ConfigEncrypted, n.encryptionKey)
	if err != nil {
		n.logger.Error("CVE notification: failed to decrypt channel config",
			"channel_id", channelID, "error", err)
		return false
	}

	dispatcher, ok := n.registry.Get(channel.ChannelType)
	if !ok {
		n.logger.Warn("CVE notification: no dispatcher for channel type",
			"channel_id", channelID, "type", channel.ChannelType)
		return false
	}

	if err := dispatcher.Send(ctx, []byte(configJSON), payload); err != nil {
		n.logger.Warn("CVE notification: dispatch failed",
			"channel_id", channelID, "type", channel.ChannelType, "error", err)
		return false
	}
	return true
}

// vulnSetSignature returns a stable hash of the CVE-ID set so we can
// detect "this is a new set of CVEs vs. the last time we notified".
// Sorted before hashing so order doesn't matter.
func vulnSetSignature(vulns []db.ListVulnsBySSVCInScanRow) string {
	ids := make([]string, len(vulns))
	for i, v := range vulns {
		ids[i] = v.CveID
	}
	sort.Strings(ids)
	h := sha256.Sum256([]byte(strings.Join(ids, ",")))
	return hex.EncodeToString(h[:8]) // 16 hex chars is plenty for dedup
}

// formatVulnList builds a human-readable message for the notification body.
// Caps the listing at 20 CVEs to keep payloads sane.
func formatVulnList(vulns []db.ListVulnsBySSVCInScanRow) string {
	const maxList = 20
	if len(vulns) == 0 {
		return ""
	}
	parts := make([]string, 0, len(vulns))
	for i, v := range vulns {
		if i >= maxList {
			parts = append(parts, fmt.Sprintf("…and %d more", len(vulns)-maxList))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s, risk %.1f)", v.CveID, v.PackageName, v.RiskScore))
	}
	return strings.Join(parts, "\n")
}
