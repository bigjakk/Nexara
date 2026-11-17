import { describe, expect, it } from "vitest";
import { spellOutKey } from "./spell-out-key";

describe("spellOutKey", () => {
  it.each([
    ["firewall_alias", "Firewall alias"],
    ["ha_resource", "HA resource"],
    ["pve_acl", "PVE ACL"],
    ["api_url", "API URL"],
    ["cluster_id", "Cluster ID"],
    ["firewall_ipset_entry", "Firewall IP set entry"],
    ["setting", "Setting"],
    ["constructor", "Constructor"],
    ["", ""],
  ])("spells %j as %j", (key, want) => {
    expect(spellOutKey(key)).toBe(want);
  });
});
