import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { DiskMoveOptions } from "./DiskMoveOptions";

/**
 * The delete-source control is the one option here with a destructive meaning
 * that VARIES by caller: for a disk move it drops the source volume, for a
 * cross-cluster migration it destroys the source guest, and for a plain
 * node-to-node migration it means nothing at all — the backend never reads it.
 *
 * It was rendered unconditionally until a user asked why a same-cluster live
 * migration offered to delete a source that does not exist. These two cases pin
 * the visibility contract so the default stays "show" for the per-disk dialogs
 * that depend on it (see DiskActions.test.tsx) while the migration dialogs can
 * opt out.
 */
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
