import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate, useLocation } from "react-router-dom";
import {
  ChevronRight,
  Server,
  Monitor,
  Container,
  FileBox,
  Folder,
  FolderOpen,
  Pencil,
  Trash2,
  FolderPlus,
} from "lucide-react";
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from "@/components/ui/context-menu";
import { cn } from "@/lib/utils";
import { StatusIcon } from "@/components/StatusIcon";
import { OSIcon } from "@/components/OSIcon";
import { classifyOS } from "@/lib/os-classify";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useClusterVMs, useClusterNodes } from "@/features/clusters/api/cluster-queries";
import {
  useVMFolders,
  useDeleteVMFolder,
  useAssignVMToFolder,
} from "@/features/vms/api/folder-queries";
import { useSidebarStore } from "@/stores/sidebar-store";
import { useDragAutoScroll } from "@/hooks/useDragAutoScroll";
import { VMContextMenu } from "@/features/vms/components/VMContextMenu";
import { VMContextDialogs } from "@/features/vms/components/VMContextDialogs";
import { CreateFolderDialog } from "@/features/vms/components/CreateFolderDialog";
import { RenameFolderDialog } from "@/features/vms/components/RenameFolderDialog";
import { buildFolderTree, type FolderNode } from "@/features/vms/lib/folder-tree";
import type { ClusterResponse, VMFolder, VMResponse } from "@/types/api";

function VMIcon({ type, template }: { type: string; template?: boolean }) {
  if (template) {
    return <FileBox className="h-3.5 w-3.5 shrink-0 text-amber-600 dark:text-amber-400" />;
  }
  if (type === "lxc") {
    return <Container className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />;
  }
  return <Monitor className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />;
}

interface VMLeafProps {
  vm: VMResponse;
  clusterId: string;
}

function VMLeaf({ vm, clusterId }: VMLeafProps) {
  const navigate = useNavigate();
  const location = useLocation();
  // Resolve the guest's node name from its node_id so context-menu actions that
  // need it (Migrate's source_node, Console) work from the tree. The nodes query
  // is shared/deduped across all leaves of this cluster by TanStack Query.
  const { data: nodes } = useClusterNodes(clusterId);
  const kind = vm.type === "lxc" ? "lxc" : "qemu";
  const path = `/inventory/${kind}/${clusterId}/${vm.id}`;
  const active = location.pathname === path;
  const currentNode = nodes?.find((n) => n.id === vm.node_id)?.name ?? "";

  return (
    <VMContextMenu
      target={{
        clusterId,
        resourceId: vm.id,
        vmid: vm.vmid,
        name: vm.name,
        kind: vm.type === "lxc" ? "ct" : "vm",
        status: vm.status,
        currentNode,
        template: vm.template,
      }}
    >
      <button
        onClick={() => { void navigate(path); }}
        className={cn(
          "flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
          active && "bg-primary/10 text-foreground",
        )}
      >
        <StatusIcon status={vm.status} />
        <VMIcon type={vm.type} template={vm.template} />
        {(classifyOS(vm.ostype) !== "unknown" ||
          classifyOS(vm.config_ostype) !== "unknown") && (
          <OSIcon ostype={vm.ostype} configOstype={vm.config_ostype} />
        )}
        <span
          className={cn(
            "truncate",
            vm.template && "text-amber-700 dark:text-amber-400",
          )}
        >
          {vm.vmid} {vm.name}
        </span>
      </button>
    </VMContextMenu>
  );
}

interface FolderRowProps {
  node: FolderNode;
  clusterId: string;
  vmsByFolder: Map<string, VMResponse[]>;
  onNewSubfolder: (parent: VMFolder) => void;
  onRename: (folder: VMFolder) => void;
  onDelete: (folder: VMFolder) => void;
}

function FolderBranch({
  node,
  clusterId,
  vmsByFolder,
  onNewSubfolder,
  onRename,
  onDelete,
}: FolderRowProps) {
  const { expandedNodes, toggleNode } = useSidebarStore();
  const key = `vm-folder:${node.folder.id}`;
  const expanded = expandedNodes.has(key);
  const assign = useAssignVMToFolder();
  const [dropActive, setDropActive] = useState(false);

  const vms = vmsByFolder.get(node.folder.id) ?? [];
  const hasChildren = node.children.length > 0 || vms.length > 0;

  function handleDragOver(e: React.DragEvent) {
    if (e.dataTransfer.types.includes("application/x-nexara-vm")) {
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

  return (
    <div className="border-l border-border pl-3 ml-3">
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
            className={cn(
              "group flex items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
              dropActive && "bg-accent/70 ring-1 ring-primary",
            )}
          >
            <button
              onClick={(e) => {
                e.stopPropagation();
                if (hasChildren) toggleNode(key);
              }}
              className="shrink-0"
            >
              <ChevronRight
                className={cn(
                  "h-3 w-3 transition-transform",
                  !hasChildren && "invisible",
                  expanded && "rotate-90",
                )}
              />
            </button>
            <button
              onClick={() => {
                if (hasChildren) toggleNode(key);
              }}
              className="flex min-w-0 flex-1 items-center gap-1.5"
            >
              {expanded ? (
                <FolderOpen className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              ) : (
                <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              )}
              <span className="truncate">{node.folder.name}</span>
            </button>
          </div>
        </ContextMenuTrigger>
        <ContextMenuContent className="w-44">
          <ContextMenuItem onClick={() => { onNewSubfolder(node.folder); }}>
            <FolderPlus className="mr-2 h-3.5 w-3.5" />
            New subfolder
          </ContextMenuItem>
          <ContextMenuItem onClick={() => { onRename(node.folder); }}>
            <Pencil className="mr-2 h-3.5 w-3.5" />
            Rename
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem
            onClick={() => { onDelete(node.folder); }}
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
              vmsByFolder={vmsByFolder}
              onNewSubfolder={onNewSubfolder}
              onRename={onRename}
              onDelete={onDelete}
            />
          ))}
          {vms.length > 0 && (
            <div className="border-l border-border pl-3 ml-3">
              {vms
                .slice()
                .sort((a, b) => a.vmid - b.vmid)
                .map((vm) => (
                  <DraggableVM key={vm.id} vm={vm} clusterId={clusterId} />
                ))}
            </div>
          )}
        </>
      )}
    </div>
  );
}

function DraggableVM({ vm, clusterId }: { vm: VMResponse; clusterId: string }) {
  function handleDragStart(e: React.DragEvent) {
    e.dataTransfer.setData("application/x-nexara-vm", vm.id);
    e.dataTransfer.effectAllowed = "move";
  }
  return (
    <div draggable onDragStart={handleDragStart}>
      <VMLeaf vm={vm} clusterId={clusterId} />
    </div>
  );
}

interface UnassignedBranchProps {
  vms: VMResponse[];
  clusterId: string;
}

function UnassignedBranch({ vms, clusterId }: UnassignedBranchProps) {
  const { expandedNodes, toggleNode } = useSidebarStore();
  const assign = useAssignVMToFolder();
  const [dropActive, setDropActive] = useState(false);
  const key = `vm-folder-unassigned:${clusterId}`;
  const expanded = expandedNodes.has(key);

  function handleDragOver(e: React.DragEvent) {
    if (e.dataTransfer.types.includes("application/x-nexara-vm")) {
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

  return (
    <div className="border-l border-border pl-3 ml-3">
      <div
        onDragOver={handleDragOver}
        onDragLeave={handleDragLeave}
        onDrop={handleDrop}
        className={cn(
          "flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
          dropActive && "bg-accent/70 ring-1 ring-primary",
        )}
      >
        <button
          onClick={() => { toggleNode(key); }}
          className="shrink-0"
        >
          <ChevronRight
            className={cn(
              "h-3 w-3 transition-transform",
              vms.length === 0 && "invisible",
              expanded && "rotate-90",
            )}
          />
        </button>
        <button
          onClick={() => { if (vms.length > 0) toggleNode(key); }}
          className="flex min-w-0 flex-1 items-center gap-1.5"
        >
          <Folder className="h-3.5 w-3.5 shrink-0 text-muted-foreground/60" />
          <span className="truncate italic text-muted-foreground">Discovered</span>
          <span className="ml-auto text-[10px] text-muted-foreground/70">
            {vms.length}
          </span>
        </button>
      </div>

      {expanded && vms.length > 0 && (
        <div className="border-l border-border pl-3 ml-3">
          {vms
            .slice()
            .sort((a, b) => a.vmid - b.vmid)
            .map((vm) => (
              <DraggableVM key={vm.id} vm={vm} clusterId={clusterId} />
            ))}
        </div>
      )}
    </div>
  );
}

interface VMClusterBranchProps {
  cluster: ClusterResponse;
  openCreateAt: (parentId: string | null, label: string, clusterId: string) => void;
  openRename: (folder: VMFolder, clusterId: string) => void;
  openDelete: (folder: VMFolder, clusterId: string) => void;
}

function VMClusterBranch({
  cluster,
  openCreateAt,
  openRename,
  openDelete,
}: VMClusterBranchProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const clusterKey = `vm-cluster:${cluster.id}`;
  const isExpanded = expandedNodes.has(clusterKey);
  const isActive = location.pathname === `/clusters/${cluster.id}`;

  const { data: vms } = useClusterVMs(isExpanded ? cluster.id : "");
  const { data: folderData } = useVMFolders(isExpanded ? cluster.id : "");

  useEffect(() => {
    if (
      location.pathname.startsWith(`/clusters/${cluster.id}/`) ||
      location.pathname.match(new RegExp(`^/inventory/(qemu|lxc)/${cluster.id}/`))
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

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div>
          <div
            className={cn(
              "group flex items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
              isActive && "bg-primary/10 text-foreground",
            )}
          >
            <button
              onClick={() => { toggleNode(clusterKey); }}
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
              onClick={() => { void navigate(`/clusters/${cluster.id}`); }}
              className="flex min-w-0 flex-1 items-center gap-1.5"
            >
              <StatusIcon
                status={cluster.status === "degraded" ? "degraded" : cluster.status}
              />
              <Server className="h-3.5 w-3.5 shrink-0 text-primary" />
              <span className="truncate font-medium">{cluster.name}</span>
            </button>
          </div>

          {isExpanded && (
            <div>
              {tree.map((node) => (
                <FolderBranch
                  key={node.folder.id}
                  node={node}
                  clusterId={cluster.id}
                  vmsByFolder={vmsByFolder}
                  onNewSubfolder={(parent) => {
                    openCreateAt(parent.id, parent.name, cluster.id);
                  }}
                  onRename={(folder) => { openRename(folder, cluster.id); }}
                  onDelete={(folder) => { openDelete(folder, cluster.id); }}
                />
              ))}
              <UnassignedBranch vms={unassigned} clusterId={cluster.id} />
            </div>
          )}
        </div>
      </ContextMenuTrigger>
      <ContextMenuContent className="w-44">
        <ContextMenuItem
          onClick={() => { openCreateAt(null, cluster.name, cluster.id); }}
        >
          <FolderPlus className="mr-2 h-3.5 w-3.5" />
          New folder
        </ContextMenuItem>
      </ContextMenuContent>
    </ContextMenu>
  );
}

export function VMTree() {
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

  function openCreateAt(parentId: string | null, label: string, clusterId: string) {
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
    <div className="space-y-1 py-1">
      <div className="flex items-center justify-between px-2 pb-1">
        <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
          {t("vms", { defaultValue: "VMs & Templates" })}
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
        <p className="px-2 text-xs text-muted-foreground">No clusters</p>
      )}

      {clusters?.map((cluster) => (
        <VMClusterBranch
          key={cluster.id}
          cluster={cluster}
          openCreateAt={openCreateAt}
          openRename={openRename}
          openDelete={openDelete}
        />
      ))}

      <VMContextDialogs />

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
