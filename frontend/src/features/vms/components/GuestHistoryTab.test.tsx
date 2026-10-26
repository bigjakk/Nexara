import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import type { GuestHistoryEntry } from "../api/guest-history-queries";
import { GuestHistoryTab } from "./GuestHistoryTab";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);
const mockedList = vi.mocked(apiClient.list);

const MIGRATE_UPID =
  "UPID:pve-01:00001A2B:00003344:5F2E1A00:qmigrate:100:root@pam:";

const items: GuestHistoryEntry[] = [
  {
    id: "status:1",
    kind: "status",
    occurred_at: "2026-01-02T00:00:00Z",
    category: "power",
    source: "detected",
    action: "status_change",
    resource_type: "status",
    vmid: 100,
    user_id: "",
    user_email: "",
    user_display_name: "",
    details: "{}",
    upid: "",
    node: "pve-01",
    description: "",
    task_type: "",
    outcome: "",
    exit_status: "",
    finished_at: null,
    from_status: "running",
    to_status: "stopped",
  },
  {
    id: "audit:2",
    kind: "audit",
    occurred_at: "2026-01-01T00:00:00Z",
    category: "migration",
    source: "proxmox",
    action: "migrate",
    resource_type: "vm",
    vmid: 100,
    user_id: "00000000-0000-0000-0000-000000000001",
    user_email: "",
    user_display_name: "",
    details: JSON.stringify({ upid: MIGRATE_UPID, proxmox_user: "root@pam" }),
    upid: MIGRATE_UPID,
    node: "pve-01",
    description: "",
    task_type: "qmigrate",
    outcome: "failed",
    exit_status: "migration aborted",
    finished_at: "2026-01-01T00:01:00Z",
  },
];

describe("GuestHistoryTab", () => {
  beforeEach(() => {
    mockedPage.mockReset();
    mockedList.mockReset();
    mockedPage.mockResolvedValue({ items, total: 2 });
  });

  it("lists the guest's history, filters it on the server, and opens a task's log", async () => {
    const user = userEvent.setup({ delay: null });
    mockedList.mockResolvedValue([{ n: 1, t: "migration aborted by peer" }]);
    renderWithProviders(
      <GuestHistoryTab
        clusterId="c1"
        vmId="v1"
        vmid={100}
        showBlockJobs={false}
      />,
    );

    expect(await screen.findByText("Stopped, no task")).toBeInTheDocument();
    const migrate = screen.getByText("Migrate").closest("tr");
    expect(migrate).not.toBeNull();
    expect(
      within(migrate as HTMLElement).getByText("Failed"),
    ).toBeInTheDocument();
    expect(
      within(migrate as HTMLElement).getByText("root@pam"),
    ).toBeInTheDocument();
    expect(String(mockedPage.mock.calls[0]?.[0])).toBe(
      "/api/v1/clusters/c1/guests/100/history?limit=50&offset=0",
    );

    fireEvent.change(screen.getByLabelText("Type"), {
      target: { value: "migration" },
    });
    await vi.waitFor(() => {
      expect(String(mockedPage.mock.calls.at(-1)?.[0])).toContain(
        "category=migration",
      );
    });

    await user.click(screen.getByText("Migrate"));
    expect(
      await screen.findByText("migration aborted by peer"),
    ).toBeInTheDocument();
    expect(String(mockedList.mock.calls[0]?.[0])).toBe(
      `/api/v1/clusters/c1/tasks/${encodeURIComponent(MIGRATE_UPID)}/log`,
    );
  });
});
