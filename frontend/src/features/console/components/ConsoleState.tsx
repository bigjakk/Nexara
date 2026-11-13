import { cn } from "@/lib/utils";
import { CONSOLE_STATUS_DOT, CONSOLE_STATUS_TEXT } from "../lib/console-status";
import type { ConsoleStatus } from "../types/console";

/** A console's state as a dot. Decorative: pair it with the state in words. */
export function ConsoleStateDot({
  status,
  className,
}: {
  status: ConsoleStatus;
  className?: string;
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "h-2 w-2 shrink-0 rounded-full",
        CONSOLE_STATUS_DOT[status],
        className,
      )}
    />
  );
}

/** A console's state as a fact: the dot and the state in words. */
export function ConsoleStateFact({
  status,
  className,
}: {
  status: ConsoleStatus;
  className?: string;
}) {
  return (
    <span className={cn("flex shrink-0 items-center gap-[7px]", className)}>
      <ConsoleStateDot status={status} />
      <span>{CONSOLE_STATUS_TEXT[status]}</span>
    </span>
  );
}
