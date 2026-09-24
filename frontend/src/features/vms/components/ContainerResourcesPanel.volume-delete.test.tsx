import { Activity } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { ContainerResourcesPanel } from "./ContainerResourcesPanel";
import panelSource from "./ContainerResourcesPanel.tsx?raw";

// The transport is mocked rather than the hooks, so the real
// useSetResourceConfig runs and each test can assert the exact request that
// leaves the browser, which is the claim a destructive action's test has to
// make. That is why these tests are not in ContainerResourcesPanel.test.tsx:
// it replaces the hooks themselves, and a vi.mock applies to a whole file.
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
const mockedPost = vi.mocked(apiClient.post);
const mockedPut = vi.mocked(apiClient.put);
const mockedPatch = vi.mocked(apiClient.patch);
const mockedDelete = vi.mocked(apiClient.delete);

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const CT = "d0000000-0000-4000-8000-000000000002";
const CONFIG_URL = `/api/v1/clusters/${CLUSTER}/containers/${CT}/config`;
const CONFIG_KEY = ["clusters", CLUSTER, "containers", CT, "config"];
// What an open confirmation says once the config has changed under it.
const CHANGED_UNDER = /configuration changed while this was open/i;

// Three unused volumes, as moves without "delete source" leave them, each on
// its own storage: a test stages the first and the last, so a dialog or a
// request that took the wrong ones, or all three, cannot match by accident.
// type=veth is spelled out on the NIC so the panel opens clean.
function ctConfig(): Record<string, unknown> {
  return {
    hostname: "linux01",
    cores: 2,
    memory: 1024,
    rootfs: "store01:vm-200-disk-0,size=8G",
    net0: "name=eth0,bridge=vmbr0,hwaddr=02:00:00:00:00:01,ip=dhcp,type=veth",
    unused0: "store01:vm-200-disk-1",
    unused1: "store02:vm-200-disk-2",
    unused2: "store03:vm-200-disk-3",
  };
}

let serverConfig: Record<string, unknown>;

// What Proxmox reports after the write: every deleted key gone.
function serverDropsDeletedKeys(_path: string, body?: unknown) {
  const { fields } = body as { fields: Record<string, string> };
  const deleted = new Set((fields["delete"] ?? "").split(","));
  serverConfig = Object.fromEntries(
    Object.entries(serverConfig).filter(([key]) => !deleted.has(key)),
  );
  return Promise.resolve({ status: "ok" });
}

function deferred<T>() {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

const props = {
  clusterId: CLUSTER,
  ctId: CT,
  ctStatus: "stopped",
  nodeName: "pve-01",
};

// Its own QueryClient rather than renderWithProviders', so a test can refetch
// the config the way a change made elsewhere would.
function renderPanel() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <MemoryRouter>
        <ContainerResourcesPanel {...props} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return queryClient;
}

/** The Unused Volumes row whose key label is `key`. */
function unusedRow(key: string): HTMLElement {
  const rows = screen
    .getAllByText(key, { exact: true })
    .map((label) => label.parentElement)
    .filter(
      (row): row is HTMLElement =>
        row !== null &&
        within(row).queryByRole("button", { name: /^(remove|undo)$/i }) !==
          null,
    );
  expect(rows).toHaveLength(1);
  const [row] = rows;
  if (!row) throw new Error(`no unused volume row for ${key}`);
  return row;
}

function isStaged(key: string): boolean {
  return within(unusedRow(key)).queryByText("removing") !== null;
}

async function stage(user: ReturnType<typeof userEvent.setup>, key: string) {
  await user.click(
    within(unusedRow(key)).getByRole("button", { name: /^remove$/i }),
  );
  expect(isStaged(key)).toBe(true);
}

/** Loads the panel and returns its Save button, checked to open disabled. */
async function openPanel() {
  const queryClient = renderPanel();
  await screen.findByText("unused1");
  const save = screen.getByRole("button", { name: /save changes/i });
  // The fixture opens clean, so Save being enabled later is the staging.
  expect(save).toBeDisabled();
  return { queryClient, save };
}

/** The input under a field label; the panel's labels are not tied to them. */
function fieldInput(label: string): HTMLInputElement {
  const input = screen.getByText(label).parentElement?.querySelector("input");
  if (!input) throw new Error(`${label} input not found`);
  return input;
}

/** The first load answers; every fetch after it never does. */
function holdRefetch() {
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
}

/**
 * Changes the container on the server, refetches it as an event from the
 * server would, and waits until the panel has rendered `rendered`, a text only
 * the changed config shows.
 */
async function changedElsewhere(
  queryClient: QueryClient,
  change: Record<string, unknown>,
  rendered: string | RegExp,
) {
  serverConfig = { ...serverConfig, ...change };
  await act(async () => {
    await queryClient.invalidateQueries({ queryKey: CONFIG_KEY });
  });
  await screen.findByText(rendered);
}

/** Each volume the dialog lists, as "key volume". */
function listedVolumes(dialog: HTMLElement): (string | null)[] {
  return within(dialog)
    .getAllByRole("listitem")
    .map((item) => item.textContent);
}

function expectNothingMutated() {
  expect(mockedPut).not.toHaveBeenCalled();
  expect(mockedPost).not.toHaveBeenCalled();
  expect(mockedPatch).not.toHaveBeenCalled();
  expect(mockedDelete).not.toHaveBeenCalled();
}

describe("ContainerResourcesPanel unused volume deletion", () => {
  beforeEach(() => {
    // reset, not clear: one test's put implementation must not answer the
    // next test's request.
    vi.resetAllMocks();
    serverConfig = ctConfig();
    mockedGet.mockImplementation((path: string) =>
      path === CONFIG_URL
        ? Promise.resolve({ ...serverConfig })
        : Promise.reject(new Error(`unexpected GET ${path}`)),
    );
    mockedList.mockResolvedValue([]);
  });

  it("asks before a Save that deletes volumes, listing exactly the staged ones", async () => {
    const user = userEvent.setup();
    const { save } = await openPanel();
    // Staged out of order: the list is sorted by key, as the section is.
    await stage(user, "unused2");
    await stage(user, "unused0");

    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");

    expect(
      within(dialog).getByText("Delete 2 unused volumes?"),
    ).toBeInTheDocument();
    expect(listedVolumes(dialog)).toEqual([
      "unused0 store01:vm-200-disk-1",
      "unused2 store03:vm-200-disk-3",
    ]);
    // The unstaged one is not in it anywhere.
    expect(dialog).not.toHaveTextContent("unused1");
    expect(dialog).not.toHaveTextContent("store02:vm-200-disk-2");
    // Part of what a screen reader announces with the dialog, not only shown.
    expect(dialog).toHaveAccessibleDescription(
      /unused0 store01:vm-200-disk-1\s*unused2 store03:vm-200-disk-3/,
    );
    expect(dialog).toHaveTextContent(/permanently delete/i);
    expect(dialog).toHaveTextContent(/from storage/i);
    expect(dialog).toHaveTextContent(/cannot be undone/i);
    // Opening the dialog is not the delete.
    expectNothingMutated();
  });

  it("sends nothing when cancelled, keeps the volumes staged, and gives focus back to Save", async () => {
    const user = userEvent.setup();
    const { save } = await openPanel();
    await stage(user, "unused0");
    await stage(user, "unused2");

    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expectNothingMutated();
    expect(isStaged("unused0")).toBe(true);
    expect(isStaged("unused2")).toBe(true);
    expect(isStaged("unused1")).toBe(false);
    expect(save).toBeEnabled();
    // Radix hands focus back on a timer, after the dialog has gone.
    await waitFor(() => {
      expect(save).toHaveFocus();
    });

    // Still staged, not only still drawn that way: Save asks again, for both.
    await user.click(save);
    expect(listedVolumes(await screen.findByRole("alertdialog"))).toEqual([
      "unused0 store01:vm-200-disk-1",
      "unused2 store03:vm-200-disk-3",
    ]);
    expectNothingMutated();
  });

  it("treats Escape as Cancel", async () => {
    const user = userEvent.setup();
    const { save } = await openPanel();
    await stage(user, "unused2");
    await user.click(save);
    await screen.findByRole("alertdialog");

    await user.keyboard("{Escape}");

    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expectNothingMutated();
    expect(isStaged("unused2")).toBe(true);
    expect(save).toBeEnabled();
    await waitFor(() => {
      expect(save).toHaveFocus();
    });
  });

  it("sends the whole Save as exactly one PUT once confirmed, then closes", async () => {
    mockedPut.mockImplementation(serverDropsDeletedKeys);
    const user = userEvent.setup();
    const { save } = await openPanel();
    // An ordinary edit rides along: the confirmation gates the Save, it does
    // not split the delete out of it.
    const memory = fieldInput("Memory (MB)");
    await user.clear(memory);
    await user.type(memory, "2048");
    await stage(user, "unused0");
    await stage(user, "unused2");

    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { memory: "2048", delete: "unused0,unused2" } }],
    ]);
    await waitFor(() => {
      expect(dialog).not.toBeInTheDocument();
    });
    // Still one once it has settled: the write, then the refetch showing the
    // two volumes gone and the third kept.
    await waitFor(() => {
      expect(screen.queryByText("unused0")).not.toBeInTheDocument();
    });
    expect(screen.queryByText("unused2")).not.toBeInTheDocument();
    expect(unusedRow("unused1")).toBeInTheDocument();
    expect(mockedPut).toHaveBeenCalledTimes(1);
    expect(mockedPost).not.toHaveBeenCalled();
    expect(mockedPatch).not.toHaveBeenCalled();
    expect(mockedDelete).not.toHaveBeenCalled();
  });

  it("holds the dialog, and its buttons, while the Save is in flight", async () => {
    const pending = deferred<unknown>();
    mockedPut.mockReturnValue(pending.promise);
    const user = userEvent.setup();
    const { save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    // Still on the page. A dialog that closed would leave this node behind
    // showing whatever it rendered last, "Saving..." included.
    expect(dialog).toBeInTheDocument();
    const confirm = await within(dialog).findByRole("button", {
      name: "Saving...",
    });
    expect(confirm).toBeDisabled();
    // Cancel cannot un-send a request, so it is not offered as if it could.
    expect(
      within(dialog).getByRole("button", { name: "Cancel" }),
    ).toBeDisabled();
    await user.keyboard("{Escape}");
    expect(screen.getByRole("alertdialog")).toBeInTheDocument();
    expect(mockedPut).toHaveBeenCalledTimes(1);

    await act(async () => {
      pending.resolve({ status: "ok" });
      await pending.promise;
    });
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  // The reviewer's case: in a browser Radix keeps a closing dialog mounted,
  // and its buttons clickable, until the exit animation ends, so a dialog
  // that closed on the first click of a double-click took the second too.
  it("sends one PUT for a double-click on Delete and Save, exit animation and all", async () => {
    // jsdom runs no animations. Report one from data-state, as tw-animate-css
    // gives a browser, so Radix's Presence waits on it as it would there.
    const realGetComputedStyle = window.getComputedStyle.bind(window);
    const animated = vi
      .spyOn(window, "getComputedStyle")
      .mockImplementation((element: Element, pseudo?: string | null) => {
        const style = realGetComputedStyle(element, pseudo);
        return new Proxy(style, {
          get(target, prop) {
            if (prop === "animationName") {
              const state = element.getAttribute("data-state");
              if (state === "open") return "enter";
              if (state === "closed") return "exit";
              return "none";
            }
            const value: unknown = Reflect.get(target, prop);
            return typeof value === "function"
              ? (value as (...args: unknown[]) => unknown).bind(target)
              : value;
          },
        });
      });
    try {
      mockedPut.mockImplementation(() => new Promise(() => undefined));
      const user = userEvent.setup();
      const { save } = await openPanel();
      await stage(user, "unused0");
      await user.click(save);
      const dialog = await screen.findByRole("alertdialog");

      await user.dblClick(
        within(dialog).getByRole("button", { name: "Delete and Save" }),
      );

      await waitFor(() => {
        expect(mockedPut).toHaveBeenCalled();
      });
      expect(mockedPut).toHaveBeenCalledTimes(1);
      // Held open, not closing, while the one request is out.
      expect(dialog).toHaveAttribute("data-state", "open");
    } finally {
      animated.mockRestore();
    }
  });

  it("sends one PUT when Delete and Save is clicked twice before a render", async () => {
    mockedPut.mockImplementation(() => new Promise(() => undefined));
    const user = userEvent.setup();
    const { save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const action = within(await screen.findByRole("alertdialog")).getByRole(
      "button",
      { name: "Delete and Save" },
    );

    // Two clicks in one go: TanStack reports the first as pending on a later
    // tick, so no render has disabled the button when the second arrives, and
    // only the check at the top of saveChanges is left to refuse it.
    fireEvent.click(action);
    expect(action).toBeInTheDocument();
    expect(action).toBeEnabled();
    fireEvent.click(action);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalled();
    });
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  // Once a Save settles, the render that re-enables Save comes before the one
  // that lays the result over the config, and until then the fields and the
  // staging still hold what was just sent.
  it("sends nothing more before a render has taken in a settled Save", async () => {
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    const cores = fieldInput("Cores");
    await user.clear(cores);
    await user.type(cores, "4");

    // Settled outside act on purpose, since act would render it, and React
    // says so on console.error; nothing else may be logged there.
    const consoleError = vi
      .spyOn(console, "error")
      .mockImplementation(() => undefined);
    try {
      fireEvent.click(save);
      // Settle it on microtasks alone, which run no render: TanStack tells
      // React on a timer, and React renders on a task of its own.
      const status = () =>
        queryClient
          .getMutationCache()
          .getAll()
          .map((mutation) => mutation.state.status);
      for (let tick = 0; tick < 200 && status()[0] !== "success"; tick += 1) {
        await Promise.resolve();
      }
      for (let tick = 0; tick < 10; tick += 1) {
        await Promise.resolve();
      }
      expect(status()).toEqual(["success"]);
      // Still on the page and enabled, so the click reaches saveChanges.
      expect(save).toBeInTheDocument();
      expect(save).toBeEnabled();
      fireEvent.click(save);

      await waitFor(() => {
        expect(save).toBeDisabled();
      });
      expect(mockedPut.mock.calls).toEqual([
        [CONFIG_URL, { fields: { cores: "4" } }],
      ]);
      expect(
        consoleError.mock.calls.filter(
          ([message]) => !String(message).includes("not wrapped in act"),
        ),
      ).toEqual([]);
    } finally {
      consoleError.mockRestore();
    }
  });

  // A parent that keeps the panel alive while hiding it (<Activity>) tears
  // down every effect in it, TanStack's subscription included, so nothing is
  // listening when a Save settles then and mutate()'s callbacks never run.
  it("frees Save again after a Save that settled while the panel was hidden", async () => {
    const first = deferred<unknown>();
    mockedPut
      .mockReturnValueOnce(first.promise)
      .mockResolvedValueOnce({ status: "ok" });
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false, gcTime: 0 } },
    });
    const panel = (mode: "visible" | "hidden") => (
      <QueryClientProvider client={queryClient}>
        <MemoryRouter>
          <Activity mode={mode}>
            <ContainerResourcesPanel {...props} />
          </Activity>
        </MemoryRouter>
      </QueryClientProvider>
    );
    const user = userEvent.setup();
    const { rerender } = render(panel("visible"));
    await screen.findByText("unused1");
    const cores = fieldInput("Cores");
    await user.clear(cores);
    await user.type(cores, "4");
    await user.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });

    rerender(panel("hidden"));
    await act(async () => {
      first.resolve({ status: "ok" });
      await first.promise;
    });
    rerender(panel("visible"));
    // Shown again, the panel fetches the config afresh; let that land before
    // editing, or it would re-populate the fields over the edit.
    await waitFor(() => {
      expect(mockedGet).toHaveBeenCalledTimes(2);
    });
    await waitFor(() => {
      expect(queryClient.isFetching({ queryKey: CONFIG_KEY })).toBe(0);
    });

    const memory = await waitFor(() => fieldInput("Memory (MB)"));
    await user.clear(memory);
    await user.type(memory, "2048");
    await user.click(screen.getByRole("button", { name: /save changes/i }));
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(mockedPut.mock.calls[1]).toEqual([
      CONFIG_URL,
      { fields: { memory: "2048" } },
    ]);
  });

  it("keeps a failure in the dialog to retry or cancel, keeps the volumes staged, and does not carry it over", async () => {
    mockedPut
      .mockRejectedValueOnce(
        new ApiClientError(409, {
          error: "conflict",
          message: "CT is locked (backup)",
        }),
      )
      .mockRejectedValueOnce(
        new ApiClientError(409, {
          error: "conflict",
          message: "CT is locked (migrate)",
        }),
      );
    const user = userEvent.setup();
    const { save } = await openPanel();
    await stage(user, "unused0");
    await stage(user, "unused2");
    await user.click(save);
    let dialog = await screen.findByRole("alertdialog");
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    expect(
      await within(dialog).findByText("CT is locked (backup)"),
    ).toBeInTheDocument();
    expect(dialog).toBeInTheDocument();
    const request = [CONFIG_URL, { fields: { delete: "unused0,unused2" } }];
    expect(mockedPut.mock.calls).toEqual([request]);

    // A retry is the same request again, from the same open dialog, and its
    // own failure replaces the first one's.
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );
    expect(
      await within(dialog).findByText("CT is locked (migrate)"),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByText("CT is locked (backup)"),
    ).not.toBeInTheDocument();
    expect(mockedPut.mock.calls).toEqual([request, request]);

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    expect(isStaged("unused0")).toBe(true);
    expect(isStaged("unused2")).toBe(true);
    expect(save).toBeEnabled();

    // Asked again rather than sent on the strength of the first yes, and
    // without the last failure, which belongs to the attempt it came from.
    await user.click(save);
    dialog = await screen.findByRole("alertdialog");
    expect(
      within(dialog).queryByText("CT is locked (migrate)"),
    ).not.toBeInTheDocument();
    expect(mockedPut).toHaveBeenCalledTimes(2);
    expect(mockedPost).not.toHaveBeenCalled();
  });

  it("leaves nothing to send or offer again once a Save succeeds, while its refetch is still out", async () => {
    holdRefetch();
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    const memory = fieldInput("Memory (MB)");
    await user.clear(memory);
    await user.type(memory, "2048");
    await user.click(screen.getByRole("button", { name: "Remove NIC" }));
    await stage(user, "unused0");

    await user.click(save);
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Delete and Save",
      }),
    );
    expect(await screen.findByText("Saved")).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    // The refetch really is out, unanswered, so what follows is the panel's
    // own doing.
    expect(
      queryClient.isFetching({
        queryKey: CONFIG_KEY,
      }),
    ).toBe(1);

    // The deleted volume is not offered again: neither staged nor listed.
    expect(screen.queryByText("unused0")).not.toBeInTheDocument();
    expect(isStaged("unused1")).toBe(false);
    expect(isStaged("unused2")).toBe(false);
    // The removed NIC is gone and the edit stands as the saved value...
    expect(screen.queryByText("net0")).not.toBeInTheDocument();
    expect(memory).toHaveValue(2048);
    // ...so there is nothing left to send.
    expect(screen.queryByText(/\d+ changes?/)).not.toBeInTheDocument();
    expect(save).toBeDisabled();
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { memory: "2048", delete: "net0,unused0" } }],
    ]);
  });

  // Two Saves before the refetch after the first lands: the second is built
  // on the first's afterSave, not on the fetched copy under both, or it would
  // undo the first's changes on screen and offer its deleted volume again.
  it("sends only its own changes on a second Save before the refetch, keeping the first's", async () => {
    holdRefetch();
    const user = userEvent.setup();
    const { save } = await openPanel();
    mockedPut.mockResolvedValueOnce({ status: "ok" });
    const memory = fieldInput("Memory (MB)");
    await user.clear(memory);
    await user.type(memory, "2048");
    await stage(user, "unused0");
    await user.click(save);
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Delete and Save",
      }),
    );
    await screen.findByText("Saved");
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });

    const second = deferred<unknown>();
    mockedPut.mockReturnValueOnce(second.promise);
    const cores = fieldInput("Cores");
    await user.clear(cores);
    await user.type(cores, "4");
    await user.click(save);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(mockedPut.mock.calls[1]).toEqual([
      CONFIG_URL,
      { fields: { cores: "4" } },
    ]);
    await act(async () => {
      second.resolve({ status: "ok" });
      await second.promise;
    });

    await screen.findByText("Saved");
    await waitFor(() => {
      expect(save).toBeDisabled();
    });
    expect(fieldInput("Memory (MB)")).toHaveValue(2048);
    expect(fieldInput("Cores")).toHaveValue(4);
    expect(screen.queryByText("unused0")).not.toBeInTheDocument();
    expect(screen.queryByText(/\d+ changes?/)).not.toBeInTheDocument();
  });

  it("asks a second Save before the refetch about its own volume only, and keeps the first's gone", async () => {
    holdRefetch();
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    const { save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Delete and Save",
      }),
    );
    await screen.findByText("Saved");
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(screen.queryByText("unused0")).not.toBeInTheDocument();

    await stage(user, "unused2");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    expect(listedVolumes(dialog)).toEqual(["unused2 store03:vm-200-disk-3"]);
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(mockedPut.mock.calls[1]).toEqual([
      CONFIG_URL,
      { fields: { delete: "unused2" } },
    ]);
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(screen.queryByText("unused0")).not.toBeInTheDocument();
    expect(screen.queryByText("unused2")).not.toBeInTheDocument();
    expect(unusedRow("unused1")).toBeInTheDocument();
    expect(save).toBeDisabled();
  });

  it("gives way to the refetched config, which may reuse a deleted key for another volume", async () => {
    // Proxmox frees unused0, then a move elsewhere that kept its source parks
    // that source at the lowest free key: unused0 again, another volume.
    mockedPut.mockImplementation((path: string, body?: unknown) => {
      const done = serverDropsDeletedKeys(path, body);
      serverConfig = { ...serverConfig, unused0: "store04:vm-200-disk-5" };
      return done;
    });
    const user = userEvent.setup();
    const { save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    await user.click(
      within(await screen.findByRole("alertdialog")).getByRole("button", {
        name: "Delete and Save",
      }),
    );

    // Read from its row: in the render before the populate effect closes
    // it, the confirmation too lists what unused0 now holds.
    await waitFor(() => {
      expect(
        within(unusedRow("unused0")).getByText("store04:vm-200-disk-5"),
      ).toBeInTheDocument();
    });
    expect(screen.queryByText("store01:vm-200-disk-1")).not.toBeInTheDocument();
    expect(isStaged("unused0")).toBe(false);
    expect(save).toBeDisabled();
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  it("shows Proxmox's copy once a fetch lands after the Save, even one that equals the copy before", async () => {
    // Proxmox passes tags through get_unique_tags, which sorts them, so it
    // stores "b;a" as "a;b": what it already had. The fetch after the Save
    // brings back the same data, and TanStack keeps the same object for it.
    serverConfig = { ...ctConfig(), tags: "a;b" };
    let gets = 0;
    mockedGet.mockImplementation((path: string) => {
      if (path !== CONFIG_URL) {
        return Promise.reject(new Error(`unexpected GET ${path}`));
      }
      gets += 1;
      const answer = { ...serverConfig };
      // Later fetches answer after a moment, as over a network, so each one
      // lands after the Save has succeeded.
      return gets === 1
        ? Promise.resolve(answer)
        : new Promise((resolve) => {
            setTimeout(() => {
              resolve(answer);
            }, 20);
          });
    });
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    const before = queryClient.getQueryState(CONFIG_KEY);
    const tags = fieldInput("Tags");
    expect(tags).toHaveValue("a;b");
    await user.clear(tags);
    await user.type(tags, "b;a");

    await user.click(save);

    await waitFor(() => {
      expect(
        queryClient.getQueryState(CONFIG_KEY)?.dataUpdatedAt,
      ).toBeGreaterThan(before?.dataUpdatedAt ?? Infinity);
    });
    // Landed, and kept the very object the Save was built on: only its time
    // says it came back after the write.
    expect(queryClient.getQueryData(CONFIG_KEY)).toBe(before?.data);
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { tags: "b;a" } }],
    ]);
    await waitFor(() => {
      expect(fieldInput("Tags")).toHaveValue("a;b");
    });
    expect(save).toBeDisabled();
  });

  it("never lays a Save over an older copy than one fetched while it was out", async () => {
    const put = deferred<unknown>();
    mockedPut.mockReturnValueOnce(put.promise);
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    const cores = fieldInput("Cores");
    await user.clear(cores);
    await user.type(cores, "4");
    await user.click(save);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });

    // Changed elsewhere while the Save is out, and fetched before it settles;
    // nothing fetched after it lands.
    serverConfig = { ...serverConfig, hostname: "linux02" };
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: CONFIG_KEY });
    });
    await waitFor(() => {
      expect(fieldInput("Hostname")).toHaveValue("linux02");
    });
    mockedGet.mockImplementation(() => new Promise(() => undefined));

    await act(async () => {
      put.resolve({ status: "ok" });
      await put.promise;
    });
    await screen.findByText("Saved");
    await act(async () => {
      await Promise.resolve();
    });
    expect(fieldInput("Hostname")).toHaveValue("linux02");
  });

  // Neither the unused volumes on the container nor a NIC removal is a volume
  // delete: a NIC removal travels in the same delete list, so a gate keyed on
  // that list, or on the section having rows, would stop this Save too.
  it("saves at once, with no dialog, when no volume is staged, even with a NIC removal", async () => {
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    const { save } = await openPanel();
    const cores = fieldInput("Cores");
    await user.clear(cores);
    await user.type(cores, "4");
    await user.click(screen.getByRole("button", { name: "Remove NIC" }));

    await user.click(save);

    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { cores: "4", delete: "net0" } }],
    ]);
    expect(mockedPost).not.toHaveBeenCalled();
    expect(mockedPatch).not.toHaveBeenCalled();
    expect(mockedDelete).not.toHaveBeenCalled();
  });

  // While the confirmation is open the populate effect waits, so a config
  // that changes under it alters neither the staging nor the list it shows,
  // the snapshot Delete and Save confirms. The change applies once it closes.
  it("keeps showing the list it opened on when the config changes under it, and applies the change once closed", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");

    await changedElsewhere(
      queryClient,
      { unused0: "store04:vm-200-disk-5", unused3: "store05:vm-200-disk-6" },
      "unused3",
    );

    expect(listedVolumes(dialog)).toEqual(["unused0 store01:vm-200-disk-1"]);
    expect(within(dialog).queryByRole("alert")).not.toBeInTheDocument();
    expectNothingMutated();

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(
        within(unusedRow("unused0")).getByText("store04:vm-200-disk-5"),
      ).toBeInTheDocument();
    });
    expect(isStaged("unused0")).toBe(false);
    expect(save).toBeDisabled();
    expectNothingMutated();
  });

  it("sends nothing when a staged key comes to hold another volume, and asks again on the one it holds now", async () => {
    mockedPut.mockImplementation(serverDropsDeletedKeys);
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    await changedElsewhere(
      queryClient,
      { unused0: "store04:vm-200-disk-5", unused3: "store05:vm-200-disk-6" },
      "unused3",
    );
    expect(listedVolumes(dialog)).toEqual(["unused0 store01:vm-200-disk-1"]);

    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    expectNothingMutated();
    expect(listedVolumes(dialog)).toEqual(["unused0 store04:vm-200-disk-5"]);
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      /changed.*nothing was deleted/i,
    );
    // On the notice, not a button, so a held Enter neither confirms the new
    // list nor cancels.
    expect(within(dialog).getByRole("alert")).toHaveFocus();

    // Confirmed again, on what the key holds now, it goes out once.
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { delete: "unused0" } }],
    ]);
  });

  it("sends nothing for a click that lands after a refetch but before any render shows it", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    const action = within(dialog).getByRole("button", {
      name: "Delete and Save",
    });

    serverConfig = {
      ...serverConfig,
      unused0: "store04:vm-200-disk-5",
      unused3: "store05:vm-200-disk-6",
    };
    await queryClient.invalidateQueries({ queryKey: CONFIG_KEY });
    // In the cache but not yet on screen: TanStack tells React on a timer.
    expect(queryClient.getQueryData(CONFIG_KEY)).toMatchObject({
      unused0: "store04:vm-200-disk-5",
    });
    expect(screen.queryByText("unused3")).not.toBeInTheDocument();
    fireEvent.click(action);

    await waitFor(() => {
      expect(listedVolumes(dialog)).toEqual(["unused0 store04:vm-200-disk-5"]);
    });
    expectNothingMutated();
  });

  it.each([
    ["a sibling appears", { unused3: "store05:vm-200-disk-6" }, "unused3"],
    [
      "only what it does not delete changes",
      { rootfs: "store01:vm-200-disk-0,size=10G" },
      "10G",
    ],
  ])(
    "sends the one PUT when %s while the confirmation is open",
    async (_what, change, rendered) => {
      mockedPut.mockResolvedValue({ status: "ok" });
      const user = userEvent.setup();
      const { queryClient, save } = await openPanel();
      await stage(user, "unused0");
      await user.click(save);
      const dialog = await screen.findByRole("alertdialog");

      await changedElsewhere(queryClient, change, rendered);
      await user.click(
        within(dialog).getByRole("button", { name: "Delete and Save" }),
      );

      await waitFor(() => {
        expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      });
      expect(mockedPut.mock.calls).toEqual([
        [CONFIG_URL, { fields: { delete: "unused0" } }],
      ]);
    },
  );

  it("does not let the second click of a double-click confirm the list the first asked again on", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    await changedElsewhere(
      queryClient,
      { unused0: "store04:vm-200-disk-5", unused3: "store05:vm-200-disk-6" },
      "unused3",
    );

    await user.dblClick(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    expect(listedVolumes(dialog)).toEqual(["unused0 store04:vm-200-disk-5"]);
    expectNothingMutated();
  });

  // What a Save writes besides its volumes is built on the config the fields
  // were populated from, which a refetch while the dialog is open does not
  // replace. Sent over a newer value it would undo a change made elsewhere.
  it("saves nothing when another NIC took the key a new one would use while the confirmation was open", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    // net1 is free when the fields are built, so the new NIC gets it.
    await user.click(
      screen.getByRole("button", { name: /add network interface/i }),
    );
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");

    await changedElsewhere(
      queryClient,
      {
        net1: "name=eth1,bridge=vmbr1,hwaddr=02:00:00:00:00:02,ip=dhcp,type=veth",
      },
      CHANGED_UNDER,
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    expectNothingMutated();
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      /nothing was saved/i,
    );
    expect(within(dialog).getByRole("alert")).toHaveFocus();
    expect(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    ).toBeDisabled();
  });

  it("saves nothing over a field that changed elsewhere while the confirmation was open", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    const memory = fieldInput("Memory (MB)");
    await user.clear(memory);
    await user.type(memory, "2048");
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");

    await changedElsewhere(queryClient, { memory: 4096 }, CHANGED_UNDER);
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    expectNothingMutated();
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      /nothing was saved/i,
    );
  });

  // A NIC the Save removes is checked like any other key it writes: the
  // delete would otherwise remove whatever NIC now holds that key.
  it("saves nothing when a NIC it removes was replaced elsewhere while the confirmation was open", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await user.click(screen.getByRole("button", { name: "Remove NIC" }));
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");

    await changedElsewhere(
      queryClient,
      {
        net0: "name=eth0,bridge=vmbr1,hwaddr=02:00:00:00:00:09,ip=dhcp,type=veth",
      },
      CHANGED_UNDER,
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    expectNothingMutated();
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      /nothing was saved/i,
    );
  });

  // A staged key that is gone was removed elsewhere; Proxmox refills the
  // lowest free unusedN, so a delete sent for it could reach another volume.
  it("drops a staged key that is gone and asks again on the rest", async () => {
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await stage(user, "unused0");
    await stage(user, "unused2");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");

    // Removed elsewhere.
    serverConfig = Object.fromEntries(
      Object.entries(serverConfig).filter(([key]) => key !== "unused0"),
    );
    await changedElsewhere(queryClient, {}, CHANGED_UNDER);
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    expectNothingMutated();
    expect(listedVolumes(dialog)).toEqual(["unused2 store03:vm-200-disk-3"]);
    expect(within(dialog).getByRole("alert")).toHaveFocus();
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { delete: "unused2" } }],
    ]);
  });

  // Closing lets the waiting config in, which reloads the panel, so the rest
  // of the Save goes too, and the notice has to say so.
  it("closes, saving nothing, when every staged key is gone, and says the reload discarded the rest", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    const memory = fieldInput("Memory (MB)");
    await user.clear(memory);
    await user.type(memory, "2048");
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");

    // Removed elsewhere.
    serverConfig = Object.fromEntries(
      Object.entries(serverConfig).filter(([key]) => key !== "unused0"),
    );
    await changedElsewhere(queryClient, {}, CHANGED_UNDER);
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(screen.getByRole("status")).toHaveTextContent(
      /no longer on this container, so nothing was saved, and the panel reloaded, discarding any other unsaved changes/i,
    );
    await waitFor(() => {
      expect(fieldInput("Memory (MB)")).toHaveValue(1024);
    });
    expect(save).toBeDisabled();
    expectNothingMutated();
  });

  it("drops 'nothing was deleted' once a Save goes out, so a failure does not claim it", async () => {
    mockedPut.mockRejectedValueOnce(
      new ApiClientError(400, {
        error: "bad request",
        message: "unable to apply pending change",
      }),
    );
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    await changedElsewhere(
      queryClient,
      { unused0: "store04:vm-200-disk-5" },
      CHANGED_UNDER,
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );
    expect(within(dialog).getByRole("alert")).toHaveTextContent(
      /nothing was deleted/i,
    );

    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );

    expect(
      await within(dialog).findByText("unable to apply pending change"),
    ).toBeInTheDocument();
    expect(mockedPut).toHaveBeenCalledTimes(1);
    expect(
      within(dialog).queryByText(/nothing was deleted/i),
    ).not.toBeInTheDocument();
  });

  it("says Cancel will reload once the config has changed under it, and Cancel does", async () => {
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    const memory = fieldInput("Memory (MB)");
    await user.clear(memory);
    await user.type(memory, "2048");
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).queryByText(CHANGED_UNDER)).not.toBeInTheDocument();

    await changedElsewhere(queryClient, { hostname: "linux02" }, CHANGED_UNDER);
    expect(within(dialog).getByRole("status")).toHaveTextContent(
      /cancel reloads it/i,
    );

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(fieldInput("Hostname")).toHaveValue("linux02");
    });
    expect(fieldInput("Memory (MB)")).toHaveValue(1024);
    expect(isStaged("unused0")).toBe(false);
    expect(save).toBeDisabled();
    // And the next Save is built on the config it reloaded.
    const cores = fieldInput("Cores");
    await user.clear(cores);
    await user.type(cores, "3");
    await user.click(save);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { cores: "3" } }],
    ]);
  });

  it("lets a held Enter neither confirm nor cancel once it has asked again", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    await changedElsewhere(
      queryClient,
      { unused0: "store04:vm-200-disk-5" },
      CHANGED_UNDER,
    );
    within(dialog).getByRole("button", { name: "Delete and Save" }).focus();

    // The first Enter asks again; the repeats land where that moved focus.
    await user.keyboard("{Enter>3}");

    expectNothingMutated();
    expect(dialog).toBeInTheDocument();
    expect(listedVolumes(dialog)).toEqual(["unused0 store04:vm-200-disk-5"]);
    expect(within(dialog).getByRole("alert")).toHaveFocus();
  });

  // After one ask again the operator can Tab back to Delete and Save; the next
  // must move focus again, or a held Enter confirms the list it has only just
  // shown. The notice is mounted afresh too, so a screen reader announces it
  // again, though its words are the same.
  it("moves focus to a notice mounted afresh every time it asks again, not only the first", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    const action = () =>
      within(dialog).getByRole("button", { name: "Delete and Save" });
    await changedElsewhere(
      queryClient,
      { unused0: "store04:vm-200-disk-5" },
      CHANGED_UNDER,
    );
    action().focus();
    await user.keyboard("{Enter}");
    const first = within(dialog).getByRole("alert");
    expect(first).toHaveFocus();

    // Back on Delete and Save, and the key changes again.
    action().focus();
    await changedElsewhere(
      queryClient,
      { unused0: "store05:vm-200-disk-6", unused3: "store06:vm-200-disk-7" },
      "unused3",
    );
    await user.keyboard("{Enter}");

    expect(listedVolumes(dialog)).toEqual(["unused0 store05:vm-200-disk-6"]);
    expect(first).not.toBeInTheDocument();
    expect(within(dialog).getByRole("alert")).toHaveFocus();
    await user.keyboard("{Enter}");
    expectNothingMutated();
    expect(listedVolumes(dialog)).toEqual(["unused0 store05:vm-200-disk-6"]);
  });

  it("confirms from the keyboard: Enter on Delete and Save sends the one PUT", async () => {
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    const { save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");
    within(dialog).getByRole("button", { name: "Delete and Save" }).focus();

    await user.keyboard("{Enter}");

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { delete: "unused0" } }],
    ]);
  });

  it("confirms from a click that carries no click count, as assistive technology sends", async () => {
    mockedPut.mockResolvedValue({ status: "ok" });
    const user = userEvent.setup();
    const { save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    const dialog = await screen.findByRole("alertdialog");

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
      { detail: 0 },
    );

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(mockedPut.mock.calls).toEqual([
      [CONFIG_URL, { fields: { delete: "unused0" } }],
    ]);
  });

  // TanStack stamps dataUpdatedAt with Date.now(), as saveChanges stamps
  // `at`, so one frozen clock puts the load and the write in one millisecond:
  // counted as before the write, that fetch leaves the afterSave in place.
  it("keeps the afterSave over a fetch stamped in the same millisecond as the write", async () => {
    const clock = vi.spyOn(Date, "now").mockReturnValue(1_800_000_000_000);
    try {
      holdRefetch();
      mockedPut.mockResolvedValue({ status: "ok" });
      const user = userEvent.setup();
      const { save } = await openPanel();
      await stage(user, "unused0");
      await user.click(save);
      await user.click(
        within(await screen.findByRole("alertdialog")).getByRole("button", {
          name: "Delete and Save",
        }),
      );
      await screen.findByText("Saved");
      await waitFor(() => {
        expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      });

      expect(screen.queryByText("unused0")).not.toBeInTheDocument();
      expect(save).toBeDisabled();
    } finally {
      clock.mockRestore();
    }
  });

  it("keeps an open confirmation through a failed refetch, and still checks it once the config is back", async () => {
    const user = userEvent.setup();
    const { queryClient, save } = await openPanel();
    await stage(user, "unused0");
    await user.click(save);
    await screen.findByRole("alertdialog");

    mockedGet.mockImplementationOnce(() => Promise.reject(new Error("boom")));
    await act(async () => {
      await queryClient
        .invalidateQueries({ queryKey: CONFIG_KEY })
        .catch(() => undefined);
    });
    await screen.findByText(/failed to load container config/i);
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();

    // Back, with unused0 now holding another volume.
    await changedElsewhere(
      queryClient,
      { unused0: "store04:vm-200-disk-5" },
      CHANGED_UNDER,
    );
    const dialog = screen.getByRole("alertdialog");
    expect(listedVolumes(dialog)).toEqual(["unused0 store01:vm-200-disk-1"]);
    await user.click(
      within(dialog).getByRole("button", { name: "Delete and Save" }),
    );
    expectNothingMutated();
    expect(listedVolumes(dialog)).toEqual(["unused0 store04:vm-200-disk-5"]);
  });
});

// saveChanges sends an unusedN delete only when its caller passes the list the
// operator confirmed and that list still matches what the request destroys.
// That protects every Save only while nothing else sends one, only the dialog
// passes a confirmed list, and that list is the one it showed rather than one
// worked out at click time (which would always match). No render can show
// that: a keyboard shortcut added later would be a caller no test here clicks.
// So this reads the source.
describe("ContainerResourcesPanel save gate", () => {
  it("sends only from saveChanges, and takes a confirmation only from the dialog's own button", () => {
    // One way to the API: the one instance of the hook, and nothing beside it.
    expect(panelSource.match(/\buseSetResourceConfig\(/g)).toHaveLength(1);
    expect(panelSource).not.toMatch(/\bapiClient\b/);
    expect(panelSource).not.toMatch(/\bfetch\s*\(/);

    // The hook's result is only ever read through a member, never handed on
    // whole (destructured, aliased, passed or indexed), and exactly one of
    // those reads is a send (mutate or mutateAsync), inside saveChanges.
    const declared = panelSource.indexOf(
      "const setConfigMutation = useSetResourceConfig();",
    );
    expect(declared).toBeGreaterThan(-1);
    const reads = Array.from(
      panelSource.matchAll(/\bsetConfigMutation\b(?:\s*\.\s*(\w+))?/g),
    ).filter((read) => read.index !== declared + "const ".length);
    const snippet = (read: RegExpExecArray) =>
      panelSource.slice(read.index, read.index + 48);
    expect(reads.filter((read) => read[1] === undefined).map(snippet)).toEqual(
      [],
    );
    const sends = reads.filter(
      (read) => read[1] === "mutate" || read[1] === "mutateAsync",
    );
    expect(sends.map(snippet)).toHaveLength(1);
    const start = panelSource.indexOf("\n  function saveChanges(");
    const end = panelSource.indexOf("\n  }\n", start);
    expect(start).toBeGreaterThan(-1);
    expect(sends[0]?.index).toBeGreaterThan(start);
    expect(sends[0]?.index).toBeLessThan(end);

    // Every call passes no list, or the list the dialog showed, and only the
    // dialog's own button passes that.
    const shown = "{ confirmed: confirmation.volumes }";
    const verdicts = Array.from(
      panelSource.matchAll(/(?<!function )\bsaveChanges\(([^)]*)\)/g),
      (call) => call[1],
    );
    for (const verdict of verdicts) {
      expect(["{ confirmed: null }", shown]).toContain(verdict);
    }
    expect(verdicts.filter((v) => v === shown)).toHaveLength(1);
    // And some caller passes none: the Save button, which the first test
    // above shows opening the confirmation.
    expect(verdicts).toContain("{ confirmed: null }");
    const action = panelSource.slice(
      panelSource.indexOf("<AlertDialogAction"),
      panelSource.indexOf("</AlertDialogAction>"),
    );
    expect(action).toContain(`saveChanges(${shown})`);
  });
});
