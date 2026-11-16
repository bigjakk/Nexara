import { useRef } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import type { RollingUpdateJob } from "@/types/api";
import { useRollingUpdateNodes } from "../api/rolling-update-queries";

/** A Start or Cancel waiting on its confirmation. */
export interface PendingJobAction {
  kind: "start" | "cancel";
  job: RollingUpdateJob;
}

/**
 * What Start sets going, in the order the orchestrator does it
 * (internal/rolling/orchestrator.go processJob, finishUpgrade and
 * reboot_policy.go decideReboot).
 */
function startSteps(job: RollingUpdateJob, t: TFunction<"security">): string {
  const steps = [
    job.parallelism > 1
      ? t("startJobOrderMany", { count: job.parallelism })
      : t("startJobOrderOne"),
    job.drain_guests
      ? `${t("startJobDrain")} ${t("startJobHARules")}`
      : t("startJobInPlace"),
    job.auto_upgrade ? t("startJobUpgradeSSH") : t("startJobUpgradeManual"),
  ];
  if (!job.drain_guests) {
    steps.push(
      job.reboot_after_update
        ? t("startJobRebootInPlace")
        : // Only the SSH path reads /var/run/reboot-required; a confirmed
          // manual upgrade goes straight on (ConfirmUpgrade).
          job.auto_upgrade
          ? t("startJobRebootInPlaceNever")
          : t("startJobRebootNever"),
    );
  } else if (job.reboot_after_update) {
    steps.push(t("startJobRebootAlways"));
  } else {
    // An automated upgrade reboots when /var/run/reboot-required asks; a
    // manual one never does.
    steps.push(
      job.auto_upgrade ? t("startJobRebootIfAsked") : t("startJobRebootNever"),
    );
  }
  steps.push(t("startJobHealth"));
  if (job.drain_guests && job.auto_restore_guests)
    steps.push(t("startJobRestore"));
  steps.push(t("startJobPauses"));
  return steps.join(" ");
}

/** The confirmation a rolling update's Start and Cancel go through. */
export function RollingUpdateJobConfirm({
  pending,
  onClose,
  onConfirm,
}: {
  pending: PendingJobAction | null;
  onClose: () => void;
  onConfirm: (action: PendingJobAction) => void;
}) {
  const { t } = useTranslation("security");
  // The last Start shown, so the title holds while the dialog closes.
  const shownStart = useRef<RollingUpdateJob | null>(null);
  if (pending !== null)
    shownStart.current = pending.kind === "start" ? pending.job : null;
  const start = shownStart.current;
  // Read for a Start only: the title says how many nodes it updates.
  const nodes = useRollingUpdateNodes(start?.cluster_id ?? "", start?.id ?? "");
  const count = nodes.data?.length;

  return (
    <ConfirmDeleteDialog
      target={pending}
      onClose={onClose}
      onConfirm={onConfirm}
      title={(a) =>
        a.kind === "cancel"
          ? t("cancelJobTitle")
          : count === undefined
            ? t("startJobTitleUnknown")
            : t("startJobTitle", { count })
      }
      description={(a) =>
        a.kind === "cancel"
          ? a.job.status === "pending"
            ? t("cancelJobPending")
            : t("cancelJobActive")
          : `${nodes.isError ? `${t("startJobNodesUnread")} ` : ""}${startSteps(a.job, t)}`
      }
      confirmLabel={(a) =>
        a.kind === "cancel" ? t("cancelJob") : t("startJob")
      }
      destructive={(a) => a.kind === "cancel"}
      cancelLabel={(a) =>
        a.kind === "cancel" ? t("keepJob") : t("cancel", { ns: "common" })
      }
      // Held while the node count is read, so the title is not a guess.
      confirmDisabled={start !== null && nodes.isPending}
    />
  );
}
