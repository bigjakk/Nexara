import { describe, expect, it } from "vitest";
import { formatDate } from "@/lib/format";
import { detailPairs } from "./audit-details";

describe("detailPairs", () => {
  // A Proxmox expiry is recorded as epoch seconds; it read as a raw number.
  it.each([
    { expire: 1_767_225_600, want: formatDate(1_767_225_600_000) },
    { expire: 0, want: "Never" },
  ])("shows expire $expire as a date", ({ expire, want }) => {
    expect(detailPairs(JSON.stringify({ expire }))).toEqual([
      ["Expires", want],
    ]);
  });
});
