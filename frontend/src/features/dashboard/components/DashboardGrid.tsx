import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ResponsiveGridLayout,
  useContainerWidth,
  type LayoutItem,
  type Layout,
} from "react-grid-layout";
import "react-grid-layout/css/styles.css";
import "react-resizable/css/styles.css";
import { GripVertical, X, Plus, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  DropdownMenuSeparator,
  DropdownMenuLabel,
} from "@/components/ui/dropdown-menu";
import { usePermissions } from "@/hooks/usePermissions";
import {
  getAllAvailableWidgets,
  getTemplate,
  getWidgetLabel,
  widgetAllowed,
  type ClusterInfo,
  type DashboardPreset,
} from "../lib/widget-registry";
import { cn } from "@/lib/utils";

export type { LayoutItem };

interface DashboardGridProps {
  preset: DashboardPreset;
  defaultPreset: DashboardPreset;
  clusters: ClusterInfo[];
  clusterNames: Map<string, string>;
  onLayoutChange: (layouts: LayoutItem[], widgetIds: string[]) => void;
  onReset: () => void;
  editMode: boolean;
  children: (widgetId: string) => React.ReactNode;
}

export function DashboardGrid({
  preset,
  defaultPreset,
  clusters,
  clusterNames,
  onLayoutChange,
  onReset,
  editMode,
  children,
}: DashboardGridProps) {
  const { hasGlobalPermission } = usePermissions();
  const [currentLayouts, setCurrentLayouts] = useState<LayoutItem[]>(() =>
    preset.layouts.map((l) => ({ ...l })),
  );
  const [activeWidgetIds, setActiveWidgetIds] = useState<string[]>(() => [
    ...preset.widgetIds,
  ]);

  // Sync internal state when preset changes from parent (e.g. reset, preset switch)
  useEffect(() => {
    setCurrentLayouts(preset.layouts.map((l) => ({ ...l })));
    setActiveWidgetIds([...preset.widgetIds]);
  }, [preset]);

  const { width, containerRef } = useContainerWidth({
    measureBeforeMount: true,
  });

  const handleLayoutChange = useCallback(
    (layout: Layout) => {
      const mutableLayout = layout.map((item) => ({ ...item }));
      setCurrentLayouts(mutableLayout);
      if (editMode) {
        onLayoutChange(mutableLayout, activeWidgetIds);
      }
    },
    [editMode, onLayoutChange, activeWidgetIds],
  );

  const handleResponsiveLayoutChange = useCallback(
    (...args: [Layout, Partial<Record<string, Layout>>]) => {
      // When the viewport crosses a breakpoint (e.g. lg → md), react-grid-layout
      // generates a layout fitted to the new breakpoint and fires this callback
      // with that derived layout as args[0]. Saving it back overwrites our
      // canonical `lg` layout, so on the way back up the dashboard renders with
      // the compacted small-breakpoint layout — the "stuck after resize" bug.
      // args[1].lg is preserved across breakpoint changes; only real drag/resize
      // at the lg breakpoint mutates it.
      handleLayoutChange(args[1]["lg"] ?? args[0]);
    },
    [handleLayoutChange],
  );

  const addWidget = useCallback(
    (widgetId: string) => {
      if (activeWidgetIds.includes(widgetId)) return;
      const template = getTemplate(widgetId);
      if (!template) return;

      const maxY = currentLayouts.reduce(
        (max, l) => Math.max(max, l.y + l.h),
        0,
      );

      const dl = template.defaultLayout;
      const newLayout: LayoutItem = {
        i: widgetId,
        x: 0,
        y: maxY,
        w: dl.w,
        h: dl.h,
        ...(dl.minW != null ? { minW: dl.minW } : {}),
        ...(dl.minH != null ? { minH: dl.minH } : {}),
      };

      const updatedLayouts = [...currentLayouts, newLayout];
      const updatedIds = [...activeWidgetIds, widgetId];
      setCurrentLayouts(updatedLayouts);
      setActiveWidgetIds(updatedIds);
      onLayoutChange(updatedLayouts, updatedIds);
    },
    [activeWidgetIds, currentLayouts, onLayoutChange],
  );

  const removeWidget = useCallback(
    (widgetId: string) => {
      const updatedLayouts = currentLayouts.filter((l) => l.i !== widgetId);
      const updatedIds = activeWidgetIds.filter((id) => id !== widgetId);
      setCurrentLayouts(updatedLayouts);
      setActiveWidgetIds(updatedIds);
      onLayoutChange(updatedLayouts, updatedIds);
    },
    [activeWidgetIds, currentLayouts, onLayoutChange],
  );

  const handleReset = useCallback(() => {
    const layouts = defaultPreset.layouts.map((l) => ({ ...l }));
    const ids = [...defaultPreset.widgetIds];
    setCurrentLayouts(layouts);
    setActiveWidgetIds(ids);
    onReset();
  }, [defaultPreset, onReset]);

  const availableWidgets = useMemo(
    () =>
      getAllAvailableWidgets(clusters).filter(
        (w) =>
          !activeWidgetIds.includes(w.id) &&
          widgetAllowed(w.template, hasGlobalPermission),
      ),
    [activeWidgetIds, clusters, hasGlobalPermission],
  );

  return (
    <div ref={containerRef}>
      {editMode && (
        <div className="mb-3 flex flex-wrap items-center gap-x-3 gap-y-2">
          <p className="mr-auto text-[12.5px] text-muted-foreground">
            Drag a widget by its bar to move it, and by its corner to resize it.
            Lock to save the layout.
          </p>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                size="sm"
                variant="outline"
                className="gap-1"
                disabled={availableWidgets.length === 0}
              >
                <Plus className="h-4 w-4" />
                Add widget
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              align="end"
              className="max-h-80 w-72 overflow-y-auto"
            >
              <DropdownMenuLabel>Available widgets</DropdownMenuLabel>
              <DropdownMenuSeparator />
              {availableWidgets.map((w) => (
                <DropdownMenuItem
                  key={w.id}
                  onClick={() => {
                    addWidget(w.id);
                  }}
                >
                  <div>
                    <div className="font-medium">{w.label}</div>
                    <div className="text-xs text-muted-foreground">
                      {w.description}
                    </div>
                  </div>
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>

          <Button
            size="sm"
            variant="ghost"
            className="gap-1"
            onClick={handleReset}
          >
            <RotateCcw className="h-4 w-4" />
            Reset to default
          </Button>
        </div>
      )}

      {width > 0 && (
        <ResponsiveGridLayout
          className="layout"
          width={width}
          layouts={{ lg: currentLayouts }}
          breakpoints={{ lg: 1200, md: 996, sm: 768, xs: 480, xxs: 0 }}
          cols={{ lg: 12, md: 12, sm: 6, xs: 4, xxs: 2 }}
          rowHeight={30}
          dragConfig={{
            enabled: editMode,
            handle: ".widget-drag-handle",
          }}
          resizeConfig={{ enabled: editMode }}
          margin={[16, 16]}
          containerPadding={[0, 0]}
          onLayoutChange={handleResponsiveLayoutChange}
        >
          {activeWidgetIds.map((widgetId) => (
            <div
              key={widgetId}
              className={cn(
                // While customising, each cell shows its bounds: that is
                // what gets dragged and resized.
                editMode && "rounded-[10px] ring-1 ring-input",
              )}
            >
              {editMode && (
                <WidgetOverlay
                  widgetId={widgetId}
                  clusterNames={clusterNames}
                  onRemove={() => {
                    removeWidget(widgetId);
                  }}
                />
              )}
              <div className="h-full w-full overflow-hidden rounded-[10px]">
                {children(widgetId)}
              </div>
            </div>
          ))}
        </ResponsiveGridLayout>
      )}
    </div>
  );
}

function WidgetOverlay({
  widgetId,
  clusterNames,
  onRemove,
}: {
  widgetId: string;
  clusterNames: Map<string, string>;
  onRemove: () => void;
}) {
  const label = getWidgetLabel(widgetId, clusterNames);
  // An opaque bar over the widget's own title row: the same name, plus the
  // grip that moves it and the button that removes it.
  return (
    <div className="absolute inset-x-0 top-0 z-10 flex h-9 items-center gap-1 rounded-t-[10px] border-b bg-secondary pl-1.5 pr-1 text-secondary-foreground">
      <div className="widget-drag-handle flex h-full min-w-0 flex-1 cursor-grab items-center gap-1.5 active:cursor-grabbing">
        <GripVertical
          aria-hidden="true"
          className="h-4 w-4 shrink-0 text-muted-foreground"
        />
        <span className="truncate text-[13px] font-medium">{label}</span>
      </div>
      <button
        type="button"
        aria-label={`Remove ${label}`}
        onClick={onRemove}
        className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-muted-foreground outline-hidden hover:bg-foreground/10 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
      >
        <X className="h-4 w-4" />
      </button>
    </div>
  );
}
