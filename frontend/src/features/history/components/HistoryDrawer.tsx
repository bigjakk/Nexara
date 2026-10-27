import { useEffect, type SyntheticEvent } from "react";
import { useTranslation } from "react-i18next";
import { useLocation } from "react-router-dom";
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
import {
  useHistoryDrawerStore,
  type HistoryDrawerRequest,
} from "@/stores/history-drawer-store";
import { HistoryTimeline } from "./HistoryTimeline";

function stopPropagation(e: SyntheticEvent) {
  e.stopPropagation();
}

/**
 * One object's History in a drawer, for an object that has a list row rather
 * than a page of its own: what the generic selector serves (audit rows by the
 * object's resource identity). It reads only while open. Draw it outside any
 * clickable row — HistoryDrawerHost does — or the row gets its events.
 */
export function HistoryDrawer({
  kind,
  target,
  title,
  rowsAbove,
  open,
  onOpenChange,
}: HistoryDrawerRequest & {
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { t } = useTranslation("history");
  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full overflow-y-auto sm:max-w-4xl"
      >
        <SheetHeader>
          <SheetTitle>{t("drawer.title", { title })}</SheetTitle>
          <SheetDescription>{t("drawer.description")}</SheetDescription>
        </SheetHeader>
        <div className="mt-4">
          <HistoryTimeline
            kind={kind}
            target={target}
            enabled={open}
            rowsAbove={rowsAbove}
          />
        </div>
      </SheetContent>
    </Sheet>
  );
}

/**
 * The drawer every HistoryButton opens, mounted once in AppShell, outside the
 * rows (useHistoryDrawerStore says why). It belongs to the list it was opened
 * from, so a move to another page closes it.
 */
export function HistoryDrawerHost() {
  const request = useHistoryDrawerStore((s) => s.request);
  const isOpen = useHistoryDrawerStore((s) => s.isOpen);
  const close = useHistoryDrawerStore((s) => s.close);
  const { pathname } = useLocation();
  useEffect(() => {
    close();
  }, [pathname, close]);
  if (request === null) return null;
  return (
    <HistoryDrawer
      {...request}
      open={isOpen}
      onOpenChange={(open) => {
        if (!open) close();
      }}
    />
  );
}

/**
 * A list row's History button. Shown only with view:audit and view:task, as a
 * guest's History tab is: the timeline is the audit log, and its rows open
 * Proxmox task logs, so without both it would be a table of refusals. The
 * row's own view permission is the page's. Its own click, press and keys stop
 * here; the drawer it opens is HistoryDrawerHost's.
 */
export function HistoryButton(props: HistoryDrawerRequest) {
  const { t } = useTranslation("history");
  const { hasPermission } = usePermissions();
  const open = useHistoryDrawerStore((s) => s.open);
  if (!hasPermission("view", "audit") || !hasPermission("view", "task")) {
    return null;
  }
  return (
    <Button
      variant="ghost"
      size="icon"
      className="h-7 w-7"
      aria-label={t("drawer.open", { title: props.title })}
      title={t("drawer.button")}
      onPointerDown={stopPropagation}
      onKeyDown={stopPropagation}
      onClick={(e) => {
        e.stopPropagation();
        open(props);
      }}
    >
      <History className="h-4 w-4" />
    </Button>
  );
}
