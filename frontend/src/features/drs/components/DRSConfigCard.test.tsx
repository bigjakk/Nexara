import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { grantPermissions } from "@/test/permissions";
import { renderWithProviders } from "@/test/test-utils";
import { DRSConfigCard } from "./DRSConfigCard";
import { useDRSConfig, useUpdateDRSConfig } from "../api/drs-queries";
import type { DRSConfig, NativeCRSStatus } from "../types/drs";

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
  exclude_veeam_workers: true,
  boot_holdoff_minutes: 10,
  created_at: "",
  updated_at: "",
};

/** Proxmox's own balancer rebalancing on its own, as the CRS banner reads it. */
const NATIVE_REBALANCING: NativeCRSStatus = {
  ha: "dynamic",
  auto_rebalance: true,
  threshold: 30,
  hold_duration: 3,
  margin: 10,
  method: "bruteforce",
  rebalance_on_start: false,
};

function mockHooks(
  config: DRSConfig,
  mutateAsync = vi.fn().mockResolvedValue(undefined),
) {
  vi.mocked(useDRSConfig).mockReturnValue({
    data: config,
    isLoading: false,
  } as unknown as ReturnType<typeof useDRSConfig>);
  vi.mocked(useUpdateDRSConfig).mockReturnValue({
    mutateAsync,
    isPending: false,
    error: null,
  } as unknown as ReturnType<typeof useUpdateDRSConfig>);
}

describe("DRSConfigCard native-CRS coexistence", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    grantPermissions("manage:drs");
  });

  it("shows the native-CRS banner and disables Automatic when native auto-rebalance is active", async () => {
    mockHooks({ ...baseConfig, native_crs: NATIVE_REBALANCING });
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

describe("DRSConfigCard boot hold-off", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    grantPermissions("manage:drs");
  });

  it("shows the stored hold-off and saves the edited one", async () => {
    const mutateAsync = vi.fn().mockResolvedValue(undefined);
    mockHooks({ ...baseConfig, boot_holdoff_minutes: 15 }, mutateAsync);
    const user = userEvent.setup();
    renderWithProviders(<DRSConfigCard clusterId="c1" />);

    const input = screen.getByLabelText("Hold-off after a node boots");
    expect(input).toHaveValue(15);
    await user.clear(input);
    await user.type(input, "0");
    await user.click(
      screen.getByRole("button", { name: /Save Configuration/i }),
    );

    expect(mutateAsync).toHaveBeenCalledWith(
      expect.objectContaining({ boot_holdoff_minutes: 0 }),
    );
  });
});

// Saving takes manage:drs: without it the card is read-only. Pointing at the
// native balancer's own setting is for whoever may change it (manage:cluster).
describe("DRSConfigCard permissions", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it.each([
    { granted: [] as string[], editable: false, crsLink: false },
    { granted: ["manage:drs"], editable: true, crsLink: false },
    { granted: ["manage:cluster"], editable: false, crsLink: true },
  ])(
    "with $granted: editable $editable, CRS link $crsLink",
    ({ granted, editable, crsLink }) => {
      grantPermissions(...granted);
      mockHooks({ ...baseConfig, native_crs: NATIVE_REBALANCING });
      renderWithProviders(<DRSConfigCard clusterId="c1" />);

      expect(
        screen.queryByRole("button", { name: /Save configuration/i }) !== null,
      ).toBe(editable);
      expect(screen.getByRole("combobox")).toHaveProperty(
        "disabled",
        !editable,
      );
      expect(
        screen.getByLabelText("Hold-off after a node boots"),
      ).toHaveProperty("disabled", !editable);
      expect(
        screen.queryByRole("link", { name: "Datacenter → Options → CRS" }) !==
          null,
      ).toBe(crsLink);
    },
  );
});
