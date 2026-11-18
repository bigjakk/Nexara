import { afterEach, describe, expect, it, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { grantPermissions } from "@/test/permissions";
import { useAuthStore } from "@/stores/auth-store";
import { ReportRunsTable } from "./ReportRunsTable";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

afterEach(() => {
  useAuthStore.setState({ permissions: [], globalPermissions: [] });
});

describe("ReportRunsTable — no runs yet", () => {
  // Only a role that may generate a report (generate:report) is asked to.
  it.each<[string[], string]>([
    [["view:report"], "No report runs yet."],
    [
      ["view:report", "generate:report"],
      "No report runs yet. Generate one from the report types.",
    ],
  ])("with %j says %s", async (permissions, text) => {
    grantPermissions(...permissions);
    vi.mocked(apiClient.list).mockResolvedValue([] as never);
    renderWithProviders(<ReportRunsTable />);
    expect(await screen.findByText(text)).toBeInTheDocument();
  });
});
