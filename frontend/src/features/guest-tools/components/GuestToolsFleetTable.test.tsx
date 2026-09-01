import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { GuestToolsFleetTable } from "./GuestToolsFleetTable";
import {
  useGuestToolsFleet,
  useDetectGuestTools,
  useStageGuestToolsUpdate,
  useCancelGuestToolsUpdate,
  useSetGuestToolsPolicy,
} from "../api/guest-tools-queries";
import { usePermissions } from "@/hooks/usePermissions";
import type { GuestToolsGuest } from "../types/guest-tools";

vi.mock("../api/guest-tools-queries", () => ({
  useGuestToolsFleet: vi.fn(),
  useDetectGuestTools: vi.fn(),
  useStageGuestToolsUpdate: vi.fn(),
  useCancelGuestToolsUpdate: vi.fn(),
  useSetGuestToolsPolicy: vi.fn(),
}));
vi.mock("@/hooks/usePermissions", () => ({ usePermissions: vi.fn() }));

const base: GuestToolsGuest = {
  vmid: 100,
  name: "server2022",
  node: "pve1",
  status: "running",
  template: false,
  installed_version: "0.1.302",
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
  up_to_date: true,
  needs_update: false,
};

function mount(guest: Partial<GuestToolsGuest>, canDo = true) {
  vi.mocked(useGuestToolsFleet).mockReturnValue({
    data: [{ ...base, ...guest }],
    isLoading: false,
  } as unknown as ReturnType<typeof useGuestToolsFleet>);
  // Mocked one at a time rather than in a loop: the hooks have different
  // mutation payload types, so a shared loop variable has no single valid type.
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
    canExecute: () => canDo,
    canManage: () => canDo,
  } as unknown as ReturnType<typeof usePermissions>);
  renderWithProviders(<GuestToolsFleetTable clusterId="c1" />);
}

describe("GuestToolsFleetTable stage action", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // Gating on needs_update meant an up-to-date guest could never be reinstalled
  // to repair a broken driver install.
  it("allows forcing a reinstall on an up-to-date guest", () => {
    mount({ up_to_date: true, needs_update: false });
    const btn = screen.getByTitle(/Reinstall the current version/i);
    expect(btn).not.toBeDisabled();
  });

  // A guest with no virtio-win reports needs_update=false, because an unknown
  // version is not "behind". Gating on it refused the guest that needed it most.
  it("allows installing on a guest with no guest tools at all", () => {
    mount({ installed_version: "", up_to_date: false, needs_update: false });
    const btn = screen.getByTitle(/Install guest tools/i);
    expect(btn).not.toBeDisabled();
  });

  it("still offers a plain update when the guest is behind", () => {
    mount({ installed_version: "0.1.285", up_to_date: false, needs_update: true });
    expect(screen.getByTitle(/Stage an update/i)).not.toBeDisabled();
  });

  it("refuses a guest that is not running", () => {
    mount({ status: "stopped" });
    expect(screen.getByTitle(/Reinstall the current version/i)).toBeDisabled();
  });

  it("refuses an excluded guest", () => {
    mount({ excluded: true });
    expect(screen.getByTitle(/Reinstall the current version/i)).toBeDisabled();
  });

  it("refuses without execute permission", () => {
    mount({}, false);
    expect(screen.getByTitle(/Reinstall the current version/i)).toBeDisabled();
  });

  // Re-reading the installed version must never be gated on being behind:
  // that is the button you reach for precisely when the display looks wrong.
  it("always allows re-reading the version on a running guest", () => {
    mount({ up_to_date: true, needs_update: false });
    expect(screen.getByTitle(/Re-read the installed version/i)).not.toBeDisabled();
  });
});
