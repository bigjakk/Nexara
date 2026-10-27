import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { detachedClusterEntry } from "@/test/audit-entry.fixtures";
import { useTaskLogStore } from "@/stores/task-log-store";
import { TaskLogPanel } from "./TaskLogPanel";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

describe("TaskLogPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useTaskLogStore.setState({ panelOpen: true });
  });

  it("lists the entry of a removed cluster and opens it without asking for its task log", async () => {
    vi.mocked(apiClient.list).mockResolvedValue([detachedClusterEntry]);
    const { queryClient } = renderWithProviders(<TaskLogPanel />);

    fireEvent.click(await screen.findByText(/linux01/));
    expect(await screen.findByText("vm / r1")).toBeInTheDocument();

    // The expanded row mounted its log query, and with no cluster it never ran.
    const logs = queryClient.getQueryCache().findAll({ queryKey: ["task-log"] });
    expect(logs.length).toBeGreaterThan(0);
    for (const q of logs) {
      expect(q.state.fetchStatus).toBe("idle");
      expect(q.state.status).toBe("pending");
    }
    expect(apiClient.list).toHaveBeenCalledTimes(1);
  });
});
