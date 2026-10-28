import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { Tabs, TabsList } from "@/components/ui/tabs";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { useHistoryDrawerStore } from "@/stores/history-drawer-store";
import { clusterHistories } from "../lib/cluster-histories";
import { ClusterHistoryButton } from "./ClusterHistoryButton";
import { HistoryTabContent, HistoryTabTrigger } from "./HistoryTab";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);

function setPermissions(permissions: string[]) {
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.test",
      display_name: "U",
      role: "user",
    },
    permissions,
    isAuthenticated: true,
    isInitialized: true,
  });
}

afterEach(() => {
  useHistoryDrawerStore.getState().resetSession();
  useAuthStore.setState({
    user: null,
    permissions: [],
    isAuthenticated: false,
  });
});

// The tab a cluster page tab strip gets, and the button a row gets: both only
// for a caller who can read what they open, and neither reads before it is opened.
describe("a cluster page tab's History", () => {
  const strip = (
    <Tabs defaultValue="history">
      <TabsList>
        <HistoryTabTrigger />
      </TabsList>
      <HistoryTabContent history={clusterHistories.ha("c1")} />
    </Tabs>
  );

  it("is offered with view:audit and view:task, and reads its route", async () => {
    mockedPage.mockReset();
    mockedPage.mockResolvedValue({ items: [], total: 0 });
    setPermissions(["view:audit"]);
    const { unmount } = renderWithProviders(strip);
    expect(screen.queryByRole("tab", { name: "History" })).toBeNull();
    expect(mockedPage).not.toHaveBeenCalled();
    unmount();

    setPermissions(["view:audit", "view:task"]);
    renderWithProviders(strip);
    expect(screen.getByRole("tab", { name: "History" })).toBeInTheDocument();
    // It opens on the last week, as the server's default window does.
    expect(
      await screen.findByText(/Nothing was recorded in this time/),
    ).toBeInTheDocument();
    expect(String(mockedPage.mock.calls[0]?.[0])).toMatch(
      /^\/api\/v1\/clusters\/c1\/history\/ha\?limit=50&offset=0&start_time=/,
    );
  });
});

describe("a row's History button", () => {
  // A name no browser can send as a path segment has nothing to ask for.
  it.each([
    ["group01", true],
    ["..", false],
  ])("for %s is shown: %s", (name, shown) => {
    setPermissions(["view:audit", "view:task"]);
    renderWithProviders(
      <ClusterHistoryButton
        history={clusterHistories.haGroup("c1", name)}
        title={name}
      />,
    );
    expect(
      screen.queryByRole("button", { name: `History of ${name}` }) !== null,
    ).toBe(shown);
  });
});
