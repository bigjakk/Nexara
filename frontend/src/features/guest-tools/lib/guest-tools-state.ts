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

/**
 * What will actually install, when that is not what the Target column says.
 *
 * A staged install fires at the guest's next boot, so the version staged and
 * the version currently targeted can drift apart — the operator changes the
 * target to back out a bad release, and every already-staged guest is still
 * armed with the release being backed out. The reconcile loop withdraws those
 * within a tick or so, but until it does, "Staged for next boot" sitting beside
 * a Target column naming a different version is actively misleading: the target
 * is not what would land.
 *
 * Returns null when there is nothing to say, which is the overwhelmingly common
 * case — the two agree, and saying so twice is noise.
 */
export function guestToolsStagedMismatch(g: GuestToolsGuest): string | null {
  // 'staged' only, matching exactly what the backend will act on, so the UI
  // never promises a withdrawal that is not coming. 'staging' is a row being
  // written right now and about to carry the new version anyway; 'running' is
  // an install already underway, which nothing withdraws. On a terminal row
  // staged_version records what ran rather than what will run, and flagging it
  // would read as a warning about an install that already finished.
  if (g.stage !== "staged") return null;
  if (!g.staged_version || !g.target_version) return null;
  if (g.staged_version === g.target_version) return null;
  return `Will install ${g.staged_version}, not ${g.target_version}`;
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
