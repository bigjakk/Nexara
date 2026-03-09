import { X, Terminal, Loader2, AlertCircle, Unplug, RotateCcw } from "lucide-react";
import { cn } from "@/lib/utils";
import { useConsoleStore } from "@/stores/console-store";
import type { ConsoleStatus } from "../types/console";

function StatusIcon({ status }: { status: ConsoleStatus }) {
  switch (status) {
    case "connecting":
      return <Loader2 className="h-3 w-3 animate-spin text-yellow-500" />;
    case "connected":
      return <Terminal className="h-3 w-3 text-green-500" />;
    case "reconnecting":
      return <RotateCcw className="h-3 w-3 animate-spin text-yellow-500" />;
    case "disconnected":
      return <Unplug className="h-3 w-3 text-muted-foreground" />;
    case "error":
      return <AlertCircle className="h-3 w-3 text-destructive" />;
  }
}

export function ConsoleTabBar() {
  const tabs = useConsoleStore((s) => s.tabs);
  const activeTabId = useConsoleStore((s) => s.activeTabId);
  const setActiveTab = useConsoleStore((s) => s.setActiveTab);
  const removeTab = useConsoleStore((s) => s.removeTab);
  const reconnectTab = useConsoleStore((s) => s.reconnectTab);

  if (tabs.length === 0) {
    return null;
  }

  return (
    <div className="flex items-center gap-1 px-2 pt-1">
      {tabs.map((tab) => (
        <div
          key={tab.id}
          className={cn(
            "group flex items-center gap-2 rounded-t-md border border-b-0 px-3 py-1.5 text-sm cursor-pointer transition-colors",
            tab.id === activeTabId
              ? "bg-background text-foreground"
              : "bg-muted/50 text-muted-foreground hover:bg-muted",
          )}
          onClick={() => { setActiveTab(tab.id); }}
          role="tab"
          aria-selected={tab.id === activeTabId}
          tabIndex={0}
          onKeyDown={(e) => {
            if (e.key === "Enter" || e.key === " ") {
              setActiveTab(tab.id);
            }
          }}
        >
          <StatusIcon status={tab.status} />
          <span className="max-w-[150px] truncate">{tab.label}</span>
          <button
            className="ml-1 rounded p-0.5 opacity-0 transition-opacity hover:bg-muted group-hover:opacity-100"
            onClick={(e) => {
              e.stopPropagation();
              reconnectTab(tab.id);
            }}
            aria-label={`Reconnect ${tab.label}`}
            title="Reconnect"
          >
            <RotateCcw className="h-3 w-3" />
          </button>
          <button
            className="rounded p-0.5 opacity-0 transition-opacity hover:bg-muted group-hover:opacity-100"
            onClick={(e) => {
              e.stopPropagation();
              removeTab(tab.id);
            }}
            aria-label={`Close ${tab.label}`}
            title="Close"
          >
            <X className="h-3 w-3" />
          </button>
        </div>
      ))}
    </div>
  );
}
