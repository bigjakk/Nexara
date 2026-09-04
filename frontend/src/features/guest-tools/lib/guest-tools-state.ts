import type {
  GuestToolsGuest,
  GuestToolsPolicyRequest,
} from "../types/guest-tools";

export type GuestToolsBadgeVariant =
  | "default"
  | "secondary"
  | "destructive"
  | "outline";

export interface GuestToolsState {
  /** What the badge reads. */
  label: string;
  /** What the badge looks like. */
  variant: GuestToolsBadgeVariant;
  /**
   * Text colour for last_error, which does not always describe a failure: the
   * installer returning 3010 leaves an explanatory message on a guest that
   * installed fine and only needs restarting. Muted so it does not read as one.
   */
  errorTone: "text-muted-foreground" | "text-destructive";
}

/**
 * One guest's state, resolved in one walk down one precedence chain.
 *
 * "Unknown" is deliberately distinct from "Up to date": never having read a
 * guest is not the same as having read it and found it current, and an operator
 * needs to tell those apart before trusting the fleet view.
 *
 * Shared by the cluster fleet table, the per-VM tab and the guest agent summary
 * so all three can never disagree about what a guest's state is called, what
 * colour it is, or how its last error reads.
 */
export function guestToolsState(g: GuestToolsGuest): GuestToolsState {
  const errorTone = g.reboot_required
    ? "text-muted-foreground"
    : "text-destructive";
  const state = (
    label: string,
    variant: GuestToolsBadgeVariant,
  ): GuestToolsState => ({ label, variant, errorTone });

  if (g.excluded) return state("Excluded", "outline");
  // Ahead of the stage switch: the install succeeded, so the stage reads
  // "succeeded", but the operator still has something left to do.
  if (g.reboot_required)
    return state("Updated - reboot to finish", "secondary");
  switch (g.stage) {
    case "staging":
      return state("Staging", "secondary");
    case "staged":
      return state("Staged for next boot", "secondary");
    case "running":
      return state("Installing", "secondary");
    case "failed":
      return state("Failed", "destructive");
    default:
      break;
  }

  // Label and variant part company here, and only here: the label leads with
  // the version check, the variant with the flags. The server sets up_to_date
  // and needs_update both false whenever installed_version is empty, so the one
  // state that would tell them apart — unknown version, yet flagged as behind —
  // is one it never emits. Preserved rather than unified, because nothing in
  // the type enforces that invariant.
  const label = !g.installed_version
    ? "Unknown"
    : g.needs_update
      ? "Update available"
      : g.up_to_date
        ? "Up to date"
        : "Unknown";
  return state(
    label,
    g.needs_update ? "secondary" : g.up_to_date ? "default" : "outline",
  );
}

/**
 * Between "an update was asked for" and "it finished" — the window where
 * cancelling is the offer rather than staging, and the fleet query polls.
 *
 * 'idle' has nothing pending; 'succeeded' and 'failed' are terminal.
 */
export function guestToolsInFlight(g: GuestToolsGuest): boolean {
  return g.stage === "staging" || g.stage === "staged" || g.stage === "running";
}

/**
 * What the stage action will do for this guest, which is not always "update".
 *
 * Staging is deliberately NOT gated on the guest being behind. The backend has
 * never required it — only the automatic scheduler pass skips current guests —
 * and two real cases need it: reinstalling to repair a broken driver install,
 * and installing on a Windows guest that has no virtio-win at all. That second
 * one reports needs_update=false (an unknown version is not "behind"), so
 * gating on it made the feature refuse the guest that most needed it.
 *
 * Returned as a discriminant rather than a string: the fleet table's button is
 * icon-only and needs a full sentence for its tooltip, the VM tab's is a
 * labelled button and needs two words. Same decision, different wording.
 */
export type GuestToolsStageAction = "stage" | "reinstall" | "install";

export function guestToolsStageAction(
  g: GuestToolsGuest,
): GuestToolsStageAction {
  if (g.needs_update) return "stage";
  return g.installed_version ? "reinstall" : "install";
}

/**
 * This guest's current policy with `changes` applied over it.
 *
 * The endpoint takes target_version and note as plain strings, so an omitted
 * key arrives as "" and overwrites — a caller changing only the exclusion has
 * to resend the pin and the note or it clears both. (excluded is a pointer
 * server-side and does survive omission, but sending it costs nothing.)
 * Spelling that out per call site is what made the handlers near-identical.
 */
export function guestToolsPolicy(
  g: GuestToolsGuest,
  changes: Partial<GuestToolsPolicyRequest>,
): GuestToolsPolicyRequest {
  return {
    excluded: g.excluded,
    target_version: g.policy_target_version,
    note: g.note,
    ...changes,
  };
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
