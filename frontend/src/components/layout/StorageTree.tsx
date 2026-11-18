import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useLocation } from "react-router-dom";
import { Server, Database, Share2, Plus } from "lucide-react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";
import {
  filterTree,
  type TreeFilterNode,
  type TreeFilterResult,
} from "@/lib/tree-filter";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import { useClusterStorage } from "@/features/storage/api/storage-queries";
import { AddStorageDialog } from "@/features/storage/components/AddStorageDialog";
import { usePermissions } from "@/hooks/usePermissions";
import { useSidebarStore } from "@/stores/sidebar-store";
import type { ClusterResponse, StorageResponse } from "@/types/api";
import {
  TreeChevron,
  TreeHeading,
  TreeNote,
  TreeRowAside,
  TreeSkeleton,
} from "./TreeRow";
import {
  TREE_ROW_MAIN,
  TREE_ROW_TOGGLE,
  treeIndent,
  treeRowClass,
} from "./tree-row-style";

const sharedKeyOf = (clusterId: string) => `storage-shared:${clusterId}`;
const nodeGroupKeyOf = (clusterId: string, nodeId: string) =>
  `storage-node:${clusterId}:${nodeId}`;
const poolKeyOf = (poolId: string) => `pool:${poolId}`;
const poolModel = (pool: StorageResponse): TreeFilterNode => ({
  id: poolKeyOf(pool.id),
  label: pool.storage,
});

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
      className={treeRowClass({ active: isActive })}
      style={treeIndent(2, true)}
    >
      <Database className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
      <span className={cn("truncate", !pool.active && "text-muted-foreground")}>
        {pool.storage}
      </span>
      {!pool.active && <TreeRowAside>inactive</TreeRowAside>}
    </button>
  );
}

interface PoolGroupProps {
  groupKey: string;
  label: string;
  icon: "shared" | "node";
  pools: StorageResponse[];
  clusterId: string;
  /** What the filter box keeps and holds open; null when it is empty. */
  filter: TreeFilterResult | null;
}

function PoolGroup({
  groupKey,
  label,
  icon,
  pools,
  clusterId,
  filter,
}: PoolGroupProps) {
  const { expandedNodes, toggleNode } = useSidebarStore();
  // The filter holds a group open over a match; the stored expansion is left
  // as it was, and is what shows again when the box is cleared.
  const heldOpen = filter?.open.has(groupKey) ?? false;
  const isExpanded = expandedNodes.has(groupKey) || heldOpen;
  // Held open by the filter, a toggle would change nothing on screen and flip
  // what clearing the box restores.
  const toggle = () => {
    if (!heldOpen) toggleNode(groupKey);
  };
  const shownPools =
    filter === null
      ? pools
      : pools.filter((pool) => filter.visible.has(poolKeyOf(pool.id)));

  if (filter !== null && !filter.visible.has(groupKey)) return null;

  return (
    <div>
      <div className={treeRowClass()} style={treeIndent(1)}>
        <button
          aria-label={`${isExpanded ? "Collapse" : "Expand"} ${icon === "node" ? `node ${label}` : label}`}
          aria-expanded={isExpanded}
          onClick={(e) => {
            e.stopPropagation();
            toggle();
          }}
          className={TREE_ROW_TOGGLE}
        >
          <TreeChevron expanded={isExpanded} />
        </button>
        <button
          aria-expanded={isExpanded}
          onClick={toggle}
          className={TREE_ROW_MAIN}
        >
          {icon === "shared" ? (
            <Share2 className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <Server className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="truncate">{label}</span>
          <TreeRowAside>{pools.length}</TreeRowAside>
        </button>
      </div>

      {isExpanded &&
        shownPools
          .slice()
          .sort((a, b) => a.storage.localeCompare(b.storage))
          .map((pool) => (
            <PoolLeaf key={pool.id} pool={pool} clusterId={clusterId} />
          ))}
    </div>
  );
}

interface StorageClusterBranchProps {
  cluster: ClusterResponse;
  /** The filter box's text, normalised; empty when nothing is typed. */
  filter: string;
}

function StorageClusterBranch({ cluster, filter }: StorageClusterBranchProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const clusterKey = `storage-cluster:${cluster.id}`;
  const isExpanded = expandedNodes.has(clusterKey);
  const [addStorageOpen, setAddStorageOpen] = useState(false);
  // Add storage, the menu's one item, takes manage:storage.
  const canAddStorage = usePermissions().hasPermission("manage", "storage");

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

  const nodeNameOf = (nodeId: string) =>
    nodes?.find((n) => n.id === nodeId)?.name ?? nodeId;

  // The rows under this cluster, as the filter box sees them. Only what has
  // loaded: a collapsed cluster has no rows to look through.
  const filtered = useMemo(() => {
    if (filter === "" || !isExpanded || pools === undefined) return null;
    const model: TreeFilterNode = {
      id: clusterKey,
      label: cluster.name,
      children: [
        ...(shared.length > 0
          ? [
              {
                id: sharedKeyOf(cluster.id),
                label: "Shared storage",
                children: shared.map(poolModel),
              },
            ]
          : []),
        ...Array.from(byNode, ([nodeId, nodePools]) => ({
          id: nodeGroupKeyOf(cluster.id, nodeId),
          label: nodes?.find((n) => n.id === nodeId)?.name ?? nodeId,
          children: nodePools.map(poolModel),
        })),
      ],
    };
    return filterTree([model], filter);
  }, [
    filter,
    isExpanded,
    pools,
    nodes,
    shared,
    byNode,
    cluster.id,
    cluster.name,
    clusterKey,
  ]);

  if (filtered !== null && !filtered.visible.has(clusterKey)) return null;
  // A collapsed cluster has loaded nothing to look through. It stays, dimmed,
  // rather than vanish as if nothing in it matched.
  const unsearched =
    filter !== "" &&
    filtered === null &&
    !cluster.name.toLowerCase().includes(filter);

  return (
    <div>
      <ContextMenu modal={false}>
        <ContextMenuTrigger asChild disabled={!canAddStorage}>
          <div
            className={cn(
              treeRowClass({ active: isClusterActive }),
              unsearched && "opacity-55",
            )}
            style={treeIndent(0)}
            title={
              unsearched ? "Collapsed: open it to filter inside" : undefined
            }
          >
            <button
              aria-label={`${isExpanded ? "Collapse" : "Expand"} cluster ${cluster.name}`}
              aria-expanded={isExpanded}
              onClick={() => {
                toggleNode(clusterKey);
              }}
              className={TREE_ROW_TOGGLE}
            >
              <TreeChevron expanded={isExpanded} />
            </button>
            <button
              onClick={() => {
                void navigate(`/storage?cluster=${cluster.id}`);
              }}
              className={TREE_ROW_MAIN}
            >
              <span className="truncate font-semibold text-foreground">
                {cluster.name}
              </span>
            </button>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="w-40">
          <ContextMenuItem
            onClick={() => {
              setAddStorageOpen(true);
            }}
          >
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
              groupKey={sharedKeyOf(cluster.id)}
              label="Shared storage"
              icon="shared"
              pools={shared}
              clusterId={cluster.id}
              filter={filtered}
            />
          )}
          {sortedNodeIds.map((nodeId) => {
            const nodePools = byNode.get(nodeId);
            if (!nodePools || nodePools.length === 0) return null;
            return (
              <PoolGroup
                key={nodeId}
                groupKey={nodeGroupKeyOf(cluster.id, nodeId)}
                label={nodeNameOf(nodeId)}
                icon="node"
                pools={nodePools}
                clusterId={cluster.id}
                filter={filtered}
              />
            );
          })}
          {shared.length === 0 && sortedNodeIds.length === 0 && (
            <TreeNote className="pl-[30px]">No storage pools</TreeNote>
          )}
        </div>
      )}
    </div>
  );
}

export function StorageTree({ filter = "" }: { filter?: string }) {
  const { t } = useTranslation("common");
  const { data: clusters, isLoading } = useClusters();

  return (
    <div>
      <TreeHeading>{t("storage", { defaultValue: "Storage" })}</TreeHeading>

      {isLoading && <TreeSkeleton />}

      {clusters?.length === 0 && <TreeNote>No clusters</TreeNote>}

      {clusters !== undefined && clusters.length > 0 && (
        <div>
          {clusters.map((cluster) => (
            <StorageClusterBranch
              key={cluster.id}
              cluster={cluster}
              filter={filter}
            />
          ))}
          {/* Alone here once the filter has hidden every cluster. */}
          {filter !== "" && (
            <TreeNote className="hidden only:block">
              Nothing in the tree matches.
            </TreeNote>
          )}
        </div>
      )}
    </div>
  );
}
