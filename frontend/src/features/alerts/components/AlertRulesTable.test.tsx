import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { AlertRulesTable } from "./AlertRulesTable";
import { apiClient } from "@/lib/api-client";
import { useAuth } from "@/hooks/useAuth";
import type { AlertRule } from "@/types/api";

vi.mock("@/lib/api-client", () => ({
  apiClient: { get: vi.fn(), put: vi.fn(), delete: vi.fn() },
}));
vi.mock("@/hooks/useAuth", () => ({ useAuth: vi.fn() }));

const mockedGet = vi.mocked(apiClient.get);
const mockedPut = vi.mocked(apiClient.put);

function makeRule(overrides: Partial<AlertRule> = {}): AlertRule {
  return {
    id: "rule-1",
    name: "High CPU",
    description: "",
    enabled: true,
    severity: "warning",
    metric: "cpu_usage",
    operator: ">",
    threshold: 90,
    duration_seconds: 300,
    scope_type: "cluster",
    cooldown_seconds: 600,
    escalation_chain: [],
    message_template: "",
    created_by: "user-1",
    created_at: "2026-08-15T00:00:00Z",
    updated_at: "2026-08-15T00:00:00Z",
    ...overrides,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useAuth).mockReturnValue({
    hasPermission: () => true,
  } as unknown as ReturnType<typeof useAuth>);
  mockedGet.mockResolvedValue([makeRule()]);
  mockedPut.mockResolvedValue(makeRule({ enabled: false }));
});

describe("AlertRulesTable", () => {
  it("toggle sends only { enabled } so the stored threshold survives", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AlertRulesTable />);

    const nameCell = await screen.findByText("High CPU");
    const row = nameCell.closest("tr");
    if (!row) throw new Error("rule row not rendered");

    await user.click(within(row).getByRole("button", { name: "Disable rule" }));

    await waitFor(() => {
      expect(mockedPut).toHaveBeenCalledTimes(1);
    });
    // Exact payload: placeholder fields like threshold: 0 in this body used
    // to overwrite the stored threshold ("cpu_usage > 90" became "> 0").
    expect(mockedPut).toHaveBeenCalledWith("/api/v1/alert-rules/rule-1", {
      enabled: false,
    });
  });

  it("hides the toggle and delete actions without manage:alert", async () => {
    vi.mocked(useAuth).mockReturnValue({
      hasPermission: () => false,
    } as unknown as ReturnType<typeof useAuth>);
    renderWithProviders(<AlertRulesTable />);

    const nameCell = await screen.findByText("High CPU");
    const row = nameCell.closest("tr");
    if (!row) throw new Error("rule row not rendered");

    expect(within(row).queryByRole("button")).not.toBeInTheDocument();
    expect(within(row).getByText("Yes")).toBeInTheDocument();
  });
});
