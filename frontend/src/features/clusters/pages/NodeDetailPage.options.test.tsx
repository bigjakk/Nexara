import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useNavigate } from "react-router-dom";

import { createAppQueryClient } from "@/test/app-query-client";
import { listOf, stubApi } from "@/test/fetch-stub";
import { useAuthStore } from "@/stores/auth-store";
import type { NodeResponse } from "@/types/api";
import type { NodeNotes, NodeOptions } from "../api/node-options-queries";
import { NodeDetailPage } from "./NodeDetailPage";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const CLUSTER = "cccccccc-0000-0000-0000-00000000000a";
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
    dns_servers: "192.0.2.1",
    dns_search: "example.com",
    timezone: "UTC",
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

function options(over: NodeOptions = {}): NodeOptions {
  return { "startall-onboot-delay": 30, digest: "d1", ...over };
}

function notes(over: NodeNotes = {}): NodeNotes {
  return { description: "sentinel notes\n", digest: "d1", ...over };
}

// The options are read with view:node and the notes with manage:node, from
// routes of their own; both are written through the options one.
function optionsUrl(name: string) {
  return `${NODES}/${name}/options`;
}

function notesUrl(name: string) {
  return `${NODES}/${name}/notes`;
}

/** How many times the page has read `url`. */
function reads(url: string): number {
  return api.sent.filter((r) => r === `GET ${url}`).length;
}

/**
 * Has the notes route answer 403 with `message`, in the body the backend gives
 * it ({error, message}), so what the card classifies is what arrives through the
 * real client and not an error built for the test.
 */
function refuseNotesWith(message: string) {
  const inner = globalThis.fetch;
  vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
    if ((init?.method ?? "GET") === "GET" && input === notesUrl("pve-01")) {
      api.sent.push(`GET ${notesUrl("pve-01")}`);
      return Promise.resolve(
        new Response(JSON.stringify({ error: "forbidden", message }), {
          status: 403,
          headers: { "Content-Type": "application/json" },
        }),
      );
    }
    return inner(input, init);
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

describe("NodeDetailPage options and notes cards", () => {
  it("shows both cards on the summary tab, editable for someone who manages nodes", async () => {
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [optionsUrl("pve-01")]: options(),
      [notesUrl("pve-01")]: notes(),
    });
    renderPage();

    expect(await screen.findByText("30 seconds")).toBeInTheDocument();
    expect(await screen.findByText("sentinel notes")).toBeInTheDocument();
    // A current node has the settings an older one lacks.
    expect(
      screen.getByText("RAM ballooning target", { selector: "dt" }),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Location", { selector: "dt" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Edit node options" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Edit node notes" }),
    ).toBeInTheDocument();
    // Each card reads its own route, once: the options with view:node and the
    // notes with manage:node.
    expect(reads(optionsUrl("pve-01"))).toBe(1);
    expect(reads(notesUrl("pve-01"))).toBe(1);
  });

  it("shows someone who may only view nodes the options, and no notes and no Edit", async () => {
    useAuthStore.setState({ permissions: ["view:node"] });
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [optionsUrl("pve-01")]: options(),
      [notesUrl("pve-01")]: notes(),
    });
    renderPage();

    expect(await screen.findByText("30 seconds")).toBeInTheDocument();
    expect(
      await screen.findByText(
        "Notes are visible to users who can manage this node.",
      ),
    ).toBeInTheDocument();
    expect(screen.queryByText("sentinel notes")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit node options" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
    // The notes route answers 403 to them: the page does not ask.
    expect(reads(optionsUrl("pve-01"))).toBe(1);
    expect(reads(notesUrl("pve-01"))).toBe(0);
    expect(api.sent.some((r) => r.includes("/notes"))).toBe(false);
  });

  it("lets someone who may only view nodes see the options, and offers no Edit of them", async () => {
    useAuthStore.setState({ permissions: ["view:node"] });
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [optionsUrl("pve-01")]: options(),
    });
    renderPage();

    expect(await screen.findByText("30 seconds")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Edit node options" }),
    ).toBeNull();
  });

  it("does not read an offline node's options, and offers no Edit", async () => {
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01", { status: "offline" })]),
      [optionsUrl("pve-01")]: options(),
      [notesUrl("pve-01")]: notes(),
    });
    renderPage();

    expect(
      await screen.findByText(
        "Options can only be read while the node is online.",
      ),
    ).toBeInTheDocument();
    expect(
      screen.getByText("Notes can only be read while the node is online."),
    ).toBeInTheDocument();
    expect(reads(optionsUrl("pve-01"))).toBe(0);
    expect(reads(notesUrl("pve-01"))).toBe(0);
  });

  it("hides the settings a node's version lacks", async () => {
    api = stubApi({
      [NODES]: listOf([
        node("n1", "pve-01", { pve_version: "pve-manager/8.1.8/0123abcd" }),
      ]),
      [optionsUrl("pve-01")]: options(),
    });
    renderPage();

    expect(await screen.findByText("30 seconds")).toBeInTheDocument();
    expect(
      screen.queryByText("RAM ballooning target", { selector: "dt" }),
    ).toBeNull();
    expect(screen.queryByText("Location", { selector: "dt" })).toBeNull();
  });

  // A manager the notes route refuses. canManage is flat and its cache can lag
  // the server's, so this happens; what the card says depends on who refused.
  it("tells a manager that Nexara refuses, in the backend's own words, who can read the notes, with no Retry and no Edit", async () => {
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [optionsUrl("pve-01")]: options(),
    });
    // The notes route is declared clusterCheck("manage", "node"), whose refusal
    // is requireClusterPerm's, word for word.
    refuseNotesWith("Insufficient permissions");
    renderPage();

    expect(
      await screen.findByText(
        "Notes are visible to users who can manage this node.",
      ),
    ).toBeInTheDocument();
    const card = screen.getByText("Notes").closest('[tabindex="-1"]');
    if (!(card instanceof HTMLElement)) throw new Error("no Notes card");
    expect(within(card).queryByRole("button", { name: "Retry" })).toBeNull();
    expect(within(card).queryByText(/Could not load/)).toBeNull();
    expect(within(card).queryByText("Insufficient permissions")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
    expect(reads(notesUrl("pve-01"))).toBe(1);
  });

  it("shows a manager that Proxmox refuses the failure it is, with Proxmox's message and a Retry, and no Edit", async () => {
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [optionsUrl("pve-01")]: options(),
    });
    // mapProxmoxError passes a Proxmox 403 on as a 403: Nexara's own API token
    // missing a privilege, say, which says nothing about this user.
    refuseNotesWith("Proxmox API permission denied");
    renderPage();

    expect(
      await screen.findByText("Could not load this node's notes."),
    ).toBeInTheDocument();
    const card = screen.getByText("Notes").closest('[tabindex="-1"]');
    if (!(card instanceof HTMLElement)) throw new Error("no Notes card");
    expect(
      within(card).getByText("Proxmox API permission denied"),
    ).toBeInTheDocument();
    expect(
      within(card).getByRole("button", { name: "Retry" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(
        "Notes are visible to users who can manage this node.",
      ),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Edit node notes" }),
    ).toBeNull();
  });

  // The page is one component for every node it is routed to. A dialog that
  // survived a change of node would carry the old node's snapshot, and its
  // digest, to the new node's PUT.
  it("drops an open dialog when the page is routed to another node", async () => {
    const user = userEvent.setup();
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01"), node("n2", "pve-02")]),
      [optionsUrl("pve-01")]: options(),
      [notesUrl("pve-01")]: notes(),
      [optionsUrl("pve-02")]: options({
        "startall-onboot-delay": 90,
        digest: "e1",
      }),
      [notesUrl("pve-02")]: notes({
        description: "other notes\n",
        digest: "e1",
      }),
    });
    renderPage("n1");

    await user.click(
      await screen.findByRole("button", { name: "Edit node options" }),
    );
    await screen.findByRole("dialog", { name: "Edit Options - pve-01" });

    // The route changes under the open dialog, as the browser's Back button
    // would change it.
    act(() => {
      navigateTo(`/clusters/${CLUSTER}/nodes/n2`);
    });

    await waitFor(() => {
      expect(screen.getByText("90 seconds")).toBeInTheDocument();
    });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(await screen.findByText("other notes")).toBeInTheDocument();
    expect(api.writes()).toEqual([]);
  });

  // The two are read separately and carry the digest of one file, so a save
  // from either dialog leaves both stale: the next dialog opened from either
  // would be refused if only the card that saved were read again.
  it("reads the notes again after the options are saved", async () => {
    const user = userEvent.setup();
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [optionsUrl("pve-01")]: options(),
      [notesUrl("pve-01")]: notes(),
    });
    renderPage();

    await user.click(
      await screen.findByRole("button", { name: "Edit node options" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Edit Options - pve-01",
    });
    expect(reads(optionsUrl("pve-01"))).toBe(1);
    expect(reads(notesUrl("pve-01"))).toBe(1);
    const delay = within(dialog).getByLabelText(
      "Start on boot delay (seconds)",
    );
    await user.clear(delay);
    await user.type(delay, "31");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    expect(api.writes()).toEqual([`PUT ${optionsUrl("pve-01")}`]);
    expect(reads(optionsUrl("pve-01"))).toBe(2);
    expect(reads(notesUrl("pve-01"))).toBe(2);
  });

  it("keeps the options dialog open until the notes are read again too", async () => {
    const user = userEvent.setup();
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [optionsUrl("pve-01")]: options(),
      [notesUrl("pve-01")]: notes(),
    });
    // The notes read that follows the save is held; the first one is not.
    let release = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    let heldNotes = 0;
    const inner = globalThis.fetch;
    vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
      if ((init?.method ?? "GET") === "GET" && input === notesUrl("pve-01")) {
        heldNotes += 1;
        if (heldNotes > 1) return gate.then(() => inner(input, init));
      }
      return inner(input, init);
    });
    renderPage();

    await user.click(
      await screen.findByRole("button", { name: "Edit node options" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Edit Options - pve-01",
    });
    const delay = within(dialog).getByLabelText(
      "Start on boot delay (seconds)",
    );
    await user.clear(delay);
    await user.type(delay, "31");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(heldNotes).toBe(2);
    });
    await new Promise((resolve) => setTimeout(resolve, 50));

    // The options are read again and the notes are not yet: the save is not
    // done, whichever card it was made from.
    expect(
      within(dialog).getByRole("button", { name: "Saving..." }),
    ).toBeDisabled();
    expect(
      screen.getByRole("dialog", { name: "Edit Options - pve-01" }),
    ).toBeInTheDocument();

    release();
    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
  });

  it("reads the options again after the notes are saved", async () => {
    const user = userEvent.setup();
    api = stubApi({
      [NODES]: listOf([node("n1", "pve-01")]),
      [optionsUrl("pve-01")]: options(),
      [notesUrl("pve-01")]: notes(),
    });
    renderPage();

    await user.click(
      await screen.findByRole("button", { name: "Edit node notes" }),
    );
    const dialog = await screen.findByRole("dialog", {
      name: "Edit Notes - pve-01",
    });
    await user.type(within(dialog).getByLabelText("Notes"), " and more");
    await user.click(within(dialog).getByRole("button", { name: "Save" }));

    await waitFor(() => {
      expect(screen.queryByRole("dialog")).toBeNull();
    });
    // Notes are written through the options route.
    expect(api.writes()).toEqual([`PUT ${optionsUrl("pve-01")}`]);
    expect(reads(notesUrl("pve-01"))).toBe(2);
    expect(reads(optionsUrl("pve-01"))).toBe(2);
  });
});
