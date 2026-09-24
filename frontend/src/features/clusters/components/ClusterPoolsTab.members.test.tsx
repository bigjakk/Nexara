import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ClusterPoolsTab } from "./ClusterPoolsTab";
import type { ResourcePoolDetail } from "@/features/pools/api/pool-queries";

// Removing a member from a pool confirms first. Unlike ClusterPoolsTab.test,
// this file watches the requests that actually leave through fetch.

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

const CLUSTER = "cccccccc-0000-0000-0000-000000000003";
const POOLS_PATH = `/api/v1/clusters/${CLUSTER}/pools`;

const POOL: ResourcePoolDetail = {
  poolid: "pool01",
  members: [
    {
      id: "qemu/101",
      node: "pve-01",
      type: "qemu",
      vmid: 101,
      name: "linux01",
    },
    {
      id: "qemu/102",
      node: "pve-02",
      type: "qemu",
      vmid: 102,
      name: "linux02",
    },
    {
      id: "storage/pve-01/store01",
      node: "pve-01",
      type: "storage",
      storage: "store01",
    },
    {
      id: "storage/pve-01/store02",
      node: "pve-01",
      type: "storage",
      storage: "store02",
    },
  ],
};

/** Every request other than a GET, as "METHOD path body". */
let writes: string[] = [];

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
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
        const body = typeof init?.body === "string" ? init.body : "";
        writes.push(`${method} ${url} ${body}`);
        return Promise.resolve(json(200, { status: "ok" }));
      }
      if (url === POOLS_PATH) {
        const items = [{ poolid: "pool01", comment: "web tier" }];
        return Promise.resolve(json(200, { items, total: items.length }));
      }
      if (url === `${POOLS_PATH}/pool01`)
        return Promise.resolve(json(200, POOL));
      return Promise.resolve(json(200, { items: [], total: 0 }));
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function openRemove(button: string) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  render(<ClusterPoolsTab clusterId={CLUSTER} />, { wrapper });
  const user = userEvent.setup();
  await user.click(await screen.findByText("web tier"));
  await user.click(await screen.findByRole("button", { name: button }));
  const dialog = await screen.findByRole("alertdialog");
  return { user, dialog };
}

// The second guest and the second storage, so a removal aimed at the first
// member of either kind would show.
const SITES = [
  {
    what: "a guest",
    button: "Remove linux02",
    title: "Remove linux02 (102) from pool pool01?",
    says: "The guest itself is not changed",
    sends: `PUT ${POOLS_PATH}/pool01 ${JSON.stringify({ vms: "102", delete: "1" })}`,
  },
  {
    what: "a storage",
    button: "Remove store02",
    title: "Remove storage store02 from pool pool01?",
    says: "The storage itself is not changed",
    sends: `PUT ${POOLS_PATH}/pool01 ${JSON.stringify({ storage: "store02", delete: "1" })}`,
  },
];

describe("ClusterPoolsTab — removing a pool member", () => {
  it.each(SITES)(
    "asks before removing $what, naming it, and sends nothing yet",
    async (site) => {
      const { dialog } = await openRemove(site.button);

      expect(
        within(dialog).getByRole("heading", { name: site.title }),
      ).toBeInTheDocument();
      expect(dialog).toHaveTextContent(site.says);
      expect(dialog).toHaveTextContent(
        "a user or token that reaches it only through this pool loses that access",
      );
      expect(writes).toEqual([]);
    },
  );

  it.each(SITES)(
    "sends nothing when removing $what is cancelled",
    async (site) => {
      const { user, dialog } = await openRemove(site.button);

      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
      await waitFor(() => {
        expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      });
      expect(writes).toEqual([]);
    },
  );

  it.each(SITES)(
    "removes exactly the $what it was opened for, once, on Remove",
    async (site) => {
      const { user, dialog } = await openRemove(site.button);

      await user.click(within(dialog).getByRole("button", { name: "Remove" }));
      await waitFor(() => {
        expect(writes).toEqual([site.sends]);
      });
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    },
  );
});
