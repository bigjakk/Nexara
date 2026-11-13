import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";
import { Panel } from "./Panel";

/**
 * One group of settings: a titled panel whose rows are divided by hairlines.
 * A settings page is a single readable column of these, not a grid of cards.
 */
export function SettingsSection({
  title,
  description,
  actions,
  children,
  className,
  ref,
  tabIndex,
}: {
  title: ReactNode;
  /** One or two sentences on what the group controls. */
  description?: ReactNode;
  /** Controls at the right of the title: an Add button, a status. */
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
} & Pick<ComponentProps<typeof Panel>, "ref" | "tabIndex">) {
  return (
    <Panel
      ref={ref}
      tabIndex={tabIndex}
      title={title}
      actions={actions}
      className={className}
    >
      {description !== undefined && (
        <p className="-mt-2 mb-3 max-w-[65ch] text-sm text-muted-foreground">
          {description}
        </p>
      )}
      <div className="divide-y">{children}</div>
    </Panel>
  );
}

/**
 * One setting: what it is on the left, its control on the right. On a narrow
 * screen the control drops under the label.
 */
export function SettingRow({
  label,
  description,
  htmlFor,
  children,
  className,
}: {
  label: ReactNode;
  /** What the setting does, in a sentence. */
  description?: ReactNode;
  /** The id of the control, so the label names it. */
  htmlFor?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "grid items-center gap-x-8 gap-y-2 py-3.5 first:pt-1 last:pb-0 sm:grid-cols-[minmax(0,1fr)_minmax(0,22rem)]",
        className,
      )}
    >
      <div className="min-w-0">
        <label htmlFor={htmlFor} className="text-sm font-medium">
          {label}
        </label>
        {description !== undefined && (
          <p className="mt-0.5 text-[13px] text-muted-foreground">
            {description}
          </p>
        )}
      </div>
      <div className="flex min-w-0 flex-wrap items-center gap-2 sm:justify-end">
        {children}
      </div>
    </div>
  );
}

/** The Save / Cancel line at the foot of a section. */
export function SettingsFooter({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-wrap items-center justify-end gap-2 pt-3.5",
        className,
      )}
    >
      {children}
    </div>
  );
}
