import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { NodePCIDevice, NodeUSBDevice } from "../../api/vm-queries";
import type {
  PCIMapping,
  USBMapping,
} from "@/features/mappings/api/mapping-queries";
import type { VMConfig } from "../../types/vm";
import { AddDeviceMenu } from "./AddDeviceMenu";

// The transport is mocked, not the hooks, so the real mapping queries run and
// each test asserts the request that would leave the browser.
vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: { get: vi.fn(), list: vi.fn(), post: vi.fn(), put: vi.fn() },
  };
});

const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const NODE = "pve-01";
const MAPPINGS_URL = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/usb-mappings`;
const CREATE_URL = `/api/v1/clusters/${CLUSTER}/usb-mappings`;
const PCI_MAPPINGS_URL = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/pci-mappings`;
const PCI_CREATE_URL = `/api/v1/clusters/${CLUSTER}/pci-mappings`;

function usbDevice(partial: Partial<NodeUSBDevice>): NodeUSBDevice {
  return {
    busnum: 1,
    devnum: 1,
    port: "0",
    prodid: "",
    vendid: "",
    product: "",
    manufacturer: "",
    speed: "12",
    class: 0,
    usbpath: "",
    level: 1,
    ...partial,
  };
}

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

function setPermissions(permissions: string[]) {
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.test",
      display_name: "Test User",
      role: "user",
    },
    permissions,
    isAuthenticated: true,
    isInitialized: true,
  });
}

// "unavailable" stands for the node's list not being there — still loading,
// or failed — which the menu receives as undefined.
function renderMenu({
  config = {},
  usbDevices = devices,
  pciDevices = pciDeviceList,
}: {
  config?: VMConfig;
  usbDevices?: NodeUSBDevice[] | "unavailable";
  pciDevices?: NodePCIDevice[] | "unavailable";
} = {}) {
  const onAddDevice = vi.fn();
  renderWithProviders(
    <AddDeviceMenu
      config={config}
      clusterId={CLUSTER}
      nodeName={NODE}
      diskStorages={[]}
      usbDevices={usbDevices === "unavailable" ? undefined : usbDevices}
      pciDevices={pciDevices === "unavailable" ? undefined : pciDevices}
      bridges={["vmbr0"]}
      isoFiles={[]}
      onAddDevice={onAddDevice}
      onAddCDROM={vi.fn()}
      onAddDisk={vi.fn()}
    />,
  );
  return onAddDevice;
}

async function openUSBDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /add device/i }));
  await user.click(
    await screen.findByRole("menuitem", { name: /usb device/i }),
  );
  return screen.findByRole("dialog");
}

function pciDevice(partial: Partial<NodePCIDevice>): NodePCIDevice {
  return {
    id: "",
    class: "0x030000",
    device_name: "",
    vendor_name: "Example Corp",
    device: "",
    vendor: "0x1234",
    iommugroup: -1,
    ...partial,
  };
}

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
  pciDevice({ id: "0000:02:00.0", device: "0x0002", device_name: "Example NIC" }),
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

let pciMappingList: PCIMapping[] = pciMappings;

beforeEach(() => {
  vi.resetAllMocks();
  setPermissions(["manage:cluster"]);
  pciMappingList = pciMappings;
  mockedList.mockImplementation((path: string) => {
    if (path === MAPPINGS_URL) return Promise.resolve(mappings);
    if (path === PCI_MAPPINGS_URL) return Promise.resolve(pciMappingList);
    return Promise.reject(new Error(`unexpected list ${path}`));
  });
  mockedPost.mockResolvedValue({ status: "ok" });
});

describe("AddUSBDialog", () => {
  it("offers only real devices, never a hub", async () => {
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openUSBDialog(user);

    const options = within(within(dialog).getByLabelText("Device"))
      .getAllByRole("option")
      .map((o) => o.textContent);
    expect(options).toEqual([
      "Select a device...",
      "Example Serial Adapter (1234:5678)",
      "Example Radio (abcd:ef01)",
    ]);
  });

  it("creates a mapping for a host device and stages the mapping", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openUSBDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "1234:5678",
    );
    expect(within(dialog).getByLabelText("Mapping name")).toHaveValue(
      "example-serial-adapter",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );

    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith(
        "usb0",
        "mapping=example-serial-adapter,usb3=1",
      );
    });
    // By id: no path key at all, so Proxmox follows the device to any port.
    expect(mockedPost).toHaveBeenCalledWith(CREATE_URL, {
      mapping_id: "example-serial-adapter",
      node: NODE,
      device_id: "1234:5678",
      description: "Example Serial Adapter",
    });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("maps a port with the id of the device on it", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openUSBDialog(user);

    await user.click(
      within(dialog).getByRole("radio", { name: /host usb port/i }),
    );
    await user.selectOptions(within(dialog).getByLabelText("Port"), "1-2");
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );

    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith(
        "usb0",
        "mapping=example-serial-adapter,usb3=1",
      );
    });
    expect(mockedPost).toHaveBeenCalledWith(CREATE_URL, {
      mapping_id: "example-serial-adapter",
      node: NODE,
      device_id: "1234:5678",
      path: "1-2",
      description: "Example Serial Adapter",
    });
  });

  it("reuses the mapping that already passes the device, without creating one", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openUSBDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "abcd:ef01",
    );
    expect(
      await within(dialog).findByText(/Uses the existing mapping “usbdev01”/),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    expect(onAdd).toHaveBeenCalledWith("usb0", "mapping=usbdev01,usb3=1");
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("does not reuse a by-id mapping for a port pick", async () => {
    // usbdev01 follows the radio to any port; a port mapping ties it to one.
    // Different mapping, so a new one.
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openUSBDialog(user);

    await user.click(
      within(dialog).getByRole("radio", { name: /host usb port/i }),
    );
    await user.selectOptions(within(dialog).getByLabelText("Port"), "1-3");

    expect(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    ).toBeEnabled();
    expect(within(dialog).queryByText(/Uses the existing mapping/)).toBeNull();
  });

  it("adds a mapped device, and shows what Proxmox says about it on this node", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openUSBDialog(user);

    await user.click(
      within(dialog).getByRole("radio", { name: /mapped device/i }),
    );
    const select = await within(dialog).findByLabelText("Mapping");
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual([
      "Select a mapping...",
      "usbdev01 — Example Radio",
      "usbdev02 (warning)",
      "usbdev03",
      "usbdev04 (error)",
    ]);

    // Selectable, as in Proxmox's own dialog — but the warning is shown.
    await user.selectOptions(select, "usbdev02");
    expect(
      within(dialog).getByText("No mapping for node pve-01."),
    ).toBeInTheDocument();

    await user.selectOptions(select, "usbdev01");
    expect(
      within(dialog).getByText("On pve-01: abcd:ef01"),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    expect(onAdd).toHaveBeenCalledWith("usb0", "mapping=usbdev01,usb3=1");
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("cannot create a mapping without manage:cluster, but can still reuse one", async () => {
    setPermissions([]);
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openUSBDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "1234:5678",
    );
    const create = within(dialog).getByRole("button", {
      name: "Create mapping & add",
    });
    expect(create).toBeDisabled();
    // As text: a disabled button's title never shows.
    expect(
      within(dialog).getByText(
        /Creating a mapping needs the Manage Cluster permission/,
      ),
    ).toBeInTheDocument();

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "abcd:ef01",
    );
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("usb0", "mapping=usbdev01,usb3=1");
  });

  it("keeps Proxmox's refusal on screen and stages nothing", async () => {
    mockedPost.mockRejectedValue(
      new ApiClientError(409, {
        error: "conflict",
        message: "A USB mapping with that ID already exists",
      }),
    );
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openUSBDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "1234:5678",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );

    expect(
      await within(dialog).findByText(
        "A USB mapping with that ID already exists",
      ),
    ).toBeInTheDocument();
    expect(onAdd).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("refuses a mapping name Proxmox would, or one already taken", async () => {
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openUSBDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "1234:5678",
    );
    const name = within(dialog).getByLabelText("Mapping name");
    const create = within(dialog).getByRole("button", {
      name: "Create mapping & add",
    });

    await user.clear(name);
    await user.type(name, "1serial");
    expect(within(dialog).getByText(/Start with a letter/)).toBeInTheDocument();
    expect(create).toBeDisabled();

    await user.clear(name);
    await user.type(name, "usbdev02");
    expect(within(dialog).getByText(/already exists/)).toBeInTheDocument();
    expect(create).toBeDisabled();
  });

  it("writes spice for the SPICE port, with usb3 only when ticked", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    let dialog = await openUSBDialog(user);

    await user.click(
      within(dialog).getByRole("radio", { name: /spice port/i }),
    );
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenLastCalledWith("usb0", "spice,usb3=1");

    dialog = await openUSBDialog(user);
    await user.click(
      within(dialog).getByRole("radio", { name: /spice port/i }),
    );
    await user.click(within(dialog).getByLabelText("USB 3.0"));
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenLastCalledWith("usb0", "spice");
  });

  it("takes the next free slot", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu({ config: { usb0: "host=1-3", usb1: "spice" } });
    const dialog = await openUSBDialog(user);

    expect(
      within(dialog).getByRole("heading", { name: "Add USB Device (usb2)" }),
    ).toBeInTheDocument();
    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "abcd:ef01",
    );
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("usb2", "mapping=usbdev01,usb3=1");
  });

  it("shows why when every slot is taken", async () => {
    const config: VMConfig = {};
    for (let i = 0; i <= 13; i++) config[`usb${String(i)}`] = "spice";
    const user = userEvent.setup();
    renderMenu({ config });
    const dialog = await openUSBDialog(user);

    await user.click(
      within(dialog).getByRole("radio", { name: /spice port/i }),
    );
    expect(within(dialog).getByRole("button", { name: "Add" })).toBeDisabled();
    expect(
      within(dialog).getByText("All 14 USB slots are in use."),
    ).toBeInTheDocument();
  });

  it("locks while a create is in flight, then stages exactly once", async () => {
    let resolvePost: (value: unknown) => void = () => undefined;
    mockedPost.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolvePost = resolve;
        }),
    );
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openUSBDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "1234:5678",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );

    expect(
      await within(dialog).findByRole("button", { name: "Creating mapping…" }),
    ).toBeDisabled();
    expect(
      within(dialog).getByRole("button", { name: "Cancel" }),
    ).toBeDisabled();
    expect(within(dialog).getByLabelText("Mapping name")).toBeDisabled();
    expect(within(dialog).getByLabelText("Device")).toBeDisabled();
    for (const radio of within(dialog).getAllByRole("radio")) {
      expect(radio).toBeDisabled();
    }
    // Escape would unmount the dialog and leave the create to stage a device
    // the operator had walked away from.
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(onAdd).not.toHaveBeenCalled();

    resolvePost({ status: "ok" });
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledTimes(1);
    });
    expect(onAdd).toHaveBeenCalledWith(
      "usb0",
      "mapping=example-serial-adapter,usb3=1",
    );
    expect(mockedPost).toHaveBeenCalledTimes(1);
  });

  it("waits for the mapping listing before a host pick can add", async () => {
    mockedList.mockImplementation(() => new Promise(() => undefined));
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openUSBDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "abcd:ef01",
    );
    expect(
      within(dialog).getByText("Checking the cluster's USB mappings…"),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Add" })).toBeDisabled();
  });

  it("will not create from a listing that failed, and says why", async () => {
    mockedList.mockRejectedValue(
      new ApiClientError(502, {
        error: "bad_gateway",
        message: "cluster unreachable",
      }),
    );
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openUSBDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "1234:5678",
    );
    expect(
      await within(dialog).findByText(
        "Could not list the cluster's USB mappings: cluster unreachable",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).queryByLabelText("Mapping name")).toBeNull();
    for (const button of within(dialog).getAllByRole("button", {
      name: /^(Add|Create mapping & add)$/,
    })) {
      expect(button).toBeDisabled();
    }
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("takes a typed device id when the node lists none", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu({ usbDevices: [] });
    const dialog = await openUSBDialog(user);

    const input = within(dialog).getByLabelText("Device");
    // host= takes 0x, a mapping's id does not.
    await user.type(input, "0x1234:0x5678");
    expect(
      within(dialog).getByText(/four hex digits each, without 0x/),
    ).toBeInTheDocument();

    await user.clear(input);
    await user.type(input, "ABCD:0001");
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith(
        "usb0",
        "mapping=usb-abcd-0001,usb3=1",
      );
    });
    // Lowercased, as the node's sysfs reports it and Proxmox compares it.
    expect(mockedPost).toHaveBeenCalledWith(CREATE_URL, {
      mapping_id: "usb-abcd-0001",
      node: NODE,
      device_id: "abcd:0001",
      description: "USB abcd:0001",
    });

    // And the port picker says there is nothing to pick.
    const again = await openUSBDialog(user);
    await user.click(
      within(again).getByRole("radio", { name: /host usb port/i }),
    );
    expect(
      within(again).getByText(/lists no USB device on a port/),
    ).toBeInTheDocument();
  });

  it("stores a device's own name only once it is safe to", async () => {
    const user = userEvent.setup();
    renderMenu({
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
    const dialog = await openUSBDialog(user);

    // Shown clean too: an override would reorder the id printed after it.
    expect(
      within(within(dialog).getByLabelText("Device"))
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toContain("Example Reader (dddd:0007)");
    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "dddd:0007",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );
    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledTimes(1);
    });
    expect(mockedPost.mock.calls[0]?.[1]).toMatchObject({
      description: "Example Reader",
    });
  });

  it("reuses the mapping of a device that is unplugged now, and says so", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu({ usbDevices: [] });
    const dialog = await openUSBDialog(user);

    await user.type(within(dialog).getByLabelText("Device"), "5555:0004");
    expect(
      await within(dialog).findByText(/Uses the existing mapping “usbdev04”/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        "Invalid configuration: usb device '5555:0004' not found",
      ),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    expect(onAdd).toHaveBeenCalledWith("usb0", "mapping=usbdev04,usb3=1");
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
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openUSBDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "abcd:ef01",
    );
    expect(
      await within(dialog).findByText(
        "Uses the existing mapping “usbdev09” (Example Dock).",
      ),
    ).toBeInTheDocument();

    await user.click(
      within(dialog).getByRole("radio", { name: /mapped device/i }),
    );
    expect(
      within(await within(dialog).findByLabelText("Mapping"))
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toContain("usbdev09 — Example Dock");
  });

  it("does not call a list that is not there yet empty", async () => {
    const user = userEvent.setup();
    renderMenu({ usbDevices: "unavailable" });
    const dialog = await openUSBDialog(user);

    await user.click(
      within(dialog).getByRole("radio", { name: /host usb port/i }),
    );
    expect(
      within(dialog).getByText(/USB devices are not available/),
    ).toBeInTheDocument();
    expect(within(dialog).queryByText(/lists no USB device/)).toBeNull();
  });

  it("clears Proxmox's refusal once the pick changes, and re-reads the list", async () => {
    mockedPost.mockRejectedValue(
      new ApiClientError(409, {
        error: "conflict",
        message: "A USB mapping with that ID already exists",
      }),
    );
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openUSBDialog(user);
    const listCalls = () =>
      mockedList.mock.calls.filter(([path]) => path === MAPPINGS_URL).length;

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "1234:5678",
    );
    const before = listCalls();
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );
    await within(dialog).findByText(
      "A USB mapping with that ID already exists",
    );
    // A 409 means the list is stale: someone made that mapping meanwhile.
    await waitFor(() => {
      expect(listCalls()).toBeGreaterThan(before);
    });

    await user.click(
      within(dialog).getByRole("radio", { name: /spice port/i }),
    );
    expect(
      within(dialog).queryByText("A USB mapping with that ID already exists"),
    ).toBeNull();
  });
});

async function openPCIDialog(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: /add device/i }));
  await user.click(
    await screen.findByRole("menuitem", { name: /pci device/i }),
  );
  return screen.findByRole("dialog");
}

describe("AddPCIDialog", () => {
  it("lists the node's devices by IOMMU group, named", async () => {
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openPCIDialog(user);

    const select = within(dialog).getByLabelText("Device");
    const groups = within(select)
      .getAllByRole("group")
      .map((g) => g.getAttribute("label"));
    expect(groups).toEqual([
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

  it("creates a mapping from the device's address alone, and stages the mapping", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:01:00.0",
    );
    expect(within(dialog).getByLabelText("Mapping name")).toHaveValue(
      "example-gpu",
    );
    // Function 1 shares the group, so the dialog asks first.
    await user.click(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );

    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=example-gpu");
    });
    // The server copies the ids, group and mdev flag from the node's own
    // report of the device; the dialog sends only where it is.
    expect(mockedPost).toHaveBeenCalledWith(PCI_CREATE_URL, {
      mapping_id: "example-gpu",
      node: NODE,
      path: "0000:01:00.0",
      description: "Example GPU",
    });
  });

  it("maps the whole device, all its functions, when asked", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:01:00.1",
    );
    await user.click(within(dialog).getByLabelText(/All functions/));
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );

    await waitFor(() => {
      expect(onAdd).toHaveBeenCalled();
    });
    expect(mockedPost).toHaveBeenCalledWith(
      PCI_CREATE_URL,
      expect.objectContaining({ path: "0000:01:00" }),
    );
  });

  it("reuses the mapping that already passes the device exactly, without creating one", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:02:00.0",
    );
    expect(
      await within(dialog).findByText(/Uses the existing mapping “pcidev01”/),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev01");
    expect(mockedPost).not.toHaveBeenCalled();
  });

  // Each differs from what the node reports for the device in one way that
  // Proxmox refuses at start — or, with two entries here, may hand the VM the
  // other device.
  it.each<[string, PCIMapping]>([
    [
      "a subsystem id the device does not have",
      {
        id: "pcidev09",
        description: "",
        map: ["id=1234:0002,node=pve-01,path=0000:02:00.0,subsystem-id=abcd:0002"],
        checks: [],
        mdev: false,
      },
    ],
    [
      "an IOMMU group the device is not in",
      {
        id: "pcidev09",
        description: "",
        map: ["id=1234:0002,iommugroup=3,node=pve-01,path=0000:02:00.0"],
        checks: [],
        mdev: false,
      },
    ],
    [
      "an mdev flag the device does not need",
      {
        id: "pcidev09",
        description: "",
        map: ["id=1234:0002,node=pve-01,path=0000:02:00.0"],
        checks: [],
        mdev: true,
      },
    ],
    [
      "a second device on this node",
      {
        id: "pcidev09",
        description: "",
        map: [
          "id=1234:0002,node=pve-01,path=0000:02:00.0",
          "id=1234:0003,node=pve-01,path=0000:03:00.0",
        ],
        checks: [],
        mdev: false,
      },
    ],
  ])("does not reuse a mapping with %s", async (_why, candidate) => {
    pciMappingList = [candidate];
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:02:00.0",
    );
    expect(
      await within(dialog).findByRole("button", {
        name: "Create mapping & add",
      }),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByText(/Uses the existing mapping/),
    ).not.toBeInTheDocument();
  });

  it("reuses a mapping that also has entries for other nodes", async () => {
    pciMappingList = [
      {
        id: "pcidev05",
        description: "",
        map: [
          "id=1234:0002,node=pve-02,path=0000:05:00.0",
          "id=1234:0002,node=pve-01,path=0000:02:00.0",
        ],
        checks: [],
        mdev: false,
      },
    ];
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:02:00.0",
    );
    await user.click(await within(dialog).findByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev05");
  });

  it("adds a mapped device, and shows what Proxmox says about it on this node", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.click(within(dialog).getByLabelText(/Mapped device/));
    const select = await within(dialog).findByLabelText("Mapping");
    expect(
      within(select)
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual([
      "Select a mapping...",
      "pcidev01 — Example NIC",
      "pcidev02 (warning)",
      "pcidev03 (error)",
    ]);
    // A clean one says what it passes through here.
    await user.selectOptions(select, "pcidev01");
    expect(within(dialog).getByText("On pve-01: 0000:02:00.0")).toBeInTheDocument();
    await user.selectOptions(select, "pcidev02");
    expect(
      within(dialog).getByText("No mapping for node pve-01."),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Add" }));

    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev02");
  });

  it("offers PCIe only on the q35 machine type, and ticks it there", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu({ config: { machine: "pc-q35-9.0" } });
    const dialog = await openPCIDialog(user);

    expect(within(dialog).getByLabelText(/^PCIe/)).toBeEnabled();
    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:02:00.0",
    );
    await user.click(await within(dialog).findByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev01,pcie=1");
  });

  it("never writes PCIe for any other machine type", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu({ config: { machine: "pc-i440fx-9.0" } });
    const dialog = await openPCIDialog(user);

    const pcie = within(dialog).getByLabelText(/^PCIe/);
    expect(pcie).toBeDisabled();
    expect(pcie).not.toBeChecked();
    expect(within(dialog).getByText("(q35 only)")).toBeInTheDocument();
    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:02:00.0",
    );
    await user.click(await within(dialog).findByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev01");
  });

  // The config is re-read while the dialog is open; a VM moved off q35
  // meanwhile must not get the pcie=1 ticked for it before.
  it("drops PCIe when the VM leaves q35 while the dialog is open", async () => {
    const user = userEvent.setup();
    const onAdd = vi.fn();
    const menu = (config: VMConfig) => (
      <AddDeviceMenu
        config={config}
        clusterId={CLUSTER}
        nodeName={NODE}
        diskStorages={[]}
        usbDevices={devices}
        pciDevices={pciDeviceList}
        bridges={["vmbr0"]}
        isoFiles={[]}
        onAddDevice={onAdd}
        onAddCDROM={vi.fn()}
        onAddDisk={vi.fn()}
      />
    );
    const { rerender } = renderWithProviders(menu({ machine: "q35" }));
    const dialog = await openPCIDialog(user);
    expect(within(dialog).getByLabelText(/^PCIe/)).toBeChecked();

    rerender(menu({ machine: "pc-i440fx-9.0" }));
    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:02:00.0",
    );
    await user.click(await within(dialog).findByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev01");
  });

  it("writes ROM-BAR off and Primary GPU when set", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:02:00.0",
    );
    await user.click(within(dialog).getByLabelText("ROM-BAR"));
    await user.click(within(dialog).getByLabelText(/Primary GPU/));
    await user.click(await within(dialog).findByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith(
      "hostpci0",
      "mapping=pcidev01,rombar=0,x-vga=1",
    );
  });

  it("cannot create a mapping without manage:cluster, but can still reuse one", async () => {
    setPermissions([]);
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    // Alone in its group, so nothing but the permission holds the button.
    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:04:00.0",
    );
    expect(
      await within(dialog).findByText(/Creating a mapping needs the Manage Cluster permission/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    ).toBeDisabled();

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:02:00.0",
    );
    await user.click(await within(dialog).findByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev01");
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("keeps Proxmox's refusal on screen and stages nothing", async () => {
    mockedPost.mockRejectedValue(
      new ApiClientError(409, {
        error: "conflict",
        message: "A PCI mapping with that ID already exists",
      }),
    );
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:04:00.0",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );
    expect(
      await within(dialog).findByText("A PCI mapping with that ID already exists"),
    ).toBeInTheDocument();
    expect(onAdd).not.toHaveBeenCalled();
  });

  it("takes a typed address when the node lists no devices, and refuses a malformed one", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu({ pciDevices: "unavailable" });
    const dialog = await openPCIDialog(user);

    const input = within(dialog).getByLabelText("Device");
    await user.type(input, "01:00.0");
    expect(
      within(dialog).getByText(/An address like 0000:01:00.0/),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByRole("button", { name: "Create mapping & add" }),
    ).not.toBeInTheDocument();

    await user.clear(input);
    await user.type(input, "0000:01:00.0");
    await user.clear(within(dialog).getByLabelText("Mapping name"));
    await user.type(within(dialog).getByLabelText("Mapping name"), "gpu01");
    // Nothing to look the address up in, so the dialog asks.
    expect(
      within(dialog).getByText(
        "Nexara cannot tell what 0000:01:00.0 is, or what shares its IOMMU group: check in Proxmox that pve-01 does not need it.",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    ).toBeDisabled();
    await user.click(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=gpu01");
    });
    // No label to describe it with: the node did not name it.
    expect(mockedPost).toHaveBeenCalledWith(PCI_CREATE_URL, {
      mapping_id: "gpu01",
      node: NODE,
      path: "0000:01:00.0",
    });
  });

  it("locks while a create is in flight, then stages exactly once", async () => {
    let resolvePost: (value: unknown) => void = () => undefined;
    mockedPost.mockImplementation(
      () =>
        new Promise((resolve) => {
          resolvePost = resolve;
        }),
    );
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:04:00.0",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );

    expect(
      await within(dialog).findByRole("button", { name: "Creating mapping…" }),
    ).toBeDisabled();
    expect(within(dialog).getByRole("button", { name: "Cancel" })).toBeDisabled();
    expect(within(dialog).getByLabelText("Mapping name")).toBeDisabled();
    expect(within(dialog).getByLabelText("Device")).toBeDisabled();
    expect(within(dialog).getByLabelText(/All functions/)).toBeDisabled();
    for (const radio of within(dialog).getAllByRole("radio")) {
      expect(radio).toBeDisabled();
    }
    await user.keyboard("{Escape}");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(onAdd).not.toHaveBeenCalled();

    resolvePost({ status: "ok" });
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalledTimes(1);
    });
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=example-vgpu");
    expect(mockedPost).toHaveBeenCalledTimes(1);
  });

  it("waits for the mapping listing before a host pick can add", async () => {
    mockedList.mockImplementation(() => new Promise(() => undefined));
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:04:00.0",
    );
    expect(
      within(dialog).getByText("Checking the cluster's PCI mappings…"),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Add" })).toBeDisabled();
  });

  it("will not create from a listing that failed, and says why", async () => {
    mockedList.mockImplementation((path: string) =>
      path === PCI_MAPPINGS_URL
        ? Promise.reject(
            new ApiClientError(502, {
              error: "bad_gateway",
              message: "Failed to connect to Proxmox",
            }),
          )
        : Promise.resolve([]),
    );
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:04:00.0",
    );
    expect(
      await within(dialog).findByText(
        "Could not list the cluster's PCI mappings: Failed to connect to Proxmox",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Add" })).toBeDisabled();
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("refuses a mapping name Proxmox would, or one already taken", async () => {
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:04:00.0",
    );
    const name = within(dialog).getByLabelText("Mapping name");
    await user.clear(name);
    await user.type(name, "1gpu");
    expect(
      within(dialog).getByText(/Start with a letter/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    ).toBeDisabled();

    await user.clear(name);
    await user.type(name, "pcidev01");
    expect(
      within(dialog).getByText('A mapping named "pcidev01" already exists for other hardware.'),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    ).toBeDisabled();
  });

  it("clears Proxmox's refusal once the pick changes", async () => {
    mockedPost.mockRejectedValue(
      new ApiClientError(409, {
        error: "conflict",
        message: "A PCI mapping with that ID already exists",
      }),
    );
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:04:00.0",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );
    expect(
      await within(dialog).findByText("A PCI mapping with that ID already exists"),
    ).toBeInTheDocument();
    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:02:00.0",
    );
    expect(
      within(dialog).queryByText("A PCI mapping with that ID already exists"),
    ).not.toBeInTheDocument();
  });

  it("names the whole device after its function 0", async () => {
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:01:00.1",
    );
    expect(within(dialog).getByLabelText("Mapping name")).toHaveValue(
      "example-gpu-audio",
    );
    await user.click(within(dialog).getByLabelText(/All functions/));
    expect(within(dialog).getByLabelText("Mapping name")).toHaveValue(
      "example-gpu",
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );
    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledWith(PCI_CREATE_URL, {
        mapping_id: "example-gpu",
        node: NODE,
        path: "0000:01:00",
        description: "Example GPU",
      });
    });
  });

  // Without the device's record there is nothing to compare an entry with,
  // but a mapping Proxmox checked clean on this node matches the device.
  it("reuses a mapping Proxmox checked clean for a typed address, and only that", async () => {
    pciMappingList = [
      {
        id: "pcidev07",
        description: "",
        map: ["id=1234:0007,node=pve-01,path=0000:07:00.0"],
        checks: [
          {
            severity: "error",
            message: "Invalid configuration: 'id' does not match for 'pcidev07' (1234:0008 != 1234:0007)",
          },
        ],
        mdev: false,
      },
      {
        id: "pcidev08",
        description: "",
        map: ["id=1234:0008,node=pve-01,path=0000:07:00.0"],
        checks: [],
        mdev: false,
      },
    ];
    const user = userEvent.setup();
    const onAdd = renderMenu({ pciDevices: "unavailable" });
    const dialog = await openPCIDialog(user);

    await user.type(within(dialog).getByLabelText("Device"), "0000:07:00.0");
    expect(
      await within(dialog).findByText(/Uses the existing mapping “pcidev08”/),
    ).toBeInTheDocument();
    await user.click(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    );
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev08");
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("drops an address typed while the node's list was loading once the list is there", async () => {
    const user = userEvent.setup();
    const onAdd = vi.fn();
    const menu = (pci: NodePCIDevice[] | undefined) => (
      <AddDeviceMenu
        config={{}}
        clusterId={CLUSTER}
        nodeName={NODE}
        diskStorages={[]}
        usbDevices={devices}
        pciDevices={pci}
        bridges={["vmbr0"]}
        isoFiles={[]}
        onAddDevice={onAdd}
        onAddCDROM={vi.fn()}
        onAddDisk={vi.fn()}
      />
    );
    const { rerender } = renderWithProviders(menu(undefined));
    const dialog = await openPCIDialog(user);
    await user.type(within(dialog).getByLabelText("Device"), "0000:09:00.0");
    expect(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    ).toBeInTheDocument();

    rerender(menu(pciDeviceList));
    expect(within(dialog).getByLabelText("Device")).toHaveValue("");
    expect(
      within(dialog).queryByRole("button", { name: "Create mapping & add" }),
    ).not.toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Add" })).toBeDisabled();
  });

  it("asks about a mapped device the node's list does not hold", async () => {
    pciMappingList = [
      {
        id: "pcidev11",
        description: "",
        map: ["id=1234:0011,node=pve-01,path=0000:0b:00.0"],
        checks: [],
        mdev: false,
      },
    ];
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.click(within(dialog).getByLabelText(/Mapped device/));
    await user.selectOptions(
      await within(dialog).findByLabelText("Mapping"),
      "pcidev11",
    );
    expect(
      within(dialog).getByText(/Nexara cannot tell what 0000:0b:00.0 is/),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Add" })).toBeDisabled();
    await user.click(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    );
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev11");
  });

  // The tick confirmed what was said then; a warning the loaded list adds
  // afterwards is asked about again.
  it("asks again when the node's list adds a warning after the tick", async () => {
    pciMappingList = [
      {
        id: "pcidev06",
        description: "",
        map: ["id=1234:0005,iommugroup=20,node=pve-01,path=0000:05:00.0"],
        checks: [],
        mdev: false,
      },
    ];
    const user = userEvent.setup();
    const menu = (pci: NodePCIDevice[] | undefined) => (
      <AddDeviceMenu
        config={{}}
        clusterId={CLUSTER}
        nodeName={NODE}
        diskStorages={[]}
        usbDevices={devices}
        pciDevices={pci}
        bridges={["vmbr0"]}
        isoFiles={[]}
        onAddDevice={vi.fn()}
        onAddCDROM={vi.fn()}
        onAddDisk={vi.fn()}
      />
    );
    const { rerender } = renderWithProviders(menu(undefined));
    const dialog = await openPCIDialog(user);
    await user.click(within(dialog).getByLabelText(/Mapped device/));
    await user.selectOptions(
      await within(dialog).findByLabelText("Mapping"),
      "pcidev06",
    );
    expect(within(dialog).getByText(/Nexara cannot tell what 0000:05:00.0 is/)).toBeInTheDocument();
    await user.click(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    );
    expect(within(dialog).getByRole("button", { name: "Add" })).toBeEnabled();

    rerender(menu(pciDeviceList));
    expect(
      within(dialog).getByText(/0000:05:00.0 is a storage controller/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    ).not.toBeChecked();
    expect(within(dialog).getByRole("button", { name: "Add" })).toBeDisabled();
  });

  it("names every device a mapping has on the node when Proxmox reports nothing", async () => {
    pciMappingList = [
      {
        id: "pcidev10",
        description: "",
        map: [
          "id=1234:0002,node=pve-01,path=0000:02:00.0",
          "id=1234:0002,node=pve-01,path=0000:03:00.0",
        ],
        checks: [],
        mdev: false,
      },
    ];
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openPCIDialog(user);

    await user.click(within(dialog).getByLabelText(/Mapped device/));
    await user.selectOptions(
      await within(dialog).findByLabelText("Mapping"),
      "pcidev10",
    );
    expect(
      within(dialog).getByText(
        "On pve-01: 0000:02:00.0, 0000:03:00.0 — the first one not in use when the VM starts",
      ),
    ).toBeInTheDocument();
  });

  it("warns before taking a device the node may need, and holds Add until told", async () => {
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:05:00.0",
    );
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
    const create = within(dialog).getByRole("button", {
      name: "Create mapping & add",
    });
    expect(create).toBeDisabled();

    await user.click(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    );
    expect(create).toBeEnabled();

    // The tick was for that pick; another one is asked about afresh.
    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:06:00.0",
    );
    expect(
      within(dialog).getByText(
        "0000:06:00.0 is a USB controller: the node loses every USB device on it.",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    ).not.toBeChecked();
    expect(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    ).toBeDisabled();
    await user.click(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping & add" }),
    );
    await waitFor(() => {
      expect(onAdd).toHaveBeenCalled();
    });
  });

  it("warns nothing for a device alone in its group, or a whole device's own functions", async () => {
    const user = userEvent.setup();
    renderMenu();
    const dialog = await openPCIDialog(user);

    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:04:00.0",
    );
    expect(
      within(dialog).queryByLabelText("The node does not need it; pass it through"),
    ).not.toBeInTheDocument();

    // Both functions of the GPU are passed, so neither is taken as a peer.
    await user.selectOptions(
      within(dialog).getByLabelText("Device"),
      "0000:01:00.0",
    );
    expect(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    ).toBeInTheDocument();
    await user.click(within(dialog).getByLabelText(/All functions/));
    expect(
      within(dialog).queryByLabelText("The node does not need it; pass it through"),
    ).not.toBeInTheDocument();
  });

  it("warns for a mapped device the node may need too", async () => {
    pciMappingList = [
      {
        id: "pcidev06",
        description: "",
        map: ["id=1234:0005,iommugroup=20,node=pve-01,path=0000:05:00.0"],
        checks: [],
        mdev: false,
      },
    ];
    const user = userEvent.setup();
    const onAdd = renderMenu();
    const dialog = await openPCIDialog(user);

    await user.click(within(dialog).getByLabelText(/Mapped device/));
    await user.selectOptions(
      await within(dialog).findByLabelText("Mapping"),
      "pcidev06",
    );
    expect(
      within(dialog).getByText(/0000:05:00.0 is a storage controller/),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Add" })).toBeDisabled();
    await user.click(
      within(dialog).getByLabelText("The node does not need it; pass it through"),
    );
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect(onAdd).toHaveBeenCalledWith("hostpci0", "mapping=pcidev06");
  });

  it("shows why when every slot is taken", async () => {
    const config: VMConfig = {};
    for (let i = 0; i <= 15; i++) {
      config[`hostpci${String(i)}`] = "mapping=pcidev01";
    }
    const user = userEvent.setup();
    renderMenu({ config });
    const dialog = await openPCIDialog(user);

    expect(
      within(dialog).getByText("All 16 PCI slots are in use."),
    ).toBeInTheDocument();
  });
});
