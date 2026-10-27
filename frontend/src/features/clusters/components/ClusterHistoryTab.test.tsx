import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { makeHistoryEntry } from "@/test/history.fixtures";
import { ClusterHistoryTab } from "./ClusterHistoryTab";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);
const mockedList = vi.mocked(apiClient.list);

describe("ClusterHistoryTab", () => {
  // The cluster's route from the kind's default window; a guest's row named
  // as on its own History and linked to its page, a run by its count.
  it("reads the cluster's History, a guest's row as the guest's", async () => {
    mockedList.mockResolvedValue([
      { id: "v1", vmid: 100, name: "linux01", type: "qemu" },
    ]);
    mockedPage.mockResolvedValue({
      items: [
        makeHistoryEntry({
          id: "status:1",
          kind: "status",
          source: "detected",
          action: "status_change",
          resource_type: "status",
          from_status: "running",
          to_status: "stopped",
        }),
        makeHistoryEntry({
          id: "run:1",
          kind: "run",
          action: "sync_failed",
          resource_type: "cluster",
          vmid: 0,
          subject_vmid: 0,
          run_count: 3,
        }),
      ],
      total: 2,
    });
    renderWithProviders(<ClusterHistoryTab clusterId="c1" />);

    expect(await screen.findByText("Stopped, no task")).toBeInTheDocument();
    expect(screen.getByText("Sync failed, 3 times")).toBeInTheDocument();
    const url = new URL(String(mockedPage.mock.calls[0]?.[0]), "http://x");
    expect(url.pathname).toBe("/api/v1/clusters/c1/history");
    expect(url.searchParams.get("start_time")).not.toBeNull();
    expect(
      await screen.findByRole("link", { name: "linux01" }),
    ).toHaveAttribute("href", "/inventory/qemu/c1/v1");
  });
});
