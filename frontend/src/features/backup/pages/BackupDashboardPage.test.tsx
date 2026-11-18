import { beforeEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { ApiClientError, apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { PBSServer } from "../types/backup";
import { BackupDashboardPage } from "./BackupDashboardPage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const VIEW = ["view:pbs", "view:backup"];
const MANAGE = [
  ...VIEW,
  "manage:pbs",
  "delete:pbs",
  "manage:backup",
  "delete:backup",
];

function pbsServer(clusterId: string | null): PBSServer {
  return {
    id: "p1",
    name: "pbs01",
    api_url: "https://192.0.2.20:8007",
    token_id: "nexara@pbs!nexara",
    tls_fingerprint: "",
    cluster_id: clusterId,
    created_at: "",
    updated_at: "",
  };
}

const JOB = {
  id: "row-1",
  pbs_server_id: "p1",
  job_id: "job01",
  store: "datastore01",
  last_run_state: "ok",
};
// One of each row the server's tabs list.
const LISTS: Record<string, unknown[]> = {
  snapshots: [
    {
      id: "snap-1",
      pbs_server_id: "p1",
      datastore: "datastore01",
      backup_type: "vm",
      backup_id: "121",
      backup_time: 1700000000,
      size: 1024,
      verified: false,
      protected: false,
      comment: "",
      owner: "",
    },
  ],
  "sync-jobs": [JOB],
  "verify-jobs": [JOB],
};

const reachable = () =>
  Promise.resolve([{ store: "datastore01", total: 100, used: 10, avail: 90 }]);
const notFound = () =>
  Promise.reject(
    new ApiClientError(404, { error: "not_found", message: "Not found" }),
  );

// The page on these PBS servers, whose datastore status answers `status`, for
// a role with these grants (the global ones install-wide).
function mountPage(
  servers: PBSServer[],
  permissions: string[],
  globalPermissions: string[],
  status: () => Promise<unknown[]> = reachable,
) {
  useAuthStore.setState({ user: null, permissions, globalPermissions });
  vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
    const p = String(path);
    if (p === "/api/v1/pbs-servers") return Promise.resolve(servers);
    if (p.endsWith("/datastores/status")) return status();
    const list = /^\/api\/v1\/pbs-servers\/p1\/([a-z-]+)/.exec(p)?.[1];
    return Promise.resolve(LISTS[list ?? ""] ?? []);
  });
  vi.mocked(apiClient.get).mockRejectedValue(new Error("not stubbed"));
  renderWithProviders(<BackupDashboardPage />);
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("BackupDashboardPage", () => {
  // A PBS server's routes take their grant on the cluster it is attached to,
  // and install-wide when it is standalone: every tab's server-scoped controls
  // follow that rule.
  it.each<[string, string[], string[], string | null, boolean]>([
    ["a viewer", VIEW, VIEW, "c1", false],
    ["a cluster's manager, on its server", MANAGE, VIEW, "c1", true],
    ["a cluster's manager, on a standalone server", MANAGE, VIEW, null, false],
    [
      "an install-wide manager, on a standalone one",
      MANAGE,
      MANAGE,
      null,
      true,
    ],
  ])(
    "offers a server's actions to %s by its scope",
    async (_, permissions, globalPermissions, clusterId, offered) => {
      const user = setupUser();
      mountPage([pbsServer(clusterId)], permissions, globalPermissions);

      expect(
        await screen.findByRole("tab", { name: /^Snapshots/ }),
      ).toBeInTheDocument();
      // A role that may not edit the server reads its settings instead.
      expect(
        screen.queryByRole("button", { name: "Configuration" }) !== null,
      ).toBe(!offered);
      const tabs: [RegExp | null, string[]][] = [
        [null, ["Edit", "More actions for pbs01", "Run GC", "Prune"]],
        [/^Snapshots/, ["Protect", "Delete"]],
        [/^Replication/, ["Run sync job job01"]],
        [/^Verification/, ["Run verify job job01"]],
      ];
      for (const [tab, names] of tabs) {
        if (tab !== null) {
          await user.click(screen.getByRole("tab", { name: tab }));
        }
        for (const name of names) {
          expect(screen.queryByRole("button", { name }) !== null, name).toBe(
            offered,
          );
        }
      }
    },
  );

  // Its settings are view:pbs's to read: Configuration opens them read-only.
  it("opens a server's settings read-only to a viewer", async () => {
    const user = setupUser();
    mountPage([pbsServer("c1")], VIEW, VIEW);

    await user.click(
      await screen.findByRole("button", { name: "Configuration" }),
    );
    expect(
      await screen.findByRole("dialog", { name: "PBS server" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /save changes/i })).toBeNull();
    expect(screen.getByLabelText(/api token id/i)).toBeDisabled();
  });

  // Only a role that may act on it is told to: register a server, or check
  // an unreachable one's credentials.
  it.each<
    [string, PBSServer[], string[], () => Promise<unknown[]>, RegExp, RegExp]
  >([
    [
      "no server, to a role that may add one",
      [],
      MANAGE,
      reachable,
      /^Register a Proxmox Backup Server/,
      /has been added yet/,
    ],
    [
      "no server, to a viewer",
      [],
      VIEW,
      reachable,
      /has been added yet/,
      /^Register a Proxmox Backup Server/,
    ],
    [
      "an unreachable server, to its manager",
      [pbsServer("c1")],
      MANAGE,
      notFound,
      /Check credentials and connectivity/,
      /^Could not connect to the PBS server\.$/,
    ],
    [
      "an unreachable server, to a viewer",
      [pbsServer("c1")],
      VIEW,
      notFound,
      /^Could not connect to the PBS server\.$/,
      /Check credentials and connectivity/,
    ],
  ])(
    "says what to do for %s",
    async (_, servers, grants, status, shown, hidden) => {
      mountPage(servers, grants, grants, status);

      expect(await screen.findByText(shown)).toBeInTheDocument();
      expect(screen.queryByText(hidden)).toBeNull();
    },
  );
});
