import { useState, useMemo, useEffect } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { HardDrive, ChevronDown, ChevronRight, Server, Share2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/EmptyState";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusterStorage } from "../api/storage-queries";
import { StorageCapacityBar } from "../components/StorageCapacityBar";
import { AddStorageDialog } from "../components/AddStorageDialog";
import type { StorageResponse, NodeResponse } from "@/types/api";

interface StorageGroup {
  label: string;
  icon: "shared" | "node";
  pools: StorageResponse[];
}

function groupStorage(
  pools: StorageResponse[],
  nodes: NodeResponse[],
): StorageGroup[] {
  const nodeMap = new Map<string, string>();
  for (const node of nodes) {
    nodeMap.set(node.id, node.name);
  }

  // Deduplicate shared storage — keep first occurrence per storage name
  const sharedSeen = new Map<string, StorageResponse>();
  const localByNode = new Map<string, StorageResponse[]>();

  for (const pool of pools) {
    if (pool.shared) {
      if (!sharedSeen.has(pool.storage)) {
        sharedSeen.set(pool.storage, pool);
      }
    } else {
      const nodeId = pool.node_id;
      const existing = localByNode.get(nodeId);
      if (existing) {
        existing.push(pool);
      } else {
        localByNode.set(nodeId, [pool]);
      }
    }
  }

  const groups: StorageGroup[] = [];

  if (sharedSeen.size > 0) {
    groups.push({
      label: "Shared Storage",
      icon: "shared",
      pools: Array.from(sharedSeen.values()),
    });
  }

  // Sort nodes by name
  const sortedNodeIds = Array.from(localByNode.keys()).sort((a, b) => {
    const nameA = nodeMap.get(a) ?? a;
    const nameB = nodeMap.get(b) ?? b;
    return nameA.localeCompare(nameB);
  });

  for (const nodeId of sortedNodeIds) {
    const nodePools = localByNode.get(nodeId);
    if (nodePools && nodePools.length > 0) {
      groups.push({
        label: nodeMap.get(nodeId) ?? nodeId,
        icon: "node",
        pools: nodePools,
      });
    }
  }

  return groups;
}

export function StoragePage() {
  const { t: td } = useTranslation("dashboard");
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const clusterParam = searchParams.get("cluster") ?? "";
  const [selectedClusterId, setSelectedClusterId] = useState<string>(clusterParam);

  // Sync with URL query param when it changes (e.g. navigating from search)
  useEffect(() => {
    if (clusterParam) {
      setSelectedClusterId(clusterParam);
    }
  }, [clusterParam]);

  const activeClusterId =
    selectedClusterId || (clusters.length > 0 ? clusters[0]?.id ?? "" : "");

  const storageQuery = useClusterStorage(activeClusterId);
  const nodesQuery = useClusterNodes(activeClusterId);
  const pools = useMemo(() => storageQuery.data ?? [], [storageQuery.data]);
  const nodes = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data]);

  const groups = useMemo(() => groupStorage(pools, nodes), [pools, nodes]);

  function openPool(pool: StorageResponse) {
    void navigate(`/storage/${activeClusterId}/${pool.id}`);
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <HardDrive className="h-6 w-6 text-primary" />
          <h1 className="text-2xl font-semibold">Storage</h1>
        </div>
        {activeClusterId && (
          <AddStorageDialog clusterId={activeClusterId} />
        )}
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

      {storageQuery.isLoading && (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className="h-36" />
          ))}
        </div>
      )}

      {!storageQuery.isLoading && (
        <>
          {groups.map((group) => (
            <StorageGroupSection
              key={group.label}
              group={group}
              onSelectPool={openPool}
            />
          ))}
          {pools.length === 0 && (
            clusters.length === 0 ? (
              <EmptyState
                icon={HardDrive}
                title={td("noClustersRegistered")}
                description={td("addClusterToGetStarted")}
                action={<AddClusterDialog />}
              />
            ) : (
              <EmptyState
                icon={HardDrive}
                title="No storage pools yet"
                description="This cluster has no storage pools configured. Add one in the Proxmox UI to see it here."
              />
            )
          )}
        </>
      )}
    </div>
  );
}

// --- Collapsible Storage Group ---

interface StorageGroupSectionProps {
  group: StorageGroup;
  onSelectPool: (pool: StorageResponse) => void;
}

function StorageGroupSection({ group, onSelectPool }: StorageGroupSectionProps) {
  const [expanded, setExpanded] = useState(true);

  return (
    <div className="space-y-3">
      <button
        onClick={() => { setExpanded(!expanded); }}
        className="flex w-full items-center gap-2 text-left"
      >
        {expanded ? (
          <ChevronDown className="h-4 w-4 text-muted-foreground" />
        ) : (
          <ChevronRight className="h-4 w-4 text-muted-foreground" />
        )}
        {group.icon === "shared" ? (
          <Share2 className="h-4 w-4 text-muted-foreground" />
        ) : (
          <Server className="h-4 w-4 text-muted-foreground" />
        )}
        <span className="text-sm font-medium">{group.label}</span>
        <Badge variant="secondary" className="text-xs">
          {group.pools.length}
        </Badge>
      </button>

      {expanded && (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {group.pools.map((pool) => (
            <Card
              key={pool.id}
              className="cursor-pointer transition-shadow hover:shadow-md"
              onClick={() => { onSelectPool(pool); }}
            >
              <CardHeader className="pb-2">
                <div className="flex items-center justify-between">
                  <CardTitle className="text-base">{pool.storage}</CardTitle>
                  <Badge variant={pool.active ? "default" : "secondary"}>
                    {pool.type}
                  </Badge>
                </div>
              </CardHeader>
              <CardContent className="space-y-3">
                <StorageCapacityBar used={pool.used} total={pool.total} />
                <div className="flex flex-wrap gap-1">
                  {pool.content.split(",").map((ct) => (
                    <Badge
                      key={ct}
                      variant="outline"
                      className="text-xs"
                    >
                      {ct.trim()}
                    </Badge>
                  ))}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
