import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { GlobalHealthIndicator } from "./GlobalHealthIndicator";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import { useHealthMuteStore } from "@/stores/health-mute-store";
import type { ClusterResponse, HealthIssue } from "@/types/api";

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

function cephIssue(detail: string): HealthIssue {
  return {
    type: "ceph",
    severity: "warn",
    scope: "cluster",
    target: "",
    summary: "Ceph storage",
    detail,
  };
}

const MON_LOW = "mon pve1 is low on available space";

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
    useHealthMuteStore.setState({ mutedTypes: [] });
    mockUseClusters.mockReset();
  });
  afterEach(() => {
    localStorage.clear();
  });

  it("shows a healthy pill and message when there are no issues", async () => {
    setClusters([makeCluster("c1", { name: "Healthy" })]);
    renderWithProviders(<GlobalHealthIndicator />);

    expect(
      screen.getByRole("button", {
        name: /infrastructure health: all healthy/i,
      }),
    ).toBeInTheDocument();

    await openPopover();
    expect(screen.getByText("All systems healthy")).toBeInTheDocument();
  });

  it("surfaces an issue with its detail and singular grammar", async () => {
    setClusters([
      makeCluster("c1", { name: "Ceph", issues: [cephIssue(MON_LOW)] }),
    ]);
    renderWithProviders(<GlobalHealthIndicator />);

    expect(
      screen.getByRole("button", { name: /infrastructure health: 1 issue/i }),
    ).toBeInTheDocument();

    await openPopover();
    expect(screen.getByText("1 issue needs attention")).toBeInTheDocument();
    expect(screen.getByText("Ceph storage")).toBeInTheDocument();
    expect(screen.getByText(MON_LOW)).toBeInTheDocument();
  });

  it("pluralizes the subtitle for multiple issues", async () => {
    setClusters([
      makeCluster("c1", { name: "A", issues: [cephIssue(MON_LOW)] }),
      makeCluster("c2", {
        name: "B",
        issues: [
          {
            type: "disk_failed",
            severity: "err",
            scope: "node",
            target: "pve2",
            summary: "Disk SMART failure",
            detail: "/dev/sdb on pve2: FAILED",
          },
        ],
      }),
    ]);
    renderWithProviders(<GlobalHealthIndicator />);

    await openPopover();
    expect(screen.getByText("2 issues need attention")).toBeInTheDocument();
  });

  it("dismisses an issue, hides it, and offers Restore", async () => {
    setClusters([
      makeCluster("c1", { name: "Ceph", issues: [cephIssue(MON_LOW)] }),
    ]);
    renderWithProviders(<GlobalHealthIndicator />);

    const user = await openPopover();
    expect(screen.getByText(MON_LOW)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /dismiss/i }));

    expect(screen.queryByText(MON_LOW)).not.toBeInTheDocument();
    expect(screen.getByText("No active issues.")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /restore/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: /infrastructure health: all healthy/i,
      }),
    ).toBeInTheDocument();
    expect(localStorage.getItem("nexara-health-dismissed")).toContain("ceph");
  });

  it("restores a dismissed issue", async () => {
    setClusters([
      makeCluster("c1", { name: "Ceph", issues: [cephIssue(MON_LOW)] }),
    ]);
    renderWithProviders(<GlobalHealthIndicator />);

    const user = await openPopover();
    await user.click(screen.getByRole("button", { name: /dismiss/i }));
    expect(screen.queryByText(MON_LOW)).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /restore/i }));
    expect(screen.getByText(MON_LOW)).toBeInTheDocument();
  });

  it("mutes an alert type, hiding all issues of that type", async () => {
    setClusters([
      makeCluster("c1", {
        name: "CRJLAB",
        issues: [
          {
            type: "task_failed",
            severity: "warn",
            scope: "cluster",
            target: "",
            summary: "Failed tasks",
            detail: "1 failed qmigrate task in the last 24h",
          },
        ],
      }),
    ]);
    renderWithProviders(<GlobalHealthIndicator />);

    const user = await openPopover();
    const detail = "1 failed qmigrate task in the last 24h";
    expect(screen.getByText(detail)).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /^mute/i }));

    expect(screen.queryByText(detail)).not.toBeInTheDocument();
    expect(useHealthMuteStore.getState().mutedTypes).toContain("task_failed");
    expect(
      screen.getByRole("button", {
        name: /infrastructure health: all healthy/i,
      }),
    ).toBeInTheDocument();
  });
});
