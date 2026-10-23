import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { BlockJobsCard } from "./BlockJobsCard";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedGet = vi.mocked(apiClient.get);

describe("BlockJobsCard", () => {
  beforeEach(() => {
    mockedGet.mockReset();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows each job's progress, and what QEMU printed that it cannot read", async () => {
    mockedGet.mockResolvedValue({
      running: true,
      jobs: [
        {
          type: "mirror",
          device: "mirror-scsi0",
          completed: 268435456,
          total: 1073741824,
          speed_limit: 0,
        },
      ],
      unparsed: ["A line QEMU has never printed"],
    });
    renderWithProviders(<BlockJobsCard clusterId="c1" vmId="v1" />);

    const job = await screen.findByTestId("block-job");
    expect(job).toHaveTextContent("mirror");
    expect(job).toHaveTextContent("256.0 MiB of 1.0 GiB (25.0%)");
    expect(job).toHaveTextContent("Speed limit: none");
    expect(
      screen.getByText("A line QEMU has never printed"),
    ).toBeInTheDocument();
  });

  it("shows a job's rate and time left once a second read has measured it", async () => {
    // Only Date is faked: it is what stamps each fetch's dataUpdatedAt.
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-01-01T00:00:00Z"));
    const job = (mib: number) => ({
      running: true,
      jobs: [
        {
          type: "mirror",
          device: "mirror-scsi0",
          completed: mib * 1048576,
          total: 8 * 1048576,
          speed_limit: 0,
        },
      ],
      unparsed: [],
    });
    mockedGet.mockResolvedValueOnce(job(1));
    const { queryClient, rerender } = renderWithProviders(
      <BlockJobsCard clusterId="c1" vmId="v1" />,
    );
    await screen.findByTestId("block-job");
    expect(screen.queryByText(/Rate:/)).not.toBeInTheDocument();

    vi.setSystemTime(new Date("2026-01-01T00:00:02Z"));
    mockedGet.mockResolvedValueOnce(job(4));
    await queryClient.refetchQueries();
    expect(await screen.findByText("Rate: 1.5 MiB/s")).toBeInTheDocument();
    expect(screen.getByText("About 3 s left")).toBeInTheDocument();
    // A render with no new fetch keeps the pace it measured.
    rerender(<BlockJobsCard clusterId="c1" vmId="v1" />);
    expect(screen.getByText("Rate: 1.5 MiB/s")).toBeInTheDocument();
  });

  it.each([
    [{ running: true, jobs: [], unparsed: [] }, "No active block jobs"],
    [{ running: false, jobs: [], unparsed: [] }, "Not running"],
  ])("says so when there is nothing to show (%#)", async (reply, text) => {
    mockedGet.mockResolvedValue(reply);
    renderWithProviders(<BlockJobsCard clusterId="c1" vmId="v1" />);
    expect(await screen.findByText(text)).toBeInTheDocument();
  });
});
