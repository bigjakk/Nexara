import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { HAMaintenanceCard } from "./HAMaintenanceCard";
import { useAuth } from "@/hooks/useAuth";
import { useArmHA, useDisarmHA } from "../api/ha-queries";

vi.mock("@/hooks/useAuth", () => ({ useAuth: vi.fn() }));
vi.mock("../api/ha-queries", () => ({
  useArmHA: vi.fn(),
  useDisarmHA: vi.fn(),
}));

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(useAuth).mockReturnValue({
    canManage: () => true,
  } as unknown as ReturnType<typeof useAuth>);
  vi.mocked(useArmHA).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
  } as unknown as ReturnType<typeof useArmHA>);
  vi.mocked(useDisarmHA).mockReturnValue({
    mutate: vi.fn(),
    isPending: false,
  } as unknown as ReturnType<typeof useDisarmHA>);
});

describe("HAMaintenanceCard", () => {
  it("renders nothing on PVE < 9.2", () => {
    const { container } = renderWithProviders(
      <HAMaintenanceCard clusterId="c1" pveVersion="9.1.2" />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("shows arm/disarm controls on PVE 9.2+ with manage:ha", () => {
    renderWithProviders(<HAMaintenanceCard clusterId="c1" pveVersion="9.2.0" />);
    expect(screen.getByRole("button", { name: /Disarm HA/i })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Re-arm HA/i })).toBeInTheDocument();
  });

  it("renders nothing without manage:ha permission", () => {
    vi.mocked(useAuth).mockReturnValue({
      canManage: () => false,
    } as unknown as ReturnType<typeof useAuth>);
    const { container } = renderWithProviders(
      <HAMaintenanceCard clusterId="c1" pveVersion="9.2.0" />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});
