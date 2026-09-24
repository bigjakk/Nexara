import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { listOf, stubApi } from "@/test/fetch-stub";
import { useAuthStore } from "@/stores/auth-store";
import type { AlertRule } from "@/types/api";
import { AlertRulesTable } from "./AlertRulesTable";

// A file of its own: AlertRulesTable.test.tsx mocks apiClient module-wide,
// and these assert on the request that actually leaves.

function rule(id: string, name: string): AlertRule {
  return {
    id,
    name,
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
  };
}

let api: ReturnType<typeof stubApi>;

beforeEach(() => {
  useAuthStore.setState({
    user: {
      id: "u1",
      email: "u@example.com",
      display_name: "Test User",
      role: "user",
    },
    permissions: ["manage:alert"],
    isAuthenticated: true,
    isInitialized: true,
  });
  api = stubApi({
    "/api/v1/alert-rules": listOf([
      rule("rule-1", "High CPU"),
      rule("rule-2", "Low disk"),
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

async function clickDelete(user: ReturnType<typeof userEvent.setup>) {
  const row = (await screen.findByText("Low disk")).closest("tr");
  if (!row) throw new Error("rule row not rendered");
  await user.click(within(row).getByRole("button", { name: "Delete rule" }));
}

describe("AlertRulesTable — delete", () => {
  it("asks first, naming the rule, and sends nothing yet", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AlertRulesTable />);
    await clickDelete(user);

    const dialog = screen.getByRole("alertdialog");
    expect(dialog).toHaveTextContent("Delete alert rule Low disk?");
    expect(dialog).toHaveTextContent("every alert it has raised");
    expect(api.writes()).toEqual([]);
  });

  it("Cancel sends nothing", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AlertRulesTable />);
    await clickDelete(user);

    await user.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => {
      expect(screen.queryByRole("alertdialog")).not.toBeInTheDocument();
    });
    expect(api.writes()).toEqual([]);
  });

  it("confirming deletes exactly the rule clicked, once", async () => {
    const user = userEvent.setup();
    renderWithProviders(<AlertRulesTable />);
    await clickDelete(user);

    await user.click(
      within(screen.getByRole("alertdialog")).getByRole("button", {
        name: "Delete",
      }),
    );

    await waitFor(() => {
      expect(api.writes()).toEqual(["DELETE /api/v1/alert-rules/rule-2"]);
    });
    // Once: a second request arriving after the first would land here.
    await new Promise((r) => setTimeout(r, 100));
    expect(api.writes()).toEqual(["DELETE /api/v1/alert-rules/rule-2"]);
  });
});
