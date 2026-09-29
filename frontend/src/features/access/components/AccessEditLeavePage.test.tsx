import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  startTransition,
  useEffect,
  useLayoutEffect,
  useState,
  type ReactNode,
} from "react";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { toast } from "sonner";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import type { AccessCapabilities } from "../api/access-queries";
import { AccessUsersSection } from "./AccessUsersSection";

/**
 * When a save from the Edit User dialog fails after the dialog is gone, the
 * dialog toasts it (AccessUsersSection.test.tsx). It knows it is gone from a
 * flag that has to be cleared by the commit that removes it, not by the passive
 * effects that follow. A navigation runs in a transition (React Router's
 * RouterProvider sets its state in startTransition), and after such a commit
 * the scheduler yields for a paint, so the commit's passive effects run a task
 * later: a request that settles in between finds the dialog off the page and a
 * flag cleared by a passive cleanup still set.
 *
 * That gap cannot be reached through the real dialog. Radix's Dialog, and its
 * Presence and FocusScope each on their own, schedule a sync update when they
 * unmount (their ref callbacks set state to null), and React flushes a commit's
 * passive effects inside the commit when a sync update is pending, so no
 * promise can settle before them. Nothing in the app should lean on that, so
 * this file stands the dialog in with plain elements, which leaves the flag as
 * the only thing that decides the outcome.
 */

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      get: vi.fn(),
      list: vi.fn(),
      post: vi.fn(),
      put: vi.fn(),
      delete: vi.fn(),
    },
  };
});

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

// The dialog as plain elements, for the reason above: an open one shows its
// content, and no primitive underneath it schedules anything when it unmounts.
vi.mock("@/components/ui/dialog", async () => {
  const { createElement } = await import("react");
  type Slot = { children?: ReactNode };
  const passThrough = ({ children }: Slot) => children;
  const element = (tag: string, role?: string) =>
    function Element({ children }: Slot) {
      return createElement(tag, role ? { role } : null, children);
    };
  return {
    Dialog: ({ open, children }: Slot & { open?: boolean }) =>
      open ? children : null,
    DialogContent: element("div", "dialog"),
    DialogHeader: element("div"),
    DialogFooter: element("div"),
    DialogTitle: element("h2"),
    DialogDescription: element("p"),
    DialogTrigger: passThrough,
    DialogPortal: passThrough,
    DialogClose: passThrough,
    DialogOverlay: () => null,
  };
});

const mockedList = vi.mocked(apiClient.list);
const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);
const mockedToastError = vi.mocked(toast.error);

const CLUSTER = "cccccccc-0000-0000-0000-000000000005";
const USERS_URL = `/api/v1/clusters/${CLUSTER}/access/users`;
// The account Nexara authenticates as, by its documented default name.
const OWN = "nexara@pve";
const OWN_URL = `${USERS_URL}/nexara%40pve`;
const DENIED = "Proxmox API permission denied";

const capabilities: AccessCapabilities = {
  loading: false,
  canModifyUsers: true,
  canModifyRoles: true,
  canModifyRealms: true,
  canModifyACL: true,
};

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  let reject: (reason: unknown) => void = () => undefined;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

/** Lets whatever is already queued run, and React draw what it set. */
async function flush(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0);
    });
  });
}

/**
 * What replaces the page once it has been left. It reports the two moments of
 * the commit that put it there: its layout effect runs in that commit, after
 * the layout cleanups of what the commit removed, and its passive effect a task
 * later, in the same flush as the passive cleanups of what was removed.
 */
function LeftPage({
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
  return <p>Left the page</p>;
}

/**
 * The section on a page that can be left in a transition, the way React Router
 * leaves one. `onReady` is handed the way out.
 */
function LeavablePage({
  onReady,
  onLayout,
  onPassive,
}: {
  onReady: (leave: () => void) => void;
  onLayout: () => void;
  onPassive: () => void;
}) {
  const [here, setHere] = useState(true);
  useEffect(() => {
    onReady(() => {
      startTransition(() => {
        setHere(false);
      });
    });
  }, [onReady]);
  return here ? (
    <AccessUsersSection clusterId={CLUSTER} capabilities={capabilities} />
  ) : (
    <LeftPage onLayout={onLayout} onPassive={onPassive} />
  );
}

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

beforeEach(() => {
  vi.resetAllMocks();
  mockedList.mockImplementation((path: string) =>
    Promise.resolve(
      path === USERS_URL
        ? [{ userid: OWN, enable: true, comment: "service account" }]
        : [],
    ),
  );
  mockedGet.mockImplementation((path: string) =>
    path === OWN_URL
      ? Promise.resolve({ userid: OWN, enable: true })
      : Promise.reject(new Error(`unexpected GET ${path}`)),
  );
  mockedPut.mockResolvedValue({ status: "ok" });
});

describe("a save that settles in the gap after the commit that removed its dialog", () => {
  it("toasts its failure, naming the account, before that commit's passive effects have run", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReturnValueOnce(held.promise);
    const passiveEffectsRan = deferred<unknown>();
    const seen: {
      leave?: () => void;
      passive: boolean;
      atSettle?: { dialogOnPage: boolean; passive: boolean };
      toastedBeforePassive?: boolean;
    } = { passive: false };
    mockedToastError.mockImplementation(() => {
      seen.toastedBeforePassive = !seen.passive;
      return "toast-id";
    });
    render(
      <QueryClientProvider client={createAppQueryClient()}>
        <LeavablePage
          onReady={(leave) => {
            seen.leave = leave;
          }}
          onLayout={() => {
            // The first time only, should a layout effect ever run twice.
            seen.atSettle ??= {
              dialogOnPage:
                screen.queryByRole("dialog", { hidden: true }) !== null,
              passive: seen.passive,
            };
            // Settled here, in the commit, so that the handlers of the
            // rejection run before the task that flushes the passive effects.
            held.reject(
              new ApiClientError(403, { error: "forbidden", message: DENIED }),
            );
          }}
          onPassive={() => {
            seen.passive = true;
            passiveEffectsRan.resolve(undefined);
          }}
        />
      </QueryClientProvider>,
    );

    await user.click(
      await screen.findByRole("button", { name: `Edit ${OWN}` }),
    );
    await user.click(
      await screen.findByRole("checkbox", { name: "Account enabled" }),
    );
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByRole("button", { name: "Saving..." }),
    ).toBeDisabled();
    const leave = seen.leave;
    if (!leave) throw new Error("the page never handed over its way out");

    await outsideAct(async () => {
      leave();
      await passiveEffectsRan.promise;
    });
    await flush();

    // The gap itself, not merely some time after it: what the run reached.
    expect(seen.atSettle).toEqual({ dialogOnPage: false, passive: false });
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(
      `Saving ${OWN} failed: ${DENIED}`,
    );
    expect(seen.toastedBeforePassive).toBe(true);
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });
});
