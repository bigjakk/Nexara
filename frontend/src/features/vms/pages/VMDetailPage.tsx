import { useState, useRef, useEffect } from "react";
import { useParams, Link } from "react-router-dom";
import { ArrowLeft, Monitor, Terminal, Pencil, Check, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { StatusBadge } from "@/features/inventory/components/StatusBadge";
import { MetricMiniBar } from "@/features/inventory/components/MetricMiniBar";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import { useVMHistoricalMetrics } from "@/features/dashboard/api/historical-queries";
import { useConsoleStore } from "@/stores/console-store";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusterMetrics } from "@/hooks/useMetrics";
import { useVM, useSetResourceConfig, useGuestAgentInfo } from "../api/vm-queries";
import { VMActions } from "../components/VMActions";
import { CloneDialog } from "../components/CloneDialog";
import { MigrateJobDialog } from "../components/MigrateJobDialog";
import { DestroyDialog } from "../components/DestroyDialog";
import { SnapshotPanel } from "../components/SnapshotPanel";
import { CloudInitPanel } from "../components/CloudInitPanel";
import { HardwarePanel } from "../components/HardwarePanel";
import { SchedulePanel } from "../components/SchedulePanel";
import { BackupPanel } from "../components/BackupPanel";
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
  const kind: ResourceKind = rawKind === "ct" ? "ct" : "vm";
  const { data: vm, isLoading, error } = useVM(clusterId, vmId, kind);

  const clusterMetrics = useClusterMetrics(clusterId);
  const liveMetric = clusterMetrics?.vmMetrics.get(vmId);

  const { data: nodes } = useClusterNodes(clusterId);
  const addTab = useConsoleStore((s) => s.addTab);
  const showConsole = useConsoleStore((s) => s.showConsole);
  const updateTabNode = useConsoleStore((s) => s.updateTabNode);

  // Resolve node name from node_id
  const nodeName = nodes?.find((n) => n.id === vm?.node_id)?.name ?? "";

  // When VM migrates to a different node, update any open console tabs
  // so they reconnect to the new node automatically.
  const prevNodeRef = useRef(nodeName);
  useEffect(() => {
    if (nodeName && prevNodeRef.current && nodeName !== prevNodeRef.current && vm) {
      updateTabNode(clusterId, vm.vmid, nodeName);
    }
    prevNodeRef.current = nodeName;
  }, [nodeName, clusterId, vm, updateTabNode]);

  const [cloneOpen, setCloneOpen] = useState(false);
  const [migrateOpen, setMigrateOpen] = useState(false);
  const [destroyOpen, setDestroyOpen] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [newName, setNewName] = useState("");
  const renameInputRef = useRef<HTMLInputElement>(null);
  const setConfig = useSetResourceConfig();

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
        resourceId: vmId,
        kind,
      });
    } else {
      addTab({
        clusterID: clusterId,
        node: nodeName,
        vmid: vm.vmid,
        type: kind === "ct" ? "ct_vnc" : "vm_serial",
        label: `${kind === "ct" ? "CT" : "Serial"}: ${vm.name}`,
        resourceId: vmId,
        kind,
      });
    }
    showConsole();
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
            {renaming ? (
              <InlineRename
                inputRef={renameInputRef}
                value={newName}
                onChange={setNewName}
                isPending={setConfig.isPending}
                onConfirm={() => {
                  const trimmed = newName.trim();
                  if (trimmed.length === 0 || trimmed === vm.name) {
                    setRenaming(false);
                    return;
                  }
                  const field = kind === "ct" ? "hostname" : "name";
                  setConfig.mutate(
                    { clusterId, resourceId: vmId, kind, fields: { [field]: trimmed } },
                    { onSuccess: () => { setRenaming(false); } },
                  );
                }}
                onCancel={() => { setRenaming(false); }}
              />
            ) : (
              <>
                <h1 className="text-2xl font-bold">{vm.name}</h1>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-7 w-7 p-0"
                  onClick={() => { setNewName(vm.name); setRenaming(true); }}
                  title="Rename"
                >
                  <Pencil className="h-3.5 w-3.5" />
                </Button>
              </>
            )}
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

      {/* Console + Actions */}
      <div className="flex items-center gap-2">
        {kind === "vm" && (
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            onClick={() => { openConsole("vnc"); }}
            disabled={normalizedStatus !== "running"}
          >
            <Monitor className="h-4 w-4" />
            VNC Console
          </Button>
        )}
        <Button
          variant="outline"
          size="sm"
          className="gap-1.5"
          onClick={() => { openConsole("terminal"); }}
          disabled={normalizedStatus !== "running"}
        >
          <Terminal className="h-4 w-4" />
          {kind === "ct" ? "Attach" : "Serial"}
        </Button>
      </div>

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
          <TabsTrigger value="snapshots">Snapshots</TabsTrigger>
          {kind === "vm" && (
            <TabsTrigger value="cloud-init">Cloud-Init</TabsTrigger>
          )}
          <TabsTrigger value="backups">Backups</TabsTrigger>
          <TabsTrigger value="schedules">Schedules</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="mt-4 space-y-6">
          {/* Summary */}
          <div className="rounded-lg border p-4">
            <div className="grid grid-cols-2 gap-x-6 gap-y-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
              <InfoItem label="VMID" value={String(vm.vmid)} />
              <InfoItem label="Node" value={nodeName || "--"} />
              <InfoItem label="Type" value={vm.type === "lxc" ? "LXC" : "QEMU"} />
              <InfoItem label="CPUs" value={String(vm.cpu_count)} />
              <InfoItem label="Memory" value={formatBytes(vm.mem_total)} />
              <InfoItem label="Disk" value={formatBytes(vm.disk_total)} />
              <InfoItem label="Uptime" value={formatUptime(vm.uptime)} />
              <InfoItem label="HA State" value={vm.ha_state || "--"} />
              <InfoItem label="Pool" value={vm.pool || "--"} />
              {vm.tags && <InfoItem label="Tags" value={vm.tags} />}
            </div>
          </div>

          {/* Guest Agent — QEMU VMs only, when running */}
          {kind === "vm" && normalizedStatus === "running" && (
            <GuestAgentSection clusterId={clusterId} vmId={vmId} />
          )}

          {/* Metrics */}
          <VMMetricsPanel
            clusterId={clusterId}
            vmId={vmId}
            liveMetric={liveMetric}
          />
        </TabsContent>

        {kind === "vm" && (
          <TabsContent value="hardware" className="mt-4">
            <HardwarePanel clusterId={clusterId} vmId={vmId} vmStatus={vm.status} nodeName={nodeName} />
          </TabsContent>
        )}

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

        <TabsContent value="backups" className="mt-4">
          <BackupPanel
            vmid={vm.vmid}
            clusterId={clusterId}
            nodeName={nodeName}
            kind={kind}
          />
        </TabsContent>

        <TabsContent value="schedules" className="mt-4">
          <SchedulePanel
            clusterId={clusterId}
            kind={kind}
            vmid={vm.vmid}
            node={nodeName}
          />
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
      <MigrateJobDialog
        open={migrateOpen}
        onOpenChange={setMigrateOpen}
        clusterId={clusterId}
        vmid={vm.vmid}
        vmName={vm.name}
        kind={kind}
        currentNode={nodeName}
        status={vm.status}
      />
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

function GuestAgentSection({ clusterId, vmId }: { clusterId: string; vmId: string }) {
  const { data, isLoading } = useGuestAgentInfo(clusterId, vmId, true);

  return (
    <div className="rounded-lg border p-4">
      <h3 className="mb-3 text-sm font-semibold">QEMU Guest Agent</h3>
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Checking...</p>
      ) : !data?.running ? (
        <p className="text-sm text-muted-foreground">Not running</p>
      ) : (
        <div className="space-y-3">
          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            {data.os_info?.["pretty-name"] && (
              <div>
                <p className="text-xs text-muted-foreground">OS</p>
                <p className="text-sm">{data.os_info["pretty-name"]}</p>
              </div>
            )}
            {data.os_info?.["kernel-release"] && (
              <div>
                <p className="text-xs text-muted-foreground">Kernel</p>
                <p className="text-sm">{data.os_info["kernel-release"]}</p>
              </div>
            )}
            {data.os_info?.machine && (
              <div>
                <p className="text-xs text-muted-foreground">Architecture</p>
                <p className="text-sm">{data.os_info.machine}</p>
              </div>
            )}
            {data.os_info?.name && (
              <div>
                <p className="text-xs text-muted-foreground">Hostname</p>
                <p className="text-sm">{data.os_info.name}</p>
              </div>
            )}
          </div>
          {data.network_interfaces && data.network_interfaces.length > 0 && (
            <div>
              <p className="mb-1 text-xs font-medium text-muted-foreground">Network Interfaces</p>
              <div className="space-y-2">
                {data.network_interfaces
                  .filter((iface) => iface.name !== "lo")
                  .slice(0, 1)
                  .map((iface) => (
                    <div key={iface.name} className="rounded border p-2 text-sm">
                      <span className="font-medium">{iface.name}</span>
                      {iface["hardware-address"] && (
                        <span className="ml-2 text-xs text-muted-foreground">
                          ({iface["hardware-address"]})
                        </span>
                      )}
                      {iface["ip-addresses"].length > 0 && (
                        <div className="mt-1 flex flex-wrap gap-2">
                          {iface["ip-addresses"].map((ip) => (
                            <Badge key={`${ip["ip-address"]}/${String(ip.prefix)}`} variant="secondary" className="font-mono text-xs">
                              {ip["ip-address"]}/{String(ip.prefix)}
                            </Badge>
                          ))}
                        </div>
                      )}
                    </div>
                  ))}
              </div>
            </div>
          )}
        </div>
      )}
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

function InfoItem({ label, value }: { label: string; value: string }) {
  return (
    <div className="py-1">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="text-sm font-medium">{value}</p>
    </div>
  );
}

function InlineRename({
  inputRef,
  value,
  onChange,
  isPending,
  onConfirm,
  onCancel,
}: {
  inputRef: React.RefObject<HTMLInputElement | null>;
  value: string;
  onChange: (v: string) => void;
  isPending: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, [inputRef]);

  return (
    <div className="flex items-center gap-1">
      <input
        ref={inputRef}
        className="h-9 rounded-md border bg-transparent px-2 text-xl font-bold outline-none focus:ring-2 focus:ring-ring"
        value={value}
        onChange={(e) => { onChange(e.target.value); }}
        onKeyDown={(e) => {
          if (e.key === "Enter") onConfirm();
          if (e.key === "Escape") onCancel();
        }}
        disabled={isPending}
      />
      <Button
        variant="ghost"
        size="sm"
        className="h-7 w-7 p-0 text-green-600"
        onClick={onConfirm}
        disabled={isPending}
      >
        <Check className="h-4 w-4" />
      </Button>
      <Button
        variant="ghost"
        size="sm"
        className="h-7 w-7 p-0 text-destructive"
        onClick={onCancel}
        disabled={isPending}
      >
        <X className="h-4 w-4" />
      </Button>
    </div>
  );
}
