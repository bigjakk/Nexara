import { Loader2 } from "lucide-react";
import { SettingRow } from "@/components/page/Settings";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import {
  useCVEScanSchedule,
  useUpdateCVEScanSchedule,
} from "../api/cve-queries";

const INTERVAL_OPTIONS = [
  { label: "Every hour", value: 1 },
  { label: "Every 6 hours", value: 6 },
  { label: "Every 12 hours", value: 12 },
  { label: "Every 24 hours", value: 24 },
  { label: "Every 48 hours", value: 48 },
  { label: "Weekly", value: 168 },
] as const;

const settingSelectClass =
  "h-9 w-full rounded-md border border-input bg-transparent px-3 text-sm transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring sm:w-56";

/**
 * The rows of the Scanning section that say when a cluster is scanned. Each
 * control saves as it is changed.
 */
export function ScanScheduleSettings({ clusterId }: { clusterId: string }) {
  const { data: schedule, isLoading } = useCVEScanSchedule(clusterId);
  const updateSchedule = useUpdateCVEScanSchedule();

  if (isLoading) {
    return (
      <div className="py-3.5 first:pt-1">
        <Skeleton className="h-10 w-full" />
      </div>
    );
  }

  const enabled = schedule?.enabled ?? true;
  const intervalHours = schedule?.interval_hours ?? 24;

  return (
    <>
      <SettingRow
        label="Automatic scans"
        htmlFor="cve-scan-enabled"
        description={
          enabled
            ? "Every node of this cluster is scanned on a timer."
            : "Automatic scanning is off. This cluster is scanned only when someone uses Scan now."
        }
      >
        {updateSchedule.isPending && (
          <Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
        )}
        <Switch
          id="cve-scan-enabled"
          checked={enabled}
          onCheckedChange={() => {
            updateSchedule.mutate({
              clusterId,
              enabled: !enabled,
              intervalHours,
            });
          }}
        />
      </SettingRow>

      {enabled && (
        <SettingRow label="Scan interval" htmlFor="cve-scan-interval">
          <select
            id="cve-scan-interval"
            value={intervalHours}
            onChange={(e) => {
              updateSchedule.mutate({
                clusterId,
                enabled,
                intervalHours: Number(e.target.value),
              });
            }}
            className={settingSelectClass}
          >
            {INTERVAL_OPTIONS.map((opt) => (
              <option key={opt.value} value={opt.value}>
                {opt.label}
              </option>
            ))}
          </select>
        </SettingRow>
      )}
    </>
  );
}
