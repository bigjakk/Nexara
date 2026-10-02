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
import type { NodeResponse, User, VMResponse } from "@/types/api";
import { useAuthStore } from "@/stores/auth-store";
import type { HARuleEntry } from "../api/ha-queries";
import { HARuleForm } from "./HARuleForm";

/**
 * Saving a rule first brings the resources it names under HA management, one
 * request each, and only then writes the rule. That sequence outlives the dialog
 * that started it, and nothing stops it: not closing the dialog, and not a
 * sign-out. Each step is a write to the cluster's HA configuration, so once the
 * user who pressed Save has signed out, no further write may go out for it —
 * least of all under whoever signs in next.
 */

const CLUSTER = "cluster01";
const RESOURCES = `/api/v1/clusters/${CLUSTER}/ha/resources`;
const ADD_RESOURCE = `POST ${RESOURCES}`;
const WRITE_RULE = `PUT /api/v1/clusters/${CLUSTER}/ha/rules/rule-01`;

const RULE: HARuleEntry = {
  rule: "rule-01",
  type: "node-affinity",
  resources: "vm:101,vm:102,vm:103",
  nodes: "pve-01",
  strict: 0,
  comment: "",
  disable: 0,
};

const VMS = [101, 102, 103].map(
  (vmid, i) =>
    ({
      id: `guest-0${String(i + 1)}`,
      cluster_id: CLUSTER,
      node_id: "node-01",
      vmid,
      name: `linux0${String(i + 1)}`,
      type: "qemu",
      status: "running",
      template: false,
    }) as unknown as VMResponse,
);

const NODES = [
  { id: "node-01", cluster_id: CLUSTER, name: "pve-01", status: "online" },
] as unknown as NodeResponse[];

let server: FakeServer;
let firstResource: ReturnType<typeof deferred<Response>>;
/** Who sent each write after the first resource. */
let laterCallers: string[];
/** The resources the cluster already holds under HA; the rest are added on save. */
let alreadyManaged: string[];

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

/** Opens the form on the rule and presses Save, with the resources not yet managed. */
async function pressSave(onSuccess: () => void) {
  const user = userEvent.setup();
  const view = render(
    <QueryClientProvider client={queryClient}>
      <HARuleForm
        mode="edit"
        clusterId={CLUSTER}
        pveVersion="9.0.6"
        allVMs={VMS}
        allNodes={NODES}
        rule={RULE}
        onSuccess={onSuccess}
      />
    </QueryClientProvider>,
  );
  // The offer to add them to HA appears once the cluster's resources are read.
  await screen.findByText(/to HA management with the settings below/i);
  await user.click(screen.getByRole("button", { name: "Save" }));
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
  firstResource = deferred<Response>();
  laterCallers = [];
  alreadyManaged = [];
  server.routes[`GET ${RESOURCES}`] = () =>
    json({
      items: alreadyManaged.map((sid) => ({
        sid,
        type: "vm",
        state: "started",
      })),
      total: alreadyManaged.length,
    });
  server.routes[`GET /api/v1/clusters/${CLUSTER}/ha/groups`] = () =>
    json({ items: [], total: 0 });
  let added = 0;
  server.routes[ADD_RESOURCE] = (init) => {
    added += 1;
    if (added === 1) return firstResource.promise;
    laterCallers.push(callerOf(init));
    return json({});
  };
  server.routes[WRITE_RULE] = (init) => {
    laterCallers.push(callerOf(init));
    return json({});
  };
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
  emptyPerSessionStores();
});

describe("HARuleForm, saving a rule", () => {
  it("stops adding resources to HA when the session of the user who pressed Save ends", async () => {
    await signInAs(ADMIN);
    const onSuccess = vi.fn();
    const view = await pressSave(onSuccess);
    await waitFor(() => {
      expect(server.times(ADD_RESOURCE)).toBe(1);
    });
    view.unmount(); // AppShell goes with the session; the save does not

    await sessionEnds();
    firstResource.resolve(json({})); // the write that was already out finishes
    await flush();
    await flush();

    // Not the second and third resource, not the rule, and no "saved".
    expect(server.times(ADD_RESOURCE)).toBe(1);
    expect(server.times(WRITE_RULE)).toBe(0);
    expect(onSuccess).not.toHaveBeenCalled();
    expect(laterCallers).toEqual([]);
  });

  it("does not write the rule when the session ends during its last resource", async () => {
    alreadyManaged = ["vm:101", "vm:102"]; // so only vm:103 is added first
    await signInAs(ADMIN);
    const onSuccess = vi.fn();
    const view = await pressSave(onSuccess);
    await waitFor(() => {
      expect(server.times(ADD_RESOURCE)).toBe(1);
    });
    view.unmount();

    await sessionEnds();
    firstResource.resolve(json({}));
    await flush();
    await flush();

    expect(server.times(WRITE_RULE)).toBe(0);
    expect(onSuccess).not.toHaveBeenCalled();
    expect(laterCallers).toEqual([]);
  });

  it("control: adds every resource, writes the rule and reports it saved when the session goes on", async () => {
    await signInAs(ADMIN);
    const onSuccess = vi.fn();
    await pressSave(onSuccess);

    firstResource.resolve(json({}));
    await waitFor(() => {
      expect(onSuccess).toHaveBeenCalledTimes(1);
    });

    expect(server.times(ADD_RESOURCE)).toBe(3);
    expect(server.times(WRITE_RULE)).toBe(1);
    expect(laterCallers).toHaveLength(3);
    expect(laterCallers.every((c) => c === ADMIN.id)).toBe(true);
  });
});
