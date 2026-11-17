import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState, type ReactElement } from "react";
import { toast } from "sonner";
import { ApiClientError, apiClient } from "@/lib/api-client";
import { describeError } from "@/lib/api-error";
import { useAuthStore } from "@/stores/auth-store";
import { createAppQueryClient } from "@/test/app-query-client";
import { ADMIN } from "@/test/fake-server";
import { signInAsAdmin, signOutForGood } from "@/test/late-toast-sessions";
import { flushInAct } from "@/test/save-outcome-kit";
import { renderWithProviders } from "@/test/test-utils";
import { CreateUserDialog } from "@/features/admin/components/CreateUserDialog";
import { ChannelsTable } from "@/features/alerts/components/ChannelsTable";
import { EditPBSServerDialog } from "@/features/backup/components/EditPBSServerDialog";
import { PoolCreateDialog } from "@/features/ceph/components/PoolCreateDialog";
import { NodePowerActions } from "@/features/clusters/components/node/NodePowerActions";
import { NodeShutdownConfirm } from "@/features/clusters/components/node/NodeShutdownConfirm";
import { DRSConfigCard } from "@/features/drs/components/DRSConfigCard";
import { GuestToolsPolicyCard } from "@/features/guest-tools/components/GuestToolsPolicyCard";
import { HAMaintenanceCard } from "@/features/ha/components/HAMaintenanceCard";
import { ReportRunsTable } from "@/features/reports/components/ReportRunsTable";
import { SSHCredentialsForm } from "@/features/rolling-updates/components/SSHCredentialsForm";
import { DeleteStorageDialog } from "@/features/storage/components/DeleteStorageDialog";
import { CreateFolderDialog } from "@/features/vms/components/CreateFolderDialog";
import type { PBSServer } from "@/features/backup/types/backup";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const REFUSED = "Refused for a reason";

/** What each read answers; every write is refused. */
const READS: Record<string, unknown> = {
  "/api/v1/notification-channels": [
    { id: "ch-1", name: "ops-webhook", channel_type: "webhook", enabled: true },
  ],
  "/api/v1/clusters": [],
  "/api/v1/clusters/c1/drs/config": {
    mode: "disabled",
    weights: { cpu: 0.5, memory: 0.5 },
    imbalance_threshold: 0.25,
    eval_interval_seconds: 300,
    boot_holdoff_minutes: 10,
    include_containers: false,
    exclude_veeam_workers: false,
  },
  "/api/v1/clusters/c1/guest-tools/config": {
    mode: "disabled",
    target_version: "",
    snapshot_before: false,
    max_concurrent: 5,
    iso_storage: "store01",
  },
  "/api/v1/virtio-win/releases": [],
  "/api/v1/reports/runs": [
    {
      id: "run-1",
      report_type: "resource_summary",
      cluster_id: "c1",
      status: "failed",
      created_at: "2026-11-01T00:00:00Z",
    },
  ],
  "/api/v1/clusters/c1/ssh-credentials": {
    cluster_id: "c1",
    username: "root",
    port: 22,
    auth_type: "password",
  },
  "/api/v1/clusters/c1/nodes": [{ id: "n1", name: "pve-01", status: "online" }],
  "/api/v1/clusters/c1/ssh-known-hosts": [],
  "/api/v1/clusters/c1/ssh-credentials/configured": { configured: false },
};

beforeEach(() => {
  vi.clearAllMocks();
  signInAsAdmin();
  useAuthStore.setState({ user: ADMIN, isAuthenticated: true });
  const read = (path: string) =>
    path in READS
      ? Promise.resolve(READS[path])
      : Promise.reject(new Error(`no read for ${path}`));
  vi.mocked(apiClient.get).mockImplementation(read);
  vi.mocked(apiClient.list).mockImplementation(read as never);
  const refuse = () =>
    Promise.reject(
      new ApiClientError(400, { error: "bad_request", message: REFUSED }),
    );
  vi.mocked(apiClient.post).mockImplementation(refuse);
  vi.mocked(apiClient.put).mockImplementation(refuse);
  vi.mocked(apiClient.delete).mockImplementation(refuse);
});

afterEach(() => {
  signOutForGood();
});

type User = ReturnType<typeof userEvent.setup>;

/** The last button of that name: a dialog's confirm, over its trigger. */
function last(name: string | RegExp): HTMLElement {
  const all = screen.getAllByRole("button", { name });
  return all[all.length - 1] as HTMLElement;
}

/** The node page's banner, as NodePowerActions gives NodeShutdownConfirm. */
function ShutdownHost() {
  const [shown, setShown] = useState("");
  return (
    <>
      <NodeShutdownConfirm
        clusterId="c1"
        nodeName="pve-01"
        open
        onError={(err) => {
          setShown(describeError(err));
        }}
      />
      <p>{shown}</p>
    </>
  );
}

// One consumer per feature folder: a failure it shows is shown there, and not
// also by the app's error toast (lib/query-client.ts cannot see an onError
// given to mutate()).
const SHOWN_IN_PLACE: [
  string,
  () => ReactElement,
  (u: User) => Promise<void>,
][] = [
  [
    "admin: Create user",
    () => <CreateUserDialog open onOpenChange={vi.fn()} />,
    async (u) => {
      await u.type(screen.getByLabelText("Email"), "alice@example.com");
      await u.type(screen.getByLabelText("Password"), "secret-01");
      await u.click(screen.getByRole("button", { name: "Create user" }));
    },
  ],
  [
    "alerts: a channel's Test",
    () => <ChannelsTable />,
    async (u) => {
      await u.click(await screen.findByRole("button", { name: /Test/ }));
    },
  ],
  [
    "backup: Edit PBS server",
    () => (
      <EditPBSServerDialog
        server={
          {
            id: "pbs-1",
            name: "pbs01",
            api_url: "https://pbs.example.com:8007",
            token_id: "nexara@pbs!token",
            tls_fingerprint: "AA:BB:CC:DD",
            cluster_id: null,
          } as unknown as PBSServer
        }
        open
        onOpenChange={vi.fn()}
      />
    ),
    async (u) => {
      await u.click(screen.getByRole("button", { name: "Save changes" }));
    },
  ],
  [
    // Rendered from the mutation's own state, settled through useSaveOutcome.
    "ceph: Create pool",
    () => <PoolCreateDialog clusterId="c1" osdCount={3} />,
    async (u) => {
      await u.click(screen.getByRole("button", { name: "Create pool" }));
      await u.type(screen.getByLabelText("Pool name"), "store01");
      await u.click(last("Create pool"));
    },
  ],
  [
    "clusters: a node's Shutdown",
    () => <ShutdownHost />,
    async (u) => {
      await u.click(screen.getByRole("button", { name: "Shutdown" }));
    },
  ],
  [
    "drs: Save Configuration",
    () => <DRSConfigCard clusterId="c1" />,
    async (u) => {
      await u.click(
        await screen.findByRole("button", { name: "Save Configuration" }),
      );
    },
  ],
  [
    "guest-tools: the policy's Save",
    () => <GuestToolsPolicyCard clusterId="c1" />,
    async (u) => {
      await u.click(await screen.findByRole("button", { name: "Save" }));
    },
  ],
  [
    "ha: Re-arm HA",
    () => <HAMaintenanceCard clusterId="c1" pveVersion="9.2.0" />,
    async (u) => {
      await u.click(screen.getByRole("button", { name: /Re-arm HA/ }));
      await u.click(last(/Re-arm HA/));
    },
  ],
  [
    "reports: a run's Delete",
    () => <ReportRunsTable />,
    async (u) => {
      await u.click(await screen.findByRole("button", { name: /^Actions/ }));
      await u.click(await screen.findByRole("menuitem", { name: /Delete/ }));
      await u.click(
        within(screen.getByRole("alertdialog")).getByRole("button", {
          name: "Delete",
        }),
      );
    },
  ],
  [
    "rolling-updates: Test connection",
    () => <SSHCredentialsForm clusterId="c1" />,
    async (u) => {
      const test = await screen.findByRole("button", {
        name: /Test connection/,
      });
      await vi.waitFor(() => {
        expect(test).toBeEnabled();
      });
      await u.click(test);
    },
  ],
  [
    "storage: Delete storage",
    () => (
      <DeleteStorageDialog
        clusterId="c1"
        storageId="s1"
        storageName="store01"
        open
        onOpenChange={vi.fn()}
      />
    ),
    async (u) => {
      await u.type(screen.getByPlaceholderText("store01"), "store01");
      await u.click(screen.getByRole("button", { name: "Delete storage" }));
    },
  ],
  [
    "vms: New folder",
    () => (
      <CreateFolderDialog
        open
        onOpenChange={vi.fn()}
        clusterId="c1"
        parentId={null}
        parentLabel="cluster01"
      />
    ),
    async (u) => {
      await u.type(screen.getByLabelText("Name"), "Production");
      await u.click(screen.getByRole("button", { name: "Create folder" }));
    },
  ],
];

describe("a failed save", () => {
  it.each(SHOWN_IN_PLACE)("is shown once, in place: %s", async (_, ui, act) => {
    renderWithProviders(ui(), { client: createAppQueryClient() });

    await act(userEvent.setup());

    expect(await screen.findByText(new RegExp(REFUSED))).toBeInTheDocument();
    await flushInAct();
    expect(toast.error).not.toHaveBeenCalled();
  });

  // A dialog that stays open on a failure shows it inside: the page's banner is
  // behind the modal, out of sight and out of a screen reader's reach.
  it("is shown inside the dialog that stays open over the page", async () => {
    renderWithProviders(
      <NodePowerActions
        clusterId="c1"
        nodeName="pve-01"
        otherNodes={["pve-02"]}
        inMaintenance={false}
        canReport={false}
      />,
      { client: createAppQueryClient() },
    );
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "More actions" }));
    await user.click(await screen.findByRole("menuitem", { name: /Evacuate/ }));
    await user.click(screen.getByRole("button", { name: "Evacuate" }));

    const dialog = screen.getByRole("dialog");
    expect(await within(dialog).findByText(REFUSED)).toBeInTheDocument();
    await flushInAct();
    expect(toast.error).not.toHaveBeenCalled();
  });

  // The control the rows above need: the same client does toast a failure that
  // nothing shows in place (NodePowerCard's Shutdown gives no onError).
  it("is toasted once when nothing shows it in place", async () => {
    renderWithProviders(
      <NodeShutdownConfirm clusterId="c1" nodeName="pve-01" open />,
      { client: createAppQueryClient() },
    );

    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Shutdown" }));

    await vi.waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith(REFUSED);
    });
    await flushInAct();
    expect(toast.error).toHaveBeenCalledTimes(1);
  });

  // useSaveOutcome: what the closed dialog can no longer show is toasted, once.
  it("that lands after its dialog was closed is toasted once, naming it", async () => {
    let refuse: (e: unknown) => void = () => undefined;
    vi.mocked(apiClient.post).mockImplementation(
      () =>
        new Promise((_resolve, reject) => {
          refuse = reject;
        }),
    );
    function Host() {
      const [open, setOpen] = useState(true);
      return (
        <CreateFolderDialog
          open={open}
          onOpenChange={setOpen}
          clusterId="c1"
          parentId={null}
          parentLabel="cluster01"
        />
      );
    }
    renderWithProviders(<Host />, { client: createAppQueryClient() });
    const user = userEvent.setup();
    await user.type(screen.getByLabelText("Name"), "Production");
    await user.click(screen.getByRole("button", { name: "Create folder" }));
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("dialog")).toBeNull();

    refuse(new ApiClientError(400, { error: "bad_request", message: REFUSED }));
    await flushInAct();

    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(toast.error).toHaveBeenCalledWith(
      `Creating folder Production failed: ${REFUSED}`,
    );
  });
});

// A dialog that stays mounted while closed and is not reset on opening: a save
// that lands after it closed puts the form away as onSuccess would have.
it("clears Create user's form for a create that lands after it closed", async () => {
  let answer: (v: unknown) => void = () => undefined;
  vi.mocked(apiClient.post).mockImplementation(
    () =>
      new Promise((resolve) => {
        answer = resolve;
      }),
  );
  function Host() {
    const [open, setOpen] = useState(true);
    return (
      <>
        <button
          type="button"
          onClick={() => {
            setOpen(true);
          }}
        >
          Reopen
        </button>
        <CreateUserDialog open={open} onOpenChange={setOpen} />
      </>
    );
  }
  renderWithProviders(<Host />, { client: createAppQueryClient() });
  const user = userEvent.setup();
  await user.type(screen.getByLabelText("Email"), "alice@example.com");
  await user.type(screen.getByLabelText("Password"), "secret-01");
  await user.click(screen.getByRole("button", { name: "Create user" }));
  await user.keyboard("{Escape}");

  answer({});
  await flushInAct();
  await user.click(screen.getByRole("button", { name: "Reopen" }));

  expect(screen.getByLabelText("Email")).toHaveValue("");
  expect(screen.getByLabelText("Password")).toHaveValue("");
});
