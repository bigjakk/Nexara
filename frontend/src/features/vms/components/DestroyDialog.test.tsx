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
});
