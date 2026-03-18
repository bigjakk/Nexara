import { useCallback, useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import {
  Minus,
  X,
  Maximize2,
  Minimize2,
  Maximize,
  TerminalSquare,
} from "lucide-react";
import { useConsoleStore } from "@/stores/console-store";
import { ConsoleTabBar } from "./ConsoleTabBar";
import { QuickConnect } from "./QuickConnect";
import { VNCViewer } from "./VNCViewer";
import { Terminal } from "./Terminal";

/**
 * Title bar with drag handle, tab label, and window controls.
 */
function TitleBar({
  onPointerDown,
  onPointerMove,
  onPointerUp,
}: {
  onPointerDown: (e: React.PointerEvent) => void;
  onPointerMove: (e: React.PointerEvent) => void;
  onPointerUp: (e: React.PointerEvent) => void;
}) {
  const windowMode = useConsoleStore((s) => s.windowMode);
  const setWindowMode = useConsoleStore((s) => s.setWindowMode);
  const tabs = useConsoleStore((s) => s.tabs);
  const activeTabId = useConsoleStore((s) => s.activeTabId);

  const activeTab = tabs.find((t) => t.id === activeTabId) ?? tabs[0];

  function handleDoubleClick() {
    setWindowMode(windowMode === "maximized" ? "floating" : "maximized");
  }

  return (
    <div
      className="flex h-8 shrink-0 cursor-grab items-center gap-2 border-b bg-muted/50 px-2 select-none active:cursor-grabbing"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onDoubleClick={handleDoubleClick}
    >
      <TerminalSquare className="h-3.5 w-3.5 text-muted-foreground" />
      <span className="min-w-0 flex-1 truncate text-xs font-medium">
        {activeTab?.label ?? "Console"}
      </span>

      {/* Window controls */}
      <div className="flex items-center gap-1">
        <button
          className="flex h-6 w-6 items-center justify-center rounded hover:bg-accent"
          onClick={(e) => { e.stopPropagation(); setWindowMode("minimized"); }}
          title="Minimize"
        >
          <Minus className="h-3.5 w-3.5" />
        </button>
        <button
          className="flex h-6 w-6 items-center justify-center rounded hover:bg-accent"
          onClick={(e) => {
            e.stopPropagation();
            setWindowMode(windowMode === "maximized" ? "floating" : "maximized");
          }}
          title={windowMode === "maximized" ? "Restore" : "Maximize"}
        >
          {windowMode === "maximized" ? (
            <Minimize2 className="h-3.5 w-3.5" />
          ) : (
            <Maximize2 className="h-3.5 w-3.5" />
          )}
        </button>
        <button
          className="flex h-6 w-6 items-center justify-center rounded hover:bg-destructive/20 hover:text-destructive"
          onClick={(e) => { e.stopPropagation(); setWindowMode("hidden"); }}
          title="Close"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      </div>
    </div>
  );
}

type ResizeDir = "nw" | "ne" | "sw" | "se";

const resizeCorners: { dir: ResizeDir; className: string }[] = [
  { dir: "nw", className: "absolute left-0 top-0 h-3 w-3 cursor-nw-resize" },
  { dir: "ne", className: "absolute right-0 top-0 h-3 w-3 cursor-ne-resize" },
  { dir: "sw", className: "absolute bottom-0 left-0 h-3 w-3 cursor-sw-resize" },
  { dir: "se", className: "absolute bottom-0 right-0 h-3 w-3 cursor-se-resize" },
];

/**
 * Floating console window that persists across page navigation.
 * Renders as a PiP preview when minimized.
 */
export function FloatingConsole() {
  const windowMode = useConsoleStore((s) => s.windowMode);
  const windowPosition = useConsoleStore((s) => s.windowPosition);
  const windowSize = useConsoleStore((s) => s.windowSize);
  const setWindowPosition = useConsoleStore((s) => s.setWindowPosition);
  const setWindowSize = useConsoleStore((s) => s.setWindowSize);
  const showConsole = useConsoleStore((s) => s.showConsole);
  const tabs = useConsoleStore((s) => s.tabs);
  const activeTabId = useConsoleStore((s) => s.activeTabId);

  const isMinimized = windowMode === "minimized";
  const isMaximized = windowMode === "maximized";

  // Drag state
  const dragRef = useRef<{ startX: number; startY: number; startPos: { x: number; y: number } } | null>(null);

  const handleDragDown = useCallback(
    (e: React.PointerEvent) => {
      if (windowMode !== "floating") return;
      if ((e.target as HTMLElement).closest("button")) return;
      e.preventDefault();
      dragRef.current = {
        startX: e.clientX,
        startY: e.clientY,
        startPos: { ...windowPosition },
      };
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    },
    [windowMode, windowPosition],
  );

  const handleDragMove = useCallback(
    (e: React.PointerEvent) => {
      if (!dragRef.current) return;
      const dx = e.clientX - dragRef.current.startX;
      const dy = e.clientY - dragRef.current.startY;
      const newX = Math.max(-windowSize.width + 100, Math.min(window.innerWidth - 100, dragRef.current.startPos.x + dx));
      const newY = Math.max(0, Math.min(window.innerHeight - 50, dragRef.current.startPos.y + dy));
      setWindowPosition({ x: newX, y: newY });
    },
    [windowSize.width, setWindowPosition],
  );

  const handleDragUp = useCallback(() => {
    dragRef.current = null;
  }, []);

  // Resize state
  const resizeRef = useRef<{
    startX: number;
    startY: number;
    startSize: { width: number; height: number };
    startPos: { x: number; y: number };
    dir: ResizeDir;
  } | null>(null);

  const handleResizeDown = useCallback(
    (dir: ResizeDir, e: React.PointerEvent) => {
      if (windowMode !== "floating") return;
      e.preventDefault();
      e.stopPropagation();
      resizeRef.current = {
        startX: e.clientX,
        startY: e.clientY,
        startSize: { ...windowSize },
        startPos: { ...windowPosition },
        dir,
      };
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    },
    [windowMode, windowSize, windowPosition],
  );

  const handleResizeMove = useCallback(
    (e: React.PointerEvent) => {
      if (!resizeRef.current) return;
      const dx = e.clientX - resizeRef.current.startX;
      const dy = e.clientY - resizeRef.current.startY;
      const { dir, startSize, startPos } = resizeRef.current;

      let newWidth = startSize.width;
      let newHeight = startSize.height;
      let newX = startPos.x;
      let newY = startPos.y;

      if (dir === "se" || dir === "ne") {
        newWidth = startSize.width + dx;
      } else {
        newWidth = startSize.width - dx;
        newX = startPos.x + dx;
      }

      if (dir === "se" || dir === "sw") {
        newHeight = startSize.height + dy;
      } else {
        newHeight = startSize.height - dy;
        newY = startPos.y + dy;
      }

      const clampedWidth = Math.max(400, newWidth);
      const clampedHeight = Math.max(300, newHeight);
      if (dir === "nw" || dir === "sw") {
        newX = newX - (clampedWidth - newWidth);
      }
      if (dir === "nw" || dir === "ne") {
        newY = newY - (clampedHeight - newHeight);
      }

      setWindowSize({ width: clampedWidth, height: clampedHeight });
      setWindowPosition({ x: newX, y: newY });
    },
    [setWindowSize, setWindowPosition],
  );

  const handleResizeUp = useCallback(() => {
    resizeRef.current = null;
  }, []);

  // Ensure window stays within viewport on browser resize
  useEffect(() => {
    function handleWindowResize() {
      const state = useConsoleStore.getState();
      if (state.windowMode !== "floating") return;
      const maxX = window.innerWidth - 100;
      const maxY = window.innerHeight - 50;
      if (state.windowPosition.x > maxX || state.windowPosition.y > maxY) {
        setWindowPosition({
          x: Math.min(state.windowPosition.x, maxX),
          y: Math.min(state.windowPosition.y, maxY),
        });
      }
    }
    window.addEventListener("resize", handleWindowResize);
    return () => { window.removeEventListener("resize", handleWindowResize); };
  }, [setWindowPosition]);

  if (windowMode === "hidden") return null;

  // --- Minimized: PiP preview ---
  if (isMinimized) {
    const activeTab = tabs.find((t) => t.id === activeTabId) ?? tabs[0];
    const pipWidth = 320;
    const pipHeight = 200;

    return createPortal(
      <div
        style={{
          position: "fixed",
          right: 16,
          bottom: 16,
          width: pipWidth,
          height: pipHeight,
          zIndex: 40,
        }}
        className="group overflow-hidden rounded-lg border border-border/30 shadow-xl transition-opacity hover:opacity-100 opacity-70"
      >
        {/* Live console content */}
        <div className="relative h-full w-full bg-[#1a1b26]/80 backdrop-blur-sm">
          {tabs.map((tab) =>
            tab.type === "vm_vnc" || tab.type === "ct_vnc" ? (
              <VNCViewer
                key={tab.id}
                tab={tab}
                visible={tab.id === activeTabId}
              />
            ) : (
              <Terminal
                key={tab.id}
                tab={tab}
                visible={tab.id === activeTabId}
              />
            ),
          )}

          {/* Top-right controls — visible on hover */}
          <div className="absolute right-2 top-2 flex gap-2 opacity-0 transition-opacity group-hover:opacity-100">
            <button
              className="flex h-7 w-7 items-center justify-center rounded-md bg-black/60 text-white/90 hover:bg-white/25 hover:text-white"
              onClick={showConsole}
              title="Restore"
            >
              <Maximize className="h-4 w-4" />
            </button>
            <button
              className="flex h-7 w-7 items-center justify-center rounded-md bg-black/60 text-white/90 hover:bg-red-500/70 hover:text-white"
              onClick={() => { useConsoleStore.getState().setWindowMode("hidden"); }}
              title="Close"
            >
              <X className="h-4 w-4" />
            </button>
          </div>

          {/* Subtle overlay label */}
          <div className="pointer-events-none absolute bottom-0 left-0 right-0 flex items-center gap-1.5 bg-gradient-to-t from-black/60 to-transparent px-2 py-1.5">
            <TerminalSquare className="h-3 w-3 text-white/70" />
            <span className="truncate text-[11px] text-white/70">
              {activeTab?.label ?? "Console"}
            </span>
            {tabs.length > 1 && (
              <span className="rounded-full bg-white/20 px-1.5 py-0.5 text-[9px] font-medium text-white/80">
                {String(tabs.length)}
              </span>
            )}
          </div>
        </div>
      </div>,
      document.body,
    );
  }

  // --- Floating or Maximized ---
  const style: React.CSSProperties = isMaximized
    ? { position: "fixed", inset: 16, zIndex: 40 }
    : {
        position: "fixed",
        left: windowPosition.x,
        top: windowPosition.y,
        width: windowSize.width,
        height: windowSize.height,
        zIndex: 40,
      };

  return createPortal(
    <div
      style={style}
      className="flex flex-col overflow-hidden rounded-lg border bg-background shadow-2xl"
    >
      <TitleBar
        onPointerDown={handleDragDown}
        onPointerMove={handleDragMove}
        onPointerUp={handleDragUp}
      />

      {/* Tab bar + Quick Connect */}
      <div className="flex items-center border-b bg-card">
        <div className="min-w-0 flex-1 overflow-x-auto">
          <ConsoleTabBar />
        </div>
        <div className="shrink-0 px-2 py-1">
          <QuickConnect />
        </div>
      </div>

      {/* Content area */}
      <div className="relative flex-1 bg-[#1a1b26]">
        {tabs.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
            <TerminalSquare className="h-12 w-12 opacity-30" />
            <p className="text-sm">No console sessions</p>
          </div>
        ) : (
          tabs.map((tab) =>
            tab.type === "vm_vnc" || tab.type === "ct_vnc" ? (
              <VNCViewer
                key={tab.id}
                tab={tab}
                visible={tab.id === activeTabId}
              />
            ) : (
              <Terminal
                key={tab.id}
                tab={tab}
                visible={tab.id === activeTabId}
              />
            ),
          )
        )}
      </div>

      {/* Resize handles on all 4 corners (floating mode only) */}
      {!isMaximized && resizeCorners.map((corner) => (
        <div
          key={corner.dir}
          className={corner.className}
          onPointerDown={(e) => { handleResizeDown(corner.dir, e); }}
          onPointerMove={handleResizeMove}
          onPointerUp={handleResizeUp}
        />
      ))}
    </div>,
    document.body,
  );
}
