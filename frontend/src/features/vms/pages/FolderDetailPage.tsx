import { useMemo, useState } from "react";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import {
  FolderOpen,
  FolderPlus,
  FolderSearch,
  MoreHorizontal,
  Pencil,
  Trash2,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  DetailHeader,
  Fact,
  StatusFact,
  type Crumb,
} from "@/components/page/DetailHeader";
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
  folderPageTab,
} from "@/features/vms/lib/folder-tree";
import { CreateFolderDialog } from "@/features/vms/components/CreateFolderDialog";
import { RenameFolderDialog } from "@/features/vms/components/RenameFolderDialog";
import {
  FolderSummaryTab,
  type ChildFolderSummary,
} from "@/features/vms/components/FolderSummaryTab";
import { FolderHistoryTab } from "@/features/vms/components/FolderHistoryTab";
import { FolderAlertsTab } from "@/features/vms/components/FolderAlertsTab";
import { guestLinksByVmid } from "@/features/vms/lib/guest-links";

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

  // The folders above this one, outermost first, for the breadcrumb trail.
  // The seen set stops a parent loop in bad data from spinning forever.
  const ancestors = useMemo(() => {
    const chain: { id: string; name: string }[] = [];
    const byId = new Map((folderData?.folders ?? []).map((f) => [f.id, f]));
    const seen = new Set<string>([folderId]);
    let next = folder?.parent_id ?? null;
    while (next !== null && !seen.has(next)) {
      seen.add(next);
      const found = byId.get(next);
      if (!found) break;
      chain.unshift({ id: found.id, name: found.name });
      next = found.parent_id;
    }
    return chain;
  }, [folderData, folder, folderId]);

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

  // Every guest of the cluster, not only the folder's: its History also names
  // the guests that have left it.
  const guestLinks = useMemo(
    () => guestLinksByVmid(vms, clusterId),
    [vms, clusterId],
  );

  const folderNames = useMemo(
    () => new Map((folderData?.folders ?? []).map((f) => [f.id, f.name])),
    [folderData],
  );

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
  const runningCount = folderVMs.filter(
    (vm) => !vm.template && vm.status === "running",
  ).length;
  const canManage = hasPermission("manage", "vm_folder");
  // History is read from the audit log, and its rows open Proxmox task logs:
  // without both grants the tab would be a table of refusals.
  const canHistory =
    hasPermission("view", "audit") && hasPermission("view", "task");
  const canViewAlerts = hasPermission("view", "alert");
  const backPath = parent
    ? `/clusters/${clusterId}/folders/${parent.id}`
    : `/clusters/${clusterId}`;

  if (foldersLoading || vmsLoading) {
    return (
      <div className="space-y-5">
        <div className="flex items-center gap-3.5">
          <Skeleton className="h-11 w-11 rounded-[10px]" />
          <Skeleton className="h-8 w-48" />
        </div>
        <Skeleton className="h-28 w-full rounded-[10px]" />
        <Skeleton className="h-64 w-full rounded-[10px]" />
      </div>
    );
  }

  if (!isUnassigned && !folder) {
    return (
      <div>
        <p className="text-destructive">Folder not found</p>
        <Button variant="link" asChild className="mt-2 px-0">
          <Link to={`/clusters/${clusterId}`}>Back to cluster</Link>
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

  const crumbs: Crumb[] = [
    { label: "Inventory", to: "/inventory" },
    {
      label: cluster?.name ?? "Cluster",
      to: `/clusters/${clusterId}`,
    },
    ...ancestors.map((a) => ({
      label: a.name,
      to: `/clusters/${clusterId}/folders/${a.id}`,
    })),
    { label: name },
  ];

  return (
    <div className="space-y-5">
      <DetailHeader
        crumbs={crumbs}
        icon={isUnassigned ? <FolderSearch /> : <FolderOpen />}
        title={name}
        facts={
          <>
            {guestCount === 0 ? (
              <StatusFact status="unknown" label="No guests" />
            ) : (
              <StatusFact
                status={runningCount > 0 ? "running" : "stopped"}
                label={
                  runningCount > 0
                    ? `${String(runningCount)} running`
                    : "None running"
                }
                detail={`of ${String(guestCount)} ${guestCount === 1 ? "guest" : "guests"}`}
              />
            )}
            {templateCount > 0 && (
              <Fact label="Templates">{String(templateCount)}</Fact>
            )}
            {childFolders.length > 0 && (
              <Fact label="Subfolders">{String(childFolders.length)}</Fact>
            )}
            {isUnassigned && <Fact label="Holds">guests in no folder</Fact>}
          </>
        }
        actions={
          !isUnassigned && canManage ? (
            <>
              <Button
                variant="outline"
                size="sm"
                className="gap-1.5"
                onClick={() => {
                  setCreateOpen(true);
                }}
              >
                <FolderPlus />
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
                <Pencil />
                Rename
              </Button>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button
                    variant="outline"
                    size="sm"
                    className="px-2"
                    aria-label="More actions"
                  >
                    <MoreHorizontal />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem
                    className="gap-2 text-destructive focus:text-destructive"
                    disabled={deleteFolder.isPending}
                    onClick={handleDelete}
                  >
                    <Trash2 className="h-4 w-4" />
                    Delete
                  </DropdownMenuItem>
                </DropdownMenuContent>
              </DropdownMenu>
            </>
          ) : undefined
        }
      />

      {/* Tabbed content. Uncontrolled (defaultValue only): a ?tab= deep link
          picks the initial tab, and AppShell keys the route tree by pathname,
          so navigating to another folder remounts onto Overview. A bogus or
          permission-hidden ?tab value falls back to Overview instead of
          leaving Radix with a blank panel; so does ?tab=vms, from when the
          guests had a tab of their own. */}
      <Tabs
        defaultValue={folderPageTab(tabParam, [
          "summary",
          ...(canHistory ? ["history"] : []),
          ...(canViewAlerts ? ["alerts"] : []),
        ])}
      >
        <TabsList>
          <TabsTrigger value="summary">Overview</TabsTrigger>
          {canHistory && <TabsTrigger value="history">History</TabsTrigger>}
          {canViewAlerts && <TabsTrigger value="alerts">Alerts</TabsTrigger>}
        </TabsList>

        {/* What the folder adds up to, the folders inside it, then its
            guests: one page, not a summary tab and a list tab. */}
        <TabsContent value="summary" className="mt-5 space-y-4">
          <FolderSummaryTab
            clusterId={clusterId}
            vms={folderVMs}
            childFolders={childFolders}
          />
          <div>
            {failedClusterIds.includes(clusterId) && (
              <InventoryUnavailableNote />
            )}
            <ResourceTable data={tableRows} label="Guests" singleCluster />
          </div>
        </TabsContent>

        {canHistory && (
          <TabsContent value="history" className="mt-5">
            <FolderHistoryTab
              clusterId={clusterId}
              folderId={folderId}
              folderNames={folderNames}
              guestLinks={guestLinks}
            />
          </TabsContent>
        )}

        {canViewAlerts && (
          <TabsContent value="alerts" className="mt-5">
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
