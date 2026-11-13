import { useId, type ReactNode } from "react";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

/**
 * A titled group of fields in a dialog form, set off by a rule above it in
 * place of a box around it.
 */
export function FormSection({
  title,
  children,
}: {
  title: string;
  children: ReactNode;
}) {
  const id = useId();
  return (
    <div role="group" aria-labelledby={id} className="space-y-4 border-t pt-4">
      <h3 id={id} className="text-[13px] font-semibold text-muted-foreground">
        {title}
      </h3>
      {children}
    </div>
  );
}

/** A label, the control it names, and a line of help under it. */
export function Field({
  label,
  hint,
  className,
  children,
}: {
  label: string;
  hint?: ReactNode;
  className?: string;
  /** Renders the control, which takes the id the label points at. */
  children: (id: string) => ReactNode;
}) {
  const id = useId();
  return (
    <div className={cn("min-w-0 space-y-1.5", className)}>
      <Label htmlFor={id}>{label}</Label>
      {children(id)}
      {hint !== undefined && (
        <p className="text-[12.5px] text-muted-foreground">{hint}</p>
      )}
    </div>
  );
}
