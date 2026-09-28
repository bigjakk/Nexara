import { Fragment, useRef, useState } from "react";
import { Pencil, Plus, Replace, Trash2, X } from "lucide-react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { ConfirmDeleteDialog } from "@/components/ConfirmDeleteDialog";
import {
  QueryFailureNote,
  QueryStateNotice,
} from "@/components/QueryStateNotice";
import { usePermissions } from "@/hooks/usePermissions";
import { describeError } from "@/lib/api-error";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import {
  useNodePCIDevices,
  type NodePCIDevice,
} from "@/features/vms/api/vm-queries";
import {
  useClusterPCIMappings,
  useDeletePCIMapping,
  usePCIMappingsSettled,
  usePCIMappingUsage,
  useUpdatePCIMapping,
  type ClusterPCIMapping,
} from "../api/mapping-queries";
import { useMappingRowHolds } from "../hooks/useMappingRowHolds";
import { usePCIPick } from "../hooks/usePCIPick";
import { usePCIRiskAck } from "../hooks/usePCIRiskAck";
import { usePinnedMapping } from "../hooks/usePinnedMapping";
import {
  isConflict,
  ownValue,
  workLabel,
  type WorkKind,
} from "../lib/mapping-edits";
import {
  parsePCIMappingEntry,
  pciEntryMatches,
  pciMappingEntryDescription,
  pciPassRisks,
  pciPathsOverlap,
  pciSlot,
  samePCIMappingEntry,
} from "../lib/pci-mapping";
import {
  characterCount,
  cleanDeviceText,
  MAPPING_ID_PATTERN,
} from "../lib/usb-mapping";
import { MappingDeleteDescription } from "./MappingDeleteDescription";
import { NodeCheckResult } from "./NodeCheckResult";
import { PCIDevicePicker } from "./PCIDevicePicker";
import { PCIRiskConfirm } from "./PCIRiskConfirm";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

/** Proxmox's cap on a mapping's description. */
const DESCRIPTION_MAX = 4096;

/**
 * Why a save or a removal came back 409. The digest covers the whole
 * pci.cfg, so the change that caused it may be to any PCI mapping.
 */
const CONFLICT_NOTE =
  "The cluster's PCI mappings changed since this was loaded — a change to any PCI mapping counts, not only to this one.";

/** One node entry of a mapping, as a row shows it. */
interface ShownEntry {
  /** The entry exactly as Proxmox stores it: how a save names it. */
  raw: string;
  /** Its place in the mapping's list. */
  index: number;
  node: string;
  path: string;
  id: string;
  subsystemId: string;
  iommugroup: string;
  description: string;
  /** Why Nexara cannot read it, when it cannot: then only its removal is offered. */
  unreadable: string | undefined;
}

function shownEntries(m: ClusterPCIMapping): ShownEntry[] {
  return m.map.map((raw, index) => ({
    raw,
    index,
    ...parsePCIMappingEntry(raw),
    description: cleanDeviceText(pciMappingEntryDescription(raw)),
    unreadable: ownValue(m.unreadable_entries, raw),
  }));
}

/**
 * A mapping's entries by node, the nodes in name order and each node's entries
 * in the mapping's own order — the order a VM starting there is offered them.
 */
function nodeGroups(
  entries: ShownEntry[],
): { node: string; entries: ShownEntry[] }[] {
  const byNode = new Map<string, ShownEntry[]>();
  for (const e of entries) {
    const list = byNode.get(e.node) ?? [];
    list.push(e);
    byNode.set(e.node, list);
  }
  return [...byNode.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([node, list]) => ({ node, entries: list }));
}

/** The dialogs that write the mapping's entries with a PUT. */
type EditTarget =
  | { kind: "add-device"; mapping: ClusterPCIMapping }
  | { kind: "replace"; mapping: ClusterPCIMapping; entry: ShownEntry }
  | { kind: "description"; mapping: ClusterPCIMapping };

interface RemoveTarget {
  mapping: ClusterPCIMapping;
  /** One entry, or every entry Nexara cannot read (openRemove says why). */
  entries: ShownEntry[];
}

interface DeleteTarget {
  mapping: ClusterPCIMapping;
  /** Why a Remove became this Delete, when one did. */
  note: string | undefined;
}

/**
 * The cluster's PCI resource mappings: each with its node entries — several
 * for a node, as alternatives — and what Proxmox reports checking the mapping
 * on each node, and, with manage:cluster, the actions that change them. Every
 * edit is a compare-and-swap against the digest of the listing the operator
 * was looking at, as on the USB card (USBMappingsCard says how).
 */
export function PCIMappingsCard({ clusterId }: { clusterId: string }) {
  const { canManage } = usePermissions();
  const canEdit = canManage("cluster");
  const listQuery = useClusterPCIMappings(clusterId);
  const nodesQuery = useClusterNodes(clusterId);
  const removeEntries = useUpdatePCIMapping(clusterId);
  const deleteMapping = useDeletePCIMapping(clusterId);
  const settled = usePCIMappingsSettled(clusterId);
  // Where focus goes after a confirmed Remove or Delete, and after an edit
  // whose opener cannot take it back, as on the USB card.
  const regionRef = useRef<HTMLDivElement>(null);
  const focusFallback = () => regionRef.current;
  const confirmed = useRef(false);

  // Unmounted on every close, so a pin and a re-read belong to one opening.
  const [edit, setEdit] = useState<EditTarget | null>(null);
  const { holdRow, workFor } = useMappingRowHolds(settled);
  const [removeTarget, setRemoveTarget] = useState<RemoveTarget | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget | null>(null);
  const [actionError, setActionError] = useState("");
  const usage = usePCIMappingUsage(clusterId, deleteTarget?.mapping.id ?? null);

  const mappings = [...(listQuery.data ?? [])].sort((a, b) =>
    a.id.localeCompare(b.id),
  );
  const clusterNodes = (nodesQuery.data ?? [])
    .map((n) => n.name)
    .sort((a, b) => a.localeCompare(b));
  // Replace reads its entry's node's devices, and Proxmox looks up and
  // connects to a name that is not one of the cluster's nodes (the listing's
  // node checks never ask one, checkMappingNodes in the handlers says why):
  // Replace is offered on a node the cluster is known to have, held while the
  // node list is loading or could not be read, and not offered on any other.
  const replaceOn = (name: string): ReplaceOffer =>
    nodesQuery.data === undefined
      ? "held"
      : clusterNodes.includes(name)
        ? "offered"
        : "none";

  const openEdit = (target: EditTarget) => {
    setActionError("");
    setEdit(target);
  };

  const focusAfterConfirm = (event: Event) => {
    if (!confirmed.current) return;
    confirmed.current = false;
    event.preventDefault();
    regionRef.current?.focus();
  };

  const openRemove = (mapping: ClusterPCIMapping, entry: ShownEntry) => {
    setActionError("");
    // An entry Nexara cannot read goes together with every other one it
    // cannot read: the server refuses to save the mapping while any is left,
    // so removing them one at a time could never be saved.
    const going =
      entry.unreadable === undefined
        ? [entry]
        : shownEntries(mapping).filter((e) => e.unreadable !== undefined);
    if (going.length === mapping.map.length) {
      // Proxmox's own UI deletes a mapping rather than empty it, and the
      // server refuses an empty one.
      setDeleteTarget({
        mapping,
        note:
          going.length === 1
            ? "This is the mapping's only entry, so removing it deletes the mapping."
            : "Nexara can read none of the mapping's entries, so removing them deletes the mapping.",
      });
    } else {
      setRemoveTarget({ mapping, entries: going });
    }
  };

  const confirmRemove = ({ mapping, entries }: RemoveTarget) => {
    const going = new Set(entries.map((e) => e.index));
    // The list goes back without them, with the digest of the read the
    // confirmation showed.
    confirmed.current = true;
    holdRow(
      mapping.id,
      "removing",
      removeEntries
        .mutateAsync({
          id: mapping.id,
          map: mapping.map.filter((_, i) => !going.has(i)),
          digest: mapping.digest,
        })
        .catch((err: unknown) => {
          setActionError(
            isConflict(err)
              ? `Nothing was removed from ${mapping.id}. ${CONFLICT_NOTE} The mappings have been reloaded; check the entry and remove it again.`
              : `Could not remove the entry from ${mapping.id}: ${describeError(err) || "the request failed."}`,
          );
        }),
    );
  };

  const confirmDelete = ({ mapping }: DeleteTarget) => {
    confirmed.current = true;
    holdRow(
      mapping.id,
      "deleting",
      deleteMapping
        .mutateAsync({ id: mapping.id, digest: mapping.digest })
        .catch((err: unknown) => {
          setActionError(
            isConflict(err)
              ? `Nothing was deleted. ${CONFLICT_NOTE} The mappings have been reloaded; check ${mapping.id} and delete it again.`
              : `Could not delete ${mapping.id}: ${describeError(err) || "the request failed."}`,
          );
        }),
    );
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>PCI Devices</CardTitle>
        <CardDescription>
          A VM passes a host PCI device through as hostpciN:
          mapping=&lt;name&gt;. A node may have several entries — a VM starting
          there takes the first device not in use — and a VM will not start on a
          node the mapping has none for. Add PCI Device on a VM creates a
          mapping; Proxmox lists only the mappings Nexara&apos;s API token may
          see.
        </CardDescription>
        {!canEdit && (
          <p className="text-xs text-muted-foreground">
            Viewing only: changing mappings needs the Manage Cluster permission.
          </p>
        )}
      </CardHeader>
      <CardContent
        ref={regionRef}
        role="region"
        aria-label="PCI mappings"
        tabIndex={-1}
        className="space-y-3 outline-none"
      >
        {actionError !== "" && (
          <div
            role="alert"
            className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"
          >
            <p className="min-w-0 flex-1">{actionError}</p>
            <button
              type="button"
              aria-label="Dismiss"
              className="shrink-0 opacity-70 hover:opacity-100"
              onClick={() => {
                setActionError("");
              }}
            >
              <X className="h-4 w-4" />
            </button>
          </div>
        )}
        {mappings.length > 0 ? (
          <>
            <QueryFailureNote query={listQuery} subject="the PCI mappings" />
            {/* Only for a user Replace is offered to: the node list needs
                view:node, which a role with view:cluster may not have. */}
            {canEdit && nodesQuery.data === undefined && (
              <QueryFailureNote
                query={nodesQuery}
                subject="the cluster's nodes, which Replace device needs"
              />
            )}
            <div>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Node</TableHead>
                    <TableHead>Device</TableHead>
                    <TableHead>IOMMU group</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="w-0" />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {mappings.map((m) => (
                    <MappingRows
                      key={m.id}
                      mapping={m}
                      canEdit={canEdit}
                      replaceOn={replaceOn}
                      working={workFor(m.id)}
                      onAddDevice={() => {
                        openEdit({ kind: "add-device", mapping: m });
                      }}
                      onEditDescription={() => {
                        openEdit({ kind: "description", mapping: m });
                      }}
                      onDelete={() => {
                        setActionError("");
                        setDeleteTarget({ mapping: m, note: undefined });
                      }}
                      onReplace={(entry) => {
                        openEdit({ kind: "replace", mapping: m, entry });
                      }}
                      onRemove={(entry) => {
                        openRemove(m, entry);
                      }}
                    />
                  ))}
                </TableBody>
              </Table>
            </div>
          </>
        ) : (
          <QueryStateNotice
            query={listQuery}
            subject="the PCI mappings"
            empty={
              canEdit
                ? "This cluster has no PCI mappings yet. Add a host PCI device to a VM to create one."
                : "This cluster has no PCI mappings yet."
            }
          />
        )}
      </CardContent>

      {edit !== null && (
        <EditMappingDialog
          clusterId={clusterId}
          target={edit}
          clusterNodes={clusterNodes}
          onSaved={(id, write) => {
            holdRow(id, "saving", write);
          }}
          onClose={() => {
            setEdit(null);
          }}
          fallbackFocus={focusFallback}
        />
      )}

      <ConfirmDeleteDialog
        target={removeTarget}
        onClose={() => {
          setRemoveTarget(null);
        }}
        onConfirm={confirmRemove}
        title={({ mapping, entries }) => removeTitle(mapping, entries)}
        description={({ mapping, entries }) =>
          removeDescription(mapping, entries)
        }
        confirmLabel={
          removeTarget !== null && removeTarget.entries.length > 1
            ? "Remove entries"
            : "Remove entry"
        }
        onCloseAutoFocus={focusAfterConfirm}
      />
      <ConfirmDeleteDialog
        target={deleteTarget}
        onClose={() => {
          setDeleteTarget(null);
        }}
        onConfirm={confirmDelete}
        title={({ mapping }) => `Delete PCI mapping ${mapping.id}?`}
        description={({ mapping, note }) => (
          <MappingDeleteDescription
            kind="PCI"
            mappingId={mapping.id}
            entries={mapping.map.length}
            note={note}
            usage={usage}
          />
        )}
        // Held until this opening's usage check has answered, as on the USB
        // card.
        confirmDisabled={
          usage.isFetching || !(usage.isSuccess || usage.isError)
        }
        confirmLabel="Delete mapping"
        onCloseAutoFocus={focusAfterConfirm}
      />
    </Card>
  );
}

function removeTitle(
  mapping: ClusterPCIMapping,
  entries: ShownEntry[],
): string {
  const [entry] = entries;
  if (entries.length > 1) {
    return `Remove the ${String(entries.length)} entries Nexara cannot read from ${mapping.id}?`;
  }
  if (entry === undefined || entry.unreadable !== undefined) {
    return `Remove the entry Nexara cannot read from ${mapping.id}?`;
  }
  return `Remove ${entry.path} on ${entry.node} from ${mapping.id}?`;
}

function removeDescription(
  mapping: ClusterPCIMapping,
  entries: ShownEntry[],
): string {
  const [entry] = entries;
  if (entry === undefined || entry.unreadable !== undefined) {
    return `Nexara cannot save a mapping while an entry it cannot read is there. The mapping's other entries stay.`;
  }
  const left = shownEntries(mapping).filter(
    (e) => e.node === entry.node && e.index !== entry.index,
  ).length;
  if (left === 1) {
    return `${entry.node} keeps its other entry: a VM using ${mapping.id} there is given that device. The mapping's other entries stay.`;
  }
  return left > 1
    ? `${entry.node} keeps its other ${String(left)} entries: a VM using ${mapping.id} there is given the first of them not in use. The mapping's other entries stay.`
    : `A VM using ${mapping.id} will not start on ${entry.node} until the mapping has an entry for it again. The mapping's other entries stay.`;
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

/** Whether Replace is offered on an entry's node (PCIMappingsCard says why). */
type ReplaceOffer = "offered" | "held" | "none";

interface MappingRowsProps {
  mapping: ClusterPCIMapping;
  canEdit: boolean;
  replaceOn: (node: string) => ReplaceOffer;
  /** A write of this mapping, or of another, being read back. */
  working: WorkKind | undefined;
  onAddDevice: () => void;
  onEditDescription: () => void;
  onDelete: () => void;
  onReplace: (entry: ShownEntry) => void;
  onRemove: (entry: ShownEntry) => void;
}

function MappingRows({
  mapping,
  canEdit,
  replaceOn,
  working,
  onAddDevice,
  onEditDescription,
  onDelete,
  onReplace,
  onRemove,
}: MappingRowsProps) {
  const entries = shownEntries(mapping);
  const groups = nodeGroups(entries);
  const unreadable = entries.filter((e) => e.unreadable !== undefined).length;
  const description = cleanDeviceText(mapping.description);
  // Proxmox states no maximum for an id; Nexara's routes address up to 128
  // characters, the ceiling its create shares.
  const addressable = MAPPING_ID_PATTERN.test(mapping.id);
  const actions = canEdit && addressable;
  const busy = working !== undefined;
  // While an entry Nexara cannot read is there, the server refuses every
  // save that keeps it: only its removal, or the delete, can go through. And
  // it saves a mapping only with at least one entry, so one with none can
  // only be deleted.
  const empty = entries.length === 0;
  const blocked = busy || unreadable > 0 || empty;

  return (
    <Fragment>
      <TableRow className="bg-muted/30 hover:bg-muted/30">
        <TableCell colSpan={4}>
          <span className="font-mono font-medium">{mapping.id}</span>
          {description !== "" && (
            <span className="ml-2 text-muted-foreground">{description}</span>
          )}
          {mapping.mdev && (
            <Badge variant="outline" className="ml-2">
              Mediated devices
            </Badge>
          )}
          {mapping.live_migration_capable && (
            <Badge variant="outline" className="ml-2">
              Live migration
            </Badge>
          )}
          {working !== undefined && (
            <span className="ml-2 text-xs text-muted-foreground">
              {workLabel[working]}
            </span>
          )}
          {!addressable && (
            <span className="block text-xs text-amber-700 dark:text-amber-400">
              Nexara manages mapping names of up to 128 characters. Change this
              one in the Proxmox web UI.
            </span>
          )}
          {unreadable > 0 && (
            <span className="block text-xs text-destructive">
              {unreadable === 1
                ? "Nexara cannot read one of this mapping's entries, and cannot save the mapping while it is there: remove it before any other change."
                : `Nexara cannot read ${String(unreadable)} of this mapping's entries, and cannot save the mapping while one is there: remove them before any other change.`}
            </span>
          )}
        </TableCell>
        <TableCell className="text-right whitespace-nowrap">
          {actions && (
            <div className="flex justify-end gap-1">
              <Button
                variant="ghost"
                size="sm"
                disabled={blocked}
                onClick={onAddDevice}
              >
                <Plus className="mr-1 h-3.5 w-3.5" />
                Add device
              </Button>
              <Button
                variant="ghost"
                size="sm"
                disabled={blocked}
                onClick={onEditDescription}
              >
                <Pencil className="mr-1 h-3.5 w-3.5" />
                Edit description
              </Button>
              <Button
                variant="ghost"
                size="sm"
                className="text-destructive hover:text-destructive"
                disabled={busy}
                onClick={onDelete}
              >
                <Trash2 className="mr-1 h-3.5 w-3.5" />
                {working === "deleting" ? "Deleting…" : "Delete"}
              </Button>
            </div>
          )}
        </TableCell>
      </TableRow>
      {entries.length === 0 && (
        <TableRow>
          <TableCell colSpan={5} className="pl-6 text-xs text-muted-foreground">
            No node entries: no VM can use this mapping anywhere. Nexara saves a
            mapping only with at least one entry, so it can only delete this
            one; give it an entry in Proxmox to keep it.
          </TableCell>
        </TableRow>
      )}
      {groups.flatMap((g) =>
        g.entries.map((e, i) => (
          <TableRow key={`${e.raw}#${String(e.index)}`}>
            {i === 0 && (
              <TableCell rowSpan={g.entries.length} className="pl-6 align-top">
                {cleanDeviceText(g.node) || "—"}
                {g.entries.length > 1 && (
                  <span className="block text-xs text-muted-foreground">
                    {g.entries.length} devices: a VM starting here takes the
                    first one not in use.
                  </span>
                )}
              </TableCell>
            )}
            <TableCell>
              {e.unreadable !== undefined ? (
                <>
                  <span className="font-mono break-all">
                    {cleanDeviceText(e.raw)}
                  </span>
                  <span className="block text-xs text-destructive">
                    Nexara cannot read this entry.{" "}
                    {cleanDeviceText(e.unreadable)}
                  </span>
                </>
              ) : (
                <>
                  <span className="font-mono">{e.path}</span>
                  <span className="ml-2 font-mono text-muted-foreground">
                    {e.id}
                    {e.subsystemId !== "" ? ` (${e.subsystemId})` : ""}
                  </span>
                  {e.description !== "" && (
                    <span className="block text-xs text-muted-foreground">
                      {e.description}
                    </span>
                  )}
                </>
              )}
            </TableCell>
            <TableCell>
              {e.unreadable === undefined && e.iommugroup !== "" ? (
                <span className="font-mono">{e.iommugroup}</span>
              ) : (
                <span className="text-muted-foreground">—</span>
              )}
            </TableCell>
            {i === 0 && (
              <TableCell rowSpan={g.entries.length} className="align-top">
                <NodeStatus mapping={mapping} node={g.node} />
              </TableCell>
            )}
            <TableCell className="text-right whitespace-nowrap">
              {actions && (
                <div className="flex justify-end gap-1">
                  {e.unreadable === undefined &&
                    replaceOn(e.node) !== "none" && (
                      <Button
                        variant="ghost"
                        size="sm"
                        disabled={blocked || replaceOn(e.node) === "held"}
                        onClick={() => {
                          onReplace(e);
                        }}
                      >
                        <Replace className="mr-1 h-3.5 w-3.5" />
                        Replace device
                      </Button>
                    )}
                  <Button
                    variant="ghost"
                    size="sm"
                    className="text-destructive hover:text-destructive"
                    disabled={e.unreadable === undefined ? blocked : busy}
                    onClick={() => {
                      onRemove(e);
                    }}
                  >
                    <Trash2 className="mr-1 h-3.5 w-3.5" />
                    Remove
                  </Button>
                </div>
              )}
            </TableCell>
          </TableRow>
        )),
      )}
    </Fragment>
  );
}

/**
 * What is known about a mapping on one node, once for all its entries there:
 * Proxmox checks them together, and qemu-server stops at the first entry that
 * fails, so one failing entry keeps every VM using the mapping from starting
 * on the node. A node that was not checked is never shown as OK.
 */
function NodeStatus({
  mapping,
  node,
}: {
  mapping: ClusterPCIMapping;
  node: string;
}) {
  if (node === "") {
    return (
      <p className="text-xs text-destructive">
        Names no node, so no VM can use it.
      </p>
    );
  }
  const checks = ownValue(mapping.node_checks, node);
  return (
    <div className="space-y-0.5">
      <NodeCheckResult
        node={node}
        unchecked={mapping.unchecked}
        checks={mapping.node_checks}
      />
      {checks !== undefined && checks.some((c) => c.severity === "error") && (
        <p className="text-xs text-muted-foreground">
          An entry that fails stops every VM using this mapping from starting on
          this node, whichever entry it would have been given.
        </p>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Add device, Replace device, Edit description
// ---------------------------------------------------------------------------

/**
 * The three edits that PUT the mapping's entries. The mapping is pinned when
 * the dialog opens and saved against as it was (usePinnedMapping). Add device
 * and Replace device name only the node and the device's address: the server
 * builds the entry from the node's own report of the device, and sends back
 * every entry the mapping keeps exactly as the listing had it.
 */
function EditMappingDialog({
  clusterId,
  target,
  clusterNodes,
  onSaved,
  onClose,
  fallbackFocus,
}: {
  clusterId: string;
  target: EditTarget;
  clusterNodes: string[];
  onSaved: (id: string, write: Promise<unknown>) => void;
  onClose: () => void;
  fallbackFocus: () => HTMLElement | null;
}) {
  const listQuery = useClusterPCIMappings(clusterId);
  const update = useUpdatePCIMapping(clusterId);
  const { pinned, reread, onSaveError } = usePinnedMapping(
    target.mapping,
    listQuery,
  );
  // Only the save locks the dialog; the re-read after a conflict holds Save.
  const busy = update.isPending;

  // Replace keeps its entry by what it says, not by position or spelling:
  // after a re-read it is wherever it now sits, or gone — or unreadable, which
  // is as good as gone: it can only be removed.
  const entryIndex =
    target.kind === "replace"
      ? pinned.map.findIndex(
          (raw) =>
            ownValue(pinned.unreadable_entries, raw) === undefined &&
            samePCIMappingEntry(raw, target.entry.raw),
        )
      : -1;
  const replacedRaw = entryIndex >= 0 ? pinned.map[entryIndex] : undefined;
  const entryGone = target.kind === "replace" && replacedRaw === undefined;
  const unreadable = pinned.map.filter(
    (raw) => ownValue(pinned.unreadable_entries, raw) !== undefined,
  ).length;

  const [node, setNode] = useState(
    target.kind === "replace" ? target.entry.node : "",
  );
  // Only a node the cluster is known to have has its devices read: the rows
  // offer Replace on no other (replaceOn), and this holds should the node
  // list change while the dialog is open — nor is a pick made before then
  // weighed against a node that is no longer there.
  const nodeKnown = clusterNodes.includes(node);
  const devicesQuery = useNodePCIDevices(
    clusterId,
    target.kind === "description" || !nodeKnown ? "" : node,
  );
  const pick = usePCIPick(devicesQuery.data, node);
  const [description, setDescription] = useState(target.mapping.description);
  // Proxmox's section config drops the white space around a description
  // when it reads pci.cfg back, so none is written.
  const trimmedDescription = description.trim();

  // The node's other entries, which the device must not repeat or overlap —
  // the entry being replaced aside, and any Nexara cannot read, which holds
  // Save anyway and whose path is not to be trusted.
  const nodeEntries = pinned.map
    .map((raw, i) => ({ i, raw, e: parsePCIMappingEntry(raw) }))
    .filter(
      ({ i, raw, e }) =>
        e.node === node &&
        i !== entryIndex &&
        ownValue(pinned.unreadable_entries, raw) === undefined,
    );
  const overlapping = (path: string) =>
    nodeEntries.find(({ e }) => e.path !== "" && pciPathsOverlap(e.path, path))
      ?.e.path;
  const isTaken = (d: NodePCIDevice) =>
    overlapping(pick.allFunctions ? pciSlot(d.id) : d.id) !== undefined;
  const overlap =
    nodeKnown && pick.path !== "" ? overlapping(pick.path) : undefined;

  // The mapping's mdev flag must match every entry's device (the server
  // refuses otherwise): a device that differs can be added only as the
  // mapping's only entry, which the flag then follows.
  const deviceMdev = pick.path !== "" ? pick.expected?.mdev : undefined;
  const flagFollows = target.kind === "replace" && pinned.map.length === 1;
  const mdevMismatch =
    target.kind !== "description" &&
    !flagFollows &&
    deviceMdev !== undefined &&
    deviceMdev !== pinned.mdev;
  const flagChange =
    flagFollows && deviceMdev !== undefined && deviceMdev !== pinned.mdev
      ? deviceMdev
      : undefined;

  let unchanged = false;
  if (target.kind === "replace" && replacedRaw !== undefined && pick.expected) {
    unchanged =
      pciEntryMatches(replacedRaw, pick.expected.entry) &&
      flagChange === undefined;
  } else if (target.kind === "description") {
    unchanged = trimmedDescription === pinned.description.trim();
  }

  const risks =
    target.kind === "description" || !nodeKnown || pick.path === ""
      ? []
      : pciPassRisks([pick.path], pick.deviceList, node);
  const risk = usePCIRiskAck(`${target.kind}|${node}|${pick.path}`, risks);

  const descriptionError =
    target.kind === "description" &&
    characterCount(description) > DESCRIPTION_MAX
      ? `At most ${String(DESCRIPTION_MAX)} characters.`
      : "";
  const deviceReady =
    target.kind === "description" ||
    (nodeKnown &&
      pick.path !== "" &&
      overlap === undefined &&
      !mdevMismatch &&
      risk.accepted &&
      !entryGone);
  const canSave =
    !busy &&
    reread !== "reading" &&
    reread !== "gone" &&
    unreadable === 0 &&
    !unchanged &&
    descriptionError === "" &&
    deviceReady;

  function clearError() {
    if (!update.isPending) update.reset();
  }

  function save() {
    if (!canSave) return;
    const saving = pinned;
    update.mutate(
      target.kind === "description"
        ? {
            id: saving.id,
            map: saving.map,
            description: trimmedDescription,
            digest: saving.digest,
          }
        : {
            id: saving.id,
            map: saving.map,
            add_node: node,
            add_path: pick.path,
            ...(target.kind === "replace" && replacedRaw !== undefined
              ? { replace: replacedRaw }
              : {}),
            digest: saving.digest,
          },
      {
        onSuccess: () => {
          onSaved(saving.id, Promise.resolve());
          onClose();
        },
        onError: (err) => {
          onSaveError(err);
        },
      },
    );
  }

  const conflicted = isConflict(update.error);
  const title =
    target.kind === "add-device"
      ? `Add a device to ${pinned.id}`
      : target.kind === "replace"
        ? `Replace ${target.entry.path} on ${target.entry.node}`
        : `Edit the description of ${pinned.id}`;
  const devicesFailed = devicesQuery.isError
    ? `Could not list the node's PCI devices${describeError(devicesQuery.error) ? `: ${describeError(devicesQuery.error)}` : "."} Type the device's address; Nexara reads the device from the node when it saves.`
    : "";

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent className="max-w-lg" fallbackFocus={fallbackFocus}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          {target.kind === "add-device" && (
            <div className="space-y-1">
              <Label htmlFor="edit-pci-mapping-node" className="text-xs">
                Node
              </Label>
              {clusterNodes.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  The cluster&apos;s nodes are not available: still loading, or
                  they could not be read.
                </p>
              ) : (
                <select
                  id="edit-pci-mapping-node"
                  className={selectClass}
                  value={node}
                  disabled={busy}
                  onChange={(e) => {
                    // A pick is of one node's device: none carries over.
                    setNode(e.target.value);
                    pick.setAddress("");
                    pick.setAllFunctions(false);
                    clearError();
                  }}
                >
                  <option value="">Select a node...</option>
                  {clusterNodes.map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              )}
              <p className="text-xs text-muted-foreground">
                A node that has entries already gets one more: a VM starting
                there takes the first device not in use.
              </p>
            </div>
          )}
          {target.kind === "replace" && replacedRaw !== undefined && (
            <p className="text-sm text-muted-foreground">
              Now:{" "}
              <span className="font-mono">
                {parsePCIMappingEntry(replacedRaw).path}{" "}
                {parsePCIMappingEntry(replacedRaw).id}
              </span>
              . The entry keeps its node and its place among the node&apos;s
              entries. Pick the same device to bring its ids and group up to
              date.
            </p>
          )}
          {target.kind === "replace" && !nodeKnown && (
            <p className="text-xs text-destructive">
              {clusterNodes.length === 0
                ? "The cluster's nodes are not available: still loading, or they could not be read."
                : `${cleanDeviceText(node)} is not one of the cluster's nodes now, so Nexara cannot read its devices. Remove the entry instead.`}
            </p>
          )}
          {target.kind !== "description" &&
            nodeKnown &&
            // Loading is not "no list": the picker would offer the typed
            // address in its place.
            (devicesQuery.isPending ? (
              <p className="text-xs text-muted-foreground">
                Loading the node&apos;s PCI devices…
              </p>
            ) : (
              <>
                <PCIDevicePicker
                  idPrefix="edit-pci-mapping"
                  pick={pick}
                  disabled={busy}
                  onPicked={clearError}
                  isTaken={isTaken}
                />
                {devicesFailed && (
                  <p className="text-xs text-muted-foreground">
                    {devicesFailed}
                  </p>
                )}
              </>
            ))}
          {overlap !== undefined && (
            <p className="text-xs text-destructive">
              {node} already has {overlap} in this mapping; a device can be in
              it only once per node, as a whole or as one of its functions.
            </p>
          )}
          {mdevMismatch && (
            <p className="text-xs text-destructive">
              {deviceMdev
                ? `${pick.path} can provide mediated devices, but ${pinned.id} is not set to use them.`
                : `${pick.path} cannot provide mediated devices, but ${pinned.id} is set to use them.`}{" "}
              Proxmox refuses to start a VM with an entry that does not match
              the mapping&apos;s flag, and one that does not stops the
              node&apos;s other entries too. Change the flag in Proxmox first.
            </p>
          )}
          {flagChange !== undefined && (
            <p className="text-xs text-amber-700 dark:text-amber-400">
              Saving also turns {pinned.id}&apos;s &quot;Use with mediated
              devices&quot; flag {flagChange ? "on" : "off"} to match{" "}
              {pick.path}: Proxmox checks the two against each other.
            </p>
          )}
          {target.kind === "replace" && unchanged && (
            <p className="text-xs text-muted-foreground">
              That is what the entry passes now.
            </p>
          )}
          {target.kind !== "description" && (
            <PCIRiskConfirm
              idPrefix="edit-pci-mapping"
              node={node}
              risks={risks}
              accepted={risk.accepted}
              onAcceptedChange={risk.setAccepted}
              disabled={busy}
              lead={
                <>
                  When a VM using {pinned.id} starts on {node}, the device may
                  be the one it is given, and is then taken away from the node —
                  check that the node does not need it:
                </>
              }
            />
          )}
          {target.kind === "description" && (
            <div className="space-y-1">
              <Label htmlFor="edit-pci-mapping-description" className="text-xs">
                Description
              </Label>
              <Input
                id="edit-pci-mapping-description"
                value={description}
                disabled={busy}
                onChange={(e) => {
                  setDescription(e.target.value);
                  clearError();
                }}
                aria-invalid={descriptionError !== ""}
              />
              {descriptionError ? (
                <p className="text-xs text-destructive">{descriptionError}</p>
              ) : (
                <p className="text-xs text-muted-foreground">
                  Leave it empty to remove the description.
                </p>
              )}
              {reread === "reloaded" && (
                <p className="text-xs text-muted-foreground">
                  Stored now: {cleanDeviceText(pinned.description) || "none"}
                </p>
              )}
            </div>
          )}
          {unreadable > 0 && (
            <p className="text-xs text-destructive">
              Nexara cannot read an entry of {pinned.id}, and cannot save the
              mapping while it is there. Remove it first.
            </p>
          )}
          {entryGone && reread !== "gone" && (
            <p className="text-xs text-destructive">
              This entry changed since it was loaded. Close this and pick it
              again from the list.
            </p>
          )}
          {conflicted ? (
            <p className="text-xs text-destructive">
              Nothing was saved. {CONFLICT_NOTE}{" "}
              {reread === "reading"
                ? "Reloading them…"
                : reread === "reloaded"
                  ? "This dialog now shows them as they are: check it and save again."
                  : reread === "gone"
                    ? `The mapping ${pinned.id} no longer exists.`
                    : "Reloading them failed; close this and try again."}
            </p>
          ) : (
            update.isError && (
              <p className="text-xs text-destructive">
                {describeError(update.error) || "The save request failed."}
              </p>
            )
          )}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={save} disabled={!canSave}>
            {update.isPending ? "Saving…" : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
