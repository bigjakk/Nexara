import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { toast } from "sonner";
import { queryClient } from "@/lib/query-client";

import { ClusterPoolsTab } from "./ClusterPoolsTab";
import type { ResourcePoolDetail } from "@/features/pools/api/pool-queries";

// Removing a member from a pool confirms first. Unlike ClusterPoolsTab.test,
// this file watches the requests that actually leave through fetch.

// The app's mutation-error net (lib/query-client.ts) toasts through sonner, so
// this mock sees every toast a removal can raise.
vi.mock("sonner", () => ({
  toast: { success: vi.fn(), warning: vi.fn(), error: vi.fn() },
}));

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
/** How many times the pool's members were read. */
let poolReads = 0;
/** What a write is answered with; a test swaps it to fail or to hold. */
let answerWrite: () => Promise<Response>;
/** What a read of the pool's members is answered with; a test can hold it. */
let answerPoolRead: () => Promise<Response>;

function json(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

beforeEach(() => {
  writes = [];
  poolReads = 0;
  answerWrite = () => Promise.resolve(json(200, { status: "ok" }));
  answerPoolRead = () => Promise.resolve(json(200, POOL));
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
        return answerWrite();
      }
      if (url === POOLS_PATH) {
        const items = [{ poolid: "pool01", comment: "web tier" }];
        return Promise.resolve(json(200, { items, total: items.length }));
      }
      if (url === `${POOLS_PATH}/pool01`) {
        poolReads++;
        return answerPoolRead();
      }
      return Promise.resolve(json(200, { items: [], total: 0 }));
    }),
  );
});

afterEach(() => {
  queryClient.clear();
  vi.clearAllMocks();
  vi.unstubAllGlobals();
});

// The app's own QueryClient, so a failed removal reaches the operator through
// the same mutation-error toast it does in production.
async function openRemove(button: string) {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
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

  it.each(SITES)(
    "shows the server's message once and keeps $what listed when removing it fails",
    async (site) => {
      const message = "pool01: cannot update pool - permission denied";
      answerWrite = () =>
        Promise.resolve(json(403, { error: "forbidden", message }));
      const { user, dialog } = await openRemove(site.button);

      await user.click(within(dialog).getByRole("button", { name: "Remove" }));
      await waitFor(() => {
        expect(vi.mocked(toast.error)).toHaveBeenCalledWith(message);
      });
      expect(writes).toEqual([site.sends]);
      expect(vi.mocked(toast.error)).toHaveBeenCalledTimes(1);
      expect(vi.mocked(toast.success)).not.toHaveBeenCalled();
      // Still listed, and removable again.
      expect(screen.getByRole("button", { name: site.button })).toBeEnabled();
    },
  );

  it.each(SITES)(
    "refuses a second removal while removing $what is in flight",
    async (site) => {
      let release: (r: Response) => void = () => undefined;
      answerWrite = () =>
        new Promise<Response>((resolve) => {
          release = resolve;
        });
      const { user, dialog } = await openRemove(site.button);

      await user.click(within(dialog).getByRole("button", { name: "Remove" }));
      await waitFor(() => {
        expect(writes).toEqual([site.sends]);
      });
      // Every member's Remove button, the one just confirmed included.
      for (const name of [
        "Remove linux01",
        "Remove linux02",
        "Remove store01",
        "Remove store02",
      ]) {
        await waitFor(() => {
          expect(screen.getByRole("button", { name })).toBeDisabled();
        });
      }
      await user.click(screen.getByRole("button", { name: site.button }));
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();

      release(json(200, { status: "ok" }));
      await waitFor(() => {
        expect(screen.getByRole("button", { name: site.button })).toBeEnabled();
      });
      expect(writes).toEqual([site.sends]);
    },
  );

  it.each(SITES)(
    "re-reads the pool's members once removing $what succeeds",
    async (site) => {
      const { user, dialog } = await openRemove(site.button);
      const readsBefore = poolReads;

      await user.click(within(dialog).getByRole("button", { name: "Remove" }));
      await waitFor(() => {
        expect(poolReads).toBe(readsBefore + 1);
      });
      // Let everything go idle, give a late second read time to start, and
      // count again: exactly one re-read, not one so far.
      await waitFor(() => {
        expect(queryClient.isFetching() + queryClient.isMutating()).toBe(0);
      });
      await new Promise((r) => setTimeout(r, 100));
      await waitFor(() => {
        expect(queryClient.isFetching() + queryClient.isMutating()).toBe(0);
      });
      expect(poolReads).toBe(readsBefore + 1);
      expect(writes).toEqual([site.sends]);
      expect(vi.mocked(toast.error)).not.toHaveBeenCalled();
    },
  );

  it.each(SITES)(
    "keeps every Remove button disabled until the pool is re-read after removing $what",
    async (site) => {
      const { user, dialog } = await openRemove(site.button);
      const gone = site.button.slice("Remove ".length);
      let release: () => void = () => undefined;
      answerPoolRead = () =>
        new Promise<Response>((resolve) => {
          release = () => {
            resolve(
              json(200, {
                ...POOL,
                members: (POOL.members ?? []).filter(
                  (m) => (m.name ?? m.storage) !== gone,
                ),
              }),
            );
          };
        });
      const readsBefore = poolReads;

      await user.click(within(dialog).getByRole("button", { name: "Remove" }));
      // The PUT has answered and the re-read is out, held.
      await waitFor(() => {
        expect(poolReads).toBe(readsBefore + 1);
      });
      expect(writes).toEqual([site.sends]);
      // Settle any pending renders; the member is still listed, and neither
      // it nor any other member can be removed yet.
      await new Promise((r) => setTimeout(r, 50));
      const others = [
        "Remove linux01",
        "Remove linux02",
        "Remove store01",
        "Remove store02",
      ].filter((n) => n !== site.button);
      for (const name of [site.button, ...others]) {
        expect(screen.getByRole("button", { name })).toBeDisabled();
      }

      release();
      await waitFor(() => {
        expect(
          screen.queryByRole("button", { name: site.button }),
        ).not.toBeInTheDocument();
      });
      for (const name of others) {
        expect(screen.getByRole("button", { name })).toBeEnabled();
      }
      expect(writes).toEqual([site.sends]);
    },
  );

  it.each(SITES)(
    "moves focus to the pool's member list, not <body>, after removing $what",
    async (site) => {
      const { user, dialog } = await openRemove(site.button);

      await user.click(within(dialog).getByRole("button", { name: "Remove" }));
      await waitFor(() => {
        expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      });
      await waitFor(() => {
        expect(document.activeElement).toBe(
          screen.getByRole("group", { name: "Members of pool pool01" }),
        );
      });
    },
  );

  it.each(SITES)(
    "returns focus to $what's Remove button when the dialog is cancelled",
    async (site) => {
      const { user, dialog } = await openRemove(site.button);

      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
      await waitFor(() => {
        expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      });
      await waitFor(() => {
        expect(document.activeElement).toBe(
          screen.getByRole("button", { name: site.button }),
        );
      });
    },
  );

  it.each(SITES)(
    "returns focus to the Remove button on a Cancel after removing $what",
    async (site) => {
      const { user, dialog } = await openRemove(site.button);
      await user.click(within(dialog).getByRole("button", { name: "Remove" }));
      await waitFor(() => {
        expect(document.activeElement).toBe(
          screen.getByRole("group", { name: "Members of pool pool01" }),
        );
      });
      // The re-read still lists every member, so all four come back.
      const other = screen.getByRole("button", { name: "Remove linux01" });
      await waitFor(() => {
        expect(other).toBeEnabled();
      });

      await user.click(other);
      const second = await screen.findByRole("alertdialog");
      await user.click(within(second).getByRole("button", { name: "Cancel" }));
      await waitFor(() => {
        expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
      });
      await waitFor(() => {
        expect(document.activeElement).toBe(other);
      });
      expect(writes).toEqual([site.sends]);
    },
  );
});
