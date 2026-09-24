import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { toast } from "sonner";
import { listOf, stubApi } from "@/test/fetch-stub";
import { FirewallRulesTable } from "./FirewallRulesTable";
import type { FirewallRule } from "../types/network";

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

const CLUSTER = "c1";
const RULES_PATH = `/api/v1/clusters/${CLUSTER}/firewall/rules`;
const RULES_KEY = ["firewall", "rules", CLUSTER];
// The digest of the list below. Proxmox stamps one digest of the whole list
// onto every rule of a listing, so every fixture rule carries it.
const DIGEST = "0123456789abcdef0123456789abcdef01234567";

// Rules 0 and 1 differ in every field the dialog shows, so a dialog naming
// the wrong one is caught. Rules 2 and 3 differ ONLY in iface and log.
const RULES: FirewallRule[] = [
  {
    pos: 0,
    type: "in",
    action: "ACCEPT",
    macro: "SSH",
    source: "192.0.2.10",
    enable: 1,
    comment: "admin ssh",
    digest: DIGEST,
  },
  {
    pos: 1,
    type: "out",
    action: "DROP",
    proto: "tcp",
    dest: "192.0.2.20",
    dport: "8006",
    enable: 0,
    comment: "block gui",
    digest: DIGEST,
  },
  {
    pos: 2,
    type: "in",
    action: "DROP",
    iface: "net0",
    enable: 1,
    digest: DIGEST,
  },
  {
    pos: 3,
    type: "in",
    action: "DROP",
    iface: "net1",
    log: "info",
    enable: 1,
    digest: DIGEST,
  },
];

let api: ReturnType<typeof stubApi>;
let qc: QueryClient;

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

async function openDeleteFor(pos: number, rules: FirewallRule[] = RULES) {
  api = stubApi({ [RULES_PATH]: listOf(rules) });
  qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const user = userEvent.setup();
  render(
    <QueryClientProvider client={qc}>
      <FirewallRulesTable clusterId={CLUSTER} />
    </QueryClientProvider>,
  );
  await user.click(
    await screen.findByRole("button", { name: `Delete rule ${String(pos)}` }),
  );
  return { user, dialog: await screen.findByRole("alertdialog") };
}

describe("FirewallRulesTable — deleting a rule", () => {
  it("asks first, naming the rule at that position, and sends nothing", async () => {
    const { dialog } = await openDeleteFor(1);

    expect(
      within(dialog).getByRole("heading", {
        name: "Delete firewall rule #1?",
      }),
    ).toBeInTheDocument();
    expect(dialog).toHaveTextContent(
      'out DROP, proto tcp, source any, dest 192.0.2.20, dport 8006, disabled, comment "block gui"',
    );
    expect(dialog).not.toHaveTextContent("admin ssh");
    expect(dialog).toHaveTextContent(
      "Rules are deleted by position — this is position 1 of the rule list of the cluster. Nexara sends Proxmox a check that the list is unchanged",
    );
    expect(api.writes()).toEqual([]);
  });

  it("tells apart two rules that differ only in interface and log level", async () => {
    const { dialog } = await openDeleteFor(3);

    expect(dialog).toHaveTextContent(
      "in DROP, source any, dest any, iface net1, log info",
    );
    expect(dialog).not.toHaveTextContent("net0");
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
      expect(api.writes()).toEqual([`DELETE ${RULES_PATH}/1?digest=${DIGEST}`]);
    });
    expect(toast.error).not.toHaveBeenCalled();
  });

  // The digest is what makes a positional delete safe, so a row without one
  // is not sent at all: an unconditional delete would remove whatever rule
  // sits at that position now.
  it("sends nothing for a rule that came without the list's digest, and says so", async () => {
    const { user, dialog } = await openDeleteFor(
      1,
      RULES.map((r) => ({ ...r, digest: "" })),
    );

    await user.click(
      within(dialog).getByRole("button", { name: "Delete Rule" }),
    );

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledTimes(1);
    });
    expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain(
      "Nothing was sent: this rule came without the rule list's digest",
    );
    expect(api.writes()).toEqual([]);
  });

  // Proxmox refused the delete because the list changed since it was loaded
  // (the server's 409). Nothing was deleted; the operator is told so and the
  // list is reloaded.
  it("on a 409 says the list changed and reloads it", async () => {
    const { user, dialog } = await openDeleteFor(1);
    const gets = () => api.sent.filter((r) => r === `GET ${RULES_PATH}`).length;
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
      if ((init?.method ?? "GET") === "GET" && input === RULES_PATH) {
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
      "Nothing was deleted: the rule list of the cluster changed since it was loaded, so Proxmox refused the change. The list has been reloaded — check it and try again.",
    );
    await waitFor(() => {
      expect(held).toBe(1);
    });
    // Give React time to render whatever a settled mutation would render.
    await new Promise((r) => setTimeout(r, 100));
    expect(
      screen.getByRole("button", { name: "Delete rule 0" }),
    ).toBeDisabled();

    release();
    await waitFor(() => {
      expect(gets()).toBe(before + 1);
    });
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Delete rule 0" }),
      ).toBeEnabled();
    });
    expect(api.writes()).toEqual([`DELETE ${RULES_PATH}/1?digest=${DIGEST}`]);
  });

  // The client-side check: if the cached list changed under the open dialog,
  // position 1 may now hold another rule, and nothing is sent.
  it.each([
    [
      "holds a different rule",
      RULES.map((r) => (r.pos === 1 ? { ...r, comment: "another rule" } : r)),
    ],
    ["is gone", RULES.filter((r) => r.pos !== 1)],
    // Same rule at position 1, but the list changed elsewhere: its digest
    // moved on, so Proxmox would refuse the delete anyway.
    [
      "belongs to a list that changed elsewhere",
      RULES.map((r) => ({
        ...r,
        digest: "fedcba9876543210fedcba9876543210fedcba98",
      })),
    ],
  ])(
    "refuses to send when position 1 now %s, and says so",
    async (_, changed) => {
      const { user, dialog } = await openDeleteFor(1);

      qc.setQueryData(RULES_KEY, changed);
      await user.click(
        within(dialog).getByRole("button", { name: "Delete Rule" }),
      );

      await waitFor(() => {
        expect(toast.error).toHaveBeenCalledTimes(1);
      });
      expect(vi.mocked(toast.error).mock.calls[0]?.[0]).toContain(
        "Nothing was deleted: the rule list of the cluster changed",
      );
      expect(api.writes()).toEqual([]);
    },
  );

  // After a delete every later rule moves up one, so a Delete clicked on
  // the old list names the wrong rule. The buttons stay disabled until the
  // refetch lands.
  it("keeps the Delete buttons disabled until the list is refetched", async () => {
    const { user, dialog } = await openDeleteFor(1);

    let release = () => {};
    const gate = new Promise<void>((r) => {
      release = r;
    });
    let held = 0;
    const inner = globalThis.fetch;
    vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
      if ((init?.method ?? "GET") === "GET" && input === RULES_PATH) {
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
      screen.getByRole("button", { name: "Delete rule 0" }),
    ).toBeDisabled();

    release();
    await waitFor(() => {
      expect(
        screen.getByRole("button", { name: "Delete rule 0" }),
      ).toBeEnabled();
    });
  });
});
