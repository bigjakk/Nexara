import { useId, type HTMLAttributes, type ReactNode } from "react";
import { cn } from "@/lib/utils";

interface PanelProps extends Omit<HTMLAttributes<HTMLElement>, "title"> {
  title?: ReactNode;
  /** Muted text beside the title: a count, a state, a one-line note. */
  aside?: ReactNode;
  /** Controls at the right of the title row. */
  actions?: ReactNode;
  /**
   * Drop the body's side padding, for a table or a strip that runs edge to
   * edge. The content then owns its own gutters.
   */
  flush?: boolean;
  bodyClassName?: string;
}

/**
 * A titled block of a page: one flat surface, no border and no shadow. The
 * page is a few of these on the canvas, not a grid of outlined cards.
 */
export function Panel({
  title,
  aside,
  actions,
  flush = false,
  className,
  bodyClassName,
  children,
  ...props
}: PanelProps) {
  const headingId = useId();
  const hasHeader =
    title !== undefined || aside !== undefined || actions !== undefined;
  return (
    <section
      aria-labelledby={title !== undefined ? headingId : undefined}
      className={cn(
        "panel min-w-0 rounded-[10px] text-card-foreground",
        className,
      )}
      {...props}
    >
      {hasHeader && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-[18px] pb-3 pt-4">
          {title !== undefined && (
            <h2 id={headingId} className="text-[15px] font-semibold">
              {title}
            </h2>
          )}
          {aside !== undefined && (
            <span className="min-w-0 text-sm text-muted-foreground">
              {aside}
            </span>
          )}
          {actions !== undefined && (
            <div className="ml-auto flex flex-wrap items-center gap-2">
              {actions}
            </div>
          )}
        </div>
      )}
      <div
        className={cn(
          flush ? "pb-3" : "px-[18px] pb-4",
          !hasHeader && (flush ? "pt-3" : "pt-4"),
          bodyClassName,
        )}
      >
        {children}
      </div>
    </section>
  );
}

/**
 * A grid of stacked `label / value` pairs (DetailField children) that wraps
 * to as many columns as fit.
 */
export function DefGrid({
  className,
  ...props
}: HTMLAttributes<HTMLDListElement>) {
  return (
    <dl
      className={cn(
        "grid grid-cols-[repeat(auto-fit,minmax(min(9.5rem,100%),1fr))] gap-x-6 gap-y-3.5 text-sm",
        className,
      )}
      {...props}
    />
  );
}

/** A list of `label …… value` rows divided by hairlines. */
export function PropertyList({
  className,
  ...props
}: HTMLAttributes<HTMLDListElement>) {
  return <dl className={cn("divide-y text-sm", className)} {...props} />;
}

export function PropertyRow({
  label,
  children,
  className,
}: {
  label: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex items-baseline justify-between gap-4 py-2 first:pt-0 last:pb-0",
        className,
      )}
    >
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 text-right [overflow-wrap:anywhere]">
        {children}
      </dd>
    </div>
  );
}
