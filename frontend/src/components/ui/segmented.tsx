import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

export interface SegmentedOption<T extends string> {
  value: T;
  label: ReactNode;
  disabled?: boolean | undefined;
  /** The accessible name when the label is an icon or abbreviated. */
  ariaLabel?: string | undefined;
  /** A hover hint that says more than the label does. */
  title?: string | undefined;
  testId?: string | undefined;
}

interface SegmentedProps<T extends string> {
  value: T;
  onValueChange: (value: T) => void;
  /**
   * The pressed segment was pressed again. For a filter that a second press
   * clears; without it the press does nothing.
   */
  onReselect?: ((value: T) => void) | undefined;
  options: readonly SegmentedOption<T>[];
  /** Names the group: "Time range", "View". */
  "aria-label": string;
  size?: "sm" | "md";
  className?: string;
}

/**
 * A short set of mutually exclusive views or ranges: one raised segment in a
 * recessed track. For choosing how to look at the same thing; a choice that
 * changes what is saved is a Select or radio group.
 */
export function Segmented<T extends string>({
  value,
  onValueChange,
  onReselect,
  options,
  "aria-label": ariaLabel,
  size = "sm",
  className,
}: SegmentedProps<T>) {
  return (
    <div
      role="group"
      aria-label={ariaLabel}
      className={cn(
        "inline-flex max-w-full gap-0.5 overflow-x-auto rounded-lg bg-foreground/[0.07] p-[3px]",
        className,
      )}
    >
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={option.value === value}
          aria-label={option.ariaLabel}
          title={option.title}
          data-testid={option.testId}
          disabled={option.disabled}
          onClick={() => {
            if (option.value !== value) onValueChange(option.value);
            else onReselect?.(option.value);
          }}
          className={cn(
            "inline-flex shrink-0 items-center justify-center gap-1.5 whitespace-nowrap rounded-[6px] font-medium text-muted-foreground outline-hidden transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50 [&_svg]:size-3.5",
            "aria-pressed:bg-card aria-pressed:font-semibold aria-pressed:text-foreground aria-pressed:shadow-sm dark:aria-pressed:bg-foreground/15",
            size === "sm" ? "h-6 px-2.5 text-[13px]" : "h-7 px-3 text-sm",
          )}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
