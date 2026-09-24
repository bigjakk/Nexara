import { describe, it, expect, vi } from "vitest";

// A stand-in for the React DevTools hook, which React calls on every commit,
// after that commit's layout effects and before its passive ones: the only
// place a test can stand between the two. A renderer looks for the hook once,
// as it loads, so it is installed here, hoisted above every import, and in a
// file of its own, as vitest loads react-dom afresh for each test file.
const commitListeners = vi.hoisted(() => {
  const listeners: (() => void)[] = [];
  (globalThis as unknown as Record<string, unknown>)[
    "__REACT_DEVTOOLS_GLOBAL_HOOK__"
  ] = {
    supportsFiber: true,
    isDisabled: false,
    renderers: new Map(),
    inject: () => 1,
    onScheduleFiberRoot: () => undefined,
    onCommitFiberRoot: () => {
      for (const listener of listeners) listener();
    },
    onPostCommitFiberRoot: () => undefined,
    onCommitFiberUnmount: () => undefined,
    checkDCE: () => undefined,
  };
  return listeners;
});

import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { apiClient } from "@/lib/api-client";
import { ContainerResourcesPanel } from "./ContainerResourcesPanel";

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
      patch: vi.fn(),
      delete: vi.fn(),
    },
  };
});

const mockedGet = vi.mocked(apiClient.get);
const mockedList = vi.mocked(apiClient.list);
const mockedPut = vi.mocked(apiClient.put);

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const CT = "d0000000-0000-4000-8000-000000000002";
const CONFIG_URL = `/api/v1/clusters/${CLUSTER}/containers/${CT}/config`;

const serverConfig = {
  hostname: "linux01",
  cores: 2,
  memory: 1024,
  rootfs: "store01:vm-200-disk-0,size=8G",
  net0: "name=eth0,bridge=vmbr0,hwaddr=02:00:00:00:00:01,ip=dhcp,type=veth",
  unused0: "store01:vm-200-disk-1",
  unused1: "store02:vm-200-disk-2",
};

/** The Unused Volumes row for `key`, if listed: its label beside Remove or Undo. */
function unusedRow(key: string): HTMLElement | undefined {
  return screen
    .queryAllByText(key, { exact: true })
    .map((label) => label.parentElement)
    .find(
      (row): row is HTMLElement =>
        row !== null &&
        Array.from(row.querySelectorAll("button")).some((button) =>
          /^(remove|undo)$/i.test(button.textContent.trim()),
        ),
    );
}

/** What a commit left on the Save button, as React keeps it on the node. */
interface SaveButtonProps {
  onClick?: () => void;
  disabled?: boolean;
}

// The render that first lays a successful Save's result over the config has
// the fields and the staging it was built on still in place: the populate
// effect resets them, but only after that render commits. A click handled in
// between (by the Save button that commit rendered) meets a Save built on a
// config already replaced, and must do nothing. Left to its checks, it would
// find the volume it just deleted gone and say it was removed elsewhere.
describe("ContainerResourcesPanel between a Save's result and the reload after it", () => {
  it("does nothing for a click on the Save button of the commit that first shows the result", async () => {
    // The first load answers; every refetch after it never does, so the
    // Save's result stands over the copy that load fetched.
    let gets = 0;
    mockedGet.mockImplementation((path: string) => {
      if (path !== CONFIG_URL) {
        return Promise.reject(new Error(`unexpected GET ${path}`));
      }
      gets += 1;
      return gets === 1
        ? Promise.resolve({ ...serverConfig })
        : new Promise(() => undefined);
    });
    mockedList.mockResolvedValue([]);
    mockedPut.mockResolvedValue({ status: "ok" });
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    });
    render(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <ContainerResourcesPanel
            clusterId={CLUSTER}
            ctId={CT}
            ctStatus="stopped"
            nodeName="pve-01"
          />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    const user = userEvent.setup();
    await screen.findByText("unused1");
    const save = screen.getByRole("button", { name: /save changes/i });
    const row = unusedRow("unused0");
    if (row === undefined) throw new Error("unused0 is not listed");
    await user.click(within(row).getByRole("button", { name: /^remove$/i }));
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");

    const propsKey = Object.keys(save).find((key) =>
      key.startsWith("__reactProps$"),
    );
    if (propsKey === undefined) throw new Error("no React props on Save");
    let clickInWindow: (() => void) | undefined;
    commitListeners.push(() => {
      const props = (
        save as unknown as Record<string, SaveButtonProps | undefined>
      )[propsKey];
      // The Save sent, its deleted volume no longer listed, and Save still
      // enabled by the staging the populate effect has yet to clear.
      if (
        clickInWindow === undefined &&
        mockedPut.mock.calls.length === 1 &&
        unusedRow("unused0") === undefined &&
        props?.disabled === false
      ) {
        clickInWindow = props.onClick;
      }
    });

    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );
    await screen.findByText("Saved");
    // Past the window: the populate effect has cleared what was sent.
    await waitFor(() => {
      expect(save).toBeDisabled();
    });
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { delete: "unused0" } }],
    ]);
    // The window is real: a commit showed the result with Save enabled.
    expect(clickInWindow).toBeTypeOf("function");

    // Handled only now, after that commit's effects have run.
    await act(async () => {
      clickInWindow?.();
      await Promise.resolve();
    });

    expect(mockedPut).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.getByText("Saved")).toBeInTheDocument();
  });
});
