import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { stubApi } from "@/test/fetch-stub";
import type { CVEScan } from "@/types/api";
import { ScanHistoryTable } from "./ScanHistoryTable";

const CLUSTER = "c1";

function scan(id: string, startedAt: string): CVEScan {
  return {
    id,
    cluster_id: CLUSTER,
    status: "completed",
    total_nodes: 3,
    scanned_nodes: 3,
    total_vulns: 10,
    critical_count: 1,
    high_count: 2,
    medium_count: 3,
    low_count: 4,
    started_at: startedAt,
    completed_at: startedAt,
    created_at: startedAt,
  };
}

const scans = [
  scan("scan-1", "2026-09-01T02:00:00Z"),
  scan("scan-2", "2026-09-08T02:00:00Z"),
];

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  api = stubApi({});
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderTable() {
  renderWithProviders(
    <ScanHistoryTable
      scans={scans}
      clusterId={CLUSTER}
      onSelectScan={() => undefined}
    />,
  );
}

async function clickDelete(user: ReturnType<typeof userEvent.setup>) {
  const label = new Date("2026-09-08T02:00:00Z").toLocaleString();
  const row = screen.getByText(label).closest("tr");
  if (!row) throw new Error("scan row not rendered");
  await user.click(within(row).getByRole("button", { name: "Delete scan" }));
  return label;
}

describe("ScanHistoryTable — delete", () => {
  it("asks first, naming the scan, and sends nothing yet", async () => {
    const user = userEvent.setup();
    renderTable();
    const label = await clickDelete(user);

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent(`Delete the scan from ${label}?`);
    expect(dialog).toHaveTextContent(
      "every vulnerability finding it recorded are deleted",
    );
    expect(dialog).toHaveTextContent(
      "if none is left, reads as though no scan had ever run",
    );
    expect(api.writes()).toEqual([]);
    // The click did not also toggle the row open behind the dialog.
    expect(api.sent).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const user = userEvent.setup();
    renderTable();
    await clickDelete(user);

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming deletes exactly the scan clicked, once", async () => {
    const user = userEvent.setup();
    renderTable();
    await clickDelete(user);

    await user.click(
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([
        `DELETE /api/v1/clusters/${CLUSTER}/cve-scans/scan-2`,
      ]);
    });
    // Once: a second request arriving after the first would land here.
    await new Promise((r) => setTimeout(r, 100));
    expect(api.writes()).toEqual([
      `DELETE /api/v1/clusters/${CLUSTER}/cve-scans/scan-2`,
    ]);
  });
});
