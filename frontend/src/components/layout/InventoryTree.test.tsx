import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import type { ClusterResponse } from "@/types/api";
import { InventoryTree } from "./InventoryTree";

function cluster(id: string, name: string): ClusterResponse {
  return {
    id,
    name,
    api_url: "https://192.0.2.10:8006",
    token_id: "user01@pve!token01",
    tls_fingerprint: "",
    sync_interval_seconds: 30,
    is_active: true,
    status: "online",
    pve_version: "9.0.6",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    credential_source: "manual",
  };
}

let api: ReturnType<typeof stubApi>;

afterEach(() => {
  vi.unstubAllGlobals();
});

// Delete cluster takes the cluster's whole branch away — the Actions button
// that opened it, and the dialog itself, which lives in the branch.
describe("InventoryTree — focus after deleting a cluster", () => {
  it("puts focus on the tree once the deleted cluster's branch has gone", async () => {
    const reads: Record<string, unknown> = {};
    // Read back without cluster02 once it has been deleted.
    Object.defineProperty(reads, "/api/v1/clusters", {
      enumerable: true,
      get: () =>
        listOf(
          api.sent.some((r) => r.startsWith("DELETE /api/v1/clusters/c2"))
            ? [cluster("c1", "cluster01")]
            : [cluster("c1", "cluster01"), cluster("c2", "cluster02")],
        ),
    });
    api = stubApi(reads);
    const user = userEvent.setup();
    renderWithProviders(<InventoryTree />);
    await user.click(
      await screen.findByRole("button", { name: "Actions for cluster02" }),
    );
    await user.click(await screen.findByRole("menuitem", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    await user.type(
      within(dialog).getByPlaceholderText("cluster02"),
      "cluster02",
    );

    await user.click(
      within(dialog).getByRole("button", { name: "Delete Cluster" }),
    );

    await waitFor(() => {
      expect(
        screen.queryByRole("button", { name: "Actions for cluster02" }),
      ).not.toBeInTheDocument();
    });
    await waitFor(() => {
      expect(document.activeElement).toBe(
        screen.getByRole("group", { name: "Datacenter" }),
      );
    });
  });
});
