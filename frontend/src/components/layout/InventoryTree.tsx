import { useState, useEffect } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import {
  ChevronRight,
  Server,
  Monitor,
  Container,
  MoreVertical,
  Plus,
  Pencil,
  Trash2,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useClusters } from "@/features/dashboard/api/dashboard-queries";
import { useClusterNodes, useClusterVMs } from "@/features/clusters/api/cluster-queries";
import { useSidebarStore } from "@/stores/sidebar-store";
import { AddClusterDialog } from "@/features/dashboard/components/AddClusterDialog";
import { EditClusterDialog } from "@/features/clusters/components/EditClusterDialog";
import { DeleteClusterDialog } from "@/features/clusters/components/DeleteClusterDialog";
import { VMContextMenu } from "@/features/vms/components/VMContextMenu";
import { VMContextDialogs } from "@/features/vms/components/VMContextDialogs";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { ClusterResponse, NodeResponse, VMResponse } from "@/types/api";

function StatusDot({ status }: { status: string }) {
  const color =
    status === "running" || status === "online" || status === "active"
      ? "bg-green-500"
      : status === "stopped" || status === "offline"
        ? "bg-red-500"
        : "bg-gray-400";
  return <span className={cn("inline-block h-2 w-2 shrink-0 rounded-full", color)} />;
}

function VMIcon({ type }: { type: string }) {
  if (type === "lxc") {
    return <Container className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />;
  }
  return <Monitor className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />;
}

interface NodeBranchProps {
  node: NodeResponse;
  vms: VMResponse[];
  clusterId: string;
}

function NodeBranch({ node, vms, clusterId }: NodeBranchProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const nodeKey = `node:${clusterId}:${node.id}`;
  const isExpanded = expandedNodes.has(nodeKey);
  const nodeVMs = vms.filter((vm) => vm.node_id === node.id);
  const isActive = location.pathname === `/clusters/${clusterId}/nodes/${node.id}`;

  // Auto-expand if a child VM is active
  useEffect(() => {
    const match = location.pathname.match(/^\/inventory\/(qemu|lxc)\/([^/]+)\/([^/]+)$/);
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

  return (
    <div className="border-l border-border pl-3 ml-3">
      <button
        onClick={() => {
          if (nodeVMs.length > 0) toggleNode(nodeKey);
          navigate(`/clusters/${clusterId}/nodes/${node.id}`);
        }}
        className={cn(
          "flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
          isActive && "bg-accent text-accent-foreground",
        )}
      >
        <ChevronRight
          className={cn(
            "h-3 w-3 shrink-0 transition-transform",
            nodeVMs.length === 0 && "invisible",
            isExpanded && "rotate-90",
          )}
        />
        <Server className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <StatusDot status={node.status} />
        <span className="truncate">{node.name}</span>
      </button>

      {isExpanded && nodeVMs.length > 0 && (
        <div className="border-l border-border pl-3 ml-3">
          {nodeVMs
            .sort((a, b) => a.vmid - b.vmid)
            .map((vm) => {
              const kind = vm.type === "lxc" ? "lxc" : "qemu";
              const vmPath = `/inventory/${kind}/${clusterId}/${vm.id}`;
              const vmActive = location.pathname === vmPath;
              return (
                <VMContextMenu
                  key={vm.id}
                  target={{
                    clusterId,
                    resourceId: vm.id,
                    vmid: vm.vmid,
                    name: vm.name,
                    kind: vm.type === "lxc" ? "ct" : "vm",
                    status: vm.status,
                    currentNode: node.name,
                  }}
                >
                  <button
                    onClick={() => { navigate(vmPath); }}
                    className={cn(
                      "flex w-full items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
                      vmActive && "bg-accent text-accent-foreground",
                    )}
                  >
                    <VMIcon type={vm.type} />
                    <StatusDot status={vm.status} />
                    <span className="truncate">
                      {vm.vmid} {vm.name}
                    </span>
                  </button>
                </VMContextMenu>
              );
            })}
        </div>
      )}
    </div>
  );
}

interface ClusterBranchProps {
  cluster: ClusterResponse;
}

function ClusterBranch({ cluster }: ClusterBranchProps) {
  const navigate = useNavigate();
  const location = useLocation();
  const { expandedNodes, toggleNode, expandNode } = useSidebarStore();
  const clusterKey = `cluster:${cluster.id}`;
  const isExpanded = expandedNodes.has(clusterKey);
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
      location.pathname.match(new RegExp(`^/inventory/(qemu|lxc)/${cluster.id}/`))
    ) {
      expandNode(clusterKey);
    }
  }, [location.pathname, cluster.id, clusterKey, expandNode]);

  return (
    <>
      <div>
        <div
          className={cn(
            "group flex items-center gap-1.5 rounded-md px-1.5 py-1 text-xs hover:bg-accent/50 transition-colors",
            isActive && "bg-accent text-accent-foreground",
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
            onClick={() => { navigate(`/clusters/${cluster.id}`); }}
            className="flex min-w-0 flex-1 items-center gap-1.5"
          >
            <Server className="h-3.5 w-3.5 shrink-0 text-primary" />
            <StatusDot status={cluster.is_active ? "active" : "offline"} />
            <span className="truncate font-medium">{cluster.name}</span>
          </button>

          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button className="shrink-0 rounded p-0.5 opacity-0 hover:bg-accent group-hover:opacity-100">
                <MoreVertical className="h-3 w-3" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-32">
              <DropdownMenuItem onClick={() => { setEditOpen(true); }}>
                <Pencil className="mr-2 h-3.5 w-3.5" />
                Edit
              </DropdownMenuItem>
              <DropdownMenuItem
                onClick={() => { setDeleteOpen(true); }}
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
                />
              ))}
          </div>
        )}
      </div>

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

export function InventoryTree() {
  const { data: clusters, isLoading } = useClusters();

  return (
    <div className="space-y-1 py-1">
      <div className="flex items-center justify-between px-2 pb-1">
        <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
          Datacenter
        </span>
        <AddClusterDialog
          trigger={
            <Button variant="ghost" size="icon" className="h-5 w-5">
              <Plus className="h-3 w-3" />
            </Button>
          }
        />
      </div>

      {isLoading && (
        <div className="space-y-1 px-2">
          {Array.from({ length: 2 }, (_, i) => (
            <div key={i} className="h-6 animate-pulse rounded bg-muted" />
          ))}
        </div>
      )}

      {clusters?.length === 0 && (
        <p className="px-2 text-xs text-muted-foreground">No clusters added.</p>
      )}

      {clusters?.map((cluster) => (
        <ClusterBranch key={cluster.id} cluster={cluster} />
      ))}

      <VMContextDialogs />
    </div>
  );
}
