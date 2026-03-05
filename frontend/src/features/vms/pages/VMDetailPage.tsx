import { useState } from "react";
import { useParams, useNavigate, Link } from "react-router-dom";
import { ArrowLeft, Monitor, Terminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/features/inventory/components/StatusBadge";
import { MetricMiniBar } from "@/features/inventory/components/MetricMiniBar";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import { useVMHistoricalMetrics } from "@/features/dashboard/api/historical-queries";
import { useMetricStore } from "@/stores/metric-store";
import { useConsoleStore } from "@/stores/console-store";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useVM } from "../api/vm-queries";
import { VMActions } from "../components/VMActions";
import { CloneDialog } from "../components/CloneDialog";
import { MigrateDialog } from "../components/MigrateDialog";
import { DestroyDialog } from "../components/DestroyDialog";
import { SnapshotPanel } from "../components/SnapshotPanel";
import { CloudInitPanel } from "../components/CloudInitPanel";
import { HardwarePanel } from "../components/HardwarePanel";
import { SchedulePanel } from "../components/SchedulePanel";
import { InlineVNCViewer } from "../components/InlineVNCViewer";
import type { ResourceKind } from "../types/vm";
import type { ResourceStatus } from "@/features/inventory/types/inventory";
import type { TimeRange } from "@/types/api";

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
        type: kind === "ct" ? "ct_vnc" : "vm_serial",
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
          {kind === "vm" && (
            <TabsTrigger value="hardware">Hardware</TabsTrigger>
          )}
          <TabsTrigger value="metrics">Metrics</TabsTrigger>
          <TabsTrigger value="snapshots">Snapshots</TabsTrigger>
          {kind === "vm" && (
            <TabsTrigger value="cloud-init">Cloud-Init</TabsTrigger>
          )}
          <TabsTrigger value="schedules">Schedules</TabsTrigger>
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

        {kind === "vm" && (
          <TabsContent value="hardware" className="mt-4">
            <HardwarePanel clusterId={clusterId} vmId={vmId} vmStatus={vm.status} />
          </TabsContent>
        )}

        <TabsContent value="metrics" className="mt-4">
          <VMMetricsPanel
            clusterId={clusterId}
            vmId={vmId}
            liveMetric={liveMetric}
          />
        </TabsContent>

        <TabsContent value="snapshots" className="mt-4">
          <SnapshotPanel
            clusterId={clusterId}
            resourceId={vmId}
            kind={kind}
          />
        </TabsContent>

        {kind === "vm" && (
          <TabsContent value="cloud-init" className="mt-4">
            <CloudInitPanel clusterId={clusterId} vmId={vmId} />
          </TabsContent>
        )}

        <TabsContent value="schedules" className="mt-4">
          <SchedulePanel
            clusterId={clusterId}
            kind={kind}
            vmid={vm.vmid}
            node={nodeName}
          />
        </TabsContent>

        <TabsContent value="console" className="mt-4 space-y-4">
          <div className="flex gap-3">
            {kind === "vm" && (
              <Button
                variant="outline"
                className="gap-2"
                onClick={() => { openConsole("vnc"); }}
                disabled={normalizedStatus !== "running"}
              >
                <Monitor className="h-4 w-4" />
                <span className="hidden sm:inline">Open in</span> Dedicated Console
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
          {normalizedStatus === "running" && (
            <InlineVNCViewer
              clusterId={clusterId}
              node={nodeName}
              vmid={vm.vmid}
              guestType={kind === "ct" ? "lxc" : "qemu"}
            />
          )}
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

const TIME_RANGES: { label: string; value: TimeRange }[] = [
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
];

function VMMetricsPanel({
  clusterId,
  vmId,
  liveMetric,
}: {
  clusterId: string;
  vmId: string;
  liveMetric: { cpuPercent: number; memPercent: number } | undefined;
}) {
  const [timeRange, setTimeRange] = useState<TimeRange>("1h");
  const { data: historicalData, isLoading } = useVMHistoricalMetrics(clusterId, vmId, timeRange);
  const chartData = historicalData ?? [];

  return (
    <div className="space-y-4">
      {/* Live gauges */}
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-muted-foreground">CPU Usage (Live)</p>
          <MetricMiniBar value={liveMetric?.cpuPercent ?? null} />
          <p className="mt-1 text-xs text-muted-foreground">
            {liveMetric ? `${liveMetric.cpuPercent.toFixed(1)}%` : "No live data"}
          </p>
        </div>
        <div className="rounded-lg border p-4">
          <p className="mb-2 text-sm font-medium text-muted-foreground">Memory Usage (Live)</p>
          <MetricMiniBar value={liveMetric?.memPercent ?? null} />
          <p className="mt-1 text-xs text-muted-foreground">
            {liveMetric ? `${liveMetric.memPercent.toFixed(1)}%` : "No live data"}
          </p>
        </div>
      </div>

      {/* Time range selector */}
      <div className="flex items-center gap-2">
        <span className="text-sm font-medium text-muted-foreground">Historical:</span>
        <div className="flex gap-1">
          {TIME_RANGES.map((tr) => (
            <Button
              key={tr.value}
              size="sm"
              variant={timeRange === tr.value ? "default" : "outline"}
              className="h-7 px-2.5 text-xs"
              onClick={() => { setTimeRange(tr.value); }}
            >
              {tr.label}
            </Button>
          ))}
        </div>
        {isLoading && (
          <span className="text-xs text-muted-foreground">Loading...</span>
        )}
      </div>

      {/* Historical charts */}
      <div className="grid gap-4 sm:grid-cols-2">
        <MetricChart
          title="CPU Usage"
          data={chartData}
          dataKey="cpuPercent"
          color="hsl(221, 83%, 53%)"
          timeRange={timeRange}
        />
        <MetricChart
          title="Memory Usage"
          data={chartData}
          dataKey="memPercent"
          color="hsl(142, 71%, 45%)"
          timeRange={timeRange}
        />
        <MetricChart
          title="Disk Read"
          data={chartData}
          dataKey="diskReadBps"
          color="hsl(38, 92%, 50%)"
          timeRange={timeRange}
        />
        <MetricChart
          title="Disk Write"
          data={chartData}
          dataKey="diskWriteBps"
          color="hsl(0, 84%, 60%)"
          timeRange={timeRange}
        />
        <MetricChart
          title="Network In"
          data={chartData}
          dataKey="netInBps"
          color="hsl(262, 83%, 58%)"
          timeRange={timeRange}
        />
        <MetricChart
          title="Network Out"
          data={chartData}
          dataKey="netOutBps"
          color="hsl(330, 81%, 60%)"
          timeRange={timeRange}
        />
      </div>
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
