import { describe, expect, it, vi, beforeEach } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  QueryClient,
  QueryClientProvider,
  useMutation,
} from "@tanstack/react-query";
import { toast } from "sonner";
import type { ReactNode } from "react";

import { ClusterACMETab } from "./ClusterACMETab";
import { ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import type { NodeACMEConfig } from "@/features/acme/api/acme-queries";

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

// The app's mutation-error net (lib/query-client.ts) toasts through sonner, so
// this one mock sees every toast a run can raise — of any kind, so that a test
// that says a save "toasts nothing" is not satisfied by a success or a warning.
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

const mockedToastError = vi.mocked(toast.error);
const toastSpies = {
  default: vi.mocked(toast),
  success: vi.mocked(toast.success),
  info: vi.mocked(toast.info),
  warning: vi.mocked(toast.warning),
  error: mockedToastError,
  message: vi.mocked(toast.message),
  loading: vi.mocked(toast.loading),
};

/** Every toast raised so far, of any kind, as "kind: message". */
function toastsRaised(): string[] {
  return Object.entries(toastSpies).flatMap(([kind, spy]) =>
    (spy.mock.calls as unknown[][]).map(
      (args) => `${kind}: ${String(args[0])}`,
    ),
  );
}

function expectNoToast() {
  expect(toastsRaised()).toEqual([]);
}

/**
 * The tab on `qc`. The default is a bare client with no mutation-error net, which
 * is enough for what is shown on screen; a test that asserts a toast, or the
 * lack of one, must pass createAppQueryClient(), since on this one a "no toast"
 * passes with the hook's opt-out removed.
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
async function openEdit(user: ReturnType<typeof userEvent.setup>, index = 0) {
  const rows = await screen.findAllByRole("button", { name: "Edit" });
  const row = rows[index];
  if (!row) throw new Error(`no Edit button at index ${String(index)}`);
  await user.click(row);
}

function configGets() {
  return getMock.mock.calls.filter((c) => c[0] === CONFIG_PATH);
}

/**
 * Serves the node list plus a sequence of acme-config reads, one per GET. The
 * last entry repeats, so a test that only cares about the first read passes a
 * single config.
 */
function serve(configs: NodeACMEConfig[]) {
  listMock.mockImplementation((path: string) =>
    path === NODES_PATH
      ? Promise.resolve([{ name: NODE, node_name: NODE }])
      : Promise.resolve([]),
  );
  let call = 0;
  getMock.mockImplementation((path: string) => {
    if (path !== CONFIG_PATH) return Promise.resolve(null);
    const cfg = configs[Math.min(call, configs.length - 1)];
    call += 1;
    return Promise.resolve(cfg);
  });
}

/** Opens the Node Certificates tab and waits for the first config read. */
async function openCertificatesTab(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("tab", { name: "Node Certificates" }));
  await waitFor(() => {
    expect(getMock).toHaveBeenCalledWith(CONFIG_PATH);
  });
}

async function typeDomainAndSave(
  user: ReturnType<typeof userEvent.setup>,
  domain: string,
) {
  await user.type(screen.getByPlaceholderText("node1.example.com"), domain);
  await user.click(screen.getByRole("button", { name: "Save" }));
}

beforeEach(() => {
  listMock.mockReset();
  getMock.mockReset();
  putMock.mockReset();
  for (const spy of Object.values(toastSpies)) spy.mockReset();
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
    const body = putMock.mock.calls[0]?.[1] as NodeACMEConfig;
    expect(body.digest).toBe("d1");
    expect(body.acmedomain0).toBe("domain=node1.example.com");
    // The account rode along on every domain save and wrote the cached copy
    // back over whatever Proxmox actually had. It must not be in the payload.
    expect(body).not.toHaveProperty("acme");
  });

  it("resolves an added domain's slot against the config the digest came from", async () => {
    const user = userEvent.setup();
    // The read behind the open dialog shows slot 1 free; the refetch that
    // opening the dialog triggers shows another operator has taken it. The
    // save must land on slot 2, not overwrite them with a digest that now
    // matches.
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
    const body = putMock.mock.calls[0]?.[1] as NodeACMEConfig;
    expect(body.digest).toBe("d2");
    expect(body.acmedomain2).toBe("domain=node3.example.com");
    expect(body).not.toHaveProperty("acmedomain1");
  });

  it("keeps an edited domain's own digest instead of refreshing it underneath", async () => {
    const user = userEvent.setup();
    // Only an add refetches on open. An edit's fields come from the config on
    // screen, so pulling a newer digest under them would let the save carry a
    // digest matching a version of this slot the operator never saw — the
    // compare-and-swap would pass and overwrite it.
    serve([
      { acmedomain0: "domain=node1.example.com", digest: "d1" },
      { acmedomain0: "domain=node2.example.com", digest: "d2" },
    ]);
    renderTab();
    await openCertificatesTab(user);

    expect(await screen.findByText("node1.example.com")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Edit" }));
    // Checked before the save: afterwards the mutation's own invalidation
    // refetches the config legitimately, which would mask an open-time one.
    expect(getMock.mock.calls.filter((c) => c[0] === CONFIG_PATH)).toHaveLength(
      1,
    );

    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    const body = putMock.mock.calls[0]?.[1] as NodeACMEConfig;
    expect(body.digest).toBe("d1");
    expect(body.acmedomain0).toBe("domain=node1.example.com");
  });

  it("refuses to save while the node config is unread", async () => {
    const user = userEvent.setup();
    listMock.mockImplementation((path: string) =>
      path === NODES_PATH
        ? Promise.resolve([{ name: NODE, node_name: NODE }])
        : Promise.resolve([]),
    );
    // Unread, the free-slot scan answers 0 and no digest is attached, so the
    // write would blindly overwrite acmedomain0 with the check switched off.
    getMock.mockImplementation((path: string) =>
      path === CONFIG_PATH
        ? Promise.reject(new ApiClientError(502, { error: "bad", message: "" }))
        : Promise.resolve(null),
    );
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await user.type(
      screen.getByPlaceholderText("node1.example.com"),
      "node1.example.com",
    );

    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(putMock).not.toHaveBeenCalled();
  });

  it("reports a dropped connection rather than failing silently", async () => {
    const user = userEvent.setup();
    serve([{ digest: "d1" }]);
    // How fetch rejects when the connection drops. describeError returns ""
    // for a TypeError and the hook has opted out of the global toast, so
    // without a fallback this failure renders as nothing on either surface.
    putMock.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await typeDomainAndSave(user, "node1.example.com");

    expect(
      await screen.findByText(/check your connection and try again/),
    ).toBeInTheDocument();
  });

  it("keeps an edit's pinned digest when the config refetches underneath it", async () => {
    const user = userEvent.setup();
    // Removing the refetch from openEditDomain removed one trigger, not the
    // class: a WebSocket reconnect invalidates every active query, and so does
    // ordering or renewing a certificate. Read live at save time, the digest
    // would move to d2 while the form still showed the d1 values — and the
    // compare-and-swap would pass and destroy the change that produced d2.
    serve([
      { acmedomain0: "domain=node1.example.com", digest: "d1" },
      { acmedomain0: "domain=node2.example.com", digest: "d2" },
    ]);
    const { qc } = renderTab();
    await openCertificatesTab(user);
    await openEdit(user);

    await qc.invalidateQueries();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });
    expect(await screen.findByText("node2.example.com")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    const body = putMock.mock.calls[0]?.[1] as NodeACMEConfig;
    expect(body.digest).toBe("d1");
  });

  it("still saves an edit after a background refresh fails", async () => {
    const user = userEvent.setup();
    // A failed background refetch flips the query to "error" while keeping the
    // data, so gating Save on isSuccess would strand every edit on a node
    // whose six slots are full — the Add button, and so the only other
    // refetch, is hidden at six. An edit needs nothing from that read anyway.
    listMock.mockImplementation((path: string) =>
      path === NODES_PATH
        ? Promise.resolve([{ name: NODE, node_name: NODE }])
        : Promise.resolve([]),
    );
    let call = 0;
    getMock.mockImplementation((path: string) => {
      if (path !== CONFIG_PATH) return Promise.resolve(null);
      call += 1;
      return call === 1
        ? Promise.resolve({
            acmedomain0: "domain=node1.example.com",
            digest: "d1",
          })
        : Promise.reject(
            new ApiClientError(502, { error: "bad", message: "" }),
          );
    });
    const { qc } = renderTab();
    await openCertificatesTab(user);
    await openEdit(user);

    await qc.invalidateQueries();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    expect((putMock.mock.calls[0]?.[1] as NodeACMEConfig).digest).toBe("d1");
  });

  it("re-pins an edit's digest after a conflict so the retry can go through", async () => {
    const user = userEvent.setup();
    // A conflict the operator has been shown is the one thing allowed to move
    // a pinned digest. Without that, an edit could never be retried: every
    // attempt would re-send the digest it already lost on.
    serve([
      { acmedomain0: "domain=node1.example.com", digest: "d1" },
      { acmedomain0: "domain=node1.example.com", digest: "d2" },
    ]);
    putMock.mockRejectedValueOnce(
      new ApiClientError(409, { error: "conflict", message: "changed" }),
    );
    renderTab();
    await openCertificatesTab(user);
    await openEdit(user);

    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("changed")).toBeInTheDocument();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect((putMock.mock.calls[1]?.[1] as NodeACMEConfig).digest).toBe("d2");
  });

  it("holds the pin when the conflict refetch itself fails", async () => {
    const user = userEvent.setup();
    // A failed refetch RETAINS the last successful data, so re-pinning on
    // `res.data` alone is a guard that can never fail: it would move the pin
    // to d2 — a change this dialog never saw — and the retry would overwrite
    // it. The pin must only move on a refetch that actually succeeded.
    listMock.mockImplementation((path: string) =>
      path === NODES_PATH
        ? Promise.resolve([{ name: NODE, node_name: NODE }])
        : Promise.resolve([]),
    );
    let call = 0;
    getMock.mockImplementation((path: string) => {
      if (path !== CONFIG_PATH) return Promise.resolve(null);
      call += 1;
      if (call === 1)
        return Promise.resolve({
          acmedomain0: "domain=node1.example.com",
          digest: "d1",
        });
      if (call === 2)
        return Promise.resolve({
          acmedomain0: "domain=node2.example.com",
          digest: "d2",
        });
      return Promise.reject(
        new ApiClientError(502, { error: "bad", message: "" }),
      );
    });
    putMock.mockRejectedValueOnce(
      new ApiClientError(409, { error: "conflict", message: "changed" }),
    );
    const { qc } = renderTab();
    await openCertificatesTab(user);
    await openEdit(user);

    // The cache moves to d2 behind the open dialog, which still shows d1's
    // values; the conflict refetch that follows cannot replace it.
    await qc.invalidateQueries();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("changed")).toBeInTheDocument();
    await waitFor(() => {
      expect(configGets()).toHaveLength(3);
    });

    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect((putMock.mock.calls[1]?.[1] as NodeACMEConfig).digest).toBe("d1");
  });

  it("does not let one dialog's conflict refetch re-pin the next dialog", async () => {
    const user = userEvent.setup();
    // The conflict refetch is async and nothing about it is tied to the dialog
    // that fired it. Cancelling and opening another row while it is in flight
    // must not land its digest on a pin that was just set to match different
    // values — that would be the same overwrite, one dialog removed.
    listMock.mockImplementation((path: string) =>
      path === NODES_PATH
        ? Promise.resolve([{ name: NODE, node_name: NODE }])
        : Promise.resolve([]),
    );
    let releaseRefetch: (() => void) | undefined;
    let call = 0;
    const firstRead = {
      acmedomain0: "domain=node1.example.com",
      acmedomain1: "domain=node2.example.com",
      digest: "d1",
    };
    getMock.mockImplementation((path: string) => {
      if (path !== CONFIG_PATH) return Promise.resolve(null);
      call += 1;
      if (call === 1) return Promise.resolve(firstRead);
      return new Promise((resolve) => {
        releaseRefetch = () => {
          resolve({ ...firstRead, digest: "d3" });
        };
      });
    });
    putMock.mockRejectedValueOnce(
      new ApiClientError(409, { error: "conflict", message: "changed" }),
    );
    renderTab();
    await openCertificatesTab(user);

    // First dialog: slot 0. Save conflicts and leaves a refetch hanging.
    await openEdit(user);
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("changed")).toBeInTheDocument();
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

    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    const body = putMock.mock.calls[1]?.[1] as NodeACMEConfig;
    expect(body.acmedomain1).toBe("domain=node2.example.com");
    expect(body.digest).toBe("d1");
  });

  it("reports a digest conflict in the dialog and refetches so a retry works", async () => {
    const user = userEvent.setup();
    const conflict =
      "The node's configuration changed since it was read — reload and try again.";
    serve([{ digest: "d1" }, { digest: "d2" }, { digest: "d3" }]);
    putMock.mockRejectedValueOnce(
      new ApiClientError(409, { error: "conflict", message: conflict }),
    );
    renderTab();
    await openCertificatesTab(user);

    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await typeDomainAndSave(user, "node1.example.com");

    // The dialog stays open carrying the typed value, so the operator can
    // retry deliberately rather than having the retry made on their behalf.
    expect(await screen.findByText(conflict)).toBeInTheDocument();
    expect(screen.getByPlaceholderText("node1.example.com")).toHaveValue(
      "node1.example.com",
    );
    expect(putMock).toHaveBeenCalledTimes(1);

    // The conflict refetch reloads the digest, so pressing Save again sends
    // the current one instead of repeating the doomed write.
    await waitFor(() => {
      expect(
        getMock.mock.calls.filter((c) => c[0] === CONFIG_PATH),
      ).toHaveLength(3);
    });
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect((putMock.mock.calls[1]?.[1] as NodeACMEConfig).digest).toBe("d3");
  });
});

// A save is reported where the operator is looking when it lands. The hook opts
// out of the app's global error toast (acme-queries.ts), because the tab shows a
// failure itself: in the dialog while it is open, on the card once it closes.
// But TanStack runs the callbacks given to mutate() only while the component is
// mounted and still attached to that mutation, so a save that lands after the
// tab was left, after the dialog was replaced by another row's, or after the
// node was changed would be reported nowhere. saveDomain reads the outcome from
// the promise instead, and toasts such a failure, naming what was saved.
//
// Every test below runs on the app's own kind of client (test/app-query-client.ts),
// where the global toast exists, and starts from the control its docs ask for.

type UserEvent = ReturnType<typeof userEvent.setup>;

const DENIED = "Proxmox API permission denied";
// What the server says to a stale digest.
const STALE =
  "The node's configuration changed since it was read — reload and try again.";
// What the dialog adds to it: its fields do not reload, so saving again
// overwrites.
const SAVING_AGAIN =
  "Saving again will overwrite the node's current configuration with the values shown here.";
const NODE_2 = "pve-02";
const CONFIG_PATH_2 = `/api/v1/clusters/${CLUSTER}/nodes/${NODE_2}/acme-config`;

/** Two domains on the first node, at digest d1. */
const TWO_DOMAINS: NodeACMEConfig = {
  acmedomain0: "domain=node1.example.com",
  acmedomain1: "domain=node2.example.com",
  digest: "d1",
};

/** What a late failure of a save of a domain (the first, unless said), on the first node, toasts. */
function failedToast(message: string, domain = "node1.example.com"): string {
  return `Saving the ACME domain ${domain} on ${NODE} failed: ${message}`;
}

function forbidden(): ApiClientError {
  return new ApiClientError(403, { error: "forbidden", message: DENIED });
}

function conflict(): ApiClientError {
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
 * Lets whatever is already queued run, timers included, and React draw what it
 * set: for looking at what did NOT happen once a request has settled.
 */
async function flush(): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, 0);
    });
  });
}

/** Opens the nth row's edit dialog and saves it, with the request held. */
async function saveHeld(user: UserEvent, row = 0) {
  const held = deferred<unknown>();
  putMock.mockReturnValueOnce(held.promise);
  await openEdit(user, row);
  await user.click(screen.getByRole("button", { name: "Save" }));
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

/** The ways the tab can be gone by the time a save lands. */
const LEAVES: [name: string, leave: (user: UserEvent) => Promise<void>][] = [
  ["Cancel and another tab", leaveForAccounts],
  [
    "Escape and another tab",
    async (user) => {
      await user.keyboard("{Escape}");
      await waitFor(() => {
        expect(screen.queryByRole("dialog")).toBeNull();
      });
      await user.click(screen.getByRole("tab", { name: "Accounts" }));
    },
  ],
  [
    "the page being left with the dialog open",
    () => {
      cleanup();
      return Promise.resolve();
    },
  ],
];

describe("a save that settles while the tab is still showing it", () => {
  const PROBE = "PROBE-NOT-A-REAL-FAILURE";

  it("control: a failed mutation with no onError of its own toasts on this client", async () => {
    const qc = createAppQueryClient();
    const { result } = renderHook(
      () => useMutation({ mutationFn: () => Promise.reject(new Error(PROBE)) }),
      {
        wrapper: ({ children }: { children: ReactNode }) => (
          <QueryClientProvider client={qc}>{children}</QueryClientProvider>
        ),
      },
    );

    await act(async () => {
      await result.current.mutateAsync().catch(() => undefined);
    });

    expect(toastsRaised()).toEqual([`error: ${PROBE}`]);
  });

  it("shows a failure in the dialog, which stays open, and toasts nothing", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    putMock.mockRejectedValueOnce(forbidden());
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    await openEdit(user);
    await user.click(screen.getByRole("button", { name: "Save" }));

    const dialog = screen.getByRole("dialog");
    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    expect(dialog).toHaveAttribute("data-state", "open");
    // The card behind it does not carry a second copy.
    expect(screen.getAllByText(DENIED)).toHaveLength(1);
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
    held.reject(forbidden());

    expect(await screen.findByText(DENIED)).toBeInTheDocument();
    await flush();
    expectNoToast();
  });

  it("closes the dialog when the save succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    await openEdit(user);
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    await flush();
    expect(putMock).toHaveBeenCalledTimes(1);
    expectNoToast();
  });

  it("answers a conflict in the dialog with the note that saving again overwrites, reads the node again and re-pins", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS, { ...TWO_DOMAINS, digest: "d2" }]);
    putMock.mockRejectedValueOnce(conflict());
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    await openEdit(user);
    await user.click(screen.getByRole("button", { name: "Save" }));

    const dialog = screen.getByRole("dialog");
    expect(await within(dialog).findByText(STALE)).toBeInTheDocument();
    expect(within(dialog).getByText(SAVING_AGAIN)).toBeInTheDocument();
    await waitFor(() => {
      expect(configGets()).toHaveLength(2);
    });

    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect((putMock.mock.calls[1]?.[1] as NodeACMEConfig).digest).toBe("d2");
    await flush();
    expectNoToast();
  });

  it("adds no such note to any other failure, which reads nothing again", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS, { ...TWO_DOMAINS, digest: "d2" }]);
    putMock.mockRejectedValueOnce(forbidden());
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    await openEdit(user);
    await user.click(screen.getByRole("button", { name: "Save" }));

    const dialog = screen.getByRole("dialog");
    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flush();
    expect(within(dialog).queryByText(SAVING_AGAIN)).toBeNull();
    expect(configGets()).toHaveLength(1);
  });
});

describe("a save that settles after its dialog was replaced", () => {
  /** The first row's save held, its dialog cancelled and the second row's open. */
  async function replaced(user: UserEvent) {
    serve([TWO_DOMAINS, { ...TWO_DOMAINS, digest: "d2" }]);
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

    held.reject(forbidden());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(failedToast(DENIED));
    });
    await flush();

    expect(toastsRaised()).toEqual([`error: ${failedToast(DENIED)}`]);
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
    await waitFor(() => {
      expect(
        qc
          .getMutationCache()
          .getAll()
          .map((mutation) => mutation.state.status),
      ).toEqual(["success"]);
    });
    await flush();

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
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(failedToast(STALE));
    });
    await flush();

    expect(toastsRaised()).toEqual([`error: ${failedToast(STALE)}`]);
    expect(configGets()).toHaveLength(before);
    // The conflict is not the newer dialog's: no note of it there.
    expect(within(second).queryByText(SAVING_AGAIN)).toBeNull();
    expect(within(second).queryByText(STALE)).toBeNull();

    // Its own save still goes out against the read it was opened from.
    await user.click(within(second).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    const body = putMock.mock.calls[1]?.[1] as NodeACMEConfig;
    expect(body.acmedomain1).toBe("domain=node2.example.com");
    expect(body.digest).toBe("d1");
  });
});

describe("a save that settles after the node was changed", () => {
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

  /** The first node's first domain saved, held, its dialog cancelled, the second node chosen. */
  async function moved(user: UserEvent) {
    serveTwoNodes();
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    const held = await saveHeld(user);
    await cancelDialog(user);
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: NODE_2 }));
    expect(await screen.findByText("node3.example.com")).toBeInTheDocument();
    return held;
  }

  it("toasts a failure, naming the node it was for, and shows nothing on the other node's card", async () => {
    const user = userEvent.setup();
    const held = await moved(user);

    held.reject(forbidden());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(failedToast(DENIED));
    });
    await flush();

    expect(toastsRaised()).toEqual([`error: ${failedToast(DENIED)}`]);
    expect(screen.queryByText(DENIED)).toBeNull();
  });

  it("toasts a stale-digest refusal, and reads neither node again", async () => {
    const user = userEvent.setup();
    const held = await moved(user);
    const before = getMock.mock.calls.length;

    held.reject(conflict());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(failedToast(STALE));
    });
    await flush();

    expect(toastsRaised()).toEqual([`error: ${failedToast(STALE)}`]);
    expect(getMock).toHaveBeenCalledTimes(before);
  });
});

describe("a save that settles after the tab was left", () => {
  it.each(LEAVES)(
    "toasts a failure that comes after %s, once, naming the node and the domain",
    async (_, leave) => {
      const user = userEvent.setup();
      serve([TWO_DOMAINS]);
      renderTab(createAppQueryClient());
      await openCertificatesTab(user);

      const held = await saveHeld(user);
      await leave(user);
      expect(screen.queryByText("ACME Domain Configuration")).toBeNull();

      held.reject(forbidden());
      await waitFor(() => {
        expect(mockedToastError).toHaveBeenCalledWith(failedToast(DENIED));
      });
      // Once: not also by the global net, and not again later.
      await flush();
      expect(toastsRaised()).toEqual([`error: ${failedToast(DENIED)}`]);
      expect(putMock).toHaveBeenCalledTimes(1);
    },
  );

  it("gives a request that got no answer the connection message rather than an empty one", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await leaveForAccounts(user);
    // How fetch rejects when the connection drops: describeError has no words
    // for a TypeError.
    held.reject(new TypeError("Failed to fetch"));

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(
        failedToast(
          "The save request failed — check your connection and try again.",
        ),
      );
    });
    await flush();
    expect(toastsRaised()).toEqual([
      `error: ${failedToast(
        "The save request failed — check your connection and try again.",
      )}`,
    ]);
  });

  it("toasts a stale-digest refusal, and does not read the node again", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS, { ...TWO_DOMAINS, digest: "d2" }]);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await leaveForAccounts(user);
    const before = configGets().length;

    held.reject(conflict());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(failedToast(STALE));
    });
    await flush();

    // A live conflict reads the node again to re-pin (above); a tab that is
    // gone has no dialog to re-pin.
    expect(toastsRaised()).toEqual([`error: ${failedToast(STALE)}`]);
    expect(configGets()).toHaveLength(before);
  });

  it("toasts nothing for a save that succeeds, and leaves the dialog opened on the tab's return alone", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    const { qc } = renderTab(createAppQueryClient());
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await leaveForAccounts(user);
    // The tab is back, with a dialog of its own open, before the save lands.
    await user.click(screen.getByRole("tab", { name: "Node Certificates" }));
    await openEdit(user, 1);
    const second = await screen.findByRole("dialog");

    held.resolve({ status: "ok" });
    await waitFor(() => {
      expect(
        qc
          .getMutationCache()
          .getAll()
          .map((mutation) => mutation.state.status),
      ).toEqual(["success"]);
    });
    await flush();

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(within(second).getByDisplayValue("node2.example.com")).toBeVisible();
    expectNoToast();
  });

  // Logout empties the whole cache — cancelQueries, then clear(), which takes
  // the MutationCache with it — and the page it was on drops. The save is still
  // out, and no longer anywhere in the cache that was cleared, so what reports
  // its failure has to be the promise saveDomain holds, not a look at the cache.
  it("toasts a failure that comes after the app cleared its cache and the page was dropped, once", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    const qc = createAppQueryClient();
    renderTab(qc);
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await act(async () => {
      await qc.cancelQueries();
      qc.clear();
    });
    // The premise: the save is out and the cache no longer knows of it.
    expect(qc.getMutationCache().getAll()).toEqual([]);
    cleanup();
    held.reject(forbidden());

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(failedToast(DENIED));
    });
    await flush();
    expect(toastsRaised()).toEqual([`error: ${failedToast(DENIED)}`]);
  });

  it("toasts a failure without showing it in the dialog opened on the tab's return", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await leaveForAccounts(user);
    await user.click(screen.getByRole("tab", { name: "Node Certificates" }));
    await openEdit(user, 1);
    const second = await screen.findByRole("dialog");

    held.reject(forbidden());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(failedToast(DENIED));
    });
    await flush();

    expect(second).toHaveAttribute("data-state", "open");
    expect(screen.queryByText(DENIED)).toBeNull();
    expect(within(second).getByRole("button", { name: "Save" })).toBeEnabled();
  });
});

// isPending — and the Save button that reads it — trails a click by a task:
// TanStack tells its observers on a timer. Two clicks inside that task reach
// saveDomain with the button still enabled, and sent two requests carrying the
// same digest. The second is refused as stale because of the first, which is a
// conflict the operator caused, and the tab, attached to the second, showed that
// over the first's outcome. The clicks below are fired back to back with nothing
// awaited between them, which is how two get in.

/**
 * Lets queued promise callbacks run until `done` (or, with no `done`, for a few
 * hundred hops, longer than any chain of them here), and nothing else: no timer
 * fires, so TanStack has not yet told React what the save did, and the Save
 * button is still the one the last click found. flush() waits a task, which is
 * exactly what these tests must not do. The request itself is a few hops behind
 * the click — a mutation awaits its onMutate first — so it is counted after.
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
    const held = deferred<unknown>();
    putMock.mockReturnValueOnce(held.promise);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    await openEdit(user);

    const save = screen.getByRole("button", { name: "Save" });
    fireEvent.click(save);
    // The premise: the first click has not reached the button yet. If React
    // were told at once, the second click would find it disabled, and this
    // test would pass with no guard at all.
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await microtasksUntil();
    expect(putMock).toHaveBeenCalledTimes(1);

    held.reject(forbidden());
    const dialog = screen.getByRole("dialog");
    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flush();
    expect(putMock).toHaveBeenCalledTimes(1);
    expectNoToast();
  });

  it("sends one request, so a save that lands is not followed by a refusal for it", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS, { ...TWO_DOMAINS, digest: "d2" }]);
    const held = deferred<unknown>();
    // A second request would carry the digest the first has just replaced.
    putMock
      .mockReturnValueOnce(held.promise)
      .mockImplementationOnce(() => Promise.reject(conflict()));
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    await openEdit(user);

    const save = screen.getByRole("button", { name: "Save" });
    fireEvent.click(save);
    expect(save).toBeEnabled();
    fireEvent.click(save);
    held.resolve({ status: "ok" });

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    await flush();
    expect(putMock).toHaveBeenCalledTimes(1);
    expect(screen.queryByText(STALE)).toBeNull();
    expectNoToast();
  });

  it("lets the same dialog save again once its first save has been answered", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    putMock.mockRejectedValueOnce(forbidden());
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);
    await openEdit(user);

    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await within(screen.getByRole("dialog")).findByText(DENIED),
    ).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("lets a newer dialog save while an older dialog's save is still out", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await cancelDialog(user);
    await openEdit(user, 1);
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(2);
    });
    expect((putMock.mock.calls[1]?.[1] as NodeACMEConfig).acmedomain1).toBe(
      "domain=node2.example.com",
    );
    held.resolve({ status: "ok" });
    await flush();
  });

  it("does not let an older dialog's answer release a newer dialog's save", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);

    const older = await saveHeld(user);
    await cancelDialog(user);
    const newer = deferred<unknown>();
    putMock.mockReturnValueOnce(newer.promise);
    await openEdit(user, 1);
    const save = screen.getByRole("button", { name: "Save" });
    fireEvent.click(save);
    await microtasksUntil(() => putMock.mock.calls.length === 2);
    expect(putMock).toHaveBeenCalledTimes(2);

    // The older save is answered while the newer one is out, and nothing yet
    // has told the button so: its toast is the proof that its handler ran.
    older.reject(forbidden());
    await microtasksUntil(() => mockedToastError.mock.calls.length > 0);
    expect(toastsRaised()).toEqual([`error: ${failedToast(DENIED)}`]);

    // The premise again: still enabled, so this click reaches saveDomain.
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await microtasksUntil();
    expect(putMock).toHaveBeenCalledTimes(2);

    newer.resolve({ status: "ok" });
    await flush();
  });

  // The twin of the test above, for the other way a save is answered: the
  // release in each handler is its own line, and each needs its own proof.
  it("does not let an older dialog's success release a newer dialog's save either", async () => {
    const user = userEvent.setup();
    serve([TWO_DOMAINS]);
    renderTab(createAppQueryClient());
    await openCertificatesTab(user);

    const older = await saveHeld(user);
    await cancelDialog(user);
    const newer = deferred<unknown>();
    putMock.mockReturnValueOnce(newer.promise);
    await openEdit(user, 1);
    const save = screen.getByRole("button", { name: "Save" });
    fireEvent.click(save);
    await microtasksUntil(() => putMock.mock.calls.length === 2);
    expect(putMock).toHaveBeenCalledTimes(2);

    // A success raises nothing to wait for, so every queued callback is let
    // run, and no timer: nothing yet has told the button that the newer save
    // is out, and its click below must still be met by the guard.
    older.resolve({ status: "ok" });
    await microtasksUntil();
    expect(save).toBeEnabled();
    fireEvent.click(save);
    await microtasksUntil();
    expect(putMock).toHaveBeenCalledTimes(2);

    newer.resolve({ status: "ok" });
    await flush();
  });
});

const NODE_0 = "pve-00";
const CONFIG_PATH_0 = `/api/v1/clusters/${CLUSTER}/nodes/${NODE_0}/acme-config`;

/**
 * One node, pve-01, until joins() lets another that sorts before it, pve-00,
 * into the list. The tab was never told to use pve-01: it is its first node,
 * and it follows the list.
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

describe("a dialog whose node stops being the one shown", () => {
  const REFUSAL = `This dialog was opened for ${NODE}, which is no longer the node shown. Close it and try again.`;

  it("refuses to send an edit, and says why, while a dialog for the node it was opened for still saves", async () => {
    const user = userEvent.setup();
    const joins = serveNodeThatSortsFirst();
    const qc = createAppQueryClient();
    renderTab(qc);
    await openCertificatesTab(user);
    await openEdit(user, 1);
    await joins(qc);

    const dialog = screen.getByRole("dialog");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText(REFUSAL)).toBeInTheDocument();
    await flush();
    expect(putMock).not.toHaveBeenCalled();
    expect(dialog).toHaveAttribute("data-state", "open");
    expectNoToast();

    // The control: a dialog opened for the node now shown goes to that node,
    // with that node's values and digest, so the refusal is not a block on
    // saving.
    await cancelDialog(user);
    await openEdit(user, 0);
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(putMock).toHaveBeenCalledTimes(1);
    });
    expect(putMock.mock.calls[0]?.[0]).toBe(CONFIG_PATH_0);
    expect(putMock.mock.calls[0]?.[1]).toEqual({
      acmedomain0: "domain=zero.example.com",
      digest: "d0",
    });
  });

  it("refuses to send an add the same way", async () => {
    const user = userEvent.setup();
    const joins = serveNodeThatSortsFirst();
    const qc = createAppQueryClient();
    renderTab(qc);
    await openCertificatesTab(user);
    await user.click(screen.getByRole("button", { name: /Add Domain/ }));
    await user.type(
      screen.getByPlaceholderText("node1.example.com"),
      "node3.example.com",
    );
    await joins(qc);

    const dialog = screen.getByRole("dialog");
    await waitFor(() => {
      expect(
        within(dialog).getByRole("button", { name: "Save" }),
      ).toBeEnabled();
    });
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    expect(await within(dialog).findByText(REFUSAL)).toBeInTheDocument();
    await flush();
    expect(putMock).not.toHaveBeenCalled();
    expectNoToast();
  });
});

// certNode follows the node list while the selector is unused, so it can move
// without anyone choosing: a node that sorts before the first joins the cluster.
// A save still out to the node left behind is then no longer what the card
// shows, and is let go of the way a chosen node lets go of it (the selector's
// own tests are above). A failure showed on the new node's card, unnamed, with
// no toast.
describe("a save that settles after the node list moved the tab to another node", () => {
  it("toasts a failure, naming the node it was sent to, and gives the new node's card none of it", async () => {
    const user = userEvent.setup();
    const joins = serveNodeThatSortsFirst();
    const qc = createAppQueryClient();
    renderTab(qc);
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await cancelDialog(user);
    await joins(qc);
    held.reject(forbidden());

    await waitFor(() => {
      expect(toastsRaised()).toEqual([`error: ${failedToast(DENIED)}`]);
    });
    await flush();
    expect(toastsRaised()).toHaveLength(1);
    // The new node's card is its own: its domain, and none of this failure.
    expect(screen.getByText("zero.example.com")).toBeInTheDocument();
    expect(screen.queryByText(DENIED)).toBeNull();
  });

  it("toasts a stale-digest refusal the same way, and reads neither node again", async () => {
    const user = userEvent.setup();
    const joins = serveNodeThatSortsFirst();
    const qc = createAppQueryClient();
    renderTab(qc);
    await openCertificatesTab(user);

    const held = await saveHeld(user);
    await cancelDialog(user);
    await joins(qc);
    const before = getMock.mock.calls.length;
    held.reject(conflict());

    await waitFor(() => {
      expect(toastsRaised()).toEqual([`error: ${failedToast(STALE)}`]);
    });
    await flush();
    expect(toastsRaised()).toHaveLength(1);
    expect(getMock).toHaveBeenCalledTimes(before);
  });

  // While a dialog is open it is the surface for what it sent, and its pin
  // refuses a send to the node now shown. Letting go of its save under it would
  // take its answer away.
  it("leaves the save of a dialog that is still open to the dialog: its failure shows there and is not toasted", async () => {
    const user = userEvent.setup();
    const joins = serveNodeThatSortsFirst();
    const qc = createAppQueryClient();
    renderTab(qc);
    await openCertificatesTab(user);

    const held = await saveHeld(user, 1);
    await joins(qc);
    held.reject(forbidden());

    const dialog = screen.getByRole("dialog");
    expect(await within(dialog).findByText(DENIED)).toBeInTheDocument();
    await flush();
    expect(dialog).toHaveAttribute("data-state", "open");
    expectNoToast();
  });

  it("leaves the save of a dialog that is still open to the dialog: its success closes it", async () => {
    const user = userEvent.setup();
    const joins = serveNodeThatSortsFirst();
    const qc = createAppQueryClient();
    renderTab(qc);
    await openCertificatesTab(user);

    const held = await saveHeld(user, 1);
    await joins(qc);
    held.resolve({ status: "ok" });

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    await flush();
    expectNoToast();
  });

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
    held.reject(forbidden());

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
    held.reject(forbidden());

    await waitFor(() => {
      expect(toastsRaised()).toEqual([
        `error: ${failedToast(DENIED, "node2.example.com")}`,
      ]);
    });
    await flush();
    expect(toastsRaised()).toHaveLength(1);
    expect(screen.queryByText(DENIED)).toBeNull();
  });
});
