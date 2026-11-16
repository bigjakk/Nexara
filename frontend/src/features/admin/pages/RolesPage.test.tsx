import { describe, it, expect, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import type { RBACPermission, RBACRole } from "@/types/api";
import { RolesPage } from "./RolesPage";

const perm = (id: string): RBACPermission => ({
  id,
  action: "view",
  resource: id,
  description: "",
});

const role: RBACRole = {
  id: "r1",
  name: "Operators",
  description: "",
  is_builtin: false,
  // "gone" is no longer in the catalogue.
  permissions: [perm("vm"), perm("gone")],
  created_at: "",
  updated_at: "",
};

const mutation = { mutate: vi.fn(), isPending: false };
vi.mock("../api/rbac-queries", () => ({
  useRoles: () => ({ data: [role], isLoading: false }),
  useRole: () => ({ data: undefined }),
  useCreateRole: () => mutation,
  useUpdateRole: () => mutation,
  useDeleteRole: () => mutation,
  usePermissionCatalog: () => ({ data: [perm("vm"), perm("node")] }),
}));

describe("RolesPage", () => {
  // The count under Permissions left out a stale id it could not show.
  it("counts only granted permissions that are still in the catalogue", async () => {
    const user = userEvent.setup();
    renderWithProviders(<RolesPage />);
    await user.click(screen.getByRole("button", { name: "Edit Operators" }));
    expect(screen.getByText("1 of 2 granted")).toBeInTheDocument();
  });

  // Delete asks in the app's own dialog (it used window.confirm).
  it("deletes a role only from the confirm dialog", async () => {
    const user = userEvent.setup();
    renderWithProviders(<RolesPage />);
    await user.click(screen.getByRole("button", { name: "Delete Operators" }));
    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent('Delete role "Operators"?');
    expect(mutation.mutate).not.toHaveBeenCalled();

    await user.click(
      within(dialog).getByRole("button", { name: "Delete role" }),
    );
    expect(mutation.mutate).toHaveBeenCalledWith("r1");
  });
});
