import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { PROBLEM_CHIP } from "../lib/alert-style";

/** Critical is red, warning amber. Info, and anything else, stays quiet. */
export function AlertSeverityBadge({ severity }: { severity: string }) {
  return (
    <Badge
      variant={severity === "warning" ? "warning" : "outline"}
      className={cn(
        "capitalize",
        severity === "critical" && PROBLEM_CHIP,
        severity !== "critical" &&
          severity !== "warning" &&
          "font-medium text-muted-foreground",
      )}
    >
      {severity}
    </Badge>
  );
}
