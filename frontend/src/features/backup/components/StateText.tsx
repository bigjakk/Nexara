import type { ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { runStateTone } from "../lib/datastore-facts";

/** What a state means, which is all that picks its colour. */
export type StateTone = "ok" | "warn" | "bad" | "running" | "idle";

const DOT_CLASS: Record<Exclude<StateTone, "running">, string> = {
  ok: "bg-emerald-500",
  warn: "bg-amber-500",
  bad: "bg-red-500",
  // Hollow: nothing happened, which is not a colour.
  idle: "ring-[1.5px] ring-inset ring-muted-foreground/60",
};

const TEXT_CLASS: Record<StateTone, string> = {
  ok: "",
  warn: "text-amber-700 dark:text-amber-400",
  bad: "text-red-700 dark:text-red-400",
  running: "",
  idle: "text-muted-foreground",
};

/**
 * A state in a table cell or on a line of facts: a dot and the word for it.
 * Healthy is a green dot beside plain text, only a problem colours the text,
 * and work in progress spins in the data colour.
 */
export function StateText({
  tone,
  children,
  title,
  className,
}: {
  tone: StateTone;
  children: ReactNode;
  title?: string | undefined;
  className?: string | undefined;
}) {
  return (
    <span
      title={title}
      className={cn(
        "inline-flex max-w-full items-center gap-[7px] align-middle",
        TEXT_CLASS[tone],
        className,
      )}
    >
      {tone === "running" ? (
        <Loader2
          aria-hidden="true"
          className="h-3.5 w-3.5 shrink-0 animate-spin text-data"
        />
      ) : (
        <span
          aria-hidden="true"
          className={cn(
            "h-[7px] w-[7px] shrink-0 rounded-full",
            DOT_CLASS[tone],
          )}
        />
      )}
      <span className="min-w-0 truncate">{children}</span>
    </span>
  );
}

/**
 * How a sync, verify or prune job last ended, from PBS's own last-run-state:
 * "OK", "WARNINGS: n", an error message, or nothing: for a job that never
 * ran, and also while one is running, so the empty state claims neither.
 */
export function RunState({ state }: { state: string }) {
  const tone = runStateTone(state);
  if (tone === "idle") {
    return <span className="text-muted-foreground">No result yet</span>;
  }
  return (
    <StateText tone={tone} title={tone === "ok" ? undefined : state}>
      {tone === "ok" ? "OK" : state}
    </StateText>
  );
}
