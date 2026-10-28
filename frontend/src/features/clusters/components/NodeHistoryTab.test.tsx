import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { makeHistoryEntry } from "@/test/history.fixtures";
import { NodeHistoryTab } from "./NodeHistoryTab";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);
const mockedList = vi.mocked(apiClient.list);

describe("NodeHistoryTab", () => {
  // The node's route from the kind's last week, without its routine checks
  // or its guests' own activity; a move named by the node kind, its guest
  // linked to the guest's page, and dated as seen; a run dated from its first.
  it("reads the node's History, a guest's move linked to the guest", async () => {
    const user = setupUser();
    mockedList.mockResolvedValue([
      { id: "v1", vmid: 101, name: "linux01", type: "qemu" },
    ]);
    mockedPage.mockResolvedValue({
      items: [
        makeHistoryEntry({
          id: "move:1",
          kind: "move",
          source: "detected",
          action: "arrived",
          vmid: 101,
          subject_vmid: 0,
          node: "pve-02",
          last_seen_at: "2026-01-01T00:00:00Z",
        }),
        makeHistoryEntry({
          id: "move:2",
          kind: "move",
          source: "detected",
          action: "moved",
          vmid: 101,
          subject_vmid: 0,
          node: "pve-02",
          last_seen_at: "2026-01-01T00:00:00Z",
          run_count: 2,
        }),
      ],
      total: 2,
    });
    renderWithProviders(<NodeHistoryTab clusterId="c1" nodeName="pve-01" />);

    expect(await screen.findByText("Guest arrived")).toBeInTheDocument();
    const [link] = await screen.findAllByRole("link", { name: "linux01" });
    expect(link).toHaveAttribute("href", "/inventory/qemu/c1/v1");
    const url = new URL(String(mockedPage.mock.calls[0]?.[0]), "http://x");
    expect(url.pathname).toBe("/api/v1/clusters/c1/nodes/pve-01/history");
    expect(url.searchParams.get("start_time")).not.toBeNull();
    expect(url.searchParams.get("exclude_category")).toBe("checks");
    expect(url.searchParams.get("include_guests")).toBeNull();
    expect(
      screen.getByRole("checkbox", { name: "Include guest activity" }),
    ).toBeInTheDocument();

    await user.click(screen.getByText("Guest arrived"));
    expect(screen.getByText("Last seen before the move")).toBeInTheDocument();
    await user.click(screen.getByText("Moved 2 times more"));
    expect(screen.getByText("First")).toBeInTheDocument();
  });
});
