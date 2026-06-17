import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { cephHealthLabel, cephSeverity } from "../lib/ceph-health";

interface CephHealthBadgeProps {
  status: string;
  className?: string;
}

export function CephHealthBadge({ status, className }: CephHealthBadgeProps) {
  const severity = cephSeverity(status);
  const label = cephHealthLabel(status);

  // Warning gets an explicit amber treatment instead of the muted "secondary"
  // variant so it reads as a real warning at a glance.
  const variant =
    severity === "ok"
      ? "default"
      : severity === "err"
        ? "destructive"
        : "outline";

  const warnClass =
    severity === "warn"
      ? "border-amber-500/50 bg-amber-500/15 text-amber-700 dark:text-amber-400"
      : undefined;

  return (
    <Badge variant={variant} className={cn(warnClass, className)}>
      {label}
    </Badge>
  );
}
