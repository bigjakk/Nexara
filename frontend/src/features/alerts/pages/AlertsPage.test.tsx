import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import { AlertsPage } from "./AlertsPage";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// What both summaries read, all quiet.
const QUIET = {
  firing_count: 0,
  pending_count: 0,
  acknowledged_count: 0,
  critical_firing: 0,
  warning_firing: 0,
  info_firing: 0,
  pending: 0,
  rate_limited: 0,
  retrying: 0,
  resolved: 0,
  dismissed: 0,
};

afterEach(() => {
  useAuthStore.setState({ permissions: [], globalPermissions: [] });
});

describe("AlertsPage — permission", () => {
  // The channel and dead-letter routes take their grants install-wide
  // (registry_alerts.go, registry_notification_dlq.go).
  it.each<[string, string[], boolean]>([
    ["on a cluster only", [], false],
    [
      "install-wide",
      ["manage:notification_channel", "view:notification_dlq"],
      true,
    ],
  ])(
    "with the channel and queue grants %s (install-wide: %j) offers Add channel and the queue: %s",
    async (_held, globalPermissions, offered) => {
      useAuthStore.setState({
        permissions: [
          "view:alert",
          "manage:notification_channel",
          "view:notification_dlq",
        ],
        globalPermissions: ["view:alert", ...globalPermissions],
      });
      vi.mocked(apiClient.list).mockResolvedValue([] as never);
      vi.mocked(apiClient.get).mockResolvedValue(QUIET);
      renderWithProviders(<AlertsPage />);
      expect(
        await screen.findByRole("tab", { name: "Alert history" }),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "Add channel" }) !== null,
      ).toBe(offered);
      // Nor is the queue's summary read, and refused, every 30 s.
      expect(
        vi
          .mocked(apiClient.get)
          .mock.calls.some(
            ([path]) => path === "/api/v1/notification-dlq/summary",
          ),
      ).toBe(offered);
      expect(
        screen.queryByRole("tab", { name: /Dead-letter queue/ }) !== null,
      ).toBe(offered);
    },
  );
});
