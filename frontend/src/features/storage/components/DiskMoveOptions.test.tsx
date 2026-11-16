import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { DiskMoveOptions } from "./DiskMoveOptions";

// Delete-source means a different destructive thing per caller (the source volume on a disk
// move, the source guest on a cross-cluster migration) and nothing on a node-to-node one.
// These pin that it shows by default, for the per-disk dialogs (DiskActions.test.tsx), and
// that the migration dialogs can hide it.
const baseProps = {
  idPrefix: "test",
  hideFormat: true,
  bwlimit: "",
  onBwlimitChange: vi.fn(),
  deleteSource: false,
  onDeleteSourceChange: vi.fn(),
};

describe("DiskMoveOptions delete-source visibility", () => {
  it("shows the control by default, so a per-disk move cannot silently orphan its source volume", () => {
    renderWithProviders(<DiskMoveOptions {...baseProps} />);
    expect(screen.getByLabelText(/delete source/i)).toBeInTheDocument();
  });

  it("omits it entirely when the caller says nothing is left behind", () => {
    renderWithProviders(<DiskMoveOptions {...baseProps} hideDeleteSource />);
    expect(screen.queryByLabelText(/delete source/i)).not.toBeInTheDocument();
  });

  it("drops the kept-source hint along with the control", () => {
    const hint = "Source volumes are kept as unused disks on the guest.";
    const { unmount } = renderWithProviders(
      <DiskMoveOptions {...baseProps} keptHint={hint} />,
    );
    expect(screen.getByText(hint)).toBeInTheDocument();
    unmount();

    renderWithProviders(
      <DiskMoveOptions {...baseProps} keptHint={hint} hideDeleteSource />,
    );
    expect(screen.queryByText(hint)).not.toBeInTheDocument();
  });

  it("still renders the options that always apply", () => {
    renderWithProviders(<DiskMoveOptions {...baseProps} hideDeleteSource />);
    expect(screen.getByLabelText(/bandwidth limit/i)).toBeInTheDocument();
  });
});
