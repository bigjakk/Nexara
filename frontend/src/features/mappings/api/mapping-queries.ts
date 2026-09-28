import { useCallback } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "@/lib/api-client";
import { apiPath } from "@/lib/api-path";

/**
 * Every USB mapping read shares this prefix — the VM dialog's per-node
 * listings and the cluster tab's listing alike — so a write here refreshes
 * whichever of them is on screen.
 */
function usbMappingsKey(clusterId: string) {
  return ["clusters", clusterId, "usb-mappings"] as const;
}

/**
 * A Proxmox USB resource mapping, as GET …/nodes/:node/usb-mappings lists it.
 *
 * `map` holds the per-node entries verbatim — node=, id= and optionally path=
 * and description=, in any order (see parseUSBMappingEntry). `errors` is Proxmox's check of the mapping against
 * the node the listing was read for: a warning when the mapping has no entry
 * for it, an error when its entry names hardware the node does not have.
 */
export interface USBMapping {
  id: string;
  description: string;
  map: string[];
  errors: MappingCheck[];
}

export interface MappingCheck {
  severity: string;
  message: string;
}

export function useNodeUSBMappings(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: [...usbMappingsKey(clusterId), nodeName],
    queryFn: () =>
      apiClient.list<USBMapping>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/usb-mappings`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
    staleTime: 30_000,
  });
}

export interface CreateUSBMappingRequest {
  mapping_id: string;
  node: string;
  device_id: string;
  path?: string;
  description?: string;
}

export function useCreateUSBMapping(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateUSBMappingRequest) =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/usb-mappings`, data),
    // Returned, not voided: the Add USB Device dialog stages mapping=<id>
    // only once this resolves, so the listing already holds the new mapping
    // when the dialog closes and the next add reuses it instead of trying to
    // create it again. A failed re-read does not reject, so it never turns a
    // created mapping into an error. On settle rather than on success: a 409
    // means the listing is stale — someone created that mapping meanwhile —
    // and re-reading it is what lets the dialog offer it for reuse.
    onSettled: () =>
      qc.invalidateQueries({ queryKey: usbMappingsKey(clusterId) }),
    // The dialog renders the failure inline, next to the name it concerns;
    // having an onError is what keeps the app-wide toast from repeating it.
    onError: () => undefined,
  });
}

/** Every PCI mapping read shares this prefix, as the USB ones share theirs. */
function pciMappingsKey(clusterId: string) {
  return ["clusters", clusterId, "pci-mappings"] as const;
}

/**
 * A Proxmox PCI resource mapping, as GET …/nodes/:node/pci-mappings lists it.
 *
 * `map` holds the node entries verbatim — node=, path=, id= and optionally
 * subsystem-id=, iommugroup= and description=, in any order — and a node may
 * have several: a VM starting there takes the first device not in use.
 * `checks` is Proxmox's check of the mapping against the node the listing was
 * read for, what USB's listing calls `errors`. `mdev` is the mapping's
 * mediated-device flag, which Proxmox requires to match the device.
 */
export interface PCIMapping {
  id: string;
  description: string;
  map: string[];
  checks: MappingCheck[];
  mdev: boolean;
}

export function useNodePCIMappings(clusterId: string, nodeName: string) {
  return useQuery({
    queryKey: [...pciMappingsKey(clusterId), nodeName],
    queryFn: () =>
      apiClient.list<PCIMapping>(
        apiPath`/api/v1/clusters/${clusterId}/nodes/${nodeName}/pci-mappings`,
      ),
    enabled: clusterId.length > 0 && nodeName.length > 0,
    staleTime: 30_000,
  });
}

/**
 * POST …/pci-mappings. Only the node and the device's address: the server
 * copies the rest of the entry from the node's own report of the device.
 */
export interface CreatePCIMappingRequest {
  mapping_id: string;
  node: string;
  path: string;
  description?: string;
}

export function useCreatePCIMapping(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreatePCIMappingRequest) =>
      apiClient.post(apiPath`/api/v1/clusters/${clusterId}/pci-mappings`, data),
    // Returned, for useCreateUSBMapping's reason: the dialog stages
    // mapping=<id> once the listing already holds the new mapping.
    onSettled: () =>
      qc.invalidateQueries({ queryKey: pciMappingsKey(clusterId) }),
    // The dialog renders the failure inline.
    onError: () => undefined,
  });
}

/**
 * A USB mapping as the cluster's Resource Mappings tab lists it
 * (GET …/usb-mappings): the mapping as Proxmox stores it, and for each node
 * one of its entries names, either Proxmox's check of it on that node
 * (`node_checks`, an empty list for a clean check) or why it was not checked
 * (`unchecked`). The server puts every such node in exactly one of the two.
 *
 * `digest` is the whole usb.cfg's, read with `map`: an update sends it back
 * and is refused with 409 if any USB mapping changed since.
 */
export interface ClusterUSBMapping {
  id: string;
  description: string;
  map: string[];
  digest: string;
  node_checks: Record<string, MappingCheck[]>;
  unchecked: Record<string, string>;
}

export function useClusterUSBMappings(clusterId: string) {
  return useQuery({
    queryKey: usbMappingsKey(clusterId),
    queryFn: () =>
      apiClient.list<ClusterUSBMapping>(
        apiPath`/api/v1/clusters/${clusterId}/usb-mappings`,
      ),
    enabled: clusterId.length > 0,
    // Each read runs Proxmox's check on every node an entry names.
    staleTime: 30_000,
  });
}

/**
 * PUT …/usb-mappings/:id. `map` is the WHOLE list of entries the mapping is
 * to have; `description` omitted leaves it, "" removes it; `digest` is the
 * one the entries were read with.
 */
export interface UpdateUSBMappingRequest {
  id: string;
  map: string[];
  description?: string;
  digest: string;
}

export function useUpdateUSBMapping(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: UpdateUSBMappingRequest) =>
      apiClient.put(
        apiPath`/api/v1/clusters/${clusterId}/usb-mappings/${id}`,
        body,
      ),
    onSettled: () => {
      refreshAfterWrite(qc, usbMappingsKey(clusterId));
    },
    // Every caller renders the failure itself, next to what it concerns.
    onError: () => undefined,
  });
}

/**
 * DELETE …/usb-mappings/:id, with the digest the caller's listing had: the
 * server refuses it with 409 if any USB mapping changed since, so a delete
 * confirmed against one set of entries cannot remove another.
 */
export interface DeleteUSBMappingRequest {
  id: string;
  digest: string;
}

export function useDeleteUSBMapping(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, digest }: DeleteUSBMappingRequest) =>
      apiClient.delete(
        apiPath`/api/v1/clusters/${clusterId}/usb-mappings/${id}?digest=${digest}`,
      ),
    onSettled: () => {
      refreshAfterWrite(qc, usbMappingsKey(clusterId));
    },
    onError: () => undefined,
  });
}

/**
 * Re-reads every listing of a kind's mappings — `key`, the kind's prefix —
 * after an update or a delete, succeeded or not: after a 409 the listing is
 * what is stale.
 *
 * Never awaited. The cluster listing runs Proxmox's check on every node, and a
 * promise returned from onSettled would hold the mutation — and the dialog's
 * Saving… — until the slowest node answered, long after the write itself was
 * done. A caller that needs the re-read itself, a 409's, joins this one rather
 * than starting a second (refetch with cancelRefetch: false).
 */
function refreshAfterWrite(
  qc: ReturnType<typeof useQueryClient>,
  key: readonly unknown[],
): void {
  void qc.invalidateQueries({ queryKey: key });
}

/**
 * Resolves once the USB mapping listings on screen have been read again —
 * joining the re-read a write has already started (refreshAfterWrite), not
 * starting a second. A caller holds a mapping's row until then, so nothing is
 * opened, and no digest pinned, from the listing the write just made stale.
 * It never rejects: a failed re-read leaves the stale listing up, and the
 * next edit's digest then answers 409.
 */
export function useUSBMappingsSettled(clusterId: string) {
  const qc = useQueryClient();
  return useCallback(
    () =>
      qc.refetchQueries(
        { queryKey: usbMappingsKey(clusterId), type: "active" },
        { cancelRefetch: false },
      ),
    [qc, clusterId],
  );
}

/**
 * A guest in a usage answer: a user carries `keys` — the usbN or hostpciN
 * that name the mapping — an unchecked one `reason`.
 */
export interface MappingGuest {
  vmid: number;
  name: string;
  node: string;
  keys?: string[];
  reason?: string;
}

/**
 * Which VMs pass a mapping through, read from their live configs. A guest in
 * `unchecked` may use it too: its config could not be read.
 */
export interface MappingUsage {
  mapping_id: string;
  checked: number;
  users: MappingGuest[];
  unchecked: MappingGuest[];
}

/** A USB mapping's usage: MappingUsage, by its earlier name. */
export type USBMappingUsage = MappingUsage;

/**
 * The usage of `mappingId`, or nothing while it is null. Its own key, outside
 * the mappings prefix, and always stale: it answers "may this be deleted
 * now". So every mount of it — each opening of the delete confirmation —
 * reads every VM's config again rather than trusting the app's five-minute
 * cache, and so does a return of the connection while it is open
 * (refetchOnReconnect). A cached answer may be on screen meanwhile, but the
 * confirmation holds its button while a read is in flight. gcTime 0 is not
 * for any of that — staleTime is — but so the answer, guest names and all,
 * is dropped once its dialog closes instead of lingering in the app's cache.
 */
export function useUSBMappingUsage(
  clusterId: string,
  mappingId: string | null,
) {
  return useQuery({
    queryKey: ["clusters", clusterId, "usb-mapping-usage", mappingId],
    queryFn: () =>
      apiClient.get<MappingUsage>(
        apiPath`/api/v1/clusters/${clusterId}/usb-mappings/${mappingId ?? ""}/usage`,
      ),
    enabled: clusterId.length > 0 && mappingId !== null,
    staleTime: 0,
    gcTime: 0,
  });
}

/**
 * A PCI mapping as the cluster's Resource Mappings tab lists it
 * (GET …/pci-mappings): ClusterUSBMapping's fields, with a node's several
 * entries all checked together on that node, the mapping's two flags, and
 * `unreadable_entries` — for each stored entry the server cannot read the way
 * Proxmox does, why. Nexara cannot save the mapping while one is there, so it
 * can only be removed.
 *
 * `digest` is the whole pci.cfg's, read with `map`.
 */
export interface ClusterPCIMapping {
  id: string;
  description: string;
  map: string[];
  digest: string;
  node_checks: Record<string, MappingCheck[]>;
  unchecked: Record<string, string>;
  mdev: boolean;
  live_migration_capable: boolean;
  unreadable_entries: Record<string, string>;
}

export function useClusterPCIMappings(clusterId: string) {
  return useQuery({
    queryKey: pciMappingsKey(clusterId),
    queryFn: () =>
      apiClient.list<ClusterPCIMapping>(
        apiPath`/api/v1/clusters/${clusterId}/pci-mappings`,
      ),
    enabled: clusterId.length > 0,
    // Each read runs Proxmox's check on every node an entry names.
    staleTime: 30_000,
  });
}

/**
 * PUT …/pci-mappings/:id. `map` is every entry the mapping keeps, each exactly
 * as the listing returned it — the server refuses any other string. With
 * `add_node` and `add_path` the server adds that node's device, the entry
 * built from the node's own report of it; with `replace` too, the new entry
 * takes that entry's place. `description` omitted leaves it, "" removes it;
 * `digest` is the one the entries were read with.
 */
export interface UpdatePCIMappingRequest {
  id: string;
  map: string[];
  add_node?: string;
  add_path?: string;
  replace?: string;
  description?: string;
  digest: string;
}

export function useUpdatePCIMapping(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: UpdatePCIMappingRequest) =>
      apiClient.put(
        apiPath`/api/v1/clusters/${clusterId}/pci-mappings/${id}`,
        body,
      ),
    onSettled: () => {
      refreshAfterWrite(qc, pciMappingsKey(clusterId));
    },
    // Every caller renders the failure itself, next to what it concerns.
    onError: () => undefined,
  });
}

/** DELETE …/pci-mappings/:id, with the listing's digest, as for USB. */
export interface DeletePCIMappingRequest {
  id: string;
  digest: string;
}

export function useDeletePCIMapping(clusterId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, digest }: DeletePCIMappingRequest) =>
      apiClient.delete(
        apiPath`/api/v1/clusters/${clusterId}/pci-mappings/${id}?digest=${digest}`,
      ),
    onSettled: () => {
      refreshAfterWrite(qc, pciMappingsKey(clusterId));
    },
    onError: () => undefined,
  });
}

/** useUSBMappingsSettled for the PCI mapping listings. It never rejects. */
export function usePCIMappingsSettled(clusterId: string) {
  const qc = useQueryClient();
  return useCallback(
    () =>
      qc.refetchQueries(
        { queryKey: pciMappingsKey(clusterId), type: "active" },
        { cancelRefetch: false },
      ),
    [qc, clusterId],
  );
}

/**
 * The usage of PCI mapping `mappingId`, as useUSBMappingUsage reads a USB
 * mapping's — and for its reasons, always stale and dropped once its dialog
 * closes.
 */
export function usePCIMappingUsage(
  clusterId: string,
  mappingId: string | null,
) {
  return useQuery({
    queryKey: ["clusters", clusterId, "pci-mapping-usage", mappingId],
    queryFn: () =>
      apiClient.get<MappingUsage>(
        apiPath`/api/v1/clusters/${clusterId}/pci-mappings/${mappingId ?? ""}/usage`,
      ),
    enabled: clusterId.length > 0 && mappingId !== null,
    staleTime: 0,
    gcTime: 0,
  });
}
