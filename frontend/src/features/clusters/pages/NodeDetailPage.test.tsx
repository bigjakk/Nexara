import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { toast } from "sonner";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { FirewallTab, NetworkTab } from "./NodeDetailPage";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const CLUSTER = "c1";
const NODE = "pve-01";

let api: ReturnType<typeof stubApi>;

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

describe("NodeDetailPage Network tab — deleting an interface", () => {
  const NET = `/api/v1/clusters/${CLUSTER}/networks/${NODE}`;

  async function openDeleteFor(iface: string) {
    api = stubApi({
      [NET]: listOf([
        { iface: "vmbr0", type: "bridge", active: 1, autostart: 1 },
        { iface: "vmbr1", type: "bridge", active: 1, autostart: 1 },
      ]),
    });
    const user = userEvent.setup();
    renderWithProviders(<NetworkTab clusterId={CLUSTER} nodeName={NODE} />);
    await user.click(
      await screen.findByRole("button", { name: `Delete ${iface}` }),
    );
    return { user, dialog: await screen.findByRole("alertdialog") };
  }

  it("asks first, naming the interface, and sends nothing", async () => {
    const { dialog } = await openDeleteFor("vmbr1");

    expect(
      within(dialog).getByRole("heading", {
        name: `Delete interface vmbr1 on ${NODE}?`,
      }),
    ).toBeInTheDocument();
    expect(dialog).toHaveTextContent(
      `Proxmox removes the configuration of vmbr1 from the pending network configuration of ${NODE}.`,
    );
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const { user, dialog } = await openDeleteFor("vmbr1");

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming sends one DELETE for that interface", async () => {
    const { user, dialog } = await openDeleteFor("vmbr1");

    await user.click(
      within(dialog).getByRole("button", { name: "Delete Interface" }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([`DELETE ${NET}/vmbr1`]);
    });
  });
});

describe("NodeDetailPage Firewall tab — deleting a rule", () => {
  const RULES = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/firewall/rules`;
  const RULES_KEY = ["clusters", CLUSTER, "nodes", NODE, "firewall", "rules"];
  // One digest of the whole list, stamped on every rule, as Proxmox lists it.
  const DIGEST = "89abcdef0123456789abcdef0123456789abcdef";
  const LIST = [
    {
      pos: 0,
      type: "in",
      action: "ACCEPT",
      macro: "SSH",
      enable: 1,
      comment: "admin ssh",
      digest: DIGEST,
    },
    {
      pos: 1,
      type: "in",
      action: "DROP",
      proto: "udp",
      source: "192.0.2.50",
      dport: "161",
      enable: 1,
      comment: "no snmp",
      digest: DIGEST,
    },
  ];
  let qc: QueryClient;

  async function openDeleteFor(pos: number) {
    api = stubApi({ [RULES]: listOf(LIST) });
    qc = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
        mutations: { retry: false },
      },
    });
    const user = userEvent.setup();
    render(
      <QueryClientProvider client={qc}>
        <MemoryRouter>
          <FirewallTab clusterId={CLUSTER} nodeName={NODE} />
        </MemoryRouter>
      </QueryClientProvider>,
    );
    await user.click(
      await screen.findByRole("button", {
        name: `Delete firewall rule ${String(pos)}`,
      }),
    );
    return { user, dialog: await screen.findByRole("alertdialog") };
  }

  it("asks first, naming the rule at that position, and sends nothing", async () => {
    const { dialog } = await openDeleteFor(1);

    expect(
      within(dialog).getByRole("heading", {
        name: "Delete firewall rule #1?",
      }),
    ).toBeInTheDocument();
    expect(dialog).toHaveTextContent(
      'in DROP, proto udp, source 192.0.2.50, dest any, dport 161, comment "no snmp"',
    );
    expect(dialog).not.toHaveTextContent("admin ssh");
    expect(dialog).toHaveTextContent(`the rule list of node ${NODE}`);
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const { user, dialog } = await openDeleteFor(1);

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming sends one DELETE for the rule the dialog named, carrying the list's digest", async () => {
    const { user, dialog } = await openDeleteFor(1);

    await user.click(
      within(dialog).getByRole("button", { name: "Delete Rule" }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([`DELETE ${RULES}/1?digest=${DIGEST}`]);
    });
    expect(toast.error).not.toHaveBeenCalled();
  });

  it("on a 409 says the node's list changed and reloads it", async () => {
    const { user, dialog } = await openDeleteFor(1);
    const gets = () => api.sent.filter((r) => r === `GET ${RULES}`).length;
    const before = gets();
    // The reload is held until released, to see the Delete buttons stay
    // disabled while it is in flight: the mutation settles only once the
    // fresh list is in, since every position may have moved.
    let release = () => {};
    const gate = new Promise<void>((r) => {
      release = r;
    });
    let held = 0;
    const inner = globalThis.fetch;
    vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
      if ((init?.method ?? "GET") === "GET" && input === RULES) {
        held++;
        return gate.then(() => inner(input, init));
      }
      if (init?.method === "DELETE") {
        // apiClient always passes the path as a string.
        api.sent.push(`DELETE ${typeof input === "string" ? input : "?"}`);
        return Promise.resolve(
          new Response(
            JSON.stringify({
              error: "conflict",
              message: "The firewall rule list changed since it was loaded",
            }),
            { status: 409, headers: { "Content-Type": "application/json" } },
          ),
        );
      }
      return inner(input, init);
    });

    await user.click(
      within(dialog).getByRole("button", { name: "Delete Rule" }),
    );

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledTimes(1);
    });
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toBe(
      `Nothing was deleted: the rule list of node ${NODE} changed since it was loaded, so Proxmox refused the change. The list has been reloaded — check it and try again.`,
    );
    await waitFor(() => {
      expect(held).toBe(1);
    });
    // Give React time to render whatever a settled mutation would render.
    await new Promise((r) => setTimeout(r, 100));
    expect(
      screen.getByRole("button", { name: "Delete firewall rule 0" }),
    ).toBeDisabled();

    release();
    await waitFor(() => {
      expect(gets()).toBe(before + 1);
    });
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Delete firewall rule 0" }),
      ).toBeEnabled();
    });
    expect(api.writes()).toEqual([`DELETE ${RULES}/1?digest=${DIGEST}`]);
  });

  it("refuses to send when position 1 now holds a different rule, and says so", async () => {
    const { user, dialog } = await openDeleteFor(1);

    qc.setQueryData(
      RULES_KEY,
      LIST.map((r) => (r.pos === 1 ? { ...r, dport: "162" } : r)),
    );
    await user.click(
      within(dialog).getByRole("button", { name: "Delete Rule" }),
    );

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledTimes(1);
    });
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain(
      `Nothing was deleted: the rule list of node ${NODE} changed`,
    );
    expect(api.writes()).toEqual([]);
  });

  it("keeps the Delete buttons disabled until the list is refetched", async () => {
    const { user, dialog } = await openDeleteFor(1);

    let release = () => {};
    const gate = new Promise<void>((r) => {
      release = r;
    });
    let held = 0;
    const inner = globalThis.fetch;
    vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
      if ((init?.method ?? "GET") === "GET" && input === RULES) {
        held++;
        return gate.then(() => inner(input, init));
      }
      return inner(input, init);
    });

    await user.click(
      within(dialog).getByRole("button", { name: "Delete Rule" }),
    );
    await waitFor(() => {
      expect(held).toBe(1);
    });
    // The refetch is in flight; give React time to render whatever the
    // settled delete would render.
    await new Promise((r) => setTimeout(r, 100));
    expect(
      screen.getByRole("button", { name: "Delete firewall rule 0" }),
    ).toBeDisabled();

    release();
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Delete firewall rule 0" }),
      ).toBeEnabled();
    });
  });
});
