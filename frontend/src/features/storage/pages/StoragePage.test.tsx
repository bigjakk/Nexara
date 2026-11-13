import { describe, expect, it, vi } from "vitest";
import { act, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { makeCluster } from "@/test/cluster.fixtures";
import { makeStorage } from "@/test/storage.fixtures";
import { apiClient } from "@/lib/api-client";
import { StoragePage } from "./StoragePage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const GIB = 1024 ** 3;

// The page reads every cluster's stores at once; the header's switch and the
// ?cluster= link narrow what it lists and sums to one of them.
describe("StoragePage", () => {
  it("lists and sums every cluster's stores, then one cluster's once it is picked", async () => {
    const user = setupUser();
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.endsWith("/clusters")) {
        return Promise.resolve([
          makeCluster({ id: "c1", name: "cluster01" }),
          makeCluster({ id: "c2", name: "cluster02" }),
        ]);
      }
      if (p.endsWith("/c1/storage")) {
        return Promise.resolve([
          makeStorage({ id: "s1", storage: "store01", total: 100 * GIB }),
        ]);
      }
      if (p.endsWith("/c2/storage")) {
        return Promise.resolve([
          makeStorage({ id: "s2", storage: "store02", total: 100 * GIB }),
          makeStorage({ id: "s3", storage: "store03", total: 100 * GIB }),
        ]);
      }
      return Promise.resolve([]);
    });

    renderWithProviders(<StoragePage />);

    expect(
      await screen.findByText("3 stores on 2 clusters, 0 B of 300.0 GiB used"),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "store01" })).toHaveAttribute(
      "href",
      "/storage/c1/s1",
    );

    await user.click(screen.getByRole("button", { name: "cluster02" }));

    expect(
      screen.getByText("2 stores on 1 cluster, 0 B of 200.0 GiB used"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "store01" })).toBeNull();
  });

  // A refresh that fails keeps the stores read before it: the panel goes on
  // listing what the header goes on counting.
  it("keeps a cluster's stores listed when a later refresh of them fails", async () => {
    let down = false;
    vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
      const p = String(path);
      if (p.endsWith("/clusters")) {
        return Promise.resolve([makeCluster({ id: "c1", name: "cluster01" })]);
      }
      if (p.endsWith("/c1/storage")) {
        return down
          ? Promise.reject(new Error("down"))
          : Promise.resolve([
              makeStorage({ id: "s1", storage: "store01", total: 100 * GIB }),
            ]);
      }
      return Promise.resolve([]);
    });
    const { queryClient } = renderWithProviders(<StoragePage />);
    await screen.findByRole("link", { name: "store01" });

    down = true;
    await act(() =>
      queryClient.refetchQueries({
        queryKey: ["clusters", "c1", "storage"],
        exact: true,
      }),
    );

    expect(await screen.findByText(/Could not refresh/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "store01" })).toBeInTheDocument();
    expect(
      screen.getByText("1 store on 1 cluster, 0 B of 100.0 GiB used"),
    ).toBeInTheDocument();
  });
});
