import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ClusterPoolsTab } from "./ClusterPoolsTab";
import { unaddressableHint } from "@/lib/api-path";
import { expectOnScreen } from "@/test/test-utils";

const CLUSTER = "cccccccc-0000-0000-0000-000000000003";
const POOLS_PATH = `/api/v1/clusters/${CLUSTER}/pools`;

const listMock = vi.fn();
const getMock = vi.fn();
const deleteMock = vi.fn();

// Spread the real module: describeError and friends import ApiClientError
// from here. Only the transport is replaced — the paths the hooks hand it are
// built by the real apiPath.
vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return {
    ...actual,
    apiClient: {
      list: (path: string) => listMock(path) as unknown,
      get: (path: string) => getMock(path) as unknown,
      delete: (...args: unknown[]) => deleteMock(...args) as unknown,
      put: vi.fn(),
      post: vi.fn(),
    },
  };
});

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

function renderTab() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return render(<ClusterPoolsTab clusterId={CLUSTER} />, { wrapper });
}

beforeEach(() => {
  vi.clearAllMocks();
  // Two pools: an ordinary one, and one named ".." — a name Proxmox's pool
  // id format admits and a browser cannot put in a path.
  listMock.mockImplementation((path: string) =>
    Promise.resolve(
      path === POOLS_PATH
        ? [
            { poolid: "pool01", comment: "web tier" },
            { poolid: "..", comment: "made outside Nexara" },
          ]
        : [],
    ),
  );
  getMock.mockResolvedValue({ poolid: "pool01", members: [] });
  deleteMock.mockResolvedValue(undefined);
});

describe("deleting a pool", () => {
  it("asks first, names the pool, and sends nothing on Cancel", async () => {
    const user = userEvent.setup();
    renderTab();

    await user.click(
      await screen.findByRole("button", { name: "Delete pool01" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).getByRole("heading", { name: "Delete pool pool01?" }),
    ).toBeInTheDocument();
    // What Proxmox's delete_pool does to a pool with members: it refuses.
    expect(dialog).toHaveTextContent(
      "Proxmox refuses to delete a pool that still has members",
    );

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(deleteMock).not.toHaveBeenCalled();
  });

  it("sends exactly one DELETE, to that pool's own path, on Confirm", async () => {
    const user = userEvent.setup();
    renderTab();

    await user.click(
      await screen.findByRole("button", { name: "Delete pool01" }),
    );
    const dialog = await screen.findByRole("alertdialog");
    // Before the confirmation nothing has been sent — the button only asks.
    expect(deleteMock).not.toHaveBeenCalled();
    await user.click(
      within(dialog).getByRole("button", { name: "Delete Pool" }),
    );

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(deleteMock.mock.calls).toEqual([[`${POOLS_PATH}/pool01`]]);
  });
});

/** The table row a piece of visible text sits in. */
function rowOf(text: string): HTMLElement {
  const row = screen.getByText(text).closest("tr");
  if (!(row instanceof HTMLElement)) throw new Error(`${text} is not in a row`);
  return row;
}

describe("a pool Nexara cannot address", () => {
  it("keeps its row, and shows why as text in place of its actions", async () => {
    renderTab();

    await screen.findByText("made outside Nexara");
    const row = rowOf("made outside Nexara");
    // The reason, as text in the row — a disabled button's title never shows
    // (Button takes no pointer events or focus when disabled) — and no
    // action to try. On screen as far as jsdom can tell: see expectOnScreen.
    expectOnScreen(within(row).getByText(reasonFor("..")));
    expect(within(row).queryAllByRole("button")).toEqual([]);
    // The ordinary pool's actions are live: nothing here hides every row's.
    const ordinary = rowOf("web tier");
    expect(
      within(ordinary).getByRole("button", { name: "Delete pool01" }),
    ).toBeEnabled();
    expect(
      within(ordinary).getByRole("button", { name: "Edit pool01" }),
    ).toBeEnabled();
  });

  it("explains itself when expanded, and never asks the server about it", async () => {
    const user = userEvent.setup();
    renderTab();

    await user.click(await screen.findByText("made outside Nexara"));
    // Once in its row, once more where its members would be.
    await waitFor(() => {
      expect(screen.getAllByText(reasonFor(".."))).toHaveLength(2);
    });

    // Positive control: expanding the ordinary pool DOES read it, so the
    // absence of a read for ".." is not just an absence of reads.
    await user.click(screen.getByText("web tier"));
    await waitFor(() => {
      expect(getMock).toHaveBeenCalled();
    });
    expect(getMock.mock.calls).toEqual([[`${POOLS_PATH}/pool01`]]);
    expect(deleteMock).not.toHaveBeenCalled();
  });
});

describe("a nested pool", () => {
  // Proxmox creates "infra/prod"; the SPA would send it as the one segment
  // "infra%2Fprod", and the per-pool routes refuse the "%"
  // (path-safe-dotted-name), so every action on it could only answer 400.
  it("keeps its row, shows why in place of its actions, and is never read", async () => {
    listMock.mockImplementation((path: string) =>
      Promise.resolve(
        path === POOLS_PATH
          ? [
              { poolid: "pool01", comment: "web tier" },
              { poolid: "infra/prod", comment: "nested" },
            ]
          : [],
      ),
    );
    const user = userEvent.setup();
    renderTab();

    await screen.findByText("nested");
    const row = rowOf("nested");
    expectOnScreen(
      within(row).getByText(
        /Nexara cannot manage the nested pool "infra\/prod"/,
      ),
    );
    expect(within(row).queryAllByRole("button")).toEqual([]);
    expect(
      within(rowOf("web tier")).getByRole("button", { name: "Delete pool01" }),
    ).toBeEnabled();

    await user.click(screen.getByText("nested"));
    await waitFor(() => {
      expect(
        screen.getAllByText(/Nexara cannot manage the nested pool/),
      ).toHaveLength(2);
    });
    expect(getMock).not.toHaveBeenCalled();
  });
});

function reasonFor(name: string): string {
  const reason = unaddressableHint(name);
  if (reason === null) throw new Error(`${name} is addressable`);
  return reason;
}
