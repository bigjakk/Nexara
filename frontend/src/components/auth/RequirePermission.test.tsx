import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { RequirePermission } from "./RequirePermission";
import { useAuthStore } from "@/stores/auth-store";

function setUser(role: "admin" | "user", permissions: string[]) {
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.test",
      display_name: "Test User",
      role,
    },
    permissions,
    isAuthenticated: true,
    isInitialized: true,
  });
}

function renderGuarded() {
  return render(
    <MemoryRouter>
      <RequirePermission action="manage" resource="user">
        <div>protected content</div>
      </RequirePermission>
    </MemoryRouter>,
  );
}

describe("RequirePermission", () => {
  beforeEach(() => {
    useAuthStore.setState({
      user: null,
      permissions: [],
      isAuthenticated: false,
      isInitialized: false,
    });
  });

  it("renders children when the user has the granular permission", () => {
    setUser("user", ["manage:user"]);
    renderGuarded();
    expect(screen.getByText("protected content")).toBeInTheDocument();
    expect(screen.queryByText("Access denied")).not.toBeInTheDocument();
  });

  it("renders the 403 page when the permission is missing", () => {
    setUser("user", ["view:cluster"]);
    renderGuarded();
    expect(screen.queryByText("protected content")).not.toBeInTheDocument();
    expect(screen.getByText("Access denied")).toBeInTheDocument();
    expect(screen.getByText("manage:user")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /back to dashboard/i })).toHaveAttribute(
      "href",
      "/",
    );
  });

  it("admin role bypasses the granular check (legacy fallback)", () => {
    setUser("admin", []);
    renderGuarded();
    expect(screen.getByText("protected content")).toBeInTheDocument();
  });

  it("denies access when there is no authenticated user", () => {
    renderGuarded();
    expect(screen.getByText("Access denied")).toBeInTheDocument();
  });
});
