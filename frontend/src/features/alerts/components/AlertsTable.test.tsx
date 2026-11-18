import { afterEach, describe, expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { useAuthStore } from "@/stores/auth-store";
import type { AlertInstance } from "@/types/api";
import { AlertsTable } from "./AlertsTable";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

function firing(
  id: string,
  resource: string,
  clusterId?: string,
): AlertInstance {
  return {
    id,
    rule_id: "rule-1",
    state: "firing",
    severity: "warning",
    ...(clusterId === undefined ? {} : { cluster_id: clusterId }),
    resource_name: resource,
    metric: "cpu_usage",
    current_value: 95,
    threshold: 90,
    message: `${resource} needs a look`,
    escalation_level: 0,
    pending_at: "2026-08-15T00:00:00Z",
    fired_at: "2026-08-15T00:05:00Z",
    created_at: "2026-08-15T00:00:00Z",
  };
}

afterEach(() => {
  useAuthStore.setState({ permissions: [], globalPermissions: [] });
});

describe("AlertsTable — permission", () => {
  // An alert on a cluster takes acknowledge:alert there; one that names no
  // cluster, a UPS's, takes it install-wide (AcknowledgeAlert and
  // ResolveAlert in handlers/alerts.go).
  it.each<[string[], boolean]>([
    [[], false],
    [["acknowledge:alert"], true],
  ])(
    "with %j install-wide offers the actions on an alert with no cluster: %s",
    async (globalPermissions, offered) => {
      useAuthStore.setState({
        permissions: ["acknowledge:alert"],
        globalPermissions,
      });
      vi.mocked(apiClient.list).mockResolvedValue([
        firing("a-c", "linux01", "c1"),
        firing("a-u", "ups01"),
      ] as never);
      renderWithProviders(<AlertsTable />);
      await screen.findByText("ups01");
      const row = (name: string) =>
        screen.getByText(name).closest("tr") as HTMLElement;
      for (const name of ["Acknowledge", "Resolve"]) {
        expect(
          within(row("linux01")).getByRole("button", { name }),
        ).toBeInTheDocument();
        expect(
          within(row("ups01")).queryByRole("button", { name }) !== null,
        ).toBe(offered);
      }
    },
  );
});
