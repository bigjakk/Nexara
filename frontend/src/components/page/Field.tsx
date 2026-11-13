import {
  cloneElement,
  forwardRef,
  isValidElement,
  type ComponentProps,
  type ReactNode,
} from "react";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

const NOTE = "text-[12.5px] leading-snug";

/** Whether an optional slot holds something to draw: `cond && "text"` may not. */
function given(node: ReactNode): boolean {
  return node !== undefined && node !== null && node !== false && node !== "";
}

/** What a field hands the control it holds, so the two are read together. */
interface ControlProps {
  id?: string | undefined;
  "aria-describedby"?: string | undefined;
  "aria-invalid"?: boolean | undefined;
}

interface FieldProps {
  label: ReactNode;
  /**
   * The id of the control, so the label names it. Leave it out when the field
   * is a group of controls: the label is then plain text above them.
   */
  htmlFor?: string | undefined;
  /** What the value means or how it is read, in small print. */
  hint?: ReactNode;
  /** Something to know about this value that does not block it. */
  warning?: ReactNode;
  /** Why the value is refused. Takes the place of the hint and the warning. */
  error?: ReactNode;
  className?: string;
  children: ReactNode;
}

/**
 * One form field: its label over its control, and its small print under it.
 * A dialog's body is a column (or a FieldGrid) of these.
 *
 * The small print carries the id `<htmlFor>-note`. A control that is the
 * field's one child and has the `htmlFor` id is pointed at it here
 * (`aria-describedby`, and `aria-invalid` on an error); one nested deeper, a
 * Radix Select's trigger say, takes `aria-describedby` itself.
 */
export function Field({
  label,
  htmlFor,
  hint,
  warning,
  error,
  className,
  children,
}: FieldProps) {
  const hasError = given(error);
  const hasWarning = !hasError && given(warning);
  const hasHint = !hasError && given(hint);
  const hasNote = hasError || hasWarning || hasHint;
  const noteId =
    hasNote && htmlFor !== undefined ? `${htmlFor}-note` : undefined;
  const control =
    noteId !== undefined &&
    isValidElement<ControlProps>(children) &&
    children.props.id === htmlFor
      ? cloneElement(children, {
          "aria-describedby": [children.props["aria-describedby"], noteId]
            .filter((id) => id !== undefined)
            .join(" "),
          ...(hasError ? { "aria-invalid": true } : {}),
        })
      : children;
  return (
    <div className={cn("min-w-0 space-y-1.5", className)}>
      {htmlFor !== undefined ? (
        <Label htmlFor={htmlFor} className="block leading-tight">
          {label}
        </Label>
      ) : (
        <p className="text-sm font-medium leading-tight">{label}</p>
      )}
      {control}
      {hasNote && (
        <div id={noteId} className="space-y-1">
          {hasError && (
            <p role="alert" className={cn(NOTE, "text-destructive")}>
              {error}
            </p>
          )}
          {hasWarning && (
            <p className={cn(NOTE, "text-amber-600 dark:text-amber-500")}>
              {warning}
            </p>
          )}
          {hasHint && (
            <p className={cn(NOTE, "text-muted-foreground")}>{hint}</p>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * Fields side by side, two to a row from the `sm` width up and one to a row
 * below it. A field that needs the whole row takes `className="sm:col-span-2"`.
 */
export function FieldGrid({ className, ...props }: ComponentProps<"div">) {
  return (
    <div
      className={cn("grid gap-x-4 gap-y-4 sm:grid-cols-2", className)}
      {...props}
    />
  );
}

interface CheckFieldProps {
  id: string;
  label: ReactNode;
  hint?: ReactNode;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  disabled?: boolean | undefined;
  className?: string;
}

/** A yes-or-no setting: the box, what ticking it does, and a line of help. */
export function CheckField({
  id,
  label,
  hint,
  checked,
  onCheckedChange,
  disabled,
  className,
}: CheckFieldProps) {
  const hasHint = given(hint);
  return (
    <div className={cn("flex min-w-0 items-start gap-2.5", className)}>
      <Checkbox
        id={id}
        aria-describedby={hasHint ? `${id}-note` : undefined}
        checked={checked}
        disabled={disabled}
        onCheckedChange={(state) => {
          onCheckedChange(state === true);
        }}
        className="mt-px"
      />
      <div className="min-w-0 space-y-1">
        <Label
          htmlFor={id}
          className="block text-sm font-normal leading-[1.1rem]"
        >
          {label}
        </Label>
        {hasHint && (
          <p id={`${id}-note`} className={cn(NOTE, "text-muted-foreground")}>
            {hint}
          </p>
        )}
      </div>
    </div>
  );
}

interface FormSectionProps {
  title: ReactNode;
  /** Muted text beside the title: a device name, a count. */
  aside?: ReactNode;
  /** A small control at the right of the title: Remove, Edit. */
  action?: ReactNode;
  className?: string;
  children: ReactNode;
}

/**
 * A group of fields under a small heading. Groups are set apart by a hairline,
 * never by a box around each.
 */
export function FormSection({
  title,
  aside,
  action,
  className,
  children,
}: FormSectionProps) {
  return (
    <section
      className={cn(
        "space-y-3.5 border-t pt-4 first:border-t-0 first:pt-0",
        className,
      )}
    >
      <div className="flex min-h-6 items-center gap-2">
        <h4 className="text-sm font-semibold">{title}</h4>
        {given(aside) && (
          <span className="min-w-0 truncate text-[12.5px] text-muted-foreground">
            {aside}
          </span>
        )}
        {given(action) && <div className="ml-auto">{action}</div>}
      </div>
      {children}
    </section>
  );
}

/**
 * The browser's own select, drawn like ui/input. The browser insets a select's
 * text by 4px of its own, so 8px of padding lines it up with an input's 12px.
 */
export const NativeSelect = forwardRef<
  HTMLSelectElement,
  ComponentProps<"select">
>(({ className, ...props }, ref) => (
  <select
    ref={ref}
    className={cn(
      "flex h-9 w-full min-w-0 rounded-md border border-input bg-transparent px-2 py-1 text-base transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
      className,
    )}
    {...props}
  />
));
NativeSelect.displayName = "NativeSelect";

/** A multi-line text control, drawn like ui/input. */
export const TextArea = forwardRef<
  HTMLTextAreaElement,
  ComponentProps<"textarea">
>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(
      "flex min-h-[60px] w-full min-w-0 rounded-md border border-input bg-transparent px-3 py-2 text-base transition-colors placeholder:text-muted-foreground focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 md:text-sm",
      className,
    )}
    {...props}
  />
));
TextArea.displayName = "TextArea";
