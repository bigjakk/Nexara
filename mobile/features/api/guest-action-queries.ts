/**
 * VM/CT lifecycle action mutations.
 *
 * Backend contract (verified against `internal/api/handlers/vms.go:164-227`
 * and `internal/api/handlers/containers.go:111-170`):
 *
 *   POST /api/v1/clusters/:cluster_id/vms/:vmid/status
 *   POST /api/v1/clusters/:cluster_id/containers/:ctid/status
 *
 *   Body: {"action": "start" | "stop" | "shutdown" | "reboot" | "suspend" | "resume"}
 *   - VMs (qemu) additionally accept "reset" — deferred from v1, behaves
 *     like a hard reset and is effectively a power-cycle.
 *
 *   Success response: {"upid": "<proxmox-task-id>", "status": "dispatched"}
 *   - The action is dispatched immediately; the actual VM state change is
 *     observed asynchronously via the existing background task watcher and
 *     the WS `vm_state_change` event (which already invalidates VM queries
 *     via `useEventInvalidation`). For UX feedback we also invalidate the
 *     VM detail + cluster lists in onSuccess so the user sees the polling
 *     refetch flicker into the new state quickly.
 *
 *   Errors:
 *     400 — invalid action / malformed body
 *     403 — RBAC denied (`execute:vm` / `execute:container`)
 *     404 — VM/cluster not found
 *     502 — Proxmox unreachable
 *     500 — other Proxmox error
 *
 * Following the same pattern as `useAcknowledgeAlert` / `useResolveAlert`
 * in `alert-queries.ts`.
 */

import { useMutation, useQueryClient } from "@tanstack/react-query";

import { apiPost } from "./api-client";
import { queryKeys } from "./query-keys";

export type GuestAction =
  | "start"
  | "stop"
  | "shutdown"
  | "reboot"
  | "suspend"
  | "resume";

export type GuestType = "qemu" | "lxc";

export interface GuestActionVars {
  clusterId: string;
  vmId: string;
  type: GuestType;
  action: GuestAction;
}

export interface GuestActionResponse {
  upid: string;
  status: string;
}

export function useGuestAction() {
  const qc = useQueryClient();
  return useMutation<GuestActionResponse, Error, GuestActionVars>({
    mutationFn: ({ clusterId, vmId, type, action }) => {
      const path = type === "lxc" ? "containers" : "vms";
      return apiPost<GuestActionResponse>(
        `/clusters/${clusterId}/${path}/${vmId}/status`,
        { action },
      );
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({
        queryKey: queryKeys.vm(vars.clusterId, vars.vmId),
      });
      void qc.invalidateQueries({
        queryKey: queryKeys.clusterVMs(vars.clusterId),
      });
      void qc.invalidateQueries({
        queryKey: queryKeys.clusterContainers(vars.clusterId),
      });
    },
  });
}
