import { describe, expect, it } from "vitest";
import { upsDevice } from "../power.fixtures";
import type { UpsDevice } from "../types/ups";
import { powerSummaryLine } from "./power-summary";

const onBattery: Partial<UpsDevice> = { state: "on_battery", flags: ["OB"] };
// Last seen on battery, unreadable now.
const unread: Partial<UpsDevice> = {
  state: "unknown",
  flags: ["OB"],
  reachability: "unreachable",
};

describe("powerSummaryLine", () => {
  it.each([
    ["no UPS yet", [], 1, "No UPS reported yet by 1 NUT server"],
    ["one on mains", [upsDevice()], 1, "1 UPS on 1 NUT server, on mains power"],
    [
      "all on mains",
      [upsDevice(), upsDevice({ id: "u2" })],
      2,
      "2 UPS on 2 NUT servers, all on mains power",
    ],
    [
      "an outage",
      [upsDevice(onBattery), upsDevice({ id: "u2" })],
      1,
      "2 UPS on 1 NUT server, 1 on battery",
    ],
    // On battery and already low is the urgent one: said first, counted once.
    [
      "low on battery beside a plain outage",
      [
        upsDevice(onBattery),
        upsDevice({ id: "u2", state: "low_battery", flags: ["OB", "LB"] }),
      ],
      1,
      "2 UPS on 1 NUT server, 1 on battery and low, 1 on battery",
    ],
    // A UPS that was not read is not read, and never an outage or on mains.
    [
      "unread UPS",
      [
        upsDevice(onBattery),
        upsDevice({ id: "u2", ...unread }),
        upsDevice({ id: "u3", server_enabled: false }),
      ],
      2,
      "3 UPS on 2 NUT servers, 1 on battery, 2 not read",
    ],
    // Read fine, no OB flag, and still not on mains as a healthy UPS is: a
    // forced shutdown on mains power, a UPS switched off, a status that
    // parsed to nothing. None of them lets the line claim mains.
    [
      "forced shutdown on mains",
      [upsDevice({ state: "low_battery", flags: ["OL", "FSD"] })],
      1,
      "1 UPS on 1 NUT server, 1 low battery",
    ],
    [
      "off and unknown beside one online",
      [
        upsDevice(),
        upsDevice({ id: "u2", state: "off", flags: ["OFF"] }),
        upsDevice({ id: "u3", state: "unknown", flags: [] }),
      ],
      1,
      "3 UPS on 1 NUT server, 1 off, 1 unknown",
    ],
  ])("%s", (_name, devices, servers, line) => {
    expect(powerSummaryLine(devices, servers)).toBe(line);
  });
});
