import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { ProtectedRoute } from "@/components/auth/ProtectedRoute";
import { clearTokens } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { queryClient } from "@/lib/query-client";
import {
  ADMIN,
  authResponse,
  deferred,
  flush,
  installFakeServer,
  json,
  VIEWER,
  type FakeServer,
} from "@/test/fake-server";
import { emptyPerSessionStores } from "@/test/per-session-stores";
import { renderWithProviders } from "@/test/test-utils";
import { makeStorage } from "@/test/storage.fixtures";
import type { User } from "@/types/api";
import { useAuthStore } from "@/stores/auth-store";
import type { CreateVMRequest } from "../types/vm";
import { CreateVMDialog } from "./CreateVMDialog";

/**
 * Once the task that creates a VM has finished, the dialog looks for the new VM
 * and navigates to its page. That look is a request, and what it finds is
 * acted on whenever it is answered — the dialog is gone with a sign-out, the
 * request is not. A navigation made then goes through whichever session is
 * current, to a page that belongs to the one that ended.
 */

const CLUSTER = "cluster01";
const VM_PAGE = `/inventory/vm/${CLUSTER}/guest-01`;
const UPID =
  "UPID:pve-01:00000001:00000002:00000003:qmcreate:101:admin@example.com:";
const LIST = `GET /api/v1/clusters/${CLUSTER}/vms`;
const CREATE = `POST /api/v1/clusters/${CLUSTER}/vms`;
const TASK = `GET ${apiPath`/api/v1/clusters/${CLUSTER}/tasks/${UPID}`}`;
const STORAGE = `GET /api/v1/clusters/${CLUSTER}/storage`;
// A thin LVM store of pve-01's own (node-01), as storage_pools holds it.
const THIN = makeStorage({
  storage: "store01",
  type: "lvmthin",
  content: "images,rootdir",
  node_id: "node-01",
});

const VM = {
  id: "guest-01",
  cluster_id: CLUSTER,
  node_id: "node-01",
  vmid: 101,
  name: "linux01",
  type: "qemu",
  status: "stopped",
  cpu_count: 2,
  mem_total: 2147483648,
};

let server: FakeServer;
/** What the VM list answers: without the VM, held back, or with it. */
let listPhase: "without" | "held" | "with";
let heldLists: ReturnType<typeof deferred<Response>>[];
let onOpenChange: ReturnType<typeof vi.fn<(open: boolean) => void>>;
/** Every path the router has been at, in order. */
let visited: string[];

async function signInAs(user: User) {
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(user));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

/** Walks the wizard to the end, presses Create VM and waits for the poll to begin. */
async function createsTheVM() {
  const router = createMemoryRouter(
    [
      { path: "/login", element: <p>login page</p> },
      {
        element: <ProtectedRoute />,
        children: [
          {
            path: "/",
            element: (
              <CreateVMDialog
                open
                onOpenChange={onOpenChange}
                clusterId={CLUSTER}
              />
            ),
          },
          { path: "/inventory/vm/:clusterId/:vmId", element: <p>vm page</p> },
        ],
      },
    ],
    { initialEntries: ["/"] },
  );
  router.subscribe((state) => {
    visited.push(state.location.pathname);
  });
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );

  const user = userEvent.setup();
  await screen.findByRole("option", { name: /pve-01/ });
  await user.selectOptions(screen.getByLabelText("Node"), ["pve-01"]);
  // The dialog offers the next free VMID and refills the field when it is
  // emptied, so this one is set in a single change: it is a guest of its own.
  fireEvent.change(screen.getByRole("spinbutton"), {
    target: { value: "101" },
  });
  for (let step = 0; step < 8; step++) {
    await user.click(screen.getByRole("button", { name: "Next" }));
  }
  listPhase = "held"; // from here on, every look at the VM list is held back
  await user.click(screen.getByRole("button", { name: "Create VM" }));
  await waitFor(() => {
    expect(server.times(TASK)).toBeGreaterThan(0);
  });
  await waitFor(() => {
    expect(heldLists.length).toBeGreaterThan(0);
  });
  return router;
}

/** The server answers every look at the list that was held back. */
async function theListAnswers() {
  listPhase = "with";
  for (const held of heldLists) held.resolve(json({ items: [VM], total: 1 }));
  await flush();
  await flush();
}

beforeEach(() => {
  localStorage.clear();
  clearTokens();
  queryClient.clear();
  emptyPerSessionStores();
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
    isLoading: false,
    isInitialized: true,
    totpPending: false,
    totpPendingToken: null,
    isLoggingOut: false,
    signedOutByUser: false,
  });
  server = installFakeServer();
  server.routes["POST /api/v1/auth/logout"] = () =>
    new Response(null, { status: 204 });
  server.routes["POST /api/v1/auth/refresh"] = () => json({}, 401);
  listPhase = "without";
  heldLists = [];
  visited = [];
  onOpenChange = vi.fn<(open: boolean) => void>();
  server.routes[`GET /api/v1/clusters/${CLUSTER}/nodes`] = () =>
    json({
      items: [
        {
          id: "node-01",
          cluster_id: CLUSTER,
          name: "pve-01",
          status: "online",
          cpu_count: 8,
          mem_total: 8589934592,
        },
      ],
      total: 1,
    });
  server.routes[LIST] = () => {
    if (listPhase === "held") {
      const held = deferred<Response>();
      heldLists.push(held);
      return held.promise;
    }
    return listPhase === "with"
      ? json({ items: [VM], total: 1 })
      : json({ items: [], total: 0 });
  };
  server.routes[CREATE] = () => json({ upid: UPID });
  server.routes[TASK] = () =>
    json({ upid: UPID, status: "stopped", exit_status: "OK" });
  server.routes[STORAGE] = () => json({ items: [THIN], total: 1 });
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
  emptyPerSessionStores();
});

describe("CreateVMDialog, looking for the VM it created", () => {
  it("does not navigate to the VM when the session that created it has ended", async () => {
    await signInAs(ADMIN);
    await createsTheVM();

    // The session ends while the look at the list is out; the dialog goes with it.
    await act(async () => {
      await useAuthStore.getState().logout();
    });
    await signInAs(VIEWER);
    await theListAnswers();

    expect(visited.filter((path) => path.startsWith("/inventory"))).toEqual([]);
    expect(onOpenChange).not.toHaveBeenCalled();
  });

  // A search left over from the last create acted on the next one: it wiped
  // the wizard being filled in and went to the earlier VM.
  it("leaves the next VM alone when the last create's search answers after a close", async () => {
    await signInAs(ADMIN);
    await createsTheVM();
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Close" }));
    fireEvent.change(await screen.findByLabelText("Name"), {
      target: { value: "linux02" },
    });
    await theListAnswers();

    expect(screen.getByLabelText("Name")).toHaveValue("linux02");
    expect(visited.filter((path) => path.startsWith("/inventory"))).toEqual([]);
  });

  it("control: navigates to the VM, and closes, when the session goes on", async () => {
    await signInAs(ADMIN);
    const router = await createsTheVM();

    await theListAnswers();

    await waitFor(() => {
      expect(router.state.location.pathname).toBe(VM_PAGE);
    });
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});

// The server refuses a create's start without execute:vm (403), so the tick is
// offered only to a holder, and says why to anyone else.
describe("CreateVMDialog, Start after creation", () => {
  it.each([
    [["manage:vm"], false],
    [["manage:vm", "execute:vm"], true],
  ])("with %j is offered: %s", async (permissions, offered) => {
    const { next } = await openFilledIn(permissions);
    for (let step = 0; step < 8; step++) await next();

    const tick = screen.getByRole("checkbox", { name: "Start after creation" });
    if (offered) expect(tick).toBeEnabled();
    else expect(tick).toBeDisabled();
    expect(screen.queryByText(/needs the execute:vm permission/) === null).toBe(
      offered,
    );
  });
});

/** Renders the dialog for a holder of `permissions` with General filled in. */
async function openFilledIn(permissions = ["manage:vm"]) {
  useAuthStore.setState({ permissions });
  renderWithProviders(
    <CreateVMDialog open onOpenChange={onOpenChange} clusterId={CLUSTER} />,
  );
  const user = userEvent.setup();
  await screen.findByRole("option", { name: /pve-01/ });
  await user.selectOptions(screen.getByLabelText("Node"), ["pve-01"]);
  fireEvent.change(screen.getByRole("spinbutton"), {
    target: { value: "101" },
  });
  const next = () => user.click(screen.getByRole("button", { name: "Next" }));
  return { user, next };
}

/** Records the body of the create request. */
function captureCreate() {
  const sent: { body?: CreateVMRequest } = {};
  server.routes[CREATE] = (init) => {
    sent.body = JSON.parse(
      typeof init?.body === "string" ? init.body : "{}",
    ) as CreateVMRequest;
    return json({ upid: UPID });
  };
  return sent;
}

// Left out, a file store made the disk raw whatever the dialog said; sent to
// thin LVM, the EFI disk's fixed qcow2 was refused, so a UEFI VM was never made.
describe("CreateVMDialog, disk formats", () => {
  it("sends each disk's format as its store takes it", async () => {
    const sent = captureCreate();
    server.routes[STORAGE] = () =>
      json({
        items: [
          THIN,
          {
            storage: "store02",
            type: "nfs",
            content: "images",
            active: true,
            enabled: true,
            shared: true,
          },
        ],
        total: 2,
      });
    const { user, next } = await openFilledIn();
    await next(); // OS
    await next(); // System
    await user.selectOptions(screen.getByLabelText("BIOS"), ["ovmf"]);
    await screen.findByRole("option", { name: "store01" });
    await user.selectOptions(screen.getByLabelText("EFI disk storage"), [
      "store01",
    ]);
    await next(); // Disks
    await user.selectOptions(screen.getByLabelText("Storage"), ["store02"]);
    for (let step = 0; step < 5; step++) await next();
    await user.click(screen.getByRole("button", { name: "Create VM" }));

    await waitFor(() => {
      expect(sent.body).toBeDefined();
    });
    expect(sent.body?.scsi0).toMatch(/^store02:32,format=qcow2,/);
    expect(sent.body?.efidisk0).toBe(
      "store01:1,efitype=4m,pre-enrolled-keys=1",
    );
  });
});

// A disk or EFI disk left on "Default" was not sent at all, so the VM was made
// without it.
describe("CreateVMDialog, stores left unset", () => {
  it("sends a disk and an EFI disk left unset on the node's first image store", async () => {
    // store00 sorts first but is another node's: Proxmox would refuse it.
    server.routes[STORAGE] = () =>
      json({
        items: [{ ...THIN, storage: "store00", node_id: "node-02" }, THIN],
        total: 2,
      });
    const sent = captureCreate();
    const { user, next } = await openFilledIn();
    await next(); // OS
    await next(); // System
    await user.selectOptions(screen.getByLabelText("BIOS"), ["ovmf"]);
    for (let step = 0; step < 6; step++) await next();
    await user.click(screen.getByRole("button", { name: "Create VM" }));

    await waitFor(() => {
      expect(sent.body).toBeDefined();
    });
    expect(sent.body?.scsi0).toMatch(/^store01:32,/);
    expect(sent.body?.efidisk0).toMatch(/^store01:1,/);
  });

  // IDE takes no iothread: every Windows VM made without the VirtIO ISO (IDE,
  // IO thread on by default) was refused ("not defined in schema").
  it("sends an IDE disk without the IO thread it cannot take", async () => {
    const sent = captureCreate();
    const { user, next } = await openFilledIn();
    await next(); // OS
    await user.selectOptions(screen.getByLabelText("OS type"), ["l24"]);
    for (let step = 0; step < 7; step++) await next();
    await user.click(screen.getByRole("button", { name: "Create VM" }));

    await waitFor(() => {
      expect(sent.body).toBeDefined();
    });
    expect(sent.body?.extra?.["ide0"]).toBe("store01:32,discard=on,ssd=1");
  });

  // A disk added back after the last was removed took SCSI whatever the OS.
  it("gives a disk added after the last was removed the OS's bus", async () => {
    const { user, next } = await openFilledIn();
    await next(); // OS
    await user.selectOptions(screen.getByLabelText("OS type"), ["l24"]);
    await next(); // System
    await next(); // Disks
    await user.click(screen.getByRole("button", { name: "Remove disk 1" }));
    await user.click(screen.getByRole("button", { name: /Add disk/ }));

    expect(screen.getByLabelText<HTMLSelectElement>("Bus type").value).toBe(
      "ide",
    );
  });

  it("holds the wizard at Disks while no store holds images, and says why, until the disk is removed", async () => {
    server.routes[STORAGE] = () => json({ items: [], total: 0 });
    const { user, next } = await openFilledIn();
    await next(); // OS
    await next(); // System
    await next(); // Disks

    expect(screen.getByLabelText("Storage")).toBeInTheDocument();
    expect(screen.getByText(/No storage for disk images/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
    expect(screen.getByRole("button", { name: /^Review/ })).toBeDisabled();

    // A VM without a disk is a choice of its own.
    await user.click(screen.getByRole("button", { name: "Remove disk 1" }));
    expect(screen.getByRole("button", { name: "Next" })).toBeEnabled();
  });
});

describe("CreateVMDialog, reopened and on its node", () => {
  // The dialog is mounted once, in AppShell: closed after a create without a
  // reset, it reopened on that task and went to the same VM again.
  it("starts afresh after a create it has navigated away from", async () => {
    const { user, next } = await openFilledIn();
    for (let step = 0; step < 8; step++) await next();
    listPhase = "with";
    await user.click(screen.getByRole("button", { name: "Create VM" }));

    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
    expect(await screen.findByLabelText("Node")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create VM" })).toBeNull();
  });

  // An ISO listed from another node's own store is not there at create.
  it("offers only the ISO stores the chosen node has", async () => {
    server.routes[STORAGE] = () =>
      json({
        items: [
          THIN,
          makeStorage({
            id: "iso-n2",
            storage: "store04",
            content: "iso",
            node_id: "node-02",
          }),
          makeStorage({
            id: "iso-sh",
            storage: "store03",
            content: "iso",
            shared: true,
            node_id: "node-02",
          }),
        ],
        total: 3,
      });
    const { next } = await openFilledIn();
    await next(); // OS

    const isoStores = within(screen.getByLabelText("ISO storage"))
      .getAllByRole("option")
      .map((o) => o.textContent);
    expect(isoStores).toContain("store03");
    expect(isoStores).not.toContain("store04");
  });
});
