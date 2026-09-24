import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ClusterHATab } from "./ClusterHATab";
import type {
  HAGroup,
  HAResource,
  HARuleEntry,
} from "@/features/ha/api/ha-queries";

// Every delete in the HA tab confirms first. These tests watch the requests
// that actually leave through fetch, so a button that deletes on its own
// click shows up as a DELETE sent before the dialog was confirmed.

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

const CLUSTER = "cccccccc-0000-0000-0000-000000000003";
const BASE = `/api/v1/clusters/${CLUSTER}/ha`;
const PVE9 = "9.0.6";
const PVE8 = "8.4.1";

const RESOURCES: HAResource[] = [
  { sid: "vm:101", type: "vm", state: "started", group: "", status: "" },
  { sid: "vm:102", type: "vm", state: "started", group: "", status: "" },
];
const RULES: HARuleEntry[] = [
  {
    rule: "rule01",
    type: "node-affinity",
    resources: "vm:101",
    nodes: "pve-01",
  },
  {
    rule: "rule02",
    type: "resource-affinity",
    resources: "vm:101,vm:102",
    affinity: "negative",
  },
  {
    rule: "rule03",
    type: "node-affinity",
    resources: "vm:102",
    nodes: "pve-02",
    disable: 1,
  },
];
const GROUPS: HAGroup[] = [
  { group: "group01", nodes: "pve-01", restricted: 0, nofailback: 0 },
  { group: "group02", nodes: "pve-02", restricted: 0, nofailback: 0 },
];

/** Every request other than a GET, as "METHOD path". */
let writes: string[] = [];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function list(items: unknown[]) {
  return json(200, { items, total: items.length });
}

beforeEach(() => {
  writes = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url =
        typeof input === "string"
          ? input
          : input instanceof URL
            ? input.href
            : input.url;
      if (url === "/api/v1/auth/refresh") {
        return Promise.resolve(new Response("{}", { status: 401 }));
      }
      const method = init?.method ?? "GET";
      if (method !== "GET") {
        writes.push(`${method} ${url}`);
        return Promise.resolve(json(200, { status: "ok" }));
      }
      if (url === `${BASE}/resources`) return Promise.resolve(list(RESOURCES));
      if (url === `${BASE}/rules`) return Promise.resolve(list(RULES));
      if (url === `${BASE}/groups`) return Promise.resolve(list(GROUPS));
      if (url === `${BASE}/manager-status`)
        return Promise.resolve(json(200, {}));
      return Promise.resolve(list([]));
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

function renderTab(pveVersion: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  render(<ClusterHATab clusterId={CLUSTER} pveVersion={pveVersion} />, {
    wrapper,
  });
  return userEvent.setup();
}

interface Site {
  what: string;
  pveVersion: string;
  tab: string;
  /** The row the tests delete — the second, so a first-row delete shows. */
  button: string;
  title: string;
  says: string[];
  sends: string;
}

const SITES: Site[] = [
  {
    what: "an HA resource",
    pveVersion: PVE9,
    tab: "Resources",
    button: "Delete resource vm:102",
    title: "Delete HA resource vm:102?",
    says: [
      "Proxmox stops managing vm:102 with HA",
      "if it is running, it keeps running where it is",
      "take it out of every HA rule that lists it",
    ],
    sends: `DELETE ${BASE}/resources/vm%3A102`,
  },
  {
    what: "an HA rule",
    pveVersion: PVE9,
    tab: "Groups / Rules",
    button: "Delete rule rule02",
    title: "Delete HA rule rule02?",
    says: [
      "deletes this resource-affinity rule",
      "HA stops applying it to vm:101,vm:102",
      "stay HA-managed",
    ],
    sends: `DELETE ${BASE}/rules/rule02`,
  },
  {
    what: "an HA group",
    pveVersion: PVE8,
    tab: "Groups / Rules",
    button: "Delete group group02",
    title: "Delete HA group group02?",
    says: ["refuses while any HA resource is still assigned to the group"],
    sends: `DELETE ${BASE}/groups/group02`,
  },
];

async function openDialog(site: Site) {
  const user = renderTab(site.pveVersion);
  await user.click(screen.getByRole("tab", { name: site.tab }));
  await user.click(await screen.findByRole("button", { name: site.button }));
  const dialog = await screen.findByRole("alertdialog");
  return { user, dialog };
}

describe("ClusterHATab — deletes confirm first", () => {
  it.each(SITES)(
    "asks before deleting $what, naming it, and sends nothing yet",
    async (site) => {
      const { dialog } = await openDialog(site);

      expect(
        within(dialog).getByRole("heading", { name: site.title }),
      ).toBeInTheDocument();
      for (const s of site.says) expect(dialog).toHaveTextContent(s);
      expect(writes).toEqual([]);
    },
  );

  it.each(SITES)(
    "sends nothing when $what's delete is cancelled",
    async (site) => {
      const { user, dialog } = await openDialog(site);

      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
      await waitFor(() => {
        expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      });
      expect(writes).toEqual([]);
    },
  );

  it.each(SITES)(
    "deletes exactly the $what it was opened for, once, on Delete",
    async (site) => {
      const { user, dialog } = await openDialog(site);

      await user.click(within(dialog).getByRole("button", { name: "Delete" }));
      await waitFor(() => {
        expect(writes).toEqual([site.sends]);
      });
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    },
  );

  // A disabled rule is not being applied now, so deleting it cannot be what
  // stops HA applying it; the enabled rule02 above is the positive twin.
  it("says a disabled rule is not being applied now", async () => {
    const user = renderTab(PVE9);
    await user.click(screen.getByRole("tab", { name: "Groups / Rules" }));
    await user.click(
      await screen.findByRole("button", { name: "Delete rule rule03" }),
    );
    const dialog = await screen.findByRole("alertdialog");

    expect(dialog).toHaveTextContent(
      "It is disabled, so HA is not applying it to vm:102 now.",
    );
    expect(dialog).not.toHaveTextContent("HA stops applying");
    expect(writes).toEqual([]);
  });
});
