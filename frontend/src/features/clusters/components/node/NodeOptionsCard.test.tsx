import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  renderHook,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";

import { apiClient, ApiClientError } from "@/lib/api-client";
import { createAppQueryClient } from "@/test/app-query-client";
import { deferred } from "@/test/fake-server";
import { DENIED, denied, flushInAct } from "@/test/save-outcome-kit";
import { createWrapper, renderWithProviders } from "@/test/test-utils";
import { fill } from "@/test/user";
import type { NodeOptions } from "../../api/node-options-queries";
import { NodeOptionsCard } from "./NodeOptionsCard";

/**
 * The wiring of the Options card and its dialog: what a save sends, the digest
 * it is pinned to, the 409 re-read and who hears of a failure. The logic the
 * dialog calls (the property strings, the field checks, changedNodeOptions,
 * nodeOptionSupport) is tested once, in lib/node-options.test.ts; here each kind
 * of field is edited once through the real UI to show the dialog uses it.
 */

// The transport is mocked, not the hooks, so the real queries and mutations
// run and each test asserts the request that would leave the browser.
vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// The app's mutation-error net (lib/query-client.ts) toasts through sonner, so
// this one mock sees every toast a run can raise.
vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);
const mockedToastError = vi.mocked(toast.error);

const CLUSTER = "cccccccc-0000-0000-0000-000000000007";
const NODE = "pve-01";
const OPTIONS_URL = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/options`;
const OPTIONS_KEY = ["clusters", CLUSTER, "nodes", NODE, "options"];
const ACME_KEY = ["clusters", CLUSTER, "nodes", NODE, "acme-config"];

// pve-manager's package string, as the node list carries it; 9.2.20 has every
// gated field.
const CURRENT = "pve-manager/9.2.20/0123abcd";

const DIALOG = `Edit Options - ${NODE}`;
const MAC = "02:00:00:00:00:01";
const MAC_2 = "02:00:00:00:00:02";

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
const COULD_NOT_REREAD =
  "Nexara could not re-read this node's configuration, so saving again will be refused again until it can.";
const NEEDS_MAC = "Enter this node's MAC address first — Proxmox requires it.";
const CLEARING_MAC =
  "Clearing the MAC address removes Wake-on-LAN, including the interface and broadcast address.";
const kept = (segment: string) =>
  `Also stored with this setting and kept as is: ${segment}`;
const removedWithIt = (segment: string) =>
  `Also stored with this setting and removed along with it: ${segment}`;

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
type Edit = [label: string, value: string];

function conflict(): ApiClientError {
  return new ApiClientError(409, { error: "conflict", message: STALE });
}

function badGateway(): ApiClientError {
  return new ApiClientError(502, {
    error: "bad_gateway",
    message: "Failed to connect to Proxmox",
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

/** Renders the card on the app's own kind of client; `rerender` changes props. */
function renderCard(over: Partial<CardProps> = {}) {
  const props: CardProps = {
    pveVersion: CURRENT,
    online: true,
    canEdit: true,
    ...over,
  };
  const ui = (p: CardProps) => (
    <NodeOptionsCard clusterId={CLUSTER} nodeName={NODE} {...p} />
  );
  const view = renderWithProviders(ui(props), {
    client: createAppQueryClient(),
    router: false,
  });
  return {
    qc: view.queryClient,
    rerender: (next: Partial<CardProps>) => {
      view.rerender(ui({ ...props, ...next }));
    },
  };
}

function editButton() {
  return screen.findByRole("button", { name: "Edit node options" });
}

function expectNoEdit() {
  expect(
    screen.queryByRole("button", { name: "Edit node options" }),
  ).toBeNull();
}

async function openDialog(user: UserEvent): Promise<HTMLElement> {
  await user.click(await editButton());
  return screen.findByRole("dialog", { name: DIALOG });
}

function field(dialog: HTMLElement, label: string): HTMLInputElement {
  return within(dialog).getByLabelText<HTMLInputElement>(label);
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

/** Waits for `text` to appear in the dialog. */
async function seen(dialog: HTMLElement, text: string) {
  expect(await within(dialog).findByText(text)).toBeInTheDocument();
}

/** Presses Save and returns the body of the one PUT that follows. */
async function savedBody(user: UserEvent, dialog: HTMLElement) {
  await save(user, dialog);
  await waitFor(() => {
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });
  return putBody();
}

/** Submits the form without the Save button, as Enter in a field would. */
function submit(dialog: HTMLElement) {
  const form = dialog.querySelector("form");
  if (form === null) throw new Error("the dialog has no form");
  fireEvent.submit(form);
}

/** Opens the dialog on `options` with `edits` made, returns what Save sends. */
async function editAndSave(options: NodeOptions, edits: Edit[]) {
  const user = userEvent.setup();
  serve(options);
  renderCard();
  const dialog = await openDialog(user);
  for (const [label, value] of edits) fill(field(dialog, label), value);
  return savedBody(user, dialog);
}

type Leave = (user: UserEvent, dialog: HTMLElement) => Promise<void>;
const cancel: Leave = async (user, dialog) => {
  await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
};
const escape: Leave = async (user) => {
  await user.keyboard("{Escape}");
};
const closeButton: Leave = async (user, dialog) => {
  await user.click(within(dialog).getByRole("button", { name: "Close" }));
};
const leavePage: Leave = () => {
  cleanup();
  return Promise.resolve();
};

beforeEach(() => {
  mockedGet.mockReset();
  mockedPut.mockReset();
  mockedToastError.mockReset();
  mockedPut.mockResolvedValue({ status: "ok" });
});

describe("what the card shows", () => {
  it.each([
    [
      "every setting that is set",
      read(),
      ["30 seconds", "60%", MAC, "Site A (12.5, -45.25)"],
    ],
    [
      "what an unset setting means",
      { digest: "d1" },
      [
        "Default (no delay)",
        "Default (80%)",
        "Not configured",
        "From datacenter",
      ],
    ],
  ] satisfies [string, NodeOptions, string[]][])(
    "lists %s",
    async (_, options, texts) => {
      serve(options);
      renderCard();

      for (const text of texts) {
        expect(await screen.findByText(text)).toBeInTheDocument();
      }
      expect(
        screen.getByText("Start on boot delay", { selector: "dt" }),
      ).toBeInTheDocument();
    },
  );

  it("keeps the value as stored in a row's tooltip, whether or not it can be read", async () => {
    const stored = `mac=${MAC},bind-interface=vmbr0`;
    serve(read({ wakeonlan: stored, location: "Site A" }));
    renderCard();

    expect(await screen.findByText(`${MAC} via vmbr0`)).toHaveAttribute(
      "title",
      stored,
    );
    expect(screen.getByText("Site A")).toHaveAttribute("title", "Site A");
  });
});

describe("when Edit is offered", () => {
  it("is not offered while the options are still being read", async () => {
    const held = deferred<NodeOptions>();
    mockedGet.mockReturnValueOnce(held.promise);
    renderCard();

    await flushInAct();
    expectNoEdit();

    held.resolve(read());
    expect(await editButton()).toBeInTheDocument();
  });

  it("is not offered for a read that failed, and the notice offers a retry", async () => {
    const user = userEvent.setup();
    mockedGet.mockRejectedValueOnce(badGateway());
    mockedGet.mockResolvedValue(read());
    renderCard();

    expect(
      await screen.findByText("Could not load this node's options."),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Failed to connect to Proxmox"),
    ).toBeInTheDocument();
    expectNoEdit();

    await user.click(screen.getByRole("button", { name: "Retry" }));

    expect(await editButton()).toBeInTheDocument();
    expect(screen.getByText("30 seconds")).toBeInTheDocument();
  });

  it("is not offered to someone who cannot manage nodes", async () => {
    serve(read());
    renderCard({ canEdit: false });

    expect(await screen.findByText("30 seconds")).toBeInTheDocument();
    expectNoEdit();
  });

  it("does not read an offline node, and says why", async () => {
    serve(read());
    renderCard({ online: false });

    expect(
      await screen.findByText(
        "Options can only be read while the node is online.",
      ),
    ).toBeInTheDocument();
    await flushInAct();
    expect(gets()).toBe(0);
    expectNoEdit();
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
    expectNoEdit();
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
    fill(field(dialog, DELAY), "45");
    expect(await savedBody(user, dialog)).toEqual({
      "startall-onboot-delay": 45,
      digest: "d1",
    });
  });
});

describe("a save sends only what was changed", () => {
  it("sends the delay alone, with the digest it was read at, and closes on the new read", async () => {
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

    fill(field(dialog, DELAY), "31");
    expect(await savedBody(user, dialog)).toEqual({
      "startall-onboot-delay": 31,
      digest: "d1",
    });
    expect(mockedPut.mock.calls[0]?.[0]).toBe(OPTIONS_URL);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("holds Save for an untouched dialog or a change made back, and a submit past the button sends nothing", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    expect(saveButton(dialog)).toBeDisabled();

    await user.click(field(dialog, DELAY));
    await user.keyboard("{Enter}");
    submit(dialog);
    await flushInAct();
    expect(mockedPut).not.toHaveBeenCalled();
    expect(screen.getByRole("dialog", { name: DIALOG })).toBeInTheDocument();

    fill(field(dialog, DELAY), "99");
    expect(saveButton(dialog)).toBeEnabled();
    fill(field(dialog, DELAY), "30");
    expect(saveButton(dialog)).toBeDisabled();
  });

  // One edit per kind of field. How a set, a clear and a build on the stored
  // value come out is changedNodeOptions', buildWakeOnLan's and buildLocation's
  // to get right (lib/node-options.test.ts); this shows the dialog hands them
  // its drafts.
  it.each([
    [
      "an integer set where there was none",
      { digest: "d1" },
      [[TARGET, "70"]],
      { "ballooning-target": 70, digest: "d1" },
    ],
    [
      "0 as a value, not as nothing",
      read(),
      [
        [DELAY, "0"],
        [TARGET, "0"],
      ],
      { "startall-onboot-delay": 0, "ballooning-target": 0, digest: "d1" },
    ],
    [
      "an integer cleared, through delete",
      read(),
      [[TARGET, ""]],
      { delete: ["ballooning-target"], digest: "d1" },
    ],
    [
      "a new location, in the order latitude, longitude, name",
      { digest: "d1" },
      [
        [NAME, "Site A"],
        [LONGITUDE, "-45.25"],
        [LATITUDE, "12.5"],
      ],
      {
        location: "latitude=12.5,longitude=-45.25,name=Site A",
        digest: "d1",
      },
    ],
    [
      "no digest key when the read had none",
      {},
      [[DELAY, "31"]],
      { "startall-onboot-delay": 31 },
    ],
  ] satisfies [string, NodeOptions, Edit[], Record<string, unknown>][])(
    "sends %s",
    async (_, options, edits, body) => {
      expect(await editAndSave(options, edits)).toStrictEqual(body);
    },
  );
});

describe("the checks of the fields", () => {
  // Every field set, so that a bad value whose check was left out of the dialog
  // would turn into a change and enable Save.
  const FULL = read({
    wakeonlan: `${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
  });
  const REFUSED: [label: string, bad: string, good: string, why: RegExp][] = [
    [DELAY, "301", "30", /whole number from 0 to 300/],
    [TARGET, "101", "60", /whole number from 0 to 100/],
    [MAC_FIELD, "03:00:00:00:00:01", MAC, /unicast MAC address/],
    [INTERFACE, "a", "vmbr0", /interface name/],
    [BROADCAST, "256.0.0.1", "192.0.2.255", /IPv4 address/],
    [LATITUDE, "91", "12.5", /latitude as a number from -90 to 90/],
    [LONGITUDE, "180.5", "-45.25", /longitude as a number from -180 to 180/],
    [NAME, "Site A, rack01", "Site A", /name cannot contain a comma/],
  ];

  it("puts each check's reason under its field and holds Save", async () => {
    const user = userEvent.setup();
    serve(FULL);
    renderCard();

    const dialog = await openDialog(user);
    for (const [label, bad, good, why] of REFUSED) {
      fill(field(dialog, label), bad);
      expect(field(dialog, label), label).toHaveAttribute(
        "aria-invalid",
        "true",
      );
      expect(describedBy(field(dialog, label)), label).toMatch(why);
      if ([LATITUDE, LONGITUDE, NAME].includes(label)) {
        // The text shared with the other location fields stays beside the reason.
        expect(describedBy(field(dialog, label)), label).toContain(
          "Leave empty to use the datacenter's location.",
        );
      }
      expect(saveButton(dialog), label).toBeDisabled();

      fill(field(dialog, label), good);
      expect(field(dialog, label), label).not.toHaveAttribute("aria-invalid");
    }
  });

  // The dialog's own rule: pve-node-location needs both coordinates.
  it("wants both coordinates for a location, and a name needs them too", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    fill(field(dialog, LATITUDE), "12.5");
    expect(
      within(dialog).getByText(
        "A location needs both coordinates: enter a longitude.",
      ),
    ).toBeInTheDocument();
    expect(field(dialog, LONGITUDE)).toHaveAttribute("aria-invalid", "true");
    expect(saveButton(dialog)).toBeDisabled();

    fill(field(dialog, LATITUDE), "");
    fill(field(dialog, NAME), "Site A");
    expect(
      within(dialog).getByText(
        "A location needs both coordinates: enter a latitude.",
      ),
    ).toBeInTheDocument();
    expect(saveButton(dialog)).toBeDisabled();
  });

  it("says what each field is for, and sets no native limit on any", async () => {
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
    // Proxmox reads the MAC from the node being woken and the other two from
    // the node that sends the packet (PVE/API2/Nodes.pm): the text says which.
    expect(describedBy(field(dialog, MAC_FIELD))).toBe(
      "The MAC address this node is woken by.",
    );
    const sending =
      "The interface and broadcast address are what this node uses when it sends a wake packet to another node.";
    expect(describedBy(field(dialog, INTERFACE))).toBe(sending);
    expect(describedBy(field(dialog, BROADCAST))).toBe(sending);
    expect(dialog).not.toHaveTextContent("Other nodes wake this one");
    for (const label of [LATITUDE, LONGITUDE, NAME]) {
      expect(describedBy(field(dialog, label))).toContain(
        "Leave empty to use the datacenter's location.",
      );
    }
  });
});

describe("Wake-on-LAN", () => {
  it("holds the interface and broadcast address until a MAC is entered, then writes it bare and first", async () => {
    const user = userEvent.setup();
    serve({ digest: "d1" });
    renderCard();

    const dialog = await openDialog(user);
    for (const label of [INTERFACE, BROADCAST]) {
      expect(field(dialog, label)).toBeDisabled();
      expect(describedBy(field(dialog, label))).toContain(NEEDS_MAC);
    }

    fill(field(dialog, MAC_FIELD), MAC);

    expect(within(dialog).queryByText(NEEDS_MAC)).toBeNull();
    for (const label of [INTERFACE, BROADCAST]) {
      expect(field(dialog, label)).toBeEnabled();
    }
    fill(field(dialog, INTERFACE), "vmbr0");
    fill(field(dialog, BROADCAST), "192.0.2.255");
    expect(await savedBody(user, dialog)).toEqual({
      wakeonlan: `${MAC},bind-interface=vmbr0,broadcast-address=192.0.2.255`,
      digest: "d1",
    });
  });

  it("builds on the stored value: a subkey with no field of its own stays where it stands", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: `${MAC},foo=bar,bind-interface=vmbr0` }));
    renderCard();

    const dialog = await openDialog(user);
    fill(field(dialog, MAC_FIELD), MAC_2);
    expect(lineOf(dialog, kept("foo=bar"))).toBeInTheDocument();

    expect(await savedBody(user, dialog)).toEqual({
      wakeonlan: `${MAC_2},foo=bar,bind-interface=vmbr0`,
      digest: "d1",
    });
  });

  it("removes the whole setting when its MAC is cleared, and no longer checks what was beside it", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: `${MAC},bind-interface=vmbr0,foo=bar` }));
    renderCard();

    const dialog = await openDialog(user);
    fill(field(dialog, INTERFACE), "a");
    fill(field(dialog, BROADCAST), "256.0.0.1");
    expect(field(dialog, INTERFACE)).toHaveAttribute("aria-invalid", "true");
    expect(field(dialog, BROADCAST)).toHaveAttribute("aria-invalid", "true");
    expect(saveButton(dialog)).toBeDisabled();

    // With no MAC the interface and broadcast address are switched off and go
    // with the setting: not checked, not in the way, and the form says so.
    fill(field(dialog, MAC_FIELD), "");

    for (const label of [INTERFACE, BROADCAST]) {
      expect(field(dialog, label)).toBeDisabled();
      expect(field(dialog, label)).not.toHaveAttribute("aria-invalid");
    }
    expect(within(dialog).getByText(CLEARING_MAC)).toBeInTheDocument();
    expect(within(dialog).getByText(NEEDS_MAC)).toBeInTheDocument();
    expect(lineOf(dialog, removedWithIt("foo=bar"))).toBeInTheDocument();
    expect(within(dialog).queryByText(/kept as is/)).toBeNull();
    expect(saveButton(dialog)).toBeEnabled();
    expect(await savedBody(user, dialog)).toEqual({
      delete: ["wakeonlan"],
      digest: "d1",
    });
  });
});

describe("location", () => {
  it("builds on the stored value: a key with no field of its own stays where it stands", async () => {
    const user = userEvent.setup();
    serve(read({ location: "latitude=12.5,longitude=-45.25,foo=bar" }));
    renderCard();

    const dialog = await openDialog(user);
    expect(lineOf(dialog, kept("foo=bar"))).toBeInTheDocument();
    // Only the location's: the Wake-on-LAN setting has no such key.
    expect(
      within(dialog).getAllByText(/Also stored with this setting/),
    ).toHaveLength(1);
    fill(field(dialog, LATITUDE), "13");

    expect(await savedBody(user, dialog)).toEqual({
      location: "latitude=13,longitude=-45.25,foo=bar",
      digest: "d1",
    });
  });

  it("removes the location, and says its other keys go with it, when all three fields are cleared", async () => {
    const user = userEvent.setup();
    serve(read({ location: "latitude=12.5,longitude=-45.25,foo=bar" }));
    renderCard();

    const dialog = await openDialog(user);
    fill(field(dialog, LATITUDE), "");
    // Part way through, it is not a location yet.
    expect(saveButton(dialog)).toBeDisabled();
    fill(field(dialog, LONGITUDE), "");
    fill(field(dialog, NAME), "");

    expect(lineOf(dialog, removedWithIt("foo=bar"))).toBeInTheDocument();
    expect(within(dialog).queryByText(/kept as is/)).toBeNull();
    expect(await savedBody(user, dialog)).toEqual({
      delete: ["location"],
      digest: "d1",
    });
  });
});

describe("a value Nexara cannot read", () => {
  const TWO_MACS = `${MAC},${MAC_2}`;
  const removeWol = "Remove this Wake-on-LAN setting";

  it("is shown as stored with nothing to type into, and is kept unless Remove is ticked", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: TWO_MACS, location: "Site A" }));
    renderCard();

    const dialog = await openDialog(user);
    expect(within(dialog).getByText(TWO_MACS)).toBeInTheDocument();
    expect(within(dialog).getByText("Site A")).toBeInTheDocument();
    expect(within(dialog).queryByLabelText(MAC_FIELD)).toBeNull();
    expect(within(dialog).queryByLabelText(LATITUDE)).toBeNull();
    expect(saveButton(dialog)).toBeDisabled();

    // Ticking Remove is a change, and unticking it is not.
    const remove = within(dialog).getByRole("checkbox", { name: removeWol });
    await user.click(remove);
    expect(saveButton(dialog)).toBeEnabled();
    await user.click(remove);
    expect(saveButton(dialog)).toBeDisabled();

    // Changing another setting does not touch it.
    fill(field(dialog, DELAY), "31");
    expect(await savedBody(user, dialog)).toEqual({
      "startall-onboot-delay": 31,
      digest: "d1",
    });
  });

  it("is removed through delete when Remove is ticked", async () => {
    const user = userEvent.setup();
    serve(read({ wakeonlan: TWO_MACS, location: "Site A" }));
    renderCard();

    const dialog = await openDialog(user);
    await user.click(within(dialog).getByRole("checkbox", { name: removeWol }));
    await user.click(
      within(dialog).getByRole("checkbox", { name: "Remove this location" }),
    );

    expect(await savedBody(user, dialog)).toEqual({
      delete: ["wakeonlan", "location"],
      digest: "d1",
    });
  });
});

describe("a node's version decides which fields there are", () => {
  it.each([
    // The version before each field's first release hides it.
    ["8.1.8", false, false, false],
    ["8.3.5", false, true, false],
    ["9.1.12", true, true, false],
    ["", false, false, false],
  ])(
    "on %s: ballooning %s, interface and broadcast %s, location %s",
    async (version, ballooning, bindBroadcast, location) => {
      const user = userEvent.setup();
      // A read that holds none of the gated settings.
      serve({ "startall-onboot-delay": 30, wakeonlan: MAC, digest: "d1" });
      renderCard({
        pveVersion: version === "" ? "" : `pve-manager/${version}/0123abcd`,
      });

      await screen.findByText("30 seconds");
      const row = (label: string) =>
        screen.queryByText(label, { selector: "dt" }) !== null;
      expect(row("RAM ballooning target")).toBe(ballooning);
      expect(row("Location")).toBe(location);

      const dialog = await openDialog(user);
      const has = (label: string) =>
        within(dialog).queryByLabelText(label) !== null;
      expect(has(TARGET)).toBe(ballooning);
      expect(has(INTERFACE)).toBe(bindBroadcast);
      expect(has(BROADCAST)).toBe(bindBroadcast);
      for (const label of [LATITUDE, LONGITUDE, NAME]) {
        expect(has(label)).toBe(location);
      }

      // What it hides it never sends: the one field changed is the one sent.
      fill(field(dialog, DELAY), "31");
      expect(await savedBody(user, dialog)).toEqual({
        "startall-onboot-delay": 31,
        digest: "d1",
      });
    },
  );

  it("offers a field the read already holds, whatever the version says", async () => {
    const user = userEvent.setup();
    serve(
      read({
        wakeonlan: `${MAC},bind-interface=vmbr0`,
        location: "latitude=1,longitude=2",
      }),
    );
    renderCard({ pveVersion: "" });

    expect(await screen.findByText("60%")).toBeInTheDocument();
    expect(screen.getByText("1, 2")).toBeInTheDocument();
    const dialog = await openDialog(user);
    expect(field(dialog, TARGET)).toHaveValue("60");
    expect(field(dialog, INTERFACE)).toHaveValue("vmbr0");
    expect(field(dialog, LATITUDE)).toHaveValue("1");

    fill(field(dialog, TARGET), "70");
    expect(await savedBody(user, dialog)).toEqual({
      "ballooning-target": 70,
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
    await flushInAct();
    // Stale, without being read again: an observer that mounted on stale data
    // would fetch it, so a dialog that read the node for itself shows. On
    // fresh data it would not, and this would pass with such a dialog in place.
    await act(async () => {
      await qc.invalidateQueries({
        queryKey: OPTIONS_KEY,
        refetchType: "none",
      });
    });

    const before = gets();
    await openDialog(user);
    await flushInAct();

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

    fill(field(dialog, TARGET), "70");
    // And only what was changed here: not the delay the refresh moved.
    expect(await savedBody(user, dialog)).toEqual({
      "ballooning-target": 70,
      digest: "d1",
    });
  });

  it("is read under the node's own prefix, which an ACME save invalidates", async () => {
    serve(read());
    const { qc } = renderCard();
    await editButton();
    expect(qc.getQueryData(OPTIONS_KEY)).toEqual(read());
    expect(gets()).toBe(1);

    await act(async () => {
      await qc.invalidateQueries({
        queryKey: ["clusters", CLUSTER, "nodes", NODE],
      });
    });

    expect(gets()).toBe(2);
  });

  it("refreshes the card and the ACME read that shares the file after a save, and keeps the dialog open until the card has it", async () => {
    const user = userEvent.setup();
    const reread = deferred<NodeOptions>();
    serve(read());
    const { qc } = renderCard();
    qc.setQueryData(ACME_KEY, { digest: "d1" });
    expect(qc.getQueryState(ACME_KEY)?.isInvalidated).toBe(false);

    const dialog = await openDialog(user);
    fill(field(dialog, DELAY), "31");
    // The read the save triggers, held: the PUT is done and the card is not.
    mockedGet.mockImplementationOnce(() => reread.promise);
    await savedBody(user, dialog);
    await flushInAct();

    expect(screen.getByRole("dialog", { name: DIALOG })).toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "Saving..." }),
    ).toBeDisabled();
    expect(screen.queryByText("31 seconds")).toBeNull();

    reread.resolve(read({ "startall-onboot-delay": 31, digest: "d2" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    // It closed on the new read, not the old one.
    expect(screen.getByText("31 seconds")).toBeInTheDocument();
    expect(qc.getQueryState(ACME_KEY)?.isInvalidated).toBe(true);
  });

  it("is still a saved write when the read that follows it fails", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const dialog = await openDialog(user);
    fill(field(dialog, DELAY), "31");
    mockedGet.mockRejectedValueOnce(badGateway());
    await save(user, dialog);

    // Closed, with no failure shown or toasted: the node did take the write.
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    await flushInAct();
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
    fill(field(dialog, DELAY), "31");
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
    fill(field(dialog, DELAY), "31");
    fill(field(dialog, TARGET), "");
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
    // A submit past the button is held too: it would go out on the stale pin.
    submit(dialog);
    await flushInAct();
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
    await seen(dialog, "Changed since you opened this: Start on boot delay.");
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
    fill(field(dialog, DELAY), "31");
    mockedGet.mockRejectedValueOnce(badGateway());
    await save(user, dialog);

    await seen(dialog, COULD_NOT_REREAD);
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
    fill(field(first, DELAY), "31");
    mockedGet.mockImplementationOnce(() => reread.promise);
    await save(user, first);
    await within(first).findByRole("alert");

    // Dismissed with the re-read still out; another dialog opened in its place.
    await cancel(user, first);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    const second = await openDialog(user);
    expect(field(second, DELAY)).toHaveValue("30");

    reread.resolve(read({ digest: "d3" }));
    await flushInAct();

    // The second dialog is untouched by it: no alert, no note, and its own
    // digest.
    expect(within(second).queryByRole("alert")).toBeNull();
    expect(within(second).queryByRole("status")).toBeNull();
    fill(field(second, TARGET), "70");
    await save(user, second);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(2);
    });
    expect(putBody(1)).toEqual({ "ballooning-target": 70, digest: "d1" });
  });

  // The note names only what the dialog itself shows, in the card's order; which
  // settings count as changed is optionsChangedBetween's (lib/node-options.test.ts).
  it.each([
    [
      "every setting that changed, in the card's order",
      CURRENT,
      read(),
      read({
        "startall-onboot-delay": 45,
        "ballooning-target": 70,
        wakeonlan: MAC_2,
        location: "latitude=1,longitude=2",
        digest: "d2",
      }),
      "Changed since you opened this: Start on boot delay, RAM ballooning target, Wake-on-LAN, Location.",
    ],
    [
      "that none of them did, when the file changed elsewhere",
      CURRENT,
      read(),
      read({ digest: "d2" }),
      "None of the settings shown here changed; something else in the node's configuration did.",
    ],
    [
      "only the ones this node's version shows",
      "pve-manager/8.1.8/0123abcd",
      { "startall-onboot-delay": 30, wakeonlan: MAC, digest: "d1" },
      {
        "startall-onboot-delay": 45,
        wakeonlan: MAC,
        "ballooning-target": 70,
        location: "latitude=1,longitude=2",
        digest: "d2",
      },
      "Changed since you opened this: Start on boot delay.",
    ],
  ] satisfies [string, string, NodeOptions, NodeOptions, string][])(
    "names %s",
    async (_, pveVersion, first, then, note) => {
      const user = userEvent.setup();
      serve(first);
      mockedPut.mockRejectedValueOnce(conflict());
      renderCard({ pveVersion });
      const dialog = await openDialog(user);
      fill(field(dialog, DELAY), "31");
      mockedGet.mockResolvedValueOnce(then);
      await save(user, dialog);

      await seen(dialog, note);
      expect(within(dialog).getByText(SAVING_AGAIN)).toBeInTheDocument();
    },
  );

  it("says what a re-read found only while it is the latest word, and pins to the newest that succeeded", async () => {
    const user = userEvent.setup();
    serve(read());
    mockedPut
      .mockRejectedValueOnce(conflict())
      .mockRejectedValueOnce(conflict())
      .mockRejectedValueOnce(conflict());
    renderCard();
    const dialog = await openDialog(user);
    fill(field(dialog, DELAY), "31");

    mockedGet.mockResolvedValueOnce(
      read({ "startall-onboot-delay": 45, digest: "d2" }),
    );
    await save(user, dialog);
    await seen(dialog, "Changed since you opened this: Start on boot delay.");

    // Refused again, and a newer read replaces the list.
    mockedGet.mockResolvedValueOnce(
      read({ "startall-onboot-delay": 45, wakeonlan: MAC_2, digest: "d3" }),
    );
    await save(user, dialog);
    await seen(
      dialog,
      "Changed since you opened this: Start on boot delay, Wake-on-LAN.",
    );

    // Refused a third time and the node cannot be read: what the last read
    // found says nothing of THIS conflict, and its advice — that saving writes
    // over the node's current values — is not true of a pin that did not move.
    mockedGet.mockRejectedValueOnce(badGateway());
    await save(user, dialog);
    await seen(dialog, COULD_NOT_REREAD);
    expect(dialog).not.toHaveTextContent(/Changed since you opened this/);
    expect(within(dialog).queryByText(SAVING_AGAIN)).toBeNull();

    // The pin stayed with the newest read that succeeded.
    await save(user, dialog);
    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(4);
    });
    expect(putBody(3)).toEqual({ "startall-onboot-delay": 31, digest: "d3" });
  });
});

// The app's QueryClient toasts, through the MutationCache in lib/query-client.ts,
// every failed mutation whose HOOK has no onError of its own. While the dialog
// is open it shows the failure itself, so useSetNodeOptions opts out
// (errorsHandledLocally), and once the dialog is gone useNodeOptionsSave toasts
// it. These run on the app's own kind of client, with the control its docs ask for
// (test/app-query-client.ts).
describe("a save that fails is reported once, not also as a toast", () => {
  const PROBE = "PROBE-NOT-A-REAL-FAILURE";

  it("control: a failed mutation with no onError of its own toasts on this client", async () => {
    const qc = createAppQueryClient();
    const { result } = renderHook(
      () => useMutation({ mutationFn: () => Promise.reject(new Error(PROBE)) }),
      { wrapper: createWrapper({ client: qc, router: false }) },
    );

    await act(async () => {
      await result.current.mutateAsync().catch(() => undefined);
    });

    expect(mockedToastError).toHaveBeenCalledTimes(1);
    expect(mockedToastError).toHaveBeenCalledWith(PROBE);
  });

  it.each([
    [
      "a 400",
      new ApiClientError(400, {
        error: "bad_request",
        message: "Parameter verification failed",
      }),
      "Parameter verification failed",
    ],
    // How a dropped connection rejects fetch: a TypeError, which describeError
    // gives no words, so without a floor this failure would be silent.
    [
      "a request that got no answer",
      new TypeError("Failed to fetch"),
      "The save request failed — check your connection and try again.",
    ],
    ["a 403", denied(), DENIED],
  ])(
    "shows %s in the dialog, which stays open, with no re-read and no toast",
    async (_, failure, shown) => {
      const user = userEvent.setup();
      serve(read());
      mockedPut.mockRejectedValueOnce(failure);
      renderCard();

      const dialog = await openDialog(user);
      fill(field(dialog, DELAY), "31");
      await save(user, dialog);

      expect(await within(dialog).findByRole("alert")).toHaveTextContent(shown);
      expect(screen.getByRole("dialog", { name: DIALOG })).toBeInTheDocument();
      expect(field(dialog, DELAY)).toHaveValue("31");
      expect(saveButton(dialog)).toBeEnabled();
      // Not a conflict: no re-read, and no note.
      expect(within(dialog).queryByRole("status")).toBeNull();
      await flushInAct();
      expect(gets()).toBe(1);
      expect(mockedToastError).not.toHaveBeenCalled();
    },
  );
});

// Only Save is held while a save is out, so the dialog can be dismissed under
// it and the page left. TanStack runs mutate()'s callbacks only while the
// component is mounted and the hook has opted out of the global toast, so
// useNodeOptionsSave toasts a failure that comes after that, naming the node,
// and a success must not close the dialog opened in the meantime.
describe("a save that settles after its dialog is gone", () => {
  const FAILED = `Saving the options of ${NODE} failed: ${DENIED}`;

  /** Edits the delay and saves, with the request held. */
  async function saveHeld(user: UserEvent) {
    const held = deferred<unknown>();
    mockedPut.mockReset();
    mockedPut.mockReturnValueOnce(held.promise);
    const dialog = await openDialog(user);
    fill(field(dialog, DELAY), "31");
    await save(user, dialog);
    expect(
      await within(dialog).findByRole("button", { name: "Saving..." }),
    ).toBeDisabled();
    return { held, dialog };
  }

  /** Dismisses the dialog the held save was made in and opens another. */
  async function dismissAndReopen(user: UserEvent, dialog: HTMLElement) {
    await escape(user, dialog);
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    return openDialog(user);
  }

  it.each([
    ["the dialog being dismissed", escape, denied(), FAILED],
    ["the page being left", leavePage, denied(), FAILED],
    [
      "Cancel, as a stale-digest refusal",
      cancel,
      conflict(),
      `Saving the options of ${NODE} failed: ${STALE}`,
    ],
  ] satisfies [string, Leave, ApiClientError, string][])(
    "toasts once a failure that comes after %s, and reads nothing again",
    async (_, leave, failure, message) => {
      const user = userEvent.setup();
      serve(read());
      renderCard();

      const { held, dialog } = await saveHeld(user);
      await leave(user, dialog);
      await waitFor(() => {
        expect(dialog).not.toBeInTheDocument();
      });
      const before = gets();

      held.reject(failure);
      await waitFor(() => {
        expect(mockedToastError).toHaveBeenCalledWith(message);
      });
      // Once: not also by the global net, and not again later.
      await flushInAct();
      expect(mockedToastError).toHaveBeenCalledTimes(1);
      expect(mockedPut).toHaveBeenCalledTimes(1);
      expect(gets()).toBe(before);
    },
  );

  it("does not close the dialog opened in its place when it succeeds, and toasts nothing", async () => {
    const user = userEvent.setup();
    serve(read());
    const { qc } = renderCard();

    const { held, dialog } = await saveHeld(user);
    const second = await dismissAndReopen(user, dialog);

    held.resolve({ status: "ok" });
    await waitFor(() => {
      expect(
        qc
          .getMutationCache()
          .getAll()
          .map((mutation) => mutation.state.status),
      ).toEqual(["success"]);
    });
    await flushInAct();

    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
    expect(mockedToastError).not.toHaveBeenCalled();
    expect(mockedPut).toHaveBeenCalledTimes(1);
  });

  it("does not show its failure in the dialog opened in its place", async () => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const { held, dialog } = await saveHeld(user);
    const second = await dismissAndReopen(user, dialog);

    held.reject(denied());
    await waitFor(() => {
      expect(mockedToastError).toHaveBeenCalledWith(FAILED);
    });
    await flushInAct();

    expect(within(second).queryByText(DENIED)).toBeNull();
    expect(within(second).queryByRole("alert")).toBeNull();
    expect(second).toBeInTheDocument();
    expect(second).toHaveAttribute("data-state", "open");
  });
});

describe("focus", () => {
  it.each([
    ["Cancel", cancel],
    ["Escape", escape],
    ["its Close button", closeButton],
  ])("goes back to the Edit button after %s", async (_, leave) => {
    const user = userEvent.setup();
    serve(read());
    renderCard();

    const edit = await editButton();
    const dialog = await openDialog(user);
    await leave(user, dialog);

    await waitFor(() => {
      expect(edit).toHaveFocus();
    });
  });

  it("falls back to the card when the Edit button is gone by the time the dialog closes", async () => {
    const user = userEvent.setup();
    serve(read());
    const { rerender } = renderCard();

    const dialog = await openDialog(user);
    // The node goes offline under the open dialog, and Edit goes with it.
    rerender({ online: false });
    await cancel(user, dialog);
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
