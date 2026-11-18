import { afterEach, describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { useAuthStore } from "@/stores/auth-store";
import { AdminNav } from "./AdminNav";

afterEach(() => {
  useAuthStore.setState({ user: null, permissions: [], globalPermissions: [] });
});

describe("AdminNav", () => {
  // A tab is offered only to a role its page's guard admits (App.tsx), and
  // every guard takes its grant install-wide: one on a cluster offers none.
  it.each<[string[], string[], string[]]>([
    [["manage:user", "manage:role", "manage:settings"], [], []],
    [["manage:user"], ["manage:user"], ["Users", "LDAP", "OIDC / SSO"]],
    [["manage:role"], ["manage:role"], ["Roles"]],
    [["manage:settings"], ["manage:settings"], ["Branding"]],
  ])(
    "with %j, %j install-wide, offers %j",
    (permissions, globalPermissions, offered) => {
      useAuthStore.setState({ user: null, permissions, globalPermissions });
      renderWithProviders(<AdminNav />);
      expect(
        screen.queryAllByRole("link").map((link) => link.textContent),
      ).toEqual(offered);
    },
  );
});
