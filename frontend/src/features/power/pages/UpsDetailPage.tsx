import { useState } from "react";
import { useParams } from "react-router-dom";
import { BatteryCharging } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Breadcrumbs, DetailHeader, Fact, StatusFact } from "@/components/page/DetailHeader";
import { Panel, PropertyList, PropertyRow } from "@/components/page/Panel";
import { Stat, StatStrip, type StatTone } from "@/components/page/StatStrip";
import { QueryFailureNote, QueryStateNotice } from "@/components/QueryStateNotice";
import { TimeRangeSelector } from "@/features/dashboard/components/TimeRangeSelector";
import { formatTime } from "@/lib/format";
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
import { DateTime } from "@/components/DateTime";
import { useTranslation } from "react-i18next";

function series(points: UpsMetricPoint[], key: keyof UpsMetricPoint): number[] {
  return points.map((p) => p[key]).filter((v): v is number => typeof v === "number");
}

/**
 * The readings: a strip of the latest figures over the last hour's shape,
 * which also picks the measure the chart below draws over the chosen range.
 */
function Readings({ device }: { device: UpsDevice }) {
  const { t } = useTranslation("power");
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
    <Panel flush title={t("upsDetailPage.readings")} actions={<TimeRangeSelector value={range} onChange={setRange} />}>
      <StatStrip>
        <Stat
          label={t("upsDetailPage.charge")}
          value={device.battery_charge == null ? "—" : `${String(Math.round(device.battery_charge))}%`}
          sub={device.battery_charge_low == null ? undefined : t("upsDetailPage.goesLowAtBatteryChargeLow", { batteryChargeLow: String(device.battery_charge_low) })}
          tone={battery}
          trend={series(points, "charge")}
          trendMax={100}
          {...pick("charge")}
        />
        <Stat
          label={t("upsDetailPage.runtime")}
          value={formatRuntime(device.battery_runtime)}
          sub={onBattery ? t("upsDetailPage.fallingOnBattery") : t("upsDetailPage.atTheCurrentLoad")}
          tone={battery}
          trend={series(points, "runtime")}
          {...pick("runtime")}
        />
        <Stat
          label={t("upsDetailPage.load")}
          value={device.load == null ? "—" : `${String(Math.round(device.load))}%`}
          sub={
            device.realpower_nominal == null
              ? formatWatts(device.realpower, device.realpower_estimated)
              : t("upsDetailPage.wattsOfNominal", {
                  watts: formatWatts(device.realpower, device.realpower_estimated),
                  nominal: String(device.realpower_nominal),
                })
          }
          tone={plain}
          trend={series(points, "load")}
          trendMax={100}
          {...pick("load")}
        />
        <Stat
          label={t("upsDetailPage.input")}
          value={device.input_voltage == null ? "—" : `${String(Math.round(device.input_voltage))} V`}
          sub={device.input_frequency == null ? undefined : `${device.input_frequency.toFixed(1)} Hz`}
          tone={battery}
          trend={series(points, "input_voltage_min")}
          {...pick("voltage")}
        />
        <Stat
          label={t("upsDetailPage.output")}
          value={device.output_voltage == null ? "—" : `${String(Math.round(device.output_voltage))} V`}
          sub={t("upsDetailPage.suppliedToTheLoad")}
          tone={plain}
          trend={series(points, "output_voltage")}
          {...pick("voltage")}
        />
      </StatStrip>
      <div className="-mt-px border-t px-[18px] pb-1 pt-4">
        {metrics.data !== undefined && metrics.data.points.length > 0 ? (
          <>
            <QueryFailureNote query={metrics} subject={t("upsDetailPage.thisUPSSReadings")} className="pb-2" />
            <UpsChart focus={focus} points={metrics.data.points} range={r} bucketSeconds={metrics.data.bucket_seconds} />
          </>
        ) : (
          <QueryStateNotice query={metrics} subject={t("upsDetailPage.thisUPSSReadings")} empty={t("upsDetailPage.noReadingsInThisRange")} />
        )}
      </div>
    </Panel>
  );
}

/** What the battery and the unit are: read once, seldom changing. */
function Identity({ device }: { device: UpsDevice }) {
  const { t } = useTranslation("power");
  return (
    <>
      <Panel title={t("upsDetailPage.battery")}>
        <PropertyList>
          <PropertyRow label={t("upsDetailPage.chemistry")}>{device.battery_type || "—"}</PropertyRow>
          <PropertyRow label={t("upsDetailPage.voltage")}>{device.battery_voltage == null ? "—" : `${String(device.battery_voltage)} V`}</PropertyRow>
          <PropertyRow label={t("upsDetailPage.installed")}>{device.battery_date || "—"}</PropertyRow>
          <PropertyRow label={t("upsDetailPage.goesLowAt")}>
            {[
              device.battery_charge_low == null ? "" : `${String(device.battery_charge_low)}%`,
              device.battery_runtime_low == null ? "" : formatRuntime(device.battery_runtime_low),
            ].filter((s) => s !== "").join(" or ") || "—"}
          </PropertyRow>
          <PropertyRow label={t("upsDetailPage.lastSelfTest")}>{device.test_result || "—"}</PropertyRow>
        </PropertyList>
      </Panel>
      <Panel title={t("upsDetailPage.device")}>
        <PropertyList>
          <PropertyRow label={t("upsDetailPage.manufacturer")}>{device.manufacturer || "—"}</PropertyRow>
          <PropertyRow label={t("upsDetailPage.model")}>{device.model || "—"}</PropertyRow>
          <PropertyRow label={t("upsDetailPage.serial")}>
            <span className="font-mono text-[12.5px]">{device.serial || "—"}</span>
          </PropertyRow>
          <PropertyRow label={t("upsDetailPage.firmware")}>{device.firmware || "—"}</PropertyRow>
          <PropertyRow label={t("upsDetailPage.driver")}>{device.driver || "—"}</PropertyRow>
          <PropertyRow label={t("upsDetailPage.transferRange")}>
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
  const { t } = useTranslation("power");
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
    if (!v.editable) return v.reason ?? t("upsDetailPage.nexaraDoesNotOfferIt");
    if (!hasGlobalPermission("manage", "ups")) return t("upsDetailPage.settingItTakesManageUps");
    if (v.affects_power && !(hasGlobalPermission("execute", "ups") && canManage("node"))) {
      return t("upsDetailPage.itCanChangeThePower");
    }
    if (!hasAccount) return t("upsDetailPage.settingItTakesAnAccount");
    return null;
  };

  if (d === undefined) {
    return (
      <div className="space-y-5">
        <Breadcrumbs crumbs={[{ label: t("upsDetailPage.power"), to: "/power" }, { label: t("upsDetailPage.ups") }]} />
        <Panel>
          <QueryStateNotice query={device} subject={t("upsDetailPage.thisUPS")} empty={t("upsDetailPage.thisUPSWasNotFound")} />
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
        crumbs={[{ label: t("upsDetailPage.power"), to: "/power" }, { label: d.name }]}
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
                  ? t("upsDetailPage.sinceOnBatterySince", { onBatterySince: formatTime(d.on_battery_since) })
                  : undefined
              }
            />
            {/* What upsd flags beside the state: in words, coloured only where it is a problem. */}
            {flags.map((f) => (
              <span key={f.label} className={cn(f.tone !== "idle" && "font-[550]", TONE_TEXT[f.tone])}>
                {f.label}
              </span>
            ))}
            {model !== "" && <Fact label={t("upsDetailPage.model")}>{model}</Fact>}
            <Fact label={t("upsDetailPage.nutServer")}>{d.server_name}</Fact>
            <Fact label={t("upsDetailPage.lastRead")}><DateTime value={d.last_reading_at} fallback={t("upsDetailPage.never")} /></Fact>
          </>
        }
      />
      <QueryFailureNote query={device} subject={t("upsDetailPage.theLatestReadingOfThis")} />
      {d.state === "unknown" && (
        <p className="rounded-[10px] bg-amber-500/10 px-[18px] py-3 text-sm">
          {d.last_reading_at === null
            ? t("upsDetailPage.nexaraHasNotReadThis")
            : t("upsDetailPage.nexaraHasNoCurrentReading", { label: status.label.toLowerCase() })}
        </p>
      )}
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">{t("upsDetailPage.overview")}</TabsTrigger>
          <TabsTrigger value="powers">
            {t("upsDetailPage.powersLength", { length: d.feeds.length })}
            {unprotected && (
              <span role="img" className="ml-1.5 h-2 w-2 rounded-full bg-amber-500" aria-label={t("upsDetailPage.aNodeIsNotProtected")} />
            )}
          </TabsTrigger>
          <TabsTrigger value="variables">{t("upsDetailPage.variablesLength", { length: Object.keys(d.vars ?? {}).length })}</TabsTrigger>
          <TabsTrigger value="commands">{t("upsDetailPage.commandsLength", { length: d.commands?.length ?? 0 })}</TabsTrigger>
          <TabsTrigger value="events">{t("upsDetailPage.powerEvents")}</TabsTrigger>
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
              <QueryStateNotice query={events} subject={t("upsDetailPage.thisUPSSPowerEvents")} empty={t("upsDetailPage.noPowerEventsRecordedYet")} />
            </Panel>
          )}
        </TabsContent>
        <HistoryTabContent history={upsHistory(d.id)} />
      </Tabs>
    </div>
  );
}
