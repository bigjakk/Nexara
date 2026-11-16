import { RotateCcw, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { useConsoleStore } from "@/stores/console-store";
import { CONSOLE_STATUS_TEXT } from "../lib/console-status";
import { ConsoleStateDot } from "./ConsoleState";
import { useTranslation } from "react-i18next";

/**
 * The floating window's sessions as tabs: a state dot, the label, and on hover
 * the two things that can be done to one. The open one carries the same
 * underline the app's page tabs do.
 */
export function ConsoleTabBar() {
  const { t } = useTranslation("console");
  const tabs = useConsoleStore((s) => s.tabs);
  const activeTabId = useConsoleStore((s) => s.activeTabId);
  const setActiveTab = useConsoleStore((s) => s.setActiveTab);
  const removeTab = useConsoleStore((s) => s.removeTab);
  const reconnectTab = useConsoleStore((s) => s.reconnectTab);

  if (tabs.length === 0) {
    return null;
  }

  return (
    <div
      role="tablist"
      aria-label={t("consoleTabBar.consoleSessions")}
      className="flex h-full"
    >
      {tabs.map((tab) => {
        const active = tab.id === activeTabId;
        return (
          <div
            key={tab.id}
            className={cn(
              "group relative flex h-full shrink-0 cursor-pointer items-center gap-2 pl-3 pr-1.5 text-[13px] outline-hidden transition-colors focus-visible:bg-foreground/[0.07]",
              active
                ? "font-medium text-foreground after:absolute after:inset-x-2 after:bottom-0 after:h-0.5 after:rounded-full after:bg-primary"
                : "text-muted-foreground hover:text-foreground",
            )}
            onClick={() => {
              setActiveTab(tab.id);
            }}
            // The bar this sits in maximizes the window on a double-click.
            onDoubleClick={(e) => {
              e.stopPropagation();
            }}
            role="tab"
            aria-selected={active}
            title={`${tab.label}: ${CONSOLE_STATUS_TEXT[tab.status]}`}
            tabIndex={0}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                setActiveTab(tab.id);
              }
            }}
          >
            <ConsoleStateDot status={tab.status} />
            <span className="max-w-[160px] truncate">{tab.label}</span>
            <span className="flex items-center opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100 pointer-coarse:opacity-100">
              <button
                className="grid h-5 w-5 place-items-center rounded text-muted-foreground outline-hidden hover:bg-foreground/10 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                onClick={(e) => {
                  e.stopPropagation();
                  reconnectTab(tab.id);
                }}
                aria-label={t("consoleTabBar.reconnectLabel", {
                  label: tab.label,
                })}
                title={t("consoleTabBar.reconnect")}
              >
                <RotateCcw className="h-3 w-3" />
              </button>
              <button
                className="grid h-5 w-5 place-items-center rounded text-muted-foreground outline-hidden hover:bg-foreground/10 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                onClick={(e) => {
                  e.stopPropagation();
                  removeTab(tab.id);
                }}
                aria-label={t("consoleTabBar.closeLabel", { label: tab.label })}
                title={t("consoleTabBar.close")}
              >
                <X className="h-3 w-3" />
              </button>
            </span>
          </div>
        );
      })}
    </div>
  );
}
