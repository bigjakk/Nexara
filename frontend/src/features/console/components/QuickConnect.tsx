import { useState, useMemo } from "react";
import { Monitor, TerminalSquare, Plus } from "lucide-react";
import { useQueries } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { apiClient } from "@/lib/api-client";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useConsoleStore } from "@/stores/console-store";
import type { VMResponse, NodeResponse } from "@/types/api";

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

export function QuickConnect() {
  const [open, setOpen] = useState(false);
  const addTab = useConsoleStore((s) => s.addTab);
  const showConsole = useConsoleStore((s) => s.showConsole);

  const { data: clusters } = useClusters();
  const clusterIds = clusters?.map((c) => c.id) ?? [];

  const nodeQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "nodes"],
      queryFn: () =>
        apiClient.get<NodeResponse[]>(
          `/api/v1/clusters/${cluster.id}/nodes`,
        ),
      enabled: open,
    })),
  });

  const vmQueries = useQueries({
    queries: (clusters ?? []).map((cluster) => ({
      queryKey: ["clusters", cluster.id, "vms"],
      queryFn: () =>
        apiClient.get<VMResponse[]>(`/api/v1/clusters/${cluster.id}/vms`),
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
  }, [clusters, nodeQueries, vmQueries]);

  function openConsole(vm: VMEntry, consoleType: "vnc" | "serial" | "attach") {
    const type =
      consoleType === "vnc"
        ? "vm_vnc" as const
        : consoleType === "attach"
          ? "ct_vnc" as const
          : "vm_serial" as const;

    const labelPrefix =
      consoleType === "vnc"
        ? "VNC"
        : consoleType === "attach"
          ? "CT"
          : "Serial";

    addTab({
      clusterID: vm.clusterId,
      node: vm.nodeName,
      vmid: vm.vmid,
      type,
      label: `${labelPrefix}: ${vm.name}`,
      resourceId: vm.resourceId,
      kind: vm.type === "qemu" ? "vm" : "ct",
    });
    showConsole();
    setOpen(false);
  }

  return (
    <>
      <Button
        variant="outline"
        size="sm"
        className="gap-1.5"
        onClick={() => { setOpen(true); }}
      >
        <Plus className="h-4 w-4" />
        New Console
      </Button>

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-lg">
          <DialogHeader>
            <DialogTitle>Open Console</DialogTitle>
          </DialogHeader>

          <div className="space-y-2">
            <Label>Select a VM or Container</Label>
            <div className="max-h-80 overflow-y-auto rounded-md border">
              {vmList.length === 0 ? (
                <p className="p-4 text-center text-sm text-muted-foreground">
                  No VMs or containers found
                </p>
              ) : (
                vmList.map((vm) => {
                  const isRunning = vm.status === "running";
                  const isVM = vm.type === "qemu";

                  return (
                    <div
                      key={`${vm.clusterId}-${String(vm.vmid)}`}
                      className="flex items-center justify-between border-b px-3 py-2 last:border-b-0"
                    >
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span
                            className={`inline-block h-2 w-2 rounded-full ${isRunning ? "bg-green-500" : vm.status === "suspended" ? "bg-yellow-500" : "bg-gray-400"}`}
                          />
                          <span className="truncate text-sm font-medium">
                            {vm.name}
                          </span>
                          <span className="text-xs text-muted-foreground">
                            {String(vm.vmid)}
                          </span>
                        </div>
                        {clusterIds.length > 1 && (
                          <span className="text-xs text-muted-foreground">
                            {vm.clusterName} / {vm.nodeName}
                          </span>
                        )}
                      </div>

                      <div className="flex items-center gap-1">
                        {isVM ? (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 gap-1 px-2 text-xs"
                            disabled={!isRunning}
                            title="VNC Console"
                            onClick={() => { openConsole(vm, "vnc"); }}
                          >
                            <Monitor className="h-3.5 w-3.5" />
                            VNC
                          </Button>
                        ) : (
                          <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 gap-1 px-2 text-xs"
                            disabled={!isRunning}
                            title="Container Attach"
                            onClick={() => { openConsole(vm, "attach"); }}
                          >
                            <TerminalSquare className="h-3.5 w-3.5" />
                            Attach
                          </Button>
                        )}
                      </div>
                    </div>
                  );
                })
              )}
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
