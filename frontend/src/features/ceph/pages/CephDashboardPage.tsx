import { useState } from "react";
import { Database } from "lucide-react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useCephStatus,
  useCephOSDs,
  useCephPools,
  useCephMonitors,
  useCephFS,
  useCephCrushRules,
} from "../api/ceph-queries";
import { CephStatusCards } from "../components/CephStatusCards";
import { OSDGrid } from "../components/OSDGrid";
import { OSDTable } from "../components/OSDTable";
import { PoolTable } from "../components/PoolTable";
import { PoolCreateDialog } from "../components/PoolCreateDialog";
import { MonitorList } from "../components/MonitorList";
import { CrushTree } from "../components/CrushTree";
import { CephMetricsChart } from "../components/CephMetricsChart";
import { ApiClientError } from "@/lib/api-client";

export function CephDashboardPage() {
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const [selectedClusterId, setSelectedClusterId] = useState<string>("");

  const activeClusterId =
    selectedClusterId || (clusters.length > 0 ? clusters[0]?.id ?? "" : "");

  const statusQuery = useCephStatus(activeClusterId);
  const osdsQuery = useCephOSDs(activeClusterId);
  const poolsQuery = useCephPools(activeClusterId);
  const monitorsQuery = useCephMonitors(activeClusterId);
  const fsQuery = useCephFS(activeClusterId);
  const crushRulesQuery = useCephCrushRules(activeClusterId);

  const status = statusQuery.data;
  const osds = osdsQuery.data ?? [];
  const pools = poolsQuery.data ?? [];
  const monitors = monitorsQuery.data ?? [];
  const filesystems = fsQuery.data ?? [];
  const crushRules = crushRulesQuery.data ?? [];

  const isCephNotFound =
    statusQuery.isError &&
    statusQuery.error instanceof ApiClientError &&
    (statusQuery.error.status === 404 ||
      statusQuery.error.status === 500 ||
      statusQuery.error.status === 502);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <Database className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-semibold">Ceph</h1>
        </div>
      </div>

      {clusters.length > 1 && (
        <div className="flex gap-2">
          {clusters.map((cluster) => (
            <button
              key={cluster.id}
              onClick={() => {
                setSelectedClusterId(cluster.id);
              }}
              className={`rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
                activeClusterId === cluster.id
                  ? "bg-primary text-primary-foreground"
                  : "bg-muted text-muted-foreground hover:bg-accent"
              }`}
            >
              {cluster.name}
            </button>
          ))}
        </div>
      )}

      {statusQuery.isLoading && (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-32" />
          ))}
        </div>
      )}

      {isCephNotFound && (
        <div className="rounded-md border bg-muted/50 px-6 py-12 text-center">
          <Database className="mx-auto mb-3 h-10 w-10 text-muted-foreground" />
          <h2 className="text-lg font-medium">Ceph Not Available</h2>
          <p className="mt-1 text-sm text-muted-foreground">
            Ceph is not installed or configured on this cluster.
          </p>
        </div>
      )}

      {statusQuery.isError && !isCephNotFound && (
        <p className="text-sm text-destructive">
          Failed to load Ceph status. Check cluster connectivity.
        </p>
      )}

      {status && (
        <>
          <CephStatusCards status={status} />

          <Tabs defaultValue="overview">
            <TabsList>
              <TabsTrigger value="overview">Overview</TabsTrigger>
              <TabsTrigger value="osds">
                OSDs ({status.osdmap.num_osds})
              </TabsTrigger>
              <TabsTrigger value="pools">Pools ({pools.length})</TabsTrigger>
              <TabsTrigger value="monitors">
                Monitors ({status.monmap.num_mons})
              </TabsTrigger>
              {filesystems.length > 0 && (
                <TabsTrigger value="fs">
                  CephFS ({filesystems.length})
                </TabsTrigger>
              )}
            </TabsList>

            <TabsContent value="overview" className="space-y-6">
              <CephMetricsChart clusterId={activeClusterId} />
              {osds.length > 0 && (
                <CrushTree osds={osds} crushRules={crushRules} />
              )}
            </TabsContent>

            <TabsContent value="osds" className="space-y-4">
              <OSDGrid osds={osds} />
              <OSDTable osds={osds} />
            </TabsContent>

            <TabsContent value="pools" className="space-y-4">
              <div className="flex justify-end">
                <PoolCreateDialog
                  clusterId={activeClusterId}
                  osdCount={status.osdmap.num_osds}
                />
              </div>
              <PoolTable pools={pools} clusterId={activeClusterId} />
            </TabsContent>

            <TabsContent value="monitors" className="space-y-4">
              <MonitorList monitors={monitors} />
            </TabsContent>

            {filesystems.length > 0 && (
              <TabsContent value="fs" className="space-y-4">
                <div className="rounded-md border">
                  <table className="w-full text-sm">
                    <thead>
                      <tr className="border-b bg-muted/30">
                        <th className="px-4 py-2 text-left font-medium">Name</th>
                        <th className="px-4 py-2 text-left font-medium">Metadata Pool</th>
                        <th className="px-4 py-2 text-left font-medium">Data Pool</th>
                      </tr>
                    </thead>
                    <tbody>
                      {filesystems.map((fs) => (
                        <tr key={fs.name} className="border-b">
                          <td className="px-4 py-2 font-medium">{fs.name}</td>
                          <td className="px-4 py-2 text-muted-foreground">{fs.metadata_pool}</td>
                          <td className="px-4 py-2 text-muted-foreground">{fs.data_pool}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </TabsContent>
            )}
          </Tabs>
        </>
      )}
    </div>
  );
}
