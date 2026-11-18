import {
  use,
  useId,
  type ComponentProps,
  type ReactNode,
  type Ref,
} from "react";
import { ChevronDown, Loader2, Pencil, Save } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";
import { useTranslation } from "react-i18next";
import { RowsReadOnlyContext } from "./read-only";

/**
 * The layout pieces of a guest's Hardware and Resources tabs: groups of
 * fields, device rows and the save bar. Presentation only. The panels own
 * every value, every staged change and what a Save sends. Under
 * RowsReadOnlyContext the pieces draw themselves read-only.
 */

/** The groups of one panel, divided by hairlines. */
export function GroupList({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("@container divide-y border-t", className)}>
      {children}
    </div>
  );
}

/**
 * One group of related settings: what it is and what it comes to on the left,
 * its controls on the right. In a narrow panel the controls drop underneath.
 */
export function Group({
  title,
  summary,
  children,
}: {
  title: ReactNode;
  /** The group's current state in a few words: "8 vCPUs", "4 GiB". */
  summary?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="grid gap-x-6 gap-y-3 px-[18px] py-4 @xl:grid-cols-[10.5rem_minmax(0,1fr)]">
      <div className="min-w-0">
        <h3 className="text-sm font-medium">{title}</h3>
        {summary !== undefined && summary !== "" && (
          <p className="mt-0.5 text-[12.5px] text-muted-foreground">
            {summary}
          </p>
        )}
      </div>
      <div className="min-w-0 space-y-3">{children}</div>
    </div>
  );
}

/** Fields side by side, as many as fit, all one width. */
export function FieldGrid({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "grid grid-cols-[repeat(auto-fill,minmax(min(8.5rem,100%),1fr))] gap-x-3 gap-y-3",
        className,
      )}
    >
      {children}
    </div>
  );
}

/**
 * One labelled control. The label wraps the control, so it names it; a note
 * under the control is not part of that name.
 */
export function Field({
  label,
  note,
  className,
  children,
}: {
  label: string;
  note?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  const readOnly = use(RowsReadOnlyContext);
  const content = (
    <>
      <label className="block space-y-1">
        <span className="block truncate text-[12.5px] text-muted-foreground">
          {label}
        </span>
        {children}
      </label>
      {note}
    </>
  );
  // A disabled fieldset disables the control, whichever kind it is.
  return readOnly ? (
    <fieldset disabled className={cn("min-w-0", className)}>
      {content}
    </fieldset>
  ) : (
    <div className={cn("min-w-0", className)}>{content}</div>
  );
}

/** Checkboxes on one wrapping line. */
export function Checks({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap gap-x-5 gap-y-2">{children}</div>;
}

export function Check({
  label,
  id: givenId,
  disabled,
  ...props
}: Omit<ComponentProps<typeof Checkbox>, "children"> & { label: ReactNode }) {
  const readOnly = use(RowsReadOnlyContext);
  const ownId = useId();
  const id = givenId ?? ownId;
  return (
    <div className="flex items-center gap-2">
      <Checkbox id={id} disabled={readOnly || disabled} {...props} />
      <Label htmlFor={id} className="cursor-pointer font-normal">
        {label}
      </Label>
    </div>
  );
}

/** A pending change's marker: amber, as everything waiting on a Save is. */
export function PendingBadge({ children }: { children: ReactNode }) {
  return <Badge variant="warning">{children}</Badge>;
}

/** A quiet icon button at the end of a row. */
export function IconAction({
  label,
  icon: Icon,
  destructive = false,
  className,
  ...props
}: Omit<ComponentProps<typeof Button>, "children"> & {
  /** The accessible name and the hover hint. */
  label: string;
  icon: LucideIcon;
  destructive?: boolean;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      aria-label={label}
      title={label}
      className={cn(
        "h-7 w-7 p-0 text-muted-foreground",
        destructive ? "hover:text-destructive" : "hover:text-foreground",
        className,
      )}
      {...props}
    >
      <Icon className="h-4 w-4" />
    </Button>
  );
}

/** The devices of one panel, one per row. */
export function DeviceList({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <ul className={cn("divide-y border-t empty:hidden", className)}>
      {children}
    </ul>
  );
}

/**
 * One device: what it is in plain words, its config key, a one-line summary
 * underneath, and its controls at the end of the row. With an editor, a
 * pencil opens it under the row.
 *
 * The key, the marker and the controls are all children of the one row
 * element, so "the row that holds scsi0" is the key's parent.
 */
export function DeviceRow({
  name,
  icon: Icon,
  title,
  summary,
  marker,
  struck = false,
  actions,
  ownActions,
  editor,
  open = false,
  onToggle,
  editLabel,
}: {
  /** The config key: scsi0, net1, usb2. */
  name: string;
  icon: LucideIcon;
  /** The kind of device, in plain words. */
  title: string;
  /** Spans, drawn on one line with a dot between them. */
  summary?: ReactNode;
  /** A PendingBadge, or a state the row is in. */
  marker?: ReactNode;
  /** Drawn struck through: the row is leaving on the next Save. */
  struck?: boolean;
  actions?: ReactNode;
  /** Controls that take a permission of their own, kept on a read-only tab. */
  ownActions?: ReactNode;
  /**
   * The device's own fields, under the row while it is open. Without
   * onToggle they are always there: a device just added has to be filled in.
   */
  editor?: ReactNode;
  open?: boolean;
  onToggle?: (() => void) | undefined;
  /** The pencil's name when the editor does one thing: "Resize rootfs". */
  editLabel?: string;
}) {
  const { t } = useTranslation("vms");
  const editorId = useId();
  const readOnly = use(RowsReadOnlyContext);
  const rowActions = readOnly ? undefined : actions;
  // Read-only, a device's own fields still open, to be read; an editor that
  // does one thing (editLabel: "Resize rootfs") is not offered.
  const rowEditor = readOnly && editLabel !== undefined ? undefined : editor;
  const hasEditor = rowEditor !== undefined && onToggle !== undefined;
  const showEditor =
    rowEditor !== undefined && (onToggle === undefined || open);
  const editorClass = "space-y-3 px-[18px] pb-4 pl-[44px] pt-1";
  return (
    <li>
      <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1 px-[18px] py-2.5 text-sm">
        <Icon
          aria-hidden="true"
          className="h-4 w-4 shrink-0 text-muted-foreground"
        />
        <span
          className={cn(
            "font-medium",
            struck && "text-muted-foreground line-through",
          )}
        >
          {title}
        </span>
        <span
          className={cn(
            "font-mono text-[12.5px] text-muted-foreground",
            struck && "line-through",
          )}
        >
          {name}
        </span>
        {marker}
        {(hasEditor ||
          rowActions !== undefined ||
          ownActions !== undefined) && (
          <div className="ml-auto flex min-w-0 flex-wrap items-center justify-end gap-1">
            {hasEditor && (
              <IconAction
                label={
                  readOnly
                    ? t("hardwareRows.showName", { name })
                    : (editLabel ?? t("hardwareRows.editName", { name }))
                }
                icon={readOnly ? ChevronDown : Pencil}
                aria-expanded={open}
                aria-controls={open ? editorId : undefined}
                onClick={onToggle}
                className={cn(open && "bg-accent text-foreground")}
              />
            )}
            {rowActions}
            {ownActions}
          </div>
        )}
        {summary !== undefined && (
          <div className="flex min-w-0 basis-full flex-wrap gap-x-2 pl-[26px] text-[12.5px] text-muted-foreground [&>*:not(:last-child)]:after:ml-2 [&>*:not(:last-child)]:after:content-['·']">
            {summary}
          </div>
        )}
      </div>
      {showEditor &&
        (readOnly ? (
          <fieldset
            disabled
            id={editorId}
            className={cn("min-w-0", editorClass)}
          >
            {rowEditor}
          </fieldset>
        ) : (
          <div id={editorId} className={editorClass}>
            {rowEditor}
          </div>
        ))}
    </li>
  );
}

/** A row that opens the rarely used half of a panel. */
export function DisclosureRow({
  open,
  onToggle,
  children,
}: {
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      aria-expanded={open}
      onClick={onToggle}
      className="flex w-full items-center gap-2 border-t px-[18px] py-3 text-left text-sm font-medium outline-hidden transition-colors hover:bg-foreground/[0.03] focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
    >
      <ChevronDown
        aria-hidden="true"
        className={cn(
          "h-4 w-4 text-muted-foreground transition-transform",
          !open && "-rotate-90",
        )}
      />
      {children}
    </button>
  );
}

/**
 * The tab's Save, pinned to the foot of the pane so it is in reach from any
 * panel. It says whether there is anything to save; the panel's own messages
 * (saved, failed, why not) are its children.
 */
export function SaveBar({
  ref,
  dirty,
  saving,
  disabled,
  onSave,
  dirtyLabel,
  hideIdle = false,
  children,
}: {
  ref?: Ref<HTMLDivElement>;
  dirty: boolean;
  saving: boolean;
  disabled: boolean;
  onSave: () => void;
  dirtyLabel?: ReactNode;
  /** A message among the children already says where things stand. */
  hideIdle?: boolean;
  children?: ReactNode;
}) {
  const { t } = useTranslation("vms");
  if (use(RowsReadOnlyContext)) return null;
  return (
    <div
      ref={ref}
      role="group"
      aria-label={t("hardwareRows.changes")}
      tabIndex={-1}
      // The negative offset is the main pane's own padding: without it the
      // bar would stick that far above the pane's edge, with the page showing
      // through underneath.
      className="sticky -bottom-4 z-10 flex flex-wrap items-center gap-x-4 gap-y-2 border-t bg-background/95 py-3 outline-none backdrop-blur-sm md:-bottom-6"
    >
      <div className="flex min-w-0 flex-1 basis-[10rem] flex-wrap items-center gap-x-3 gap-y-1 text-sm">
        {dirty ? (
          <span className="flex items-center gap-[7px] font-medium text-amber-700 dark:text-amber-400">
            <span
              aria-hidden="true"
              className="h-[7px] w-[7px] rounded-full bg-amber-500"
            />
            {dirtyLabel ?? t("hardwareRows.unsavedChanges")}
          </span>
        ) : (
          !hideIdle && (
            <span className="text-muted-foreground">
              {t("hardwareRows.noUnsavedChanges")}
            </span>
          )
        )}
        {children}
      </div>
      <Button
        size="sm"
        variant={dirty ? "default" : "outline"}
        className="gap-1.5"
        disabled={disabled}
        onClick={onSave}
      >
        {saving ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
        ) : (
          <Save className="h-3.5 w-3.5" />
        )}
        {t("hardwareRows.saveChanges")}
      </Button>
    </div>
  );
}
