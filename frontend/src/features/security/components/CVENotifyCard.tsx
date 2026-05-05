import { useEffect, useState } from "react";
import { Bell, Loader2 } from "lucide-react";
import {
  useCVENotifyConfig,
  useUpdateCVENotifyConfig,
} from "../api/cve-queries";
import { useNotificationChannels } from "@/features/alerts/api/alert-queries";

const COOLDOWN_OPTIONS = [
  { label: "Immediately", value: 0 },
  { label: "15 minutes", value: 15 },
  { label: "1 hour", value: 60 },
  { label: "6 hours", value: 360 },
  { label: "1 day", value: 1440 },
] as const;

/**
 * Per-cluster CVE notification config: dispatches a payload via configured
 * notification channels (email/Slack/Discord/webhook/PagerDuty/etc.) when a
 * scan turns up new Act-class — and optionally Attend-class — vulnerabilities.
 *
 * Dedup is by CVE-ID set: re-fires when the set changes since last
 * notification, OR when cooldown has elapsed and the set is still nonempty.
 */
export function CVENotifyCard({ clusterId }: { clusterId: string }) {
  const { data: config, isLoading } = useCVENotifyConfig(clusterId);
  const { data: channels } = useNotificationChannels();
  const update = useUpdateCVENotifyConfig();

  const [enabled, setEnabled] = useState(false);
  const [onAct, setOnAct] = useState(true);
  const [onAttend, setOnAttend] = useState(false);
  const [channelIDs, setChannelIDs] = useState<string[]>([]);
  const [cooldownMinutes, setCooldownMinutes] = useState(60);
  const [dirty, setDirty] = useState(false);

  useEffect(() => {
    if (!config) return;
    setEnabled(config.enabled);
    setOnAct(config.notify_on_act);
    setOnAttend(config.notify_on_attend);
    setChannelIDs(config.channel_ids);
    setCooldownMinutes(config.cooldown_minutes);
    setDirty(false);
  }, [config]);

  if (isLoading) {
    return <div className="h-24 animate-pulse rounded-lg border bg-card" />;
  }

  const toggleChannel = (id: string) => {
    setChannelIDs((prev) =>
      prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id],
    );
    setDirty(true);
  };

  const canSave =
    dirty &&
    !update.isPending &&
    (!enabled || (channelIDs.length > 0 && (onAct || onAttend)));

  const onSave = () => {
    update.mutate({
      clusterId,
      enabled,
      notify_on_act: onAct,
      notify_on_attend: onAttend,
      channel_ids: channelIDs,
      cooldown_minutes: cooldownMinutes,
    });
  };

  const enabledChannels = (channels ?? []).filter((c) => c.enabled);

  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="mb-3 flex items-center gap-2">
        <Bell className="h-4 w-4 text-muted-foreground" />
        <h3 className="text-sm font-semibold">CVE Notifications</h3>
      </div>
      <p className="mb-4 text-xs text-muted-foreground">
        Send a notification when a scan finds new vulnerabilities requiring
        action. Reuses your existing notification channels (email, Slack,
        webhook, PagerDuty, etc.).
      </p>

      <label className="mb-3 flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={enabled}
          onChange={(e) => {
            setEnabled(e.target.checked);
            setDirty(true);
          }}
          className="h-4 w-4 rounded border"
        />
        <span className="font-medium">Enable notifications</span>
      </label>

      {enabled && (
        <>
          <div className="mb-3 ml-6 space-y-2">
            <p className="text-xs font-medium text-muted-foreground">
              Notify on
            </p>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={onAct}
                onChange={(e) => {
                  setOnAct(e.target.checked);
                  setDirty(true);
                }}
                className="h-4 w-4 rounded border"
              />
              <span>
                <span className="font-semibold text-red-500">Act</span> —
                actively exploited (KEV) or high-likelihood (EPSS≥0.5 + CVSS≥7)
              </span>
            </label>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={onAttend}
                onChange={(e) => {
                  setOnAttend(e.target.checked);
                  setDirty(true);
                }}
                className="h-4 w-4 rounded border"
              />
              <span>
                <span className="font-semibold text-orange-500">Attend</span>{" "}
                — moderate likelihood (EPSS≥0.1) or critical CVSS
              </span>
            </label>
          </div>

          <div className="mb-3 ml-6">
            <p className="mb-1 text-xs font-medium text-muted-foreground">
              Channels
            </p>
            {enabledChannels.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                No notification channels configured. Add one in{" "}
                <a className="underline" href="/alerts">
                  Alerts → Channels
                </a>{" "}
                first.
              </p>
            ) : (
              <div className="space-y-1">
                {enabledChannels.map((ch) => (
                  <label
                    key={ch.id}
                    className="flex items-center gap-2 text-sm"
                  >
                    <input
                      type="checkbox"
                      checked={channelIDs.includes(ch.id)}
                      onChange={() => { toggleChannel(ch.id); }}
                      className="h-4 w-4 rounded border"
                    />
                    <span>{ch.name}</span>
                    <span className="text-xs text-muted-foreground">
                      ({ch.channel_type})
                    </span>
                  </label>
                ))}
              </div>
            )}
          </div>

          <div className="mb-3 ml-6">
            <label className="text-xs font-medium text-muted-foreground">
              Re-notify after
            </label>
            <select
              value={cooldownMinutes}
              onChange={(e) => {
                setCooldownMinutes(Number(e.target.value));
                setDirty(true);
              }}
              className="ml-2 rounded border bg-background px-2 py-1 text-sm"
            >
              {COOLDOWN_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
            <p className="mt-1 text-[11px] text-muted-foreground">
              New CVEs always trigger immediately. Cooldown only gates
              re-notifications when the same set of CVEs is still present.
            </p>
          </div>
        </>
      )}

      <div className="flex items-center justify-end gap-2">
        {update.isPending && (
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        )}
        <button
          onClick={onSave}
          disabled={!canSave}
          className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50"
        >
          Save
        </button>
      </div>
    </div>
  );
}
