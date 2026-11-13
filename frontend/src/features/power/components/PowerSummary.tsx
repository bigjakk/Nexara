import { Panel } from "@/components/page/Panel";
import { Stat, StatStrip } from "@/components/page/StatStrip";
import type { UpsDevice } from "../types/ups";
import { formatRuntime, upsOnBattery } from "../lib/ups-state";
import { protectionSummary } from "../lib/ups-feeds";
import { upsRead } from "../lib/power-summary";

/**
 * The figures that answer "how much power and time do I have": how many UPS
 * are on battery, the shortest runtime and the total load, from UPSes read
 * just now (an unreadable one is called out rather than averaged in), and how
 * many of the nodes they power would shut down with them.
 */
export function PowerSummary({ devices }: { devices: UpsDevice[] }) {
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

  const unreadNote = unread > 0 ? ` · ${String(unread)} not read` : "";
  const protection = protectionSummary(devices);

  return (
    <Panel flush bodyClassName="py-4" aria-label="Power summary">
      <StatStrip>
        {/* Counts, as the strip's other figures are: a sentence here is cut short on a phone. */}
        <Stat
          label="On battery"
          value={live.length > 0 ? String(onBattery.length) : "—"}
          sub={
            // Wraps where the strip would cut it: "not read" is the part that matters.
            <span className="whitespace-normal">
              {`${String(live.length)} of ${String(devices.length)} UPS read${unreadNote}`}
            </span>
          }
          tone={outage ? "warn" : "quiet"}
        />
        <Stat
          label="Shortest runtime"
          value={shortest === null ? "—" : formatRuntime(shortest.battery_runtime)}
          sub={shortest === null ? "No runtime reported" : `${shortest.name} at its current load`}
          tone={outage ? "warn" : shortest === null ? "quiet" : "default"}
        />
        <Stat
          label="Load"
          value={watts > 0 ? `${estimated ? "≈" : ""}${String(Math.round(watts))} W` : "—"}
          sub={nominal > 0 ? `of ${String(Math.round(nominal))} W across ${String(live.length)} UPS` : "No power reported"}
          tone={watts > 0 ? "default" : "quiet"}
          percent={watts > 0 && nominal > 0 ? (watts / nominal) * 100 : undefined}
        />
        <Stat
          label="Nodes protected"
          value={protection.judged === 0 ? "—" : `${String(protection.protected)} of ${String(protection.judged)}`}
          sub={
            // Wraps too: every unprotected node is named, however many there are.
            <span className="whitespace-normal">
              {protection.judged === 0
                ? "No node it powers is checked yet"
                : protection.unprotected.length > 0
                  ? `Not protected: ${protection.unprotected.join(", ")}`
                  : "run upsmon against the UPS that powers them"}
            </span>
          }
          tone={protection.unprotected.length > 0 ? "warn" : protection.judged === 0 ? "quiet" : "default"}
        />
      </StatStrip>
    </Panel>
  );
}
