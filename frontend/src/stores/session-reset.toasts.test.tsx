import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { Toaster, toast } from "sonner";
import { queryClient } from "@/lib/query-client";
import {
  emptyPerSessionStores,
  PER_SESSION_STORES,
} from "@/test/per-session-stores";
import {
  afterADismissal,
  mountedToasterAndDrawn,
  toasterShowing,
} from "@/test/real-toaster";
import { resetSessionState } from "./session-reset";

/**
 * The toasts a session leaves behind (stores/session-reset.ts). Run on the real
 * sonner, with Toasters mounted, unmounted and mounted again as AppShell's is
 * when a session ends or changes hands: sonner keeps its toasts in module state
 * and hands every one that was never dismissed to each Toaster that mounts.
 *
 * What is looked for is the text of a failure naming a node, in the server's
 * words: what the previous user would otherwise leave for the next one.
 */

const ON_SCREEN = "Saving the options of pve-01 failed: Proxmox API denied it";

beforeEach(() => {
  toast.dismiss();
  emptyPerSessionStores();
});

afterEach(() => {
  vi.restoreAllMocks();
  toast.dismiss();
  queryClient.clear();
  emptyPerSessionStores();
  localStorage.clear();
});

describe("sonner, without the reset: what makes it matter", () => {
  it("control: a toast on screen when its Toaster unmounts is shown by the next Toaster that mounts", async () => {
    const first = await toasterShowing(ON_SCREEN);

    first.unmount();
    await mountedToasterAndDrawn();

    expect(screen.getByText(ON_SCREEN)).toBeInTheDocument();
  });

  it("control: so is one raised while no Toaster is mounted", async () => {
    toast.error(ON_SCREEN);

    await mountedToasterAndDrawn();

    expect(screen.getByText(ON_SCREEN)).toBeInTheDocument();
  });
});

describe("resetSessionState: the toasts", () => {
  // No Toaster, and so no DOM: sonner's own count of the toasts that are active,
  // which is what it hands to the next Toaster that mounts.
  it("control: a toast that exists is active until something dismisses it, with no Toaster mounted", () => {
    toast.error(ON_SCREEN);

    expect(toast.getToasts()).toHaveLength(1);
  });

  it("leaves no toast active, whether or not a Toaster is mounted", () => {
    toast.error(ON_SCREEN);
    expect(toast.getToasts()).toHaveLength(1);

    resetSessionState();

    expect(toast.getToasts()).toHaveLength(0);
  });

  it("does not leave a toast that was on screen to be shown by the Toaster of the next session", async () => {
    const first = await toasterShowing(ON_SCREEN);

    resetSessionState();
    first.unmount();
    await mountedToasterAndDrawn();

    expect(screen.queryByText(ON_SCREEN)).toBeNull();
  });

  it("does not leave one that was raised while no Toaster was mounted, before the reset", async () => {
    const first = render(<Toaster />);
    first.unmount();
    toast.error(ON_SCREEN);

    resetSessionState();
    await mountedToasterAndDrawn();

    expect(screen.queryByText(ON_SCREEN)).toBeNull();
  });

  it("takes every toast that is on screen off it, and not only the last", async () => {
    render(<Toaster />);
    toast.error(`${ON_SCREEN} (first)`);
    toast.error(`${ON_SCREEN} (second)`);
    await screen.findByText(`${ON_SCREEN} (second)`);
    await screen.findByText(`${ON_SCREEN} (first)`);

    resetSessionState();
    await afterADismissal();

    expect(
      screen.queryByText(/Saving the options of pve-01 failed/),
    ).toBeNull();
    expect(toast.getToasts()).toHaveLength(0);
  });

  // The same wait as the test above, with no reset: a toast that is still there
  // after it was never dismissed, so one that is gone in the test above was.
  it("control: a toast that is on screen stays there for its time when nothing resets the session", async () => {
    render(<Toaster />);
    toast.error(`${ON_SCREEN} (first)`);
    toast.error(`${ON_SCREEN} (second)`);
    await screen.findByText(`${ON_SCREEN} (second)`);
    await screen.findByText(`${ON_SCREEN} (first)`);

    await afterADismissal();

    expect(screen.getByText(`${ON_SCREEN} (first)`)).toBeInTheDocument();
    expect(screen.getByText(`${ON_SCREEN} (second)`)).toBeInTheDocument();
    expect(toast.getToasts()).toHaveLength(2);
  });

  it("dismisses everything, not one toast: toast.dismiss is called without an id", () => {
    const dismiss = vi.spyOn(toast, "dismiss");

    resetSessionState();

    expect(dismiss).toHaveBeenCalledTimes(1);
    expect(dismiss).toHaveBeenCalledWith();
  });

  // What the reset alone cannot reach: it dismisses what exists when it runs, and
  // a toast raised after it, with no Toaster mounted (the login pages have none),
  // is a new one. auth-store dismisses again as the next identity begins, which
  // closes that (auth-store.toasts.test.tsx); what comes after that is shown at
  // once, and only the work that raises it can refuse to (lib/query-client.ts,
  // and the hook-level sites that name sessionScope).
  it("does not, on its own, cover a toast raised after it while no Toaster is mounted: the next one shows it", async () => {
    resetSessionState();
    toast.error(ON_SCREEN);

    await mountedToasterAndDrawn();

    expect(screen.getByText(ON_SCREEN)).toBeInTheDocument();
  });
});

describe("resetSessionState: a failure of sonner's", () => {
  const failure = new Error("sonner could not dismiss");

  /** Something of the session in every store and in the cache, to be forgotten. */
  function stateToForget() {
    queryClient.setQueryData(["clusters"], [{ id: "cluster01" }]);
    for (const probe of Object.values(PER_SESSION_STORES)) probe.dirty();
  }

  function everythingIsForgotten() {
    expect(queryClient.getQueryCache().getAll()).toHaveLength(0);
    for (const [file, probe] of Object.entries(PER_SESSION_STORES)) {
      expect(probe.holdsData(), file).toBe(false);
    }
  }

  it("does not skip the cache or the stores, and is reported", () => {
    stateToForget();
    vi.spyOn(toast, "dismiss").mockImplementation(() => {
      throw failure;
    });
    const reported = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);

    expect(() => {
      resetSessionState();
    }).not.toThrow();

    everythingIsForgotten();
    expect(reported).toHaveBeenCalledTimes(1);
    expect(reported).toHaveBeenCalledWith(
      "Could not dismiss the toasts a session left behind",
      failure,
    );
  });

  it("control: says nothing when it works, and forgets the same", () => {
    stateToForget();
    const reported = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);

    resetSessionState();

    everythingIsForgotten();
    expect(reported).not.toHaveBeenCalled();
  });

  // The reset throws when localStorage does (console-store's persisted copy), as
  // it is documented to, and it throws last: the toasts were dismissed before it
  // got there, so the previous user's are not left for the next one.
  it("has dismissed the toasts by the time a full localStorage makes the reset throw", async () => {
    const first = await toasterShowing(ON_SCREEN);
    PER_SESSION_STORES["console-store.ts"]?.dirty();
    vi.spyOn(Storage.prototype, "setItem").mockImplementation((key: string) => {
      if (key === "nexara-console-tabs") {
        throw new DOMException("quota exceeded", "QuotaExceededError");
      }
    });

    let thrown: unknown;
    try {
      resetSessionState();
    } catch (err) {
      thrown = err;
    }
    expect(thrown).toMatchObject({ name: "QuotaExceededError" });
    vi.restoreAllMocks();
    first.unmount();
    await mountedToasterAndDrawn();

    expect(screen.queryByText(ON_SCREEN)).toBeNull();
  });
});
