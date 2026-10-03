import { afterEach, beforeEach, describe, it, expect, vi } from "vitest";
import {
  hydrate,
  MutationObserver as QueryMutationObserver,
  QueryClient,
} from "@tanstack/react-query";
import { toast } from "sonner";
import {
  createMutationCache,
  retryUnlessClientError,
  queryClient,
} from "./query-client";
import {
  apiClient,
  ApiClientError,
  clearTokens,
  currentSessionEpoch,
  StaleSessionError,
  storeTokens,
} from "@/lib/api-client";
import { apiPath, PathSegmentError } from "@/lib/api-path";
import {
  ADMIN,
  authResponse,
  deferred,
  installFakeServer,
  json,
  VIEWER,
} from "@/test/fake-server";

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

// The net reports a failure to whoever is signed in when it lands, and a
// mutation's answer can land after the session that submitted it has ended: a
// toast raised after a sign-out, an expiry or another user signing in is shown to
// whoever is signed in by then (what is dismissed when a session ends and when
// the next begins is only what exists at the time). Each mutation is tied to the
// session it was submitted in, and what failed for one that has ended is dropped.
describe("the app-wide mutation-error net and the session", () => {
  const PROBE = "PROBE-NOT-A-REAL-FAILURE";
  const mockedToastError = vi.mocked(toast.error);

  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    clearTokens();
    storeTokens(authResponse(ADMIN));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    clearTokens();
    localStorage.clear();
  });

  /**
   * Submits a mutation on `client` that fails with whatever `fail` is given, once
   * the test says so. `settled` is how it ended: the net has run by then, so what
   * it did not toast was not toasted because it chose not to, and not because the
   * failure had not come yet.
   */
  function submit(client: QueryClient) {
    const held = deferred<unknown>();
    const observer = new QueryMutationObserver(client, {
      mutationFn: () => held.promise,
    });
    const settled = observer.mutate(undefined).then(
      () => "succeeded",
      () => "failed",
    );
    return {
      fail: (failure: unknown) => {
        held.reject(failure);
        return settled;
      },
    };
  }

  const ENDS: [name: string, end: () => void][] = [
    [
      "a sign-out",
      () => {
        clearTokens();
      },
    ],
    [
      "another user signing in",
      () => {
        storeTokens(authResponse(VIEWER));
      },
    ],
    [
      "the same user signing in again",
      () => {
        clearTokens();
        storeTokens(authResponse(ADMIN));
      },
    ],
  ];

  it("control: toasts a failure that settles in the session it was submitted in, once", async () => {
    const mutation = submit(
      new QueryClient({ mutationCache: createMutationCache() }),
    );

    expect(await mutation.fail(new Error(PROBE))).toBe("failed");

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(PROBE);
  });

  it.each(ENDS)(
    "toasts nothing for a failure that settles after %s",
    async (_, end) => {
      const mutation = submit(
        new QueryClient({ mutationCache: createMutationCache() }),
      );

      end();

      expect(await mutation.fail(new Error(PROBE))).toBe("failed");
      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );

  // A refresh that names the user already signed in rotates their token, and is
  // not the end of anything: the session, and so the epoch, are the same after it.
  // A request that waits on one and then fails is a failure of the session still
  // current, and is reported once — not dropped as if the rotation had ended it.
  it("control: toasts, once, the failure of a request that waited on a rotation of the signed-in user's token", async () => {
    const server = installFakeServer();
    // A token that is about to expire, so the request refreshes it first.
    storeTokens(authResponse(ADMIN, { expiresIn: 30 }));
    const epoch = currentSessionEpoch();
    server.routes["POST /api/v1/auth/refresh"] = () =>
      json(authResponse(ADMIN));
    server.routes["PUT /api/v1/probe"] = () =>
      json({ error: "bad_gateway", message: PROBE }, 502);
    const client = new QueryClient({ mutationCache: createMutationCache() });
    const observer = new QueryMutationObserver(client, {
      mutationFn: () => apiClient.put(apiPath`/api/v1/probe`, {}),
    });

    await observer.mutate(undefined).catch(() => undefined);

    // The premise: the token was rotated, and the session was not touched.
    expect(server.times("POST /api/v1/auth/refresh")).toBe(1);
    expect(currentSessionEpoch()).toBe(epoch);
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(PROBE);
  });

  it("drops the 'Session expired' a request fails with when its session ends under it", async () => {
    const mutation = submit(
      new QueryClient({ mutationCache: createMutationCache() }),
    );

    // How a request that meets its session's expiry fails: after the sign-out
    // that the refresh failing caused (api-client's request()).
    clearTokens();
    const expired = new ApiClientError(401, {
      error: "unauthorized",
      message: "Session expired",
    });

    expect(await mutation.fail(expired)).toBe("failed");
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("judges a mutation by the session it was submitted in, not by the one the cache was made in", async () => {
    const client = new QueryClient({ mutationCache: createMutationCache() });
    // The session changes hands after the cache exists and before the submit.
    storeTokens(authResponse(VIEWER));
    const mutation = submit(client);

    expect(await mutation.fail(new Error(PROBE))).toBe("failed");

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(PROBE);
  });

  it("judges each mutation by its own session: one submitted before a sign-out is dropped and one after it is not", async () => {
    const client = new QueryClient({ mutationCache: createMutationCache() });
    const before = submit(client);
    clearTokens();
    storeTokens(authResponse(VIEWER));
    const after = submit(client);

    expect(await before.fail(new Error("before"))).toBe("failed");
    expect(await after.fail(new Error("after"))).toBe("failed");

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith("after");
  });

  it("never toasts a StaleSessionError, in the session it was submitted in either", async () => {
    const mutation = submit(
      new QueryClient({ mutationCache: createMutationCache() }),
    );

    expect(await mutation.fail(new StaleSessionError())).toBe("failed");

    // The same failure as the control above, with another error: it is the
    // error that is kept out, not the session.
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("toasts nothing for a mutation it never saw submitted, which is how a restored one fails", async () => {
    const client = new QueryClient({ mutationCache: createMutationCache() });
    // What a client that restores its mutations does: builds one from a
    // dehydrated, paused state, which skips onMutate when it runs. Nothing in the
    // app does, and nothing says whose such a mutation was.
    client.setMutationDefaults(["restored"], {
      mutationFn: () => Promise.reject(new Error(PROBE)),
    });
    hydrate(client, {
      queries: [],
      mutations: [
        {
          mutationKey: ["restored"],
          state: {
            context: undefined,
            data: undefined,
            error: null,
            failureCount: 0,
            failureReason: null,
            isPaused: true,
            status: "pending",
            variables: undefined,
            submittedAt: 0,
          },
        },
      ],
    });
    const restored = client.getMutationCache().getAll();
    expect(restored).toHaveLength(1);

    await client.resumePausedMutations();

    // It ran and failed: the net had it in front of it and left it alone.
    expect(restored[0]?.state.status).toBe("error");
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("keeps to the session on the app's own client too", async () => {
    const mutation = submit(queryClient);

    clearTokens();

    expect(await mutation.fail(new Error(PROBE))).toBe("failed");
    expect(mockedToastError).not.toHaveBeenCalled();
    queryClient.clear();
  });

  // What auth-store does when a session ends (stores/session-reset.ts): the cache
  // is emptied, which takes a mutation in flight out of it and does not stop it.
  // Which session it was submitted in is not something the cache holds.
  it("control: toasts a failure that settles after the cache was cleared, in a session that goes on", async () => {
    const client = new QueryClient({ mutationCache: createMutationCache() });
    const mutation = submit(client);

    await client.cancelQueries();
    client.clear();
    expect(client.getMutationCache().getAll()).toEqual([]);

    expect(await mutation.fail(new Error(PROBE))).toBe("failed");
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(PROBE);
  });

  it.each(ENDS)(
    "toasts nothing for a failure that settles after %s and the cache was cleared",
    async (_, end) => {
      const client = new QueryClient({ mutationCache: createMutationCache() });
      const mutation = submit(client);

      end();
      await client.cancelQueries();
      client.clear();
      expect(client.getMutationCache().getAll()).toEqual([]);

      expect(await mutation.fail(new Error(PROBE))).toBe("failed");
      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});
