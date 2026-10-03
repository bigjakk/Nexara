import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
  onlineManager,
  QueryClient,
  QueryClientProvider,
} from "@tanstack/react-query";
import { toast } from "sonner";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import {
  SESSION_ENDS,
  signInAsAdmin,
  signOutForGood,
} from "@/test/late-toast-sessions";
import {
  useNodeDNS,
  useNodeTime,
  type NodeDNSResponse,
  type NodeTimeResponse,
} from "../../api/cluster-queries";
import {
  DNSReadNote,
  EditDNSDialog,
  EditTimezoneDialog,
  TimezoneReadNote,
} from "./NodeSettingsDialogs";

/**
 * The node page's two small settings dialogs: the DNS of the Network & DNS card
 * and the timezone of the System card. Each is an Edit button, offered once the
 * node's own read is in hand, that READS THE NODE AGAIN when it is pressed and
 * opens a dialog mounted afresh from that read, and only from one that
 * succeeded. (Who is offered it at all is the page's to decide:
 * NodeDetailPage.settings.test.tsx.)
 *
 * What these pin down: the form is only ever drawn from what the node holds
 * when Edit is pressed, since the DNS write replaces all four of its settings
 * and a form drawn from an old read puts the old values back — a read the page
 * took before, one a save has just made old, one a refresh that failed left
 * behind; what a user types is not overwritten by a refresh that lands behind
 * the open dialog; and a save sends exactly what the form holds, and fails
 * where the operator can see it.
 */

// The transport is mocked, not the hooks, so the real queries and mutations
// run and each test asserts the request that would leave the browser.
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

// The app's mutation-error net (lib/query-client.ts) toasts through sonner, and
// so does the Edit button when its read fails, so this one mock sees every toast
// a run can raise.
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);
const mockedToastError = vi.mocked(toast.error);

const CLUSTER = "cccccccc-0000-0000-0000-000000000008";
const NODE = "pve-01";
const DNS_URL = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/dns`;
const TIME_URL = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/time`;
const DNS_KEY = ["clusters", CLUSTER, "nodes", NODE, "dns"];
const TIME_KEY = ["clusters", CLUSTER, "nodes", NODE, "time"];
// What an inventory event invalidates (useEventInvalidation): every read of the
// cluster's nodes, these two included.
const NODES_PREFIX = ["clusters", CLUSTER, "nodes"];

/** What a node's DNS is, as the read reports it: all four settings set. */
const DNS: NodeDNSResponse = {
  search: "corp.example.com",
  dns1: "192.0.2.53",
  dns2: "192.0.2.54",
  dns3: "192.0.2.55",
};

/** The same node a while later, after somebody changed two of them. */
const DNS_LATER: NodeDNSResponse = {
  search: "lab.example.com",
  dns1: "192.0.2.99",
  dns2: "192.0.2.54",
  dns3: "192.0.2.55",
};

function time(timezone: string): NodeTimeResponse {
  return { timezone, time: 1_767_225_600, localtime: 1_767_225_600 };
}

const DENIED = "Proxmox API permission denied";

type UserEvent = ReturnType<typeof userEvent.setup>;

function badGateway(): ApiClientError {
  return new ApiClientError(502, {
    error: "bad_gateway",
    message: "Failed to connect to Proxmox",
  });
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

/**
 * Lets the clock move on, so that what was read before is older than what is
 * done next by more than the clock's grain: Edit tells a read made after it was
 * pressed by its timestamp.
 */
async function aMoment(ms = 5): Promise<void> {
  await act(async () => {
    await new Promise<void>((resolve) => {
      setTimeout(resolve, ms);
    });
  });
}

/**
 * Takes focus off whatever has it, as browsers that move focus off an element
 * that becomes disabled do. jsdom does not: it leaves focus on the disabled
 * button, which blur() cannot take it from either, so focus goes through another
 * element and off it.
 */
function loseFocus() {
  const elsewhere = document.createElement("input");
  document.body.append(elsewhere);
  act(() => {
    elsewhere.focus();
    elsewhere.blur();
  });
  elsewhere.remove();
  expect(document.body).toHaveFocus();
}

/**
 * Serves each route's reads in turn, the last repeating, so a test that cares
 * only about the first passes one. Every Edit pressed is a read of its own, so
 * a test that counts them counts that too.
 */
function serve(routes: Record<string, unknown[]>) {
  const served: Record<string, number> = {};
  mockedGet.mockImplementation((path: string) => {
    const reads = routes[path];
    if (reads === undefined) {
      return Promise.reject(new Error(`unexpected GET ${path}`));
    }
    const call = served[path] ?? 0;
    served[path] = call + 1;
    return Promise.resolve(reads[Math.min(call, reads.length - 1)]);
  });
}

/** How many times `url` was read. */
function reads(url: string): number {
  return mockedGet.mock.calls.filter(([path]) => path === url).length;
}

/** The body of the nth PUT. */
function putBody(n = 0): unknown {
  return mockedPut.mock.calls[n]?.[1];
}

interface RenderOptions {
  /** Draw a read probe beside the host: see waitForRead. */
  probe?: boolean;
}

interface Rendered {
  qc: QueryClient;
  unmount: () => void;
}

/**
 * Renders what the page draws for one of the cards: the Edit button (in the
 * card's header) and the note under its rows, on `qc`.
 */
function renderHost(
  host: ReactNode,
  note: ReactNode,
  probe: ReactNode,
  { probe: probed = false }: RenderOptions,
  qc: QueryClient,
): Rendered {
  const view = render(
    <QueryClientProvider client={qc}>
      {host}
      {note}
      {probed && probe}
    </QueryClientProvider>,
  );
  return { qc, unmount: view.unmount };
}

function renderDNS(
  over: RenderOptions = {},
  qc: QueryClient = createAppQueryClient(),
): Rendered {
  return renderHost(
    <EditDNSDialog clusterId={CLUSTER} nodeName={NODE} />,
    <DNSReadNote clusterId={CLUSTER} nodeName={NODE} />,
    <DNSProbe />,
    over,
    qc,
  );
}

function renderTimezone(
  over: RenderOptions = {},
  qc: QueryClient = createAppQueryClient(),
): Rendered {
  return renderHost(
    <EditTimezoneDialog clusterId={CLUSTER} nodeName={NODE} />,
    <TimezoneReadNote clusterId={CLUSTER} nodeName={NODE} />,
    <TimeProbe />,
    over,
    qc,
  );
}

/**
 * What the cache holds for a node's read, drawn beside the host that is reading
 * the same one: "<status>|<the value the tests change>". A refresh is in the
 * cache before it has reached the components, so a test that asserts what a
 * host did with one has to wait for it to show here first, or it asserts on a
 * host that has not seen it yet and cannot fail.
 */
function DNSProbe() {
  const { status, data } = useNodeDNS(CLUSTER, NODE);
  return <output data-testid="probe">{`${status}|${data?.dns1 ?? ""}`}</output>;
}

function TimeProbe() {
  const { status, data } = useNodeTime(CLUSTER, NODE);
  return (
    <output data-testid="probe">{`${status}|${data?.timezone ?? ""}`}</output>
  );
}

/**
 * Waits for the read probe to show the read in `status` holding `value`, then
 * for the effects, and the updates they make, that the render it was drawn in
 * brought with it.
 */
async function waitForRead(status: "success" | "error", value: string) {
  await waitFor(() => {
    expect(screen.getByTestId("probe")).toHaveTextContent(`${status}|${value}`);
  });
  await flush();
}

function field(dialog: HTMLElement, label: string): HTMLInputElement {
  return within(dialog).getByLabelText<HTMLInputElement>(label);
}

async function setField(
  user: UserEvent,
  dialog: HTMLElement,
  label: string,
  value: string,
) {
  const input = field(dialog, label);
  await user.clear(input);
  if (value !== "") await user.type(input, value);
}

function saveButton(dialog: HTMLElement): HTMLElement {
  return within(dialog).getByRole("button", { name: "Save" });
}

async function save(user: UserEvent, dialog: HTMLElement) {
  await user.click(saveButton(dialog));
}

/** Escape, Cancel and Close each end the dialog. */
const LEAVES: [
  name: string,
  leave: (user: UserEvent, dialog: HTMLElement) => Promise<void>,
][] = [
  [
    "Cancel",
    async (user, dialog) => {
      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    },
  ],
  [
    "Escape",
    async (user) => {
      await user.keyboard("{Escape}");
    },
  ],
  [
    "its Close button",
    async (user, dialog) => {
      await user.click(within(dialog).getByRole("button", { name: "Close" }));
    },
  ],
];

/**
 * What the two dialogs have in common, for the tests that do not care which of
 * them it is. `field` is the one the tests type into, `stored` and `refreshed`
 * two reads that differ in it, `after(typed)` what the node holds once `typed`
 * has been saved into it, and `untouched` another field the two reads differ in,
 * where there is one.
 */
interface Kind {
  render: (over?: RenderOptions, qc?: QueryClient) => Rendered;
  url: string;
  key: string[];
  button: string;
  dialog: string;
  /** What the note under the card's rows says it could not load. */
  noteSubject: string;
  /** What a toast says of an Edit whose read failed: it names the node. */
  toastSubject: string;
  field: string;
  stored: unknown;
  storedValue: string;
  refreshed: unknown;
  refreshedValue: string;
  typed: string;
  after: (typed: string) => unknown;
  untouched?: { field: string; value: string };
}

const DNS_KIND: Kind = {
  render: renderDNS,
  url: DNS_URL,
  key: DNS_KEY,
  button: "Edit DNS settings",
  dialog: `Edit DNS Configuration - ${NODE}`,
  noteSubject: "this node's DNS settings",
  toastSubject: `the DNS settings of ${NODE}`,
  field: "DNS Server 1",
  stored: DNS,
  storedValue: DNS.dns1,
  refreshed: DNS_LATER,
  refreshedValue: DNS_LATER.dns1,
  typed: "192.0.2.77",
  after: (typed) => ({ ...DNS, dns1: typed }),
  untouched: { field: "Search Domain", value: DNS.search },
};

const TIMEZONE_KIND: Kind = {
  render: renderTimezone,
  url: TIME_URL,
  key: TIME_KEY,
  button: "Edit timezone",
  dialog: `Edit Timezone - ${NODE}`,
  noteSubject: "this node's timezone",
  toastSubject: `the timezone of ${NODE}`,
  field: "Timezone",
  stored: time("Europe/London"),
  storedValue: "Europe/London",
  refreshed: time("Etc/UTC"),
  refreshedValue: "Etc/UTC",
  typed: "Etc/UTC",
  after: (typed) => time(typed),
};

// A table of [name, kind]: a name that is a plain string reads without quotes.
const KINDS: [name: string, kind: Kind][] = [
  ["DNS", DNS_KIND],
  ["timezone", TIMEZONE_KIND],
];

function editButton(kind: Kind) {
  return screen.findByRole("button", { name: kind.button });
}

function queryEditButton(kind: Kind) {
  return screen.queryByRole("button", { name: kind.button });
}

async function openDialog(user: UserEvent, kind: Kind): Promise<HTMLElement> {
  await user.click(await editButton(kind));
  return screen.findByRole("dialog", { name: kind.dialog });
}

beforeEach(() => {
  mockedGet.mockReset();
  mockedPut.mockReset();
  mockedToastError.mockReset();
  mockedPut.mockResolvedValue({ status: "ok" });
});

afterEach(() => {
  onlineManager.setOnline(true);
});

describe.each(KINDS)("the %s Edit button", (_, kind) => {
  it("is not offered while the node is still being read, and is once it has been", async () => {
    const held = deferred<unknown>();
    mockedGet.mockReturnValueOnce(held.promise);
    kind.render();

    await flush();
    expect(queryEditButton(kind)).toBeNull();
    // It is waiting for the read, which is what the button is held for.
    expect(reads(kind.url)).toBe(1);

    held.resolve(kind.stored);
    expect(await editButton(kind)).toBeInTheDocument();
  });

  it("says nothing of a failure while the read is merely loading", async () => {
    const held = deferred<unknown>();
    mockedGet.mockReturnValueOnce(held.promise);
    kind.render();

    await flush();

    expect(screen.queryByRole("button")).toBeNull();
    expect(screen.queryByRole("status")).toBeNull();
    held.resolve(kind.stored);
    await editButton(kind);
  });

  it("is not offered for a read that failed, which the card says with its reason and offers a retry", async () => {
    const user = userEvent.setup();
    // The first read fails; the retry gets the node's.
    mockedGet.mockRejectedValueOnce(badGateway());
    mockedGet.mockResolvedValue(kind.stored);
    kind.render();

    expect(
      await screen.findByText(
        `Could not load ${kind.noteSubject}: Failed to connect to Proxmox`,
        { exact: false },
      ),
    ).toBeInTheDocument();
    expect(queryEditButton(kind)).toBeNull();

    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await editButton(kind)).toBeInTheDocument();
    expect(screen.queryByText(/Could not load/)).toBeNull();
    expect(reads(kind.url)).toBe(2);
  });

  it("holds the card's retry while it reads, disabled and saying so", async () => {
    const user = userEvent.setup();
    // The first read fails; the retry's is held.
    const held = deferred<unknown>();
    mockedGet.mockRejectedValueOnce(badGateway());
    mockedGet.mockReturnValueOnce(held.promise);
    kind.render();

    await user.click(await screen.findByRole("button", { name: "Retry" }));

    expect(
      await screen.findByRole("button", { name: "Retrying..." }),
    ).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
    held.resolve(kind.stored);
    expect(await editButton(kind)).toBeInTheDocument();
  });

  it("says a paused retry is paused, and offers no retry that could do nothing", async () => {
    const user = userEvent.setup();
    mockedGet.mockRejectedValueOnce(badGateway());
    mockedGet.mockResolvedValue(kind.stored);
    kind.render();
    const retry = await screen.findByRole("button", { name: "Retry" });

    // Offline, the read is parked rather than sent: nothing is retrying, and a
    // button that says so is not telling the truth.
    act(() => {
      onlineManager.setOnline(false);
    });
    await user.click(retry);

    expect(
      await screen.findByText(/The retry is paused\./),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Retry/ })).toBeNull();
    expect(queryEditButton(kind)).toBeNull();

    // Control: back online, the parked read goes out and the card has its node.
    act(() => {
      onlineManager.setOnline(true);
    });
    expect(await editButton(kind)).toBeInTheDocument();
  });

  it("is still offered after a refresh failed and left the data, and Edit then reads for itself", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    const { qc } = kind.render({ probe: true });
    expect(await editButton(kind)).toBeInTheDocument();

    mockedGet.mockRejectedValueOnce(badGateway());
    await act(async () => {
      await qc.invalidateQueries({ queryKey: kind.key });
    });

    // The query is now in error, and still holds what it read before. The card
    // has been told, and has no note: it has something to edit from.
    await waitForRead("error", kind.storedValue);
    expect(qc.getQueryData(kind.key)).toEqual(kind.stored);
    expect(screen.queryByText(/Could not load/)).toBeNull();
    const dialog = await openDialog(user, kind);
    expect(field(dialog, kind.field)).toHaveValue(kind.storedValue);
    expect(reads(kind.url)).toBe(3);
  });
});

// Edit is a read of its own. What the page took before is what the node held
// then, and nothing here can tell whether it still does.
describe.each(KINDS)("pressing the %s Edit button", (_, kind) => {
  it("reads the node again, and opens on that read, not on what the page held", async () => {
    const user = userEvent.setup();
    // The page's read; then the node changes behind the page; then the press.
    serve({ [kind.url]: [kind.stored, kind.refreshed] });
    kind.render();
    await editButton(kind);
    expect(reads(kind.url)).toBe(1);

    const dialog = await openDialog(user, kind);

    expect(reads(kind.url)).toBe(2);
    expect(field(dialog, kind.field)).toHaveValue(kind.refreshedValue);
  });

  it("holds the button while the read is out, and a second press sends no second read", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();
    const edit = await editButton(kind);
    const held = deferred<unknown>();
    mockedGet.mockReturnValueOnce(held.promise);

    await user.click(edit);

    expect(edit).toBeDisabled();
    expect(edit).toHaveAttribute("aria-busy", "true");
    // A disabled button that does not say why is a button that looks broken. The
    // title shows on hover only if the disabled button takes pointer events,
    // which jsdom, with no layout, can only be asked about by the class.
    expect(edit).toHaveAttribute("title", `Reading ${kind.toastSubject}...`);
    expect(edit).toHaveClass("disabled:pointer-events-auto");
    expect(screen.queryByRole("dialog")).toBeNull();
    await user.click(edit);
    await flush();
    expect(reads(kind.url)).toBe(2);

    held.resolve(kind.refreshed);
    const dialog = await screen.findByRole("dialog", { name: kind.dialog });
    expect(field(dialog, kind.field)).toHaveValue(kind.refreshedValue);
    expect(reads(kind.url)).toBe(2);
    expect(edit).toBeEnabled();
    expect(edit).not.toHaveAttribute("aria-busy");
    expect(edit).not.toHaveAttribute("title");
  });

  it("opens nothing from a read that failed, says so in a toast naming the node, and can be pressed again", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    const { qc } = kind.render();
    const edit = await editButton(kind);

    mockedGet.mockRejectedValueOnce(badGateway());
    await user.click(edit);

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(
        `Could not load ${kind.toastSubject}: Failed to connect to Proxmox`,
      );
    });
    await flush();
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(edit).toBeEnabled();
    expect(edit).not.toHaveAttribute("aria-busy");
    // The page's read is still there: a failed read keeps its data, and that is
    // what the dialog must not open from.
    expect(qc.getQueryData(kind.key)).toEqual(kind.stored);

    // Control: pressed again, with the node answering, the same button opens.
    const dialog = await openDialog(user, kind);
    expect(field(dialog, kind.field)).toHaveValue(kind.storedValue);
    expect(mockedToastError).toHaveBeenCalledTimes(1);
  });

  it("says a read that got no answer at all without a reason, rather than with an empty one", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();
    const edit = await editButton(kind);

    // How a dropped connection rejects fetch: describeError leaves it out.
    mockedGet.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    await user.click(edit);

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(
        `Could not load ${kind.toastSubject}.`,
      );
    });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("replaces a refresh that was already on its way, whose late answer then changes nothing", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    const { qc } = kind.render();
    await editButton(kind);
    // A refresh (an inventory event, a reconnect) that is slow to answer ...
    const late = deferred<unknown>();
    mockedGet.mockReturnValueOnce(late.promise);
    act(() => {
      void qc.invalidateQueries({ queryKey: kind.key });
    });
    await waitFor(() => {
      expect(reads(kind.url)).toBe(2);
    });
    // ... and the node has changed since it was asked.
    mockedGet.mockResolvedValue(kind.refreshed);

    const dialog = await openDialog(user, kind);

    // The press did not wait for it, or take what it brings.
    expect(field(dialog, kind.field)).toHaveValue(kind.refreshedValue);
    await act(async () => {
      late.resolve(kind.stored);
      await new Promise<void>((resolve) => {
        setTimeout(resolve, 20);
      });
    });
    await flush();
    expect(qc.getQueryData(kind.key)).toEqual(kind.refreshed);
    expect(field(dialog, kind.field)).toHaveValue(kind.refreshedValue);
  });

  // The press's own read can be cancelled by a refresh that starts after it.
  // What it then waits for is the refresh's answer, which is a read made after
  // the press too, and not the cancelled read's, which is old by then.
  it("opens on the read that replaced it when a refresh cancels its read, and not on the one that was cancelled", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    const { qc } = kind.render();
    const edit = await editButton(kind);
    const pressed = deferred<unknown>();
    mockedGet.mockReturnValueOnce(pressed.promise);
    await user.click(edit);
    const refresh = deferred<unknown>();
    mockedGet.mockReturnValueOnce(refresh.promise);

    act(() => {
      void qc.invalidateQueries({ queryKey: NODES_PREFIX });
    });
    await waitFor(() => {
      expect(reads(kind.url)).toBe(3);
    });
    // The cancelled read answers with the node as it was.
    await act(async () => {
      pressed.resolve(kind.stored);
      await new Promise<void>((resolve) => {
        setTimeout(resolve, 20);
      });
    });
    await flush();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(edit).toBeDisabled();

    refresh.resolve(kind.refreshed);
    const dialog = await screen.findByRole("dialog", { name: kind.dialog });
    expect(field(dialog, kind.field)).toHaveValue(kind.refreshedValue);
  });

  it("says nothing of a read that ends after its button is gone", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    const { unmount } = kind.render();
    const edit = await editButton(kind);
    const held = deferred<unknown>();
    mockedGet.mockReturnValueOnce(held.promise);
    await user.click(edit);
    expect(edit).toHaveAttribute("aria-busy", "true");

    // The page is left, or the node changes, or the permission goes.
    unmount();
    held.reject(badGateway());
    await flush();

    expect(mockedToastError).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("is held while a save of the setting is in flight, even with its dialog gone", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();
    const dialog = await openDialog(user, kind);
    await setField(user, dialog, kind.field, kind.typed);
    const heldPut = deferred<unknown>();
    mockedPut.mockReturnValueOnce(heldPut.promise);
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    // The operator gives up waiting on the dialog; the write is still out.
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    // A read now would answer with the setting as it was before the write.
    const edit = await editButton(kind);
    expect(edit).toBeDisabled();
    expect(edit).toHaveAttribute("title", `Saving ${kind.toastSubject}...`);
    const before = reads(kind.url);
    await user.click(edit);
    await flush();
    expect(reads(kind.url)).toBe(before);

    // The write lands, and the button is let go.
    mockedGet.mockResolvedValue(kind.after(kind.typed));
    heldPut.resolve({ status: "ok" });
    await waitFor(() => {
      expect(edit).toBeEnabled();
    });
    expect(edit).not.toHaveAttribute("title");
    const again = await openDialog(user, kind);
    expect(field(again, kind.field)).toHaveValue(kind.typed);
  });

  // The dialog is gone, so the failure has nowhere to be shown but the app's own
  // net, which toasts it once; and the button, which was held for the write, is
  // let go: a failed write changed nothing, and a read is then as good as ever.
  it("is let go when a save dismissed with its dialog fails, and the failure is toasted once", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();
    const dialog = await openDialog(user, kind);
    await setField(user, dialog, kind.field, kind.typed);
    const heldPut = deferred<unknown>();
    mockedPut.mockReturnValueOnce(heldPut.promise);
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    const edit = await editButton(kind);
    expect(edit).toBeDisabled();

    heldPut.reject(
      new ApiClientError(403, { error: "forbidden", message: DENIED }),
    );

    await waitFor(() => {
      expect(edit).toBeEnabled();
    });
    await flush();
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(DENIED);
    // Control: the button reads again, and opens.
    const again = await openDialog(user, kind);
    expect(field(again, kind.field)).toHaveValue(kind.storedValue);
  });

  // In browsers that move focus off a button that becomes disabled, a keyboard
  // user whose press fails is left on <body> with nothing to tell them the
  // button is back.
  it("takes focus back when a press ends with nothing opened, if the browser had taken it off the disabled button", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();
    const edit = await editButton(kind);
    const held = deferred<unknown>();
    mockedGet.mockReturnValueOnce(held.promise);
    await user.click(edit);
    loseFocus();
    // After the press, which focuses the button itself (user-event does).
    const focus = vi.spyOn(edit, "focus");

    held.reject(badGateway());

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledTimes(1);
    });
    await waitFor(() => {
      expect(edit).toHaveFocus();
    });
    expect(edit).toBeEnabled();
    // The press may end long after the user scrolled away, to the charts below:
    // focus() without this option would scroll the page back up to the card.
    expect(focus).toHaveBeenCalledTimes(1);
    expect(focus).toHaveBeenCalledWith({ preventScroll: true });
  });

  // The button asks for focus once, for the press that ended with nothing
  // opened, and is done with it: a later press that opens leaves focus to the
  // dialog, whatever became of focus while it read.
  it("does not ask for focus when a press opens, even after an earlier press that ended with nothing opened", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();
    const edit = await editButton(kind);
    const focus = vi.spyOn(edit, "focus");
    const failing = deferred<unknown>();
    mockedGet.mockReturnValueOnce(failing.promise);
    // A press focuses the button itself (user-event does): not what is counted.
    await user.click(edit);
    loseFocus();
    focus.mockClear();
    failing.reject(badGateway());
    await waitFor(() => {
      expect(edit).toHaveFocus();
    });
    // Control: that press asked for it.
    expect(focus).toHaveBeenCalledTimes(1);

    const opening = deferred<unknown>();
    mockedGet.mockReturnValueOnce(opening.promise);
    await user.click(edit);
    loseFocus();
    focus.mockClear();
    opening.resolve(kind.refreshed);

    const dialog = await screen.findByRole("dialog", { name: kind.dialog });
    expect(focus).not.toHaveBeenCalled();
    expect(dialog.contains(document.activeElement)).toBe(true);
  });

  // The stamp of a read is compared with the press's by >=: a clock that has not
  // moved between the two, the extreme case of a read answered within the same
  // millisecond, still means a read made after the press.
  it("opens on a read answered in the very millisecond of the press", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    try {
      vi.setSystemTime(2_000_000_000_000);
      const user = userEvent.setup();
      serve({ [kind.url]: [kind.stored, kind.refreshed] });
      kind.render();

      const dialog = await openDialog(user, kind);

      expect(field(dialog, kind.field)).toHaveValue(kind.refreshedValue);
      expect(mockedToastError).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("leaves focus where the user has put it when a press ends with nothing opened", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();
    const edit = await editButton(kind);
    const held = deferred<unknown>();
    mockedGet.mockReturnValueOnce(held.promise);
    await user.click(edit);
    // While the read is out, focus goes somewhere else on purpose.
    const elsewhere = document.createElement("input");
    document.body.append(elsewhere);
    act(() => {
      elsewhere.focus();
    });

    held.reject(badGateway());

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledTimes(1);
    });
    await waitFor(() => {
      expect(edit).toBeEnabled();
    });
    expect(elsewhere).toHaveFocus();
    elsewhere.remove();
  });

  it("sends focus back to the button when the browser took it off the disabled button", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();
    const edit = await editButton(kind);
    const held = deferred<unknown>();
    mockedGet.mockReturnValueOnce(held.promise);
    await user.click(edit);

    // Browsers that move focus off an element that becomes disabled leave the
    // dialog nothing to go back to when it opens.
    loseFocus();
    held.resolve(kind.stored);
    const dialog = await screen.findByRole("dialog", { name: kind.dialog });
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    await waitFor(() => {
      expect(edit).toHaveFocus();
    });
  });
});

// The press's refetch() resolves as a SUCCESS carrying the OLD data when its
// read is cancelled with nothing to replace it (cancelQueries puts the query back
// as it was) and when the query is dropped from under it (clear(), removeQueries()).
// None of that is a read made after the press, and none of it is a failure: it is
// a sign-out as often as anything. Nothing opens, nothing is said, and the button
// is usable again.
describe.each(KINDS)(
  "pressing the %s Edit button when its read is cancelled or its query dropped",
  (_, kind) => {
    /** Presses Edit with the read held, a moment after the page's own read. */
    async function pressHeld(user: UserEvent) {
      serve({ [kind.url]: [kind.stored] });
      const rendered = kind.render();
      const edit = await editButton(kind);
      await aMoment();
      const held = deferred<unknown>();
      mockedGet.mockReturnValueOnce(held.promise);
      await user.click(edit);
      expect(edit).toHaveAttribute("aria-busy", "true");
      return { ...rendered, held };
    }

    /**
     * Nothing opened and nothing was said, and what the held read answers when it
     * is finally let go changes nothing; the button is back and works, which a
     * press with the node answering shows by opening. (The query can have been
     * rebuilt under the host, so the button is looked up again.)
     */
    async function nothingOpenedAndPressedAgain(
      user: UserEvent,
      held: { resolve: (value: unknown) => void },
    ) {
      await flush();
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(mockedToastError).not.toHaveBeenCalled();
      const edit = await editButton(kind);
      await waitFor(() => {
        expect(edit).toBeEnabled();
      });
      expect(edit).not.toHaveAttribute("aria-busy");

      held.resolve(kind.refreshed);
      await flush();
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(mockedToastError).not.toHaveBeenCalled();

      const dialog = await openDialog(user, kind);
      expect(field(dialog, kind.field)).toHaveValue(kind.storedValue);
    }

    it("opens nothing, and says nothing, when the read is cancelled and the query goes back to the old data", async () => {
      const user = userEvent.setup();
      const { qc, held } = await pressHeld(user);

      await act(async () => {
        await qc.cancelQueries({ queryKey: kind.key });
      });

      await nothingOpenedAndPressedAgain(user, held);
    });

    it("opens nothing, and says nothing, when the read is cancelled without a revert, which leaves the query in error", async () => {
      const user = userEvent.setup();
      const { qc, held } = await pressHeld(user);

      await act(async () => {
        await qc.cancelQueries({ queryKey: kind.key }, { revert: false });
      });

      // The cancellation is the query's error here, and not a failure to say.
      await nothingOpenedAndPressedAgain(user, held);
    });

    it("opens nothing, and says nothing, when the whole cache is cleared under the read", async () => {
      const user = userEvent.setup();
      const { qc, held } = await pressHeld(user);

      act(() => {
        qc.clear();
      });

      await nothingOpenedAndPressedAgain(user, held);
    });

    it("opens nothing, and says nothing, when the query is removed from under the read", async () => {
      const user = userEvent.setup();
      const { qc, held } = await pressHeld(user);

      act(() => {
        qc.removeQueries({ queryKey: kind.key });
      });

      await nothingOpenedAndPressedAgain(user, held);
    });

    it("shows no dialog even for a moment when the page outlives the cancellation by a task", async () => {
      const user = userEvent.setup();
      const { qc, held, unmount } = await pressHeld(user);

      // A sign-out cancels everything; the page goes a task later, as it does
      // while a router transition is pending.
      void qc.cancelQueries();
      await act(async () => {
        await new Promise<void>((resolve) => {
          setTimeout(resolve, 0);
        });
      });
      expect(screen.queryByRole("dialog")).toBeNull();
      unmount();
      held.resolve(kind.refreshed);
      await flush();

      expect(screen.queryByRole("dialog")).toBeNull();
      expect(mockedToastError).not.toHaveBeenCalled();
    });

    // resetQueries() zeroes the query's count of data updates and refetches it:
    // a fresh read made after the press, which a count would refuse.
    it("opens on the read that a reset of the query started, which is a read made after the press", async () => {
      const user = userEvent.setup();
      const { qc, held } = await pressHeld(user);
      mockedGet.mockResolvedValue(kind.refreshed);

      await act(async () => {
        await qc.resetQueries({ queryKey: kind.key });
      });

      const dialog = await screen.findByRole("dialog", { name: kind.dialog });
      expect(field(dialog, kind.field)).toHaveValue(kind.refreshedValue);
      // The read it replaced answers with the node as it was.
      held.resolve(kind.stored);
      await flush();
      expect(field(dialog, kind.field)).toHaveValue(kind.refreshedValue);
      expect(mockedToastError).not.toHaveBeenCalled();
    });

    it("opens nothing from the cancelled read's old data when the read that replaced it fails, and says so once", async () => {
      const user = userEvent.setup();
      const { qc, held } = await pressHeld(user);
      mockedGet.mockRejectedValueOnce(badGateway());

      act(() => {
        void qc.invalidateQueries({ queryKey: NODES_PREFIX });
      });
      await waitFor(() => {
        expect(reads(kind.url)).toBe(3);
      });
      // The cancelled read answers with the node as it was.
      await act(async () => {
        held.resolve(kind.stored);
        await new Promise<void>((resolve) => {
          setTimeout(resolve, 20);
        });
      });
      await flush();

      expect(screen.queryByRole("dialog")).toBeNull();
      await waitFor(() => {
        expect(mockedToastError).toHaveBeenCalledWith(
          `Could not load ${kind.toastSubject}: Failed to connect to Proxmox`,
        );
      });
      expect(mockedToastError).toHaveBeenCalledTimes(1);
    });

    it("takes focus back when a press ends with its read cancelled, if the browser had taken it off the disabled button", async () => {
      const user = userEvent.setup();
      const { qc, held } = await pressHeld(user);
      const focus = vi.spyOn(await editButton(kind), "focus");
      loseFocus();

      await act(async () => {
        await qc.cancelQueries({ queryKey: kind.key });
      });

      const edit = await editButton(kind);
      await waitFor(() => {
        expect(edit).toHaveFocus();
      });
      expect(focus).toHaveBeenCalledWith({ preventScroll: true });
      held.resolve(kind.refreshed);
    });
  },
);

// A save changes the node. What the page read before it is old from then on, and
// an Edit pressed behind it must not put the old setting back.
describe.each(KINDS)("pressing the %s Edit button after a save", (_, kind) => {
  /** Opens the dialog, types into it, and saves, with every later read `later`. */
  async function saveWithReads(user: UserEvent, later: () => Promise<unknown>) {
    serve({ [kind.url]: [kind.stored] });
    const rendered = kind.render({ probe: true });
    const dialog = await openDialog(user, kind);
    await setField(user, dialog, kind.field, kind.typed);
    mockedGet.mockReset();
    mockedGet.mockImplementation(later);
    await save(user, dialog);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(mockedPut).toHaveBeenCalledTimes(1);
    return rendered;
  }

  // Control for the two below, which differ from it only in how slowly the node
  // answers: with the re-read already in, the page's own data is the saved
  // setting too.
  it("opens on what the node holds when the re-read the save asked for has already landed", async () => {
    const user = userEvent.setup();
    await saveWithReads(user, () => Promise.resolve(kind.after(kind.typed)));
    await waitForRead("success", kind.typed);

    const dialog = await openDialog(user, kind);

    expect(field(dialog, kind.field)).toHaveValue(kind.typed);
  });

  it("opens on what the node holds now once the read lands, not on the read before the save", async () => {
    const user = userEvent.setup();
    // The node holds the saved value, but its answers are slow: the re-read the
    // save asked for, and the read of the press, are both still out.
    const held = deferred<unknown>();
    await saveWithReads(user, () => held.promise);
    const edit = await editButton(kind);
    await waitFor(() => {
      expect(edit).toBeEnabled();
    });

    await user.click(edit);

    await flush();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(edit).toBeDisabled();
    held.resolve(kind.after(kind.typed));
    const dialog = await screen.findByRole("dialog", { name: kind.dialog });
    expect(field(dialog, kind.field)).toHaveValue(kind.typed);
  });

  it("opens nothing, and says why, when the read after a save fails and the page holds only the read before it", async () => {
    const user = userEvent.setup();
    const { qc } = await saveWithReads(user, () =>
      Promise.reject(badGateway()),
    );
    // The re-read the save asked for failed too, and left the old data.
    await waitForRead("error", kind.storedValue);
    expect(qc.getQueryData(kind.key)).toEqual(kind.stored);
    const edit = await editButton(kind);

    await user.click(edit);

    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(
        `Could not load ${kind.toastSubject}: Failed to connect to Proxmox`,
      );
    });
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

describe("pressing the DNS Edit button after a save, a second time", () => {
  // The harm the stale read did: a second save carried the first one's old
  // search domain back to the node.
  it("saves on top of the first save, not on top of what it replaced", async () => {
    const user = userEvent.setup();
    serve({ [DNS_URL]: [DNS] });
    renderDNS({ probe: true });
    const first = await openDialog(user, DNS_KIND);
    await setField(user, first, "Search Domain", "new.example.com");
    // From here every read is slow: the re-read the save asks for, and the
    // read of the press, which both answer with the node as the save left it.
    const held = deferred<unknown>();
    mockedGet.mockReset();
    mockedGet.mockImplementation(() => held.promise);
    await save(user, first);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    const edit = await editButton(DNS_KIND);
    await waitFor(() => {
      expect(edit).toBeEnabled();
    });
    await user.click(edit);
    await flush();
    expect(screen.queryByRole("dialog")).toBeNull();
    held.resolve({ ...DNS, search: "new.example.com" });
    const second = await screen.findByRole("dialog", {
      name: DNS_KIND.dialog,
    });
    expect(field(second, "Search Domain")).toHaveValue("new.example.com");
    await setField(user, second, "DNS Server 3", "192.0.2.78");
    await save(user, second);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(putBody(0)).toEqual({
      search: "new.example.com",
      dns1: "192.0.2.53",
      dns2: "192.0.2.54",
      dns3: "192.0.2.55",
    });
    expect(putBody(1)).toEqual({
      search: "new.example.com",
      dns1: "192.0.2.53",
      dns2: "192.0.2.54",
      dns3: "192.0.2.78",
    });
  });
});

describe.each(KINDS)("the %s dialog", (_, kind) => {
  it("is not overwritten by a refresh that lands while it is open, and the next one is drawn from it", async () => {
    const user = userEvent.setup();
    // The page's read, the read of the press, and then a refresh.
    serve({ [kind.url]: [kind.stored, kind.stored, kind.refreshed] });
    const { qc } = kind.render({ probe: true });
    const dialog = await openDialog(user, kind);
    await setField(user, dialog, kind.field, "typed-by-the-operator");

    await act(async () => {
      await qc.invalidateQueries({ queryKey: kind.key });
    });

    // The refresh did land, and has reached the components reading it ...
    await waitForRead("success", kind.refreshedValue);
    expect(qc.getQueryData(kind.key)).toEqual(kind.refreshed);
    expect(reads(kind.url)).toBe(3);
    // ... and the form kept what was typed, and what it was drawn from.
    expect(field(dialog, kind.field)).toHaveValue("typed-by-the-operator");
    if (kind.untouched !== undefined) {
      expect(field(dialog, kind.untouched.field)).toHaveValue(
        kind.untouched.value,
      );
    }

    // Control: a dialog opened now is drawn from the refresh, so the one above
    // kept its values for a reason other than the read never changing.
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    const again = await openDialog(user, kind);
    expect(field(again, kind.field)).toHaveValue(kind.refreshedValue);
  });

  it("is not overwritten by a refresh that fails behind it either", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    const { qc } = kind.render({ probe: true });
    const dialog = await openDialog(user, kind);
    await setField(user, dialog, kind.field, "typed-by-the-operator");

    mockedGet.mockRejectedValueOnce(badGateway());
    await act(async () => {
      await qc.invalidateQueries({ queryKey: kind.key });
    });

    // The failure has reached the components reading it, and left the dialog be.
    await waitForRead("error", kind.storedValue);
    expect(field(dialog, kind.field)).toHaveValue("typed-by-the-operator");
    expect(dialog).toHaveAttribute("data-state", "open");
  });

  it.each(LEAVES)(
    "sends nothing when it is left with %s, and focus goes back to the Edit button",
    async (_, leave) => {
      const user = userEvent.setup();
      serve({ [kind.url]: [kind.stored] });
      kind.render();

      const edit = await editButton(kind);
      const dialog = await openDialog(user, kind);
      await setField(user, dialog, kind.field, "typed-by-the-operator");
      await leave(user, dialog);

      await waitFor(() => {
        expect(screen.queryByRole("dialog")).toBeNull();
      });
      await waitFor(() => {
        expect(edit).toHaveFocus();
      });
      expect(mockedPut).not.toHaveBeenCalled();
    },
  );

  it("opens again from the node's values, not from what was typed and left", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();

    const dialog = await openDialog(user, kind);
    await setField(user, dialog, kind.field, "typed-and-abandoned");
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    const again = await openDialog(user, kind);
    expect(field(again, kind.field)).toHaveValue(kind.storedValue);
  });

  it("closes when the save goes through", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    kind.render();

    const dialog = await openDialog(user, kind);
    await setField(user, dialog, kind.field, "typed-by-the-operator");
    await save(user, dialog);

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(mockedPut).toHaveBeenCalledTimes(1);
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("stays open with what was typed when the save fails, and the failure is toasted once", async () => {
    const user = userEvent.setup();
    serve({ [kind.url]: [kind.stored] });
    mockedPut.mockRejectedValueOnce(
      new ApiClientError(403, { error: "forbidden", message: DENIED }),
    );
    kind.render();

    const dialog = await openDialog(user, kind);
    await setField(user, dialog, kind.field, "typed-by-the-operator");
    await save(user, dialog);

    // Reported by the app's own net: the mutation has no handler of its own,
    // and this client has the app's mutation cache.
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(DENIED);
    });
    await flush();
    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(dialog).toBeInTheDocument();
    expect(dialog).toHaveAttribute("data-state", "open");
    expect(field(dialog, kind.field)).toHaveValue("typed-by-the-operator");
    expect(saveButton(dialog)).toBeEnabled();
  });
});

describe("the DNS dialog", () => {
  it("opens with all four settings the node has", async () => {
    const user = userEvent.setup();
    serve({ [DNS_URL]: [DNS] });
    renderDNS();

    const dialog = await openDialog(user, DNS_KIND);

    expect(field(dialog, "Search Domain")).toHaveValue(DNS.search);
    expect(field(dialog, "DNS Server 1")).toHaveValue(DNS.dns1);
    expect(field(dialog, "DNS Server 2")).toHaveValue(DNS.dns2);
    expect(field(dialog, "DNS Server 3")).toHaveValue(DNS.dns3);
  });

  // The write replaces every setting: a resolver left out of it is removed from
  // the node.
  it("sends all four settings when only one was changed, the ones left alone as they were read", async () => {
    const user = userEvent.setup();
    serve({ [DNS_URL]: [DNS] });
    renderDNS();

    const dialog = await openDialog(user, DNS_KIND);
    await setField(user, dialog, "Search Domain", "new.example.com");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls[0]?.[0]).toBe(DNS_URL);
    expect(putBody()).toEqual({
      search: "new.example.com",
      dns1: "192.0.2.53",
      dns2: "192.0.2.54",
      dns3: "192.0.2.55",
    });
  });

  it("sends a resolver that was emptied as empty, which is how it is removed", async () => {
    const user = userEvent.setup();
    serve({ [DNS_URL]: [DNS] });
    renderDNS();

    const dialog = await openDialog(user, DNS_KIND);
    await setField(user, dialog, "DNS Server 2", "");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      search: "corp.example.com",
      dns1: "192.0.2.53",
      dns2: "",
      dns3: "192.0.2.55",
    });
  });

  it("sends the resolvers a node has none of as empty, beside the one that was added", async () => {
    const user = userEvent.setup();
    serve({
      [DNS_URL]: [{ search: "corp.example.com", dns1: "", dns2: "", dns3: "" }],
    });
    renderDNS();

    const dialog = await openDialog(user, DNS_KIND);
    expect(field(dialog, "DNS Server 1")).toHaveValue("");
    await setField(user, dialog, "DNS Server 1", "192.0.2.53");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      search: "corp.example.com",
      dns1: "192.0.2.53",
      dns2: "",
      dns3: "",
    });
  });

  it("cannot be saved without a search domain, which Proxmox requires", async () => {
    const user = userEvent.setup();
    serve({ [DNS_URL]: [DNS] });
    renderDNS();

    const dialog = await openDialog(user, DNS_KIND);
    expect(saveButton(dialog)).toBeEnabled();
    await setField(user, dialog, "Search Domain", "");

    expect(saveButton(dialog)).toBeDisabled();
    await save(user, dialog);
    expect(mockedPut).not.toHaveBeenCalled();

    await user.type(field(dialog, "Search Domain"), "new.example.com");
    expect(saveButton(dialog)).toBeEnabled();
  });
});

describe("the timezone dialog", () => {
  it("opens with the timezone the node reports", async () => {
    const user = userEvent.setup();
    serve({ [TIME_URL]: [time("Europe/London")] });
    renderTimezone();

    const dialog = await openDialog(user, TIMEZONE_KIND);

    expect(field(dialog, "Timezone")).toHaveValue("Europe/London");
  });

  it("sends the timezone it was opened with when it is saved as it is, not a default", async () => {
    const user = userEvent.setup();
    serve({ [TIME_URL]: [time("Europe/London")] });
    renderTimezone();

    const dialog = await openDialog(user, TIMEZONE_KIND);
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(mockedPut.mock.calls[0]?.[0]).toBe(TIME_URL);
    expect(putBody()).toEqual({ timezone: "Europe/London" });
  });

  it("sends the timezone that was typed", async () => {
    const user = userEvent.setup();
    serve({ [TIME_URL]: [time("Europe/London")] });
    renderTimezone();

    const dialog = await openDialog(user, TIMEZONE_KIND);
    await setField(user, dialog, "Timezone", "Etc/UTC");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ timezone: "Etc/UTC" });
  });

  // The node list's copy of the timezone is empty when the collector could not
  // read it. A node whose own read says nothing opens with nothing: filling it
  // in would make Save set a timezone nobody chose.
  it("does not make a timezone up for a node that reports none", async () => {
    const user = userEvent.setup();
    serve({ [TIME_URL]: [time("")] });
    renderTimezone();

    const dialog = await openDialog(user, TIMEZONE_KIND);

    expect(field(dialog, "Timezone")).toHaveValue("");
    expect(saveButton(dialog)).toBeDisabled();
    await save(user, dialog);
    expect(mockedPut).not.toHaveBeenCalled();

    await user.type(field(dialog, "Timezone"), "Etc/UTC");
    expect(saveButton(dialog)).toBeEnabled();
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ timezone: "Etc/UTC" });
  });
});

// The read an Edit button makes when it is pressed can be answered after the
// session it was pressed in has ended — a sign-out, an expiry, another user
// signing in. A failure toasts the node's name, and a toast raised after the
// session ended is shown to whoever is signed in by then; and what a read found
// must not seed a dialog for them. Each case is paired with the same answer in a
// session that goes on.
describe.each(KINDS)(
  "the %s Edit button, once the session it was pressed in has ended",
  (_, kind) => {
    beforeEach(() => {
      signInAsAdmin();
    });

    afterEach(() => {
      signOutForGood();
    });

    /** Edit pressed on a node already read, with the read it makes held. */
    async function pressedWithReadHeld(user: UserEvent) {
      serve({ [kind.url]: [kind.stored] });
      kind.render();
      const edit = await editButton(kind);
      const held = deferred<unknown>();
      mockedGet.mockReturnValueOnce(held.promise);
      await user.click(edit);
      expect(reads(kind.url)).toBe(2);
      return { edit, held };
    }

    it("control: says a read that failed in a toast naming the node, once, while the session goes on", async () => {
      const user = userEvent.setup();
      const { held } = await pressedWithReadHeld(user);

      held.reject(badGateway());

      await waitFor(() => {
        expect(mockedToastError).toHaveBeenCalledWith(
          `Could not load ${kind.toastSubject}: Failed to connect to Proxmox`,
        );
      });
      await flush();
      expect(mockedToastError).toHaveBeenCalledTimes(1);
    });

    it.each(SESSION_ENDS)(
      "says nothing of a read that fails after %s",
      async (_, end) => {
        const user = userEvent.setup();
        const { held } = await pressedWithReadHeld(user);

        end();
        held.reject(badGateway());
        await flush();

        expect(mockedToastError).not.toHaveBeenCalled();
        expect(screen.queryByRole("dialog")).toBeNull();
      },
    );

    it("control: opens the dialog from a read that succeeds while the session goes on", async () => {
      const user = userEvent.setup();
      const { held } = await pressedWithReadHeld(user);

      held.resolve(kind.refreshed);

      const dialog = await screen.findByRole("dialog", { name: kind.dialog });
      expect(field(dialog, kind.field)).toHaveValue(kind.refreshedValue);
    });

    it.each(SESSION_ENDS)(
      "opens nothing from a read that succeeds after %s",
      async (_, end) => {
        const user = userEvent.setup();
        const { held } = await pressedWithReadHeld(user);

        end();
        held.resolve(kind.refreshed);
        await flush();

        expect(screen.queryByRole("dialog")).toBeNull();
        expect(mockedToastError).not.toHaveBeenCalled();
      },
    );

    // The hold on the button is let go whatever the session, as the guards of the
    // other sites are: a button that is somehow still there after its read ended
    // with the session must not be left reading, and can be pressed again.
    const ANSWERS: [
      name: string,
      answer: (held: ReturnType<typeof deferred<unknown>>) => void,
    ][] = [
      [
        "fails",
        (held) => {
          held.reject(badGateway());
        },
      ],
      [
        "succeeds",
        (held) => {
          held.resolve(kind.refreshed);
        },
      ],
    ];

    it.each(
      ANSWERS.flatMap(([name, answer]) =>
        SESSION_ENDS.map(
          ([session, end]) =>
            [name, session, answer, end] as [
              string,
              string,
              (held: ReturnType<typeof deferred<unknown>>) => void,
              () => void,
            ],
        ),
      ),
    )(
      "lets the button be pressed again after a read that %s after %s",
      async (_name, _session, answer, end) => {
        const user = userEvent.setup();
        const { edit, held } = await pressedWithReadHeld(user);

        end();
        answer(held);
        await flush();

        expect(edit).toBeEnabled();
        expect(edit).not.toHaveAttribute("aria-busy");
        // And pressed again, it reads as the session it is in now, and opens.
        await user.click(edit);
        const dialog = await screen.findByRole("dialog", { name: kind.dialog });
        expect(field(dialog, kind.field)).toHaveValue(kind.storedValue);
        expect(reads(kind.url)).toBe(3);
      },
    );
  },
);
