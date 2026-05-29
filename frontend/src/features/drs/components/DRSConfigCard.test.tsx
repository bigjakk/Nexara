import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { DRSConfigCard } from "./DRSConfigCard";
import { useDRSConfig, useUpdateDRSConfig } from "../api/drs-queries";
import type { DRSConfig } from "../types/drs";

vi.mock("../api/drs-queries", () => ({
  useDRSConfig: vi.fn(),
  useUpdateDRSConfig: vi.fn(),
}));

const baseConfig: DRSConfig = {
  id: "d1",
  cluster_id: "c1",
  mode: "advisory",
  enabled: true,
  weights: { cpu: 0.3, memory: 0.7 },
  imbalance_threshold: 0.25,
  eval_interval_seconds: 300,
  include_containers: false,
  created_at: "",
  updated_at: "",
};

function mockHooks(config: DRSConfig) {
  vi.mocked(useDRSConfig).mockReturnValue({
    data: config,
    isLoading: false,
  } as unknown as ReturnType<typeof useDRSConfig>);
  vi.mocked(useUpdateDRSConfig).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useUpdateDRSConfig>);
}

describe("DRSConfigCard native-CRS coexistence", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows the native-CRS banner and disables Automatic when native auto-rebalance is active", async () => {
    mockHooks({
      ...baseConfig,
      native_crs: {
        ha: "dynamic",
        auto_rebalance: true,
        threshold: 30,
        hold_duration: 3,
        margin: 10,
        method: "bruteforce",
        rebalance_on_start: false,
      },
    });
    const user = userEvent.setup();
    renderWithProviders(<DRSConfigCard clusterId="c1" />);

    expect(
      screen.getByText(/native Dynamic Load Balancer is active/i),
    ).toBeInTheDocument();

    // Open the Mode select; the Automatic option must be disabled.
    await user.click(screen.getByRole("combobox"));
    const autoOption = screen.getByRole("option", { name: /Automatic/i });
    expect(autoOption).toHaveAttribute("aria-disabled", "true");
  });

  it("hides the banner when native CRS is not auto-rebalancing", () => {
    mockHooks({ ...baseConfig });
    renderWithProviders(<DRSConfigCard clusterId="c1" />);
    expect(
      screen.queryByText(/native Dynamic Load Balancer is active/i),
    ).not.toBeInTheDocument();
  });
});
