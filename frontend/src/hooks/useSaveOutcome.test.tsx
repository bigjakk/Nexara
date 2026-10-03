import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  startTransition,
  useEffect,
  useLayoutEffect,
  useState,
  type ReactNode,
} from "react";
import { act, render, renderHook } from "@testing-library/react";
import { QueryClientProvider, useMutation } from "@tanstack/react-query";
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
import { useSaveOutcome, type SaveOutcome } from "./useSaveOutcome";

/**
 * A dialog that opted out of the global error toast has to report a failure
 * itself, and one that settles after the dialog is gone has nowhere to: this is
 * the hook that decides who hears of it. These tests drive it through a real
 * mutation that carries the same opt-out the access, LDAP, OIDC and cluster
 * hooks do, on the app's own kind of client (test/app-query-client.ts), so that
 * "once" means once: the global net would otherwise toast beside it, and a
 * "nothing was toasted" assertion on a client without the net proves nothing.
 */

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedToastError = vi.mocked(toast.error);

const SAVING = "Saving thing-01";
const CONNECTION_FAILED =
  "The request failed — check your connection and try again.";

function withAppClient() {
  const qc = createAppQueryClient();
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
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
 * unsettled until the test settles `held`. `rerender` changes what is on
 * screen, `unmount` takes the dialog away. `extra` is whatever else a caller
 * hands `settle`.
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

beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
});

afterEach(() => {
  signOutForGood();
});

describe("a save that settles while what sent it is on screen", () => {
  it("hands a failure to onError, and toasts nothing", async () => {
    const h = renderSave();
    await h.send();

    const failure = denied();
    h.held.reject(failure);
    await flushInAct();

    expect(h.handlers.onError).toHaveBeenCalledTimes(1);
    expect(h.handlers.onError).toHaveBeenCalledWith(failure);
    expect(h.handlers.onSuccess).not.toHaveBeenCalled();
    expect(h.handlers.onLateSuccess).not.toHaveBeenCalled();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("hands a success to onSuccess, with its result, and toasts nothing", async () => {
    const h = renderSave();
    await h.send();

    h.held.resolve("saved");
    await flushInAct();

    expect(h.handlers.onSuccess).toHaveBeenCalledTimes(1);
    expect(h.handlers.onSuccess).toHaveBeenCalledWith("saved");
    expect(h.handlers.onError).not.toHaveBeenCalled();
    expect(h.handlers.onLateSuccess).not.toHaveBeenCalled();
    expect(mockedToastError).not.toHaveBeenCalled();
  });
});

describe("a save that settles after the component is gone", () => {
  it("toasts a failure once, naming the object and giving the server's words, and calls nothing", async () => {
    const h = renderSave();
    await h.send();
    h.unmount();

    h.held.reject(denied());
    await flushInAct();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(
      `${SAVING} failed: ${DENIED}`,
    );
    expect(h.handlers.onError).not.toHaveBeenCalled();
    expect(h.handlers.onSuccess).not.toHaveBeenCalled();
  });

  it("does not call onSuccess for a success, and toasts nothing", async () => {
    const h = renderSave();
    await h.send();
    h.unmount();

    h.held.resolve("saved");
    await flushInAct();

    expect(h.handlers.onSuccess).not.toHaveBeenCalled();
    expect(h.handlers.onError).not.toHaveBeenCalled();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("calls onLateSuccess, and not onSuccess, with the result of a success", async () => {
    const h = renderSave();
    await h.send();
    h.unmount();

    h.held.resolve("saved");
    await flushInAct();

    expect(h.handlers.onLateSuccess).toHaveBeenCalledTimes(1);
    // With the component gone, there is no dialog of its open.
    expect(h.handlers.onLateSuccess).toHaveBeenCalledWith("saved", {
      open: false,
    });
    expect(h.handlers.onSuccess).not.toHaveBeenCalled();
  });

  it("does not call onLateSuccess for a failure: that is a toast", async () => {
    const h = renderSave();
    await h.send();
    h.unmount();

    h.held.reject(denied());
    await flushInAct();

    expect(h.handlers.onLateSuccess).not.toHaveBeenCalled();
    expect(mockedToastError).toHaveBeenCalledTimes(1);
  });
});

describe("a save that settles after another dialog took the place of the one that sent it", () => {
  // The component hosts one dialog after another and tells them apart by its
  // open flag, so a dialog closed under a save, and one opened after it, are
  // both a different dialog from the one the save belongs to.
  const CHANGES: [name: string, changes: boolean[]][] = [
    ["closed", [false]],
    ["closed and opened again", [false, true]],
  ];

  it.each(CHANGES)(
    "toasts a failure once when the dialog was %s, and does not show it in what is on screen",
    async (_, changes) => {
      const h = renderSave(true);
      await h.send();

      for (const shown of changes) {
        h.rerender({ shown });
      }
      h.held.reject(denied());
      await flushInAct();

      expect(mockedToastError).toHaveBeenCalledTimes(1);
      expect(mockedToastError).toHaveBeenCalledWith(
        `${SAVING} failed: ${DENIED}`,
      );
      expect(h.handlers.onError).not.toHaveBeenCalled();
    },
  );

  it.each(CHANGES)(
    "does not call onSuccess for a success when the dialog was %s",
    async (_, changes) => {
      const h = renderSave(true);
      await h.send();

      for (const shown of changes) {
        h.rerender({ shown });
      }
      h.held.resolve("saved");
      await flushInAct();

      expect(h.handlers.onSuccess).not.toHaveBeenCalled();
      expect(h.handlers.onLateSuccess).toHaveBeenCalledTimes(1);
      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );

  it("keeps a save with the dialog it was sent from when something else re-renders", async () => {
    // The control for the two above: the flag is compared, not the render.
    const h = renderSave(true);
    await h.send();

    h.rerender({ shown: true });
    h.rerender({ shown: true });
    h.held.reject(denied());
    await flushInAct();

    expect(h.handlers.onError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

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
});

describe("what a late success is told about the dialogs of its component", () => {
  // A component that hosts one dialog after another keeps a draft for them
  // (what was typed in a Create dialog stays when it is dismissed), and a late
  // success may put the draft away only when no dialog is showing it: a dialog
  // opened since is showing it as its own text.
  const SHOWN: [name: string, start: Shown, changes: Shown[], open: boolean][] =
    [
      ["the dialog was closed", true, [false], false],
      ["the dialog was closed and opened again", true, [false, true], true],
      ["the form was closed", "form-a", [null], false],
      [
        "the form was closed and another opened",
        "form-a",
        [null, "form-b"],
        true,
      ],
      ["the form was replaced by another", "form-a", ["form-b"], true],
    ];

  it.each(SHOWN)(
    "says whether a dialog is open when %s",
    async (_, start, changes, open) => {
      const h = renderSave(start);
      await h.send();

      for (const shown of changes) {
        h.rerender({ shown });
      }
      h.held.resolve("saved");
      await flushInAct();

      expect(h.handlers.onLateSuccess).toHaveBeenCalledTimes(1);
      expect(h.handlers.onLateSuccess).toHaveBeenCalledWith("saved", { open });
    },
  );
});

describe("what a failure nobody is looking at says when the caller words it", () => {
  const REFUSED = "Saving thing-01 was refused: do it again from its dialog.";

  it("toasts the whole text lateFailure gives, in place of the default, and hands it the error", async () => {
    const lateFailure = vi.fn(() => REFUSED);
    const h = renderSave(true, { lateFailure });
    await h.send();
    h.unmount();

    const failure = denied();
    h.held.reject(failure);
    await flushInAct();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(REFUSED);
    expect(lateFailure).toHaveBeenCalledTimes(1);
    expect(lateFailure).toHaveBeenCalledWith(failure);
  });

  it("keeps the default text when lateFailure has nothing to say about the error", async () => {
    const lateFailure = vi.fn(() => undefined);
    const h = renderSave(true, { lateFailure });
    await h.send();
    h.unmount();

    h.held.reject(denied());
    await flushInAct();

    expect(lateFailure).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(
      `${SAVING} failed: ${DENIED}`,
    );
  });

  it("toasts the text lateFailure gives when the dialog was closed rather than unmounted", async () => {
    const lateFailure = vi.fn(() => REFUSED);
    const h = renderSave(true, { lateFailure });
    await h.send();
    h.rerender({ shown: false });

    h.held.reject(denied());
    await flushInAct();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(REFUSED);
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
      signIn();
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

describe("a save that settles after its session ended", () => {
  // The protected outlet is keyed by user, so a sign-out or a change of hands
  // unmounts the dialog. Both cases are covered anyway: the component being
  // gone is not what keeps a toast naming the previous user's objects out of
  // the next user's Toaster.

  it("control: toasts a failure that settles in the same session, with the component gone", async () => {
    signIn();
    const h = renderSave();
    await h.send();
    h.unmount();

    h.held.reject(denied());
    await flushInAct();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
  });

  it.each(SESSION_ENDINGS)(
    "toasts nothing for a failure after %s, with the component gone",
    async (_, end) => {
      signIn();
      const h = renderSave();
      await h.send();
      h.unmount();

      end();
      h.held.reject(denied());
      await flushInAct();

      expect(mockedToastError).not.toHaveBeenCalled();
      expect(h.handlers.onError).not.toHaveBeenCalled();
    },
  );

  it.each(SESSION_ENDINGS)(
    "does nothing at all for a failure after %s, with the component still mounted",
    async (_, end) => {
      signIn();
      const h = renderSave();
      await h.send();

      end();
      h.held.reject(denied());
      await flushInAct();

      expect(h.handlers.onError).not.toHaveBeenCalled();
      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );

  it("control: calls onSuccess for a success in the same session, with the component mounted", async () => {
    signIn();
    const h = renderSave();
    await h.send();

    h.held.resolve("saved");
    await flushInAct();

    expect(h.handlers.onSuccess).toHaveBeenCalledTimes(1);
  });

  it.each(SESSION_ENDINGS)(
    "does nothing at all for a success after %s, with the component still mounted",
    async (_, end) => {
      signIn();
      const h = renderSave();
      await h.send();

      end();
      h.held.resolve("saved");
      await flushInAct();

      expect(h.handlers.onSuccess).not.toHaveBeenCalled();
      expect(h.handlers.onLateSuccess).not.toHaveBeenCalled();
    },
  );

  it("control: calls onLateSuccess for a success in the same session, with the component gone", async () => {
    signIn();
    const h = renderSave();
    await h.send();
    h.unmount();

    h.held.resolve("saved");
    await flushInAct();

    expect(h.handlers.onLateSuccess).toHaveBeenCalledTimes(1);
  });

  it.each(SESSION_ENDINGS)(
    "does not call onLateSuccess for a success after %s: it would hand a result to a screen it was not made for",
    async (_, end) => {
      signIn();
      const h = renderSave();
      await h.send();
      h.unmount();

      end();
      h.held.resolve("saved");
      await flushInAct();

      expect(h.handlers.onLateSuccess).not.toHaveBeenCalled();
      expect(h.handlers.onSuccess).not.toHaveBeenCalled();
    },
  );

  it("is the session the save was sent in that counts, not the one at the time of the failure", async () => {
    // Sent in the second session, settled in it: the earlier sign-out is not
    // this save's.
    signIn();
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

      expect(mockedToastError).toHaveBeenCalledTimes(1);
      expect(mockedToastError).toHaveBeenCalledWith(
        `${SAVING} failed: ${said}`,
      );
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
