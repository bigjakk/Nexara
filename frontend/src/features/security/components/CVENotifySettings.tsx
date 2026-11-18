import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { Loader2 } from "lucide-react";
import { SettingRow, SettingsFooter } from "@/components/page/Settings";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  useCVENotifyConfig,
  useUpdateCVENotifyConfig,
} from "../api/cve-queries";
import { useNotificationChannels } from "@/features/alerts/api/alert-queries";
import { usePermissions } from "@/hooks/usePermissions";

const COOLDOWN_OPTIONS = [
  { label: "Immediately", value: 0 },
  { label: "15 minutes", value: 15 },
  { label: "1 hour", value: 60 },
  { label: "6 hours", value: 360 },
  { label: "1 day", value: 1440 },
] as const;

const settingSelectClass =
  "h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring sm:w-56";

/**
 * The rows of the Scanning section for a cluster's CVE notifications:
 * dispatches a payload via configured notification channels
 * (email/Slack/Discord/webhook/PagerDuty/etc.) when a scan turns up new
 * Act-class — and optionally Attend-class — vulnerabilities. Unlike the
 * schedule above them, these are saved together, by the Save button.
 *
 * Dedup is by CVE-ID set: re-fires when the set changes since last
 * notification, OR when cooldown has elapsed and the set is still nonempty.
 *
 * The save takes manage:cve_scan on the cluster; without it the rows only
 * show the settings, with no Save.
 */
export function CVENotifySettings({
  clusterId,
  canManage,
}: {
  clusterId: string;
  canManage: boolean;
}) {
  const { data: config, isLoading } = useCVENotifyConfig(clusterId);
  const { data: channels } = useNotificationChannels();
  const update = useUpdateCVENotifyConfig();
  // Adding a channel takes manage:notification_channel install-wide.
  const canAddChannel = usePermissions().hasGlobalPermission(
    "manage",
    "notification_channel",
  );
  const readOnly = !canManage;

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
    return (
      <div className="py-3.5">
        <Skeleton className="h-10 w-full" />
      </div>
    );
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
    <>
      <SettingRow
        label="Enable notifications"
        htmlFor="cve-notify-enabled"
        description="Send a notification when a scan finds new vulnerabilities that need action, through your existing notification channels (email, Slack, webhook, PagerDuty and the rest)."
      >
        <Switch
          id="cve-notify-enabled"
          checked={enabled}
          disabled={readOnly}
          onCheckedChange={(checked) => {
            setEnabled(checked);
            setDirty(true);
          }}
        />
      </SettingRow>

      {enabled && (
        <>
          <SettingRow label="Notify on" className="items-start">
            <div className="w-full space-y-2.5 text-sm">
              <label className="flex items-start gap-2.5">
                <Checkbox
                  className="mt-0.5"
                  checked={onAct}
                  disabled={readOnly}
                  onCheckedChange={(checked) => {
                    setOnAct(checked === true);
                    setDirty(true);
                  }}
                />
                <span>
                  <span className="font-semibold">Act</span>
                  <span className="block text-[13px] text-muted-foreground">
                    Actively exploited (KEV), or high likelihood (EPSS≥0.5 and
                    CVSS≥7)
                  </span>
                </span>
              </label>
              <label className="flex items-start gap-2.5">
                <Checkbox
                  className="mt-0.5"
                  checked={onAttend}
                  disabled={readOnly}
                  onCheckedChange={(checked) => {
                    setOnAttend(checked === true);
                    setDirty(true);
                  }}
                />
                <span>
                  <span className="font-semibold">Attend</span>
                  <span className="block text-[13px] text-muted-foreground">
                    Moderate likelihood (EPSS≥0.1), or critical CVSS
                  </span>
                </span>
              </label>
            </div>
          </SettingRow>

          <SettingRow label="Channels" className="items-start">
            {enabledChannels.length === 0 ? (
              <p className="w-full text-[13px] text-muted-foreground">
                No notification channels configured.
                {canAddChannel && (
                  <>
                    {" "}
                    Add one in{" "}
                    <Link className="underline" to="/alerts">
                      Alerts, Channels
                    </Link>{" "}
                    first.
                  </>
                )}
              </p>
            ) : (
              <div className="w-full space-y-2 text-sm">
                {enabledChannels.map((ch) => (
                  <label key={ch.id} className="flex items-center gap-2.5">
                    <Checkbox
                      checked={channelIDs.includes(ch.id)}
                      disabled={readOnly}
                      onCheckedChange={() => {
                        toggleChannel(ch.id);
                      }}
                    />
                    <span className="min-w-0 [overflow-wrap:anywhere]">
                      {ch.name}
                    </span>
                    <span className="text-[12.5px] text-muted-foreground">
                      {ch.channel_type}
                    </span>
                  </label>
                ))}
              </div>
            )}
          </SettingRow>

          <SettingRow
            label="Re-notify after"
            htmlFor="cve-notify-cooldown"
            description="New CVEs always notify immediately. This only holds back a repeat while the same set of CVEs is still present."
          >
            <select
              id="cve-notify-cooldown"
              value={cooldownMinutes}
              disabled={readOnly}
              onChange={(e) => {
                setCooldownMinutes(Number(e.target.value));
                setDirty(true);
              }}
              className={settingSelectClass}
            >
              {COOLDOWN_OPTIONS.map((opt) => (
                <option key={opt.value} value={opt.value}>
                  {opt.label}
                </option>
              ))}
            </select>
          </SettingRow>
        </>
      )}

      {!readOnly && (
        <SettingsFooter>
          {update.isPending && (
            <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
          )}
          {dirty && !update.isPending && (
            <span className="text-[12.5px] text-muted-foreground">
              Notification changes are not saved yet
            </span>
          )}
          {/* Outline: the screen's one primary button is Scan now. */}
          <Button
            variant="outline"
            size="sm"
            onClick={onSave}
            disabled={!canSave}
          >
            Save
          </Button>
        </SettingsFooter>
      )}
    </>
  );
}
