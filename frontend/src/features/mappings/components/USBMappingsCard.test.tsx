import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  act,
  fireEvent,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { onlineManager, type QueryClient } from "@tanstack/react-query";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { deferred } from "@/test/fake-server";
import type { NodeUSBDevice } from "@/features/vms/api/vm-queries";
import type {
  ClusterUSBMapping,
  USBMappingUsage,
} from "../api/mapping-queries";
import { USBMappingsCard } from "./USBMappingsCard";
import {
  CLUSTER,
  conflict,
  mappingRow,
  node,
  renderCard,
  setPermissions,
  usbDevice,
} from "./mappings-test-kit";

// The transport is mocked, not the hooks, so the real queries and mutations
// run and each test asserts the request that would leave the browser.
vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedGet = vi.mocked(apiClient.get);
const mockedList = vi.mocked(apiClient.list);
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);
const mockedDelete = vi.mocked(apiClient.delete);

const LIST_URL = `/api/v1/clusters/${CLUSTER}/usb-mappings`;
const NODES_URL = `/api/v1/clusters/${CLUSTER}/nodes`;
const mappingURL = (id: string) => `${LIST_URL}/${id}`;
const devicesURL = (nodeName: string) =>
  `/api/v1/clusters/${CLUSTER}/nodes/${nodeName}/hardware/usb`;
const USAGE_KEY = ["clusters", CLUSTER, "usb-mapping-usage"];

function usbMapping(over: Partial<ClusterUSBMapping>): ClusterUSBMapping {
  return {
    id: "",
    description: "",
    digest: "d1",
    map: [],
    node_checks: {},
    unchecked: {},
    ...over,
  };
}

// usbdev01 has three entries: clean on pve-01, a Proxmox error on pve-02,
// unchecked on pve-03. usbdev02 has one entry, so removing it deletes the
// mapping. usbdev03 has two entries for pve-01, which qemu-server refuses.
// usbdev04's entry carries a description of its own.
function baseMappings(digest = "d1"): ClusterUSBMapping[] {
  return [
    usbMapping({
      id: "usbdev02",
      digest,
      map: ["node=pve-01,id=1234:5678"],
      node_checks: { "pve-01": [] },
    }),
    usbMapping({
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
    }),
    usbMapping({
      id: "usbdev03",
      digest,
      // The same entry twice, as Proxmox's API will store it.
      map: [
        "node=pve-01,id=9999:0001",
        "node=pve-01,id=9999:0001",
        "node=pve-02,id=9999:0001",
      ],
      node_checks: { "pve-01": [], "pve-02": [] },
    }),
    usbMapping({
      id: "usbdev04",
      digest,
      map: [
        "node=pve-01,id=5555:0004,description=left port",
        "node=pve-02,id=5555:0004",
      ],
      node_checks: { "pve-01": [], "pve-02": [] },
    }),
  ];
}

/** The base listing as another operator left it: `id` with these entries. */
function listingWith(digest: string, id: string, map: string[]) {
  return baseMappings(digest).map((m) => (m.id === id ? { ...m, map } : m));
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

type User = ReturnType<typeof userEvent.setup>;

/** Presses the button of that name in a dialog. */
const clickIn = (user: User, el: HTMLElement, name: string) =>
  user.click(within(el).getByRole("button", { name }));

const card = () => <USBMappingsCard clusterId={CLUSTER} />;
const region = () => screen.getByRole("region", { name: "USB mappings" });
const listReads = () =>
  mockedList.mock.calls.filter(([p]) => p === LIST_URL).length;
const usage = (over: Partial<USBMappingUsage> = {}): USBMappingUsage => ({
  mapping_id: "usbdev01",
  checked: 5,
  users: [],
  unchecked: [],
  ...over,
});
const optionsOf = (select: HTMLElement) =>
  within(select)
    .getAllByRole("option")
    .map((o) => o.textContent);

/** The entry row for `nodeName` within mapping `id`: the rows after its header. */
async function entryRow(id: string, nodeName: string, nth = 0) {
  const header = await mappingRow(id);
  const rows: HTMLElement[] = [];
  let next = header.nextElementSibling;
  while (
    next instanceof HTMLElement &&
    !next.classList.contains("bg-muted/30")
  ) {
    if (next.querySelector("td")?.textContent === nodeName) rows.push(next);
    next = next.nextElementSibling;
  }
  const row = rows[nth];
  if (!row)
    throw new Error(`no entry row ${String(nth)} for ${nodeName} in ${id}`);
  return row;
}

async function openIn(
  user: User,
  row: Promise<HTMLElement>,
  name: RegExp,
  role: "dialog" | "alertdialog",
) {
  await user.click(within(await row).getByRole("button", { name }));
  return screen.findByRole(role);
}
const openEdit = (user: User, id: string) =>
  openIn(user, mappingRow(id), /Edit description/, "dialog");
const openAddNode = (user: User, id: string) =>
  openIn(user, mappingRow(id), /Add node/, "dialog");
const openDelete = (user: User, id: string) =>
  openIn(user, mappingRow(id), /^Delete$/, "alertdialog");
const openReplace = (user: User, id: string, nodeName: string) =>
  openIn(user, entryRow(id, nodeName), /Replace device/, "dialog");
const openRemove = (user: User, id: string, nodeName: string, nth = 0) =>
  openIn(user, entryRow(id, nodeName, nth), /Remove/, "alertdialog");

/** Edit description of usbdev01, with something typed so that Save is on. */
async function dirtyDescription(user: User) {
  const dialog = await openEdit(user, "usbdev01");
  await user.type(within(dialog).getByLabelText("Description"), "!");
  return dialog;
}

const save = (dialog: HTMLElement) =>
  within(dialog).getByRole("button", { name: "Save" });
const closed = (role: "dialog" | "alertdialog" = "dialog") =>
  waitFor(() => {
    expect(screen.queryByRole(role)).not.toBeInTheDocument();
  });
const focusOn = (el: () => HTMLElement) =>
  waitFor(() => {
    expect(document.activeElement).toBe(el());
  });
const expectPut = (id: string, body: unknown) =>
  waitFor(() => {
    expect(mockedPut).toHaveBeenCalledWith(mappingURL(id), body);
  });

/** The Delete mapping button of a confirmation, once its usage check answered. */
async function readyToDelete(confirm: HTMLElement) {
  const button = within(confirm).getByRole("button", {
    name: "Delete mapping",
  });
  await waitFor(() => {
    expect(button).toBeEnabled();
  });
  return button;
}

/** Another tab's write lands while a dialog is open: the list is read again. */
async function refetchWith(queryClient: QueryClient, digest: string) {
  const reads = listReads();
  listing = baseMappings(digest);
  await queryClient.invalidateQueries({
    queryKey: ["clusters", CLUSTER, "usb-mappings"],
  });
  await waitFor(() => {
    expect(listReads()).toBeGreaterThan(reads);
  });
}

describe("USBMappingsCard listing", () => {
  it("shows each entry's check on its own node, never an unchecked node as OK, and its descriptions", async () => {
    // usbdev05 names a node that is in neither the checks nor the unchecked.
    listing = [
      ...baseMappings(),
      usbMapping({ id: "usbdev05", map: ["node=pve-01,id=1234:5678"] }),
    ];
    renderCard(card());

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

    const unlisted = await entryRow("usbdev05", "pve-01");
    expect(within(unlisted).getByText("Not checked.")).toBeInTheDocument();
    expect(within(unlisted).queryByText("OK")).not.toBeInTheDocument();

    // Two entries for one node, which qemu-server refuses: a clean check does
    // not read as OK next to that.
    const doubled = await entryRow("usbdev03", "pve-01", 0);
    expect(
      within(doubled).getByText(/pve-01 has 2 entries: Proxmox refuses/),
    ).toBeInTheDocument();
    expect(within(doubled).queryByText("OK")).not.toBeInTheDocument();

    expect(screen.getByText("Example Radio")).toBeInTheDocument();
    expect(
      within(await entryRow("usbdev04", "pve-01")).getByText("left port"),
    ).toBeInTheDocument();
  });

  it("says what is odd about a mapping: an id too long to address, an entry naming no node, no entries", async () => {
    const long = "u".repeat(129);
    listing = [
      usbMapping({
        id: long,
        map: ["node=pve-01,id=1234:5678"],
        node_checks: { "pve-01": [] },
      }),
      usbMapping({
        id: "usbdev07",
        map: ["node=pve-01,id=1234:5678", "id=abcd:ef01"],
        node_checks: { "pve-01": [] },
      }),
      usbMapping({ id: "usbdev08" }),
    ];
    renderCard(card());

    const longRow = await mappingRow(long);
    expect(
      within(longRow).getByText(/Nexara manages mapping names of up to 128/),
    ).toBeInTheDocument();
    expect(within(longRow).queryAllByRole("button")).toHaveLength(0);
    expect(
      within(await entryRow(long, "pve-01")).queryAllByRole("button"),
    ).toHaveLength(0);

    const noNode = await entryRow("usbdev07", "—");
    expect(
      within(noNode).getByText(
        "This entry names no node, so no VM can use it.",
      ),
    ).toBeInTheDocument();
    expect(
      within(noNode).queryByRole("button", { name: /Replace device/ }),
    ).not.toBeInTheDocument();
    expect(
      within(noNode).getByRole("button", { name: /Remove/ }),
    ).toBeInTheDocument();

    expect(
      screen.getByText("No node entries: no VM can use this mapping anywhere."),
    ).toBeInTheDocument();
  });

  it("reports a failed listing rather than an empty one", async () => {
    listFails = true;
    renderCard(card());
    expect(
      await screen.findByText("Could not load the USB mappings."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/no USB mappings yet/)).not.toBeInTheDocument();
  });

  it("says so when the cluster has none", async () => {
    listing = [];
    renderCard(card());
    expect(
      await screen.findByText(/This cluster has no USB mappings yet/),
    ).toBeInTheDocument();
  });

  it("is read-only without manage:cluster, and says why", async () => {
    setPermissions(["view:cluster"]);
    renderCard(card());
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
    renderCard(card());
    const dialog = await openAddNode(user, "usbdev02");
    // Nodes that already have an entry are not offered.
    expect(optionsOf(within(dialog).getByLabelText("Node"))).toEqual([
      "Select a node...",
      "pve-02",
      "pve-03",
      "pve-04",
    ]);

    await user.selectOptions(within(dialog).getByLabelText("Node"), "pve-02");
    // The device the other entries pass is picked for this node too.
    await waitFor(() => {
      expect(within(dialog).getByLabelText("Device")).toHaveValue("1234:5678");
    });
    await user.click(save(dialog));

    await expectPut("usbdev02", {
      map: ["node=pve-01,id=1234:5678", "node=pve-02,id=1234:5678"],
      digest: "d1",
    });
    await closed();
  });

  // The cluster listing checks every node, which can take a while: the dialog
  // closes once the write is done, and the digest covers every USB mapping, so
  // every row is held — an edit opened now would pin a stale digest — until
  // the listing has been read again.
  it("a saved edit closes at once, holds every row with the reason until the listing is read again, and puts focus on the card", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openEdit(user, "usbdev01");
    const input = within(dialog).getByLabelText("Description");
    await user.clear(input);
    await user.type(input, "Example Radio, desk");
    const reads = listReads();
    const reread = deferred<ClusterUSBMapping[]>();
    listGate = reread.promise;

    await user.click(save(dialog));
    await closed();

    expect(listReads()).toBe(reads + 1);
    const row = await mappingRow("usbdev01");
    expect(within(row).getByText("Saving…")).toBeInTheDocument();
    expect(
      within(row).getByRole("button", { name: /Edit description/ }),
    ).toBeDisabled();
    const other = await mappingRow("usbdev04");
    expect(
      within(other).getByRole("button", { name: /Edit description/ }),
    ).toBeDisabled();
    expect(
      within(other).getByText("Reloading after a change…"),
    ).toBeInTheDocument();
    // The button that opened the dialog is held, so focus goes to the card.
    await focusOn(region);

    listGate = null;
    reread.resolve(
      baseMappings("d2").map((m) =>
        m.id === "usbdev01" ? { ...m, description: "Example Radio, desk" } : m,
      ),
    );
    expect(await screen.findByText("Example Radio, desk")).toBeInTheDocument();
    await waitFor(() => {
      expect(
        within(region())
          .getAllByRole("button", { name: /Edit description/ })
          .every((b) => !(b as HTMLButtonElement).disabled),
      ).toBe(true);
    });

    // A cancelled edit goes back to its own button, not to the card.
    const opener = within(await mappingRow("usbdev04")).getByRole("button", {
      name: /Edit description/,
    });
    await user.click(opener);
    await clickIn(user, await screen.findByRole("dialog"), "Cancel");
    await closed();
    await focusOn(() => opener);
  });

  it.each([
    {
      sent: "the entries unchanged and the new description",
      typed: "Example Radio, desk",
      description: "Example Radio, desk",
    },
    // Proxmox drops the white space around a description, so none is written;
    // emptied or blank, it is sent empty, which removes it.
    { sent: "a blank description as empty", typed: "   ", description: "" },
  ])("Edit description sends $sent", async ({ typed, description }) => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openEdit(user, "usbdev01");
    const input = within(dialog).getByLabelText("Description");
    expect(input).toHaveValue("Example Radio");
    // Only the padding differs from what is stored: nothing to save.
    await user.type(input, "  ");
    expect(save(dialog)).toBeDisabled();
    await user.clear(input);
    await user.type(input, typed);
    await user.click(save(dialog));

    await expectPut("usbdev01", {
      map: [
        "node=pve-01,id=abcd:ef01",
        "node=pve-02,id=abcd:ef01,path=1-3",
        "node=pve-03,id=abcd:ef01",
      ],
      description,
      digest: "d1",
    });
  });

  it("Replace device keeps the node, the other entries and the entry's own description", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openReplace(user, "usbdev04", "pve-01");
    await user.click(
      within(dialog).getByRole("radio", { name: /Host USB port/ }),
    );
    // Each port is offered with the device on it, and the port itself.
    await waitFor(() => {
      expect(optionsOf(within(dialog).getByLabelText("Port"))).toEqual([
        "Select a port...",
        "Example Receiver (1-7)",
        "Example Key (1-8)",
      ]);
    });
    await user.selectOptions(within(dialog).getByLabelText("Port"), "1-8");
    await user.click(save(dialog));

    await expectPut("usbdev04", {
      map: [
        "node=pve-01,id=6666:0006,path=1-8,description=left port",
        "node=pve-02,id=5555:0004",
      ],
      digest: "d1",
    });
  });

  it("Replace device takes a typed id when the node's devices cannot be listed", async () => {
    const user = userEvent.setup();
    renderCard(card());
    // pve-03 is offline: its device listing fails.
    const dialog = await openReplace(user, "usbdev01", "pve-03");
    expect(
      await within(dialog).findByText(/Could not list the node's USB devices/),
    ).toBeInTheDocument();
    const typed = within(dialog).getByLabelText("Device");
    await user.type(typed, " ABCD:0001 ");
    expect(typed).toHaveValue("ABCD:0001");
    await user.click(save(dialog));

    await expectPut("usbdev01", {
      map: [
        "node=pve-01,id=abcd:ef01",
        "node=pve-02,id=abcd:ef01,path=1-3",
        "node=pve-03,id=abcd:0001",
      ],
      digest: "d1",
    });
  });

  it("Replace device will not save the device the entry already passes", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openReplace(user, "usbdev04", "pve-01");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "5555:0004",
    );
    expect(save(dialog)).toBeDisabled();
    expect(
      within(dialog).getByText("That is the device the entry passes now."),
    ).toBeInTheDocument();
  });

  // reference_cas_token_pin_class: the digest comes from the read the dialog
  // was opened from. A refetch landing while it is open — a WebSocket
  // reconnect, another tab's invalidation — must not swap it under values
  // that never moved with it.
  it("a refetch while the dialog is open does not move the pinned digest", async () => {
    const user = userEvent.setup();
    const queryClient = renderCard(card());
    const dialog = await openEdit(user, "usbdev01");
    await refetchWith(queryClient, "d2");

    await user.type(within(dialog).getByLabelText("Description"), "!");
    await user.click(save(dialog));

    await expectPut("usbdev01", expect.objectContaining({ digest: "d1" }));
  });

  it("a 409 re-reads the mappings, shows them, and pins the new read", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openAddNode(user, "usbdev02");
    await user.selectOptions(within(dialog).getByLabelText("Node"), "pve-02");
    await waitFor(() => {
      expect(within(dialog).getByLabelText("Device")).toHaveValue("1234:5678");
    });

    // Meanwhile someone gave usbdev02 an entry for pve-04.
    mockedPut.mockRejectedValueOnce(conflict("USB"));
    listing = listingWith("d2", "usbdev02", [
      "node=pve-01,id=1234:5678",
      "node=pve-04,id=1234:5678",
    ]);
    const reads = listReads();
    await user.click(save(dialog));
    expect(
      await within(dialog).findByText(/This dialog now shows them as they are/),
    ).toBeInTheDocument();
    // One read after the conflict: the dialog joins the one the save started
    // instead of cancelling it for a second.
    expect(listReads()).toBe(reads + 1);
    expect(
      within(dialog).getByText(/a change to any USB mapping counts/),
    ).toBeInTheDocument();
    expect(
      within(dialog).getByText(/^Nothing was saved\./),
    ).toBeInTheDocument();
    // pve-04 has an entry now, so it is no longer offered.
    expect(optionsOf(within(dialog).getByLabelText("Node"))).toEqual([
      "Select a node...",
      "pve-02",
      "pve-03",
    ]);

    await user.click(save(dialog));
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
    renderCard(card());
    const dialog = await dirtyDescription(user);
    const reread = deferred<ClusterUSBMapping[]>();
    listGate = reread.promise;
    mockedPut.mockRejectedValueOnce(conflict("USB"));

    await user.click(save(dialog));
    expect(
      await within(dialog).findByText(/Reloading them…/),
    ).toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
    // Closing is not held: only the write itself locks the dialog.
    expect(
      within(dialog).getByRole("button", { name: "Cancel" }),
    ).toBeEnabled();

    listGate = null;
    reread.resolve(baseMappings("d2"));
    expect(
      await within(dialog).findByText(/This dialog now shows them as they are/),
    ).toBeInTheDocument();
    expect(save(dialog)).toBeEnabled();
  });

  it("a 409 whose re-read fails keeps the pin, and says so", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await dirtyDescription(user);
    mockedPut.mockRejectedValueOnce(conflict("USB"));
    listing = baseMappings("d2");
    listFails = true;

    await user.click(save(dialog));
    expect(
      await within(dialog).findByText(/Reloading them failed/),
    ).toBeInTheDocument();

    listFails = false;
    await user.click(save(dialog));
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
    renderCard(card());
    const dialog = await dirtyDescription(user);
    mockedPut.mockRejectedValueOnce(conflict("USB"));
    listing = baseMappings("d2").filter((m) => m.id !== "usbdev01");

    await user.click(save(dialog));
    expect(
      await within(dialog).findByText(/The mapping usbdev01 no longer exists/),
    ).toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
  });

  it("a failure that is not a 409 shows the server's words and moves no pin", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await dirtyDescription(user);
    mockedPut.mockRejectedValueOnce(
      new ApiClientError(400, {
        error: "bad_request",
        message: 'USB mapping entry "node=pve-03" needs id=<vendor:product>',
      }),
    );
    listing = baseMappings("d2");

    await user.click(save(dialog));
    expect(
      await within(dialog).findByText(
        'USB mapping entry "node=pve-03" needs id=<vendor:product>',
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByText(/a change to any USB mapping counts/),
    ).not.toBeInTheDocument();

    await user.click(save(dialog));
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
  // The confirmation closes onto the card, not the page: the Remove button
  // that opened it is held. A later Cancel goes back to its own opener.
  it("Remove asks first, then sends the list without the entry with the digest it was opened from, and puts focus on the card", async () => {
    const user = userEvent.setup();
    const queryClient = renderCard(card());
    const confirm = await openRemove(user, "usbdev01", "pve-02");
    expect(
      within(confirm).getByText("Remove pve-02's entry from usbdev01?"),
    ).toBeInTheDocument();
    expect(mockedPut).not.toHaveBeenCalled();
    await refetchWith(queryClient, "d2");

    await clickIn(user, confirm, "Remove entry");

    await expectPut("usbdev01", {
      map: ["node=pve-01,id=abcd:ef01", "node=pve-03,id=abcd:ef01"],
      digest: "d1",
    });
    await closed("alertdialog");
    await focusOn(region);

    await waitFor(() => {
      expect(
        within(region()).queryAllByText("Reloading after a change…"),
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
    await closed("alertdialog");
    await focusOn(() => opener);
  });

  it("each opening of Delete checks the usage again, and a closed dialog leaves its answer out of the cache", async () => {
    const user = userEvent.setup();
    mockedGet.mockResolvedValueOnce(usage());
    // With the app's own cache times, which would otherwise serve the first
    // answer again for five minutes.
    const queryClient = renderCard(card(), { cached: true });
    let confirm = await openDelete(user, "usbdev01");
    expect(
      await within(confirm).findByText(
        "No VM's current configuration uses it.",
      ),
    ).toBeInTheDocument();
    await clickIn(user, confirm, "Cancel");
    await closed("alertdialog");
    await waitFor(() => {
      expect(
        queryClient
          .getQueryCache()
          .findAll({ queryKey: [...USAGE_KEY, "usbdev01"] }),
      ).toHaveLength(0);
    });

    // A VM started using it since.
    mockedGet.mockResolvedValueOnce(
      usage({
        users: [{ vmid: 101, name: "linux01", node: "pve-01", keys: ["usb0"] }],
      }),
    );
    confirm = await openDelete(user, "usbdev01");
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
    mockedGet.mockResolvedValue(usage());
    renderCard(card(), { cached: true });
    const confirm = await openDelete(user, "usbdev01");
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
    mockedGet.mockResolvedValueOnce(usage());
    const queryClient = renderCard(card());
    const confirm = await openDelete(user, "usbdev01");
    const deleteButton = await readyToDelete(confirm);

    const recheck = deferred<USBMappingUsage>();
    mockedGet.mockReturnValueOnce(recheck.promise);
    void queryClient.invalidateQueries({ queryKey: USAGE_KEY });
    await waitFor(() => {
      expect(deleteButton).toBeDisabled();
    });
    expect(
      within(confirm).getByText(/Checking which VMs use it/),
    ).toBeInTheDocument();
    recheck.resolve(usage());
    await waitFor(() => {
      expect(deleteButton).toBeEnabled();
    });
  });

  it("Remove on one of two entries for a node removes that one", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const confirm = await openRemove(user, "usbdev03", "pve-01", 1);
    await clickIn(user, confirm, "Remove entry");
    // One of the two identical entries goes, not both.
    await expectPut("usbdev03", {
      map: ["node=pve-01,id=9999:0001", "node=pve-02,id=9999:0001"],
      digest: "d1",
    });
  });

  it("a 409 on Remove says nothing was removed and why", async () => {
    const user = userEvent.setup();
    renderCard(card());
    mockedPut.mockRejectedValueOnce(conflict("USB"));
    const confirm = await openRemove(user, "usbdev01", "pve-02");
    await clickIn(user, confirm, "Remove entry");
    expect(
      await screen.findByText(/Nothing was removed from usbdev01\./),
    ).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent(
      /a change to any USB mapping counts/,
    );
  });

  // Focus stays on the card: its content, not the table, which the delete of
  // the last mapping takes away.
  it("removing the last entry deletes the mapping instead, after the usage check, and keeps focus on the card", async () => {
    const user = userEvent.setup();
    listing = baseMappings().filter((m) => m.id === "usbdev02");
    mockedGet.mockResolvedValue(usage({ mapping_id: "usbdev02", checked: 3 }));
    renderCard(card());
    const confirm = await openRemove(user, "usbdev02", "pve-01");
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
    listing = [];

    await user.click(await readyToDelete(confirm));

    await waitFor(() => {
      expect(mockedDelete).toHaveBeenCalledWith(
        `${mappingURL("usbdev02")}?digest=d1`,
      );
    });
    // Never an update that empties the list.
    expect(mockedPut).not.toHaveBeenCalled();
    expect(
      await screen.findByText(/This cluster has no USB mappings yet/),
    ).toBeInTheDocument();
    expect(document.activeElement).toBe(region());
  });

  it("Delete lists the VMs that use the mapping, and is held until the check answers", async () => {
    const user = userEvent.setup();
    const pending = deferred<USBMappingUsage>();
    mockedGet.mockReturnValue(pending.promise);
    renderCard(card());
    const confirm = await openDelete(user, "usbdev01");
    expect(mockedGet).toHaveBeenCalledWith(`${mappingURL("usbdev01")}/usage`);
    const deleteButton = within(confirm).getByRole("button", {
      name: "Delete mapping",
    });
    expect(deleteButton).toBeDisabled();
    expect(
      within(confirm).getByText(/Checking which VMs use it/),
    ).toBeInTheDocument();

    pending.resolve(
      usage({
        users: [
          {
            vmid: 101,
            name: "linux01",
            node: "pve-01",
            keys: ["usb0", "usb2"],
          },
        ],
        unchecked: [
          {
            vmid: 104,
            name: "win04",
            node: "pve-03",
            reason: "The node is offline.",
          },
        ],
      }),
    );
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

    await user.click(await readyToDelete(confirm));
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
    renderCard(card());
    const confirm = await openDelete(user, "usbdev01");
    expect(
      await within(confirm).findByText(
        /Could not check which VMs use it: Permission denied/,
      ),
    ).toBeInTheDocument();
    await readyToDelete(confirm);
  });

  it("a delete refused with 409 says nothing was deleted and why", async () => {
    const user = userEvent.setup();
    mockedGet.mockResolvedValue(usage());
    mockedDelete.mockRejectedValueOnce(conflict("USB"));
    renderCard(card());
    const confirm = await openDelete(user, "usbdev01");

    await user.click(await readyToDelete(confirm));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      /Nothing was deleted\..*a change to any USB mapping counts/,
    );
  });

  it("holds a mapping's actions while its delete is in flight", async () => {
    const user = userEvent.setup();
    mockedGet.mockResolvedValue(usage());
    const pending = deferred<unknown>();
    mockedDelete.mockReturnValueOnce(pending.promise);
    renderCard(card());
    const confirm = await openDelete(user, "usbdev01");
    await user.click(await readyToDelete(confirm));

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
      expect(listReads()).toBeGreaterThan(1);
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
});

describe("USBMappingsCard new mapping", () => {
  // Focus goes back to New mapping when the dialog closes — and to the card
  // when the click did not focus the button (Safari's, say), leaving no
  // opener to go back to.
  it("creates a mapping with one entry for the picked device, and returns focus to New mapping", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const opener = await screen.findByRole("button", { name: /New mapping/ });
    await user.click(opener);
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
    await closed();
    await focusOn(() => opener);

    act(() => {
      opener.blur();
    });
    fireEvent.click(opener);
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "Cancel",
      }),
    );
    await closed();
    await focusOn(region);
  });

  it("refuses a name that is taken, and a description over 4096 characters", async () => {
    const user = userEvent.setup();
    renderCard(card());
    await user.click(
      await screen.findByRole("button", { name: /New mapping/ }),
    );
    const dialog = await screen.findByRole("dialog");
    await user.selectOptions(within(dialog).getByLabelText("Node"), "pve-02");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "1234:5678",
    );
    const create = within(dialog).getByRole("button", {
      name: "Create mapping",
    });
    const name = within(dialog).getByLabelText("Name");
    await user.clear(name);
    await user.type(name, "usbdev01");
    expect(
      within(dialog).getByText('A mapping named "usbdev01" already exists.'),
    ).toBeInTheDocument();
    expect(create).toBeDisabled();

    await user.clear(name);
    await user.type(name, "example-key");
    const description = within(dialog).getByLabelText("Description");
    fireEvent.change(description, { target: { value: "é".repeat(4096) } });
    expect(create).toBeEnabled();
    fireEvent.change(description, { target: { value: "é".repeat(4097) } });
    expect(
      within(dialog).getByText("At most 4096 characters."),
    ).toBeInTheDocument();
    expect(create).toBeDisabled();
  });
});

describe("USBMappingsCard edge cases", () => {
  it("Replace repairs an entry stored with an uppercase id", async () => {
    const user = userEvent.setup();
    listing = [
      usbMapping({
        id: "usbdev06",
        map: ["node=pve-02,id=ABCD:EF01"],
        node_checks: { "pve-02": [] },
      }),
    ];
    renderCard(card());
    const dialog = await openReplace(user, "usbdev06", "pve-02");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "abcd:ef01",
    );
    // The same device, but the stored id is broken: Proxmox compares it
    // against the node's lowercase hex. Saving it writes it lowercase.
    expect(
      within(dialog).queryByText("That is the device the entry passes now."),
    ).not.toBeInTheDocument();
    await user.click(save(dialog));
    await expectPut("usbdev06", {
      map: ["node=pve-02,id=abcd:ef01"],
      digest: "d1",
    });
  });

  // The server rewrites every entry's keys on each save, so after another
  // operator's edit the entry reads differently while saying the same thing —
  // or says something else, and the entry the dialog was opened for is gone,
  // with the row button that opened it.
  it.each([
    {
      what: "finds its entry when only its spelling changed",
      map: [
        "id=5555:0004,node=pve-02",
        "description=left port,id=5555:0004,node=pve-01",
      ],
      then: async (user: User, dialog: HTMLElement) => {
        expect(
          within(dialog).queryByText(/This entry changed since it was loaded/),
        ).not.toBeInTheDocument();
        await user.click(save(dialog));
        await waitFor(() => {
          expect(mockedPut).toHaveBeenLastCalledWith(mappingURL("usbdev04"), {
            map: [
              "id=5555:0004,node=pve-02",
              "node=pve-01,id=6666:0006,description=left port",
            ],
            digest: "d2",
          });
        });
      },
    },
    {
      what: "says the entry changed when its device did, will not save, and puts focus on the card",
      map: ["node=pve-01,id=7777:0007", "node=pve-02,id=5555:0004"],
      then: async (user: User, dialog: HTMLElement) => {
        expect(
          within(dialog).getByText(/This entry changed since it was loaded/),
        ).toBeInTheDocument();
        expect(save(dialog)).toBeDisabled();
        await clickIn(user, dialog, "Cancel");
        await closed();
        await focusOn(region);
      },
    },
  ])("after a 409, Replace $what", async ({ map, then }) => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openReplace(user, "usbdev04", "pve-01");
    await user.selectOptions(
      await within(dialog).findByLabelText("Device"),
      "6666:0006",
    );
    mockedPut.mockRejectedValueOnce(conflict("USB"));
    listing = listingWith("d2", "usbdev04", map);

    await user.click(save(dialog));
    expect(
      await within(dialog).findByText(/This dialog now shows them as they are/),
    ).toBeInTheDocument();
    await then(user, dialog);
  });

  it("will not save any edit of a mapping with two entries for one node, and says why", async () => {
    const user = userEvent.setup();
    renderCard(card());
    const dialog = await openEdit(user, "usbdev03");
    await user.type(within(dialog).getByLabelText("Description"), "x");
    expect(
      within(dialog).getByText(/usbdev03 has more than one entry for pve-01/),
    ).toBeInTheDocument();
    expect(save(dialog)).toBeDisabled();
  });
});
