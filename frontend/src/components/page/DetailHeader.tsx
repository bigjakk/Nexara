import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ChevronRight } from "lucide-react";
import { StatusIcon } from "@/components/StatusIcon";
import { cn } from "@/lib/utils";
import { PAGE_TITLE_CLASS } from "./PageHeader";

export interface Crumb {
  label: string;
  /** Absent on the last crumb, which is the page itself. */
  to?: string | undefined;
}

/** Where this page sits: each ancestor a link, the page itself plain text. */
export function Breadcrumbs({ crumbs }: { crumbs: readonly Crumb[] }) {
  return (
    <nav aria-label="Breadcrumb">
      <ol className="flex flex-wrap items-center gap-1.5 text-[12.5px]">
        {crumbs.map((crumb, i) => (
          <li key={`${crumb.label}-${String(i)}`} className="contents">
            {i > 0 && (
              <ChevronRight
                aria-hidden="true"
                className="h-3 w-3 text-muted-foreground/60"
              />
            )}
            {crumb.to !== undefined ? (
              <Link
                to={crumb.to}
                className="text-muted-foreground hover:text-foreground hover:underline"
              >
                {crumb.label}
              </Link>
            ) : (
              <span aria-current="page" className="text-muted-foreground/75">
                {crumb.label}
              </span>
            )}
          </li>
        ))}
      </ol>
    </nav>
  );
}

/** One `label value` pair on a detail page's facts line. */
export function Fact({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span className={cn("flex min-w-0 items-baseline gap-1.5", className)}>
      <span className="shrink-0 text-muted-foreground/75">{label}</span>
      <span className="min-w-0 text-muted-foreground [overflow-wrap:anywhere]">
        {children}
      </span>
    </span>
  );
}

/**
 * The state the thing is in, first on the facts line: a dot, the state in
 * words, and what qualifies it ("for 8 days").
 */
export function StatusFact({
  status,
  label,
  detail,
  nameDot = false,
}: {
  /** What StatusIcon understands: running, stopped, online, offline, … */
  status: string;
  label: string;
  detail?: ReactNode;
  /**
   * Name the dot by `label`, where `status` only picks its colour and its own
   * word would be wrong ("offline" for a UPS on low battery).
   */
  nameDot?: boolean;
}) {
  return (
    <span className="flex items-center gap-[7px]">
      <StatusIcon status={status} label={nameDot ? label : undefined} />
      <strong className="font-[550] text-foreground">{label}</strong>
      {detail}
    </span>
  );
}

interface DetailHeaderProps {
  crumbs: readonly Crumb[];
  /** The icon in the identity tile. */
  icon: ReactNode;
  /** A string is the page's h1. Pass a node to swap in an inline editor. */
  title: ReactNode;
  /** Beside the title: a rename button, a Template badge. */
  titleAside?: ReactNode;
  /** `Fact` and `StatusFact` children. */
  facts?: ReactNode;
  /** Controls on the right. At most one of them is a primary button. */
  actions?: ReactNode;
  className?: string;
}

/**
 * The top of a page about one thing (a guest, a node, a cluster, a store):
 * where it sits, what it is called, the state it is in, what can be done.
 */
export function DetailHeader({
  crumbs,
  icon,
  title,
  titleAside,
  facts,
  actions,
  className,
}: DetailHeaderProps) {
  return (
    <header className={cn("space-y-2.5", className)}>
      <Breadcrumbs crumbs={crumbs} />
      <div className="flex flex-wrap items-start gap-x-3.5 gap-y-3">
        <span
          aria-hidden="true"
          className="grid h-11 w-11 shrink-0 place-items-center rounded-[10px] bg-card text-muted-foreground [&_svg]:size-6"
        >
          {icon}
        </span>
        <div className="min-w-0 flex-[1_1_240px]">
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            {typeof title === "string" ? (
              <h1 className={PAGE_TITLE_CLASS}>{title}</h1>
            ) : (
              title
            )}
            {titleAside}
          </div>
          {facts !== undefined && (
            <div className="mt-1.5 flex flex-wrap items-baseline gap-x-5 gap-y-0.5 text-sm text-muted-foreground">
              {facts}
            </div>
          )}
        </div>
        {actions !== undefined && (
          <div className="flex flex-wrap items-center gap-2">{actions}</div>
        )}
      </div>
    </header>
  );
}
