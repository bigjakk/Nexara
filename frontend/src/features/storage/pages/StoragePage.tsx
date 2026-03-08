import { useState, useMemo } from "react";
import { HardDrive, ChevronDown, ChevronRight, Server, Share2 } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Skeleton } from "@/components/ui/skeleton";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import {
  useClusterStorage,
  useStorageContent,
} from "../api/storage-queries";
import { StorageCapacityBar } from "../components/StorageCapacityBar";
import { StorageContentTable } from "../components/StorageContentTable";
import { UploadDialog } from "../components/UploadDialog";
import { BulkMoveDialog } from "../components/BulkMoveDialog";
import { AddStorageDialog } from "../components/AddStorageDialog";
import { EditStorageDialog } from "../components/EditStorageDialog";
import { DeleteStorageDialog } from "../components/DeleteStorageDialog";
import type { StorageResponse, NodeResponse } from "@/types/api";
import type { StorageContentItem } from "../types/storage";

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
  const clustersQuery = useClusters();
  const clusters = clustersQuery.data ?? [];
  const [selectedClusterId, setSelectedClusterId] = useState<string>("");
  const [selectedPool, setSelectedPool] = useState<StorageResponse | null>(
    null,
  );

  const activeClusterId =
    selectedClusterId || (clusters.length > 0 ? clusters[0]?.id ?? "" : "");

  const storageQuery = useClusterStorage(activeClusterId);
  const nodesQuery = useClusterNodes(activeClusterId);
  const pools = useMemo(() => storageQuery.data ?? [], [storageQuery.data]);
  const nodes = useMemo(() => nodesQuery.data ?? [], [nodesQuery.data]);

  const groups = useMemo(() => groupStorage(pools, nodes), [pools, nodes]);

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
                setSelectedPool(null);
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

      {!selectedPool && !storageQuery.isLoading && (
        <>
          {groups.map((group) => (
            <StorageGroupSection
              key={group.label}
              group={group}
              onSelectPool={setSelectedPool}
            />
          ))}
          {pools.length === 0 && (
            <p className="py-8 text-center text-muted-foreground">
              No storage pools found.
            </p>
          )}
        </>
      )}

      {selectedPool && (
        <StoragePoolDetail
          pool={selectedPool}
          clusterId={activeClusterId}
          allPools={pools}
          onBack={() => { setSelectedPool(null); }}
        />
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

// --- Storage Pool Detail View ---

interface StoragePoolDetailProps {
  pool: StorageResponse;
  clusterId: string;
  allPools: StorageResponse[];
  onBack: () => void;
}

function StoragePoolDetail({
  pool,
  clusterId,
  allPools,
  onBack,
}: StoragePoolDetailProps) {
  const contentQuery = useStorageContent(clusterId, pool.id);
  const items = contentQuery.data ?? [];

  const contentTypes = pool.content.split(",").map((s) => s.trim());
  const hasImages = contentTypes.includes("images");
  const filterableTypes = contentTypes.filter(
    (t) => t === "iso" || t === "vztmpl" || t === "images" || t === "backup" || t === "rootdir" || t === "snippets",
  );

  // Deduplicated target storage options (other image-capable pools)
  const evacuateTargets = useMemo(() => {
    const seen = new Set<string>();
    return allPools
      .filter((p) => p.content.includes("images") && p.storage !== pool.storage && p.active && p.enabled)
      .filter((p) => {
        if (seen.has(p.storage)) return false;
        seen.add(p.storage);
        return true;
      })
      .map((p) => p.storage);
  }, [allPools, pool.storage]);

  // Group items by content type.
  function filterByType(type: string): StorageContentItem[] {
    return items.filter((item) => item.content === type);
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <button
            onClick={onBack}
            className="text-sm text-muted-foreground hover:text-foreground"
          >
            &larr; All Pools
          </button>
          <h2 className="text-xl font-semibold">{pool.storage}</h2>
          <Badge variant="outline">{pool.type}</Badge>
        </div>
        <div className="flex items-center gap-2">
          {hasImages && evacuateTargets.length > 0 && (
            <BulkMoveDialog
              clusterId={clusterId}
              storageId={pool.id}
              storageName={pool.storage}
              targetOptions={evacuateTargets}
            />
          )}
          <UploadDialog
            clusterId={clusterId}
            storageId={pool.id}
            supportedContent={pool.content}
          />
          <EditStorageDialog
            clusterId={clusterId}
            storageId={pool.id}
            storageName={pool.storage}
            storageType={pool.type}
          />
          <DeleteStorageDialog
            clusterId={clusterId}
            storageId={pool.id}
            storageName={pool.storage}
            onDeleted={onBack}
          />
        </div>
      </div>

      <StorageCapacityBar used={pool.used} total={pool.total} />

      {contentQuery.isLoading && (
        <div className="space-y-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-8 w-full" />
        </div>
      )}

      {!contentQuery.isLoading && filterableTypes.length > 1 && (
        <Tabs defaultValue={filterableTypes[0] ?? "all"}>
          <TabsList>
            {filterableTypes.map((t) => (
              <TabsTrigger key={t} value={t}>
                {t} ({filterByType(t).length})
              </TabsTrigger>
            ))}
            <TabsTrigger value="all">All ({items.length})</TabsTrigger>
          </TabsList>
          {filterableTypes.map((t) => (
            <TabsContent key={t} value={t}>
              <StorageContentTable
                items={filterByType(t)}
                clusterId={clusterId}
                storageId={pool.id}
              />
            </TabsContent>
          ))}
          <TabsContent value="all">
            <StorageContentTable
              items={items}
              clusterId={clusterId}
              storageId={pool.id}
            />
          </TabsContent>
        </Tabs>
      )}

      {!contentQuery.isLoading && filterableTypes.length <= 1 && (
        <StorageContentTable
          items={items}
          clusterId={clusterId}
          storageId={pool.id}
        />
      )}

      {contentQuery.isError && (
        <p className="text-sm text-destructive">
          Failed to load storage content.
        </p>
      )}
    </div>
  );
}
