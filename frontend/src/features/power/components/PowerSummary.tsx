import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import type { UpsDevice } from "../types/ups";
import { formatRuntime, upsOnBattery } from "../lib/ups-state";
import { protectionSummary } from "../lib/ups-feeds";
import { upsRead } from "../lib/power-summary";
import { useTranslation } from "react-i18next";

/**
 * The figures that answer "how much power and time do I have": how many UPS
 * are on battery, the shortest runtime and the total load, from UPSes read
 * just now (an unreadable one is called out rather than averaged in), and how
 * many of the nodes they power would shut down with them.
 */
export function PowerSummary({ devices }: { devices: UpsDevice[] }) {
  const { t } = useTranslation("power");
  const live = devices.filter(upsRead);
  const unread = devices.length - live.length;
  const onBattery = live.filter(upsOnBattery);
  const outage = onBattery.length > 0;

  const withRuntime = live.filter((d) => d.battery_runtime != null);
  const shortest = withRuntime.reduce<UpsDevice | null>(
    (min, d) => (min === null || (d.battery_runtime ?? Infinity) < (min.battery_runtime ?? Infinity) ? d : min),
    null,
  );

  const watts = live.reduce((sum, d) => sum + (d.realpower ?? 0), 0);
  const nominal = live.reduce((sum, d) => sum + (d.realpower_nominal ?? 0), 0);
  const estimated = live.some((d) => d.realpower_estimated);

  const protection = protectionSummary(devices);

  return (
    <Panel flush bodyClassName="py-4" aria-label={t("powerSummary.powerSummary")}>
      <StatStrip>
        {/* Counts, as the strip's other figures are: a sentence here is cut short on a phone. */}
        <Stat
          label={t("powerSummary.onBattery")}
          value={live.length > 0 ? String(onBattery.length) : "—"}
          sub={
            // Wraps where the strip would cut it: "not read" is the part that matters.
            <span className="whitespace-normal">
              {unread > 0
                ? t("powerSummary.upsReadUnread", { read: String(live.length), total: String(devices.length), unread: String(unread) })
                : t("powerSummary.upsRead", { read: String(live.length), total: String(devices.length) })}
            </span>
          }
          tone={outage ? "warn" : "quiet"}
        />
        <Stat
          label={t("powerSummary.shortestRuntime")}
          value={shortest === null ? "—" : formatRuntime(shortest.battery_runtime)}
          sub={shortest === null ? t("powerSummary.noRuntimeReported") : t("powerSummary.nameAtItsCurrentLoad", { name: shortest.name })}
          tone={outage ? "warn" : shortest === null ? "quiet" : "default"}
        />
        <Stat
          label={t("powerSummary.load")}
          value={watts > 0 ? `${estimated ? "≈" : ""}${String(Math.round(watts))} W` : "—"}
          sub={nominal > 0 ? t("powerSummary.ofNominalWAcrossLength", { nominal: String(Math.round(nominal)), length: String(live.length) }) : t("powerSummary.noPowerReported")}
          tone={watts > 0 ? "default" : "quiet"}
          percent={watts > 0 && nominal > 0 ? (watts / nominal) * 100 : undefined}
        />
        <Stat
          label={t("powerSummary.nodesProtected")}
          value={protection.judged === 0 ? "—" : t("powerSummary.protectedOfJudged", { protected: String(protection.protected), judged: String(protection.judged) })}
          sub={
            // Wraps too: every unprotected node is named, however many there are.
            <span className="whitespace-normal">
              {protection.judged === 0
                ? t("powerSummary.noNodeItPowersIs")
                : protection.unprotected.length > 0
                  ? t("powerSummary.notProtectedUnprotected", { unprotected: protection.unprotected.join(", ") })
                  : t("powerSummary.runUpsmonAgainstTheUPS")}
            </span>
          }
          tone={protection.unprotected.length > 0 ? "warn" : protection.judged === 0 ? "quiet" : "default"}
        />
      </StatStrip>
    </Panel>
  );
}
