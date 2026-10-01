import type { ReactNode, Ref } from "react";

import { cn } from "@/lib/utils";

/**
 * The chrome of the node page's summary cards (the same as HardwareSection's in
 * NodeDetailPage): a bordered box with an icon, a title and, at the right of
 * the header, an optional action.
 *
 * The root takes focus (tabIndex -1) and is handed back as `cardRef`, for a
 * dialog opened from the card to fall back to when its opener cannot take focus
 * (DialogContent's fallbackFocus): the opener is an Edit button that is not
 * there, or not enabled, by then.
 */
export function NodeCardShell({
  icon,
  title,
  action,
  className,
  cardRef,
  children,
}: {
  icon: ReactNode;
  title: string;
  action?: ReactNode;
  className?: string | undefined;
  cardRef?: Ref<HTMLDivElement> | undefined;
  children: ReactNode;
}) {
  return (
    <div
      ref={cardRef}
      tabIndex={-1}
      className={cn(
        "rounded-lg border p-4 outline-hidden focus-visible:ring-1 focus-visible:ring-ring",
        className,
      )}
    >
      <div className="mb-3 flex items-center justify-between text-sm font-medium">
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground">{icon}</span>
          {title}
        </div>
        {action}
      </div>
      {children}
    </div>
  );
}
