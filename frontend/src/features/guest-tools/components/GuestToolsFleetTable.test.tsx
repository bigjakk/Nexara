import { describe, it, expect, vi, beforeEach } from "vitest";
import { screen } from "@testing-library/react";
import { renderWithProviders } from "@/test/test-utils";
import { GuestToolsFleetTable } from "./GuestToolsFleetTable";
import { useGuestToolsFleet } from "../api/guest-tools-queries";
import type { GuestToolsGuest } from "../types/guest-tools";
import { guest } from "../guest-tools.fixtures";
import { mockGuestToolsMutations, mockPermissions } from "../guest-tools.mocks";

vi.mock("../api/guest-tools-queries", () => ({
  useGuestToolsFleet: vi.fn(),
  useDetectGuestTools: vi.fn(),
  useStageGuestToolsUpdate: vi.fn(),
  useCancelGuestToolsUpdate: vi.fn(),
  useSetGuestToolsPolicy: vi.fn(),
}));
vi.mock("@/hooks/usePermissions", () => ({ usePermissions: vi.fn() }));

function mount(over: Partial<GuestToolsGuest>, canDo = true) {
  vi.mocked(useGuestToolsFleet).mockReturnValue({
    data: [guest(over)],
    isLoading: false,
  } as unknown as ReturnType<typeof useGuestToolsFleet>);
  mockGuestToolsMutations();
  mockPermissions("guest_tools", canDo);
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
    mount({
      installed_version: "0.1.285",
      up_to_date: false,
      needs_update: true,
    });
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

  // A positive control on the MANAGE scope specifically — the test above
  // withholds both scopes at once, so it cannot tell which one the row reads.
  it("offers the exclude control to a manager", () => {
    mount({});
    expect(
      screen.getByTitle(/Exclude this guest from updates/i),
    ).not.toBeDisabled();
  });

  // Re-reading the installed version must never be gated on being behind:
  // that is the button you reach for precisely when the display looks wrong.
  it("always allows re-reading the version on a running guest", () => {
    mount({ up_to_date: true, needs_update: false });
    expect(
      screen.getByTitle(/Re-read the installed version/i),
    ).not.toBeDisabled();
  });
});
