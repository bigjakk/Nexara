import type { ComponentProps, ReactNode } from "react";
import { AlertTriangle, type LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

interface WarningCalloutProps extends Omit<ComponentProps<"div">, "title"> {
  /** Headline, bolded above the body. Omit for a single-sentence callout. */
  title?: ReactNode;
  /** Defaults to the triangle; pass ShieldAlert for a security-shaped warning. */
  icon?: LucideIcon;
  children: ReactNode;
}

/**
 * The amber advisory a card or a dialog shows above its controls — "this is
 * configured in a way that will not do what you expect", not "this failed".
 * A flat tinted block, no outline: the amber is the only thing that sets it
 * apart from the form around it.
 *
 * Distinct from ConfirmRequiredWarning, which is the gate a dialog puts in
 * front of a submit it is refusing until acknowledged (and is built on this).
 * This one sits inline in a card's flow and does not, by itself, block
 * anything.
 */
export function WarningCallout({
  title,
  icon: Icon = AlertTriangle,
  className,
  children,
  ...props
}: WarningCalloutProps) {
  return (
    <div
      className={cn(
        "flex items-start gap-2.5 rounded-[10px] bg-amber-500/10 px-3.5 py-3",
        className,
      )}
      {...props}
    >
      <Icon
        aria-hidden="true"
        className="mt-px h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400"
      />
      <div className="min-w-0 flex-1 space-y-1 text-[13px] leading-snug text-foreground/80">
        {title !== undefined && (
          <p className="font-semibold text-foreground">{title}</p>
        )}
        {children}
      </div>
    </div>
  );
}
