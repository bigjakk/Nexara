import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { makeCluster } from "@/test/cluster.fixtures";
import { NeedsAttention } from "./NeedsAttention";
import { useClusters } from "../api/dashboard-queries";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";

vi.mock("../api/dashboard-queries", () => ({ useClusters: vi.fn() }));
vi.mock("@/features/power/api/ups-queries", () => ({
  useUpsDevices: () => ({ data: [] }),
}));

describe("NeedsAttention", () => {
  beforeEach(() => {
    useHealthDismissStore.setState({ dismissed: [] });
  });

  // The filtering itself is GlobalHealthIndicator.test's; this proves the
  // dashboard lists what that hook gives it and acts through it.
  it("lists an open issue with its way in, and drops it once dismissed", async () => {
    vi.mocked(useClusters).mockReturnValue({
      data: [
        makeCluster({
          id: "c1",
          name: "cluster01",
          issues: [
            {
              type: "ceph",
              severity: "warn",
              scope: "cluster",
              target: "",
              summary: "Ceph health warning",
              detail: "1 pool has too few placement groups",
            },
          ],
        }),
      ],
    } as unknown as ReturnType<typeof useClusters>);

    renderWithProviders(<NeedsAttention />);

    expect(screen.getByText("Ceph health warning")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open" })).toHaveAttribute(
      "href",
      "/clusters/c1?tab=ceph",
    );

    await userEvent.click(screen.getByRole("button", { name: /^Dismiss/ }));
    expect(screen.queryByText("Ceph health warning")).not.toBeInTheDocument();
  });
});
