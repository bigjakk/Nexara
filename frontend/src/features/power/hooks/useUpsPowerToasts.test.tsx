import { beforeEach, describe, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { renderWithProviders } from "@/test/test-utils";
import { useAuthStore } from "@/stores/auth-store";
import { useUpsDevices } from "../api/ups-queries";
import { upsDevice } from "../power.fixtures";
import type { UpsState } from "../types/ups";
import { useUpsPowerToasts } from "./useUpsPowerToasts";

vi.mock("sonner", async () => (await import("@/test/mocks")).sonnerMock());
vi.mock("../api/ups-queries", () => ({ useUpsDevices: vi.fn() }));

function Harness() {
  useUpsPowerToasts();
  return null;
}

function serve(state: UpsState, flags: string[] = []) {
  vi.mocked(useUpsDevices).mockReturnValue({
    data: [upsDevice({ state, flags, battery_runtime: 580 })],
  } as unknown as ReturnType<typeof useUpsDevices>);
}

describe("useUpsPowerToasts", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    useAuthStore.setState({ globalPermissions: ["view:ups"] });
  });

  // The first fetch is a baseline (already on battery when the app opened is
  // not news), and an outage seen on both sides of a gap in contact is one.
  it("toasts each move onto battery or low battery once", () => {
    serve("on_battery");
    const { rerender } = renderWithProviders(<Harness />);
    for (const state of ["online", "on_battery", "unknown", "on_battery"] as const) {
      serve(state);
      rerender(<Harness />);
    }
    serve("low_battery", ["OB", "LB"]);
    rerender(<Harness />);

    expect(vi.mocked(toast.warning).mock.calls.map((c) => c[0])).toEqual(["ups01 is on battery · 9m 40s left"]);
    expect(vi.mocked(toast.error).mock.calls.map((c) => c[0])).toEqual(["ups01: Low battery · 9m 40s left"]);
  });
});
