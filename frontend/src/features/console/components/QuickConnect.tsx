import { useState, useMemo } from "react";
import { Monitor, TerminalSquare, Plus } from "lucide-react";
import { useQueries } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { StatusIcon } from "@/components/StatusIcon";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { usePermissions } from "@/hooks/usePermissions";
import type { VMResponse, NodeResponse } from "@/types/api";
import type { ConsoleClickKeys } from "../lib/console-session";
import { openConsole } from "../lib/open-console";

interface VMEntry {
  clusterId: string;
  clusterName: string;
  nodeName: string;
  vmid: number;
  name: string;
  type: string;
  status: string;
  resourceId: string;
}

export function QuickConnect({
  appearance = "primary",
}: {
  /**
   * `primary` is the Console page's one button; `compact` is the plus in the
   * floating window's bar, where a labelled button would crowd the tabs.
   */
  appearance?: "primary" | "compact";
}) {
  const [open, setOpen] = useState(false);
  const { canConsole } = usePermissions();

  const { data: clusters } = useClusters();
  const clusterIds = clusters?.map((c) => c.id) ?? [];

  const nodeQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.list<NodeResponse>(
          apiPath`/api/v1/clusters/${cluster.id}/nodes`,
        ),
      enabled: open,
    })),
  });

  const vmQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.list<VMResponse>(apiPath`/api/v1/clusters/${cluster.id}/vms`),
      enabled: open,
    })),
  });

  const vmList = useMemo(() => {
    if (!clusters) return [];
    const entries: VMEntry[] = [];

    for (let i = 0; i < clusters.length; i++) {
      const cluster = clusters[i];
      if (!cluster) continue;
      const nodes = nodeQueries[i]?.data ?? [];
      const vms = vmQueries[i]?.data ?? [];

      const nodeMap = new Map<string, string>();
      for (const node of nodes) {
        nodeMap.set(node.id, node.name);
      }

      for (const vm of vms) {
        if (vm.template) continue;
        // Only list guests the user can actually console — the mint 403s
        // without the console:* permission for the guest's kind.
        if (!canConsole(vm.type === "qemu" ? "vm" : "container")) continue;
        entries.push({
          clusterId: cluster.id,
          clusterName: cluster.name,
          nodeName: nodeMap.get(vm.node_id) ?? "",
          vmid: vm.vmid,
          name: vm.name,
          type: vm.type,
          status: vm.status.toLowerCase(),
          resourceId: vm.id,
        });
      }
    }

    return entries.sort((a, b) => a.vmid - b.vmid);
  }, [clusters, nodeQueries, vmQueries, canConsole]);

  function openGuest(
    vm: VMEntry,
    consoleType: "vnc" | "serial" | "attach",
    keys: ConsoleClickKeys,
  ) {
    const type =
      consoleType === "vnc"
        ? ("vm_vnc" as const)
        : consoleType === "attach"
          ? ("ct_vnc" as const)
          : ("vm_serial" as const);

    openConsole(
      {
        clusterID: vm.clusterId,
        node: vm.nodeName,
        vmid: vm.vmid,
        type,
        resourceId: vm.resourceId,
        kind: vm.type === "qemu" ? "vm" : "ct",
        guestName: vm.name,
      },
      keys,
    );
    setOpen(false);
  }

  return (
    <>
      {appearance === "compact" ? (
        <Button
          variant="ghost"
          size="icon"
          className="h-7 w-7 text-muted-foreground"
          aria-label="New console"
          title="New console"
          onClick={() => {
            setOpen(true);
          }}
        >
          <Plus className="h-4 w-4" />
        </Button>
      ) : (
        <Button
          size="sm"
          className="gap-1.5"
          onClick={() => {
            setOpen(true);
          }}
        >
          <Plus className="h-4 w-4" />
          New console
        </Button>
      )}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-lg gap-3">
          <DialogHeader>
            <DialogTitle>Open a console</DialogTitle>
            <DialogDescription>
              Choose a running guest to open its console.
            </DialogDescription>
          </DialogHeader>

          {vmList.length === 0 ? (
            <p className="py-6 text-center text-sm text-muted-foreground">
              No VMs or containers found
            </p>
          ) : (
            <ul className="-mx-6 max-h-80 divide-y overflow-y-auto border-y">
              {vmList.map((vm) => {
                const isRunning = vm.status === "running";
                const isVM = vm.type === "qemu";

                return (
                  <li
                    key={`${vm.clusterId}-${String(vm.vmid)}`}
                    className="flex items-center gap-3 py-1.5 pl-6 pr-4"
                  >
                    <StatusIcon status={vm.status} />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-baseline gap-2">
                        <span className="truncate text-sm font-medium">
                          {vm.name}
                        </span>
                        <span className="shrink-0 text-[12.5px] tabular-nums text-muted-foreground">
                          {String(vm.vmid)}
                        </span>
                      </div>
                      {clusterIds.length > 1 && (
                        <span className="block truncate text-[12.5px] text-muted-foreground">
                          {vm.clusterName} / {vm.nodeName}
                        </span>
                      )}
                    </div>

                    {isVM ? (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 shrink-0 gap-1.5 px-2 text-[12.5px]"
                        disabled={!isRunning}
                        title="VNC Console"
                        onClick={(e) => {
                          openGuest(vm, "vnc", e);
                        }}
                      >
                        <Monitor className="h-3.5 w-3.5" />
                        VNC
                      </Button>
                    ) : (
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 shrink-0 gap-1.5 px-2 text-[12.5px]"
                        disabled={!isRunning}
                        title="Container Attach"
                        onClick={(e) => {
                          openGuest(vm, "attach", e);
                        }}
                      >
                        <TerminalSquare className="h-3.5 w-3.5" />
                        Attach
                      </Button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
