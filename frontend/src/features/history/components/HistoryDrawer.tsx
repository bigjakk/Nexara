import { useState, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";
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

function stopPropagation(e: SyntheticEvent) {
  e.stopPropagation();
}

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
  const { t } = useTranslation("history");
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      {/* Portaled out of the list row that opens it, the drawer is still that
          row's child to React, which bubbles its events up the row: a click,
          press or key inside would select or open the row. */}
      <SheetContent
        side="right"
        className="w-full overflow-y-auto sm:max-w-4xl"
        onClick={stopPropagation}
        onPointerDown={stopPropagation}
        onKeyDown={stopPropagation}
      >
        <SheetHeader>
          <SheetTitle>{t("drawer.title", { title })}</SheetTitle>
          <SheetDescription>{t("drawer.description")}</SheetDescription>
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
  const { t } = useTranslation("history");
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
        aria-label={t("drawer.open", { title: props.title })}
        title={t("drawer.button")}
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
