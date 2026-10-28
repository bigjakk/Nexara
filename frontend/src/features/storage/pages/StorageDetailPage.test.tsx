import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { Route, Routes } from "react-router-dom";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { makeStorage } from "@/test/storage.fixtures";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { NodeResponse } from "@/types/api";
import { StorageDetailPage } from "./StorageDetailPage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

afterEach(() => {
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
  });
});

// A storage's History is read by its name; one that is not shared is a
// different storage on every node, so the page asks for its host node's.
describe("a storage's History tab", () => {
  it.each([
    [false, "&node=pve-01"],
    [true, ""],
  ])(
    "for a pool with shared=%s reads the storage's rows%s",
    async (shared, node) => {
      const user = setupUser();
      useAuthStore.setState({
        user: {
          id: "u1",
          email: "u@example.test",
          display_name: "U",
          role: "user",
        },
        permissions: ["view:audit", "view:task", "view:storage"],
        isAuthenticated: true,
        isInitialized: true,
      });
      vi.mocked(apiClient.list).mockImplementation((path: unknown) => {
        const p = String(path);
        if (p.endsWith("/nodes")) {
          return Promise.resolve([
            { id: "n1", name: "pve-01" } as NodeResponse,
          ]);
        }
        if (p.endsWith("/storage")) {
          return Promise.resolve([
            makeStorage({ id: "s1", storage: "store01", shared }),
          ]);
        }
        return Promise.resolve([]);
      });
      vi.mocked(apiClient.get).mockResolvedValue({ pve_version: "9.0.0" });
      vi.mocked(apiClient.page).mockReset();
      vi.mocked(apiClient.page).mockResolvedValue({ items: [], total: 0 });
      renderWithProviders(
        <Routes>
          <Route
            path="/clusters/:clusterId/storage/:storageId"
            element={<StorageDetailPage />}
          />
        </Routes>,
        { router: { initialEntries: ["/clusters/c1/storage/s1"] } },
      );

      await user.click(await screen.findByRole("tab", { name: "History" }));

      // The window the History opens on (a week back) is not what is asserted.
      await waitFor(() => {
        expect(
          String(vi.mocked(apiClient.page).mock.calls[0]?.[0]).replace(
            /&start_time=[^&]*/,
            "",
          ),
        ).toBe(
          `/api/v1/clusters/c1/storages/store01/history?limit=50&offset=0${node}`,
        );
      });
    },
  );
});
