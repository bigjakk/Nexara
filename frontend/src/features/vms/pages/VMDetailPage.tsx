import { useState } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import { ArrowLeft, Monitor, Terminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/features/inventory/components/StatusBadge";
import { MetricMiniBar } from "@/features/inventory/components/MetricMiniBar";
import { useMetricStore } from "@/stores/metric-store";
import { useConsoleStore } from "@/stores/console-store";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useVM } from "../api/vm-queries";
import { VMActions } from "../components/VMActions";
import { CloneDialog } from "../components/CloneDialog";
import { MigrateDialog } from "../components/MigrateDialog";
import { DestroyDialog } from "../components/DestroyDialog";
import type { ResourceKind } from "../types/vm";
import type { ResourceStatus } from "@/features/inventory/types/inventory";

function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(1024));
  const val = bytes / Math.pow(1024, i);
  return `${val.toFixed(val >= 100 ? 0 : 1)} ${units[i] ?? ""}`;
}

function formatUptime(seconds: number): string {
  if (seconds <= 0) return "--";
  const days = Math.floor(seconds / 86400);
  const hours = Math.floor((seconds % 86400) / 3600);
  if (days > 0) return `${String(days)}d ${String(hours)}h`;
  const mins = Math.floor((seconds % 3600) / 60);
  return hours > 0 ? `${String(hours)}h ${String(mins)}m` : `${String(mins)}m`;
}

export function VMDetailPage() {
  const { clusterId = "", vmId = "", kind: rawKind } = useParams<{
    clusterId: string;
    vmId: string;
    kind: string;
  }>();
  const navigate = useNavigate();

  const kind: ResourceKind = rawKind === "ct" ? "ct" : "vm";
  const { data: vm, isLoading, error } = useVM(clusterId, vmId, kind);

  const metricsMap = useMetricStore((s) => s.metrics);
  const clusterMetrics = metricsMap.get(clusterId);
  const liveMetric = clusterMetrics?.vmMetrics.get(vmId);

  const { data: nodes } = useClusterNodes(clusterId);
  const addTab = useConsoleStore((s) => s.addTab);

  // Resolve node name from node_id
  const nodeName = nodes?.find((n) => n.id === vm?.node_id)?.name ?? "";

  const [cloneOpen, setCloneOpen] = useState(false);
  const [migrateOpen, setMigrateOpen] = useState(false);
  const [destroyOpen, setDestroyOpen] = useState(false);

  if (isLoading) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }

  if (error || !vm) {
    return (
      <div className="p-6">
        <p className="text-destructive">
          {error?.message ?? "Resource not found"}
        </p>
        <Button variant="link" asChild className="mt-2 px-0">
          <Link to="/inventory">Back to Inventory</Link>
        </Button>
      </div>
    );
  }

  const normalizedStatus = vm.status.toLowerCase() as ResourceStatus;

  function openConsole(type: "terminal" | "vnc") {
    if (!vm) return;
    if (type === "vnc") {
      addTab({
        clusterID: clusterId,
        node: nodeName,
        vmid: vm.vmid,
        type: "vm_vnc",
        label: `VNC: ${vm.name}`,
      });
    } else {
      addTab({
        clusterID: clusterId,
        node: nodeName,
        vmid: vm.vmid,
        type: kind === "ct" ? "ct_attach" : "vm_serial",
        label: `${kind === "ct" ? "CT" : "Serial"}: ${vm.name}`,
      });
    }
    void navigate("/console");
  }

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <div className="flex items-center gap-3">
            <Button variant="ghost" size="sm" asChild className="-ml-2">
              <Link to="/inventory">
                <ArrowLeft className="h-4 w-4" />
              </Link>
            </Button>
            <h1 className="text-2xl font-bold">{vm.name}</h1>
            <StatusBadge status={normalizedStatus} />
            {vm.template && (
              <Badge variant="secondary">Template</Badge>
            )}
          </div>
          <p className="text-sm text-muted-foreground">
            {kind === "ct" ? "Container" : "Virtual Machine"} &middot; VMID{" "}
            {String(vm.vmid)}
          </p>
        </div>
      </div>

      {/* Actions */}
      <VMActions
        clusterId={clusterId}
        resourceId={vmId}
        kind={kind}
        status={vm.status}
        name={vm.name}
        onClone={() => { setCloneOpen(true); }}
        onMigrate={() => { setMigrateOpen(true); }}
        onDestroy={() => { setDestroyOpen(true); }}
      />

      {/* Tabs */}
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="metrics">Metrics</TabsTrigger>
          <TabsTrigger value="console">Console</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="mt-4">
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <InfoCard label="Status" value={vm.status} />
            <InfoCard label="VMID" value={String(vm.vmid)} />
            <InfoCard label="Node" value={nodeName || "--"} />
            <InfoCard label="Type" value={vm.type === "lxc" ? "LXC Container" : "QEMU VM"} />
            <InfoCard label="CPUs" value={String(vm.cpu_count)} />
            <InfoCard label="Memory" value={formatBytes(vm.mem_total)} />
            <InfoCard label="Disk" value={formatBytes(vm.disk_total)} />
            <InfoCard label="Uptime" value={formatUptime(vm.uptime)} />
            <InfoCard label="Tags" value={vm.tags || "--"} />
            <InfoCard label="HA State" value={vm.ha_state || "--"} />
            <InfoCard label="Pool" value={vm.pool || "--"} />
            <InfoCard label="Template" value={vm.template ? "Yes" : "No"} />
          </div>
        </TabsContent>

        <TabsContent value="metrics" className="mt-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="rounded-lg border p-4">
              <p className="mb-2 text-sm font-medium text-muted-foreground">
                CPU Usage
              </p>
              <MetricMiniBar value={liveMetric?.cpuPercent ?? null} />
              <p className="mt-1 text-xs text-muted-foreground">
                {liveMetric
                  ? `${liveMetric.cpuPercent.toFixed(1)}%`
                  : "No live data"}
              </p>
            </div>
            <div className="rounded-lg border p-4">
              <p className="mb-2 text-sm font-medium text-muted-foreground">
                Memory Usage
              </p>
              <MetricMiniBar value={liveMetric?.memPercent ?? null} />
              <p className="mt-1 text-xs text-muted-foreground">
                {liveMetric
                  ? `${liveMetric.memPercent.toFixed(1)}%`
                  : "No live data"}
              </p>
            </div>
          </div>
        </TabsContent>

        <TabsContent value="console" className="mt-4">
          <div className="flex gap-3">
            {kind === "vm" && (
              <Button
                variant="outline"
                className="gap-2"
                onClick={() => { openConsole("vnc"); }}
                disabled={normalizedStatus !== "running"}
              >
                <Monitor className="h-4 w-4" />
                Open VNC Console
              </Button>
            )}
            <Button
              variant="outline"
              className="gap-2"
              onClick={() => { openConsole("terminal"); }}
              disabled={normalizedStatus !== "running"}
            >
              <Terminal className="h-4 w-4" />
              Open {kind === "ct" ? "Attach" : "Serial"} Console
            </Button>
            {normalizedStatus !== "running" && (
              <p className="self-center text-sm text-muted-foreground">
                Resource must be running to open a console.
              </p>
            )}
          </div>
        </TabsContent>
      </Tabs>

      {/* Dialogs */}
      <CloneDialog
        open={cloneOpen}
        onOpenChange={setCloneOpen}
        clusterId={clusterId}
        resourceId={vmId}
        kind={kind}
        sourceName={vm.name}
      />
      {kind === "ct" && (
        <MigrateDialog
          open={migrateOpen}
          onOpenChange={setMigrateOpen}
          clusterId={clusterId}
          containerId={vmId}
          containerName={vm.name}
        />
      )}
      <DestroyDialog
        open={destroyOpen}
        onOpenChange={setDestroyOpen}
        clusterId={clusterId}
        resourceId={vmId}
        kind={kind}
        resourceName={vm.name}
      />
    </div>
  );
}

function InfoCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border p-3">
      <p className="text-xs font-medium text-muted-foreground">{label}</p>
      <p className="mt-1 text-sm font-medium">{value}</p>
    </div>
  );
}
