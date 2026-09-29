import { beforeEach, describe, it, expect, vi } from "vitest";
import {
  MutationObserver as QueryMutationObserver,
  QueryClient,
} from "@tanstack/react-query";
import { toast } from "sonner";
import {
  createMutationCache,
  retryUnlessClientError,
  queryClient,
} from "./query-client";
import { ApiClientError } from "@/lib/api-client";
import { PathSegmentError } from "@/lib/api-path";

// The mutation-error net toasts through sonner, so this one mock sees every
// toast it raises.
vi.mock("sonner", () => ({ toast: { error: vi.fn() } }));

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

describe("the app-wide mutation-error net", () => {
  const PROBE = "PROBE-NOT-A-REAL-FAILURE";
  const mockedToastError = vi.mocked(toast.error);

  beforeEach(() => {
    vi.clearAllMocks();
  });

  /**
   * Fails one mutation on `client` with `failure`. `hook` is what the hook
   * itself passes to useMutation, which is where an onError has to be for the
   * net to see it.
   */
  async function fail(
    client: QueryClient,
    failure: unknown,
    hook: { onError?: () => void } = {},
  ) {
    const observer = new QueryMutationObserver(client, {
      mutationFn: vi.fn().mockRejectedValue(failure),
      ...hook,
    });
    await observer.mutate(undefined).catch(() => undefined);
    client.clear();
  }

  it.each([
    ["the error's own message", new Error(PROBE), PROBE],
    ["a generic one for an error with none", new Error(""), "Request failed"],
    [
      "a generic one for a failure that is not an Error",
      PROBE,
      "Request failed",
    ],
  ])(
    "toasts %s, for a mutation whose hook has no onError",
    async (_, failure, shown) => {
      await fail(
        new QueryClient({ mutationCache: createMutationCache() }),
        failure,
      );

      expect(mockedToastError).toHaveBeenCalledTimes(1);
      expect(mockedToastError).toHaveBeenCalledWith(shown);
    },
  );

  it("leaves a mutation whose hook has an onError to that onError", async () => {
    const hookOnError = vi.fn();

    await fail(
      new QueryClient({ mutationCache: createMutationCache() }),
      new Error(PROBE),
      { onError: hookOnError },
    );

    expect(hookOnError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  // The app's own client, not only one built from the factory.
  it("is the net the app's own client runs", async () => {
    await fail(queryClient, new Error(PROBE));

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(PROBE);
  });
});
