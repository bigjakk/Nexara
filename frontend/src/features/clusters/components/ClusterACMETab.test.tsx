import { afterEach, describe, expect, it, vi, beforeEach } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ClusterACMETab } from "./ClusterACMETab";
import { ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import { deferred } from "@/test/fake-server";
import {
  type UserEvent,
  expectNoToast,
  expectOneToast,
  expectSilence,
  heldOnce,
  row,
} from "@/test/late-save-kit";
import {
  SESSION_ENDS,
  signInAsAdmin,
  signOutForGood,
  toastsRaised,
} from "@/test/late-toast-sessions";
import {
  DENIED,
  denied,
  flushInAct as flush,
  waitForSuccess,
} from "@/test/save-outcome-kit";
import type { NodeACMEConfig } from "@/features/acme/api/acme-queries";

/**
 * The tab does not use useSaveOutcome: saveDomain settles its save itself, with
 * a mounted flag, a generation for the dialog and node it was sent from, and the
 * session (sessionScope). The first block is what a save carries (slot, digest,
 * pin); the rest is who hears of its answer, wherever the tab is by then.
 * Wherever a toast is asserted the tab is on the app's own client
 * (test/app-query-client.ts), where the global toast exists.
 */

const CLUSTER = "cccccccc-0000-0000-0000-000000000002";
const NODE = "pve-01";
const NODES_PATH = `/api/v1/clusters/${CLUSTER}/nodes`;
const CONFIG_PATH = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/acme-config`;

const listMock = vi.fn();
const getMock = vi.fn();
const putMock = vi.fn();

// Spread the real module: api-error.ts imports ApiClientError from here, and a
// mock that only supplies apiClient makes describeError throw on every render.
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
      put: (path: string, body: unknown) => putMock(path, body) as unknown,
      post: vi.fn(),
      delete: vi.fn(),
    },
  };
});

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

// The app's mutation-error net toasts through sonner, so this one mock sees every
// toast a run can raise, of any kind: "toasts nothing" is not satisfied by a
// success or a warning.
vi.mock("sonner", () => ({
  toast: Object.assign(vi.fn(), {
    success: vi.fn(),
    info: vi.fn(),
    warning: vi.fn(),
    error: vi.fn(),
    message: vi.fn(),
    loading: vi.fn(),
  }),
}));

const NODE_2 = "pve-02";
const CONFIG_PATH_2 = `/api/v1/clusters/${CLUSTER}/nodes/${NODE_2}/acme-config`;
const NODE_0 = "pve-00";
const CONFIG_PATH_0 = `/api/v1/clusters/${CLUSTER}/nodes/${NODE_0}/acme-config`;
// What the server says to a stale digest, and what the dialog adds: its fields
// do not reload, so saving again overwrites.
const STALE =
  "The node's configuration changed since it was read — reload and try again.";
const SAVING_AGAIN =
  "Saving again will overwrite the node's current configuration with the values shown here.";
const CONNECTION_FAILED =
  "The save request failed — check your connection and try again.";

/** Two domains on the first node, at digest d1. */
const TWO_DOMAINS: NodeACMEConfig = {
  acmedomain0: "domain=node1.example.com",
  acmedomain1: "domain=node2.example.com",
  digest: "d1",
};
const later = (digest: string): NodeACMEConfig => ({ ...TWO_DOMAINS, digest });

/** The toast a save of a domain (the first, unless said) on the first node leaves when it fails late. */
function failedToast(message: string, domain = "node1.example.com"): string {
  return `Saving the ACME domain ${domain} on ${NODE} failed: ${message}`;
}

const conflict = () =>
  new ApiClientError(409, { error: "conflict", message: STALE });
const badGateway = () => new ApiClientError(502, { error: "bad", message: "" });

/**
 * The tab on `qc`. The default is a bare client with no mutation-error net,
 * enough for what is shown on screen; a test that asserts a toast, or the lack
 * of one, passes createAppQueryClient(), since on a bare one "no toast" passes
 * with the hook's opt-out removed.
 */
function renderTab(
  qc: QueryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  }),
) {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  return { qc, ...render(<ClusterACMETab clusterId={CLUSTER} />, { wrapper }) };
}

/** Opens the edit dialog on the nth configured domain, defaulting to the first. */
async function openEdit(user: UserEvent, index = 0) {
  const rows = await screen.findAllByRole("button", { name: "Edit" });
  const edit = rows[index];
  if (!edit) throw new Error(`no Edit button at index ${String(index)}`);
  await user.click(edit);
}

function configGets() {
  return getMock.mock.calls.filter((c) => c[0] === CONFIG_PATH);
}

/** The body of the nth PUT. */
function sentBody(n = 0): NodeACMEConfig {
  return putMock.mock.calls[n]?.[1] as NodeACMEConfig;
}

/**
 * Serves the node list, and an acme-config read per GET in turn (an Error
 * rejects), the last repeating: a test that only cares about the first read
 * passes one.
 */
function serve(reads: (NodeACMEConfig | Error)[]) {
  listMock.mockImplementation((path: string) =>
    path === NODES_PATH
      ? Promise.resolve([{ name: NODE, node_name: NODE }])
      : Promise.resolve([]),
  );
  let call = 0;
  getMock.mockImplementation((path: string) => {
    if (path !== CONFIG_PATH) return Promise.resolve(null);
    const read = reads[Math.min(call, reads.length - 1)];
    call += 1;
    return read instanceof Error ? Promise.reject(read) : Promise.resolve(read);
  });
}

/** Opens the Node Certificates tab and waits for the first config read. */
async function openCertificatesTab(user: UserEvent) {
  await user.click(screen.getByRole("tab", { name: "Node Certificates" }));
  await waitFor(() => {
    expect(getMock).toHaveBeenCalledWith(CONFIG_PATH);
  });
}

async function typeDomainAndSave(user: UserEvent, domain: string) {
  await user.type(screen.getByPlaceholderText("node1.example.com"), domain);
  await user.click(screen.getByRole("button", { name: "Save" }));
}

async function pressSave(user: UserEvent) {
  await user.click(screen.getByRole("button", { name: "Save" }));
}

beforeEach(() => {
  vi.resetAllMocks();
  putMock.mockResolvedValue({ status: "ok" });
});

describe("ClusterACMETab domain save", () => {
  it("sends the config digest and never the cached ACME account", async () => {
    const user = userEvent.setup();
    serve([{ acme: "account=acme-account-01", digest: "d1" }]);
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await typeDomainAndSave(user, "node1.example.com");

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    expect(sentBody().digest).toBe("d1");
    expect(sentBody().acmedomain0).toBe("domain=node1.example.com");
    // The account rode along on every domain save and wrote the cached copy
    // back over whatever Proxmox actually had.
    expect(sentBody()).not.toHaveProperty("acme");
  });

  it("resolves an added domain's slot against the config the digest came from", async () => {
    const user = userEvent.setup();
    // The read behind the open dialog shows slot 1 free; the refetch that
    // opening the dialog triggers shows another operator has taken it. The save
    // must land on slot 2, not overwrite them with a digest that now matches.
    serve([
      { acmedomain0: "node1.example.com", digest: "d1" },
      {
        acmedomain0: "node1.example.com",
        acmedomain1: "node2.example.com",
        digest: "d2",
      },
    ]);
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    expect(await screen.findByText("node2.example.com")).toBeInTheDocument();
    await typeDomainAndSave(user, "node3.example.com");

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    expect(sentBody().digest).toBe("d2");
    expect(sentBody().acmedomain2).toBe("domain=node3.example.com");
    expect(sentBody()).not.toHaveProperty("acmedomain1");
  });

  it("keeps an edited domain's own digest instead of refreshing it underneath", async () => {
    const user = userEvent.setup();
    // Only an add refetches on open: an edit's fields come from the config on
    // screen, so a newer digest under them would let the compare-and-swap pass
    // and overwrite a version of the slot the operator never saw.
    serve([
      { acmedomain0: "domain=node1.example.com", digest: "d1" },
      { acmedomain0: "domain=node2.example.com", digest: "d2" },
    ]);
    renderTab();
    await openCertificatesTab(user);

    expect(await screen.findByText("node1.example.com")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    // Before the save: afterwards its own invalidation refetches legitimately.
    expect(configGets()).toHaveLength(1);

    await pressSave(user);

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    expect(sentBody().digest).toBe("d1");
    expect(sentBody().acmedomain0).toBe("domain=node1.example.com");
  });

  it("refuses to save while the node config is unread", async () => {
    const user = userEvent.setup();
    // Unread, the free-slot scan answers 0 and no digest is attached, so the
    // write would blindly overwrite acmedomain0 with the check switched off.
    serve([badGateway()]);
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await user.type(
      screen.getByPlaceholderText("node1.example.com"),
      "node1.example.com",
    );

    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await pressSave(user);
    expect(putMock).not.toHaveBeenCalled();
  });

  it("reports a dropped connection rather than failing silently", async () => {
    const user = userEvent.setup();
    serve([{ digest: "d1" }]);
    // describeError returns "" for a TypeError, and the hook has opted out of
    // the global toast: without a fallback this renders as nothing.
    putMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await typeDomainAndSave(user, "node1.example.com");

    expect(
      await screen.findByText(/check your connection and try again/),
    ).toBeInTheDocument();
  });

  // A refetch can land under an open edit: a WebSocket reconnect invalidates
  // every active query, and so does ordering or renewing a certificate. Read live
  // at save time, the digest would move to d2 while the form still showed the d1
  // values, and the compare-and-swap would pass and destroy the change behind d2.
  // A failed one flips the query to "error" and keeps the data, so gating Save
  // on isSuccess would strand every edit on a node whose six slots are full.
  const REFETCHES: [name: string, next: NodeACMEConfig | Error][] = [
    [
      "a refetch that finds a newer digest",
      { acmedomain0: "domain=node2.example.com", digest: "d2" },
    ],
    ["a refetch that fails", badGateway()],
  ];
  it.each(REFETCHES)(
    "saves an edit with the digest it was opened with after %s",
    async (_, next) => {
      const user = userEvent.setup();
      serve([{ acmedomain0: "domain=node1.example.com", digest: "d1" }, next]);
      const { qc } = renderTab();
      await openCertificatesTab(user);
      await openEdit(user);

      await qc.invalidateQueries();
      await waitFor(() => {
        expect(configGets()).toHaveLength(2);
      });
      if (!(next instanceof Error)) {
        expect(
          await screen.findByText("node2.example.com"),
        ).toBeInTheDocument();
      }

      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
      await pressSave(user);
      await waitFor(() => {
        expect(putMock).toHaveBeenCalledTimes(1);
      });
      expect(sentBody().digest).toBe("d1");
    },
  );

  // The one thing allowed to move a pinned digest is a conflict the operator has
  // been shown: without that, an edit could never be retried. The dialog stays
  // open carrying what was typed and says that saving again overwrites, so the
  // retry is deliberate, not made on the operator's behalf. An add takes its
  // digest from the newest read, which includes the one opening the dialog.
  const CONFLICTS: [
    name: string,
    reads: NodeACMEConfig[],
    start: (user: UserEvent) => Promise<void>,
    readsAfter: number,
    retryDigest: string,
  ][] = [
    [
      "an edit",
      [TWO_DOMAINS, later("d2")],
      async (user) => {
        await openEdit(user);
        await pressSave(user);
      },
      2,
      "d2",
    ],
    [
      "an add",
      [{ digest: "d1" }, { digest: "d2" }, { digest: "d3" }],
      async (user) => {
        await user.click(screen.getByRole("button", { name: /Add Domain/ }));
        await typeDomainAndSave(user, "node1.example.com");
      },
      3,
      "d3",
    ],
  ];
  it.each(CONFLICTS)(
    "answers a conflict on %s in the dialog with the note that saving again overwrites, reads the node again and re-pins the digest for the retry",
    async (_, reads, start, readsAfter, retryDigest) => {
      const user = userEvent.setup();
      serve(reads);
      putMock.mockRejectedValueOnce(conflict());
      renderTab(createAppQueryClient());
      await openCertificatesTab(user);
      await start(user);

      const dialog = screen.getByRole("dialog");
      expect(await within(dialog).findByText(STALE)).toBeInTheDocument();
      expect(within(dialog).getByText(SAVING_AGAIN)).toBeInTheDocument();
      expect(
        within(dialog).getByPlaceholderText("node1.example.com"),
      ).toHaveValue("node1.example.com");
      expect(putMock).toHaveBeenCalledTimes(1);
      await waitFor(() => {
        expect(configGets()).toHaveLength(readsAfter);
      });

      await pressSave(user);
      await waitFor(() => {
        expect(putMock).toHaveBeenCalledTimes(2);
      });
      expect(sentBody(1).digest).toBe(retryDigest);
      await flush();
      expectNoToast();
    },
  );

  it("holds the pin when the conflict refetch itself fails", async () => {
    const user = userEvent.setup();
    // A failed refetch RETAINS the last successful data, so re-pinning on
    // `res.data` alone can never fail: it would move the pin to d2, a change
    // this dialog never saw, and the retry would overwrite it.
    serve([
      { acmedomain0: "domain=node1.example.com", digest: "d1" },
      { acmedomain0: "domain=node2.example.com", digest: "d2" },
      badGateway(),
    ]);
    putMock.mockRejectedValueOnce(conflict());
    const { qc } = renderTab();
    await openCertificatesTab(user);
    await openEdit(user);

    // The cache moves to d2 behind the open dialog, which still shows d1's
    // values; the conflict refetch that follows cannot replace it.
    await qc.invalidateQueries();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    await pressSave(user);
    expect(await screen.findByText(STALE)).toBeInTheDocument();
    await waitFor(() => {
      expect(configGets()).toHaveLength(3);
    });

    await pressSave(user);
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect(sentBody(1).digest).toBe("d1");
  });

  it("does not let one dialog's conflict refetch re-pin the next dialog", async () => {
    const user = userEvent.setup();
    // The conflict refetch is async and tied to nothing about the dialog that
    // fired it: cancelling and opening another row while it is in flight must
    // not land its digest on a pin just set to match different values.
    listMock.mockImplementation((path: string) =>
      path === NODES_PATH
        ? Promise.resolve([{ name: NODE, node_name: NODE }])
        : Promise.resolve([]),
    );
    let releaseRefetch: (() => void) | undefined;
    let call = 0;
    getMock.mockImplementation((path: string) => {
      if (path !== CONFIG_PATH) return Promise.resolve(null);
      call += 1;
      if (call === 1) return Promise.resolve(TWO_DOMAINS);
      return new Promise((resolve) => {
        releaseRefetch = () => {
          resolve(later("d3"));
        };
      });
    });
    putMock.mockRejectedValueOnce(conflict());
    renderTab();
    await openCertificatesTab(user);

    // First dialog: slot 0. Save conflicts and leaves a refetch hanging.
    await openEdit(user);
    await pressSave(user);
    expect(await screen.findByText(STALE)).toBeInTheDocument();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    // Second dialog: slot 1, pinned to the read still on screen.
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    await openEdit(user, 1);
    releaseRefetch?.();
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    });

    await pressSave(user);
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect(sentBody(1).acmedomain1).toBe("domain=node2.example.com");
    expect(sentBody(1).digest).toBe("d1");
  });
});

// ── Who hears of a save's answer ────────────────────────────────────────────

/** Opens the nth row's edit dialog and saves it, with the request held. */
async function saveHeld(user: UserEvent, index = 0) {
  const held = heldOnce(putMock);
  await openEdit(user, index);
  await pressSave(user);
  expect(
    await screen.findByRole("button", { name: "Saving..." }),
  ).toBeDisabled();
  return held;
}

async function cancelDialog(user: UserEvent) {
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  await waitFor(() => {
    expect(screen.queryByRole("dialog")).toBeNull();
  });
}

/** Cancel, then another tab: the tab is gone, the dialog with it. */
async function leaveForAccounts(user: UserEvent) {
  await cancelDialog(user);
  await user.click(screen.getByRole("tab", { name: "Accounts" }));
  expect(screen.queryByText("ACME Domain Configuration")).toBeNull();
}

/** The page is left with the dialog open. */
function leavePage() {
  cleanup();
  return Promise.resolve();
}

/** The tab is back, with a dialog of its own open, before the save lands. */
async function returnAndOpenSecond(user: UserEvent) {
  await user.click(screen.getByRole("tab", { name: "Node Certificates" }));
  await openEdit(user, 1);
  return screen.findByRole("dialog");
}

describe("a save that settles while the tab is still showing it", () => {
  it("shows a failure in the dialog, which stays open, with no toast and no note, and lets the same dialog save again", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS, later("d2")]);
    putMock.mockRejectedValueOnce(denied());
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    await openEdit(user);
    await pressSave(user);

    const dialog = screen.getByRole("dialog");
    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    expect(dialog).toHaveAttribute("data-state", "open");
    // The card behind it does not carry a second copy.
    expect(screen.getAllByText(DENIED)).toHaveLength(1);
    await flush();
    expectNoToast();
    // The note is for a conflict, which reads the node again; no other failure does.
    expect(within(dialog).queryByText(SAVING_AGAIN)).toBeNull();
    expect(configGets()).toHaveLength(1);

    await pressSave(user);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(putMock).toHaveBeenCalledTimes(2);
    await flush();
    expectNoToast();
  });

  it("shows a failure that lands after Cancel on the card, and toasts nothing", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await cancelDialog(user);
    held.reject(denied());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    await flush();
    expectNoToast();
  });
});

describe("a save that settles after its dialog was replaced", () => {
  /** The first row's save held, its dialog cancelled and the second row's open. */
  async function replaced(user: UserEvent) {
    serve([TWO_DOMAINS, later("d2")]);
    const view = renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    const held = await saveHeld(user);
    await cancelDialog(user);
    await openEdit(user, 1);
    const second = await screen.findByRole("dialog");
    return { ...view, held, second };
  }

  it("toasts a failure, naming what it was for, and shows it neither in the newer dialog nor on the card", async () => {
    const user = userEvent.setup();
    const { held, second } = await replaced(user);

    held.reject(denied());

    await expectOneToast(failedToast(DENIED));
    expect(second).toHaveAttribute("data-state", "open");
    expect(screen.queryByText(DENIED)).toBeNull();
    // Its Save is its own, not held on "Saving..." by the older dialog's.
    expect(within(second).getByRole("button", { name: "Save" })).toBeEnabled();
    // And it does not turn up on the card once that dialog is closed.
    await cancelDialog(user);
    expect(screen.queryByText(DENIED)).toBeNull();
  });

  it("does not close the newer dialog when it succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    const { qc, held, second } = await replaced(user);

    held.resolve({ status: "ok" });
    await waitForSuccess(qc);

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByDisplayValue("node2.example.com")).toBeVisible();
    expectNoToast();
    expect(putMock).toHaveBeenCalledTimes(1);
  });

  it("toasts a stale-digest refusal, and neither reads the node again nor moves the newer dialog's pin", async () => {
    const user = userEvent.setup();
    const { held, second } = await replaced(user);
    const before = configGets().length;

    held.reject(conflict());

    await expectOneToast(failedToast(STALE));
    expect(configGets()).toHaveLength(before);
    // The conflict is not the newer dialog's: no note of it there.
    expect(within(second).queryByText(SAVING_AGAIN)).toBeNull();
    expect(within(second).queryByText(STALE)).toBeNull();

    // Its own save still goes out against the read it was opened from.
    await user.click(within(second).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect(sentBody(1).acmedomain1).toBe("domain=node2.example.com");
    expect(sentBody(1).digest).toBe("d1");
  });
});

/** Two nodes: the second holds one domain of its own, at digest d9. */
function serveTwoNodes() {
  listMock.mockImplementation((path: string) =>
    path === NODES_PATH
      ? Promise.resolve([
          { name: NODE, node_name: NODE },
          { name: NODE_2, node_name: NODE_2 },
        ])
      : Promise.resolve([]),
  );
  getMock.mockImplementation((path: string) => {
    if (path === CONFIG_PATH) return Promise.resolve(TWO_DOMAINS);
    if (path === CONFIG_PATH_2) {
      return Promise.resolve({
        acmedomain0: "domain=node3.example.com",
        digest: "d9",
      });
    }
    return Promise.resolve(null);
  });
}

/**
 * One node, pve-01, until the function this returns lets another that sorts
 * before it, pve-00, into the list. The tab was never told to use pve-01: it is
 * its first node, and it follows the list.
 */
function serveNodeThatSortsFirst() {
  let nodes = [{ name: NODE, node_name: NODE }];
  listMock.mockImplementation((path: string) =>
    path === NODES_PATH ? Promise.resolve(nodes) : Promise.resolve([]),
  );
  getMock.mockImplementation((path: string) => {
    if (path === CONFIG_PATH) return Promise.resolve(TWO_DOMAINS);
    if (path === CONFIG_PATH_0) {
      return Promise.resolve({
        acmedomain0: "domain=zero.example.com",
        digest: "d0",
      });
    }
    return Promise.resolve(null);
  });
  return async (qc: QueryClient) => {
    nodes = [
      { name: NODE_0, node_name: NODE_0 },
      { name: NODE, node_name: NODE },
    ];
    await act(async () => {
      await qc.invalidateQueries({
        queryKey: ["clusters", CLUSTER, "nodes"],
        exact: true,
      });
    });
    // The tab is on pve-00 now: its domain is what the card behind shows.
    expect(await screen.findByText("zero.example.com")).toBeInTheDocument();
  };
}

// certNode follows the node list while the selector is unused, so it moves
// without anyone choosing when a node that sorts first joins; either way a save
// still out to the node left behind is no longer what the card shows, and is
// let go of the way a new dialog lets go of it.
describe("a save that settles after the tab moved to another node", () => {
  const MOVES: [
    name: string,
    serveAndMove: () => (user: UserEvent, qc: QueryClient) => Promise<void>,
    shows: string,
  ][] = [
    [
      "the selector choosing it",
      () => {
        serveTwoNodes();
        return async (user) => {
          await user.click(screen.getByRole("combobox"));
          await user.click(await screen.findByRole("option", { name: NODE_2 }));
          expect(
            await screen.findByText("node3.example.com"),
          ).toBeInTheDocument();
        };
      },
      "node3.example.com",
    ],
    [
      "the node list moving it",
      () => {
        const joins = serveNodeThatSortsFirst();
        return (_, qc) => joins(qc);
      },
      "zero.example.com",
    ],
  ];

  it.each(MOVES)(
    "toasts a failure, naming the node it was sent to, and gives the new node's card none of it, after %s",
    async (_, serveAndMove, shows) => {
      const user = userEvent.setup();
      const move = serveAndMove();
      const qc = createAppQueryClient();
      renderTab(qc);
      await openCertificatesTab(user);

      const held = await saveHeld(user);
      await cancelDialog(user);
      await move(user, qc);
      held.reject(denied());

      await expectOneToast(failedToast(DENIED));
      expect(screen.getByText(shows)).toBeInTheDocument();
      expect(screen.queryByText(DENIED)).toBeNull();
    },
  );

  // While a dialog is open it is the surface for what it sent, and its pin
  // refuses a send to the node now shown. Letting go of its save under it would
  // take its answer away.
  it.each([
    ["fails", "its failure shows there and is not toasted"],
    ["succeeds", "its success closes it"],
  ])(
    "leaves the save of a dialog that is still open to the dialog when it %s: %s",
    async (answer) => {
      const user = userEvent.setup();
      const joins = serveNodeThatSortsFirst();
      const qc = createAppQueryClient();
      renderTab(qc);
      await openCertificatesTab(user);

      const held = await saveHeld(user, 1);
      await joins(qc);
      if (answer === "fails") {
        held.reject(denied());
        const dialog = screen.getByRole("dialog");
        expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
        expect(dialog).toHaveAttribute("data-state", "open");
      } else {
        held.resolve({ status: "ok" });
        await waitFor(() => {
          expect(screen.queryByRole("dialog")).toBeNull();
        });
      }
      await flush();
      expectNoToast();
    },
  );

  // And once it has moved the tab is the same tab: it lets go of a save when the
  // node changes, not each time a dialog closes.
  it("answers a save of the node now shown, cancelled while it was out, on the card as anywhere else", async () => {
    const user = userEvent.setup();
    const joins = serveNodeThatSortsFirst();
    const qc = createAppQueryClient();
    renderTab(qc);
    await openCertificatesTab(user);
    await joins(qc);

    const held = await saveHeld(user);
    await cancelDialog(user);
    held.reject(denied());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    await flush();
    expectNoToast();
  });

  it("judges what a dialog left behind when it closes: a failure after that is toasted, not put on the new node's card", async () => {
    const user = userEvent.setup();
    const joins = serveNodeThatSortsFirst();
    const qc = createAppQueryClient();
    renderTab(qc);
    await openCertificatesTab(user);

    const held = await saveHeld(user, 1);
    await joins(qc);
    await cancelDialog(user);
    held.reject(denied());

    await expectOneToast(failedToast(DENIED, "node2.example.com"));
    expect(screen.queryByText(DENIED)).toBeNull();
  });
});

describe("a save that settles after the tab was left", () => {
  const LEFT: [
    name: string,
    leave: (user: UserEvent) => Promise<void>,
    failure: unknown,
    said: string,
  ][] = [
    ["Cancel and another tab", leaveForAccounts, denied(), DENIED],
    ["the page being left with the dialog open", leavePage, denied(), DENIED],
    [
      "Escape and another tab, for a request that got no answer",
      async (user) => {
        await user.keyboard("{Escape}");
        await waitFor(() => {
          expect(screen.queryByRole("dialog")).toBeNull();
        });
        await user.click(screen.getByRole("tab", { name: "Accounts" }));
      },
      // How fetch rejects when the connection drops: describeError has no words for it.
      new TypeError("Failed to fetch"),
      CONNECTION_FAILED,
    ],
  ];
  it.each(LEFT)(
    "toasts the failure that comes after %s, once, naming the node and the domain",
    async (_, leave, failure, said) => {
      const user = userEvent.setup();
      serve([TWO_DOMAINS]);
      renderTab(createAppQueryClient());
      await openCertificatesTab(user);

      const held = await saveHeld(user);
      await leave(user);
      expect(screen.queryByText("ACME Domain Configuration")).toBeNull();
      held.reject(failure);

      await expectOneToast(failedToast(said));
      expect(putMock).toHaveBeenCalledTimes(1);
    },
  );

  it("toasts a stale-digest refusal, and does not read the node again", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS, later("d2")]);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await leaveForAccounts(user);
    const before = configGets().length;
    held.reject(conflict());

    // A live conflict reads the node again to re-pin; a tab that is gone has no
    // dialog to re-pin.
    await expectOneToast(failedToast(STALE));
    expect(configGets()).toHaveLength(before);
  });

  it.each(["fails", "succeeds"])(
    "when it %s, leaves alone the dialog the tab opened on its return and shows nothing of it there",
    async (answer) => {
      const user = userEvent.setup();
      serve([TWO_DOMAINS]);
      const { qc } = renderTab(createAppQueryClient());
      await openCertificatesTab(user);

      const held = await saveHeld(user);
      await leaveForAccounts(user);
      const second = await returnAndOpenSecond(user);

      if (answer === "fails") {
        held.reject(denied());
        await expectOneToast(failedToast(DENIED));
        expect(screen.queryByText(DENIED)).toBeNull();
        expect(
          within(second).getByRole("button", { name: "Save" }),
        ).toBeEnabled();
      } else {
        held.resolve({ status: "ok" });
        await waitForSuccess(qc);
        expect(
          within(second).getByDisplayValue("node2.example.com"),
        ).toBeVisible();
        expectNoToast();
      }
      expect(second).toHaveAttribute("data-state", "open");
    },
  );
});

// isPending, and the Save button that reads it, trails a click by a task:
// TanStack tells its observers on a timer. Two clicks inside that task reach
// saveDomain with the button still enabled, and sent two requests carrying the
// same digest; the second is refused as stale because of the first, a conflict
// the operator caused. The clicks below are fired back to back with nothing
// awaited between them, which is how two get in.

/**
 * Lets queued promise callbacks run until `done` (or, with no `done`, for a few
 * hundred hops, longer than any chain of them here), and nothing else: no timer
 * fires, so TanStack has not yet told React what the save did, and the Save
 * button is still the one the last click found. The request itself is a few hops
 * behind the click, since a mutation awaits its onMutate first.
 */
async function microtasksUntil(done: () => boolean = () => false) {
  for (let hops = 0; hops < 300 && !done(); hops += 1) {
    await Promise.resolve();
  }
}

describe("a Save pressed twice before the button has been disabled", () => {
  it("sends one request, and shows its failure in the dialog", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    const held = heldOnce(putMock);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    await openEdit(user);

    const save = screen.getByRole("button", { name: "Save" });
    fireEvent.click(save);
    // The premise: the first click has not reached the button yet. If React
    // were told at once, the second click would find it disabled, and this
    // would pass with no guard at all.
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await microtasksUntil();
    expect(putMock).toHaveBeenCalledTimes(1);

    held.reject(denied());
    const dialog = screen.getByRole("dialog");
    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flush();
    expect(putMock).toHaveBeenCalledTimes(1);
    expectNoToast();
  });

  // The guard is released in each handler, each by a line of its own, and each
  // needs its own proof. A newer dialog's Save stays open while an older
  // dialog's save is out (so its first click goes out), and the older answer
  // must not release the guard on the newer one.
  const OLDER_ANSWERS: [
    name: string,
    answer: (older: ReturnType<typeof deferred<unknown>>) => void,
    afterwards: () => Promise<void>,
  ][] = [
    [
      "failure",
      (older) => {
        older.reject(denied());
      },
      async () => {
        // The toast is the proof that the older handler ran.
        await microtasksUntil(() => toastsRaised().length > 0);
        expect(toastsRaised()).toEqual([`error: ${failedToast(DENIED)}`]);
      },
    ],
    [
      "success",
      (older) => {
        older.resolve({ status: "ok" });
      },
      // A success raises nothing to wait for.
      () => microtasksUntil(),
    ],
  ];
  it.each(OLDER_ANSWERS)(
    "does not let an older dialog's %s release a newer dialog's save",
    async (_, answerOlder, afterAnswer) => {
      const user = userEvent.setup();
      serve([TWO_DOMAINS]);
      renderTab(createAppQueryClient());
      await openCertificatesTab(user);

      const older = await saveHeld(user);
      await cancelDialog(user);
      const newer = heldOnce(putMock);
      await openEdit(user, 1);
      const save = screen.getByRole("button", { name: "Save" });
      fireEvent.click(save);
      await microtasksUntil(() => putMock.mock.calls.length === 2);
      expect(putMock).toHaveBeenCalledTimes(2);
      expect(sentBody(1).acmedomain1).toBe("domain=node2.example.com");

      // The older save is answered while the newer one is out, and nothing yet
      // has told the button so.
      answerOlder(older);
      await afterAnswer();

      // The premise again: still enabled, so this click reaches saveDomain.
      expect(save).toBeEnabled();
      fireEvent.click(save);
      await microtasksUntil();
      expect(putMock).toHaveBeenCalledTimes(2);

      newer.resolve({ status: "ok" });
      await flush();
    },
  );
});

// ── The node a dialog was opened for ────────────────────────────────────────

describe("a dialog whose node stops being the one shown", () => {
  const REFUSAL = `This dialog was opened for ${NODE}, which is no longer the node shown. Close it and try again.`;

  const OPENED: [name: string, open: (user: UserEvent) => Promise<void>][] = [
    ["an edit", (user) => openEdit(user, 1)],
    [
      "an add",
      async (user) => {
        await user.click(screen.getByRole("button", { name: /Add Domain/ }));
        await user.type(
          screen.getByPlaceholderText("node1.example.com"),
          "node3.example.com",
        );
      },
    ],
  ];
  it.each(OPENED)(
    "refuses to send %s, and says why, while a dialog for the node it was opened for still saves",
    async (_, open) => {
      const user = userEvent.setup();
      const joins = serveNodeThatSortsFirst();
      const qc = createAppQueryClient();
      renderTab(qc);
      await openCertificatesTab(user);
      await open(user);
      await joins(qc);

      const dialog = screen.getByRole("dialog");
      // An add waits for the live config before it can be saved at all.
      await waitFor(() => {
        expect(
          within(dialog).getByRole("button", { name: "Save" }),
        ).toBeEnabled();
      });
      await user.click(within(dialog).getByRole("button", { name: "Save" }));

      expect(await within(dialog).findByText(REFUSAL)).toBeInTheDocument();
      await flush();
      expect(putMock).not.toHaveBeenCalled();
      expect(dialog).toHaveAttribute("data-state", "open");
      expectNoToast();

      // The control: a dialog opened for the node now shown goes to that node,
      // with that node's values and digest, so the refusal is not a block on saving.
      await cancelDialog(user);
      await openEdit(user, 0);
      await pressSave(user);
      await waitFor(() => {
        expect(putMock).toHaveBeenCalledTimes(1);
      });
      expect(putMock.mock.calls[0]?.[0]).toBe(CONFIG_PATH_0);
      expect(sentBody()).toEqual({
        acmedomain0: "domain=zero.example.com",
        digest: "d0",
      });
    },
  );
});

// ── The session ─────────────────────────────────────────────────────────────

// A save is made in a session, and a toast raised after that session ended is
// shown to whoever is signed in by then, with the node's name and the server's
// words in it: a sign-out, an expiry or another user signing in. So a save that
// settles after its session ended does nothing at all, and each place saveDomain
// would act is paired here with the same answer in a session that goes on. The
// page is dropped the way a sign-out drops it: the cache is cleared and the tab
// unmounted. (The three ways a session ends are api-client.session.test.ts's;
// one is used at each place.)
describe("a save that settles after its session ended", () => {
  beforeEach(() => {
    signInAsAdmin();
  });

  afterEach(() => {
    signOutForGood();
  });

  /** A session that goes on, or one of the ways it ends; `ended` says which. */
  const AFTER = (name: string): [string, () => void, boolean][] => [
    ["the session goes on", () => undefined, false],
    [name, row(SESSION_ENDS, name)[1], true],
  ];

  /** Waits until the one save has settled, as `status`, and then for whatever it queued. */
  async function saveSettled(qc: QueryClient, status: "success" | "error") {
    await waitFor(() => {
      expect(
        qc
          .getMutationCache()
          .getAll()
          .map((mutation) => mutation.state.status),
      ).toEqual([status]);
    });
    await flush();
  }

  it.each(AFTER("a sign-out"))(
    "%s: a failure that comes after the page was dropped is toasted, or not",
    async (_, end, ended) => {
      const user = userEvent.setup();
      serve([TWO_DOMAINS]);
      const qc = createAppQueryClient();
      renderTab(qc);
      await openCertificatesTab(user);

      const held = await saveHeld(user);
      end();
      await act(async () => {
        await qc.cancelQueries();
        qc.clear();
      });
      // The premise: the save is out and the cache no longer knows of it, so
      // what reports its failure is the promise saveDomain holds.
      expect(qc.getMutationCache().getAll()).toEqual([]);
      cleanup();
      held.reject(denied());

      if (ended) await expectSilence();
      else await expectOneToast(failedToast(DENIED));
      expect(putMock).toHaveBeenCalledTimes(1);
    },
  );

  it.each(AFTER("another user signing in"))(
    "%s: a stale digest is read again, or not",
    async (_, end, ended) => {
      const user = userEvent.setup();
      serve([TWO_DOMAINS, later("d2")]);
      const { qc } = renderTab(createAppQueryClient());
      await openCertificatesTab(user);

      const held = await saveHeld(user);
      const before = configGets().length;
      end();
      held.reject(conflict());

      if (ended) {
        await saveSettled(qc, "error");
        // A tab that is somehow still there: the session still wins, so there
        // is no read of the node as whoever is signed in now.
        expect(configGets()).toHaveLength(before);
      } else {
        await waitFor(() => {
          expect(configGets()).toHaveLength(before + 1);
        });
      }
      expectNoToast();
    },
  );

  it.each(AFTER("the same user signing in again"))(
    "%s: a save that succeeds closes the dialog, or not",
    async (_, end, ended) => {
      const user = userEvent.setup();
      serve([TWO_DOMAINS]);
      const { qc } = renderTab(createAppQueryClient());
      await openCertificatesTab(user);

      const held = await saveHeld(user);
      end();
      held.resolve({ status: "ok" });

      if (ended) {
        await saveSettled(qc, "success");
        expect(screen.getByRole("dialog")).toHaveAttribute(
          "data-state",
          "open",
        );
        expectNoToast();
      } else {
        await waitFor(() => {
          expect(screen.queryByRole("dialog")).toBeNull();
        });
      }
    },
  );

  // The guard that holds a dialog's Save shut while its save is out is released
  // whatever the session: a dialog that is somehow still there must not be left
  // unable to save.
  it.each([
    [
      "a failure",
      (held: ReturnType<typeof deferred<unknown>>) => {
        held.reject(denied());
      },
    ],
    [
      "a success",
      (held: ReturnType<typeof deferred<unknown>>) => {
        held.resolve({ status: "ok" });
      },
    ],
  ])(
    "does not hold Save shut once the session has ended and %s has come: the dialog can save again",
    async (_, answer) => {
      const user = userEvent.setup();
      serve([TWO_DOMAINS]);
      renderTab(createAppQueryClient());
      await openCertificatesTab(user);

      const held = await saveHeld(user);
      signOutForGood();
      answer(held);
      signInAsAdmin();
      // Once the tab has been told the save is over, which is when its button
      // reads "Save" again.
      await user.click(await screen.findByRole("button", { name: "Save" }));

      await waitFor(() => {
        expect(putMock).toHaveBeenCalledTimes(2);
      });
    },
  );

  describe("whose stale digest was read again", () => {
    /** An edit saved and refused as stale, with the read that follows held. */
    async function refusedWithReadHeld(user: UserEvent) {
      const read = deferred<NodeACMEConfig>();
      let call = 0;
      listMock.mockImplementation((path: string) =>
        path === NODES_PATH
          ? Promise.resolve([{ name: NODE, node_name: NODE }])
          : Promise.resolve([]),
      );
      getMock.mockImplementation((path: string) => {
        if (path !== CONFIG_PATH) return Promise.resolve(null);
        call += 1;
        return call === 1 ? Promise.resolve(TWO_DOMAINS) : read.promise;
      });
      putMock.mockRejectedValueOnce(conflict());
      renderTab(createAppQueryClient());
      await openCertificatesTab(user);
      await openEdit(user);
      await pressSave(user);
      // The read of the node that the refusal starts.
      await waitFor(() => {
        expect(configGets()).toHaveLength(2);
      });
      return () => {
        read.resolve(later("d2"));
      };
    }

    it.each(AFTER("a sign-out"))(
      "%s: the next save is pinned to what that read found, or to nothing it found",
      async (_, end, ended) => {
        const user = userEvent.setup();
        const answer = await refusedWithReadHeld(user);

        end();
        answer();
        await flush();
        await pressSave(user);

        await waitFor(() => {
          expect(putMock).toHaveBeenCalledTimes(2);
        });
        expect(sentBody(1).digest).toBe(ended ? "d1" : "d2");
      },
    );
  });
});
