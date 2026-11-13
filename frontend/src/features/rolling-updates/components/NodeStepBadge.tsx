import { cn } from "@/lib/utils";
import type { RollingUpdateNode } from "@/types/api";

/** How a step reads: waiting, being worked on, wanting the operator, done, or failed. */
type StepTone = "idle" | "active" | "attention" | "done" | "failed";

const stepConfig: Record<
  RollingUpdateNode["step"],
  {
    label: string;
    tone: StepTone;
  }
> = {
  pending: { label: "Pending", tone: "idle" },
  draining: { label: "Draining", tone: "active" },
  awaiting_upgrade: { label: "Awaiting upgrade", tone: "attention" },
  upgrading: { label: "Upgrading", tone: "active" },
  rebooting: { label: "Rebooting", tone: "active" },
  health_check: { label: "Health check", tone: "active" },
  restoring: { label: "Restoring", tone: "active" },
  completed: { label: "Completed", tone: "done" },
  failed: { label: "Failed", tone: "failed" },
  skipped: { label: "Skipped", tone: "idle" },
};

// Work in progress is blue (--info), a finished step is plain, and only a
// step that wants someone or went wrong takes amber or red.
const DOT_CLASS: Record<StepTone, string> = {
  idle: "ring-[1.5px] ring-inset ring-muted-foreground/60",
  active: "animate-pulse bg-info",
  attention: "bg-amber-500",
  done: "bg-muted-foreground/45",
  failed: "bg-red-500",
};

const TEXT_CLASS: Record<StepTone, string> = {
  idle: "text-muted-foreground",
  active: "",
  attention: "text-amber-700 dark:text-amber-400",
  done: "",
  failed: "text-red-700 dark:text-red-400",
};

export function NodeStepBadge({
  step,
  rebootRequired = false,
  className,
}: {
  step: RollingUpdateNode["step"];
  /** The node's upgrade landed but it still owes a reboot. */
  rebootRequired?: boolean;
  className?: string;
}) {
  // Overrides the label rather than sitting beside it, following what
  // guest-tools does with the same concept: a node that owes a reboot has not
  // finished, and "Completed" next to a second mark is read as finished.
  // The only surface that tells an operator a reboot is pending must not also
  // be the one saying the work is done.
  const { label, tone } = rebootRequired
    ? { label: "Reboot required", tone: "attention" as const }
    : stepConfig[step];
  return (
    <span
      className={cn(
        "inline-flex items-center gap-[7px] whitespace-nowrap text-sm",
        TEXT_CLASS[tone],
        className,
      )}
    >
      <span
        aria-hidden="true"
        className={cn("h-2 w-2 shrink-0 rounded-full", DOT_CLASS[tone])}
      />
      {label}
    </span>
  );
}
