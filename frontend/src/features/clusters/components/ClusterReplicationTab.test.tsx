import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ClusterReplicationTab } from "./ClusterReplicationTab";
import type { ReplicationJob } from "@/features/replication/api/replication-queries";

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

const CLUSTER = "cccccccc-0000-0000-0000-000000000003";
const JOBS_PATH = `/api/v1/clusters/${CLUSTER}/replication`;

const JOBS: ReplicationJob[] = [
  {
    id: "101-0",
    type: "local",
    source: "pve-01",
    target: "pve-02",
    guest: 101,
  },
  {
    id: "102-0",
    type: "local",
    source: "pve-01",
    target: "pve-03",
    guest: 102,
  },
];

/** Every request other than a GET, as "METHOD path". */
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
        writes.push(`${method} ${url}`);
        return Promise.resolve(json(200, { status: "ok" }));
      }
      const items = url === JOBS_PATH ? JOBS : [];
      return Promise.resolve(json(200, { items, total: items.length }));
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function openDialog() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  render(<ClusterReplicationTab clusterId={CLUSTER} />, { wrapper });
  const user = userEvent.setup();
  // The second row, so a delete aimed at the first row would show.
  await user.click(
    await screen.findByRole("button", {
      name: "Delete replication job 102-0",
    }),
  );
  const dialog = await screen.findByRole("alertdialog");
  return { user, dialog };
}

describe("ClusterReplicationTab — deleting a job", () => {
  it("asks first, naming the job and what it deletes on the target, and sends nothing yet", async () => {
    const { dialog } = await openDialog();

    expect(
      within(dialog).getByRole("heading", {
        name: "Delete replication job 102-0?",
      }),
    ).toBeInTheDocument();
    // Without keep, Proxmox deletes the replicated volumes on the target.
    expect(dialog).toHaveTextContent(
      "it deletes the copy of guest 102's disks that this job replicated to pve-03",
    );
    expect(dialog).toHaveTextContent(
      "The replicated copy on pve-03 cannot be recovered.",
    );
    expect(writes).toEqual([]);
  });

  it("sends nothing on Cancel", async () => {
    const { user, dialog } = await openDialog();

    await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(writes).toEqual([]);
  });

  it("deletes exactly the job it was opened for, once, on Delete", async () => {
    const { user, dialog } = await openDialog();

    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => {
      expect(writes).toEqual([`DELETE ${JOBS_PATH}/102-0`]);
    });
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });
});
