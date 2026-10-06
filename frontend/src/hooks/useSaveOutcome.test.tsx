import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { startTransition, useEffect, useLayoutEffect, useState } from "react";
import { act, render, renderHook } from "@testing-library/react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";

import { ApiClientError, clearTokens, storeTokens } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import { VIEWER, authResponse, deferred, sleep } from "@/test/fake-server";
import {
  DENIED,
  SESSION_ENDINGS,
  denied,
  flushInAct,
  signIn,
  signOutForGood,
} from "@/test/save-outcome-kit";
import { createWrapper } from "@/test/test-utils";
import { useSaveOutcome, type SaveOutcome } from "./useSaveOutcome";

/**
 * The outcome matrix of useSaveOutcome: where a save's answer goes, by what is on
 * screen when it comes and what the session has done since the save went out.
 * The dialogs and sections that use it prove only what they pass it. Driven
 * through a real mutation with the opt-out the hooks it serves carry, on the
 * app's own kind of client, whose global error net would otherwise toast beside
 * it, and without which "toasts nothing" proves nothing.
 */

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedToastError = vi.mocked(toast.error);

const SAVING = "Saving thing-01";
const CONNECTION_FAILED =
  "The request failed — check your connection and try again.";

function withAppClient() {
  return createWrapper({ client: createAppQueryClient(), router: false });
}

/** A mutation that opted out of the global toast, as the hooks this serves do. */
function useOptedOutSave() {
  return useMutation({
    mutationFn: (held: Promise<string>) => held,
    onError: () => undefined,
  });
}

type Shown = string | number | boolean | null;

/**
 * The hook the way a dialog uses it: `send` starts a save whose outcome stays
 * unsettled until the test settles `held`. `rerender` changes what is on screen,
 * `unmount` takes the dialog away, `extra` is whatever else a caller hands
 * `settle`.
 */
function renderSave(
  shown: Shown = true,
  extra: Partial<SaveOutcome<string>> = {},
) {
  const view = renderHook(
    ({ shown: on }: { shown: Shown }) => {
      const settle = useSaveOutcome(on);
      const { mutateAsync } = useOptedOutSave();
      return { settle, mutateAsync };
    },
    { wrapper: withAppClient(), initialProps: { shown } },
  );
  const handlers = {
    onSuccess: vi.fn(),
    onError: vi.fn(),
    onLateSuccess: vi.fn(),
  };
  const held = deferred<string>();
  const send = async () => {
    await act(async () => {
      const { settle, mutateAsync } = view.result.current;
      settle(mutateAsync(held.promise), {
        action: SAVING,
        ...handlers,
        ...extra,
      });
      await sleep(0);
    });
  };
  return { ...view, handlers, held, send };
}

/** Everything a settled save did, by handler: the calls each got, and the toasts raised. */
function didOf(h: ReturnType<typeof renderSave>) {
  return {
    onError: h.handlers.onError.mock.calls,
    onSuccess: h.handlers.onSuccess.mock.calls,
    onLateSuccess: h.handlers.onLateSuccess.mock.calls,
    toasts: mockedToastError.mock.calls,
  };
}

const NOTHING = { onError: [], onSuccess: [], onLateSuccess: [], toasts: [] };

beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
  signIn();
});

afterEach(() => {
  signOutForGood();
});

describe("the app's own client", () => {
  it("control: toasts a failed mutation with no onError of its own, which the opt-out of the hooks here avoids", async () => {
    const PROBE = "PROBE-NOT-A-REAL-FAILURE";
    const { result } = renderHook(
      () => useMutation({ mutationFn: () => Promise.reject(new Error(PROBE)) }),
      { wrapper: withAppClient() },
    );

    await act(async () => {
      await result.current.mutateAsync().catch(() => undefined);
    });

    expect(mockedToastError.mock.calls).toEqual([[PROBE]]);
  });
});

/**
 * Where the save's sender is when the answer comes. The component hosts one
 * dialog after another and tells them apart by what it passes as `shown`: an
 * open flag, or the identity of what is open (null for none), so a dialog closed
 * under a save, one opened after it and one put in its place without a close
 * between are all a different dialog from the one the save belongs to. `open` is
 * what a late success is told about the dialogs of the component: its draft may
 * be put away only when none is showing it, and a dialog opened since is.
 */
const WHERE: {
  name: string;
  start: Shown;
  changes: Shown[] | "unmount";
  onScreen: boolean;
  open: boolean;
}[] = [
  {
    name: "still on screen",
    start: true,
    changes: [],
    onScreen: true,
    open: true,
  },
  {
    name: "still on screen after other renders",
    start: true,
    changes: [true, true],
    onScreen: true,
    open: true,
  },
  {
    name: "gone",
    start: true,
    changes: "unmount",
    onScreen: false,
    open: false,
  },
  {
    name: "closed",
    start: true,
    changes: [false],
    onScreen: false,
    open: false,
  },
  {
    name: "closed and opened again",
    start: true,
    changes: [false, true],
    onScreen: false,
    open: true,
  },
  {
    name: "closed (a form)",
    start: "form-a",
    changes: [null],
    onScreen: false,
    open: false,
  },
  {
    name: "closed and another form opened",
    start: "form-a",
    changes: [null, "form-b"],
    onScreen: false,
    open: true,
  },
  {
    name: "replaced by another form, with no close between",
    start: "form-a",
    changes: ["form-b"],
    onScreen: false,
    open: true,
  },
];

describe.each(WHERE)("a save that settles with its sender $name", (where) => {
  async function sentThenMoved() {
    const h = renderSave(where.start);
    await h.send();
    if (where.changes === "unmount") {
      h.unmount();
    } else {
      for (const shown of where.changes) h.rerender({ shown });
    }
    return h;
  }

  it("hands a failure to onError, or toasts it once, naming the object and giving the server's words", async () => {
    const h = await sentThenMoved();
    const failure = denied();
    h.held.reject(failure);
    await flushInAct();

    expect(didOf(h)).toEqual({
      ...NOTHING,
      ...(where.onScreen
        ? { onError: [[failure]] }
        : { toasts: [[`${SAVING} failed: ${DENIED}`]] }),
    });
  });

  it("hands a success to onSuccess, or to onLateSuccess, which is told whether a dialog is open", async () => {
    const h = await sentThenMoved();
    h.held.resolve("saved");
    await flushInAct();

    expect(didOf(h)).toEqual({
      ...NOTHING,
      ...(where.onScreen
        ? { onSuccess: [["saved"]] }
        : { onLateSuccess: [["saved", { open: where.open }]] }),
    });
  });
});

describe("a save that settles after its session ended", () => {
  // The protected outlet is keyed by user, so a sign-out or a change of hands
  // unmounts the dialog. Both cases are covered anyway: the component being gone
  // is not what keeps a toast naming the previous user's objects out of the next
  // user's Toaster. The same answers in a session that goes on are the rows
  // above.
  describe.each(SESSION_ENDINGS)("after %s", (_, end) => {
    it.each([
      ["gone", "fails"],
      ["gone", "succeeds"],
      ["still mounted", "fails"],
      ["still mounted", "succeeds"],
    ])(
      "does nothing at all when its component is %s and it %s",
      async (life, answer) => {
        const h = renderSave();
        await h.send();
        if (life === "gone") h.unmount();

        end();
        if (answer === "fails") h.held.reject(denied());
        else h.held.resolve("saved");
        await flushInAct();

        expect(didOf(h)).toEqual(NOTHING);
      },
    );
  });

  it("is the session the save was sent in that counts, not the one at the time of the failure", async () => {
    // Sent in the second session, settled in it: the earlier sign-out is not this save's.
    clearTokens();
    storeTokens(authResponse(VIEWER));
    const h = renderSave();
    await h.send();
    h.unmount();

    h.held.reject(denied());
    await flushInAct();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
  });
});

describe("saves sent from different dialogs of one component", () => {
  it("belongs a save sent from the second dialog to the second, not the first", async () => {
    const h = renderSave(true);
    h.rerender({ shown: false });
    h.rerender({ shown: true });
    await h.send();

    h.held.reject(denied());
    await flushInAct();

    expect(h.handlers.onError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("keeps each with its own dialog when both are out: the older one's failure is a toast, the newer one's the dialog's", async () => {
    const h = renderSave(true);
    await h.send();
    h.rerender({ shown: false });
    h.rerender({ shown: true });
    const newer = deferred<string>();
    const onError = vi.fn();
    await act(async () => {
      const { settle, mutateAsync } = h.result.current;
      settle(mutateAsync(newer.promise), {
        action: "Saving thing-02",
        onError,
      });
      await sleep(0);
    });

    h.held.reject(denied());
    await flushInAct();
    expect(mockedToastError.mock.calls).toEqual([
      [`${SAVING} failed: ${DENIED}`],
    ]);
    expect(onError).not.toHaveBeenCalled();

    const failure = new ApiClientError(403, {
      error: "forbidden",
      message: "Permission check failed",
    });
    newer.reject(failure);
    await flushInAct();
    expect(onError.mock.calls).toEqual([[failure]]);
    expect(h.handlers.onError).not.toHaveBeenCalled();
    expect(mockedToastError).toHaveBeenCalledTimes(1);
  });
});

describe("what a failure nobody is looking at says when the caller words it", () => {
  const REFUSED = "Saving thing-01 was refused: do it again from its dialog.";

  it.each([
    [
      "the dialog was unmounted",
      (h: ReturnType<typeof renderSave>) => {
        h.unmount();
      },
    ],
    [
      "the dialog was closed rather than unmounted",
      (h: ReturnType<typeof renderSave>) => {
        h.rerender({ shown: false });
      },
    ],
  ])(
    "toasts the whole text lateFailure gives, in place of the default, and hands it the error, when %s",
    async (_, leave) => {
      const lateFailure = vi.fn(() => REFUSED);
      const h = renderSave(true, { lateFailure });
      await h.send();
      leave(h);

      const failure = denied();
      h.held.reject(failure);
      await flushInAct();

      expect(mockedToastError.mock.calls).toEqual([[REFUSED]]);
      expect(lateFailure.mock.calls).toEqual([[failure]]);
    },
  );

  it("keeps the default text when lateFailure has nothing to say about the error", async () => {
    const lateFailure = vi.fn(() => undefined);
    const h = renderSave(true, { lateFailure });
    await h.send();
    h.unmount();

    h.held.reject(denied());
    await flushInAct();

    expect(lateFailure).toHaveBeenCalledTimes(1);
    expect(mockedToastError.mock.calls).toEqual([
      [`${SAVING} failed: ${DENIED}`],
    ]);
  });

  it("does not ask about a failure that what sent the save is there to show", async () => {
    const lateFailure = vi.fn(() => REFUSED);
    const h = renderSave(true, { lateFailure });
    await h.send();

    h.held.reject(denied());
    await flushInAct();

    expect(h.handlers.onError).toHaveBeenCalledTimes(1);
    expect(lateFailure).not.toHaveBeenCalled();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it.each(SESSION_ENDINGS)(
    "does not ask, and toasts nothing, after %s",
    async (_, end) => {
      const lateFailure = vi.fn(() => REFUSED);
      const h = renderSave(true, { lateFailure });
      await h.send();
      h.unmount();

      end();
      h.held.reject(denied());
      await flushInAct();

      expect(lateFailure).not.toHaveBeenCalled();
      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

describe("what a late failure says", () => {
  const FAILURES: [name: string, failure: unknown, said: string][] = [
    ["a refusal from the server", denied(), DENIED],
    [
      "a gateway answer with no message",
      new ApiClientError(502, { error: "bad_gateway", message: "" }),
      "HTTP 502",
    ],
    [
      "an error of our own",
      new Error("not a path segment"),
      "not a path segment",
    ],
    // A TypeError is how a dropped connection rejects fetch, and its message
    // is the browser's own wording.
    [
      "a dropped connection",
      new TypeError("Failed to fetch"),
      CONNECTION_FAILED,
    ],
    ["something that is not an Error", "boom", CONNECTION_FAILED],
  ];

  it.each(FAILURES)(
    "puts %s after the name of the object",
    async (_, failure, said) => {
      const h = renderSave();
      await h.send();
      h.unmount();

      h.held.reject(failure);
      await flushInAct();

      expect(mockedToastError.mock.calls).toEqual([
        [`${SAVING} failed: ${said}`],
      ]);
    },
  );
});

/**
 * Runs `body` with React's act environment off, so that what it renders goes
 * through the real scheduler. Inside act, React flushes a commit's passive
 * effects in the same synchronous stretch as the commit, and there is no gap
 * between the two to settle a request in.
 */
async function outsideAct(body: () => Promise<void>): Promise<void> {
  const env = globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT: boolean };
  const before = env.IS_REACT_ACT_ENVIRONMENT;
  env.IS_REACT_ACT_ENVIRONMENT = false;
  try {
    await body();
  } finally {
    env.IS_REACT_ACT_ENVIRONMENT = before;
  }
}

/**
 * Both facts the hook keeps — that the component is mounted and which dialog is
 * on screen — are read when a save settles, and a save can settle in the gap
 * between the commit that changed them and the passive effects of that commit,
 * which React runs a task later after a transition (React Router's navigations
 * are one). A fact kept by a passive effect is stale in that gap. The gap
 * cannot be reached from inside act, so these run outside it.
 */
describe("a save that settles in the gap between a commit and its passive effects", () => {
  const seen: { passive: boolean; atToast: boolean | null } = {
    passive: false,
    atToast: null,
  };

  beforeEach(() => {
    seen.passive = false;
    seen.atToast = null;
    mockedToastError.mockImplementation(() => {
      seen.atToast = seen.passive;
      return "toast-id";
    });
  });

  /** What replaces the component once it is left: it reports the commit's two moments. */
  function Left({
    onLayout,
    onPassive,
  }: {
    onLayout: () => void;
    onPassive: () => void;
  }) {
    useLayoutEffect(() => {
      onLayout();
    }, [onLayout]);
    useEffect(() => {
      onPassive();
    }, [onPassive]);
    return <p>Left</p>;
  }

  function Saver({
    held,
    onReady,
    onError,
  }: {
    held: Promise<string>;
    onReady: (send: () => void) => void;
    onError: () => void;
  }) {
    const settle = useSaveOutcome();
    const { mutateAsync } = useOptedOutSave();
    useEffect(() => {
      onReady(() => {
        settle(mutateAsync(held), { action: SAVING, onError });
      });
    }, [onReady, settle, mutateAsync, held, onError]);
    return <p>Saving</p>;
  }

  it("toasts the failure of a save whose component was just removed", async () => {
    const held = deferred<string>();
    const passiveRan = deferred<null>();
    const handle: { send?: () => void; leave?: () => void } = {};
    const onError = vi.fn();
    function Page() {
      const [here, setHere] = useState(true);
      useEffect(() => {
        handle.leave = () => {
          startTransition(() => {
            setHere(false);
          });
        };
      }, []);
      return here ? (
        <Saver
          held={held.promise}
          onError={onError}
          onReady={(send) => {
            handle.send = send;
          }}
        />
      ) : (
        <Left
          onLayout={() => {
            // Settled in the commit itself, so that the handlers of the
            // rejection run before the task that flushes the passive effects.
            held.reject(denied());
          }}
          onPassive={() => {
            seen.passive = true;
            passiveRan.resolve(null);
          }}
        />
      );
    }
    const Wrapper = withAppClient();
    render(
      <Wrapper>
        <Page />
      </Wrapper>,
    );
    await act(async () => {
      handle.send?.();
      await sleep(0);
    });

    await outsideAct(async () => {
      handle.leave?.();
      await passiveRan.promise;
    });
    await flushInAct();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    // In the gap, not merely some time after it.
    expect(seen.atToast).toBe(false);
    expect(onError).not.toHaveBeenCalled();
  });

  it("toasts the failure of a save whose dialog was just closed", async () => {
    const held = deferred<string>();
    const passiveRan = deferred<null>();
    const onError = vi.fn();
    const handle: { send?: () => void; close?: () => void } = {};
    /** Reports the moments of the commit that closes the dialog. */
    function Probe({ open }: { open: boolean }) {
      useLayoutEffect(() => {
        if (!open) held.reject(denied());
      }, [open]);
      useEffect(() => {
        if (!open) {
          seen.passive = true;
          passiveRan.resolve(null);
        }
      }, [open]);
      return null;
    }
    function Host() {
      const [open, setOpen] = useState(true);
      const settle = useSaveOutcome(open);
      const { mutateAsync } = useOptedOutSave();
      useEffect(() => {
        handle.send = () => {
          settle(mutateAsync(held.promise), { action: SAVING, onError });
        };
        handle.close = () => {
          startTransition(() => {
            setOpen(false);
          });
        };
      }, [settle, mutateAsync]);
      return <Probe open={open} />;
    }
    const Wrapper = withAppClient();
    render(
      <Wrapper>
        <Host />
      </Wrapper>,
    );
    await act(async () => {
      handle.send?.();
      await sleep(0);
    });

    await outsideAct(async () => {
      handle.close?.();
      await passiveRan.promise;
    });
    await flushInAct();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(seen.atToast).toBe(false);
    expect(onError).not.toHaveBeenCalled();
  });
});
