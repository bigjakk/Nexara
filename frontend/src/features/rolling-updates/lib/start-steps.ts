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
 * What Start sets going, in the order the orchestrator does it
 * (internal/rolling/orchestrator.go processJob, finishUpgrade and
 * reboot_policy.go decideReboot). Shared by the Start confirmation and the
 * wizard's "Create and start", so both say the same thing.
 */
export function startJobSteps(
  job: StartStepsJob,
  t: TFunction<"security">,
): string {
  // Null is a cluster Proxmox did not answer for: the job waits until it can
  // tell (internal/rolling/ceph_gate.go cephPresent), so it is told as Ceph.
  const ceph = job.ceph_present !== false;
  const cephWait = ceph && job.ceph_wait;
  const steps = [
    job.parallelism <= 1
      ? t("startJobOrderOne")
      : cephWait
        ? t("startJobOrderCeph")
        : t("startJobOrderMany", { count: job.parallelism }),
    job.drain_guests
      ? `${t("startJobDrain")} ${t("startJobHARules")}`
      : t("startJobInPlace"),
    job.auto_upgrade ? t("startJobUpgradeSSH") : t("startJobUpgradeManual"),
  ];
  if (cephWait) steps.splice(1, 0, t("startJobCephWait"));
  if (ceph && job.ceph_noout)
    steps.splice(cephWait ? 2 : 1, 0, t("startJobCephNoout"));
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
    // Both reboot for a kernel (upgradeInstallsKernel); only an automated
    // upgrade reads /var/run/reboot-required.
    steps.push(
      job.auto_upgrade
        ? t("startJobRebootIfAsked")
        : t("startJobRebootIfKernel"),
    );
  }
  steps.push(cephWait ? t("startJobHealthCeph") : t("startJobHealth"));
  if (job.drain_guests && job.auto_restore_guests)
    steps.push(t("startJobRestore"));
  steps.push(t("startJobPauses"));
  return steps.join(" ");
}
