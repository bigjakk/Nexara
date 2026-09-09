import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { NodeTemperatures } from "./NodeTemperatures";
import type {
  NodeSensorReading,
  NodeSensorsResponse,
} from "../../api/cluster-queries";

vi.mock("@/lib/api-client", async () => {
  const actual =
    await vi.importActual<typeof import("@/lib/api-client")>(
      "@/lib/api-client",
    );
  return { ...actual, apiClient: { get: vi.fn() } };
});

const mockedGet = vi.mocked(apiClient.get);

const CLUSTER = "c0000000-0000-4000-8000-000000000001";
const NODE = "pve-01";

function reading(over: Partial<NodeSensorReading> = {}): NodeSensorReading {
  return {
    chip: "coretemp",
    device: "hwmon1",
    key: "temp1",
    label: "Package id 0",
    temp_c: 45,
    ...over,
  };
}

function sensors(items: NodeSensorReading[]): NodeSensorsResponse {
  return { items, total: items.length, available: true };
}

function render(online = true) {
  return renderWithProviders(
    <NodeTemperatures clusterId={CLUSTER} nodeName={NODE} online={online} />,
  );
}

describe("NodeTemperatures", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows one row per device, headed by its hottest reading", async () => {
    mockedGet.mockResolvedValue(
      sensors([
        reading({ key: "temp1", label: "Package id 0", temp_c: 45 }),
        reading({ key: "temp2", label: "Core 0", temp_c: 51.5 }),
        reading({
          chip: "nvme",
          device: "hwmon2",
          label: "Composite",
          temp_c: 38.9,
        }),
      ]),
    );
    render();

    expect(await screen.findByText("coretemp")).toBeInTheDocument();
    expect(screen.getByText("nvme")).toBeInTheDocument();
    // The hotter core, not the package reading that happens to come first.
    expect(screen.getByText("51.5 °C")).toBeInTheDocument();
    expect(screen.getByText("38.9 °C")).toBeInTheDocument();
    // Collapsed: the cooler sibling is not on screen until asked for.
    expect(screen.queryByText("45.0 °C")).not.toBeInTheDocument();
  });

  it("reveals a device's individual sensors when expanded", async () => {
    mockedGet.mockResolvedValue(
      sensors([
        reading({ key: "temp1", label: "Package id 0", temp_c: 45 }),
        reading({ key: "temp2", label: "Core 0", temp_c: 51.5 }),
      ]),
    );
    render();

    await userEvent.click(await screen.findByRole("button", { name: /\+1/ }));

    expect(screen.getByText("Package id 0")).toBeInTheDocument();
    expect(screen.getByText("Core 0")).toBeInTheDocument();
    expect(screen.getByText("45.0 °C")).toBeInTheDocument();
  });

  it("labels a sensor by its sysfs key when the kernel publishes no label", async () => {
    mockedGet.mockResolvedValue(
      sensors([
        reading({ chip: "acpitz", device: "hwmon0", label: "", temp_c: 27.8 }),
      ]),
    );
    render();

    expect(await screen.findByText("acpitz")).toBeInTheDocument();
    expect(screen.getByText("27.8 °C")).toBeInTheDocument();
  });

  // Two NVMe drives both publish the chip name "nvme". Without the device
  // suffix the panel would show two identical rows and give no way to tell
  // which drive is the hot one.
  it("disambiguates two devices that share a chip name", async () => {
    mockedGet.mockResolvedValue(
      sensors([
        reading({ chip: "nvme", device: "hwmon2", label: "", temp_c: 38 }),
        reading({ chip: "nvme", device: "hwmon3", label: "", temp_c: 61 }),
      ]),
    );
    render();

    expect(await screen.findByText("nvme (hwmon2)")).toBeInTheDocument();
    expect(screen.getByText("nvme (hwmon3)")).toBeInTheDocument();
  });

  it("explains why sensors are unavailable instead of erroring", async () => {
    mockedGet.mockResolvedValue({
      items: [],
      total: 0,
      available: false,
      reason: "SSH is not configured for this cluster.",
    } satisfies NodeSensorsResponse);
    render();

    expect(
      await screen.findByText("SSH is not configured for this cluster."),
    ).toBeInTheDocument();
  });

  // Distinct from the unavailable case: the node WAS read, and it genuinely has
  // nothing to report. Nothing for the operator to fix, so it must not read as
  // a misconfiguration.
  it("distinguishes a node that reports no sensors from one it cannot read", async () => {
    mockedGet.mockResolvedValue(sensors([]));
    render();

    expect(
      await screen.findByText(/reports no temperature sensors/),
    ).toBeInTheDocument();
  });

  it("does not call the API for an offline node", async () => {
    render(false);

    expect(
      await screen.findByText(/only readable while the node is online/),
    ).toBeInTheDocument();
    expect(mockedGet).not.toHaveBeenCalled();
  });

  it("marks a reading critical only against the chip's own threshold", async () => {
    mockedGet.mockResolvedValue(
      sensors([
        // Over its crit limit.
        reading({
          chip: "nvme",
          device: "hwmon2",
          label: "Composite",
          temp_c: 86,
          crit_c: 84.9,
        }),
        // Hotter still, but the driver published no limit to judge it by, so
        // the panel declines to invent one.
        reading({
          chip: "chipset",
          device: "hwmon4",
          label: "",
          temp_c: 92,
        }),
      ]),
    );
    render();

    const critical = await screen.findByText("86.0 °C");
    expect(critical).toHaveClass("text-destructive");
    expect(critical).toHaveAttribute("title", expect.stringContaining("84.9"));

    const unjudged = screen.getByText("92.0 °C");
    expect(unjudged).not.toHaveClass("text-destructive");
    expect(unjudged).not.toHaveAttribute("title");
  });

  it("falls back to a note when the request fails", async () => {
    mockedGet.mockRejectedValue(new Error("boom"));
    render();

    await waitFor(() => {
      expect(
        screen.getByText(/Could not load temperatures/),
      ).toBeInTheDocument();
    });
  });
});
