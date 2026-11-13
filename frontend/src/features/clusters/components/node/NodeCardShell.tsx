import type { ReactNode, Ref } from "react";

import { Panel } from "@/components/page/Panel";
import { cn } from "@/lib/utils";

/**
 * A summary panel of the node page that a dialog is opened from: a Panel with,
 * at the right of its title, an optional action.
 *
 * The root takes focus (tabIndex -1) and is handed back as `cardRef`, for a
 * dialog opened from the panel to fall back to when its opener cannot take
 * focus (DialogContent's fallbackFocus): the opener is an Edit button that is
 * not there, or not enabled, by then.
 */
export function NodeCardShell({
  title,
  action,
  flush,
  className,
  cardRef,
  children,
}: {
  title: string;
  action?: ReactNode;
  /** For a table that runs edge to edge: see Panel. */
  flush?: boolean | undefined;
  className?: string | undefined;
  cardRef?: Ref<HTMLDivElement> | undefined;
  children: ReactNode;
}) {
  return (
    <div
      ref={cardRef}
      tabIndex={-1}
      className={cn(
        "min-w-0 rounded-[10px] outline-hidden focus-visible:ring-1 focus-visible:ring-ring",
        className,
      )}
    >
      <Panel title={title} actions={action} flush={flush ?? false}>
        {children}
      </Panel>
    </div>
  );
}
