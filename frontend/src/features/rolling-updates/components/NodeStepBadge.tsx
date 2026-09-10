import { Badge, type BadgeVariant } from "@/components/ui/badge";
import type { RollingUpdateNode } from "@/types/api";

const stepConfig: Record<
  RollingUpdateNode["step"],
  {
    label: string;
    variant: BadgeVariant;
  }
> = {
  pending: { label: "Pending", variant: "outline" },
  draining: { label: "Draining", variant: "default" },
  awaiting_upgrade: { label: "Awaiting Upgrade", variant: "secondary" },
  upgrading: { label: "Upgrading", variant: "default" },
  rebooting: { label: "Rebooting", variant: "default" },
  health_check: { label: "Health Check", variant: "default" },
  restoring: { label: "Restoring", variant: "default" },
  completed: { label: "Completed", variant: "outline" },
  failed: { label: "Failed", variant: "destructive" },
  skipped: { label: "Skipped", variant: "outline" },
};

export function NodeStepBadge({
  step,
  rebootRequired = false,
}: {
  step: RollingUpdateNode["step"];
  /** The node's upgrade landed but it still owes a reboot. */
  rebootRequired?: boolean;
}) {
  // Overrides the label rather than sitting beside it, following what
  // guest-tools does with the same concept: a node that owes a reboot has not
  // finished, and "Completed" next to a secondary badge is read as finished.
  // The only surface that tells an operator a reboot is pending must not also
  // be the one saying the work is done.
  if (rebootRequired) {
    return <Badge variant="secondary">Reboot required</Badge>;
  }
  const config = stepConfig[step];
  return <Badge variant={config.variant}>{config.label}</Badge>;
}
