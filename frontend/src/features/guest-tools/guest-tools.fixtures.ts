import type { GuestToolsGuest } from "./types/guest-tools";

/**
 * Shared guest-tools row fixtures.
 *
 * Test-only, and deliberately DATA ONLY — type imports and nothing else. The
 * mock installers live in `guest-tools.mocks.ts` so that importing a guest row
 * does not drag the whole query layer (`@tanstack/react-query`, the api
 * client) into a test that mocks none of it.
 */

/** A guest carrying the current tools — the resting state most rows are in. */
export function guest(over: Partial<GuestToolsGuest> = {}): GuestToolsGuest {
  return {
    vmid: 100,
    name: "win02",
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
    ...over,
  };
}

/**
 * A guest running an older build, so an update is genuinely offered.
 *
 * Separate from `guest()` rather than an override at the call site because the
 * two defaults are load-bearing in opposite directions: hand the card an
 * up-to-date guest and its version assertions fail, hand the fleet table an
 * outdated one and its "refuses …" tests fail. Neither file's default is the
 * other's.
 */
export function outdatedGuest(
  over: Partial<GuestToolsGuest> = {},
): GuestToolsGuest {
  return guest({
    installed_version: "0.1.285",
    up_to_date: false,
    needs_update: true,
    ...over,
  });
}
