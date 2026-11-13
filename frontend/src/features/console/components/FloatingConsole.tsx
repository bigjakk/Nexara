import { useCallback, useEffect, useRef } from "react";
import { createPortal } from "react-dom";
import {
  Minus,
  X,
  Maximize2,
  Minimize2,
  Maximize,
  TerminalSquare,
  ExternalLink,
} from "lucide-react";
import { useConsoleStore } from "@/stores/console-store";
import { useIsMobile } from "@/hooks/useIsMobile";
import { cn } from "@/lib/utils";
import { popOutConsoleTab } from "../lib/open-console";
import { ConsoleTabBar } from "./ConsoleTabBar";
import { QuickConnect } from "./QuickConnect";
import { VNCViewer } from "./VNCViewer";
import { Terminal } from "./Terminal";

/** A window control in the bar: touch-sized on a phone. */
function WindowButton({
  title,
  danger = false,
  onClick,
  children,
}: {
  title: string;
  danger?: boolean;
  onClick: (e: React.MouseEvent) => void;
  children: React.ReactNode;
}) {
  const isMobile = useIsMobile();
  return (
    <button
      className={cn(
        "grid shrink-0 place-items-center rounded-md text-muted-foreground outline-hidden transition-colors focus-visible:ring-2 focus-visible:ring-ring",
        danger
          ? "hover:bg-destructive/15 hover:text-destructive"
          : "hover:bg-foreground/[0.08] hover:text-foreground",
        isMobile ? "h-8 w-8" : "h-7 w-7",
      )}
      onClick={(e) => {
        e.stopPropagation();
        onClick(e);
      }}
      // Nor does a double-click maximize the window under it.
      onDoubleClick={(e) => {
        e.stopPropagation();
      }}
      title={title}
    >
      {children}
    </button>
  );
}

/**
 * The window's one bar: its sessions as tabs, a way to open another, and the
 * window controls. The space between is what the window is dragged by.
 */
function TitleBar({
  onPointerDown,
  onPointerMove,
  onPointerUp,
  onPopOut,
  hasTabs,
}: {
  onPointerDown: (e: React.PointerEvent) => void;
  onPointerMove: (e: React.PointerEvent) => void;
  onPointerUp: (e: React.PointerEvent) => void;
  /** Moves the active session to a browser tab; absent with none open. */
  onPopOut: (() => void) | undefined;
  hasTabs: boolean;
}) {
  const windowMode = useConsoleStore((s) => s.windowMode);
  const setWindowMode = useConsoleStore((s) => s.setWindowMode);
  const isMobile = useIsMobile();

  function handleDoubleClick(e: React.MouseEvent) {
    // React events bubble through portals: a double-click inside the New
    // console dialog is not one on this bar.
    if (!e.currentTarget.contains(e.target as Node)) return;
    setWindowMode(windowMode === "maximized" ? "floating" : "maximized");
  }

  return (
    <div
      className={cn(
        "flex shrink-0 select-none items-stretch border-b bg-card",
        isMobile ? "h-10" : "h-9 cursor-grab active:cursor-grabbing",
      )}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onDoubleClick={handleDoubleClick}
    >
      <div className="min-w-0 overflow-x-auto overflow-y-hidden">
        <ConsoleTabBar />
      </div>
      {!hasTabs && (
        <span className="flex items-center pl-3 text-[13px] font-medium">
          Console
        </span>
      )}
      {/* What is left of the bar: the handle the window is dragged by. */}
      <div className="min-w-6 flex-1" />

      <div className="flex shrink-0 items-center gap-0.5 px-1.5">
        <QuickConnect appearance="compact" />
        <span aria-hidden="true" className="mx-1 h-4 w-px bg-border" />
        {/* Window controls. Maximize is meaningless on a phone, where the
            console is always a full-screen takeover. */}
        {onPopOut && (
          <WindowButton
            title="Open in new tab"
            onClick={(e) => {
              // Once per click sequence: the next tab's button takes its place.
              if (e.detail > 1) return;
              onPopOut();
            }}
          >
            <ExternalLink className="h-3.5 w-3.5" />
          </WindowButton>
        )}
        <WindowButton
          title="Minimize"
          onClick={() => {
            setWindowMode("minimized");
          }}
        >
          <Minus className="h-3.5 w-3.5" />
        </WindowButton>
        {!isMobile && (
          <WindowButton
            title={windowMode === "maximized" ? "Restore" : "Maximize"}
            onClick={() => {
              setWindowMode(
                windowMode === "maximized" ? "floating" : "maximized",
              );
            }}
          >
            {windowMode === "maximized" ? (
              <Minimize2 className="h-3.5 w-3.5" />
            ) : (
              <Maximize2 className="h-3.5 w-3.5" />
            )}
          </WindowButton>
        )}
        <WindowButton
          title="Close"
          danger
          onClick={() => {
            setWindowMode("hidden");
          }}
        >
          <X className="h-3.5 w-3.5" />
        </WindowButton>
      </div>
    </div>
  );
}

type ResizeDir = "nw" | "ne" | "sw" | "se";

const resizeCorners: { dir: ResizeDir; className: string }[] = [
  { dir: "nw", className: "absolute left-0 top-0 h-3 w-3 cursor-nw-resize" },
  { dir: "ne", className: "absolute right-0 top-0 h-3 w-3 cursor-ne-resize" },
  { dir: "sw", className: "absolute bottom-0 left-0 h-3 w-3 cursor-sw-resize" },
  {
    dir: "se",
    className: "absolute bottom-0 right-0 h-3 w-3 cursor-se-resize",
  },
];

/**
 * Floating console window that persists across page navigation.
 * Renders as a PiP preview when minimized.
 */
export function FloatingConsole() {
  const isMobile = useIsMobile();
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
  const dragRef = useRef<{
    startX: number;
    startY: number;
    startPos: { x: number; y: number };
  } | null>(null);

  const handleDragDown = useCallback(
    (e: React.PointerEvent) => {
      if (isMobile || windowMode !== "floating") return;
      // React events bubble through portals: a press inside the New console
      // dialog reaches this handler without being on the bar at all.
      if (!e.currentTarget.contains(e.target as Node)) return;
      // A press on a control or a session's tab is that control's.
      if ((e.target as HTMLElement).closest("button, [role=tab]")) return;
      e.preventDefault();
      dragRef.current = {
        startX: e.clientX,
        startY: e.clientY,
        startPos: { ...windowPosition },
      };
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    },
    [isMobile, windowMode, windowPosition],
  );

  const handleDragMove = useCallback(
    (e: React.PointerEvent) => {
      if (!dragRef.current) return;
      const dx = e.clientX - dragRef.current.startX;
      const dy = e.clientY - dragRef.current.startY;
      const newX = Math.max(
        -windowSize.width + 100,
        Math.min(window.innerWidth - 100, dragRef.current.startPos.x + dx),
      );
      const newY = Math.max(
        0,
        Math.min(window.innerHeight - 50, dragRef.current.startPos.y + dy),
      );
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
    [windowSize, windowPosition],
  );

  const handleResizeMove = useCallback(
    (e: React.PointerEvent) => {
      if (!resizeRef.current) return;
      const dx = e.clientX - resizeRef.current.startX;
      const dy = e.clientY - resizeRef.current.startY;
      const { dir, startSize, startPos } = resizeRef.current;

      // newWidth/newHeight are assigned in both branches of each if below,
      // so no initializer (would be a dead store; flagged by no-useless-assignment).
      let newWidth: number;
      let newHeight: number;
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
    return () => {
      window.removeEventListener("resize", handleWindowResize);
    };
  }, [setWindowPosition]);

  if (windowMode === "hidden") return null;

  const activeTab = tabs.find((t) => t.id === activeTabId) ?? tabs[0];
  const activeLabel = activeTab?.label ?? "Console";
  const popOut = activeTab
    ? () => {
        popOutConsoleTab(activeTab.id);
      }
    : undefined;

  // Every window mode renders ONE tree, with the console content always in
  // the same slot. Minimize used to return a separate picture-in-picture
  // tree; because React reconciles children by position, that swapped a
  // <TitleBar> in where a <div> had been and unmounted the whole subtree —
  // tearing down the live RFB/WebSocket and re-dialling Proxmox (re-minting
  // and re-auditing a console token) on every minimize *and* every restore,
  // while the outgoing connection's late disconnect event stamped
  // "disconnected" over its replacement. Floating and maximized already
  // shared a tree, which is exactly why maximizing never dropped a session.
  // Keep the slots stable: per-mode branches belong in a className or inside
  // a slot, never wrapped around one.
  const style: React.CSSProperties = isMinimized
    ? {
        position: "fixed",
        right: 16,
        bottom: 16,
        width: isMobile ? Math.min(320, window.innerWidth - 32) : 320,
        height: 200,
        zIndex: 40,
      }
    : isMobile
      ? // Mobile is always a full-screen takeover: a draggable/resizable
        // window is a precision-pointer UX, and at phone widths it would
        // cover the app anyway. The stored floating position/size is desktop
        // only.
        { position: "fixed", inset: 0, zIndex: 50 }
      : isMaximized
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
      className={cn(
        "flex flex-col overflow-hidden",
        isMinimized
          ? // `group` only here: it reveals the PiP's own controls below.
            // ConsoleTabBar marks each tab `group` and reveals that tab's
            // close/reconnect buttons with group-hover, and group-hover
            // matches ANY .group ancestor — so putting it on the window root
            // unconditionally would pop every tab's buttons open at once
            // whenever the pointer was anywhere inside the console.
            "group rounded-[10px] opacity-70 shadow-xl transition-opacity hover:opacity-100"
          : "bg-card shadow-2xl",
        !isMinimized && !isMobile && "rounded-[10px] ring-1 ring-border",
      )}
    >
      {/* The one bar: tabs, New console, the window controls. */}
      {!isMinimized && (
        <TitleBar
          onPointerDown={handleDragDown}
          onPointerMove={handleDragMove}
          onPointerUp={handleDragUp}
          onPopOut={popOut}
          hasTabs={tabs.length > 0}
        />
      )}

      {/* Content area — the one slot every window mode shares. min-h-0 so
          flex-1 can actually shrink: a flex item's min-height defaults to
          auto, so without it a tall console (an xterm's intrinsic rows)
          pushes this past the window and overflows the 200px PiP. */}
      <div
        className={cn(
          "relative min-h-0 flex-1",
          isMinimized ? "bg-[#1a1b26]/80 backdrop-blur-xs" : "bg-[#1a1b26]",
        )}
      >
        {tabs.length === 0 ? (
          <div className="flex h-full items-center justify-center text-sm text-neutral-400">
            No console sessions
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

        {/* Minimized chrome: the PiP overlay rides on top of the same live
            content rather than replacing it. */}
        {isMinimized && (
          <>
            {/* Top-right controls — hover-revealed on desktop, always visible
                on touch (there is no hover to reveal them with) */}
            <div
              className={cn(
                "absolute right-2 top-2 flex gap-2",
                isMobile
                  ? "opacity-100"
                  : "opacity-0 transition-opacity group-hover:opacity-100",
              )}
            >
              {popOut && (
                <button
                  className="flex h-7 w-7 items-center justify-center rounded-md bg-black/60 text-white/90 hover:bg-white/25 hover:text-white"
                  onClick={(e) => {
                    if (e.detail > 1) return;
                    popOut();
                  }}
                  title="Open in new tab"
                >
                  <ExternalLink className="h-4 w-4" />
                </button>
              )}
              <button
                className="flex h-7 w-7 items-center justify-center rounded-md bg-black/60 text-white/90 hover:bg-white/25 hover:text-white"
                onClick={showConsole}
                title="Restore"
              >
                <Maximize className="h-4 w-4" />
              </button>
              <button
                className="flex h-7 w-7 items-center justify-center rounded-md bg-black/60 text-white/90 hover:bg-red-500/70 hover:text-white"
                onClick={() => {
                  useConsoleStore.getState().setWindowMode("hidden");
                }}
                title="Close"
              >
                <X className="h-4 w-4" />
              </button>
            </div>

            {/* Subtle overlay label */}
            <div className="pointer-events-none absolute bottom-0 left-0 right-0 flex items-center gap-1.5 bg-linear-to-t from-black/60 to-transparent px-2 py-1.5">
              <TerminalSquare className="h-3 w-3 text-white/70" />
              <span className="truncate text-[11px] text-white/70">
                {activeLabel}
              </span>
              {tabs.length > 1 && (
                <span className="rounded-full bg-white/20 px-1.5 py-0.5 text-[9px] font-medium text-white/80">
                  {String(tabs.length)}
                </span>
              )}
            </div>
          </>
        )}
      </div>

      {/* Resize handles on all 4 corners (desktop floating mode only) */}
      {!isMinimized &&
        !isMobile &&
        !isMaximized &&
        resizeCorners.map((corner) => (
          <div
            key={corner.dir}
            className={corner.className}
            onPointerDown={(e) => {
              handleResizeDown(corner.dir, e);
            }}
            onPointerMove={handleResizeMove}
            onPointerUp={handleResizeUp}
          />
        ))}
    </div>,
    document.body,
  );
}
