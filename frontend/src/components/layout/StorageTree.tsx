import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useLocation } from "react-router-dom";
import {
  ChevronRight,
  Server,
  HardDrive,
  Database,
  Share2,
  FolderArchive,
  Plus,
} from "lucide-react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusterStorage } from "@/features/storage/api/storage-queries";
import { AddStorageDialog } from "@/features/storage/components/AddStorageDialog";
import { useSidebarStore } from "@/stores/sidebar-store";
import type { ClusterResponse, StorageResponse } from "@/types/api";

function PoolLeaf({
  pool,
  clusterId,
}: {
  pool: StorageResponse;
  clusterId: string;
}) {
  const navigate = useNavigate();
  const location = useLocation();
  const path = `/storage/${clusterId}/${pool.id}`;
  const isActive = location.pathname === path;

  return (
    <button
      onClick={() => {
        void navigate(path);
      }}
      className={cn(
        "flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
        isActive && "bg-primary/10 text-foreground",
      )}
    >
      <Database className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span
        className={cn(
          "truncate",
          !pool.active && "italic text-muted-foreground",
        )}
      >
        {pool.storage}
        {!pool.active && " (inactive)"}
      </span>
    </button>
  );
}

interface PoolGroupProps {
  groupKey: string;
  label: string;
  icon: "shared" | "node";
  pools: StorageResponse[];
  clusterId: string;
}

function PoolGroup({ groupKey, label, icon, pools, clusterId }: PoolGroupProps) {
  const { expandedNodes, toggleNode } = useSidebarStore();
  const isExpanded = expandedNodes.has(groupKey);

  return (
    <div className="border-l border-border pl-3 ml-3">
      <div className="flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors">
        <button
          onClick={(e) => {
            e.stopPropagation();
            toggleNode(groupKey);
          }}
          className="shrink-0"
        >
          <ChevronRight
            className={cn(
              "h-3 w-3 transition-transform",
              isExpanded && "rotate-90",
            )}
          />
        </button>
        <button
          onClick={() => {
            toggleNode(groupKey);
          }}
          className="flex min-w-0 flex-1 items-center gap-1.5"
        >
          {icon === "shared" ? (
            <Share2 className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <Server className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="truncate text-muted-foreground">{label}</span>
          <span className="ml-auto text-[10px] text-muted-foreground/70">
            {pools.length}
          </span>
        </button>
      </div>

      {isExpanded && pools.length > 0 && (
        <div className="border-l border-border pl-3 ml-3">
          {pools
            .slice()
            .sort((a, b) => a.storage.localeCompare(b.storage))
            .map((pool) => (
              <PoolLeaf key={pool.id} pool={pool} clusterId={clusterId} />
            ))}
        </div>
      )}
    </div>
  );
}

interface StorageClusterBranchProps {
  cluster: ClusterResponse;
}

function StorageClusterBranch({ cluster }: StorageClusterBranchProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const clusterKey = `storage-cluster:${cluster.id}`;
  const isExpanded = expandedNodes.has(clusterKey);
  const [addStorageOpen, setAddStorageOpen] = useState(false);

  const { data: pools } = useClusterStorage(isExpanded ? cluster.id : "");
  const { data: nodes } = useClusterNodes(isExpanded ? cluster.id : "");

  const search = new URLSearchParams(location.search);
  const onStorageDetail = location.pathname.startsWith(
    `/storage/${cluster.id}/`,
  );
  const onStorageListForCluster =
    location.pathname === "/storage" && search.get("cluster") === cluster.id;

  useEffect(() => {
    if (onStorageDetail || onStorageListForCluster) {
      expandNode(clusterKey);
    }
  }, [onStorageDetail, onStorageListForCluster, clusterKey, expandNode]);

  const isClusterActive = onStorageListForCluster;

  const { shared, byNode } = useMemo(() => {
    const sharedMap = new Map<string, StorageResponse>();
    const local = new Map<string, StorageResponse[]>();
    for (const p of pools ?? []) {
      if (p.shared) {
        if (!sharedMap.has(p.storage)) sharedMap.set(p.storage, p);
      } else {
        const list = local.get(p.node_id) ?? [];
        list.push(p);
        local.set(p.node_id, list);
      }
    }
    return { shared: Array.from(sharedMap.values()), byNode: local };
  }, [pools]);

  const sortedNodeIds = useMemo(() => {
    return Array.from(byNode.keys()).sort((a, b) => {
      const aName = nodes?.find((n) => n.id === a)?.name ?? a;
      const bName = nodes?.find((n) => n.id === b)?.name ?? b;
      return aName.localeCompare(bName);
    });
  }, [byNode, nodes]);

  return (
    <div>
      <ContextMenu modal={false}>
        <ContextMenuTrigger asChild>
          <div
            className={cn(
              "group flex items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
              isClusterActive && "bg-primary/10 text-foreground",
            )}
          >
            <button
              onClick={() => {
                toggleNode(clusterKey);
              }}
              className="shrink-0"
            >
              <ChevronRight
                className={cn(
                  "h-3 w-3 transition-transform",
                  isExpanded && "rotate-90",
                )}
              />
            </button>
            <button
              onClick={() => {
                void navigate(`/storage?cluster=${cluster.id}`);
              }}
              className="flex min-w-0 flex-1 items-center gap-1.5"
            >
              <Server className="h-3.5 w-3.5 shrink-0 text-primary" />
              <span className="truncate font-medium">{cluster.name}</span>
            </button>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="w-40">
          <ContextMenuItem onClick={() => { setAddStorageOpen(true); }}>
            <Plus className="mr-2 h-3.5 w-3.5" />
            Add storage
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

      <AddStorageDialog
        clusterId={cluster.id}
        open={addStorageOpen}
        onOpenChange={setAddStorageOpen}
      />

      {isExpanded && pools && (
        <div>
          {shared.length > 0 && (
            <PoolGroup
              groupKey={`storage-shared:${cluster.id}`}
              label="Shared Storage"
              icon="shared"
              pools={shared}
              clusterId={cluster.id}
            />
          )}
          {sortedNodeIds.map((nodeId) => {
            const nodePools = byNode.get(nodeId);
            if (!nodePools || nodePools.length === 0) return null;
            const nodeName = nodes?.find((n) => n.id === nodeId)?.name ?? nodeId;
            return (
              <PoolGroup
                key={nodeId}
                groupKey={`storage-node:${cluster.id}:${nodeId}`}
                label={nodeName}
                icon="node"
                pools={nodePools}
                clusterId={cluster.id}
              />
            );
          })}
          {shared.length === 0 && sortedNodeIds.length === 0 && (
            <p className="ml-6 py-1 text-xs text-muted-foreground">
              No storage pools
            </p>
          )}
        </div>
      )}
    </div>
  );
}

export function StorageTree() {
  const { t } = useTranslation("common");
  const { data: clusters, isLoading } = useClusters();

  return (
    <div className="space-y-1 py-1">
      <div className="flex items-center justify-between px-2 pb-1">
        <span className="flex items-center gap-1 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
          <FolderArchive className="h-3 w-3" />
          {t("storage", { defaultValue: "Storage" })}
        </span>
      </div>

      {isLoading && (
        <div className="space-y-1 px-2">
          {Array.from({ length: 2 }, (_, i) => (
            <div key={i} className="h-6 animate-pulse rounded bg-muted" />
          ))}
        </div>
      )}

      {clusters?.length === 0 && (
        <p className="flex items-center gap-1.5 px-2 text-xs text-muted-foreground">
          <HardDrive className="h-3 w-3" />
          No clusters
        </p>
      )}

      {clusters?.map((cluster) => (
        <StorageClusterBranch key={cluster.id} cluster={cluster} />
      ))}
    </div>
  );
}
