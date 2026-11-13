import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import type { GuestSnapshotRow } from "../types/snapshots";
import { SnapshotsPage } from "./SnapshotsPage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const DAY = 86400;
const NOW_SEC = Math.floor(Date.now() / 1000);

function makeRow(over: Partial<GuestSnapshotRow>): GuestSnapshotRow {
  return {
    cluster_id: "c1",
    cluster_name: "cluster01",
    vmid: 100,
    guest_type: "qemu",
    vm_id: "vm-1",
    vm_name: "linux01",
    vm_status: "running",
    node: "pve-01",
    name: "snap01",
    description: "",
    parent: "",
    vmstate: false,
    snap_time: NOW_SEC - 2 * DAY,
    last_seen_at: new Date().toISOString(),
    ...over,
  };
}

// The header's sentence is built from the rows: how many, on how many guests,
// and the oldest bucket that has anything in it.
describe("SnapshotsPage", () => {
  it("says how many snapshots there are, on how many guests, and how many are old", async () => {
    const items = [
      makeRow({ name: "snap01", snap_time: NOW_SEC - 40 * DAY }),
      makeRow({ name: "snap02", snap_time: NOW_SEC - 10 * DAY }),
      makeRow({ name: "snap03", vmid: 101, vm_name: "linux02" }),
    ];
    vi.mocked(apiClient.page).mockResolvedValue({ items, total: items.length });

    renderWithProviders(<SnapshotsPage />);

    expect(
      await screen.findByText(/3 snapshots on 2 guests, 1 older than 30 days/),
    ).toBeInTheDocument();
  });
});
