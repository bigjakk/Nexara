import { expect, it, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { VeeamLiveRunRows } from "./VeeamLiveRunRows";
import { session } from "./veeam.fixtures";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// A job's History lists its own runs above the first page, from the runs
// Nexara has collected, and how each ended.
it("lists the job's runs and how they ended, and no other job's", async () => {
  vi.mocked(apiClient.list).mockResolvedValue([
    session({ id: "r1", job_veeam_id: "j1", result: "Warning" }),
    session({ id: "r2", job_veeam_id: "j2", result: "Failed", name: "Other" }),
    session({
      id: "r3",
      job_veeam_id: "j1",
      state: "Working",
      result: "",
      creation_time: "2026-08-27T01:00:00Z",
    }),
  ]);
  renderWithProviders(
    <table>
      <tbody>
        <VeeamLiveRunRows serverId="srv-1" jobId="j1" colSpan={6} />
      </tbody>
    </table>,
  );

  const rows = await screen.findAllByTestId("veeam-live-run");
  expect(rows).toHaveLength(2);
  expect(within(rows[0]).getByText("Working")).toBeInTheDocument();
  expect(within(rows[1]).getByText("Warning")).toBeInTheDocument();
});
