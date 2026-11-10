import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { QueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { createTestQueryClient, renderWithProviders } from "@/test/test-utils";
import { bodiesOf, listOf, stubApi } from "@/test/fetch-stub";
import { setupUser } from "@/test/user";
import { FirewallTab, NetworkTab, ServicesTab } from "./NodeDetailPage";

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());

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
    qc = createTestQueryClient({ keepCache: true });
    const user = userEvent.setup();
    renderWithProviders(<FirewallTab clusterId={CLUSTER} nodeName={NODE} />, {
      client: qc,
    });
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

// The node tabs' disruptive one-click actions now ask first. Each case opens
// the confirmation, checks it names the node and what happens, and that the
// request goes out only on confirm.
describe("NodeDetailPage Network tab — Apply Config and Revert", () => {
  const NET = `/api/v1/clusters/${CLUSTER}/networks/${NODE}`;

  it.each([
    {
      button: "Apply Config",
      title: `Apply the pending network changes on ${NODE}?`,
      says: "can lose its network connection, Nexara's included",
      sends: `POST ${NET}/apply`,
    },
    {
      button: "Revert",
      title: `Discard the pending network changes on ${NODE}?`,
      says: "The running network does not change.",
      sends: `POST ${NET}/revert`,
    },
  ])(
    "$button asks first and sends only on confirm",
    async ({ button, title, says, sends }) => {
      api = stubApi({ [NET]: listOf([]) });
      const user = setupUser();
      renderWithProviders(<NetworkTab clusterId={CLUSTER} nodeName={NODE} />);

      await user.click(await screen.findByRole("button", { name: button }));
      const dialog = await screen.findByRole("alertdialog");
      expect(
        within(dialog).getByRole("heading", { name: title }),
      ).toBeInTheDocument();
      expect(dialog).toHaveTextContent(says);
      expect(api.writes()).toEqual([]);

      await user.click(within(dialog).getByRole("button", { name: button }));
      await waitFor(() => {
        expect(api.writes()).toEqual([sends]);
      });
    },
  );
});

describe("NodeDetailPage Firewall tab — quick-add rules", () => {
  const RULES = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/firewall/rules`;

  it.each([
    {
      button: "+ Drop Rule",
      title: `Drop all inbound traffic to ${NODE}?`,
      says: "this can cut off access to the node",
      confirm: "Add Drop Rule",
      action: "DROP",
    },
    {
      button: "+ Accept Rule",
      title: `Accept all inbound traffic to ${NODE}?`,
      says: "it lets every inbound connection to the node through",
      confirm: "Add Accept Rule",
      action: "ACCEPT",
    },
  ])(
    "$button asks first, saying what the rule does, and adds it only on confirm",
    async ({ button, title, says, confirm, action }) => {
      api = stubApi({ [RULES]: listOf([]) });
      const user = setupUser();
      renderWithProviders(<FirewallTab clusterId={CLUSTER} nodeName={NODE} />);

      await user.click(await screen.findByRole("button", { name: button }));
      const dialog = await screen.findByRole("alertdialog");
      expect(
        within(dialog).getByRole("heading", { name: title }),
      ).toBeInTheDocument();
      expect(dialog).toHaveTextContent(says);
      expect(api.writes()).toEqual([]);

      await user.click(within(dialog).getByRole("button", { name: confirm }));
      await waitFor(() => {
        expect(api.writes()).toEqual([`POST ${RULES}`]);
      });
      expect(bodiesOf("POST", RULES)).toEqual([
        { type: "in", action, enable: 1 },
      ]);
    },
  );
});

describe("NodeDetailPage Services tab — Stop and Restart", () => {
  const SERVICES = `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/services`;

  // The second row is the one acted on, so an action sent for the first
  // shows.
  it.each([
    {
      button: "Stop",
      says: "it stays stopped until it is started again",
      sends: `POST ${SERVICES}/pvestatd/stop`,
    },
    {
      button: "Restart",
      says: "interrupted while it restarts",
      sends: `POST ${SERVICES}/pvestatd/restart`,
    },
  ])(
    "$button asks first and sends only on confirm",
    async ({ button, says, sends }) => {
      api = stubApi({
        [SERVICES]: listOf([
          { service: "cron", name: "cron", desc: "Cron", state: "running" },
          {
            service: "pvestatd",
            name: "pvestatd",
            desc: "PVE Status Daemon",
            state: "running",
          },
        ]),
      });
      const user = setupUser();
      renderWithProviders(<ServicesTab clusterId={CLUSTER} nodeName={NODE} />);

      const row = (await screen.findByText("pvestatd")).closest("tr");
      if (!(row instanceof HTMLElement)) throw new Error("no row for pvestatd");
      await user.click(within(row).getByRole("button", { name: button }));
      const dialog = await screen.findByRole("alertdialog");
      expect(
        within(dialog).getByRole("heading", {
          name: `${button} pvestatd on ${NODE}?`,
        }),
      ).toBeInTheDocument();
      expect(dialog).toHaveTextContent(says);
      expect(api.writes()).toEqual([]);

      await user.click(within(dialog).getByRole("button", { name: button }));
      await waitFor(() => {
        expect(api.writes()).toEqual([sends]);
      });
    },
  );

  // Proxmox's stop task refuses pveproxy, pvedaemon and pve-cluster, so a
  // Stop there could only fail after a confirmation saying it would stop.
  it("offers Restart but no Stop for a service Proxmox will not stop", async () => {
    api = stubApi({
      [SERVICES]: listOf([
        {
          service: "pveproxy",
          name: "pveproxy",
          desc: "PVE API Proxy Server",
          state: "running",
        },
      ]),
    });
    renderWithProviders(<ServicesTab clusterId={CLUSTER} nodeName={NODE} />);

    const row = (await screen.findByText("pveproxy")).closest("tr");
    if (!(row instanceof HTMLElement)) throw new Error("no row for pveproxy");
    expect(
      within(row).getByRole("button", { name: "Restart" }),
    ).toBeInTheDocument();
    expect(
      within(row).queryByRole("button", { name: "Stop" }),
    ).not.toBeInTheDocument();
  });
});
