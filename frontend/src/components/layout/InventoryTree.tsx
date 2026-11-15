import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useLocation } from "react-router-dom";
import {
  AlertTriangle,
  Server,
  MoreHorizontal,
  Plus,
  Pencil,
  Trash2,
  Wrench,
} from "lucide-react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";
import {
  FallbackFocusContext,
  useFallbackFocus,
} from "@/components/ui/return-focus";
import {
  severityTextClass,
  worstIssueSeverity,
} from "@/features/ceph/lib/ceph-health";
import { visibleClusterIssues } from "@/lib/health-issues";
import { useHealthDismissStore } from "@/stores/health-dismiss-store";
import { useHealthMuteStore } from "@/stores/health-mute-store";
import { StatusIcon } from "@/components/StatusIcon";
import {
  filterTree,
  type TreeFilterNode,
  type TreeFilterResult,
} from "@/lib/tree-filter";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useClusterNodes,
  useClusterVMs,
  useSetNodeMaintenance,
} from "@/features/clusters/api/cluster-queries";
import { useSSHCredentials } from "@/features/rolling-updates/api/rolling-update-queries";
import { useAuth } from "@/hooks/useAuth";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { useSidebarStore } from "@/stores/sidebar-store";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { EditClusterDialog } from "@/features/clusters/components/EditClusterDialog";
import { DeleteClusterDialog } from "@/features/clusters/components/DeleteClusterDialog";
import {
  FavoriteContextItem,
  FavoriteDropdownItem,
} from "@/features/favorites/components/FavoriteMenuItem";
import { CreateResourceContextItems } from "./create-resource-actions";
import {
  TreeChevron,
  TreeGuestRow,
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
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { ClusterResponse, NodeResponse, VMResponse } from "@/types/api";

const nodeKeyOf = (clusterId: string, nodeId: string) =>
  `node:${clusterId}:${nodeId}`;
const guestKeyOf = (vmId: string) => `vm:${vmId}`;
const guestLabel = (vm: VMResponse) => `${String(vm.vmid)} ${vm.name}`;

interface NodeBranchProps {
  node: NodeResponse;
  vms: VMResponse[];
  clusterId: string;
  /** What the filter box keeps and holds open; null when it is empty. */
  filter: TreeFilterResult | null;
}

function NodeBranch({ node, vms, clusterId, filter }: NodeBranchProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const nodeKey = nodeKeyOf(clusterId, node.id);
  // The filter holds a branch open over a match; the stored expansion is left
  // as it was, and is what shows again when the box is cleared.
  const heldOpen = filter?.open.has(nodeKey) ?? false;
  const isExpanded = expandedNodes.has(nodeKey) || heldOpen;
  const nodeVMs = vms.filter((vm) => vm.node_id === node.id);
  const shownVMs =
    filter === null
      ? nodeVMs
      : nodeVMs.filter((vm) => filter.visible.has(guestKeyOf(vm.id)));
  const isActive =
    location.pathname === `/clusters/${clusterId}/nodes/${node.id}`;
  const { canManage } = useAuth();
  const { data: sshCreds } = useSSHCredentials(clusterId);
  const maintenance = useSetNodeMaintenance(clusterId, node.name);
  const inMaintenance = node.ha_state === "maintenance";
  const canMaintenance = sshCreds != null && canManage("node");
  const [maintOpen, setMaintOpen] = useState(false);

  // Auto-expand if a child VM is active
  useEffect(() => {
    const match = location.pathname.match(
      /^\/inventory\/(qemu|lxc)\/([^/]+)\/([^/]+)$/,
    );
    if (match) {
      const [, , pathClusterId, pathVmId] = match;
      if (pathClusterId === clusterId) {
        const vm = nodeVMs.find((v) => v.id === pathVmId);
        if (vm) {
          expandNode(nodeKey);
        }
      }
    }
  }, [location.pathname, clusterId, nodeKey, nodeVMs, expandNode]);

  if (filter !== null && !filter.visible.has(nodeKey)) return null;

  return (
    <div>
      <ContextMenu modal={false}>
        <ContextMenuTrigger asChild>
          <div
            className={treeRowClass({ active: isActive })}
            style={treeIndent(1)}
          >
            <button
              aria-label={`${isExpanded ? "Collapse" : "Expand"} node ${node.name}`}
              aria-expanded={nodeVMs.length > 0 ? isExpanded : undefined}
              disabled={nodeVMs.length === 0}
              onClick={(e) => {
                e.stopPropagation();
                // Held open by the filter: a toggle would change nothing on
                // screen and flip what clearing the box restores.
                if (heldOpen) return;
                if (nodeVMs.length > 0) toggleNode(nodeKey);
              }}
              className={TREE_ROW_TOGGLE}
            >
              <TreeChevron
                expanded={isExpanded}
                hidden={nodeVMs.length === 0}
              />
            </button>
            <button
              onClick={() => {
                void navigate(`/clusters/${clusterId}/nodes/${node.id}`);
              }}
              className={TREE_ROW_MAIN}
            >
              {node.ha_state === "maintenance" ? (
                <Wrench
                  aria-label="Maintenance"
                  className="h-3 w-3 shrink-0 text-amber-600 dark:text-amber-500"
                />
              ) : (
                <StatusIcon status={node.status} />
              )}
              <Server className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <span className="truncate">{node.name}</span>
              {nodeVMs.length > 0 && (
                <TreeRowAside>{nodeVMs.length}</TreeRowAside>
              )}
            </button>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="w-48">
          <FavoriteContextItem
            target={{
              resource_type: "node",
              cluster_id: clusterId,
              ref: node.name,
            }}
          />
          {canMaintenance && (
            <>
              <ContextMenuSeparator />
              <ContextMenuItem
                onClick={() => {
                  setMaintOpen(true);
                }}
              >
                <Wrench className="mr-2 h-3.5 w-3.5" />
                {inMaintenance ? "Exit Maintenance" : "Enter Maintenance"}
              </ContextMenuItem>
            </>
          )}
        </ContextMenuContent>
      </ContextMenu>

      {isExpanded &&
        shownVMs
          .slice()
          .sort((a, b) => a.vmid - b.vmid)
          .map((vm) => (
            <TreeGuestRow
              key={vm.id}
              vm={vm}
              clusterId={clusterId}
              currentNode={node.name}
              depth={2}
            />
          ))}

      <AlertDialog open={maintOpen} onOpenChange={setMaintOpen}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {inMaintenance
                ? `Exit maintenance on ${node.name}?`
                : `Enter maintenance on ${node.name}?`}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {inMaintenance
                ? "Takes the node out of HA maintenance so HA can place guests on it again. Runs ha-manager node-maintenance over SSH."
                : "Puts the node into HA maintenance: HA-managed guests are migrated away and the node will not receive new ones. Runs ha-manager node-maintenance over SSH."}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                maintenance.mutate(!inMaintenance);
              }}
              disabled={maintenance.isPending}
            >
              {inMaintenance ? "Exit Maintenance" : "Enter Maintenance"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

interface ClusterBranchProps {
  cluster: ClusterResponse;
  /** The filter box's text, normalised; empty when nothing is typed. */
  filter: string;
}

function ClusterBranch({ cluster, filter }: ClusterBranchProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const clusterKey = `cluster:${cluster.id}`;
  const isExpanded = expandedNodes.has(clusterKey);
  // Respect dismissed/muted so the sidebar dot matches the health pill.
  const dismissed = useHealthDismissStore((s) => s.dismissed);
  const mutedTypes = useHealthMuteStore((s) => s.mutedTypes);
  const clusterVisibleIssues = visibleClusterIssues(
    cluster.id,
    cluster.issues ?? [],
    new Set(dismissed),
    new Set(mutedTypes),
  );
  const clusterWorst = worstIssueSeverity(clusterVisibleIssues);
  const clusterIssueCount = clusterVisibleIssues.length;
  const isActive = location.pathname === `/clusters/${cluster.id}`;

  const [editOpen, setEditOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  // Only fetch children when expanded
  const { data: nodes } = useClusterNodes(isExpanded ? cluster.id : "");
  const { data: vms } = useClusterVMs(isExpanded ? cluster.id : "");

  // Auto-expand if navigating to a child route
  useEffect(() => {
    if (
      location.pathname.startsWith(`/clusters/${cluster.id}/`) ||
      location.pathname.match(
        new RegExp(`^/inventory/(qemu|lxc)/${cluster.id}/`),
      )
    ) {
      expandNode(clusterKey);
    }
  }, [location.pathname, cluster.id, clusterKey, expandNode]);

  // The rows under this cluster, as the filter box sees them. Only what has
  // loaded: a collapsed cluster has no rows to look through.
  const filtered = useMemo(() => {
    if (filter === "" || !isExpanded || nodes === undefined) return null;
    const model: TreeFilterNode = {
      id: clusterKey,
      label: cluster.name,
      children: nodes.map((node) => ({
        id: nodeKeyOf(cluster.id, node.id),
        label: node.name,
        children: (vms ?? [])
          .filter((vm) => vm.node_id === node.id)
          .map((vm) => ({ id: guestKeyOf(vm.id), label: guestLabel(vm) })),
      })),
    };
    return filterTree([model], filter);
  }, [filter, isExpanded, nodes, vms, cluster.id, cluster.name, clusterKey]);

  if (filtered !== null && !filtered.visible.has(clusterKey)) return null;
  // A collapsed cluster has loaded nothing to look through. It stays, dimmed,
  // rather than vanish as if nothing in it matched.
  const unsearched =
    filter !== "" &&
    filtered === null &&
    !cluster.name.toLowerCase().includes(filter);

  return (
    <>
      <ContextMenu modal={false}>
        <ContextMenuTrigger asChild>
          <div>
            <div
              className={cn(
                treeRowClass({ active: isActive }),
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
                  void navigate(`/clusters/${cluster.id}`);
                }}
                className={TREE_ROW_MAIN}
              >
                <StatusIcon
                  status={
                    cluster.status === "degraded" ? "degraded" : cluster.status
                  }
                />
                <span className="truncate font-semibold text-foreground">
                  {cluster.name}
                </span>
                {clusterWorst !== "ok" && (
                  <span
                    className={cn(
                      "flex shrink-0 items-center gap-0.5 text-[11.5px] font-semibold tabular-nums",
                      severityTextClass[clusterWorst],
                    )}
                    title={`${String(clusterIssueCount)} health issue${clusterIssueCount === 1 ? "" : "s"}`}
                  >
                    <AlertTriangle aria-hidden="true" className="h-3 w-3" />
                    {clusterIssueCount}
                  </span>
                )}
              </button>

              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button
                    aria-label={`Actions for ${cluster.name}`}
                    className="grid h-5 w-5 shrink-0 place-items-center rounded-md text-muted-foreground opacity-0 outline-hidden hover:bg-foreground/10 hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring group-hover/row:opacity-100 data-[state=open]:opacity-100 pointer-coarse:opacity-100"
                  >
                    <MoreHorizontal className="h-3.5 w-3.5" />
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-44">
                  <FavoriteDropdownItem
                    target={{
                      resource_type: "cluster",
                      cluster_id: cluster.id,
                      ref: "",
                    }}
                  />
                  <DropdownMenuItem
                    onClick={() => {
                      setEditOpen(true);
                    }}
                  >
                    <Pencil className="mr-2 h-3.5 w-3.5" />
                    Edit
                  </DropdownMenuItem>
                  <DropdownMenuItem
                    onClick={() => {
                      setDeleteOpen(true);
                    }}
                    className="text-destructive focus:text-destructive"
                  >
                    <Trash2 className="mr-2 h-3.5 w-3.5" />
                    Delete
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </div>

            {isExpanded && nodes && (
              <div>
                {nodes
                  .slice()
                  .sort((a, b) => a.name.localeCompare(b.name))
                  .map((node) => (
                    <NodeBranch
                      key={node.id}
                      node={node}
                      vms={vms ?? []}
                      clusterId={cluster.id}
                      filter={filtered}
                    />
                  ))}
              </div>
            )}
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="w-48">
          <CreateResourceContextItems clusterId={cluster.id} />
          <ContextMenuSeparator />
          <FavoriteContextItem
            target={{
              resource_type: "cluster",
              cluster_id: cluster.id,
              ref: "",
            }}
          />
          <ContextMenuSeparator />
          <ContextMenuItem
            onClick={() => {
              setEditOpen(true);
            }}
          >
            <Pencil className="mr-2 h-3.5 w-3.5" />
            Edit
          </ContextMenuItem>
          <ContextMenuItem
            onClick={() => {
              setDeleteOpen(true);
            }}
            className="text-destructive focus:text-destructive"
          >
            <Trash2 className="mr-2 h-3.5 w-3.5" />
            Delete
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

      {editOpen && (
        <EditClusterDialog
          cluster={cluster}
          open={editOpen}
          onOpenChange={setEditOpen}
        />
      )}
      {deleteOpen && (
        <DeleteClusterDialog
          cluster={cluster}
          open={deleteOpen}
          onOpenChange={setDeleteOpen}
        />
      )}
    </>
  );
}

export function InventoryTree({ filter = "" }: { filter?: string }) {
  const { t } = useTranslation("common");
  const { data: clusters, isLoading } = useClusters();
  // Where a dialog opened from the tree sends focus when its opener has gone
  // — Delete cluster takes the whole branch, dialog and all: back into the
  // tree, not to the page's main content.
  const tree = useRef<HTMLDivElement>(null);
  const treeRoot = useCallback(() => tree.current, []);
  const fallbacks = useFallbackFocus(treeRoot);

  return (
    <FallbackFocusContext value={fallbacks}>
      <div
        ref={tree}
        role="group"
        aria-label={t("datacenter")}
        tabIndex={-1}
        className="outline-none"
      >
        <TreeHeading
          action={
            <AddClusterDialog
              trigger={
                <Button
                  aria-label="Add cluster"
                  variant="ghost"
                  size="icon"
                  className="h-6 w-6 text-muted-foreground"
                >
                  <Plus className="h-3.5 w-3.5" />
                </Button>
              }
            />
          }
        >
          {/* The Clusters page: every cluster in a table, with Edit and Delete. */}
          <Link
            to="/clusters"
            title={t("allClusters")}
            className="rounded-sm hover:text-foreground hover:underline focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring"
          >
            {t("datacenter")}
          </Link>
        </TreeHeading>

        {isLoading && <TreeSkeleton />}

        {clusters?.length === 0 && <TreeNote>{t("noClustersAdded")}</TreeNote>}

        {clusters !== undefined && clusters.length > 0 && (
          <div>
            {clusters.map((cluster) => (
              <ClusterBranch
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
    </FallbackFocusContext>
  );
}
