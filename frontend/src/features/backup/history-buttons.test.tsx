import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import type { ReactElement } from "react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { useHistoryDrawerStore } from "@/stores/history-drawer-store";
import { HistoryDrawerHost } from "@/features/history/components/HistoryDrawer";
import { BackupJobTable } from "./components/BackupJobTable";
import { DatastoreCards } from "./components/DatastoreCards";
import { VeeamJobTable } from "./veeam/VeeamJobTable";
import { VeeamServerTable } from "./veeam/VeeamServerTable";
import { VeeamSessionTable } from "./veeam/VeeamSessionTable";
import { job, session } from "./veeam/veeam.fixtures";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const JOB = "3953c24f-bbe6-41fc-ae2f-a34e25bfd614";
const RUN = "20ff3c43-65c0-414d-ae03-10cf59fd2faa";

afterEach(() => {
  useHistoryDrawerStore.getState().resetSession();
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
  });
});

function signIn(permissions: string[]) {
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.test",
      display_name: "U",
      role: "user",
    },
    permissions,
    isAuthenticated: true,
    isInitialized: true,
  });
}

const server = {
  id: "srv-1",
  name: "veeam01",
  base_url: "https://192.0.2.10:9419",
  username: "",
  api_revision: "1.3-rev2",
  product_version: "13.1.0.411",
  license_edition: "EnterprisePlus",
  tls_fingerprint: "",
  verify_tls: true,
  enabled: true,
  last_sync_at: null,
  last_sync_error: "",
  created_at: "2026-08-26T19:56:00Z",
  updated_at: "2026-08-26T19:56:00Z",
};

// Each list row that has a History has a button for it: shown with the grants
// the History takes, and reading the object's own route. One row for each
// kind a list shows, the drawer being the host's.
describe("a list row's History button", () => {
  const rows: [string, ReactElement, string, string][] = [
    [
      "a datastore",
      <DatastoreCards
        key="d"
        pbsId="p1"
        datastores={[{ store: "datastore01", total: 100, used: 10, avail: 90 }]}
      />,
      "datastore01",
      "/api/v1/pbs-servers/p1/history?limit=50&offset=0&datastore=datastore01",
    ],
    [
      "a Veeam server",
      <VeeamServerTable
        key="s"
        servers={[server]}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />,
      "veeam01",
      "/api/v1/veeam-servers/srv-1/history?limit=50&offset=0",
    ],
    [
      "a Veeam job",
      <VeeamJobTable
        key="j"
        serverId="srv-1"
        jobs={[job({ veeam_id: JOB })]}
      />,
      "Daily-Backup",
      `/api/v1/veeam-servers/srv-1/jobs/${JOB}/history?limit=50&offset=0`,
    ],
    [
      "a Veeam run",
      <VeeamSessionTable
        key="r"
        serverId="srv-1"
        sessions={[session({ veeam_id: RUN })]}
      />,
      "Daily-Backup",
      `/api/v1/veeam-servers/srv-1/sessions/${RUN}/history?limit=50&offset=0`,
    ],
    [
      "a backup job",
      <BackupJobTable
        key="b"
        clusterId="c1"
        jobs={[{ id: "backup-1a2b3c4d", type: "vzdump", enabled: 1 }]}
      />,
      "backup-1a2b3c4d",
      "/api/v1/clusters/c1/backup-jobs/backup-1a2b3c4d/history?limit=50&offset=0",
    ],
  ];

  it.each(rows)("of %s opens its History", async (_, element, title, path) => {
    const user = setupUser();
    signIn([
      "view:audit",
      "view:task",
      "view:pbs",
      "view:veeam",
      "view:backup",
    ]);
    vi.mocked(apiClient.page).mockReset();
    vi.mocked(apiClient.page).mockResolvedValue({ items: [], total: 0 });
    vi.mocked(apiClient.list).mockResolvedValue([]);
    renderWithProviders(
      <>
        {element}
        <HistoryDrawerHost />
      </>,
    );

    expect(vi.mocked(apiClient.page)).not.toHaveBeenCalled();
    await user.click(
      screen.getByRole("button", { name: `History of ${title}` }),
    );
    await waitFor(() => {
      expect(String(vi.mocked(apiClient.page).mock.calls[0]?.[0])).toBe(path);
    });
  });

  it.each(rows)(
    "of %s is not offered without the object's own view permission",
    (_, element, title) => {
      signIn(["view:audit", "view:task"]);
      renderWithProviders(element);
      expect(
        screen.queryByRole("button", { name: `History of ${title}` }),
      ).toBeNull();
    },
  );
});
