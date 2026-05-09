import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { TerminalSquare } from "lucide-react";
import { EmptyState } from "@/components/EmptyState";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { useConsoleStore } from "@/stores/console-store";
import { QuickConnect } from "../components/QuickConnect";

export function ConsolePage() {
  const { t: td } = useTranslation("dashboard");
  const setWindowMode = useConsoleStore((s) => s.setWindowMode);
  const tabs = useConsoleStore((s) => s.tabs);
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

  return (
    <div className="flex h-full items-center justify-center text-muted-foreground">
      <div className="space-y-2 text-center">
        <TerminalSquare className="mx-auto h-12 w-12 opacity-30" />
        <p>Console sessions run in the floating window.</p>
        {tabs.length === 0 && (
          <div className="flex justify-center pt-2">
            <QuickConnect />
          </div>
        )}
      </div>
    </div>
  );
}
