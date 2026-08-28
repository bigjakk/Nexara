import type { VeeamServer } from "../types/backup";

export type VeeamServerStatus = "Disabled" | "Error" | "Connected";

/**
 * The single source of a registered server's three states.
 *
 * Shared by the badge and by the Status column's sort key, so a state added to
 * one cannot leave the other disagreeing — the header would then order rows by
 * a rule the badges no longer follow. Lives in its own module rather than
 * beside the badge because a component file that also exports a plain function
 * breaks React Fast Refresh.
 */
export function veeamServerStatus(server: VeeamServer): VeeamServerStatus {
  if (!server.enabled) return "Disabled";
  if (server.last_sync_error !== "") return "Error";
  return "Connected";
}
