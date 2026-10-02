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
import { BulkPinDialog } from "./BulkPinDialog";

/**
 * The dialog scans each node's SSH host key, then pins the ones the user trusts,
 * one request each in a loop. Pinning is the write that makes the cluster trust a
 * host key. The dialog stops both loops when it goes away; it must also stop them
 * when the session that opened it ends, so that nothing the previous user chose
 * to trust is pinned on behalf of whoever signs in next.
 */

const CLUSTER = "cluster01";
const SCAN = `POST /api/v1/clusters/${CLUSTER}/ssh-credentials/test`;
const PIN = `POST /api/v1/clusters/${CLUSTER}/ssh-known-hosts`;

// Stable across renders: the dialog rescans whenever it is handed a new array.
const NODES = ["pve-01", "pve-02", "pve-03"].map((name, i) => ({
  name,
  address: `192.0.2.${String(10 + i)}`,
}));

const FINGERPRINT = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99";

const nodeOf = (init: RequestInit | undefined): string => {
  const body = typeof init?.body === "string" ? init.body : "{}";
  return (JSON.parse(body) as { node_name?: string }).node_name ?? "";
};

const PENDING = (node: string) =>
  json({
    success: false,
    message: "host key not pinned",
    host_key_pending: {
      host: `${node}.example.com`,
      port: 22,
      fingerprint: FINGERPRINT,
      public_key: "ssh-ed25519 AAAAexample",
    },
  });

const PINNED = (node: string) =>
  json({
    id: `key-${node}`,
    cluster_id: CLUSTER,
    host: `${node}.example.com`,
    port: 22,
    fingerprint: FINGERPRINT,
    pinned_at: "2026-01-01T00:00:00Z",
  });

/** How the request that was already out when the session ended finishes. */
const FINISHES = [
  { how: "answers", settle: (node: string) => PENDING(node), pin: PINNED },
  {
    how: "fails",
    settle: () => json({ error: "internal", message: "no route to host" }, 500),
    pin: () => json({ error: "internal", message: "no route to host" }, 500),
  },
] as const;

let server: FakeServer;
let firstScan: ReturnType<typeof deferred<Response>>;
let firstPin: ReturnType<typeof deferred<Response>>;
/** Whether the first node's scan is held back, as the first pin otherwise is. */
let holdFirstScan = false;
/** Who sent each scan and each pin after the first. */
let laterScanCallers: string[];
let laterPinCallers: string[];

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

function openTheDialog() {
  return render(
    <QueryClientProvider client={queryClient}>
      <BulkPinDialog
        open
        onOpenChange={() => undefined}
        clusterId={CLUSTER}
        nodes={NODES}
      />
    </QueryClientProvider>,
  );
}

/** Scans every node and presses Trust & Pin on all of them. */
async function pinEveryNode() {
  const user = userEvent.setup();
  const view = openTheDialog();
  await user.click(
    await screen.findByRole("button", { name: /Trust & Pin 3 of 3/ }),
  );
  await waitFor(() => {
    expect(server.times(PIN)).toBe(1);
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
  firstScan = deferred<Response>();
  firstPin = deferred<Response>();
  laterScanCallers = [];
  laterPinCallers = [];
  let scans = 0;
  server.routes[SCAN] = (init) => {
    scans += 1;
    if (scans === 1 && holdFirstScan) return firstScan.promise;
    if (scans > 1) laterScanCallers.push(callerOf(init));
    return PENDING(nodeOf(init));
  };
  let pins = 0;
  server.routes[PIN] = (init) => {
    pins += 1;
    if (pins === 1) return firstPin.promise;
    laterPinCallers.push(callerOf(init));
    return PINNED(nodeOf(init));
  };
  holdFirstScan = false;
});

afterEach(() => {
  vi.unstubAllGlobals();
  clearTokens();
  queryClient.clear();
  localStorage.clear();
  emptyPerSessionStores();
});

describe("BulkPinDialog, scanning the nodes", () => {
  it.each(FINISHES)(
    "stops scanning when the session that opened the dialog ends and the scan in flight $how",
    async ({ settle }) => {
      holdFirstScan = true;
      await signInAs(ADMIN);
      openTheDialog();
      await waitFor(() => {
        expect(server.times(SCAN)).toBe(1);
      });

      await sessionEnds(); // the dialog is still open: nothing here closes it
      firstScan.resolve(settle("pve-01"));
      await flush();
      await flush();

      expect(server.times(SCAN)).toBe(1);
      expect(laterScanCallers).toEqual([]);
    },
  );

  it("stops scanning when the dialog goes away", async () => {
    holdFirstScan = true;
    await signInAs(ADMIN);
    const view = openTheDialog();
    await waitFor(() => {
      expect(server.times(SCAN)).toBe(1);
    });
    view.unmount();

    firstScan.resolve(PENDING("pve-01"));
    await flush();
    await flush();

    expect(server.times(SCAN)).toBe(1);
    expect(laterScanCallers).toEqual([]);
  });

  it("control: scans every node when the session goes on", async () => {
    holdFirstScan = true;
    await signInAs(ADMIN);
    openTheDialog();
    await waitFor(() => {
      expect(server.times(SCAN)).toBe(1);
    });

    firstScan.resolve(PENDING("pve-01"));
    await screen.findByRole("button", { name: /Trust & Pin 3 of 3/ });

    expect(server.times(SCAN)).toBe(3);
    expect(laterScanCallers).toHaveLength(2);
    expect(laterScanCallers.every((c) => c === ADMIN.id)).toBe(true);
  });
});

describe("BulkPinDialog, pinning the host keys", () => {
  it.each(FINISHES)(
    "stops pinning when the session of the user who chose them ends and the pin in flight $how",
    async ({ pin }) => {
      await signInAs(ADMIN);
      await pinEveryNode();

      await sessionEnds(); // the dialog is still open: nothing here closes it
      firstPin.resolve(pin("pve-01"));
      await flush();
      await flush();

      expect(server.times(PIN)).toBe(1);
      expect(laterPinCallers).toEqual([]);
    },
  );

  it("stops pinning when the dialog goes away", async () => {
    await signInAs(ADMIN);
    const view = await pinEveryNode();
    view.unmount();

    firstPin.resolve(PINNED("pve-01"));
    await flush();
    await flush();

    expect(server.times(PIN)).toBe(1);
    expect(laterPinCallers).toEqual([]);
  });

  it("control: pins every chosen node when the session goes on", async () => {
    await signInAs(ADMIN);
    await pinEveryNode();

    firstPin.resolve(PINNED("pve-01"));
    await screen.findByRole("button", { name: "Done" });

    expect(server.times(PIN)).toBe(3);
    expect(laterPinCallers).toHaveLength(2);
    expect(laterPinCallers.every((c) => c === ADMIN.id)).toBe(true);
  });
});
