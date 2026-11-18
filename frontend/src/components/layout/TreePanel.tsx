import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { PanelLeftClose } from "lucide-react";
import { Button } from "@/components/ui/button";
import { clampTreeWidth, useSidebarStore } from "@/stores/sidebar-store";
import { useTreeVisibility } from "@/hooks/useTreeVisibility";
import { TreeView } from "./TreeView";

/** The inventory tree as its own panel beside the navigation rail. */
export function TreePanel() {
  const { t } = useTranslation("navigation");
  const width = useSidebarStore((s) => s.width);
  const setWidth = useSidebarStore((s) => s.setWidth);
  const { hide } = useTreeVisibility();
  // The width while a drag is in flight. Kept here and stored once on release:
  // the store writes localStorage and re-renders every tree row on each change.
  const [dragWidth, setDragWidth] = useState<number | null>(null);

  const handleMouseDown = useCallback(
    (e: React.MouseEvent) => {
      e.preventDefault();
      const startX = e.clientX;
      let latest = width;

      const onMouseMove = (ev: MouseEvent) => {
        latest = clampTreeWidth(width + (ev.clientX - startX));
        setDragWidth(latest);
      };
      const onMouseUp = () => {
        document.removeEventListener("mousemove", onMouseMove);
        document.removeEventListener("mouseup", onMouseUp);
        document.body.style.cursor = "";
        document.body.style.userSelect = "";
        setWidth(latest);
        setDragWidth(null);
      };

      document.body.style.cursor = "col-resize";
      document.body.style.userSelect = "none";
      document.addEventListener("mousemove", onMouseMove);
      document.addEventListener("mouseup", onMouseUp);
    },
    [width, setWidth],
  );

  return (
    <aside
      aria-label={t("inventory")}
      className="relative flex h-full shrink-0 flex-col border-r bg-background"
      style={{ width: `${String(dragWidth ?? width)}px` }}
    >
      <div className="flex h-14 shrink-0 items-center gap-2 border-b pl-4 pr-2">
        <h2 className="flex-1 truncate text-sm font-semibold">
          {t("inventory")}
        </h2>
        <Button
          variant="ghost"
          size="icon"
          className="h-8 w-8"
          aria-label="Hide inventory tree"
          onClick={hide}
        >
          <PanelLeftClose />
        </Button>
      </div>
      <TreeView className="flex-1" />
      <div
        onMouseDown={handleMouseDown}
        className="absolute inset-y-0 -right-1 z-10 w-2 cursor-col-resize transition-colors hover:bg-primary/20 active:bg-primary/30"
      />
    </aside>
  );
}
