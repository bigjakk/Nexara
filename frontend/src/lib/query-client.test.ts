import { describe, it, expect, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import { retryUnlessClientError, queryClient } from "./query-client";
import { ApiClientError } from "@/lib/api-client";
import { PathSegmentError } from "@/lib/api-path";

function apiError(status: number) {
  return new ApiClientError(status, {
    error: "test_error",
    message: `status ${String(status)}`,
  });
}

describe("retryUnlessClientError", () => {
  it.each([400, 401, 403, 404, 409, 422, 499])(
    "does not retry a %i — the answer cannot change",
    (status) => {
      expect(retryUnlessClientError(0, apiError(status))).toBe(false);
    },
  );

  it.each([408, 429])(
    "retries a %i once — the status itself means 'ask again'",
    (status) => {
      expect(retryUnlessClientError(0, apiError(status))).toBe(true);
      // Pinned: a carve-out that returned a bare `true` would hammer a limiter
      // that is already tripped, forever.
      expect(retryUnlessClientError(1, apiError(status))).toBe(false);
    },
  );

  it.each([500, 502, 503, 504])("retries a %i once", (status) => {
    expect(retryUnlessClientError(0, apiError(status))).toBe(true);
    // Still capped at one retry, exactly as the previous `retry: 1` was.
    expect(retryUnlessClientError(1, apiError(status))).toBe(false);
  });

  it("does not retry a path apiPath refused — the name cannot change", () => {
    const refused = new PathSegmentError("..", "refused");
    expect(retryUnlessClientError(0, refused)).toBe(false);
  });

  it("retries a non-API error, which carries no status to judge", () => {
    expect(retryUnlessClientError(0, new Error("network down"))).toBe(true);
    expect(retryUnlessClientError(1, new Error("network down"))).toBe(false);
  });
});

describe("the app-wide query defaults", () => {
  it("uses the predicate, so no feature has to opt in", () => {
    expect(queryClient.getDefaultOptions().queries?.retry).toBe(
      retryUnlessClientError,
    );
  });

  // Exercises the real default options through a live QueryClient rather than
  // the predicate in isolation: a policy that is correct but wired in wrongly
  // would still pass the unit tests above.
  it.each([
    { status: 403, calls: 1, label: "gives up immediately on a 403" },
    { status: 502, calls: 2, label: "still retries a 502 once" },
  ])("$label", async ({ status, calls }) => {
    const client = new QueryClient({
      defaultOptions: {
        queries: {
          ...queryClient.getDefaultOptions().queries,
          retryDelay: 0, // the decision under test, not the wait
        },
      },
    });
    const queryFn = vi.fn().mockRejectedValue(apiError(status));

    await expect(
      client.query({ queryKey: ["retry-policy", status], queryFn }),
    ).rejects.toThrow(ApiClientError);

    expect(queryFn).toHaveBeenCalledTimes(calls);
    client.clear();
  });
});
