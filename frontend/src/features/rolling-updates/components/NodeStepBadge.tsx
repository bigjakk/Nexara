import { Badge } from "@/components/ui/badge";
import type { RollingUpdateNode } from "@/types/api";

const stepConfig: Record<
  RollingUpdateNode["step"],
  { label: string; variant: "default" | "secondary" | "destructive" | "outline" }
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

export function NodeStepBadge({ step }: { step: RollingUpdateNode["step"] }) {
  const config = stepConfig[step];
  return <Badge variant={config.variant}>{config.label}</Badge>;
}
