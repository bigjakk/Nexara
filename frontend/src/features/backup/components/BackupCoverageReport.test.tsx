import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { BackupCoverageReport } from "./BackupCoverageReport";
import type { BackupCoverageEntry } from "../types/backup";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

function guest(
  vmid: number,
  name: string,
  coverage_status: BackupCoverageEntry["coverage_status"],
): BackupCoverageEntry {
  return {
    vmid,
    name,
    type: "qemu",
    status: "running",
    cluster_id: "c1",
    cluster_name: "cluster01",
    latest_backup: null,
    backup_count: 0,
    coverage_status,
    eligibility:
      coverage_status === "not_eligible" ? "veeam_worker" : "eligible",
    veeam_capable: true,
    veeam: null,
    protection:
      coverage_status === "recent"
        ? "pbs"
        : coverage_status === "not_eligible"
          ? "not_eligible"
          : "none",
  };
}

// The figures are the table's filter: pressing one lists only those guests,
// pressing it again lists them all.
it("filters the guests by the figure that is pressed", async () => {
  const user = setupUser();
  vi.mocked(apiClient.list).mockResolvedValue([
    guest(100, "linux01", "recent"),
    guest(101, "linux02", "stale"),
    guest(102, "vbr01-worker01", "not_eligible"),
  ]);
  renderWithProviders(<BackupCoverageReport />);

  const stale = await screen.findByRole("button", { name: /^Stale/ });
  expect(screen.getByText("linux01")).toBeInTheDocument();

  await user.click(stale);
  expect(stale).toHaveAttribute("aria-pressed", "true");
  expect(screen.queryByText("linux01")).toBeNull();
  expect(screen.getByText("linux02")).toBeInTheDocument();

  await user.click(stale);
  expect(screen.getByText("linux01")).toBeInTheDocument();

  // Backup targets counts every guest but Veeam's own, so pressing it lists
  // those and no others; All lists Veeam's own again.
  expect(screen.getByText("vbr01-worker01")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /^Backup targets/ }));
  expect(screen.queryByText("vbr01-worker01")).toBeNull();
  expect(screen.getByText("linux02")).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Coverage"), "all");
  expect(screen.getByText("vbr01-worker01")).toBeInTheDocument();
});
