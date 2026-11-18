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
      // Proxmox refuses Start for a VM whose process runs.
      name: "a paused VM leads with Resume, not a Start Proxmox refuses",
      props: { status: "suspended" },
      button: "Resume",
      others: ["Stop", "Reset"],
    },
    {
      name: "a VM asleep in S3 leads with Resume, without Shutdown",
      props: { status: "running", runState: "suspended" },
      button: "Resume",
      others: ["Stop", "Reset"],
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

  // Shutdown is Proxmox's noun on the button; the question needs the verb.
  it.each([
    { action: "Stop", inMenu: true, title: "Stop test-vm?" },
    { action: "Shutdown", inMenu: false, title: "Shut down test-vm?" },
  ])(
    "asks before $action, naming the guest",
    async ({ action, inMenu, title }) => {
      renderWithProviders(<VMActions {...defaultProps} />);
      if (inMenu) {
        await userEvent.click(
          screen.getByRole("button", { name: "More power actions" }),
        );
        await userEvent.click(screen.getByRole("menuitem", { name: action }));
      } else {
        await userEvent.click(screen.getByRole("button", { name: action }));
      }
      expect(
        await screen.findByRole("dialog", { name: title }),
      ).toBeInTheDocument();
    },
  );

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
