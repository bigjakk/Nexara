import type { GuestToolsGuest } from "../types/guest-tools";

/**
 * One label per guest, covering the states that matter operationally.
 *
 * "Unknown" is deliberately distinct from "Up to date": never having read a
 * guest is not the same as having read it and found it current, and an operator
 * needs to tell those apart before trusting the fleet view.
 *
 * Shared by the cluster fleet table, the per-VM tab and the guest agent summary
 * so all three can never disagree about what a guest's state is called.
 */
export function guestToolsStateLabel(g: GuestToolsGuest): string {
  if (g.excluded) return "Excluded";
  // Ahead of the stage switch: the install succeeded, so the stage reads
  // "succeeded", but the operator still has something left to do.
  if (g.reboot_required) return "Updated - reboot to finish";
  switch (g.stage) {
    case "staging":
      return "Staging";
    case "staged":
      return "Staged for next boot";
    case "running":
      return "Installing";
    case "failed":
      return "Failed";
    default:
      break;
  }
  if (!g.installed_version) return "Unknown";
  if (g.needs_update) return "Update available";
  if (g.up_to_date) return "Up to date";
  return "Unknown";
}

export type GuestToolsBadgeVariant =
  | "default"
  | "secondary"
  | "destructive"
  | "outline";

export function guestToolsStateVariant(
  g: GuestToolsGuest,
): GuestToolsBadgeVariant {
  if (g.excluded) return "outline";
  // Not destructive: nothing went wrong, it is just not finished.
  if (g.reboot_required) return "secondary";
  if (g.stage === "failed") return "destructive";
  if (g.stage === "staged" || g.stage === "running" || g.stage === "staging")
    return "secondary";
  if (g.needs_update) return "secondary";
  if (g.up_to_date) return "default";
  return "outline";
}
