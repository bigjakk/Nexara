import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Database } from "lucide-react";
import {
  useCephStatus,
  useCephOSDs,
  useCephPools,
  useCephMonitors,
  useCephFS,
  useCephCrushRules,
} from "@/features/ceph/api/ceph-queries";
import { CephStatusCards } from "@/features/ceph/components/CephStatusCards";
import { OSDGrid } from "@/features/ceph/components/OSDGrid";
import { OSDTable } from "@/features/ceph/components/OSDTable";
import { PoolTable } from "@/features/ceph/components/PoolTable";
import { PoolCreateDialog } from "@/features/ceph/components/PoolCreateDialog";
import { MonitorList } from "@/features/ceph/components/MonitorList";
import { CrushTree } from "@/features/ceph/components/CrushTree";
import { CephMetricsChart } from "@/features/ceph/components/CephMetricsChart";
import { ApiClientError } from "@/lib/api-client";

interface ClusterCephTabProps {
  clusterId: string;
}

export function ClusterCephTab({ clusterId }: ClusterCephTabProps) {
  const statusQuery = useCephStatus(clusterId);
  const osdsQuery = useCephOSDs(clusterId);
  const poolsQuery = useCephPools(clusterId);
  const monitorsQuery = useCephMonitors(clusterId);
  const fsQuery = useCephFS(clusterId);
  const crushRulesQuery = useCephCrushRules(clusterId);

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

  if (statusQuery.isLoading) {
    return (
      <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, i) => (
          <Skeleton key={i} className="h-32" />
        ))}
      </div>
    );
  }

  if (isCephNotFound) {
    return (
      <div className="rounded-md border bg-muted/50 px-6 py-12 text-center">
        <Database className="mx-auto mb-3 h-10 w-10 text-muted-foreground" />
        <h2 className="text-lg font-medium">Ceph Not Available</h2>
        <p className="mt-1 text-sm text-muted-foreground">
          Ceph is not installed or configured on this cluster.
        </p>
      </div>
    );
  }

  if (statusQuery.isError) {
    return (
      <p className="text-sm text-destructive">
        Failed to load Ceph status. Check cluster connectivity.
      </p>
    );
  }

  if (!status) return null;

  return (
    <div className="space-y-6">
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
          <CephMetricsChart clusterId={clusterId} status={status} />
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
              clusterId={clusterId}
              osdCount={status.osdmap.num_osds}
            />
          </div>
          <PoolTable pools={pools} clusterId={clusterId} />
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
    </div>
  );
}
