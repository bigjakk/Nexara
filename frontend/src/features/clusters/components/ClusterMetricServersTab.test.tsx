import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { ClusterMetricServersTab } from "./ClusterMetricServersTab";
import type { MetricServerConfig } from "../api/metric-server-queries";

vi.mock("@/hooks/useAuth", () => ({
  useAuth: () => ({ canManage: () => true }),
}));

const CLUSTER = "cccccccc-0000-0000-0000-000000000003";
const SERVERS_PATH = `/api/v1/clusters/${CLUSTER}/metric-servers`;

const SERVERS: MetricServerConfig[] = [
  { id: "influx01", type: "influxdb", server: "192.0.2.10", port: 8089 },
  { id: "graphite01", type: "graphite", server: "192.0.2.11", port: 2003 },
  {
    id: "graphite02",
    type: "graphite",
    server: "192.0.2.12",
    port: 2003,
    disable: 1,
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
      const items = url === SERVERS_PATH ? SERVERS : [];
      return Promise.resolve(json(200, { items, total: items.length }));
    }),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
});

async function openDialog(name = "Delete graphite01") {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>{children}</QueryClientProvider>
  );
  render(<ClusterMetricServersTab clusterId={CLUSTER} />, { wrapper });
  const user = userEvent.setup();
  // The second row, so a delete aimed at the first row would show.
  await user.click(await screen.findByRole("button", { name }));
  const dialog = await screen.findByRole("alertdialog");
  return { user, dialog };
}

describe("ClusterMetricServersTab — deleting a server", () => {
  it("asks first, naming the server, and sends nothing yet", async () => {
    const { dialog } = await openDialog();

    expect(
      within(dialog).getByRole("heading", {
        name: "Delete metric server graphite01?",
      }),
    ).toBeInTheDocument();
    expect(dialog).toHaveTextContent(
      "stops sending the cluster's metrics to 192.0.2.11:2003",
    );
    expect(dialog).toHaveTextContent(
      "Metrics already sent stay on 192.0.2.11.",
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

  it("deletes exactly the server it was opened for, once, on Delete", async () => {
    const { user, dialog } = await openDialog();

    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    await waitFor(() => {
      expect(writes).toEqual([`DELETE ${SERVERS_PATH}/graphite01`]);
    });
    expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
  });

  // A disabled server is not being sent anything now, so deleting it cannot
  // be what stops the sending; graphite01 above is the positive twin.
  it("says a disabled server is not being sent metrics now", async () => {
    const { dialog } = await openDialog("Delete graphite02");

    expect(dialog).toHaveTextContent(
      "It is disabled, so no metrics are being sent to 192.0.2.12:2003 now.",
    );
    expect(dialog).not.toHaveTextContent("stops sending");
    expect(writes).toEqual([]);
  });
});
