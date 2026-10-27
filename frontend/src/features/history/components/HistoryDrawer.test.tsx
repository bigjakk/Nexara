import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { makeHistoryEntry } from "@/test/history.fixtures";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useAuthStore } from "@/stores/auth-store";
import { guestHistoryKind } from "@/features/vms/lib/guest-history-kind";
import type { HistoryTarget } from "../types/history";
import { HistoryButton } from "./HistoryDrawer";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);

const target: HistoryTarget = {
  clusterId: "c1",
  objectKey: "rule01",
  path: (query) =>
    apiPath`/api/v1/clusters/c1/ha/rules/rule01/history?${query}`,
};

function setPermissions(permissions: string[]) {
  useAuthStore.setState({
    user: { id: "u1", email: "u@example.test", display_name: "U", role: "user" },
    permissions,
    isAuthenticated: true,
    isInitialized: true,
  });
}

afterEach(() => {
  useAuthStore.setState({ user: null, permissions: [], isAuthenticated: false });
});

describe("HistoryButton", () => {
  // Any kind does for the drawer; the guest's namespace is registered.
  it("opens the object's History in a drawer, read only once opened and only with view:audit and view:task", async () => {
    const user = setupUser();
    mockedPage.mockReset();
    mockedPage.mockResolvedValue({
      items: [makeHistoryEntry({ action: "config_update", category: "config" })],
      total: 1,
    });

    setPermissions(["view:audit"]);
    const { unmount } = renderWithProviders(
      <HistoryButton kind={guestHistoryKind} target={target} title="rule01" />,
    );
    expect(screen.queryByRole("button", { name: "History of rule01" })).toBeNull();
    unmount();

    setPermissions(["view:audit", "view:task"]);
    renderWithProviders(
      <HistoryButton kind={guestHistoryKind} target={target} title="rule01" />,
    );
    expect(mockedPage).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "History of rule01" }));

    expect(await screen.findByText("Config change")).toBeInTheDocument();
    expect(screen.getByText("History: rule01")).toBeInTheDocument();
    expect(String(mockedPage.mock.calls[0]?.[0])).toBe(
      "/api/v1/clusters/c1/ha/rules/rule01/history?limit=50&offset=0",
    );
  });
});
