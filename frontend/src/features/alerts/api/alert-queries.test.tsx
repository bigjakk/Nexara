import { describe, it, expect, afterEach, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { createTestQueryClient, createWrapper } from "@/test/test-utils";
import { stubApi } from "@/test/fetch-stub";
import { useResolveAlert } from "./alert-queries";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useResolveAlert", () => {
  // A resolve that lost the race to the engine answers 409; the list must
  // still refetch, or the row keeps showing Firing beside the message.
  it("refreshes the alerts when the resolve answers 409", async () => {
    stubApi({}, {
      "POST /api/v1/alerts/a1/resolve": () =>
        new Response(JSON.stringify({ error: "conflict", message: "Alert cannot be resolved from current state" }), {
          status: 409,
          headers: { "Content-Type": "application/json" },
        }),
    });
    const client = createTestQueryClient();
    const invalidate = vi.spyOn(client, "invalidateQueries");
    const { result } = renderHook(() => useResolveAlert(), { wrapper: createWrapper({ client }) });
    result.current.mutate("a1");
    await waitFor(() => {
      expect(result.current.isError).toBe(true);
    });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["alerts"] });
  });
});
