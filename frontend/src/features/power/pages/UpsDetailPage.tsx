import { useState } from "react";
import { useParams } from "react-router-dom";
import { BatteryCharging } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Breadcrumbs, DetailHeader, Fact, StatusFact } from "@/components/page/DetailHeader";
import { Panel, PropertyList, PropertyRow } from "@/components/page/Panel";
import { Stat, StatStrip, type StatTone } from "@/components/page/StatStrip";
import { QueryFailureNote, QueryStateNotice } from "@/components/QueryStateNotice";
import { TimeRangeSelector } from "@/features/dashboard/components/TimeRangeSelector";
import { formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { TimeRange } from "@/types/api";
import { useNutServers, useUpsDevice, useUpsEvents, useUpsMetrics } from "../api/ups-queries";
import type { UpsDevice, UpsMetricPoint, UpsRange } from "../types/ups";
import { formatRuntime, formatWatts, upsFlagChips, upsOnBattery, upsStatus } from "../lib/ups-state";
import { TONE_DOT, TONE_TEXT } from "../lib/ups-style";
import { UpsChart, type UpsChartFocus } from "../components/UpsCharts";
import { UpsVariablesTable } from "../components/UpsVariablesTable";
import { LatestEventsNote, PowerEventsTable } from "../components/PowerEventsTable";
import { UpsPowers } from "../components/UpsPowers";
import { UpsCommands } from "../components/UpsCommands";
import { UpsVariableDialog } from "../components/UpsVariableDialog";
import { upsHistory } from "../lib/ups-history";
import { UpsMaintenance } from "../components/UpsMaintenance";
import { HistoryTabContent, HistoryTabTrigger } from "@/features/history/components/HistoryTab";
import { usePermissions } from "@/hooks/usePermissions";
import type { UpsWritableVar } from "../types/ups";

function series(points: UpsMetricPoint[], key: keyof UpsMetricPoint): number[] {
  return points.map((p) => p[key]).filter((v): v is number => typeof v === "number");
}

/**
 * The readings: a strip of the latest figures over the last hour's shape,
 * which also picks the measure the chart below draws over the chosen range.
 */
function Readings({ device }: { device: UpsDevice }) {
  const [range, setRange] = useState<TimeRange>("24h");
  const [focus, setFocus] = useState<UpsChartFocus>("charge");
  const r: UpsRange = range === "live" ? "1h" : range;
  const hour = useUpsMetrics(device.id, "1h");
  const metrics = useUpsMetrics(device.id, r);
  const points = hour.data?.points ?? [];
  const live = device.state !== "unknown";
  const onBattery = upsOnBattery(device);
  // The last figures of a UPS that cannot be read now are not drawn as live;
  // the battery's wear the outage's colour while one lasts.
  const plain: StatTone = live ? "default" : "quiet";
  const battery: StatTone = !live ? "quiet" : !onBattery ? "default" : device.state === "low_battery" ? "bad" : "warn";
  const pick = (f: UpsChartFocus) => ({
    selected: focus === f,
    onSelect: () => {
      setFocus(f);
    },
  });

  return (
    <Panel flush title="Readings" actions={<TimeRangeSelector value={range} onChange={setRange} />}>
      <StatStrip>
        <Stat
          label="Charge"
          value={device.battery_charge == null ? "—" : `${String(Math.round(device.battery_charge))}%`}
          sub={device.battery_charge_low == null ? undefined : `goes low at ${String(device.battery_charge_low)}%`}
          tone={battery}
          trend={series(points, "charge")}
          trendMax={100}
          {...pick("charge")}
        />
        <Stat
          label="Runtime"
          value={formatRuntime(device.battery_runtime)}
          sub={onBattery ? "falling, on battery" : "at the current load"}
          tone={battery}
          trend={series(points, "runtime")}
          {...pick("runtime")}
        />
        <Stat
          label="Load"
          value={device.load == null ? "—" : `${String(Math.round(device.load))}%`}
          sub={`${formatWatts(device.realpower, device.realpower_estimated)}${device.realpower_nominal == null ? "" : ` of ${String(device.realpower_nominal)} W`}`}
          tone={plain}
          trend={series(points, "load")}
          trendMax={100}
          {...pick("load")}
        />
        <Stat
          label="Input"
          value={device.input_voltage == null ? "—" : `${String(Math.round(device.input_voltage))} V`}
          sub={device.input_frequency == null ? undefined : `${device.input_frequency.toFixed(1)} Hz`}
          tone={battery}
          trend={series(points, "input_voltage_min")}
          {...pick("voltage")}
        />
        <Stat
          label="Output"
          value={device.output_voltage == null ? "—" : `${String(Math.round(device.output_voltage))} V`}
          sub="supplied to the load"
          tone={plain}
          trend={series(points, "output_voltage")}
          {...pick("voltage")}
        />
      </StatStrip>
      <div className="-mt-px border-t px-[18px] pb-1 pt-4">
        {metrics.data !== undefined && metrics.data.points.length > 0 ? (
          <>
            <QueryFailureNote query={metrics} subject="this UPS's readings" className="pb-2" />
            <UpsChart focus={focus} points={metrics.data.points} range={r} bucketSeconds={metrics.data.bucket_seconds} />
          </>
        ) : (
          <QueryStateNotice query={metrics} subject="this UPS's readings" empty="No readings in this range yet." />
        )}
      </div>
    </Panel>
  );
}

/** What the battery and the unit are: read once, seldom changing. */
function Identity({ device }: { device: UpsDevice }) {
  return (
    <>
      <Panel title="Battery">
        <PropertyList>
          <PropertyRow label="Chemistry">{device.battery_type || "—"}</PropertyRow>
          <PropertyRow label="Voltage">{device.battery_voltage == null ? "—" : `${String(device.battery_voltage)} V`}</PropertyRow>
          <PropertyRow label="Installed">{device.battery_date || "—"}</PropertyRow>
          <PropertyRow label="Goes low at">
            {[
              device.battery_charge_low == null ? "" : `${String(device.battery_charge_low)}%`,
              device.battery_runtime_low == null ? "" : formatRuntime(device.battery_runtime_low),
            ].filter((s) => s !== "").join(" or ") || "—"}
          </PropertyRow>
          <PropertyRow label="Last self-test">{device.test_result || "—"}</PropertyRow>
        </PropertyList>
      </Panel>
      <Panel title="Device">
        <PropertyList>
          <PropertyRow label="Manufacturer">{device.manufacturer || "—"}</PropertyRow>
          <PropertyRow label="Model">{device.model || "—"}</PropertyRow>
          <PropertyRow label="Serial">
            <span className="font-mono text-[12.5px]">{device.serial || "—"}</span>
          </PropertyRow>
          <PropertyRow label="Firmware">{device.firmware || "—"}</PropertyRow>
          <PropertyRow label="Driver">{device.driver || "—"}</PropertyRow>
          <PropertyRow label="Transfer range">
            {device.input_transfer_low == null || device.input_transfer_high == null
              ? "—"
              : `${String(device.input_transfer_low)}–${String(device.input_transfer_high)} V`}
          </PropertyRow>
        </PropertyList>
      </Panel>
    </>
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

  if (d === undefined) {
    return (
      <div className="space-y-5">
        <Breadcrumbs crumbs={[{ label: "Power", to: "/power" }, { label: "UPS" }]} />
        <Panel>
          <QueryStateNotice query={device} subject="this UPS" empty="This UPS was not found." />
        </Panel>
      </div>
    );
  }

  const status = upsStatus(d);
  const flags = d.reachability === "ok" && !d.reading_stale ? upsFlagChips(d) : [];
  const model = [d.manufacturer, d.model].filter((s) => s !== "").join(" ");
  const unprotected = d.feeds.some((f) => f.protection === "not_protected");
  const eventList = events.data ?? [];

  return (
    <div className="space-y-5">
      <DetailHeader
        crumbs={[{ label: "Power", to: "/power" }, { label: d.name }]}
        icon={<BatteryCharging />}
        title={d.name}
        titleAside={d.description !== "" ? <span className="text-muted-foreground">{d.description}</span> : undefined}
        facts={
          <>
            <StatusFact
              status={TONE_DOT[status.tone]}
              label={status.label}
              nameDot
              detail={
                upsOnBattery(d) && d.on_battery_since !== null
                  ? `since ${new Date(d.on_battery_since).toLocaleTimeString()}`
                  : undefined
              }
            />
            {/* What upsd flags beside the state: in words, coloured only where it is a problem. */}
            {flags.map((f) => (
              <span key={f.label} className={cn(f.tone !== "idle" && "font-[550]", TONE_TEXT[f.tone])}>
                {f.label}
              </span>
            ))}
            {model !== "" && <Fact label="Model">{model}</Fact>}
            <Fact label="NUT server">{d.server_name}</Fact>
            <Fact label="Last read">{d.last_reading_at === null ? "never" : formatDateTime(d.last_reading_at)}</Fact>
          </>
        }
      />
      <QueryFailureNote query={device} subject="the latest reading of this UPS" />
      {d.state === "unknown" && (
        <p className="rounded-[10px] bg-amber-500/10 px-[18px] py-3 text-sm">
          {d.last_reading_at === null
            ? "Nexara has not read this UPS yet."
            : `Nexara has no current reading for this UPS (${status.label.toLowerCase()}). The values below are the last ones it read.`}
        </p>
      )}
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="powers">
            Powers ({d.feeds.length})
            {unprotected && (
              <span role="img" className="ml-1.5 h-2 w-2 rounded-full bg-amber-500" aria-label="a node is not protected" />
            )}
          </TabsTrigger>
          <TabsTrigger value="variables">Variables ({Object.keys(d.vars ?? {}).length})</TabsTrigger>
          <TabsTrigger value="commands">Commands ({d.commands?.length ?? 0})</TabsTrigger>
          <TabsTrigger value="events">Power events</TabsTrigger>
          <HistoryTabTrigger history={upsHistory(d.id)} />
        </TabsList>
        <TabsContent value="overview" className="mt-5">
          {/* What the UPS is doing on the left, what it is on the right. */}
          <div className="flex flex-wrap items-start gap-4">
            <div className="flex min-w-0 flex-[999_1_560px] flex-col gap-4">
              <Readings device={d} />
              <UpsMaintenance device={d} />
            </div>
            <div className="flex min-w-0 flex-[1_1_300px] flex-col gap-4">
              <Identity device={d} />
            </div>
          </div>
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
          {eventList.length > 0 ? (
            <Panel flush>
              <PowerEventsTable events={eventList} />
              <LatestEventsNote count={eventList.length} />
            </Panel>
          ) : (
            <Panel>
              <QueryStateNotice query={events} subject="this UPS's power events" empty="No power events recorded yet." />
            </Panel>
          )}
        </TabsContent>
        <HistoryTabContent history={upsHistory(d.id)} />
      </Tabs>
    </div>
  );
}
