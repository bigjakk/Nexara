import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { clearTokens } from "@/lib/api-client";
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
import { useTaskLogStore } from "@/stores/task-log-store";
import { MOVE_NOTES, waitForTask } from "../lib/resolve-disk";
import { MigrateBatchDialog, type MigrateBatchJob } from "./MigrateBatchDialog";

// The real wait, which one test replaces once with a status it cannot read.
vi.mock("../lib/resolve-disk", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../lib/resolve-disk")>();
  return { ...actual, waitForTask: vi.fn(actual.waitForTask) };
});

/**
 * The dialog moves its volumes one after another in a loop that nothing stops:
 * not closing the dialog, and not a sign-out. Each move is a disk move on the
 * cluster, so once the user who started the batch has signed out, no further
 * request may go out for it — least of all under whoever signs in next — and
 * the task it started may not be put in their task log.
 */

const CLUSTER = "cluster01";
const JOBS: MigrateBatchJob[] = ["01", "02", "03"].map((n) => ({
  label: `linux${n} (1${n})`,
  guestId: `guest-${n}`,
  guestKind: "vm",
  vmid: Number(`1${n}`),
  volid: `store01:vm-1${n}-disk-0`,
  running: false,
}));

const config = (n: string) =>
  `GET /api/v1/clusters/${CLUSTER}/vms/guest-${n}/config`;
const move = (n: string) =>
  `POST /api/v1/clusters/${CLUSTER}/vms/guest-${n}/disks/move`;
const upidOf = (n: string) =>
  `UPID:pve-01:0000000${n}:00000001:00000001:qmmove:1${n}:admin@example.com:`;
const taskStatus = (upid: string) =>
  `GET /api/v1/clusters/${CLUSTER}/tasks/${encodeURIComponent(upid)}`;
const stopped = (exit = "OK") => json({ status: "stopped", exit_status: exit });

let server: FakeServer;
let firstMove: ReturnType<typeof deferred<Response>>;
/** Who sent each request that reached the later guests. */
let callersOfLaterRequests: string[];

async function signInAs(user: User) {
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(user));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

async function startTheBatch(onOpenChange: (open: boolean) => void = vi.fn()) {
  const user = userEvent.setup();
  const view = render(
    <MigrateBatchDialog
      open
      onOpenChange={onOpenChange}
      clusterId={CLUSTER}
      jobs={JOBS}
      targetOptions={["store02"]}
      title="Move volumes"
    />,
  );
  await user.selectOptions(screen.getByLabelText("Target storage"), "store02");
  await user.click(screen.getByRole("button", { name: "Migrate" }));
  await waitFor(() => {
    expect(server.times(move("01"))).toBe(1);
  });
  return view;
}

beforeEach(() => {
  localStorage.clear();
  clearTokens();
  emptyPerSessionStores();
  useTaskLogStore.setState({ focusedTask: null });
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
  firstMove = deferred<Response>();
  callersOfLaterRequests = [];
  for (const n of ["01", "02", "03"]) {
    server.routes[config(n)] = (init) => {
      if (n !== "01") callersOfLaterRequests.push(callerOf(init));
      return json({ scsi0: `store01:vm-1${n}-disk-0,size=8G` });
    };
    server.routes[move(n)] = (init) => {
      if (n === "01") return firstMove.promise;
      callersOfLaterRequests.push(callerOf(init));
      return json({ upid: upidOf(n) });
    };
    server.routes[taskStatus(upidOf(n))] = () => stopped();
  }
  server.routes[taskStatus(FIRST_TASK.upid)] = () => stopped();
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  localStorage.clear();
  emptyPerSessionStores();
});

const FIRST_TASK = {
  upid: "UPID:pve-01:00000001:00000001:00000001:qmmove:101:admin@example.com:",
};

describe("MigrateBatchDialog", () => {
  // A disk move holds its guest's config lock until it ends, so a second
  // move of that guest started earlier fails; and a move Proxmox took may
  // still fail. The row ticks on the task's end, and the next waits for it.
  it("starts the next move only once the task has ended, and marks a failed task failed", async () => {
    const firstTask = deferred<Response>();
    server.routes[taskStatus(FIRST_TASK.upid)] = () => firstTask.promise;
    await signInAs(ADMIN);
    await startTheBatch();
    firstMove.resolve(json(FIRST_TASK));
    await waitFor(() => {
      expect(server.times(taskStatus(FIRST_TASK.upid))).toBe(1);
    });
    await flush();

    expect(server.times(move("02"))).toBe(0);
    expect(screen.queryByText("lock timeout")).not.toBeInTheDocument();

    firstTask.resolve(stopped("lock timeout"));
    await waitFor(() => {
      expect(server.times(move("03"))).toBe(1);
    });
    expect(screen.getByText("lock timeout")).toBeInTheDocument();
    expect(screen.getByText(/2 of 3 completed, 1 failed/)).toBeInTheDocument();
  });

  // A status unread for two minutes may be a move still holding the lock:
  // the batch stops there rather than start the next move into it.
  it("stops the batch, starting no further move, when a task's status cannot be read", async () => {
    vi.mocked(waitForTask).mockResolvedValueOnce({ status: "unknown" });
    await signInAs(ADMIN);
    await startTheBatch();
    firstMove.resolve(json(FIRST_TASK));

    expect(await screen.findByText(MOVE_NOTES.unknown)).toBeInTheDocument();
    expect(screen.getAllByText(MOVE_NOTES.skipped)).toHaveLength(2);
    expect(server.times(move("02"))).toBe(0);
    expect(server.times(move("03"))).toBe(0);
  });

  // Closed while running, the loop went on out of sight and a reopened
  // dialog could start a second batch beside it; a close waits for Stop.
  it("ignores a close while the batch runs", async () => {
    const onOpenChange = vi.fn();
    await signInAs(ADMIN);
    await startTheBatch(onOpenChange);

    await userEvent.keyboard("{Escape}");

    expect(onOpenChange).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
  });

  // Stop ends the wait for the current task without cancelling it, starts
  // nothing more, and lets the dialog close.
  it("stops at once on Stop, leaving the current move running and starting no other", async () => {
    const firstTask = deferred<Response>();
    server.routes[taskStatus(FIRST_TASK.upid)] = () => firstTask.promise;
    await signInAs(ADMIN);
    await startTheBatch();
    firstMove.resolve(json(FIRST_TASK));
    await waitFor(() => {
      expect(server.times(taskStatus(FIRST_TASK.upid))).toBe(1);
    });

    await userEvent.click(screen.getByRole("button", { name: "Stop" }));

    expect(await screen.findByText(MOVE_NOTES.detached)).toBeInTheDocument();
    expect(screen.getAllByText(MOVE_NOTES.skipped)).toHaveLength(2);
    expect(screen.getByText("Close", { selector: "button" })).toBeEnabled();
    firstTask.resolve(stopped());
    await flush();
    expect(server.times(move("02"))).toBe(0);
  });

  it("stops when the session that started the batch ends: no further request, no task in the next user's log", async () => {
    await signInAs(ADMIN);
    const view = await startTheBatch();
    view.unmount(); // AppShell goes with the session; the loop does not

    await act(async () => {
      await useAuthStore.getState().logout();
    });
    await signInAs(VIEWER);
    firstMove.resolve(json(FIRST_TASK)); // the move that was already out finishes
    await flush();
    await flush();

    // The next user's task log is theirs.
    expect(useTaskLogStore.getState().focusedTask).toBeNull();
    // And nothing more was sent for the batch the first user started.
    for (const n of ["02", "03"]) {
      expect(server.times(config(n))).toBe(0);
      expect(server.times(move(n))).toBe(0);
    }
    expect(callersOfLaterRequests).toEqual([]);
  });

  it("stops between a volume's config read and its move when the session ends there", async () => {
    await signInAs(ADMIN);
    // Hold the first volume's config read instead of its move.
    const configRead = deferred<Response>();
    server.routes[config("01")] = () => configRead.promise;
    const user = userEvent.setup();
    const view = render(
      <MigrateBatchDialog
        open
        onOpenChange={() => undefined}
        clusterId={CLUSTER}
        jobs={JOBS}
        targetOptions={["store02"]}
        title="Move volumes"
      />,
    );
    await user.selectOptions(
      screen.getByLabelText("Target storage"),
      "store02",
    );
    await user.click(screen.getByRole("button", { name: "Migrate" }));
    await waitFor(() => {
      expect(server.times(config("01"))).toBe(1);
    });
    view.unmount();

    await act(async () => {
      await useAuthStore.getState().logout();
    });
    configRead.resolve(json({ scsi0: "store01:vm-101-disk-0,size=8G" }));
    await flush();
    await flush();

    // The volume's move, the one that would have run as whoever is signed in
    // by now, was never sent; nor was anything for the rest.
    for (const n of ["01", "02", "03"]) {
      expect(server.times(move(n))).toBe(0);
    }
    expect(server.times(config("02"))).toBe(0);
  });

  it("control: carries on through every volume, and surfaces the first task, when the session goes on", async () => {
    await signInAs(ADMIN);
    await startTheBatch();

    firstMove.resolve(json(FIRST_TASK));
    await waitFor(() => {
      expect(server.times(move("03"))).toBe(1);
    });

    expect(server.times(move("02"))).toBe(1);
    expect(useTaskLogStore.getState().focusedTask).toEqual({
      clusterId: CLUSTER,
      upid: FIRST_TASK.upid,
      description: "Migrate linux01 (101) → store02",
    });
    expect(callersOfLaterRequests.every((c) => c === ADMIN.id)).toBe(true);
    expect(callersOfLaterRequests).not.toEqual([]);
  });
});
