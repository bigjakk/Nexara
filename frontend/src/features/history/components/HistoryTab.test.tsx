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
// for a caller who can read what they open — the audit log, the task log and
// the tab's or object's own view permission — and neither reads before it is
// opened.
describe("a cluster page tab's History", () => {
  const ha = clusterHistories.ha("c1");
  const strip = (
    <Tabs defaultValue="history">
      <TabsList>
        <HistoryTabTrigger history={ha} />
      </TabsList>
      <HistoryTabContent history={ha} />
    </Tabs>
  );
  const grants = ["view:audit", "view:task", "view:ha"];

  it("is offered with all three grants, and reads its route", async () => {
    mockedPage.mockReset();
    mockedPage.mockResolvedValue({ items: [], total: 0 });
    for (const missing of grants) {
      setPermissions(grants.filter((g) => g !== missing));
      const { unmount } = renderWithProviders(strip);
      expect(
        screen.queryByRole("tab", { name: "History" }),
        `without ${missing}`,
      ).toBeNull();
      expect(mockedPage).not.toHaveBeenCalled();
      unmount();
    }

    setPermissions(grants);
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
  // A name no browser can send as a path segment has nothing to ask for, and a
  // caller without the object's view permission would be answered 403.
  it.each([
    ["group01", ["view:ha"], true],
    ["..", ["view:ha"], false],
    ["group01", [], false],
  ])("for %s with %o is shown: %s", (name, extra, shown) => {
    setPermissions(["view:audit", "view:task", ...extra]);
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
