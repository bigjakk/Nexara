import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  onlineManager,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { queryClient as appQueryClient } from "@/lib/query-client";
import { MemoryRouter } from "react-router-dom";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { NodeResponse } from "@/types/api";
import type { NodeUSBDevice } from "@/features/vms/api/vm-queries";
import type {
  ClusterUSBMapping,
  USBMappingUsage,
} from "../api/mapping-queries";
import { USBMappingsCard } from "./USBMappingsCard";

// The transport is mocked, not the hooks, so the real queries and mutations
// run and each test asserts the request that would leave the browser.
vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      get: vi.fn(),
      list: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  };
});

const mockedGet = vi.mocked(apiClient.get);
const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);
const mockedDelete = vi.mocked(apiClient.delete);

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const LIST_URL = `/api/v1/clusters/${CLUSTER}/usb-mappings`;
const NODES_URL = `/api/v1/clusters/${CLUSTER}/nodes`;
const mappingURL = (id: string) =>
  `/api/v1/clusters/${CLUSTER}/usb-mappings/${id}`;
const devicesURL = (node: string) =>
  `/api/v1/clusters/${CLUSTER}/nodes/${node}/hardware/usb`;

function conflict(): ApiClientError {
  return new ApiClientError(409, {
    error: "conflict",
    message: "The cluster's USB mappings changed since they were loaded",
  });
}

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

const node = (name: string, status = "online") =>
  ({ name, status }) as NodeResponse;

// usbdev01 has three entries: clean on pve-01, a Proxmox error on pve-02,
// unchecked on pve-03. usbdev02 has one entry, so removing it deletes the
// mapping. usbdev03 has two entries for pve-01, which qemu-server refuses.
// usbdev04's entry carries a description of its own.
function baseMappings(digest = "d1"): ClusterUSBMapping[] {
  return [
    {
      id: "usbdev02",
      description: "",
      digest,
      map: ["node=pve-01,id=1234:5678"],
      node_checks: { "pve-01": [] },
      unchecked: {},
    },
    {
      id: "usbdev01",
      description: "Example Radio",
      digest,
      map: [
        "node=pve-01,id=abcd:ef01",
        "node=pve-02,id=abcd:ef01,path=1-3",
        "node=pve-03,id=abcd:ef01",
      ],
      node_checks: {
        "pve-01": [],
        "pve-02": [
          {
            severity: "error",
            message: "Invalid configuration: usb device '1-3' not found",
          },
        ],
      },
      unchecked: { "pve-03": "The node is offline." },
    },
    {
      id: "usbdev03",
      description: "",
      digest,
      // The same entry twice, as Proxmox's API will store it.
      map: [
        "node=pve-01,id=9999:0001",
        "node=pve-01,id=9999:0001",
        "node=pve-02,id=9999:0001",
      ],
      node_checks: { "pve-01": [], "pve-02": [] },
      unchecked: {},
    },
    {
      id: "usbdev04",
      description: "",
      digest,
      map: [
        "node=pve-01,id=5555:0004,description=left port",
        "node=pve-02,id=5555:0004",
      ],
      node_checks: { "pve-01": [], "pve-02": [] },
      unchecked: {},
    },
  ];
}

let listing: ClusterUSBMapping[] = baseMappings();
let listFails = false;
// When set, the listing answers with this instead, until a test settles it.
let listGate: Promise<ClusterUSBMapping[]> | null = null;

const nodeDevices: Record<string, NodeUSBDevice[]> = {
  "pve-02": [
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
  ],
  "pve-01": [
    usbDevice({
      devnum: 5,
      vendid: "5555",
      prodid: "0004",
      product: "Example Receiver",
      usbpath: "7",
    }),
    usbDevice({
      devnum: 6,
      vendid: "6666",
      prodid: "0006",
      product: "Example Key",
      usbpath: "8",
    }),
  ],
};

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

/**
 * `cached` gives the client the app's own query defaults
 * (lib/query-client.ts) — its cache times, and no refetch on window focus —
 * so a test of what the cache may serve runs against what production does.
 * Only retry is turned off. The default is no cache, so no other test leans
 * on a cached answer by accident.
 */
function renderCard({ cached = false }: { cached?: boolean } = {}) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: cached
        ? { ...appQueryClient.getDefaultOptions().queries, retry: false }
        : { retry: false, gcTime: 0 },
    },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <USBMappingsCard clusterId={CLUSTER} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return queryClient;
}

/** The header row of a mapping, where its own actions sit. */
async function mappingRow(id: string) {
  const name = await screen.findByText(id);
  const row = name.closest("tr");
  if (!row) throw new Error(`no row for ${id}`);
  return row;
}

/** The entry row for `node` within mapping `id`: the rows after its header. */
async function entryRow(id: string, nodeName: string, nth = 0) {
  const header = await mappingRow(id);
  const rows: HTMLElement[] = [];
  let next = header.nextElementSibling;
  while (
    next instanceof HTMLElement &&
    !next.classList.contains("bg-muted/30")
  ) {
    const first = next.querySelector("td");
    if (first?.textContent === nodeName) rows.push(next);
    next = next.nextElementSibling;
  }
  const row = rows[nth];
  if (!row)
    throw new Error(`no entry row ${String(nth)} for ${nodeName} in ${id}`);
  return row;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (err: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  vi.resetAllMocks();
  setPermissions(["view:cluster", "manage:cluster"]);
  listing = baseMappings();
  listFails = false;
  listGate = null;
  mockedList.mockImplementation((path: string) => {
    if (path === LIST_URL) {
      if (listGate !== null) return listGate;
      return listFails
        ? Promise.reject(new Error("list failed"))
        : Promise.resolve(listing);
    }
    if (path === NODES_URL) {
      return Promise.resolve([
        node("pve-01"),
        node("pve-02"),
        node("pve-03", "offline"),
        node("pve-04"),
      ]);
    }
    for (const [n, devs] of Object.entries(nodeDevices)) {
      if (path === devicesURL(n)) return Promise.resolve(devs);
    }
    return Promise.reject(new Error(`unexpected list ${path}`));
  });
  mockedPut.mockResolvedValue({ status: "ok" });
  mockedPost.mockResolvedValue({ status: "ok" });
  mockedDelete.mockResolvedValue({ status: "ok" });
});

describe("USBMappingsCard listing", () => {
  it("shows each entry's check on its own node, and never an unchecked node as OK", async () => {
    renderCard();

    expect(
      within(await entryRow("usbdev01", "pve-01")).getByText("OK"),
    ).toBeInTheDocument();

    const pve02 = await entryRow("usbdev01", "pve-02");
    expect(
      within(pve02).getByText(
        "Invalid configuration: usb device '1-3' not found",
      ),
    ).toBeInTheDocument();
    expect(within(pve02).queryByText("OK")).not.toBeInTheDocument();
    expect(within(pve02).getByText("1-3")).toBeInTheDocument();

    const pve03 = await entryRow("usbdev01", "pve-03");
    expect(
      within(pve03).getByText("Not checked: The node is offline."),
    ).toBeInTheDocument();
    expect(within(pve03).queryByText("OK")).not.toBeInTheDocument();
    expect(within(pve03).getByText("Any port")).toBeInTheDocument();
  });

  it("says a node in neither map was not checked", async () => {
    listing = [
      {
        id: "usbdev05",
        description: "",
        digest: "d1",
        map: ["node=pve-01,id=1234:5678"],
        node_checks: {},
        unchecked: {},
      },
    ];
    renderCard();
    const row = await entryRow("usbdev05", "pve-01");
    expect(within(row).getByText("Not checked.")).toBeInTheDocument();
    expect(within(row).queryByText("OK")).not.toBeInTheDocument();
  });

  it("flags two entries for one node, which qemu-server refuses", async () => {
    renderCard();
    const first = await entryRow("usbdev03", "pve-01", 0);
    expect(
      within(first).getByText(/pve-01 has 2 entries: Proxmox refuses/),
    ).toBeInTheDocument();
    // A clean check does not read as OK next to that.
    expect(within(first).queryByText("OK")).not.toBeInTheDocument();
  });

  it("shows an entry's own description and the mapping's", async () => {
    renderCard();
    expect(await screen.findByText("Example Radio")).toBeInTheDocument();
    expect(
      within(await entryRow("usbdev04", "pve-01")).getByText("left port"),
    ).toBeInTheDocument();
  });

  it("reports a failed listing rather than an empty one", async () => {
    listFails = true;
    renderCard();
    expect(
      await screen.findByText("Could not load the USB mappings."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/no USB mappings yet/)).not.toBeInTheDocument();
  });

  it("says so when the cluster has none", async () => {
    listing = [];
    renderCard();
    expect(
      await screen.findByText(/This cluster has no USB mappings yet/),
    ).toBeInTheDocument();
  });

  it("is read-only without manage:cluster, and says why", async () => {
    setPermissions(["view:cluster"]);
    renderCard();
    await mappingRow("usbdev01");
    expect(
      screen.getByText(
        /Viewing only: changing mappings needs the Manage Cluster permission/,
      ),
    ).toBeInTheDocument();
    for (const name of [
      "New mapping",
      "Add node",
      "Edit description",
      "Delete",
      "Replace device",
      "Remove",
    ]) {
      expect(
        screen.queryByRole("button", { name: new RegExp(`^${name}$`) }),
      ).not.toBeInTheDocument();
    }
  });
});

describe("USBMappingsCard edits", () => {
  it("Add node sends the whole list plus the new entry, with the listing's digest", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev02")).getByRole("button", {
        name: /Add node/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    // Nodes that already have an entry are not offered.
    const options = within(within(dialog).getByLabelText("Node"))
      .getAllByRole("option")
      .map((o) => o.textContent);
    expect(options).toEqual(["Select a node...", "pve-02", "pve-03", "pve-04"]);

    await user.selectOptions(within(dialog).getByLabelText("Node"), "pve-02");
    // The device the other entries pass is picked for this node too.
    await waitFor(() => {
      expect(within(dialog).getByLabelText("Device")).toHaveValue("1234:5678");
    });
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(mappingURL("usbdev02"), {
        map: ["node=pve-01,id=1234:5678", "node=pve-02,id=1234:5678"],
        digest: "d1",
      });
    });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
  });

  // The cluster listing checks every node, which can take a while; the
  // dialog does not wait for it once the write is done.
  it("a successful save closes at once, and the listing is read again behind it", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    const input = within(dialog).getByLabelText("Description");
    await user.clear(input);
    await user.type(input, "Example Radio, desk");

    const reads = mockedList.mock.calls.filter(([p]) => p === LIST_URL).length;
    const refreshed = deferred<ClusterUSBMapping[]>();
    listGate = refreshed.promise;
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    // Closed while the re-read is still out.
    expect(mockedList.mock.calls.filter(([p]) => p === LIST_URL).length).toBe(
      reads + 1,
    );

    const after = baseMappings("d2");
    const edited = after.find((m) => m.id === "usbdev01");
    if (!edited) throw new Error("fixture");
    edited.description = "Example Radio, desk";
    listGate = null;
    refreshed.resolve(after);
    expect(await screen.findByText("Example Radio, desk")).toBeInTheDocument();
  });

  it("an emptied or padded description is sent trimmed", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    const input = within(dialog).getByLabelText("Description");
    // Only the padding differs from what is stored: nothing to save.
    await user.type(input, "  ");
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    await user.clear(input);
    await user.type(input, "   ");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(
        mappingURL("usbdev01"),
        expect.objectContaining({ description: "" }),
      );
    });
  });

  it("Replace device keeps the node, the other entries and the entry's own description", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await entryRow("usbdev04", "pve-01")).getByRole("button", {
        name: /Replace device/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.click(
      within(dialog).getByRole("radio", { name: /Host USB port/ }),
    );
    // Each port is offered with the device on it, and the port itself.
    await waitFor(() => {
      expect(
        within(within(dialog).getByLabelText("Port"))
          .getAllByRole("option")
          .map((o) => o.textContent),
      ).toEqual([
        "Select a port...",
        "Example Receiver (1-7)",
        "Example Key (1-8)",
      ]);
    });
    await user.selectOptions(within(dialog).getByLabelText("Port"), "1-8");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(mappingURL("usbdev04"), {
        map: [
          "node=pve-01,id=6666:0006,path=1-8,description=left port",
          "node=pve-02,id=5555:0004",
        ],
        digest: "d1",
      });
    });
  });

  it("Replace device takes a typed id when the node's devices cannot be listed", async () => {
    const user = userEvent.setup();
    renderCard();
    // pve-03 is offline: its device listing fails.
    await user.click(
      within(await entryRow("usbdev01", "pve-03")).getByRole("button", {
        name: /Replace device/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    expect(
      await within(dialog).findByText(/Could not list the node's USB devices/),
    ).toBeInTheDocument();
    const typed = within(dialog).getByLabelText("Device");
    await user.type(typed, " ABCD:0001 ");
    expect(typed).toHaveValue("ABCD:0001");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(mappingURL("usbdev01"), {
        map: [
          "node=pve-01,id=abcd:ef01",
          "node=pve-02,id=abcd:ef01,path=1-3",
          "node=pve-03,id=abcd:0001",
        ],
        digest: "d1",
      });
    });
  });

  it("Replace device will not save the device the entry already passes", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await entryRow("usbdev04", "pve-01")).getByRole("button", {
        name: /Replace device/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "5555:0004",
    );
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    expect(
      within(dialog).getByText("That is the device the entry passes now."),
    ).toBeInTheDocument();
  });

  it("Edit description sends the entries unchanged and the new description", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    const input = within(dialog).getByLabelText("Description");
    expect(input).toHaveValue("Example Radio");
    await user.clear(input);
    await user.type(input, "Example Radio, desk");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(mappingURL("usbdev01"), {
        map: [
          "node=pve-01,id=abcd:ef01",
          "node=pve-02,id=abcd:ef01,path=1-3",
          "node=pve-03,id=abcd:ef01",
        ],
        description: "Example Radio, desk",
        digest: "d1",
      });
    });
  });

  it("an emptied description is sent empty, which removes it", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.clear(within(dialog).getByLabelText("Description"));
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(
        mappingURL("usbdev01"),
        expect.objectContaining({ description: "" }),
      );
    });
  });

  // reference_cas_token_pin_class: the digest comes from the read the dialog
  // was opened from. A refetch landing while it is open — a WebSocket
  // reconnect, another tab's invalidation — must not swap it under values
  // that never moved with it.
  it("a refetch while the dialog is open does not move the pinned digest", async () => {
    const user = userEvent.setup();
    const queryClient = renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");

    listing = baseMappings("d2");
    await queryClient.invalidateQueries({
      queryKey: ["clusters", CLUSTER, "usb-mappings"],
    });
    await waitFor(() => {
      expect(
        mockedList.mock.calls.filter(([p]) => p === LIST_URL).length,
      ).toBeGreaterThanOrEqual(2);
    });

    await user.type(within(dialog).getByLabelText("Description"), "!");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(
        mappingURL("usbdev01"),
        expect.objectContaining({ digest: "d1" }),
      );
    });
  });

  it("a 409 re-reads the mappings, shows them, and pins the new read", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev02")).getByRole("button", {
        name: /Add node/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(within(dialog).getByLabelText("Node"), "pve-02");
    await waitFor(() => {
      expect(within(dialog).getByLabelText("Device")).toHaveValue("1234:5678");
    });

    // Meanwhile someone gave usbdev02 an entry for pve-04.
    const moved = baseMappings("d2");
    const target = moved.find((m) => m.id === "usbdev02");
    if (!target) throw new Error("fixture");
    target.map = ["node=pve-01,id=1234:5678", "node=pve-04,id=1234:5678"];
    mockedPut.mockRejectedValueOnce(conflict());
    listing = moved;

    const reads = mockedList.mock.calls.filter(([p]) => p === LIST_URL).length;
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(
      await within(dialog).findByText(/This dialog now shows them as they are/),
    ).toBeInTheDocument();
    // One read after the conflict: the dialog joins the one the save started
    // instead of cancelling it for a second.
    expect(mockedList.mock.calls.filter(([p]) => p === LIST_URL).length).toBe(
      reads + 1,
    );
    expect(
      within(dialog).getByText(/a change to any USB mapping counts/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(/^Nothing was saved\./),
    ).toBeInTheDocument();
    // pve-04 has an entry now, so it is no longer offered.
    expect(
      within(within(dialog).getByLabelText("Node"))
        .getAllByRole("option")
        .map((o) => o.textContent),
    ).toEqual(["Select a node...", "pve-02", "pve-03"]);

    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenLastCalledWith(mappingURL("usbdev02"), {
        map: [
          "node=pve-01,id=1234:5678",
          "node=pve-04,id=1234:5678",
          "node=pve-02,id=1234:5678",
        ],
        digest: "d2",
      });
    });
  });

  it("holds Save while the re-read after a 409 is in flight", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Description"), "!");

    const reread = deferred<ClusterUSBMapping[]>();
    listGate = reread.promise;
    mockedPut.mockRejectedValueOnce(conflict());
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(
      await within(dialog).findByText(/Reloading them…/),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
    // Closing is not held: only the write itself locks the dialog.
    expect(
      within(dialog).getByRole("button", { name: "Cancel" }),
    ).toBeEnabled();

    listGate = null;
    reread.resolve(baseMappings("d2"));
    expect(
      await within(dialog).findByText(/This dialog now shows them as they are/),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("a 409 whose re-read fails keeps the pin, and says so", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Description"), "!");

    mockedPut.mockRejectedValueOnce(conflict());
    listing = baseMappings("d2");
    listFails = true;
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(
      await within(dialog).findByText(/Reloading them failed/),
    ).toBeInTheDocument();

    listFails = false;
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(mockedPut).toHaveBeenLastCalledWith(
      mappingURL("usbdev01"),
      expect.objectContaining({ digest: "d1" }),
    );
  });

  it("a 409 for a mapping deleted meanwhile says so and will not save", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Description"), "!");

    mockedPut.mockRejectedValueOnce(conflict());
    listing = baseMappings("d2").filter((m) => m.id !== "usbdev01");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(
      await within(dialog).findByText(/The mapping usbdev01 no longer exists/),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
  });
});

describe("USBMappingsCard edit failures", () => {
  it("a failure that is not a 409 shows the server's words and moves no pin", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Description"), "x");

    mockedPut.mockRejectedValueOnce(
      new ApiClientError(400, {
        error: "bad_request",
        message: 'USB mapping entry "node=pve-03" needs id=<vendor:product>',
      }),
    );
    listing = baseMappings("d2");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(
      await within(dialog).findByText(
        'USB mapping entry "node=pve-03" needs id=<vendor:product>',
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByText(/a change to any USB mapping counts/),
    ).not.toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(mockedPut).toHaveBeenLastCalledWith(
      mappingURL("usbdev01"),
      expect.objectContaining({ digest: "d1" }),
    );
  });
});

describe("USBMappingsCard remove and delete", () => {
  it("Remove pins the digest of the list it was opened from", async () => {
    const user = userEvent.setup();
    const queryClient = renderCard();
    await user.click(
      within(await entryRow("usbdev01", "pve-02")).getByRole("button", {
        name: /Remove/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");

    listing = baseMappings("d2");
    await queryClient.invalidateQueries({
      queryKey: ["clusters", CLUSTER, "usb-mappings"],
    });
    await waitFor(() => {
      expect(
        mockedList.mock.calls.filter(([p]) => p === LIST_URL).length,
      ).toBeGreaterThanOrEqual(2);
    });

    await user.click(
      within(confirm).getByRole("button", { name: "Remove entry" }),
    );
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(
        mappingURL("usbdev01"),
        expect.objectContaining({ digest: "d1" }),
      );
    });
  });

  it("each opening of Delete checks the usage again", async () => {
    const user = userEvent.setup();
    mockedGet.mockResolvedValueOnce({
      mapping_id: "usbdev01",
      checked: 5,
      users: [],
      unchecked: [],
    });
    // With the app's own cache times, which would otherwise serve the first
    // answer again for five minutes.
    renderCard({ cached: true });
    const openDelete = async () => {
      await user.click(
        within(await mappingRow("usbdev01")).getByRole("button", {
          name: /^Delete$/,
        }),
      );
      return screen.findByRole("alertdialog");
    };
    let confirm = await openDelete();
    expect(
      await within(confirm).findByText(
        "No VM's current configuration uses it.",
      ),
    ).toBeInTheDocument();
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });

    // A VM started using it since.
    mockedGet.mockResolvedValueOnce({
      mapping_id: "usbdev01",
      checked: 5,
      users: [{ vmid: 101, name: "linux01", node: "pve-01", keys: ["usb0"] }],
      unchecked: [],
    });
    confirm = await openDelete();
    expect(
      await within(confirm).findByText("101 (linux01) on pve-01 — usb0"),
    ).toBeInTheDocument();
    expect(mockedGet).toHaveBeenCalledTimes(2);
  });

  // The connection dropping while the confirmation is open may have cut the
  // check short or outdated it: when it returns, the answer on screen is read
  // again — under the app's own defaults, five-minute cache included.
  it("the connection coming back while Delete is open checks the usage again", async () => {
    const user = userEvent.setup();
    mockedGet.mockResolvedValue({
      mapping_id: "usbdev01",
      checked: 5,
      users: [],
      unchecked: [],
    });
    renderCard({ cached: true });
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /^Delete$/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    expect(
      await within(confirm).findByText(
        "No VM's current configuration uses it.",
      ),
    ).toBeInTheDocument();
    expect(mockedGet).toHaveBeenCalledTimes(1);

    act(() => {
      onlineManager.setOnline(false);
    });
    act(() => {
      onlineManager.setOnline(true);
    });
    await waitFor(() => {
      expect(mockedGet).toHaveBeenCalledTimes(2);
    });
  });

  it("a usage re-check in flight holds the delete again", async () => {
    const user = userEvent.setup();
    mockedGet.mockResolvedValueOnce({
      mapping_id: "usbdev01",
      checked: 5,
      users: [],
      unchecked: [],
    });
    const queryClient = renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /^Delete$/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    const deleteButton = within(confirm).getByRole("button", {
      name: "Delete mapping",
    });
    await waitFor(() => {
      expect(deleteButton).toBeEnabled();
    });

    const recheck = deferred<USBMappingUsage>();
    mockedGet.mockReturnValueOnce(recheck.promise);
    void queryClient.invalidateQueries({
      queryKey: ["clusters", CLUSTER, "usb-mapping-usage"],
    });
    await waitFor(() => {
      expect(deleteButton).toBeDisabled();
    });
    expect(
      within(confirm).getByText(/Checking which VMs use it/),
    ).toBeInTheDocument();
    recheck.resolve({
      mapping_id: "usbdev01",
      checked: 5,
      users: [],
      unchecked: [],
    });
    await waitFor(() => {
      expect(deleteButton).toBeEnabled();
    });
  });

  it("Remove sends the list without the entry, after a confirmation", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await entryRow("usbdev01", "pve-02")).getByRole("button", {
        name: /Remove/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    expect(
      within(confirm).getByText("Remove pve-02's entry from usbdev01?"),
    ).toBeInTheDocument();
    expect(mockedPut).not.toHaveBeenCalled();
    await user.click(
      within(confirm).getByRole("button", { name: "Remove entry" }),
    );

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(mappingURL("usbdev01"), {
        map: ["node=pve-01,id=abcd:ef01", "node=pve-03,id=abcd:ef01"],
        digest: "d1",
      });
    });
  });

  it("Remove on one of two entries for a node removes that one", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await entryRow("usbdev03", "pve-01", 1)).getByRole("button", {
        name: /Remove/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    await user.click(
      within(confirm).getByRole("button", { name: "Remove entry" }),
    );
    // One of the two identical entries goes, not both.
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(mappingURL("usbdev03"), {
        map: ["node=pve-01,id=9999:0001", "node=pve-02,id=9999:0001"],
        digest: "d1",
      });
    });
  });

  it("a 409 on Remove says nothing was removed and why", async () => {
    const user = userEvent.setup();
    renderCard();
    mockedPut.mockRejectedValueOnce(conflict());
    await user.click(
      within(await entryRow("usbdev01", "pve-02")).getByRole("button", {
        name: /Remove/,
      }),
    );
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Remove entry",
      }),
    );
    expect(
      await screen.findByText(/Nothing was removed from usbdev01\./),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      /a change to any USB mapping counts/,
    );
  });

  it("removing the last entry deletes the mapping instead, after the usage check", async () => {
    const user = userEvent.setup();
    const usage: USBMappingUsage = {
      mapping_id: "usbdev02",
      checked: 3,
      users: [],
      unchecked: [],
    };
    mockedGet.mockResolvedValue(usage);
    renderCard();
    await user.click(
      within(await entryRow("usbdev02", "pve-01")).getByRole("button", {
        name: /Remove/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    expect(
      within(confirm).getByText("Delete USB mapping usbdev02?"),
    ).toBeInTheDocument();
    expect(
      within(confirm).getByText(/This is the mapping's only entry/),
    ).toBeInTheDocument();
    expect(
      await within(confirm).findByText(
        "No VM's current configuration uses it.",
      ),
    ).toBeInTheDocument();
    await user.click(
      within(confirm).getByRole("button", { name: "Delete mapping" }),
    );
    await waitFor(() => {
      expect(mockedDelete).toHaveBeenCalledWith(
        `${mappingURL("usbdev02")}?digest=d1`,
      );
    });
    // Never an update that empties the list.
    expect(mockedPut).not.toHaveBeenCalled();
  });

  it("Delete lists the VMs that use the mapping, and is held until the check answers", async () => {
    const user = userEvent.setup();
    const pending = deferred<USBMappingUsage>();
    mockedGet.mockReturnValue(pending.promise);
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /^Delete$/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    expect(mockedGet).toHaveBeenCalledWith(`${mappingURL("usbdev01")}/usage`);
    const deleteButton = within(confirm).getByRole("button", {
      name: "Delete mapping",
    });
    expect(deleteButton).toBeDisabled();
    expect(
      within(confirm).getByText(/Checking which VMs use it/),
    ).toBeInTheDocument();

    pending.resolve({
      mapping_id: "usbdev01",
      checked: 5,
      users: [
        { vmid: 101, name: "linux01", node: "pve-01", keys: ["usb0", "usb2"] },
      ],
      unchecked: [
        {
          vmid: 104,
          name: "win04",
          node: "pve-03",
          reason: "The node is offline.",
        },
      ],
    });
    expect(
      await within(confirm).findByText(
        "1 VM uses it and will not start after the delete:",
        { exact: false },
      ),
    ).toBeInTheDocument();
    expect(
      within(confirm).getByText("101 (linux01) on pve-01 — usb0, usb2"),
    ).toBeInTheDocument();
    expect(
      within(confirm).getByText("1 VM could not be checked and may use it:", {
        exact: false,
      }),
    ).toBeInTheDocument();
    expect(
      within(confirm).getByText("104 (win04) on pve-03 — The node is offline."),
    ).toBeInTheDocument();

    await waitFor(() => {
      expect(deleteButton).toBeEnabled();
    });
    await user.click(deleteButton);
    await waitFor(() => {
      expect(mockedDelete).toHaveBeenCalledWith(
        `${mappingURL("usbdev01")}?digest=d1`,
      );
    });
  });

  it("Delete is still on offer when the usage check fails, with the warning", async () => {
    const user = userEvent.setup();
    mockedGet.mockRejectedValue(
      new ApiClientError(403, {
        error: "forbidden",
        message: "Permission denied",
      }),
    );
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /^Delete$/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    expect(
      await within(confirm).findByText(
        /Could not check which VMs use it: Permission denied/,
      ),
    ).toBeInTheDocument();
    const deleteButton = within(confirm).getByRole("button", {
      name: "Delete mapping",
    });
    await waitFor(() => {
      expect(deleteButton).toBeEnabled();
    });
  });
});

describe("USBMappingsCard new mapping", () => {
  it("creates a mapping with one entry for the picked device", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      await screen.findByRole("button", { name: /New mapping/ }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(within(dialog).getByLabelText("Node"), "pve-02");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "abcd:ef01",
    );
    expect(within(dialog).getByLabelText("Name")).toHaveValue("example-radio");
    // usbdev01 already passes this device on pve-02 — by port, though, so
    // it is not the same pick; nothing is flagged.
    expect(
      within(dialog).queryByText(/already passes this device/),
    ).not.toBeInTheDocument();
    await user.click(
      within(dialog).getByRole("button", { name: "Create mapping" }),
    );
    await waitFor(() => {
      expect(mockedPost).toHaveBeenCalledWith(LIST_URL, {
        mapping_id: "example-radio",
        node: "pve-02",
        device_id: "abcd:ef01",
        description: "Example Radio",
      });
    });
  });

  it("refuses a name that is taken", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      await screen.findByRole("button", { name: /New mapping/ }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(within(dialog).getByLabelText("Node"), "pve-02");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "1234:5678",
    );
    const name = within(dialog).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "usbdev01");
    expect(
      within(dialog).getByText('A mapping named "usbdev01" already exists.'),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Create mapping" }),
    ).toBeDisabled();
  });
});

describe("USBMappingsCard edge cases", () => {
  it("Replace repairs an entry stored with an uppercase id", async () => {
    const user = userEvent.setup();
    listing = [
      {
        id: "usbdev06",
        description: "",
        digest: "d1",
        map: ["node=pve-02,id=ABCD:EF01"],
        node_checks: { "pve-02": [] },
        unchecked: {},
      },
    ];
    renderCard();
    await user.click(
      within(await entryRow("usbdev06", "pve-02")).getByRole("button", {
        name: /Replace device/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "abcd:ef01",
    );
    // The same device, but the stored id is broken: Proxmox compares it
    // against the node's lowercase hex. Saving it writes it lowercase.
    expect(
      within(dialog).queryByText("That is the device the entry passes now."),
    ).not.toBeInTheDocument();
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledWith(mappingURL("usbdev06"), {
        map: ["node=pve-02,id=abcd:ef01"],
        digest: "d1",
      });
    });
  });

  // The server rewrites every entry's keys on each save, so after another
  // operator's edit the entry reads differently while saying the same thing.
  it("after a 409, Replace still finds its entry when only its spelling changed", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await entryRow("usbdev04", "pve-01")).getByRole("button", {
        name: /Replace device/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "6666:0006",
    );

    const respelled = baseMappings("d2");
    const target = respelled.find((m) => m.id === "usbdev04");
    if (!target) throw new Error("fixture");
    target.map = [
      "id=5555:0004,node=pve-02",
      "description=left port,id=5555:0004,node=pve-01",
    ];
    mockedPut.mockRejectedValueOnce(conflict());
    listing = respelled;
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    expect(
      await within(dialog).findByText(/This dialog now shows them as they are/),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByText(/This entry changed since it was loaded/),
    ).not.toBeInTheDocument();

    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenLastCalledWith(mappingURL("usbdev04"), {
        map: [
          "id=5555:0004,node=pve-02",
          "node=pve-01,id=6666:0006,description=left port",
        ],
        digest: "d2",
      });
    });
  });

  it("will not save any edit of a mapping with two entries for one node, and says why", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev03")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Description"), "x");
    expect(
      within(dialog).getByText(/usbdev03 has more than one entry for pve-01/),
    ).toBeInTheDocument();
    expect(within(dialog).getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("offers no action on a mapping whose id is too long to address, and says why", async () => {
    const long = "u".repeat(129);
    listing = [
      {
        id: long,
        description: "",
        digest: "d1",
        map: ["node=pve-01,id=1234:5678"],
        node_checks: { "pve-01": [] },
        unchecked: {},
      },
    ];
    renderCard();
    const row = await mappingRow(long);
    expect(
      within(row).getByText(/Nexara manages mapping names of up to 128/),
    ).toBeInTheDocument();
    expect(within(row).queryAllByRole("button")).toHaveLength(0);
    expect(
      within(await entryRow(long, "pve-01")).queryAllByRole("button"),
    ).toHaveLength(0);
  });

  it("shows an entry that names no node, and offers only its removal", async () => {
    listing = [
      {
        id: "usbdev07",
        description: "",
        digest: "d1",
        map: ["node=pve-01,id=1234:5678", "id=abcd:ef01"],
        node_checks: { "pve-01": [] },
        unchecked: {},
      },
    ];
    renderCard();
    const row = await entryRow("usbdev07", "—");
    expect(
      within(row).getByText("This entry names no node, so no VM can use it."),
    ).toBeInTheDocument();
    expect(
      within(row).queryByRole("button", { name: /Replace device/ }),
    ).not.toBeInTheDocument();
    expect(
      within(row).getByRole("button", { name: /Remove/ }),
    ).toBeInTheDocument();
  });

  it("says so for a mapping with no entries at all", async () => {
    listing = [
      {
        id: "usbdev08",
        description: "",
        digest: "d1",
        map: [],
        node_checks: {},
        unchecked: {},
      },
    ];
    renderCard();
    await mappingRow("usbdev08");
    expect(
      screen.getByText("No node entries: no VM can use this mapping anywhere."),
    ).toBeInTheDocument();
  });

  it("refuses a new mapping's description over 4096 characters", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      await screen.findByRole("button", { name: /New mapping/ }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(within(dialog).getByLabelText("Node"), "pve-02");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "1234:5678",
    );
    const description = within(dialog).getByLabelText("Description");
    fireEvent.change(description, { target: { value: "é".repeat(4096) } });
    expect(
      within(dialog).getByRole("button", { name: "Create mapping" }),
    ).toBeEnabled();
    fireEvent.change(description, { target: { value: "é".repeat(4097) } });
    expect(
      within(dialog).getByText("At most 4096 characters."),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Create mapping" }),
    ).toBeDisabled();
  });

  it("a delete refused with 409 says nothing was deleted and why", async () => {
    const user = userEvent.setup();
    mockedGet.mockResolvedValue({
      mapping_id: "usbdev01",
      checked: 5,
      users: [],
      unchecked: [],
    });
    mockedDelete.mockRejectedValueOnce(conflict());
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /^Delete$/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    const deleteButton = within(confirm).getByRole("button", {
      name: "Delete mapping",
    });
    await waitFor(() => {
      expect(deleteButton).toBeEnabled();
    });
    await user.click(deleteButton);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Nothing was deleted\..*a change to any USB mapping counts/,
    );
  });

  it("holds a mapping's actions while its delete is in flight", async () => {
    const user = userEvent.setup();
    mockedGet.mockResolvedValue({
      mapping_id: "usbdev01",
      checked: 5,
      users: [],
      unchecked: [],
    });
    const pending = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(pending.promise);
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /^Delete$/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    const deleteButton = within(confirm).getByRole("button", {
      name: "Delete mapping",
    });
    await waitFor(() => {
      expect(deleteButton).toBeEnabled();
    });
    await user.click(deleteButton);

    const row = await mappingRow("usbdev01");
    expect(
      await within(row).findByRole("button", { name: /Deleting…/ }),
    ).toBeDisabled();
    expect(
      within(row).getByRole("button", { name: /Add node/ }),
    ).toBeDisabled();
    // Another mapping's row is held too, and says why: the delete will
    // change the digest every row's next edit would pin.
    const other = await mappingRow("usbdev02");
    expect(
      within(other).getByRole("button", { name: /^Delete$/ }),
    ).toBeDisabled();
    expect(
      within(other).getByText("Reloading after a change…"),
    ).toBeInTheDocument();

    // Deleted, but the listing has not been read again: the row stays held,
    // so its Delete cannot be pressed a second time on the old listing.
    const reread = deferred<ClusterUSBMapping[]>();
    listGate = reread.promise;
    pending.resolve({ status: "ok" });
    await waitFor(() => {
      expect(
        mockedList.mock.calls.filter(([p]) => p === LIST_URL).length,
      ).toBeGreaterThan(1);
    });
    expect(
      within(row).getByRole("button", { name: /Deleting…/ }),
    ).toBeDisabled();

    listGate = null;
    reread.resolve(baseMappings().filter((m) => m.id !== "usbdev01"));
    await waitFor(() => {
      expect(screen.queryByText("usbdev01")).not.toBeInTheDocument();
    });
  });

  it("holds a mapping's row after a save until the listing has been read again", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Description"), "!");
    const reread = deferred<ClusterUSBMapping[]>();
    listGate = reread.promise;
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });
    // An edit opened now would pin the digest the save just changed.
    const row = await mappingRow("usbdev01");
    expect(
      within(row).getByRole("button", { name: /Edit description/ }),
    ).toBeDisabled();

    listGate = null;
    reread.resolve(baseMappings("d2"));
    await waitFor(() => {
      expect(
        within(row).getByRole("button", { name: /Edit description/ }),
      ).toBeEnabled();
    });
  });

  it("puts focus on the table after a confirmed Remove, not on the page", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await entryRow("usbdev01", "pve-02")).getByRole("button", {
        name: /Remove/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    await user.click(
      within(confirm).getByRole("button", { name: "Remove entry" }),
    );
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("region", { name: "USB mappings" }),
      );
    });
  });

  it("returns focus to the opener after a Cancel, even following a confirmed Remove", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await entryRow("usbdev01", "pve-02")).getByRole("button", {
        name: /Remove/,
      }),
    );
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Remove entry",
      }),
    );
    await waitFor(() => {
      expect(
        within(
          screen.getByRole("region", { name: "USB mappings" }),
        ).queryAllByText("Reloading after a change…"),
      ).toHaveLength(0);
    });

    const opener = within(await entryRow("usbdev04", "pve-02")).getByRole(
      "button",
      { name: /Remove/ },
    );
    await user.click(opener);
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Cancel",
      }),
    );
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(opener);
    });
  });

  it("keeps focus on the card when the last mapping is deleted", async () => {
    const user = userEvent.setup();
    listing = [
      {
        id: "usbdev02",
        description: "",
        digest: "d1",
        map: ["node=pve-01,id=1234:5678"],
        node_checks: { "pve-01": [] },
        unchecked: {},
      },
    ];
    mockedGet.mockResolvedValue({
      mapping_id: "usbdev02",
      checked: 1,
      users: [],
      unchecked: [],
    });
    renderCard();
    await user.click(
      within(await mappingRow("usbdev02")).getByRole("button", {
        name: /^Delete$/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    const deleteButton = within(confirm).getByRole("button", {
      name: "Delete mapping",
    });
    await waitFor(() => {
      expect(deleteButton).toBeEnabled();
    });
    listing = [];
    await user.click(deleteButton);
    expect(
      await screen.findByText(/This cluster has no USB mappings yet/),
    ).toBeInTheDocument();
    expect(document.activeElement).toBe(
      screen.getByRole("region", { name: "USB mappings" }),
    );
  });

  // The digest covers every USB mapping: after a write, every row's pinned
  // digest is out of date until the listing is read again.
  it("holds every mapping's row while a write is being read back, and says why", async () => {
    const user = userEvent.setup();
    renderCard();
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /Edit description/,
      }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.type(within(dialog).getByLabelText("Description"), "!");
    const reread = deferred<ClusterUSBMapping[]>();
    listGate = reread.promise;
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    });

    const other = await mappingRow("usbdev04");
    expect(
      within(other).getByRole("button", { name: /Edit description/ }),
    ).toBeDisabled();
    expect(
      within(other).getByText("Reloading after a change…"),
    ).toBeInTheDocument();
    expect(
      within(await mappingRow("usbdev01")).getByText("Saving…"),
    ).toBeInTheDocument();

    listGate = null;
    reread.resolve(baseMappings("d2"));
    await waitFor(() => {
      expect(
        within(other).getByRole("button", { name: /Edit description/ }),
      ).toBeEnabled();
    });
  });

  it("drops a usage answer from the cache once its dialog is closed", async () => {
    const user = userEvent.setup();
    mockedGet.mockResolvedValue({
      mapping_id: "usbdev01",
      checked: 5,
      users: [{ vmid: 101, name: "linux01", node: "pve-01", keys: ["usb0"] }],
      unchecked: [],
    });
    const queryClient = renderCard({ cached: true });
    await user.click(
      within(await mappingRow("usbdev01")).getByRole("button", {
        name: /^Delete$/,
      }),
    );
    const confirm = await screen.findByRole("alertdialog");
    expect(
      await within(confirm).findByText("101 (linux01) on pve-01 — usb0"),
    ).toBeInTheDocument();
    await user.click(within(confirm).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(
        queryClient.getQueryCache().findAll({
          queryKey: ["clusters", CLUSTER, "usb-mapping-usage", "usbdev01"],
        }),
      ).toHaveLength(0);
    });
  });
});
