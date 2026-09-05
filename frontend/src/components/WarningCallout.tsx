import type { ReactNode } from "react";
import { AlertTriangle, type LucideIcon } from "lucide-react";

interface WarningCalloutProps {
  /** Headline, bolded above the body. Omit for a single-sentence callout. */
  title?: string;
  /** Defaults to the triangle; pass ShieldAlert for a security-shaped warning. */
  icon?: LucideIcon;
  children: ReactNode;
}

/**
 * The amber advisory box a card shows above its controls — "this is configured
 * in a way that will not do what you expect", not "this failed".
 *
 * Distinct from ConfirmRequiredWarning, which is the larger gate a dialog puts
 * in front of a submit it is refusing until acknowledged. This one sits inline
 * in a card's flow and does not, by itself, block anything.
 */
export function WarningCallout({
  title,
  icon: Icon = AlertTriangle,
  children,
}: WarningCalloutProps) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-amber-300 bg-amber-50 p-3 dark:border-amber-700 dark:bg-amber-950">
      <Icon className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
      <div className="space-y-1 text-xs text-amber-700 dark:text-amber-300">
        {title !== undefined && <p className="font-medium">{title}</p>}
        {children}
      </div>
    </div>
  );
}
