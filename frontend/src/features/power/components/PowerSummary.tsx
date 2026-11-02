import { Clock, Plug, Zap } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { cn } from "@/lib/utils";
import type { UpsDevice } from "../types/ups";
import { formatRuntime, upsOnBattery } from "../lib/ups-state";

function isLive(d: UpsDevice): boolean {
  return d.server_enabled && d.reachability === "ok" && !d.reading_stale;
}

function SummaryCard({
  title,
  icon: Icon,
  value,
  sub,
  warn,
}: {
  title: string;
  icon: typeof Zap;
  value: string;
  sub: string;
  warn?: boolean;
}) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
        <Icon className="h-4 w-4 text-muted-foreground" aria-hidden="true" />
      </CardHeader>
      <CardContent>
        <div className={cn("text-lg font-semibold tabular-nums", warn === true && "text-amber-600 dark:text-amber-400")}>
          {value}
        </div>
        <div className="mt-0.5 text-xs text-muted-foreground">{sub}</div>
      </CardContent>
    </Card>
  );
}

/**
 * The three figures that answer "how much power and time do I have": whether
 * the mains are up, the shortest runtime, and the total load. Only UPSes read
 * just now count; an unreadable one is called out rather than averaged in.
 */
export function PowerSummary({ devices }: { devices: UpsDevice[] }) {
  const live = devices.filter(isLive);
  const unread = devices.length - live.length;
  const onBattery = live.filter(upsOnBattery);

  const withRuntime = live.filter((d) => d.battery_runtime != null);
  const shortest = withRuntime.reduce<UpsDevice | null>(
    (min, d) => (min === null || (d.battery_runtime ?? Infinity) < (min.battery_runtime ?? Infinity) ? d : min),
    null,
  );

  const watts = live.reduce((sum, d) => sum + (d.realpower ?? 0), 0);
  const nominal = live.reduce((sum, d) => sum + (d.realpower_nominal ?? 0), 0);
  const estimated = live.some((d) => d.realpower_estimated);

  const unreadNote = unread > 0 ? ` · ${String(unread)} not read` : "";

  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
      <SummaryCard
        title="Mains power"
        icon={Plug}
        value={onBattery.length > 0 ? `${String(onBattery.length)} on battery` : live.length > 0 ? "On mains" : "—"}
        sub={`${String(live.length)} of ${String(devices.length)} UPS read${unreadNote}`}
        warn={onBattery.length > 0}
      />
      <SummaryCard
        title="Shortest runtime"
        icon={Clock}
        value={shortest === null ? "—" : formatRuntime(shortest.battery_runtime)}
        sub={shortest === null ? "No runtime reported" : `${shortest.name} at its current load`}
        warn={onBattery.length > 0}
      />
      <SummaryCard
        title="Load"
        icon={Zap}
        value={watts > 0 ? `${estimated ? "≈" : ""}${String(Math.round(watts))} W` : "—"}
        sub={nominal > 0 ? `of ${String(Math.round(nominal))} W across ${String(live.length)} UPS` : "No power reported"}
      />
    </div>
  );
}
