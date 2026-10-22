import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { NodeMemoryModules } from "./NodeMemoryModules";
import type { NodeMemoryModulesResponse } from "../../api/cluster-queries";

vi.mock("@/lib/api-client", async () =>
  (await import("@/test/mocks")).apiClientMock(),
);

const mockedGet = vi.mocked(apiClient.get);
const mockedPost = vi.mocked(apiClient.post);

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const NODE = "pve-01";
const readAt = new Date(Date.now() - 2 * 3600 * 1000).toISOString();

const notRead: NodeMemoryModulesResponse = {
  items: [],
  total: 0,
  status: "never",
  installed_bytes: 0,
  slots_used: 0,
  slots_total: 0,
  max_capacity_bytes: 0,
  auto_read: true,
};

const goodRead: NodeMemoryModulesResponse = {
  items: [
    {
      locator: "A1",
      size_bytes: 32 * 2 ** 30,
      type: "DDR4",
      form_factor: "DIMM",
      speed_mts: 2933,
      configured_speed_mts: 2666,
      manufacturer: "Samsung",
      part_number: "TEST-PART-0001",
    },
    { locator: "A2", size_bytes: 0 },
  ],
  total: 2,
  status: "ok",
  attempted_at: readAt,
  read_at: readAt,
  installed_bytes: 32 * 2 ** 30,
  slots_used: 1,
  slots_total: 2,
  max_capacity_bytes: 2 ** 40,
  ecc: "Multi-bit ECC",
  auto_read: true,
};

function render(canRead: boolean) {
  return renderWithProviders(
    <NodeMemoryModules
      clusterId={CLUSTER}
      nodeName={NODE}
      online
      canRead={canRead}
    />,
  );
}

describe("NodeMemoryModules", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it.each<[string, NodeMemoryModulesResponse, string]>([
    ["never read, with the daily pass on", notRead, "once a day"],
    [
      "never read, with the pass off",
      { ...notRead, auto_read: false },
      "Read now reads it",
    ],
    [
      "unavailable before any read",
      {
        ...notRead,
        status: "unavailable",
        reason: "SSH is not configured for this cluster.",
      },
      "SSH is not configured for this cluster.",
    ],
    ["a good read", goodRead, "installed in 1 of 2 slots"],
    [
      "a failed read after a good one, which stays on show",
      { ...goodRead, status: "error", reason: "dmidecode failed on this node." },
      "The last read failed: dmidecode failed on this node.",
    ],
  ])("%s", async (_name, response, text) => {
    mockedGet.mockResolvedValue(response);
    render(false);
    expect(
      await screen.findByText(text, { exact: false }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /read now/i })).toBeNull();
  });

  it("lists every slot, an empty one included, and reads the node now on request", async () => {
    mockedGet.mockResolvedValue(goodRead);
    mockedPost.mockResolvedValue(goodRead);
    render(true);

    expect(await screen.findByText("TEST-PART-0001")).toBeInTheDocument();
    expect(screen.getByText("Empty")).toBeInTheDocument();
    expect(screen.getByText("2666 of 2933 MT/s")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /read now/i }));
    expect(mockedPost).toHaveBeenCalledWith(
      `/api/v1/clusters/${CLUSTER}/nodes/${NODE}/memory-modules/refresh`,
    );
  });
});
