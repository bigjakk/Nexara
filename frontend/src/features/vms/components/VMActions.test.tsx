import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

const menuItems = () =>
  screen.getAllByRole("menuitem").map((item) => item.textContent);

describe("VMActions", () => {
  it.each([
    {
      name: "a running VM leads with Shutdown",
      props: { status: "running" },
      button: "Shutdown",
      others: ["Reboot", "Stop", "Reset", "Suspend"],
    },
    {
      name: "a running container has no Reset",
      props: { status: "running", kind: "ct" as const },
      button: "Shutdown",
      others: ["Reboot", "Stop", "Suspend"],
    },
    {
      name: "a suspended guest leads with Resume, not a cold Start",
      props: { status: "suspended" },
      button: "Resume",
      others: ["Start"],
    },
  ])(
    "$name and folds the rest beside it",
    async ({ props, button, others }) => {
      renderWithProviders(<VMActions {...defaultProps} {...props} />);
      expect(screen.getByRole("button", { name: button })).toBeEnabled();

      await userEvent.click(
        screen.getByRole("button", { name: "More power actions" }),
      );
      expect(menuItems()).toEqual(others);
    },
  );

  it("asks before a destructive power action chosen from the menu", async () => {
    renderWithProviders(<VMActions {...defaultProps} />);
    await userEvent.click(
      screen.getByRole("button", { name: "More power actions" }),
    );
    await userEvent.click(screen.getByRole("menuitem", { name: "Stop" }));
    expect(
      await screen.findByRole("dialog", { name: "Confirm Stop" }),
    ).toBeInTheDocument();
  });

  it("offers a stopped guest Start alone", () => {
    renderWithProviders(<VMActions {...defaultProps} status="stopped" />);
    expect(screen.getByRole("button", { name: "Start" })).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: "More power actions" }),
    ).toBeNull();
  });

  it("keeps Snapshot and Migrate one click away, and Snapshot off a template", () => {
    const { unmount } = renderWithProviders(
      <VMActions {...defaultProps} onSnapshot={vi.fn()} />,
    );
    expect(screen.getByRole("button", { name: "Snapshot" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Migrate" })).toBeEnabled();
    unmount();

    renderWithProviders(
      <VMActions {...defaultProps} template onSnapshot={vi.fn()} />,
    );
    expect(screen.queryByRole("button", { name: "Snapshot" })).toBeNull();
  });

  it.each([
    { status: "running", destroyDisabled: "true" },
    { status: "stopped", destroyDisabled: null },
  ])(
    "puts Clone and Destroy under More, Destroy unavailable while running ($status)",
    async ({ status, destroyDisabled }) => {
      const onClone = vi.fn();
      renderWithProviders(
        <VMActions {...defaultProps} status={status} onClone={onClone} />,
      );
      await userEvent.click(
        screen.getByRole("button", { name: "More actions" }),
      );

      expect(
        screen
          .getByRole("menuitem", { name: "Destroy" })
          .getAttribute("aria-disabled"),
      ).toBe(destroyDisabled);
      await userEvent.click(screen.getByRole("menuitem", { name: "Clone" }));
      expect(onClone).toHaveBeenCalledOnce();
    },
  );
});
