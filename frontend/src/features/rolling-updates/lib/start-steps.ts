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
>;

/**
 * What Start sets going, in the order the orchestrator does it
 * (internal/rolling/orchestrator.go processJob, finishUpgrade and
 * reboot_policy.go decideReboot). Shared by the Start confirmation and the
 * wizard's "Create & Start", so both say the same thing.
 */
export function startJobSteps(
  job: StartStepsJob,
  t: TFunction<"security">,
): string {
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
