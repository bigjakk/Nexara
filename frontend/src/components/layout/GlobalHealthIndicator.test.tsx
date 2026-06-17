import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { GlobalHealthIndicator } from "./GlobalHealthIndicator";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import type { ClusterResponse } from "@/types/api";

vi.mock("@/features/dashboard/api/dashboard-queries", () => ({
  useClusters: vi.fn(),
}));

const mockUseClusters = vi.mocked(useClusters);

function setClusters(clusters: ClusterResponse[] | undefined): void {
  mockUseClusters.mockReturnValue({
    data: clusters,
  } as unknown as ReturnType<typeof useClusters>);
}

function makeCluster(
  id: string,
  over: Partial<ClusterResponse> = {},
): ClusterResponse {
  return {
    id,
    name: id,
    api_url: "",
    token_id: "",
    tls_fingerprint: "",
    sync_interval_seconds: 30,
    is_active: true,
    status: "online",
    pve_version: "",
    created_at: "",
    updated_at: "",
    ...over,
  };
}

function cephWarnCluster(
  id: string,
  name: string,
  message = "mon pve1 is low on available space",
): ClusterResponse {
  return makeCluster(id, {
    name,
    ceph_health: {
      status: "HEALTH_WARN",
      checks: [{ type: "MON_DISK_LOW", severity: "HEALTH_WARN", message }],
    },
  });
}

async function openPopover(): Promise<ReturnType<typeof userEvent.setup>> {
  const user = userEvent.setup();
  await user.click(
    screen.getByRole("button", { name: /infrastructure health/i }),
  );
  return user;
}

describe("GlobalHealthIndicator", () => {
  beforeEach(() => {
    localStorage.clear();
    useHealthDismissStore.setState({ dismissed: [] });
    mockUseClusters.mockReset();
  });
  afterEach(() => {
    localStorage.clear();
  });

  it("shows a healthy pill and message when there are no issues", async () => {
    setClusters([makeCluster("c1", { name: "Healthy", status: "online" })]);
    renderWithProviders(<GlobalHealthIndicator />);

    expect(
      screen.getByRole("button", {
        name: /infrastructure health: all healthy/i,
      }),
    ).toBeInTheDocument();

    await openPopover();
    expect(screen.getByText("All systems healthy")).toBeInTheDocument();
  });

  it("surfaces a Ceph warning with its reason and singular grammar", async () => {
    setClusters([cephWarnCluster("c1", "Ceph")]);
    renderWithProviders(<GlobalHealthIndicator />);

    expect(
      screen.getByRole("button", { name: /infrastructure health: 1 issue/i }),
    ).toBeInTheDocument();

    await openPopover();
    expect(screen.getByText("1 issue needs attention")).toBeInTheDocument();
    expect(screen.getByText("Ceph storage: Warning")).toBeInTheDocument();
    expect(
      screen.getByText("mon pve1 is low on available space"),
    ).toBeInTheDocument();
  });

  it("pluralizes the subtitle for multiple issues", async () => {
    setClusters([
      cephWarnCluster("c1", "CephA"),
      makeCluster("c2", { name: "CephB", status: "offline" }),
    ]);
    renderWithProviders(<GlobalHealthIndicator />);

    await openPopover();
    expect(screen.getByText("2 issues need attention")).toBeInTheDocument();
  });

  it("dismisses an issue, hides it, and offers Restore", async () => {
    setClusters([cephWarnCluster("c1", "Ceph")]);
    renderWithProviders(<GlobalHealthIndicator />);

    const user = await openPopover();
    expect(
      screen.getByText("mon pve1 is low on available space"),
    ).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /dismiss/i }));

    // Reason gone, pill back to healthy, and a Restore affordance appears.
    expect(
      screen.queryByText("mon pve1 is low on available space"),
    ).not.toBeInTheDocument();
    expect(screen.getByText("No active issues.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /restore/i })).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: /infrastructure health: all healthy/i,
      }),
    ).toBeInTheDocument();
    // Dismissal persisted (signature keyed by cluster + kind + reason).
    expect(localStorage.getItem("nexara-health-dismissed")).toContain(
      "c1:ceph",
    );
  });

  it("restores a dismissed issue", async () => {
    setClusters([cephWarnCluster("c1", "Ceph")]);
    renderWithProviders(<GlobalHealthIndicator />);

    const user = await openPopover();
    await user.click(screen.getByRole("button", { name: /dismiss/i }));
    expect(
      screen.queryByText("mon pve1 is low on available space"),
    ).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /restore/i }));
    expect(
      screen.getByText("mon pve1 is low on available space"),
    ).toBeInTheDocument();
  });
});
