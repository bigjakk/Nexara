import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { NodeUSBDevice } from "../../api/vm-queries";
import type { USBMapping } from "@/features/mappings/api/mapping-queries";
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
}: {
  config?: VMConfig;
  usbDevices?: NodeUSBDevice[] | "unavailable";
} = {}) {
  const onAddDevice = vi.fn();
  renderWithProviders(
    <AddDeviceMenu
      config={config}
      clusterId={CLUSTER}
      nodeName={NODE}
      diskStorages={[]}
      usbDevices={usbDevices === "unavailable" ? undefined : usbDevices}
      pciDevices={[]}
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

beforeEach(() => {
  vi.resetAllMocks();
  setPermissions(["manage:cluster"]);
  mockedList.mockImplementation((path: string) =>
    path === MAPPINGS_URL
      ? Promise.resolve(mappings)
      : Promise.reject(new Error(`unexpected list ${path}`)),
  );
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
