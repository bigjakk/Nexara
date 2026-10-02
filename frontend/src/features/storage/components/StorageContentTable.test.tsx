import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { clearTokens } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
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
import { StorageContentTable } from "./StorageContentTable";

/**
 * "Delete selected" deletes the volumes one after another in a loop that nothing
 * stops: not leaving the page, and not a sign-out. Each is a volume deleted from
 * the storage, for good, so once the user who chose them has signed out, no
 * further DELETE may go out for it — least of all under whoever signs in next.
 */

const CLUSTER = "cluster01";
const STORAGE = "store01";
const VOLIDS = ["01", "02", "03"].map((n) => `${STORAGE}:vm-1${n}-disk-0`);

const ITEMS: StorageContentItem[] = VOLIDS.map((volid, i) => ({
  volid,
  format: "raw",
  size: 8_589_934_592,
  ctime: 1_700_000_000,
  content: "images",
  vmid: 101 + i,
}));

const del = (volid: string) =>
  `DELETE ${apiPath`/api/v1/clusters/${CLUSTER}/storage/${STORAGE}/content/${volid}`}`;

let server: FakeServer;
let firstDelete: ReturnType<typeof deferred<Response>>;
/** Who sent each DELETE after the first. */
let laterCallers: string[];

async function signInAs(user: User) {
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(user));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

async function deleteEverySelected() {
  const user = userEvent.setup();
  const view = render(
    <QueryClientProvider client={queryClient}>
      <StorageContentTable
        items={ITEMS}
        clusterId={CLUSTER}
        storageId={STORAGE}
      />
    </QueryClientProvider>,
  );
  await user.click(screen.getByRole("checkbox", { name: "Select all items" }));
  await user.click(screen.getByRole("button", { name: "Delete selected" }));
  return view;
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
  vi.spyOn(window, "confirm").mockReturnValue(true);
  server = installFakeServer();
  server.routes["POST /api/v1/auth/logout"] = () =>
    new Response(null, { status: 204 });
  server.routes["POST /api/v1/auth/refresh"] = () => json({}, 401);
  firstDelete = deferred<Response>();
  laterCallers = [];
  VOLIDS.forEach((volid, i) => {
    server.routes[del(volid)] = (init) => {
      if (i === 0) return firstDelete.promise;
      laterCallers.push(callerOf(init));
      return json({
        upid: `UPID:pve-01:0000000${String(i)}:00000001:00000001:imgdel:101:admin@example.com:`,
        status: "ok",
      });
    };
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
  emptyPerSessionStores();
});

const FIRST_DONE = {
  upid: "UPID:pve-01:00000000:00000001:00000001:imgdel:101:admin@example.com:",
  status: "ok",
};

describe("StorageContentTable, deleting the selected volumes", () => {
  it("stops deleting when the session of the user who chose them ends", async () => {
    await signInAs(ADMIN);
    const view = await deleteEverySelected();
    await waitFor(() => {
      expect(server.times(del(VOLIDS[0] ?? ""))).toBe(1);
    });
    view.unmount(); // AppShell goes with the session; the loop does not

    await act(async () => {
      await useAuthStore.getState().logout();
    });
    await signInAs(VIEWER);
    firstDelete.resolve(json(FIRST_DONE)); // the delete that was already out finishes
    await flush();
    await flush();

    expect(server.times(del(VOLIDS[1] ?? ""))).toBe(0);
    expect(server.times(del(VOLIDS[2] ?? ""))).toBe(0);
    expect(laterCallers).toEqual([]);
  });

  it("control: deletes every selected volume when the session goes on", async () => {
    await signInAs(ADMIN);
    await deleteEverySelected();

    firstDelete.resolve(json(FIRST_DONE));
    await waitFor(() => {
      expect(server.times(del(VOLIDS[2] ?? ""))).toBe(1);
    });

    expect(server.times(del(VOLIDS[1] ?? ""))).toBe(1);
    expect(laterCallers.length).toBeGreaterThan(0);
    expect(laterCallers.every((c) => c === ADMIN.id)).toBe(true);
  });
});
