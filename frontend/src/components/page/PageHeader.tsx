import type { ReactNode } from "react";
import { cn } from "@/lib/utils";

/** The type scale of a page's own name. Shared with DetailHeader. */
export const PAGE_TITLE_CLASS =
  "min-w-0 text-[27px] font-[650] leading-[1.2] tracking-[-0.012em] [overflow-wrap:anywhere]";

interface PageHeaderProps {
  title: ReactNode;
  /**
   * One plain sentence under the title: what the page holds, or the state it
   * is in ("44 guests, 31 running"). Not a slogan.
   */
  summary?: ReactNode;
  /** Controls on the right. At most one of them is a primary button. */
  actions?: ReactNode;
  className?: string;
}

/** The top of a list or settings page: a wide title, a sentence, the actions. */
export function PageHeader({
  title,
  summary,
  actions,
  className,
}: PageHeaderProps) {
  return (
    <header
      className={cn(
        "flex flex-wrap items-end justify-between gap-x-6 gap-y-3",
        className,
      )}
    >
      <div className="min-w-0">
        <h1 className={PAGE_TITLE_CLASS}>{title}</h1>
        {summary !== undefined && summary !== null && summary !== "" && (
          <p className="mt-1 text-sm text-muted-foreground">{summary}</p>
        )}
      </div>
      {actions !== undefined && (
        <div className="flex flex-wrap items-center gap-2">{actions}</div>
      )}
    </header>
  );
}
