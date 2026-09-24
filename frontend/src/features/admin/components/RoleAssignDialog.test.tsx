import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { useAuthStore } from "@/stores/auth-store";
import type { RBACUserRole } from "@/types/api";
import { RoleAssignDialog } from "./RoleAssignDialog";

const SELF = "user-self";
const OTHER = "user-other";

function assignment(
  id: string,
  userId: string,
  roleName: string,
  scope: "global" | "cluster",
): RBACUserRole {
  return {
    id,
    user_id: userId,
    role_id: `role-${roleName}`,
    role_name: roleName,
    role_description: "",
    is_builtin: true,
    scope_type: scope,
    created_at: "2026-09-01T00:00:00Z",
  };
}

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: SELF,
      email: "admin@example.com",
      display_name: "Admin",
      role: "user",
    },
    permissions: ["manage:role"],
    isAuthenticated: true,
    isInitialized: true,
  });
  api = stubApi({
    "/api/v1/rbac/roles": listOf([]),
    [`/api/v1/rbac/users/${OTHER}/roles`]: listOf([
      assignment("ur-1", OTHER, "Viewer", "global"),
      assignment("ur-2", OTHER, "Operator", "cluster"),
    ]),
    [`/api/v1/rbac/users/${SELF}/roles`]: listOf([
      assignment("ur-3", SELF, "Viewer", "global"),
      assignment("ur-4", SELF, "Administrator", "global"),
    ]),
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
  });
});

function renderFor(userId: string) {
  renderWithProviders(
    <RoleAssignDialog userId={userId} open onOpenChange={() => undefined} />,
  );
}

describe("RoleAssignDialog — revoke", () => {
  it("asks first, naming the assignment, and sends nothing yet", async () => {
    const user = userEvent.setup();
    renderFor(OTHER);
    await user.click(
      await screen.findByRole("button", {
        name: "Revoke Operator at cluster scope",
      }),
    );

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Revoke Operator at cluster scope?");
    // Someone else's assignment carries no self-revocation warning.
    expect(dialog).not.toHaveTextContent("This is your own account");
    expect(api.writes()).toEqual([]);
  });

  it("warns when the assignment is the signed-in user's own", async () => {
    const user = userEvent.setup();
    renderFor(SELF);
    await user.click(
      await screen.findByRole("button", {
        name: "Revoke Administrator at global scope",
      }),
    );

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Revoke Administrator at global scope?");
    expect(dialog).toHaveTextContent("This is your own account.");
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const user = userEvent.setup();
    renderFor(OTHER);
    await user.click(
      await screen.findByRole("button", {
        name: "Revoke Operator at cluster scope",
      }),
    );

    await user.click(
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "Cancel",
      }),
    );

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming revokes exactly the assignment clicked, once", async () => {
    const user = userEvent.setup();
    renderFor(OTHER);
    await user.click(
      await screen.findByRole("button", {
        name: "Revoke Operator at cluster scope",
      }),
    );

    await user.click(
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "Revoke",
      }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual([
        `DELETE /api/v1/rbac/users/${OTHER}/roles/ur-2`,
      ]);
    });
    // Once: a second request arriving after the first would land here.
    await new Promise((r) => setTimeout(r, 100));
    expect(api.writes()).toEqual([
      `DELETE /api/v1/rbac/users/${OTHER}/roles/ur-2`,
    ]);
  });
});
