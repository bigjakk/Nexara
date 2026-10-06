import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  startTransition,
  useEffect,
  useLayoutEffect,
  useState,
  type ReactNode,
} from "react";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { ApiClientError } from "@/lib/api-client";
import { deferred } from "@/test/fake-server";
import { flushInAct, renderOnAppClient } from "@/test/save-outcome-kit";
import { ClusterACMETab } from "./ClusterACMETab";

/**
 * When a domain save fails after the Certificates tab is gone, the tab toasts it
 * (ClusterACMETab.test.tsx). It knows it is gone from a flag that has to be
 * cleared by the commit that removes it, not by the passive effects that follow.
 * A navigation runs in a transition (React Router's RouterProvider sets its state
 * in startTransition), and after such a commit the scheduler yields for a paint,
 * so the commit's passive effects run a task later: a request that settles in
 * between finds the tab off the page and a flag cleared by a passive cleanup
 * still set.
 *
 * That gap cannot be reached through the real components. Radix's Dialog, and
 * its Presence and FocusScope each on their own, and its Select and Tabs,
 * schedule a sync update when they unmount (their ref callbacks set state to
 * null), and React flushes a commit's passive effects inside the commit when a
 * sync update is pending, so no promise can settle before them. Nothing in the
 * app should lean on that, so this file stands those in with plain elements,
 * which leaves the flag as the only thing that decides the outcome.
 */

const listMock = vi.fn();
const getMock = vi.fn();
const putMock = vi.fn();

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock({
    list: (path: string) => listMock(path) as unknown,
    get: (path: string) => getMock(path) as unknown,
    put: (path: string, body: unknown) => putMock(path, body) as unknown,
  }),
);

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

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

// The select and the tabs as plain elements too: each is a Radix primitive that
// schedules the same sync update when it unmounts. The tab's own select is never
// opened here, so what it would list does not matter.
vi.mock("@/components/ui/select", async () => {
  const { createElement } = await import("react");
  type Slot = { children?: ReactNode };
  return {
    Select: ({ children }: Slot) => createElement("div", null, children),
    SelectTrigger: () => null,
    SelectValue: () => null,
    SelectContent: () => null,
    SelectItem: () => null,
  };
});

vi.mock("@/components/ui/tabs", async () => {
  const { createContext, createElement, useContext, useState } =
    await import("react");
  type Slot = { children?: ReactNode };
  const Selected = createContext<{
    value: string;
    select: (value: string) => void;
  }>({ value: "", select: () => undefined });
  return {
    Tabs: function Tabs({
      defaultValue,
      children,
    }: Slot & { defaultValue: string }) {
      const [value, select] = useState(defaultValue);
      return createElement(
        Selected.Provider,
        { value: { value, select } },
        createElement("div", null, children),
      );
    },
    TabsList: ({ children }: Slot) =>
      createElement("div", { role: "tablist" }, children),
    TabsTrigger: function TabsTrigger({
      value,
      children,
    }: Slot & { value: string }) {
      const tabs = useContext(Selected);
      return createElement(
        "button",
        {
          role: "tab",
          "aria-selected": tabs.value === value,
          onClick: () => {
            tabs.select(value);
          },
        },
        children,
      );
    },
    TabsContent: function TabsContent({
      value,
      children,
    }: Slot & { value: string }) {
      const tabs = useContext(Selected);
      return tabs.value === value
        ? createElement("div", { role: "tabpanel" }, children)
        : null;
    },
  };
});

const mockedToastError = vi.mocked(toast.error);

const CLUSTER = "cccccccc-0000-0000-0000-000000000003";
const NODE = "pve-01";
const NODES_PATH = `/api/v1/clusters/${CLUSTER}/nodes`;
const CONFIG_PATH = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/acme-config`;
const DENIED = "Proxmox API permission denied";

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
 * The tab on a page that can be left in a transition, the way React Router
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
    <ClusterACMETab clusterId={CLUSTER} />
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
  listMock.mockImplementation((path: string) =>
    path === NODES_PATH
      ? Promise.resolve([{ name: NODE, node_name: NODE }])
      : Promise.resolve([]),
  );
  getMock.mockImplementation((path: string) =>
    path === CONFIG_PATH
      ? Promise.resolve({
          acmedomain0: "domain=node1.example.com",
          digest: "d1",
        })
      : Promise.resolve(null),
  );
  putMock.mockResolvedValue({ status: "ok" });
});

describe("a save that settles in the gap after the commit that removed its tab", () => {
  it("toasts its failure, naming the node, before that commit's passive effects have run", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    putMock.mockReturnValueOnce(held.promise);
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
    renderOnAppClient(
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
      />,
    );

    await user.click(screen.getByRole("tab", { name: "Node Certificates" }));
    await user.click(await screen.findByRole("button", { name: "Edit" }));
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
    await flushInAct();

    // The gap itself, not merely some time after it: what the run reached.
    expect(seen.atSettle).toEqual({ dialogOnPage: false, passive: false });
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(
      `Saving the ACME domain node1.example.com on ${NODE} failed: ${DENIED}`,
    );
    expect(seen.toastedBeforePassive).toBe(true);
    expect(putMock).toHaveBeenCalledTimes(1);
  });
});
