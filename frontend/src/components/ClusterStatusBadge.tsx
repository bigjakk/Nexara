import { Badge, type BadgeVariant } from "@/components/ui/badge";
import type { ClusterResponse } from "@/types/api";

const statusConfig: Record<
  ClusterResponse["status"],
  {
    label: string;
    variant: BadgeVariant;
    className?: string;
    /** A healthy cluster gets a small dot, not a filled badge. */
    dot?: string;
  }
> = {
  online: { label: "Online", variant: "outline", dot: "bg-emerald-500" },
  // Louder than online on purpose: the cluster that needs a look is the one
  // that carries colour.
  degraded: {
    label: "Degraded",
    variant: "outline",
    className:
      "border-amber-500/50 bg-amber-500/15 text-amber-700 dark:text-amber-400",
  },
  offline: { label: "Offline", variant: "destructive" },
  inactive: { label: "Inactive", variant: "secondary" },
  unknown: { label: "Unknown", variant: "secondary" },
};

export function ClusterStatusBadge({
  status,
}: {
  status: ClusterResponse["status"];
}) {
  const cfg = statusConfig[status];
  return (
    <Badge
      variant={cfg.variant}
      className={cfg.dot ? "gap-1.5" : cfg.className}
    >
      {cfg.dot && (
        <span
          aria-hidden="true"
          className={`h-1.5 w-1.5 rounded-full ${cfg.dot}`}
        />
      )}
      {cfg.label}
    </Badge>
  );
}
