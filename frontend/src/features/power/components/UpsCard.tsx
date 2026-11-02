import { Link } from "react-router-dom";
import { BatteryCharging } from "lucide-react";
import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import { formatRelativeTime } from "@/lib/format";
import type { UpsDevice } from "../types/ups";
import { formatRuntime, formatWatts, upsOnBattery, upsStatus, type UpsTone } from "../lib/ups-state";
import { UpsStatusBadges } from "./UpsBadges";

const BAR: Record<UpsTone, string> = {
  ok: "bg-emerald-500",
  warn: "bg-amber-500",
  err: "bg-red-500",
  idle: "bg-muted-foreground/40",
};

const VALUE: Record<UpsTone, string> = {
  ok: "",
  warn: "text-amber-600 dark:text-amber-400",
  err: "text-red-600 dark:text-red-400",
  idle: "text-muted-foreground",
};

function Metric({
  label,
  value,
  sub,
  tone = "ok",
}: {
  label: string;
  value: string;
  sub?: string | undefined;
  tone?: UpsTone;
}) {
  return (
    <div className="min-w-0">
      <div className="text-[11px] font-medium uppercase tracking-wider text-muted-foreground">{label}</div>
      <div className={cn("text-lg font-semibold tabular-nums", VALUE[tone])}>{value}</div>
      {sub !== undefined && <div className="truncate text-xs text-muted-foreground">{sub}</div>}
    </div>
  );
}

/**
 * One UPS on the Power page. Values are dimmed when the reading is not
 * current: the last numbers stay visible for context, but the status chip
 * says Unreachable and nothing is coloured as if it were live.
 */
export function UpsCard({ device }: { device: UpsDevice }) {
  const status = upsStatus(device);
  const live = device.reachability === "ok" && !device.reading_stale;
  const tone: UpsTone = live ? status.tone : "idle";
  const onBattery = live && upsOnBattery(device);
  const charge = device.battery_charge;

  return (
    <Card className="flex flex-col">
      <div className="flex items-start gap-3 p-4">
        <div
          className={cn(
            "flex h-10 w-10 shrink-0 items-center justify-center rounded-lg",
            tone === "ok" && "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400",
            tone === "warn" && "bg-amber-500/10 text-amber-600 dark:text-amber-400",
            tone === "err" && "bg-red-500/10 text-red-600 dark:text-red-400",
            tone === "idle" && "bg-muted text-muted-foreground",
          )}
        >
          <BatteryCharging className="h-5 w-5" aria-hidden="true" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-baseline gap-x-2">
            <Link to={`/power/${device.id}`} className="font-semibold hover:underline">
              {device.name}
            </Link>
            {device.description !== "" && (
              <span className="truncate text-sm text-muted-foreground">{device.description}</span>
            )}
          </div>
          <div className="truncate text-xs text-muted-foreground">
            {[device.manufacturer, device.model].filter((s) => s !== "").join(" ") || "Unknown model"} · via{" "}
            {device.server_name}
          </div>
        </div>
        <UpsStatusBadges device={device} />
      </div>

      <div className="grid grid-cols-2 gap-3 px-4 pb-4 sm:grid-cols-4">
        <div className="min-w-0">
          <Metric label="Battery" value={charge == null ? "—" : `${String(Math.round(charge))}%`} tone={onBattery ? tone : live ? "ok" : "idle"} />
          <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
            <div
              className={cn("h-full rounded-full", BAR[onBattery ? tone : live ? "ok" : "idle"])}
              style={{ width: `${String(Math.min(100, Math.max(0, charge ?? 0)))}%` }}
            />
          </div>
        </div>
        <Metric
          label="Runtime"
          value={formatRuntime(device.battery_runtime)}
          sub={onBattery ? "falling, on battery" : "at the current load"}
          tone={onBattery ? tone : live ? "ok" : "idle"}
        />
        <Metric
          label="Load"
          value={device.load == null ? "—" : `${String(Math.round(device.load))}%`}
          sub={formatWatts(device.realpower, device.realpower_estimated)}
          tone={live ? "ok" : "idle"}
        />
        <Metric
          label="Input"
          value={device.input_voltage == null ? "—" : `${String(Math.round(device.input_voltage))} V`}
          sub={device.input_frequency == null ? undefined : `${device.input_frequency.toFixed(1)} Hz`}
          tone={onBattery ? tone : live ? "ok" : "idle"}
        />
      </div>

      <div className="mt-auto flex flex-wrap items-center gap-x-3 gap-y-1 border-t px-4 py-2.5 text-xs text-muted-foreground">
        {device.test_result !== "" && <span>Self-test: {device.test_result}</span>}
        {device.battery_date !== "" && <span>Battery from {device.battery_date}</span>}
        <span className="ml-auto">
          {device.last_reading_at === null ? "Never read" : `Read ${formatRelativeTime(device.last_reading_at)}`}
        </span>
        <Link to={`/power/${device.id}`} className="font-medium text-primary hover:underline">
          Open
        </Link>
      </div>
    </Card>
  );
}
