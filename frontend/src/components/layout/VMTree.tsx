import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useLocation } from "react-router-dom";
import { Folder, FolderOpen, Pencil, Trash2, FolderPlus } from "lucide-react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";
import { StatusIcon } from "@/components/StatusIcon";
import {
  filterTree,
  type TreeFilterNode,
  type TreeFilterResult,
} from "@/lib/tree-filter";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import {
  useClusterVMs,
  useClusterNodes,
} from "@/features/clusters/api/cluster-queries";
import {
  useVMFolders,
  useDeleteVMFolder,
  useAssignVMToFolder,
} from "@/features/vms/api/folder-queries";
import { useSidebarStore } from "@/stores/sidebar-store";
import { usePermissions } from "@/hooks/usePermissions";
import { useDragAutoScroll } from "@/hooks/useDragAutoScroll";
import { CreateFolderDialog } from "@/features/vms/components/CreateFolderDialog";
import { RenameFolderDialog } from "@/features/vms/components/RenameFolderDialog";
import {
  UNASSIGNED_FOLDER_SEGMENT,
  buildFolderTree,
  type FolderNode,
} from "@/features/vms/lib/folder-tree";
import type { ClusterResponse, VMFolder, VMResponse } from "@/types/api";
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

const folderKeyOf = (folderId: string) => `vm-folder:${folderId}`;
const unassignedKeyOf = (clusterId: string) =>
  `vm-folder-unassigned:${clusterId}`;
const guestKeyOf = (vmId: string) => `vm:${vmId}`;
const guestModel = (vm: VMResponse): TreeFilterNode => ({
  id: guestKeyOf(vm.id),
  label: `${String(vm.vmid)} ${vm.name}`,
});

/** What the filter box keeps of `vms`; all of them when it is empty. */
function keptGuests(
  vms: VMResponse[],
  filter: TreeFilterResult | null,
): VMResponse[] {
  const kept =
    filter === null
      ? vms.slice()
      : vms.filter((vm) => filter.visible.has(guestKeyOf(vm.id)));
  return kept.sort((a, b) => a.vmid - b.vmid);
}

interface VMLeafProps {
  vm: VMResponse;
  clusterId: string;
  depth: number;
}

function VMLeaf({ vm, clusterId, depth }: VMLeafProps) {
  // Resolve the guest's node name from its node_id so context-menu actions that
  // need it (Migrate's source_node, Console) work from the tree. The nodes query
  // is shared/deduped across all leaves of this cluster by TanStack Query.
  const { data: nodes } = useClusterNodes(clusterId);
  const currentNode = nodes?.find((n) => n.id === vm.node_id)?.name ?? "";

  return (
    <TreeGuestRow
      vm={vm}
      clusterId={clusterId}
      currentNode={currentNode}
      depth={depth}
    />
  );
}

interface FolderRowProps {
  node: FolderNode;
  clusterId: string;
  /** 1 for a cluster's own folders, one more per level of nesting. */
  depth: number;
  /** What the filter box keeps and holds open; null when it is empty. */
  filter: TreeFilterResult | null;
  vmsByFolder: Map<string, VMResponse[]>;
  onNewSubfolder: (parent: VMFolder) => void;
  onRename: (folder: VMFolder) => void;
  onDelete: (folder: VMFolder) => void;
}

function FolderBranch({
  node,
  clusterId,
  depth,
  filter,
  vmsByFolder,
  onNewSubfolder,
  onRename,
  onDelete,
}: FolderRowProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { hasPermission } = usePermissions();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const key = folderKeyOf(node.folder.id);
  // The filter holds a folder open over a match; the stored expansion is left
  // as it was, and is what shows again when the box is cleared.
  const heldOpen = filter?.open.has(key) ?? false;
  const expanded = expandedNodes.has(key) || heldOpen;
  const assign = useAssignVMToFolder();
  const [dropActive, setDropActive] = useState(false);

  const vms = vmsByFolder.get(node.folder.id) ?? [];
  const hasChildren = node.children.length > 0 || vms.length > 0;
  const path = `/clusters/${clusterId}/folders/${node.folder.id}`;
  const active = location.pathname === path;
  // Without view:vm_folder the detail route would just show Access denied —
  // keep the pre-navigation expand/collapse behavior for such roles.
  const canOpen = hasPermission("view", "vm_folder");

  function handleDragOver(e: React.DragEvent) {
    // Filing a guest takes manage:vm_folder; without it nothing is dropped.
    if (
      hasPermission("manage", "vm_folder") &&
      e.dataTransfer.types.includes("application/x-nexara-vm")
    ) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      setDropActive(true);
    }
  }
  function handleDragLeave() {
    setDropActive(false);
  }
  function handleDrop(e: React.DragEvent) {
    e.preventDefault();
    setDropActive(false);
    const vmId = e.dataTransfer.getData("application/x-nexara-vm");
    if (!vmId) return;
    assign.mutate({ clusterId, vmId, folder_id: node.folder.id });
  }

  if (filter !== null && !filter.visible.has(key)) return null;

  return (
    <div>
      <ContextMenu modal={false}>
        {/* Every item on it takes manage:vm_folder. */}
        <ContextMenuTrigger
          asChild
          disabled={!hasPermission("manage", "vm_folder")}
        >
          <div
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
            className={treeRowClass({ active, dropActive })}
            style={treeIndent(depth)}
          >
            <button
              aria-label={`${expanded ? "Collapse" : "Expand"} folder ${node.folder.name}`}
              aria-expanded={hasChildren ? expanded : undefined}
              disabled={!hasChildren}
              onClick={(e) => {
                e.stopPropagation();
                // Held open by the filter: a toggle would change nothing on
                // screen and flip what clearing the box restores.
                if (heldOpen) return;
                if (hasChildren) toggleNode(key);
              }}
              className={TREE_ROW_TOGGLE}
            >
              <TreeChevron expanded={expanded} hidden={!hasChildren} />
            </button>
            <button
              onClick={() => {
                if (!canOpen) {
                  if (hasChildren && !heldOpen) toggleNode(key);
                  return;
                }
                if (hasChildren) expandNode(key);
                void navigate(path);
              }}
              className={TREE_ROW_MAIN}
            >
              {expanded ? (
                <FolderOpen className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              ) : (
                <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              )}
              <span className="truncate">{node.folder.name}</span>
              {vms.length > 0 && <TreeRowAside>{vms.length}</TreeRowAside>}
            </button>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="w-44">
          <ContextMenuItem
            onClick={() => {
              onNewSubfolder(node.folder);
            }}
          >
            <FolderPlus className="mr-2 h-3.5 w-3.5" />
            New subfolder
          </ContextMenuItem>
          <ContextMenuItem
            onClick={() => {
              onRename(node.folder);
            }}
          >
            <Pencil className="mr-2 h-3.5 w-3.5" />
            Rename
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            onClick={() => {
              onDelete(node.folder);
            }}
            className="text-destructive focus:text-destructive"
          >
            <Trash2 className="mr-2 h-3.5 w-3.5" />
            Delete
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

      {expanded && (
        <>
          {node.children.map((child) => (
            <FolderBranch
              key={child.folder.id}
              node={child}
              clusterId={clusterId}
              depth={depth + 1}
              filter={filter}
              vmsByFolder={vmsByFolder}
              onNewSubfolder={onNewSubfolder}
              onRename={onRename}
              onDelete={onDelete}
            />
          ))}
          {keptGuests(vms, filter).map((vm) => (
            <DraggableVM
              key={vm.id}
              vm={vm}
              clusterId={clusterId}
              depth={depth + 1}
            />
          ))}
        </>
      )}
    </div>
  );
}

function DraggableVM({
  vm,
  clusterId,
  depth,
}: {
  vm: VMResponse;
  clusterId: string;
  depth: number;
}) {
  // The only drop target is a folder, which files it (manage:vm_folder).
  const canFile = usePermissions().hasPermission("manage", "vm_folder");
  function handleDragStart(e: React.DragEvent) {
    e.dataTransfer.setData("application/x-nexara-vm", vm.id);
    e.dataTransfer.effectAllowed = "move";
  }
  return (
    <div draggable={canFile} onDragStart={handleDragStart}>
      <VMLeaf vm={vm} clusterId={clusterId} depth={depth} />
    </div>
  );
}

interface UnassignedBranchProps {
  vms: VMResponse[];
  clusterId: string;
  filter: TreeFilterResult | null;
}

function UnassignedBranch({ vms, clusterId, filter }: UnassignedBranchProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { hasPermission } = usePermissions();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const assign = useAssignVMToFolder();
  const [dropActive, setDropActive] = useState(false);
  const key = unassignedKeyOf(clusterId);
  const heldOpen = filter?.open.has(key) ?? false;
  const expanded = expandedNodes.has(key) || heldOpen;
  const path = `/clusters/${clusterId}/folders/${UNASSIGNED_FOLDER_SEGMENT}`;
  const active = location.pathname === path;
  const canOpen = hasPermission("view", "vm_folder");

  function handleDragOver(e: React.DragEvent) {
    // Filing a guest takes manage:vm_folder; without it nothing is dropped.
    if (
      hasPermission("manage", "vm_folder") &&
      e.dataTransfer.types.includes("application/x-nexara-vm")
    ) {
      e.preventDefault();
      e.dataTransfer.dropEffect = "move";
      setDropActive(true);
    }
  }
  function handleDragLeave() {
    setDropActive(false);
  }
  function handleDrop(e: React.DragEvent) {
    e.preventDefault();
    setDropActive(false);
    const vmId = e.dataTransfer.getData("application/x-nexara-vm");
    if (!vmId) return;
    assign.mutate({ clusterId, vmId, folder_id: null });
  }

  if (filter !== null && !filter.visible.has(key)) return null;

  return (
    <div>
      <div
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
        className={treeRowClass({ active, dropActive })}
        style={treeIndent(1)}
      >
        <button
          aria-label={`${expanded ? "Collapse" : "Expand"} Discovered`}
          aria-expanded={vms.length > 0 ? expanded : undefined}
          disabled={vms.length === 0}
          onClick={() => {
            // Held open by the filter: see FolderBranch.
            if (vms.length > 0 && !heldOpen) toggleNode(key);
          }}
          className={TREE_ROW_TOGGLE}
        >
          <TreeChevron expanded={expanded} hidden={vms.length === 0} />
        </button>
        <button
          onClick={() => {
            if (!canOpen) {
              if (vms.length > 0 && !heldOpen) toggleNode(key);
              return;
            }
            if (vms.length > 0) expandNode(key);
            void navigate(path);
          }}
          className={TREE_ROW_MAIN}
        >
          <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground/60" />
          <span className="truncate text-muted-foreground">Discovered</span>
          <TreeRowAside>{vms.length}</TreeRowAside>
        </button>
      </div>

      {expanded &&
        keptGuests(vms, filter).map((vm) => (
          <DraggableVM key={vm.id} vm={vm} clusterId={clusterId} depth={2} />
        ))}
    </div>
  );
}

interface VMClusterBranchProps {
  cluster: ClusterResponse;
  /** The filter box's text, normalised; empty when nothing is typed. */
  filter: string;
  openCreateAt: (
    parentId: string | null,
    label: string,
    clusterId: string,
  ) => void;
  openRename: (folder: VMFolder, clusterId: string) => void;
  openDelete: (folder: VMFolder, clusterId: string) => void;
}

function VMClusterBranch({
  cluster,
  filter,
  openCreateAt,
  openRename,
  openDelete,
}: VMClusterBranchProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const { hasPermission } = usePermissions();
  const clusterKey = `vm-cluster:${cluster.id}`;
  const isExpanded = expandedNodes.has(clusterKey);
  const isActive = location.pathname === `/clusters/${cluster.id}`;

  const { data: vms } = useClusterVMs(isExpanded ? cluster.id : "");
  const { data: folderData } = useVMFolders(isExpanded ? cluster.id : "");

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

  const tree = useMemo(
    () => buildFolderTree(folderData?.folders ?? []),
    [folderData],
  );

  const { vmsByFolder, unassigned } = useMemo(() => {
    const memberFolder = new Map<string, string>();
    for (const m of folderData?.memberships ?? []) {
      memberFolder.set(m.vm_id, m.folder_id);
    }
    const byFolder = new Map<string, VMResponse[]>();
    const orphans: VMResponse[] = [];
    for (const vm of vms ?? []) {
      const fid = memberFolder.get(vm.id);
      if (fid && (folderData?.folders.some((f) => f.id === fid) ?? false)) {
        const list = byFolder.get(fid) ?? [];
        list.push(vm);
        byFolder.set(fid, list);
      } else {
        orphans.push(vm);
      }
    }
    return { vmsByFolder: byFolder, unassigned: orphans };
  }, [vms, folderData]);

  // The rows under this cluster, as the filter box sees them. Only what has
  // loaded: a collapsed cluster has no rows to look through.
  const filtered = useMemo(() => {
    if (filter === "" || !isExpanded) return null;
    if (vms === undefined || folderData === undefined) return null;
    const folderModel = (node: FolderNode): TreeFilterNode => ({
      id: folderKeyOf(node.folder.id),
      label: node.folder.name,
      children: [
        ...node.children.map(folderModel),
        ...(vmsByFolder.get(node.folder.id) ?? []).map(guestModel),
      ],
    });
    const model: TreeFilterNode = {
      id: clusterKey,
      label: cluster.name,
      children: [
        ...tree.map(folderModel),
        {
          id: unassignedKeyOf(cluster.id),
          label: "Discovered",
          children: unassigned.map(guestModel),
        },
      ],
    };
    return filterTree([model], filter);
  }, [
    filter,
    isExpanded,
    vms,
    folderData,
    tree,
    vmsByFolder,
    unassigned,
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
    <ContextMenu modal={false}>
      {/* New folder, its one item, takes manage:vm_folder. */}
      <ContextMenuTrigger
        asChild
        disabled={!hasPermission("manage", "vm_folder")}
      >
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
            </button>
          </div>

          {isExpanded && (
            <div>
              {tree.map((node) => (
                <FolderBranch
                  key={node.folder.id}
                  node={node}
                  clusterId={cluster.id}
                  depth={1}
                  filter={filtered}
                  vmsByFolder={vmsByFolder}
                  onNewSubfolder={(parent) => {
                    openCreateAt(parent.id, parent.name, cluster.id);
                  }}
                  onRename={(folder) => {
                    openRename(folder, cluster.id);
                  }}
                  onDelete={(folder) => {
                    openDelete(folder, cluster.id);
                  }}
                />
              ))}
              <UnassignedBranch
                vms={unassigned}
                clusterId={cluster.id}
                filter={filtered}
              />
            </div>
          )}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent className="w-44">
        <ContextMenuItem
          onClick={() => {
            openCreateAt(null, cluster.name, cluster.id);
          }}
        >
          <FolderPlus className="mr-2 h-3.5 w-3.5" />
          New folder
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

export function VMTree({ filter = "" }: { filter?: string }) {
  const { t } = useTranslation("common");
  const { data: clusters, isLoading } = useClusters();
  const deleteFolder = useDeleteVMFolder();

  // Auto-scroll the sidebar tree container while dragging a VM near its
  // top/bottom edges. The browser's native drag autoscroll has a tiny
  // (~10 px) hot zone and a fixed, fast speed, so users often can't reach
  // a folder that is just out of view.
  useDragAutoScroll({ selector: "[data-tree-scroller]" });

  const [createOpen, setCreateOpen] = useState(false);
  const [createCtx, setCreateCtx] = useState<{
    parentId: string | null;
    label: string;
    clusterId: string;
  } | null>(null);

  const [renameOpen, setRenameOpen] = useState(false);
  const [renameCtx, setRenameCtx] = useState<{
    folder: VMFolder;
    clusterId: string;
  } | null>(null);

  function openCreateAt(
    parentId: string | null,
    label: string,
    clusterId: string,
  ) {
    setCreateCtx({ parentId, label, clusterId });
    setCreateOpen(true);
  }
  function openRename(folder: VMFolder, clusterId: string) {
    setRenameCtx({ folder, clusterId });
    setRenameOpen(true);
  }
  function openDelete(folder: VMFolder, clusterId: string) {
    if (
      !confirm(
        `Delete folder "${folder.name}"?\n\nVMs inside will fall back to "Discovered". Sub-folders will be deleted.`,
      )
    ) {
      return;
    }
    deleteFolder.mutate({ clusterId, folderId: folder.id });
  }

  return (
    <div>
      <TreeHeading>
        {t("vms", { defaultValue: "VMs and templates" })}
      </TreeHeading>

      {isLoading && <TreeSkeleton />}

      {clusters?.length === 0 && <TreeNote>No clusters</TreeNote>}

      {clusters !== undefined && clusters.length > 0 && (
        <div>
          {clusters.map((cluster) => (
            <VMClusterBranch
              key={cluster.id}
              cluster={cluster}
              filter={filter}
              openCreateAt={openCreateAt}
              openRename={openRename}
              openDelete={openDelete}
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

      {createCtx && (
        <CreateFolderDialog
          open={createOpen}
          onOpenChange={(o) => {
            setCreateOpen(o);
            if (!o) setCreateCtx(null);
          }}
          clusterId={createCtx.clusterId}
          parentId={createCtx.parentId}
          parentLabel={createCtx.label}
        />
      )}

      {renameCtx && (
        <RenameFolderDialog
          open={renameOpen}
          onOpenChange={(o) => {
            setRenameOpen(o);
            if (!o) setRenameCtx(null);
          }}
          clusterId={renameCtx.clusterId}
          folderId={renameCtx.folder.id}
          currentName={renameCtx.folder.name}
        />
      )}
    </div>
  );
}
