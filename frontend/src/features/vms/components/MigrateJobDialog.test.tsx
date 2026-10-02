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
import { MigrateJobDialog } from "./MigrateJobDialog";

/**
 * Creating a migration job and running its pre-flight check are two requests,
 * the second sent when the first has been answered. The check is a write to the
 * job the create just made — it sets it to "checking", records the report and
 * moves it on, to pending or to failed — and it needs manage:migration, not
 * ownership of the job. So once the user who pressed the button has signed out,
 * it must not go out for them: it would run as whoever signs in next.
 */

const CLUSTER = "cluster01";
const CREATE = "POST /api/v1/migrations";
const CHECK = "POST /api/v1/migrations/job-01/check";

const NODES = ["pve-01", "pve-02"].map((name, i) => ({
  id: `node-0${String(i + 1)}`,
  cluster_id: CLUSTER,
  name,
  status: "online",
}));

let server: FakeServer;
let created: ReturnType<typeof deferred<Response>>;
/** Who sent each pre-flight check. */
let checkCallers: string[];

async function signInAs(user: User) {
  server.routes["POST /api/v1/auth/login"] = () => json(authResponse(user));
  await act(async () => {
    await useAuthStore
      .getState()
      .login({ email: user.email, password: "example-password" });
  });
}

async function sessionEnds() {
  await act(async () => {
    await useAuthStore.getState().logout();
  });
  await signInAs(VIEWER);
}

async function pressCreate() {
  const user = userEvent.setup();
  const view = render(
    <QueryClientProvider client={queryClient}>
      <MigrateJobDialog
        open
        onOpenChange={() => undefined}
        clusterId={CLUSTER}
        resourceId="guest-01"
        vmid={101}
        vmName="linux01"
        kind="vm"
        currentNode="pve-01"
        status="running"
      />
    </QueryClientProvider>,
  );
  const button = await screen.findByRole("button", {
    name: "Create & Run Pre-Flight Checks",
  });
  // Enabled once the nodes are read and a target is picked.
  await waitFor(() => {
    expect(button).toBeEnabled();
  });
  await user.click(button);
  await waitFor(() => {
    expect(server.times(CREATE)).toBe(1);
  });
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
  server = installFakeServer();
  server.routes["POST /api/v1/auth/logout"] = () =>
    new Response(null, { status: 204 });
  server.routes["POST /api/v1/auth/refresh"] = () => json({}, 401);
  created = deferred<Response>();
  checkCallers = [];
  server.routes["GET /api/v1/clusters"] = () => json({ items: [], total: 0 });
  server.routes[`GET /api/v1/clusters/${CLUSTER}/nodes`] = () =>
    json({ items: NODES, total: NODES.length });
  server.routes[CREATE] = () => created.promise;
  server.routes[CHECK] = (init) => {
    checkCallers.push(callerOf(init));
    return json({ passed: true, checks: [] });
  };
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
  emptyPerSessionStores();
});

describe("MigrateJobDialog, creating a job and checking it", () => {
  it("does not run the check on the job when the session that created it has ended", async () => {
    await signInAs(ADMIN);
    const view = await pressCreate();
    view.unmount(); // AppShell goes with the session; the second request does not

    await sessionEnds();
    created.resolve(json({ id: "job-01", status: "pending" })); // the create already out finishes
    await flush();
    await flush();

    expect(server.times(CHECK)).toBe(0);
    expect(checkCallers).toEqual([]);
  });

  it("control: runs the check, as the user who created the job, when the session goes on", async () => {
    await signInAs(ADMIN);
    await pressCreate();

    created.resolve(json({ id: "job-01", status: "pending" }));
    await waitFor(() => {
      expect(server.times(CHECK)).toBe(1);
    });

    expect(checkCallers).toEqual([ADMIN.id]);
  });
});
