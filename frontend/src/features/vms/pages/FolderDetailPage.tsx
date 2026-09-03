import { useMemo, useState } from "react";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import {
  ArrowLeft,
  FolderOpen,
  FolderPlus,
  Pencil,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DetailChip } from "@/components/DetailChip";
import { usePermissions } from "@/hooks/usePermissions";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useClusterVMs } from "@/features/clusters/api/cluster-queries";
import { useInventoryData } from "@/features/inventory/api/inventory-queries";
import { ResourceTable } from "@/features/inventory/components/ResourceTable";
import { InventoryUnavailableNote } from "@/features/inventory/components/InventoryUnavailableNote";
import {
  useDeleteVMFolder,
  useVMFolders,
} from "@/features/vms/api/folder-queries";
import {
  UNASSIGNED_FOLDER_SEGMENT,
  collectSubtreeFolderIds,
} from "@/features/vms/lib/folder-tree";
import { CreateFolderDialog } from "@/features/vms/components/CreateFolderDialog";
import { RenameFolderDialog } from "@/features/vms/components/RenameFolderDialog";
import {
  FolderSummaryTab,
  type ChildFolderSummary,
} from "@/features/vms/components/FolderSummaryTab";
import {
  FolderTasksTab,
  type FolderVMLink,
} from "@/features/vms/components/FolderTasksTab";
import { FolderAlertsTab } from "@/features/vms/components/FolderAlertsTab";

export function FolderDetailPage() {
  const { clusterId = "", folderId = "" } = useParams();
  const [searchParams] = useSearchParams();
  const tabParam = searchParams.get("tab") ?? "";
  const navigate = useNavigate();
  const { hasPermission } = usePermissions();

  const isUnassigned = folderId === UNASSIGNED_FOLDER_SEGMENT;
  const { data: clusters } = useClusters();
  const { data: folderData, isLoading: foldersLoading } =
    useVMFolders(clusterId);
  const { data: vms, isLoading: vmsLoading } = useClusterVMs(clusterId);
  const { rows: inventoryRows, failedClusterIds } = useInventoryData();
  const deleteFolder = useDeleteVMFolder();

  const [createOpen, setCreateOpen] = useState(false);
  const [renameOpen, setRenameOpen] = useState(false);

  const cluster = clusters?.find((c) => c.id === clusterId);
  const folder = isUnassigned
    ? null
    : (folderData?.folders.find((f) => f.id === folderId) ?? null);
  const parent = folder?.parent_id
    ? (folderData?.folders.find((f) => f.id === folder.parent_id) ?? null)
    : null;

  const subtreeIds = useMemo(
    () =>
      isUnassigned || !folderData
        ? new Set<string>()
        : collectSubtreeFolderIds(folderData.folders, folderId),
    [folderData, folderId, isUnassigned],
  );

  // The VM set shown by every tab: subtree memberships for a real folder,
  // membership-less VMs for "Discovered" (mirrors the sidebar VMTree, incl.
  // treating memberships of since-deleted folders as unassigned).
  const folderVmIdSet = useMemo(() => {
    const set = new Set<string>();
    if (!folderData) return set;
    if (isUnassigned) {
      const known = new Set(folderData.folders.map((f) => f.id));
      const assigned = new Set(
        folderData.memberships
          .filter((m) => known.has(m.folder_id))
          .map((m) => m.vm_id),
      );
      for (const vm of vms ?? []) {
        if (!assigned.has(vm.id)) set.add(vm.id);
      }
    } else {
      for (const m of folderData.memberships) {
        if (subtreeIds.has(m.folder_id)) set.add(m.vm_id);
      }
    }
    return set;
  }, [folderData, vms, subtreeIds, isUnassigned]);

  const folderVMs = useMemo(
    () => (vms ?? []).filter((vm) => folderVmIdSet.has(vm.id)),
    [vms, folderVmIdSet],
  );

  const vmids = useMemo(
    () => new Set(folderVMs.map((vm) => vm.vmid)),
    [folderVMs],
  );

  const vmLinkByVmid = useMemo(() => {
    const map = new Map<number, FolderVMLink>();
    for (const vm of folderVMs) {
      map.set(vm.vmid, {
        name: vm.name,
        path: `/inventory/${vm.type === "lxc" ? "lxc" : "qemu"}/${clusterId}/${vm.id}`,
      });
    }
    return map;
  }, [folderVMs, clusterId]);

  const tableRows = useMemo(
    () =>
      inventoryRows.filter(
        (row) =>
          row.clusterId === clusterId &&
          row.type !== "node" &&
          folderVmIdSet.has(row.id),
      ),
    [inventoryRows, clusterId, folderVmIdSet],
  );

  const childFolders = useMemo<ChildFolderSummary[]>(() => {
    if (!folderData || isUnassigned) return [];
    const vmById = new Map((vms ?? []).map((vm) => [vm.id, vm]));
    return folderData.folders
      .filter((f) => f.parent_id === folderId)
      .sort((a, b) => a.name.localeCompare(b.name))
      .map((child) => {
        const ids = collectSubtreeFolderIds(folderData.folders, child.id);
        let vmCount = 0;
        for (const m of folderData.memberships) {
          if (!ids.has(m.folder_id)) continue;
          const vm = vmById.get(m.vm_id);
          if (vm && !vm.template) vmCount++;
        }
        return { folder: child, vmCount };
      });
  }, [folderData, vms, folderId, isUnassigned]);

  const guestCount = folderVMs.filter((vm) => !vm.template).length;
  const templateCount = folderVMs.length - guestCount;
  const canManage = hasPermission("manage", "vm_folder");
  const canViewTasks = hasPermission("view", "task");
  const canViewAlerts = hasPermission("view", "alert");
  const backPath = parent
    ? `/clusters/${clusterId}/folders/${parent.id}`
    : `/clusters/${clusterId}`;

  if (foldersLoading || vmsLoading) {
    return (
      <div className="space-y-4 p-6">
        <div className="flex items-center gap-3">
          <Skeleton className="h-11 w-11 rounded-xl" />
          <Skeleton className="h-7 w-48" />
        </div>
        <Skeleton className="h-48 w-full" />
      </div>
    );
  }

  if (!isUnassigned && !folder) {
    return (
      <div className="p-6">
        <p className="text-destructive">Folder not found</p>
        <Button variant="link" asChild className="mt-2 px-0">
          <Link to={`/clusters/${clusterId}`}>Back to Cluster</Link>
        </Button>
      </div>
    );
  }

  const name = folder?.name ?? "Discovered";

  function handleDelete() {
    if (!folder) return;
    if (
      !confirm(
        `Delete folder "${folder.name}"?\n\nVMs inside will fall back to "Discovered". Sub-folders will be deleted.`,
      )
    ) {
      return;
    }
    deleteFolder.mutate(
      { clusterId, folderId: folder.id },
      {
        onSuccess: () => {
          void navigate(backPath);
        },
      },
    );
  }

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex min-w-0 items-start gap-3">
          <Button
            aria-label="Back"
            variant="ghost"
            size="sm"
            asChild
            className="-ml-2 mt-1.5"
          >
            <Link to={backPath}>
              <ArrowLeft className="h-4 w-4" />
            </Link>
          </Button>
          <div className="mt-0.5 flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-violet-500/10">
            <FolderOpen className="h-6 w-6 text-violet-500" />
          </div>
          <div className="min-w-0 space-y-1.5">
            <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
              <h1 className="min-w-0 [overflow-wrap:anywhere] text-2xl font-bold tracking-tight">
                {name}
              </h1>
            </div>
            <div className="flex flex-wrap items-center gap-1.5">
              <DetailChip>
                {isUnassigned ? "Auto-discovered VMs" : "VM folder"}
              </DetailChip>
              <Link to={`/clusters/${clusterId}`}>
                <DetailChip className="transition-colors hover:text-foreground">
                  {cluster?.name ?? clusterId.slice(0, 8)}
                </DetailChip>
              </Link>
              {parent && (
                <Link to={`/clusters/${clusterId}/folders/${parent.id}`}>
                  <DetailChip className="transition-colors hover:text-foreground">
                    in {parent.name}
                  </DetailChip>
                </Link>
              )}
              <DetailChip>
                {guestCount} VM{guestCount === 1 ? "" : "s"}
              </DetailChip>
              {templateCount > 0 && (
                <DetailChip>
                  {templateCount} template{templateCount === 1 ? "" : "s"}
                </DetailChip>
              )}
              {childFolders.length > 0 && (
                <DetailChip>
                  {childFolders.length} subfolder
                  {childFolders.length === 1 ? "" : "s"}
                </DetailChip>
              )}
            </div>
          </div>
        </div>
        {!isUnassigned && canManage && (
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              className="gap-1.5"
              onClick={() => {
                setCreateOpen(true);
              }}
            >
              <FolderPlus className="h-4 w-4" />
              New subfolder
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1.5"
              onClick={() => {
                setRenameOpen(true);
              }}
            >
              <Pencil className="h-4 w-4" />
              Rename
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="gap-1.5 text-destructive hover:text-destructive"
              onClick={handleDelete}
              disabled={deleteFolder.isPending}
            >
              <Trash2 className="h-4 w-4" />
              Delete
            </Button>
          </div>
        )}
      </div>

      {/* Tabbed content. Uncontrolled (defaultValue only): a ?tab= deep link
          picks the initial tab, and AppShell keys the route tree by pathname,
          so navigating to another folder remounts onto Summary. A bogus or
          permission-hidden ?tab value falls back to Summary instead of
          leaving Radix with a blank panel. */}
      <Tabs
        defaultValue={
          [
            "summary",
            "vms",
            ...(canViewTasks ? ["tasks"] : []),
            ...(canViewAlerts ? ["alerts"] : []),
          ].includes(tabParam)
            ? tabParam
            : "summary"
        }
      >
        <TabsList>
          <TabsTrigger value="summary">Summary</TabsTrigger>
          <TabsTrigger value="vms">VMs ({folderVMs.length})</TabsTrigger>
          {canViewTasks && <TabsTrigger value="tasks">Tasks</TabsTrigger>}
          {canViewAlerts && <TabsTrigger value="alerts">Alerts</TabsTrigger>}
        </TabsList>

        <TabsContent value="summary" className="mt-4">
          <FolderSummaryTab
            clusterId={clusterId}
            vms={folderVMs}
            childFolders={childFolders}
            isUnassigned={isUnassigned}
          />
        </TabsContent>

        <TabsContent value="vms" className="mt-4">
          {failedClusterIds.includes(clusterId) && <InventoryUnavailableNote />}
          <ResourceTable data={tableRows} />
        </TabsContent>

        {canViewTasks && (
          <TabsContent value="tasks" className="mt-4">
            <FolderTasksTab
              clusterId={clusterId}
              clusterName={cluster?.name ?? clusterId.slice(0, 8)}
              vmids={vmids}
              vmLinkByVmid={vmLinkByVmid}
            />
          </TabsContent>
        )}

        {canViewAlerts && (
          <TabsContent value="alerts" className="mt-4">
            <FolderAlertsTab clusterId={clusterId} vmids={vmids} />
          </TabsContent>
        )}
      </Tabs>

      {folder && (
        <>
          <CreateFolderDialog
            open={createOpen}
            onOpenChange={setCreateOpen}
            clusterId={clusterId}
            parentId={folder.id}
            parentLabel={folder.name}
          />
          <RenameFolderDialog
            open={renameOpen}
            onOpenChange={setRenameOpen}
            clusterId={clusterId}
            folderId={folder.id}
            currentName={folder.name}
          />
        </>
      )}
    </div>
  );
}
