import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient, ApiClientError } from "@/lib/api-client";
import { LiveBlockJobRows } from "./LiveBlockJobRows";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedGet = vi.mocked(apiClient.get);

function rows() {
  return (
    <table>
      <tbody>
        <LiveBlockJobRows clusterId="c1" vmId="v1" colSpan={6} />
      </tbody>
    </table>
  );
}

describe("LiveBlockJobRows", () => {
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
    renderWithProviders(rows());

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
    const { queryClient, rerender } = renderWithProviders(rows());
    await screen.findByTestId("block-job");
    expect(screen.queryByText(/Rate:/)).not.toBeInTheDocument();

    vi.setSystemTime(new Date("2026-01-01T00:00:02Z"));
    mockedGet.mockResolvedValueOnce(job(4));
    await queryClient.refetchQueries();
    expect(await screen.findByText("Rate: 1.5 MiB/s")).toBeInTheDocument();
    expect(screen.getByText("About 3 s left")).toBeInTheDocument();
    // A render with no new fetch keeps the pace it measured.
    rerender(rows());
    expect(screen.getByText("Rate: 1.5 MiB/s")).toBeInTheDocument();
  });

  // Live rows sit above the history: with nothing running they are not there
  // at all, while a QEMU that did not answer is itself the answer.
  it.each([
    [{ running: true, jobs: [], unparsed: [] }, ""],
    [{ running: false, jobs: [], unparsed: [] }, ""],
    [
      new ApiClientError(502, { error: "bad_gateway", message: "got timeout" }),
      "got timeout",
    ],
  ])("renders only what there is to show (%#)", async (reply, text) => {
    if (reply instanceof Error) mockedGet.mockRejectedValue(reply);
    else mockedGet.mockResolvedValue(reply);
    const { container, queryClient } = renderWithProviders(rows());
    await vi.waitFor(() => {
      const state = queryClient.getQueryState([
        "clusters",
        "c1",
        "vms",
        "v1",
        "block-jobs",
      ]);
      expect(state?.status).not.toBe("pending");
    });
    expect(container.querySelectorAll("tr")).toHaveLength(text ? 1 : 0);
    if (text) expect(screen.getByText(text)).toBeInTheDocument();
  });
});
