import { describe, expect, it } from "vitest";

import { haRequester, linkedTaskRole } from "./linked-task";

const upid = (type: string) =>
  `UPID:pve-01:0000A1B2:00000001:6A8CE407:${type}:100:root@pam:`;

describe("linkedTaskRole", () => {
  it.each([
    [
      "an HA request, by its task type",
      { upid: upid("hamigrate") },
      "ha-request",
    ],
    [
      "the HA manager's task, by its link to the request",
      { upid: upid("qmstart"), ha_request_upid: upid("hastart") },
      "ha-worker",
    ],
    [
      "a migration's start on its target, by its link to the migration",
      { upid: upid("qmstart"), migration_upid: upid("qmigrate") },
      "migration-start",
    ],
    // A start as root on the node, or at boot, looks the same without a link.
    ["a root@pam start nothing links", { upid: upid("qmstart") }, null],
    ["an entry with no task", {}, null],
  ])("is %s", (_, details, want) => {
    expect(linkedTaskRole(details)).toBe(want);
  });
});

describe("haRequester", () => {
  const request = upid("hastart");
  it.each([
    [
      "the actor of the request's row when the list holds it",
      { ha_request_upid: request, ha_requested_by: "admin@pam" },
      new Map([[request, "Ops"]]),
      "Ops",
    ],
    [
      "the PVE account the collector recorded, without that row",
      { ha_request_upid: request, ha_requested_by: "admin@pam" },
      new Map<string, string>(),
      "admin@pam",
    ],
    [
      "nobody when neither says",
      { ha_request_upid: request },
      new Map<string, string>(),
      "",
    ],
  ])("is %s", (_, details, rows, want) => {
    expect(haRequester(details, rows)).toBe(want);
  });
});
