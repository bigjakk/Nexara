import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import type { RollingUpdateJob } from "@/types/api";
import { useRollingUpdateNodes } from "../api/rolling-update-queries";
import { startJobSteps } from "../lib/start-steps";

/** A Start or Cancel waiting on its confirmation. */
export interface PendingJobAction {
  kind: "start" | "cancel";
  job: RollingUpdateJob;
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
          : `${nodes.isError ? `${t("startJobNodesUnread")} ` : ""}${startJobSteps(a.job, t)}`
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
