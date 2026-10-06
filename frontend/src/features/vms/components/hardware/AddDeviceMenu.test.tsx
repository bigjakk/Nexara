import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { deferred } from "@/test/fake-server";
import { apiClient, ApiClientError } from "@/lib/api-client";
import {
  CLUSTER,
  pciDevice,
  setPermissions,
  usbDevice,
} from "@/features/mappings/components/mappings-test-kit";
import type { NodePCIDevice, NodeUSBDevice } from "../../api/vm-queries";
import type {
  PCIMapping,
  USBMapping,
} from "@/features/mappings/api/mapping-queries";
import type { VMConfig } from "../../types/vm";
import { AddDeviceMenu } from "./AddDeviceMenu";

// The transport is mocked, not the hooks, so the real mapping queries run and
// each test asserts the request that would leave the browser.
vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);

const NODE = "pve-01";

// Two devices, and one of each kind no picker may offer: a root hub, a hub,
// something with no port path, and something with no product id — the last
// two report class 0, so only their own clause of the filter can drop them.
const devices: NodeUSBDevice[] = [
  usbDevice({
    devnum: 1,
    vendid: "1d6b",
    prodid: "0002",
    product: "xHCI Host Controller",
    class: 9,
    level: 0,
  }),
  usbDevice({
    devnum: 2,
    vendid: "aaaa",
    prodid: "0001",
    product: "Example Hub",
    class: 9,
    usbpath: "1",
  }),
  usbDevice({
    devnum: 3,
    vendid: "1234",
    prodid: "5678",
    product: "Example Serial Adapter",
    usbpath: "2",
  }),
  usbDevice({
    devnum: 4,
    vendid: "abcd",
    prodid: "ef01",
    product: "Example Radio",
    usbpath: "3",
  }),
  usbDevice({
    devnum: 5,
    vendid: "bbbb",
    prodid: "0005",
    product: "No Port Path",
  }),
  usbDevice({
    devnum: 6,
    vendid: "cccc",
    product: "No Product Id",
    usbpath: "4",
  }),
];

// usbdev01 already passes the radio by id on this node; usbdev02 has no
// entry here, which Proxmox reports as a warning; usbdev03 names the serial
// adapter's port twice for this node, which qemu-server refuses at start, so
// it is never reused; usbdev04 is for a device the node does not have now.
const mappings: USBMapping[] = [
  {
    id: "usbdev01",
    description: "Example Radio",
    map: ["id=abcd:ef01,node=pve-01"],
    errors: [],
  },
  {
    id: "usbdev02",
    description: "",
    map: ["id=9999:0001,node=pve-02"],
    errors: [{ severity: "warning", message: "No mapping for node pve-01." }],
  },
  {
    id: "usbdev03",
    description: "",
    map: [
      "id=1234:5678,node=pve-01,path=1-2",
      "id=1234:5678,node=pve-01,path=1-2",
    ],
    errors: [],
  },
  {
    id: "usbdev04",
    description: "",
    map: ["id=5555:0004,node=pve-01"],
    errors: [
      {
        severity: "error",
        message: "Invalid configuration: usb device '5555:0004' not found",
      },
    ],
  },
];

// As Proxmox's lspci lists them: "0x" ids, function 1 of the GPU before its
// function 0, a NIC in no IOMMU group, and a device that can provide
// mediated devices.
const pciDeviceList: NodePCIDevice[] = [
  pciDevice({
    id: "0000:01:00.1",
    device: "0x0011",
    device_name: "Example GPU Audio",
    iommugroup: 14,
    subsystem_vendor: "0xabcd",
    subsystem_device: "0x0001",
  }),
  pciDevice({
    id: "0000:01:00.0",
    device: "0x5678",
    device_name: "Example GPU",
    iommugroup: 14,
    subsystem_vendor: "0xABCD",
    subsystem_device: "0xEF01",
  }),
  pciDevice({
    id: "0000:02:00.0",
    device: "0x0002",
    device_name: "Example NIC",
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
    id: "0000:06:00.0",
    class: "0x0c0330",
    device: "0x0006",
    device_name: "Example USB Controller",
    iommugroup: 20,
  }),
];

// pcidev01 passes the NIC exactly; pcidev02 has no entry on this node, which
// Proxmox reports as a warning; pcidev03 is the vGPU without the mdev flag,
// which Proxmox reports as an error and which is never reused.
const pciMappings: PCIMapping[] = [
  {
    id: "pcidev01",
    description: "Example NIC",
    map: ["id=1234:0002,node=pve-01,path=0000:02:00.0"],
    checks: [],
    mdev: false,
  },
  {
    id: "pcidev02",
    description: "",
    map: ["id=9999:0001,node=pve-02,path=0000:09:00.0"],
    checks: [{ severity: "warning", message: "No mapping for node pve-01." }],
    mdev: false,
  },
  {
    id: "pcidev03",
    description: "",
    map: ["id=1234:0004,iommugroup=7,node=pve-01,path=0000:04:00.0"],
    checks: [
      {
        severity: "error",
        message:
          "Invalid configuration: 'mdev' does not match for 'pcidev03' (1 != 0)",
      },
    ],
    mdev: false,
  },
];

/** A PCI mapping of one test's own: clean, no flag, with these entries. */
const pciMapping = (id: string, map: string[]): PCIMapping => ({
  id,
  description: "",
  map,
  checks: [],
  mdev: false,
});

let pciMappingList: PCIMapping[] = pciMappings;

type User = ReturnType<typeof userEvent.setup>;

// "unavailable" stands for the node's list not being there — still loading,
// or failed — which the menu receives as undefined.
interface MenuOptions {
  config?: VMConfig;
  usbDevices?: NodeUSBDevice[] | "unavailable";
  pciDevices?: NodePCIDevice[] | "unavailable";
}

function menuElement(
  options: MenuOptions,
  onAddDevice: (key: string, value: string) => void,
) {
  return (
    <AddDeviceMenu
      config={options.config ?? {}}
      clusterId={CLUSTER}
      nodeName={NODE}
      diskStorages={[]}
      usbDevices={
        options.usbDevices === "unavailable"
          ? undefined
          : (options.usbDevices ?? devices)
      }
      pciDevices={
        options.pciDevices === "unavailable"
          ? undefined
          : (options.pciDevices ?? pciDeviceList)
      }
      bridges={["vmbr0"]}
      isoFiles={[]}
      onAddDevice={onAddDevice}
      onAddCDROM={vi.fn()}
      onAddDisk={vi.fn()}
    />
  );
}

/** Renders the menu; `rerender` gives the same menu other props. */
function mount(options: MenuOptions) {
  const onAdd = vi.fn();
  const view = renderWithProviders(menuElement(options, onAdd));
  return {
    onAdd,
    rerender: (next: MenuOptions) => {
      view.rerender(menuElement(next, onAdd));
    },
  };
}

async function openDialog(user: User, item: RegExp) {
  await user.click(screen.getByRole("button", { name: /add device/i }));
  await user.click(await screen.findByRole("menuitem", { name: item }));
  return screen.findByRole("dialog");
}

/** The dialog of one kind of device, open on a menu with these props. */
async function start(flavor: Flavor, options: MenuOptions = {}) {
  const user = userEvent.setup();
  const menu = mount(options);
  return { user, ...menu, dialog: await flavor.open(user) };
}

const device = (dialog: HTMLElement) => within(dialog).getByLabelText("Device");
// Awaited: a button that depends on the mapping listing waits for it.
const button = (dialog: HTMLElement, name: string) =>
  within(dialog).findByRole("button", { name });
const radio = (dialog: HTMLElement, name: RegExp) =>
  within(dialog).getByRole("radio", { name });
const optionsOf = (select: HTMLElement) =>
  within(select)
    .getAllByRole("option")
    .map((o) => o.textContent);
const ACK = "The node does not need it; pass it through";
const ack = (dialog: HTMLElement) => within(dialog).getByLabelText(ACK);

beforeEach(() => {
  vi.resetAllMocks();
  setPermissions(["manage:cluster"]);
  pciMappingList = pciMappings;
  mockedList.mockImplementation((path: string) => {
    if (path === USB.listUrl) return Promise.resolve(mappings);
    if (path === PCI.listUrl) return Promise.resolve(pciMappingList);
    return Promise.reject(new Error(`unexpected list ${path}`));
  });
  mockedPost.mockResolvedValue({ status: "ok" });
});

/**
 * What differs between the USB and PCI dialogs, for the tests they share. The
 * dialogs are two components that do the same things (AddUSBDialog,
 * AddPCIDialog), so each of those things is one test run for both.
 */
interface Flavor {
  kind: "USB" | "PCI";
  open: (user: User) => Promise<HTMLElement>;
  /** The listing of this node's mappings, and where a new one is created. */
  listUrl: string;
  createUrl: string;
  /** The config key prefix, how many slots there are, and the text when full. */
  slots: { prefix: string; count: number; full: string };
  /** A host pick no mapping passes, alone in its group: it takes a create. */
  create: { pick: string; name: string; staged: string; body: object };
  /** A host pick that a mapping passes exactly. */
  reuse: { pick: string; id: string; staged: string };
  /** The mapped-device listing: a clean mapping, and one with a warning. */
  mapped: { options: string[]; clean: string; warned: string; staged: string };
  /** A mapping id already taken. */
  taken: string;
  /** What a create in flight locks, besides the radios. */
  locked: (string | RegExp)[];
  /** A change of pick other than the device. */
  changePick: (user: User, dialog: HTMLElement) => Promise<void>;
}

const USB: Flavor = {
  kind: "USB",
  open: (user) => openDialog(user, /usb device/i),
  listUrl: `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/usb-mappings`,
  createUrl: `/api/v1/clusters/${CLUSTER}/usb-mappings`,
  slots: { prefix: "usb", count: 14, full: "All 14 USB slots are in use." },
  create: {
    pick: "1234:5678",
    name: "example-serial-adapter",
    staged: "mapping=example-serial-adapter,usb3=1",
    // By id: no path key at all, so Proxmox follows the device to any port.
    body: {
      mapping_id: "example-serial-adapter",
      node: NODE,
      device_id: "1234:5678",
      description: "Example Serial Adapter",
    },
  },
  reuse: {
    pick: "abcd:ef01",
    id: "usbdev01",
    staged: "mapping=usbdev01,usb3=1",
  },
  mapped: {
    options: [
      "Select a mapping...",
      "usbdev01 — Example Radio",
      "usbdev02 (warning)",
      "usbdev03",
      "usbdev04 (error)",
    ],
    clean: "On pve-01: abcd:ef01",
    warned: "usbdev02",
    staged: "mapping=usbdev02,usb3=1",
  },
  taken: "usbdev02",
  locked: ["Mapping name", "Device"],
  // The mode is part of the pick.
  changePick: async (user, dialog) => {
    await user.click(radio(dialog, /spice port/i));
  },
};

const PCI: Flavor = {
  kind: "PCI",
  open: (user) => openDialog(user, /pci device/i),
  listUrl: `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/pci-mappings`,
  createUrl: `/api/v1/clusters/${CLUSTER}/pci-mappings`,
  slots: { prefix: "hostpci", count: 16, full: "All 16 PCI slots are in use." },
  create: {
    pick: "0000:04:00.0",
    name: "example-vgpu",
    staged: "mapping=example-vgpu",
    // The server copies the ids, group and mdev flag from the node's own
    // report of the device; the dialog sends only where it is.
    body: {
      mapping_id: "example-vgpu",
      node: NODE,
      path: "0000:04:00.0",
      description: "Example vGPU",
    },
  },
  reuse: { pick: "0000:02:00.0", id: "pcidev01", staged: "mapping=pcidev01" },
  mapped: {
    options: [
      "Select a mapping...",
      "pcidev01 — Example NIC",
      "pcidev02 (warning)",
      "pcidev03 (error)",
    ],
    clean: "On pve-01: 0000:02:00.0",
    warned: "pcidev02",
    staged: "mapping=pcidev02",
  },
  taken: "pcidev01",
  locked: ["Mapping name", "Device", /All functions/],
  changePick: async (user, dialog) => {
    await user.selectOptions(device(dialog), "0000:02:00.0");
  },
};

describe.each([USB, PCI])("Add $kind device dialog", (k) => {
  it("creates a mapping for a host device and stages the mapping", async () => {
    const { user, onAdd, dialog } = await start(k);

    await user.selectOptions(device(dialog), k.create.pick);
    expect(within(dialog).getByLabelText("Mapping name")).toHaveValue(
      k.create.name,
    );
    await user.click(await button(dialog, "Create mapping & add"));

    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith(`${k.slots.prefix}0`, k.create.staged);
    });
    expect(mockedPost).toHaveBeenCalledWith(k.createUrl, k.create.body);
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("reuses the mapping that already passes the device, without creating one", async () => {
    const { user, onAdd, dialog } = await start(k);

    await user.selectOptions(device(dialog), k.reuse.pick);
    expect(
      await within(dialog).findByText(
        new RegExp(`Uses the existing mapping “${k.reuse.id}”`),
      ),
    ).toBeInTheDocument();
    await user.click(await button(dialog, "Add"));

    expect(onAdd).toHaveBeenCalledWith(`${k.slots.prefix}0`, k.reuse.staged);
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("adds a mapped device, and shows what Proxmox says about it on this node", async () => {
    const { user, onAdd, dialog } = await start(k);

    await user.click(radio(dialog, /mapped device/i));
    const select = await within(dialog).findByLabelText("Mapping");
    expect(optionsOf(select)).toEqual(k.mapped.options);
    // A clean one says what it passes through here.
    await user.selectOptions(select, k.reuse.id);
    expect(within(dialog).getByText(k.mapped.clean)).toBeInTheDocument();
    // Selectable, as in Proxmox's own dialog — but the warning is shown.
    await user.selectOptions(select, k.mapped.warned);
    expect(
      within(dialog).getByText("No mapping for node pve-01."),
    ).toBeInTheDocument();
    await user.click(await button(dialog, "Add"));

    expect(onAdd).toHaveBeenCalledWith(`${k.slots.prefix}0`, k.mapped.staged);
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("cannot create a mapping without manage:cluster, but can still reuse one", async () => {
    setPermissions([]);
    const { user, onAdd, dialog } = await start(k);

    await user.selectOptions(device(dialog), k.create.pick);
    // As text: a disabled button's title never shows.
    expect(
      await within(dialog).findByText(
        /Creating a mapping needs the Manage Cluster permission/,
      ),
    ).toBeInTheDocument();
    expect(await button(dialog, "Create mapping & add")).toBeDisabled();

    await user.selectOptions(device(dialog), k.reuse.pick);
    await user.click(await button(dialog, "Add"));
    expect(onAdd).toHaveBeenCalledWith(`${k.slots.prefix}0`, k.reuse.staged);
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("keeps Proxmox's refusal on screen and stages nothing, until the pick changes, and re-reads the list", async () => {
    const refusal = `A ${k.kind} mapping with that ID already exists`;
    mockedPost.mockRejectedValue(
      new ApiClientError(409, { error: "conflict", message: refusal }),
    );
    const { user, onAdd, dialog } = await start(k);
    const reads = () =>
      mockedList.mock.calls.filter(([path]) => path === k.listUrl).length;

    await user.selectOptions(device(dialog), k.create.pick);
    const before = reads();
    await user.click(await button(dialog, "Create mapping & add"));

    expect(await within(dialog).findByText(refusal)).toBeInTheDocument();
    expect(onAdd).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    // A 409 means the list is stale: someone made that mapping meanwhile.
    await waitFor(() => {
      expect(reads()).toBeGreaterThan(before);
    });

    await k.changePick(user, dialog);
    expect(within(dialog).queryByText(refusal)).toBeNull();
  });

  it("refuses a mapping name Proxmox would, or one already taken", async () => {
    const { user, dialog } = await start(k);

    await user.selectOptions(device(dialog), k.create.pick);
    const create = await button(dialog, "Create mapping & add");
    const name = within(dialog).getByLabelText("Mapping name");
    await user.clear(name);
    await user.type(name, "1bad");
    expect(within(dialog).getByText(/Start with a letter/)).toBeInTheDocument();
    expect(create).toBeDisabled();

    await user.clear(name);
    await user.type(name, k.taken);
    expect(
      within(dialog).getByText(
        `A mapping named "${k.taken}" already exists for other hardware.`,
      ),
    ).toBeInTheDocument();
    expect(create).toBeDisabled();
  });

  it("shows why when every slot is taken", async () => {
    const config: VMConfig = {};
    for (let i = 0; i < k.slots.count; i++) {
      config[`${k.slots.prefix}${String(i)}`] = "taken";
    }
    const { user, dialog } = await start(k, { config });

    // A pick that would be addable, were there a slot.
    await user.click(radio(dialog, /mapped device/i));
    await user.selectOptions(
      await within(dialog).findByLabelText("Mapping"),
      k.reuse.id,
    );
    expect(await button(dialog, "Add")).toBeDisabled();
    expect(within(dialog).getByText(k.slots.full)).toBeInTheDocument();
  });

  it("locks while a create is in flight, then stages exactly once", async () => {
    const post = deferred<unknown>();
    mockedPost.mockReturnValue(post.promise);
    const { user, onAdd, dialog } = await start(k);

    await user.selectOptions(device(dialog), k.create.pick);
    await user.click(await button(dialog, "Create mapping & add"));

    expect(await button(dialog, "Creating mapping…")).toBeDisabled();
    expect(
      within(dialog).getByRole("button", { name: "Cancel" }),
    ).toBeDisabled();
    for (const label of k.locked) {
      expect(within(dialog).getByLabelText(label)).toBeDisabled();
    }
    for (const r of within(dialog).getAllByRole("radio")) {
      expect(r).toBeDisabled();
    }
    // Escape would unmount the dialog and leave the create to stage a device
    // the operator had walked away from.
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(onAdd).not.toHaveBeenCalled();

    post.resolve({ status: "ok" });
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledTimes(1);
    });
    expect(onAdd).toHaveBeenCalledWith(`${k.slots.prefix}0`, k.create.staged);
    expect(mockedPost).toHaveBeenCalledTimes(1);
  });

  it("waits for the mapping listing before a host pick can add", async () => {
    mockedList.mockImplementation(() => new Promise(() => undefined));
    const { user, dialog } = await start(k);

    await user.selectOptions(device(dialog), k.reuse.pick);
    expect(
      within(dialog).getByText(`Checking the cluster's ${k.kind} mappings…`),
    ).toBeInTheDocument();
    expect(await button(dialog, "Add")).toBeDisabled();
  });

  it("will not create from a listing that failed, and says why", async () => {
    mockedList.mockImplementation((path: string) =>
      path === k.listUrl
        ? Promise.reject(
            new ApiClientError(502, {
              error: "bad_gateway",
              message: "cluster unreachable",
            }),
          )
        : Promise.resolve([]),
    );
    const { user, dialog } = await start(k);

    await user.selectOptions(device(dialog), k.create.pick);
    expect(
      await within(dialog).findByText(
        `Could not list the cluster's ${k.kind} mappings: cluster unreachable`,
      ),
    ).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Mapping name")).toBeNull();
    for (const b of within(dialog).getAllByRole("button", {
      name: /^(Add|Create mapping & add)$/,
    })) {
      expect(b).toBeDisabled();
    }
    expect(mockedPost).not.toHaveBeenCalled();
  });
});

describe("AddUSBDialog", () => {
  it("offers only real devices, never a hub, and maps a port with the id of the device on it", async () => {
    const { user, onAdd, dialog } = await start(USB);

    expect(optionsOf(device(dialog))).toEqual([
      "Select a device...",
      "Example Serial Adapter (1234:5678)",
      "Example Radio (abcd:ef01)",
    ]);

    await user.click(radio(dialog, /host usb port/i));
    // usbdev01 follows the radio to any port; a port mapping ties it to one.
    // Different mapping, so a new one.
    await user.selectOptions(within(dialog).getByLabelText("Port"), "1-3");
    expect(await button(dialog, "Create mapping & add")).toBeEnabled();
    expect(within(dialog).queryByText(/Uses the existing mapping/)).toBeNull();

    await user.selectOptions(within(dialog).getByLabelText("Port"), "1-2");
    await user.click(await button(dialog, "Create mapping & add"));
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith(
        "usb0",
        "mapping=example-serial-adapter,usb3=1",
      );
    });
    expect(mockedPost).toHaveBeenCalledWith(USB.createUrl, {
      mapping_id: "example-serial-adapter",
      node: NODE,
      device_id: "1234:5678",
      path: "1-2",
      description: "Example Serial Adapter",
    });
  });

  it("writes spice for the SPICE port, with usb3 only when ticked", async () => {
    const { user, onAdd, dialog } = await start(USB);

    await user.click(radio(dialog, /spice port/i));
    await user.click(await button(dialog, "Add"));
    expect(onAdd).toHaveBeenLastCalledWith("usb0", "spice,usb3=1");

    const again = await USB.open(user);
    await user.click(radio(again, /spice port/i));
    await user.click(within(again).getByLabelText("USB 3.0"));
    await user.click(await button(again, "Add"));
    expect(onAdd).toHaveBeenLastCalledWith("usb0", "spice");
  });

  it("takes a typed device id when the node lists none", async () => {
    const { user, onAdd, dialog } = await start(USB, { usbDevices: [] });

    const input = device(dialog);
    // host= takes 0x, a mapping's id does not.
    await user.type(input, "0x1234:0x5678");
    expect(
      within(dialog).getByText(/four hex digits each, without 0x/),
    ).toBeInTheDocument();

    await user.clear(input);
    await user.type(input, "ABCD:0001");
    await user.click(await button(dialog, "Create mapping & add"));
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith(
        "usb0",
        "mapping=usb-abcd-0001,usb3=1",
      );
    });
    // Lowercased, as the node's sysfs reports it and Proxmox compares it.
    expect(mockedPost).toHaveBeenCalledWith(USB.createUrl, {
      mapping_id: "usb-abcd-0001",
      node: NODE,
      device_id: "abcd:0001",
      description: "USB abcd:0001",
    });

    // And the port picker says there is nothing to pick.
    const again = await USB.open(user);
    await user.click(radio(again, /host usb port/i));
    expect(
      within(again).getByText(/lists no USB device on a port/),
    ).toBeInTheDocument();
  });

  it("does not call a list that is not there yet empty", async () => {
    const { user, dialog } = await start(USB, { usbDevices: "unavailable" });

    await user.click(radio(dialog, /host usb port/i));
    expect(
      within(dialog).getByText(/USB devices are not available/),
    ).toBeInTheDocument();
    expect(within(dialog).queryByText(/lists no USB device/)).toBeNull();
  });

  it("stores a device's own name only once it is safe to", async () => {
    const { user, dialog } = await start(USB, {
      usbDevices: [
        usbDevice({
          devnum: 7,
          vendid: "dddd",
          prodid: "0007",
          product: "Example\u202eReader\r",
          usbpath: "5",
        }),
      ],
    });

    // Shown clean too: an override would reorder the id printed after it.
    expect(optionsOf(device(dialog))).toContain("Example Reader (dddd:0007)");
    await user.selectOptions(device(dialog), "dddd:0007");
    await user.click(await button(dialog, "Create mapping & add"));
    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    expect(mockedPost.mock.calls[0]?.[1]).toMatchObject({
      description: "Example Reader",
    });
  });

  it("reuses the mapping of a device that is unplugged now, and says so, in the next free slot", async () => {
    const { user, onAdd, dialog } = await start(USB, {
      usbDevices: [],
      config: { usb0: "host=1-3", usb1: "spice" },
    });

    expect(
      within(dialog).getByRole("heading", { name: "Add USB Device (usb2)" }),
    ).toBeInTheDocument();
    await user.type(device(dialog), "5555:0004");
    expect(
      await within(dialog).findByText(/Uses the existing mapping “usbdev04”/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        "Invalid configuration: usb device '5555:0004' not found",
      ),
    ).toBeInTheDocument();
    await user.click(await button(dialog, "Add"));

    expect(onAdd).toHaveBeenCalledWith("usb2", "mapping=usbdev04,usb3=1");
    expect(mockedPost).not.toHaveBeenCalled();
  });

  // Anyone who can edit a mapping in Proxmox sets its description, so what
  // comes back is cleaned before it is shown, in the list and in the reuse.
  it("shows a mapping's description from Proxmox cleaned", async () => {
    mockedList.mockResolvedValue([
      {
        id: "usbdev09",
        description: "Example\u202eDock\u061c",
        map: ["id=abcd:ef01,node=pve-01"],
        errors: [],
      },
    ]);
    const { user, dialog } = await start(USB);

    await user.selectOptions(device(dialog), "abcd:ef01");
    expect(
      await within(dialog).findByText(
        "Uses the existing mapping “usbdev09” (Example Dock).",
      ),
    ).toBeInTheDocument();

    await user.click(radio(dialog, /mapped device/i));
    expect(
      optionsOf(await within(dialog).findByLabelText("Mapping")),
    ).toContain("usbdev09 — Example Dock");
  });
});

describe("AddPCIDialog", () => {
  it("lists the node's devices by IOMMU group, named", async () => {
    const { dialog } = await start(PCI);

    const select = device(dialog);
    expect(
      within(select)
        .getAllByRole("group")
        .map((g) => g.getAttribute("label")),
    ).toEqual([
      "No IOMMU group",
      "IOMMU Group 7",
      "IOMMU Group 14",
      "IOMMU Group 20",
    ]);
    expect(
      within(select).getByRole("option", {
        name: "0000:01:00.0 — Example GPU",
      }),
    ).toBeInTheDocument();
  });

  it("maps the whole device, all its functions, when asked, named after its function 0", async () => {
    const { user, onAdd, dialog } = await start(PCI);

    await user.selectOptions(device(dialog), "0000:01:00.1");
    expect(within(dialog).getByLabelText("Mapping name")).toHaveValue(
      "example-gpu-audio",
    );
    // Function 0 shares the group, so the dialog asks first — until the whole
    // device is passed, when neither function is the other's peer.
    expect(ack(dialog)).toBeInTheDocument();
    await user.click(within(dialog).getByLabelText(/All functions/));
    expect(within(dialog).getByLabelText("Mapping name")).toHaveValue(
      "example-gpu",
    );
    expect(within(dialog).queryByLabelText(ACK)).not.toBeInTheDocument();
    await user.click(await button(dialog, "Create mapping & add"));

    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledWith(PCI.createUrl, {
        mapping_id: "example-gpu",
        node: NODE,
        path: "0000:01:00",
        description: "Example GPU",
      });
    });
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=example-gpu");
  });

  // What findReusablePCIMapping weighs is tested in pci-mapping.test.ts; this
  // is the dialog offering a create when it finds nothing.
  it("does not reuse a mapping that differs from what the node reports for the device", async () => {
    pciMappingList = [
      pciMapping("pcidev09", [
        "id=1234:0002,node=pve-01,path=0000:02:00.0,subsystem-id=abcd:0002",
      ]),
    ];
    const { user, dialog } = await start(PCI);

    await user.selectOptions(device(dialog), "0000:02:00.0");

    expect(await button(dialog, "Create mapping & add")).toBeInTheDocument();
    expect(
      within(dialog).queryByText(/Uses the existing mapping/),
    ).not.toBeInTheDocument();
  });

  it.each([
    { machine: "pc-q35-9.0", offered: true, staged: "mapping=pcidev01,pcie=1" },
    { machine: "pc-i440fx-9.0", offered: false, staged: "mapping=pcidev01" },
  ])(
    "offers PCIe only on the q35 machine type ($machine)",
    async ({ machine, offered, staged }) => {
      const { user, onAdd, dialog } = await start(PCI, {
        config: { machine },
      });

      const pcie = within(dialog).getByLabelText(/^PCIe/);
      if (offered) {
        expect(pcie).toBeEnabled();
      } else {
        expect(pcie).toBeDisabled();
        expect(pcie).not.toBeChecked();
        expect(within(dialog).getByText("(q35 only)")).toBeInTheDocument();
      }
      await user.selectOptions(device(dialog), "0000:02:00.0");
      await user.click(await button(dialog, "Add"));
      expect(onAdd).toHaveBeenCalledWith("hostpci0", staged);
    },
  );

  // The config is re-read while the dialog is open; a VM moved off q35
  // meanwhile must not get the pcie=1 ticked for it before.
  it("drops PCIe when the VM leaves q35 while the dialog is open", async () => {
    const { user, onAdd, dialog, rerender } = await start(PCI, {
      config: { machine: "q35" },
    });
    expect(within(dialog).getByLabelText(/^PCIe/)).toBeChecked();

    rerender({ config: { machine: "pc-i440fx-9.0" } });
    await user.selectOptions(device(dialog), "0000:02:00.0");
    await user.click(await button(dialog, "Add"));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev01");
  });

  it("writes ROM-BAR off and Primary GPU when set", async () => {
    const { user, onAdd, dialog } = await start(PCI);

    await user.selectOptions(device(dialog), "0000:02:00.0");
    await user.click(within(dialog).getByLabelText("ROM-BAR"));
    await user.click(within(dialog).getByLabelText(/Primary GPU/));
    await user.click(await button(dialog, "Add"));
    expect(onAdd).toHaveBeenCalledWith(
      "hostpci0",
      "mapping=pcidev01,rombar=0,x-vga=1",
    );
  });

  it("takes a typed address when the node lists no devices, and refuses a malformed one", async () => {
    const { user, onAdd, dialog } = await start(PCI, {
      pciDevices: "unavailable",
    });

    const input = device(dialog);
    await user.type(input, "01:00.0");
    expect(
      within(dialog).getByText(/An address like 0000:01:00.0/),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("button", { name: "Create mapping & add" }),
    ).not.toBeInTheDocument();

    await user.clear(input);
    await user.type(input, "0000:01:00.0");
    const name = within(dialog).getByLabelText("Mapping name");
    await user.clear(name);
    await user.type(name, "gpu01");
    // Nothing to look the address up in, so the dialog asks.
    expect(
      within(dialog).getByText(
        "Nexara cannot tell what 0000:01:00.0 is, or what shares its IOMMU group: check in Proxmox that pve-01 does not need it.",
      ),
    ).toBeInTheDocument();
    expect(await button(dialog, "Create mapping & add")).toBeDisabled();
    await user.click(ack(dialog));
    await user.click(await button(dialog, "Create mapping & add"));
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=gpu01");
    });
    // No label to describe it with: the node did not name it.
    expect(mockedPost).toHaveBeenCalledWith(PCI.createUrl, {
      mapping_id: "gpu01",
      node: NODE,
      path: "0000:01:00.0",
    });
  });

  // Without the device's record there is nothing to compare an entry with,
  // but a mapping Proxmox checked clean on this node matches the device.
  it("reuses a mapping Proxmox checked clean for a typed address, and only that", async () => {
    pciMappingList = [
      {
        ...pciMapping("pcidev07", [
          "id=1234:0007,node=pve-01,path=0000:07:00.0",
        ]),
        checks: [
          {
            severity: "error",
            message:
              "Invalid configuration: 'id' does not match for 'pcidev07' (1234:0008 != 1234:0007)",
          },
        ],
      },
      pciMapping("pcidev08", ["id=1234:0008,node=pve-01,path=0000:07:00.0"]),
    ];
    const { user, onAdd, dialog } = await start(PCI, {
      pciDevices: "unavailable",
    });

    await user.type(device(dialog), "0000:07:00.0");
    expect(
      await within(dialog).findByText(/Uses the existing mapping “pcidev08”/),
    ).toBeInTheDocument();
    await user.click(ack(dialog));
    await user.click(await button(dialog, "Add"));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev08");
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("drops an address typed while the node's list was loading once the list is there", async () => {
    const { user, dialog, rerender } = await start(PCI, {
      pciDevices: "unavailable",
    });
    await user.type(device(dialog), "0000:09:00.0");
    expect(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    ).toBeInTheDocument();

    rerender({ pciDevices: pciDeviceList });
    expect(device(dialog)).toHaveValue("");
    expect(
      within(dialog).queryByRole("button", { name: "Create mapping & add" }),
    ).not.toBeInTheDocument();
    expect(await button(dialog, "Add")).toBeDisabled();
  });

  it("warns before taking a device the node may need, and holds Add until told", async () => {
    const { user, onAdd, dialog } = await start(PCI);

    // Alone in its group, there is nothing to ask.
    await user.selectOptions(device(dialog), "0000:04:00.0");
    expect(within(dialog).queryByLabelText(ACK)).not.toBeInTheDocument();

    await user.selectOptions(device(dialog), "0000:05:00.0");
    expect(
      within(dialog).getByText(
        "0000:05:00.0 is a storage controller: if the node's own disks are on it, the node loses them.",
      ),
    ).toBeInTheDocument();
    // Its group holds the USB controller, which goes too.
    expect(
      within(dialog).getByText(
        "Proxmox takes every device in the IOMMU group away from the node when the VM starts, so 0000:06:00.0 goes too.",
      ),
    ).toBeInTheDocument();
    const create = await button(dialog, "Create mapping & add");
    expect(create).toBeDisabled();

    await user.click(ack(dialog));
    expect(create).toBeEnabled();

    // The tick was for that pick; another one is asked about afresh.
    await user.selectOptions(device(dialog), "0000:06:00.0");
    expect(
      within(dialog).getByText(
        "0000:06:00.0 is a USB controller: the node loses every USB device on it.",
      ),
    ).toBeInTheDocument();
    expect(ack(dialog)).not.toBeChecked();
    expect(create).toBeDisabled();
    await user.click(ack(dialog));
    await user.click(create);
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalled();
    });
  });

  it("asks about a mapped device the node may need or its list does not hold, and says what a mapping has here", async () => {
    pciMappingList = [
      pciMapping("pcidev06", [
        "id=1234:0005,iommugroup=20,node=pve-01,path=0000:05:00.0",
      ]),
      pciMapping("pcidev10", [
        "id=1234:0002,node=pve-01,path=0000:02:00.0",
        "id=1234:0002,node=pve-01,path=0000:03:00.0",
      ]),
      pciMapping("pcidev11", ["id=1234:0011,node=pve-01,path=0000:0b:00.0"]),
    ];
    const { user, onAdd, dialog } = await start(PCI);
    await user.click(radio(dialog, /mapped device/i));
    const select = await within(dialog).findByLabelText("Mapping");

    await user.selectOptions(select, "pcidev10");
    expect(
      within(dialog).getByText(
        "On pve-01: 0000:02:00.0, 0000:03:00.0 — the first one not in use when the VM starts",
      ),
    ).toBeInTheDocument();

    await user.selectOptions(select, "pcidev06");
    expect(
      within(dialog).getByText(/0000:05:00.0 is a storage controller/),
    ).toBeInTheDocument();
    expect(await button(dialog, "Add")).toBeDisabled();
    await user.click(ack(dialog));
    expect(await button(dialog, "Add")).toBeEnabled();

    await user.selectOptions(select, "pcidev11");
    expect(
      within(dialog).getByText(/Nexara cannot tell what 0000:0b:00.0 is/),
    ).toBeInTheDocument();
    expect(await button(dialog, "Add")).toBeDisabled();
    await user.click(ack(dialog));
    await user.click(await button(dialog, "Add"));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev11");
  });

  // The tick confirmed what was said then; a warning the loaded list adds
  // afterwards is asked about again.
  it("asks again when the node's list adds a warning after the tick", async () => {
    pciMappingList = [
      pciMapping("pcidev06", [
        "id=1234:0005,iommugroup=20,node=pve-01,path=0000:05:00.0",
      ]),
    ];
    const { user, dialog, rerender } = await start(PCI, {
      pciDevices: "unavailable",
    });
    await user.click(radio(dialog, /mapped device/i));
    await user.selectOptions(
      await within(dialog).findByLabelText("Mapping"),
      "pcidev06",
    );
    expect(
      within(dialog).getByText(/Nexara cannot tell what 0000:05:00.0 is/),
    ).toBeInTheDocument();
    await user.click(ack(dialog));
    expect(await button(dialog, "Add")).toBeEnabled();

    rerender({ pciDevices: pciDeviceList });
    expect(
      within(dialog).getByText(/0000:05:00.0 is a storage controller/),
    ).toBeInTheDocument();
    expect(ack(dialog)).not.toBeChecked();
    expect(await button(dialog, "Add")).toBeDisabled();
  });
});
