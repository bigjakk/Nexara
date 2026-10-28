import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
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

const nowSeconds = () => Math.floor(Date.now() / 1000);

const task = (over: Partial<PBSTask>): PBSTask => ({
  upid: GC_UPID,
  node: "pbs",
  pid: 1,
  starttime: nowSeconds() - 3 * 86_400,
  worker_type: "garbage_collection",
  user: "root@pam!tok",
  ...over,
});

// The garbage collection finished three days ago, inside the History's default
// week and outside a day; the backup began a minute ago and is still running
// (no end, no status).
const gcTask = () =>
  task({
    starttime: nowSeconds() - 3 * 86_400,
    endtime: nowSeconds() - 3 * 86_400 + 60,
    status: "OK",
  });
const backupTask = () =>
  task({
    upid: BACKUP_UPID,
    worker_type: "backup",
    starttime: nowSeconds() - 60,
  });

// Three entries of a sixty-entry history: the one that records Nexara starting
// the garbage collection, and two others.
const firstPage = [
  makeHistoryEntry({
    id: "audit:1",
    resource_type: "pbs_server",
    action: "gc_triggered",
    category: "maintenance",
    user_display_name: "Alice",
    details: JSON.stringify({ pbs_upid: GC_UPID, datastore: "datastore01" }),
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

const GRANTS = ["view:audit", "view:task", "view:pbs", "view:backup"];

function mount() {
  return renderWithProviders(
    <Tabs defaultValue="history">
      <PBSHistoryTabContent pbsId="p1" />
    </Tabs>,
  );
}

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.test",
      display_name: "U",
      role: "user",
    },
    permissions: GRANTS,
    globalPermissions: GRANTS,
    isAuthenticated: true,
    isInitialized: true,
  });
  vi.mocked(apiClient.list).mockReset();
  vi.mocked(apiClient.list).mockResolvedValue([backupTask(), gcTask()]);
  // Sixty entries in all, so there is a second page for the footer to agree on.
  vi.mocked(apiClient.page).mockReset();
  vi.mocked(apiClient.page).mockImplementation((path) =>
    Promise.resolve(
      String(path).includes("offset=50")
        ? { items: [], total: 60 }
        : { items: firstPage, total: 60 },
    ),
  );
});

afterEach(() => {
  useAuthStore.setState({
    user: null,
    permissions: [],
    globalPermissions: [],
    isAuthenticated: false,
  });
});

// The server's own latest tasks sit above the first page, and a task Nexara
// started is one row — named by who started it — rather than two.
describe("a PBS server's History tab", () => {
  it("lists the server's tasks live and folds in the entries that record Nexara starting one", async () => {
    const user = setupUser();
    mount();

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
    // …and its entry is not a second row; the ones nothing lists are the page's.
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

  // The live rows are held to the time filter as the entries are, and an entry
  // is left out only for a task that is shown: the garbage collection began
  // before the last 24 hours, so its row goes and its entry comes back.
  it("holds the live tasks to the time filter", async () => {
    const user = setupUser();
    mount();
    expect(await screen.findAllByTestId("pbs-live-task")).toHaveLength(2);

    await user.selectOptions(screen.getByLabelText("Time"), "24h");

    expect(await screen.findAllByTestId("pbs-live-task")).toHaveLength(1);
    expect(screen.getByText("Garbage collection started")).toBeInTheDocument();
  });

  // Most of a server's rows are the backups' (snapshots, restores), which
  // view:backup owns beside the server's own view:pbs; and the server belongs
  // to no one cluster, so only the grants held on the whole install count.
  it.each([
    ["without view:backup", GRANTS.slice(0, 3), GRANTS.slice(0, 3)],
    ["held on one cluster only", GRANTS, []],
  ])("is not offered %s", async (_, permissions, globalPermissions) => {
    useAuthStore.setState({ permissions, globalPermissions });
    mount();

    await waitFor(() => {
      expect(apiClient.list).toHaveBeenCalled();
    });
    expect(screen.queryByTestId("pbs-live-task")).toBeNull();
    expect(apiClient.page).not.toHaveBeenCalled();
  });

  // TanStack keeps the last good data when a refetch fails, so the tasks it
  // still holds must not hide the entries while the block says it cannot read.
  it("shows every entry again when the server's tasks cannot be read", async () => {
    const { queryClient } = mount();
    await screen.findAllByTestId("pbs-live-task");
    expect(screen.queryByText("Garbage collection started")).toBeNull();

    vi.mocked(apiClient.list).mockRejectedValue(new Error("unreachable"));
    await act(() =>
      queryClient.refetchQueries({ queryKey: ["pbs-servers", "p1", "tasks"] }),
    );

    expect(
      await screen.findByText("Could not read the server's tasks."),
    ).toBeInTheDocument();
    expect(screen.queryByTestId("pbs-live-task")).toBeNull();
    expect(screen.getByText("Garbage collection started")).toBeInTheDocument();
  });
});
