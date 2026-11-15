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

	db "github.com/bigjakk/nexara/internal/db/generated"
	"github.com/bigjakk/nexara/internal/notifications"
)

// Notifier fires notifications when a CVE scan turns up Act-class (or
// optionally Attend-class) vulnerabilities. It dedups by the set of CVE
// IDs at the configured severity: re-firing only when the set changes
// since the last notification, OR when the cooldown has elapsed and the
// set is still non-empty.
type Notifier struct {
	queries *db.Queries
	sender  noticeSender
	logger  *slog.Logger
}

// noticeSender is the alert engine's channel dispatch (notifications.Engine),
// which a CVE notice goes through for the rate limit, retries and dead letter
// an alert's notification gets.
type noticeSender interface {
	SendNotice(ctx context.Context, channelID, clusterID uuid.UUID, payload notifications.AlertPayload) bool
}

// NewNotifier creates a new CVE notifier that sends through sender.
func NewNotifier(queries *db.Queries, sender noticeSender, logger *slog.Logger) *Notifier {
	return &Notifier{
		queries: queries,
		sender:  sender,
		logger:  logger,
	}
}

// MaybeNotify is invoked at the end of a scan. It loads the cluster's CVE
// notification config, decides whether to fire based on the SSVC label
// counts and the dedup state, and dispatches via the registry on a hit.
// All failures are logged but never propagate — notification is best-effort.
func (n *Notifier) MaybeNotify(ctx context.Context, clusterID uuid.UUID, scanID uuid.UUID) {
	if n == nil || n.sender == nil {
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

	// Read channels from the join table — the single source of truth.
	// The legacy cfg.ChannelIds array column was dropped in 4.8c (mig 058).
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

	payload := cveNoticePayload(clusterID, clusterName, signature, vulns)

	dispatched := 0
	for _, channelID := range channelIDs {
		if n.sender.SendNotice(ctx, channelID, clusterID, payload) {
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

// cveNoticePayload is one CVE notice. Its PagerDuty key is its own, the
// cluster and the CVE set: a new CVE opens an incident of its own rather than
// joining the last notice's, and a repeat of the same set, after the cooldown,
// is the same notice again. Its message lists the CVEs, which every channel
// type carries (the chat ones under their own text).
func cveNoticePayload(clusterID uuid.UUID, clusterName, signature string, vulns []db.ListVulnsBySSVCInScanRow) notifications.AlertPayload {
	return notifications.AlertPayload{
		RuleName:     fmt.Sprintf("CVE: action required on %s", clusterName),
		Severity:     "critical",
		State:        "fired",
		Metric:       notifications.MetricCVEActionRequired,
		CurrentValue: float64(len(vulns)),
		ResourceName: clusterName,
		ClusterID:    clusterID.String(),
		Message:      formatVulnList(vulns),
		FiredAt:      time.Now().UTC().Format(time.RFC3339Nano),
		DedupKey:     "nexara-cve-" + clusterID.String() + "-" + signature,
	}
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
// Caps the listing at 20 CVEs to keep payloads sane; the highest risk come
// first, as ListVulnsBySSVCInScan orders them.
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
