import { useState } from "react";
import { useParams, Link, useSearchParams } from "react-router-dom";
import {
  ArrowLeft, Terminal, Cpu, MemoryStick, HardDrive, Info,
  Network, CircuitBoard, Globe,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { MetricMiniBar } from "@/features/inventory/components/MetricMiniBar";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import { useNodeHistoricalMetrics } from "@/features/dashboard/api/historical-queries";
import { useClusterMetrics } from "@/hooks/useMetrics";
import {
  useClusterNodes,
  useNodeDisks,
  useNodeNetworkInterfaces,
  useNodePCIDevices,
} from "../api/cluster-queries";
import { useConsoleStore } from "@/stores/console-store";
import { NodeAptRepositories } from "../components/NodeAptRepositories";
import { formatBytes, formatUptime } from "@/lib/format";
import type { TimeRange } from "@/types/api";

const TIME_RANGES: { label: string; value: TimeRange }[] = [
  { label: "1h", value: "1h" },
  { label: "6h", value: "6h" },
  { label: "24h", value: "24h" },
  { label: "7d", value: "7d" },
];

export function NodeDetailPage() {
  const { clusterId = "", nodeId = "" } = useParams<{
    clusterId: string;
    nodeId: string;
  }>();
  const [searchParams] = useSearchParams();
  const tabParam = searchParams.get("tab") ?? "";
  const { data: nodes, isLoading } = useClusterNodes(clusterId);
  const node = nodes?.find((n) => n.id === nodeId);

  const clusterMetrics = useClusterMetrics(clusterId);
  const liveMetric = clusterMetrics?.nodeMetrics.get(nodeId);
  const addTab = useConsoleStore((s) => s.addTab);
  const showConsole = useConsoleStore((s) => s.showConsole);

  function openShell() {
    if (!node) return;
    addTab({
      clusterID: clusterId,
      node: node.name,
      type: "node_shell",
      label: `Shell: ${node.name}`,
    });
    showConsole();
  }

  if (isLoading) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }

  if (!node) {
    return (
      <div className="p-6">
        <p className="text-destructive">Node not found</p>
        <Button variant="link" asChild className="mt-2 px-0">
          <Link to={`/clusters/${clusterId}`}>Back to Cluster</Link>
        </Button>
      </div>
    );
  }

  const swapPercent = node.swap_total > 0 ? ((node.swap_used / node.swap_total) * 100).toFixed(1) : null;

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex items-start justify-between gap-4">
        <div className="space-y-1">
          <div className="flex items-center gap-3">
            <Button variant="ghost" size="sm" asChild className="-ml-2">
              <Link to={`/clusters/${clusterId}`}>
                <ArrowLeft className="h-4 w-4" />
              </Link>
            </Button>
            <h1 className="text-2xl font-bold">{node.name}</h1>
            <Badge variant={node.status === "online" ? "default" : "destructive"}>
              {node.status}
            </Badge>
          </div>
          <p className="text-sm text-muted-foreground">
            Proxmox Node
          </p>
        </div>
        {node.status === "online" && (
          <Button
            variant="outline"
            size="sm"
            className="gap-1.5"
            onClick={openShell}
          >
            <Terminal className="h-4 w-4" />
            Shell
          </Button>
        )}
      </div>

      {/* Tabbed content */}
      <Tabs defaultValue={tabParam || "summary"} {...(tabParam ? { value: tabParam } : {})}>
        <TabsList>
          <TabsTrigger value="summary">Summary</TabsTrigger>
          <TabsTrigger value="network">Network</TabsTrigger>
          <TabsTrigger value="disks">Disks</TabsTrigger>
          <TabsTrigger value="pci">PCI Devices</TabsTrigger>
          <TabsTrigger value="updates">Updates</TabsTrigger>
        </TabsList>

        {/* Summary Tab */}
        <TabsContent value="summary" className="mt-4 space-y-6">
          {/* Hardware Info Cards */}
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <HardwareSection
              icon={<Cpu className="h-4 w-4" />}
              title="Processor"
              items={[
                { label: "Model", value: node.cpu_model || "--" },
                { label: "Sockets", value: node.cpu_sockets ? String(node.cpu_sockets) : "--" },
                { label: "Cores", value: node.cpu_cores ? `${String(node.cpu_cores)} per socket` : "--" },
                { label: "Threads", value: node.cpu_threads ? `${String(node.cpu_threads)} per core` : "--" },
                { label: "Total CPUs", value: String(node.cpu_count) },
                { label: "Frequency", value: node.cpu_mhz ? `${node.cpu_mhz} MHz` : "--" },
              ]}
            />
            <HardwareSection
              icon={<MemoryStick className="h-4 w-4" />}
              title="Memory"
              items={[
                { label: "Total RAM", value: formatBytes(node.mem_total) },
                { label: "Swap Total", value: node.swap_total ? formatBytes(node.swap_total) : "--" },
                { label: "Swap Used", value: node.swap_total ? `${formatBytes(node.swap_used)} (${swapPercent ?? "0"}%)` : "--" },
              ]}
            />
            <HardwareSection
              icon={<Info className="h-4 w-4" />}
              title="System"
              items={[
                { label: "PVE Version", value: node.pve_version || "--" },
                { label: "Kernel", value: node.kernel_version || "--" },
                { label: "Uptime", value: formatUptime(node.uptime) },
                { label: "Load Average", value: node.load_avg || "--" },
                { label: "I/O Wait", value: node.io_wait > 0 ? `${(node.io_wait * 100).toFixed(1)}%` : "--" },
                { label: "Timezone", value: node.timezone || "--" },
              ]}
            />
            <HardwareSection
              icon={<Globe className="h-4 w-4" />}
              title="Network & DNS"
              items={[
                { label: "DNS Servers", value: node.dns_servers || "--" },
                { label: "Search Domain", value: node.dns_search || "--" },
                ...(node.subscription_status ? [
                  { label: "Subscription", value: `${node.subscription_status}${node.subscription_level ? ` (${node.subscription_level})` : ""}` },
                ] : []),
              ]}
            />
          </div>

          {/* Live metrics + charts */}
          <NodeMetricsPanel
            clusterId={clusterId}
            nodeId={nodeId}
            liveMetric={liveMetric}
          />
        </TabsContent>

        {/* Network Tab */}
        <TabsContent value="network" className="mt-4">
          <NetworkTab clusterId={clusterId} nodeId={nodeId} />
        </TabsContent>

        {/* Disks Tab */}
        <TabsContent value="disks" className="mt-4">
          <DisksTab clusterId={clusterId} nodeId={nodeId} />
        </TabsContent>

        {/* PCI Devices Tab */}
        <TabsContent value="pci" className="mt-4">
          <PCIDevicesTab clusterId={clusterId} nodeId={nodeId} />
        </TabsContent>

        {/* Updates Tab */}
        <TabsContent value="updates" className="mt-4">
          {node.status === "online" ? (
            <NodeAptRepositories clusterId={clusterId} nodeName={node.name} />
          ) : (
            <p className="text-sm text-muted-foreground">
              Node must be online to view repositories.
            </p>
          )}
        </TabsContent>
      </Tabs>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Tab content components                                             */
/* ------------------------------------------------------------------ */

function NetworkTab({ clusterId, nodeId }: { clusterId: string; nodeId: string }) {
  const { data: networkInterfaces, isLoading } = useNodeNetworkInterfaces(clusterId, nodeId);

  if (isLoading) return <Skeleton className="h-48 w-full" />;

  if (!networkInterfaces || networkInterfaces.length === 0) {
    return <p className="text-sm text-muted-foreground">No network interfaces found.</p>;
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <Network className="h-4 w-4 text-muted-foreground" />
        <h2 className="text-lg font-semibold">Network Interfaces</h2>
      </div>
      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full text-sm">
          <thead className="border-b bg-muted/50">
            <tr>
              <th className="px-3 py-2 text-left font-medium">Interface</th>
              <th className="px-3 py-2 text-left font-medium">Type</th>
              <th className="px-3 py-2 text-left font-medium">Active</th>
              <th className="px-3 py-2 text-left font-medium">Address / CIDR</th>
              <th className="px-3 py-2 text-left font-medium">Gateway</th>
              <th className="px-3 py-2 text-left font-medium">Bridge Ports</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {networkInterfaces.map((iface) => (
              <tr key={iface.id} className="hover:bg-muted/30">
                <td className="px-3 py-2 font-mono text-xs">{iface.iface}</td>
                <td className="px-3 py-2">
                  <Badge variant="outline" className="text-xs">{iface.iface_type}</Badge>
                </td>
                <td className="px-3 py-2">
                  <Badge variant={iface.active ? "default" : "secondary"} className="text-xs">
                    {iface.active ? "up" : "down"}
                  </Badge>
                </td>
                <td className="px-3 py-2 font-mono text-xs">{iface.cidr || iface.address || "--"}</td>
                <td className="px-3 py-2 font-mono text-xs">{iface.gateway || "--"}</td>
                <td className="px-3 py-2 text-xs">{iface.bridge_ports || "--"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function DisksTab({ clusterId, nodeId }: { clusterId: string; nodeId: string }) {
  const { data: disks, isLoading } = useNodeDisks(clusterId, nodeId);

  if (isLoading) return <Skeleton className="h-48 w-full" />;

  if (!disks || disks.length === 0) {
    return <p className="text-sm text-muted-foreground">No physical disks found.</p>;
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <HardDrive className="h-4 w-4 text-muted-foreground" />
        <h2 className="text-lg font-semibold">Physical Disks</h2>
      </div>
      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full text-sm">
          <thead className="border-b bg-muted/50">
            <tr>
              <th className="px-3 py-2 text-left font-medium">Device</th>
              <th className="px-3 py-2 text-left font-medium">Model</th>
              <th className="px-3 py-2 text-left font-medium">Serial</th>
              <th className="px-3 py-2 text-left font-medium">Size</th>
              <th className="px-3 py-2 text-left font-medium">Type</th>
              <th className="px-3 py-2 text-left font-medium">Health</th>
              <th className="px-3 py-2 text-left font-medium">Wearout</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {disks.map((d) => (
              <tr key={d.id} className="hover:bg-muted/30">
                <td className="px-3 py-2 font-mono text-xs">{d.dev_path}</td>
                <td className="px-3 py-2">{d.model || "--"}</td>
                <td className="px-3 py-2 font-mono text-xs">{d.serial || "--"}</td>
                <td className="px-3 py-2">{formatBytes(d.size)}</td>
                <td className="px-3 py-2">
                  <Badge variant="outline" className="text-xs">
                    {d.disk_type.toUpperCase() || "unknown"}
                  </Badge>
                </td>
                <td className="px-3 py-2">
                  <Badge variant={d.health === "PASSED" ? "default" : "destructive"} className="text-xs">
                    {d.health || "unknown"}
                  </Badge>
                </td>
                <td className="px-3 py-2">{d.wearout && d.wearout !== "N/A" ? d.wearout : "--"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function PCIDevicesTab({ clusterId, nodeId }: { clusterId: string; nodeId: string }) {
  const { data: pciDevices, isLoading } = useNodePCIDevices(clusterId, nodeId);

  if (isLoading) return <Skeleton className="h-48 w-full" />;

  if (!pciDevices || pciDevices.length === 0) {
    return <p className="text-sm text-muted-foreground">No PCI devices found.</p>;
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <CircuitBoard className="h-4 w-4 text-muted-foreground" />
        <h2 className="text-lg font-semibold">PCI Devices</h2>
      </div>
      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full text-sm">
          <thead className="border-b bg-muted/50">
            <tr>
              <th className="px-3 py-2 text-left font-medium">PCI ID</th>
              <th className="px-3 py-2 text-left font-medium">Device</th>
              <th className="px-3 py-2 text-left font-medium">Vendor</th>
              <th className="px-3 py-2 text-left font-medium">IOMMU Group</th>
            </tr>
          </thead>
          <tbody className="divide-y">
            {pciDevices.map((d) => (
              <tr key={d.id} className="hover:bg-muted/30">
                <td className="px-3 py-2 font-mono text-xs">{d.pci_id}</td>
                <td className="px-3 py-2 text-xs">{d.device_name || d.device || "--"}</td>
                <td className="px-3 py-2 text-xs">{d.vendor_name || d.vendor || "--"}</td>
                <td className="px-3 py-2">{d.iommu_group >= 0 ? String(d.iommu_group) : "--"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/* ------------------------------------------------------------------ */
/* Shared sub-components                                              */
/* ------------------------------------------------------------------ */

interface HardwareItem {
  label: string;
  value: string;
}

function HardwareSection({
  icon,
  title,
  items,
}: {
  icon: React.ReactNode;
  title: string;
  items: HardwareItem[];
}) {
  return (
    <div className="rounded-lg border p-4">
      <div className="mb-3 flex items-center gap-2 text-sm font-medium">
        <span className="text-muted-foreground">{icon}</span>
        {title}
      </div>
      <dl className="space-y-1.5">
        {items.map((item) => (
          <div key={item.label} className="flex justify-between gap-2 text-sm">
            <dt className="text-muted-foreground">{item.label}</dt>
            <dd className="truncate font-medium text-right" title={item.value}>{item.value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function NodeMetricsPanel({
  clusterId,
  nodeId,
  liveMetric,
}: {
  clusterId: string;
  nodeId: string;
  liveMetric: { cpuPercent: number; memPercent: number } | undefined;
}) {
  const [timeRange, setTimeRange] = useState<TimeRange>("1h");
  const { data: historicalData, isLoading } = useNodeHistoricalMetrics(clusterId, nodeId, timeRange);
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
        <div className="h-64">
          <MetricChart title="CPU Usage" data={chartData} dataKey="cpuPercent" color="hsl(221, 83%, 53%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Memory Usage" data={chartData} dataKey="memPercent" color="hsl(142, 71%, 45%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Disk Read" data={chartData} dataKey="diskReadBps" color="hsl(38, 92%, 50%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Disk Write" data={chartData} dataKey="diskWriteBps" color="hsl(0, 84%, 60%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Network In" data={chartData} dataKey="netInBps" color="hsl(262, 83%, 58%)" timeRange={timeRange} />
        </div>
        <div className="h-64">
          <MetricChart title="Network Out" data={chartData} dataKey="netOutBps" color="hsl(330, 81%, 60%)" timeRange={timeRange} />
        </div>
      </div>
    </div>
  );
}
