import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { setupUser } from "@/test/user";
import { makeHistoryEntry } from "@/test/history.fixtures";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useAuthStore } from "@/stores/auth-store";
import { useHistoryDrawerStore } from "@/stores/history-drawer-store";
import { guestHistoryKind } from "@/features/vms/lib/guest-history-kind";
import type { HistoryTarget } from "../types/history";
import { HistoryButton, HistoryDrawerHost } from "./HistoryDrawer";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedPage = vi.mocked(apiClient.page);

const target: HistoryTarget = {
  clusterId: "c1",
  path: (query) =>
    apiPath`/api/v1/clusters/c1/ha/rules/rule01/history?${query}`,
};

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
    globalPermissions: [],
    isAuthenticated: false,
  });
});

describe("HistoryButton", () => {
  // Any kind does for the drawer; the guest's namespace is registered.
  it("opens the object's History in a drawer, read only once opened and only with view:audit and view:task", async () => {
    const user = setupUser();
    mockedPage.mockReset();
    mockedPage.mockResolvedValue({
      items: [
        makeHistoryEntry({ action: "config_update", category: "config" }),
      ],
      total: 1,
    });

    setPermissions(["view:audit"]);
    const { unmount } = renderWithProviders(
      <HistoryButton kind={guestHistoryKind} target={target} title="rule01" />,
    );
    expect(
      screen.queryByRole("button", { name: "History of rule01" }),
    ).toBeNull();
    unmount();

    setPermissions(["view:audit", "view:task"]);
    renderWithProviders(
      <>
        <HistoryDrawerHost />
        <HistoryButton
          kind={guestHistoryKind}
          target={target}
          title="rule01"
          rowsAbove={(colSpan) => (
            <tr>
              <td colSpan={colSpan}>a live task</td>
            </tr>
          )}
        />
      </>,
    );
    expect(mockedPage).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "History of rule01" }));

    expect(await screen.findByText("Config change")).toBeInTheDocument();
    expect(screen.getByText("History: rule01")).toBeInTheDocument();
    expect(screen.getByText("a live task")).toBeInTheDocument();
    expect(String(mockedPage.mock.calls[0]?.[0])).toBe(
      "/api/v1/clusters/c1/ha/rules/rule01/history?limit=50&offset=0",
    );
  });

  // An object of no one cluster is read by a route that takes the audit log
  // held on the whole install, which a grant on one cluster is not.
  it.each([
    ["on one cluster only", [], false],
    ["on the whole install", ["view:audit"], true],
  ])(
    "of an object of no cluster is offered to the audit log held %s: %s",
    (_, globalPermissions, offered) => {
      setPermissions(["view:audit", "view:task"]);
      useAuthStore.setState({ globalPermissions });
      renderWithProviders(
        <HistoryButton
          kind={guestHistoryKind}
          target={{ ...target, clusterId: null }}
          title="template01"
        />,
      );
      expect(
        screen.queryByRole("button", { name: "History of template01" }) !==
          null,
      ).toBe(offered);
    },
  );

  // React bubbles a portal's events up its tree: the button's, the drawer's
  // and its overlay's would reach the list row it sits in.
  it("keeps what is done in the History from reaching the row it was opened from", async () => {
    const user = setupUser();
    mockedPage.mockResolvedValue({ items: [], total: 0 });
    setPermissions(["view:audit", "view:task"]);
    const onRow = vi.fn();
    renderWithProviders(
      <>
        <HistoryDrawerHost />
        <table>
          <tbody>
            <tr onClick={onRow} onPointerDown={onRow} onKeyDown={onRow}>
              <td>
                <HistoryButton
                  kind={guestHistoryKind}
                  target={target}
                  title="rule01"
                />
              </td>
            </tr>
          </tbody>
        </table>
      </>,
    );
    await user.click(screen.getByRole("button", { name: "History of rule01" }));
    await user.click(await screen.findByText("History: rule01"));
    await user.keyboard("a");
    // The overlay dismisses the drawer, from beside it in the portal.
    const overlay = screen.getByRole("dialog").previousElementSibling;
    if (!(overlay instanceof HTMLElement))
      throw new Error("no overlay beside the drawer");
    await user.click(overlay);
    expect(screen.queryByRole("dialog")).toBeNull();
    // Focus is back on the button, whose Enter opens it again.
    await user.keyboard("{Enter}");
    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(onRow).not.toHaveBeenCalled();
  });
});
