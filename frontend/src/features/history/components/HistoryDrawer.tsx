import { useState } from "react";
import { History } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet";
import { usePermissions } from "@/hooks/usePermissions";
import type { HistoryKind, HistoryTarget } from "../types/history";
import { HistoryTimeline } from "./HistoryTimeline";

interface HistoryDrawerProps {
  kind: HistoryKind;
  target: HistoryTarget;
  /** What the History is of, e.g. the object's name. */
  title: string;
}

/**
 * One object's History in a drawer, for an object that has a list row rather
 * than a page of its own: what the generic selector serves (audit rows by the
 * object's resource identity). It reads only while open.
 */
export function HistoryDrawer({
  kind,
  target,
  title,
  open,
  onOpenChange,
}: HistoryDrawerProps & {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full overflow-y-auto sm:max-w-4xl"
      >
        <SheetHeader>
          <SheetTitle>History: {title}</SheetTitle>
          <SheetDescription>
            What happened to it, newest first — in Nexara and in Proxmox.
          </SheetDescription>
        </SheetHeader>
        <div className="mt-4">
          <HistoryTimeline kind={kind} target={target} enabled={open} />
        </div>
      </SheetContent>
    </Sheet>
  );
}

/**
 * A list row's History button and its drawer. Shown only with view:audit and
 * view:task, as a guest's History tab is: the timeline is the audit log, and
 * its rows open Proxmox task logs, so without both it would be a table of
 * refusals. The row's own view permission is the page's.
 */
export function HistoryButton(props: HistoryDrawerProps) {
  const { hasPermission } = usePermissions();
  const [open, setOpen] = useState(false);
  if (!hasPermission("view", "audit") || !hasPermission("view", "task")) {
    return null;
  }
  return (
    <>
      <Button
        variant="ghost"
        size="icon"
        className="h-7 w-7"
        aria-label={`History of ${props.title}`}
        title="History"
        onClick={(e) => {
          e.stopPropagation();
          setOpen(true);
        }}
      >
        <History className="h-4 w-4" />
      </Button>
      <HistoryDrawer {...props} open={open} onOpenChange={setOpen} />
    </>
  );
}
