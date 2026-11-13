import { describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { makeNode } from "@/test/cluster.fixtures";
import { makeStorage } from "@/test/storage.fixtures";
import { apiClient } from "@/lib/api-client";
import { ClusterStoresPanel } from "./ClusterStoresPanel";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const GIB = 1024 ** 3;

// Each storage definition is one row, as in Proxmox's Datacenter > Storage; a
// local one opens to a row per node, each linking to that node's store page.
// The grouping itself is storeGroups's (store-rows.test.ts).
describe("ClusterStoresPanel", () => {
  it("lists a local store once, for its fullest node, and opens it to its nodes", async () => {
    const user = setupUser();
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.endsWith("/c1/storage")) {
        return Promise.resolve([
          makeStorage({
            id: "s1",
            node_id: "n1",
            storage: "local",
            total: 50 * GIB,
            used: 10 * GIB,
          }),
          makeStorage({
            id: "s2",
            node_id: "n2",
            storage: "local",
            total: 50 * GIB,
            used: 40 * GIB,
          }),
          makeStorage({
            id: "s3",
            node_id: "n1",
            storage: "store01",
            shared: true,
            total: 100 * GIB,
          }),
          makeStorage({
            id: "s4",
            node_id: "n2",
            storage: "store01",
            shared: true,
            total: 100 * GIB,
          }),
        ]);
      }
      if (p.endsWith("/c1/nodes")) {
        return Promise.resolve([
          makeNode({ id: "n1", name: "pve-01" }),
          makeNode({ id: "n2", name: "pve-02" }),
        ]);
      }
      return Promise.resolve([]);
    });

    renderWithProviders(
      <ClusterStoresPanel clusterId="c1" title="Storage" showTotals />,
    );

    const local = await screen.findByRole("button", { name: "local" });
    expect(local).toHaveAttribute("aria-expanded", "false");
    expect(screen.getAllByRole("link", { name: "store01" })).toHaveLength(1);
    expect(await screen.findByText("Fullest on pve-02")).toBeInTheDocument();
    expect(screen.getByText(/^2 stores, /)).toBeInTheDocument();

    await user.click(local);
    expect(
      screen.getByRole("link", { name: "local on pve-01" }),
    ).toHaveAttribute("href", "/storage/c1/s1");
    expect(
      screen.getByRole("link", { name: "local on pve-02" }),
    ).toHaveAttribute("href", "/storage/c1/s2");
  });
});
