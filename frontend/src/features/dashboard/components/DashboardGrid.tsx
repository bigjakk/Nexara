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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  getAllAvailableWidgets,
  getTemplate,
  getWidgetLabel,
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
  const [currentLayouts, setCurrentLayouts] = useState<LayoutItem[]>(
    () => preset.layouts.map((l) => ({ ...l })),
  );
  const [activeWidgetIds, setActiveWidgetIds] = useState<string[]>(
    () => [...preset.widgetIds],
  );

  // Sync internal state when preset changes from parent (e.g. reset, preset switch)
  useEffect(() => {
    setCurrentLayouts(preset.layouts.map((l) => ({ ...l })));
    setActiveWidgetIds([...preset.widgetIds]);
  }, [preset]);

  const { width, containerRef } = useContainerWidth({ measureBeforeMount: true });

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
      handleLayoutChange(args[0]);
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
    () => getAllAvailableWidgets(clusters).filter((w) => !activeWidgetIds.includes(w.id)),
    [activeWidgetIds, clusters],
  );

  return (
    <div ref={containerRef}>
      {editMode && (
        <div className="mb-4 flex items-center gap-2">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button
                size="sm"
                variant="outline"
                className="gap-1"
                disabled={availableWidgets.length === 0}
              >
                <Plus className="h-4 w-4" />
                Add Widget
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-64 max-h-80 overflow-y-auto">
              <DropdownMenuLabel>Available Widgets</DropdownMenuLabel>
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
            variant="outline"
            className="gap-1"
            onClick={handleReset}
          >
            <RotateCcw className="h-4 w-4" />
            Reset to Default
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
              className={cn(editMode && "rounded-lg ring-1 ring-border")}
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
              <div className="h-full w-full overflow-auto">
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
  return (
    <Card className="absolute inset-x-0 top-0 z-10 flex h-8 items-center justify-between rounded-b-none border-b bg-muted/80 px-2 backdrop-blur-sm">
      <CardHeader className="flex flex-row items-center gap-1 p-0">
        <GripVertical className="widget-drag-handle h-4 w-4 cursor-grab text-muted-foreground" />
        <CardTitle className="text-xs font-medium">
          {label}
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        <button
          onClick={onRemove}
          className="rounded p-0.5 text-muted-foreground hover:bg-destructive/10 hover:text-destructive"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </CardContent>
    </Card>
  );
}
