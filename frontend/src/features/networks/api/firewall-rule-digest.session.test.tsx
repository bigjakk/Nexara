import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { QueryClient } from "@tanstack/react-query";

import { createAppQueryClient } from "@/test/app-query-client";
import {
  deferred,
  installFakeServer,
  json,
  type FakeServer,
} from "@/test/fake-server";
import {
  SESSION_ENDS,
  settle,
  signInAsAdmin,
  signOutForGood,
  toastsRaised,
} from "@/test/late-toast-sessions";
import { createWrapper } from "@/test/test-utils";
import { useDeleteNodeFirewallRule } from "@/features/clusters/api/cluster-queries";
import { onRuleWriteError } from "./firewall-rule-digest";
import {
  useDeleteClusterFirewallRule,
  useDeleteVMFirewallRule,
  useUpdateClusterFirewallRule,
} from "./network-queries";

/**
 * A rule update or delete that fails says so in a toast naming whose rule list
 * it was, and on a stale list reloads it (onRuleWriteError). That is the hook's
 * own onError, which TanStack runs whenever the answer lands — after the page
 * has gone, and a sign-out with it. A toast raised after the session ended is
 * shown to whoever is signed in by then, so a failure answered after its session
 * ended must raise no toast and send no reload: the toast would show the next
 * user the previous one's node or guest, and the reload is a request nobody who
 * is here asked for.
 *
 * Each of the four hooks that use it is held to that, with the control that the
 * same answer is reported, and the list reloaded, when the session goes on.
 */

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const CLUSTER = "cluster01";
const DIGEST = "0123456789abcdef0123456789abcdef01234567";
const DENIED = "Proxmox API permission denied";

interface Case {
  hook: string;
  /** Whose list it is, as the toast says it. */
  owner: string;
  /** What did not happen, as the toast says it. */
  outcome: string;
  /** The request the hook sends, as the fake server keys it. */
  route: string;
  /**
   * Mounts the hook on `qc` and returns what submits one write, which settles
   * as how it ended. Mounting is not part of the act that submits: a hook
   * rendered inside one has no result to read until it ends.
   */
  mount: (qc: QueryClient) => () => Promise<"succeeded" | "failed">;
}

function settledAs(write: Promise<unknown>): Promise<"succeeded" | "failed"> {
  return write.then(
    () => "succeeded" as const,
    () => "failed" as const,
  );
}

const CASES: Case[] = [
  {
    hook: "useUpdateClusterFirewallRule",
    owner: "the cluster",
    outcome: "Nothing was changed",
    route: `PUT /api/v1/clusters/${CLUSTER}/firewall/rules/2`,
    mount: (qc) => {
      const { result } = renderHook(
        () => useUpdateClusterFirewallRule(CLUSTER),
        { wrapper: createWrapper({ client: qc, router: false }) },
      );
      return () =>
        settledAs(
          result.current.mutateAsync({
            pos: 2,
            digest: DIGEST,
            rule: { type: "in", action: "ACCEPT", enable: 1 },
          }),
        );
    },
  },
  {
    hook: "useDeleteClusterFirewallRule",
    owner: "the cluster",
    outcome: "Nothing was deleted",
    route: `DELETE /api/v1/clusters/${CLUSTER}/firewall/rules/4?digest=${DIGEST}`,
    mount: (qc) => {
      const { result } = renderHook(
        () => useDeleteClusterFirewallRule(CLUSTER),
        { wrapper: createWrapper({ client: qc, router: false }) },
      );
      return () =>
        settledAs(result.current.mutateAsync({ pos: 4, digest: DIGEST }));
    },
  },
  {
    hook: "useDeleteVMFirewallRule",
    owner: "guest 101",
    outcome: "Nothing was deleted",
    route: `DELETE /api/v1/clusters/${CLUSTER}/vms/101/firewall/rules/4?digest=${DIGEST}`,
    mount: (qc) => {
      const { result } = renderHook(
        () => useDeleteVMFirewallRule(CLUSTER, "101"),
        { wrapper: createWrapper({ client: qc, router: false }) },
      );
      return () =>
        settledAs(result.current.mutateAsync({ pos: 4, digest: DIGEST }));
    },
  },
  {
    hook: "useDeleteNodeFirewallRule",
    owner: "node pve-01",
    outcome: "Nothing was deleted",
    route: `DELETE /api/v1/clusters/${CLUSTER}/nodes/pve-01/firewall/rules/4?digest=${DIGEST}`,
    mount: (qc) => {
      const { result } = renderHook(
        () => useDeleteNodeFirewallRule(CLUSTER, "pve-01"),
        { wrapper: createWrapper({ client: qc, router: false }) },
      );
      return () =>
        settledAs(result.current.mutateAsync({ pos: 4, digest: DIGEST }));
    },
  },
];

const STALE = (c: Case) =>
  `${c.outcome}: the rule list of ${c.owner} changed since it was loaded, so Proxmox refused the change. The list has been reloaded — check it and try again.`;

let server: FakeServer;
let held: ReturnType<typeof deferred<Response>>;

/** The write sent and held, on a client whose reloads are counted. */
async function sent(c: Case) {
  const qc = createAppQueryClient();
  const reloads = vi.spyOn(qc, "invalidateQueries");
  const submit = c.mount(qc);
  let outcome!: Promise<"succeeded" | "failed">;
  await act(async () => {
    outcome = submit();
    await Promise.resolve();
  });
  // Sent in the session that is current now: what ends it comes after.
  await waitFor(() => {
    expect(server.times(c.route)).toBe(1);
  });
  return { outcome, reloads };
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  server = installFakeServer();
  held = deferred<Response>();
  for (const c of CASES) server.routes[c.route] = () => held.promise;
  // No refresh is expected: ADMIN is signed in with a token that has an hour to
  // run. Should one be asked for all the same, it fails as a dead session's does,
  // and is not answered as if it were a session.
  server.routes["POST /api/v1/auth/refresh"] = () => json({}, 401);
  signInAsAdmin();
});

afterEach(() => {
  vi.unstubAllGlobals();
  signOutForGood();
  localStorage.clear();
});

describe.each(CASES)("$hook, answered after its session ended", (c) => {
  it("control: reports a refusal in a toast naming whose list it was, when the session goes on", async () => {
    const { outcome, reloads } = await sent(c);

    held.resolve(json({ error: "forbidden", message: DENIED }, 403));
    expect(await outcome).toBe("failed");
    await settle();

    expect(toastsRaised()).toEqual([`error: ${DENIED}`]);
    // Not a stale list: nothing is reloaded for it.
    expect(reloads).not.toHaveBeenCalled();
  });

  it.each(SESSION_ENDS)(
    "raises no toast for a refusal after %s",
    async (_, end) => {
      const { outcome } = await sent(c);

      end();
      held.resolve(json({ error: "forbidden", message: DENIED }, 403));
      expect(await outcome).toBe("failed");
      await settle();

      expect(toastsRaised()).toEqual([]);
    },
  );

  it("control: reports a stale list in a toast naming its owner, and reloads it, when the session goes on", async () => {
    const { outcome, reloads } = await sent(c);

    held.resolve(json({ error: "conflict", message: "the list changed" }, 409));
    expect(await outcome).toBe("failed");
    await settle();

    expect(toastsRaised()).toEqual([`error: ${STALE(c)}`]);
    expect(reloads).toHaveBeenCalledTimes(1);
  });

  it.each(SESSION_ENDS)(
    "raises no toast and reloads nothing for a stale list after %s",
    async (_, end) => {
      const { outcome, reloads } = await sent(c);

      end();
      held.resolve(
        json({ error: "conflict", message: "the list changed" }, 409),
      );
      expect(await outcome).toBe("failed");
      await settle();

      expect(toastsRaised()).toEqual([]);
      expect(reloads).not.toHaveBeenCalled();
    },
  );
});

describe("onRuleWriteError", () => {
  const reload = vi.fn(() => Promise.resolve());
  const refused = new Error(DENIED);

  beforeEach(() => {
    reload.mockClear();
  });

  it("control: says what happened while the session it is told of goes on", () => {
    expect(
      onRuleWriteError(
        refused,
        "Nothing was deleted",
        "the cluster",
        reload,
        () => false,
      ),
    ).toBeUndefined();
    expect(toastsRaised()).toEqual([`error: ${DENIED}`]);
  });

  it("says nothing, and reloads nothing, once that session has ended", () => {
    expect(
      onRuleWriteError(
        refused,
        "Nothing was deleted",
        "the cluster",
        reload,
        () => true,
      ),
    ).toBeUndefined();
    expect(toastsRaised()).toEqual([]);
    expect(reload).not.toHaveBeenCalled();
  });

  it("treats a write whose session cannot be told as one that has ended", () => {
    expect(
      onRuleWriteError(
        refused,
        "Nothing was deleted",
        "the cluster",
        reload,
        undefined,
      ),
    ).toBeUndefined();
    expect(toastsRaised()).toEqual([]);
    expect(reload).not.toHaveBeenCalled();
  });
});
