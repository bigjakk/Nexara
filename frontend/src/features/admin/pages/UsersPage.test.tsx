import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import type { UserListItem } from "@/types/api";
import { UsersPage } from "./UsersPage";

const account = (email: string, ssoLinked: boolean): UserListItem => ({
  id: email,
  email,
  display_name: "",
  role: "user",
  roles: [],
  is_active: true,
  auth_source: "oidc",
  totp_enabled: false,
  sso_linked: ssoLinked,
  created_at: "",
  updated_at: "",
});

const resetSSOLink = { mutate: vi.fn(), isPending: false };
const mutation = { mutate: vi.fn(), isPending: false };
vi.mock("../api/rbac-queries", () => ({
  useUsers: () => ({
    data: [
      account("alice@example.com", true),
      account("carol@example.com", false),
    ],
    isLoading: false,
  }),
  useUpdateUser: () => mutation,
  useDeleteUser: () => mutation,
  useResetSSOLink: () => resetSSOLink,
  useRoles: () => ({ data: [] }),
  useCreateUser: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/features/settings/api/totp-queries", () => ({
  useAdminResetTOTP: () => mutation,
}));

describe("UsersPage", () => {
  // The reset is offered for a linked SSO account only, and confirms first.
  it("offers Reset SSO link only for a linked account, after a confirm", async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    renderWithProviders(<UsersPage />);

    await user.click(
      screen.getByRole("button", {
        name: "More actions for carol@example.com",
      }),
    );
    expect(
      screen.queryByRole("menuitem", { name: "Reset SSO link" }),
    ).toBeNull();
    await user.keyboard("{Escape}");

    await user.click(
      screen.getByRole("button", {
        name: "More actions for alice@example.com",
      }),
    );
    await user.click(screen.getByRole("menuitem", { name: "Reset SSO link" }));
    expect(confirm).toHaveBeenCalledOnce();
    expect(resetSSOLink.mutate).toHaveBeenCalledWith("alice@example.com");
  });
});
