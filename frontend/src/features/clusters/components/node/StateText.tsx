import type { ReactNode } from "react";

import { cn } from "@/lib/utils";

export type StateTone = "ok" | "off" | "warn" | "bad";

const DOT_CLASS: Record<StateTone, string> = {
  ok: "bg-emerald-500",
  // Hollow, so "off" reads without relying on colour.
  off: "ring-[1.5px] ring-inset ring-muted-foreground/70",
  warn: "bg-amber-500",
  bad: "bg-red-500",
};

const TEXT_CLASS: Record<StateTone, string> = {
  ok: "",
  off: "text-muted-foreground",
  warn: "font-medium text-amber-700 dark:text-amber-400",
  bad: "font-medium text-red-700 dark:text-red-400",
};

/**
 * A state in a table cell: a dot and plain text. Fine is a green dot and
 * ordinary text, off is hollow and muted, and only a problem colours the
 * words. The dot is decorative; the text is what is read out.
 */
export function StateText({
  tone,
  children,
  className,
}: {
  tone: StateTone;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-[7px] whitespace-nowrap",
        TEXT_CLASS[tone],
        className,
      )}
    >
      <span
        aria-hidden="true"
        className={cn("h-2 w-2 shrink-0 rounded-full", DOT_CLASS[tone])}
      />
      {children}
    </span>
  );
}

/** What an empty cell shows. */
export function NoValue() {
  return <span className="font-sans text-muted-foreground/60">—</span>;
}
