import type { UpsDevice } from "../types/ups";
import { upsOnBattery } from "./ups-state";

/** Read just now: its server is polled, upsd answered, the reading is fresh. */
export function upsRead(
  d: Pick<UpsDevice, "server_enabled" | "reachability" | "reading_stale">,
): boolean {
  return d.server_enabled && d.reachability === "ok" && !d.reading_stale;
}

/**
 * The Power page's one-line state: how many UPS on how many NUT servers, and
 * whether the mains are up. Mains is claimed only when every UPS was read and
 * is online: one that is not is named for what it is (low battery on mains
 * during a forced shutdown, off, a status that parsed to nothing), and one
 * that was not read is counted as that. A UPS on battery and already low is
 * the most urgent, so it is said first and in those words, and counted once.
 */
export function powerSummaryLine(
  devices: UpsDevice[],
  serverCount: number,
): string {
  const servers = `${String(serverCount)} NUT server${serverCount === 1 ? "" : "s"}`;
  if (devices.length === 0) return `No UPS reported yet by ${servers}`;
  const read = devices.filter(upsRead);
  const draining = read.filter(upsOnBattery);
  const onBatteryLow = draining.filter((d) => d.state === "low_battery").length;
  const onBattery = draining.length - onBatteryLow;
  // Read, not on battery, and still not online.
  const rest = read.filter((d) => !upsOnBattery(d) && d.state !== "online");
  const lowBattery = rest.filter((d) => d.state === "low_battery").length;
  const off = rest.filter((d) => d.state === "off").length;
  const unknown = rest.length - lowBattery - off;
  const unread = devices.length - read.length;
  const state: string[] = [];
  if (onBatteryLow > 0) {
    state.push(`${String(onBatteryLow)} on battery and low`);
  }
  if (onBattery > 0) state.push(`${String(onBattery)} on battery`);
  if (lowBattery > 0) state.push(`${String(lowBattery)} low battery`);
  if (off > 0) state.push(`${String(off)} off`);
  if (unknown > 0) state.push(`${String(unknown)} unknown`);
  if (unread > 0) state.push(`${String(unread)} not read`);
  if (state.length === 0) {
    state.push(devices.length === 1 ? "on mains power" : "all on mains power");
  }
  return `${String(devices.length)} UPS on ${servers}, ${state.join(", ")}`;
}
