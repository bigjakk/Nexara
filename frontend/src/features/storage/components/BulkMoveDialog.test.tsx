import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { clearTokens } from "@/lib/api-client";
import { queryClient } from "@/lib/query-client";
import {
  ADMIN,
  authResponse,
  callerOf,
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
import type { StorageContentItem } from "../types/storage";
import { BulkMoveDialog } from "./BulkMoveDialog";

/**
 * Evacuating a storage resolves each disk, then moves them one after another in
 * a loop that nothing stops: not closing the dialog, and not a sign-out. Each
 * move is a disk move on the cluster, so once the user who started the
 * evacuation has signed out, no further request may go out for it — least of all
 * under whoever signs in next.
 */

const CLUSTER = "cluster01";
const STORAGE = "store01";
const NUMBERS = ["01", "02", "03"];

const ITEMS: StorageContentItem[] = NUMBERS.map((n) => ({
  volid: `${STORAGE}:vm-1${n}-disk-0`,
  format: "raw",
  size: 8_589_934_592,
  ctime: 1_700_000_000,
  content: "images",
  vmid: Number(`1${n}`),
}));

const CONTENT = `GET /api/v1/clusters/${CLUSTER}/storage/${STORAGE}/content`;
const VMS = `GET /api/v1/clusters/${CLUSTER}/vms`;
const config = (n: string) =>
  `GET /api/v1/clusters/${CLUSTER}/vms/guest-${n}/config`;
const move = (n: string) =>
  `POST /api/v1/clusters/${CLUSTER}/vms/guest-${n}/disks/move`;

let server: FakeServer;
let heldMove: ReturnType<typeof deferred<Response>>;
let heldConfig: ReturnType<typeof deferred<Response>>;
let heldList: ReturnType<typeof deferred<Response>>;
/** Who sent each config read and each move after the first volume's. */
let laterConfigCallers: string[];
let laterMoveCallers: string[];
/** Whether the first volume's config read is held back, as the move otherwise is. */
let holdFirstConfig = false;
/** Whether the read of the cluster's VMs, which comes first of all, is held back. */
let holdVmList = false;

async function signInAs(user: User) {
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(user));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

async function startTheEvacuation() {
  const user = userEvent.setup();
  const view = render(
    <QueryClientProvider client={queryClient}>
      <BulkMoveDialog
        clusterId={CLUSTER}
        storageId={STORAGE}
        storageName={STORAGE}
        targetOptions={["store02"]}
      />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("button", { name: "Evacuate" }));
  await user.selectOptions(
    await screen.findByLabelText("Target Storage"),
    "store02",
  );
  await user.click(screen.getByRole("button", { name: "Start Evacuation" }));
  return view;
}

async function sessionEnds() {
  await act(async () => {
    await useAuthStore.getState().logout();
  });
  await signInAs(VIEWER);
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
  heldMove = deferred<Response>();
  heldConfig = deferred<Response>();
  heldList = deferred<Response>();
  laterConfigCallers = [];
  laterMoveCallers = [];
  server.routes[CONTENT] = () => json({ items: ITEMS, total: ITEMS.length });
  server.routes[VMS] = () =>
    holdVmList
      ? heldList.promise
      : json({
          items: NUMBERS.map((n) => ({
            id: `guest-${n}`,
            vmid: Number(`1${n}`),
          })),
          total: NUMBERS.length,
        });
  for (const n of NUMBERS) {
    server.routes[config(n)] = (init) => {
      if (n !== "01") laterConfigCallers.push(callerOf(init));
      return n === "01" && holdFirstConfig
        ? heldConfig.promise
        : json({ scsi0: `${STORAGE}:vm-1${n}-disk-0,size=8G` });
    };
    server.routes[move(n)] = (init) => {
      if (n === "01") return heldMove.promise;
      laterMoveCallers.push(callerOf(init));
      return json({
        upid: `UPID:pve-01:0000000${n}:00000001:00000001:qmmove:1${n}:admin@example.com:`,
      });
    };
  }
  holdFirstConfig = false;
  holdVmList = false;
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
  emptyPerSessionStores();
});

const FIRST_MOVE = {
  upid: "UPID:pve-01:00000001:00000001:00000001:qmmove:101:admin@example.com:",
};

describe("BulkMoveDialog", () => {
  it("stops moving disks when the session that started the evacuation ends", async () => {
    await signInAs(ADMIN);
    const view = await startTheEvacuation();
    await waitFor(() => {
      expect(server.times(move("01"))).toBe(1);
    });
    view.unmount(); // AppShell goes with the session; the loop does not

    await sessionEnds();
    heldMove.resolve(json(FIRST_MOVE)); // the move that was already out finishes
    await flush();
    await flush();

    expect(server.times(move("02"))).toBe(0);
    expect(server.times(move("03"))).toBe(0);
    expect(laterMoveCallers).toEqual([]);
  });

  it("stops resolving disks, and so moves nothing, when the session ends while it is still resolving", async () => {
    holdFirstConfig = true;
    await signInAs(ADMIN);
    const view = await startTheEvacuation();
    await waitFor(() => {
      expect(server.times(config("01"))).toBe(1);
    });
    view.unmount();

    await sessionEnds();
    heldConfig.resolve(json({ scsi0: `${STORAGE}:vm-101-disk-0,size=8G` }));
    await flush();
    await flush();

    // Not another disk's config read, and not a move of any of them.
    expect(server.times(config("02"))).toBe(0);
    expect(server.times(config("03"))).toBe(0);
    for (const n of NUMBERS) expect(server.times(move(n))).toBe(0);
    expect(laterConfigCallers).toEqual([]);
    expect(laterMoveCallers).toEqual([]);
  });

  it("resolves and moves nothing when the session ends before the VMs are even read", async () => {
    holdVmList = true;
    await signInAs(ADMIN);
    const view = await startTheEvacuation();
    await waitFor(() => {
      expect(server.times(VMS)).toBe(1);
    });
    view.unmount();

    await sessionEnds();
    heldList.resolve(
      json({
        items: NUMBERS.map((n) => ({
          id: `guest-${n}`,
          vmid: Number(`1${n}`),
        })),
        total: NUMBERS.length,
      }),
    );
    await flush();
    await flush();

    for (const n of NUMBERS) {
      expect(server.times(config(n))).toBe(0);
      expect(server.times(move(n))).toBe(0);
    }
  });

  it("control: resolves and moves every disk when the session goes on", async () => {
    await signInAs(ADMIN);
    await startTheEvacuation();

    heldMove.resolve(json(FIRST_MOVE));
    await waitFor(() => {
      expect(server.times(move("03"))).toBe(1);
    });

    expect(server.times(move("02"))).toBe(1);
    expect(laterConfigCallers).toHaveLength(2);
    expect(laterMoveCallers).toHaveLength(2);
    expect(
      [...laterConfigCallers, ...laterMoveCallers].every((c) => c === ADMIN.id),
    ).toBe(true);
  });
});
