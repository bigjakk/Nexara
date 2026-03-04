import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { VMActions } from "./VMActions";

const defaultProps = {
  clusterId: "c1",
  resourceId: "vm-1",
  kind: "vm" as const,
  status: "running",
  name: "test-vm",
  onClone: vi.fn(),
  onMigrate: vi.fn(),
  onDestroy: vi.fn(),
};

describe("VMActions", () => {
  it("shows shutdown, reboot, stop, reset, suspend for running VM", () => {
    renderWithProviders(<VMActions {...defaultProps} />);
    expect(screen.getByRole("button", { name: /shutdown/i })).toBeEnabled();
    expect(screen.getByRole("button", { name: /reboot/i })).toBeEnabled();
    expect(screen.getByRole("button", { name: /stop/i })).toBeEnabled();
    expect(screen.getByRole("button", { name: /reset/i })).toBeEnabled();
    expect(screen.getByRole("button", { name: /suspend/i })).toBeEnabled();
    expect(screen.queryByRole("button", { name: /^start$/i })).toBeNull();
  });

  it("shows start for stopped VM", () => {
    renderWithProviders(
      <VMActions {...defaultProps} status="stopped" />,
    );
    expect(screen.getByRole("button", { name: /start/i })).toBeEnabled();
    expect(screen.queryByRole("button", { name: /shutdown/i })).toBeNull();
  });

  it("does not show reset for CT", () => {
    renderWithProviders(
      <VMActions {...defaultProps} kind="ct" status="running" />,
    );
    expect(screen.queryByRole("button", { name: /reset/i })).toBeNull();
  });

  it("shows migrate button only for CT", () => {
    const { unmount } = renderWithProviders(
      <VMActions {...defaultProps} kind="ct" />,
    );
    expect(screen.getByRole("button", { name: /migrate/i })).toBeInTheDocument();
    unmount();

    renderWithProviders(<VMActions {...defaultProps} kind="vm" />);
    expect(screen.queryByRole("button", { name: /migrate/i })).toBeNull();
  });

  it("disables destroy when running", () => {
    renderWithProviders(<VMActions {...defaultProps} status="running" />);
    expect(screen.getByRole("button", { name: /destroy/i })).toBeDisabled();
  });

  it("enables destroy when stopped", () => {
    renderWithProviders(<VMActions {...defaultProps} status="stopped" />);
    expect(screen.getByRole("button", { name: /destroy/i })).toBeEnabled();
  });
});
