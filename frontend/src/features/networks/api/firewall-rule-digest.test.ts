import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { toast } from "sonner";
import { createWrapper } from "@/test/test-utils";
import { stubApi } from "@/test/fetch-stub";
import {
  useDeleteVMFirewallRule,
  useUpdateClusterFirewallRule,
} from "./network-queries";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

// The two rule-write hooks no page uses yet. Whoever wires them up gets the
// digest by construction: it is part of what they take, and they send it.

const CLUSTER = "c1";
const DIGEST = "0123456789abcdef0123456789abcdef01234567";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

/** stubApi, plus the body of every write. */
function stubWithBodies() {
  const api = stubApi({});
  const bodies: unknown[] = [];
  const inner = globalThis.fetch;
  vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
    // The session refresh apiClient tries first is not one of the writes.
    if (
      init?.method !== undefined &&
      init.method !== "GET" &&
      input !== "/api/v1/auth/refresh"
    ) {
      bodies.push(
        typeof init.body === "string" ? JSON.parse(init.body) : undefined,
      );
    }
    return inner(input, init);
  });
  return { api, bodies };
}

describe("useUpdateClusterFirewallRule", () => {
  it("sends the list's digest with the rule", async () => {
    const { api, bodies } = stubWithBodies();
    const { result } = renderHook(() => useUpdateClusterFirewallRule(CLUSTER), {
      wrapper: createWrapper(),
    });

    act(() => {
      result.current.mutate({
        pos: 2,
        digest: DIGEST,
        rule: { type: "in", action: "ACCEPT", enable: 1 },
      });
    });

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    expect(api.writes()).toEqual([
      `PUT /api/v1/clusters/${CLUSTER}/firewall/rules/2`,
    ]);
    expect(bodies).toEqual([
      { type: "in", action: "ACCEPT", enable: 1, digest: DIGEST },
    ]);
  });

  it("sends nothing without a digest", async () => {
    const { api } = stubWithBodies();
    const { result } = renderHook(() => useUpdateClusterFirewallRule(CLUSTER), {
      wrapper: createWrapper(),
    });

    act(() => {
      result.current.mutate({
        pos: 2,
        digest: "",
        rule: { type: "in", action: "ACCEPT", enable: 1 },
      });
    });

    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });
    expect(api.writes()).toEqual([]);
    expect(toast.error).toHaveBeenCalledTimes(1);
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain(
      "Nothing was sent: this rule came without the rule list's digest",
    );
  });
});

describe("useDeleteVMFirewallRule", () => {
  it("sends the list's digest with the delete", async () => {
    const { api } = stubWithBodies();
    const { result } = renderHook(
      () => useDeleteVMFirewallRule(CLUSTER, "101"),
      {
        wrapper: createWrapper(),
      },
    );

    act(() => {
      result.current.mutate({ pos: 4, digest: DIGEST });
    });

    await waitFor(() => {
      expect(result.current.isSuccess).toBe(true);
    });
    expect(api.writes()).toEqual([
      `DELETE /api/v1/clusters/${CLUSTER}/vms/101/firewall/rules/4?digest=${DIGEST}`,
    ]);
  });
});
