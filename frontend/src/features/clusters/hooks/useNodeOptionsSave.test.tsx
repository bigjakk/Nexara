import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import {
  QueryClientProvider,
  QueryObserver,
  type QueryClient,
  type QueryObserverResult,
} from "@tanstack/react-query";
import { toast } from "sonner";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import {
  SESSION_ENDS,
  settle,
  signInAsAdmin,
  signOutForGood,
} from "@/test/late-toast-sessions";
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
const mockedToastError = vi.mocked(toast.error);

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

// A save that is answered after the session it was made in has ended — a
// sign-out, an expiry, another user signing in — does nothing at all: its toast
// would be shown to whoever is signed in by then, with the node's name and the
// server's words in it, and a re-read would be a request nobody who is here asked
// for. Each case is paired with the same answer in a session that goes on.
describe("a save that settles after its session ended", () => {
  const DENIED = "Proxmox API permission denied";
  const FAILED = `Saving the options of ${NODE} failed: ${DENIED}`;

  function forbidden(): ApiClientError {
    return new ApiClientError(403, { error: "forbidden", message: DENIED });
  }

  beforeEach(() => {
    mockedToastError.mockReset();
    signInAsAdmin();
  });

  afterEach(() => {
    signOutForGood();
  });

  /** The hook, with a save of the delay sent and held. */
  async function savingHeld(
    reread?: () => Promise<QueryObserverResult<NodeOptions>>,
  ) {
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const view = setup(reread);
    act(() => {
      view.result.current.save({ "startall-onboot-delay": 31 });
    });
    // Sent in the session that is current now: what ends it comes after.
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    return { ...view, held };
  }

  /** A re-read that the test holds, and settles as it says. */
  function heldReread() {
    const read = deferred<QueryObserverResult<NodeOptions>>();
    return { ...read, reread: vi.fn(() => read.promise) };
  }

  it("control: toasts a failure that lands after the dialog was dismissed, naming the node, once", async () => {
    const { held, unmount, onSaved } = await savingHeld();

    unmount();
    held.reject(forbidden());
    await settle();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(FAILED);
    expect(onSaved).not.toHaveBeenCalled();
  });

  it.each(SESSION_ENDS)(
    "toasts nothing for a failure that lands after %s, with the dialog gone",
    async (_, end) => {
      const { held, unmount, onSaved } = await savingHeld();

      unmount();
      end();
      held.reject(forbidden());
      await settle();

      expect(mockedToastError).not.toHaveBeenCalled();
      expect(onSaved).not.toHaveBeenCalled();
    },
  );

  it("control: shows a failure in the dialog while the session goes on", async () => {
    const { held, result } = await savingHeld();

    held.reject(forbidden());
    await waitFor(() => {
      expect(result.current.error).toBe(DENIED);
    });

    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each(SESSION_ENDS)(
    "puts no failure in a dialog that is somehow still there after %s",
    async (_, end) => {
      const { held, result } = await savingHeld();

      end();
      held.reject(forbidden());
      // The save has settled, and so has what answers it: TanStack tells the
      // hook so after the continuations of the promise have run.
      await waitFor(() => {
        expect(result.current.pending).toBe(false);
      });
      await settle();

      // `live` is true here, and the session still wins.
      expect(result.current.error).toBe("");
      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );

  // The flag that holds Save shut while a save is out is released whatever the
  // session: a dialog that is somehow still there must not be left unable to save.
  const ANSWERS: [
    name: string,
    answer: (held: ReturnType<typeof deferred<unknown>>) => void,
  ][] = [
    [
      "a failure",
      (held) => {
        held.reject(forbidden());
      },
    ],
    [
      "a success",
      (held) => {
        held.resolve({ status: "ok" });
      },
    ],
  ];

  it.each(ANSWERS)(
    "does not hold Save shut once the session has ended and %s has come: the dialog can save again",
    async (_, answer) => {
      const { held, result } = await savingHeld();

      signOutForGood();
      answer(held);
      await settle();
      signInAsAdmin();
      mockedPut.mockResolvedValueOnce({ status: "ok" });
      act(() => {
        result.current.save({ "startall-onboot-delay": 32 });
      });

      await waitFor(() => {
        expect(mockedPut).toHaveBeenCalledTimes(2);
      });
    },
  );

  it("control: reads the node again after a stale digest while the session goes on", async () => {
    const reading = heldReread();
    const { held, result } = await savingHeld(reading.reread);

    held.reject(stale());
    await waitFor(() => {
      expect(result.current.conflict).toBe("rereading");
    });

    expect(reading.reread).toHaveBeenCalledTimes(1);
  });

  it.each(SESSION_ENDS)(
    "reads nothing again after a stale digest that lands after %s",
    async (_, end) => {
      const reading = heldReread();
      const { held, result } = await savingHeld(reading.reread);

      end();
      held.reject(stale());
      await settle();

      expect(reading.reread).not.toHaveBeenCalled();
      expect(result.current.conflict).toBeNull();
      expect(result.current.error).toBe("");
    },
  );

  it("control: calls a save made, and says so, while the session goes on", async () => {
    const { held, onSaved } = await savingHeld();

    held.resolve({ status: "ok" });
    await waitFor(() => {
      expect(onSaved).toHaveBeenCalledTimes(1);
    });
  });

  it.each(SESSION_ENDS)(
    "does not call a save that was answered after %s one that was made",
    async (_, end) => {
      const { held, onSaved } = await savingHeld();

      end();
      held.resolve({ status: "ok" });
      await settle();

      expect(onSaved).not.toHaveBeenCalled();
    },
  );

  // The read is made in a session that is current and answered in one that is
  // not: what it found is not for the dialog of whoever is signed in now.
  it("control: pins to a re-read that answers while the session goes on", async () => {
    const reading = heldReread();
    const { held, result } = await savingHeld(reading.reread);
    held.reject(stale());
    await waitFor(() => {
      expect(result.current.conflict).toBe("rereading");
    });

    reading.resolve({
      isSuccess: true,
      data: { digest: "d2" },
    } as QueryObserverResult<NodeOptions>);

    await waitFor(() => {
      expect(result.current.conflict).toBe("repinned");
    });
    expect(result.current.latest).toEqual({ digest: "d2" });
  });

  it.each(SESSION_ENDS)(
    "pins to nothing a re-read that answers after %s",
    async (_, end) => {
      const reading = heldReread();
      const { held, result } = await savingHeld(reading.reread);
      held.reject(stale());
      await waitFor(() => {
        expect(result.current.conflict).toBe("rereading");
      });

      end();
      reading.resolve({
        isSuccess: true,
        data: { digest: "d2" },
      } as QueryObserverResult<NodeOptions>);
      await settle();

      expect(result.current.conflict).toBe("rereading");
      expect(result.current.latest).toBeNull();
    },
  );

  it("control: calls a re-read that fails one that failed while the session goes on", async () => {
    const reading = heldReread();
    const { held, result } = await savingHeld(reading.reread);
    held.reject(stale());
    await waitFor(() => {
      expect(result.current.conflict).toBe("rereading");
    });

    reading.reject(new Error("the re-read threw"));

    await waitFor(() => {
      expect(result.current.conflict).toBe("reread-failed");
    });
  });

  it.each(SESSION_ENDS)(
    "calls a re-read that fails after %s nothing",
    async (_, end) => {
      const reading = heldReread();
      const { held, result } = await savingHeld(reading.reread);
      held.reject(stale());
      await waitFor(() => {
        expect(result.current.conflict).toBe("rereading");
      });

      end();
      reading.reject(new Error("the re-read threw"));
      await settle();

      expect(result.current.conflict).toBe("rereading");
    },
  );
});
