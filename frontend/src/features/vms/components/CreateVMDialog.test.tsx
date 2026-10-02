import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
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
import type { User } from "@/types/api";
import { useAuthStore } from "@/stores/auth-store";
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
  await user.selectOptions(screen.getAllByRole("combobox")[0] as HTMLElement, [
    "pve-01",
  ]);
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
