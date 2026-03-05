import { useState } from "react";
import { useParams, Link, useNavigate } from "react-router-dom";
import { ArrowLeft, Terminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { MetricMiniBar } from "@/features/inventory/components/MetricMiniBar";
import { MetricChart } from "@/features/dashboard/components/MetricChart";
import { useNodeHistoricalMetrics } from "@/features/dashboard/api/historical-queries";
import { useMetricStore } from "@/stores/metric-store";
import { useConsoleStore } from "@/stores/console-store";
import { useClusterNodes } from "../api/cluster-queries";
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
  const navigate = useNavigate();
  const addTab = useConsoleStore((s) => s.addTab);

  const { data: nodes, isLoading } = useClusterNodes(clusterId);
  const node = nodes?.find((n) => n.id === nodeId);

  const metricsMap = useMetricStore((s) => s.metrics);
  const clusterMetrics = metricsMap.get(clusterId);
  const liveMetric = clusterMetrics?.nodeMetrics.get(nodeId);

  function openShell() {
    if (!node) return;
    addTab({
      clusterID: clusterId,
      node: node.name,
      type: "node_shell",
      label: `Shell: ${node.name}`,
    });
    void navigate("/console");
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
        <Button
          variant="outline"
          className="gap-2"
          disabled={node.status !== "online"}
          onClick={openShell}
        >
          <Terminal className="h-4 w-4" />
          Open Shell
        </Button>
      </div>

      {/* Tabs */}
      <Tabs defaultValue="overview">
        <TabsList>
          <TabsTrigger value="overview">Overview</TabsTrigger>
          <TabsTrigger value="metrics">Metrics</TabsTrigger>
        </TabsList>

        <TabsContent value="overview" className="mt-4">
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <InfoCard label="Status" value={node.status} />
            <InfoCard label="CPUs" value={String(node.cpu_count)} />
            <InfoCard label="Memory" value={formatBytes(node.mem_total)} />
            <InfoCard label="Disk" value={formatBytes(node.disk_total)} />
            <InfoCard label="PVE Version" value={node.pve_version || "--"} />
            <InfoCard label="Uptime" value={formatUptime(node.uptime)} />
          </div>
        </TabsContent>

        <TabsContent value="metrics" className="mt-4">
          <NodeMetricsPanel
            clusterId={clusterId}
            nodeId={nodeId}
            liveMetric={liveMetric}
          />
        </TabsContent>
      </Tabs>
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
