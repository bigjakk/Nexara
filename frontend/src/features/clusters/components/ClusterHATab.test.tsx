import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ClusterHATab } from "./ClusterHATab";
import type { HAGroup, HAResource } from "@/features/ha/api/ha-queries";

const CLUSTER = "cccccccc-0000-0000-0000-000000000003";

const listMock = vi.fn();
const getMock = vi.fn();
const putMock = vi.fn();
const postMock = vi.fn();

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      list: (path: string) => listMock(path) as unknown,
      get: (path: string) => getMock(path) as unknown,
      put: (path: string, body: unknown) => putMock(path, body) as unknown,
      post: (path: string, body: unknown) => postMock(path, body) as unknown,
      delete: vi.fn(),
    },
  };
});

// Whether the signed-in user may manage HA: the state column is a select for
// a manager and a badge for everyone else.
let mockCanManage = true;
vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => mockCanManage }),
}));

// A resource has a failback of its own only from PVE 9 (haResourceHasFailback).
const PVE9 = "9.0.6";
const PVE8 = "8.4.1";

// Resources as a PVE 9 cluster's raw config returns them: one on every
// default, where the three keys are ABSENT; one with explicit zeros, which
// Proxmox stores as written; one with explicit non-default values; and one
// that never set a state, which the API passes through as "".
const RESOURCES: HAResource[] = [
  { sid: "vm:101", type: "vm", state: "started", group: "", status: "" },
  {
    sid: "vm:102",
    type: "vm",
    state: "started",
    group: "",
    status: "",
    max_restart: 0,
    failback: 0,
  },
  {
    sid: "ct:103",
    type: "ct",
    state: "stopped",
    group: "",
    status: "",
    max_restart: 3,
    max_relocate: 2,
    failback: 1,
  },
  { sid: "vm:104", type: "vm", state: "", group: "", status: "" },
];

// The same guests on PVE 8, which has no resource failback to store.
const PVE8_RESOURCES: HAResource[] = [
  { sid: "vm:101", type: "vm", state: "started", group: "", status: "" },
  {
    sid: "vm:102",
    type: "vm",
    state: "started",
    group: "",
    status: "",
    max_restart: 0,
  },
];

// A guest not yet under HA, for the Add Resource dialog.
const UNMANAGED_VM = {
  id: "vm-row-105",
  cluster_id: CLUSTER,
  vmid: 105,
  name: "linux05",
  type: "qemu",
  template: false,
};

function serve(resources: HAResource[], groups: HAGroup[] = []) {
  listMock.mockImplementation((path: string) => {
    if (path === `/api/v1/clusters/${CLUSTER}/ha/resources`) {
      return Promise.resolve(resources);
    }
    if (path === `/api/v1/clusters/${CLUSTER}/ha/groups`) {
      return Promise.resolve(groups);
    }
    if (path === `/api/v1/clusters/${CLUSTER}/vms`) {
      return Promise.resolve([UNMANAGED_VM]);
    }
    return Promise.resolve([]);
  });
  getMock.mockResolvedValue({});
  putMock.mockResolvedValue(undefined);
  postMock.mockResolvedValue(undefined);
}

async function openResources(
  pveVersion: string,
  resources: HAResource[],
  groups: HAGroup[] = [],
) {
  serve(resources, groups);
  const user = userEvent.setup();
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  render(<ClusterHATab clusterId={CLUSTER} pveVersion={pveVersion} />, {
    wrapper,
  });
  await user.click(screen.getByRole("tab", { name: "Resources" }));
  await screen.findByText("vm:101");
  return user;
}

/** The Restart / Relocate and Failback cells of the row for sid. */
function settingsCells(sid: string) {
  const row = screen.getByText(sid).closest("tr");
  if (!(row instanceof HTMLElement)) throw new Error(`no row for ${sid}`);
  const cells = within(row).getAllByRole("cell");
  // Resource, State, Status, Group, Restart / Relocate, Failback, ...
  const counts = cells[4];
  const failback = cells[5];
  if (!counts || !failback) throw new Error(`row for ${sid} is too short`);
  return { counts, failback };
}

describe("ClusterHATab — HA resource settings", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockCanManage = true;
  });

  it("shows unset settings as Proxmox's defaults and set ones as themselves", async () => {
    await openResources(PVE9, RESOURCES);
    // The headers the cell indexes above rely on.
    expect(
      screen.getByRole("columnheader", { name: "Restart / Relocate" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("columnheader", { name: "Failback" }),
    ).toBeInTheDocument();

    const defaulted = settingsCells("vm:101");
    expect(defaulted.counts).toHaveTextContent(
      /^1 \(default\) \/ 1 \(default\)$/,
    );
    expect(defaulted.failback).toHaveTextContent(/^On \(default\)$/);

    const zeros = settingsCells("vm:102");
    expect(zeros.counts).toHaveTextContent(/^0 \/ 1 \(default\)$/);
    expect(zeros.failback).toHaveTextContent(/^Off$/);

    const explicit = settingsCells("ct:103");
    expect(explicit.counts).toHaveTextContent(/^3 \/ 2$/);
    expect(explicit.failback).toHaveTextContent(/^On$/);
  });

  it("edits through the tab without writing back the settings it did not touch", async () => {
    const user = await openResources(PVE9, RESOURCES);
    await user.click(
      screen.getByRole("button", { name: "Edit resource vm:102" }),
    );

    const dialog = await screen.findByRole("dialog");
    // The dialog opens on the resource's real values: the explicit 0 as 0,
    // the unset max_relocate as the default, failback off.
    expect(within(dialog).getByLabelText("Max Restart")).toHaveValue(0);
    expect(within(dialog).getByLabelText("Max Relocate")).toHaveValue(1);
    expect(
      within(dialog).getByRole("switch", { name: "Failback" }),
    ).not.toBeChecked();

    await user.type(within(dialog).getByLabelText("Comment"), "note");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    expect(putMock.mock.calls[0]).toEqual([
      `/api/v1/clusters/${CLUSTER}/ha/resources/vm%3A102`,
      { comment: "note" },
    ]);
  });

  it("shows a resource with no stored state as started to a manager", async () => {
    await openResources(PVE9, RESOURCES);
    const row = screen.getByText("vm:104").closest("tr");
    if (!(row instanceof HTMLElement)) throw new Error("no row for vm:104");
    expect(within(row).getByRole("combobox")).toHaveTextContent("started");
  });

  it("shows a resource with no stored state as started to a viewer", async () => {
    mockCanManage = false;
    await openResources(PVE9, RESOURCES);
    const row = screen.getByText("vm:104").closest("tr");
    if (!(row instanceof HTMLElement)) throw new Error("no row for vm:104");
    // Resource, State, ... — and no select for a viewer, only the badge.
    expect(within(row).queryByRole("combobox")).not.toBeInTheDocument();
    expect(within(row).getAllByRole("cell")[1]).toHaveTextContent(/^started$/);
  });

  it("has no failback column or switch on PVE 8, and an edit there never sends failback", async () => {
    const user = await openResources(PVE8, PVE8_RESOURCES);
    expect(
      screen.getByRole("columnheader", { name: "Restart / Relocate" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("columnheader", { name: "Failback" }),
    ).not.toBeInTheDocument();
    // The cell after the counts is the comment, not a failback badge.
    const row = screen.getByText("vm:102").closest("tr");
    if (!(row instanceof HTMLElement)) throw new Error("no row for vm:102");
    const cells = within(row).getAllByRole("cell");
    expect(cells[4]).toHaveTextContent(/^0 \/ 1 \(default\)$/);
    expect(cells[5]).toHaveTextContent(/^—$/);

    await user.click(
      screen.getByRole("button", { name: "Edit resource vm:102" }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(
      within(dialog).queryByRole("switch", { name: "Failback" }),
    ).not.toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Comment"), "note");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    expect(putMock.mock.calls[0]).toEqual([
      `/api/v1/clusters/${CLUSTER}/ha/resources/vm%3A102`,
      { comment: "note" },
    ]);
  });

  // The dialog must get the cluster's real version: hard-coding one would
  // send failback to PVE 8, or withhold it on PVE 9.
  it.each([
    { version: PVE8, resources: PVE8_RESOURCES, failback: {} },
    { version: PVE9, resources: RESOURCES, failback: { failback: 1 } },
  ])(
    "creates through Add Resource with the failback $version allows",
    async ({ version, resources, failback }) => {
      const user = await openResources(version, resources);
      await user.click(screen.getByRole("button", { name: "Add Resource" }));
      const dialog = await screen.findByRole("dialog");
      const [guestTrigger] = within(dialog).getAllByRole("combobox");
      if (!guestTrigger) throw new Error("guest select not rendered");
      await user.click(guestTrigger);
      await user.click(
        await screen.findByRole("option", { name: "vm:105 — linux05" }),
      );
      await user.click(within(dialog).getByRole("button", { name: "Create" }));

      await waitFor(() => {
        expect(postMock).toHaveBeenCalledTimes(1);
      });
      expect(postMock.mock.calls[0]).toEqual([
        `/api/v1/clusters/${CLUSTER}/ha/resources`,
        {
          sid: "vm:105",
          state: "started",
          max_restart: 1,
          max_relocate: 1,
          ...failback,
        },
      ]);
    },
  );

  it("offers no HA rules on PVE 8, which has no rules API, and offers groups", async () => {
    const user = await openResources(PVE8, PVE8_RESOURCES);
    await user.click(screen.getByRole("tab", { name: "Groups / Rules" }));

    expect(
      await screen.findByText("HA rules require Proxmox VE 9.0 or newer."),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add Rule" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText(/^HA Groups/)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Add Group" }),
    ).toBeInTheDocument();
  });

  it("offers HA rules on PVE 9, and no groups once they are migrated", async () => {
    const user = await openResources(PVE9, RESOURCES);
    await user.click(screen.getByRole("tab", { name: "Groups / Rules" }));

    expect(
      await screen.findByRole("button", { name: "Add Rule" }),
    ).toBeInTheDocument();
    expect(screen.getByText("No HA rules configured.")).toBeInTheDocument();
    expect(screen.queryByText(/^HA Groups/)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add Group" }),
    ).not.toBeInTheDocument();
  });

  // The version is "" until the cluster query resolves, and on a cluster that
  // never synced. Read as PVE 8, the tab told a PVE 9 cluster it was too old
  // for rules and offered group writes a migrated PVE 9 cluster refuses.
  it("says it is checking while the version is unknown, and offers no rule or group writes", async () => {
    const user = await openResources("", RESOURCES);
    await user.click(screen.getByRole("tab", { name: "Groups / Rules" }));

    expect(
      await screen.findByText("Checking the cluster's Proxmox VE version…"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("HA rules require Proxmox VE 9.0 or newer."),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add Rule" }),
    ).not.toBeInTheDocument();
    // Nothing to list, so no groups card either — and so no Add Group.
    expect(screen.queryByText(/^HA Groups/)).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add Group" }),
    ).not.toBeInTheDocument();
  });

  it("lists existing groups while the version is unknown, without offering to add or edit one", async () => {
    const user = await openResources("", RESOURCES, [
      {
        group: "ha-group01",
        nodes: "pve-01:100,pve-02",
        restricted: 0,
        nofailback: 0,
      },
    ]);
    await user.click(screen.getByRole("tab", { name: "Groups / Rules" }));

    expect(await screen.findByText("ha-group01")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Add Group" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Edit group ha-group01" }),
    ).not.toBeInTheDocument();
  });
});
