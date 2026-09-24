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
  },
  { pos: 2, type: "in", action: "DROP", iface: "net0", enable: 1 },
  {
    pos: 3,
    type: "in",
    action: "DROP",
    iface: "net1",
    log: "info",
    enable: 1,
  },
];

let api: ReturnType<typeof stubApi>;
let qc: QueryClient;

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

async function openDeleteFor(pos: number) {
  api = stubApi({ [RULES_PATH]: listOf(RULES) });
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
      "Proxmox deletes whichever rule is at position 1 of the rule list of the cluster",
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

  it("confirming sends one DELETE for the rule the dialog named", async () => {
    const { user, dialog } = await openDeleteFor(1);

    await user.click(
      within(dialog).getByRole("button", { name: "Delete Rule" }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([`DELETE ${RULES_PATH}/1`]);
    });
    expect(toast.error).not.toHaveBeenCalled();
  });

  // The rule is deleted by position with no digest. If the cached list
  // changed under the open dialog, position 1 may now hold another rule.
  it.each([
    [
      "holds a different rule",
      RULES.map((r) => (r.pos === 1 ? { ...r, comment: "another rule" } : r)),
    ],
    ["is gone", RULES.filter((r) => r.pos !== 1)],
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
