import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { ExternalLink, RotateCcw, TerminalSquare, X } from "lucide-react";
import { EmptyState } from "@/components/EmptyState";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { Button } from "@/components/ui/button";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { useConsoleStore } from "@/stores/console-store";
import { usePreferencesStore } from "@/stores/preferences-store";
import { ConsoleStateDot } from "../components/ConsoleState";
import { QuickConnect } from "../components/QuickConnect";
import { CONSOLE_STATUS_TEXT, CONSOLE_TYPE_TEXT } from "../lib/console-status";
import { popOutConsoleTab } from "../lib/open-console";
import type { ConsoleTab } from "../types/console";

/** "2 sessions open, 1 connected": the page's state in a sentence. */
function sessionSummary(tabs: readonly ConsoleTab[]): string {
  const connected = tabs.filter((t) => t.status === "connected").length;
  const open = `${String(tabs.length)} ${tabs.length === 1 ? "session" : "sessions"} open`;
  return `${open}, ${connected === 0 ? "none" : String(connected)} connected`;
}

export function ConsolePage() {
  const { t: td } = useTranslation("dashboard");
  const setWindowMode = useConsoleStore((s) => s.setWindowMode);
  const showConsole = useConsoleStore((s) => s.showConsole);
  const setActiveTab = useConsoleStore((s) => s.setActiveTab);
  const reconnectTab = useConsoleStore((s) => s.reconnectTab);
  const removeTab = useConsoleStore((s) => s.removeTab);
  const tabs = useConsoleStore((s) => s.tabs);
  const opensInTab = usePreferencesStore(
    (s) => s.preferences.consoleOpenMode === "tab",
  );
  const { data: clusters } = useClusters();
  const hasClusters = (clusters?.length ?? 0) > 0;

  useEffect(() => {
    if (tabs.length > 0) {
      setWindowMode("maximized");
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  if (!hasClusters) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <EmptyState
          icon={TerminalSquare}
          title={td("noClustersRegistered")}
          description="Add a Proxmox cluster to open VNC or serial consoles into your VMs and containers."
          action={<AddClusterDialog />}
        />
      </div>
    );
  }

  const where = opensInTab
    ? "Consoles open in a new browser tab."
    : "Consoles run in the floating window, which stays open as you move between pages.";

  if (tabs.length === 0) {
    return (
      <div className="flex min-h-full flex-col gap-5">
        <PageHeader title="Console" summary={where} />
        <EmptyState
          icon={TerminalSquare}
          title="No console is open"
          description="Open one here, or from a guest's or a node's own page."
          action={<QuickConnect />}
        />
      </div>
    );
  }

  return (
    <div className="flex min-h-full flex-col gap-5">
      <PageHeader
        title="Console"
        summary={`${sessionSummary(tabs)}. ${where}`}
        actions={<QuickConnect />}
      />
      <Panel flush title="Open sessions" aside={String(tabs.length)}>
        <ul className="divide-y border-t">
          {tabs.map((tab) => {
            const clusterName = clusters?.find(
              (c) => c.id === tab.clusterID,
            )?.name;
            return (
              <li
                key={tab.id}
                className="flex flex-wrap items-center gap-x-4 gap-y-2 px-[18px] py-2.5"
              >
                <ConsoleStateDot status={tab.status} />
                <div className="min-w-0 flex-[1_1_14rem]">
                  <p className="truncate text-sm font-medium">{tab.label}</p>
                  <p className="truncate text-[12.5px] text-muted-foreground">
                    {CONSOLE_TYPE_TEXT[tab.type]} on{" "}
                    {clusterName !== undefined
                      ? `${clusterName} / ${tab.node}`
                      : tab.node}
                  </p>
                </div>
                <span className="w-28 shrink-0 text-[12.5px] text-muted-foreground">
                  {CONSOLE_STATUS_TEXT[tab.status]}
                </span>
                <div className="flex shrink-0 items-center gap-1">
                  <Button
                    variant="outline"
                    size="sm"
                    className="h-7"
                    aria-label={`Show ${tab.label}`}
                    onClick={() => {
                      setActiveTab(tab.id);
                      showConsole();
                    }}
                  >
                    Show
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 text-muted-foreground"
                    aria-label={`Open ${tab.label} in a new tab`}
                    title="Open in a new tab"
                    onClick={(e) => {
                      // Once per click sequence: the row goes with its tab.
                      if (e.detail > 1) return;
                      popOutConsoleTab(tab.id);
                    }}
                  >
                    <ExternalLink className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 text-muted-foreground"
                    aria-label={`Reconnect ${tab.label}`}
                    title="Reconnect"
                    onClick={() => {
                      reconnectTab(tab.id);
                    }}
                  >
                    <RotateCcw className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 text-muted-foreground"
                    aria-label={`Close ${tab.label}`}
                    title="Close"
                    onClick={() => {
                      removeTab(tab.id);
                    }}
                  >
                    <X className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </li>
            );
          })}
        </ul>
      </Panel>
    </div>
  );
}
