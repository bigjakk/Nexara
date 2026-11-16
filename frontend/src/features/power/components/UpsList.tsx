import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ChevronRight } from "lucide-react";
import { StatusIcon } from "@/components/StatusIcon";
import { UsageBar } from "@/components/UsageBar";
import type { UsageLevel } from "@/lib/usage-level";
import { cn } from "@/lib/utils";
import { formatDateTime } from "@/lib/format";
import type { UpsDevice } from "../types/ups";
import {
  formatRuntime,
  formatWatts,
  upsOnBattery,
  upsStatus,
  type UpsTone,
} from "../lib/ups-state";
import { TONE_DOT, TONE_TEXT } from "../lib/ups-style";
import { ProtectionBadge, UpsStatusText } from "./UpsBadges";
import { useTranslation } from "react-i18next";

/** One column template for the header line and every row, once there is room. */
const COLUMNS =
  "@4xl:grid-cols-[minmax(0,2.7fr)_minmax(0,1.1fr)_minmax(0,1fr)_minmax(0,1.2fr)_minmax(0,0.8fr)_minmax(0,1.2fr)]";

const BAR_LEVEL: Record<UpsTone, UsageLevel> = {
  ok: "ok",
  warn: "warn",
  err: "err",
  idle: "ok",
};

/** How many of the things a UPS powers its row names before counting the rest. */
const FEED_LIMIT = 6;

/**
 * One reading in a row: its value over a bar or a line of small print. Named
 * above the value where the list is too narrow for the header line.
 */
function Reading({
  label,
  value,
  tone = "ok",
  children,
}: {
  label: string;
  value: ReactNode;
  tone?: UpsTone;
  children?: ReactNode;
}) {
  return (
    <div className="min-w-0">
      <div className="text-xs text-muted-foreground @4xl:sr-only">{label}</div>
      <div className={cn("truncate text-sm font-medium", TONE_TEXT[tone])}>
        {value}
      </div>
      {children}
    </div>
  );
}

function Sub({ children }: { children: ReactNode }) {
  return (
    <div className="truncate text-[12.5px] text-muted-foreground">
      {children}
    </div>
  );
}

/**
 * One UPS. The whole row opens its page. Values are dimmed when the reading
 * is not current: the last numbers stay for context, but the status says
 * Unreachable and nothing is drawn as if it were live.
 */
function UpsRow({ device }: { device: UpsDevice }) {
  const { t } = useTranslation("power");
  const status = upsStatus(device);
  const live = device.reachability === "ok" && !device.reading_stale;
  const tone: UpsTone = live ? status.tone : "idle";
  const onBattery = live && upsOnBattery(device);
  // The battery's figures wear the outage's colour while it lasts.
  const battery: UpsTone = onBattery ? tone : live ? "ok" : "idle";
  const plain: UpsTone = live ? "ok" : "idle";
  const charge = device.battery_charge;
  const model =
    [device.manufacturer, device.model].filter((s) => s !== "").join(" ") ||
    t("upsList.unknownModel");

  return (
    <li
      className={cn(
        "relative flex flex-wrap items-start gap-x-6 gap-y-3 px-[18px] py-3.5 transition-colors hover:bg-foreground/[0.035] @4xl:grid",
        COLUMNS,
      )}
    >
      <div className="order-1 min-w-0 flex-[1_1_12rem] @4xl:order-none">
        <div className="flex min-w-0 items-center gap-2">
          <StatusIcon status={TONE_DOT[tone]} label={status.label} />
          <Link
            to={`/power/${device.id}`}
            className="max-w-[calc(100%-1rem)] shrink-0 truncate font-medium outline-hidden after:absolute after:inset-0 hover:underline focus-visible:after:ring-2 focus-visible:after:ring-inset focus-visible:after:ring-ring"
          >
            {device.name}
          </Link>
          {device.description !== "" && (
            <span className="min-w-0 truncate text-sm text-muted-foreground">
              {device.description}
            </span>
          )}
        </div>
        <div className="truncate pl-4 text-[12.5px] text-muted-foreground">
          {t("upsList.modelViaServerName", { model, serverName: device.server_name })}
        </div>
      </div>

      <div className="order-3 grid basis-full grid-cols-2 gap-x-6 gap-y-3 @xl:grid-cols-4 @4xl:contents">
        <Reading
          label={t("upsList.charge")}
          value={charge == null ? "—" : `${String(Math.round(charge))}%`}
          tone={battery}
        >
          <UsageBar
            percent={live ? charge : null}
            thresholds={false}
            level={BAR_LEVEL[battery]}
            className="mt-1.5 max-w-36"
          />
        </Reading>
        <Reading
          label={t("upsList.runtime")}
          value={formatRuntime(device.battery_runtime)}
          tone={battery}
        >
          <Sub>{onBattery ? t("upsList.fallingOnBattery") : t("upsList.atTheCurrentLoad")}</Sub>
        </Reading>
        <Reading
          label={t("upsList.load")}
          value={
            <>
              {device.load == null
                ? "—"
                : `${String(Math.round(device.load))}%`}
              {device.realpower != null && (
                <span className="ml-2 font-normal text-muted-foreground">
                  {formatWatts(device.realpower, device.realpower_estimated)}
                </span>
              )}
            </>
          }
          tone={plain}
        >
          <UsageBar
            percent={live ? device.load : null}
            className="mt-1.5 max-w-36"
          />
        </Reading>
        <Reading
          label={t("upsList.input")}
          value={
            device.input_voltage == null
              ? "—"
              : `${String(Math.round(device.input_voltage))} V`
          }
          tone={battery}
        >
          {device.input_frequency != null && (
            <Sub>{t("upsList.inputFrequencyHz", { inputFrequency: device.input_frequency.toFixed(1) })}</Sub>
          )}
        </Reading>
      </div>

      <div className="order-2 flex items-start gap-2 @4xl:order-none @4xl:justify-between">
        <div className="min-w-0">
          <UpsStatusText device={device} />
          {/* A fresh reading's age is small print, dropped where the row is
              narrow. One that is stale or was never taken shows at any width. */}
          <div
            className={cn(
              "truncate text-[12.5px] text-muted-foreground",
              live && device.last_reading_at !== null && "hidden @4xl:block",
            )}
          >
            {device.last_reading_at === null
              ? t("upsList.neverRead")
              : t("upsList.readLastReadingAt", { lastReadingAt: formatDateTime(device.last_reading_at, "—", "relative") })}
          </div>
        </div>
        <ChevronRight
          aria-hidden="true"
          className="mt-0.5 hidden h-4 w-4 shrink-0 text-muted-foreground/60 @4xl:block"
        />
      </div>

      {device.feeds.length > 0 && (
        <div className="order-4 flex basis-full flex-wrap items-center gap-x-1.5 gap-y-1 pl-4 text-[12.5px] text-muted-foreground @4xl:order-none @4xl:col-span-full">
          <span className="text-muted-foreground/75">{t("upsList.powers")}</span>
          {device.feeds.slice(0, FEED_LIMIT).map((f, i, shown) => {
            const name = f.kind === "node" ? f.node_name : f.label;
            const unprotected =
              f.kind === "node" && f.protection === "not_protected";
            return (
              <span key={f.id} className="inline-flex items-center gap-1.5">
                <span className={cn(unprotected && "text-foreground")}>
                  {name}
                  {!unprotected && i < shown.length - 1 && ","}
                </span>
                {/* Above the row's link, so its hint shows on hover. */}
                {unprotected && (
                  <ProtectionBadge feed={f} className="relative z-[1]" />
                )}
              </span>
            );
          })}
          {device.feeds.length > FEED_LIMIT && (
            <span>{t("upsList.andLengthMore", { length: device.feeds.length - FEED_LIMIT })}</span>
          )}
        </div>
      )}
    </li>
  );
}

/**
 * Every UPS as a row: its state and name, then charge, runtime, load and
 * input, each row a link to its page. A table's columns where the panel is
 * wide, a block per UPS where it is not.
 */
export function UpsList({ devices }: { devices: UpsDevice[] }) {
  const { t } = useTranslation("power");
  return (
    <div className="@container">
      <div
        aria-hidden="true"
        className={cn(
          "hidden gap-x-6 border-b px-[18px] pb-2 text-xs font-medium text-muted-foreground @4xl:grid",
          COLUMNS,
        )}
      >
        <span>{t("upsList.ups")}</span>
        <span>{t("upsList.charge")}</span>
        <span>{t("upsList.runtime")}</span>
        <span>{t("upsList.load")}</span>
        <span>{t("upsList.input")}</span>
        <span>{t("upsList.status")}</span>
      </div>
      <ul className="divide-y">
        {devices.map((d) => (
          <UpsRow key={d.id} device={d} />
        ))}
      </ul>
    </div>
  );
}
