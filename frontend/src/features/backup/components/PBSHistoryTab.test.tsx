import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { Tabs } from "@/components/ui/tabs";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { makeHistoryEntry } from "@/test/history.fixtures";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { PBSTask } from "../types/backup";
import { PBSHistoryTabContent } from "./PBSHistoryTab";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const GC_UPID = "UPID:pbs:1:2:3:4:garbage_collection:datastore01:root@pam!tok:";
const BACKUP_UPID =
  "UPID:pbs:1:2:3:5:backup:datastore01\\x3avm\\x2f100:root@pam!tok:";

const task = (over: Partial<PBSTask>): PBSTask => ({
  upid: GC_UPID,
  node: "pbs",
  pid: 1,
  starttime: 1_700_000_000,
  worker_type: "garbage_collection",
  user: "root@pam!tok",
  ...over,
});

afterEach(() => {
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
  });
});

// The server's own latest tasks sit above the first page, and a task Nexara
// started is one row — named by who started it — rather than two.
describe("a PBS server's History tab", () => {
  it("lists the server's tasks live and folds in the entries that record Nexara starting one", async () => {
    const user = setupUser();
    useAuthStore.setState({
      user: {
        id: "u1",
        email: "u@example.test",
        display_name: "U",
        role: "user",
      },
      permissions: ["view:audit", "view:task", "view:pbs"],
      isAuthenticated: true,
      isInitialized: true,
    });
    vi.mocked(apiClient.list).mockResolvedValue([
      task({ endtime: 1_700_000_060, status: "OK" }),
      // Still running: no end, no status.
      task({
        upid: BACKUP_UPID,
        worker_type: "backup",
        starttime: 1_700_000_100,
      }),
    ]);
    const firstPage = [
      makeHistoryEntry({
        id: "audit:1",
        resource_type: "pbs_server",
        action: "gc_triggered",
        category: "maintenance",
        user_display_name: "Alice",
        details: JSON.stringify({
          pbs_upid: GC_UPID,
          datastore: "datastore01",
        }),
      }),
      makeHistoryEntry({
        id: "audit:2",
        resource_type: "pbs_server",
        action: "snapshot_deleted",
        category: "snapshots",
      }),
      makeHistoryEntry({
        id: "audit:3",
        resource_type: "pbs_server",
        action: "snapshot_protected",
        category: "snapshots",
      }),
    ];
    // Sixty entries in all, so there is a second page for the footer to agree on.
    vi.mocked(apiClient.page).mockImplementation((path) =>
      Promise.resolve(
        String(path).includes("offset=50")
          ? { items: [], total: 60 }
          : { items: firstPage, total: 60 },
      ),
    );
    renderWithProviders(
      <Tabs defaultValue="history">
        <PBSHistoryTabContent pbsId="p1" />
      </Tabs>,
    );

    const live = await screen.findAllByTestId("pbs-live-task");
    expect(live).toHaveLength(2);
    // Newest first: the running backup, whose worker id is decoded.
    expect(
      within(live[0] as HTMLElement).getByText("datastore01:vm/100"),
    ).toBeInTheDocument();
    expect(
      within(live[0] as HTMLElement).getByText(/root@pam!tok/),
    ).toBeInTheDocument();
    // The garbage collection Nexara started is named by who started it…
    expect(
      within(live[1] as HTMLElement).getByText(/Started in Nexara by Alice/),
    ).toBeInTheDocument();
    // …and its entry is not a second row; the one nothing lists is the page's.
    expect(screen.queryByText("Garbage collection started")).toBeNull();
    expect(screen.getByText("Snapshot deleted")).toBeInTheDocument();
    // The footer keeps the server's total, and says how many of its entries
    // are shown above as live tasks — on the first page, which has them.
    expect(
      screen.getByText(/^60 entries · 1 shown above as a live task$/),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    await screen.findByText("Page 2 of 2");
    expect(screen.getByText("60 entries")).toBeInTheDocument();
  });
});
