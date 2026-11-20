import type { TFunction } from "i18next";
import type { RollingUpdateJob } from "@/types/api";

/** The settings that decide what Start sets going. */
export type StartStepsJob = Pick<
  RollingUpdateJob,
  | "parallelism"
  | "drain_guests"
  | "auto_upgrade"
  | "reboot_after_update"
  | "auto_restore_guests"
  | "ceph_wait"
  | "ceph_noout"
  | "ceph_present"
>;

/**
 * A short summary of what Start sets going; the admin guide's Rolling Updates
 * section has the rest. Shared by the Start confirmation and the wizard's
 * "Create and start", so both say the same thing.
 */
export function startJobSteps(
  job: StartStepsJob,
  t: TFunction<"security">,
): string {
  // Null is a cluster Proxmox did not answer for: the job waits until it can
  // tell (internal/rolling/ceph_gate.go cephPresent), so it is told as Ceph.
  const ceph = job.ceph_present !== false;
  const sentences = [
    // Waiting for Ceph takes the nodes one at a time whatever the parallelism.
    ceph && job.ceph_wait
      ? t("startJobOrderCeph")
      : job.parallelism <= 1
        ? t("startJobOrderOne")
        : t("startJobOrderMany", { count: job.parallelism }),
  ];
  if (ceph && job.ceph_noout) sentences.push(t("startJobCephNoout"));
  const steps = [
    job.auto_upgrade
      ? t("startJobStepUpgradeSSH")
      : t("startJobStepUpgradeManual"),
    rebootStep(job, t),
  ];
  if (job.drain_guests) {
    steps.unshift(t("startJobStepDrain"));
    if (job.auto_restore_guests) steps.push(t("startJobStepRestore"));
    sentences.push(t("startJobEachNode", { steps }));
  } else {
    sentences.push(t("startJobEachNodeInPlace", { steps }));
  }
  return sentences.join(" ");
}

/** Mirrors internal/rolling/reboot_policy.go decideReboot. */
function rebootStep(job: StartStepsJob, t: TFunction<"security">): string {
  if (!job.drain_guests) {
    // In place, a node is never rebooted under running guests; an owed reboot
    // is deferred and the node marked Reboot required. Both paths owe one for
    // a kernel (ConfirmUpgrade too); only SSH reads /var/run/reboot-required.
    if (job.reboot_after_update) return t("startJobStepRebootInPlace");
    return job.auto_upgrade
      ? t("startJobStepRebootRequired")
      : t("startJobStepRebootRequiredIfKernel");
  }
  if (job.reboot_after_update) return t("startJobStepReboot");
  // Both reboot for a kernel (upgradeInstallsKernel); only an automated
  // upgrade reads /var/run/reboot-required.
  return job.auto_upgrade
    ? t("startJobStepRebootIfNeeded")
    : t("startJobStepRebootIfKernel");
}
