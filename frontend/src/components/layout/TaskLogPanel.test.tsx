import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import {
  haStartEntries,
  removedClusterEntry,
} from "@/test/audit-entry.fixtures";
import type { AuditLogEntry } from "@/features/audit/api/audit-queries";
import { useTaskLogStore } from "@/stores/task-log-store";
import { TaskLogPanel } from "./TaskLogPanel";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

describe("TaskLogPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useTaskLogStore.setState({ panelOpen: true, focusedTask: null });
  });

  it("lists the entry of a removed cluster and opens or focuses it without asking for its task log", async () => {
    vi.mocked(apiClient.list).mockResolvedValue([removedClusterEntry]);
    const { queryClient } = renderWithProviders(<TaskLogPanel />);

    fireEvent.click(await screen.findByText(/linux01/));
    expect(await screen.findByText("vm / r1")).toBeInTheDocument();
    // The cluster is named in words in the column and in the row's details.
    expect(screen.getAllByText("cluster01 (removed)")).toHaveLength(2);

    // The expanded row mounted its log query, and for a cluster that is gone it never ran.
    const logs = queryClient
      .getQueryCache()
      .findAll({ queryKey: ["task-log"] });
    expect(logs.length).toBeGreaterThan(0);
    for (const q of logs) {
      expect(q.state.fetchStatus).toBe("idle");
      expect(q.state.status).toBe("pending");
    }
    expect(apiClient.list).toHaveBeenCalledTimes(1);

    // Focusing the task would open its log from that cluster as well.
    fireEvent.doubleClick(screen.getByText(/linux01/));
    expect(useTaskLogStore.getState().focusedTask).toBeNull();
  });

  it("marks an HA request and the HA manager's task, and says who asked for it", async () => {
    vi.mocked(apiClient.list).mockResolvedValue(haStartEntries);
    renderWithProviders(<TaskLogPanel />);

    expect(await screen.findByText("HA request")).toBeInTheDocument();
    expect(screen.getByText("HA manager · for Ops")).toBeInTheDocument();
  });

  describe("running tasks and the error filter", () => {
    const row = (
      name: string,
      createdAt: string,
      over: Partial<AuditLogEntry>,
    ): AuditLogEntry => ({
      ...removedClusterEntry,
      id: name,
      cluster_removed: false,
      details: JSON.stringify({ upid: `UPID:${name}` }),
      created_at: createdAt,
      resource_name: name,
      ...over,
    });
    const completed = row("linux03", "2026-09-01T12:00:00Z", {
      task_status: "completed",
    });
    const entries = [
      completed,
      row("linux02", "2026-09-01T11:00:00Z", { task_status: "failed" }),
      row("linux01", "2026-09-01T10:00:00Z", { task_status: "running" }),
    ];
    const shown = () =>
      screen
        .queryAllByText(/linux0\d/)
        .map((el) => /linux0\d/.exec(el.textContent)?.[0]);

    beforeEach(() => {
      vi.mocked(apiClient.get).mockResolvedValue({
        status: "running",
        exit_status: "",
      });
    });

    it("lists a running task first and filters on the error count without collapsing", async () => {
      vi.mocked(apiClient.list).mockResolvedValue(entries);
      renderWithProviders(<TaskLogPanel />);
      // Pinned once its status poll has confirmed it.
      await waitFor(() => {
        expect(shown()).toEqual(["linux01", "linux03", "linux02"]);
      });

      fireEvent.click(screen.getByRole("button", { name: "1 error" }));
      expect(shown()).toEqual(["linux02"]);
      expect(useTaskLogStore.getState().panelOpen).toBe(true);

      fireEvent.click(screen.getByRole("button", { name: "Others" }));
      expect(shown()).toEqual(["linux01", "linux03"]);

      // The selected segment toggles back to everything.
      fireEvent.click(screen.getByRole("button", { name: "Others" }));
      expect(shown()).toHaveLength(3);
    });

    it("unpins a task whose status poll starts failing", async () => {
      vi.mocked(apiClient.list).mockResolvedValue(entries);
      const { queryClient } = renderWithProviders(<TaskLogPanel />);
      await waitFor(() => {
        expect(shown()[0]).toBe("linux01");
      });

      vi.mocked(apiClient.get).mockRejectedValue(new Error("no route to host"));
      await queryClient.refetchQueries({ queryKey: ["clusters"] });
      await waitFor(() => {
        expect(shown()[0]).toBe("linux03");
      });
    });

    it("keeps a task's failure when a refetch fails after it stopped", async () => {
      vi.mocked(apiClient.list).mockResolvedValue([
        row("linux01", "2026-09-01T10:00:00Z", { task_status: "running" }),
        row("linux02", "2026-09-01T11:00:00Z", { task_status: "running" }),
      ]);
      vi.mocked(apiClient.get).mockImplementation((path) =>
        Promise.resolve(
          path.includes("linux02")
            ? { status: "stopped", exit_status: "disk full" }
            : { status: "running", exit_status: "" },
        ),
      );
      const { queryClient } = renderWithProviders(<TaskLogPanel />);
      await screen.findByRole("button", { name: "1 error" });
      await waitFor(() => {
        expect(shown()[0]).toBe("linux01");
      });

      vi.mocked(apiClient.get).mockRejectedValue(new Error("no route to host"));
      await queryClient.refetchQueries({ queryKey: ["clusters"] });
      // linux01 unpinning is the sign the failed refetches have reached the screen.
      await waitFor(() => {
        expect(shown()[0]).toBe("linux02");
      });
      expect(
        screen.getByRole("button", { name: "1 error" }),
      ).toBeInTheDocument();
    });

    it("opens a collapsed drawer on the list that was asked for", async () => {
      vi.mocked(apiClient.list).mockResolvedValue(entries);
      useTaskLogStore.setState({ panelOpen: false });
      renderWithProviders(<TaskLogPanel />);

      fireEvent.click(await screen.findByRole("button", { name: "1 error" }));
      expect(useTaskLogStore.getState().panelOpen).toBe(true);
      expect(shown()).toEqual(["linux02"]);
    });

    it("counts an error with no task behind it, and filters to it", async () => {
      vi.mocked(apiClient.list).mockResolvedValue([
        completed,
        row("linux04", "2026-09-01T11:00:00Z", {
          action: "tls_fingerprint_mismatch",
          details: JSON.stringify({ error: "certificate changed" }),
        }),
      ]);
      renderWithProviders(<TaskLogPanel />);

      fireEvent.click(await screen.findByRole("button", { name: "1 error" }));
      expect(shown()).toEqual(["linux04"]);
    });

    it("claims no count until the feed has answered", async () => {
      vi.mocked(apiClient.list).mockRejectedValue(new Error("bad gateway"));
      const { queryClient } = renderWithProviders(<TaskLogPanel />);

      await waitFor(() => {
        expect(queryClient.getQueryState(["recent-activity"])?.status).toBe(
          "error",
        );
      });
      expect(
        screen.getByRole("button", { name: "Errors" }),
      ).toBeInTheDocument();
    });

    it("keeps the filter with no errors to count, says so when it matches nothing, and leaves the way back", async () => {
      vi.mocked(apiClient.list).mockResolvedValue([completed]);
      renderWithProviders(<TaskLogPanel />);
      await screen.findByText(/linux03/);

      fireEvent.click(screen.getByRole("button", { name: "0 errors" }));
      expect(
        screen.getByText("No recent activity matches this filter"),
      ).toBeInTheDocument();
      expect(screen.getByRole("button", { name: "All" })).toBeInTheDocument();
    });
  });
});
