import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { renderWithProviders } from "@/test/test-utils";
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
