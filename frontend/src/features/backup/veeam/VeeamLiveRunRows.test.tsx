import { expect, it, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { VeeamLiveRunRows } from "./VeeamLiveRunRows";
import { session } from "./veeam.fixtures";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

// A job's History lists its own runs above the first page, from the job's
// runs Nexara has collected — asked for by the job, so a busy server's newer
// runs of other jobs do not push them out — and how each ended.
it("lists the job's runs and how they ended", async () => {
  vi.mocked(apiClient.list).mockReset();
  vi.mocked(apiClient.list).mockResolvedValue([
    session({ id: "r1", result: "Warning" }),
    session({
      id: "r3",
      state: "Working",
      result: "",
      creation_time: "2026-08-27T01:00:00Z",
    }),
  ]);
  renderWithProviders(
    <table>
      <tbody>
        <VeeamLiveRunRows
          serverId="srv-1"
          jobId="j1"
          since={null}
          colSpan={6}
        />
      </tbody>
    </table>,
  );

  const rows = await screen.findAllByTestId("veeam-live-run");
  expect(String(vi.mocked(apiClient.list).mock.calls[0]?.[0])).toBe(
    "/api/v1/veeam-servers/srv-1/sessions?job_id=j1",
  );
  expect(rows).toHaveLength(2);
  expect(
    within(rows[0] as HTMLElement).getByText("Working"),
  ).toBeInTheDocument();
  expect(
    within(rows[1] as HTMLElement).getByText("Warning"),
  ).toBeInTheDocument();
});

// The listing is keyed by the job asked for, so two jobs of one server open at
// once do not share a cache entry.
it("keeps each job's runs apart", async () => {
  vi.mocked(apiClient.list).mockReset();
  vi.mocked(apiClient.list).mockImplementation((path) =>
    Promise.resolve(
      String(path).endsWith("job_id=j1")
        ? [session({ id: "r1", result: "Warning" })]
        : [session({ id: "r2", result: "Failed" })],
    ),
  );
  renderWithProviders(
    <table>
      <tbody>
        <VeeamLiveRunRows
          serverId="srv-1"
          jobId="j1"
          since={null}
          colSpan={6}
        />
        <VeeamLiveRunRows
          serverId="srv-1"
          jobId="j2"
          since={null}
          colSpan={6}
        />
      </tbody>
    </table>,
  );

  await waitFor(() => {
    expect(screen.getAllByTestId("veeam-live-run")).toHaveLength(2);
  });
  const rows = screen.getAllByTestId("veeam-live-run");
  expect(
    within(rows[0] as HTMLElement).getByText("Warning"),
  ).toBeInTheDocument();
  expect(
    within(rows[1] as HTMLElement).getByText("Failed or cancelled"),
  ).toBeInTheDocument();
});
