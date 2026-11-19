import { expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { apiClient } from "@/lib/api-client";
import { renderWithProviders } from "@/test/test-utils";
import { makeCluster } from "@/test/cluster.fixtures";
import { grantPermissions } from "@/test/permissions";
import { CreateResourceMenu } from "./CreateResourceMenu";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// Create offered a Viewer every create, each refused: a create takes what its
// route declares (an import, which creates a VM, manage:vm as well), and a role
// allowed none gets no Create button.
it.each([
  [["manage:vm"], ["Virtual machine"]],
  [["manage:container", "manage:vm_import"], ["Container"]],
  [
    ["manage:vm", "manage:vm_import"],
    ["Virtual machine", "Import VM"],
  ],
  [["view:vm"], []],
])("with %j offers %j", async (permissions, offered) => {
  grantPermissions(...permissions);
  vi.mocked(apiClient.list).mockResolvedValue([makeCluster()]);
  renderWithProviders(<CreateResourceMenu />);

  const create = screen.queryByRole("button", { name: "Create" });
  if (offered.length === 0) {
    expect(create).toBeNull();
    return;
  }
  await userEvent.setup().click(create as HTMLElement);
  const items = await screen.findAllByRole("menuitem");
  expect(items.map((i) => i.textContent)).toEqual(offered);
});
