import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import {
  QueryClientProvider,
  QueryObserver,
  type QueryClient,
  type QueryObserverResult,
} from "@tanstack/react-query";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import { nodeOptionsKey, type NodeOptions } from "../api/node-options-queries";
import { useNodeOptionsSave } from "./useNodeOptionsSave";

/**
 * The cases of useNodeOptionsSave that the cards cannot reach: a card's re-read
 * is TanStack's refetch, which resolves with a failed result rather than
 * rejecting, and its dialogs hold Save while a re-read is out and offer no empty
 * save. The rest of the hook is held to account through the cards
 * (NodeOptionsCard.test.tsx).
 */

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return { ...actual, apiClient: { put: vi.fn(), get: vi.fn() } };
});

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedPut = vi.mocked(apiClient.put);

const CLUSTER = "cccccccc-0000-0000-0000-000000000009";
const NODE = "pve-01";
// What the server says, and what the dialog says in its place.
const STALE =
  "The node's configuration changed since it was read — reload and try again.";
const CHANGED = "This node's configuration changed while this dialog was open.";

function stale(): ApiClientError {
  return new ApiClientError(409, { error: "conflict", message: STALE });
}

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  let reject: (reason: unknown) => void = () => undefined;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/**
 * The hook on a client, with `reread` as given. The default is one that throws,
 * which the card's never does; a test that wants a result of a real query takes
 * `observerReread`.
 */
function setup(
  reread: () => Promise<QueryObserverResult<NodeOptions>> = () =>
    Promise.reject(new Error("the re-read threw")),
  qc: QueryClient = createAppQueryClient(),
) {
  const onSaved = vi.fn();
  const opened: NodeOptions = { digest: "d1" };
  const hook = renderHook(
    () =>
      useNodeOptionsSave({
        clusterId: CLUSTER,
        nodeName: NODE,
        opened,
        subject: "options",
        reread,
        onSaved,
      }),
    {
      wrapper: ({ children }: { children: ReactNode }) => (
        <QueryClientProvider client={qc}>{children}</QueryClientProvider>
      ),
    },
  );
  return { ...hook, onSaved };
}

/**
 * A re-read that is a real observer's refetch, so the result it gives is a real
 * query result, with `isSuccess` and `data` as TanStack sets them. Each fetch is
 * held until `release` is called with what it should find, or `fail` with what
 * it should fail with.
 */
function observerReread() {
  const qc = createAppQueryClient();
  let current = deferred<NodeOptions>();
  const observer = new QueryObserver<NodeOptions>(qc, {
    queryKey: nodeOptionsKey(CLUSTER, NODE),
    queryFn: () => {
      current = deferred<NodeOptions>();
      return current.promise;
    },
    enabled: false,
  });
  return {
    qc,
    reread: () => observer.refetch(),
    release: (options: NodeOptions) => {
      current.resolve(options);
    },
    fail: (reason: unknown) => {
      current.reject(reason);
    },
  };
}

beforeEach(() => {
  mockedPut.mockReset();
});

describe("useNodeOptionsSave", () => {
  it("sends nothing for no changes", () => {
    const { result, onSaved } = setup();

    act(() => {
      result.current.save({});
    });

    expect(mockedPut).not.toHaveBeenCalled();
    expect(onSaved).not.toHaveBeenCalled();
    expect(result.current.pending).toBe(false);
  });

  it("pins the digest of the read it was opened with", async () => {
    mockedPut.mockResolvedValueOnce({ status: "ok" });
    const { result, onSaved } = setup();

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });

    await waitFor(() => {
      expect(onSaved).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls[0]?.[1]).toEqual({
      "startall-onboot-delay": 31,
      digest: "d1",
    });
  });

  it("calls a re-read that throws one that failed, and keeps its pin", async () => {
    mockedPut
      .mockRejectedValueOnce(stale())
      .mockResolvedValueOnce({ status: "ok" });
    const { result } = setup();

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });

    await waitFor(() => {
      expect(result.current.conflict).toBe("reread-failed");
    });
    // In the dialog's own words: the server's says to reload, and the dialog's
    // fields do not.
    expect(result.current.error).toBe(CHANGED);
    expect(result.current.error).not.toContain("reload");
    expect(result.current.latest).toBeNull();

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(mockedPut.mock.calls[1]?.[1]).toEqual({
      "startall-onboot-delay": 31,
      digest: "d1",
    });
  });

  it("pins to a re-read that succeeded, and to nothing else", async () => {
    mockedPut
      .mockRejectedValueOnce(stale())
      .mockResolvedValueOnce({ status: "ok" });
    const { reread, release } = observerReread();
    const { result } = setup(reread);

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });
    await waitFor(() => {
      expect(result.current.conflict).toBe("rereading");
    });
    // Nothing to show of a read that has not come back.
    expect(result.current.latest).toBeNull();
    release({ "startall-onboot-delay": 45, digest: "d2" });
    await waitFor(() => {
      expect(result.current.conflict).toBe("repinned");
    });
    // The read that was pinned to is the one handed back.
    expect(result.current.latest).toEqual({
      "startall-onboot-delay": 45,
      digest: "d2",
    });

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(mockedPut.mock.calls[1]?.[1]).toEqual({
      "startall-onboot-delay": 31,
      digest: "d2",
    });
  });

  it("keeps the latest read that succeeded when a later one fails", async () => {
    mockedPut.mockRejectedValueOnce(stale()).mockRejectedValueOnce(stale());
    const { reread, release, fail } = observerReread();
    const { result } = setup(reread);

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });
    await waitFor(() => {
      expect(result.current.conflict).toBe("rereading");
    });
    release({ "startall-onboot-delay": 45, digest: "d2" });
    await waitFor(() => {
      expect(result.current.conflict).toBe("repinned");
    });

    // Saved again, refused again, and this time the node cannot be read.
    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });
    await waitFor(() => {
      expect(result.current.conflict).toBe("rereading");
    });
    fail(new Error("the node is unreachable"));
    await waitFor(() => {
      expect(result.current.conflict).toBe("reread-failed");
    });

    // The node is no less different from what the dialog opened with.
    expect(result.current.latest).toEqual({
      "startall-onboot-delay": 45,
      digest: "d2",
    });
  });

  it("replaces it with a newer read that succeeds", async () => {
    mockedPut.mockRejectedValueOnce(stale()).mockRejectedValueOnce(stale());
    const { reread, release } = observerReread();
    const { result } = setup(reread);

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });
    await waitFor(() => {
      expect(result.current.conflict).toBe("rereading");
    });
    release({ "startall-onboot-delay": 45, digest: "d2" });
    await waitFor(() => {
      expect(result.current.conflict).toBe("repinned");
    });

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });
    await waitFor(() => {
      expect(result.current.conflict).toBe("rereading");
    });
    release({ "startall-onboot-delay": 90, digest: "d3" });
    await waitFor(() => {
      expect(result.current.latest?.digest).toBe("d3");
    });
  });

  it("shows any other failure in the server's own words, and reads nothing again", async () => {
    mockedPut.mockRejectedValueOnce(
      new ApiClientError(403, {
        error: "forbidden",
        message: "Proxmox API permission denied",
      }),
    );
    const { reread } = observerReread();
    const read = vi.fn(reread);
    const { result } = setup(read);

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });

    await waitFor(() => {
      expect(result.current.error).toBe("Proxmox API permission denied");
    });
    expect(result.current.conflict).toBeNull();
    expect(result.current.latest).toBeNull();
    expect(read).not.toHaveBeenCalled();
  });

  it("clears the last failure's message when it saves again", async () => {
    const held = deferred<unknown>();
    mockedPut
      .mockRejectedValueOnce(
        new ApiClientError(400, {
          error: "bad_request",
          message: "Parameter verification failed",
        }),
      )
      .mockReturnValueOnce(held.promise);
    const { result } = setup();

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });
    await waitFor(() => {
      expect(result.current.error).toBe("Parameter verification failed");
    });

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });

    // Gone as soon as the second goes out, not when it settles.
    expect(result.current.pending).toBe(true);
    expect(result.current.error).toBe("");
  });

  // The dialogs hold Save while a re-read is out, so for them this cannot come
  // about; the hook is what moves the pin, so it is the hook that has to hold.
  it("ignores the re-read of a save that was superseded before it came back", async () => {
    mockedPut
      .mockRejectedValueOnce(stale())
      // The second save never settles in this test.
      .mockReturnValueOnce(new Promise(() => undefined));
    const { reread, release } = observerReread();
    const { result } = setup(reread);

    act(() => {
      result.current.save({ "startall-onboot-delay": 31 });
    });
    await waitFor(() => {
      expect(result.current.conflict).toBe("rereading");
    });
    // Saved again with the first re-read still out.
    act(() => {
      result.current.save({ "startall-onboot-delay": 32 });
    });
    expect(result.current.conflict).toBeNull();
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });

    release({ digest: "d2" });
    // Let the re-read land, and React draw whatever it set.
    await act(async () => {
      await new Promise<void>((resolve) => {
        setTimeout(resolve, 0);
      });
    });

    // It belonged to the first save: no note for it over the second's.
    expect(result.current.conflict).toBeNull();
    expect(result.current.pending).toBe(true);
  });
});
