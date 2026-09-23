import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { AuditLogPanel } from "./AuditLogPanel";
import {
  useEvents,
  useAuditActions,
  useAuditUsers,
} from "../api/events-queries";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { usePermissions } from "@/hooks/usePermissions";

vi.mock("../api/events-queries", () => ({
  useEvents: vi.fn(),
  useAuditActions: vi.fn(),
  useAuditUsers: vi.fn(),
  buildExportUrl: vi.fn(),
}));
vi.mock("@/features/dashboard/api/dashboard-queries", () => ({
  useClusters: vi.fn(),
}));
vi.mock("@/hooks/usePermissions", () => ({
  usePermissions: vi.fn(),
}));
// The card has its own tests. Here the only question is whether the panel
// mounts it.
vi.mock("./SyslogConfigCard", () => ({
  SyslogConfigCard: () => <p>syslog card</p>,
}));

/**
 * Serves an empty audit log to a user holding exactly `held`. The permission
 * answers are worked out from `held`, so a panel that asked about the wrong
 * permission would get the wrong answer rather than a blanket yes.
 */
function renderFor(held: string[]) {
  const has = (action: string, resource: string) =>
    held.includes(`${action}:${resource}`);
  vi.mocked(usePermissions).mockReturnValue({
    hasPermission: has,
    canView: (resource: string) => has("view", resource),
    canManage: (resource: string) => has("manage", resource),
  } as unknown as ReturnType<typeof usePermissions>);
  vi.mocked(useClusters).mockReturnValue({
    data: [],
  } as unknown as ReturnType<typeof useClusters>);
  vi.mocked(useAuditActions).mockReturnValue({
    data: [],
  } as unknown as ReturnType<typeof useAuditActions>);
  vi.mocked(useAuditUsers).mockReturnValue({
    data: [],
  } as unknown as ReturnType<typeof useAuditUsers>);
  vi.mocked(useEvents).mockReturnValue({
    data: { items: [], total: 0 },
    isLoading: false,
    error: null,
  } as unknown as ReturnType<typeof useEvents>);
  renderWithProviders(<AuditLogPanel />);
}

describe("AuditLogPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows the syslog card to a user who can manage the audit log", () => {
    renderFor(["view:audit", "manage:audit"]);

    expect(screen.getByText("syslog card")).toBeInTheDocument();
  });

  it("leaves the syslog card out for a Viewer, whose every read of it would 403", () => {
    // view:audit opens this tab; the syslog routes all need manage:audit.
    renderFor(["view:audit"]);

    // The panel itself did render, so the card's absence is the gate's doing.
    expect(screen.getByRole("button", { name: "CSV" })).toBeInTheDocument();
    expect(screen.queryByText("syslog card")).not.toBeInTheDocument();
  });
});
