import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import type { UserListItem } from "@/types/api";
import { UsersPage } from "./UsersPage";

const account = (
  email: string,
  source: "oidc" | "ldap",
  linked: boolean,
): UserListItem => ({
  id: email,
  email,
  display_name: "",
  role: "user",
  roles: [],
  is_active: true,
  auth_source: source,
  totp_enabled: false,
  ...(source === "oidc"
    ? { sso_linked: linked }
    : { directory_linked: linked }),
  created_at: "",
  updated_at: "",
});

const resetSSOLink = { mutate: vi.fn(), isPending: false };
const resetDirectoryLink = { mutate: vi.fn(), isPending: false };
const mutation = { mutate: vi.fn(), isPending: false };
vi.mock("../api/rbac-queries", () => ({
  useUsers: () => ({
    data: [
      account("alice@example.com", "oidc", true),
      account("carol@example.com", "oidc", false),
      account("dave@example.com", "ldap", true),
      account("erin@example.com", "ldap", false),
    ],
    isLoading: false,
  }),
  useUpdateUser: () => mutation,
  useDeleteUser: () => mutation,
  useResetSSOLink: () => resetSSOLink,
  useResetDirectoryLink: () => resetDirectoryLink,
  useRoles: () => ({ data: [] }),
  useCreateUser: () => ({ mutateAsync: vi.fn(), isPending: false }),
}));
vi.mock("@/features/settings/api/totp-queries", () => ({
  useAdminResetTOTP: () => mutation,
}));

describe("UsersPage", () => {
  // Each reset is offered for a linked account of its source only, and confirms first.
  it.each([
    ["Reset SSO link", "alice@example.com", "carol@example.com", resetSSOLink],
    [
      "Reset directory link",
      "dave@example.com",
      "erin@example.com",
      resetDirectoryLink,
    ],
  ])(
    "offers %s only for a linked account, after a confirm",
    async (item, linked, unlinked, reset) => {
      const user = userEvent.setup();
      const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
      renderWithProviders(<UsersPage />);

      await user.click(
        screen.getByRole("button", { name: `More actions for ${unlinked}` }),
      );
      expect(screen.queryByRole("menuitem", { name: item })).toBeNull();
      await user.keyboard("{Escape}");

      await user.click(
        screen.getByRole("button", { name: `More actions for ${linked}` }),
      );
      await user.click(screen.getByRole("menuitem", { name: item }));
      expect(confirm).toHaveBeenCalledOnce();
      expect(reset.mutate).toHaveBeenCalledWith(linked);
      confirm.mockRestore();
    },
  );
});
