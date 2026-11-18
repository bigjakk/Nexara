import { expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { makeCluster } from "@/test/cluster.fixtures";
import { grantPermissions } from "@/test/permissions";
import { StorageTree } from "./StorageTree";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// Add storage, the cluster row's one menu item, takes manage:storage; a
// Viewer got the menu, and the dialog's create answered 403.
it.each([
  [["view:storage", "manage:storage"], true],
  [["view:storage"], false],
])("with %j offers Add storage: %s", async (permissions, offered) => {
  grantPermissions(...permissions);
  vi.mocked(apiClient.list).mockImplementation((path: unknown) =>
    Promise.resolve(String(path) === "/api/v1/clusters" ? [makeCluster()] : []),
  );
  renderWithProviders(<StorageTree />);

  fireEvent.contextMenu(await screen.findByText("test-cluster"));
  expect(screen.queryByRole("menuitem", { name: "Add storage" }) !== null).toBe(
    offered,
  );
});
