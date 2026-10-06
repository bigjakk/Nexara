import { describe, it, expect, vi, beforeEach } from "vitest";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiClient } from "@/lib/api-client";
import { flush } from "@/test/fake-server";
import type { NodePCIDevice } from "@/features/vms/api/vm-queries";
import type { ClusterPCIMapping, MappingUsage } from "../api/mapping-queries";
import { PCIMappingsCard } from "./PCIMappingsCard";
import {
  CLUSTER,
  conflict,
  mappingRow,
  node,
  pciDevice,
  renderCard,
  setPermissions,
} from "./mappings-test-kit";

// The transport is mocked, not the hooks, so the real queries and mutations
// run and each test asserts the request that would leave the browser.
vi.mock("@/lib/api-client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api-client")>()),
  apiClient: {
    get: vi.fn(),
    list: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
  },
}));

const mockedGet = vi.mocked(apiClient.get);
const mockedList = vi.mocked(apiClient.list);
const mockedPut = vi.mocked(apiClient.put);
const mockedDelete = vi.mocked(apiClient.delete);

const LIST_URL = `/api/v1/clusters/${CLUSTER}/pci-mappings`;
const NODES_URL = `/api/v1/clusters/${CLUSTER}/nodes`;
const mappingURL = (id: string) => `${LIST_URL}/${id}`;
const devicesURL = (nodeName: string) =>
  `/api/v1/clusters/${CLUSTER}/nodes/${nodeName}/hardware/pci`;

const GPU_A = "id=1234:5678,iommugroup=14,node=pve-01,path=0000:01:00.0";
const GPU_B =
  "description=left slot,id=1234:5678,iommugroup=99,node=pve-01,path=0000:02:00.0";
const GPU_C = "id=1234:5678,iommugroup=14,node=pve-02,path=0000:01:00.0";
const ODD_1 = "node=pve-02,path=0000:07:00.0,foo=bar";
const ODD_2 = "path=0000:08:00.0";
// Why the server cannot read ODD_1, as it says it: without the entry itself.
const UNKNOWN_KEY =
  'The entry has an unknown key "foo"; the keys are node, path, id, subsystem-id, iommugroup and description.';

function mapping(partial: Partial<ClusterPCIMapping>): ClusterPCIMapping {
  return {
    id: "",
    description: "",
    map: [],
    digest: "d1",
    node_checks: {},
    unchecked: {},
    mdev: false,
    live_migration_capable: false,
    unreadable_entries: {},
    ...partial,
  };
}

// gpu01 has two entries on pve-01 — the second with a stale group and an
// entry description — clean there, and one on pve-02, where Proxmox reports
// an error. vgpu01 is one mdev entry, both flags set. nic01's node is
// offline. odd01 and odd02 hold entries Nexara cannot read, one and two.
function baseMappings(digest = "d1"): ClusterPCIMapping[] {
  return [
    mapping({
      id: "gpu01",
      description: "Example GPU",
      digest,
      map: [GPU_A, GPU_B, GPU_C],
      node_checks: {
        "pve-01": [],
        "pve-02": [
          {
            severity: "error",
            message:
              "Invalid configuration: 'id' does not match for 'gpu01' (1234:9999 != 1234:5678)",
          },
        ],
      },
    }),
    mapping({
      id: "vgpu01",
      digest,
      mdev: true,
      live_migration_capable: true,
      map: ["id=1234:0004,iommugroup=7,node=pve-01,path=0000:04:00.0"],
      node_checks: { "pve-01": [] },
    }),
    mapping({
      id: "nic01",
      digest,
      map: ["id=1234:0002,node=pve-03,path=0000:02:00.0"],
      unchecked: { "pve-03": "The node is offline." },
    }),
    mapping({
      id: "odd01",
      digest,
      map: ["id=1234:0006,node=pve-01,path=0000:06:00.0", ODD_1],
      node_checks: { "pve-01": [], "pve-02": [] },
      unreadable_entries: { [ODD_1]: UNKNOWN_KEY },
    }),
    mapping({
      id: "odd02",
      digest,
      map: ["id=1234:0008,node=pve-01,path=0000:08:00.0", ODD_1, ODD_2],
      node_checks: { "pve-01": [], "pve-02": [] },
      unreadable_entries: {
        [ODD_1]: UNKNOWN_KEY,
        [ODD_2]: "The entry needs node=<a Proxmox node name>.",
      },
    }),
  ];
}

let listing: ClusterPCIMapping[] = baseMappings();

// pve-01's devices as the node lists them: the GPU of gpu01's first entry,
// the device of its second (now in group 15), an mdev device, a storage
// controller, and a spare device alone in its group.
const nodeDevices: Record<string, NodePCIDevice[] | "unavailable"> = {
  "pve-01": [
    pciDevice({
      id: "0000:01:00.0",
      device: "0x5678",
      device_name: "Example GPU",
      iommugroup: 14,
    }),
    pciDevice({
      id: "0000:01:00.1",
      device: "0x5679",
      device_name: "Example GPU Audio",
      iommugroup: 14,
    }),
    pciDevice({
      id: "0000:02:00.0",
      device: "0x5678",
      device_name: "Example GPU",
      iommugroup: 15,
    }),
    pciDevice({
      id: "0000:04:00.0",
      device: "0x0004",
      device_name: "Example vGPU",
      iommugroup: 7,
      mdev: true,
    }),
    pciDevice({
      id: "0000:05:00.0",
      class: "0x010802",
      device: "0x0005",
      device_name: "Example NVMe",
      iommugroup: 20,
    }),
    pciDevice({
      id: "0000:09:00.0",
      device: "0x0009",
      device_name: "Example Spare",
      iommugroup: 30,
    }),
  ],
  "pve-02": "unavailable",
};

beforeEach(() => {
  vi.resetAllMocks();
  setPermissions(["view:cluster", "manage:cluster", "view:vm"]);
  listing = baseMappings();
  mockedList.mockImplementation((path: string) => {
    if (path === LIST_URL) return Promise.resolve(listing);
    if (path === NODES_URL) {
      return Promise.resolve([
        node("pve-01"),
        node("pve-02"),
        node("pve-03", "offline"),
      ]);
    }
    for (const [n, devs] of Object.entries(nodeDevices)) {
      if (path === devicesURL(n)) {
        return devs === "unavailable"
          ? Promise.reject(new Error("list failed"))
          : Promise.resolve(devs);
      }
    }
    return Promise.reject(new Error(`unexpected list ${path}`));
  });
  mockedPut.mockResolvedValue({ status: "ok" });
  mockedDelete.mockResolvedValue({ status: "ok" });
});

type User = ReturnType<typeof userEvent.setup>;

/** Presses the button of that name in a dialog. */
const clickIn = (user: User, el: HTMLElement, name: string) =>
  user.click(within(el).getByRole("button", { name }));

const card = () => <PCIMappingsCard clusterId={CLUSTER} />;

/**
 * An entry row's Replace device button once it is offered: it is held until
 * the cluster's nodes are known.
 */
async function replaceButton(row: HTMLElement) {
  const button = await within(row).findByRole("button", {
    name: /Replace device/,
  });
  await waitFor(() => {
    expect(button).toBeEnabled();
  });
  return button;
}

/** The entry row of mapping `id` whose device cell holds `text`. */
async function entryRow(id: string, text: string) {
  const header = await mappingRow(id);
  let next = header.nextElementSibling;
  while (
    next instanceof HTMLElement &&
    !next.classList.contains("bg-muted/30")
  ) {
    if (next.textContent.includes(text)) return next;
    next = next.nextElementSibling;
  }
  throw new Error(`no entry row with ${text} in ${id}`);
}

async function openIn(
  user: User,
  button: Promise<HTMLElement>,
  role: "dialog" | "alertdialog",
) {
  await user.click(await button);
  return screen.findByRole(role);
}
const inRow = async (row: Promise<HTMLElement>, name: RegExp) =>
  within(await row).getByRole("button", { name });
const openAddDevice = (user: User, id = "gpu01") =>
  openIn(user, inRow(mappingRow(id), /Add device/), "dialog");
const openEdit = (user: User, id = "gpu01") =>
  openIn(user, inRow(mappingRow(id), /Edit description/), "dialog");
const openReplace = async (user: User, id: string, text: string) =>
  openIn(user, replaceButton(await entryRow(id, text)), "dialog");
const openRemove = (user: User, id: string, text: string) =>
  openIn(user, inRow(entryRow(id, text), /Remove/), "alertdialog");

/** Add device on gpu01, with the node picked: the device list is next. */
async function addDeviceOn(user: User, nodeName: string) {
  const dialog = await openAddDevice(user);
  await user.selectOptions(within(dialog).getByLabelText("Node"), nodeName);
  return dialog;
}

const save = (dialog: HTMLElement) =>
  within(dialog).getByRole("button", { name: "Save" });
const expectPut = (id: string, body: unknown) =>
  waitFor(() => {
    expect(mockedPut).toHaveBeenCalledWith(mappingURL(id), body);
  });
const ACK = "The node does not need it; pass it through";

describe("PCIMappingsCard listing", () => {
  it("groups a mapping's entries by node with the status once per node, never shows an unchecked node as OK, and shows the flags", async () => {
    renderCard(card());
    const first = await entryRow("gpu01", "0000:01:00.0");
    expect(within(first).getByText("pve-01")).toBeInTheDocument();
    expect(
      within(first).getByText(
        /2 devices: a VM starting here takes the first one not in use/,
      ),
    ).toBeInTheDocument();
    expect(within(first).getByText("OK")).toBeInTheDocument();
    // The second entry shares pve-01's node and status cells.
    const second = await entryRow("gpu01", "0000:02:00.0");
    expect(second.querySelectorAll("td")).toHaveLength(3);
    expect(within(second).getByText("left slot")).toBeInTheDocument();
    expect(within(second).getByText("99")).toBeInTheDocument();
    // pve-02's one entry, and the error Proxmox reports there.
    const pve02 = await entryRow("gpu01", "pve-02");
    expect(
      within(pve02).getByText(/'id' does not match for 'gpu01'/),
    ).toBeInTheDocument();
    expect(
      within(pve02).getByText(
        /An entry that fails stops every VM using this mapping from starting on this node/,
      ),
    ).toBeInTheDocument();
    expect(within(pve02).queryByText("OK")).not.toBeInTheDocument();

    const nic = await entryRow("nic01", "0000:02:00.0");
    expect(
      within(nic).getByText("Not checked: The node is offline."),
    ).toBeInTheDocument();
    expect(within(nic).queryByText("OK")).not.toBeInTheDocument();

    const vgpu = await mappingRow("vgpu01");
    expect(within(vgpu).getByText("Mediated devices")).toBeInTheDocument();
    expect(within(vgpu).getByText("Live migration")).toBeInTheDocument();
    expect(
      within(await mappingRow("gpu01")).queryByText("Mediated devices"),
    ).not.toBeInTheDocument();
  });

  it("shows an entry Nexara cannot read, and offers only its removal", async () => {
    renderCard(card());
    const header = await mappingRow("odd01");
    expect(
      within(header).getByText(
        /Nexara cannot read one of this mapping's entries/,
      ),
    ).toBeInTheDocument();
    expect(
      within(header).getByRole("button", { name: /Add device/ }),
    ).toBeDisabled();
    expect(
      within(header).getByRole("button", { name: /Edit description/ }),
    ).toBeDisabled();
    expect(
      within(header).getByRole("button", { name: /Delete/ }),
    ).toBeEnabled();

    // The readable entry's own edits wait for the unreadable one to go —
    // once the cluster's nodes are known, so that is not why.
    await replaceButton(await entryRow("gpu01", "0000:01:00.0"));
    const good = await entryRow("odd01", "0000:06:00.0");
    expect(
      within(good).getByRole("button", { name: /Replace device/ }),
    ).toBeDisabled();
    expect(within(good).getByRole("button", { name: /Remove/ })).toBeDisabled();

    // Its node is one of the cluster's: only unreadability leaves it no
    // Replace.
    const odd = await entryRow("odd01", "foo=bar");
    expect(
      within(odd).getByText(
        /Nexara cannot read this entry\. The entry has an unknown key "foo"/,
      ),
    ).toBeInTheDocument();
    expect(
      within(odd).queryByRole("button", { name: /Replace device/ }),
    ).not.toBeInTheDocument();
    expect(within(odd).getByRole("button", { name: /Remove/ })).toBeEnabled();
  });

  it("offers no Replace on a node the cluster does not have", async () => {
    listing = [
      mapping({
        id: "gpu01",
        map: [GPU_A, "id=1234:5678,node=pve-09,path=0000:01:00.0"],
        node_checks: { "pve-01": [] },
        unchecked: { "pve-09": "This cluster has no node of that name." },
      }),
    ];
    renderCard(card());
    // pve-01's Replace is offered once the cluster's nodes are known.
    await replaceButton(await entryRow("gpu01", "pve-01"));
    const stray = await entryRow("gpu01", "pve-09");
    expect(
      within(stray).getByText(
        "Not checked: This cluster has no node of that name.",
      ),
    ).toBeInTheDocument();
    expect(
      within(stray).queryByRole("button", { name: /Replace device/ }),
    ).not.toBeInTheDocument();
    expect(within(stray).getByRole("button", { name: /Remove/ })).toBeEnabled();
  });

  /** The nodes listing fails; everything else answers as usual. */
  function nodesFail() {
    const listed = mockedList.getMockImplementation();
    mockedList.mockImplementation((path) =>
      path === NODES_URL
        ? Promise.reject(new Error("nodes failed"))
        : (listed?.(path) ?? Promise.reject(new Error(path))),
    );
  }

  it("holds Replace until the cluster's nodes are known, and says why when they cannot be read", async () => {
    nodesFail();
    const user = userEvent.setup();
    renderCard(card());
    expect(
      await screen.findByText(
        /Could not load the cluster's nodes, which Replace device needs/,
      ),
    ).toBeInTheDocument();
    const row = await entryRow("gpu01", "0000:01:00.0");
    const replace = within(row).getByRole("button", { name: /Replace device/ });
    expect(replace).toBeDisabled();
    expect(within(row).getByRole("button", { name: /Remove/ })).toBeEnabled();
    await user.click(replace);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("says nothing about the cluster's nodes to a user who cannot change mappings", async () => {
    setPermissions(["view:cluster"]);
    nodesFail();
    const queryClient = renderCard(card());
    await mappingRow("gpu01");
    await waitFor(() => {
      expect(
        queryClient.getQueryState(["clusters", CLUSTER, "nodes"])?.status,
      ).toBe("error");
    });
    // Past the render that follows the failure.
    await act(flush);
    expect(
      screen.queryByText(/Could not load the cluster's nodes/),
    ).not.toBeInTheDocument();
  });

  it("shows an entry's node without the control characters it may hold", async () => {
    const odd = "node=pve-\u202e01,path=0000:07:00.0,foo=bar";
    listing = [
      mapping({
        id: "odd03",
        map: [odd],
        unreadable_entries: { [odd]: UNKNOWN_KEY },
      }),
    ];
    renderCard(card());
    const nodeCell = (await entryRow("odd03", "foo=bar")).querySelector("td");
    expect(nodeCell?.textContent).toBe("pve- 01");
  });

  it("offers only Delete for a mapping with no entries, and says why", async () => {
    listing = [mapping({ id: "empty01" })];
    renderCard(card());
    const header = await mappingRow("empty01");
    expect(
      within(header).getByRole("button", { name: /Add device/ }),
    ).toBeDisabled();
    expect(
      within(header).getByRole("button", { name: /Edit description/ }),
    ).toBeDisabled();
    expect(
      within(header).getByRole("button", { name: /Delete/ }),
    ).toBeEnabled();
    expect(
      screen.getByText(/Nexara saves a mapping only with at least one entry/),
    ).toBeInTheDocument();
  });

  it("is read-only without manage:cluster, and says why", async () => {
    setPermissions(["view:cluster"]);
    renderCard(card());
    await mappingRow("gpu01");
    expect(
      screen.getByText(
        /Viewing only: changing mappings needs the Manage Cluster permission/,
      ),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Add device/ }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Remove/ }),
    ).not.toBeInTheDocument();
  });

  it("reports a failed listing rather than an empty one", async () => {
    mockedList.mockImplementation((path: string) =>
      path === LIST_URL
        ? Promise.reject(new Error("list failed"))
        : Promise.resolve([]),
    );
    renderCard(card());
    expect(
      await screen.findByText(/Could not load the PCI mappings/),
    ).toBeInTheDocument();
  });
});

describe("PCIMappingsCard edits", () => {
  it("Add device sends every entry as listed, the node and the address, with the listing's digest", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await addDeviceOn(user, "pve-01");
    const devices = await within(dialog).findByLabelText("Device");
    // The node's entries already name these.
    for (const name of [
      "0000:01:00.0 — Example GPU",
      "0000:02:00.0 — Example GPU",
    ]) {
      expect(within(devices).getByRole("option", { name })).toBeDisabled();
    }
    await user.selectOptions(devices, "0000:09:00.0");
    await user.click(save(dialog));

    await expectPut("gpu01", {
      map: [GPU_A, GPU_B, GPU_C],
      add_node: "pve-01",
      add_path: "0000:09:00.0",
      digest: "d1",
    });
  });

  it("will not add the whole device when one of its functions is already an entry", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await addDeviceOn(user, "pve-01");
    const devices = await within(dialog).findByLabelText("Device");
    const audio = within(devices).getByRole("option", {
      name: "0000:01:00.1 — Example GPU Audio",
    });
    expect(audio).toBeEnabled();
    // Picked as a function, then made the whole device: the pick stays, and
    // is refused for what it now overlaps.
    await user.selectOptions(devices, "0000:01:00.1");
    await user.click(within(dialog).getByLabelText(/All functions/));
    expect(audio).toBeDisabled();
    expect(
      within(dialog).getByText(
        /pve-01 already has 0000:01:00.0 in this mapping/,
      ),
    ).toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
  });

  it("forgets the pick when the node changes: no device carries over to another node", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await addDeviceOn(user, "pve-01");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:09:00.0",
    );
    await user.click(within(dialog).getByLabelText(/All functions/));
    await user.selectOptions(within(dialog).getByLabelText("Node"), "pve-02");
    // pve-02's devices cannot be listed, so an address is typed there.
    expect(
      await within(dialog).findByText(/Could not list the node's PCI devices/),
    ).toBeInTheDocument();
    expect(within(dialog).getByLabelText("Device")).toHaveValue("");
    expect(within(dialog).getByLabelText(/All functions/)).not.toBeChecked();
    expect(save(dialog)).toBeDisabled();
  });

  it("says the node's devices are loading, rather than asking for an address", async () => {
    const listed = mockedList.getMockImplementation();
    mockedList.mockImplementation((path) =>
      path === devicesURL("pve-01")
        ? new Promise<NodePCIDevice[]>(() => undefined)
        : (listed?.(path) ?? Promise.reject(new Error(path))),
    );
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await addDeviceOn(user, "pve-01");
    expect(
      within(dialog).getByText("Loading the node's PCI devices…"),
    ).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Device")).not.toBeInTheDocument();
  });

  it("asks before adding a device the node may need", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await addDeviceOn(user, "pve-01");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:05:00.0",
    );
    expect(
      within(dialog).getByText(/0000:05:00.0 is a storage controller/),
    ).toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
    await user.click(within(dialog).getByLabelText(ACK));
    expect(save(dialog)).toBeEnabled();
  });

  it("refuses a device whose mdev capability the mapping's flag does not match, and says which way", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await addDeviceOn(user, "pve-01");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:04:00.0",
    );
    expect(
      within(dialog).getByText(
        /0000:04:00.0 can provide mediated devices, but gpu01 is not set to use them/,
      ),
    ).toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
  });

  it("Replace on the only entry says the flag will follow the device, and sends the entry it replaces", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openReplace(user, "vgpu01", "0000:04:00.0");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:09:00.0",
    );
    expect(
      within(dialog).getByText(
        /Saving also turns vgpu01's "Use with mediated devices" flag off to match 0000:09:00.0/,
      ),
    ).toBeInTheDocument();
    await user.click(save(dialog));
    await expectPut("vgpu01", {
      map: ["id=1234:0004,iommugroup=7,node=pve-01,path=0000:04:00.0"],
      add_node: "pve-01",
      add_path: "0000:09:00.0",
      replace: "id=1234:0004,iommugroup=7,node=pve-01,path=0000:04:00.0",
      digest: "d1",
    });
  });

  it("Replace with the same device brings a stale group up to date, and will not save one already current", async () => {
    const user = userEvent.setup();
    renderCard(card());
    let dialog = await openReplace(user, "gpu01", "0000:01:00.0");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:01:00.0",
    );
    expect(
      within(dialog).getByText("That is what the entry passes now."),
    ).toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
    await clickIn(user, dialog, "Cancel");

    dialog = await openReplace(user, "gpu01", "0000:02:00.0");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:02:00.0",
    );
    await user.click(save(dialog));
    await expectPut("gpu01", {
      map: [GPU_A, GPU_B, GPU_C],
      add_node: "pve-01",
      add_path: "0000:02:00.0",
      replace: GPU_B,
      digest: "d1",
    });
  });

  it("Replace with the same device repairs an entry Proxmox refuses for its spelling", async () => {
    // Group 15 written "015": Proxmox compares the group as a string.
    const respelled = GPU_B.replace("iommugroup=99", "iommugroup=015");
    listing = [
      mapping({
        id: "gpu01",
        map: [GPU_A, respelled, GPU_C],
        node_checks: {
          "pve-01": [
            {
              severity: "error",
              message:
                "Invalid configuration: 'iommugroup' does not match for 'gpu01' (15 != 015)",
            },
          ],
          "pve-02": [],
        },
      }),
    ];
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openReplace(user, "gpu01", "0000:02:00.0");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:02:00.0",
    );
    expect(
      within(dialog).queryByText("That is what the entry passes now."),
    ).not.toBeInTheDocument();
    await user.click(save(dialog));
    await expectPut("gpu01", {
      map: [GPU_A, respelled, GPU_C],
      add_node: "pve-01",
      add_path: "0000:02:00.0",
      replace: respelled,
      digest: "d1",
    });
  });

  it("Replace refuses a device whose mdev capability the flag does not match while other entries remain", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openReplace(user, "gpu01", "0000:01:00.0");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:04:00.0",
    );
    expect(
      within(dialog).getByText(
        /0000:04:00.0 can provide mediated devices, but gpu01 is not set to use them/,
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByText(/Saving also turns/),
    ).not.toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
  });

  it("stops at a node that leaves the cluster while Replace is open", async () => {
    const user = userEvent.setup();
    const queryClient = renderCard(card());
    const dialog = await openReplace(user, "gpu01", "0000:02:00.0");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:09:00.0",
    );
    queryClient.setQueryData(
      ["clusters", CLUSTER, "nodes"],
      [node("pve-02"), node("pve-03", "offline")],
    );
    expect(
      await within(dialog).findByText(
        /pve-01 is not one of the cluster's nodes now/,
      ),
    ).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Device")).not.toBeInTheDocument();
    // The pick made before is not weighed against a node no longer there.
    expect(
      within(dialog).queryByText(/Nexara cannot tell what/),
    ).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(ACK)).not.toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
  });

  it("after a re-read, an entry to replace that Nexara can no longer read counts as gone", async () => {
    mockedPut.mockRejectedValueOnce(conflict("PCI"));
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openReplace(user, "gpu01", "0000:02:00.0");
    // The same device: its entry's group is stale.
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "0000:02:00.0",
    );
    expect(within(dialog).getByText(/Now:/)).toBeInTheDocument();
    // Someone gave the entry a key Nexara does not know.
    const broken = `${GPU_B},foo=bar`;
    listing = baseMappings("d2").map((m) =>
      m.id === "gpu01"
        ? {
            ...m,
            map: [GPU_A, broken, GPU_C],
            unreadable_entries: { [broken]: UNKNOWN_KEY },
          }
        : m,
    );
    await user.click(save(dialog));
    expect(
      await within(dialog).findByText(/This dialog now shows them as they are/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(/This entry changed since it was loaded/),
    ).toBeInTheDocument();
    expect(within(dialog).queryByText(/Now:/)).not.toBeInTheDocument();
    // Nor is what it says trusted to refuse the pick: it only holds Save.
    expect(within(dialog).queryByText(/already has/)).not.toBeInTheDocument();
    expect(
      within(dialog).getByText(/Nexara cannot read an entry of gpu01/),
    ).toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
  });

  it("takes a typed address when the node's devices cannot be listed, and asks about it", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openReplace(user, "gpu01", "pve-02");
    expect(
      await within(dialog).findByText(/Could not list the node's PCI devices/),
    ).toBeInTheDocument();
    await user.type(within(dialog).getByLabelText("Device"), "0000:0b:00.0");
    expect(
      within(dialog).getByText(/Nexara cannot tell what 0000:0b:00.0 is/),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByLabelText(ACK));
    await user.click(save(dialog));
    await expectPut("gpu01", {
      map: [GPU_A, GPU_B, GPU_C],
      add_node: "pve-02",
      add_path: "0000:0b:00.0",
      replace: GPU_C,
      digest: "d1",
    });
  });

  it("Edit description sends the entries unchanged and the new description", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openEdit(user);
    const input = within(dialog).getByLabelText("Description");
    await user.clear(input);
    await user.type(input, "  Example accelerator ");
    await user.click(save(dialog));
    await expectPut("gpu01", {
      map: [GPU_A, GPU_B, GPU_C],
      description: "Example accelerator",
      digest: "d1",
    });
  });

  it("a 409 re-reads the mappings, shows them, and pins the new read", async () => {
    mockedPut.mockRejectedValueOnce(conflict("PCI"));
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openEdit(user);
    listing = baseMappings("d2");
    await user.type(within(dialog).getByLabelText("Description"), "!");
    await user.click(save(dialog));
    expect(
      await within(dialog).findByText(/This dialog now shows them as they are/),
    ).toBeInTheDocument();
    await user.click(save(dialog));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenLastCalledWith(
        mappingURL("gpu01"),
        expect.objectContaining({ digest: "d2" }),
      );
    });
  });
});

describe("PCIMappingsCard remove and delete", () => {
  // The confirmation closes onto the card, not the page: the Remove button
  // that opened it is held while the write is read back.
  it("Remove drops one of a node's entries, says the node keeps the other, and puts focus on the card", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openRemove(user, "gpu01", "0000:02:00.0");
    expect(
      within(dialog).getByText("Remove 0000:02:00.0 on pve-01 from gpu01?"),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        /pve-01 keeps its other entry: a VM using gpu01 there is given that device/,
      ),
    ).toBeInTheDocument();
    await clickIn(user, dialog, "Remove entry");
    await expectPut("gpu01", { map: [GPU_A, GPU_C], digest: "d1" });
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("region", { name: "PCI mappings" }),
      );
    });
  });

  it("Remove of one of a node's several entries says the first free one of the rest is used", async () => {
    const spare = "id=1234:0009,iommugroup=30,node=pve-01,path=0000:09:00.0";
    listing = [
      mapping({
        id: "gpu01",
        map: [GPU_A, GPU_B, spare, GPU_C],
        node_checks: { "pve-01": [], "pve-02": [] },
      }),
    ];
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openRemove(user, "gpu01", "0000:02:00.0");
    expect(
      within(dialog).getByText(
        /pve-01 keeps its other 2 entries: a VM using gpu01 there is given the first of them not in use/,
      ),
    ).toBeInTheDocument();
  });

  it("Remove of a node's last entry says VMs will not start there", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openRemove(user, "gpu01", "pve-02");
    expect(
      within(dialog).getByText(
        /A VM using gpu01 will not start on pve-02 until the mapping has an entry for it again/,
      ),
    ).toBeInTheDocument();
  });

  it("removing an unreadable entry removes every one, the only save Proxmox takes", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openRemove(user, "odd02", "foo=bar");
    expect(
      within(dialog).getByText(
        "Remove the 2 entries Nexara cannot read from odd02?",
      ),
    ).toBeInTheDocument();
    await clickIn(user, dialog, "Remove entries");
    await expectPut("odd02", {
      map: ["id=1234:0008,node=pve-01,path=0000:08:00.0"],
      digest: "d1",
    });
  });

  it("removing the last entry deletes the mapping, after the usage check", async () => {
    const usage: MappingUsage = {
      mapping_id: "vgpu01",
      checked: 2,
      users: [
        { vmid: 101, name: "linux01", node: "pve-01", keys: ["hostpci0"] },
      ],
      unchecked: [],
    };
    mockedGet.mockResolvedValue(usage);
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openRemove(user, "vgpu01", "0000:04:00.0");
    expect(
      within(dialog).getByText(
        /This is the mapping's only entry, so removing it deletes the mapping/,
      ),
    ).toBeInTheDocument();
    expect(
      await within(dialog).findByText(/101 \(linux01\) on pve-01 — hostpci0/),
    ).toBeInTheDocument();
    expect(mockedGet).toHaveBeenCalledWith(`${mappingURL("vgpu01")}/usage`);
    await clickIn(user, dialog, "Delete mapping");
    await waitFor(() => {
      expect(mockedDelete).toHaveBeenCalledWith(
        `${mappingURL("vgpu01")}?digest=d1`,
      );
    });
    expect(mockedPut).not.toHaveBeenCalled();
  });

  it("a 409 on Remove says nothing was removed and why", async () => {
    mockedPut.mockRejectedValueOnce(conflict("PCI"));
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openRemove(user, "gpu01", "0000:02:00.0");
    await clickIn(user, dialog, "Remove entry");
    expect(
      await screen.findByText(
        /Nothing was removed from gpu01. The cluster's PCI mappings changed since this was loaded/,
      ),
    ).toBeInTheDocument();
  });
});
