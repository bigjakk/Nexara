import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { BatteryCharging } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DetailField } from "@/components/DetailField";
import { QueryFailureNote, QueryStateNotice } from "@/components/QueryStateNotice";
import { Sparkline } from "@/components/Sparkline";
import { TimeRangeSelector } from "@/features/dashboard/components/TimeRangeSelector";
import { formatDateTime } from "@/lib/format";
import type { TimeRange } from "@/types/api";
import { useNutServers, useUpsDevice, useUpsEvents, useUpsMetrics } from "../api/ups-queries";
import type { UpsDevice, UpsMetricPoint, UpsRange } from "../types/ups";
import { formatRuntime, formatWatts, upsOnBattery, upsStatus } from "../lib/ups-state";
import { UpsStatusBadges } from "../components/UpsBadges";
import { UpsCharts } from "../components/UpsCharts";
import { UpsVariablesTable } from "../components/UpsVariablesTable";
import { LatestEventsNote, PowerEventsTable } from "../components/PowerEventsTable";
import { UpsPowers } from "../components/UpsPowers";
import { UpsCommands } from "../components/UpsCommands";
import { UpsVariableDialog } from "../components/UpsVariableDialog";
import { usePermissions } from "@/hooks/usePermissions";
import type { UpsWritableVar } from "../types/ups";

function series(points: UpsMetricPoint[], key: keyof UpsMetricPoint): number[] {
  return points.map((p) => p[key]).filter((v): v is number => typeof v === "number");
}

function StatCard({ title, value, sub, spark }: { title: string; value: string; sub: string; spark?: number[] }) {
  return (
    <Card className="flex flex-col">
      <CardHeader className="pb-2">
        <CardTitle className="text-sm font-medium">{title}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-1 flex-col">
        <div className="text-lg font-semibold tabular-nums">{value}</div>
        <div className="mt-0.5 text-xs text-muted-foreground">{sub}</div>
        {spark !== undefined && spark.length > 1 && (
          <div className="mt-auto pt-2">
            <Sparkline points={spark} className="h-7 w-full text-primary" />
          </div>
        )}
      </CardContent>
    </Card>
  );
}

function Overview({ device }: { device: UpsDevice }) {
  const [range, setRange] = useState<TimeRange>("24h");
  const r: UpsRange = range === "live" ? "1h" : range;
  const hour = useUpsMetrics(device.id, "1h");
  const metrics = useUpsMetrics(device.id, r);
  const points = hour.data?.points ?? [];
  const onBattery = upsOnBattery(device);

  return (
    <div className="space-y-6">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <StatCard
          title="Battery charge"
          value={device.battery_charge == null ? "—" : `${String(Math.round(device.battery_charge))}%`}
          sub={device.battery_charge_low == null ? "" : `goes low at ${String(device.battery_charge_low)}%`}
          spark={series(points, "charge")}
        />
        <StatCard
          title="Runtime"
          value={formatRuntime(device.battery_runtime)}
          sub={onBattery ? "falling, on battery" : "at the current load"}
          spark={series(points, "runtime")}
        />
        <StatCard
          title="Load"
          value={device.load == null ? "—" : `${String(Math.round(device.load))}%`}
          sub={`${formatWatts(device.realpower, device.realpower_estimated)}${device.realpower_nominal == null ? "" : ` of ${String(device.realpower_nominal)} W`}`}
          spark={series(points, "load")}
        />
        <StatCard
          title="Input"
          value={device.input_voltage == null ? "—" : `${String(Math.round(device.input_voltage))} V`}
          sub={[
            device.input_frequency == null ? "" : `${device.input_frequency.toFixed(1)} Hz`,
            device.output_voltage == null ? "" : `output ${String(Math.round(device.output_voltage))} V`,
          ].filter((s) => s !== "").join(" · ")}
          spark={series(points, "input_voltage_min")}
        />
      </div>

      <div className="flex justify-end">
        <TimeRangeSelector value={range} onChange={setRange} />
      </div>
      {metrics.data !== undefined && metrics.data.points.length > 0 ? (
        <>
          <QueryFailureNote query={metrics} subject="this UPS's readings" />
          <UpsCharts points={metrics.data.points} range={r} bucketSeconds={metrics.data.bucket_seconds} />
        </>
      ) : (
        <QueryStateNotice query={metrics} subject="this UPS's readings" empty="No readings in this range yet." />
      )}

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium">Battery</CardTitle></CardHeader>
          <CardContent>
            <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
              <DetailField label="Chemistry">{device.battery_type || "—"}</DetailField>
              <DetailField label="Voltage">{device.battery_voltage == null ? "—" : `${String(device.battery_voltage)} V`}</DetailField>
              <DetailField label="Installed">{device.battery_date || "—"}</DetailField>
              <DetailField label="Goes low at">
                {[
                  device.battery_charge_low == null ? "" : `${String(device.battery_charge_low)}%`,
                  device.battery_runtime_low == null ? "" : formatRuntime(device.battery_runtime_low),
                ].filter((s) => s !== "").join(" or ") || "—"}
              </DetailField>
              <DetailField label="Last self-test">{device.test_result || "—"}</DetailField>
            </dl>
          </CardContent>
        </Card>
        <Card>
          <CardHeader className="pb-2"><CardTitle className="text-sm font-medium">Device</CardTitle></CardHeader>
          <CardContent>
            <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
              <DetailField label="Manufacturer">{device.manufacturer || "—"}</DetailField>
              <DetailField label="Model">{device.model || "—"}</DetailField>
              <DetailField label="Serial" variant="mono">{device.serial || "—"}</DetailField>
              <DetailField label="Firmware">{device.firmware || "—"}</DetailField>
              <DetailField label="Driver">{device.driver || "—"}</DetailField>
              <DetailField label="Transfer range">
                {device.input_transfer_low == null || device.input_transfer_high == null
                  ? "—"
                  : `${String(device.input_transfer_low)}–${String(device.input_transfer_high)} V`}
              </DetailField>
            </dl>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

/** One UPS: its readings and charts, every variable upsd reports, and its power events. */
export function UpsDetailPage() {
  const { upsId = "" } = useParams<{ upsId: string }>();
  const device = useUpsDevice(upsId);
  const events = useUpsEvents(upsId);
  const d = device.data;
  const { hasGlobalPermission, canManage } = usePermissions();
  const [setting, setSetting] = useState<UpsWritableVar | null>(null);
  const hasAccount = useNutServers().data?.find((s) => s.id === d?.server_id)?.has_password === true;
  // As the server gates it: one that can change the power also takes
  // execute:ups, and manage:node on the clusters the UPS powers.
  const whyNotSet = (v: UpsWritableVar): string | null => {
    if (!v.editable) return v.reason ?? "Nexara does not offer it for editing";
    if (!hasGlobalPermission("manage", "ups")) return "Setting it takes manage:ups";
    if (v.affects_power && !(hasGlobalPermission("execute", "ups") && canManage("node"))) {
      return "It can change the power the UPS supplies, so setting it also takes execute:ups and manage:node";
    }
    if (!hasAccount) return "Setting it takes an account from upsd.users, stored on the NUT server";
    return null;
  };

  return (
    <div className="space-y-6">
      <nav aria-label="Breadcrumb" className="flex items-center gap-1.5 text-sm text-muted-foreground">
        <Link to="/power" className="hover:text-foreground">Power</Link>
        <span aria-hidden="true">/</span>
        <span className="text-foreground">{d?.name ?? "UPS"}</span>
      </nav>
      {d === undefined ? (
        <QueryStateNotice query={device} subject="this UPS" empty="This UPS was not found." />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-4">
            <BatteryCharging className="h-6 w-6 text-primary" aria-hidden="true" />
            <div className="min-w-0 flex-1">
              <div className="flex flex-wrap items-baseline gap-x-3">
                <h1 className="text-2xl font-semibold">{d.name}</h1>
                {d.description !== "" && <span className="text-muted-foreground">{d.description}</span>}
              </div>
              <div className="mt-1.5 flex flex-wrap items-center gap-2">
                <UpsStatusBadges device={d} />
                {(d.manufacturer !== "" || d.model !== "") && (
                  <Badge variant="outline">{[d.manufacturer, d.model].filter((s) => s !== "").join(" ")}</Badge>
                )}
                <Badge variant="outline">via {d.server_name}</Badge>
                <span className="text-xs text-muted-foreground">
                  {d.last_reading_at === null ? "Never read" : `Last read ${formatDateTime(d.last_reading_at)}`}
                </span>
              </div>
            </div>
          </div>
          <QueryFailureNote query={device} subject="the latest reading of this UPS" />
          {d.state === "unknown" && (
            <p className="rounded-md border border-amber-500/50 bg-amber-500/10 p-3 text-sm">
              {d.last_reading_at === null
                ? "Nexara has not read this UPS yet."
                : `Nexara has no current reading for this UPS (${upsStatus(d).label.toLowerCase()}). The values below are the last ones it read.`}
            </p>
          )}
          <Tabs defaultValue="overview">
            <TabsList>
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="powers">
                Powers ({d.feeds.length})
                {d.feeds.some((f) => f.protection === "not_protected") && (
                  <span role="img" className="ml-1.5 h-2 w-2 rounded-full bg-amber-500" aria-label="a node is not protected" />
                )}
              </TabsTrigger>
              <TabsTrigger value="variables">Variables ({Object.keys(d.vars ?? {}).length})</TabsTrigger>
              <TabsTrigger value="commands">Commands ({d.commands?.length ?? 0})</TabsTrigger>
              <TabsTrigger value="events">Power events</TabsTrigger>
            </TabsList>
            <TabsContent value="overview">
              <Overview device={d} />
            </TabsContent>
            <TabsContent value="powers">
              <UpsPowers device={d} />
            </TabsContent>
            <TabsContent value="variables">
              <UpsVariablesTable
                vars={d.vars ?? {}}
                descriptions={d.var_descriptions ?? {}}
                writable={d.writable ?? []}
                whyNotSet={whyNotSet}
                onSet={setSetting}
              />
              {setting !== null && (
                <UpsVariableDialog
                  device={d}
                  variable={setting}
                  onClose={() => {
                    setSetting(null);
                  }}
                />
              )}
            </TabsContent>
            <TabsContent value="commands">
              <UpsCommands device={d} />
            </TabsContent>
            <TabsContent value="events">
              {(events.data?.length ?? 0) > 0 ? (
                <>
                  <PowerEventsTable events={events.data ?? []} />
                  <LatestEventsNote count={events.data?.length ?? 0} />
                </>
              ) : (
                <QueryStateNotice query={events} subject="this UPS's power events" empty="No power events recorded yet." />
              )}
            </TabsContent>
          </Tabs>
        </>
      )}
    </div>
  );
}
