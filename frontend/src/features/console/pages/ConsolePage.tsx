import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import i18n from "@/lib/i18n";
import { ExternalLink, RotateCcw, TerminalSquare, X } from "lucide-react";
import { EmptyState } from "@/components/EmptyState";
import { PageHeader } from "@/components/page/PageHeader";
import { Panel } from "@/components/page/Panel";
import { Button } from "@/components/ui/button";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { NoClustersEmptyState } from "@/features/dashboard/components/NoClustersEmptyState";
import { useConsoleStore } from "@/stores/console-store";
import { usePreferencesStore } from "@/stores/preferences-store";
import { usePermissions } from "@/hooks/usePermissions";
import { ConsoleStateDot } from "../components/ConsoleState";
import { QuickConnect } from "../components/QuickConnect";
import { CONSOLE_STATUS_TEXT, CONSOLE_TYPE_TEXT } from "../lib/console-status";
import { popOutConsoleTab } from "../lib/open-console";
import type { ConsoleTab } from "../types/console";

/** "2 sessions open, 1 connected": the page's state in a sentence. */
function sessionSummary(tabs: readonly ConsoleTab[]): string {
  const connected = tabs.filter((t) => t.status === "connected").length;
  return connected === 0
    ? i18n.t("console:consolePage.sessionsOpenNoneConnected", {
        count: tabs.length,
      })
    : i18n.t("console:consolePage.sessionsOpenConnected", {
        count: tabs.length,
        connected: String(connected),
      });
}

export function ConsolePage() {
  const { t } = useTranslation("console");
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
  // What the empty state may offer: a node's shell, a guest's console, both
  // or neither.
  const { canConsole } = usePermissions();
  const canNodeConsole = canConsole("node");
  const canGuestConsole = canConsole("vm") || canConsole("container");

  useEffect(() => {
    if (tabs.length > 0) {
      setWindowMode("maximized");
    }
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  if (!hasClusters) {
    return (
      <div className="flex h-full items-center justify-center p-6">
        <NoClustersEmptyState
          icon={TerminalSquare}
          description={t("consolePage.addAProxmoxClusterTo")}
        />
      </div>
    );
  }

  const where = opensInTab
    ? t("consolePage.consolesOpenInANewTab")
    : t("consolePage.consolesRunInTheFloatingWindow");

  if (tabs.length === 0) {
    return (
      <div className="flex min-h-full flex-col gap-5">
        <PageHeader title={t("consolePage.console")} summary={where} />
        <EmptyState
          icon={TerminalSquare}
          title={t("consolePage.noConsoleIsOpen")}
          description={
            canNodeConsole && canGuestConsole
              ? t("consolePage.openOneHereOrFrom")
              : canNodeConsole
                ? t("consolePage.openOneHereOrFromANode")
                : canGuestConsole
                  ? t("consolePage.openOneHereOrFromAGuest")
                  : t("consolePage.noConsolePermission")
          }
          action={<QuickConnect />}
        />
      </div>
    );
  }

  return (
    <div className="flex min-h-full flex-col gap-5">
      <PageHeader
        title={t("consolePage.console")}
        summary={t("consolePage.summary", {
          sessions: sessionSummary(tabs),
          where,
        })}
        actions={<QuickConnect />}
      />
      <Panel
        flush
        title={t("consolePage.openSessions")}
        aside={String(tabs.length)}
      >
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
                    {t("consolePage.typeOn", {
                      type: CONSOLE_TYPE_TEXT[tab.type],
                      where:
                        clusterName !== undefined
                          ? `${clusterName} / ${tab.node}`
                          : tab.node,
                    })}
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
                    aria-label={t("consolePage.showLabel", {
                      label: tab.label,
                    })}
                    onClick={() => {
                      setActiveTab(tab.id);
                      showConsole();
                    }}
                  >
                    {t("consolePage.show")}
                  </Button>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7 text-muted-foreground"
                    aria-label={t("consolePage.openLabelInANew", {
                      label: tab.label,
                    })}
                    title={t("consolePage.openInANewTab")}
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
                    aria-label={t("consolePage.reconnectLabel", {
                      label: tab.label,
                    })}
                    title={t("consolePage.reconnect")}
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
                    aria-label={t("consolePage.closeLabel", {
                      label: tab.label,
                    })}
                    title={t("consolePage.close")}
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
