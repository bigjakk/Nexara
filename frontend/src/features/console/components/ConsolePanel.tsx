import { TerminalSquare } from "lucide-react";
import { useConsoleStore } from "@/stores/console-store";
import { ConsoleTabBar } from "./ConsoleTabBar";
import { Terminal } from "./Terminal";
import { VNCViewer } from "./VNCViewer";

export function ConsolePanel() {
  const tabs = useConsoleStore((s) => s.tabs);
  const activeTabId = useConsoleStore((s) => s.activeTabId);

  return (
    <div className="flex h-full flex-col">
      <ConsoleTabBar />
      <div className="relative flex-1 bg-[#1a1b26]">
        {tabs.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-4 text-muted-foreground">
            <TerminalSquare className="h-16 w-16 opacity-30" />
            <div className="text-center">
              <p className="text-lg font-medium">No console sessions</p>
              <p className="text-sm">
                Open a terminal or VNC console from the Inventory or Cluster
                detail page
              </p>
            </div>
          </div>
        ) : (
          tabs.map((tab) =>
            tab.type === "vm_vnc" ? (
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
    </div>
  );
}
