import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
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

import { apiClient, ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import type { NodeOptions } from "../../api/node-options-queries";
import { NodeOptionsCard } from "./NodeOptionsCard";

/**
 * The Options card reads a node's own settings and edits them in a dialog that
 * is mounted afresh for each open, from a snapshot of what the card showed.
 *
 * What these pin down: a save carries only the settings that were changed, and
 * clears through `delete`; the digest it is made against is the one of the read
 * the form was drawn from, however much the card refreshes behind the dialog;
 * after a 409 the dialog re-reads through the card, and moves its pin only to a
 * read that succeeded; and a failure is reported once, in the dialog while it is
 * open and as a toast once it is gone.
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

// The app's mutation-error net (lib/query-client.ts) toasts through sonner, so
// this one mock sees every toast a run can raise.
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);
const mockedToastError = vi.mocked(toast.error);

const CLUSTER = "cccccccc-0000-0000-0000-000000000007";
const NODE = "pve-01";
const OPTIONS_URL = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/options`;
const OPTIONS_KEY = ["clusters", CLUSTER, "nodes", NODE, "options"];
const ACME_KEY = ["clusters", CLUSTER, "nodes", NODE, "acme-config"];

// pve-manager's package string, as the node list carries it. 9.2.20 has every
// gated field.
const CURRENT = "pve-manager/9.2.20/0123abcd";

const DIALOG = `Edit Options - ${NODE}`;
const MAC = "02:00:00:00:00:01";
const MAC_2 = "02:00:00:00:00:02";

// What the card's fields are called.
const DELAY = "Start on boot delay (seconds)";
const TARGET = "RAM ballooning target (%)";
const MAC_FIELD = "MAC address";
const INTERFACE = "Interface";
const BROADCAST = "Broadcast address";
const LATITUDE = "Latitude";
const LONGITUDE = "Longitude";
const NAME = "Name";

// What the server says to a stale digest, and what the dialog says in its place
// (its fields do not reload, so "reload and try again" is not advice it can give).
const STALE =
  "The node's configuration changed since it was read — reload and try again.";
const CHANGED = "This node's configuration changed while this dialog was open.";
const SAVING_AGAIN =
  "Saving again writes the settings you changed here over the node's current values for them; the rest are left as they are.";

/** A fully configured node: every setting set, at digest d1. */
const D1: NodeOptions = {
  "startall-onboot-delay": 30,
  "ballooning-target": 60,
  wakeonlan: MAC,
  location: "latitude=12.5,longitude=-45.25,name=Site A",
  digest: "d1",
};

function read(over: NodeOptions = {}): NodeOptions {
  return { ...D1, ...over };
}

type UserEvent = ReturnType<typeof userEvent.setup>;

function conflict(): ApiClientError {
  return new ApiClientError(409, { error: "conflict", message: STALE });
}

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
 * Serves the options read: one `read` per GET, the last repeating, so a test
 * that cares only about the first passes one.
 */
function serve(...reads: NodeOptions[]) {
  let call = 0;
  mockedGet.mockImplementation((path: string) => {
    if (path !== OPTIONS_URL) {
      return Promise.reject(new Error(`unexpected GET ${path}`));
    }
    const next = reads[Math.min(call, reads.length - 1)];
    call += 1;
    return Promise.resolve(next);
  });
}

function gets(): number {
  return mockedGet.mock.calls.filter(([path]) => path === OPTIONS_URL).length;
}

/** The body of the nth PUT. */
function putBody(n = 0): unknown {
  return mockedPut.mock.calls[n]?.[1];
}

interface CardProps {
  pveVersion: string;
  online: boolean;
  canEdit: boolean;
}

/** Renders the card on `qc` and hands back what is needed to drive it. */
function renderCard(
  over: Partial<CardProps> = {},
  qc: QueryClient = createAppQueryClient(),
) {
  const props: CardProps = {
    pveVersion: CURRENT,
    online: true,
    canEdit: true,
    ...over,
  };
  const ui = (p: CardProps): ReactNode => (
    <QueryClientProvider client={qc}>
      <NodeOptionsCard clusterId={CLUSTER} nodeName={NODE} {...p} />
    </QueryClientProvider>
  );
  const view = render(ui(props));
  return {
    qc,
    rerender: (next: Partial<CardProps>) => {
      view.rerender(ui({ ...props, ...next }));
    },
  };
}

function editButton() {
  return screen.findByRole("button", { name: "Edit node options" });
}

async function openDialog(user: UserEvent): Promise<HTMLElement> {
  await user.click(await editButton());
  return screen.findByRole("dialog", { name: DIALOG });
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

/** What an input is described by: the text of the elements its aria-describedby names. */
function describedBy(input: HTMLElement): string {
  return (input.getAttribute("aria-describedby") ?? "")
    .split(" ")
    .filter((id) => id !== "")
    .map((id) => document.getElementById(id)?.textContent ?? "")
    .join(" ");
}

/** The one element whose whole text is `text`: for a line with markup inside it. */
function lineOf(dialog: HTMLElement, text: string): HTMLElement {
  return within(dialog).getByText((_, el) => el?.textContent === text);
}

function saveButton(dialog: HTMLElement): HTMLElement {
  return within(dialog).getByRole("button", { name: /^Save/ });
}

async function save(user: UserEvent, dialog: HTMLElement) {
  await user.click(saveButton(dialog));
}

/** Escape, Cancel and Close each end the dialog; so does the page leaving. */
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
  [
    "the page being left",
    () => {
      cleanup();
      return Promise.resolve();
    },
  ],
];

beforeEach(() => {
  mockedGet.mockReset();
  mockedPut.mockReset();
  mockedToastError.mockReset();
  mockedPut.mockResolvedValue({ status: "ok" });
});

describe("what the card shows", () => {
  it("lists every setting that is set", async () => {
    serve(read());
    renderCard();

    expect(await screen.findByText("30 seconds")).toBeInTheDocument();
    expect(screen.getByText("60%")).toBeInTheDocument();
    expect(screen.getByText(MAC)).toBeInTheDocument();
    expect(screen.getByText("Site A (12.5, -45.25)")).toBeInTheDocument();
    expect(
      screen.getByText("Start on boot delay", { selector: "dt" }),
    ).toBeInTheDocument();
  });

  it("says what an unset setting means", async () => {
    serve({ digest: "d1" });
    renderCard();

    expect(await screen.findByText("Default (no delay)")).toBeInTheDocument();
    expect(screen.getByText("Default (80%)")).toBeInTheDocument();
    expect(screen.getByText("Not configured")).toBeInTheDocument();
    expect(screen.getByText("From datacenter")).toBeInTheDocument();
  });

  it("shows a Wake-on-LAN setting with its interface and broadcast address", async () => {
    serve(
      read({
        wakeonlan: `mac=${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
      }),
    );
    renderCard();

    expect(
      await screen.findByText(`${MAC} via vmbr0, broadcast 192.0.2.255`),
    ).toBeInTheDocument();
  });

  it("shows a value it cannot read as it is stored", async () => {
    const stored = `${MAC},${MAC_2}`;
    serve(read({ wakeonlan: stored, location: "Site A" }));
    renderCard();

    // The full value is also the tooltip of a row that has to cut it short.
    const wol = await screen.findByText(stored);
    expect(wol).toHaveAttribute("title", stored);
    const place = screen.getByText("Site A");
    expect(place).toHaveAttribute("title", "Site A");
  });

  it("keeps the stored wakeonlan value in the row's tooltip", async () => {
    const stored = `mac=${MAC},bind-interface=vmbr0`;
    serve(read({ wakeonlan: stored }));
    renderCard();

    expect(await screen.findByText(`${MAC} via vmbr0`)).toHaveAttribute(
      "title",
      stored,
    );
  });
});

describe("when Edit is offered", () => {
  it("is not offered while the options are still being read", async () => {
    const held = deferred<NodeOptions>();
    mockedGet.mockReturnValueOnce(held.promise);
    renderCard();

    await flush();
    expect(
      screen.queryByRole("button", { name: "Edit node options" }),
    ).toBeNull();

    held.resolve(read());
    expect(await editButton()).toBeInTheDocument();
  });

  it("is not offered for a read that failed, and the notice offers a retry", async () => {
    const user = userEvent.setup();
    // The first read fails; the retry gets the node's.
    mockedGet.mockRejectedValueOnce(badGateway());
    mockedGet.mockResolvedValue(read());
    renderCard();

    expect(
      await screen.findByText("Could not load this node's options."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Failed to connect to Proxmox"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Edit node options" }),
    ).toBeNull();

    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await editButton()).toBeInTheDocument();
    expect(screen.getByText("30 seconds")).toBeInTheDocument();
  });

  it("is not offered to someone who cannot manage nodes", async () => {
    serve(read());
    renderCard({ canEdit: false });

    expect(await screen.findByText("30 seconds")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Edit node options" }),
    ).toBeNull();
  });

  it("does not read an offline node, and says why", async () => {
    serve(read());
    renderCard({ online: false });

    expect(
      await screen.findByText(
        "Options can only be read while the node is online.",
      ),
    ).toBeInTheDocument();
    await flush();
    expect(gets()).toBe(0);
    expect(
      screen.queryByRole("button", { name: "Edit node options" }),
    ).toBeNull();
  });

  it("shows what it last read of a node that went offline, and offers no Edit", async () => {
    serve(read());
    const { rerender } = renderCard();
    expect(await editButton()).toBeInTheDocument();

    rerender({ online: false });

    expect(screen.getByText("30 seconds")).toBeInTheDocument();
    expect(
      screen.getByText("Node is offline — showing the options as last read."),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Edit node options" }),
    ).toBeNull();
  });

  it("is still offered after a background refresh failed and left the data", async () => {
    const user = userEvent.setup();
    serve(read());
    const { qc } = renderCard();
    expect(await editButton()).toBeInTheDocument();

    mockedGet.mockRejectedValueOnce(badGateway());
    await act(async () => {
      await qc.invalidateQueries({ queryKey: OPTIONS_KEY });
    });

    // The failure is noted beside the data it did not take away.
    expect(
      await screen.findByText(/Could not load this node's options: Failed/),
    ).toBeInTheDocument();
    expect(screen.getByText("30 seconds")).toBeInTheDocument();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "45");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ "startall-onboot-delay": 45, digest: "d1" });
  });
});

describe("a save sends only what was changed", () => {
  it("sends the delay alone, with the digest it was read at", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    // The dialog shows what was read.
    expect(field(dialog, DELAY)).toHaveValue("30");
    expect(field(dialog, TARGET)).toHaveValue("60");
    expect(field(dialog, MAC_FIELD)).toHaveValue(MAC);
    expect(field(dialog, LATITUDE)).toHaveValue("12.5");
    expect(field(dialog, LONGITUDE)).toHaveValue("-45.25");
    expect(field(dialog, NAME)).toHaveValue("Site A");

    await setField(user, dialog, DELAY, "31");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ "startall-onboot-delay": 31, digest: "d1" });
    expect(mockedPut.mock.calls[0]?.[0]).toBe(OPTIONS_URL);
    // And the dialog closes once the card has the new read.
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("keeps Save disabled for an untouched dialog, and Enter sends nothing", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    expect(saveButton(dialog)).toBeDisabled();

    await user.click(field(dialog, DELAY));
    await user.keyboard("{Enter}");
    // A submit that does not come through the button is refused just the same.
    const form = dialog.querySelector("form");
    if (form === null) throw new Error("the dialog has no form");
    fireEvent.submit(form);
    await flush();

    expect(mockedPut).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog", { name: DIALOG })).toBeInTheDocument();
  });

  it("sends a change made back to what it was as no change", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "99");
    expect(saveButton(dialog)).toBeEnabled();
    await setField(user, dialog, DELAY, "30");
    expect(saveButton(dialog)).toBeDisabled();
  });

  it("sends 0 as a value", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "0");
    await setField(user, dialog, TARGET, "0");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      "startall-onboot-delay": 0,
      "ballooning-target": 0,
      digest: "d1",
    });
  });

  it("sets what was unset, in the keys that changed only", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, TARGET, "70");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ "ballooning-target": 70, digest: "d1" });
  });
});

describe("clearing a setting", () => {
  it("names the integer in delete and sends no empty value", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, TARGET, "");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ delete: ["ballooning-target"], digest: "d1" });
  });

  it("removes the whole Wake-on-LAN setting when its MAC is cleared", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: `${MAC},bind-interface=vmbr0,foo=bar` }));
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, MAC_FIELD, "");
    // The interface and broadcast address go with it, and say so.
    expect(field(dialog, INTERFACE)).toBeDisabled();
    expect(field(dialog, BROADCAST)).toBeDisabled();
    expect(
      within(dialog).getByText(
        "Clearing the MAC address removes Wake-on-LAN, including the interface and broadcast address.",
      ),
    ).toBeInTheDocument();
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ delete: ["wakeonlan"], digest: "d1" });
  });

  it("removes the location when all three of its fields are cleared", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, LATITUDE, "");
    // Part way through, it is not a location yet.
    expect(saveButton(dialog)).toBeDisabled();
    await setField(user, dialog, LONGITUDE, "");
    await setField(user, dialog, NAME, "");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ delete: ["location"], digest: "d1" });
  });

  it("clears and sets in one save without naming a key twice", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    await setField(user, dialog, TARGET, "");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      "startall-onboot-delay": 31,
      delete: ["ballooning-target"],
      digest: "d1",
    });
  });
});

describe("Wake-on-LAN", () => {
  it("writes a new setting with the MAC bare", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, MAC_FIELD, MAC);
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ wakeonlan: MAC, digest: "d1" });
  });

  it("keeps a bare MAC bare when an interface is added", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: MAC }));
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, INTERFACE, "vmbr0");
    await setField(user, dialog, BROADCAST, "192.0.2.255");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      wakeonlan: `${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
      digest: "d1",
    });
  });

  it("keeps the keyed form when the MAC changes, and the rest in place", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: `bind-interface=vmbr0,mac=${MAC}` }));
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, MAC_FIELD, MAC_2);
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      wakeonlan: `bind-interface=vmbr0,mac=${MAC_2}`,
      digest: "d1",
    });
  });

  it("keeps a subkey it has no field for, and says so", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: `${MAC},foo=bar,bind-interface=vmbr0` }));
    renderCard();

    const dialog = await openDialog(user);
    expect(
      within(dialog).getByText(
        (_, el) =>
          el?.textContent ===
          "Also stored with this setting and kept as is: foo=bar",
      ),
    ).toBeInTheDocument();
    await setField(user, dialog, MAC_FIELD, MAC_2);
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      wakeonlan: `${MAC_2},foo=bar,bind-interface=vmbr0`,
      digest: "d1",
    });
  });

  it("removes only the interface when only the interface is cleared", async () => {
    const user = userEvent.setup();
    serve(
      read({
        wakeonlan: `mac=${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
      }),
    );
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, INTERFACE, "");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      wakeonlan: `mac=${MAC},broadcast-address=192.0.2.255`,
      digest: "d1",
    });
  });

  it.each([
    ["a multicast MAC", MAC_FIELD, "03:00:00:00:00:01", /unicast MAC address/],
    ["a dashed MAC", MAC_FIELD, "02-00-00-00-00-01", /unicast MAC address/],
    ["an interface name that is too short", INTERFACE, "a", /interface name/],
    [
      "a broadcast address out of range",
      BROADCAST,
      "256.0.0.1",
      /IPv4 address/,
    ],
  ])(
    "blocks %s, with the reason under the field",
    async (_, label, value, why) => {
      const user = userEvent.setup();
      serve(read());
      renderCard();

      const dialog = await openDialog(user);
      await setField(user, dialog, label, value);

      const input = field(dialog, label);
      expect(input).toHaveAttribute("aria-invalid", "true");
      const described = input.getAttribute("aria-describedby") ?? "";
      expect(
        described
          .split(" ")
          .map((id) => document.getElementById(id)?.textContent ?? "")
          .join(" "),
      ).toMatch(why);
      expect(saveButton(dialog)).toBeDisabled();
    },
  );

  // Proxmox reads the MAC from the node being woken and the interface and the
  // broadcast address from the node that sends the packet (the wakeonlan method,
  // PVE/API2/Nodes.pm), so the three are not one thing and the text says which
  // is which.
  it("says what the MAC is for and what the interface and broadcast address are for", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);

    expect(describedBy(field(dialog, MAC_FIELD))).toBe(
      "The MAC address this node is woken by.",
    );
    const sending =
      "The interface and broadcast address are what this node uses when it sends a wake packet to another node.";
    expect(describedBy(field(dialog, INTERFACE))).toBe(sending);
    expect(describedBy(field(dialog, BROADCAST))).toBe(sending);
    expect(dialog).not.toHaveTextContent("Other nodes wake this one");
  });

  const NEEDS_MAC =
    "Enter this node's MAC address first — Proxmox requires it.";

  it("always says why the interface and the broadcast address are off while there is no MAC", async () => {
    const user = userEvent.setup();
    // Nothing configured: there is no setting to clear, which is where the
    // other explanation of this would not appear.
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);

    for (const label of [INTERFACE, BROADCAST]) {
      expect(field(dialog, label)).toBeDisabled();
      expect(describedBy(field(dialog, label))).toContain(NEEDS_MAC);
    }

    await setField(user, dialog, MAC_FIELD, MAC);

    expect(within(dialog).queryByText(NEEDS_MAC)).toBeNull();
    for (const label of [INTERFACE, BROADCAST]) {
      expect(field(dialog, label)).toBeEnabled();
      expect(describedBy(field(dialog, label))).not.toContain(NEEDS_MAC);
    }
  });

  it("says it too when a stored MAC is cleared, beside what clearing does", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: `${MAC},bind-interface=vmbr0` }));
    renderCard();

    const dialog = await openDialog(user);
    expect(within(dialog).queryByText(NEEDS_MAC)).toBeNull();
    await setField(user, dialog, MAC_FIELD, "");

    expect(within(dialog).getByText(NEEDS_MAC)).toBeInTheDocument();
    expect(
      within(dialog).getByText(
        "Clearing the MAC address removes Wake-on-LAN, including the interface and broadcast address.",
      ),
    ).toBeInTheDocument();
  });

  it("checks the interface and the broadcast address only once a MAC is entered", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: `${MAC},bind-interface=vmbr0` }));
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, INTERFACE, "a");
    await setField(user, dialog, BROADCAST, "256.0.0.1");
    expect(field(dialog, INTERFACE)).toHaveAttribute("aria-invalid", "true");
    expect(field(dialog, BROADCAST)).toHaveAttribute("aria-invalid", "true");
    expect(saveButton(dialog)).toBeDisabled();

    // With no MAC the whole setting goes, and what is in the two fields that
    // are switched off goes with it: it is not checked, and not in the way.
    await setField(user, dialog, MAC_FIELD, "");

    expect(field(dialog, INTERFACE)).not.toHaveAttribute("aria-invalid");
    expect(field(dialog, BROADCAST)).not.toHaveAttribute("aria-invalid");
    expect(saveButton(dialog)).toBeEnabled();
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ delete: ["wakeonlan"], digest: "d1" });
  });

  it("says a subkey it has no field for is kept, until the whole setting is removed", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: `${MAC},foo=bar` }));
    renderCard();

    const dialog = await openDialog(user);
    const kept = "Also stored with this setting and kept as is: foo=bar";
    const removed =
      "Also stored with this setting and removed along with it: foo=bar";
    expect(lineOf(dialog, kept)).toBeInTheDocument();

    await setField(user, dialog, MAC_FIELD, "");
    expect(
      within(dialog).queryByText((_, el) => el?.textContent === kept),
    ).toBeNull();
    expect(lineOf(dialog, removed)).toBeInTheDocument();

    await setField(user, dialog, MAC_FIELD, MAC_2);
    expect(lineOf(dialog, kept)).toBeInTheDocument();
    expect(
      within(dialog).queryByText((_, el) => el?.textContent === removed),
    ).toBeNull();
  });

  it("puts no native limit on a field, so the check is Proxmox's own", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    const inputs = within(dialog).getAllByRole("textbox");
    // Delay, target, MAC, interface, broadcast, latitude, longitude, name.
    expect(inputs).toHaveLength(8);
    for (const input of inputs) {
      expect(input).toHaveAttribute("type", "text");
      for (const attribute of ["min", "max", "maxlength", "pattern"]) {
        expect(input).not.toHaveAttribute(attribute);
      }
    }
  });
});

describe("location", () => {
  it("writes a new value in the order latitude, longitude, name", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, NAME, "Site A");
    await setField(user, dialog, LONGITUDE, "-45.25");
    await setField(user, dialog, LATITUDE, "12.5");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      location: "latitude=12.5,longitude=-45.25,name=Site A",
      digest: "d1",
    });
  });

  it("writes a location without a name", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, LATITUDE, "0");
    await setField(user, dialog, LONGITUDE, "0");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      location: "latitude=0,longitude=0",
      digest: "d1",
    });
  });

  it("changes one field where it stands", async () => {
    const user = userEvent.setup();
    serve(read({ location: "name=Site A,longitude=-45.25,latitude=12.5" }));
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, LATITUDE, "13");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      location: "name=Site A,longitude=-45.25,latitude=13",
      digest: "d1",
    });
  });

  it("blocks a location with only one coordinate", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, LATITUDE, "12.5");

    expect(
      within(dialog).getByText(
        "A location needs both coordinates: enter a longitude.",
      ),
    ).toBeInTheDocument();
    expect(field(dialog, LONGITUDE)).toHaveAttribute("aria-invalid", "true");
    expect(saveButton(dialog)).toBeDisabled();
  });

  it("blocks a name with no coordinates", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, NAME, "Site A");

    expect(
      within(dialog).getByText(
        "A location needs both coordinates: enter a latitude.",
      ),
    ).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
  });

  it("blocks a comma in the name, which a property string cannot carry", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, NAME, "Site A, rack01");

    expect(
      within(dialog).getByText("The name cannot contain a comma."),
    ).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
  });

  it.each([
    ["a latitude past 90", LATITUDE, "91"],
    ["a longitude past 180", LONGITUDE, "180.5"],
    ["a latitude that is not a number", LATITUDE, "north"],
    ["a name of 129 characters", NAME, "a".repeat(129)],
  ])("blocks %s", async (_, label, value) => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    // Typing 129 characters one by one is the slow part; paste them.
    const input = field(dialog, label);
    await user.clear(input);
    await user.click(input);
    await user.paste(value);

    expect(input).toHaveAttribute("aria-invalid", "true");
    expect(saveButton(dialog)).toBeDisabled();
  });

  it("ties what it says about the datacenter to the three fields it is about", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);

    for (const label of [LATITUDE, LONGITUDE, NAME]) {
      expect(describedBy(field(dialog, label))).toContain(
        "Leave empty to use the datacenter's location.",
      );
    }
    // And the reason a field is refused is read beside it.
    await setField(user, dialog, LATITUDE, "north");
    expect(describedBy(field(dialog, LATITUDE))).toContain(
      "Leave empty to use the datacenter's location.",
    );
    expect(describedBy(field(dialog, LATITUDE))).toContain(
      "Enter the latitude as a number from -90 to 90.",
    );
  });

  it("keeps a key it has no field for, and says so", async () => {
    const user = userEvent.setup();
    serve(read({ location: "latitude=12.5,longitude=-45.25,foo=bar" }));
    renderCard();

    const dialog = await openDialog(user);
    expect(
      lineOf(dialog, "Also stored with this setting and kept as is: foo=bar"),
    ).toBeInTheDocument();
    // Only the location's: the Wake-on-LAN setting has no such key.
    expect(
      within(dialog).getAllByText(/Also stored with this setting/),
    ).toHaveLength(1);

    await setField(user, dialog, LATITUDE, "13");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      location: "latitude=13,longitude=-45.25,foo=bar",
      digest: "d1",
    });
  });

  it("says that same key goes when all three fields are cleared and the location with them", async () => {
    const user = userEvent.setup();
    serve(read({ location: "latitude=12.5,longitude=-45.25,foo=bar" }));
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, LATITUDE, "");
    await setField(user, dialog, LONGITUDE, "");
    await setField(user, dialog, NAME, "");

    expect(
      lineOf(
        dialog,
        "Also stored with this setting and removed along with it: foo=bar",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByText(
        (_, el) => el?.textContent.includes("kept as is") === true,
      ),
    ).toBeNull();
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ delete: ["location"], digest: "d1" });
  });

  it("reads a name of only spaces as no name, and leaves it as it is", async () => {
    const user = userEvent.setup();
    // Proxmox never trims a property string, so "name= " is a one-space name
    // to it, and a location it keeps: it must be neither refused nor rewritten.
    serve(read({ location: "latitude=1,name= ,longitude=2" }));
    renderCard();

    const dialog = await openDialog(user);
    expect(field(dialog, LATITUDE)).toHaveValue("1");
    expect(field(dialog, LONGITUDE)).toHaveValue("2");
    expect(field(dialog, NAME)).toHaveValue("");
    expect(within(dialog).queryByText(/cannot read this value/)).toBeNull();

    await setField(user, dialog, LONGITUDE, "3");
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      location: "latitude=1,name= ,longitude=3",
      digest: "d1",
    });
  });
});

describe("a value Nexara cannot read", () => {
  const TWO_MACS = `${MAC},${MAC_2}`;

  it("is shown as stored and sends nothing while it is left alone", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: TWO_MACS, location: "Site A" }));
    renderCard();

    const dialog = await openDialog(user);
    expect(within(dialog).getByText(TWO_MACS)).toBeInTheDocument();
    expect(within(dialog).getByText("Site A")).toBeInTheDocument();
    // There is nothing to type into: it can only be removed.
    expect(within(dialog).queryByLabelText(MAC_FIELD)).not.toBeInTheDocument();
    expect(within(dialog).queryByLabelText(LATITUDE)).not.toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();

    // Changing another setting does not touch it.
    await setField(user, dialog, DELAY, "31");
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ "startall-onboot-delay": 31, digest: "d1" });
  });

  it("is removed when Remove is ticked, through delete", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: TWO_MACS, location: "Site A" }));
    renderCard();

    const dialog = await openDialog(user);
    await user.click(
      within(dialog).getByRole("checkbox", {
        name: "Remove this Wake-on-LAN setting",
      }),
    );
    await user.click(
      within(dialog).getByRole("checkbox", { name: "Remove this location" }),
    );
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      delete: ["wakeonlan", "location"],
      digest: "d1",
    });
  });

  it("is kept after Remove is ticked and unticked", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: TWO_MACS }));
    renderCard();

    const dialog = await openDialog(user);
    const remove = within(dialog).getByRole("checkbox", {
      name: "Remove this Wake-on-LAN setting",
    });
    await user.click(remove);
    expect(saveButton(dialog)).toBeEnabled();
    await user.click(remove);
    expect(saveButton(dialog)).toBeDisabled();
  });
});

describe("a node's version decides which fields there are", () => {
  it.each([
    // The version before each field's first release hides it.
    ["8.3.5", false, true, false],
    ["8.1.8", false, false, false],
    ["9.1.12", true, true, false],
    ["9.1.13", true, true, true],
    // Unknown: nothing gated.
    ["", false, false, false],
  ])(
    "offers, on %j: ballooning %s, interface and broadcast %s, location %s",
    async (version, ballooning, bindBroadcast, location) => {
      const user = userEvent.setup();
      // A read that holds none of the gated settings.
      serve({ "startall-onboot-delay": 30, wakeonlan: MAC, digest: "d1" });
      renderCard({
        pveVersion: version === "" ? "" : `pve-manager/${version}/0123abcd`,
      });

      const dialog = await openDialog(user);
      const has = (label: string) =>
        within(dialog).queryByLabelText(label) !== null;
      expect(has(TARGET)).toBe(ballooning);
      expect(has(INTERFACE)).toBe(bindBroadcast);
      expect(has(BROADCAST)).toBe(bindBroadcast);
      expect(has(LATITUDE)).toBe(location);
      expect(has(LONGITUDE)).toBe(location);
      expect(has(NAME)).toBe(location);

      // What it hides it never sends: the one field changed is the one sent.
      await setField(user, dialog, DELAY, "31");
      await save(user, dialog);
      await waitFor(() => {
        expect(mockedPut).toHaveBeenCalledTimes(1);
      });
      expect(putBody()).toEqual({
        "startall-onboot-delay": 31,
        digest: "d1",
      });
    },
  );

  it("does not list a row for a setting the node's version lacks", async () => {
    serve({ digest: "d1" });
    renderCard({ pveVersion: "pve-manager/8.1.8/0123abcd" });

    await screen.findByText("Default (no delay)");
    expect(screen.queryByText("RAM ballooning target")).toBeNull();
    expect(screen.queryByText("Location")).toBeNull();
    expect(screen.getByText("Wake-on-LAN")).toBeInTheDocument();
  });

  it.each([["pve-manager/7.4.1/0123abcd"], [""]])(
    "offers a field the read already holds, on version %j",
    async (version) => {
      const user = userEvent.setup();
      serve(
        read({
          wakeonlan: `${MAC},bind-interface=vmbr0`,
          location: "latitude=1,longitude=2",
        }),
      );
      renderCard({ pveVersion: version });

      expect(await screen.findByText("60%")).toBeInTheDocument();
      expect(screen.getByText("1, 2")).toBeInTheDocument();
      const dialog = await openDialog(user);
      expect(field(dialog, TARGET)).toHaveValue("60");
      expect(field(dialog, INTERFACE)).toHaveValue("vmbr0");
      expect(field(dialog, LATITUDE)).toHaveValue("1");

      await setField(user, dialog, TARGET, "70");
      await save(user, dialog);
      await waitFor(() => {
        expect(mockedPut).toHaveBeenCalledTimes(1);
      });
      expect(putBody()).toEqual({ "ballooning-target": 70, digest: "d1" });
    },
  );
});

describe("a field that is not a whole number", () => {
  it.each([
    [DELAY, "301", "Enter a whole number from 0 to 300."],
    [DELAY, "1.5", "Enter a whole number from 0 to 300."],
    [DELAY, "-1", "Enter a whole number from 0 to 300."],
    [TARGET, "101", "Enter a whole number from 0 to 100."],
    [TARGET, "1e2", "Enter a whole number from 0 to 100."],
  ])("blocks %s = %j", async (label, value, message) => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, label, value);

    expect(within(dialog).getByText(message)).toBeInTheDocument();
    expect(field(dialog, label)).toHaveAttribute("aria-invalid", "true");
    expect(saveButton(dialog)).toBeDisabled();
  });

  it("accepts the ends of the range", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "300");
    await setField(user, dialog, TARGET, "100");
    expect(saveButton(dialog)).toBeEnabled();
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({
      "startall-onboot-delay": 300,
      "ballooning-target": 100,
      digest: "d1",
    });
  });
});

describe("the digest a save carries", () => {
  it("does not read the node again when the dialog opens", async () => {
    const user = userEvent.setup();
    serve(read());
    const { qc } = renderCard();
    await editButton();
    await flush();
    // Stale, without being read again: an observer that mounted on stale data
    // would fetch it, so a dialog that read the node for itself shows. On
    // fresh data (the five minutes the app keeps it) it would not, and this
    // would pass with such a dialog in place.
    await act(async () => {
      await qc.invalidateQueries({
        queryKey: OPTIONS_KEY,
        refetchType: "none",
      });
    });

    const before = gets();
    await openDialog(user);
    await flush();

    expect(gets()).toBe(before);
  });

  it("stays that of the read the dialog opened with while the card refreshes behind it", async () => {
    const user = userEvent.setup();
    serve(read(), read({ "startall-onboot-delay": 45, digest: "d2" }));
    const { qc } = renderCard();

    const dialog = await openDialog(user);
    await act(async () => {
      await qc.invalidateQueries({ queryKey: OPTIONS_KEY });
    });

    // The card has moved on to d2 ...
    await waitFor(() => {
      expect(screen.getByText("45 seconds")).toBeInTheDocument();
    });
    expect(qc.getQueryData<NodeOptions>(OPTIONS_KEY)?.digest).toBe("d2");
    // ... and the dialog has not: it keeps the values it was drawn from.
    expect(field(dialog, DELAY)).toHaveValue("30");

    await setField(user, dialog, TARGET, "70");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    // And only what was changed here: not the delay the refresh moved.
    expect(putBody()).toEqual({ "ballooning-target": 70, digest: "d1" });
  });

  it("is read under the node's own prefix, which an ACME save invalidates", async () => {
    serve(read());
    const { qc } = renderCard();
    await editButton();
    expect(qc.getQueryData(OPTIONS_KEY)).toEqual(read());
    expect(gets()).toBe(1);

    // useSetNodeACMEConfig invalidates ["clusters", id, "nodes", node] after a
    // save: the file's digest moved, and this read holds it.
    await act(async () => {
      await qc.invalidateQueries({
        queryKey: ["clusters", CLUSTER, "nodes", NODE],
      });
    });

    expect(gets()).toBe(2);
  });

  it("is left out of the request when the read had none", async () => {
    const user = userEvent.setup();
    // A node with no config file yet: no digest to check against.
    serve({});
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    expect(putBody()).toEqual({ "startall-onboot-delay": 31 });
    expect(putBody()).not.toHaveProperty("digest");
  });

  it("refreshes the card, and the ACME read that shares the file, after a save", async () => {
    const user = userEvent.setup();
    serve(read(), read({ "startall-onboot-delay": 31, digest: "d2" }));
    const { qc } = renderCard();
    qc.setQueryData(ACME_KEY, { digest: "d1" });
    expect(qc.getQueryState(ACME_KEY)?.isInvalidated).toBe(false);

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    await save(user, dialog);

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    // The dialog closed on the new read, not the old one.
    expect(screen.getByText("31 seconds")).toBeInTheDocument();
    expect(qc.getQueryState(ACME_KEY)?.isInvalidated).toBe(true);
  });

  it("keeps the dialog open until the card has what was saved", async () => {
    const user = userEvent.setup();
    const reread = deferred<NodeOptions>();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    // The read the save triggers, held: the PUT is done and the card is not.
    mockedGet.mockImplementationOnce(() => reread.promise);
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    await flush();

    expect(screen.getByRole("dialog", { name: DIALOG })).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Saving..." }),
    ).toBeDisabled();
    expect(screen.queryByText("31 seconds")).toBeNull();

    reread.resolve(read({ "startall-onboot-delay": 31, digest: "d2" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(screen.getByText("31 seconds")).toBeInTheDocument();
  });

  it("is still a saved write when the read that follows it fails", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    mockedGet.mockRejectedValueOnce(badGateway());
    await save(user, dialog);

    // Closed, with no failure shown or toasted: the node did take the write.
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    await flush();
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  it("sends one request for two submits in the same moment", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    mockedPut.mockReset();
    mockedPut.mockReturnValueOnce(held.promise);
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    // Both land before React has drawn the first one's "Saving...".
    const button = saveButton(dialog);
    act(() => {
      fireEvent.click(button);
      fireEvent.click(button);
    });
    held.resolve({ status: "ok" });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    expect(mockedPut).toHaveBeenCalledTimes(1);
  });
});

describe("a save the node refuses as stale", () => {
  it("says so, holds Save while it reads the node again, then saves the same changes against the new digest", async () => {
    const user = userEvent.setup();
    const reread = deferred<NodeOptions>();
    serve(read());
    mockedPut.mockRejectedValueOnce(conflict());
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    await setField(user, dialog, TARGET, "");
    // The re-read the 409 triggers is held, to see the dialog while it waits.
    mockedGet.mockImplementationOnce(() => reread.promise);
    await save(user, dialog);

    // The dialog's own words, not the server's "reload and try again", and the
    // note that the node is being read again.
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(CHANGED);
    expect(dialog).not.toHaveTextContent("reload and try again");
    expect(
      within(dialog).getByText("Reading this node's current configuration…"),
    ).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
    expect(mockedPut).toHaveBeenCalledTimes(1);
    expect(putBody(0)).toEqual({
      "startall-onboot-delay": 31,
      delete: ["ballooning-target"],
      digest: "d1",
    });

    // The dialog stays open with what was typed.
    expect(field(dialog, DELAY)).toHaveValue("31");
    expect(field(dialog, TARGET)).toHaveValue("");

    // The re-read comes back with another operator's change and a new digest.
    reread.resolve(read({ "startall-onboot-delay": 45, digest: "d2" }));
    // What changed is named, since the card that shows it is behind the overlay.
    expect(
      await within(dialog).findByText(
        "Changed since you opened this: Start on boot delay.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(SAVING_AGAIN)).toBeInTheDocument();
    expect(saveButton(dialog)).toBeEnabled();
    // The form did not take it in; the card behind it did.
    expect(field(dialog, DELAY)).toHaveValue("31");
    expect(screen.getByText("45 seconds")).toBeInTheDocument();
    // No retry on the operator's behalf.
    expect(mockedPut).toHaveBeenCalledTimes(1);

    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(putBody(1)).toEqual({
      "startall-onboot-delay": 31,
      delete: ["ballooning-target"],
      digest: "d2",
    });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("keeps the pin when the re-read fails, and says the retry will be refused again", async () => {
    const user = userEvent.setup();
    serve(read());
    mockedPut
      .mockRejectedValueOnce(conflict())
      .mockRejectedValueOnce(conflict());
    const { qc } = renderCard();

    const dialog = await openDialog(user);
    // The cache moves to d2 behind the dialog, so that a refetch that FAILS
    // still holds d2 as retained data — which is no reason to pin to it.
    act(() => {
      qc.setQueryData<NodeOptions>(OPTIONS_KEY, read({ digest: "d2" }));
    });
    await setField(user, dialog, DELAY, "31");
    mockedGet.mockRejectedValueOnce(badGateway());
    await save(user, dialog);

    expect(
      await within(dialog).findByText(
        "Nexara could not re-read this node's configuration, so saving again will be refused again until it can.",
      ),
    ).toBeInTheDocument();
    // Nothing is said to have changed of a read that did not succeed, though the
    // cache holds d2's values.
    expect(
      within(dialog).queryByText(/Changed since you opened this/),
    ).toBeNull();
    expect(saveButton(dialog)).toBeEnabled();

    mockedGet.mockRejectedValueOnce(badGateway());
    await save(user, dialog);

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    // Still the digest the form was drawn from, not d2.
    expect(putBody(0)).toEqual({ "startall-onboot-delay": 31, digest: "d1" });
    expect(putBody(1)).toEqual({ "startall-onboot-delay": 31, digest: "d1" });
  });

  it("does not let a dismissed dialog's re-read re-pin the one opened after it", async () => {
    const user = userEvent.setup();
    const reread = deferred<NodeOptions>();
    serve(read());
    mockedPut
      .mockRejectedValueOnce(conflict())
      .mockResolvedValueOnce({ status: "ok" });
    renderCard();

    const first = await openDialog(user);
    await setField(user, first, DELAY, "31");
    mockedGet.mockImplementationOnce(() => reread.promise);
    await save(user, first);
    await within(first).findByRole("alert");
    expect(
      within(first).getByText("Reading this node's current configuration…"),
    ).toBeInTheDocument();

    // Dismissed with the re-read still out; another dialog opened in its place.
    await user.click(within(first).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    const second = await openDialog(user);
    expect(field(second, DELAY)).toHaveValue("30");

    reread.resolve(read({ digest: "d3" }));
    await flush();

    // The second dialog is untouched by it: no alert, no note, and its own
    // digest.
    expect(within(second).queryByRole("alert")).toBeNull();
    expect(within(second).queryByRole("status")).toBeNull();
    expect(second).not.toHaveTextContent(/Changed since you opened this/);
    await setField(user, second, TARGET, "70");
    await save(user, second);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(putBody(1)).toEqual({ "ballooning-target": 70, digest: "d1" });
  });

  /** Opens the dialog on `first`, edits the delay, saves, and lets the re-read return `then`. */
  async function refusedThenRead(
    user: UserEvent,
    first: NodeOptions,
    then: NodeOptions,
    pveVersion = CURRENT,
  ) {
    serve(first);
    mockedPut.mockRejectedValueOnce(conflict());
    renderCard({ pveVersion });
    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    mockedGet.mockResolvedValueOnce(then);
    await save(user, dialog);
    return dialog;
  }

  it("names every setting that changed while it was open, in the card's order", async () => {
    const user = userEvent.setup();
    const dialog = await refusedThenRead(
      user,
      read(),
      read({
        "startall-onboot-delay": 45,
        "ballooning-target": 70,
        wakeonlan: MAC_2,
        location: "latitude=1,longitude=2",
        digest: "d2",
      }),
    );

    expect(
      await within(dialog).findByText(
        "Changed since you opened this: Start on boot delay, RAM ballooning target, Wake-on-LAN, Location.",
      ),
    ).toBeInTheDocument();
  });

  it("names a setting that was set or removed meanwhile", async () => {
    const user = userEvent.setup();
    const dialog = await refusedThenRead(user, read({ wakeonlan: MAC }), {
      "startall-onboot-delay": 30,
      "ballooning-target": 60,
      digest: "d2",
    });

    expect(
      await within(dialog).findByText(
        "Changed since you opened this: Wake-on-LAN, Location.",
      ),
    ).toBeInTheDocument();
  });

  it("says so when none of the settings it shows changed, because the file changed elsewhere", async () => {
    const user = userEvent.setup();
    // The digest covers the whole file, ACME keys and notes included.
    const dialog = await refusedThenRead(user, read(), read({ digest: "d2" }));

    expect(
      await within(dialog).findByText(
        "None of the settings shown here changed; something else in the node's configuration did.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(SAVING_AGAIN)).toBeInTheDocument();
    expect(dialog).not.toHaveTextContent(/Changed since you opened this/);
  });

  it("does not name a setting it does not show", async () => {
    const user = userEvent.setup();
    // 8.1.8 has neither the ballooning target nor the location, and the node
    // answers with both now (set in Proxmox since): the dialog shows neither,
    // so saving here overwrites neither.
    const dialog = await refusedThenRead(
      user,
      { "startall-onboot-delay": 30, wakeonlan: MAC, digest: "d1" },
      {
        "startall-onboot-delay": 45,
        wakeonlan: MAC,
        "ballooning-target": 70,
        location: "latitude=1,longitude=2",
        digest: "d2",
      },
      "pve-manager/8.1.8/0123abcd",
    );

    expect(
      await within(dialog).findByText(
        "Changed since you opened this: Start on boot delay.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(TARGET)).toBeNull();
    expect(within(dialog).queryByLabelText(LATITUDE)).toBeNull();
  });

  it("replaces the list with that of a newer conflict", async () => {
    const user = userEvent.setup();
    serve(read());
    mockedPut
      .mockRejectedValueOnce(conflict())
      .mockRejectedValueOnce(conflict());
    renderCard();
    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    mockedGet.mockResolvedValueOnce(
      read({ "startall-onboot-delay": 45, digest: "d2" }),
    );
    await save(user, dialog);
    await within(dialog).findByText(
      "Changed since you opened this: Start on boot delay.",
    );

    // Saved again, and refused again: by then the wake-up MAC changed as well.
    mockedGet.mockResolvedValueOnce(
      read({
        "startall-onboot-delay": 45,
        wakeonlan: MAC_2,
        digest: "d3",
      }),
    );
    await save(user, dialog);

    expect(
      await within(dialog).findByText(
        "Changed since you opened this: Start on boot delay, Wake-on-LAN.",
      ),
    ).toBeInTheDocument();
    // Pinned to the newest of them.
    mockedPut.mockResolvedValueOnce({ status: "ok" });
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(3);
    });
    expect(putBody(2)).toEqual({ "startall-onboot-delay": 31, digest: "d3" });
  });

  it("says what a re-read found only while that re-read is the latest word, not after one that failed", async () => {
    const user = userEvent.setup();
    serve(read());
    mockedPut
      .mockRejectedValueOnce(conflict())
      .mockRejectedValueOnce(conflict());
    renderCard();
    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    mockedGet.mockResolvedValueOnce(
      read({ "startall-onboot-delay": 45, digest: "d2" }),
    );
    await save(user, dialog);
    expect(
      await within(dialog).findByText(
        "Changed since you opened this: Start on boot delay.",
      ),
    ).toBeInTheDocument();
    expect(within(dialog).getByText(SAVING_AGAIN)).toBeInTheDocument();

    // Saved again and refused again, and this time the node cannot be read. The
    // first read said nothing of THIS conflict, and its advice — that saving
    // writes over the node's current values — is not true of a pin that did not
    // move: the one note left is that it could not be read.
    mockedGet.mockRejectedValueOnce(badGateway());
    await save(user, dialog);

    expect(
      await within(dialog).findByText(
        "Nexara could not re-read this node's configuration, so saving again will be refused again until it can.",
      ),
    ).toBeInTheDocument();
    expect(
      within(dialog).queryByText(/Changed since you opened this/),
    ).toBeNull();
    expect(within(dialog).queryByText(SAVING_AGAIN)).toBeNull();
    expect(dialog).not.toHaveTextContent(/Changed since you opened this/);
  });
});

// The app's QueryClient toasts, through the MutationCache in
// lib/query-client.ts, every failed mutation whose HOOK has no onError of its
// own. While the dialog is open it shows the failure itself, so
// useSetNodeOptions opts out (errorsHandledLocally), and once the dialog is
// gone useNodeOptionsSave toasts it (below).
//
// These run on the app's own kind of client, and start from the control its
// docs ask for (test/app-query-client.ts).
describe("a save that fails is reported once, not also as a toast", () => {
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

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(PROBE);
  });

  it("shows a 400 in the dialog, which stays open with its values, and toasts nothing", async () => {
    const user = userEvent.setup();
    serve(read());
    mockedPut.mockRejectedValueOnce(
      new ApiClientError(400, {
        error: "bad_request",
        message: "Parameter verification failed",
      }),
    );
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    await save(user, dialog);

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Parameter verification failed",
    );
    expect(screen.getByRole("dialog", { name: DIALOG })).toBeInTheDocument();
    expect(field(dialog, DELAY)).toHaveValue("31");
    // Not a conflict: no re-read, and no note.
    expect(within(dialog).queryByRole("status")).toBeNull();
    expect(gets()).toBe(1);
    expect(saveButton(dialog)).toBeEnabled();
    await flush();
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("takes the message of a failed save down as soon as it saves again", async () => {
    const user = userEvent.setup();
    const held = deferred<unknown>();
    serve(read());
    mockedPut
      .mockRejectedValueOnce(
        new ApiClientError(400, {
          error: "bad_request",
          message: "Parameter verification failed",
        }),
      )
      .mockReturnValueOnce(held.promise);
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    await save(user, dialog);
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Parameter verification failed",
    );

    await save(user, dialog);
    await within(dialog).findByRole("button", { name: "Saving..." });

    // Not left up beside a save that is out: it is not the answer to that one.
    expect(within(dialog).queryByRole("alert")).toBeNull();
    held.resolve({ status: "ok" });
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("falls back to a connection message for a request that got no answer", async () => {
    const user = userEvent.setup();
    serve(read());
    // How a dropped connection rejects fetch: a TypeError, which describeError
    // gives no words, so without a floor this failure would be silent.
    mockedPut.mockRejectedValueOnce(new TypeError("Failed to fetch"));
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    await save(user, dialog);

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "The save request failed — check your connection and try again.",
    );
    expect(mockedToastError).not.toHaveBeenCalled();
  });

  it("shows another operator's 403 in the dialog without a re-read", async () => {
    const user = userEvent.setup();
    serve(read());
    mockedPut.mockRejectedValueOnce(
      new ApiClientError(403, {
        error: "forbidden",
        message: "Proxmox API permission denied",
      }),
    );
    renderCard();

    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    await save(user, dialog);

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "Proxmox API permission denied",
    );
    await flush();
    expect(gets()).toBe(1);
  });
});

// Only Save is held while a save is out, so the dialog can be dismissed under
// it, and the page left. TanStack runs the callbacks given to mutate() only
// while the component is mounted, and the hook has opted out of the global
// toast, so a save that fails after that would be reported nowhere:
// useNodeOptionsSave toasts it instead, naming the node, and one that succeeds
// must not close the dialog opened in the meantime.
describe("a save that settles after its dialog is gone", () => {
  const DENIED = "Proxmox API permission denied";
  const FAILED = `Saving the options of ${NODE} failed: ${DENIED}`;

  /** Edits the delay and saves, with the request held. */
  async function saveHeld(user: UserEvent) {
    const held = deferred<unknown>();
    mockedPut.mockReset();
    mockedPut.mockReturnValueOnce(held.promise);
    const dialog = await openDialog(user);
    await setField(user, dialog, DELAY, "31");
    await save(user, dialog);
    expect(
      await within(dialog).findByRole("button", { name: "Saving..." }),
    ).toBeDisabled();
    return { held, dialog };
  }

  it.each(LEAVES)(
    "toasts a failure that comes after %s, once",
    async (_, leave) => {
      const user = userEvent.setup();
      serve(read());
      renderCard();

      const { held, dialog } = await saveHeld(user);
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });

      held.reject(
        new ApiClientError(403, { error: "forbidden", message: DENIED }),
      );
      await waitFor(() => {
        expect(mockedToastError).toHaveBeenCalledWith(FAILED);
      });
      // Once: not also by the global net, and not again later.
      await flush();
      expect(mockedToastError).toHaveBeenCalledTimes(1);
      expect(mockedPut).toHaveBeenCalledTimes(1);
    },
  );

  it("toasts a stale-digest refusal that comes after the dialog was dismissed, and does not read again", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const { held } = await saveHeld(user);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    const before = gets();

    held.reject(conflict());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(
        `Saving the options of ${NODE} failed: ${STALE}`,
      );
    });
    await flush();

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(gets()).toBe(before);
  });

  it("does not close the dialog opened in its place when it succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    serve(read());
    const { qc } = renderCard();

    const { held } = await saveHeld(user);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    const second = await openDialog(user);

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
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  it("does not show a dismissed dialog's failure in the dialog opened in its place", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const { held } = await saveHeld(user);
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    const second = await openDialog(user);

    held.reject(
      new ApiClientError(403, { error: "forbidden", message: DENIED }),
    );
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(FAILED);
    });
    await flush();

    expect(within(second).queryByText(DENIED)).toBeNull();
    expect(within(second).queryByRole("alert")).toBeNull();
    expect(second).toHaveAttribute("data-state", "open");
  });
});

describe("focus", () => {
  it.each(LEAVES.slice(0, 3))(
    "goes back to the Edit button after %s",
    async (_, leave) => {
      const user = userEvent.setup();
      serve(read());
      renderCard();

      const edit = await editButton();
      const dialog = await openDialog(user);
      await leave(user, dialog);

      await waitFor(() => {
        expect(edit).toHaveFocus();
      });
    },
  );

  it("falls back to the card when the Edit button is gone by the time the dialog closes", async () => {
    const user = userEvent.setup();
    serve(read());
    const { rerender } = renderCard();

    const dialog = await openDialog(user);
    // The node goes offline under the open dialog, and Edit goes with it.
    rerender({ online: false });
    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    // The card is the region the dialog came from, and takes focus.
    const card = screen.getByText("Options").closest('[tabindex="-1"]');
    expect(card).not.toBeNull();
    await waitFor(() => {
      expect(card).toHaveFocus();
    });
  });
});
