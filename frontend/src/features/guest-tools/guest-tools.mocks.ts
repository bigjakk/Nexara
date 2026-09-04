import { vi } from "vitest";
import {
  useDetectGuestTools,
  useStageGuestToolsUpdate,
  useCancelGuestToolsUpdate,
  useSetGuestToolsPolicy,
} from "./api/guest-tools-queries";
import { usePermissions } from "@/hooks/usePermissions";

/**
 * Mock installers for the guest-tools surfaces. Split from
 * `guest-tools.fixtures.ts` because these reach into the query layer and the
 * row fixtures must not.
 *
 * `mockGuestToolsMutations` touches only the four mutation hooks that the card
 * and fleet-table test files both mock — not every guest-tools test file does
 * (the virtio-win ones mock none of them). It deliberately leaves `useGuestToolsGuest` and
 * `useGuestToolsFleet` alone: each file's `vi.mock` factory declares only the
 * reader it uses, so a helper reaching for the other one would find undefined.
 */

/**
 * Park the four guest-tools mutation hooks in their idle state.
 *
 * Mocked one at a time rather than in a loop: the hooks have different mutation
 * payload types, so a shared loop variable has no single valid type.
 */
export function mockGuestToolsMutations() {
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
}

/**
 * Grant `canDo` for `scope` and refuse every other scope.
 *
 * The scope is checked rather than ignored: a mock that answers the same for
 * any string lets a component ask for the wrong permission entirely and still
 * pass its "refuses without X" test.
 */
export function mockPermissions(scope: string, canDo = true) {
  const allows = (asked: string) => asked === scope && canDo;
  vi.mocked(usePermissions).mockReturnValue({
    canExecute: allows,
    canManage: allows,
  } as unknown as ReturnType<typeof usePermissions>);
}
