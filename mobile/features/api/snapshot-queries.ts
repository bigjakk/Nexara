/**
 * Snapshot list / create / restore / delete hooks for VMs and containers.
 *
 * Backend contract (verified against `internal/api/handlers/vms.go:937-950`
 * and `internal/api/handlers/containers.go`):
 *
 *   GET    /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots
 *   POST   /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots
 *   POST   /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots/:snap_name/rollback
 *   DELETE /api/v1/clusters/:cluster_id/vms/:vm_id/snapshots/:snap_name
 *
 * Same shape under `/containers/:ct_id/snapshots/...` for LXC.
 *
 * **Asymmetric RBAC** — VMs and containers don't use the same permissions
 * for delete:
 *   VM list:     view:vm
 *   VM create:   execute:vm
 *   VM rollback: execute:vm
 *   VM delete:   delete:vm     ← uses delete, not execute
 *   CT list:     view:container
 *   CT create:   execute:container
 *   CT rollback: execute:container
 *   CT delete:   execute:container ← uses execute, not delete
 *
 * The `useSnapshotPermissions(type)` helper below resolves these into
 * `canList / canCreate / canRollback / canDelete` booleans for the
 * caller, so the UI doesn't have to remember the asymmetry.
 *
 * **VMs (qemu) only support RAM snapshots** via `vmstate=true`. The
 * backend's container snapshot handler ignores the `vmstate` flag — see
 * the explore notes in PLAN.md. The CreateSnapshotModal hides the
 * "Include RAM" toggle for containers.
 *
 * **Backend response on create/rollback/delete** is `{upid, status:
 * "dispatched"}` (mirrors VM lifecycle actions). The actual change is
 * applied asynchronously by Proxmox; we invalidate the snapshot list
 * after the mutation returns and let the polling pick up the new state.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { apiDelete, apiGet, apiPost } from "./api-client";
import { queryKeys } from "./query-keys";
import type { CreateSnapshotRequest, Snapshot } from "./types";
import type { GuestType } from "./guest-action-queries";
import { usePermissions } from "@/hooks/usePermissions";

interface DispatchResponse {
  upid: string;
  status: string;
}

function basePath(clusterId: string, vmId: string, type: GuestType): string {
  const seg = type === "lxc" ? "containers" : "vms";
  return `/clusters/${clusterId}/${seg}/${vmId}`;
}

export function useVMSnapshots(
  clusterId: string | undefined,
  vmId: string | undefined,
  type: GuestType,
) {
  return useQuery({
    queryKey: queryKeys.vmSnapshots(clusterId ?? "", vmId ?? ""),
    queryFn: () =>
      apiGet<Snapshot[]>(`${basePath(clusterId ?? "", vmId ?? "", type)}/snapshots`),
    enabled: Boolean(clusterId && vmId),
    staleTime: 10_000,
    refetchInterval: 30_000,
    refetchIntervalInBackground: false,
  });
}

interface CreateSnapshotVars {
  clusterId: string;
  vmId: string;
  type: GuestType;
  body: CreateSnapshotRequest;
}

export function useCreateSnapshot() {
  const qc = useQueryClient();
  return useMutation<DispatchResponse, Error, CreateSnapshotVars>({
    mutationFn: ({ clusterId, vmId, type, body }) =>
      apiPost<DispatchResponse>(
        `${basePath(clusterId, vmId, type)}/snapshots`,
        body,
      ),
    onSuccess: (_d, vars) => {
      void qc.invalidateQueries({
        queryKey: queryKeys.vmSnapshots(vars.clusterId, vars.vmId),
      });
    },
  });
}

interface SnapshotActionVars {
  clusterId: string;
  vmId: string;
  type: GuestType;
  snapName: string;
}

export function useRollbackSnapshot() {
  const qc = useQueryClient();
  return useMutation<DispatchResponse, Error, SnapshotActionVars>({
    mutationFn: ({ clusterId, vmId, type, snapName }) =>
      apiPost<DispatchResponse>(
        `${basePath(clusterId, vmId, type)}/snapshots/${encodeURIComponent(snapName)}/rollback`,
      ),
    onSuccess: (_d, vars) => {
      void qc.invalidateQueries({
        queryKey: queryKeys.vmSnapshots(vars.clusterId, vars.vmId),
      });
      // Status will change while rollback runs — refresh the VM detail too.
      void qc.invalidateQueries({
        queryKey: queryKeys.vm(vars.clusterId, vars.vmId),
      });
    },
  });
}

export function useDeleteSnapshot() {
  const qc = useQueryClient();
  return useMutation<DispatchResponse, Error, SnapshotActionVars>({
    mutationFn: ({ clusterId, vmId, type, snapName }) =>
      apiDelete<DispatchResponse>(
        `${basePath(clusterId, vmId, type)}/snapshots/${encodeURIComponent(snapName)}`,
      ),
    onSuccess: (_d, vars) => {
      void qc.invalidateQueries({
        queryKey: queryKeys.vmSnapshots(vars.clusterId, vars.vmId),
      });
    },
  });
}

/**
 * Resolves the asymmetric snapshot RBAC into a clean set of booleans
 * the UI can consume without remembering the VM/CT permission split.
 *
 *   VM (qemu): list=view:vm, create/rollback=execute:vm, delete=delete:vm
 *   CT (lxc):  list=view:container, create/rollback/delete=execute:container
 */
export function useSnapshotPermissions(type: GuestType) {
  const { canView, canExecute, canDelete } = usePermissions();
  if (type === "lxc") {
    const can = canExecute("container");
    return {
      canList: canView("container"),
      canCreate: can,
      canRollback: can,
      canDelete: can,
    };
  }
  return {
    canList: canView("vm"),
    canCreate: canExecute("vm"),
    canRollback: canExecute("vm"),
    canDelete: canDelete("vm"),
  };
}
