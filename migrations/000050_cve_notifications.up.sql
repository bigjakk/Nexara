-- 000050_cve_notifications.up.sql
-- Per-cluster notification config for CVE scans. Reuses the existing
-- notification_channels table (the same Slack/Email/PagerDuty/etc.
-- destinations alerts and reports already use).
--
-- Trigger semantics: option A — fire when the set of CVEs at the
-- configured severity has changed since last notification, OR when
-- cooldown has elapsed and the set is still nonempty.

CREATE TABLE cve_notification_configs (
    cluster_id              UUID PRIMARY KEY REFERENCES clusters(id) ON DELETE CASCADE,
    enabled                 BOOLEAN NOT NULL DEFAULT FALSE,
    notify_on_act           BOOLEAN NOT NULL DEFAULT TRUE,
    notify_on_attend        BOOLEAN NOT NULL DEFAULT FALSE,
    channel_ids             UUID[] NOT NULL DEFAULT ARRAY[]::UUID[],
    cooldown_minutes        INT NOT NULL DEFAULT 60,
    last_notified_at        TIMESTAMPTZ,
    last_notified_signature TEXT NOT NULL DEFAULT '',
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (cooldown_minutes >= 0 AND cooldown_minutes <= 10080) -- max 1 week
);
