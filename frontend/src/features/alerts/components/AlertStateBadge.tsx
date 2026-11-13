import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { PROBLEM_CHIP } from "../lib/alert-style";

/**
 * Firing is red and pending amber, the two states that want attention.
 * Acknowledged is a plain chip and resolved a quiet one: both are handled.
 */
export function AlertStateBadge({ state }: { state: string }) {
  return (
    <Badge
      variant={state === "pending" ? "warning" : "outline"}
      className={cn(
        "capitalize",
        state === "firing" && PROBLEM_CHIP,
        state !== "firing" &&
          state !== "pending" &&
          state !== "acknowledged" &&
          "font-medium text-muted-foreground",
      )}
    >
      {state}
    </Badge>
  );
}
