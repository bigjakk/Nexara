import { TerminalSquare } from "lucide-react";
import { useConsoleStore } from "@/stores/console-store";
import { ConsoleTabBar } from "./ConsoleTabBar";
import { QuickConnect } from "./QuickConnect";
import { Terminal } from "./Terminal";
import { VNCViewer } from "./VNCViewer";

export function ConsolePanel() {
  const tabs = useConsoleStore((s) => s.tabs);
  const activeTabId = useConsoleStore((s) => s.activeTabId);

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center border-b bg-card">
        <div className="flex-1">
          <ConsoleTabBar />
        </div>
        <div className="px-2 py-1">
          <QuickConnect />
        </div>
      </div>
      <div className="relative flex-1 bg-[#1a1b26]">
        {tabs.length === 0 ? (
          <div className="flex h-full flex-col items-center justify-center gap-4 text-muted-foreground">
            <TerminalSquare className="h-16 w-16 opacity-30" />
            <div className="text-center">
              <p className="text-lg font-medium">No console sessions</p>
              <p className="text-sm">
                Click &quot;New Console&quot; to connect to a VM or container,
                or open one from the Inventory page
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
