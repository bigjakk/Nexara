import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { VMGuestToolsCard } from "./VMGuestToolsCard";
import {
  useGuestToolsGuest,
  useDetectGuestTools,
  useStageGuestToolsUpdate,
  useCancelGuestToolsUpdate,
  useSetGuestToolsPolicy,
} from "../api/guest-tools-queries";
import { useVirtioWinReleases } from "../api/virtio-win-queries";
import { usePermissions } from "@/hooks/usePermissions";
import type { GuestToolsGuest } from "../types/guest-tools";

vi.mock("../api/guest-tools-queries", () => ({
  useGuestToolsGuest: vi.fn(),
  useDetectGuestTools: vi.fn(),
  useStageGuestToolsUpdate: vi.fn(),
  useCancelGuestToolsUpdate: vi.fn(),
  useSetGuestToolsPolicy: vi.fn(),
}));
vi.mock("../api/virtio-win-queries", () => ({ useVirtioWinReleases: vi.fn() }));
vi.mock("@/hooks/usePermissions", () => ({ usePermissions: vi.fn() }));

const guest: GuestToolsGuest = {
  vmid: 100,
  name: "server2022",
  node: "pve1",
  status: "running",
  template: false,
  installed_version: "0.1.285",
  agent_version: "110.0.2",
  agent_running: true,
  detected_at: "2026-09-01T00:00:00Z",
  stage: "idle",
  reboot_required: false,
  staged_version: "",
  staged_at: null,
  last_error: "",
  last_result_at: null,
  excluded: false,
  policy_target_version: "",
  note: "",
  target_version: "0.1.302-1",
  up_to_date: false,
  needs_update: true,
};

function mount(
  overrides: Partial<GuestToolsGuest> = {},
  opts: { windows?: boolean; canDo?: boolean } = {},
) {
  vi.mocked(useGuestToolsGuest).mockReturnValue({
    guest: { ...guest, ...overrides },
    isLoading: false,
  } as unknown as ReturnType<typeof useGuestToolsGuest>);
  vi.mocked(useVirtioWinReleases).mockReturnValue({
    data: [],
    isLoading: false,
  } as unknown as ReturnType<typeof useVirtioWinReleases>);
  const idle = { mutateAsync: vi.fn(), isPending: false };
  vi.mocked(useDetectGuestTools).mockReturnValue(
    idle as unknown as ReturnType<typeof useDetectGuestTools>,
  );
  vi.mocked(useStageGuestToolsUpdate).mockReturnValue(
    idle as unknown as ReturnType<typeof useStageGuestToolsUpdate>,
  );
  vi.mocked(useCancelGuestToolsUpdate).mockReturnValue(
    idle as unknown as ReturnType<typeof useCancelGuestToolsUpdate>,
  );
  vi.mocked(useSetGuestToolsPolicy).mockReturnValue(
    idle as unknown as ReturnType<typeof useSetGuestToolsPolicy>,
  );
  vi.mocked(usePermissions).mockReturnValue({
    canExecute: () => opts.canDo ?? true,
    canManage: () => opts.canDo ?? true,
  } as unknown as ReturnType<typeof usePermissions>);

  const ostype = opts.windows === false ? "l26" : "win11";
  renderWithProviders(
    <VMGuestToolsCard
      clusterId="c1"
      vmid={100}
      configOstype={ostype}
      ostype={ostype}
    />,
  );
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

  // Guest tools are a Windows-only concept; the card must not appear elsewhere.
  it("renders nothing for a non-Windows guest", () => {
    mount({}, { windows: false });
    expect(screen.queryByText("Guest Tools")).not.toBeInTheDocument();
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

  // The hook is called before the Windows check (rules of hooks), so it must be
  // gated: otherwise every Linux VM detail page fires a cluster-wide request,
  // and 403s it for anyone without view:guest_tools.
  it("does not query guest tools for a non-Windows guest", () => {
    mount({}, { windows: false });
    const call = vi.mocked(useGuestToolsGuest).mock.calls[0];
    expect(call?.[2]).toBe(false);
  });
});
