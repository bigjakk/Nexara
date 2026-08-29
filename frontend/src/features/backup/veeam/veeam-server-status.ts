import type { VeeamServer } from "../types/backup";

export type VeeamServerStatus =
  | "Disabled"
  | "Error"
  | "Syncing"
  | "Connected";

/**
 * The single source of a registered server's four states.
 *
 * Shared by the badge and by the Status column's sort key, so a state added to
 * one cannot leave the other disagreeing — the header would then order rows by
 * a rule the badges no longer follow. Lives in its own module rather than
 * beside the badge because a component file that also exports a plain function
 * breaks React Fast Refresh.
 *
 * "Syncing" is the window between registering a server and the collector's
 * first inventory pass landing. It has to be its own state: a just-added server
 * is enabled with no recorded error, which used to fall through to "Connected"
 * and put a green badge over tables that were still empty — the UI asserting
 * everything was fine while the operator saw nothing and assumed it was broken.
 *
 * Order matters. last_sync_error is checked BEFORE last_sync_at, so a first
 * pass that failed reads Error rather than sitting on Syncing forever: the
 * error is the newer fact, and it is the one worth acting on.
 */
export function veeamServerStatus(server: VeeamServer): VeeamServerStatus {
  if (!server.enabled) return "Disabled";
  if (server.last_sync_error !== "") return "Error";
  if (server.last_sync_at == null) return "Syncing";
  return "Connected";
}
