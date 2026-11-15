/**
 * A VM whose QEMU process runs while its vCPUs are halted keeps the status
 * "running" (it still holds its memory, and counts, filters, DRS and drains
 * treat it so) and carries QEMU's own run state beside it in `run_state`.
 * These are the states a guest is shown in and acted on instead of "running".
 *
 * QEMU's "suspended" is S3 sleep; it is shown as "asleep", because the
 * status "suspended" already means paused.
 */
export const HALTED_STATES = [
  "io-error",
  "internal-error",
  "guest-panicked",
  "watchdog",
  "shutdown",
  "debug",
  "asleep",
] as const;

export type HaltedState = (typeof HALTED_STATES)[number];

const fromRunState: Record<string, HaltedState> = {
  "io-error": "io-error",
  "internal-error": "internal-error",
  "guest-panicked": "guest-panicked",
  watchdog: "watchdog",
  shutdown: "shutdown",
  debug: "debug",
  suspended: "asleep",
};

/** The halted state of a running guest, or null. */
export function haltedState(
  status: string,
  runState: string | undefined,
): HaltedState | null {
  if (status.toLowerCase() !== "running" || !runState) return null;
  return fromRunState[runState] ?? null;
}

/** The state to show and act on: the halted state, else the status. */
export function guestState(status: string, runState?: string): string {
  return haltedState(status, runState) ?? status.toLowerCase();
}

export function isHaltedState(state: string): state is HaltedState {
  return (HALTED_STATES as readonly string[]).includes(state);
}

/**
 * The halted states Proxmox's resume brings back (qemu-server
 * RunState::vm_resume): S3 sleep by `system_wakeup`, the rest by `cont`, which
 * also retries the I/O that failed. Left out: internal-error, which `cont`
 * refuses ("Resetting the Virtual Machine is required"); shutdown, which resume
 * turns into a reset; and guest-panicked, whose guest is dead though `cont`
 * would run it.
 */
const RESUMABLE: ReadonlySet<string> = new Set<HaltedState>([
  "io-error",
  "watchdog",
  "debug",
  "asleep",
]);

export function canResume(state: string): boolean {
  return state === "suspended" || RESUMABLE.has(state);
}

/** States that are a fault, drawn red; the others are amber or quiet. */
const FAULTS: ReadonlySet<string> = new Set<HaltedState>([
  "io-error",
  "internal-error",
  "guest-panicked",
  "watchdog",
]);

export function isFaultState(state: string): boolean {
  return FAULTS.has(state);
}
