import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  configure,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";

import { createAppQueryClient } from "@/test/app-query-client";
import { listOf, stubApi } from "@/test/fetch-stub";
import { useAuthStore } from "@/stores/auth-store";
import type { NodeResponse } from "@/types/api";
import type { NodeDNSResponse, NodeTimeResponse } from "../api/cluster-queries";
import type { NodeOptions } from "../api/node-options-queries";
import { NodeDetailPage } from "./NodeDetailPage";

/**
 * The timezone and DNS dialogs on the node page: who is offered them, what they
 * open with, and that an open one cannot outlive the node it was opened on, or
 * the permission it was opened with. The dialogs themselves are in
 * NodeSettingsDialogs.test.tsx; these run the whole page, on the wire, since
 * what is under test is how the page wires them.
 */

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

// The whole page renders cold in the first test of this file, and under load
// that takes longer than the second testing-library waits by default, which is
// all vitest.config.ts leaves it: this file alone gets more (it failed 3 runs of
// 5 under 14 busy CPUs at the default). A wait that is really stuck still ends
// the test, in 5 s instead of 1.
configure({ asyncUtilTimeout: 5000 });

const CLUSTER = "cccccccc-0000-0000-0000-00000000000b";
const NODES = `/api/v1/clusters/${CLUSTER}/nodes`;

function node(id: string, name: string, over: Partial<NodeResponse> = {}) {
  const base: NodeResponse = {
    id,
    cluster_id: CLUSTER,
    name,
    address: "192.0.2.10",
    status: "online",
    ha_state: "",
    cpu_count: 8,
    cpu_model: "Example CPU",
    cpu_cores: 4,
    cpu_sockets: 1,
    cpu_threads: 2,
    cpu_mhz: "2400",
    mem_total: 17179869184,
    disk_total: 107374182400,
    swap_total: 0,
    swap_used: 0,
    swap_free: 0,
    pve_version: "pve-manager/9.2.20/0123abcd",
    kernel_version: "6.8.0",
    // What the collector stored. It is not what the node itself says (see dns()
    // and time() below), so a dialog drawn from the wrong one shows.
    dns_servers: "192.0.2.1",
    dns_search: "stored.example.com",
    timezone: "Etc/UTC",
    subscription_status: "",
    subscription_level: "",
    load_avg: "0.1 0.1 0.1",
    io_wait: 0,
    uptime: 3600,
    last_seen_at: "2026-01-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
  return { ...base, ...over };
}

function dnsUrl(name: string) {
  return `${NODES}/${name}/dns`;
}

function timeUrl(name: string) {
  return `${NODES}/${name}/time`;
}

function optionsUrl(name: string) {
  return `${NODES}/${name}/options`;
}

// Something for the Options card to show, which is how a test knows the page
// has drawn itself and made the requests it makes.
const OPTIONS: NodeOptions = { "startall-onboot-delay": 30 };

/** What the node itself reports for its DNS. */
function dns(over: Partial<NodeDNSResponse> = {}): NodeDNSResponse {
  return {
    search: "live.example.com",
    dns1: "192.0.2.53",
    dns2: "192.0.2.54",
    dns3: "192.0.2.55",
    ...over,
  };
}

/** What the node itself reports for its time. */
function time(timezone: string): NodeTimeResponse {
  return { timezone, time: 1_767_225_600, localtime: 1_767_225_600 };
}

/** How many times the page has read `url`. */
function reads(url: string): number {
  return api.sent.filter((r) => r === `GET ${url}`).length;
}

/** The body of each request of `method` to `url` the page has sent. */
function bodiesOf(method: string, url: string): unknown[] {
  return vi
    .mocked(globalThis.fetch)
    .mock.calls.filter(
      ([input, init]) => input === url && init?.method === method,
    )
    .map(([, init]) => {
      // apiClient sends every body as a JSON string.
      if (typeof init?.body !== "string") {
        throw new Error(`${method} ${url} was sent without a JSON body`);
      }
      return JSON.parse(init.body) as unknown;
    });
}

/** Hands the router's navigate to the test, to move the page under a dialog. */
let navigateTo: (path: string) => void = () => undefined;

function Navigator() {
  const navigate = useNavigate();
  navigateTo = (path) => {
    void navigate(path);
  };
  return null;
}

function renderPage(startAt = "n1") {
  const qc = createAppQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/clusters/${CLUSTER}/nodes/${startAt}`]}>
        <Navigator />
        <Routes>
          <Route
            path="/clusters/:clusterId/nodes/:nodeId"
            element={<NodeDetailPage />}
          />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  useAuthStore.setState({
    user: null,
    permissions: ["view:node", "manage:node"],
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  useAuthStore.setState({ user: null, permissions: [] });
});

/** The two Edit buttons, as the page names them. */
const EDIT_DNS = { name: "Edit DNS settings" };
const EDIT_TIMEZONE = { name: "Edit timezone" };

/**
 * What these cards say of a read they could not make. Other cards on the page
 * have their own failures to say.
 */
const SETTINGS_NOTE = /Could not load this node's (DNS settings|timezone)/;

/** The summary card with this title: the bordered box its header sits in. */
function cardOf(title: string): HTMLElement {
  const card = screen.getByText(title).closest(".rounded-lg");
  if (!(card instanceof HTMLElement)) throw new Error(`no ${title} card`);
  return card;
}

/** The two dialogs, for the test that moves the page to another node under one. */
const MOVES: [
  name: string,
  button: { name: string },
  first: string,
  second: string,
  label: string,
  firstValue: string,
  secondValue: string,
][] = [
  [
    "DNS",
    EDIT_DNS,
    "Edit DNS Configuration - pve-01",
    "Edit DNS Configuration - pve-02",
    "Search Domain",
    "live.example.com",
    "other.example.com",
  ],
  [
    "timezone",
    EDIT_TIMEZONE,
    "Edit Timezone - pve-01",
    "Edit Timezone - pve-02",
    "Timezone",
    "Europe/London",
    "Etc/UTC",
  ],
];

describe("NodeDetailPage timezone and DNS Edit buttons", () => {
  it("are offered to someone who manages nodes, once the node has been read", async () => {
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [dnsUrl("pve-01")]: dns(),
      [timeUrl("pve-01")]: time("Europe/London"),
    });
    renderPage();

    expect(await screen.findByRole("button", EDIT_DNS)).toBeInTheDocument();
    expect(
      await screen.findByRole("button", EDIT_TIMEZONE),
    ).toBeInTheDocument();
    // Each reads its own route, once.
    expect(reads(dnsUrl("pve-01"))).toBe(1);
    expect(reads(timeUrl("pve-01"))).toBe(1);
    expect(screen.queryByText(SETTINGS_NOTE)).toBeNull();
  });

  // The Edit button is not drawn without its read, and a button that is simply
  // absent explains nothing: each card says what it could not read, under its
  // own rows.
  it("say under their own card's rows what could not be read, to someone who manages nodes", async () => {
    // Neither route is stubbed: the stub answers 404.
    api = stubApi({ [NODES]: listOf([node("n1", "pve-01")]) });
    renderPage();

    await screen.findByText("System");
    const system = cardOf("System");
    const network = cardOf("Network & DNS");
    expect(
      await within(network).findByText(
        /Could not load this node's DNS settings: HTTP 404/,
      ),
    ).toBeInTheDocument();
    expect(
      await within(system).findByText(
        /Could not load this node's timezone: HTTP 404/,
      ),
    ).toBeInTheDocument();
    expect(within(network).queryByText(/timezone/)).toBeNull();
    expect(within(system).queryByText(/DNS settings/)).toBeNull();
    expect(screen.queryByRole("button", EDIT_DNS)).toBeNull();
    expect(screen.queryByRole("button", EDIT_TIMEZONE)).toBeNull();
  });

  // A write needs manage:node, so for a viewer there is no dialog to open, and
  // neither read has any use.
  it("are not offered to someone who may only view nodes, who read neither", async () => {
    useAuthStore.setState({ permissions: ["view:node"] });
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [optionsUrl("pve-01")]: OPTIONS,
      [dnsUrl("pve-01")]: dns(),
      [timeUrl("pve-01")]: time("Europe/London"),
    });
    renderPage();

    // The page is up, and has made the requests it makes. Its cards show the
    // node list's copy of the DNS and the timezone, which is all they need.
    expect(await screen.findByText("30 seconds")).toBeInTheDocument();
    expect(screen.getByText("stored.example.com")).toBeInTheDocument();
    expect(screen.getByText("Etc/UTC")).toBeInTheDocument();
    expect(screen.queryByRole("button", EDIT_DNS)).toBeNull();
    expect(screen.queryByRole("button", EDIT_TIMEZONE)).toBeNull();
    expect(screen.queryByText(SETTINGS_NOTE)).toBeNull();
    expect(reads(dnsUrl("pve-01"))).toBe(0);
    expect(reads(timeUrl("pve-01"))).toBe(0);
  });

  it("are not offered on an offline node, which is read from neither", async () => {
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01", { status: "offline" })]),
      [dnsUrl("pve-01")]: dns(),
      [timeUrl("pve-01")]: time("Europe/London"),
    });
    renderPage();

    expect(
      await screen.findByText(
        "Options can only be read while the node is online.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", EDIT_DNS)).toBeNull();
    expect(screen.queryByRole("button", EDIT_TIMEZONE)).toBeNull();
    expect(reads(dnsUrl("pve-01"))).toBe(0);
    expect(reads(timeUrl("pve-01"))).toBe(0);
  });
});

describe("NodeDetailPage timezone and DNS dialogs", () => {
  // The node list holds what the collector stored, which can be empty or behind
  // a change; the dialogs are drawn from what the node itself reports.
  it("open with what the node reports, not what the node list holds", async () => {
    const user = userEvent.setup();
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [dnsUrl("pve-01")]: dns(),
      [timeUrl("pve-01")]: time("Europe/London"),
    });
    renderPage();

    // The cards show the collector's copy: the values the dialogs must not use.
    expect(await screen.findByText("stored.example.com")).toBeInTheDocument();
    expect(screen.getByText("Etc/UTC")).toBeInTheDocument();

    await user.click(await screen.findByRole("button", EDIT_TIMEZONE));
    const timezone = await screen.findByRole("dialog", {
      name: "Edit Timezone - pve-01",
    });
    expect(within(timezone).getByLabelText("Timezone")).toHaveValue(
      "Europe/London",
    );
    // Pressing Edit read the node again.
    expect(reads(timeUrl("pve-01"))).toBe(2);
    await user.click(within(timezone).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    await user.click(await screen.findByRole("button", EDIT_DNS));
    const dialog = await screen.findByRole("dialog", {
      name: "Edit DNS Configuration - pve-01",
    });
    expect(within(dialog).getByLabelText("Search Domain")).toHaveValue(
      "live.example.com",
    );
    expect(within(dialog).getByLabelText("DNS Server 1")).toHaveValue(
      "192.0.2.53",
    );
    expect(within(dialog).getByLabelText("DNS Server 2")).toHaveValue(
      "192.0.2.54",
    );
    expect(within(dialog).getByLabelText("DNS Server 3")).toHaveValue(
      "192.0.2.55",
    );
  });

  it("save what they were opened with, the DNS with all four of its settings", async () => {
    const user = userEvent.setup();
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [dnsUrl("pve-01")]: dns(),
      [timeUrl("pve-01")]: time("Europe/London"),
    });
    renderPage();

    await user.click(await screen.findByRole("button", EDIT_DNS));
    const dnsDialog = await screen.findByRole("dialog", {
      name: "Edit DNS Configuration - pve-01",
    });
    const search = within(dnsDialog).getByLabelText("Search Domain");
    await user.clear(search);
    await user.type(search, "new.example.com");
    await user.click(within(dnsDialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    await user.click(await screen.findByRole("button", EDIT_TIMEZONE));
    const timezoneDialog = await screen.findByRole("dialog", {
      name: "Edit Timezone - pve-01",
    });
    await user.click(
      within(timezoneDialog).getByRole("button", { name: "Save" }),
    );
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    expect(api.writes()).toEqual([
      `PUT ${dnsUrl("pve-01")}`,
      `PUT ${timeUrl("pve-01")}`,
    ]);
    expect(bodiesOf("PUT", dnsUrl("pve-01"))).toEqual([
      {
        search: "new.example.com",
        dns1: "192.0.2.53",
        dns2: "192.0.2.54",
        dns3: "192.0.2.55",
      },
    ]);
    expect(bodiesOf("PUT", timeUrl("pve-01"))).toEqual([
      { timezone: "Europe/London" },
    ]);
  });

  // The role loses manage:node under an open dialog, which the auth store can do
  // on any background token refresh. What the dialog held goes with the
  // permission: it does not wait to be shown again, with what was typed in it
  // and the snapshot it was opened from, when the permission returns.
  it("are discarded with their typed input when the permission to manage nodes is revoked, and do not come back when it returns", async () => {
    const user = userEvent.setup();
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [dnsUrl("pve-01")]: dns(),
      [timeUrl("pve-01")]: time("Europe/London"),
    });
    renderPage();
    await user.click(await screen.findByRole("button", EDIT_DNS));
    const dialog = await screen.findByRole("dialog", {
      name: "Edit DNS Configuration - pve-01",
    });
    const search = within(dialog).getByLabelText("Search Domain");
    await user.clear(search);
    await user.type(search, "typed.example.com");

    act(() => {
      useAuthStore.setState({ permissions: ["view:node"] });
    });

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(screen.queryByRole("button", EDIT_DNS)).toBeNull();

    act(() => {
      useAuthStore.setState({ permissions: ["view:node", "manage:node"] });
    });

    const edit = await screen.findByRole("button", EDIT_DNS);
    expect(screen.queryByRole("dialog")).toBeNull();
    // Opened again, it is drawn from the node and not from what was typed.
    await user.click(edit);
    const again = await screen.findByRole("dialog", {
      name: "Edit DNS Configuration - pve-01",
    });
    expect(within(again).getByLabelText("Search Domain")).toHaveValue(
      "live.example.com",
    );
    expect(api.writes()).toEqual([]);
  });

  // A save that is still out holds the Edit button of its own setting, on its
  // own node, and no other.
  it("hold only the Edit button of the setting that is being saved, on the node it is being saved on", async () => {
    const user = userEvent.setup();
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01"), node("n2", "pve-02")]),
      [dnsUrl("pve-01")]: dns(),
      [timeUrl("pve-01")]: time("Europe/London"),
      [dnsUrl("pve-02")]: dns({ search: "other.example.com" }),
      [timeUrl("pve-02")]: time("Etc/UTC"),
    });
    // The DNS save of pve-01 is never answered.
    const inner = globalThis.fetch;
    vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "PUT") {
        // apiClient always passes the path as a string.
        api.sent.push(`PUT ${typeof input === "string" ? input : "?"}`);
        return new Promise<Response>(() => undefined);
      }
      return inner(input, init);
    });
    renderPage("n1");
    await user.click(await screen.findByRole("button", EDIT_DNS));
    const dialog = await screen.findByRole("dialog", {
      name: "Edit DNS Configuration - pve-01",
    });
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(api.writes()).toEqual([`PUT ${dnsUrl("pve-01")}`]);
    });
    await user.keyboard("{Escape}");
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });

    expect(screen.getByRole("button", EDIT_DNS)).toBeDisabled();
    expect(await screen.findByRole("button", EDIT_TIMEZONE)).toBeEnabled();

    // Another node's is not held by it.
    act(() => {
      navigateTo(`/clusters/${CLUSTER}/nodes/n2`);
    });
    await waitFor(() => {
      expect(
        screen.getByRole("heading", { name: "pve-02" }),
      ).toBeInTheDocument();
    });
    expect(await screen.findByRole("button", EDIT_DNS)).toBeEnabled();
  });

  // The page is one component for every node it is routed to. A dialog that
  // survived a change of node would PUT the old node's values to the new one.
  it.each(MOVES)(
    "drop an open %s dialog when the page is routed to another node",
    async (_, button, first, second, label, firstValue, secondValue) => {
      const user = userEvent.setup();
      api = stubApi({
        [NODES]: listOf([node("n1", "pve-01"), node("n2", "pve-02")]),
        [dnsUrl("pve-01")]: dns(),
        [timeUrl("pve-01")]: time("Europe/London"),
        [dnsUrl("pve-02")]: dns({ search: "other.example.com" }),
        [timeUrl("pve-02")]: time("Etc/UTC"),
      });
      renderPage("n1");

      await user.click(await screen.findByRole("button", button));
      const dialog = await screen.findByRole("dialog", { name: first });
      expect(within(dialog).getByLabelText(label)).toHaveValue(firstValue);

      // The route changes under the open dialog, as the browser's Back button
      // would change it.
      act(() => {
        navigateTo(`/clusters/${CLUSTER}/nodes/n2`);
      });

      // `hidden`: a dialog that is still open hides the page from the roles.
      await waitFor(() => {
        expect(
          screen.getByRole("heading", { name: "pve-02", hidden: true }),
        ).toBeInTheDocument();
      });
      expect(screen.queryByRole("dialog")).toBeNull();

      // What the new node's Edit opens is the new node's, drawn from its own
      // read.
      await user.click(await screen.findByRole("button", button));
      const next = await screen.findByRole("dialog", { name: second });
      expect(within(next).getByLabelText(label)).toHaveValue(secondValue);
      expect(api.writes()).toEqual([]);
    },
  );
});
