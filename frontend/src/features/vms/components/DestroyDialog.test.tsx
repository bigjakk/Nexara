import { afterEach, describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
import { apiClient } from "@/lib/api-client";
import { DestroyDialog } from "./DestroyDialog";

const defaultProps = {
  open: true,
  onOpenChange: () => {},
  clusterId: "c1",
  resourceId: "vm-1",
  kind: "vm" as const,
  resourceName: "my-test-vm",
};

describe("DestroyDialog", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  // A failed destroy closed the dialog and went to the inventory, as if the
  // guest had gone; it is still there, and the banner says why.
  it("stays on the banner when the destroy task fails", async () => {
    const upid = "UPID:pve-01:00000001:00000002:00000003:qmdestroy:101:x:";
    vi.spyOn(apiClient, "delete").mockResolvedValue({ upid, status: "OK" });
    vi.spyOn(apiClient, "get").mockResolvedValue({
      upid,
      status: "stopped",
      exit_status: "VM is locked (backup)",
    });
    const onOpenChange = vi.fn<(open: boolean) => void>();
    const user = userEvent.setup();
    renderWithProviders(
      <DestroyDialog {...defaultProps} onOpenChange={onOpenChange} />,
    );
    await user.type(screen.getByPlaceholderText("my-test-vm"), "my-test-vm");
    await user.click(screen.getByRole("button", { name: /^destroy$/i }));

    expect(
      await screen.findByText("Task failed: VM is locked (backup)"),
    ).toBeInTheDocument();
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it("renders dialog with confirmation prompt", () => {
    renderWithProviders(<DestroyDialog {...defaultProps} />);
    expect(screen.getByText(/destroy vm/i)).toBeInTheDocument();
    expect(screen.getByText(/permanently destroy/i)).toBeInTheDocument();
  });

  it("disables destroy button until name typed", () => {
    renderWithProviders(<DestroyDialog {...defaultProps} />);
    const destroyBtn = screen.getByRole("button", { name: /^destroy$/i });
    expect(destroyBtn).toBeDisabled();
  });

  it("enables destroy button when name matches", async () => {
    const user = userEvent.setup();
    renderWithProviders(<DestroyDialog {...defaultProps} />);

    const input = screen.getByPlaceholderText("my-test-vm");
    await user.type(input, "my-test-vm");

    const destroyBtn = screen.getByRole("button", { name: /^destroy$/i });
    expect(destroyBtn).toBeEnabled();
  });

  it("copies the resource name when its chip is clicked", async () => {
    Object.defineProperty(window, "isSecureContext", {
      value: true,
      configurable: true,
    });
    const user = userEvent.setup();
    renderWithProviders(<DestroyDialog {...defaultProps} />);

    // The name appears in both the description and the confirm label.
    const [chip] = screen.getAllByRole("button", { name: "my-test-vm" });
    if (!chip) throw new Error("expected a copyable name chip");
    await user.click(chip);

    expect(await window.navigator.clipboard.readText()).toBe("my-test-vm");
  });
});
