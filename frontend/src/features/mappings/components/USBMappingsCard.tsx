import { Fragment, useRef, useState } from "react";
import type { UseQueryResult } from "@tanstack/react-query";
import { CheckCircle2, Pencil, Plus, Replace, Trash2, X } from "lucide-react";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
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
import { useOpenerFocus } from "@/hooks/useOpenerFocus";
import { usePermissions } from "@/hooks/usePermissions";
import { ApiClientError } from "@/lib/api-client";
import { describeError } from "@/lib/api-error";
import { useClusterNodes } from "@/features/clusters/api/cluster-queries";
import {
  useNodeUSBDevices,
  type NodeUSBDevice,
} from "@/features/vms/api/vm-queries";
import {
  useClusterUSBMappings,
  useCreateUSBMapping,
  useDeleteUSBMapping,
  useUpdateUSBMapping,
  useUSBMappingsSettled,
  useUSBMappingUsage,
  type ClusterUSBMapping,
  type USBMappingUsage,
} from "../api/mapping-queries";
import {
  buildUSBMappingEntry,
  characterCount,
  cleanDeviceText,
  findReusableUSBMapping,
  MAPPING_ID_PATTERN,
  parseUSBMappingEntry,
  passthroughCandidates,
  pickedUSBDevice,
  sameUSBMappingEntry,
  suggestMappingName,
  USB_DEVICE_ID_HINT,
  USB_DEVICE_ID_PATTERN,
  usbDeviceId,
  usbMappingEntriesPerNode,
  usbMappingEntryDescription,
  type PickedUSBDevice,
  type USBPickMode,
} from "../lib/usb-mapping";
import { USBDevicePicker } from "./USBDevicePicker";

const selectClass =
  "flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs transition-colors focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-ring";

/** Proxmox's cap on a mapping's description (src/PVE/Mapping/USB.pm). */
const DESCRIPTION_MAX = 4096;

/**
 * Why a save or a removal came back 409. The digest covers the whole
 * usb.cfg, so the change that caused it may be to any USB mapping.
 */
const CONFLICT_NOTE =
  "The cluster's USB mappings changed since this was loaded — a change to any USB mapping counts, not only to this one.";

/** A remove, delete or save refused because usb.cfg changed since its read. */
function isConflict(err: unknown): boolean {
  return err instanceof ApiClientError && err.status === 409;
}

/**
 * What a mapping's row is waiting on: its own write and the re-read of it, or
 * — "waiting" — another mapping's. The digest covers every USB mapping, so
 * any write outdates every row's; each row is held until the re-read lands.
 */
type WorkKind = "removing" | "deleting" | "saving" | "waiting";

/** Why a held row offers nothing, shown in it. */
const workLabel: Record<WorkKind, string> = {
  removing: "Removing the entry…",
  deleting: "Deleting…",
  saving: "Saving…",
  waiting: "Reloading after a change…",
};

/** One node entry of a mapping, as a row shows it. */
interface ShownEntry {
  /** The entry exactly as Proxmox stores it: how an edit finds it again. */
  raw: string;
  node: string;
  id: string;
  path: string;
  description: string;
}

function shownEntries(map: readonly string[]): ShownEntry[] {
  return map
    .map((raw) => ({
      raw,
      ...parseUSBMappingEntry(raw),
      description: cleanDeviceText(usbMappingEntryDescription(raw)),
    }))
    .sort((a, b) => a.node.localeCompare(b.node));
}

/** The dialogs that write the mapping's entries with a PUT. */
type EditTarget =
  | { kind: "add-node"; mapping: ClusterUSBMapping }
  | { kind: "replace"; mapping: ClusterUSBMapping; entry: ShownEntry }
  | { kind: "description"; mapping: ClusterUSBMapping };

interface RemoveTarget {
  mapping: ClusterUSBMapping;
  entry: ShownEntry;
}

interface DeleteTarget {
  mapping: ClusterUSBMapping;
  /** Opened as Remove on the mapping's last entry. */
  lastEntry: boolean;
}

/**
 * The cluster's USB resource mappings, each with its node entries and what
 * Proxmox reports checking each on its own node, and — with manage:cluster —
 * the actions that change them.
 *
 * Every edit is a compare-and-swap against the digest of the listing the
 * operator was looking at: the dialog or confirmation pins the mapping, entries
 * and digest from the same read when it opens, and never refetches under
 * them. The digest covers every USB mapping, so a change to any of them makes
 * the save answer 409; the dialog then re-reads the mappings, shows them, and
 * pins the new read — the one thing allowed to move a pin is a conflict the
 * operator was shown.
 */
export function USBMappingsCard({ clusterId }: { clusterId: string }) {
  const { canManage } = usePermissions();
  const canEdit = canManage("cluster");
  const listQuery = useClusterUSBMappings(clusterId);
  const nodesQuery = useClusterNodes(clusterId);
  const removeEntry = useUpdateUSBMapping(clusterId);
  const deleteMapping = useDeleteUSBMapping(clusterId);
  const settled = useUSBMappingsSettled(clusterId);
  // Where focus goes after a confirmed Remove or Delete — the row's button
  // that opened the confirmation is held (disabled) by then, and focus
  // returned to it would fall to the page — and after an edit or a new
  // mapping whose opener cannot take it back: held after a save, or gone
  // after a conflict's re-read. The card's content, not the table: deleting
  // the last mapping takes the table away.
  const regionRef = useRef<HTMLDivElement>(null);
  const focusFallback = () => regionRef.current;
  const confirmed = useRef(false);

  const [creating, setCreating] = useState(false);
  // The edit dialog is unmounted on every close and mounted on every open, so
  // a pin, the form and a conflict re-read in flight all belong to one
  // opening: a re-read that lands after a Cancel reaches an unmounted dialog,
  // never the next one.
  const [edit, setEdit] = useState<EditTarget | null>(null);
  // Mappings with a Remove or Delete in flight, each its own: the two
  // mutations are shared by every row, so their own pending state would
  // track only the latest call.
  const [working, setWorking] = useState<ReadonlyMap<string, WorkKind>>(
    new Map(),
  );
  const [removeTarget, setRemoveTarget] = useState<RemoveTarget | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget | null>(null);
  const [actionError, setActionError] = useState("");
  const usage = useUSBMappingUsage(clusterId, deleteTarget?.mapping.id ?? null);

  const mappings = [...(listQuery.data ?? [])].sort((a, b) =>
    a.id.localeCompare(b.id),
  );
  const clusterNodes = (nodesQuery.data ?? [])
    .map((n) => n.name)
    .sort((a, b) => a.localeCompare(b));

  const openEdit = (target: EditTarget) => {
    setActionError("");
    setEdit(target);
  };

  const startWork = (id: string, kind: WorkKind) => {
    setWorking((w) => new Map(w).set(id, kind));
  };
  const endWork = (id: string) => {
    setWorking((w) => {
      const next = new Map(w);
      next.delete(id);
      return next;
    });
  };

  // Holds the row until its write has settled AND the listing has been read
  // again: released on the write alone, the row would offer the old listing's
  // entries — a deleted mapping's Delete, an edit pinned to the digest the
  // write just changed — until the re-read landed.
  const holdRow = (id: string, kind: WorkKind, write: Promise<unknown>) => {
    startWork(id, kind);
    void write
      .then(() => settled())
      .finally(() => {
        endWork(id);
      });
  };

  const focusAfterConfirm = (event: Event) => {
    if (!confirmed.current) return;
    confirmed.current = false;
    event.preventDefault();
    regionRef.current?.focus();
  };

  const openRemove = (mapping: ClusterUSBMapping, entry: ShownEntry) => {
    setActionError("");
    if (mapping.map.length === 1) {
      // Proxmox's own UI deletes a mapping rather than empty it: an update
      // that leaves no entry corrupts usb.cfg, and the server refuses one.
      setDeleteTarget({ mapping, lastEntry: true });
    } else {
      setRemoveTarget({ mapping, entry });
    }
  };

  const confirmRemove = ({ mapping, entry }: RemoveTarget) => {
    const index = mapping.map.indexOf(entry.raw);
    if (index < 0) return;
    // The whole list goes back, minus this entry, with the digest of the read
    // the confirmation showed.
    confirmed.current = true;
    holdRow(
      mapping.id,
      "removing",
      removeEntry
        .mutateAsync({
          id: mapping.id,
          map: mapping.map.filter((_, i) => i !== index),
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
    // With the digest of the read the confirmation showed: the entries it
    // listed — "the mapping's only entry", say — are what gets deleted, or
    // nothing is.
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
      <CardHeader className="flex flex-row items-start justify-between gap-4">
        <div className="space-y-1.5">
          <CardTitle>USB Devices</CardTitle>
          <CardDescription>
            A VM passes a host USB device through as usbN: mapping=&lt;name&gt;.
            Each node needs its own entry — a VM will not start on a node the
            mapping has none for. Proxmox lists only the mappings Nexara&apos;s
            API token may see.
          </CardDescription>
          {!canEdit && (
            <p className="text-xs text-muted-foreground">
              Viewing only: changing mappings needs the Manage Cluster
              permission.
            </p>
          )}
        </div>
        {canEdit && (
          <Button
            size="sm"
            onClick={() => {
              setActionError("");
              setCreating(true);
            }}
          >
            <Plus className="mr-2 h-4 w-4" />
            New mapping
          </Button>
        )}
      </CardHeader>
      <CardContent
        ref={regionRef}
        role="region"
        aria-label="USB mappings"
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
            <QueryFailureNote query={listQuery} subject="the USB mappings" />
            <div>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Node</TableHead>
                    <TableHead>Device</TableHead>
                    <TableHead>Port</TableHead>
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
                      clusterNodes={clusterNodes}
                      working={
                        working.get(m.id) ??
                        (working.size > 0 ? "waiting" : undefined)
                      }
                      onAddNode={() => {
                        openEdit({ kind: "add-node", mapping: m });
                      }}
                      onEditDescription={() => {
                        openEdit({ kind: "description", mapping: m });
                      }}
                      onDelete={() => {
                        setActionError("");
                        setDeleteTarget({ mapping: m, lastEntry: false });
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
            subject="the USB mappings"
            empty={
              canEdit
                ? "This cluster has no USB mappings yet. Create one here, or add a host USB device to a VM."
                : "This cluster has no USB mappings yet."
            }
          />
        )}
      </CardContent>

      {creating && (
        <NewMappingDialog
          clusterId={clusterId}
          mappings={listQuery.data ?? []}
          clusterNodes={clusterNodes}
          onClose={() => {
            setCreating(false);
          }}
          fallbackFocus={focusFallback}
        />
      )}
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
        title={({ mapping, entry }) =>
          entry.node === ""
            ? `Remove this entry from ${mapping.id}?`
            : `Remove ${entry.node}'s entry from ${mapping.id}?`
        }
        description={({ mapping, entry }) =>
          entry.node === ""
            ? `The entry names no node, so no VM can use it anywhere. The mapping's other entries stay.`
            : `A VM using ${mapping.id} will not start on ${entry.node} until the mapping has an entry for it again. The mapping's other entries stay.`
        }
        confirmLabel="Remove entry"
        onCloseAutoFocus={focusAfterConfirm}
      />
      <ConfirmDeleteDialog
        target={deleteTarget}
        onClose={() => {
          setDeleteTarget(null);
        }}
        onConfirm={confirmDelete}
        title={({ mapping }) => `Delete USB mapping ${mapping.id}?`}
        description={(t) => (
          <DeleteMappingDescription target={t} usage={usage} />
        )}
        // Held until this opening's usage check has answered, one way or the
        // other: the operator sees which VMs use the mapping — or that they
        // could not be checked — before the delete is on offer. isFetching
        // too, so an answer cached from an earlier opening cannot stand in
        // while the new one is read.
        confirmDisabled={
          usage.isFetching || !(usage.isSuccess || usage.isError)
        }
        confirmLabel="Delete mapping"
        onCloseAutoFocus={focusAfterConfirm}
      />
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Rows
// ---------------------------------------------------------------------------

interface MappingRowsProps {
  mapping: ClusterUSBMapping;
  canEdit: boolean;
  clusterNodes: string[];
  /** A Remove or Delete of this mapping in flight. */
  working: WorkKind | undefined;
  onAddNode: () => void;
  onEditDescription: () => void;
  onDelete: () => void;
  onReplace: (entry: ShownEntry) => void;
  onRemove: (entry: ShownEntry) => void;
}

function MappingRows({
  mapping,
  canEdit,
  clusterNodes,
  working,
  onAddNode,
  onEditDescription,
  onDelete,
  onReplace,
  onRemove,
}: MappingRowsProps) {
  const entries = shownEntries(mapping.map);
  const perNode = usbMappingEntriesPerNode(mapping.map);
  const description = cleanDeviceText(mapping.description);
  // Proxmox states no maximum for an id; Nexara's routes address up to 128
  // characters, the ceiling its create shares.
  const addressable = MAPPING_ID_PATTERN.test(mapping.id);
  const actions = canEdit && addressable;
  // Offered while the cluster has a node without an entry, or while the node
  // list is not known — the dialog says so then.
  const nodeFree =
    clusterNodes.length === 0 || clusterNodes.some((n) => !perNode.has(n));
  const busy = working !== undefined;

  return (
    <Fragment>
      <TableRow className="bg-muted/30 hover:bg-muted/30">
        <TableCell colSpan={4}>
          <span className="font-mono font-medium">{mapping.id}</span>
          {description !== "" && (
            <span className="ml-2 text-muted-foreground">{description}</span>
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
        </TableCell>
        <TableCell className="text-right whitespace-nowrap">
          {actions && (
            <div className="flex justify-end gap-1">
              <Button
                variant="ghost"
                size="sm"
                disabled={!nodeFree || busy}
                onClick={onAddNode}
              >
                <Plus className="mr-1 h-3.5 w-3.5" />
                Add node
              </Button>
              <Button
                variant="ghost"
                size="sm"
                disabled={busy}
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
            No node entries: no VM can use this mapping anywhere.
          </TableCell>
        </TableRow>
      )}
      {entries.map((e, i) => (
        // The raw entry is not unique on its own — a mapping can hold one
        // twice — so its position among the shown rows keys it.
        <TableRow key={`${e.raw}#${String(i)}`}>
          <TableCell className="pl-6">{e.node || "—"}</TableCell>
          <TableCell>
            <span className="font-mono">{e.id || "—"}</span>
            {e.description !== "" && (
              <span className="block text-xs text-muted-foreground">
                {e.description}
              </span>
            )}
          </TableCell>
          <TableCell>
            {e.path !== "" ? (
              <span className="font-mono">{e.path}</span>
            ) : (
              <span className="text-muted-foreground">Any port</span>
            )}
          </TableCell>
          <TableCell>
            <EntryStatus
              mapping={mapping}
              entry={e}
              entriesForNode={perNode.get(e.node) ?? 0}
            />
          </TableCell>
          <TableCell className="text-right whitespace-nowrap">
            {actions && (
              <div className="flex justify-end gap-1">
                {e.node !== "" && (
                  <Button
                    variant="ghost"
                    size="sm"
                    disabled={busy}
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
                  disabled={busy}
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
      ))}
    </Fragment>
  );
}

/**
 * What is known about an entry on its node: OK, the problems Proxmox reported
 * checking it there, or why it was not checked. A node that was not checked is
 * never shown as OK.
 */
function EntryStatus({
  mapping,
  entry,
  entriesForNode,
}: {
  mapping: ClusterUSBMapping;
  entry: ShownEntry;
  entriesForNode: number;
}) {
  if (entry.node === "") {
    return (
      <p className="text-xs text-destructive">
        This entry names no node, so no VM can use it.
      </p>
    );
  }
  const unchecked = mapping.unchecked[entry.node];
  const checks = mapping.node_checks[entry.node];
  const duplicate = entriesForNode > 1;
  return (
    <div className="space-y-0.5">
      {duplicate && (
        <p className="text-xs text-destructive">
          {entry.node} has {entriesForNode} entries: Proxmox refuses to start a
          VM using this mapping there. Remove all but one.
        </p>
      )}
      {unchecked !== undefined ? (
        <p className="text-xs text-muted-foreground">
          Not checked: {cleanDeviceText(unchecked)}
        </p>
      ) : checks === undefined ? (
        <p className="text-xs text-muted-foreground">Not checked.</p>
      ) : checks.length > 0 ? (
        <ul className="space-y-0.5">
          {checks.map((c) => (
            <li
              key={`${c.severity}:${c.message}`}
              className={
                c.severity === "error"
                  ? "text-xs text-destructive"
                  : "text-xs text-amber-700 dark:text-amber-400"
              }
            >
              {cleanDeviceText(c.message)}
            </li>
          ))}
        </ul>
      ) : (
        !duplicate && (
          <span className="inline-flex items-center gap-1 text-xs text-emerald-700 dark:text-emerald-400">
            <CheckCircle2 className="h-3.5 w-3.5" />
            OK
          </span>
        )
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

/**
 * The delete confirmation's text: what goes, and which VMs use the mapping.
 * Inline elements only — the dialog renders this inside a paragraph.
 */
function DeleteMappingDescription({
  target,
  usage,
}: {
  target: DeleteTarget;
  usage: UseQueryResult<USBMappingUsage>;
}) {
  const { mapping, lastEntry } = target;
  return (
    <>
      {lastEntry && (
        <span className="block">
          This is the mapping&apos;s only entry, so removing it deletes the
          mapping.
        </span>
      )}
      <span className="block">
        Deletes the USB mapping {mapping.id} and its{" "}
        {mapping.map.length === 1
          ? "entry"
          : `${String(mapping.map.length)} entries`}{" "}
        from the cluster. Proxmox does not check whether a VM uses it: one that
        does will not start until a mapping named {mapping.id} exists again.
      </span>
      <UsageSummary usage={usage} />
    </>
  );
}

function UsageChecking() {
  return (
    <span className="mt-2 block">
      Checking which VMs use it… The delete is on offer once the check has
      answered.
    </span>
  );
}

function guestName(g: { vmid: number; name: string; node: string }): string {
  const name = cleanDeviceText(g.name);
  return `${String(g.vmid)}${name ? ` (${name})` : ""} on ${g.node}`;
}

function UsageSummary({ usage }: { usage: UseQueryResult<USBMappingUsage> }) {
  if (usage.isFetching) {
    return <UsageChecking />;
  }
  if (usage.isError) {
    const why = describeError(usage.error);
    return (
      <span className="mt-2 block font-medium text-amber-700 dark:text-amber-400">
        Could not check which VMs use it{why ? `: ${why}` : "."} Any VM that
        does will not start after the delete.
      </span>
    );
  }
  if (!usage.isSuccess) {
    return <UsageChecking />;
  }
  const { users, unchecked } = usage.data;
  return (
    <>
      {users.length > 0 ? (
        <span className="mt-2 block font-medium text-destructive">
          {users.length === 1
            ? "1 VM uses it and will not start after the delete:"
            : `${String(users.length)} VMs use it and will not start after the delete:`}
          {users.map((g) => (
            <span key={g.vmid} className="block pl-3 font-normal">
              {guestName(g)} — {(g.keys ?? []).join(", ")}
            </span>
          ))}
        </span>
      ) : (
        <span className="mt-2 block">
          {unchecked.length > 0
            ? "No VM that could be checked uses it."
            : "No VM's current configuration uses it."}
        </span>
      )}
      {unchecked.length > 0 && (
        <span className="mt-2 block text-amber-700 dark:text-amber-400">
          {unchecked.length === 1
            ? "1 VM could not be checked and may use it:"
            : `${String(unchecked.length)} VMs could not be checked and may use it:`}
          {unchecked.map((g) => (
            <span key={g.vmid} className="block pl-3">
              {guestName(g)} — {cleanDeviceText(g.reason ?? "")}
            </span>
          ))}
        </span>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Picking a device on a node
// ---------------------------------------------------------------------------

/**
 * The by-id / by-port choice and the picker for one node's devices, with the
 * refusal for a typed id. Shared by New mapping, Add node and Replace device.
 */
function DevicePickFields({
  idPrefix,
  mode,
  onModeChange,
  devices,
  devicesFailed,
  deviceId,
  onDeviceIdChange,
  port,
  onPortChange,
  disabled,
}: {
  idPrefix: string;
  mode: USBPickMode;
  onModeChange: (mode: USBPickMode) => void;
  devices: NodeUSBDevice[] | undefined;
  devicesFailed: string;
  deviceId: string;
  onDeviceIdChange: (value: string) => void;
  port: string;
  onPortChange: (value: string) => void;
  disabled: boolean;
}) {
  const hasDeviceList = passthroughCandidates(devices).length > 0;
  const typedIdError =
    mode === "device" &&
    !hasDeviceList &&
    deviceId !== "" &&
    !USB_DEVICE_ID_PATTERN.test(deviceId)
      ? USB_DEVICE_ID_HINT
      : "";
  return (
    <>
      <fieldset className="grid gap-1.5" disabled={disabled}>
        <legend className="sr-only">Pass through</legend>
        {(
          [
            ["device", "Host device", "This device, on any port"],
            ["port", "Host USB port", "This device, on this port"],
          ] as const
        ).map(([value, label, hint]) => (
          <label key={value} className="flex items-baseline gap-2 text-sm">
            <input
              type="radio"
              name={`${idPrefix}-mode`}
              value={value}
              checked={mode === value}
              onChange={() => {
                onModeChange(value);
              }}
              className="h-4 w-4 translate-y-0.5 accent-primary"
            />
            <span>
              {label}
              <span className="ml-1.5 text-xs text-muted-foreground">
                {hint}
              </span>
            </span>
          </label>
        ))}
      </fieldset>
      <div className="space-y-1">
        <Label htmlFor={`${idPrefix}-${mode}`} className="text-xs">
          {mode === "device" ? "Device" : "Port"}
        </Label>
        <USBDevicePicker
          id={`${idPrefix}-${mode}`}
          mode={mode}
          devices={devices}
          value={mode === "device" ? deviceId : port}
          onChange={mode === "device" ? onDeviceIdChange : onPortChange}
          disabled={disabled}
          invalid={typedIdError !== ""}
        />
        {typedIdError && (
          <p className="text-xs text-destructive">{typedIdError}</p>
        )}
        {devicesFailed && (
          <p className="text-xs text-muted-foreground">{devicesFailed}</p>
        )}
      </div>
    </>
  );
}

/** Why a node's devices are not listed, for the note under the picker. */
function devicesFailure(query: { isError: boolean; error: unknown }): string {
  if (!query.isError) return "";
  const why = describeError(query.error);
  return `Could not list the node's USB devices${why ? `: ${why}` : "."}`;
}

// ---------------------------------------------------------------------------
// New mapping
// ---------------------------------------------------------------------------

function NewMappingDialog({
  clusterId,
  mappings,
  clusterNodes,
  onClose,
  fallbackFocus,
}: {
  clusterId: string;
  mappings: readonly ClusterUSBMapping[];
  clusterNodes: string[];
  onClose: () => void;
  /** Where focus goes on close if New mapping cannot take it back. */
  fallbackFocus: () => HTMLElement | null;
}) {
  const create = useCreateUSBMapping(clusterId);
  const restoreFocus = useOpenerFocus(true, fallbackFocus);
  const busy = create.isPending;
  const [node, setNode] = useState("");
  const [mode, setMode] = useState<USBPickMode>("device");
  const [deviceId, setDeviceId] = useState("");
  const [port, setPort] = useState("");
  // null until typed, so each follows the pick.
  const [nameInput, setNameInput] = useState<string | null>(null);
  const [descriptionInput, setDescriptionInput] = useState<string | null>(null);
  const devicesQuery = useNodeUSBDevices(clusterId, node);
  const candidates = passthroughCandidates(devicesQuery.data);
  const picked = node
    ? pickedUSBDevice(mode, deviceId, port, candidates)
    : null;

  const taken = new Set(mappings.map((m) => m.id));
  const name =
    nameInput ?? (picked ? suggestMappingName(picked.label, taken) : "");
  const description = descriptionInput ?? picked?.label ?? "";
  let nameError = "";
  if (name !== "" && !MAPPING_ID_PATTERN.test(name)) {
    nameError =
      "Start with a letter; use letters, digits, '_' and '-' (2 to 128 characters).";
  } else if (taken.has(name)) {
    nameError = `A mapping named "${name}" already exists.`;
  }
  const descriptionError =
    characterCount(description) > DESCRIPTION_MAX
      ? `At most ${String(DESCRIPTION_MAX)} characters.`
      : "";
  const existing = picked
    ? findReusableUSBMapping(mappings, node, picked.deviceId, picked.path)
    : undefined;
  const canCreate =
    !busy &&
    picked !== null &&
    name !== "" &&
    nameError === "" &&
    descriptionError === "";

  function clearError() {
    if (!create.isPending) create.reset();
  }

  function handleCreate() {
    if (!canCreate) return;
    create.mutate(
      {
        mapping_id: name,
        node,
        device_id: picked.deviceId,
        ...(picked.path ? { path: picked.path } : {}),
        ...(description.trim() !== ""
          ? { description: description.trim() }
          : {}),
      },
      { onSuccess: onClose },
    );
  }

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent className="max-w-md" onCloseAutoFocus={restoreFocus}>
        <DialogHeader>
          <DialogTitle>New USB mapping</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          <div className="space-y-1">
            <Label htmlFor="new-usb-mapping-node" className="text-xs">
              Node
            </Label>
            <select
              id="new-usb-mapping-node"
              className={selectClass}
              value={node}
              disabled={busy}
              onChange={(e) => {
                setNode(e.target.value);
                setDeviceId("");
                setPort("");
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
            <p className="text-xs text-muted-foreground">
              The first entry. Add one for each node a VM using the mapping may
              run on.
            </p>
          </div>
          {node !== "" && (
            <DevicePickFields
              idPrefix="new-usb-mapping"
              mode={mode}
              onModeChange={(m) => {
                setMode(m);
                clearError();
              }}
              devices={devicesQuery.data}
              devicesFailed={devicesFailure(devicesQuery)}
              deviceId={deviceId}
              onDeviceIdChange={(v) => {
                setDeviceId(v);
                clearError();
              }}
              port={port}
              onPortChange={(v) => {
                setPort(v);
                clearError();
              }}
              disabled={busy}
            />
          )}
          {existing && (
            <p className="text-xs text-amber-700 dark:text-amber-400">
              {existing.id} already passes this device through on {node}.
            </p>
          )}
          <div className="space-y-1">
            <Label htmlFor="new-usb-mapping-name" className="text-xs">
              Name
            </Label>
            <Input
              id="new-usb-mapping-name"
              value={name}
              disabled={busy}
              onChange={(e) => {
                setNameInput(e.target.value);
                clearError();
              }}
              aria-invalid={nameError !== ""}
            />
            {nameError && (
              <p className="text-xs text-destructive">{nameError}</p>
            )}
          </div>
          <div className="space-y-1">
            <Label htmlFor="new-usb-mapping-description" className="text-xs">
              Description
            </Label>
            <Input
              id="new-usb-mapping-description"
              value={description}
              disabled={busy}
              onChange={(e) => {
                setDescriptionInput(e.target.value);
                clearError();
              }}
              aria-invalid={descriptionError !== ""}
            />
            {descriptionError && (
              <p className="text-xs text-destructive">{descriptionError}</p>
            )}
          </div>
          {create.isError && (
            <p className="text-xs text-destructive">
              {describeError(create.error) || "Could not create the mapping."}
            </p>
          )}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={handleCreate} disabled={!canCreate}>
            {busy ? "Creating…" : "Create mapping"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---------------------------------------------------------------------------
// Add node, Replace device, Edit description
// ---------------------------------------------------------------------------

/** Where the re-read after a 409 stands, and what it found. */
type Reread = "none" | "reading" | "reloaded" | "gone" | "failed";

/**
 * The three edits that PUT the mapping's whole entry list. The mapping — its
 * entries and the digest they were read with — is pinned when the dialog
 * opens and saved against as it was: a refetch of the list while the dialog is
 * open does not reach it. Only a 409 this dialog was shown moves the pin, and
 * only to a re-read that succeeded.
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
  /** Hands the card a successful save, so it can hold the row until the
   * listing has been read again. */
  onSaved: (id: string, write: Promise<unknown>) => void;
  onClose: () => void;
  /** Where focus goes on close when the button that opened the dialog
   * cannot take it back: held after a save, gone after a conflict. */
  fallbackFocus: () => HTMLElement | null;
}) {
  const listQuery = useClusterUSBMappings(clusterId);
  const update = useUpdateUSBMapping(clusterId);
  const restoreFocus = useOpenerFocus(true, fallbackFocus);
  const [pinned, setPinned] = useState(target.mapping);
  const [reread, setReread] = useState<Reread>("none");
  // Only the save locks the dialog: closing it then would leave the write to
  // land unseen. The re-read after a conflict just holds Save.
  const busy = update.isPending;

  // Replace keeps its entry by what it says, not by position or spelling:
  // after a re-read the entry is wherever it now sits — the server rewrites
  // every entry's keys on each save — or gone if someone changed it.
  const entryIndex =
    target.kind === "replace"
      ? pinned.map.findIndex((raw) =>
          sameUSBMappingEntry(raw, target.entry.raw),
        )
      : -1;
  const entryGone = target.kind === "replace" && entryIndex < 0;
  const perNode = usbMappingEntriesPerNode(pinned.map);
  const freeNodes = clusterNodes.filter((n) => !perNode.has(n));
  // The server refuses to store a mapping with two entries for one node, and
  // every save sends the whole list: until the extra entry is removed, no
  // edit of this mapping can be saved.
  const doubledNodes = [...perNode]
    .filter(([, count]) => count > 1)
    .map(([node]) => node);

  const [node, setNode] = useState(
    target.kind === "replace" ? target.entry.node : "",
  );
  const [mode, setMode] = useState<USBPickMode>(
    target.kind === "replace" && target.entry.path !== "" ? "port" : "device",
  );
  // null until picked or typed. For Add node it defaults to the device the
  // mapping's other entries pass, when this node lists it — usually the same
  // hardware on each node.
  const [deviceInput, setDeviceInput] = useState<string | null>(null);
  const [port, setPort] = useState("");
  const [description, setDescription] = useState(target.mapping.description);
  // What is sent: Proxmox's section config drops the white space around a
  // description when it reads usb.cfg back, so none is written.
  const trimmedDescription = description.trim();

  const devicesQuery = useNodeUSBDevices(
    clusterId,
    target.kind === "description" ? "" : node,
  );
  const candidates = passthroughCandidates(devicesQuery.data);
  const sharedIds = [
    ...new Set(
      pinned.map
        .map((raw) => parseUSBMappingEntry(raw).id.toLowerCase())
        .filter((id) => id !== ""),
    ),
  ];
  const [sharedId] = sharedIds;
  const defaultDevice =
    target.kind === "add-node" &&
    sharedIds.length === 1 &&
    sharedId !== undefined &&
    candidates.some((c) => usbDeviceId(c) === sharedId)
      ? sharedId
      : "";
  const deviceId = deviceInput ?? defaultDevice;
  const picked: PickedUSBDevice | null =
    target.kind === "description" || node === ""
      ? null
      : pickedUSBDevice(mode, deviceId, port, candidates);

  // Add node: the node must still be free in the pinned mapping — a re-read
  // can show someone gave it an entry meanwhile.
  const nodeStillFree =
    target.kind !== "add-node" || (node !== "" && !perNode.has(node));

  let nextMap: string[] | null = null;
  let unchanged = false;
  if (target.kind === "add-node" && picked && nodeStillFree) {
    nextMap = [
      ...pinned.map,
      buildUSBMappingEntry({ node, id: picked.deviceId, path: picked.path }),
    ];
  } else if (target.kind === "replace" && picked && !entryGone) {
    const current = parseUSBMappingEntry(pinned.map[entryIndex] ?? "");
    // Compared exactly: the pick is lowercase, and an entry stored with an
    // uppercase id is broken — Proxmox compares it against the node's
    // lowercase sysfs hex — so picking the same device repairs it.
    unchanged = current.id === picked.deviceId && current.path === picked.path;
    nextMap = pinned.map.map((raw, i) =>
      i === entryIndex
        ? buildUSBMappingEntry({
            node: target.entry.node,
            id: picked.deviceId,
            path: picked.path,
            // The entry's own description survives the new device.
            description: usbMappingEntryDescription(raw),
          })
        : raw,
    );
  } else if (target.kind === "description") {
    unchanged = trimmedDescription === pinned.description.trim();
    nextMap = pinned.map;
  }
  const descriptionError =
    target.kind === "description" &&
    characterCount(description) > DESCRIPTION_MAX
      ? `At most ${String(DESCRIPTION_MAX)} characters.`
      : "";
  const canSave =
    !busy &&
    reread !== "reading" &&
    reread !== "gone" &&
    doubledNodes.length === 0 &&
    nextMap !== null &&
    !unchanged &&
    descriptionError === "";

  function clearError() {
    if (!update.isPending) update.reset();
  }

  function save() {
    if (!canSave || nextMap === null) return;
    const saving = pinned;
    update.mutate(
      {
        id: saving.id,
        map: nextMap,
        digest: saving.digest,
        ...(target.kind === "description"
          ? { description: trimmedDescription }
          : {}),
      },
      {
        onSuccess: () => {
          onSaved(saving.id, Promise.resolve());
          onClose();
        },
        onError: (err) => {
          if (!(err instanceof ApiClientError) || err.status !== 409) return;
          // Re-read, show it, and pin it: without this the pin would
          // conflict forever. Not a retry — the operator checks the new
          // state and saves again, or not.
          setReread("reading");
          // Joins the re-read the mutation already started (refreshAfterWrite)
          // rather than cancelling it for a second one: that one began after
          // the 409, so it is the read to pin.
          void listQuery.refetch({ cancelRefetch: false }).then((res) => {
            // isSuccess, not res.data: a failed refetch keeps the last good
            // data, which would pin a digest nobody was shown.
            if (!res.isSuccess) {
              setReread("failed");
              return;
            }
            const fresh = res.data.find((m) => m.id === saving.id);
            if (fresh === undefined) {
              setReread("gone");
              return;
            }
            setPinned(fresh);
            setReread("reloaded");
          });
        },
      },
    );
  }

  const conflicted =
    update.error instanceof ApiClientError && update.error.status === 409;
  const title =
    target.kind === "add-node"
      ? `Add a node to ${pinned.id}`
      : target.kind === "replace"
        ? `Replace the device on ${target.entry.node}`
        : `Edit the description of ${pinned.id}`;

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <DialogContent className="max-w-md" onCloseAutoFocus={restoreFocus}>
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-3">
          {target.kind === "add-node" && (
            <div className="space-y-1">
              <Label htmlFor="edit-usb-mapping-node" className="text-xs">
                Node
              </Label>
              {clusterNodes.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  The cluster&apos;s nodes are not available: still loading, or
                  they could not be read.
                </p>
              ) : freeNodes.length === 0 ? (
                <p className="text-xs text-muted-foreground">
                  Every node of this cluster has an entry already.
                </p>
              ) : (
                <select
                  id="edit-usb-mapping-node"
                  className={selectClass}
                  value={nodeStillFree ? node : ""}
                  disabled={busy}
                  onChange={(e) => {
                    setNode(e.target.value);
                    setDeviceInput(null);
                    setPort("");
                    clearError();
                  }}
                >
                  <option value="">Select a node...</option>
                  {freeNodes.map((n) => (
                    <option key={n} value={n}>
                      {n}
                    </option>
                  ))}
                </select>
              )}
            </div>
          )}
          {target.kind === "replace" && (
            <p className="text-sm text-muted-foreground">
              Now:{" "}
              <span className="font-mono">
                {target.entry.id}
                {target.entry.path
                  ? ` on port ${target.entry.path}`
                  : " on any port"}
              </span>
              . The entry keeps its node; a VM using {pinned.id} passes the new
              device through on {target.entry.node}.
            </p>
          )}
          {target.kind !== "description" && node !== "" && (
            <DevicePickFields
              idPrefix="edit-usb-mapping"
              mode={mode}
              onModeChange={(m) => {
                setMode(m);
                clearError();
              }}
              devices={devicesQuery.data}
              devicesFailed={devicesFailure(devicesQuery)}
              deviceId={deviceId}
              onDeviceIdChange={(v) => {
                setDeviceInput(v);
                clearError();
              }}
              port={port}
              onPortChange={(v) => {
                setPort(v);
                clearError();
              }}
              disabled={busy}
            />
          )}
          {target.kind === "replace" && unchanged && (
            <p className="text-xs text-muted-foreground">
              That is the device the entry passes now.
            </p>
          )}
          {target.kind === "description" && (
            <div className="space-y-1">
              <Label htmlFor="edit-usb-mapping-description" className="text-xs">
                Description
              </Label>
              <Input
                id="edit-usb-mapping-description"
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
          {doubledNodes.length > 0 && (
            <p className="text-xs text-destructive">
              {pinned.id} has more than one entry for {doubledNodes.join(", ")},
              which Proxmox refuses to start a VM with. Remove the extra entry
              first: until then no change to this mapping can be saved.
            </p>
          )}
          {entryGone && reread !== "gone" && (
            <p className="text-xs text-destructive">
              This entry changed since it was loaded. Close this and pick it
              again from the list.
            </p>
          )}
          {target.kind === "add-node" &&
            node !== "" &&
            !nodeStillFree &&
            reread === "reloaded" && (
              <p className="text-xs text-destructive">
                {node} has an entry now. Pick another node, or close this.
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
