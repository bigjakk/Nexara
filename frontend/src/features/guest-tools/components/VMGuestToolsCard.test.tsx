import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { VMGuestToolsCard } from "./VMGuestToolsCard";
import { useGuestToolsGuest } from "../api/guest-tools-queries";
import { useVirtioWinReleases } from "../api/virtio-win-queries";
import type { GuestToolsGuest } from "../types/guest-tools";
import { outdatedGuest } from "../guest-tools.fixtures";
import { mockGuestToolsMutations, mockPermissions } from "../guest-tools.mocks";

vi.mock("../api/guest-tools-queries", () => ({
  useGuestToolsGuest: vi.fn(),
  useDetectGuestTools: vi.fn(),
  useStageGuestToolsUpdate: vi.fn(),
  useCancelGuestToolsUpdate: vi.fn(),
  useSetGuestToolsPolicy: vi.fn(),
}));
vi.mock("../api/virtio-win-queries", () => ({ useVirtioWinReleases: vi.fn() }));
vi.mock("@/hooks/usePermissions", () => ({ usePermissions: vi.fn() }));

function mount(
  overrides: Partial<GuestToolsGuest> = {},
  opts: { canDo?: boolean } = {},
) {
  vi.mocked(useGuestToolsGuest).mockReturnValue({
    guest: outdatedGuest(overrides),
    isLoading: false,
  } as unknown as ReturnType<typeof useGuestToolsGuest>);
  vi.mocked(useVirtioWinReleases).mockReturnValue({
    data: [],
    isLoading: false,
  } as unknown as ReturnType<typeof useVirtioWinReleases>);
  mockGuestToolsMutations();
  mockPermissions("guest_tools", opts.canDo ?? true);

  renderWithProviders(<VMGuestToolsCard clusterId="c1" vmid={100} />);
}

describe("VMGuestToolsCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("shows the installed and target versions", () => {
    mount();
    expect(screen.getByText("0.1.285")).toBeInTheDocument();
    expect(screen.getByText(/0\.1\.302-1/)).toBeInTheDocument();
    expect(screen.getByText("Update available")).toBeInTheDocument();
  });

  it("offers both staging and an immediate update", () => {
    mount();
    expect(screen.getByRole("button", { name: /at next boot/i })).toBeEnabled();
    expect(screen.getByRole("button", { name: /Update now/i })).toBeEnabled();
  });

  // Forcing must stay available on a current guest, for repairing a broken
  // driver install.
  it("offers a reinstall when the guest is already up to date", () => {
    mount({
      installed_version: "0.1.302",
      up_to_date: true,
      needs_update: false,
    });
    expect(
      screen.getByRole("button", { name: /Reinstall at next boot/i }),
    ).toBeEnabled();
  });

  it("offers an install when the guest has no tools at all", () => {
    mount({ installed_version: "", up_to_date: false, needs_update: false });
    expect(
      screen.getByRole("button", { name: /Install at next boot/i }),
    ).toBeEnabled();
  });

  it("swaps to cancel while an update is staged", () => {
    mount({ stage: "staged", staged_version: "0.1.302-1" });
    expect(
      screen.getByRole("button", { name: /Cancel staged update/i }),
    ).toBeEnabled();
    expect(
      screen.queryByRole("button", { name: /at next boot/i }),
    ).not.toBeInTheDocument();
  });

  it("disables actions on a stopped guest but still shows its last known state", () => {
    mount({ status: "stopped" });
    expect(screen.getByText("0.1.285")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Re-check/i })).toBeDisabled();
    expect(
      screen.getByRole("button", { name: /at next boot/i }),
    ).toBeDisabled();
  });

  it("disables actions without permission", () => {
    mount({}, { canDo: false });
    expect(
      screen.getByRole("button", { name: /at next boot/i }),
    ).toBeDisabled();
  });

  // A positive control on the MANAGE scope specifically. The "without
  // permission" test above cannot tell the scopes apart — it withholds both —
  // so without this the card could ask for the wrong permission entirely and
  // every test here would still pass.
  it("enables the exclude toggle for a manager", () => {
    mount();
    expect(
      screen.getByLabelText(/Exclude from automatic updates/i),
    ).toBeEnabled();
  });

  // A reboot-required note is not a failure and must not read as one.
  it("renders a reboot-required note without error styling", () => {
    mount({
      reboot_required: true,
      last_error: "installed; a reboot is needed to finish replacing drivers",
    });
    const note = screen.getByText(/a reboot is needed/i);
    expect(note.className).toContain("text-muted-foreground");
    expect(note.className).not.toContain("text-destructive");
  });
});
